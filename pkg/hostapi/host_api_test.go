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
