# ADR 0003: Versioned SQL migrations instead of GORM AutoMigrate

**Status:** accepted · **Date:** 2026-11-17

## Context

GORM's `AutoMigrate` creates and alters tables from struct tags. It is convenient, but:

- it never drops or renames columns, and cannot express data migrations;
- partial indexes, CHECK constraints and expression indexes (`lower(email)`) are awkward or impossible to express;
- the schema is implicit, so reviewers cannot see what changed between releases.

## Decision

Use **golang-migrate** with plain SQL files (`NNNN_name.up.sql` / `.down.sql`) per service. The files are embedded into the binary with `//go:embed` and applied at startup (`pkg/database.Migrate`). GORM is used only for queries.

## Consequences

- The schema is explicit, reviewable and reversible. Week 2's `processing` status will be a small migration that alters the CHECK constraint.
- GORM struct tags must match the SQL by hand. A mismatch shows up as a runtime error, which the end-to-end checks catch.
- Running migrations at startup is fine for one replica. With many replicas, golang-migrate's advisory lock serialises them. At larger scale, run migrations as a separate deploy step (an init container or a CI job).
