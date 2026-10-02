// Command api runs the KamaraPMS REST API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // property time zones must resolve even on hosts without a zone database

	"kamarapms/internal/app"
	"kamarapms/internal/iam"
	"kamarapms/internal/notifications"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/config"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/logging"
	"kamarapms/internal/platform/migrate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kamara-pms api:", err)
		os.Exit(1)
	}
}

func run() error {
	// Local development: read ./.env if present (real environment variables take precedence).
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("database connected", "url", cfg.RedactedDatabaseURL())

	if cfg.MigrateOnStart {
		n, err := migrate.Up(ctx, pool)
		if err != nil {
			return err
		}
		logger.Info("migrations applied", "count", n)
	}

	var mailer notifications.Sender
	if cfg.SMTPHost != "" {
		mailer = notifications.NewSMTPSender(notifications.SMTPConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
			From: cfg.SMTPFrom, FromName: cfg.SMTPFromName, TLS: cfg.SMTPTLS,
		})
		logger.Info("e-mail enabled", "host", cfg.SMTPHost, "port", cfg.SMTPPort, "tls", cfg.SMTPTLS, "from", cfg.SMTPFrom)
	} else {
		logger.Info("e-mail is off (PMS_SMTP_HOST is not set)")
	}
	application := app.New(app.Deps{
		Mail:               mailer,
		Logger:             logger,
		DB:                 pool,
		TxManager:          db.NewTxManager(pool, cfg.DBLockTimeout),
		Clock:              clock.System{},
		RateLimitPerMinute: cfg.RateLimit,
		Tokens: iam.TokenConfig{
			Secret:       cfg.JWTSecret,
			AccessTTL:    cfg.AccessTokenTTL,
			RefreshTTL:   cfg.RefreshTokenTTL,
			CookieSecure: cfg.CookieSecure,
		},
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           application.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	go application.Background(ctx) // the e-mail worker; it stops with the signal context

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
	}
	return nil
}
