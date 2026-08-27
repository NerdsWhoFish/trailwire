# Contributing

Trailwire welcomes focused issues and pull requests.

## Development setup

Trailwire requires Go 1.25 or newer. Clone the repository, then run:

```sh
make test
make build
```

Tests use temporary SQLite databases and disposable harness configuration directories. They do not modify your real Claude Code, Codex, or Cursor configuration.

## Pull requests

- Keep repository broadcasts and channels as separate concepts.
- Keep delivery identities scoped to resumable harness conversations. A second session in the same harness must not reuse the first session's inbox.
- Preserve once-per-recipient delivery. One claim must never consume another recipient's inbox row.
- Treat required channels as human-owned delivery policy, not mutable agent membership.
- Preserve message history when adding lifecycle behavior. A recant is an event, not a delete.
- Add tests for concurrent claims, session resume, repository and channel fan-out, direct isolation, migration, reply routes, and every harness-specific output envelope.
- Keep OpenTelemetry disabled unless `TRAILWIRE_OTEL_ENABLED` is explicitly true.
- Run `make check` before opening a pull request.

Architecture changes should include an ADR under `adr/` with considered options and both good and bad consequences.
