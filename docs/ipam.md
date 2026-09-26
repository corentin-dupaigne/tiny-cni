# IPAM

The IPAM is part of the CNI plugin and ships in the same binary. It is stateful: it reads and updates a JSON state file at `ipam.storagePath` from the network config. Every operation that changes the state holds a `flock` on that file, so concurrent ADD/DEL calls can't race.

## Allocation

- The network (`.0`), gateway (`.1`) and broadcast addresses are never handed out.
- The first container gets `.2`. Later containers get a free address next to one that is already allocated.
- ADD is idempotent per container: a container ID that already has an IP gets the same IP back.
- DEL releases the IP, and a later ADD can reuse it.

With the default `/24`, a node can run 253 pods at the same time.

The design rationale is in [ADR 0002](adr/0002-ipam-allocation-set-with-ip-reuse.md), which supersedes the original counter design from [ADR 0001](adr/0001-ipam-sequential-allocation-without-ip-reuse.md).

## State file

The state holds two maps: each container's IP, and the set of allocated IPs (reserved addresses included):

```json
{
  "containerToIp": {
    "container1": "10.244.0.2",
    "container2": "10.244.0.3"
  },
  "allocatedSet": {
    "10.244.0.0": true,
    "10.244.0.1": true,
    "10.244.0.2": true,
    "10.244.0.3": true
  }
}
```

A release deletes the IP's key from `allocatedSet`. It does not set the value to `false`.

The file is also read, read-only, by the [metrics agent](observability.md).
