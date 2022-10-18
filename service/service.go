package service

import (
	"context"
	"errors"

	"math/rand"
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

func (s *service) getConnectionEntries(conn *clientconfig.Connection, kato time.Duration) ([]*hostapi.NvmeDiscPageEntry, []*hostapi.NvmeDiscPageEntry, *hostapi.DiscoverRequest, error) {
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

//On a new connection, add it to the connections that multiplex AEN events to the service AEN channel
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
					s.log.Infof("%s keep alive failed", conn)
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
	go func() {
		for {
			select {
			case connections := <-s.cache.Connections():
				s.log.Debug("received notification on changes in connections")
				// remove from service connections that are no longer in cache
				s.removeConnections(connections)
				// update service connections with new connections
				s.addConnections(connections)
				for pair, clientClusterConnections := range s.connections {
					if clientClusterConnections.ActiveConnection != nil {
						s.log.Debugf("Already connected to pair %+v", pair)
						continue
					}
					s.log.Debugf("connecting service to pair %v", pair)
					go s.connectCluster(pair)
				}
			case aenAlert := <-s.aggregateChan:
				if aenAlert != nil {
					conn := aenAlert.conn
					pair := clientconfig.ClientClusterPair{
						ClusterNqn: conn.Key.Nqn,
						HostNqn:    conn.Hostnqn,
					}
					aen := aenAlert.aen
					if aen.ServerChange != nil {
						go s.connectCluster(pair)
						continue
					}
					s.log.Debugf("received notification through aggregate chan on %s", conn)
					nvmeLogPageEntries, discLogPageEntries, request, err := s.getConnectionEntries(conn, time.Duration(0))
					if err != nil {
						s.log.WithError(err).Errorf("Error in receiving log page entries through %s. Start reconnect process", conn)
						conn.SetState(false)
						go s.connectCluster(pair)
						continue
					}
					nvmeclient.ConnectAllNVMEDevices(nvmeLogPageEntries, request.Hostnqn, request.Transport, s.maxIOQueues)
					refMap := clientconfig.ReferralMap{}
					for _, referral := range discLogPageEntries {
						refKey := clientconfig.ReferralKey{Ip: referral.Traddr, Port: referral.TrsvcID, DPSubNqn: conn.Key.Nqn, Hostnqn: conn.Hostnqn}
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

func (s *service) connectCluster(pair clientconfig.ClientClusterPair) {
	clientClusterConnections, ok := s.connections[pair]
	clientClusterConnectionsMap := clientClusterConnections.ClusterConnectionsMap
	if !ok || len(clientClusterConnectionsMap) == 0 {
		s.log.Errorf("Cannot connect to cluster with subsysNQN %s from client with hostnqn %s. No connections found", pair.ClusterNqn, pair.HostNqn)
		return
	}
	clientClusterConnections.ActiveConnection = nil
	s.connections[pair] = clientClusterConnections

	clusterConnectionsList := make([]*clientconfig.Connection, len(clientClusterConnectionsMap))
	ind := 0
	for _, conn := range clientClusterConnectionsMap {
		clusterConnectionsList[ind] = conn
		ind++
	}
	//Generate a random permutation of connections order to balance used target among clients
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(clusterConnectionsList), func(i, j int) {
		clusterConnectionsList[i], clusterConnectionsList[j] = clusterConnectionsList[j], clusterConnectionsList[i]
	})
	s.log.Infof("Connecting to cluster %s with hostnqn %s", pair.ClusterNqn, pair.HostNqn)
	aen := hostapi.AENStruct{
		AenChange:    true,
		ServerChange: nil,
	}
	if conn := s.getLiveConnection(clusterConnectionsList, pair.ClusterNqn); conn != nil {
		s.multiplexNewConnection(conn)
		s.wg.Add(1)
		s.log.Debugf("Pushing AEN notification to live %s to trigger discovery on new connection", conn)
		conn.AENChan <- aen
		s.log.Debugf("Returned from pushing AEN notification to live %s to trigger discovery on new connection", conn)
		return
	}
	ticker := time.NewTicker(s.reconnectInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if conn := s.getLiveConnection(clusterConnectionsList, pair.ClusterNqn); conn != nil {
				s.multiplexNewConnection(conn)
				s.wg.Add(1)
				s.log.Debugf("Pushing AEN notification to live connection %s to trigger discovery on it", conn)
				conn.AENChan <- aen
				return
			}
		case <-s.ctx.Done():
			s.log.Infof("Discovery client canceled, aborting attempts to connect to cluster %s from %s", pair.ClusterNqn, pair.HostNqn)
			return
		}
	}
}

func (s *service) getLiveConnection(connections []*clientconfig.Connection, subsysNqn string) *clientconfig.Connection {
	for _, conn := range connections {
		_, _, _, err := s.getConnectionEntries(conn, kato)
		if err == nil {
			s.log.Infof("connected successfully to cluster %s with %s", subsysNqn, conn)
			return conn
		}
		s.log.WithError(err).Errorf("Failed to connect with %s", conn)
	}
	s.log.Errorf("Failed to connect to cluster %s with all connections. Retry in %v seconds", subsysNqn, s.reconnectInterval)
	return nil
}

// A function for removing connections that are no longer cached
func (s *service) removeConnections(fromCache clientconfig.ConnectionMap) {
	for pair, cachedClusterConnections := range fromCache {
		serviceClusterConnections, ok := s.connections[pair]
		if !ok {
			// New cluster-client pair from cache. No existing service connections to remove
			continue
		}
		for key, conn := range serviceClusterConnections.ClusterConnectionsMap {
			if !cachedClusterConnections.Exists(conn) {
				s.log.Infof("remove %s from service connections", conn)
				if serviceClusterConnections.ActiveConnection == conn {
					s.log.Debugf("Connection is active, disconnecting it")
					if err := s.hostAPI.Disconnect(conn.ConnectionID); err != nil {
						s.log.WithError(err).Errorf("Error in disconnecting %s", conn)
					}
				}
				s.log.Debugf("Deleting %+v from service connections", conn)
				delete(serviceClusterConnections.ClusterConnectionsMap, key)
				conn.Stop()
			}
		}
	}
}

// A function for dealing with new connections from cache
func (s *service) addConnections(cached clientconfig.ConnectionMap) {
	for pair, clusterConnections := range cached {
		for key, conn := range clusterConnections.ClusterConnectionsMap {
			if !s.connections[pair].Exists(conn) {
				//update service connections
				s.connections.AddConnection(key, conn)
				s.log.Debugf("add %s to service", conn)
			}
		}
	}
}

// Stop run logic of discovery client
func (s *service) Stop() error {
	s.cancel()
	for subsysNqn, clusteConnections := range s.connections {
		for key, conn := range clusteConnections.ClusterConnectionsMap {
			if err := s.hostAPI.Disconnect(conn.ConnectionID); err != nil {
				s.log.WithError(err).Errorf("Error in disconnecting connection %s", conn)
			}
			delete(s.connections[subsysNqn].ClusterConnectionsMap, key)
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
