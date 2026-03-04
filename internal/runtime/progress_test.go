package runtime

import (
	"testing"

	"github.com/jguan/aima/internal/k3s"
	"github.com/jguan/aima/internal/knowledge"
)

func TestDetectStartupProgress(t *testing.T) {
	vllmPatterns := &knowledge.StartupLogPatterns{
		Phases: []knowledge.StartupPhasePattern{
			{Name: "model_init", Pattern: "Starting to load model", Progress: 5},
			{Name: "loading_shards", Pattern: `Loading safetensors checkpoint shards:\s+(\d+)%`, ProgressRegexGroup: 1, ProgressBase: 5, ProgressRange: 35},
			{Name: "weights_loaded", Pattern: "Loading weights took|Loading model weights", Progress: 40},
			{Name: "torch_compile", Pattern: `torch\.compile takes`, Progress: 50},
			{Name: "cuda_piecewise", Pattern: `Capturing CUDA graphs \(mixed.*?(\d+)%`, ProgressRegexGroup: 1, ProgressBase: 50, ProgressRange: 5},
			{Name: "cuda_full", Pattern: `Capturing CUDA graphs \(decode.*?(\d+)%`, ProgressRegexGroup: 1, ProgressBase: 55, ProgressRange: 35},
			{Name: "cuda_graphs", Pattern: `Capturing CUDA graph[^s].*?(\d+)%`, ProgressRegexGroup: 1, ProgressBase: 50, ProgressRange: 40},
			{Name: "graph_done", Pattern: "Graph capturing finished", Progress: 92},
			{Name: "ready", Pattern: "Application startup complete|Uvicorn running", Progress: 100},
		},
		Errors: []knowledge.StartupErrorPattern{
			{Pattern: "OutOfMemoryError|CUDA out of memory", Message: "GPU memory insufficient"},
		},
	}

	tests := []struct {
		name         string
		logText      string
		patterns     *knowledge.StartupLogPatterns
		wantPhase    string
		wantProgress int
	}{
		{
			name:         "nil patterns",
			logText:      "some log output",
			patterns:     nil,
			wantPhase:    "",
			wantProgress: 0,
		},
		{
			name:         "no match",
			logText:      "INFO: server config loaded\nINFO: Initializing",
			patterns:     vllmPatterns,
			wantPhase:    "",
			wantProgress: 0,
		},
		{
			name:         "model init",
			logText:      "INFO: Starting to load model /models...",
			patterns:     vllmPatterns,
			wantPhase:    "model_init",
			wantProgress: 5,
		},
		{
			name:         "safetensors shards 43%",
			logText:      "Starting to load model /models...\nLoading safetensors checkpoint shards:  43% Completed | 6/14",
			patterns:     vllmPatterns,
			wantPhase:    "loading_shards",
			wantProgress: 20, // 5 + (43 * 35 / 100) = 5 + 15
		},
		{
			name:         "safetensors shards 100%",
			logText:      "Starting to load model\nLoading safetensors checkpoint shards: 100% Completed | 14/14",
			patterns:     vllmPatterns,
			wantPhase:    "loading_shards",
			wantProgress: 40, // 5 + (100 * 35 / 100) = 40
		},
		{
			name:         "weights loaded (legacy format)",
			logText:      "INFO: Loading model weights took 5.2s",
			patterns:     vllmPatterns,
			wantPhase:    "weights_loaded",
			wantProgress: 40,
		},
		{
			name:         "torch compile done",
			logText:      "Loading weights took 78s\ntorch.compile takes 25.49 s in total",
			patterns:     vllmPatterns,
			wantPhase:    "torch_compile",
			wantProgress: 50,
		},
		{
			name:         "cuda piecewise 50%",
			logText:      "Loading weights took 78s\ntorch.compile takes 25s\nCapturing CUDA graphs (mixed prefill-decode, PIECEWISE): 50%|...",
			patterns:     vllmPatterns,
			wantPhase:    "cuda_piecewise",
			wantProgress: 52, // 50 + (50 * 5 / 100)
		},
		{
			name:         "cuda full 50%",
			logText:      "torch.compile takes 25s\nCapturing CUDA graphs (mixed prefill-decode, PIECEWISE): 100%\nCapturing CUDA graphs (decode, FULL): 50%|...",
			patterns:     vllmPatterns,
			wantPhase:    "cuda_full",
			wantProgress: 72, // 55 + (50 * 35 / 100) = 72; piecewise=55, torch=50 → highest is 72
		},
		{
			name:         "cuda full 100%",
			logText:      "torch.compile takes 25s\nCapturing CUDA graphs (decode, FULL): 100%",
			patterns:     vllmPatterns,
			wantPhase:    "cuda_full",
			wantProgress: 90, // 55 + (100 * 35 / 100)
		},
		{
			name:         "graph done",
			logText:      "Capturing CUDA graphs (decode, FULL): 100%\nGraph capturing finished in 57 secs",
			patterns:     vllmPatterns,
			wantPhase:    "graph_done",
			wantProgress: 92,
		},
		{
			name:         "ready",
			logText:      "Graph capturing finished in 57 secs\nApplication startup complete",
			patterns:     vllmPatterns,
			wantPhase:    "ready",
			wantProgress: 100,
		},
		{
			name:         "old vllm cuda graph format",
			logText:      "Loading model weights\nCapturing CUDA graph for batch size 1: 30%\nCapturing CUDA graph: 80%",
			patterns:     vllmPatterns,
			wantPhase:    "cuda_graphs",
			wantProgress: 82, // 50 + (80 * 40 / 100) = 82
		},
		{
			name:    "sglang server starting",
			logText: "loading weights\nThe server is fired up",
			patterns: &knowledge.StartupLogPatterns{
				Phases: []knowledge.StartupPhasePattern{
					{Name: "loading_weights", Pattern: "loading weights", Progress: 40},
					{Name: "server_starting", Pattern: "The server is fired up", Progress: 80},
					{Name: "ready", Pattern: "Application startup complete", Progress: 100},
				},
			},
			wantPhase:    "server_starting",
			wantProgress: 80,
		},
		{
			name:    "llamacpp ready",
			logText: "llm_load_print_meta: general.architecture\nHTTP server listening on 0.0.0.0:8080",
			patterns: &knowledge.StartupLogPatterns{
				Phases: []knowledge.StartupPhasePattern{
					{Name: "loading_model", Pattern: "llm_load_print_meta|loading model", Progress: 50},
					{Name: "ready", Pattern: "HTTP server listening|server listening", Progress: 100},
				},
			},
			wantPhase:    "ready",
			wantProgress: 100,
		},
		{
			name:         "no backward jump: piecewise done then full starts",
			logText:      "torch.compile takes 25s\nCapturing CUDA graphs (mixed prefill-decode, PIECEWISE): 100%\nCapturing CUDA graphs (decode, FULL): 2%",
			patterns:     vllmPatterns,
			wantPhase:    "cuda_piecewise",
			wantProgress: 55, // piecewise=55, full=55+(2*35/100)=55, torch=50 → highest=55
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectStartupProgress(tt.logText, tt.patterns)
			if got.Phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", got.Phase, tt.wantPhase)
			}
			if got.Progress != tt.wantProgress {
				t.Errorf("progress = %d, want %d", got.Progress, tt.wantProgress)
			}
		})
	}
}

func TestDetectStartupError(t *testing.T) {
	patterns := &knowledge.StartupLogPatterns{
		Errors: []knowledge.StartupErrorPattern{
			{Pattern: "OutOfMemoryError|CUDA out of memory", Message: "GPU memory insufficient"},
			{Pattern: "ImportError|ModuleNotFoundError", Message: "Missing Python dependency"},
		},
	}

	tests := []struct {
		name    string
		logText string
		want    string
	}{
		{"no error", "INFO: Server started", ""},
		{"OOM", "torch.cuda.OutOfMemoryError: CUDA out of memory", "GPU memory insufficient"},
		{"import error", "ModuleNotFoundError: No module named 'librosa'", "Missing Python dependency"},
		{"nil patterns", "some log", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := patterns
			if tt.name == "nil patterns" {
				p = nil
			}
			got := DetectStartupError(tt.logText, p)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectK3SPhaseFromConditions(t *testing.T) {
	tests := []struct {
		name             string
		conditions       []k3s.PodCondition
		containerRunning bool
		wantPhase        string
		wantProgress     int
	}{
		{
			name:             "container running",
			conditions:       []k3s.PodCondition{{Type: "PodScheduled", Status: "True"}},
			containerRunning: true,
			wantPhase:        "initializing",
			wantProgress:     20,
		},
		{
			name:             "not scheduled",
			conditions:       nil,
			containerRunning: false,
			wantPhase:        "scheduling",
			wantProgress:     2,
		},
		{
			name:             "scheduled not running",
			conditions:       []k3s.PodCondition{{Type: "PodScheduled", Status: "True"}},
			containerRunning: false,
			wantPhase:        "pulling_image",
			wantProgress:     10,
		},
		{
			name:             "scheduled false",
			conditions:       []k3s.PodCondition{{Type: "PodScheduled", Status: "False"}},
			containerRunning: false,
			wantPhase:        "scheduling",
			wantProgress:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase, progress := DetectK3SPhaseFromConditions(tt.conditions, tt.containerRunning)
			if phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", phase, tt.wantPhase)
			}
			if progress != tt.wantProgress {
				t.Errorf("progress = %d, want %d", progress, tt.wantProgress)
			}
		})
	}
}

func TestFormatPhaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"loading_weights", "Loading weights..."},
		{"cuda_graphs", "Cuda graphs..."},
		{"ready", "Ready..."},
		{"pulling_image", "Pulling image..."},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := formatPhaseName(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
