.DEFAULT_GOAL = build

# Get all dependencies
setup:
	go mod download
.PHONY: setup

# Build tb
build:
	go build
.PHONY: build

# Run the linter
lint:
	./bin/golangci-lint run ./...
.PHONY: lint

# Remove version of tb installed with go install
go-uninstall:
	rm $(shell go env GOPATH)/bin/gehen
.PHONY: go-uninstall
