package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	state "github.com/jguan/aima/internal"
	"github.com/jguan/aima/internal/agent"
	"github.com/jguan/aima/internal/engine"
	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/proxy"
	aimaRuntime "github.com/jguan/aima/internal/runtime"
)

type deleteTrackingRuntime struct {
	name    string
	status  map[string]*aimaRuntime.DeploymentStatus
	list    []*aimaRuntime.DeploymentStatus
	delErrs map[string]error
	deleted []string
}

func (r *deleteTrackingRuntime) Deploy(context.Context, *aimaRuntime.DeployRequest) error { return nil }

func (r *deleteTrackingRuntime) Delete(_ context.Context, name string) error {
	r.deleted = append(r.deleted, name)
	if err, ok := r.delErrs[name]; ok && err != nil {
		return err
	}
	delete(r.status, name)
	for i, d := range r.list {
		if d != nil && d.Name == name {
			r.list = append(r.list[:i], r.list[i+1:]...)
			break
		}
	}
	return nil
}

func (r *deleteTrackingRuntime) Status(_ context.Context, name string) (*aimaRuntime.DeploymentStatus, error) {
	if s, ok := r.status[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("not found")
}

func (r *deleteTrackingRuntime) List(context.Context) ([]*aimaRuntime.DeploymentStatus, error) {
	return append([]*aimaRuntime.DeploymentStatus(nil), r.list...), nil
}

func (r *deleteTrackingRuntime) Logs(context.Context, string, int) (string, error) { return "", nil }
func (r *deleteTrackingRuntime) Name() string                                      { return r.name }

type noopToolExecutor struct{}

func (noopToolExecutor) ExecuteTool(context.Context, string, json.RawMessage) (*agent.ToolResult, error) {
	return nil, fmt.Errorf("unexpected tool call")
}

func (noopToolExecutor) ListTools() []agent.ToolDefinition { return nil }

func TestDeployDeleteRemovesProxyAndRecordsSnapshotAcrossRuntimeFallbacks(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	deploy := &aimaRuntime.DeploymentStatus{
		Name:          "qwen3-8b-vllm",
		Phase:         "running",
		Ready:         true,
		Address:       "127.0.0.1:8000",
		StartTime:     time.Now().UTC().Format(time.RFC3339),
		StartedAtUnix: time.Now().Unix(),
		Runtime:       "docker",
		Labels: map[string]string{
			"aima.dev/model":  "qwen3-8b",
			"aima.dev/engine": "vllm",
		},
	}

	primary := &deleteTrackingRuntime{
		name:    "k3s",
		status:  map[string]*aimaRuntime.DeploymentStatus{},
		delErrs: map[string]error{"qwen3-8b": fmt.Errorf("not found")},
	}
	dockerRt := &deleteTrackingRuntime{
		name:   "docker",
		status: map[string]*aimaRuntime.DeploymentStatus{deploy.Name: deploy},
		list:   []*aimaRuntime.DeploymentStatus{deploy},
		delErrs: map[string]error{
			"qwen3-8b": fmt.Errorf("not found"),
		},
	}

	proxyServer := proxy.NewServer()
	proxyServer.RegisterBackend("qwen3-8b", &proxy.Backend{
		ModelName:  "qwen3-8b",
		EngineType: "vllm",
		Address:    "127.0.0.1:8000",
		Ready:      true,
	})

	deps := &mcp.ToolDeps{}
	buildDeployDeps(&appContext{
		db:       db,
		rt:       primary,
		dockerRt: dockerRt,
		proxy:    proxyServer,
	}, deps,
		func(context.Context, string, func(string, string), func(int64, int64)) error { return nil },
		func(context.Context, string, string, string, map[string]any, bool, func(string, string), func(engine.ProgressEvent)) (json.RawMessage, error) {
			return nil, nil
		},
	)

	if err := deps.DeployDelete(ctx, "qwen3-8b"); err != nil {
		t.Fatalf("DeployDelete: %v", err)
	}

	if got := dockerRt.deleted; len(got) != 2 || got[0] != "qwen3-8b" || got[1] != deploy.Name {
		t.Fatalf("docker delete sequence = %v, want [qwen3-8b %s]", got, deploy.Name)
	}
	if backends := proxyServer.ListBackends(); len(backends) != 0 {
		t.Fatalf("proxy backends = %v, want empty after undeploy", backends)
	}

	snapshots, err := db.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshots len = %d, want 1", len(snapshots))
	}
	if snapshots[0].ResourceName != deploy.Name {
		t.Fatalf("snapshot resource_name = %q, want %q", snapshots[0].ResourceName, deploy.Name)
	}

	tombstones, err := db.ListDeletedDeploymentsSince(ctx, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("ListDeletedDeploymentsSince: %v", err)
	}
	for _, key := range []string{"qwen3-8b", deploy.Name} {
		if _, ok := tombstones[key]; !ok {
			t.Fatalf("missing deleted deployment tombstone for %q: %v", key, tombstones)
		}
	}
}

func TestBuildLLMClientRoutesAgentToConfiguredModel(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var (
		gotModel       string
		gotAuth        string
		gotUserAgent   string
		gotTemperature any
		gotTopP        any
		gotBody        []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotUserAgent = r.Header.Get("User-Agent")
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatalf("ReadAll: %v", readErr)
		}
		gotBody = append([]byte(nil), body...)

		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Unmarshal request: %v", err)
		}
		gotModel, _ = req["model"].(string)
		gotTemperature = req["temperature"]
		gotTopP = req["top_p"]

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	defer server.Close()

	if err := db.SetConfig(ctx, "llm.endpoint", server.URL+"/v1"); err != nil {
		t.Fatalf("SetConfig llm.endpoint: %v", err)
	}
	if err := db.SetConfig(ctx, "llm.model", "qwen3-8b"); err != nil {
		t.Fatalf("SetConfig llm.model: %v", err)
	}
	if err := db.SetConfig(ctx, "llm.api_key", "sk-local"); err != nil {
		t.Fatalf("SetConfig llm.api_key: %v", err)
	}
	if err := db.SetConfig(ctx, "llm.user_agent", "AIMA-UAT/1.0"); err != nil {
		t.Fatalf("SetConfig llm.user_agent: %v", err)
	}
	if err := db.SetConfig(ctx, "llm.extra_params", `{"temperature":0.25,"top_p":0.8}`); err != nil {
		t.Fatalf("SetConfig llm.extra_params: %v", err)
	}

	llm := buildLLMClient(ctx, db)
	goAgent := agent.NewAgent(llm, noopToolExecutor{})

	result, _, _, err := goAgent.Ask(ctx, "", "say pong")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if result != "pong" {
		t.Fatalf("result = %q, want pong", result)
	}
	if gotModel != "qwen3-8b" {
		t.Fatalf("model = %q, want qwen3-8b", gotModel)
	}
	if gotAuth != "Bearer sk-local" {
		t.Fatalf("authorization = %q, want Bearer sk-local", gotAuth)
	}
	if gotUserAgent != "AIMA-UAT/1.0" {
		t.Fatalf("user-agent = %q, want AIMA-UAT/1.0", gotUserAgent)
	}
	if gotTemperature != 0.25 {
		t.Fatalf("temperature = %#v, want 0.25", gotTemperature)
	}
	if gotTopP != 0.8 {
		t.Fatalf("top_p = %#v, want 0.8", gotTopP)
	}
	if !strings.Contains(string(gotBody), `"model":"qwen3-8b"`) {
		t.Fatalf("request body missing configured model: %s", string(gotBody))
	}
}
