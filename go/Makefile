# Convenience targets for the Go port. See README.md.
#
# The bridge targets run pyright's own TypeScript tests against the Go
# implementation, so they need the 1.1.412 reference sources and an esbuild
# binary. Override REF, ESBUILD and REPO on the command line as needed.

REPO ?= $(realpath ..)
REF ?= /tmp/pyright-ref/packages/pyright-internal/src
ESBUILD ?= $(shell ls -d $$(npm root 2>/dev/null)/@esbuild/*/bin/esbuild 2>/dev/null | head -1)
SERVER := $(CURDIR)/bin/tokenserver

.PHONY: all build test fmt vet ref bridge-tests bridge-parser-tests bridge-typeprinter-tests bridge-ast bridge-parsetreeutils bridge-corpus bridge clean generate

all: fmt vet test

build:
	go build ./...

$(SERVER):
	go build -o $(SERVER) ./cmd/tokenserver

test:
	go test ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# Extract the 1.1.412 sources the port was transliterated from.
ref:
	@mkdir -p $(dir $(REF))/../../..
	git -C $(REPO) archive 1.1.412 packages/pyright-internal/src | tar -x -C $(dir $(REF))../../..

# Run the unmodified TypeScript tests against the Go implementation.
bridge-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

# Run pyright's own parser tests against the Go parser.
bridge-parser-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test parser.test.ts

# Run pyright's own type printer tests against the Go type model and printer.
bridge-typeprinter-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test typePrinter.test.ts

# Diff the Go and TypeScript parse trees over the whole sample corpus.
bridge-ast: $(SERVER)
	node tools/ts-bridge/compare-ast.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

# Diff every scope-independent parseTreeUtils function, for every node, over the
# whole sample corpus. Stands in for parseTreeUtils.test.ts, which needs the
# fourslash harness and so cannot be bridged.
bridge-parsetreeutils: $(SERVER)
	node --max-old-space-size=8192 tools/ts-bridge/compare-parsetreeutils.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

# Diff the Go and TypeScript tokenizers over the whole sample corpus.
bridge-corpus: $(SERVER)
	node tools/ts-bridge/compare-corpus.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

bridge: bridge-tests bridge-parser-tests bridge-typeprinter-tests bridge-corpus bridge-ast bridge-parsetreeutils

# Regenerate the two generated files from the TypeScript sources.
generate:
	node localization/gen/generate.js $(REF)/localization localization/localize_gen.go
	node parser/gen/generate_unicode.js $(REF)/parser/unicode.ts parser/unicode_gen.go
	python3 analyzer/gen/generate_parsetreewalker.py $(REF)/analyzer/parseTreeWalker.ts
	gofmt -w localization/localize_gen.go parser/unicode_gen.go analyzer/parsetreewalker.go

clean:
	rm -rf bin
