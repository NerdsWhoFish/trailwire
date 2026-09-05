package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/theoutdoorprogrammer/trailwire/internal/config"
	"github.com/theoutdoorprogrammer/trailwire/internal/repository"
	"github.com/theoutdoorprogrammer/trailwire/internal/store"
)

type Session struct {
	ConfigPath      string
	Config          *config.Config
	Agent           config.Agent
	LegacyAgent     config.Agent
	Harness         string
	NativeSessionID string
	Repository      *repository.Info
	Store           *store.Store
	Workspace       string
}

type Options struct {
	ConfigPath      string
	Harness         string
	NativeSessionID string
	CWD             string
}

func Open(ctx context.Context, options Options) (*Session, error) {
	harness := strings.ToLower(strings.TrimSpace(options.Harness))
	if harness == "" {
		harness = "human"
	}
	configPath := options.ConfigPath
	if configPath == "" {
		var err error
		configPath, err = config.DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	agent, created, err := cfg.EnsureAgent(harness, "")
	if err != nil {
		return nil, err
	}
	if created || cfg.NeedsSave() {
		if err := config.Save(configPath, cfg); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Database), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	database, err := store.Open(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
	if err := database.RegisterAgent(ctx, store.Agent{ID: agent.ID, Harness: harness, Name: agent.Name}); err != nil {
		database.Close()
		return nil, err
	}
	if err := database.SyncForcedChannels(ctx, cfg.ForcedChannels); err != nil {
		database.Close()
		return nil, err
	}
	ttl, err := cfg.MessageTTLDuration()
	if err != nil {
		database.Close()
		return nil, err
	}
	if _, err := database.Cleanup(ctx, time.Now().Add(-ttl)); err != nil {
		database.Close()
		return nil, err
	}

	nativeSessionID := strings.TrimSpace(options.NativeSessionID)
	result := &Session{
		ConfigPath: configPath, Config: cfg, LegacyAgent: agent, Harness: harness,
		NativeSessionID: nativeSessionID, Store: database,
	}
	cwd := options.CWD
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			result.Close()
			return nil, fmt.Errorf("find current directory: %w", err)
		}
	}
	repo, repoErr := repository.Discover(cwd)
	if repoErr != nil {
		result.Close()
		return nil, repoErr
	}
	result.Repository = &repo
	result.Workspace = workspaceName(cwd)
	if harness == "human" {
		result.Agent = agent
	} else if nativeSessionID != "" {
		bound, err := result.AgentFor(ctx, nativeSessionID)
		if err != nil {
			result.Close()
			return nil, err
		}
		result.Agent = bound
	}
	return result, nil
}

func (s *Session) Close() error {
	return s.Store.Close()
}

func (s *Session) Touch(ctx context.Context, nativeSessionID string) error {
	agent, err := s.AgentFor(ctx, nativeSessionID)
	if err != nil {
		return err
	}
	s.Agent = agent
	return s.TouchAgent(ctx, agent, nativeSessionID)
}

func (s *Session) TouchCurrent(ctx context.Context) error {
	return s.Touch(ctx, s.NativeSessionID)
}

func (s *Session) AgentFor(ctx context.Context, nativeSessionID string) (config.Agent, error) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if s.Harness == "human" {
		return s.LegacyAgent, nil
	}
	if nativeSessionID == "" {
		return config.Agent{}, errors.New("native session id is required")
	}
	candidate, err := s.Config.SessionAgent(s.Harness, nativeSessionID, s.Workspace)
	if err != nil {
		return config.Agent{}, err
	}
	bound, err := s.Store.BindSession(ctx, s.Harness, nativeSessionID, store.Agent{
		ID: candidate.ID, Harness: s.Harness, Name: candidate.Name,
	}, s.LegacyAgent.ID)
	if err != nil {
		return config.Agent{}, err
	}
	return config.Agent{ID: bound.ID, Name: bound.Name}, nil
}

func EnvironmentSessionID(harness string) string {
	keys := []string{"TRAILWIRE_SESSION_ID"}
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "claude":
		keys = append(keys, "CLAUDE_CODE_SESSION_ID")
	case "codex":
		keys = append(keys, "CODEX_THREAD_ID", "CODEX_SESSION_ID")
	case "cursor":
		keys = append(keys, "CURSOR_CONVERSATION_ID", "CURSOR_SESSION_ID")
	}
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func workspaceName(cwd string) string {
	name := strings.TrimSpace(filepath.Base(filepath.Clean(cwd)))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "workspace"
	}
	return name
}

func (s *Session) TouchAgent(ctx context.Context, agent config.Agent, nativeSessionID string) error {
	if s.Repository == nil {
		return nil
	}
	if strings.TrimSpace(nativeSessionID) == "" {
		return errors.New("native session id is required")
	}
	return s.Store.TouchPresence(ctx, agent.ID, nativeSessionID, s.Repository.ID)
}
