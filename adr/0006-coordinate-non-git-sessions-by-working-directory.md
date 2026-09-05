# 6. Coordinate non-Git sessions by working directory

Date: 2026-09-05

## Status

Accepted.

## Context and Problem Statement

Repo-scoped messages currently fail when a harness starts outside a Git repository.
Hooks, MCP, and CLI commands need the same automatic coordination boundary for ordinary directories.

## Considered Options

1. Fall back to the canonical working directory
2. Broadcast non-Git repo messages globally
3. Require a named channel or a Git repository

## Decision Outcome

Chosen: **option 1**.

Keep the existing repo scope and Git identity rules. If Git discovery fails, hash the absolute, symlink-resolved working directory under a separate directory: prefix. Reuse the existing presence and per-recipient delivery machinery across hooks, MCP, and CLI. Parent, child, and sibling non-Git directories remain separate; named channels and announcements cover broader coordination. Reject missing paths and files instead of inventing a scope.

## Consequences

### Good

- Ordinary directories and hosts without Git can coordinate without setup.
- Git remotes and linked worktrees keep their existing routing.
- Symlink aliases share a scope without exposing full paths in target IDs.

### Bad

- Non-Git parent and child directories do not automatically coordinate.
- Moving a directory or initializing Git changes its coordination identity.
- Git discovery failures fall back to directory routing and can narrow delivery during a Git failure.

### Rejected because

- Global fallback would send local file coordination to unrelated sessions.
- Requiring a channel or Git preserves the setup requirement the user asked to remove.
