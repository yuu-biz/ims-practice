package sip

import (
	"reflect"
	"testing"
)

func mustURI(t *testing.T, s string) URI {
	t.Helper()
	u, err := ParseURI(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPopOwnRoute(t *testing.T) {
	scscf := mustURI(t, "sip:scscf."+imsDomain+":6060")
	tests := []struct {
		name     string
		routes   []string
		popped   string // "" when nothing is removed
		leftOver []string
	}{
		{
			name:     "own URI with a user part and lr",
			routes:   []string{"<sip:orig@SCSCF." + imsDomain + ":6060;lr>", "<sip:as." + imsDomain + ";lr>"},
			popped:   "sip:orig@SCSCF." + imsDomain + ":6060;lr",
			leftOver: []string{"<sip:as." + imsDomain + ";lr>"},
		},
		{
			name:     "another port",
			routes:   []string{"<sip:scscf." + imsDomain + ";lr>"},
			leftOver: []string{"<sip:scscf." + imsDomain + ";lr>"},
		},
		{
			name:     "another host",
			routes:   []string{"<sip:pcscf." + imsDomain + ":6060;lr>"},
			leftOver: []string{"<sip:pcscf." + imsDomain + ":6060;lr>"},
		},
		{name: "no Route"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := inviteRequest()
			for _, r := range tt.routes {
				req.Add("Route", r)
			}
			popped, ok := PopOwnRoute(req, mustURI(t, "sip:pcscf."+imsDomain), scscf)
			if ok != (tt.popped != "") || (ok && popped.String() != tt.popped) {
				t.Errorf("popped %v, %v; want %q", popped, ok, tt.popped)
			}
			if got := req.GetAll("Route"); !reflect.DeepEqual(got, tt.leftOver) {
				t.Errorf("Route left = %q, want %q", got, tt.leftOver)
			}
		})
	}
}

func TestPopOwnRouteDefaultsThePort(t *testing.T) {
	req := inviteRequest()
	req.Add("Route", "<sip:icscf."+imsDomain+":5060;lr>")
	if _, ok := PopOwnRoute(req, mustURI(t, "sip:icscf."+imsDomain)); !ok {
		t.Error("port 5060 and no port should name the same proxy")
	}
}

func TestNextHop(t *testing.T) {
	req := inviteRequest()
	got, err := NextHop(req)
	if err != nil || got.String() != req.RequestURI {
		t.Errorf("without Route: %v, %v; want the Request-URI %s", got, err, req.RequestURI)
	}

	req.Add("Route", "<sip:orig@scscf."+imsDomain+":6060;lr>, <sip:as."+imsDomain+";lr>")
	got, err = NextHop(req)
	if err != nil || got.Host != "scscf."+imsDomain || got.Port != 6060 {
		t.Errorf("with Route: %v, %v; want the top Route", got, err)
	}

	strict := inviteRequest()
	strict.Add("Route", "<sip:proxy."+imsDomain+">")
	if _, err := NextHop(strict); err == nil {
		t.Error("a Route without lr gave no error")
	}

	tel := inviteRequest()
	tel.RequestURI = "tel:+0010000000002"
	if _, err := NextHop(tel); err == nil {
		t.Error("a tel Request-URI without Route gave no error")
	}
}

func TestAddRecordRoute(t *testing.T) {
	req := inviteRequest()
	req.Add("Record-Route", "<sip:scscf."+imsDomain+":6060;lr>")
	own := mustURI(t, "sip:pcscf."+imsDomain+":5060")
	AddRecordRoute(req, own)
	want := []string{"<sip:pcscf." + imsDomain + ":5060;lr>", "<sip:scscf." + imsDomain + ":6060;lr>"}
	if got := req.GetAll("Record-Route"); !reflect.DeepEqual(got, want) {
		t.Errorf("Record-Route = %q, want %q", got, want)
	}
	if _, lr := own.Params.Get("lr"); lr {
		t.Error("AddRecordRoute changed the caller's URI")
	}

	// A URI that already has lr keeps a single one.
	req = inviteRequest()
	AddRecordRoute(req, mustURI(t, "sip:pcscf."+imsDomain+";lr"))
	if got := req.Get("Record-Route"); got != "<sip:pcscf."+imsDomain+";lr>" {
		t.Errorf("Record-Route = %q", got)
	}
}
