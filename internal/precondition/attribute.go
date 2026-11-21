// Package precondition models the quality-of-service preconditions of
// RFC 3312, as updated by RFC 4032: the a=curr, a=des and a=conf media
// attributes, and the status table each user agent keeps per media
// stream to decide when the session may go on (RFC 3312 5, 6 and 7).
//
// The package knows neither SIP nor the rest of SDP. Its caller moves
// the attributes in and out of the media description and tells the
// table when the access network has reserved resources.
package precondition

import (
	"fmt"
	"strings"
)

// QoS is the only precondition type this project uses (RFC 3312 4).
const QoS = "qos"

// Direction is a direction-tag (RFC 3312 4). Send and Recv are seen from
// the side that writes the SDP.
type Direction uint8

const (
	None     Direction = 0
	Send     Direction = 1
	Recv     Direction = 2
	SendRecv Direction = Send | Recv
)

var directionNames = map[Direction]string{None: "none", Send: "send", Recv: "recv", SendRecv: "sendrecv"}

func (d Direction) String() string { return directionNames[d] }

// invert is Table 4 of RFC 3312: what one side calls send, the other
// calls recv.
func (d Direction) invert() Direction {
	var out Direction
	if d&Send != 0 {
		out |= Recv
	}
	if d&Recv != 0 {
		out |= Send
	}
	return out
}

func parseDirection(s string) (Direction, error) {
	for d, name := range directionNames {
		if s == name {
			return d, nil
		}
	}
	return 0, fmt.Errorf("precondition: direction-tag %q", s)
}

// StatusType is a status-type (RFC 3312 4). EndToEnd covers the whole
// path; Local and Remote are the two access networks of the segmented
// status type, seen from the side that writes the SDP.
type StatusType string

const (
	EndToEnd StatusType = "e2e"
	Local    StatusType = "local"
	Remote   StatusType = "remote"
)

// invert is Table 4 of RFC 3312: the writer's local network is the
// reader's remote one.
func (s StatusType) invert() StatusType {
	switch s {
	case Local:
		return Remote
	case Remote:
		return Local
	}
	return s
}

func parseStatusType(s string) (StatusType, error) {
	switch t := StatusType(s); t {
	case EndToEnd, Local, Remote:
		return t, nil
	}
	return "", fmt.Errorf("precondition: status-type %q", s)
}

// Strength is a strength-tag (RFC 3312 4). StrengthNone, Optional and
// Mandatory are ordered, from the lowest to the highest (RFC 3312 5.2).
// Failure and Unknown only appear in an SDP that refuses an offer
// (RFC 3312 8 and 9).
type Strength int

const (
	StrengthNone Strength = iota
	Optional
	Mandatory
	Failure
	Unknown
)

var strengthNames = []string{"none", "optional", "mandatory", "failure", "unknown"}

func (s Strength) String() string { return strengthNames[s] }

func parseStrength(s string) (Strength, error) {
	for i, name := range strengthNames {
		if s == name {
			return Strength(i), nil
		}
	}
	return 0, fmt.Errorf("precondition: strength-tag %q", s)
}

// Current is the value of an a=curr attribute: the current status of
// the resources of one segment (RFC 3312 4).
type Current struct {
	Type      string
	Status    StatusType
	Direction Direction
}

// Desired is the value of an a=des attribute: the precondition itself
// (RFC 3312 4).
type Desired struct {
	Type      string
	Strength  Strength
	Status    StatusType
	Direction Direction
}

// Confirm is the value of an a=conf attribute: the writer asks to be
// told once the status reaches this threshold (RFC 3312 7).
type Confirm struct {
	Type      string
	Status    StatusType
	Direction Direction
}

func (c Current) String() string { return fmt.Sprintf("%s %s %s", c.Type, c.Status, c.Direction) }

func (d Desired) String() string {
	return fmt.Sprintf("%s %s %s %s", d.Type, d.Strength, d.Status, d.Direction)
}

func (c Confirm) String() string { return fmt.Sprintf("%s %s %s", c.Type, c.Status, c.Direction) }

// ParseCurrent reads the value of an a=curr attribute, such as
// "qos local none".
func ParseCurrent(v string) (Current, error) {
	f := strings.Fields(v)
	if len(f) != 3 {
		return Current{}, fmt.Errorf("precondition: curr:%s is not <type> <status-type> <direction-tag>", v)
	}
	status, err := parseStatusType(f[1])
	if err != nil {
		return Current{}, err
	}
	dir, err := parseDirection(f[2])
	if err != nil {
		return Current{}, err
	}
	return Current{Type: f[0], Status: status, Direction: dir}, nil
}

// ParseDesired reads the value of an a=des attribute, such as
// "qos mandatory local sendrecv".
func ParseDesired(v string) (Desired, error) {
	f := strings.Fields(v)
	if len(f) != 4 {
		return Desired{}, fmt.Errorf("precondition: des:%s is not <type> <strength-tag> <status-type> <direction-tag>", v)
	}
	strength, err := parseStrength(f[1])
	if err != nil {
		return Desired{}, err
	}
	status, err := parseStatusType(f[2])
	if err != nil {
		return Desired{}, err
	}
	dir, err := parseDirection(f[3])
	if err != nil {
		return Desired{}, err
	}
	return Desired{Type: f[0], Strength: strength, Status: status, Direction: dir}, nil
}

// ParseConfirm reads the value of an a=conf attribute, such as
// "qos remote sendrecv".
func ParseConfirm(v string) (Confirm, error) {
	c, err := ParseCurrent(v)
	if err != nil {
		return Confirm{}, fmt.Errorf("precondition: conf:%s is not <type> <status-type> <direction-tag>", v)
	}
	return Confirm(c), nil
}

// Attributes are the precondition attributes of one media description,
// in the order they are written: a=curr, then a=des, then a=conf.
type Attributes struct {
	Current []Current
	Desired []Desired
	Confirm []Confirm
}

// Empty tells that the media description carries no preconditions.
func (a Attributes) Empty() bool {
	return len(a.Current) == 0 && len(a.Desired) == 0 && len(a.Confirm) == 0
}
