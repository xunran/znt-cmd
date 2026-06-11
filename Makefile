.PHONY: fmt vet test test-race contract-check e2e-api-smoke e2e-deepseek-smoke e2e-postgres-release release-candidate run migrate-up migrate-status docker-build compose-up compose-down

fmt:
	gofmt -w ./cmd ./internal ./pkg

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test ./... -race -count=1

contract-check:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify_contracts.ps1

e2e-api-smoke:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_api_smoke.ps1

e2e-deepseek-smoke:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_deepseek_smoke.ps1

e2e-postgres-release:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_postgres_release.ps1

release-candidate:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release_candidate_check.ps1

run:
	go run ./cmd/clean-core-server

migrate-up:
	go run ./cmd/clean-core-server -config config.example.json -migration up -migration-dir migrations

migrate-status:
	go run ./cmd/clean-core-server -config config.example.json -migration status -migration-dir migrations

docker-build:
	docker build -t clean-core:local .

compose-up:
	docker compose up --build

compose-down:
	docker compose down -v
