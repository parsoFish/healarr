package docker

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newUnixServer starts an httptest server bound to a real unix socket in a
// temp dir, so New's DialContext is exercised end to end. It returns the
// server (already started) and the socket path to pass to New.
func newUnixServer(t *testing.T, handler http.Handler) (*httptest.Server, string) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	if err := srv.Listener.Close(); err != nil {
		t.Fatalf("close default listener: %v", err)
	}
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, sockPath
}

func newClient(t *testing.T, handler http.Handler) *HTTPClient {
	t.Helper()
	_, sockPath := newUnixServer(t, handler)
	c, err := New(sockPath, "docker")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNewRequiresBinary(t *testing.T) {
	if _, err := New("/var/run/docker.sock", ""); err == nil {
		t.Fatal("expected error for empty binary")
	}
}

func TestContainersMergesInspectData(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("all"); got != "1" {
			t.Errorf("expected all=1, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "containers.json"))
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/json") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "inspect.json"))
	})
	c := newClient(t, mux)

	list, err := c.Containers(context.Background())
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(list))
	}
	if list[0].Name != "prowlarr" {
		t.Errorf("expected Name %q, got %q", "prowlarr", list[0].Name)
	}
	if list[1].Name != "tautulli" {
		t.Errorf("expected Name %q, got %q", "tautulli", list[1].Name)
	}
	if list[0].Health != "healthy" {
		t.Errorf("expected Health healthy, got %q", list[0].Health)
	}
	if list[0].RestartCount != 0 {
		t.Errorf("expected RestartCount 0, got %d", list[0].RestartCount)
	}
	want, err := time.Parse(time.RFC3339Nano, "2026-09-12T15:34:12.447333468Z")
	if err != nil {
		t.Fatal(err)
	}
	if !list[0].StartedAt.Equal(want) {
		t.Errorf("expected StartedAt %v, got %v", want, list[0].StartedAt)
	}
	if list[0].State != "running" || list[0].Status == "" {
		t.Errorf("expected summary fields to survive merge, got %+v", list[0])
	}
}

func TestContainersSurfacesListError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", http.NotFound)
	c := newClient(t, mux)

	if _, err := c.Containers(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// TestContainersKeepsSummaryRowWhenInspect404s covers a container removed
// mid-listing: /containers/json still reports it, but its own
// /containers/{id}/json inspect now 404s. That single 404 must not fail the
// whole listing; the summary row is kept with zero Health/StartedAt/
// RestartCount instead.
func TestContainersKeepsSummaryRowWhenInspect404s(t *testing.T) {
	const removedID = "2003f0a938aba30485c40ea7bf110b77962ba26f9abcc0ea4d1b4af6dd46007a" // tautulli, per containers.json
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "containers.json"))
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, removedID) {
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/json") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "inspect.json"))
	})
	c := newClient(t, mux)

	list, err := c.Containers(context.Background())
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 containers despite one inspect 404, got %d", len(list))
	}
	if list[0].Name != "prowlarr" || list[0].Health != "healthy" {
		t.Errorf("expected the successfully-inspected container's data intact, got %+v", list[0])
	}
	removed := list[1]
	if removed.Name != "tautulli" || removed.ID != removedID {
		t.Fatalf("expected the removed container's summary row to survive, got %+v", removed)
	}
	if removed.Health != "" || removed.RestartCount != 0 || !removed.StartedAt.IsZero() {
		t.Errorf("expected zero-valued inspect fields for a 404'd container, got %+v", removed)
	}
	if removed.State != "running" || removed.Status == "" {
		t.Errorf("expected summary-only fields to still be populated, got %+v", removed)
	}
}

// TestContainersSurfacesInspectError covers a non-404 inspect failure (a 404
// is handled separately: see TestContainersKeepsSummaryRowWhenInspect404s).
func TestContainersSurfacesInspectError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "containers.json"))
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	c := newClient(t, mux)

	if _, err := c.Containers(context.Background()); err == nil {
		t.Fatal("expected error when inspect fails")
	}
}

func TestContainersSurfacesStartedAtParseError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "containers.json"))
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"State":{"StartedAt":"not-a-time"},"RestartCount":0}`))
	})
	c := newClient(t, mux)

	if _, err := c.Containers(context.Background()); err == nil {
		t.Fatal("expected error for malformed StartedAt")
	}
}

func TestContainersHandlesNeverStartedContainer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "containers.json"))
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"State":{"StartedAt":"0001-01-01T00:00:00Z","Health":null},"RestartCount":0}`))
	})
	c := newClient(t, mux)

	list, err := c.Containers(context.Background())
	if err != nil {
		t.Fatalf("Containers: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected at least one container")
	}
	for _, cont := range list {
		if !cont.StartedAt.IsZero() {
			t.Errorf("expected zero StartedAt for a never-started container, got %v", cont.StartedAt)
		}
		if cont.Health != "" {
			t.Errorf("expected empty Health when State.Health is null, got %q", cont.Health)
		}
	}
}

func frame(streamType byte, payload string) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = streamType
	binary.BigEndian.PutUint32(b[4:8], uint32(len(payload)))
	copy(b[8:], payload)
	return b
}

// inspectHandler serves a minimal inspect body reporting the given Tty
// value, for tests that only care about Logs' tty-detection branch.
func inspectHandler(tty bool) http.HandlerFunc {
	body := []byte(`{"Config":{"Tty":false}}`)
	if tty {
		body = []byte(`{"Config":{"Tty":true}}`)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func TestLogsStripsStreamHeadersWhenNotTTY(t *testing.T) {
	body := append(frame(1, "hello "), frame(2, "world")...)
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/sonarr/json", inspectHandler(false))
	mux.HandleFunc("/containers/sonarr/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stdout") != "1" || r.URL.Query().Get("stderr") != "1" {
			t.Errorf("expected stdout=1&stderr=1, got %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("tail") != "100" {
			t.Errorf("expected tail=100, got %s", r.URL.Query().Get("tail"))
		}
		_, _ = w.Write(body)
	})
	c := newClient(t, mux)

	out, err := c.Logs(context.Background(), "sonarr", 100)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out != "hello world" {
		t.Errorf("expected stripped log %q, got %q", "hello world", out)
	}
}

func TestLogsPassesThroughRawWhenInspectReportsTTY(t *testing.T) {
	raw := "plain tty output\nline two\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/sonarr/json", inspectHandler(true))
	mux.HandleFunc("/containers/sonarr/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(raw))
	})
	c := newClient(t, mux)

	out, err := c.Logs(context.Background(), "sonarr", 50)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out != raw {
		t.Errorf("expected raw passthrough %q, got %q", raw, out)
	}
}

func TestLogsFallsBackToRawWhenTTYFalseButBodyNotFramed(t *testing.T) {
	// Defensive fallback: inspect said Tty == false (a framed stream is
	// expected), but the body doesn't actually look framed. demultiplex
	// must return it unchanged rather than mangle it.
	raw := "not actually framed\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/sonarr/json", inspectHandler(false))
	mux.HandleFunc("/containers/sonarr/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(raw))
	})
	c := newClient(t, mux)

	out, err := c.Logs(context.Background(), "sonarr", 50)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out != raw {
		t.Errorf("expected raw fallback %q, got %q", raw, out)
	}
}

func TestLogsHandlesTruncatedFrame(t *testing.T) {
	full := frame(1, "hello")
	truncated := full[:len(full)-2] // header claims 5 bytes, only 3 delivered
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/sonarr/json", inspectHandler(false))
	mux.HandleFunc("/containers/sonarr/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(truncated)
	})
	c := newClient(t, mux)

	out, err := c.Logs(context.Background(), "sonarr", 10)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out != "hel" {
		t.Errorf("expected truncated payload %q, got %q", "hel", out)
	}
}

func TestLogsSurfacesTransportError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/sonarr/json", inspectHandler(false))
	mux.HandleFunc("/containers/sonarr/logs", http.NotFound)
	c := newClient(t, mux)

	if _, err := c.Logs(context.Background(), "sonarr", 10); err == nil {
		t.Fatal("expected error")
	}
}

func TestLogsSurfacesInspectErrorWithoutFetchingLogs(t *testing.T) {
	logsCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/sonarr/json", http.NotFound)
	mux.HandleFunc("/containers/sonarr/logs", func(w http.ResponseWriter, r *http.Request) {
		logsCalled = true
	})
	c := newClient(t, mux)

	if _, err := c.Logs(context.Background(), "sonarr", 10); err == nil {
		t.Fatal("expected inspect error")
	}
	if logsCalled {
		t.Error("expected Logs to short-circuit on inspect failure without fetching logs")
	}
}
