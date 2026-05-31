# Retrieval quality eval

An offline, deterministic measurement of `memory.fetch_context`'s search
core: given a query, does it return the sections a human labeled relevant?

- **Harness:** [`internal/eval/retrieval_test.go`](../../internal/eval/retrieval_test.go)
- **Reproduce:** `go test -run TestRetrievalEval -v ./internal/eval/`
- **Runs in CI** with regression floors, so the numbers below can't quietly rot.

## Results

28 natural-language queries over a 28-section labeled corpus (the corpus
and gold labels are in the harness — audit them):

| Config | recall@5 | hit@1 | MRR | nDCG@5 |
|---|---|---|---|---|
| match-all (AND) — *prior behaviour* | 0.071 | 0.071 | 0.071 | 0.071 |
| **match-any (OR) — shipped** | **0.982** | **0.964** | **0.973** | **0.966** |
| **lift** | **+0.911** | **+0.893** | **+0.902** | **+0.894** |

Read: on this set the shipped retrieval puts a relevant section in the top
5 for **98%** of queries and as the very first hit for **96%**.

## What this means (and doesn't)

- **The delta is the story.** The prior query builder AND-joined every
  token of the query — including stopwords like *how* / *the* / *with* — so
  a natural-language query only matched if one section happened to contain
  all of them. It almost never did (recall 0.07). Switching to match-any
  (OR) + BM25 ranking is what lifted recall to 0.98. The baseline isn't a
  strawman; it's the exact behaviour we shipped before, and the bug this
  change fixed.
- **The corpus is adversarial on purpose.** Three sections mention Kafka;
  *gateway* / *webhook* / *token* each recur across unrelated sections; and
  several queries are paraphrases whose wording is absent from the target
  (*"message broker"* → Kafka, *"data store"* → Postgres, *"single
  sign-on"* → JWT). BM25 still has to discriminate — the score is earned,
  not a toy 1.0.
- **Scope: retrieval/recall only.** This does **not** claim memory makes an
  agent's task succeed (that's the behavioural eval — see
  [ROADMAP.md](../../ROADMAP.md)), and it excludes the context-dependent
  ranking signals (scope / active-branch / changed-file), which need
  runtime context an offline harness doesn't have.
- **The honest frontier.** These queries share vocabulary with their
  targets — the common case when an agent asks about a project in the
  project's own terms, and where lexical BM25 is strong. Pure-semantic
  paraphrase with no shared words is where it would strain; that's exactly
  the bar a future vector/hybrid mode must clear to earn its place
  (ROADMAP "Exploratory").

## Method

Each query runs through the real index pipeline (`index.Search`: FTS5
`MATCH` + BM25). The shipped config is OR-joined terms (`sanitizeFTSMatch`).
The baseline filters the same ranked results to sections containing *all*
query terms — a faithful stand-in for the prior implicit-AND, with BM25
order preserved. Metrics are binary-relevance recall@5, success@1 (hit@1),
mean reciprocal rank, and nDCG@5, averaged over all queries.
