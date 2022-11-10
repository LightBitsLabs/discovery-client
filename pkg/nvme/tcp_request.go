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

//#include <linux/nvme-tcp.h>
import "C"

type TCPRequest interface {
	SetNvmeRequest(nvmeRequest Request)
	NvmeRequest() Request
	HasInlineData() bool
	HasDataIn() bool
	NeedDataIn() bool
	SetPDULength(length uint32)
	GetPDULength() uint32
}

type tcpRequest struct {
	nvmeRequest Request
	pduLen      uint32
}

func NewTCPRequest() TCPRequest {
	return &tcpRequest{}
}

func (request *tcpRequest) SetNvmeRequest(nvmeRequest Request) {
	request.nvmeRequest = nvmeRequest
}

func (request *tcpRequest) NvmeRequest() Request {
	return request.nvmeRequest
}

func (request *tcpRequest) HasInlineData() bool {
	return request.nvmeRequest.isWrite() && request.pduLen > 0
}

func (request *tcpRequest) HasDataIn() bool {
	return request.nvmeRequest.isWrite()
}

func (request *tcpRequest) NeedDataIn() bool {
	if !request.HasDataIn() {
		return false
	}
	if request.nvmeRequest.Completion() == nil || request.nvmeRequest.Completion().Status == C.NVME_SC_SUCCESS {
		return true
	}
	return false
}

func (request *tcpRequest) SetPDULength(length uint32) {
	request.pduLen = length
}

func (request *tcpRequest) GetPDULength() uint32 {
	return request.pduLen
}
