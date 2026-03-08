package runner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hotreload/logger"
)

// Manager handles the lifecycle of the build and run processes.
type Manager struct {
	dir      string
	buildCmd string
	execCmd  string
}

// New creates a new Manager with the provided CLI commands.
func New(dir, buildCmd, execCmd string) *Manager {
	return &Manager{
		dir:      dir,
		buildCmd: buildCmd,
		execCmd:  execCmd,
	}
}

// Start coordinates the build and run process under a specific context.
func (m *Manager) Start(ctx context.Context) {
	slog.Info("Starting build...", "command", m.buildCmd)
	if err := m.runProcess(ctx, m.buildCmd); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Error("Build failed", "error", err)
		}
		return 
	}

	slog.Info("Build successful. Starting server...", "command", m.execCmd)
	if err := m.runProcess(ctx, m.execCmd); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Error("Server stopped", "error", err)
		}
	}
}

// runProcess executes a shell command, streams logs, and ties it to the context.
func (m *Manager) runProcess(ctx context.Context, cmdStr string) error {
	args := strings.Fields(cmdStr)
	if len(args) == 0 {
		return nil
	}

	cmd := exec.Command(args[0], args[1:]...)
	
	// Set the working directory to the project root so it doesn't build in the CLI folder
	cmd.Dir = m.dir 

	// Pipe standard output through our Blue [APP] logger
	cmd.Stdout = logger.NewPrefixWriter("APP", logger.ColorBlue, os.Stdout)
	// Pipe error output through our Red [ERR] logger
	cmd.Stderr = logger.NewPrefixWriter("ERR", logger.ColorRed, os.Stderr)

	// WINDOWS MAGIC TRICK: Creates a new process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return m.killProcessTree(cmd.Process.Pid, errCh)
	case err := <-errCh:
		return err
	}
}

// killProcessTree ensures the process and all its children are terminated safely on Windows.
func (m *Manager) killProcessTree(pid int, errCh chan error) error {
	slog.Info("Changes detected. Forcefully stopping process tree...")

	killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	
	if err := killCmd.Run(); err != nil {
		slog.Warn("Failed to execute taskkill (process might already be dead)", "error", err)
	}

	select {
	case <-errCh:
		return context.Canceled
	case <-time.After(2 * time.Second):
		slog.Warn("Process took too long to die after taskkill")
		return context.Canceled
	}
}