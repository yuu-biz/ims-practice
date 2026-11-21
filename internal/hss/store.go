package hss

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// Subscription is one IMS subscription: a private user identity and the
// public user identities registered with it (TS 23.228 4.3.3.4).
//
// SIMPLIFIED: TS 29.228 6.1.1.1 and 6.1.2.1. The HSS keeps a
// registration state per public identity, and a subscription can hold
// several implicit registration sets. Here every subscription is one
// implicit registration set, so all of its public identities share one
// state.
type Subscription struct {
	PrivateIdentity  string
	PublicIdentities []string
	// BarredIdentities are those of PublicIdentities that may register
	// and do nothing else (TS 29.228 B.2.1, TS 23.228 5.2.1 item 7). For
	// a UE without an ISIM that is the temporary public identity derived
	// from the IMSI, which only the CSCFs and the HSS are to see (TS
	// 23.228 E.3.1). The default public identity is the first of
	// PublicIdentities that is not barred (TS 29.228 6.5.1.1).
	BarredIdentities []string
	// Password is the SIP Digest password. The HSS only hands out H(A1)
	// derived from it (TS 29.228 Table 6.3.7).
	Password string
	// AKA is set for a subscription that can use IMS-AKA. A
	// subscription has a password, AKA credentials or both; which one a
	// registration uses is the S-CSCF's choice in the MAR (TS 29.228
	// 6.3.1 step 4).
	AKA *AKACredentials
	// RepositoryData is the repository data that the operator provisions
	// with the subscription, such as MMTelServicesData and
	// UtCredentialData. Store.Add stores each with Sequence Number 0;
	// from then on Sh changes it (Store.RepositoryData), and a
	// Subscriber that a Store returns has none here.
	RepositoryData []sh.TransparentData
}

// AKACredentials are what the AuC part of the HSS keeps for IMS-AKA
// (TS 33.203 6.1.1, TS 33.102 6.3.2).
type AKACredentials struct {
	// Secret is K and OPc, shared with the ISIM.
	aka.Secret
	// AMF goes into the AUTN of every vector.
	AMF aka.AMF
	// SQN is SQN_HE, the last sequence number the HSS used. After Add,
	// only Store.CompareAndSwapSQN changes it: Put keeps the stored one,
	// so that writing a registration state never takes it back.
	SQN aka.SQN
}

// Validate checks the rules that every subscription follows, whether it
// comes from the configuration or from the provisioning API. The error
// names the field as the configuration and the API spell it. That no
// identity is in two subscriptions is the Store's to check.
func (s Subscription) Validate() error {
	switch {
	case s.PrivateIdentity == "":
		return errors.New("private_identity: required")
	case len(s.PublicIdentities) == 0:
		return errors.New("public_identities: at least one is required")
	case s.Password == "" && s.AKA == nil:
		return errors.New("password or aka: at least one is required")
	}
	indications := map[string]bool{}
	for _, d := range s.RepositoryData {
		switch {
		case d.ServiceIndication == "" || d.ServiceData == nil:
			return errors.New("repository data: a Service-Indication and its data are required")
		case indications[d.ServiceIndication]:
			return fmt.Errorf("repository data: %s appears twice", d.ServiceIndication)
		}
		indications[d.ServiceIndication] = true
	}
	seen := map[string]bool{}
	for _, id := range s.PublicIdentities {
		if !strings.HasPrefix(id, "sip:") && !strings.HasPrefix(id, "tel:") {
			return fmt.Errorf("public_identities: %q is not a SIP or tel URI", id)
		}
		if seen[publicKey(id)] {
			return fmt.Errorf("public_identities: %q appears twice", id)
		}
		seen[publicKey(id)] = true
	}
	for _, id := range s.BarredIdentities {
		if !seen[publicKey(id)] {
			return fmt.Errorf("barred_identities: %q is not one of public_identities", id)
		}
	}
	return nil
}

// Barred reports whether id is one of the barred public identities of
// the subscription.
func (s Subscription) Barred(id string) bool {
	return slices.ContainsFunc(s.BarredIdentities, func(b string) bool { return publicKey(b) == publicKey(id) })
}

// ParseAKACredentials reads AKA credentials as the configuration file
// and the provisioning API write them, in hexadecimal like the test
// data of TS 35.208: k and opc of 16 octets, amf of 2 and sqn of 6. An
// empty amf is all zeros, which leaves the separation bit for EPS and
// the bits reserved by TS 33.102 Annex H at 0. An empty sqn is 0, the
// start condition of TS 33.102 C.3.2.
func ParseAKACredentials(k, opc, amf, sqn string) (*AKACredentials, error) {
	var c AKACredentials
	var sqnOctets [8]byte // SQN is 6 octets; the first 2 stay zero
	fields := []struct {
		name, value string
		dst         []byte
		optional    bool
	}{
		{"k", k, c.K[:], false},
		{"opc", opc, c.OPc[:], false},
		{"amf", amf, c.AMF[:], true},
		{"sqn", sqn, sqnOctets[2:], true},
	}
	for _, f := range fields {
		if f.value == "" && f.optional {
			continue
		}
		b, err := hex.DecodeString(f.value)
		if err != nil || len(b) != len(f.dst) {
			return nil, fmt.Errorf("aka.%s: want %d octets in hexadecimal", f.name, len(f.dst))
		}
		copy(f.dst, b)
	}
	c.SQN = aka.SQN(binary.BigEndian.Uint64(sqnOctets[:]))
	return &c, nil
}

// RegistrationState is the state of TS 29.228 6.1.1.1.
type RegistrationState int

const (
	NotRegistered RegistrationState = iota
	Registered
	// Unregistered means an S-CSCF holds the profile for services to an
	// identity that is not registered.
	Unregistered
)

func (s RegistrationState) String() string {
	switch s {
	case Registered:
		return "Registered"
	case Unregistered:
		return "Unregistered"
	}
	return "Not Registered"
}

// Subscriber is what the HSS keeps for one subscription: its data and
// its registration state.
type Subscriber struct {
	Subscription
	State RegistrationState
	// ServerName is the S-CSCF assigned, if any.
	ServerName string
	// SCSCF is the Diameter identity of the S-CSCF that last stored
	// ServerName: the Origin-Host and Origin-Realm of its MAR or SAR.
	// A Registration-Termination-Request goes there (TS 29.228 Table
	// 6.1.3.1, Routing Information).
	SCSCF diameter.Identity
	// AuthenticationPending is set by a MAR and cleared by the SAR that
	// ends the authentication (TS 29.228 6.3.1 and 6.1.2.1).
	AuthenticationPending bool
	// IPSMGWNumber is the IP-SM-GW that an Sh PUR registered for the
	// user's short messages, or "" (TS 29.328 6.1.2.1 step 4b).
	IPSMGWNumber string
}

// ErrIdentityTaken means that an identity of a new subscription already
// belongs to another one.
var ErrIdentityTaken = errors.New("identity already belongs to a subscription")

// Store keeps the subscribers of the HSS. It is an interface so that the
// data can live outside the node; MemoryStore keeps it in the
// process. Every lookup that a Cx request makes is by a key, the private
// or a public identity, so that no request walks all subscribers.
type Store interface {
	Subscriber(privateID string) (Subscriber, bool)
	// SubscriberOf finds the subscriber one of whose public
	// identities is publicID.
	SubscriberOf(publicID string) (Subscriber, bool)
	// Add stores a new subscription, Not Registered, with its
	// repository data. It fails with ErrIdentityTaken if one of its
	// identities is in another subscription.
	Add(Subscription) error
	// Put writes back the registration state of a subscriber that is
	// stored. The caller holds the subscriber's lock. It does not
	// change SQN_HE.
	Put(Subscriber)
	// CompareAndSwapSQN sets SQN_HE of the subscriber to new if it is
	// still old, and reports whether it did. SQN_HE must never go back
	// or be used twice, even when several HSS instances share the
	// store and the lock of one instance does not hold the others back,
	// so it changes only by this one atomic step.
	CompareAndSwapSQN(privateID string, old, new aka.SQN) bool
	// RepositoryData returns the repository data of the subscription of
	// privateID under serviceIndication (TS 29.328 7.4). The data belongs
	// to the subscription, not to one of its public identities: they are
	// all aliases of each other (TS 29.328 6.1.2.1 step 6), as every
	// subscription of the stub is one implicit registration set with
	// one service profile.
	RepositoryData(privateID, serviceIndication string) (sh.TransparentData, bool)
	// CompareAndSwapRepositoryData stores data under its
	// ServiceIndication, or removes what is stored there when data has
	// no ServiceData, if what is stored is still old: nil for nothing, or
	// the data with old's SequenceNumber. It reports whether it did. As
	// for SQN_HE, this one atomic step keeps two HSS instances from both
	// taking an update from the same version.
	CompareAndSwapRepositoryData(privateID string, old *sh.TransparentData, data sh.TransparentData) bool
	Delete(privateID string) (Subscriber, bool)
	// List returns at most limit subscribers, in the order of their
	// private identities. It is for operators, never for a Cx request.
	List(limit int) []Subscriber
}

// MemoryStore is a Store in the process. It indexes the subscribers by
// private identity, and the private identities by public identity.
type MemoryStore struct {
	mu        sync.Mutex
	byPrivate map[string]Subscriber
	byPublic  map[string]string // public key to private key
	// repository holds the repository data by private key, then by
	// Service-Indication.
	repository map[string]map[string]sh.TransparentData
}

// NewMemoryStore returns a store that holds subs.
func NewMemoryStore(subs ...Subscription) (*MemoryStore, error) {
	s := &MemoryStore{byPrivate: map[string]Subscriber{}, byPublic: map[string]string{}, repository: map[string]map[string]sh.TransparentData{}}
	for _, sub := range subs {
		if err := s.Add(sub); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// privateKey is the key of a private identity, a NAI, which compares
// without case.
func privateKey(id string) string { return strings.ToLower(id) }

// publicKey is the key of a public identity, so that
// "tel:+1-202-555-0101" and the "tel:+12025550101" of an MSISDN are the
// same identity, and so is a SIP URI with its host in capitals.
func publicKey(id string) string { return ims.PublicIdentityKey(id) }

func (s *MemoryStore) Subscriber(privateID string) (Subscriber, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.byPrivate[privateKey(privateID)]
	return copied(sub), ok
}

// copied gives a caller its own AKACredentials, so that what it does
// with them never reaches the store.
func copied(sub Subscriber) Subscriber {
	sub.AKA = cloneAKA(sub.AKA)
	return sub
}

func cloneAKA(a *AKACredentials) *AKACredentials {
	if a == nil {
		return nil
	}
	c := *a
	return &c
}

func (s *MemoryStore) SubscriberOf(publicID string) (Subscriber, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.byPublic[publicKey(publicID)]
	if !ok {
		return Subscriber{}, false
	}
	sub, ok := s.byPrivate[k]
	return copied(sub), ok
}

func (s *MemoryStore) Add(sub Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(sub.PrivateIdentity)
	if _, taken := s.byPrivate[k]; taken {
		return fmt.Errorf("%w: %s", ErrIdentityTaken, sub.PrivateIdentity)
	}
	for _, id := range sub.PublicIdentities {
		if _, taken := s.byPublic[publicKey(id)]; taken {
			return fmt.Errorf("%w: %s", ErrIdentityTaken, id)
		}
	}
	sub.PublicIdentities = slices.Clone(sub.PublicIdentities)
	sub.BarredIdentities = slices.Clone(sub.BarredIdentities)
	sub.AKA = cloneAKA(sub.AKA)
	for _, d := range sub.RepositoryData {
		if s.repository[k] == nil {
			s.repository[k] = map[string]sh.TransparentData{}
		}
		d.SequenceNumber = 0 // new data (TS 29.328 6.1.2.1)
		s.repository[k][d.ServiceIndication] = cloneTransparentData(d)
	}
	sub.RepositoryData = nil
	s.byPrivate[k] = Subscriber{Subscription: sub}
	for _, id := range sub.PublicIdentities {
		s.byPublic[publicKey(id)] = k
	}
	return nil
}

func (s *MemoryStore) Put(sub Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(sub.PrivateIdentity)
	if stored, ok := s.byPrivate[k]; ok {
		sub.AKA = stored.AKA
		s.byPrivate[k] = sub
	}
}

func (s *MemoryStore) CompareAndSwapSQN(privateID string, old, new aka.SQN) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.byPrivate[privateKey(privateID)]
	if !ok || sub.AKA == nil || sub.AKA.SQN != old {
		return false
	}
	sub.AKA.SQN = new
	return true
}

func (s *MemoryStore) Delete(privateID string) (Subscriber, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(privateID)
	sub, ok := s.byPrivate[k]
	if !ok {
		return Subscriber{}, false
	}
	delete(s.byPrivate, k)
	for _, id := range sub.PublicIdentities {
		delete(s.byPublic, publicKey(id))
	}
	delete(s.repository, k)
	return sub, true
}

func (s *MemoryStore) RepositoryData(privateID, serviceIndication string) (sh.TransparentData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.repository[privateKey(privateID)][serviceIndication]
	return cloneTransparentData(d), ok
}

func (s *MemoryStore) CompareAndSwapRepositoryData(privateID string, old *sh.TransparentData, data sh.TransparentData) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(privateID)
	if _, ok := s.byPrivate[k]; !ok {
		return false
	}
	stored, ok := s.repository[k][data.ServiceIndication]
	if ok != (old != nil) || ok && stored.SequenceNumber != old.SequenceNumber {
		return false
	}
	if data.ServiceData == nil {
		delete(s.repository[k], data.ServiceIndication)
		return true
	}
	if s.repository[k] == nil {
		s.repository[k] = map[string]sh.TransparentData{}
	}
	s.repository[k][data.ServiceIndication] = cloneTransparentData(data)
	return true
}

// cloneTransparentData gives the store and its callers each their own
// ServiceData.
func cloneTransparentData(d sh.TransparentData) sh.TransparentData {
	if d.ServiceData != nil {
		d.ServiceData = &sh.ServiceData{XML: slices.Clone(d.ServiceData.XML)}
	}
	return d
}

func (s *MemoryStore) List(limit int) []Subscriber {
	s.mu.Lock()
	keys := make([]string, 0, len(s.byPrivate))
	for k := range s.byPrivate {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	subs := make([]Subscriber, 0, len(keys))
	for _, k := range keys {
		subs = append(subs, copied(s.byPrivate[k]))
	}
	s.mu.Unlock()
	return subs
}
