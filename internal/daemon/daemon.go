package daemon

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	PIDFileName  = "free-model-router.pid"
	LogFileName  = "daemon.log"
	StartTimeout = 5 * time.Second
	StopTimeout  = 15 * time.Second
)

// Status represents the daemon status.
type Status int

const (
	StatusStopped Status = iota
	StatusRunning
	StatusError
)

// Result holds the outcome of a daemon operation.
type Result struct {
	Status Status
	PID    int
	Msg    string
}

// Start forks the process into the background and writes a PID file.
func Start(extraArgs []string) error {
	pidFile := PIDFile()

	// Check if already running
	existingPID, err := readPID(pidFile)
	if err != nil {
		return fmt.Errorf("checking existing PID: %w", err)
	}
	if isAlive(existingPID) {
		return fmt.Errorf("daemon already running (PID %d). Use '-daemon stop' first.", existingPID)
	}
	// Stale PID file
	if existingPID > 0 {
		removePID(pidFile)
	}

	// Resolve absolute path to the current binary
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve symlink: %w", err)
	}

	// Build child args: -daemon start is removed, extra args are passed through
	args := []string{filepath.Base(exe)}
	args = append(args, extraArgs...)

	// Open log file for the child
	logFile := LogFile()
	logFH, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logFile, err)
	}

	// Write startup timestamp to log
	fmt.Fprintf(logFH, "--- daemon started %s ---\n", time.Now().Format(time.RFC3339))

	// Fork via os.StartProcess
	devNull, _ := os.Open(os.DevNull)

	attr := &os.ProcAttr{
		Dir:   ".",
		Env:   os.Environ(),
		Files: []*os.File{devNull, logFH, logFH},
		Sys: &syscall.SysProcAttr{
			Setsid: true,
		},
	}

	process, err := os.StartProcess(exe, args, attr)
	if err != nil {
		devNull.Close()
		logFH.Close()
		return fmt.Errorf("fork daemon: %w", err)
	}

	devNull.Close()
	logFH.Close()

	// Wait for child to write PID file
	deadline := time.Now().Add(StartTimeout)
	for time.Now().Before(deadline) {
		pid, _ := readPID(pidFile)
		if pid > 0 && isAlive(pid) {
			fmt.Printf("Daemon started (PID %d). Log: %s\n", pid, logFile)
			process.Release()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Child didn't start in time
	process.Signal(syscall.SIGKILL)
	process.Release()
	removePID(pidFile)
	return fmt.Errorf("daemon failed to start within %s", StartTimeout)
}

// Stop sends SIGTERM to the daemon and waits for it to exit.
func Stop() error {
	pidFile := PIDFile()

	pid, err := readPID(pidFile)
	if err != nil {
		return fmt.Errorf("reading PID file: %w", err)
	}
	if pid == 0 {
		return fmt.Errorf("no daemon running (no PID file)")
	}
	if !isAlive(pid) {
		fmt.Printf("Daemon not running (stale PID %d). Cleaning up.\n", pid)
		removePID(pidFile)
		return nil
	}

	// Send SIGTERM for graceful shutdown
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	fmt.Printf("Stopping daemon (PID %d)...\n", pid)
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", pid, err)
	}

	// Wait for process to exit
	deadline := time.Now().Add(StopTimeout)
	for time.Now().Before(deadline) {
		if !isAlive(pid) {
			removePID(pidFile)
			fmt.Println("Daemon stopped.")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Force kill if still alive
	fmt.Println("Daemon did not stop gracefully. Sending SIGKILL...")
	process.Signal(syscall.SIGKILL)
	time.Sleep(500 * time.Millisecond)
	removePID(pidFile)
	fmt.Println("Daemon killed.")
	return nil
}

// Status checks if the daemon is running.
func GetStatus() Result {
	pidFile := PIDFile()

	pid, err := readPID(pidFile)
	if err != nil {
		return Result{Status: StatusError, Msg: fmt.Sprintf("read PID file: %v", err)}
	}
	if pid == 0 {
		return Result{Status: StatusStopped, Msg: "No daemon running."}
	}
	if isAlive(pid) {
		return Result{Status: StatusRunning, PID: pid, Msg: fmt.Sprintf("Daemon running (PID %d).", pid)}
	}

	// Stale PID
	removePID(pidFile)
	return Result{Status: StatusStopped, Msg: fmt.Sprintf("Stale PID file (process %d dead). Cleaned up.", pid)}
}

// WritePID writes the current process PID to the PID file.
// Called by the child process after forking.
func WritePID() {
	pidFile := PIDFile()
	writePID(pidFile, os.Getpid())
}

// SetupGracefulCleanup registers a signal handler that removes the PID file on exit.
func SetupGracefulCleanup() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		RemovePIDFile()
		os.Exit(0)
	}()
}

// RemovePIDFile removes the PID file if it exists.
func RemovePIDFile() {
	removePID(PIDFile())
}

// --- Internal helpers ---

func PIDFile() string {
	return PIDFileName
}

func LogFile() string {
	return LogFileName
}

func readPID(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse PID: %w", err)
	}
	return pid, nil
}

func writePID(pidFile string, pid int) error {
	tmp := pidFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, pidFile)
}

func removePID(pidFile string) {
	os.Remove(pidFile)
}

func isAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
