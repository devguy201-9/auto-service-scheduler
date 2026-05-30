# AI collaboration notes

Working notes captured while building this service. These are the raw material
for the AI Collaboration Narrative section of the final README — they document
how AI assistance was directed and verified at each step, including issues caught
during review and how they were addressed.
 
---
 
## Task 1 — Availability Check Endpoint
 
**Branch:** `feat/availability-endpoint`
**Goal:** Add `GET /api/v1/dealerships/{dealership_id}/availability` that returns
whether a booking would be possible for a given service type and start time, without creating an appointment.
 
### Direction strategy
 
I keep a `CLAUDE.md` at the repo root that encodes the architectural invariants
of the system — half-open intervals `[)`, the Postgres exclusion constraint as
the source of truth for no-double-booking, reuse of repository methods rather
than duplicating SQL. This file is read by Claude Code at the start of every
session, so the agent is grounded in the same invariants on every prompt rather than re-deriving them.
 
For this task I gave Claude Code a single detailed prompt with four parts:
 
1. **Required reading first** — `CLAUDE.md`, `SYSTEM_DESIGN.md` (sections 6 and 7),
   and the existing repository/service/handler files. The instruction was
   explicit: *do not start coding until all are read*.
2. **Exact endpoint contract** — URL pattern, query params, full JSON response
   shapes for available and unavailable cases, three distinct `reason` values
   (`no_bay`, `no_qualified_technician`, `no_bay_and_no_technician`), and all
   400 error conditions enumerated.
3. **Hard rules** — must reuse `findFreeBay` / `findFreeTechnician` rather than
   writing new SELECT queries; must preserve the half-open `[)` interval; must
   match existing error-helper and logging patterns.
4. **Verification checklist** — three commands the agent had to run before
   declaring success, with their expected outputs.
### What the AI delivered on first pass
 
- Added `getAvailability` handler in `internal/api/handler.go` with **per-field**
  400 validation (separate error message for invalid `dealership_id`, missing
  `service_type_id`, missing `start`, invalid timestamp). This is actually
  cleaner than the existing lump-sum validation in `createAppointment`.
- Added a `CheckAvailability` method to `internal/service/booking.go` that
  reuses `GetServiceType`, `FindFreeBay`, and `FindFreeTechnician` — no
  duplicated SQL.
- Refactored `findFreeBay` / `findFreeTechnician` to exported
  `FindFreeBay` / `FindFreeTechnician`, with the corresponding update to
  `BookAppointment` callers. This was the correct refactor — the alternative
  would have been duplicating the queries, which the prompt forbade.
- Returned `AvailabilityResult` with separate `BayFree` and `TechFree`
  booleans, letting the handler compute the precise reason without re-querying.
- Created `test/availability_test.go` (build tag `integration`) with three
  scenarios using `httptest.NewServer`:
  - `TestAvailability_Available` — clean DB, 11:00 → 200, `available: true`
  - `TestAvailability_NoQualifiedTechnician` — book Alice at 09:00, check 09:30    (overlap) → `available: false`, `reason: "no_qualified_technician"`
  - `TestAvailability_AdjacentSlotIsAvailable` — book 09:00–10:00, check exactly
    10:00 → `available: true`. This test proves the half-open `[)` convention end-to-end through the HTTP layer.
- Each test calls a `cleanAppointments` helper at the start, so tests are
  independent and can run in any order.
### Verification I ran
 
```
go build ./...              # clean, no output
go test ./internal/... -count=1
  ?  internal/api          [no test files]
  ?  internal/config       [no test files]
  ?  internal/domain       [no test files]
  ?  internal/repository   [no test files]
  ?  internal/service      [no test files]
go test ./test/... -tags=integration -count=1 -v
  --- PASS: TestAvailability_Available             (0.06s)
  --- PASS: TestAvailability_NoQualifiedTechnician (0.03s)
  --- PASS: TestAvailability_AdjacentSlotIsAvailable (0.04s)
  --- PASS: TestConcurrentBooking_NoDoubleBooking  (0.08s)
  PASS    0.365s
```
 
Manual cURL spot-checks (server running locally):
- `dealership_id` = `not-a-uuid` → `400 invalid dealership_id` ✅
- missing `start` query param → `400 missing start` ✅
- happy path → `200 { "available": true, "window": {...}, ... }` ✅
### Issues I identified during review and addressed
 
Despite tests passing, two consistency issues stood out during code review:
 
1. **`windowShape` struct declared locally inside `getAvailability`.** The
   existing `internal/api/response.go` file holds a shared `appointmentView`
   helper following the convention that view-shaping helpers live in
   `response.go`. The locally-declared struct broke that convention and
   would force copy-paste if any future endpoint needed to return a window.
   *Action:* Extracted a `windowView(domain.TimeRange) map[string]string`
   helper into `response.go` and replaced the local struct with a single call
   to it. Re-ran integration tests — response shape unchanged, all 4 tests
   still pass. Committed as a separate `refactor:` commit so the history makes
   the review→improvement loop visible.
2. **`CheckAvailability` lacks structured logging.** The `Book` method logs
   both confirmed bookings and conflicts via `slog.Info`. `CheckAvailability`
   logs nothing, which would make availability behavior invisible in
   production logs while booking behavior is fully observable. Inconsistent
   observability across two methods of the same service.
   *Status:* Pending. Plan is to add an `s.log.Info("availability checked", ...)`
   call at the end of `CheckAvailability` with fields matching the style of
   `Book` (dealership, service_type, available, bay_free, tech_free) and
   re-verify with the integration suite.
### What the AI got right unprompted (worth noting)
 
These are things I did not explicitly require but the AI chose well:
 
- Per-field 400 validation messages rather than a single "invalid input"
  response, which I now think should be retrofitted to `createAppointment` too.
- Independent tests with explicit `cleanAppointments` setup — tests can be
  reordered or run in isolation without breaking.
- Using `httptest.NewServer` instead of calling the handler directly with
  `ResponseRecorder` — this exercises the full chi routing layer, not just
  the handler function.
- Picking the adjacent-slot boundary at exactly the end time (10:00, not
  10:01) so the test materially proves the `[)` convention rather than
  trivially passing with any non-overlapping time.
---
 
## Task 2 — Unit tests for `domain.TimeRange.Overlaps`
 
_(pending — fill in after task is complete)_
 
---
 
## Task 3 — OpenAPI specification
 
_(pending — fill in after task is complete)_