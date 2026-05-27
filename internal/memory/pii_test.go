package memory

import (
	"strings"
	"testing"
)

// =============================================================================
// luhnValid: the gate that keeps the credit-card detector honest
// =============================================================================

func TestLuhnValid_KnownGood(t *testing.T) {
	// Industry-standard test numbers (not real cards; documented to be
	// safe for validation testing — Stripe's published test corpus).
	cases := []string{
		"4242424242424242", // Visa test
		"5555555555554444", // Mastercard test
		"378282246310005",  // Amex test (15 digits)
	}
	for _, c := range cases {
		if !luhnValid(c) {
			t.Errorf("luhnValid(%q) = false, want true", c)
		}
	}
}

func TestLuhnValid_KnownBad(t *testing.T) {
	cases := []string{
		"4242424242424241",   // one digit off
		"1234567890123456",   // sequential, fails Luhn
		"0000000000000000",   // sum 0 BUT len(digits)=16; Luhn requires non-trivial
		"1",                  // too short
		"12345",              // too short
		"99999999999999999999999", // too long (we'd also reject in caller)
	}
	for _, c := range cases {
		// We don't test the "all zeros = sum 0" case as bad because Luhn
		// technically accepts it. The regex+length guard in piiPatterns
		// filters trivial inputs at the boundary. Just test obvious misses:
		if c == "0000000000000000" {
			continue
		}
		if luhnValid(c) {
			t.Errorf("luhnValid(%q) = true, want false", c)
		}
	}
}

func TestLuhnValid_NonDigitInput(t *testing.T) {
	// Caller (piiSSNAndCC's confirm) should strip non-digits before
	// calling luhnValid. If somehow a non-digit slips in, luhnValid
	// returns false rather than panicking.
	if luhnValid("abcd") {
		t.Error("luhnValid('abcd') should be false")
	}
}

// =============================================================================
// stripNonDigits
// =============================================================================

func TestStripNonDigits(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1234", "1234"},
		{"1234-5678-9012-3456", "1234567890123456"},
		{"1234 5678 9012 3456", "1234567890123456"},
		{"a1b2c3", "123"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripNonDigits(c.in); got != c.want {
			t.Errorf("stripNonDigits(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// =============================================================================
// SSN pattern (high confidence, no confirm step)
// =============================================================================

func TestScan_PII_SSN_Default(t *testing.T) {
	src := []byte("Customer SSN: 123-45-6789 — please verify.\n")
	opts := DefaultScanOpts()
	opts.PIIScanSSNAndCC = true
	findings := Scan(src, opts)

	if findingsOfType(findings, "pii_ssn") == 0 {
		t.Errorf("pii_ssn not detected: %+v", findings)
	}
}

func TestScan_PII_SSN_DisabledWhenFlagOff(t *testing.T) {
	src := []byte("Customer SSN: 123-45-6789\n")
	opts := DefaultScanOpts()
	// PIIScanSSNAndCC stays false.
	findings := Scan(src, opts)
	if findingsOfType(findings, "pii_ssn") != 0 {
		t.Errorf("pii_ssn flagged when feature flag is off: %+v", findings)
	}
}

func TestScan_PII_SSN_DoesNotEchoValue(t *testing.T) {
	const ssn = "123-45-6789"
	src := []byte("X " + ssn + " Y\n")
	opts := DefaultScanOpts()
	opts.PIIScanSSNAndCC = true
	findings := Scan(src, opts)
	for _, f := range findings {
		for _, field := range []string{f.Type, f.ApproximateLocation} {
			if strings.Contains(field, ssn) {
				t.Errorf("PII finding leaked the SSN value via %q field", field)
			}
		}
	}
}

// =============================================================================
// Credit-card pattern (regex + Luhn confirmation)
// =============================================================================

func TestScan_PII_CreditCard_KnownGood(t *testing.T) {
	// Stripe's documented test Visa. Luhn-valid by construction.
	src := []byte("test charge 4242 4242 4242 4242\n")
	opts := DefaultScanOpts()
	opts.PIIScanSSNAndCC = true
	findings := Scan(src, opts)
	if findingsOfType(findings, "pii_credit_card") == 0 {
		t.Errorf("pii_credit_card not detected on Luhn-valid number: %+v", findings)
	}
}

func TestScan_PII_CreditCard_RandomDigitsNotFlagged(t *testing.T) {
	// 16 random-but-not-Luhn-valid digits; should NOT be flagged.
	// The regex would match but the Luhn confirm rejects.
	src := []byte("trace id: 1234567890123456 (random)\n")
	opts := DefaultScanOpts()
	opts.PIIScanSSNAndCC = true
	findings := Scan(src, opts)
	if findingsOfType(findings, "pii_credit_card") != 0 {
		t.Errorf("Luhn-invalid digits falsely flagged as credit card: %+v", findings)
	}
}

func TestScan_PII_CreditCard_TooShortNotFlagged(t *testing.T) {
	// Even if Luhn would accept, < 13 digits doesn't pass the
	// confirm step.
	src := []byte("short num: 12345\n")
	opts := DefaultScanOpts()
	opts.PIIScanSSNAndCC = true
	findings := Scan(src, opts)
	if findingsOfType(findings, "pii_credit_card") != 0 {
		t.Errorf("too-short digit sequence flagged: %+v", findings)
	}
}

// =============================================================================
// Email pattern (opt-in)
// =============================================================================

func TestScan_PII_Email_Default_NotDetected(t *testing.T) {
	src := []byte("Reach out to support@example.com for help.\n")
	opts := DefaultScanOpts()
	opts.PIIScanSSNAndCC = true
	// PIIScanEmail stays false — emails are opt-in.
	findings := Scan(src, opts)
	if findingsOfType(findings, "pii_email") != 0 {
		t.Errorf("pii_email detected when flag is off: %+v", findings)
	}
}

func TestScan_PII_Email_Enabled(t *testing.T) {
	src := []byte("Reach out to support@example.com for help.\n")
	opts := DefaultScanOpts()
	opts.PIIScanEmail = true
	findings := Scan(src, opts)
	if findingsOfType(findings, "pii_email") == 0 {
		t.Errorf("pii_email not detected when flag is on: %+v", findings)
	}
}

func TestScan_PII_Email_AllowlistRegionSkipped(t *testing.T) {
	src := []byte(`Contact info:

<!-- @secret-scan: allow reason="public contact" -->
support@example.com
<!-- @secret-scan: end -->

Other text.
`)
	regions, err := ExtractAllowlistRegions(src)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultScanOpts()
	opts.PIIScanEmail = true
	opts.Allowlist = regions
	findings := Scan(src, opts)
	if findingsOfType(findings, "pii_email") != 0 {
		t.Errorf("email inside allowlist region was flagged: %+v", findings)
	}
}

// =============================================================================
// ClassifyFindings
// =============================================================================

func TestClassifyFindings_OnlyPII(t *testing.T) {
	findings := []Finding{
		{Type: "pii_ssn"},
		{Type: "pii_credit_card"},
	}
	if got := ClassifyFindings(findings); got != ReasonPIIDetected {
		t.Errorf("ClassifyFindings(only PII) = %q, want %q", got, ReasonPIIDetected)
	}
}

func TestClassifyFindings_Mixed(t *testing.T) {
	findings := []Finding{
		{Type: "aws_access_key"},
		{Type: "pii_ssn"},
	}
	if got := ClassifyFindings(findings); got != ReasonSecretDetected {
		t.Errorf("ClassifyFindings(mixed) = %q, want secret_detected (most severe wins)", got)
	}
}

func TestClassifyFindings_OnlySecrets(t *testing.T) {
	findings := []Finding{
		{Type: "github_token"},
		{Type: "stripe_live_key"},
	}
	if got := ClassifyFindings(findings); got != ReasonSecretDetected {
		t.Errorf("ClassifyFindings(only secrets) = %q, want %q", got, ReasonSecretDetected)
	}
}

func TestClassifyFindings_Empty(t *testing.T) {
	if got := ClassifyFindings(nil); got != "" {
		t.Errorf("ClassifyFindings(nil) = %q, want empty", got)
	}
}
