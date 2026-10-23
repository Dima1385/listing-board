.PHONY: test up

test:
	go test ./...

up:
	docker compose up --build
