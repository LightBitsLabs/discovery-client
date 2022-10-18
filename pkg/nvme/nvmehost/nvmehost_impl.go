package nvmehost

import (
	"fmt"

	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
	"github.com/sirupsen/logrus"
)

/*

Connection pool struct that apply the HostApi interface
	Discover(discoveryRequest *DiscoverRequest) ([]*NvmeDiscPageEntry, ConnectionID, error)
	Disconnect(connectionID ConnectionID) error

*/

type connInfo struct {
	request *hostapi.DiscoverRequest
	client  TCPClient
}

type hostApiImp struct {
	ConnTbl                  map[hostapi.ConnectionID]*connInfo
	log                      *logrus.Entry
	i                        int
	logPagePaginationEnabled bool
}

func NewHostApi(logPagePaginationEnabled bool) hostapi.HostAPI {

	return &hostApiImp{
		ConnTbl:                  make(map[hostapi.ConnectionID]*connInfo),
		log:                      logrus.WithFields(logrus.Fields{}),
		i:                        1,
		logPagePaginationEnabled: logPagePaginationEnabled,
	}
}

func (h *hostApiImp) Discover(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, hostapi.ConnectionID, error) {
	// convert discovery request type
	req := createDiscoveryRequest(discoveryRequest)
	client := NewClient(h.logPagePaginationEnabled) // creates aenCh

	response, err := client.Discover(req)
	if discoveryRequest.Kato == 0 {
		client.Stop()
		return createDiscoveryEntries(response), hostapi.ConnectionID("0"), err
	}

	// if err on discover - kill, return connection_id
	connection_id, found := h.findClient(discoveryRequest)
	h.Disconnect(connection_id)

	if err != nil {
		h.log.Debugf("Failed discovery (kato=%v) was in db? %v", discoveryRequest.Kato, found)
		return nil, connection_id, err
	}

	// if found in db -> disconnect previous and replace
	if found {
		h.log.Debugf("replacing %v", connection_id)
	} else {
		h.log.Debugf("creating new %v", connection_id)
		h.i++
	}

	h.ConnTbl[connection_id] = &connInfo{
		request: discoveryRequest,
		client:  client,
	}

	go h.handleChannel(connection_id)

	return createDiscoveryEntries(response), connection_id, nil
}

func (h *hostApiImp) handleChannel(connection_id hostapi.ConnectionID) {

	info, ok := h.ConnTbl[connection_id]
	if !ok {
		panic("started loop for non exist cid")
	}
	h.log.Debugf("Start CHandler [cid=%v]", connection_id)
	for {
		select {
		case _, aenOk := <-info.client.AENChan():
			if !aenOk {
				h.log.Debugf("CHandler found aen is closed.")
				return
			}
			hostApiAEN := hostapi.AENStruct{AenChange: true, ServerChange: nil}
			h.log.Debugf("Pushing to ip %s aen notification", info.request.Traddr)
			info.request.AENChan <- hostApiAEN
			h.log.Debugf("Returned from pushing to ip %s aen notification", info.request.Traddr)
			// todo: should kill ?
			continue

		case <-info.client.KAChan():
			// incase ka closed we like to notify connection lost
			hostApiAEN := hostapi.AENStruct{AenChange: true, ServerChange: fmt.Errorf("keep alive died")}
			h.log.Debugf("Pushing to ip %s ka dead notification", info.request.Traddr)
			info.request.AENChan <- hostApiAEN
			h.log.Debugf("Returned from pushing to ip %s ka deadnotification", info.request.Traddr)
			return
		}
	}
}

func (h *hostApiImp) Disconnect(connectionID hostapi.ConnectionID) error {
	info, ok := h.ConnTbl[connectionID]
	if !ok {
		err := fmt.Errorf("connection with id %v not found", connectionID)
		//h.log.WithError(err).Debug("Failed to disconnect")
		return err
	}

	delete(h.ConnTbl, connectionID)
	info.client.Stop()
	h.log.Debugf("cid %v  removed", connectionID)
	return nil
}

func (h *hostApiImp) nextConnectionID() hostapi.ConnectionID {
	return hostapi.ConnectionID(fmt.Sprintf("%d", h.i))
}

func (h *hostApiImp) findClient(r *hostapi.DiscoverRequest) (hostapi.ConnectionID, bool) {
	// if not found return new connection id
	for cid, c := range h.ConnTbl {
		s := c.request
		// compare all fields but Kato
		if s.Transport == r.Transport &&
			s.Traddr == r.Traddr &&
			s.Trsvcid == r.Trsvcid &&
			s.Hostnqn == r.Hostnqn &&
			s.Hostaddr == r.Hostaddr {

			if s.Kato != r.Kato {
				// same request but different kato field [interval]
				// treat as new
				// but should we update ? (if persistent)
				continue
			}
			//h.log.Infof("found client in db, connection_id=%v", cid)
			return cid, true
		}
	}
	return h.nextConnectionID(), false
}

/*
 funtions of type conversions
*/

func createDiscoveryRequest(discoveryRequest *hostapi.DiscoverRequest) *DiscoverRequest {
	// convert discover requests
	return &DiscoverRequest{
		Transport: discoveryRequest.Transport,
		Traddr:    discoveryRequest.Traddr,
		Trsvcid:   discoveryRequest.Trsvcid,
		Hostnqn:   discoveryRequest.Hostnqn,
		Hostaddr:  discoveryRequest.Hostaddr,
		Kato:      discoveryRequest.Kato,
	}
}

func createDiscoveryEntries(entries []*NvmeDiscPageEntry) []*hostapi.NvmeDiscPageEntry {
	// convert discovery log pages entries
	response := []*hostapi.NvmeDiscPageEntry{}
	if entries == nil {
		return response
	}
	for _, entry := range entries {
		res := &hostapi.NvmeDiscPageEntry{
			PortID:  entry.PortID,
			CntlID:  entry.CntlID,
			SubType: entry.SubType,
			TrsvcID: entry.TrsvcID,
			Subnqn:  entry.Subnqn,
			Traddr:  entry.Traddr,
		}
		response = append(response, res)
	}
	return response
}
