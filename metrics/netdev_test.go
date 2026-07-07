// Copyright 2016--2026 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/procfs/sysfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSysfs builds a sysfs.FS rooted at a temp dir containing
// /class/net/<device>/mtu for each entry in mtuByDevice. An empty mtu value
// means the device directory is created without an mtu file, simulating an
// unreadable/unsupported attribute.
func newTestSysfs(t *testing.T, mtuByDevice map[string]string) sysfs.FS {
	t.Helper()
	root := t.TempDir()
	for dev, mtu := range mtuByDevice {
		devDir := filepath.Join(root, "class", "net", dev)
		require.NoError(t, os.MkdirAll(devDir, 0o755))
		if mtu != "" {
			require.NoError(t, os.WriteFile(filepath.Join(devDir, "mtu"), []byte(mtu), 0o644))
		}
	}
	fs, err := sysfs.NewFS(root)
	require.NoError(t, err)
	return fs
}

// collectNetdevMTUMetrics reads back the current values of the
// NetdevMTUBytes gauge, keyed by the "device" label.
func collectNetdevMTUMetrics(t *testing.T) map[string]float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	Metrics.NetdevMTUBytes.Collect(ch)
	close(ch)

	values := map[string]float64{}
	for m := range ch {
		pb := &dto.Metric{}
		require.NoError(t, m.Write(pb))
		var device string
		for _, lp := range pb.GetLabel() {
			if lp.GetName() == "device" {
				device = lp.GetValue()
			}
		}
		values[device] = pb.GetGauge().GetValue()
	}
	return values
}

func TestUpdateNetdevMTU(t *testing.T) {
	Metrics.NetdevMTUBytes.Reset()
	fs := newTestSysfs(t, map[string]string{
		"lo":   "65536\n",
		"eth0": "1500\n",
	})

	require.NoError(t, UpdateNetdevMTU(fs))

	assert.Equal(t, map[string]float64{"lo": 65536, "eth0": 1500}, collectNetdevMTUMetrics(t))
}

func TestUpdateNetdevMTUSkipsUnreadableAttribute(t *testing.T) {
	Metrics.NetdevMTUBytes.Reset()
	fs := newTestSysfs(t, map[string]string{
		"lo":   "65536\n",
		"eth0": "", // directory exists but has no mtu file
	})

	require.NoError(t, UpdateNetdevMTU(fs))

	assert.Equal(t, map[string]float64{"lo": 65536}, collectNetdevMTUMetrics(t))
}

func TestUpdateNetdevMTUMissingClassNetDir(t *testing.T) {
	Metrics.NetdevMTUBytes.Reset()
	// The sysfs root exists, but "class/net" under it does not.
	fs, err := sysfs.NewFS(t.TempDir())
	require.NoError(t, err)

	assert.Error(t, UpdateNetdevMTU(fs))
}
