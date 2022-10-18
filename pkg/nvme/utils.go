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
