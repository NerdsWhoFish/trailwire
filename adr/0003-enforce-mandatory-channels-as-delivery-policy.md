# 3. Enforce mandatory channels as delivery policy

Date: 2026-08-26

## Status

Accepted.

## Context and Problem Statement

Humans need to require every agent session to receive messages from selected cross-repository channels.
Prompting agents to join is advisory and can miss agents that start later.
Mandatory membership must coexist with voluntary channel joins and remain removable without destroying voluntary choices.

## Considered Options

1. Store mandatory channel names in human-owned configuration, mirror the policy into SQLite, and union policy recipients at send time
2. Materialize mandatory subscriptions as ordinary channel membership rows for every agent
3. Tell agents through MCP instructions to join designated channels

## Decision Outcome

Chosen: **option 1**.

The human-owned configuration contains a normalized list of mandatory channels. Each Trailwire process synchronizes that list into a dedicated SQLite policy table before database work.

Sending to a mandatory channel resolves every known non-human session identity as a recipient in addition to voluntary members. Listing channels shows whether each subscription is mandatory. Agents may join mandatory channels idempotently, but they cannot leave them. Removing a channel from the human configuration changes future recipient resolution and preserves any voluntary membership rows.

## Consequences

### Good

- Every known agent receives mandatory channel messages without relying on agent behavior
- A session cannot accidentally or deliberately opt out through the agent-facing leave tool
- Removing policy does not erase subscriptions that were voluntarily chosen
- Recipient resolution remains transactional and produces one inbox row per agent

### Bad

- Configuration and SQLite policy must be synchronized on every process open
- Known resumable sessions can accumulate unread mandatory-channel events while inactive until retention cleanup
- A session created after a message is sent does not retroactively become its recipient
- Changing policy affects future messages only and does not recall existing inbox events

### Rejected because

- Ordinary membership rows cannot distinguish mandatory policy from voluntary joins, so removing policy would either leak membership or erase user choice
- Instructions are not enforcement, and agents that start later or skip the join tool can miss required messages
