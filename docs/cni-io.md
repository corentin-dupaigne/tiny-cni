# CNI I/O contract

Per the [CNI spec](https://www.cni.dev/docs/spec/), the runtime and the plugin only talk through env vars, stdin, stdout, stderr and the exit code. They share nothing else.

## In

**Env vars:** `CNI_COMMAND` (`ADD`/`DEL`/`VERSION`), `CNI_CONTAINERID`, `CNI_NETNS`, `CNI_IFNAME` and `CNI_PATH`. `CNI_ARGS` is accepted but ignored.

**Stdin:** the network configuration, as JSON:

```json
{
  "cniVersion": "1.0.0",
  "name": "tinycni",
  "type": "tiny-cni",
  "bridge": "tcni-bridge",
  "prefix": "tcni",
  "ipam": {
    "type": "tiny-cni",
    "subnet": "10.244.0.0/24",
    "storagePath": "/run/tinycni.json"
  }
}
```

| Field               | Description                                                         |
| ------------------- | ------------------------------------------------------------------- |
| `cniVersion`        | Standard CNI field. Also sets the schema of the result.             |
| `name`, `type`      | Standard CNI fields.                                                |
| `bridge`            | Name of the Linux bridge on the host, created on first ADD.         |
| `prefix`            | Prefix of host-side veth names (`<prefix>-<8 hex chars>`).          |
| `ipam.subnet`       | Pod subnet. `.0` is the network, `.1` is the gateway on the bridge. |
| `ipam.storagePath`  | IPAM state file, see [ipam.md](ipam.md).                            |

## Out

**On success:** stdout carries the CNI `Result` as JSON, in the schema of the request's `cniVersion`, and the exit code is `0`.

**On failure:** stdout carries a CNI error object (`{"code", "msg", "details"}`) instead, and the exit code is `1`.

stderr only carries structured JSON logs (`slog`), for debugging.

### ADD result

The result lists both ends of the veth pair: the host end without `sandbox`, and the container end with `sandbox` set to `CNI_NETNS`. It also lists the IP handed out by the IPAM, which points at the container interface by index:

```json
{
  "cniVersion": "1.0.0",
  "interfaces": [
    { "name": "tcni-a1b2c3d4", "mac": "aa:bb:cc:dd:ee:01" },
    { "name": "eth0", "mac": "aa:bb:cc:dd:ee:02", "sandbox": "/var/run/netns/container2" }
  ],
  "ips": [
    { "interface": 1, "address": "10.244.0.2/24" }
  ]
}
```

### DEL result

DEL writes nothing to stdout on success (empty result).
