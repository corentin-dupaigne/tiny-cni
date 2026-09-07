GO      ?= go
GOFLAGS ?=

# Inputs (also picked up from the environment):
#   make CNI_PLUGIN=/opt/cni/bin/bridge CNI_CONFIG=./bridge.json
CNI_PLUGIN ?=
CNI_CONFIG ?=
CNI_PATH   ?=
CNI_TEST_STRICT      ?=
CNI_TEST_TIMEOUT     ?=
CNI_TEST_CONCURRENCY ?=
FOCUS ?=                     # ginkgo focus regex, e.g. FOCUS='§2|§3'
LABEL_FILTER ?=              # ginkgo label filter, e.g. LABEL_FILTER='MUST && !optional'

# Namespace tests need root; re-exec through sudo when we are not root.
SUDO := $(shell [ "$$(id -u)" = 0 ] || command -v sudo)

TEST_ENV = CNI_PLUGIN=$(abspath $(CNI_PLUGIN)) CNI_CONFIG=$(abspath $(CNI_CONFIG)) \
           CNI_PATH=$(CNI_PATH) CNI_TEST_STRICT=$(CNI_TEST_STRICT) \
           CNI_TEST_TIMEOUT=$(CNI_TEST_TIMEOUT) CNI_TEST_CONCURRENCY=$(CNI_TEST_CONCURRENCY) \
           PATH=$(PATH) HOME=$(HOME) GOPATH=$(shell $(GO) env GOPATH) GOCACHE=$(shell $(GO) env GOCACHE) GOFLAGS=$(GOFLAGS)
COLOR ?= 1                   # COLOR=0 disables Ginkgo's ANSI colours (used for the matrix logs)
TEST_CMD = $(GO) test -count=1 -v ./conformance/ -ginkgo.v $(if $(filter 0,$(COLOR)),-ginkgo.no-color) \
           $(if $(FOCUS),-ginkgo.focus '$(FOCUS)') $(if $(LABEL_FILTER),-ginkgo.label-filter '$(LABEL_FILTER)')

# Upstream reference plugins (bridge, ptp, host-local, ...) for test-matrix.
CNI_PLUGINS_VERSION ?= v1.6.2
CNI_PLUGINS_ARCH    ?= $(shell $(GO) env GOARCH)
CNI_PLUGINS_URL      = https://github.com/containernetworking/plugins/releases/download/$(CNI_PLUGINS_VERSION)/cni-plugins-linux-$(CNI_PLUGINS_ARCH)-$(CNI_PLUGINS_VERSION).tgz

.PHONY: all test test-ref test-matrix refcni ref-plugins install-plugins build vet unit clean clean-host help

## Default: run the conformance suite against CNI_PLUGIN / CNI_CONFIG.
all: test

test:
	@test -n "$(CNI_PLUGIN)" || { echo "usage: make CNI_PLUGIN=/path/to/plugin CNI_CONFIG=/path/to/config.json"; exit 2; }
	@test -n "$(CNI_CONFIG)" || { echo "usage: make CNI_PLUGIN=/path/to/plugin CNI_CONFIG=/path/to/config.json"; exit 2; }
	$(SUDO) env $(TEST_ENV) $(TEST_CMD)

## Self-check against the bundled minimal plugin.
test-ref: refcni
	$(MAKE) test CNI_PLUGIN=$(CURDIR)/bin/refcni CNI_CONFIG=$(CURDIR)/hack/refcni/config.json

refcni:
	$(GO) build -o bin/refcni ./hack/refcni

## Download the upstream reference plugins into bin/ref (idempotent).
ref-plugins: bin/ref/bridge
bin/ref/bridge:
	mkdir -p bin/ref
	curl -fsSL $(CNI_PLUGINS_URL) | tar xz -C bin/ref
	@echo "installed $(CNI_PLUGINS_VERSION) reference plugins into bin/ref:"; ls bin/ref

## Everything the suite can run against: reference plugins + refcni.
install-plugins: ref-plugins refcni

build:
	$(GO) build $(GOFLAGS) ./...

vet:
	$(GO) vet ./...

## Harness unit tests (no root, no plugin).
unit:
	$(GO) test ./internal/...

clean:
	rm -rf bin

help:
	@echo "make CNI_PLUGIN=/path/to/plugin CNI_CONFIG=/path/to/config.json   run the suite (sudo is used if needed)"
	@echo "  optional: CNI_PATH= CNI_TEST_STRICT=1 CNI_TEST_TIMEOUT=60s CNI_TEST_CONCURRENCY=16"
	@echo "            FOCUS='§2|§3'  LABEL_FILTER='MUST && !optional'   (labels: MUST SHOULD optional netns)"
	@echo "make install-plugins  download reference plugins ($(CNI_PLUGINS_VERSION)) into bin/ref and build refcni"
	@echo "make test-matrix  run every hack/configs/*.json against bin/ref (installs plugins if missing)"
	@echo "make test-ref     self-check with hack/refcni"
	@echo "make unit         harness unit tests"
	@echo "make vet build    lint / compile"

## Run the suite over every reference plugin in bin/ref with hack/configs/*.json.
## Needs bin/ref (see README) and creates the dummy master link used by
## macvlan/ipvlan/vlan. Continues past failures; prints a summary at the end.
MATRIX ?= bridge ptp dummy macvlan ipvlan vlan bridge-0.4.0 bridge-static
test-matrix: ref-plugins
	@$(SUDO) sh -c 'ip link show cni-master0 >/dev/null 2>&1 || { ip link add cni-master0 type dummy && ip link set cni-master0 up; }'
	@rc=0; for c in $(MATRIX); do \
	  p=$${c%%-*}; echo "=== $$c (plugin $$p) ==="; \
	  if $(MAKE) --no-print-directory test COLOR=0 CNI_PLUGIN=$(CURDIR)/bin/ref/$$p CNI_CONFIG=$(CURDIR)/hack/configs/$$c.json > bin/matrix-$$c.log 2>&1; \
	  then echo "PASS $$c"; else echo "FAIL $$c (see bin/matrix-$$c.log)"; rc=1; fi; \
	  grep -F "Passed |" bin/matrix-$$c.log | sed 's/^/     /' || true; \
	  grep -F "[FAIL]" bin/matrix-$$c.log | grep -v "^\s*/" | sort -u | sed 's/^/     /' || true; \
	done; exit $$rc

## Remove the state the matrix leaves behind.
clean-host:
	$(SUDO) sh -c 'for f in /var/run/netns/cnitest-*; do [ -e "$$f" ] && umount "$$f" 2>/dev/null; rm -f "$$f"; done; \
	  ip link del cni-master0 2>/dev/null; ip link del cni-conf0 2>/dev/null; \
	  ip link del tcni-bridge 2>/dev/null; \
	  rm -rf /run/cni-conformance-ipam /run/cni-conformance-refcni /run/cni-conformance-tinycni.json; true'
