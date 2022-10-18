package nvme

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lunixbochs/struc"
	"github.com/sirupsen/logrus"
)

//#include <linux/nvme.h>
import "C"

type TCPHeaderType struct {
	Type  uint8 `struc:"uint8"`
	Flags uint8 `struc:"uint8"`
	Hlen  uint8 `struc:"uint8"`
	Pdo   uint8 `struc:"uint8"`
	Plen  int   `struc:"uint32,little"`
}

type TCPIcReqPduType struct {
	Pfv      uint16 `struc:"int16,little"`
	Maxr2t   int    `struc:"int32,little"`
	Hpda     byte   `struc:"int8"`
	Digest   byte   `struc:"int8"`
	Reserved []byte `struc:"[112]int8"`
}

type TCPIcrespPdu struct {
	Pfv      uint16 `struc:"int16,little"`
	Cpda     byte   `struc:"int8"`
	Digest   byte   `struc:"int8"`
	Maxdata  int    `struc:"int32,little"`
	Reserved []byte `struc:"[112]int8"`
}

type TCPDataPDU struct {
	CommandID  uint16  `struc:"uint16,little"`
	TTag       uint16  `struc:"uint16,little"`
	DataOffset uint32  `struc:"uint32,little"`
	DataLength uint32  `struc:"uint32,little"`
	Reserved   [4]byte `struc:"[4]int8"`
}

type IDPowerState struct {
	MaxPower        uint16   `struc:"uint16,little"`
	Rsvd2           uint8    `struc:"uint8"`
	Flags           uint8    `struc:"uint8"`
	EntryLat        uint32   `struc:"uint32,little"`
	ExitLat         uint32   `struc:"uint32,little"`
	ReadTput        uint8    `struc:"uint8"`
	ReadLat         uint8    `struc:"uint8"`
	WriteTput       uint8    `struc:"uint8"`
	WriteLat        uint8    `struc:"uint8"`
	IddlePower      uint16   `struc:"uint16,little"`
	IddleScale      uint8    `struc:"uint8"`
	Rsvd19          uint8    `struc:"uint8"`
	AcrivePower     uint16   `struc:"uint16,little"`
	ActiveWorkScale uint8    `struc:"uint8"`
	Rsvd23          [9]uint8 `struc:"[9]uint8"`
}

// type IDPowerStateArray struct {
// 	Len          uint8            `struc:"sizeof=IDPowerState"`
// 	IDPowerState [32]IDPowerState `struc:"[32]IDPowerState"`
// }

type IDCtrl struct {
	VID       uint16     `struc:"uint16,little"`
	SSVID     uint16     `struc:"uint16,little"`
	Sn        [20]uint8  `struc:"[20]uint8"`
	Mn        [40]uint8  `struc:"[40]uint8"`
	Fr        string     `struc:"[8]uint8"`
	Rab       uint8      `struc:"uint8"`
	Ieee      [3]uint8   `struc:"[3]uint8"`
	Cmic      uint8      `struc:"uint8"`
	Mdts      uint8      `struc:"uint8"`
	CntlID    uint16     `struc:"uint16,little"`
	Ver       uint32     `struc:"uint32,little"`
	Rtd3r     uint32     `struc:"uint32,little"`
	Rtd3e     uint32     `struc:"uint32,little"`
	Oaes      uint32     `struc:"uint32,little"`
	CtrAtt    uint32     `struc:"uint32,little"`
	Rsvd100   [156]uint8 `struc:"[156]uint8"`
	Oacs      uint16     `struc:"uint16,little"`
	ACL       uint8      `struc:"uint8"`
	Arel      uint8      `struc:"uint8"`
	Frmw      uint8      `struc:"uint8"`
	Lpa       uint8      `struc:"uint8"`
	Elpe      uint8      `struc:"uint8"`
	Npss      uint8      `struc:"uint8"`
	Avscc     uint8      `struc:"uint8"`
	Apsta     uint8      `struc:"uint8"`
	Wctemp    uint16     `struc:"uint16,little"`
	Cctemp    uint16     `struc:"uint16,little"`
	Mtfa      uint16     `struc:"uint16,little"`
	Hmpre     uint32     `struc:"uint32,little"`
	Hmmin     uint32     `struc:"uint32,little"`
	Tnvmcap   [16]uint8  `struc:"[16]uint8"`
	Unvmcap   [16]uint8  `struc:"[16]uint8"`
	Rpmbs     uint32     `struc:"uint32,little"`
	Edstt     uint16     `struc:"uint16,little"`
	Dsto      uint8      `struc:"uint8"`
	FwUg      uint8      `struc:"uint8"`
	Kas       uint16     `struc:"uint16,little"`
	Hctma     uint16     `struc:"uint16,little"`
	MntMt     uint16     `struc:"uint16,little"`
	MxtMt     uint16     `struc:"uint16,little"`
	Sancap    uint32     `struc:"uint32,little"`
	Hmminds   uint32     `struc:"uint32,little"`
	Hmmaxd    uint16     `struc:"uint16,little"`
	Rsvd338   [4]uint8   `struc:"[4]uint8"`
	Anatt     uint8      `struc:"uint8"`
	AnaCap    uint8      `struc:"uint8"`
	AnaGrpMax uint32     `struc:"uint32,little"`
	AnaGrpID  uint32     `struc:"uint32,little"`
	Rsvd352   [160]uint8 `struc:"[160]uint8"`
	Sqes      uint8      `struc:"uint8"`
	Cqes      uint8      `struc:"uint8"`
	Maxcmd    uint16     `struc:"uint16,little"`
	Nn        uint32     `struc:"uint32,little"`
	Oncs      uint16     `struc:"uint16,little"`
	Fuses     uint16     `struc:"uint16,little"`
	Fna       uint8      `struc:"uint8"`
	Vwc       uint8      `struc:"uint8"`
	Awun      uint16     `struc:"uint16,little"`
	AwUpf     uint16     `struc:"uint16,little"`
	Nvscc     uint8      `struc:"uint8"`
	Nwpc      uint8      `struc:"uint8"`
	Acwu      uint16     `struc:"uint16,little"`
	Rsvd534   [2]uint8   `struc:"[2]uint8"`
	Sgls      uint32     `struc:"uint32,little"`
	Mnan      uint32     `struc:"uint32,little"`
	Rsvd544   [224]uint8 `struc:"[224]uint8"`
	SubNqn    [256]byte  `struc:"[256]uint8"`
	Rsvd1024  [768]uint8 `struc:"[768]uint8"`
	Ioccsz    uint32     `struc:"uint32,little"`
	Iorcsz    uint32     `struc:"uint32,little"`
	IodOff    uint16     `struc:"uint16,little"`
	CtrlAttr  uint8      `struc:"uint8"`
	Msdbd     uint8      `struc:"uint8"`
	Rsvd1804  [244]uint8 `struc:"[244]uint8"`
	// HACK :: i don't know how to serialize an array of 32 IDPowerState so we read it as array of bytes
	//Psd       [32]IDPowerState
	Psd [1024]uint8 `struc:"[1024]uint8"`
	VS  [1024]uint8 `struc:"[1024]uint8"`
}

func OpcodeName(opcode uint8) string {
	var name string
	switch opcode {
	case C.nvme_admin_identify:
		name = "nvme_admin_identify"
	case C.nvme_admin_get_log_page:
		name = "nvme_admin_get_log_page"
	case C.nvme_admin_keep_alive:
		name = "nvme_admin_keep_alive"
	case C.nvme_admin_set_features:
		name = "nvme_admin_set_features"
	case C.nvme_admin_get_features:
		name = "nvme_admin_get_features"
	case C.nvme_admin_async_event:
		name = "nvme_admin_async_event"
	case C.nvme_fabrics_type_property_get:
		name = "nvme_fabrics_type_property_get"
	case C.nvme_fabrics_type_property_set:
		name = "nvme_fabrics_type_property_set"
	case C.nvme_fabrics_command:
		name = "nvme_fabrics_command"
	default:
		name = "UNKNOWN"
	}
	return name
}

// Port describes nvme port
type Port struct {
	TrAddr  string
	TrsvcID string
	TrType  uint8
	AdrFam  uint8
	Treq    uint8
	ID      uint16
	Tsas    [C.NVMF_TSAS_SIZE]byte
}

//////////////////////////////////////////////////

type Request interface {
	execute()
	Completion() *Completion
	SetCompletion(cqe *Completion)
	CommandID() uint16
	dataLen() uint32
	transport() nvmetTransport
	setTransport(transport nvmetTransport)
	dptr() *DataPtr
	isWrite() bool
	SetData(sgl *ScatterList)
	GetData() *ScatterList
	PackCmd(buffer *bufio.Writer) error
	String() string
}

//////////////////////////////////////////////////

type AbstractRequest struct {
	Req            Request
	Resp           *Completion
	queue          *nvmeQueue
	CmdID          uint16
	DataLength     uint32
	queueTransport nvmetTransport
	sgl            *ScatterList
}

func (request *AbstractRequest) Completion() *Completion {
	return request.Resp
}

func (request *AbstractRequest) SetCompletion(cqe *Completion) {
	request.Resp = cqe
}

func (request *AbstractRequest) CommandID() uint16 {
	return request.CmdID
}

func (request *AbstractRequest) dataLen() uint32 {
	return request.DataLength
}

func (request *AbstractRequest) setTransport(transport nvmetTransport) {
	request.queueTransport = transport
}

func (request *AbstractRequest) transport() nvmetTransport {
	return request.queueTransport
}

func (request *AbstractRequest) SetData(sgl *ScatterList) {
	request.sgl = sgl
}

func (request *AbstractRequest) GetData() *ScatterList {
	return request.sgl
}

//////////////////////////////////////////////////

type AdminConnectRequest struct {
	AbstractRequest
	Cmd ConnectCommand
}

func NewAdminConnectRequest(cmdID uint16, kato time.Duration, connectData *ConnectData) *AdminConnectRequest {
	connect := &AdminConnectRequest{
		AbstractRequest: AbstractRequest{
			CmdID:      cmdID,
			DataLength: C.sizeof_struct_nvmf_connect_data,
		},
		Cmd: ConnectCommand{
			Opcode:    C.nvme_fabrics_command,
			FcType:    C.nvme_fabrics_type_connect,
			Resv1:     uint8(0x40),
			RecFmt:    0,
			Kato:      uint32(kato.Milliseconds()),
			SqSize:    C.NVME_AQ_DEPTH - 1,
			CommandID: cmdID,
		},
	}
	connect.sgl = NewScatterList(C.sizeof_struct_nvmf_connect_data, 1024)
	struc.Pack(NewScatterListWriter(connect.sgl), connectData)
	connect.dptr().SetSgInline(connect.DataLength)
	return connect
}

func (request *AdminConnectRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q, fcType: %#02x, kato: %d",
		reflect.TypeOf(request).String(), request.Cmd.CommandID,
		OpcodeName(request.Cmd.Opcode), request.Cmd.Opcode, status,
		request.Cmd.FcType, request.Cmd.Kato)
}

func (request *AdminConnectRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.Cmd)
}

func (request *AdminConnectRequest) dptr() *DataPtr {
	return &request.Cmd.Dptr
}

func (request *AdminConnectRequest) isWrite() bool {
	return true
}

func (request *AdminConnectRequest) execute() {
	var connectData = &ConnectData{}

	if err := struc.Unpack(NewScatterListReader(request.sgl), connectData); err != nil {
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_CONNECT_INVALID_PARAM|C.NVME_SC_DNR)
		completion.Result.setU32Result(1)
		request.queue.completeRequest(request, completion)
		return
	}
	// trim trailing zeroes
	connectData.SubsysNqn = strings.TrimRight(connectData.SubsysNqn, "\x00")
	if connectData.SubsysNqn != DiscoverySubsysName {
		logrus.Errorf("SubsysNqn(%s) != %s. failing...", connectData.SubsysNqn, DiscoverySubsysName)
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_CONNECT_INVALID_PARAM|C.NVME_SC_DNR)
		completion.Result.setU32Result(1)
		request.queue.completeRequest(request, completion)
		return
	}

	connectData.HostNqn = strings.TrimRight(connectData.HostNqn, "\x00")
	// convent string to uuid
	_, err := uuid.FromBytes([]byte(connectData.HostID))
	if err != nil {
		logrus.WithError(err).Errorf("hostid should be a valid uuid, got: %v", connectData.HostID)
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_CONNECT_INVALID_PARAM|C.NVME_SC_DNR)
		completion.Result.setU32Result(1)
		request.queue.completeRequest(request, completion)
		return
	}

	if request.Cmd.RecFmt != 0 {
		logrus.Warnf("invalid connect version (%d)", request.Cmd.RecFmt)
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_CONNECT_FORMAT|C.NVME_SC_DNR)
		request.queue.completeRequest(request, completion)
		return
	}

	if connectData.CntlID != 0xffff {
		logrus.Warnf("connect attempt for invalid controller ID %#x\n", connectData.CntlID)
		// TODO: sashas set result ... as in kernel with offsets in struct
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_CONNECT_INVALID_PARAM|C.NVME_SC_DNR)
		completion.Result.setU32Result(1)
		request.queue.completeRequest(request, completion)
		return
	}

	ctrl, err := request.queue.createNvmeController(request, connectData)
	if err != nil {
		var perr *ParserError
		if errors.As(err, &perr) {
			request.queue.completeRequest(request, NewCompletion(request.CommandID(), request.queue.sq.qID, perr.status))
		} else {
			request.queue.completeRequest(request, NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_CONNECT_INVALID_PARAM|C.NVME_SC_DNR))
		}
		return
	}
	completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
	completion.Result.setU16Result(ctrl.ControllerID())
	request.queue.completeRequest(request, completion)
}

//////////////////////////////////////////////////

type CompletionResult struct {
	Result [8]uint8 `struc:"[8]uint8"`
}

func (cqe *CompletionResult) setU32Result(result uint32) {
	binary.LittleEndian.PutUint32(cqe.Result[:], result)
}

func (cqe *CompletionResult) setU16Result(result uint16) {
	binary.LittleEndian.PutUint16(cqe.Result[:2], result)
}

func (cqe *CompletionResult) setU64Result(result uint64) {
	binary.LittleEndian.PutUint64(cqe.Result[:], result)
}

////////////////////////////////////////////////////

type Completion struct {
	Result    CompletionResult
	SqHead    uint16 `struc:"uint16,little"`
	SqID      uint16 `struc:"uint16,little"`
	CommandID uint16 `struc:"uint16,little"`
	Status    uint16 `struc:"uint16,little"`
}

func NewCompletion(commandID uint16, sqID uint16, status uint16) *Completion {
	c := &Completion{
		CommandID: commandID,
		Status:    status << 1,
		SqID:      sqID,
	}
	return c
}

////////////////////////////////////////////////////

// https://nvmexpress.org/wp-content/uploads/NVM-Express-1_4-2019.06.10-Ratified.pdf
// Figure 105: Command Format – Admin and NVM Command Set
type CommonCommand struct {
	Opcode    uint8     `struc:"uint8"`
	Flags     uint8     `struc:"uint8"`
	CommandID uint16    `struc:"uint16,little"`
	NSId      uint32    `struc:"uint32,little"`
	Cdw2      [2]uint32 `struc:"[2]uint32,little"`
	Metadata  uint64    `struc:"uint64,little"`
	Dptr      DataPtr
	// CDW10 command specific Dword 10.
	Cdw10 uint32 `struc:"uint32,little"`
	// CDW11 command specific Dword 11.
	Cdw11 uint32 `struc:"uint32,little"`
	// CDW12 command specific Dword 12.
	Cdw12 uint32 `struc:"uint32,little"`
	// CDW13 command specific Dword 13.
	Cdw13 uint32 `struc:"uint32,little"`
	// CDW14 command specific Dword 14.
	Cdw14 uint32 `struc:"uint32,little"`
	// CDW15 command specific Dword 15.
	Cdw15 uint32 `struc:"uint32,little"`
}

func (cmd *CommonCommand) String() string {
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#02x). nsid: %d",
		reflect.TypeOf(cmd).String(), cmd.CommandID,
		OpcodeName(cmd.Opcode), cmd.Opcode, cmd.NSId)
}

//////////////////////////////////////////////////////

type FeaturesRequest struct {
	AbstractRequest
	cmd CommonCommand
}

func NewSetFeatureAsyncEventRequest(cmdID uint16) *FeaturesRequest {
	request := &FeaturesRequest{
		AbstractRequest: AbstractRequest{
			CmdID: cmdID,
		},
		cmd: CommonCommand{
			Opcode:    C.nvme_admin_set_features,
			Flags:     REQ_FAILFAST_DRIVER,
			CommandID: cmdID,
			Cdw10:     C.NVME_FEAT_ASYNC_EVENT,
			Cdw11:     0x80000000,
		},
	}
	return request
}

func NewSetFeatureKatoRequest(cmdID uint16, kato time.Duration) *FeaturesRequest {
	request := &FeaturesRequest{
		AbstractRequest: AbstractRequest{
			CmdID: cmdID,
		},
		cmd: CommonCommand{
			Opcode:    C.nvme_admin_set_features,
			Flags:     REQ_FAILFAST_DRIVER,
			CommandID: cmdID,
			Cdw10:     C.NVME_FEAT_KATO,
			Cdw11:     uint32(kato.Milliseconds()),
		},
	}
	return request
}

func (request *FeaturesRequest) execute() {

}

func (request *FeaturesRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q, cdw10: %#02x, cdw11: %#02x, cdw12: %#02x, cdw13: %#02x",
		reflect.TypeOf(request).String(), request.cmd.CommandID,
		OpcodeName(request.cmd.Opcode), request.cmd.Opcode, status,
		request.cmd.Cdw10, request.cmd.Cdw11, request.cmd.Cdw12, request.cmd.Cdw13)
}

func (request *FeaturesRequest) isWrite() bool {
	return false
}

func (request *FeaturesRequest) dptr() *DataPtr {
	return nil
}

func (request *FeaturesRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.cmd)
}

//////////////////////////////////////////////////////

type KeepAliveRequest struct {
	AbstractRequest
	Cmd CommonCommand
}

func NewKeepAliveRequest(cmdID uint16) *KeepAliveRequest {
	request := &KeepAliveRequest{
		AbstractRequest: AbstractRequest{},
		Cmd: CommonCommand{
			Opcode:    C.nvme_admin_keep_alive,
			Flags:     REQ_FAILFAST_DRIVER,
			CommandID: cmdID,
		},
	}
	return request
}

func (request *KeepAliveRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}

	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q",
		reflect.TypeOf(request).String(), request.Cmd.CommandID,
		OpcodeName(request.Cmd.Opcode), request.Cmd.Opcode, status)
}

func (request *KeepAliveRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.Cmd)
}

func (request *KeepAliveRequest) isWrite() bool {
	return false
}

func (request *KeepAliveRequest) dptr() *DataPtr {
	return nil
}

func (request *KeepAliveRequest) execute() {
	request.queue.sq.ctrl.resetKatoTimer()
	completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
	request.queue.completeRequest(request, completion)
}

//////////////////////////////////////////////////////

type CommonRequest struct {
	AbstractRequest
	cmd CommonCommand
	pdu []byte
}

func (request *CommonRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}

	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q",
		reflect.TypeOf(request).String(), request.cmd.CommandID,
		OpcodeName(request.cmd.Opcode), request.cmd.Opcode, status)
}

func (request *CommonRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.cmd)
}

func (request *CommonRequest) execute() {
	logrus.Panicf("Cannot execute common command")
}

func (request *CommonRequest) dptr() *DataPtr {
	return &request.cmd.Dptr
}

func (request *CommonRequest) isWrite() bool {
	if request.cmd.Opcode == C.nvme_fabrics_command {
		fctype := request.pdu[4]
		return (fctype & 1) != 0
	}
	return (request.cmd.Opcode & 1) != 0
}

//////////////////////////////////////////////////////

type AsyncEventRequest struct {
	AbstractRequest
	cmd CommonCommand
}

func NewAsyncEventRequest(cmdID uint16) *AsyncEventRequest {
	request := &AsyncEventRequest{
		AbstractRequest: AbstractRequest{
			CmdID: cmdID,
		},
		cmd: CommonCommand{
			Opcode:    C.nvme_admin_async_event,
			Flags:     REQ_FAILFAST_DRIVER,
			CommandID: cmdID,
		},
	}
	return request
}

func (request *AsyncEventRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}

	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q",
		reflect.TypeOf(request).String(), request.cmd.CommandID,
		OpcodeName(request.cmd.Opcode), request.cmd.Opcode, status)
}

func (request *AsyncEventRequest) execute() {
	request.queue.sq.ctrl.registerAsyncEventRequest(request)
}

func (request *AsyncEventRequest) dptr() *DataPtr {
	return &request.cmd.Dptr
}

func (request *AsyncEventRequest) isWrite() bool {
	return false
}

func (request *AsyncEventRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.cmd)
}

//////////////////////////////////////////////////////

type DataPtr struct {
	Part1 uint64   `struc:"uint64,little"`
	Part2 [8]uint8 `struc:"[8]uint8"`
}

func (dataPtr *DataPtr) sgl() *C.struct_nvme_sgl_desc {
	len := C.uint(binary.LittleEndian.Uint32(dataPtr.Part2[:4]))
	sglType := C.uchar(dataPtr.Part2[7])
	return &C.struct_nvme_sgl_desc{addr: C.ulonglong(dataPtr.Part1), length: len, _type: sglType}
}

func (dataPtr *DataPtr) SetSgHostData(length uint32) {
	// usually length eq 16
	dataPtr.Part1 = uint64(0)
	binary.LittleEndian.PutUint32(dataPtr.Part2[:4], uint32(length))
	dataPtr.Part2[7] = (C.NVME_TRANSPORT_SGL_DATA_DESC << 4) | C.NVME_SGL_FMT_TRANSPORT_A

}

func (dataPtr *DataPtr) SetSgInline(length uint32) {
	// usually length eq 1024
	dataPtr.Part1 = uint64(0)
	binary.LittleEndian.PutUint32(dataPtr.Part2[:4], uint32(length))
	dataPtr.Part2[7] = (C.NVME_SGL_FMT_DATA_DESC << 4) | C.NVME_SGL_FMT_OFFSET
}
