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
