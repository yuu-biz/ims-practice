package diameter

import (
	"slices"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

func TestLoadDictionaryAddsCxAndSh(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{ApplicationIDCx, ApplicationIDSh} {
		app, err := dict.Default.App(id)
		if err != nil {
			t.Fatalf("application %d not in the dictionary: %v", id, err)
		}
		if len(app.Vendor) != 1 || app.Vendor[0].ID != VendorID3GPP {
			t.Fatalf("application %d vendor = %+v, want 3GPP (%d)", id, app.Vendor, VendorID3GPP)
		}
	}
}

// The Sh codes and flags are those of TS 29.329 Table 6.1.1 and Table
// 6.3.1. A wrong one would still round-trip here, but Wireshark and a
// product HSS would read something else.
func TestShDictionaryMatchesTS29329(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	commands := map[uint32]string{306: "UD", 307: "PU"}
	for code, short := range commands {
		c, err := dict.Default.FindCommand(ApplicationIDSh, code)
		if err != nil || c.Short != short {
			t.Errorf("command %d = %+v, %v, want %s", code, c, err, short)
		}
	}
	avps := []struct {
		name      string
		code      uint32
		mandatory bool
	}{
		{"User-Identity", 700, true},
		{"MSISDN", 701, true},
		{"User-Data", 702, true},
		{"Data-Reference", 703, true},
		{"Identity-Set", 708, false},
		{"Public-Identity", 601, true},
		{"Server-Name", 602, true},
	}
	for _, want := range avps {
		a, err := dict.Default.FindAVPWithVendor(ApplicationIDSh, want.name, VendorID3GPP)
		if err != nil {
			t.Errorf("%s: %v", want.name, err)
			continue
		}
		if a.Code != want.code || a.Must != map[bool]string{true: "M,V", false: "V"}[want.mandatory] {
			t.Errorf("%s = code %d, must %q; want code %d, mandatory %v", want.name, a.Code, a.Must, want.code, want.mandatory)
		}
	}
}

// The Cx AVPs of IMS-AKA, with the codes and flags of TS 29.229 V19.1.0
// Table 6.3.0.1, and their place in SIP-Auth-Data-Item (6.3.13).
func TestCxDictionaryHasTheAKAItems(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	avps := []struct {
		name string
		code uint32
		typ  string
	}{
		{"SIP-Authentication-Scheme", 608, "UTF8String"},
		{"SIP-Authenticate", 609, "OctetString"},
		{"SIP-Authorization", 610, "OctetString"},
		{"SIP-Auth-Data-Item", 612, "Grouped"},
		{"SIP-Item-Number", 613, "Unsigned32"},
		{"Confidentiality-Key", 625, "OctetString"},
		{"Integrity-Key", 626, "OctetString"},
	}
	for _, want := range avps {
		a, err := dict.Default.FindAVPWithVendor(ApplicationIDCx, want.name, VendorID3GPP)
		if err != nil {
			t.Errorf("%s: %v", want.name, err)
			continue
		}
		if a.Code != want.code || a.Must != "M,V" || a.Data.TypeName != want.typ {
			t.Errorf("%s = code %d, must %q, type %s; want code %d, M,V, %s", want.name, a.Code, a.Must, a.Data.TypeName, want.code, want.typ)
		}
	}
	item, err := dict.Default.FindAVPWithVendor(ApplicationIDCx, "SIP-Auth-Data-Item", VendorID3GPP)
	if err != nil {
		t.Fatal(err)
	}
	var children []string
	for _, r := range item.Data.Rule {
		children = append(children, r.AVP)
	}
	for _, name := range []string{"SIP-Item-Number", "SIP-Authentication-Scheme", "SIP-Authenticate", "SIP-Authorization", "Confidentiality-Key", "Integrity-Key"} {
		if !slices.Contains(children, name) {
			t.Errorf("SIP-Auth-Data-Item has no rule for %s: %v", name, children)
		}
	}
}
