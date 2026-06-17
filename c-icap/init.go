// C-ICAP hardened init — replaces entrypoint.sh + healthcheck.
// Static binary, zero shell dependency.
//
// Usage:
//
//	init --healthcheck      run Docker healthcheck (exit 0/1)
//	init --setup-dirs       create runtime directories (build-time, FROM scratch)
//	init [CMD [ARGS...]]    wait for clamd, then exec CMD
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	cicapUID = 4100
	cicapGID = 4100
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--healthcheck":
			os.Exit(healthcheck())
		case "--setup-dirs":
			if err := setupDirs(); err != nil {
				fmt.Fprintf(os.Stderr, "[init][ERROR] setup-dirs: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := entrypoint(); err != nil {
		fmt.Fprintf(os.Stderr, "[init][ERROR] %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Setup directories — called at build time in FROM scratch stage.
// Creates runtime dirs with correct ownership; no shell needed.
// ---------------------------------------------------------------------------

func setupDirs() error {
	dirs := []struct {
		path string
		mode os.FileMode
		uid  int
		gid  int
	}{
		{"/var/log/c-icap", 0755, cicapUID, cicapGID},
		{"/run/c-icap", 0755, cicapUID, cicapGID},
		{"/tmp", 01777, 0, 0},
	}
	for _, d := range dirs {
		fmt.Printf("[init] mkdir %s (mode=%04o uid=%d gid=%d)\n", d.path, d.mode, d.uid, d.gid)
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("mkdir %s: %w", d.path, err)
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return fmt.Errorf("chmod %s: %w", d.path, err)
		}
		if err := os.Chown(d.path, d.uid, d.gid); err != nil {
			return fmt.Errorf("chown %s: %w", d.path, err)
		}
	}
	fmt.Println("[init] setup-dirs complete")
	return nil
}

// ---------------------------------------------------------------------------
// Healthcheck: OPTIONS icap://localhost/squidclamav → expect "200"
// ---------------------------------------------------------------------------

func healthcheck() int {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:1344", 2*time.Second)
	if err != nil {
		return 1
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprint(conn, "OPTIONS icap://localhost/squidclamav ICAP/1.0\r\nHost: localhost\r\n\r\n")
	sc := bufio.NewScanner(conn)
	if sc.Scan() && strings.Contains(sc.Text(), "200") {
		return 0
	}
	return 1
}

// ---------------------------------------------------------------------------
// Entrypoint: wait for clamd to be reachable, then exec
// ---------------------------------------------------------------------------

func entrypoint() error {
	host := env("CLAMD_HOST", "clamav")
	port := env("CLAMD_PORT", "3310")
	addr := net.JoinHostPort(host, port)

	fmt.Printf("[init] Attente de clamd (%s)...\n", addr)

	for i := 0; i < 120; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			fmt.Println("[init] clamd OK, démarrage c-icap")
			return execCmd(os.Args[1:])
		}
		time.Sleep(time.Second)
	}

	return fmt.Errorf("clamd injoignable après 120s (%s)", addr)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func execCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command specified")
	}
	bin, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("command not found: %s", args[0])
	}
	return syscall.Exec(bin, args, os.Environ())
}
