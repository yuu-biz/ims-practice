package config

import (
	"net/netip"
	"strings"
	"testing"
)

// The h248 section of a node that controls media and of a media relay.

// A media relay on its own, the h248 section that puts a media gateway
// in front of it, and the two together.
const (
	relayAlone = `
node: agw
roles: [agw]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
agw:
  rtp_address: 172.28.0.50
  rtp_ports: {min: 40000, max: 40039}
`
	relayGatewaySection = `  h248:
    listen: 0.0.0.0:2944
    host: agw.ims.mnc001.mcc001.3gppnetwork.org
    controller: pcscf.ims.mnc001.mcc001.3gppnetwork.org:2944
`
	relayWithGateway = relayAlone + relayGatewaySection
)

// The P-CSCF either drives the control APIs of media_relays or takes
// the registrations of media gateways over H.248, never both: a dialog
// names the relay of its context one way only.
func TestParsePCSCFMediaControl(t *testing.T) {
	const visited = "  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org\n"
	withPCSCF := func(lines string) string {
		return strings.Replace(combinedYAML, visited, visited+lines, 1)
	}
	cfg, err := Parse(strings.NewReader(withPCSCF("  h248: {listen: 0.0.0.0:2944}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PCSCF.MediaController == nil {
		t.Fatal("no media controller")
	}
	if got := cfg.PCSCF.MediaController.Listen; got != netip.MustParseAddrPort("0.0.0.0:2944") {
		t.Errorf("listen = %v", got)
	}
	if cfg, _ := Parse(strings.NewReader(combinedYAML)); cfg.PCSCF.MediaController != nil {
		t.Errorf("without an h248 section the P-CSCF has the controller %+v, want none", cfg.PCSCF.MediaController)
	}
	for name, tt := range map[string]struct{ lines, wantErr string }{
		"both ways of control": {
			"  media_relays: [http://agw:8080]\n  h248: {listen: 0.0.0.0:2944}\n", "not both"},
		"no listen address": {"  h248: {}\n", "h248.listen"},
		"port 0":            {"  h248: {listen: 0.0.0.0:0}\n", "h248.listen: port 0"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(withPCSCF(tt.lines)))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// A controller and a gateway name themselves by a name that goes into
// the header of every H.248 message they send, so a name the protocol
// cannot carry keeps the node from starting instead of making every
// message unwritable. The P-CSCF's name is the host of its SIP section.
func TestParseH248NamesAreNamesH248CanCarry(t *testing.T) {
	const visited = "  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org\n"
	const controller = "  h248: {listen: 0.0.0.0:2944}\n"
	withHost := func(host string) string {
		yaml := strings.Replace(combinedYAML, visited, visited+controller, 1)
		out := strings.Replace(yaml, "host: pcscf.ims.mnc001.mcc001.3gppnetwork.org}", "host: "+host+"}", 1)
		if out == yaml {
			t.Fatal("the host of the pcscf section is not in the configuration")
		}
		return out
	}
	// A name of more than 64 characters, which the grammar of H.248 does
	// not hold, and one with a character that is not in it.
	long := strings.Repeat("a", 60) + ".ims.mnc001.mcc001.3gppnetwork.org"
	for name, host := range map[string]string{"too long": long, "an underscore": "pcscf_1.ims.mnc001.mcc001.3gppnetwork.org"} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(withHost(host)))
			if err == nil || !strings.Contains(err.Error(), "sip.host") || !strings.Contains(err.Error(), "H.248 can carry") {
				t.Errorf("error = %v, want it to say that sip.host is not a name H.248 can carry", err)
			}
		})
	}
	// The same name is good enough for a P-CSCF that controls no media.
	if _, err := Parse(strings.NewReader(strings.Replace(combinedYAML,
		"host: pcscf.ims.mnc001.mcc001.3gppnetwork.org}", "host: pcscf_1.ims.mnc001.mcc001.3gppnetwork.org}", 1))); err != nil {
		t.Errorf("without an h248 section: %v", err)
	}
	// The gateway's own name is checked the same way.
	gateway := strings.Replace(relayWithGateway, "host: agw.ims.mnc001.mcc001.3gppnetwork.org", "host: agw_1.ims.mnc001.mcc001.3gppnetwork.org", 1)
	if _, err := Parse(strings.NewReader(gateway)); err == nil || !strings.Contains(err.Error(), "h248.host") {
		t.Errorf("error = %v, want it to mention h248.host", err)
	}
}

// TestParseH248RefusesWhatNeedsASecondInstance: a media gateway
// registers with one controller, so the contexts it holds for a node
// belong to the one instance it registered with. A P-CSCF that controls
// media over H.248 therefore takes neither a pool nor the shared store,
// and a relay that takes its commands over H.248 takes no store either:
// its gateway holds in memory which H.248 context is which.
func TestParseH248RefusesWhatNeedsASecondInstance(t *testing.T) {
	const visited = "  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org\n"
	const controller = "  h248: {listen: 0.0.0.0:2944}\n"
	const store = "store: {kind: valkey, address: 127.0.0.1:6379, prefix: \"ims:\"}\n"
	withController := strings.Replace(combinedYAML, visited, visited+controller, 1)

	pool := strings.Replace(withController, "host: pcscf.ims.mnc001.mcc001.3gppnetwork.org}",
		"host: pcscf-1.ims.mnc001.mcc001.3gppnetwork.org, pool: pcscf.ims.mnc001.mcc001.3gppnetwork.org}", 1)
	if _, err := Parse(strings.NewReader(pool)); err == nil || !strings.Contains(err.Error(), "pcscf.sip.pool") {
		t.Errorf("a pool: error = %v, want it to mention pcscf.sip.pool", err)
	}
	if _, err := Parse(strings.NewReader(withController + store)); err == nil || !strings.Contains(err.Error(), "store") {
		t.Errorf("a shared store: error = %v, want it to mention the store section", err)
	}
	// Both are fine for a P-CSCF that drives the control APIs instead.
	if _, err := Parse(strings.NewReader(combinedYAML + store)); err != nil {
		t.Errorf("a shared store without an h248 section: %v", err)
	}
	if _, err := Parse(strings.NewReader(relayWithGateway + store)); err == nil || !strings.Contains(err.Error(), "store") {
		t.Errorf("a relay over h248 with a shared store: error = %v, want it to mention the store section", err)
	}
	// A store of kind memory keeps the state in the process, so it asks
	// nothing of a second instance.
	memory := "store: {kind: memory}\n"
	if _, err := Parse(strings.NewReader(withController + memory)); err != nil {
		t.Errorf("a memory store: %v", err)
	}
}

// A node that both controls media and relays it runs two H.248
// endpoints, which cannot share a port.
func TestParseH248EndpointsOfOneNodeTakePortsOfTheirOwn(t *testing.T) {
	both := `
node: ims
roles: [pcscf, agw]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
pcscf:
  sip: {listen: 0.0.0.0:5060, host: pcscf.ims.mnc001.mcc001.3gppnetwork.org}
  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org
  h248: {listen: 0.0.0.0:2944}
agw:
  rtp_address: 172.28.0.50
  rtp_ports: {min: 40000, max: 40039}
  h248:
    listen: 0.0.0.0:2944
    host: agw.ims.mnc001.mcc001.3gppnetwork.org
    controller: pcscf.ims.mnc001.mcc001.3gppnetwork.org:2944
`
	if _, err := Parse(strings.NewReader(both)); err == nil || !strings.Contains(err.Error(), "both listen on port 2944") {
		t.Errorf("error = %v, want it to say that two endpoints take port 2944", err)
	}
	apart := strings.Replace(both, "  h248: {listen: 0.0.0.0:2944}", "  h248: {listen: 0.0.0.0:2945}", 1)
	cfg, err := Parse(strings.NewReader(apart))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PCSCF.MediaController.Listen.Port() != 2945 || cfg.AGW.MediaGateway.Listen.Port() != 2944 {
		t.Errorf("controller on %v, gateway on %v", cfg.PCSCF.MediaController.Listen, cfg.AGW.MediaGateway.Listen)
	}
}

// A media relay takes its commands over the HTTP control API or over
// H.248, never both: two controllers on one relay would each change the
// contexts of the other.
func TestParseAGWMediaControl(t *testing.T) {
	const relay, gateway = relayAlone, relayGatewaySection
	cfg, err := Parse(strings.NewReader(relay + gateway))
	if err != nil {
		t.Fatal(err)
	}
	want := MediaGateway{
		Listen:     netip.MustParseAddrPort("0.0.0.0:2944"),
		Host:       "agw.ims.mnc001.mcc001.3gppnetwork.org",
		Controller: "pcscf.ims.mnc001.mcc001.3gppnetwork.org:2944",
	}
	if cfg.AGW.MediaGateway == nil || *cfg.AGW.MediaGateway != want {
		t.Errorf("media gateway = %+v, want %+v", cfg.AGW.MediaGateway, want)
	}
	// A relay driven over H.248 serves no control API, so it is given no
	// address to serve it on.
	if cfg.AGW.Control.IsValid() {
		t.Errorf("control = %v, want none", cfg.AGW.Control)
	}
	if cfg, _ := Parse(strings.NewReader(relay + "  control: 0.0.0.0:8080\n")); cfg.AGW.MediaGateway != nil {
		t.Errorf("without an h248 section the relay has the gateway %+v, want none", cfg.AGW.MediaGateway)
	}
	for name, tt := range map[string]struct{ lines, wantErr string }{
		"neither way of control": {"", "control: required"},
		"both ways of control":   {"  control: 0.0.0.0:8080\n" + gateway, "not both"},
		"no host":                {strings.Replace(gateway, "    host: agw.ims.mnc001.mcc001.3gppnetwork.org\n", "", 1), "h248.host: required"},
		"an address for a host":  {strings.Replace(gateway, "host: agw.ims.mnc001.mcc001.3gppnetwork.org", "host: 172.28.0.50", 1), "h248.host: must be a name"},
		"a controller without a port": {strings.Replace(gateway, "controller: pcscf.ims.mnc001.mcc001.3gppnetwork.org:2944",
			"controller: pcscf.ims.mnc001.mcc001.3gppnetwork.org", 1), "h248.controller"},
		"a controller on port 0": {strings.Replace(gateway, "3gppnetwork.org:2944\n", "3gppnetwork.org:0\n", 1), "h248.controller"},
	} {
		t.Run(name, func(t *testing.T) {
			if tt.lines != "" && tt.lines == gateway {
				t.Fatal("the configuration is unchanged")
			}
			_, err := Parse(strings.NewReader(relay + tt.lines))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// The mrfp role takes no h248 section. Mp, between the MRFC and the
// MRFP, has commands of its own (TS 29.333 5.17.2), and an h248 section
// that the node read as Iq would put the wrong profile in front of the
// announcements and the mixes of the MRFP.
func TestParseMRFPTakesNoH248Section(t *testing.T) {
	const mrfp = `
node: mrfp
roles: [mrfp]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
mrfp:
  control: 0.0.0.0:8080
  rtp_address: 172.28.0.86
  rtp_ports: {min: 40100, max: 40139}
`
	if _, err := Parse(strings.NewReader(mrfp)); err != nil {
		t.Fatal(err)
	}
	withH248 := mrfp + "  h248: {listen: 0.0.0.0:2944}\n"
	if _, err := Parse(strings.NewReader(withH248)); err == nil || !strings.Contains(err.Error(), "HTTP control API") {
		t.Errorf("error = %v, want it to mention the HTTP control API", err)
	}
	// So an MRFP with no control API is told that it needs one, and not
	// that h248 would do instead.
	withoutControl := strings.Replace(mrfp, "  control: 0.0.0.0:8080\n", "", 1)
	_, err := Parse(strings.NewReader(withoutControl))
	if err == nil || !strings.Contains(err.Error(), "control: required") {
		t.Fatalf("error = %v, want it to say that control is required", err)
	}
	if strings.Contains(err.Error(), "h248") {
		t.Errorf("error = %v, want it not to offer h248 to the mrfp role", err)
	}
}
