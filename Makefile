.PHONY: all build generate test lint run clean

DB_PATH ?= digesterr.db
PORT ?= 8080
JWT_SECRET ?= dev-secret-change-in-production

all: generate build

generate:
	sqlc generate

build:
	go build -o bin/digesterr ./cmd/digesterr

run: build
	JWT_SECRET=$(JWT_SECRET) DATABASE_PATH=$(DB_PATH) PORT=$(PORT) ./bin/digesterr

test:
	go test ./... -v

lint:
	go vet ./...

clean:
	rm -f bin/digesterr $(DB_PATH)