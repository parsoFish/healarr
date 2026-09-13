VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT  ?= $(shell git rev-parse --short HEAD)
LDFLAGS := -s -w -X github.com/parsoFish/healarr/internal/version.Version=$(VERSION) -X github.com/parsoFish/healarr/internal/version.Commit=$(COMMIT)

.PHONY: build build-pi build-nas test lint

build:
	go build -ldflags '$(LDFLAGS)' -o dist/healarr ./cmd/healarr

build-pi:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o dist/healarr-linux-arm64 ./cmd/healarr

build-nas:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o dist/healarr-linux-amd64 ./cmd/healarr

test:
	go test ./... -race -cover

lint:
	go vet ./...
	golangci-lint run ./...
