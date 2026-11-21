// Package fivegc is the stub of the 5GS access network:
// one node that stands for the AMF, the SMF and the UPF of a 5GC, with
// the PCF stub inside it. It is to 5GS what package epc is to EPS.
//
// A UE registers and sets up its PDU session over an HTTP and JSON API
// whose messages are named after the NAS messages of TS 24.501
// (api.go). The stub gives it its static IP address and the P-CSCF
// addresses in the extended PCO, and keeps the QoS flow of the default
// QoS rule for SIP (5QI 5). The PCF serves the P-CSCF over N5
// (Npcf_PolicyAuthorization, TS 29.514, package n5); when it authorises
// the media of a call, the stub adds a GBR QoS flow for voice (5QI 1)
// and tells the UE on its event stream. Over the same API a learner
// makes the network release the voice QoS flow or the PDU session, or
// deregister the UE, procedures that the network initiates, to watch
// the network end a call.
//
// SIMPLIFIED: TS 23.502 4.3.2.2.1. The AMF, the SMF,
// the UPF and the PCF are one node, so N11, N4 and N7 are Go calls over
// one Store, and the NRF and the UDM are not there: the stub serves the
// subscribers of its configuration, and the AF is given the PCF's
// apiRoot in its configuration instead of finding it through the NRF.
// There is no user plane: N3 and
// N6 are the one Docker network.
package fivegc

import (
	"log/slog"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/n5"
)

// Config is the configuration of the 5gc role.
type Config struct {
	// DNN is the data network name of the IMS, such as "ims".
	DNN string
	// PCSCFAddresses go to the UE in the extended PCO, the most preferred
	// first (TS 24.501 6.4.1.3). An IPv4 address goes to a UE that asks
	// for IPv4 ones, an IPv6 address to a UE that asks for IPv6 ones.
	PCSCFAddresses []netip.Addr
	// Subscribers are the UEs the stub serves, each with the static IPv4
	// address of its PDU session.
	Subscribers []Subscriber
	// Cell is the NR cell every UE camps on.
	Cell SystemInformation
}

// Subscriber is a UE the stub serves.
//
// SIMPLIFIED: TS 24.501 6.2.4.2. The SMF gives the UE its address in
// the PDU address IE; here the address is a static one per subscriber,
// with no pool and no UPF. The UE's container has the same address on
// the Docker network, so its packets reach the IMS directly, with no
// tunnel and no network setting of the host.
type Subscriber struct {
	// IMSI is 15 decimal digits, such as 001010000000001 (TS 23.003 2.2).
	IMSI    string
	Address netip.Addr
}

// FiveGC is the 5gc role.
type FiveGC struct {
	cfg Config
	// addresses holds the static address of each subscriber, from the
	// configuration.
	addresses map[string]netip.Addr
	store     Store
	streams   *streams
	notifier  *n5.Notifier
	log       *slog.Logger
}

// New makes the stub. Serve Handler to the UEs and N5Handler to the
// AFs.
func New(cfg Config, store Store, log *slog.Logger) *FiveGC {
	g := &FiveGC{cfg: cfg, addresses: map[string]netip.Addr{}, store: store, streams: newStreams(), notifier: n5.NewNotifier(), log: log}
	for _, s := range cfg.Subscribers {
		g.addresses[s.IMSI] = s.Address
	}
	return g
}
