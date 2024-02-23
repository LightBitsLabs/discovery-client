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

package nvmeclient

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/avast/retry-go"
	"github.com/google/uuid"
	"github.com/lightbitslabs/discovery-client/metrics"
	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
	"github.com/lightbitslabs/discovery-client/pkg/ioctl"
	"github.com/lightbitslabs/discovery-client/pkg/nvme"
	"github.com/lightbitslabs/discovery-client/pkg/regexutil"
	"github.com/lunixbochs/struc"
	"github.com/sirupsen/logrus"
)

//#include <../nvme/linux/nvme.h>
import "C"

type NvmeClientError struct {
	Msg    string
	Status int
	Err    error
}

func (e *NvmeClientError) Error() string {
	return fmt.Sprintf("%s. err: %v", e.Msg, e.Unwrap())
}

func (e *NvmeClientError) Unwrap() error {
	return e.Err
}

const (
	SysNvme = "/sys/class/nvme"

	PathNvmeFabrics = "/dev/nvme-fabrics"

	NVME_NO_LOG_LSP = 0x0
)

const (
	DISC_OK                = 1
	DISC_NO_LOG            = 2
	DISC_GET_NUMRECS       = 3
	DISC_GET_LOG           = 4
	DISC_RETRY_EXHAUSTED   = 5
	DISC_NOT_EQUAL         = 6
	DISC_FAILED            = 7
	CONN_FAILED            = 8
	CONN_ALREADY_CONNECTED = 9
	ADD_CTRL_FAILED        = 10
)

var (
	connPattern = regexp.MustCompile(`(?P<field>\w+)=(?P<value>[^,]+)`)
)

type NvmeControllerInfo struct {
	Traddr     string
	Trsvcid    int
	Transport  string
	Subsysnqn  string
	HostTraddr string
	// Device path of the controller - /dev/nvme<Instance>
	Device string
}

type CtrlIdentifier struct {
	Instance int
	Cntlid   int
	// Device path of the controller - /dev/nvme<Instance>
	Device string
}

type ConnectRequest struct {
	Transport   string
	Traddr      string
	Trsvcid     int
	Hostnqn     string
	Hostaddr    string
	Subsysnqn   string
	CtrlLossTMO int
	MaxIOQueues int
	Hostid      string
}

// ToOptions returns a comma delimited key=value string
// example: transport=tcp,traddr=2.2.2.2,trsvcid=8009,hostnqn=xxxxxxx
func (c *ConnectRequest) ToOptions() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("nqn=%s", c.Subsysnqn))
	if len(c.Transport) > 0 {
		sb.WriteString(fmt.Sprintf(",transport=%s", c.Transport))
	}
	if len(c.Traddr) > 0 {
		sb.WriteString(fmt.Sprintf(",traddr=%s", c.Traddr))
	}
	if c.Trsvcid > 0 {
		sb.WriteString(fmt.Sprintf(",trsvcid=%d", c.Trsvcid))
	}
	if len(c.Hostnqn) > 0 {
		sb.WriteString(fmt.Sprintf(",hostnqn=%s", c.Hostnqn))
	}
	if len(c.Hostaddr) > 0 {
		sb.WriteString(fmt.Sprintf(",host_traddr=%s", c.Hostaddr))
	}
	if c.CtrlLossTMO >= -1 {
		sb.WriteString(fmt.Sprintf(",ctrl_loss_tmo=%d", c.CtrlLossTMO))
	}
	if c.MaxIOQueues > 0 {
		sb.WriteString(fmt.Sprintf(",nr_io_queues=%d", c.MaxIOQueues))
	}
	if len(c.Hostid) > 0 {
		sb.WriteString(fmt.Sprintf(",hostid=%s", c.Hostid))
	}
	return sb.String()
}

func listNvmeControllers() ([]string, error) {
	entries, err := os.ReadDir(SysNvme)
	if err != nil {
		return nil, err
	}

	var controllers []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fsinfo, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if fsinfo.Mode()&os.ModeSymlink == 0 {
			continue
		}
		controllers = append(controllers, entry.Name())
	}
	return controllers, nil
}

func getNvmeControllerInfo(ctrlName string) (*NvmeControllerInfo, error) {
	info := &NvmeControllerInfo{}

	ctrlPath := path.Join(SysNvme, ctrlName)

	subsysnqn, err := os.ReadFile(path.Join(ctrlPath, "subsysnqn"))
	if err != nil {
		return nil, err
	}
	info.Subsysnqn = strings.TrimSpace(string(subsysnqn))

	transport, err := os.ReadFile(path.Join(ctrlPath, "transport"))
	if err != nil {
		return nil, err
	}
	info.Transport = strings.TrimSpace(string(transport))

	address, err := os.ReadFile(path.Join(ctrlPath, "address"))
	if err != nil {
		return nil, err
	}
	params := regexutil.GetRepeatedParams(connPattern, strings.TrimSpace(string(address)))
	for _, param := range params {
		value := strings.TrimSpace(param["value"])
		switch param["field"] {
		case "traddr":
			info.Traddr = value
		case "trsvcid":
			port, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				logrus.WithError(err).Errorf("failed to parse port")
			}
			info.Trsvcid = int(port)
		case "host_traddr":
			info.HostTraddr = value
		}
	}
	info.Device = fmt.Sprintf("/dev/%s", ctrlName)

	return info, nil
}

func ListNvmeControllersInfo() (map[string]*NvmeControllerInfo, error) {
	ctrlNames, err := listNvmeControllers()
	if err != nil {
		return nil, err
	}
	res := make(map[string]*NvmeControllerInfo)
	for _, ctrlName := range ctrlNames {
		info, err := getNvmeControllerInfo(ctrlName)
		if err != nil {
			logrus.WithError(err).Errorf("read ctrl failed")
		}
		res[ctrlName] = info
	}
	return res, nil
}

func checkCtrlPathExists(sysPath string) (bool, error) {
	_, err := os.Stat(sysPath)

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return true, err
}

func CheckCtrlRemovePathExists(devicePath string) (bool, error) {
	deleteControllerPath := path.Join(SysNvme, fmt.Sprintf("%s/delete_controller", path.Base(devicePath)))
	return checkCtrlPathExists(deleteControllerPath)
}

func removeCtrlByPath(sysPath string) error {
	f, err := os.OpenFile(sysPath, os.O_WRONLY, 0755)
	if err != nil {
		logrus.WithError(err).Errorf("failed to open file")
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			logrus.WithError(err).Errorf("failed to close file")
			return
		}
	}()

	_, err = f.Write([]byte("1"))
	if err != nil {
		return err
	}
	return nil
}

func RemoveCtrl(instanceID int) error {
	deleteControllerPath := path.Join(SysNvme, fmt.Sprintf("nvme%d/delete_controller", instanceID))
	return removeCtrlByPath(deleteControllerPath)

}

// devicePath is `/dev/nvme0`
func RemoveCtrlByDevice(devicePath string) error {
	deleteControllerPath := path.Join(SysNvme, fmt.Sprintf("%s/delete_controller", path.Base(devicePath)))
	return removeCtrlByPath(deleteControllerPath)
}

func addCtrl(options string) (*CtrlIdentifier, error) {
	f, err := os.OpenFile(PathNvmeFabrics, os.O_RDWR, 0755)
	if err != nil {
		return nil, &NvmeClientError{
			Status: ADD_CTRL_FAILED,
			Msg:    fmt.Sprintf("something wrong happened. failed to open file: %q", PathNvmeFabrics),
			Err:    err,
		}
	}

	defer func() {
		if err := f.Close(); err != nil {
			logrus.WithError(err).Errorf("failed to close file: %q", f.Name())
			return
		}
	}()
	_, err = f.Write([]byte(options))
	if err != nil {
		return nil, &NvmeClientError{
			Status: ADD_CTRL_FAILED,
			Msg:    fmt.Sprintf("write failed: %q", options),
			Err:    err,
		}
	}

	buf := make([]byte, 4096)
	_, err = f.Read(buf)
	if err != nil {
		return nil, &NvmeClientError{
			Status: ADD_CTRL_FAILED,
			Msg:    fmt.Sprintf("read failed: %q", options),
			Err:    err,
		}
	}
	conn := strings.TrimSpace(strings.TrimRight(string(buf), "\x00"))
	ctrlInfo := parseConnInfo(conn)
	return ctrlInfo, nil
}

func parseConnInfo(conn string) *CtrlIdentifier {
	ctrlInfo := &CtrlIdentifier{}
	params := regexutil.GetRepeatedParams(connPattern, strings.TrimSpace(conn))
	for _, param := range params {
		value := strings.TrimSpace(param["value"])
		switch param["field"] {
		case "instance":
			v, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				logrus.WithError(err).Errorf("failed to parse instance id")
				continue
			}
			ctrlInfo.Instance = int(v)
		case "cntlid":
			v, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				logrus.WithError(err).Errorf("failed to parse controller id")
				continue
			}
			ctrlInfo.Cntlid = int(v)
		}
	}
	ctrlInfo.Device = fmt.Sprintf("/dev/nvme%d", ctrlInfo.Instance)
	return ctrlInfo
}

type nvmefDiscRspPageHdr struct {
	GenCtr uint64      `struc:"uint64,little"`
	NumRec uint64      `struc:"uint64,little"`
	RecFmt uint16      `struc:"uint16,little"`
	Resv14 [1006]uint8 `struc:"[1006]uint8"`
}

type nvmefDiscRspPageEntry struct {
	TrType  uint8                      `struc:"uint8"`
	AdrFam  uint8                      `struc:"uint8"`
	SubType nvme.SubsystemType         `struc:"uint8"`
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

var (
	nvmefDiscRspPageEntrySize uint64
	discoveryHeaderSize       uint64
)

// initialize the struct sizes which should be fixed across application
func init() {
	var err error
	var val int
	val, err = struc.Sizeof(&nvmefDiscRspPageEntry{})
	if err != nil {
		panic(err)
	}
	nvmefDiscRspPageEntrySize = uint64(val)
	val, err = struc.Sizeof(&nvmefDiscRspPageHdr{})
	if err != nil {
		panic(err)
	}
	discoveryHeaderSize = uint64(val)
}

func readDiscoveryResponseHeader(f *os.File) (*nvmefDiscRspPageHdr, error) {
	bufSize := discoveryHeaderSize + 8
	buf := make([]byte, bufSize)
	if err := nvmeDiscoveryLog(f, buf); err != nil {
		logrus.WithError(err).Errorf("get discovery log failed")
		return nil, err
	}
	discoveryResponseHdr := &nvmefDiscRspPageHdr{}
	if err := struc.Unpack(bytes.NewReader(buf), discoveryResponseHdr); err != nil {
		logrus.WithError(err).Errorf("unpack discovery response header failed")
		return nil, err
	}
	return discoveryResponseHdr, nil
}

func nvmfDiscoveryGetLogPage(ctrlInfo *CtrlIdentifier) ([]*nvmefDiscRspPageEntry, error) {
	f, err := os.OpenFile(ctrlInfo.Device, os.O_RDWR, 0755)
	if err != nil {
		return nil, &NvmeClientError{Status: DISC_FAILED, Msg: "failed to open file", Err: err}
	}

	defer func() {
		if err := f.Close(); err != nil {
			logrus.WithError(err).Errorf("failed to close file")
			return
		}
	}()

	discoveryResponseHdr, err := readDiscoveryResponseHeader(f)
	if err != nil {
		return nil, &NvmeClientError{Status: DISC_GET_LOG, Msg: "get discovery log failed", Err: err}
	}

	if discoveryResponseHdr.NumRec == 0 {
		return nil, &NvmeClientError{Status: DISC_NO_LOG, Msg: "no log entries", Err: nil}
	}

	logSize := discoveryHeaderSize + nvmefDiscRspPageEntrySize*discoveryResponseHdr.NumRec
	log := make([]byte, logSize)
	if err := nvmeDiscoveryLog(f, log); err != nil {
		return nil, &NvmeClientError{Status: DISC_GET_LOG, Msg: "get discovery log failed", Err: err}
	}
	var res []*nvmefDiscRspPageEntry
	reader := bytes.NewReader(log)

	if err := struc.Unpack(reader, &discoveryResponseHdr); err != nil {
		return nil, &NvmeClientError{Status: DISC_GET_LOG, Msg: "unpack discovery response header failed", Err: err}
	}

	for i := uint64(0); i < discoveryResponseHdr.NumRec; i++ {
		entry := &nvmefDiscRspPageEntry{}
		if err := struc.Unpack(reader, entry); err != nil {
			logrus.WithError(err).Errorf("unpack discovery response page entry failed")
			return nil, err
		}
		res = append(res, entry)
	}

	/*
	 * The above call to nvme_discovery_log() might result
	 * in several calls (with different offsets), so we need
	 * to fetch the header again to have the most up-to-date
	 * value for the generation counter
	 */
	generationCounter := discoveryResponseHdr.GenCtr
	numRecords := discoveryResponseHdr.NumRec
	currentDiscoveryResponseHdr, err := readDiscoveryResponseHeader(f)
	if err != nil {
		return nil, &NvmeClientError{Status: DISC_GET_LOG, Msg: "get discovery log failed", Err: err}
	}

	// notice that now we implement no retries so if this is not equal we fail on the first try.
	// maybe later we will implement this part
	if generationCounter != currentDiscoveryResponseHdr.GenCtr {
		return nil, &NvmeClientError{Status: DISC_RETRY_EXHAUSTED, Msg: "exhausted all retries", Err: fmt.Errorf("exhausted all retries")}
	}

	if numRecords != currentDiscoveryResponseHdr.NumRec {
		return nil, &NvmeClientError{
			Status: DISC_NOT_EQUAL,
			Msg:    fmt.Sprintf("got different record count: %d != %d", numRecords, currentDiscoveryResponseHdr.NumRec),
			Err:    fmt.Errorf("exhausted all retries")}
	}

	return res, nil
}

func Connect(request *ConnectRequest) (*CtrlIdentifier, error) {
	traddr, err := nvme.AdjustTraddr(request.Traddr)
	if err != nil {
		return nil, &NvmeClientError{
			Status: CONN_FAILED,
			Msg:    fmt.Sprintf("invalid traddr %s", request.Traddr),
			Err:    err,
		}
	}
	request.Traddr = traddr
	request.Hostid = uuid.NewMD5(uuid.Nil, []byte(request.Hostnqn)).String()

	ctrlID, err := addCtrl(request.ToOptions())
	if err != nil {
		var perr *NvmeClientError
		if errors.As(err, &perr) {
			if strings.Contains(perr.Err.Error(), "operation already in progress") {
				return nil, &NvmeClientError{
					Status: CONN_ALREADY_CONNECTED,
					Msg:    "controller already connected",
					Err:    err,
				}
			}
			return nil, &NvmeClientError{
				Status: CONN_FAILED,
				Msg:    "add controller failed",
				Err:    err,
			}
		}
	}
	return ctrlID, nil
}

func ConnectAll(discoveryRequest *hostapi.DiscoverRequest, maxIOQueues int) ([]*CtrlIdentifier, error) {
	logPageEntries, err := Discover(discoveryRequest)
	if err != nil {
		return nil, err
	}
	ctrls := ConnectAllNVMEDevices(logPageEntries, discoveryRequest.Hostnqn, discoveryRequest.Transport, maxIOQueues)
	return ctrls, nil
}

func connectNVMEDevicesWithRetry(request *ConnectRequest) (*CtrlIdentifier, error) {
	var err error
	var ctrlID *CtrlIdentifier

	logrus.Debug("try to reconnect nvme-devices")

	retry.Do(func() error {
		ctrlID, err = Connect(request)
		if err == nil {
			return nil
		}

		return err
	}, retry.DelayType(retry.BackOffDelay), retry.Attempts(5), retry.Delay(time.Millisecond*10))

	return ctrlID, err
}

func ConnectAllNVMEDevices(logPageEntries []*hostapi.NvmeDiscPageEntry, hostnqn string, transport string, maxIOQueues int) []*CtrlIdentifier {
	var ctrls []*CtrlIdentifier
	for _, logPageEntry := range logPageEntries {
		// skip the non IO subsystems.
		if logPageEntry.SubType != nvme.NVME_NQN_NVME {
			continue
		}
		request := &ConnectRequest{
			Traddr:      logPageEntry.Traddr,
			Trsvcid:     int(logPageEntry.TrsvcID),
			Subsysnqn:   logPageEntry.Subnqn,
			Hostnqn:     hostnqn,
			Transport:   transport,
			CtrlLossTMO: -1,
			MaxIOQueues: maxIOQueues,
		}
		ctrlID, err := Connect(request)
		if err != nil {
			// we might get 2 problems here, either we already connected, and we don't care about this error
			// or the connection has failed because io ctrl on the published target not accessible for some reason.
			// either way we don't have anything to do with this information and we continue.
			var perr *NvmeClientError
			if errors.As(err, &perr) {
				if perr.Status == CONN_ALREADY_CONNECTED {
					continue
				} else {
					ctrlID, err = connectNVMEDevicesWithRetry(request)
					if errors.As(err, &perr) {
						// This warn will occur every 5 sec in case the node is down.
						// discovery service will still report this controller to connect to but we will fail to connect.
						// we can't deduce that if the DS is down on that node we will fail to connect cause there might be a network partition
						// on the discovery-service or the DS is down on that node but the IO controller is still accessible.
						logrus.WithError(perr).Warnf("failed to connect IO controller. This may be a transient error or due to a node being down.",
							"Continuing to attempt connection until the discovery-service stops providing the down node's address..")
					}
				}
			}
			continue
		}
		ctrls = append(ctrls, ctrlID)
	}
	return ctrls
}

func Discover(discoveryRequest *hostapi.DiscoverRequest) ([]*hostapi.NvmeDiscPageEntry, error) {
	traddr, err := nvme.AdjustTraddr(discoveryRequest.Traddr)
	if err != nil {
		return nil, &NvmeClientError{
			Status: CONN_FAILED,
			Msg:    fmt.Sprintf("invalid traddr %s", discoveryRequest.Traddr),
			Err:    err,
		}
	}
	discoveryRequest.Traddr = traddr
	discoveryRequest.Hostid = uuid.NewMD5(uuid.Nil, []byte(discoveryRequest.Hostnqn)).String()

	ctrlID, err := addCtrl(discoveryRequest.ToOptions())
	if err != nil {
		return nil, &NvmeClientError{
			Status: DISC_FAILED,
			Msg:    "discover failed",
			Err:    err,
		}
	}
	defer func() {
		if discoveryRequest.Kato == 0 {
			RemoveCtrl(ctrlID.Instance)
		}
	}()

	logPageEntries, err := nvmfDiscoveryGetLogPage(ctrlID)
	if err != nil {
		var perr *NvmeClientError
		if errors.As(err, &perr) {
			if perr.Status == DISC_NO_LOG {
				metrics.Metrics.DiscoveryLogPageCount.WithLabelValues(discoveryRequest.Hostnqn).Set(0)
				return nil, err
			}
		}
		metrics.Metrics.DiscoveryLogPageCount.DeleteLabelValues(discoveryRequest.Hostnqn)
		return nil, err
	}

	metrics.Metrics.DiscoveryLogPageCount.WithLabelValues(discoveryRequest.Hostnqn).Set(float64(len(logPageEntries)))

	var res []*hostapi.NvmeDiscPageEntry
	for _, entry := range logPageEntries {
		// TrsvcID is the string representation of the tcp port.
		// this means that we first convert the byte slice to string, then trim the x00 out then convert the string to int.
		port, err := strconv.ParseInt(strings.TrimRight(string(entry.TrsvcID[:]), "\x00"), 10, 64)
		if err != nil {
			logrus.WithError(err).Errorf("failed to parse trsvcid")
			continue
		}
		respEntry := &hostapi.NvmeDiscPageEntry{
			SubType: entry.SubType,
			PortID:  entry.PortID,
			CntlID:  entry.CntlID,
			TrsvcID: uint16(port),
			Subnqn:  strings.TrimRight(entry.Subnqn, "\x00"),
			Traddr:  strings.TrimRight(entry.Traddr, "\x00"),
		}
		res = append(res, respEntry)
	}
	return res, nil
}

func nvmeDiscoveryLog(f *os.File, log []byte) error {
	offset := uint64(0)
	rae := false
	return nvmeGetLog(f, 0, C.NVME_LOG_DISC, NVME_NO_LOG_LSP, offset, 0, rae, log)
}

func nvmeGetLog(f *os.File, nsid uint32, logID int, lsp uint8, lpo uint64, lsi uint16, rae bool, data []byte) error {
	numd := uint32(len(data)>>2) - 1
	numdu := uint16(numd >> 16)
	numdl := uint16(numd & 0xffff)
	var cdw10 uint32
	if rae {
		cdw10 = uint32(logID) | uint32(numdl)<<16 | uint32(1<<15) | uint32(lsp)<<8
	} else {
		cdw10 = uint32(logID) | uint32(numdl)<<16 | uint32(lsp)<<8
	}
	uuidIX := uint32(0)
	buffer := uintptr(unsafe.Pointer(&data[0]))
	cmd := &NvmeAdminCmd{
		Opcode:  C.nvme_admin_get_log_page,
		Nsid:    nsid,
		Addr:    buffer,
		DataLen: uint32(len(data)),
		Cdw10:   cdw10,
		Cdw11:   uint32(numdu) | uint32(lsi)<<16,
		Cdw12:   uint32(lpo) & 0xffffffff,
		Cdw13:   uint32(lpo >> 32),
		Cdw14:   uuidIX,
	}
	if err := ioctl.Ioctl(f.Fd(), NvmeIoctlAdminCmd(cmd), uintptr(unsafe.Pointer(cmd))); err != nil {
		logrus.WithError(err).Errorf("ioctl failed")
		return err
	}

	return nil
}
