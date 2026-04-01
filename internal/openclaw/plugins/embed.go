package plugins

import "embed"

// FS embeds the AIMA OpenClaw plugins.
//
//go:embed aima-local-image
var FS embed.FS
