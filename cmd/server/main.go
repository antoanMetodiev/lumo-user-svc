// Command server is the Go rewrite of lumo-user-svc (originally Spring
// Boot). Endpoint paths, params, status codes, and stored data all mirror
// the Java service exactly — see each internal/ package for the per-package
// notes on where behavior was intentionally preserved (including a couple
// of the original's own quirks) vs. where a Java-only implementation detail
// was replaced with a plain, equivalent statement.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lumo-user-svc/internal/aiassistant"
	"lumo-user-svc/internal/clerkwebhook"
	"lumo-user-svc/internal/config"
	"lumo-user-svc/internal/cors"
	"lumo-user-svc/internal/dbx"
	"lumo-user-svc/internal/friends"
	"lumo-user-svc/internal/gatewayauth"
	"lumo-user-svc/internal/heartbeat"
	"lumo-user-svc/internal/lastviewed"
	"lumo-user-svc/internal/likes"
	"lumo-user-svc/internal/metrics"
	"lumo-user-svc/internal/playlists"
	"lumo-user-svc/internal/reqlog"
	"lumo-user-svc/internal/users"
	"lumo-user-svc/internal/watching"
	"lumo-user-svc/internal/watchlist"
)

// userScopedPaths mirrors SecurityConfig.USER_SCOPED_PATHS exactly — plus
// "/watching", a new-feature addition with no Java original (see friends'
// own package doc comment for the precedent).
var userScopedPaths = []string{"/user/me", "/watchlist", "/last-viewed", "/friends", "/likes", "/playlists", "/watching", "/ai-assistant"}

// internalPaths mirrors SecurityConfig.INTERNAL_PATHS exactly.
var internalPaths = []string{"/internal"}

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := dbx.Dial(ctx, cfg.DatabaseURL, cfg.DBUsername, cfg.DBPassword)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer pool.Close()

	dbx.StartKeepAlivePing(ctx, pool, log.Printf)

	shutdownMetrics, err := metrics.Start(ctx, cfg.GrafanaEnabled, cfg.GrafanaEndpoint, cfg.GrafanaAuth)
	if err != nil {
		log.Printf("metrics exporter did not start: %v", err)
		shutdownMetrics = func(context.Context) error { return nil }
	}
	defer shutdownMetrics(context.Background())

	usersRepo := users.NewRepository(pool)
	usersSvc := users.NewService(usersRepo)

	watchlistRepo := watchlist.NewRepository(pool)
	watchlistSvc := watchlist.NewService(watchlistRepo)

	lastViewedRepo := lastviewed.NewRepository(pool)
	lastViewedSvc := lastviewed.NewService(lastViewedRepo)

	friendsRepo := friends.NewRepository(pool)
	friendsNotifier := friends.NewNotifyClient(cfg.GatewayURL, cfg.InternalSecret)
	friendsSvc := friends.NewService(friendsRepo, usersRepo, friendsNotifier)

	likesRepo := likes.NewRepository(pool)
	likesSvc := likes.NewService(likesRepo, friendsRepo)

	playlistsRepo := playlists.NewRepository(pool)
	playlistsSvc := playlists.NewService(playlistsRepo, friendsRepo)

	watchingSvc := watching.NewService(friendsRepo, usersRepo, friendsNotifier)
	go watchingSvc.StartSweeper(ctx)

	aiAssistantRepo := aiassistant.NewRepository(pool)
	aiAssistantSvc := aiassistant.NewService(aiAssistantRepo)

	paymentsSync := clerkwebhook.NewPaymentsSync(cfg.GatewayURL, cfg.InternalSecret)
	clerkWebhookSvc := clerkwebhook.NewService(usersRepo, paymentsSync)

	mux := http.NewServeMux()
	users.NewHandlers(usersSvc).Register(mux)
	watchlist.NewHandlers(watchlistSvc).Register(mux)
	lastviewed.NewHandlers(lastViewedSvc).Register(mux)
	likes.NewHandlers(likesSvc).Register(mux)
	friends.NewHandlers(friendsSvc).Register(mux)
	playlists.NewHandlers(playlistsSvc).Register(mux)
	watching.NewHandlers(watchingSvc).Register(mux)
	aiassistant.NewHandlers(aiAssistantSvc).Register(mux)
	clerkwebhook.NewHandlers(cfg.ClerkWebhookSecret, clerkWebhookSvc).Register(mux)
	heartbeat.Register(mux)

	// management.endpoints.web.exposure.include=health — Render's own health
	// check and any uptime monitor hit this.
	mux.HandleFunc("GET /actuator/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"UP"}`))
	})

	handler := reqlog.Middleware(
		cors.Middleware(
			gatewayauth.Middleware(cfg.InternalSecret, userScopedPaths, internalPaths)(mux),
		),
	)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("lumo-user-svc (Go) listening on :%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
