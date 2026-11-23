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
