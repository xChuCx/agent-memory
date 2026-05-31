package cli

import "runtime/debug"

// Version returns the version string shown by `agent-memory version`, sent
// in the MCP server handshake, and reported as memory.status.memory_version.
//
// It resolves so a binary always identifies itself usefully in a bug report,
// in this order:
//
//  1. ProgramVersion, if a release build stamped it via -ldflags (e.g. the
//     goreleaser archives → "v0.4.0").
//  2. else the module version from the build info — set when the binary was
//     installed with `go install …@v0.4.0` / `@latest` → e.g. "v0.4.0".
//     (This is the case `go install` users hit; without it they'd report
//     "dev" and we couldn't tell which version they ran.)
//  3. else "dev", enriched with the VCS revision (and a "dirty" marker) when
//     built from a checkout → e.g. "dev (a5a4b0c9, dirty)".
func Version() string {
	if ProgramVersion != "dev" {
		return ProgramVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ProgramVersion
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 8 {
		rev = rev[:8]
	}
	if dirty {
		rev += ", dirty"
	}
	return "dev (" + rev + ")"
}
