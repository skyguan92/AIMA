package skills

import "embed"

// FS embeds the AIMA OpenClaw skills.
//
//go:embed aima-control aima-image-gen aima-tts aima-asr
var FS embed.FS
