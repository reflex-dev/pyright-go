# Convenience targets for the Go port. See README.md.
#
# The bridge targets run pyright's own TypeScript tests against the Go
# implementation, so they need the 1.1.412 reference sources and an esbuild
# binary. Override REF, ESBUILD and REPO on the command line as needed.

REPO ?= $(realpath ..)
REF ?= /tmp/pyright-ref/packages/pyright-internal/src
ESBUILD ?= $(shell ls -d $$(npm root 2>/dev/null)/@esbuild/*/bin/esbuild 2>/dev/null | head -1)
SERVER := $(CURDIR)/bin/tokenserver

.PHONY: all build test fmt vet ref bridge-types bridge-types-full bridge-types-oracle bridge-evaluator-tests bridge-evaluator-oracle bridge-tests bridge-parser-tests bridge-typeprinter-tests bridge-pathutils-tests bridge-uri-tests bridge-importresolver-tests bridge-symbolnameutils-tests bridge-typecacheutils-tests bridge-ast bridge-parsetreeutils bridge-corpus bridge-binder bridge-binder-typeshed bridge-binder-oracle bridge-config bridge-config-oracle bridge clean generate

all: fmt vet test

build:
	go build ./...

# The bridge server is rebuilt whenever any Go source changes; without the
# prerequisite make would keep an out-of-date binary and the bridge targets
# would silently test the previous port.
$(SERVER): $(shell find . -name '*.go') go.mod
	go build -o $(SERVER) ./cmd/tokenserver

test:
	go test ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# Extract the 1.1.412 sources the port was transliterated from, plus the
# bundled typeshed stdlib stubs, which the binder differential also runs over.
ref:
	@mkdir -p $(dir $(REF))/../../..
	git -C $(REPO) archive 1.1.412 packages/pyright-internal/src | tar -x -C $(dir $(REF))../../..
	git -C $(REPO) archive 1.1.412 packages/pyright-internal/typeshed-fallback | tar -x -C $(dir $(REF))../../..

# Run the unmodified TypeScript tests against the Go implementation.
bridge-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

# Run pyright's own parser tests against the Go parser.
bridge-parser-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test parser.test.ts

# Run pyright's own type printer tests against the Go type model and printer.
bridge-typeprinter-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test typePrinter.test.ts

# Run pyright's own pathUtils, Uri, import resolver, symbol name and type cache
# tests against the Go port.
bridge-pathutils-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test pathUtils.test.ts

bridge-uri-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test uri.test.ts

bridge-importresolver-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test importResolver.test.ts

bridge-symbolnameutils-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test symbolNameUtils.test.ts

bridge-typecacheutils-tests: $(SERVER)
	node tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test typeCacheUtils.test.ts

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

# Diff everything the binder produces -- scopes, symbols, declarations and the
# code-flow graph -- over the whole sample corpus, in both import modes.
# binder.ts has no directly bridgeable test either; see dump-binder.ts.
bridge-binder: $(SERVER)
	node --max-old-space-size=8192 tools/ts-bridge/compare-binder.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

# The same, over the bundled typeshed stdlib stubs, which are far heavier code
# than the samples.
bridge-binder-typeshed: $(SERVER)
	node --max-old-space-size=8192 tools/ts-bridge/compare-binder.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --dir $(REF)/../typeshed-fallback/stdlib

# Diff the whole ConfigOptions and the enumerated file list over every sample
# project, as a language server and as the command line. config.test.ts is not
# bridgeable -- it asserts on object identity and mutates in place -- so this
# stands in for it; see tools/ts-bridge/dump-config.ts.
bridge-config: $(SERVER)
	node tools/ts-bridge/compare-config.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

# The Stage D gate: pyright's own type evaluator and checker tests -- 1,279
# cases across nine files -- run unmodified against the Go port. See
# tools/ts-bridge/shim-evaluatorTestUtils.ts.
EVALUATOR_TESTS := typeEvaluator1.test.ts,typeEvaluator2.test.ts,typeEvaluator3.test.ts,typeEvaluator4.test.ts,typeEvaluator5.test.ts,typeEvaluator6.test.ts,typeEvaluator7.test.ts,typeEvaluator8.test.ts,checker.test.ts

bridge-evaluator-tests: $(SERVER)
	node --max-old-space-size=8192 tools/ts-bridge/run-ts-tests.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --test $(EVALUATOR_TESTS)

# The same suite, answered by the TypeScript evaluator but round-tripped through
# the same wire format. Validates the harness without needing a Go evaluator:
# anything the serialization loses shows up here, with no port in the picture.
bridge-evaluator-oracle:
	PYRIGHT_GO_BRIDGE_MODE=oracle node --max-old-space-size=8192 tools/ts-bridge/run-ts-tests.js --ref $(REF) --server /bin/true --esbuild $(ESBUILD) --test $(EVALUATOR_TESTS)

# Per-node type differential: the printed type of every name, diffed one name at
# a time. This is what localizes an evaluator failure to a single expression
# instead of an error count; see compare-types.js.
#
# The oracle builds a fresh Program per file and so runs at about three files a
# second -- ten minutes for the full corpus, which is too slow to sit in the
# edit loop. bridge-types takes a sample, and the frontier it reports is stable
# across sample sizes because it is a ranking rather than a count. Use
# bridge-types-full for the number that goes in a commit message.
TYPES_SAMPLE ?= 150

bridge-types: $(SERVER)
	node --max-old-space-size=8192 tools/ts-bridge/compare-types.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD) --limit $(TYPES_SAMPLE)

bridge-types-full: $(SERVER)
	node --max-old-space-size=8192 tools/ts-bridge/compare-types.js --ref $(REF) --server $(SERVER) --esbuild $(ESBUILD)

bridge-types-oracle:
	node --max-old-space-size=8192 tools/ts-bridge/compare-types.js --ref $(REF) --esbuild $(ESBUILD) --ts-only 1 --limit $(TYPES_SAMPLE)

bridge-config-oracle:
	node tools/ts-bridge/compare-config.js --ref $(REF) --esbuild $(ESBUILD) --ts-only 1

# Run the binder differential's TypeScript oracle on its own and report what it
# produced. Validates the harness without needing the Go binder.
bridge-binder-oracle:
	node --max-old-space-size=8192 tools/ts-bridge/compare-binder.js --ref $(REF) --esbuild $(ESBUILD) --ts-only 1

bridge: bridge-tests bridge-parser-tests bridge-typeprinter-tests bridge-pathutils-tests bridge-uri-tests bridge-importresolver-tests bridge-symbolnameutils-tests bridge-typecacheutils-tests bridge-corpus bridge-ast bridge-parsetreeutils bridge-binder bridge-binder-typeshed bridge-config

# Regenerate the two generated files from the TypeScript sources.
generate:
	node localization/gen/generate.js $(REF)/localization localization/localize_gen.go
	node parser/gen/generate_unicode.js $(REF)/parser/unicode.ts parser/unicode_gen.go
	python3 analyzer/gen/generate_parsetreewalker.py $(REF)/analyzer/parseTreeWalker.ts
	python3 analyzer/gen/generate_configoptions.py $(REF)/common
	gofmt -w localization/localize_gen.go parser/unicode_gen.go analyzer/parsetreewalker.go analyzer/configoptions_gen.go

clean:
	rm -rf bin
