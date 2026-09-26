# Observability

## Metrics agent

`tiny-cni-agent` ([`cmd/tiny-cni-agent`](../cmd/tiny-cni-agent)) is the main container of the DaemonSet, so one runs on each node. It serves Prometheus metrics at `:9102/metrics`, and computes them on every scrape from:

- the CNI config at `/etc/cni/net.d/05-tinycni.conf`, which is hardcoded for now,
- the IPAM state file at `ipam.storagePath` (see [ipam.md](ipam.md)),
- the host's links, counting veths whose name starts with `prefix`.

Both files are mounted read-only.

| Metric                  | Type  | Meaning                                                         |
| ----------------------- | ----- | --------------------------------------------------------------- |
| `tinycni_capacity_ips`  | gauge | Usable IPs in the subnet (size − network − gateway − broadcast) |
| `tinycni_allocated_ips` | gauge | IPs currently allocated to containers                           |
| `tinycni_left_ips`      | gauge | `capacity − allocated`                                          |
| `tinycni_veths`         | gauge | Host-side veths with the configured prefix                      |

If `tinycni_veths` and `tinycni_allocated_ips` disagree for long, some ADD or DEL left a leak behind (a veth without an IP, or the reverse).

## Prometheus

The pod template carries the usual scrape annotations:

```yaml
prometheus.io/scrape: "true"
prometheus.io/port: "9102"
```

Any Prometheus that uses annotation-based pod discovery picks the agents up automatically. The agent uses `hostNetwork: true`, so you can also scrape `<node-ip>:9102` directly.

## Grafana

Import [`monitoring/grafana/grafana-dashboard.json`](../monitoring/grafana/grafana-dashboard.json). It has two rows:

- **Cluster**: allocated IPs, free IPs, pool usage, nodes reporting, and IP/veth drift.
- **Per node**: allocated IPs, pool usage, free IPs and veths, for each node.
