package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (d *DB) InsertModel(ctx context.Context, m *Model) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO models (id, name, type, path, format, size_bytes, detected_arch, detected_params,
		                    model_class, total_params, active_params, quantization, quant_src, status, download_progress)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Name, m.Type, m.Path, m.Format, m.SizeBytes, m.DetectedArch, m.DetectedParams,
		m.ModelClass, m.TotalParams, m.ActiveParams, m.Quantization, m.QuantSrc, m.Status, m.DownloadProgress)
	if err != nil {
		return fmt.Errorf("insert model %s: %w", m.ID, err)
	}
	return nil
}

// UpsertScannedModel inserts a new model or updates metadata of an existing one.
// If a model with the same path exists, update that record instead of creating a duplicate.
// Status defaults to 'registered' if not set.
func (d *DB) UpsertScannedModel(ctx context.Context, m *Model) error {
	// First check if a model with this path already exists
	var existingID string
	var existingStatus string
	err := d.db.QueryRowContext(ctx, `SELECT id, COALESCE(status,'registered') FROM models WHERE path = ?`, m.Path).Scan(&existingID, &existingStatus)
	if err == nil {
		// Existing model found with same path, use its ID for update
		m.ID = existingID
		// Preserve existing status if new status is empty
		if m.Status == "" {
			m.Status = existingStatus
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing model by path %s: %w", m.Path, err)
	}
	// else: no existing model, use the scanned hash ID

	// Default status to 'registered' if not set
	if m.Status == "" {
		m.Status = "registered"
	}

	_, err = d.db.ExecContext(ctx,
		`INSERT INTO models (id, name, type, path, format, size_bytes, detected_arch, detected_params,
		                    model_class, total_params, active_params, quantization, quant_src, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name=excluded.name, type=excluded.type, path=excluded.path,
		   format=excluded.format, size_bytes=excluded.size_bytes,
		   detected_arch=excluded.detected_arch, detected_params=excluded.detected_params,
		   model_class=excluded.model_class, total_params=excluded.total_params,
		   active_params=excluded.active_params, quantization=excluded.quantization,
		   quant_src=excluded.quant_src, status=excluded.status`,
		m.ID, m.Name, m.Type, m.Path, m.Format, m.SizeBytes, m.DetectedArch, m.DetectedParams,
		m.ModelClass, m.TotalParams, m.ActiveParams, m.Quantization, m.QuantSrc, m.Status)
	if err != nil {
		return fmt.Errorf("upsert scanned model %s: %w", m.ID, err)
	}
	return nil
}

func (d *DB) GetModel(ctx context.Context, id string) (*Model, error) {
	m := &Model{}
	err := d.db.QueryRowContext(ctx,
		`SELECT id, name, type, path, COALESCE(format,''), COALESCE(size_bytes,0),
		        COALESCE(detected_arch,''), COALESCE(detected_params,''),
		        COALESCE(model_class,''), COALESCE(total_params,0), COALESCE(active_params,0),
		        COALESCE(quantization,''), COALESCE(quant_src,''),
		        COALESCE(status,'registered'), COALESCE(download_progress,0), created_at
		 FROM models WHERE id = ? OR name = ?
		 ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END
		 LIMIT 1`, id, id, id).Scan(
		&m.ID, &m.Name, &m.Type, &m.Path, &m.Format, &m.SizeBytes,
		&m.DetectedArch, &m.DetectedParams, &m.ModelClass, &m.TotalParams, &m.ActiveParams,
		&m.Quantization, &m.QuantSrc, &m.Status, &m.DownloadProgress, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("model %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get model %s: %w", id, err)
	}
	return m, nil
}

func (d *DB) ListModels(ctx context.Context) ([]*Model, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, name, type, path, COALESCE(format,''), COALESCE(size_bytes,0),
		        COALESCE(detected_arch,''), COALESCE(detected_params,''),
		        COALESCE(model_class,''), COALESCE(total_params,0), COALESCE(active_params,0),
		        COALESCE(quantization,''), COALESCE(quant_src,''),
		        COALESCE(status,'registered'), COALESCE(download_progress,0), created_at
		 FROM models ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	models := make([]*Model, 0)
	for rows.Next() {
		m := &Model{}
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Path, &m.Format, &m.SizeBytes,
			&m.DetectedArch, &m.DetectedParams, &m.ModelClass, &m.TotalParams, &m.ActiveParams,
			&m.Quantization, &m.QuantSrc, &m.Status, &m.DownloadProgress, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan model row: %w", err)
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

func (d *DB) UpdateModelStatus(ctx context.Context, id, status string) error {
	res, err := d.db.ExecContext(ctx, `UPDATE models SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("update model status %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("model %s not found", id)
	}
	return nil
}

// FindModelByName searches for a model by name with prioritized matching:
// 1. Case-insensitive exact  2. Substring match
func (d *DB) FindModelByName(ctx context.Context, name string) (*Model, error) {
	queries := []string{
		`SELECT id, name, type, path, COALESCE(format,''), COALESCE(size_bytes,0),
		        COALESCE(detected_arch,''), COALESCE(detected_params,''),
		        COALESCE(model_class,''), COALESCE(total_params,0), COALESCE(active_params,0),
		        COALESCE(quantization,''), COALESCE(quant_src,''),
		        COALESCE(status,'registered'), COALESCE(download_progress,0), created_at
		 FROM models WHERE LOWER(name) = LOWER(?) ORDER BY created_at DESC LIMIT 1`,
		`SELECT id, name, type, path, COALESCE(format,''), COALESCE(size_bytes,0),
		        COALESCE(detected_arch,''), COALESCE(detected_params,''),
		        COALESCE(model_class,''), COALESCE(total_params,0), COALESCE(active_params,0),
		        COALESCE(quantization,''), COALESCE(quant_src,''),
		        COALESCE(status,'registered'), COALESCE(download_progress,0), created_at
		 FROM models WHERE LOWER(name) LIKE '%' || LOWER(?) || '%' ORDER BY created_at DESC LIMIT 1`,
	}
	for _, q := range queries {
		m := &Model{}
		err := d.db.QueryRowContext(ctx, q, name).Scan(
			&m.ID, &m.Name, &m.Type, &m.Path, &m.Format, &m.SizeBytes,
			&m.DetectedArch, &m.DetectedParams, &m.ModelClass, &m.TotalParams, &m.ActiveParams,
			&m.Quantization, &m.QuantSrc, &m.Status, &m.DownloadProgress, &m.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("find model by name %q: %w", name, err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("model %q not found", name)
}

func (d *DB) DeleteModel(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM models WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete model %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("model %s not found", id)
	}
	return nil
}
