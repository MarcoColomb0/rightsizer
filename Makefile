VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test lint image demo-pdf

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o rightsizer ./cmd/rightsizer

test:
	go vet ./...
	go test -race ./...

lint:
	golangci-lint run ./...

image:
	docker build --build-arg VERSION=$(VERSION) -t rightsizer:local .

demo-pdf:
	RIGHTSIZER_DEMO_PDF=$(CURDIR)/demo.pdf go test -count=1 -run TestWritePDF ./internal/report/
