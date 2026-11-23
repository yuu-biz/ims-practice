package config

import (
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ifc"

	"github.com/yuu-biz/ims-practice/internal/milenage"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

func TestComposeConfigsLoad(t *testing.T) {
	files, err := filepath.Glob("../../deploy/compose/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files in deploy/compose")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			if _, err := Load(f); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestComposeFilterCriteriaNameTheApplicationServers: the S-CSCF sends
// requests to the ServerName of a criterion. The criteria of MESSAGE
// and REGISTER name the IP-SM-GW, whose URI the UE also answers with
// its delivery report (TS 24.341 5.3.2.4 NOTE 1); those of INVITE name
// the MMTel AS, once for each session case it serves.
func TestComposeFilterCriteriaNameTheApplicationServers(t *testing.T) {
	hssCfg, err := Load("../../deploy/compose/hss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	gwCfg, err := Load("../../deploy/compose/ipsmgw.yaml")
	if err != nil {
		t.Fatal(err)
	}
	mmtelCfg, err := Load("../../deploy/compose/mmtel.yaml")
	if err != nil {
		t.Fatal(err)
	}
	criteria := hssCfg.HSS.ServiceProfiles[hssCfg.HSS.DefaultServiceProfile]
	if len(criteria) == 0 {
		t.Fatalf("the service profile %q of hss.yaml has no filter criteria", hssCfg.HSS.DefaultServiceProfile)
	}
	var mmtelCases []ifc.SessionCase
	for _, c := range criteria {
		want := gwCfg.IPSMGW.SIP.URI()
		for _, spt := range c.TriggerPoint.SPT {
			if spt.Method == "INVITE" {
				want = mmtelCfg.MMTel.SIP.URI()
			}
			if spt.SessionCase != nil && want == mmtelCfg.MMTel.SIP.URI() {
				mmtelCases = append(mmtelCases, *spt.SessionCase)
			}
		}
		if c.ApplicationServer.ServerName != want {
			t.Errorf("criterion %d names %s, want %s", c.Priority, c.ApplicationServer.ServerName, want)
		}
	}
	slices.Sort(mmtelCases)
	want := []ifc.SessionCase{ifc.OriginatingRegistered, ifc.TerminatingRegistered, ifc.TerminatingUnregistered, ifc.OriginatingCDIV}
	slices.Sort(want)
	if !slices.Equal(mmtelCases, want) {
		t.Errorf("the MMTel AS serves the session cases %v, want %v", mmtelCases, want)
	}
}

// TestComposeAKASubscribers: the Compose HSS has three subscribers for
// IMS-AKA, without a password. The two VoLTE UEs have the K and OPc of
// TS 35.208 test sets 1 and 3 (4.3.1, 4.3.3), and SIPp the printable K
// and OP that its scenario writes out, with the OPc that MILENAGE
// derives from that OP (TS 35.206 4.1).
func TestComposeAKASubscribers(t *testing.T) {
	cfg, err := Load("../../deploy/compose/hss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	hex16 := func(s string) [16]byte {
		var b [16]byte
		if n, err := hex.Decode(b[:], []byte(s)); err != nil || n != 16 {
			t.Fatalf("%q: %v", s, err)
		}
		return b
	}
	text16 := func(s string) [16]byte {
		var b [16]byte
		if copy(b[:], s) != 16 {
			t.Fatalf("%q is not 16 bytes", s)
		}
		return b
	}
	want := map[string]struct{ k, op [16]byte }{
		// Test set 1.
		"001010000000010@ims.mnc001.mcc001.3gppnetwork.org": {hex16("465b5ce8b199b49faa5f0a2ee238a6bc"), hex16("cdc202d5123e20f62b6d676ac72cb318")},
		// Test set 3.
		"001010000000012@ims.mnc001.mcc001.3gppnetwork.org": {hex16("fec86ba6eb707ed08905757b1bb44b8f"), hex16("dbc59adcb6f9a0ef735477b7fadf8374")},
		// images/softphone/sipp/register-aka.xml.
		"001010000000011@ims.mnc001.mcc001.3gppnetwork.org": {text16("ims-practice-K01"), text16("ims-practice-OP1")},
	}
	found := map[string]bool{}
	for _, s := range cfg.HSS.Subscriptions {
		if s.AKA == nil {
			continue
		}
		found[s.PrivateIdentity] = true
		w, ok := want[s.PrivateIdentity]
		if !ok {
			t.Errorf("%s: an AKA subscriber this test does not know", s.PrivateIdentity)
			continue
		}
		if s.Password != "" || s.AKA.K != w.k || s.AKA.OPc != milenage.OPc(w.k, w.op) {
			t.Errorf("%s: password %q, K %x, OPc %x; want no password, K %x and the OPc of OP %x", s.PrivateIdentity, s.Password, s.AKA.K, s.AKA.OPc, w.k, w.op)
		}
	}
	for impi := range want {
		if !found[impi] {
			t.Errorf("%s: no AKA subscriber", impi)
		}
	}
}

// TestComposeEPCServesTheHSSSubscribers: the stub serves IMSIs whose
// private identities are in the HSS, in the <IMSI>@<home domain> form of
// TS 23.003 13.3.
func TestComposeEPCServesTheHSSSubscribers(t *testing.T) {
	hssCfg, err := Load("../../deploy/compose/hss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	epcCfg, err := Load("../../deploy/compose/epc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, s := range hssCfg.HSS.Subscriptions {
		known[s.PrivateIdentity] = true
	}
	for _, s := range epcCfg.EPC.Subscribers {
		if !known[s.IMSI+"@"+hssCfg.HomeDomain] {
			t.Errorf("epc.yaml serves IMSI %s, which hss.yaml has no subscription for", s.IMSI)
		}
	}
}

// TestComposeSubscribersHaveSupplementaryServices: every subscriber of
// the Compose HSS starts with the operator's MMTelServices, each rule of
// call forwarding and barring there but deactivated (GSMA IR.92 2.3.2),
// and a Ut credential for the XCAP server.
func TestComposeSubscribersHaveSupplementaryServices(t *testing.T) {
	cfg, err := Load("../../deploy/compose/hss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cfg.HSS.Subscriptions {
		data := map[string][]byte{}
		for _, d := range s.RepositoryData {
			data[d.ServiceIndication] = d.ServiceData.XML
		}
		cred, err := xcap.ParseCredential(data[xcap.CredentialServiceIndication])
		if err != nil || cred.Username != s.PrivateIdentity || cred.Realm != cfg.HomeDomain {
			t.Errorf("%s: Ut credential %+v, %v", s.PrivateIdentity, cred, err)
		}
		m, err := simservs.ParseMMTelServices(data[simservs.ServiceIndication])
		if err != nil {
			t.Fatalf("%s: %v", s.PrivateIdentity, err)
		}
		var rules []simservs.Rule
		rules = append(rules, m.CompleteCommunicationDiversion.CommunicationDiversion.Ruleset.Rules...)
		rules = append(rules, m.CompleteCommunicationBarring.IncomingCommunicationBarring.Ruleset.Rules...)
		rules = append(rules, m.CompleteCommunicationBarring.OutgoingCommunicationBarring.Ruleset.Rules...)
		var ids []string
		for _, r := range rules {
			ids = append(ids, r.ID)
			if r.Conditions == nil || r.Conditions.RuleDeactivated == nil {
				t.Errorf("%s: rule %s is active", s.PrivateIdentity, r.ID)
			}
		}
		want := []string{"cfu", "cfb", "cfnr", "cfnrc", "cfnl", "baic", "bic-roam", "acr", "baoc", "boic", "boic-exhc"}
		if !slices.Equal(ids, want) {
			t.Errorf("%s: rules %v, want %v", s.PrivateIdentity, ids, want)
		}
		// Subscriber 2 has CAT provisioned but off, so that its calls
		// in the other scenarios stay as they are until the operator
		// turns CAT on.
		cat, ok := data[mmtel.CATServiceIndication]
		switch {
		case strings.HasPrefix(s.PrivateIdentity, "001010000000002@"):
			if t2, err := mmtel.ParseCustomizedAlertingTone(cat); !ok || err != nil || t2.Active || t2.Tone != "cat1" {
				t.Errorf("%s: CAT %s, %v", s.PrivateIdentity, cat, err)
			}
		case ok:
			t.Errorf("%s has CAT %s", s.PrivateIdentity, cat)
		}
	}
}
