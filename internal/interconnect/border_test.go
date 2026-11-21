package interconnect

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// borderB is network A's border with network B: an untrusted peer that
// takes SIP URIs, PCMU and DTMF only.
func borderB(t *testing.T) Border {
	t.Helper()
	b := Border{
		Peer: Profile{
			Name:         "peer-b",
			Domain:       peerDomain,
			Transports:   []sip.Transport{sip.UDP},
			Methods:      []sip.Method{sip.MESSAGE},
			OptionTags:   map[string]OptionTagPolicy{sip.OptionPrecondition: OptionPass},
			NumberFormat: NumberFormatSIP,
			LocalNumbers: []LocalNumberPlan{{PhoneContext: "+1-202", GlobalPrefix: "+1202"}},
			Codecs:       []string{"PCMU", "telephone-event/8000"},
		},
		HomeDomain: homeDomain,
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	return b
}

// leavingInvite is exitInvite after the IBCF took its own Route off
// (TS 24.229 5.10.2.2 step 1A).
func leavingInvite(t *testing.T) *sip.Message {
	t.Helper()
	m := parse(t, exitInvite)
	m.RemoveFirst("Route")
	return m
}

// leaks lists the header fields of fields that name a node of network
// A.
func leaks(fields []sip.HeaderField) []string {
	var found []string
	for _, h := range fields {
		line := h.Name + ": " + h.Value
		for _, node := range []string{"scscf.", "pcscf.", "ibcf.ims.", "192.0.2.", "001010000000001"} {
			if strings.Contains(line, node) {
				found = append(found, line)
			}
		}
	}
	return found
}

func names(fields []sip.HeaderField) []string {
	var out []string
	for _, h := range fields {
		if !slices.Contains(out, h.Name) {
			out = append(out, h.Name)
		}
	}
	return out
}

func TestBorderValidate(t *testing.T) {
	b := borderB(t)
	b.HomeDomain = ""
	if err := b.Validate(); err == nil {
		t.Error("number format sip without the home domain was accepted")
	}
	b = borderB(t)
	b.HomeNumberFormat = "e164"
	if err := b.Validate(); err == nil {
		t.Error("an unknown home number format was accepted")
	}
	b = borderB(t)
	b.Peer.Name = ""
	if err := b.Validate(); err == nil {
		t.Error("an invalid profile was accepted")
	}
}

func TestExitRequestUntrusted(t *testing.T) {
	b := borderB(t)
	m := leavingInvite(t)
	if err := b.ExitRequest(m); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{
		"P-Asserted-Identity", "P-Access-Network-Info", "P-Charging-Vector",
		"P-Charging-Function-Addresses", "P-Early-Media", "User-Agent",
	} {
		if v := m.Get(h); v != "" {
			t.Errorf("%s: %s left network A", h, v)
		}
	}
	if !m.Requires(sip.OptionPrecondition) || !m.Supports(sip.OptionPrecondition) {
		t.Errorf("Require = %q, Supported = %q; want the caller's precondition to cross as it came", m.GetAll("Require"), m.GetAll("Supported"))
	}
	if !strings.Contains(string(m.Body), "m=audio 40000 RTP/AVP 0 101\r\n") {
		t.Errorf("SDP:\n%s", m.Body)
	}
	// What the IBCF copies into the INVITE towards the peer names no
	// node of network A; the SDP addresses go through the TrGW.
	if found := leaks(CrossingFields(m, "Allow", "Supported", "Session-Expires", "Min-SE")); len(found) > 0 {
		t.Errorf("leaves network A:\n%s", strings.Join(found, "\n"))
	}
}

func TestExitRequestTrusted(t *testing.T) {
	b := borderB(t)
	b.Peer.Trusted = true
	m := leavingInvite(t)
	if err := b.ExitRequest(m); err != nil {
		t.Fatal(err)
	}
	if got, want := m.GetAll("P-Asserted-Identity"), []string{"<sip:+12025550123@" + homeDomain + ";user=phone>"}; !slices.Equal(got, want) {
		t.Errorf("P-Asserted-Identity = %q, want %q", got, want)
	}
	for _, h := range []string{"P-Charging-Vector", "P-Access-Network-Info", "User-Agent"} {
		if m.Get(h) == "" {
			t.Errorf("%s did not reach a trusted peer", h)
		}
	}
	if m.Get("P-Charging-Function-Addresses") != "" {
		t.Error("P-Charging-Function-Addresses left network A")
	}
}

func TestBorderRejects(t *testing.T) {
	tests := []struct {
		name     string
		dir      Direction
		edit     func(*Border, *sip.Message)
		wantCode int
		wantErr  error
	}{
		{
			name: "orig from an untrusted peer",
			dir:  Entry,
			edit: func(_ *Border, m *sip.Message) {
				m.Set("Route", "<sip:ibcf.nni.mnc001.example.net;lr;orig>")
			},
			wantCode: 403, wantErr: ErrOrigFromUntrusted,
		},
		{
			name:     "a method without agreement",
			dir:      Entry,
			edit:     func(_ *Border, m *sip.Message) { m.Method = "REFER" },
			wantCode: 405, wantErr: ErrMethodNotAllowed,
		},
		{
			name: "a required extension the agreement rejects",
			dir:  Exit,
			edit: func(b *Border, _ *sip.Message) {
				b.Peer.OptionTags = map[string]OptionTagPolicy{sip.OptionPrecondition: OptionReject}
			},
			wantCode: 420, wantErr: ErrExtensionNotAllowed,
		},
		{
			name:     "a local number without a plan",
			dir:      Exit,
			edit:     func(_ *Border, m *sip.Message) { m.RequestURI = "tel:5550151;phone-context=+1-303" },
			wantCode: 404, wantErr: ErrLocalNumber,
		},
		{
			name:     "no codec in common",
			dir:      Exit,
			edit:     func(b *Border, _ *sip.Message) { b.Peer.Codecs = []string{"EVS"} },
			wantCode: 488, wantErr: ErrNoAcceptedCodec,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := borderB(t)
			m := parse(t, exitInvite)
			tt.edit(&b, m)
			err := b.Request(m, tt.dir)
			var r *Rejection
			if !errors.As(err, &r) || r.StatusCode != tt.wantCode || !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %d with %v", err, tt.wantCode, tt.wantErr)
			}
		})
	}
}

// TestBorderCall follows a call from network A to network B through
// the border: the INVITE leaves, the 200 OK comes back, and the
// callee's BYE comes in.
func TestBorderCall(t *testing.T) {
	b := borderB(t)
	invite := leavingInvite(t)
	if err := b.Request(invite, Exit); err != nil {
		t.Fatal(err)
	}
	if want := "sip:+12025550151@" + peerDomain + ";user=phone"; invite.RequestURI != want {
		t.Errorf("Request-URI = %s, want %s", invite.RequestURI, want)
	}

	ok := parse(t, entryOK)
	b.Response(ok, Entry)
	for _, h := range []string{"P-Asserted-Identity", "P-Charging-Vector", "P-Charging-Function-Addresses", "Server"} {
		if v := ok.Get(h); v != "" {
			t.Errorf("%s: %s came in from an untrusted peer", h, v)
		}
	}
	// The callee's precondition crosses; the 100rel and timer of the
	// peer's leg do not, and neither do its Contact and Record-Route.
	fields := CrossingFields(ok)
	if got := names(fields); !slices.Equal(got, []string{"Require"}) {
		t.Errorf("the 200 OK copies %q into the home network", got)
	}

	bye := parse(t, entryBye)
	if err := b.Request(bye, Entry); err != nil {
		t.Fatal(err)
	}
	if got := names(CrossingFields(bye)); !slices.Equal(got, []string{"Reason"}) {
		t.Errorf("the BYE copies %q into the home network, want its Reason only", got)
	}
}

func TestExitResponse(t *testing.T) {
	b := borderB(t)
	ok := parse(t, terminatingOK)
	ok.Add("Reason", "SIP;cause=200")
	ok.Add("Server", "ims-practice-ue")
	ok.Add("P-Asserted-Identity", "<tel:+12025550123>")
	b.Response(ok, Exit)
	for _, h := range []string{"Reason", "Server", "P-Asserted-Identity"} {
		if v := ok.Get(h); v != "" {
			t.Errorf("%s: %s left network A", h, v)
		}
	}
	if found := leaks(CrossingFields(ok)); len(found) > 0 {
		t.Errorf("the 200 OK names nodes of network A:\n%s", strings.Join(found, "\n"))
	}
}

// Header privacy (RFC 3323 5.1) needs nothing of the border: Via,
// Record-Route and Contact never cross.
func TestCrossingFieldsLeaveTheLegBehind(t *testing.T) {
	m := leavingInvite(t)
	m.Add("Privacy", "header")
	for _, h := range CrossingFields(m) {
		if isOneOf(h.Name, []string{"Via", "Record-Route", "Route", "Contact", "Call-ID", "From", "To", "CSeq", "Supported"}) {
			t.Errorf("%s crosses the border: %s", h.Name, h.Value)
		}
	}
	got := CrossingFields(m, "Supported")
	if i := slices.IndexFunc(got, func(h sip.HeaderField) bool { return h.Name == "Supported" }); i < 0 {
		t.Error("an INVITE does not carry the caller's Supported across")
	}
	if i := slices.IndexFunc(got, func(h sip.HeaderField) bool { return h.Name == "Require" }); i < 0 || got[i].Value != sip.OptionPrecondition {
		t.Errorf("Require across the border: %v", got)
	}
}

func TestEntryRequestNumbers(t *testing.T) {
	b := borderB(t)
	b.HomeNumberFormat = NumberFormatTel
	m := parse(t, exitInvite)
	m.RequestURI = "sip:+1-202-555-0123@" + homeDomain + ";user=phone"
	m.Set("Route", "<sip:ibcf.nni.mnc001.example.net;lr>")
	if err := b.EntryRequest(m); err != nil {
		t.Fatal(err)
	}
	if m.RequestURI != "tel:+12025550123" {
		t.Errorf("Request-URI = %s", m.RequestURI)
	}
}
