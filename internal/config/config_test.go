package config

import (
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

const scscfYAML = `
node: scscf
roles: [scscf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
scscf:
  sip:
    listen: 0.0.0.0:5060
    host: scscf.ims.mnc001.mcc001.3gppnetwork.org
  max_expires: 600
diameter:
  origin_host: scscf.ims.mnc001.mcc001.3gppnetwork.org
  origin_realm: ims.mnc001.mcc001.3gppnetwork.org
  peers:
    - address: hss.ims.mnc001.mcc001.3gppnetwork.org:3868
      origin_host: hss.ims.mnc001.mcc001.3gppnetwork.org
      kind: hss
  timers:
    tw: 10s
    tc: 5s
`

func TestParseScscf(t *testing.T) {
	cfg, err := Parse(strings.NewReader(scscfYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Node:       "scscf",
		Roles:      []Role{RoleSCSCF},
		HomeDomain: "ims.mnc001.mcc001.3gppnetwork.org",
		SCSCF: &SCSCF{
			SIP:        SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "scscf.ims.mnc001.mcc001.3gppnetwork.org"},
			MinExpires: 60 * time.Second,
			MaxExpires: 600 * time.Second,
		},
		Diameter: &Diameter{
			Identity: diameter.Identity{
				OriginHost:  "scscf.ims.mnc001.mcc001.3gppnetwork.org",
				OriginRealm: "ims.mnc001.mcc001.3gppnetwork.org",
			},
			Peers: []Peer{{
				PeerConfig: diameter.PeerConfig{
					Address:    "hss.ims.mnc001.mcc001.3gppnetwork.org:3868",
					OriginHost: "hss.ims.mnc001.mcc001.3gppnetwork.org",
				},
				Kind: PeerHSS,
			}},
			Timers: diameter.Timers{
				Watchdog:       10 * time.Second,
				WatchdogJitter: 2 * time.Second,
				Reconnect:      5 * time.Second,
				Answer:         5 * time.Second,
			},
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got  %+v\nwant %+v", cfg, want)
	}
}

// TestParseSCSCFWithAnIBCF: the IBCF that requests for other networks
// leave through (TS 24.229 5.4.3.2 steps 11 and 12).
func TestParseSCSCFWithAnIBCF(t *testing.T) {
	with := strings.Replace(scscfYAML, "  max_expires: 600\n", "  max_expires: 600\n  ibcf: sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060\n", 1)
	cfg, err := Parse(strings.NewReader(with))
	if err != nil {
		t.Fatal(err)
	}
	if u := cfg.SCSCF.IBCF; u.Host != "ibcf.ims.mnc001.mcc001.3gppnetwork.org" || u.Port != 5060 {
		t.Errorf("ibcf = %+v", u)
	}
	bad := strings.Replace(with, "ibcf: sip:", "ibcf: ", 1)
	if _, err := Parse(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "scscf: ibcf:") {
		t.Errorf("an IBCF that is no SIP URI: %v", err)
	}
}

// TestParseSIPPool: an instance of a role that runs as several
// names itself with its host and the role with the pool.
func TestParseSIPPool(t *testing.T) {
	yaml := strings.Replace(scscfYAML, "    host: scscf.ims.mnc001.mcc001.3gppnetwork.org\n",
		"    host: scscf-1.ims.mnc001.mcc001.3gppnetwork.org\n    pool: scscf.ims.mnc001.mcc001.3gppnetwork.org\n    transaction_timeout: 4s\n", 1)
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.SCSCF.SIP
	if got, want := s.URI(), "sip:scscf-1.ims.mnc001.mcc001.3gppnetwork.org:5060"; got != want {
		t.Errorf("URI() = %q, want %q", got, want)
	}
	if got, want := s.PoolURI(), "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org"; got != want {
		t.Errorf("PoolURI() = %q, want %q", got, want)
	}
	if s.TransactionTimeout != 4*time.Second {
		t.Errorf("TransactionTimeout = %v, want 4s", s.TransactionTimeout)
	}
	want := sip.DefaultTimers()
	want.TransactionTimeout = 4 * time.Second
	if got := s.Timers(); got != want {
		t.Errorf("Timers() = %+v, want %+v", got, want)
	}
}

func TestPoolURIWithoutAPoolIsTheInstanceURI(t *testing.T) {
	s := SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "scscf.ims.mnc001.mcc001.3gppnetwork.org"}
	if got, want := s.PoolURI(), "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060"; got != want {
		t.Errorf("PoolURI() = %q, want %q", got, want)
	}
}

func TestParseSCSCFRestoration(t *testing.T) {
	cfg, err := Parse(strings.NewReader(scscfYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SCSCF.Restoration {
		t.Error("Restoration is on without the key")
	}
	with := strings.Replace(scscfYAML, "  max_expires: 600\n", "  max_expires: 600\n  restoration: true\n", 1)
	if cfg, err = Parse(strings.NewReader(with)); err != nil {
		t.Fatal(err)
	}
	if !cfg.SCSCF.Restoration {
		t.Error("Restoration is off with restoration: true")
	}
}

func TestParseDefaultsTimersToRFCValues(t *testing.T) {
	yaml := strings.Replace(scscfYAML, "  timers:\n    tw: 10s\n    tc: 5s\n", "", 1)
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Diameter.Timers; got.Watchdog != 30*time.Second || got.Reconnect != 30*time.Second {
		t.Errorf("Tw = %v, Tc = %v, want 30s and 30s", got.Watchdog, got.Reconnect)
	}
}

func TestParseHEP(t *testing.T) {
	tests := []struct {
		name, hep string
		want      *HEP
	}{
		{"absent", "", nil},
		{"collector only", "hep: {collector: lab:9060}\n", &HEP{Collector: "lab:9060"}},
		{"with agent ID", "hep:\n  collector: 172.28.0.60:9060\n  agent_id: 4294967295\n", &HEP{Collector: "172.28.0.60:9060", AgentID: 4294967295}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(strings.NewReader(strings.Replace(scscfYAML, "roles: [scscf]\n", "roles: [scscf]\n"+tt.hep, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.HEP, tt.want) {
				t.Errorf("HEP = %+v, want %+v", cfg.HEP, tt.want)
			}
		})
	}
}

func TestParseStore(t *testing.T) {
	tests := []struct {
		name, store string
		want        *Store
	}{
		{"absent", "", nil},
		{"memory", "store: {kind: memory}\n", &Store{Kind: StoreMemory, Timeout: time.Second}},
		{"kind left out", "store: {}\n", &Store{Kind: StoreMemory, Timeout: time.Second}},
		{"valkey", "store:\n  kind: valkey\n  address: valkey:6379\n  prefix: scscf-a\n  timeout: 500ms\n",
			&Store{Kind: StoreValkey, Address: "valkey:6379", Prefix: "scscf-a", Timeout: 500 * time.Millisecond}},
		{"valkey with the default timeout", "store: {kind: valkey, address: 172.28.0.90:6379}\n",
			&Store{Kind: StoreValkey, Address: "172.28.0.90:6379", Timeout: time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(strings.NewReader(strings.Replace(scscfYAML, "roles: [scscf]\n", "roles: [scscf]\n"+tt.store, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.Store, tt.want) {
				t.Errorf("Store = %+v, want %+v", cfg.Store, tt.want)
			}
		})
	}
}

func TestParseMaintenance(t *testing.T) {
	cfg, err := Parse(strings.NewReader(strings.Replace(scscfYAML, "roles: [scscf]\n", "roles: [scscf]\nmaintenance: {listen: 0.0.0.0:9100}\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if want := (&Maintenance{Listen: "0.0.0.0:9100"}); !reflect.DeepEqual(cfg.Maintenance, want) {
		t.Errorf("Maintenance = %+v, want %+v", cfg.Maintenance, want)
	}
	cfg, err = Parse(strings.NewReader(scscfYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Maintenance != nil {
		t.Errorf("Maintenance = %+v without the section, want nil", cfg.Maintenance)
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"unknown role", "roles: [scscf]", "roles: [scscf, mgcf]", `unknown role "mgcf"`},
		{"duplicate role", "roles: [scscf]", "roles: [scscf, scscf]", "listed twice"},
		{"no role", "roles: [scscf]", "roles: []", "at least one role"},
		{"unknown field", "  timers:", "  timer_tw: 10s\n  timers:", "timer_tw"},
		{"Tw below the RFC 3539 floor", "tw: 10s", "tw: 5s", "below the RFC 3539 minimum"},
		{"peer without port", "3gppnetwork.org:3868", "3gppnetwork.org", "peers[0].address"},
		{"diameter without a Diameter role", "roles: [scscf]", "roles: [agw]", "diameter: only the hss, icscf, scscf, ipsmgw, mmtel, epc and pcscf roles"},
		{"hss without listen", "roles: [scscf]", "roles: [scscf, hss]", "listen: required"},
		{"role without its section", "roles: [scscf]", "roles: [scscf, icscf]", "icscf: required for the icscf role"},
		{"section without its role", "scscf:\n", "icscf:\n  sip: {}\nscscf:\n", "icscf: only the icscf role"},
		{"no home domain", "home_domain: ims.mnc001.mcc001.3gppnetwork.org\n", "", "home_domain: required"},
		{"SIP host given as an address", "host: scscf.ims.mnc001.mcc001.3gppnetwork.org", "host: 172.28.0.30", "must be a name"},
		{"SIP pool given as an address", "    host: scscf.ims", "    pool: 172.28.0.30\n    host: scscf.ims", "sip.pool: must be a name"},
		{"SIP pool with a port", "    host: scscf.ims", "    pool: scscf.ims.mnc001.mcc001.3gppnetwork.org:5060\n    host: scscf.ims", "sip.pool: a name without a port"},
		{"negative SIP transaction timeout", "    host: scscf.ims", "    transaction_timeout: -1s\n    host: scscf.ims", "sip.transaction_timeout"},
		{"max_expires below min_expires", "max_expires: 600", "max_expires: 10", "need 0 < min <= max"},
		{"unknown store kind", "roles: [scscf]\n", "roles: [scscf]\nstore: {kind: redis}\n", `store: kind: unknown kind "redis"`},
		{"valkey store without an address", "roles: [scscf]\n", "roles: [scscf]\nstore: {kind: valkey}\n", "store: address"},
		{"memory store with an address", "roles: [scscf]\n", "roles: [scscf]\nstore: {kind: memory, address: valkey:6379}\n", "only kind valkey"},
		{"negative store timeout", "roles: [scscf]\n", "roles: [scscf]\nstore: {kind: valkey, address: valkey:6379, timeout: -1s}\n", "store: timeout"},
		{"maintenance without a port", "roles: [scscf]\n", "roles: [scscf]\nmaintenance: {listen: scscf}\n", "maintenance: listen"},
		{"HEP collector without port", "roles: [scscf]\n", "roles: [scscf]\nhep: {collector: lab}\n", "hep.collector"},
		{"HEP without collector", "roles: [scscf]\n", "roles: [scscf]\nhep: {agent_id: 1}\n", "hep.collector"},
		{"HEP agent ID beyond 32 bits", "roles: [scscf]\n", "roles: [scscf]\nhep: {collector: lab:9060, agent_id: 4294967296}\n", "into uint32"},
		{"HEP agent ID below zero", "roles: [scscf]\n", "roles: [scscf]\nhep: {collector: lab:9060, agent_id: -1}\n", "into uint32"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(scscfYAML, tt.old, tt.new, 1)
			if yaml == scscfYAML {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil {
				t.Fatalf("Parse accepted:\n%s", yaml)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// combinedYAML is one node with every role: the CSCFs reach the
// HSS role over Diameter like separate nodes would.
const combinedYAML = `
node: ims
roles: [pcscf, icscf, scscf, hss]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
pcscf:
  sip: {listen: 0.0.0.0:5060, host: pcscf.ims.mnc001.mcc001.3gppnetwork.org}
  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org
icscf:
  sip: {listen: 0.0.0.0:5061, host: icscf.ims.mnc001.mcc001.3gppnetwork.org}
  scscfs:
    - name: sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5062
scscf:
  sip: {listen: 0.0.0.0:5062, host: scscf.ims.mnc001.mcc001.3gppnetwork.org}
hss:
  subscriptions:
    - private_identity: 001010000000001@ims.mnc001.mcc001.3gppnetwork.org
      public_identities: [sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org]
      password: test-password
diameter:
  origin_host: ims.ims.mnc001.mcc001.3gppnetwork.org
  origin_realm: ims.mnc001.mcc001.3gppnetwork.org
  listen: 127.0.0.1:3868
  peers:
    - address: 127.0.0.1:3868
      origin_host: ims.ims.mnc001.mcc001.3gppnetwork.org
      kind: hss
`

// Every peer of the diameter section says what it is, so that a node
// whose roles reach both the HSS and the PCRF never has to tell them
// apart by their place in the list: a configuration with two instances
// of the HSS and no PCRF would otherwise read the second instance as
// the PCRF.
func TestParsePeersSayWhatTheyAre(t *testing.T) {
	const pcscfAndSCSCF = `
node: ims
roles: [pcscf, scscf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
pcscf:
  sip: {listen: 0.0.0.0:5060, host: pcscf.ims.mnc001.mcc001.3gppnetwork.org}
  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org
scscf:
  sip: {listen: 0.0.0.0:5062, host: scscf.ims.mnc001.mcc001.3gppnetwork.org}
diameter:
  origin_host: ims.ims.mnc001.mcc001.3gppnetwork.org
  origin_realm: ims.mnc001.mcc001.3gppnetwork.org
  peers:
    - {address: hss-1.ims.mnc001.mcc001.3gppnetwork.org:3868, origin_host: hss-1.ims.mnc001.mcc001.3gppnetwork.org, kind: hss}
    - {address: hss-2.ims.mnc001.mcc001.3gppnetwork.org:3868, origin_host: hss-2.ims.mnc001.mcc001.3gppnetwork.org, kind: hss}
`
	cfg, err := Parse(strings.NewReader(pcscfAndSCSCF))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Diameter.Peers) != 2 {
		t.Fatalf("peers = %+v", cfg.Diameter.Peers)
	}
	for i, p := range cfg.Diameter.Peers {
		if p.Kind != PeerHSS {
			t.Errorf("peers[%d] is %q, want both to be instances of the HSS", i, p.Kind)
		}
	}
	withPCRF := strings.Replace(pcscfAndSCSCF, "origin_host: hss-2.ims.mnc001.mcc001.3gppnetwork.org, kind: hss",
		"origin_host: pcrf.ims.mnc001.mcc001.3gppnetwork.org, kind: pcrf", 1)
	cfg, err = Parse(strings.NewReader(withPCRF))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Diameter.Peers[1].Kind; got != PeerPCRF {
		t.Errorf("the second peer is %q, want the PCRF", got)
	}
	for _, tt := range []struct{ name, old, new, wantErr string }{
		{"no kind", ", kind: hss}", "}", "peers[0].kind: required"},
		{"a kind of its own", "kind: hss}", "kind: slf}", `"slf" is not`},
		{"the PCRF without a pcscf role", "roles: [pcscf, scscf]", "roles: [scscf]", "only the pcscf role"},
		{"the HSS without a role that reaches it", "roles: [pcscf, scscf]", "roles: [pcscf]", "only the icscf, scscf"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(withPCRF, tt.old, tt.new, 1)
			if yaml == withPCRF {
				t.Fatalf("%q is not in the configuration", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseCombinedRoles(t *testing.T) {
	cfg, err := Parse(strings.NewReader(combinedYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PCSCF == nil || cfg.ICSCF == nil || cfg.SCSCF == nil || cfg.HSS == nil || cfg.Diameter == nil {
		t.Fatalf("a section is missing: %+v", cfg)
	}
	if got := cfg.HSS.VisitedNetworks; len(got) != 1 || got[0] != cfg.HomeDomain {
		t.Errorf("visited networks default to %v, want the home domain", got)
	}
	if cfg.HSS.ProvisioningListen != "" {
		t.Errorf("provisioning_listen defaults to %q, want none", cfg.HSS.ProvisioningListen)
	}
	withAPI := strings.Replace(combinedYAML, "hss:\n", "hss:\n  provisioning_listen: \":8081\"\n", 1)
	if cfg, err := Parse(strings.NewReader(withAPI)); err != nil || cfg.HSS.ProvisioningListen != ":8081" {
		t.Errorf("provisioning_listen: %v, %+v", err, cfg.HSS)
	}
	// TS 23.228 5.4.12.4: a PSI user is a range of PSIs with its private
	// service identity.
	psiUsers := "  psi_users:\n    - private_identity: conference@ims.mnc001.mcc001.3gppnetwork.org\n      wildcarded_psi: sip:!.*!@conf.ims.mnc001.mcc001.3gppnetwork.org\n"
	if cfg, err := Parse(strings.NewReader(strings.Replace(combinedYAML, "hss:\n", "hss:\n"+psiUsers, 1))); err != nil || len(cfg.HSS.PSIUsers) != 1 ||
		!cfg.HSS.PSIUsers[0].Matches("sip:conf-7f3a@conf.ims.mnc001.mcc001.3gppnetwork.org") {
		t.Errorf("psi_users: %v, %+v", err, cfg.HSS.PSIUsers)
	}
	if got := cfg.SCSCF.SIP.URI(); got != "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5062" {
		t.Errorf("S-CSCF URI = %s", got)
	}

	// TS 24.229 5.3.2.1 step 3: the I-CSCF routes the subdomains of public
	// service identities by DNS. Each is below the home domain.
	scscfs := "    - name: sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5062\n"
	withPSI := func(subdomain string) string {
		return strings.Replace(combinedYAML, scscfs, scscfs+"  psi_subdomains: ["+subdomain+"]\n", 1)
	}
	if cfg, err := Parse(strings.NewReader(withPSI("conf.ims.mnc001.mcc001.3gppnetwork.org"))); err != nil ||
		len(cfg.ICSCF.PSISubdomains) != 1 || cfg.ICSCF.PSISubdomains[0] != "conf.ims.mnc001.mcc001.3gppnetwork.org" {
		t.Errorf("psi_subdomains: %v, %+v", err, cfg.ICSCF)
	}
	for _, bad := range []string{"conf.example.net", "\"conf.ims.mnc001.mcc001.3gppnetwork.org:5060\""} {
		if _, err := Parse(strings.NewReader(withPSI(bad))); err == nil {
			t.Errorf("accepted the PSI subdomain %s", bad)
		}
	}

	clash := strings.Replace(combinedYAML, "0.0.0.0:5062", "0.0.0.0:5061", 1)
	if _, err := Parse(strings.NewReader(clash)); err == nil || !strings.Contains(err.Error(), "both listen on port 5061") {
		t.Errorf("two roles on one port: err = %v", err)
	}
}

func TestParseRejectsHSS(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"provisioning_listen without a port", "hss:\n", "hss:\n  provisioning_listen: hss\n", "hss: provisioning_listen:"},
		{"a PSI user of a distinct PSI", "hss:\n", "hss:\n  psi_users: [{private_identity: conference@ims.mnc001.mcc001.3gppnetwork.org, wildcarded_psi: sip:conf-1@conf.ims.mnc001.mcc001.3gppnetwork.org}]\n", "psi_users[0].wildcarded_psi:"},
		{"no password", "      password: test-password\n", "", "subscriptions[0].password or aka: at least one is required"},
		{"not a SIP or tel URI", "[sip:001010000000001@", "[mailto:001010000000001@", "subscriptions[0].public_identities: \"mailto:"},
		{"not an MMTelServices document", "      password: test-password\n", "      password: test-password\n      mmtel_services: <simservs/>\n", "subscriptions[0].mmtel_services:"},
		{"AKA without OPc", "      password: test-password\n", "      aka: {k: 465b5ce8b199b49faa5f0a2ee238a6bc}\n", "subscriptions[0].aka.opc: want 16 octets"},
		{"AKA with a short SQN", "      password: test-password\n", "      aka: {k: 465b5ce8b199b49faa5f0a2ee238a6bc, opc: cd63cb71954a9f4e48a5994e37a02baf, sqn: \"0020\"}\n", "subscriptions[0].aka.sqn: want 6 octets"},
		{"identity twice", "      password: test-password\n",
			"      password: test-password\n    - private_identity: 001010000000001@ims.mnc001.mcc001.3gppnetwork.org\n      public_identities: [sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org]\n      password: test-password\n",
			"subscriptions[1]: identity already belongs to a subscription: 001010000000001@ims.mnc001.mcc001.3gppnetwork.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(combinedYAML, tt.old, tt.new, 1)
			if yaml == combinedYAML {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseHSSAKASubscription(t *testing.T) {
	yaml := strings.Replace(combinedYAML, "      password: test-password\n",
		"      aka:\n        k: 465b5ce8b199b49faa5f0a2ee238a6bc\n        opc: cd63cb71954a9f4e48a5994e37a02baf\n        amf: \"8000\"\n        sqn: \"000000000020\"\n", 1)
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	sub := cfg.HSS.Subscriptions[0]
	if sub.Password != "" || sub.AKA == nil {
		t.Fatalf("subscription = %+v", sub)
	}
	if sub.AKA.K[0] != 0x46 || sub.AKA.OPc[15] != 0xaf || sub.AKA.AMF != (aka.AMF{0x80, 0}) || sub.AKA.SQN != 0x20 {
		t.Errorf("AKA = %+v", *sub.AKA)
	}
}

func TestParseAGW(t *testing.T) {
	yaml := `
node: agw
roles: [agw]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
agw:
  control: 0.0.0.0:8080
  rtp_address: 172.28.0.50
  rtp_ports: {min: 40000, max: 40099}
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AGW == nil || cfg.AGW.MinPort != 40000 || cfg.AGW.RTPAddress.String() != "172.28.0.50" || cfg.Diameter != nil {
		t.Errorf("agw = %+v", cfg.AGW)
	}
	bad := strings.Replace(yaml, "172.28.0.50", "0.0.0.0", 1)
	if _, err := Parse(strings.NewReader(bad)); err == nil {
		t.Error("an unspecified rtp_address was accepted")
	}
}

// TestParseMRFP: the mrfp role is a media node as for agw, with
// announcements; a node can have both roles.
func TestParseMRFP(t *testing.T) {
	yaml := `
node: media
roles: [agw, mrfp]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
agw:
  control: 0.0.0.0:8080
  rtp_address: 172.28.0.50
  rtp_ports: {min: 40000, max: 40039}
mrfp:
  control: 0.0.0.0:8090
  rtp_address: 172.28.0.50
  rtp_ports: {min: 40100, max: 40199}
  announcements:
    notice: /usr/share/ims/announcements/notice.wav
    cat1: /usr/share/ims/announcements/cat1.wav
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if m := cfg.MRFP; m == nil || m.MinPort != 40100 || m.Control.Port() != 8090 || m.Announcements["cat1"] != "/usr/share/ims/announcements/cat1.wav" {
		t.Errorf("mrfp = %+v", cfg.MRFP)
	}
	tests := []struct {
		name, old, new, wantErr string
	}{
		{"announcement name with a dash", "cat1:", "cat-1:", `announcements: "cat-1" must be letters and digits only`},
		{"announcement without a file", "notice: /usr/share/ims/announcements/notice.wav", `notice: ""`, "announcements.notice: the file is missing"},
		{"same control port", "0.0.0.0:8090", "0.0.0.0:8080", "agw and mrfp both listen on port 8080"},
		{"overlapping RTP ports", "{min: 40100, max: 40199}", "{min: 40030, max: 40199}", "rtp_ports of agw (40000-40039) and mrfp (40030-40199) overlap"},
		{"section without its role", "roles: [agw, mrfp]", "roles: [agw]", "mrfp: only the mrfp role"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edited := strings.Replace(yaml, tt.old, tt.new, 1)
			if edited == yaml {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			if _, err := Parse(strings.NewReader(edited)); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestParseMRFC: the MRFC and its MRFP can share a node.
func TestParseMRFC(t *testing.T) {
	yaml := `
node: mrf
roles: [mrfc, mrfp]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
mrfc:
  sip: {listen: 0.0.0.0:5060, host: mrfc.ims.mnc001.mcc001.3gppnetwork.org}
  notify: 0.0.0.0:8081
  mrfp: [http://mrf:8080]
mrfp:
  control: 0.0.0.0:8080
  rtp_address: 172.28.0.80
  rtp_ports: {min: 40100, max: 40199}
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if m := cfg.MRFC; m == nil || m.NotifyURL() != "http://mrfc.ims.mnc001.mcc001.3gppnetwork.org:8081/notify" || len(m.MRFPs) != 1 || cfg.MRFP == nil {
		t.Errorf("mrfc = %+v", cfg.MRFC)
	}
	tests := []struct {
		name, old, new, wantErr string
	}{
		{"no MRFP", "mrfp: [http://mrf:8080]", "mrfp: []", "mrfc: mrfp: at least one MRFP is required"},
		{"MRFP not a URL", "mrfp: [http://mrf:8080]", "mrfp: [mrf:8080]", `mrfp: "mrf:8080" is not an http:// URL`},
		{"MRFP twice", "mrfp: [http://mrf:8080]", "mrfp: [http://mrf:8080, http://mrf:8080]", "is listed twice"},
		{"notify without a port", "notify: 0.0.0.0:8081", "notify: 0.0.0.0", "mrfc: notify"},
		{"notify on the port of the MRFP", "notify: 0.0.0.0:8081", "notify: 0.0.0.0:8080", "mrfp and the notify server of mrfc both listen on port 8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edited := strings.Replace(yaml, tt.old, tt.new, 1)
			if edited == yaml {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			if _, err := Parse(strings.NewReader(edited)); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseIPSMGW(t *testing.T) {
	yaml := `
node: ipsmgw
roles: [ipsmgw]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
ipsmgw:
  sip: {listen: 0.0.0.0:5060, host: ipsmgw.ims.mnc001.mcc001.3gppnetwork.org}
  number: "+12025550199"
  service_centre: "+12025550100"
diameter:
  origin_host: ipsmgw.ims.mnc001.mcc001.3gppnetwork.org
  origin_realm: ims.mnc001.mcc001.3gppnetwork.org
  peers: [{address: hss.ims.mnc001.mcc001.3gppnetwork.org:3868, origin_host: hss.ims.mnc001.mcc001.3gppnetwork.org, kind: hss}]
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	want := IPSMGW{
		SIP:           SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "ipsmgw.ims.mnc001.mcc001.3gppnetwork.org"},
		Number:        "12025550199",
		ServiceCentre: sms.InternationalNumber("+12025550100"),
	}
	if cfg.IPSMGW == nil || !reflect.DeepEqual(*cfg.IPSMGW, want) || len(cfg.Diameter.Peers) != 1 {
		t.Errorf("ipsmgw = %+v, diameter = %+v", cfg.IPSMGW, cfg.Diameter)
	}
	// The IBCF for short messages to the users of other networks.
	withIBCF := strings.Replace(yaml, `  service_centre: "+12025550100"`, `  service_centre: "+12025550100"`+"\n  ibcf: sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060", 1)
	if cfg, err := Parse(strings.NewReader(withIBCF)); err != nil || cfg.IPSMGW.IBCF.Host != "ibcf.ims.mnc001.mcc001.3gppnetwork.org" {
		t.Errorf("ipsmgw with an IBCF: %v, %+v", err, cfg.IPSMGW)
	}
	for _, bad := range []struct{ old, new string }{
		{`"+12025550199"`, `"12025550199"`},
		{`"+12025550100"`, `"+1202555010x"`},
		{`"+12025550100"`, `"+1234567890123456"`},
		{"  peers:", "  unused:"},
		{`  service_centre: "+12025550100"`, `  service_centre: "+12025550100"` + "\n  ibcf: ibcf"},
	} {
		if _, err := Parse(strings.NewReader(strings.Replace(yaml, bad.old, bad.new, 1))); err == nil {
			t.Errorf("accepted %s in place of %s", bad.new, bad.old)
		}
	}
}

func TestParseMMTel(t *testing.T) {
	yaml := `
node: mmtel
roles: [mmtel]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
mmtel:
  sip: {listen: 0.0.0.0:5060, host: mmtel.ims.mnc001.mcc001.3gppnetwork.org}
  home_country_code: "1"
  xcap: {listen: 0.0.0.0:80, nonce_key: test-nonce-key-0123456789}
diameter:
  origin_host: mmtel.ims.mnc001.mcc001.3gppnetwork.org
  origin_realm: ims.mnc001.mcc001.3gppnetwork.org
  peers: [{address: hss.ims.mnc001.mcc001.3gppnetwork.org:3868, origin_host: hss.ims.mnc001.mcc001.3gppnetwork.org, kind: hss}]
`
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	want := MMTel{
		SIP:             SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "mmtel.ims.mnc001.mcc001.3gppnetwork.org"},
		HomeCountryCode: "1",
		XCAP:            XCAP{Listen: netip.MustParseAddrPort("0.0.0.0:80"), NonceKey: []byte("test-nonce-key-0123456789")},
	}
	if cfg.MMTel == nil || !reflect.DeepEqual(*cfg.MMTel, want) || len(cfg.Diameter.Peers) != 1 {
		t.Errorf("mmtel = %+v, diameter = %+v", cfg.MMTel, cfg.Diameter)
	}
	mrf := `  home_country_code: "1"` + "\n  mrfc: sip:mrfc.ims.mnc001.mcc001.3gppnetwork.org:5060\n  announcement: notice"
	withMRF := strings.Replace(yaml, `  home_country_code: "1"`, mrf+"\n  conference_domain: conf.ims.mnc001.mcc001.3gppnetwork.org", 1)
	if cfg, err := Parse(strings.NewReader(withMRF)); err != nil || cfg.MMTel.MRFC != "sip:mrfc.ims.mnc001.mcc001.3gppnetwork.org:5060" ||
		cfg.MMTel.Announcement != "notice" || cfg.MMTel.ConferenceDomain != "conf.ims.mnc001.mcc001.3gppnetwork.org" {
		t.Errorf("with the MRFC: %+v, %v", cfg.MMTel, err)
	}
	// The conference URIs are public service identities in a subdomain of
	// the home domain, with no port (TS 23.003 13.5), and need the MRFC
	// that mixes the conferences.
	for _, domain := range []string{"", "conf.ims.mnc001.mcc001.3gppnetwork.org:5060", "conf.example.net", "ims.mnc001.mcc001.3gppnetwork.org", "a@conf.ims.mnc001.mcc001.3gppnetwork.org"} {
		bad := strings.Replace(yaml, `  home_country_code: "1"`, mrf+"\n  conference_domain: \""+domain+"\"", 1)
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted conference_domain %q", domain)
		}
	}
	noMRF := strings.Replace(yaml, `  home_country_code: "1"`, `  home_country_code: "1"`+"\n  conference_domain: conf.ims.mnc001.mcc001.3gppnetwork.org", 1)
	if _, err := Parse(strings.NewReader(noMRF)); err == nil {
		t.Error("accepted conference_domain without an mrfc")
	}
	for _, bad := range []struct{ old, new string }{
		{`"1"`, `"+1"`},
		{`"1"`, `"1234"`},
		{"  peers:", "  unused:"},
		{"roles: [mmtel]", "roles: [scscf]"},
		{"  xcap: {listen: 0.0.0.0:80,", "  xcap: {listen: 0.0.0.0:5060,"},
		{"  xcap: {listen: 0.0.0.0:80, nonce_key: test-nonce-key-0123456789}\n", ""},
		{`  home_country_code: "1"`, `  home_country_code: "1"` + "\n  announcement: notice"},
		{`  home_country_code: "1"`, `  home_country_code: "1"` + "\n  mrfc: http://mrfc"},
		{`  home_country_code: "1"`, `  home_country_code: "1"` + "\n  mrfc: sip:mrfc\n  announcement: no-tice"},
	} {
		if _, err := Parse(strings.NewReader(strings.Replace(yaml, bad.old, bad.new, 1))); err == nil {
			t.Errorf("accepted %s in place of %s", bad.new, bad.old)
		}
	}
}

const labYAML = `
node: lab
roles: [lab]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
lab:
  hep_listen: ":9060"
  http_listen: ":8080"
  hss_provisioning: http://hss:8081
  epc: http://epc:8080
  fault_agent: http://fault-agent:8080
  ues:
    - {name: phone1, control: "http://phone1:8090"}
    - {name: phone2, control: "http://172.28.0.111:8090"}
`

func TestParseLab(t *testing.T) {
	cfg, err := Parse(strings.NewReader(labYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := Lab{HEPListen: ":9060", HTTPListen: ":8080", HSSProvisioning: "http://hss:8081", EPC: "http://epc:8080",
		FaultAgent: "http://fault-agent:8080", UEs: []UESimulator{
			{Name: "phone1", Control: "http://phone1:8090"},
			{Name: "phone2", Control: "http://172.28.0.111:8090"},
		}}
	if cfg.Lab == nil || !reflect.DeepEqual(*cfg.Lab, want) || cfg.Diameter != nil {
		t.Errorf("lab = %+v, want %+v", cfg.Lab, want)
	}

	optional, _, _ := strings.Cut(labYAML, "  hss_provisioning:")
	cfg, err = Parse(strings.NewReader(optional))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lab.HSSProvisioning != "" || cfg.Lab.EPC != "" || cfg.Lab.FaultAgent != "" || cfg.Lab.UEs != nil {
		t.Errorf("lab = %+v without hss_provisioning, epc, fault_agent and ues", cfg.Lab)
	}

	tests := []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"hep_listen without port", `hep_listen: ":9060"`, `hep_listen: "lab"`, "hep_listen"},
		{"http_listen with port 0", `http_listen: ":8080"`, `http_listen: ":0"`, "http_listen"},
		{"provisioning not a URL", "http://hss:8081", "hss:8081", "hss_provisioning"},
		{"EPC not a URL", "http://epc:8080", "epc:8080", "epc:"},
		{"fault agent not a URL", "http://fault-agent:8080", "fault-agent:8080", "fault_agent:"},
		{"UE without a name", "name: phone1, ", "", "ues[0].name"},
		{"UE name with a slash", "name: phone1", "name: ue/a", "ues[0].name"},
		{"UE name used twice", "name: phone2", "name: phone1", `ues[1].name: "phone1" is used twice`},
		{"UE control not a URL", `control: "http://phone1:8090"`, "control: phone1:8090", "ues[0].control"},
		{"section without its role", "roles: [lab]", "roles: [agw]\nagw: {control: 0.0.0.0:8080, rtp_address: 172.28.0.50, rtp_ports: {min: 40000, max: 40099}}", "lab: only the lab role"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(labYAML, tt.old, tt.new, 1)
			if yaml == labYAML {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
	noSection := "node: lab\nroles: [lab]\nhome_domain: ims.mnc001.mcc001.3gppnetwork.org\n"
	if _, err := Parse(strings.NewReader(noSection)); err == nil || !strings.Contains(err.Error(), "lab: required for the lab role") {
		t.Errorf("lab role without its section: err = %v", err)
	}
}

const epcYAML = `
node: epc
roles: [epc]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
epc:
  api: 0.0.0.0:8080
  apn: ims
  pcscf_addresses: [172.28.0.40]
  subscribers:
    - {imsi: "001010000000001", address: 172.28.0.100}
    - {imsi: "001010000000002", address: 172.28.0.101}
  cell: {mcc: "001", mnc: "01", tac: 1, eci: 257}
diameter:
  origin_host: pcrf.ims.mnc001.mcc001.3gppnetwork.org
  origin_realm: ims.mnc001.mcc001.3gppnetwork.org
  listen: 0.0.0.0:3868
`

func TestParseEPC(t *testing.T) {
	cfg, err := Parse(strings.NewReader(epcYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := &EPC{API: netip.MustParseAddrPort("0.0.0.0:8080"), Config: epc.Config{
		APN:            "ims",
		PCSCFAddresses: []netip.Addr{netip.MustParseAddr("172.28.0.40")},
		Subscribers: []epc.Subscriber{
			{IMSI: "001010000000001", Address: netip.MustParseAddr("172.28.0.100")},
			{IMSI: "001010000000002", Address: netip.MustParseAddr("172.28.0.101")},
		},
		Cell: epc.SystemInformation{
			TrackingAreaIdentity: epc.TrackingAreaIdentity{PLMN: epc.PLMNIdentity{MCC: "001", MNC: "01"}, TAC: 1},
			CellIdentity:         257,
		},
	}}
	if !reflect.DeepEqual(cfg.EPC, want) {
		t.Errorf("epc\n got %+v\nwant %+v", cfg.EPC, want)
	}
	if cfg.Diameter.Listen != "0.0.0.0:3868" || len(cfg.Diameter.Peers) != 0 {
		t.Errorf("diameter = %+v, want a listener and no peers", cfg.Diameter)
	}
}

func TestParseRejectsEPC(t *testing.T) {
	for _, tt := range []struct {
		name, old, new, wantErr string
	}{
		{"no P-CSCF", "pcscf_addresses: [172.28.0.40]", "pcscf_addresses: []", "pcscf_addresses: at least one"},
		{"P-CSCF as a name", "[172.28.0.40]", "[pcscf]", "not an IPv4 address"},
		{"IMSI with a letter", `"001010000000002"`, `"00101000000000x"`, "subscribers[1].imsi"},
		{"two UEs on one address", "address: 172.28.0.101", "address: 172.28.0.100", "used twice"},
		{"MCC of two digits", `mcc: "001"`, `mcc: "01"`, "three digits"},
		{"ECI beyond 28 bits", "eci: 257", "eci: 268435456", "28 bits"},
		{"APN with a space", "apn: ims", "apn: i ms", "apn:"},
		{"no Diameter listener", "  listen: 0.0.0.0:3868\n", "", "listen: required for the hss and epc roles"},
		{"a Diameter peer", "  listen: 0.0.0.0:3868\n", "  listen: 0.0.0.0:3868\n  peers: [{address: hss:3868, origin_host: hss}]\n", "peers: only"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(epcYAML, tt.old, tt.new, 1)
			if yaml == epcYAML {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// A P-CSCF may talk Rx to a PCRF (TS 29.214); without a diameter section
// it works as before, without policy control.
func TestParsePCSCFWithAPCRF(t *testing.T) {
	const pcscfYAML = `
node: pcscf
roles: [pcscf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
pcscf:
  sip: {listen: 0.0.0.0:5060, host: pcscf.ims.mnc001.mcc001.3gppnetwork.org}
  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org
diameter:
  origin_host: pcscf.ims.mnc001.mcc001.3gppnetwork.org
  origin_realm: ims.mnc001.mcc001.3gppnetwork.org
  peers:
    - {address: pcrf.ims.mnc001.mcc001.3gppnetwork.org:3868, origin_host: pcrf.ims.mnc001.mcc001.3gppnetwork.org, kind: pcrf}
`
	cfg, err := Parse(strings.NewReader(pcscfYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Diameter.Peers) != 1 || cfg.Diameter.Peers[0].OriginHost != "pcrf.ims.mnc001.mcc001.3gppnetwork.org" {
		t.Errorf("diameter = %+v, want the PCRF as the peer", cfg.Diameter)
	}
	noPeer := pcscfYAML[:strings.Index(pcscfYAML, "  peers:")]
	if _, err := Parse(strings.NewReader(noPeer)); err == nil || !strings.Contains(err.Error(), "needs the PCRF") {
		t.Errorf("P-CSCF Diameter without a peer: %v", err)
	}
	withoutRx := pcscfYAML[:strings.Index(pcscfYAML, "diameter:")]
	if cfg, err := Parse(strings.NewReader(withoutRx)); err != nil || cfg.Diameter != nil {
		t.Errorf("P-CSCF without Diameter: %+v, %v", cfg.Diameter, err)
	}
}

// TS 33.203 7.1: the P-CSCF's protected server port for IMS AKA is a port
// of its own, never 5060 or 5061.
func TestParsePCSCFProtectedServerPort(t *testing.T) {
	withPort := strings.Replace(combinedYAML, "  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org\n",
		"  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org\n  sec_agree: {protected_server_port: 5064}\n", 1)
	cfg, err := Parse(strings.NewReader(withPort))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PCSCF.ProtectedServerPort != 5064 {
		t.Errorf("protected server port = %d, want 5064", cfg.PCSCF.ProtectedServerPort)
	}
	if cfg, _ := Parse(strings.NewReader(combinedYAML)); cfg.PCSCF.ProtectedServerPort != 0 {
		t.Errorf("without sec_agree the port is %d, want 0", cfg.PCSCF.ProtectedServerPort)
	}
	for port, want := range map[string]string{"5061": "not 5060 or 5061", "5060": "not 5060 or 5061", "0": "not 5060 or 5061",
		"5062": "scscf and the protected server port of the pcscf both listen on port 5062"} {
		yaml := strings.Replace(withPort, "protected_server_port: 5064", "protected_server_port: "+port, 1)
		if _, err := Parse(strings.NewReader(yaml)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("port %s: err = %v, want it to mention %q", port, err, want)
		}
	}
}

// The P-CSCF may list several media relays; a dialog names the one that
// holds its context by its URL.
func TestParsePCSCFMediaRelays(t *testing.T) {
	const visited = "  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org\n"
	withRelays := func(list string) string {
		return strings.Replace(combinedYAML, visited, visited+"  media_relays: "+list+"\n", 1)
	}
	cfg, err := Parse(strings.NewReader(withRelays("[http://agw-1:8080, http://agw-2:8080]")))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"http://agw-1:8080", "http://agw-2:8080"}; !slices.Equal(cfg.PCSCF.MediaRelays, want) {
		t.Errorf("media relays = %v, want %v", cfg.PCSCF.MediaRelays, want)
	}
	if cfg, _ := Parse(strings.NewReader(combinedYAML)); len(cfg.PCSCF.MediaRelays) != 0 {
		t.Errorf("without media_relays the P-CSCF has the relays %v, want none", cfg.PCSCF.MediaRelays)
	}
	for list, want := range map[string]string{
		"[agw:8080]":                         `media_relays: "agw:8080" is not an http:// URL`,
		"[http://agw:8080, http://agw:8080]": `media_relays: "http://agw:8080" is listed twice`,
	} {
		if _, err := Parse(strings.NewReader(withRelays(list))); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("media_relays %s: err = %v, want it to mention %q", list, err, want)
		}
	}
}
