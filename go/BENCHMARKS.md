# Benchmarks

All numbers are measured against **pyright 1.1.412 under Node**, run from the
same working directory with the same command line and the same
`pyrightconfig.json`, on the same machine (Linux, 16 logical CPUs). Every
configuration below produces **identical diagnostics** to pyright in its
reference mode — that equivalence, not the speed, is the point of the port;
PORTING.md documents how it is verified.

Methodology: wall-clock and peak RSS are taken from `wait4`'s `ru_maxrss` on
the child process; multi-run figures are the median of 3. Single-run figures
vary ±10% run to run on this machine (thermal), so treat small deltas as
noise. The main test subject is a 3,138-file production codebase (10,192
errors, 31,697 warnings under pyright's default rule set); a few older
figures, marked as such, come from a 3,135-file revision of the same project.

## The headline

| mode | pyright 1.1.412 | Go port | speedup |
| --- | --- | --- | --- |
| single-threaded | 71.5 s / 3.6 GB | 35 s / 5.4 GB | 2.0× |
| `--threads` (16 workers) | 37.0 s | ~13 s / ~19 GB | 2.8× |
| `--threads 8` | — | ~12 s / ~14 GB | 3.0× |
| `--cachedir`, nothing changed | n/a — pyright has no cache | **1.0 s / 190 MB** | 71× |
| `--cachedir`, one file changed | n/a | **2.6 s** | 27× |

The last two rows are the ones that matter for a development loop or CI:
with a warm cache, the typical invocation answers in one to three seconds.

## Where the time goes

Phase breakdown from `--stats`, single-threaded (3,135-file revision; both
implementations agree on 3,812 files parsed and bound, 3,135 checked):

| phase | pyright | Go port | ratio |
| --- | --- | --- | --- |
| Find Source Files | 0.20 s | 0.13 s | 1.5× |
| Read Source Files | 0.39 s | 0.03 s | 13× |
| Tokenize | 1.79 s | 0.20 s | 9× |
| Parse | 2.58 s | 0.50 s | 5× |
| Resolve Imports | 0.78 s | 0.01 s | 78× |
| Bind | 3.24 s | 0.39 s | 8× |
| **Check** | **77.31 s** | **30.91 s** | **2.5×** |

The front end totals ~1.3 s; checking is everything. That shapes all the
engineering below: only things that reduce or parallelize checking, or skip
it entirely, move the headline.

## `--threads`

The port transliterates pyright's own worker model — per-worker analyzer,
each file checked in isolation (`checkOnlyOpenFiles`), affinity queues cut
from directory order, work stealing — with goroutines standing in for
upstream's forked processes. Neither implementation scales to the core
count, because every worker re-parses, re-binds and re-infers the dependency
closure of its own files; that redundancy is the design being ported.

Worker-count sweep (single runs unless noted; single-threaded baseline 35 s):

| `--threads` | time | peak RSS | speedup |
| --- | --- | --- | --- |
| 2 | 22.1 s | 9.5 GB | 1.6× |
| 4 | 15.3 s | 10.7 GB | 2.3× |
| 8 | 10.4–12.9 s | 14–16 GB | ~3.0× |
| 12 | 13.4 s | 19.4 GB | 2.6× |
| 16 (default) | 12.2–12.7 s | 19–21 GB | ~2.8× |

**The curve flattens at 8 workers on this machine.** 16 buys nothing
measurable over 8 and costs ~6 GB, because each extra worker duplicates more
of the closure. If memory matters, `--threads 8` is the sweet spot here.
For reference, pyright at the same counts: 55.7 s with 2 threads, 37.0 s
with 16 — the port is 2.5–3× faster at every point on the curve.

Two findings behind these numbers:

- **GC policy is part of the parallelism.** At Go's default GOGC the shared
  heap's collector scan work ate the speedup entirely — 1.03× over
  single-threaded. The threaded mode therefore sets GOGC=200 automatically
  (unless the environment sets GOGC or GOMEMLIMIT), which is what produces
  the table above. Upstream needs no such knob: each forked worker brings
  its own V8 heap and collector.
- **Partitioning smarter does not help.** An experiment replacing directory
  order with import-graph clustering was built, measured, and reverted: the
  duplicated work is the typeshed and third-party closure, which every
  cluster needs no matter how user files are grouped. Work stealing, on the
  other hand, is worth ~20% of threaded wall time.

The pyright `--threads` RSS is not comparable (its 16 forked workers' heaps
are separate processes); the port's figure is the whole thing.

## `--cachedir`

A pyright-go extension (pyright re-checks everything every run): a run-to-run
diagnostic cache keyed by each file's transitive dependency closure. Import
descriptors are cached by content hash so unchanged files skip parsing, but
**import resolution reruns fresh every run** — a new file shadowing a module
still invalidates correctly, which is the staleness class other tools' caches
get wrong. See cmd/pyright-go/cache.go for the design.

Measured with `--cachedir` + `--threads 8`:

| scenario | time | peak RSS |
| --- | --- | --- |
| cold (empty cache) | 16.3 s | 14 GB |
| nothing changed | **1.0 s** | **190 MB** |
| one file changed | **2.6 s** | 320 MB |
| edit reverted | 1.4 s (hit again — content-addressed, not mtime) | — |

The cold run pays ~2.5 s over plain `--threads` for fingerprinting and earns
it back on the first warm run. The warm floor is honest, irreducible work:
enumerating the project, hashing every reachable file, and re-resolving every
import against the live file system.

Cached (and threaded) output carries per-file isolation semantics — the same
two-diagnostic difference from single-threaded mode that pyright's own
`--threads` exhibits (UPSTREAM-BUGS.md #17). Warm output is byte-identical
to cold, and cold is byte-identical to the equivalent uncached `--threads`
run.

## Memory

The port trades memory for speed: at pyright's ~3.6 GB the port is slower,
and at full speed it wants ~1.5× the memory single-threaded and several
times that with 16 workers. Two structure-layout changes (the hybrid
`AnalyzerNodeInfo` slot and the `ClassType` rare-field split — see
PORTING.md) recovered ~12% of live heap at wall-time parity; the remainder
is the cost of pyright's clone-on-specialize type model as represented in
Go, not a specific defect.

`GOMEMLIMIT` bounds it when needed, with identical diagnostics at every
setting (3,135-file revision, single-threaded, before the layout work):

| GOMEMLIMIT | time | peak RSS |
| --- | --- | --- |
| unset | 47.1 s | 6.3 GB |
| 4 GiB | 81.4 s | 4.4 GB |
| 3 GiB | 137.7 s | 3.7 GB |
| 2 GiB | 228.9 s | 3.0 GB |

For small inputs the trade never appears: an 84-file project runs in 0.85 s
/ 202 MB against pyright's 2.34 s / 303 MB.

## What these numbers do not say

Neither implementation is measured as a language server here — this is all
batch analysis. One machine, one large project; ratios will vary with
project shape (stub-heavy projects lean harder on the front end, where the
port's lead is 5–9×). And the comparison target is pyright under Node;
checkers with different architectures (interned types, no GC, shared
inference across threads) occupy a different performance class by design,
at the cost of not being pyright.
