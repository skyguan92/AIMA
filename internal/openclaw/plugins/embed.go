package plugins

import "embed"

// FS embeds the AIMA OpenClaw plugins.
//
//go:embed aima-local-image aima-local-audio
var FS embed.FS
