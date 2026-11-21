package confinfo

import (
	"reflect"
	"strings"
	"testing"
)

func TestFullStateHasWhatIR92AsksFor(t *testing.T) {
	c := Conference{
		URI:              confURI,
		MaximumUserCount: 3,
		Participants: []Participant{
			{User: alice, DisplayText: "Alice", Endpoint: "sip:+12025550101@192.0.2.11:5060", Status: Connected, JoiningMethod: DialedIn},
			{User: bob, DisplayText: "Bob", Endpoint: "sip:+12025550102@192.0.2.12:5060", Status: OnHold},
			{User: carol, Endpoint: "sip:+12025550103@192.0.2.13:5060", Status: Disconnected},
		},
	}
	b, err := c.FullState(0).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, want := range []string{
		`entity="` + confURI + `" state="full" version="0"`,
		`<maximum-user-count>3</maximum-user-count>`,
		`<user entity="` + alice + `">`,
		`<display-text>Alice</display-text>`,
		`<endpoint entity="sip:+12025550101@192.0.2.11:5060">`,
		`<status>connected</status>`,
		`<status>on-hold</status>`,
		`<status>disconnected</status>`,
		`<joining-method>dialed-in</joining-method>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %s:\n%s", want, doc)
		}
	}
	got := parse(t, doc)
	if n := len(got.Users.Users); n != 3 {
		t.Errorf("%d users, want 3", n)
	}
}

func TestFullStateGroupsEndpointsOfOneUser(t *testing.T) {
	c := Conference{URI: confURI, MaximumUserCount: 5, Participants: []Participant{
		{User: alice, DisplayText: "Alice", Endpoint: "sip:phone@192.0.2.11", Status: Connected},
		{User: bob, Endpoint: "sip:bob@192.0.2.12", Status: Connected},
		{User: alice, DisplayText: "Alice (tablet)", Endpoint: "sip:tablet@192.0.2.21", Status: OnHold},
	}}
	got := c.FullState(2)
	want := &Users{Users: []User{
		{Entity: alice, DisplayText: "Alice", Endpoints: []Endpoint{
			{Entity: "sip:phone@192.0.2.11", Status: Connected},
			{Entity: "sip:tablet@192.0.2.21", Status: OnHold},
		}},
		{Entity: bob, Endpoints: []Endpoint{{Entity: "sip:bob@192.0.2.12", Status: Connected}}},
	}}
	if !reflect.DeepEqual(got.Users, want) {
		t.Errorf("got  %+v\nwant %+v", got.Users, want)
	}
	if got.Version != 2 || *got.Description.MaximumUserCount != 5 {
		t.Errorf("version %d, maximum-user-count %d", got.Version, *got.Description.MaximumUserCount)
	}
}

func TestFullStateOfAnEmptyConference(t *testing.T) {
	b, err := Conference{URI: confURI, MaximumUserCount: 3}.FullState(0).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// RFC 4575 5.2: users is there even with nobody in it.
	if !strings.Contains(string(b), "<users></users>") {
		t.Errorf("no users element:\n%s", b)
	}
}

func TestNextVersion(t *testing.T) {
	if v, err := NextVersion(7); err != nil || v != 8 {
		t.Errorf("NextVersion(7) = %d, %v", v, err)
	}
	if _, err := NextVersion(^uint32(0)); err == nil {
		t.Error("the version wrapped around")
	}
}
