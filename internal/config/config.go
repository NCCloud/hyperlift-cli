// Package config stores the non-secret CLI configuration as YAML in
// ~/.config/hyperlift/config.yml, and resolves the precedence when the
// environment supplies a setting too.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// DefaultBaseURL applies when neither the environment nor the config supplies a
// base URL.
const DefaultBaseURL = "https://spaceship.dev/api/v1"

// Environment variable names that override a config value.
const (
	EnvBaseURL = "HYPERLIFT_BASE_URL"
	EnvAPIKey  = "HYPERLIFT_API_KEY" //nolint:gosec // env-var name, not a credential
	// EnvNoUpdateCheck turns off the background "new version available" check.
	// Any non-empty value works.
	EnvNoUpdateCheck = "HYPERLIFT_NO_UPDATE_CHECK"
)

// Config is the stored document of the single account. It never holds the API
// secret, which lives in the OS keyring (internal/keyring). Read a resolved
// value through the BaseURL, APIKey or DefaultOutput method, which apply the
// environment overrides. The exported fields hold the raw on-disk values.
type Config struct {
	StoredBaseURL       string `yaml:"base_url,omitempty"`
	StoredAPIKey        string `yaml:"api_key,omitempty"`
	StoredDefaultOutput string `yaml:"default_output,omitempty"`

	// path is the file this config came from, and the file Save writes to.
	// Unexported, so YAML never marshals it.
	path string
}

// Path returns the config file location. It honors XDG_CONFIG_HOME.
func Path() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "hyperlift", "config.yml"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}

	return filepath.Join(home, ".config", "hyperlift", "config.yml"), nil
}

// Dir returns the directory that holds the config file.
func Dir() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}

	return filepath.Dir(p), nil
}

// Load reads the config from disk. A missing file gives an empty, usable Config
// and no error, so the first run needs no setup.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}

	cfg := &Config{path: p}

	data, err := os.ReadFile(p) //nolint:gosec // p is the CLI's own config path (XDG/home-derived)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}

		return nil, fmt.Errorf("read config %s: %w", p, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", p, err)
	}

	cfg.path = p

	return cfg, nil
}

// Save writes the config to disk atomically, with 0600 permissions.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}

		c.path = p
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(c) //nolint:gosec // the config holds the API key by design, never the secret
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// CreateTemp gives each writer its own 0600 temp file, so two concurrent
	// saves cannot interleave on one temp path. The rename keeps the replace
	// atomic.
	tmp, err := os.CreateTemp(filepath.Dir(c.path), filepath.Base(c.path)+".tmp-")
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write config: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write config: %w", err)
	}

	if err := os.Rename(tmp.Name(), c.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("replace config: %w", err)
	}

	return nil
}

// BaseURL resolves the API base URL. The order is the HYPERLIFT_BASE_URL
// environment variable, then the config, then DefaultBaseURL.
func (c *Config) BaseURL() string {
	if v := os.Getenv(EnvBaseURL); v != "" {
		return v
	}

	if c.StoredBaseURL != "" {
		return c.StoredBaseURL
	}

	return DefaultBaseURL
}

// APIKey resolves the API key. The HYPERLIFT_API_KEY environment variable wins
// over the config.
func (c *Config) APIKey() string {
	if v := os.Getenv(EnvAPIKey); v != "" {
		return v
	}

	return c.StoredAPIKey
}

// DefaultOutput returns the configured default output format. An empty result
// means the caller must fall back to "table".
func (c *Config) DefaultOutput() string {
	return c.StoredDefaultOutput
}
