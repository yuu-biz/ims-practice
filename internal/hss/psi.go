package hss

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
)

// PSIUser is a PSI user (TS 23.228 5.4.12.4): a range of public service
// identities that an application server hosts, as the HSS stores it,
// so that an S-CSCF can serve a request that the AS sends on behalf of
// one of them (TS 23.228 5.4.12.5). Build one with ParsePSIUser.
type PSIUser struct {
	// PrivateIdentity is the private service identity, which ties the
	// PSIs to the Cx procedures and serves for nothing else: a PSI user
	// neither registers nor authenticates (TS 23.003 13.5A).
	PrivateIdentity string
	// WildcardedPSI is the range as the HSS stores it: a SIP URI whose
	// user part holds an extended regular expression between the first
	// and the last "!" (TS 23.003 13.5).
	WildcardedPSI string
	// matcher matches the canonical form of a PSI of the range
	// (ims.CanonicalURI).
	matcher *regexp.Regexp
}

// ParsePSIUser checks a PSI user and compiles its range.
func ParsePSIUser(privateIdentity, wildcardedPSI string) (PSIUser, error) {
	if privateIdentity == "" {
		return PSIUser{}, errors.New("private_identity: required")
	}
	rest, ok := strings.CutPrefix(wildcardedPSI, "sip:")
	if !ok {
		return PSIUser{}, fmt.Errorf("wildcarded_psi: %q is not a SIP URI", wildcardedPSI)
	}
	first, last := strings.Index(rest, "!"), strings.LastIndex(rest, "!")
	at := strings.LastIndex(rest, "@")
	if first < 0 || first == last || at < last {
		return PSIUser{}, fmt.Errorf("wildcarded_psi: %q has no regular expression between two \"!\" in its user part", wildcardedPSI)
	}
	host := strings.ToLower(rest[at+1:])
	if host == "" || strings.ContainsAny(host, ";:?") {
		return PSIUser{}, fmt.Errorf("wildcarded_psi: %q needs a host with no port and no parameters", wildcardedPSI)
	}
	// The fixed parts match as they are; the part between the
	// delimiters is an ERE (TS 23.003 13.5), which CompilePOSIX takes.
	pattern := "^" + regexp.QuoteMeta("sip:"+rest[:first]) + "(" + rest[first+1:last] + ")" +
		regexp.QuoteMeta(rest[last+1:at]+"@"+host) + "$"
	matcher, err := regexp.CompilePOSIX(pattern)
	if err != nil {
		return PSIUser{}, fmt.Errorf("wildcarded_psi: %q: %w", wildcardedPSI, err)
	}
	return PSIUser{PrivateIdentity: privateIdentity, WildcardedPSI: wildcardedPSI, matcher: matcher}, nil
}

// Matches reports whether psi is one of the range. An exclamation mark
// in psi is a character like any other (TS 23.003 13.5).
func (p PSIUser) Matches(psi string) bool {
	canonical, err := ims.CanonicalURI(psi)
	return err == nil && p.matcher != nil && p.matcher.MatchString(canonical)
}

// psiUserOf is the PSI user whose range holds publicID, for a request
// whose User-Name, when there is one, is the private service identity
// of that PSI user. The HSS looks for a PSI user only when no
// subscription has publicID: a distinct identity in the range of a
// wildcarded one can have a profile of its own (TS 29.228 6.1.2.1 NOTE
// 5). The configuration holds a few PSI users, so the loop does not
// grow with the subscribers.
func (h *HSS) psiUserOf(userName, publicID string) (PSIUser, bool) {
	for _, p := range h.config.PSIUsers {
		if p.Matches(publicID) && (userName == "" || privateKey(userName) == privateKey(p.PrivateIdentity)) {
			return p, true
		}
	}
	return PSIUser{}, false
}

// psiServerAssignment is TS 29.228 6.1.2.1 for a PSI of the PSI user p.
// Step 4: a PSI neither registers nor authenticates. The S-CSCF takes
// the profile of the PSI for a request that the AS sends on behalf of
// it (UNREGISTERED_USER), and lets it go when it no longer keeps it.
//
// SIMPLIFIED: TS 29.228 6.1.2.1. The HSS keeps no state for a PSI user:
// no S-CSCF name, no registration state and no PSI Activation State. So
// it answers the SAR of any S-CSCF. A PSI user is a range of SIP URIs,
// with no criteria. The S-CSCF asks for the profile of a PSI only when
// the AS that hosts it sends a request on its behalf, and a request to
// a PSI finds the AS by DNS (TS 24.229 5.3.2.1 step 3), so no node asks
// the HSS where a PSI is served.
func psiServerAssignment(p PSIUser, publicID string, r cx.ServerAssignmentRequest) cx.ServerAssignmentAnswer {
	switch r.ServerAssignmentType {
	case cx.AssignRegistration, cx.ReRegistration, cx.UserDeregistration, cx.AuthenticationFailure, cx.AuthenticationTimeout:
		return cx.ServerAssignmentAnswer{Result: diameter.Experimental(cx.DiameterErrorInAssignmentType)}
	case cx.UnregisteredUser:
		a := cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: p.PrivateIdentity}
		if !r.UserDataAlreadyAvailable {
			// The profile names the PSI that the S-CSCF asked for, with the
			// wildcarded PSI that it matched (B.2.1).
			a.UserData = cx.UserProfile{PrivateIdentity: p.PrivateIdentity, PublicIdentities: []string{publicID},
				WildcardedPSI: p.WildcardedPSI}.Marshal()
		}
		return a
	case cx.TimeoutDeregistration, cx.AdministrativeDeregistration:
		return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	}
	return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
}
