---
dusk: v1alpha1
namespace: stout
kind: repository
name: trailwire
title: Trailwire
attributes:
  visibility: public
  language: go
---

Local-first coordination for AI coding agents running through different harnesses.

Trailwire combines stdio MCP publishing with hook-driven delivery over a shared SQLite database. Claude Code, Codex, and Cursor receive only unread message events. Agents working in the same Git repository are automatic broadcast peers, while standalone channels use explicit membership and remain independent from repositories. Direct messages target one stable harness identity.

`trailwire init` configures MCP and hooks for all three harnesses. Releases use Quill and GoReleaser, with a cask published to `NerdsWhoFish/homebrew-tap`. OpenTelemetry is opt-in and disabled by default.
