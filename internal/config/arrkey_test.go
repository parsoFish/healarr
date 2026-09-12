package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadArrAPIKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.xml")
	if err := os.WriteFile(p, []byte("<Config>\n  <ApiKey>0123abcd</ApiKey>\n</Config>"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadArrAPIKey(p)
	if err != nil || got != "0123abcd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ReadArrAPIKey(filepath.Join(t.TempDir(), "missing.xml")); err == nil {
		t.Fatal("expected error for missing file")
	}
	empty := filepath.Join(t.TempDir(), "empty.xml")
	if err := os.WriteFile(empty, []byte("<Config></Config>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArrAPIKey(empty); err == nil {
		t.Fatal("expected error when ApiKey element missing")
	}
}
