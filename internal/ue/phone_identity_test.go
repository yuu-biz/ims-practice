package ue_test

import (
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// Before it registers, a phone shows the public identity it was
// configured with, unless that is a temporary public identity: the
// network bars it, and a UE shows it to nobody (TS 24.229 5.1.1.1A).
func TestPhoneShowsNoTemporaryPublicIdentityBeforeItRegisters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		temporary bool
		want      string
	}{
		{"configured identity", false, impu},
		{"temporary identity", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := listenTransport(t)
			addr := tr.LocalAddr()
			line := ue.NewLine(true)
			ua, err := sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
				SentBy:  addr.String(),
				Contact: ue.ContactURI(impu, addr),
				OnCall:  line.Ring,
				Locate: func(sip.URI) (sip.Destination, error) {
					return sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("127.0.0.1:9")}, nil
				},
			}, ue.CallConfig{}), quiet())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(ua.Close)
			u := ue.New(ue.Config{PrivateIdentity: impi, PublicIdentity: impu, TemporaryPublicIdentity: tc.temporary,
				Password: password, HomeDomain: domain}, ua.Stack(), addr, quiet())
			p := ue.NewPhone(u, ua, line, ue.PhoneConfig{MediaListen: "127.0.0.1:0"}, quiet())
			if s := p.Status(); s.Registered || s.IMPU != tc.want {
				t.Errorf("status before registration: registered %v, IMPU %q, want %q", s.Registered, s.IMPU, tc.want)
			}
		})
	}
}
