all: build test lint

build:
	go build cmd/prometheus-gitlab-notifier/...

test-release:
	goreleaser --snapshot --skip-publish --clean

test:
	go test -v ./...

lint:
	golangci-lint run
