package nvme

//#include <linux/nvme-tcp.h>
import "C"
import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"time"

	"github.com/lunixbochs/struc"
	"github.com/sirupsen/logrus"
)

const (
	// DiscoverySubsysName name of discovery subsystem
	DiscoverySubsysName string = "nqn.2014-08.org.nvmexpress.discovery"
	NVME_NO_LOG_LSP            = 0x0
)

// SubsystemType (SUBTYPE): Specifies the type of the NVM subsystem that is indicated in this entry.
type SubsystemType uint8

const (
	// NVME_NQN_NVME - The entry describes a referral to another Discovery Service composed of
	// Discovery controllers for additional records.
	NVME_NQN_NVME SubsystemType = C.NVME_NQN_NVME
	// NVME_NQN_DISC - The entry describes an NVM subsystem that is not associated with
	// Discovery controllers and whose controllers may have attached
	// namespaces.
	NVME_NQN_DISC SubsystemType = C.NVME_NQN_DISC
)

// DiscoverySubsystem ...
type DiscoverySubsystem interface {
	RegisterController(controller Controller)
	DeregisterController(controller Controller)
	FillDiscoveryLogPage(offset uint64, maxEntries uint32, hostNqn string) ([]*NvmefDiscRspPageEntry, uint32, uint64)
}

type IdentifyCommand struct {
	Opcode    uint8     `struc:"uint8"`
	Flags     uint8     `struc:"uint8"`
	CommandID uint16    `struc:"uint16,little"`
	NSId      uint32    `struc:"uint32,little"`
	Rsvd2     [2]uint64 `struc:"[2]uint64"`
	Dptr      DataPtr
	Cns       uint8     `struc:"uint8"`
	Rsvd3     uint8     `struc:"uint8"`
	CtrlID    uint16    `struc:"uint16,little"`
	Rsvd11    [5]uint16 `struc:"[5]uint32"`
}

func (cmd *IdentifyCommand) String() string {
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#02x). nsid: %d",
		reflect.TypeOf(cmd).String(), cmd.CommandID, OpcodeName(cmd.Opcode), cmd.Opcode, cmd.NSId)
}

type GetLogPageCommand struct {
	Opcode    uint8     `struc:"uint8"`
	Flags     uint8     `struc:"uint8"`
	CommandID uint16    `struc:"uint16,little"`
	NSId      uint32    `struc:"uint32,little"`
	Rsvd2     [2]uint64 `struc:"[2]uint64"`
	Dptr      DataPtr
	Lid       uint8     `struc:"uint8"`
	Lsp       uint8     `struc:"uint8"`
	NumDl     uint16    `struc:"uint16,little"`
	NumDu     uint16    `struc:"uint16,little"`
	Rsvd11    uint16    `struc:"uint16,little"`
	Lpol      uint32    `struc:"uint32,little"`
	Lpou      uint32    `struc:"uint32,little"`
	Rsvf14    [2]uint32 `struc:"[2]uint32"`
}

func (cmd *GetLogPageCommand) String() string {
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#02x). nsid: %d",
		reflect.TypeOf(cmd).String(), cmd.CommandID, OpcodeName(cmd.Opcode), cmd.Opcode, cmd.NSId)
}

func NewNvmeGetDiscoveryLogPageRequest(cmdid uint16, size uint32, offset uint64, nsid uint32) *NvmeGetDiscoveryLogPageRequest {
	dwlen := nvmeBytesToNumd(size)

	req := &NvmeGetDiscoveryLogPageRequest{
		AbstractRequest: AbstractRequest{
			CmdID:      cmdid,
			DataLength: 0,
		},
		Cmd: GetLogPageCommand{
			Opcode:    C.nvme_admin_get_log_page,
			Flags:     REQ_FAILFAST_DRIVER,
			CommandID: cmdid,
			NSId:      nsid,
			Dptr:      DataPtr{Part1: 0, Part2: [8]uint8{16, 0, 0, 0, 0, 0, 0, 90}},
			Lid:       C.NVME_LOG_DISC,
			Lsp:       NVME_NO_LOG_LSP,
			Lpol:      lower32Bits(offset),
			Lpou:      upper32Bits(offset),
			NumDl:     uint16(dwlen & ((1 << 16) - 1)),
			NumDu:     uint16(dwlen >> 16),
		},
	}
	return req
}

// Convert byte length to nvme's 0-based num dwords
func nvmeBytesToNumd(len uint32) uint32 {
	return (len >> 2) - 1
}

/**
 * upper32Bits - return bits 32-63 of a number
 * @n: the number we're accessing
 *
 * A basic shift-right of a 64- or 32-bit quantity.  Use this to suppress
 * the "right shift count >= width of type" warning when that quantity is
 * 32-bits.
 */
func upper32Bits(n uint64) uint32 {
	return ((uint32)(((n) >> 16) >> 16))
}

// lower32Bits - return bits 0-31 of a number
func lower32Bits(n uint64) uint32 {
	return ((uint32)(n))
}

func (cmd *GetLogPageCommand) GetLogPageLen() uint32 {
	len := uint32(cmd.NumDu)
	len <<= 16
	len += uint32(cmd.NumDl)
	/* NUMD is a 0's based value */
	len++
	// sizeof(int32)
	len *= 4
	return len
}

func (cmd *GetLogPageCommand) GetLogPageOffset() uint64 {
	len := uint64(cmd.Lpou)
	len <<= 32
	len += uint64(cmd.Lpol)
	return len
}

type NvmeIdentifyDiscoveryRequest struct {
	AbstractRequest
	Cmd IdentifyCommand
}

const REQ_FAILFAST_DRIVER = 0x40

func NewIdentifyRequest(cmdid uint16) *NvmeIdentifyDiscoveryRequest {
	req := &NvmeIdentifyDiscoveryRequest{
		AbstractRequest: AbstractRequest{
			CmdID: cmdid,
			//DataLength: C.sizeof_struct_nvme_identify,
		},
		Cmd: IdentifyCommand{
			Opcode:    C.nvme_admin_identify,
			CommandID: cmdid,
			NSId:      0,
			Cns:       C.NVME_ID_CNS_CTRL,
			Flags:     REQ_FAILFAST_DRIVER,
			Rsvd3:     0x0,
			CtrlID:    uint16(0),
		},
	}
	return req
}

func (request *NvmeIdentifyDiscoveryRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q",
		reflect.TypeOf(request).String(), request.Cmd.CommandID,
		OpcodeName(request.Cmd.Opcode), request.Cmd.Opcode,
		status)
}

func (request *NvmeIdentifyDiscoveryRequest) isWrite() bool {
	return false
}

func (request *NvmeIdentifyDiscoveryRequest) CommandID() uint16 {
	return request.Cmd.CommandID
}

func (request *NvmeIdentifyDiscoveryRequest) dptr() *DataPtr {
	return &request.Cmd.Dptr
}

func (request *NvmeIdentifyDiscoveryRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.Cmd)
}

type DiscRspPageHdr struct {
	GenCtr uint64      `struc:"uint64,little"`
	NumRec uint64      `struc:"uint64,little"`
	RecFmt uint16      `struc:"uint16,little"`
	Resv14 [1006]uint8 `struc:"[1006]uint8"`
}

type NvmefDiscRspPageEntry struct {
	TrType  uint8                      `struc:"uint8"`
	AdrFam  uint8                      `struc:"uint8"`
	SubType SubsystemType              `struc:"uint8"`
	Treq    uint8                      `struc:"uint8"`
	PortID  uint16                     `struc:"uint16,little"`
	CntlID  uint16                     `struc:"uint16,little"`
	Asqsz   uint16                     `struc:"uint16,little"`
	Resv8   [22]uint8                  `struc:"[22]uint8"`
	TrsvcID [C.NVMF_TRSVCID_SIZE]uint8 `struc:"[32]uint8"`
	Resv64  [192]uint8                 `struc:"[192]uint8"`
	Subnqn  string                     `struc:"[256]uint8"`
	Traddr  string                     `struc:"[256]uint8"`
	Tsas    [256]uint8                 `struc:"[256]uint8"`
}

const (
	nvmeAENBitDiscChange = uint32(31)
	nvmeAENCfgDiscChange = uint32(1) << nvmeAENBitDiscChange
	nvmeAENCfgOptional   = nvmeAENCfgDiscChange
)

type nvmeDiscoverySetFeaturesRequest struct {
	FeaturesRequest
}

func (request *nvmeDiscoverySetFeaturesRequest) execute() {
	switch request.cmd.Cdw10 & 0xff {
	case C.NVME_FEAT_KATO:
		request.nvmetSetFeaturesKato()
	case C.NVME_FEAT_ASYNC_EVENT:
		request.nvmetSetFeaturesAsyncEvent(nvmeAENCfgDiscChange)
	default:
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_INVALID_FIELD|C.NVME_SC_DNR)
		logrus.Errorf("request %s failed. status %#02x", request.String(), completion.Status)
		request.queue.completeRequest(request, completion)
	}
}

func (request *nvmeDiscoverySetFeaturesRequest) nvmetSetFeaturesKato() {
	katoMsec := request.cmd.Cdw11
	// kato given in millisec and we convert it to time.Duration
	kato := time.Duration(katoMsec) * time.Millisecond
	request.queue.sq.ctrl.setKato(kato)

	completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
	completion.Result.setU32Result(uint32(request.queue.sq.ctrl.kato.Seconds()))
	request.queue.completeRequest(request, completion)
}

func (request *nvmeDiscoverySetFeaturesRequest) nvmetSetFeaturesAsyncEvent(mask uint32) {
	value := request.cmd.Cdw11

	// this means that the mask is not describing the AEN bit which is the 31 bit
	if (value & (^mask)) != 0 {
		request.queue.completeRequest(request, NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_INVALID_FIELD|C.NVME_SC_DNR))
		return
	}

	request.queue.sq.ctrl.setAENValue(value)

	completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
	completion.Result.setU32Result(value)
	request.queue.completeRequest(request, completion)
}

func (request *nvmeDiscoverySetFeaturesRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.cmd)
}

type nvmeDiscoveryGetFeaturesRequest struct {
	FeaturesRequest
}

func (request *nvmeDiscoveryGetFeaturesRequest) execute() {
	ctrl := request.queue.sq.ctrl

	// first byte of cdw10 indicate the type of feature
	switch request.cmd.Cdw10 & 0xff {
	case C.NVME_FEAT_KATO:
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
		completion.Result.setU32Result(uint32(ctrl.kato.Milliseconds()))
		request.queue.completeRequest(request, completion)
	case C.NVME_FEAT_ASYNC_EVENT:
		var aenBitValue uint32 = 0
		if ctrl.aenBitEnabled {
			aenBitValue = 1 << nvmeAENBitDiscChange
		}
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
		completion.Result.setU32Result(aenBitValue)
		request.queue.completeRequest(request, completion)
	default:
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_INVALID_FIELD|C.NVME_SC_DNR)
		logrus.Errorf("request %s failed. status %#02x", request.String(), completion.Status)
		request.queue.completeRequest(request, completion)
	}
}

const (
	nvmetMaxCmd = 1024
)

// https://nvmexpress.org/wp-content/uploads/NVM-Express-1_4-2019.06.10-Ratified.pdf
// Figure 247: Identify – Identify Controller Data Structure
func (request *NvmeIdentifyDiscoveryRequest) execute() {
	var status uint16 = C.NVME_SC_SUCCESS

	id := &IDCtrl{}

	// TODO set this to actual
	id.Fr = "12345678"

	/* no limit on data transfer sizes for now */
	id.Mdts = 0
	id.CntlID = request.queue.sq.ctrl.ControllerID()

	id.Ver = uint32(nvmeVS(1, 3, 0))
	id.Lpa = (1 << 2)
	id.Oaes = nvmeAENCfgOptional

	id.Maxcmd = nvmetMaxCmd

	id.Sgls = 1 << 0

	if request.queue.hasKeyedSgls() {
		id.Sgls |= 1 << 2
	}

	if request.queue.inlineSize() > 0 {
		id.Sgls |= 1 << 20
	}
	copy(id.SubNqn[:], DiscoverySubsysName)

	err := struc.Pack(NewScatterListWriter(request.GetData()), id)
	if err != nil {
		status = C.NVME_SC_SGL_INVALID_DATA | C.NVME_SC_DNR
	}
	completion := NewCompletion(request.CommandID(), request.queue.sq.qID, status)
	request.queue.completeRequest(request, completion)
}

func getLogPageName(logID uint8) string {
	var logPageName string
	switch logID {
	case C.NVME_LOG_ERROR:
		logPageName = "Error Information"
	case C.NVME_LOG_SMART:
		logPageName = "SMART / Health Information"
	case C.NVME_LOG_FW_SLOT:
		logPageName = "Firmware Slot Information"
	case C.NVME_LOG_CHANGED_NS:
		logPageName = "Changed Namespace List"
	case C.NVME_LOG_CMD_EFFECTS:
		logPageName = "Commands Supported and Effects"
	case C.NVME_LOG_ANA:
		logPageName = "Asymmetric Namespace Access"
	case C.NVME_LOG_RESERVATION:
		logPageName = "I/O Command Set Specific"
	case C.NVME_LOG_DISC:
		logPageName = "Discovery"
	default:
		return "UNKNOWN"
	}
	return logPageName
}

func (queue *nvmeQueue) nvmetParseDiscoveryCommand(opcode uint8, pdu []byte) (Request, error) {
	ctrl := queue.sq.ctrl

	queue.log.Debugf("####### parse discovery cmd ####### %s(%#02x)", OpcodeName(opcode), opcode)

	if (ctrl.csts & C.NVME_CSTS_RDY) == 0 {
		return nil, &ParserError{
			status: C.NVME_SC_INVALID_OPCODE | C.NVME_SC_DNR,
			msg:    fmt.Sprintf("got cmd %d while CSTS.RDY == 0 on qid = %d", opcode, queue.sq.qID),
		}
	}
	switch opcode {
	case C.nvme_admin_identify:
		request := &NvmeIdentifyDiscoveryRequest{}

		if err := struc.Unpack(bytes.NewReader(pdu), &request.Cmd); err != nil {
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("failed to parse command. command: %s", request.Cmd.String()),
			}
		}
		request.AbstractRequest = AbstractRequest{
			queue:      queue,
			Req:        request,
			CmdID:      request.Cmd.CommandID,
			DataLength: C.NVME_IDENTIFY_DATA_SIZE,
		}
		return request, nil
	case C.nvme_admin_get_log_page:
		var request = &NvmeGetDiscoveryLogPageRequest{}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.Cmd); err != nil {
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("failed to parse command. command: %s", request.Cmd.String()),
			}
		}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.commonCmd); err != nil {
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("failed to parse command. command: %s", request.commonCmd.String()),
			}
		}
		request.AbstractRequest = AbstractRequest{
			queue:      queue,
			Req:        request,
			CmdID:      request.Cmd.CommandID,
			DataLength: request.Cmd.GetLogPageLen(),
		}
		// we support only 0x70 - log page discovery
		if request.Cmd.Lid != C.NVME_LOG_DISC {
			logPageName := getLogPageName(request.Cmd.Lid)
			logrus.Errorf("unsupported get_log_page lid: 0x%x, log name: %s", request.Cmd.Lid, logPageName)
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_LOG_PAGE | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("unsupported get_log_page lid: 0x%x, log name: %s", request.Cmd.Lid, logPageName),
			}
		}
		return request, nil
	case C.nvme_admin_keep_alive:
		var request = &KeepAliveRequest{}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.Cmd); err != nil {
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("failed to parse command. command: %s", request.Cmd.String()),
			}
		}
		request.AbstractRequest = AbstractRequest{
			queue: queue,
			Req:   request,
			CmdID: request.Cmd.CommandID,
		}
		return request, nil
	case C.nvme_admin_set_features:
		var request = &nvmeDiscoverySetFeaturesRequest{}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.cmd); err != nil {
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("failed to parse command. command: %s", request.cmd.String()),
			}
		}
		request.AbstractRequest = AbstractRequest{
			queue: queue,
			Req:   request,
			CmdID: request.cmd.CommandID,
		}
		return request, nil
	case C.nvme_admin_get_features:
		var request = &nvmeDiscoveryGetFeaturesRequest{}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.cmd); err != nil {
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("failed to parse command. command: %s", request.cmd.String()),
			}
		}
		request.AbstractRequest = AbstractRequest{
			queue: queue,
			Req:   request,
			CmdID: request.cmd.CommandID,
		}
		return request, nil
	case C.nvme_admin_async_event:
		var request = &AsyncEventRequest{}
		if err := struc.Unpack(bytes.NewReader(pdu), &request.cmd); err != nil {
			return nil, &ParserError{
				status: C.NVME_SC_INVALID_FIELD | C.NVME_SC_DNR,
				msg:    fmt.Sprintf("failed to parse command. command: %s", request.cmd.String()),
			}
		}
		request.AbstractRequest = AbstractRequest{
			queue: queue,
			Req:   request,
			CmdID: request.cmd.CommandID,
		}
		return request, nil
	default:
		return nil, &ParserError{
			status: C.NVME_SC_INVALID_OPCODE | C.NVME_SC_SUCCESS,
			msg:    fmt.Sprintf("invalid opcode: %d", opcode),
		}
	}
}

func NewDiscoveryRspPageEntry(port *Port, subsysNqn string, subType SubsystemType) *NvmefDiscRspPageEntry {
	entry := &NvmefDiscRspPageEntry{
		TrType:  port.TrType,
		AdrFam:  port.AdrFam,
		Treq:    port.Treq,
		PortID:  port.ID,
		CntlID:  C.NVME_CNTLID_DYNAMIC,
		Asqsz:   C.NVME_AQ_DEPTH,
		SubType: subType,
		Traddr:  port.TrAddr,
		Tsas:    port.Tsas,
		Subnqn:  subsysNqn,
	}
	copy(entry.TrsvcID[:], port.TrsvcID)
	return entry
}

type NvmeGetDiscoveryLogPageRequest struct {
	AbstractRequest
	Cmd GetLogPageCommand
	// Discovery also accesses common command parts which are
	// not available on log page command
	commonCmd CommonCommand
}

func (request *NvmeGetDiscoveryLogPageRequest) String() string {
	status := "not completed"
	if request.Completion() != nil {
		status = fmt.Sprintf("%#02x", request.Completion().Status>>1)
	}
	return fmt.Sprintf("%s, id: %#04x. opcode: %s(%#04x). status: %q",
		reflect.TypeOf(request).String(), request.Cmd.CommandID,
		OpcodeName(request.Cmd.Opcode), request.Cmd.Opcode, status)
}

func (request *NvmeGetDiscoveryLogPageRequest) PackCmd(buffer *bufio.Writer) error {
	return struc.Pack(buffer, &request.Cmd)
}

func (request *NvmeGetDiscoveryLogPageRequest) isWrite() bool {
	return false
}

func (request *NvmeGetDiscoveryLogPageRequest) CommandID() uint16 {
	return request.Cmd.CommandID
}

func (request *NvmeGetDiscoveryLogPageRequest) dptr() *DataPtr {
	return &request.Cmd.Dptr
}

// This bit specifies when to retain or clear an Asynchronous Event.
// If this bit is cleared to ‘0’, the corresponding Asynchronous Event is cleared after the
// command completes successfully. If this bit is set to ‘1’, the corresponding Asynchronous Event
// is retained (i.e., not cleared) after the command completes successfully.
// Figure 186: Get Log Page – Command Dword 10
func (request *NvmeGetDiscoveryLogPageRequest) retainAsynchronousEvent() {
	// the 15th bit in cdw10 means Retain Asynchronous Event (RAE)
	rae := request.commonCmd.Cdw10 & (1 << 15)
	value := (rae != 0)
	request.queue.sq.ctrl.setRetainAsynchronousEvent(value)
}

// this method will be called multiple times for each discovery command.
// the first time we will have dataLen == 1024 and all we can return is the header
// which is sized 1024B.
// the next call will be triggered by the client with an offset of 0 and dataLen == 4K
// we need to understand what is the offset write the header if the offset is 0 and start writing entries,
// until we wrote all the SGL buffer.
// the next iteration we need to understand the offset, and return the entries that was not yet returned.
//
//
// example flow for 9 entries:
// first call:  dataLen == 1024 offset == 0
//    	1K | header
//
// second call:  dataLen == 4096 offset == 0
//    	1K | header
// 		1K | entry-1
// 		1K | entry-2
// 		1K | entry-3
//
// third call:  dataLen == 4096 offset == 4096
// 		1K | entry-4
// 		1K | entry-5
// 		1K | entry-6
// 		1K | entry-7
//
// forth call:  dataLen == 2048 offset == 8192
// 		1K | entry-8
// 		1K | entry-9
//
//
func (request *NvmeGetDiscoveryLogPageRequest) execute() {
	dataLen := maxUint32(request.Cmd.GetLogPageLen(), C.sizeof_struct_nvmf_disc_rsp_page_entry)
	offset := request.Cmd.GetLogPageOffset()
	header := &DiscRspPageHdr{}
	var residualLen, numEntries uint32

	discoveryResponseHeaderSize := uint32(binary.Size(header))
	if offset == 0 {
		if dataLen > discoveryResponseHeaderSize {
			residualLen = dataLen - discoveryResponseHeaderSize
			numEntries = residualLen / discoveryResponseHeaderSize
		}
	} else {
		numEntries = dataLen / discoveryResponseHeaderSize
	}

	if request.queue.discoverySubsystem == nil {
		completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_INTERNAL|C.NVME_SC_DNR)
		request.queue.completeRequest(request, completion)
		return
	}

	// in case this is not offset == 0 we already wrote the header so we remove it from the offset size.
	if offset > 0 {
		offset -= uint64(binary.Size(header))
	}
	entries, numRec, genCounter := request.queue.discoverySubsystem.FillDiscoveryLogPage(offset, numEntries, request.queue.sq.ctrl.hostNqn)
	header.GenCtr = genCounter
	request.retainAsynchronousEvent()
	header.NumRec = uint64(numRec)
	header.RecFmt = 0

	sglWriter := NewScatterListWriter(request.GetData())

	// we write the header only once on the command that is with offset == 0
	if offset == 0 {
		// It is ok to have error here, asks short buffer on first request only 16 bytes
		struc.Pack(sglWriter, header)
	}

	// write all the entries one by one - each one is 1K size
	for _, entry := range entries {
		err := struc.Pack(sglWriter, entry)
		if err != nil {
			logrus.WithError(err).Errorf("failed to write discovery log page entry")
			completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_INTERNAL|C.NVME_SC_DNR)
			request.queue.completeRequest(request, completion)
			return
		}
	}

	// logrus.Printf("offset: %d, request.cmd: %#v", offset, request.cmd)
	// logrus.Printf("dataLen: %d, header.NumRec: %d, header.GenCtr: %d, entries count: %d, numEntries: %d", dataLen, header.NumRec, header.GenCtr, len(entries), numEntries)
	completion := NewCompletion(request.CommandID(), request.queue.sq.qID, C.NVME_SC_SUCCESS)
	request.queue.completeRequest(request, completion)
}
