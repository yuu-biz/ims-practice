package fivegc

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestPathIsTheMessageNameInLowerCase(t *testing.T) {
	for typ, want := range map[MessageType]string{
		TypeRegistrationRequest:                 "/ues/001010000000001/registration-request",
		TypePDUSessionEstablishmentRequest:      "/ues/001010000000001/pdu-session-establishment-request",
		TypePDUSessionModificationCommandReject: "/ues/001010000000001/pdu-session-modification-command-reject",
	} {
		if got := Path("001010000000001", typ); got != want {
			t.Errorf("Path(%q) = %s, want %s", typ, got, want)
		}
	}
}

// TS 24.008 10.5.6.3 writes container identifiers as four hexadecimal
// digits and H; the JSON does the same, so a learner can look them up.
func TestExtendedPCOContainersAreWrittenAsTheSpecWritesThem(t *testing.T) {
	pco := ExtendedProtocolConfigurationOptions{Containers: []Container{
		{ID: IMCNSubsystemSignalingFlag},
		{ID: PCSCFIPv4Address, Contents: "172.28.0.40"},
		{ID: PCSCFIPv6Address, Contents: "2001:db8::40"},
		{ID: PCSCFIPv4Address, Contents: "172.28.0.41"},
	}}
	b, err := json.Marshal(pco)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"containers":[{"id":"0002H"},{"id":"000CH","contents":"172.28.0.40"},{"id":"0001H","contents":"2001:db8::40"},{"id":"000CH","contents":"172.28.0.41"}]}`
	if string(b) != want {
		t.Errorf("extended PCO = %s, want %s", b, want)
	}
	var got ExtendedProtocolConfigurationOptions
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	addrs := []netip.Addr{netip.MustParseAddr("172.28.0.40"), netip.MustParseAddr("2001:db8::40"), netip.MustParseAddr("172.28.0.41")}
	if a := got.PCSCFAddresses(); !reflect.DeepEqual(a, addrs) {
		t.Errorf("P-CSCF addresses = %v, want %v", a, addrs)
	}
	if err := json.Unmarshal([]byte(`{"containers":[{"id":"12"}]}`), &got); err == nil || !strings.Contains(err.Error(), "000CH") {
		t.Errorf("a container identifier without H was accepted: %v", err)
	}
}

// An IPv6 address in an IPv4 container, or the other way round, is not
// a P-CSCF address.
func TestPCSCFAddressesKeepToTheVersionOfTheirContainer(t *testing.T) {
	pco := ExtendedProtocolConfigurationOptions{Containers: []Container{
		{ID: PCSCFIPv4Address, Contents: "2001:db8::40"},
		{ID: PCSCFIPv6Address, Contents: "172.28.0.40"},
	}}
	if a := pco.PCSCFAddresses(); len(a) != 0 {
		t.Errorf("P-CSCF addresses = %v, want none", a)
	}
}

func TestPCSCFAddressRequestAsksForBothVersions(t *testing.T) {
	req := PCSCFAddressRequest()
	for _, id := range []ContainerID{IMCNSubsystemSignalingFlag, PCSCFIPv4Address, PCSCFIPv6Address} {
		if !req.has(id) {
			t.Errorf("the request has no %04XH", uint16(id))
		}
	}
	if len(req.PCSCFAddresses()) != 0 {
		t.Errorf("a request holds addresses: %+v", req)
	}
}

// The JSON names of the information elements are those of TS 24.501,
// in lower case, so that a learner finds each in the spec.
func TestEstablishmentRequestJSONNamesTheInformationElements(t *testing.T) {
	m := NewULNASTransport(1, PDUSessionEstablishmentRequest{PDUSessionID: 1, PTI: 1, PDUSessionType: IPv4v6, SSCMode: SSCMode1,
		ExtendedProtocolConfigurationOptions: ExtendedProtocolConfigurationOptions{Containers: []Container{{ID: PCSCFIPv4Address}}}})
	m.RequestType, m.DNN = InitialRequest, "ims"
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"payload_container_type":"N1 SM information","payload_container":{"pdu_session_id":1,"pti":1,"pdu_session_type":"IPv4v6","ssc_mode":"SSC mode 1","extended_protocol_configuration_options":{"containers":[{"id":"000CH"}]}},"pdu_session_id":1,"request_type":"initial request","dnn":"ims"}`
	if string(b) != want {
		t.Errorf("UL NAS TRANSPORT =\n%s\nwant\n%s", b, want)
	}
}

func TestCauseNamesTheValueOfTheTable(t *testing.T) {
	if got := PDUSessionTypeIPv4OnlyAllowed.String(); got != "#50 PDU session type IPv4 only allowed" {
		t.Errorf("#50 = %q", got)
	}
	if got := Cause(99).String(); got != "#99" {
		t.Errorf("an unnamed cause = %q", got)
	}
}
