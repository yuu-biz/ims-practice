package config

import (
	"strings"
	"testing"
)

// The esp section of a P-CSCF, and the file of deploy/esp, the
// deployment whose P-CSCF and VoLTE UEs protect their signalling with
// IPsec ESP.

const pcscfWithESP = `
node: pcscf
roles: [pcscf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
pcscf:
  sip:
    listen: 172.28.0.40:5060
    host: pcscf.ims.mnc001.mcc001.3gppnetwork.org
  visited_network_id: ims.mnc001.mcc001.3gppnetwork.org
  sec_agree:
    protected_server_port: 5064
    esp:
      protected_client_port: 5065
`

// TestESPDeployment: the P-CSCF of deploy/esp loads, with both
// protected ports of TS 33.203 7.1 and its own address, which the
// security associations are bound to.
func TestESPDeployment(t *testing.T) {
	cfg, err := Load("../../deploy/esp/pcscf.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.PCSCF
	if c == nil {
		t.Fatal("deploy/esp/pcscf.yaml has no pcscf role")
	}
	if c.ProtectedServerPort == 0 || c.ProtectedClientPort == 0 {
		t.Errorf("port_ps is %d and port_pc %d, want both", c.ProtectedServerPort, c.ProtectedClientPort)
	}
	if addr := c.SIP.Listen.Addr(); !addr.IsValid() || addr.IsUnspecified() {
		t.Errorf("sip.listen is on %s, want the P-CSCF's own address", addr)
	}
}

// TestESPConfidentiality: the P-CSCF decides whether the signalling is
// encrypted (TS 33.203 5.1.3 item 2), and the esp section is where its
// policy is set. Without it the P-CSCF applies no confidentiality,
// which is what deploy/esp does, so that the SIP inside ESP stays
// readable in a capture.
func TestESPConfidentiality(t *testing.T) {
	cfg, err := Parse(strings.NewReader(pcscfWithESP))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PCSCF.Confidentiality {
		t.Error("a P-CSCF whose esp section says nothing of confidentiality applies it")
	}
	cfg, err = Parse(strings.NewReader(pcscfWithESP + "      confidentiality: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PCSCF.Confidentiality {
		t.Error("a P-CSCF with confidentiality: true applies none")
	}
	// The policy needs ESP: only a P-CSCF that sets security
	// associations up encrypts anything, and the protected client port
	// is what gives it one.
	without := strings.ReplaceAll(pcscfWithESP, "      protected_client_port: 5065\n", "")
	if _, err := Parse(strings.NewReader(without + "      confidentiality: true\n")); err == nil {
		t.Error("confidentiality without a protected client port was accepted")
	}
}

// TestESPRefusedWithoutAnAddress: the security associations are bound
// to the P-CSCF's own address, which it cannot read off a socket that
// listens on every address, so the configuration has to name it.
func TestESPRefusedWithoutAnAddress(t *testing.T) {
	for _, c := range []struct {
		what string
		yaml string
	}{
		{"every address", strings.ReplaceAll(pcscfWithESP, "172.28.0.40:5060", "0.0.0.0:5060")},
		{"the protected server port again", strings.ReplaceAll(pcscfWithESP, "protected_client_port: 5065", "protected_client_port: 5064")},
		{"the standard SIP port", strings.ReplaceAll(pcscfWithESP, "protected_client_port: 5065", "protected_client_port: 5060")},
	} {
		if _, err := Parse(strings.NewReader(c.yaml)); err == nil {
			t.Errorf("a P-CSCF with ESP on %s was accepted", c.what)
		}
	}
}
