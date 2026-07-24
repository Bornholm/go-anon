GORELEASER_ARGS ?= --snapshot --clean

GO_ANON_LATEST_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")

build: build-server build-anon-doc build-mappings

build-server:
	CGO_ENABLED=0 go build -o bin/server ./cmd/server

build-anon-doc:
	CGO_ENABLED=0 go build -o bin/anon-doc ./cmd/anon-doc

build-mappings:
	CGO_ENABLED=0 go build -o bin/mappings ./cmd/mappings

build-tools:
	CGO_ENABLED=0 go build -o bin/train        ./cmd/train
	CGO_ENABLED=0 go build -o bin/eval         ./cmd/eval
	CGO_ENABLED=0 go build -o bin/demo         ./cmd/demo
	CGO_ENABLED=0 go build -o bin/prune        ./cmd/prune
	CGO_ENABLED=0 go build -o bin/brown-cluster ./cmd/brown-cluster

test:
	go test ./...

release:
	goreleaser $(GORELEASER_ARGS)

include misc/*/*.mk
