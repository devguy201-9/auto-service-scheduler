package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/devguy201-9/auto-service-scheduler/internal/domain"
)

var (
	ErrNoAvailability      = errors.New("no available bay and/or qualified technician")
	ErrServiceTypeNotFound = errors.New("service type not found")
	ErrAppointmentNotFound = errors.New("appointment not found")
)

const maxBookingRetries = 5

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) GetServiceType(ctx context.Context, id uuid.UUID) (domain.ServiceType, error) {
	const q = `SELECT id, name, duration_minutes, required_skill_id
	           FROM service_type WHERE id = $1`
	var st domain.ServiceType
	var minutes int
	err := r.pool.QueryRow(ctx, q, id).Scan(&st.ID, &st.Name, &minutes, &st.RequiredSkillID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ServiceType{}, ErrServiceTypeNotFound
	}
	if err != nil {
		return domain.ServiceType{}, err
	}
	st.Duration = time.Duration(minutes) * time.Minute
	return st, nil
}

type BookingRequest struct {
	DealershipID uuid.UUID
	CustomerID   uuid.UUID
	VehicleID    uuid.UUID
	ServiceType  domain.ServiceType
	Window       domain.TimeRange
}

// BookAppointment selects available bay and technician candidates, then performs the INSERT.
// The DB exclusion constraint serves as the final line of defense against double-booking.
// If a race condition occurs, it re-selects candidates and retries (bounded retry).
func (r *Repository) BookAppointment(ctx context.Context, req BookingRequest) (domain.Appointment, error) {
	for attempt := 0; attempt < maxBookingRetries; attempt++ {
		bayID, err := r.FindFreeBay(ctx, req.DealershipID, req.Window)
		if err != nil {
			return domain.Appointment{}, err
		}
		techID, err := r.FindFreeTechnician(ctx, req.DealershipID, req.ServiceType.RequiredSkillID, req.Window)
		if err != nil {
			return domain.Appointment{}, err
		}
		if bayID == uuid.Nil || techID == uuid.Nil {
			return domain.Appointment{}, ErrNoAvailability
		}

		appt := domain.Appointment{
			ID:            uuid.New(),
			DealershipID:  req.DealershipID,
			CustomerID:    req.CustomerID,
			VehicleID:     req.VehicleID,
			ServiceTypeID: req.ServiceType.ID,
			TechnicianID:  techID,
			ServiceBayID:  bayID,
			Window:        req.Window,
			Status:        "confirmed",
		}

		const ins = `
INSERT INTO appointment
  (id, dealership_id, customer_id, vehicle_id, service_type_id,
   technician_id, service_bay_id, during, status)
VALUES
  ($1, $2, $3, $4, $5, $6, $7, tstzrange($8, $9, '[)'), 'confirmed')`
		_, err = r.pool.Exec(ctx, ins,
			appt.ID, appt.DealershipID, appt.CustomerID, appt.VehicleID,
			appt.ServiceTypeID, appt.TechnicianID, appt.ServiceBayID,
			appt.Window.Start, appt.Window.End,
		)
		if err == nil {
			return appt, nil
		}

		// Lost the race: The selected bay or technician was taken between SELECT and INSERT.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23P01": // exclusion_violation — another transaction won the slot
				continue
			case "40P01": // deadlock_detected — Postgres killed us, safe to retry
				continue
			case "40001": // serialization_failure — also retryable for completeness
				continue
			}
		}
		return domain.Appointment{}, err
	}
	return domain.Appointment{}, ErrNoAvailability
}

func (r *Repository) FindFreeBay(ctx context.Context, dealershipID uuid.UUID, w domain.TimeRange) (uuid.UUID, error) {
	const q = `
SELECT sb.id
FROM service_bay sb
WHERE sb.dealership_id = $1
  AND NOT EXISTS (
    SELECT 1 FROM appointment a
    WHERE a.service_bay_id = sb.id
      AND a.status = 'confirmed'
      AND a.during && tstzrange($2, $3, '[)')
  )
LIMIT 1`
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, q, dealershipID, w.Start, w.End).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return id, err
}

func (r *Repository) FindFreeTechnician(ctx context.Context, dealershipID, skillID uuid.UUID, w domain.TimeRange) (uuid.UUID, error) {
	const q = `
SELECT t.id
FROM technician t
JOIN technician_skill ts ON ts.technician_id = t.id
WHERE t.dealership_id = $1
  AND ts.skill_id = $2
  AND NOT EXISTS (
    SELECT 1 FROM appointment a
    WHERE a.technician_id = t.id
      AND a.status = 'confirmed'
      AND a.during && tstzrange($3, $4, '[)')
  )
LIMIT 1`
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, q, dealershipID, skillID, w.Start, w.End).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return id, err
}

func (r *Repository) GetAppointment(ctx context.Context, id uuid.UUID) (domain.Appointment, error) {
	const q = `
SELECT id, dealership_id, customer_id, vehicle_id, service_type_id,
       technician_id, service_bay_id, lower(during), upper(during), status
FROM appointment WHERE id = $1`
	var a domain.Appointment
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&a.ID, &a.DealershipID, &a.CustomerID, &a.VehicleID, &a.ServiceTypeID,
		&a.TechnicianID, &a.ServiceBayID, &a.Window.Start, &a.Window.End, &a.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Appointment{}, ErrAppointmentNotFound
	}
	return a, err
}
