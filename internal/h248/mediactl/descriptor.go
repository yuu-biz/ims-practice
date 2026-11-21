package mediactl

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// The lines of the session descriptions in Local and Remote
// descriptors. The text encoding carries SDP there, with three
// exceptions for what a controller sends: o=, s= and t= may be left
// out, CHOOSE ("$") may stand for a value, and a value may be a list
// of alternatives (H.248.1 (05/2002) 7.1.8). TS 29.334 5.15 makes v=,
// c=, m= and b= the lines that both sides must support, and lets the
// address in c= and the port in m= be CHOOSE.
const (
	choose      = "$"
	mediaAudio  = "audio"
	protoRTPAVP = "RTP/AVP"
	// noFormat stands for the format list when no resources for a
	// particular format are to be reserved (TS 29.334 5.15, Table
	// 5.15.1).
	noFormat = "-"
)

// sessionDescription is one session description of a Local or Remote
// descriptor, as far as this package reads and writes it.
type sessionDescription struct {
	// address is the RTP address and port of the stream (c= and the
	// port of m=), and media the formats and the bandwidth.
	address netip.AddrPort
	media   agw.Media
	// chooseAddress and choosePort tell that the address or the port
	// was CHOOSE.
	chooseAddress bool
	choosePort    bool
	// mediaType and proto are the first and the third field of m=.
	mediaType string
	proto     string
	// origin, name and timing are the o=, s= and t= lines without
	// their "x=", or "" when they were left out.
	origin, name, timing string
}

// controllerSDP is the session description that a controller sends:
// the lines that TS 29.334 5.15 requires, without o=, s= and t=, and
// CHOOSE for the address and port when address is the zero value, for
// the gateway to choose them.
func controllerSDP(address netip.AddrPort, media agw.Media) string {
	return writeSDP(sessionDescription{
		address:       address,
		media:         media,
		chooseAddress: !address.IsValid(),
		choosePort:    !address.IsValid(),
		mediaType:     mediaAudio,
		proto:         protoRTPAVP,
	})
}

// writeSDP writes the lines in the order of RFC 8866 5. Each line ends
// with a line feed, so that the "}" of the descriptor follows the last
// one right away: tshark warns of an empty SDP line otherwise.
func writeSDP(d sessionDescription) string {
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line("v=0")
	if d.origin != "" {
		line("o=%s", d.origin)
	}
	if d.name != "" {
		line("s=%s", d.name)
	}
	addrType, addr := "IP4", choose
	if !d.chooseAddress {
		addr = d.address.Addr().String()
		if d.address.Addr().Is6() {
			addrType = "IP6"
		}
	}
	line("c=IN %s %s", addrType, addr)
	if d.timing != "" {
		line("t=%s", d.timing)
	}
	port := choose
	if !d.choosePort {
		port = strconv.Itoa(int(d.address.Port()))
	}
	formats := noFormat
	if len(d.media.Codecs) > 0 {
		pts := make([]string, len(d.media.Codecs))
		for i, c := range d.media.Codecs {
			pts[i] = strconv.Itoa(c.PayloadType)
		}
		formats = strings.Join(pts, " ")
	}
	line("m=%s %s %s %s", d.mediaType, port, d.proto, formats)
	if d.media.Bandwidth > 0 {
		line("b=AS:%d", d.media.Bandwidth)
	}
	for _, c := range d.media.Codecs {
		if c.Name != "" {
			line("a=rtpmap:%s", c.Rtpmap())
		}
		if c.Fmtp != "" {
			line("a=fmtp:%d %s", c.PayloadType, c.Fmtp)
		}
	}
	return b.String()
}

// gatewayLocal is the Local descriptor of the reply to an Add: the one
// the controller sent, with the address and port the relay chose. TS
// 29.334 5.15 (Table 5.15.1) has the gateway return the o=, s= and t=
// lines it was sent, and fill in those it was not.
func gatewayLocal(local sessionDescription, address netip.AddrPort, id h248.ContextID) string {
	local.address = address
	local.chooseAddress, local.choosePort = false, false
	if local.origin == "" {
		addrType := "IP4"
		if address.Addr().Is6() {
			addrType = "IP6"
		}
		local.origin = fmt.Sprintf("- %d 0 IN %s %s", id, addrType, address.Addr())
	}
	if local.name == "" {
		local.name = "-"
	}
	if local.timing == "" {
		local.timing = "0 0"
	}
	return writeSDP(local)
}

// errAlternatives is returned for a descriptor that offers more than
// one session description or more than one value.
var errAlternatives = errors.New("alternatives are not supported")

// parseSDP reads the session description of a Local or Remote
// descriptor.
//
// SIMPLIFIED: H.248.1 (05/2002) 7.1.8 lets a controller offer
// alternatives, as several session descriptions in one descriptor or
// as several values in one field, for the gateway to choose from. The
// controllers of this project offer one, so alternatives are refused.
func parseSDP(text string) (sessionDescription, error) {
	var d sessionDescription
	var media sdp.Media
	versions := 0
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		key, value, ok := strings.Cut(l, "=")
		if !ok || len(key) != 1 {
			return d, fmt.Errorf("%q is not an SDP line", l)
		}
		switch key {
		case "v":
			if versions++; versions > 1 {
				return d, errAlternatives
			}
			if value != "0" {
				return d, fmt.Errorf("v=%s, want v=0", value)
			}
		case "o":
			d.origin = value
		case "s":
			d.name = value
		case "t":
			d.timing = value
		case "c":
			if err := d.parseConnection(value); err != nil {
				return d, err
			}
		case "m":
			if d.mediaType != "" {
				return d, errors.New("a session description in a Stream holds one m= line")
			}
			if err := d.parseMedia(value, &media); err != nil {
				return d, err
			}
		case "b":
			bwtype, kbps, _ := strings.Cut(value, ":")
			n, err := strconv.Atoi(kbps)
			if err != nil || n < 0 {
				return d, fmt.Errorf("b=%s is not <bwtype>:<bandwidth>", value)
			}
			if bwtype == "AS" {
				d.media.Bandwidth = n
			}
		case "a":
			name, v, _ := strings.Cut(value, ":")
			media.Attributes = append(media.Attributes, sdp.Attribute{Name: name, Value: v})
		}
	}
	if d.mediaType == "" {
		return d, errors.New("no m= line")
	}
	d.media.Codecs = media.Codecs()
	return d, nil
}

func (d *sessionDescription) parseConnection(value string) error {
	f := strings.Fields(value)
	if len(f) != 3 || f[0] != "IN" || (f[1] != "IP4" && f[1] != "IP6") {
		return fmt.Errorf("c=%s is not IN IP4 or IP6 <address>", value)
	}
	if f[2] == choose {
		d.chooseAddress = true
		return nil
	}
	addr, err := netip.ParseAddr(f[2])
	if err != nil {
		return fmt.Errorf("c=%s: %w", value, err)
	}
	d.address = netip.AddrPortFrom(addr, d.address.Port())
	return nil
}

func (d *sessionDescription) parseMedia(value string, media *sdp.Media) error {
	f := strings.Fields(value)
	if len(f) < 4 {
		return fmt.Errorf("m=%s is not <media> <port> <proto> <fmt> ...", value)
	}
	d.mediaType, d.proto = f[0], f[2]
	if f[1] == choose {
		d.choosePort = true
	} else {
		port, err := strconv.ParseUint(f[1], 10, 16)
		if err != nil {
			return fmt.Errorf("m= port %q", f[1])
		}
		d.address = netip.AddrPortFrom(d.address.Addr(), uint16(port))
	}
	if len(f) == 4 && f[3] == noFormat {
		return nil
	}
	for _, pt := range f[3:] {
		n, err := strconv.Atoi(pt)
		if err != nil || n < 0 || n > 127 {
			return fmt.Errorf("m= format %q is not an RTP payload type", pt)
		}
		media.Formats = append(media.Formats, n)
	}
	return nil
}
