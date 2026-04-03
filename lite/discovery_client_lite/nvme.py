"""NVMe-CLI wrapper and sysfs reader functions."""

import json
import logging
import subprocess
from pathlib import Path
from typing import Dict, List, Optional, Set, Tuple

from .models import ConnectedController

log = logging.getLogger('discovery-client-lite')


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
    def disconnect(device: str) -> bool:
        """Disconnect an NVMe controller by device name."""
        rc, _, stderr = NvmeCli._run(['disconnect', '-d', device])
        if rc != 0:
            log.error('Disconnect failed for %s: %s', device, stderr)
            return False
        return True

    @staticmethod
    def disconnect_by_nqn(nqn: str) -> bool:
        """Disconnect all controllers matching a subsystem NQN."""
        rc, _, stderr = NvmeCli._run(['disconnect', '-n', nqn])
        if rc != 0:
            log.error('Disconnect by NQN failed for %s: %s', nqn, stderr)
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
        """Disconnect all NVMe fabrics controllers."""
        rc, _, stderr = NvmeCli._run(['disconnect-all'])
        if rc != 0:
            log.error('Disconnect-all failed: %s', stderr)
            return False
        return True

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

NVME_FABRICS_CTL = Path('/sys/class/nvme-fabrics/ctl')


def get_connected_controllers() -> List[ConnectedController]:
    """Read currently connected NVMe fabrics controllers from sysfs.

    Uses /sys/class/nvme-fabrics/ctl/ which only contains fabrics
    controllers, avoiding the need to filter by transport.
    """
    controllers = []
    if not NVME_FABRICS_CTL.exists():
        return controllers

    for ctrl_path in sorted(NVME_FABRICS_CTL.iterdir()):
        if not ctrl_path.name.startswith('nvme'):
            continue
        try:
            transport = (ctrl_path / 'transport').read_text().strip()
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
DISCOVERY_CONF = '/etc/nvme/discovery.conf'


def _read_discovery_conf() -> List[str]:
    """Read lines from /etc/nvme/discovery.conf."""
    try:
        return Path(DISCOVERY_CONF).read_text().splitlines()
    except OSError:
        return []


def _write_discovery_conf(lines: List[str]):
    """Write lines to /etc/nvme/discovery.conf."""
    try:
        Path(DISCOVERY_CONF).write_text('\n'.join(lines) + '\n' if lines else '')
    except OSError as e:
        log.warning('Cannot write %s: %s', DISCOVERY_CONF, e)


DC_TAG_PREFIX = '# dc:'

NAME_COMMENT_PREFIX = '# name='


def append_discovery_conf(name: str, lines: List[str]):
    """Append lines to discovery.conf, each tagged with # name=<name>."""
    try:
        with open(DISCOVERY_CONF, 'a') as f:
            for line in lines:
                f.write('%s %s%s\n' % (line.rstrip(), NAME_COMMENT_PREFIX, name))
    except OSError as e:
        log.warning('Cannot append to %s: %s', DISCOVERY_CONF, e)


def remove_named_lines(name: str) -> int:
    """Remove lines tagged with # name=<name> from discovery.conf."""
    tag = '%s%s' % (NAME_COMMENT_PREFIX, name)
    existing = _read_discovery_conf()
    kept = [l for l in existing if tag not in l]
    removed = len(existing) - len(kept)
    if removed:
        _write_discovery_conf(kept)
        log.info('Removed %d lines for name=%s from %s', removed, name, DISCOVERY_CONF)
    return removed


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


def set_ctrl_loss_tmo_sysfs(value: int) -> int:
    """Set ctrl_loss_tmo on all TCP fabrics controllers via sysfs.

    Returns the number of controllers updated.
    """
    count = 0
    if not NVME_FABRICS_CTL.exists():
        return count
    for ctrl_path in sorted(NVME_FABRICS_CTL.iterdir()):
        if not ctrl_path.name.startswith('nvme'):
            continue
        try:
            (ctrl_path / 'ctrl_loss_tmo').write_text(str(value))
            count += 1
        except OSError:
            continue
    return count


def get_connected_hostids() -> Dict[str, str]:
    """Scan all NVMe fabrics controllers and return hostnqn -> hostid map.

    Matches the Go discovery-client's GetNvmfHosts() behavior: when
    controllers are already connected with a given hostnqn, the hostid
    they're using takes precedence over whatever the config says.
    """
    hosts: Dict[str, str] = {}
    if not NVME_FABRICS_CTL.exists():
        return hosts

    for ctrl_path in sorted(NVME_FABRICS_CTL.iterdir()):
        if not ctrl_path.name.startswith('nvme'):
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
