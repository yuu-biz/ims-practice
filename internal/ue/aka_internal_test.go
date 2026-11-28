package ue

import (
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ims"
)

// TestSecurityClientOffersEncryptionWithESP: TS 33.203 5.1.3 item 1 has
// the UE offer the encryption algorithms it supports, which it does
// wherever it sets ESP up: its Security-Client then carries AES-CBC
// before null, with the same SPIs and protected ports in each
// mechanism. A UE that sets no ESP up encrypts nothing whatever it
// offered, so it offers null encryption alone and its signalling stays
// readable.
func TestSecurityClientOffersEncryptionWithESP(t *testing.T) {
	for _, c := range []struct {
		what string
		esp  *ESP
		want []string
	}{
		{"without ESP", nil, []string{"null"}},
		{"with ESP", &ESP{ProtectedClientPort: 5063}, []string{"aes-cbc", "null"}},
	} {
		u := &UE{cfg: Config{AKA: &AKA{ESP: c.esp}},
			contact: Contact{addr: netip.MustParseAddrPort("127.0.0.1:5062")}}
		client := u.newSecurityClient(nil)
		var got []string
		for _, m := range client {
			got = append(got, m.EncryptionAlgorithm)
			if m.Algorithm != "hmac-sha2-256-128" {
				t.Errorf("%s: a mechanism offers alg %q", c.what, m.Algorithm)
			}
			if m.SPIC != client[0].SPIC || m.SPIS != client[0].SPIS ||
				m.PortC != client[0].PortC || m.PortS != client[0].PortS {
				t.Errorf("%s: the mechanisms name different associations: %+v", c.what, client)
			}
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: Security-Client offers %v, want %v", c.what, got, c.want)
		}
		for i, want := range c.want {
			if got[i] != want {
				t.Errorf("%s: Security-Client offers %v, want %v", c.what, got, c.want)
			}
		}
		if c.esp != nil && client[0].PortC != c.esp.ProtectedClientPort {
			t.Errorf("%s: port-c is %d, want the bound %d", c.what, client[0].PortC, c.esp.ProtectedClientPort)
		}
		if u.confidentiality() != ims.Confidentiality(c.esp != nil) {
			t.Errorf("%s: the UE's policy is %v", c.what, u.confidentiality())
		}
	}
}
