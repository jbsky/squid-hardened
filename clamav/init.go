// ClamAV hardened init — replaces entrypoint.sh + healthcheck.
// Static binary, zero shell dependency.
//
// Usage:
//
//	init [--healthcheck]     run Docker healthcheck (exit 0/1)
//	init [CMD [ARGS...]]    freshclam init + daemon, then exec CMD
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
// Healthcheck: PING → expect PONG
// ---------------------------------------------------------------------------

func healthcheck() int {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:3310", 2*time.Second)
	if err != nil {
		return 1
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprint(conn, "PING\n")
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err == nil && strings.Contains(string(buf[:n]), "PONG") {
		return 0
	}
	return 1
}

// ---------------------------------------------------------------------------
// Entrypoint
// ---------------------------------------------------------------------------

func entrypoint() error {
	dbDir := "/var/lib/clamav"
	freshclamConf := "/etc/clamav/freshclam.conf"

	// 1) Initial signature download if DB is empty
	if !hasSignatures(dbDir) {
		fmt.Println("[init] Téléchargement initial des signatures (peut prendre plusieurs minutes)...")
		cmd := exec.Command("freshclam",
			"--config-file="+freshclamConf,
			"--foreground=true",
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("freshclam initial échoué — vérifier l'accès Internet ou le proxy: %w", err)
		}
	}

	// 2) Start freshclam daemon in background
	fmt.Println("[init] Démarrage de freshclam en daemon")
	daemon := exec.Command("freshclam",
		"--config-file="+freshclamConf,
		"--daemon",
		"--checks=24",
	)
	daemon.Stdout = os.Stdout
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		// Non-fatal: clamd can run without auto-updates
		fmt.Fprintf(os.Stderr, "[init][WARN] freshclam daemon: %v\n", err)
	}

	// 3) Exec clamd
	fmt.Println("[init] Démarrage de clamd")
	return execCmd(os.Args[1:])
}

// hasSignatures checks for .cvd or .cld files in the DB directory.
func hasSignatures(dir string) bool {
	for _, ext := range []string{"*.cvd", "*.cld"} {
		matches, _ := filepath.Glob(filepath.Join(dir, ext))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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
