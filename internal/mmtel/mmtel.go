// Package mmtel is the MMTel AS: the application server that gives the
// users of the IMS the supplementary services of multimedia telephony
// that GSMA IR.92 2.3 asks the network for (TS 24.173).
//
// The S-CSCF sends it every INVITE of a user by initial filter
// criteria: those the user makes (P-Served-User with sescase=orig),
// those to the user, registered or not (sescase=term), and those that
// the AS itself diverted away from the user (orig-cdiv). The AS acts as
// a routeing B2BUA (TS 24.229 5.7.5): it answers the INVITE as a UA,
// and sends a new INVITE back to the S-CSCF along the Route that holds
// the S-CSCF's original dialog identifier. It relays between the two
// legs what it does not change for a service.
//
// Each session case runs its services in a fixed order, written out in
// one function: originating, originatingAfterDiversion and
// terminating. The settings of the services come from the HSS over Sh
// for every initial INVITE (TS 29.364); the AS keeps no state of a user.
// What it keeps of a call while the call lasts goes into the
// Store.
package mmtel

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Config is how the AS names itself and the network.
type Config struct {
	// Host and Port are where the requests within the dialogs of the AS
	// reach it: the Contact of both legs of each call. Host is the name
	// of this instance, which DNS resolves to it.
	Host string
	Port int
	// HomeDomain is the domain of the home network, such as
	// ims.mnc001.mcc001.3gppnetwork.org. A diverted call to a tel URI
	// goes to a SIP URI in this domain (TS 24.604 4.5.2.6.2.2).
	HomeDomain string
	// HomeCountryCode is the E.164 country code of the home network,
	// digits only. Outgoing barring of international calls needs it
	// (TS 24.611 4.9.1.4).
	HomeCountryCode string
	// MRFC is the SIP URI of the MRFC, such as
	// sip:mrfc.ims.mnc001.mcc001.3gppnetwork.org, which plays the media
	// of the services: customized alerting tones (TS 24.182) and
	// announcements (TS 24.628), and mixes the conferences of which the
	// AS is the focus (TS 24.147 4). The AS reaches it straight over Mr'
	// (TS 24.229 10.2.3.2). Empty plays none, and the AS creates no
	// conference.
	MRFC string
	// ConferenceDomain is the subdomain of the home domain that holds the
	// conference URIs of this instance, such as
	// conf.ims.mnc001.mcc001.3gppnetwork.org. A conference URI is a
	// public service identity in it, which DNS resolves to this instance
	// (TS 23.228 5.4.12.3). Empty creates no conference.
	ConferenceDomain string
	// Announcement names the provisioned announcement that the caller
	// hears before the AS rejects a call for a service (TS 24.628
	// 4.2.4). Empty plays none: the call is rejected at once.
	Announcement string
	// AfterFunc starts a timer of the AS, such as the no reply timer of
	// CFNR or the expiry of a subscription to a conference: it calls f
	// in a goroutine of its own after d, unless stop is called first.
	// Nil means time.AfterFunc. A test passes its own, to fire the
	// timer without waiting for it.
	AfterFunc func(d time.Duration, f func()) (stop func() bool)
	// Timers are those of the UA; the zero value means
	// sip.DefaultTimers.
	Timers sip.Timers
}

// HSS is the part of Sh that the AS uses. *sh.Client is one.
type HSS interface {
	RepositoryData(ctx context.Context, publicID, serviceIndication string) (data sh.TransparentData, found bool, err error)
}

// AS is the mmtel role.
type AS struct {
	cfg   Config
	hss   HSS
	store Store
	// conferenceStore keeps the records of the conferences.
	conferenceStore ConferenceStore
	locator         ims.Locator
	log             *slog.Logger
	// ua is set by Listen before any call can arrive, and read by the
	// goroutines of the calls.
	ua atomic.Pointer[sip.UA]
	// conferences are the conferences that this instance hosts, by
	// their ID (conference.go).
	conferences sync.Map
	// managed is the administrative state that the maintenance API of
	// the node keeps for the AS, and users counts the work that a shut
	// down waits for: the calls it is serving and the requests of Ut it
	// is answering (maintenance.go).
	managed atomic.Pointer[maintenance.ManagedFunction]
	users   atomic.Int64
}

// New returns the AS. store keeps its calls and conferences its
// conferences; locator finds the S-CSCF of each Route.
func New(cfg Config, hss HSS, store Store, conferences ConferenceStore, locator ims.Locator, log *slog.Logger) *AS {
	if cfg.AfterFunc == nil {
		cfg.AfterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	return &AS{cfg: cfg, hss: hss, store: store, conferenceStore: conferences, locator: locator, log: log}
}

// uri is the SIP URI of this instance, which both legs of every call
// have as their Contact.
//
// The live handling of a call stays in the instance that took its
// INVITE: the Contact names that instance, and the dialogs, the
// transactions and the no reply timer live in its process. The Store
// keeps the call record outside it, which any instance reads to find
// the other leg of a dialog that a Replaces names (otherSide).
//
// SIMPLIFIED: RFC 3261 12.2.2. A UAS that has restarted may accept a
// request for a dialog it does not know, and carry the dialog on from
// state it kept. An instance of the AS that is created again under the
// same name does not: it builds no dialog from the call records, so
// each request within a call of the earlier process gets 481
// (Call/Transaction Does Not Exist), and the parties are left to end
// the call. A node that is told to stop closes the UA, which ends its
// calls without a BYE, and the goroutine of each call then removes the
// record of the call, but the node does not wait for those goroutines
// (sip.UA.Close leaves them to the user). The record of a call whose
// instance is killed, or whose process exits before that goroutine has
// removed it, stays in the Store with no time to live, as that of a
// call whose BYE no instance sees. Carrying a call on needs the UA to
// take a dialog that it did not set up, with CSeq numbers above any
// that the earlier process sent, and the relaying of each leg's
// requests set up again around it.
func (as *AS) uri() string { return "sip:" + as.cfg.Host + ":" + strconv.Itoa(as.cfg.Port) }

// Listen starts the UA of the AS on transport: each INVITE outside a
// dialog becomes a call of the AS, or joins the conference whose URI it
// is addressed to (conference.go); an INVITE that would start something
// new is refused while the operator has the AS out of service
// (maintenance.go). A SUBSCRIBE outside a call goes to
// the conference it names (conferenceevent.go); the UA answers any
// other request outside a dialog by itself: OPTIONS with 200, anything
// else with 405 (Method Not Allowed). The filter criteria of the AS
// match INVITE only (TS 24.604 Annex B); a request to a conference URI
// comes by DNS, which leads the subdomain of the URI to the AS.
func (as *AS) Listen(transport *sip.TransportLayer) error {
	ua, err := sip.NewUA(transport, sip.UAConfig{
		SentBy:  as.cfg.Host + ":" + strconv.Itoa(as.cfg.Port),
		Timers:  as.cfg.Timers,
		Contact: as.uri(),
		// The AS carries the SDP of preconditions from leg to leg
		// without reading it, so an INVITE that requires them is taken.
		// It names the other leg in a Replaces that it passes on (TS
		// 24.229 5.7.5.1), so it supports Replaces too (RFC 3891 6.2).
		Supported: []string{sip.OptionPrecondition, sip.OptionReplaces},
		// REFER goes from leg to leg like any other request within a
		// dialog (b2bua.Call).
		Allow:  []sip.Method{sip.REFER},
		OnCall: as.onCall,
		// The focus is the notifier of the conference event package (TS
		// 24.147 5.3.3).
		OnSubscribe: as.onSubscribe,
		Locate: func(u sip.URI) ([]sip.Destination, error) {
			return ims.Targets(context.Background(), as.locator, u)
		},
	}, as.log)
	if err != nil {
		return err
	}
	as.ua.Store(ua)
	return nil
}

// Close stops the UA. Calls in progress end without a BYE.
func (as *AS) Close() {
	if ua := as.ua.Load(); ua != nil {
		ua.Close()
	}
}

// errNotListening is why a call fails that arrives while Listen has not
// yet kept the UA it started.
var errNotListening = errors.New("mmtel: the AS is not listening yet")
