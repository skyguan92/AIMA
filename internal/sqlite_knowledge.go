package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (d *DB) InsertNote(ctx context.Context, n *KnowledgeNote) error {
	if n.ID == "" {
		var buf [8]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return fmt.Errorf("generate note id: %w", err)
		}
		n.ID = hex.EncodeToString(buf[:])
	}
	tagsJSON, err := json.Marshal(n.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags for note %s: %w", n.ID, err)
	}
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO knowledge_notes (id, title, tags, hardware_profile, model, engine, content, confidence)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Title, string(tagsJSON), n.HardwareProfile, n.Model, n.Engine, n.Content, n.Confidence)
	if err != nil {
		return fmt.Errorf("insert note %s: %w", n.ID, err)
	}
	return nil
}

func (d *DB) SearchNotes(ctx context.Context, filter NoteFilter) ([]*KnowledgeNote, error) {
	query := `SELECT id, title, COALESCE(tags,'[]'), COALESCE(hardware_profile,''),
	                 COALESCE(model,''), COALESCE(engine,''), content,
	                 COALESCE(confidence,''), created_at
	          FROM knowledge_notes WHERE 1=1`
	var args []any

	if filter.HardwareProfile != "" {
		query += " AND hardware_profile = ?"
		args = append(args, filter.HardwareProfile)
	}
	if filter.Model != "" {
		query += " AND model = ?"
		args = append(args, filter.Model)
	}
	if filter.Engine != "" {
		query += " AND engine = ?"
		args = append(args, filter.Engine)
	}
	query += " ORDER BY created_at DESC"

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search notes: %w", err)
	}
	defer rows.Close()

	notes := make([]*KnowledgeNote, 0)
	for rows.Next() {
		n := &KnowledgeNote{}
		var tagsStr string
		if err := rows.Scan(&n.ID, &n.Title, &tagsStr, &n.HardwareProfile,
			&n.Model, &n.Engine, &n.Content, &n.Confidence, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan note row: %w", err)
		}
		if err := json.Unmarshal([]byte(tagsStr), &n.Tags); err != nil {
			n.Tags = splitTags(tagsStr)
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

func splitTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func (d *DB) DeleteNote(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM knowledge_notes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete note %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("note %s not found", id)
	}
	return nil
}

// UpsertOpenQuestion inserts or updates an open question.
func (d *DB) UpsertOpenQuestion(ctx context.Context, id, sourceAsset, question, testCommand, expected, status, actualResult string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO open_questions (id, source_asset, question, test_command, expected, status, actual_result)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		     source_asset = excluded.source_asset,
		     question = excluded.question,
		     test_command = excluded.test_command,
		     expected = excluded.expected,
		     status = CASE
		       WHEN open_questions.status IN ('tested', 'confirmed', 'confirmed_incompatible', 'rejected') THEN open_questions.status
		       WHEN excluded.status <> '' AND excluded.status <> 'untested' THEN excluded.status
		       ELSE open_questions.status
		     END,
		     actual_result = CASE
		       WHEN open_questions.status IN ('tested', 'confirmed', 'confirmed_incompatible', 'rejected')
		            AND COALESCE(open_questions.actual_result, '') <> '' THEN open_questions.actual_result
		       WHEN excluded.status <> '' AND excluded.status <> 'untested'
		            AND COALESCE(excluded.actual_result, '') <> '' THEN excluded.actual_result
		       ELSE open_questions.actual_result
		     END`,
		id, sourceAsset, question, testCommand, expected, status, actualResult)
	return err
}

// GetOpenQuestion returns a single open question by ID.
func (d *DB) GetOpenQuestion(ctx context.Context, id string) (*OpenQuestion, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, source_asset, question, test_command, expected, status, actual_result, tested_at, hardware
		   FROM open_questions
		  WHERE id = ?`,
		id)

	var q OpenQuestion
	var testCmd, expected, actualResult, testedAt, hardware sql.NullString
	if err := row.Scan(&q.ID, &q.SourceAsset, &q.Question, &testCmd, &expected, &q.Status, &actualResult, &testedAt, &hardware); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("open question %s not found", id)
		}
		return nil, fmt.Errorf("get open question %s: %w", id, err)
	}
	if testCmd.Valid {
		q.TestCommand = testCmd.String
	}
	if expected.Valid {
		q.Expected = expected.String
	}
	if actualResult.Valid {
		q.ActualResult = actualResult.String
	}
	if testedAt.Valid {
		if ts, err := time.Parse("2006-01-02 15:04:05", testedAt.String); err == nil {
			q.TestedAt = ts
		}
	}
	if hardware.Valid {
		q.Hardware = hardware.String
	}
	return &q, nil
}

// ListOpenQuestions returns open questions, optionally filtering by status.
func (d *DB) ListOpenQuestions(ctx context.Context, status string) ([]map[string]any, error) {
	query := `SELECT id, source_asset, question, test_command, expected, status, actual_result, tested_at, hardware FROM open_questions`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY CASE status
		WHEN 'untested' THEN 0
		WHEN 'tested' THEN 1
		WHEN 'confirmed' THEN 2
		WHEN 'confirmed_incompatible' THEN 3
		ELSE 4 END`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]map[string]any, 0)
	for rows.Next() {
		var id, source, question, status string
		var testCmd, expected, actualResult, testedAt, hardware sql.NullString
		if err := rows.Scan(&id, &source, &question, &testCmd, &expected, &status, &actualResult, &testedAt, &hardware); err != nil {
			continue
		}
		r := map[string]any{
			"id": id, "source_asset": source, "question": question, "status": status,
		}
		if testCmd.Valid {
			r["test_command"] = testCmd.String
		}
		if expected.Valid {
			r["expected"] = expected.String
		}
		if actualResult.Valid {
			r["actual_result"] = actualResult.String
		}
		if testedAt.Valid {
			r["tested_at"] = testedAt.String
		}
		if hardware.Valid {
			r["hardware"] = hardware.String
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ResolveOpenQuestion marks a question as confirmed or rejected with the actual result.
func (d *DB) ResolveOpenQuestion(ctx context.Context, id, status, actualResult, hardware string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE open_questions SET status = ?, actual_result = ?, tested_at = datetime('now'), hardware = ? WHERE id = ?`,
		status, actualResult, hardware, id)
	return err
}
