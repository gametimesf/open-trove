package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
port: "9090"
base_url: "https://trove.example.com"
store:
  type: s3
  s3:
    bucket: my-bucket
    endpoint: "http://localhost:9000"
    region: us-west-2
`)

	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("expected port 9090, got %q", cfg.Port)
	}
	if cfg.BaseURL != "https://trove.example.com" {
		t.Errorf("expected base_url, got %q", cfg.BaseURL)
	}
	if cfg.Store.Type != "s3" {
		t.Errorf("expected store type s3, got %q", cfg.Store.Type)
	}
	if cfg.Store.S3.Bucket != "my-bucket" {
		t.Errorf("expected bucket my-bucket, got %q", cfg.Store.S3.Bucket)
	}
	if cfg.Store.S3.Endpoint != "http://localhost:9000" {
		t.Errorf("expected endpoint, got %q", cfg.Store.S3.Endpoint)
	}
	if cfg.Store.S3.Region != "us-west-2" {
		t.Errorf("expected region us-west-2, got %q", cfg.Store.S3.Region)
	}
}

func TestLoadEnvSpecificYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
port: "8080"
store:
  type: s3
  s3:
    bucket: default-bucket
`)
	writeYAML(t, dir, "trove-staging.yaml", `
port: "8080"
base_url: "https://trove.staging.example.com"
store:
  type: s3
  s3:
    bucket: staging-bucket
    region: us-west-2
`)

	origDir := chdir(t, dir)
	defer os.Chdir(origDir)
	t.Setenv("ENVIRONMENT", "staging")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Store.S3.Bucket != "staging-bucket" {
		t.Errorf("expected staging-bucket, got %q", cfg.Store.S3.Bucket)
	}
	if cfg.BaseURL != "https://trove.staging.example.com" {
		t.Errorf("expected staging base_url, got %q", cfg.BaseURL)
	}
}

func TestLoadFallsBackToDefaultYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
port: "8080"
store:
  type: s3
  s3:
    bucket: default-bucket
`)

	origDir := chdir(t, dir)
	defer os.Chdir(origDir)
	t.Setenv("ENVIRONMENT", "nonexistent")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Store.S3.Bucket != "default-bucket" {
		t.Errorf("expected default-bucket fallback, got %q", cfg.Store.S3.Bucket)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("expected default port 8080, got %q", cfg.Port)
	}
	if cfg.Store.Type != "s3" {
		t.Errorf("expected default store type s3, got %q", cfg.Store.Type)
	}
}

func TestLoadBaseURLDefault(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
port: "3000"
store:
  type: s3
`)

	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BaseURL != "http://localhost:3000" {
		t.Errorf("expected default base_url with port, got %q", cfg.BaseURL)
	}
}

func TestLoadBadYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", "{{not yaml}}")

	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	_, err := Load()
	if err == nil {
		t.Error("expected error for invalid yaml")
	}
}

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func chdir(t *testing.T, dir string) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return orig
}
