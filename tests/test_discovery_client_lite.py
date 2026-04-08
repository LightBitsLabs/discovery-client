"""Tests for discovery-client-lite."""

import importlib.util
import os
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch, MagicMock

# Load the hyphenated module via importlib
_SCRIPT = os.path.join(os.path.dirname(__file__), '..', 'discovery-client-lite.py')
_spec = importlib.util.spec_from_file_location('dcl', _SCRIPT)
dcl = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(dcl)


class TestParseConfigLine(unittest.TestCase):

    def test_basic_line(self):
        ep = dcl.parse_config_line('-t tcp -a 10.0.0.1 -s 8009 -q hostnqn1 -n subnqn1')
        self.assertEqual(ep.traddr, '10.0.0.1')
        self.assertEqual(ep.port, '8009')
        self.assertEqual(ep.hostnqn, 'hostnqn1')
        self.assertEqual(ep.subnqn, 'subnqn1')

    def test_comment_line(self):
        self.assertIsNone(dcl.parse_config_line('# comment'))

    def test_empty_line(self):
        self.assertIsNone(dcl.parse_config_line(''))

    def test_missing_traddr(self):
        self.assertIsNone(dcl.parse_config_line('-t tcp -s 8009'))

    def test_default_port(self):
        ep = dcl.parse_config_line('-a 10.0.0.1')
        self.assertEqual(ep.port, '8009')

    def test_dhchap_flags(self):
        ep = dcl.parse_config_line('-a 10.0.0.1 -S secret1 -C ctrlsecret1')
        self.assertEqual(ep.secret, 'secret1')
        self.assertEqual(ep.ctrl_secret, 'ctrlsecret1')

    def test_hostid_flag(self):
        ep = dcl.parse_config_line('-a 10.0.0.1 -I uuid-123')
        self.assertEqual(ep.hostid, 'uuid-123')

    def test_ctrl_loss_tmo(self):
        ep = dcl.parse_config_line('-a 10.0.0.1 -l 600')
        self.assertEqual(ep.ctrl_loss_tmo, 600)


class TestParseInterval(unittest.TestCase):

    def test_int_value(self):
        self.assertEqual(dcl.parse_interval(5), 5)

    def test_seconds_string(self):
        self.assertEqual(dcl.parse_interval('5s'), 5)

    def test_minutes_string(self):
        self.assertEqual(dcl.parse_interval('1m'), 60)

    def test_plain_string(self):
        self.assertEqual(dcl.parse_interval('10'), 10)

    def test_negative_clamped(self):
        self.assertEqual(dcl.parse_interval(-1), 0)

    def test_invalid(self):
        self.assertEqual(dcl.parse_interval('abc'), 0)


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
        refs = dcl.extract_referrals(output)
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
        refs = dcl.extract_referrals(output)
        self.assertEqual(len(refs), 0)

    def test_empty_records(self):
        self.assertEqual(dcl.extract_referrals({}), [])


class TestSimpleHistogram(unittest.TestCase):

    def test_observe_increments_count(self):
        h = dcl.SimpleHistogram('test_hist', 'A test histogram')
        h.observe(0.5)
        h.observe(1.5)
        self.assertEqual(h.count, 2)

    def test_observe_tracks_sum(self):
        h = dcl.SimpleHistogram('test_hist', 'A test histogram')
        h.observe(0.1)
        h.observe(0.2)
        self.assertAlmostEqual(h.total, 0.3)

    def test_bucket_boundaries(self):
        h = dcl.SimpleHistogram('test_hist', 'A test histogram')
        h.observe(0.003)  # <= 0.005 bucket
        h.observe(0.5)    # <= 0.5 bucket
        h.observe(100.0)  # +Inf only
        rendered = h.render()
        self.assertIn('test_hist_bucket{le="0.005"} 1', rendered)
        self.assertIn('test_hist_bucket{le="0.5"} 2', rendered)
        self.assertIn('test_hist_bucket{le="+Inf"} 3', rendered)
        self.assertIn('test_hist_count 3', rendered)

    def test_render_format(self):
        h = dcl.SimpleHistogram('test_hist', 'A test histogram')
        rendered = h.render()
        self.assertIn('# HELP test_hist A test histogram', rendered)
        self.assertIn('# TYPE test_hist histogram', rendered)
        self.assertIn('test_hist_sum 0', rendered)
        self.assertIn('test_hist_count 0', rendered)


class TestMetrics(unittest.TestCase):

    def setUp(self):
        self.m = dcl.Metrics()

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
        result = dcl.load_env_overrides({})
        self.assertEqual(result, {})

    def test_clientconfigdir(self):
        env = {'DC_CLIENTCONFIGDIR': '/tmp/test-config'}
        result = dcl.load_env_overrides(env)
        self.assertEqual(result['clientConfigDir'], '/tmp/test-config')

    def test_nested_logging(self):
        env = {'DC_LOGGING_LEVEL': 'debug', 'DC_LOGGING_FILENAME': '/tmp/test.log'}
        result = dcl.load_env_overrides(env)
        self.assertEqual(result['logging.level'], 'debug')
        self.assertEqual(result['logging.filename'], '/tmp/test.log')

    def test_both_interval_names(self):
        env = {'DC_RECONNECTINTERVAL': '10s'}
        result = dcl.load_env_overrides(env)
        self.assertEqual(result['reconnectInterval'], '10s')

        env2 = {'DC_POLLINGINTERVAL': '15s'}
        result2 = dcl.load_env_overrides(env2)
        self.assertEqual(result2['pollingInterval'], '15s')

    def test_unrelated_env_ignored(self):
        env = {'HOME': '/home/user', 'PATH': '/usr/bin', 'DC_KATO': '20'}
        result = dcl.load_env_overrides(env)
        self.assertEqual(len(result), 1)
        self.assertEqual(result['kato'], '20')

    def test_autodetect_nested(self):
        env = {'DC_AUTODETECTENTRIES_ENABLED': 'false'}
        result = dcl.load_env_overrides(env)
        self.assertEqual(result['autoDetectEntries.enabled'], 'false')


class TestNvmeCliDiscover(unittest.TestCase):

    @patch.object(dcl.NvmeCli, '_run_json')
    def test_discover_builds_correct_args(self, mock_run):
        mock_run.return_value = {'records': []}
        dcl.NvmeCli.discover(
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

    @patch.object(dcl.NvmeCli, '_run_json')
    def test_discover_persistent_flag(self, mock_run):
        mock_run.return_value = {}
        dcl.NvmeCli.discover(
            traddr='10.0.0.1', trsvcid='8009', hostnqn='nqn.host1',
            persistent=True,
        )
        args = mock_run.call_args[0][0]
        self.assertIn('--persistent', args)


class TestNvmeCliConnect(unittest.TestCase):

    @patch.object(dcl.NvmeCli, '_run_json')
    def test_connect_builds_correct_args(self, mock_run):
        mock_run.return_value = {'cntlid': 1}
        dcl.NvmeCli.connect(
            traddr='10.0.0.1', trsvcid='4420', nqn='nqn.subsys1',
            transport='tcp',
        )
        args = mock_run.call_args[0][0]
        self.assertEqual(args[0], 'connect')
        self.assertIn('-a', args)
        self.assertIn('-n', args)
        self.assertIn('nqn.subsys1', args)

    @patch.object(dcl.NvmeCli, '_run_json')
    def test_connect_with_dhchap(self, mock_run):
        mock_run.return_value = {'cntlid': 1}
        dcl.NvmeCli.connect(
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
        self.assertTrue(hasattr(dcl.NvmeCli, 'disconnect_all'))
        self.assertTrue(callable(dcl.NvmeCli.disconnect_all))

    def test_list_controllers_exists(self):
        self.assertTrue(hasattr(dcl.NvmeCli, 'list_controllers'))
        self.assertTrue(callable(dcl.NvmeCli.list_controllers))


class TestParseIntervalHours(unittest.TestCase):

    def test_hours_string(self):
        self.assertEqual(dcl.parse_interval('96h'), 96 * 3600)

    def test_hours_to_days(self):
        hours = dcl.parse_interval('96h')
        self.assertEqual(hours // 3600, 96)

    def test_48h(self):
        self.assertEqual(dcl.parse_interval('48h'), 48 * 3600)


class TestGetHostId(unittest.TestCase):

    def test_custom_path(self):
        with tempfile.NamedTemporaryFile(mode='w', suffix='.hostid', delete=False) as f:
            f.write('custom-uuid-123\n')
            f.flush()
            result = dcl.get_host_id(f.name)
            self.assertEqual(result, 'custom-uuid-123')
            os.unlink(f.name)

    def test_missing_file(self):
        result = dcl.get_host_id('/nonexistent/path/hostid')
        self.assertEqual(result, '')


class TestArgparse(unittest.TestCase):
    """Test that subcommand parsers accept the correct flags."""

    def _parse(self, args_str):
        """Helper: build the parser and parse args."""
        return dcl.build_parser().parse_args(args_str.split())

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

    def test_connect_all_flags(self):
        args = self._parse('connect-all -a 10.0.0.1 -p -m 4 -k 10')
        self.assertEqual(args.command, 'connect-all')
        self.assertTrue(args.persistant)
        self.assertEqual(args.max_queues, 4)
        self.assertEqual(args.kato, 10)

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

    def tearDown(self):
        shutil.rmtree(self.config_dir)

    def test_creates_config_file(self):
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1:8009', '10.0.0.2:8009']
        args.hostnqn = 'nqn.host1'
        args.hostid = ''
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        rc = dcl.cmd_add_hostnqn(args, self.config_dir)
        self.assertEqual(rc, 0)

        filepath = os.path.join(self.config_dir, 'test-cluster')
        self.assertTrue(os.path.exists(filepath))
        content = open(filepath).read()
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

        rc = dcl.cmd_add_hostnqn(args, self.config_dir)
        self.assertEqual(rc, 1)

    def test_includes_hostid_when_provided(self):
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1:8009']
        args.hostnqn = 'nqn.host1'
        args.hostid = 'uuid-abc-123'
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        dcl.cmd_add_hostnqn(args, self.config_dir)
        content = open(os.path.join(self.config_dir, 'test-cluster')).read()
        self.assertIn('-I uuid-abc-123', content)

    def test_address_without_port(self):
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1']
        args.hostnqn = 'nqn.host1'
        args.hostid = ''
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        dcl.cmd_add_hostnqn(args, self.config_dir)
        content = open(os.path.join(self.config_dir, 'test-cluster')).read()
        self.assertIn('-s 8009', content)


class TestRemoveHostnqn(unittest.TestCase):

    def setUp(self):
        self.config_dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.config_dir)

    def test_removes_file(self):
        filepath = os.path.join(self.config_dir, 'test-cluster')
        with open(filepath, 'w') as f:
            f.write('-a 10.0.0.1 -s 8009\n')

        args = MagicMock()
        args.name = 'test-cluster'
        rc = dcl.cmd_remove_hostnqn(args, self.config_dir)
        self.assertEqual(rc, 0)
        self.assertFalse(os.path.exists(filepath))

    def test_missing_file_silent(self):
        args = MagicMock()
        args.name = 'nonexistent'
        rc = dcl.cmd_remove_hostnqn(args, self.config_dir)
        self.assertEqual(rc, 0)


class TestEndpointShuffling(unittest.TestCase):

    def test_poll_cycle_shuffles_endpoints(self):
        """Verify that endpoints are shuffled before failover iteration."""
        import inspect
        source = inspect.getsource(dcl.DiscoveryDaemon.poll_cycle)
        self.assertIn('random.shuffle', source)


class TestSplitHostPort(unittest.TestCase):

    def test_ip_with_port(self):
        self.assertEqual(dcl._split_host_port('10.0.0.1:8009'), ('10.0.0.1', '8009'))

    def test_ip_without_port(self):
        self.assertEqual(dcl._split_host_port('10.0.0.1'), ('10.0.0.1', '8009'))

    def test_ip_with_custom_port(self):
        self.assertEqual(dcl._split_host_port('10.0.0.1:4420'), ('10.0.0.1', '4420'))


class TestAddHostnqnCommaSplit(unittest.TestCase):

    def setUp(self):
        self.config_dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.config_dir)

    def test_comma_separated_addresses(self):
        """Comma-separated addresses must produce one line per address."""
        args = MagicMock()
        args.name = 'test-cluster'
        args.addresses = ['10.0.0.1:8009,10.0.0.2:8009,10.0.0.3:8009']
        args.hostnqn = 'nqn.host1'
        args.hostid = ''
        args.nqn = 'nqn.subsys1'
        args.transport = 'tcp'

        rc = dcl.cmd_add_hostnqn(args, self.config_dir)
        self.assertEqual(rc, 0)

        content = open(os.path.join(self.config_dir, 'test-cluster')).read()
        lines = [l for l in content.strip().split('\n') if l.strip()]
        self.assertEqual(len(lines), 3, f"Expected 3 lines, got {len(lines)}: {lines}")
        # Each line must have a clean IP in -a, not IP:PORT
        for line in lines:
            ep = dcl.parse_config_line(line)
            self.assertNotIn(':', ep.traddr, f"traddr should not contain port: {ep.traddr}")
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

        rc = dcl.cmd_add_hostnqn(args, self.config_dir)
        self.assertEqual(rc, 0)

        content = open(os.path.join(self.config_dir, 'test-cluster')).read()
        lines = [l for l in content.strip().split('\n') if l.strip()]
        self.assertEqual(len(lines), 2)


class TestParseConfigLineWithPort(unittest.TestCase):
    """Defense-in-depth: parse_config_line handles IP:PORT in -a field."""

    def test_addr_with_embedded_port_no_s_flag(self):
        ep = dcl.parse_config_line('-t tcp -a 10.0.0.1:8009 -q nqn.host1')
        self.assertEqual(ep.traddr, '10.0.0.1')
        self.assertEqual(ep.port, '8009')

    def test_addr_with_embedded_port_and_s_flag(self):
        """Explicit -s takes precedence over embedded port."""
        ep = dcl.parse_config_line('-t tcp -a 10.0.0.1:9999 -s 4420 -q nqn.host1')
        # -s was given explicitly, so traddr keeps the full value
        self.assertEqual(ep.port, '4420')

    def test_addr_without_port(self):
        ep = dcl.parse_config_line('-t tcp -a 10.0.0.1 -q nqn.host1')
        self.assertEqual(ep.traddr, '10.0.0.1')
        self.assertEqual(ep.port, '8009')


if __name__ == '__main__':
    unittest.main()
