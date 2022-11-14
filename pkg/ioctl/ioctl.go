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

package ioctl

import "syscall"

const (
	typeBits      = 8
	numberBits    = 8
	sizeBits      = 14
	directionBits = 2

	typeMask      = (1 << typeBits) - 1
	numberMask    = (1 << numberBits) - 1
	sizeMask      = (1 << sizeBits) - 1
	directionMask = (1 << directionBits) - 1

	directionNone  = 0
	directionWrite = 1
	directionRead  = 2

	numberShift    = 0
	typeShift      = numberShift + numberBits
	sizeShift      = typeShift + typeBits
	directionShift = sizeShift + sizeBits
)

func ioc(dir, t, nr, size uintptr) uintptr {
	return (dir << directionShift) | (t << typeShift) | (nr << numberShift) | (size << sizeShift)
}

// Io used for a simple ioctl that sends nothing but the type and number, and receives back nothing but an (integer) retval.
func Io(t, nr uintptr) uintptr {
	return ioc(directionNone, t, nr, 0)
}

// IoR used for an ioctl that reads data from the device driver. The driver will be allowed to return sizeof(data_type) bytes to the user.
func IoR(t, nr, size uintptr) uintptr {
	return ioc(directionRead, t, nr, size)
}

// IoW used for an ioctl that writes data to the device driver.
func IoW(t, nr, size uintptr) uintptr {
	return ioc(directionWrite, t, nr, size)
}

// IoRW  a combination of IoR and IoW. That is, data is both written to the driver and then read back from the driver by the client.
func IoRW(t, nr, size uintptr) uintptr {
	return ioc(directionRead|directionWrite, t, nr, size)
}

// Ioctl simplified ioct call
func Ioctl(fd, op, arg uintptr) error {
	_, _, ep := syscall.Syscall(syscall.SYS_IOCTL, fd, op, arg)
	if ep != 0 {
		return syscall.Errno(ep)
	}
	return nil
}
