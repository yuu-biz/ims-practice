package interconnect

import (
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(*Profile)
		wantErr string // a part of the error, "" for none
	}{
		{name: "the sample profile", edit: func(*Profile) {}},
		{name: "every field", edit: func(p *Profile) {
			p.Trusted = true
			p.Methods = []sip.Method{sip.MESSAGE, "INFO", "REFER", sip.SUBSCRIBE, sip.NOTIFY, "PUBLISH"}
			p.OptionTags = map[string]OptionTagPolicy{"100rel": OptionPass, "precondition": OptionReject, "timer": OptionRemove, "199": OptionReject}
			p.Headers = []HeaderRule{
				{Header: "User-Agent", Action: HeaderRemove, Direction: Exit},
				{Header: "P-Charging-Vector", Param: "access-network-charging-info", Action: HeaderRemove},
				{Header: "Organization", Action: HeaderRewrite, Value: "network A"},
				{Header: "P-Charging-Vector", Param: "transit-ioi", Action: HeaderRewrite, Direction: Entry},
				{Header: "Server", Action: HeaderPass},
			}
		}},
		{name: "no name", edit: func(p *Profile) { p.Name = "" }, wantErr: "without a name"},
		{name: "no transport", edit: func(p *Profile) { p.Transports = nil }, wantErr: "no transport"},
		{name: "SCTP", edit: func(p *Profile) { p.Transports = []sip.Transport{"SCTP"} }, wantErr: `transport "SCTP"`},
		{name: "a transport twice", edit: func(p *Profile) { p.Transports = []sip.Transport{sip.UDP, sip.UDP} }, wantErr: "listed twice"},
		{name: "a mandatory method", edit: func(p *Profile) { p.Methods = []sip.Method{sip.INVITE} }, wantErr: "mandatory"},
		{name: "REGISTER", edit: func(p *Profile) { p.Methods = []sip.Method{sip.REGISTER} }, wantErr: "not applicable"},
		{name: "an option tag that is not a token", edit: func(p *Profile) { p.OptionTags = map[string]OptionTagPolicy{"pre condition": OptionPass} }, wantErr: "not a token"},
		{name: "an unknown option tag policy", edit: func(p *Profile) { p.OptionTags = map[string]OptionTagPolicy{"100rel": "drop"} }, wantErr: "unknown policy"},
		{name: "a rule for Via", edit: func(p *Profile) { p.Headers = []HeaderRule{{Header: "via", Action: HeaderRemove}} }, wantErr: "not open to rules"},
		{name: "a rule for WWW-Authenticate", edit: func(p *Profile) { p.Headers = []HeaderRule{{Header: "WWW-Authenticate", Action: HeaderRemove}} }, wantErr: "not open to rules"},
		{name: "a rewrite without a value", edit: func(p *Profile) { p.Headers = []HeaderRule{{Header: "Organization", Action: HeaderRewrite}} }, wantErr: "needs a value"},
		{name: "an unknown action", edit: func(p *Profile) { p.Headers = []HeaderRule{{Header: "Organization", Action: "drop"}} }, wantErr: "unknown action"},
		{name: "an unknown direction", edit: func(p *Profile) {
			p.Headers = []HeaderRule{{Header: "Organization", Action: HeaderRemove, Direction: "both"}}
		}, wantErr: "unknown direction"},
		{name: "a parameter that is not a token", edit: func(p *Profile) {
			p.Headers = []HeaderRule{{Header: "P-Charging-Vector", Param: "a b", Action: HeaderRemove}}
		}, wantErr: "not a token"},
		{name: "pass for a parameter", edit: func(p *Profile) {
			p.Headers = []HeaderRule{{Header: "Server", Param: "x", Action: HeaderPass}}
		}, wantErr: "whole header field"},
		{name: "pass outside the trust domain", edit: func(p *Profile) {
			p.Headers = []HeaderRule{{Header: "Organization", Action: HeaderPass}}
		}, wantErr: "trust domain"},
		{name: "pass for P-Asserted-Identity", edit: func(p *Profile) {
			p.Headers = []HeaderRule{{Header: "P-Asserted-Identity", Action: HeaderPass}}
		}, wantErr: "TS 24.229 4.4.2"},
		{name: "pass for P-Charging-Vector, which entry always removes", edit: func(p *Profile) {
			p.Headers = []HeaderRule{{Header: "P-Charging-Vector", Action: HeaderPass}}
		}, wantErr: "5.10.3.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := peerProfile()
			tt.edit(&p)
			err := p.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Validate() = %v, want an error with %q", err, tt.wantErr)
			}
		})
	}
}

func TestTransports(t *testing.T) {
	p := Profile{Transports: []sip.Transport{sip.TCP, sip.UDP}}
	if got := p.PreferredTransport(); got != sip.TCP {
		t.Errorf("PreferredTransport() = %s, want TCP", got)
	}
	if !p.AcceptsTransport(sip.UDP) {
		t.Error("AcceptsTransport(UDP) = false")
	}
	p.Transports = []sip.Transport{sip.TCP}
	if p.AcceptsTransport(sip.UDP) {
		t.Error("AcceptsTransport(UDP) = true for a TCP only peer")
	}
}
