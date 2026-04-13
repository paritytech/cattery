package repositories

import (
	"cattery/lib/trays"
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type SqliteTrayRepository struct {
	db *sql.DB
}

func NewSqliteTrayRepository(db *sql.DB) (*SqliteTrayRepository, error) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS trays (
			id              TEXT PRIMARY KEY,
			tray_type_name  TEXT NOT NULL,
			provider_name   TEXT NOT NULL,
			github_org_name TEXT NOT NULL,
			github_runner_id INTEGER NOT NULL DEFAULT 0,
			job_run_id      INTEGER NOT NULL DEFAULT 0,
			workflow_run_id INTEGER NOT NULL DEFAULT 0,
			repository      TEXT NOT NULL DEFAULT '',
			status          INTEGER NOT NULL,
			status_changed  TEXT NOT NULL,
			provider_data   TEXT NOT NULL DEFAULT '{}'
		)`)
	if err != nil {
		return nil, err
	}
	return &SqliteTrayRepository{db: db}, nil
}

func (s *SqliteTrayRepository) GetById(ctx context.Context, trayId string) (*trays.Tray, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, tray_type_name, provider_name, github_org_name, github_runner_id,
				job_run_id, workflow_run_id, repository, status, status_changed, provider_data
		 FROM trays WHERE id = ?`, trayId)
	return scanTray(row)
}

func (s *SqliteTrayRepository) List(ctx context.Context) ([]*trays.Tray, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tray_type_name, provider_name, github_org_name, github_runner_id,
				job_run_id, workflow_run_id, repository, status, status_changed, provider_data
		 FROM trays ORDER BY status_changed DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrays(rows)
}

func (s *SqliteTrayRepository) Save(ctx context.Context, tray *trays.Tray) error {
	tray.StatusChanged = time.Now().UTC()
	providerData, err := json.Marshal(tray.ProviderData)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO trays (id, tray_type_name, provider_name, github_org_name, github_runner_id,
							job_run_id, workflow_run_id, repository, status, status_changed, provider_data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tray.Id, tray.TrayTypeName, tray.ProviderName, tray.GitHubOrgName, tray.GitHubRunnerId,
		tray.JobRunId, tray.WorkflowRunId, tray.Repository, int(tray.Status),
		tray.StatusChanged.Format(time.RFC3339Nano), string(providerData))
	return err
}

func (s *SqliteTrayRepository) Delete(ctx context.Context, trayId string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM trays WHERE id = ?`, trayId)
	return err
}

func (s *SqliteTrayRepository) UpdateStatus(ctx context.Context, trayId string, status trays.TrayStatus, jobRunId int64, workflowRunId int64, ghRunnerId int64, repository string) (*trays.Tray, error) {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	result, err := s.db.ExecContext(ctx,
		`UPDATE trays SET
			status = ?,
			status_changed = ?,
			job_run_id = CASE WHEN ? != 0 THEN ? ELSE job_run_id END,
			workflow_run_id = CASE WHEN ? != 0 THEN ? ELSE workflow_run_id END,
			github_runner_id = CASE WHEN ? != 0 THEN ? ELSE github_runner_id END,
			repository = CASE WHEN ? != '' THEN ? ELSE repository END
		 WHERE id = ?`,
		int(status), nowStr,
		jobRunId, jobRunId,
		workflowRunId, workflowRunId,
		ghRunnerId, ghRunnerId,
		repository, repository,
		trayId)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, nil
	}
	return s.GetById(ctx, trayId)
}

func (s *SqliteTrayRepository) CountActive(ctx context.Context, trayType string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM trays WHERE tray_type_name = ? AND status != ?`,
		trayType, int(trays.TrayStatusDeleting)).Scan(&count)
	return count, err
}

func (s *SqliteTrayRepository) GetStale(ctx context.Context, d time.Duration) ([]*trays.Tray, error) {
	cutoff := time.Now().UTC().Add(-d).Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tray_type_name, provider_name, github_org_name, github_runner_id,
				job_run_id, workflow_run_id, repository, status, status_changed, provider_data
		 FROM trays WHERE status != ? AND status_changed <= ?`,
		int(trays.TrayStatusRunning), cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrays(rows)
}

// scanTray reads a single tray from a *sql.Row.
func scanTray(row *sql.Row) (*trays.Tray, error) {
	var t trays.Tray
	var statusInt int
	var statusChanged string
	var providerDataJSON string

	err := row.Scan(&t.Id, &t.TrayTypeName, &t.ProviderName, &t.GitHubOrgName,
		&t.GitHubRunnerId, &t.JobRunId, &t.WorkflowRunId, &t.Repository,
		&statusInt, &statusChanged, &providerDataJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t.Status = trays.TrayStatus(statusInt)
	t.StatusChanged, _ = time.Parse(time.RFC3339Nano, statusChanged)
	_ = json.Unmarshal([]byte(providerDataJSON), &t.ProviderData)
	if t.ProviderData == nil {
		t.ProviderData = make(map[string]string)
	}
	return &t, nil
}

// scanTrays reads multiple trays from *sql.Rows.
func scanTrays(rows *sql.Rows) ([]*trays.Tray, error) {
	var result []*trays.Tray
	for rows.Next() {
		var t trays.Tray
		var statusInt int
		var statusChanged string
		var providerDataJSON string

		err := rows.Scan(&t.Id, &t.TrayTypeName, &t.ProviderName, &t.GitHubOrgName,
			&t.GitHubRunnerId, &t.JobRunId, &t.WorkflowRunId, &t.Repository,
			&statusInt, &statusChanged, &providerDataJSON)
		if err != nil {
			return nil, err
		}
		t.Status = trays.TrayStatus(statusInt)
		t.StatusChanged, _ = time.Parse(time.RFC3339Nano, statusChanged)
		_ = json.Unmarshal([]byte(providerDataJSON), &t.ProviderData)
		if t.ProviderData == nil {
			t.ProviderData = make(map[string]string)
		}
		result = append(result, &t)
	}
	return result, rows.Err()
}
