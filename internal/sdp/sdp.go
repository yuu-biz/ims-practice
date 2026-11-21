// Package sdp reads and writes the Session Description Protocol (RFC
// 8866) and makes offers and answers (RFC 3264). Calls in this project
// carry one audio stream, so the package covers what such a session
// needs and keeps every other line as it was received.
package sdp

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// Session is one session description.
type Session struct {
	Origin Origin
	Name   string
	// Connection is the session-level "c=" address, used by every media
	// description that has none of its own (RFC 8866 5.7).
	Connection netip.Addr
	// Lines holds the other session-level lines, such as "t=" and "a=",
	// in order, as "x=value".
	Lines []string
	Media []Media
}

// Origin is the "o=" line (RFC 8866 5.2). The version goes up by one
// with each new offer or answer from the same side (RFC 3264 8).
type Origin struct {
	Username  string
	SessionID string
	Version   uint64
	Address   netip.Addr
}

// Media is one media description (RFC 8866 5.14).
type Media struct {
	Type       string // "audio"
	Port       int    // 0 rejects the stream (RFC 3264 6)
	Proto      string // "RTP/AVP"
	Formats    []int  // RTP payload types, most preferred first
	Connection netip.Addr
	// Bandwidths are the "b=" lines of the media description (RFC 8866
	// 5.8).
	Bandwidths []Bandwidth
	Attributes []Attribute
}

// Bandwidth is a "b=" line. AS is in kbit/s (RFC 8866 5.8); RS and RR,
// the RTCP bandwidth of senders and of receivers, are in bit/s (RFC
// 3556 2).
type Bandwidth struct {
	Type  string
	Value int
}

// Bandwidth returns the value of the "b=" line of this type.
func (m Media) Bandwidth(bwtype string) (int, bool) {
	for _, b := range m.Bandwidths {
		if b.Type == bwtype {
			return b.Value, true
		}
	}
	return 0, false
}

// SetBandwidth sets the "b=" line of this type, adding it if needed.
func (m *Media) SetBandwidth(bwtype string, value int) {
	for i, b := range m.Bandwidths {
		if b.Type == bwtype {
			m.Bandwidths[i].Value = value
			return
		}
	}
	m.Bandwidths = append(m.Bandwidths, Bandwidth{Type: bwtype, Value: value})
}

// Attribute is an "a=" line (RFC 8866 5.13). A property attribute has
// no value.
type Attribute struct {
	Name  string
	Value string
}

func (a Attribute) String() string {
	if a.Value == "" {
		return a.Name
	}
	return a.Name + ":" + a.Value
}

// Get returns the value of the first attribute with this name.
func (m Media) Get(name string) (string, bool) {
	for _, a := range m.Attributes {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Address is where to send the stream: its own connection address, or
// the session's.
func (s Session) Address(m Media) netip.AddrPort {
	addr := m.Connection
	if !addr.IsValid() {
		addr = s.Connection
	}
	return netip.AddrPortFrom(addr, uint16(m.Port))
}

// Parse reads a session description.
func Parse(b []byte) (Session, error) {
	var s Session
	var media *Media
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || len(key) != 1 {
			return Session{}, fmt.Errorf("sdp: line %d %q is not <type>=<value>", i+1, line)
		}
		var err error
		switch {
		case key == "v":
			if value != "0" {
				return Session{}, fmt.Errorf("sdp: version %q, want 0 (RFC 8866 5.1)", value)
			}
		case key == "o":
			s.Origin, err = parseOrigin(value)
		case key == "s":
			s.Name = value
		case key == "m":
			s.Media = append(s.Media, Media{})
			media = &s.Media[len(s.Media)-1]
			err = media.parseMediaLine(value)
		case key == "c" && media != nil:
			media.Connection, err = parseConnection(value)
		case key == "c":
			s.Connection, err = parseConnection(value)
		case key == "b" && media != nil:
			var bw Bandwidth
			bw, err = parseBandwidth(value)
			media.Bandwidths = append(media.Bandwidths, bw)
		case key == "a" && media != nil:
			name, val, _ := strings.Cut(value, ":")
			media.Attributes = append(media.Attributes, Attribute{Name: name, Value: val})
		case media == nil:
			s.Lines = append(s.Lines, line)
		}
		if err != nil {
			return Session{}, fmt.Errorf("sdp: line %d: %w", i+1, err)
		}
	}
	if !s.Origin.Address.IsValid() {
		return Session{}, errors.New("sdp: no o= line")
	}
	return s, nil
}

func parseOrigin(v string) (Origin, error) {
	f := strings.Fields(v)
	if len(f) != 6 || f[3] != "IN" || f[4] != "IP4" {
		return Origin{}, fmt.Errorf("o=%s is not <username> <sess-id> <sess-version> IN IP4 <address>", v)
	}
	version, err := strconv.ParseUint(f[2], 10, 64)
	if err != nil {
		return Origin{}, fmt.Errorf("o= version: %w", err)
	}
	addr, err := netip.ParseAddr(f[5])
	if err != nil {
		return Origin{}, fmt.Errorf("o= address: %w", err)
	}
	return Origin{Username: f[0], SessionID: f[1], Version: version, Address: addr}, nil
}

// SIMPLIFIED: RFC 8866 5.7. Only IPv4 unicast addresses are accepted in
// "c=": every node of this project uses IPv4, and FQDNs or multicast
// would only add cases nothing exercises.
func parseConnection(v string) (netip.Addr, error) {
	f := strings.Fields(v)
	if len(f) != 3 || f[0] != "IN" || f[1] != "IP4" {
		return netip.Addr{}, fmt.Errorf("c=%s is not IN IP4 <address>", v)
	}
	return netip.ParseAddr(f[2])
}

func parseBandwidth(v string) (Bandwidth, error) {
	bwtype, value, ok := strings.Cut(v, ":")
	n, err := strconv.Atoi(value)
	if !ok || err != nil || n < 0 {
		return Bandwidth{}, fmt.Errorf("b=%s is not <bwtype>:<bandwidth>", v)
	}
	return Bandwidth{Type: bwtype, Value: n}, nil
}

func (m *Media) parseMediaLine(v string) error {
	f := strings.Fields(v)
	if len(f) < 4 {
		return fmt.Errorf("m=%s is not <media> <port> <proto> <fmt> ...", v)
	}
	port, err := strconv.Atoi(f[1])
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("m= port %q", f[1])
	}
	m.Type, m.Port, m.Proto = f[0], port, f[2]
	for _, pt := range f[3:] {
		n, err := strconv.Atoi(pt)
		if err != nil {
			return fmt.Errorf("m= format %q is not an RTP payload type", pt)
		}
		m.Formats = append(m.Formats, n)
	}
	return nil
}

// Bytes writes the description in the order of RFC 8866 5, with CRLF.
func (s Session) Bytes() []byte {
	var b bytes.Buffer
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\r\n", a...) }
	line("v=0")
	line("o=%s %s %d IN IP4 %s", s.Origin.Username, s.Origin.SessionID, s.Origin.Version, s.Origin.Address)
	name := s.Name
	if name == "" {
		name = "-" // RFC 8866 5.3: s= must not be empty
	}
	line("s=%s", name)
	if s.Connection.IsValid() {
		line("c=IN IP4 %s", s.Connection)
	}
	hasTime := slices.ContainsFunc(s.Lines, func(l string) bool { return strings.HasPrefix(l, "t=") })
	if !hasTime {
		line("t=0 0") // RFC 8866 5.9: an unbounded session
	}
	for _, l := range s.Lines {
		line("%s", l)
	}
	for _, m := range s.Media {
		formats := make([]string, len(m.Formats))
		for i, f := range m.Formats {
			formats[i] = strconv.Itoa(f)
		}
		line("m=%s %d %s %s", m.Type, m.Port, m.Proto, strings.Join(formats, " "))
		if m.Connection.IsValid() {
			line("c=IN IP4 %s", m.Connection)
		}
		for _, bw := range m.Bandwidths {
			line("b=%s:%d", bw.Type, bw.Value)
		}
		for _, a := range m.Attributes {
			line("a=%s", a)
		}
	}
	return b.Bytes()
}
