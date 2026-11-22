package icscf

import (
	"errors"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TestICIDOfACallToTheCallee is TS 24.229 5.3.2.1, step 2 of "upon
// successful user location query": the S-CSCF of the callee gets the
// P-Charging-Vector that came, and a request without one, such as one
// from a peer network whose IBCF removed it, gets an icid-value of the
// I-CSCF.
func TestICIDOfACallToTheCallee(t *testing.T) {
	ue, icscf, received := setup(t, registeredAtHSS(t))
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: impu}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<sip:caller@"+domain+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("P-Charging-Vector", "icid-value=from-caller;orig-ioi=caller.example")
	send(t, ue, icscf, m)
	if pcv := (<-received).Get("P-Charging-Vector"); pcv != "icid-value=from-caller;orig-ioi=caller.example" {
		t.Errorf("with a P-Charging-Vector: the S-CSCF got %q, want it as it came", pcv)
	}

	invite(t, ue, icscf, impu)
	got, ok := sip.ChargingVectorOf(<-received)
	if !ok || got.OrigIOI != "" || got.TermIOI != "" {
		t.Errorf("without a P-Charging-Vector: the S-CSCF got %+v, want an icid-value alone", got)
	}
}

// TestIOIsOfARequestToAPSI is TS 24.229 5.3.2.1 for the AS that hosts a
// public service identity: the request goes with its icid-value, or a
// new one, and this network as its orig-ioi (step 1). The response that
// brings a term-ioi goes back with the orig-ioi of the request and this
// network as its term-ioi.
func TestIOIsOfARequestToAPSI(t *testing.T) {
	const subdomain = "conf." + domain
	conference := "sip:conf-7f3a@" + subdomain
	for _, tt := range []struct {
		name, sent string
		wantICID   string
		answer     string
	}{
		{"with a P-Charging-Vector", "icid-value=from-caller;orig-ioi=caller.example", "from-caller",
			"icid-value=from-caller;orig-ioi=caller.example;term-ioi=" + domain},
		{"without one", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			received := make(chan *sip.Message, 1)
			as := newStack(t, func(st *sip.ServerTransaction) {
				received <- st.Request
				resp := sip.NewResponse(st.Request, 200, sip.NewTag())
				got, _ := sip.ChargingVectorOf(st.Request)
				resp.Add("P-Charging-Vector", got.Response("as.example").String())
				st.Respond(resp)
			})
			locate := locatorFunc(func(host string, _ int) ([]sip.Destination, error) {
				if host != subdomain {
					return nil, errors.New("unexpected lookup of " + host)
				}
				return []sip.Destination{{Transport: sip.UDP, Addr: as.LocalAddr()}}, nil
			})
			h := hssFunc(func(*diam.Message) (*diam.Message, error) { return nil, errors.New("the HSS is not to be asked") })
			node := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
			i := New(Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf}}, Host: "icscf." + domain, Port: 5060,
				PSISubdomains: []string{subdomain}}, h, node, locate, quiet())
			icscf := newStack(t, i.OnRequest)
			ue := newStackWith(t, sip.DefaultTimers(), func(*sip.ServerTransaction) {})

			m := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: conference}
			m.Add("Max-Forwards", "68")
			m.Add("From", "<sip:+12025550151@example.net>;tag="+sip.NewTag())
			m.Add("To", "<"+conference+">")
			m.Add("Call-ID", sip.NewCallID())
			m.Add("CSeq", "1 SUBSCRIBE")
			m.Add("Event", "conference")
			m.Add("Contact", "<sip:ue@192.0.2.20>")
			if tt.sent != "" {
				m.Add("P-Charging-Vector", tt.sent)
			}
			r := send(t, ue, icscf.LocalAddr(), m)
			atAS, ok := sip.ChargingVectorOf(<-received)
			if !ok || atAS.OrigIOI != domain || atAS.TermIOI != "" || tt.wantICID != "" && atAS.ICID != tt.wantICID {
				t.Errorf("the AS got %+v, want the icid-value %q and the orig-ioi %s", atAS, tt.wantICID, domain)
			}
			want := tt.answer
			if want == "" {
				want = sip.ChargingVector{ICID: atAS.ICID, TermIOI: domain}.String()
			}
			if pcv := r.Get("P-Charging-Vector"); pcv != want {
				t.Errorf("the 200 came back with P-Charging-Vector %q, want %q", pcv, want)
			}
		})
	}
}
