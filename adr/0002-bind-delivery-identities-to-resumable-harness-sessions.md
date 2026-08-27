# 2. Bind delivery identities to resumable harness sessions

Date: 2026-08-26

## Status

Accepted. Supersedes [ADR-0001](0001-use-hooks-and-a-shared-sqlite-inbox-for-cross-harness-delive.md).

## Context and Problem Statement

A single machine-wide identity per harness collapses concurrent sessions into one recipient.
That makes one session claiming an inbox event suppress delivery to other visible sessions.
MCP clients expose session identity through different mechanisms, so binding must be portable without relying on activity timing.

## Considered Options

1. Bind native harness session IDs using MCP metadata or environment, with pre-tool hook correlation as the portable fallback
2. Keep one stable agent identity per harness installation
3. Infer the sender from the most recently active session in the repository

## Decision Outcome

Chosen: **option 1**.

Each resumable harness conversation is a distinct Trailwire agent. Trailwire binds the harness-native session ID to an agent row in SQLite and uses that agent ID for publishing, presence, channel membership, inbox claims, and work intents.

Hooks bind directly from their stable session or conversation ID. MCP calls bind from client metadata when available, from harness-provided environment variables when available, and otherwise from a short-lived pre-tool hook record matched to the tool call. If no trustworthy binding exists, Trailwire returns an actionable error instead of selecting a recent session.

The first session bound for each upgraded v0 harness adopts that harness's legacy agent ID. This preserves pending direct messages and channel membership for one resumable session while all later sessions receive distinct identities.

## Consequences

### Good

- Every visible repository or channel recipient gets its own inbox row and can claim it exactly once
- Resumed conversations retain identity while concurrent conversations remain independent
- Direct messages and work intents address the actual sending session
- Existing installations preserve the legacy harness identity for the first bound session

### Bad

- Session binding adds database state and harness-specific identity extraction
- Cursor requires a pre-tool hook correlation path until its MCP client supplies conversation metadata
- An MCP call without metadata, environment identity, or a matching hook record fails instead of being guessed
- Legacy pending messages can only be assigned to the first upgraded session because v0 did not record the intended session

### Rejected because

- A per-harness identity is the bug: one session's claim blocks every other session using that harness
- Most-recent-session inference races when concurrent agents call tools and can attribute messages to the wrong sender
