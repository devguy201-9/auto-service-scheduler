package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/devguy201-9/auto-service-scheduler/internal/api"
	"github.com/devguy201-9/auto-service-scheduler/internal/config"
	"github.com/devguy201-9/auto-service-scheduler/internal/repository"
	"github.com/devguy201-9/auto-service-scheduler/internal/service"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Error("cannot connect to postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	repo := repository.New(pool)
	booking := service.NewBookingService(repo, log)
	handler := api.NewHandler(booking, repo, log)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      handler.Routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Info("server starting", "addr", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}
