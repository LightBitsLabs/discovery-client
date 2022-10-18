package nvme

import (
	"bufio"
	"fmt"
	"io"
	"net"

	"github.com/lightbitslabs/discovery-client/pkg/metrics"
	"github.com/lunixbochs/struc"
	"github.com/sirupsen/logrus"
)

//#include <linux/nvme-tcp.h>
import "C"

type nvmetTransport interface {
	queueResponse(Request)
}

type tcpQueue struct {
	nvmeQueue
	tcpConn               net.Conn
	tcpReader             *bufio.Reader
	tcpWriter             *bufio.Writer
	completedNvmeRequests chan Request
	log                   *logrus.Entry
	doneCh                chan interface{}
}

func newNvmeTCPQueue(discoverySubsystem DiscoverySubsystem, id uint16, tcpConn net.Conn, serviceID string, controllerID uint16) *tcpQueue {
	queue := &tcpQueue{
		nvmeQueue: nvmeQueue{
			id:                 id,
			discoverySubsystem: discoverySubsystem,
			serviceID:          serviceID,
			controllerID:       controllerID,
			keepAliveCh:        make(chan bool),
		},
		tcpConn:               tcpConn,
		log:                   logrus.WithFields(logrus.Fields{"queue_id": id, "local_addr": tcpConn.LocalAddr(), "remote_addr": tcpConn.RemoteAddr()}),
		tcpReader:             bufio.NewReader(tcpConn),
		tcpWriter:             bufio.NewWriter(tcpConn),
		completedNvmeRequests: make(chan Request),
		doneCh:                make(chan interface{}),
	}
	queue.nvmeQueue.log = queue.log

	metrics.Metrics.TCPQueues.WithLabelValues(serviceID, queue.tcpConn.LocalAddr().String(), queue.tcpConn.RemoteAddr().String()).Inc()

	return queue
}

func (queue *tcpQueue) destroy() {
	queue.tcpConn.Close()
	close(queue.completedNvmeRequests)
	queue.completedNvmeRequests = nil

	queue.nvmeQueue.destroy()
	metrics.Metrics.TCPQueues.DeleteLabelValues(queue.serviceID, queue.tcpConn.LocalAddr().String(), queue.tcpConn.RemoteAddr().String())
	logrus.Infof("deleted tcp queue: %d", queue.id)
}

func pduSize(pduType uint8) int {
	switch pduType {
	case C.nvme_tcp_icreq:
		return C.sizeof_struct_nvme_tcp_icreq_pdu
	case C.nvme_tcp_cmd:
		return C.sizeof_struct_nvme_tcp_cmd_pdu
	default:
		panic("BUG!!")
	}
	return 0
}

func pduValid(pduType uint8) bool {
	switch pduType {
	case C.nvme_tcp_icreq, C.nvme_tcp_cmd:
		return true
	}
	return false
}

func (queue *tcpQueue) recvTCPHeader() (*TCPHeaderType, error) {
	hdr := &TCPHeaderType{}

	err := struc.Unpack(queue.tcpReader, hdr)
	if err != nil {
		return nil, err
	}

	if !pduValid(hdr.Type) {
		return nil, fmt.Errorf("unexpected pdu type %v", hdr.Type)
	}

	if int(hdr.Hlen) != pduSize(hdr.Type) {
		return nil, fmt.Errorf("pdu type %v bad hlen %v", hdr.Type, hdr.Hlen)
	}
	return hdr, nil
}

func (queue *tcpQueue) nvmeConnect() error {
	hdr, err := queue.recvTCPHeader()
	if err != nil {
		queue.log.WithError(err).Errorf("recv header failed")
		return err
	}

	if hdr.Type != C.nvme_tcp_icreq {
		return fmt.Errorf("unexpected pdu type %v", hdr.Type)
	}

	icReq := &TCPIcReqPduType{}
	err = struc.Unpack(queue.tcpReader, icReq)
	if err != nil {
		return err
	}

	if hdr.Plen != C.sizeof_struct_nvme_tcp_icreq_pdu {
		return fmt.Errorf("bad nvme-tcp pdu length (%d)", hdr.Hlen)
	}

	if icReq.Pfv != C.NVME_TCP_PFV_1_0 {
		return fmt.Errorf("queue %d: bad pfv %d", queue.id, icReq.Pfv)
	}

	if icReq.Hpda != 0 {
		return fmt.Errorf("queue %d: unsupported hpda %d", queue.id, icReq.Hpda)
	}

	connectRespTCPHeader := &TCPHeaderType{
		Type: C.nvme_tcp_icresp,
		Hlen: C.sizeof_struct_nvme_tcp_icresp_pdu,
		Plen: C.sizeof_struct_nvme_tcp_icresp_pdu,
	}
	connectResp := &TCPIcrespPdu{
		Pfv:     C.NVME_TCP_PFV_1_0,
		Maxdata: 0x10000,
	}

	err = struc.Pack(queue.tcpWriter, connectRespTCPHeader)
	if err != nil {
		queue.log.WithError(err).Infof("failed to serialize tcp header")
		return err
	}

	err = struc.Pack(queue.tcpWriter, connectResp)
	if err != nil {
		queue.log.WithError(err).Infof("failed to serialize connect initialize response")
		return err
	}

	return queue.tcpWriter.Flush()
}

func (queue *tcpQueue) sendCqe(cqe *Completion) error {

	hdr := &TCPHeaderType{
		Hlen: C.sizeof_struct_nvme_tcp_rsp_pdu,
		Plen: C.sizeof_struct_nvme_tcp_rsp_pdu,
		Type: C.nvme_tcp_rsp,
	}

	// TODO: digest is not supported yet
	if err := struc.Pack(queue.tcpWriter, hdr); err != nil {
		queue.log.WithError(err).Infof("failed to serialize cqe header")
		return err
	}

	return struc.Pack(queue.tcpWriter, cqe)
}

func (queue *tcpQueue) sendDataPdu(nvmeRequest Request) error {
	hdr := &TCPHeaderType{
		Hlen: C.sizeof_struct_nvme_tcp_rsp_pdu,
		Plen: C.sizeof_struct_nvme_tcp_rsp_pdu,
		Type: C.nvme_tcp_rsp,
	}
	hdr.Type = C.nvme_tcp_c2h_data
	hdr.Flags = C.NVME_TCP_F_DATA_LAST
	hdr.Hlen = C.sizeof_struct_nvme_tcp_data_pdu
	if nvmeRequest.dataLen() > 0 {
		hdr.Pdo = C.sizeof_struct_nvme_tcp_data_pdu
	} else {
		hdr.Pdo = 0
	}
	hdr.Plen = int(C.sizeof_struct_nvme_tcp_data_pdu + nvmeRequest.dataLen())

	pdu := &TCPDataPDU{
		CommandID:  nvmeRequest.CommandID(),
		DataLength: uint32(nvmeRequest.dataLen()),
		DataOffset: 0,
	}

	// Digest is not supported yet

	// send tcp header
	if err := struc.Pack(queue.tcpWriter, hdr); err != nil {
		queue.log.WithError(err).Infof("failed to serialize data pdu")
		return err
	}

	// send pdu
	if err := struc.Pack(queue.tcpWriter, pdu); err != nil {
		queue.log.WithError(err).Infof("failed to serialize data pdu")
		return err
	}

	// Now send the data
	if _, err := io.Copy(queue.tcpWriter, NewScatterListReader(nvmeRequest.GetData())); err != nil {
		queue.log.WithError(err).Infof("failed to copy sgl to tcpWriter")
		return err
	}

	return nil
}

func (queue *tcpQueue) pollResponseChannel() {
	var err error
	for nvmeRequest := range queue.completedNvmeRequests {
		if needsDataOut(nvmeRequest) {
			err = queue.sendDataPdu(nvmeRequest)
			if err != nil {
				break
			}
		}

		err = queue.sendCqe(nvmeRequest.Completion())
		if err != nil {
			break
		}

		logrus.Debugf("request completed: %s", nvmeRequest.String())

		if err := queue.tcpWriter.Flush(); err != nil {
			break
		}
	}
	queue.log.Infof("exiting TX loop")
}

func (queue *tcpQueue) queueResponse(nvmeRequest Request) {
	queue.completedNvmeRequests <- nvmeRequest
}

func (queue *tcpQueue) mapData(tcpRequest TCPRequest) uint16 {
	length := tcpRequest.NvmeRequest().dataLen()
	if length == 0 {
		return C.NVME_SC_SUCCESS
	}

	if tcpRequest.NvmeRequest().dptr().sgl()._type == ((C.NVME_SGL_FMT_DATA_DESC << 4) | C.NVME_SGL_FMT_OFFSET) {
		if !tcpRequest.NvmeRequest().isWrite() {
			return C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR
		}

		if length > queue.nvmeQueue.inlineSize() {
			return C.NVME_SC_SGL_INVALID_OFFSET | C.NVME_SC_DNR
		}
		tcpRequest.SetPDULength(uint32(length))
	}
	sgl := NewScatterList(int(length), 8192)
	tcpRequest.NvmeRequest().SetData(sgl)
	return C.NVME_SC_SUCCESS
}

// in case we close the returned channel means that the method has ended.
// case there was an error we will send it on the channel.
func (queue *tcpQueue) handleReceivePdu() <-chan error {
	errChan := make(chan error)
	go func() {
		for {
			//defer close(errChan)
			hdr, err := queue.recvTCPHeader()
			if err != nil {
				if err == io.EOF {
					errChan <- err
					return
				}
				queue.log.WithError(err).Errorf("failed to read tcp header")
				errChan <- err
				return
			}

			// no digest support for now
			hdgst := 0
			rcvLeft := int(hdr.Hlen) - C.sizeof_struct_nvme_tcp_hdr + hdgst
			pdu := make([]byte, rcvLeft)

			_, err = io.ReadFull(queue.tcpReader, pdu)
			if err != nil {
				queue.log.WithError(err).Infof("failed to read pdu")
				errChan <- err
				return
			}

			tcpRequest := NewTCPRequest()
			request, err := queue.nvmetRequestInit(queue, pdu)
			tcpRequest.SetNvmeRequest(request)
			if err != nil {
				// if we are here response was sent
				// shift status back (the status code is 15 bits) shifted in completeRequest()
				stt := tcpRequest.NvmeRequest().Completion().Status >> 1

				if stt&C.NVME_SC_INVALID_LOG_PAGE == C.NVME_SC_INVALID_LOG_PAGE {
					// yet to support all the log-pages, some are optional
					// and we do NOT want to close the queue.
					continue
				}
				errChan <- err
				return
			}

			status := queue.mapData(tcpRequest)
			if status != C.NVME_SC_SUCCESS {
				if tcpRequest.HasInlineData() {
					errChan <- fmt.Errorf("failed to create data map but tcpRequest has inline data")
					return
				}
				queue.log.Warnf("mapData failed. status: %d", status)
				queue.completeRequest(request, NewCompletion(request.CommandID(), queue.sq.qID, status))
				return
			}

			if tcpRequest.NeedDataIn() {
				if !tcpRequest.HasInlineData() {
					errChan <- fmt.Errorf("non inline data is not yet supported. nvmeRequest %s", tcpRequest.NvmeRequest().String())
					return
				}

				sglWriter := NewScatterListWriter(tcpRequest.NvmeRequest().GetData())
				_, err := io.CopyN(sglWriter, queue.tcpReader, int64(tcpRequest.GetPDULength()))
				if err != nil {
					queue.log.WithError(err).Errorf("failed to copy sgl data to tcp queue")
					errChan <- err
					return
				}
			}

			if tcpRequest.NvmeRequest().Completion() != nil && tcpRequest.NvmeRequest().Completion().Status != C.NVME_SC_SUCCESS {
				queue.log.Warnf("request %s failed. status: %#02x",
					tcpRequest.NvmeRequest().String(),
					tcpRequest.NvmeRequest().Completion().Status)
			} else {
				tcpRequest.NvmeRequest().execute()
			}
		}
	}()
	return errChan
}

func (queue *tcpQueue) doneChan() chan interface{} {
	return queue.doneCh
}

// doneCh will signal when the ioWork method is done.
func (queue *tcpQueue) ioWork() {
	go func() {
		defer close(queue.doneCh)

		err := queue.nvmeConnect()
		if err != nil {
			queue.log.WithError(err).Infof("nvme connect failed")
			return
		}

		go queue.pollResponseChannel()

		receivePduErrChan := queue.handleReceivePdu()

		select {
		case err := <-receivePduErrChan:
			if err == io.EOF {
				queue.log.Infof("tcp stream ended. %v", err)
			} else {
				queue.log.WithError(err).Errorf("got error from pdu goroutine")
			}
		case <-queue.nvmeQueue.keepAliveCh:
			queue.log.WithError(err).Errorf("keep alive expired")
		}

		queue.log.Info("goroutines closed - ioWork closed")
	}()
}
