---
status: Accepted
date: 2026-07-30
---

# IPAM Sequential Allocation Without IP Reuse

## Context

tiny-cni must implement IPAM to hand each pod a unique IP from the node's pod CIDR. IPAM state is persisted to a local file on each node, so each node allocates independently from its own per-node podCIDR.

There are two idiomatic approaches:

1. **Monotonic counter:** Store the next free IP in the file; assign it and increment on every allocation. Simple to implement and test. Because the counter only moves forward, freed IPs are never reclaimed; therefore once it reaches the end of the node's pod CIDR, the node is considered full even if most IPs are unused.

2. **Allocation set:** Store the set of currently-allocated IPs. On ADD, pick a free one; on DEL, remove it. More code and more test surface, but it reuses freed IPs and gives DEL real meaning (DEL being a mandatory operation of the CNI spec).

## Decision

Ship **solution 1 (monotonic counter)** for short term, and defer IP reuse to a future ADR.

The short term goal is to prove the CNI path end-to-end and answer to the CNI Contract requirements. The monotonic counter is the simplest allocator that makes this path correct and testable, letting the initial milestone focus on the CNI contract rather than optimizing each step of the contract.

## Consequences

- The limit is ~254 **lifetime** allocations per node (cidr 24 by default), not concurrent pods: each pod creation advances the counter permanently and can exhaust the podCIDR over time.
- DEL succeeds but reclaims nothing reusable.
- Not suitable for large or long-lived production nodes; acceptable for a small-scale, spec-demonstration plugin.
