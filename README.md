# Trailwire

Trailwire is a local coordination bus for AI coding agents working through different harnesses.

Run Claude Code, Codex, and Cursor in the same repository. Trailwire gives each harness a stable identity, lets agents announce work and message one another through stdio MCP, and injects only unread coordination through native hooks. Named channels are separate from repositories. Everything stays in one local SQLite database and no daemon is required.

## Install

With Homebrew:

```sh
brew install TheOutdoorProgrammer/tap/trailwire
```

With Go 1.25 or newer:

```sh
go install github.com/theoutdoorprogrammer/trailwire/cmd/trailwire@latest
```

Then configure every supported harness:

```sh
trailwire init
```

Restart open Claude Code, Codex, and Cursor sessions so they load the new MCP server and hooks. `trailwire init --dry-run` reports the files it would change. Re-running init is safe and preserves unrelated hooks and MCP servers.

## How agents coordinate

Trailwire exposes MCP tools for agents to:

- Announce work in the current Git repository
- Send repository broadcasts, standalone channel messages, and direct messages
- Modify or recant their own messages
- Join and leave channels
- Propose a new channel through a distinct tool call that the harness can deny
- Clear an active work announcement when finished

Hooks check Trailwire around prompts, tools, and session lifecycle events. Inbox events are atomically claimed, so a hook injects only events missed since the previous successful check. It never replays the conversation. A modification or recant is a new one-time event, and recanting preserves the original history until retention cleanup.

Agents seen working in the same canonical Git remote receive repository broadcasts automatically. No repo-specific channel exists. Channels use explicit membership and can span repositories.

## Human CLI

```sh
# Tell every active agent in this repository what you are changing.
trailwire announce --path internal/store "Changing inbox persistence"

# Send to the current repository, a channel, or one agent.
trailwire send --repo "Schema migration lands in the next commit"
trailwire send --channel architecture "The interface changed"
trailwire send --to codex "Please keep the response type stable"

# Correct or recant a message without erasing its history.
trailwire message modify 42 "The migration is now 0042"
trailwire message recant 42 "Superseded by the storage rewrite"

# Humans create channels. Agents propose them with an MCP tool.
trailwire channel create architecture
trailwire channel join architecture

trailwire inbox
trailwire agents --repo
trailwire done
```

The default CLI identity is `human`. Pass `--harness claude`, `--harness codex`, or `--harness cursor` when diagnosing one harness identity.

## Harness delivery

| Harness | Automatic checks | Context injection |
| --- | --- | --- |
| Claude Code | Session, prompt, tool, stop, and session end hooks | Session start, prompt submit, before and after tools, and stop |
| Codex | Session, prompt, tool, stop, and session end hooks | Session start, prompt submit, before and after tools, and stop |
| Cursor | Session, prompt, tool, stop, and session end hooks | Session start, after successful tools, and stop |

Cursor's `beforeSubmitPrompt` and `preToolUse` hooks cannot inject context. Trailwire still refreshes presence and runs cleanup there, but leaves unread events unclaimed until `sessionStart`, `postToolUse`, or `stop` can deliver them.

## Retention

Message TTL is global and human-owned. Agents cannot set a per-message TTL.

```sh
trailwire config retention 72h
```

The default is seven days. Trailwire accepts values from one hour through 30 days. Every hook invocation, database-backed CLI command, and MCP tool call opportunistically removes expired messages and work intents.

## Storage and identity

Trailwire uses SQLite in WAL mode with foreign keys and a busy timeout. The default database and config live in the platform user-config directory. Override them when needed:

```sh
export TRAILWIRE_CONFIG_DIR=/path/to/config-directory
export TRAILWIRE_DATA_DIR=/path/to/data-directory
export TRAILWIRE_DATABASE=/path/to/trailwire.db
```

Git remote URLs are normalized to identify repositories. Repositories without an origin use a hash of their local Git common directory, so linked worktrees still coordinate. The first release is intentionally local-only: harnesses must share the database as the same operating-system user.

## OpenTelemetry

Telemetry is disabled by default. The disabled path does not construct an exporter.

To opt in, enable it explicitly and use standard OTLP environment variables:

```sh
export TRAILWIRE_OTEL_ENABLED=true
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
trailwire status
```

Trailwire emits command spans with service name and version. It does not attach message bodies, repository paths, prompts, or agent identifiers.

## Security model

Peer messages are injected as escaped JSON inside a warning that marks them as untrusted collaboration data. They never become system or user instructions. Harness permissions remain authoritative for MCP tool calls, including channel proposals.

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities and [docs/PRODUCT.md](docs/PRODUCT.md) for the product contract.

## Development

```sh
make test
make build
make install
```

Architecture decisions live in [adr/](adr/README.md). Releases are cut from `main` through Quill, which runs GoReleaser and publishes the Homebrew cask.
