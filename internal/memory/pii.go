package memory

import "regexp"

// PIIPrefix is the prefix every PII Finding.Type carries. Classifiers
// use it to split mixed scan results into "credential present" vs
// "only PII".
const PIIPrefix = "pii_"

// piiPattern parallels secretPattern but for PII detection. confirm is
// an optional post-regex check — useful for shapes that need a checksum
// to confirm a real-world identifier (e.g., Luhn for credit cards).
// A nil confirm always confirms.
type piiPattern struct {
	name    string
	regex   *regexp.Regexp
	confirm func(matched string) bool
}

// piiSSNAndCC are the high-confidence PII patterns. Both are extremely
// rare in legitimate technical documentation, so default-on detection
// has a low false-positive rate.
//
// SSN: rigid "XXX-XX-XXXX" shape; unlikely to appear by accident in
// timestamps, IPs, or version strings.
//
// Credit card: 13-19 digits (with optional spaces/dashes between
// groups) AND a valid Luhn checksum. The Luhn gate is essential —
// without it, any 13+-digit run (long IDs, hashes) would false-fire.
var piiSSNAndCC = []piiPattern{
	{
		name:  "pii_ssn",
		regex: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	},
	{
		name:  "pii_credit_card",
		regex: regexp.MustCompile(`\b(?:\d[ \-]?){13,19}\b`),
		confirm: func(matched string) bool {
			digits := stripNonDigits(matched)
			if len(digits) < 13 || len(digits) > 19 {
				return false
			}
			return luhnValid(digits)
		},
	},
}

// piiEmailPattern is configured separately because emails appear in
// legitimate documentation contexts (maintainer addresses, support
// contacts, example syntax). Opt-in via manifest.security.pii_scan_email.
//
// The regex is intentionally permissive — it favours catching emails
// over rejecting marginally non-RFC-compliant ones. Domain part
// requires at least one dot and a 2+-char TLD.
var piiEmailPattern = piiPattern{
	name:  "pii_email",
	regex: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),
}

// scanPIIInto walks the PII patterns and appends findings to existing.
// The existing slice may already contain secret findings; PII walks
// after secrets so the entropy-dedup check inside Scan sees the secret
// findings first.
//
// Same allowlist behaviour as the secret scanner: matches fully inside
// an allowlist region are dropped.
func scanPIIInto(existing []Finding, content []byte, opts ScanOpts) []Finding {
	if opts.PIIScanSSNAndCC {
		for _, p := range piiSSNAndCC {
			existing = appendPIIMatches(existing, content, p, opts)
		}
	}
	if opts.PIIScanEmail {
		existing = appendPIIMatches(existing, content, piiEmailPattern, opts)
	}
	return existing
}

func appendPIIMatches(existing []Finding, content []byte, p piiPattern, opts ScanOpts) []Finding {
	for _, m := range p.regex.FindAllIndex(content, -1) {
		start, end := m[0], m[1]
		if IsAllowlisted(start, end, opts.Allowlist) {
			continue
		}
		if p.confirm != nil && !p.confirm(string(content[start:end])) {
			continue
		}
		existing = append(existing, Finding{
			Type:                p.name,
			Line:                byteToLine(content, start),
			ApproximateLocation: locationFor(content, start),
		})
	}
	return existing
}

// luhnValid runs the Luhn algorithm on a digits-only string. Standard
// "double every second digit from the right, sum, mod 10 == 0".
//
// Used as a confirmation gate after the credit-card regex matches —
// most random 13-19-digit runs (long hashes, fake test IDs) won't
// satisfy Luhn, so this drops the false-positive rate by orders of
// magnitude. Real credit card numbers are Luhn-valid by construction.
func luhnValid(digits string) bool {
	if len(digits) < 2 {
		return false
	}
	sum := 0
	// Walk right-to-left. parity tracks whether the current digit
	// position should be doubled.
	parity := len(digits) % 2
	for i, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// stripNonDigits removes any character that isn't 0-9. Used to canonicalise
// credit-card matches (the regex allows " " and "-" between digit groups)
// before handing to luhnValid.
func stripNonDigits(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			out = append(out, c)
		}
	}
	return string(out)
}

// ClassifyFindings inspects a mixed Findings slice and returns the
// appropriate reject reason: ReasonSecretDetected if ANY finding is a
// credential (Type doesn't start with PIIPrefix), ReasonPIIDetected
// otherwise. Used by the orchestrator + rebase paths so callers don't
// have to walk the slice manually.
//
// An empty slice returns "" — caller checks len() first.
func ClassifyFindings(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	for _, f := range findings {
		if !startsWithPIIPrefix(f.Type) {
			return ReasonSecretDetected
		}
	}
	return ReasonPIIDetected
}

func startsWithPIIPrefix(s string) bool {
	if len(s) < len(PIIPrefix) {
		return false
	}
	return s[:len(PIIPrefix)] == PIIPrefix
}
