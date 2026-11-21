package interconnect

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

func TestScreenMethod(t *testing.T) {
	tests := []struct {
		method  sip.Method
		agreed  []sip.Method
		wantErr bool
	}{
		{method: sip.INVITE},
		{method: sip.ACK},
		{method: sip.CANCEL},
		{method: sip.PRACK},
		{method: sip.UPDATE},
		{method: sip.OPTIONS},
		{method: sip.BYE},
		{method: sip.MESSAGE, wantErr: true},
		{method: sip.MESSAGE, agreed: []sip.Method{sip.MESSAGE}},
		{method: sip.SUBSCRIBE, agreed: []sip.Method{sip.MESSAGE}, wantErr: true},
		{method: sip.REGISTER, agreed: []sip.Method{sip.MESSAGE}, wantErr: true},
		{method: "FOO", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			req := &sip.Message{Method: tt.method, RequestURI: "sip:+12025550151@" + peerDomain + ";user=phone"}
			err := ScreenMethod(req, Profile{Methods: tt.agreed})
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("ScreenMethod() = %v, want nil", err)
				}
				return
			}
			var r *Rejection
			if !errors.As(err, &r) || r.StatusCode != 405 || !errors.Is(err, ErrMethodNotAllowed) {
				t.Fatalf("ScreenMethod() = %v, want a 405 rejection", err)
			}
			want := "ACK, BYE, CANCEL, INVITE, OPTIONS, PRACK, UPDATE"
			if len(tt.agreed) > 0 {
				want += ", MESSAGE"
			}
			if len(r.Headers) != 1 || r.Headers[0] != (sip.HeaderField{Name: "Allow", Value: want}) {
				t.Errorf("headers = %v, want Allow: %s", r.Headers, want)
			}
		})
	}
}

func TestApplyOptionTags(t *testing.T) {
	tests := []struct {
		name          string
		policy        OptionTagPolicy
		wantSupported []string
		wantRequire   []string
		wantErr       bool
	}{
		{name: "pass", policy: OptionPass, wantSupported: []string{"100rel", "precondition", "timer"}, wantRequire: []string{"precondition"}},
		{name: "remove", policy: OptionRemove, wantSupported: []string{"100rel", "precondition"}, wantRequire: []string{"precondition"}},
		{name: "reject", policy: OptionReject, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := parse(t, exitInvite)
			tag := sip.OptionPrecondition
			if tt.policy == OptionRemove {
				// A profile cannot remove precondition (Validate).
				tag = sip.OptionTimer
			}
			p := Profile{OptionTags: map[string]OptionTagPolicy{tag: tt.policy}}
			err := ApplyOptionTags(req, p)
			if tt.wantErr {
				var r *Rejection
				if !errors.As(err, &r) || r.StatusCode != 420 || !slices.Contains(r.Headers, sip.HeaderField{Name: "Unsupported", Value: "precondition"}) {
					t.Fatalf("ApplyOptionTags() = %v, want 420 with Unsupported: precondition", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := req.GetAll("Supported"); !slices.Equal(got, tt.wantSupported) {
				t.Errorf("Supported = %q, want %q", got, tt.wantSupported)
			}
			if got := req.GetAll("Require"); !slices.Equal(got, tt.wantRequire) {
				t.Errorf("Require = %q, want %q", got, tt.wantRequire)
			}
		})
	}
}

func TestApplyOptionTagsRemovesProxyRequire(t *testing.T) {
	req := parse(t, "OPTIONS sip:"+peerDomain+" SIP/2.0\r\nProxy-Require: sec-agree, foo\r\nSupported: timer, foo\r\n\r\n")
	if err := ApplyOptionTags(req, Profile{OptionTags: map[string]OptionTagPolicy{"foo": OptionRemove}}); err != nil {
		t.Fatal(err)
	}
	if got := req.GetAll("Proxy-Require"); !slices.Equal(got, []string{"sec-agree"}) {
		t.Errorf("Proxy-Require = %q", got)
	}
	if got := req.GetAll("Supported"); !slices.Equal(got, []string{"timer"}) {
		t.Errorf("Supported = %q", got)
	}
}

// A tag that the request only supports crosses as it came: the far end
// answers without the extension when it lacks it (RFC 3312 11).
func TestApplyOptionTagsRejectOnlySupported(t *testing.T) {
	req := parse(t, exitInvite)
	req.Remove("Require")
	p := Profile{OptionTags: map[string]OptionTagPolicy{sip.OptionPrecondition: OptionReject}}
	if err := ApplyOptionTags(req, p); err != nil {
		t.Fatalf("ApplyOptionTags() = %v; a tag only in Supported is not rejected", err)
	}
	if !req.Supports(sip.OptionPrecondition) {
		t.Error("precondition went from Supported")
	}
}

func TestRemoveNotApplicable(t *testing.T) {
	m := parse(t, exitInvite)
	m.Add("P-Served-User", "<sip:+12025550123@"+homeDomain+";user=phone>;sescase=orig;regstate=reg")
	m.Add("P-Preferred-Identity", "<tel:+12025550123>")
	m.Add("Security-Verify", "ipsec-3gpp;alg=hmac-sha-1-96;spi-c=1;spi-s=2;port-c=5062;port-s=5064")
	RemoveNotApplicable(m)
	for _, name := range []string{"P-Served-User", "P-Preferred-Identity", "Security-Verify", "P-Charging-Function-Addresses"} {
		if v := m.Get(name); v != "" {
			t.Errorf("%s: %s is still there", name, v)
		}
	}
	if m.Get("P-Charging-Vector") == "" {
		t.Error("P-Charging-Vector went too")
	}
}

func TestApplyHeaderRules(t *testing.T) {
	tests := []struct {
		name   string
		rule   HeaderRule
		dir    Direction
		header string
		want   []string
	}{
		{
			name:   "remove a header field",
			rule:   HeaderRule{Header: "user-agent", Action: HeaderRemove},
			dir:    Exit,
			header: "User-Agent",
		},
		{
			name:   "a rule for the other direction",
			rule:   HeaderRule{Header: "User-Agent", Action: HeaderRemove, Direction: Entry},
			dir:    Exit,
			header: "User-Agent",
			want:   []string{"ims-practice-ue"},
		},
		{
			name:   "rewrite a header field",
			rule:   HeaderRule{Header: "User-Agent", Action: HeaderRewrite, Value: "IMS-A"},
			dir:    Exit,
			header: "User-Agent",
			want:   []string{"IMS-A"},
		},
		{
			name:   "rewrite does not add a header field",
			rule:   HeaderRule{Header: "Organization", Action: HeaderRewrite, Value: "network A"},
			dir:    Exit,
			header: "Organization",
		},
		{
			name:   "remove a parameter of a value without a base",
			rule:   HeaderRule{Header: "P-Charging-Vector", Param: "icid-generated-at", Action: HeaderRemove},
			dir:    Exit,
			header: "P-Charging-Vector",
			want:   []string{"icid-value=1234bc9876e;orig-ioi=ims.mnc001.mcc001.3gppnetwork.org"},
		},
		{
			name:   "set a parameter",
			rule:   HeaderRule{Header: "P-Charging-Vector", Param: "transit-ioi", Action: HeaderRewrite, Value: "nni.example.net"},
			dir:    Exit,
			header: "P-Charging-Vector",
			want:   []string{"icid-value=1234bc9876e;icid-generated-at=192.0.2.20;orig-ioi=ims.mnc001.mcc001.3gppnetwork.org;transit-ioi=nni.example.net"},
		},
		{
			name:   "set a parameter on each value of a list header field",
			rule:   HeaderRule{Header: "P-Asserted-Identity", Param: "x", Action: HeaderRewrite, Value: "1"},
			dir:    Exit,
			header: "P-Asserted-Identity",
			want: []string{
				"<sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>;x=1",
				"<tel:+12025550123>;x=1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := parse(t, exitInvite)
			before := headerNames(m)
			ApplyHeaderRules(m, Profile{Headers: []HeaderRule{tt.rule}}, tt.dir)
			if got := m.GetAll(tt.header); !slices.Equal(got, tt.want) {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
			}
			if len(tt.want) > 0 && headerNames(m) != before {
				t.Errorf("order of the header fields changed:\n%s\n%s", before, headerNames(m))
			}
		})
	}
}

func TestSplitParams(t *testing.T) {
	tests := []struct {
		value    string
		wantBase string
		want     sip.Params
	}{
		{value: "<sip:a@example.net;lr>;tag=1", wantBase: "<sip:a@example.net;lr>", want: sip.Params{{Name: "tag", Value: "1"}}},
		{value: `"A;=B" <sip:a@example.net>`, wantBase: `"A;=B" <sip:a@example.net>`},
		{value: "icid-value=1;orig-ioi=a", want: sip.Params{{Name: "icid-value", Value: "1"}, {Name: "orig-ioi", Value: "a"}}},
		{value: "supported", wantBase: "supported"},
		{value: "SIP;cause=200", wantBase: "SIP", want: sip.Params{{Name: "cause", Value: "200"}}},
	}
	for _, tt := range tests {
		base, params := splitParams(tt.value)
		if base != tt.wantBase || !slices.Equal(params, tt.want) {
			t.Errorf("splitParams(%q) = %q, %v; want %q, %v", tt.value, base, params, tt.wantBase, tt.want)
		}
	}
}

func TestReplaceValuesKeepsPlace(t *testing.T) {
	m := parse(t, "OPTIONS sip:a@example.net SIP/2.0\r\nX-A: 1\r\nVia: v1, v2\r\nX-B: 2\r\nVia: v3\r\nX-C: 3\r\n\r\n")
	replaceValues(m, "via", []string{"x", "y"})
	var rows []string
	for _, h := range m.Headers {
		rows = append(rows, h.Name+"="+h.Value)
	}
	if got := strings.Join(rows, " "); got != "X-A=1 Via=x Via=y X-B=2 X-C=3" {
		t.Errorf("rows = %s", got)
	}
}

func TestRejectionResponse(t *testing.T) {
	req := parse(t, exitInvite)
	err := ScreenMethod(&sip.Message{Method: sip.MESSAGE}, Profile{})
	var r *Rejection
	if !errors.As(err, &r) {
		t.Fatal(err)
	}
	resp := r.Response(req, "t1")
	if resp.StatusCode != 405 || resp.Reason != "Method Not Allowed" || resp.Get("Allow") == "" || resp.Get("Call-ID") != req.Get("Call-ID") {
		t.Errorf("response:\n%s", resp)
	}
	if !strings.Contains(r.Error(), "405 Method Not Allowed") {
		t.Errorf("Error() = %q", r.Error())
	}
}
