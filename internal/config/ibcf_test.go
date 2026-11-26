package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const ibcfYAML = `
node: ibcf
roles: [ibcf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
ibcf:
  sip: {listen: 172.28.0.90:5060, host: ibcf.ims.mnc001.mcc001.3gppnetwork.org}
  external_sip: {listen: 172.28.2.10:5060, host: ibcf.nni.mnc001.example.net}
  icscf: sip:icscf.ims.mnc001.mcc001.3gppnetwork.org:5060
  trgw:
    control: http://trgw.ims.mnc001.mcc001.3gppnetwork.org:8080
    core_address: 172.28.0.91
    peer_address: 172.28.2.11
  peers:
    - profile_file: peer-b.yaml
      entry_point: sip:ibcf.nni.mnc002.example.net:5060
      sources: [172.28.2.20]
`

const profileYAML = `
name: peer-b
domain: ims.mnc002.mcc001.3gppnetwork.org
trusted: true
transports: [udp, tcp]
methods: [MESSAGE]
option_tags: {precondition: reject}
headers:
  - {header: User-Agent, action: remove, direction: exit}
number_format: sip
local_numbers:
  - {phone_context: "+1-202", global_prefix: "+1202"}
codecs: [AMR-WB, PCMU, telephone-event]
`

// writeIBCF writes the node's file and the profile next to it, and
// returns the path of the node's file.
func writeIBCF(t *testing.T, node, profile string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "peer-b.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ibcf.yaml")
	if err := os.WriteFile(path, []byte(node), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadIBCF(t *testing.T) {
	cfg, err := Load(writeIBCF(t, ibcfYAML, profileYAML))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.IBCF
	if c == nil {
		t.Fatal("no ibcf section")
	}
	if c.SIP.URI() != "sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060" || c.ExternalSIP.URI() != "sip:ibcf.nni.mnc001.example.net:5060" {
		t.Errorf("URIs %s and %s", c.SIP.URI(), c.ExternalSIP.URI())
	}
	if c.ICSCF.Host != "icscf.ims.mnc001.mcc001.3gppnetwork.org" || c.ICSCF.Port != 5060 {
		t.Errorf("icscf = %+v", c.ICSCF)
	}
	if c.TrGW.CoreAddress != netip.MustParseAddr("172.28.0.91") || c.TrGW.PeerAddress != netip.MustParseAddr("172.28.2.11") {
		t.Errorf("trgw = %+v", c.TrGW)
	}
	if len(c.Peers) != 1 {
		t.Fatalf("peers = %+v", c.Peers)
	}
	peer := c.Peers[0]
	if peer.EntryPoint != "sip:ibcf.nni.mnc002.example.net:5060" || !slices.Equal(peer.Sources, []netip.Prefix{netip.MustParsePrefix("172.28.2.20/32")}) {
		t.Errorf("peer = %+v", peer)
	}
	p := peer.Profile
	if p.Name != "peer-b" || !p.Trusted || p.NumberFormat != interconnect.NumberFormatSIP {
		t.Errorf("profile = %+v", p)
	}
	if !slices.Equal(p.Transports, []sip.Transport{sip.UDP, sip.TCP}) || !slices.Equal(p.Methods, []sip.Method{sip.MESSAGE}) {
		t.Errorf("transports %v and methods %v, want them in upper case", p.Transports, p.Methods)
	}
	if p.OptionTags["precondition"] != interconnect.OptionReject || len(p.Headers) != 1 || p.Headers[0].Direction != interconnect.Exit {
		t.Errorf("option tags %v and headers %+v", p.OptionTags, p.Headers)
	}
	if len(p.LocalNumbers) != 1 || p.LocalNumbers[0].GlobalPrefix != "+1202" || !slices.Equal(p.Codecs, []string{"AMR-WB", "PCMU", "telephone-event"}) {
		t.Errorf("local numbers %+v and codecs %v", p.LocalNumbers, p.Codecs)
	}
}

// TestLoadIBCFPools: an instance of an IBCF that runs as several names
// itself with the host of each side, and the role with the pool of each
// side.
func TestLoadIBCFPools(t *testing.T) {
	node := strings.Replace(ibcfYAML, "host: ibcf.ims.mnc001.mcc001.3gppnetwork.org}",
		"host: ibcf-1.ims.mnc001.mcc001.3gppnetwork.org, pool: ibcf.ims.mnc001.mcc001.3gppnetwork.org}", 1)
	node = strings.Replace(node, "host: ibcf.nni.mnc001.example.net}",
		"host: ibcf-1.nni.mnc001.example.net, pool: ibcf.nni.mnc001.example.net}", 1)
	cfg, err := Load(writeIBCF(t, node, profileYAML))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.IBCF
	if c.SIP.Host != "ibcf-1.ims.mnc001.mcc001.3gppnetwork.org" || c.Pool != "ibcf.ims.mnc001.mcc001.3gppnetwork.org" {
		t.Errorf("internal side %q, pool %q", c.SIP.Host, c.Pool)
	}
	if c.ExternalSIP.Host != "ibcf-1.nni.mnc001.example.net" || c.ExternalPool != "ibcf.nni.mnc001.example.net" {
		t.Errorf("external side %q, pool %q", c.ExternalSIP.Host, c.ExternalPool)
	}
}

func TestLoadIBCFRejects(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		// profile, when set, replaces profileYAML.
		profile string
		wantErr string
	}{
		{name: "pool given as an address", old: "host: ibcf.ims.mnc001.mcc001.3gppnetwork.org}", new: "host: ibcf.ims.mnc001.mcc001.3gppnetwork.org, pool: 172.28.0.90}", wantErr: "sip.pool: must be a name"},
		{name: "external pool with a port", old: "host: ibcf.nni.mnc001.example.net}", new: "host: ibcf.nni.mnc001.example.net, pool: ibcf.nni.mnc001.example.net:5060}", wantErr: "external_sip.pool: a name without a port"},
		{name: "same host on both sides", old: "host: ibcf.nni.mnc001.example.net", new: "host: ibcf.ims.mnc001.mcc001.3gppnetwork.org", wantErr: "external_sip.host: must differ"},
		{name: "external side without a port", old: "listen: 172.28.2.10:5060", new: "listen: 172.28.2.10", wantErr: "external_sip.listen"},
		{name: "both sides on one address and port", old: "listen: 172.28.2.10:5060", new: "listen: 172.28.0.90:5060", wantErr: "ibcf and the external side of ibcf both listen on port 5060"},
		{name: "external side on every address", old: "listen: 172.28.2.10:5060", new: "listen: 0.0.0.0:5060", wantErr: "both listen on port 5060"},
		{name: "no I-CSCF", old: "icscf: sip:icscf.ims.mnc001.mcc001.3gppnetwork.org:5060", new: "icscf: icscf", wantErr: "icscf:"},
		{name: "unknown home number format", old: "icscf: sip:", new: "home_number_format: e164\n  icscf: sip:", wantErr: "home_number_format"},
		{name: "TrGW not a URL", old: "control: http://trgw", new: "control: trgw", wantErr: "trgw.control"},
		{name: "TrGW without a peer address", old: "peer_address: 172.28.2.11", new: "peer_address: 0.0.0.0", wantErr: "trgw.peer_address"},
		{name: "no peers", old: "  peers:\n    - profile_file: peer-b.yaml\n      entry_point: sip:ibcf.nni.mnc002.example.net:5060\n      sources: [172.28.2.20]\n", new: "", wantErr: "peers: at least one"},
		{name: "no profile file", old: "profile_file: peer-b.yaml", new: "profile_file: missing.yaml", wantErr: "peers[0].profile_file"},
		{name: "bad source", old: "sources: [172.28.2.20]", new: "sources: [ibcf-b]", wantErr: "peers[0].sources[0]"},
		{name: "bad entry point", old: "entry_point: sip:ibcf.nni.mnc002.example.net:5060", new: "entry_point: ibcf-b", wantErr: "entry point"},
		{name: "unknown key in the profile", profile: profileYAML + "trust: true\n", wantErr: "field trust not found"},
		{name: "profile without a transport", profile: strings.Replace(profileYAML, "transports: [udp, tcp]\n", "", 1), wantErr: "no transport"},
		{name: "profile with SCTP", profile: strings.Replace(profileYAML, "[udp, tcp]", "[sctp]", 1), wantErr: "want UDP or TCP"},
		{name: "profile allowing a mandatory method", profile: strings.Replace(profileYAML, "[MESSAGE]", "[INVITE]", 1), wantErr: "is mandatory at every II-NNI"},
		{name: "profile with an unknown option tag policy", profile: strings.Replace(profileYAML, "precondition: reject", "precondition: drop", 1), wantErr: "unknown policy"},
		{name: "profile removing precondition", profile: strings.Replace(profileYAML, "precondition: reject", "precondition: remove", 1), wantErr: "would leave the precondition attributes"},
		{name: "profile with a bad codec", profile: strings.Replace(profileYAML, "PCMU,", "PCMU/x,", 1), wantErr: "PCMU/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, profile := ibcfYAML, profileYAML
			if tt.old != "" {
				node = strings.Replace(ibcfYAML, tt.old, tt.new, 1)
				if node == ibcfYAML {
					t.Fatalf("test edit %q did not apply", tt.old)
				}
			}
			if tt.profile != "" {
				profile = tt.profile
			}
			_, err := Load(writeIBCF(t, node, profile))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestParseIBCFProfileRelativeToTheWorkingDirectory: Parse has no file
// to take a relative path from.
func TestParseIBCFProfileRelativeToTheWorkingDirectory(t *testing.T) {
	path := writeIBCF(t, ibcfYAML, profileYAML)
	t.Chdir(filepath.Dir(path))
	if _, err := Parse(strings.NewReader(ibcfYAML)); err != nil {
		t.Fatal(err)
	}
}

// TestParseIBCFWithOtherRoles: the internal side may share port 5060
// with no other role, unless their addresses differ.
func TestParseIBCFWithOtherRoles(t *testing.T) {
	node := strings.Replace(ibcfYAML, "roles: [ibcf]", "roles: [ibcf, agw]", 1) +
		"agw: {control: 0.0.0.0:8080, rtp_address: 172.28.0.91, rtp_ports: {min: 41000, max: 41039}}\n"
	if _, err := Load(writeIBCF(t, node, profileYAML)); err != nil {
		t.Errorf("ibcf with the TrGW in one node: %v", err)
	}
	if _, err := Parse(strings.NewReader(strings.Replace(ibcfYAML, "roles: [ibcf]", "roles: [agw]", 1) + "agw: {control: 0.0.0.0:8080, rtp_address: 172.28.0.91, rtp_ports: {min: 41000, max: 41039}}\n")); err == nil || !strings.Contains(err.Error(), "ibcf: only the ibcf role") {
		t.Errorf("ibcf section without the role: %v", err)
	}
}
