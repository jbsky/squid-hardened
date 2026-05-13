// C-ICAP hardened init — replaces entrypoint.sh + healthcheck.
// Static binary, zero shell dependency.
//
// Usage:
//
//	init [--healthcheck]     run Docker healthcheck (exit 0/1)
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

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--healthcheck" {
		os.Exit(healthcheck())
	}
	if err := entrypoint(); err != nil {
		fmt.Fprintf(os.Stderr, "[init][ERROR] %v\n", err)
		os.Exit(1)
	}
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
