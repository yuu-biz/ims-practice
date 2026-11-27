package fivegc

import (
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/n5"
	"github.com/yuu-biz/ims-practice/internal/policy"
)

// UEContext is what the AMF keeps of a registered UE.
type UEContext struct {
	// SUPI is the IMSI of the UE: the SUPI of a 3GPP UE is based on an
	// IMSI (TS 23.501 5.9.2).
	SUPI  string            `json:"supi"`
	State RegistrationState `json:"state"`
}

// PDUSession is a UE's PDU session to the IMS DNN: its address, its QoS
// flows, and, as the PCF sees the same session through its SM policy
// association, the application session contexts of N5 bound to it (TS
// 29.513 6.2).
type PDUSession struct {
	IMSI         string          `json:"imsi"`
	PDUSessionID uint8           `json:"pdu_session_id"`
	Address      netip.Addr      `json:"address"`
	DNN          string          `json:"dnn"`
	State        PDUSessionState `json:"state"`
	// DefaultQoSFlow is the QoS flow of the default QoS rule, which
	// carries SIP (5QI 5) and lasts as long as the session (TS 23.501
	// 5.7.1.1).
	DefaultQoSFlow QoSFlow `json:"default_qos_flow"`
	// VoiceQoSFlow is the GBR QoS flow for voice (5QI 1), or nil. It
	// carries the voice of all the UE's calls.
	VoiceQoSFlow *QoSFlow     `json:"voice_qos_flow,omitempty"`
	AppSessions  []AppSession `json:"app_sessions"`
}

// QoSFlow is one QoS flow as the SMF keeps it: its QoS, and the one QoS
// rule the UE classifies its uplink packets with. The bit rates are in
// kbit/s, and only a GBR flow has them.
type QoSFlow struct {
	QFI          uint8  `json:"qfi"`
	FiveQI       FiveQI `json:"5qi"`
	GFBRUplink   uint32 `json:"gfbr_uplink,omitempty"`
	GFBRDownlink uint32 `json:"gfbr_downlink,omitempty"`
	MFBRUplink   uint32 `json:"mfbr_uplink,omitempty"`
	MFBRDownlink uint32 `json:"mfbr_downlink,omitempty"`
	// QoSRuleIdentifier, QoSRulePrecedence and PacketFilters make the QoS
	// rule of the flow.
	QoSRuleIdentifier uint8          `json:"qos_rule_identifier"`
	QoSRulePrecedence uint8          `json:"qos_rule_precedence"`
	PacketFilters     []PacketFilter `json:"packet_filters"`
}

// qosRule is the QoS rule of the flow, to create in the UE.
func (f QoSFlow) qosRule(isDefault bool) QoSRule {
	return QoSRule{
		QoSRuleIdentifier: f.QoSRuleIdentifier,
		RuleOperationCode: CreateNewQoSRule,
		DQR:               isDefault,
		PacketFilters:     slices.Clone(f.PacketFilters),
		QoSRulePrecedence: f.QoSRulePrecedence,
		QFI:               f.QFI,
	}
}

// qosFlowDescription is the description of the flow, to create in the
// UE.
func (f QoSFlow) qosFlowDescription() QoSFlowDescription {
	return QoSFlowDescription{
		QFI: f.QFI, OperationCode: CreateNewQoSFlowDescription, FiveQI: f.FiveQI,
		GFBRUplink: f.GFBRUplink, GFBRDownlink: f.GFBRDownlink, MFBRUplink: f.MFBRUplink, MFBRDownlink: f.MFBRDownlink,
	}
}

// AppSession is an Individual Application Session Context of N5 that
// the PCF has bound to a PDU session (TS 29.514 4.2.2.2).
type AppSession struct {
	AppSessionID string `json:"app_session_id"`
	// ResourceURI is the URI the AF got in the Location of the Create.
	// The notifications name the context by it (TS 29.514 4.2.5.2,
	// 4.2.5.3).
	ResourceURI string `json:"resource_uri"`
	// NotifURI is ascReqData.notifUri, where the termination request
	// goes (TS 29.514 4.2.5.3).
	NotifURI string `json:"notif_uri"`
	// EvSubsc is the Events Subscription: the events the AF wants and
	// where it hears of them (TS 29.514 4.2.5.2).
	EvSubsc *n5.EventsSubscReqData `json:"ev_subsc,omitempty"`
	// MedComponents is the service information, kept so that an update
	// can be applied to it.
	MedComponents map[string]n5.MediaComponent `json:"med_components,omitempty"`
	// Rules are the PCC rules made from the media, which the voice QoS
	// flow carries.
	Rules []PCCRule `json:"pcc_rules,omitempty"`
}

// subscribed reports whether the AF of s asked to hear about ev.
func (s AppSession) subscribed(ev n5.AfEvent) bool {
	return s.EvSubsc != nil && slices.ContainsFunc(s.EvSubsc.Events, func(e n5.AfEventSubscription) bool { return e.Event == ev })
}

// PCCRule is the dynamic PCC rule the PCF makes from one media
// component of an application session context (TS 29.513 7.3.3) and
// gives the SMF. The bit rates are in bit/s.
type PCCRule struct {
	MedCompN            int                      `json:"med_comp_n"`
	FNums               []int                    `json:"f_nums"`
	FiveQI              FiveQI                   `json:"5qi"`
	Flows               []policy.FlowDescription `json:"flows"`
	MaxBitRateUL        uint64                   `json:"max_bit_rate_ul"`
	MaxBitRateDL        uint64                   `json:"max_bit_rate_dl"`
	GuaranteedBitRateUL uint64                   `json:"guaranteed_bit_rate_ul"`
	GuaranteedBitRateDL uint64                   `json:"guaranteed_bit_rate_dl"`
}

// Binding names the PDU session an application session context is
// bound to, so that an Update or a Delete, which carry only the
// appSessionId, find it (TS 29.513 6.2).
type Binding struct {
	Address netip.Addr `json:"address"`
	// Terminated is true once the PCF has asked the AF to delete the
	// context (TS 29.514 4.2.5.3). The Delete that the AF then sends
	// ends the context with success.
	Terminated bool `json:"terminated,omitempty"`
}

// Errors of the Store.
var (
	ErrNotRegistered = errors.New("fivegc: the UE is not registered")
	ErrNoPDUSession  = errors.New("fivegc: no PDU session")
)

// Store keeps the UE contexts, the PDU sessions and the bindings of the
// application session contexts, so that the state of a UE is not tied
// to one 5gc instance and several instances can run. Every lookup is by
// key; nothing
// walks all UEs.
//
// The event stream of a UE stays on the instance the UE connected to,
// as the N1 connection of a UE stays on one AMF.
type Store interface {
	PutUEContext(c UEContext)
	UEContext(supi string) (UEContext, bool)
	// TakeUEContext returns the context of supi and removes it.
	TakeUEContext(supi string) (UEContext, bool)

	// PutPDUSessionIfRegistered keeps s, in place of any session of the
	// same address, only while the UE of s.IMSI is registered, and
	// otherwise returns ErrNotRegistered and keeps nothing. The check and
	// the keeping are one step: a deregistration of the UE that
	// completes between them would leave a PDU session that belongs to
	// no registration. A store outside the process does both under the
	// key of the subscriber, with no lock on all UEs.
	PutPDUSessionIfRegistered(s PDUSession) error
	PDUSession(addr netip.Addr) (PDUSession, bool)
	// UpdatePDUSession runs change on the session of addr and keeps the
	// result, as one step. change must not do I/O; when it fails,
	// nothing is kept.
	UpdatePDUSession(addr netip.Addr, change func(*PDUSession) error) (PDUSession, error)
	// TakePDUSession returns the session of addr and removes it.
	TakePDUSession(addr netip.Addr) (PDUSession, bool)

	PutBinding(appSessionID string, b Binding)
	Binding(appSessionID string) (Binding, bool)
	DeleteBinding(appSessionID string)
}

// MemoryStore is a Store in the memory of one process. It keeps every
// record as JSON, as a store outside the process would, so a caller
// never shares a slice or a map with it.
type MemoryStore struct {
	mu         sync.Mutex
	ueContexts map[string][]byte
	sessions   map[netip.Addr][]byte
	bindings   map[string]Binding
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{ueContexts: map[string][]byte{}, sessions: map[netip.Addr][]byte{}, bindings: map[string]Binding{}}
}

func (s *MemoryStore) PutUEContext(c UEContext) {
	b := encode(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ueContexts[c.SUPI] = b
}

func (s *MemoryStore) UEContext(supi string) (UEContext, bool) {
	s.mu.Lock()
	b, ok := s.ueContexts[supi]
	s.mu.Unlock()
	if !ok {
		return UEContext{}, false
	}
	return decode[UEContext](b), true
}

func (s *MemoryStore) TakeUEContext(supi string) (UEContext, bool) {
	s.mu.Lock()
	b, ok := s.ueContexts[supi]
	delete(s.ueContexts, supi)
	s.mu.Unlock()
	if !ok {
		return UEContext{}, false
	}
	return decode[UEContext](b), true
}

func (s *MemoryStore) PutPDUSessionIfRegistered(p PDUSession) error {
	b := encode(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ueContexts[p.IMSI]; !ok {
		return ErrNotRegistered
	}
	s.sessions[p.Address] = b
	return nil
}

func (s *MemoryStore) PDUSession(addr netip.Addr) (PDUSession, bool) {
	s.mu.Lock()
	b, ok := s.sessions[addr]
	s.mu.Unlock()
	if !ok {
		return PDUSession{}, false
	}
	return decode[PDUSession](b), true
}

func (s *MemoryStore) UpdatePDUSession(addr netip.Addr, change func(*PDUSession) error) (PDUSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.sessions[addr]
	if !ok {
		return PDUSession{}, ErrNoPDUSession
	}
	p := decode[PDUSession](b)
	if err := change(&p); err != nil {
		return PDUSession{}, err
	}
	s.sessions[addr] = encode(p)
	return p, nil
}

func (s *MemoryStore) TakePDUSession(addr netip.Addr) (PDUSession, bool) {
	s.mu.Lock()
	b, ok := s.sessions[addr]
	delete(s.sessions, addr)
	s.mu.Unlock()
	if !ok {
		return PDUSession{}, false
	}
	return decode[PDUSession](b), true
}

func (s *MemoryStore) PutBinding(appSessionID string, b Binding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[appSessionID] = b
}

func (s *MemoryStore) Binding(appSessionID string) (Binding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bindings[appSessionID]
	return b, ok
}

func (s *MemoryStore) DeleteBinding(appSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, appSessionID)
}

// encode cannot fail: the records hold only strings, numbers,
// addresses, and slices and maps of them.
func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func decode[T any](b []byte) T {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		panic(err)
	}
	return v
}
