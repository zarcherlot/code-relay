package relay

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresControlPlane owns durable SaaS metadata. Runbook contents, workflow
// state and receipts remain in GitHub; this repository stores tenant access,
// project bindings, run references and audit facts.
type PostgresControlPlane struct {
	db *sql.DB
}

// ControlPlane captures the durable operations required by the MCP request
// path. It keeps the HTTP adapter testable and allows a transactional service
// wrapper to replace the direct PostgreSQL implementation later.
type ControlPlane interface {
	EnsureTenant(context.Context, string, string) error
	UpsertProject(context.Context, string, string, string, string, int64) error
	AppendAudit(context.Context, string, string, string, string, string, []byte) error
}

// RunRecorder is an optional durable run lifecycle extension. It is kept
// separate from ControlPlane so test doubles and alternative control planes
// can adopt run persistence independently.
type RunRecorder interface {
	UpsertRun(context.Context, string, string, string, string, string, string, string) error
}

func NewPostgresControlPlane(ctx context.Context, dsn string) (*PostgresControlPlane, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &PostgresControlPlane{db: db}, nil
}

func (p *PostgresControlPlane) Close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

func (p *PostgresControlPlane) Ping(ctx context.Context) error {
	if p == nil || p.db == nil {
		return errors.New("postgres control plane is not configured")
	}
	return p.db.PingContext(ctx)
}

func (p *PostgresControlPlane) EnsureTenant(ctx context.Context, id, name string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tenants (id, name) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name`, id, name); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO members (tenant_id, subject, role) VALUES ($1, $1, 'owner') ON CONFLICT (tenant_id, subject) DO NOTHING`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (p *PostgresControlPlane) UpsertProject(ctx context.Context, id, tenantID, repository, ref string, installationID int64) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO projects (id, tenant_id, repository, ref, installation_id) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (tenant_id, repository, ref) DO UPDATE SET installation_id = EXCLUDED.installation_id, updated_at = now()`, id, tenantID, repository, ref, installationID)
	return err
}

func (p *PostgresControlPlane) AppendAudit(ctx context.Context, tenantID, subject, action, projectID, runID string, metadata []byte) error {
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}
	_, err := p.db.ExecContext(ctx, `INSERT INTO audit_events (tenant_id, subject, action, project_id, run_id, metadata) VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6::jsonb)`, tenantID, subject, action, projectID, runID, string(metadata))
	return err
}

func (p *PostgresControlPlane) UpsertRun(ctx context.Context, id, projectID, subject, runbookID, commitSHA, workflowRunID, state string) error {
	if strings.TrimSpace(state) == "" {
		state = "queued"
	}
	_, err := p.db.ExecContext(ctx, `INSERT INTO runs (id, project_id, subject, runbook_id, commit_sha, workflow_run_id, state) VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, '')::bigint, $7) ON CONFLICT (project_id, runbook_id) DO UPDATE SET commit_sha = EXCLUDED.commit_sha, workflow_run_id = EXCLUDED.workflow_run_id, state = EXCLUDED.state, updated_at = now()`, id, projectID, subject, runbookID, commitSHA, workflowRunID, state)
	return err
}
