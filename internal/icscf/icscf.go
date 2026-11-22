// Package icscf is the I-CSCF role: the entry point of the home
// network. For each REGISTER it asks the HSS which S-CSCF serves the
// user, picks one when none does yet, and forwards the request there
// (TS 24.229 5.3.1).
package icscf

import (
	"context"
	"log/slog"
	"slices"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

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
}

type ICSCF struct {
	cfg     Config
	hss     HSS
	node    *diameter.Node
	locator Locator
	log     *slog.Logger
}

func New(cfg Config, hss HSS, node *diameter.Node, locator Locator, log *slog.Logger) *ICSCF {
	return &ICSCF{cfg: cfg, hss: hss, node: node, locator: locator, log: log}
}

// OnRequest is the sip.Config.OnRequest of the I-CSCF.
func (i *ICSCF) OnRequest(st *sip.ServerTransaction) {
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
		Header:                   cx.NewHeader(i.node, "", i.cfg.HomeDomain),
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
// SIMPLIFIED: TS 24.229 5.3.1.3. When the S-CSCF times out or answers
// 3xx or 480, the I-CSCF should pick another S-CSCF. This network has
// one S-CSCF, so its answer goes back as it is.
//
// SIMPLIFIED: RFC 3263 4.2 and 4.3. Only the first target that DNS
// gives for the S-CSCF is tried, never the next one after a failure.
// Every name in the DNS of this project leads to one server.
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
	final, err := st.Proxy(out, targets[0])
	if err != nil {
		// 5.3.1.3: an S-CSCF that does not answer, with no other to try,
		// gives 504.
		log.Warn("S-CSCF did not answer", "server_name", server, "error", err.Error())
		final = sip.NewResponse(req, 504, sip.NewTag())
	}
	st.Respond(final)
}

func errString(err error) string {
	if err == nil {
		return "no target"
	}
	return err.Error()
}
