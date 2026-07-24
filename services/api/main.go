package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Depo-dev/trident/services/api/handlers"
	trident "github.com/Depo-dev/trident/services/api/internal/proto"
	"github.com/Depo-dev/trident/services/api/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	middleware.InitLogger("trident-api")

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	grpcAddr := os.Getenv("GRPC_API_ADDR")
	if grpcAddr == "" {
		grpcAddr = "localhost:50051"
	}

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		slog.Error("failed to connect to gRPC API", "addr", grpcAddr, "err", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	eventsClient := trident.NewEventsClient(conn)
	eventsHandler := handlers.NewEventsHandler(eventsClient)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", handlers.Health)
	mux.HandleFunc("GET /v1/events", eventsHandler.ListEvents)
	mux.HandleFunc("GET /v1/events/{id}", eventsHandler.GetEvent)

	// RequestID is the outer layer so the correlation ids it attaches are
	// visible to Logging (and every handler) when they run.
	chain := middleware.RequestID(middleware.Logging(mux))

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      chain,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("Trident API server listening", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}
