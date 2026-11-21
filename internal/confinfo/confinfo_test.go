package confinfo

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// fullDocument is a document written for these tests after the schema
// of RFC 4575 6: a three-party conference in which one user is on hold
// and one has left.
const fullDocument = `<?xml version="1.0" encoding="UTF-8"?>
<conference-info xmlns="urn:ietf:params:xml:ns:conference-info"
    entity="sip:conf-7f3a@mmtel.ims.mnc001.mcc001.3gppnetwork.org"
    state="full" version="4">
  <conference-description>
    <display-text>Ad hoc conference</display-text>
    <conf-uris>
      <entry>
        <uri>sip:conf-7f3a@mmtel.ims.mnc001.mcc001.3gppnetwork.org</uri>
        <purpose>participation</purpose>
      </entry>
    </conf-uris>
    <maximum-user-count>3</maximum-user-count>
    <available-media>
      <entry label="1">
        <type>audio</type>
        <status>sendrecv</status>
      </entry>
    </available-media>
  </conference-description>
  <conference-state>
    <user-count>2</user-count>
    <active>true</active>
    <locked>false</locked>
  </conference-state>
  <users>
    <user entity="sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org" state="full">
      <display-text>Alice</display-text>
      <endpoint entity="sip:+12025550101@192.0.2.11:5060">
        <status>connected</status>
        <joining-method>dialed-in</joining-method>
        <joining-info>
          <when>2026-11-20T10:00:00Z</when>
        </joining-info>
        <media id="1">
          <type>audio</type>
          <label>1</label>
          <src-id>305419896</src-id>
          <status>sendrecv</status>
        </media>
        <call-info>
          <sip>
            <call-id>a84b4c76e66710</call-id>
            <from-tag>1928301774</from-tag>
            <to-tag>314159</to-tag>
          </sip>
        </call-info>
      </endpoint>
    </user>
    <user entity="sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org">
      <display-text>Bob</display-text>
      <endpoint entity="sip:+12025550102@192.0.2.12:5060">
        <status>on-hold</status>
        <joining-method>dialed-out</joining-method>
        <referred>
          <by>sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org</by>
        </referred>
      </endpoint>
    </user>
    <user entity="sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org">
      <endpoint entity="sip:+12025550103@192.0.2.13:5060">
        <status>disconnected</status>
        <disconnection-method>departed</disconnection-method>
        <disconnection-info>
          <reason>Reason: SIP;cause=200;text="Call completed"</reason>
        </disconnection-info>
      </endpoint>
    </user>
  </users>
  <ex:note xmlns:ex="urn:example:not-conference-info">ignored</ex:note>
</conference-info>`

const (
	confURI = "sip:conf-7f3a@mmtel.ims.mnc001.mcc001.3gppnetwork.org"
	alice   = "sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org"
	bob     = "sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org"
	carol   = "sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org"
)

func ptr[T any](v T) *T { return &v }

var fullDocumentWant = Document{
	Entity:  confURI,
	State:   Full,
	Version: 4,
	Description: &Description{
		DisplayText:      "Ad hoc conference",
		ConfURIs:         &URIs{Entries: []URI{{URI: confURI, Purpose: Participation}}},
		MaximumUserCount: ptr(uint32(3)),
		AvailableMedia:   &AvailableMedia{Entries: []AvailableMedium{{Label: "1", Type: "audio", Status: SendRecv}}},
	},
	ConferenceState: &ConferenceState{UserCount: ptr(uint32(2)), Active: ptr(true), Locked: ptr(false)},
	Users: &Users{Users: []User{
		{Entity: alice, State: Full, DisplayText: "Alice", Endpoints: []Endpoint{{
			Entity:        "sip:+12025550101@192.0.2.11:5060",
			Status:        Connected,
			JoiningMethod: DialedIn,
			JoiningInfo:   &Execution{When: ptr(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))},
			Media:         []Media{{ID: "1", Type: "audio", Label: "1", SrcID: "305419896", Status: SendRecv}},
			CallInfo:      &CallInfo{SIP: &SIPDialogID{CallID: "a84b4c76e66710", FromTag: "1928301774", ToTag: "314159"}},
		}}},
		{Entity: bob, DisplayText: "Bob", Endpoints: []Endpoint{{
			Entity:        "sip:+12025550102@192.0.2.12:5060",
			Status:        OnHold,
			JoiningMethod: DialedOut,
			Referred:      &Execution{By: alice},
		}}},
		{Entity: carol, Endpoints: []Endpoint{{
			Entity:              "sip:+12025550103@192.0.2.13:5060",
			Status:              Disconnected,
			DisconnectionMethod: Departed,
			DisconnectionInfo:   &Execution{Reason: `Reason: SIP;cause=200;text="Call completed"`},
		}}},
	}},
}

func parse(t *testing.T, s string) Document {
	t.Helper()
	d, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseFullDocument(t *testing.T) {
	got := parse(t, fullDocument)
	if got.XMLName.Space != Namespace || got.XMLName.Local != "conference-info" {
		t.Errorf("root is %v", got.XMLName)
	}
	got.XMLName = fullDocumentWant.XMLName
	if !reflect.DeepEqual(got, fullDocumentWant) {
		t.Errorf("got  %+v\nwant %+v", got, fullDocumentWant)
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	b, err := fullDocumentWant.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("no XML declaration: %.60s", b)
	}
	if !strings.Contains(string(b), `<conference-info xmlns="urn:ietf:params:xml:ns:conference-info" entity="`+confURI+`" state="full" version="4">`) {
		t.Errorf("root element not as expected:\n%s", b)
	}
	got := parse(t, string(b))
	got.XMLName = fullDocumentWant.XMLName
	if !reflect.DeepEqual(got, fullDocumentWant) {
		t.Errorf("after a round trip\ngot  %+v\nwant %+v", got, fullDocumentWant)
	}
}

// partialDocument tells, after fullDocument, that Bob is back from hold
// and Carol is gone (RFC 4575 4.4, 4.6).
const partialDocument = `<?xml version="1.0" encoding="UTF-8"?>
<conference-info xmlns="urn:ietf:params:xml:ns:conference-info"
    entity="sip:conf-7f3a@mmtel.ims.mnc001.mcc001.3gppnetwork.org"
    state="partial" version="5">
  <users state="partial">
    <user entity="sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org" state="partial">
      <endpoint entity="sip:+12025550102@192.0.2.12:5060" state="partial">
        <status>connected</status>
      </endpoint>
    </user>
    <user entity="sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org" state="deleted"/>
  </users>
</conference-info>`

func TestParsePartialDocument(t *testing.T) {
	got := parse(t, partialDocument)
	want := Document{
		XMLName: got.XMLName,
		Entity:  confURI, State: Partial, Version: 5,
		Users: &Users{State: Partial, Users: []User{
			{Entity: bob, State: Partial, Endpoints: []Endpoint{{Entity: "sip:+12025550102@192.0.2.12:5060", State: Partial, Status: Connected}}},
			{Entity: carol, State: Deleted},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseDeletedConference(t *testing.T) {
	// The conference has ended: no description or users are needed.
	got := parse(t, `<conference-info xmlns="urn:ietf:params:xml:ns:conference-info" entity="`+confURI+`" state="deleted" version="9"/>`)
	if got.State != Deleted || got.Version != 9 {
		t.Errorf("got %+v", got)
	}
}

func TestParseRejects(t *testing.T) {
	const ns = `xmlns="urn:ietf:params:xml:ns:conference-info"`
	const body = `<conference-description/><users/>`
	tests := map[string]string{
		"no entity":  `<conference-info ` + ns + ` state="full" version="0">` + body + `</conference-info>`,
		"no version": `<conference-info ` + ns + ` entity="sip:c@example.invalid" state="full">` + body + `</conference-info>`,
		"bad state":  `<conference-info ` + ns + ` entity="sip:c@example.invalid" state="whole" version="0">` + body + `</conference-info>`,
		// An absent state is full, which needs both children.
		"full without users":       `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/></conference-info>`,
		"full without description": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><users/></conference-info>`,
		"partial user under full users": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid" state="partial"/></users></conference-info>`,
		"user without entity": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user/></users></conference-info>`,
		"same user twice": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid"/><user entity="sip:a@example.invalid"/></users></conference-info>`,
		"same endpoint twice": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid"><endpoint entity="e"/><endpoint entity="e"/></user></users></conference-info>`,
		"unknown status": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid"><endpoint entity="e"><status>talking</status></endpoint></user></users></conference-info>`,
		"unknown joining-method": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid"><endpoint entity="e"><joining-method>walked-in</joining-method></endpoint></user></users></conference-info>`,
		"media without id": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid"><endpoint entity="e"><media/></endpoint></user></users></conference-info>`,
		"unknown media status": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid"><endpoint entity="e"><media id="1"><status>muted</status></media></endpoint></user></users></conference-info>`,
		"dialog without to-tag": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description/>
			<users><user entity="sip:a@example.invalid"><endpoint entity="e"><call-info><sip><call-id>x</call-id><from-tag>f</from-tag></sip></call-info></endpoint></user></users></conference-info>`,
		"repeated media label": `<conference-info ` + ns + ` entity="sip:c@example.invalid" version="0"><conference-description>
			<available-media><entry label="1"><type>audio</type></entry><entry label="1"><type>video</type></entry></available-media>
			</conference-description><users/></conference-info>`,
		"other root namespace": `<conference-info xmlns="urn:ietf:params:xml:ns:reginfo" entity="sip:c@example.invalid" version="0">` + body + `</conference-info>`,
	}
	for name, doc := range tests {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestMarshalChecks(t *testing.T) {
	if _, err := (Document{Entity: confURI, Version: 1}).Marshal(); err == nil {
		t.Error("a full document without description and users was written")
	}
}
