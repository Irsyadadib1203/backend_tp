package provider

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(p Provider) error {
	if r == nil {
		return fmt.Errorf("provider registry is nil")
	}
	if p == nil {
		return fmt.Errorf("provider is nil")
	}
	code := normalizeCode(p.Code())
	if code == "" {
		return fmt.Errorf("provider code is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[code]; exists {
		return fmt.Errorf("provider %q already registered", code)
	}
	r.providers[code] = p
	return nil
}

func (r *Registry) Get(code string) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[normalizeCode(code)]
	return p, ok
}

func (r *Registry) Codes() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	codes := make([]string, 0, len(r.providers))
	for code := range r.providers {
		codes = append(codes, code)
	}
	r.mu.RUnlock()
	sort.Strings(codes)
	return codes
}

func normalizeCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }
