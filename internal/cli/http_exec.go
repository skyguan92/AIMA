package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/jguan/aima/internal/proxy"
)

type execRequest struct {
	Command string `json:"command"`
	Stdin   string `json:"stdin,omitempty"`
}

type ExecResult struct {
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
	Output   string   `json:"output,omitempty"`
	Error    string   `json:"error,omitempty"`
	ExitCode int      `json:"exit_code"`
}

// NewExecHandler exposes the Cobra CLI over HTTP so the UI can execute the exact
// same command path as the local `aima` binary, minus the binary name itself.
func NewExecHandler(appFn func() *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app := appFn()
		if app == nil {
			proxy.WriteJSONError(w, http.StatusServiceUnavailable, "cli_unavailable", "cli is not ready")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			proxy.WriteJSONError(w, http.StatusBadRequest, "bad_request", "failed to read request body")
			return
		}
		if len(body) == 0 {
			body = []byte(`{}`)
		}

		var req execRequest
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			proxy.WriteJSONError(w, http.StatusBadRequest, "bad_request", "invalid cli request")
			return
		}

		result := ExecuteLine(r.Context(), app, req.Command, strings.NewReader(req.Stdin))
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			proxy.WriteJSONError(w, http.StatusInternalServerError, "internal_error", "failed to encode cli response")
			return
		}
	}
}

// ExecuteLine runs a command line through the same Cobra command tree used by the
// local CLI binary. The UI omits the leading `aima` token and sends the rest.
func ExecuteLine(ctx context.Context, app *App, line string, stdin io.Reader) ExecResult {
	result := ExecResult{
		Command: strings.TrimSpace(line),
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}

	args, err := splitCommandLine(result.Command)
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 2
		return result
	}
	result.Args = args

	root := NewRootCmd(app)
	var output bytes.Buffer
	root.SetIn(stdin)
	root.SetOut(&output)
	root.SetErr(&output)
	if len(args) == 0 {
		root.SetArgs([]string{"help"})
	} else {
		root.SetArgs(args)
	}

	if err := root.ExecuteContext(ctx); err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
	}
	result.Output = output.String()
	return result
}

func splitCommandLine(line string) ([]string, error) {
	var (
		args    []string
		buf     strings.Builder
		quote   rune
		escaped bool
		inToken bool
	)

	flush := func() {
		if !inToken {
			return
		}
		args = append(args, buf.String())
		buf.Reset()
		inToken = false
	}

	for _, r := range line {
		switch {
		case escaped:
			buf.WriteRune(r)
			escaped = false
			inToken = true
		case quote != 0:
			switch r {
			case '\\':
				escaped = true
			case quote:
				quote = 0
			default:
				buf.WriteRune(r)
				inToken = true
			}
		case unicode.IsSpace(r):
			flush()
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == '\\':
			escaped = true
			inToken = true
		default:
			buf.WriteRune(r)
			inToken = true
		}
	}

	if escaped {
		return nil, fmt.Errorf("cli parse: unfinished escape sequence")
	}
	if quote != 0 {
		return nil, fmt.Errorf("cli parse: unterminated %q quote", string(quote))
	}
	flush()
	return args, nil
}
