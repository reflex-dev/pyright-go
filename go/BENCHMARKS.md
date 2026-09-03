# Benchmarks

The test subject is a ~3,150-file production Python codebase with its virtual
environment active, so every third-party import (a large web framework,
SQLAlchemy, their dependencies) resolves and is inferred — the workload a
real deployment sees. Machine: Linux, 16 logical CPUs, 60 GB RAM. Comparison
target: pyright 1.1.412 under Node with `NODE_OPTIONS=--max-old-space-size=8192`
(at Node's default 4 GB heap, pyright cannot complete this workload).

Both implementations produce **byte-identical diagnostics** in every
configuration measured here — 3,726 errors and 9,618 warnings agree
diagnostic-for-diagnostic on the whole project, 0 errors and 2,156 warnings
on the pre-commit subset. That equivalence is the point of the port;
PORTING.md documents how it is verified.

Wall-clock and peak RSS come from `wait4`'s `ru_maxrss`. Figures are single
runs and vary ±10% with thermal state; treat small deltas as noise.

## Whole project, config-driven

`pyright-go` from the project root, file list from the configuration:

| invocation | time | peak RSS |
| --- | --- | --- |
| pyright, single-threaded | 111 s | 5.3 GB |
| pyright `--threads` | **59 s** | multi-process (parent 1.7 GB) |
| pyright-go, single-threaded | 191 s | 7.9 GB |
| pyright-go `--threads 2` | 163 s | 19 GB |
| pyright-go `--threads 4` | 173 s | 26 GB |
| pyright-go `--threads 8` | 197 s | 31 GB |
| pyright-go `--threads 16` | exceeds this machine's memory | — |
| pyright-go `--threads 4 --cachedir`, cold | 163 s | 17 GB |
| pyright-go `--cachedir`, **nothing changed** | **5.9 s** | **0.6 GB** |
| pyright-go `--cachedir`, leaf file changed | 6.8 s | 0.7 GB |
| pyright-go `--cachedir`, hub file changed | 174 s | 12 GB |

Reading this honestly:

- **Uncached, pyright wins this workload.** The port's single-threaded check
  is ~1.7× slower than pyright's here, and its `--threads` does not scale on
  this project — each worker re-infers its own copy of the enormous shared
  framework closure, so added workers add memory (roughly 6 GB apiece)
  without adding speed, and 16 workers exceed a 60 GB machine. pyright's
  forked workers duplicate the same work but each brings an independent V8
  heap, which is why its `--threads` halves its time while the port's does
  not. The single-threaded gap is the open optimization front: the resolved
  third-party closure exercises inference paths where the port's constant
  factors have not yet been profiled.
- **The cache is the port's result.** A no-change run answers in ~6 seconds
  at half a gigabyte; a typical edit costs seconds more. That mode has no
  pyright equivalent — pyright re-checks everything, every run.
- **A changed file's cost is its reverse import closure.** The cache
  re-checks exactly the files that could see the change. A leaf file costs
  ~7 s; a file the whole project transitively imports costs a full recheck.
  On a densely-connected codebase, most edits sit near the leaf end; the
  hub row is the worst case, not the typical one.

## Pre-commit file lists

A pre-commit hook passes changed files as arguments — here the hook's full
scope, 1,779 files at `--level error`:

| invocation (1,779 files as argv) | time |
| --- | --- |
| pyright, single-threaded | 107 s |
| pyright-go `--threads 8 --cachedir`, cold | 133 s |
| pyright-go `--cachedir`, warm | **5.8 s** |

The recurring case — the run the hook repeats all day — is ~6 s against the
107 s it replaces. The first run pays for the cache; every later run
collects.

Files named on the command line become include file specs, and membership
queries consult them constantly. Wildcard-free specs are indexed by path
(see MatchFileSpecs in analyzer/configoptions_class.go), so large file lists
cost the same as a config-driven run; pyright scans all N specs per query
and pays for it at this scale (UPSTREAM-BUGS.md #18).

## Where the port's time goes

From `--stats`, single-threaded, whole project:

| phase | pyright-go |
| --- | --- |
| Find + read source files | 0.2 s |
| Tokenize | 0.4 s |
| Parse | 1.1 s |
| Resolve imports | 0.1 s |
| Bind | 0.7 s |
| **Check** | **155 s** |

The front end is ~2.5 s; type checking is everything. Optimization effort
anywhere else is wasted, and the cache wins precisely because it skips the
check phase for unchanged files while re-running the honest parts —
enumeration, content hashing, and import resolution against the live file
system (a new file shadowing a module invalidates correctly).

## Memory

The port trades memory for its architecture: ~8 GB single-threaded on this
workload against pyright's 5.3 GB, multiplying per worker under `--threads`.
`GOMEMLIMIT` bounds it — diagnostics are identical at every setting, time
degrades as the bound tightens — and the threaded mode raises GOGC on its
own unless GOGC or GOMEMLIMIT is set in the environment (a shared Go heap
needs the headroom that pyright's per-process V8 heaps get implicitly).
pyright has its own ceiling in the other direction: Node's default 4 GB heap
aborts on this workload, so it requires `NODE_OPTIONS=--max-old-space-size`
to run at all.

The cache's steady state needs almost nothing: warm runs peak well under a
gigabyte, because checking is skipped and only the front end runs.

## Recommended invocations

For CI and pre-commit — the recurring, mostly-unchanged case:

```bash
pyright-go --threads 4 --cachedir .pyright-cache
```

For a one-shot full check with no cache to reuse, prefer few workers and
expect pyright's own `--threads` to be faster on heavily-interconnected
projects; the port earns its keep from the second run onward.

## What these numbers do not say

This is batch analysis on one machine and one project; neither
implementation is measured as a language server. Ratios shift with project
shape: the port's front end is several times faster than pyright's, so
projects that lean on parsing and binding (stub-heavy, lighter inference)
tilt toward the port even uncached, while this project's dense third-party
inference tilts the uncached comparison toward pyright. Checkers with
different architectures — interned types, no GC, inference shared across
threads — are in a different performance class by design, at the cost of
not being pyright.
