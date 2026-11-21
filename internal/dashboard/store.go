package dashboard

import (
	"cmp"
	"net/netip"
	"slices"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// store keeps the latest messages and what they show about calls and
// registrations. It is the dashboard's own view, built only from the
// copies the nodes send, so it lives in memory: the dashboard is not on
// the path of any call.
type store struct {
	mu            sync.Mutex
	maxMessages   int
	nextID        uint64
	log           []logEntry // in arrival order
	callIDs       map[string]*callIDEntry
	sessions      map[string]*sessionEntry // by Session-Id
	causes        causeIndex
	registrations map[string]Registration // by IMPU
	// legs are, for each icid-value, the Call-IDs of the INVITE sessions
	// whose INVITE carried it (see joinCall).
	legs map[string][]string
}

// logEntry is a kept message with the causes it added, which go with
// it.
type logEntry struct {
	Message
	causes []causeKey
}

// sessionEntry is what the store knows about one Diameter Session-Id
// that still has messages in the log.
type sessionEntry struct {
	messages int
	// callID is the Call-ID of the SIP request that caused the request of
	// the session, once a copy of that request showed it. Every message
	// of the session gets it, also the copies that came before.
	callID string
}

// callIDEntry is what the store knows about one SIP Call-ID that still
// has messages in the log. It goes when the last of them is dropped, so
// calls are bounded with the log.
type callIDEntry struct {
	messages int
	// registerIMPI is the private identity of the last REGISTER, which
	// the 200 to it does not repeat.
	registerIMPI string
	// registrationCSeq is the CSeq of the last 200 to REGISTER or NOTIFY
	// read for registrations. Its other copies, and retransmissions,
	// are skipped, so an expiry counts from the first copy, which
	// usually is the S-CSCF's own.
	registrationCSeq sip.CSeq
	call             *Call // nil until an INVITE is seen
	// icid is the icid-value of the INVITE that started the session, ""
	// until a copy that has one is seen.
	icid string
}

// maxRegistrations bounds the registrations kept, as maxMessages bounds
// the log.
const maxRegistrations = 10000

func newStore(maxMessages int) *store {
	return &store{
		maxMessages:   maxMessages,
		callIDs:       map[string]*callIDEntry{},
		sessions:      map[string]*sessionEntry{},
		causes:        causeIndex{},
		registrations: map[string]Registration{},
		legs:          map[string][]string{},
	}
}

// stateChange is the data of an "event: state": the call or the
// registration that changed, in the shape of /api/calls or
// /api/registrations.
type stateChange struct {
	Call         *Call         `json:"call,omitempty"`
	Registration *Registration `json:"registration,omitempty"`
}

// add numbers the message of c, keeps it, and derives state from it. It
// returns the message as /api/messages has it and the state that
// changed.
func (st *store) add(c capture) (Message, []stateChange) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.nextID++
	c.ID = st.nextID
	entry := logEntry{Message: c.Message}
	m := c.Message
	s := c.sip
	// A copy that the node did not send is one it received, or one it
	// sent to itself.
	if s != nil && c.CallID != "" && c.Direction != DirectionSend {
		src, _ := netip.ParseAddrPort(c.Src)
		entry.causes = causeKeys(s, c.from, src.Addr())
		st.causes.add(entry.causes, cause{c.ID, c.Time, c.CallID})
	}
	if c.diameter != nil && c.CorrelationID != "" {
		m.CallID = st.linkSession(c)
	}
	st.log = append(st.log, entry)
	if len(st.log) > st.maxMessages {
		st.drop(st.log[0])
		st.log = st.log[1:]
	}
	if s == nil || m.CallID == "" {
		return m, nil
	}
	e := st.callIDs[m.CallID]
	if e == nil {
		e = &callIDEntry{}
		st.callIDs[m.CallID] = e
	}
	e.messages++
	var changes []stateChange
	changed := st.applyToCall(e, s, m)
	if st.joinCall(e, s, m.CallID) || changed {
		c := st.callOf(e)
		changes = append(changes, stateChange{Call: &c})
	}
	for _, r := range st.applyToRegistrations(e, s, m) {
		changes = append(changes, stateChange{Registration: &r})
	}
	return m, changes
}

// linkSession counts the Diameter message of c in its session and
// returns the Call-ID the session is linked to. The sender's copy of a
// Cx request links it.
func (st *store) linkSession(c capture) string {
	s := st.sessions[c.CorrelationID]
	if s == nil {
		s = &sessionEntry{}
		st.sessions[c.CorrelationID] = s
	}
	s.messages++
	if s.callID == "" && c.diameter.Request && c.Direction != DirectionRecv {
		s.callID = st.causes.callIDOf(c.diameter, c.from, c.Time)
	}
	return s.callID
}

func (st *store) drop(entry logEntry) {
	st.causes.remove(entry.causes, entry.ID)
	if entry.Protocol == ProtocolDiameter {
		if s := st.sessions[entry.CorrelationID]; s != nil {
			s.messages--
			if s.messages == 0 {
				delete(st.sessions, entry.CorrelationID)
			}
		}
		return
	}
	m := entry.Message
	e := st.callIDs[m.CallID]
	if e == nil {
		return
	}
	e.messages--
	if e.call != nil {
		e.call.Messages = e.messages
	}
	if e.messages == 0 {
		delete(st.callIDs, m.CallID)
		if e.icid != "" {
			st.leaveCall(e.icid, m.CallID)
		}
	}
}

// applyToCall moves the INVITE session of e on s, and reports whether
// that changed it.
func (st *store) applyToCall(e *callIDEntry, s *sip.Message, m Message) bool {
	changed := false
	if e.call == nil {
		if s.Method != sip.INVITE {
			return false
		}
		e.call, changed = newCall(s, m.Time), true
	} else {
		changed = e.call.apply(s, m.Time)
	}
	e.call.Messages = e.messages
	return changed
}

// joinCall puts the INVITE session of e into the call of its
// icid-value, once a copy of the INVITE that started it shows one, and
// reports whether it did.
//
// A routeing B2BUA, such as the MMTel AS, answers the INVITE it got and
// sends a new one, with a Call-ID of its own. It passes on what it does
// not change, and of the P-Charging-Vector it takes out only the
// orig-ioi (TS 24.229 5.7.5.1), so every dialog of the call carries the
// icid-value that the caller's P-CSCF put in (5.2.6.3.3 step 7). The
// UE's own copy of its INVITE has none yet.
func (st *store) joinCall(e *callIDEntry, s *sip.Message, callID string) bool {
	if e.call == nil || e.icid != "" || s.Method != sip.INVITE || hasToTag(s) {
		return false
	}
	icid := icidValue(s)
	if icid == "" {
		return false
	}
	e.icid = icid
	st.legs[icid] = append(st.legs[icid], callID)
	return true
}

// leaveCall takes the session callID out of the call of icid, when its
// last message has gone from the log.
func (st *store) leaveCall(icid, callID string) {
	legs := slices.DeleteFunc(st.legs[icid], func(id string) bool { return id == callID })
	if len(legs) == 0 {
		delete(st.legs, icid)
		return
	}
	st.legs[icid] = legs
}

// callOf is the call that the INVITE session of e is part of, as
// /api/calls lists it.
func (st *store) callOf(e *callIDEntry) Call {
	if e.icid == "" {
		return joined([]*Call{e.call})
	}
	var legs []*Call
	for _, id := range st.legs[e.icid] {
		legs = append(legs, st.callIDs[id].call)
	}
	return joined(legs)
}

// joined is one call made of its INVITE sessions: the one that started
// first, which is the caller's, with the Call-IDs and the messages of
// them all.
func joined(legs []*Call) Call {
	legs = slices.Clone(legs)
	slices.SortFunc(legs, func(a, b *Call) int {
		return cmp.Or(a.Started.Compare(b.Started), cmp.Compare(a.CallID, b.CallID))
	})
	c := *legs[0]
	c.Legs, c.Messages = nil, 0
	for _, l := range legs {
		c.Legs = append(c.Legs, l.CallID)
		c.Messages += l.Messages
	}
	return c
}

func (st *store) applyToRegistrations(e *callIDEntry, s *sip.Message, m Message) []Registration {
	if s.Method == sip.REGISTER {
		if impi := registerIMPI(s); impi != "" {
			e.registerIMPI = impi
		}
		return nil
	}
	registerOK := !s.IsRequest() && cseqMethod(s) == sip.REGISTER && s.StatusCode >= 200 && s.StatusCode < 300
	if s.Method != sip.NOTIFY && !registerOK {
		return nil
	}
	cseq, err := sip.ParseCSeq(s.Get("CSeq"))
	if err != nil || cseq == e.registrationCSeq {
		return nil
	}
	e.registrationCSeq = cseq
	var regs []Registration
	if registerOK {
		regs = registrationsFromRegisterOK(s, e.registerIMPI, m.Time)
	} else {
		regs = registrationsFromNotify(s, m.Time)
	}
	var changed []Registration
	for _, r := range regs {
		old, known := st.registrations[r.IMPU]
		r = r.merge(old)
		if known && r == old {
			continue
		}
		if !known && len(st.registrations) >= maxRegistrations {
			st.dropOldestRegistration()
		}
		st.registrations[r.IMPU] = r
		changed = append(changed, r)
	}
	return changed
}

// dropOldestRegistration drops the identity whose binding ran out or
// ended first. It runs only when the table is full.
func (st *store) dropOldestRegistration() {
	var oldest Registration
	for _, r := range st.registrations {
		if oldest.IMPU == "" || r.Expires.Before(oldest.Expires) {
			oldest = r
		}
	}
	delete(st.registrations, oldest.IMPU)
}

// messages returns the messages that match, in capture order: by the
// time in the HEP packet, then by id. The copies of different nodes
// arrive in no fixed order, so the ids alone are not the capture order.
func (st *store) messages(match func(Message) bool) []Message {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []Message{}
	for _, e := range st.log {
		m := e.Message
		if s := st.sessions[m.CorrelationID]; m.Protocol == ProtocolDiameter && s != nil {
			m.CallID = s.callID
		}
		if match(m) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b Message) int {
		return cmp.Or(a.Time.Compare(b.Time), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// calls returns every call that still has messages, oldest first: the
// INVITE sessions of one icid-value as one call.
func (st *store) calls() []Call {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []Call{}
	listed := map[string]bool{}
	for _, e := range st.callIDs {
		if e.call == nil || e.icid != "" && listed[e.icid] {
			continue
		}
		if e.icid != "" {
			listed[e.icid] = true
		}
		out = append(out, st.callOf(e))
	}
	slices.SortFunc(out, func(a, b Call) int {
		return cmp.Or(a.Started.Compare(b.Started), cmp.Compare(a.CallID, b.CallID))
	})
	return out
}

func (st *store) registrationList() []Registration {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []Registration{}
	for _, r := range st.registrations {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Registration) int { return cmp.Compare(a.IMPU, b.IMPU) })
	return out
}
