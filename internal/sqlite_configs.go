package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// UpdateConfigStatus transitions a configuration's status (e.g., experiment → golden).
func (d *DB) UpdateConfigStatus(ctx context.Context, configID, status string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE configurations SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, configID)
	if err != nil {
		return fmt.Errorf("update config status %s: %w", configID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("configuration %q not found", configID)
	}
	return nil
}

func (d *DB) InsertConfiguration(ctx context.Context, c *Configuration) error {
	tagsJSON, _ := json.Marshal(c.Tags)
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO configurations (id, hardware_id, engine_id, model_id, partition_slot, config, config_hash, derived_from, status, tags, source, device_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.HardwareID, c.EngineID, c.ModelID, c.Slot, c.Config, c.ConfigHash,
		nullStr(c.DerivedFrom), c.Status, string(tagsJSON), c.Source, c.DeviceID)
	if err != nil {
		return fmt.Errorf("insert configuration %s: %w", c.ID, err)
	}
	return nil
}

func (d *DB) GetConfiguration(ctx context.Context, id string) (*Configuration, error) {
	c := &Configuration{}
	var tagsStr, derivedFrom sql.NullString
	err := d.db.QueryRowContext(ctx,
		`SELECT id, hardware_id, engine_id, model_id, COALESCE(partition_slot,''),
		        config, config_hash, derived_from, COALESCE(status,'experiment'),
		        COALESCE(tags,'[]'), COALESCE(source,'local'), COALESCE(device_id,''),
		        created_at, updated_at
		 FROM configurations WHERE id = ?`, id).Scan(
		&c.ID, &c.HardwareID, &c.EngineID, &c.ModelID, &c.Slot,
		&c.Config, &c.ConfigHash, &derivedFrom, &c.Status,
		&tagsStr, &c.Source, &c.DeviceID, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("configuration %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get configuration %s: %w", id, err)
	}
	if derivedFrom.Valid {
		c.DerivedFrom = derivedFrom.String
	}
	_ = json.Unmarshal([]byte(tagsStr.String), &c.Tags)
	return c, nil
}

// FindConfigByHash returns a configuration matching the given config_hash, or nil if not found.
func (d *DB) FindConfigByHash(ctx context.Context, hash string) (*Configuration, error) {
	c := &Configuration{}
	var tagsStr, derivedFrom sql.NullString
	err := d.db.QueryRowContext(ctx,
		`SELECT id, hardware_id, engine_id, model_id, COALESCE(partition_slot,''),
		        config, config_hash, derived_from, COALESCE(status,'experiment'),
		        COALESCE(tags,'[]'), COALESCE(source,'local'), COALESCE(device_id,''),
		        created_at, updated_at
		 FROM configurations WHERE config_hash = ?`, hash).Scan(
		&c.ID, &c.HardwareID, &c.EngineID, &c.ModelID, &c.Slot,
		&c.Config, &c.ConfigHash, &derivedFrom, &c.Status,
		&tagsStr, &c.Source, &c.DeviceID, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find config by hash: %w", err)
	}
	if derivedFrom.Valid {
		c.DerivedFrom = derivedFrom.String
	}
	_ = json.Unmarshal([]byte(tagsStr.String), &c.Tags)
	return c, nil
}

// FindGoldenBenchmark returns the golden configuration and its best benchmark result
// for the given hardware/engine/model triple. Uses a single JOIN query to avoid
// MaxOpenConns(1) deadlocks. Returns (nil, nil, nil) if no golden config exists.
func (d *DB) FindGoldenBenchmark(ctx context.Context, hardware, engine, model string) (*Configuration, *BenchmarkResult, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT c.id, c.hardware_id, c.engine_id, c.model_id, COALESCE(c.partition_slot,''),
		        c.config, c.config_hash, c.derived_from, c.status,
		        COALESCE(c.tags,'[]'), COALESCE(c.source,'local'), COALESCE(c.device_id,''),
		        c.created_at, c.updated_at,
		        b.id, b.throughput_tps
		 FROM configurations c
		 LEFT JOIN benchmark_results b ON b.config_id = c.id
		 WHERE c.status = 'golden'
		   AND c.hardware_id = ? AND c.engine_id = ? AND c.model_id = ?
		 ORDER BY b.throughput_tps DESC
		 LIMIT 1`,
		hardware, engine, model)

	cfg := &Configuration{}
	var tagsStr, derivedFrom, benchID sql.NullString
	var throughput sql.NullFloat64
	err := row.Scan(
		&cfg.ID, &cfg.HardwareID, &cfg.EngineID, &cfg.ModelID, &cfg.Slot,
		&cfg.Config, &cfg.ConfigHash, &derivedFrom, &cfg.Status,
		&tagsStr, &cfg.Source, &cfg.DeviceID, &cfg.CreatedAt, &cfg.UpdatedAt,
		&benchID, &throughput)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("find golden benchmark: %w", err)
	}
	if derivedFrom.Valid {
		cfg.DerivedFrom = derivedFrom.String
	}
	_ = json.Unmarshal([]byte(tagsStr.String), &cfg.Tags)

	var bench *BenchmarkResult
	if benchID.Valid {
		bench = &BenchmarkResult{ID: benchID.String, ConfigID: cfg.ID, ThroughputTPS: throughput.Float64}
	}
	return cfg, bench, nil
}

func (d *DB) InsertBenchmarkResult(ctx context.Context, b *BenchmarkResult) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO benchmark_results (id, config_id, concurrency, input_len_bucket, output_len_bucket, modality,
		    ttft_ms_p50, ttft_ms_p95, ttft_ms_p99, tpot_ms_p50, tpot_ms_p95,
		    throughput_tps, qps, vram_usage_mib, ram_usage_mib, power_draw_watts, gpu_utilization_pct,
		    error_rate, oom_occurred, stability, duration_s, sample_count, agent_model, notes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.ConfigID, b.Concurrency, b.InputLenBucket, b.OutputLenBucket, b.Modality,
		b.TTFTP50ms, b.TTFTP95ms, b.TTFTP99ms, b.TPOTP50ms, b.TPOTP95ms,
		b.ThroughputTPS, b.QPS, b.VRAMUsageMiB, b.RAMUsageMiB, b.PowerDrawWatts, b.GPUUtilPct,
		b.ErrorRate, b.OOMOccurred, b.Stability, b.DurationS, b.SampleCount, b.AgentModel, b.Notes)
	if err != nil {
		return fmt.Errorf("insert benchmark %s: %w", b.ID, err)
	}
	return nil
}

func (d *DB) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("config key %q not found", key)
	}
	if err != nil {
		return "", fmt.Errorf("get config %q: %w", key, err)
	}
	return value, nil
}

func (d *DB) SetConfig(ctx context.Context, key, value string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO config (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value)
	if err != nil {
		return fmt.Errorf("set config %q: %w", key, err)
	}
	return nil
}

func (d *DB) LogAction(ctx context.Context, entry *AuditEntry) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO audit_log (agent_type, tool_name, arguments, result_summary) VALUES (?, ?, ?, ?)`,
		entry.AgentType, entry.ToolName, entry.Arguments, entry.ResultSummary)
	if err != nil {
		return fmt.Errorf("log action %s: %w", entry.ToolName, err)
	}
	return nil
}

// ListConfigurations returns configurations matching optional filters.
// Empty filter values are ignored.
func (d *DB) ListConfigurations(ctx context.Context, hardware, model, engine string) ([]*Configuration, error) {
	query := `SELECT id, hardware_id, engine_id, model_id, COALESCE(partition_slot,''),
	                 config, config_hash, derived_from, COALESCE(status,'experiment'),
	                 COALESCE(tags,'[]'), COALESCE(source,'local'), COALESCE(device_id,''),
	                 created_at, updated_at
	          FROM configurations WHERE 1=1`
	var args []any
	if hardware != "" {
		query += ` AND hardware_id = ?`
		args = append(args, hardware)
	}
	if model != "" {
		query += ` AND model_id = ?`
		args = append(args, model)
	}
	if engine != "" {
		query += ` AND engine_id = ?`
		args = append(args, engine)
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list configurations: %w", err)
	}
	defer rows.Close()

	configs := make([]*Configuration, 0)
	for rows.Next() {
		c := &Configuration{}
		var tagsStr, derivedFrom sql.NullString
		if err := rows.Scan(&c.ID, &c.HardwareID, &c.EngineID, &c.ModelID, &c.Slot,
			&c.Config, &c.ConfigHash, &derivedFrom, &c.Status,
			&tagsStr, &c.Source, &c.DeviceID, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan configuration row: %w", err)
		}
		if derivedFrom.Valid {
			c.DerivedFrom = derivedFrom.String
		}
		_ = json.Unmarshal([]byte(tagsStr.String), &c.Tags)
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

// ListBenchmarkResults returns benchmark results, optionally filtered by config IDs.
func (d *DB) ListBenchmarkResults(ctx context.Context, configIDs []string, limit int) ([]*BenchmarkResult, error) {
	query := `SELECT id, config_id, concurrency, COALESCE(input_len_bucket,''),
	                 COALESCE(output_len_bucket,''), COALESCE(modality,'text'),
	                 ttft_ms_p50, ttft_ms_p95, COALESCE(ttft_ms_p99,0),
	                 COALESCE(tpot_ms_p50,0), COALESCE(tpot_ms_p95,0),
	                 throughput_tps, COALESCE(qps,0),
	                 COALESCE(vram_usage_mib,0), COALESCE(ram_usage_mib,0),
	                 COALESCE(power_draw_watts,0), COALESCE(gpu_utilization_pct,0),
	                 COALESCE(error_rate,0), COALESCE(oom_occurred,0),
	                 COALESCE(stability,''), COALESCE(duration_s,0), COALESCE(sample_count,0),
	                 tested_at, COALESCE(agent_model,''), COALESCE(notes,'')
	          FROM benchmark_results WHERE 1=1`
	var args []any
	if len(configIDs) > 0 {
		placeholders := strings.Repeat("?,", len(configIDs))
		placeholders = placeholders[:len(placeholders)-1]
		query += fmt.Sprintf(` AND config_id IN (%s)`, placeholders)
		for _, id := range configIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY tested_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list benchmark results: %w", err)
	}
	defer rows.Close()

	results := make([]*BenchmarkResult, 0)
	for rows.Next() {
		b := &BenchmarkResult{}
		if err := rows.Scan(&b.ID, &b.ConfigID, &b.Concurrency, &b.InputLenBucket,
			&b.OutputLenBucket, &b.Modality,
			&b.TTFTP50ms, &b.TTFTP95ms, &b.TTFTP99ms, &b.TPOTP50ms, &b.TPOTP95ms,
			&b.ThroughputTPS, &b.QPS,
			&b.VRAMUsageMiB, &b.RAMUsageMiB, &b.PowerDrawWatts, &b.GPUUtilPct,
			&b.ErrorRate, &b.OOMOccurred, &b.Stability, &b.DurationS, &b.SampleCount,
			&b.TestedAt, &b.AgentModel, &b.Notes); err != nil {
			return nil, fmt.Errorf("scan benchmark row: %w", err)
		}
		results = append(results, b)
	}
	return results, rows.Err()
}
