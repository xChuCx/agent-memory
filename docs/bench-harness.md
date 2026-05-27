# Benchmark Harness (M8)

**Status:** Implemented in [`internal/bench/`](../internal/bench/) (end-to-end), per-package `*_bench_test.go` files (low-level hot paths), and [`scripts/bench.sh`](../scripts/bench.sh).
**Owner:** `internal/bench/` (M8, Release 0.2).
**Tracks design:** [Implementation Plan §7.8 M8](../agent-memory-implementation-plan.md).

## Goal

Track performance over time. Catch regressions early. Tell the
difference between "this PR is 10% slower" (acceptable) and "this PR
is 10× slower" (block).

## What's measured

**End-to-end pipelines** (`internal/bench/`):

- `FetchContext_Bootstrap` — empty-query pack assembly. The call
  every agent does at session start.
- `FetchContext_Query` — query-driven FTS + ranking + section
  packing.
- `FetchContext_ScopedQuery` — same plus a `scope` boost filter.
- `FetchContext_BootstrapLargeCorpus` — large fixture variant
  (stays cheap because bootstrap only touches 4 known files).
- `ProposeUpdate_AppendPitfall` — the apply-routing path; full
  pipeline including secret scan, schema validation, splice, index
  upsert, optional git auto-stage.
- `ProposeUpdate_StageDecision` — the stage-routing path; writes
  the staging directory artefacts.
- `ProposeUpdate_SessionLog` — session_log intent with auto path
  rewrite.
- `RebuildAll_Default` / `RebuildAll_LargeCorpus` — full FTS
  rebuild over the fixture.

**Low-level hot paths** (per-package):

| Package | Benchmark | What it measures |
|---------|-----------|------------------|
| `markdown` | `ParseSections` (medium + large) | goldmark walk + anchor extraction. Reports MB/s. |
| `markdown` | `Splice_SingleOp` | byte-range substitution. Should be near memcpy speed. |
| `markdown` | `FindByID` | linear scan over the sections slice. |
| `memory` | `Scan_CleanSmall` / `_CleanLarge` | regex set + entropy on innocent prose. |
| `memory` | `Scan_WithAWSKey` | worst case: regex matches AND entropy considers the token. |
| `memory` | `Scan_WithAllowlist` | allowlist-skip path. |
| `index` | `Search_Small/Medium/Large` | FTS5 query on a 50 / 500 / 5000-section index. |
| `index` | `UpsertSections_Batch10` | incremental write path used after every applied propose_update. |

## Running

Local quick check:

```bash
scripts/bench.sh
```

Defaults: `-count=3` runs each benchmark three times (Go picks a stable
N per run; this gives us median + variance), `-benchmem` reports
allocations, `-run=^$` skips non-bench tests.

Subset by pattern:

```bash
scripts/bench.sh -bench=FetchContext
scripts/bench.sh -bench=Scan
```

Compare two runs with `benchstat`:

```bash
go install golang.org/x/perf/cmd/benchstat@latest

scripts/bench.sh > /tmp/before.txt
# ... change something ...
scripts/bench.sh > /tmp/after.txt
benchstat /tmp/before.txt /tmp/after.txt
```

`benchstat` outputs a `delta` column and a `p` value; deltas with `p
< 0.05` are statistically meaningful.

## Baseline numbers (local, Windows 11, AMD Ryzen 7 3700X, NVMe)

Captured `2026-05-27` with `-count=1` for compactness. Real perf work
should use `-count=10` and `benchstat`.

### End-to-end

```
BenchmarkFetchContext_Bootstrap              150µs   ~9 KB   ~40 allocs
BenchmarkFetchContext_Query                  3.4ms   ~935 KB   ~4500 allocs
BenchmarkFetchContext_ScopedQuery            4.0ms   ~1.2 MB   ~5900 allocs
BenchmarkFetchContext_BootstrapLargeCorpus   160µs   ~14 KB   ~45 allocs
BenchmarkProposeUpdate_AppendPitfall         33ms    ~1.6 MB   ~11000 allocs
BenchmarkProposeUpdate_StageDecision         19ms    ~350 KB   ~2800 allocs
BenchmarkProposeUpdate_SessionLog            7.6ms   ~112 KB   ~500 allocs
BenchmarkRebuildAll_Default                  69ms    ~2.5 MB   ~20000 allocs
BenchmarkRebuildAll_LargeCorpus              586ms   ~12 MB   ~97000 allocs
```

### Low-level

```
ParseSections (~8 KB file)        33 MB/s    106 KB    641 allocs
ParseSections_LargeFile (~60 KB)  73 MB/s    540 KB   2773 allocs
Splice_SingleOp                  1.24 GB/s   10 KB       3 allocs
FindByID                          300 ns       0         0 allocs

Scan_CleanSmall   (~4 KB)        3.98 MB/s    45 KB    652 allocs
Scan_CleanLarge   (~64 KB)       3.89 MB/s   1.1 MB  10232 allocs
Scan_WithAWSKey                  3.85 MB/s    82 KB    660 allocs
Scan_WithAllowlist               4.15 MB/s    43 KB    662 allocs

Search_SmallIndex   (50 secs)     555µs     16 KB    284 allocs
Search_MediumIndex  (500 secs)   1.49ms     16 KB    288 allocs
Search_LargeIndex   (5000 secs)  9.92ms     16 KB    286 allocs
UpsertSections_Batch10            954µs     13 KB    416 allocs
```

## Interpreting the numbers

**Bootstrap fetch is cheap** because it reads four known files and
doesn't query the index. Cost is dominated by file I/O on a warm cache.
Don't expect this to drop further without a fundamental change (e.g.,
keeping a parsed-section cache).

**Query fetch costs ~3ms / ~1 MB** because each result requires re-
parsing the source file to extract the matched section's bytes. The
fileCache inside `buildSearchPack` amortises across results from the
same file, but cross-file queries still re-parse. A future optimisation:
cache parsed `[]Section` slices per file across queries.

**ProposeUpdate ~20-30ms** is dominated by `WriteAtomic` (fsync + rename)
+ the FTS5 reindex of touched files. Per-call wall time is invariant
on corpus size for small files (we touch only what changed).

**RebuildAll scales linearly** with corpus size, ~600ms for ~600
sections. The walk-parse-upsert path is the natural cost; the per-row
FTS5 INSERT amortises well thanks to the single transaction.

**Scan @ ~4 MB/s** is the regex set's lower bound. Optimisation
opportunities are theoretically there (compile once into a multi-
pattern matcher, e.g., Hyperscan or Aho-Corasick), but unnecessary at
current usage — even a 64 KB body scans in 17ms.

**FTS5 Search scales sub-linearly** with index size: 50→5000 sections
is 100× bigger but only ~18× slower. The full-text index is doing
its job.

## What this harness does NOT do

- **No CI gating.** Benchmarks run on-demand. Adding a CI job that
  fails on regression requires a stable baseline file and a tolerance
  policy — both project-specific decisions deferred to M8 batch 2.
- **No multi-machine averaging.** The baseline above is from a single
  developer machine. CI runners are ~2-3× slower; don't compare absolute
  numbers across hosts.
- **No long-tail testing.** Benchmarks measure typical-case throughput;
  tail latencies under pressure (concurrent lock contention,
  large transactions) need different methodology — fuzz / chaos
  testing, not bench.

## References

- [Implementation Plan §7.8 M8](../agent-memory-implementation-plan.md).
- [`scripts/bench.sh`](../scripts/bench.sh).
- [`internal/bench/`](../internal/bench/).
- `benchstat` — https://pkg.go.dev/golang.org/x/perf/cmd/benchstat.
