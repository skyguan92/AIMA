package state

import (
	"context"
	"fmt"
)

func (d *DB) migrateV1(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)

	if version < 1 {
		// Old table schemas may be incomplete (e.g. missing size_bytes column).
		// These are all scan caches that can be safely rebuilt.
		for _, t := range []string{"models", "engines", "knowledge_notes", "config", "audit_log"} {
			if _, err := d.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+t); err != nil {
				return fmt.Errorf("drop old table %s: %w", t, err)
			}
		}
	}

	ddl := `
CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    path TEXT NOT NULL,
    format TEXT,
    size_bytes INTEGER,
    detected_arch TEXT,
    detected_params TEXT,
    status TEXT DEFAULT 'registered',
    download_progress REAL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS engines (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    image TEXT NOT NULL,
    tag TEXT NOT NULL,
    size_bytes INTEGER,
    platform TEXT,
    available BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS knowledge_notes (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    tags TEXT,
    hardware_profile TEXT,
    model TEXT,
    engine TEXT,
    content TEXT NOT NULL,
    confidence TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_type TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    arguments TEXT,
    result_summary TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);`
	if _, err := d.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("migrate v1 schema: %w", err)
	}

	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (d *DB) migrateV2(ctx context.Context) error {
	// Check if v2 migration already applied
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='hardware_profiles'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check v2 migration: %w", err)
	}
	if count > 0 {
		return nil // already migrated
	}

	ddl := `
-- ====================================================================
-- Static knowledge tables (rebuilt on startup from go:embed YAML)
-- ====================================================================

CREATE TABLE hardware_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    gpu_arch TEXT NOT NULL,
    gpu_vram_mib INTEGER,
    gpu_compute_id TEXT,
    cpu_arch TEXT,
    cpu_cores INTEGER,
    ram_mib INTEGER,
    unified_memory BOOLEAN DEFAULT FALSE,
    tdp_watts INTEGER,
    power_modes TEXT,
    gpu_tools TEXT,
    raw_yaml TEXT
);
CREATE INDEX idx_hp_gpu ON hardware_profiles(gpu_arch);

CREATE TABLE engine_assets (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    version TEXT,
    image_name TEXT,
    image_tag TEXT,
    image_size_mb INTEGER,
    api_protocol TEXT,
    cold_start_s_min INTEGER,
    cold_start_s_max INTEGER,
    power_watts_min INTEGER,
    power_watts_max INTEGER,
    perf_gain_desc TEXT,
    raw_yaml TEXT
);

CREATE TABLE engine_features (
    engine_id TEXT NOT NULL REFERENCES engine_assets(id),
    feature TEXT NOT NULL,
    PRIMARY KEY (engine_id, feature)
);
CREATE INDEX idx_ef_feature ON engine_features(feature);

CREATE TABLE engine_hardware_compat (
    engine_id TEXT NOT NULL REFERENCES engine_assets(id),
    hardware_id TEXT NOT NULL REFERENCES hardware_profiles(id),
    vram_min_mib INTEGER,
    cpu_offload BOOLEAN DEFAULT FALSE,
    ssd_offload BOOLEAN DEFAULT FALSE,
    npu_offload BOOLEAN DEFAULT FALSE,
    min_gpu_mem_mib INTEGER,
    recommended_cores_pct INTEGER,
    PRIMARY KEY (engine_id, hardware_id)
);

CREATE TABLE model_assets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    family TEXT,
    param_count TEXT,
    formats TEXT,
    sources TEXT,
    raw_yaml TEXT
);
CREATE INDEX idx_ma_type ON model_assets(type);
CREATE INDEX idx_ma_family ON model_assets(family);

CREATE TABLE model_variants (
    id TEXT PRIMARY KEY,
    model_id TEXT NOT NULL REFERENCES model_assets(id),
    hardware_id TEXT NOT NULL REFERENCES hardware_profiles(id),
    engine_type TEXT NOT NULL,
    format TEXT,
    default_config TEXT NOT NULL,
    expected_perf TEXT,
    vram_min_mib INTEGER,
    gpu_count_min INTEGER
);
CREATE INDEX idx_mv_lookup ON model_variants(model_id, hardware_id, engine_type);

CREATE TABLE partition_strategies (
    id TEXT PRIMARY KEY,
    hardware_id TEXT NOT NULL,
    workload_pattern TEXT NOT NULL,
    slots TEXT NOT NULL,
    raw_yaml TEXT
);

-- ====================================================================
-- Dynamic knowledge tables (Agent exploration, persisted across restarts)
-- ====================================================================

CREATE TABLE configurations (
    id TEXT PRIMARY KEY,
    hardware_id TEXT NOT NULL,
    engine_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    partition_slot TEXT,
    config TEXT NOT NULL,
    config_hash TEXT NOT NULL,
    derived_from TEXT REFERENCES configurations(id),
    status TEXT DEFAULT 'experiment',
    tags TEXT,
    source TEXT DEFAULT 'local',
    device_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_cfg_4d ON configurations(hardware_id, engine_id, model_id);
CREATE INDEX idx_cfg_status ON configurations(status);
CREATE INDEX idx_cfg_hash ON configurations(config_hash);

CREATE TABLE benchmark_results (
    id TEXT PRIMARY KEY,
    config_id TEXT NOT NULL REFERENCES configurations(id),
    concurrency INTEGER NOT NULL DEFAULT 1,
    input_len_bucket TEXT,
    output_len_bucket TEXT,
    modality TEXT DEFAULT 'text',
    ttft_ms_p50 REAL,
    ttft_ms_p95 REAL,
    ttft_ms_p99 REAL,
    tpot_ms_p50 REAL,
    tpot_ms_p95 REAL,
    throughput_tps REAL,
    qps REAL,
    vram_usage_mib INTEGER,
    ram_usage_mib INTEGER,
    power_draw_watts REAL,
    gpu_utilization_pct REAL,
    error_rate REAL DEFAULT 0,
    oom_occurred BOOLEAN DEFAULT FALSE,
    stability TEXT,
    duration_s INTEGER,
    sample_count INTEGER,
    tested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    agent_model TEXT,
    notes TEXT
);
CREATE INDEX idx_br_config ON benchmark_results(config_id);
CREATE INDEX idx_br_perf ON benchmark_results(throughput_tps DESC);
CREATE INDEX idx_br_load ON benchmark_results(concurrency, input_len_bucket);

CREATE TABLE perf_vectors (
    config_id TEXT PRIMARY KEY REFERENCES configurations(id),
    norm_ttft_p95 REAL,
    norm_tpot_p95 REAL,
    norm_throughput REAL,
    norm_qps REAL,
    norm_vram REAL,
    norm_power REAL,
    avg_throughput REAL,
    avg_ttft_p95 REAL,
    avg_vram_mib REAL,
    benchmark_count INTEGER,
    updated_at DATETIME
);`

	_, err = d.db.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("migrate v2 schema: %w", err)
	}
	return nil
}

func (d *DB) migrateV3(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if version >= 3 {
		return nil
	}

	// Add new columns to models table for enhanced metadata
	// Use ALTER TABLE with IF NOT EXISTS pattern by checking column existence first
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('models') WHERE name='model_class'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check model_class column: %w", err)
	}
	if count == 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE models ADD COLUMN model_class TEXT DEFAULT ''`)
		if err != nil {
			return fmt.Errorf("add model_class column: %w", err)
		}
	}

	err = d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('models') WHERE name='total_params'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check total_params column: %w", err)
	}
	if count == 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE models ADD COLUMN total_params INTEGER DEFAULT 0`)
		if err != nil {
			return fmt.Errorf("add total_params column: %w", err)
		}
	}

	err = d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('models') WHERE name='active_params'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check active_params column: %w", err)
	}
	if count == 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE models ADD COLUMN active_params INTEGER DEFAULT 0`)
		if err != nil {
			return fmt.Errorf("add active_params column: %w", err)
		}
	}

	err = d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('models') WHERE name='quantization'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check quantization column: %w", err)
	}
	if count == 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE models ADD COLUMN quantization TEXT DEFAULT ''`)
		if err != nil {
			return fmt.Errorf("add quantization column: %w", err)
		}
	}

	err = d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('models') WHERE name='quant_src'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check quant_src column: %w", err)
	}
	if count == 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE models ADD COLUMN quant_src TEXT DEFAULT ''`)
		if err != nil {
			return fmt.Errorf("add quant_src column: %w", err)
		}
	}

	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (d *DB) migrateV4(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if version >= 4 {
		return nil
	}

	// Add runtime_type column to engines table
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('engines') WHERE name='runtime_type'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check runtime_type column: %w", err)
	}
	if count == 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE engines ADD COLUMN runtime_type TEXT DEFAULT 'container'`)
		if err != nil {
			return fmt.Errorf("add runtime_type column: %w", err)
		}
	}

	// Add binary_path column to engines table
	err = d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('engines') WHERE name='binary_path'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check binary_path column: %w", err)
	}
	if count == 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE engines ADD COLUMN binary_path TEXT`)
		if err != nil {
			return fmt.Errorf("add binary_path column: %w", err)
		}
	}

	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (d *DB) migrateV5(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if version >= 5 {
		return nil
	}

	// Rename gpu_compute_cap → gpu_compute_id (vendor-neutral)
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('hardware_profiles') WHERE name='gpu_compute_cap'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check gpu_compute_cap column: %w", err)
	}
	if count > 0 {
		_, err = d.db.ExecContext(ctx, `ALTER TABLE hardware_profiles RENAME COLUMN gpu_compute_cap TO gpu_compute_id`)
		if err != nil {
			return fmt.Errorf("rename gpu_compute_cap: %w", err)
		}
	}

	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (d *DB) migrateV6(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if version >= 6 {
		return nil
	}

	ddl := `CREATE TABLE IF NOT EXISTS rollback_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tool_name TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_name TEXT NOT NULL,
    snapshot TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);`
	if _, err := d.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create rollback_snapshots table: %w", err)
	}
	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 6"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (d *DB) migrateV7(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if version >= 7 {
		return nil
	}

	ddl := `
CREATE TABLE IF NOT EXISTS patrol_alerts (
    id TEXT PRIMARY KEY,
    severity TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME,
    resolved BOOLEAN NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS power_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    gpu_index INTEGER NOT NULL DEFAULT 0,
    power_watts REAL,
    temperature_c REAL,
    utilization_pct REAL,
    vram_used_mib INTEGER,
    vram_total_mib INTEGER
);
CREATE INDEX IF NOT EXISTS idx_power_samples_ts ON power_samples(timestamp);

CREATE TABLE IF NOT EXISTS validation_results (
    id TEXT PRIMARY KEY,
    config_id TEXT NOT NULL,
    hardware TEXT NOT NULL,
    engine TEXT NOT NULL,
    model TEXT NOT NULL,
    metric TEXT NOT NULL,
    predicted_value REAL,
    actual_value REAL,
    deviation_pct REAL,
    validated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (config_id) REFERENCES configurations(id)
);

CREATE TABLE IF NOT EXISTS tuning_sessions (
    id TEXT PRIMARY KEY,
    model TEXT NOT NULL,
    engine TEXT,
    status TEXT NOT NULL DEFAULT 'running',
    progress INTEGER DEFAULT 0,
    total INTEGER DEFAULT 0,
    best_config TEXT,
    best_score REAL DEFAULT 0,
    results TEXT,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS apps (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    spec TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS app_dependencies (
    app_id TEXT NOT NULL REFERENCES apps(id),
    need_type TEXT NOT NULL,
    model TEXT,
    deploy_name TEXT,
    satisfied BOOLEAN DEFAULT 0,
    PRIMARY KEY (app_id, need_type)
);

CREATE TABLE IF NOT EXISTS open_questions (
    id TEXT PRIMARY KEY,
    source_asset TEXT NOT NULL,
    question TEXT NOT NULL,
    test_command TEXT,
    expected TEXT,
    status TEXT NOT NULL DEFAULT 'untested',
    actual_result TEXT,
    tested_at DATETIME,
    hardware TEXT
);`
	if _, err := d.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create v7 tables: %w", err)
	}
	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (d *DB) migrateV8(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if version >= 8 {
		return nil
	}

	ddl := `
CREATE TABLE IF NOT EXISTS exploration_runs (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    goal TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    executor TEXT NOT NULL,
    planner TEXT NOT NULL,
    status TEXT NOT NULL,
    hardware_id TEXT,
    engine_id TEXT,
    model_id TEXT,
    source_ref TEXT,
    approval_mode TEXT NOT NULL DEFAULT 'none',
    approved_at DATETIME,
    started_at DATETIME,
    completed_at DATETIME,
    error TEXT,
    plan_json TEXT NOT NULL,
    summary_json TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_er_status ON exploration_runs(status);
CREATE INDEX IF NOT EXISTS idx_er_kind ON exploration_runs(kind);
CREATE INDEX IF NOT EXISTS idx_er_lookup ON exploration_runs(hardware_id, engine_id, model_id);

CREATE TABLE IF NOT EXISTS exploration_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL REFERENCES exploration_runs(id),
    step_index INTEGER NOT NULL,
    step_kind TEXT NOT NULL,
    status TEXT NOT NULL,
    tool_name TEXT,
    request_json TEXT,
    response_json TEXT,
    artifact_type TEXT,
    artifact_id TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_ee_run ON exploration_events(run_id, step_index);`
	if _, err := d.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create v8 tables: %w", err)
	}
	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 8"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func (d *DB) migrateV9(ctx context.Context) error {
	var version int
	_ = d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if version >= 9 {
		return nil
	}

	rows, err := d.db.QueryContext(ctx, "PRAGMA table_info(model_variants)")
	if err != nil {
		return fmt.Errorf("inspect model_variants: %w", err)
	}

	hasGPUCountMin := false
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultV   any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultV, &primaryKey); err != nil {
			return fmt.Errorf("scan model_variants column: %w", err)
		}
		if name == "gpu_count_min" {
			hasGPUCountMin = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate model_variants columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close model_variants column rows: %w", err)
	}

	if !hasGPUCountMin {
		if _, err := d.db.ExecContext(ctx, `ALTER TABLE model_variants ADD COLUMN gpu_count_min INTEGER`); err != nil {
			return fmt.Errorf("add model_variants.gpu_count_min: %w", err)
		}
	}

	if _, err := d.db.ExecContext(ctx, "PRAGMA user_version = 9"); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}
