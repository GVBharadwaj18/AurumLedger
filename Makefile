# AurumLedger Automation & Developer Ergonomics

.PHONY: help run test test-verbose docker-up docker-down demo simulate build clean

help:
	@echo "AurumLedger Developer Commands:"
	@echo "  make run          - Run AurumLedger API server locally (with embedded SQLite dev fallback)"
	@echo "  make test         - Run full automated test suite"
	@echo "  make test-verbose - Run full automated test suite with verbose log output"
	@echo "  make simulate     - Run interactive terminal simulator walkthrough"
	@echo "  make demo         - Run live end-to-end bash demo script"
	@echo "  make docker-up    - Spin up full stack (Postgres, Redis, RabbitMQ, AurumLedger)"
	@echo "  make docker-down  - Tear down Docker containers and prune networks"
	@echo "  make build        - Build production binary to ./bin/aurumledger-server"
	@echo "  make clean        - Remove build artifacts and temporary databases"

run:
	go build -mod=vendor -o server.exe ./cmd/server
	./server.exe

test:
	go test -mod=vendor ./tests/...

test-verbose:
	go test -v -mod=vendor ./tests/...

simulate:
	go run -mod=vendor cmd/simulator/main.go

demo:
	bash scripts/test_live.sh

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -mod=vendor -ldflags="-w -s" -o bin/aurumledger-server ./cmd/server

clean:
	rm -rf bin/ aurumledger.db* .tools/tmp/*

