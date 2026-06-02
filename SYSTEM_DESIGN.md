# System Design Document — Unified Service Scheduler

**Scenario:** A (Ownership domain) · **Layer implemented:** Backend · **Language:** Go

---

## 1. Overview

A backend service — the **Service Scheduler** — that replaces manual booking. A user requests a service appointment for a specific vehicle, service type, dealership, and desired start time. Before confirming, the system checks that **both** a service bay **and** a qualified technician are free for the **entire** service duration, then persists a confirmed appointment linking the customer, vehicle, technician, and bay.

The defining engineering problem is **concurrency-safe resource-constrained booking**: under concurrent requests for the same slot, the system must never double-book a bay or a technician, while still serving every request it safely can.

---

## 2. Scope & Assumptions

- **Backend only.** The client/frontend is stubbed via an OpenAPI 3.0 contract (`openapi.yaml`) and cURL examples in the README.
- A **ServiceType** has a fixed `duration` and a `required_skill`. A technician is "qualified" if they hold that skill.
- Bays and technicians belong to a **dealership**; booking is scoped within a dealership.
- An appointment occupies `[start, start + duration)` — a **half-open** interval, so a slot ending at 10:00 and one starting at 10:00 do **not** conflict.
- The system assigns *any* free qualified technician and *any* free bay (no preference logic). Assignment strategy is documented as an extension point.
- Cancelled appointments do not block resources (the exclusion constraint is partial on `status = 'confirmed'`).

---

## 3. Architecture (as implemented)

```
   Client (OpenAPI contract + cURL harness)
        │ POST /api/v1/appointments
        │ GET  /api/v1/appointments/{id}
        │ GET  /api/v1/dealerships/{id}/availability
        ▼
 ┌───────────────────────────────────────────────────────┐
 │                 SERVICE SCHEDULER (Go)                  │
 │  ┌────────────┐  ┌────────────────┐  ┌──────────────┐  │
 │  │ HTTP layer │─▶│ BookingService │─▶│ Repository   │  │
 │  │ chi router │  │ booking +      │  │ (pgx)        │  │
 │  │ validation │  │ availability   │  │ availability │  │
 │  │ middleware │  │ check          │  │ + INSERT +   │  │
 │  │ slog JSON  │  │                │  │ retry on     │  │
 │  │ logs       │  │                │  │ 23P01/40P01  │  │
 │  └────────────┘  └────────────────┘  └──────┬───────┘  │
 └─────────────────────────────────────────────┼──────────┘
                                                ▼
                          ┌──────────────────────────────────────┐
                          │              PostgreSQL                │
                          │ customer · vehicle · dealership        │
                          │ service_bay · technician · skill       │
                          │ technician_skill · service_type        │
                          │ appointment                            │
                          │                                        │
                          │ EXCLUDE USING gist constraints:        │
                          │   no overlapping bay,                  │
                          │   no overlapping technician            │
                          │   (source of truth)                    │
                          └──────────────────────────────────────┘
```

---

## 4. Components (as implemented)

- **`internal/api/`** — chi routing, request validation (per-field 400 responses),   middleware (`RequestID`, `Recoverer`), structured request handlers, response shaping   helpers (`appointmentView`, `windowView`), and `/healthz` · `/readyz`.
- **`internal/service/booking.go` (`BookingService`)** — the application core.
  Two operations:
  - `Book` — resolves the service type, computes the window, delegates the booking     transaction to the repository, logs the outcome via `slog`.
  - `CheckAvailability` — same window computation, queries free bay and free qualified     technician via the repository, returns an `AvailabilityResult` carrying separate     `BayFree` and `TechFree` flags so the HTTP layer can compute a precise `reason`.
- **`internal/domain/`** — entities (`ServiceType`, `Appointment`) and the `TimeRange` value object with the half-open `Overlaps` predicate. Unit-tested as documentation of the overlap semantics.
- **`internal/repository/`** — Postgres access via `pgx`: `FindFreeBay`,   `FindFreeTechnician`, `GetServiceType`, `GetAppointment`, and the transactional `BookAppointment` with bounded retry on `23P01` / `40P01` / `40001`.
- **`internal/config/`** — environment-variable loader with sensible defaults   (12-factor style).
- **`cmd/scheduler/main.go`** — wiring, `pgxpool`, `slog` JSON logger, HTTP server bootstrap.

---

## 5. Domain Model

```
customer        (id, name)
vehicle         (id, customer_id, vin, make, model)
dealership      (id, name)
service_bay     (id, dealership_id, name)
skill           (id, name)                 -- e.g. brakes, engine
technician      (id, dealership_id, name)
technician_skill(technician_id, skill_id)  -- many-to-many
service_type    (id, name, duration_minutes, required_skill_id)
appointment     (id, dealership_id, customer_id, vehicle_id, service_type_id,
                 technician_id, service_bay_id, during tstzrange, status)
```

**Interval overlap rule.** Two windows `[a_start, a_end)` and `[b_start, b_end)` overlap iff `a_start < b_end AND b_start < a_end`. In Postgres this is the range overlap operator `&&` on `tstzrange` with `[)` bounds. The unit tests for `domain.TimeRange.Overlaps` codify this rule in 11 table-driven cases.

---

## 6. Concurrency Strategy (the core of the design)

Naive "SELECT to check availability, then INSERT" has a **time-of-check / time-of-use race**: two requests can both see a resource as free and both insert. The design closes this with a database-enforced guarantee.

**Primary guarantee — Postgres exclusion constraints.** The `appointment` table carries two partial exclusion constraints (using the `btree_gist` extension):

```sql
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- no two confirmed appointments may share a bay over overlapping time
CONSTRAINT no_bay_overlap
  EXCLUDE USING gist (service_bay_id WITH =, during WITH &&)
  WHERE (status = 'confirmed'),
-- nor share a technician over overlapping time
CONSTRAINT no_technician_overlap
  EXCLUDE USING gist (technician_id WITH =, during WITH &&)
  WHERE (status = 'confirmed')
```

The database itself makes double-booking **impossible**, regardless of application timing. The integration test fires 20 concurrent booking requests at a single slot served by a single qualified technician and asserts exactly one succeeds.

**Booking flow.**
1. Resolve `service_type` → `duration`, `required_skill`; compute
   `during = [desired_start, desired_start + duration)`.
2. `SELECT` a candidate free bay and a free qualified technician (excluding resources with an overlapping confirmed appointment).
3. `INSERT` the appointment with the chosen bay + technician + `during`.
4. If the insert raises a retryable SQLSTATE — a concurrent transaction claimed a resource — re-select the next candidate and retry (bounded at 5 attempts). The handler covers three codes:
   - `23P01` (`exclusion_violation`) — the exclusion constraint refused the row
   - `40P01` (`deadlock_detected`) — Postgres killed this transaction in a lock cycle
   - `40001` (`serialization_failure`) — preemptive coverage if the isolation level
     is ever raised to `SERIALIZABLE`
5. If no candidate remains → `409 Conflict`.

The SELECT is only an **optimization** for picking candidates; the exclusion constraint is the **source of truth** that makes the check-then-act race unwinnable.

**Alternative considered — `SELECT … FOR UPDATE SKIP LOCKED`.** Lock candidate resource rows for the duration of the transaction, then check overlap and insert.
This works (and is a common pattern), but the overlap check is then hand-coded in the application; the exclusion constraint instead expresses the rule declaratively at the
schema level and enforces it natively. Exclusion constraints were chosen as the primary guarantee for that reason.

---

## 7. API Contract

```
POST /api/v1/appointments
body: { customer_id, vehicle_id, dealership_id, service_type_id, desired_start }
  201 → { id, dealership_id, customer_id, vehicle_id, service_type_id,
          technician_id, service_bay_id, start_time, end_time, status }
  400 → invalid request, unknown service type
  409 → no bay and/or qualified technician for the window

GET /api/v1/appointments/{id}
  200 → appointment
  400 → invalid id, 404 → not found

GET /api/v1/dealerships/{id}/availability?service_type_id=&start=
  200 → { available: true|false, reason?, window, dealership_id, service_type_id }
       reason ∈ { no_bay, no_qualified_technician, no_bay_and_no_technician }
  400 → invalid/missing params, unknown service type

GET /healthz · /readyz   (200, empty body)
```

The full machine-readable contract is in `openapi.yaml` (OpenAPI 3.0.3), with examples that map to the seed data so a reviewer can copy any example into a curl call against a running instance.

---

## 8. Persistence

PostgreSQL with the full relational schema in §5, foreign keys, the `btree_gist` extension, and the two exclusion constraints from §6. The schema and seed data are
loaded by Postgres at first startup via files in `/docker-entrypoint-initdb.d/` (mounted from `./migrations/`); no separate migration runner is needed for this deliverable.

---

## 9. Technology Choices & Justification

- **Go** — clean transaction handling and explicit error paths suit a   correctness-critical booking service; goroutines make the concurrency test   (§11) trivial to write.
- **PostgreSQL** — exclusion constraints with `tstzrange` + `btree_gist` give a   declarative, schema-level no-double-booking guarantee that few other databases express as cleanly. This is the deciding reason for the stack.
- **chi router** — lightweight, idiomatic, good middleware ergonomics.
- **pgx** — modern Postgres driver with first-class control over transactions and SQLSTATE error codes (needed to detect `23P01` / `40P01` / `40001`).
- **`slog`** (Go 1.26+ standard library) — structured JSON logging without a  third-party dependency.
- **Docker Compose** — Postgres + scheduler in one `docker compose up`.

---

## 10. Observability — what's implemented and what's deferred

**Implemented:**
- **Structured JSON logging** via `slog` on every request and on every booking outcome (confirmed, conflict). Availability checks are also logged with   per-resource flags.
- **Request ID middleware** (chi's `RequestID`) — every request is assigned a  unique ID stored in the request context.
- **Recoverer middleware** — panics return `500` with a stack trace in logs  rather than crashing the server.
- **Liveness/readiness probes** at `/healthz` and `/readyz`.

**Deliberately deferred** (would be the next iteration, not a bug in the current design):
- **Prometheus `/metrics` endpoint** with histograms for booking latency and  counters for `23P01` retries / `40P01` retries / `409` rejections. Booking  retry rate is the single most informative signal for contention hotspots, and is the first thing I would add after this submission.
- **OpenTelemetry tracing** with a child span per repository call so a slow  `FindFreeBay` or a retry storm is visible end-to-end.
- **Correlation ID propagation into log records.** The chi `RequestID`  middleware sets it in context but `slog` is not currently wired to read it  per-request — a small additional step (a `slog.Handler` wrapper or per-handler  `WithContext`) that I did not complete within the timebox.

The honest reason for deferral is timebox discipline: the centerpiece of this deliverable is the concurrency-safety design and its proof-by-test, and I chose to spend the budget there rather than spread thin over an observability surface the reviewer cannot exercise without a metrics-scraping setup.

---

## 11. Testing Strategy

- **Unit** — `domain.TimeRange.Overlaps` covered by 11 table-driven cases including the half-open boundary cases (adjacent ranges expected `false`).
- **Integration** (Postgres, build tag `integration`):
  - Availability: available case, no-qualified-technician case, adjacent-slot case (proves half-open `[)` through the HTTP layer)
  - Booking: **20 concurrent booking requests against a single slot served by a single qualified technician** → exactly one `201`, nineteen `409`. This is the centerpiece test — it proves the core requirement under realistic concurrent contention, and it is the test that surfaced the missing `40P01` retry handler during development.

---

## 12. GenAI Usage in the Design Phase

AI (Claude) was used as a design collaborator, not an autopilot:

- To **pressure-test** the booking concurrency — this is how the time-of-check / time-of-use race surfaced, and why the design relies on a database-enforced exclusion constraint as the source of truth rather than application-level checking alone (§6).
- To **enumerate the failure and contention cases** that became the test matrix in §11, including the parallel double-booking test.
- To **draft this document**, which was then reviewed and adjusted against the code that was actually built — the *Observability* section in particular was rewritten to reflect what is implemented versus what is deferred, rather than  what the original draft proposed.

A key decision was made (and the first AI suggestion was overruled) by me: choosing **exclusion constraints** as the primary guarantee over a purely application-level `FOR UPDATE SKIP LOCKED` lock-and-check, because the former
gives a hard, declarative correctness guarantee at the schema level. The `FOR UPDATE SKIP LOCKED` approach is retained in the document as a considered alternative with its tradeoff stated.

A separate document, `notes.md`, captures the per-task AI collaboration narrative — prompt strategies, what the AI got right and wrong, what was corrected during review, and what the experience taught me about directing AI agents. The README distills the key points from those notes into the *AI Collaboration Narrative* section the reviewer is most likely to read.