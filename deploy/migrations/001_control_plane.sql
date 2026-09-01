-- Code Relay SaaS control-plane schema (PostgreSQL 15+).
-- GitHub remains the source of truth for runbooks, workflow runs and receipts;
-- these tables hold tenant ownership, authorization bindings and audit facts.

CREATE TABLE IF NOT EXISTS tenants (
    id              text PRIMARY KEY,
    name            text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS members (
    tenant_id       text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subject         text NOT NULL,
    role            text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, subject)
);

CREATE TABLE IF NOT EXISTS projects (
    id              text PRIMARY KEY,
    tenant_id       text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    repository      text NOT NULL,
    ref             text NOT NULL,
    installation_id bigint NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, repository, ref)
);

CREATE TABLE IF NOT EXISTS runs (
    id              text PRIMARY KEY,
    project_id      text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    subject         text NOT NULL,
    runbook_id      text NOT NULL,
    commit_sha      text,
    workflow_run_id bigint,
    state           text NOT NULL CHECK (state IN ('queued', 'running', 'passed', 'failed', 'cancelled')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, runbook_id)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id              bigserial PRIMARY KEY,
    tenant_id       text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subject         text NOT NULL,
    action          text NOT NULL,
    project_id      text,
    run_id          text,
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS members_subject_idx ON members(subject);
CREATE INDEX IF NOT EXISTS projects_tenant_idx ON projects(tenant_id);
CREATE INDEX IF NOT EXISTS runs_project_updated_idx ON runs(project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS audit_tenant_created_idx ON audit_events(tenant_id, created_at DESC);
