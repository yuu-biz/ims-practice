package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// IBCF is the configuration of the ibcf role, the border of the home
// network towards the IMS networks it interconnects with (TS 24.229
// 5.10). The role is a B2BUA with two UAs: SIP faces the home network
// on Mx, and ExternalSIP faces the peers on Ici (TS 29.165 3.2).
type IBCF struct {
	// SIP is the internal side. Its URI, or the URI of its pool when the
	// role runs as several instances, is the one that the S-CSCF puts in
	// Route to send a request out (TS 24.229 5.4.3.2 step 12).
	SIP SIP
	// ExternalSIP is the side towards the peers. Its host is the one
	// name of the home network that the peers see, so it is a name in
	// the interconnect network, not in the home domain.
	ExternalSIP SIP
	// ICSCF is the SIP URI of the I-CSCF that the initial requests from
	// the peers go to (TS 24.229 5.10.3.2 step 4).
	ICSCF sip.URI
	// HomeNumberFormat is how numbers are written in the home network,
	// for those that come in (interconnect.Border.HomeNumberFormat).
	// Empty keeps the scheme that the peer used; the I-CSCF turns a SIP
	// URI with user=phone into a tel URI (TS 24.229 5.3.2.1 item 1 b).
	HomeNumberFormat interconnect.NumberFormat
	// TrGW is the media relay that the IBCF puts the media of each call
	// through.
	TrGW TrGW
	// Peers are the networks that the home network has an agreement
	// with, each with its interconnect profile.
	Peers []ibcf.Peer
}

// TrGW is the TrGW as the IBCF reaches it: the control API of a node of
// the agw role, and the address that the SDP shows on each side
// of the border.
type TrGW struct {
	// Control is the URL of the control API, such as
	// http://trgw.ims.mnc001.mcc001.3gppnetwork.org:8080.
	Control string
	// CoreAddress is the TrGW's address in the home network, and
	// PeerAddress the one towards the peers' TrGWs (Izi, TS 29.165 3.2).
	CoreAddress netip.Addr
	PeerAddress netip.Addr
}

type rawIBCF struct {
	SIP              rawSIP `yaml:"sip"`
	ExternalSIP      rawSIP `yaml:"external_sip"`
	ICSCF            string `yaml:"icscf"`
	HomeNumberFormat string `yaml:"home_number_format"`
	TrGW             struct {
		Control     string `yaml:"control"`
		CoreAddress string `yaml:"core_address"`
		PeerAddress string `yaml:"peer_address"`
	} `yaml:"trgw"`
	Peers []struct {
		// ProfileFile is a file that holds the interconnect profile, the
		// agreement with the peer. A relative path is relative to the
		// directory of the node's configuration file.
		ProfileFile string   `yaml:"profile_file"`
		EntryPoint  string   `yaml:"entry_point"`
		Hosts       []string `yaml:"hosts"`
		Sources     []string `yaml:"sources"`
	} `yaml:"peers"`
}

func (raw *rawIBCF) validate(homeDomain, dir string) (IBCF, error) {
	internal, err := raw.SIP.validate()
	if err != nil {
		return IBCF{}, err
	}
	external, err := raw.ExternalSIP.validate()
	if err != nil {
		return IBCF{}, fmt.Errorf("external_%w", err)
	}
	if strings.EqualFold(internal.Host, external.Host) {
		return IBCF{}, errors.New("external_sip.host: must differ from sip.host, which only the home network sees")
	}
	c := IBCF{SIP: internal, ExternalSIP: external}
	if c.ICSCF, err = sip.ParseURI(raw.ICSCF); err != nil || c.ICSCF.Host == "" {
		return IBCF{}, fmt.Errorf("icscf: %q is not a SIP URI", raw.ICSCF)
	}
	switch f := interconnect.NumberFormat(raw.HomeNumberFormat); f {
	case "", interconnect.NumberFormatTel, interconnect.NumberFormatSIP:
		c.HomeNumberFormat = f
	default:
		return IBCF{}, fmt.Errorf("home_number_format: %q is neither tel nor sip", raw.HomeNumberFormat)
	}

	tr := raw.TrGW
	if !strings.HasPrefix(tr.Control, "http://") {
		return IBCF{}, fmt.Errorf("trgw.control: %q is not an http:// URL", tr.Control)
	}
	c.TrGW.Control = tr.Control
	for _, a := range []struct {
		name string
		raw  string
		addr *netip.Addr
	}{{"core_address", tr.CoreAddress, &c.TrGW.CoreAddress}, {"peer_address", tr.PeerAddress, &c.TrGW.PeerAddress}} {
		addr, err := netip.ParseAddr(a.raw)
		if err != nil || addr.IsUnspecified() {
			return IBCF{}, fmt.Errorf("trgw.%s: %q must be an address that the other side can send RTP to", a.name, a.raw)
		}
		*a.addr = addr
	}

	if len(raw.Peers) == 0 {
		return IBCF{}, errors.New("peers: at least one peer network is required")
	}
	for i, p := range raw.Peers {
		path := p.ProfileFile
		if path == "" {
			return IBCF{}, fmt.Errorf("peers[%d].profile_file: required", i)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		profile, err := LoadProfile(path)
		if err != nil {
			return IBCF{}, fmt.Errorf("peers[%d].profile_file: %w", i, err)
		}
		peer := ibcf.Peer{Profile: profile, EntryPoint: p.EntryPoint, Hosts: p.Hosts}
		for j, s := range p.Sources {
			prefix, err := parseAddressOrPrefix(s)
			if err != nil {
				return IBCF{}, fmt.Errorf("peers[%d].sources[%d]: %w", i, j, err)
			}
			peer.Sources = append(peer.Sources, prefix)
		}
		c.Peers = append(c.Peers, peer)
	}
	// The same checks as the role makes when it starts.
	if _, err := ibcf.NewPeerTable(c.Peers...); err != nil {
		return IBCF{}, fmt.Errorf("peers: %w", err)
	}
	return c, nil
}

// parseAddressOrPrefix reads an address, which is a prefix of its full
// length, or a prefix such as 172.28.2.0/24.
func parseAddressOrPrefix(s string) (netip.Prefix, error) {
	if addr, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is neither an address nor a prefix", s)
	}
	return prefix.Masked(), nil
}

// rawProfile is an interconnect profile in a file of its own: the
// agreement with one peer network, which an operator changes without
// touching the rest of the IBCF's configuration. The keys follow the
// fields of interconnect.Profile, in snake case.
type rawProfile struct {
	Name       string            `yaml:"name"`
	Domain     string            `yaml:"domain"`
	Trusted    bool              `yaml:"trusted"`
	Transports []string          `yaml:"transports"`
	Methods    []string          `yaml:"methods"`
	OptionTags map[string]string `yaml:"option_tags"`
	Headers    []struct {
		Header    string `yaml:"header"`
		Param     string `yaml:"param"`
		Action    string `yaml:"action"`
		Value     string `yaml:"value"`
		Direction string `yaml:"direction"`
	} `yaml:"headers"`
	NumberFormat string `yaml:"number_format"`
	LocalNumbers []struct {
		PhoneContext string `yaml:"phone_context"`
		GlobalPrefix string `yaml:"global_prefix"`
	} `yaml:"local_numbers"`
	AllowLocalNumbers bool     `yaml:"allow_local_numbers"`
	Codecs            []string `yaml:"codecs"`
}

// LoadProfile reads and validates the interconnect profile in the file
// at path. Unknown keys are errors, as in a node's configuration.
func LoadProfile(path string) (interconnect.Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return interconnect.Profile{}, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var raw rawProfile
	if err := dec.Decode(&raw); err != nil {
		return interconnect.Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	p := interconnect.Profile{
		Name:              raw.Name,
		Domain:            raw.Domain,
		Trusted:           raw.Trusted,
		NumberFormat:      interconnect.NumberFormat(raw.NumberFormat),
		AllowLocalNumbers: raw.AllowLocalNumbers,
		Codecs:            raw.Codecs,
	}
	for _, t := range raw.Transports {
		p.Transports = append(p.Transports, sip.Transport(strings.ToUpper(t)))
	}
	for _, m := range raw.Methods {
		p.Methods = append(p.Methods, sip.Method(strings.ToUpper(m)))
	}
	if len(raw.OptionTags) > 0 {
		p.OptionTags = map[string]interconnect.OptionTagPolicy{}
		for tag, policy := range raw.OptionTags {
			p.OptionTags[tag] = interconnect.OptionTagPolicy(policy)
		}
	}
	for _, h := range raw.Headers {
		p.Headers = append(p.Headers, interconnect.HeaderRule{
			Header: h.Header, Param: h.Param, Action: interconnect.HeaderAction(h.Action),
			Value: h.Value, Direction: interconnect.Direction(h.Direction),
		})
	}
	for _, l := range raw.LocalNumbers {
		p.LocalNumbers = append(p.LocalNumbers, interconnect.LocalNumberPlan{PhoneContext: l.PhoneContext, GlobalPrefix: l.GlobalPrefix})
	}
	if err := p.Validate(); err != nil {
		return interconnect.Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}
