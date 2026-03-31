package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (d *DB) InsertEngine(ctx context.Context, e *Engine) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO engines (id, type, image, tag, size_bytes, platform, runtime_type, binary_path, available)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Type, e.Image, e.Tag, e.SizeBytes, e.Platform, e.RuntimeType, e.BinaryPath, e.Available)
	if err != nil {
		return fmt.Errorf("insert engine %s: %w", e.ID, err)
	}
	return nil
}

// UpsertScannedEngine inserts a new engine or updates an existing one.
func (d *DB) UpsertScannedEngine(ctx context.Context, e *Engine) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO engines (id, type, image, tag, size_bytes, platform, runtime_type, binary_path, available)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   type=excluded.type, image=excluded.image, tag=excluded.tag,
		   size_bytes=excluded.size_bytes, platform=excluded.platform,
		   runtime_type=excluded.runtime_type, binary_path=excluded.binary_path,
		   available=excluded.available`,
		e.ID, e.Type, e.Image, e.Tag, e.SizeBytes, e.Platform, e.RuntimeType, e.BinaryPath, e.Available)
	if err != nil {
		return fmt.Errorf("upsert scanned engine %s: %w", e.ID, err)
	}
	return nil
}

func (d *DB) GetEngine(ctx context.Context, id string) (*Engine, error) {
	e := &Engine{}
	err := d.db.QueryRowContext(ctx,
		`SELECT id, type, image, tag, COALESCE(size_bytes,0), COALESCE(platform,''),
		        COALESCE(runtime_type,'container'), COALESCE(binary_path,''),
		        available, created_at
		 FROM engines WHERE id = ?`, id).Scan(
		&e.ID, &e.Type, &e.Image, &e.Tag, &e.SizeBytes, &e.Platform,
		&e.RuntimeType, &e.BinaryPath, &e.Available, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("engine %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get engine %s: %w", id, err)
	}
	return e, nil
}

func (d *DB) ListEngines(ctx context.Context) ([]*Engine, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, type, image, tag, COALESCE(size_bytes,0), COALESCE(platform,''),
		        COALESCE(runtime_type,'container'), COALESCE(binary_path,''),
		        available, created_at
		 FROM engines WHERE available = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list engines: %w", err)
	}
	defer rows.Close()
	engines := make([]*Engine, 0)
	for rows.Next() {
		e := &Engine{}
		if err := rows.Scan(&e.ID, &e.Type, &e.Image, &e.Tag, &e.SizeBytes,
			&e.Platform, &e.RuntimeType, &e.BinaryPath, &e.Available, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan engine row: %w", err)
		}
		engines = append(engines, e)
	}
	return engines, rows.Err()
}

// MarkEnginesUnavailableExcept sets available=false for engines whose ID is not in keepIDs.
// When runtimeType is non-empty, only engines of that runtime are affected (filtered scan).
// When runtimeType is empty, all engines not in keepIDs are marked unavailable (full scan).
func (d *DB) MarkEnginesUnavailableExcept(ctx context.Context, keepIDs []string, runtimeType string) error {
	if len(keepIDs) == 0 {
		// No scan results — don't wipe everything (might be a permission issue)
		return nil
	}
	placeholders := make([]string, len(keepIDs))
	args := make([]any, len(keepIDs))
	for i, id := range keepIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`UPDATE engines SET available = 0 WHERE id NOT IN (%s)`,
		strings.Join(placeholders, ","))
	if runtimeType != "" {
		query += ` AND runtime_type = ?`
		args = append(args, runtimeType)
	}
	_, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("mark stale engines unavailable: %w", err)
	}
	return nil
}

func (d *DB) DeleteEngine(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM engines WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete engine %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("engine %s not found", id)
	}
	return nil
}
