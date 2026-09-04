package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	_ "modernc.org/sqlite"
)

const (
	activeWindow         = 24 * time.Hour
	AnnouncementsChannel = "announcements"
)

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

type Channel struct {
	Name   string
	Forced bool
}

type Message struct {
	EventID    int64
	ID         int64
	EventKind  string
	SenderID   string
	SenderName string
	TargetKind string
	TargetID   string
	Body       string
	CreatedAt  time.Time
}

type ObservedMessage struct {
	EventID          int64
	ID               int64
	EventKind        string
	SenderID         string
	SenderHarness    string
	SenderName       string
	TargetKind       string
	TargetID         string
	TargetName       string
	Body             string
	CreatedAt        time.Time
	MessageCreatedAt time.Time
}

type SendRequest struct {
	SenderID   string
	TargetKind string
	TargetID   string
	Body       string
}

type CleanupResult struct {
	Messages int64
	Intents  int64
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}

	dsn := "file:" + filepath.ToSlash(path) + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
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

CREATE TABLE IF NOT EXISTS agent_sessions (
  harness TEXT NOT NULL,
  native_session_id TEXT NOT NULL,
  agent_id TEXT NOT NULL UNIQUE REFERENCES agents(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (harness, native_session_id)
);

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

CREATE TABLE IF NOT EXISTS forced_channels (
  channel_id INTEGER PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
  configured_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  sender_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('repo', 'channel', 'agent')),
  target_id TEXT NOT NULL,
	body TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	recanted_at INTEGER
);
CREATE INDEX IF NOT EXISTS messages_created ON messages(created_at);

CREATE TABLE IF NOT EXISTS message_recipients (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	PRIMARY KEY (message_id, agent_id)
);

CREATE TABLE IF NOT EXISTS message_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	kind TEXT NOT NULL CHECK (kind IN ('created', 'modified', 'recanted')),
	body TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS message_events_message ON message_events(message_id, id);

CREATE TABLE IF NOT EXISTS inbox (
	event_id INTEGER NOT NULL REFERENCES message_events(id) ON DELETE CASCADE,
	agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	claimed_at INTEGER,
	PRIMARY KEY (event_id, agent_id)
);
CREATE INDEX IF NOT EXISTS inbox_unclaimed ON inbox(agent_id, claimed_at, event_id);

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

CREATE TABLE IF NOT EXISTS mcp_call_contexts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  harness TEXT NOT NULL,
  native_session_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  arguments_hash TEXT NOT NULL,
  call_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(harness, native_session_id, tool_name, arguments_hash, call_id)
);
CREATE INDEX IF NOT EXISTS mcp_call_context_match ON mcp_call_contexts(harness, tool_name, arguments_hash, created_at);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

func (s *Store) BindSession(ctx context.Context, harness, nativeSessionID string, candidate Agent, legacyAgentID string) (Agent, error) {
	harness = strings.ToLower(strings.TrimSpace(harness))
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	legacyAgentID = strings.TrimSpace(legacyAgentID)
	candidate.ID = strings.TrimSpace(candidate.ID)
	candidate.Harness = harness
	candidate.Name = strings.TrimSpace(candidate.Name)
	if harness == "" || nativeSessionID == "" || candidate.ID == "" || candidate.Name == "" {
		return Agent{}, errors.New("harness, native session id, and candidate agent are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, fmt.Errorf("begin session binding: %w", err)
	}
	defer tx.Rollback()

	readBinding := func() (Agent, error) {
		var agent Agent
		var lastSeen int64
		err := tx.QueryRowContext(ctx, `
SELECT a.id, a.harness, a.name, a.last_seen_at
FROM agent_sessions s
JOIN agents a ON a.id = s.agent_id
WHERE s.harness = ? AND s.native_session_id = ?`, harness, nativeSessionID).Scan(&agent.ID, &agent.Harness, &agent.Name, &lastSeen)
		agent.LastSeen = time.UnixMilli(lastSeen)
		return agent, err
	}

	agent, err := readBinding()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Agent{}, fmt.Errorf("read session binding: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) && legacyAgentID != "" {
		_, err = tx.ExecContext(ctx, `
INSERT OR IGNORE INTO agent_sessions (harness, native_session_id, agent_id, created_at)
SELECT ?, ?, a.id, ?
FROM agents a
WHERE a.id = ?
  AND a.harness = ?
  AND NOT EXISTS (SELECT 1 FROM agent_sessions existing WHERE existing.agent_id = a.id)`, harness, nativeSessionID, s.now().UnixMilli(), legacyAgentID, harness)
		if err != nil {
			return Agent{}, fmt.Errorf("adopt legacy agent: %w", err)
		}
		agent, err = readBinding()
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Agent{}, fmt.Errorf("read adopted session binding: %w", err)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		now := s.now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO agents (id, harness, name, created_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET harness = excluded.harness, name = excluded.name, last_seen_at = excluded.last_seen_at`, candidate.ID, harness, candidate.Name, now, now); err != nil {
			return Agent{}, fmt.Errorf("register session agent: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO agent_sessions (harness, native_session_id, agent_id, created_at)
VALUES (?, ?, ?, ?)`, harness, nativeSessionID, candidate.ID, now); err != nil {
			return Agent{}, fmt.Errorf("bind session agent: %w", err)
		}
		agent, err = readBinding()
		if err != nil {
			return Agent{}, fmt.Errorf("read bound session agent: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET name = ?, last_seen_at = ? WHERE id = ?`, candidate.Name, s.now().UnixMilli(), agent.ID); err != nil {
		return Agent{}, fmt.Errorf("touch bound agent: %w", err)
	}
	agent.Name = candidate.Name
	agent.LastSeen = s.now()
	if err := tx.Commit(); err != nil {
		return Agent{}, fmt.Errorf("commit session binding: %w", err)
	}
	return agent, nil
}

func (s *Store) SyncForcedChannels(ctx context.Context, names []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin forced channel sync: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM forced_channels`); err != nil {
		return fmt.Errorf("clear forced channels: %w", err)
	}
	names = append([]string{AnnouncementsChannel}, names...)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = normalizeChannel(name)
		if name == "" {
			return errors.New("channel name is required")
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		now := s.now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels (name, created_at) VALUES (?, ?) ON CONFLICT(name) DO NOTHING`, name, now); err != nil {
			return fmt.Errorf("create forced channel: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO forced_channels (channel_id, configured_at)
SELECT id, ? FROM channels WHERE name = ?
ON CONFLICT(channel_id) DO UPDATE SET configured_at = excluded.configured_at`, now, name); err != nil {
			return fmt.Errorf("force channel: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit forced channel sync: %w", err)
	}
	return nil
}

func (s *Store) RecordMCPCall(ctx context.Context, harness, nativeSessionID, toolName, argumentsHash, callID string) error {
	harness = strings.ToLower(strings.TrimSpace(harness))
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	toolName = strings.TrimSpace(toolName)
	argumentsHash = strings.TrimSpace(argumentsHash)
	callID = strings.TrimSpace(callID)
	if harness == "" || nativeSessionID == "" || toolName == "" || argumentsHash == "" {
		return errors.New("harness, native session id, tool name, and arguments hash are required")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO mcp_call_contexts (harness, native_session_id, tool_name, arguments_hash, call_id, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(harness, native_session_id, tool_name, arguments_hash, call_id) DO UPDATE SET created_at = excluded.created_at`, harness, nativeSessionID, toolName, argumentsHash, callID, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("record MCP call context: %w", err)
	}
	return nil
}

func (s *Store) ClaimMCPCall(ctx context.Context, harness, toolName, argumentsHash, callID string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin MCP call claim: %w", err)
	}
	defer tx.Rollback()
	cutoff := s.now().Add(-time.Minute).UnixMilli()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_call_contexts WHERE created_at < ?`, cutoff); err != nil {
		return "", fmt.Errorf("expire MCP call contexts: %w", err)
	}
	var id int64
	var nativeSessionID string
	err = tx.QueryRowContext(ctx, `
SELECT id, native_session_id
FROM mcp_call_contexts
WHERE harness = ? AND tool_name = ? AND arguments_hash = ? AND created_at >= ?
ORDER BY CASE WHEN call_id = ? AND ? <> '' THEN 0 ELSE 1 END, created_at, id
LIMIT 1`, strings.ToLower(strings.TrimSpace(harness)), strings.TrimSpace(toolName), strings.TrimSpace(argumentsHash), cutoff, callID, callID).Scan(&id, &nativeSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit empty MCP call claim: %w", err)
		}
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find MCP call context: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_call_contexts WHERE id = ?`, id); err != nil {
		return "", fmt.Errorf("claim MCP call context: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit MCP call claim: %w", err)
	}
	return nativeSessionID, nil
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
  name = excluded.name`, agent.ID, agent.Harness, agent.Name, now, now)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session end: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM presence WHERE agent_id = ? AND session_id = ?`, agentID, sessionID); err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE agents
SET last_seen_at = 0
WHERE id = ? AND NOT EXISTS (SELECT 1 FROM presence WHERE agent_id = ?)`, agentID, agentID); err != nil {
		return fmt.Errorf("deactivate agent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session end: %w", err)
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
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE name = ?`, name).Scan(&exists); err != nil {
		return fmt.Errorf("find channel: %w", err)
	}
	if exists == 0 {
		return fmt.Errorf("channel %q does not exist, create or propose it first", name)
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

func (s *Store) ProposeChannel(ctx context.Context, agentID, name string) error {
	if err := s.CreateChannel(ctx, name); err != nil {
		return err
	}
	if err := s.JoinChannel(ctx, agentID, name); err != nil {
		return fmt.Errorf("join proposed channel: %w", err)
	}
	return nil
}

func (s *Store) LeaveChannel(ctx context.Context, agentID, name string) error {
	name = normalizeChannel(name)
	var forced int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM forced_channels fc
JOIN channels c ON c.id = fc.channel_id
JOIN agent_sessions session ON session.agent_id = ?
WHERE c.name = ?`, agentID, name).Scan(&forced); err != nil {
		return fmt.Errorf("check forced channel: %w", err)
	}
	if forced > 0 {
		return fmt.Errorf("channel %q is required by human configuration", name)
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM channel_members
WHERE agent_id = ? AND channel_id = (SELECT id FROM channels WHERE name = ?)`, agentID, name)
	if err != nil {
		return fmt.Errorf("leave channel: %w", err)
	}
	return nil
}

func (s *Store) Channels(ctx context.Context, agentID string) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT name, MAX(forced)
FROM (
  SELECT c.name, 0 AS forced
  FROM channels c
  JOIN channel_members cm ON cm.channel_id = c.id
  WHERE cm.agent_id = ?
  UNION ALL
  SELECT c.name, 1 AS forced
  FROM channels c
  JOIN forced_channels fc ON fc.channel_id = c.id
  WHERE EXISTS (SELECT 1 FROM agent_sessions session WHERE session.agent_id = ?)
)
GROUP BY name
ORDER BY name COLLATE NOCASE`, agentID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var channel Channel
		if err := rows.Scan(&channel.Name, &channel.Forced); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		channels = append(channels, channel)
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

	now := s.now().UnixMilli()
	result, err := tx.ExecContext(ctx, `
INSERT INTO messages (sender_id, target_kind, target_id, body, created_at)
VALUES (?, ?, ?, ?, ?)`, request.SenderID, request.TargetKind, request.TargetID, request.Body, now)
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
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO message_recipients (message_id, agent_id) VALUES (?, ?)`, messageID, agentID); err != nil {
			return 0, 0, fmt.Errorf("store message recipient: %w", err)
		}
	}
	eventID, err := insertMessageEvent(ctx, tx, messageID, "created", request.Body, now)
	if err != nil {
		return 0, 0, err
	}
	if err := enqueueMessageEvent(ctx, tx, messageID, eventID); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit send: %w", err)
	}
	return messageID, len(recipients), nil
}

func (s *Store) ModifyMessage(ctx context.Context, senderID string, messageID int64, body string) (int, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return 0, errors.New("message body is required")
	}
	return s.reviseMessage(ctx, senderID, messageID, "modified", body)
}

func (s *Store) RecantMessage(ctx context.Context, senderID string, messageID int64, reason string) (int, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "No reason provided"
	}
	return s.reviseMessage(ctx, senderID, messageID, "recanted", reason)
}

func (s *Store) reviseMessage(ctx context.Context, senderID string, messageID int64, kind, body string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin message revision: %w", err)
	}
	defer tx.Rollback()

	var owner string
	var recantedAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT sender_id, recanted_at FROM messages WHERE id = ?`, messageID).Scan(&owner, &recantedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("message %d was not found", messageID)
		}
		return 0, fmt.Errorf("find message: %w", err)
	}
	if owner != senderID {
		return 0, errors.New("only the sender can modify or recant a message")
	}
	if recantedAt.Valid {
		return 0, fmt.Errorf("message %d is already recanted", messageID)
	}

	now := s.now().UnixMilli()
	if kind == "modified" {
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET body = ? WHERE id = ?`, body, messageID); err != nil {
			return 0, fmt.Errorf("modify message: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET recanted_at = ? WHERE id = ?`, now, messageID); err != nil {
			return 0, fmt.Errorf("recant message: %w", err)
		}
	}
	eventID, err := insertMessageEvent(ctx, tx, messageID, kind, body, now)
	if err != nil {
		return 0, err
	}
	if err := enqueueMessageEvent(ctx, tx, messageID, eventID); err != nil {
		return 0, err
	}
	var recipients int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_recipients WHERE message_id = ?`, messageID).Scan(&recipients); err != nil {
		return 0, fmt.Errorf("count message recipients: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit message revision: %w", err)
	}
	return recipients, nil
}

func insertMessageEvent(ctx context.Context, tx *sql.Tx, messageID int64, kind, body string, createdAt int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `
INSERT INTO message_events (message_id, kind, body, created_at)
VALUES (?, ?, ?, ?)`, messageID, kind, body, createdAt)
	if err != nil {
		return 0, fmt.Errorf("store message event: %w", err)
	}
	eventID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read message event id: %w", err)
	}
	return eventID, nil
}

func enqueueMessageEvent(ctx context.Context, tx *sql.Tx, messageID, eventID int64) error {
	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO inbox (event_id, agent_id)
SELECT ?, agent_id FROM message_recipients WHERE message_id = ?`, eventID, messageID); err != nil {
		return fmt.Errorf("enqueue message event: %w", err)
	}
	return nil
}

func recipientQuery(request SendRequest, cutoff int64) (string, []any) {
	switch request.TargetKind {
	case "repo":
		return `
SELECT DISTINCT p.agent_id
FROM presence p
WHERE p.repo_id = ? AND p.last_seen_at >= ? AND p.agent_id <> ?`, []any{request.TargetID, cutoff, request.SenderID}
	case "channel":
		if request.TargetID == AnnouncementsChannel {
			return `
SELECT session.agent_id
FROM agent_sessions session
JOIN agents a ON a.id = session.agent_id
WHERE session.native_session_id <> 'cli'
  AND a.harness <> 'human'
  AND a.last_seen_at >= ?
  AND session.agent_id <> ?`, []any{cutoff, request.SenderID}
		}
		return `
SELECT agent_id
FROM (
  SELECT cm.agent_id
  FROM channel_members cm
  JOIN channels c ON c.id = cm.channel_id
  WHERE c.name = ?
  UNION
  SELECT session.agent_id
  FROM forced_channels fc
  JOIN channels c ON c.id = fc.channel_id
  CROSS JOIN agent_sessions session
  JOIN agents a ON a.id = session.agent_id
  WHERE c.name = ? AND a.harness <> 'human' AND session.native_session_id <> 'cli'
)
WHERE agent_id <> ?`, []any{request.TargetID, request.TargetID, request.SenderID}
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
UPDATE inbox
SET claimed_at = ?
WHERE rowid IN (
  SELECT rowid FROM inbox
  WHERE agent_id = ? AND claimed_at IS NULL
  ORDER BY event_id
  LIMIT ?
)
RETURNING event_id`, s.now().UnixMilli(), agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("claim inbox: %w", err)
	}
	var eventIDs []int64
	for rows.Next() {
		var eventID int64
		if err := rows.Scan(&eventID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan claimed event: %w", err)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close claimed events: %w", err)
	}
	if len(eventIDs) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty inbox claim: %w", err)
		}
		return nil, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(eventIDs)), ",")
	args := make([]any, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		args = append(args, eventID)
	}
	rows, err = tx.QueryContext(ctx, `
SELECT e.id, m.id, e.kind, m.sender_id, a.name, m.target_kind, m.target_id, e.body, e.created_at
FROM message_events e
JOIN messages m ON m.id = e.message_id
JOIN agents a ON a.id = m.sender_id
WHERE e.id IN (`+placeholders+`)
ORDER BY e.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}

	var messages []Message
	for rows.Next() {
		var message Message
		var createdAt int64
		if err := rows.Scan(&message.EventID, &message.ID, &message.EventKind, &message.SenderID, &message.SenderName, &message.TargetKind, &message.TargetID, &message.Body, &createdAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan inbox: %w", err)
		}
		message.CreatedAt = time.UnixMilli(createdAt)
		messages = append(messages, message)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close inbox: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit inbox claim: %w", err)
	}
	return messages, nil
}

func (s *Store) ObserveMessages(ctx context.Context, afterEventID int64, messageCutoff time.Time) (messages []ObservedMessage, err error) {
	ctx, span := otel.Tracer("github.com/theoutdoorprogrammer/trailwire/internal/store").Start(ctx, "trailwire.store.observe_messages")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "observe messages failed")
		} else {
			span.SetAttributes(attribute.Int("trailwire.observer.event_count", len(messages)))
		}
		span.End()
	}()
	if afterEventID < 0 {
		afterEventID = 0
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT
  e.id,
  m.id,
  e.kind,
  sender.id,
  sender.harness,
  sender.name,
  m.target_kind,
  m.target_id,
  COALESCE(target.name, ''),
  e.body,
  e.created_at,
  m.created_at
FROM message_events e
JOIN messages m ON m.id = e.message_id
JOIN agents sender ON sender.id = m.sender_id
LEFT JOIN agents target ON m.target_kind = 'agent' AND target.id = m.target_id
WHERE e.id > ? AND m.created_at >= ?
ORDER BY e.id`, afterEventID, messageCutoff.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("observe messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var message ObservedMessage
		var createdAt, messageCreatedAt int64
		if err := rows.Scan(
			&message.EventID,
			&message.ID,
			&message.EventKind,
			&message.SenderID,
			&message.SenderHarness,
			&message.SenderName,
			&message.TargetKind,
			&message.TargetID,
			&message.TargetName,
			&message.Body,
			&createdAt,
			&messageCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan observed message: %w", err)
		}
		message.CreatedAt = time.UnixMilli(createdAt)
		message.MessageCreatedAt = time.UnixMilli(messageCreatedAt)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("observe messages: %w", err)
	}
	return messages, nil
}

func (s *Store) Cleanup(ctx context.Context, messageCutoff time.Time) (CleanupResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("begin cleanup: %w", err)
	}
	defer tx.Rollback()

	messageResult, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE created_at < ?`, messageCutoff.UnixMilli())
	if err != nil {
		return CleanupResult{}, fmt.Errorf("delete expired messages: %w", err)
	}
	intentResult, err := tx.ExecContext(ctx, `DELETE FROM intents WHERE expires_at <= ?`, s.now().UnixMilli())
	if err != nil {
		return CleanupResult{}, fmt.Errorf("delete expired intents: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_call_contexts WHERE created_at < ?`, s.now().Add(-time.Minute).UnixMilli()); err != nil {
		return CleanupResult{}, fmt.Errorf("delete expired MCP call contexts: %w", err)
	}
	messages, err := messageResult.RowsAffected()
	if err != nil {
		return CleanupResult{}, fmt.Errorf("count expired messages: %w", err)
	}
	intents, err := intentResult.RowsAffected()
	if err != nil {
		return CleanupResult{}, fmt.Errorf("count expired intents: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CleanupResult{}, fmt.Errorf("commit cleanup: %w", err)
	}
	return CleanupResult{Messages: messages, Intents: intents}, nil
}

func (s *Store) Agents(ctx context.Context, repoID string, includeInactive bool) ([]Agent, error) {
	cutoff := s.now().Add(-activeWindow).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `
SELECT a.id, a.harness, a.name, a.last_seen_at
FROM agents a
WHERE a.harness <> 'human'
  AND (? OR a.last_seen_at >= ?)
  AND (? OR EXISTS (
    SELECT 1 FROM agent_sessions session
    WHERE session.agent_id = a.id AND session.native_session_id <> 'cli'
  ))
  AND (
    ? = '' OR EXISTS (
      SELECT 1 FROM presence p
      WHERE p.agent_id = a.id AND p.repo_id = ? AND (? OR p.last_seen_at >= ?)
    )
  )
ORDER BY a.last_seen_at DESC, a.name COLLATE NOCASE`, includeInactive, cutoff, includeInactive, repoID, repoID, includeInactive, cutoff)
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

func (s *Store) ResolveAgent(ctx context.Context, query string) (Agent, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Agent{}, errors.New("agent is required")
	}
	suffix := "/" + query
	rows, err := s.db.QueryContext(ctx, `
SELECT id, harness, name, last_seen_at
FROM agents
WHERE id = ?
   OR name = ? COLLATE NOCASE
   OR substr(name, -length(?)) = ? COLLATE NOCASE
   OR (
     harness = ? COLLATE NOCASE
     AND last_seen_at >= ?
     AND EXISTS (
       SELECT 1 FROM agent_sessions session
       WHERE session.agent_id = agents.id AND session.native_session_id <> 'cli'
     )
   )
ORDER BY CASE WHEN id = ? THEN 0 WHEN name = ? COLLATE NOCASE THEN 1 ELSE 2 END`,
		query, query, suffix, suffix, query, s.now().Add(-activeWindow).UnixMilli(), query, query)
	if err != nil {
		return Agent{}, fmt.Errorf("resolve agent: %w", err)
	}
	defer rows.Close()

	var matches []Agent
	for rows.Next() {
		var agent Agent
		var lastSeen int64
		if err := rows.Scan(&agent.ID, &agent.Harness, &agent.Name, &lastSeen); err != nil {
			return Agent{}, fmt.Errorf("scan agent: %w", err)
		}
		agent.LastSeen = time.UnixMilli(lastSeen)
		matches = append(matches, agent)
	}
	if err := rows.Err(); err != nil {
		return Agent{}, fmt.Errorf("resolve agent: %w", err)
	}
	if len(matches) == 0 {
		return Agent{}, fmt.Errorf("agent %q was not found", query)
	}
	if len(matches) > 1 && matches[0].ID != query && !strings.EqualFold(matches[0].Name, query) {
		return Agent{}, fmt.Errorf("agent %q is ambiguous, use its id or full name", query)
	}
	return matches[0], nil
}

func normalizeChannel(name string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "#")
}
