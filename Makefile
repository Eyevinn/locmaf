.PHONY: all build test lint check web deploy clean

LDFLAGS = -X github.com/Eyevinn/locmaf/internal.commitVersion=$$(git describe --tags --always HEAD) -X github.com/Eyevinn/locmaf/internal.commitDate=$$(git log -1 --format=%ct)

all: build test

build:
	go build ./...
	go build -ldflags "$(LDFLAGS)" -o out/locmaf ./cmd/locmaf

test:
	go test ./...

lint:
	golangci-lint run

check: lint test

web:
	cd web && npm run build

deploy: web
	./update_site.sh

clean:
	rm -rf web/public out
