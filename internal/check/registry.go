package check

import (
	"errors"
	"fmt"

	"github.com/parsoFish/healarr/internal/config"
)

// Registry holds every known check keyed by ID. Registration order is
// preserved for deterministic output.
type Registry struct {
	order []string
	byID  map[string]Check
}

func NewRegistry() *Registry { return &Registry{byID: map[string]Check{}} }

// Register adds c; a duplicate ID is a programming error and returns an error.
func (r *Registry) Register(c Check) error {
	if c.ID == "" {
		return errors.New("check: empty id")
	}
	if c.Run == nil {
		return fmt.Errorf("check %s: nil Run", c.ID)
	}
	if len(c.Nodes) == 0 {
		return fmt.Errorf("check %s: no nodes", c.ID)
	}
	if _, dup := r.byID[c.ID]; dup {
		return fmt.Errorf("check %s: already registered", c.ID)
	}
	r.byID[c.ID] = c
	r.order = append(r.order, c.ID)
	return nil
}

// All returns every check in registration order (a copy).
func (r *Registry) All() []Check {
	out := make([]Check, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// ForNode returns the checks that apply to node, in registration order.
func (r *Registry) ForNode(node config.Node) []Check {
	out := make([]Check, 0, len(r.order))
	for _, id := range r.order {
		c := r.byID[id]
		if c.AppliesTo(node) {
			out = append(out, c)
		}
	}
	return out
}

// ByID looks one up.
func (r *Registry) ByID(id string) (Check, bool) {
	c, ok := r.byID[id]
	return c, ok
}
