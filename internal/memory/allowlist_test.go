package memory

import (
	"strings"
	"testing"
)

// =============================================================================
// CheckAllowlistLimits — the hardening guardrail
// =============================================================================

func TestCheckAllowlistLimits_Passes_WhenAllWithinLimits(t *testing.T) {
	regions := []AllowlistRegion{
		{ByteStart: 0, ByteEnd: 100, Reason: "a"},
		{ByteStart: 200, ByteEnd: 300, Reason: "b"},
	}
	limits := AllowlistLimits{
		MaxBytesPerFile:   1024,
		MaxRegionsPerFile: 10,
		MaxBytesPerRegion: 512,
	}
	if msg := CheckAllowlistLimits(regions, limits); msg != "" {
		t.Errorf("CheckAllowlistLimits unexpectedly rejected: %q", msg)
	}
}

func TestCheckAllowlistLimits_RegionCountExceeded(t *testing.T) {
	regions := make([]AllowlistRegion, 11)
	for i := range regions {
		regions[i] = AllowlistRegion{ByteStart: i * 10, ByteEnd: i*10 + 5, Reason: "x"}
	}
	limits := AllowlistLimits{MaxRegionsPerFile: 10}
	msg := CheckAllowlistLimits(regions, limits)
	if msg == "" || !strings.Contains(msg, "regions = 11") {
		t.Errorf("expected region-count error mentioning 11, got %q", msg)
	}
}

func TestCheckAllowlistLimits_SingleRegionTooLarge(t *testing.T) {
	regions := []AllowlistRegion{
		{ByteStart: 0, ByteEnd: 1000, Reason: "huge"},
	}
	limits := AllowlistLimits{MaxBytesPerRegion: 512}
	msg := CheckAllowlistLimits(regions, limits)
	if msg == "" || !strings.Contains(msg, "max allowed = 512") {
		t.Errorf("expected per-region size error, got %q", msg)
	}
	if !strings.Contains(msg, `reason="huge"`) {
		t.Errorf("error should mention the region's reason: %q", msg)
	}
}

func TestCheckAllowlistLimits_TotalBytesExceeded(t *testing.T) {
	// Each region is within MaxBytesPerRegion, but the sum exceeds
	// MaxBytesPerFile.
	regions := []AllowlistRegion{
		{ByteStart: 0, ByteEnd: 400, Reason: "a"},
		{ByteStart: 500, ByteEnd: 900, Reason: "b"},
		{ByteStart: 1000, ByteEnd: 1400, Reason: "c"},
	}
	limits := AllowlistLimits{
		MaxBytesPerFile:   1000,
		MaxRegionsPerFile: 10,
		MaxBytesPerRegion: 500,
	}
	msg := CheckAllowlistLimits(regions, limits)
	if msg == "" || !strings.Contains(msg, "max allowed = 1000") {
		t.Errorf("expected total-bytes error mentioning 1000-byte cap, got %q", msg)
	}
}

func TestCheckAllowlistLimits_ZeroLimitsDisableCheck(t *testing.T) {
	// Limits all 0 → no check fires regardless of region counts/sizes.
	regions := make([]AllowlistRegion, 100)
	for i := range regions {
		regions[i] = AllowlistRegion{ByteStart: i * 10000, ByteEnd: i*10000 + 5000, Reason: "x"}
	}
	if msg := CheckAllowlistLimits(regions, AllowlistLimits{}); msg != "" {
		t.Errorf("zero limits should disable the check; got error %q", msg)
	}
}

func TestCheckAllowlistLimits_RegionCountCheckedFirst(t *testing.T) {
	// If both region-count AND total-bytes would fire, the region-count
	// error wins (documented order). 11 regions of 1 byte each.
	regions := make([]AllowlistRegion, 11)
	for i := range regions {
		regions[i] = AllowlistRegion{ByteStart: i * 100, ByteEnd: i*100 + 1, Reason: "x"}
	}
	limits := AllowlistLimits{
		MaxBytesPerFile:   5,
		MaxRegionsPerFile: 10,
	}
	msg := CheckAllowlistLimits(regions, limits)
	if !strings.Contains(msg, "regions = 11") {
		t.Errorf("region-count error should win; got %q", msg)
	}
}

func TestExtractAllowlistRegions_NoMarkers(t *testing.T) {
	regions, err := ExtractAllowlistRegions([]byte("plain text\nno markers here\n"))
	if err != nil {
		t.Fatal(err)
	}
	if regions != nil {
		t.Errorf("expected nil regions, got %+v", regions)
	}
}

func TestExtractAllowlistRegions_SinglePair(t *testing.T) {
	src := []byte(`prefix
<!-- @secret-scan: allow reason="docs example" -->
allowed body
<!-- @secret-scan: end -->
suffix
`)
	regions, err := ExtractAllowlistRegions(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(regions))
	}
	r := regions[0]
	if r.Reason != "docs example" {
		t.Errorf("Reason = %q, want 'docs example'", r.Reason)
	}
	// The body must be inside the range.
	body := string(src[r.ByteStart:r.ByteEnd])
	if !strings.Contains(body, "allowed body") {
		t.Errorf("region doesn't include body: %q", body)
	}
	// The markers themselves must NOT be inside the range.
	if strings.Contains(body, "@secret-scan: allow") {
		t.Errorf("open marker leaked into region body")
	}
	if strings.Contains(body, "@secret-scan: end") {
		t.Errorf("end marker leaked into region body")
	}
}

func TestExtractAllowlistRegions_MultiplePairs(t *testing.T) {
	src := []byte(`A
<!-- @secret-scan: allow reason="one" -->
first
<!-- @secret-scan: end -->
B
<!-- @secret-scan: allow reason="two" -->
second
<!-- @secret-scan: end -->
C
`)
	regions, err := ExtractAllowlistRegions(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(regions))
	}
	if regions[0].Reason != "one" || regions[1].Reason != "two" {
		t.Errorf("reasons: %q, %q (want 'one', 'two')", regions[0].Reason, regions[1].Reason)
	}
	if !(regions[0].ByteEnd < regions[1].ByteStart) {
		t.Errorf("regions overlap or out of order: %+v", regions)
	}
}

func TestExtractAllowlistRegions_UnmatchedOpenIsError(t *testing.T) {
	src := []byte(`<!-- @secret-scan: allow reason="x" -->
body without close
`)
	_, err := ExtractAllowlistRegions(src)
	if err == nil {
		t.Error("expected error for unmatched open marker")
	}
	if !strings.Contains(err.Error(), "no matching end") {
		t.Errorf("error message: %v", err)
	}
}

func TestExtractAllowlistRegions_UnmatchedEndIsError(t *testing.T) {
	src := []byte(`prefix
<!-- @secret-scan: end -->
`)
	_, err := ExtractAllowlistRegions(src)
	if err == nil {
		t.Error("expected error for end without open")
	}
	if !strings.Contains(err.Error(), "no matching open") {
		t.Errorf("error message: %v", err)
	}
}

func TestExtractAllowlistRegions_NestedOpenIsError(t *testing.T) {
	src := []byte(`<!-- @secret-scan: allow reason="outer" -->
<!-- @secret-scan: allow reason="inner" -->
body
<!-- @secret-scan: end -->
<!-- @secret-scan: end -->
`)
	_, err := ExtractAllowlistRegions(src)
	if err == nil {
		t.Error("expected error for nested open")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Errorf("error doesn't mention nested: %v", err)
	}
}

func TestExtractAllowlistRegions_MissingReasonIsError(t *testing.T) {
	src := []byte(`<!-- @secret-scan: allow -->
body
<!-- @secret-scan: end -->
`)
	_, err := ExtractAllowlistRegions(src)
	if err == nil {
		t.Error("expected error for missing reason attribute")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("error doesn't mention reason: %v", err)
	}
}

func TestExtractAllowlistRegions_EmptyReasonIsError(t *testing.T) {
	src := []byte(`<!-- @secret-scan: allow reason="" -->
body
<!-- @secret-scan: end -->
`)
	_, err := ExtractAllowlistRegions(src)
	if err == nil {
		t.Error("expected error for empty reason")
	}
}

func TestExtractAllowlistRegions_FlexibleWhitespace(t *testing.T) {
	// Markers may have extra whitespace inside the comment.
	src := []byte(`<!--    @secret-scan:   allow   reason="ok"    -->
body
<!--   @secret-scan:   end   -->
`)
	regions, err := ExtractAllowlistRegions(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 1 {
		t.Errorf("expected 1 region with flexible whitespace, got %d", len(regions))
	}
}

func TestIsAllowlisted(t *testing.T) {
	regions := []AllowlistRegion{
		{ByteStart: 10, ByteEnd: 50, Reason: "x"},
		{ByteStart: 100, ByteEnd: 150, Reason: "y"},
	}
	cases := []struct {
		name       string
		start, end int
		want       bool
	}{
		{"fully inside first", 20, 30, true},
		{"exactly at edges of first", 10, 50, true},
		{"straddles first edge low", 5, 30, false},
		{"straddles first edge high", 40, 60, false},
		{"between regions", 70, 80, false},
		{"fully inside second", 110, 140, true},
		{"after all regions", 200, 210, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsAllowlisted(c.start, c.end, regions)
			if got != c.want {
				t.Errorf("IsAllowlisted(%d, %d) = %v, want %v", c.start, c.end, got, c.want)
			}
		})
	}
}
