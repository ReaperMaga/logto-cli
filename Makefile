VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
COMPOSE := docker compose -f docker-compose.test.yml

.PHONY: build build-all test test-integration lint clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/logto-cli ./cmd/logto-cli

# Same targets as the release workflow.
build-all:
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o bin/logto-cli_$${os}_$${arch}$$ext ./cmd/logto-cli || exit 1; \
	done

test:
	go test ./...

test-integration:
	$(COMPOSE) up -d --wait --wait-timeout 300
	go test -tags integration -count=1 -v ./test/integration/; status=$$?; $(COMPOSE) down -v; exit $$status

lint:
	go vet ./...
	go vet -tags integration ./...
	golangci-lint run

clean:
	rm -rf bin dist
