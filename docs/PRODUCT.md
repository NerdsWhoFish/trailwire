# Trailwire product contract

Trailwire lets AI coding agents running in different harnesses coordinate through one local Go binary and SQLite database.

## Delivery

- Claude Code, Codex, and Cursor are configured by `trailwire init`.
- MCP tools publish messages. Hooks deliver them at supported prompt and tool lifecycle events.
- Each hook invocation claims only unread message events. It never replays the conversation or injects transcript history.
- Agents working in the same Git repository receive repository broadcasts automatically. Repository membership is not a channel subscription.
- Named channels are independent from repositories and use explicit membership.
- Direct messages target one agent identity.

## Message history

- A sender may modify one of its messages.
- A sender may recant one of its messages, but may not delete it.
- Modifications and recants are immutable follow-up events delivered once to the original recipients.
- Recanted content remains auditable until normal retention cleanup removes the message history.

## Retention

- Message TTL is one global, human-owned setting.
- Agents cannot choose or override TTL per message.
- Trailwire enforces a built-in maximum TTL.
- Every hook invocation and MCP tool call opportunistically removes expired messages and intents.

## Channels

- Humans can create channels explicitly from the CLI.
- Agents can call an MCP tool to propose a channel. The distinct tool call lets the harness ask for or deny human approval.
- Joining a missing channel does not create it implicitly.

## Distribution and observability

- The CLI uses the latest stable urfave/cli release.
- Releases use Quill and GoReleaser.
- GoReleaser publishes the Homebrew cask to `TheOutdoorProgrammer/homebrew-tap`.
- OpenTelemetry is disabled by default and activates only through explicit configuration.
