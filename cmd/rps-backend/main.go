// Command rps-backend runs the Rock-Paper-Scissors game server.
//
// Usage:
//
//	rps-backend [serve] [flags]   run the server (default)
//	rps-backend healthcheck       exit 0 if the local server is healthy
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hamologist/rps-backend/internal/clock"
	"github.com/hamologist/rps-backend/internal/config"
	"github.com/hamologist/rps-backend/internal/httpapi"
	"github.com/hamologist/rps-backend/internal/hub"
	"github.com/hamologist/rps-backend/internal/store"
	"github.com/hamologist/rps-backend/internal/sweeper"
	"github.com/hamologist/rps-backend/internal/ws"
)

func main() {
	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 && (args[0] == "serve" || args[0] == "healthcheck") {
		cmd, args = args[0], args[1:]
	}
	cfg, err := config.Load("rps-backend "+cmd, args, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	switch cmd {
	case "healthcheck":
		os.Exit(healthcheck(cfg))
	default:
		if err := serve(cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func healthcheck(cfg config.Config) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(cfg.Port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy:", resp.Status)
		return 1
	}
	return 0
}

func serve(cfg config.Config) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	clk := clock.Real{}
	h := hub.New(st, clk, log)
	wsHandler := ws.New(ws.Options{
		Hub:            h,
		Logger:         log,
		AllowedOrigins: cfg.AllowedOrigins,
		AuthTimeout:    cfg.AuthTimeout,
		PingInterval:   cfg.PingInterval,
	})
	srv := &http.Server{
		Addr: net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.Port)),
		Handler: httpapi.New(httpapi.Options{
			Store:          st,
			Clock:          clk,
			Logger:         log,
			AllowedOrigins: cfg.AllowedOrigins,
			WS:             wsHandler,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	sweepCtx, stopSweep := context.WithCancel(context.Background())
	sweepDone := make(chan struct{})
	sw := &sweeper.Sweeper{
		Store: st, Hub: h, Clock: clk, Logger: log,
		PlayerTTL: cfg.PlayerTTL, GameTTL: cfg.GameTTL, Interval: cfg.SweepInterval,
	}
	go func() { defer close(sweepDone); sw.Run(sweepCtx) }()

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "db", cfg.DBPath)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		stopSweep()
		<-sweepDone
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stopSweep()
	<-sweepDone
	// Shutdown stops accepting connections; hijacked WebSockets are closed
	// separately and waited for below.
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http shutdown", "err", err)
	}
	h.CloseAll(hub.CloseNormal, "server shutdown")
	wsHandler.Wait(shutdownCtx)
	log.Info("stopped")
	return nil
}
