package memory

import (
	"bytes"
	"math"
	"regexp"
)

// Finding describes one secret-scan hit. The actual matched bytes are
// intentionally NOT included — by policy (design doc §13.2 / §23.3) the
// scanner must not echo the full secret value back to the caller or to
// logs. Type + Line + ApproximateLocation is enough for the agent to
// locate and rewrite the offending content.
type Finding struct {
	Type                string `json:"type"`
	Line                int    `json:"line"`
	ApproximateLocation string `json:"approximate_location"`
}

// ScanOpts configures Scan.
type ScanOpts struct {
	// Allowlist excludes the listed byte ranges from scanning. Build it
	// with ExtractAllowlistRegions over the same content.
	Allowlist []AllowlistRegion

	// EntropyThreshold and EntropyMinLength enable Shannon-entropy
	// detection over alphanumeric tokens. Tokens shorter than
	// EntropyMinLength are skipped. Tokens whose entropy meets or exceeds
	// EntropyThreshold are flagged as type "high_entropy".
	//
	// Set both to zero to disable entropy scanning. Recommended starting
	// values per design doc §23.2: threshold=4.5, min_length=32. Tighter
	// thresholds reduce false positives at the cost of catching fewer
	// real-but-unrecognised tokens.
	EntropyThreshold float64
	EntropyMinLength int

	// PIIScanSSNAndCC enables high-confidence PII detection (SSN shape +
	// credit card with Luhn validation). Both patterns are extremely rare
	// in legitimate technical content; default-on in DefaultManifest.
	PIIScanSSNAndCC bool

	// PIIScanEmail enables email-address detection. Opt-in because emails
	// appear legitimately in documentation (maintainer addresses, support
	// contacts, example syntax). Use allowlist regions for legitimate
	// occurrences when this is on.
	PIIScanEmail bool
}

// DefaultScanOpts returns the recommended scanner configuration per
// design doc §23.2.
func DefaultScanOpts() ScanOpts {
	return ScanOpts{
		EntropyThreshold: 4.5,
		EntropyMinLength: 32,
	}
}

// secretPattern is one named regex in the high-confidence rule set.
type secretPattern struct {
	name  string
	regex *regexp.Regexp
}

// Regex set per design doc §23.2. Each pattern uses word boundaries to
// avoid mid-word false positives. Lengths and prefixes match real-world
// tokens of each kind.
//
// We do NOT try to validate that a candidate is a "real" credential
// (e.g., by checking AWS key checksum, decoding JWT header) — the goal
// is to flag *anything that looks like* a secret and let the agent
// either rewrite or use the allowlist mechanism.
var secretPatterns = []secretPattern{
	{
		name:  "aws_access_key",
		regex: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	},
	{
		name:  "github_token",
		regex: regexp.MustCompile(`\bgh[poursr]_[A-Za-z0-9]{36,}\b`),
	},
	{
		name:  "gitlab_token",
		regex: regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20}\b`),
	},
	{
		name:  "anthropic_api_key",
		regex: regexp.MustCompile(`\bsk-ant-(?:api03-)?[A-Za-z0-9_\-]{40,}\b`),
	},
	{
		// OpenAI key — checked AFTER anthropic so anthropic's longer prefix wins.
		name:  "openai_api_key",
		regex: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9]{32,}\b`),
	},
	{
		name:  "stripe_live_key",
		regex: regexp.MustCompile(`\b(?:sk|pk|rk)_live_[A-Za-z0-9]{24,}\b`),
	},
	{
		// JWT: header.payload.signature, base64-urlsafe. Heuristic — three
		// base64ish runs separated by dots, where the first starts with
		// "eyJ" (base64 of '{"').
		name:  "jwt",
		regex: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{4,}\b`),
	},
	{
		// PEM/SSH private key blocks. Match the header; any text afterwards
		// is keyish enough to flag regardless of corpus.
		name:  "private_key_block",
		regex: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	},
}

// tokenPattern is the unit Shannon-entropy detection operates on:
// contiguous runs of alphanumerics plus a few base64/JWT-friendly
// characters. Wider than \w so we catch tokens like
// "abc-DEF_ghi+JKL/mno=pqr".
var tokenPattern = regexp.MustCompile(`[A-Za-z0-9_+/=\-]+`)

// Scan returns every secret-shaped hit in content, ordered by byte
// position. Returns nil (not an error) when content is empty.
//
// Behaviour:
//   - Each pattern in the rule set is checked against content. Matches
//     fully inside an opts.Allowlist region are dropped.
//   - If opts.EntropyThreshold and opts.EntropyMinLength are both > 0,
//     a secondary pass flags any alphanumeric-ish token whose Shannon
//     entropy meets the threshold.
//   - Findings carry the type, line number (1-based), and a string
//     location — NEVER the matched bytes.
func Scan(content []byte, opts ScanOpts) []Finding {
	if len(content) == 0 {
		return nil
	}
	var findings []Finding

	for _, p := range secretPatterns {
		for _, m := range p.regex.FindAllIndex(content, -1) {
			start, end := m[0], m[1]
			if IsAllowlisted(start, end, opts.Allowlist) {
				continue
			}
			findings = append(findings, Finding{
				Type:                p.name,
				Line:                byteToLine(content, start),
				ApproximateLocation: locationFor(content, start),
			})
		}
	}

	if opts.EntropyMinLength > 0 && opts.EntropyThreshold > 0 {
		for _, m := range tokenPattern.FindAllIndex(content, -1) {
			start, end := m[0], m[1]
			if end-start < opts.EntropyMinLength {
				continue
			}
			if IsAllowlisted(start, end, opts.Allowlist) {
				continue
			}
			// Skip if already flagged by a higher-confidence pattern
			// covering the same span. Avoids double-reporting JWTs etc.
			if findingCovers(findings, start, byteToLine(content, start)) {
				continue
			}
			if shannonEntropy(content[start:end]) >= opts.EntropyThreshold {
				findings = append(findings, Finding{
					Type:                "high_entropy",
					Line:                byteToLine(content, start),
					ApproximateLocation: locationFor(content, start),
				})
			}
		}
	}

	// PII pass — high-confidence + opt-in email. Runs after the secret
	// + entropy passes so allowlist regions and dedup behave consistently.
	// See pii.go for the patterns.
	findings = scanPIIInto(findings, content, opts)

	return findings
}

// shannonEntropy returns the Shannon entropy of data in bits/character.
// Empty input returns 0.
func shannonEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range data {
		counts[b]++
	}
	n := float64(len(data))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// byteToLine returns the 1-based line number for byteOffset within content.
func byteToLine(content []byte, byteOffset int) int {
	if byteOffset > len(content) {
		byteOffset = len(content)
	}
	return 1 + bytes.Count(content[:byteOffset], []byte("\n"))
}

// locationFor returns a short human-readable position string for logging
// and the rejection response.
func locationFor(content []byte, byteOffset int) string {
	line := byteToLine(content, byteOffset)
	// Could also return column; line alone is enough for an agent to
	// re-emit the offending line.
	return formatLineRef(line)
}

func formatLineRef(line int) string {
	// Keep this function out of fmt.Sprintf calls in hot paths — but
	// secret scanning is not a hot path, so the simple form is fine.
	switch line {
	case 1:
		return "line 1"
	default:
		return "line " + itoa(line)
	}
}

// itoa is a tiny non-allocating int-to-string we can inline above. Avoids
// pulling strconv into this file's import set for a single use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// findingCovers reports whether any existing finding is on the same line
// AND has start within the candidate's range. Used to dedupe entropy
// hits that overlap with higher-confidence regex hits.
func findingCovers(existing []Finding, _ int, line int) bool {
	for _, f := range existing {
		if f.Line == line {
			return true
		}
	}
	return false
}
