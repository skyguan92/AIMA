package skills

import "embed"

// FS embeds the AIMA OpenClaw skills (aima-image-gen, aima-tts, aima-asr).
// Each skill is a directory with SKILL.md + scripts/.
//
//go:embed aima-image-gen aima-tts aima-asr
var FS embed.FS
