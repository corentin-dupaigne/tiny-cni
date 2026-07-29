package ipam

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// hardcoded subnet (short term solution)
const Subnet string = "172.17.17.0/24"
const CounterPath string = "/tmp/tiny-cni-counter"

func getNextIp(subnet string, counterPath string) (string, error) {
	nextIp := ""
	data, err := os.ReadFile(counterPath)
	if errors.Is(err, os.ErrNotExist) {
		_, after, _ := strings.Cut(subnet, "/")
		prefixLength, _ := strconv.Atoi(after)
		totalIps := math.Pow(2, 32-float64(prefixLength)) - 2
		fileContent := []byte("0/" + strconv.Itoa(int(totalIps)))
		err = os.WriteFile(counterPath, fileContent, 0644)
		if err != nil {
			return "", fmt.Errorf("Error while writing at path %s", counterPath)
		}
	} else if err != nil {
		return "", fmt.Errorf("Error while reading file %s", counterPath)
	}

	currentIp, maxIp, _ := strings.Cut(string(data), "/")
	if maxIp >= currentIp {
		return "", fmt.Errorf("no IP addresses available in range %s", subnet)
	}
	lastPeriod := strings.LastIndexByte(subnet, '.')
	before, _, _ := strings.Cut(subnet, string(subnet[lastPeriod]))
	nextIp = before + "." + currentIp

	return nextIp, nil
}
