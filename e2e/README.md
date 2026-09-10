# CNI Conformance Test Suite

End-to-end conformance tests for a CNI plugin **binary**, derived from the
[CNI specification v1.1.0](https://www.cni.dev/docs/spec/), written with
[Ginkgo v2](https://onsi.github.io/ginkgo/) / Gomega. The suite is
implementation-agnostic: it takes a plugin path and a config file, forwards the
config to the plugin **unmodified**, and asserts only spec-defined, observable
behaviour (exit codes, stdout structures, interfaces in the netns).

The requirement matrix, coverage classification and scope reasoning live in
[`CNI Conformance Test Suite — Requirements & Coverage.md`](./requirement_and_coverage.md).
Every spec's text carries the requirement number(s) it covers
(`§2 ADD operation 2.3 errors when CNI_IFNAME already exists …`).

## Running

Namespace specs need root (`CAP_SYS_ADMIN` + `CAP_NET_ADMIN`); they are
labelled `netns` and skip when not root.

```sh
# simplest: make escalates with sudo when needed
make CNI_PLUGIN=/opt/cni/bin/bridge CNI_CONFIG=./bridge.json

# plain go test
sudo -E env CNI_PLUGIN=/opt/cni/bin/bridge CNI_CONFIG=./bridge.json \
    go test -count=1 ./conformance/ -ginkgo.v

# ginkgo CLI
sudo -E env CNI_PLUGIN=... CNI_CONFIG=... ginkgo -v ./conformance/

# self-check against the bundled minimal plugin (hack/refcni)
make test-ref

# every reference plugin in bin/ref against hack/configs/*.json (see below)
make test-matrix
make clean-host        # remove leftover netns / links / IPAM state
```

## Reference plugins

`hack/configs/` holds ready-made configs for the upstream reference plugins
(bridge, ptp, dummy, macvlan, ipvlan, vlan, plus a 0.4.0 bridge and a
static-IPAM bridge used as a negative control for the concurrency spec).
Install the binaries once (`bin/` is git-ignored; `make test-matrix` does this
automatically when `bin/ref` is missing):

```sh
make install-plugins                          # bin/ref/* (v1.6.2) + bin/refcni
make install-plugins CNI_PLUGINS_VERSION=v1.7.1   # pick another release
```

Known, expected results on them:

| Config | Outcome |
|---|---|
| bridge, dummy, macvlan, ipvlan | all pass; advisories for missing `type`/`cniVersion` and GC 9.1 |
| ptp | 2.4 advisory: a second attachment of the *same* network in one netns collides on the `/32` gateway route; the mixed ADD/DEL concurrency spec uses distinct netns so it is unaffected |
| vlan | 7.1 fails and 2.4 advisory: the kernel allows one VLAN device per (master, vlanId), so this network supports a single attachment at a time |
| bridge-0.4.0 | CHECK runs; STATUS/GC skip (protocol 0.4.0); VERSION-echo advisory (skel reports the library version) |
| bridge-static | 7.1 **must fail** (every container gets the same static IP) — proves the concurrency spec bites |

Select specs with Ginkgo's focus / label filters (`FOCUS=` and `LABEL_FILTER=`
with make, or `-ginkgo.focus` / `-ginkgo.label-filter` directly):

| Label | Meaning |
|---|---|
| `MUST` | spec asserts MUST-level requirements (hard failures) |
| `SHOULD` | spec contains SHOULD-level checks (advisory unless strict) |
| `optional` | operation the plugin may not implement; skips gracefully |
| `netns` | needs root |

e.g. `LABEL_FILTER='MUST && !optional'`, `FOCUS='§2|§3'`.

Environment variables:

| Variable | Meaning | Default |
|---|---|---|
| `CNI_PLUGIN` | path to the plugin binary under test | required |
| `CNI_CONFIG` | path to the network config JSON (opaque, forwarded verbatim) | required |
| `CNI_PATH` | `CNI_PATH` handed to the plugin (for IPAM/delegate lookup) | directory of `CNI_PLUGIN` |
| `CNI_TEST_STRICT` | `1` turns SHOULD-level violations into failures | off |
| `CNI_TEST_TIMEOUT` | per-invocation timeout (Go duration) | `30s` |
| `CNI_TEST_CONCURRENCY` | parallel ADDs in the concurrency test | `8` |

## MUST vs SHOULD

- **MUST** requirements fail the spec outright.
- **SHOULD** requirements go through `should(...)`: recorded as a report entry
  on the spec, summarised at the end of the run, and — with
  `CNI_TEST_STRICT=1` — turned into a failure of that spec.
- Optional operations (CHECK < 0.4.0, STATUS/GC < 1.1.0, or a plugin that
  answers "unknown command") are **skipped**, not failed.

Advisory-only by design (see the requirements doc for why):
`cniVersion`/`type` missing from the config, unsupported `cniVersion` (code 1),
GC actually removing stale resources (9.1), DEL with a vanished netns (3.3),
same container with a second `CNI_IFNAME` (2.4), VERSION echoing the request,
`CNI_ARGS` with `IgnoreUnknown=1`, interface UP/MTU matching the result.

## What the suite touches in the config

Nothing plugin-specific. It only ever:

- **reads** the top-level `cniVersion` (to build the VERSION request and to
  validate result echo);
- **removes** `type` / `cniVersion`, or **replaces** `cniVersion`, for the
  spec-mandated-field error tests;
- **sets** `cni.dev/valid-attachments` for the GC request, as a runtime would;
- sends **malformed / non-object JSON** for the code-6 tests.

## Layout

```
conformance/           Ginkgo specs, one file per spec section; suite_test.go holds setup + helpers
internal/harness/      plugin exec, result/error/version parsing, netns helpers
hack/refcni/           minimal reference plugin + config for self-checking the suite
hack/configs/          configs for the upstream reference plugins (make test-matrix)
```

Coverage table (what is asserted):

| Section | Tests |
|---|---|
| §1 process contract | 1.1, 1.2, 1.3 |
| §2 ADD | 2.1, 2.2, 2.3, 2.4 (different-ifname half, advisory), 2.5 (each required var); exact `CNI_IFNAME` incl. 15-char names; **result vs. netns**: reported sandbox interfaces exist with the claimed MAC (MTU/UP advisory), reported IPs are assigned to the interface they point at, reported routes are in the routing table |
| §3 DEL | 3.1 (+ ADD after DEL succeeds), 3.2 (+ never-added variant), 3.3 (missing netns), 3.4, 3.5 (+ optional `CNI_NETNS`) |
| §4 CHECK | 4.8 only |
| §5 VERSION | 5.1, 5.2 (echo advisory); every advertised version serves ADD/DEL with a result in that version's schema |
| §6 errors | code 4 (via 2.5/3.5/4.8), code 6 (two variants), unknown `CNI_COMMAND`, nonexistent `CNI_NETNS`; advisory: code 1, missing mandated fields, `CNI_ARGS` `IgnoreUnknown=1` |
| §7 concurrency | 7.1: N parallel ADDs (no duplicate IP), N parallel DELs, mixed ADD/DEL across containers |
| §8 STATUS | 8.1 healthy path |
| §9 GC | 9.2 exit 0, 9.1 best-effort stale removal, valid attachment untouched |

## Self-check rule

If a test passes on `hack/refcni` but fails on a mature plugin (bridge,
Calico, …), suspect the test before the plugin, and revise it.
