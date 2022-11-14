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
	"net"
)

func minInt(x, y int) int {
	if x < y {
		return x
	}
	return y
}

func maxUint32(x, y uint32) uint32 {
	if x > y {
		return x
	}
	return y
}

func AdjustTraddr(traddr string) (string, error) {
	if net.ParseIP(traddr).To4() != nil {
		// traddr is ipv4 - do nothing
		return traddr, nil
	}
	if net.ParseIP(traddr).To16() != nil {
		// traddr is ipv6 - do nothing
		return traddr, nil
	}
	addrs, err := net.LookupIP(traddr)
	if err == nil {
		// traddr is hostname - adjust to the first ip
		return addrs[0].String(), nil
	}
	return "", err
}
