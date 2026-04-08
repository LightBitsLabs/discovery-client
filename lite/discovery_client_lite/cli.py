"""CLI argument parser, command handlers, and run_serve entry point."""

import argparse
import json
import logging
import logging.handlers
import os
import sys
from pathlib import Path
from typing import Tuple

from .nvme import NvmeCli
from .config import load_yaml_config, parse_interval, load_env_overrides
from .daemon import DiscoveryDaemon

log = logging.getLogger('discovery-client-lite')


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
