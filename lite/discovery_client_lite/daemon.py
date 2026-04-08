"""Discovery daemon, AEN listener, and systemd notification."""

import logging
import os
import random
import signal
import socket
import time
from pathlib import Path
from threading import Event, Thread
from typing import Dict, List, Optional, Tuple

from .models import Endpoint, CachedReferral, ClusterState, RECONNECT_GRACE_MULTIPLIER
from .metrics import metrics, start_metrics_server
from .nvme import NvmeCli, DISCOVERY_SUBNQN, get_connected_controllers, get_discovery_controllers, get_host_id, get_connected_hostids
from .config import read_config_dir, auto_detect_endpoints, load_referral_cache, save_referral_cache, extract_referrals

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

    Falls back gracefully if the netlink socket cannot be opened (e.g.,
    missing permissions or unsupported platform).
    """
    try:
        sock = socket.socket(socket.AF_NETLINK, socket.SOCK_DGRAM, NETLINK_KOBJECT_UEVENT)
        sock.bind((os.getpid(), 1))  # multicast group 1 = kernel events
        sock.settimeout(1.0)
    except OSError as e:
        log.warning('Cannot open netlink socket for AEN monitoring: %s', e)
        log.info('Falling back to poll-only mode')
        return None

    def _listener():
        while running_check():
            try:
                data = sock.recv(4096)
            except socket.timeout:
                continue
            except OSError as e:
                if running_check():
                    log.warning('Netlink recv error: %s', e)
                break

            if b'NVME_AEN=' not in data:
                continue

            # Extract the AEN value for logging
            for part in data.split(b'\0'):
                if part.startswith(b'NVME_AEN='):
                    log.info('Kernel AEN uevent: %s', part.decode('ascii', errors='replace'))
                    break

            metrics.aen_sent_total += 1
            wake_event.set()

        sock.close()

    thread = Thread(target=_listener, daemon=True, name='aen-listener')
    thread.start()
    log.info('AEN listener started (netlink uevent monitoring)')
    return thread


class DiscoveryDaemon:
    """Discovery daemon using nvme connect-all --persistent with failover.

    The kernel manages persistent discovery controllers and reacts to AEN
    for topology changes. The daemon's role is config management, endpoint
    failover, and adopting existing persistent controllers on restart to
    prevent duplicate connections.
    """

    def __init__(
        self,
        config_dir: str,
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
        auto_detect_enabled: bool = True,
        auto_detect_filename: str = 'detected-io-controllers',
    ):
        self.config_dir = config_dir
        self.cache_file = cache_file
        self.poll_interval = poll_interval
        self.ctrl_loss_tmo = ctrl_loss_tmo
        self.reconnect_delay = 10  # matches the value passed to nvme connect-all
        self.reconnect_grace = self.reconnect_delay * RECONNECT_GRACE_MULTIPLIER
        self.discovery_port = discovery_port
        self.kato = kato
        self.nr_io_queues = nr_io_queues
        self.referral_ttl = referral_ttl
        self.metrics_port = metrics_port
        self.dhchap_secret = dhchap_secret
        self.dhchap_ctrl_secret = dhchap_ctrl_secret
        self.nvme_host_id_path = nvme_host_id_path
        self.auto_detect_enabled = auto_detect_enabled
        self.auto_detect_filename = auto_detect_filename
        self.running = True
        self._aen_event = Event()
        self.host_id = get_host_id(nvme_host_id_path)

        # Per-cluster state: keyed by cluster subnqn (or "" for default)
        self.clusters: Dict[str, ClusterState] = {}

        # Referral cache (shared across clusters)
        self.referrals: List[CachedReferral] = []

        # Track config files → endpoints for disconnect-on-removal
        self.prev_config_files: Dict[str, List[Endpoint]] = {}

    def shutdown(self, signum, frame):
        """Signal handler for graceful shutdown."""
        log.info('Received signal %d, shutting down...', signum)
        self.running = False
        self._aen_event.set()

    def get_cluster(self, subnqn: str) -> ClusterState:
        """Get or create cluster state for a subnqn."""
        if subnqn not in self.clusters:
            self.clusters[subnqn] = ClusterState(subnqn=subnqn)
        return self.clusters[subnqn]

    def _get_effective_hostid(self, endpoint: Endpoint) -> str:
        """Return the effective hostid for an endpoint.

        If controllers are already connected with the same hostnqn but a
        different hostid, adopt the existing hostid rather than disconnecting
        them. This matches the Go discovery-client's MaybeUpdateHostIDs
        behavior: the kernel rejects new controllers for the same hostnqn
        with a different hostid, so we must use what's already established.
        """
        configured = endpoint.hostid or self.host_id
        if not endpoint.hostnqn:
            return configured

        connected = get_connected_hostids()
        existing = connected.get(endpoint.hostnqn, '')
        if existing and existing != configured:
            log.info(
                'Overriding hostid for %s: configured=%s effective=%s',
                endpoint.hostnqn,
                configured,
                existing,
            )
            return existing
        return configured

    def try_connect_all(self, endpoint: Endpoint) -> bool:
        """Try connect-all against an endpoint. Returns True on success."""
        metrics.connect_attempts_total += 1
        tmo = endpoint.ctrl_loss_tmo or self.ctrl_loss_tmo
        effective_hostid = self._get_effective_hostid(endpoint)

        log.info('Trying connect-all to %s:%s', endpoint.traddr, endpoint.port)
        success, output = NvmeCli.connect_all(
            traddr=endpoint.traddr,
            port=endpoint.port,
            hostnqn=endpoint.hostnqn,
            hostid=effective_hostid,
            secret=endpoint.secret or self.dhchap_secret,
            ctrl_secret=endpoint.ctrl_secret or self.dhchap_ctrl_secret,
            ctrl_loss_tmo=tmo,
            kato=self.kato,
            nr_io_queues=self.nr_io_queues,
        )

        if not success:
            metrics.connect_failures_total += 1
            log.warning('connect-all failed for %s:%s', endpoint.traddr, endpoint.port)
            return False

        cluster = self.get_cluster(endpoint.subnqn)
        cluster.active_endpoint = (endpoint.traddr, endpoint.port)
        log.info(
            'connect-all succeeded for %s:%s (cluster: %s)',
            endpoint.traddr,
            endpoint.port,
            endpoint.subnqn or 'default',
        )

        # Extract and cache referrals
        if output:
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

    def handle_config_removal(self, current_files: Dict[str, List[Endpoint]]):
        """Disconnect controllers whose config file was removed."""
        removed_files = set(self.prev_config_files.keys()) - set(current_files.keys())
        if not removed_files:
            return

        controllers = get_connected_controllers()
        for filename in removed_files:
            log.info('Config file removed: %s, disconnecting associated controllers', filename)
            for ep in self.prev_config_files[filename]:
                for ctrl in controllers:
                    if (
                        ctrl.traddr == ep.traddr
                        and ctrl.port == ep.port
                        and (not ep.hostnqn or ctrl.hostnqn == ep.hostnqn)
                    ):
                        log.info(
                            'Disconnecting %s (%s at %s:%s)',
                            ctrl.device,
                            ctrl.subnqn,
                            ctrl.traddr,
                            ctrl.port,
                        )
                        NvmeCli.disconnect(ctrl.device)

                # Clear cluster active endpoint if it matches
                cluster = self.get_cluster(ep.subnqn)
                if cluster.active_endpoint == (ep.traddr, ep.port):
                    cluster.active_endpoint = None

    def poll_cycle(self):
        """Single poll cycle: check health, failover, handle config changes."""
        live_discovery = get_discovery_controllers()
        all_controllers = get_connected_controllers()

        # Update metrics
        io_count = sum(1 for c in all_controllers if c.subnqn != DISCOVERY_SUBNQN)
        metrics.tcp_targets_total = io_count
        metrics.tcp_server_serving_states = len(live_discovery)

        # Per-hostnqn target counts
        hostnqn_counts: Dict[str, int] = {}
        for c in all_controllers:
            if c.subnqn != DISCOVERY_SUBNQN and c.hostnqn:
                hostnqn_counts[c.hostnqn] = hostnqn_counts.get(c.hostnqn, 0) + 1
        metrics.targets_per_hostnqn = hostnqn_counts

        # Read config and handle removals
        current_config = read_config_dir(self.config_dir)
        self.handle_config_removal(current_config)
        self.prev_config_files = current_config

        # Flatten all endpoints
        all_endpoints = [ep for eps in current_config.values() for ep in eps]
        metrics.tcp_queues_total = len(all_endpoints)

        # Group endpoints by cluster (subnqn)
        endpoints_by_cluster: Dict[str, List[Endpoint]] = {}
        for ep in all_endpoints:
            endpoints_by_cluster.setdefault(ep.subnqn, []).append(ep)

        # Add referrals as fallback endpoints per cluster
        for ref in self.referrals:
            # Inherit credentials from the referral's own cluster if possible,
            # otherwise fall back to the first configured endpoint
            cluster_eps = endpoints_by_cluster.get(ref.subnqn, [])
            donor_ep = (
                cluster_eps[0] if cluster_eps else (all_endpoints[0] if all_endpoints else None)
            )
            if donor_ep:
                ref_ep = Endpoint(
                    traddr=ref.traddr,
                    port=ref.port,
                    hostnqn=donor_ep.hostnqn,
                    subnqn=ref.subnqn,
                    secret=donor_ep.secret,
                    ctrl_secret=donor_ep.ctrl_secret,
                    hostid=donor_ep.hostid,
                )
                endpoints_by_cluster.setdefault(ref.subnqn, []).append(ref_ep)

        # Per-cluster: check active controller, failover if needed
        for subnqn, endpoints in endpoints_by_cluster.items():
            cluster = self.get_cluster(subnqn)
            cluster.endpoints = endpoints

            # Check if active controller is still alive
            if cluster.active_endpoint and cluster.active_endpoint in live_discovery:
                cluster.reconnecting_since = None
                log.debug(
                    'Cluster %s: active controller %s:%s alive',
                    subnqn or 'default',
                    *cluster.active_endpoint,
                )
                continue

            # Active endpoint lost or not yet set (e.g., after daemon restart).
            # Before calling connect-all, check if the kernel already has a
            # persistent discovery controller for one of our endpoints. This
            # prevents duplicate persistent controllers when the daemon
            # restarts while the kernel is still maintaining connections from
            # the previous daemon instance.
            adopted = False
            for ep in endpoints:
                if (ep.traddr, ep.port) in live_discovery:
                    cluster.active_endpoint = (ep.traddr, ep.port)
                    cluster.reconnecting_since = None
                    log.info(
                        'Cluster %s: adopted existing persistent controller %s:%s',
                        subnqn or 'default',
                        ep.traddr,
                        ep.port,
                    )
                    adopted = True
                    break

            if adopted:
                continue

            # Active endpoint is gone and no existing controller found.
            # Give the kernel a grace period to finish reconnecting the
            # persistent controller before we failover and create a new one.
            # This avoids duplicate controllers when a network blip causes
            # the controller to briefly disappear from sysfs.
            if cluster.active_endpoint:
                now = time.monotonic()
                if cluster.reconnecting_since is None:
                    cluster.reconnecting_since = now
                    log.info(
                        'Cluster %s: active controller %s:%s not visible, '
                        'waiting for kernel reconnect (grace period %ds)',
                        subnqn or 'default',
                        *cluster.active_endpoint,
                        self.reconnect_grace,
                    )
                    continue

                elapsed = now - cluster.reconnecting_since
                if elapsed < self.reconnect_grace:
                    log.debug(
                        'Cluster %s: still waiting for reconnect (%.0fs / %ds)',
                        subnqn or 'default',
                        elapsed,
                        self.reconnect_grace,
                    )
                    continue

                old_addr, old_port = cluster.active_endpoint
                log.warning(
                    'Cluster %s: active controller %s:%s lost after %ds, failing over',
                    subnqn or 'default',
                    old_addr,
                    old_port,
                    self.reconnect_grace,
                )
                # Disconnect ALL discovery controllers by NQN to get a clean
                # slate. Individual disconnect-by-device doesn't cancel
                # kernel persistent reconnection. Disconnect-by-NQN is a
                # single kernel operation that clears all discovery state.
                # The subsequent connect-all will create a fresh persistent
                # controller to the new endpoint.
                NvmeCli.disconnect_by_nqn(DISCOVERY_SUBNQN)
                cluster.active_endpoint = None
                cluster.reconnecting_since = None
                metrics.failovers_total += 1

            # No existing persistent controller — create one
            candidates = list(endpoints)
            random.shuffle(candidates)
            for ep in candidates:
                if self.try_connect_all(ep):
                    break
            else:
                if endpoints:
                    log.warning(
                        'Cluster %s: all %d endpoints unreachable',
                        subnqn or 'default',
                        len(endpoints),
                    )

        # Expire stale referrals
        self.expire_referrals()

        # Clean up clusters with no endpoints
        empty = [k for k, v in self.clusters.items() if not v.endpoints]
        for k in empty:
            del self.clusters[k]

        metrics.clusters_tracked = len(self.clusters)

    def run(self, aen_enabled: bool = True):
        """Main daemon loop."""
        signal.signal(signal.SIGTERM, self.shutdown)
        signal.signal(signal.SIGINT, self.shutdown)

        log.info('discovery-client-lite starting')
        log.info('Config dir: %s', self.config_dir)
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

        Path(self.config_dir).mkdir(parents=True, exist_ok=True)
        if self.auto_detect_enabled:
            auto_detect_endpoints(self.config_dir, self.discovery_port, self.auto_detect_filename)
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

        # Disconnect persistent discovery controllers by NQN so the kernel
        # cancels persistent reconnection. Only discovery controllers — NOT
        # IO controllers. IO connections must survive daemon restarts (that's
        # the point of --persistent). The test framework or operator handles
        # IO teardown separately via nvme disconnect-all.
        log.info('Disconnecting all discovery controllers on shutdown')
        NvmeCli.disconnect_by_nqn(DISCOVERY_SUBNQN)

        sd_notify('STOPPING=1')
        log.info('discovery-client-lite stopped')
