package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/devguy201-9/auto-service-scheduler/internal/domain"
	"github.com/devguy201-9/auto-service-scheduler/internal/repository"
)

type BookingService struct {
	repo *repository.Repository
	log  *slog.Logger
}

func NewBookingService(repo *repository.Repository, log *slog.Logger) *BookingService {
	return &BookingService{repo: repo, log: log}
}

type BookRequest struct {
	CustomerID    uuid.UUID
	VehicleID     uuid.UUID
	DealershipID  uuid.UUID
	ServiceTypeID uuid.UUID
	DesiredStart  time.Time
}

func (s *BookingService) Book(ctx context.Context, req BookRequest) (domain.Appointment, error) {
	st, err := s.repo.GetServiceType(ctx, req.ServiceTypeID)
	if err != nil {
		return domain.Appointment{}, err
	}

	window := domain.TimeRange{
		Start: req.DesiredStart,
		End:   req.DesiredStart.Add(st.Duration),
	}

	appt, err := s.repo.BookAppointment(ctx, repository.BookingRequest{
		DealershipID: req.DealershipID,
		CustomerID:   req.CustomerID,
		VehicleID:    req.VehicleID,
		ServiceType:  st,
		Window:       window,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNoAvailability) {
			s.log.Info("booking conflict",
				"dealership", req.DealershipID, "start", req.DesiredStart)
		}
		return domain.Appointment{}, err
	}

	s.log.Info("appointment confirmed",
		"id", appt.ID, "bay", appt.ServiceBayID, "technician", appt.TechnicianID)
	return appt, nil
}
