// Package agw is the media node of this project. One implementation
// serves two roles:
//
//   - the IMS Access Gateway (IMS-AGW, TS 23.334), which the P-CSCF
//     controls over Iq. The P-CSCF asks for a context with two
//     terminations, one towards the UE (access) and one towards the
//     network (core), puts their addresses into the SDP, and RTP and
//     RTCP then flow through the relay;
//   - the Multimedia Resource Function Processor (MRFP, TS 23.228 4.7),
//     which the MRFC controls over Mp.
//
// A context holds any number of terminations (TS 29.333 5.4). Media
// that enters one termination leaves through the others, as the mode
// of each termination allows. With two terminations the relay passes
// the packets on as they are. With three or more the context is a
// conference (TS 23.333 6.2.9.1), and each termination gets the mix of
// the others, made on a clock of 20 ms. A termination can also play an
// announcement, and report its end to the controller.
//
// The control interface is a small HTTP API. Its operations
// carry the names and the granularity of the H.248 commands that Iq and
// Mp use instead (H.248.1 (03/2013) 7): Add puts a termination into a
// context and creates the context with the first one, Modify changes
// the descriptors of a termination, Subtract removes it and deletes the
// context with the last one, and Notify reports an event to the
// controller. A request carries one field per H.248 descriptor: Mode
// for LocalControl, Remote, Signals and Events, so that H.248 can carry
// the same operations.
package agw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mixer"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// TerminationID names a termination within its context.
type TerminationID string

// The P-CSCF names the two terminations of its contexts after the
// network each one faces. An MRFC leaves the names to the relay.
const (
	Access TerminationID = "access" // towards the UE
	Core   TerminationID = "core"   // towards the rest of the network
)

// Mode is the StreamMode of a termination, which H.248 carries in the
// LocalControl descriptor (H.248.1 (03/2013) 7.1.7.1.1). Sending and
// receiving are seen from outside the context: a SendOnly termination
// passes media from the context to its remote party, a ReceiveOnly one
// from its remote party into the context. The values are those of the
// SDP direction attributes, so that the direction of an SDP answer that
// an MRFC writes for its termination is the mode of that termination.
//
// The mode only affects media. RTCP, the control flow of the stream,
// passes whatever the mode (the NOTE of 7.1.7.1.1).
type Mode string

const (
	SendReceive Mode = "sendrecv"
	SendOnly    Mode = "sendonly"
	ReceiveOnly Mode = "recvonly"
	Inactive    Mode = "inactive"
)

func (m Mode) valid() bool {
	return m == SendReceive || m == SendOnly || m == ReceiveOnly || m == Inactive
}

func (m Mode) sends() bool    { return m == SendReceive || m == SendOnly }
func (m Mode) receives() bool { return m == SendReceive || m == ReceiveOnly }

// Latch is the latching of a termination: whether it sends to the
// remote address that its controller gives, or to the source of the
// packets it receives. A UE behind NAT sends from an address that its
// SDP cannot know, and latching still reaches it. The first three
// values are those of the parameter napt, with which H.248 asks for
// latching in the signal ipnapt/latch (H.248.37 (06/2008) 6.3.1.1.1).
// The fourth is what a Signals descriptor without that signal does to
// a termination (6.6.4), which none of the three does. RTP and RTCP
// latch each on their own, by the same rule (6.3.1.1.1 applies napt to
// every flow of a stream).
type Latch string

const (
	// NAPTOff sends to the remote address, also after a latch (6.6.1).
	NAPTOff Latch = "off"
	// NAPTLatch takes the source of the first packet that arrives as
	// the address to send to, and keeps it (6.6.2). Until that packet,
	// the termination sends where it did before.
	NAPTLatch Latch = "latch"
	// NAPTRelatch is NAPTLatch for the first packet from another source
	// than the address that the termination sends to: it follows a
	// party that has moved (6.6.3).
	NAPTRelatch Latch = "relatch"
	// StopLatching stops a NAPTLatch or a NAPTRelatch that still waits
	// for its packet, and keeps sending where the termination sends
	// now. Unlike NAPTOff, it keeps an address that the termination has
	// latched (6.6.4). On Add there is nothing to stop yet, and it does
	// what NAPTOff does.
	StopLatching Latch = "stop"
)

func (l Latch) valid() bool {
	return l == NAPTOff || l == NAPTLatch || l == NAPTRelatch || l == StopLatching
}

// TerminationRequest is what Add and Modify set on one termination: one
// field for each descriptor of the H.248 command (H.248.1 (03/2013)
// 7.1). A field left at its zero value keeps what the termination has.
type TerminationRequest struct {
	// ID names the termination on Add. Empty lets the relay choose, as
	// the CHOOSE wildcard $ does in H.248.
	ID TerminationID `json:"id,omitempty"`
	// Mode is the StreamMode. An Add without one gives SendReceive,
	// where H.248 would default to Inactive.
	Mode Mode `json:"mode,omitempty"`
	// LocalMedia, on Add, describes the stream that the termination is
	// to receive, as the Local descriptor does besides the address,
	// which the node chooses.
	LocalMedia Media `json:"-"`
	// Remote is where the termination sends RTP, as the SDP of the party
	// it faces gives it: the Remote descriptor. RTCP goes to the port
	// above.
	Remote netip.AddrPort `json:"remote,omitzero"`
	// RemoteMedia describes the stream that the termination sends to
	// Remote, as the Remote descriptor does besides the address.
	RemoteMedia Media `json:"-"`
	// Latch is the latching. An Add without one gives NAPTLatch, where
	// H.248 would leave latching off (H.248.37 (06/2008) 6.6.1); a
	// Modify without one keeps it.
	Latch Latch `json:"latch,omitempty"`
	// Signals, when not nil, replaces what the termination plays.
	Signals *Signals `json:"signals,omitempty"`
	// Events, when not nil, replaces the events that the termination
	// reports.
	Events *Events `json:"events,omitempty"`
}

// Media is what the SDP of a Local or a Remote descriptor says of an
// audio stream besides its address: the payload formats of the m= line
// and the bandwidth of the b= line (TS 29.334 5.15). The relay does not
// transcode and passes every payload type as it is, so the HTTP API
// does not carry it; the H.248 Controller of internal/h248/mediactl
// writes it into its descriptors.
type Media struct {
	// Codecs are the payload formats of the m= line, with their
	// a=rtpmap and a=fmtp lines, most preferred first. With none, the
	// m= line carries "-": no resources for a particular format are
	// reserved (TS 29.334 5.15, Table 5.15.1).
	Codecs []sdp.Codec
	// Bandwidth is the b=AS value in kbit/s, or 0 for none.
	Bandwidth int
}

// Termination is one end of a context as the API shows it: a pair of
// local UDP ports, RTP on the even one and RTCP on the odd one above it
// (RFC 3550 11), and the remote addresses that packets leaving through
// them go to.
type Termination struct {
	ID     TerminationID  `json:"id"`
	Mode   Mode           `json:"mode"`
	Local  netip.AddrPort `json:"local"`
	Remote netip.AddrPort `json:"remote,omitzero"`
	// Latched tells that Remote is the source of a packet that arrived,
	// which the termination latched onto, rather than the address of the
	// SDP.
	Latched bool `json:"latched"`
	// Packets counts the RTP packets received on this termination, and
	// PacketsSent those that left through it. They are the statistics
	// that H.248 returns on Subtract.
	Packets     uint64 `json:"packets"`
	PacketsSent uint64 `json:"packets_sent"`
	// The same for RTCP. RTCPRemote starts as the port above Remote,
	// and latches on its own: behind NAT the RTCP port of a UE need not
	// be its RTP port plus one.
	RTCPLocal   netip.AddrPort `json:"rtcp_local"`
	RTCPRemote  netip.AddrPort `json:"rtcp_remote,omitzero"`
	RTCPLatched bool           `json:"rtcp_latched"`
	RTCPPackets uint64         `json:"rtcp_packets"`
	// Signals is what the termination plays now, and Events what it
	// reports.
	Signals *Signals `json:"signals,omitempty"`
	Events  *Events  `json:"events,omitempty"`
}

// Context is an association of terminations: one call leg through the
// IMS-AGW, or one announcement or conference in the MRFP.
type Context struct {
	ID           string                         `json:"id"`
	Terminations map[TerminationID]*Termination `json:"terminations"`
}

// Relay owns the contexts and their sockets.
type Relay struct {
	address       netip.Addr // the address written into SDP
	ports         *portPool
	announcements Announcements
	notifier      *http.Client
	store         ContextStore
	log           *slog.Logger

	// managed is the administrative state that the maintenance API of
	// the node keeps for this relay (maintenance.go).
	managed atomic.Pointer[maintenance.ManagedFunction]

	mu       sync.Mutex
	contexts map[string]*relayContext
	// adding counts the Adds that have begun and whose context is not
	// in contexts yet. It falls under mu when the context goes in, so
	// that a count under mu sees each context once (countContexts).
	adding atomic.Int64
	nextID atomic.Uint64
}

// NewRelay relays on address, with RTP ports taken from [minPort,
// maxPort]. announcements are what it can play; the IMS-AGW has none.
// store is where the relay keeps its contexts, so that a relay created
// again under the same name finds them; nil keeps them in the memory of
// this process, which they go with.
func NewRelay(address netip.Addr, minPort, maxPort uint16, announcements Announcements, store ContextStore, log *slog.Logger) (*Relay, error) {
	if minPort == 0 || maxPort < minPort {
		return nil, fmt.Errorf("agw: port range %d-%d", minPort, maxPort)
	}
	if store == nil {
		store = NewMemoryContextStore()
	}
	r := &Relay{
		address: address, ports: newPortPool(minPort, maxPort), announcements: announcements,
		notifier: &http.Client{Timeout: RequestTimeout}, store: store, log: log, contexts: map[string]*relayContext{},
	}
	// A context ID from before a restart must not name a new context,
	// or a controller that still holds the old ID would change a
	// context it does not own. The IDs start at a random number, as the
	// context IDs of an H.248 media gateway do.
	r.nextID.Store(rand.Uint64N(1 << 32))
	if err := r.restore(); err != nil {
		return nil, err
	}
	return r, nil
}

// restore opens again the ports of the contexts that an earlier process
// of this relay left in the store, with the mode and the remote address
// each termination had: the media of a call goes on after the gap, and
// the controller, which knows nothing of the restart, goes on changing
// and subtracting the contexts it has.
//
// A relay whose store cannot be read does not start. One that started
// without its contexts would leave the calls of its earlier process
// without media and answer their Subtract with 404, while its ports
// stayed open; a controller takes the next relay of its list meanwhile.
//
// SIMPLIFIED: TS 29.334 5.17.2.5. A context whose removal from the
// store never succeeded, after the controller released it, cannot be
// told from one of a call that is still up: this relay and every later
// process of it open its ports again and hold them, until the store is
// cleared. Nothing of the call is left to ask, and the items of a
// relay have no time to live, as those of the IBCF and the P-CSCF
// dialogs have none.
//
// SIMPLIFIED: TS 29.333 5.17.2.9. A termination opened again plays
// nothing: an announcement or a tone that an MRFP was playing does not
// start again, and its controller gets no event for an end that never
// comes. The media the relay makes itself comes from a new RTP source
// after the gap, and the packet counts of the terminations start at
// zero. What a termination had played of an announcement is not kept
// with the context, and the controller starts the next announcement
// itself.
func (r *Relay) restore() error {
	saved, err := r.store.Contexts(context.Background())
	if err != nil {
		return fmt.Errorf("agw: cannot read the contexts that an earlier process left: %w", err)
	}
	for _, c := range saved {
		r.reopen(c)
	}
	return nil
}

// reopen makes one stored context again. A context whose ports it
// cannot open, because something else holds them or the range of the
// relay no longer covers them, is given up: it is removed from the
// store, so that no later process tries it again, and its controller
// finds it gone, as it would a context of a relay that never came back.
func (r *Relay) reopen(c SavedContext) {
	rc := &relayContext{id: c.ID, relay: r, terms: map[TerminationID]*relayTermination{}, mix: mixer.NewConference()}
	rc.mu.Lock()
	for _, saved := range c.Terminations {
		if err := rc.reopen(saved); err != nil {
			rc.closed = true
			rc.mu.Unlock()
			rc.close()
			r.log.Warn("a context of an earlier process was given up: its ports could not be opened again",
				"context", c.ID, "termination", string(saved.ID), "port", saved.LocalRTP, "error", err.Error())
			r.forget(c.ID)
			return
		}
	}
	rc.startClock()
	view := rc.view()
	rc.mu.Unlock()
	r.mu.Lock()
	r.contexts[c.ID] = rc
	r.mu.Unlock()
	r.log.Info("context of an earlier process opened again", "context", c.ID, "terminations", view.summary())
}

var (
	// ErrNoContext means the context does not exist.
	ErrNoContext = errors.New("agw: no such context")
	// ErrNoTermination means the context has no termination of that ID.
	ErrNoTermination = errors.New("agw: no such termination")
	// ErrNoPort means every port of the range is in use.
	ErrNoPort = errors.New("agw: no free port")
	// ErrBadRequest means a request that the relay cannot carry out as
	// it stands, such as an unknown mode, or a termination ID that the
	// context has already.
	ErrBadRequest = errors.New("agw: bad request")
)

// Add creates a context with the given terminations, as one transaction
// of H.248 Add commands to a new context does. It fails as a whole.
//
// The context is in the store before the controller hears of it: a
// context that the relay reserved ports for but could not remember
// would be gone after a restart, with its ports open and nobody left to
// subtract it. A controller whose Add fails takes the next relay of its
// list instead.
//
// A relay that the operator has taken out of service makes no context
// (maintenance.go). It is refused here, before any port is reserved.
// The Add is counted among the users of the relay before the state is
// read, and until its context is in the list or it has failed: the write
// to the store comes in between and may take a round trip to another
// node, and a shut down that read no user in that time would report
// the relay LOCKED with a call about to pass through it.
func (r *Relay) Add(terminations ...TerminationRequest) (_ Context, err error) {
	r.adding.Add(1)
	defer func() {
		if err != nil {
			r.adding.Add(-1)
			r.contextGone()
		}
	}()
	if !r.unlocked() {
		return Context{}, ErrOutOfService
	}
	if len(terminations) == 0 {
		return Context{}, fmt.Errorf("%w: a context needs at least one termination", ErrBadRequest)
	}
	rc := &relayContext{id: r.newID(), relay: r, terms: map[TerminationID]*relayTermination{}, mix: mixer.NewConference()}
	rc.mu.Lock()
	for _, req := range terminations {
		if _, err := rc.add(req); err != nil {
			rc.closed = true
			rc.mu.Unlock()
			rc.close()
			return Context{}, err
		}
	}
	saved, view := rc.saved(), rc.view()
	rc.mu.Unlock()
	// Nothing outside this call knows the context yet, so no change of
	// it can overtake this write.
	if err := r.store.PutContext(context.Background(), saved); err != nil {
		rc.mu.Lock()
		rc.closed = true
		rc.mu.Unlock()
		rc.close()
		// The write may have landed all the same, or left the entry
		// that the store writes before the context itself, so the
		// context the controller is told it never got is removed.
		r.forget(rc.id)
		r.log.Warn("the context was given up: it could not be stored", "context", rc.id, "error", err.Error())
		return Context{}, fmt.Errorf("agw: cannot store the context %s: %w", rc.id, err)
	}
	r.mu.Lock()
	r.contexts[rc.id] = rc
	r.adding.Add(-1)
	r.mu.Unlock()
	rc.tick()
	r.log.Info("context added", "context", rc.id, "terminations", view.summary())
	return view, nil
}

// keep writes a context that an operation has changed. The caller has
// let go of rc.mu, so that the packets of the context do not wait for
// the store, and holds rc.writes, so that two changes of one context
// cannot reach the store the other way round.
//
// Unlike Add, an operation on a context that exists goes on when the
// store fails, and answers its controller: the change has taken effect
// in the relay already, and the controller cannot take it back. One
// told that its Modify failed would end a call whose media flows. The
// stored context then lacks that one change until the next operation on
// it writes the context again. It is not written again here: a retry
// has let go of rc.writes and could land after the write of a later
// change, which would leave the store further behind than this does.
func (r *Relay) keep(c SavedContext) {
	if err := r.store.PutContext(context.Background(), c); err != nil {
		r.log.Warn("the stored context lacks a change: it could not be written", "context", c.ID, "error", err.Error())
	}
}

// forgetAttempts and forgetBackoff bound the attempts to remove a
// context that is gone from the store: the first, and four more a
// second further apart each, which covers a store that is restarting
// (the guides measure 0.2 to 0.3 s) without holding an ID for minutes.
const (
	forgetAttempts = 5
	forgetBackoff  = time.Second
)

// forget removes a context that is gone from the store. Nothing writes
// that ID again: the context is closed and out of the list of the
// relay before forget is called, and the IDs of new contexts skip it,
// so no attempt here can overtake another write.
//
// A failed removal is tried again, after the caller has been answered:
// a controller tearing a call down waits for no retry, and the ports
// are closed before the first attempt. What a removal that never
// succeeds leaves behind is in the comment of restore.
func (r *Relay) forget(id string) {
	err := r.store.DeleteContext(context.Background(), id)
	if err == nil {
		return
	}
	r.log.Warn("could not remove the context from the store, trying again", "context", id, "error", err.Error())
	go r.forgetAgain(id)
}

func (r *Relay) forgetAgain(id string) {
	for attempt := 2; ; attempt++ {
		time.Sleep(time.Duration(attempt-1) * forgetBackoff)
		err := r.store.DeleteContext(context.Background(), id)
		if err == nil {
			r.log.Info("removed the context from the store", "context", id, "attempts", attempt)
			return
		}
		if attempt == forgetAttempts {
			r.log.Warn("the context stays in the store: it could not be removed", "context", id, "attempts", attempt, "error", err.Error())
			return
		}
		r.log.Warn("could not remove the context from the store, trying again", "context", id, "error", err.Error())
	}
}

// AddTermination adds a termination to a context that exists, as when a
// participant joins a conference (TS 23.333 6.2.9.2.5). As with Add, a
// termination the relay could not store is given up again, and the
// context as it was goes back into the store, since the write that
// failed may have landed.
func (r *Relay) AddTermination(id string, req TerminationRequest) (Termination, error) {
	rc, ok := r.context(id)
	if !ok {
		return Termination{}, ErrNoContext
	}
	rc.writes.Lock()
	defer rc.writes.Unlock()
	rc.mu.Lock()
	if rc.closed {
		rc.mu.Unlock()
		return Termination{}, ErrNoContext
	}
	before := rc.saved()
	t, err := rc.add(req)
	if err != nil {
		rc.mu.Unlock()
		return Termination{}, err
	}
	saved, view := rc.saved(), t.view()
	rc.mu.Unlock()
	if err := r.store.PutContext(context.Background(), saved); err != nil {
		rc.mu.Lock()
		rc.remove(t)
		rc.mu.Unlock()
		if back := r.store.PutContext(context.Background(), before); back != nil {
			r.log.Warn("the stored context may hold a termination that was given up", "context", id,
				"termination", string(t.id), "error", back.Error())
		}
		r.log.Warn("the termination was given up: the context could not be stored", "context", id, "termination", string(t.id), "error", err.Error())
		return Termination{}, fmt.Errorf("agw: cannot store the context %s: %w", id, err)
	}
	rc.tick()
	r.log.Info("termination added", "context", id, "termination", string(view.ID), "local", view.Local.String(), "mode", string(view.Mode))
	return view, nil
}

// Modify changes what req sets on a termination. A remote address
// counts only while the termination has not latched, for RTP and RTCP
// each: the latched address wins until NAPTOff.
func (r *Relay) Modify(id string, tid TerminationID, req TerminationRequest) (Termination, error) {
	rc, ok := r.context(id)
	if !ok {
		return Termination{}, ErrNoContext
	}
	rc.writes.Lock()
	defer rc.writes.Unlock()
	rc.mu.Lock()
	t, ok := rc.terms[tid]
	if !ok || rc.closed {
		rc.mu.Unlock()
		return Termination{}, fmt.Errorf("%w: %q in context %s", ErrNoTermination, tid, id)
	}
	if req.ID != "" && req.ID != tid {
		rc.mu.Unlock()
		return Termination{}, fmt.Errorf("%w: Modify cannot rename termination %q", ErrBadRequest, tid)
	}
	if err := t.modify(req); err != nil {
		rc.mu.Unlock()
		return Termination{}, err
	}
	saved, view := rc.saved(), t.view()
	rc.mu.Unlock()
	r.keep(saved)
	rc.tick()
	r.log.Info("termination modified", "context", id, "termination", string(tid), "remote", view.Remote.String(), "mode", string(view.Mode),
		"signals", view.Signals.String())
	return view, nil
}

// SubtractTermination removes one termination and closes its ports,
// and returns it with the counts of what it carried. Subtracting the
// last termination deletes the context (H.248.1 (03/2013) 7), which
// deleted then tells.
func (r *Relay) SubtractTermination(id string, tid TerminationID) (_ Termination, deleted bool, _ error) {
	rc, ok := r.context(id)
	if !ok {
		return Termination{}, false, ErrNoContext
	}
	rc.writes.Lock()
	defer rc.writes.Unlock()
	rc.mu.Lock()
	t, ok := rc.terms[tid]
	if !ok || rc.closed {
		rc.mu.Unlock()
		return Termination{}, false, fmt.Errorf("%w: %q in context %s", ErrNoTermination, tid, id)
	}
	delete(rc.terms, tid)
	rc.mix.Leave(string(tid))
	deleted = len(rc.terms) == 0
	rc.closed = deleted
	view := t.view()
	var saved SavedContext
	if !deleted {
		saved = rc.saved()
	}
	rc.mu.Unlock()
	t.close()
	if deleted {
		r.mu.Lock()
		delete(r.contexts, id)
		r.mu.Unlock()
		r.forget(id)
		r.contextGone()
	} else {
		r.keep(saved)
	}
	r.log.Info("termination subtracted", "context", id, "termination", string(tid), "context_deleted", deleted,
		"packets", view.Packets, "packets_sent", view.PacketsSent, "rtcp_packets", view.RTCPPackets)
	return view, deleted, nil
}

// Subtract removes a context with all its terminations, as a Subtract
// with the termination ID wildcarded does, and returns the context as
// it was, with the counts of what each termination carried. As with
// SubtractTermination, the ports are closed before the context leaves
// the list, so that a context no longer listed sends nothing more.
func (r *Relay) Subtract(id string) (Context, error) {
	rc, ok := r.context(id)
	if !ok {
		return Context{}, ErrNoContext
	}
	rc.writes.Lock()
	defer rc.writes.Unlock()
	rc.mu.Lock()
	if rc.closed {
		rc.mu.Unlock()
		return Context{}, ErrNoContext
	}
	rc.closed = true
	view := rc.view()
	rc.mu.Unlock()
	rc.close()
	r.mu.Lock()
	delete(r.contexts, id)
	r.mu.Unlock()
	r.forget(id)
	r.contextGone()
	r.log.Info("context subtracted", "context", id, "terminations", view.counts())
	return view, nil
}

// Close closes the sockets of every context, as the process of a relay
// that stops does, and leaves the contexts in the store: a relay
// created again under the same name opens their ports again. It is not
// a Subtract, which is what gives a context up.
func (r *Relay) Close() {
	r.mu.Lock()
	all := make([]*relayContext, 0, len(r.contexts))
	for _, rc := range r.contexts {
		all = append(all, rc)
	}
	clear(r.contexts)
	r.mu.Unlock()
	for _, rc := range all {
		rc.mu.Lock()
		rc.closed = true
		rc.mu.Unlock()
		rc.close()
	}
}

// Contexts lists the contexts, for learners and the Lab.
func (r *Relay) Contexts() []Context {
	r.mu.Lock()
	all := make([]*relayContext, 0, len(r.contexts))
	for _, rc := range r.contexts {
		all = append(all, rc)
	}
	r.mu.Unlock()
	out := make([]Context, 0, len(all))
	for _, rc := range all {
		rc.mu.Lock()
		out = append(out, rc.view())
		rc.mu.Unlock()
	}
	return out
}

// newID is the ID of a new context: the next number that no context of
// the relay holds, so that a context opened again from the store keeps
// the ID its controller knows it by.
func (r *Relay) newID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		id := fmt.Sprintf("c%d", r.nextID.Add(1))
		if _, taken := r.contexts[id]; !taken {
			return id
		}
	}
}

func (r *Relay) context(id string) (*relayContext, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rc, ok := r.contexts[id]
	return rc, ok
}

// open binds an even port from the pool for RTP, and the odd one above
// it for RTCP (RFC 3550 11).
func (r *Relay) open() (rtp, rtcp *channel, err error) {
	for range r.ports.size() {
		port, ok := r.ports.take()
		if !ok {
			break
		}
		rtp, err := r.listen(port)
		if err != nil {
			// Something else holds the port; leave it out of the pool.
			continue
		}
		rtcp, err := r.listen(port + 1)
		if err != nil {
			rtp.conn.Close()
			continue
		}
		return rtp, rtcp, nil
	}
	return nil, nil, ErrNoPort
}

// reopenPorts binds the pair of ports that a termination had before a
// restart, and takes them out of the pool. A port the pool no longer
// has is one that another context of this relay has taken, which a
// stored context whose ports overlap another's would ask for, or one
// outside the range of the relay.
func (r *Relay) reopenPorts(port uint16) (rtp, rtcp *channel, err error) {
	if !r.ports.takePort(port) {
		return nil, nil, fmt.Errorf("%w: port %d is not free in the range of the relay", ErrNoPort, port)
	}
	rtp, err = r.listen(port)
	if err != nil {
		return nil, nil, err
	}
	rtcp, err = r.listen(port + 1)
	if err != nil {
		rtp.conn.Close()
		return nil, nil, err
	}
	return rtp, rtcp, nil
}

func (r *Relay) listen(port uint16) (*channel, error) {
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), port)))
	if err != nil {
		return nil, err
	}
	return &channel{conn: conn, local: netip.AddrPortFrom(r.address, port)}, nil
}

// summary is the local RTP address of each termination, and counts
// what each one carried, for the log.
func (c Context) summary() map[TerminationID]string {
	out := make(map[TerminationID]string, len(c.Terminations))
	for id, t := range c.Terminations {
		out[id] = t.Local.String()
	}
	return out
}

func (c Context) counts() map[TerminationID]string {
	out := make(map[TerminationID]string, len(c.Terminations))
	for id, t := range c.Terminations {
		out[id] = fmt.Sprintf("rtp in %d out %d, rtcp in %d", t.Packets, t.PacketsSent, t.RTCPPackets)
	}
	return out
}

// portPool hands out the even ports of a range whose odd neighbour, for
// RTCP, is in the range too.
type portPool struct {
	mu   sync.Mutex
	free []uint16
}

func newPortPool(minPort, maxPort uint16) *portPool {
	p := &portPool{}
	for port := int(minPort + minPort%2); port+1 <= int(maxPort); port += 2 {
		p.free = append(p.free, uint16(port))
	}
	return p
}

func (p *portPool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.free)
}

func (p *portPool) take() (uint16, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.free) == 0 {
		return 0, false
	}
	port := p.free[0]
	p.free = p.free[1:]
	return port, true
}

// takePort takes one port of the range, for a termination that had it
// before a restart, and tells whether the pool had it free.
func (p *portPool) takePort(port uint16) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	at := slices.Index(p.free, port)
	if at < 0 {
		return false
	}
	p.free = slices.Delete(p.free, at, at+1)
	return true
}

func (p *portPool) give(port uint16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.free = append(p.free, port)
}
