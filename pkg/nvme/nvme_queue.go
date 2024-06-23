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

package nvme

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/lunixbochs/struc"
	"github.com/sirupsen/logrus"
)

//#include <linux/nvme.h>
import "C"

const (
	nvmetQueueSize = 1024
)

type nvmetCQ struct {
	qID  uint16
	size uint16
}

type nvmetSQ struct {
	ctrl         *nvmeController
	qID          uint16
	size         uint16
	sqhd         uint32
	sqhdDisabled bool
}

type nvmeQueue struct {
	id                   uint16
	cq                   nvmetCQ
	sq                   nvmetSQ
	discoverySubsystem   DiscoverySubsystem
	log                  *logrus.Entry
	keepAliveCh          chan bool
	keepAliveWatchStopCh chan interface{}
	serviceID            string
	controllerID         uint16
}

func (queue *nvmeQueue) nvmetParseConnectCmd(pdu []byte) (Request, error) {
	var request = &AdminConnectRequest{}
	if err := struc.Unpack(bytes.NewReader(pdu), &request.Cmd); err != nil {
		return nil, err
	}
	cmdID := request.Cmd.CommandID

	request.Req = request
	request.DataLength = C.sizeof_struct_nvmf_connect_data
	request.CmdID = cmdID
	request.queue = queue

	if request.Cmd.Opcode != C.nvme_fabrics_command {
		return nil, &ParserError{
			status: C.NVME_SC_INVALID_OPCODE | C.NVME_SC_DNR,
			msg:    "invalid command on unconnected queue",
		}
	}

	if request.Cmd.FcType != C.nvme_fabrics_type_connect {
		return nil, &ParserError{
			status: C.NVME_SC_INVALID_OPCODE | C.NVME_SC_DNR,
			msg:    "invalid command on unconnected queue",
		}
	}
	return request, nil
}

func (queue *nvmeQueue) nvmetRequestInit(transport nvmetTransport, pdu []byte) (Request, error) {
	opcode := pdu[0]
	flags := pdu[1]

	queue.log.Debugf("####### init request ####### opcode: %s(%#02x), flags: %#02x", OpcodeName(opcode), opcode, flags)

	/* no support for fused commands yet */
	if (flags & (C.NVME_CMD_FUSE_FIRST | C.NVME_CMD_FUSE_SECOND)) != 0 {
		status := uint16(C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR)
		request, _ := queue.nvmetParseCommonCmd(pdu)
		request.setTransport(transport)
		queue.completeRequest(request, NewCompletion(request.CommandID(), queue.sq.qID, status))
		queue.log.Errorf("init request failed. opcode: %#02x, flags: %#02x. C.NVME_CMD_FUSE_FIRST(%#02x) | C.NVME_CMD_FUSE_SECOND(%#02x)",
			opcode, flags, C.NVME_CMD_FUSE_FIRST, C.NVME_CMD_FUSE_SECOND)
		return request, fmt.Errorf("no support for fused commands yet")
	}

	/*
	 * For fabrics, PSDT field shall describe metadata pointer (MPTR) that
	 * contains an address of a single contiguous physical buffer that is
	 * byte aligned.
	 */
	if (flags & C.NVME_CMD_SGL_ALL) != C.NVME_CMD_SGL_METABUF {
		status := uint16(C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR)
		request, _ := queue.nvmetParseCommonCmd(pdu)
		request.setTransport(transport)
		queue.completeRequest(request, NewCompletion(request.CommandID(), queue.sq.qID, status))
		queue.log.Errorf("init request failed. opcode: %#02x, flags: %#02x. C.NVME_CMD_SGL_ALL(%#02x), C.NVME_CMD_SGL_METABUF(%#02x)",
			opcode, flags, C.NVME_CMD_SGL_ALL, C.NVME_CMD_SGL_METABUF)
		return request, fmt.Errorf("opcode: %#02x, flags: %#02x. C.NVME_CMD_SGL_ALL(%#02x), C.NVME_CMD_SGL_METABUF(%#02x)", opcode, flags, C.NVME_CMD_SGL_ALL, C.NVME_CMD_SGL_METABUF)
	}

	var status uint16
	var request Request
	var err error
	if queue.sq.ctrl == nil {
		request, err = queue.nvmetParseConnectCmd(pdu)
	} else if opcode == C.nvme_fabrics_command {
		request, err = queue.nvmetParseFabricsCommand(opcode, pdu)
	} else {
		request, err = queue.nvmetParseDiscoveryCommand(opcode, pdu)
	}
	if err != nil {
		var perr *ParserError
		if errors.As(err, &perr) {
			status = perr.status
		}
		queue.log.WithError(err).Errorf("failed to parse command: %v", err)
		request, _ = queue.nvmetParseCommonCmd(pdu)
		request.setTransport(transport)
		queue.completeRequest(request, NewCompletion(request.CommandID(), queue.sq.qID, status))
		return request, err
	}

	request.setTransport(transport)
	return request, nil
}

func (queue *nvmeQueue) nvmetParseCommonCmd(pdu []byte) (Request, error) {
	var request = &CommonRequest{}
	if err := struc.Unpack(bytes.NewReader(pdu), &request.cmd); err != nil {
		return nil, err
	}
	request.Req = request
	request.DataLength = 0
	request.CmdID = request.cmd.CommandID
	request.pdu = pdu
	request.queue = queue
	return request, nil
}

func (queue *nvmeQueue) createNvmeController(request *AdminConnectRequest, connectData *ConnectData) (*nvmeController, error) {
	ctrl, status := newController(uint16(queue.id), queue.controllerID, request, connectData)
	if status != C.NVME_SC_SUCCESS {
		return nil, &ParserError{
			status: status,
			msg:    "failed to create new controller",
		}
	}
	if request.Cmd.SqSize == 0 {
		return nil, &ParserError{
			status: C.NVME_SC_CONNECT_INVALID_PARAM | C.NVME_SC_DNR,
			msg:    "sq size should be greater then zero",
		}
	}
	queue.sq.ctrl = ctrl
	queue.sq.qID = request.Cmd.QID
	queue.cq.qID = request.Cmd.QID
	queue.sq.size = request.Cmd.SqSize
	queue.sq.sqhd = 0
	queue.sq.sqhdDisabled = ((request.Cmd.CatTr & C.NVME_CONNECT_SQ_FC_DISABLED) != 0)
	if queue.sq.sqhdDisabled {
		queue.sq.sqhd = 0xffff
	}

	queue.discoverySubsystem.RegisterController(ctrl)
	queue.keepAliveWatchStopCh = queue.watchKeepAliveExpired()

	return ctrl, nil
}

// a watch will be created when we start the controller and will monitor the KA chan exposed by the controller.
// in case the KA expire we will be notified on that chan and will propogate it to the tcp_queue in order
// for it to close the queue and the controller.
func (queue *nvmeQueue) watchKeepAliveExpired() chan interface{} {
	stopChan := make(chan interface{})
	go func() {
		select {
		case ka := <-queue.sq.ctrl.keepAliveExpiredChan():
			queue.log.Infof("KA expired")
			queue.keepAliveCh <- ka
			return
		case <-stopChan:
			queue.log.Infof("stop watch for KA")
			return
		}
	}()
	return stopChan
}

func (queue *nvmeQueue) destroy() {
	if queue.keepAliveWatchStopCh != nil {
		close(queue.keepAliveWatchStopCh)
		queue.keepAliveWatchStopCh = nil
	}
	if queue.sq.ctrl != nil {
		queue.discoverySubsystem.DeregisterController(queue.sq.ctrl)
		queue.sq.ctrl.delete()
		queue.sq.ctrl = nil
	}
	queue.log.Infof("destroyed nvme queue")
}

func (queue *nvmeQueue) completeRequest(request Request, completion *Completion) {
	request.SetCompletion(completion)
	transport := request.transport()
	if transport == nil {
		logrus.Errorf("transport is nil")
	}
	transport.queueResponse(request)
}

func (queue *nvmeQueue) inlineSize() uint32 {
	return 16 * 4096
}

func (queue *nvmeQueue) hasKeyedSgls() bool {
	return false
}
