package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed series.txt
var devSeries string

func defaultVersion() string {
	series := strings.TrimSpace(devSeries)
	if series == "" {
		return "dev"
	}
	return series + "-dev"
}

// Version information is injected at build time via -ldflags.
var (
	// Keep Version as a plain string so `go build -ldflags -X` can override it.
	Version   string
	BuildTime = "unknown"
	GitCommit = "none"
)

func init() {
	if strings.TrimSpace(Version) == "" {
		Version = defaultVersion()
	}
}
