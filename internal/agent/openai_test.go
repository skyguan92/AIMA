package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClient_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			json.NewEncoder(w).Encode(chatResponse{
				Choices: []chatChoice{
					{Message: chatMessage{Role: "assistant", Content: "Hello!"}},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("test-model"))
	resp, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("content = %q, want Hello!", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("tool calls = %d, want 0", len(resp.ToolCalls))
	}
}

func TestOpenAIClient_ToolCallsResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			json.NewEncoder(w).Encode(chatResponse{
				Choices: []chatChoice{
					{Message: chatMessage{
						Role: "assistant",
						ToolCalls: []chatToolCall{
							{
								ID:   "call_1",
								Type: "function",
								Function: chatFunction{
									Name:      "hardware.detect",
									Arguments: `{"verbose":true}`,
								},
							},
							{
								ID:   "call_2",
								Type: "function",
								Function: chatFunction{
									Name:      "model.list",
									Arguments: `{}`,
								},
							},
						},
					}},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("test-model"))
	resp, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "What hardware?"},
	}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "hardware.detect" {
		t.Errorf("tool[0].Name = %q, want hardware.detect", resp.ToolCalls[0].Name)
	}
	if resp.ToolCalls[0].ID != "call_1" {
		t.Errorf("tool[0].ID = %q, want call_1", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Arguments != `{"verbose":true}` {
		t.Errorf("tool[0].Arguments = %q", resp.ToolCalls[0].Arguments)
	}
	if resp.ToolCalls[1].Name != "model.list" {
		t.Errorf("tool[1].Name = %q, want model.list", resp.ToolCalls[1].Name)
	}
}

func TestOpenAIClient_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "ok"}},
			},
		})
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("m"), WithAPIKey("sk-test-123"))
	_, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "test"},
	}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Errorf("auth = %q, want Bearer sk-test-123", gotAuth)
	}
}

func TestOpenAIClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("m"))
	_, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "test"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
}

func TestOpenAIClient_ModelAutoDiscover(t *testing.T) {
	var requestedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(modelsResponse{
				Data: []modelData{
					{ID: "qwen3-8b"},
					{ID: "glm-4"},
				},
			})
		case "/v1/chat/completions":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			requestedModel, _ = req["model"].(string)
			json.NewEncoder(w).Encode(chatResponse{
				Choices: []chatChoice{
					{Message: chatMessage{Role: "assistant", Content: "ok"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL + "/v1") // no WithModel
	_, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "test"},
	}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if requestedModel != "qwen3-8b" {
		t.Errorf("model = %q, want qwen3-8b (first from /models)", requestedModel)
	}
}

func TestOpenAIClient_ToolDefinitionsSent(t *testing.T) {
	var reqBody map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			json.NewDecoder(r.Body).Decode(&reqBody)
			json.NewEncoder(w).Encode(chatResponse{
				Choices: []chatChoice{
					{Message: chatMessage{Role: "assistant", Content: "ok"}},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("m"))
	tools := []ToolDefinition{
		{Name: "hw.detect", Description: "Detect hardware", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	_, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "test"},
	}, tools)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	var receivedTools []chatTool
	if err := json.Unmarshal(reqBody["tools"], &receivedTools); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if len(receivedTools) != 1 {
		t.Fatalf("tools sent = %d, want 1", len(receivedTools))
	}
	// Wire name should be sanitized: "hw.detect" → "hw__detect"
	if receivedTools[0].Function.Name != "hw__detect" {
		t.Errorf("tool name = %q, want hw__detect", receivedTools[0].Function.Name)
	}
}

func TestOpenAIClient_Available(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(modelsResponse{Data: []modelData{{ID: "m"}}})
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL + "/v1")
	if !client.Available(context.Background()) {
		t.Error("Available() = false, want true")
	}

	srv.Close()
	client2 := NewOpenAIClient(srv.URL + "/v1")
	if client2.Available(context.Background()) {
		t.Error("Available() = true after close, want false")
	}
}

func TestOpenAIClient_FleetDiscovery_EmptyModels(t *testing.T) {
	// Local server returns empty model list
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(modelsResponse{Data: []modelData{}})
	}))
	defer srv.Close()

	// Fleet discover returns a remote endpoint
	discoverCalled := false
	discover := func(ctx context.Context, apiKey string) []FleetEndpoint {
		discoverCalled = true
		return []FleetEndpoint{{BaseURL: srv.URL + "/v1", Model: "remote-qwen3"}}
	}

	client := NewOpenAIClient(srv.URL+"/v1", WithDiscoverFunc(discover))
	model, err := client.resolveModel(context.Background())
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if !discoverCalled {
		t.Error("expected discover function to be called")
	}
	if model != "remote-qwen3" {
		t.Errorf("model = %q, want remote-qwen3", model)
	}
}

func TestOpenAIClient_FleetDiscovery_Unreachable(t *testing.T) {
	// Fleet discover returns a remote endpoint when local is unreachable
	discover := func(ctx context.Context, apiKey string) []FleetEndpoint {
		return []FleetEndpoint{{BaseURL: "http://10.0.0.1:6188/v1", Model: "remote-model"}}
	}

	client := NewOpenAIClient("http://127.0.0.1:1/v1", WithDiscoverFunc(discover))
	model, err := client.resolveModel(context.Background())
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if model != "remote-model" {
		t.Errorf("model = %q, want remote-model", model)
	}
}

func TestOpenAIClient_FleetDiscovery_NilFunc(t *testing.T) {
	// No discover function — original error propagated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(modelsResponse{Data: []modelData{}})
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL + "/v1") // no WithDiscoverFunc
	_, err := client.resolveModel(context.Background())
	if err == nil {
		t.Fatal("expected error when no models and no discover func")
	}
}

func TestOpenAIClient_FleetDiscovery_NoEndpoints(t *testing.T) {
	// Discover function returns empty — original error propagated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(modelsResponse{Data: []modelData{}})
	}))
	defer srv.Close()

	discover := func(ctx context.Context, apiKey string) []FleetEndpoint {
		return nil
	}

	client := NewOpenAIClient(srv.URL+"/v1", WithDiscoverFunc(discover))
	_, err := client.resolveModel(context.Background())
	if err == nil {
		t.Fatal("expected error when discover returns no endpoints")
	}
}

func TestOpenAIClient_ReasoningContentPreserved(t *testing.T) {
	var reqBody map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&reqBody)
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "result", ReasoningContent: "thought about it"}},
			},
		})
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("m"))
	resp, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "test"},
		{Role: "assistant", Content: "", ReasoningContent: "let me think"},
	}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	// Verify reasoning_content was sent in request
	var msgs []map[string]any
	if err := json.Unmarshal(reqBody["messages"], &msgs); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	rc, _ := msgs[1]["reasoning_content"].(string)
	if rc != "let me think" {
		t.Errorf("request reasoning_content = %q, want %q", rc, "let me think")
	}

	// Verify reasoning_content was parsed from response
	if resp.ReasoningContent != "thought about it" {
		t.Errorf("response ReasoningContent = %q, want %q", resp.ReasoningContent, "thought about it")
	}
}

func TestOpenAIClient_ExtraParams(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&reqBody)
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "ok"}},
			},
		})
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("m"), WithExtraParams(map[string]any{
		"temperature": 0.6,
		"top_p":       0.95,
	}))
	_, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "user", Content: "test"},
	}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if temp, ok := reqBody["temperature"].(float64); !ok || temp != 0.6 {
		t.Errorf("temperature = %v, want 0.6", reqBody["temperature"])
	}
	if topP, ok := reqBody["top_p"].(float64); !ok || topP != 0.95 {
		t.Errorf("top_p = %v, want 0.95", reqBody["top_p"])
	}
	// model/messages must not be overridden by extra params
	if reqBody["model"] != "m" {
		t.Errorf("model = %v, want m", reqBody["model"])
	}
}

func TestOpenAIClient_ContentAlwaysPresent(t *testing.T) {
	var reqBody map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&reqBody)
		json.NewEncoder(w).Encode(chatResponse{
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "ok"}},
			},
		})
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL+"/v1", WithModel("m"))
	// Send assistant message with empty Content (should still serialize as "content":"")
	_, err := client.ChatCompletion(context.Background(), []Message{
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "hw.detect", Arguments: "{}"}}},
		{Role: "tool", Content: `{"gpu":"RTX 4060"}`, ToolCallID: "c1"},
	}, nil)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	// Verify "content" field is present even when empty
	raw := string(reqBody["messages"])
	if !strings.Contains(raw, `"content":""`) {
		t.Errorf("expected empty content field to be present in JSON, got: %s", raw)
	}
}
