.PHONY: all build test lint check web deploy clean

all: build test

build:
	go build ./...

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
	rm -rf web/public
