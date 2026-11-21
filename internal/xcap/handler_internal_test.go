package xcap

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/simservs"
)

func TestParseTarget(t *testing.T) {
	for _, tc := range []struct {
		path, query string
		xui         string
		selector    string
		ok          bool
		selectorErr error
	}{
		{path: "simservs.ngn.etsi.org/users/sip:001010000000001@ims.example/simservs.xml", xui: "sip:001010000000001@ims.example", ok: true},
		// RFC 4825 6.2: characters of a SIP URI that a path segment
		// cannot hold are percent-encoded.
		{path: "simservs.ngn.etsi.org/users/sip:%2B12025550101@ims.example%3Buser=phone/simservs.xml", xui: "sip:+12025550101@ims.example;user=phone", ok: true},
		{path: "simservs.ngn.etsi.org/users/tel:+12025550101/simservs.xml/~~/simservs/communication-diversion/cp:ruleset/cp:rule%5B@id=%22cfu%22%5D",
			query: "xmlns(cp=urn:ietf:params:xml:ns:common-policy)", xui: "tel:+12025550101",
			selector: `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfu"]`, ok: true},
		// RFC 4825 6: %7E%7E is the separator too.
		{path: "simservs.ngn.etsi.org/users/tel:+12025550101/simservs.xml/%7E%7E/simservs/communication-diversion", xui: "tel:+12025550101", selector: "simservs/communication-diversion", ok: true},
		{path: "simservs.ngn.etsi.org/users/tel:+12025550101/simservs.xml/~~/simservs/cp:ruleset", xui: "tel:+12025550101", ok: true, selectorErr: simservs.ErrUndefinedPrefix},
		{path: "simservs.ngn.etsi.org/global/index"},
		{path: "resource-lists/users/tel:+12025550101/index"},
		{path: "simservs.ngn.etsi.org/users/tel:+12025550101/other.xml"},
		{path: "simservs.ngn.etsi.org/users/tel:+12025550101/simservs.xml/~~/simservs/communication-diversion[1"},
	} {
		got, ok := parseTarget(tc.path, tc.query)
		if ok != tc.ok || ok && (got.xui != tc.xui || !errors.Is(got.selectorErr, tc.selectorErr)) {
			t.Errorf("%s: ok %v, xui %q, selector error %v", tc.path, ok, got.xui, got.selectorErr)
			continue
		}
		if s := selectorText(got); ok && tc.selectorErr == nil && s != tc.selector {
			t.Errorf("%s: selector %q, want %q", tc.path, s, tc.selector)
		}
	}
}

func TestPrecondition(t *testing.T) {
	for _, tc := range []struct {
		method, header, value string
		exists                bool
		want                  int
	}{
		{http.MethodPut, "If-Match", `"3"`, true, 0},
		{http.MethodPut, "If-Match", `"2", "3"`, true, 0},
		{http.MethodPut, "If-Match", `"2"`, true, http.StatusPreconditionFailed},
		{http.MethodPut, "If-Match", `W/"3"`, true, http.StatusPreconditionFailed},
		{http.MethodPut, "If-Match", `*`, true, 0},
		{http.MethodPut, "If-Match", `*`, false, http.StatusPreconditionFailed},
		{http.MethodGet, "If-None-Match", `"3"`, true, http.StatusNotModified},
		{http.MethodGet, "If-None-Match", `W/"3"`, true, http.StatusNotModified},
		{http.MethodGet, "If-None-Match", `"2"`, true, 0},
		{http.MethodPut, "If-None-Match", `*`, true, http.StatusPreconditionFailed},
		{http.MethodPut, "If-None-Match", `*`, false, 0},
	} {
		r := httptest.NewRequest(tc.method, "/", nil)
		r.Header.Set(tc.header, tc.value)
		if got := precondition(r, `"3"`, tc.exists); got != tc.want {
			t.Errorf("%s %s: %s (exists %v) = %d, want %d", tc.method, tc.header, tc.value, tc.exists, got, tc.want)
		}
	}
}
