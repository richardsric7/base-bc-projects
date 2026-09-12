.PHONY: run build vet lint test tidy-check ci

run:
	go run .

build:
	go build ./...

vet:
	go vet ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed. Install: https://golangci-lint.run/welcome/install/"; exit 1; }
	golangci-lint run --timeout=5m

test:
	go test ./... -race -coverprofile=coverage.out -covermode=atomic

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum \
		|| (echo "go.mod / go.sum not tidy; commit the changes above" && exit 1)

ci: tidy-check build vet lint test
	@echo "wallet-backend CI checks passed"
