"""Entry point for python -m discovery_client_lite."""

import sys

from discovery_client_lite.cli import build_parser, cmd_discover, cmd_connect, \
    cmd_disconnect, cmd_disconnect_all, cmd_set, cmd_list_ctrl, cmd_add_hostnqn, \
    cmd_remove_hostnqn, run_serve


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
        'disconnect': cmd_disconnect,
        'disconnect-all': cmd_disconnect_all,
        'set': cmd_set,
        'list_ctrl': cmd_list_ctrl,
    }

    if command in handlers:
        sys.exit(handlers[command](args))

    if command in ('add-hostnqn', 'remove-hostnqn'):
        if command == 'add-hostnqn':
            sys.exit(cmd_add_hostnqn(args))
        else:
            sys.exit(cmd_remove_hostnqn(args))

    # Default: serve mode (command is None or 'serve')
    run_serve(args)


if __name__ == '__main__':
    main()
