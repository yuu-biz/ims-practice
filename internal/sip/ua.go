package sip

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// This file holds the user agent core: the UA itself, which routes the
// requests it receives to its calls. What both ends of a call share is
// in call.go, the UAC side of an INVITE (RFC 3261 13.2) in uac.go and
// the UAS side (13.3) in uas.go, both with reliable provisional
// responses (RFC 3262). A UE uses the UA to make and answer calls, and
// to subscribe to events (RFC 6665) through the Subscriber in
// subscription.go; the CSCFs are proxies and do not.
//
// The UA carries session descriptions without reading them. The offer
// may come in the INVITE, with the answer in a reliable provisional
// response or the 2xx; or in the 2xx, with the answer in the ACK; or in
// a reliable provisional response, with the answer in the PRACK. A
// PRACK or an UPDATE may carry a later offer, which its 2xx answers
// (RFC 3261 13.2.1, RFC 3262 5, RFC 3311 5.1). The table that follows
// these exchanges is in offeranswer.go.

// UAConfig is what a UA needs besides its transport.
type UAConfig struct {
	// SentBy, Timers and Screen are those of the Stack (Config). A
	// request that Screen refuses never reaches the UA.
	SentBy string
	Timers Timers
	Screen func(req *Message, source Destination) bool
	// Contact is the SIP URI where this UA takes the later requests of
	// its dialogs, such as sip:001010000000001@192.0.2.10:5060 (RFC 3261
	// 12.1.1, 12.1.2).
	Contact string
	// ContactParams are the parameters that the UA writes after that URI
	// in the Contact of its INVITEs and of its responses, such as the
	// feature tags that tell what the UA can do (RFC 3840).
	ContactParams Params
	// Supported are the option tags of the extensions that the user of
	// the UA handles on top of it, such as precondition, whose SDP the
	// UA only carries. The UA lists them in Supported after its own and
	// takes an INVITE that requires them.
	Supported []string
	// SessionTimer is what the UA wants of the session timer (RFC 4028).
	// The zero value asks for none, and takes part when the peer asks.
	SessionTimer SessionTimerPolicy
	// AfterFunc starts the timers of the session timer: it calls f in a
	// goroutine of its own after d, unless stop is called first. Nil
	// means time.AfterFunc. A test passes its own, to fire the timers
	// by hand instead of waiting for half an hour.
	AfterFunc func(d time.Duration, f func()) (stop func() bool)
	// Allow are the methods that the users of the calls take within a
	// dialog (Invitation.OnRequest, IncomingCall.Handle), such as REFER.
	// The UA lists them in Allow after its own.
	Allow []Method
	// OnCall is called, in a goroutine of its own, for each INVITE
	// outside a dialog. It answers the call with Accept or Reject.
	OnCall func(*IncomingCall)
	// OnMessage, when set, is called in a goroutine of its own for each
	// MESSAGE outside a dialog, a standalone request that is answered at
	// once with st.Respond (RFC 3428 7). Without it the UA answers a
	// MESSAGE with 405 and leaves MESSAGE out of Allow.
	OnMessage func(st *ServerTransaction)
	// OnSubscribe, when set, makes the UA a notifier (RFC 6665 4.2): it
	// is called in a goroutine of its own for each SUBSCRIBE that is not
	// within the dialog of a call, both an initial one and one within a
	// subscription's dialog, which the user keeps (NotifierSubscription)
	// and answers with st.Respond. Without it the UA answers a SUBSCRIBE
	// outside a call with 405, or 481 within a dialog it does not know,
	// and leaves SUBSCRIBE out of Allow.
	OnSubscribe func(st *ServerTransaction)
	// OnOptions, when set, tells how the UA answers an OPTIONS: with the
	// status code that an INVITE would get now, such as 200, or 486
	// (Busy Here) while the user is in a call, and with a body that
	// describes what media the UA can take, normally an SDP (RFC 3261
	// 11.2). Nil means 200 without a body.
	OnOptions func(req *Message) (statusCode int, body Body)
	// OwnHeaders, when set, gives the header fields that the UA adds to
	// the messages it makes on its own, which no call of its user takes
	// header fields for: the ACK of a 2xx, a session refresh (RFC 4028),
	// a PRACK without a body, a BYE that ends the call by itself, and its
	// responses to a BYE, an OPTIONS and the requests it answers without
	// the user. A UE gives User-Agent or Server here (GSMA IR.92 2.6), and
	// P-Access-Network-Info (TS 24.229 5.1.2A.1.1, 5.1.2A.2), as in the
	// messages of its calls.
	//
	// method is that of the request, or of the request that a response
	// answers, and response tells which of the two it is. For a BYE,
	// reason is why the UA ends the call (ErrSessionExpired,
	// ErrDialogLost, ErrNoACK), for a Reason (TS 24.229 5.1.5); nil for
	// anything else, and for the BYE to a 2xx the UA does not want.
	OwnHeaders func(method Method, response bool, reason error) []HeaderField
	// Locate gives the destination of a request, for the URI that
	// NextHop picks from it: the top Route, or else the Request-URI (RFC
	// 3261 8.1.2). Nil means that the host of that URI is an IP address,
	// with port 5060 unless the URI names one, and the transport its
	// transport parameter names, UDP by default (RFC 3263 4.1).
	Locate func(URI) (Destination, error)
}

// UA is a user agent on top of a Stack of its own. It keeps the dialogs
// of its calls, so that the requests and ACKs within a dialog reach the
// call they belong to.
type UA struct {
	stack      *Stack
	cfg        UAConfig
	log        *slog.Logger
	subscriber *Subscriber

	mu    sync.Mutex
	calls map[DialogID]*CallDialog
	// open is every call that has not ended, with a dialog or not yet,
	// so that Close can end them. closed is set by Close.
	open   map[*call]bool
	closed bool
	// running counts what the UA runs by itself in goroutines of its
	// own (spawn, afterFunc), so that Close can wait for it.
	running sync.WaitGroup
}

// NewUA starts a Stack on transport for the UA. The UA closes it in
// Close.
func NewUA(transport *TransportLayer, cfg UAConfig, log *slog.Logger) (*UA, error) {
	if cfg.OnCall == nil {
		return nil, errors.New("sip: UAConfig.OnCall is required")
	}
	if _, err := ParseURI(cfg.Contact); err != nil {
		return nil, fmt.Errorf("sip: UAConfig.Contact: %w", err)
	}
	if cfg.Locate == nil {
		cfg.Locate = locateIPAddress
	}
	ua := &UA{cfg: cfg, log: log, subscriber: NewSubscriber(cfg.Contact, cfg.Locate, log),
		calls: map[DialogID]*CallDialog{}, open: map[*call]bool{}}
	stack, err := NewStack(transport, Config{
		SentBy:    cfg.SentBy,
		Timers:    cfg.Timers,
		Screen:    cfg.Screen,
		Ordered:   true,
		OnRequest: ua.receiveRequest,
		OnACK:     ua.receiveACK,
	}, log)
	if err != nil {
		return nil, err
	}
	ua.stack = stack
	return ua, nil
}

func locateIPAddress(u URI) (Destination, error) {
	ip, err := netip.ParseAddr(u.Host)
	if err != nil {
		return Destination{}, fmt.Errorf("sip: %s names no IP address, and UAConfig.Locate is not set", u)
	}
	port := u.Port
	if port == 0 {
		port = defaultPort
	}
	transport := UDP
	if t, ok := u.Params.Get("transport"); ok {
		transport = Transport(strings.ToUpper(t))
	}
	return Destination{Transport: transport, Addr: netip.AddrPortFrom(ip, uint16(port))}, nil
}

// Stack is the stack the UA runs on, for requests outside a call such
// as REGISTER.
func (ua *UA) Stack() *Stack { return ua.stack }

// Close stops the UA and its stack. Every call that has not ended ends
// with ErrStackClosed, and its dialog with it; no BYE or final response
// goes out, since the stack is gone. Close returns once what the UA
// runs by itself, such as the PRACK or the BYE that it sends without
// its user, has stopped: after that the UA no longer writes to its
// logger. The goroutines in which the UA calls its user, such as
// OnCall, are the user's to end.
func (ua *UA) Close() {
	ua.stack.Close()
	ua.mu.Lock()
	ua.closed = true
	open := make([]*call, 0, len(ua.open))
	for c := range ua.open {
		open = append(open, c)
	}
	ua.mu.Unlock()
	// Each call is ended under its own lock, which comes before ua.mu.
	for _, c := range open {
		c.mu.Lock()
		c.end("UA closed", ErrStackClosed)
		c.mu.Unlock()
	}
	// The stack has ended every transaction and the calls have ended,
	// so nothing that runs waits any longer.
	ua.running.Wait()
}

// enter counts in something that the UA runs by itself, which then
// calls ua.running.Done when it is over. It reports false once the UA
// has closed: the work is not to start then. Taking ua.mu orders every
// enter before the Wait of Close or after its closed.
func (ua *UA) enter() bool {
	ua.mu.Lock()
	defer ua.mu.Unlock()
	if ua.closed {
		return false
	}
	ua.running.Add(1)
	return true
}

// spawn runs f in a goroutine of its own that Close waits for. It
// reports false, and f does not run, once the UA has closed.
func (ua *UA) spawn(f func()) bool {
	if !ua.enter() {
		return false
	}
	go func() {
		defer ua.running.Done()
		f()
	}()
	return true
}

// Subscribe subscribes to an event (RFC 6665 4.1.2.1). The UA routes the
// NOTIFYs of the subscription to it.
func (ua *UA) Subscribe(r SubscribeRequest) (*Subscription, error) {
	return ua.subscriber.Subscribe(ua.stack, r)
}

// SendRequest sends req, a request outside any call of the UA such as
// the NOTIFY of a subscription that OnSubscribe took, to the next hop
// that its Route or Request-URI names (RFC 3261 8.1.2), found with
// UAConfig.Locate.
func (ua *UA) SendRequest(req *Message) (*ClientTransaction, error) {
	hop, err := NextHop(req)
	if err != nil {
		return nil, err
	}
	dst, err := ua.cfg.Locate(hop)
	if err != nil {
		return nil, err
	}
	return ua.stack.SendRequest(req, dst)
}

// allowed is what the Allow header field lists: the methods this UA
// takes within a dialog (RFC 3261 13.2.1, 13.3.1.4) and the methods it
// supports at all (8.2.1). It takes NOTIFY as a subscriber; SUBSCRIBE
// follows only when its user is a notifier (UAConfig.OnSubscribe, RFC
// 6665 4.1.1), and MESSAGE when it takes it (UAConfig.OnMessage).
var allowed = []Method{INVITE, ACK, CANCEL, BYE, PRACK, UPDATE, OPTIONS, NOTIFY}

func (ua *UA) allowHeader() string {
	var names []string
	for _, m := range allowed {
		names = append(names, string(m))
	}
	if ua.cfg.OnMessage != nil {
		names = append(names, string(MESSAGE))
	}
	if ua.cfg.OnSubscribe != nil {
		names = append(names, string(SUBSCRIBE))
	}
	for _, m := range ua.cfg.Allow {
		names = append(names, string(m))
	}
	return strings.Join(names, ", ")
}

// supported is the option tags of the extensions that the UA itself
// understands (RFC 3261 19.2).
//
// SIMPLIFIED: RFC 4028 7.1. A UA that supports the session timer lists
// timer in the Supported of every request but ACK. The UA writes its
// Supported into the INVITE and into every request within the dialog
// of a call. REGISTER, SUBSCRIBE and MESSAGE have senders of their own,
// which do not; the session timer only concerns the dialog of a call.
var supported = []string{Option100rel, OptionTimer, Option199}

// supportedTags is what the Supported header field lists: the UA's own
// extensions and those of its user (UAConfig.Supported).
func (ua *UA) supportedTags() []string {
	return append(slices.Clone(supported), ua.cfg.Supported...)
}

func (ua *UA) supportedHeader() string { return strings.Join(ua.supportedTags(), ", ") }

// contact is the Contact value of this UA.
func (ua *UA) contact() string {
	return NameAddr{URI: ua.cfg.Contact, Params: ua.cfg.ContactParams}.String()
}

// receiveRequest is Config.OnRequest. A NOTIFY of a subscription goes to
// the Subscriber. Any other request with a To tag belongs to a dialog
// (RFC 3261 12.2.2); the rest are outside one.
//
// The stack calls it for one request after the other (Config.Ordered),
// so that the requests within a dialog are checked against the remote
// sequence number in the order they arrived. It therefore never waits:
// what the user of the UA does with a request runs in a goroutine of
// its own.
func (ua *UA) receiveRequest(st *ServerTransaction) {
	req := st.Request
	if sub := ua.subscriber.find(req); sub != nil {
		go sub.receiveNotify(st)
		return
	}
	if tagOf(req.Get("To")) != "" {
		if d, ok := ua.dialog(ReceivedDialogID(req)); ok {
			d.receiveRequest(st)
			return
		}
		if req.Method == SUBSCRIBE && ua.cfg.OnSubscribe != nil {
			// The dialog of a subscription, which the notifier keeps.
			go ua.cfg.OnSubscribe(st)
			return
		}
		// RFC 3261 12.2.2: no dialog to recreate.
		ua.respondOwn(st, NewResponse(req, 481, ""))
		return
	}
	switch req.Method {
	case INVITE:
		ua.receiveInvite(st)
	case MESSAGE:
		if ua.cfg.OnMessage == nil {
			ua.respondNotAllowed(st)
			return
		}
		go ua.cfg.OnMessage(st)
	case OPTIONS:
		go ua.answerOptions(st)
	case SUBSCRIBE:
		if ua.cfg.OnSubscribe == nil {
			ua.respondNotAllowed(st)
			return
		}
		go ua.cfg.OnSubscribe(st)
	case NOTIFY:
		// RFC 6665 4.1.3: a NOTIFY that matches no subscription.
		st.Respond(NewResponse(req, 481, NewTag()))
	default:
		ua.respondNotAllowed(st)
	}
}

// own is UAConfig.OwnHeaders for a message the UA makes on its own.
func (ua *UA) own(method Method, response bool, reason error) []HeaderField {
	if ua.cfg.OwnHeaders == nil {
		return nil
	}
	return ua.cfg.OwnHeaders(method, response, reason)
}

// respondOwn sends resp, a response that the UA makes on its own, with
// the header fields of OwnHeaders.
func (ua *UA) respondOwn(st *ServerTransaction, resp *Message) {
	addHeaders(resp, ua.own(st.Request.Method, true, nil))
	st.Respond(resp)
}

// answerOptions is RFC 3261 11.2: the answer to an OPTIONS tells what
// the UA can do, without ringing anybody. It lists the methods, the
// extensions and the body types that the UA takes, and its Contact with
// the feature tags, and it has the status code that an INVITE would get
// (UAConfig.OnOptions).
func (ua *UA) answerOptions(st *ServerTransaction) {
	code, body := 200, Body{}
	if ua.cfg.OnOptions != nil {
		code, body = ua.cfg.OnOptions(st.Request)
	}
	resp := NewResponse(st.Request, code, NewTag())
	resp.Add("Allow", ua.allowHeader())
	resp.Add("Accept", "application/sdp")
	// The UA takes no content coding, and writes its reason phrases in
	// English.
	resp.Add("Accept-Encoding", "identity")
	resp.Add("Accept-Language", "en")
	resp.Add("Supported", ua.supportedHeader())
	resp.Add("Contact", ua.contact())
	body.addTo(resp)
	ua.respondOwn(st, resp)
}

// respondNotAllowed is RFC 3261 8.2.1 for a method this UA does not
// take.
func (ua *UA) respondNotAllowed(st *ServerTransaction) {
	resp := NewResponse(st.Request, 405, NewTag())
	resp.Add("Allow", ua.allowHeader())
	ua.respondOwn(st, resp)
}

// unsupportedExtensions is RFC 3261 8.2.2.3: the option tags in Require
// that neither this UA nor its user understands.
func (ua *UA) unsupportedExtensions(req *Message) []string {
	var unknown []string
	for _, tag := range req.GetAll("Require") {
		if !slices.Contains(ua.supportedTags(), tag) {
			unknown = append(unknown, tag)
		}
	}
	return unknown
}

// receiveACK is Config.OnACK: the ACK for a 2xx, which goes to the call
// whose dialog it names.
func (ua *UA) receiveACK(ack *Message) {
	d, ok := ua.dialog(ReceivedDialogID(ack))
	if !ok {
		ua.log.Info("discarded ACK that matches no dialog", "call_id", ack.Get("Call-ID"))
		return
	}
	d.receiveACK(ack)
}

// dialog is the dialog of a call that the UA routes the requests with
// this ID to.
func (ua *UA) dialog(id DialogID) (*CallDialog, bool) {
	ua.mu.Lock()
	defer ua.mu.Unlock()
	d, ok := ua.calls[id]
	return d, ok
}

func (ua *UA) logDialog(d *Dialog, event, from string) {
	ua.log.Info("sip dialog", "call_id", d.ID.CallID, "local_tag", d.ID.LocalTag, "remote_tag", d.ID.RemoteTag,
		"event", event, "from", from, "to", d.State.String())
}

// Body is a message body and its media type, such as an SDP offer of
// type application/sdp (RFC 3261 7.4, 20.15). The zero Body is no body.
type Body struct {
	Type    string
	Content []byte
}

func (b Body) addTo(m *Message) {
	if len(b.Content) == 0 {
		return
	}
	m.Set("Content-Type", b.Type)
	m.Body = slices.Clone(b.Content)
}

// BodyOf is the body of m with its media type, to carry on in another
// message as a B2BUA does. It is the zero Body when m has none.
func BodyOf(m *Message) Body {
	if len(m.Body) == 0 {
		return Body{}
	}
	return Body{Type: m.Get("Content-Type"), Content: slices.Clone(m.Body)}
}

// addHeaders appends the header fields that the user of a call gives
// for a message.
func addHeaders(m *Message, headers []HeaderField) {
	for _, h := range headers {
		m.Add(h.Name, h.Value)
	}
}
