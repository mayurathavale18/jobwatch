.PHONY: build frontend test run-serve poll backfill test-notify clean

BINARY := jobwatch
CONFIG := config.yaml

frontend:
	cd frontend && npm install && npm run build

build: frontend
	go build -o bin/$(BINARY) ./cmd/jobwatch

test:
	go test ./...

run-serve: build
	./bin/$(BINARY) serve -config $(CONFIG)

poll: build
	./bin/$(BINARY) poll -config $(CONFIG)

backfill: build
	./bin/$(BINARY) backfill -config $(CONFIG)

test-notify: build
	./bin/$(BINARY) test-notify -config $(CONFIG)

clean:
	rm -rf bin
