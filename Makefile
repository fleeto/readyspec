.PHONY: test test-collection vet web-install web web-test verify-goss build build-all

test: web-test
	go test -race ./...

# Linux isolation is mandatory for this target; missing bubblewrap is a failure.
test-collection:
	READYSPEC_REQUIRE_COLLECTION_SANDBOX=1 go test ./internal/compiler -run 'Collect|Collector|Symlink' -count=1 -v

web-install:
	npm --prefix web ci

web:
	npm --prefix web run build

web-test:
	npm --prefix web test

vet:
	go vet ./...

# Starting the Go CLI verifies both embedded payloads against pinned digests.
verify-goss:
	go run ./cmd/readyspec --version

build: web verify-goss
	CGO_ENABLED=0 go build -trimpath -o dist/readyspec ./cmd/readyspec

build-all: web verify-goss
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/readyspec-linux-amd64 ./cmd/readyspec
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dist/readyspec-linux-arm64 ./cmd/readyspec
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o dist/readyspec-windows-amd64.exe ./cmd/readyspec
