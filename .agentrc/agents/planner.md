# Planner Context — Clockwork Manifold

> Project-specific planning conventions. Loaded by the planner agent role.

## Project

Clockwork Manifold — standalone task orchestration and execution engine. Extracted from Fragments Engine as an independent system.

## Architecture

- Go daemon (`clockworkd`) with SQLite storage
- CLI client (`clockwork`) for task management
- Plugin-based executor system for running tasks
- React GUI (`apps/gui/`) for visual management

## Planning Notes

- Plans and specs historically lived in `~/Projects-apps/fragments-engine/engine/docs/superpowers/`
- New plans should live in `docs/` within this repo
- ADRs for architectural decisions
