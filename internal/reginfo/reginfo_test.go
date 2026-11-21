package reginfo

import (
	"reflect"
	"strings"
	"testing"
)

// example is the document of RFC 3680 5.3.
const example = `<?xml version="1.0"?>
    <reginfo xmlns="urn:ietf:params:xml:ns:reginfo"
        xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                 version="0" state="full">
      <registration aor="sip:user@example.com" id="as9"
                    state="active">
        <contact id="76" state="active" event="registered"
                 duration-registered="7322"
                 q="0.8">
                 <uri>sip:user@pc887.example.com</uri>
        </contact>
        <contact id="77" state="terminated" event="expired"
                 duration-registered="3600"
                 q="0.5">
                 <uri>sip:user@university.edu</uri>
        </contact>
      </registration>
    </reginfo>`

var exampleDocument = Document{
	Version: 0,
	State:   Full,
	Registrations: []Registration{{
		AOR: "sip:user@example.com", ID: "as9", State: Active,
		Contacts: []Contact{
			{ID: "76", State: Active, Event: Registered, DurationRegistered: 7322, Q: "0.8", URI: "sip:user@pc887.example.com"},
			{ID: "77", State: Terminated, Event: Expired, DurationRegistered: 3600, Q: "0.5", URI: "sip:user@university.edu"},
		},
	}},
}

func TestParseTheExampleOfRFC3680(t *testing.T) {
	got, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	got.XMLName = exampleDocument.XMLName
	if !reflect.DeepEqual(got, exampleDocument) {
		t.Errorf("got %+v\nwant %+v", got, exampleDocument)
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	d := Document{Version: 3, State: Full, Registrations: []Registration{{
		AOR: "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org", ID: "r0", State: Active,
		Contacts: []Contact{{ID: "c1", State: Active, Event: Refreshed, Expires: 3599, CallID: "abc", CSeq: 3,
			URI: "sip:001010000000001@172.28.0.100:5060", UnknownParams: []UnknownParam{{Name: "audio"}}}},
	}}}
	b, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.HasPrefix(text, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("no XML declaration:\n%s", text)
	}
	// The namespace is declared once, on the root; the elements below
	// inherit it.
	if n := strings.Count(text, "xmlns"); n != 1 || !strings.Contains(text, `<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="3" state="full">`) {
		t.Errorf("namespace declared %d times:\n%s", n, text)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	back.XMLName = d.XMLName
	if !reflect.DeepEqual(back, d) {
		t.Errorf("round trip changed the document:\n%+v\n%+v", back, d)
	}
}

// TestElementsOfOtherNamespacesAreIgnored uses the first example of TS
// 24.229 5.4.2.1.2, which carries policy elements of 3GPP namespaces.
func TestElementsOfOtherNamespacesAreIgnored(t *testing.T) {
	doc := `<?xml version="1.0"?>
<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" xmlns:cp="urn:ietf:params:xml:ns:common-policy"
    xmlns:eri="urn:3gpp:ns:extRegInfo:1.0" version="0" state="full">
  <registration aor="sip:user1_public2@home1.net" id="as10" state="active">
    <contact id="86" state="active" event="created">
      <uri>sip:[5555::aaa:bbb:ccc:ddd]</uri>
      <unknown-param name="audio"/>
    </contact>
    <cp:actions><eri:rph ns="wps" val="1"/><eri:privSender/></cp:actions>
  </registration>
</reginfo>`
	d, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	c := d.Registrations[0].Contacts[0]
	if c.Event != Created || c.URI != "sip:[5555::aaa:bbb:ccc:ddd]" || len(c.UnknownParams) != 1 || c.UnknownParams[0].Name != "audio" {
		t.Errorf("contact = %+v", c)
	}
}

func TestParseRejectsWhatTheSchemaForbids(t *testing.T) {
	for name, doc := range map[string]string{
		"another namespace":  `<reginfo xmlns="urn:ietf:params:xmlns:reginfo" version="0" state="full"/>`,
		"no state":           `<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="0"/>`,
		"registration state": `<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="0" state="full"><registration aor="sip:a@b" id="1" state="gone"/></reginfo>`,
		"contact in init": `<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="0" state="full"><registration aor="sip:a@b" id="1" state="active">` +
			`<contact id="1" state="init" event="registered"><uri>sip:a@c</uri></contact></registration></reginfo>`,
		"unknown event": `<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="0" state="full"><registration aor="sip:a@b" id="1" state="active">` +
			`<contact id="1" state="active" event="moved"><uri>sip:a@c</uri></contact></registration></reginfo>`,
		"shortened without expires": `<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="0" state="full"><registration aor="sip:a@b" id="1" state="active">` +
			`<contact id="1" state="active" event="shortened"><uri>sip:a@c</uri></contact></registration></reginfo>`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}
