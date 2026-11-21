package icscf

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// session is TS 24.229 5.3.2.1 for an initial request that enters the
// home network: the I-CSCF asks the HSS where the callee is (LIR, TS
// 29.228 6.1.4), puts that S-CSCF on top of Route and forwards. A callee
// who is not registered but has services for that state comes with
// capabilities instead, and the I-CSCF picks an S-CSCF for them. A
// Request-URI in a subdomain of public service identities, such as a
// conference URI, goes to the application server that hosts it without
// a query (step 3). The I-CSCF does not record-route, so later requests
// of the dialog skip it.
//
// SIMPLIFIED: TS 24.229 5.3.2.1, 5.3.2.1A and 5.3.2.2. The "orig"
// routing for an application server that acts on behalf of a user, and
// the choice of another S-CSCF when the chosen one does not answer, are
// left out. No service of this project sends on behalf of a user, and
// this network has no redundant S-CSCF to choose.
//
// SIMPLIFIED: TS 24.229 5.3.2.1. The I-CSCF does not check whether a
// request comes from a trusted domain, so it never removes
// P-Charging-Vector and P-Charging-Function-Addresses for that reason.
// In the networks that this project sets up, every node that sends to
// the I-CSCF belongs to its own network: another network reaches it
// only through the IBCF, which removes both header fields from the
// requests of an untrusted peer as they enter (5.10.3.2), and a UE only
// through its P-CSCF. Nor does the I-CSCF store the
// icid-value of a request or the term-ioi of a response: they are for
// the charging records of the I-CSCF, which no node of this project
// writes, and the I-CSCF sees no later request of the dialog.
func (i *ICSCF) session(st *sip.ServerTransaction) {
	req := st.Request
	out := req.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		st.Respond(sip.NewResponse(req, 483, sip.NewTag()))
		return
	}
	log := i.log.With("method", string(req.Method), "call_id", req.Get("Call-ID"))
	// Step 1 b: a number in a SIP URI, as a peer network sends it (TS
	// 29.165 8.1), becomes a tel URI, which the HSS knows as an identity
	// of the user.
	if tel, ok := telURIOf(out.RequestURI); ok {
		log.Info("the Request-URI is a number, asking the HSS for its tel URI", "request_uri", out.RequestURI, "tel_uri", tel)
		out.RequestURI = tel
	}
	// Step 2: remove its own URI from the top Route, if it is there.
	sip.PopOwnRoute(out, i.ownURI())

	// Step 3: the subdomains of public service identities are in DNS,
	// which leads to the AS that hosts them (TS 23.228 5.4.12.3 b).
	if host, ok := i.psiSubdomain(out.RequestURI); ok {
		log.Info("the Request-URI is in a subdomain of public service identities, routing by DNS without the HSS",
			"request_uri", out.RequestURI, "subdomain", host)
		i.toPSIHost(st, out, log)
		return
	}

	callee, err := ims.CanonicalURI(out.RequestURI)
	if err != nil {
		st.Respond(sip.NewResponse(req, 404, sip.NewTag()))
		return
	}
	answer, err := i.hss.Request(context.Background(), cx.LocationInfoRequest{
		Header:         cx.NewHeader(i.node, "", i.cfg.HomeDomain),
		PublicIdentity: callee,
	}.Message())
	if err != nil {
		// 5.3.2.2 has no rule for an LIR that cannot complete; the
		// registration case (5.3.1.3) answers 480, which is used here too.
		log.Warn("LIR failed", "error", err.Error())
		st.Respond(sip.NewResponse(req, 480, sip.NewTag()))
		return
	}
	lia, err := cx.ParseLocationInfoAnswer(answer)
	switch {
	case err != nil:
		st.Respond(sip.NewResponse(req, 500, sip.NewTag()))
		return
	case lia.Result == cx.Experimental(cx.DiameterErrorIdentityNotRegistered):
		// 5.3.2.1: a known user who is not registered and has no services
		// for that state.
		log.Info("callee is not registered", "callee", callee)
		st.Respond(sip.NewResponse(req, 480, sip.NewTag()))
		return
	case !lia.Result.Success():
		// 5.3.2.1 and 5.3.2.2: the user does not exist, or the query
		// failed.
		log.Info("HSS does not know the callee", "callee", callee, "result", lia.Result.String())
		st.Respond(sip.NewResponse(req, 404, sip.NewTag()))
		return
	}
	// Step 2 of "upon successful user location query".
	withICID(out)
	server := lia.ServerName
	if server == "" {
		// "Upon successful user location query, when the response contains
		// information about the required S-CSCF capabilities": the
		// DIAMETER_UNREGISTERED_SERVICE of TS 29.228 6.1.4.1 step 3.
		server = i.choose(lia.ServerCapabilities)
		if server == "" {
			// 5.3.2.1 names no response for it. 480, as for a callee who
			// cannot be reached now.
			log.Warn("no S-CSCF has the capabilities", "callee", callee, "capabilities", lia.ServerCapabilities)
			st.Respond(sip.NewResponse(req, 480, sip.NewTag()))
			return
		}
		log.Info("selected S-CSCF for the services of an unregistered callee", "callee", callee, "server_name", server, "result", lia.Result.String())
	}
	final, err := i.toSCSCF(st, out, server, log)
	// 5.3.2.1: an S-CSCF that finds the callee served by another one
	// answers 305 with that S-CSCF in Contact (5.4.3.3), and the I-CSCF
	// sends the request there. 5.3.2.1 says so for an INVITE; any other
	// initial request is no different here.
	if err == nil && final != nil && final.StatusCode == 305 {
		if contact, perr := sip.ParseNameAddr(final.Get("Contact")); perr == nil {
			log.Info("the S-CSCF named the one that serves the callee", "server_name", contact.URI)
			final, err = i.toSCSCF(st, out, contact.URI, log)
		}
	}
	ims.Finish(st, final, err)
}

// toSCSCF puts server on top of Route and proxies out there (step 1 of
// "upon successful user location query"; every node here is a loose
// router, hence lr). It returns what is left to send, as Proxy does.
func (i *ICSCF) toSCSCF(st *sip.ServerTransaction, out *sip.Message, server string, log *slog.Logger) (*sip.Message, error) {
	route, err := ims.LooseRoute(server)
	if err != nil {
		log.Warn("Server-Name is not a SIP URI", "server_name", server)
		return sip.NewResponse(st.Request, 500, sip.NewTag()), nil
	}
	m := out.Clone()
	m.Prepend("Route", sip.NameAddr{URI: route.String()}.String())
	dst, err := ims.Destination(context.Background(), i.locator, route)
	if err != nil {
		log.Warn("cannot locate the S-CSCF", "server_name", server, "error", err.Error())
		return sip.NewResponse(st.Request, 504, sip.NewTag()), nil
	}
	log.Info("routing to the callee's S-CSCF", "server_name", server)
	return st.Proxy(m, dst)
}

// psiSubdomain is the subdomain of public service identities that the
// host of uri is, when it is one that the I-CSCF has configured.
func (i *ICSCF) psiSubdomain(uri string) (string, bool) {
	u, err := sip.ParseURI(uri)
	if err != nil {
		return "", false
	}
	host := strings.TrimSuffix(u.Host, ".")
	for _, d := range i.cfg.PSISubdomains {
		if strings.EqualFold(host, d) {
			return d, true
		}
	}
	return "", false
}

// toPSIHost forwards out to where DNS resolves its Request-URI: the AS
// that hosts the public service identity (TS 24.229 5.3.2.1, "in case
// the I-CSCF is able to resolve the Request-URI into the IP address of
// the AS hosting the PSI"). The request goes with an icid-value and this
// network as its orig-ioi (step 1). A response that brings a term-ioi
// goes back with the orig-ioi of the request and this network as its
// term-ioi, in place of the IOIs that the AS wrote.
func (i *ICSCF) toPSIHost(st *sip.ServerTransaction, out *sip.Message, log *slog.Logger) {
	u, err := sip.ParseURI(out.RequestURI)
	if err != nil {
		st.Respond(sip.NewResponse(st.Request, 404, sip.NewTag()))
		return
	}
	dst, err := ims.Destination(context.Background(), i.locator, u)
	if err != nil {
		// 5.3.2.1 names no response for it; the PSI has no host to
		// reach, as an unknown user has none.
		log.Warn("DNS has no AS for the public service identity", "request_uri", out.RequestURI, "error", err.Error())
		st.Respond(sip.NewResponse(st.Request, 404, sip.NewTag()))
		return
	}
	charging := withICID(out)
	charging.OrigIOI, charging.TermIOI = i.cfg.HomeDomain, ""
	out.Set("P-Charging-Vector", charging.String())
	received, _ := sip.ChargingVectorOf(st.Request)
	answer := sip.ChargingVector{ICID: charging.ICID, OrigIOI: received.OrigIOI}.Response(i.cfg.HomeDomain)
	relay := func(resp *sip.Message) {
		if got, _ := sip.ChargingVectorOf(resp); got.TermIOI != "" {
			resp.Set("P-Charging-Vector", answer.String())
		}
	}
	final, err := st.ProxyWith(out, dst, relay)
	if final != nil {
		relay(final)
	}
	ims.Finish(st, final, err)
}

// withICID keeps the icid-value that out came with, and gives a request
// without one a P-Charging-Vector with a new icid-value: TS 24.229
// 5.3.2.1, step 1 for the AS that hosts a PSI and step 2 of "upon
// successful user location query". It returns the charging vector of
// out.
func withICID(out *sip.Message) sip.ChargingVector {
	c, ok := sip.ChargingVectorOf(out)
	if !ok {
		c = sip.ChargingVector{ICID: sip.NewCallID()}
		out.Set("P-Charging-Vector", c.String())
	}
	return c
}

// telURIOf is the tel URI that TS 24.229 5.3.2.1 item 1 b puts in place
// of uri: a SIP URI that is not a GRUU (no "gr" parameter, RFC 5627),
// whose user part starts with "+", and whose "user" parameter is
// "phone". The user part becomes the telephone-subscriber of the tel
// URI with the parameters it carries (RFC 3261 19.1.6).
func telURIOf(uri string) (string, bool) {
	u, err := sip.ParseURI(uri)
	if err != nil || !strings.HasPrefix(u.User, "+") {
		return "", false
	}
	if user, ok := u.Params.Get("user"); !ok || !strings.EqualFold(user, "phone") {
		return "", false
	}
	if _, gruu := u.Params.Get("gr"); gruu {
		return "", false
	}
	return "tel:" + u.User, true
}

func (i *ICSCF) ownURI() sip.URI {
	u, _ := ims.LooseRoute(fmt.Sprintf("sip:%s:%d", i.cfg.Host, i.cfg.Port))
	return u
}
