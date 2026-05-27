package memory

import (
	"strings"
	"testing"
)

// buildScanBody produces approxKB kilobytes of clean prose with no
// secrets. Used as the baseline for "what does Scan cost on innocent
// content".
func buildScanBody(approxKB int) []byte {
	chunk := "The session token rotates on every successful request. Tracing\n" +
		"spans carry the tenant id propagated via header. Logging uses\n" +
		"structured JSON with the request id as the correlation key. Queries\n" +
		"go through the prepared statement cache to amortise parser cost.\n"
	var b strings.Builder
	for b.Len() < approxKB*1024 {
		b.WriteString(chunk)
	}
	return []byte(b.String())
}

// BenchmarkScan_CleanSmall — 4 KB of innocent prose. Lower bound for
// per-propose_update overhead since every apply runs the scan.
func BenchmarkScan_CleanSmall(b *testing.B) {
	body := buildScanBody(4)
	opts := DefaultScanOpts()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if findings := Scan(body, opts); len(findings) != 0 {
			b.Fatalf("unexpected findings on clean body: %+v", findings)
		}
	}
}

// BenchmarkScan_CleanLarge — 64 KB. Roughly the cost of scanning a
// big aggregated decisions / archive file.
func BenchmarkScan_CleanLarge(b *testing.B) {
	body := buildScanBody(64)
	opts := DefaultScanOpts()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if findings := Scan(body, opts); len(findings) != 0 {
			b.Fatalf("unexpected findings: %+v", findings)
		}
	}
}

// BenchmarkScan_WithAWSKey — same baseline but with an AWS key
// embedded. Worst case: regex matches AND entropy fires.
func BenchmarkScan_WithAWSKey(b *testing.B) {
	body := append(buildScanBody(4),
		[]byte("\nKey: AKIAIOSFODNN7EXAMPLE\n")...)
	opts := DefaultScanOpts()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if findings := Scan(body, opts); len(findings) == 0 {
			b.Fatal("expected aws_access_key finding, got none")
		}
	}
}

// BenchmarkScan_WithAllowlist — same body but with an allowlist
// region covering the secret-looking content. Measures the
// allowlist-skip path: regex still runs but each hit is checked
// against the region list.
func BenchmarkScan_WithAllowlist(b *testing.B) {
	body := []byte("Prose.\n\n<!-- @secret-scan: allow reason=\"docs\" -->\nKey: AKIAIOSFODNN7EXAMPLE\n<!-- @secret-scan: end -->\n")
	body = append(body, buildScanBody(4)...)
	regions, err := ExtractAllowlistRegions(body)
	if err != nil {
		b.Fatal(err)
	}
	opts := DefaultScanOpts()
	opts.Allowlist = regions
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if findings := Scan(body, opts); len(findings) != 0 {
			b.Fatalf("allowlist failed: %+v", findings)
		}
	}
}
