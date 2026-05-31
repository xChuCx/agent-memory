package markdown

import (
	"bytes"
	"fmt"
)

// Merge3Way performs a section-aware three-way merge of a memory Markdown
// file: base is the common ancestor, ours/theirs the two sides. It exists so
// a git merge driver can union two branches' edits to a shared
// `.agent-memory/` file instead of producing whole-file conflict markers.
//
// The file is treated as a tree of sections keyed by their `@id` anchor (or,
// when un-anchored, by heading text + level + occurrence). The merge is
// recursive and byte-preserving — each kept section's bytes are copied
// verbatim from the side that owns them, so nothing is reflowed:
//
//   - present on both sides, unchanged or changed on only one side → take it;
//   - added on one side only → keep it (this is the common "both branches
//     appended a different decision/pitfall" case → clean union);
//   - changed on BOTH sides differently → a `<!-- @merge-conflict -->` block
//     wrapping both versions, and conflicted=true so the driver leaves the
//     file unmerged for a human;
//   - deleted on one side → the surviving version is kept and a warning is
//     recorded (memory is never silently lost; both-sides-deleted is honoured).
//
// conflicted reports whether any section needs human resolution; warnings
// lists non-fatal notes (kept-despite-delete). A parse failure on any input
// returns an error so the caller can fall back to git's default merge.
func Merge3Way(base, ours, theirs []byte) (result []byte, conflicted bool, warnings []string, err error) {
	bs, err := ParseSections(base)
	if err != nil {
		return nil, false, nil, fmt.Errorf("merge: parse base: %w", err)
	}
	os_, err := ParseSections(ours)
	if err != nil {
		return nil, false, nil, fmt.Errorf("merge: parse ours: %w", err)
	}
	ts, err := ParseSections(theirs)
	if err != nil {
		return nil, false, nil, fmt.Errorf("merge: parse theirs: %w", err)
	}

	// Bytes before the first heading (usually empty for memory files).
	preMerged, preConf := merge3("(preamble)", preamble(base, bs), preamble(ours, os_), preamble(theirs, ts))

	merged, warns, conf := mergeForest(
		buildForest(base, bs), buildForest(ours, os_), buildForest(theirs, ts),
	)

	var out bytes.Buffer
	out.Write(preMerged)
	out.Write(render(merged))
	return out.Bytes(), preConf || conf, warns, nil
}

// mnode is one node in the section tree: its own bytes (heading + anchor +
// body, excluding descendants) and its child sections.
type mnode struct {
	key      string
	own      []byte
	children []mnode
}

func preamble(src []byte, secs []Section) []byte {
	if len(secs) == 0 {
		return src
	}
	return src[:secs[0].ByteStart]
}

// sectionKey identifies a section across sides. Anchored sections key by
// @id (stable across moves/edits); un-anchored ones by heading identity.
func sectionKey(s Section) string {
	if s.AnchorID != "" {
		return "@" + s.AnchorID
	}
	return fmt.Sprintf("h%d:%s#%d", s.HeadingLevel, s.HeadingText, s.Occurrence)
}

// buildForest turns a document-ordered section slice into a sibling forest.
// secs is the full ParseSections output (or a sub-slice for a recursion);
// the shallowest heading level in the slice defines the sibling level, and
// deeper sections become children.
func buildForest(src []byte, secs []Section) []mnode {
	if len(secs) == 0 {
		return nil
	}
	minLevel := secs[0].HeadingLevel
	for _, s := range secs {
		if s.HeadingLevel < minLevel {
			minLevel = s.HeadingLevel
		}
	}
	var forest []mnode
	for i := 0; i < len(secs); {
		// secs[i] is a sibling at minLevel; its descendants run until the
		// next sibling at minLevel (or the end of the slice).
		j := i + 1
		for j < len(secs) && secs[j].HeadingLevel > minLevel {
			j++
		}
		ownEnd := secs[i].ByteEnd
		var kids []mnode
		if j > i+1 {
			ownEnd = secs[i+1].ByteStart
			kids = buildForest(src, secs[i+1:j])
		}
		forest = append(forest, mnode{
			key:      sectionKey(secs[i]),
			own:      src[secs[i].ByteStart:ownEnd],
			children: kids,
		})
		i = j
	}
	return forest
}

// render serializes a forest back to bytes. For unmodified nodes this
// reproduces the original bytes exactly (own + children partition the range).
func render(forest []mnode) []byte {
	var b bytes.Buffer
	for _, n := range forest {
		b.Write(n.own)
		b.Write(render(n.children))
	}
	return b.Bytes()
}

func indexForest(forest []mnode) map[string]*mnode {
	m := make(map[string]*mnode, len(forest))
	for i := range forest {
		// First occurrence of a key wins (duplicate keys within one side are
		// pathological; keep it deterministic).
		if _, dup := m[forest[i].key]; !dup {
			m[forest[i].key] = &forest[i]
		}
	}
	return m
}

// mergeForest merges sibling forests three-way, preserving order: base
// siblings first (in base order), then ours-only additions, then theirs-only.
func mergeForest(base, ours, theirs []mnode) ([]mnode, []string, bool) {
	bm, om, tm := indexForest(base), indexForest(ours), indexForest(theirs)
	var out []mnode
	var warns []string
	conflicted := false
	done := map[string]bool{}

	handle := func(key string) {
		if done[key] {
			return
		}
		done[key] = true
		b, o, t := bm[key], om[key], tm[key]

		switch {
		case o != nil && t != nil:
			// Present on both sides (b may be nil = added on both).
			var bOwn []byte
			var bKids []mnode
			if b != nil {
				bOwn, bKids = b.own, b.children
			}
			own, ownConf := merge3(key, bOwn, o.own, t.own)
			kids, kw, kidConf := mergeForest(bKids, o.children, t.children)
			warns = append(warns, kw...)
			out = append(out, mnode{key: key, own: own, children: kids})
			conflicted = conflicted || ownConf || kidConf
		case o != nil && t == nil:
			// theirs dropped it.
			out = append(out, *o)
			if b != nil {
				warns = append(warns, fmt.Sprintf("kept %s (deleted on the other side, retained from ours)", key))
			}
		case o == nil && t != nil:
			// ours dropped it.
			out = append(out, *t)
			if b != nil {
				warns = append(warns, fmt.Sprintf("kept %s (deleted on this side, retained from theirs)", key))
			}
			// o == nil && t == nil: present only in base → both deleted → drop.
		}
	}

	for i := range base {
		handle(base[i].key)
	}
	for i := range ours {
		handle(ours[i].key)
	}
	for i := range theirs {
		handle(theirs[i].key)
	}
	return out, warns, conflicted
}

// merge3 reconciles a node's own bytes three-way. Returns the chosen bytes
// and whether it's an unresolved conflict.
func merge3(key string, base, ours, theirs []byte) ([]byte, bool) {
	switch {
	case bytes.Equal(ours, theirs):
		return ours, false
	case bytes.Equal(ours, base):
		return theirs, false // only theirs changed
	case bytes.Equal(theirs, base):
		return ours, false // only ours changed
	default:
		return conflictBlock(key, ours, theirs), true
	}
}

// conflictBlock wraps two divergent versions in a human-resolvable block.
// The @merge-conflict comment carries the key so a reviewer knows which
// section diverged; the git-style markers make resolution familiar.
func conflictBlock(key string, ours, theirs []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<!-- @merge-conflict %s — resolve and delete these markers -->\n", key)
	b.WriteString("<<<<<<< ours\n")
	b.Write(withTrailingNewline(ours))
	b.WriteString("=======\n")
	b.Write(withTrailingNewline(theirs))
	b.WriteString(">>>>>>> theirs\n")
	b.WriteString("<!-- /@merge-conflict -->\n")
	return b.Bytes()
}

func withTrailingNewline(b []byte) []byte {
	if len(b) == 0 || b[len(b)-1] == '\n' {
		return b
	}
	return append(append([]byte{}, b...), '\n')
}
