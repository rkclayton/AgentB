package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime/debug"
	"strings"
	"sync"
)

// Commit and Dirty are set by release builds with -ldflags. Ordinary Go builds
// fall back to the VCS metadata embedded by the Go toolchain.
var (
	Commit string
	Dirty  string
	Tag    = "v0.42.0"
)

type Info struct {
	Tag              string `json:"tag"`
	Commit           string `json:"commit"`
	Dirty            bool   `json:"dirty"`
	Known            bool   `json:"known"`
	Source           string `json:"source"`
	Display          string `json:"display"`
	ExecutableSHA256 string `json:"executable_sha256"`
}

var executableHash = sync.OnceValue(func() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
})

func Current() Info {
	tag := strings.TrimSpace(Tag)
	commit := strings.TrimSpace(Commit)
	dirty, dirtyKnown := parseDirty(Dirty)
	source := "ldflags"
	if commit == "" {
		commit, dirty, dirtyKnown = vcsInfo()
		source = "go-vcs"
	}
	if commit == "" {
		return Info{Tag: tag, Commit: "unknown", Source: "unknown", Display: "unknown", ExecutableSHA256: executableHash()}
	}
	display := commit
	if len(display) > 12 {
		display = display[:12]
	}
	if dirtyKnown && dirty {
		display += "+dirty"
	}
	return Info{Tag: tag, Commit: commit, Dirty: dirtyKnown && dirty, Known: true, Source: source, Display: display, ExecutableSHA256: executableHash()}
}

func parseDirty(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "dirty":
		return true, true
	case "false", "0", "clean":
		return false, true
	default:
		return false, false
	}
}

func vcsInfo() (string, bool, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false, false
	}
	var commit string
	var dirty bool
	var dirtyKnown bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			commit = setting.Value
		case "vcs.modified":
			dirty, dirtyKnown = parseDirty(setting.Value)
		}
	}
	return commit, dirty, dirtyKnown
}
