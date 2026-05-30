# System Design Document — Unified Service Scheduler

**Scenario:** A (Ownership domain) · **Layer implemented:** Backend · **Language:** Go

---

## 1. Overview

A backend service — the **Service Scheduler** — that replaces manual booking. A user requests a service appointment for a specific vehicle, service type, dealership, and desired start time. Before confirming, the system checks that **both** a service bay **and** a qualified technician are free for the **entire** service duration, then persists a confirmed appointment linking the customer, vehicle, technician, and bay.

The defining engineering problem is **concurrency-safe resource-constrained booking**:
under concurrent requests for the same slot, the system must never double-book a bay or a technician, while still serving every request it safely can.

---

## 2. Scope & Assumptions

- **Backend only.** The client/frontend is stubbed via an OpenAPI contract and cURL
  examples.
- A **ServiceType** has a fixed `duration` and a `required_skill`. A technician is "qualified" if they hold that skill.
- Bays and technicians belong to a **dealership**; booking is scoped within a dealership.
- An appointment occupies `[start, start + duration)` — a **half-open** interval, so a slot
  ending at 10:00 and one starting at 10:00 do **not** conflict.
- The system assigns *any* free qualified technician and *any* free bay (no preference
  logic). Assignment strategy is documented as an extension point.
- Cancelled appointments do not block resources.

---

## 3. Architecture

```
   Client (OpenAPI contract + cURL harness)
        │ POST /api/v1/appointments
        ▼
 ┌───────────────────────────────────────────────────────┐
 │                 SERVICE SCHEDULER (Go)                  │
 │  ┌───────────┐   ┌────────────────┐   ┌─────────────┐  │
 │  │ HTTP layer│──▶│ BookingService │──▶│ Repository  │  │
 │  │ (chi)     │   │ availability + │   │ (pgx)       │  │
 │  │ validate  │   │ transactional  │   │ TX +        │  │
 │  │ request   │   │ booking        │   │ conflict    │  │
 │  └───────────┘   └────────────────┘   │ detection   │  │
 │                                       └──────┬──────┘  │
 └──────────────────────────────────────────────┼────────┘
                                                 ▼
                          ┌──────────────────────────────────────┐
                          │              PostgreSQL                │
                          │ customers · vehicles · dealerships     │
                          │ service_bays · technicians · skills    │
                          │ technician_skills · service_types      │
                          │ appointments                           │
                          │                                        │
                          │ EXCLUDE USING gist constraints:        │
                          │   no overlapping bay, no overlapping   │
                          │   technician  (source of truth)        │
                          └──────────────────────────────────────┘
```

---

## 4. Components

- **HTTP layer (`internal/transport/http`)** — chi routing, request validation, a
  correlation-ID middleware, structured request logging, panic recovery, response shaping, and `/healthz` · `/readyz` · `/metrics`.
- **BookingService (`internal/service`)** — the application core. Resolves the service
  type (duration + required skill), finds a candidate free bay and a free qualified
  technician for the requested window, performs the booking transaction, and translates a
  lost race or no-availability into the right outcome.
- **Domain (`internal/domain`)** — entities and a `TimeRange` value object; pure rules:
  interval overlap and technician qualification.
- **Repository (`internal/repository`)** — Postgres via `pgx`: availability queries, the
  transactional insert, conflict detection, and seed data.
- **Observability (`internal/observability`)** — `slog`, Prometheus metrics, OTel tracing.

---

## 5. Domain Model

```
customer        (id, name)
vehicle         (id, customer_id, vin, make, model)
dealership      (id, name)
service_bay     (id, dealership_id, name)
skill           (id, name)                 -- e.g. brakes, engine, diagnostics
technician      (id, dealership_id, name)
technician_skill(technician_id, skill_id)  -- many-to-many
service_type    (id, name, duration_minutes, required_skill_id)
appointment     (id, dealership_id, customer_id, vehicle_id, service_type_id,
                 technician_id, service_bay_id, during tstzrange, status)
```

**Interval overlap rule.** Two windows `[a_start, a_end)` and `[b_start, b_end)` overlap
iff `a_start < b_end AND b_start < a_end`. In Postgres this is the range overlap operator
`&&` on `tstzrange`.

---

## 6. Concurrency Strategy (the core of the design)

Naive "SELECT to check availability, then INSERT" has a **time-of-check / time-of-use
race**: two requests can both see a resource as free and both insert. The design closes
this with a database-enforced guarantee.

**Primary guarantee — Postgres exclusion constraints.** The `appointments` table carries
two partial exclusion constraints (requires the `btree_gist` extension):

```sql
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- no two confirmed appointments may share a bay over overlapping time
EXCLUDE USING gist (service_bay_id WITH =, during WITH &&) WHERE (status = 'confirmed'),
-- nor share a technician over overlapping time
EXCLUDE USING gist (technician_id  WITH =, during WITH &&) WHERE (status = 'confirmed')
```

The database itself makes double-booking **impossible**, regardless of application timing.

**Booking flow.**
1. Resolve `service_type` → `duration`, `required_skill`; compute
   `during = [desired_start, desired_start + duration)`.
2. `SELECT` a candidate free bay and a free qualified technician (excluding resources with
   an overlapping confirmed appointment).
3. `BEGIN`; `INSERT` the appointment with the chosen bay + technician + `during`.
4. If the insert raises `exclusion_violation` (SQLSTATE `23P01`) — a concurrent booking
   claimed the resource between our SELECT and INSERT — `ROLLBACK`, re-select the next candidate, and retry (bounded). If no candidate remains → `409 Conflict`.
5. `COMMIT` → `201 Created`.

The SELECT is only an **optimization** for picking candidates; the exclusion constraint is
the **source of truth** that makes the check-then-act race unwinnable.

**Alternative considered — `SELECT … FOR UPDATE SKIP LOCKED`.** Lock candidate resource
rows for the duration of the transaction, then check overlap and insert. This works (and
is the pattern used in my Shift Scheduling System project), but the overlap check is then
hand-coded in the application; the exclusion constraint instead expresses the rule
declaratively at the schema level and enforces it natively. Exclusion constraints were
chosen as the primary guarantee for that reason.

---

## 7. API Contract

```
POST /api/v1/appointments
body: { customer_id, vehicle_id, dealership_id, service_type_id, desired_start }

201 Created
{ "id": "...", "dealership_id": "...", "customer_id": "...", "vehicle_id": "...",
  "service_type_id": "...", "technician_id": "...", "service_bay_id": "...",
  "start_time": "2026-06-01T09:00:00Z", "end_time": "2026-06-01T10:00:00Z",
  "status": "confirmed" }

400 invalid request (bad VIN/ids/time)
409 no bay and/or qualified technician available for the window
```

Plus `GET /api/v1/appointments/{id}`, and optionally
`GET /api/v1/dealerships/{id}/availability?service_type_id=&start=` to expose the check
on its own. `GET /healthz` · `/readyz` · `/metrics`.

---

## 8. Persistence

Full relational schema (§5) in PostgreSQL, with foreign keys, the `btree_gist` extension,
and the two exclusion constraints from §6. Seed data (a dealership, a few bays,
technicians with varied skills, and service types) is loaded via migrations so the system
is demonstrable out of the box.

---

## 9. Technology Choices & Justification

- **Go** — clean transaction handling and explicit error paths suit a correctness-critical
  booking service; goroutines make the concurrency test (§11) trivial to write.
- **PostgreSQL** — exclusion constraints with `tstzrange` + `btree_gist` give a
  declarative, schema-level no-double-booking guarantee that few other databases express
  as cleanly. This is the deciding reason for the stack.
- **chi** — lightweight idiomatic router with middleware support.
- **pgx** — modern Postgres driver with good control over transactions and SQLSTATE codes
  (needed to detect `23P01`).
- **slog** (structured logs), **Prometheus** (metrics), **OpenTelemetry** (tracing).
- **httptest / testcontainers-go** (or a Compose Postgres) for tests; **Docker Compose**
  for a one-command run.

---

## 10. Observability Strategy

- **Logging** — structured JSON via `slog`; a correlation ID per request, propagated into
  every log line; booking outcome (confirmed / conflict) and chosen resources are logged.
- **Metrics** (`/metrics`) — booking attempts, confirmations, `409` conflicts, retry count
  on lost races, and a booking-latency histogram. The conflict and retry counters are the
  signals that reveal contention hotspots.
- **Tracing** — OpenTelemetry; a root span per request with child spans for the
  availability query and the booking transaction.

---

## 11. Testing Strategy

- **Unit** — interval overlap; qualification check; end-time computation from duration.
- **Integration** (Postgres):
  - happy path — booking succeeds, returns assigned bay + technician + end time;
  - no qualified technician → `409`;
  - no free bay → `409`;
  - two **adjacent** (non-overlapping) bookings both succeed;
  - **double-booking prevention under concurrency** — fire N parallel booking requests for
    the same overlapping window where only one bay and one qualified technician exist;
    assert **exactly one** succeeds and the rest receive `409`. This single test proves the
    core requirement and is the centerpiece of the suite.

---

## 12. GenAI Usage in the Design Phase

AI (Claude) was used as a design collaborator, not an autopilot:

- To **pressure-test** the booking concurrency — this is how the time-of-check/time-of-use
  race surfaced, and why the design relies on a database-enforced exclusion constraint as
  the source of truth rather than application-level checking alone (§6).
- To **enumerate the failure and contention cases** that became the test matrix in §11,
  including the parallel double-booking test.
- To **draft** this document, which was then reviewed and adjusted.

A key decision was made (and the first AI suggestion was overruled) by me: choosing
**exclusion constraints** as the primary guarantee over a purely application-level
`FOR UPDATE SKIP LOCKED` lock-and-check, because the former gives a hard, declarative
correctness guarantee at the schema level. The `FOR UPDATE SKIP LOCKED` approach is
retained in the document as a considered alternative with its tradeoff stated.
