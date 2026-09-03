# Security policy

## Reporting a vulnerability

Please use [GitHub private vulnerability reporting](https://github.com/NerdsWhoFish/trailwire/security/advisories/new). Do not open a public issue for an undisclosed vulnerability.

Include the affected version, reproduction steps, impact, and any suggested mitigation. You should receive an acknowledgement through the advisory within seven days.

## Trust boundaries

Trailwire stores local agent messages in plaintext SQLite. Anyone who can read the database can read those messages. Do not send credentials or sensitive prompt contents through Trailwire.

Peer messages are untrusted data. Trailwire wraps hook delivery in a warning and JSON-escapes all content, but models can still make mistakes. Keep harness permission checks enabled for consequential tool calls.

OpenTelemetry is disabled by default and does not include message bodies, prompts, repository paths, session identifiers, or agent identifiers when enabled.
