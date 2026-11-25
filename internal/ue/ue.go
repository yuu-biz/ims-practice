// Package ue is the UE simulator. It registers with the IMS the
// way TS 24.229 5.1.1 describes for SIP Digest without TLS, or for IMS
// AKA with its security agreement.
package ue

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Config is the identity and the credentials of the UE.
type Config struct {
	PrivateIdentity string
	PublicIdentity  string
	// TemporaryPublicIdentity reports that PublicIdentity is the
	// temporary public identity that a UE without an ISIM derives from
	// the IMSI (TS 23.003 13.4B). The network bars it: it serves REGISTER
	// only (TS 24.229 5.1.1.1A), so the UE shows it to nobody.
	TemporaryPublicIdentity bool
	Password                string
	HomeDomain              string
	// PCSCF is where every request goes, the address that P-CSCF
	// discovery gives (TS 24.229 5.1.1.1): with an access network the
	// first one of the PCO (L.2.2.1).
	//
	// SIMPLIFIED: TS 24.229 9.2.1. A UE without an access network, such
	// as a softphone on the host, gets the P-CSCF address from its
	// configuration.
	PCSCF netip.AddrPort
	// Expires is the interval the UE asks for: 600 000 s in TS 24.229
	// 5.1.1.2.1 item e.
	Expires time.Duration
	// PCSCFURI names the P-CSCF in the Route of the UE's requests, such
	// as sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060.
	//
	// SIMPLIFIED: TS 24.229 5.1.2A.1.1. The URI comes from the
	// configuration even when the address comes from the PCO: the
	// P-CSCF knows its own Route by its host name only.
	PCSCFURI string
	// Features are the feature parameters of the Contact of REGISTER
	// (RFC 3840 9): what the UE tells the network it can take, so that
	// the S-CSCF routes to it only what it can (RFC 3841). DefaultFeatures
	// is what the UE simulator registers.
	Features sip.Params
	// SCAddress is the number of the UE's SC (service centre) in
	// international format, such as +12025550100, which a USIM holds. It
	// is the RP-DA of every short message the UE submits, and, as a tel
	// URI, the PSI of the SC that its MESSAGE goes to (TS 24.341 5.3.1.2
	// a and NOTE 4). Empty means the UE sends no short messages.
	SCAddress string
	// AKA, when set, has the UE register with IMS AKA (TS 33.203)
	// instead of SIP Digest, and Password is not used.
	AKA *AKA

	// AccessNetworkInfo is the P-Access-Network-Info that the access
	// network lets the UE write in its requests and responses (TS 24.229
	// 5.1.2A.1.1, 7.2A.4), or "" without one.
	AccessNetworkInfo string
	// Calls is how the UE makes and takes calls.
	Calls CallConfig
}

// FeatureSMSOverIP is the feature tag of an SM-over-IP receiver (TS
// 24.341 5.3.2.2, defined in its Annex A.2).
const FeatureSMSOverIP = "+g.3gpp.smsip"

// DefaultFeatures are the feature parameters of the UE simulator: it
// receives short messages over IP, and GSMA IR.92 has a UE register
// the tag.
var DefaultFeatures = sip.Params{{Name: FeatureSMSOverIP}}

// DefaultExpires is the interval of TS 24.229 5.1.1.2.1 item e.
const DefaultExpires = 600000 * time.Second

// Registration is what the UE learns from the 200 OK (TS 24.229
// 5.1.1.2.1).
type Registration struct {
	Expires time.Duration
	// PublicIdentities are those of P-Associated-URI: the identities
	// the UE may use, which the S-CSCF lists without the barred ones.
	PublicIdentities []string
	ServiceRoute     []string
}

// DefaultIdentity is the default public identity, the first URI of
// P-Associated-URI (TS 24.229 5.1.1.2.1 item b). The UE uses it in its
// requests: a UE without an ISIM registers the temporary public
// identity derived from the IMSI, which serves only REGISTER (5.1.1.1A).
func (r Registration) DefaultIdentity() string { return r.PublicIdentities[0] }

// ErrForbidden means the network refused the registration with 403
// (TS 24.229 5.1.1.5.5).
var ErrForbidden = errors.New("registration refused with 403")

// RegisterFailure is a final response to a REGISTER that ends the
// registration attempt: neither a 2xx, nor the 401 and the 423 that the
// UE answers with another REGISTER. A 403 is ErrForbidden as well.
type RegisterFailure struct {
	StatusCode int
	Reason     string
	// RetryAfter is the Retry-After of the response, zero without one.
	// For that long the UE does not try again through the same P-CSCF
	// (TS 24.229 5.1.1.2.1).
	RetryAfter time.Duration
}

func (e *RegisterFailure) Error() string {
	text := fmt.Sprintf("REGISTER answered %d %s", e.StatusCode, e.Reason)
	if e.StatusCode == 403 {
		text = ErrForbidden.Error()
	}
	if e.RetryAfter > 0 {
		text += fmt.Sprintf(", Retry-After %d", int(e.RetryAfter/time.Second))
	}
	return text
}

func (e *RegisterFailure) Is(target error) bool {
	return target == ErrForbidden && e.StatusCode == 403
}

// registerFailure is the RegisterFailure of resp. A Retry-After that
// does not parse counts as none.
func registerFailure(resp *sip.Message) *RegisterFailure {
	f := &RegisterFailure{StatusCode: resp.StatusCode, Reason: resp.Reason}
	// RFC 3261 20.33: delta-seconds, then an optional comment and
	// parameters.
	value, _, _ := strings.Cut(resp.Get("Retry-After"), ";")
	value, _, _ = strings.Cut(value, "(")
	if value = strings.TrimSpace(value); value != "" {
		if n, err := sip.ParseDeltaSeconds(value); err == nil {
			f.RetryAfter = time.Duration(n) * time.Second
		}
	}
	return f
}

// UE is one simulated UE.
type UE struct {
	cfg     Config
	stack   *sip.Stack
	contact Contact
	log     *slog.Logger

	callID string
	cseq   int
	// The last challenge, kept to sign later requests (5.1.1.4.3).
	challenge *sip.Challenge
	nc        int
	// With IMS AKA, usim authenticates the challenges, and answer is the
	// last Authorization that answered one, which later REGISTERs repeat
	// (5.1.1.4.2). Like the fields above, only Register uses them.
	usim   *aka.USIM
	answer *akaAnswer

	// mu guards what the goroutines of incoming requests share with the
	// rest of the UE: the registration, under which the UE sends the
	// MESSAGEs of SMS, and the short messages it sent and received.
	mu  sync.Mutex
	reg *Registration // nil while not registered
	sms smsState
	// sa is the agreement on security associations with the P-CSCF; nil
	// without one.
	sa *securityAssociations
}

// Contact is the contact address of a UE: the SIP URI that it
// registers, and that the UA side of its calls writes in Contact too.
type Contact struct {
	user string
	addr netip.AddrPort
}

// NewContact is the contact address of the UE of cfg at addr. TS 24.229
// 5.1.1.2.1 item c has the UE give it a user part that is globally
// unique and reveals nothing private. The user part of the public
// identity would not do: a phone without an ISIM makes that identity
// from its IMSI (C.2). The UE keeps its contact for the refreshes and
// the removal of the binding (5.1.1.4.1 item c).
func NewContact(cfg Config, addr netip.AddrPort) Contact {
	return Contact{user: contactUser(cfg).String(), addr: addr}
}

// contactLabel keeps this use of the UE's key apart from any other.
const contactLabel = "contact user part"

// contactUser is a UUID that stays the same when the UE restarts. With a
// new user part, the restarted UE would bind a new contact address, and
// the S-CSCF would deregister the old one (TS 24.229 5.1.1.2.1 NOTE 13);
// with the same one, its registration refreshes the binding.
//
// The UUID is HMAC-SHA-256 of the private identity, keyed with what only
// the UE and its home network hold: K of the USIM, or the password. A
// hash of the private identity alone could be reversed by trying every
// IMSI. RFC 9562 5.5 puts a UUID made with SHA-256 in version 8, and its
// B.2 lays one out: the first 128 bits, with the version and variant
// bits set. A UE with neither K nor a password has nothing of its own to
// key the hash with, and takes a random UUID, which changes when it
// restarts.
func contactUser(cfg Config) uuid.UUID {
	key := []byte(cfg.Password)
	if cfg.AKA != nil {
		key = cfg.AKA.Secret.K[:]
	}
	if len(key) == 0 {
		return uuid.New()
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(contactLabel + "\x00" + cfg.PrivateIdentity))
	var u uuid.UUID
	copy(u[:], mac.Sum(nil))
	// RFC 9562 4.2 and 4.1: version 8 in the high 4 bits of octet 6,
	// variant 10 in the high 2 bits of octet 8.
	u[6] = u[6]&0x0f | 0x80
	u[8] = u[8]&0x3f | 0x80
	return u
}

// URI is the SIP URI of c.
func (c Contact) URI() string {
	return fmt.Sprintf("sip:%s@%s", c.user, c.addr)
}

// New uses stack to send its requests, and registers contact.
func New(cfg Config, stack *sip.Stack, contact Contact, log *slog.Logger) *UE {
	if cfg.Expires == 0 {
		cfg.Expires = DefaultExpires
	}
	var usim *aka.USIM
	if cfg.AKA != nil {
		usim = aka.NewUSIM(cfg.AKA.Secret, cfg.AKA.SQN)
	}
	return &UE{
		usim:    usim,
		cfg:     cfg,
		stack:   stack,
		contact: contact,
		log:     log,
		// TS 24.229 5.1.1.4.1: refreshes keep the Call-ID of the initial
		// registration.
		callID: sip.NewCallID(),
	}
}

// Register registers, answering one challenge (TS 24.229 5.1.1.2 and
// 5.1.1.5.4). It also refreshes an existing registration. An interval
// that the S-CSCF finds too brief is raised to its Min-Expires once
// (TS 24.229 5.1.1.2.1). After the first registration, the UE
// subscribes to its registration state with SubscribeRegEvent (5.1.1.3).
//
// Register tries once. After a failure the UE holds no registration,
// and the next Register is an initial registration (5.1.1.4.1);
// RegistrationBackoff tells how long to wait before it. A Register that
// the end of ctx cuts short leaves the registration as it was, so that
// the UE can still deregister it.
func (u *UE) Register(ctx context.Context) (Registration, error) {
	reg, err := u.send(ctx, u.cfg.Expires)
	var brief *intervalTooBrief
	if errors.As(err, &brief) {
		u.log.Info("interval too brief", "min_expires", int(brief.min/time.Second))
		u.cfg.Expires = brief.min
		reg, err = u.send(ctx, u.cfg.Expires)
	}
	if err != nil && ctx.Err() == nil {
		u.Unbind()
	}
	return reg, err
}

// Unbind is item 1 A of TS 24.229 5.1.2A.1.6: the UE no longer takes
// its contact as bound to any public user identity. The next Register
// is then an initial registration, sent unprotected and without the
// answer to an earlier challenge.
//
// It is also what the UE does when a NOTIFY of the reg event takes its
// registration away (RegEvent.Lost): it removes the registration and
// deletes its security associations towards the P-CSCF (5.1.1.7), as the
// P-CSCF ends its own on the same NOTIFY (5.2.5.2). The response to that
// NOTIFY, and to any retransmission of it, is the server transaction's,
// which does not need them.
//
// Only the goroutine that calls Register may call Unbind, since the
// answer to the last challenge is that goroutine's alone.
func (u *UE) Unbind() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.reg, u.sa = nil, nil
	u.challenge, u.answer = nil, nil
}

// intervalTooBrief is a 423 with the Min-Expires it asked for.
type intervalTooBrief struct{ min time.Duration }

func (e *intervalTooBrief) Error() string {
	return fmt.Sprintf("REGISTER answered 423, Min-Expires %d", int(e.min/time.Second))
}

// Deregister sends a REGISTER with an interval of zero (TS 24.229
// 5.1.1.6.1).
func (u *UE) Deregister(ctx context.Context) error {
	_, err := u.send(ctx, 0)
	return err
}

func (u *UE) send(ctx context.Context, expires time.Duration) (Registration, error) {
	var resp *sip.Message
	var err error
	if u.usim != nil {
		resp, err = u.sendAKA(ctx, expires)
	} else {
		resp, err = u.sendDigest(ctx, expires)
	}
	if err != nil {
		return Registration{}, err
	}
	switch {
	case resp.StatusCode == 423:
		n, err := sip.ParseDeltaSeconds(resp.Get("Min-Expires"))
		if err != nil {
			return Registration{}, fmt.Errorf("423 without a usable Min-Expires: %w", err)
		}
		return Registration{}, &intervalTooBrief{min: time.Duration(n) * time.Second}
	case resp.StatusCode != 200:
		return Registration{}, registerFailure(resp)
	}
	if err := u.checkRspauth(resp); err != nil {
		return Registration{}, err
	}
	reg := parseRegistration(resp)
	// 5.1.1.2.1 item c: an identity that P-Associated-URI leaves out is
	// barred. With none listed, the UE has no identity to use.
	if expires != 0 && len(reg.PublicIdentities) == 0 {
		return Registration{}, errors.New("the 200 to the REGISTER lists no identity in P-Associated-URI that the UE may use")
	}
	u.mu.Lock()
	if expires == 0 {
		u.reg = nil
	} else {
		u.reg = &reg
	}
	u.mu.Unlock()
	return reg, nil
}

// sendDigest is a REGISTER with SIP Digest, and its answer to a 401
// (5.1.1.5.4).
func (u *UE) sendDigest(ctx context.Context, expires time.Duration) (*sip.Message, error) {
	resp, err := u.transact(ctx, expires, u.authorization())
	if err != nil || resp.StatusCode != 401 {
		return resp, err
	}
	ch, err := sip.ParseChallenge(resp.Get("WWW-Authenticate"))
	if err != nil {
		return nil, fmt.Errorf("401 without a usable challenge: %w", err)
	}
	if ch.Algorithm != "" && ch.Algorithm != "MD5" {
		return nil, fmt.Errorf("challenge with algorithm %s, only MD5 is supported", ch.Algorithm)
	}
	u.challenge, u.nc = &ch, 0
	u.log.Info("challenged", "realm", ch.Realm, "nonce", ch.Nonce)
	return u.transact(ctx, expires, u.authorization())
}

// registration is the registration the UE holds, if any.
func (u *UE) registration() (Registration, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.reg == nil {
		return Registration{}, false
	}
	return *u.reg, true
}

func (u *UE) transact(ctx context.Context, expires time.Duration, authorization string) (*sip.Message, error) {
	return u.request(ctx, u.newREGISTER(expires, authorization))
}

// newREGISTER is a REGISTER of the UE with the header fields of TS 24.229
// 5.1.1.2.1.
func (u *UE) newREGISTER(expires time.Duration, authorization string) *sip.Message {
	u.cseq++
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + u.cfg.HomeDomain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+u.cfg.PublicIdentity+">;tag="+sip.NewTag())
	m.Add("To", "<"+u.cfg.PublicIdentity+">")
	m.Add("Call-ID", u.callID)
	m.Add("CSeq", strconv.Itoa(u.cseq)+" REGISTER")
	m.Add("Contact", sip.NameAddr{URI: u.contact.URI(), Params: u.cfg.Features}.String()+";expires="+strconv.Itoa(int(expires/time.Second)))
	// TS 24.229 5.1.1.2.1: the UE supports Path.
	m.Add("Supported", "path")
	m.Add("Authorization", authorization)
	m.Add("User-Agent", UserAgent)
	u.addAccessNetworkInfo(m)
	return m
}

// request sends req to the P-CSCF, where every request of the UE goes
// (TS 24.229 5.1.2A.1.1), and returns its final response.
func (u *UE) request(ctx context.Context, req *sip.Message) (*sip.Message, error) {
	return u.requestVia(ctx, u.stack, u.Outbound(), req)
}

// requestVia sends req from stack to dst and returns its final
// response.
func (u *UE) requestVia(ctx context.Context, stack *sip.Stack, dst sip.Destination, req *sip.Message) (*sip.Message, error) {
	ct, err := stack.SendRequest(req, dst)
	if err != nil {
		return nil, err
	}
	var final *sip.Message
	for {
		select {
		case r, ok := <-ct.Responses():
			if !ok {
				if final == nil {
					return nil, fmt.Errorf("no answer to %s: %w", req.Method, ct.Err())
				}
				return final, nil
			}
			if r.StatusCode >= 200 {
				final = r
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// authorization is the Authorization header of the next REGISTER.
// Before any challenge it is the one of TS 24.229 5.1.1.2.3, with an
// empty nonce and response. After one it answers the stored challenge
// with the next nonce count (RFC 2617 3.2.2).
func (u *UE) authorization() string {
	c := sip.Credentials{Username: u.cfg.PrivateIdentity, Realm: u.cfg.HomeDomain, URI: "sip:" + u.cfg.HomeDomain}
	if u.challenge == nil {
		return c.String()
	}
	u.nc++
	c.Realm, c.Nonce, c.Opaque = u.challenge.Realm, u.challenge.Nonce, u.challenge.Opaque
	c.Algorithm, c.QOP, c.CNonce, c.NC = "MD5", "auth", rand.Text()[:16], fmt.Sprintf("%08x", u.nc)
	c.Response, _ = sip.DigestResponse(u.ha1(), sip.REGISTER, c)
	return c.String()
}

func (u *UE) ha1() string {
	return sip.HA1(u.cfg.PrivateIdentity, u.challenge.Realm, u.cfg.Password)
}

// checkRspauth verifies Authentication-Info, which proves the network
// knows the password too (TS 24.229 5.1.1.5.4, RFC 2617 3.2.3). With
// IMS AKA the password is RES on this side and XRES on the network's
// (RFC 3310 3.5).
func (u *UE) checkRspauth(resp *sip.Message) error {
	info := resp.Get("Authentication-Info")
	switch {
	case info == "":
		return nil
	case u.answer != nil:
		c := u.answer.creds
		return u.verifyRspauth(info, sip.HA1(u.cfg.PrivateIdentity, c.Realm, aka.DigestPassword(u.answer.res)), c.Realm, c.Nonce)
	case u.challenge != nil:
		return u.verifyRspauth(info, u.ha1(), u.challenge.Realm, u.challenge.Nonce)
	}
	return nil
}

func (u *UE) verifyRspauth(info, ha1, realm, nonce string) error {
	got, err := sip.ParseCredentials("Digest " + info)
	if err != nil {
		return fmt.Errorf("Authentication-Info: %w", err)
	}
	rspauth, _ := got.AuthParams.Get("rspauth")
	want, err := sip.DigestResponse(ha1, "", sip.Credentials{
		Username: u.cfg.PrivateIdentity, Realm: realm, Nonce: nonce,
		URI: "sip:" + u.cfg.HomeDomain, QOP: "auth", CNonce: got.CNonce, NC: got.NC,
	})
	if err != nil || sip.Unquote(rspauth) != want {
		return errors.New("Authentication-Info: rspauth does not match")
	}
	return nil
}

func parseRegistration(resp *sip.Message) Registration {
	r := Registration{ServiceRoute: resp.GetAll("Service-Route")}
	for _, v := range resp.GetAll("P-Associated-URI") {
		if na, err := sip.ParseNameAddr(v); err == nil {
			r.PublicIdentities = append(r.PublicIdentities, na.URI)
		}
	}
	if na, err := sip.ParseNameAddr(resp.Get("Contact")); err == nil {
		if v, ok := na.Params.Get("expires"); ok {
			n, _ := strconv.Atoi(v)
			r.Expires = time.Duration(n) * time.Second
		}
	}
	return r
}

// addAccessNetworkInfo writes P-Access-Network-Info into m, a request
// or response of the UE, when the access network tells the UE its cell
// (TS 24.229 5.1.2A.1.1).
func (u *UE) addAccessNetworkInfo(m *sip.Message) {
	if u.cfg.AccessNetworkInfo != "" {
		m.Add("P-Access-Network-Info", u.cfg.AccessNetworkInfo)
	}
}
