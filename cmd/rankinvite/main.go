package main

import (
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"rankinvite/internal/auth"
	internaldb "rankinvite/internal/db"
	"rankinvite/internal/storage"
	"rankinvite/internal/web"
	"rankinvite/internal/worker"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func main() {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if os.Getenv("DEBUG") != "" {
		opts.Level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, opts))
	slog.SetDefault(logger)

	dbPath := os.Getenv("DATABASE_URL")
	if dbPath == "" {
		dbPath = "rankinvite.db"
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Run Migrations
	goose.SetBaseFS(internaldb.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		log.Fatal(err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		log.Fatal(err)
	}

	// Initialize Storage
	repo := storage.NewInvitationRepository(db)

	// Initialize Auth
	authService := auth.NewAuthService(db)

	// Ensure default admin
	if err := authService.EnsureAdmin("admin@rankinvite.no", "admin"); err != nil {
		log.Fatalf("Failed to ensure default admin: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:" + port
	}

	// Initialize Web Server
	mux := http.NewServeMux()
	server := web.NewServer(repo, authService)
	server.SetBaseURL(baseURL)
	server.RegisterHandlers(mux)

	// Initialize and Start Worker
	workerInstance := worker.NewWorker(repo, authService)
	workerInstance.SetBaseURL(baseURL)
	workerInstance.Start()
	
	// Pass worker to server so it can process events on manual actions
	server.SetWorker(workerInstance)

	fmt.Printf("RankInvite (Go) - Listening on http://localhost:%s\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
