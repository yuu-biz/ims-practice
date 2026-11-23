package pcscf

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the security agreement of IMS AKA at the P-CSCF (TS
// 24.229 5.2.2.2, TS 33.203 7): the Security-Client of the UE, the
// Security-Server of the P-CSCF in the 401, the Security-Verify of the
// REGISTER that answers it, the keys that the S-CSCF sends, and the
// protected server port.
//
// SIMPLIFIED: TS 33.203 7.1 and 7.2, TS 24.229 5.2.2.2. No IPsec
// ESP is set up. The P-CSCF agrees on the security associations, keeps
// them in its SA table with IK and CK, and counts a message as
// protected when it arrives on the protected server port from the
// address of a set of security associations. IMS AKA without ESP is
// not in the standard; it is left out here so that captures stay
// readable by default. Without ESP:
//   - The UE and the P-CSCF send and receive every protected message on
//     their protected server ports. internal/sip sends a message from
//     the socket it listens on, so the protected client ports are
//     agreed and kept, but never bound. A message over TCP comes from a
//     port of the moment: the P-CSCF knows the UE of a request over TCP,
//     a REGISTER of the agreement as well, by the sent-by of its Via
//     (ueSource). Nothing authenticates that sent-by, so a host that
//     shares the UE's IP address could name the UE's protected server
//     port in it; with ESP the security associations would authenticate
//     the sender.
//   - A request that the P-CSCF proxies with its server transaction
//     leaves from the transport it came in on, so the P-CSCF also talks
//     to the core about a UE with security associations over the
//     protected server port: its Path and Record-Route name that port.
//     TS 33.203 7.1 keeps unprotected messages off it. An initial
//     INVITE, which the P-CSCF forwards with a client transaction of its
//     own, and the BYEs that it sends itself go from the protected
//     server port when they go to such a UE, and from the unprotected
//     one otherwise.
//   - The P-CSCF keeps one set of security associations per UE. A new
//     challenge replaces the set that the UE had, which TS 24.229 Table
//     5.2.2-1 keeps until the 200 (OK); a UE that starts again with an
//     unprotected REGISTER no longer has that set anyway (TS 33.203
//     7.4.2a), and the S-CSCF here never challenges a protected one.

// SecurityAssociations are the two pairs of security associations that
// one IMS AKA authentication sets up between the P-CSCF and a UE: one
// entry of the SA table of TS 33.203 7.1.
type SecurityAssociations struct {
	// UE is the UE's IP address and protected server port, from which
	// its protected messages come in this project. It is the key of the
	// entry.
	UE netip.AddrPort
	// Temporary is true from the 401 that set the associations up until
	// the 200 (OK) to the REGISTER that answered it, which makes them
	// newly established (TS 24.229 5.2.2.2).
	Temporary bool
	// UEMechanism and PCSCFMechanism are the parameters of the UE's end
	// and the P-CSCF's: the algorithms both chose, the SPIs and the ports.
	UEMechanism, PCSCFMechanism ims.IPsec3GPP
	// Client is the Security-Client of the REGISTER that was challenged,
	// and Server the Security-Server of the 401. The REGISTER that
	// answers the challenge has to repeat both, the second in
	// Security-Verify (5.2.2.2 item 3 a).
	Client, Server []sip.SecurityMechanism
	// IK and CK are the keys of the 401 (TS 33.203 6.1.1), which ESP
	// would use.
	IK              aka.IK
	CK              aka.CK
	PrivateIdentity string
	// Expires ends the associations: reg-await-auth after the 401, and
	// 30 s after the registration once they are established (5.2.2.2).
	Expires time.Time
}

// securityAssociationMargin is how much longer established security
// associations last than the registration (TS 24.229 5.2.2.2).
const securityAssociationMargin = 30 * time.Second

// saTable is the SA table of MemoryStore. Each entry is removed when it
// is read after its Expires, or replaced.
type saTable struct {
	mu sync.Mutex
	m  map[netip.AddrPort]SecurityAssociations
}

func (t *saTable) SecurityAssociations(_ context.Context, ue netip.AddrPort) (SecurityAssociations, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sas, ok := t.m[ue]
	if ok && !time.Now().Before(sas.Expires) {
		delete(t.m, ue)
		return SecurityAssociations{}, false, nil
	}
	return sas, ok, nil
}

func (t *saTable) PutSecurityAssociations(_ context.Context, sas SecurityAssociations) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[netip.AddrPort]SecurityAssociations{}
	}
	sas.Client, sas.Server = slices.Clone(sas.Client), slices.Clone(sas.Server)
	t.m[sas.UE] = sas
	return nil
}

func (t *saTable) DeleteSecurityAssociations(_ context.Context, ue netip.AddrPort) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, ue)
	return nil
}

// OnProtectedRequest is the sip.Config.OnRequest of the stack on the
// protected server port.
func (p *PCSCF) OnProtectedRequest(st *sip.ServerTransaction) {
	if st.Request.Method == sip.REGISTER {
		p.register(st, true)
		return
	}
	p.session(st)
}

// OnProtectedACK is the sip.Config.OnACK of that stack.
func (p *PCSCF) OnProtectedACK(ack *sip.Message) { p.forwardACK(&p.protectedStack, ack) }

// OnProtectedCANCEL is the sip.Config.OnCANCEL of that stack.
func (p *PCSCF) OnProtectedCANCEL(cancel *sip.Message) { p.forwardCANCEL(&p.protectedStack, cancel) }

// SetProtectedStack gives the P-CSCF the stack on its protected server
// port. Call it right after the stack is made.
func (p *PCSCF) SetProtectedStack(stack *sip.Stack) { p.protectedStack.Set(stack) }

// protectedURI is the P-CSCF at its protected server port.
func (p *PCSCF) protectedURI() sip.URI {
	u, _ := ims.LooseRoute(fmt.Sprintf("sip:%s:%d", p.cfg.Host, p.cfg.ProtectedServerPort))
	return u
}

// ownURIs are the URIs of the P-CSCF, which it takes off the top of a
// Route.
func (p *PCSCF) ownURIs() []sip.URI {
	if p.cfg.ProtectedServerPort == 0 {
		return []sip.URI{p.ownURI()}
	}
	return []sip.URI{p.ownURI(), p.protectedURI()}
}

// ownURIToward is the P-CSCF's URI in a Record-Route that the UE at ue
// sees: with the protected server port for a UE with established
// security associations (TS 24.229 5.2.6.3.4 item 4, 5.2.6.4.3 item 4),
// and with the unprotected port otherwise.
func (p *PCSCF) ownURIToward(ue sip.Destination) (sip.URI, error) {
	established, err := p.established(ue.Addr)
	if err != nil || !established {
		return p.ownURI(), err
	}
	return p.protectedURI(), nil
}

// stackToward is the stack that a request which the P-CSCF sends with a
// client transaction of its own leaves from: the one on the protected
// server port for a UE with established security associations (TS
// 33.203 7.1), the unprotected one otherwise.
func (p *PCSCF) stackToward(dst sip.Destination) (*ims.StackRef, error) {
	established, err := p.established(dst.Addr)
	if err != nil {
		return nil, err
	}
	if established && p.cfg.ProtectedServerPort != 0 {
		return &p.protectedStack, nil
	}
	return &p.stack, nil
}

// established reports whether the UE at ue has established security
// associations. Temporary ones are those of an authentication under way
// (TS 24.229 5.2.2.2), so they do not count.
func (p *PCSCF) established(ue netip.AddrPort) (bool, error) {
	sas, ok, err := p.store.SecurityAssociations(context.Background(), ue)
	return ok && !sas.Temporary, err
}

// protectedPathURI is the Path URI of a REGISTER that came protected,
// so that requests to the UE come to the protected server port.
func (p *PCSCF) protectedPathURI() string {
	return fmt.Sprintf("<sip:term@%s:%d;lr>", p.cfg.Host, p.cfg.ProtectedServerPort)
}

// pathURIOf is the URI that the P-CSCF put in the Path of the
// registration of a: the protected one for a UE with established
// security associations.
func (p *PCSCF) pathURIOf(a Association) (string, error) {
	established, err := p.established(a.Source.Addr)
	if err != nil || !established {
		return p.pathURI(), err
	}
	return p.protectedPathURI(), nil
}

// AdmitUnprotected is the sip.Config.Admit of the stack on the
// P-CSCF's unprotected port. A request other than REGISTER that comes
// there from a UE with established security associations is not
// protected, and the P-CSCF discards an unprotected message outside the
// registration and authentication procedures (TS 24.229 5.2.1 NOTE 8):
// as Screen does with a request that has no IP association, it sends
// nothing back, not even a 100 (Trying), and forwards nothing. TS
// 33.203 7.1 lets it either discard or reject what does not arrive on a
// protected port.
//
// The SA table is in the Store, which several P-CSCFs may share, and a
// UE's entry changes with each registration, through whichever of them
// it goes. So the P-CSCF reads the entry for each request. The stack
// asks Admit off its loop, and about the requests with different
// Call-IDs side by side, so the read holds up only the later requests
// with the same Call-ID from the same source.
func (p *PCSCF) AdmitUnprotected(req *sip.Message, source sip.Destination) bool {
	if req.Method == sip.REGISTER {
		return true
	}
	ue, _ := ueSourceOf(req, source)
	established, err := p.established(ue.Addr)
	if err != nil {
		// Unlike Screen, which keeps out a request whose IP association
		// it cannot read, Admit lets the request in when it cannot read
		// the SA table: the P-CSCF then reads the Store again to serve
		// the request, and answers 500 when that fails too.
		p.log.Warn("could not read the SA table for an unprotected request", "method", string(req.Method),
			"source", source.String(), "call_id", req.Get("Call-ID"), "error", err.Error())
		return true
	}
	if !established {
		return true
	}
	p.log.Info("discarded an unprotected request from a UE with security associations",
		"method", string(req.Method), "source", source.String(), "call_id", req.Get("Call-ID"))
	return false
}

// asksSecAgree reports whether a REGISTER chooses IMS AKA: it carries
// Security-Client and lists sec-agree in Require and Proxy-Require (TS
// 24.229 5.2.2.1 item 1).
func asksSecAgree(req *sip.Message) bool {
	return req.Get("Security-Client") != "" && req.Requires(sip.OptionSecAgree) &&
		slices.Contains(req.GetAll("Proxy-Require"), sip.OptionSecAgree)
}

// secAgreement is the security agreement of one REGISTER while the
// P-CSCF forwards it.
type secAgreement struct {
	// ue is the key of the UE in the SA table.
	ue        netip.AddrPort
	privateID string
	// client is the Security-Client of the REGISTER, and offered its
	// ipsec-3gpp mechanisms.
	client  []sip.SecurityMechanism
	offered []ims.IPsec3GPP
	// over is the set of security associations the REGISTER came over;
	// nil when it came unprotected.
	over      *SecurityAssociations
	integrity sip.IntegrityProtected
}

// receiveSecAgree is TS 24.229 5.2.2.2 for a REGISTER from the UE,
// items 1 to 3. It takes the security agreement out of out, the
// REGISTER to forward, or returns the response that refuses it.
func (p *PCSCF) receiveSecAgree(st *sip.ServerTransaction, out *sip.Message, privateID string, creds *sip.Credentials, protected bool, log *slog.Logger) (*secAgreement, *sip.Message) {
	req := st.Request
	refuse := func(code int, why string) (*secAgreement, *sip.Message) {
		log.Info("security agreement refused", "why", why, "status", code)
		resp := sip.NewResponse(req, code, sip.NewTag())
		if code == 420 {
			resp.Add("Unsupported", sip.OptionSecAgree)
		}
		if code == 494 {
			// RFC 3329 2.3.1: the server's list goes with a 494.
			for _, t := range ims.SupportedTransforms {
				m := ims.IPsec3GPP{Transform: t}.SecurityMechanism()
				m.Params = slices.DeleteFunc(m.Params, func(p sip.Param) bool { return strings.HasPrefix(p.Name, "spi-") || strings.HasPrefix(p.Name, "port-") })
				resp.Add("Security-Server", m.String())
			}
		}
		return nil, resp
	}
	if p.cfg.ProtectedServerPort == 0 {
		// A P-CSCF without a protected server port does no IMS AKA, and a
		// proxy names an option tag of Proxy-Require that it does not
		// support in a 420 (RFC 3261 16.3 step 5).
		return refuse(420, "no protected server port")
	}
	a := &secAgreement{privateID: privateID, integrity: sip.IntegrityProtectedNo}
	var err error
	if a.client, err = sip.SecurityMechanisms(req, "Security-Client"); err == nil {
		a.offered, err = ims.IPsec3GPPMechanisms(req, "Security-Client")
	}
	if err != nil || len(a.offered) == 0 {
		// Items 2 a and 3 a and b.
		return refuse(494, "no usable Security-Client")
	}
	if _, _, ok := ims.ChooseTransform(p.offers(ims.IPsec3GPP{}), a.offered); !ok {
		// TS 33.203 7.3.2.1.
		return refuse(494, "no algorithms in common")
	}
	if protected {
		// Item 3 a: the associations that protected the request. TCP and
		// UDP share them (TS 33.203 7.1), so a REGISTER over 1300 bytes,
		// which comes over TCP, has them at the UE that ueSource names.
		sas, ok, err := p.store.SecurityAssociations(context.Background(), ueSource(st).Addr)
		switch {
		case err != nil:
			return nil, storeFailed(req, err, log)
		case !ok:
			// TS 33.203 7.1 item 3: ESP would have dropped it.
			return refuse(403, "no security associations")
		case !strings.EqualFold(sas.PrivateIdentity, privateID):
			// Item 3 c.
			return refuse(403, "another private user identity")
		}
		a.ue, a.over = sas.UE, &sas
		if sas.Temporary {
			// Item 3 a, and TS 33.203 Annex H items 1 to 3.
			verify, err := sip.SecurityMechanisms(req, "Security-Verify")
			if err != nil || !sip.SameSecurityMechanisms(verify, sas.Server) || !sip.SameSecurityMechanisms(a.client, sas.Client) {
				return refuse(494, "Security-Verify or Security-Client changed")
			}
		}
		// Item 1: yes for the answer to the challenge over the temporary
		// associations, and for any REGISTER over established ones.
		if !sas.Temporary || (creds != nil && creds.Response != "") {
			a.integrity = sip.IntegrityProtectedYes
		}
	} else {
		a.ue = netip.AddrPortFrom(st.Source.Addr.Addr(), a.offered[0].PortS)
		// TS 33.203 7.3.1.2 to 7.3.1.4: a new unprotected REGISTER ends
		// an authentication under way, with its temporary associations.
		ctx := context.Background()
		sas, ok, err := p.store.SecurityAssociations(ctx, a.ue)
		if err == nil && ok && sas.Temporary {
			err = p.store.DeleteSecurityAssociations(ctx, a.ue)
		}
		if err != nil {
			return nil, storeFailed(req, err, log)
		}
	}
	// Items 2 a, 3 a and 3 b: the security agreement ends here, at the
	// first hop (RFC 3329 2.3.1).
	out.Remove("Security-Client")
	out.Remove("Security-Verify")
	removeOption(out, "Require", sip.OptionSecAgree)
	removeOption(out, "Proxy-Require", sip.OptionSecAgree)
	return a, nil
}

// offers are the ipsec-3gpp mechanisms of the P-CSCF's Security-Server,
// in the order of ims.SupportedTransforms, with the SPIs and ports of
// sa. The q values go down the list (RFC 3329 2.2).
func (p *PCSCF) offers(sa ims.IPsec3GPP) []ims.IPsec3GPP {
	var list []ims.IPsec3GPP
	for i, t := range ims.SupportedTransforms {
		m := sa
		m.Transform = t
		m.Preference = fmt.Sprintf("0.%d", len(ims.SupportedTransforms)-i)
		list = append(list, m)
	}
	return list
}

// answerSecAgree is TS 24.229 5.2.2.2 for the final response to a
// REGISTER that came with a security agreement. It returns the failure
// of the Store when the SA table cannot have what the response tells the
// UE.
func (p *PCSCF) answerSecAgree(st *sip.ServerTransaction, final *sip.Message, a *secAgreement, log *slog.Logger) (*sip.Message, error) {
	ctx := context.Background()
	switch {
	case final.StatusCode == 401:
		return p.challenged(st, final, a, log)
	case final.StatusCode == 200 && a.over != nil:
		interval := grantedInterval(final)
		if interval == 0 {
			// TS 33.203 7.4.2a: the associations go once the UE has no
			// public user identity registered.
			return final, p.store.DeleteSecurityAssociations(ctx, a.ue)
		}
		// On a 200 (OK), items 1 and 2.
		sas := *a.over
		expires := time.Now().Add(interval + securityAssociationMargin)
		if sas.Temporary || expires.After(sas.Expires) {
			sas.Expires = expires
		}
		if sas.Temporary {
			log.Info("security associations established", "ue", sas.UE.String())
		}
		sas.Temporary = false
		if err := p.store.PutSecurityAssociations(ctx, sas); err != nil {
			return nil, err
		}
	case final.StatusCode >= 300 && a.over != nil && a.over.Temporary:
		// TS 33.203 7.3.1.1: after a failed authentication both ends
		// delete the new associations. The UE learns of the failure all
		// the same; associations that stay go at their Expires.
		if err := p.store.DeleteSecurityAssociations(ctx, a.ue); err != nil {
			log.Warn("the temporary security associations stay until they expire: the store failed", "error", err.Error())
		}
	}
	return final, nil
}

// challenged is TS 24.229 5.2.2.2 for a 401: the P-CSCF takes IK and
// CK out of the challenge, sets up temporary security associations and
// offers them in Security-Server. A 401 without the keys does not reach
// the UE (item 2).
func (p *PCSCF) challenged(st *sip.ServerTransaction, final *sip.Message, a *secAgreement, log *slog.Logger) (*sip.Message, error) {
	var ik aka.IK
	var ck aka.CK
	found := false
	challenges := final.GetAll("WWW-Authenticate")
	final.Remove("WWW-Authenticate")
	for _, v := range challenges {
		if ch, err := sip.ParseChallenge(v); err == nil {
			if hexIK, hexCK, ok := ch.Keys(); ok {
				found = found || (decodeKey(ik[:], hexIK) && decodeKey(ck[:], hexCK))
				ch.RemoveKeys()
				v = ch.String()
			}
		}
		final.Add("WWW-Authenticate", v)
	}
	if !found {
		log.Warn("401 without ik and ck")
		return sip.NewResponse(st.Request, 500, sip.NewTag()), nil
	}
	// Item 4, and TS 33.203 7.2: the P-CSCF's SPIs differ from the UE's.
	ue := a.offered[0]
	spiC := ims.NewSPI(ue.SPIC, ue.SPIS)
	own := ims.IPsec3GPP{SPIC: spiC, SPIS: ims.NewSPI(ue.SPIC, ue.SPIS, spiC),
		PortC: ims.NewProtectedPort(uint16(p.cfg.ProtectedServerPort)), PortS: uint16(p.cfg.ProtectedServerPort)}
	server := p.offers(own)
	fromPCSCF, fromUE, _ := ims.ChooseTransform(server, a.offered)
	sas := SecurityAssociations{UE: a.ue, Temporary: true, UEMechanism: fromUE, PCSCFMechanism: fromPCSCF,
		Client: a.client, IK: ik, CK: ck, PrivateIdentity: a.privateID, Expires: time.Now().Add(ims.RegAwaitAuth)}
	// Item 3.
	for _, m := range server {
		sas.Server = append(sas.Server, m.SecurityMechanism())
		final.Add("Security-Server", m.SecurityMechanism().String())
	}
	// Item 1: the new set replaces any earlier one. Without it, the UE
	// would answer the challenge over associations that no P-CSCF
	// instance knows.
	if err := p.store.PutSecurityAssociations(context.Background(), sas); err != nil {
		return nil, err
	}
	log.Info("temporary security associations", "ue", sas.UE.String(), "alg", fromPCSCF.Algorithm,
		"spi_pc", fromPCSCF.SPIC, "spi_ps", fromPCSCF.SPIS, "spi_uc", fromUE.SPIC, "spi_us", fromUE.SPIS)
	return final, nil
}

func decodeKey(dst []byte, s string) bool {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(dst) {
		return false
	}
	copy(dst, b)
	return true
}

// grantedInterval is the interval that the 200 (OK) to a REGISTER gives
// the contact (RFC 3261 10.3 step 8).
func grantedInterval(resp *sip.Message) time.Duration {
	contact, err := sip.ParseNameAddr(resp.Get("Contact"))
	if err != nil {
		return 0
	}
	v, _ := contact.Params.Get("expires")
	n, err := sip.ParseDeltaSeconds(v)
	if err != nil {
		return 0
	}
	return time.Duration(n) * time.Second
}

// removeOption takes one option tag out of Require or Proxy-Require,
// and the header field with it when no tag is left.
func removeOption(m *sip.Message, header, option string) {
	tags := slices.DeleteFunc(m.GetAll(header), func(t string) bool { return strings.EqualFold(t, option) })
	m.Remove(header)
	for _, t := range tags {
		m.Add(header, t)
	}
}
