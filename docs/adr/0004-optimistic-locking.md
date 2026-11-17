# ADR 0004: Optimistic locking with a version column

**Status:** accepted · **Date:** 2026-11-17

## Context

Several recruiters can edit the same job at the same time (PRD: "concurrent recruiter activities"). Without protection, the last write silently overwrites the earlier one (a lost update). A publish can also race with an edit to requirements.

## Decision

- Each aggregate root (`jobs`, `candidates`) has `version INT`. Every write sets `version = version + 1`.
- Clients send the `version` they read. The repository runs `UPDATE … WHERE id = ? AND version = ?`. Zero rows updated means 409 CONFLICT (or 404 if the row is gone).
- Changes to child rows (requirements, stages, skills…) also bump the parent version, so the aggregate is versioned as a whole.
- Status transitions also guard on the current status: `WHERE status = 'draft' AND version = ?`.

## Why not pessimistic locks (`SELECT … FOR UPDATE`)?

Edits are rare compared with reads, and a human sits between the read and the write. Holding a row lock across that gap is impossible, and for short transactions it adds blocking. Optimistic locking costs nothing when there is no conflict and makes conflicts explicit to the client.

## Consequences

- Clients must handle 409 by reloading and retrying (or showing a merge UI).
- `version` is part of the API contract.
