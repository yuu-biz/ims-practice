package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The h248 section of a node that controls media and of a media relay,
// and the files of deploy/h248, the deployment whose P-CSCF controls
// the media relay over H.248 in place of its HTTP control API.

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
// media over H.248 therefore does not take the shared store, and a relay that takes its commands over H.248 takes no store either:
// its gateway holds in memory which H.248 context is which.
func TestParseH248RefusesWhatNeedsASecondInstance(t *testing.T) {
	const visited = "  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org\n"
	const controller = "  h248: {listen: 0.0.0.0:2944}\n"
	const store = "store: {kind: valkey, address: 127.0.0.1:6379, prefix: \"ims:\"}\n"
	withController := strings.Replace(combinedYAML, visited, visited+controller, 1)

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

// composeH248Overlay is the file of deploy/h248 that is a Compose file
// rather than a node's configuration.
const composeH248Overlay = "h248.yaml"

func h248Nodes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("../../deploy/h248/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var nodes []string
	for _, f := range files {
		if filepath.Base(f) != composeH248Overlay {
			nodes = append(nodes, f)
		}
	}
	if len(nodes) == 0 {
		t.Fatal("no node files in deploy/h248")
	}
	return nodes
}

// TestH248ConfigsChangeOnlyTheMediaControl: each node of deploy/h248 is
// the node of the same name in deploy/compose with its media control
// changed, and nothing else. The signalling of the deployment is then
// the one the guides describe, which is why the voice call of
// scripts/e2e-call.sh passes on it as it does on compose.yaml alone.
func TestH248ConfigsChangeOnlyTheMediaControl(t *testing.T) {
	for _, f := range h248Nodes(t) {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			compose, err := Load(filepath.Join("../../deploy/compose", name))
			if err != nil {
				t.Fatal(err)
			}
			h248, err := Load(f)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "pcscf.yaml":
				if h248.PCSCF.MediaController == nil || len(h248.PCSCF.MediaRelays) != 0 {
					t.Fatalf("media control = %+v, relays %v", h248.PCSCF.MediaController, h248.PCSCF.MediaRelays)
				}
				h248.PCSCF.MediaController, h248.PCSCF.MediaRelays = nil, compose.PCSCF.MediaRelays
			case "agw.yaml":
				if h248.AGW.MediaGateway == nil || h248.AGW.Control.IsValid() {
					t.Fatalf("media control = %+v, control API on %v", h248.AGW.MediaGateway, h248.AGW.Control)
				}
				h248.AGW.MediaGateway, h248.AGW.Control = nil, compose.AGW.Control
			default:
				t.Fatalf("deploy/h248 has the node %s, which this test does not know", name)
			}
			if !reflect.DeepEqual(h248, compose) {
				t.Errorf("%s differs from deploy/compose/%s in more than the media control:\n%+v\n%+v",
					name, name, h248, compose)
			}
		})
	}
}

// TestH248ConfigsAreFullyRead: every section of these files reaches a
// role, so that none of them makes the node look as though it were
// controlled over H.248 when nothing reads the section.
func TestH248ConfigsAreFullyRead(t *testing.T) {
	for _, f := range h248Nodes(t) {
		t.Run(filepath.Base(f), func(t *testing.T) {
			cfg, err := Load(f)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Unread(); len(got) != 0 {
				t.Errorf("Unread() = %v, want none", got)
			}
		})
	}
}

// TestH248GatewayRegistersWithTheControllerOfTheDeployment: the gateway
// of the media relay sends its ServiceChange to the name and port the
// P-CSCF of the same deployment listens on. The two files are written
// apart, so nothing else keeps them pointing at each other.
func TestH248GatewayRegistersWithTheControllerOfTheDeployment(t *testing.T) {
	relay, err := Load("../../deploy/h248/agw.yaml")
	if err != nil {
		t.Fatal(err)
	}
	controller, err := Load("../../deploy/h248/pcscf.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if relay.AGW.MediaGateway == nil || controller.PCSCF.MediaController == nil {
		t.Fatalf("gateway = %+v, controller = %+v", relay.AGW.MediaGateway, controller.PCSCF.MediaController)
	}
	// The controller names itself by the host of its SIP section, which
	// is the name DNS resolves to the node.
	want := controller.PCSCF.SIP.Host + ":" + strconv.Itoa(int(controller.PCSCF.MediaController.Listen.Port()))
	if got := relay.AGW.MediaGateway.Controller; got != want {
		t.Errorf("the gateway registers with %q, want %q", got, want)
	}
}

// TestH248MountsEveryNodeFile: the Compose override gives each node of
// deploy/h248 its file in place of the one of deploy/compose. A node
// file that the override does not mount would leave that node on its
// HTTP control API, while every test of the file itself still passes.
func TestH248MountsEveryNodeFile(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("../../deploy/h248", composeH248Overlay))
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct {
		Services map[string]struct {
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &overlay); err != nil {
		t.Fatal(err)
	}
	for _, f := range h248Nodes(t) {
		name := filepath.Base(f)
		service := strings.TrimSuffix(name, ".yaml")
		t.Run(service, func(t *testing.T) {
			s, ok := overlay.Services[service]
			if !ok {
				t.Fatalf("%s has no service %s", composeH248Overlay, service)
			}
			mount := "./deploy/h248/" + name + ":/etc/ims/node.yaml:ro"
			if !slices.Contains(s.Volumes, mount) {
				t.Errorf("volumes = %v, want %s", s.Volumes, mount)
			}
		})
	}
}
