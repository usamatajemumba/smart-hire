# Week 1 learning notes: tools, concepts, and where to find them in the code

Read this alongside the code. Each section covers what the tool is, why it is here, where it is used, and the gotchas worth knowing.

---

## 1. Gin (HTTP framework)

**What:** A fast HTTP router with middleware, JSON binding and validation (built on `go-playground/validator`).

**Where:** `services/*/internal/handler/handler.go`, `pkg/middleware`, `pkg/httpx`.

**Concepts to understand:**

- `gin.New()` vs `gin.Default()`. `Default` adds Gin's own logger and recovery. We use `New()` and add our own JSON logger and recovery (`pkg/middleware`) so logs are structured.
- **Middleware chain:** `RequestID → Logger → Recovery → handler`. `c.Next()` runs the rest of the chain. Code after `c.Next()` runs after the handler (that is how the logger measures latency).
- **Binding tags:** `binding:"required,oneof=onsite remote hybrid"` on DTO structs. `c.ShouldBindJSON` decodes and validates in one step. `dive` validates each slice element.
- **Pointer fields for PATCH:** `*string` tells "not sent" (nil) apart from "sent empty" (""). Without pointers you cannot do partial updates.
- `RegisterTagNameFunc` in `pkg/httpx` makes validation errors use JSON names (`recruiter_id`) instead of Go names (`RecruiterID`).

**Gotchas:** `c.JSON` after `c.Abort…` still writes. Always `return` after sending an error. `required` on an `int` fails for `0`, so use a pointer if `0` is valid.

**Try:** add `GET /jobs/:id/stages` returning only the stages.

---

## 2. GORM (ORM) + pgx (driver)

**What:** GORM maps structs to tables and builds SQL. pgx is the Postgres driver underneath.

**Where:** `services/*/internal/repository/repository.go`, `pkg/database/postgres.go`.

**Concepts:**

- **`WithContext(ctx)` on every query.** If the HTTP client disconnects or times out, the query is cancelled too. Forgetting this is a classic leak under load.
- **Transactions:** `db.Transaction(func(tx *gorm.DB) error {...})` commits if the function returns nil and rolls back otherwise. `Repository.InTx` wraps it so the service layer never sees GORM.
- **`Updates(map[string]any)` vs `Save(struct)`.** `Save` writes every column. `Updates` with a map writes only the keys you give it, and can use `gorm.Expr("version + 1")`.
- **`RowsAffected`** is how optimistic locking detects conflicts (see ADR 0004).
- **Soft delete:** a `gorm.DeletedAt` field makes `Delete` set `deleted_at`, and every query automatically adds `WHERE deleted_at IS NULL`.
- **`Preload`** loads associations in separate queries (`SELECT … WHERE job_id IN (…)`), which avoids N+1 queries.
- **`TranslateError: true`** turns Postgres unique violations into `gorm.ErrDuplicatedKey`.
- **Connection pool:** `SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime`. Under a hiring spike the pool size, not CPU, often limits throughput.

**Gotchas:** `Updates(struct)` skips zero values (`false`, `0`, `""`), so use a map. A zero-row `Update` is not an error, so you must check `RowsAffected`.

---

## 3. PostgreSQL (schema design)

**Where:** `services/*/internal/migrations/0001_init.up.sql`.

**Concepts used:**

- **UUIDv7 primary keys** (`uuid.NewV7()`): globally unique like v4, but time-ordered, so new rows append to the index instead of splitting random pages.
- **CHECK constraints** for enums and ranges. The DB rejects bad data even if a buggy service sends it.
- **Partial indexes** (`WHERE deleted_at IS NULL`) are smaller and faster, and a **partial unique index** allows re-registering after a soft delete.
- **Expression index** `lower(email)` gives case-insensitive uniqueness without the `citext` extension.
- **`TIMESTAMPTZ`** always. We store UTC.
- **`JSONB`** for audit `changes`: a flexible shape that can still be queried (`changes->>'title'`).

**Try:** `make psql-jobs`, then:

```sql
\d jobs
SELECT action, actor_id, changes, created_at FROM audit_logs ORDER BY created_at;
EXPLAIN ANALYZE SELECT * FROM jobs WHERE status = 'published' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 20;
```

---

## 4. golang-migrate

**What:** Applies numbered SQL migrations and records the current version in a `schema_migrations` table.

**Where:** `pkg/database.Migrate`, `services/*/internal/migrations/` (embedded with `//go:embed *.sql`).

**Concepts:** up/down pairs, the "dirty" state (a migration failed halfway, which needs a manual fix and `force`), advisory locking when several replicas start together. See ADR 0003 for why we use this instead of AutoMigrate.

**Try (Week 2 preview):** add `0002_add_processing_status.up.sql` that drops and re-adds the `status` CHECK constraint with `'processing'`.

---

## 5. Docker & Docker Compose

**Where:** `deployments/docker/Dockerfile`, `docker-compose.yml`, `Makefile`.

**Concepts:**

- **Multi-stage build:** stage 1 has the Go toolchain (about 300 MB). Stage 2 is `distroless/static` (about 2 MB plus our binary), with no shell, no package manager, and a non-root user. That means a smaller attack surface.
- **Layer caching:** `COPY go.mod go.sum` and `go mod download` run *before* copying the source, so dependency downloads are cached until `go.mod` changes. `--mount=type=cache` also caches the build cache.
- **One Dockerfile, many services:** `ARG SERVICE`.
- **Healthchecks:** distroless has no `curl`, so the binary checks itself (`/app healthcheck`, see `pkg/server/healthcheck.go`). `depends_on: condition: service_healthy` makes services wait for Postgres.
- **Volumes:** `pgdata` (DB) and `resumes` (files) survive `make down`. `make reset` deletes them.
- **YAML anchors** (`x-service-env: &service-env`, `<<: *service-env`) remove repetition.
- **Compose `.env`:** `${POSTGRES_PORT:-5432}` reads `.env` if present, otherwise uses the default.

**Try:** `docker compose stop job-service` and watch `make logs`. You will see "shutdown signal received, draining connections". That is graceful shutdown at work.

---

## 6. Go standard library pieces

- **`log/slog`** (`pkg/logger`): structured logs. `log.With("service", name)` adds fields to every line.
- **`context.Context`** (`cmd/main.go`, `pkg/server`): `signal.NotifyContext` cancels a context on SIGTERM. `server.Run` waits for that cancellation and then calls `Shutdown` with a timeout. This is the base you will build on in Weeks 2–3 (timeouts, cancellation and deadlines across goroutines).
- **`embed`**: ships the SQL files inside the binary.
- **`io.LimitReader` / `http.MaxBytesReader` / `io.MultiReader`** (candidate resume upload): stream large bodies safely without loading them into memory.
- **`errors.Is` / `errors.As`** with wrapped errors (`pkg/apperr`): branch on error *type*, never on the message string.

---

## 7. Design concepts practised this week

| Concept | Where |
|---------|-------|
| Layered architecture (handler/service/repository) | each service's `internal/` |
| Dependency inversion (service depends on an interface) | `repository.Repository`, `storage.BlobStore` |
| State machine for lifecycle | `job-service/internal/domain/job.go` (`transitions`, `CanTransition`) |
| Optimistic concurrency | `UpdateJob`, `TransitionStatus` (ADR 0004) |
| Transactional audit trail | `service.audit` called inside `InTx` |
| Compensating action | resume upload deletes the blob if the DB insert fails |
| Idempotent PUT | replace-all endpoints for requirements, stages, skills |
| Defensive validation (app + DB) | binding tags, service checks, SQL CHECK/UNIQUE |
| Table-driven tests + fakes | `*_test.go` |

---

## 8. Self-check questions (for your mentor review)

1. Why does `PublishJob` include `version` in the `WHERE` clause, not just `status`?
2. What happens if two candidates register with `a@x.com` and `A@x.com` at the same millisecond? Which layer stops it?
3. Why is the audit row written in the same transaction as the change? What would go wrong otherwise?
4. Why upload the file first and insert the DB row second, rather than the other way round?
5. What breaks in resume storage if you run two replicas of candidate-service?
6. Why do the services not share one database?
7. What does `/readyz` tell a load balancer that `/healthz` does not?
