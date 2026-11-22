package ue_test

import (
	"net/netip"
	"strings"
	"testing"
	"uuid"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// TestContactOfADevice is TS 24.229 5.1.1.2.1 item c: the user part of
// a UE's contact is globally unique and reveals nothing private. The
// same UE, which has the same identity and key, makes the same contact
// each time it starts; a UE with another key or password makes another.
func TestContactOfADevice(t *testing.T) {
	addr := netip.MustParseAddrPort("192.0.2.10:5062")
	otherKey := testSet1
	otherKey.K[15] ^= 1
	volte := ue.Config{PrivateIdentity: impiAKA, PublicIdentity: impuAKA, AKA: &ue.AKA{Secret: testSet1}}
	softphone := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password}
	for _, c := range []struct {
		name string
		a, b ue.Config
		same bool
	}{
		{"the same USIM", volte, volte, true},
		{"the same password", softphone, softphone, true},
		{"another K", volte, ue.Config{PrivateIdentity: impiAKA, PublicIdentity: impuAKA, AKA: &ue.AKA{Secret: otherKey}}, false},
		{"another password", softphone, ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password2}, false},
		{"the same password for another user", softphone, ue.Config{PrivateIdentity: impi2, PublicIdentity: impu2, Password: password}, false},
	} {
		a, b := ue.NewContact(c.a, addr).URI(), ue.NewContact(c.b, addr).URI()
		if (a == b) != c.same {
			t.Errorf("%s: contacts %s and %s, want them the same: %v", c.name, a, b, c.same)
		}
	}

	const imsi = "001010000000010"
	contact := ue.NewContact(volte, addr).URI()
	u, err := sip.ParseURI(contact)
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(u.User)
	// RFC 9562 4.2 and 4.1: version 8, variant 10.
	if err != nil || id[6]>>4 != 8 || id[8]>>6 != 0b10 {
		t.Errorf("the user part of %s is not a version 8 UUID", contact)
	}
	if strings.Contains(contact, imsi) || u.Host != "192.0.2.10" || u.Port != 5062 {
		t.Errorf("contact %s, want a UUID without the IMSI %s at %v", contact, imsi, addr)
	}
}
