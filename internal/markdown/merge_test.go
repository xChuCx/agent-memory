package markdown

import (
	"strings"
	"testing"
)

const mergeBase = `# Decisions
<!-- @id: decisions -->

Project decisions land here.

## Use Postgres
<!-- @id: postgres -->

Chose Postgres for transactional storage.
`

func TestMerge3Way_IdenticalRoundTrips(t *testing.T) {
	got, conf, warns, err := Merge3Way([]byte(mergeBase), []byte(mergeBase), []byte(mergeBase))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mergeBase {
		t.Errorf("identical merge must reproduce the file exactly:\n--- got ---\n%s", got)
	}
	if conf || len(warns) != 0 {
		t.Errorf("identical merge: conflicted=%v warns=%v", conf, warns)
	}
}

func TestMerge3Way_BothAppendDifferentSections(t *testing.T) {
	// The headline case: two branches each append a new decision. The merge
	// must keep BOTH, in base-then-ours-then-theirs order, with no conflict.
	ours := mergeBase + `
## Use Kafka
<!-- @id: kafka -->

Event streaming via Kafka.
`
	theirs := mergeBase + `
## Use Redis
<!-- @id: redis -->

Cache hot keys in Redis.
`
	got, conf, _, err := Merge3Way([]byte(mergeBase), []byte(ours), []byte(theirs))
	if err != nil {
		t.Fatal(err)
	}
	if conf {
		t.Error("non-overlapping appends must not conflict")
	}
	s := string(got)
	for _, id := range []string{"@id: postgres", "@id: kafka", "@id: redis"} {
		if !strings.Contains(s, id) {
			t.Errorf("merged file missing %q:\n%s", id, s)
		}
	}
	if strings.Contains(s, "@merge-conflict") {
		t.Errorf("unexpected conflict block:\n%s", s)
	}
	// Order: postgres (base) → kafka (ours) → redis (theirs).
	pi, ki, ri := strings.Index(s, "@id: postgres"), strings.Index(s, "@id: kafka"), strings.Index(s, "@id: redis")
	if !(pi < ki && ki < ri) {
		t.Errorf("section order wrong: postgres=%d kafka=%d redis=%d", pi, ki, ri)
	}
}

func TestMerge3Way_OneSideModifiesSection(t *testing.T) {
	ours := strings.Replace(mergeBase, "Chose Postgres for transactional storage.", "Chose Postgres; MySQL was considered.", 1)
	// theirs == base (unchanged). Merge takes ours' edit, no conflict.
	got, conf, _, err := Merge3Way([]byte(mergeBase), []byte(ours), []byte(mergeBase))
	if err != nil {
		t.Fatal(err)
	}
	if conf {
		t.Error("one-sided edit must not conflict")
	}
	if !strings.Contains(string(got), "MySQL was considered") {
		t.Errorf("merge dropped the one-sided edit:\n%s", got)
	}
}

func TestMerge3Way_BothModifySameSectionConflicts(t *testing.T) {
	ours := strings.Replace(mergeBase, "Chose Postgres for transactional storage.", "Chose Postgres for its JSONB support.", 1)
	theirs := strings.Replace(mergeBase, "Chose Postgres for transactional storage.", "Chose Postgres for strong transactions.", 1)
	got, conf, _, err := Merge3Way([]byte(mergeBase), []byte(ours), []byte(theirs))
	if err != nil {
		t.Fatal(err)
	}
	if !conf {
		t.Error("divergent edits to the same section must conflict")
	}
	s := string(got)
	if !strings.Contains(s, "@merge-conflict @postgres") {
		t.Errorf("missing scoped conflict marker:\n%s", s)
	}
	if !strings.Contains(s, "JSONB support") || !strings.Contains(s, "strong transactions") {
		t.Errorf("conflict block must carry both versions:\n%s", s)
	}
}

func TestMerge3Way_DeleteKeepsSurviving(t *testing.T) {
	// ours removes the postgres section; theirs leaves it untouched.
	ours := `# Decisions
<!-- @id: decisions -->

Project decisions land here.
`
	got, conf, warns, err := Merge3Way([]byte(mergeBase), []byte(ours), []byte(mergeBase))
	if err != nil {
		t.Fatal(err)
	}
	if conf {
		t.Error("delete-vs-unchanged should not hard-conflict (memory is retained)")
	}
	if !strings.Contains(string(got), "@id: postgres") {
		t.Errorf("deleted-on-one-side section must be retained:\n%s", got)
	}
	if len(warns) == 0 {
		t.Error("expected a warning about retaining a deleted section")
	}
}

func TestMerge3Way_BothDeleteHonoured(t *testing.T) {
	dropped := `# Decisions
<!-- @id: decisions -->

Project decisions land here.
`
	got, conf, _, err := Merge3Way([]byte(mergeBase), []byte(dropped), []byte(dropped))
	if err != nil {
		t.Fatal(err)
	}
	if conf {
		t.Fatal("both-deleted should not conflict")
	}
	if strings.Contains(string(got), "@id: postgres") {
		t.Errorf("both sides deleted the section; it should be gone:\n%s", got)
	}
}
