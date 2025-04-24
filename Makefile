.PHONY: fmt lint test

APP_NAME = excalibur
MAIN_DIR := cmd/$(APP_NAME)
BUILD_DIR := build

fmt:
	@gofumpt -w .
	@golines --ignore-generated --chain-split-dots --reformat-tags --shorten-comments --max-len 120 -w .
	@gci write -s standard -s default -s "prefix(excalibur)" .

lint:
	@golangci-lint run -c .golangci.yaml --fix ./...

test:
	@TZ=UTC go test -race ./...

clean:
	@rm -rf $(BUILD_DIR)
	@mkdir -p $(BUILD_DIR)

build-linux-64:
	@mkdir -p $(BUILD_DIR)/linux/64
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/linux/64/$(APP_NAME) -ldflags="-s -w" -trimpath $(MAIN_DIR)/main.go
