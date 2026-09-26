---
status: accepted
date: 2026-09-01
---

# IPAM Allocation Set With IP Reuse

Supersedes [ADR 0001](0001-ipam-sequential-allocation-without-ip-reuse.md).

## Context

ADR 0001 shipped a monotonic counter to prove the CNI path end-to-end. Once that milestone was reached, its known limitation became the main problem: every ADD permanently consumes an IP, so a node exhausts its `/24` after ~254 lifetime pod creations, even if only a handful of pods are running. On a live node with regular pod churn (rollouts, restarts, CronJobs) that limit is reached quickly. DEL also had no real effect, which is at odds with it being a mandatory CNI operation.

## Decision

Replace the counter with an **allocation set** (solution 2 of ADR 0001), persisted in the same `flock`-protected state file as two maps:

- `containerToIp`: container ID → IP. It makes ADD idempotent per container and lets DEL find the IP to release.
- `allocatedSet`: every IP in use, including the reserved network and gateway addresses.

On ADD, the allocator hands out `.2` to the first container and afterwards a free address adjacent to an already-allocated one. On DEL, the IP's key is deleted from both maps so the address can be reused.

## Consequences

- The limit becomes ~253 **concurrent** pods per node with the default `/24`, not lifetime allocations.
- DEL now reclaims the IP, giving it real meaning.
- The state file format changed, so state files written by the counter version are not compatible. This was acceptable because tiny-cni had no deployments to migrate yet.
- More code and test surface in the allocator, including a concurrency test for the `flock` path.
- Allocation scans existing IPs to find a free neighbour, which is O(n) per ADD. That is negligible at `/24` scale.
