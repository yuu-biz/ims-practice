package interconnect

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Profile is the interconnect agreement with one peer network: what
// the two operators chose for the II-NNI between them. TS 29.165 Annex
// C lists such choices as "option items" for operators to fill in
// (C.1, C.2); the fields here are the ones the IBCF of this project
// uses. The agreement
// is bilateral, so a profile applies to messages in both directions.
//
// Profile is plain data so that a configuration file can fill it in.
// Validate checks it.
type Profile struct {
	// Name names the peer in logs and errors.
	Name string
	// Domain is the home domain of the peer network. A telephone
	// number that leaves as a SIP URI with user=phone gets it as its
	// host when it was a tel URI before (TS 29.165 8.1).
	Domain string
	// Trusted tells whether the peer is inside the trust domain, which
	// depends on an interconnect agreement (TS 24.229 4.4.1). Without
	// one, the header fields of TS 24.229 4.4 and TS 29.165 Table 6.2
	// do not cross the border.
	Trusted bool
	// Transports are the transports the peer accepts at the Ici,
	// preferred first (TS 29.165 6.2, Table C.3.1.8).
	Transports []sip.Transport
	// Methods are the methods the agreement allows on top of the ones
	// TS 29.165 Table 6.1 makes mandatory at every II-NNI.
	Methods []sip.Method
	// OptionTags says what to do with an option tag in Supported,
	// Require and Proxy-Require. A tag that is not listed passes.
	OptionTags map[string]OptionTagPolicy
	// Headers are the operator's rules for header fields and their
	// parameters (TS 24.229 5.10.6.2), applied after the rules of the
	// specifications.
	Headers []HeaderRule
	// NumberFormat is how a telephone number in the Request-URI and in
	// P-Asserted-Identity crosses the border (TS 29.165 8.1). The empty
	// format keeps the URI scheme and only makes the number global.
	NumberFormat NumberFormat
	// LocalNumbers say how a local number becomes a global one, for
	// each phone-context that the two networks use (TS 29.165 8.1 NOTE
	// 2, RFC 3966 5.1.5).
	LocalNumbers []LocalNumberPlan
	// AllowLocalNumbers lets a local number that LocalNumbers cannot
	// make global cross the border as it is. Without such an agreement
	// TS 29.165 8.1 asks for global numbers.
	AllowLocalNumbers bool
	// Codecs are the payload formats the peer accepts, by encoding name
	// with an optional clock rate, such as "AMR-WB", "PCMU" or
	// "telephone-event/8000" (TS 29.165 7.1, Table C.3.1.9). An SDP
	// body loses every other format. No codecs means no filtering.
	// DTMF needs telephone-event in the list (TS 29.165 7.1 NOTE 2).
	Codecs []string
}

// LocalNumberPlan says how the local numbers of one phone-context
// become global ones. RFC 3966 5.1.5 warns that the phone-context is
// not a prefix in general, so the prefix to put in front of the local
// number is part of the agreement.
type LocalNumberPlan struct {
	// PhoneContext is the phone-context parameter as the sender writes
	// it: a global number prefix such as "+1-202", or a domain name.
	PhoneContext string
	// GlobalPrefix is what makes the local number global, such as
	// "+1202": a "+" and digits.
	GlobalPrefix string
}

// OptionTagPolicy is what the IBCF does with one option tag in a
// request (RFC 3261 19.2). The tags matter at the II-NNI through the
// capability they name in TS 29.165 Table 6.1.3.1: 100rel is item 18
// (mandatory), precondition item 20 (optional) and timer item 45
// (mandatory).
type OptionTagPolicy string

const (
	// OptionPass leaves the tag as it is.
	OptionPass OptionTagPolicy = "pass"
	// OptionRemove takes the tag out of Supported, Require and
	// Proxy-Require, as if the sender did not know the extension (TS
	// 24.229 5.10.6.2). It is for a tag whose extension lives in the
	// header fields alone: a profile cannot remove precondition, whose
	// SDP attributes would stay in the body (RFC 3312 4).
	OptionRemove OptionTagPolicy = "remove"
	// OptionReject has the IBCF, the UAS of the dialog in which the
	// request arrives, answer a request that requires the tag with 420
	// (Bad Extension), as a UAS that lacks the extension does (RFC 3261
	// 8.2.2.3): the agreement leaves the extension out of the II-NNI. A
	// request that only lists the tag in Supported crosses as it came,
	// and the far end answers with or without the extension, as RFC
	// 3312 11 has a UAS that lacks preconditions do.
	OptionReject OptionTagPolicy = "reject"
)

// HeaderAction is what a HeaderRule does.
type HeaderAction string

const (
	// HeaderRemove removes the header field, or one parameter of each
	// of its values.
	HeaderRemove HeaderAction = "remove"
	// HeaderRewrite replaces the value of a header field that is
	// present, or sets one parameter on each of its values.
	HeaderRewrite HeaderAction = "rewrite"
	// HeaderPass keeps a header field that the trust domain rules
	// would remove by operator policy towards an untrusted peer, such
	// as Server (TS 24.229 4.4.26). It cannot keep one that a
	// specification makes the IBCF remove.
	HeaderPass HeaderAction = "pass"
)

// Direction is which way a message crosses the border.
type Direction string

const (
	// Exit is from the home network to the peer (TS 24.229 5.10.2).
	Exit Direction = "exit"
	// Entry is from the peer into the home network (TS 24.229 5.10.3).
	Entry Direction = "entry"
)

// HeaderRule is one rule of the operator for a header field or one of
// its parameters, such as removing User-Agent, or removing the
// access-network-charging-info parameter of P-Charging-Vector before a
// request leaves (TS 24.229 5.10.2.2 step 8).
type HeaderRule struct {
	// Header is the name of the header field.
	Header string
	// Param, when set, is the header field parameter the rule applies
	// to. The rule then applies to each value of the header field.
	Param  string
	Action HeaderAction
	// Value is the new value of a rewrite: of the header field, or of
	// the parameter.
	Value string
	// Direction limits the rule to one direction. The empty direction
	// is both.
	Direction Direction
}

// mandatoryMethods are the methods that every II-NNI supports (TS
// 29.165 Table 6.1, status m).
var mandatoryMethods = []sip.Method{sip.ACK, sip.BYE, sip.CANCEL, sip.INVITE, sip.OPTIONS, sip.PRACK, sip.UPDATE}

// optionalMethods are the methods of TS 29.165 Table 6.1 that a
// non-roaming II-NNI supports by agreement: status o, and c1, which is
// o outside roaming. REGISTER (c2) is not applicable there.
var optionalMethods = []sip.Method{"INFO", sip.MESSAGE, "REFER", sip.NOTIFY, "PUBLISH", sip.SUBSCRIBE}

// protectedHeaders are header fields a HeaderRule may not touch. The
// routing ones would break the message (TS 24.229 5.10.6.2 NOTE 1),
// Authorization and WWW-Authenticate are named by 5.10.6.2, and the
// body and the Contact have procedures of their own here.
var protectedHeaders = []string{
	"Via", "Route", "Record-Route", "Call-ID", "CSeq", "Max-Forwards",
	"Contact", "Content-Type", "Content-Length",
	"Authorization", "WWW-Authenticate",
}

// Validate reports the first mistake in the profile.
func (p Profile) Validate() error {
	if p.Name == "" {
		return errors.New("interconnect: profile without a name")
	}
	wrap := func(err error) error { return fmt.Errorf("interconnect: profile %s: %w", p.Name, err) }
	if len(p.Transports) == 0 {
		return wrap(errors.New("no transport"))
	}
	for i, t := range p.Transports {
		if t != sip.UDP && t != sip.TCP {
			// TS 29.165 6.2.1 allows SCTP as an option, which the sip
			// package does not have.
			return wrap(fmt.Errorf("transport %q: want UDP or TCP", t))
		}
		if slices.Contains(p.Transports[:i], t) {
			return wrap(fmt.Errorf("transport %s listed twice", t))
		}
	}
	for _, m := range p.Methods {
		switch {
		case slices.Contains(mandatoryMethods, m):
			return wrap(fmt.Errorf("method %s is mandatory at every II-NNI (TS 29.165 Table 6.1) and needs no agreement", m))
		case !slices.Contains(optionalMethods, m):
			return wrap(fmt.Errorf("method %s is not applicable at a non-roaming II-NNI (TS 29.165 Table 6.1)", m))
		}
	}
	for tag, policy := range p.OptionTags {
		if !isToken(tag) {
			return wrap(fmt.Errorf("option tag %q is not a token", tag))
		}
		switch policy {
		case OptionPass, OptionReject:
		case OptionRemove:
			if tag == sip.OptionPrecondition {
				return wrap(fmt.Errorf("option tag %s: %s would leave the precondition attributes in the SDP; use %s or %s", tag, policy, OptionPass, OptionReject))
			}
		default:
			return wrap(fmt.Errorf("option tag %s: unknown policy %q", tag, policy))
		}
	}
	for _, r := range p.Headers {
		if err := r.validate(); err != nil {
			return wrap(err)
		}
	}
	switch p.NumberFormat {
	case "", NumberFormatTel:
	case NumberFormatSIP:
		if p.Domain == "" {
			return wrap(errors.New("number format sip needs the peer's domain"))
		}
	default:
		return wrap(fmt.Errorf("unknown number format %q", p.NumberFormat))
	}
	for _, l := range p.LocalNumbers {
		if l.PhoneContext == "" {
			return wrap(errors.New("local number plan without a phone-context"))
		}
		if !isGlobalNumber(l.GlobalPrefix) {
			return wrap(fmt.Errorf("phone-context %s: global prefix %q is not + and digits", l.PhoneContext, l.GlobalPrefix))
		}
	}
	for _, c := range p.Codecs {
		if _, _, err := parseCodecName(c); err != nil {
			return wrap(err)
		}
	}
	return nil
}

func (r HeaderRule) validate() error {
	if !isToken(r.Header) {
		return fmt.Errorf("header rule for %q: not a header field name", r.Header)
	}
	if slices.ContainsFunc(protectedHeaders, func(h string) bool { return strings.EqualFold(h, r.Header) }) {
		return fmt.Errorf("header rule for %s: this header field is not open to rules", r.Header)
	}
	if r.Param != "" && !isToken(r.Param) {
		return fmt.Errorf("header rule for %s: parameter %q is not a token", r.Header, r.Param)
	}
	switch r.Direction {
	case "", Exit, Entry:
	default:
		return fmt.Errorf("header rule for %s: unknown direction %q", r.Header, r.Direction)
	}
	switch r.Action {
	case HeaderRemove:
	case HeaderRewrite:
		if r.Param == "" && r.Value == "" {
			return fmt.Errorf("header rule for %s: a rewrite needs a value", r.Header)
		}
	case HeaderPass:
		if r.Param != "" {
			return fmt.Errorf("header rule for %s: pass applies to a whole header field", r.Header)
		}
		row, ok := trustRow(r.Header)
		if !ok {
			return fmt.Errorf("header rule for %s: pass only applies to the header fields of the trust domain (TS 24.229 4.4)", r.Header)
		}
		if row.exit == removeAlways || row.entry == removeAlways {
			return fmt.Errorf("header rule for %s: %s makes the IBCF remove it towards an untrusted peer", r.Header, row.reference)
		}
	default:
		return fmt.Errorf("header rule for %s: unknown action %q", r.Header, r.Action)
	}
	return nil
}

// allowedMethods are the methods that may cross the border with this
// peer, in the order of TS 29.165 Table 6.1.
func (p Profile) allowedMethods() []sip.Method {
	allowed := slices.Clone(mandatoryMethods)
	for _, m := range optionalMethods {
		if slices.Contains(p.Methods, m) {
			allowed = append(allowed, m)
		}
	}
	return allowed
}

// PreferredTransport is the transport to send to the peer with: the
// first one the agreement lists.
func (p Profile) PreferredTransport() sip.Transport {
	if len(p.Transports) == 0 {
		return sip.UDP
	}
	return p.Transports[0]
}

// AcceptsTransport tells whether the agreement lists the transport.
func (p Profile) AcceptsTransport(t sip.Transport) bool {
	return slices.Contains(p.Transports, t)
}

// isToken reports whether s is a token of RFC 3261 25.1, the grammar
// of header field names, parameter names and option tags.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		isAlnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !isAlnum && !strings.ContainsRune("-.!%*_+`'~", rune(c)) {
			return false
		}
	}
	return true
}
