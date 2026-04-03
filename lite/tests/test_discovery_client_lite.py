"""Tests for discovery-client-lite."""

import os
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch, MagicMock

from discovery_client_lite.models import Endpoint
from discovery_client_lite.metrics import SimpleHistogram, Metrics
from discovery_client_lite.nvme import NvmeCli, get_host_id
from discovery_client_lite.config import parse_config_line, parse_interval, extract_referrals, load_env_overrides
from discovery_client_lite.cli import build_parser, cmd_add_hostnqn, cmd_remove_hostnqn, _split_host_port
from discovery_client_lite.daemon import DiscoveryDaemon


class TestParseConfigLine(unittest.TestCase):

    def test_basic_line(self):
        ep = parse_config_line('-t tcp -a 10.0.0.1 -s 8009 -q hostnqn1 -n subnqn1')
        self.assertEqual(ep.traddr, '10.0.0.1')
        self.assertEqual(ep.port, '8009')
        self.assertEqual(ep.hostnqn, 'hostnqn1')
        self.assertEqual(ep.subnqn, 'subnqn1')

    def test_comment_line(self):
        self.assertIsNone(parse_config_line('# comment'))

    def test_empty_line(self):
        self.assertIsNone(parse_config_line(''))

    def test_missing_traddr(self):
        self.assertIsNone(parse_config_line('-t tcp -s 8009'))

    def test_default_port(self):
        ep = parse_config_line('-a 10.0.0.1')
        self.assertEqual(ep.port, '8009')

    def test_dhchap_flags(self):
        ep = parse_config_line('-a 10.0.0.1 -S secret1 -C ctrlsecret1')
        self.assertEqual(ep.secret, 'secret1')
        self.assertEqual(ep.ctrl_secret, 'ctrlsecret1')

    def test_hostid_flag(self):
        ep = parse_config_line('-a 10.0.0.1 -I uuid-123')
        self.assertEqual(ep.hostid, 'uuid-123')

    def test_ctrl_loss_tmo(self):
        ep = parse_config_line('-a 10.0.0.1 -l 600')
        self.assertEqual(ep.ctrl_loss_tmo, 600)


class TestParseInterval(unittest.TestCase):

    def test_int_value(self):
        self.assertEqual(parse_interval(5), 5)

    def test_seconds_string(self):
        self.assertEqual(parse_interval('5s'), 5)

    def test_minutes_string(self):
        self.assertEqual(parse_interval('1m'), 60)

    def test_plain_string(self):
        self.assertEqual(parse_interval('10'), 10)

    def test_negative_clamped(self):
        self.assertEqual(parse_interval(-1), 0)

    def test_invalid(self):
        self.assertEqual(parse_interval('abc'), 0)


class TestExtractReferrals(unittest.TestCase):

    def test_discovery_subtype(self):
        output = {
            'records': [
                {
                    'subtype': 'discovery service referral',
                    'subnqn': 'nqn.2014-08.org.nvmexpress.discovery',
                    'traddr': '10.0.0.2',
                    'trsvcid': '8009',
                },
            ]
        }
        refs = extract_referrals(output)
        self.assertEqual(len(refs), 1)
        self.assertEqual(refs[0].traddr, '10.0.0.2')

    def test_nvme_subtype_ignored(self):
        output = {
            'records': [
                {
                    'subtype': 'nvme subsystem',
                    'subnqn': 'nqn.io.lightbits:subsys1',
                    'traddr': '10.0.0.3',
                    'trsvcid': '4420',
                },
            ]
        }
        refs = extract_referrals(output)
        self.assertEqual(len(refs), 0)

    def test_empty_records(self):
        self.assertEqual(extract_referrals({}), [])


class TestSimpleHistogram(unittest.TestCase):

    def test_observe_increments_count(self):
        h = SimpleHistogram('test_hist', 'A test histogram')
        h.observe(0.5)
        h.observe(1.5)
        self.assertEqual(h.count, 2)

    def test_observe_tracks_sum(self):
        h = SimpleHistogram('test_hist', 'A test histogram')
        h.observe(0.1)
        h.observe(0.2)
        self.assertAlmostEqual(h.total, 0.3)

    def test_bucket_boundaries(self):
        h = SimpleHistogram('test_hist', 'A test histogram')
        h.observe(0.003)  # <= 0.005 bucket
        h.observe(0.5)    # <= 0.5 bucket
        h.observe(100.0)  # +Inf only
        rendered = h.render()
        self.assertIn('test_hist_bucket{le="0.005"} 1', rendered)
        self.assertIn('test_hist_bucket{le="0.5"} 2', rendered)
        self.assertIn('test_hist_bucket{le="+Inf"} 3', rendered)
        self.assertIn('test_hist_count 3', rendered)

    def test_render_format(self):
        h = SimpleHistogram('test_hist', 'A test histogram')
        rendered = h.render()
        self.assertIn('# HELP test_hist A test histogram', rendered)
        self.assertIn('# TYPE test_hist histogram', rendered)
        self.assertIn('test_hist_sum 0', rendered)
        self.assertIn('test_hist_count 0', rendered)


class TestMetrics(unittest.TestCase):

    def setUp(self):
        self.m = Metrics()

    def test_render_uses_discovery_prefix(self):
        rendered = self.m.render()
        self.assertNotIn('dc_', rendered)
        self.assertIn('discovery_', rendered)

    def test_poll_cycle_duration_is_histogram(self):
        self.m.poll_cycle_duration.observe(0.05)
        rendered = self.m.render()
        self.assertIn('discovery_poll_cycle_duration_seconds_bucket', rendered)
        self.assertIn('discovery_poll_cycle_duration_seconds_sum', rendered)
        self.assertIn('discovery_poll_cycle_duration_seconds_count', rendered)

    def test_per_hostnqn_labels(self):
        self.m.targets_per_hostnqn = {'nqn.host1': 3, 'nqn.host2': 5}
        rendered = self.m.render()
        self.assertIn('discovery_targets_per_hostnqn_total{hostnqn="nqn.host1"} 3', rendered)
        self.assertIn('discovery_targets_per_hostnqn_total{hostnqn="nqn.host2"} 5', rendered)

    def test_tcp_server_serving_states(self):
        rendered = self.m.render()
        self.assertIn('discovery_tcp_server_serving_states', rendered)

    def test_targets_map_id(self):
        self.m.targets_map_id += 1
        rendered = self.m.render()
        self.assertIn('discovery_targets_map_id 1', rendered)

    def test_aen_sent_total_emitted_as_zero(self):
        rendered = self.m.render()
        self.assertIn('discovery_aen_sent_total 0', rendered)


class TestLoadEnvOverrides(unittest.TestCase):

    def test_empty_env(self):
        result = load_env_overrides({})
        self.assertEqual(result, {})

    def test_clientconfigdir(self):
        # DC_CLIENTCONFIGDIR was removed in the simplified daemon; verify it is
        # not recognized (no entry should be returned for it).
        env = {'DC_CLIENTCONFIGDIR': '/tmp/test-config'}
        result = load_env_overrides(env)
        self.assertNotIn('clientConfigDir', result)

    def test_nested_logging(self):
        env = {'DC_LOGGING_LEVEL': 'debug', 'DC_LOGGING_FILENAME': '/tmp/test.log'}
        result = load_env_overrides(env)
        self.assertEqual(result['logging.level'], 'debug')
        self.assertEqual(result['logging.filename'], '/tmp/test.log')

    def test_both_interval_names(self):
        env = {'DC_RECONNECTINTERVAL': '10s'}
        result = load_env_overrides(env)
        self.assertEqual(result['reconnectInterval'], '10s')

        env2 = {'DC_POLLINGINTERVAL': '15s'}
        result2 = load_env_overrides(env2)
        self.assertEqual(result2['pollingInterval'], '15s')

    def test_unrelated_env_ignored(self):
        env = {'HOME': '/home/user', 'PATH': '/usr/bin', 'DC_KATO': '20'}
        result = load_env_overrides(env)
        self.assertEqual(len(result), 1)
        self.assertEqual(result['kato'], '20')

    def test_autodetect_nested(self):
        # DC_AUTODETECTENTRIES_ENABLED was removed in the simplified daemon;
        # verify it is not recognized.
        env = {'DC_AUTODETECTENTRIES_ENABLED': 'false'}
        result = load_env_overrides(env)
        self.assertNotIn('autoDetectEntries.enabled', result)


class TestNvmeCliDiscover(unittest.TestCase):

    @patch.object(NvmeCli, '_run_json')
    def test_discover_builds_correct_args(self, mock_run):
        mock_run.return_value = {'records': []}
        NvmeCli.discover(
            traddr='10.0.0.1', trsvcid='8009', hostnqn='nqn.host1',
            transport='tcp',
        )
        args = mock_run.call_args[0][0]
        self.assertEqual(args[0], 'discover')
        self.assertIn('-a', args)
        self.assertIn('10.0.0.1', args)
        self.assertIn('-s', args)
        self.assertIn('8009', args)
        self.assertIn('-q', args)
        self.assertIn('nqn.host1', args)

    @patch.object(NvmeCli, '_run_json')
    def test_discover_persistent_flag(self, mock_run):
        mock_run.return_value = {}
        NvmeCli.discover(
            traddr='10.0.0.1', trsvcid='8009', hostnqn='nqn.host1',
            persistent=True,
        )
        args = mock_run.call_args[0][0]
        self.assertIn('--persistent', args)


class TestNvmeCliConnect(unittest.TestCase):

    @patch.object(NvmeCli, '_run_json')
    def test_connect_builds_correct_args(self, mock_run):
        mock_run.return_value = {'cntlid': 1}
        NvmeCli.connect(
            traddr='10.0.0.1', trsvcid='4420', nqn='nqn.subsys1',
            transport='tcp',
        )
        args = mock_run.call_args[0][0]
        self.assertEqual(args[0], 'connect')
        self.assertIn('-a', args)
        self.assertIn('-n', args)
        self.assertIn('nqn.subsys1', args)

    @patch.object(NvmeCli, '_run_json')
    def test_connect_with_dhchap(self, mock_run):
        mock_run.return_value = {'cntlid': 1}
        NvmeCli.connect(
            traddr='10.0.0.1', trsvcid='4420', nqn='nqn.subsys1',
            dhchap_secret='secret1', dhchap_ctrl_secret='ctrl1',
        )
        args = mock_run.call_args[0][0]
        self.assertIn('-S', args)
        self.assertIn('secret1', args)
        self.assertIn('-C', args)
        self.assertIn('ctrl1', args)


class TestNvmeCliMethods(unittest.TestCase):

    def test_disconnect_all_exists(self):
        self.assertTrue(hasattr(NvmeCli, 'disconnect_all'))
        self.assertTrue(callable(NvmeCli.disconnect_all))

    def test_list_controllers_exists(self):
        self.assertTrue(hasattr(NvmeCli, 'list_controllers'))
        self.assertTrue(callable(NvmeCli.list_controllers))


class TestParseIntervalHours(unittest.TestCase):

    def test_hours_string(self):
        self.assertEqual(parse_interval('96h'), 96 * 3600)

    def test_hours_to_days(self):
        hours = parse_interval('96h')
        self.assertEqual(hours // 3600, 96)

    def test_48h(self):
        self.assertEqual(parse_interval('48h'), 48 * 3600)


class TestGetHostId(unittest.TestCase):

    def test_custom_path(self):
        with tempfile.NamedTemporaryFile(mode='w', suffix='.hostid', delete=False) as f:
            f.write('custom-uuid-123\n')
            f.flush()
            result = get_host_id(f.name)
            self.assertEqual(result, 'custom-uuid-123')
            os.unlink(f.name)

    def test_missing_file(self):
        result = get_host_id('/nonexistent/path/hostid')
        self.assertEqual(result, '')


class TestArgparse(unittest.TestCase):
    """Test that subcommand parsers accept the correct flags."""

    def _parse(self, args_str):
        """Helper: build the parser and parse args."""
        return build_parser().parse_args(args_str.split())

    def test_discover_flags(self):
        args = self._parse('discover -a 10.0.0.1 -q nqn.host1')
        self.assertEqual(args.command, 'discover')
        self.assertEqual(args.traddr, '10.0.0.1')
        self.assertEqual(args.hostnqn, 'nqn.host1')
        self.assertEqual(args.trsvcid, 8009)
        self.assertEqual(args.transport, 'tcp')

    def test_connect_flags(self):
        args = self._parse('connect -a 10.0.0.1 -n nqn.subsys1')
        self.assertEqual(args.command, 'connect')
        self.assertEqual(args.trsvcid, 4420)
        self.assertEqual(args.nqn, 'nqn.subsys1')

    def test_disconnect_flag(self):
        args = self._parse('disconnect -d /dev/nvme0')
        self.assertEqual(args.command, 'disconnect')
        self.assertEqual(args.device, '/dev/nvme0')

    def test_disconnect_all(self):
        args = self._parse('disconnect-all')
        self.assertEqual(args.command, 'disconnect-all')

    def test_add_hostnqn_flags(self):
        args = self._parse('add-hostnqn --name myconfig -a 10.0.0.1:8009 -q nqn.host1 -n nqn.subsys1')
        self.assertEqual(args.command, 'add-hostnqn')
        self.assertEqual(args.name, 'myconfig')
        self.assertEqual(args.addresses, ['10.0.0.1:8009'])

    def test_remove_hostnqn_flag(self):
        args = self._parse('remove-hostnqn -n myconfig')
        self.assertEqual(args.command, 'remove-hostnqn')
        self.assertEqual(args.name, 'myconfig')

    def test_list_ctrl(self):
        args = self._parse('list ctrl')
        self.assertEqual(args.command, 'list')
        self.assertEqual(args.list_command, 'ctrl')

    def test_list_ctrl_discovery_flag(self):
        args = self._parse('list ctrl -d')
        self.assertEqual(args.command, 'list')
        self.assertEqual(args.list_command, 'ctrl')
        self.assertTrue(args.discovery)

    def test_no_command_defaults_to_serve(self):
        args = self._parse('--config /tmp/test.yaml')
        self.assertIsNone(args.command)

    def test_global_config_flag(self):
        args = self._parse('discover -a 10.0.0.1 -q nqn.host1 --config /tmp/c.yaml')
        self.assertEqual(args.config, '/tmp/c.yaml')

    def test_connect_dhchap(self):
        args = self._parse('connect -a 10.0.0.1 -n nqn.subsys1 -S secret -C ctrl_secret')
        self.assertEqual(args.dhchap_secret, 'secret')
        self.assertEqual(args.dhchap_ctrl_secret, 'ctrl_secret')

    def test_discover_persistant_typo(self):
        args = self._parse('discover -a 10.0.0.1 -q nqn.host1 -p')
        self.assertTrue(args.persistant)


class TestAddHostnqn(unittest.TestCase):

    def setUp(self):
        self.config_dir = tempfile.mkdtemp()
        self.conf_file = os.path.join(self.config_dir, 'discovery.conf')
        # Redirect DISCOVERY_CONF to temp file
        import discovery_client_lite.nvme as _nvme
        self._orig_conf = _nvme.DISCOVERY_CONF
        _nvme.DISCOVERY_CONF = self.conf_file

    def tearDown(self):
        import discovery_client_lite.nvme as _nvme
        _nvme.DISCOVERY_CONF = self._orig_conf
        shutil.rmtree(self.config_dir)

    def _read_conf(self):
        return open(self.conf_file).read()

    def test_creates_config_entry(self):
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1:8009', '10.0.0.2:8009']
        args.hostnqn = 'nqn.host1'
        args.hostid = ''
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        rc = cmd_add_hostnqn(args)
        self.assertEqual(rc, 0)

        content = self._read_conf()
        self.assertIn('# name=test-cluster', content)
        self.assertIn('-a 10.0.0.1', content)
        self.assertIn('-a 10.0.0.2', content)
        self.assertIn('-q nqn.host1', content)
        self.assertIn('-n nqn.subsys1', content)

    def test_rejects_reserved_prefix(self):
        args = MagicMock()
        args.name = 'tmp.dc.badname'
        args.addresses = ['10.0.0.1:8009']
        args.hostnqn = 'nqn.host1'
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'
        args.hostid = ''

        rc = cmd_add_hostnqn(args)
        self.assertEqual(rc, 1)

    def test_includes_hostid_when_provided(self):
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1:8009']
        args.hostnqn = 'nqn.host1'
        args.hostid = 'uuid-abc-123'
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        cmd_add_hostnqn(args)
        self.assertIn('-I uuid-abc-123', self._read_conf())

    def test_address_without_port(self):
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1']
        args.hostnqn = 'nqn.host1'
        args.hostid = ''
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        cmd_add_hostnqn(args)
        self.assertIn('-s 8009', self._read_conf())


class TestRemoveHostnqn(unittest.TestCase):

    def setUp(self):
        self.config_dir = tempfile.mkdtemp()
        self.conf_file = os.path.join(self.config_dir, 'discovery.conf')
        import discovery_client_lite.nvme as _nvme
        self._orig_conf = _nvme.DISCOVERY_CONF
        _nvme.DISCOVERY_CONF = self.conf_file

    def tearDown(self):
        import discovery_client_lite.nvme as _nvme
        _nvme.DISCOVERY_CONF = self._orig_conf
        shutil.rmtree(self.config_dir)

    def test_removes_tagged_section(self):
        # Pre-populate discovery.conf with lines tagged with # name=test-cluster
        with open(self.conf_file, 'w') as f:
            f.write('-t tcp -a 10.0.0.1 -s 8009 -q nqn.host1 -n nqn.subsys1 # name=test-cluster\n')

        args = MagicMock()
        args.name = 'test-cluster'
        rc = cmd_remove_hostnqn(args)
        self.assertEqual(rc, 0)
        content = open(self.conf_file).read().strip()
        self.assertEqual(content, '')

    def test_missing_section_silent(self):
        args = MagicMock()
        args.name = 'nonexistent'
        rc = cmd_remove_hostnqn(args)
        self.assertEqual(rc, 0)


class TestEndpointShuffling(unittest.TestCase):

    def test_poll_cycle_shuffles_endpoints(self):
        """Verify that endpoints are shuffled before failover iteration."""
        import inspect
        source = inspect.getsource(DiscoveryDaemon.poll_cycle)
        self.assertIn('random.shuffle', source)


class TestSplitHostPort(unittest.TestCase):

    def test_ip_with_port(self):
        self.assertEqual(_split_host_port('10.0.0.1:8009'), ('10.0.0.1', '8009'))

    def test_ip_without_port(self):
        self.assertEqual(_split_host_port('10.0.0.1'), ('10.0.0.1', '8009'))

    def test_ip_with_custom_port(self):
        self.assertEqual(_split_host_port('10.0.0.1:4420'), ('10.0.0.1', '4420'))


class TestAddHostnqnCommaSplit(unittest.TestCase):

    def setUp(self):
        self.config_dir = tempfile.mkdtemp()
        self.conf_file = os.path.join(self.config_dir, 'discovery.conf')
        import discovery_client_lite.nvme as _nvme
        self._orig_conf = _nvme.DISCOVERY_CONF
        _nvme.DISCOVERY_CONF = self.conf_file

    def tearDown(self):
        import discovery_client_lite.nvme as _nvme
        _nvme.DISCOVERY_CONF = self._orig_conf
        shutil.rmtree(self.config_dir)

    def _endpoint_lines(self):
        content = open(self.conf_file).read()
        return [l for l in content.strip().split('\n')
                if l.strip() and not l.strip().startswith('#')]

    def test_comma_separated_addresses(self):
        """Comma-separated addresses must produce one line per address."""
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1:8009,10.0.0.2:8009,10.0.0.3:8009']
        args.hostnqn = 'nqn.host1'
        args.hostid = ''
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        rc = cmd_add_hostnqn(args)
        self.assertEqual(rc, 0)

        lines = self._endpoint_lines()
        self.assertEqual(len(lines), 3)
        for line in lines:
            ep = parse_config_line(line)
            self.assertNotIn(':', ep.traddr)
            self.assertEqual(ep.port, '8009')

    def test_space_separated_addresses(self):
        """Space-separated addresses (nargs='+') work too."""
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1:8009', '10.0.0.2:8009']
        args.hostnqn = 'nqn.host1'
        args.hostid = ''
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        rc = cmd_add_hostnqn(args)
        self.assertEqual(rc, 0)

        lines = self._endpoint_lines()
        self.assertEqual(len(lines), 2)


class TestParseConfigLineWithPort(unittest.TestCase):
    """Defense-in-depth: parse_config_line handles IP:PORT in -a field."""

    def test_addr_with_embedded_port_no_s_flag(self):
        ep = parse_config_line('-t tcp -a 10.0.0.1:8009 -q nqn.host1')
        self.assertEqual(ep.traddr, '10.0.0.1')
        self.assertEqual(ep.port, '8009')

    def test_addr_with_embedded_port_and_s_flag(self):
        """Explicit -s takes precedence over embedded port."""
        ep = parse_config_line('-t tcp -a 10.0.0.1:9999 -s 4420 -q nqn.host1')
        # -s was given explicitly, so traddr keeps the full value
        self.assertEqual(ep.port, '4420')

    def test_addr_without_port(self):
        ep = parse_config_line('-t tcp -a 10.0.0.1 -q nqn.host1')
        self.assertEqual(ep.traddr, '10.0.0.1')
        self.assertEqual(ep.port, '8009')


class TestReadDiscoveryConf(unittest.TestCase):
    """Tests for the flat read_discovery_conf returning List[Endpoint]."""

    def setUp(self):
        self.tmp = tempfile.NamedTemporaryFile(mode='w', suffix='.conf', delete=False)
        self.tmp_path = self.tmp.name

    def tearDown(self):
        os.unlink(self.tmp_path)

    def _write(self, content):
        with open(self.tmp_path, 'w') as f:
            f.write(content)

    def test_returns_list_of_endpoints(self):
        from discovery_client_lite.config import read_discovery_conf
        self._write('-t tcp -a 10.0.0.1 -s 8009 -q nqn.host1 -n nqn.subsys1\n')
        result = read_discovery_conf(self.tmp_path)
        self.assertIsInstance(result, list)
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0].traddr, '10.0.0.1')
        self.assertEqual(result[0].port, '8009')
        self.assertEqual(result[0].hostnqn, 'nqn.host1')

    def test_name_comment_ignored_by_parser(self):
        from discovery_client_lite.config import read_discovery_conf
        self._write(
            '-t tcp -a 10.0.0.1 -s 8009 -q nqn.host1 -n nqn.subsys1 # name=test-cluster\n'
            '-t tcp -a 10.0.0.2 -s 8009 -q nqn.host1 -n nqn.subsys1 # name=test-cluster\n'
        )
        result = read_discovery_conf(self.tmp_path)
        self.assertEqual(len(result), 2)
        self.assertEqual(result[0].traddr, '10.0.0.1')
        self.assertEqual(result[1].traddr, '10.0.0.2')

    def test_empty_file_returns_empty_list(self):
        from discovery_client_lite.config import read_discovery_conf
        self._write('')
        result = read_discovery_conf(self.tmp_path)
        self.assertEqual(result, [])

    def test_comment_only_lines_skipped(self):
        from discovery_client_lite.config import read_discovery_conf
        self._write('# This is a comment\n# Another comment\n')
        result = read_discovery_conf(self.tmp_path)
        self.assertEqual(result, [])

    def test_missing_file_returns_empty_list(self):
        from discovery_client_lite.config import read_discovery_conf
        result = read_discovery_conf('/nonexistent/path/discovery.conf')
        self.assertEqual(result, [])

    def test_multiple_endpoints(self):
        from discovery_client_lite.config import read_discovery_conf
        self._write(
            '-t tcp -a 10.0.0.1 -s 8009 -q nqn.h1 -n nqn.s1\n'
            '-t tcp -a 10.0.0.2 -s 8009 -q nqn.h2 -n nqn.s2\n'
            '-t tcp -a 10.0.0.3 -s 8009 -q nqn.h3 -n nqn.s3\n'
        )
        result = read_discovery_conf(self.tmp_path)
        self.assertEqual(len(result), 3)
        addrs = [ep.traddr for ep in result]
        self.assertIn('10.0.0.1', addrs)
        self.assertIn('10.0.0.2', addrs)
        self.assertIn('10.0.0.3', addrs)


class TestSetCtrlLossTmoSysfs(unittest.TestCase):
    """Tests for the sysfs ctrl_loss_tmo helper."""

    def setUp(self):
        self.sysfs = tempfile.mkdtemp()
        # Create fake fabrics controller directories (all are fabrics — no pcie)
        for name in ('nvme0', 'nvme1', 'nvme2'):
            d = os.path.join(self.sysfs, name)
            os.makedirs(d)
            with open(os.path.join(d, 'ctrl_loss_tmo'), 'w') as f:
                f.write('3600\n')

    def tearDown(self):
        shutil.rmtree(self.sysfs)

    def test_sets_only_tcp_controllers(self):
        from discovery_client_lite.nvme import set_ctrl_loss_tmo_sysfs
        real_path = __import__('pathlib').Path
        with patch('discovery_client_lite.nvme.NVME_FABRICS_CTL',
                   real_path(self.sysfs)):
            count = set_ctrl_loss_tmo_sysfs(1)

        self.assertEqual(count, 3)
        for name in ('nvme0', 'nvme1', 'nvme2'):
            with open(os.path.join(self.sysfs, name, 'ctrl_loss_tmo')) as f:
                self.assertEqual(f.read().strip(), '1')

    def test_returns_zero_when_no_controllers(self):
        empty = tempfile.mkdtemp()
        try:
            from discovery_client_lite.nvme import set_ctrl_loss_tmo_sysfs
            real_path = __import__('pathlib').Path
            with patch('discovery_client_lite.nvme.NVME_FABRICS_CTL',
                       real_path(empty)):
                count = set_ctrl_loss_tmo_sysfs(1)
            self.assertEqual(count, 0)
        finally:
            shutil.rmtree(empty)


class TestControlSocket(unittest.TestCase):
    """Tests for the daemon control socket interface."""

    def _make_daemon(self):
        """Create a daemon with mocked externals."""
        with patch('discovery_client_lite.daemon.get_host_id', return_value='test-id'):
            d = DiscoveryDaemon(
                cache_file='/dev/null',
                poll_interval=5,
                ctrl_loss_tmo=3600,
            )
        return d

    def test_handle_set_ctrl_loss_tmo(self):
        daemon = self._make_daemon()
        with patch('discovery_client_lite.daemon.set_ctrl_loss_tmo_sysfs', return_value=5) as mock_sysfs:
            result = daemon.handle_control_command(
                {'command': 'set', 'key': 'ctrl_loss_tmo', 'value': 1}
            )
        self.assertTrue(result['ok'])
        self.assertEqual(daemon.ctrl_loss_tmo, 1)
        mock_sysfs.assert_called_once_with(1)

    def test_handle_set_invalid_key(self):
        daemon = self._make_daemon()
        result = daemon.handle_control_command(
            {'command': 'set', 'key': 'bogus', 'value': 1}
        )
        self.assertFalse(result['ok'])
        self.assertIn('unknown key', result['error'])

    def test_handle_unknown_command(self):
        daemon = self._make_daemon()
        result = daemon.handle_control_command({'command': 'foobar'})
        self.assertFalse(result['ok'])
        self.assertIn('unknown command', result['error'])

    def test_handle_set_ctrl_loss_tmo_bad_value(self):
        daemon = self._make_daemon()
        result = daemon.handle_control_command(
            {'command': 'set', 'key': 'ctrl_loss_tmo', 'value': 'not_a_number'}
        )
        self.assertFalse(result['ok'])
        self.assertIn('integer', result['error'])


class TestControlSocketRoundTrip(unittest.TestCase):
    """Tests the control socket listener end-to-end."""

    def test_socket_round_trip(self):
        import json
        import socket
        import discovery_client_lite.daemon as daemon_mod
        import time

        sock_path = os.path.join(tempfile.mkdtemp(), 'test.sock')
        orig_path = daemon_mod.CONTROL_SOCKET_PATH
        daemon_mod.CONTROL_SOCKET_PATH = sock_path

        mock_daemon = MagicMock()
        mock_daemon.handle_control_command.return_value = {'ok': True, 'message': 'done'}
        running = True

        try:
            thread = daemon_mod.start_control_listener(mock_daemon, lambda: running)
            if thread is None:
                self.skipTest('Cannot create control socket')

            time.sleep(0.1)
            s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            s.settimeout(2)
            s.connect(sock_path)
            s.sendall(json.dumps({'command': 'set', 'key': 'ctrl_loss_tmo', 'value': 1}).encode())
            response = json.loads(s.recv(4096).decode())
            s.close()

            self.assertTrue(response['ok'])
            mock_daemon.handle_control_command.assert_called_once_with(
                {'command': 'set', 'key': 'ctrl_loss_tmo', 'value': 1}
            )
        finally:
            running = False
            time.sleep(1.5)
            daemon_mod.CONTROL_SOCKET_PATH = orig_path
            try:
                os.unlink(sock_path)
                os.rmdir(os.path.dirname(sock_path))
            except OSError:
                pass


class TestShutdownDisconnects(unittest.TestCase):
    """Verify daemon shutdown disconnects discovery controllers."""

    @patch('discovery_client_lite.daemon.sd_notify')
    @patch.object(NvmeCli, 'disconnect_by_nqn')
    @patch('discovery_client_lite.daemon.start_aen_listener')
    @patch('discovery_client_lite.daemon.start_control_listener')
    @patch('discovery_client_lite.daemon.load_referral_cache', return_value=[])
    @patch('discovery_client_lite.daemon.start_metrics_server')
    @patch('discovery_client_lite.daemon.get_connected_controllers', return_value=[])
    @patch('discovery_client_lite.daemon.read_discovery_conf', return_value=[])
    def test_run_disconnects_on_shutdown(
        self, _read_disc, _get_conn, _metrics, _cache,
        _ctrl_listener, _aen, mock_disconnect, _notify
    ):
        with patch('discovery_client_lite.daemon.get_host_id', return_value='test-id'):
            daemon = DiscoveryDaemon(
                cache_file='/dev/null',
                poll_interval=5,
                ctrl_loss_tmo=3600,
            )
        # Simulate immediate shutdown
        daemon.running = False
        daemon.run(aen_enabled=False)

        mock_disconnect.assert_called_once()


class TestCmdSet(unittest.TestCase):
    """Tests for the 'set' CLI subcommand."""

    def test_no_setting_returns_error(self):
        from discovery_client_lite.cli import cmd_set
        args = MagicMock()
        args.ctrl_loss_tmo = None
        rc = cmd_set(args)
        self.assertEqual(rc, 1)

    @patch('discovery_client_lite.cli._socket')
    def test_sends_request_to_daemon(self, mock_sock_mod):
        from discovery_client_lite.cli import cmd_set
        import json

        mock_conn = MagicMock()
        mock_conn.recv.return_value = json.dumps({'ok': True, 'message': 'done'}).encode()
        mock_sock_mod.socket.return_value = mock_conn
        mock_sock_mod.AF_UNIX = __import__('socket').AF_UNIX
        mock_sock_mod.SOCK_STREAM = __import__('socket').SOCK_STREAM

        args = MagicMock()
        args.ctrl_loss_tmo = 1
        rc = cmd_set(args)
        self.assertEqual(rc, 0)

        # Verify the JSON request sent
        sent_data = mock_conn.sendall.call_args[0][0]
        request = json.loads(sent_data.decode())
        self.assertEqual(request['command'], 'set')
        self.assertEqual(request['key'], 'ctrl_loss_tmo')
        self.assertEqual(request['value'], 1)

    @patch('discovery_client_lite.cli._socket')
    def test_daemon_unreachable(self, mock_sock_mod):
        from discovery_client_lite.cli import cmd_set

        mock_conn = MagicMock()
        mock_conn.connect.side_effect = ConnectionRefusedError('no daemon')
        mock_sock_mod.socket.return_value = mock_conn
        mock_sock_mod.AF_UNIX = __import__('socket').AF_UNIX
        mock_sock_mod.SOCK_STREAM = __import__('socket').SOCK_STREAM

        args = MagicMock()
        args.ctrl_loss_tmo = 1
        rc = cmd_set(args)
        self.assertEqual(rc, 1)


if __name__ == '__main__':
    unittest.main()
