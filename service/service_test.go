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

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/clientconfig"
	"github.com/lightbitslabs/discovery-client/pkg/commonstructs"
	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
	"github.com/lightbitslabs/discovery-client/pkg/nvme"
	"github.com/lightbitslabs/discovery-client/pkg/nvmeclient"
	"github.com/lightbitslabs/discovery-client/pkg/testutils"
	"github.com/sirupsen/logrus"
	_ "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	reconnectInterval        = 3 * time.Second
	obtainConnectionsTimeout = 5 * time.Second
	firstSubsysNQN           = "subsysnqn1"
	secondSubsysNQN          = "subsysnqn2"
	hostnqn                  = "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431"
)

type hostAPIMock struct{}

func NewHostAPIMock() hostapi.HostAPI {
	return &hostAPIMock{}
}

var discoverMock func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error)

func (h *hostAPIMock) Discover(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
	return discoverMock(discoveryRequest)
}

func (h *hostAPIMock) Disconnect(connectionID hostapi.ConnectionID) error {
	return nil
}

func (h *hostAPIMock) CloseConnection(conn *clientconfig.Connection) error {
	return nil
}

func genFileContent(numEntries uint, subsysNQN string) string {
	entries := make([]*commonstructs.Entry, numEntries)
	var thirdIPVal int
	switch subsysNQN {
	case firstSubsysNQN:
		thirdIPVal = 1
	case secondSubsysNQN:
		thirdIPVal = 2
	}
	for i := 0; i < int(numEntries); i++ {
		entry := &commonstructs.Entry{
			Transport: "tcp",
			Trsvcid:   8009,
			Traddr:    fmt.Sprintf("192.168.%d.%d", thirdIPVal, i),
			Hostnqn:   hostnqn,
			Nqn:       subsysNQN,
		}
		entries[i] = entry
	}
	return commonstructs.EntriesToString(entries)
}

func correctNumberOfClusterConnectionsInCache(t *testing.T, S Service, pair clientconfig.ClientClusterPair, expected uint) bool {
	connections, err := getServiceConnectionsOfCluster(S, pair)
	if err != nil {
		t.Error(err)
		return false
	}
	numConnections := uint(len(connections))
	t.Logf("Expected %d connections for pair %v found %d.", expected, pair, numConnections)
	return numConnections == expected
}

func getServiceConnectionsOfCluster(S Service, pair clientconfig.ClientClusterPair) ([]*clientconfig.Connection, error) {
	s, ok := S.(*service)
	if !ok {
		return nil, errors.New("Failed to convert Service interface to service struct to obtain connections")
	}
	connections := make([]*clientconfig.Connection, len(s.connections[pair].ClusterConnectionsMap))
	index := 0
	for _, conn := range s.connections[pair].ClusterConnectionsMap {
		connections[index] = conn
		index++
	}
	return connections, nil
}

// A utility function for generating mocked log page entries of type referral (i.e. SubType: nvme.NVME_NQN_DISC)
// To mock the discovery it does not return referral for the endpoint with address that appears in the discovery request
func getReferrals(numEndpoints uint, discoveryRequest *hostapi.DiscoverRequest) []*hostapi.NvmeDiscPageEntry {
	referrals := []*hostapi.NvmeDiscPageEntry{}
	ipAddrthirdVal := strings.Split(discoveryRequest.Traddr, ".")[2]
	var subsysNQN string
	switch ipAddrthirdVal {
	case "1":
		subsysNQN = firstSubsysNQN
	case "2":
		subsysNQN = secondSubsysNQN
	}
	for i := 0; i < int(numEndpoints); i++ {
		traddr := fmt.Sprintf(`192.168.%s.%d`, ipAddrthirdVal, i)
		if traddr != discoveryRequest.Traddr {
			referral := &hostapi.NvmeDiscPageEntry{
				PortID:  1,
				CntlID:  1,
				TrsvcID: 8009,
				Subnqn:  subsysNQN,
				Traddr:  traddr,
				SubType: nvme.NVME_NQN_DISC,
			}
			referrals = append(referrals, referral)
		}
	}
	return referrals
}

// A utility function to generate the mocked connection ID per connection
// Returns according to the last byte od the address of the connection endpoint
// i.e. for connection to "192.168.1.0" it will return "0"
// for connection to "192.168.1.5" it will return "5" and so on
func getCid(discoveryRequest *hostapi.DiscoverRequest) hostapi.ConnectionID {
	traddr := discoveryRequest.Traddr
	return hostapi.ConnectionID(strings.Split(traddr, ".")[3])
}

func TestConnectionsExistAtServiceStart(t *testing.T) {
	numEndpoints := uint(3)
	//Verifying that connections are recognized when the discovery client starts with files existing in the discovery directory
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return getReferrals(numEndpoints, discoveryRequest), getCid(discoveryRequest), nil
	}
	userDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(userDir)
	internalDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(internalDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
	fileName := "vol1.conf"
	fileContent := genFileContent(numEndpoints, firstSubsysNQN)
	filePath := filepath.Join(userDir, fileName)
	testutils.CreateFile(t, filePath, fileContent)
	hostAPIMock := NewHostAPIMock()
	serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
	serviceInterface.Start()
	correctConnections := func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, numEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", numEndpoints)
	serviceInterface.Stop()
}

func TestConnectionsDualCluster(t *testing.T) {
	testCases := []struct {
		name                            string
		clustersAddedBeforeServiceStart []string
		clustersAddedAfterServiceStart  []string
	}{
		{
			name:                            "1. Both clusters exist before service start",
			clustersAddedBeforeServiceStart: []string{firstSubsysNQN, secondSubsysNQN},
		},
		{
			name:                            "2. One cluster before one after service start",
			clustersAddedBeforeServiceStart: []string{firstSubsysNQN},
			clustersAddedAfterServiceStart:  []string{secondSubsysNQN},
		},
		{
			name:                           "3. Both clusters added after service start",
			clustersAddedAfterServiceStart: []string{firstSubsysNQN, secondSubsysNQN},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			numEndpointsPerCluster := uint(3)
			//Verifying that connections are recognized when the discovery client starts with files existing in the discovery directory
			discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
				return getReferrals(numEndpointsPerCluster, discoveryRequest), getCid(discoveryRequest), nil
			}
			userDir := testutils.CreateTempDir(t)
			defer os.RemoveAll(userDir)
			internalDir := testutils.CreateTempDir(t)
			defer os.RemoveAll(internalDir)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
			fileIndex := 1
			for _, subsysNqn := range tc.clustersAddedBeforeServiceStart {
				fileContent := genFileContent(numEndpointsPerCluster, subsysNqn)
				filePath := filepath.Join(userDir, fmt.Sprintf("vol%d.conf", fileIndex))
				testutils.CreateFile(t, filePath, fileContent)
				fileIndex += 1
			}
			hostAPIMock := NewHostAPIMock()
			serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
			serviceInterface.Start()
			for _, subsysNqn := range tc.clustersAddedAfterServiceStart {
				fileContent := genFileContent(numEndpointsPerCluster, subsysNqn)
				filePath := filepath.Join(userDir, fmt.Sprintf("vol%d.conf", fileIndex))
				testutils.CreateFile(t, filePath, fileContent)
				fileIndex += 1
			}
			allSubsysNqns := []string{}
			allSubsysNqns = append(allSubsysNqns, tc.clustersAddedBeforeServiceStart...)
			allSubsysNqns = append(allSubsysNqns, tc.clustersAddedAfterServiceStart...)
			for _, subsysNQN := range allSubsysNqns {
				correctConnections := func() bool {
					return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{subsysNQN, hostnqn}, numEndpointsPerCluster)
				}
				require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", numEndpointsPerCluster)
			}
			serviceInterface.Stop()
		})
	}
}

func TestConnectionsCreatedBeforeAndAfterServeiceStart(t *testing.T) {
	//a case of files existing at monitored directory at service start and more files added later with partially overlapping connections
	initialNumEndpoints := uint(3)
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return getReferrals(initialNumEndpoints, discoveryRequest), getCid(discoveryRequest), nil
	}
	userDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(userDir)
	internalDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(internalDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
	fileName := "vol1.conf"
	fileContent := genFileContent(initialNumEndpoints, firstSubsysNQN)
	filePath := filepath.Join(userDir, fileName)
	hostAPIMock := NewHostAPIMock()
	serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
	testutils.CreateFile(t, filePath, fileContent)
	serviceInterface.Start()
	correctConnections := func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, initialNumEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", initialNumEndpoints)

	logrus.Info("Adding new connections")
	file2Name := "vol2.conf"
	newEntries := []*commonstructs.Entry{
		{ //Already cached endpoint
			Transport: "tcp",
			Trsvcid:   8009,
			Traddr:    "192.168.1.2",
			Hostnqn:   "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431",
			Nqn:       "subsysnqn1",
		},
		{ //New endpoint
			Transport: "tcp",
			Trsvcid:   8009,
			Traddr:    "192.168.1.3",
			Hostnqn:   "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431",
			Nqn:       "subsysnqn1",
		},
		{ //New endpoint
			Transport: "tcp",
			Trsvcid:   8009,
			Traddr:    "192.168.1.4",
			Hostnqn:   "nqn.2014-08.com.example:nvme:nvm-subsystem-sn-d78431",
			Nqn:       "subsysnqn1",
		},
	}
	numExpectedConnections := uint(5) // 3 from first file 2 new ones from the second
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return getReferrals(numExpectedConnections, discoveryRequest), getCid(discoveryRequest), nil
	}
	file2Content := commonstructs.EntriesToString(newEntries)
	file2Path := filepath.Join(userDir, file2Name)
	testutils.CreateFile(t, file2Path, file2Content)
	correctConnections = func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, numExpectedConnections)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", numExpectedConnections)
	serviceInterface.Stop()
}

func TestConnectionAENNotification(t *testing.T) {
	//Testing a case of connection notifying change through its AEN channel
	numEndpoints := uint(3)
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return getReferrals(numEndpoints, discoveryRequest), getCid(discoveryRequest), nil
	}
	userDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(userDir)
	internalDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(internalDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
	fileName := "vol1.conf"
	fileContent := genFileContent(numEndpoints, firstSubsysNQN)
	filePath := filepath.Join(userDir, fileName)
	hostAPIMock := NewHostAPIMock()
	serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
	serviceInterface.Start()
	testutils.CreateFile(t, filePath, fileContent)
	correctConnections := func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, numEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", numEndpoints)
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		entries := getReferrals(numEndpoints, discoveryRequest)
		nvmeEntry := &hostapi.NvmeDiscPageEntry{
			PortID:  1,
			CntlID:  1,
			TrsvcID: 8009,
			Subnqn:  "subsysnqn1",
			Traddr:  `192.168.1.1`,
			SubType: nvme.NVME_NQN_NVME,
		}
		entries = append(entries, nvmeEntry)
		return entries, getCid(discoveryRequest), nil
	}
	pair := clientconfig.ClientClusterPair{
		ClusterNqn: firstSubsysNQN,
		HostNqn:    hostnqn,
	}
	connections, _ := getServiceConnectionsOfCluster(serviceInterface, pair)
	var conn *clientconfig.Connection
	for _, c := range connections {
		if c.State == true {
			conn = c
			break
		}
	}
	require.NotNil(t, conn, "Failed to find a connection with state true")
	aenStruct := hostapi.AENStruct{true, nil}
	t.Log("Going to send notification through AENChan")
	conn.AENChan <- aenStruct
	t.Log("Going to stop service")
	time.Sleep(3 * time.Second)
	serviceInterface.Stop()
}

func TestDiscoveryNoLogPageEntries(t *testing.T) {
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return nil, getCid(discoveryRequest), &nvmeclient.NvmeClientError{Status: nvmeclient.DISC_NO_LOG, Msg: "no log entries", Err: nil}
	}
	userDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(userDir)
	internalDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(internalDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
	fileName := "vol1.conf"
	numEndpoints := uint(3)
	fileContent := genFileContent(numEndpoints, firstSubsysNQN)
	filePath := filepath.Join(userDir, fileName)
	hostAPIMock := NewHostAPIMock()
	serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
	serviceInterface.Start()
	testutils.CreateFile(t, filePath, fileContent)
	correctConnections := func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, 1)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*500, "number of expected connections, 0, not reached")
	serviceInterface.Stop()
}
func TestFailedConnection(t *testing.T) {
	// Testing the following scenario:
	// 7 Discovery endpoints
	// Discovery succeeds on 6, on one connection Discovery returns an error
	// Verify 6 connetions in OK state, one failed
	numEndpoints := uint(7)
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		if discoveryRequest.Traddr != "192.168.1.0" {
			return getReferrals(numEndpoints, discoveryRequest), getCid((discoveryRequest)), nil
		}
		// on connection to "192.168.1.0" return error
		err := errors.New("discoverMock is on strike today. Come another time")
		return nil, getCid(discoveryRequest), &nvmeclient.NvmeClientError{Status: nvmeclient.DISC_GET_LOG, Msg: "get discovery log failed", Err: err}
	}
	userDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(userDir)
	internalDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(internalDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
	fileName := "vol1.conf"
	fileContent := genFileContent(numEndpoints, firstSubsysNQN)
	filePath := filepath.Join(userDir, fileName)
	hostAPIMock := NewHostAPIMock()
	serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
	serviceInterface.Start()
	testutils.CreateFile(t, filePath, fileContent)
	correctConnections := func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, numEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", numEndpoints)
}
func TestConnectionsAddedThroughReferrals(t *testing.T) {
	// Scenario:
	// Initial user config file contains one entry
	// Discover returns 7 entries
	// Verify 7 connections in service
	// Stop existing and start new cache and service. Connections are expected to be recreated from json file
	// Verify 7 connections in service
	fileNumEndpoints := uint(1)
	referralNumEndpoints := uint(7)
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return getReferrals(referralNumEndpoints, discoveryRequest), getCid(discoveryRequest), nil
	}
	userDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(userDir)
	internalDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(internalDir)
	ctx, cancel := context.WithCancel(context.Background())
	cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
	fileName := "vol1.conf"
	fileContent := genFileContent(fileNumEndpoints, firstSubsysNQN)
	filePath := filepath.Join(userDir, fileName)
	hostAPIMock := NewHostAPIMock()
	serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
	testutils.CreateFile(t, filePath, fileContent)
	serviceInterface.Start()
	correctConnections := func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, referralNumEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", referralNumEndpoints)
	cancel()
	t.Log("Starting new service. Expect 7 connections formed from json")
	newCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	newCache := clientconfig.NewCache(newCtx, userDir, internalDir, nil)
	newServiceInterface := NewService(newCtx, newCache, hostAPIMock, reconnectInterval, 0, 10)
	newServiceInterface.Start()
	correctConnections = func() bool {
		return correctNumberOfClusterConnectionsInCache(t, newServiceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, referralNumEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*500, "number of expected connections, %d, not reached", referralNumEndpoints)
}
func TestConnectionsAtStartNotFromJson(t *testing.T) {
	// Scenario:
	// Populate service with 3 entries
	// Verify 3 connections in service
	// Stop service
	// Replace user file to contain 5 entries
	// Adjust referrals to return 5 entries
	// Restart service
	// Verify 2 connections in service
	numEndpoints := uint(3)
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return getReferrals(numEndpoints, discoveryRequest), getCid(discoveryRequest), nil
	}
	userDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(userDir)
	internalDir := testutils.CreateTempDir(t)
	defer os.RemoveAll(internalDir)
	ctx, cancel := context.WithCancel(context.Background())
	cache := clientconfig.NewCache(ctx, userDir, internalDir, nil)
	fileName := "vol1.conf"
	fileContent := genFileContent(numEndpoints, firstSubsysNQN)
	filePath := filepath.Join(userDir, fileName)
	hostAPIMock := NewHostAPIMock()
	serviceInterface := NewService(ctx, cache, hostAPIMock, reconnectInterval, 0, 10)
	testutils.CreateFile(t, filePath, fileContent)
	serviceInterface.Start()
	correctConnections := func() bool {
		return correctNumberOfClusterConnectionsInCache(t, serviceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, numEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*100, "number of expected connections, %d, not reached", numEndpoints)
	cancel()
	t.Log("Stopped first service instance")
	os.Remove(filePath)
	numEndpoints = uint(5)
	discoverMock = func(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
		return getReferrals(numEndpoints, discoveryRequest), getCid(discoveryRequest), nil
	}
	fileName = "vol2.conf"
	fileContent = genFileContent(numEndpoints, firstSubsysNQN)
	filePath = filepath.Join(userDir, fileName)
	testutils.CreateFile(t, filePath, fileContent)
	t.Log("Starting new service. Expect 5 connections according to new file")
	newCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	newCache := clientconfig.NewCache(newCtx, userDir, internalDir, nil)
	newServiceInterface := NewService(newCtx, newCache, hostAPIMock, reconnectInterval, 0, 10)
	newServiceInterface.Start()
	correctConnections = func() bool {
		return correctNumberOfClusterConnectionsInCache(t, newServiceInterface, clientconfig.ClientClusterPair{firstSubsysNQN, hostnqn}, numEndpoints)
	}
	require.Eventuallyf(t, correctConnections, obtainConnectionsTimeout, time.Millisecond*500, "number of expected connections, %d, not reached", numEndpoints)
}
