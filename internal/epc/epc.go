// Package epc is the stub of the access network: one node that
// stands for the MME, the Serving GW and the PDN GW of an EPC, with the
// PCRF stub inside it.
//
// A UE attaches over an HTTP and JSON API whose messages are named after
// the NAS messages of TS 24.301 (api.go). The stub gives it its static
// IP address and the P-CSCF addresses, and keeps its default bearer for
// SIP (QCI 5). The PCRF answers the P-CSCF over Rx (TS 29.214); when it
// authorises the media of a call, the stub activates a dedicated bearer
// for voice (QCI 1) and tells the UE on its event stream. Over the same
// API a learner makes the network deactivate a bearer or detach a UE,
// procedures that the network initiates, to watch the network end a
// call.
package epc

import (
	"context"
	"log/slog"
	"net/netip"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/accessapi"
	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// Config is the configuration of the epc role.
type Config struct {
	// APN is the access point name of the IMS PDN, such as "ims" (TS
	// 23.003 9.1, IR.92 4.3).
	APN string
	// PCSCFAddresses go to the UE in the PCO, the most preferred first
	// (TS 24.229 L.2.2.1).
	PCSCFAddresses []netip.Addr
	// Subscribers are the UEs the stub serves, each with the static IPv4
	// address of its IMS PDN connection.
	Subscribers []Subscriber
	// Cell is the cell every UE camps on.
	Cell SystemInformation
}

// Subscriber is a UE the stub serves.
//
// SIMPLIFIED: TS 23.401 5.3.1.1. The address is a static one that the
// HPLMN assigns, as the spec allows; there is no address pool and no
// GTP-U or SGi. The UE's container has the same address on the Docker
// network, so its packets reach the IMS directly.
type Subscriber struct {
	// IMSI is 15 decimal digits, such as 001010000000001 (TS 23.003 2.2).
	IMSI    string
	Address netip.Addr
}

// AFs reaches the AFs that opened Rx sessions, for the RARs and ASRs of
// the PCRF. *diameter.Server is one: it sends on the connection that
// the AF opened.
type AFs interface {
	Request(ctx context.Context, originHost string, request *diam.Message) (*diam.Message, error)
}

// EPC is the epc role.
type EPC struct {
	cfg Config
	// addresses holds the static address of each subscriber, from the
	// configuration.
	addresses map[string]netip.Addr
	store     Store
	streams   *accessapi.Streams
	node      *diameter.Node
	afs       AFs
	log       *slog.Logger
}

// New makes the stub. node is the Diameter node of the PCRF; give it
// HandleRx as its handler, and the stub its server with SetAFs.
func New(cfg Config, store Store, node *diameter.Node, log *slog.Logger) *EPC {
	e := &EPC{cfg: cfg, addresses: map[string]netip.Addr{}, store: store, streams: accessapi.NewStreams("epc"), node: node, log: log}
	for _, s := range cfg.Subscribers {
		e.addresses[s.IMSI] = s.Address
	}
	return e
}

// SetAFs gives the PCRF the server the AFs connect to. The server comes
// from node.Listen, after the node has its handler, so it is set here;
// call it before any AF connects.
func (e *EPC) SetAFs(afs AFs) { e.afs = afs }
