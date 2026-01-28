package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/itsmylife44/terminus-pty/internal/api"
	"github.com/itsmylife44/terminus-pty/internal/auth"
	"github.com/itsmylife44/terminus-pty/internal/session"
	"github.com/itsmylife44/terminus-pty/internal/tmux"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	port := flag.Int("port", 3001, "Port to listen on")
	host := flag.String("host", "127.0.0.1", "Host to bind to")
	sessionTimeout := flag.Duration("session-timeout", 30*time.Second, "Session pool timeout after disconnect")
	cleanupInterval := flag.Duration("cleanup-interval", 10*time.Second, "Session cleanup interval")
	shell := flag.String("shell", "", "Shell to use (default: $SHELL or /bin/bash) - alias for --command")
	command := flag.String("command", "", "Command to run (default: $SHELL or /bin/bash)")
	args := flag.String("args", "", "Command arguments (comma-separated, default: -l,-i for shells)")
	workdir := flag.String("workdir", "", "Working directory for new sessions")
	authUser := flag.String("auth-user", "", "Basic auth username (optional)")
	authPass := flag.String("auth-pass", "", "Basic auth password (optional)")
	tmuxEnabled := flag.Bool("tmux-enabled", false, "Spawn PTY sessions inside tmux for persistence")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("terminus-pty %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Check tmux is installed if tmux mode is enabled
	if *tmuxEnabled {
		if err := tmux.CheckInstalled(); err != nil {
			slog.Error("tmux mode enabled but tmux is not installed", "error", err)
			fmt.Fprintf(os.Stderr, "Error: tmux mode enabled but tmux is not installed.\n")
			fmt.Fprintf(os.Stderr, "Install tmux or run without --tmux-enabled flag.\n")
			os.Exit(1)
		}
		slog.Info("tmux mode enabled - sessions will persist across disconnections")
	}

	// Resolve command (--command takes precedence over --shell)
	cmdPath := *command
	if cmdPath == "" {
		cmdPath = *shell // Backward compatibility
	}
	if cmdPath == "" {
		cmdPath = os.Getenv("SHELL")
		if cmdPath == "" {
			cmdPath = "/bin/bash"
		}
	}

	// Parse args
	var cmdArgs []string
	if *args != "" {
		cmdArgs = strings.Split(*args, ",")
	}
	// Default args for shells
	if len(cmdArgs) == 0 && (strings.HasSuffix(cmdPath, "sh") || strings.Contains(cmdPath, "/sh")) {
		cmdArgs = []string{"-l", "-i"}
	}

	pool := session.NewPool(session.PoolConfig{
		SessionTimeout:  *sessionTimeout,
		CleanupInterval: *cleanupInterval,
		DefaultCommand:  cmdPath,
		DefaultArgs:     cmdArgs,
		DefaultWorkdir:  *workdir,
		TmuxEnabled:     *tmuxEnabled,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.StartCleanup(ctx)

	var authenticator *auth.BasicAuth
	if *authUser != "" && *authPass != "" {
		authenticator = auth.NewBasicAuth(*authUser, *authPass)
		slog.Info("Basic auth enabled")
	}

	handler := api.NewHandler(pool, authenticator)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("Starting terminus-pty", "addr", addr, "command", cmdPath, "args", cmdArgs, "workdir", *workdir, "version", version, "tmux_enabled", *tmuxEnabled, "session_timeout", *sessionTimeout)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("Shutting down...")

	cancel()
	pool.CloseAll()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown error", "error", err)
	}

	slog.Info("Goodbye")
}
