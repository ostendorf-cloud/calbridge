// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider

import (
	"fmt"
	"slices"
	"sync"
)

// Registry holds one connector per provider id and is the only way the
// rest of calbridge reaches a provider. A caller resolves a connector by
// the id stored on a connection, so adding a provider never adds a branch
// anywhere above this package.
//
// A Registry is built once in cmd/calbridge's wiring, filled there, and
// read afterwards. It is safe for concurrent use either way, because a
// connector added by a later feature, such as a CalDAV profile the user
// configures, still arrives while the service is serving.
//
// The zero Registry is ready to use.
type Registry struct {
	mu         sync.RWMutex
	connectors map[ID]Connector
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a connector under the id it reports. It fails on an id
// that is not well formed and on an id already taken, so a typo or a
// package imported twice is a startup error rather than a provider that
// silently answers for another.
func (r *Registry) Register(c Connector) error {
	if c == nil {
		return fmt.Errorf("register: %w", ErrNilConnector)
	}

	id := c.ID()
	if err := id.Validate(); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, taken := r.connectors[id]; taken {
		return fmt.Errorf("register %q: %w", id, ErrDuplicateProvider)
	}
	if r.connectors == nil {
		r.connectors = make(map[ID]Connector)
	}
	r.connectors[id] = c

	return nil
}

// Connector returns the connector registered under id. It is the only
// resolution step there is: nothing above this package decides what a
// provider id means.
func (r *Registry) Connector(id ID) (Connector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	c, ok := r.connectors[id]
	if !ok {
		return nil, fmt.Errorf("provider %q: %w", id, ErrUnknownProvider)
	}

	return c, nil
}

// Capabilities returns what the provider registered under id can do,
// without the caller holding on to the connector itself.
func (r *Registry) Capabilities(id ID) (Capabilities, error) {
	c, err := r.Connector(id)
	if err != nil {
		return Capabilities{}, err
	}

	return c.Capabilities(), nil
}

// IDs returns every registered provider id, sorted, so a list the UI
// renders and a log line both read the same on every start.
func (r *Registry) IDs() []ID {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]ID, 0, len(r.connectors))
	for id := range r.connectors {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	return ids
}

// Len returns how many connectors are registered.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.connectors)
}
