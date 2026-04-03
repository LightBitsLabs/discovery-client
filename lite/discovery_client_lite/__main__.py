"""Entry point for python -m discovery_client_lite."""

import sys

from discovery_client_lite.cli import build_parser, cmd_discover, cmd_connect, cmd_connect_all, \
    cmd_disconnect, cmd_disconnect_all, cmd_list_ctrl, cmd_add_hostnqn, cmd_remove_hostnqn, \
    run_serve
from discovery_client_lite.config import load_yaml_config, load_env_overrides


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
