package mediactl

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// Relay is the set of operations with which an IMS-ALG, the P-CSCF,
// has its media pass the IMS-AGW. Each method is one call related
// procedure of Iq (TS 29.334 5.17.2), or two of them done together:
//
//   - Reserve is Reserve AGW Connection Point (5.17.2.2) for an access
//     and a core termination of one new context; each pass of an
//     INVITE through the P-CSCF has its own context.
//   - Configure is Configure AGW Connection Point (5.17.2.3).
//   - Release is Release AGW Termination (5.17.2.5) for both
//     terminations of the context.
//
// Two kinds of value implement it. Controller does, by sending the
// H.248 commands to a media gateway. The relay given to a Gateway
// does, and the Gateway calls it to carry out the commands that a
// controller sends.
//
// The termination that faces the UE is Access and the one that faces
// the rest of the network is Core, as in the HTTP API of the media
// relay. Media entering one termination leaves through the other.
type Relay interface {
	// Reserve makes a context with an access and a core termination,
	// each with a local address on which it receives RTP. RTCP is on
	// the port above (RFC 3550 11). media describes the stream that
	// the terminations are to receive; its Address is left zero, for
	// the relay to choose.
	Reserve(ctx context.Context, media Media) (Reservation, error)
	// Configure tells a termination where to send and in which form,
	// as the SDP of the party it faces describes the stream that the
	// party receives: remote is the content of the Remote descriptor.
	// RTCP goes to the port above remote.Address. A relay that latches
	// on the first packet it receives (H.248.37 6.6.2) keeps the
	// latched address.
	Configure(ctx context.Context, id string, side Side, remote Media) error
	// Release removes the context and both terminations, and returns
	// what each termination carried while it was in the context.
	Release(ctx context.Context, id string) (map[Side]Statistics, error)
}

// Side names a termination of a context by the network it faces. It is
// the <interface> field of the termination ID ip/<group>/<interface>/<id>
// (TS 29.334 5.6.1.1.1).
type Side string

const (
	// Access faces the UE.
	Access Side = "access"
	// Core faces the rest of the network.
	Core Side = "core"
)

// Media is the content of a Local or a Remote descriptor: one audio
// stream, with the lines that TS 29.334 5.15 makes mandatory (c=, m=
// and b=) and the payload formats of the m= line.
type Media struct {
	// Address is the RTP address and port of the stream (c= and the
	// port of m=). It is the zero value where the relay is to choose
	// it.
	Address netip.AddrPort
	// Codecs are the payload formats of the m= line, with their
	// a=rtpmap and a=fmtp lines, most preferred first. A relay that
	// does not transcode passes every payload type as it is. With no
	// codecs, the m= line carries "-": no resources for a particular
	// format are reserved (TS 29.334 5.15, Table 5.15.1).
	Codecs []sdp.Codec
	// Bandwidth is the b=AS value in kbit/s, or 0 for none.
	Bandwidth int
}

// Reservation is a context that Reserve made.
type Reservation struct {
	// ID names the context in the later calls. It means something only
	// to the Relay that returned it.
	ID string
	// Local is, for each termination, the address on which it
	// receives RTP: what the SDP that the party it faces receives is
	// to show.
	Local map[Side]netip.AddrPort
}

// Statistics is what a termination carried while it was in its
// context. Each field is a statistic of the packages that an RTP
// termination realizes (H.248.1 (05/2002) E.11.4 and E.12.4). They
// count RTP only.
type Statistics struct {
	// Duration is how long the termination was in the context
	// (nt/dur). Gateway measures it itself, so a Relay behind a
	// Gateway may leave it zero.
	Duration time.Duration
	// OctetsSent and OctetsReceived count the RTP octets (nt/os,
	// nt/or).
	OctetsSent     uint64
	OctetsReceived uint64
	// PacketsSent and PacketsReceived count the RTP packets (rtp/ps,
	// rtp/pr).
	PacketsSent     uint64
	PacketsReceived uint64
}

var (
	// ErrNoContext means that the relay has no context of that ID. A
	// media gateway answers it with error 411 (H.248.8 (03/2013)
	// 4.2.7), and Controller returns it for 411, 430 and 435, the codes
	// that tell that a context or termination is unknown (H.248.36
	// (03/2013) 5.6.1).
	ErrNoContext = errors.New("mediactl: no such context")
	// ErrInsufficientResources means that the relay has no room for
	// another context, such as no free port. A media gateway answers
	// it with error 510 (H.248.8 (03/2013) 4.2.49).
	ErrInsufficientResources = errors.New("mediactl: insufficient resources")
	// ErrUnavailable means that Controller has no media gateway to
	// send the command to: none has registered, or the one that holds
	// the context has stopped answering or is out of service.
	ErrUnavailable = errors.New("mediactl: no media gateway available")
)
