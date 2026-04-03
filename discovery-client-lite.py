#!/usr/bin/env python3
"""discovery-client-lite: Lightweight NVMe/TCP discovery daemon.

Thin wrapper around `nvme connect-all --persistent` that fills gaps:
  - Multiple discovery endpoints with failover
  - Referral following with expiry
  - Config directory watching with disconnect on removal
  - Auto-detection from existing IO controllers
  - Recovery when discovery controllers go down
  - Multi-cluster support (one persistent controller per cluster)
  - Prometheus metrics endpoint
  - Systemd readiness notification

The kernel handles persistent connections, AEN, keep-alive, and IO
controller reconnection natively via the --persistent flag. The daemon
adopts existing persistent controllers on restart to avoid duplicates.

When the kernel receives an AEN on a persistent discovery connection, it
emits a uevent (NVME_AEN=...) via netlink. The daemon listens for these
events and triggers an immediate poll cycle, giving near-instant reaction
to topology changes without reimplementing the NVMe/TCP protocol.

Usage:
  discovery-client-lite.py [--config-dir DIR] [--interval SECS] [--cache-file PATH]
"""

import argparse
import json
import logging
import logging.handlers
import os
import random
import signal
import socket
import subprocess
import sys
import time
from dataclasses import dataclass, asdict, field
from http.server import HTTPServer, BaseHTTPRequestHandler
from pathlib import Path
from threading import Event, Thread
from typing import Dict, List, Optional, Set, Tuple

log = logging.getLogger('discovery-client-lite')


# --- Data types ---


@dataclass(frozen=True)
class Endpoint:
    """A discovery controller endpoint from config."""

    traddr: str
    port: str = '8009'
    hostnqn: str = ''
    subnqn: str = ''
    secret: str = ''
    ctrl_secret: str = ''  # DH-CHAP controller secret
    hostid: str = ''
    ctrl_loss_tmo: Optional[int] = None  # per-endpoint override


@dataclass
class CachedReferral:
    """A discovered referral with a timestamp for expiry."""

    traddr: str
    port: str
    subnqn: str
    discovered_at: float = 0.0

    def to_key(self) -> Tuple[str, str, str]:
        return (self.traddr, self.port, self.subnqn)


@dataclass
class ConnectedController:
    """An NVMe controller visible in sysfs."""

    device: str
    traddr: str
    port: str
    subnqn: str
    hostnqn: str
    transport: str


RECONNECT_GRACE_MULTIPLIER = 3  # wait this many reconnect_delay intervals before failover


@dataclass
class ClusterState:
    """State for a single cluster (identified by subnqn)."""

    subnqn: str
    active_endpoint: Optional[Tuple[str, str]] = None
    endpoints: List[Endpoint] = field(default_factory=list)
    referrals: List[CachedReferral] = field(default_factory=list)
    reconnecting_since: Optional[float] = None  # timestamp when active endpoint disappeared


# --- Metrics ---


class SimpleHistogram:
    """Minimal Prometheus histogram (no external dependency).

    Uses single-bucket counting: each observe() increments only the
    smallest matching bucket. render() computes cumulative counts.
    """

    BUCKETS = (0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10)

    def __init__(self, name: str, help_text: str):
        self.name = name
        self.help_text = help_text
        self.buckets = [0] * len(self.BUCKETS)
        self.total = 0.0
        self.count = 0

    def observe(self, value: float):
        self.total += value
        self.count += 1
        for i, bound in enumerate(self.BUCKETS):
            if value <= bound:
                self.buckets[i] += 1
                return
        # Value exceeds all finite buckets — only +Inf

    def render(self) -> str:
        lines = [
            f'# HELP {self.name} {self.help_text}',
            f'# TYPE {self.name} histogram',
        ]
        cumulative = 0
        for i, bound in enumerate(self.BUCKETS):
            cumulative += self.buckets[i]
            lines.append(f'{self.name}_bucket{{le="{bound}"}} {cumulative}')
        lines.append(f'{self.name}_bucket{{le="+Inf"}} {self.count}')
        lines.append(f'{self.name}_sum {self.total}')
        lines.append(f'{self.name}_count {self.count}')
        return '\n'.join(lines)


class Metrics:
    """Prometheus metrics collector matching Go discovery-client metric names."""

    def __init__(self):
        # Gauges
        self.tcp_server_serving_states = 0
        self.tcp_queues_total = 0
        self.tcp_targets_total = 0
        self.clusters_tracked = 0
        # Per-hostnqn gauge
        self.targets_per_hostnqn: Dict[str, int] = {}
        # Counters
        self.targets_map_id = 0
        self.aen_sent_total = 0
        self.poll_cycles_total = 0
        self.poll_cycle_errors_total = 0
        self.failovers_total = 0
        self.connect_attempts_total = 0
        self.connect_failures_total = 0
        # Histogram
        self.poll_cycle_duration = SimpleHistogram(
            'discovery_poll_cycle_duration_seconds',
            'Duration of poll cycle in seconds',
        )

    def render(self) -> str:
        lines = []

        def gauge(name, help_text, value):
            lines.append(f'# HELP {name} {help_text}')
            lines.append(f'# TYPE {name} gauge')
            lines.append(f'{name} {value}')

        def counter(name, help_text, value):
            lines.append(f'# HELP {name} {help_text}')
            lines.append(f'# TYPE {name} counter')
            lines.append(f'{name} {value}')

        gauge(
            'discovery_tcp_server_serving_states',
            'TCP server serving states',
            self.tcp_server_serving_states,
        )
        gauge('discovery_tcp_queues_total', 'Total open TCP queues', self.tcp_queues_total)
        gauge('discovery_tcp_targets_total', 'Total TCP targets', self.tcp_targets_total)

        # Per-hostnqn labeled gauge
        lines.append('# HELP discovery_targets_per_hostnqn_total Targets per host NQN')
        lines.append('# TYPE discovery_targets_per_hostnqn_total gauge')
        for hostnqn, count in sorted(self.targets_per_hostnqn.items()):
            lines.append(f'discovery_targets_per_hostnqn_total{{hostnqn="{hostnqn}"}} {count}')

        counter('discovery_targets_map_id', 'Current target map ID', self.targets_map_id)
        counter('discovery_aen_sent_total', 'Total AEN notifications sent', self.aen_sent_total)
        counter(
            'discovery_poll_cycles_total',
            'Total number of poll cycles executed',
            self.poll_cycles_total,
        )
        counter(
            'discovery_poll_cycle_errors_total',
            'Total poll cycle errors',
            self.poll_cycle_errors_total,
        )

        # Histogram
        lines.append(self.poll_cycle_duration.render())

        counter(
            'discovery_failovers_total',
            'Total discovery controller failovers',
            self.failovers_total,
        )
        counter(
            'discovery_connect_attempts_total',
            'Total connect-all attempts',
            self.connect_attempts_total,
        )
        counter(
            'discovery_connect_failures_total',
            'Total connect-all failures',
            self.connect_failures_total,
        )
        gauge(
            'discovery_clusters_tracked', 'Number of clusters being tracked', self.clusters_tracked
        )

        return '\n'.join(lines) + '\n'


metrics = Metrics()


class MetricsHandler(BaseHTTPRequestHandler):
    """HTTP handler for /metrics endpoint."""

    def do_GET(self):
        if self.path == '/metrics':
            body = metrics.render().encode()
            self.send_response(200)
            self.send_header('Content-Type', 'text/plain; version=0.0.4')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, format, *args):
        """Suppress default request logging."""
        pass


def start_metrics_server(port: int):
    """Start HTTP metrics server in a daemon thread."""
    try:
        server = HTTPServer(('0.0.0.0', port), MetricsHandler)
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        log.info('Metrics server listening on :%d/metrics', port)
    except OSError as e:
        log.warning('Failed to start metrics server on port %d: %s', port, e)


# --- Systemd notification ---


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


# --- nvme-cli wrapper ---


class NvmeCli:
    """Wraps nvme-cli commands."""

    @staticmethod
    def _run(args: List[str], timeout: int = 30) -> Tuple[int, str, str]:
        """Run an nvme command, return (rc, stdout, stderr)."""
        cmd = ['nvme'] + args
        log.debug('Running: %s', ' '.join(cmd))
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
            return result.returncode, result.stdout.strip(), result.stderr.strip()
        except subprocess.TimeoutExpired:
            log.error('nvme command timed out: %s', ' '.join(cmd))
            return 1, '', 'timeout'
        except FileNotFoundError:
            log.error('nvme-cli not found. Is it installed?')
            return 1, '', 'nvme not found'

    @staticmethod
    def _run_json(args: List[str], timeout: int = 30) -> Optional[dict]:
        """Run an nvme command with -o json and return parsed output."""
        rc, stdout, _ = NvmeCli._run(args + ['-o', 'json'], timeout)
        if rc != 0:
            return None
        if not stdout:
            return {}
        try:
            return json.loads(stdout)
        except json.JSONDecodeError as e:
            log.error('Failed to parse nvme JSON output: %s', e)
            return None

    @staticmethod
    def connect_all(
        traddr: str,
        port: str,
        hostnqn: str = '',
        hostid: str = '',
        secret: str = '',
        ctrl_secret: str = '',
        ctrl_loss_tmo: int = 3600,
        reconnect_delay: int = 10,
        nr_io_queues: int = 0,
        persistent: bool = True,
        kato: int = 10,
    ) -> Tuple[bool, Optional[dict]]:
        """Run nvme connect-all. Returns (success, discovery_log_json)."""
        args = [
            'connect-all',
            '-t',
            'tcp',
            '-a',
            traddr,
            '-s',
            port,
            '--ctrl-loss-tmo=%d' % ctrl_loss_tmo,
            '--reconnect-delay=%d' % reconnect_delay,
        ]
        if persistent:
            args += ['--persistent']
        if kato:
            args += ['--keep-alive-tmo=%d' % kato]
        if hostnqn:
            args += ['-q', hostnqn]
        if hostid:
            args += ['-I', hostid]
        if secret:
            args += ['-S', secret]  # nvme-cli 2.x+ (DH-CHAP)
        if ctrl_secret:
            args += ['-C', ctrl_secret]  # nvme-cli 2.x+ (DH-CHAP bidirectional)
        if nr_io_queues > 0:
            args += ['--nr-io-queues=%d' % nr_io_queues]

        output = NvmeCli._run_json(args)
        if output is None:
            return False, None
        return True, output

    @staticmethod
    def disconnect(device: str) -> bool:
        """Disconnect an NVMe controller by device name."""
        rc, _, stderr = NvmeCli._run(['disconnect', '-d', device])
        if rc != 0:
            log.error('Disconnect failed for %s: %s', device, stderr)
            return False
        return True

    @staticmethod
    def discover(
        traddr: str,
        trsvcid: str = '8009',
        hostnqn: str = '',
        hostid: str = '',
        transport: str = 'tcp',
        host_traddr: str = '',
        persistent: bool = False,
    ) -> Optional[dict]:
        """Run nvme discover. Returns parsed JSON output or None."""
        args = ['discover', '-t', transport, '-a', traddr, '-s', trsvcid]
        if hostnqn:
            args += ['-q', hostnqn]
        if hostid:
            args += ['-I', hostid]
        if host_traddr:
            args += ['-w', host_traddr]
        if persistent:
            args += ['--persistent']
        return NvmeCli._run_json(args)

    @staticmethod
    def connect(
        traddr: str,
        trsvcid: str = '4420',
        hostnqn: str = '',
        hostid: str = '',
        transport: str = 'tcp',
        host_traddr: str = '',
        nqn: str = '',
        ctrl_loss_tmo: int = -1,
        dhchap_secret: str = '',
        dhchap_ctrl_secret: str = '',
    ) -> Optional[dict]:
        """Run nvme connect. Returns parsed JSON output or None."""
        args = ['connect', '-t', transport, '-a', traddr, '-s', trsvcid]
        if nqn:
            args += ['-n', nqn]
        if hostnqn:
            args += ['-q', hostnqn]
        if hostid:
            args += ['-I', hostid]
        if host_traddr:
            args += ['-w', host_traddr]
        if ctrl_loss_tmo != 0:
            args += ['--ctrl-loss-tmo=%d' % ctrl_loss_tmo]
        if dhchap_secret:
            args += ['-S', dhchap_secret]
        if dhchap_ctrl_secret:
            args += ['-C', dhchap_ctrl_secret]
        return NvmeCli._run_json(args)

    @staticmethod
    def disconnect_all() -> bool:
        """Disconnect all NVMe fabrics controllers (matching Go behavior).

        Iterates all controllers in /sys/class/nvme/, checks for
        delete_controller sysfs path existence before disconnecting.
        """
        nvme_dir = Path('/sys/class/nvme')
        if not nvme_dir.exists():
            return True
        success = True
        for ctrl_path in sorted(nvme_dir.iterdir()):
            if not ctrl_path.is_dir():
                continue
            delete_path = ctrl_path / 'delete_controller'
            if not delete_path.exists():
                continue
            device = ctrl_path.name
            if not NvmeCli.disconnect(device):
                success = False
        return success

    @staticmethod
    def list_controllers(discovery_only: bool = False) -> List[dict]:
        """List connected NVMe controllers as dicts (for JSON output)."""
        controllers = get_connected_controllers()
        if discovery_only:
            controllers = [c for c in controllers if c.subnqn == DISCOVERY_SUBNQN]
        return [
            {
                'device': c.device,
                'traddr': c.traddr,
                'trsvcid': c.port,
                'subnqn': c.subnqn,
                'hostnqn': c.hostnqn,
                'transport': c.transport,
            }
            for c in controllers
        ]


# --- Sysfs reader ---


def get_connected_controllers() -> List[ConnectedController]:
    """Read currently connected NVMe/TCP controllers from sysfs."""
    controllers = []
    nvme_dir = Path('/sys/class/nvme')
    if not nvme_dir.exists():
        return controllers

    for ctrl_path in sorted(nvme_dir.iterdir()):
        if not ctrl_path.is_dir():
            continue
        try:
            transport = (ctrl_path / 'transport').read_text().strip()
            if transport != 'tcp':
                continue
            address = (ctrl_path / 'address').read_text().strip()
            subnqn = (ctrl_path / 'subsysnqn').read_text().strip()
            hostnqn = (ctrl_path / 'hostnqn').read_text().strip()

            addr_fields = {}
            for f in address.split(','):
                if '=' in f:
                    k, v = f.split('=', 1)
                    addr_fields[k.strip()] = v.strip()

            controllers.append(
                ConnectedController(
                    device=ctrl_path.name,
                    traddr=addr_fields.get('traddr', ''),
                    port=addr_fields.get('trsvcid', ''),
                    subnqn=subnqn,
                    hostnqn=hostnqn,
                    transport=transport,
                )
            )
        except (OSError, ValueError) as e:
            log.debug('Skipping %s: %s', ctrl_path.name, e)

    return controllers


DISCOVERY_SUBNQN = 'nqn.2014-08.org.nvmexpress.discovery'


def get_discovery_controllers() -> Set[Tuple[str, str]]:
    """Return set of (traddr, port) for currently connected discovery controllers."""
    discovery = set()
    for ctrl in get_connected_controllers():
        if ctrl.subnqn == DISCOVERY_SUBNQN:
            discovery.add((ctrl.traddr, ctrl.port))
    return discovery


def get_host_id(path: str = '/etc/nvme/hostid') -> str:
    """Read the NVMe host ID from the given path."""
    try:
        return Path(path).read_text().strip()
    except OSError:
        return ''


def get_connected_hostids() -> Dict[str, str]:
    """Scan all NVMe fabrics controllers and return hostnqn -> hostid map.

    Matches the Go discovery-client's GetNvmfHosts() behavior: when
    controllers are already connected with a given hostnqn, the hostid
    they're using takes precedence over whatever the config says.
    """
    hosts: Dict[str, str] = {}
    fabrics_dir = Path('/sys/class/nvme-fabrics/ctl')
    if not fabrics_dir.exists():
        return hosts

    for ctrl_path in sorted(fabrics_dir.iterdir()):
        if not ctrl_path.is_dir():
            continue
        try:
            hostnqn = (ctrl_path / 'hostnqn').read_text().strip()
            hostid = (ctrl_path / 'hostid').read_text().strip()
            if hostnqn and hostid:
                hosts[hostnqn] = hostid
        except OSError:
            continue

    if hosts:
        log.debug('Found %d connected hosts with hostids', len(hosts))
    return hosts


# --- Config parsing ---


def parse_config_line(line: str) -> Optional[Endpoint]:
    """Parse a config line: -t tcp -a ADDR -s PORT -q HOSTNQN -n SUBNQN [-w SECRET]."""
    line = line.strip()
    if not line or line.startswith('#'):
        return None

    tokens = line.split()
    traddr = port = hostnqn = subnqn = secret = ctrl_secret = hostid = ''
    ctrl_loss_tmo = None
    i = 0
    while i < len(tokens):
        if tokens[i] == '-a' and i + 1 < len(tokens):
            traddr = tokens[i + 1]
            i += 2
        elif tokens[i] == '-s' and i + 1 < len(tokens):
            port = tokens[i + 1]
            i += 2
        elif tokens[i] == '-q' and i + 1 < len(tokens):
            hostnqn = tokens[i + 1]
            i += 2
        elif tokens[i] == '-n' and i + 1 < len(tokens):
            subnqn = tokens[i + 1]
            i += 2
        elif tokens[i] in ('-w', '-S') and i + 1 < len(tokens):
            secret = tokens[i + 1]
            i += 2
        elif tokens[i] == '-C' and i + 1 < len(tokens):
            ctrl_secret = tokens[i + 1]
            i += 2
        elif tokens[i] in ('-I', '--hostid') and i + 1 < len(tokens):
            hostid = tokens[i + 1]
            i += 2
        elif tokens[i] == '-l' and i + 1 < len(tokens):
            try:
                ctrl_loss_tmo = int(tokens[i + 1])
            except ValueError:
                log.warning('Invalid ctrl_loss_tmo value: %s', tokens[i + 1])
            i += 2
        elif tokens[i] == '-t' and i + 1 < len(tokens):
            i += 2
        else:
            i += 1

    if not traddr:
        return None
    # Defense-in-depth: if traddr contains a port (IP:PORT format) and no
    # explicit -s was given, split it. This handles config lines like
    # "-a 10.0.0.1:8009" where the port is embedded in the address field.
    if not port and ':' in traddr:
        traddr, _, extracted_port = traddr.rpartition(':')
        if extracted_port.isdigit():
            port = extracted_port
    return Endpoint(
        traddr=traddr,
        port=port or '8009',
        hostnqn=hostnqn,
        subnqn=subnqn,
        secret=secret,
        ctrl_secret=ctrl_secret,
        hostid=hostid,
        ctrl_loss_tmo=ctrl_loss_tmo,
    )


def read_config_dir(config_dir: str) -> Dict[str, List[Endpoint]]:
    """Read discovery endpoints grouped by source file.

    Returns dict of filename → list of endpoints.
    """
    result: Dict[str, List[Endpoint]] = {}
    config_path = Path(config_dir)
    if not config_path.is_dir():
        return result

    for f in sorted(config_path.iterdir()):
        if not f.is_file():
            continue
        try:
            endpoints = []
            for line in f.read_text().splitlines():
                ep = parse_config_line(line)
                if ep:
                    endpoints.append(ep)
            if endpoints:
                result[f.name] = endpoints
        except OSError as e:
            log.warning('Failed to read config file %s: %s', f, e)

    return result


def auto_detect_endpoints(
    config_dir: str, discovery_port: str, filename: str = 'detected-io-controllers'
) -> bool:
    """Bootstrap config from existing IO controllers when config dir is empty."""
    config_path = Path(config_dir)
    if any(f for f in config_path.iterdir() if f.is_file() and not f.name.startswith('.')):
        return False

    controllers = get_connected_controllers()
    if not controllers:
        return False

    seen = set()
    lines = []
    for ctrl in controllers:
        if ctrl.subnqn == DISCOVERY_SUBNQN:
            continue
        key = (ctrl.traddr, ctrl.hostnqn)
        if key in seen:
            continue
        seen.add(key)
        line = f'-t tcp -a {ctrl.traddr} -s {discovery_port}'
        if ctrl.hostnqn:
            line += f' -q {ctrl.hostnqn}'
        lines.append(line)

    if not lines:
        return False

    auto_file = config_path / filename
    auto_file.write_text('\n'.join(lines) + '\n')
    log.info(
        'Auto-detected %d endpoints from existing controllers, wrote %s', len(lines), auto_file
    )
    return True


# --- Referral cache ---


def load_referral_cache(cache_file: str) -> List[CachedReferral]:
    """Load cached referrals from disk."""
    path = Path(cache_file)
    if not path.exists():
        return []
    try:
        data = json.loads(path.read_text())
        referrals = [CachedReferral(**entry) for entry in data]
        log.debug('Loaded %d cached referrals', len(referrals))
        return referrals
    except (json.JSONDecodeError, OSError, TypeError) as e:
        log.warning('Failed to load referral cache: %s', e)
        return []


def save_referral_cache(cache_file: str, referrals: List[CachedReferral]):
    """Save referrals to disk."""
    path = Path(cache_file)
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        data = [asdict(r) for r in referrals]
        path.write_text(json.dumps(data, indent=2))
    except OSError as e:
        log.warning('Failed to save referral cache: %s', e)


# --- Discovery log parsing ---


def extract_referrals(output: dict) -> List[CachedReferral]:
    """Extract referral entries from discovery log page JSON.

    TODO: Validate field names against different nvme-cli versions.
          JSON schema may differ between nvme-cli 1.x and 2.x.
    """
    referrals = []
    for rec in output.get('records', []):
        subtype = rec.get('subtype', '').lower()
        if 'discovery' not in subtype and 'referral' not in subtype:
            continue
        subnqn = rec.get('subnqn', '')
        traddr = rec.get('traddr', '').strip()
        trsvcid = str(rec.get('trsvcid', '')).strip()
        if subnqn and traddr:
            referrals.append(
                CachedReferral(
                    traddr=traddr, port=trsvcid, subnqn=subnqn, discovered_at=time.time()
                )
            )
    return referrals


# --- AEN listener ---


NETLINK_KOBJECT_UEVENT = 15


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


# --- Main daemon ---


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
                NvmeCli._run(['disconnect', '-n', DISCOVERY_SUBNQN])
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
        NvmeCli._run(['disconnect', '-n', DISCOVERY_SUBNQN])

        sd_notify('STOPPING=1')
        log.info('discovery-client-lite stopped')


def load_yaml_config(config_path: str) -> dict:
    """Load discovery-client.yaml config file.

    Compatible with the Go discovery-client's config format.
    """
    try:
        import yaml
    except ImportError:
        log.warning('PyYAML not available, skipping config file')
        return {}

    path = Path(config_path)
    if not path.exists():
        return {}
    try:
        with open(path) as f:
            conf = yaml.safe_load(f) or {}
        log.info('Loaded config from %s', path)
        return conf
    except Exception as e:
        log.warning('Failed to load config from %s: %s', path, e)
        return {}


def parse_interval(value) -> int:
    """Parse an interval value that may be a string like '5s' or an int."""
    try:
        if isinstance(value, int):
            return max(0, value)
        if isinstance(value, str):
            value = value.strip().lower()
            if not value:
                return 0
            if value.endswith('s'):
                return max(0, int(value[:-1]))
            if value.endswith('m'):
                return max(0, int(value[:-1]) * 60)
            if value.endswith('h'):
                return max(0, int(value[:-1]) * 3600)
            return max(0, int(value))
        return max(0, int(value))
    except (ValueError, TypeError):
        log.warning('Invalid interval value: %s, defaulting to 0', value)
        return 0


# Static mapping: env var name → config key
_ENV_MAP = {
    'DC_CLIENTCONFIGDIR': 'clientConfigDir',
    'DC_INTERNALDIR': 'internalDir',
    'DC_RECONNECTINTERVAL': 'reconnectInterval',
    'DC_POLLINGINTERVAL': 'pollingInterval',
    'DC_MAXIOQUEUES': 'maxIOQueues',
    'DC_KATO': 'kato',
    'DC_CTRLOSSTMO': 'ctrlLossTMO',
    'DC_NVMEHOSTIDPATH': 'nvmeHostIDPath',
    'DC_DHCHAPSECRET': 'dhChapSecret',
    'DC_DHCHAPCTRLSECRET': 'dhChapCtrlSecret',
    'DC_LOGGING_FILENAME': 'logging.filename',
    'DC_LOGGING_LEVEL': 'logging.level',
    'DC_LOGGING_MAXSIZE': 'logging.maxSize',
    'DC_LOGGING_MAXAGE': 'logging.maxAge',
    'DC_LOGGING_REPORTCALLER': 'logging.reportcaller',
    'DC_DEBUG_ENDPOINT': 'debug.endpoint',
    'DC_DEBUG_METRICS': 'debug.metrics',
    'DC_DEBUG_ENABLEPPROF': 'debug.enablepprof',
    'DC_AUTODETECTENTRIES_ENABLED': 'autoDetectEntries.enabled',
    'DC_AUTODETECTENTRIES_FILENAME': 'autoDetectEntries.filename',
    'DC_AUTODETECTENTRIES_DISCOVERYSERVICEPORT': 'autoDetectEntries.discoveryServicePort',
    'DC_REFERRALTTL': 'referralTTL',
    'DC_AEN_ENABLED': 'aenEnabled',
}


def load_env_overrides(environ: Optional[dict] = None) -> dict:
    """Scan environment for DC_* variables and return config overrides.

    Args:
        environ: Environment dict to scan. Defaults to os.environ.

    Returns:
        Dict mapping config keys to string values from the environment.
    """
    if environ is None:
        environ = os.environ
    overrides = {}
    for env_key, config_key in _ENV_MAP.items():
        value = environ.get(env_key)
        if value is not None:
            overrides[config_key] = value
    return overrides


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser with all subcommands."""
    # Shared parent parser that contributes --config to every subcommand,
    # so `discover -a x -q y --config /tmp/c.yaml` works.
    global_parent = argparse.ArgumentParser(add_help=False)
    global_parent.add_argument(
        '--config',
        default='/etc/discovery-client/discovery-client.yaml',
        help='Path to YAML config file',
    )

    parser = argparse.ArgumentParser(
        description='Lightweight NVMe/TCP discovery client wrapping nvme-cli',
        parents=[global_parent],
    )

    # Backward-compat direct CLI flags on the root parser so that
    # `discovery-client-lite.py --interval 5` still works without a subcommand.
    parser.add_argument('--config-dir', dest='config_dir', default=None)
    parser.add_argument('--cache-file', default=None)
    parser.add_argument('--interval', type=int, default=None)
    parser.add_argument('--ctrl-loss-tmo', type=int, default=None)
    parser.add_argument('--kato', type=int, default=None)
    parser.add_argument('--nr-io-queues', type=int, default=None)
    parser.add_argument('--discovery-port', default=None)
    parser.add_argument('--referral-ttl', type=int, default=None)
    parser.add_argument('--metrics-port', type=int, default=None)
    parser.add_argument('--log-level', default=None)
    parser.add_argument('--log-file', default=None)
    parser.add_argument('--log-max-size', type=int, default=None)
    parser.add_argument('--log-max-age', type=int, default=None)
    parser.add_argument(
        '--no-aen',
        dest='aen_enabled',
        action='store_false',
        default=None,
        help='Disable AEN uevent listener (poll-only mode)',
    )

    sub = parser.add_subparsers(dest='command')

    # --- serve (default when no subcommand) ---
    serve_p = sub.add_parser('serve', help='Run as daemon (default)', parents=[global_parent])
    serve_p.add_argument('--logging.filename', dest='logging_filename', default=None)
    serve_p.add_argument('--logging.maxage', dest='logging_maxage', default=None)
    serve_p.add_argument('--logging.maxSize', dest='logging_maxsize', type=int, default=None)
    serve_p.add_argument('--logging.reportcaller', dest='logging_reportcaller', default=None)
    serve_p.add_argument('--logging.level', dest='logging_level', default=None)
    serve_p.add_argument('--debug.endpoint', dest='debug_endpoint', default=None)
    serve_p.add_argument('--debug.enablepprof', dest='debug_enablepprof', default=None)
    serve_p.add_argument('--debug.metrics', dest='debug_metrics', default=None)
    serve_p.add_argument('--clientConfigDir', dest='config_dir', default=None)
    serve_p.add_argument('--internalDir', dest='internal_dir', default=None)
    serve_p.add_argument('--nvmeHostIDPath', dest='nvme_host_id_path', default=None)
    serve_p.add_argument('--pollingInterval', dest='polling_interval', default=None)
    serve_p.add_argument('--maxIOQueues', dest='max_io_queues', type=int, default=None)
    serve_p.add_argument('--kato', type=int, default=None)
    serve_p.add_argument(
        '-e', '--autoDetectEntries.enabled', dest='autodetect_enabled', default=None
    )
    serve_p.add_argument(
        '-f', '--autoDetectEntries.filename', dest='autodetect_filename', default=None
    )
    serve_p.add_argument(
        '-p', '--autoDetectEntries.discoveryServicePort', dest='autodetect_port', default=None
    )
    # Backward-compat direct CLI flags
    serve_p.add_argument('--config-dir', dest='config_dir_compat', default=None)
    serve_p.add_argument('--cache-file', default=None)
    serve_p.add_argument('--interval', type=int, default=None)
    serve_p.add_argument('--ctrl-loss-tmo', type=int, default=None)
    serve_p.add_argument('--nr-io-queues', type=int, default=None)
    serve_p.add_argument('--discovery-port', default=None)
    serve_p.add_argument('--referral-ttl', type=int, default=None)
    serve_p.add_argument('--metrics-port', type=int, default=None)
    serve_p.add_argument('--log-level', default=None)
    serve_p.add_argument('--log-file', default=None)
    serve_p.add_argument('--log-max-size', type=int, default=None)
    serve_p.add_argument('--log-max-age', type=int, default=None)
    serve_p.add_argument(
        '--no-aen',
        dest='aen_enabled',
        action='store_false',
        default=None,
        help='Disable AEN uevent listener (poll-only mode)',
    )

    # --- discover ---
    disc_p = sub.add_parser('discover', help='Discover NVMe subsystems', parents=[global_parent])
    disc_p.add_argument('-a', '--traddr', required=True)
    disc_p.add_argument('-s', '--trsvcid', type=int, default=8009)
    disc_p.add_argument('-q', '--hostnqn', required=True)
    disc_p.add_argument('-I', '--hostid', default='')
    disc_p.add_argument('-t', '--transport', default='tcp')
    disc_p.add_argument('-w', '--host-traddr', default='')
    disc_p.add_argument('-p', '--persistant', action='store_true', default=False)

    # --- connect ---
    conn_p = sub.add_parser('connect', help='Connect to an NVMe subsystem', parents=[global_parent])
    conn_p.add_argument('-a', '--traddr', required=True)
    conn_p.add_argument('-s', '--trsvcid', type=int, default=4420)
    conn_p.add_argument('-q', '--hostnqn', default='')
    conn_p.add_argument('-I', '--hostid', default='')
    conn_p.add_argument('-t', '--transport', default='tcp')
    conn_p.add_argument('-w', '--host-traddr', default='')
    conn_p.add_argument('-n', '--nqn', required=True)
    conn_p.add_argument('--ctrl-loss-tmo', type=int, default=-1)
    conn_p.add_argument('-S', '--dhchap-secret', default='')
    conn_p.add_argument('-C', '--dhchap-ctrl-secret', default='')

    # --- connect-all ---
    ca_p = sub.add_parser(
        'connect-all', help='Discover and connect to all subsystems', parents=[global_parent]
    )
    ca_p.add_argument('-a', '--traddr', required=True)
    ca_p.add_argument('-s', '--trsvcid', type=int, default=8009)
    ca_p.add_argument('-q', '--hostnqn', default='')
    ca_p.add_argument('-I', '--hostid', default='')
    ca_p.add_argument('-t', '--transport', default='tcp')
    ca_p.add_argument('-w', '--host-traddr', default='')
    ca_p.add_argument('-p', '--persistant', action='store_true', default=False)
    ca_p.add_argument('-m', '--max-queues', type=int, default=0)
    ca_p.add_argument('--ctrl-loss-tmo', type=int, default=-1)
    ca_p.add_argument('-k', '--kato', type=int, default=0)

    # --- disconnect ---
    dc_p = sub.add_parser(
        'disconnect', help='Disconnect an NVMe controller', parents=[global_parent]
    )
    dc_p.add_argument('-d', '--device', required=True)

    # --- disconnect-all ---
    sub.add_parser(
        'disconnect-all', help='Disconnect all NVMe/TCP controllers', parents=[global_parent]
    )

    # --- add-hostnqn ---
    ah_p = sub.add_parser(
        'add-hostnqn', help='Add a discovery config entry', parents=[global_parent]
    )
    ah_p.add_argument('--name', required=True)
    ah_p.add_argument('-a', '--addresses', nargs='+', required=True)
    ah_p.add_argument('-q', '--hostnqn', required=True)
    ah_p.add_argument('-I', '--hostid', default='')
    ah_p.add_argument('-n', '--nqn', required=True)
    ah_p.add_argument('-t', '--transport', default='tcp')

    # --- remove-hostnqn ---
    rh_p = sub.add_parser(
        'remove-hostnqn', help='Remove a discovery config entry', parents=[global_parent]
    )
    rh_p.add_argument('-n', '--name', required=True)

    # --- list ctrl ---
    list_p = sub.add_parser('list', help='List resources', parents=[global_parent])
    list_sub = list_p.add_subparsers(dest='list_command')
    ctrl_p = list_sub.add_parser('ctrl', help='List connected NVMe controllers')
    ctrl_p.add_argument('-d', '--discovery', action='store_true', default=False)

    return parser


def cmd_discover(args):
    output = NvmeCli.discover(
        traddr=args.traddr,
        trsvcid=str(args.trsvcid),
        hostnqn=args.hostnqn,
        hostid=args.hostid,
        transport=args.transport,
        host_traddr=getattr(args, 'host_traddr', ''),
        persistent=args.persistant,
    )
    if output is not None:
        print(json.dumps(output, indent=2))
        return 0
    return 1


def cmd_connect(args):
    if args.dhchap_ctrl_secret and not args.dhchap_secret:
        print('error: --dhchap-secret is required when using --dhchap-ctrl-secret', file=sys.stderr)
        return 1
    output = NvmeCli.connect(
        traddr=args.traddr,
        trsvcid=str(args.trsvcid),
        hostnqn=args.hostnqn,
        hostid=args.hostid,
        transport=args.transport,
        host_traddr=getattr(args, 'host_traddr', ''),
        nqn=args.nqn,
        ctrl_loss_tmo=args.ctrl_loss_tmo,
        dhchap_secret=args.dhchap_secret,
        dhchap_ctrl_secret=args.dhchap_ctrl_secret,
    )
    if output is not None:
        print(json.dumps(output, indent=2))
        return 0
    return 1


def cmd_connect_all(args):
    success, output = NvmeCli.connect_all(
        traddr=args.traddr,
        port=str(args.trsvcid),
        hostnqn=getattr(args, 'hostnqn', ''),
        hostid=getattr(args, 'hostid', ''),
        ctrl_loss_tmo=args.ctrl_loss_tmo,
        nr_io_queues=args.max_queues,
        persistent=args.persistant,
        kato=args.kato,
    )
    if success and output:
        print(json.dumps(output, indent=2))
        return 0
    return 0 if success else 1


def cmd_disconnect(args):
    if NvmeCli.disconnect(args.device):
        return 0
    return 1


def cmd_disconnect_all(args):
    if NvmeCli.disconnect_all():
        return 0
    return 1


def _split_host_port(address: str) -> Tuple[str, str]:
    """Split an address into (host, port), handling IP:PORT and bare IP.

    Matches Go's net.SplitHostPort behavior for the common case.
    """
    if ':' in address:
        host, _, port = address.rpartition(':')
        if host and port.isdigit():
            return host, port
    return address, '8009'


def cmd_add_hostnqn(args, config_dir: str):
    if args.name.startswith('tmp.dc.'):
        print('error: name cannot start with "tmp.dc."', file=sys.stderr)
        return 1

    # Split comma-separated addresses (matching Go's StringSliceP behavior)
    all_addrs = []
    for a in args.addresses:
        all_addrs.extend(a.split(','))

    lines = []
    for addr in all_addrs:
        addr = addr.strip()
        if not addr:
            continue
        host, port = _split_host_port(addr)
        line = f'-t {args.transport} -a {host} -s {port} -q {args.hostnqn} -n {args.nqn}'
        if args.hostid:
            line += f' -I {args.hostid}'
        lines.append(line)

    filepath = os.path.join(config_dir, args.name)
    Path(filepath).parent.mkdir(parents=True, exist_ok=True)
    Path(filepath).write_text('\n'.join(lines) + '\n')
    print(json.dumps({'name': filepath}))
    return 0


def cmd_remove_hostnqn(args, config_dir: str):
    filepath = os.path.join(config_dir, args.name)
    try:
        os.unlink(filepath)
    except FileNotFoundError:
        pass
    print(json.dumps({'name': filepath}))
    return 0


def cmd_list_ctrl(args):
    discovery_only = getattr(args, 'discovery', False)
    controllers = NvmeCli.list_controllers(discovery_only=discovery_only)
    print(json.dumps(controllers, indent=2))
    return 0


def run_serve(args):
    """Run the discovery daemon (serve mode).

    Handles both the 'serve' subcommand (with serve-subparser attrs) and the
    no-subcommand case (with root-parser attrs). Uses getattr() throughout to
    handle both cases gracefully.
    """
    # Load YAML config file (Go discovery-client compatible)
    yaml_conf = load_yaml_config(args.config)
    env_conf = load_env_overrides()
    log_conf = yaml_conf.get('logging', {})
    debug_conf = yaml_conf.get('debug', {})

    # Merge: CLI args override env vars override YAML config override defaults.
    # Use 'is not None' checks so that explicit 0 values from CLI are respected.
    def pick(cli_val, env_val, yaml_val, default):
        if cli_val is not None:
            return cli_val
        if env_val is not None:
            return env_val
        if yaml_val is not None:
            return yaml_val
        return default

    def env_int(val):
        if val is None:
            return None
        try:
            return int(val)
        except (ValueError, TypeError):
            return None

    def env_bool(val):
        if val is None:
            return None
        return val.lower() in ('true', '1', 'yes')

    # Helper: merge Go-style serve flag with backward-compat flag
    def cli_arg(*names):
        for name in names:
            val = getattr(args, name, None)
            if val is not None:
                return val
        return None

    config_dir = pick(
        cli_arg('config_dir', 'config_dir_compat'),
        env_conf.get('clientConfigDir'),
        yaml_conf.get('clientConfigDir'),
        '/etc/discovery-client/discovery.d',
    )
    cache_dir_env = env_conf.get('internalDir')
    cache_dir = pick(
        cli_arg('internal_dir'),
        cache_dir_env,
        yaml_conf.get('internalDir'),
        '/etc/discovery-client/internal',
    )
    cache_file = pick(cli_arg('cache_file'), None, None, os.path.join(cache_dir, 'referrals.json'))

    # poll_interval: Go flag is --pollingInterval (dest=polling_interval), compat is --interval
    env_interval_raw = env_conf.get('reconnectInterval') or env_conf.get('pollingInterval')
    env_interval = parse_interval(env_interval_raw) if env_interval_raw else None
    cli_interval = cli_arg('interval')
    cli_polling = getattr(args, 'polling_interval', None)
    if cli_polling is not None:
        cli_polling = parse_interval(cli_polling)
    poll_interval = pick(
        cli_interval or cli_polling,
        env_interval or None,
        None,
        parse_interval(yaml_conf.get('reconnectInterval', 5)),
    )

    ctrl_loss_tmo = pick(
        cli_arg('ctrl_loss_tmo'),
        env_int(env_conf.get('ctrlLossTMO')),
        yaml_conf.get('ctrlLossTMO'),
        3600,
    )
    kato = pick(cli_arg('kato'), env_int(env_conf.get('kato')), yaml_conf.get('kato'), 10)
    nr_io_queues = pick(
        cli_arg('nr_io_queues', 'max_io_queues'),
        env_int(env_conf.get('maxIOQueues')),
        yaml_conf.get('maxIOQueues'),
        0,
    )
    discovery_port = pick(
        cli_arg('discovery_port', 'autodetect_port'),
        env_conf.get('autoDetectEntries.discoveryServicePort'),
        None,
        str(yaml_conf.get('autoDetectEntries', {}).get('discoveryServicePort', 8009)),
    )
    referral_ttl = pick(
        cli_arg('referral_ttl'),
        env_int(env_conf.get('referralTTL')),
        yaml_conf.get('referralTTL'),
        3600,
    )
    # DH-CHAP: config-only (no dedicated CLI flags for serve mode)
    dhchap_secret = env_conf.get('dhChapSecret', '') or yaml_conf.get('dhChapSecret', '')
    dhchap_ctrl_secret = env_conf.get('dhChapCtrlSecret', '') or yaml_conf.get(
        'dhChapCtrlSecret', ''
    )
    if dhchap_ctrl_secret and not dhchap_secret:
        sys.exit('error: dhChapSecret is mandatory when using dhChapCtrlSecret')
    log_level = pick(
        cli_arg('log_level', 'logging_level'),
        env_conf.get('logging.level'),
        log_conf.get('level'),
        'info',
    )
    log_file = pick(
        cli_arg('log_file', 'logging_filename'),
        env_conf.get('logging.filename'),
        log_conf.get('filename'),
        '',
    )
    log_max_size = pick(
        cli_arg('log_max_size', 'logging_maxsize'),
        env_int(env_conf.get('logging.maxSize')),
        log_conf.get('maxSize'),
        100,
    )
    log_max_age_raw = pick(
        cli_arg('log_max_age', 'logging_maxage'),
        env_conf.get('logging.maxAge'),
        log_conf.get('maxAge'),
        '96h',
    )
    # Convert to backup count (days)
    if isinstance(log_max_age_raw, str):
        secs = parse_interval(log_max_age_raw)
        log_backup_count = max(1, secs // 86400) if secs > 0 else 4
    elif isinstance(log_max_age_raw, int):
        log_backup_count = max(1, log_max_age_raw)
    else:
        log_backup_count = 4

    report_caller = pick(
        getattr(args, 'logging_reportcaller', None),
        env_bool(env_conf.get('logging.reportcaller')),
        log_conf.get('reportCaller'),
        True,
    )
    # Handle string 'true'/'false' from CLI
    if isinstance(report_caller, str):
        report_caller = report_caller.lower() in ('true', '1', 'yes')

    if report_caller:
        log_format = '%(asctime)s %(levelname)-5s %(funcName)s:%(lineno)d %(message)s'
    else:
        log_format = '%(asctime)s %(levelname)-5s %(message)s'

    autodetect_conf = yaml_conf.get('autoDetectEntries', {})
    autodetect_enabled = pick(
        getattr(args, 'autodetect_enabled', None),
        env_bool(env_conf.get('autoDetectEntries.enabled')),
        autodetect_conf.get('enabled'),
        True,
    )
    if isinstance(autodetect_enabled, str):
        autodetect_enabled = autodetect_enabled.lower() in ('true', '1', 'yes')

    autodetect_filename = pick(
        getattr(args, 'autodetect_filename', None),
        env_conf.get('autoDetectEntries.filename'),
        autodetect_conf.get('filename'),
        'detected-io-controllers',
    )

    nvme_host_id_path = pick(
        getattr(args, 'nvme_host_id_path', None),
        env_conf.get('nvmeHostIDPath'),
        yaml_conf.get('nvmeHostIDPath'),
        '/etc/nvme/hostid',
    )

    metrics_port = cli_arg('metrics_port')
    if metrics_port is None:
        # Check Go-style --debug.metrics and --debug.endpoint flags
        debug_metrics_cli = getattr(args, 'debug_metrics', None)
        debug_endpoint_cli = getattr(args, 'debug_endpoint', None)
        metrics_enabled = (
            debug_metrics_cli
            or env_bool(env_conf.get('debug.metrics'))
            or debug_conf.get('metrics', False)
        )
        if isinstance(metrics_enabled, str):
            metrics_enabled = metrics_enabled.lower() in ('true', '1', 'yes')
        if metrics_enabled:
            endpoint = (
                debug_endpoint_cli
                or env_conf.get('debug.endpoint')
                or debug_conf.get('endpoint', '0.0.0.0:6060')
            )
            try:
                metrics_port = int(endpoint.rsplit(':', 1)[-1])
            except (ValueError, IndexError):
                metrics_port = 6060
        else:
            metrics_port = 0

    # Configure logging
    log_handlers: list = []
    if log_file:
        log_dir = os.path.dirname(log_file)
        if log_dir:
            os.makedirs(log_dir, exist_ok=True)
        handler = logging.handlers.RotatingFileHandler(
            log_file,
            maxBytes=log_max_size * 1024 * 1024,
            backupCount=log_backup_count,
        )
        log_handlers.append(handler)
    else:
        log_handlers.append(logging.StreamHandler())

    logging.basicConfig(
        level=getattr(logging, log_level.upper(), logging.INFO),
        format=log_format,
        datefmt='%Y-%m-%dT%H:%M:%S',
        handlers=log_handlers,
    )

    aen_enabled = pick(
        cli_arg('aen_enabled'),
        env_bool(env_conf.get('aenEnabled')),
        yaml_conf.get('aenEnabled'),
        True,
    )
    if isinstance(aen_enabled, str):
        aen_enabled = aen_enabled.lower() in ('true', '1', 'yes')

    enable_pprof = pick(
        getattr(args, 'debug_enablepprof', None),
        env_bool(env_conf.get('debug.enablepprof')),
        debug_conf.get('enablepprof'),
        True,
    )
    if isinstance(enable_pprof, str):
        enable_pprof = enable_pprof.lower() in ('true', '1', 'yes')
    if enable_pprof:
        log.info('pprof not available in Python build (use py-spy for profiling)')

    daemon = DiscoveryDaemon(
        config_dir=config_dir,
        cache_file=cache_file,
        poll_interval=poll_interval,
        ctrl_loss_tmo=ctrl_loss_tmo,
        discovery_port=discovery_port,
        kato=kato,
        nr_io_queues=nr_io_queues,
        referral_ttl=referral_ttl,
        metrics_port=metrics_port,
        dhchap_secret=dhchap_secret,
        dhchap_ctrl_secret=dhchap_ctrl_secret,
        nvme_host_id_path=nvme_host_id_path,
        auto_detect_enabled=autodetect_enabled,
        auto_detect_filename=autodetect_filename,
    )
    daemon.run(aen_enabled=aen_enabled)


def main():
    """Entry point."""
    parser = build_parser()
    args = parser.parse_args()

    command = args.command

    # list ctrl is nested
    if command == 'list':
        if getattr(args, 'list_command', None) == 'ctrl':
            command = 'list_ctrl'
        else:
            parser.parse_args(['list', '--help'])
            return

    handlers = {
        'discover': cmd_discover,
        'connect': cmd_connect,
        'connect-all': cmd_connect_all,
        'disconnect': cmd_disconnect,
        'disconnect-all': cmd_disconnect_all,
        'list_ctrl': cmd_list_ctrl,
    }

    if command in handlers:
        sys.exit(handlers[command](args))

    # add-hostnqn and remove-hostnqn need config_dir
    if command in ('add-hostnqn', 'remove-hostnqn'):
        yaml_conf = load_yaml_config(args.config)
        env_conf = load_env_overrides()
        config_dir = (
            env_conf.get('clientConfigDir')
            or yaml_conf.get('clientConfigDir')
            or '/etc/discovery-client/discovery.d'
        )
        if command == 'add-hostnqn':
            sys.exit(cmd_add_hostnqn(args, config_dir))
        else:
            sys.exit(cmd_remove_hostnqn(args, config_dir))

    # Default: serve mode (command is None or 'serve')
    run_serve(args)


if __name__ == '__main__':
    main()
