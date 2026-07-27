BINARY_NAME=tiny-cni
BUILD_DIR=bin
MAIN_PATH=./cmd/tiny-cni/

BLUE=\033[0;34m
NC=\033[0m

.PHONY: all build test

all: build

build:
	@printf "$(BLUE)Building $(BINARY_NAME)...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

test:
	@printf "$(BLUE)Running tests with -race...$(NC)"
	@go test -v -race ./...

clean:
	@printf "$(BLUE)Cleaning...$(NC)"
	@rm -rf $(BUILD_DIR)

