package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"listingboard/internal/store"
	"listingboard/internal/worker"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	pause, err := stepPause()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Connect(ctx, dsn, 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	log.Printf("worker started, step pause %s", pause)
	worker.New(st, env("DATA_DIR", "data"), pause).Run(ctx)
	log.Print("worker stopped")
}

func stepPause() (time.Duration, error) {
	raw := os.Getenv("STEP_PAUSE")
	if raw == "" {
		return 2 * time.Second, nil
	}
	pause, err := time.ParseDuration(raw)
	if err == nil && pause < 0 {
		err = errors.New("must not be negative")
	}
	if err != nil {
		return 0, fmt.Errorf("STEP_PAUSE %q: %w", raw, err)
	}
	return pause, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
