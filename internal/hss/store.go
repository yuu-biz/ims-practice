package hss

import (
	"context"
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
	"github.com/yuu-biz/ims-practice/internal/sgd"
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
	// TestUser marks a subscription that the operator keeps for trying
	// features out, as against one of an ordinary user. The HSS reads
	// the mark when it answers for this subscription, so marking one
	// user costs the same however many the network has.
	TestUser bool
	// ServiceProfile is the name of the service profile that this
	// subscription gets, one of Config.ServiceProfiles. It is empty for
	// a subscription that takes the profile of its kind of user instead
	// (HSS.serviceProfile).
	ServiceProfile string
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
	// IPSMGW is the Diameter identity of that IP-SM-GW: the Origin-Host
	// and Origin-Realm of the PUR that registered it, which the HSS
	// keeps only when the IP-SM-GW announced that S6c reaches it (TS
	// 29.328 6.1.2.1 step 4b). The Serving-Node of an SRA names the
	// IP-SM-GW by it (TS 29.338 Table 5.2.1.1-2), so an IP-SM-GW without
	// one cannot be given to an SMS-GMSC.
	IPSMGW diameter.Identity
	// MessageWaitingData are the E.164 numbers, digits only, of the
	// service centres that hold a short message for the user because a
	// delivery to it failed: the MWD of TS 23.040 3.2.6. The HSS alerts
	// each of them once the user can take short messages again (3.2.7).
	MessageWaitingData []string
	// UNRI is UE-Not-Reachable-for-IP: set while the MWD holds an entry
	// put there because a delivery through the IP-SM-GW found the user
	// absent (TS 23.040 3.2.6). While it is set the HSS names no serving
	// node, as TS 29.338 5.2.1.3 has it leave out a node that is marked
	// not reachable.
	UNRI bool
	// MCEF is the Mobile Station Memory Capacity Exceeded Flag: set
	// while a delivery failed because the memory of the UE was full (TS
	// 23.040 3.2.6).
	MCEF bool
}

// maxMessageWaitingData is how many service centre addresses the HSS
// keeps for one user. TS 23.040 3.2.6 leaves the size of the MWD to the
// HLR, and 3.2.8 has an SC whose address does not fit fall back on its
// own retries. This project runs one SC, so one entry is already
// enough; a few leave room for the networks of a lab that run their
// own.
const maxMessageWaitingData = 4

// DeliveryFailure is why a service centre address goes into the message
// waiting data of a user (TS 23.040 3.2.6 items 1a to 1e).
type DeliveryFailure int

const (
	// AbsentUser is a delivery that did not reach the user. It sets
	// UNRI, so that the registration of an IP-SM-GW alerts the SC.
	AbsentUser DeliveryFailure = iota
	// MemoryCapacityExceeded is a delivery that the UE refused for want
	// of memory. It sets MCEF and clears UNRI: the UE is reachable, so
	// only a notification of free memory helps.
	MemoryCapacityExceeded
)

func (f DeliveryFailure) String() string {
	if f == MemoryCapacityExceeded {
		return "UE memory capacity exceeded"
	}
	return "absent user"
}

// addMessageWaiting records that the service centre at scAddress holds a
// short message for the user, and sets the flags that cause asks for (TS
// 23.040 3.2.6 items 1a to 1e). It reports whether the address is in the
// data afterwards: a full MWD takes no more, and the SC is left with its
// own retries.
func (s *Subscriber) addMessageWaiting(scAddress string, cause DeliveryFailure) bool {
	switch cause {
	case MemoryCapacityExceeded:
		s.MCEF, s.UNRI = true, false
	default:
		s.UNRI = true
	}
	if slices.Contains(s.MessageWaitingData, scAddress) {
		return true
	}
	if len(s.MessageWaitingData) >= maxMessageWaitingData {
		return false
	}
	s.MessageWaitingData = append(s.MessageWaitingData, scAddress)
	return true
}

// shortMessageDelivered records that a short message from the service
// centre at scAddress reached the user. The address leaves the message
// waiting data, and the flags go with it: a UE that took a short message
// is reachable and has memory. It reports whether service centres are
// left to alert. TS 23.040 3.2.6 items 2g and 2h alert them when the
// memory of the UE had been full; one that waits because the UE was not
// reachable is alerted as well, since the UE has shown that it is, as
// item 2c does for a UE that the IP-SM-GW reports reachable.
//
// 3.2.6 has no item for a delivery through an IP-SM-GW, so the HSS
// follows those, which are the same procedure through an MSC and
// through an SGSN.
func (s *Subscriber) shortMessageDelivered(scAddress string) bool {
	s.removeMessageWaiting(scAddress)
	s.MCEF, s.UNRI = false, false
	return len(s.MessageWaitingData) > 0
}

// readyForShortMessages records a notification from the IP-SM-GW that
// the user can take short messages again, and reports whether the
// service centres in the message waiting data are to be alerted (TS
// 23.040 3.2.6 items 2c and 2f).
//
// A notification alerts whenever service centres still wait, even when
// its flag is clear already: an alert that a service centre refused, or
// that never reached it, leaves its address in the message waiting
// data, and only a later notification can alert it again. Where no
// service centre waits, the HSS answers and alerts nobody (item 2i).
func (s *Subscriber) readyForShortMessages(reason sgd.AlertReason) bool {
	switch reason {
	case sgd.AlertReasonUEPresent:
		// Item 2c: the UE is reachable over IP again, so UNRI goes. While
		// MCEF is set the service centres wait for free memory instead.
		s.UNRI = false
		return len(s.MessageWaitingData) > 0 && !s.MCEF
	case sgd.AlertReasonUEMemoryAvailable:
		// Item 2f alerts the service centres when the UE has memory
		// again, and clears UNRI, since a UE that says so is reachable.
		// MCEF goes too: TS 23.040 3.2.8 Case 1 has the HLR clear it on
		// the MS Memory Capacity Available message, together with UNRI
		// for a UE reachable via the IP-SM-GW.
		s.MCEF, s.UNRI = false, false
		return len(s.MessageWaitingData) > 0
	}
	return false
}

// removeMessageWaiting takes the address of a service centre out of the
// message waiting data, and reports whether it was there.
func (s *Subscriber) removeMessageWaiting(scAddress string) bool {
	i := slices.Index(s.MessageWaitingData, scAddress)
	if i < 0 {
		return false
	}
	s.MessageWaitingData = slices.Delete(s.MessageWaitingData, i, i+1)
	return true
}

// ErrIdentityTaken means that an identity of a new subscription already
// belongs to another one.
//
// SIMPLIFIED: TS 23.228 4.3.3.4. A subscription has one private
// identity, and a public identity belongs to one subscription, so no
// public identity is shared by several private identities and
// registered from several UEs at once. Each subscriber of this project
// has one UE, and a shared identity would need the S-CSCF to fork the
// requests to it.
var ErrIdentityTaken = errors.New("identity already belongs to a subscription")

// Store keeps the subscribers of the HSS. It is an interface so that the
// data can live outside the node; MemoryStore keeps it in the
// process. Every lookup that a Cx request makes is by a key, the private
// or a public identity, so that no request walks all subscribers.
//
// An error is a failure of the store, after which the caller cannot
// tell what the store holds: the HSS answers the request with
// DIAMETER_UNABLE_TO_COMPLY, and the provisioning API with 500.
type Store interface {
	// Subscriber returns the subscriber of privateID, and whether there
	// is one.
	Subscriber(ctx context.Context, privateID string) (Subscriber, bool, error)
	// SubscriberOf finds the subscriber one of whose public
	// identities is publicID.
	SubscriberOf(ctx context.Context, publicID string) (Subscriber, bool, error)
	// Add stores a new subscription, Not Registered, with its
	// repository data. It fails with ErrIdentityTaken if one of its
	// identities is in another subscription. The caller holds the
	// subscriber's lock, as for Delete: in a store that several
	// instances share, a Delete takes the subscriber away before its
	// public identities and its entry in List, and an Add of the same
	// private identity in between would lose those to the rest of that
	// Delete.
	Add(ctx context.Context, sub Subscription) error
	// Put writes back the registration state of a subscriber that is
	// stored, and writes nothing for one that is not any more. The
	// caller holds the subscriber's lock. It does not change SQN_HE.
	Put(ctx context.Context, sub Subscriber) error
	// Replace writes the whole of a subscriber that is stored, its
	// credentials and public identities included, and writes nothing for
	// one that is not any more. The caller holds the subscriber's lock,
	// having read what it replaces. The private identity is the key, so
	// a Replace never moves a subscriber to another one; only how it is
	// spelled changes.
	//
	// It keeps the stored SQN_HE, as Put does: that changes by
	// CompareAndSwapSQN alone. It leaves the repository data alone as
	// well, which CompareAndSwapRepositoryData changes, so that an
	// update over Sh is never overwritten unseen.
	//
	// It fails with ErrIdentityTaken, leaving the subscription as it
	// was, if one of the public identities belongs to another
	// subscription. The identities that the subscription no longer holds
	// stop finding it.
	Replace(ctx context.Context, sub Subscriber) error
	// CompareAndSwapSQN sets SQN_HE of the subscriber to new if it is
	// still old, and reports whether it did. SQN_HE must never go back
	// or be used twice, even when several HSS instances share the
	// store and the lock of one instance does not hold the others back,
	// so it changes only by this one atomic step.
	CompareAndSwapSQN(ctx context.Context, privateID string, old, new aka.SQN) (bool, error)
	// RepositoryData returns the repository data of the subscription of
	// privateID under serviceIndication (TS 29.328 7.4), and whether
	// there is any. The data belongs to the subscription, not to one of
	// its public identities: they are all aliases of each other (TS
	// 29.328 6.1.2.1 step 6), as every subscription of the stub is one
	// implicit registration set with one service profile.
	RepositoryData(ctx context.Context, privateID, serviceIndication string) (sh.TransparentData, bool, error)
	// AllRepositoryData returns the repository data of the subscription
	// of privateID under every Service-Indication it has, in the order
	// the indications sort in, and none for a subscription that is not
	// stored. It is for the provisioning API, which replaces the data of
	// a subscription and writes it out; an Sh request names the
	// Service-Indication it is after (RepositoryData). It reads what one
	// subscription holds, so it costs the same however many
	// subscriptions the store holds.
	AllRepositoryData(ctx context.Context, privateID string) ([]sh.TransparentData, error)
	// CompareAndSwapRepositoryData stores data under its
	// ServiceIndication, or removes what is stored there when data has
	// no ServiceData, if what is stored is still old: nil for nothing, or
	// the data with old's SequenceNumber. It reports whether it did. As
	// for SQN_HE, this one atomic step keeps two HSS instances from both
	// taking an update from the same version.
	CompareAndSwapRepositoryData(ctx context.Context, privateID string, old *sh.TransparentData, data sh.TransparentData) (bool, error)
	// Delete removes the subscriber of privateID, and returns it and
	// whether there was one. The caller holds the subscriber's lock.
	Delete(ctx context.Context, privateID string) (Subscriber, bool, error)
	// List returns one page of the subscribers, in the order of their
	// private identities: at most limit of them, starting after the
	// private identity after. An empty after starts at the first
	// subscriber.
	//
	// next is the cursor of the page that follows, which the caller
	// passes back as after. It is empty once the store has reached its
	// last subscriber, which a page that is not full shows; a full page
	// always has a next, even when nothing follows it, and the page
	// after it is then empty. A full page may hold fewer than limit
	// subscribers, since an Add or a Delete that another instance has
	// not finished leaves an entry with no subscriber behind it.
	//
	// It is for operators, never for a Cx request. Reading a page costs
	// the same however many subscribers the store holds, so a listing
	// of a large network is as many pages as the operator asks for and
	// no walk over the rest.
	List(ctx context.Context, after string, limit int) (subs []Subscriber, next string, err error)
	// ListServed returns one page of the private identities of the
	// subscribers whose user profile an S-CSCF has and whose service
	// profile name comes from source (ServedSource): at most limit of
	// them, after the private identity after, in the order the
	// identities sort in, with the next cursor of List. The empty source
	// names none of them.
	//
	// A change of the setting that names the profile of source gives
	// every one of those subscribers another user profile, which the HSS
	// pushes to its S-CSCF (TS 29.228 6.6.1, HSS.Settings). Reading them
	// by the page costs the same however many subscribers the store
	// holds, so a change costs one page per limit subscribers of that
	// source and no walk over the rest.
	//
	// Put, Replace and Delete keep what this reads: they are the writes
	// through which a subscriber's profile reaches an S-CSCF or stops
	// being held there, and through which its source changes, and the
	// caller of each holds the subscriber's lock. Add writes a
	// subscription that no S-CSCF has yet, which belongs under no
	// source. Each of those writes names the one source its subscriber
	// belongs under and takes the subscriber out of the others, so
	// writing it again puts the index right whatever an instance that
	// stopped halfway left behind. What such an instance can leave is a
	// subscriber under two sources, or one that no S-CSCF has any more:
	// the caller reads each subscriber it is given and leaves out the
	// ones that are gone, so an entry too many costs a push of the
	// profile that the S-CSCF has already.
	ListServed(ctx context.Context, source ProfileSource, after string, limit int) (privateIDs []string, next string, err error)
	// Settings returns the settings that the instances of the HSS on the
	// store share (StoredSettings), and whether any are stored.
	Settings(ctx context.Context) (StoredSettings, bool, error)
	// CompareAndSwapSettings stores next if the settings stored are
	// still old, or, for a nil old, if none are stored yet, and reports
	// whether it did. Every instance reads the settings and any of them
	// changes them, so they change by this one atomic step, and no
	// instance writes over a change it has not seen.
	CompareAndSwapSettings(ctx context.Context, old *StoredSettings, next StoredSettings) (bool, error)
	// Counts are the numbers of TS 32.409 4.2.1 that the store holds,
	// and whether it keeps them. A store that does not keep them
	// returns false, and the HSS on it serves no database usage
	// measurement: working the numbers out would mean walking every
	// subscriber, which no operation of a Store does.
	Counts(ctx context.Context) (Counts, bool, error)
}

// StoredSettings are the settings that an operator changes while the
// HSS runs (HSS.Settings), as the instances of the HSS on one Store
// share them, with the configuration generation of the change that put
// them in force. The first instance on a Store stores those of its
// configuration file, at generation 0; from then on the maintenance API
// changes them, and every instance on the Store answers with them, so
// that an instance made in place of another answers as the role does
// and not as its file says.
type StoredSettings struct {
	Generation             uint64
	Admission              Admission
	DefaultServiceProfile  string
	TestUserServiceProfile string
}

// Counts are the database usage measurements of TS 32.409 4.2.1: how
// many subscriptions and public user identities the store holds, and
// how many of them are registered. They are numbers, never a listing.
// A store that keeps them changes them as it stores and removes
// subscribers, so that reading them costs the same however many
// subscribers it holds.
type Counts struct {
	// Subscriptions is the number of IMS subscriptions stored (TS
	// 32.409 4.2.1.1.1, DBU.NbrImsSubscription).
	Subscriptions int
	// SIPPublicIdentities and TelPublicIdentities are the public user
	// identities stored, by the format of the URI (4.2.1.1.3,
	// DBU.NbrSipPubUserId, and 4.2.1.1.4, DBU.NbrTelPubUserId). Every
	// public identity of a Subscription is one or the other
	// (Subscription.Validate), barred ones included: a barred identity
	// is a public user identity that may only register (TS 23.228
	// 5.2.1 item 7).
	SIPPublicIdentities int
	TelPublicIdentities int
	// RegisteredPublicIdentities is the number of public user
	// identities whose registration state is registered (4.2.1.2.1,
	// DBU.NbrRegPubUserId). TS 32.409 splits it by the visited network
	// a registration came from; this one is not split.
	RegisteredPublicIdentities int
	// RegisteredPrivateIdentities is the number of private user
	// identities that have a registered public user identity
	// (4.2.1.2.3, DBU.NbrRegPriUsrId). Every public identity of a
	// subscription here shares one registration state, so it is the
	// number of registered subscriptions.
	RegisteredPrivateIdentities int
}

// MemoryStore is a Store in the process. It indexes the subscribers by
// private identity, and the private identities by public identity. It
// never fails.
type MemoryStore struct {
	mu        sync.Mutex
	byPrivate map[string]Subscriber
	byPublic  map[string]string // public key to private key
	// order holds the private keys in order, so that a page of List is
	// a binary search and a slice of it, as a range lookup in a store
	// outside the process is.
	order []string
	// served holds the private keys of the subscribers whose user
	// profile an S-CSCF has, in order, under the source of their
	// service profile name, so that a page of ListServed is a binary
	// search and a slice of it as a page of List is.
	served map[ProfileSource][]string
	// repository holds the repository data by private key, then by
	// Service-Indication.
	repository map[string]map[string]sh.TransparentData
	// counts are the numbers of TS 32.409 4.2.1, kept as Add, Put and
	// Delete change what is stored.
	counts Counts
	// settings are the settings of the instances on the store, or nil
	// before the first of them stores its own.
	settings *StoredSettings
}

// NewMemoryStore returns a store that holds subs.
func NewMemoryStore(subs ...Subscription) (*MemoryStore, error) {
	s := &MemoryStore{byPrivate: map[string]Subscriber{}, byPublic: map[string]string{}, repository: map[string]map[string]sh.TransparentData{},
		served: map[ProfileSource][]string{}}
	for _, sub := range subs {
		if err := s.Add(context.Background(), sub); err != nil {
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

func (s *MemoryStore) Subscriber(_ context.Context, privateID string) (Subscriber, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.byPrivate[privateKey(privateID)]
	return copied(sub), ok, nil
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

func (s *MemoryStore) SubscriberOf(_ context.Context, publicID string) (Subscriber, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.byPublic[publicKey(publicID)]
	if !ok {
		return Subscriber{}, false, nil
	}
	sub, ok := s.byPrivate[k]
	return copied(sub), ok, nil
}

func (s *MemoryStore) Add(_ context.Context, sub Subscription) error {
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
	// A new subscription is Not Registered, so only the provisioning
	// numbers change (TS 32.409 4.2.1.1).
	s.counts.Subscriptions++
	s.countIdentities(sub, 1)
	if i, found := slices.BinarySearch(s.order, k); !found {
		s.order = slices.Insert(s.order, i, k)
	}
	for _, id := range sub.PublicIdentities {
		s.byPublic[publicKey(id)] = k
	}
	return nil
}

// Put stores sub with the public identities it was added with: neither
// the index by public identity nor the numbers of the provisioned
// identities (Counts) follow a change of them.
func (s *MemoryStore) Put(_ context.Context, sub Subscriber) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(sub.PrivateIdentity)
	if stored, ok := s.byPrivate[k]; ok {
		sub.AKA = stored.AKA
		s.byPrivate[k] = sub
		s.countRegistered(stored, -1)
		s.countRegistered(sub, 1)
		s.indexServed(k, ServedSource(sub))
	}
	return nil
}

// indexServed makes the index of the served subscribers hold k under
// source and under no other, and under none at all for the empty
// source (ListServed).
func (s *MemoryStore) indexServed(k string, source ProfileSource) {
	for _, each := range ProfileSources {
		ids := s.served[each]
		i, found := slices.BinarySearch(ids, k)
		switch {
		case each == source && !found:
			s.served[each] = slices.Insert(ids, i, k)
		case each != source && found:
			s.served[each] = slices.Delete(ids, i, i+1)
		}
	}
}

// Replace writes the whole subscription back: unlike Put, the index by
// public identity and the numbers of the provisioned identities (Counts)
// follow a change of the public identities. All of it happens under the
// one lock of the store, so no reader sees the subscription between the
// two, and the order of the writes is nobody's business; a store whose
// items are written one at a time has an order to keep to, and keeps it
// (internal/kvstore/hssstore).
func (s *MemoryStore) Replace(_ context.Context, sub Subscriber) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(sub.PrivateIdentity)
	stored, ok := s.byPrivate[k]
	if !ok {
		return nil
	}
	for _, id := range sub.PublicIdentities {
		if owner, taken := s.byPublic[publicKey(id)]; taken && owner != k {
			return fmt.Errorf("%w: %s", ErrIdentityTaken, id)
		}
	}
	sub.PublicIdentities = slices.Clone(sub.PublicIdentities)
	sub.BarredIdentities = slices.Clone(sub.BarredIdentities)
	sub.MessageWaitingData = slices.Clone(sub.MessageWaitingData)
	sub.AKA = cloneAKA(sub.AKA)
	if sub.AKA != nil && stored.AKA != nil {
		sub.AKA.SQN = stored.AKA.SQN
	}
	sub.RepositoryData = nil // the repository data is not the subscriber's item
	for _, id := range sub.PublicIdentities {
		s.byPublic[publicKey(id)] = k
	}
	s.byPrivate[k] = sub
	for _, id := range stored.PublicIdentities {
		if !holdsPublic(sub.Subscription, publicKey(id)) {
			delete(s.byPublic, publicKey(id))
		}
	}
	s.countIdentities(stored.Subscription, -1)
	s.countRegistered(stored, -1)
	s.countIdentities(sub.Subscription, 1)
	s.countRegistered(sub, 1)
	s.indexServed(k, ServedSource(sub))
	return nil
}

// holdsPublic reports whether sub has the public identity of key pk.
func holdsPublic(sub Subscription, pk string) bool {
	return slices.ContainsFunc(sub.PublicIdentities, func(id string) bool { return publicKey(id) == pk })
}

// countIdentities adds sign, 1 or -1, for each public user identity of
// sub to the numbers of TS 32.409 4.2.1.1.3 and 4.2.1.1.4.
func (s *MemoryStore) countIdentities(sub Subscription, sign int) {
	for _, id := range sub.PublicIdentities {
		if strings.HasPrefix(id, "tel:") {
			s.counts.TelPublicIdentities += sign
		} else {
			s.counts.SIPPublicIdentities += sign
		}
	}
}

// countRegistered adds sign, 1 or -1, for sub to the numbers of TS
// 32.409 4.2.1.2.1 and 4.2.1.2.3 when sub is registered, and nothing
// otherwise. Its public identities are one implicit registration set,
// so they enter and leave the registered state together.
func (s *MemoryStore) countRegistered(sub Subscriber, sign int) {
	if sub.State != Registered {
		return
	}
	s.counts.RegisteredPublicIdentities += sign * len(sub.PublicIdentities)
	s.counts.RegisteredPrivateIdentities += sign
}

func (s *MemoryStore) Counts(context.Context) (Counts, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts, true, nil
}

func (s *MemoryStore) CompareAndSwapSQN(_ context.Context, privateID string, old, new aka.SQN) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.byPrivate[privateKey(privateID)]
	if !ok || sub.AKA == nil || sub.AKA.SQN != old {
		return false, nil
	}
	sub.AKA.SQN = new
	return true, nil
}

func (s *MemoryStore) Delete(_ context.Context, privateID string) (Subscriber, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(privateID)
	sub, ok := s.byPrivate[k]
	if !ok {
		return Subscriber{}, false, nil
	}
	delete(s.byPrivate, k)
	s.counts.Subscriptions--
	s.countIdentities(sub.Subscription, -1)
	s.countRegistered(sub, -1)
	if i, found := slices.BinarySearch(s.order, k); found {
		s.order = slices.Delete(s.order, i, i+1)
	}
	s.indexServed(k, "")
	for _, id := range sub.PublicIdentities {
		delete(s.byPublic, publicKey(id))
	}
	delete(s.repository, k)
	return sub, true, nil
}

func (s *MemoryStore) RepositoryData(_ context.Context, privateID, serviceIndication string) (sh.TransparentData, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.repository[privateKey(privateID)][serviceIndication]
	return cloneTransparentData(d), ok, nil
}

func (s *MemoryStore) AllRepositoryData(_ context.Context, privateID string) ([]sh.TransparentData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.repository[privateKey(privateID)]
	data := make([]sh.TransparentData, 0, len(stored))
	for _, d := range stored {
		data = append(data, cloneTransparentData(d))
	}
	slices.SortFunc(data, func(a, b sh.TransparentData) int { return strings.Compare(a.ServiceIndication, b.ServiceIndication) })
	return data, nil
}

func (s *MemoryStore) CompareAndSwapRepositoryData(_ context.Context, privateID string, old *sh.TransparentData, data sh.TransparentData) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := privateKey(privateID)
	if _, ok := s.byPrivate[k]; !ok {
		return false, nil
	}
	stored, ok := s.repository[k][data.ServiceIndication]
	if ok != (old != nil) || ok && stored.SequenceNumber != old.SequenceNumber {
		return false, nil
	}
	if data.ServiceData == nil {
		delete(s.repository[k], data.ServiceIndication)
		return true, nil
	}
	if s.repository[k] == nil {
		s.repository[k] = map[string]sh.TransparentData{}
	}
	s.repository[k][data.ServiceIndication] = cloneTransparentData(data)
	return true, nil
}

// cloneTransparentData gives the store and its callers each their own
// ServiceData.
func cloneTransparentData(d sh.TransparentData) sh.TransparentData {
	if d.ServiceData != nil {
		d.ServiceData = &sh.ServiceData{XML: slices.Clone(d.ServiceData.XML)}
	}
	return d
}

func (s *MemoryStore) List(_ context.Context, after string, limit int) ([]Subscriber, string, error) {
	if limit < 1 {
		return nil, "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, next := page(s.order, after, limit)
	subs := make([]Subscriber, 0, len(keys))
	for _, k := range keys {
		subs = append(subs, copied(s.byPrivate[k]))
	}
	return subs, next, nil
}

func (s *MemoryStore) ListServed(_ context.Context, source ProfileSource, after string, limit int) ([]string, string, error) {
	if limit < 1 {
		return nil, "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, next := page(s.served[source], after, limit)
	// The caller gets a slice of its own: the index behind this one
	// changes as subscribers are written.
	return slices.Clone(keys), next, nil
}

func (s *MemoryStore) Settings(context.Context) (StoredSettings, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings == nil {
		return StoredSettings{}, false, nil
	}
	return *s.settings, true, nil
}

func (s *MemoryStore) CompareAndSwapSettings(_ context.Context, old *StoredSettings, next StoredSettings) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (old == nil) != (s.settings == nil) || old != nil && *old != *s.settings {
		return false, nil
	}
	s.settings = &next
	return true, nil
}

// page is the part of the ordered private keys ids that one page of a
// listing holds, at most limit of them, and the cursor of the page that
// follows (Store.List).
func page(ids []string, after string, limit int) (keys []string, next string) {
	start := 0
	if after != "" {
		// after is not in the page, whether or not its subscriber is
		// still there: a page goes on from where the one before it ended.
		i, found := slices.BinarySearch(ids, privateKey(after))
		start = i
		if found {
			start++
		}
	}
	keys = ids[start:min(start+limit, len(ids))]
	if len(keys) == limit {
		next = keys[len(keys)-1]
	}
	return keys, next
}
