package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvSecretPrefersEnvironment(t *testing.T) {
	t.Setenv("TEST_SECRET_VALUE", " from-env ")
	t.Setenv("TEST_SECRET_FILE", filepath.Join(t.TempDir(), "missing"))

	value, err := envSecret("TEST_SECRET_VALUE", "TEST_SECRET_FILE")
	if err != nil || value != "from-env" {
		t.Fatalf("env secret = %q, %v", value, err)
	}
}

func TestEnvSecretReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(" file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SECRET_VALUE", "")
	t.Setenv("TEST_SECRET_FILE", path)

	value, err := envSecret("TEST_SECRET_VALUE", "TEST_SECRET_FILE")
	if err != nil || value != "file-secret" {
		t.Fatalf("file secret = %q, %v", value, err)
	}
}

func TestEnvSecretMissingIsEmpty(t *testing.T) {
	t.Setenv("TEST_SECRET_VALUE", "")
	t.Setenv("TEST_SECRET_FILE", "")

	value, err := envSecret("TEST_SECRET_VALUE", "TEST_SECRET_FILE")
	if err != nil || value != "" {
		t.Fatalf("missing secret = %q, %v", value, err)
	}
}
