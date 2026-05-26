package markdown

import "testing"

func TestValidateMarkdown_HappyPaths(t *testing.T) {
	cases := []string{
		"",
		"plain text\n",
		"# Heading\n\nbody\n",
		"## H\n<!-- @id: h -->\n\n- list\n- item\n\n```\ncode\n```\n",
		"---\nfront: matter\n---\n\n# After\n",
	}
	for _, src := range cases {
		if err := ValidateMarkdown([]byte(src)); err != nil {
			t.Errorf("ValidateMarkdown(%q): %v", src, err)
		}
	}
}

func TestCountSections(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"empty", "", 0},
		{"plain text", "no headings here\n", 0},
		{"one", "# A\n", 1},
		{"three", "# A\n## B\n## C\n", 3},
		{"nested", "# A\n## B\n### C\n## D\n", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CountSections([]byte(c.src))
			if err != nil {
				t.Fatalf("CountSections: %v", err)
			}
			if got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
