package nvme

type ConnectCommand struct {
	Opcode    uint8     `struc:"uint8"`
	Resv1     uint8     `struc:"uint8"`
	CommandID uint16    `struc:"uint16,little"`
	FcType    uint8     `struc:"uint8"`
	Rsvd2     [19]uint8 `struc:"[19]uint8"`
	Dptr      DataPtr
	RecFmt    uint16    `struc:"uint16,little"`
	QID       uint16    `struc:"uint16,little"`
	SqSize    uint16    `struc:"uint16,little"`
	CatTr     uint8     `struc:"uint8"`
	Resv3     uint8     `struc:"uint8"`
	Kato      uint32    `struc:"uint32,little"`
	Resv4     [12]uint8 `struc:"[12]uint8"`
}

type ConnectData struct {
	HostID    string     `struc:"[16]byte"`
	CntlID    uint16     `struc:"uint16,little"`
	Rsv4      [238]uint8 `struc:"[238]uint8"`
	SubsysNqn string     `struc:"[256]uint8"`
	HostNqn   string     `struc:"[256]uint8"`
	Rsv5      [256]uint8 `struc:"[256]uint8"`
}
