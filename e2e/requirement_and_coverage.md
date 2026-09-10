Requirements derived from the CNI specification v1.1.0: https://www.cni.dev/docs/spec/

## Purpose & design

This e2e test suite validates whether a CNI plugin **binary** conforms to the CNI
specification. It is **implementation-agnostic**:

- It runs against a **plugin binary path** provided by the user, so the same suite can
  validate any CNI implementation and be used to compare implementations.
- It treats the user-provided **plugin config as an opaque blob**: it forwards the config
  to the plugin's stdin unmodified and does **not** parse or manipulate plugin-specific
  fields. (Exception: the suite MAY supply its own *universally-invalid* input — e.g.
  malformed JSON — to test error handling, because such input is invalid for every plugin
  and therefore not implementation-specific. See "Config-error testing boundary" below.)
- Because the assertions target only spec-defined, observable behavior, the suite doubles
  as a check on its own correctness: **if a test passes on a minimal plugin (e.g. tiny-cni)
  but fails on a mature plugin (e.g. Calico), the test almost certainly does not correctly
  reflect the spec** and should be revised.

The suite uses helper packages from the official `containernetworking/plugins` repo
(`pkg/ns` for namespace entry, `pkg/testutils` for namespace creation) to reduce the risk
of bugs in test scaffolding and keep the suite code light.

## How requirements are classified

Each requirement is tagged with:

- **Level** — RFC 2119 keyword from the spec: **MUST** / **SHOULD** / **MAY**.
- **Coverage** — one of:
  - **TESTED** — the neutral suite can and does verify it.
  - **PARTIAL** — only part of the requirement is neutrally testable; the boundary is noted.
  - **OUT OF SCOPE** — cannot be tested by a neutral single-binary harness; reason given.

The mandatory bar for the suite is the set of **MUST + TESTED** requirements. SHOULD/MAY
requirements are advisory. OUT OF SCOPE requirements are documented for completeness and
honesty about coverage, not tested.

Reasons a requirement is OUT OF SCOPE fall into three categories. All three describe
requirements that **are** the plugin's responsibility but that a neutral harness cannot
verify:

- **(C1) Plugin-specific config** — testing it would require knowing the plugin's own
  config schema or validation rules, violating config-agnosticity.
- **(C2) Requires runtime / chain / delegation** — the behavior only manifests within a
  plugin chain, via delegated plugins, or under a full runtime the neutral harness does
  not provide.
- **(C3) Hard-to-create condition** — the trigger is fragile, timing-dependent, or
  environment-specific.

Requirements that constrain the **runtime** rather than the plugin are not plugin-conformance
requirements at all, so they do not appear in the matrix below. They are listed separately in
the "Runtime-only requirements" section at the end, purely to prevent them from being mistaken
for plugin requirements and tested by accident.

---

## 1. Process contract (all operations)

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 1.1 | On success, the plugin exits with return code **0**. | MUST | TESTED |
| 1.2 | On failure, the plugin exits with a **non-zero** return code. | MUST | TESTED |
| 1.3 | On failure, the plugin **should** output an error result structure (see §6) on stdout. | SHOULD | TESTED |
| 1.4 | stderr may carry unstructured output (logs); it is not part of the structured contract. | MAY | OUT OF SCOPE — advisory only; there is no required behavior to assert. |
| 1.5 | The runtime provides config as JSON on **stdin**; parameters via **environment variables**; the plugin returns result on **stdout**. | MUST | TESTED (exercised by every operation test) |

**Note on 1.3:** the error *JSON* is spec-level SHOULD, while the non-zero *exit* (1.2) is
MUST. The suite asserts both but treats a missing/invalid error JSON as a weaker failure
than a wrong exit code.

---

## 2. ADD operation

Required env: `CNI_COMMAND`, `CNI_CONTAINERID`, `CNI_NETNS`, `CNI_IFNAME`.
Optional env: `CNI_ARGS`, `CNI_PATH`.

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 2.1 | On ADD, the plugin creates the interface named by `CNI_IFNAME` inside the netns at `CNI_NETNS`, **or** adjusts that interface's configuration. | MUST | TESTED — after ADD, assert an interface named `CNI_IFNAME` exists in the target netns (via netlink, entering the ns). |
| 2.2 | On success, the plugin outputs a valid **result structure** on stdout (schema in §5). | MUST | TESTED — parse stdout, validate against the result schema. |
| 2.3 | If an interface of the requested name **already exists** in the container, the plugin MUST return an error. | MUST | TESTED — ADD once, then ADD again with the same `CNI_IFNAME` into the same netns; expect an error (non-zero exit). |
| 2.4 | A given container ID may be added to a network more than once **only** if each addition uses a **different interface name**. | MUST | PARTIAL — the "same (containerID, ifname) added twice must error" half is testable (see 2.3). The "different ifname is allowed" half is testable (ADD with ifname A then ifname B → both succeed). The runtime-side "should not call ADD twice" rule is a runtime-only requirement (see final section). |
| 2.5 | If missing/invalid required env var, the plugin returns an error with **code 4**, and the message must contain the names of the invalid variables. | MUST | TESTED — invoke ADD omitting each required env var in turn; expect code 4 and the variable name in `msg`/`details`. |
| 2.6 | If supplied a `prevResult`, the plugin MUST handle it (pass through or modify) and MUST output it (with any modifications) as its result. | MUST | OUT OF SCOPE (C2 — requires a chain to supply a meaningful prevResult; applies only to chained plugins). |

---

## 3. DEL operation

Required env: `CNI_COMMAND`, `CNI_CONTAINERID`, `CNI_IFNAME`.
Optional env: **`CNI_NETNS`**, `CNI_ARGS`, `CNI_PATH`.
(Note the asymmetry: `CNI_NETNS` is **required for ADD** but **optional for DEL**.)

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 3.1 | On DEL, the plugin deletes the interface named by `CNI_IFNAME` in the netns at `CNI_NETNS`, **or** undoes the modifications its ADD applied. | MUST | TESTED — ADD, then DEL, then assert the interface no longer exists in the netns. |
| 3.2 | The plugin MUST accept **multiple DEL calls** for the same (`CNI_CONTAINERID`, `CNI_IFNAME`) and return **success** if the interface / modifications are already missing (idempotency). | MUST | TESTED — ADD, DEL, DEL again; second DEL must exit 0. |
| 3.3 | A DEL **should** complete without error even if resources are missing (e.g. the netns no longer exists). | SHOULD | PARTIAL — testable for "already-deleted interface" and "missing netns" (delete the netns, then DEL, expect success). Resource-specific cases (DHCP lease release, etc.) are C1/C2. |
| 3.4 | No stdout result structure is required on DEL success. | — | TESTED — assert DEL success produces no result JSON (empty stdout is acceptable). |
| 3.5 | Required-env validation (code 4) applies as in ADD, for DEL's required set. | MUST | TESTED — omit `CNI_CONTAINERID`/`CNI_IFNAME`; expect code 4. Omitting `CNI_NETNS` must **not** error (it is optional for DEL). |

---

## 4. CHECK operation

Required env: `CNI_COMMAND`, `CNI_CONTAINERID`, `CNI_NETNS`, `CNI_IFNAME`.
All params except `CNI_PATH` must match the corresponding ADD.

CHECK is heavily prevResult- and chain-dependent, so most of it is OUT OF SCOPE for a
neutral single-plugin harness. Documented for completeness.

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 4.1 | The plugin must consult `prevResult` to determine expected interfaces/addresses. | MUST | OUT OF SCOPE (C2 — requires a prevResult from a prior ADD/chain to supply). |
| 4.2 | The plugin must allow a later chained plugin to have modified resources on ADD. | MUST | OUT OF SCOPE (C2 — chain-dependent). |
| 4.3 | The plugin should return an error if a Result-type resource it created (interface/address/route), listed in prevResult, is missing or invalid. | SHOULD | OUT OF SCOPE (C2 — requires prevResult). |
| 4.4 | The plugin should return an error if non-Result resources (firewall rules, traffic shaping, IP reservations, external daemons) are missing/invalid. | SHOULD | OUT OF SCOPE (C1/C3 — plugin-specific resources, hard to create). |
| 4.5 | The plugin should return an error if it knows the container is generally unreachable. | SHOULD | OUT OF SCOPE (C3 — hard to force this condition neutrally). |
| 4.6 | The plugin must handle CHECK called immediately after ADD (allow convergence delay). | MUST | OUT OF SCOPE (C3 — timing-dependent; also needs prevResult). |
| 4.7 | The plugin should call CHECK on delegated plugins and propagate errors. | SHOULD | OUT OF SCOPE (C2 — delegation). |
| 4.8 | Required-env validation (code 4) for CHECK's required set. | MUST | TESTED — if the plugin implements CHECK, omitting a required var should yield code 4. (Skip gracefully if the plugin declines CHECK.) |

**Scope note:** whether a standalone, non-chained plugin must implement CHECK at all is
plugin-dependent (a plugin may legitimately provide a minimal/no-op CHECK). The suite
should not fail a plugin merely for a minimal CHECK; only 4.8 is neutrally assertable.

---

## 5. VERSION operation

Required env: `CNI_COMMAND`. Input: a JSON on stdin containing `cniVersion`.

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 5.1 | On VERSION, the plugin outputs a JSON version result on stdout. | MUST | TESTED — invoke with `CNI_COMMAND=VERSION`; assert valid JSON on stdout, exit 0. |
| 5.2 | The version result contains `cniVersion` (echoing input) and `supportedVersions` (a list of supported spec versions). | MUST | TESTED — assert both keys present; `supportedVersions` is a non-empty list of version strings. |

(Version *selection* — the runtime picking the highest mutually-supported version — is a
runtime requirement, not the plugin's; see "Runtime-only requirements". The plugin's only
obligation is to *report* its versions, covered by 5.2.)

---

## 6. Error result structure

On error, the plugin should output a JSON object with:

- `cniVersion` (string) — protocol version in use.
- `code` (uint) — numeric error code (see table).
- `msg` (string) — short characterization.
- `details` (string) — longer description.

Reserved error codes (0–99 well-known; 100+ free for plugin-specific):

| Code | Meaning | Testable neutrally? |
|------|---------|---------------------|
| 1 | Incompatible CNI version | PARTIAL — feeding a config/VERSION request for a version the plugin does not support could trigger this, but which versions a plugin supports is discovered via VERSION (5.2), and constructing an unsupported-version request edges toward config manipulation. Test only if a clearly-unsupported version can be requested without plugin-specific config knowledge. |
| 2 | Unsupported field in config (msg must contain the key & value) | OUT OF SCOPE (C1 — which fields are unsupported is plugin-specific). |
| 3 | Container unknown / does not exist | OUT OF SCOPE (C1/C3 — depends on plugin's notion of container existence). |
| 4 | Invalid required env vars (msg must name the invalid variables) | TESTED — see 2.5, 3.5. This is the primary neutrally-testable error code. |
| 5 | I/O failure (e.g. failed to read config from stdin) | OUT OF SCOPE (C3 — forcing a stdin I/O failure is fragile/environment-specific). |
| 6 | Failed to decode (e.g. malformed JSON config, bad version string) | TESTED — feed **syntactically invalid JSON** as config; expect a non-zero exit and (SHOULD) code 6. Neutral because malformed JSON is invalid for *every* plugin (see boundary note). |
| 7 | Invalid network config (validation failure, e.g. subnet too small) | OUT OF SCOPE (C1 — "invalid" is plugin-defined; triggering it requires knowing the plugin's validation rules). |
| 11 | Try again later (transient condition) | OUT OF SCOPE (C3 — plugin-internal transient state, not forceable neutrally). |
| 50 | (STATUS) plugin not available | OUT OF SCOPE (C3 — requires forcing plugin-unavailability). |
| 51 | (STATUS) not available; existing containers may have limited connectivity | OUT OF SCOPE (C3). |

### Config-error testing boundary

The opaque-blob principle forbids the suite from manipulating the user's **plugin-specific**
config. It does **not** forbid supplying **universally-invalid** input, because such input
is invalid for every conformant plugin and therefore reveals nothing plugin-specific:

- **Neutrally testable:** malformed JSON (code 6) — no plugin accepts non-JSON. Missing a
  **spec-mandated** field (`type`, `cniVersion`) — required of all plugins by the spec.
- **NOT neutrally testable:** code 7 (semantic validity is plugin-defined), code 2
  (unsupported-field set is plugin-defined). Testing these would require the suite to
  understand the plugin's schema, breaking agnosticity.

If config-error coverage for codes 2/7 is desired, the *user* could supply known-invalid
configs for their plugin as additional inputs (the suite still forwards them opaquely). This
keeps the suite from encoding any plugin's validation rules, at the cost of extra user input.

---

## 7. Concurrency (Lifecycle & Ordering)

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 7.1 | Plugins MUST handle being executed **concurrently across different containers**, implementing locking on shared resources (e.g. IPAM databases) if necessary. | MUST | TESTED — fire N concurrent ADDs for **distinct** container IDs (each with its own netns); assert all succeed and no two receive the same IP / conflicting state. |

(The complementary rule — the runtime must *not* invoke parallel operations for the *same*
container — is a runtime requirement; see "Runtime-only requirements". The plugin is
therefore not required to handle same-container concurrency, which is why 7.1 tests only
*distinct*-container concurrency.)

**7.1 is a high-value neutral test:** correct plugins serialize shared-state access; a plugin
lacking locking will double-allocate under this test. It is fully implementation-agnostic
(it asserts only "no two containers got the same IP," observable from the results).

---

## 8. STATUS operation

Required env: `CNI_COMMAND`. Optional: `CNI_PATH`.

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 8.1 | Exit 0 if ready to service ADD; non-zero + error output if not. | MUST (if implemented) | PARTIAL — the "exit 0 when ready" path is testable (invoke STATUS on a healthy plugin, expect 0). The "non-zero when unavailable" path is OUT OF SCOPE (C3 — forcing unavailability is plugin-specific). |
| 8.2 | STATUS is purely informational; a plugin MUST NOT rely on STATUS being called, and other operations must work regardless of STATUS. | MUST | OUT OF SCOPE (C3 — this constrains the plugin's internal assumptions; there is no observable behavior to assert. Partially implied by testing that ADD/DEL work without a prior STATUS call.) |
| 8.3 | If the plugin delegates (e.g. IPAM), it must STATUS the delegate and propagate errors. | MUST | OUT OF SCOPE (C2 — delegation). |

**Scope note:** STATUS is optional to implement for many plugins; the suite should skip
gracefully if the plugin does not support it rather than fail.

---

## 9. GC operation

Required env: `CNI_COMMAND`, `CNI_PATH`. Input includes `cni.dev/valid-attachments`.

| # | Requirement | Level | Coverage |
|---|-------------|-------|----------|
| 9.1 | GC removes resources for attachments **not** in the provided valid-attachments list. | SHOULD | PARTIAL — could be tested by ADDing two attachments, then GC with only one listed, and asserting the other's resources are gone. But asserting "resources gone" for arbitrary plugins edges toward plugin-specific state; observable IP-release is testable if the plugin exposes it. Treat as best-effort/optional. |
| 9.2 | GC should complete without error, removing as many resources as possible and reporting errors back. | SHOULD | PARTIAL — invoke GC, assert exit 0 on the happy path. |
| 9.3 | Plugins MUST forward GC to delegated plugins. | MUST | OUT OF SCOPE (C2 — delegation). |

(The rule that the runtime must not use GC as a substitute for DEL is a runtime requirement;
see "Runtime-only requirements".)

**Scope note:** GC is optional to implement; skip gracefully if unsupported.

---

## 10. Delegation (Section 4)

All delegation requirements are **OUT OF SCOPE (C2)** for the neutral suite: they only apply
when the plugin under test is configured to delegate (e.g. to an IPAM plugin), which requires
the suite to provide delegate binaries and know the plugin delegates — both implementation-
specific. Documented for completeness:

- A delegating plugin must invoke the delegate with the same env + config, via `CNI_PATH`.
- It must forward stderr, and on ADD failure of a delegate, run DEL before returning failure.
- On CHECK/DEL/GC it must also execute delegates and propagate their errors.

---

## Coverage summary

**Neutrally tested (the suite's mandatory bar):**
- Process contract: exit codes (1.1, 1.2), error JSON presence (1.3).
- ADD: interface creation (2.1), result schema (2.2), duplicate-ifname error (2.3),
  different-ifname allowed (2.4 partial), required-env / code 4 (2.5).
- DEL: interface removal (3.1), idempotency / multiple DEL (3.2), missing-resource success
  (3.3 partial), no-result-on-success (3.4), required-env / optional-netns (3.5).
- VERSION: output present (5.1), schema `cniVersion` + `supportedVersions` (5.2).
- Errors: code 4 (env), code 6 (malformed JSON), spec-mandated-field-missing.
- Concurrency: no double-allocation across containers (7.1).

**Out of scope (plugin requirements a neutral harness cannot verify), by reason:**
- **C1 (plugin-specific config):** codes 2, 7; unsupported-field and semantic-validation.
- **C2 (runtime/chain/delegation):** prevResult (2.6, 4.1–4.3, 4.7), CHECK's chain
  requirements, all of Section 10, GC/STATUS delegation forwarding.
- **C3 (hard-to-create condition):** codes 5, 11, 50, 51; CHECK convergence/unreachability;
  STATUS unavailability (8.2); stderr semantics (1.4, advisory).

**Not in the matrix (runtime requirements, not the plugin's contract):** version selection,
same-container serialization, GC-not-a-substitute-for-DEL, and other Section 3 runtime rules.
See below.

This document is a complete map of the spec's requirements against what a neutral,
single-binary conformance harness can verify. A passing run of this suite indicates
conformance to the **neutrally-testable subset** — not full spec compliance, since the
out-of-scope requirements (chaining, delegation, plugin-specific validation, runtime
behavior) are, by construction, not verifiable without a runtime or knowledge of the
plugin's implementation.

---

## Runtime-only requirements (not tested — not the plugin's contract)

The CNI spec interleaves requirements for the **runtime** with requirements for the
**plugin**. The matrix above covers only *plugin* requirements. The following constrain the
runtime and are therefore **not plugin-conformance requirements at all** — they are listed
here solely so they are not mistaken for plugin behavior and tested by accident.

- **Version selection** — the runtime MUST select the highest mutually-supported CNI version
  from the config's `cniVersion`/`cniVersions`. The plugin only *reports* its supported
  versions (tested via VERSION, §5.2).
- **Same-container serialization** — the runtime MUST NOT invoke parallel operations for the
  same container (it may parallelize across different containers). The plugin is thus only
  required to handle *cross-container* concurrency (§7.1).
- **ADD-not-called-twice** — the runtime should not call ADD twice for the same
  (containerID, ifname) without an intervening DEL.
- **GC is not a substitute for DEL** — the runtime MUST NOT use GC in place of DEL.
- **`disableCheck` / `disableGC`** — the runtime must not call CHECK/GC when these are set in
  the config.
- **Config aggregation & request derivation** — the runtime assembles plugin configs, injects
  `cniVersion`/`name`/`runtimeConfig`/`prevResult`, and passes unknown fields through
  unchanged (Section 3).
- **Namespace lifecycle** — the runtime creates the container netns before invoking plugins
  and is responsible for its cleanup.
- **Result persistence** — the runtime stores the final ADD result for later CHECK/DEL.

These are the responsibility of the container runtime (e.g. the kubelet / CRI), not of the
plugin under test, and cannot — and should not — be asserted by a plugin-conformance suite.