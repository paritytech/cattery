package repositories

import (
	"context"
	"database/sql"
	"time"
)

type SqliteRestarterRepository struct {
	db *sql.DB
}

func NewSqliteRestarterRepository(db *sql.DB) (*SqliteRestarterRepository, error) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS restart_requests (
			workflow_run_id INTEGER PRIMARY KEY,
			org_name        TEXT NOT NULL,
			repo_name       TEXT NOT NULL,
			created_at      TEXT NOT NULL
		)`)
	if err != nil {
		return nil, err
	}
	return &SqliteRestarterRepository{db: db}, nil
}

func (s *SqliteRestarterRepository) SaveRestartRequest(ctx context.Context, workflowRunId int64, orgName string, repoName string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO restart_requests (workflow_run_id, org_name, repo_name, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(workflow_run_id) DO UPDATE SET
			org_name = excluded.org_name,
			repo_name = excluded.repo_name,
			created_at = excluded.created_at`,
		workflowRunId, orgName, repoName, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SqliteRestarterRepository) DeleteRestartRequest(ctx context.Context, workflowRunId int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM restart_requests WHERE workflow_run_id = ?`, workflowRunId)
	return err
}

func (s *SqliteRestarterRepository) GetAllPendingRestartRequests(ctx context.Context) ([]RestartRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workflow_run_id, org_name, repo_name, created_at FROM restart_requests`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RestartRequest
	for rows.Next() {
		var r RestartRequest
		var createdAt string
		err := rows.Scan(&r.WorkflowRunId, &r.OrgName, &r.RepoName, &createdAt)
		if err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		result = append(result, r)
	}
	return result, rows.Err()
}
