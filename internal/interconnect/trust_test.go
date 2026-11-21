package interconnect

import (
	"errors"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

func TestApplyTrustDomainTrusted(t *testing.T) {
	m := parse(t, exitInvite)
	before := m.String()
	p := peerProfile()
	p.Trusted = true
	ApplyTrustDomain(m, p, Exit)
	if m.String() != before {
		t.Errorf("a trusted peer changed the message:\n%s", m)
	}
}

func TestApplyTrustDomainUntrusted(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		dir      Direction
		rules    []HeaderRule
		extra    []sip.HeaderField
		gone     []string
		kept     []string
		wantPrio []string
	}{
		{
			name:    "an INVITE leaves",
			message: exitInvite,
			dir:     Exit,
			extra: []sip.HeaderField{
				{Name: "Feature-Caps", Value: "*;+g.3gpp.srvcc"},
				{Name: "Priority", Value: "psap-callback"},
				{Name: "Priority", Value: "urgent"},
				{Name: "Cellular-Network-Info", Value: "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000001"},
			},
			gone:     []string{"P-Asserted-Identity", "P-Access-Network-Info", "P-Early-Media", "P-Charging-Vector", "User-Agent", "Cellular-Network-Info"},
			kept:     []string{"Feature-Caps", "Privacy", "Session-Expires", "From"},
			wantPrio: []string{"urgent"},
		},
		{
			name:    "a pass rule keeps User-Agent and P-Charging-Vector at exit",
			message: exitInvite,
			dir:     Exit,
			rules:   []HeaderRule{{Header: "User-Agent", Action: HeaderPass}, {Header: "p-charging-vector", Action: HeaderPass}},
			gone:    []string{"P-Asserted-Identity"},
			kept:    []string{"User-Agent", "P-Charging-Vector"},
		},
		{
			name:    "an INVITE enters",
			message: exitInvite,
			dir:     Entry,
			extra:   []sip.HeaderField{{Name: "Feature-Caps", Value: "*;+g.3gpp.srvcc"}},
			gone:    []string{"P-Asserted-Identity", "P-Charging-Vector", "Feature-Caps"},
		},
		{
			name:    "a 200 OK enters with a Reason",
			message: entryOK,
			dir:     Entry,
			extra:   []sip.HeaderField{{Name: "Reason", Value: "SIP;cause=200"}},
			gone:    []string{"P-Asserted-Identity", "P-Charging-Vector", "Server"},
			kept:    []string{"Reason"},
		},
		{
			name:    "a 200 OK leaves with a Reason",
			message: entryOK,
			dir:     Exit,
			extra:   []sip.HeaderField{{Name: "Reason", Value: "SIP;cause=200"}},
			gone:    []string{"P-Asserted-Identity", "Reason", "Server"},
		},
		{
			name:    "a BYE keeps its Reason",
			message: entryBye,
			dir:     Exit,
			kept:    []string{"Reason"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parse(t, tt.message)
			m.Headers = append(m.Headers, tt.extra...)
			m.Add("Privacy", "id")
			p := peerProfile()
			p.Headers = tt.rules
			ApplyTrustDomain(m, p, tt.dir)
			for _, h := range tt.gone {
				if v := m.Get(h); v != "" {
					t.Errorf("%s: %s is still there", h, v)
				}
			}
			for _, h := range tt.kept {
				if m.Get(h) == "" {
					t.Errorf("%s is gone", h)
				}
			}
			if got := m.GetAll("Priority"); !slices.Equal(got, tt.wantPrio) {
				t.Errorf("Priority = %q, want %q", got, tt.wantPrio)
			}
		})
	}
}

func TestApplyTrustDomainIotl(t *testing.T) {
	m := parse(t, exitInvite)
	m.RequestURI = "sip:+12025550151@" + peerDomain + ";user=phone;iotl=homea-homeb"
	m.Set("Route", "<sip:ibcf.nni.mnc002.example.net;lr;iotl=homea-homeb>")
	ApplyTrustDomain(m, peerProfile(), Exit)
	if m.RequestURI != "sip:+12025550151@"+peerDomain+";user=phone" {
		t.Errorf("Request-URI = %s", m.RequestURI)
	}
	if got := m.Get("Route"); got != "<sip:ibcf.nni.mnc002.example.net;lr>" {
		t.Errorf("Route = %s", got)
	}
}

func TestCheckOrig(t *testing.T) {
	tests := []struct {
		name    string
		route   string
		trusted bool
		inDlg   bool
		wantErr bool
	}{
		{name: "orig from an untrusted peer", route: "<sip:ibcf.nni.mnc001.example.net;lr;orig>", wantErr: true},
		{name: "orig from a trusted peer", route: "<sip:ibcf.nni.mnc001.example.net;lr;orig>", trusted: true},
		{name: "no orig", route: "<sip:ibcf.nni.mnc001.example.net;lr>"},
		{name: "no Route"},
		{name: "within a dialog", route: "<sip:ibcf.nni.mnc001.example.net;lr;orig>", inDlg: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parse(t, exitInvite)
			m.Remove("Route")
			if tt.route != "" {
				m.Add("Route", tt.route)
			}
			if tt.inDlg {
				m.Set("To", "<tel:+12025550151>;tag=x1")
			}
			p := peerProfile()
			p.Trusted = tt.trusted
			err := CheckOrig(m, p)
			var r *Rejection
			if tt.wantErr != (errors.As(err, &r) && r.StatusCode == 403 && errors.Is(err, ErrOrigFromUntrusted)) {
				t.Errorf("CheckOrig() = %v", err)
			}
		})
	}
}

func TestRemoveReceivedRealm(t *testing.T) {
	m := parse(t, exitInvite)
	m.SetFirst("Via", "SIP/2.0/UDP scscf.ims.mnc001.mcc001.3gppnetwork.org:6060;branch=z9hG4bKs1c2;received-realm=nni.example.net")
	RemoveReceivedRealm(m)
	if got := m.Get("Via"); got != "SIP/2.0/UDP scscf.ims.mnc001.mcc001.3gppnetwork.org:6060;branch=z9hG4bKs1c2" {
		t.Errorf("top Via = %s", got)
	}
	if n := len(m.GetAll("Via")); n != 3 {
		t.Errorf("%d Via values, want 3", n)
	}
}

func TestApplyPrivacy(t *testing.T) {
	const history = "<sip:+12025550151@" + peerDomain + ";user=phone>;index=1, " +
		"<sip:+12025550188@" + homeDomain + ";user=phone?Reason=SIP%3Bcause%3D302>;index=1.1;rc=1"
	tests := []struct {
		name        string
		privacy     string
		trusted     bool
		wantFrom    string
		wantHistory []string
		wantPrivacy string
	}{
		{
			name:        "user",
			privacy:     "user;id",
			wantFrom:    `"Anonymous" <sip:anonymous@anonymous.invalid>;tag=a73kszlfl`,
			wantHistory: []string{"<sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone>;index=1", "<sip:+12025550188@ims.mnc001.mcc001.3gppnetwork.org;user=phone?Reason=SIP%3Bcause%3D302>;index=1.1;rc=1"},
			wantPrivacy: "user;id",
		},
		{
			name:        "header",
			privacy:     "header",
			wantHistory: []string{"<sip:anonymous@anonymous.invalid>;index=1", "<sip:anonymous@anonymous.invalid?Reason=SIP%3Bcause%3D302>;index=1.1;rc=1"},
			wantPrivacy: "header",
		},
		{
			name:        "history goes once it is applied",
			privacy:     "history",
			wantHistory: []string{"<sip:anonymous@anonymous.invalid>;index=1", "<sip:anonymous@anonymous.invalid?Reason=SIP%3Bcause%3D302>;index=1.1;rc=1"},
		},
		{
			name:        "none",
			privacy:     "none",
			wantHistory: []string{"<sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone>;index=1", "<sip:+12025550188@ims.mnc001.mcc001.3gppnetwork.org;user=phone?Reason=SIP%3Bcause%3D302>;index=1.1;rc=1"},
			wantPrivacy: "none",
		},
		{
			name:        "a trusted peer",
			privacy:     "header;user",
			trusted:     true,
			wantHistory: []string{"<sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone>;index=1", "<sip:+12025550188@ims.mnc001.mcc001.3gppnetwork.org;user=phone?Reason=SIP%3Bcause%3D302>;index=1.1;rc=1"},
			wantPrivacy: "header;user",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parse(t, exitInvite)
			from := m.Get("From")
			m.Add("Privacy", tt.privacy)
			m.Add("History-Info", history)
			p := peerProfile()
			p.Trusted = tt.trusted
			ApplyPrivacy(m, p)
			if tt.wantFrom == "" {
				tt.wantFrom = from
			}
			if got := m.Get("From"); got != tt.wantFrom {
				t.Errorf("From = %s, want %s", got, tt.wantFrom)
			}
			if got := m.GetAll("History-Info"); !slices.Equal(got, tt.wantHistory) {
				t.Errorf("History-Info = %q\nwant %q", got, tt.wantHistory)
			}
			if got := m.Get("Privacy"); got != tt.wantPrivacy {
				t.Errorf("Privacy = %q, want %q", got, tt.wantPrivacy)
			}
		})
	}
}

func TestApplyPrivacyHistoryOfOneEntry(t *testing.T) {
	m := parse(t, exitInvite)
	m.Add("History-Info", "<sip:+12025550151@"+peerDomain+";user=phone>;index=1")
	m.Add("History-Info", "\"Desk\" <sip:+12025550188@"+homeDomain+";user=phone?Privacy=history>;index=1.1")
	ApplyPrivacy(m, peerProfile())
	want := []string{
		"<sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone>;index=1",
		"<sip:anonymous@anonymous.invalid>;index=1.1",
	}
	if got := m.GetAll("History-Info"); !slices.Equal(got, want) {
		t.Errorf("History-Info = %q\nwant %q", got, want)
	}
}
