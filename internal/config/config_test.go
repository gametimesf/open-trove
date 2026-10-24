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
	if cfg.Uploads.MaxBytes != 200<<20 || cfg.Uploads.MaxSiteFiles != 2_000 {
		t.Errorf("unexpected upload defaults: %+v", cfg.Uploads)
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

func TestLoadShareURLRules(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
port: "8080"
base_url: "https://trove.example.com"
share_url_rules:
  - slug_prefix: partner-
    base_url: "https://proxy.example.com/"
    path_prefix: "/shared/trove/"
store:
  type: s3
`)

	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.ShareURLRules) != 1 {
		t.Fatalf("expected one share URL rule, got %d", len(cfg.ShareURLRules))
	}
	rule := cfg.ShareURLRules[0]
	if rule.BaseURL != "https://proxy.example.com" || rule.PathPrefix != "/shared/trove" {
		t.Errorf("expected normalized share URL rule, got %+v", rule)
	}
}

func TestLoadInlineYAMLAndEnvironmentOverrides(t *testing.T) {
	dir := t.TempDir()
	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	t.Setenv("TROVE_CONFIG_YAML", `
base_url: https://inline.example.com
store:
  type: s3
  s3:
    bucket: inline-bucket
`)
	t.Setenv("TROVE_PORT", "9091")
	t.Setenv("TROVE_BASE_URL", "https://env.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "9091" || cfg.BaseURL != "https://env.example.com" {
		t.Errorf("environment did not win: %+v", cfg)
	}
	if cfg.Store.S3.Bucket != "inline-bucket" {
		t.Errorf("inline YAML not loaded: %+v", cfg.Store)
	}
}

func TestLoadExplicitConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	writeYAML(t, dir, "custom.yaml", `
base_url: https://custom.example.com
store:
  type: s3
  s3:
    bucket: custom-bucket
`)
	t.Setenv("TROVE_CONFIG", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Store.S3.Bucket != "custom-bucket" {
		t.Errorf("explicit config not loaded: %+v", cfg.Store)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
base_url: https://trove.example.com
internal_only_knob: true
`)
	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	if _, err := Load(); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateRejectsInvalidShareURLRule(t *testing.T) {
	cfg := &Config{
		Port:    "8080",
		BaseURL: "https://trove.example.com",
		Uploads: Uploads{MaxBytes: 1, MaxSiteFiles: 1, MaxSiteBytes: 1, MaxSiteFileBytes: 1},
		ShareURLRules: []ShareURLRule{{
			SlugPrefix: "../partner",
			BaseURL:    "https://proxy.example.com",
		}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid share URL rule error")
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

func TestValidateAuth(t *testing.T) {
	cf := CloudflareAccess{TeamDomain: "team.example.com", Audience: "aud-tag"}
	tests := []struct {
		name          string
		auth          Auth
		wantErr       bool
		wantMode      string
		wantEnforceOn string
	}{
		{name: "empty defaults to off and writes", auth: Auth{}, wantMode: "off", wantEnforceOn: "writes"},
		{name: "shadow with cloudflare", auth: Auth{Mode: "shadow", CloudflareAccess: cf}, wantMode: "shadow", wantEnforceOn: "writes"},
		{name: "enforce all is not supported yet", auth: Auth{Mode: "enforce", EnforceOn: "all", CloudflareAccess: cf}, wantErr: true},
		{name: "enforce writes with cloudflare", auth: Auth{Mode: "enforce", CloudflareAccess: cf}, wantMode: "enforce", wantEnforceOn: "writes"},
		{name: "off may preconfigure cloudflare", auth: Auth{Mode: "off", CloudflareAccess: cf}, wantMode: "off", wantEnforceOn: "writes"},
		{name: "shadow without source", auth: Auth{Mode: "shadow"}, wantErr: true},
		{name: "enforce without source", auth: Auth{Mode: "enforce"}, wantErr: true},
		{name: "unknown mode", auth: Auth{Mode: "strict", CloudflareAccess: cf}, wantErr: true},
		{name: "unknown scope", auth: Auth{Mode: "enforce", EnforceOn: "reads", CloudflareAccess: cf}, wantErr: true},
		{name: "audience without team", auth: Auth{CloudflareAccess: CloudflareAccess{Audience: "aud-tag"}}, wantErr: true},
		{name: "team without audience", auth: Auth{CloudflareAccess: CloudflareAccess{TeamDomain: "team.example.com"}}, wantErr: true},
		{name: "team with scheme", auth: Auth{CloudflareAccess: CloudflareAccess{TeamDomain: "https://team.example.com", Audience: "aud-tag"}}, wantErr: true},
		{name: "tokens with shadow and cloudflare", auth: Auth{Mode: "shadow", CloudflareAccess: cf, Tokens: Tokens{Enabled: true}}, wantMode: "shadow", wantEnforceOn: "writes"},
		{name: "tokens require non-off mode", auth: Auth{Mode: "off", CloudflareAccess: cf, Tokens: Tokens{Enabled: true}}, wantErr: true},
		{name: "tokens require cloudflare", auth: Auth{Mode: "off", Tokens: Tokens{Enabled: true}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:    "8080",
				BaseURL: "https://trove.example.com",
				Uploads: Uploads{MaxBytes: 1, MaxSiteFiles: 1, MaxSiteBytes: 1, MaxSiteFileBytes: 1},
				Auth:    tt.auth,
			}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if cfg.Auth.Mode != tt.wantMode || cfg.Auth.EnforceOn != tt.wantEnforceOn {
				t.Errorf("auth = %+v, want mode %q enforce_on %q", cfg.Auth, tt.wantMode, tt.wantEnforceOn)
			}
		})
	}
}

func TestLoadAuthFromYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
auth:
  mode: shadow
  cloudflare_access:
    team_domain: team.example.com
    audience: aud-tag
`)
	origDir := chdir(t, dir)
	defer os.Chdir(origDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Auth{Mode: "shadow", EnforceOn: "writes", CloudflareAccess: CloudflareAccess{TeamDomain: "team.example.com", Audience: "aud-tag"}}
	if cfg.Auth != want {
		t.Errorf("auth = %+v, want %+v", cfg.Auth, want)
	}
}

func TestValidateSlack(t *testing.T) {
	const bot, app = "xoxb-test-placeholder", "xapp-test-placeholder"
	tests := []struct {
		name    string
		slack   Slack
		wantErr bool
	}{
		{name: "disabled needs nothing", slack: Slack{Notifications: true, Unfurls: true}},
		{name: "enabled with both tokens", slack: Slack{Enabled: true, Notifications: true, Unfurls: true, BotToken: bot, AppToken: app}},
		{name: "notifications only need the bot token", slack: Slack{Enabled: true, Notifications: true, BotToken: bot}},
		{name: "missing bot token", slack: Slack{Enabled: true, Notifications: true, Unfurls: true, AppToken: app}, wantErr: true},
		{name: "unfurls need the app token", slack: Slack{Enabled: true, Unfurls: true, BotToken: bot}, wantErr: true},
		{name: "tokens swapped", slack: Slack{Enabled: true, Notifications: true, Unfurls: true, BotToken: app, AppToken: bot}, wantErr: true},
		{name: "nothing turned on", slack: Slack{Enabled: true, BotToken: bot}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port:    "8080",
				BaseURL: "https://trove.example.com",
				Uploads: Uploads{MaxBytes: 1, MaxSiteFiles: 1, MaxSiteBytes: 1, MaxSiteFileBytes: 1},
				Slack:   tt.slack,
			}
			if err := cfg.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadSlackTokensFromEnvironmentOnly(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "trove.yaml", `
slack:
  enabled: true
  unfurls: false
`)
	origDir := chdir(t, dir)
	defer os.Chdir(origDir)
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test-placeholder")
	t.Setenv("SLACK_APP_TOKEN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Slack.Enabled || !cfg.Slack.Notifications || cfg.Slack.Unfurls || cfg.Slack.BotToken != "xoxb-test-placeholder" {
		t.Fatalf("slack = %+v", cfg.Slack)
	}

	writeYAML(t, dir, "trove.yaml", "slack:\n  bot_token: xoxb-in-yaml\n")
	if _, err := Load(); err == nil {
		t.Fatal("expected tokens in YAML to be rejected as unknown fields")
	}
}
