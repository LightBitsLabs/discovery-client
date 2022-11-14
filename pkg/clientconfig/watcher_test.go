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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/testutils"
	"github.com/stretchr/testify/require"
)

func TestWatcher(t *testing.T) {
	testCases := []struct {
		name     string
		filename string
		content  string
		err      error
		entries  map[string][]*Entry
	}{
		{
			name:     "valid volume with 3 targets",
			filename: "vol1.conf",
			content: `
			-t tcp -a 192.168.1.1 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1
			-t tcp -a 192.168.1.2 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1
			-t tcp -a 192.168.1.3 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1`,
			err: nil,
			entries: map[string][]*Entry{
				"vol1.conf": []*Entry{
					&Entry{Traddr: "192.168.1.1", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: false, Subsysnqn: "subsysnqn1"},
					&Entry{Traddr: "192.168.1.2", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: false, Subsysnqn: "subsysnqn1"},
					&Entry{Traddr: "192.168.1.3", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: false, Subsysnqn: "subsysnqn1"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := testutils.CreateTempDir(t)
			defer os.RemoveAll(tempDir)

			var fw FileWatcher
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch, err := fw.Watch(ctx, tempDir)
			require.NoErrorf(t, err, "unexpected watch error")
			go func() {
				for {
					event := <-ch
					if event.Op == Create {
						entries, err := parse(event.Name)
						fileEntries := map[string][]*Entry{
							event.Name: entries,
						}
						if tc.err != nil {
							require.EqualError(t, tc.err, err.Error(), "unexpected parse failure")
						}
						entriesEqual(t, tc.entries[tc.filename], fileEntries[event.Name])
					}
				}
			}()
			file1 := filepath.Join(tempDir, tc.filename)
			testutils.CreateFile(t, file1, tc.content)
			time.Sleep(2 * time.Second)
		})
	}
}

func TestCache(t *testing.T) {
	testCases := []struct {
		name     string
		contents []string
		err      error
		entries  []*Entry
	}{
		{
			name: "valid volume with 3 targets",
			contents: []string{`
			-t tcp -a 192.168.1.1 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1
			-t tcp -a 192.168.1.2 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78432 -n subsysnqn1
			-t tcp -a 192.168.1.3 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78433 -n subsysnqn1`},
			err: nil,
			entries: []*Entry{
				{Traddr: "192.168.1.1", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: true, Subsysnqn: "subsysnqn1"},
				{Traddr: "192.168.1.2", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78432", Persistent: true, Subsysnqn: "subsysnqn1"},
				{Traddr: "192.168.1.3", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78433", Persistent: true, Subsysnqn: "subsysnqn1"},
			},
		},
		{
			name: "test dedup same file have 3 similar entries",
			contents: []string{
				`-t tcp -a 192.168.1.1 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1
						-t tcp -a 192.168.1.1 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -s 8009 -n subsysnqn1
						-a 192.168.1.1 -t tcp -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1
						`},
			err: nil,
			entries: []*Entry{
				{Traddr: "192.168.1.1", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: true, Subsysnqn: "subsysnqn1"},
			},
		},
		{
			name: "test dedup 2 files have 3 similar entries",
			contents: []string{
				`-t tcp -a 192.168.1.1 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1
				-t tcp -a 192.168.1.2 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78432 -s 8009 -n subsysnqn1
				-a 192.168.1.3 -t tcp -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78433 -n subsysnqn1`,
				`-t tcp -a 192.168.1.4 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78434 -s 8009 -n subsysnqn1
				-a 192.168.1.5 -t tcp -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78435 -p -n subsysnqn1
				-t tcp -a 192.168.1.1 -s 8009 -q nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431 -n subsysnqn1`},
			err: nil,
			entries: []*Entry{
				{Traddr: "192.168.1.1", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: true, Subsysnqn: "subsysnqn1"},
				{Traddr: "192.168.1.2", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78432", Persistent: true, Subsysnqn: "subsysnqn1"},
				{Traddr: "192.168.1.3", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78433", Persistent: true, Subsysnqn: "subsysnqn1"},
				{Traddr: "192.168.1.4", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78434", Persistent: true, Subsysnqn: "subsysnqn1"},
				{Traddr: "192.168.1.5", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78435", Persistent: true, Subsysnqn: "subsysnqn1"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			userDir := testutils.CreateTempDir(t)
			defer os.RemoveAll(userDir)
			internalDir := testutils.CreateTempDir(t)
			defer os.RemoveAll(internalDir)
			filename := strings.Join(strings.Split(tc.name, " "), "_")
			path := filepath.Join(userDir, filename)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cacheInt := NewCache(ctx, userDir, internalDir, nil)
			defer cacheInt.Stop()
			cacheInt.Run(false)

			// load all files
			// var entries *Entries
			for i, content := range tc.contents {
				file := path + fmt.Sprintf("_%d", i)
				testutils.CreateFile(t, file, content)
				<-cacheInt.Connections()
			}
			// verify that exactly the expected entries are exist.
			cacheImpl := cacheInt.(*cache)
			entriesEqual(t, tc.entries, cacheImpl.cacheEntries)
		})
	}
}

func entriesEqual(t *testing.T, expected, actual []*Entry) {
	require.Equal(t, len(expected), len(actual), "number of entries is not as expected")
	for _, expectedEntry := range expected {
		found := false
		for _, entry := range actual {
			if expectedEntry.compare(entry) {
				found = true
				break
			}
		}
		require.True(t, found, fmt.Sprintf("failed to find %+v in found entries: %s", expectedEntry, EntriesToString(actual)))
	}
}
