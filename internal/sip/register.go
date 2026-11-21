package sip

import (
	"errors"
	"strconv"
	"strings"
)

// NoExpires is what RequestedExpires returns when a REGISTER asks for no
// interval, which leaves the choice to the registrar (RFC 3261 10.3
// step 7).
const NoExpires = -1

// RequestedExpires reads the interval a REGISTER asks for, in seconds:
// the expires parameter of the Contact, or else the Expires header (RFC
// 3261 10.2.1.1). wildcard reports "Contact: *", which RFC 3261 10.3
// step 6 allows only with Expires 0.
func RequestedExpires(req *Message) (seconds int, wildcard bool, err error) {
	contact := strings.TrimSpace(req.Get("Contact"))
	if contact == "*" {
		wildcard = true
	} else if contact != "" {
		na, err := ParseNameAddr(contact)
		if err != nil {
			return 0, false, err
		}
		if v, ok := na.Params.Get("expires"); ok {
			n, err := ParseDeltaSeconds(v)
			return int(n), false, err
		}
	}
	if v := req.Get("Expires"); v != "" {
		n, err := ParseDeltaSeconds(v)
		if err == nil && wildcard && n != 0 {
			err = errors.New("sip: Contact: * with a non-zero Expires")
		}
		return int(n), wildcard, err
	}
	if wildcard {
		return 0, false, errors.New("sip: Contact: * without Expires")
	}
	return NoExpires, false, nil
}

// ErrTooManyHops means Max-Forwards reached zero (RFC 3261 16.3 step 3).
var ErrTooManyHops = errors.New("sip: Max-Forwards is zero")

// DecrementMaxForwards is RFC 3261 16.6 step 3. A request without
// Max-Forwards gets 70 (RFC 3261 8.1.1.6) less one.
func DecrementMaxForwards(m *Message) error {
	n := 70
	if v := m.Get("Max-Forwards"); v != "" {
		var err error
		if n, err = ParseMaxForwards(v); err != nil {
			return err
		}
	}
	if n <= 0 {
		return ErrTooManyHops
	}
	m.Set("Max-Forwards", strconv.Itoa(n-1))
	return nil
}
