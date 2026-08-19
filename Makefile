include common-go.mk

ROOT_DIR := $(dir $(realpath $(firstword $(MAKEFILE_LIST))))

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"


build:  ## Build binary
	go build $(LDFLAGS) -o build/musickit ./cmd/musickit


install:  ## Install musickit to $GOBIN (or $GOPATH/bin)
	go install $(LDFLAGS) ./cmd/musickit


bench:  ## Run benchmarks with memory stats
	go test -bench=. -benchmem -run=^$$ -count=1 ./...


bench-save:  ## Save benchmark baseline to bench.txt
	go test -bench=. -benchmem -run=^$$ -count=6 ./... | tee bench.txt


bench-compare:  ## Compare benchmarks against saved baseline (bench.txt)
	((test -z "$$FORCE_UPDATE" && which benchstat) || go install golang.org/x/perf/cmd/benchstat@latest) > /dev/null
	go test -bench=. -benchmem -run=^$$ -count=6 ./... > bench-new.txt
	$$(go env GOPATH)/bin/benchstat bench.txt bench-new.txt
	rm -f bench-new.txt


ci:  codefix format lint test  ## Run codefix, format, lint and tests


deploy:  ## Deploy the project
	echo "not implemented" && false


# Dry run on purpose: the wet one writes a playlist to a real Apple Music
# library, which is not something `make run` should do by surprise.
run:  ## Run the application locally (dry-run import of the sample list)
	go run ./cmd/musickit import --dry-run --file summer-playlist-2026.txt --playlist "Summer 2026"


test:  ## Run unit tests with coverage and race detection
	go test -race -cover -coverprofile cover.out ./...
	test ! -s .covignore || { grep -v -E -f .covignore cover.out > cover.out.tmp && mv cover.out.tmp cover.out; }
	go tool cover -func cover.out


vulncheck:  ## Scan dependencies for known vulnerabilities (needs network)
	((test -z "$$FORCE_UPDATE" && which govulncheck) || go install golang.org/x/vuln/cmd/govulncheck@latest) > /dev/null
	# Everything in this module ships in the one binary, so nothing is scoped out.
	$$(go env GOPATH)/bin/govulncheck ./...


.SILENT: bench bench-compare bench-save build deploy install run test vulncheck
.PHONY: bench bench-compare bench-save build deploy install run test vulncheck
