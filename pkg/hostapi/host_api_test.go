package hostapi

import (
	"testing"
	"time"
)

type hostAPIMock struct{}

func NewHostAPIMock() HostAPI {
	return &hostAPIMock{}
}

var discoverMock func(discoveryRequest *DiscoverRequest) ([]*NvmeDiscPageEntry, ConnectionID, error)

func (h *hostAPIMock) Discover(discoveryRequest *DiscoverRequest) ([]*NvmeDiscPageEntry, ConnectionID, error) {
	return discoverMock(discoveryRequest)
}

func (h *hostAPIMock) Disconnect(connectionID ConnectionID) error {
	return nil
}

func TestEmptyDiscovery(t *testing.T) {
	request := &DiscoverRequest{
		Traddr:    "192.168.1010",
		Transport: "tcp",
		Trsvcid:   8009,
		Hostnqn:   "client_0",
		Kato:      time.Duration(30 * time.Second),
	}
	discoverMock = func(discoveryRequest *DiscoverRequest) ([]*NvmeDiscPageEntry, ConnectionID, error) {
		return nil, ConnectionID("1"), nil
	}
	apiMock := NewHostAPIMock()
	entries, _, _ := apiMock.Discover(request)
	if entries != nil {
		t.Error("Expected entries to be nil")
	}
}
