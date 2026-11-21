package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// SIP is where a CSCF role listens and how it names itself.
type SIP struct {
	// Listen is the address SIP binds over UDP and TCP, such as
	// 0.0.0.0:5060.
	Listen netip.AddrPort
	// Host is the FQDN the role puts in Via, Path, Service-Route and
	// Record-Route, so that other nodes find it through DNS. With a Pool
	// it names this instance alone, such as
	// scscf-1.ims.mnc001.mcc001.3gppnetwork.org.
	Host string
	// Pool is the name that the instances of the role share, such as
	// scscf.ims.mnc001.mcc001.3gppnetwork.org, for which DNS returns
	// every instance. Empty when the role runs as one instance.
	Pool string
	// TransactionTimeout shortens Timer B and Timer F of the client
	// transactions, so that a request to an instance that has stopped
	// fails over sooner. Zero keeps 64*T1 (RFC 3261 17.1.1.2,
	// 17.1.2.2).
	TransactionTimeout time.Duration
}

// URI is the SIP URI of the role, such as
// sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060.
func (s SIP) URI() string { return "sip:" + s.Host + ":" + strconv.Itoa(int(s.Listen.Port())) }

// PoolURI is the URI that the instances of a role share: sip:<pool>
// without a port, which DNS resolves with SRV to every instance
// (RFC 3263 4.2). Without a pool it is URI(), the instance's own.
func (s SIP) PoolURI() string {
	if s.Pool == "" {
		return s.URI()
	}
	return "sip:" + s.Pool
}

// PCSCF is the configuration of the pcscf role.
type PCSCF struct {
	SIP SIP
	// VisitedNetworkID goes into P-Visited-Network-ID (TS 24.229
	// 5.2.2.1): a string provisioned for the P-CSCF's network.
	VisitedNetworkID string
	// MediaRelay is the URL of the control API of the media relay, such
	// as http://agw:8080. Empty sends RTP between the UEs directly.
	MediaRelay string
	// HostAccess, when set, tells UEs that come through the NAT of the
	// container host (from Gateway) to send RTP to Address, where the
	// relay's ports are published.
	HostAccess *HostAccess
	// ProtectedServerPort is port_ps, where the P-CSCF takes the
	// protected messages of UEs that register with IMS AKA, on the
	// address of SIP.Listen (TS 33.203 7.1). Zero means no IMS AKA.
	ProtectedServerPort uint16
}

type HostAccess struct {
	Gateway netip.Addr
	Address netip.Addr
}

// ICSCF is the configuration of the icscf role.
type ICSCF struct {
	SIP SIP
	// SCSCFs are the S-CSCFs the I-CSCF can choose from, with their
	// capabilities (TS 29.228 6.7).
	SCSCFs []SCSCFEntry
	// PSISubdomains are the subdomains of the home domain that hold
	// public service identities, such as that of the conference URIs.
	// DNS resolves each to the application server that hosts it, and the
	// I-CSCF sends a request for one there without asking the HSS (TS
	// 24.229 5.3.2.1 step 3).
	PSISubdomains []string
}

type SCSCFEntry struct {
	Name         string // the S-CSCF's SIP URI, as in Server-Name
	Capabilities []uint32
}

// SCSCF is the configuration of the scscf role.
type SCSCF struct {
	SIP SIP
	// MinExpires and MaxExpires bound the registration interval, which
	// the operator sets in the S-CSCF (TS 24.229 5.4.1.1). A shorter
	// request gets 423; a longer one is shortened.
	MinExpires time.Duration
	MaxExpires time.Duration
	// IBCF is the IBCF that a request for another network leaves
	// through (TS 24.229 5.4.3.2 steps 11 and 12). Its zero value has no
	// host, and such a request goes to the other network directly.
	IBCF sip.URI
	// Restoration has the S-CSCF keep the restoration information of
	// its registrations in the HSS, so that another S-CSCF can take them
	// over (TS 23.380).
	Restoration bool
}

// AGW is the configuration of the agw role, the media relay.
type AGW struct {
	// Control is where the HTTP control API listens.
	Control netip.AddrPort
	// RTPAddress is the address the relay's terminations have, which the
	// P-CSCF writes into SDP.
	RTPAddress netip.Addr
	// MinPort and MaxPort bound the RTP ports. A fixed range lets the
	// ports be published from a container to the host.
	MinPort, MaxPort uint16
}

type rawAGW struct {
	Control    string `yaml:"control"`
	RTPAddress string `yaml:"rtp_address"`
	RTPPorts   struct {
		Min uint16 `yaml:"min"`
		Max uint16 `yaml:"max"`
	} `yaml:"rtp_ports"`
}

func (raw *rawAGW) validate() (AGW, error) {
	control, err := netip.ParseAddrPort(raw.Control)
	if err != nil {
		return AGW{}, fmt.Errorf("control: %w", err)
	}
	addr, err := netip.ParseAddr(raw.RTPAddress)
	if err != nil || addr.IsUnspecified() {
		return AGW{}, fmt.Errorf("rtp_address: %q must be an address other nodes can send to", raw.RTPAddress)
	}
	if raw.RTPPorts.Min == 0 || raw.RTPPorts.Max < raw.RTPPorts.Min+3 {
		return AGW{}, fmt.Errorf("rtp_ports: %d-%d must hold at least two even ports", raw.RTPPorts.Min, raw.RTPPorts.Max)
	}
	return AGW{Control: control, RTPAddress: addr, MinPort: raw.RTPPorts.Min, MaxPort: raw.RTPPorts.Max}, nil
}

// MRFP is the configuration of the mrfp role: a media node as for the
// agw role, and the announcements it can play.
type MRFP struct {
	AGW
	// Announcements are the WAV files of the announcements, by the name
	// that an MRFC asks for as /provisioned/<name> (RFC 4240 3.3).
	Announcements map[string]string
}

type rawMRFP struct {
	rawAGW        `yaml:",inline"`
	Announcements map[string]string `yaml:"announcements"`
}

func (raw *rawMRFP) validate() (MRFP, error) {
	node, err := raw.rawAGW.validate()
	if err != nil {
		return MRFP{}, err
	}
	for name, file := range raw.Announcements {
		if !isAnnouncementID(name) {
			return MRFP{}, fmt.Errorf("announcements: %q must be letters and digits only", name)
		}
		if file == "" {
			return MRFP{}, fmt.Errorf("announcements.%s: the file is missing", name)
		}
	}
	return MRFP{AGW: node, Announcements: raw.Announcements}, nil
}

// isAnnouncementID is RFC 4240 3.3: announcement-id = 1*(ALPHA / DIGIT).
func isAnnouncementID(name string) bool {
	return name != "" && strings.IndexFunc(name, func(c rune) bool {
		return !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9')
	}) < 0
}

// MRFC is the configuration of the mrfc role.
type MRFC struct {
	// SIP is where the MRFC listens. Its host names this instance: it
	// goes into the Contact of each dialog, so that the requests within
	// a dialog come back here.
	SIP SIP
	// Notify is where the HTTP server listens that takes the Notify of
	// the MRFPs.
	Notify netip.AddrPort
	// MRFPs are the URLs of the control APIs of the MRFPs that the MRFC
	// may use, such as http://mrfp:8080. Every instance lists the same,
	// in the same order, so that each picks the same MRFP for a new
	// conference.
	MRFPs []string
}

// NotifyURL is the URL of the Notify server of this instance, which the
// MRFC gives the MRFPs.
func (c MRFC) NotifyURL() string {
	return "http://" + c.SIP.Host + ":" + strconv.Itoa(int(c.Notify.Port())) + "/notify"
}

type rawMRFC struct {
	SIP    rawSIP   `yaml:"sip"`
	Notify string   `yaml:"notify"`
	MRFPs  []string `yaml:"mrfp"`
}

func (raw *rawMRFC) validate() (MRFC, error) {
	sip, err := raw.SIP.validate()
	if err != nil {
		return MRFC{}, err
	}
	notify, err := netip.ParseAddrPort(raw.Notify)
	if err != nil || notify.Port() == 0 {
		return MRFC{}, fmt.Errorf("notify: %q must be an address and a port", raw.Notify)
	}
	if len(raw.MRFPs) == 0 {
		return MRFC{}, errors.New("mrfp: at least one MRFP is required")
	}
	seen := map[string]bool{}
	for _, u := range raw.MRFPs {
		if !strings.HasPrefix(u, "http://") {
			return MRFC{}, fmt.Errorf("mrfp: %q is not an http:// URL", u)
		}
		if seen[u] {
			return MRFC{}, fmt.Errorf("mrfp: %q is listed twice", u)
		}
		seen[u] = true
	}
	return MRFC{SIP: sip, Notify: notify, MRFPs: raw.MRFPs}, nil
}

type rawSIP struct {
	Listen             string        `yaml:"listen"`
	Host               string        `yaml:"host"`
	Pool               string        `yaml:"pool"`
	TransactionTimeout time.Duration `yaml:"transaction_timeout"`
}

type rawPCSCF struct {
	SIP              rawSIP `yaml:"sip"`
	VisitedNetworkID string `yaml:"visited_network_id"`
	MediaRelay       string `yaml:"media_relay"`
	HostAccess       *struct {
		Gateway string `yaml:"gateway"`
		Address string `yaml:"address"`
	} `yaml:"host_access"`
	SecAgree *struct {
		ProtectedServerPort uint16 `yaml:"protected_server_port"`
	} `yaml:"sec_agree"`
}

type rawICSCF struct {
	SIP           rawSIP   `yaml:"sip"`
	PSISubdomains []string `yaml:"psi_subdomains"`
	SCSCFs        []struct {
		Name         string   `yaml:"name"`
		Capabilities []uint32 `yaml:"capabilities"`
	} `yaml:"scscfs"`
}

type rawSCSCF struct {
	SIP         rawSIP `yaml:"sip"`
	MinExpires  int    `yaml:"min_expires"`
	MaxExpires  int    `yaml:"max_expires"`
	IBCF        string `yaml:"ibcf"`
	Restoration bool   `yaml:"restoration"`
}

type rawHSS struct {
	VisitedNetworks    []string `yaml:"visited_networks"`
	ProvisioningListen string   `yaml:"provisioning_listen"`
	ServerCapabilities struct {
		Mandatory []uint32 `yaml:"mandatory"`
		Optional  []uint32 `yaml:"optional"`
	} `yaml:"server_capabilities"`
	Subscriptions []struct {
		PrivateIdentity  string   `yaml:"private_identity"`
		PublicIdentities []string `yaml:"public_identities"`
		BarredIdentities []string `yaml:"barred_identities"`
		Password         string   `yaml:"password"`
		// AKA is in the hexadecimal form of hss.ParseAKACredentials.
		AKA *struct {
			K   string `yaml:"k"`
			OPc string `yaml:"opc"`
			AMF string `yaml:"amf"`
			SQN string `yaml:"sqn"`
		} `yaml:"aka"`
		// UtPassword is the password of the Ut credential, which the HSS
		// keeps as H(A1) only (hss.UtCredentialData).
		UtPassword string `yaml:"ut_password"`
		// MMTelServices replaces the top-level one for this subscription.
		MMTelServices string `yaml:"mmtel_services"`
		// CustomizedAlertingTone provisions CAT for this subscription
		// (TS 24.182 4.5.2): the tone, and whether it plays.
		CustomizedAlertingTone *struct {
			Tone   string `yaml:"tone"`
			Active bool   `yaml:"active"`
		} `yaml:"customized_alerting_tone"`
	} `yaml:"subscriptions"`
	// MMTelServices is the first MMTelServices document of every
	// subscription that has none of its own, those of the provisioning
	// API included (hss.Config).
	MMTelServices string `yaml:"mmtel_services"`
	// InitialFilterCriteria apply to every subscription (hss.Config).
	InitialFilterCriteria []rawInitialFilterCriteria `yaml:"initial_filter_criteria"`
}

func (raw rawSIP) validate() (SIP, error) {
	addr, err := netip.ParseAddrPort(raw.Listen)
	if err != nil {
		return SIP{}, fmt.Errorf("sip.listen: %w", err)
	}
	if addr.Port() == 0 {
		return SIP{}, errors.New("sip.listen: port 0")
	}
	if raw.Host == "" {
		return SIP{}, errors.New("sip.host: required")
	}
	if net.ParseIP(raw.Host) != nil {
		return SIP{}, errors.New("sip.host: must be a name that DNS resolves, not an address")
	}
	if raw.Pool != "" {
		if net.ParseIP(raw.Pool) != nil {
			return SIP{}, errors.New("sip.pool: must be a name that DNS resolves, not an address")
		}
		// RFC 3263 4.2: a URI with a port is not looked up with SRV, so
		// it would reach one instance only.
		if strings.Contains(raw.Pool, ":") {
			return SIP{}, errors.New("sip.pool: a name without a port")
		}
	}
	if raw.TransactionTimeout < 0 {
		return SIP{}, fmt.Errorf("sip.transaction_timeout: %v is negative", raw.TransactionTimeout)
	}
	return SIP{Listen: addr, Host: raw.Host, Pool: raw.Pool, TransactionTimeout: raw.TransactionTimeout}, nil
}

// parseSIPURI reads the SIP URI of a node, which needs a host.
func parseSIPURI(s string) (sip.URI, error) {
	u, err := sip.ParseURI(s)
	if err != nil || u.Host == "" {
		return sip.URI{}, fmt.Errorf("%q is not a SIP URI with a host", s)
	}
	return u, nil
}

func (raw *rawPCSCF) validate() (PCSCF, error) {
	sip, err := raw.SIP.validate()
	if err != nil {
		return PCSCF{}, err
	}
	if raw.VisitedNetworkID == "" {
		return PCSCF{}, errors.New("visited_network_id: required")
	}
	c := PCSCF{SIP: sip, VisitedNetworkID: raw.VisitedNetworkID, MediaRelay: raw.MediaRelay}
	if c.MediaRelay != "" && !strings.HasPrefix(c.MediaRelay, "http://") {
		return PCSCF{}, fmt.Errorf("media_relay: %q is not an http:// URL", c.MediaRelay)
	}
	if h := raw.HostAccess; h != nil {
		gw, err1 := netip.ParseAddr(h.Gateway)
		addr, err2 := netip.ParseAddr(h.Address)
		if err1 != nil || err2 != nil {
			return PCSCF{}, fmt.Errorf("host_access: gateway %q and address %q must be IP addresses", h.Gateway, h.Address)
		}
		c.HostAccess = &HostAccess{Gateway: gw, Address: addr}
	}
	if a := raw.SecAgree; a != nil {
		// TS 33.203 7.1: never one of the standard SIP ports.
		if port := a.ProtectedServerPort; port == 0 || port == 5060 || port == 5061 || port == c.SIP.Listen.Port() {
			return PCSCF{}, fmt.Errorf("sec_agree.protected_server_port: %d must be a port of its own, not 5060 or 5061", port)
		}
		c.ProtectedServerPort = a.ProtectedServerPort
	}
	return c, nil
}

func (raw *rawICSCF) validate(homeDomain string) (ICSCF, error) {
	sip, err := raw.SIP.validate()
	if err != nil {
		return ICSCF{}, err
	}
	c := ICSCF{SIP: sip}
	for i, d := range raw.PSISubdomains {
		if err := checkSubdomain(d, homeDomain); err != nil {
			return ICSCF{}, fmt.Errorf("psi_subdomains[%d]: %w", i, err)
		}
		c.PSISubdomains = append(c.PSISubdomains, d)
	}
	if len(raw.SCSCFs) == 0 {
		return ICSCF{}, errors.New("scscfs: at least one S-CSCF is required")
	}
	for i, s := range raw.SCSCFs {
		if !strings.HasPrefix(s.Name, "sip:") {
			return ICSCF{}, fmt.Errorf("scscfs[%d].name: %q is not a SIP URI", i, s.Name)
		}
		c.SCSCFs = append(c.SCSCFs, SCSCFEntry{Name: s.Name, Capabilities: s.Capabilities})
	}
	return c, nil
}

// TS 24.229 5.1.1.2.1 has the UE ask for 600 000 s. These defaults keep
// expiry and re-registration short enough to watch.
const (
	defaultMinExpires = 60 * time.Second
	defaultMaxExpires = 3600 * time.Second
)

func (raw *rawSCSCF) validate() (SCSCF, error) {
	sip, err := raw.SIP.validate()
	if err != nil {
		return SCSCF{}, err
	}
	c := SCSCF{SIP: sip, MinExpires: defaultMinExpires, MaxExpires: defaultMaxExpires, Restoration: raw.Restoration}
	if raw.MinExpires != 0 {
		c.MinExpires = time.Duration(raw.MinExpires) * time.Second
	}
	if raw.MaxExpires != 0 {
		c.MaxExpires = time.Duration(raw.MaxExpires) * time.Second
	}
	if c.MinExpires <= 0 || c.MaxExpires < c.MinExpires {
		return SCSCF{}, fmt.Errorf("min_expires %v and max_expires %v: need 0 < min <= max", c.MinExpires, c.MaxExpires)
	}
	if raw.IBCF != "" {
		if c.IBCF, err = parseSIPURI(raw.IBCF); err != nil {
			return SCSCF{}, fmt.Errorf("ibcf: %w", err)
		}
	}
	return c, nil
}

func (raw *rawHSS) validate(homeDomain string) (hss.Config, error) {
	c := hss.Config{
		Realm:           homeDomain,
		VisitedNetworks: raw.VisitedNetworks,
		ServerCapabilities: cx.ServerCapabilities{
			MandatoryCapabilities: raw.ServerCapabilities.Mandatory,
			OptionalCapabilities:  raw.ServerCapabilities.Optional,
		},
	}
	if len(c.VisitedNetworks) == 0 {
		c.VisitedNetworks = []string{homeDomain}
	}
	if raw.ProvisioningListen != "" {
		if err := checkHostPort(raw.ProvisioningListen); err != nil {
			return hss.Config{}, fmt.Errorf("provisioning_listen: %w", err)
		}
		c.ProvisioningListen = raw.ProvisioningListen
	}
	if raw.MMTelServices != "" {
		d, err := hss.MMTelServicesData([]byte(raw.MMTelServices))
		if err != nil {
			return hss.Config{}, err
		}
		c.MMTelServices = &d
	}
	if len(raw.Subscriptions) == 0 {
		return hss.Config{}, errors.New("subscriptions: at least one is required")
	}
	// The store is the one that knows when an identity is in two
	// subscriptions; this one only checks the file and is dropped.
	store, _ := hss.NewMemoryStore()
	for i, s := range raw.Subscriptions {
		sub := hss.Subscription{
			PrivateIdentity:  s.PrivateIdentity,
			PublicIdentities: s.PublicIdentities,
			BarredIdentities: s.BarredIdentities,
			Password:         s.Password,
		}
		if s.AKA != nil {
			creds, err := hss.ParseAKACredentials(s.AKA.K, s.AKA.OPc, s.AKA.AMF, s.AKA.SQN)
			if err != nil {
				return hss.Config{}, fmt.Errorf("subscriptions[%d].%w", i, err)
			}
			sub.AKA = creds
		}
		var cat *mmtel.CustomizedAlertingTone
		if t := s.CustomizedAlertingTone; t != nil {
			cat = &mmtel.CustomizedAlertingTone{Tone: t.Tone, Active: t.Active}
		}
		data, err := c.RepositoryData(s.PrivateIdentity, s.MMTelServices, s.UtPassword, cat)
		if err != nil {
			return hss.Config{}, fmt.Errorf("subscriptions[%d].%w", i, err)
		}
		sub.RepositoryData = data
		if err := sub.Validate(); err != nil {
			return hss.Config{}, fmt.Errorf("subscriptions[%d].%w", i, err)
		}
		if err := store.Add(sub); err != nil {
			return hss.Config{}, fmt.Errorf("subscriptions[%d]: %w", i, err)
		}
		c.Subscriptions = append(c.Subscriptions, sub)
	}
	criteria, err := parseInitialFilterCriteria(raw.InitialFilterCriteria)
	if err != nil {
		return hss.Config{}, err
	}
	c.InitialFilterCriteria = criteria
	return c, nil
}

// Dashboard is the configuration of the dashboard role.
type Dashboard struct {
	// HEPListen is the UDP address of the HEP collector, such as :9060.
	HEPListen string
	// HTTPListen is where the web UI and its API are served.
	HTTPListen string
	// HSSProvisioning is the URL of the HSS provisioning API, such as
	// http://hss:8081. Empty leaves the subscriber endpoints unavailable.
	HSSProvisioning string
	// EPC is the URL of the API of the EPC stub, such as
	// http://epc:8080, whose endpoints for the state of a UE and for
	// the procedures that the network initiates the dashboard passes
	// on. Empty leaves them unavailable.
	EPC string
	// UEs are the UE simulators ("ue serve") that the web UI drives, in
	// the order it shows them.
	UEs []UESimulator
}

// UESimulator is one UE simulator that the dashboard controls.
type UESimulator struct {
	// Name is how the dashboard API names the UE, as in
	// /api/ues/{name}/call: letters, digits and hyphens.
	Name string
	// Control is the URL of its control API, such as http://ue-a:8090.
	Control string
}

type rawDashboard struct {
	HEPListen       string `yaml:"hep_listen"`
	HTTPListen      string `yaml:"http_listen"`
	HSSProvisioning string `yaml:"hss_provisioning"`
	EPC             string `yaml:"epc"`
	UEs             []struct {
		Name    string `yaml:"name"`
		Control string `yaml:"control"`
	} `yaml:"ues"`
}

func (raw *rawDashboard) validate() (Dashboard, error) {
	if err := checkHostPort(raw.HEPListen); err != nil {
		return Dashboard{}, fmt.Errorf("hep_listen: %w", err)
	}
	if err := checkHostPort(raw.HTTPListen); err != nil {
		return Dashboard{}, fmt.Errorf("http_listen: %w", err)
	}
	if raw.HSSProvisioning != "" && !strings.HasPrefix(raw.HSSProvisioning, "http://") {
		return Dashboard{}, fmt.Errorf("hss_provisioning: %q is not an http:// URL", raw.HSSProvisioning)
	}
	if raw.EPC != "" && !strings.HasPrefix(raw.EPC, "http://") {
		return Dashboard{}, fmt.Errorf("epc: %q is not an http:// URL", raw.EPC)
	}
	d := Dashboard{HEPListen: raw.HEPListen, HTTPListen: raw.HTTPListen, HSSProvisioning: raw.HSSProvisioning, EPC: raw.EPC}
	names := map[string]bool{}
	for i, u := range raw.UEs {
		if !isUEName(u.Name) {
			return Dashboard{}, fmt.Errorf("ues[%d].name: %q is not made of letters, digits and hyphens", i, u.Name)
		}
		if names[u.Name] {
			return Dashboard{}, fmt.Errorf("ues[%d].name: %q is used twice", i, u.Name)
		}
		names[u.Name] = true
		if !strings.HasPrefix(u.Control, "http://") {
			return Dashboard{}, fmt.Errorf("ues[%d].control: %q is not an http:// URL", i, u.Control)
		}
		d.UEs = append(d.UEs, UESimulator{Name: u.Name, Control: u.Control})
	}
	return d, nil
}

// isUEName keeps a name as it is in a URL path.
func isUEName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// IPSMGW is the configuration of the ipsmgw role.
type IPSMGW struct {
	// SIP is where the IP-SM-GW listens. Its URI is the ServerName of the
	// filter criteria that send requests here, and must equal it.
	SIP SIP
	// Number is the E.164 number of the IP-SM-GW, digits only, as Sh
	// writes it into the HSS for the users it serves (TS 29.328 7.6.16).
	Number string
	// ServiceCentre is the address of the SC inside the node: the RP-DA
	// that a short message from a UE must carry, and the RP-OA of a short
	// message to a UE (TS 24.011 8.2.5.1, 8.2.5.2).
	ServiceCentre sms.Address
	// IBCF is the IBCF that a short message for a user of another IMS
	// network leaves through. With it, the SC finds such users by ENUM.
	// Its zero value has no host: the SC knows the users of
	// the home network only.
	IBCF sip.URI
}

type rawIPSMGW struct {
	SIP           rawSIP `yaml:"sip"`
	Number        string `yaml:"number"`
	ServiceCentre string `yaml:"service_centre"`
	IBCF          string `yaml:"ibcf"`
}

func (raw *rawIPSMGW) validate() (IPSMGW, error) {
	sip, err := raw.SIP.validate()
	if err != nil {
		return IPSMGW{}, err
	}
	number, err := e164Digits(raw.Number)
	if err != nil {
		return IPSMGW{}, fmt.Errorf("number: %w", err)
	}
	sc, err := e164Digits(raw.ServiceCentre)
	if err != nil {
		return IPSMGW{}, fmt.Errorf("service_centre: %w", err)
	}
	c := IPSMGW{SIP: sip, Number: number, ServiceCentre: sms.InternationalNumber("+" + sc)}
	if raw.IBCF != "" {
		if c.IBCF, err = parseSIPURI(raw.IBCF); err != nil {
			return IPSMGW{}, fmt.Errorf("ibcf: %w", err)
		}
	}
	return c, nil
}

// e164Digits reads an international number such as +12025550199 and
// returns its digits. An E.164 number has at most 15 digits.
func e164Digits(s string) (string, error) {
	digits, ok := strings.CutPrefix(s, "+")
	if !ok || !sh.ValidMSISDN(digits) {
		return "", fmt.Errorf("%q is not an international number such as +12025550199", s)
	}
	return digits, nil
}

// EPC is the configuration of the epc role.
type EPC struct {
	// API is where the API of the stub listens: the UE API, the state
	// of a UE, and the procedures that the network initiates.
	API netip.AddrPort
	epc.Config
}

type rawEPC struct {
	API            string   `yaml:"api"`
	APN            string   `yaml:"apn"`
	PCSCFAddresses []string `yaml:"pcscf_addresses"`
	Subscribers    []struct {
		IMSI    string `yaml:"imsi"`
		Address string `yaml:"address"`
	} `yaml:"subscribers"`
	Cell struct {
		MCC string `yaml:"mcc"`
		MNC string `yaml:"mnc"`
		TAC uint16 `yaml:"tac"`
		ECI uint32 `yaml:"eci"`
	} `yaml:"cell"`
}

func (raw *rawEPC) validate() (EPC, error) {
	api, err := netip.ParseAddrPort(raw.API)
	if err != nil {
		return EPC{}, fmt.Errorf("api: %w", err)
	}
	if !isAPN(raw.APN) {
		return EPC{}, fmt.Errorf("apn: %q is not made of letters, digits, hyphens and dots (TS 23.003 9.1)", raw.APN)
	}
	c := EPC{API: api, Config: epc.Config{APN: raw.APN}}
	if len(raw.PCSCFAddresses) == 0 {
		return EPC{}, errors.New("pcscf_addresses: at least one is required (TS 24.229 L.2.2.1)")
	}
	for i, s := range raw.PCSCFAddresses {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() {
			return EPC{}, fmt.Errorf("pcscf_addresses[%d]: %q is not an IPv4 address", i, s)
		}
		c.PCSCFAddresses = append(c.PCSCFAddresses, a)
	}
	if len(raw.Subscribers) == 0 {
		return EPC{}, errors.New("subscribers: at least one is required")
	}
	imsis, addrs := map[string]bool{}, map[netip.Addr]bool{}
	for i, s := range raw.Subscribers {
		if !isDigits(s.IMSI, 6, 15) {
			return EPC{}, fmt.Errorf("subscribers[%d].imsi: %q is not an IMSI of 6 to 15 digits (TS 23.003 2.2)", i, s.IMSI)
		}
		a, err := netip.ParseAddr(s.Address)
		if err != nil || !a.Is4() || a.IsUnspecified() {
			return EPC{}, fmt.Errorf("subscribers[%d].address: %q is not an IPv4 address of a UE", i, s.Address)
		}
		if imsis[s.IMSI] || addrs[a] {
			return EPC{}, fmt.Errorf("subscribers[%d]: IMSI %s or address %s is used twice", i, s.IMSI, a)
		}
		imsis[s.IMSI], addrs[a] = true, true
		c.Subscribers = append(c.Subscribers, epc.Subscriber{IMSI: s.IMSI, Address: a})
	}
	cell := raw.Cell
	// TS 23.003 2.2: the MCC has three digits and the MNC two or three. TS
	// 23.003 19.6: the ECI has 28 bits.
	if !isDigits(cell.MCC, 3, 3) || !isDigits(cell.MNC, 2, 3) {
		return EPC{}, fmt.Errorf("cell: MCC %q and MNC %q must be three digits and two or three digits", cell.MCC, cell.MNC)
	}
	if cell.ECI >= 1<<28 {
		return EPC{}, fmt.Errorf("cell.eci: %d does not fit in 28 bits", cell.ECI)
	}
	c.Cell = epc.SystemInformation{
		TrackingAreaIdentity: epc.TrackingAreaIdentity{PLMN: epc.PLMNIdentity{MCC: cell.MCC, MNC: cell.MNC}, TAC: cell.TAC},
		CellIdentity:         cell.ECI,
	}
	return c, nil
}

func isDigits(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isAPN accepts the labels of an APN: letters, digits and hyphens,
// separated by dots (TS 23.003 9.1).
func isAPN(s string) bool {
	if s == "" {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// MMTel is the configuration of the mmtel role, the MMTel AS.
type MMTel struct {
	// SIP is where the AS listens. Its host goes into the Contact of
	// both legs of every call, so it names this instance: the requests
	// within a dialog come back to the instance that has the call. The
	// server_name of the filter criteria may name the service instead,
	// which DNS spreads over the instances.
	SIP SIP
	// HomeCountryCode is the E.164 country code of the home network,
	// digits only, such as 1. Outgoing communication barring needs it to
	// tell international calls (TS 24.611 4.9.1.4); empty takes no call
	// for international.
	HomeCountryCode string
	// XCAP is the XCAP server of Ut, with which the users set up their
	// services (TS 24.623, GSMA IR.92 2.3.2).
	XCAP XCAP
	// MRFC is the SIP URI of the MRFC that plays the media of the
	// services: the customized alerting tones (TS 24.182) and the
	// announcements (TS 24.628). The AS reaches it over Mr' and DNS
	// resolves its host. Empty gives the calls no media of the MRF.
	MRFC string
	// Announcement is the announcement that the AS has the MRFC play to
	// the caller before it rejects a call for a service, such as notice,
	// one that the MRFPs have provisioned (RFC 4240 3.3). Empty plays
	// none, and the call is rejected at once.
	Announcement string
	// ConferenceDomain is the subdomain of the home domain whose public
	// service identities are the conferences of this instance: each
	// conference URI is sip:<conference>@<ConferenceDomain> (TS 23.003
	// 13.5, TS 23.228 5.4.12.3). DNS resolves it to this instance, so a
	// request for a conference reaches the instance that has it. Every
	// instance has its own. Needed when mrfc is set, since the MRFC
	// mixes the conferences.
	ConferenceDomain string
}

type rawMMTel struct {
	SIP              rawSIP   `yaml:"sip"`
	HomeCountryCode  string   `yaml:"home_country_code"`
	XCAP             *rawXCAP `yaml:"xcap"`
	MRFC             string   `yaml:"mrfc"`
	Announcement     string   `yaml:"announcement"`
	ConferenceDomain string   `yaml:"conference_domain"`
}

func (raw *rawMMTel) validate(homeDomain string) (MMTel, error) {
	listen, err := raw.SIP.validate()
	if err != nil {
		return MMTel{}, err
	}
	// ITU-T E.164 country codes have one to three digits.
	if raw.HomeCountryCode != "" && !isDigits(raw.HomeCountryCode, 1, 3) {
		return MMTel{}, fmt.Errorf("home_country_code: %q is not one to three digits", raw.HomeCountryCode)
	}
	c := MMTel{SIP: listen, HomeCountryCode: raw.HomeCountryCode}
	if raw.XCAP == nil {
		return MMTel{}, errors.New("xcap: required")
	}
	if c.XCAP, err = raw.XCAP.validate(); err != nil {
		return MMTel{}, fmt.Errorf("xcap: %w", err)
	}
	if raw.MRFC != "" {
		if u, err := sip.ParseURI(raw.MRFC); err != nil || u.User != "" {
			return MMTel{}, fmt.Errorf("mrfc: %q is not a SIP URI of a host", raw.MRFC)
		}
	}
	switch {
	case raw.Announcement != "" && raw.MRFC == "":
		return MMTel{}, errors.New("announcement: needs the mrfc that plays it")
	case raw.Announcement != "" && !isAnnouncementID(raw.Announcement):
		return MMTel{}, fmt.Errorf("announcement: %q must be letters and digits only", raw.Announcement)
	}
	switch {
	case raw.ConferenceDomain != "" && raw.MRFC == "":
		return MMTel{}, errors.New("conference_domain: needs the mrfc that mixes the conferences")
	case raw.ConferenceDomain == "" && raw.MRFC != "":
		return MMTel{}, errors.New("conference_domain: required with mrfc, for the conference URIs")
	case raw.ConferenceDomain != "":
		if err := checkSubdomain(raw.ConferenceDomain, homeDomain); err != nil {
			return MMTel{}, fmt.Errorf("conference_domain: %w", err)
		}
	}
	c.MRFC, c.Announcement, c.ConferenceDomain = raw.MRFC, raw.Announcement, raw.ConferenceDomain
	return c, nil
}

// checkSubdomain checks that name is a host name below homeDomain: a
// public service identity takes a domain that the operator of the home
// network defines (TS 23.003 13.5), with no port and no address.
func checkSubdomain(name, homeDomain string) error {
	u, err := sip.ParseURI("sip:" + name)
	switch {
	case err != nil || u.Port != 0 || u.User != "" || u.Host != name:
		return fmt.Errorf("%q is not a host name", name)
	case homeDomain == "" || !strings.HasSuffix(strings.ToLower(name), "."+strings.ToLower(homeDomain)):
		return fmt.Errorf("%q is not a subdomain of the home domain %q", name, homeDomain)
	}
	return nil
}
