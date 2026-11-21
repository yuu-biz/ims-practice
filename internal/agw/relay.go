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
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/mixer"
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
	// Remote is where the termination sends RTP, as the SDP of the party
	// it faces gives it: the Remote descriptor. RTCP goes to the port
	// above.
	Remote netip.AddrPort `json:"remote,omitzero"`
	// Signals, when not nil, replaces what the termination plays.
	Signals *Signals `json:"signals,omitempty"`
	// Events, when not nil, replaces the events that the termination
	// reports.
	Events *Events `json:"events,omitempty"`
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
	// Latched tells that Remote was learned from the first packet that
	// arrived, rather than from the SDP.
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
	log           *slog.Logger

	mu       sync.Mutex
	contexts map[string]*relayContext
	nextID   atomic.Uint64
}

// NewRelay relays on address, with RTP ports taken from [minPort,
// maxPort]. announcements are what it can play; the IMS-AGW has none.
func NewRelay(address netip.Addr, minPort, maxPort uint16, announcements Announcements, log *slog.Logger) (*Relay, error) {
	if minPort == 0 || maxPort < minPort {
		return nil, fmt.Errorf("agw: port range %d-%d", minPort, maxPort)
	}
	r := &Relay{
		address: address, ports: newPortPool(minPort, maxPort), announcements: announcements,
		notifier: &http.Client{Timeout: RequestTimeout}, log: log, contexts: map[string]*relayContext{},
	}
	// A context ID from before a restart must not name a new context,
	// or a controller that still holds the old ID would change a
	// context it does not own. The IDs start at a random number, as the
	// context IDs of an H.248 media gateway do.
	r.nextID.Store(rand.Uint64N(1 << 32))
	return r, nil
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
func (r *Relay) Add(terminations ...TerminationRequest) (Context, error) {
	if len(terminations) == 0 {
		return Context{}, fmt.Errorf("%w: a context needs at least one termination", ErrBadRequest)
	}
	rc := &relayContext{id: fmt.Sprintf("c%d", r.nextID.Add(1)), relay: r, terms: map[TerminationID]*relayTermination{}, mix: mixer.NewConference()}
	rc.mu.Lock()
	for _, req := range terminations {
		if _, err := rc.add(req); err != nil {
			rc.closed = true
			rc.mu.Unlock()
			rc.close()
			return Context{}, err
		}
	}
	rc.startClock()
	view := rc.view()
	rc.mu.Unlock()
	r.mu.Lock()
	r.contexts[rc.id] = rc
	r.mu.Unlock()
	r.log.Info("context added", "context", rc.id, "terminations", view.summary())
	return view, nil
}

// AddTermination adds a termination to a context that exists, as when a
// participant joins a conference (TS 23.333 6.2.9.2.5).
func (r *Relay) AddTermination(id string, req TerminationRequest) (Termination, error) {
	rc, ok := r.context(id)
	if !ok {
		return Termination{}, ErrNoContext
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.closed {
		return Termination{}, ErrNoContext
	}
	t, err := rc.add(req)
	if err != nil {
		return Termination{}, err
	}
	rc.startClock()
	view := t.view()
	r.log.Info("termination added", "context", id, "termination", string(view.ID), "local", view.Local.String(), "mode", string(view.Mode))
	return view, nil
}

// Modify changes what req sets on a termination. A remote address
// counts only until a packet has arrived: latching wins, for RTP and
// RTCP each.
func (r *Relay) Modify(id string, tid TerminationID, req TerminationRequest) (Termination, error) {
	rc, ok := r.context(id)
	if !ok {
		return Termination{}, ErrNoContext
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	t, ok := rc.terms[tid]
	if !ok || rc.closed {
		return Termination{}, fmt.Errorf("%w: %q in context %s", ErrNoTermination, tid, id)
	}
	if req.ID != "" && req.ID != tid {
		return Termination{}, fmt.Errorf("%w: Modify cannot rename termination %q", ErrBadRequest, tid)
	}
	if err := t.modify(req); err != nil {
		return Termination{}, err
	}
	rc.startClock()
	view := t.view()
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
	rc.mu.Unlock()
	t.close()
	if deleted {
		r.mu.Lock()
		delete(r.contexts, id)
		r.mu.Unlock()
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
	r.log.Info("context subtracted", "context", id, "terminations", view.counts())
	return view, nil
}

// Contexts lists the contexts, for learners and the dashboard.
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

func (p *portPool) give(port uint16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.free = append(p.free, port)
}
