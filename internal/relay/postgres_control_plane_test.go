package relay

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresControlPlaneIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CODE_RELAY_DATABASE_URL"))
	if dsn == "" {
		t.Skip("CODE_RELAY_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	controlPlane, err := NewPostgresControlPlane(ctx, dsn)
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(func() { _ = controlPlane.Close() })
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	tenantID := "test-" + strings.ReplaceAll(suffix, ".", "-")
	projectID := tenantID + "-project"
	if err := controlPlane.EnsureTenant(ctx, tenantID, "integration tenant"); err != nil {
		t.Fatal(err)
	}
	if err := controlPlane.UpsertProject(ctx, projectID, tenantID, "acme/demo", "refs/heads/main", 7); err != nil {
		t.Fatal(err)
	}
	if err := controlPlane.UpsertRun(ctx, projectID+"-run", projectID, "17", "runbook-001", "abc123", "", "queued"); err != nil {
		t.Fatal(err)
	}
	if err := controlPlane.AppendAudit(ctx, tenantID, "17", "integration.test", projectID, "", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := controlPlane.db.ExecContext(ctx, `DELETE FROM tenants WHERE id = $1`, tenantID); err != nil {
		t.Fatal(err)
	}
}
