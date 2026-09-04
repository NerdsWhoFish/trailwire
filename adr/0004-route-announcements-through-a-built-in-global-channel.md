# 4. Route announcements through a built-in global channel

Date: 2026-09-04

## Status

Accepted.

## Context and Problem Statement

The announce command currently requires a Git repository, broadcasts only to repository peers, and creates repository-scoped work intents.
Agents also accumulate permanently in listings even though repository activity already uses a 24-hour inactivity window.
Announcements need to work from any session and reach active agents across repositories without opt-in or human configuration.

## Considered Options

1. Reserve a built-in announcements channel and automatically subscribe every active agent
2. Keep repository-scoped announcements and work intents
3. Require humans to configure announcements as an ordinary forced channel
4. Add a new announcement target kind beside repository, channel, and direct delivery

## Decision Outcome

Chosen: **option 1**.

Reserve `announcements` as a built-in channel. `trailwire announce` and `trailwire_announce` publish directly to it without repository discovery, paths, per-announcement TTL, or work intents. The channel is always present and cannot be left or removed through forced-channel configuration.

An announcement resolves every non-human agent seen within the existing 24-hour activity window, excluding the sender. Agent listings use the same activity window and omit the human CLI identity. Historical agent rows and session bindings remain stored so resumed conversations keep their identity and exact direct addressing still works.

Repository-scoped coordination remains available through `trailwire send --repo` and repo-scoped `trailwire_send`.

## Consequences

### Good

- Announcements work from sessions that are not inside a Git repository
- Every active agent receives each announcement event once without joining or configuration
- The existing channel, inbox, modification, recant, reply-route, and retention machinery is reused
- Agent listings and announcement recipient counts no longer include indefinitely stale session identities
- Resumable session bindings remain intact instead of being deleted for presentation cleanup

### Bad

- Announcements have a wider blast radius, so noisy messages reach unrelated active work
- An agent inactive for more than 24 hours does not receive announcements sent while it was inactive
- The command removes repository-specific paths, temporary work intents, and the separate done operation
- The announcements channel is reserved product policy rather than ordinary human-configurable channel policy

### Rejected because

- Repository scope is the coupling being removed: it fails outside Git and misses agents working elsewhere
- A configured forced channel is not automatic on a fresh install and lets configuration accidentally remove the product-wide announcement path
- A new target kind would duplicate channel fan-out, inbox, observation, modification, recant, and reply behavior without adding distinct semantics
