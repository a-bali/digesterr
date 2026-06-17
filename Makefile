.PHONY: all build build-web generate test test-web lint run clean

DB_PATH ?= digesterr.db
PORT ?= 8080
JWT_SECRET ?= dev-secret-change-in-production

all: generate build-web build

generate:
	sqlc generate

build-web:
	cd web && pnpm install && pnpm build
	rm -rf cmd/digesterr/static
	cp -r web/dist cmd/digesterr/static

build: build-web
	go build -o bin/digesterr ./cmd/digesterr

run: build
	JWT_SECRET=$(JWT_SECRET) DATABASE_PATH=$(DB_PATH) PORT=$(PORT) ./bin/digesterr

test:
	go test ./... -v

test-web:
	cd web && pnpm test

lint:
	go vet ./...

clean:
	rm -f bin/digesterr $(DB_PATH)
	rm -rf cmd/digesterr/static
	mkdir -p cmd/digesterr/static
	@echo 'placeholder' > cmd/digesterr/static/.gitkeep