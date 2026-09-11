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
	"context"
	"time"

	"github.com/prometheus/procfs/sysfs"
	"github.com/sirupsen/logrus"
)

// netdevMTUCollectionInterval is how often the netdev MTU gauge is refreshed.
const netdevMTUCollectionInterval = 30 * time.Second

// UpdateNetdevMTU reads the MTU of every local network device from sysfs and
// updates the NetdevMTUBytes gauge. A device whose MTU can't be read (e.g. it
// disappeared, or the attribute isn't supported) is skipped rather than
// failing the whole update.
func UpdateNetdevMTU(fs sysfs.FS) error {
	devices, err := fs.NetClassDevices()
	if err != nil {
		return err
	}
	for _, dev := range devices {
		iface, err := fs.NetClassByIface(dev)
		if err != nil {
			logrus.WithError(err).WithField("device", dev).Debug("could not read sysfs net class info for device")
			continue
		}
		if iface.MTU == nil {
			continue
		}
		Metrics.NetdevMTUBytes.WithLabelValues(dev).Set(float64(*iface.MTU))
	}
	return nil
}

// RunNetdevMTUCollector periodically refreshes the netdev MTU gauge until ctx
// is cancelled. Intended to be run in its own goroutine for the lifetime of
// the application.
func RunNetdevMTUCollector(ctx context.Context) {
	fs, err := sysfs.NewDefaultFS()
	if err != nil {
		logrus.WithError(err).Warn("could not open sysfs, netdev MTU metric will not be collected")
		return
	}

	if err := UpdateNetdevMTU(fs); err != nil {
		logrus.WithError(err).Warn("could not collect netdev MTU metrics")
	}

	ticker := time.NewTicker(netdevMTUCollectionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := UpdateNetdevMTU(fs); err != nil {
				logrus.WithError(err).Warn("could not collect netdev MTU metrics")
			}
		}
	}
}
