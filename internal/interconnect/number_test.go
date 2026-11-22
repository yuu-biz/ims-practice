package interconnect

import (
	"errors"
	"slices"
	"testing"
)

func numberProfile() Profile {
	p := peerProfile()
	p.LocalNumbers = []LocalNumberPlan{
		{PhoneContext: "+1-202", GlobalPrefix: "+1202"},
		{PhoneContext: "ims.mnc001.mcc001.3gppnetwork.org", GlobalPrefix: "+1202"},
	}
	return p
}

func TestNormalizeNumber(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		format     NumberFormat
		allowLocal bool
		want       string
		wantErr    error
	}{
		{name: "tel to SIP", uri: "tel:+12025550151", format: NumberFormatSIP, want: "sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone"},
		{name: "SIP to tel", uri: "sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone", format: NumberFormatTel, want: "tel:+12025550151"},
		{name: "SIP stays SIP with its host", uri: "sip:+12025550151@other.example.net;user=phone", format: NumberFormatSIP, want: "sip:+12025550151@other.example.net;user=phone"},
		{name: "visual separators go", uri: "tel:+1-202-555-0151", format: NumberFormatTel, want: "tel:+12025550151"},
		{name: "number portability parameters stay", uri: "tel:+12025550151;npdi;rn=+12025550100", format: NumberFormatSIP, want: "sip:+12025550151;npdi;rn=+12025550100@ims.mnc002.mcc001.3gppnetwork.org;user=phone"},
		{name: "and come back", uri: "sip:+12025550151;npdi;rn=+12025550100@ims.mnc002.mcc001.3gppnetwork.org;user=phone", format: NumberFormatTel, want: "tel:+12025550151;npdi;rn=+12025550100"},
		{name: "a local number with a number context", uri: "tel:555-0151;phone-context=+1-202", format: NumberFormatTel, want: "tel:+12025550151"},
		{name: "a local number with a domain context", uri: "sip:5550151;phone-context=ims.mnc001.mcc001.3gppnetwork.org@ims.mnc001.mcc001.3gppnetwork.org;user=phone", want: "sip:+12025550151@ims.mnc001.mcc001.3gppnetwork.org;user=phone"},
		{name: "a local number of an unknown context", uri: "tel:5550151;phone-context=+1-303", format: NumberFormatSIP, wantErr: ErrLocalNumber},
		{name: "a local number the agreement allows", uri: "tel:5550151;phone-context=+1-303", format: NumberFormatSIP, allowLocal: true, want: "sip:5550151;phone-context=+1-303@ims.mnc002.mcc001.3gppnetwork.org;user=phone"},
		{name: "the empty format keeps tel", uri: "tel:+1-202-555-0151", want: "tel:+12025550151"},
		{name: "a SIP URI without user=phone is no number", uri: "sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org", format: NumberFormatTel, want: "sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org"},
		// A URI with no telephone-subscriber holds no number to put in a
		// format, whatever user=phone says, so it crosses as it came
		// instead of becoming a local number of an empty number plan.
		{name: "a SIP URI with no user part is no number", uri: "sip:" + peerDomain + ";user=phone", format: NumberFormatTel, want: "sip:" + peerDomain + ";user=phone"},
		{name: "a SIP URI with an empty user part is no number", uri: "sip:@" + peerDomain + ";user=phone", format: NumberFormatTel, want: "sip:@" + peerDomain + ";user=phone"},
		{name: "a tel URI with parameters only is no number", uri: "tel:;npdi", format: NumberFormatTel, want: "tel:;npdi"},
		{name: "a user name", uri: "sip:alice@example.net", format: NumberFormatTel, want: "sip:alice@example.net"},
		{name: "a URN", uri: "urn:service:sos", format: NumberFormatTel, want: "urn:service:sos"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := numberProfile()
			p.AllowLocalNumbers = tt.allowLocal
			got, err := NormalizeNumber(tt.uri, tt.format, peerDomain, p)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NormalizeNumber(%s) = %q, %v; want %v", tt.uri, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("NormalizeNumber(%s) = %q, %v; want %q", tt.uri, got, err, tt.want)
			}
		})
	}
}

func TestNormalizeNumberNeedsHost(t *testing.T) {
	if _, err := NormalizeNumber("tel:+12025550151", NumberFormatSIP, "", numberProfile()); err == nil {
		t.Error("a tel URI became a SIP URI without a host")
	}
}

func TestNormalizeNumbers(t *testing.T) {
	m := parse(t, exitInvite)
	m.RequestURI = "tel:+1-202-555-0151"
	if err := NormalizeNumbers(m, numberProfile(), NumberFormatSIP, peerDomain, homeDomain); err != nil {
		t.Fatal(err)
	}
	if want := "sip:+12025550151@" + peerDomain + ";user=phone"; m.RequestURI != want {
		t.Errorf("Request-URI = %s, want %s", m.RequestURI, want)
	}
	// The tel URI of P-Asserted-Identity turns into the SIP URI that is
	// there already.
	want := []string{"<sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>"}
	if got := m.GetAll("P-Asserted-Identity"); !slices.Equal(got, want) {
		t.Errorf("P-Asserted-Identity = %q, want %q", got, want)
	}
}

func TestNormalizeNumbersRejects(t *testing.T) {
	m := parse(t, exitInvite)
	m.RequestURI = "tel:5550151;phone-context=+1-303"
	err := NormalizeNumbers(m, numberProfile(), NumberFormatSIP, peerDomain, homeDomain)
	var r *Rejection
	if !errors.As(err, &r) || r.StatusCode != 404 || !errors.Is(err, ErrLocalNumber) {
		t.Fatalf("NormalizeNumbers() = %v, want 404", err)
	}
}

func TestNormalizeNumbersKeepsLocalIdentity(t *testing.T) {
	m := parse(t, entryOK)
	m.Set("P-Asserted-Identity", `"Desk" <tel:5550188;phone-context=+1-303>`)
	if err := NormalizeNumbers(m, numberProfile(), NumberFormatSIP, homeDomain, peerDomain); err != nil {
		t.Fatal(err)
	}
	if got := m.Get("P-Asserted-Identity"); got != `"Desk" <tel:5550188;phone-context=+1-303>` {
		t.Errorf("P-Asserted-Identity = %s", got)
	}
}

func TestValidateNumbers(t *testing.T) {
	p := numberProfile()
	p.NumberFormat = NumberFormatSIP
	if err := p.Validate(); err == nil {
		t.Error("number format sip without the peer's domain was accepted")
	}
	p.Domain = peerDomain
	if err := p.Validate(); err != nil {
		t.Error(err)
	}
	p.NumberFormat = "e164"
	if err := p.Validate(); err == nil {
		t.Error("an unknown number format was accepted")
	}
	p.NumberFormat = NumberFormatTel
	p.LocalNumbers = []LocalNumberPlan{{PhoneContext: "+1-202", GlobalPrefix: "1202"}}
	if err := p.Validate(); err == nil {
		t.Error("a global prefix without + was accepted")
	}
	p.LocalNumbers = []LocalNumberPlan{{GlobalPrefix: "+1202"}}
	if err := p.Validate(); err == nil {
		t.Error("a plan without a phone-context was accepted")
	}
}
