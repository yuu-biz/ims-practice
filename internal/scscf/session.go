package scscf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
)

// Resolver finds next hops (RFC 3263) and turns numbers into SIP URIs
// (ENUM, RFC 6116). *sipdns.Resolver is one.
type Resolver interface {
	ims.Locator
	ENUM(ctx context.Context, number, suffix string) (string, error)
}

// ownURI is the S-CSCF as it appears in Route and Record-Route.
func (s *SCSCF) ownURI() sip.URI {
	u, _ := ims.LooseRoute(fmt.Sprintf("sip:%s:%d", s.cfg.Host, s.cfg.Port))
	return u
}

// session handles every request but REGISTER. A SUBSCRIBE to the reg
// event package is for the S-CSCF itself (5.4.2.1.1); any other request
// it routes. It decides by the Route that brought the request whether it
// serves the caller or the callee (TS 24.229 5.4.3.1): the Service-Route
// it handed out at registration carries the user part "orig". A request
// that comes back from an application server carries an original dialog
// identifier instead, and the S-CSCF has kept which case it was.
func (s *SCSCF) session(st *sip.ServerTransaction) {
	if s.receiveRegEvent(st) {
		return
	}
	out := st.Request.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		s.respond(st, 483, nil)
		return
	}
	top, popped := sip.PopOwnRoute(out, s.ownURI())
	log := s.log.With("method", string(out.Method), "call_id", out.Get("Call-ID"))
	odi, returned := top.Params.Get(odiParam)
	if !ims.IsInDialog(st.Request) {
		s.chargeRequest(out)
	}
	switch {
	case ims.IsInDialog(st.Request):
		// A request within a dialog follows the route set the dialog
		// already has (RFC 3261 16.12).
		s.forward(st, out, nil, log)
	case popped && returned:
		s.fromApplicationServer(st, out, odi, log)
	case popped && top.User == "orig":
		s.originating(st, out, log)
	default:
		s.terminating(st, out, log)
	}
}

// originating is TS 24.229 5.4.3.2 for an initial request from a
// registered user that visits the S-CSCF for the first time: there is
// no original dialog identifier in its Route.
//
// SIMPLIFIED: TS 24.229 5.4.3.1 and 5.4.3.2. Only a registered user is
// served. A request that an application server sends on behalf of a
// user who is not registered gets 403, and the criteria of that state
// (ORIGINATING_UNREGISTERED) are not evaluated: no application server
// of this network originates a request on behalf of a user, so the
// state would serve no request here.
func (s *SCSCF) originating(st *sip.ServerTransaction, out *sip.Message, log *slog.Logger) {
	// Step 0 does not apply: the P-CSCF handles P-Served-User at the
	// edge of the trust domain, and removes one that came from the UE.
	// Step 1: P-Served-User names the served user when a node of the
	// trust domain put it there; otherwise P-Asserted-Identity does. A
	// barred identity, such as the temporary public identity of a UE
	// without an ISIM, may not originate a request: the P-CSCF asserts
	// the default public identity instead, so one that comes here came
	// from a node that does not follow the rules (NOTE 5, NOTE 6).
	served, err := ims.ServedUser(out)
	if err != nil {
		s.respond(st, 403, nil)
		return
	}
	reg, ok, err := s.store.RegistrationOf(context.Background(), served)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if !ok {
		log.Info("originating user is not registered here", "served_user", served)
		s.respond(st, 403, nil)
		return
	}
	if reg.Barred(served) {
		log.Info("originating user is a barred public identity", "served_user", served)
		s.respond(st, 403, nil)
		return
	}
	// Step 9 applies to a request without an original dialog identifier
	// only, so it is taken here, before any application server sees the
	// request.
	assertAlias(out, reg, s.cfg.HomeDomain)
	// Steps 4C and 4D.
	assertService(out)
	log.Info("originating", "served_user", served, "request_uri", out.RequestURI)
	// Steps 3 and 6: the ordered list of criteria, and the icid-value.
	s.invokeApplicationServers(st, out, OriginalDialog{
		ServedUser:  served,
		SessionCase: ifc.OriginatingRegistered,
		RegState:    sip.RegStateRegistered,
		Criteria:    ifc.Ordered(reg.InitialFilterCriteria),
		Invoked:     -1,
		ICID:        icidOf(out),
	}, log)
}

// routeOriginating is steps 10 to 15 of TS 24.229 5.4.3.2, once no
// application server is left to invoke: the request goes to where its
// Route or its Request-URI says.
func (s *SCSCF) routeOriginating(st *sip.ServerTransaction, out *sip.Message, log *slog.Logger) {
	// Step 10: a tel URI is translated with ENUM, and so is a SIP URI of
	// a global number with user=phone in the home domain, as the step
	// lets local policy choose. That is the form an AS gives the target
	// of a diversion (TS 24.604 4.5.2.6.2.2). When the translation fails
	// the call would leave the IMS through a BGCF, which this project
	// does not have, so the S-CSCF answers instead, as step 10
	// allows.
	if number, ok := s.globalNumber(out.RequestURI); ok {
		uri, err := s.resolver.ENUM(context.Background(), number, s.cfg.ENUMSuffix)
		if err != nil {
			log.Info("ENUM has no SIP URI for the number", "number", number, "error", err.Error())
			s.respond(st, 404, nil)
			return
		}
		log.Info("translated the number with ENUM", "number", number, "uri", uri)
		out.RequestURI = uri
	}
	// Steps 11 and 12: a destination in another network needs the
	// interconnect functions, and the home network hides its topology,
	// so the request goes through the IBCF, on top of Route.
	if ibcf, ok := s.exitIBCF(out); ok {
		log.Info("the destination is in another network, leaving through the IBCF", "request_uri", out.RequestURI, "ibcf", ibcf.String())
		out.Prepend("Route", sip.NameAddr{URI: ibcf.String()}.String())
	}
	// Step 13: an initial request for a dialog is record-routed.
	if ims.CreatesDialog(out.Method) {
		sip.AddRecordRoute(out, s.ownURI())
	}
	// Step 14A: P-Served-User stays only on the way to an application
	// server or a BGCF. This project has no BGCF, and the S-CSCF sends a
	// request to an application server only from invoke.
	out.Remove("P-Served-User")
	log.Info("routing the originating request", "request_uri", out.RequestURI)
	s.forward(st, out, s.chargeResponses(st.Request, true), log)
}

// assertAlias is TS 24.229 5.4.3.2 step 9: when P-Asserted-Identity
// holds only a SIP URI, the tel URI that is its alias goes in as a
// second value (a); when it holds only a tel URI, the SIP URI form of
// the number does (b). The HSS stub has no AliasInd feature, so every
// identity of a registration is an alias of the others: they are one
// implicit registration set with one service profile (TS 29.228 B.2.1).
// The S-CSCF knows the aliases of the served user's identities only, so
// a P-Asserted-Identity of someone else, as a request on behalf of the
// served user may have (RFC 5502 4.4), stays as it is.
func assertAlias(out *sip.Message, reg Registration, homeDomain string) {
	asserted := out.GetAll("P-Asserted-Identity")
	if len(asserted) != 1 {
		return
	}
	na, err := sip.ParseNameAddr(asserted[0])
	if err != nil {
		return
	}
	if !slices.ContainsFunc(reg.PublicIdentities, func(id string) bool { return ims.SamePublicIdentity(id, na.URI) }) {
		return
	}
	if _, ok := ims.TelNumber(na.URI); ok {
		canonical, err := ims.CanonicalURI(na.URI)
		if number, global := strings.CutPrefix(canonical, "tel:+"); err == nil && global {
			out.Add("P-Asserted-Identity", sip.NameAddr{DisplayName: na.DisplayName, URI: "sip:+" + number + "@" + homeDomain + ";user=phone"}.String())
		}
		return
	}
	for _, id := range reg.PublicIdentities {
		if _, ok := ims.TelNumber(id); ok {
			out.Add("P-Asserted-Identity", sip.NameAddr{URI: id}.String())
			return
		}
	}
}

// terminating is TS 24.229 5.4.3.3 for an initial request to a user that
// this S-CSCF serves, on its first visit: there is no original dialog
// identifier in its Route. The P-Asserted-Identity is left as it came:
// it may be a user of another S-CSCF, or an application server that
// sends the request as an originating UA (5.7.3), such as the IP-SM-GW
// delivering an SMS.
//
// A callee who has no registration here is served with the criteria of
// the unregistered state (TERMINATING_UNREGISTERED), from the profile
// that the S-CSCF takes from the HSS for it (unregistered.go).
func (s *SCSCF) terminating(st *sip.ServerTransaction, out *sip.Message, log *slog.Logger) {
	// Step 1: the served user is the one in the Request-URI.
	callee, err := ims.CanonicalURI(out.RequestURI)
	if err != nil {
		s.respond(st, 404, nil)
		return
	}
	// Steps 1, 2A and 6: the saved Request-URI, the ordered list of
	// criteria and the icid-value.
	d := OriginalDialog{ServedUser: callee, Invoked: -1, RequestURI: out.RequestURI, ICID: icidOf(out)}
	var barredIDs []string
	reg, ok, err := s.store.RegistrationOf(context.Background(), callee)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if ok {
		d.SessionCase, d.RegState = ifc.TerminatingRegistered, sip.RegStateRegistered
		d.Criteria, d.Aliases = ifc.Ordered(reg.InitialFilterCriteria), canonicalIdentities(reg.NonBarredIdentities())
		barredIDs = reg.BarredIdentities
	} else {
		p, ok := s.unregisteredProfile(st, callee, log)
		if !ok {
			return
		}
		d.SessionCase, d.RegState = ifc.TerminatingUnregistered, sip.RegStateUnregistered
		d.Criteria, d.Aliases = ifc.Ordered(p.InitialFilterCriteria), canonicalIdentities(nonBarred(p.PublicIdentities, p.BarredIdentities))
		barredIDs = p.BarredIdentities
	}
	// Step 1, for a registered and an unregistered callee alike: nobody
	// reaches a barred identity. 404 tells the caller as little as for
	// an identity that does not exist.
	if barred(barredIDs, callee) {
		log.Info("callee is a barred public identity", "callee", callee)
		s.respond(st, 404, nil)
		return
	}
	// Steps 3C and 3D.
	confirmService(out)
	log.Info("terminating", "served_user", callee, "session_case", d.SessionCase.String())
	s.invokeApplicationServers(st, out, d, log)
}

// canonicalIdentities are ids in the form of ims.CanonicalURI.
func canonicalIdentities(ids []string) []string {
	var canonical []string
	for _, id := range ids {
		if c, err := ims.CanonicalURI(id); err == nil {
			canonical = append(canonical, c)
		}
	}
	return canonical
}

// routeTerminating is steps 8 to 14 of TS 24.229 5.4.3.3, once no
// application server is left to invoke: the request goes to the contact
// that the user registered, along the Path the P-CSCF put there, if the
// contact meets the caller preferences of the request. When
// an application server has put Route values of its own, the request
// follows them instead (steps 8 and 14).
func (s *SCSCF) routeTerminating(st *sip.ServerTransaction, out *sip.Message, d OriginalDialog, log *slog.Logger) {
	if out.Get("Route") != "" {
		log.Info("routing the terminating request along its Route", "route", out.Get("Route"))
		s.forward(st, out, s.chargeResponses(st.Request, false), log)
		return
	}
	reg, ok, err := s.store.RegistrationOf(context.Background(), d.ServedUser)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if !ok {
		// Step 10: no contact to route to. The user may have gone while
		// an application server held the request.
		log.Info("callee has no registration", "callee", d.ServedUser)
		s.respond(st, 480, nil)
		return
	}
	// Step 9: the caller preferences may take the contact out of the
	// target set, which leaves no contact to route to (step 10 a, RFC
	// 3841 7.2.4).
	if !meetsCallerPreferences(out, reg.ContactParams) {
		log.Info("the contact does not meet the caller preferences", "callee", d.ServedUser,
			"accept_contact", out.GetAll("Accept-Contact"), "contact_params", reg.ContactParams.String())
		s.respond(st, 480, nil)
		return
	}
	// Step 13.
	if ims.CreatesDialog(out.Method) {
		sip.AddRecordRoute(out, s.ownURI())
	}
	out.Set("P-Called-Party-ID", "<"+out.RequestURI+">")
	// Step 13A: P-Served-User is for application servers, not for the UE.
	out.Remove("P-Served-User")
	withholdIdentity(out)
	out.RequestURI = reg.Contact
	for i := len(reg.Path) - 1; i >= 0; i-- {
		out.Prepend("Route", reg.Path[i])
	}
	log.Info("routing to the contact", "callee", d.ServedUser, "contact", reg.Contact)
	s.forward(st, out, s.chargeResponses(st.Request, false), log)
}

// forward sends out to its next hop: the top Route, or else the
// Request-URI (RFC 3261 16.6 steps 6 and 7). answer, when not nil, sees
// each response before it goes back.
func (s *SCSCF) forward(st *sip.ServerTransaction, out *sip.Message, answer func(*sip.Message), log *slog.Logger) {
	targets, err := s.nextHop(out)
	if err != nil {
		log.Warn("cannot route the request", "error", err.Error())
		s.respond(st, 404, nil)
		return
	}
	final, err := st.ProxyWith(out, answer, targets...)
	if final != nil && answer != nil {
		answer(final)
	}
	ims.Finish(st, final, err)
}

func (s *SCSCF) nextHop(m *sip.Message) ([]sip.Destination, error) {
	u, err := sip.NextHop(m)
	if err != nil {
		return nil, err
	}
	return ims.Targets(context.Background(), s.resolver, u)
}

// OnACK forwards the ACK for a 2xx along the dialog's route
// (RFC 3261 16.3 to 16.7 apply to it as to any request).
func (s *SCSCF) OnACK(ack *sip.Message) {
	forwardACK(&s.stack, s.ownURI(), s.resolver, ack, s.log)
}

func forwardACK(ref *ims.StackRef, own sip.URI, loc ims.Locator, ack *sip.Message, log *slog.Logger) {
	stack, err := ref.Get()
	if err != nil {
		return
	}
	out := ack.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		// RFC 3261 16.3 step 3 answers such a request 483, but no
		// response to an ACK is allowed (17), so it is only dropped.
		log.Info("dropped ACK", "call_id", ack.Get("Call-ID"), "error", err.Error())
		return
	}
	sip.PopOwnRoute(out, own)
	u, err := sip.NextHop(out)
	if err == nil {
		var targets []sip.Destination
		if targets, err = ims.Targets(context.Background(), loc, u); err == nil {
			err = stack.SendACK(out, targets...)
		}
	}
	if err != nil && !errors.Is(err, sip.ErrStackClosed) {
		log.Warn("cannot forward ACK", "call_id", ack.Get("Call-ID"), "error", err.Error())
	}
}

var _ Resolver = (*sipdns.Resolver)(nil)

// withholdIdentity is step 12 of TS 24.229 5.4.3.3: the UE is outside
// the trust domain (4.4.1), so the identity of a caller who asked for
// "id" privacy stays out of the request to it (RFC 3325 5, 9.3). The
// priv-value stays, so that the callee knows that the identity was
// withheld (4.4.2, NOTE 18). This is how OIR reaches the callee (TS
// 24.607 4.5.2.9 NOTE 2).
//
// SIMPLIFIED: TS 24.229 5.4.3.3 step 12. The privacy of History-Info
// entries (RFC 7044 10.1.2) is not applied: a diverted call carries
// the entries of the diverting users as the AS wrote them.
func withholdIdentity(out *sip.Message) {
	if sip.ParsePrivacy(out.Get("Privacy")).Has(sip.PrivacyID) {
		out.Remove("P-Asserted-Identity")
	}
}

// exitIBCF is the IBCF that out leaves the home network through, as a
// loose route (TS 24.229 5.4.3.2 steps 11 and 12): when the S-CSCF has
// an IBCF, no application server has put a Route of its own, and the
// Request-URI names a host outside the home domain. A request to an
// address, such as a softphone's contact, stays in the home network.
func (s *SCSCF) exitIBCF(out *sip.Message) (sip.URI, bool) {
	if s.cfg.IBCF.Host == "" || out.Get("Route") != "" {
		return sip.URI{}, false
	}
	u, err := sip.ParseURI(out.RequestURI)
	if err != nil {
		return sip.URI{}, false
	}
	if _, err := netip.ParseAddr(u.Host); err == nil {
		return sip.URI{}, false
	}
	host, home := strings.ToLower(strings.TrimSuffix(u.Host, ".")), strings.ToLower(s.cfg.HomeDomain)
	if host == home || strings.HasSuffix(host, "."+home) {
		return sip.URI{}, false
	}
	ibcf := s.cfg.IBCF
	ibcf.Params = slices.Clone(ibcf.Params)
	ibcf.Params.Set("lr", "")
	return ibcf, true
}

// globalNumber is the global number of uri when step 10 of TS 24.229
// 5.4.3.2 translates uri with ENUM: a tel URI, or a SIP URI of the home
// domain whose user part starts with + and that has user=phone.
func (s *SCSCF) globalNumber(uri string) (string, bool) {
	n, ok := ims.ParseTelephoneNumber(uri)
	if !ok {
		return "", false
	}
	if n.SIP == nil {
		return n.Number, true
	}
	// Only the home domain writes a number of its users in a SIP URI
	// this way; in another domain's URI the user part is that domain's
	// business.
	if !strings.EqualFold(n.SIP.Host, s.cfg.HomeDomain) || !strings.HasPrefix(n.Number, "+") {
		return "", false
	}
	return n.Number, true
}
