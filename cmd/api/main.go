package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/infrastructure/database"
	"github.com/johirdev/Hisabji-Server/internal/shared/banner"
)

func main() {
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	redisClient, e := database.OpenRedis(cfg.RedisURL)
	if e != nil {
		log.Fatalf("redis configuration failed: %v", e)
	}
	defer redisClient.Close()
	if e = redisClient.Ping(ctx).Err(); e != nil {
		log.Fatalf("redis connection failed: %v", e)
	}

	dbOk := true
	if e = db.Ping(ctx); e != nil {
		dbOk = false
		log.Fatalf("database connection failed: %v", e)
	}
	if e = database.EnsureSchema(ctx, db); e != nil {
		log.Fatalf("database schema initialization failed: %v", e)
	}

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           setupRoutes(db, redisClient, cfg),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()

	// Give ListenAndServe a brief moment to fail fast (e.g. port already in
	// use) before printing the "up and running" banner.
	select {
	case e := <-serverErrors:
		if e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	case <-time.After(150 * time.Millisecond):
		banner.Print(banner.Info{
			AppName:   "Hisabji API",
			Port:      cfg.Port,
			Env:       cfg.Env,
			DBOk:      dbOk,
			StartedAt: time.Now(),
		})
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	select {
	case e = <-serverErrors:
		if e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	case <-stop:
		log.Println("shutting down gracefully...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if e = server.Shutdown(shutdownCtx); e != nil {
			log.Printf("server shutdown failed: %v", e)
		} else {
			log.Println("server stopped cleanly")
		}
	}
}
