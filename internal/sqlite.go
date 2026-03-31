package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

// RawDB exposes the underlying *sql.DB for packages that need direct SQL access
// (e.g., knowledge query engine).
func (d *DB) RawDB() *sql.DB {
	return d.db
}

type Model struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Type             string    `json:"type"`
	Path             string    `json:"path"`
	Format           string    `json:"format"`
	SizeBytes        int64     `json:"size_bytes"`
	DetectedArch     string    `json:"detected_arch"`
	DetectedParams   string    `json:"detected_params"`
	ModelClass       string    `json:"model_class"`
	TotalParams      int64     `json:"total_params"`
	ActiveParams     int64     `json:"active_params"`
	Quantization     string    `json:"quantization"`
	QuantSrc         string    `json:"quant_src"`
	Status           string    `json:"status"`
	DownloadProgress float64   `json:"download_progress"`
	CreatedAt        time.Time `json:"created_at"`
}

type Engine struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Image       string    `json:"image"` // container image name (container engines) or empty (native)
	Tag         string    `json:"tag"`   // container image tag (container engines) or empty (native)
	SizeBytes   int64     `json:"size_bytes"`
	Platform    string    `json:"platform"`
	RuntimeType string    `json:"runtime_type"` // "container" or "native"
	BinaryPath  string    `json:"binary_path"`  // path to native binary (native engines only)
	Available   bool      `json:"available"`
	CreatedAt   time.Time `json:"created_at"`
}

type KnowledgeNote struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Tags            []string  `json:"tags"`
	HardwareProfile string    `json:"hardware_profile"`
	Model           string    `json:"model"`
	Engine          string    `json:"engine"`
	Content         string    `json:"content"`
	Confidence      string    `json:"confidence"`
	CreatedAt       time.Time `json:"created_at"`
}

type NoteFilter struct {
	HardwareProfile string `json:"hardware_profile"`
	Model           string `json:"model"`
	Engine          string `json:"engine"`
}

type AuditEntry struct {
	AgentType     string `json:"agent_type"`
	ToolName      string `json:"tool_name"`
	Arguments     string `json:"arguments"`
	ResultSummary string `json:"result_summary"`
}

// Configuration represents a tested Hardware×Engine×Model×Config combination.
type Configuration struct {
	ID          string    `json:"id"`
	HardwareID  string    `json:"hardware_id"`
	EngineID    string    `json:"engine_id"`
	ModelID     string    `json:"model_id"`
	Slot        string    `json:"slot"`
	Config      string    `json:"config"` // JSON
	ConfigHash  string    `json:"config_hash"`
	DerivedFrom string    `json:"derived_from"`
	Status      string    `json:"status"`
	Tags        []string  `json:"tags"`
	Source      string    `json:"source"`
	DeviceID    string    `json:"device_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// BenchmarkResult stores multi-dimensional performance data for a configuration.
type BenchmarkResult struct {
	ID              string    `json:"id"`
	ConfigID        string    `json:"config_id"`
	Concurrency     int       `json:"concurrency"`
	InputLenBucket  string    `json:"input_len_bucket"`
	OutputLenBucket string    `json:"output_len_bucket"`
	Modality        string    `json:"modality"`
	TTFTP50ms       float64   `json:"ttft_p50_ms"`
	TTFTP95ms       float64   `json:"ttft_p95_ms"`
	TTFTP99ms       float64   `json:"ttft_p99_ms"`
	TPOTP50ms       float64   `json:"tpot_p50_ms"`
	TPOTP95ms       float64   `json:"tpot_p95_ms"`
	ThroughputTPS   float64   `json:"throughput_tps"`
	QPS             float64   `json:"qps"`
	VRAMUsageMiB    int       `json:"vram_usage_mib"`
	RAMUsageMiB     int       `json:"ram_usage_mib"`
	PowerDrawWatts  float64   `json:"power_draw_watts"`
	GPUUtilPct      float64   `json:"gpu_util_pct"`
	ErrorRate       float64   `json:"error_rate"`
	OOMOccurred     bool      `json:"oom_occurred"`
	Stability       string    `json:"stability"`
	DurationS       int       `json:"duration_s"`
	SampleCount     int       `json:"sample_count"`
	TestedAt        time.Time `json:"tested_at"`
	AgentModel      string    `json:"agent_model"`
	Notes           string    `json:"notes"`
}

type ExplorationRun struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Goal         string    `json:"goal"`
	RequestedBy  string    `json:"requested_by"`
	Executor     string    `json:"executor"`
	Planner      string    `json:"planner"`
	Status       string    `json:"status"`
	HardwareID   string    `json:"hardware_id,omitempty"`
	EngineID     string    `json:"engine_id,omitempty"`
	ModelID      string    `json:"model_id,omitempty"`
	SourceRef    string    `json:"source_ref,omitempty"`
	ApprovalMode string    `json:"approval_mode"`
	ApprovedAt   time.Time `json:"approved_at,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
	Error        string    `json:"error,omitempty"`
	PlanJSON     string    `json:"plan_json"`
	SummaryJSON  string    `json:"summary_json,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type OpenQuestion struct {
	ID           string    `json:"id"`
	SourceAsset  string    `json:"source_asset"`
	Question     string    `json:"question"`
	TestCommand  string    `json:"test_command,omitempty"`
	Expected     string    `json:"expected,omitempty"`
	Status       string    `json:"status"`
	ActualResult string    `json:"actual_result,omitempty"`
	TestedAt     time.Time `json:"tested_at,omitempty"`
	Hardware     string    `json:"hardware,omitempty"`
}

type ExplorationEvent struct {
	ID           int64     `json:"id"`
	RunID        string    `json:"run_id"`
	StepIndex    int       `json:"step_index"`
	StepKind     string    `json:"step_kind"`
	Status       string    `json:"status"`
	ToolName     string    `json:"tool_name,omitempty"`
	RequestJSON  string    `json:"request_json,omitempty"`
	ResponseJSON string    `json:"response_json,omitempty"`
	ArtifactType string    `json:"artifact_type,omitempty"`
	ArtifactID   string    `json:"artifact_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// RollbackSnapshot stores pre-deletion state for agent safety recovery.
type RollbackSnapshot struct {
	ID           int64     `json:"id"`
	ToolName     string    `json:"tool_name"`
	ResourceType string    `json:"resource_type"`
	ResourceName string    `json:"resource_name"`
	Snapshot     string    `json:"snapshot"`
	CreatedAt    time.Time `json:"created_at"`
}

func Open(ctx context.Context, dbPath string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}
	// Keep one long-lived connection so PRAGMA settings are stable and access is
	// serialized per process (SQLite is optimized for this pattern).
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	// busy_timeout is a per-connection setting that needs no lock — set it
	// first so all subsequent operations benefit from SQLite's built-in retry.
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA busy_timeout=3000"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}
	d := &DB{db: sqlDB}
	// journal_mode=WAL requires a write lock, so it goes inside retryBusy
	// together with migrate (which uses BEGIN IMMEDIATE).
	if err := retryBusy(ctx, 8, func() error {
		if _, err := sqlDB.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			return fmt.Errorf("set WAL mode: %w", err)
		}
		if _, err := sqlDB.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
			return fmt.Errorf("enable foreign keys: %w", err)
		}
		return d.migrate(ctx)
	}); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func retryBusy(ctx context.Context, maxAttempts int, fn func() error) error {
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else if !isSQLiteBusy(err) {
			return err
		} else {
			lastErr = err
		}

		delay := time.Duration(50*(i+1)) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w (last busy error: %v)", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("sqlite busy retry exhausted")
}

func isSQLiteBusy(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate(ctx context.Context) error {
	// Use raw "BEGIN IMMEDIATE" instead of db.BeginTx because database/sql
	// doesn't support SQLite's IMMEDIATE lock level. Safe because
	// SetMaxOpenConns(1) guarantees all statements use the same connection.
	if _, err := d.db.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin migration lock: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = d.db.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	// v1: system tables (models, engines, config, audit_log, knowledge_notes)
	if err := d.migrateV1(ctx); err != nil {
		return fmt.Errorf("migrate v1: %w", err)
	}
	// v2: knowledge architecture tables (static + dynamic)
	if err := d.migrateV2(ctx); err != nil {
		return fmt.Errorf("migrate v2: %w", err)
	}
	// v3: enhanced model metadata
	if err := d.migrateV3(ctx); err != nil {
		return fmt.Errorf("migrate v3: %w", err)
	}
	// v4: unified engine scan (container + native)
	if err := d.migrateV4(ctx); err != nil {
		return fmt.Errorf("migrate v4: %w", err)
	}
	// v5: vendor-neutral GPU fields (gpu_compute_cap → gpu_compute_id)
	if err := d.migrateV5(ctx); err != nil {
		return fmt.Errorf("migrate v5: %w", err)
	}
	// v6: rollback snapshots for agent safety guardrails
	if err := d.migrateV6(ctx); err != nil {
		return fmt.Errorf("migrate v6: %w", err)
	}
	// v7: patrol alerts, power samples, validation results, tuning sessions
	if err := d.migrateV7(ctx); err != nil {
		return fmt.Errorf("migrate v7: %w", err)
	}
	// v8: exploration runs and events
	if err := d.migrateV8(ctx); err != nil {
		return fmt.Errorf("migrate v8: %w", err)
	}
	// v9: model_variants.gpu_count_min for multi-GPU variant selection
	if err := d.migrateV9(ctx); err != nil {
		return fmt.Errorf("migrate v9: %w", err)
	}
	if _, err := d.db.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	committed = true
	return nil
}

// Analyze updates SQLite's index statistics for the query optimizer.
func (d *DB) Analyze(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, "ANALYZE")
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
