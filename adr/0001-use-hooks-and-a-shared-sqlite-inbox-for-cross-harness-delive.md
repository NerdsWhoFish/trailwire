# 1. Use hooks and a shared SQLite inbox for cross-harness delivery

Date: 2026-08-26

## Status

Accepted. The stable per-harness agent identity is superseded by [ADR-0002](0002-bind-delivery-identities-to-resumable-harness-sessions.md).

The shared SQLite inbox, hook delivery, MCP publishing, repository addressing, and security model remain accepted.

## Context and Problem Statement

Codex, Claude Code, and Cursor need to exchange coordination messages while working in the same repository.
MCP resource subscriptions are not portable enough to guarantee that a host injects new content into a live turn.
The first release must be local-first, installable as one Go binary, and require no daemon.

## Considered Options

1. Use a shared SQLite database, stdio MCP tools for publishing, and harness hooks for delivery
2. Use MCP resource subscriptions as the delivery mechanism
3. Run a local HTTP daemon that pushes messages to harness integrations
4. Store coordination messages in files inside each Git repository

## Decision Outcome

Each configured harness gets a stable Trailwire agent identity. MCP tools publish messages and manage explicit channel membership. Hooks run at supported prompt and tool lifecycle points, update presence, and atomically claim unread messages for injection into the harness. Repository broadcasts are addressed to a canonical repository identity and are independent from named channels. Direct messages target an agent identity. SQLite runs in WAL mode with a busy timeout so short-lived hook and MCP processes can safely share one database. Subscription notifications may be added as an optimization, but never as the correctness path.

## Consequences

### Good

- One binary and one local database are enough to install and operate Trailwire
- Delivery works even when a harness does not support MCP resource subscriptions
- Repository broadcasts require no channel setup while channels remain reusable across repositories
- Atomic claims prevent the same inbox item from being injected repeatedly

### Bad

- All communicating harnesses must run as the same operating-system user or share the database path explicitly
- Every configured hook adds a small SQLite open and query to lifecycle events
- A stable per-harness identity does not distinguish two concurrent sessions of the same harness unless the native session ID is available
- SQLite does not provide communication across machines without a future transport

### Rejected because

- Chosen because it matches the local-first and no-daemon constraints while providing portable delivery.
- Rejected because hosts control whether subscribed resources enter model context, and current harness support is inconsistent.
- Rejected because it adds process supervision, ports, authentication, and failure modes that the local-only first release does not need.
- Rejected because messages would dirty worktrees, require merge behavior, and leak coordination data into project history.
