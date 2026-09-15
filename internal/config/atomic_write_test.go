package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomicReplacesDestination(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte("debug: true\nport: 8317\n")
	if err := writeFileAtomic(path, payload, 0o600); err != nil {
		t.Fatalf("writeFileAtomic() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") || strings.HasSuffix(entry.Name(), ".replace-bak") {
			t.Fatalf("leftover temp file %s", entry.Name())
		}
	}
}

func TestWriteFileInPlaceFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mounted.yaml")
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte("plugins:\n  enabled: true\n")
	if err := writeFileInPlace(path, payload, 0o600); err != nil {
		t.Fatalf("writeFileInPlace() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestSaveConfigPreserveCommentsUsesPreparedBuffer(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("debug: false\n# keep-me\nport: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Debug: true, Port: 8317}
	if err := SaveConfigPreserveComments(path, cfg); err != nil {
		t.Fatalf("SaveConfigPreserveComments() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "debug: true") {
		t.Fatalf("saved config missing updated debug:\n%s", text)
	}
	if strings.Contains(text, "truncated") {
		t.Fatalf("saved config looks truncated:\n%s", text)
	}
}
