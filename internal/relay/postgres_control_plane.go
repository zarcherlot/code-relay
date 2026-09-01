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

func (p *PostgresControlPlane) EnsureTenant(ctx context.Context, id, name string) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO tenants (id, name) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name`, id, name)
	return err
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
