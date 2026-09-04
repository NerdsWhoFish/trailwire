# 5. Name agent sessions by workspace

Date: 2026-09-04

## Status

Accepted.

## Context and Problem Statement

Trailwire session identity is correct for delivery but difficult for humans to follow because lists expose full UUIDs and names end in opaque hashes.
Using a synthetic `cli` native session ID also creates fake harness agents that cannot receive events from the real conversation's hooks.
Operators need recognizable targets without weakening resumable-session identity or collapsing concurrent agents.

## Considered Options

1. Keep stable UUID identity and add workspace-based friendly names with a short disambiguating suffix
2. Use the working directory path as the agent identity
3. Keep UUIDs and opaque hashed names as the only identifiers
4. Require humans to assign every session a unique label

## Decision Outcome

Chosen: **option 1**.

Keep the UUID and harness-native session binding as the durable database identity. Build the human-facing session name from the harness and host, the current workspace directory basename, and the existing eight-character native-session hash. Existing bound agents refresh to the friendly name when seen again.

Agent listings show active non-human sessions by default, ordered by recent activity. The CLI hides full UUIDs unless `--verbose` is requested and provides `--all` for historical diagnostics. Direct-recipient lookup accepts an exact UUID, full friendly name, or unique short suffix.

Harness-scoped CLI commands bind through the real harness session ID exported by the environment. They fail with an actionable error when no native ID exists instead of inventing a `cli` session. The human CLI keeps its ordinary `cli` identity.

## Consequences

### Good

- Humans can recognize the task or workspace before choosing a recipient
- UUID stability and once-per-recipient delivery semantics remain unchanged
- Concurrent sessions in one workspace remain distinct through the short suffix
- CLI diagnostics operate on the real conversation identity instead of a fake harness agent
- Historical identities remain available explicitly without overwhelming the default list

### Bad

- Friendly names become longer than the old harness and hash form
- A workspace rename does not rename an inactive session until that session is seen again
- Two sessions in one workspace still require the short suffix to distinguish them
- Harness-scoped CLI commands cannot impersonate an agent outside a harness session without an explicit session-ID environment variable

### Rejected because

- Paths are mutable, can disappear, and are shared by concurrent agents, so they cannot safely identify a resumable conversation
- Opaque identifiers caused the usability problem and encouraged targeting the wrong synthetic session
- Mandatory manual labels add setup and drift, and agents started through automated harnesses may never receive one
