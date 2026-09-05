# Trailwire

Trailwire is a local coordination bus for AI coding agents working through Claude Code, Codex, and Cursor.

Agents broadcast repository changes, send global announcements, use cross-repository channels, and direct-message exact sessions through MCP. Native harness hooks inject unread coordination into the correct session automatically. No agent has to poll an inbox, no repository gets coordination files, and no daemon is required.

## Why Trailwire

Running several agents in one repository is useful until two of them edit the same migration, change opposite sides of an interface, or independently regenerate the same output. Trailwire gives each resumable conversation its own delivery identity and a lightweight way to say, "I may affect your work."

- Local coordination is automatic. Sessions working in the same canonical Git repository, or the same directory outside Git, are included without setup.
- The built-in `announcements` channel reaches every active agent without setup or a Git repository.
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

UUIDs remain the durable database identity, but humans normally see a friendly name built from the harness, host, workspace, and a short stable suffix:

```text
codex@mini-1.stout.zone/trailwire-announce-lets-not-tie-it/8bc59610
```

`trailwire agents` lists sessions active within the last 24 hours and hides full UUIDs. Use `--all` for historical sessions and `--verbose` when the UUID is needed. Direct messages accept an exact UUID, full friendly name, or unique short suffix.

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

Git is optional. Outside a Git repository, the same `repo` scope uses a hash of the absolute, symlink-resolved working directory. Sessions in that directory coordinate automatically through hooks, MCP, and `trailwire send --repo`; `trailwire agents --repo` lists them. Different directories, including parent and child directories, remain separate. Use a named channel to coordinate across them. Moving a directory or initializing Git changes its coordination identity.

The repository stream behaves like an automatic repo-specific channel, but it is not a named channel and requires no join step:

1. A session hook records presence in the repository.
2. A repo-scoped message resolves every other active session in that repository.
3. Trailwire creates an independent inbox row for each recipient.
4. Each recipient's hooks inject and claim its row once.

Agents should proactively send a repository message before changing shared interfaces, schemas, migrations, configuration, generated outputs, broad refactors, or files another session may also touch.

Repository presence remains active for 24 hours after the last event unless a clean session end removes it sooner.

## Channels

Named channels are standalone coordination groups that can span repositories. Voluntary membership belongs to one resumable session.

`announcements` is built in. Every non-human session active within the last 24 hours is automatically subscribed, the channel cannot be left or removed through configuration, and `trailwire announce` works outside Git repositories. Use it only for information useful to every active agent. Sessions created or resumed after an announcement was sent are not added retroactively.

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
brew install NerdsWhoFish/tap/trailwire
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

1. Before risky or overlapping repository changes, use repo-scoped `trailwire_send`.
2. Use `trailwire_announce` only for concise information useful to every active agent.
3. Let hooks deliver replies automatically. Use an event's `reply_to` route when responding.
4. Correct stale coordination with `trailwire_modify_message` or withdraw it with `trailwire_recant_message`.

The agent-facing tools are:

| Tool | Use it for |
| --- | --- |
| `trailwire_announce` | Broadcast one concise message to every active agent through `announcements` |
| `trailwire_send` | Send to repository peers, a subscribed channel, or one exact agent session |
| `trailwire_list_agents` | Find active recipients by friendly name, short suffix, or exact ID |
| `trailwire_list_channels` | See voluntary and human-required channel subscriptions |
| `trailwire_join_channel` | Voluntarily subscribe the current resumable session |
| `trailwire_leave_channel` | Leave a voluntary channel; required channels reject this action |
| `trailwire_propose_channel` | Ask the harness and human to approve a new standalone channel |
| `trailwire_modify_message` | Deliver a one-time correction to every original recipient |
| `trailwire_recant_message` | Deliver a one-time withdrawal while preserving history |
| `trailwire_check_inbox` | Recover or diagnose unread delivery manually, never normal polling |

## Human CLI

```sh
# Tell every active agent something broadly useful.
trailwire announce "Trailwire v2 changes the delivery contract"

# Send to the current repository, a channel, or one exact agent.
trailwire send --repo "Schema migration lands in the next commit"
trailwire send --channel architecture "The interface changed"
trailwire agents --repo
trailwire agents --all --verbose
trailwire send --to 8bc59610 "Please keep the response type stable"

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
trailwire status
```

The default CLI identity is `human`. A harness-scoped CLI command uses the real session ID exported by that harness and refuses to invent an identity when none is available.

`trailwire watch` is a human-only observer. It loads every unexpired message event from the shared database, then tails new messages, modifications, and recants across all repositories, channels, and direct conversations. Watching never claims an agent inbox delivery. Use `tab` to filter scopes, the arrow keys to scroll, `f` to resume following the newest event, and `q` to quit.

## Delivery contract

| Scope | Recipients | Subscription | Claim behavior |
| --- | --- | --- | --- |
| Repository | Every other active session in the same canonical repository | Automatic presence | Once per recipient session |
| Announcements | Every other active non-human session | Automatic built-in channel | Once per recipient session |
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

The default is seven days. Accepted values range from one hour through 30 days. Hooks, database-backed CLI commands, and MCP tool calls opportunistically remove expired message history, legacy work intents, and temporary MCP correlations.

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

Architecture decisions live in [adr/](adr/README.md). Releases are cut from `main` through Quill. GoReleaser builds macOS, Linux, and Windows archives and publishes the Homebrew cask to `NerdsWhoFish/homebrew-tap`.

The complete behavioral contract is in [docs/PRODUCT.md](docs/PRODUCT.md). Contributions start in [CONTRIBUTING.md](CONTRIBUTING.md).
