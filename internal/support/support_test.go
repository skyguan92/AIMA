package support

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServiceAskForHelpAndRun(t *testing.T) {
	t.Parallel()

	type serverState struct {
		mu             sync.Mutex
		taskActive     bool
		notified       bool
		progressCalls  int
		resultCalls    int
		lastResultBody map[string]any
	}

	state := &serverState{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/devices/self-register", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"device_id":             "dev-1",
			"token":                 "tok-1",
			"recovery_code":         "rec-1",
			"token_expires_at":      time.Now().Add(48 * time.Hour).Format(time.RFC3339),
			"poll_interval_seconds": 1,
		})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/active-task", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		active := state.taskActive
		state.mu.Unlock()
		if active {
			writeJSON(t, w, map[string]any{
				"has_active_task": true,
				"task_id":         "task-1",
				"status":          "created",
				"target":          "diagnose and fix the issue",
			})
			return
		}
		writeJSON(t, w, map[string]any{"has_active_task": false})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/tasks", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.taskActive = true
		state.mu.Unlock()
		writeJSON(t, w, map[string]any{"task_id": "task-1", "status": "created"})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/poll", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.taskActive && state.resultCalls == 0 {
			writeJSON(t, w, map[string]any{
				"command_id":              "cmd-1",
				"command":                 base64.StdEncoding.EncodeToString([]byte("sleep 0.15; printf 'hello from support'")),
				"command_encoding":        "base64",
				"command_timeout_seconds": 30,
				"command_intent":          "Run diagnostics",
				"poll_interval_seconds":   1,
			})
			return
		}
		if !state.notified && state.resultCalls > 0 {
			state.notified = true
			writeJSON(t, w, map[string]any{
				"poll_interval_seconds":        1,
				"notif_task_id":                "task-1",
				"notif_task_status":            "succeeded",
				"notif_budget_tasks_remaining": 9,
			})
			return
		}
		writeJSON(t, w, map[string]any{"poll_interval_seconds": 1})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/commands/cmd-1/progress", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.progressCalls++
		state.mu.Unlock()
		writeJSON(t, w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/result", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode result body: %v", err)
		}
		state.mu.Lock()
		state.resultCalls++
		state.lastResultBody = body
		state.taskActive = false
		state.mu.Unlock()
		writeJSON(t, w, map[string]any{"ok": true})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	store := newMemoryStore()
	svc := NewService(store,
		WithHTTPClient(server.Client()),
		WithProgressInterval(20*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := svc.AskForHelp(ctx, AskRequest{
		Description: "diagnose and fix the issue",
		Endpoint:    server.URL,
		InviteCode:  "invite-123",
	})
	if err != nil {
		t.Fatalf("AskForHelp: %v", err)
	}
	if !result.Created {
		t.Fatalf("expected Created=true, got %+v", result)
	}
	if result.TaskID != "task-1" {
		t.Fatalf("TaskID = %q, want task-1", result.TaskID)
	}
	if got := store.mustGet(ConfigEnabled); got != "true" {
		t.Fatalf("support.enabled = %q, want true", got)
	}
	if got := store.mustGet(configStateDeviceID); got != "dev-1" {
		t.Fatalf("device state not saved, got %q", got)
	}
	statusBeforeRun := svc.Status(ctx)
	if !statusBeforeRun.Enabled || !statusBeforeRun.Registered {
		t.Fatalf("expected enabled registered status, got %+v", statusBeforeRun)
	}
	if statusBeforeRun.ActiveTask == nil {
		t.Fatalf("expected active task snapshot before Run, got %+v", statusBeforeRun)
	}
	if statusBeforeRun.ActiveTask.TaskID != "task-1" || statusBeforeRun.ActiveTask.Status != "created" {
		t.Fatalf("unexpected active task snapshot before Run: %+v", statusBeforeRun.ActiveTask)
	}
	if statusBeforeRun.ActiveTask.Target != "diagnose and fix the issue" {
		t.Fatalf("unexpected active task target before Run: %+v", statusBeforeRun.ActiveTask)
	}

	if err := svc.Run(ctx, RunOptions{StopWhenIdle: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.progressCalls == 0 {
		t.Fatal("expected at least one progress update")
	}
	if state.resultCalls != 1 {
		t.Fatalf("resultCalls = %d, want 1", state.resultCalls)
	}
	if stdout, _ := state.lastResultBody["stdout"].(string); stdout == "" {
		t.Fatalf("stdout missing from result payload: %+v", state.lastResultBody)
	}
	statusAfterRun := svc.Status(ctx)
	if statusAfterRun.ActiveTask != nil {
		t.Fatalf("expected active task cleared after Run, got %+v", statusAfterRun.ActiveTask)
	}
	if statusAfterRun.LastTask == nil {
		t.Fatalf("expected last task snapshot after Run, got %+v", statusAfterRun)
	}
	if statusAfterRun.LastTask.TaskID != "task-1" || statusAfterRun.LastTask.Status != "succeeded" {
		t.Fatalf("unexpected last task snapshot after Run: %+v", statusAfterRun.LastTask)
	}
	if statusAfterRun.LastMessage == nil || !strings.Contains(statusAfterRun.LastMessage.Message, "Task task-1 finished with status succeeded") {
		t.Fatalf("unexpected last message snapshot after Run: %+v", statusAfterRun.LastMessage)
	}
}

func TestServiceRunRetriesTransientPollFailure(t *testing.T) {
	t.Parallel()

	type serverState struct {
		mu            sync.Mutex
		taskActive    bool
		taskCounter   int
		progressCalls int
		resultCalls   int
		pollFailures  int
	}

	state := &serverState{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/devices/self-register", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"device_id":             "dev-1",
			"token":                 "tok-1",
			"recovery_code":         "rec-1",
			"token_expires_at":      time.Now().Add(48 * time.Hour).Format(time.RFC3339),
			"poll_interval_seconds": 1,
		})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/active-task", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		active := state.taskActive
		counter := state.taskCounter
		state.mu.Unlock()
		if active {
			writeJSON(t, w, map[string]any{
				"has_active_task": true,
				"task_id":         fmt.Sprintf("task-%d", counter),
				"status":          "created",
				"target":          "readonly",
			})
			return
		}
		writeJSON(t, w, map[string]any{"has_active_task": false})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/tasks", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.taskActive = true
		state.taskCounter++
		counter := state.taskCounter
		state.mu.Unlock()
		writeJSON(t, w, map[string]any{"task_id": fmt.Sprintf("task-%d", counter), "status": "created"})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/poll", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.pollFailures == 0 {
			state.pollFailures++
			http.Error(w, `{"detail":"temporary overload"}`, http.StatusServiceUnavailable)
			return
		}
		if state.taskActive && state.resultCalls == 0 {
			writeJSON(t, w, map[string]any{
				"command_id":              "cmd-1",
				"command":                 base64.StdEncoding.EncodeToString([]byte("printf 'ok'")),
				"command_encoding":        "base64",
				"command_timeout_seconds": 10,
				"command_intent":          "Run readonly check",
				"poll_interval_seconds":   1,
			})
			return
		}
		writeJSON(t, w, map[string]any{
			"poll_interval_seconds": 1,
			"notif_task_id":         "task-1",
			"notif_task_status":     "succeeded",
		})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/result", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.resultCalls++
		state.taskActive = false
		state.mu.Unlock()
		writeJSON(t, w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/v1/devices/dev-1/commands/cmd-1/progress", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.progressCalls++
		state.mu.Unlock()
		writeJSON(t, w, map[string]any{"ok": true})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	store := newMemoryStore()
	svc := NewService(store, WithHTTPClient(server.Client()))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := svc.AskForHelp(ctx, AskRequest{
		Description: "readonly",
		Endpoint:    server.URL,
		InviteCode:  "invite-123",
	}); err != nil {
		t.Fatalf("AskForHelp: %v", err)
	}

	if err := svc.Run(ctx, RunOptions{StopWhenIdle: true}); err != nil {
		t.Fatalf("Run should tolerate transient 503, got: %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.pollFailures != 1 {
		t.Fatalf("pollFailures = %d, want 1", state.pollFailures)
	}
	if state.resultCalls != 1 {
		t.Fatalf("resultCalls = %d, want 1", state.resultCalls)
	}
}

func TestServiceAskForHelpRegistrationPromptErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		detail string
		kind   RegistrationPromptKind
	}{
		{
			name:   "invite_or_worker",
			status: http.StatusUnprocessableEntity,
			detail: "invite_code or worker_enrollment_code is required for new device registration",
			kind:   RegistrationPromptInviteOrWorker,
		},
		{
			name:   "recovery",
			status: http.StatusForbidden,
			detail: "valid recovery_code required to refresh existing device credentials",
			kind:   RegistrationPromptRecovery,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/devices/self-register", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if err := json.NewEncoder(w).Encode(map[string]any{"detail": tc.detail}); err != nil {
					t.Fatalf("encode error response: %v", err)
				}
			})
			server := httptest.NewServer(mux)
			defer server.Close()

			svc := NewService(newMemoryStore(), WithHTTPClient(server.Client()))
			_, err := svc.AskForHelp(context.Background(), AskRequest{Endpoint: server.URL})
			if err == nil {
				t.Fatal("expected registration prompt error")
			}

			var promptErr *RegistrationPromptError
			if !errors.As(err, &promptErr) {
				t.Fatalf("expected RegistrationPromptError, got %T (%v)", err, err)
			}
			if promptErr.Kind != tc.kind {
				t.Fatalf("prompt kind = %q, want %q", promptErr.Kind, tc.kind)
			}
			if promptErr.Detail != tc.detail {
				t.Fatalf("prompt detail = %q, want %q", promptErr.Detail, tc.detail)
			}
		})
	}
}

type memoryStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{values: make(map[string]string)}
}

func (s *memoryStore) GetConfig(ctx context.Context, key string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return "", fmt.Errorf("key not found: %s", key)
	}
	return value, nil
}

func (s *memoryStore) SetConfig(ctx context.Context, key, value string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *memoryStore) mustGet(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func writeJSON(t *testing.T, w http.ResponseWriter, body map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode json response: %v", err)
	}
}
