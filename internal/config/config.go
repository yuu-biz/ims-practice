// Package config reads a node's YAML file into validated, typed values.
// Everything past Load can trust what it gets.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
)

// Role is one IMS function that a node can take on.
type Role string

const (
	RoleHSS   Role = "hss"
	RoleICSCF Role = "icscf"
	RoleSCSCF Role = "scscf"
	RolePCSCF Role = "pcscf"
	// RoleAGW is the media relay in the IMS-AGW role.
	RoleAGW Role = "agw"
	// RoleLab collects HEP copies of the signalling and serves the web
	// UI of the Lab.
	RoleLab Role = "lab"
	// RoleOAM is the OAM server: it operates the other nodes through
	// their maintenance API and serves the OAM screen. It is not an IMS
	// function and sends no signalling.
	RoleOAM Role = "oam"
	// RoleIPSMGW is the IP-SM-GW, the application server for SMS over
	// IP. It hands the short messages of the UEs to the SC, either over
	// SGd or to the stub of the SC inside it.
	RoleIPSMGW Role = "ipsmgw"
	// RoleSC is the Service Centre of SMS: the SMS-SC that holds the
	// short messages, with the SMS-IWMSC that takes them from the
	// serving nodes and the SMS-GMSC that delivers them, which TS 23.040
	// 4.1 lets one node be.
	RoleSC Role = "sc"
	// RoleEPC is the stub of the EPC, with the PCRF stub inside it.
	RoleEPC Role = "epc"
	// RoleMMTel is the MMTel AS, the application server of the MMTel
	// supplementary services.
	RoleMMTel Role = "mmtel"
	// RoleMRFP is the MRFP, the media side of the MRF (TS 23.228 4.7):
	// the media node of the agw role, with announcements to play.
	RoleMRFP Role = "mrfp"
	// RoleMRFC is the MRFC, the control side of the MRF, which an AS
	// drives over Mr' with RFC 4240.
	RoleMRFC Role = "mrfc"
	// RoleIBCF is the IBCF, the border of the home network towards the
	// IMS networks it interconnects with (TS 24.229 5.10).
	RoleIBCF Role = "ibcf"
	// RoleFault is the fault agent: it breaks the nodes on purpose, so
	// that a learner can watch what a failure does. It is not an IMS
	// function and sends no signalling.
	RoleFault Role = "fault"
)

var knownRoles = []Role{RoleHSS, RoleICSCF, RoleSCSCF, RolePCSCF, RoleAGW, RoleLab, RoleOAM, RoleIPSMGW, RoleSC, RoleEPC, RoleMMTel, RoleMRFP, RoleMRFC, RoleIBCF, RoleFault}

// Config is a node's validated configuration. Each role's section is
// set exactly when the node has that role.
type Config struct {
	Node  string
	Roles []Role
	// HomeDomain is the home network domain, such as
	// ims.mnc001.mcc001.3gppnetwork.org (TS 23.003).
	HomeDomain string
	// DNSServer is the host:port to send DNS queries to. Empty means the
	// nameserver in /etc/resolv.conf.
	DNSServer string
	// HEP is where the node sends a copy of every SIP and Diameter
	// message. Nil sends nothing.
	HEP *HEP
	// Store is where the node keeps the state of its users. Nil keeps it
	// in memory, as kind memory does.
	Store *Store
	// Maintenance is set when the node serves its maintenance API.
	Maintenance *Maintenance
	// Diameter is set for the roles that speak Cx, Sh or Rx: hss, icscf,
	// scscf, ipsmgw, mmtel and epc, and a pcscf that talks to a PCRF.
	Diameter *Diameter
	PCSCF    *PCSCF
	AGW      *AGW
	MRFP     *MRFP
	MRFC     *MRFC
	ICSCF    *ICSCF
	SCSCF    *SCSCF
	HSS      *hss.Config
	IPSMGW   *IPSMGW
	SC       *SC
	EPC      *EPC
	MMTel    *MMTel
	IBCF     *IBCF

	Lab   *Lab
	OAM   *OAM
	Fault *Fault
}

// Diameter is the node's Diameter identity and connections.
type Diameter struct {
	Identity diameter.Identity
	// Listen is set only for the hss, sc and epc roles, which accept
	// connections.
	Listen string
	// Peers is set for the icscf, scscf, ipsmgw, sc and mmtel roles,
	// which connect to the HSS, for a pcscf, which connects to the PCRF,
	// and for an ipsmgw that reaches the SC over SGd. Each peer says
	// which of the kinds it is, so a node with several such roles has no
	// doubt about it. Several peers of the HSS are its instances, which
	// all serve the same subscribers.
	Peers  []Peer
	Timers diameter.Timers
}

// PeerKind is what a Diameter peer of the node serves.
type PeerKind string

const (
	// PeerHSS serves Cx and Sh. A configuration may name several
	// instances of one HSS.
	PeerHSS PeerKind = "hss"
	// PeerPCRF serves Rx, for the P-CSCF as an AF.
	PeerPCRF PeerKind = "pcrf"
	// PeerSC serves SGd, for the IP-SM-GW as a serving node: the
	// SMS-IWMSC that takes the short messages of its UEs. A
	// configuration may name several instances of one SC, which share
	// the short messages they hold. An ipsmgw with such a peer reaches
	// the SC over SGd; one without it uses the stub of the SC inside its
	// own process.
	PeerSC PeerKind = "sc"
)

// Peer is one Diameter peer: where to reach it, the Origin-Host it must
// send in its CEA, and what it serves.
type Peer struct {
	diameter.PeerConfig
	Kind PeerKind
}

// HEP is the collector of the HEP copies.
type HEP struct {
	Collector string // host:port, such as lab:9060
	// AgentID is the capture agent ID in every packet (HEP3 chunk
	// 0x000c). Zero when the configuration leaves it out.
	AgentID uint32
}

func (c Config) HasRole(r Role) bool { return slices.Contains(c.Roles, r) }

const (
	// RFC 3539 3.4.1 [1]: Twinit defaults to 30 s, MUST NOT be below 6 s,
	// and is jittered by up to 2 s either way.
	defaultTw = 30 * time.Second
	minimumTw = 6 * time.Second
	jitterTw  = 2 * time.Second
	defaultTc = 30 * time.Second // RFC 6733 12
	// answerTimeout is the application-defined Timeout of RFC 6733
	// 5.6.2. It is kept well below the 10 s that docker stop waits
	// before it kills the container, so the DPR/DPA exchange on SIGTERM
	// finishes in time.
	answerTimeout = 5 * time.Second
)

type rawConfig struct {
	Node        string          `yaml:"node"`
	Roles       []string        `yaml:"roles"`
	HomeDomain  string          `yaml:"home_domain"`
	DNSServer   string          `yaml:"dns_server"`
	HEP         *rawHEP         `yaml:"hep"`
	Store       *rawStore       `yaml:"store"`
	Maintenance *rawMaintenance `yaml:"maintenance"`
	Diameter    *rawDiameter    `yaml:"diameter"`
	PCSCF       *rawPCSCF       `yaml:"pcscf"`
	AGW         *rawAGW         `yaml:"agw"`
	MRFP        *rawMRFP        `yaml:"mrfp"`
	MRFC        *rawMRFC        `yaml:"mrfc"`
	ICSCF       *rawICSCF       `yaml:"icscf"`
	SCSCF       *rawSCSCF       `yaml:"scscf"`
	HSS         *rawHSS         `yaml:"hss"`
	IPSMGW      *rawIPSMGW      `yaml:"ipsmgw"`
	SC          *rawSC          `yaml:"sc"`
	EPC         *rawEPC         `yaml:"epc"`
	MMTel       *rawMMTel       `yaml:"mmtel"`
	IBCF        *rawIBCF        `yaml:"ibcf"`

	Lab   *rawLab   `yaml:"lab"`
	OAM   *rawOAM   `yaml:"oam"`
	Fault *rawFault `yaml:"fault"`
}

type rawDiameter struct {
	OriginHost  string    `yaml:"origin_host"`
	OriginRealm string    `yaml:"origin_realm"`
	Listen      string    `yaml:"listen"`
	Peers       []rawPeer `yaml:"peers"`
	Timers      rawTimers `yaml:"timers"`
}

type rawHEP struct {
	Collector string `yaml:"collector"`
	AgentID   uint32 `yaml:"agent_id"`
}

type rawPeer struct {
	Address    string `yaml:"address"`
	OriginHost string `yaml:"origin_host"`
	Kind       string `yaml:"kind"`
}

type rawTimers struct {
	Tw time.Duration `yaml:"tw"`
	Tc time.Duration `yaml:"tc"`
}

// Load reads and validates the file at path.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	cfg, err := parse(f, filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse reads and validates one YAML document. Unknown fields are
// errors, so a misspelt key is caught instead of silently ignored. A
// relative path in the document, such as the profile_file of the ibcf
// role, is relative to the working directory; Load takes it relative to
// the file instead.
func Parse(r io.Reader) (Config, error) {
	return parse(r, "")
}

func parse(r io.Reader, dir string) (Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var raw rawConfig
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return Config{}, errors.New("empty configuration")
		}
		return Config{}, err
	}
	return raw.validate(dir)
}

func (raw rawConfig) validate(dir string) (Config, error) {
	if raw.Node == "" {
		return Config{}, errors.New("node: required")
	}
	roles, err := parseRoles(raw.Roles)
	if err != nil {
		return Config{}, err
	}
	if raw.HomeDomain == "" {
		return Config{}, errors.New("home_domain: required")
	}
	cfg := Config{Node: raw.Node, Roles: roles, HomeDomain: raw.HomeDomain}
	if raw.DNSServer != "" {
		if err := checkHostPort(raw.DNSServer); err != nil {
			return Config{}, fmt.Errorf("dns_server: %w", err)
		}
		cfg.DNSServer = raw.DNSServer
	}
	if raw.HEP != nil {
		if err := checkHostPort(raw.HEP.Collector); err != nil {
			return Config{}, fmt.Errorf("hep.collector: %w", err)
		}
		cfg.HEP = &HEP{Collector: raw.HEP.Collector, AgentID: raw.HEP.AgentID}
	}
	if raw.Store != nil {
		s, err := raw.Store.validate()
		if err != nil {
			return Config{}, fmt.Errorf("store: %w", err)
		}
		cfg.Store = &s
	}
	if raw.Maintenance != nil {
		m, err := raw.Maintenance.validate()
		if err != nil {
			return Config{}, fmt.Errorf("maintenance: %w", err)
		}
		cfg.Maintenance = &m
	}

	// The P-CSCF speaks Rx only once it is given a PCRF, so its Diameter
	// is optional.
	needsDiameter := cfg.HasRole(RoleHSS) || cfg.HasRole(RoleICSCF) || cfg.HasRole(RoleSCSCF) || cfg.HasRole(RoleIPSMGW) ||
		cfg.HasRole(RoleSC) || cfg.HasRole(RoleMMTel) || cfg.HasRole(RoleEPC)
	switch {
	case needsDiameter && raw.Diameter == nil:
		return Config{}, errors.New("diameter: required for the hss, icscf, scscf, ipsmgw, sc, mmtel and epc roles")
	case !needsDiameter && !cfg.HasRole(RolePCSCF) && raw.Diameter != nil:
		return Config{}, errors.New("diameter: only the hss, icscf, scscf, ipsmgw, sc, mmtel, epc and pcscf roles use this section")
	}
	if raw.Diameter != nil {
		d, err := raw.Diameter.validate(cfg)
		if err != nil {
			return Config{}, fmt.Errorf("diameter: %w", err)
		}
		cfg.Diameter = &d
	}

	if err := section("pcscf", raw.PCSCF != nil, cfg.HasRole(RolePCSCF), "the pcscf role"); err != nil {
		return Config{}, err
	}
	if raw.PCSCF != nil {
		c, err := raw.PCSCF.validate()
		if err != nil {
			return Config{}, fmt.Errorf("pcscf: %w", err)
		}
		cfg.PCSCF = &c
	}
	if err := section("icscf", raw.ICSCF != nil, cfg.HasRole(RoleICSCF), "the icscf role"); err != nil {
		return Config{}, err
	}
	if raw.ICSCF != nil {
		c, err := raw.ICSCF.validate(cfg.HomeDomain)
		if err != nil {
			return Config{}, fmt.Errorf("icscf: %w", err)
		}
		cfg.ICSCF = &c
	}
	if err := section("scscf", raw.SCSCF != nil, cfg.HasRole(RoleSCSCF), "the scscf role"); err != nil {
		return Config{}, err
	}
	if raw.SCSCF != nil {
		c, err := raw.SCSCF.validate()
		if err != nil {
			return Config{}, fmt.Errorf("scscf: %w", err)
		}
		cfg.SCSCF = &c
	}
	if err := section("agw", raw.AGW != nil, cfg.HasRole(RoleAGW), "the agw role"); err != nil {
		return Config{}, err
	}
	if raw.AGW != nil {
		c, err := raw.AGW.validate(true)
		if err != nil {
			return Config{}, fmt.Errorf("agw: %w", err)
		}
		cfg.AGW = &c
	}
	if err := section("mrfp", raw.MRFP != nil, cfg.HasRole(RoleMRFP), "the mrfp role"); err != nil {
		return Config{}, err
	}
	if raw.MRFP != nil {
		c, err := raw.MRFP.validate()
		if err != nil {
			return Config{}, fmt.Errorf("mrfp: %w", err)
		}
		cfg.MRFP = &c
	}
	if err := section("mrfc", raw.MRFC != nil, cfg.HasRole(RoleMRFC), "the mrfc role"); err != nil {
		return Config{}, err
	}
	if raw.MRFC != nil {
		c, err := raw.MRFC.validate()
		if err != nil {
			return Config{}, fmt.Errorf("mrfc: %w", err)
		}
		cfg.MRFC = &c
	}
	if err := section("hss", raw.HSS != nil, cfg.HasRole(RoleHSS), "the hss role"); err != nil {
		return Config{}, err
	}
	if raw.HSS != nil {
		c, err := raw.HSS.validate(cfg.HomeDomain)
		if err != nil {
			return Config{}, fmt.Errorf("hss: %w", err)
		}
		cfg.HSS = &c
	}

	if err := section("ipsmgw", raw.IPSMGW != nil, cfg.HasRole(RoleIPSMGW), "the ipsmgw role"); err != nil {
		return Config{}, err
	}
	if raw.IPSMGW != nil {
		c, err := raw.IPSMGW.validate()
		if err != nil {
			return Config{}, fmt.Errorf("ipsmgw: %w", err)
		}
		// With a Service Centre over SGd, a short message for a user of
		// another network is the Service Centre's to deliver, so the
		// IP-SM-GW would send nothing to its IBCF: only the stub of the
		// SC hands the IP-SM-GW such deliveries.
		if c.IBCF.Host != "" && slices.ContainsFunc(cfg.Diameter.Peers, func(p Peer) bool { return p.Kind == PeerSC }) {
			return Config{}, errors.New("ipsmgw: ibcf: an IP-SM-GW with a Diameter peer of kind sc sends nothing through an IBCF")
		}
		cfg.IPSMGW = &c
	}

	if err := section("sc", raw.SC != nil, cfg.HasRole(RoleSC), "the sc role"); err != nil {
		return Config{}, err
	}
	if raw.SC != nil {
		c, err := raw.SC.validate()
		if err != nil {
			return Config{}, fmt.Errorf("sc: %w", err)
		}
		cfg.SC = &c
	}

	if err := section("mmtel", raw.MMTel != nil, cfg.HasRole(RoleMMTel), "the mmtel role"); err != nil {
		return Config{}, err
	}
	if raw.MMTel != nil {
		c, err := raw.MMTel.validate(cfg.HomeDomain)
		if err != nil {
			return Config{}, fmt.Errorf("mmtel: %w", err)
		}
		cfg.MMTel = &c
	}

	if err := section("epc", raw.EPC != nil, cfg.HasRole(RoleEPC), "the epc role"); err != nil {
		return Config{}, err
	}
	if raw.EPC != nil {
		c, err := raw.EPC.validate()
		if err != nil {
			return Config{}, fmt.Errorf("epc: %w", err)
		}
		cfg.EPC = &c
	}

	if err := section("ibcf", raw.IBCF != nil, cfg.HasRole(RoleIBCF), "the ibcf role"); err != nil {
		return Config{}, err
	}
	if raw.IBCF != nil {
		c, err := raw.IBCF.validate(cfg.HomeDomain, dir)
		if err != nil {
			return Config{}, fmt.Errorf("ibcf: %w", err)
		}
		cfg.IBCF = &c
	}

	if err := section("lab", raw.Lab != nil, cfg.HasRole(RoleLab), "the lab role"); err != nil {
		return Config{}, err
	}
	if raw.Lab != nil {
		c, err := raw.Lab.validate()
		if err != nil {
			return Config{}, fmt.Errorf("lab: %w", err)
		}
		cfg.Lab = &c
	}

	if err := section("oam", raw.OAM != nil, cfg.HasRole(RoleOAM), "the oam role"); err != nil {
		return Config{}, err
	}
	if raw.OAM != nil {
		c, err := raw.OAM.validate()
		if err != nil {
			return Config{}, fmt.Errorf("oam: %w", err)
		}
		cfg.OAM = &c
	}

	if err := section("fault", raw.Fault != nil, cfg.HasRole(RoleFault), "the fault role"); err != nil {
		return Config{}, err
	}
	if raw.Fault != nil {
		c, err := raw.Fault.validate()
		if err != nil {
			return Config{}, fmt.Errorf("fault: %w", err)
		}
		cfg.Fault = &c
	}

	if err := cfg.checkPorts(); err != nil {
		return Config{}, err
	}
	if err := cfg.checkMediaControl(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// checkMediaControl refuses the configurations that media control over
// H.248 cannot serve. A media gateway registers with one controller, so
// the association, and with it every context the gateway holds for this
// node, belongs to the one instance that the gateway registered with.
// Anything that assumes another instance can take those contexts over
// is refused here, rather than left to fail on the first call that
// reaches the other instance.
//
// SIMPLIFIED: a controller does not hand its media gateways to another
// instance. A gateway follows the controller that a reply names while
// it registers, and after a failure registers again with the controller
// it was given, so it always ends at one controller; nothing moves an
// association that already holds contexts. So a P-CSCF that controls
// media over H.248 runs as one instance, and the configurations that
// ask for a second one are refused here.
func (c Config) checkMediaControl() error {
	shared := c.Store != nil && c.Store.Kind == StoreValkey
	if p := c.PCSCF; p != nil && p.MediaController != nil {
		if p.SIP.Pool != "" {
			return errors.New("pcscf.sip.pool: a P-CSCF that controls media over h248 runs as one instance, " +
				"because a media gateway registers with one controller and no other instance can reach the contexts it holds")
		}
		if shared {
			return errors.New("store: a P-CSCF that controls media over h248 cannot hand its dialogs to another instance, " +
				"because a media gateway registers with one controller; leave out the store section or control the media relays over media_relays")
		}
	}
	// The gateway's own table of which H.248 context holds which context
	// of the relay is in its memory. A relay that opened the contexts of
	// an earlier process again from the store would hold ports for
	// contexts its gateway knows nothing about, and no controller could
	// release them.
	if a := c.AGW; a != nil && a.MediaGateway != nil && shared {
		return errors.New("store: a media relay that takes its commands over h248 keeps no contexts across processes, " +
			"because its gateway holds in memory which H.248 context is which; leave out the store section or serve the control API instead")
	}
	return nil
}

// section checks that a role's section is present exactly when the
// node has the role.
func section(name string, present, needed bool, who string) error {
	switch {
	case needed && !present:
		return fmt.Errorf("%s: required for %s", name, who)
	case !needed && present:
		return fmt.Errorf("%s: only %s use this section", name, who)
	}
	return nil
}

// checkPorts makes sure that roles combined in one node do not
// bind the same port. Two listeners clash when they have the same
// port and the same address, or when either takes every address: the
// two stacks of the ibcf role share port 5060 on two addresses.
func (c Config) checkPorts() error {
	type listener struct {
		name string
		addr netip.AddrPort
	}
	var listeners []listener
	listen := func(name string, addr netip.AddrPort) error {
		for _, l := range listeners {
			sameAddress := l.addr.Addr() == addr.Addr() || l.addr.Addr().IsUnspecified() || addr.Addr().IsUnspecified()
			if l.addr.Port() == addr.Port() && sameAddress {
				return fmt.Errorf("%s and %s both listen on port %d", l.name, name, addr.Port())
			}
		}
		listeners = append(listeners, listener{name, addr})
		return nil
	}
	var sip []listener
	if c.PCSCF != nil {
		sip = append(sip, listener{"pcscf", c.PCSCF.SIP.Listen})
	}
	if c.ICSCF != nil {
		sip = append(sip, listener{"icscf", c.ICSCF.SIP.Listen})
	}
	if c.SCSCF != nil {
		sip = append(sip, listener{"scscf", c.SCSCF.SIP.Listen})
	}
	if c.IPSMGW != nil {
		sip = append(sip, listener{"ipsmgw", c.IPSMGW.SIP.Listen})
	}
	if c.MMTel != nil {
		sip = append(sip, listener{"mmtel", c.MMTel.SIP.Listen})
	}
	if c.MRFC != nil {
		sip = append(sip, listener{"mrfc", c.MRFC.SIP.Listen})
	}
	if c.IBCF != nil {
		sip = append(sip, listener{"ibcf", c.IBCF.SIP.Listen}, listener{"the external side of ibcf", c.IBCF.ExternalSIP.Listen})
	}
	for _, l := range sip {
		if err := listen(l.name, l.addr); err != nil {
			return err
		}
	}
	if c.MMTel != nil {
		// XCAP is HTTP over TCP, which shares the port numbers of SIP over
		// TCP.
		if err := listen("the XCAP server of the mmtel role", c.MMTel.XCAP.Listen); err != nil {
			return err
		}
	}
	// Diameter and the other HTTP servers are carried over TCP too. A
	// listener whose host is a name rather than an address is left out:
	// the address it binds is the resolver's to say, so only the start
	// of the node finds out whether it clashes.
	for _, l := range []struct {
		name    string
		set     bool
		address func() string
	}{
		{"the Diameter listener", c.Diameter != nil && c.Diameter.Listen != "", func() string { return c.Diameter.Listen }},
		{"the provisioning API of the hss role", c.HSS != nil && c.HSS.ProvisioningListen != "", func() string { return c.HSS.ProvisioningListen }},
		{"the API of the epc role", c.EPC != nil, func() string { return c.EPC.API.String() }},
		// A relay that takes its commands over H.248 serves no control
		// API; its H.248 endpoint is below.
		{"agw", c.AGW != nil && c.AGW.MediaGateway == nil, func() string { return c.AGW.Control.String() }},
		{"mrfp", c.MRFP != nil, func() string { return c.MRFP.Control.String() }},
		{"the notify server of mrfc", c.MRFC != nil, func() string { return c.MRFC.Notify.String() }},
		{"the HTTP server of the lab role", c.Lab != nil, func() string { return c.Lab.HTTPListen }},
		{"the HTTP server of the oam role", c.OAM != nil, func() string { return c.OAM.HTTPListen }},
		{"the API of the fault role", c.Fault != nil, func() string { return c.Fault.HTTPListen }},
		{"the maintenance listener", c.Maintenance != nil, func() string { return c.Maintenance.Listen }},
	} {
		if !l.set {
			continue
		}
		if addr, ok := listenAddress(l.address()); ok {
			if err := listen(l.name, addr); err != nil {
				return err
			}
		}
	}
	// H.248 is carried over UDP, whose port numbers SIP shares, so the
	// endpoints of media control go in the same list. A node that both
	// controls media and relays it holds two of them.
	if c.PCSCF != nil && c.PCSCF.MediaController != nil {
		if err := listen("the H.248 controller of the pcscf role", c.PCSCF.MediaController.Listen); err != nil {
			return err
		}
	}
	if c.AGW != nil && c.AGW.MediaGateway != nil {
		if err := listen("the H.248 gateway of the agw role", c.AGW.MediaGateway.Listen); err != nil {
			return err
		}
	}
	if err := c.checkMediaNodes(); err != nil {
		return err
	}
	if c.PCSCF != nil && c.PCSCF.ProtectedServerPort != 0 {
		protected := netip.AddrPortFrom(c.PCSCF.SIP.Listen.Addr(), c.PCSCF.ProtectedServerPort)
		if err := listen("the protected server port of the pcscf", protected); err != nil {
			return err
		}
	}
	if c.PCSCF != nil && c.PCSCF.ProtectedClientPort != 0 {
		protected := netip.AddrPortFrom(c.PCSCF.SIP.Listen.Addr(), c.PCSCF.ProtectedClientPort)
		if err := listen("the protected client port of the pcscf", protected); err != nil {
			return err
		}
	}
	return nil
}

// checkMediaNodes makes sure that agw and mrfp in one node do not share
// RTP ports.
func (c Config) checkMediaNodes() error {
	if c.AGW != nil && c.MRFP != nil && c.AGW.MinPort <= c.MRFP.MaxPort && c.MRFP.MinPort <= c.AGW.MaxPort {
		return fmt.Errorf("the rtp_ports of agw (%d-%d) and mrfp (%d-%d) overlap", c.AGW.MinPort, c.AGW.MaxPort, c.MRFP.MinPort, c.MRFP.MaxPort)
	}
	return nil
}

func parseRoles(raw []string) ([]Role, error) {
	if len(raw) == 0 {
		return nil, errors.New("roles: at least one role is required")
	}
	var roles []Role
	for _, name := range raw {
		r := Role(name)
		if !slices.Contains(knownRoles, r) {
			return nil, fmt.Errorf("roles: unknown role %q (known: %v)", name, knownRoles)
		}
		if slices.Contains(roles, r) {
			return nil, fmt.Errorf("roles: %q listed twice", name)
		}
		roles = append(roles, r)
	}
	return roles, nil
}

func (raw rawDiameter) validate(cfg Config) (Diameter, error) {
	if raw.OriginHost == "" {
		return Diameter{}, errors.New("origin_host: required")
	}
	if raw.OriginRealm == "" {
		return Diameter{}, errors.New("origin_realm: required")
	}
	d := Diameter{
		Identity: diameter.Identity{OriginHost: raw.OriginHost, OriginRealm: raw.OriginRealm},
	}

	// The SC listens because the serving node is the end that opens the
	// SGd connection: it brings the mobile originated short messages,
	// and the SC sends the mobile terminated ones back over the same
	// connection (TS 29.338 6.3.1.1, 6.3.1.2).
	listens := cfg.HasRole(RoleHSS) || cfg.HasRole(RoleSC) || cfg.HasRole(RoleEPC)
	switch {
	case listens && raw.Listen == "":
		return Diameter{}, errors.New("listen: required for the hss, sc and epc roles")
	case !listens && raw.Listen != "":
		return Diameter{}, errors.New("listen: only the hss, sc and epc roles listen")
	case listens:
		if err := checkHostPort(raw.Listen); err != nil {
			return Diameter{}, fmt.Errorf("listen: %w", err)
		}
		d.Listen = raw.Listen
	}

	needsHSS := cfg.HasRole(RoleICSCF) || cfg.HasRole(RoleSCSCF) || cfg.HasRole(RoleIPSMGW) ||
		cfg.HasRole(RoleSC) || cfg.HasRole(RoleMMTel)
	onlyRx := !needsHSS && !listens && cfg.HasRole(RolePCSCF)
	kinds := fmt.Sprintf("%q, %q or %q", PeerHSS, PeerPCRF, PeerSC)
	switch {
	case needsHSS && len(raw.Peers) == 0:
		return Diameter{}, errors.New("peers: the icscf, scscf, ipsmgw, sc and mmtel roles need the HSS as a peer")
	case onlyRx && len(raw.Peers) == 0:
		return Diameter{}, errors.New("peers: the pcscf role needs the PCRF as a peer")
	case !needsHSS && !cfg.HasRole(RolePCSCF) && len(raw.Peers) != 0:
		return Diameter{}, errors.New("peers: only the icscf, scscf, ipsmgw, sc, mmtel and pcscf roles connect to peers")
	}
	for i, p := range raw.Peers {
		if err := checkHostPort(p.Address); err != nil {
			return Diameter{}, fmt.Errorf("peers[%d].address: %w", i, err)
		}
		if p.OriginHost == "" {
			return Diameter{}, fmt.Errorf("peers[%d].origin_host: required", i)
		}
		kind := PeerKind(p.Kind)
		switch {
		case p.Kind == "":
			return Diameter{}, fmt.Errorf("peers[%d].kind: required, %s", i, kinds)
		case kind != PeerHSS && kind != PeerPCRF && kind != PeerSC:
			return Diameter{}, fmt.Errorf("peers[%d].kind: %q is not %s", i, p.Kind, kinds)
		case kind == PeerHSS && !needsHSS:
			return Diameter{}, fmt.Errorf("peers[%d].kind: only the icscf, scscf, ipsmgw, sc and mmtel roles reach the HSS", i)
		case kind == PeerPCRF && !cfg.HasRole(RolePCSCF):
			return Diameter{}, fmt.Errorf("peers[%d].kind: only the pcscf role reaches the PCRF", i)
		case kind == PeerSC && !cfg.HasRole(RoleIPSMGW):
			return Diameter{}, fmt.Errorf("peers[%d].kind: only the ipsmgw role reaches the SC", i)
		}
		d.Peers = append(d.Peers, Peer{PeerConfig: diameter.PeerConfig{Address: p.Address, OriginHost: p.OriginHost}, Kind: kind})
	}
	if needsHSS && !slices.ContainsFunc(d.Peers, func(p Peer) bool { return p.Kind == PeerHSS }) {
		return Diameter{}, errors.New("peers: the icscf, scscf, ipsmgw, sc and mmtel roles need the HSS as a peer")
	}
	if onlyRx && !slices.ContainsFunc(d.Peers, func(p Peer) bool { return p.Kind == PeerPCRF }) {
		return Diameter{}, errors.New("peers: the pcscf role needs the PCRF as a peer")
	}

	timers, err := raw.Timers.validate()
	if err != nil {
		return Diameter{}, fmt.Errorf("timers: %w", err)
	}
	d.Timers = timers
	return d, nil
}

func (raw rawTimers) validate() (diameter.Timers, error) {
	tw, tc := raw.Tw, raw.Tc
	if tw == 0 {
		tw = defaultTw
	}
	if tc == 0 {
		tc = defaultTc
	}
	if tw < minimumTw {
		return diameter.Timers{}, fmt.Errorf("tw: %v is below the RFC 3539 minimum of %v", tw, minimumTw)
	}
	if tc < 0 {
		return diameter.Timers{}, fmt.Errorf("tc: %v is negative", tc)
	}
	return diameter.Timers{
		Watchdog:       tw,
		WatchdogJitter: jitterTw,
		Reconnect:      tc,
		Answer:         answerTimeout,
	}, nil
}

// listenAddress is the address that a listener written as host:port
// binds, for checkPorts: every address when the host is empty, as
// net.Listen takes it. ok is false when the host is a name.
func listenAddress(hostPort string) (addr netip.AddrPort, ok bool) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return netip.AddrPort{}, false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	ip := netip.IPv6Unspecified()
	if host != "" {
		if ip, err = netip.ParseAddr(host); err != nil {
			return netip.AddrPort{}, false
		}
	}
	return netip.AddrPortFrom(ip, uint16(n)), true
}

func checkHostPort(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
		return fmt.Errorf("invalid port %q", port)
	}
	return nil
}
