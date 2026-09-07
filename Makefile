BINARY_NAME=tiny-cni
BUILD_DIR=bin
MAIN_PATH=./cmd/tiny-cni/

BLUE=\033[0;34m
NC=\033[0m

# gotestsum gives nicer output; fall back to plain go test when it isn't installed
GOTESTSUM := $(shell command -v gotestsum 2>/dev/null)

E2E_DIR=e2e
E2E_CONFIG=$(E2E_DIR)/hack/configs/tiny-cni.json

.PHONY: all build test test-e2e clean

all: build

build:
	@printf "$(BLUE)Building $(BINARY_NAME)...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

test:
	@printf "$(BLUE)Running tests with -race...$(NC)\n"
ifdef GOTESTSUM
	# -p 1: gotestsum streams results live, so parallel packages interleave
	@gotestsum --format testname -- -p 1 -race -cover ./...
else
	@go test -v -race -cover ./...
endif

# Runs the CNI conformance suite (e2e/, its own module) against the built binary.
# BUILD_DIR is handed over as CNI_PATH so the ipam delegate resolves to the same
# binary. Extra args pass through, e.g. make test-e2e E2E_ARGS="FOCUS='§2'".
test-e2e: build
	@printf "$(BLUE)Running e2e conformance suite...$(NC)\n"
	@$(MAKE) -C $(E2E_DIR) test \
		CNI_PLUGIN=$(abspath $(BUILD_DIR)/$(BINARY_NAME)) \
		CNI_CONFIG=$(abspath $(E2E_CONFIG)) \
		CNI_PATH=$(abspath $(BUILD_DIR)) \
		$(E2E_ARGS)

clean:
	@printf "$(BLUE)Cleaning...$(NC)"
	@rm -rf $(BUILD_DIR)

