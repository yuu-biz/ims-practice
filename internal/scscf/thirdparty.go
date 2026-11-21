package scscf

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the third-party registration of TS 24.229 5.4.1.7:
// after a registration, a reregistration or a deregistration, the
// S-CSCF tells each application server whose filter criterion matches
// the REGISTER, with a REGISTER of its own.

// notifyApplicationServers sends a third-party REGISTER to the server
// of each criterion of reg that matches req. req is the REGISTER of the
// UE, or, for a deregistration by the network, the REGISTER that would
// have had the same effect (TS 23.218 5.2.3). expires is what the 200
// (OK) gave the UE, or 0 for a deregistration.
//
// The REGISTERs go out on their own goroutines. The UE has its answer
// already, and the lock of the user is not held while the servers take
// their time to answer.
//
// SIMPLIFIED: TS 24.229 5.4.0. The S-CSCF looks the server up in DNS for
// every request, rather than keeping the address that the third-party
// REGISTER of the initial registration went to. Each application
// server role of this project keeps its state in a Store that all its
// nodes share, so any of them can take the next request.
func (s *SCSCF) notifyApplicationServers(req *sip.Message, reg Registration, expires time.Duration, log *slog.Logger) {
	for _, c := range ifc.Ordered(reg.InitialFilterCriteria) {
		// TS 23.218 5.2.1 NOTE 2: a REGISTER is part of the originating
		// case.
		if !c.Matches(req, ifc.OriginatingRegistered) {
			continue
		}
		m := s.thirdPartyRegister(req, reg, c.ApplicationServer, expires)
		go s.sendThirdPartyRegister(m, reg, c.ApplicationServer, expires, log)
	}
}

// thirdPartyRegister builds the REGISTER of TS 24.229 5.4.1.7 items a
// to h.
//
// SIMPLIFIED: TS 24.229 5.4.1.7 item c. The To is always the default
// public identity, which is never barred; the operator may choose
// another identity of the set that is not barred. Item i has nothing to
// copy: the HSS stub sends no charging addresses. Items j, l and m are
// for features this project does not have (P-User-Database,
// registration tokens, S-CSCF reselection).
//
// SIMPLIFIED: RFC 3261 10.2. Each third-party REGISTER has a Call-ID of
// its own, although a UAC should keep one Call-ID for its registrations
// with a registrar. That would be more state in the Store for every
// user and server; the registrar accepts a new Call-ID (RFC 3261 10.3
// step 7).
func (s *SCSCF) thirdPartyRegister(req *sip.Message, reg Registration, as ifc.ApplicationServer, expires time.Duration) *sip.Message {
	own := sip.NameAddr{URI: s.cfg.ServerName}.String()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: as.ServerName} // item a
	m.Add("Max-Forwards", "70")
	m.Add("From", own+";tag="+sip.NewTag())                        // item b
	m.Add("To", sip.NameAddr{URI: reg.DefaultIdentity()}.String()) // item c
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", own)                                    // item d
	m.Add("Expires", strconv.Itoa(int(expires/time.Second))) // items e and f
	// The opening paragraph of 5.4.1.7: the access network information
	// of the UE's REGISTER, for an application server in the trust
	// domain, as every server of this project is.
	for _, name := range []string{"P-Access-Network-Info", "P-Visited-Network-ID"} {
		for _, v := range req.GetAll(name) {
			m.Add(name, v)
		}
	}
	if expires == 0 {
		return m
	}
	// Item h: the icid-value of the UE's REGISTER, and a type 3 orig-ioi
	// naming this network.
	if icid := icidOf(req); icid != "" {
		m.Add("P-Charging-Vector", "icid-value="+icid+";orig-ioi="+s.cfg.HomeDomain)
	}
	// Item g and 5.4.1.7A.
	if as.ServiceInfo != "" {
		m.Add("Content-Type", ims.IMS3GPPContentType)
		m.Body = ims.ServiceInfoBody(as.ServiceInfo)
	}
	return m
}

// sendThirdPartyRegister sends m and applies the default handling of
// the criterion when the server fails (TS 24.229 5.4.1.7).
//
// SIMPLIFIED: TS 24.229 5.4.1.7. The term-ioi of the response is not
// stored; the S-CSCF does no charging.
func (s *SCSCF) sendThirdPartyRegister(m *sip.Message, reg Registration, as ifc.ApplicationServer, expires time.Duration, log *slog.Logger) {
	log = log.With("application_server", as.ServerName, "expires", int(expires/time.Second))
	final, err := s.sendRequest(m)
	if !unreachable(final, err) {
		log.Info("third-party REGISTER answered", "status", final.StatusCode)
		return
	}
	log.Info("third-party REGISTER failed", "status", statusOf(final), "error", errorText(err), "default_handling", as.DefaultHandling.String())
	if as.DefaultHandling != ifc.SessionTerminated || expires == 0 {
		return
	}
	// SESSION_TERMINATED: the network deregisters the identities that
	// the REGISTER registered (5.4.1.5). The UE is told rejected, since
	// registering again would only fail the same way while the server
	// is down.
	unlock, _ := s.users.Lock(context.Background(), reg.PrivateIdentity)
	defer unlock()
	current, ok := s.store.Registration(reg.PrivateIdentity)
	if !ok || current.ContactID != reg.ContactID {
		// Already deregistered, or another contact has taken its place.
		return
	}
	s.deregisterByNetwork(current, reginfo.Rejected)
	s.serverAssignment(current.PrivateIdentity, current.PublicIdentities[0], current.HSSHost, cx.AdministrativeDeregistration)
}

// equivalentRegister is the REGISTER that would have had the effect of
// a deregistration by the network (TS 23.218 5.2.3), for matching the
// criteria against.
func (s *SCSCF) equivalentRegister(reg Registration) *sip.Message {
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + s.cfg.HomeDomain}
	m.Add("From", sip.NameAddr{URI: reg.DefaultIdentity()}.String())
	m.Add("To", sip.NameAddr{URI: reg.DefaultIdentity()}.String())
	m.Add("Call-ID", reg.CallID)
	m.Add("Contact", sip.NameAddr{URI: reg.Contact}.String())
	m.Add("Expires", "0")
	return m
}

// sendRequest sends a request of the S-CSCF's own, as a UAC, to the
// next hop that its Route or Request-URI names, and returns the final
// response. With no final response, the error says why.
func (s *SCSCF) sendRequest(m *sip.Message) (*sip.Message, error) {
	stack, err := s.stack.Get()
	if err != nil {
		return nil, err
	}
	dst, err := s.nextHop(m)
	if err != nil {
		return nil, err
	}
	ct, err := stack.SendRequest(m, dst)
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
		return nil, errors.New("scscf: no final response")
	}
	return final, nil
}

// unreachable reports the failures after which TS 24.229 applies the
// default handling of a criterion: no response at all, a 408 (Request
// Timeout) or a 5xx (5.4.1.7, and 5.4.3.2 and 5.4.3.3 for requests
// other than REGISTER). Any other final response is the server's
// answer. A nil response with a nil error is the 2xx to an INVITE that
// Proxy has already passed on.
func unreachable(final *sip.Message, err error) bool {
	if err != nil {
		return true
	}
	return final != nil && (final.StatusCode == 408 || final.StatusCode/100 == 5)
}

func statusOf(m *sip.Message) int {
	if m == nil {
		return 0
	}
	return m.StatusCode
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
