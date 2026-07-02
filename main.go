package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kamccabe44/co_tracker/internal/api"
	"github.com/kamccabe44/co_tracker/internal/store"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := envOr("LISTEN_ADDR", ":8080")
	dbPath := envOr("DB_PATH", "data/co_tracker.db")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.SeedAccounts(seedUsers()); err != nil {
		log.Fatalf("seed accounts: %v", err)
	}

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(st, staticFS),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("co_tracker listening on %s (db: %s)", addr, dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// seedUsers parses USERS_JSON (a username -> password object, as in os_alerts)
// into the initial login accounts. Only used when the accounts table is empty.
func seedUsers() map[string]string {
	fallback := map[string]string{"admin": "admin"}
	raw := os.Getenv("USERS_JSON")
	if raw == "" {
		return fallback
	}
	var users map[string]string
	if err := json.Unmarshal([]byte(raw), &users); err != nil || len(users) == 0 {
		log.Printf("invalid USERS_JSON, using default admin account: %v", err)
		return fallback
	}
	return users
}
