// Copyright 2016--2022 Lightbits Labs Ltd.
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

package clientconfig

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/lightbitslabs/discovery-client/pkg/commonstructs"
	"github.com/stretchr/testify/require"
)

func createFileTree(t *testing.T, userConf bool, userConfFiles int, internalConf bool, internalConfFiles int) string {
	dir, err := ioutil.TempDir("", "prefix")
	require.NoError(t, err, "failed creating temp dir")
	if userConf {
		userConfDirName := path.Join(dir, "discovery.d")
		require.NoError(t, os.MkdirAll(userConfDirName, os.ModePerm), "failed")
		for i := 0; i < userConfFiles; i++ {
			data := []byte("-t tcp -a 1.1.1.1 -s 8009 -q bla -n bla")
			filename := path.Join(userConfDirName, fmt.Sprintf("%d", i))
			require.NoError(t, ioutil.WriteFile(filename, data, os.ModePerm), "failed")
		}
	}
	if internalConf {
		internalConfDirName := path.Join(dir, "internal")
		require.NoError(t, os.MkdirAll(internalConfDirName, os.ModePerm), "failed")
		for i := 0; i < internalConfFiles; i++ {
			data := []byte("{}")
			filename := path.Join(internalConfDirName, fmt.Sprintf("%d", i))
			require.NoError(t, ioutil.WriteFile(filename, data, os.ModePerm), "failed")
		}
	}
	return dir
}

func getHostsInCIDR(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		ips = append(ips, ip.String())
	}
	// remove network address and broadcast address
	return ips[1 : len(ips)-1], nil
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func createSysClassNvmeFileTree(t *testing.T, subsysnqn, hostnqn, transport, cidr string, port int, controllers int) string {
	dir, err := ioutil.TempDir("", "prefix")
	require.NoError(t, err, "failed creating temp dir")
	hosts, err := getHostsInCIDR(cidr)
	if err != nil {
		hosts = nil
	}
	for i := 0; i < controllers; i++ {
		nvmeCtrlPath := path.Join(dir, fmt.Sprintf("nvme%d", i))
		require.NoError(t, os.MkdirAll(nvmeCtrlPath, os.ModePerm), "failed")
		if len(subsysnqn) > 0 {
			require.NoError(t, ioutil.WriteFile(path.Join(nvmeCtrlPath, "subsysnqn"), []byte(subsysnqn), os.ModePerm), "failed")
		}
		if len(hostnqn) > 0 {
			require.NoError(t, ioutil.WriteFile(path.Join(nvmeCtrlPath, "hostnqn"), []byte(hostnqn), os.ModePerm), "failed")
		}
		if len(transport) > 0 {
			require.NoError(t, ioutil.WriteFile(path.Join(nvmeCtrlPath, "transport"), []byte(transport), os.ModePerm), "failed")
		}
		if hosts != nil {
			require.NoError(t, ioutil.WriteFile(path.Join(nvmeCtrlPath, "address"), []byte(fmt.Sprintf("traddr=%s,trsvcid=%d", hosts[i], port)), os.ModePerm), "failed")
		}
	}
	return dir
}

func TestShouldGenerateAutoDetectedEntries(t *testing.T) {
	testCases := []struct {
		name     string
		dir      string
		expected bool
	}{
		{
			name:     "should create - don't have any folder",
			dir:      createFileTree(t, false, 0, false, 0),
			expected: true,
		},
		{
			name:     "should create - have only userConf",
			dir:      createFileTree(t, true, 0, false, 0),
			expected: true,
		},
		{
			name:     "should create - have only internalConf",
			dir:      createFileTree(t, false, 0, true, 0),
			expected: true,
		},
		{
			name:     "should create - have userConf and internalConf",
			dir:      createFileTree(t, true, 0, true, 0),
			expected: true,
		},
		{
			name:     "shouldn't create - have 1 userConf file",
			dir:      createFileTree(t, true, 1, true, 0),
			expected: false,
		},
		{
			name:     "shouldn't create - have 1 internalConf file",
			dir:      createFileTree(t, true, 0, true, 1),
			expected: false,
		},
		{
			name:     "shouldn't create - have userConf and internalConf file",
			dir:      createFileTree(t, true, 1, true, 1),
			expected: false,
		},
		{
			name:     "shouldn't create - have userConf files",
			dir:      createFileTree(t, true, 2, false, 0),
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer os.RemoveAll(tc.dir)
			userConfDirName := path.Join(tc.dir, "discovery.d")
			internalConfDirName := path.Join(tc.dir, "internal")
			require.Equal(t, tc.expected, ShouldGenerateAutoDetectedEntries(userConfDirName, internalConfDirName), "should match")
		})
	}
}

func TestDetectEntriesByIOControllers(t *testing.T) {
	goodSubsys := "nqn.2016-01.com.lightbitslabs:uuid:a40beb3e-08a4-45cb-b4e9-2fd136fb2d6f"
	badSubsys := "nqn.2016-01.com.bla:uuid:a40beb3e-08a4-45cb-b4e9-2fd136fb2d6f"
	hostnqn := "nqn.2019-09.com.lightbitslabs:host:rack08-server55-vm06.node"
	testCases := []struct {
		name     string
		dir      string
		dsPort   uint
		expected []*commonstructs.Entry
	}{
		{
			name:   "succeed",
			dir:    createSysClassNvmeFileTree(t, goodSubsys, hostnqn, "tcp", "192.168.11.0/24", 8009, 3),
			dsPort: 8009,
			expected: []*commonstructs.Entry{
				{Transport: "tcp", Traddr: "192.168.11.1", Trsvcid: 8009, Hostnqn: hostnqn, Nqn: goodSubsys},
				{Transport: "tcp", Traddr: "192.168.11.2", Trsvcid: 8009, Hostnqn: hostnqn, Nqn: goodSubsys},
				{Transport: "tcp", Traddr: "192.168.11.3", Trsvcid: 8009, Hostnqn: hostnqn, Nqn: goodSubsys},
			},
		},
		{
			name:     "succeed - no IO controller device exists",
			dir:      createSysClassNvmeFileTree(t, goodSubsys, hostnqn, "tcp", "192.168.11.0/24", 8009, 0),
			dsPort:   8009,
			expected: []*commonstructs.Entry{},
		},
		{
			name:     "failed - bad subsysnqn",
			dir:      createSysClassNvmeFileTree(t, badSubsys, hostnqn, "tcp", "192.168.11.0/24", 8009, 3),
			dsPort:   8009,
			expected: []*commonstructs.Entry{},
		},
		{
			name:     "failed - no address file",
			dir:      createSysClassNvmeFileTree(t, goodSubsys, hostnqn, "tcp", "", 8009, 3),
			dsPort:   8009,
			expected: []*commonstructs.Entry{},
		},
		{
			name:     "failed - no hostnqn file",
			dir:      createSysClassNvmeFileTree(t, goodSubsys, "", "tcp", "10.10.11.0/24", 8009, 3),
			dsPort:   8009,
			expected: []*commonstructs.Entry{},
		},
		{
			name:     "failed - no subsysnqn file",
			dir:      createSysClassNvmeFileTree(t, "", hostnqn, "tcp", "10.10.11.0/24", 8009, 3),
			dsPort:   8009,
			expected: []*commonstructs.Entry{},
		},
		{
			name:     "failed - no transport file",
			dir:      createSysClassNvmeFileTree(t, goodSubsys, hostnqn, "", "10.10.11.0/24", 8009, 3),
			dsPort:   8009,
			expected: []*commonstructs.Entry{},
		},
		{
			name:     "failed - transport not valid",
			dir:      createSysClassNvmeFileTree(t, goodSubsys, hostnqn, "rdma", "10.10.11.0/24", 8009, 3),
			dsPort:   8009,
			expected: []*commonstructs.Entry{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer os.RemoveAll(tc.dir)
			nvmeCtrlPath := filepath.Join(tc.dir, "nvme[0-9]")
			entries, err := DetectEntriesByIOControllers(nvmeCtrlPath, tc.dsPort)
			require.NoErrorf(t, err, "should succeed")
			require.Equal(t, tc.expected, entries, "should match")
		})
	}
}
