"""Config parsing, referral cache, and environment overrides."""

import json
import logging
import os
import time
from dataclasses import asdict
from pathlib import Path
from typing import Dict, List, Optional

from .models import Endpoint, CachedReferral
from .nvme import get_connected_controllers, DISCOVERY_SUBNQN

log = logging.getLogger('discovery-client-lite')


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
