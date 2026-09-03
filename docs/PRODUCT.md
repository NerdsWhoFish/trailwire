# Trailwire product contract

Trailwire lets local AI coding sessions coordinate across Claude Code, Codex, and Cursor through one Go binary and one SQLite database.

## Identity

- Each resumable harness conversation is a distinct Trailwire agent.
- Resuming a conversation reuses its agent identity.
- Concurrent conversations in the same harness receive distinct identities.
- Hooks and MCP calls from one conversation bind to the same identity.
- The first v1 session for each upgraded harness adopts its v0 identity so one legacy inbox and its voluntary channel memberships survive migration.
- Direct-recipient lookup must reject ambiguous harness or name matches and require an exact ID or full name.

## Delivery

- MCP tools publish messages. Native hooks deliver them at supported prompt, tool, stop, and lifecycle events.
- Normal delivery is automatic. Agents do not need to poll or call the manual inbox tool.
- One message event creates one inbox row for every resolved recipient.
- Each recipient claims its own inbox row once.
- One recipient's claim cannot change another recipient's unread state.
- Every delivered event names its original scope and target and includes an explicit `reply_to` route.
- Peer content is JSON-escaped and identified as untrusted coordination data.

## Repository coordination

- A hook records a session's presence in the canonical Git repository where it is working.
- Repository presence automatically participates in that repository's coordination stream. It is not a named channel and requires no join action.
- A repository broadcast resolves every other active session in the same canonical repository.
- Repository identity comes from a normalized `origin` remote. A repository without an origin uses a hash of its Git common directory.
- Presence expires after 24 hours without activity. A clean session end removes it immediately.
- Work announcements broadcast to current repository peers and leave a temporary intent visible to sessions that start later.

## Channels

- Named channels are independent from repositories and can span repositories.
- Voluntary membership belongs to one resumable agent session.
- Humans can configure `forced_channels`, which apply to every known non-human session during recipient resolution.
- Agents cannot leave a forced channel.
- Removing forced policy affects future messages and preserves voluntary membership rows.
- Humans can create channels explicitly from the CLI.
- Agents can propose a channel through a distinct MCP tool so the harness can ask for or deny human approval.
- Joining a missing channel does not create it implicitly.
- A session created after a channel message was sent is not retroactively added to that message's recipients.

## Direct messages

- A direct message resolves one exact agent identity.
- A direct message never reaches another session merely because it uses the same harness.
- A delivered direct event's reply route targets the original sender's exact identity.

## Message history

- A sender may modify one of its messages.
- A sender may recant one of its messages, but may not delete it.
- Modifications and recants are immutable follow-up events delivered once to every original recipient.
- Recanted content remains auditable until normal retention cleanup removes the message history.

## Human observation

- The human CLI provides one live TUI over all repository, channel, and direct message events in the shared database.
- Observation loads all unexpired history before following new message events.
- Observation never creates or claims inbox deliveries and is unavailable to agent harness identities.
- Terminal control sequences in peer-authored fields are removed before rendering.

## Agent guidance

- MCP instructions encourage agents to announce before changing shared files, interfaces, schemas, migrations, configuration, generated outputs, broad refactors, or other likely overlap.
- Tool descriptions distinguish repository, channel, and direct scopes and explain their recipient semantics.
- Tool descriptions direct replies through the delivered event's `reply_to` route.
- The manual inbox tool identifies itself as recovery and diagnostics only.
- Agents clear work intents when work is complete, abandoned, or handed off.

## Retention

- Message TTL is one global, human-owned setting.
- Agents cannot choose or override TTL per message.
- Trailwire accepts retention from one hour through 30 days.
- Hooks, database-backed CLI commands, and MCP calls opportunistically remove expired messages, work intents, and short-lived MCP call correlations.

## Configuration and migration

- `trailwire init` configures Claude Code, Codex, and Cursor without removing unrelated hooks or MCP servers.
- Config v1 upgrades automatically to config v2.
- Database migration is additive and preserves v0 messages, recipients, claims, agents, presence, channels, voluntary memberships, and intents.
- Human-required channel policy is stored in config and mirrored into SQLite.
- Session bindings are stored in SQLite so the config does not grow for every conversation.

## Distribution and observability

- The CLI uses the latest stable urfave/cli release selected by the module.
- Releases use Quill and GoReleaser.
- GoReleaser publishes the Homebrew cask to `NerdsWhoFish/homebrew-tap`.
- OpenTelemetry is disabled by default and activates only through explicit configuration.
- Telemetry excludes message bodies, repository paths, prompts, session IDs, and agent identifiers.
