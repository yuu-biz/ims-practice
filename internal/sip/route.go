package sip

import (
	"fmt"
	"slices"
	"strings"
)

// This file holds the routing steps a proxy takes before it forwards a
// request: RFC 3261 16.4 and 16.6 steps 4 to 7. Every node in this
// project is a loose router, so every URI it puts in Route,
// Record-Route, Path (RFC 3327) or Service-Route (RFC 3608) carries lr.

// PopOwnRoute removes the top Route value if it names this proxy, that
// is, if its host and port are those of one of own (RFC 3261 16.4). It
// returns the URI it removed. An IMS proxy may tell its URIs apart by
// the user part, such as orig in the Service-Route of an S-CSCF, so the
// user part does not take part in the comparison.
//
// SIMPLIFIED: RFC 3261 16.4. The first two steps are left out: putting
// back a Request-URI that a strict router rewrote, and stripping a
// maddr parameter that names this proxy. No node in this project is a
// strict router or writes maddr.
func PopOwnRoute(req *Message, own ...URI) (URI, bool) {
	na, err := ParseNameAddr(req.Get("Route"))
	if err != nil {
		return URI{}, false
	}
	top, err := ParseURI(na.URI)
	if err != nil {
		return URI{}, false
	}
	for _, u := range own {
		if sameHostPort(top, u) {
			req.RemoveFirst("Route")
			return top, true
		}
	}
	return URI{}, false
}

func sameHostPort(a, b URI) bool {
	port := func(u URI) int {
		if u.Port == 0 {
			return defaultPort
		}
		return u.Port
	}
	return strings.EqualFold(a.Host, b.Host) && port(a) == port(b)
}

// NextHop is the URI whose address, port and transport the request goes
// to (RFC 3261 16.6 step 7): the top Route value if there is one,
// otherwise the Request-URI. The caller looks the address up as RFC
// 3263 says, which is what sipdns does.
//
// SIMPLIFIED: RFC 3261 16.6 step 6. A top Route without lr names a
// strict router, for which the proxy moves that URI into the
// Request-URI and the Request-URI to the end of Route. No node in this
// project is a strict router, so NextHop reports an error instead.
func NextHop(req *Message) (URI, error) {
	if route := req.Get("Route"); route != "" {
		na, err := ParseNameAddr(route)
		if err != nil {
			return URI{}, err
		}
		u, err := ParseURI(na.URI)
		if err != nil {
			return URI{}, err
		}
		if _, lr := u.Params.Get("lr"); !lr {
			return URI{}, fmt.Errorf("sip: Route %s names a strict router, which is not supported", na.URI)
		}
		return u, nil
	}
	return ParseURI(req.RequestURI)
}

// AddRecordRoute puts own on top of the Record-Route values, with lr
// added if it is missing, so that the later requests of the dialog come
// through this proxy (RFC 3261 16.6 step 4).
func AddRecordRoute(req *Message, own URI) {
	own.Params = slices.Clone(own.Params)
	own.Params.Set("lr", "")
	req.Prepend("Record-Route", NameAddr{URI: own.String()}.String())
}
