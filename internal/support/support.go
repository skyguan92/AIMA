package support

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	ConfigEnabled    = "support.enabled"
	ConfigEndpoint   = "support.endpoint"
	ConfigInviteCode = "support.invite_code"
	ConfigWorkerCode = "support.worker_code"

	DefaultEndpoint = "http://121.37.119.185/platform"

	configStateDeviceID        = "support.state.device_id"
	configStateToken           = "support.state.token"
	configStateRecoveryCode    = "support.state.recovery_code"
	configStateReferralCode    = "support.state.referral_code"
	configStateTokenExpiresAt  = "support.state.token_expires_at"
	configStatePollIntervalSec = "support.state.poll_interval_seconds"

	defaultPollInterval     = 5 * time.Second
	defaultProgressInterval = 5 * time.Second
	defaultDisabledRetry    = 5 * time.Second
	defaultPreviewLimit     = 16 * 1024
	defaultOutputLimit      = 512 * 1024
)

// ConfigStore persists support settings and device state.
type ConfigStore interface {
	GetConfig(ctx context.Context, key string) (string, error)
	SetConfig(ctx context.Context, key, value string) error
}

// Prompt carries a pending interaction from the remote support service.
type Prompt struct {
	InteractionID string
	Question      string
	Type          string
	Level         string
	Phase         string
}

// Notification reports support-side status updates and task completion.
type Notification struct {
	Message              string
	Type                 string
	Level                string
	Phase                string
	TaskID               string
	TaskStatus           string
	ReferralCode         string
	ShareText            string
	BudgetTasksRemaining int
}

// PromptFunc answers interactive prompts from the support service.
type PromptFunc func(ctx context.Context, prompt Prompt) (string, error)

// NotifyFunc receives background notifications from the support service.
type NotifyFunc func(ctx context.Context, notification Notification)

// AskRequest captures the user-facing support entrypoint parameters.
type AskRequest struct {
	Description string
	Endpoint    string
	InviteCode  string
	WorkerCode  string
}

// AskResult is returned by CLI, MCP, and UI support entrypoints.
type AskResult struct {
	Enabled             bool   `json:"enabled"`
	Endpoint            string `json:"endpoint"`
	DeviceID            string `json:"device_id"`
	PollIntervalSeconds int    `json:"poll_interval_seconds,omitempty"`
	Created             bool   `json:"created"`
	ReusedActiveTask    bool   `json:"reused_active_task"`
	TaskID              string `json:"task_id,omitempty"`
	TaskStatus          string `json:"task_status,omitempty"`
	TaskTarget          string `json:"task_target,omitempty"`
}

// RunOptions control how the support polling loop behaves.
type RunOptions struct {
	StopWhenIdle bool
	Prompt       PromptFunc
	Notify       NotifyFunc
}

// Option customizes a Service.
type Option func(*Service)

// Service is the built-in AIMA device client for aima-service-new.
type Service struct {
	store            ConfigStore
	client           *http.Client
	logger           *slog.Logger
	now              func() time.Time
	progressInterval time.Duration
	outputLimit      int
	previewLimit     int
}

// NewService constructs a support client backed by the given config store.
func NewService(store ConfigStore, opts ...Option) *Service {
	s := &Service{
		store:  store,
		client: &http.Client{Timeout: 30 * time.Second},
		logger: slog.Default(),
		now:    time.Now,

		progressInterval: defaultProgressInterval,
		outputLimit:      defaultOutputLimit,
		previewLimit:     defaultPreviewLimit,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithHTTPClient overrides the HTTP client used to talk to the support service.
func WithHTTPClient(client *http.Client) Option {
	return func(s *Service) {
		if client != nil {
			s.client = client
		}
	}
}

// WithLogger overrides the logger used by the support client.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithProgressInterval overrides how often long-running commands send progress.
func WithProgressInterval(interval time.Duration) Option {
	return func(s *Service) {
		if interval > 0 {
			s.progressInterval = interval
		}
	}
}

// AskForHelp ensures this AIMA instance is registered as a support device and
// optionally creates a new remote help task.
func (s *Service) AskForHelp(ctx context.Context, req AskRequest) (AskResult, error) {
	if err := s.persistOverrides(ctx, req); err != nil {
		return AskResult{}, err
	}

	state, endpoint, err := s.ensureRegistered(ctx)
	if err != nil {
		return AskResult{}, err
	}
	if err := s.store.SetConfig(ctx, ConfigEnabled, "true"); err != nil {
		return AskResult{}, fmt.Errorf("enable support: %w", err)
	}

	active, err := s.getActiveTask(ctx, endpoint, state)
	if err != nil {
		return AskResult{}, err
	}

	result := AskResult{
		Enabled:             true,
		Endpoint:            endpoint,
		DeviceID:            state.DeviceID,
		PollIntervalSeconds: state.PollIntervalSeconds,
	}

	if req.Description == "" {
		if active.HasActiveTask {
			result.TaskID = active.TaskID
			result.TaskStatus = active.Status
			result.TaskTarget = active.Target
			result.ReusedActiveTask = true
		}
		return result, nil
	}

	if active.HasActiveTask {
		result.TaskID = active.TaskID
		result.TaskStatus = active.Status
		result.TaskTarget = active.Target
		result.ReusedActiveTask = true
		return result, nil
	}

	task, err := s.createTask(ctx, endpoint, state, req.Description)
	if err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusConflict {
			active, activeErr := s.getActiveTask(ctx, endpoint, state)
			if activeErr != nil {
				return AskResult{}, activeErr
			}
			result.TaskID = active.TaskID
			result.TaskStatus = active.Status
			result.TaskTarget = active.Target
			result.ReusedActiveTask = true
			return result, nil
		}
		return AskResult{}, err
	}

	result.Created = true
	result.TaskID = task.TaskID
	result.TaskStatus = task.Status
	return result, nil
}

// RunBackground keeps a support polling loop alive for as long as ctx is active.
// It is safe to call from `aima serve`; the loop idles until support is enabled.
func (s *Service) RunBackground(ctx context.Context) error {
	err := s.Run(ctx, RunOptions{})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Run executes the support polling loop. With StopWhenIdle=true, the loop exits
// after the current active task finishes.
func (s *Service) Run(ctx context.Context, opts RunOptions) error {
	var sawActive bool
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		enabled := s.isEnabled(ctx)
		endpoint := s.endpointFromConfig(ctx)
		if !enabled || endpoint == "" {
			if opts.StopWhenIdle {
				return nil
			}
			if err := sleepContext(ctx, defaultDisabledRetry); err != nil {
				return err
			}
			continue
		}

		state, endpoint, err := s.ensureRegistered(ctx)
		if err != nil {
			if opts.StopWhenIdle {
				return err
			}
			s.logger.Warn("support registration failed", "error", err)
			if err := sleepContext(ctx, defaultDisabledRetry); err != nil {
				return err
			}
			continue
		}

		state, err = s.renewTokenIfNeeded(ctx, endpoint, state)
		if err != nil {
			if isAuthError(err) {
				s.logger.Warn("support token rejected; re-registering", "error", err)
				_ = s.clearSavedToken(ctx)
			} else if isTransientError(err) {
				if err := s.retryTransient(ctx, "renew_token", err, defaultPollInterval); err != nil {
					return err
				}
				continue
			} else if opts.StopWhenIdle {
				return err
			} else {
				s.logger.Warn("support token renewal failed", "error", err)
			}
		}

		pollResp, err := s.poll(ctx, endpoint, state, 20)
		if err != nil {
			if isAuthError(err) {
				s.logger.Warn("support poll rejected; re-registering", "error", err)
				_ = s.clearSavedToken(ctx)
				continue
			}
			if isTransientError(err) {
				if err := s.retryTransient(ctx, "poll", err, defaultPollInterval); err != nil {
					return err
				}
				continue
			}
			if opts.StopWhenIdle {
				return err
			}
			s.logger.Warn("support poll failed", "error", err)
			if err := sleepContext(ctx, defaultPollInterval); err != nil {
				return err
			}
			continue
		}

		if pollResp.PollIntervalSeconds > 0 {
			state.PollIntervalSeconds = pollResp.PollIntervalSeconds
			_ = s.store.SetConfig(ctx, configStatePollIntervalSec, fmt.Sprintf("%d", pollResp.PollIntervalSeconds))
		}

		if pollResp.InteractionID != "" {
			sawActive = true
			if err := s.handleInteraction(ctx, endpoint, state, pollResp, opts); err != nil {
				if isTransientError(err) {
					if err := s.retryTransient(ctx, "interaction", err, 3*time.Second); err != nil {
						return err
					}
					continue
				}
				if opts.StopWhenIdle {
					return err
				}
				s.logger.Warn("support interaction failed", "interaction_id", pollResp.InteractionID, "error", err)
				if err := sleepContext(ctx, 3*time.Second); err != nil {
					return err
				}
			}
			continue
		}

		if pollResp.CommandID != "" && pollResp.Command != "" {
			sawActive = true
			if err := s.executeCommands(ctx, endpoint, state, pollResp); err != nil {
				if isAuthError(err) {
					s.logger.Warn("support command submission rejected; re-registering", "error", err)
					_ = s.clearSavedToken(ctx)
					continue
				}
				if isTransientError(err) {
					if err := s.retryTransient(ctx, "command_execution", err, defaultPollInterval); err != nil {
						return err
					}
					continue
				}
				if opts.StopWhenIdle {
					return err
				}
				s.logger.Warn("support command execution failed", "command_id", pollResp.CommandID, "error", err)
			}
			continue
		}

		if pollResp.NotifTaskID != "" || pollResp.NotifTaskStatus != "" {
			sawActive = true
			s.emitNotification(ctx, opts.Notify, Notification{
				Message:              fmt.Sprintf("Task %s finished with status %s", pollResp.NotifTaskID, pollResp.NotifTaskStatus),
				Type:                 "task_completion",
				TaskID:               pollResp.NotifTaskID,
				TaskStatus:           pollResp.NotifTaskStatus,
				ReferralCode:         pollResp.NotifReferralCode,
				ShareText:            pollResp.NotifShareText,
				BudgetTasksRemaining: pollResp.NotifBudgetTasksRemaining,
			})
		}

		active, err := s.getActiveTask(ctx, endpoint, state)
		if err != nil {
			if isAuthError(err) {
				_ = s.clearSavedToken(ctx)
				continue
			}
			if isTransientError(err) {
				if err := s.retryTransient(ctx, "active_task", err, defaultPollInterval); err != nil {
					return err
				}
				continue
			}
			if opts.StopWhenIdle {
				return err
			}
			s.logger.Warn("support active-task check failed", "error", err)
			if err := sleepContext(ctx, defaultPollInterval); err != nil {
				return err
			}
			continue
		}
		if active.HasActiveTask {
			sawActive = true
		} else if opts.StopWhenIdle && sawActive {
			return nil
		}

		if err := sleepContext(ctx, nextPollInterval(state.PollIntervalSeconds)); err != nil {
			return err
		}
	}
}

type deviceState struct {
	DeviceID            string
	Token               string
	RecoveryCode        string
	ReferralCode        string
	TokenExpiresAt      string
	PollIntervalSeconds int
}

type activeTaskResponse struct {
	HasActiveTask bool   `json:"has_active_task"`
	TaskID        string `json:"task_id"`
	Status        string `json:"status"`
	Target        string `json:"target"`
}

type deviceTaskResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

type selfRegisterResponse struct {
	DeviceID            string `json:"device_id"`
	Token               string `json:"token"`
	RecoveryCode        string `json:"recovery_code"`
	TokenExpiresAt      string `json:"token_expires_at"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	ReferralCode        string `json:"referral_code"`
}

type renewTokenResponse struct {
	Token          string `json:"token"`
	TokenExpiresAt string `json:"token_expires_at"`
}

type pollResponse struct {
	CommandID                 string `json:"command_id"`
	Command                   string `json:"command"`
	CommandEncoding           string `json:"command_encoding"`
	CommandTimeoutSeconds     int    `json:"command_timeout_seconds"`
	CommandIntent             string `json:"command_intent"`
	InteractionID             string `json:"interaction_id"`
	Question                  string `json:"question"`
	InteractionType           string `json:"interaction_type"`
	InteractionLevel          string `json:"interaction_level"`
	InteractionPhase          string `json:"interaction_phase"`
	PollIntervalSeconds       int    `json:"poll_interval_seconds"`
	NotifTaskID               string `json:"notif_task_id"`
	NotifTaskStatus           string `json:"notif_task_status"`
	NotifReferralCode         string `json:"notif_referral_code"`
	NotifShareText            string `json:"notif_share_text"`
	NotifBudgetTasksRemaining int    `json:"notif_budget_tasks_remaining"`
}

type commandProgressAckResponse struct {
	OK              bool   `json:"ok"`
	CancelRequested bool   `json:"cancel_requested"`
	CommandStatus   string `json:"command_status"`
}

type commandResultAckResponse struct {
	OK                        bool   `json:"ok"`
	NextCommandID             string `json:"next_command_id"`
	NextCommand               string `json:"next_command"`
	NextCommandEncoding       string `json:"next_command_encoding"`
	NextCommandTimeoutSeconds int    `json:"next_command_timeout_seconds"`
	NextCommandIntent         string `json:"next_command_intent"`
	PollIntervalSeconds       int    `json:"poll_interval_seconds"`
}

func (s *Service) persistOverrides(ctx context.Context, req AskRequest) error {
	if strings.TrimSpace(req.Endpoint) != "" {
		if err := s.store.SetConfig(ctx, ConfigEndpoint, strings.TrimSpace(req.Endpoint)); err != nil {
			return fmt.Errorf("set %s: %w", ConfigEndpoint, err)
		}
	} else if s.optionalConfig(ctx, ConfigEndpoint, "AIMA_SUPPORT_ENDPOINT") == "" {
		if err := s.store.SetConfig(ctx, ConfigEndpoint, DefaultEndpoint); err != nil {
			return fmt.Errorf("set default %s: %w", ConfigEndpoint, err)
		}
	}
	if strings.TrimSpace(req.InviteCode) != "" {
		if err := s.store.SetConfig(ctx, ConfigInviteCode, strings.TrimSpace(req.InviteCode)); err != nil {
			return fmt.Errorf("set %s: %w", ConfigInviteCode, err)
		}
	}
	if strings.TrimSpace(req.WorkerCode) != "" {
		if err := s.store.SetConfig(ctx, ConfigWorkerCode, strings.TrimSpace(req.WorkerCode)); err != nil {
			return fmt.Errorf("set %s: %w", ConfigWorkerCode, err)
		}
	}
	return nil
}

func (s *Service) ensureRegistered(ctx context.Context) (deviceState, string, error) {
	state := s.loadState(ctx)
	endpoint := s.endpointFromConfig(ctx)
	if endpoint == "" {
		return deviceState{}, "", fmt.Errorf("support endpoint is not configured; set %s or AIMA_SUPPORT_ENDPOINT", ConfigEndpoint)
	}

	if state.DeviceID != "" && state.Token != "" {
		if _, err := s.getActiveTask(ctx, endpoint, state); err == nil {
			return state, endpoint, nil
		} else if !isAuthError(err) {
			return deviceState{}, "", err
		}
	}

	registerReq, err := buildSelfRegisterRequest(ctx)
	if err != nil {
		return deviceState{}, "", err
	}
	if state.RecoveryCode != "" {
		registerReq["recovery_code"] = state.RecoveryCode
	}
	if invite := s.optionalConfig(ctx, ConfigInviteCode, "AIMA_SUPPORT_INVITE_CODE"); invite != "" {
		registerReq["invite_code"] = invite
	}
	if worker := s.optionalConfig(ctx, ConfigWorkerCode, "AIMA_SUPPORT_WORKER_CODE"); worker != "" {
		registerReq["worker_enrollment_code"] = worker
	}

	var resp selfRegisterResponse
	if err := s.doJSON(ctx, http.MethodPost, endpoint+"/devices/self-register", "", registerReq, &resp); err != nil {
		return deviceState{}, "", err
	}
	state.DeviceID = resp.DeviceID
	state.Token = resp.Token
	state.RecoveryCode = resp.RecoveryCode
	state.ReferralCode = resp.ReferralCode
	state.TokenExpiresAt = resp.TokenExpiresAt
	if resp.PollIntervalSeconds > 0 {
		state.PollIntervalSeconds = resp.PollIntervalSeconds
	}
	if err := s.saveState(ctx, state); err != nil {
		return deviceState{}, "", err
	}
	return state, endpoint, nil
}

func (s *Service) renewTokenIfNeeded(ctx context.Context, endpoint string, state deviceState) (deviceState, error) {
	if state.DeviceID == "" || state.Token == "" {
		return state, nil
	}
	if state.TokenExpiresAt != "" {
		if expiresAt, err := time.Parse(time.RFC3339, state.TokenExpiresAt); err == nil {
			if time.Until(expiresAt) > 24*time.Hour {
				return state, nil
			}
		}
	}

	var resp renewTokenResponse
	err := s.doJSON(ctx, http.MethodPost, endpoint+"/devices/"+state.DeviceID+"/renew-token", state.Token, nil, &resp)
	if err != nil {
		return state, err
	}
	if resp.Token != "" {
		state.Token = resp.Token
	}
	if resp.TokenExpiresAt != "" {
		state.TokenExpiresAt = resp.TokenExpiresAt
	}
	if err := s.saveState(ctx, state); err != nil {
		return state, err
	}
	return state, nil
}

func (s *Service) poll(ctx context.Context, endpoint string, state deviceState, waitSeconds int) (pollResponse, error) {
	var resp pollResponse
	url := fmt.Sprintf("%s/devices/%s/poll?wait=%d", endpoint, state.DeviceID, waitSeconds)
	if err := s.doJSON(ctx, http.MethodGet, url, state.Token, nil, &resp); err != nil {
		return pollResponse{}, err
	}
	return resp, nil
}

func (s *Service) getActiveTask(ctx context.Context, endpoint string, state deviceState) (activeTaskResponse, error) {
	var resp activeTaskResponse
	if err := s.doJSON(ctx, http.MethodGet, endpoint+"/devices/"+state.DeviceID+"/active-task", state.Token, nil, &resp); err != nil {
		return activeTaskResponse{}, err
	}
	return resp, nil
}

func (s *Service) createTask(ctx context.Context, endpoint string, state deviceState, description string) (deviceTaskResponse, error) {
	var resp deviceTaskResponse
	body := map[string]any{"description": description}
	if err := s.doJSON(ctx, http.MethodPost, endpoint+"/devices/"+state.DeviceID+"/tasks", state.Token, body, &resp); err != nil {
		return deviceTaskResponse{}, err
	}
	return resp, nil
}

func (s *Service) handleInteraction(ctx context.Context, endpoint string, state deviceState, resp pollResponse, opts RunOptions) error {
	notification := Notification{
		Message: resp.Question,
		Type:    resp.InteractionType,
		Level:   resp.InteractionLevel,
		Phase:   resp.InteractionPhase,
	}
	if resp.InteractionType == "notification" {
		s.emitNotification(ctx, opts.Notify, notification)
		return s.respondInteraction(ctx, endpoint, state, resp.InteractionID, "displayed")
	}

	answer := defaultInteractionAnswer(resp)
	if opts.Prompt != nil {
		prompt := Prompt{
			InteractionID: resp.InteractionID,
			Question:      resp.Question,
			Type:          resp.InteractionType,
			Level:         resp.InteractionLevel,
			Phase:         resp.InteractionPhase,
		}
		if response, err := opts.Prompt(ctx, prompt); err == nil && strings.TrimSpace(response) != "" {
			answer = strings.TrimSpace(response)
		} else if err != nil {
			s.logger.Warn("support prompt handler failed; using auto-answer", "interaction_id", resp.InteractionID, "error", err)
		}
	}
	return s.respondInteraction(ctx, endpoint, state, resp.InteractionID, answer)
}

func (s *Service) respondInteraction(ctx context.Context, endpoint string, state deviceState, interactionID, answer string) error {
	body := map[string]any{"answer": answer}
	return s.doJSON(ctx, http.MethodPost, endpoint+"/devices/"+state.DeviceID+"/interactions/"+interactionID+"/respond", state.Token, body, nil)
}

func (s *Service) executeCommands(ctx context.Context, endpoint string, state deviceState, resp pollResponse) error {
	current := resp
	for current.CommandID != "" && current.Command != "" {
		ack, err := s.executeSingleCommand(ctx, endpoint, state, current)
		if err != nil {
			return err
		}
		if ack.PollIntervalSeconds > 0 {
			state.PollIntervalSeconds = ack.PollIntervalSeconds
			_ = s.store.SetConfig(ctx, configStatePollIntervalSec, fmt.Sprintf("%d", ack.PollIntervalSeconds))
		}
		if ack.NextCommandID == "" || ack.NextCommand == "" {
			return nil
		}
		current = pollResponse{
			CommandID:             ack.NextCommandID,
			Command:               ack.NextCommand,
			CommandEncoding:       ack.NextCommandEncoding,
			CommandTimeoutSeconds: ack.NextCommandTimeoutSeconds,
			CommandIntent:         ack.NextCommandIntent,
		}
	}
	return nil
}

func (s *Service) executeSingleCommand(ctx context.Context, endpoint string, state deviceState, resp pollResponse) (commandResultAckResponse, error) {
	command, err := decodeCommand(resp.Command, resp.CommandEncoding)
	if err != nil {
		return commandResultAckResponse{}, err
	}

	timeoutSeconds := resp.CommandTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300
	}
	startedAt := s.now()
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	stdoutBuf := newSafeBuffer(s.outputLimit)
	stderrBuf := newSafeBuffer(s.outputLimit)
	cmd := shellCommand(cmdCtx, command)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		stderrBuf.AppendString(err.Error())
		return s.submitResultWithRetry(ctx, endpoint, state, map[string]any{
			"command_id": resp.CommandID,
			"exit_code":  127,
			"stdout":     stdoutBuf.String(),
			"stderr":     stderrBuf.String(),
			"result_id":  buildResultID(s.now()),
		})
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	ticker := time.NewTicker(s.progressInterval)
	defer ticker.Stop()

	remoteCanceled := false
	for {
		select {
		case err := <-waitCh:
			if errors.Is(ctx.Err(), context.Canceled) {
				return commandResultAckResponse{}, ctx.Err()
			}

			exitCode := 0
			switch {
			case remoteCanceled:
				exitCode = 130
				stderrBuf.AppendString("\nCommand cancelled after remote request\n")
			case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
				exitCode = 124
				stderrBuf.AppendString(fmt.Sprintf("\nCommand timed out after %ds\n", timeoutSeconds))
			case err == nil:
				exitCode = 0
			default:
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = 1
					stderrBuf.AppendString("\n" + err.Error() + "\n")
				}
			}

			return s.submitResultWithRetry(ctx, endpoint, state, map[string]any{
				"command_id": resp.CommandID,
				"exit_code":  exitCode,
				"stdout":     stdoutBuf.String(),
				"stderr":     stderrBuf.String(),
				"result_id":  buildResultID(s.now()),
			})

		case <-ticker.C:
			elapsed := s.now().Sub(startedAt).Round(time.Second)
			ack, err := s.submitProgress(ctx, endpoint, state, resp.CommandID, stdoutBuf.Snapshot(s.previewLimit), stderrBuf.Snapshot(s.previewLimit), fmt.Sprintf("Command still running (%s)", elapsed))
			if err != nil {
				s.logger.Warn("support progress update failed", "command_id", resp.CommandID, "error", err)
				continue
			}
			if ack.CancelRequested || strings.EqualFold(ack.CommandStatus, "cancelled") {
				remoteCanceled = true
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}

		case <-ctx.Done():
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-waitCh
			return commandResultAckResponse{}, ctx.Err()
		}
	}
}

func (s *Service) submitProgress(ctx context.Context, endpoint string, state deviceState, commandID, stdout, stderr, message string) (commandProgressAckResponse, error) {
	var resp commandProgressAckResponse
	err := s.doJSON(ctx, http.MethodPost, endpoint+"/devices/"+state.DeviceID+"/commands/"+commandID+"/progress", state.Token, map[string]any{
		"stdout":  stdout,
		"stderr":  stderr,
		"message": message,
	}, &resp)
	return resp, err
}

func (s *Service) submitResultWithRetry(ctx context.Context, endpoint string, state deviceState, body map[string]any) (commandResultAckResponse, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		var resp commandResultAckResponse
		err := s.doJSON(ctx, http.MethodPost, endpoint+"/devices/"+state.DeviceID+"/result", state.Token, body, &resp)
		if err == nil {
			return resp, nil
		}
		if isAuthError(err) {
			return commandResultAckResponse{}, err
		}
		lastErr = err
		if err := sleepContext(ctx, time.Duration(attempt+1)*time.Second); err != nil {
			return commandResultAckResponse{}, err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("submit result failed")
	}
	return commandResultAckResponse{}, lastErr
}

func (s *Service) emitNotification(ctx context.Context, notify NotifyFunc, notification Notification) {
	if notify != nil {
		notify(ctx, notification)
		return
	}
	if notification.Message != "" {
		s.logger.Info("support notification", "message", notification.Message, "type", notification.Type, "phase", notification.Phase)
		return
	}
	if notification.TaskID != "" {
		s.logger.Info("support task completed", "task_id", notification.TaskID, "status", notification.TaskStatus)
	}
}

func (s *Service) loadState(ctx context.Context) deviceState {
	state := deviceState{
		PollIntervalSeconds: int(defaultPollInterval / time.Second),
	}
	state.DeviceID = s.optionalConfig(ctx, configStateDeviceID, "")
	state.Token = s.optionalConfig(ctx, configStateToken, "")
	state.RecoveryCode = s.optionalConfig(ctx, configStateRecoveryCode, "")
	state.ReferralCode = s.optionalConfig(ctx, configStateReferralCode, "")
	state.TokenExpiresAt = s.optionalConfig(ctx, configStateTokenExpiresAt, "")
	if raw := s.optionalConfig(ctx, configStatePollIntervalSec, ""); raw != "" {
		var parsed int
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err == nil && parsed > 0 {
			state.PollIntervalSeconds = parsed
		}
	}
	return state
}

func (s *Service) saveState(ctx context.Context, state deviceState) error {
	entries := map[string]string{
		configStateDeviceID:        state.DeviceID,
		configStateToken:           state.Token,
		configStateRecoveryCode:    state.RecoveryCode,
		configStateReferralCode:    state.ReferralCode,
		configStateTokenExpiresAt:  state.TokenExpiresAt,
		configStatePollIntervalSec: fmt.Sprintf("%d", maxInt(state.PollIntervalSeconds, int(defaultPollInterval/time.Second))),
	}
	for key, value := range entries {
		if err := s.store.SetConfig(ctx, key, value); err != nil {
			return fmt.Errorf("save support state %s: %w", key, err)
		}
	}
	return nil
}

func (s *Service) clearSavedToken(ctx context.Context) error {
	if err := s.store.SetConfig(ctx, configStateToken, ""); err != nil {
		return err
	}
	return s.store.SetConfig(ctx, configStateTokenExpiresAt, "")
}

func (s *Service) isEnabled(ctx context.Context) bool {
	value := s.optionalConfig(ctx, ConfigEnabled, "")
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Service) endpointFromConfig(ctx context.Context) string {
	value := s.optionalConfig(ctx, ConfigEndpoint, "AIMA_SUPPORT_ENDPOINT")
	if value == "" {
		value = DefaultEndpoint
	}
	return normalizeEndpoint(value)
}

func (s *Service) optionalConfig(ctx context.Context, key, envKey string) string {
	if key != "" {
		if value, err := s.store.GetConfig(ctx, key); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if envKey != "" {
		return strings.TrimSpace(os.Getenv(envKey))
	}
	return ""
}

func (s *Service) doJSON(ctx context.Context, method, url, token string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		payload = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return fmt.Errorf("build request %s %s: %w", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response %s %s: %w", method, url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newHTTPStatusError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response %s %s: %w", method, url, err)
	}
	return nil
}

type httpStatusError struct {
	StatusCode int
	Detail     string
	Body       string
}

func (e *httpStatusError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("support service returned HTTP %d: %s", e.StatusCode, e.Detail)
	}
	if e.Body != "" {
		return fmt.Sprintf("support service returned HTTP %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("support service returned HTTP %d", e.StatusCode)
}

func newHTTPStatusError(statusCode int, body []byte) error {
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	detail, _ := payload["detail"].(string)
	return &httpStatusError{
		StatusCode: statusCode,
		Detail:     detail,
		Body:       strings.TrimSpace(string(body)),
	}
}

func isAuthError(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	switch statusErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	default:
		return false
	}
}

func isTransientError(err error) bool {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		default:
			return statusErr.StatusCode >= 500 && statusErr.StatusCode < 600
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func (s *Service) retryTransient(ctx context.Context, stage string, err error, delay time.Duration) error {
	s.logger.Warn("support transient failure; retrying", "stage", stage, "error", err, "retry_in", delay.String())
	return sleepContext(ctx, delay)
}

func decodeCommand(command, encoding string) (string, error) {
	if !strings.EqualFold(encoding, "base64") {
		return command, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(command)
	if err != nil {
		return "", fmt.Errorf("decode base64 command: %w", err)
	}
	return string(decoded), nil
}

func normalizeEndpoint(raw string) string {
	raw = strings.TrimSpace(strings.TrimRight(raw, "/"))
	if raw == "" {
		return ""
	}
	if strings.HasSuffix(raw, "/api/v1") {
		return raw
	}
	return raw + "/api/v1"
}

func nextPollInterval(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultPollInterval
	}
	return time.Duration(seconds) * time.Second
}

func buildResultID(now time.Time) string {
	return fmt.Sprintf("res_%d", now.UnixNano())
}

func defaultInteractionAnswer(resp pollResponse) string {
	return fmt.Sprintf("AIMA local support client has no interactive answer for %q. Continue autonomously if possible, and explain any assumptions you make.", resp.Question)
}

func buildSelfRegisterRequest(ctx context.Context) (map[string]any, error) {
	profile, fingerprint, hardwareID, err := collectOSProfile(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"fingerprint": fingerprint,
		"os_profile":  profile,
	}
	if hardwareID != "" {
		body["hardware_id"] = hardwareID
	}
	return body, nil
}

func collectOSProfile(ctx context.Context) (map[string]any, string, string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, "", "", fmt.Errorf("resolve hostname: %w", err)
	}
	hostname = strings.TrimSpace(hostname)
	machineID := strings.TrimSpace(readMachineID(ctx))
	if machineID == "" {
		machineID = hostname
	}
	hardwareID := hashString(machineID)

	profile := map[string]any{
		"os_type":          runtime.GOOS,
		"os_version":       detectOSVersion(ctx),
		"arch":             runtime.GOARCH,
		"hostname":         hostname,
		"machine_id":       machineID,
		"package_managers": detectPackageManagers(),
		"shell":            detectShell(),
		"shell_env": map[string]any{
			"proxy": map[string]any{
				"http_configured":     envConfigured("http_proxy", "HTTP_PROXY"),
				"https_configured":    envConfigured("https_proxy", "HTTPS_PROXY"),
				"no_proxy_configured": envConfigured("no_proxy", "NO_PROXY"),
			},
		},
	}
	fingerprint := fmt.Sprintf("%s|%s|%s", runtime.GOOS, runtime.GOARCH, hostname)
	return profile, fingerprint, hardwareID, nil
}

func readMachineID(ctx context.Context) string {
	switch runtime.GOOS {
	case "linux":
		for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
			if data, err := os.ReadFile(path); err == nil {
				return strings.TrimSpace(string(data))
			}
		}
	case "darwin":
		if out, err := exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if !strings.Contains(line, "IOPlatformUUID") {
					continue
				}
				parts := strings.Split(line, "\"")
				if len(parts) >= 4 {
					return strings.TrimSpace(parts[3])
				}
			}
		}
	}
	return ""
}

func detectOSVersion(ctx context.Context) string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output(); err == nil {
			version := strings.TrimSpace(string(out))
			if version != "" {
				return "macOS " + version
			}
		}
	case "linux":
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if !strings.HasPrefix(line, "PRETTY_NAME=") {
					continue
				}
				value := strings.TrimPrefix(line, "PRETTY_NAME=")
				value = strings.Trim(value, `"`)
				if value != "" {
					return value
				}
			}
		}
	}
	return runtime.GOOS
}

func detectPackageManagers() []string {
	candidates := []struct {
		Name string
		Bin  string
	}{
		{Name: "apt", Bin: "apt-get"},
		{Name: "brew", Bin: "brew"},
		{Name: "dnf", Bin: "dnf"},
		{Name: "yum", Bin: "yum"},
		{Name: "snap", Bin: "snap"},
		{Name: "pip", Bin: "pip3"},
	}
	found := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate.Bin); err == nil {
			found = append(found, candidate.Name)
		}
	}
	return found
}

func detectShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "/bin/sh"
}

func envConfigured(keys ...string) bool {
	for _, key := range keys {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-lc", command)
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func newSafeBuffer(max int) *safeBuffer {
	return &safeBuffer{max: max}
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		return len(p), nil
	}
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *safeBuffer) AppendString(s string) {
	_, _ = b.Write([]byte(s))
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *safeBuffer) Snapshot(limit int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 || b.buf.Len() <= limit {
		return b.buf.String()
	}
	return b.buf.String()[:limit]
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
