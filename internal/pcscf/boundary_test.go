package pcscf

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file send a message with header fields of every
// kind across the edge between the UE and the trust domain, and look at
// what the other side gets.

// borderFields are what the sender of a message across the edge puts in.
var borderFields = []sip.HeaderField{
	{Name: "P-Charging-Vector", Value: "icid-value=from-sender;orig-ioi=sender"},
	{Name: "P-Charging-Function-Addresses", Value: "ccf=192.0.2.1"},
	{Name: "P-Media-Authorization", Value: "0020000100100101706466312e6578616d706c652e636f6d0000"},
	{Name: "P-Access-Network-Info", Value: "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101"},
	{Name: "P-Access-Network-Info", Value: "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000102;network-provided"},
	{Name: "Feature-Caps", Value: "*;+g.3gpp.icsi-ref=\"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel\""},
	{Name: "Geolocation", Value: "<cid:target@192.0.2.10>;inserted-by=192.0.2.10;loc-src=192.0.2.10"},
	{Name: "P-Asserted-Identity", Value: "<sip:forged@" + domain + ">"},
	{Name: "P-Preferred-Identity", Value: "<sip:forged@" + domain + ">"},
	{Name: "P-Served-User", Value: "<sip:forged@" + domain + ">;sescase=orig;regstate=reg"},
	{Name: "P-Profile-Key", Value: "<sip:chatroom-!.*!@" + domain + ">"},
	{Name: "Resource-Priority", Value: "wps.0"},
	{Name: "Relayed-Charge", Value: "transit"},
	{Name: "Service-Interact-Info", Value: "<urn:3gpp:example>"},
	{Name: "P-Preferred-Service", Value: "urn:urn-7:3gpp-service.ims.icsi.mmtel"},
	{Name: "Subject", Value: "not a field of the trust domain"},
}

// requestOnlyFields are header fields of the trust domain that only a
// request carries (RFC 6050 5.2, TS 24.229 4.4.10, 4.4.19).
var requestOnlyFields = []sip.HeaderField{
	{Name: "P-Asserted-Service", Value: "urn:urn-7:3gpp-service.ims.icsi.mmtel"},
	{Name: "P-Private-Network-Indication", Value: "example.com"},
	{Name: "Cellular-Network-Info", Value: "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101"},
}

// TS 24.229 4.4.7 is about a Reason in a response.
var reasonField = sip.HeaderField{Name: "Reason", Value: "SIP;cause=480"}

// want is what one side gets of a header field; nil for none.
type want map[string][]string

func checkFields(t *testing.T, side string, m *sip.Message, w want) {
	t.Helper()
	for name, values := range w {
		if got := m.GetAll(name); !slices.Equal(got, values) {
			t.Errorf("%s got %s %q, want %q", side, name, got, values)
		}
	}
}

// borderHarness is a P-CSCF between a registered UE and a fake S-CSCF,
// without a media relay. Each side answers a request with answer, and
// puts what it got on its channel.
type borderHarness struct {
	pcscf, ue, scscf *sip.Stack
	toUE, toNetwork  chan *sip.Message
	// A test sets what a side answers with while the stacks run, and a
	// datagram tells the race detector nothing about the order.
	ueAnswer, netAnswer atomic.Pointer[[]sip.HeaderField]
	contact, ueSentBy   string
}

func newBorderHarness(t *testing.T) *borderHarness {
	t.Helper()
	h := &borderHarness{toUE: make(chan *sip.Message, 4), toNetwork: make(chan *sip.Message, 4)}
	answer := func(got chan<- *sip.Message, fields *atomic.Pointer[[]sip.HeaderField]) func(*sip.ServerTransaction) {
		return func(st *sip.ServerTransaction) {
			got <- st.Request
			resp := sip.NewResponse(st.Request, 200, sip.NewTag())
			if with := fields.Load(); with != nil {
				for _, f := range *with {
					resp.Add(f.Name, f.Value)
				}
			}
			st.Respond(resp)
		}
	}
	h.scscf = newStack(t, answer(h.toNetwork, &h.netAnswer))
	h.ue = newStack(t, answer(h.toUE, &h.ueAnswer))
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: h.scscf.LocalAddr()}}
	})
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, NewMemoryStore(), nil, quiet())
	h.pcscf = newPCSCFStack(t, p)
	p.SetStack(h.pcscf)
	h.contact = "sip:001010000000001@" + h.ue.LocalAddr().String()
	p.store.PutAssociation(Association{
		Source:           sip.Destination{Transport: sip.UDP, Addr: h.ue.LocalAddr()},
		SentBy:           h.ue.LocalAddr().String(),
		Contact:          h.contact,
		PrivateIdentity:  impi,
		PublicIdentities: []string{impu},
		ServiceRoute:     []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires:          time.Now().Add(time.Hour),
	})
	return h
}

// borderMessage is a MESSAGE with fields, a request for a standalone
// transaction.
func borderMessage(requestURI, route string, fields ...[]sip.HeaderField) *sip.Message {
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: requestURI}
	m.Add("Max-Forwards", "70")
	m.Add("Route", route)
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<sip:001010000000002@"+domain+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	for _, fs := range fields {
		for _, f := range fs {
			m.Add(f.Name, f.Value)
		}
	}
	return m
}

// transact sends m from one stack to the P-CSCF, and returns what the
// other side got and the final response.
func (h *borderHarness) transact(t *testing.T, from *sip.Stack, m *sip.Message, at <-chan *sip.Message) (got, final *sip.Message) {
	t.Helper()
	ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: h.pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		t.Fatalf("no final response: %v", ct.Err())
	}
	select {
	case got = <-at:
	case <-time.After(3 * time.Second):
		t.Fatal("the request did not get through")
	}
	return got, final
}

// TestRequestAndResponseOfTheUEAtTheEdge is TS 24.229 5.2.1 and 4.4 for
// a request from the UE and its response from the network.
func TestRequestAndResponseOfTheUEAtTheEdge(t *testing.T) {
	h := newBorderHarness(t)
	netAnswer := append(slices.Clone(borderFields), reasonField)
	h.netAnswer.Store(&netAnswer)
	got, final := h.transact(t, h.ue, borderMessage("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>", borderFields, requestOnlyFields), h.toNetwork)

	if pcv := got.Get("P-Charging-Vector"); strings.Contains(pcv, "from-sender") || !strings.HasPrefix(pcv, "icid-value=") {
		t.Errorf("the S-CSCF got P-Charging-Vector %q, want a new one of the P-CSCF", pcv)
	}
	checkFields(t, "the S-CSCF", got, want{
		"P-Charging-Function-Addresses": nil,
		"P-Media-Authorization":         nil,
		// The UE's own stays; only the network may say network-provided.
		"P-Access-Network-Info":        {"3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101"},
		"Feature-Caps":                 nil,
		"Geolocation":                  {"<cid:target@192.0.2.10>;inserted-by=192.0.2.10"},
		"P-Asserted-Identity":          {"<" + impu + ">"},
		"P-Preferred-Identity":         nil,
		"P-Served-User":                nil,
		"P-Asserted-Service":           nil,
		"P-Profile-Key":                nil,
		"P-Private-Network-Indication": nil,
		"Relayed-Charge":               nil,
		"Service-Interact-Info":        nil,
		// The P-CSCF handles priority from the UE by TS 24.229 5.2, not
		// by 4.4.6, and has no priority procedures.
		"Resource-Priority":     {"wps.0"},
		"Cellular-Network-Info": {"3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101"},
		"P-Preferred-Service":   {"urn:urn-7:3gpp-service.ims.icsi.mmtel"},
		"Subject":               {"not a field of the trust domain"},
	})
	checkFields(t, "the UE", final, want{
		"P-Charging-Vector":             nil,
		"P-Charging-Function-Addresses": nil,
		"P-Media-Authorization":         nil,
		"P-Access-Network-Info":         nil,
		"Feature-Caps":                  {"*;+g.3gpp.icsi-ref=\"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel\""},
		"P-Asserted-Identity":           {"<sip:forged@" + domain + ">"},
		"P-Served-User":                 nil,
		"P-Profile-Key":                 nil,
		"Resource-Priority":             nil,
		// See ueBoundary about Reason.
		"Reason":                {reasonField.Value},
		"Relayed-Charge":        nil,
		"Service-Interact-Info": nil,
		"Subject":               {"not a field of the trust domain"},
	})
}

// TestRequestAndResponseToTheUEAtTheEdge is TS 24.229 5.2.1 and 4.4 for
// a request from the network to the UE and the UE's response.
func TestRequestAndResponseToTheUEAtTheEdge(t *testing.T) {
	h := newBorderHarness(t)
	ueAnswer := append(slices.Clone(borderFields), reasonField)
	h.ueAnswer.Store(&ueAnswer)
	called := sip.HeaderField{Name: "P-Called-Party-ID", Value: "<sip:001010000000001@" + domain + ">"}
	m := borderMessage(h.contact, "<sip:term@pcscf."+domain+":5060;lr>", borderFields, requestOnlyFields, []sip.HeaderField{called})
	got, final := h.transact(t, h.scscf, m, h.toUE)

	checkFields(t, "the UE", got, want{
		"P-Charging-Vector":             nil,
		"P-Charging-Function-Addresses": nil,
		"P-Media-Authorization":         nil,
		"P-Access-Network-Info":         nil,
		"Feature-Caps":                  {"*;+g.3gpp.icsi-ref=\"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel\""},
		"P-Asserted-Identity":           {"<sip:forged@" + domain + ">"},
		"P-Served-User":                 nil,
		"P-Asserted-Service":            nil,
		"P-Profile-Key":                 nil,
		"P-Private-Network-Indication":  nil,
		"Resource-Priority":             nil,
		"Relayed-Charge":                nil,
		"Service-Interact-Info":         nil,
		"Cellular-Network-Info":         nil,
		"P-Called-Party-ID":             {called.Value},
		"Subject":                       {"not a field of the trust domain"},
	})
	checkFields(t, "the S-CSCF", final, want{
		"P-Charging-Vector":             nil,
		"P-Charging-Function-Addresses": nil,
		"P-Media-Authorization":         nil,
		"P-Access-Network-Info":         {"3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101"},
		"Feature-Caps":                  nil,
		"Geolocation":                   {"<cid:target@192.0.2.10>;inserted-by=192.0.2.10"},
		// 5.2.6.4.8 step 2: the identity of the P-Called-Party-ID.
		"P-Asserted-Identity":  {called.Value},
		"P-Preferred-Identity": nil,
		"P-Served-User":        nil,
		"P-Profile-Key":        nil,
		"Relayed-Charge":       nil,
		// TS 24.229 4.4.7 NOTE: a Reason from outside the trust domain
		// stays.
		"Reason":                {reasonField.Value},
		"Service-Interact-Info": nil,
		"Subject":               {"not a field of the trust domain"},
	})
}

// TestIdentityOfAPrivateCallerStaysInTheNetwork is RFC 3325 7 at the
// edge (TS 24.229 4.4.2): a request to the UE whose Privacy has "id"
// arrives without P-Asserted-Identity, and with the Privacy.
func TestIdentityOfAPrivateCallerStaysInTheNetwork(t *testing.T) {
	h := newBorderHarness(t)
	m := borderMessage(h.contact, "<sip:term@pcscf."+domain+":5060;lr>", []sip.HeaderField{
		{Name: "P-Asserted-Identity", Value: "<sip:001010000000002@" + domain + ">"},
		{Name: "Privacy", Value: "id"},
	})
	got, _ := h.transact(t, h.scscf, m, h.toUE)
	checkFields(t, "the UE", got, want{"P-Asserted-Identity": nil, "Privacy": {"id"}})
}

// TestResponsesToREGISTERReachTheUEWithoutChargingInformation is TS
// 24.229 5.2.1 for the 401 and the 200 of a registration.
func TestResponsesToREGISTERReachTheUEWithoutChargingInformation(t *testing.T) {
	home := newStack(t, func(st *sip.ServerTransaction) {
		code := 401
		if c, _ := sip.ParseCredentials(st.Request.Get("Authorization")); c.Response != "" {
			code = 200
		}
		resp := sip.NewResponse(st.Request, code, sip.NewTag())
		resp.Add("P-Charging-Vector", st.Request.Get("P-Charging-Vector")+";term-ioi="+domain)
		resp.Add("P-Charging-Function-Addresses", "ccf=192.0.2.1")
		if code == 200 {
			resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
			resp.Add("P-Associated-URI", "<"+impu+">")
			resp.Add("Contact", st.Request.Get("Contact")+";expires=3600")
		}
		st.Respond(resp)
	})
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: home.LocalAddr()}}
	})
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, NewMemoryStore(), nil, quiet())
	pcscf := newPCSCFStack(t, p)
	p.SetStack(pcscf)
	ue := newStack(t, func(*sip.ServerTransaction) {})
	for _, response := range []string{"", "0123456789abcdef0123456789abcdef"} {
		m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
		m.Add("Max-Forwards", "70")
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", "register-1")
		m.Add("CSeq", "1 REGISTER")
		m.Add("Contact", "<sip:001010000000001@"+ue.LocalAddr().String()+">")
		m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="`+response+`"`)
		m.Add("P-Charging-Vector", "icid-value=from-ue")
		ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		var final *sip.Message
		for r := range ct.Responses() {
			final = r
		}
		if final == nil {
			t.Fatalf("no final response: %v", ct.Err())
		}
		checkFields(t, "the UE in the "+final.Reason, final, want{"P-Charging-Vector": nil, "P-Charging-Function-Addresses": nil})
	}
}
