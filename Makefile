dev:
	go run .

dev-env:
	docker compose -f local.docker-compose.yml build && docker compose -f local.docker-compose.yml up

test-env:
	docker compose -f test.docker-compose.yml down --volumes && docker compose -f test.docker-compose.yml build && docker compose -f test.docker-compose.yml up

dev-env-down:
	docker compose -f local.docker-compose.yml down --volumes

repair:
	docker compose -f local.docker-compose.yml --profile repair up repair

repair-build:
	docker compose -f local.docker-compose.yml --profile repair up --build repair

repair-local:
	cd cmd/repair && go run repair.go