package plugins

import "embed"

// FS embeds the AIMA OpenClaw plugins.
//
//go:embed aima-local-image aima-local-audio aima-local-tts
var FS embed.FS
