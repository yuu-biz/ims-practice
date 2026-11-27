package ibcf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Config is what the IBCF needs besides its peers, TrGW, Store and
// locator.
type Config struct {
	// Internal is the URI of this IBCF instance in the home network, on
	// the Mx reference point, with the host and port of its internal
	// transport. It is the Contact of every dialog that the instance has
	// in the home network, so that the requests of those dialogs come
	// back to the instance that holds them, and its host names the
	// instance in the Store.
	Internal sip.URI
	// External is the URI of this instance towards the peers, on the Ici
	// reference point, with the host and port of its external transport.
	// It is the Contact of the dialogs with the peers.
	External sip.URI
	// InternalPool and ExternalPool are the host names that the
	// instances of the network share on each side, when there are
	// several: what the S-CSCF puts in Route to send a request out (TS
	// 24.229 5.4.3.2 step 12), and the peers' provisioned entry point
	// (5.10.2.2 NOTE 3). Empty when the IBCF runs as one instance and
	// Internal and External are those names.
	InternalPool, ExternalPool string
	// HomeDomain and HomeNumberFormat are those of
	// interconnect.Border.
	HomeDomain       string
	HomeNumberFormat interconnect.NumberFormat
	// ICSCF is the I-CSCF that an initial request from a peer goes to
	// (TS 24.229 5.10.3.2 step 4: a preconfigured I-CSCF).
	ICSCF sip.URI
	// TrGWCoreAddress and TrGWPeerAddress are the TrGW's addresses in
	// the home network and towards the peers' TrGWs (Izi, TS 29.165
	// 3.2). The SDP of each side shows the termination that faces it at
	// that address, with the port that the TrGW chose. A TrGW has an
	// address in each network; the media relay that serves as the TrGW
	// writes one address for both terminations, and takes the media on
	// every interface. The zero address keeps the one that the TrGW
	// wrote.
	TrGWCoreAddress, TrGWPeerAddress netip.Addr
	// InternalTimers and ExternalTimers are those of the UA on each
	// side; the zero value means sip.DefaultTimers. The operator may
	// give each side a transaction timeout of its own.
	InternalTimers, ExternalTimers sip.Timers
	// Metrics is where the IBCF registers its counters (metrics.go).
	// Nil gives it a registry of its own, which nothing serves.
	Metrics *metrics.Registry
}

// IBCF is the role at the border of the home network (TS 24.229 5.10).
// It is a B2BUA (5.10.5, TS 29.165 5.2.1) with a UA on each side: the
// internal one faces the home network, and a request that arrives there
// leaves for a peer, which makes the IBCF its exit point (5.10.2); the
// external one faces the peers, and a request that arrives there
// enters the home network, which makes the IBCF its entry point
// (5.10.3). Each call has a dialog on each side, with its own Call-ID,
// tags, CSeq, Via and Contact; the IBCF maps between them what
// interconnect.Border lets cross, and puts the media through the TrGW.
type IBCF struct {
	cfg     Config
	peers   Peers
	trgw    TrGW
	store   Store
	locator ims.Locator
	log     *slog.Logger
	metrics *borderMetrics

	internal atomic.Pointer[sip.UA]
	external atomic.Pointer[sip.UA]
	// closed is set by Close: the calls that end with it leave their
	// records and TrGW contexts to the next process of the instance.
	closed atomic.Bool
}

// New makes the IBCF. Listen then starts its two UAs.
func New(cfg Config, peers Peers, trgw TrGW, store Store, locator ims.Locator, log *slog.Logger) (*IBCF, error) {
	switch {
	case cfg.Internal.Host == "" || cfg.External.Host == "":
		return nil, errors.New("ibcf: the internal and the external URI need a host")
	case cfg.ICSCF.Host == "":
		return nil, errors.New("ibcf: no I-CSCF for the requests that enter")
	case peers == nil || trgw == nil || store == nil || locator == nil:
		return nil, errors.New("ibcf: peers, TrGW, store and locator are all needed")
	case len(peers.Names()) == 0:
		return nil, errors.New("ibcf: no peer network")
	}
	switch cfg.HomeNumberFormat {
	case "", interconnect.NumberFormatTel, interconnect.NumberFormatSIP:
	default:
		return nil, fmt.Errorf("ibcf: unknown home number format %q", cfg.HomeNumberFormat)
	}
	reg := cfg.Metrics
	if reg == nil {
		reg = metrics.NewRegistry()
	}
	m, err := newBorderMetrics(reg, peers.Names())
	if err != nil {
		return nil, err
	}
	return &IBCF{cfg: cfg, peers: peers, trgw: trgw, store: store, locator: locator, log: log, metrics: m}, nil
}

// side is one of the two UAs of the IBCF.
type side struct {
	// direction is the way a request that arrives on this side crosses
	// the border.
	direction interconnect.Direction
	own       sip.URI
	pool      string
	timers    sip.Timers
	ua        *atomic.Pointer[sip.UA]
}

func (b *IBCF) inside() side {
	return side{direction: interconnect.Exit, own: b.cfg.Internal, pool: b.cfg.InternalPool, timers: b.cfg.InternalTimers, ua: &b.internal}
}

func (b *IBCF) outside() side {
	return side{direction: interconnect.Entry, own: b.cfg.External, pool: b.cfg.ExternalPool, timers: b.cfg.ExternalTimers, ua: &b.external}
}

// other is the side that a request arriving on s leaves on.
func (b *IBCF) other(s side) side {
	if s.direction == interconnect.Exit {
		return b.outside()
	}
	return b.inside()
}

// ErrNotListening is what the IBCF reports for a request it cannot send
// on because its UAs have not started or have closed.
var ErrNotListening = errors.New("ibcf: not listening")

func (s side) agent() (*sip.UA, error) {
	if ua := s.ua.Load(); ua != nil {
		return ua, nil
	}
	return nil, ErrNotListening
}

// Listen starts the two UAs, internal on the transport of the home
// network and external on the one towards the peers. It first takes
// the calls that an earlier process of this instance left in the
// Store, and ends them once the UAs run (ReleaseLeftovers).
//
// When the Store cannot be read, Listen fails and starts neither UA.
// An instance that took calls without its records would never end the
// calls of its earlier process: it reads them only here, and their
// parties would keep their TrGW contexts until they hang up. It could
// not keep the records of its own calls either, and would answer most
// of them with 500. A node that stops on this error can start again
// once the Store is back; the other instances of the pool take the new
// calls meanwhile.
func (b *IBCF) Listen(internal, external *sip.TransportLayer) error {
	leftovers, err := b.store.CallsOf(context.Background(), b.instance())
	if err != nil {
		return fmt.Errorf("ibcf: cannot read the calls that an earlier process of %s left: %w", b.instance(), err)
	}
	in, err := b.newUA(internal, b.inside(), b.locateInside)
	if err != nil {
		return err
	}
	out, err := b.newUA(external, b.outside(), b.locateOutside)
	if err != nil {
		in.Close()
		return err
	}
	b.internal.Store(in)
	b.external.Store(out)
	if len(leftovers) > 0 {
		go b.ReleaseLeftovers(leftovers)
	}
	return nil
}

func (b *IBCF) newUA(transport *sip.TransportLayer, s side, locate func(sip.URI) ([]sip.Destination, error)) (*sip.UA, error) {
	var screen func(*sip.Message, sip.Destination) bool
	var leftOut func(*sip.Message, sip.Destination, bool) []string
	if s.direction == interconnect.Entry {
		screen, leftOut = b.Screen, b.optionsLeftOut
	}
	return sip.NewUA(transport, sip.UAConfig{
		SentBy:  s.own.Host + ":" + strconv.Itoa(portOf(s.own)),
		Timers:  s.timers,
		Contact: sip.URI{Scheme: "sip", Host: s.own.Host, Port: s.own.Port}.String(),
		// The IBCF carries the SDP of preconditions from leg to leg
		// without reading it, so an INVITE that requires them is taken
		// and the requirement goes on to the far end. It names the other
		// side's dialog in a Replaces that it passes on
		// (replacesOnTheOtherSide), so it supports Replaces too (RFC
		// 3891 6.2).
		Supported:      []string{sip.OptionPrecondition, sip.OptionReplaces},
		OnCall:         func(in *sip.IncomingCall) { b.onCall(s, in) },
		OnMessage:      func(st *sip.ServerTransaction) { b.onMessage(s, st) },
		OnSubscribe:    func(st *sip.ServerTransaction) { b.onSubscribe(s, st) },
		Locate:         locate,
		Screen:         screen,
		OptionsLeftOut: leftOut,
	}, b.log.With("side", string(s.direction)))
}

// Close stops both UAs. Calls in progress end without a BYE; their
// records stay in the Store for the next process of the instance.
func (b *IBCF) Close() {
	b.closed.Store(true)
	for _, p := range []*atomic.Pointer[sip.UA]{&b.internal, &b.external} {
		if ua := p.Swap(nil); ua != nil {
			ua.Close()
		}
	}
}

// instance is the name of this instance in the Store.
func (b *IBCF) instance() string {
	return strings.ToLower(b.cfg.Internal.Host)
}

func portOf(u sip.URI) int {
	if u.Port == 0 {
		return 5060
	}
	return u.Port
}

// crossing is an initial request that may cross the border: the copy
// that the border prepared, and what the request on the other side
// needs.
type crossing struct {
	direction interconnect.Direction
	peer      Peer
	border    interconnect.Border
	// req is the copy of the request, as the border left it, without
	// the IBCF's own Route.
	req *sip.Message
	// route is the Route of the request on the other side.
	route []string
	log   *slog.Logger
}

// refusal is the response the IBCF sends itself instead of passing a
// request on.
type refusal struct {
	status  int
	headers []sip.HeaderField
}

// prepare runs the procedures of the exit point (TS 24.229 5.10.2.2) or
// the entry point (5.10.3.2) on a copy of req, an initial request or a
// standalone one that arrived on side s from source. The stack has
// answered an INVITE with 100 (Trying) already (step 1).
func (b *IBCF) prepare(s side, req *sip.Message, source netip.Addr) (*crossing, *refusal) {
	log := b.log.With("direction", string(s.direction), "method", string(req.Method), "call_id", req.Get("Call-ID"))
	c := &crossing{direction: s.direction, req: req.Clone()}
	var peer Peer
	var ok bool
	if s.direction == interconnect.Exit {
		// Step 1A, then the peer that the next hop belongs to.
		popOwnRoutes(c.req, s)
		if peer, ok = b.peerOfNextHop(c.req); !ok {
			log.Info("no peer network for the destination", "request_uri", c.req.RequestURI, "route", c.req.Get("Route"))
			return nil, b.refuse(req, Peer{}, s.direction, 404)
		}
	} else if peer, ok = b.peers.BySource(source); !ok {
		// Screen has let in only the requests of a peer, so only a Peers
		// that changes its answer for the same address gets here.
		log.Error("a request of no peer network passed the screen", "source", source.String())
		return nil, b.refuse(req, Peer{}, s.direction, 500)
	}
	c.peer, c.log = peer, log.With("peer", peer.Profile.Name)
	border, ok := b.border(peer, c.log)
	if !ok {
		return nil, b.refuse(req, peer, s.direction, 500)
	}
	c.border = border
	if err := sip.DecrementMaxForwards(c.req); err != nil {
		return nil, b.refuse(req, peer, s.direction, 483)
	}
	// At the entry point the check for orig looks at the topmost Route
	// as it arrived, so the border goes before step 1C.
	if err := border.Request(c.req, s.direction); err != nil {
		return nil, b.rejected(req, peer, s.direction, err, c.log)
	}
	if s.direction == interconnect.Exit {
		c.route = c.req.GetAll("Route")
		if len(c.route) == 0 && peer.EntryPoint != "" {
			// 5.10.2.2 NOTE 3: the provisioned next hop. As a Route, the
			// peer's IBCF sees a single Route that names it, which is
			// what 5.10.3.2 step 4 looks for.
			if entry, err := ims.LooseRoute(peer.EntryPoint); err == nil {
				c.route = []string{sip.NameAddr{URI: entry.String()}.String()}
			}
		}
		return c, nil
	}
	own, popped := popOwnRoutes(c.req, s)
	c.route = c.req.GetAll("Route")
	if len(c.route) == 0 {
		// Step 4. A request that came without a Route goes to the
		// I-CSCF too: the peer found the IBCF by RFC 3263.
		icscf := b.cfg.ICSCF
		icscf.Params = slices.Clone(icscf.Params)
		icscf.Params.Set("lr", "")
		if _, orig := own.Params.Get("orig"); popped && orig {
			icscf.Params.Set("orig", "")
		}
		c.route = []string{sip.NameAddr{URI: icscf.String()}.String()}
	}
	return c, nil
}

// Screen is the sip.UAConfig.Screen of the external UA: it tells
// whether a request that arrived from source may reach the IBCF, and
// only a peer's may. The stack drops each one it turns away without any
// response, not even the 100 (Trying) of an INVITE, so that a sender
// outside every agreement learns nothing about the network. Screen
// counts and logs it. It runs in the goroutine of the stack, so it
// only reads the Peers, which take no lock that a request holds.
func (b *IBCF) Screen(req *sip.Message, source sip.Destination) bool {
	if _, ok := b.peers.BySource(source.Addr.Addr()); ok {
		return true
	}
	b.log.Info("discarded a request from an address of no peer network", "method", string(req.Method),
		"source", source.String(), "call_id", req.Get("Call-ID"))
	b.metrics.discard(DiscardUnknownSource, req.Method)
	return false
}

// optionsLeftOut is the sip.UAConfig.OptionsLeftOut of the external
// UA: the option tags that the agreement with the peer at the far end
// of m removes (interconnect.Profile.RemovedOptionTags). The border
// takes them out of each request that crosses, but on its leg of a call
// the UA also writes messages of its own for the peer, with its own
// Supported, and reads the peer's requests to choose what it uses, such
// as reliable provisional responses. With the tags left out, the leg
// uses no extension that the agreement removes. The IBCF finds the peer
// as it does for an initial request: by the next hop of a request that
// leaves, and by the address of the far end for a request that comes in
// and for a response, which goes back to where its request came from
// (RFC 3261 18.2.2). A request within a dialog goes to the Contact or
// the Record-Route of the peer, whose host may be an address or a name
// that the agreement does not list; the address it goes to is then one
// of the peer's IBCFs, which BySource knows.
func (b *IBCF) optionsLeftOut(m *sip.Message, remote sip.Destination, sent bool) []string {
	var peer Peer
	ok := false
	if sent && m.IsRequest() {
		peer, ok = b.peerOfNextHop(m)
	}
	if !ok {
		peer, ok = b.peers.BySource(remote.Addr.Addr())
	}
	if !ok {
		return nil
	}
	return peer.Profile.RemovedOptionTags()
}

// refuse is a refusal with status, counted for peer.
func (b *IBCF) refuse(req *sip.Message, peer Peer, d interconnect.Direction, status int, headers ...sip.HeaderField) *refusal {
	b.metrics.reject(peer.Profile.Name, d, req.Method, status)
	return &refusal{status: status, headers: headers}
}

// rejected is the refusal of a request that the border turned away. A
// Rejection carries the response that TS 24.229 5.10.1 lets the IBCF
// send by local policy; any other error is the IBCF's own.
func (b *IBCF) rejected(req *sip.Message, peer Peer, d interconnect.Direction, err error, log *slog.Logger) *refusal {
	r := rejection(err, log)
	return b.refuse(req, peer, d, r.status, r.headers...)
}

// rejection is the refusal of a request that the border turned away for
// err, as rejected has it, not yet counted.
func rejection(err error, log *slog.Logger) refusal {
	var r *interconnect.Rejection
	if errors.As(err, &r) {
		log.Info("rejected at the border", "status", r.StatusCode, "reason", r.Err.Error())
		return refusal{status: r.StatusCode, headers: r.Headers}
	}
	log.Warn("cannot prepare the request for the border", "error", err.Error())
	return refusal{status: 500}
}

// popOwnRoutes removes the URIs of side s from the top of Route
// (5.10.2.2 step 1A, 5.10.3.2 step 1C): the instance's own and the
// pool's. It returns the last URI it removed.
func popOwnRoutes(m *sip.Message, s side) (sip.URI, bool) {
	own := []sip.URI{s.own}
	if s.pool != "" {
		own = append(own, sip.URI{Host: s.pool, Port: s.own.Port}, sip.URI{Host: s.pool})
	}
	var last sip.URI
	popped := false
	for {
		u, ok := sip.PopOwnRoute(m, own...)
		if !ok {
			return last, popped
		}
		last, popped = u, true
	}
}

// peerOfNextHop finds the peer that a request leaves for, from the host
// of its next hop: the top Route, or else the Request-URI.
//
// SIMPLIFIED: TS 24.229 Annex I and RFC 4694. A Request-URI without a
// host, such as a tel URI that ENUM did not translate, or one with an
// rn parameter, finds no peer: the IBCF has no table of number ranges
// per peer. The S-CSCF of this project sends a SIP URI (5.4.3.2 step
// 10).
func (b *IBCF) peerOfNextHop(m *sip.Message) (Peer, bool) {
	u, err := sip.NextHop(m)
	if err != nil {
		return Peer{}, false
	}
	return b.peers.ByHost(u.Host)
}

// locateOutside is where a request of the external UA goes: the
// address of its next hop, over the transport that the agreement with
// the peer prefers, or that the URI names when the agreement lists it
// (TS 29.165 6.2.1). A URI of no peer, which only a dialog with a peer
// that changed its Contact could have, goes over its own transport.
//
// TS 29.165 6.2.1 asks for TS 24.229 4.2A, which has every message
// longer than 1300 bytes go as RFC 3261 18.1.1 says. The sip stack does
// that by itself: a request for UDP that is longer goes over TCP, and
// over UDP after all when the peer refuses the connection.
func (b *IBCF) locateOutside(u sip.URI) ([]sip.Destination, error) {
	peer, ok := b.peers.ByHost(u.Host)
	if !ok {
		return ims.Targets(context.Background(), b.locator, u)
	}
	transport := peer.Profile.PreferredTransport()
	if t, ok := u.Params.Get("transport"); ok && peer.Profile.AcceptsTransport(sip.Transport(strings.ToUpper(t))) {
		transport = sip.Transport(strings.ToUpper(t))
	}
	targets, err := b.locator.Locate(context.Background(), u.Host, u.Port, transport)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("ibcf: no address for %s", u)
	}
	over := make([]sip.Destination, len(targets))
	for i, target := range targets {
		target.Transport = transport
		over[i] = target
	}
	return over, nil
}

// locateInside is where a request of the internal UA goes in the home
// network.
func (b *IBCF) locateInside(u sip.URI) ([]sip.Destination, error) {
	return ims.Targets(context.Background(), b.locator, u)
}

// border is the border with peer. A profile that does not fit the
// IBCF's configuration fails the request.
func (b *IBCF) border(peer Peer, log *slog.Logger) (interconnect.Border, bool) {
	border := interconnect.Border{Peer: peer.Profile, HomeDomain: b.cfg.HomeDomain, HomeNumberFormat: b.cfg.HomeNumberFormat}
	if err := border.Validate(); err != nil {
		log.Error("the border with the peer is misconfigured", "error", err.Error())
		return interconnect.Border{}, false
	}
	return border, true
}

// sourceOf is where a request came from, as the transport wrote it in
// the top Via: in received, or in the sent-by when that is the address
// it came from (RFC 3261 18.2.1). An IncomingCall hands the INVITE over
// without its source.
func sourceOf(req *sip.Message) netip.Addr {
	via, err := sip.ParseVia(req.Get("Via"))
	if err != nil {
		return netip.Addr{}
	}
	host := via.Host
	if received, ok := via.Params.Get("received"); ok {
		host = received
	}
	addr, _ := netip.ParseAddr(host)
	return addr
}

// reverse is the direction in which the responses to a request that
// crosses in direction d go, and the requests of the other party.
func reverse(d interconnect.Direction) interconnect.Direction {
	if d == interconnect.Exit {
		return interconnect.Entry
	}
	return interconnect.Exit
}
