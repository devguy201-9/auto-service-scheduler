package domain

import (
	"testing"
	"time"
)

// TestTimeRange_Overlaps exercises the half-open interval [Start, End) overlap
// semantics. Two ranges overlap iff a.Start < b.End && b.Start < a.End, which
// means ranges that merely touch at a boundary (one ends exactly where the
// other begins) do NOT overlap — back-to-back appointments must not conflict.
func TestTimeRange_Overlaps(t *testing.T) {
	// Anchor every case to a single realistic day for clarity.
	at := func(hour, min int) time.Time {
		return time.Date(2026, 6, 1, hour, min, 0, 0, time.UTC)
	}

	tests := []struct {
		name string
		a    TimeRange
		b    TimeRange
		want bool
	}{
		{
			// Same start and end: every point is shared, so they overlap.
			name: "identical_ranges",
			a:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			b:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			want: true,
		},
		{
			// B is entirely contained within A's interval.
			name: "complete_overlap_b_inside_a",
			a:    TimeRange{Start: at(9, 0), End: at(12, 0)},
			b:    TimeRange{Start: at(10, 0), End: at(11, 0)},
			want: true,
		},
		{
			// A is entirely contained within B's interval (symmetric containment).
			name: "complete_overlap_a_inside_b",
			a:    TimeRange{Start: at(10, 0), End: at(11, 0)},
			b:    TimeRange{Start: at(9, 0), End: at(12, 0)},
			want: true,
		},
		{
			// B begins during A and ends after A — partial overlap on A's tail.
			name: "partial_overlap_b_starts_inside_a",
			a:    TimeRange{Start: at(9, 0), End: at(11, 0)},
			b:    TimeRange{Start: at(10, 0), End: at(12, 0)},
			want: true,
		},
		{
			// A begins during B and ends after B — symmetric partial overlap.
			name: "partial_overlap_a_starts_inside_b",
			a:    TimeRange{Start: at(10, 0), End: at(12, 0)},
			b:    TimeRange{Start: at(9, 0), End: at(11, 0)},
			want: true,
		},
		{
			// A finishes well before B begins — disjoint with a gap.
			name: "disjoint_a_before_b_with_gap",
			a:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			b:    TimeRange{Start: at(11, 0), End: at(12, 0)},
			want: false,
		},
		{
			// B finishes well before A begins — symmetric disjoint with a gap.
			name: "disjoint_b_before_a_with_gap",
			a:    TimeRange{Start: at(11, 0), End: at(12, 0)},
			b:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			want: false,
		},
		{
			// Half-open: A=[09:00,10:00) excludes 10:00, so it does not overlap
			// B=[10:00,11:00). Adjacent/touching, never conflicting.
			name: "adjacent_a_ends_when_b_starts",
			a:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			b:    TimeRange{Start: at(10, 0), End: at(11, 0)},
			want: false,
		},
		{
			// Half-open: symmetric adjacency — B ends exactly when A starts, so
			// the shared boundary point belongs to neither interval's overlap.
			name: "adjacent_b_ends_when_a_starts",
			a:    TimeRange{Start: at(10, 0), End: at(11, 0)},
			b:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			want: false,
		},
		{
			// A=[09:00,10:00), B=[09:59,11:00): one shared minute, so they overlap.
			name: "one_minute_overlap_a_ends_during_b",
			a:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			b:    TimeRange{Start: at(9, 59), End: at(11, 0)},
			want: true,
		},
		{
			// Symmetric single-minute overlap — B's tail minute falls inside A.
			name: "one_minute_overlap_b_ends_during_a",
			a:    TimeRange{Start: at(9, 59), End: at(11, 0)},
			b:    TimeRange{Start: at(9, 0), End: at(10, 0)},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Overlaps(tc.b); got != tc.want {
				t.Errorf("Overlaps(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
