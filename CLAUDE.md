# CLAUDE.md — Unified Service Scheduler

Guidance for Claude Code working in this repository.

## What we're building

A Go backend that books vehicle service appointments. A request gives a customer, vehicle,
dealership, service type, and desired start time. Before confirming, the service must
verify that **both** a service bay **and** a qualified technician are free for the **whole**
service duration, then persist a confirmed appointment. Backend only; the client is mocked
via an OpenAPI contract + cURL. See `SYSTEM_DESIGN.md` for the full design.

## Layout (clean-ish layering)

```
cmd/scheduler/main.go          wiring, config, server bootstrap
internal/domain/               entities, TimeRange value object, overlap + qualification rules
internal/service/              BookingService: availability + transactional booking + conflict handling
internal/transport/http/       chi handlers, middleware (correlation ID, logging, recovery), routing
internal/repository/           Postgres (pgx): availability queries, transactional insert, conflict detection
internal/observability/        slog setup, Prometheus metrics, OTel tracing
migrations/                    schema + btree_gist + exclusion constraints + seed data
```

## Hard rules (do not deviate without asking me first)

1. **No double-booking is the core requirement, enforced by the database.** The
   `appointments` table uses `btree_gist` + two partial `EXCLUDE USING gist` constraints
   (bay, technician) on a `tstzrange` `during` column, `WHERE status = 'confirmed'`.
2. Booking flow: resolve service_type (duration + required_skill) -> compute the half-open
   `during` window -> SELECT a candidate free bay + free qualified technician -> INSERT in
   a transaction -> on `exclusion_violation` (SQLSTATE `23P01`) retry the next candidate
   (bounded) -> `409` if none remain.
3. Treat the exclusion constraint as the source of truth; the availability SELECT is only
   an optimization, never the sole guard against races.
4. Half-open intervals `[start, end)` — back-to-back appointments do NOT conflict.
5. Technician must hold the service type's `required_skill`.
6. Structured logging via `slog` (JSON), correlation ID per request.

## API

```
POST /api/v1/appointments  -> 201 { id, technician_id, service_bay_id, start_time, end_time, status }
                              400 invalid, 409 no bay and/or qualified technician free
GET  /api/v1/appointments/{id}
GET  /healthz  /readyz  /metrics
```

## Commands

```
make run        # or: docker compose up
make test       # unit + integration
make lint
```

## Build order — one small step at a time, run tests after each

1. migrations: schema + `btree_gist` + exclusion constraints + seed data
2. domain: entities, TimeRange, overlap + qualification rules (+ unit tests)
3. repository: availability queries + transactional insert with `23P01` detection
4. BookingService: candidate selection + booking + bounded retry on lost race (+ unit tests)
5. HTTP transport: routing, validation, middleware
6. observability: metrics, tracing
7. integration tests (happy / no-tech / no-bay / adjacent-ok / concurrent double-booking)
8. README + OpenAPI spec + cURL examples

## Tests that must exist

- **Double-booking under concurrency:** with exactly one bay and one qualified technician,
  fire N parallel booking requests for the same overlapping window; assert exactly ONE
  succeeds and the rest return `409`. This is the centerpiece — do not skip it.
- **Adjacent bookings:** two non-overlapping windows on the same resources both succeed.
