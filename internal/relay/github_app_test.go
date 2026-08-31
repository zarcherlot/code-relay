package relay

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestGitHubAppRepositoryAPI(t *testing.T) {
	var putBody string
	var dispatchBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing authorization")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user/installations/42/repositories":
			_, _ = w.Write([]byte(`{"repositories":[{"full_name":"acme/demo"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens":
			_, _ = w.Write([]byte(`{"token":"installation-token","expires_at":"2030-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/contents/tasks/demo/task.md"):
			_, _ = w.Write([]byte(`{"type":"file","encoding":"base64","content":"aGVsbG8=","sha":"sha-old"}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/contents/tasks/demo/task.md"):
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			putBody = string(buf)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/actions/workflows/"):
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			dispatchBody = string(buf)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/git/ref/heads/"):
			_, _ = w.Write([]byte(`{"ref":"refs/heads/main"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app, err := NewGitHubAppClient(GitHubAppConfig{AppID: 123, PrivateKeyPEM: testPrivateKeyPEM(t), APIBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.UserCanAccessRepository(t.Context(), "user-token", 42, "acme/demo"); err != nil {
		t.Fatal(err)
	}
	repo, err := app.Repository(t.Context(), 42, "acme/demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.PutContent(t.Context(), "acme/demo", "tasks/demo/task.md", "refs/heads/main", "publish", []byte("content"), "sha-old"); err != nil {
		t.Fatal(err)
	}
	if err := repo.DispatchWorkflow(t.Context(), "acme/demo", "verify-on-b.yml", "refs/heads/main", map[string]string{"task_id": "demo"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(putBody, "sha-old") || !strings.Contains(putBody, "content") {
		t.Fatalf("unexpected put body: %s", putBody)
	}
	if !strings.Contains(dispatchBody, `"task_id":"demo"`) {
		t.Fatalf("unexpected dispatch body: %s", dispatchBody)
	}
	if _, err := normalizeRepository("acme/demo/extra"); err == nil {
		t.Fatal("expected repository path rejection")
	}
}
