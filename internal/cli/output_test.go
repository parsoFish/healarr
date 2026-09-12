package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type widget struct {
	Name  string
	Count int
}

func TestPrintJSONEmitsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, true, []widget{{Name: "a", Count: 1}}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var out []widget
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(out) != 1 || out[0].Name != "a" || out[0].Count != 1 {
		t.Errorf("unexpected round-trip: %+v", out)
	}
}

func TestPrintJSONMap(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, true, map[string]any{"missing": 3}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var out map[string]int
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out["missing"] != 3 {
		t.Errorf("missing = %d, want 3", out["missing"])
	}
}

func TestPrintTableSliceOfStructsHasHeaderRow(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, false, []widget{{Name: "a", Count: 1}, {Name: "b", Count: 2}}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d lines: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "Name") || !strings.Contains(lines[0], "Count") {
		t.Errorf("header missing field names: %q", lines[0])
	}
	if !strings.Contains(lines[1], "a") || !strings.Contains(lines[2], "b") {
		t.Errorf("rows missing values: %q", buf.String())
	}
}

func TestPrintTableEmptySliceStillHasHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, false, []widget{}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "Name") {
		t.Errorf("expected header-only output, got %q", buf.String())
	}
}

func TestPrintTableSingleStructOneRow(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, false, widget{Name: "solo", Count: 9}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got %d lines: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[1], "solo") || !strings.Contains(lines[1], "9") {
		t.Errorf("row missing values: %q", lines[1])
	}
}

func TestPrintTableMapKeyValueRowsSorted(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, false, map[string]any{"b": 2, "a": 1}); err != nil {
		t.Fatalf("Print: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %q", buf.String())
	}
	if !strings.HasPrefix(lines[0], "a") || !strings.Contains(lines[0], "1") {
		t.Errorf("row 0 = %q, want key a, value 1", lines[0])
	}
	if !strings.HasPrefix(lines[1], "b") || !strings.Contains(lines[1], "2") {
		t.Errorf("row 1 = %q, want key b, value 2", lines[1])
	}
}

func TestPrintTableScalarPlain(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, false, "hello"); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "hello" {
		t.Errorf("expected plain scalar, got %q", buf.String())
	}
}

func TestParseSinceDaysSuffix(t *testing.T) {
	since, err := parseSince("30d")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	age := time.Since(since)
	if age < 29*24*time.Hour || age > 31*24*time.Hour {
		t.Errorf("expected ~30 days ago, got %v", since)
	}
}

func TestParseSinceDuration(t *testing.T) {
	since, err := parseSince("24h")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	age := time.Since(since)
	if age < 23*time.Hour || age > 25*time.Hour {
		t.Errorf("expected ~24h ago, got %v", since)
	}
}

func TestParseSinceEmptyIsZero(t *testing.T) {
	since, err := parseSince("")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if !since.IsZero() {
		t.Errorf("expected zero time for empty since, got %v", since)
	}
}

func TestParseSinceInvalid(t *testing.T) {
	if _, err := parseSince("banana"); err == nil {
		t.Fatal("expected error for invalid --since value")
	}
}

func TestParseIDValidAndInvalid(t *testing.T) {
	id, err := parseID("42")
	if err != nil || id != 42 {
		t.Fatalf("parseID(42) = %d, %v", id, err)
	}
	if _, err := parseID("nope"); err == nil {
		t.Fatal("expected error for non-numeric id")
	}
}

func TestParseOnOff(t *testing.T) {
	if v, err := parseOnOff("on"); err != nil || !v {
		t.Fatalf("parseOnOff(on) = %v, %v", v, err)
	}
	if v, err := parseOnOff("off"); err != nil || v {
		t.Fatalf("parseOnOff(off) = %v, %v", v, err)
	}
	if _, err := parseOnOff("maybe"); err == nil {
		t.Fatal("expected error for invalid on/off value")
	}
}
