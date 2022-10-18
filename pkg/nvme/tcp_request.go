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
