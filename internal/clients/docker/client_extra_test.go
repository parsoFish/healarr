package docker

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// This file supplements client_test.go: DiskUsage arithmetic, PruneImages'
// query construction, and the docker-group permission hint.

func TestDiskUsageFromFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/system/df", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "df.json"))
	})
	c := newClient(t, mux)

	du, err := c.DiskUsage(context.Background())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if du.ImagesTotal != 2 {
		t.Errorf("expected ImagesTotal 2, got %d", du.ImagesTotal)
	}
	if du.ImagesActive != 2 {
		t.Errorf("expected ImagesActive 2, got %d", du.ImagesActive)
	}
	if du.ImagesSize != 2362967038 {
		t.Errorf("expected ImagesSize 2362967038, got %d", du.ImagesSize)
	}
	if du.ImagesReclaimable != 0 {
		t.Errorf("expected ImagesReclaimable 0, got %d", du.ImagesReclaimable)
	}
	if du.BuildCacheSize != 0 {
		t.Errorf("expected BuildCacheSize 0 when BuildCache key absent, got %d", du.BuildCacheSize)
	}
}

func TestDiskUsageArithmeticWithReclaimableAndBuildCache(t *testing.T) {
	body := `{
		"LayersSize": 1000,
		"Images": [
			{"Containers": 1, "Size": 100},
			{"Containers": 0, "Size": 50},
			{"Containers": 0, "Size": 25}
		],
		"BuildCache": [{"Size": 10}, {"Size": 5}]
	}`
	mux := http.NewServeMux()
	mux.HandleFunc("/system/df", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	c := newClient(t, mux)

	du, err := c.DiskUsage(context.Background())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if du.ImagesTotal != 3 {
		t.Errorf("expected ImagesTotal 3, got %d", du.ImagesTotal)
	}
	if du.ImagesActive != 1 {
		t.Errorf("expected ImagesActive 1, got %d", du.ImagesActive)
	}
	if du.ImagesSize != 1000 {
		t.Errorf("expected ImagesSize 1000, got %d", du.ImagesSize)
	}
	if du.ImagesReclaimable != 75 {
		t.Errorf("expected ImagesReclaimable 75, got %d", du.ImagesReclaimable)
	}
	if du.BuildCacheSize != 15 {
		t.Errorf("expected BuildCacheSize 15, got %d", du.BuildCacheSize)
	}
}

func TestDiskUsageSurfacesError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/system/df", http.NotFound)
	c := newClient(t, mux)

	if _, err := c.DiskUsage(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestPruneImagesDangling(t *testing.T) {
	var gotQuery, gotMethod string
	mux := http.NewServeMux()
	mux.HandleFunc("/images/prune", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"SpaceReclaimed": 12345}`))
	})
	c := newClient(t, mux)

	reclaimed, err := c.PruneImages(context.Background(), true)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if reclaimed != 12345 {
		t.Errorf("expected 12345, got %d", reclaimed)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	unesc, err := url.QueryUnescape(gotQuery)
	if err != nil {
		t.Fatal(err)
	}
	if unesc != `filters={"dangling":["true"]}` {
		t.Errorf("expected dangling filter, got %q", unesc)
	}
}

func TestPruneImagesNotDangling(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/images/prune", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"SpaceReclaimed": 0}`))
	})
	c := newClient(t, mux)

	if _, err := c.PruneImages(context.Background(), false); err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	unesc, err := url.QueryUnescape(gotQuery)
	if err != nil {
		t.Fatal(err)
	}
	if unesc != `filters={"dangling":["false"]}` {
		t.Errorf("expected non-dangling filter, got %q", unesc)
	}
}

func TestPruneImagesSurfacesAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/prune", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	c := newClient(t, mux)

	_, err := c.PruneImages(context.Background(), false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !httpx.IsStatus(err, http.StatusInternalServerError) {
		t.Errorf("expected *httpx.APIError 500, got %v", err)
	}
}

func TestPruneImagesSurfacesDecodeError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/prune", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	})
	c := newClient(t, mux)

	if _, err := c.PruneImages(context.Background(), false); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestPermissionDeniedYieldsDockerGroupHint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission checks are bypassed")
	}
	sockPath := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if err := os.Chmod(sockPath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	c, err := New(sockPath, "docker")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Containers(context.Background())
	if err == nil {
		t.Fatal("expected permission error")
	}
	if !strings.Contains(err.Error(), "add this user to the docker group") {
		t.Errorf("expected docker group hint, got: %v", err)
	}
	if !strings.Contains(err.Error(), sockPath) {
		t.Errorf("expected socket path %q in error, got: %v", sockPath, err)
	}
}

func TestMissingSocketHasNoDockerGroupHint(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "missing.sock")
	c, err := New(sockPath, "docker")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Containers(context.Background())
	if err == nil {
		t.Fatal("expected dial error for missing socket")
	}
	if strings.Contains(err.Error(), "docker group") {
		t.Errorf("did not expect docker group hint for a missing socket, got: %v", err)
	}
}
