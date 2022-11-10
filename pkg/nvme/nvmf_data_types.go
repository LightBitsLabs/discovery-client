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
