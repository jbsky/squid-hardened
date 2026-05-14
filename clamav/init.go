// ClamAV hardened init — replaces entrypoint.sh + healthcheck.
// Static binary, zero shell dependency.
//
// Usage:
//
//	init --healthcheck      run Docker healthcheck (exit 0/1)
//	init --setup-dirs       create runtime directories (build-time, FROM scratch)
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

// diagVolume logs ownership, permissions and contents of a directory.
// Helps diagnose bind-mount issues (sgid, uid mismatch, etc.).
func diagVolume(path string) {
	fmt.Printf("[init][diag] process uid=%d gid=%d\n", os.Getuid(), os.Getgid())
	info, err := os.Stat(path)
	if err != nil {
		fmt.Printf("[init][diag] stat %s: %v\n", path, err)
		return
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok {
		fmt.Printf("[init][diag] stat %s: mode=%04o uid=%d gid=%d\n", path, info.Mode().Perm()|os.FileMode(stat.Mode&0xFFFFF000>>12<<12), stat.Uid, stat.Gid)
	} else {
		fmt.Printf("[init][diag] stat %s: mode=%s (no syscall info)\n", path, info.Mode())
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Printf("[init][diag] readdir %s: %v\n", path, err)
		return
	}
	fmt.Printf("[init][diag] readdir %s: %d entries\n", path, len(entries))
	for _, e := range entries {
		ei, _ := e.Info()
		if ei != nil {
			fmt.Printf("[init][diag]   %s (size=%d mode=%s)\n", e.Name(), ei.Size(), ei.Mode())
		} else {
			fmt.Printf("[init][diag]   %s\n", e.Name())
		}
	}
	// write-test
	tmp, err := os.CreateTemp(path, ".diag-write-test-*")
	if err != nil {
		fmt.Printf("[init][diag] write-test %s: FAILED: %v\n", path, err)
		return
	}
	name := tmp.Name()
	tmp.Close()
	os.Remove(name)
	fmt.Printf("[init][diag] write-test %s: OK\n", path)
}

// ensureWritable verifies that path is writable by the current process.
// If not, it attempts chown (requires CAP_CHOWN — usually unavailable as
// non-root).  On failure it returns an actionable error with the host-side
// fix command.
func ensureWritable(path string, uid, gid int) error {
	// Check the directory exists and is accessible
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w\n"+
			"  If this is a bind-mount, ensure the host directory exists and\n"+
			"  its parent directories are traversable (mode 0755)",
			path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", path)
	}
	// Fast path: already writable
	tmp, err := os.CreateTemp(path, ".write-test-*")
	if err == nil {
		name := tmp.Name()
		tmp.Close()
		os.Remove(name)
		return nil
	}
	// Not writable — attempt recursive chown (best-effort, requires CAP_CHOWN)
	fmt.Printf("[init] %s is not writable by uid %d, attempting chown to %d:%d\n", path, os.Getuid(), uid, gid)
	if chErr := chownRecursive(path, uid, gid); chErr == nil {
		// Retry write test
		tmp2, err2 := os.CreateTemp(path, ".write-test-*")
		if err2 == nil {
			name := tmp2.Name()
			tmp2.Close()
			os.Remove(name)
			fmt.Printf("[init] fixed ownership of %s to %d:%d\n", path, uid, gid)
			return nil
		}
	}
	return fmt.Errorf(
		"%s is not writable by uid %d.\n"+
			"  Bind-mounted volumes default to root:root on the host.\n"+
			"  Fix with:\n\n"+
			"    sudo chown -R %d:%d <host-path-mounted-to%s>\n\n"+
			"  Then restart the container",
		path, os.Getuid(), uid, gid, path,
	)
}

// chownRecursive applies chown uid:gid to path and all contents.
func chownRecursive(path string, uid, gid int) error {
	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(name, uid, gid)
	})
}

const (
	clamavUID = 4000
	clamavGID = 4000
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
		// Parent dirs first — 0755 root:root so non-root can traverse.
		{"/var", 0755, 0, 0},
		{"/var/lib", 0755, 0, 0},
		{"/var/log", 0755, 0, 0},
		// Leaf dirs with correct ownership
		{"/var/lib/clamav", 0750, clamavUID, clamavGID},
		{"/var/log/clamav", 0750, clamavUID, clamavGID},
		{"/run/clamav", 0755, clamavUID, clamavGID},
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
	if err := ensureWritable(dbDir, clamavUID, clamavGID); err != nil {
		diagVolume(dbDir)
		return err
	}
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
		pattern := filepath.Join(dir, ext)
		matches, err := filepath.Glob(pattern)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[init][WARN] glob %s: %v\n", pattern, err)
			continue
		}
		if len(matches) > 0 {
			fmt.Printf("[init] found signatures: %v\n", matches)
			return true
		}
	}
	fmt.Println("[init] no signatures found in", dir)
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
