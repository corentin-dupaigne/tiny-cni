BINARY_NAME=tiny-cni
BUILD_DIR=bin
MAIN_PATH=./cmd/tiny-cni/

BLUE=\033[0;34m
NC=\033[0m

.PHONY: all build test test-e2e list-e2e clean

all: build

build:
	@printf "$(BLUE)Building $(BINARY_NAME)...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

test:
	@printf "$(BLUE)Running tests with -race...$(NC)"
	@go test -v -race ./...

# needs root: creates network namespaces, a bridge and veth pairs
#
# RUN filters by test name (a regexp), so a single test can be run on its own:
#   make test-e2e RUN=TestPodsOnSameNodeExchangeTraffic
#   make test-e2e RUN=SameNode
test-e2e:
	@printf "$(BLUE)Running e2e tests (requires root)...$(NC)\n"
	@mkdir -p $(BUILD_DIR)
	@go test -c -tags e2e -o $(BUILD_DIR)/e2e.test ./test/e2e/
	@sudo $(BUILD_DIR)/e2e.test -test.v $(if $(RUN),-test.run '$(RUN)')

# the test names that can be passed to RUN
list-e2e:
	@mkdir -p $(BUILD_DIR)
	@go test -c -tags e2e -o $(BUILD_DIR)/e2e.test ./test/e2e/
	@$(BUILD_DIR)/e2e.test -test.list '.*'

clean:
	@printf "$(BLUE)Cleaning...$(NC)"
	@rm -rf $(BUILD_DIR)

