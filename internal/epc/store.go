package epc

import (
	"encoding/json"
	"errors"
	"net/netip"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
)

// PDNConnection is a UE's connection to the IMS APN: its address, its
// bearers, and, as the PCRF sees the same thing as an IP-CAN session
// (TS 23.203 3.1), the Rx sessions bound to it (TS 29.213 5.2).
type PDNConnection struct {
	IMSI    string
	Address netip.Addr
	APN     string
	// DefaultBearer carries SIP (QCI 5, IR.92 4.4.1).
	DefaultBearer Bearer
	// VoiceBearer is the dedicated bearer for voice (QCI 1), or nil.
	// IR.92 4.4.2: the UE has one, and it carries the voice of all its
	// calls.
	VoiceBearer *Bearer
	AFSessions  []AFSession
}

// Bearer is one EPS bearer as the network keeps it.
type Bearer struct {
	EPSBearerIdentity uint8
	State             BearerState
	QoS               EPSQoS
	TFT               *TrafficFlowTemplate // dedicated bearers only
}

// AFSession is an Rx session the PCRF has bound to a PDN connection.
type AFSession struct {
	SessionID string
	// AFHost and AFRealm name the AF that opened the session. RAR and ASR
	// go to it (TS 29.214 5.6.3, 5.6.7).
	AFHost  string
	AFRealm string
	// Signalling is true for the session that only watches the signalling
	// path (TS 29.214 4.4.5).
	Signalling      bool
	SpecificActions []rx.SpecificAction
	// Rules are the PCC rules of the session's media, which the voice
	// bearer carries.
	Rules []PCCRule
}

// PCCRule is the dynamic PCC rule the PCRF makes from one media
// component of an AF session (TS 29.213 6.3) and installs in the PDN GW.
type PCCRule struct {
	MediaComponentNumber uint32
	FlowNumbers          []uint32
	Flows                []policy.FlowDescription
	// The bit rates are the requested bandwidths, in bit/s.
	BitRateUL uint32
	BitRateDL uint32
}

// subscribed reports whether the AF of s asked to hear about a.
func (s AFSession) subscribed(a rx.SpecificAction) bool {
	for _, b := range s.SpecificActions {
		if a == b {
			return true
		}
	}
	return false
}

// Binding names the PDN connection an Rx session is bound to, so that an
// STR, which carries only the Session-Id, finds it (TS 29.213 5.2).
type Binding struct {
	Address netip.Addr
	// Aborted is true once the PDN connection has gone and the PCRF has
	// sent the ASR (TS 29.214 4.4.6.1). The AF's STR then ends the
	// session with success.
	Aborted bool
}

// ErrNoPDNConnection: the address has no PDN connection.
var ErrNoPDNConnection = errors.New("epc: no PDN connection")

// Store keeps the PDN connections and the bindings of the Rx sessions,
// so that the state of a UE is not tied to one epc instance and the role can run as several
// instances. Every lookup is by key; nothing walks all UEs.
//
// The event stream of a UE stays on the instance the UE connected to,
// as the S1 connection of a UE stays on one MME.
type Store interface {
	// PutPDNConnection keeps c, in place of any connection of the same
	// address.
	PutPDNConnection(c PDNConnection)
	PDNConnection(addr netip.Addr) (PDNConnection, bool)
	// UpdatePDNConnection runs change on the connection of addr and keeps
	// the result, as one step. change must not do I/O; when it fails,
	// nothing is kept.
	UpdatePDNConnection(addr netip.Addr, change func(*PDNConnection) error) (PDNConnection, error)
	// TakePDNConnection returns the connection of addr and removes it.
	TakePDNConnection(addr netip.Addr) (PDNConnection, bool)

	PutBinding(sessionID string, b Binding)
	Binding(sessionID string) (Binding, bool)
	DeleteBinding(sessionID string)
}

// MemoryStore is a Store in the memory of one process. It keeps every
// PDN connection as JSON, as a store outside the process would, so a
// caller never shares a slice with it.
type MemoryStore struct {
	mu          sync.Mutex
	connections map[netip.Addr][]byte
	bindings    map[string]Binding
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{connections: map[netip.Addr][]byte{}, bindings: map[string]Binding{}}
}

func (s *MemoryStore) PutPDNConnection(c PDNConnection) {
	b := encode(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[c.Address] = b
}

func (s *MemoryStore) PDNConnection(addr netip.Addr) (PDNConnection, bool) {
	s.mu.Lock()
	b, ok := s.connections[addr]
	s.mu.Unlock()
	if !ok {
		return PDNConnection{}, false
	}
	return decode(b), true
}

func (s *MemoryStore) UpdatePDNConnection(addr netip.Addr, change func(*PDNConnection) error) (PDNConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.connections[addr]
	if !ok {
		return PDNConnection{}, ErrNoPDNConnection
	}
	c := decode(b)
	if err := change(&c); err != nil {
		return PDNConnection{}, err
	}
	s.connections[addr] = encode(c)
	return c, nil
}

func (s *MemoryStore) TakePDNConnection(addr netip.Addr) (PDNConnection, bool) {
	s.mu.Lock()
	b, ok := s.connections[addr]
	delete(s.connections, addr)
	s.mu.Unlock()
	if !ok {
		return PDNConnection{}, false
	}
	return decode(b), true
}

func (s *MemoryStore) PutBinding(sessionID string, b Binding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[sessionID] = b
}

func (s *MemoryStore) Binding(sessionID string) (Binding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bindings[sessionID]
	return b, ok
}

func (s *MemoryStore) DeleteBinding(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, sessionID)
}

// encode cannot fail: a PDNConnection holds only strings, numbers,
// addresses and slices of them.
func encode(c PDNConnection) []byte {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return b
}

func decode(b []byte) PDNConnection {
	var c PDNConnection
	if err := json.Unmarshal(b, &c); err != nil {
		panic(err)
	}
	return c
}
