// Package icscf is the I-CSCF role: the entry point of the home
// network. For each REGISTER it asks the HSS which S-CSCF serves the
// user, picks one when none does yet, and forwards the request there
// (TS 24.229 5.3.1).
package icscf

import (
	"context"
	"log/slog"
	"slices"
	"sync/atomic"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// role is the name of the role in the configuration file: the value of
// the role label of what the I-CSCF counts.
const role = "icscf"

// HSS is how the I-CSCF reaches the HSS over Cx.
// *diameter.PeerGroup, the instances of the HSS, is one.
type HSS interface {
	Request(ctx context.Context, request *diam.Message) (*diam.Message, error)
}

// Locator finds where to send a request for a SIP URI (RFC 3263).
// *sipdns.Resolver is one.
type Locator interface {
	Locate(ctx context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error)
}

// SCSCF is an S-CSCF the I-CSCF may choose, with the capabilities it
// offers (TS 29.228 6.7).
type SCSCF struct {
	Name         string
	Capabilities []uint32
}

type Config struct {
	HomeDomain string
	SCSCFs     []SCSCF
	// Host and Port name the I-CSCF, so that it can find itself in Route.
	Host string
	Port int
	// PSISubdomains are the subdomains of public service identities, such
	// as conf.ims.mnc001.mcc001.3gppnetwork.org for the conference URIs:
	// DNS resolves each to the application server that hosts the
	// identities in it, so the I-CSCF does not ask the HSS for them (TS
	// 24.229 5.3.2.1 step 3, TS 23.228 5.4.12.3).
	PSISubdomains []string
	// Metrics is where the I-CSCF registers its counters, the session
	// measurements (ims.Sessions). Nil gives it a registry of its own,
	// which nothing serves.
	Metrics *metrics.Registry
}

type ICSCF struct {
	cfg     Config
	hss     HSS
	node    *diameter.Node
	locator Locator
	stack   ims.StackRef
	log     *slog.Logger

	// sessions counts the session establishments.
	sessions *ims.Sessions
	// transactions counts the requests that the I-CSCF is serving, the
	// users of its maintenance function (maintenance.go).
	transactions atomic.Int64
	// managed is the administrative state that the maintenance API of
	// the node keeps for the I-CSCF; nil without one.
	managed atomic.Pointer[maintenance.ManagedFunction]
}

// New returns an I-CSCF. It fails when cfg.Metrics holds the metrics of
// another I-CSCF already.
func New(cfg Config, hss HSS, node *diameter.Node, locator Locator, log *slog.Logger) (*ICSCF, error) {
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NewRegistry()
	}
	sessions, err := ims.NewSessions(cfg.Metrics)
	if err != nil {
		return nil, err
	}
	return &ICSCF{cfg: cfg, hss: hss, node: node, locator: locator, log: log, sessions: sessions}, nil
}

// SetStack gives the I-CSCF the stack it runs on, which it needs to
// forward CANCELs. Call it right after the stack is made.
func (i *ICSCF) SetStack(stack *sip.Stack) { i.stack.Set(stack) }

// OnRequest is the sip.Config.OnRequest of the I-CSCF.
//
// An initial INVITE that arrives here enters the home network from
// outside it, from the originating S-CSCF or, for a user of another
// network, from the IBCF: it is a terminating session establishment,
// which TS 32.409 4.1.2.11 counts on receipt.
//
// TS 32.409 gives the I-CSCF two further session measurements that it
// does not count here. The successful terminating session
// establishments (4.1.2.12) are the first 180 or 200 that the I-CSCF
// passes back; no panel of the dashboard reads them, as none reads the
// successes of the P-CSCF and the S-CSCF (4.1.2.2) either. The dropped
// sessions (4.1.2.8) are the sessions removed when they expire, and
// the I-CSCF has none to remove: it does not record-route, so it sees
// no request of a dialog after the first.
//
// An initial INVITE that the I-CSCF refuses while it is not UNLOCKED
// counts in no measurement. Its sender tries the next I-CSCF, which
// counts it, so the instances add up to the attempts of the network,
// as the refused REGISTERs of the P-CSCF and the S-CSCF do.
func (i *ICSCF) OnRequest(st *sip.ServerTransaction) {
	i.transactions.Add(1)
	defer i.transactionEnded()
	if !i.unlocked() {
		i.refuse(st)
		return
	}
	if st.Request.Method == sip.INVITE && !ims.IsInDialog(st.Request) {
		i.sessions.Started(role, ims.TerminatingSession)
	}
	if st.Request.Method != sip.REGISTER {
		i.session(st)
		return
	}
	i.register(st)
}

// register is TS 24.229 5.3.1.2, with the abnormal cases of 5.3.1.3.
func (i *ICSCF) register(st *sip.ServerTransaction) {
	req := st.Request
	publicID, err := ims.PublicIdentity(req.Get("To"))
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	privateID, _, err := ims.PrivateIdentity(req, publicID)
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	// TS 29.228 6.1.1: DE_REGISTRATION when the REGISTER asks for an
	// interval of zero.
	uat := cx.Registration
	if seconds, _, _ := sip.RequestedExpires(req); seconds == 0 {
		uat = cx.DeRegistration
	}
	log := i.log.With("private_identity", privateID, "public_identity", publicID, "call_id", req.Get("Call-ID"))

	answer, err := i.hss.Request(context.Background(), cx.UserAuthorizationRequest{
		Header:                   diameter.NewHeader(i.node, "", i.cfg.HomeDomain),
		UserName:                 privateID,
		PublicIdentity:           publicID,
		VisitedNetworkIdentifier: req.Get("P-Visited-Network-ID"),
		UserAuthorizationType:    uat,
	}.Message())
	if err != nil {
		// 5.3.1.3: the UAR could not complete.
		log.Warn("UAR failed", "error", err.Error())
		st.Respond(sip.NewResponse(req, 480, sip.NewTag()))
		return
	}
	uaa, err := cx.ParseUserAuthorizationAnswer(answer)
	if err != nil || !uaa.Result.Success() {
		// 5.3.1.3: a negative UAA gets 403.
		log.Info("HSS refused registration", "result", uaa.Result.String())
		st.Respond(sip.NewResponse(req, 403, sip.NewTag()))
		return
	}

	server := uaa.ServerName
	if server == "" {
		server = i.choose(uaa.ServerCapabilities)
		if server == "" {
			// 5.3.1.3: no S-CSCF has the mandatory capabilities.
			log.Warn("no S-CSCF has the capabilities", "capabilities", uaa.ServerCapabilities)
			st.Respond(sip.NewResponse(req, 600, sip.NewTag()))
			return
		}
	}
	log.Info("selected S-CSCF", "server_name", server, "result", uaa.Result.String())
	i.forward(st, server, log)
}

// choose picks the first configured S-CSCF that offers every mandatory
// capability, preferring the one with the most optional ones (TS 29.228
// 6.7). Server-Name AVPs inside Server-Capabilities steer the choice to
// those names.
func (i *ICSCF) choose(caps *cx.ServerCapabilities) string {
	if caps == nil {
		caps = &cx.ServerCapabilities{}
	}
	if len(caps.ServerNames) > 0 {
		return caps.ServerNames[0]
	}
	best, bestScore := "", -1
	for _, s := range i.cfg.SCSCFs {
		if !containsAll(s.Capabilities, caps.MandatoryCapabilities) {
			continue
		}
		score := 0
		for _, c := range caps.OptionalCapabilities {
			if slices.Contains(s.Capabilities, c) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = s.Name, score
		}
	}
	return best
}

func containsAll(have, want []uint32) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// forward sets the Request-URI to the S-CSCF and proxies the REGISTER
// there (TS 24.229 5.3.1.2).
//
// The REGISTER goes to every target that DNS gives for the S-CSCF in
// turn, until one does not fail (RFC 3263 4.3): the instances of an
// S-CSCF that runs as several share its name.
//
// SIMPLIFIED: TS 24.229 5.3.1.3. When the S-CSCF times out or answers
// 3xx or 480, the I-CSCF should pick another S-CSCF. The configuration
// of this network names one S-CSCF, so there is no other to pick, and
// its answer goes back through ims.Finish. When that S-CSCF runs as several
// instances, a REGISTER that one of them leaves unanswered already goes
// on to the next.
func (i *ICSCF) forward(st *sip.ServerTransaction, server string, log *slog.Logger) {
	req := st.Request
	u, err := sip.ParseURI(server)
	if err != nil {
		log.Warn("Server-Name is not a SIP URI", "server_name", server)
		st.Respond(sip.NewResponse(req, 500, sip.NewTag()))
		return
	}
	targets, err := i.locator.Locate(context.Background(), u.Host, u.Port, "")
	if err != nil || len(targets) == 0 {
		log.Warn("cannot locate the S-CSCF", "server_name", server, "error", errString(err))
		st.Respond(sip.NewResponse(req, 504, sip.NewTag()))
		return
	}
	out := req.Clone()
	out.RequestURI = server
	if err := sip.DecrementMaxForwards(out); err != nil {
		st.Respond(sip.NewResponse(req, 483, sip.NewTag()))
		return
	}
	final, err := st.Proxy(out, targets...)
	if err != nil {
		// 5.3.1.3: an S-CSCF that does not answer, with no other to try,
		// gives 504.
		log.Warn("S-CSCF did not answer", "server_name", server, "error", err.Error())
	}
	ims.Finish(st, final, err)
}

func errString(err error) string {
	if err == nil {
		return "no target"
	}
	return err.Error()
}
