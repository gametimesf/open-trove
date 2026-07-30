// Package config loads trove application configuration from YAML files.
// It is a leaf package — no domain imports.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	Port          string         `yaml:"port"`
	BaseURL       string         `yaml:"base_url"`
	Store         Store          `yaml:"store"`
	Intake        Intake         `yaml:"intake"`
	Uploads       Uploads        `yaml:"uploads"`
	ShareURLRules []ShareURLRule `yaml:"share_url_rules"`
	ContentReview ContentReview  `yaml:"content_review"`
}

// Uploads bounds request and ZIP expansion costs. All byte values are literal
// bytes, not compressed sizes.
type Uploads struct {
	MaxBytes         int64 `yaml:"max_bytes"`
	MaxSiteFiles     int   `yaml:"max_site_files"`
	MaxSiteBytes     int64 `yaml:"max_site_bytes"`
	MaxSiteFileBytes int64 `yaml:"max_site_file_bytes"`
}

// ShareURLRule lets a deployment publish selected slugs through an alternate
// URL without adding deployment-specific routing policy to the application.
type ShareURLRule struct {
	SlugPrefix string `yaml:"slug_prefix"`
	BaseURL    string `yaml:"base_url"`
	PathPrefix string `yaml:"path_prefix"`
}

// ContentReview controls the contact shown when an optional intake provider
// flags an upload.
type ContentReview struct {
	ContactName  string `yaml:"contact_name"`
	ContactEmail string `yaml:"contact_email"`
}

// Intake configures the upload intake pipeline. When Enabled is false,
// the inspector is a no-op and uploads pass straight through.
type Intake struct {
	Enabled      bool   `yaml:"enabled"`
	Provider     string `yaml:"provider"`        // "anthropic"
	Model        string `yaml:"model"`           // e.g. "claude-sonnet-4-6"
	APIKey       string `yaml:"api_key"`         // env-var fallback: ANTHROPIC_API_KEY
	PromptInline string `yaml:"prompt_inline"`   // operator-supplied guidance string
	PromptPath   string `yaml:"prompt_path"`     // operator-supplied guidance file
	FailMode     string `yaml:"fail_mode"`       // "closed" (default) or "open"
	MaxBytes     int    `yaml:"max_check_bytes"` // default 200KB
	TimeoutMS    int    `yaml:"timeout_ms"`      // default 15000
	Endpoint     string `yaml:"endpoint"`        // override; tests / proxies

	// PromptSourceBucket and PromptSourceKey point at the guidance blob
	// in S3. Confidentiality is provided by SSE-KMS on the bucket plus
	// IAM scoping. Refresh polls the source on this interval so updates
	// propagate without restart.
	PromptSourceBucket string `yaml:"prompt_source_bucket"`
	PromptSourceKey    string `yaml:"prompt_source_key"`
	PromptRefresh      string `yaml:"prompt_refresh"` // duration string, e.g. "10m"
}

// Store configures which storage backend to use.
type Store struct {
	Type string   `yaml:"type"`
	S3   S3Config `yaml:"s3"`
}

// S3Config holds S3-specific storage configuration.
type S3Config struct {
	Bucket   string `yaml:"bucket"`
	Endpoint string `yaml:"endpoint,omitempty"`
	Region   string `yaml:"region,omitempty"`
}

// Load reads configuration from a YAML file.
// Resolution order:
//  1. trove-{ENVIRONMENT}.yaml (if ENVIRONMENT env var is set and file exists)
//  2. trove.yaml (if it exists)
func Load() (*Config, error) {
	cfg := &Config{
		Port: "8080",
		Store: Store{
			Type: "s3",
		},
		Uploads: Uploads{
			MaxBytes:         200 << 20,
			MaxSiteFiles:     2_000,
			MaxSiteBytes:     200 << 20,
			MaxSiteFileBytes: 100 << 20,
		},
		ContentReview: ContentReview{
			ContactName: "the site administrator",
		},
	}

	configFile, err := configPath()
	if err != nil {
		return nil, err
	}

	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := decodeStrict(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	if inline := os.Getenv("TROVE_CONFIG_YAML"); inline != "" {
		if err := decodeStrict([]byte(inline), cfg); err != nil {
			return nil, fmt.Errorf("parsing TROVE_CONFIG_YAML: %w", err)
		}
	}

	if value := strings.TrimSpace(os.Getenv("TROVE_PORT")); value != "" {
		cfg.Port = value
	}
	if value := strings.TrimSpace(os.Getenv("TROVE_BASE_URL")); value != "" {
		cfg.BaseURL = value
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = fmt.Sprintf("http://localhost:%s", cfg.Port)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// configPath returns the path to the config file to load.
func configPath() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("TROVE_CONFIG")); explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("TROVE_CONFIG %q: %w", explicit, err)
		}
		return explicit, nil
	}
	if env := os.Getenv("ENVIRONMENT"); env != "" {
		envPath := fmt.Sprintf("trove-%s.yaml", env)
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}
	if _, err := os.Stat("trove.yaml"); err == nil {
		return "trove.yaml", nil
	}
	return "", nil
}

func decodeStrict(data []byte, cfg *Config) error {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	return decoder.Decode(cfg)
}

// Validate rejects configuration that would produce ambiguous or unsafe URLs
// and non-positive resource limits.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Port) == "" {
		return fmt.Errorf("config: port is required")
	}
	if err := validateHTTPURL("base_url", c.BaseURL); err != nil {
		return err
	}
	if c.Uploads.MaxBytes <= 0 || c.Uploads.MaxSiteFiles <= 0 ||
		c.Uploads.MaxSiteBytes <= 0 || c.Uploads.MaxSiteFileBytes <= 0 {
		return fmt.Errorf("config: upload limits must be positive")
	}
	if c.Uploads.MaxSiteFileBytes > c.Uploads.MaxSiteBytes {
		return fmt.Errorf("config: uploads.max_site_file_bytes cannot exceed uploads.max_site_bytes")
	}
	for i := range c.ShareURLRules {
		rule := &c.ShareURLRules[i]
		rule.SlugPrefix = strings.TrimSpace(rule.SlugPrefix)
		rule.BaseURL = strings.TrimRight(strings.TrimSpace(rule.BaseURL), "/")
		if rule.SlugPrefix == "" {
			return fmt.Errorf("config: share_url_rules[%d].slug_prefix is required", i)
		}
		if strings.ContainsAny(rule.SlugPrefix, "/\\") {
			return fmt.Errorf("config: share_url_rules[%d].slug_prefix cannot contain a slash", i)
		}
		if err := validateHTTPURL(fmt.Sprintf("share_url_rules[%d].base_url", i), rule.BaseURL); err != nil {
			return err
		}
		if rule.PathPrefix != "" {
			if !strings.HasPrefix(rule.PathPrefix, "/") {
				return fmt.Errorf("config: share_url_rules[%d].path_prefix must start with /", i)
			}
			rule.PathPrefix = strings.TrimSuffix(path.Clean(rule.PathPrefix), "/")
		}
	}
	return nil
}

func validateHTTPURL(field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("config: %s must be an absolute http(s) URL", field)
	}
	return nil
}
