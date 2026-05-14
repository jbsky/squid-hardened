// Squid hardened init — replaces entrypoint.sh + healthcheck.
// Static binary, zero shell dependency.
//
// Usage:
//
//	init --healthcheck      run Docker healthcheck (exit 0/1)
//	init --setup-dirs       create runtime directories (build-time, FROM scratch)
//	init [CMD [ARGS...]]    entrypoint: SSL DB, cache, parse-check, then exec CMD
package main

import (
	"bufio"
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
	log("%s is not writable by uid %d, attempting chown to %d:%d", path, os.Getuid(), uid, gid)
	if chErr := chownRecursive(path, uid, gid); chErr == nil {
		// Retry write test
		tmp2, err2 := os.CreateTemp(path, ".write-test-*")
		if err2 == nil {
			name := tmp2.Name()
			tmp2.Close()
			os.Remove(name)
			log("fixed ownership of %s to %d:%d", path, uid, gid)
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
	squidUID = 3128
	squidGID = 3128
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
		// MkdirAll applies the given mode to intermediate dirs it creates,
		// so /var/lib must be explicitly created before /var/lib/ssl_db
		// (whose mode 0750 would otherwise propagate to the parent).
		{"/var", 0755, 0, 0},
		{"/var/lib", 0755, 0, 0},
		{"/var/log", 0755, 0, 0},
		{"/var/spool", 0755, 0, 0},
		// Leaf dirs with correct ownership
		{"/var/log/squid", 0755, squidUID, squidGID},
		{"/var/spool/squid", 0755, squidUID, squidGID},
		{"/var/lib/ssl_db", 0750, squidUID, squidGID},
		{"/run", 0755, 0, 0},
		{"/etc/squid/ssl_cert", 0755, squidUID, squidGID},
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
// Healthcheck: GET cache_object://localhost/info → expect "200"
// ---------------------------------------------------------------------------

func healthcheck() int {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:3128", 2*time.Second)
	if err != nil {
		return 1
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprint(conn, "GET cache_object://localhost/info HTTP/1.0\r\n\r\n")
	sc := bufio.NewScanner(conn)
	if sc.Scan() && strings.Contains(sc.Text(), "200") {
		return 0
	}
	return 1
}

// ---------------------------------------------------------------------------
// Entrypoint
// ---------------------------------------------------------------------------

func entrypoint() error {
	conf := env("SQUID_CONF", "/etc/squid/squid.conf")

	// 0) mime.conf — hidden when /etc/squid is a volume mount
	if !exists("/etc/squid/mime.conf") && exists("/usr/share/squid/mime.conf") {
		log("mime.conf absent, lien depuis /usr/share/squid/")
		_ = os.Symlink("/usr/share/squid/mime.conf", "/etc/squid/mime.conf")
	}

	// 1) SSL Bump init
	confData, err := os.ReadFile(conf)
	if err != nil {
		return fmt.Errorf("read config %s: %w", conf, err)
	}
	confStr := string(confData)

	if strings.Contains(confStr, "sslcrtd_program") || strings.Contains(confStr, "ssl_bump") {
		sslDB := "/var/lib/ssl_db/db"
		if err := ensureWritable("/var/lib/ssl_db", squidUID, squidGID); err != nil {
			diagVolume("/var/lib/ssl_db")
			return err
		}
		if !exists(sslDB + "/index.txt") {
			log("Initialisation de la DB SSL dans %s", sslDB)
			if err := os.RemoveAll(sslDB); err != nil {
				warn("RemoveAll %s: %v", sslDB, err)
			}
			if err := run("/usr/lib/squid/security_file_certgen", "-c", "-s", sslDB, "-M", "20MB"); err != nil {
				return fmt.Errorf("security_file_certgen: %w", err)
			}
		}
		if !exists("/etc/squid/ssl_cert/bump.pem") {
			warn("SSL Bump activé mais /etc/squid/ssl_cert/bump.pem absent.")
			warn("Génère ta CA avec scripts/generate-ca.sh puis monte-la en read-only.")
		}
	}

	// 2) Cache init (if cache_dir directive present and cache empty)
	if lineStartsWith(confStr, "cache_dir") && !exists("/var/spool/squid/00") {
		if err := ensureWritable("/var/spool/squid", squidUID, squidGID); err != nil {
			diagVolume("/var/spool/squid")
			return err
		}
		log("Initialisation du cache Squid")
		_ = run("squid", "-N", "-z", "-f", conf) // best-effort
	}

	// 3) Parse-check
	log("Parse-check de la configuration...")
	if err := run("squid", "-k", "parse", "-f", conf); err != nil {
		return fmt.Errorf("squid -k parse: %w", err)
	}

	// 4) Exec
	log("Démarrage de Squid")
	return execCmd(os.Args[1:])
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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func lineStartsWith(text, prefix string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

func log(format string, a ...any) {
	fmt.Printf("[init] "+format+"\n", a...)
}

func warn(format string, a ...any) {
	fmt.Printf("[init][WARN] "+format+"\n", a...)
}
