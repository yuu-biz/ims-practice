package xcap_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

var cp = map[string]string{"cp": simservs.NamespaceCommonPolicy}

// ruleSelector selects one rule of a service, as GSMA IR.92 2.3.2 has
// the UE change one rule per request.
func ruleSelector(service, id string) xcap.Selector {
	return xcap.Selector{Path: "simservs/" + service + `/cp:ruleset/cp:rule[@id="` + id + `"]`, Namespaces: cp}
}

// forwardRule is a diversion rule without <rule-deactivated/>: active.
func forwardRule(id, target string, conditions *simservs.Conditions) []byte {
	return simservs.MarshalElement(simservs.Rule{ID: id, Conditions: conditions,
		Actions: &simservs.Actions{ForwardTo: &simservs.ForwardTo{Target: target}}})
}

func status(err error) int {
	var se *xcap.StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

func TestSetCFUOverXCAPAndReadItBackOverSh(t *testing.T) {
	n := newNetwork(t)
	ctx := context.Background()
	c := n.client(impuA)

	doc, err := c.Get(ctx, xcap.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContentType != simservs.MIMEType || doc.ETag != `"0"` {
		t.Errorf("GET: Content-Type %q, ETag %s", doc.ContentType, doc.ETag)
	}
	user, err := simservs.ParseSimservs(doc.Body)
	if err != nil {
		t.Fatal(err)
	}
	if user.CommunicationDiversion == nil || user.CommunicationDiversion.Ruleset.Rules[0].Conditions.RuleDeactivated == nil {
		t.Fatalf("the first document has no deactivated CFU rule: %s", doc.Body)
	}

	resp, err := c.Put(ctx, ruleSelector("communication-diversion", "cfu"), forwardRule("cfu", "tel:+12025550153", nil), doc.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK || resp.ETag != `"1"` || len(resp.Body) != 0 {
		t.Errorf("PUT: %d, ETag %s, body %q", resp.Status, resp.ETag, resp.Body)
	}

	// The MMTel AS reads the setting over Sh and diverts the next call.
	m, seq := n.stored(t, impuA)
	if seq != 1 {
		t.Errorf("Sequence-Number %d", seq)
	}
	cdiv := m.CompleteCommunicationDiversion
	r := simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, simservs.AtCommunicationSetup, simservs.CallInfo{CallingIdentity: "tel:+12025550170"})
	if !r.Diverts() || r.ForwardTo.Target != "tel:+12025550153" {
		t.Errorf("after the PUT the AS diverts %+v", r)
	}
	if cdiv.OperatorCommunicationDiversion.TotalNumberOfDiversionsForEachCommunication != 5 {
		t.Errorf("the operator part changed: %+v", cdiv.OperatorCommunicationDiversion)
	}

	// The tel URI is an alias of the same subscription, so it names the
	// same document.
	got, err := n.client(telA).Get(ctx, ruleSelector("communication-diversion", "cfu"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentType != simservs.ElementMIMEType || got.ETag != `"1"` || !bytes.Contains(got.Body, []byte("tel:+12025550153")) {
		t.Errorf("GET of the rule: %q %s %s", got.ContentType, got.ETag, got.Body)
	}
}

func TestPutAttributeAndGetIt(t *testing.T) {
	n := newNetwork(t)
	ctx := context.Background()
	c := n.client(impuA)
	// TS 24.623 6.2 b: the owner may not add an attribute to a service.
	if _, err := c.Put(ctx, xcap.Selector{Path: "simservs/communication-diversion/@active"}, []byte(`"false"`), ""); status(err) != http.StatusConflict {
		t.Errorf("adding active to <communication-diversion>: %v", err)
	}
	sel := xcap.Selector{Path: "simservs/originating-identity-presentation-restriction/@active"}
	if _, err := c.Put(ctx, sel, []byte(`"false"`), `"0"`); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, sel)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentType != simservs.AttributeMIMEType || string(got.Body) != `"false"` {
		t.Errorf("GET: %q %s", got.ContentType, got.Body)
	}
	if m, _ := n.stored(t, impuA); m.CompleteOriginatingIdentityRestriction.OriginatingIdentityPresentationRestriction.IsActive() {
		t.Error("OIR is still active in the HSS")
	}
}

// Two devices of one user change different rules from the same version:
// the second gets 412 and starts again from the newer document (RFC
// 4825 7.11).
func TestConcurrentWritersRetry(t *testing.T) {
	n := newNetwork(t)
	ctx := context.Background()
	first, second := n.client(impuA), n.client(impuA)
	d1, err := first.Get(ctx, xcap.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := second.Get(ctx, xcap.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Put(ctx, ruleSelector("communication-diversion", "cfu"), forwardRule("cfu", "tel:+12025550153", nil), d1.ETag); err != nil {
		t.Fatal(err)
	}
	busy := &simservs.Conditions{Busy: &simservs.Empty{}}
	_, err = second.Put(ctx, ruleSelector("communication-diversion", "cfb"), forwardRule("cfb", "tel:+12025550154", busy), d2.ETag)
	if status(err) != http.StatusPreconditionFailed {
		t.Fatalf("PUT on the old version: %v", err)
	}
	d2, err = second.Get(ctx, xcap.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Put(ctx, ruleSelector("communication-diversion", "cfb"), forwardRule("cfb", "tel:+12025550154", busy), d2.ETag); err != nil {
		t.Fatal(err)
	}
	m, seq := n.stored(t, impuA)
	rules := m.CompleteCommunicationDiversion.CommunicationDiversion.Ruleset.Rules
	if seq != 2 || rules[0].Actions.ForwardTo.Target != "tel:+12025550153" || rules[1].Actions.ForwardTo.Target != "tel:+12025550154" {
		t.Errorf("Sequence-Number %d, rules %+v", seq, rules)
	}
}

// interleaved is the HSS as seen by an AS instance while another
// instance writes the same document between this one's UDR and PUR.
type interleaved struct {
	xcap.Repository
	once  sync.Once
	other func()
}

func (r *interleaved) UpdateRepositoryData(ctx context.Context, publicID, serviceIndication string, read uint16, data []byte) error {
	r.once.Do(r.other)
	return r.Repository.UpdateRepositoryData(ctx, publicID, serviceIndication, read, data)
}

// The HSS refuses the PUR with 5105 (TS 29.328 6.1.2.1): a conditional
// request then fails as if it had come after the other writer, and an
// unconditional one is applied to the newer document.
func TestWriterBetweenReadAndWrite(t *testing.T) {
	for _, tc := range []struct {
		ifMatch string
		want    int
		seq     uint16
	}{
		{ifMatch: `"0"`, want: http.StatusPreconditionFailed, seq: 1},
		{ifMatch: "", want: http.StatusOK, seq: 2},
	} {
		n := newNetwork(t)
		ctx := context.Background()
		repo := &interleaved{Repository: n.sh, other: func() {
			d, _, err := n.sh.RepositoryData(ctx, impuA, simservs.ServiceIndication)
			if err != nil {
				t.Error(err)
				return
			}
			if err := n.sh.UpdateRepositoryData(ctx, impuA, simservs.ServiceIndication, d.SequenceNumber, d.ServiceData.XML); err != nil {
				t.Error(err)
			}
		}}
		server := httptest.NewServer(xcap.NewHandler(repo, "", []byte(nonceKey), quiet()))
		c := n.client(impuA)
		c.Root = server.URL
		_, err := c.Put(ctx, ruleSelector("communication-diversion", "cfu"), forwardRule("cfu", "tel:+12025550153", nil), tc.ifMatch)
		if got := status(err); err != nil && got != tc.want || err == nil && tc.want != http.StatusOK {
			t.Errorf("If-Match %q: %v, want %d", tc.ifMatch, err, tc.want)
		}
		if _, seq := n.stored(t, impuA); seq != tc.seq {
			t.Errorf("If-Match %q: Sequence-Number %d, want %d", tc.ifMatch, seq, tc.seq)
		}
		server.Close()
	}
}

// countingTransport counts the 401 responses and lets a test change
// requests on their way.
type countingTransport struct {
	unauthorized int
	change       func(*http.Request)
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if c.change != nil {
		c.change(r)
	}
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		c.unauthorized++
	}
	return resp, err
}

func TestAuthentication(t *testing.T) {
	n := newNetwork(t)
	ctx := context.Background()

	resp, err := http.Get(n.server.URL + "/simservs.ngn.etsi.org/users/" + impuA + "/simservs.xml")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	challenges := resp.Header.Values("WWW-Authenticate")
	if resp.StatusCode != http.StatusUnauthorized || len(challenges) != 2 ||
		!strings.Contains(challenges[0], "algorithm=SHA-256") || !strings.Contains(challenges[1], "algorithm=MD5") {
		t.Errorf("without Authorization: %d %q", resp.StatusCode, challenges)
	}

	// The client answers the challenge once, then sends its
	// credentials with every request.
	transport := &countingTransport{}
	c := n.client(impuA)
	c.HTTP = &http.Client{Transport: transport}
	for range 3 {
		if _, err := c.Get(ctx, xcap.Selector{}); err != nil {
			t.Fatal(err)
		}
	}
	if transport.unauthorized != 1 {
		t.Errorf("%d responses were 401", transport.unauthorized)
	}

	wrong := n.client(impuA)
	wrong.Password = "wrong"
	if _, err := wrong.Get(ctx, xcap.Selector{}); status(err) != http.StatusUnauthorized {
		t.Errorf("wrong password: %v", err)
	}
	// A authenticates fine, but the document is B's (TS 24.623 6.2).
	if _, err := n.client(impuB).Get(ctx, xcap.Selector{}); status(err) != http.StatusForbidden {
		t.Errorf("the document of another user: %v", err)
	}
	// C has no Ut credential, so is not allowed to use Ut (TS 24.623
	// 5.3.2.3).
	if _, err := n.client(impuC).Get(ctx, xcap.Selector{}); status(err) != http.StatusForbidden {
		t.Errorf("a user without a Ut credential: %v", err)
	}
	// RFC 4825 8: an XUI the server does not know.
	if _, err := n.client("sip:001010000000099@"+realm).Get(ctx, xcap.Selector{}); status(err) != http.StatusNotFound {
		t.Errorf("an unknown XUI: %v", err)
	}
}

func TestConflicts(t *testing.T) {
	n := newNetwork(t)
	ctx := context.Background()
	c := n.client(impuA)
	cfu := ruleSelector("communication-diversion", "cfu")
	for _, tc := range []struct {
		name      string
		do        func() error
		condition string
	}{
		{"a condition that diversion does not take", func() error {
			_, err := c.Put(ctx, cfu, forwardRule("cfu", "tel:+12025550153", &simservs.Conditions{Roaming: &simservs.Empty{}}), "")
			return err
		}, "constraint-failure"},
		{"a NoReplyTimer out of range", func() error {
			_, err := c.Put(ctx, xcap.Selector{Path: "simservs/communication-diversion/NoReplyTimer"}, []byte(`<NoReplyTimer xmlns="`+simservs.NamespaceSimservs+`">3</NoReplyTimer>`), "")
			return err
		}, ""},
		{"a body that is not one element", func() error {
			_, err := c.Put(ctx, cfu, []byte(`<cp:rule`), "")
			return err
		}, "not-xml-frag"},
		{"removing a service", func() error {
			_, err := c.Delete(ctx, xcap.Selector{Path: "simservs/communication-diversion"}, "")
			return err
		}, ""},
		{"removing the document", func() error {
			_, err := c.Delete(ctx, xcap.Selector{}, "")
			return err
		}, "cannot-delete"},
		{"a barring service under password control", func() error {
			_, err := c.Put(ctx, ruleSelector("outgoing-communication-barring", "baoc"),
				simservs.MarshalElement(simservs.Rule{ID: "baoc", Actions: &simservs.Actions{Allow: new(false)}}), "")
			return err
		}, "constraint-failure"},
	} {
		err := tc.do()
		var se *xcap.StatusError
		if !errors.As(err, &se) || se.Status != http.StatusConflict || se.ContentType != simservs.ErrorMIMEType ||
			!bytes.Contains(se.Body, []byte("<xcap-error")) || !bytes.Contains(se.Body, []byte(tc.condition)) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, seq := n.stored(t, impuA); seq != 0 {
		t.Errorf("a refused change was stored: Sequence-Number %d", seq)
	}
	// A barring service without password control can be changed.
	if _, err := c.Put(ctx, ruleSelector("incoming-communication-barring", "baic"),
		simservs.MarshalElement(simservs.Rule{ID: "baic", Actions: &simservs.Actions{Allow: new(false)}}), ""); err != nil {
		t.Error(err)
	}
}

func TestRequestErrors(t *testing.T) {
	n := newNetwork(t)
	ctx := context.Background()
	transport := &countingTransport{}
	c := n.client(impuA)
	c.HTTP = &http.Client{Transport: transport}

	transport.change = func(r *http.Request) { r.Header.Set("Content-Type", "application/xml") }
	if _, err := c.Put(ctx, ruleSelector("communication-diversion", "cfu"), forwardRule("cfu", "", nil), ""); status(err) != http.StatusUnsupportedMediaType {
		t.Errorf("PUT of an element as application/xml: %v", err)
	}
	transport.change = nil
	if _, err := c.Get(ctx, ruleSelector("communication-diversion", "cfnl")); status(err) != http.StatusNotFound {
		t.Errorf("GET of a rule that does not exist: %v", err)
	}
	if _, err := c.Get(ctx, xcap.Selector{Path: "simservs/communication-diversion/cp:ruleset"}); status(err) != http.StatusBadRequest {
		t.Errorf("a prefix without a binding: %v", err)
	}
	if _, err := c.Put(ctx, xcap.Selector{Path: "simservs/communication-diversion/namespace::*"}, nil, ""); status(err) != http.StatusMethodNotAllowed {
		t.Errorf("PUT of the namespace bindings: %v", err)
	}
	other := n.client(impuA)
	other.Root = n.server.URL + "/resource-lists"
	if _, err := other.Get(ctx, xcap.Selector{}); status(err) != http.StatusNotFound {
		t.Errorf("an application usage the server does not have: %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	server := httptest.NewServer(xcap.NewHandler(nil, "/xcap", []byte(nonceKey), quiet()))
	defer server.Close()
	resp, err := http.Get(server.URL + "/xcap/xcap-caps/global/index")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/xcap-caps+xml" || resp.Header.Get("ETag") == "" ||
		!bytes.Contains(body, []byte("<auid>simservs.ngn.etsi.org</auid>")) {
		t.Errorf("%d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
}

var _ xcap.Repository = (*sh.Client)(nil)
