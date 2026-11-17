# ADR 0001: Microservices in a single Go module (monorepo)

**Status:** accepted · **Date:** 2026-11-17

## Context

SmartHire must scale job publishing, applications and analytics independently (PRD: hiring spikes, tens of thousands of concurrent applications). The assignment asks for clear separation of concerns between domain components. The team is one engineer learning the stack over 3 weeks.

## Decision

- Split the system into **independently deployable services**: `job-service` and `candidate-service` in Week 1, more later. Each service has its own binary, container, port and database.
- Keep all services in **one repository and one Go module** (`github.com/usamatajemumba/smart-hire`).
- Enforce boundaries with Go's `internal/` rule: `services/job-service/internal/...` cannot be imported by candidate-service. Shared code lives only in `pkg/`, which must stay generic (no domain types).

## Consequences

Good:

- One `go test ./...` and one `go.sum`. Refactors that touch `pkg/` are atomic.
- The services are still separately built (`--build-arg SERVICE=`), deployed and scaled.

Bad / watch out:

- All services share dependency versions. Upgrading GORM upgrades it everywhere.
- `pkg/` can grow into a "shared kernel" that couples services. Keep it to infrastructure helpers.

## Alternatives considered

- **Modular monolith.** Simpler. But Week 2–3 (separate Temporal and Kafka workers, independent scaling) would force a split later anyway.
- **Multi-module (`go.work`) or polyrepo.** Each service gets its own versions, at the cost of more release overhead. This is the right move if separate teams own the services.
