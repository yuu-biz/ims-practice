// Package ipsmgw is the IP-SM-GW, the application server that carries
// short messages between UEs in the IMS and the SC (TS 23.204, TS
// 24.341 5.3.3). It does transport layer interworking: the RP message
// that a UE sends in the body of a SIP MESSAGE goes to the SC, and a
// TPDU from the SC goes to the UE in RP-DATA.
//
// The S-CSCF brings it two kinds of request by initial filter criteria:
// a third-party REGISTER for each registration of a user (5.3.3.2), and
// each MESSAGE with a short message body that a user sends (5.3.3.4.1).
// After a third-party REGISTER the IP-SM-GW subscribes to the user's
// registration state, and tells the HSS whether the user can take short
// messages (5.3.3.2). It sends MESSAGEs of its own to the users
// (5.3.3.4.2 to 5.3.3.4.4), into the home network through the I-CSCF.
//
// Every state that outlives one SIP transaction is in the Store, so that
// several IP-SM-GWs can serve one network behind one DNS name.
package ipsmgw

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// ContentType is the media type of a short message in the body of a
// MESSAGE (TS 24.341 5.3.1.2 item d).
const ContentType = "application/vnd.3gpp.sms"

// acceptContact asks the S-CSCF to deliver a MESSAGE only to a contact
// that registered the "+g.3gpp.smsip" feature tag (TS 24.341 5.3.2.2),
// as TS 24.341 5.3.3.4.2 to 5.3.3.4.4 item b want: the tag, with the
// "require" and "explicit" parameters of RFC 3841.
const acceptContact = "*;+g.3gpp.smsip;require;explicit"

// Config is how the IP-SM-GW names itself and the SC.
type Config struct {
	// URI is the SIP URI of the IP-SM-GW, such as
	// sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060. It is the
	// ServerName of the filter criteria that send requests here, and the
	// P-Asserted-Identity of every MESSAGE the IP-SM-GW sends (TS 24.341
	// 5.3.3.4.2 item d), where the UE takes the address of its delivery
	// report from (5.3.2.4 NOTE 1).
	URI string
	// HomeDomain leads, through DNS, to the I-CSCF: the entry point of
	// the home network, where the MESSAGEs of the IP-SM-GW go.
	HomeDomain string
	// Number is the E.164 number of the IP-SM-GW, digits only, which it
	// registers in the HSS for each user it serves (TS 29.328 7.6.16).
	Number string
	// Diameter is how the IP-SM-GW names itself on Diameter: the
	// Origin-Host and Origin-Realm of the answers it sends over SGd (TS
	// 29.338 6.3.2.6), and the identity that the HSS keeps from the
	// registration and that an SMS-GMSC sends a TFR to. The zero value
	// leaves them empty, which an IP-SM-GW whose node takes no SGd
	// request never needs.
	Diameter diameter.Identity
	// ServiceCentre is the address of the SC: the RP-Destination Address
	// that a short message from a UE must carry, and the
	// RP-Originator Address of the RP-DATA to a UE (TS 24.011 8.2.5.1,
	// 8.2.5.2).
	ServiceCentre sms.Address
	// ReportTimeout is how long the IP-SM-GW waits for the delivery
	// report of an RP-DATA. Zero means defaultReportTimeout.
	ReportTimeout time.Duration
	// SweepInterval is how often this instance claims the deliveries
	// whose TR1N is due, and so how late a delivery may be ended; zero
	// means ims.SweepInterval. Tests shorten it.
	SweepInterval time.Duration
	// ExpiryLease is how long a claimed delivery is this instance's
	// alone, after which another instance may claim it; zero means
	// ims.ExpiryLease. It is longer than ending one takes, the report to
	// the SC included. Tests shorten it.
	ExpiryLease time.Duration
	// IBCF is the IBCF of the home network, through which a short
	// message for a user of another IMS network leaves (deliver.go). The
	// zero value has no host: every recipient is in the home network.
	IBCF sip.URI
	// UserLocks serialises what happens to the reg event subscription
	// of one user. Nil means a lock per user in the memory of this
	// process (ims.UserLocks), which serves an IP-SM-GW that runs as one
	// instance. Instances that share a store take the lock of a user in
	// it (internal/kvstore/kvlock), so that only one of them handles the
	// user at a time.
	UserLocks ims.UserLocker
}

// HSS is the part of Sh that the IP-SM-GW uses. *sh.Client is one.
type HSS interface {
	// IMSI reads the IMSI of a user (TS 29.328 7.6.24), which a TFR
	// names the recipient of a short message by (TS 29.338 Table
	// 6.2.2.1/1). It is empty for a user the HSS has none for.
	IMSI(ctx context.Context, publicID string) (string, error)
	UpdateSMSRegistrationInfo(ctx context.Context, id sh.UserIdentity, ipsmgwNumber string) error
	// NotifyReadyForSM tells the HSS that the user can take short
	// messages again, for the reason given, so that it alerts the
	// Service Centres that hold short messages for the user (TS 23.204
	// 6.5b step 2d and 6.6 step 3). ipsmgwNumber is this IP-SM-GW's own
	// number, which the HSS compares with the registration it holds.
	NotifyReadyForSM(ctx context.Context, id sh.UserIdentity, ipsmgwNumber string, reason sh.ReadyForSM) error
}

// IPSMGW is the IP-SM-GW role.
type IPSMGW struct {
	cfg     Config
	sc      ServiceCentre
	store   Store
	hss     HSS
	locator ims.Locator
	stack   ims.StackRef
	// users serializes what happens to the reg event subscription of
	// one user, by its public identity. The Store does not lock across
	// nodes.
	users ims.UserLocker
	log   *slog.Logger
	// stopSweep ends the sweep of this instance and waits for the
	// handlers it started (deliver.go).
	stopSweep func()
	// managed is the administrative state that the maintenance API of
	// the node keeps for the IP-SM-GW, and inHand counts the work this
	// instance has taken on and not finished: the users of X.731 7.1.3
	// (maintenance.go).
	managed atomic.Pointer[maintenance.ManagedFunction]
	inHand  atomic.Int64
}

// New returns the IP-SM-GW and starts its sweep, which ends the
// deliveries whose TR1N has run out until Close. sc is where short
// messages from the UEs go; when it is the SC stub, the stub also needs
// the IP-SM-GW, to deliver through it.
func New(cfg Config, sc ServiceCentre, store Store, hss HSS, locator ims.Locator, log *slog.Logger) *IPSMGW {
	if cfg.ReportTimeout == 0 {
		cfg.ReportTimeout = defaultReportTimeout
	}
	if cfg.UserLocks == nil {
		cfg.UserLocks = &ims.UserLocks{}
	}
	if cfg.SweepInterval == 0 {
		cfg.SweepInterval = ims.SweepInterval
	}
	if cfg.ExpiryLease == 0 {
		cfg.ExpiryLease = ims.ExpiryLease
	}
	g := &IPSMGW{cfg: cfg, sc: sc, store: store, hss: hss, locator: locator, users: cfg.UserLocks, log: log}
	g.stopSweep = ims.Sweep[Expiry]{
		Kinds: []string{ReportTimeout}, Interval: cfg.SweepInterval, Lease: cfg.ExpiryLease,
		Claim:  g.claimDue,
		Handle: g.handleExpiry,
		Done:   store.Done,
		Log:    log,
	}.Start()
	return g
}

// Close stops the sweep of this instance and waits for the deliveries it
// was ending, whose report to the SC it ends. A delivery that this
// instance had claimed and not ended is ended by another instance of the
// IP-SM-GW once the claim runs out.
func (g *IPSMGW) Close() { g.stopSweep() }

// SetStack hands the IP-SM-GW the stack that calls OnRequest.
func (g *IPSMGW) SetStack(stack *sip.Stack) { g.stack.Set(stack) }

// OnRequest is the IP-SM-GW as a SIP UAS: it answers every request
// itself and forwards none (TS 24.229 5.7.2). Each request that the
// IP-SM-GW does take goes through take, which refuses it while the
// operator has the IP-SM-GW out of service and it would start something
// new (maintenance.go).
func (g *IPSMGW) OnRequest(st *sip.ServerTransaction) {
	switch st.Request.Method {
	case sip.REGISTER:
		g.take(st, g.register)
	case sip.MESSAGE:
		g.take(st, g.message)
	case sip.NOTIFY:
		g.take(st, g.receiveRegEvent)
	default:
		g.respond(st, 405, func(resp *sip.Message) { resp.Add("Allow", "REGISTER, MESSAGE, NOTIFY") })
	}
}

// respond answers st with code as TS 24.229 5.7.2 has an AS that acts
// as a terminating UA answer: the P-Charging-Vector of the response
// repeats the icid-value and the orig-ioi of the request and adds a
// type 3 term-ioi that names this network. fill, when not nil, adds
// what else the response needs.
func (g *IPSMGW) respond(st *sip.ServerTransaction, code int, fill func(*sip.Message)) {
	resp := sip.NewResponse(st.Request, code, sip.NewTag())
	if fill != nil {
		fill(resp)
	}
	if c, ok := sip.ChargingVectorOf(st.Request); ok {
		resp.Add("P-Charging-Vector", c.Response(g.cfg.HomeDomain).String())
	}
	st.Respond(resp)
}

// register is a third-party REGISTER from the S-CSCF (TS 24.341
// 5.3.3.2, TS 23.204 6.1 and 6.2): the IP-SM-GW answers 200, and for a
// registration or a reregistration keeps the MSISDN that the filter
// criterion's ServiceInfo carries and subscribes to the user's
// registration state (regevent.go). The NOTIFYs of that subscription,
// not the REGISTER, tell the HSS whether the user takes short messages:
// only they say whether a contact has "+g.3gpp.smsip". A deregistration
// needs nothing more, since the S-CSCF ends the subscription with a
// NOTIFY too (TS 24.229 5.4.2.1.2).
func (g *IPSMGW) register(st *sip.ServerTransaction) {
	req := st.Request
	log := g.log.With("method", "REGISTER", "call_id", req.Get("Call-ID"))
	publicID, err := ims.PublicIdentity(req.Get("To"))
	if err != nil {
		g.respond(st, 400, nil)
		return
	}
	n, _, err := sip.RequestedExpires(req)
	if err != nil || n < 0 {
		g.respond(st, 400, nil)
		return
	}
	g.respond(st, 200, func(resp *sip.Message) {
		if contact := req.Get("Contact"); contact != "" && n > 0 {
			// RFC 3261 10.3 step 8: the 200 lists the binding, as table
			// B.3-2 of TS 24.341 shows.
			resp.Add("Contact", contact+";expires="+strconv.Itoa(n))
		}
	})
	log = log.With("public_identity", publicID, "expires", n)
	if n == 0 {
		log.Info("third-party REGISTER of a deregistration")
		return
	}
	msisdn, err := ims.ParseServiceInfo(req.Body)
	if err != nil || !sh.ValidMSISDN(msisdn) {
		log.Warn("third-party REGISTER without an MSISDN in service-info")
		msisdn = ""
	}
	g.watchRegistration(publicID, msisdn, time.Duration(n)*time.Second, log.With("msisdn", msisdn))
}

// isShortMessage reports whether a MESSAGE carries a short message.
// Media types compare without case and may have parameters (RFC 2045
// 5.1).
func isShortMessage(req *sip.Message) bool {
	mediaType, _, _ := strings.Cut(req.Get("Content-Type"), ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), ContentType)
}

// newMessage starts a MESSAGE of the IP-SM-GW's own to a user, with the
// header fields that TS 24.341 5.3.3.4.2 to 5.3.3.4.4 have in common:
// the user in the Request-URI (item a), the Accept-Contact (item b),
// the Request-Disposition (item c), the IP-SM-GW in P-Asserted-Identity
// (item d of 5.3.3.4.2, item e of 5.3.3.4.3), and the body. The
// IP-SM-GW acts as an originating UA (TS 24.229 5.7.3).
//
// 5.3.3.4.4 does not list P-Asserted-Identity for the status report,
// but the UE answers a status report with a delivery report like any
// other RP-DATA (5.3.1.3), to the address it finds there (5.3.2.4
// NOTE 1).
func (g *IPSMGW) newMessage(to, disposition string, rp sms.RPMessage) (*sip.Message, error) {
	body, err := rp.Marshal()
	if err != nil {
		return nil, err
	}
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: to}
	m.Add("Max-Forwards", "70")
	m.Add("From", sip.NameAddr{URI: g.cfg.URI}.String()+";tag="+sip.NewTag())
	m.Add("To", sip.NameAddr{URI: to}.String())
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("P-Asserted-Identity", sip.NameAddr{URI: g.cfg.URI}.String())
	m.Add("P-Charging-Vector", sip.ChargingVector{ICID: sip.NewCallID(), OrigIOI: g.cfg.HomeDomain}.String())
	m.Add("Accept-Contact", acceptContact)
	m.Add("Request-Disposition", disposition)
	m.Add("Content-Type", ContentType)
	m.Body = body
	return m, nil
}

// entryPoint leads, through DNS, to the I-CSCF of the home network,
// where every request of the IP-SM-GW outside a dialog goes.
func (g *IPSMGW) entryPoint() sip.URI { return sip.URI{Scheme: "sip", Host: g.cfg.HomeDomain} }

// send sends a request of the IP-SM-GW to next and returns the final
// response. With no final response, the error says why. A request
// outside a dialog gets a Route to next: an AS that sends a request to a
// user puts the S-CSCF of the user or the entry point of the user's
// network in Route, without "orig" (TS 24.229 5.7.3). A request within a
// dialog has the route set of the dialog in Route already (RFC 3261
// 12.2.1.1).
//
// SIMPLIFIED: TS 24.229 5.7.1.1 and 5.7.3. The IP-SM-GW stores neither
// the icid-value and the orig-ioi of a third-party REGISTER nor the
// term-ioi of the responses to its own requests: they are for the
// charging records of the IP-SM-GW, which no node of this project
// writes. The vector of each response that it sends comes from the
// request that the response answers (respond).
func (g *IPSMGW) send(m *sip.Message, next sip.URI) (*sip.Message, error) {
	stack, err := g.stack.Get()
	if err != nil {
		return nil, err
	}
	if !ims.IsInDialog(m) {
		route, err := ims.LooseRoute(next.String())
		if err != nil {
			return nil, err
		}
		m.Prepend("Route", sip.NameAddr{URI: route.String()}.String())
	}
	targets, err := ims.Targets(context.Background(), g.locator, next)
	if err != nil {
		return nil, err
	}
	ct, err := stack.SendRequest(m, targets...)
	if err != nil {
		return nil, err
	}
	var final *sip.Message
	for r := range ct.Responses() {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	if final == nil {
		if err := ct.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("ipsmgw: no final response")
	}
	return final, nil
}
