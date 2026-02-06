.PHONY: help tidy fmt vet test lint check

help:
	@echo "Targets:"
	@echo "  tidy   - go mod tidy"
	@echo "  fmt    - gofmt ./..."
	@echo "  vet    - go vet ./..."
	@echo "  test   - go test ./..."
	@echo "  lint   - golangci-lint run (if installed)"
	@echo "  check  - fmt + vet + test"

tidy:
	go mod tidy

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || \
	  (echo "golangci-lint not found; skip lint" && exit 0)

check: fmt vet test

