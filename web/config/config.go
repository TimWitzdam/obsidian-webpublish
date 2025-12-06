package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime options for the publishing service.
type Config struct {
	ListenAddr string          `yaml:"listenAddr"`
	BaseURL    string          `yaml:"baseURL"`
	StorageDir string          `yaml:"storageDir"`
	APIKeys    []string        `yaml:"apiKeys"`
	RateLimit  RateLimitConfig `yaml:"rateLimit"`
	Upload     UploadConfig    `yaml:"upload"`
}

type RateLimitConfig struct {
	RequestsPerMinute int `yaml:"requestsPerMinute"`
	Burst             int `yaml:"burst"`
}

type UploadConfig struct {
	MaxBytes int64 `yaml:"maxBytes"`
}

// Load reads the YAML file at the provided path and returns a normalized Config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults(filepath.Dir(path))
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate ensures the config has sensible values.
func (c *Config) Validate() error {
	if len(c.APIKeys) == 0 {
		return errors.New("config: at least one api key is required")
	}

	cleanedKeys := make([]string, 0, len(c.APIKeys))
	seen := make(map[string]struct{})
	for _, key := range c.APIKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		cleanedKeys = append(cleanedKeys, trimmed)
	}
	if len(cleanedKeys) == 0 {
		return errors.New("config: api key list cannot be empty")
	}
	c.APIKeys = cleanedKeys

	if c.ListenAddr == "" {
		return errors.New("config: listenAddr cannot be empty")
	}
	if c.StorageDir == "" {
		return errors.New("config: storageDir cannot be empty")
	}
	if c.BaseURL == "" {
		return errors.New("config: baseURL cannot be empty")
	}
	if err := c.RateLimit.Validate(); err != nil {
		return err
	}
	if err := c.Upload.Validate(); err != nil {
		return err
	}
	return nil
}

func (c *Config) applyDefaults(configDir string) {
	if strings.TrimSpace(c.ListenAddr) == "" {
		c.ListenAddr = ":8080"
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		c.BaseURL = "http://localhost:8080"
	}
	if strings.TrimSpace(c.StorageDir) == "" {
		c.StorageDir = "data"
	}
	c.RateLimit.applyDefaults()
	c.Upload.applyDefaults()

	if configDir != "" && !filepath.IsAbs(c.StorageDir) {
		c.StorageDir = filepath.Join(configDir, c.StorageDir)
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
}

func (r *RateLimitConfig) applyDefaults() {
	if r.RequestsPerMinute == 0 {
		r.RequestsPerMinute = 120
	}
	if r.Burst == 0 {
		r.Burst = 60
	}
}

func (r *RateLimitConfig) Validate() error {
	if r.RequestsPerMinute < 0 {
		return errors.New("config: rateLimit.requestsPerMinute cannot be negative")
	}
	if r.Burst < 0 {
		return errors.New("config: rateLimit.burst cannot be negative")
	}
	return nil
}

func (u *UploadConfig) applyDefaults() {
	if u.MaxBytes == 0 {
		u.MaxBytes = 5 * 1024 * 1024 // 5 MiB default
	}
}

func (u *UploadConfig) Validate() error {
	if u.MaxBytes < 0 {
		return errors.New("config: upload.maxBytes cannot be negative")
	}
	return nil
}
