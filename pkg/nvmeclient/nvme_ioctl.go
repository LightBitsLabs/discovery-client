package nvmeclient

import (
	"unsafe"

	"github.com/lightbitslabs/discovery-client/pkg/ioctl"
)

type NvmeUserIO struct {
	Opcode   uint8
	Flags    uint8
	Control  uint16
	Nblocks  uint16
	Rsvd     uint16
	Metadata uint64
	Addr     uint64
	Slba     uint64
	Dsmgmt   uint32
	Reftag   uint32
	Apptag   uint16
	Appmask  uint16
}

type NvmePassthruCmd struct {
	Opcode      uint8
	Flags       uint8
	Rsvd1       uint16
	Nsid        uint32
	Cdw2        uint32
	Cdw3        uint32
	Metadata    uint64
	Addr        uintptr
	MetadataLen uint32
	DataLen     uint32
	Cdw10       uint32
	Cdw11       uint32
	Cdw12       uint32
	Cdw13       uint32
	Cdw14       uint32
	Cdw15       uint32
	TimeoutMS   uint32
	Result      uint32
}

type NvmeAdminCmd NvmePassthruCmd

const NvmeIocMagic = uintptr(int('N'))

func NvmeIoctlID(size uintptr) uintptr {
	return ioctl.Io(NvmeIocMagic, 0x40)
}

func NvmeIoctlAdminCmd(data *NvmeAdminCmd) uintptr {
	return ioctl.IoRW(NvmeIocMagic, 0x41, unsafe.Sizeof(*data))
}

func NvmeIoctlSubmitIO(data *NvmeUserIO) uintptr {
	return ioctl.IoW(NvmeIocMagic, 0x42, unsafe.Sizeof(data))
}

func NvmeIoctlIoCmd(data *NvmePassthruCmd) uintptr {
	return ioctl.IoRW(NvmeIocMagic, 0x43, unsafe.Sizeof(data))
}

func NvmeIoctlReset() uintptr {
	return ioctl.Io(NvmeIocMagic, 0x44)
}

func NvmeIoctlSubsysReset() uintptr {
	return ioctl.Io(NvmeIocMagic, 0x45)
}

func NvmeIoctlRescan() uintptr {
	return ioctl.Io(NvmeIocMagic, 0x46)
}
