.PHONY: build test coverage backend-coverage fe-build fe-test fe-coverage run dev tidy

tidy:
	cd backend && go mod tidy

test:
	cd backend && go test ./...

coverage: backend-coverage

backend-coverage:
	mkdir -p coverage
	cd backend && go test -race -covermode=atomic -coverpkg=./... -coverprofile=../coverage/backend.out ./...
	cd backend && go run github.com/boumenot/gocover-cobertura@v1.5.0 < ../coverage/backend.out > ../coverage/backend.xml
	./hack/coverage-gate.sh backend

# The UI lands in phase 5; these targets are wired now so the Makefile surface
# matches peeq's and CI does not need reshaping later.
fe-test:
	@echo "no ui/ yet — lands in phase 5"

fe-coverage:
	@echo "no ui/ yet — lands in phase 5"

fe-build:
	@echo "no ui/ yet — lands in phase 5"

build:
	cd backend && CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/trick77/mimostats/internal/version.Version=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o ../bin/mimostats ./cmd/mimostats

run:
	cd backend && go run ./cmd/mimostats

dev:
	./hack/dev.sh
