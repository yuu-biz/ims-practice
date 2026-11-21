package ims

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// A Security-Client in the syntax of TS 33.203 Annex H, beside a tls
// mechanism that the parser leaves out.
func TestIPsec3GPPMechanisms(t *testing.T) {
	m := &sip.Message{Method: sip.REGISTER}
	m.Add("Security-Client", "tls;q=0.5, ipsec-3gpp;alg=hmac-sha-1-96;prot=esp;mod=trans;ealg=null;spi-c=4096;spi-s=4097;port-c=50001;port-s=5062")
	got, err := IPsec3GPPMechanisms(m, "Security-Client")
	if err != nil {
		t.Fatal(err)
	}
	want := IPsec3GPP{Transform: Transform{"hmac-sha-1-96", "null"}, Protocol: "esp", Mode: "trans",
		SPIC: 4096, SPIS: 4097, PortC: 50001, PortS: 5062}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("IPsec3GPPMechanisms = %+v, want %+v", got, want)
	}
	if s := got[0].SecurityMechanism().String(); s != "ipsec-3gpp;alg=hmac-sha-1-96;prot=esp;mod=trans;ealg=null;spi-c=4096;spi-s=4097;port-c=50001;port-s=5062" {
		t.Errorf("SecurityMechanism() = %s", s)
	}
	for _, bad := range []string{
		"ipsec-3gpp;spi-c=1;spi-s=2;port-c=3;port-s=4",
		"ipsec-3gpp;alg=hmac-sha-1-96;spi-c=1;spi-s=2;port-c=3",
		"ipsec-3gpp;alg=hmac-sha-1-96;spi-c=1;spi-s=4294967296;port-c=3;port-s=4",
	} {
		sm, _ := sip.ParseSecurityMechanism(bad)
		if _, err := ParseIPsec3GPP(sm); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
}

// TS 33.203 7.2: the P-CSCF's order decides, and a missing ealg is null.
func TestChooseTransform(t *testing.T) {
	pcscf := []IPsec3GPP{{Transform: Transform{"hmac-sha2-256-128", "null"}}, {Transform: Transform{"hmac-sha-1-96", "null"}}}
	ue := []IPsec3GPP{{Transform: Transform{Algorithm: "hmac-sha-1-96"}}, {Transform: Transform{"hmac-sha2-256-128", "null"}}}
	if got, theirs, ok := ChooseTransform(pcscf, ue); !ok || got.Algorithm != "hmac-sha2-256-128" || theirs != ue[1] {
		t.Errorf("chose %+v and %+v, %v; want the P-CSCF's first", got, theirs, ok)
	}
	if got, theirs, ok := ChooseTransform(pcscf, ue[:1]); !ok || got.Algorithm != "hmac-sha-1-96" || theirs != ue[0] {
		t.Errorf("chose %+v and %+v, %v; want hmac-sha-1-96 with null", got, theirs, ok)
	}
	if _, _, ok := ChooseTransform(pcscf, []IPsec3GPP{{Transform: Transform{"aes-gmac", "null"}}}); ok {
		t.Error("chose a transform the P-CSCF does not have")
	}
}

func TestNewSPIAndPort(t *testing.T) {
	for range 100 {
		spi := NewSPI(4096)
		if spi <= 255 || spi == 4096 {
			t.Fatalf("NewSPI = %d", spi)
		}
		if port := NewProtectedPort(); port < 49152 {
			t.Fatalf("NewProtectedPort = %d", port)
		}
	}
}
