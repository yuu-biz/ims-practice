package sip

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// URI is a SIP or SIPS URI (RFC 3261 19.1.1):
//
//	sip:user@host:port;uri-parameters?headers
//
// Other schemes, such as the tel URI of RFC 3966, are kept as strings
// in NameAddr.URI and Message.RequestURI, and ParseURI rejects them.
type URI struct {
	Scheme string // "sip" or "sips", lower case
	// User is the userinfo before "@", escapes kept. It may hold ";"
	// (RFC 3261 19.1.3), and a password stays part of it, as RFC 3261
	// 19.1.1 allows.
	User    string
	Host    string // an IPv6 address appears without its brackets
	Port    int    // 0 when the URI names no port
	Params  Params
	Headers string // the part after "?", as written
}

// ErrNotSIPURI reports a URI whose scheme is neither sip nor sips.
var ErrNotSIPURI = errors.New("sip: not a sip or sips URI")

func ParseURI(s string) (URI, error) {
	scheme, rest, ok := strings.Cut(s, ":")
	scheme = strings.ToLower(scheme)
	if !ok || (scheme != "sip" && scheme != "sips") {
		return URI{}, fmt.Errorf("%w: %q", ErrNotSIPURI, s)
	}
	u := URI{Scheme: scheme}
	// "@" may not appear unescaped in the userinfo, the parameters or
	// the headers, so the last one ends the userinfo. The userinfo is cut
	// off first because a user may hold "?" (user-unreserved in RFC 3261
	// 25.1); only a "?" after the host starts the headers.
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		u.User = rest[:at]
		rest = rest[at+1:]
	}
	rest, u.Headers, _ = strings.Cut(rest, "?")
	hostport, params, _ := strings.Cut(rest, ";")
	u.Params = parseParams(params)
	var err error
	if u.Host, u.Port, err = splitHostPort(hostport); err != nil {
		return URI{}, fmt.Errorf("sip: URI %q: %w", s, err)
	}
	return u, nil
}

func (u URI) String() string {
	var b strings.Builder
	b.WriteString(u.Scheme)
	b.WriteString(":")
	if u.User != "" {
		b.WriteString(u.User)
		b.WriteString("@")
	}
	b.WriteString(joinHostPort(u.Host, u.Port))
	b.WriteString(u.Params.String())
	if u.Headers != "" {
		b.WriteString("?")
		b.WriteString(u.Headers)
	}
	return b.String()
}

// splitHostPort reads hostport = host [ ":" port ] (RFC 3261 25.1),
// where an IPv6 host is written in brackets.
func splitHostPort(hostport string) (host string, port int, err error) {
	if hostport == "" {
		return "", 0, errors.New("empty host")
	}
	portText := ""
	if strings.HasPrefix(hostport, "[") {
		end := strings.IndexByte(hostport, ']')
		if end < 0 {
			return "", 0, errors.New("unterminated IPv6 reference")
		}
		host = hostport[1:end]
		after := hostport[end+1:]
		if after != "" {
			var ok bool
			if portText, ok = strings.CutPrefix(after, ":"); !ok {
				return "", 0, fmt.Errorf("unexpected %q after IPv6 reference", after)
			}
		}
	} else {
		host, portText, _ = strings.Cut(hostport, ":")
	}
	if host == "" {
		return "", 0, errors.New("empty host")
	}
	if portText == "" {
		return host, 0, nil
	}
	port, err = strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("malformed port %q", portText)
	}
	return host, port, nil
}

// joinHostPort writes host and, when it is not 0, port. An IPv6 address
// gets its brackets back.
func joinHostPort(host string, port int) string {
	if port == 0 {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
