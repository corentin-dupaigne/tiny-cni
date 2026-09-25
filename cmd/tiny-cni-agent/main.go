package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/corentin-dupaigne/tiny-cni/internal/config"
	"github.com/corentin-dupaigne/tiny-cni/internal/ipam"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vishvananda/netlink"
)

type collector struct {
	config       *config.Config
	veths        *prometheus.Desc
	routes       *prometheus.Desc
	ipsAllocated *prometheus.Desc
	ipsCapacity  *prometheus.Desc
	ipsLeft      *prometheus.Desc
}

func newCollector() (*collector, error) {

	confUnparsed, err := os.ReadFile("/etc/cni/net.d/05-tinycni.conf")
	if err != nil {
		return &collector{}, err
	}

	conf, err := config.Parse(confUnparsed)
	if err != nil {
		return &collector{}, err
	}

	return &collector{
		config:       conf,
		veths:        prometheus.NewDesc("tinycni_veths", "Number of veth created", nil, nil),
		ipsAllocated: prometheus.NewDesc("tinycni_allocated_ips", "Number of IPs allocated", nil, nil),
		ipsLeft:      prometheus.NewDesc("tinycni_left_ips", "Number of IPs allocated", nil, nil),
		ipsCapacity:  prometheus.NewDesc("tinycni_capacity_ips", "Number of IPs allocated", nil, nil),
	}, nil
}

func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.ipsCapacity
	ch <- c.ipsAllocated
	ch <- c.ipsLeft
	ch <- c.veths
}

func (c *collector) vethCount() int {
	links, err := netlink.LinkList()
	if err != nil {
		return 0
	}

	n := 0
	for _, l := range links {
		if l.Type() == "veth" && strings.HasPrefix(l.Attrs().Name, c.config.Prefix) {
			n++
		}
	}

	return n
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {

	ipamStateUnparsed, err := os.ReadFile(c.config.IPAM.StoragePath)
	if err != nil {
		return
	}

	ipamState := &ipam.IPAMState{}

	err = json.Unmarshal(ipamStateUnparsed, ipamState)
	if err != nil {
		return
	}

	subnet, err := netip.ParsePrefix(c.config.IPAM.Subnet)
	if err != nil {
		return
	}

	capacity := 1<<(32-subnet.Bits()) - 3

	ch <- prometheus.MustNewConstMetric(c.ipsCapacity, prometheus.GaugeValue, float64(capacity))
	ch <- prometheus.MustNewConstMetric(c.ipsAllocated, prometheus.GaugeValue, float64(len(ipamState.AllocatedSet)-2))
	ch <- prometheus.MustNewConstMetric(c.ipsLeft, prometheus.GaugeValue, float64(capacity-len(ipamState.AllocatedSet)+2))
	ch <- prometheus.MustNewConstMetric(c.veths, prometheus.GaugeValue, float64(c.vethCount()))
}

func main() {
	collec, err := newCollector()
	if err != nil {
		return
	}

	prometheus.MustRegister(collec)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(":9102", nil))
}
