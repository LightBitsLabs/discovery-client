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

	"sync"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/clientconfig"
	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
	"github.com/lightbitslabs/discovery-client/pkg/nvme"
	"github.com/lightbitslabs/discovery-client/pkg/nvmeclient"
	"github.com/sirupsen/logrus"
)

const (
	kato            = time.Duration(30 * time.Second) //keep alive time out for a persistent connection. TODO: make it configurable
	nvmeTCPDiscPort = uint16(8009)
)

type aenNotification struct {
	conn *clientconfig.Connection
	aen  hostapi.AENStruct
}

type Service interface {
	Start() error
	Stop() error
}

type service struct {
	cache             clientconfig.Cache
	connections       clientconfig.ConnectionMap
	ctx               context.Context
	cancel            context.CancelFunc
	log               *logrus.Entry
	aggregateChan     chan *aenNotification
	hostAPI           hostapi.HostAPI
	wg                *sync.WaitGroup
	reconnectInterval time.Duration
	maxIOQueues       int
}

func NewService(ctx context.Context, cache clientconfig.Cache, hostAPI hostapi.HostAPI, reconnectInterval time.Duration, maxIOQueues int) Service {
	s := &service{
		log:               logrus.WithFields(logrus.Fields{}),
		cache:             cache,
		hostAPI:           hostAPI,
		reconnectInterval: reconnectInterval,
		maxIOQueues:       maxIOQueues,
	}
	var wg sync.WaitGroup
	s.wg = &wg
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.connections = make(clientconfig.ConnectionMap)
	s.aggregateChan = make(chan *aenNotification)
	return s
}

func (s *service) Discover(req *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
	logPageEntries, id, err := s.hostAPI.Discover(req)
	if err != nil {
		var perr *nvmeclient.NvmeClientError
		if errors.As(err, &perr) {
			if perr.Status != nvmeclient.DISC_NO_LOG {
				return nil, id, perr
			}
			return logPageEntries, id, nil
		}
		return nil, id, err
	}
	return logPageEntries, id, nil
}

func (s *service) getLogPageEntries(conn *clientconfig.Connection, kato time.Duration) ([]*hostapi.NvmeDiscPageEntry, []*hostapi.NvmeDiscPageEntry, *hostapi.DiscoverRequest, error) {
	request := conn.GetDiscoveryRequest(kato)
	logPageEntries, id, err := s.Discover(request)
	//In case the connection is persistent keep the connection id
	if kato > 0 && err == nil {
		conn.ConnectionID = id
		s.log.Debugf("Added ID %v to %s", id, conn)
	}
	if err != nil {
		conn.SetState(false)
		return nil, nil, nil, err
	}
	conn.SetState(true)
	pair := clientconfig.ClientClusterPair{
		ClusterNqn: conn.Key.Nqn,
		HostNqn:    conn.Hostnqn,
	}
	clientClusterConnections := s.connections[pair]
	clientClusterConnections.ActiveConnection = conn
	s.connections[pair] = clientClusterConnections
	s.log.Debugf("Run Discovery on connection %q and got %d log page entries", conn.Key.Ip, len(logPageEntries))
	nvmeLogPageEntries := []*hostapi.NvmeDiscPageEntry{}
	discLogPageEntries := []*hostapi.NvmeDiscPageEntry{}
	for _, entry := range logPageEntries {
		switch entry.SubType {
		case nvme.NVME_NQN_NVME:
			nvmeLogPageEntries = append(nvmeLogPageEntries, entry)
		case nvme.NVME_NQN_DISC:
			discLogPageEntries = append(discLogPageEntries, entry)
		default:
			s.log.Errorf("Unexpected subtype in logPageEntry: %+v", entry)
		}
	}
	s.log.Debugf("got from tcp client %d nvme entries and %d referrals", len(nvmeLogPageEntries), len(discLogPageEntries))
	// Add another log page entry of a referral to the sending server as it is not returned by the server
	selfReferral := &hostapi.NvmeDiscPageEntry{
		TrsvcID: nvmeTCPDiscPort,
		Subnqn:  conn.Key.Nqn,
		Traddr:  conn.Key.Ip,
		SubType: nvme.NVME_NQN_DISC,
	}
	s.log.Debugf("Added self referral %+v", selfReferral)
	discLogPageEntries = append(discLogPageEntries, selfReferral)
	s.log.Debugf("After adding self referral, len(discLogPageEntries) = %d", len(discLogPageEntries))
	return nvmeLogPageEntries, discLogPageEntries, request, nil
}

// On a new connection, add it to the connections that multiplex AEN events to the service AEN channel
func (s *service) multiplexNewConnection(conn *clientconfig.Connection) {
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-conn.Ctx.Done():
				return
			case event, ok := <-conn.AENChan:
				if !ok {
					s.log.Infof("%s AEN channel closed", conn)
					conn.SetState(false)
					return
				}
				if event.ServerChange != nil {
					s.log.Warnf("%s keep alive failed: %s", conn, event.ServerChange.Error())
					conn.SetState(false)
					s.aggregateChan <- &aenNotification{conn, event}
					return
				}
				s.log.Debugf("aen on %s", conn)
				aen := hostapi.AENStruct{
					AenChange:    true,
					ServerChange: nil,
				}
				s.aggregateChan <- &aenNotification{conn, aen}
			}
		}
	}()
}

// Start run logic of discovery client
func (s *service) Start() error {
	if err := s.cache.Run(true); err != nil {
		return err
	}

	triggerReconnectToClusterCh := make(chan clientconfig.ClientClusterPair)

	go func() {
		var stopCh chan bool
		for {
			select {
			case clusterMapId := <-triggerReconnectToClusterCh:
				// first - close any running scheduler for reconnect
				if stopCh != nil {
					close(stopCh)
				}
				err := s.reconnectToCluster(clusterMapId)
				if err != nil {
					// reconnect failed. will schedule a reconnect goroutine.
					// create a new goroutine that would try to reconnect.
					ticker := time.NewTicker(s.reconnectInterval)
					stopCh = make(chan bool, 1)
					go func() {
						defer ticker.Stop()
						s.log.Infof("%s. schedule reconnect in %s", err, s.reconnectInterval)
						select {
						case <-ticker.C:
							triggerReconnectToClusterCh <- clusterMapId
						case <-stopCh:
							return
						}
					}()
				}
			case connections := <-s.cache.Connections():
				// this case hit when the cache is updated with new/removed connections.
				// we would need to refresh the current connection list and invoke connectCluster.
				s.log.Debug("received notification on changes in connections")
				// remove from service connections that are no longer in cache
				modified := make(map[clientconfig.ClientClusterPair]bool)
				s.removeConnections(connections, modified)
				// update service connections with new connections
				s.addConnections(connections, modified)
				for clusterMapId, clientClusterConnections := range s.connections {
					if mod, ok := modified[clusterMapId]; !ok || !mod {
						continue
					}
					if clientClusterConnections.ActiveConnection != nil {
						s.log.Debugf("already connected to cluster %+v", clusterMapId)
						continue
					}
					s.log.Debugf("connecting service to cluster: %v", clusterMapId)
					go func(clusterMapId clientconfig.ClientClusterPair) {
						triggerReconnectToClusterCh <- clusterMapId
					}(clusterMapId)
				}
			case aenAlert := <-s.aggregateChan:
				// this case hit when we got AEN notification and we need to call getLogPage.
				// this will then iterate over all log-pages and will connect to all.
				if aenAlert != nil {
					conn := aenAlert.conn
					clusterMapId := clientconfig.ClientClusterPair{
						ClusterNqn: conn.Key.Nqn,
						HostNqn:    conn.Hostnqn,
					}
					aen := aenAlert.aen
					// meaning we have a new server in the cluster and we would want to invoke reconnect.
					if aen.ServerChange != nil {
						go func() {
							triggerReconnectToClusterCh <- clusterMapId
						}()
						continue
					}
					s.log.Debugf("received notification through aggregate chan on %s", conn)
					nvmeLogPageEntries, discLogPageEntries, request, err := s.getLogPageEntries(conn, time.Duration(0))
					if err != nil {
						// failed issuing get-log-page command, meaning we should try
						// to reconnect to the cluster - probably different service, that would provide better answer.
						s.log.WithError(err).Errorf("error in receiving log page entries through %s. Start reconnect process", conn)
						conn.SetState(false)
						go func() {
							triggerReconnectToClusterCh <- clusterMapId
						}()
						continue
					}
					nvmeclient.ConnectAllNVMEDevices(nvmeLogPageEntries, request.Hostnqn, request.Transport, s.maxIOQueues, request.HostIface)
					refMap := clientconfig.ReferralMap{}
					for _, referral := range discLogPageEntries {
						refKey := clientconfig.ReferralKey{
							Ip:       referral.Traddr,
							Port:     referral.TrsvcID,
							DPSubNqn: conn.Key.Nqn,
							Hostnqn:  conn.Hostnqn}
						refMap[refKey] = referral
					}
					s.cache.HandleReferrals(refMap)
				}
			case <-s.ctx.Done():
				s.log.Infof("exiting the main func ctx done")
				return
			}
		}
	}()
	return nil
}

// pair would be the identifier of the cluster we want to connect to.
// the DC support multiple clusters at the same time, and this method will
// try to connect to single DS service in cluster defined by `pair`
func (s *service) reconnectToCluster(clusterMapId clientconfig.ClientClusterPair) error {
	clientClusterConnections, ok := s.connections[clusterMapId]
	if !ok || len(clientClusterConnections.ClusterConnectionsMap) == 0 {
		s.log.Errorf("cannot connect to cluster with subsysNQN %s from client with hostnqn %s. no connections found",
			clusterMapId.ClusterNqn, clusterMapId.HostNqn)
		return nil
	}
	clusterConnectionsList := clientClusterConnections.GetRandomConnectionList()

	s.log.Infof("trying to connect to cluster %s as hostnqn %s", clusterMapId.ClusterNqn, clusterMapId.HostNqn)
	aen := hostapi.AENStruct{
		AenChange:    true,
		ServerChange: nil,
	}
	conn, err := s.getLiveConnection(clusterConnectionsList, clusterMapId.ClusterNqn)
	if err != nil {
		return err
	}
	s.multiplexNewConnection(conn)
	s.wg.Add(1)
	s.log.Debugf("pushing AEN notification to live %s to trigger discovery on new connection", conn)
	conn.AENChan <- aen
	s.log.Debugf("returned from pushing AEN notification to live %s to trigger discovery on new connection", conn)
	return nil
}

// this method will iterate over all connections and will try to issue a Discover command.
// The first one that succeeded will be selected as a persistent connection to the cluster.
func (s *service) getLiveConnection(connections []*clientconfig.Connection, subsysNqn string) (*clientconfig.Connection, error) {
	var connectionIPs []string
	for _, conn := range connections {
		connectionIPs = append(connectionIPs, conn.Key.Ip)
		_, _, _, err := s.getLogPageEntries(conn, kato)
		if err == nil {
			s.log.Infof("connected successfully to cluster %s with %s after trying %+v",
				subsysNqn, conn, connectionIPs)
			return conn, nil
		}
	}
	return nil, fmt.Errorf("connect to cluster %s failed with all (%d) connections: %+v",
		subsysNqn, len(connectionIPs), connectionIPs)
}

// iterate over all cluster-connections.
// for each cluster-connection verify that current connection exists in the new cache-connections Map
// if exists leave it, if does not exists remove the connection and disconnect it.
func (s *service) removeConnections(
	fromCache clientconfig.ConnectionMap,
	modified map[clientconfig.ClientClusterPair]bool,
) {
	for clusterMapId, cachedClusterConnections := range fromCache {
		serviceClusterConnections, ok := s.connections[clusterMapId]
		if !ok {
			// New cluster-client pair from cache. No existing service connections to remove
			if _, ok := modified[clusterMapId]; !ok {
				modified[clusterMapId] = false
			}
			continue
		}
		// iterate over connections of cluster[clusterMapId]
		for key, conn := range serviceClusterConnections.ClusterConnectionsMap {
			if !cachedClusterConnections.Exists(conn) {
				log := s.log.WithField("subsys-nqn", conn.Key.Nqn).
					WithField("ip", conn.Key.Ip).
					WithField("id", conn.ConnectionID)

				log.Infof("remove from service connections")
				if serviceClusterConnections.ActiveConnection == conn {
					log.Debugf("disconnecting active connection and setting active connection to nil")
					if err := s.hostAPI.Disconnect(conn.ConnectionID); err != nil {
						log.WithError(err).Errorf("disconnecting connection")
					}
					serviceClusterConnections.ActiveConnection = nil
				}
				log.Debugf("deleting connection from service connections")
				delete(serviceClusterConnections.ClusterConnectionsMap, key)
				conn.Stop()
				modified[clusterMapId] = true
			}
		}
	}
}

// A function for dealing with new connections from cache
// it will iterate over all cached conn and will look for a conn that does not exist
// in current-connection map - if found it will add it to current list.
func (s *service) addConnections(
	fromCache clientconfig.ConnectionMap,
	modified map[clientconfig.ClientClusterPair]bool,
) {
	for clusterMapId, clusterConnections := range fromCache {
		if _, ok := modified[clusterMapId]; !ok {
			modified[clusterMapId] = false
		}
		for key, conn := range clusterConnections.ClusterConnectionsMap {
			if !s.connections[clusterMapId].Exists(conn) {
				//update service connections
				s.connections.AddConnection(key, conn)
				s.log.Debugf("added connection: %s", conn)
				modified[clusterMapId] = true
			}
		}
	}
}

// Stop run logic of discovery client
func (s *service) Stop() error {
	s.cancel()
	for clientClusterPair, clusterConnections := range s.connections {
		for key, conn := range clusterConnections.ClusterConnectionsMap {
			log := s.log.WithField("subsys-nqn", conn.Key.Nqn).
				WithField("ip", conn.Key.Ip).
				WithField("id", conn.ConnectionID)
			if err := s.hostAPI.Disconnect(conn.ConnectionID); err != nil {
				log.WithError(err).Errorf("Error in disconnecting connection %s", conn)
			}
			s.connections.DeleteConnection(clientClusterPair, key)
			log.Debugf("removed connection")
			conn.Stop()
		}
	}
	//Clear the service aggregate channel before closing it
	for i := 0; i < len(s.connections); i++ {
		select {
		case <-s.aggregateChan:
		default:
			break
		}
	}
	s.log.Debug("Closing agg chan")
	close(s.aggregateChan)
	s.cache.Stop()
	s.log.Debug("Waiting for all multiplexing functions on all connections to return")
	s.wg.Wait()
	s.log.Debug("Finished stopping discovery client")
	return nil
}
