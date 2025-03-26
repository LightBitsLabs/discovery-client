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

package nvmehost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net"
	"reflect"
	"strings"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/nvme"
	"github.com/lunixbochs/struc"
	"github.com/sirupsen/logrus"
)

//#cgo CFLAGS: -I../
//#include <linux/nvme-tcp.h>
import "C"

const (
	// important not to put too small
	waitForReplyTimeout = 5000000000 // 5 seconds
)

type nvmetTransport interface {
	queueResponse(nvme.Request)
}

type tcpQueue struct {
	// nvmeQueue
	tcpConn               net.Conn
	tcpReader             *bufio.Reader
	tcpWriter             *bufio.Writer
	outstandingRequests   map[uint16]nvme.Request
	log                   *logrus.Entry
	doneCh                chan interface{}
	id                    uint16
	commandID             uint16
	completedRequestsChan chan nvme.Request
	completedAENRequestCh chan nvme.Request
}

func newNvmeTCPQueue(id uint16, tcpConn net.Conn) *tcpQueue {
	queue := &tcpQueue{
		id:                    id,
		tcpConn:               tcpConn,
		log:                   logrus.WithFields(logrus.Fields{"queue_id": id, "local_addr": tcpConn.LocalAddr(), "remote_addr": tcpConn.RemoteAddr()}),
		tcpReader:             bufio.NewReader(tcpConn),
		tcpWriter:             bufio.NewWriter(tcpConn),
		outstandingRequests:   make(map[uint16]nvme.Request),
		doneCh:                make(chan interface{}),
		completedRequestsChan: make(chan nvme.Request),
		completedAENRequestCh: make(chan nvme.Request),
		commandID:             0x01,
	}
	// queue.nvmeQueue.log = queue.log
	// metrics.Metrics.TCPQueues.WithLabelValues(serviceID, queue.tcpConn.LocalAddr().String(), queue.tcpConn.RemoteAddr().String()).Inc()

	return queue
}

func (queue *tcpQueue) destroy() {
	queue.tcpConn.Close()
	// metrics.Metrics.TCPQueues.DeleteLabelValues(queue.serviceID, queue.tcpConn.LocalAddr().String(), queue.tcpConn.RemoteAddr().String())
}

// https://github.com/torvalds/linux/blob/1ee08de1e234d95b5b4f866878b72fceb5372904/drivers/nvme/host/tcp.c
func (queue *tcpQueue) sendNvmeInitConnection() error {
	hdr := &nvme.TCPHeaderType{
		Hlen:  C.sizeof_struct_nvme_tcp_icreq_pdu,
		Plen:  C.sizeof_struct_nvme_tcp_icreq_pdu,
		Pdo:   0,
		Type:  C.nvme_tcp_icreq,
		Flags: 0,
	}

	if err := struc.Pack(queue.tcpWriter, hdr); err != nil {
		return err
	}

	icReq := &nvme.TCPIcReqPduType{
		Pfv:    C.NVME_TCP_PFV_1_0,
		Hpda:   0, /* no alignment constraint */
		Digest: 0,
		Maxr2t: 0,
	}
	if err := struc.Pack(queue.tcpWriter, icReq); err != nil {
		return err
	}
	if err := queue.tcpWriter.Flush(); err != nil {
		return err
	}

	return nil
}

func (queue *tcpQueue) recvNvmeInitConnResponse(pduReader *bytes.Reader) (*nvme.TCPIcrespPdu, error) {
	icresp := &nvme.TCPIcrespPdu{}
	if err := struc.Unpack(pduReader, icresp); err != nil {
		return nil, err
	}

	if icresp.Pfv != C.NVME_TCP_PFV_1_0 {
		return nil, fmt.Errorf("queue %d: bad pfv returned %d", queue.id, icresp.Pfv)
	}

	if icresp.Cpda != 0 {
		return nil, fmt.Errorf("queue %d: unsupported cpda returned %d", queue.id, icresp.Cpda)
	}
	return icresp, nil
}

func (queue *tcpQueue) sendConnectRequest(ctx context.Context, hostnqn string, hostID string) error {
	theNewHostID, _ := hex.DecodeString(hostID)
	connectData := &nvme.ConnectData{
		HostID:    string(theNewHostID),
		CntlID:    0xffff,
		SubsysNqn: nvme.DiscoverySubsysName,
		HostNqn:   hostnqn,
	}

	request := nvme.NewAdminConnectRequest(queue.nextCmdID(), 0*time.Millisecond, connectData)

	if err := struc.Pack(queue.tcpWriter, createTCPHeader(true)); err != nil {
		return err
	}
	if err := request.PackCmd(queue.tcpWriter); err != nil {
		return err
	}
	// copy sgl to tcpWriter
	if _, err := io.Copy(queue.tcpWriter, nvme.NewScatterListReader(request.GetData())); err != nil {
		return err
	}

	queue.outstandingRequests[request.CommandID()] = request

	if err := queue.tcpWriter.Flush(); err != nil {
		return err
	}

	if _, err := queue.waitForResponse(ctx); err != nil {
		return err
	}

	return nil
}

func createTCPHeader(isConnect bool) *nvme.TCPHeaderType {
	var dataLenBytes int
	flags := uint8(0)
	cmdLenBytes := uint8(C.sizeof_struct_nvme_tcp_cmd_pdu)
	if isConnect {
		dataLenBytes = C.sizeof_struct_nvmf_connect_data
	} else {
		dataLenBytes = 0
	}

	header := &nvme.TCPHeaderType{
		Type:  C.nvme_tcp_cmd,
		Flags: flags,
		Hlen:  cmdLenBytes,
		Plen:  int(cmdLenBytes) + dataLenBytes,
	}
	return header
}

func (queue *tcpQueue) sendSetPropertyRequest(ctx context.Context, registerOffset uint32, val uint64) error {
	cmdID := queue.nextCmdID()
	request := nvme.NewPropertySetRequest(cmdID, registerOffset, val)
	if err := queue.sendRequest(request); err != nil {
		return err
	}
	if _, err := queue.waitForResponse(ctx); err != nil {
		return err
	}
	return nil
}

func (queue *tcpQueue) nextCmdID() uint16 {
	cmdID := queue.commandID
	queue.commandID++
	return cmdID
}

func (queue *tcpQueue) sendGetPropertyRequest(ctx context.Context, registerOffset uint32) error {
	cmdID := queue.nextCmdID()
	request := nvme.NewPropertyGetRequest(cmdID, registerOffset)
	if err := queue.sendRequest(request); err != nil {
		return err
	}
	if _, err := queue.waitForResponse(ctx); err != nil {
		return err
	}
	return nil
}

func (queue *tcpQueue) setControllerConfiguration(ctx context.Context, controllerConfigValue uint64) error {
	err := queue.sendSetPropertyRequest(ctx, C.NVME_REG_CC, controllerConfigValue)
	if err != nil {
		return err
	}

	err = queue.sendGetPropertyRequest(ctx, C.NVME_REG_CSTS)
	if err != nil {
		return err
	}
	return nil
}

func (queue *tcpQueue) setProperties(ctx context.Context, persistent bool) error {
	/* phases:
	+ configures the  controller’s settings by writing the Controller Configuration property,
	  including setting CC.EN to ‘1’ to enable command processing
	+ wait for ready  ( read Controller Status)
	The host waits for the controller to indicate that the controller is ready to process commands.
	The controller is ready to process commands when CSTS.RDY is set to ‘1’ in the
	Controller Status property

	// The host determines the features and capabilities of the controller by
	+ issuing the Identify command,	specifying the Controller data structure.

	// After initializing the Discovery controller,
	//https://nvmexpress.org/wp-content/uploads/NVMe-over-Fabrics-1.1-2019.10.22-Ratified.pdf
	+ the host reads the Discovery Log Page. Refer to section 5.3
	*/

	if err := queue.sendGetPropertyRequest(ctx, C.NVME_REG_CAP); err != nil {
		return err
	}

	if err := queue.setControllerConfiguration(ctx, 0x460001); err != nil {
		return err
	}

	if err := queue.sendGetPropertyRequest(ctx, C.NVME_REG_VS); err != nil {
		return err
	}
	// YOGEV: add this version check that we don on the
	// if 1 == cqe.Result.Result[2] && 3 == cqe.Result.Result[1] && 0 == cqe.Result.Result[0] {
	// 	queue.log.Infof("same version, major minor")
	// }
	if err := queue.sendGetPropertyRequest(ctx, C.NVME_REG_CAP); err != nil {
		return err
	}
	return nil
}

func (queue *tcpQueue) recvCqe(pduReader *bytes.Reader) (*nvme.Completion, error) {
	cqe := &nvme.Completion{}
	if err := struc.Unpack(pduReader, cqe); err != nil {
		return nil, err
	}
	if cqe.Status != C.NVME_SC_SUCCESS {
		return cqe, fmt.Errorf("nvme completion failed: id: %#04x, Status: %#02x", cqe.CommandID, cqe.Status)
	}
	return cqe, nil
}

func (queue *tcpQueue) recvTCPDataPdu(pduReader *bytes.Reader) (*nvme.TCPDataPDU, error) {
	c2hData := &nvme.TCPDataPDU{}
	if err := struc.Unpack(pduReader, c2hData); err != nil {
		return nil, err
	}
	return c2hData, nil
}

func (queue *tcpQueue) recvIdentifyDataPdu(pduReader *bytes.Reader) error {
	id := &nvme.IDCtrl{}
	if err := struc.Unpack(pduReader, id); err != nil {
		return err
	}
	subNqn := strings.TrimRight(string(id.SubNqn[:]), "\x00")

	if subNqn != C.NVME_DISC_SUBSYS_NAME {
		return fmt.Errorf("subNqn must equal %q", C.NVME_DISC_SUBSYS_NAME)
	}
	return nil
}

func (queue *tcpQueue) parseDiscRspHeader(pduReader *bytes.Reader) (uint64, uint64, error) {
	pageHeader := &nvme.DiscRspPageHdr{}
	if err := struc.Unpack(pduReader, pageHeader); err != nil {
		return 0, 0, err
	}
	return pageHeader.GenCtr, pageHeader.NumRec, nil
}

func (queue *tcpQueue) recvLogPageDataPdu(pduReader *bytes.Reader, offset uint64, numRec uint64) ([]*nvme.NvmefDiscRspPageEntry, error) {
	var entries []*nvme.NvmefDiscRspPageEntry
	entrySizeInBytes, _ := struc.Sizeof(&nvme.NvmefDiscRspPageEntry{})
	for i := offset / 1024; i < numRec && pduReader.Len() >= entrySizeInBytes; i++ {
		queue.log.Debugf("recovering entry number %d of %d", i, numRec)
		entry := &nvme.NvmefDiscRspPageEntry{}
		if err := struc.Unpack(pduReader, entry); err != nil {
			queue.log.WithError(err).Errorf("fail read response entry")
			return entries, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (queue *tcpQueue) handleAEN(ctx context.Context) (nvme.Request, error) {
	if err := queue.sendAsyncEventRequest(); err != nil {
		return nil, fmt.Errorf("failed calling async event request")
	}

	select {
	case request := <-queue.completedAENRequestCh:
		return request, nil
	case <-ctx.Done():
		break
	}
	return nil, nil
}

func (queue *tcpQueue) keepAliveDone() chan interface{} {
	return queue.doneCh
}

func (queue *tcpQueue) keepAlive(ctx context.Context, kato time.Duration) {
	ticker := time.NewTicker(kato / 2)
	defer close(queue.doneCh)
	for {
		select {
		case <-ticker.C:
			err := queue.sendKeepAlive(ctx)
			if err != nil {
				// keep alive received error ending
				// keepAliveDone will be closed
				queue.log.WithError(err).Errorf("keep alive received error, End")
				return
			}

		case <-ctx.Done():
			queue.log.Info("keep alive context done")
			return
		}
	}
}

func (queue *tcpQueue) sendKeepAlive(ctx context.Context) error {
	// we reuse cmd id 0 cause it was already in use by connect so we know
	// it will never get used by any other commnad
	cmdID := uint16(0) //queue.nextCmdID()
	request := nvme.NewKeepAliveRequest(cmdID)

	if err := queue.sendRequest(request); err != nil {
		return err
	}
	if _, err := queue.waitForResponse(ctx); err != nil {
		return err
	}
	return nil
}

func (queue *tcpQueue) sendRequest(request nvme.Request) error {
	var header *nvme.TCPHeaderType
	requestType := reflect.TypeOf(request).String()
	if requestType == "*nvme.AdminConnectRequest" {
		header = createTCPHeader(true)
	} else {
		header = createTCPHeader(false)
	}
	if err := struc.Pack(queue.tcpWriter, header); err != nil {
		return err
	}
	if err := request.PackCmd(queue.tcpWriter); err != nil {
		return err
	}
	queue.outstandingRequests[request.CommandID()] = request
	if err := queue.tcpWriter.Flush(); err != nil {
		return err
	}
	return nil
}

func (queue *tcpQueue) sendIdentifyRequest(ctx context.Context) error {
	request := nvme.NewIdentifyRequest(queue.nextCmdID()) //C.nvme_admin_identify
	if err := queue.sendRequest(request); err != nil {
		return err
	}
	completedRequest, err := queue.waitForResponse(ctx)
	if err != nil {
		return err
	}

	// did we get back a valid response?
	if completedRequest == nil {
		return fmt.Errorf("queue %d [%v]: got nil identify response",
			queue.id, queue.tcpConn.RemoteAddr())
	}

	data := completedRequest.GetData()
	if data == nil {
		return fmt.Errorf("queue %d [%v]: got identify response with nil data",
			queue.id, queue.tcpConn.RemoteAddr())
	}

	// copy sgl to buffer
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, nvme.NewScatterListReader(data)); err != nil {
		return err
	}
	pduReader := bytes.NewReader(buf.Bytes())

	if err := queue.recvIdentifyDataPdu(pduReader); err != nil {
		return err
	}

	return nil
}

func pduSize(pduType uint8) int {
	switch pduType {
	case C.nvme_tcp_icresp:
		return C.sizeof_struct_nvme_tcp_icresp_pdu
	case C.nvme_tcp_rsp:
		return C.sizeof_struct_nvme_tcp_rsp_pdu
	case C.nvme_tcp_c2h_data:
		return C.sizeof_struct_nvme_tcp_data_pdu
	default:
		panic("BUG!!")
	}
	return 0
}

func pduValid(pduType uint8) bool {
	switch pduType {
	case C.nvme_tcp_icresp, C.nvme_tcp_rsp, C.nvme_tcp_c2h_data:
		return true
	}
	return false
}

func (queue *tcpQueue) recvTCPHeader() (*nvme.TCPHeaderType, error) {
	hdr := &nvme.TCPHeaderType{}
	if err := struc.Unpack(queue.tcpReader, hdr); err != nil {
		return hdr, err
	}

	if !pduValid(hdr.Type) {
		return hdr, fmt.Errorf("unexpected pdu type %v", hdr.Type)
	}

	if int(hdr.Hlen) != pduSize(hdr.Type) {
		return hdr, fmt.Errorf("pdu type %v bad hlen %v", hdr.Type, hdr.Hlen)
	}
	return hdr, nil
}

func (queue *tcpQueue) getLogPageEntries(ctx context.Context, logPagePaginationEnabled bool) ([]*nvme.NvmefDiscRspPageEntry, error) {
	numRec, genCtr, _, err := queue.sendDiscLogPageRequest(ctx, 1024, 0, 0xffffffff, 0)
	if err != nil {
		return nil, err
	}
	var res []*nvme.NvmefDiscRspPageEntry
	offset := uint64(0)
	if logPagePaginationEnabled {
		// retrieving with pagination
		for uint64(len(res)) < numRec {
			queue.log.Debug("loop --", uint64(len(res)), numRec)
			_, _, entries, err := queue.sendDiscLogPageRequest(ctx, 4096, offset, 0x00000000, numRec)
			if err != nil {
				return nil, err
			}
			res = append(res, entries...)
			offset = uint64(len(res) * 1024)
		}
	} else {
		// retrieving log page entries without pagination
		eSize, _ := struc.Sizeof(&nvme.NvmefDiscRspPageEntry{})
		entrySize := uint32(eSize)
		hSize, _ := struc.Sizeof(&nvme.DiscRspPageHdr{})
		headerSize := uint32(hSize)
		requestSize := headerSize + uint32(numRec)*entrySize
		_, _, res, err = queue.sendDiscLogPageRequest(ctx, requestSize, 0, 0x00000000, numRec)
		if err != nil {
			return nil, err
		}
	}

	if uint64(len(res)) != numRec {
		err = fmt.Errorf("number of obtained entries differs from numRec")
		queue.log.WithError(err).Errorf("Expected %d entries, received %d entries", numRec, len(res))
		return nil, err
	}

	_, newGenCtr, _, err := queue.sendDiscLogPageRequest(ctx, 1024, 0, 0x00000000, 0)
	if err != nil {
		return nil, err
	}
	if genCtr != newGenCtr {
		return nil, fmt.Errorf("genCtr changed during GetLogPage. issue another discover request")
	}
	return res, nil
}

func (queue *tcpQueue) waitForResponse(ctx context.Context) (nvme.Request, error) {
	ctxWaitResponse, ctxWaitREsponsecancel := context.WithTimeout(ctx, waitForReplyTimeout)
	defer ctxWaitREsponsecancel()
	select {
	case <-ctxWaitResponse.Done():
		return nil, ctxWaitResponse.Err()
	case completedRequest := <-queue.completedRequestsChan:
		return completedRequest, nil
	}
	return nil, fmt.Errorf("aborted")
}

func (queue *tcpQueue) sendDiscLogPageRequest(ctx context.Context, size uint32, offset uint64, nsid uint32, num uint64) (uint64, uint64, []*nvme.NvmefDiscRspPageEntry, error) {
	var entries []*nvme.NvmefDiscRspPageEntry
	var numRec, genCtr uint64
	var err error
	request := nvme.NewNvmeGetDiscoveryLogPageRequest(queue.nextCmdID(), size, offset, nsid)
	if err := queue.sendRequest(request); err != nil {
		return 0, 0, entries, err
	}
	select {
	case <-ctx.Done():
		return 0, 0, entries, fmt.Errorf("aborted")

	case completedRequest := <-queue.completedRequestsChan:
		if completedRequest.GetData().Size() == 0 {
			return 0, 0, entries, nil
		}
		// copy sgl to buffer
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, nvme.NewScatterListReader(completedRequest.GetData())); err != nil {
			return 0, 0, entries, err
		}

		pduReader := bytes.NewReader(buf.Bytes())
		genCtr, numRec, err = queue.parseDiscRspHeader(pduReader)
		if err != nil {
			queue.log.WithError(err).Errorf("Failed to parse response header")
			return numRec, genCtr, []*nvme.NvmefDiscRspPageEntry{}, err
		}
		entries, err = queue.recvLogPageDataPdu(pduReader, offset, num) // should ignore numRec
		if err != nil {
			return 0, 0, entries, err
		}
	}
	return numRec, genCtr, entries, nil
}

func (queue *tcpQueue) sendAsyncEventSetFeature(ctx context.Context) error {
	cmdID := queue.nextCmdID()
	request := nvme.NewSetFeatureAsyncEventRequest(cmdID)
	if err := queue.sendRequest(request); err != nil {
		return err
	}
	if _, err := queue.waitForResponse(ctx); err != nil {
		return err
	}
	return nil
}

func (queue *tcpQueue) sendAsyncEventRequest() error {
	cmdID := queue.nextCmdID()
	request := nvme.NewAsyncEventRequest(cmdID)
	return queue.sendRequest(request)
}

func (queue *tcpQueue) handleRecv() error {
	hdr, err := queue.recvTCPHeader()
	if err != nil {
		return err
	}

	// no digest support for now
	hdgst := 0
	rcvLeft := int(hdr.Plen) - C.sizeof_struct_nvme_tcp_hdr + hdgst
	pdu := make([]byte, rcvLeft)

	_, err = io.ReadFull(queue.tcpReader, pdu)
	if err != nil {
		return err
	}

	completedRequest, err := queue.parseResponse(hdr.Type, pdu)
	if err != nil {
		return err
	}
	// this case cover response from init-connection request. it is not
	// the same as all other nvme-requests - we didn't push it to the
	// pending requests queue hence we don't mark it as completed.
	if completedRequest == nil {
		return nil
	}
	if completedRequest.Completion() != nil {
		switch completedRequest.(type) {
		case *nvme.AsyncEventRequest:
			queue.completedAENRequestCh <- completedRequest
		default:
			queue.completedRequestsChan <- completedRequest
		}
	}
	return nil
}

// in case we close the returned channel means that the method has ended.
// case there was an error we will send it on the channel.
func (queue *tcpQueue) recvPdu(ctx context.Context) <-chan error {
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if err := queue.handleRecv(); err != nil {
					errChan <- err // routine can stuck here
					return
				}
			}
		}
	}()
	return errChan
}

func (queue *tcpQueue) parseResponse(pduType uint8, pdu []byte) (nvme.Request, error) {
	var commandID uint16
	pduReader := bytes.NewReader(pdu)
	switch pduType {
	case C.nvme_tcp_icresp:
		_, err := queue.recvNvmeInitConnResponse(pduReader)
		if err != nil {
			return nil, err
		}
		// current code does not handle this response as an outstanding requests.
		// it will not try to match such request from queue.outstandingRequests
		return nil, nil
	case C.nvme_tcp_rsp:
		cqe, err := queue.recvCqe(pduReader)
		// we might got error status but still got the cqe so we proceed with the parsing
		if err != nil && cqe == nil {
			return nil, err
		}
		commandID = cqe.CommandID

		request, ok := queue.outstandingRequests[commandID]
		if !ok {
			// we don't have an outstanding request
			return nil, err
		}
		delete(queue.outstandingRequests, commandID)
		request.SetCompletion(cqe)
		return request, nil

	case C.nvme_tcp_c2h_data:
		c2hData, err := queue.recvTCPDataPdu(pduReader)
		if err != nil {
			return nil, err
		}
		commandID = c2hData.CommandID
		request, ok := queue.outstandingRequests[commandID]
		if !ok {
			// we don't have an outstanding request
			return nil, err
		}

		chunkSize := int(math.Min(1024.0, float64(c2hData.DataLength)))
		request.SetData(nvme.NewScatterList(int(c2hData.DataLength), chunkSize))
		sglWriter := nvme.NewScatterListWriter(request.GetData())

		if _, err := io.Copy(sglWriter, pduReader); err != nil {
			return nil, err
		}
		return request, nil
	default:
		panic("BUG!!")
	}
	return nil, nil
}
