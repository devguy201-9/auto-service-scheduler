# AI collaboration notes

Working notes captured while building this service. These are the raw material for the AI Collaboration Narrative section of the final README — they document how AI assistance was directed and verified at each step, including issues caught during review and how they were addressed.
 
---
 
## Task 1 — Availability Check Endpoint
 
**Branch:** `feat/availability-endpoint`
**Goal:** Add `GET /api/v1/dealerships/{dealership_id}/availability` that returns whether a booking would be possible for a given service type and start time, without creating an appointment.
 
### Direction strategy
 
I keep a `CLAUDE.md` at the repo root that encodes the architectural invariants of the system — half-open intervals `[)`, the Postgres exclusion constraint as the source of truth for no-double-booking, reuse of repository methods rather than duplicating SQL. This file is read by Claude Code at the start of every
session, so the agent is grounded in the same invariants on every prompt rather than re-deriving them.
 
For this task I gave Claude Code a single detailed prompt with four parts:
 
1. **Required reading first** — `CLAUDE.md`, `SYSTEM_DESIGN.md` (sections 6 and 7), and the existing repository/service/handler files. The instruction was    explicit: *do not start coding until all are read*.
2. **Exact endpoint contract** — URL pattern, query params, full JSON response    shapes for available and unavailable cases, three distinct `reason` values (`no_bay`, `no_qualified_technician`, `no_bay_and_no_technician`), and all    400 error conditions enumerated.
3. **Hard rules** — must reuse `findFreeBay` / `findFreeTechnician` rather than    writing new SELECT queries; must preserve the half-open `[)` interval; must match existing error-helper and logging patterns.
4. **Verification checklist** — three commands the agent had to run before    declaring success, with their expected outputs.
### What the AI delivered on first pass
 
- Added `getAvailability` handler in `internal/api/handler.go` with **per-field**   400 validation (separate error message for invalid `dealership_id`, missing `service_type_id`, missing `start`, invalid timestamp). This is actually cleaner than the existing lump-sum validation in `createAppointment`.
- Added a `CheckAvailability` method to `internal/service/booking.go` that   reuses `GetServiceType`, `FindFreeBay`, and `FindFreeTechnician` — no   duplicated SQL.
- Refactored `findFreeBay` / `findFreeTechnician` to exported   `FindFreeBay` / `FindFreeTechnician`, with the corresponding update to `BookAppointment` callers. This was the correct refactor — the alternative would have been duplicating the queries, which the prompt forbade.
- Returned `AvailabilityResult` with separate `BayFree` and `TechFree`   booleans, letting the handler compute the precise reason without re-querying.
- Created `test/availability_test.go` (build tag `integration`) with three   scenarios using `httptest.NewServer`:
  - `TestAvailability_Available` — clean DB, 11:00 → 200, `available: true`
  - `TestAvailability_NoQualifiedTechnician` — book Alice at 09:00, check 09:30    (overlap) → `available: false`, `reason: "no_qualified_technician"`
  - `TestAvailability_AdjacentSlotIsAvailable` — book 09:00–10:00, check exactly
    10:00 → `available: true`. This test proves the half-open `[)` convention end-to-end through the HTTP layer.
- Each test calls a `cleanAppointments` helper at the start, so tests are   independent and can run in any order.
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
 
1. **`windowShape` struct declared locally inside `getAvailability`.** The    existing `internal/api/response.go` file holds a shared `appointmentView`    helper following the convention that view-shaping helpers live in `response.go`. The locally-declared struct broke that convention and would force copy-paste if any future endpoint needed to return a window.
   *Action:* Extracted a `windowView(domain.TimeRange) map[string]string`    helper into `response.go` and replaced the local struct with a single call    to it. Re-ran integration tests — response shape unchanged, all 4 tests    still pass. Committed as a separate `refactor:` commit so the history makes
   the review→improvement loop visible.
2. **`CheckAvailability` lacks structured logging.** The `Book` method logs    both confirmed bookings and conflicts via `slog.Info`. `CheckAvailability`
   logs nothing, which would make availability behavior invisible in    production logs while booking behavior is fully observable. Inconsistent    observability across two methods of the same service.
   *Status:* Pending. Plan is to add an `s.log.Info("availability checked", ...)`
   call at the end of `CheckAvailability` with fields matching the style of    `Book` (dealership, service_type, available, bay_free, tech_free) and    re-verify with the integration suite.
### What the AI got right unprompted (worth noting)
 
These are things I did not explicitly require but the AI chose well:
 
- Per-field 400 validation messages rather than a single "invalid input"   response, which I now think should be retrofitted to `createAppointment` too.
- Independent tests with explicit `cleanAppointments` setup — tests can be   reordered or run in isolation without breaking.
- Using `httptest.NewServer` instead of calling the handler directly with   `ResponseRecorder` — this exercises the full chi routing layer, not just the handler function.
- Picking the adjacent-slot boundary at exactly the end time (10:00, not   10:01) so the test materially proves the `[)` convention rather than trivially passing with any non-overlapping time.
---
 
## Task 2 — Unit tests for `domain.TimeRange.Overlaps`
 
**Branch:** `test/timerange-unit`
**Goal:** A table-driven unit-test suite that documents and verifies the half-open `[)` overlap semantics — particularly that back-to-back adjacent ranges do NOT overlap. These tests serve as executable documentation of the core invariant the integration tests rely on, and run in milliseconds without a  database.
 
### Direction strategy
 
The prompt enumerated 11 specific cases by name (identical, complete-overlap both directions, partial-overlap both directions, disjoint both directions, adjacent both directions, one-minute-overlap both directions). I called out the adjacent cases explicitly in the prompt: they are the half-open property under test, and the most likely place for the AI to err.
 
Hard rules required:
 
- Table-driven structure with `t.Run` sub-tests so each case is independently reportable in failure output
- Standard library only — no testify or third-party assertion helpers
- **Refuse to modify `domain.go`** if a bug is suspected. The unit tests   document intended behavior; production code is not the variable under test
- Adjacent cases must carry a comment that explicitly mentions "half-open"
- Realistic timestamps built with `time.Date(...)` rather than `time.Parse`   for clarity and determinism
### What the AI delivered
 
The first pass was clean and required no corrections:
 
- Both `adjacent_*` cases expect `false` with explicit comments referencing   the half-open `[)` convention — the single place where a wrong answer would   have been concerning
- A small `at(hour, min int)` closure anchored every case to the same date, which is cleaner than 11 repeated `time.Date(2026, 6, 1, ...)` calls
- Every overlap pattern is exercised in both directions, so the symmetry of   `Overlaps` is verified by the data itself rather than asserted separately
- A "one-minute overlap" pair beyond the minimum spec — proves the operator   works on a single shared minute, not just on coarse hour-aligned ranges
- White-box `package domain` testing, no exports widened for testing alone
- A header comment on the test function restates the overlap formula `a.Start < b.End && b.Start < a.End` and its half-open consequence, doubling as in-source documentation for future readers
### Verification I ran
 
```
go test ./internal/domain/... -v -count=1
  === RUN   TestTimeRange_Overlaps
  --- PASS: TestTimeRange_Overlaps/identical_ranges
  --- PASS: TestTimeRange_Overlaps/complete_overlap_b_inside_a
  --- PASS: TestTimeRange_Overlaps/complete_overlap_a_inside_b
  --- PASS: TestTimeRange_Overlaps/partial_overlap_b_starts_inside_a
  --- PASS: TestTimeRange_Overlaps/partial_overlap_a_starts_inside_b
  --- PASS: TestTimeRange_Overlaps/disjoint_a_before_b_with_gap
  --- PASS: TestTimeRange_Overlaps/disjoint_b_before_a_with_gap
  --- PASS: TestTimeRange_Overlaps/adjacent_a_ends_when_b_starts
  --- PASS: TestTimeRange_Overlaps/adjacent_b_ends_when_a_starts
  --- PASS: TestTimeRange_Overlaps/one_minute_overlap_a_ends_during_b
  --- PASS: TestTimeRange_Overlaps/one_minute_overlap_b_ends_during_a
  PASS
go test ./... -count=1                                  # everything green
go test ./test/... -tags=integration -count=1 -v        # see Bugfix below
```
 
### Reflection — comparing this task to Task 1
 
This is the inverse of Task 1's narrative. In Task 1 the AI made consistency mistakes I had to catch in code review (locally-declared response struct, missing structured logging); here the AI got it right on the first pass, including the case that mattered most.
 
The difference, I think, was prompt precision. Task 1 gave the AI an open design space — the spec described *what* to build but left the *how* to
inference, and it cut corners on conventions that already existed in the codebase. Task 2 enumerated exactly what each test case must contain, what helpers were allowed, and what semantic each adjacent case must demonstrate. That left no room to drift.
 
Documenting both outcomes honestly is the point: AI quality scales with the specificity of direction, and a single workflow has room for both modes — loose prompts for exploration, tight prompts when the shape of the answer is already known.
 
---
 
## Bugfix — Retry handler missed `deadlock_detected`
 
**Branch:** `fix/retry-on-deadlock`
 
### How it was found
 
After completing Task 2, I re-ran the full integration suite as a sanity check — even though Task 2 only touched `internal/domain/` and should have been independent. The concurrency test failed in a way I had not seen before:
 
```
booking_concurrency_test.go:66: unexpected error: ERROR: deadlock detected (SQLSTATE 40P01)
```
 
The exclusion-constraint correctness guarantee was intact — no double-booking occurred — but the retry loop in `BookAppointment` only caught `23P01` (`exclusion_violation`). Postgres had detected a deadlock and aborted the losing transaction with `40P01`, which the loop did not recognize, so the raw error surfaced to the service and the test reported it as `unexpected`.
 
Critically, this was non-deterministic: the same concurrency test had passed cleanly multiple times before. The failure depended on which conflict detector fired first under the specific transaction interleaving — and a flaky integration test that hides a real error-handling gap is a worse smell than a deterministic
failure would have been.
 
### Root cause
 
Two retry-safe Postgres error codes were in play, but only one was handled:
 
- `23P01` — `exclusion_violation`: my exclusion constraint refused the insert because another confirmed appointment already occupied the bay or technician for the requested window. Handled.
- `40P01` — `deadlock_detected`: Postgres detected a cycle between two   transactions holding row-level locks on each other's needed rows, picke a victim, and rolled it back. **Not handled.** Both are transient and both leave the database in a consistent state — they are exactly the kind of errors a bounded retry loop should swallow and try again on different candidate resources.
 
### Fix
 
Extended the retry switch to cover both transient SQLSTATEs, plus `40001` (`serialization_failure`) preemptively in case the isolation level is ever raised to `SERIALIZABLE`:
 
```go
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) {
    switch pgErr.Code {
    case "23P01": // exclusion_violation — another tx won the slot
        continue
    case "40P01": // deadlock_detected — Postgres killed us, safe to retry
        continue
    case "40001": // serialization_failure — retryable under SERIALIZABLE
        continue
    }
}
return domain.Appointment{}, err
```
 
### Verification
 
Ran the concurrency test five times in a row after the fix:
 
```
=== Run 1 === OK: 1 success, 19 conflicts out of 20
=== Run 2 === OK: 1 success, 19 conflicts out of 20
=== Run 3 === OK: 1 success, 19 conflicts out of 20
=== Run 4 === OK: 1 success, 19 conflicts out of 20
=== Run 5 === OK: 1 success, 19 conflicts out of 20
```
 
Stable. Full integration suite (4 tests) and the unit suite both pass.
 
### What I took away
 
This was the kind of issue a single test run won't surface. The value of re-running the integration suite after *every* change — including ones (like Task 2's pure unit tests) that should be unrelated to the failing area — is exactly catching transient bugs masquerading as flakes. This incident made the case for treating them as the primary signal that the system is still healthy, not just that the new code compiles.
 
It also reframed the original retry logic for me. The `23P01`-only handler felt complete because it matched the design document's vocabulary ("exclusion constraint as source of truth"). But Postgres has its own opinion about which error it raises first under contention, and a correctness mechanism is only as good as the error-handling surface around it.
 
 
## Task 3 — OpenAPI 3.0 specification

**Branch:** `docs/openapi-spec`
**Goal:** A complete, valid `openapi.yaml` at the repo root, documenting every endpoint registered in `handler.Routes()`. This is the formal API contract that the (mocked) client layer integrates against, and the most visible artifact besides the README for anyone evaluating the service without reading Go code.

### Direction strategy

The biggest risk with AI-generated OpenAPI specs is that they **drift from the actual code** — wire-format field names, response shapes, and error codes can all be invented if the AI infers from convention rather than reading the source. A spec that contradicts the implementation is worse than no spec.

The prompt required the AI to read `handler.go`, `response.go`, `booking.go`, and `domain.go` first, and to extract field names from the actual `json:"..."`
tags and `map[string]any` literals rather than reverse-engineering from English descriptions. Hard rules enumerated:

- snake_case field names from the wire format
- Timestamps `type: string, format: date-time`; UUIDs `type: string, format: uuid`
- A single shared `Error` schema referenced by every 4xx/5xx response
- `reason` as an explicit enum, not a free string
- Polymorphic `available: true | false` response handled with reasoning
- Examples must use the seed UUIDs from `migrations/0002_seed.sql` so the spec is immediately curl-testable
- Top-level `info` block must name both distinguishing properties of the API:
  no-double-booking and half-open intervals

### What the AI delivered

The first pass was clean and required no corrections. A few items worth highlighting because they reflect judgment, not just mechanical translation:

- **Polymorphic response choice.** The availability response has different   shapes when `available` is `true` vs `false` (the latter includes `reason`).
  The AI picked the single-schema approach with nullable `reason` over `oneOf` and explained why in the schema description:

  > *"OpenAPI/JSON Schema discriminators key off a string property, but the
  > discriminating field here (`available`) is a boolean, so a boolean-keyed
  > `oneOf` would be invalid/awkward."*

  This is technically correct — OpenAPI 3's `discriminator` keyword requires  a string property. The AI did not just pick a valid approach; it picked   the right approach and documented the reasoning where a future reader sees it.

- **Tag organization, `operationId`, example summaries.** None of these were required by the prompt. The AI added them anyway because they make the spec more useful when rendered (Swagger UI groups by tag, code generators consume   `operationId`, example summaries appear as a dropdown).

- **`info.description` matches the prompt requirement** — explicitly names   the half-open interval convention and the exclusion-constraint-based   no-double-booking guarantee, so a reader of the spec alone understands what makes this API non-trivial.

### Verification I ran

```
# Imported into https://editor.swagger.io/ — parsed cleanly, no errors panel.
# All endpoints render under their tag groups with response schemas, examples,
# and 4xx/5xx codes correctly resolved via $ref to the shared Error schema.
# (Swagger Editor's "Try it out" was blocked by browser CORS for HTTP localhost,
# unrelated to spec validity; verified separately via curl below.)

# End-to-end check: POST /appointments using the spec's request example
# against the running server.
$ curl -s -X POST localhost:8080/api/v1/appointments \
    -H 'Content-Type: application/json' \
    -d '{"customer_id":"22222222-2222-2222-2222-222222222222",
         "vehicle_id":"33333333-3333-3333-3333-333333333333",
         "dealership_id":"11111111-1111-1111-1111-111111111111",
         "service_type_id":"88888888-8888-8888-8888-888888888888",
         "desired_start":"2026-12-25T08:00:00Z"}' \
    -w "\n%{http_code}\n"

{"id":"e9f99525-65e0-4e90-8a45-ab395bc488d8",
 "dealership_id":"11111111-1111-1111-1111-111111111111",
 "customer_id":"22222222-2222-2222-2222-222222222222",
 "vehicle_id":"33333333-3333-3333-3333-333333333333",
 "service_type_id":"88888888-8888-8888-8888-888888888888",
 "technician_id":"66666666-6666-6666-6666-666666666666",
 "service_bay_id":"44444444-4444-4444-4444-444444444444",
 "start_time":"2026-12-25T08:00:00Z",
 "end_time":"2026-12-25T09:00:00Z",
 "status":"confirmed"}
201
```

Field-by-field, every key in the response body matches the `Appointment` schema in the spec — including the half-open derivation of `end_time` from `start_time + service_type.duration` (08:00 → 09:00 for the 60-minute Brake Service). The contract is faithful, not just internally consistent.

End-to-end-against-the-real-server is the verification step I trust most for an API contract. A spec that is internally valid but drifts from the implementation is the trap with AI-generated documentation; running a spec example against the running service is the cheapest way to expose that drift. If the example fails, the spec lied.

### Reflection — three tasks, three different AI failure modes (or lack thereof)

Across the three tasks, the AI produced different kinds of output — or made different kinds of mistakes — in ways that map onto the structure of each task:

- **Task 1 (availability endpoint):** AI made *consistency* mistakes — a   locally-declared response struct that broke the `appointmentView` convention, and missing structured logging that broke parity with `Book`. Tests passed; reviewer-level concerns surfaced only on code review.
- **Task 2 (unit tests for `Overlaps`):** AI made no mistakes. The spec was   tight, the surface area small, and the cases enumerated by name. Prompt
  precision mapped directly to output correctness.
- **Task 3 (OpenAPI spec):** AI made no functional mistakes and showed   judgment beyond what the prompt required. The risk was misalignment with source code, mitigated by the prompt forcing source reading first and by the end-to-end curl verification at the end.

And — separately — the post-Task-2 sanity check surfaced a real bug *in the existing code base* (the missing `40P01` retry handler). That had nothing to do with AI authorship; it was found by treating sanity-check runs as
primary signal, not as formality.

Taken together: AI assistance worked best when the task's deterministic checks (tests, valid YAML, working curl example) were defined up front and treated as the agent's stopping condition. The most useful prompts were not the longest — they were the ones that left no room for the AI to invent shape where shape was already determined by code.