# Minimum acceptable total statement coverage, enforced by `make cover-check` (and CI).
COVERAGE_THRESHOLD := 70

# Targets built by `make dist`, as GOOS/GOARCH pairs (amd64 is 64-bit x86).
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build dist test cover cover-html cover-check

build:
	go build -o claude-status .

dist:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building dist/claude-status-$$os-$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -o dist/claude-status-$$os-$$arch . || exit 1; \
	done

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
