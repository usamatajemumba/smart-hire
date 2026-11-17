# ADR 0002: Database per service

**Status:** accepted · **Date:** 2026-11-17

## Context

Jobs and candidates change for different reasons and at different rates. If services shared tables, a schema change in one would silently break the other, and they could not be scaled or migrated independently.

## Decision

Each service owns exactly one PostgreSQL database (`smarthire_jobs`, `smarthire_candidates`). No service reads or writes another service's database. For local development, both databases live in **one Postgres container** (`deployments/postgres/init.sql`). Production would use separate instances, or at least separate roles with grants limited to their own database.

## Consequences

- There are no cross-service foreign keys. `jobs.recruiter_id` is a plain UUID. Cross-service consistency (e.g. "an application references an existing published job") will come from API calls or events in Week 2/3, not from SQL joins.
- Reports that span services (PRD analytics) need a separate read model. That is the planned use of Kafka plus MongoDB in Week 3.
- Each service runs its own migrations at startup.
