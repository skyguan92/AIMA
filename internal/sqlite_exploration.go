package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (d *DB) InsertExplorationRun(ctx context.Context, run *ExplorationRun) error {
	if run == nil {
		return fmt.Errorf("exploration run is nil")
	}
	_, err := d.db.ExecContext(ctx, `
INSERT INTO exploration_runs (
    id, kind, goal, requested_by, executor, planner, status,
    hardware_id, engine_id, model_id, source_ref, approval_mode,
    approved_at, started_at, completed_at, error, plan_json, summary_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.Kind, run.Goal, run.RequestedBy, run.Executor, run.Planner, run.Status,
		nullStr(run.HardwareID), nullStr(run.EngineID), nullStr(run.ModelID), nullStr(run.SourceRef), run.ApprovalMode,
		nullTime(run.ApprovedAt), nullTime(run.StartedAt), nullTime(run.CompletedAt), nullStr(run.Error), run.PlanJSON, nullStr(run.SummaryJSON))
	if err != nil {
		return fmt.Errorf("insert exploration run %s: %w", run.ID, err)
	}
	return nil
}

func (d *DB) UpdateExplorationRun(ctx context.Context, run *ExplorationRun) error {
	if run == nil {
		return fmt.Errorf("exploration run is nil")
	}
	_, err := d.db.ExecContext(ctx, `
UPDATE exploration_runs
SET kind = ?, goal = ?, requested_by = ?, executor = ?, planner = ?, status = ?,
    hardware_id = ?, engine_id = ?, model_id = ?, source_ref = ?, approval_mode = ?,
    approved_at = ?, started_at = ?, completed_at = ?, error = ?, plan_json = ?, summary_json = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?`,
		run.Kind, run.Goal, run.RequestedBy, run.Executor, run.Planner, run.Status,
		nullStr(run.HardwareID), nullStr(run.EngineID), nullStr(run.ModelID), nullStr(run.SourceRef), run.ApprovalMode,
		nullTime(run.ApprovedAt), nullTime(run.StartedAt), nullTime(run.CompletedAt), nullStr(run.Error), run.PlanJSON, nullStr(run.SummaryJSON),
		run.ID)
	if err != nil {
		return fmt.Errorf("update exploration run %s: %w", run.ID, err)
	}
	return nil
}

func (d *DB) GetExplorationRun(ctx context.Context, id string) (*ExplorationRun, error) {
	var run ExplorationRun
	var hardwareID, engineID, modelID, sourceRef, errStr, summary sql.NullString
	var approvedAt, startedAt, completedAt sql.NullTime
	err := d.db.QueryRowContext(ctx, `
SELECT id, kind, goal, requested_by, executor, planner, status,
       COALESCE(hardware_id,''), COALESCE(engine_id,''), COALESCE(model_id,''), COALESCE(source_ref,''),
       approval_mode, approved_at, started_at, completed_at, error,
       plan_json, summary_json, created_at, updated_at
FROM exploration_runs
WHERE id = ?`, id).Scan(
		&run.ID, &run.Kind, &run.Goal, &run.RequestedBy, &run.Executor, &run.Planner, &run.Status,
		&hardwareID, &engineID, &modelID, &sourceRef,
		&run.ApprovalMode, &approvedAt, &startedAt, &completedAt, &errStr,
		&run.PlanJSON, &summary, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("exploration run %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get exploration run %s: %w", id, err)
	}
	run.HardwareID = hardwareID.String
	run.EngineID = engineID.String
	run.ModelID = modelID.String
	run.SourceRef = sourceRef.String
	run.Error = errStr.String
	run.SummaryJSON = summary.String
	if approvedAt.Valid {
		run.ApprovedAt = approvedAt.Time
	}
	if startedAt.Valid {
		run.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = completedAt.Time
	}
	return &run, nil
}

func (d *DB) ListExplorationRuns(ctx context.Context, status string, limit int) ([]*ExplorationRun, error) {
	query := `
SELECT id, kind, goal, requested_by, executor, planner, status,
       COALESCE(hardware_id,''), COALESCE(engine_id,''), COALESCE(model_id,''), COALESCE(source_ref,''),
       approval_mode, approved_at, started_at, completed_at, error,
       plan_json, summary_json, created_at, updated_at
FROM exploration_runs`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list exploration runs: %w", err)
	}
	defer rows.Close()

	runs := make([]*ExplorationRun, 0)
	for rows.Next() {
		var run ExplorationRun
		var hardwareID, engineID, modelID, sourceRef, errStr, summary sql.NullString
		var approvedAt, startedAt, completedAt sql.NullTime
		if err := rows.Scan(
			&run.ID, &run.Kind, &run.Goal, &run.RequestedBy, &run.Executor, &run.Planner, &run.Status,
			&hardwareID, &engineID, &modelID, &sourceRef,
			&run.ApprovalMode, &approvedAt, &startedAt, &completedAt, &errStr,
			&run.PlanJSON, &summary, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan exploration run: %w", err)
		}
		run.HardwareID = hardwareID.String
		run.EngineID = engineID.String
		run.ModelID = modelID.String
		run.SourceRef = sourceRef.String
		run.Error = errStr.String
		run.SummaryJSON = summary.String
		if approvedAt.Valid {
			run.ApprovedAt = approvedAt.Time
		}
		if startedAt.Valid {
			run.StartedAt = startedAt.Time
		}
		if completedAt.Valid {
			run.CompletedAt = completedAt.Time
		}
		cp := run
		runs = append(runs, &cp)
	}
	return runs, rows.Err()
}

func (d *DB) CountActiveExplorationRuns(ctx context.Context) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM exploration_runs WHERE status IN ('planning', 'needs_approval', 'queued', 'running')`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active exploration runs: %w", err)
	}
	return count, nil
}

func (d *DB) InsertExplorationEvent(ctx context.Context, event *ExplorationEvent) error {
	if event == nil {
		return fmt.Errorf("exploration event is nil")
	}
	res, err := d.db.ExecContext(ctx, `
INSERT INTO exploration_events (
    run_id, step_index, step_kind, status, tool_name, request_json, response_json, artifact_type, artifact_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.RunID, event.StepIndex, event.StepKind, event.Status,
		nullStr(event.ToolName), nullStr(event.RequestJSON), nullStr(event.ResponseJSON), nullStr(event.ArtifactType), nullStr(event.ArtifactID))
	if err != nil {
		return fmt.Errorf("insert exploration event for run %s: %w", event.RunID, err)
	}
	if id, err := res.LastInsertId(); err == nil {
		event.ID = id
	}
	return nil
}

func (d *DB) ListExplorationEvents(ctx context.Context, runID string) ([]*ExplorationEvent, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT id, run_id, step_index, step_kind, status,
       COALESCE(tool_name,''), COALESCE(request_json,''), COALESCE(response_json,''),
       COALESCE(artifact_type,''), COALESCE(artifact_id,''), created_at
FROM exploration_events
WHERE run_id = ?
ORDER BY id ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("list exploration events for run %s: %w", runID, err)
	}
	defer rows.Close()

	events := make([]*ExplorationEvent, 0)
	for rows.Next() {
		var event ExplorationEvent
		if err := rows.Scan(&event.ID, &event.RunID, &event.StepIndex, &event.StepKind, &event.Status,
			&event.ToolName, &event.RequestJSON, &event.ResponseJSON, &event.ArtifactType, &event.ArtifactID, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan exploration event: %w", err)
		}
		events = append(events, &event)
	}
	return events, rows.Err()
}
