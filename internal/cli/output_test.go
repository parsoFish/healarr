package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestPrintJSONNilSliceIsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	var nilSlice []widget
	if err := Print(&buf, true, nilSlice); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("expected \"[]\" for a nil slice, got %q", got)
	}
}

func TestPrintTablePointerToStructDereferences(t *testing.T) {
	var buf bytes.Buffer
	w := &widget{Name: "ptr", Count: 3}
	if err := Print(&buf, false, w); err != nil {
		t.Fatalf("Print: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got %d lines: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "Name") || !strings.Contains(lines[1], "ptr") {
		t.Errorf("expected dereferenced struct fields, got %q", buf.String())
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

func TestParseSinceTable(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		in      string
		wantErr bool
		checkAt func(t *testing.T, since time.Time)
	}{
		{
			name: "-1h", in: "-1h", wantErr: true,
		},
		{
			name: "-30d", in: "-30d", wantErr: true,
		},
		{
			// Ruling: "0d" is allowed and means now.
			name: "0d", in: "0d", wantErr: false,
			checkAt: func(t *testing.T, since time.Time) {
				t.Helper()
				if d := since.Sub(now).Abs(); d > time.Second {
					t.Errorf("0d should mean ~now, got %v away", d)
				}
			},
		},
		{
			name: "1h30m", in: "1h30m", wantErr: false,
			checkAt: func(t *testing.T, since time.Time) {
				t.Helper()
				age := time.Since(since)
				if age < 89*time.Minute || age > 91*time.Minute {
					t.Errorf("expected ~1h30m ago, got %v", since)
				}
			},
		},
		{
			name: "abc", in: "abc", wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			since, err := parseSince(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSince(%q): expected error, got %v", tc.in, since)
				}
				if strings.Contains(tc.name, "-") && !errors.Is(err, errSinceMustBePositive) {
					t.Errorf("parseSince(%q): expected errSinceMustBePositive, got %v", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSince(%q): unexpected error: %v", tc.in, err)
			}
			if tc.checkAt != nil {
				tc.checkAt(t, since)
			}
		})
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
