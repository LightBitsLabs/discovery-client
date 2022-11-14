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
	"fmt"
	"strings"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/nvme"
)

const (
	// DiscoverySubsysName name of discovery subsystem
	DiscoverySubsysName string = "nqn.2014-08.org.nvmexpress.discovery"
)

type DiscoverRequest struct {
	Transport string
	Traddr    string
	Trsvcid   int
	Hostnqn   string
	Hostaddr  string
	Kato      time.Duration //keep alive timeout. 0 value signifies request for non persistant connection
	AENChan   chan AENStruct
}

// NvmeDiscPageEntry struct represent discovery log page that will be returned from discover method
type NvmeDiscPageEntry struct {
	PortID  uint16             `json:"portid"`
	CntlID  uint16             `json:"cntlid"`
	TrsvcID uint16             `json:"trsvcid"`
	Subnqn  string             `json:"subnqn"`
	Traddr  string             `json:"traddr"`
	SubType nvme.SubsystemType `json:"subtype"`
}

type AENStruct struct {
	AenChange    bool
	ServerChange error
}

type ConnectionID string

type HostAPI interface {
	Discover(discoveryRequest *DiscoverRequest) ([]*NvmeDiscPageEntry, ConnectionID, error)
	Disconnect(connectionID ConnectionID) error
}

// ToOptions returns a comma delimited key=value string
// example: transport=tcp,traddr=2.2.2.2,trsvcid=8009,hostnqn=xxxxxxx
func (c *DiscoverRequest) ToOptions() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("nqn=%s", DiscoverySubsysName))
	if len(c.Transport) > 0 {
		sb.WriteString(fmt.Sprintf(",transport=%s", c.Transport))
	}
	if len(c.Traddr) > 0 {
		sb.WriteString(fmt.Sprintf(",traddr=%s", c.Traddr))
	}
	if c.Trsvcid > 0 {
		sb.WriteString(fmt.Sprintf(",trsvcid=%d", c.Trsvcid))
	}
	if len(c.Hostnqn) > 0 {
		sb.WriteString(fmt.Sprintf(",hostnqn=%s", c.Hostnqn))
	}
	if len(c.Hostaddr) > 0 {
		sb.WriteString(fmt.Sprintf(",host_traddr=%s", c.Hostaddr))
	}
	if c.Kato > 0 {
		sb.WriteString(fmt.Sprintf(",keep_alive_tmo=%d", c.Kato))
	}
	return sb.String()
}
