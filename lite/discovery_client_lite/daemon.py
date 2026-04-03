"""Discovery daemon, AEN listener, and systemd notification."""

import json
import logging
import os
import random
import select
import signal
import socket
import time
from pathlib import Path
from threading import Event, Thread
from typing import Dict, List, Optional

from .models import Endpoint, CachedReferral, ClusterState
from .metrics import metrics, start_metrics_server
from .nvme import (
    NvmeCli,
    DISCOVERY_SUBNQN,
    DISCOVERY_CONF,
    get_connected_controllers,
    get_host_id,
    get_connected_hostids,
    set_ctrl_loss_tmo_sysfs,
)
from .config import (
    read_discovery_conf,
    load_referral_cache,
    save_referral_cache,
    extract_referrals,
    extract_io_targets,
)

log = logging.getLogger('discovery-client-lite')

NETLINK_KOBJECT_UEVENT = 15


def sd_notify(state: str):
    """Send a systemd notification. No-op if not running under systemd."""
    addr = os.environ.get('NOTIFY_SOCKET')
    if not addr:
        return
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM) as sock:
            if addr.startswith('@'):
                addr = '\0' + addr[1:]
            sock.sendto(state.encode(), addr)
    except OSError as e:
        log.debug('sd_notify failed: %s', e)


def start_aen_listener(wake_event: Event, running_check) -> Optional[Thread]:
    """Start a daemon thread that listens for NVMe AEN kernel uevents.

    When the kernel receives an AEN on a persistent discovery connection,
    it emits a KOBJ_CHANGE uevent with NVME_AEN=<result_code>. This
    listener monitors those via a netlink socket and signals the main loop
    to run a poll cycle immediately.

    Uses epoll for efficient blocking instead of timeout-based polling.
    A wake pipe allows clean shutdown without waiting for a timeout.

    Falls back gracefully if the netlink socket cannot be opened (e.g.,
    missing permissions or unsupported platform).
    """
    try:
        sock = socket.socket(socket.AF_NETLINK, socket.SOCK_DGRAM, NETLINK_KOBJECT_UEVENT)
        sock.bind((os.getpid(), 1))  # multicast group 1 = kernel events
        sock.setblocking(False)
    except OSError as e:
        log.warning('Cannot open netlink socket for AEN monitoring: %s', e)
        log.info('Falling back to poll-only mode')
        return None

    # Wake pipe: write end is used to unblock epoll on shutdown
    wake_r, wake_w = os.pipe()
    os.set_blocking(wake_r, False)

    ep = select.epoll()
    ep.register(sock.fileno(), select.EPOLLIN)
    ep.register(wake_r, select.EPOLLIN)

    def _listener():
        try:
            while running_check():
                try:
                    events = ep.poll(timeout=5.0)
                except OSError:
                    if running_check():
                        break
                    return

                for fd, _ in events:
                    if fd == wake_r:
                        return
                    if fd == sock.fileno():
                        try:
                            data = sock.recv(4096)
                        except OSError as e:
                            if running_check():
                                log.warning('Netlink recv error: %s', e)
                            return

                        if b'NVME_AEN=' not in data:
                            continue

                        for part in data.split(b'\0'):
                            if part.startswith(b'NVME_AEN='):
                                log.info('Kernel AEN uevent: %s',
                                         part.decode('ascii', errors='replace'))
                                break

                        metrics.aen_sent_total += 1
                        wake_event.set()
        finally:
            ep.close()
            sock.close()
            os.close(wake_r)
            os.close(wake_w)

    thread = Thread(target=_listener, daemon=True, name='aen-listener')
    thread.start()
    log.info('AEN listener started (epoll-based netlink monitoring)')
    return thread


CONTROL_SOCKET_PATH = '/run/discovery-client-lite.sock'


def start_control_listener(daemon, running_check) -> Optional[Thread]:
    """Start a daemon thread that accepts commands on a Unix domain socket.

    Enables runtime control via the CLI, e.g.:
        discovery-client-lite set --ctrl-loss-tmo 1
    """
    sock_path = CONTROL_SOCKET_PATH
    try:
        os.unlink(sock_path)
    except OSError:
        pass

    try:
        srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        srv.bind(sock_path)
        os.chmod(sock_path, 0o660)
        srv.listen(2)
        srv.settimeout(1.0)
    except OSError as e:
        log.warning('Cannot open control socket %s: %s', sock_path, e)
        return None

    def _handle(conn):
        try:
            data = conn.recv(4096)
            if not data:
                return
            request = json.loads(data.decode())
            response = daemon.handle_control_command(request)
            conn.sendall(json.dumps(response).encode() + b'\n')
        except Exception as e:
            try:
                conn.sendall(json.dumps({'ok': False, 'error': str(e)}).encode() + b'\n')
            except OSError:
                pass
        finally:
            conn.close()

    def _listener():
        while running_check():
            try:
                conn, _ = srv.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            _handle(conn)
        srv.close()
        try:
            os.unlink(sock_path)
        except OSError:
            pass

    thread = Thread(target=_listener, daemon=True, name='control-listener')
    thread.start()
    log.info('Control socket listening on %s', sock_path)
    return thread


class DiscoveryDaemon:
    """Discovery daemon using nvme discover + nvme connect.

    Each poll cycle (or AEN trigger), the daemon reads discovery service
    endpoints from /etc/nvme/discovery.conf, runs 'nvme discover' to
    find IO targets, and 'nvme connect' for each.  The daemon manages
    reconnection; the kernel manages the individual TCP connections.
    """

    def __init__(
        self,
        cache_file: str,
        poll_interval: int,
        ctrl_loss_tmo: int,
        discovery_port: str = '8009',
        kato: int = 10,
        nr_io_queues: int = 0,
        referral_ttl: int = 3600,
        metrics_port: int = 0,
        dhchap_secret: str = '',
        dhchap_ctrl_secret: str = '',
        nvme_host_id_path: str = '/etc/nvme/hostid',
    ):
        self.cache_file = cache_file
        self.poll_interval = poll_interval
        self.ctrl_loss_tmo = ctrl_loss_tmo
        self.discovery_port = discovery_port
        self.kato = kato
        self.nr_io_queues = nr_io_queues
        self.referral_ttl = referral_ttl
        self.metrics_port = metrics_port
        self.dhchap_secret = dhchap_secret
        self.dhchap_ctrl_secret = dhchap_ctrl_secret
        self.nvme_host_id_path = nvme_host_id_path
        self.running = True
        self._aen_event = Event()
        self.host_id = get_host_id(nvme_host_id_path)

        # Per-cluster state: keyed by cluster subnqn (or "" for default)
        self.clusters: Dict[str, ClusterState] = {}

        # Referral cache (shared across clusters)
        self.referrals: List[CachedReferral] = []

    def handle_control_command(self, request: dict) -> dict:
        """Process a command received on the control socket."""
        cmd = request.get('command')
        if cmd == 'set':
            return self._handle_set(request)
        return {'ok': False, 'error': 'unknown command: %s' % cmd}

    def _handle_set(self, request: dict) -> dict:
        key = request.get('key')
        value = request.get('value')
        if key == 'ctrl_loss_tmo':
            try:
                tmo = int(value)
            except (TypeError, ValueError):
                return {'ok': False, 'error': 'ctrl_loss_tmo must be an integer'}
            self.ctrl_loss_tmo = tmo
            count = set_ctrl_loss_tmo_sysfs(tmo)
            log.info('ctrl_loss_tmo set to %d (updated %d controllers via sysfs)', tmo, count)
            return {'ok': True, 'message': 'ctrl_loss_tmo=%d on %d controllers' % (tmo, count)}
        return {'ok': False, 'error': 'unknown key: %s' % key}

    def shutdown(self, signum, _frame=None):
        """Signal handler for graceful shutdown."""
        log.info('Received signal %d, shutting down...', signum)
        self.running = False
        self._aen_event.set()

    def get_cluster(self, cluster_key: str) -> ClusterState:
        """Get or create cluster state keyed by (hostnqn, subnqn) string."""
        if cluster_key not in self.clusters:
            self.clusters[cluster_key] = ClusterState(subnqn=cluster_key)
        return self.clusters[cluster_key]

    def _get_effective_hostid(self, endpoint: Endpoint, hostid_map: dict) -> str:
        """Return the effective hostid for an endpoint.

        If controllers are already connected with the same hostnqn but a
        different hostid, adopt the existing hostid rather than disconnecting
        them. This matches the Go discovery-client's MaybeUpdateHostIDs
        behavior: the kernel rejects new controllers for the same hostnqn
        with a different hostid, so we must use what's already established.

        Args:
            endpoint: The endpoint being configured.
            hostid_map: Pre-cached hostnqn -> hostid map from sysfs.
        """
        configured = endpoint.hostid or self.host_id
        if not endpoint.hostnqn:
            return configured

        existing = hostid_map.get(endpoint.hostnqn, '')
        if existing and existing != configured:
            log.info(
                'Overriding hostid for %s: configured=%s effective=%s',
                endpoint.hostnqn,
                configured,
                existing,
            )
            return existing
        return configured

    def discover_and_connect(self, endpoint: Endpoint, connected: set,
                             hostid_map: dict) -> bool:
        """Discover targets via an endpoint, connect only new ones.

        Uses 'nvme discover' to fetch the log page, then 'nvme connect'
        for each IO target not already in the ``connected`` set.

        Args:
            endpoint: Discovery endpoint to query.
            connected: Set of (traddr, trsvcid, subnqn) tuples already
                       connected in sysfs.  Targets in this set are skipped.
            hostid_map: Pre-cached hostnqn -> hostid map from sysfs.
        """
        metrics.connect_attempts_total += 1
        tmo = endpoint.ctrl_loss_tmo or self.ctrl_loss_tmo
        effective_hostid = self._get_effective_hostid(endpoint, hostid_map)

        log.info('Discovering targets via %s:%s', endpoint.traddr, endpoint.port)
        output = NvmeCli.discover(
            traddr=endpoint.traddr,
            trsvcid=endpoint.port,
            hostnqn=endpoint.hostnqn,
            hostid=effective_hostid,
        )

        if output is None:
            metrics.connect_failures_total += 1
            log.warning('discover failed for %s:%s', endpoint.traddr, endpoint.port)
            return False

        # Connect only to IO targets not already connected
        io_targets = extract_io_targets(output)

        # Warn if config subnqn doesn't match what the discovery service returns
        if endpoint.subnqn and io_targets:
            mismatched = {t.subnqn for t in io_targets if t.subnqn != endpoint.subnqn}
            if mismatched:
                log.warning(
                    'Config subnqn %s does not match discovered subnqns %s '
                    'from %s:%s — config may be stale',
                    endpoint.subnqn, mismatched, endpoint.traddr, endpoint.port,
                )

        new_targets = [t for t in io_targets
                       if (t.traddr, t.trsvcid, t.subnqn) not in connected]
        skipped = len(io_targets) - len(new_targets)
        new_connected = 0
        for target in new_targets:
            result = NvmeCli.connect(
                traddr=target.traddr,
                trsvcid=target.trsvcid,
                hostnqn=endpoint.hostnqn,
                hostid=effective_hostid,
                nqn=target.subnqn,
                ctrl_loss_tmo=tmo,
                dhchap_secret=endpoint.secret or self.dhchap_secret,
                dhchap_ctrl_secret=endpoint.ctrl_secret or self.dhchap_ctrl_secret,
            )
            if result is not None:
                new_connected += 1
            else:
                log.debug('connect to %s:%s (%s) failed',
                          target.traddr, target.trsvcid, target.subnqn)

        cluster_key = '%s/%s' % (endpoint.hostnqn, endpoint.subnqn)
        cluster = self.get_cluster(cluster_key)
        cluster.active_endpoint = (endpoint.traddr, endpoint.port)
        log.info(
            'discover via %s:%s: %d IO targets (%d new, %d already connected) (cluster: %s)',
            endpoint.traddr,
            endpoint.port,
            len(io_targets),
            new_connected,
            skipped,
            cluster_key or 'default',
        )

        # Extract and cache referrals
        new_refs = extract_referrals(output)
        existing_keys = {r.to_key() for r in self.referrals}
        added = False
        for ref in new_refs:
            if ref.to_key() not in existing_keys:
                log.info('New referral: %s at %s:%s', ref.subnqn, ref.traddr, ref.port)
                self.referrals.append(ref)
                added = True
        if added:
            metrics.targets_map_id += 1
            save_referral_cache(self.cache_file, self.referrals)

        return True

    def expire_referrals(self):
        """Remove referrals older than referral_ttl."""
        now = time.time()
        before = len(self.referrals)
        self.referrals = [r for r in self.referrals if now - r.discovered_at < self.referral_ttl]
        expired = before - len(self.referrals)
        if expired > 0:
            log.info('Expired %d stale referrals', expired)
            save_referral_cache(self.cache_file, self.referrals)

    def poll_cycle(self):
        """Single poll cycle: discover targets, connect to new ones.

        Uses a single-active-endpoint model per cluster: for each cluster
        (identified by subnqn), try the previously active endpoint first.
        If it succeeds, move on.  If it fails, try remaining endpoints in
        shuffled order until one succeeds.  Only new IO targets (not already
        connected in sysfs) trigger an ``nvme connect`` subprocess call.
        """
        all_controllers = get_connected_controllers()

        # Build set of already-connected IO targets for skip logic
        connected = set()
        for c in all_controllers:
            if c.subnqn != DISCOVERY_SUBNQN:
                connected.add((c.traddr, c.port, c.subnqn))

        # Cache hostid map once per cycle (avoids repeated sysfs scans)
        hostid_map = get_connected_hostids()

        # Metrics
        metrics.tcp_targets_total = len(connected)
        hostnqn_counts = {}
        for c in all_controllers:
            if c.subnqn != DISCOVERY_SUBNQN and c.hostnqn:
                hostnqn_counts[c.hostnqn] = hostnqn_counts.get(c.hostnqn, 0) + 1
        metrics.targets_per_hostnqn = hostnqn_counts

        # Read endpoints
        endpoints = read_discovery_conf()
        metrics.tcp_queues_total = len(endpoints)

        # Add referrals as additional endpoints (inherit creds from donor)
        endpoints_by_hostnqn = {}
        for ep in endpoints:
            endpoints_by_hostnqn.setdefault(ep.hostnqn, []).append(ep)
        for ref in self.referrals:
            # Find a donor to inherit hostnqn and credentials from
            donor = None
            for hostnqn_eps in endpoints_by_hostnqn.values():
                donor = hostnqn_eps[0]
                break
            if donor:
                endpoints.append(Endpoint(
                    traddr=ref.traddr, port=ref.port,
                    hostnqn=donor.hostnqn, subnqn=ref.subnqn,
                    secret=donor.secret, ctrl_secret=donor.ctrl_secret,
                    hostid=donor.hostid, ctrl_loss_tmo=donor.ctrl_loss_tmo,
                ))

        # Deduplicate by (traddr, port)
        seen = set()
        unique = []
        for ep in endpoints:
            key = (ep.traddr, ep.port)
            if key not in seen:
                seen.add(key)
                unique.append(ep)

        # Group endpoints by (hostnqn, subnqn) — same client + same cluster
        # are redundant; same client + different cluster are independent.
        clusters: Dict[str, List[Endpoint]] = {}
        for ep in unique:
            key = '%s/%s' % (ep.hostnqn, ep.subnqn)
            clusters.setdefault(key, []).append(ep)

        # Discover through one endpoint per cluster (single-active model)
        for cluster_key, cluster_eps in clusters.items():
            cluster = self.get_cluster(cluster_key)
            active = cluster.active_endpoint

            # Try active endpoint first if it's still in the list
            ordered = list(cluster_eps)
            if active:
                active_ep = None
                rest = []
                for ep in ordered:
                    if (ep.traddr, ep.port) == active:
                        active_ep = ep
                    else:
                        rest.append(ep)
                if active_ep:
                    random.shuffle(rest)
                    ordered = [active_ep] + rest
                else:
                    random.shuffle(ordered)
            else:
                random.shuffle(ordered)

            success = False
            for ep in ordered:
                if self.discover_and_connect(ep, connected, hostid_map):
                    success = True
                    break
                log.info('Cluster %s: endpoint %s:%s failed, trying next',
                         cluster_key, ep.traddr, ep.port)

            if not success:
                log.warning('Cluster %s: all %d endpoints unreachable',
                            cluster_key, len(cluster_eps))

        self.expire_referrals()
        metrics.clusters_tracked = len(clusters)

    def run(self, aen_enabled: bool = True):
        """Main daemon loop."""
        signal.signal(signal.SIGTERM, self.shutdown)
        signal.signal(signal.SIGINT, self.shutdown)

        log.info('discovery-client-lite starting')
        log.info('Discovery conf: %s', DISCOVERY_CONF)
        log.info('Poll interval: %ds', self.poll_interval)
        log.info('Ctrl loss timeout: %ds', self.ctrl_loss_tmo)
        log.info('Referral TTL: %ds', self.referral_ttl)
        if self.host_id:
            log.info('Host ID: %s', self.host_id)
        if self.nr_io_queues:
            log.info('Max IO queues: %d', self.nr_io_queues)

        # Start metrics server
        if self.metrics_port:
            start_metrics_server(self.metrics_port)

        # Start AEN listener for near-instant topology change detection
        if aen_enabled:
            start_aen_listener(self._aen_event, lambda: self.running)
        else:
            log.info('AEN listener disabled, using poll-only mode')

        # Start control socket for runtime commands
        start_control_listener(self, lambda: self.running)

        # Ensure discovery.conf parent dir exists
        Path(DISCOVERY_CONF).parent.mkdir(parents=True, exist_ok=True)
        self.referrals = load_referral_cache(self.cache_file)

        # Initial connection attempt
        start = time.monotonic()
        try:
            self.poll_cycle()
            metrics.poll_cycles_total += 1
        except Exception:
            log.exception('Error in initial poll cycle')
            metrics.poll_cycle_errors_total += 1
        metrics.poll_cycle_duration.observe(time.monotonic() - start)

        # Notify systemd we're ready
        sd_notify('READY=1')

        while self.running:
            triggered = self._aen_event.wait(timeout=self.poll_interval)
            self._aen_event.clear()

            if not self.running:
                break

            if triggered:
                log.debug('Poll cycle triggered by AEN event')

            start = time.monotonic()
            try:
                self.poll_cycle()
                metrics.poll_cycles_total += 1
            except Exception:
                log.exception('Error in poll cycle')
                metrics.poll_cycle_errors_total += 1
            metrics.poll_cycle_duration.observe(time.monotonic() - start)

        # Disconnect discovery controllers on shutdown.  Since we don't use
        # --persistent, there's no kernel-level persistent state to clean up.
        # IO connections are left intact — the kernel manages them independently
        # and they survive daemon restarts.
        log.info('Disconnecting discovery controllers on shutdown')
        NvmeCli.disconnect_by_nqn(DISCOVERY_SUBNQN)

        sd_notify('STOPPING=1')
        log.info('discovery-client-lite stopped')
