package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const currentVersion = 1

const (
	DefaultMessageTTL = 7 * 24 * time.Hour
	MaximumMessageTTL = 30 * 24 * time.Hour
	MinimumMessageTTL = time.Hour
)

type Config struct {
	Version    int              `json:"version"`
	Database   string           `json:"database"`
	MessageTTL string           `json:"message_ttl"`
	Agents     map[string]Agent `json:"agents"`
}

type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func DefaultPath() (string, error) {
	if dir := os.Getenv("TRAILWIRE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, "trailwire", "config.json"), nil
}

func DefaultDatabasePath() (string, error) {
	if path := os.Getenv("TRAILWIRE_DATABASE"); path != "" {
		return filepath.Clean(path), nil
	}
	if dir := os.Getenv("TRAILWIRE_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "trailwire.db"), nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" && runtime.GOOS != "windows" {
		return filepath.Join(dir, "trailwire", "trailwire.db"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user data directory: %w", err)
	}
	return filepath.Join(dir, "trailwire", "trailwire.db"), nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		database, pathErr := DefaultDatabasePath()
		if pathErr != nil {
			return nil, pathErr
		}
		return &Config{Version: currentVersion, Database: database, MessageTTL: DefaultMessageTTL.String(), Agents: map[string]Agent{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Version != currentVersion {
		return nil, fmt.Errorf("unsupported config version %d", cfg.Version)
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]Agent{}
	}
	if cfg.MessageTTL == "" {
		cfg.MessageTTL = DefaultMessageTTL.String()
	}
	if _, err := cfg.MessageTTLDuration(); err != nil {
		return nil, err
	}
	if override := os.Getenv("TRAILWIRE_DATABASE"); override != "" {
		cfg.Database = filepath.Clean(override)
	}
	return &cfg, nil
}

func (c *Config) EnsureAgent(harness, name string) (Agent, bool, error) {
	harness = strings.ToLower(strings.TrimSpace(harness))
	if harness == "" {
		return Agent{}, false, errors.New("harness is required")
	}
	if agent, ok := c.Agents[harness]; ok {
		return agent, false, nil
	}
	if strings.TrimSpace(name) == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "local"
		}
		name = harness + "@" + hostname
	}
	id, err := newID()
	if err != nil {
		return Agent{}, false, err
	}
	agent := Agent{ID: id, Name: name}
	c.Agents[harness] = agent
	return agent, true, nil
}

func (c *Config) MessageTTLDuration() (time.Duration, error) {
	ttl, err := time.ParseDuration(c.MessageTTL)
	if err != nil {
		return 0, fmt.Errorf("parse message ttl: %w", err)
	}
	if ttl < MinimumMessageTTL {
		return 0, fmt.Errorf("message ttl must be at least %s", MinimumMessageTTL)
	}
	if ttl > MaximumMessageTTL {
		return 0, fmt.Errorf("message ttl cannot exceed %s", MaximumMessageTTL)
	}
	return ttl, nil
}

func (c *Config) SetMessageTTL(ttl time.Duration) error {
	if ttl < MinimumMessageTTL {
		return fmt.Errorf("message ttl must be at least %s", MinimumMessageTTL)
	}
	if ttl > MaximumMessageTTL {
		return fmt.Errorf("message ttl cannot exceed %s", MaximumMessageTTL)
	}
	c.MessageTTL = ttl.String()
	return nil
}

func Save(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	if cfg.Version == 0 {
		cfg.Version = currentVersion
	}
	if cfg.Database == "" {
		database, err := DefaultDatabasePath()
		if err != nil {
			return err
		}
		cfg.Database = database
	}
	if cfg.MessageTTL == "" {
		cfg.MessageTTL = DefaultMessageTTL.String()
	}
	if _, err := cfg.MessageTTLDuration(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate agent id: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
