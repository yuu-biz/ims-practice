package sdp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Encoding names of a=rtpmap used in this project.
const (
	NameAMRWB          = "AMR-WB"          // RFC 4867 8.2
	NameAMR            = "AMR"             // RFC 4867 8.1
	NamePCMU           = "PCMU"            // RFC 3551 4.5.14
	NameTelephoneEvent = "telephone-event" // RFC 4733 7.1.1
)

// Codec is one RTP payload format of a media description: a payload
// type with its a=rtpmap and a=fmtp lines (RFC 8866 6.6 and 6.15).
type Codec struct {
	PayloadType int    `json:"payload_type"`
	Name        string `json:"name"` // the encoding name, such as "AMR-WB"
	ClockRate   int    `json:"clock_rate"`
	// Channels is the encoding parameter of a=rtpmap, or 0 when the line
	// leaves it out, which means one channel.
	Channels int `json:"channels,omitempty"`
	// Fmtp is the value of a=fmtp after the payload type, or "".
	Fmtp string `json:"fmtp,omitempty"`
}

// Rtpmap is the value of the a=rtpmap line.
func (c Codec) Rtpmap() string {
	v := fmt.Sprintf("%d %s/%d", c.PayloadType, c.Name, c.ClockRate)
	if c.Channels != 0 {
		v += "/" + strconv.Itoa(c.Channels)
	}
	return v
}

// Param returns a parameter of an a=fmtp value written as a list of
// name=value pairs separated by semicolons, as AMR does (RFC 4867
// 8.3). A parameter without "=" has the value "".
func (c Codec) Param(name string) (string, bool) {
	for _, p := range strings.Split(c.Fmtp, ";") {
		n, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		if strings.EqualFold(n, name) {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// HasEvent tells that the telephone-event c lists event among the events
// it supports. The a=fmtp value is a comma-separated list of single event
// numbers and ranges such as "0-15,66,70", and the union of them is the
// set supported. With no list, events 0-15 are assumed (RFC 4733 7.1.1).
// A sender restricts itself to the events the receiver listed (RFC 4733
// 2.5.1.1). An element that is no number or range lists no event.
func (c Codec) HasEvent(event int) bool {
	if strings.TrimSpace(c.Fmtp) == "" {
		return event >= 0 && event <= 15
	}
	for _, e := range strings.Split(c.Fmtp, ",") {
		first, last, isRange := strings.Cut(strings.TrimSpace(e), "-")
		lo, err := strconv.Atoi(first)
		if err != nil {
			continue
		}
		hi := lo
		if isRange {
			if hi, err = strconv.Atoi(last); err != nil {
				continue
			}
		}
		if lo <= event && event <= hi {
			return true
		}
	}
	return false
}

// Is tells that c has this encoding name; names are case-insensitive
// (RFC 4855 3), so a peer may write "pcmu" for PCMU.
func (c Codec) Is(name string) bool { return strings.EqualFold(c.Name, name) }

// staticCodecs are the static payload types of RFC 3551 Table 4 that a
// media description may use without a=rtpmap.
var staticCodecs = map[int]Codec{
	0: {PayloadType: 0, Name: NamePCMU, ClockRate: 8000},
	8: {PayloadType: 8, Name: "PCMA", ClockRate: 8000},
}

// Codecs lists the payload formats of m in the order of the "m=" line.
// A payload type without a=rtpmap is known only if it is static.
func (m Media) Codecs() []Codec {
	var out []Codec
	for _, pt := range m.Formats {
		c, ok := staticCodecs[pt]
		if v, found := m.formatAttribute("rtpmap", pt); found {
			if parsed, err := parseRtpmap(pt, v); err == nil {
				c, ok = parsed, true
			}
		}
		if !ok {
			c = Codec{PayloadType: pt}
		}
		c.Fmtp, _ = m.formatAttribute("fmtp", pt)
		out = append(out, c)
	}
	return out
}

// formatAttribute returns the value of a=<name>:<pt> <value>.
func (m Media) formatAttribute(name string, pt int) (string, bool) {
	prefix := strconv.Itoa(pt) + " "
	for _, a := range m.Attributes {
		if a.Name == name {
			if v, ok := strings.CutPrefix(a.Value, prefix); ok {
				return strings.TrimSpace(v), true
			}
		}
	}
	return "", false
}

func parseRtpmap(pt int, v string) (Codec, error) {
	f := strings.Split(v, "/")
	if len(f) < 2 || len(f) > 3 {
		return Codec{}, fmt.Errorf("rtpmap:%d %s is not <encoding name>/<clock rate>[/<channels>]", pt, v)
	}
	rate, err := strconv.Atoi(f[1])
	if err != nil {
		return Codec{}, err
	}
	c := Codec{PayloadType: pt, Name: f[0], ClockRate: rate}
	if len(f) == 3 {
		if c.Channels, err = strconv.Atoi(f[2]); err != nil {
			return Codec{}, err
		}
	}
	return c, nil
}

// SetCodecs replaces the payload formats of m with codecs: the "m="
// line, and the a=rtpmap and a=fmtp lines, which come first among the
// attributes.
func (m *Media) SetCodecs(codecs []Codec) {
	m.Formats = nil
	attrs := []Attribute{}
	for _, c := range codecs {
		m.Formats = append(m.Formats, c.PayloadType)
		attrs = append(attrs, Attribute{Name: "rtpmap", Value: c.Rtpmap()})
		if c.Fmtp != "" {
			attrs = append(attrs, Attribute{Name: "fmtp", Value: strconv.Itoa(c.PayloadType) + " " + c.Fmtp})
		}
	}
	for _, a := range m.Attributes {
		if a.Name != "rtpmap" && a.Name != "fmtp" {
			attrs = append(attrs, a)
		}
	}
	m.Attributes = attrs
}

// Codec returns the payload format of payload type pt.
func (m Media) Codec(pt int) (Codec, bool) {
	cs := m.Codecs()
	i := slices.IndexFunc(cs, func(c Codec) bool { return c.PayloadType == pt })
	if i < 0 || cs[i].Name == "" {
		return Codec{}, false
	}
	return cs[i], true
}
