package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnv(t *testing.T) {
	const key = "SQUID_INIT_TEST_VAR"

	if got := env(key, "default"); got != "default" {
		t.Errorf("env(%q, %q) = %q, want default value", key, "default", got)
	}

	t.Setenv(key, "custom")
	if got := env(key, "default"); got != "custom" {
		t.Errorf("env(%q, ...) = %q, want %q", key, got, "custom")
	}

	t.Setenv(key, "")
	if got := env(key, "default"); got != "default" {
		t.Errorf("env(%q, ...) with empty value = %q, want fallback %q", key, got, "default")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present")

	if exists(present) {
		t.Errorf("exists(%q) = true before file creation", present)
	}

	if err := os.WriteFile(present, nil, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !exists(present) {
		t.Errorf("exists(%q) = false after file creation", present)
	}

	if exists(filepath.Join(dir, "absent")) {
		t.Errorf("exists() reported true for a path that was never created")
	}
}

func TestWriteOK(t *testing.T) {
	dir := t.TempDir()
	if !writeOK(dir) {
		t.Errorf("writeOK(%q) = false for a writable temp dir", dir)
	}

	if writeOK(filepath.Join(dir, "does-not-exist")) {
		t.Errorf("writeOK() = true for a non-existent directory")
	}
}

// --- propres a squid ---

func TestPidFile(t *testing.T) {
	cases := []struct{ conf, want string }{
		{"http_port 3128\n", defaultPidFile},
		{"# pid_filename /commente\npid_filename /run/x/s.pid\n", "/run/x/s.pid"},
		{"pid_filename /a.pid\npid_filename   /b.pid\n", "/b.pid"},
		{"pid_filename none\n", ""},
	}
	for _, c := range cases {
		if got := pidFile(c.conf); got != c.want {
			t.Errorf("pidFile(%q) = %q, attendu %q", c.conf, got, c.want)
		}
	}
}

func TestRemoveStalePID(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "squid.pid")
	if err := os.WriteFile(pid, []byte("7\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := removeStalePID(pid); err != nil {
		t.Fatalf("removeStalePID(present) = %v", err)
	}
	if exists(pid) {
		t.Error("le fichier pid perime existe toujours")
	}
	if err := removeStalePID(pid); err != nil {
		t.Errorf("removeStalePID(absent) = %v, attendu nil", err)
	}
	if err := removeStalePID(""); err != nil {
		t.Errorf("removeStalePID(none) = %v, attendu nil", err)
	}
	// Un repertoire non vide a la place du fichier : la cause doit remonter.
	blk := filepath.Join(dir, "bloque.pid")
	if err := os.MkdirAll(filepath.Join(blk, "x"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := removeStalePID(blk); err == nil {
		t.Error("removeStalePID(repertoire non vide) = nil, attendu une erreur")
	}
}
