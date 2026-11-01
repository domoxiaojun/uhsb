IMAGE ?= cftest:local
SINGBOX_VERSION ?= 1.14.4

.PHONY: test build up down logs pull

test:
	go test ./...

build:
	docker compose build

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f

image:
	docker build --build-arg SINGBOX_VERSION=$(SINGBOX_VERSION) -t $(IMAGE) .
