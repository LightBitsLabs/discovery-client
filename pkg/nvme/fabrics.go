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
	"bufio"
	"bytes"
	"fmt"
	"reflect"

	"github.com/lunixbochs/struc"
)

//#include <linux/nvme-tcp.h>
import "C"

type PropertySetCommand struct {
	Opcode    uint8     `struc:"uint8"`
	Resv1     uint8     `struc:"uint8"`
	CommandID uint16    `struc:"uint16,little"`
	FcType    uint8     `struc:"uint8"`
	Rsvd2     [35]uint8 `struc:"[35]uint8"`
	Attrib    uint8     `struc:"uint8"`
	Rsvd3     [3]uint8  `struc:"[3]uint8"`
	Offset    uint32    `struc:"uint32,little"`
	Value     uint64    `struc:"uint64,little"`
	Rsvd4     [8]uint8  `struc:"[8]uint8"`
}

type PropertyGetCommand struct {
	Opcode    uint8     `struc:"uint8"`
	Resv1     uint8     `struc:"uint8"`
	CommandID uint16    `struc:"uint16,little"`
	FcType    uint8     `struc:"uint8"`
	Rsvd2     [35]uint8 `struc:"[35]uint8"`
	Attrib    uint8     `struc:"uint8"`
	Rsvd3     [3]uint8  `struc:"[3]uint8"`
	Offset    uint32    `struc:"uint32,little"`
	Rsvd4     [16]uint8 `struc:"[16]uint8"`
}

// Rsvd2[34] 90 == 0x5a
// att 1
// Rsvd3 [0,0,0] [3]uint8
type NvmefPropertySetRequest struct {
	AbstractRequest
	Cmd PropertySetCommand
}

func NewPropertySetRequest(cmdID uint16, off uint32, val uint64) *NvmefPropertySetRequest {
	request := &NvmefPropertySetRequest{
		AbstractRequest: AbstractRequest{
			CmdID:      cmdID,
			DataLength: C.sizeof_struct_nvmf_property_set_command,
		},
		Cmd: PropertySetCommand{
			Opcode:    C.nvme_fabrics_command, // 0x7f
			Resv1:     uint8(0x40),
			CommandID: cmdID,
			FcType:    C.nvme_fabrics_type_property_set, // 0x00
			Attrib:    0,
			//Rsvd2: ,
			Offset: off,
			Value:  val,
		},
	}
	request.Cmd.Rsvd2[34] = uint8(90) //0x5a

	return request
}

func (request *NvmefPropertySetRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status)
	}
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q. property - %s(%#04x), Value: %#08x",
		reflect.TypeOf(request).String(), request.Cmd.CommandID,
		OpcodeName(request.Cmd.Opcode), request.Cmd.Opcode, status,
		registerName(request.Cmd.Offset), request.Cmd.Offset, request.Cmd.Value)
}

func (request *NvmefPropertySetRequest) dptr() *DataPtr {
	return nil
}

func (request *NvmefPropertySetRequest) isWrite() bool {
	return false
}

func (request *NvmefPropertySetRequest) execute() {
	var status uint16 = C.NVME_SC_SUCCESS
	if !((request.Cmd.Attrib & 1) != 0) {
		request.queue.log.Infof("cmd id: %#04x, request.cmd.Attrib & 1 offset=%v =? REGCC-%v", request.Cmd.CommandID, request.Cmd.Offset, C.NVME_REG_CC)
		value := request.Cmd.Value

		switch request.Cmd.Offset {
		case C.NVME_REG_CC:
			request.queue.sq.ctrl.updateControllerConfiguration(uint32(value))
		default:
			status = C.NVME_SC_INVALID_FIELD | C.NVME_SC_SUCCESS
			request.queue.log.Errorf("cmd id: %#04x, respond with invalid field. we don't support offset: %d",
				request.Cmd.CommandID, request.Cmd.Offset)
		}
	} else {
		status = C.NVME_SC_INVALID_FIELD | C.NVME_SC_SUCCESS
		request.queue.log.Errorf("cmd id: %#04x, respond with invalid field. support Attrib == 0. got (request.Cmd.Attrib & 1)",
			request.Cmd.CommandID)
	}

	request.queue.completeRequest(request, NewCompletion(request.CommandID(), request.queue.sq.qID, status))
}

func (request *NvmefPropertySetRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.Cmd)
}

type NvmefPropertyGetRequest struct {
	AbstractRequest
	Cmd PropertyGetCommand
}

func NewPropertyGetRequest(cmdID uint16, propOff uint32) *NvmefPropertyGetRequest {
	request := &NvmefPropertyGetRequest{
		AbstractRequest: AbstractRequest{
			CmdID:      cmdID,
			DataLength: C.sizeof_struct_nvmf_property_get_command,
		},
		Cmd: PropertyGetCommand{
			Opcode:    C.nvme_fabrics_command, // 0x7f
			Resv1:     uint8(0x40),
			CommandID: cmdID,
			FcType:    C.nvme_fabrics_type_property_get, // 0x04
			Attrib:    map[bool]uint8{true: 1, false: 0}[propOff == C.NVME_REG_CAP],
			Offset:    propOff,
		},
	}
	request.Cmd.Rsvd2[34] = uint8(90)
	return request
}

func (request *NvmefPropertyGetRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q. property - %s(%#04x)",
		reflect.TypeOf(request).String(), request.Cmd.CommandID,
		OpcodeName(request.Cmd.Opcode), request.Cmd.Opcode, status,
		registerName(request.Cmd.Offset), request.Cmd.Offset)
}

func (request *NvmefPropertyGetRequest) execute() {
	ctrl := request.queue.sq.ctrl
	var value uint64
	var status uint16 = C.NVME_SC_SUCCESS

	if (request.Cmd.Attrib & 1) != 0 {
		if request.Cmd.Offset == C.NVME_REG_CAP {
			value = ctrl.cap
			goto done
		}
		status = C.NVME_SC_INVALID_FIELD | C.NVME_SC_SUCCESS
	} else {

		switch request.Cmd.Offset {
		case C.NVME_REG_VS:
			value = uint64(nvmeVS(1, 3, 0))
		case C.NVME_REG_CC:
			value = uint64(ctrl.cc)
		case C.NVME_REG_CSTS:
			value = uint64(ctrl.csts)
		default:
			status = C.NVME_SC_INVALID_FIELD | C.NVME_SC_SUCCESS
		}
	}
done:
	completion := NewCompletion(request.CommandID(), request.queue.sq.qID, status)
	completion.Result.setU64Result(value)
	request.queue.completeRequest(request, completion)
}

func (request *NvmefPropertyGetRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.Cmd)
}

func (request *NvmefPropertyGetRequest) dptr() *DataPtr {
	return nil
}

func (request *NvmefPropertyGetRequest) isWrite() bool {
	return false
}

func (queue *nvmeQueue) nvmetParseFabricsCommand(opcode uint8, pdu []byte) (Request, error) {
	fctype := pdu[4]

	queue.log.Debugf("####### parse fabrics cmd ####### %s(%#02x)", OpcodeName(opcode), opcode)

	switch fctype {
	case C.nvme_fabrics_type_property_set:
		request := &NvmefPropertySetRequest{}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.Cmd); err != nil {
			return nil, err
		}
		request.AbstractRequest = AbstractRequest{
			queue: queue,
			Req:   request,
			CmdID: request.Cmd.CommandID,
		}
		return request, nil
	case C.nvme_fabrics_type_property_get:
		request := &NvmefPropertyGetRequest{}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.Cmd); err != nil {
			return nil, err
		}
		request.AbstractRequest = AbstractRequest{
			queue: queue,
			Req:   request,
			CmdID: request.Cmd.CommandID,
		}
		return request, nil
	default:
		queue.log.Errorf("received unknown capsule type 0x%x\n", fctype)
		_, _ = queue.nvmetParseCommonCmd(pdu)
		return nil, &ParserError{
			status: C.NVME_SC_INVALID_OPCODE | C.NVME_SC_DNR,
			msg:    fmt.Sprintf("received unknown capsule type 0x%x", fctype),
		}
	}
}

func registerName(reg uint32) string {
	switch reg {
	case C.NVME_REG_CAP:
		return "ControllerCapabilities"
	case C.NVME_REG_VS:
		return "ControllerVersion"
	case C.NVME_REG_INTMS:
		return "Interrupt Mask Set"
	case C.NVME_REG_INTMC:
		return "Interrupt Mask Clear"
	case C.NVME_REG_CC:
		return "ControllerConfiguration"
	case C.NVME_REG_CSTS:
		return "ControllerStatus"
	case C.NVME_REG_NSSR:
		return "NVM Subsystem Reset"
	case C.NVME_REG_AQA:
		return "Admin Queue Attributes"
	case C.NVME_REG_ASQ:
		return "Admin SQ Base Address"
	case C.NVME_REG_ACQ:
		return "Admin CQ Base Address"
	case C.NVME_REG_CMBLOC:
		return "Controller Memory Buffer Location"
	case C.NVME_REG_CMBSZ:
		return "Controller Memory Buffer Size"
	case C.NVME_REG_DBS:
		return "SQ 0 Tail Doorbell"
	default:
		return "UNKNOWN register name"
	}
}
