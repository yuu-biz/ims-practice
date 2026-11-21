package sip

import (
	"fmt"
	"strings"
)

// OptionSecAgree is the security mechanism agreement of RFC 3329 (6.5).
// A client that starts it lists the tag in both Require and
// Proxy-Require (2.3.1).
const OptionSecAgree = "sec-agree"

// SecurityMechanism is one sec-mechanism of Security-Client,
// Security-Server or Security-Verify (RFC 3329 2.2): the name of a
// mechanism, such as ipsec-3gpp (TS 33.203 Annex H), and its
// parameters, such as alg or port-s.
//
//	Security-Client: ipsec-3gpp;alg=hmac-sha-1-96;spi-c=1111;spi-s=2222;port-c=5063;port-s=5062
type SecurityMechanism struct {
	Name   string
	Params Params
}

// ParseSecurityMechanism reads one element of the header field.
func ParseSecurityMechanism(value string) (SecurityMechanism, error) {
	name, params, _ := strings.Cut(strings.TrimSpace(value), ";")
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " \t=") {
		return SecurityMechanism{}, fmt.Errorf("sip: malformed sec-mechanism %q", value)
	}
	return SecurityMechanism{Name: name, Params: parseParams(params)}, nil
}

func (m SecurityMechanism) String() string { return m.Name + m.Params.String() }

// SecurityMechanisms reads every sec-mechanism of the named header field
// of m, in order, whether they come in one row or in several.
func SecurityMechanisms(m *Message, header string) ([]SecurityMechanism, error) {
	var mechanisms []SecurityMechanism
	for _, v := range m.GetAll(header) {
		sm, err := ParseSecurityMechanism(v)
		if err != nil {
			return nil, err
		}
		mechanisms = append(mechanisms, sm)
	}
	return mechanisms, nil
}

// SameSecurityMechanisms is the comparison of RFC 3329 2.3.1, which a
// server makes between the Security-Verify of a request and the
// Security-Server it sent: the same mechanisms in the same order, each
// with the same parameters and values. Names and values compare without
// case, as tokens do in SIP (RFC 3261 7.3.1), and the parameters of a
// mechanism may come in any order.
func SameSecurityMechanisms(a, b []SecurityMechanism) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i].Name, b[i].Name) || len(a[i].Params) != len(b[i].Params) {
			return false
		}
		for _, p := range a[i].Params {
			v, ok := b[i].Params.Get(p.Name)
			if !ok || !strings.EqualFold(v, p.Value) {
				return false
			}
		}
	}
	return true
}
