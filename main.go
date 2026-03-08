package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"hotreload/runner"
	"hotreload/watcher"
)

// Config holds the CLI parameters
type Config struct {
	Root  string
	Build string
	Exec  string
}

func main() {
	// 1. Configure structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// 2. Parse CLI flags
	var cfg Config
	flag.StringVar(&cfg.Root, "root", ".", "Directory to watch for file changes")
	flag.StringVar(&cfg.Build, "build", "", "Command used to build the project")
	flag.StringVar(&cfg.Exec, "exec", "", "Command used to run the built server")
	flag.Parse()

	if cfg.Build == "" || cfg.Exec == "" {
		slog.Error("Both --build and --exec commands are required")
		os.Exit(1)
	}

	slog.Info("Starting hotreload...", "root", cfg.Root)

	// 3. Initialize the Watcher
	w, err := watcher.New(cfg.Root)
	if err != nil {
		slog.Error("Failed to initialize watcher", "error", err)
		os.Exit(1)
	}

	// 4. Initialize the Runner
	m := runner.New(cfg.Root, cfg.Build, cfg.Exec)

	// A channel that acts as the tripwire. The watcher triggers this.
	triggerChan := make(chan struct{}, 1)

	// Base context to tie everything to the main application lifecycle
	ctx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	// Start the watcher in the background
	go w.Start(ctx, triggerChan)

	// Kick off the very first build immediately without waiting for a file change
	triggerChan <- struct{}{}

	// 5. The Pipeline Coordinator
	var runCtx context.Context
	var cancelRun context.CancelFunc
	var runnerDone chan struct{}

	for {
		select {
		case <-triggerChan:
			// If a pipeline is already running, we must kill it before starting a new one
			if cancelRun != nil {
				slog.Info("Pipeline interrupt received...")
				cancelRun()  // This sends the signal to runner.go to execute taskkill
				<-runnerDone // Block until we are absolutely sure the process tree is dead
			}

			// Create a fresh context for the new build/run cycle
			runCtx, cancelRun = context.WithCancel(context.Background())
			runnerDone = make(chan struct{})

			// Start the new pipeline in a separate goroutine
			go func(ctx context.Context, done chan struct{}) {
				defer close(done) // Signal that this pipeline has fully exited
				m.Start(ctx)
			}(runCtx, runnerDone)
		}
	}
}