# Unified Service Scheduler

A backend service for booking vehicle service appointments at a dealership. Built in Go for the Keyloop coding challenge — Scenario A, Ownership domain.

The defining engineering property of this service is **no double-booking under concurrency**, guaranteed at the database layer via a partial PostgreSQL exclusion constraint on a `tstzrange` column. Application code treats the constraint as the source of truth and retries safely on lost races; the integration test fires twenty concurrent booking requests at the same slot and asserts exactly one succeeds.

---

## Quick start

Requires Docker (and Docker Compose). Nothing else — no Go SDK, no `make`, no Postgres client on the host. 

**Run the test suite (the one-command reviewer path):**

```bash
docker compose --profile test up --build --abort-on-container-exit --exit-code-from tests
```

This brings up Postgres, applies the schema and seed, runs the unit suite (`TimeRange.Overlaps`) and the integration suite (availability + booking concurrency), and exits with the test result's exit code. Expect:

```
--- PASS: TestTimeRange_Overlaps                    (11 sub-tests)
--- PASS: TestAvailability_Available
--- PASS: TestAvailability_NoQualifiedTechnician
--- PASS: TestAvailability_AdjacentSlotIsAvailable
--- PASS: TestConcurrentBooking_NoDoubleBooking     (1 success, 19 conflicts out of 20)
PASS
tests-1 exited with code 0
```

**Run the service interactively** (Postgres + HTTP server, for manual cURL exploration):

```bash
docker compose up --build
```

The service listens on `http://localhost:8080`. Stop with `Ctrl+C`, clean up with `docker compose down -v`.

---

## What this service does

Given a customer, vehicle, dealership, service type, and desired start time, the service:

1. Resolves the service type to its duration and required skill.
2. Computes the half-open booking window `[start, start + duration)`.
3. Checks that **both** a free service bay **and** a free qualified technician exist for the entire window in that dealership.
4. Persists a confirmed `appointment` row linking customer, vehicle,    technician, and bay — in a single transaction protected by a database-level exclusion constraint that makes overlapping bookings impossible.

A second endpoint exposes the availability check on its own, without committing a booking — useful for UI flows that want to show free slots before asking the user to confirm.

---

## The no-double-booking guarantee

Naive "SELECT to check availability, then INSERT" has a time-of-check / time-of-use race: two requests can both see a resource as free and both insert. The design closes this with a database-enforced guarantee.

The `appointment` table carries two partial exclusion constraints using the `btree_gist` extension:

```sql
CONSTRAINT no_bay_overlap
  EXCLUDE USING gist (service_bay_id WITH =, during WITH &&)
  WHERE (status = 'confirmed'),

CONSTRAINT no_technician_overlap
  EXCLUDE USING gist (technician_id  WITH =, during WITH &&)
  WHERE (status = 'confirmed')
```

PostgreSQL itself refuses to admit two confirmed rows that share a bay (or technician) over an overlapping `tstzrange` window. The application's booking flow selects a candidate bay and a candidate qualified technician, then attempts the insert. If the insert raises a retryable SQLSTATE (`23P01` exclusion_violation, `40P01` deadlock_detected, `40001` serialization_failure), the booking re-selects candidates and tries again (bounded retries). If no candidate remains, the API returns `409`.

The SELECT is an optimization for picking candidates; the exclusion constraint is what makes the check-then-act race unwinnable. See [`SYSTEM_DESIGN.md`](./SYSTEM_DESIGN.md) §6 for the full design rationale, including the considered alternative (`SELECT … FOR UPDATE SKIP LOCKED`).

---

## API

The full machine-readable contract is in [`openapi.yaml`](./openapi.yaml) (OpenAPI 3.0.3). Three endpoints carry the business logic; `/healthz` and `/readyz` are standard probes.

### Create an appointment

```bash
curl -s -X POST localhost:8080/api/v1/appointments \
  -H 'Content-Type: application/json' \
  -d '{
    "customer_id":"22222222-2222-2222-2222-222222222222",
    "vehicle_id":"33333333-3333-3333-3333-333333333333",
    "dealership_id":"11111111-1111-1111-1111-111111111111",
    "service_type_id":"88888888-8888-8888-8888-888888888888",
    "desired_start":"2026-06-01T09:00:00Z"
  }'
```

Returns `201` with the confirmed appointment (bay, technician, start, end, status). Returns `409` if no free bay and/or qualified technician exists for the window.

### Check availability without booking

```bash
curl -s "localhost:8080/api/v1/dealerships/11111111-1111-1111-1111-111111111111/availability?service_type_id=88888888-8888-8888-8888-888888888888&start=2026-06-01T11:00:00Z"
```

Returns `200` with either `{ "available": true, "window": {...}, ... }` or
`{ "available": false, "reason": "no_bay" | "no_qualified_technician" | "no_bay_and_no_technician", "window": {...}, ... }`.

### Fetch an appointment

```bash
curl -s localhost:8080/api/v1/appointments/{id}
```

### Seed data used by the examples

The Postgres init scripts in [`migrations/`](./migrations) load a single dealership with two bays and two technicians, one qualified for *brakes* (Alice) and one for *engine* (Bob), plus two service types (Brake Service 60min, Engine Diagnostic 90min). Brake Service is intentionally served by a single technician — this is what makes the concurrency test deterministic.

---

## Tests

Two layers:

**Unit tests** (`internal/domain/domain_test.go`) — 11 table-driven cases for `TimeRange.Overlaps`, documenting the half-open interval semantics. The adjacent cases (one range ends exactly when the other begins) explicitly expect `false`, codifying the back-to-back-is-not-conflict rule that the booking layer relies on.

**Integration tests** (`test/`, build tag `integration`) — require Postgres, run automatically inside the test container under `docker compose --profile test`:

- `TestAvailability_Available` — happy path
- `TestAvailability_NoQualifiedTechnician` — discriminates between the three unavailability reasons
- `TestAvailability_AdjacentSlotIsAvailable` — books `[09:00, 10:00)`, then checks `10:00` is free; proves the half-open `[)` convention end-to-end   through the HTTP layer
- `TestConcurrentBooking_NoDoubleBooking` — **the centerpiece.** Fires 20   concurrent booking requests at the same slot served by Alice (the only   qualified brake technician). Asserts exactly **one** `201` and **nineteen** `409`. Proves the no-double-booking guarantee under realistic contention.

---

## Project layout

```
cmd/scheduler/main.go         Wiring: pgxpool, slog, HTTP server bootstrap
internal/
  api/                        chi router, handlers, middleware, response helpers
  service/booking.go          BookingService.Book + CheckAvailability
  domain/                     Entities, TimeRange value object, overlap predicate (+ unit tests)
  repository/                 Postgres access via pgx; FindFreeBay/FindFreeTechnician;
                              BookAppointment with bounded retry on 23P01/40P01/40001
  config/                     12-factor config loader with defaults
migrations/
  0001_init.sql               Schema + btree_gist + exclusion constraints
  0002_seed.sql               One dealership, two bays, two technicians, two service types
test/                         Integration tests (build tag `integration`)
openapi.yaml                  OpenAPI 3.0.3 API contract
SYSTEM_DESIGN.md              Architecture, design rationale, deferred work
notes.md                      Raw AI collaboration notes captured task-by-task
Dockerfile / Dockerfile.test  Production binary + test runner images
docker-compose.yml            One-command run and one-command test
```

---

## AI Collaboration Narrative

This challenge required GenAI as an essential collaborator. The relevant question is therefore not whether AI wrote the code, but how the AI was *directed*, *validated*, and *taken ownership of*. This section explains that workflow honestly. The raw, task-by-task account is in [`notes.md`](./notes.md); what follows is the distilled story.

### Workflow

The repository ships a [`CLAUDE.md`](./CLAUDE.md) at the root that encodes
the system's hard invariants — half-open `[)` intervals, the exclusion constraint as the source of truth, reuse of repository methods rather than duplicated SQL, structured logging conventions. Claude Code reads this file at the start of every session, so the agent is grounded in the same invariants on every prompt rather than re-deriving them.

For each task (the availability endpoint, the `TimeRange.Overlaps` unit suite, the OpenAPI specification) the prompt followed a fixed shape:

1. **Required reading first** — the actual source files relevant to the task, so the agent extracted field names and types from `json:"..."` tags and real handler code rather than inferring from English.
2. **Hard rules** — invariants the agent could not violate without surfacing the disagreement (no duplicated SQL, half-open intervals preserved, shared error schema, etc.).
3. **Verification checklist** — the exact commands the agent had to run    before declaring success, with the expected outputs.

### Three tasks, three different outcomes

The three feature tasks produced three different kinds of AI output, in ways that map onto how tightly each task was specified:

- **Availability endpoint.** The AI made *consistency* mistakes that tests did not catch. The first draft declared a `windowShape` struct locally   inside the handler, breaking the `appointmentView` pattern already   established in `internal/api/response.go`. It also omitted structured
  logging from `CheckAvailability` while `Book` was fully logged — an   observability asymmetry. Both were caught on code review (not on green   tests) and corrected as separate `refactor:` commits, keeping the   history honest about the build → review → improve loop.

- **`TimeRange.Overlaps` unit tests.** The AI made no mistakes. The spec   was tight, the surface area small, and the cases enumerated by name in   the prompt — including the adjacent-half-open cases that were the most   likely place for an error. Prompt precision mapped directly to output   correctness.

- **OpenAPI spec.** The AI made no functional mistakes and made one   judgment call that deserves a flag. It chose a single-schema availability   response with nullable `reason` over `oneOf`, and explained why in the   schema description: OpenAPI 3's `discriminator` keyword requires a   *string*-typed property, but the discriminating field here (`available`)   is a boolean, so a `oneOf` would have been awkward. That was the right   call and the right place to document the reasoning. The risk on this task was contract drift from code; it was mitigated by forcing source reading first and by an end-to-end curl verification at the end.

### The bugfix

After completing the second task I re-ran the full integration suite as a sanity check — a habit I want to call out because it paid off. The concurrency test failed with:

```
ERROR: deadlock detected (SQLSTATE 40P01)
```

The exclusion constraint had still done its job: no double-booking occurred. But the retry loop only caught `23P01` (exclusion_violation), not `40P01` (deadlock_detected) — Postgres had won the conflict race a different way, killing the losing transaction in a lock cycle rather than refusing the row on constraint check. Critically, the failure was non-deterministic: the test had passed cleanly multiple runs before, and a flaky integration test that hides a real error-handling gap is a worse smell than a deterministic failure. I extended the retry handler to cover `40P01` and `40001` (serialization_failure, preemptive for any future
SERIALIZABLE isolation), ran the concurrency test five times in a row to confirm stability, and committed the fix as its own `fix:` commit so the narrative survives in git history.

This was the moment that most reframed my thinking about AI-assisted code: correctness mechanisms are only as good as the error-handling surface around them, and that surface is exactly where AI-generated code is likeliest to be incomplete. Sanity-check runs are not formality.

### One more correction worth flagging

 
The first draft of `SYSTEM_DESIGN.md` (also AI-drafted) described an observability stack — Prometheus `/metrics`, OpenTelemetry tracing, dedicated `internal/observability/` and `internal/transport/http/` directories — that the implementation did not actually contain. Late in the process I ran a second-pass review using a fresh AI session — feeding it the existing `SYSTEM_DESIGN.md` and the current code tree, and asking it to flag mismatches between the two. That pass surfaced the gap, which I had missed because the document and the original prompt that generated it shared the same author and the same assumptions. The fix was to align the document with the code (correct directory names, the actual middleware in use) and to add an explicit *Implemented vs. Deferred* section that names what's deferred (Prometheus, OTel, correlation ID propagation into log records), why (timebox discipline, keeping the centerpiece tight), and what I would add first in a next iteration.
 
The lesson generalised: a single AI session is consistent with its own prior reasoning and can drift from reality together with the human collaborator. Using a second AI session as a fresh-eyes reviewer — same codebase, no prior context — is the cheapest way to catch this class of mistake. The honest gap is more credible than the inflated story.

### What I'd say in one sentence

The most useful prompts were not the longest — they were the ones that left no room for the AI to invent shape where shape was already determined by code, and the most useful verification step was not "tests pass" but "this artifact still works against the running system."

---

## Trade-offs and what was deliberately deferred

Within a one-week timebox the implementation focuses on the concurrency correctness centerpiece and the supporting integration test. The following are designed-for but not implemented; they would be the first additions in a next iteration, and the rationale is in [`SYSTEM_DESIGN.md`](./SYSTEM_DESIGN.md) §10:

- **Prometheus `/metrics`** — histograms for booking latency; counters for   `23P01`/`40P01` retry events and for `409` rejections. Retry rate is the single most informative signal for contention hotspots.
- **OpenTelemetry tracing** — a child span per repository call, so a slow   `FindFreeBay` or a retry storm is visible end-to-end.
- **Correlation ID propagation into log records.** The chi `RequestID`   middleware sets the ID into the request context but `slog` is not currently   wired to read it per-log-line. A small follow-up — a `slog.Handler` wrapper that pulls the ID from `request.Context()`.
- **A graceful shutdown path** in `cmd/scheduler/main.go` (SIGTERM →   `server.Shutdown`). The current binary exits on signal; in a real deployment the in-flight booking transaction should be allowed to commit or roll back cleanly before the container terminates.

---

## Documents in this repo

- [`SYSTEM_DESIGN.md`](./SYSTEM_DESIGN.md) — architecture, data model, concurrency strategy, alternatives considered, testing strategy, what's deferred
- [`openapi.yaml`](./openapi.yaml) — OpenAPI 3.0.3 contract for all endpoints
- [`notes.md`](./notes.md) — task-by-task AI collaboration notes, the unedited record this README's narrative section is distilled from
- [`CLAUDE.md`](./CLAUDE.md) — the invariants file the AI agent reads at every session start