package sh

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

const (
	realm  = "ims.mnc001.mcc001.3gppnetwork.org"
	impu   = "sip:001010000000001@" + realm
	tel    = "tel:+12025550101"
	msisdn = "12025550101"
	ipsmgw = "ipsmgw." + realm
)

var hss = diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}

var header = diameter.Header{SessionID: ipsmgw + ";1;2", OriginHost: ipsmgw, OriginRealm: realm, DestinationRealm: realm}

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

// wire serializes m and reads it back, as a peer would.
func wire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	b, err := m.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	got, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestUserDataRoundTrip(t *testing.T) {
	for _, udr := range []UserDataRequest{
		{Header: header, UserIdentity: UserIdentity{MSISDN: msisdn}, DataReferences: []DataReference{IMSPublicIdentity}},
		{Header: header, UserIdentity: UserIdentity{PublicIdentity: impu}, ServerName: "sip:" + ipsmgw,
			DataReferences: []DataReference{IMSPublicIdentity}, IdentitySets: []IdentitySet{RegisteredIdentities}},
	} {
		req := wire(t, udr.Message())
		got, err := ParseUserDataRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, udr) {
			t.Errorf("UDR\n got %+v\nwant %+v", got, udr)
		}

		uda := UserDataAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserData: []byte("<Sh-Data/>")}
		ans := wire(t, uda.Message(req, hss))
		if ans.Header.ApplicationID != diameter.ApplicationIDSh || ans.Header.CommandCode != CommandUserData {
			t.Errorf("UDA header = %+v", ans.Header)
		}
		gotA, err := ParseUserDataAnswer(ans)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotA, uda) {
			t.Errorf("UDA\n got %+v\nwant %+v", gotA, uda)
		}
	}
}

func TestProfileUpdateRoundTrip(t *testing.T) {
	pur := ProfileUpdateRequest{Header: header, UserIdentity: UserIdentity{PublicIdentity: tel},
		DataReferences: []DataReference{SMSRegistrationInfo}, UserData: []byte("<Sh-Data/>")}
	req := wire(t, pur.Message())
	got, err := ParseProfileUpdateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, pur) {
		t.Errorf("PUR\n got %+v\nwant %+v", got, pur)
	}

	pua := ProfileUpdateAnswer{Result: diameter.Experimental(DiameterErrorUserDataCannotBeModified)}
	gotA, err := ParseProfileUpdateAnswer(wire(t, pua.Message(req, hss)))
	if err != nil {
		t.Fatal(err)
	}
	if gotA != pua {
		t.Errorf("PUA = %+v, want %+v", gotA, pua)
	}
}

// avpAt reads the AVP header at b[0:]: code, flags, length and vendor
// (RFC 6733 4.1).
func avpAt(b []byte) (code uint32, flags byte, length int, vendor uint32) {
	code = binary.BigEndian.Uint32(b)
	flags = b[4]
	length = int(binary.BigEndian.Uint32(b[4:]) & 0xffffff)
	if flags&0x80 != 0 {
		vendor = binary.BigEndian.Uint32(b[8:])
	}
	return
}

// TestUserDataRequestBytes checks the encoding against the numbers of
// TS 29.329 rather than against this package's own decoder: the header
// of TS 29.329 6.1.1, the User-Identity, MSISDN and Data-Reference
// codes and flags of Table 6.3.1, and the TBCD digits of 6.3.2.
func TestUserDataRequestBytes(t *testing.T) {
	b, err := UserDataRequest{Header: header, UserIdentity: UserIdentity{MSISDN: msisdn},
		DataReferences: []DataReference{IMSPublicIdentity}}.Message().Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], uint32(b[5])<<16|uint32(b[6])<<8|uint32(b[7]), binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 306 || app != 16777217 {
		t.Fatalf("header: flags %#x, command %d, application %d; want R and P set, 306, 16777217", flags, code, app)
	}
	var userIdentity, dataReference []byte
	for off := 20; off < len(b); {
		code, _, length, vendor := avpAt(b[off:])
		if vendor == 10415 {
			switch code {
			case 700:
				userIdentity = b[off : off+length]
			case 703:
				dataReference = b[off : off+length]
			}
		}
		off += (length + 3) &^ 3
	}
	if userIdentity == nil || userIdentity[4] != 0xc0 {
		t.Fatalf("User-Identity (700, V and M) = % x", userIdentity)
	}
	// User-Identity holds one MSISDN AVP: code 701, V and M, length 18,
	// vendor 10415, then +1 202 555 0101 in TBCD with a filler.
	want := []byte{0, 0, 0x02, 0xbd, 0xc0, 0, 0, 18, 0, 0, 0x28, 0xaf, 0x21, 0x20, 0x55, 0x05, 0x01, 0xf1}
	if got := userIdentity[12:30]; !bytes.Equal(got, want) {
		t.Errorf("MSISDN AVP = % x, want % x", got, want)
	}
	if want := []byte{0, 0, 0x02, 0xbf, 0xc0, 0, 0, 16, 0, 0, 0x28, 0xaf, 0, 0, 0, 10}; !bytes.Equal(dataReference, want) {
		t.Errorf("Data-Reference AVP = % x, want % x", dataReference, want)
	}
}

func TestIdentitySetIsNotMandatory(t *testing.T) {
	m := UserDataRequest{Header: header, UserIdentity: UserIdentity{PublicIdentity: impu},
		DataReferences: []DataReference{IMSPublicIdentity}, IdentitySets: []IdentitySet{AllIdentities}}.Message()
	a, err := m.FindAVP(avpIdentitySet, diameter.VendorID3GPP)
	if err != nil {
		t.Fatal(err)
	}
	// TS 29.329 Table 6.3.1: V must be set, M must not.
	if a.Flags != 0x80 {
		t.Errorf("Identity-Set flags = %#x, want 0x80", a.Flags)
	}
}

func TestValidMSISDN(t *testing.T) {
	for s, want := range map[string]bool{
		msisdn: true, "": false, "+12025550101": false, "1202555010a": false, "1234567890123456": false,
	} {
		if got := ValidMSISDN(s); got != want {
			t.Errorf("ValidMSISDN(%q) = %v, want %v", s, got, want)
		}
	}
}

// The element path of SMSRegistrationInfo comes from the schema of TS
// 29.328 Annex D: Sh-IMS-Data, then three Extension elements.
func TestShDataSMSRegistrationInfoPath(t *testing.T) {
	d := ShData{ShIMSData: &ShIMSData{SMSRegistrationInfo: &SMSRegistrationInfoData{IPSMGWNumber: "12025550199"}}}
	b := d.Marshal()
	want := "<Sh-Data><Sh-IMS-Data><Extension><Extension><Extension><SMSRegistrationInfo>" +
		"<IP-SM-GW-Number>12025550199</IP-SM-GW-Number></SMSRegistrationInfo></Extension></Extension></Extension></Sh-IMS-Data></Sh-Data>"
	if !strings.HasSuffix(string(b), want) {
		t.Errorf("Sh-Data = %s\nwant suffix %s", b, want)
	}
	got, err := ParseShData(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, d) {
		t.Errorf("parsed %+v, want %+v", got, d)
	}
}

func TestShDataPublicIdentifiersAndSCSCFName(t *testing.T) {
	d := ShData{
		PublicIdentifiers: &PublicIdentifiers{IMSPublicIdentities: []string{impu, tel}, MSISDNs: []string{msisdn}},
		ShIMSData:         &ShIMSData{SCSCFName: "sip:scscf." + realm + ":5060"},
	}
	b := d.Marshal()
	want := "<Sh-Data><PublicIdentifiers><IMSPublicIdentity>" + impu + "</IMSPublicIdentity><IMSPublicIdentity>" + tel +
		"</IMSPublicIdentity><MSISDN>" + msisdn + "</MSISDN></PublicIdentifiers><Sh-IMS-Data><SCSCFName>sip:scscf." + realm +
		":5060</SCSCFName></Sh-IMS-Data></Sh-Data>"
	if !strings.HasSuffix(string(b), want) {
		t.Errorf("Sh-Data = %s\nwant suffix %s", b, want)
	}
	got, err := ParseShData(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, d) {
		t.Errorf("parsed %+v, want %+v", got, d)
	}
	if _, err := ParseShData([]byte("<IMSSubscription/>")); err == nil {
		t.Error("a document whose root is not Sh-Data was accepted")
	}
}

// The Lab reads Diameter as the JSON of diameter.EncodeJSON, so
// the Sh commands and AVPs must have their names there, as in
// Wireshark.
func TestShMessagesHaveNamesInTheLabJSON(t *testing.T) {
	b, err := diameter.EncodeJSON(UserDataRequest{Header: header, UserIdentity: UserIdentity{MSISDN: msisdn},
		DataReferences: []DataReference{IMSPublicIdentity}}.Message())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"command":"User-Data"`, `"name":"User-Identity"`, `"name":"MSISDN"`, `"name":"Data-Reference"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON lacks %s: %s", want, b)
		}
	}
}

// TestRepositoryDataRequest: a read of repository data names it with
// Service-Indication (TS 29.329 6.1.1), which the Lab shows by
// name like any other AVP.
func TestRepositoryDataRequest(t *testing.T) {
	udr := UserDataRequest{Header: header, UserIdentity: UserIdentity{PublicIdentity: impu},
		ServiceIndications: []string{"MMTEL-Services"}, DataReferences: []DataReference{RepositoryData}}
	got, err := ParseUserDataRequest(wire(t, udr.Message()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, udr) {
		t.Errorf("UDR\n got %+v\nwant %+v", got, udr)
	}
	b, err := diameter.EncodeJSON(udr.Message())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"Service-Indication"`, `"name":"Data-Reference"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON lacks %s: %s", want, b)
		}
	}
}

// TestRepositoryDataKeepsTheServiceDataAsItIs: the element in
// ServiceData, with its namespace, comes out of Sh-Data byte for byte,
// and a RepositoryData without ServiceData stays without it (TS 29.328
// Annex D, tTransparentData).
func TestRepositoryDataKeepsTheServiceDataAsItIs(t *testing.T) {
	element := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/mmtel"><complete-communication-diversion active="true"/></MMTelServices>`
	in := ShData{RepositoryData: []TransparentData{
		{ServiceIndication: "MMTEL-Services", SequenceNumber: 65535, ServiceData: &ServiceData{XML: []byte(element)}},
		{ServiceIndication: "removed", SequenceNumber: 7},
	}}
	doc := in.Marshal()
	if !strings.Contains(string(doc), "<RepositoryData><ServiceIndication>MMTEL-Services</ServiceIndication><SequenceNumber>65535</SequenceNumber><ServiceData>"+element+"</ServiceData></RepositoryData>") {
		t.Errorf("Sh-Data = %s", doc)
	}
	out, err := ParseShData(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("parsed\n %+v\nwant %+v", out, in)
	}
	if _, err := ParseShData([]byte(`<Sh-Data><RepositoryData><ServiceIndication>x</ServiceIndication><SequenceNumber>65536</SequenceNumber></RepositoryData></Sh-Data>`)); err == nil {
		t.Error("a Sequence Number above 65535 was accepted")
	}
}

func TestNextSequenceNumber(t *testing.T) {
	for n, want := range map[uint16]uint16{0: 1, 1: 2, 65534: 65535, 65535: 1} {
		if got := NextSequenceNumber(n); got != want {
			t.Errorf("NextSequenceNumber(%d) = %d, want %d", n, got, want)
		}
	}
}
