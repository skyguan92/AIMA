package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jguan/aima/internal/onboarding"
)

// sseHeartbeatInterval is short enough to survive common idle-timeouts at
// reverse proxies, load balancers, and browser fetch clients (typically
// 30-60s). Keep well under those so phases with no observable progress
// (e.g. a slow docker image pull) do not drop the connection.
const sseHeartbeatInterval = 20 * time.Second

// sseStream serializes all writes to an SSE response so event emits from
// parallel goroutines (RunScan), phase callbacks, and the heartbeat ticker
// cannot interleave and corrupt frames.
type sseStream struct {
	w  http.ResponseWriter
	f  http.Flusher
	mu sync.Mutex
}

func newSSEStream(w http.ResponseWriter, f http.Flusher) *sseStream {
	return &sseStream{w: w, f: f}
}

func (s *sseStream) write(event, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	s.f.Flush()
}

func (s *sseStream) writeJSON(event string, v any) {
	b, _ := json.Marshal(v)
	s.write(event, string(b))
}

// sink adapts the stream to the onboarding EventSink contract so RunScan /
// RunInit / RunDeploy can push events directly into the SSE response as they
// are produced.
func (s *sseStream) sink() onboarding.EventSink {
	return func(ev onboarding.Event) { s.writeJSON(ev.Type, ev.Data) }
}

// startHeartbeat fires an SSE comment (a line starting with ":") every
// interval until ctx is cancelled. Comments are ignored by the browser
// EventSource API but keep the TCP connection from idling at intermediaries.
// The caller is expected to cancel ctx (usually r.Context()) when the handler
// returns; the goroutine exits cleanly.
func (s *sseStream) startHeartbeat(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.mu.Lock()
				_, _ = fmt.Fprint(s.w, ": keepalive\n\n")
				s.f.Flush()
				s.mu.Unlock()
			}
		}
	}()
}
