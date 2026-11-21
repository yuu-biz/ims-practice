package h248

import (
	"fmt"
	"strings"
)

// ServiceChangeParm holds the parameters of a ServiceChange request
// (RFC 3525 7.2.8). Which methods and reasons each side of an
// interface sends is in TS 29.334 5.8.8, TS 29.238 5.8.8 and TS 29.333
// 5.8.8.
//
// The ServiceChangeAddress and ServiceChangeDelay parameters are left
// out: no profile uses them (TS 29.334 5.8.8, Tables 5.8.8.3 and
// 5.8.8.4).
type ServiceChangeParm struct {
	Method ServiceChangeMethod
	// Reason is a reason code, a space and a text, such as
	// "901 Cold Boot".
	Reason string
	// Profile names the profile that the media gateway follows. It
	// is required when a media gateway registers (TS 29.334 5.8.8,
	// Table 5.8.8.7).
	Profile *ServiceChangeProfile
	// Version is the version of H.248.1 that the sender supports
	// (RFC 3525 11.3). Zero leaves it out.
	Version int
	// MgcID ("MgcIdToTry") names another controller that the media
	// gateway is to register with.
	MgcID *MId
	// TimeStamp is the time of the sender. The zero value leaves it
	// out.
	TimeStamp TimeNotation
}

// ServiceChangeResParm holds the parameters of the reply to a
// ServiceChange.
type ServiceChangeResParm struct {
	// MgcID sends the media gateway to another controller.
	MgcID *MId
	// Profile is the profile that the controller agrees to.
	Profile *ServiceChangeProfile
	// Version is the version to use from now on. The controller sends
	// it when it supports a lower version than the media gateway
	// offered (RFC 3525 11.3). Zero leaves it out.
	Version   int
	TimeStamp TimeNotation
}

// ServiceChangeProfile names a profile and its version, such as
// threegIq version 7 (TS 29.334 5.1).
type ServiceChangeProfile struct {
	ProfileName string
	Version     int
}

// ServiceChangeMethod says what kind of change a ServiceChange
// reports.
type ServiceChangeMethod string

const (
	// Graceful: the terminations will go out of service. Connections
	// that exist are left alone for now, and the controller should
	// set up no new ones.
	Graceful ServiceChangeMethod = "Graceful"
	// Forced: the terminations went out of service abruptly, and
	// their connections may be lost.
	Forced ServiceChangeMethod = "Forced"
	// Restart: service will be restored. With the ROOT termination,
	// this is how a media gateway registers.
	Restart ServiceChangeMethod = "Restart"
	// Disconnected: the media gateway lost contact with its
	// controller and has found the same controller again.
	Disconnected ServiceChangeMethod = "Disconnected"
	// HandOff: sent by the controller, it is going out of service and
	// the media gateway is to register elsewhere. Sent by the media
	// gateway, it registers because of such a hand-over.
	HandOff ServiceChangeMethod = "HandOff"
	// Failover: a standby media gateway takes over, or the media
	// gateway found that its controller has failed. Of the three
	// profiles only Mp names it (TS 29.333 5.8.8, Table 5.8.8.2).
	Failover ServiceChangeMethod = "Failover"
)

// serviceChangeParm reads what follows the Services token, as it
// stands in a request: "{ serviceChangeParm, ... }". The method and
// the reason are required there (serviceChangeDescriptor in RFC 3525
// Annex B.2).
func (s *scanner) serviceChangeParm() (*ServiceChangeParm, error) {
	p, err := s.servicesParms()
	if err != nil {
		return nil, err
	}
	if p.Method == "" || p.Reason == "" {
		return nil, s.errorf("a %s request carries both %s and %s", ServiceChange, tokMethod, tokReason)
	}
	return p, nil
}

// servicesParms reads the parameters of a Services descriptor, in a
// request or in a reply, without checking which of them are there.
func (s *scanner) servicesParms() (*ServiceChangeParm, error) {
	p := &ServiceChangeParm{}
	err := s.list(func() error {
		s.skipLWSP()
		if isDigit(s.peek()) {
			if p.TimeStamp != (TimeNotation{}) {
				return s.errorf("the time stamp appears twice in a %s descriptor", tokServices)
			}
			var err error
			p.TimeStamp, err = s.timeNotation()
			return err
		}
		word := s.word()
		if word == "" {
			return s.errorf("expected a parameter of the %s descriptor, found %s", tokServices, s.found())
		}
		name, ok := longToken(word)
		if !ok {
			// An extension parameter ("X-...") or, from version 2
			// on, an audit item that tells which capability changed.
			return s.errorf("%q in a %s descriptor is not supported", word, tokServices)
		}
		if err := s.expect('='); err != nil {
			return err
		}
		var err error
		switch {
		case name == tokMethod && p.Method == "":
			p.Method, err = s.serviceChangeMethod()
		case name == tokReason && p.Reason == "":
			p.Reason, err = s.serviceChangeReason()
		case name == tokProfile && p.Profile == nil:
			p.Profile, err = s.serviceChangeProfile()
		case name == tokVersion && p.Version == 0:
			p.Version, err = s.version()
		case name == tokMgcIDToTry && p.MgcID == nil:
			var id MId
			id, err = s.mId()
			p.MgcID = &id
		case name == tokMethod || name == tokReason || name == tokProfile || name == tokVersion || name == tokMgcIDToTry:
			return s.errorf("%s appears twice in a %s descriptor", name, tokServices)
		default:
			return s.errorf("%s in a %s descriptor is not supported", name, tokServices)
		}
		return err
	})
	return p, err
}

// serviceChangeResParm reads the Services descriptor of a reply, which
// has neither a method nor a reason.
func (s *scanner) serviceChangeResParm() (*ServiceChangeResParm, error) {
	p, err := s.servicesParms()
	if err != nil {
		return nil, err
	}
	if p.Method != "" || p.Reason != "" {
		return nil, s.errorf("the reply to a %s carries neither %s nor %s", ServiceChange, tokMethod, tokReason)
	}
	return &ServiceChangeResParm{MgcID: p.MgcID, Profile: p.Profile, Version: p.Version, TimeStamp: p.TimeStamp}, nil
}

func (s *scanner) serviceChangeMethod() (ServiceChangeMethod, error) {
	name, err := s.token()
	if err != nil {
		return "", err
	}
	if !isServiceChangeMethod(name) {
		return "", s.errorf("%s is not a method of %s", name, ServiceChange)
	}
	return ServiceChangeMethod(name), nil
}

func isServiceChangeMethod(name string) bool {
	switch ServiceChangeMethod(name) {
	case Graceful, Forced, Restart, Disconnected, HandOff, Failover:
		return true
	}
	return false
}

func (s *scanner) serviceChangeReason() (string, error) {
	v, err := s.value()
	if err != nil {
		return "", err
	}
	if !validServiceChangeReason(v.Text) {
		return "", s.errorf("the reason %q does not start with a reason code", v.Text)
	}
	return v.Text, nil
}

// validServiceChangeReason follows the comment on serviceChangeReason
// in the grammar: a decimal reason code, which may be followed by a
// space and a text.
func validServiceChangeReason(reason string) bool {
	code, _, _ := strings.Cut(reason, " ")
	return code != "" && strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func (s *scanner) serviceChangeProfile() (*ServiceChangeProfile, error) {
	p := &ServiceChangeProfile{ProfileName: s.word()}
	if !validName(p.ProfileName) {
		return nil, s.errorf("expected the name of a profile, found %s", s.found())
	}
	if s.peek() != '/' {
		return nil, s.errorf("expected \"/\" and the version of the profile, found %s", s.found())
	}
	s.pos++
	var err error
	p.Version, err = s.version()
	return p, err
}

// version reads a Version: one or two digits.
func (s *scanner) version() (int, error) {
	version, err := s.uint(7)
	if err != nil {
		return 0, err
	}
	if version > 99 {
		return 0, s.errorf("version %d has more than two digits", version)
	}
	return int(version), nil
}

func (w *writer) serviceChangeParm(p *ServiceChangeParm) {
	if !isServiceChangeMethod(string(p.Method)) {
		w.fail("%q is not a method of %s", string(p.Method), ServiceChange)
	}
	if !validServiceChangeReason(p.Reason) {
		w.fail("the reason %q does not start with a reason code", p.Reason)
	}
	parms := []string{
		fmt.Sprintf("%s = %s", tokMethod, p.Method),
		fmt.Sprintf("%s = %s", tokReason, w.quoted(p.Reason)),
	}
	parms = append(parms, w.serviceChangeCommonParms(p.Profile, p.Version, p.MgcID, p.TimeStamp)...)
	w.parmBlock(tokServices, parms)
}

func (w *writer) serviceChangeResParm(p *ServiceChangeResParm) {
	w.parmBlock(tokServices, w.serviceChangeCommonParms(p.Profile, p.Version, p.MgcID, p.TimeStamp))
}

// serviceChangeCommonParms returns the parameters that a request and a
// reply share.
func (w *writer) serviceChangeCommonParms(profile *ServiceChangeProfile, version int, mgcID *MId, timeStamp TimeNotation) []string {
	var parms []string
	if profile != nil {
		if !validName(profile.ProfileName) || profile.Version < 0 || profile.Version > 99 {
			w.fail("%s/%d is not a profile and its version", profile.ProfileName, profile.Version)
		}
		parms = append(parms, fmt.Sprintf("%s = %s/%d", tokProfile, profile.ProfileName, profile.Version))
	}
	if version != 0 {
		if version < 0 || version > 99 {
			w.fail("version %d is not between 1 and 99", version)
		}
		parms = append(parms, fmt.Sprintf("%s = %d", tokVersion, version))
	}
	if mgcID != nil {
		parms = append(parms, fmt.Sprintf("%s = %s", tokMgcIDToTry, w.mId(*mgcID)))
	}
	if timeStamp != (TimeNotation{}) {
		parms = append(parms, w.timeNotation(timeStamp))
	}
	return parms
}
