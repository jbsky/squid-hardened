package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnv(t *testing.T) {
	const key = "C_ICAP_INIT_TEST_VAR"

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

// --- propres a c-icap ---

func TestConfPath(t *testing.T) {
	if got := confPath([]string{"c-icap", "-N", "-D", "-f", "/x/c.conf"}); got != "/x/c.conf" {
		t.Errorf("confPath(-f /x/c.conf) = %q", got)
	}
	if got := confPath([]string{"c-icap", "-N"}); got != defaultConf {
		t.Errorf("confPath() sans -f = %q, attendu %q", got, defaultConf)
	}
	if got := confPath([]string{"c-icap", "-f"}); got != defaultConf {
		t.Errorf("confPath() avec -f sans valeur = %q, attendu %q", got, defaultConf)
	}
}

func TestPidFile(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "c-icap.conf")
	body := "# PidFile /commente\nPort 1344\nPidFile          /run/x/c.pid\n"
	if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := pidFile(conf); got != "/run/x/c.pid" {
		t.Errorf("pidFile() = %q, attendu /run/x/c.pid", got)
	}
	if got := pidFile(filepath.Join(dir, "absent.conf")); got != defaultPidFile {
		t.Errorf("pidFile(conf absente) = %q, attendu %q", got, defaultPidFile)
	}
}

func TestRemoveStalePID(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "c-icap.pid")
	if err := os.WriteFile(pid, []byte("2"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := removeStalePID(pid); err != nil {
		t.Errorf("removeStalePID(present) = %v", err)
	}
	if exists(pid) {
		t.Errorf("PidFile toujours present apres removeStalePID")
	}
	if err := removeStalePID(pid); err != nil {
		t.Errorf("removeStalePID(absent) = %v, attendu nil", err)
	}
	// Un repertoire non vide ne se retire pas : l'erreur doit remonter.
	if err := os.WriteFile(filepath.Join(dir, "plein"), nil, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := removeStalePID(dir); err == nil {
		t.Errorf("removeStalePID(repertoire non vide) = nil, attendu une erreur")
	}
}
