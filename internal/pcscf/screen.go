package pcscf

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// screen is TS 24.229 5.2.6.3.2A, and 5.2.1 for SIP Digest without TLS,
// for a request other than REGISTER: one from a UE that has no IP
// association with the P-CSCF is discarded. The P-CSCF sends nothing
// back, not even a 100 (Trying), and forwards nothing to the S-CSCF.
// Such a request may come from a UE whose registration the P-CSCF has
// lost; that UE notices that no response comes and registers again
// (5.1.2A.1.6).
//
// The 5.2.6.3.2A exceptions do not arise here: this P-CSCF gives no
// emergency call support, which 5.2.1 makes optional, and has no UE
// configured as an external attached network in static mode.
//
// AdmitUnprotected and AdmitProtected ask screen first, so it runs off
// the stack's loop and may wait for the Store, which may be on another
// machine. It decides from what the P-CSCF holds in memory (admissions),
// and reads the Store only for a request that this does not let in: the
// association may be there all the same, written before this process
// started, by a P-CSCF of the same name that this one replaces. The
// request waits for that read rather than being discarded, since a
// request over TCP comes only once (RFC 3261 17.1.1.2), as a VoLTE
// INVITE does.
//
// SIMPLIFIED: TS 24.229 5.2.6.2 and TS 23.228. A P-CSCF has Gm towards
// the UEs and Mw towards the network, which a real one usually serves
// on different interfaces. Here both come to the same address and port,
// so a request is taken to come from the network when it is addressed
// the way only the network addresses the P-CSCF: to the URI that the
// P-CSCF put in Path, to the P-CSCF's own contact, or to the contact of
// a UE it serves.
func (p *PCSCF) screen(req *sip.Message, source sip.Destination) bool {
	ue, sentBy := p.ueSourceOf(req, source)
	known := func() bool { return p.admissions.admitsFrom(ue, sentBy, time.Now()) || p.fromNetwork(req) }
	if known() {
		return true
	}
	p.lookUpAssociation(ue, req.RequestURI)
	if known() {
		return true
	}
	p.log.Info("discarded a request that does not map to an IP association",
		"method", string(req.Method), "source", source.String(), "call_id", req.Get("Call-ID"))
	return false
}

// fromNetwork reports whether req is addressed as only the network
// addresses the P-CSCF (screen).
func (p *PCSCF) fromNetwork(req *sip.Message) bool {
	if top, own := sip.PopOwnRoute(req.Clone(), p.ownURIs()...); own && top.User == "term" {
		// The Path of a registration (5.2.6.2).
		return true
	}
	// A NOTIFY of the P-CSCF's own reg event subscription.
	if p.ownRegEventNotify(req) {
		return true
	}
	target, err := sip.ParseURI(req.RequestURI)
	if err != nil {
		return false
	}
	return p.admissions.servesContact(target)
}

// lookUpAssociation reads the Store for the association of a UE at
// source, and for that of the contact target, and keeps what it finds
// in admissions. A lookup that fails leaves admissions as it was, so the
// request is discarded, and the next request of the UE reads the Store
// again.
func (p *PCSCF) lookUpAssociation(source sip.Destination, target string) {
	keep := func(err error) {
		if err != nil {
			p.log.Warn("could not look up an IP association: the store failed", "source", source.String(), "error", err.Error())
		}
	}
	keep(p.admissions.lookUp("source "+source.String(), func() (Association, bool, error) {
		return p.store.Association(context.Background(), source)
	}))
	if _, err := sip.ParseURI(target); err == nil {
		keep(p.admissions.lookUp("contact "+target, func() (Association, bool, error) {
			return p.associationOfContact(target)
		}))
	}
}

// admissions is what screen knows of the IP associations without asking
// the Store: the source, Via sent-by and expiry of each, and its
// contact. It is a copy, in memory, of the associations in the Store,
// and holds no state of its own: indexedStore keeps it in step with
// every write of the P-CSCF, screen fills it from the Store for an
// association written before the process started, and it forgets an
// association at its KeepUntil, as the Store does. Only the P-CSCF that
// a UE registered through writes the UE's association, so a copy is not
// changed behind its back.
//
// The associations in admissions are those of the UEs that use this
// instance, which makes them the users of its maintenance function
// (Function).
type admissions struct {
	mu        sync.Mutex
	bySource  map[sip.Destination]admission
	byContact map[contactKey]sip.Destination
	// lookingUp holds the lookup in the Store that runs for each key
	// (lookUp).
	lookingUp map[string]*pendingLookUp
	// size is how many associations bySource holds, which users reads
	// without mu.
	size atomic.Int64
	// gone is called, without mu, after an association has gone.
	gone func()
}

type admission struct {
	sentBy             string
	expires, keepUntil time.Time
	contact            contactKey
}

// contactKey is what AssociationOfContact compares of a contact: user,
// host and port.
type contactKey struct {
	user, host string
	port       int
}

func contactKeyOf(u sip.URI) contactKey {
	return contactKey{user: u.User, host: strings.ToLower(u.Host), port: u.Port}
}

func newAdmissions(gone func()) *admissions {
	return &admissions{bySource: map[sip.Destination]admission{}, byContact: map[contactKey]sip.Destination{}, lookingUp: map[string]*pendingLookUp{},
		gone: gone}
}

// users is how many associations admissions holds.
func (s *admissions) users() int { return int(s.size.Load()) }

func (s *admissions) put(a Association) {
	var contact contactKey
	if u, err := sip.ParseURI(a.Contact); err == nil {
		contact = contactKeyOf(u)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.removeLocked(a.Source) {
		s.size.Add(1)
	}
	s.bySource[a.Source] = admission{sentBy: a.SentBy, expires: a.Expires, keepUntil: a.KeepUntil, contact: contact}
	if a.Contact != "" {
		s.byContact[contact] = a.Source
	}
	if a.KeepUntil.IsZero() {
		return
	}
	time.AfterFunc(time.Until(a.KeepUntil), func() {
		s.mu.Lock()
		gone := false
		if current, ok := s.bySource[a.Source]; ok && current.keepUntil.Equal(a.KeepUntil) {
			gone = s.removeLocked(a.Source)
		}
		s.mu.Unlock()
		s.counted(gone)
	})
}

func (s *admissions) remove(source sip.Destination) {
	s.mu.Lock()
	gone := s.removeLocked(source)
	s.mu.Unlock()
	s.counted(gone)
}

// removeLocked forgets the association of source, with s.mu held, and
// reports whether there was one. It leaves size to the caller, so that
// an association that put replaces is never missing from it.
func (s *admissions) removeLocked(source sip.Destination) bool {
	old, ok := s.bySource[source]
	if ok {
		delete(s.byContact, old.contact)
		delete(s.bySource, source)
	}
	return ok
}

// counted takes an association that removeLocked reported gone out of
// size, and tells gone, after s.mu has been released.
func (s *admissions) counted(gone bool) {
	if gone {
		s.size.Add(-1)
		s.gone()
	}
}

// admitsFrom is the check of PCSCF.association: an association of the
// source, with this Via sent-by, that has not expired. An association
// that has ended takes no more requests from the UE (endAssociation).
func (s *admissions) admitsFrom(source sip.Destination, sentBy string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.bySource[source]
	return ok && a.sentBy == sentBy && now.Before(a.expires)
}

// servesContact reports whether target is the contact of an
// association. Its expiry is not checked, as associationOfContact does
// not check it.
func (s *admissions) servesContact(target sip.URI) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.byContact[contactKeyOf(target)]
	return ok
}

// pendingLookUp is a read of the Store that runs for a key of lookingUp,
// and what came of it once done is closed.
type pendingLookUp struct {
	done chan struct{}
	err  error
}

// lookUp has read find the association that key names in the Store,
// and keeps what it finds. The stack decides on the requests of other
// Call-IDs side by side (sip.Config.Admit), so several of a source may
// miss the copy at once. One read at a time runs for a key, and a call
// that comes while it runs waits for it and returns its error, so that
// a burst of requests from a source reads the Store once.
func (s *admissions) lookUp(key string, read func() (Association, bool, error)) error {
	s.mu.Lock()
	l, running := s.lookingUp[key]
	if !running {
		l = &pendingLookUp{done: make(chan struct{})}
		s.lookingUp[key] = l
	}
	s.mu.Unlock()
	if running {
		<-l.done
		return l.err
	}
	a, ok, err := read()
	if err == nil && ok {
		s.put(a)
	}
	l.err = err
	s.mu.Lock()
	delete(s.lookingUp, key)
	s.mu.Unlock()
	close(l.done)
	return err
}

// indexedStore is the Store of the P-CSCF with admissions kept in step
// with each write of an association. A write that fails leaves
// admissions as it was: the association it held is still bounded by its
// own Expires, and forgetting it would drop the next request of the UE,
// which a UE on TCP does not send again, until a lookup in the Store
// brought it back.
type indexedStore struct {
	Store
	admissions *admissions
}

func (s indexedStore) PutAssociation(ctx context.Context, a Association) error {
	if err := s.Store.PutAssociation(ctx, a); err != nil {
		return err
	}
	s.admissions.put(a)
	return nil
}

func (s indexedStore) DeleteAssociation(ctx context.Context, source sip.Destination) error {
	err := s.Store.DeleteAssociation(ctx, source)
	s.admissions.remove(source)
	return err
}

// ueSourceOf is where a request of a UE comes from, as the P-CSCF knows
// the UE, and the sent-by of its top Via. A UE that registered over UDP
// sends a request over TCP when it is larger than 1300 bytes (RFC 3261
// 18.1.1), as a VoLTE INVITE is. Its connection comes from a port of the
// moment, so the UE of such a request is the one at the sent-by of its
// Via, when that has the address the request came from.
//
// With ESP every request of a UE comes from its protected client port,
// over UDP as well (TS 33.203 7.1 item 2), which no association holds
// either, so such a request is matched by its sent-by too. The kernel
// has let it through on the security associations of that UE, so the
// address it came from is the UE's, and TS 33.203 7.1 rule 2 has the
// P-CSCF check that the Via names that same address, which is what
// matching it here does.
//
// SIMPLIFIED: TS 24.229 5.2.2.3. The IP association pairs the address
// and port that the REGISTER came from with the sent-by of its Via. A
// request over TCP is matched by its sent-by alone, when that has the
// address the request came from: the port of its connection is not
// checked, since it is a port of the moment that no association holds.
// A TCP request whose sent-by names another address is not recognized.
// Without ESP nothing authenticates the port in that sent-by, so a host
// that shares the UE's IP address could name it; with ESP the security
// associations authenticate the sender (TS 33.203 7.1).
func (p *PCSCF) ueSourceOf(req *sip.Message, source sip.Destination) (sip.Destination, string) {
	via, err := sip.ParseVia(req.Get("Via"))
	if err != nil {
		return source, ""
	}
	if source.Transport != sip.TCP && p.cfg.ESP == nil {
		return source, via.SentBy()
	}
	sentBy, err := netip.ParseAddrPort(via.SentBy())
	if err != nil || sentBy.Addr() != source.Addr.Addr() {
		return source, via.SentBy()
	}
	return sip.Destination{Transport: sip.UDP, Addr: sentBy}, via.SentBy()
}
