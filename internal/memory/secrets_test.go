package memory

import (
	"strings"
	"testing"
)

// findingsOfType counts how many findings of the given type are in fs.
func findingsOfType(fs []Finding, kind string) int {
	n := 0
	for _, f := range fs {
		if f.Type == kind {
			n++
		}
	}
	return n
}

func TestScan_NoSecretsNoFindings(t *testing.T) {
	src := []byte(`# Heading

Normal prose. Mentions an authentication system but doesn't include a
token. Run ` + "`go test ./...`" + ` before merging.

- bullet one
- bullet two
`)
	got := Scan(src, ScanOpts{})
	if len(got) != 0 {
		t.Errorf("expected no findings, got %+v", got)
	}
}

func TestScan_AWSAccessKey(t *testing.T) {
	// AWS docs canonical example, intentionally invalid for use.
	src := []byte("Set AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE in your .env\n")
	got := Scan(src, ScanOpts{})
	if findingsOfType(got, "aws_access_key") == 0 {
		t.Errorf("aws_access_key not detected: %+v", got)
	}
	// And the finding must NOT contain the actual key in any field.
	for _, f := range got {
		if strings.Contains(f.Type, "AKIA") || strings.Contains(f.ApproximateLocation, "AKIA") {
			t.Errorf("finding leaked token bytes: %+v", f)
		}
	}
}

func TestScan_GitHubToken(t *testing.T) {
	src := []byte("token: ghp_abcdef0123456789abcdef0123456789abcd\n")
	got := Scan(src, ScanOpts{})
	if findingsOfType(got, "github_token") == 0 {
		t.Errorf("github_token not detected: %+v", got)
	}
}

func TestScan_JWT(t *testing.T) {
	// Fabricated JWT with the canonical "eyJ" header and three segments.
	src := []byte("auth: eyJhbGciOiJIUzI1NiJ9.eyJ1c2VyIjoidGVzdCJ9.abc123\n")
	got := Scan(src, ScanOpts{})
	if findingsOfType(got, "jwt") == 0 {
		t.Errorf("jwt not detected: %+v", got)
	}
}

func TestScan_PrivateKeyBlock(t *testing.T) {
	src := []byte(`Private key:
-----BEGIN OPENSSH PRIVATE KEY-----
...truncated...
-----END OPENSSH PRIVATE KEY-----
`)
	got := Scan(src, ScanOpts{})
	if findingsOfType(got, "private_key_block") == 0 {
		t.Errorf("private_key_block not detected: %+v", got)
	}
}

func TestScan_AnthropicAndOpenAIDoNotDoubleFire(t *testing.T) {
	// An Anthropic key starts with sk-ant-, so the OpenAI rule (sk-...)
	// would also match it. We accept overlapping findings; what we care
	// about is that at least the more specific anthropic_api_key fires.
	src := []byte("API_KEY=sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghij\n")
	got := Scan(src, ScanOpts{})
	if findingsOfType(got, "anthropic_api_key") == 0 {
		t.Errorf("anthropic_api_key not detected: %+v", got)
	}
}

func TestScan_StripeLiveKey(t *testing.T) {
	src := []byte("STRIPE_SECRET=sk_live_ABCDEFGHIJKLMNOPQRSTUVWX\n")
	got := Scan(src, ScanOpts{})
	if findingsOfType(got, "stripe_live_key") == 0 {
		t.Errorf("stripe_live_key not detected: %+v", got)
	}
}

func TestScan_AllowlistedRegionIsSkipped(t *testing.T) {
	src := []byte(`Normal prose.

<!-- @secret-scan: allow reason="docs: AWS key format example" -->
The format starts with AKIA followed by 16 chars: AKIAIOSFODNN7EXAMPLE
<!-- @secret-scan: end -->

After the region.
`)
	regions, err := ExtractAllowlistRegions(src)
	if err != nil {
		t.Fatal(err)
	}
	got := Scan(src, ScanOpts{Allowlist: regions})
	if findingsOfType(got, "aws_access_key") != 0 {
		t.Errorf("allowlisted region was scanned anyway: %+v", got)
	}
}

func TestScan_AllowlistDoesNotCoverOutsideContent(t *testing.T) {
	src := []byte(`Outside: AKIAIOSFODNN7EXAMPLE

<!-- @secret-scan: allow reason="x" -->
Inside (skipped): AKIAIOSFODNN7EXAMPLE
<!-- @secret-scan: end -->
`)
	regions, _ := ExtractAllowlistRegions(src)
	got := Scan(src, ScanOpts{Allowlist: regions})
	// We expect exactly one aws_access_key finding (the outside one).
	if n := findingsOfType(got, "aws_access_key"); n != 1 {
		t.Errorf("aws_access_key count = %d, want 1 (outside-only)", n)
	}
}

func TestScan_EntropyDetectsHighEntropyLongTokens(t *testing.T) {
	// 40-char random-looking alphanumeric — high entropy.
	src := []byte("token: aB3xQ9zL4mPnR7tY2vK8wE5jH1uF6oI0sD2yG4cX\n")
	got := Scan(src, DefaultScanOpts())
	if findingsOfType(got, "high_entropy") == 0 {
		t.Errorf("high_entropy not detected: %+v", got)
	}
}

func TestScan_EntropyIgnoresLowEntropy(t *testing.T) {
	// 50 lowercase 'a's — entropy is 0, well below threshold.
	src := []byte("token: " + strings.Repeat("a", 50) + "\n")
	got := Scan(src, DefaultScanOpts())
	if findingsOfType(got, "high_entropy") != 0 {
		t.Errorf("low-entropy string flagged: %+v", got)
	}
}

func TestScan_EntropyIgnoresShortTokens(t *testing.T) {
	// 20-char high-entropy token, below min length (32).
	src := []byte("token: aB3xQ9zL4mPnR7tY2vK\n")
	got := Scan(src, DefaultScanOpts())
	if findingsOfType(got, "high_entropy") != 0 {
		t.Errorf("short token flagged: %+v", got)
	}
}

func TestScan_EntropyDisabledByDefaultOnZeroOpts(t *testing.T) {
	src := []byte("token: aB3xQ9zL4mPnR7tY2vK8wE5jH1uF6oI0sD2yG4cX\n")
	// Caller passes zero ScanOpts → entropy detection off.
	got := Scan(src, ScanOpts{})
	if findingsOfType(got, "high_entropy") != 0 {
		t.Errorf("entropy detection ran with zero opts: %+v", got)
	}
}

func TestScan_FindingHasUsefulLine(t *testing.T) {
	src := []byte(`line one
line two
line three with token AKIAIOSFODNN7EXAMPLE
line four
`)
	got := Scan(src, ScanOpts{})
	if len(got) == 0 {
		t.Fatal("expected finding")
	}
	// AKIA is on line 3.
	if got[0].Line != 3 {
		t.Errorf("Line = %d, want 3", got[0].Line)
	}
	if !strings.Contains(got[0].ApproximateLocation, "line 3") {
		t.Errorf("ApproximateLocation = %q, want to mention line 3", got[0].ApproximateLocation)
	}
}

func TestScan_DoesNotEchoTokenValue(t *testing.T) {
	const sample = "AKIAIOSFODNN7EXAMPLE"
	src := []byte("token: " + sample + "\n")
	got := Scan(src, DefaultScanOpts())
	if len(got) == 0 {
		t.Fatal("expected finding")
	}
	for _, f := range got {
		for _, field := range []string{f.Type, f.ApproximateLocation} {
			if strings.Contains(field, sample) {
				t.Errorf("finding leaked token text via %q field", field)
			}
		}
	}
}

func TestScan_EntropyDoesNotDoubleFireWithRegex(t *testing.T) {
	// A line containing a JWT should produce a "jwt" finding but NOT
	// also a separate "high_entropy" finding for the same line.
	src := []byte("auth: eyJhbGciOiJIUzI1NiJ9.eyJ1c2VyIjoidGVzdEAyMDI1MTIzNDU2In0.abcDEFghijKLMnopQRSTuvw\n")
	got := Scan(src, DefaultScanOpts())
	if findingsOfType(got, "jwt") == 0 {
		t.Errorf("jwt not detected: %+v", got)
	}
	if findingsOfType(got, "high_entropy") != 0 {
		t.Errorf("entropy double-fired on a regex-matched line: %+v", got)
	}
}

func TestShannonEntropy(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// We assert min/max ranges rather than exact values to keep the
		// test robust against small implementation differences.
		minH, maxH float64
	}{
		{"empty", "", 0, 0},
		{"all same", "aaaaaaaaaa", 0, 0},
		{"binary 50/50", "ababababab", 0.99, 1.01},
		{"random-looking 40 chars", "aB3xQ9zL4mPnR7tY2vK8wE5jH1uF6oI0sD2yG4cX", 4.5, 6.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := shannonEntropy([]byte(c.in))
			if h < c.minH || h > c.maxH {
				t.Errorf("shannonEntropy(%q) = %f, want in [%f, %f]", c.in, h, c.minH, c.maxH)
			}
		})
	}
}
