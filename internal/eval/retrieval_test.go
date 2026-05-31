// Package eval contains an offline, deterministic retrieval-quality
// benchmark for memory.fetch_context's search core. No LLM calls, no
// randomness: it builds a labeled corpus, runs each gold query through the
// real index pipeline, and reports standard IR metrics.
//
// What it measures: does FTS5 + the match-any query builder retrieve the
// sections a human labeled relevant? It compares the shipped match-any
// (OR) retrieval against a match-all (AND) baseline — the behaviour
// agent-memory had before the OR change — to quantify the recall lift.
//
// What it does NOT measure: whether memory improves an agent's task
// outcome (that's the behavioural eval, tracked in ROADMAP), nor the
// context-dependent ranking signals (scope/branch/changed-file) which
// need runtime context this offline harness doesn't have. The claim is
// scoped to retrieval/recall.
//
// The corpus is deliberately adversarial: many sections share vocabulary
// (three sections mention Kafka; "gateway"/"webhook"/"token" each recur),
// and several queries are paraphrases whose wording does NOT appear in the
// target section ("message broker" → Kafka, "data store" → Postgres,
// "single sign-on" → JWT). That's where lexical search is supposed to
// strain — so the numbers are earned, not a toy 1.0.
//
// The corpus and labels live in this file on purpose: the numbers are only
// as trustworthy as the gold set, so the gold set is auditable in one
// place. Run it with:
//
//	go test -run TestRetrievalEval -v ./internal/eval/
package eval

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/index"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// doc is one labeled section in the corpus.
type doc struct {
	file    string // repo-relative path under .agent-memory/
	id      string // @id anchor (what gold queries reference)
	heading string
	body    string
}

// goldQuery is a natural-language query and the section @ids a human
// considers relevant to it.
type goldQuery struct {
	query    string
	relevant []string
}

// corpus: a memory for a fictional "orders service". Beyond the sections
// gold queries target, it carries topical DISTRACTORS that share query
// vocabulary (analytics-pipeline also mentions Kafka+events; webhook-delivery
// shares "webhook"+"retry" with the Stripe section; api-gateway shares
// "gateway"; clock-skew shares "token") so BM25 has to discriminate.
var corpus = []doc{
	// --- conventions ---
	{"conventions.md", "build-test", "Build and test",
		"Run `go build ./...` then `go test ./...`. Lint with golangci-lint. CI runs on every push across linux, macos, and windows."},
	{"conventions.md", "git-flow", "Branching and commits",
		"Trunk-based development on main. Conventional commit subjects. Squash-merge pull requests; keep history linear."},

	// --- decisions ---
	{"decisions.md", "postgres", "Use Postgres for the orders store",
		"We chose Postgres over MySQL for the orders database: stronger transactional guarantees and JSONB for flexible order payloads."},
	{"decisions.md", "kafka-events", "Emit order events to Kafka",
		"Order state changes publish to Kafka for an event-driven architecture. Delivery is at-least-once; producers use the transactional outbox pattern."},
	{"decisions.md", "auth-jwt", "Short-lived JWT for service-to-service auth",
		"Services authenticate with short-lived JWTs signed by the auth service. Tokens rotate; no long-lived shared secrets between services."},
	{"decisions.md", "idempotency", "Idempotency keys on the payment API",
		"Every payment request carries an idempotency key so retries don't double-charge. Keys are deduplicated within a 24-hour window."},
	{"decisions.md", "grpc-internal", "gRPC for internal RPC",
		"Internal service-to-service calls use gRPC with protobuf contracts; only edge traffic stays REST."},
	{"decisions.md", "feature-flags", "Feature flags via the config service",
		"Runtime feature flags are served by the config service, so toggling a feature needs no redeploy."},
	{"decisions.md", "monorepo", "Single monorepo for all services",
		"All services live in one monorepo with a shared build; this simplifies cross-cutting changes and CI."},

	// --- pitfalls ---
	{"pitfalls.md", "kafka-rebalance", "Kafka consumer rebalance drops in-flight work",
		"A consumer group rebalance can lose in-flight messages if offsets are committed before processing finishes. Always commit the offset after the handler succeeds."},
	{"pitfalls.md", "pg-pool", "Postgres connection-pool exhaustion under load",
		"Under burst load the service exhausts the Postgres connection pool and requests hang. Cap max connections and put pgbouncer in front."},
	{"pitfalls.md", "timezone", "Timestamps stored without a timezone",
		"Early migrations used TIMESTAMP, not TIMESTAMPTZ, so timestamps were ambiguous. Always store UTC in TIMESTAMPTZ columns."},
	{"pitfalls.md", "cache-stampede", "Redis cache stampede on expiry",
		"When a hot Redis key expires, concurrent requests stampede the database. Use a mutex or stale-while-revalidate."},
	{"pitfalls.md", "clock-skew", "Clock skew breaks token validation",
		"If service clocks drift, short-lived tokens are rejected as expired. Run NTP and allow a small leeway when validating expiry."},
	{"pitfalls.md", "migration-lock", "Schema migration locks the orders table",
		"An ALTER on the large orders table took a long lock and stalled writes. Use online migration tooling for big tables."},

	// --- modules ---
	{"modules/orders.md", "orders-api", "Orders API",
		"REST endpoints to create, fetch, and cancel orders. Validates the cart, reserves inventory, and writes the order row inside one transaction."},
	{"modules/payments.md", "payments-gateway", "Payments gateway integration",
		"Integrates the Stripe gateway. Charges go through the idempotent payment API and Stripe webhooks confirm settlement asynchronously."},
	{"modules/payments.md", "payment-retries", "Payment retry strategy",
		"Failed charges retry with exponential backoff, reusing the original idempotency key so a retry never creates a second charge."},
	{"modules/inventory.md", "inventory-reservation", "Inventory reservation",
		"Stock is reserved when an order is placed and released when the order is cancelled or the reservation times out."},
	{"modules/notifications.md", "notif-email", "Email notifications",
		"Customer emails (order confirmed, shipped) are sent via SES from templates, with retry on transient send failures."},
	{"modules/auth.md", "auth-tokens", "Token issuing and refresh",
		"The auth service issues access tokens and refresh tokens. Refresh tokens rotate on every use and can be revoked per session."},
	{"modules/cart.md", "cart-service", "Cart service",
		"Holds the customer cart and validates line items and quantities before checkout turns it into an order."},
	{"modules/shipping.md", "shipping-carrier", "Shipping carrier integration",
		"Integrates FedEx and UPS for label creation and tracking; retries transient carrier API errors."},
	{"modules/search.md", "catalog-search", "Catalog search",
		"Product catalog search runs on OpenSearch with synonym expansion and typo tolerance."},
	{"modules/reporting.md", "analytics-pipeline", "Analytics pipeline",
		"Order and payment events stream from Kafka into the data warehouse for analytics and reporting dashboards."},
	{"modules/webhooks.md", "webhook-delivery", "Outbound webhook delivery",
		"Delivers webhooks to merchant endpoints with signed payloads, retries, and a dead-letter queue."},
	{"modules/gateway.md", "api-gateway", "API gateway",
		"The edge API gateway terminates TLS, enforces rate limits, and routes requests to backend services."},

	// --- archive (retired) ---
	{"archive/2025-legacy-soap.md", "legacy-soap", "Legacy SOAP order intake (retired)",
		"The original SOAP endpoint for order intake was decommissioned in 2025; all clients moved to the REST orders API."},
}

// queries: natural-language, multi-word. The set mixes easy lexical hits
// with hard paraphrases (wording absent from the target) and queries with
// strong distractors, so aggregate scores are realistic, not perfect.
var queries = []goldQuery{
	// direct-ish
	{"how do we run the tests", []string{"build-test"}},
	{"branching strategy and commit rules", []string{"git-flow"}},
	{"why postgres instead of mysql", []string{"postgres"}},
	{"event streaming with kafka", []string{"kafka-events", "kafka-rebalance", "analytics-pipeline"}},
	{"consumer rebalance losing in-flight messages", []string{"kafka-rebalance"}},
	{"kafka offset commit after processing", []string{"kafka-rebalance"}},
	{"postgres connection pool exhausted under load", []string{"pg-pool"}},
	{"storing timestamps in utc with a timezone", []string{"timezone"}},
	{"idempotency for payments", []string{"idempotency", "payment-retries", "payments-gateway"}},
	{"how does payment retry work", []string{"payment-retries"}},
	{"stripe webhook integration", []string{"payments-gateway"}},
	{"reserve inventory when an order is placed", []string{"inventory-reservation"}},
	{"release stock on order cancellation", []string{"inventory-reservation"}},
	{"jwt authentication between services", []string{"auth-jwt", "auth-tokens"}},
	{"refresh token rotation and revocation", []string{"auth-tokens"}},
	{"send a confirmation email to the customer", []string{"notif-email"}},
	{"create and cancel orders endpoint", []string{"orders-api"}},
	{"old retired soap order intake endpoint", []string{"legacy-soap"}},
	{"redis cache stampede when a key expires", []string{"cache-stampede"}},
	{"rate limiting at the edge", []string{"api-gateway"}},
	{"cart validation before checkout", []string{"cart-service"}},
	{"shipping label creation with carriers", []string{"shipping-carrier"}},

	// harder paraphrases — target wording differs from the query
	{"which data store technology did we choose", []string{"postgres"}},                               // "data store" vs database/Postgres
	{"message broker for order events", []string{"kafka-events"}},                                     // "broker" vs Kafka; analytics distracts
	{"prevent duplicate charges when retrying a payment", []string{"idempotency", "payment-retries"}}, // "duplicate charges" vs double-charge / second charge
	{"single sign-on across microservices", []string{"auth-jwt"}},                                     // SSO vs JWT — genuinely hard for lexical
	{"avoid losing kafka messages when a consumer restarts", []string{"kafka-rebalance"}},
	{"warehouse analytics from streamed events", []string{"analytics-pipeline"}}, // kafka-events distracts
}

// reportK is the cut-off for the headline metrics (a budgeted pack's
// "first screen" of sections).
const reportK = 5

func TestRetrievalEval(t *testing.T) {
	idx, ctx := buildEvalIndex(t)

	all := runConfig(ctx, t, idx, true)  // match-all (AND) — prior behaviour
	any := runConfig(ctx, t, idx, false) // match-any (OR) — shipped

	t.Logf("\nRetrieval eval — %d queries, %d sections (offline, deterministic)\n", len(queries), len(corpus))
	t.Logf("%-24s  %8s  %6s  %6s  %8s", "config", fmt.Sprintf("recall@%d", reportK), "hit@1", "MRR", fmt.Sprintf("nDCG@%d", reportK))
	t.Logf("%-24s  %8.3f  %6.3f  %6.3f  %8.3f", "match-all (AND, prior)", all.recall, all.hit1, all.mrr, all.ndcg)
	t.Logf("%-24s  %8.3f  %6.3f  %6.3f  %8.3f", "match-any (OR, shipped)", any.recall, any.hit1, any.mrr, any.ndcg)
	t.Logf("%-24s  %+8.3f  %+6.3f  %+6.3f  %+8.3f", "lift", any.recall-all.recall, any.hit1-all.hit1, any.mrr-all.mrr, any.ndcg-all.ndcg)

	// Regression guards (not vanity asserts):
	// 1. the shipped config must not retrieve worse than the AND baseline.
	if any.recall < all.recall {
		t.Errorf("match-any recall@%d (%.3f) < match-all (%.3f) — recall regressed", reportK, any.recall, all.recall)
	}
	if any.mrr < all.mrr {
		t.Errorf("match-any MRR (%.3f) < match-all (%.3f)", any.mrr, all.mrr)
	}
	// 2. an absolute floor so a future change that quietly tanks retrieval fails CI.
	//    Kept comfortably below the observed scores so it guards regressions
	//    without being brittle to ranking tweaks.
	const recallFloor, mrrFloor = 0.80, 0.80
	if any.recall < recallFloor {
		t.Errorf("match-any recall@%d (%.3f) below floor %.2f", reportK, any.recall, recallFloor)
	}
	if any.mrr < mrrFloor {
		t.Errorf("match-any MRR (%.3f) below floor %.2f", any.mrr, mrrFloor)
	}
}

// metrics is the mean of each IR metric across all gold queries.
type metrics struct{ recall, hit1, mrr, ndcg float64 }

// runConfig runs every gold query and averages the metrics. When matchAll
// is true the ranked list is filtered to sections containing ALL query
// terms (simulating the old implicit-AND), preserving BM25 order.
func runConfig(ctx context.Context, t *testing.T, idx *index.Index, matchAll bool) metrics {
	t.Helper()
	var m metrics
	for _, gq := range queries {
		res, err := idx.Search(ctx, gq.query, 50)
		if err != nil {
			t.Fatalf("search %q: %v", gq.query, err)
		}
		if matchAll {
			res = filterAllTerms(res, gq.query)
		}
		ranked := topIDs(res, reportK)
		rel := toSet(gq.relevant)
		m.recall += recallAtK(ranked, rel, len(gq.relevant))
		m.hit1 += hitAt1(ranked, rel)
		m.mrr += reciprocalRank(ranked, rel)
		m.ndcg += ndcgAtK(ranked, rel)
	}
	n := float64(len(queries))
	return metrics{m.recall / n, m.hit1 / n, m.mrr / n, m.ndcg / n}
}

// --- IR metrics (binary relevance) ---

func recallAtK(ranked []string, rel map[string]bool, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(countHits(ranked, rel)) / float64(total)
}

// hitAt1 is 1 when the top-ranked result is relevant (success@1).
func hitAt1(ranked []string, rel map[string]bool) float64 {
	if len(ranked) > 0 && rel[ranked[0]] {
		return 1
	}
	return 0
}

func reciprocalRank(ranked []string, rel map[string]bool) float64 {
	for i, id := range ranked {
		if rel[id] {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

func ndcgAtK(ranked []string, rel map[string]bool) float64 {
	var dcg float64
	for i, id := range ranked {
		if rel[id] {
			dcg += 1.0 / math.Log2(float64(i+2)) // rank i (0-based) → log2(i+2)
		}
	}
	ideal := len(rel)
	if ideal > reportK {
		ideal = reportK
	}
	var idcg float64
	for i := 0; i < ideal; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

func countHits(ranked []string, rel map[string]bool) int {
	n := 0
	for _, id := range ranked {
		if rel[id] {
			n++
		}
	}
	return n
}

// --- helpers ---

func topIDs(res []index.SearchResult, k int) []string {
	out := make([]string, 0, k)
	for i, r := range res {
		if i >= k {
			break
		}
		out = append(out, r.SectionID)
	}
	return out
}

func toSet(ids []string) map[string]bool {
	s := make(map[string]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}

// filterAllTerms keeps only results whose indexed body contains every
// alphanumeric term of the query — a faithful stand-in for FTS implicit
// AND (the prior behaviour). BM25 order is preserved.
func filterAllTerms(res []index.SearchResult, query string) []index.SearchResult {
	want := terms(query)
	var out []index.SearchResult
	for _, r := range res {
		have := termSet(r.Content)
		ok := true
		for _, term := range want {
			if !have[term] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

func terms(s string) []string {
	seen := map[string]bool{}
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tok := b.String()
		b.Reset()
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func termSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, t := range terms(s) {
		set[t] = true
	}
	return set
}

// buildEvalIndex writes the corpus to a temp .agent-memory/ and rebuilds
// the real FTS index over it — exercising the production parse/index path.
func buildEvalIndex(t *testing.T) (*index.Index, context.Context) {
	t.Helper()
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".agent-memory")
	for _, sub := range []string{"meta", "modules", "archive", "local"} {
		if err := os.MkdirAll(filepath.Join(memDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := schema.WriteDefault(filepath.Join(memDir, "meta", "schema.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteDefault(filepath.Join(memDir, "meta", "manifest.yaml"), "eval"); err != nil {
		t.Fatal(err)
	}

	// Group sections by file (stable order) and write each file.
	byFile := map[string][]doc{}
	var order []string
	for _, d := range corpus {
		if _, seen := byFile[d.file]; !seen {
			order = append(order, d.file)
		}
		byFile[d.file] = append(byFile[d.file], d)
	}
	sort.Strings(order)
	for _, f := range order {
		var sb strings.Builder
		for _, d := range byFile[f] {
			fmt.Fprintf(&sb, "## %s\n<!-- @id: %s -->\n\n%s\n\n", d.heading, d.id, d.body)
		}
		full := filepath.Join(memDir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	ctx := context.Background()
	if err := idx.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := idx.RebuildAll(ctx, memDir, schema.DefaultSchema(), index.RebuildOpts{}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	return idx, ctx
}
