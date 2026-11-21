package sip

import (
	"testing"
)

// The Security-Server of a P-CSCF, in the syntax of TS 33.203 Annex H,
// split over two rows the way RFC 3261 7.3.1 allows.
func TestSecurityMechanisms(t *testing.T) {
	m := &Message{StatusCode: 401}
	m.Add("Security-Server", "ipsec-3gpp;q=0.2;alg=hmac-sha2-256-128;ealg=null;spi-c=1001;spi-s=1002;port-c=5065;port-s=5064, "+
		"ipsec-3gpp;q=0.1;alg=hmac-sha-1-96;ealg=null;spi-c=1001;spi-s=1002;port-c=5065;port-s=5064")
	got, err := SecurityMechanisms(m, "Security-Server")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "ipsec-3gpp" {
		t.Fatalf("SecurityMechanisms = %+v", got)
	}
	if alg, _ := got[1].Params.Get("alg"); alg != "hmac-sha-1-96" {
		t.Errorf("second alg = %q", alg)
	}
	if s := got[0].String(); s != "ipsec-3gpp;q=0.2;alg=hmac-sha2-256-128;ealg=null;spi-c=1001;spi-s=1002;port-c=5065;port-s=5064" {
		t.Errorf("String() = %s", s)
	}

	// RFC 3329 2.3.1: Security-Verify mirrors the list. The order of the
	// parameters and the case of names do not count; the order of the
	// mechanisms and every value do.
	mirror, _ := ParseSecurityMechanism("IPSEC-3GPP;alg=HMAC-SHA2-256-128;q=0.2;ealg=null;spi-c=1001;spi-s=1002;port-c=5065;port-s=5064")
	if !SameSecurityMechanisms([]SecurityMechanism{got[0], got[1]}, []SecurityMechanism{mirror, got[1]}) {
		t.Error("a faithful mirror is not the same")
	}
	if SameSecurityMechanisms(got, []SecurityMechanism{got[1], got[0]}) {
		t.Error("another order is the same")
	}
	changed, _ := ParseSecurityMechanism("ipsec-3gpp;q=0.2;alg=hmac-sha2-256-128;ealg=null;spi-c=1001;spi-s=1002;port-c=5065;port-s=5066")
	if SameSecurityMechanisms(got, []SecurityMechanism{changed, got[1]}) {
		t.Error("a changed port is the same")
	}
	dropped, _ := ParseSecurityMechanism("ipsec-3gpp;q=0.2;alg=hmac-sha2-256-128;spi-c=1001;spi-s=1002;port-c=5065;port-s=5064")
	if SameSecurityMechanisms(got, []SecurityMechanism{dropped, got[1]}) {
		t.Error("a dropped parameter is the same")
	}
	if _, err := ParseSecurityMechanism(";alg=null"); err == nil {
		t.Error("a mechanism without a name parsed")
	}
}

func TestParamsRemove(t *testing.T) {
	p := ParseParams("a=1;B=2;c")
	p.Remove("b")
	if p.String() != ";a=1;c" {
		t.Errorf("after Remove: %s", p.String())
	}
}
