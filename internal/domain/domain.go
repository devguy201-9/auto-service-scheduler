package domain

import (
	"time"

	"github.com/google/uuid"
)

// TimeRange represents a half-open interval [Start, End).
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Overlaps returns true if two intervals [r.Start, r.End) and [other.Start, other.End) overlap.
// iff a.Start < b.End && b.Start < a.End.
func (r TimeRange) Overlaps(other TimeRange) bool {
	return r.Start.Before(other.End) && other.Start.Before(r.End)
}

type ServiceType struct {
	ID              uuid.UUID
	Name            string
	Duration        time.Duration
	RequiredSkillID uuid.UUID
}

type Appointment struct {
	ID            uuid.UUID
	DealershipID  uuid.UUID
	CustomerID    uuid.UUID
	VehicleID     uuid.UUID
	ServiceTypeID uuid.UUID
	TechnicianID  uuid.UUID
	ServiceBayID  uuid.UUID
	Window        TimeRange
	Status        string
}
