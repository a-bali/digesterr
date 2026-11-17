.PHONY: all build build-linux build-web generate test test-web lint run clean \
       image release

IMAGE_REPO ?= ghcr.io/a-bali/digesterr
VERSION ?= $(shell git describe --tags --always --dirty)

all: generate build-web build

generate:
	sqlc generate

build-web:
	cd web && pnpm install && pnpm build
	cd web && node scripts/verify-classes.mjs
	rm -rf cmd/digesterr/static/*
	cp -r web/dist/. cmd/digesterr/static/

build: build-web
	go build -ldflags "-X digesterr/internal/httpapi.Version=$(VERSION)" -o bin/digesterr ./cmd/digesterr

build-linux: build-web
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-X digesterr/internal/httpapi.Version=$(VERSION)" -o bin/digesterr-linux-amd64 ./cmd/digesterr

run: build
	JWT_SECRET=$(JWT_SECRET) DATABASE_PATH=$(DB_PATH) PORT=$(PORT) ./bin/digesterr

test:
	go test ./... -v

test-web:
	cd web && pnpm test

lint:
	go vet ./...

clean:
	rm -f bin/digesterr $(DB_PATH) data/digesterr.db data/digesterr.db-shm data/digesterr.db-wal
	rm -rf cmd/digesterr/static
	mkdir -p cmd/digesterr/static
	@echo 'placeholder' > cmd/digesterr/static/.gitkeep

image: build-web
	VERSION=$(VERSION) \
	KO_DOCKER_REPO=$(IMAGE_REPO) \
	ko build ./cmd/digesterr \
	  --bare \
	  --tags "$(VERSION)" \
	  --image-user "1000:1000"
	@if echo "$(VERSION)" | grep -qE '^v[0-9]'; then \
	  crane tag "$(IMAGE_REPO):$(VERSION)" latest; \
	fi

release:
	@test -n "$(VERSION)" || (echo "VERSION not set (e.g. make release VERSION=v0.5.0)" >&2; exit 1)
	test -z "$$(git status --porcelain)" || (echo "uncommitted changes" >&2; exit 1)
	@test "$$(git rev-parse --abbrev-ref HEAD)" = "main" || (echo "must be on main" >&2; exit 1)
	@test -z "$$(git tag -l '$(VERSION)')" || (echo "tag $(VERSION) already exists" >&2; exit 1)
	@# Build and push the image BEFORE the tag is published. The reverse order
	@# leaves a pushed tag with no image behind it if the build fails, and any
	@# gitops bump would then reference a digest that does not exist.
	@# `image` ends with `crane tag ... latest`, which also proves the push
	@# landed: crane fails if the version tag is not in the registry.
	$(MAKE) image VERSION=$(VERSION)
	git tag -a "$(VERSION)" -m "$(VERSION)"
	git push origin "$(VERSION)"
	@echo "==> Release $(VERSION) built and pushed"
