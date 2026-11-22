package pcscf

import (
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Screen is the sip.Config.Screen of both stacks of the P-CSCF. It is
// TS 24.229 5.2.6.3.2A, and 5.2.1 for SIP Digest without TLS: a request
// other than REGISTER from a UE that has no IP association with the
// P-CSCF is discarded. The P-CSCF sends nothing back, not even a 100
// (Trying), and forwards nothing to the S-CSCF. Such a request may come
// from a UE whose registration the P-CSCF has lost; that UE notices
// that no response comes and registers again (5.1.2A.1.6).
//
// The 5.2.6.3.2A exceptions do not arise here: this P-CSCF gives no
// emergency call support, which 5.2.1 makes optional, and has no UE
// configured as an external attached network in static mode.
//
// The stack asks Screen in its own goroutine, so Screen decides from
// what the P-CSCF holds in memory (admissions) and never waits for the
// Store, which may be on another machine.
//
// SIMPLIFIED: TS 24.229 5.2.6.2 and TS 23.228. A P-CSCF has Gm towards
// the UEs and Mw towards the network, which a real one usually serves
// on different interfaces. Here both come to the same address and port,
// so a request is taken to come from the network when it is addressed
// the way only the network addresses the P-CSCF: to the URI that the
// P-CSCF put in Path, to the P-CSCF's own contact, or to the contact of
// a UE it serves.
func (p *PCSCF) Screen(req *sip.Message, source sip.Destination) bool {
	if req.Method == sip.REGISTER {
		return true
	}
	ue, sentBy := ueSourceOf(req, source)
	if p.admissions.admitsFrom(ue, sentBy, time.Now()) || p.fromNetwork(req) {
		return true
	}
	// The association may be in the Store all the same, written before
	// this process started. Once it is in memory, the next copy of the
	// request gets through: a UE retransmits over UDP (RFC 3261 17.1.1.2,
	// 17.1.2.2).
	p.lookUpAssociation(ue, req.RequestURI)
	p.log.Info("discarded a request that does not map to an IP association",
		"method", string(req.Method), "source", source.String(), "call_id", req.Get("Call-ID"))
	return false
}

// fromNetwork reports whether req is addressed as only the network
// addresses the P-CSCF (Screen).
func (p *PCSCF) fromNetwork(req *sip.Message) bool {
	if top, own := sip.PopOwnRoute(req.Clone(), p.ownURIs()...); own && top.User == "term" {
		// The Path of a registration (5.2.6.2).
		return true
	}
	target, err := sip.ParseURI(req.RequestURI)
	if err != nil {
		return false
	}
	for _, own := range p.ownURIs() {
		// A NOTIFY of the P-CSCF's own reg event subscription.
		if req.Method == sip.NOTIFY && target.User == "" && strings.EqualFold(target.Host, own.Host) && target.Port == own.Port {
			return true
		}
	}
	return p.admissions.servesContact(target)
}

// lookUpAssociation reads the Store in the background for the
// association of a UE at source, or of the contact target, and keeps
// what it finds in admissions. One lookup at a time runs for a source or
// a contact, so that retransmissions do not pile them up.
func (p *PCSCF) lookUpAssociation(source sip.Destination, target string) {
	if p.admissions.startLookUp("source " + source.String()) {
		go func() {
			defer p.admissions.endLookUp("source " + source.String())
			if a, ok := p.store.Association(source); ok {
				p.admissions.put(a)
			}
		}()
	}
	if _, err := sip.ParseURI(target); err == nil && p.admissions.startLookUp("contact "+target) {
		go func() {
			defer p.admissions.endLookUp("contact " + target)
			if a, ok := p.store.AssociationOfContact(target); ok {
				p.admissions.put(a)
			}
		}()
	}
}

// admissions is what Screen knows of the IP associations without asking
// the Store: the source, Via sent-by and expiry of each, and its
// contact. It is a copy, in memory, of the associations in the Store,
// and holds no state of its own: indexedStore keeps it in step with
// every write of the P-CSCF, Screen fills it from the Store for an
// association written before the process started, and it forgets an
// association at its KeepUntil, as the Store does. Only the P-CSCF that
// a UE registered through writes the UE's association, so a copy is not
// changed behind its back.
type admissions struct {
	mu        sync.Mutex
	bySource  map[sip.Destination]admission
	byContact map[contactKey]sip.Destination
	lookingUp map[string]bool
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

func newAdmissions() *admissions {
	return &admissions{bySource: map[sip.Destination]admission{}, byContact: map[contactKey]sip.Destination{}, lookingUp: map[string]bool{}}
}

func (s *admissions) put(a Association) {
	var contact contactKey
	if u, err := sip.ParseURI(a.Contact); err == nil {
		contact = contactKeyOf(u)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(a.Source)
	s.bySource[a.Source] = admission{sentBy: a.SentBy, expires: a.Expires, keepUntil: a.KeepUntil, contact: contact}
	if a.Contact != "" {
		s.byContact[contact] = a.Source
	}
	if a.KeepUntil.IsZero() {
		return
	}
	time.AfterFunc(time.Until(a.KeepUntil), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, ok := s.bySource[a.Source]; ok && current.keepUntil.Equal(a.KeepUntil) {
			s.removeLocked(a.Source)
		}
	})
}

func (s *admissions) remove(source sip.Destination) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(source)
}

// removeLocked is remove with s.mu held.
func (s *admissions) removeLocked(source sip.Destination) {
	if old, ok := s.bySource[source]; ok {
		delete(s.byContact, old.contact)
		delete(s.bySource, source)
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

func (s *admissions) startLookUp(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookingUp[key] {
		return false
	}
	s.lookingUp[key] = true
	return true
}

func (s *admissions) endLookUp(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lookingUp, key)
}

// indexedStore is the Store of the P-CSCF with admissions kept in step
// with each write of an association.
type indexedStore struct {
	Store
	admissions *admissions
}

func (s indexedStore) PutAssociation(a Association) {
	s.Store.PutAssociation(a)
	s.admissions.put(a)
}

func (s indexedStore) DeleteAssociation(source sip.Destination) {
	s.Store.DeleteAssociation(source)
	s.admissions.remove(source)
}

// ueSourceOf is where a request of a UE comes from, as the P-CSCF knows
// the UE, and the sent-by of its top Via. A UE that registered over UDP
// sends a request over TCP when it is larger than 1300 bytes (RFC 3261
// 18.1.1), as a VoLTE INVITE is. Its connection comes from a port of the
// moment, so the UE of such a request is the one at the sent-by of its
// Via, when that has the address the request came from.
//
// SIMPLIFIED: TS 24.229 5.2.2.3. The IP association pairs the address
// and port that the REGISTER came from with the sent-by of its Via. A
// request over TCP is matched by its sent-by alone, when that has the
// address the request came from: the port of its connection is not
// checked, since it is a port of the moment that no association holds.
// A TCP request whose sent-by names another address is not recognized.
// Nothing authenticates the port in that sent-by, so a host that shares
// the UE's IP address could name it. With IPsec the UE's TCP
// connections would come from its protected client port instead, and
// the security associations would authenticate them (TS 33.203 7.1).
func ueSourceOf(req *sip.Message, source sip.Destination) (sip.Destination, string) {
	via, err := sip.ParseVia(req.Get("Via"))
	if err != nil {
		return source, ""
	}
	if source.Transport != sip.TCP {
		return source, via.SentBy()
	}
	sentBy, err := netip.ParseAddrPort(via.SentBy())
	if err != nil || sentBy.Addr() != source.Addr.Addr() {
		return source, via.SentBy()
	}
	return sip.Destination{Transport: sip.UDP, Addr: sentBy}, via.SentBy()
}
