// Package config loads trove application configuration from YAML files.
// It is a leaf package — no domain imports.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	Port    string `yaml:"port"`
	BaseURL string `yaml:"base_url"`
	Store   Store  `yaml:"store"`
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
	}

	path := configPath()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = fmt.Sprintf("http://localhost:%s", cfg.Port)
	}

	return cfg, nil
}

// configPath returns the path to the config file to load.
func configPath() string {
	if env := os.Getenv("ENVIRONMENT"); env != "" {
		envPath := fmt.Sprintf("trove-%s.yaml", env)
		if _, err := os.Stat(envPath); err == nil {
			return envPath
		}
	}
	if _, err := os.Stat("trove.yaml"); err == nil {
		return "trove.yaml"
	}
	return ""
}
