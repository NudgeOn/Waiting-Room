export GOCACHE ?= $(CURDIR)/.cache/go-build
export GOMODCACHE ?= $(CURDIR)/.cache/go-mod

.PHONY: check test-unit check-docs lint-api test-contract fmt-check vet test-final

check: fmt-check vet test-unit check-docs lint-api test-contract test-admin-ui test-install-plan-schema

test-install-plan-schema:
	node --test test/installplan/*.test.mjs

test-admin-ui:
	npm run build:admin
	npm run test:admin-ui

preview:
	npm run build:admin
	go run ./cmd/wrctl preview

fmt-check:
	@test -z "$$(gofmt -l internal cmd)" || (gofmt -l internal cmd; exit 1)

vet:
	go vet ./...

test-unit:
	@case "$(PRD)" in \
	  "") go test -race -count=1 ./... && npm test ;; \
	  01) go test -race -count=1 ./internal/policy/... ;; \
	  02) go test -race -count=1 ./internal/queue/model/... ./internal/queue/valkeystore/... ;; \
	  03) go test -race -count=1 ./internal/lab/... ./internal/admission/... ./internal/waiting/... ;; \
	  04) go test -race -count=1 ./internal/adminauth/... ;; \
	  05) go test -race -count=1 ./internal/adminlab/... ./internal/processlab/... ./internal/configtrust/... ;; \
	  06) go test -race -count=1 ./internal/installplan/... ./internal/installer/... ./cmd/wrctl/... && node --test test/installplan/*.test.mjs ;; \
	  07|08) npm test ;; \
	  *) echo "NO-GO: unit suite for PRD=$(PRD) is not implemented"; exit 1 ;; \
	esac

test-integration:
	@test -n "$(WR_TEST_VALKEY)" || (echo 'NO-GO: WR_TEST_VALKEY is required'; exit 1)
	go test -p=1 -tags=integration -race -count=1 -v ./internal/queue/valkeystore/... ./internal/lab/...

test-local-beta:
	WR_TEST_LOCAL_BETA=local PLAYWRIGHT_BROWSERS_PATH=$(CURDIR)/.cache/ms-playwright node test/localbeta/run.mjs

lab-valkey:
	docker compose -f deploy/compose/lab.yaml up -d --wait

lab-auth-db:
	docker compose -f deploy/compose/auth-lab.yaml up -d --wait

admin-lab:
	npm run build:admin
	WR_TEST_AUTH_DB=local go run ./cmd/wr-admin-lab

auth-calibrate:
	go run ./cmd/wr-auth-calibrate

test-auth-db:
	@test "$(WR_TEST_AUTH_DB)" = "local" || (echo 'NO-GO: WR_TEST_AUTH_DB=local required (dedicated loopback auth lab)'; exit 1)
	go test -tags=integration -race -count=1 -v ./internal/adminauth/pgstore ./internal/adminauth/sessionhttp ./internal/adminauth/authhttp

lab:
	go run ./cmd/wr-lab

lab-quick:
	go run ./cmd/wr-lab -quick

process-lab:
	go run ./cmd/wr-process-lab

process-lab-quick:
	go run ./cmd/wr-process-lab --quick

test-processes:
	@test "$(WR_TEST_VALKEY)" = "127.0.0.1:16379" || (echo 'NO-GO: dedicated WR_TEST_VALKEY=127.0.0.1:16379 required'; exit 1)
	go test -tags=integration -race -count=1 -v ./internal/processlab

test-local-tiers:
	@test "$(WR_TEST_VALKEY)" = "127.0.0.1:16379" || (echo 'NO-GO: dedicated WR_TEST_VALKEY=127.0.0.1:16379 required'; exit 1)
	GOMAXPROCS=2 go test -tags=integration,tiers -race -count=1 -timeout=10m -v -run '^TestLocalVisitorTiers$$' ./internal/queue/valkeystore

test-http-tiers:
	@test "$(WR_TEST_VALKEY)" = "127.0.0.1:16379" || (echo 'NO-GO: dedicated WR_TEST_VALKEY=127.0.0.1:16379 required'; exit 1)
	GOMAXPROCS=2 go test -tags=integration,tiers -race -count=1 -timeout=5m -v -run '^TestProcessHTTPVisitorTiers$$' ./internal/processlab

test-fuzz-input:
	GOMAXPROCS=2 go test -run '^$$' -fuzz '^FuzzJoinTarget$$' -fuzztime=10000x -parallel=2 -timeout=45s ./internal/lab

test-fuzz-config:
	GOMAXPROCS=2 go test -run '^$$' -fuzz '^FuzzSignedSnapshot$$' -fuzztime=10000x -parallel=2 -timeout=45s ./internal/configtrust

test-fuzz-control:
	GOMAXPROCS=2 go test -run '^$$' -fuzz '^FuzzConfigDecode$$' -fuzztime=10000x -parallel=2 -timeout=45s ./internal/control

test-persistence:
	@test "$(WR_TEST_RESTART_CONTAINER)" = "waiting-room-m1-valkey-1" || (echo 'NO-GO: dedicated restart container required'; exit 1)
	go test -tags=integration,persistence -race -count=1 -v -run '^TestRestartPersistenceFailClosed$$' ./internal/queue/valkeystore

check-docs:
	node scripts/check-docs.mjs

lint-api:
	npm run lint:api

test-contract:
	node --test test/contract/*.test.mjs

test-browser:
	PLAYWRIGHT_BROWSERS_PATH=$(CURDIR)/.cache/ms-playwright npx playwright test

test-final:
	node scripts/check-docs.mjs --final
	@echo "NO-GO: final integration runner is not implemented"
	@exit 1
