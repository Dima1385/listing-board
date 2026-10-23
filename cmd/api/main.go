package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"listingboard/internal/httpapi"
	"listingboard/internal/store"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	dataDir := env("DATA_DIR", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := waitStore(dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           httpapi.New(st, dataDir, os.Getenv("WEB_DIR")).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func waitStore(dsn string) (*store.Store, error) {
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for {
		st, err := store.Open(dsn)
		if err == nil {
			return st, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, last
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
