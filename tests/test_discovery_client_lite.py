"""Tests for discovery-client-lite."""

import importlib.util
import os
import shutil
import tempfile
import unittest
from unittest.mock import patch, call

# Load the hyphenated module via importlib
_SCRIPT = os.path.join(os.path.dirname(__file__), '..', 'discovery-client-lite.py')
_spec = importlib.util.spec_from_file_location('dcl', _SCRIPT)
dcl = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(dcl)


class TestParseConfigLine(unittest.TestCase):

    def test_basic_line(self):
        result = dcl.parse_config_line('-t tcp -a 10.0.0.1 -s 8009 -q hostnqn1 -n subnqn1')
        self.assertIn('-t tcp', result)
        self.assertIn('-a 10.0.0.1', result)
        self.assertIn('-s 8009', result)
        self.assertIn('-q hostnqn1', result)

    def test_subnqn_stripped(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -n subnqn1')
        self.assertNotIn('-n subnqn1', result)

    def test_comment_line(self):
        self.assertIsNone(dcl.parse_config_line('# comment'))

    def test_empty_line(self):
        self.assertIsNone(dcl.parse_config_line(''))

    def test_whitespace_only(self):
        self.assertIsNone(dcl.parse_config_line('   '))

    def test_missing_traddr(self):
        self.assertIsNone(dcl.parse_config_line('-t tcp -s 8009'))

    def test_default_port(self):
        result = dcl.parse_config_line('-a 10.0.0.1')
        self.assertIn('-s 8009', result)

    def test_default_transport(self):
        result = dcl.parse_config_line('-a 10.0.0.1')
        self.assertIn('-t tcp', result)

    def test_explicit_transport(self):
        result = dcl.parse_config_line('-t rdma -a 10.0.0.1')
        self.assertIn('-t rdma', result)

    def test_dhchap_secret(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -S secret1')
        self.assertIn('-S secret1', result)

    def test_dhchap_w_alias(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -w secret1')
        self.assertIn('-S secret1', result)

    def test_dhchap_ctrl_secret(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -C ctrlsecret1')
        self.assertIn('-C ctrlsecret1', result)

    def test_hostid_short_flag(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -I uuid-123')
        self.assertIn('-I uuid-123', result)

    def test_hostid_long_flag(self):
        result = dcl.parse_config_line('-a 10.0.0.1 --hostid uuid-456')
        self.assertIn('-I uuid-456', result)

    def test_ctrl_loss_tmo(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -l 600')
        self.assertIn('-l 600', result)

    def test_ctrl_loss_tmo_invalid_rejects_line(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -l notanumber')
        self.assertIsNone(result)

    def test_no_persistent_flag_in_output(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -s 8009')
        self.assertNotIn('-p', result)

    def test_embedded_port_in_traddr(self):
        result = dcl.parse_config_line('-a 10.0.0.1:8009')
        self.assertIn('-a 10.0.0.1', result)
        self.assertIn('-s 8009', result)
        self.assertNotIn('10.0.0.1:8009', result)

    def test_embedded_port_no_explicit_s(self):
        result = dcl.parse_config_line('-a 10.0.0.1:4420')
        self.assertIn('-s 4420', result)

    def test_explicit_s_overrides_embedded_port(self):
        result = dcl.parse_config_line('-a 10.0.0.1:9999 -s 4420')
        self.assertIn('-s 4420', result)

    def test_all_fields(self):
        line = '-t tcp -a 10.0.0.1 -s 8009 -q host1 -n sub1 -S sec -C csec -I hid -l 300'
        result = dcl.parse_config_line(line)
        self.assertIn('-t tcp', result)
        self.assertIn('-a 10.0.0.1', result)
        self.assertIn('-s 8009', result)
        self.assertIn('-q host1', result)
        self.assertIn('-S sec', result)
        self.assertIn('-C csec', result)
        self.assertIn('-I hid', result)
        self.assertIn('-l 300', result)

    def test_unknown_flags_ignored(self):
        result = dcl.parse_config_line('-a 10.0.0.1 -x unknown --foo bar')
        self.assertIn('-a 10.0.0.1', result)
        self.assertNotIn('-x', result)
        self.assertNotIn('--foo', result)


class TestReadConfigDir(unittest.TestCase):

    def setUp(self):
        self.config_dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.config_dir)

    def test_reads_single_file(self):
        with open(os.path.join(self.config_dir, 'cluster1'), 'w') as f:
            f.write('-t tcp -a 10.0.0.1 -s 8009 -q nqn.host1\n')
        lines = dcl.read_config_dir(self.config_dir)
        self.assertEqual(len(lines), 1)
        self.assertIn('-a 10.0.0.1', lines[0])

    def test_reads_multiple_files_sorted(self):
        with open(os.path.join(self.config_dir, 'b_cluster'), 'w') as f:
            f.write('-a 10.0.0.2 -s 8009\n')
        with open(os.path.join(self.config_dir, 'a_cluster'), 'w') as f:
            f.write('-a 10.0.0.1 -s 8009\n')
        lines = dcl.read_config_dir(self.config_dir)
        self.assertEqual(len(lines), 2)
        self.assertIn('-a 10.0.0.1', lines[0])
        self.assertIn('-a 10.0.0.2', lines[1])

    def test_multiple_lines_per_file(self):
        with open(os.path.join(self.config_dir, 'cluster1'), 'w') as f:
            f.write('-a 10.0.0.1 -s 8009\n')
            f.write('-a 10.0.0.2 -s 8009\n')
        lines = dcl.read_config_dir(self.config_dir)
        self.assertEqual(len(lines), 2)

    def test_skips_comments_and_blanks(self):
        with open(os.path.join(self.config_dir, 'cluster1'), 'w') as f:
            f.write('# header comment\n')
            f.write('\n')
            f.write('-a 10.0.0.1 -s 8009\n')
            f.write('  \n')
        lines = dcl.read_config_dir(self.config_dir)
        self.assertEqual(len(lines), 1)

    def test_nonexistent_dir(self):
        lines = dcl.read_config_dir('/nonexistent/path')
        self.assertEqual(lines, [])

    def test_empty_dir(self):
        lines = dcl.read_config_dir(self.config_dir)
        self.assertEqual(lines, [])

    def test_skips_subdirectories(self):
        os.makedirs(os.path.join(self.config_dir, 'subdir'))
        with open(os.path.join(self.config_dir, 'cluster1'), 'w') as f:
            f.write('-a 10.0.0.1 -s 8009\n')
        lines = dcl.read_config_dir(self.config_dir)
        self.assertEqual(len(lines), 1)


class TestWriteDiscoveryConf(unittest.TestCase):

    def setUp(self):
        self.tmpdir = tempfile.mkdtemp()
        self.conf_path = os.path.join(self.tmpdir, 'nvme', 'discovery.conf')

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    def test_writes_lines(self):
        with patch.object(dcl, 'DISCOVERY_CONF', self.conf_path):
            dcl.write_discovery_conf(['-t tcp -a 10.0.0.1 -s 8009', '-t tcp -a 10.0.0.2 -s 8009'])
        content = open(self.conf_path).read()
        self.assertIn('# Generated by discovery-client-lite', content)
        self.assertIn('-t tcp -a 10.0.0.1 -s 8009', content)
        self.assertIn('-t tcp -a 10.0.0.2 -s 8009', content)

    def test_creates_parent_dir(self):
        with patch.object(dcl, 'DISCOVERY_CONF', self.conf_path):
            dcl.write_discovery_conf(['-t tcp -a 10.0.0.1 -s 8009'])
        self.assertTrue(os.path.exists(self.conf_path))

    def test_empty_list(self):
        with patch.object(dcl, 'DISCOVERY_CONF', self.conf_path):
            dcl.write_discovery_conf([])
        content = open(self.conf_path).read()
        self.assertEqual(content, '# Generated by discovery-client-lite\n')


class TestClearDiscoveryConf(unittest.TestCase):

    def setUp(self):
        self.tmpdir = tempfile.mkdtemp()
        self.conf_path = os.path.join(self.tmpdir, 'discovery.conf')

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    def test_clears_existing_file(self):
        with open(self.conf_path, 'w') as f:
            f.write('-t tcp -a 10.0.0.1 -s 8009\n')
        with patch.object(dcl, 'DISCOVERY_CONF', self.conf_path):
            dcl.clear_discovery_conf()
        self.assertEqual(open(self.conf_path).read(), '')

    def test_nonexistent_file_no_error(self):
        with patch.object(dcl, 'DISCOVERY_CONF', '/nonexistent/path/file'):
            dcl.clear_discovery_conf()


class TestRunNvmeConnectAll(unittest.TestCase):

    @patch('subprocess.run')
    def test_calls_nvme_connect_all_with_persistent(self, mock_run):
        mock_run.return_value = type('Result', (), {
            'returncode': 0, 'stdout': '', 'stderr': ''
        })()
        result = dcl.run_nvme_connect_all()
        self.assertTrue(result)
        mock_run.assert_called_once_with(
            ['nvme', 'connect-all', '-p'], capture_output=True, text=True,
        )

    @patch('subprocess.run')
    def test_returns_false_on_failure(self, mock_run):
        mock_run.return_value = type('Result', (), {
            'returncode': 1, 'stdout': '', 'stderr': 'error'
        })()
        result = dcl.run_nvme_connect_all()
        self.assertFalse(result)


class TestRunNvmeDisconnectAll(unittest.TestCase):

    @patch('subprocess.run')
    def test_calls_nvme_disconnect_all(self, mock_run):
        mock_run.return_value = type('Result', (), {
            'returncode': 0, 'stdout': '', 'stderr': ''
        })()
        result = dcl.run_nvme_disconnect_all()
        self.assertTrue(result)
        mock_run.assert_called_once_with(
            ['nvme', 'disconnect-all'], capture_output=True, text=True,
        )

    @patch('subprocess.run')
    def test_returns_false_on_failure(self, mock_run):
        mock_run.return_value = type('Result', (), {
            'returncode': 1, 'stdout': '', 'stderr': 'disconnect failed'
        })()
        result = dcl.run_nvme_disconnect_all()
        self.assertFalse(result)


class TestCmdConnectAll(unittest.TestCase):

    def setUp(self):
        self.config_dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.config_dir)

    @patch.object(dcl, 'run_nvme_connect_all', return_value=True)
    @patch.object(dcl, 'write_discovery_conf')
    def test_success(self, mock_write, mock_connect):
        with open(os.path.join(self.config_dir, 'cluster1'), 'w') as f:
            f.write('-a 10.0.0.1 -s 8009\n')
        args = type('Args', (), {'config_dir': self.config_dir})()
        rc = dcl.cmd_connect_all(args)
        self.assertEqual(rc, 0)
        mock_write.assert_called_once()
        mock_connect.assert_called_once()

    def test_empty_config_returns_success(self):
        args = type('Args', (), {'config_dir': self.config_dir})()
        rc = dcl.cmd_connect_all(args)
        self.assertEqual(rc, 0)

    def test_missing_config_dir_returns_success(self):
        args = type('Args', (), {'config_dir': '/nonexistent/path'})()
        rc = dcl.cmd_connect_all(args)
        self.assertEqual(rc, 0)

    @patch.object(dcl, 'run_nvme_connect_all', return_value=False)
    @patch.object(dcl, 'write_discovery_conf')
    def test_nvme_failure_returns_error(self, mock_write, mock_connect):
        with open(os.path.join(self.config_dir, 'cluster1'), 'w') as f:
            f.write('-a 10.0.0.1 -s 8009\n')
        args = type('Args', (), {'config_dir': self.config_dir})()
        rc = dcl.cmd_connect_all(args)
        self.assertEqual(rc, 1)


class TestCmdDisconnectAll(unittest.TestCase):

    @patch.object(dcl, 'run_nvme_disconnect_all', return_value=True)
    @patch.object(dcl, 'clear_discovery_conf')
    def test_success(self, mock_clear, mock_disconnect):
        rc = dcl.cmd_disconnect_all()
        self.assertEqual(rc, 0)
        mock_clear.assert_called_once()
        mock_disconnect.assert_called_once()

    @patch.object(dcl, 'run_nvme_disconnect_all', return_value=False)
    @patch.object(dcl, 'clear_discovery_conf')
    def test_nvme_failure_returns_error(self, mock_clear, mock_disconnect):
        rc = dcl.cmd_disconnect_all()
        self.assertEqual(rc, 1)


if __name__ == '__main__':
    unittest.main()
