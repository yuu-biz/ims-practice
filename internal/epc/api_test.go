package epc

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestPathIsTheMessageNameInLowerCase(t *testing.T) {
	for typ, want := range map[MessageType]string{
		TypeAttachRequest: "/ues/001010000000001/attach-request",
		TypeActivateDedicatedEPSBearerContextAccept: "/ues/001010000000001/activate-dedicated-eps-bearer-context-accept",
	} {
		if got := Path("001010000000001", typ); got != want {
			t.Errorf("Path(%q) = %s, want %s", typ, got, want)
		}
	}
}

// TS 24.008 10.5.6.3 writes container identifiers as four hexadecimal
// digits and H; the JSON does the same, so a learner can look them up.
func TestPCOContainersAreWrittenAsTheSpecWritesThem(t *testing.T) {
	pco := ProtocolConfigurationOptions{Containers: []Container{
		{ID: PCSCFIPv4Address, Contents: "172.28.0.40"},
		{ID: PCSCFIPv4Address, Contents: "172.28.0.41"},
	}}
	b, err := json.Marshal(pco)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"containers":[{"id":"000CH","contents":"172.28.0.40"},{"id":"000CH","contents":"172.28.0.41"}]}`; string(b) != want {
		t.Errorf("PCO = %s, want %s", b, want)
	}
	var got ProtocolConfigurationOptions
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{netip.MustParseAddr("172.28.0.40"), netip.MustParseAddr("172.28.0.41")}
	if addrs := got.PCSCFIPv4Addresses(); !reflect.DeepEqual(addrs, want) {
		t.Errorf("P-CSCF addresses = %v, want %v", addrs, want)
	}
	if err := json.Unmarshal([]byte(`{"containers":[{"id":"12"}]}`), &got); err == nil || !strings.Contains(err.Error(), "000CH") {
		t.Errorf("a container identifier without H was accepted: %v", err)
	}
}

func TestPCOWithoutAddressesIsARequest(t *testing.T) {
	req := PCSCFIPv4AddressRequest()
	if !req.asksForPCSCF() || len(req.PCSCFIPv4Addresses()) != 0 {
		t.Errorf("request = %+v", req)
	}
	if (ProtocolConfigurationOptions{}).asksForPCSCF() {
		t.Error("an empty PCO asks for the P-CSCF")
	}
}
