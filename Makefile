# Minimum acceptable total statement coverage, enforced by `make cover-check` (and CI).
COVERAGE_THRESHOLD := 70

.PHONY: build test cover cover-html cover-check

build:
	go build -o claude-status .

test:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

cover-html: cover
	go tool cover -html=coverage.out -o coverage.html

cover-check: cover
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {sub(/%/, "", $$3); print $$3}'); \
	awk -v t="$$total" -v min="$(COVERAGE_THRESHOLD)" 'BEGIN {exit !(t >= min)}' || \
		{ echo "FAIL: coverage $${total}% is below threshold $(COVERAGE_THRESHOLD)%"; exit 1; }; \
	echo "OK: coverage $${total}% meets threshold $(COVERAGE_THRESHOLD)%"
