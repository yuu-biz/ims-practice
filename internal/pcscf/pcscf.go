// Package pcscf is the P-CSCF role: the first SIP hop of the UE. On
// REGISTER it adds what the home network needs to reach the UE again,
// finds the I-CSCF of the home network through DNS, and remembers the
// registration once the S-CSCF accepts it (TS 24.229 5.2.2). It then
// subscribes to the registration state, to learn when the registration
// ends (5.2.3).
package pcscf

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Locator finds where to send a request for a SIP URI (RFC 3263).
// *sipdns.Resolver is one.
type Locator interface {
	Locate(ctx context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error)
}

type Config struct {
	// Host and Port name the P-CSCF in Path.
	Host string
	Port int
	// ProtectedServerPort is port_ps, the port where UEs that register
	// with IMS AKA send their protected messages (TS 33.203 7.1). Zero
	// means that the P-CSCF does not offer IMS AKA.
	ProtectedServerPort int
	// VisitedNetworkID goes into P-Visited-Network-ID.
	VisitedNetworkID string
	// HostAccess, when set, gives UEs behind the host's NAT the
	// published address of the relay.
	HostAccess *HostAccess
}

// Association is the IP association of TS 24.229 5.2.2.3 for SIP Digest
// without TLS: after a successful registration, requests from this
// address and Via sent-by belong to this private identity. It also
// keeps what the P-CSCF learns from the 200 OK (5.2.2.1).
type Association struct {
	Source sip.Destination
	// Contact is the URI the UE registered. Requests to the UE carry it
	// in the Request-URI, but go to Source.
	Contact          string
	SentBy           string
	PrivateIdentity  string
	PublicIdentities []string // from P-Associated-URI, default first
	ServiceRoute     []string
	Expires          time.Time
	// KeepUntil is when the Store forgets the association:
	// associationGrace after Expires (endAssociation), also when the
	// P-CSCF never hears that the registration has ended, such as when
	// the S-CSCF stops before its NOTIFY. Zero keeps it until
	// DeleteAssociation.
	KeepUntil time.Time
	// SignallingSession is the policy session with which the PCRF
	// watches the signalling path of the UE (TS 29.214 4.4.5). It is
	// empty when the P-CSCF has no policy control, or the PCRF knows no
	// IP-CAN session for the UE; the calls of such a UE get no policy
	// control either.
	SignallingSession policy.SessionID
	// SignallingLost is true once the PCRF has reported that the bearer
	// of the UE's signalling is gone. Requests to the UE are then
	// refused, until the UE registers again (TS 24.229 5.2.6.4.3 item 1
	// with NOTE 1).
	SignallingLost bool
}

// Store keeps the associations and the dialogs. It is an interface so
// that the state can live outside the node; MemoryStore keeps
// it in the process.
//
// An error is a failure of the store, after which the caller cannot
// tell what the store holds. The P-CSCF writes every expiry into what
// it stores, as a time; the Store keeps nothing longer than that.
//
// A SIP transaction, an event of the PCRF and a timer carry no context
// of their own, so the P-CSCF passes context.Background() and relies on
// the time limit that an adapter on a store outside the process gives
// each operation.
type Store interface {
	// Association returns the association of a source, and whether
	// there is one.
	Association(ctx context.Context, source sip.Destination) (Association, bool, error)
	// AssociationOfContact finds the association whose registered
	// contact has this host, port and user.
	AssociationOfContact(ctx context.Context, contact string) (Association, bool, error)
	// PutAssociation keeps a, in place of the association of its
	// source, until its KeepUntil.
	PutAssociation(ctx context.Context, a Association) error
	DeleteAssociation(ctx context.Context, source sip.Destination) error
	// AssociationOfPolicySession finds the association whose
	// SignallingSession is session, for an event that the PCRF reports
	// about that session.
	AssociationOfPolicySession(ctx context.Context, session policy.SessionID) (Association, bool, error)
	// PutDialog stores a new dialog under its key.
	PutDialog(ctx context.Context, d Dialog) error
	// UpdateDialog changes the dialog of key with change, as one step
	// that no other change of that dialog comes between, and returns the
	// dialog as it is afterwards. It reports false, and does not call
	// change, when there is no such dialog: a dialog that has ended does
	// not come back. change must not block, and may run more than once,
	// each time on a fresh copy.
	UpdateDialog(ctx context.Context, key DialogKey, change func(*Dialog)) (Dialog, bool, error)
	// TakeDialog removes and returns the dialog of key. Only one caller
	// gets it, and that one ends what the dialog holds; this is what
	// makes the end of a dialog happen once, however many requests and
	// events ask for it.
	TakeDialog(ctx context.Context, key DialogKey) (Dialog, bool, error)
	// DialogOfPolicySession finds the dialog whose PolicySession is
	// session, for an event that the PCRF reports about that session.
	DialogOfPolicySession(ctx context.Context, session policy.SessionID) (Dialog, bool, error)
	// SecurityAssociations returns the entry of the SA table for a UE's
	// address and protected server port, until it expires;
	// PutSecurityAssociations replaces it.
	SecurityAssociations(ctx context.Context, ue netip.AddrPort) (SecurityAssociations, bool, error)
	PutSecurityAssociations(ctx context.Context, sas SecurityAssociations) error
	DeleteSecurityAssociations(ctx context.Context, ue netip.AddrPort) error
}

// MemoryStore is a Store in the memory of one process. Two P-CSCFs
// share state only when they share one MemoryStore, as in a test; a
// network of several nodes needs a store outside them. It never fails.
type MemoryStore struct {
	mu           sync.Mutex
	associations map[sip.Destination]Association
	dialogs      map[DialogKey]Dialog
	// dialogOfSession and associationOfSession are the indexes of the
	// dialogs and the associations by policy session.
	dialogOfSession      map[policy.SessionID]DialogKey
	associationOfSession map[policy.SessionID]sip.Destination
	saTable
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{associations: map[sip.Destination]Association{}, dialogs: map[DialogKey]Dialog{},
		dialogOfSession: map[policy.SessionID]DialogKey{}, associationOfSession: map[policy.SessionID]sip.Destination{}}
}

func (s *MemoryStore) AssociationOfContact(_ context.Context, contact string) (Association, bool, error) {
	want, err := sip.ParseURI(contact)
	if err != nil {
		return Association{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.associations {
		got, err := sip.ParseURI(a.Contact)
		if err == nil && got.User == want.User && strings.EqualFold(got.Host, want.Host) && got.Port == want.Port {
			return a, true, nil
		}
	}
	return Association{}, false, nil
}

func (s *MemoryStore) PutDialog(_ context.Context, d Dialog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putDialog(d)
	return nil
}

// putDialog stores d and keeps the index by policy session in step.
func (s *MemoryStore) putDialog(d Dialog) {
	if old, ok := s.dialogs[d.Key]; ok && old.PolicySession != d.PolicySession {
		delete(s.dialogOfSession, old.PolicySession)
	}
	s.dialogs[d.Key] = d
	if d.PolicySession != "" {
		s.dialogOfSession[d.PolicySession] = d.Key
	}
}

func (s *MemoryStore) UpdateDialog(_ context.Context, key DialogKey, change func(*Dialog)) (Dialog, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dialogs[key]
	if !ok {
		return Dialog{}, false, nil
	}
	change(&d)
	s.putDialog(d)
	return d, true, nil
}

func (s *MemoryStore) TakeDialog(_ context.Context, key DialogKey) (Dialog, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dialogs[key]
	delete(s.dialogs, key)
	delete(s.dialogOfSession, d.PolicySession)
	return d, ok, nil
}

func (s *MemoryStore) DialogOfPolicySession(_ context.Context, session policy.SessionID) (Dialog, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.dialogOfSession[session]
	if !ok {
		return Dialog{}, false, nil
	}
	d, ok := s.dialogs[key]
	return d, ok, nil
}

func (s *MemoryStore) Association(_ context.Context, source sip.Destination) (Association, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.associations[source]
	return a, ok, nil
}

// PutAssociation keeps a. The store removes it at its KeepUntil, unless
// another association of the source has taken its place by then.
func (s *MemoryStore) PutAssociation(_ context.Context, a Association) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.associationOfSession, s.associations[a.Source].SignallingSession)
	s.associations[a.Source] = a
	if a.SignallingSession != "" {
		s.associationOfSession[a.SignallingSession] = a.Source
	}
	if a.KeepUntil.IsZero() {
		return nil
	}
	time.AfterFunc(time.Until(a.KeepUntil), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, ok := s.associations[a.Source]; ok && current.KeepUntil.Equal(a.KeepUntil) {
			s.deleteAssociation(a.Source)
		}
	})
	return nil
}

func (s *MemoryStore) DeleteAssociation(_ context.Context, source sip.Destination) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteAssociation(source)
	return nil
}

func (s *MemoryStore) deleteAssociation(source sip.Destination) {
	delete(s.associationOfSession, s.associations[source].SignallingSession)
	delete(s.associations, source)
}

func (s *MemoryStore) AssociationOfPolicySession(_ context.Context, session policy.SessionID) (Association, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.associationOfSession[session]
	if !ok {
		return Association{}, false, nil
	}
	a, ok := s.associations[source]
	return a, ok, nil
}

type PCSCF struct {
	cfg        Config
	locator    Locator
	store      Store
	relay      Relay                // nil passes media end to end
	policy     policy.PolicyControl // nil asks nothing of a PCRF
	stack      ims.StackRef
	subscriber *sip.Subscriber
	// admissions is what Screen knows of the IP associations in store.
	admissions *admissions
	log        *slog.Logger

	// protectedStack listens on the protected server port.
	protectedStack ims.StackRef

	// regEvents are the P-CSCF's own subscriptions to the reg event
	// package, by private identity in lower case. Like the other
	// timers, they live in the process: a subscription is mostly its
	// refresh and expiry timers.
	mu        sync.Mutex
	regEvents map[string]*sip.Subscription
	// setups are the initial INVITEs that have created a dialog and have
	// no final response yet. An INVITE transaction lives in the process
	// that forwards it, and only there can it be cancelled; the PCRF
	// reports the events of a policy session to the P-CSCF instance that
	// opened it, which is the one that holds the INVITE. An entry lasts
	// as long as its transaction, as the stack's own entries do. The
	// lock is their own, apart from that of regEvents, and is held only
	// to read or change the map.
	setupsMu sync.Mutex
	setups   map[DialogKey]*setup

	// ackWait is how long a release waits for the ACK of a 2xx (release):
	// 64*T1, after which the callee itself gives up waiting for the ACK
	// (RFC 3261 13.3.1.4).
	ackWait time.Duration
}

// New makes a P-CSCF. relay may be nil, and then RTP goes between the
// UEs directly. Both stacks of the P-CSCF need its Screen in their
// sip.Config, and the one on the unprotected port its AdmitUnprotected
// as well.
func New(cfg Config, locator Locator, store Store, relay Relay, log *slog.Logger) *PCSCF {
	admitted := newAdmissions()
	p := &PCSCF{cfg: cfg, locator: locator, store: indexedStore{Store: store, admissions: admitted}, admissions: admitted,
		relay: relay, log: log, regEvents: map[string]*sip.Subscription{}, setups: map[DialogKey]*setup{}, ackWait: 64 * sip.DefaultTimers().T1}
	p.subscriber = sip.NewSubscriber(p.contactURI(), p.locate, log)
	return p
}

// OnRequest is the sip.Config.OnRequest of the P-CSCF, on its
// unprotected port.
func (p *PCSCF) OnRequest(st *sip.ServerTransaction) {
	if st.Request.Method != sip.REGISTER {
		p.session(st)
		return
	}
	p.register(st, false)
}

// SetStack gives the P-CSCF the stack it runs on, which it needs to
// forward ACKs. Call it right after the stack is made.
func (p *PCSCF) SetStack(stack *sip.Stack) { p.stack.Set(stack) }

// register is TS 24.229 5.2.2.1, with the rules of 5.2.2.2 for IMS AKA
// and of 5.2.2.3 for SIP Digest without TLS. protected tells that the
// REGISTER came to the protected server port. After the 200 to the first
// initial REGISTER of a private identity, the P-CSCF subscribes to the
// reg event package (5.2.3).
//
// SIMPLIFIED: TS 24.229 5.2.2.1 item 6. The P-CSCF does not store the
// term-ioi of the P-Charging-Vector of the 200: that IOI names the home
// network in the charging records of the P-CSCF, which no node of this
// project writes, and no later message carries it.
func (p *PCSCF) register(st *sip.ServerTransaction, protected bool) {
	req := st.Request
	publicID, err := ims.PublicIdentity(req.Get("To"))
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	privateID, creds, err := ims.PrivateIdentity(req, publicID)
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	log := p.log.With("private_identity", privateID, "source", st.Source.String(), "call_id", req.Get("Call-ID"))

	out := req.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		st.Respond(sip.NewResponse(req, 483, sip.NewTag()))
		return
	}
	crossBoundary(out, requestFromUE)
	// SIMPLIFIED: TS 24.229 5.2.2.1 item 1 and RFC 5626. The P-CSCF
	// URI in Path should carry a flow token and the "ob" parameter,
	// which belong to SIP outbound. This project does not implement
	// outbound, so the URI only marks the terminating direction.
	path := p.pathURI()
	if protected {
		path = p.protectedPathURI()
	}
	if out.Get("Path") == "" {
		out.Add("Path", path)
	} else {
		// RFC 3327 5.1: a proxy adds its URI as the first value.
		out.Prepend("Path", path)
	}
	// Item 2: Require, not Supported.
	addOptionTag(out, "Require", "path")
	// Item 3: a new charging identifier for this request (TS 32.260).
	p.chargeRequest(out, rand.Text())
	// Item 4.
	out.Set("P-Visited-Network-ID", p.cfg.VisitedNetworkID)

	// 5.2.2.1 items 1 and 2: Security-Client with sec-agree, or the
	// protected server port, is IMS AKA (5.2.2.2). Otherwise 5.2.2.3 tells
	// the I-CSCF and the S-CSCF whether this UE already has an IP
	// association with the P-CSCF.
	assoc, associated, err := p.association(st, privateID)
	if err != nil {
		st.Respond(storeFailed(req, err, log))
		return
	}
	var integrity sip.IntegrityProtected
	var agreement *secAgreement
	switch {
	case protected || asksSecAgree(req):
		var refusal *sip.Message
		if agreement, refusal = p.receiveSecAgree(st, out, privateID, creds, protected, log); refusal != nil {
			st.Respond(refusal)
			return
		}
		integrity = agreement.integrity
	case associated:
		integrity = sip.IPAssociationYes
	case creds != nil && creds.Response != "":
		integrity = sip.IPAssociationPending
	}
	// Whatever the UE wrote is replaced (5.2.2.1 item 4B).
	if creds != nil {
		c := *creds
		c.SetIntegrityProtected(integrity)
		out.Set("Authorization", c.String())
	}

	u, err := sip.ParseURI(req.RequestURI)
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	transport, _ := u.Params.Get("transport")
	targets, err := p.locator.Locate(context.Background(), u.Host, u.Port, sip.Transport(strings.ToUpper(transport)))
	if err != nil || len(targets) == 0 {
		// 5.2.2.1: if no I-CSCF can be reached, 504.
		log.Warn("cannot locate the I-CSCF", "domain", u.Host, "error", fmt.Sprint(err))
		st.Respond(sip.NewResponse(req, 504, sip.NewTag()))
		return
	}

	final := p.forward(st, out, targets, log)
	if agreement != nil {
		if final, err = p.answerSecAgree(st, final, agreement, log); err != nil {
			st.Respond(storeFailed(req, err, log))
			return
		}
	}
	var registered *Association
	var interval time.Duration
	switch {
	case final.StatusCode == 200:
		a, d, ok, err := p.registered(st, final, privateID, assoc, log)
		if err != nil {
			// A 200 would stand for a registration that no P-CSCF
			// instance can find.
			st.Respond(storeFailed(req, err, log))
			return
		}
		if ok {
			registered, interval = &a, d
		}
	case final.StatusCode == 500 || final.StatusCode == 504:
		// 5.2.2.3: the network failed, so the association can no longer
		// be trusted.
		if err := p.deleteAssociation(ueSource(st), log); err != nil {
			log.Warn("the IP association stays until it expires: the store failed", "error", err.Error())
		}
	}
	crossBoundary(final, responseToUE)
	st.Respond(final)
	if registered != nil {
		if !associated || assoc.SignallingLost {
			// TS 29.214 A.4: on the initial registration, after its 200
			// (TS 29.213 B.1). A refresh keeps the session of the
			// registration it refreshes, unless the signalling path was
			// lost: this REGISTER shows that the UE has it again (TS
			// 24.229 5.2.6.4.3 NOTE 1).
			*registered = p.watchSignallingPath(*registered, log)
		}
		p.subscribeRegEvent(*registered, interval, log)
	}
}

// deleteAssociation forgets the IP association of source, and with it
// the watch on its signalling path.
func (p *PCSCF) deleteAssociation(source sip.Destination, log *slog.Logger) error {
	ctx := context.Background()
	a, ok, err := p.store.Association(ctx, source)
	if err != nil || !ok {
		return err
	}
	p.unwatchSignallingPath(&a, log)
	return p.store.DeleteAssociation(ctx, source)
}

// storeFailed is the 500 (Server Internal Error) to req, a request whose
// state the P-CSCF cannot read or write (RFC 3261 21.5.1). The UE or the
// network may send the request again.
func storeFailed(req *sip.Message, err error, log *slog.Logger) *sip.Message {
	log.Warn("the store failed", "method", string(req.Method), "error", err.Error())
	return sip.NewResponse(req, 500, sip.NewTag())
}

// errStore marks a failure of the Store in an error that may also be
// one of routing or of the network, so that the request gets 500 rather
// than the response to those.
var errStore = errors.New("pcscf: the store failed")

func storeError(err error) error { return fmt.Errorf("%w: %w", errStore, err) }

// pathURI is the P-CSCF's URI in Path, which marks the requests to the
// UE (5.2.6.2).
func (p *PCSCF) pathURI() string { return fmt.Sprintf("<sip:term@%s:%d;lr>", p.cfg.Host, p.cfg.Port) }

// forward tries the I-CSCFs in the order DNS gave them. TS 24.229
// 5.2.2.1 item 7 moves on to the next one on a timeout, a 3xx or a 480,
// and answers 504 when none is left.
func (p *PCSCF) forward(st *sip.ServerTransaction, out *sip.Message, targets []sip.Destination, log *slog.Logger) *sip.Message {
	for _, target := range targets {
		final, err := st.ProxyWith(out, target, func(resp *sip.Message) { crossBoundary(resp, responseToUE) })
		switch {
		case err != nil:
			log.Warn("I-CSCF did not answer", "target", target.String(), "error", err.Error())
		case final.StatusCode/100 == 3 || final.StatusCode == 480:
			log.Info("I-CSCF turned the request away", "target", target.String(), "status", final.StatusCode)
		default:
			return final
		}
	}
	return sip.NewResponse(st.Request, 504, sip.NewTag())
}

// ueSource is ueSourceOf for the request of st.
func ueSource(st *sip.ServerTransaction) sip.Destination {
	source, _ := ueSourceOf(st.Request, st.Source)
	return source
}

// association returns the IP association of the request's source
// (ueSource), if it is still valid and belongs to privateID. An empty
// privateID accepts any user.
func (p *PCSCF) association(st *sip.ServerTransaction, privateID string) (Association, bool, error) {
	via, viaErr := sip.ParseVia(st.Request.Get("Via"))
	source := ueSource(st)
	a, ok, err := p.store.Association(context.Background(), source)
	if err != nil || !ok {
		return Association{}, false, err
	}
	if time.Now().After(a.Expires) {
		return Association{}, false, p.deleteAssociation(source, p.log)
	}
	if viaErr != nil || via.SentBy() != a.SentBy || (privateID != "" && !strings.EqualFold(a.PrivateIdentity, privateID)) {
		return Association{}, false, nil
	}
	return a, true, nil
}

// registered handles the 200 OK to a REGISTER (TS 24.229 5.2.2.1 and
// 5.2.2.3): with a non-zero interval it creates or refreshes the IP
// association and stores Service-Route and P-Associated-URI, and
// returns the association with the interval; with zero it removes the
// association.
func (p *PCSCF) registered(st *sip.ServerTransaction, resp *sip.Message, privateID string, old Association, log *slog.Logger) (Association, time.Duration, bool, error) {
	ctx := context.Background()
	interval := grantedInterval(resp)
	if interval == 0 {
		a, ok, err := p.store.Association(ctx, ueSource(st))
		if err == nil && ok {
			err = p.endAssociation(a, log)
		}
		if err != nil {
			return Association{}, 0, false, err
		}
		log.Info("deregistered")
		return Association{}, 0, false, nil
	}
	via, _ := sip.ParseVia(st.Request.Get("Via"))
	expires := time.Now().Add(interval)
	a := Association{
		Source:          ueSource(st),
		Contact:         contactOf(st.Request),
		SentBy:          via.SentBy(),
		PrivateIdentity: privateID,
		ServiceRoute:    resp.GetAll("Service-Route"),
		Expires:         expires,
		KeepUntil:       expires.Add(associationGrace),
		// A refresh keeps the watch on the signalling path.
		SignallingSession: old.SignallingSession,
	}
	for _, v := range resp.GetAll("P-Associated-URI") {
		if na, err := sip.ParseNameAddr(v); err == nil {
			a.PublicIdentities = append(a.PublicIdentities, na.URI)
		}
	}
	if err := p.store.PutAssociation(ctx, a); err != nil {
		return Association{}, 0, false, err
	}
	log.Info("registered", "public_identities", a.PublicIdentities, "service_route", a.ServiceRoute,
		"expires", int(interval/time.Second), "refresh", old.PrivateIdentity != "")
	return a, interval, true, nil
}

func addOptionTag(m *sip.Message, header, tag string) {
	for _, v := range m.GetAll(header) {
		if strings.EqualFold(strings.TrimSpace(v), tag) {
			return
		}
	}
	m.Add(header, tag)
}

// associationGrace is how long an IP association that has ended still
// carries requests to the UE: 64*T1, the longest a non-INVITE
// transaction lasts over UDP (RFC 3261 17.1.2.2, 17.2.2). TS 24.229
// 5.2.5.1 item 2 deletes the association once the server transaction of
// the deregistration has terminated, and 5.2.5.2 NOTE 2 keeps it until
// the NOTIFY that reports the deregistration has reached the UE.
var associationGrace = 64 * sip.DefaultTimers().T1

// endAssociation ends the IP association of a UE whose registration has
// ended. The P-CSCF takes no more requests from the UE at once, but
// still passes requests to it for associationGrace, until the Store
// forgets the association at its KeepUntil. The watch on the signalling
// path ends at once (TS 29.214 A.4).
func (p *PCSCF) endAssociation(a Association, log *slog.Logger) error {
	ctx := context.Background()
	p.unwatchSignallingPath(&a, log)
	a.Expires = time.Now()
	a.KeepUntil = a.Expires.Add(associationGrace)
	// The association ends before the security associations go, so that
	// a write that fails in between leaves security associations without
	// an association, whose requests are dropped, rather than an
	// association still in force without them, whose unprotected
	// requests would be taken.
	if err := p.store.PutAssociation(ctx, a); err != nil {
		return err
	}
	// TS 33.203 7.4.2a: the security associations of the UE go with its
	// last registered identity.
	return p.store.DeleteSecurityAssociations(ctx, a.Source.Addr)
}

// associationOfContact is the association of a contact, for a request
// to the UE. Its expiry is not checked: the S-CSCF decides whether the
// registration has ended, routes nothing to a user it no longer serves,
// and tells the P-CSCF with the reg event (5.2.5.2), which ends the
// association. The expiry that the P-CSCF noted comes a moment after the
// S-CSCF's, and the NOTIFY that reports the end has to reach the UE.
func (p *PCSCF) associationOfContact(contact string) (Association, bool, error) {
	return p.store.AssociationOfContact(context.Background(), contact)
}

func contactOf(req *sip.Message) string {
	na, err := sip.ParseNameAddr(req.Get("Contact"))
	if err != nil {
		return ""
	}
	return na.URI
}
