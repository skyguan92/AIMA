package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// InsertPatrolAlert persists a patrol alert.
func (d *DB) InsertPatrolAlert(ctx context.Context, id, severity, typ, message string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO patrol_alerts (id, severity, type, message) VALUES (?, ?, ?, ?)`,
		id, severity, typ, message)
	return err
}

// ListPatrolAlerts returns alerts, optionally filtering by resolved status.
func (d *DB) ListPatrolAlerts(ctx context.Context, onlyActive bool) ([]map[string]any, error) {
	query := `SELECT id, severity, type, message, created_at, resolved_at, resolved FROM patrol_alerts`
	if onlyActive {
		query += ` WHERE resolved = 0`
	}
	query += ` ORDER BY created_at DESC LIMIT 100`
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	alerts := make([]map[string]any, 0)
	for rows.Next() {
		var id, severity, typ, message, createdAt string
		var resolvedAt sql.NullString
		var resolved bool
		if err := rows.Scan(&id, &severity, &typ, &message, &createdAt, &resolvedAt, &resolved); err != nil {
			continue
		}
		a := map[string]any{
			"id": id, "severity": severity, "type": typ, "message": message,
			"created_at": createdAt, "resolved": resolved,
		}
		if resolvedAt.Valid {
			a["resolved_at"] = resolvedAt.String
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// InsertPowerSample records a power/temp/util snapshot.
func (d *DB) InsertPowerSample(ctx context.Context, gpuIndex int, powerW, tempC, utilPct float64, vramUsed, vramTotal int) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO power_samples (gpu_index, power_watts, temperature_c, utilization_pct, vram_used_mib, vram_total_mib) VALUES (?, ?, ?, ?, ?, ?)`,
		gpuIndex, powerW, tempC, utilPct, vramUsed, vramTotal)
	return err
}

// QueryPowerHistory returns aggregated power samples in a time range.
func (d *DB) QueryPowerHistory(ctx context.Context, fromTime, toTime string, intervalS int) ([]map[string]any, error) {
	// Group by interval buckets using strftime
	query := `SELECT
		strftime('%Y-%m-%dT%H:%M:00', timestamp) as bucket,
		AVG(power_watts) as avg_power,
		MAX(power_watts) as max_power,
		AVG(temperature_c) as avg_temp,
		AVG(utilization_pct) as avg_util,
		AVG(vram_used_mib) as avg_vram_used
	FROM power_samples
	WHERE timestamp >= ? AND timestamp <= ?
	GROUP BY bucket
	ORDER BY bucket`
	rows, err := d.db.QueryContext(ctx, query, fromTime, toTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]map[string]any, 0)
	for rows.Next() {
		var bucket string
		var avgPower, maxPower, avgTemp, avgUtil, avgVRAM float64
		if err := rows.Scan(&bucket, &avgPower, &maxPower, &avgTemp, &avgUtil, &avgVRAM); err != nil {
			continue
		}
		results = append(results, map[string]any{
			"timestamp": bucket, "avg_power_watts": avgPower, "max_power_watts": maxPower,
			"avg_temperature_c": avgTemp, "avg_utilization_pct": avgUtil, "avg_vram_used_mib": int(avgVRAM),
		})
	}
	return results, rows.Err()
}

// PrunePowerSamples removes samples older than retentionDays.
func (d *DB) PrunePowerSamples(ctx context.Context, retentionDays int) error {
	_, err := d.db.ExecContext(ctx,
		`DELETE FROM power_samples WHERE timestamp < datetime('now', ? || ' days')`,
		fmt.Sprintf("-%d", retentionDays))
	return err
}

// InsertValidation records a predicted vs actual comparison.
func (d *DB) InsertValidation(ctx context.Context, id, configID, hardware, engine, model, metric string, predicted, actual, deviation float64) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO validation_results (id, config_id, hardware, engine, model, metric, predicted_value, actual_value, deviation_pct) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, configID, hardware, engine, model, metric, predicted, actual, deviation)
	return err
}

// ListValidations returns validation results for a hardware/engine/model combo.
func (d *DB) ListValidations(ctx context.Context, hardware, engine, model string) ([]map[string]any, error) {
	query := `SELECT id, config_id, hardware, engine, model, metric, predicted_value, actual_value, deviation_pct, validated_at FROM validation_results WHERE 1=1`
	var args []any
	if hardware != "" {
		query += ` AND hardware = ?`
		args = append(args, hardware)
	}
	if engine != "" {
		query += ` AND engine = ?`
		args = append(args, engine)
	}
	if model != "" {
		query += ` AND model = ?`
		args = append(args, model)
	}
	query += ` ORDER BY validated_at DESC LIMIT 50`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]map[string]any, 0)
	for rows.Next() {
		var id, configID, hw, eng, mdl, metric, validatedAt string
		var predicted, actual, deviation float64
		if err := rows.Scan(&id, &configID, &hw, &eng, &mdl, &metric, &predicted, &actual, &deviation, &validatedAt); err != nil {
			continue
		}
		status := "accurate"
		if deviation > 20 || deviation < -20 {
			status = "divergent"
		}
		results = append(results, map[string]any{
			"id": id, "config_id": configID, "hardware": hw, "engine": eng, "model": mdl,
			"metric": metric, "predicted": predicted, "actual": actual, "deviation_pct": deviation,
			"status": status, "validated_at": validatedAt,
		})
	}
	return results, rows.Err()
}

// SaveSnapshot writes a rollback snapshot and prunes old entries (keeps last 10).
func (d *DB) SaveSnapshot(ctx context.Context, s *RollbackSnapshot) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO rollback_snapshots (tool_name, resource_type, resource_name, snapshot) VALUES (?, ?, ?, ?)`,
		s.ToolName, s.ResourceType, s.ResourceName, s.Snapshot)
	if err != nil {
		return fmt.Errorf("save snapshot for %s: %w", s.ResourceName, err)
	}
	// Prune: keep only the 10 most recent
	_, _ = d.db.ExecContext(ctx,
		`DELETE FROM rollback_snapshots WHERE id NOT IN (SELECT id FROM rollback_snapshots ORDER BY id DESC LIMIT 10)`)
	return nil
}

// ListSnapshots returns the most recent rollback snapshots (up to 10).
func (d *DB) ListSnapshots(ctx context.Context) ([]*RollbackSnapshot, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, tool_name, resource_type, resource_name, snapshot, created_at
		 FROM rollback_snapshots ORDER BY id DESC LIMIT 10`)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()
	snapshots := make([]*RollbackSnapshot, 0)
	for rows.Next() {
		s := &RollbackSnapshot{}
		if err := rows.Scan(&s.ID, &s.ToolName, &s.ResourceType, &s.ResourceName, &s.Snapshot, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan snapshot row: %w", err)
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, rows.Err()
}

// GetSnapshot returns a single rollback snapshot by ID.
func (d *DB) GetSnapshot(ctx context.Context, id int64) (*RollbackSnapshot, error) {
	s := &RollbackSnapshot{}
	err := d.db.QueryRowContext(ctx,
		`SELECT id, tool_name, resource_type, resource_name, snapshot, created_at
		 FROM rollback_snapshots WHERE id = ?`, id).Scan(
		&s.ID, &s.ToolName, &s.ResourceType, &s.ResourceName, &s.Snapshot, &s.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("snapshot %d not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get snapshot %d: %w", id, err)
	}
	return s, nil
}

// ClearStaticKnowledge deletes all rows from static knowledge tables.
// Called on startup before reloading from go:embed YAML.
func (d *DB) ClearStaticKnowledge(ctx context.Context) error {
	// Order matters: child tables first (foreign keys)
	tables := []string{
		"engine_hardware_compat",
		"engine_features",
		"model_variants",
		"partition_strategies",
		"engine_assets",
		"model_assets",
		"hardware_profiles",
	}
	for _, t := range tables {
		if _, err := d.db.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return fmt.Errorf("clear %s: %w", t, err)
		}
	}
	return nil
}

// InsertApp registers an app with its spec.
func (d *DB) InsertApp(ctx context.Context, id, name, spec string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO apps (id, name, spec, status) VALUES (?, ?, ?, 'pending')`,
		id, name, spec)
	return err
}

// ListApps returns all registered apps with their dependency satisfaction status.
func (d *DB) ListApps(ctx context.Context) ([]map[string]any, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT a.id, a.name, a.spec, a.status, a.created_at,
			COALESCE((SELECT COUNT(*) FROM app_dependencies WHERE app_id = a.id), 0) as total_deps,
			COALESCE((SELECT COUNT(*) FROM app_dependencies WHERE app_id = a.id AND satisfied = 1), 0) as satisfied_deps
		 FROM apps a ORDER BY a.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, spec, status, createdAt string
		var totalDeps, satisfiedDeps int
		if err := rows.Scan(&id, &name, &spec, &status, &createdAt, &totalDeps, &satisfiedDeps); err != nil {
			continue
		}
		results = append(results, map[string]any{
			"id": id, "name": name, "spec": json.RawMessage(spec), "status": status,
			"created_at": createdAt, "total_deps": totalDeps, "satisfied_deps": satisfiedDeps,
		})
	}
	return results, rows.Err()
}

// UpsertAppDependency records a dependency for an app.
func (d *DB) UpsertAppDependency(ctx context.Context, appID, needType, model, deployName string, satisfied bool) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO app_dependencies (app_id, need_type, model, deploy_name, satisfied) VALUES (?, ?, ?, ?, ?)`,
		appID, needType, model, deployName, satisfied)
	return err
}

// UpdateAppStatus updates an app's provisioning status.
func (d *DB) UpdateAppStatus(ctx context.Context, id, status string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE apps SET status = ? WHERE id = ?`, status, id)
	return err
}

// GetSyncTimestamp returns the last sync timestamp for a direction (push/pull).
func (d *DB) GetSyncTimestamp(ctx context.Context, direction string) (string, error) {
	// Store sync metadata in the config table (already exists)
	var val string
	err := d.db.QueryRowContext(ctx,
		`SELECT value FROM config WHERE key = ?`, "sync_"+direction+"_at").Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SetSyncTimestamp records the last sync timestamp.
func (d *DB) SetSyncTimestamp(ctx context.Context, direction string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO config (key, value) VALUES (?, datetime('now'))`,
		"sync_"+direction+"_at")
	return err
}
