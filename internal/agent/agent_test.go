package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// Compile-time assertion (constraints.md): *store.Store must satisfy
// agent.Store with the exact signatures declared here.
var _ Store = (*store.Store)(nil)

func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// validOptions returns an Options that New accepts, for tests that only
// want to flip one field.
func validOptions() Options {
	return Options{
		Cfg:      config.Config{Node: config.NodePi},
		Registry: check.NewRegistry(),
		Deps:     fakeDeps(check.Deps{}, nil),
		Store:    &fakeStore{},
		Now:      fixedNow(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)),
		Version:  "test-version",
	}
}

func TestNewRejectsMissingRequiredFields(t *testing.T) {
	cases := map[string]func(*Options){
		"cfg.node": func(o *Options) { o.Cfg.Node = "" },
		"registry": func(o *Options) { o.Registry = nil },
		"deps":     func(o *Options) { o.Deps = nil },
		"store":    func(o *Options) { o.Store = nil },
		"now":      func(o *Options) { o.Now = nil },
		"version":  func(o *Options) { o.Version = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := validOptions()
			mutate(&o)
			if _, err := New(o); !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("New() err = %v, want wrapping ErrInvalidOptions", err)
			}
		})
	}
}

func TestNewAcceptsNilPeerAndSender(t *testing.T) {
	o := validOptions()
	o.Peer = nil
	o.Sender = nil
	a, err := New(o)
	if err != nil {
		t.Fatalf("New() err = %v, want nil", err)
	}
	if a.HasSender() {
		t.Fatal("HasSender() = true, want false with Sender nil")
	}
}

func TestNewDefaultsNilLogger(t *testing.T) {
	o := validOptions()
	o.Logger = nil
	a, err := New(o)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if a.logger == nil {
		t.Fatal("logger not defaulted")
	}
}

func TestHasSenderReflectsOption(t *testing.T) {
	o := validOptions()
	o.Sender = &notify.FakeSender{}
	a, err := New(o)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if !a.HasSender() {
		t.Fatal("HasSender() = false, want true with Sender set")
	}
}

func TestNewAcceptsPeer(t *testing.T) {
	o := validOptions()
	o.Peer = &peer.Fake{}
	if _, err := New(o); err != nil {
		t.Fatalf("New() err = %v, want nil", err)
	}
}
