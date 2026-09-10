// Command api is the Hisabji HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embeds the IANA timezone database in the binary. Without it, a scratch or
	// alpine container has no /usr/share/zoneinfo, LoadLocation("Asia/Dhaka")
	// fails, and every daily total silently shifts to UTC — a bug that only
	// appears in production and looks like a calculation error.
	_ "time/tzdata"

	"github.com/johirdev/Hisabji-Server/internal/app"
	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/shared/banner"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nhisabji: %v\n\n", err)
		os.Exit(1)
	}
}

func run() error {
	// The boot context is cancelled by SIGINT/SIGTERM, so a Ctrl+C during a long
	// migration stops cleanly instead of leaving a half-applied schema.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	application, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer application.Close()

	server := &http.Server{
		Addr:    cfg.Server.Addr(),
		Handler: application.Router(),

		// Every one of these timeouts exists to stop one specific attack or bug:
		//
		//   ReadHeaderTimeout  a Slowloris client that sends headers one byte at
		//                      a time and holds a connection open forever
		//   ReadTimeout        a client that stalls mid-body
		//   WriteTimeout       a handler that hangs on a slow dependency
		//   IdleTimeout        keep-alive connections accumulating until the
		//                      process runs out of file descriptors
		//
		// A server with no timeouts stays up for weeks and then falls over all at
		// once, which is the hardest kind of outage to diagnose.
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,

		ErrorLog: nil, // gin logs through our structured logger instead
	}

	serverErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	// Give ListenAndServe a moment to fail fast — a port already in use should
	// print the real error, not a cheerful "up and running" banner.
	select {
	case err := <-serverErrors:
		if err != nil {
			return fmt.Errorf("the server could not start: %w", err)
		}
		return nil
	case <-time.After(200 * time.Millisecond):
	}

	dbOK, _ := application.Health(ctx)
	banner.Print(banner.Info{
		AppName:   cfg.App.Name,
		Port:      cfg.Server.Port,
		Env:       cfg.App.Env,
		DBOk:      dbOK,
		StartedAt: time.Now(),
	})

	// ---- wait for a shutdown signal or a server failure --------------------
	select {
	case err := <-serverErrors:
		if err != nil {
			return fmt.Errorf("the server stopped unexpectedly: %w", err)
		}
		return nil

	case <-ctx.Done():
		stop() // restore default signal handling: a second Ctrl+C kills at once
		logger.Named("server").Info("shutdown signal received, draining connections",
			"timeout", cfg.Server.ShutdownTimeout.String())

		// Graceful shutdown finishes in-flight requests before exiting. Without
		// it, a deploy drops every request that happened to be mid-flight —
		// including a payment confirmation that had already taken the money.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Named("server").Error("graceful shutdown timed out; closing forcibly",
				"error", err.Error())
			_ = server.Close()
			return fmt.Errorf("shutdown did not complete within %s: %w",
				cfg.Server.ShutdownTimeout, err)
		}

		logger.Named("server").Info("server stopped cleanly")
		return nil
	}
}
