package clientconfig

import (
	"fmt"
	"testing"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
	"github.com/stretchr/testify/require"
)

func TestEntryToOptions(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		entries  []*Entry
		expected []string
	}{
		{
			name: "test some entries with build options",
			err:  nil,
			entries: []*Entry{
				&Entry{Traddr: "192.168.1.1", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: false, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.2", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78432", Persistent: true, Subsysnqn: "subsysnqn1"},
			},
			expected: []string{
				"nqn=nqn.2014-08.org.nvmexpress.discovery,transport=tcp,traddr=192.168.1.1,trsvcid=8009,hostnqn=nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431",
				"nqn=nqn.2014-08.org.nvmexpress.discovery,transport=tcp,traddr=192.168.1.2,trsvcid=8009,hostnqn=nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78432,keep_alive_tmo=30",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for i, entry := range tc.entries {
				discoveryRequest := &hostapi.DiscoverRequest{
					Traddr:    entry.Traddr,
					Trsvcid:   entry.Trsvcid,
					Kato:      map[bool]time.Duration{true: 30, false: 0}[entry.Persistent],
					Hostnqn:   entry.Hostnqn,
					Transport: entry.Transport,
				}

				options := discoveryRequest.ToOptions()
				require.Equal(t, tc.expected[i], options, "options should have matched expected")
			}
		})
	}
}

func TestDiscoveryConfParser(t *testing.T) {
	testCases := []struct {
		name     string
		filename string
		err      error
		entries  []*Entry
	}{
		{
			name:     "valid values",
			filename: "testdata/discovery_k8s.conf",
			err:      nil,
			entries: []*Entry{
				&Entry{Traddr: "192.168.1.1", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431", Persistent: false, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.2", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78432", Persistent: false, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.3", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78433", Persistent: true, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.4", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78434", Persistent: true, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.5", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78435", Persistent: false, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.6", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78436", Persistent: false, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.7", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78437", Persistent: false, Subsysnqn: "subsysnqn1"},
				&Entry{Traddr: "192.168.1.8", Trsvcid: 8009, Transport: "tcp", Hostnqn: "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78438", Persistent: true, Subsysnqn: "subsysnqn1"},
			},
		},
		{name: "bad port", filename: "testdata/discovery_k8s_bad_port.conf", err: &ParserError{Msg: "bad port"}},
		{name: "bad address", filename: "testdata/discovery_k8s_bad_address.conf", err: &ParserError{Msg: "bad address"}},
		{name: "missing address", filename: "testdata/discovery_k8s_missing_address.conf", err: &ParserError{Msg: "bad address"}},
		{name: "bad transport", filename: "testdata/discovery_k8s_bad_transport.conf", err: &ParserError{Msg: "bad transport"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parse(tc.filename)
			if tc.err == nil && err != nil {
				require.NoErrorf(t, err, "unexpected error")
			}
			if tc.err != nil {
				require.EqualError(t, err, tc.err.Error(), "the errors should match")
			}
			require.Equal(t, len(tc.entries), len(entries), "number of entries is different than expected entries")
			for _, expectedEntry := range tc.entries {
				found := false
				for _, entry := range entries {
					if expectedEntry.compare(entry) {
						found = true
						break
					}
				}
				require.True(t, found, fmt.Sprintf("failed to find %+v in found entries: %s", expectedEntry, EntriesToString(entries)))
			}
		})
	}
}
