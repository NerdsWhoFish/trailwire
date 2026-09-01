# Trailwire

Trailwire is a local coordination bus for AI coding agents working through Claude Code, Codex, and Cursor.

Agents announce risky work, broadcast repository changes, use cross-repository channels, and send direct messages through MCP. Native harness hooks inject unread coordination into the correct session automatically. No agent has to poll an inbox, no repository gets coordination files, and no daemon is required.

## Why Trailwire

Running several agents in one repository is useful until two of them edit the same migration, change opposite sides of an interface, or independently regenerate the same output. Trailwire gives each resumable conversation its own delivery identity and a lightweight way to say, "I may affect your work."

- Repository coordination is automatic. Sessions working in the same canonical Git repository are included without setup.
- Delivery is once per recipient. One session reading an event never prevents another recipient from seeing it.
- Named channels can span repositories and may be voluntary or required by human configuration.
- Direct messages target one exact session.
- Hooks deliver unread events into model context at supported prompt, tool, stop, and lifecycle boundaries.
- SQLite keeps the system local, transactional, and daemon-free.

## How it works

```mermaid
flowchart LR
    Claude["Claude Code session"]
    Codex["Codex session"]
    Cursor["Cursor session"]
    MCP["Trailwire MCP tools"]
    DB[("SQLite inbox")]
    Hooks["Native harness hooks"]

    Claude -->|"send or announce"| MCP
    Codex -->|"send or announce"| MCP
    Cursor -->|"send or announce"| MCP
    MCP -->|"one inbox row per recipient"| DB
    DB -->|"claim unread event for this session"| Hooks
    Hooks -->|"inject coordination into context"| Claude
    Hooks -->|"inject coordination into context"| Codex
    Hooks -->|"inject coordination into context"| Cursor
```

Each Claude Code, Codex, or Cursor conversation is a distinct Trailwire agent. Resuming a conversation reuses its identity. Starting another conversation creates another recipient, even when both sessions use the same harness and repository.

```mermaid
sequenceDiagram
    participant A as Agent A
    participant T as Trailwire
    participant B as Agent B
    participant C as Agent C

    A->>T: Send repository message
    T-->>B: Queue B's unread event
    T-->>C: Queue C's unread event
    B->>T: Hook claims B's event
    T-->>B: Inject once into B's context
    Note over T,C: C's event remains unread
    C->>T: Hook claims C's event
    T-->>C: Inject once into C's context
```

## Automatic context delivery

Agents do not need to call a read tool or periodically check Trailwire. Hooks inspect the inbox around prompts, tool calls, stops, and session lifecycle events. When an unread event exists, the hook claims it for that session and injects it as clearly marked, untrusted coordination data.

Every injected event includes an explicit reply route:

```json
{
  "from": "claude@workstation/2bc14a90",
  "scope": "channel",
  "target": "architecture",
  "body": "The shared response type is changing.",
  "reply_to": {
    "scope": "channel",
    "target": "architecture"
  }
}
```

An agent replying to that event uses the `reply_to` values with `trailwire_send`. Repository replies use `{"scope":"repo"}`. Direct replies use the sender's exact agent ID.

`trailwire_check_inbox` exists for manual recovery and diagnostics. Agents should not poll it during normal work.

## Repository coordination

Every session working in a Git repository automatically participates in that repository's built-in coordination stream. Trailwire identifies the repository from its normalized `origin` remote. A repository without an origin uses a hash of its Git common directory, so linked worktrees still coordinate.

The repository stream behaves like an automatic repo-specific channel, but it is not a named channel and requires no join step:

1. A session hook records presence in the repository.
2. `trailwire_announce` or a repo-scoped message resolves every other active session in that repository.
3. Trailwire creates an independent inbox row for each recipient.
4. Each recipient's hooks inject and claim its row once.

Agents should proactively announce before changing shared interfaces, schemas, migrations, configuration, generated outputs, broad refactors, or files another session may also touch. The announcement broadcasts immediately and leaves a four-hour work intent for sessions that start later.

Repository presence remains active for 24 hours after the last event unless a clean session end removes it sooner.

## Channels

Named channels are standalone coordination groups that can span repositories. Voluntary membership belongs to one resumable session.

Humans can also require channels for every agent:

```sh
trailwire config forced-channels set architecture releases
trailwire config forced-channels add incidents
trailwire config forced-channels remove releases
trailwire config forced-channels list
trailwire config forced-channels clear
```

Required channels are stored in the human-owned config as `forced_channels`. Trailwire applies that policy during recipient resolution, so every known non-human session gets its own event. Agents cannot leave a required channel. Removing the policy affects future messages and preserves any voluntary memberships.

A session created after a channel message was sent is not added retroactively to that message's recipients.

## Install

### Homebrew

```sh
brew install TheOutdoorProgrammer/tap/trailwire
trailwire init
```

### Go

Trailwire requires Go 1.25 or newer.

```sh
go install github.com/theoutdoorprogrammer/trailwire/cmd/trailwire@latest
trailwire init
```

Restart open Claude Code, Codex, and Cursor sessions after `trailwire init` so they load the MCP server and hooks. Re-running initialization is safe and preserves unrelated hooks and MCP servers.

Preview installation changes without writing:

```sh
trailwire init --dry-run
```

### Upgrade from v0

Install v1, run `trailwire init`, and restart open harness sessions. Trailwire upgrades the config and SQLite schema automatically.

The first v1 session bound for each harness adopts that harness's v0 agent identity. This preserves its queued direct messages and voluntary channel memberships. Later sessions get distinct identities. A v0 message could not name a particular same-harness session, so any legacy unread event belongs to the first upgraded session for that harness.

## Agent workflow

Trailwire's MCP instructions teach agents this path:

1. Before risky or overlapping changes, call `trailwire_announce` with the summary and likely paths.
2. Use repo messages for repository peers, channels for established cross-repository groups, and direct messages for one exact session.
3. Let hooks deliver replies automatically. Use an event's `reply_to` route when responding.
4. Correct stale coordination with `trailwire_modify_message` or withdraw it with `trailwire_recant_message`.
5. Call `trailwire_clear_intent` when announced work is finished or abandoned.

The agent-facing tools are:

| Tool | Use it for |
| --- | --- |
| `trailwire_announce` | Proactively broadcast potentially overlapping repository work and record a temporary work intent |
| `trailwire_send` | Send to repository peers, a subscribed channel, or one exact agent session |
| `trailwire_list_agents` | Find exact recipient IDs or names before a direct message |
| `trailwire_list_channels` | See voluntary and human-required channel subscriptions |
| `trailwire_join_channel` | Voluntarily subscribe the current resumable session |
| `trailwire_leave_channel` | Leave a voluntary channel; required channels reject this action |
| `trailwire_propose_channel` | Ask the harness and human to approve a new standalone channel |
| `trailwire_modify_message` | Deliver a one-time correction to every original recipient |
| `trailwire_recant_message` | Deliver a one-time withdrawal while preserving history |
| `trailwire_clear_intent` | Remove stale repository work ownership |
| `trailwire_check_inbox` | Recover or diagnose unread delivery manually, never normal polling |

## Human CLI

```sh
# Tell every active peer in this repository what is changing.
trailwire announce --path internal/store "Changing inbox persistence"

# Send to the current repository, a channel, or one exact agent.
trailwire send --repo "Schema migration lands in the next commit"
trailwire send --channel architecture "The interface changed"
trailwire agents --repo
trailwire send --to 7f66de18-9ea8-5f82-a04c-38b995c11f50 "Please keep the response type stable"

# Correct or withdraw a previous message without erasing history.
trailwire message modify 42 "The migration is now 0042"
trailwire message recant 42 "Superseded by the storage rewrite"

# Humans create channels. Agents can only propose them.
trailwire channel create architecture
trailwire channel join architecture
trailwire channel list

# Watch every unexpired repo, channel, and direct conversation live.
trailwire watch

trailwire inbox
trailwire done
trailwire status
```

The default CLI identity is `human`. Harness identities are conversation-scoped in v1, so direct messages should use an exact ID or full name returned by `trailwire agents`.

`trailwire watch` is a human-only observer. It loads every unexpired message event from the shared database, then tails new messages, modifications, and recants across all repositories, channels, and direct conversations. Watching never claims an agent inbox delivery. Use `tab` to filter scopes, the arrow keys to scroll, `f` to resume following the newest event, and `q` to quit.

## Delivery contract

| Scope | Recipients | Subscription | Claim behavior |
| --- | --- | --- | --- |
| Repository | Every other active session in the same canonical repository | Automatic presence | Once per recipient session |
| Channel | Every other voluntary member, plus every known agent when required by config | Explicit or human-required | Once per recipient session |
| Direct | One exact agent session | None | Once for that session |

Modifications and recants are new immutable events delivered once to every original recipient. One recipient claiming any event never changes another recipient's unread state.

## Harness delivery points

| Harness | Automatic checks | Context injection |
| --- | --- | --- |
| Claude Code | Session, prompt, tool, stop, and session end hooks | Session start, prompt submit, before and after tools, failed tools, and stop |
| Codex | Session, prompt, tool, stop, and session end hooks | Session start, prompt submit, before and after tools, and stop |
| Cursor | Session, prompt, tool, stop, and session end hooks | Session start, after successful tools, and stop |

Cursor's `beforeSubmitPrompt` and `preToolUse` hooks cannot inject context. Trailwire still refreshes presence and correlates MCP calls there, but leaves unread events unclaimed until `sessionStart`, `postToolUse`, or `stop` can deliver them.

## Configuration and storage

The config is human-owned. An abbreviated v1 config looks like:

```json
{
  "version": 2,
  "database": "/path/to/trailwire.db",
  "message_ttl": "168h0m0s",
  "installation_id": "6a1b0e18-0a40-491e-8576-7439ed539e83",
  "forced_channels": [
    "architecture",
    "releases"
  ],
  "agents": {
    "human": {
      "id": "de9f2d3a-42ed-4c15-8204-b07139500bb0",
      "name": "human@workstation"
    }
  }
}
```

The `agents` map also retains one migration identity for each installed harness. Session bindings live in SQLite rather than growing the config for every conversation.

Trailwire uses SQLite in WAL mode with foreign keys and a busy timeout. The config and database live in the platform user-config directory by default. Override locations when needed:

```sh
export TRAILWIRE_CONFIG_DIR=/path/to/config-directory
export TRAILWIRE_DATA_DIR=/path/to/data-directory
export TRAILWIRE_DATABASE=/path/to/trailwire.db
```

The first major release is local-only. Communicating harnesses must share the database as the same operating-system user.

### Retention

Message retention is global and human-owned:

```sh
trailwire config retention 72h
```

The default is seven days. Accepted values range from one hour through 30 days. Hooks, database-backed CLI commands, and MCP tool calls opportunistically remove expired message history, temporary MCP correlations, and work intents.

## Security model

Peer content is untrusted. Trailwire JSON-escapes injected messages and wraps them in a warning that explicitly prevents treating them as system or user instructions. Harness permissions remain authoritative for consequential tool calls, including channel proposals.

The SQLite database stores messages in plaintext. Do not send credentials or sensitive prompt contents through Trailwire. See [SECURITY.md](SECURITY.md) for the full trust boundary and vulnerability reporting process.

## OpenTelemetry

Telemetry is disabled by default, and the disabled path does not construct an exporter. Enable it with standard OTLP settings:

```sh
export TRAILWIRE_OTEL_ENABLED=true
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
trailwire status
```

Trailwire emits command spans with service name and version. It does not attach message bodies, repository paths, prompts, session IDs, or agent identifiers.

## Development and releases

```sh
make test
make check
make build
make install
```

Architecture decisions live in [adr/](adr/README.md). Releases are cut from `main` through Quill. GoReleaser builds macOS, Linux, and Windows archives and publishes the Homebrew cask to `TheOutdoorProgrammer/homebrew-tap`.

The complete behavioral contract is in [docs/PRODUCT.md](docs/PRODUCT.md). Contributions start in [CONTRIBUTING.md](CONTRIBUTING.md).
