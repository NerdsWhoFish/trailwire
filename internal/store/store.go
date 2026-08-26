package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const activeWindow = 24 * time.Hour

type Store struct {
	db  *sql.DB
	now func() time.Time
}

type Agent struct {
	ID       string
	Harness  string
	Name     string
	LastSeen time.Time
}

type Message struct {
	ID         int64
	SenderID   string
	SenderName string
	TargetKind string
	TargetID   string
	Body       string
	CreatedAt  time.Time
}

type Intent struct {
	AgentID   string
	AgentName string
	RepoID    string
	Summary   string
	Paths     []string
	ExpiresAt time.Time
	UpdatedAt time.Time
}

type SendRequest struct {
	SenderID   string
	TargetKind string
	TargetID   string
	Body       string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}

	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  harness TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS presence (
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  repo_id TEXT NOT NULL,
  last_seen_at INTEGER NOT NULL,
  PRIMARY KEY (agent_id, session_id)
);
CREATE INDEX IF NOT EXISTS presence_repo_seen ON presence(repo_id, last_seen_at);

CREATE TABLE IF NOT EXISTS channels (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS channel_members (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  joined_at INTEGER NOT NULL,
  PRIMARY KEY (channel_id, agent_id)
);

CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  sender_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('repo', 'channel', 'agent')),
  target_id TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_created ON messages(created_at);

CREATE TABLE IF NOT EXISTS inbox (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  claimed_at INTEGER,
  PRIMARY KEY (message_id, agent_id)
);
CREATE INDEX IF NOT EXISTS inbox_unclaimed ON inbox(agent_id, claimed_at, message_id);

CREATE TABLE IF NOT EXISTS intents (
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  repo_id TEXT NOT NULL,
  summary TEXT NOT NULL,
  paths_json TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (agent_id, repo_id)
);
CREATE INDEX IF NOT EXISTS intents_repo_expiry ON intents(repo_id, expires_at);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

func (s *Store) RegisterAgent(ctx context.Context, agent Agent) error {
	agent.ID = strings.TrimSpace(agent.ID)
	agent.Harness = strings.TrimSpace(agent.Harness)
	agent.Name = strings.TrimSpace(agent.Name)
	if agent.ID == "" || agent.Harness == "" || agent.Name == "" {
		return errors.New("agent id, harness, and name are required")
	}

	now := s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agents (id, harness, name, created_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  harness = excluded.harness,
  name = excluded.name,
  last_seen_at = excluded.last_seen_at`, agent.ID, agent.Harness, agent.Name, now, now)
	if err != nil {
		return fmt.Errorf("register agent: %w", err)
	}
	return nil
}

func (s *Store) TouchPresence(ctx context.Context, agentID, sessionID, repoID string) error {
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(repoID) == "" {
		return errors.New("agent id, session id, and repository id are required")
	}

	now := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin presence update: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE agents SET last_seen_at = ? WHERE id = ?`, now, agentID); err != nil {
		return fmt.Errorf("touch agent: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO presence (agent_id, session_id, repo_id, last_seen_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(agent_id, session_id) DO UPDATE SET
  repo_id = excluded.repo_id,
  last_seen_at = excluded.last_seen_at`, agentID, sessionID, repoID, now)
	if err != nil {
		return fmt.Errorf("touch presence: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("touch presence: agent %q is not registered", agentID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit presence update: %w", err)
	}
	return nil
}

func (s *Store) EndSession(ctx context.Context, agentID, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM presence WHERE agent_id = ? AND session_id = ?`, agentID, sessionID)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	return nil
}

func (s *Store) CreateChannel(ctx context.Context, name string) error {
	name = normalizeChannel(name)
	if name == "" {
		return errors.New("channel name is required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO channels (name, created_at) VALUES (?, ?) ON CONFLICT(name) DO NOTHING`, name, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("create channel: %w", err)
	}
	return nil
}

func (s *Store) JoinChannel(ctx context.Context, agentID, name string) error {
	name = normalizeChannel(name)
	if name == "" {
		return errors.New("channel name is required")
	}
	if err := s.CreateChannel(ctx, name); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO channel_members (channel_id, agent_id, joined_at)
SELECT id, ?, ? FROM channels WHERE name = ?
ON CONFLICT(channel_id, agent_id) DO NOTHING`, agentID, s.now().UnixMilli(), name)
	if err != nil {
		return fmt.Errorf("join channel: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		var joined int
		checkErr := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM channel_members cm
JOIN channels c ON c.id = cm.channel_id
WHERE cm.agent_id = ? AND c.name = ?`, agentID, name).Scan(&joined)
		if checkErr != nil || joined == 0 {
			return fmt.Errorf("join channel: agent %q is not registered", agentID)
		}
	}
	return nil
}

func (s *Store) LeaveChannel(ctx context.Context, agentID, name string) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM channel_members
WHERE agent_id = ? AND channel_id = (SELECT id FROM channels WHERE name = ?)`, agentID, normalizeChannel(name))
	if err != nil {
		return fmt.Errorf("leave channel: %w", err)
	}
	return nil
}

func (s *Store) Channels(ctx context.Context, agentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT c.name
FROM channels c
JOIN channel_members cm ON cm.channel_id = c.id
WHERE cm.agent_id = ?
ORDER BY c.name COLLATE NOCASE`, agentID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		channels = append(channels, name)
	}
	return channels, rows.Err()
}

func (s *Store) Send(ctx context.Context, request SendRequest) (int64, int, error) {
	request.SenderID = strings.TrimSpace(request.SenderID)
	request.TargetKind = strings.ToLower(strings.TrimSpace(request.TargetKind))
	request.TargetID = strings.TrimSpace(request.TargetID)
	request.Body = strings.TrimSpace(request.Body)
	if request.SenderID == "" || request.TargetID == "" || request.Body == "" {
		return 0, 0, errors.New("sender, target, and body are required")
	}
	if request.TargetKind != "repo" && request.TargetKind != "channel" && request.TargetKind != "agent" {
		return 0, 0, fmt.Errorf("unsupported target kind %q", request.TargetKind)
	}
	if request.TargetKind == "channel" {
		request.TargetID = normalizeChannel(request.TargetID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin send: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
INSERT INTO messages (sender_id, target_kind, target_id, body, created_at)
VALUES (?, ?, ?, ?, ?)`, request.SenderID, request.TargetKind, request.TargetID, request.Body, s.now().UnixMilli())
	if err != nil {
		return 0, 0, fmt.Errorf("store message: %w", err)
	}
	messageID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, fmt.Errorf("read message id: %w", err)
	}

	query, args := recipientQuery(request, s.now().Add(-activeWindow).UnixMilli())
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve recipients: %w", err)
	}
	var recipients []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan recipient: %w", err)
		}
		recipients = append(recipients, agentID)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, fmt.Errorf("close recipients: %w", err)
	}

	for _, agentID := range recipients {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inbox (message_id, agent_id) VALUES (?, ?)`, messageID, agentID); err != nil {
			return 0, 0, fmt.Errorf("enqueue message: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit send: %w", err)
	}
	return messageID, len(recipients), nil
}

func recipientQuery(request SendRequest, cutoff int64) (string, []any) {
	switch request.TargetKind {
	case "repo":
		return `
SELECT DISTINCT p.agent_id
FROM presence p
WHERE p.repo_id = ? AND p.last_seen_at >= ? AND p.agent_id <> ?`, []any{request.TargetID, cutoff, request.SenderID}
	case "channel":
		return `
SELECT cm.agent_id
FROM channel_members cm
JOIN channels c ON c.id = cm.channel_id
WHERE c.name = ? AND cm.agent_id <> ?`, []any{request.TargetID, request.SenderID}
	default:
		return `SELECT id FROM agents WHERE id = ? AND id <> ?`, []any{request.TargetID, request.SenderID}
	}
}

func (s *Store) ClaimInbox(ctx context.Context, agentID string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin inbox claim: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
SELECT m.id, m.sender_id, a.name, m.target_kind, m.target_id, m.body, m.created_at
FROM inbox i
JOIN messages m ON m.id = i.message_id
JOIN agents a ON a.id = m.sender_id
WHERE i.agent_id = ? AND i.claimed_at IS NULL
ORDER BY m.id
LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}

	var messages []Message
	for rows.Next() {
		var message Message
		var createdAt int64
		if err := rows.Scan(&message.ID, &message.SenderID, &message.SenderName, &message.TargetKind, &message.TargetID, &message.Body, &createdAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan inbox: %w", err)
		}
		message.CreatedAt = time.UnixMilli(createdAt)
		messages = append(messages, message)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close inbox: %w", err)
	}

	now := s.now().UnixMilli()
	claimed := messages[:0]
	for _, message := range messages {
		result, err := tx.ExecContext(ctx, `
UPDATE inbox SET claimed_at = ?
WHERE message_id = ? AND agent_id = ? AND claimed_at IS NULL`, now, message.ID, agentID)
		if err != nil {
			return nil, fmt.Errorf("claim message: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			claimed = append(claimed, message)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit inbox claim: %w", err)
	}
	return claimed, nil
}

func (s *Store) SetIntent(ctx context.Context, intent Intent) error {
	intent.Summary = strings.TrimSpace(intent.Summary)
	if intent.AgentID == "" || intent.RepoID == "" || intent.Summary == "" {
		return errors.New("agent, repository, and summary are required")
	}
	if intent.ExpiresAt.IsZero() {
		intent.ExpiresAt = s.now().Add(4 * time.Hour)
	}
	paths, err := json.Marshal(intent.Paths)
	if err != nil {
		return fmt.Errorf("encode intent paths: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO intents (agent_id, repo_id, summary, paths_json, expires_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(agent_id, repo_id) DO UPDATE SET
  summary = excluded.summary,
  paths_json = excluded.paths_json,
  expires_at = excluded.expires_at,
  updated_at = excluded.updated_at`, intent.AgentID, intent.RepoID, intent.Summary, string(paths), intent.ExpiresAt.UnixMilli(), s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("set intent: %w", err)
	}
	return nil
}

func (s *Store) ClearIntent(ctx context.Context, agentID, repoID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM intents WHERE agent_id = ? AND repo_id = ?`, agentID, repoID)
	if err != nil {
		return fmt.Errorf("clear intent: %w", err)
	}
	return nil
}

func (s *Store) ActiveIntents(ctx context.Context, repoID, excludeAgentID string) ([]Intent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT i.agent_id, a.name, i.repo_id, i.summary, i.paths_json, i.expires_at, i.updated_at
FROM intents i
JOIN agents a ON a.id = i.agent_id
WHERE i.repo_id = ? AND i.agent_id <> ? AND i.expires_at > ?
ORDER BY i.updated_at DESC`, repoID, excludeAgentID, s.now().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list active intents: %w", err)
	}
	defer rows.Close()

	var intents []Intent
	for rows.Next() {
		var intent Intent
		var paths string
		var expiresAt, updatedAt int64
		if err := rows.Scan(&intent.AgentID, &intent.AgentName, &intent.RepoID, &intent.Summary, &paths, &expiresAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan active intent: %w", err)
		}
		if err := json.Unmarshal([]byte(paths), &intent.Paths); err != nil {
			return nil, fmt.Errorf("decode intent paths: %w", err)
		}
		intent.ExpiresAt = time.UnixMilli(expiresAt)
		intent.UpdatedAt = time.UnixMilli(updatedAt)
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (s *Store) Agents(ctx context.Context, repoID string) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT a.id, a.harness, a.name, a.last_seen_at
FROM agents a
LEFT JOIN presence p ON p.agent_id = a.id
WHERE ? = '' OR p.repo_id = ?
ORDER BY a.name COLLATE NOCASE`, repoID, repoID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var agent Agent
		var lastSeen int64
		if err := rows.Scan(&agent.ID, &agent.Harness, &agent.Name, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agent.LastSeen = time.UnixMilli(lastSeen)
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func normalizeChannel(name string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "#")
}
