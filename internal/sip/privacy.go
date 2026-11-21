package sip

import (
	"slices"
	"strings"
)

// Values of the Privacy header field: the priv-values of RFC 3323 4.2,
// and the ones that later documents added.
const (
	PrivacyHeader   = "header"
	PrivacySession  = "session"
	PrivacyUser     = "user"
	PrivacyNone     = "none"
	PrivacyCritical = "critical"
	// PrivacyID keeps P-Asserted-Identity inside the trust domain (RFC
	// 3325 9.3). It is how a caller withholds its number.
	PrivacyID = "id"
	// PrivacyHistory asks to anonymize History-Info entries (RFC 7044
	// 10.1.1).
	PrivacyHistory = "history"
)

// Privacy is the value of the Privacy header field (RFC 3323 4.2): the
// privacy services that the user asks of the network.
//
//	Privacy: id;critical
type Privacy []string

// ParsePrivacy reads the priv-values, which ";" separates. Privacy is
// not a comma-separated list, so a message has one such value.
func ParsePrivacy(value string) Privacy {
	var p Privacy
	for _, v := range strings.Split(value, ";") {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			p = append(p, v)
		}
	}
	return p
}

func (p Privacy) String() string { return strings.Join(p, ";") }

// Has reports whether the priv-value is among those asked for.
func (p Privacy) Has(value string) bool { return slices.Contains(p, value) }

// Header is the Privacy header field with this value.
func (p Privacy) Header() HeaderField { return HeaderField{Name: "Privacy", Value: p.String()} }
