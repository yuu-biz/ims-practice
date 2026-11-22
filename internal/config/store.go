package config

import (
	"errors"
	"fmt"
	"time"
)

// StoreKind is where a node keeps the state of its users.
type StoreKind string

const (
	// StoreMemory keeps the state in the node's own process, so that
	// a role runs as a single instance without a shared store.
	StoreMemory StoreKind = "memory"
	// StoreValkey keeps it in the shared store, where every instance of
	// a role reads it.
	StoreValkey StoreKind = "valkey"
)

// defaultStoreTimeout bounds one request to the store.
const defaultStoreTimeout = time.Second

// Store is the store section. A node without one keeps its state in
// memory.
type Store struct {
	Kind StoreKind
	// Address is the host:port of the store, set for StoreValkey.
	Address string
	// Prefix starts every key the node writes, so that groups of
	// instances that must not share their state, such as two S-CSCF
	// pools, can share one store.
	Prefix  string
	Timeout time.Duration
}

// Maintenance is the maintenance section: where the node serves the
// metrics of its roles at /metrics (internal/metrics) and its
// maintenance API (internal/maintenance).
type Maintenance struct {
	Listen string // host:port
}

type rawStore struct {
	Kind    string        `yaml:"kind"`
	Address string        `yaml:"address"`
	Prefix  string        `yaml:"prefix"`
	Timeout time.Duration `yaml:"timeout"`
}

type rawMaintenance struct {
	Listen string `yaml:"listen"`
}

func (raw rawStore) validate() (Store, error) {
	s := Store{Kind: StoreKind(raw.Kind), Address: raw.Address, Prefix: raw.Prefix, Timeout: raw.Timeout}
	if s.Kind == "" {
		s.Kind = StoreMemory
	}
	if s.Timeout == 0 {
		s.Timeout = defaultStoreTimeout
	}
	switch s.Kind {
	case StoreMemory:
		if s.Address != "" || s.Prefix != "" {
			return Store{}, errors.New("address and prefix: only kind valkey uses them")
		}
	case StoreValkey:
		if err := checkHostPort(s.Address); err != nil {
			return Store{}, fmt.Errorf("address: %w", err)
		}
	default:
		return Store{}, fmt.Errorf("kind: unknown kind %q (known: %s, %s)", raw.Kind, StoreMemory, StoreValkey)
	}
	if s.Timeout < 0 {
		return Store{}, fmt.Errorf("timeout: %v is negative", s.Timeout)
	}
	return s, nil
}

func (raw rawMaintenance) validate() (Maintenance, error) {
	if err := checkHostPort(raw.Listen); err != nil {
		return Maintenance{}, fmt.Errorf("listen: %w", err)
	}
	return Maintenance{Listen: raw.Listen}, nil
}
