package ipam

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
)

// hardcoded subnet (short term solution)
const Subnet string = "172.17.17.0/24"
const StoragePath string = "/tmp/tiny-cni-counter"

func GetNextIp(subnet string, storagePath string) (string, error) {
	var availableIp netip.Addr

	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return "", fmt.Errorf("parsing prefix from subnet %s: %w", subnet, err)
	}

	data, err := os.ReadFile(storagePath)
	if errors.Is(err, os.ErrNotExist) {

		networkIp := prefix.Masked().Addr()

		availableIp = networkIp.Next().Next()

	} else if err != nil {
		return "", fmt.Errorf("reading file %s: %w", storagePath, err)
	} else {
		availableIp, err = netip.ParseAddr(string(data))
		if err != nil {
			return "", fmt.Errorf("parsing IP from file %s: %w", storagePath, err)
		}
	}

	if !availableIp.IsValid() {
		return "", fmt.Errorf("parsed IP is invalid: %s", availableIp)
	}

	if !prefix.Contains(availableIp.Next()) {
		return "", fmt.Errorf("no IP addresses available in range %s", subnet)
	}

	err = os.WriteFile(storagePath, []byte(availableIp.Next().String()), 0644)
	if err != nil {
		return "", fmt.Errorf("writing in file %s: %w", storagePath, err)
	}

	return availableIp.String(), nil
}
