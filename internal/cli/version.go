package cli

// Filled in by -ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// BuildInfo identifies the running binary.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func defaultBuildInfo() BuildInfo {
	return BuildInfo{Version: version, Commit: commit, Date: date}
}
