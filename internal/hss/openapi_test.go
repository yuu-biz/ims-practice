package hss

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// provisioningDocument is openapi.yaml, the description of the
// provisioning API that the tests check its exchanges against.
func provisioningDocument(t *testing.T) *openapitest.Document {
	t.Helper()
	return openapitest.Load(t, "openapi.yaml")
}

// TestOpenAPI sends a request to each operation of openapi.yaml, with
// the answers that the description of the operation names, and checks
// that the handler takes exactly the methods of the document.
func TestOpenAPI(t *testing.T) {
	h := newTestHSS()
	withProfiles(h, map[string]ServiceProfile{"default": {}, "trial": {}}, "default", "trial")
	api := h.Provisioning(&fakeSCSCFs{})
	doc := provisioningDocument(t)
	checked := doc.Handler(t, api)
	send := func(method, path, body string, status int) {
		t.Helper()
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		w := httptest.NewRecorder()
		checked.ServeHTTP(w, httptest.NewRequest(method, path, r))
		if w.Code != status {
			t.Errorf("%s %s %s = %d %s, want %d", method, path, body, w.Code, w.Body, status)
		}
	}
	added := "001010000000009@" + realm
	aka := `{"private_identity": "001010000000009@` + realm + `",
		"public_identities": ["sip:001010000000009@` + realm + `"], "tel": "tel:+12025550109",
		"aka": {"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "opc": "cd63cb71954a9f4e48a5994e37a02baf"},
		"test_user": true, "service_profile": "trial"}`
	taken := `{"private_identity": "001010000000008@` + realm + `",
		"public_identities": ["sip:001010000000009@` + realm + `"], "password": "test-password-8"}`

	send("GET", "/subscriptions", "", 200)
	send("GET", "/subscriptions?limit=1", "", 200)
	send("GET", "/subscriptions?limit=0", "", 400)
	send("POST", "/subscriptions", aka, 201)
	send("POST", "/subscriptions", aka, 409)
	send("POST", "/subscriptions", `{"private_identity": "x"}`, 400)
	send("GET", "/subscriptions/"+added, "", 200)
	send("GET", "/subscriptions/nobody@"+realm, "", 404)
	send("GET", "/subscriptions/export?after="+impi+"&limit=10", "", 200)
	send("GET", "/subscriptions/export?limit=many", "", 400)
	send("PUT", "/subscriptions/"+added+"?dry_run=true", aka, 200)
	send("PUT", "/subscriptions/"+added, aka, 200)
	// null is how a client written in Go sends a nil slice; the HSS reads
	// it as a request without the field.
	send("PUT", "/subscriptions/"+added, strings.Replace(aka, `"test_user"`, `"repository_data": null, "test_user"`, 1), 200)
	send("PUT", "/subscriptions/001010000000007@"+realm, strings.NewReplacer("000009", "000007", "5550109", "5550107").Replace(newSubscription), 201)
	send("PUT", "/subscriptions/001010000000008@"+realm, taken, 409)
	send("PUT", "/subscriptions/"+added+"?dry_run=maybe", aka, 400)
	send("PATCH", "/subscriptions/"+added, `{"test_user": false, "service_profile": "default"}`, 200)
	send("PATCH", "/subscriptions/"+added, `{"service_profile": "gold"}`, 400)
	send("PATCH", "/subscriptions/nobody@"+realm, `{}`, 404)
	send("PUT", "/subscriptions/"+added+"/customized-alerting-tone", `{"tone": "cat1", "active": true}`, 200)
	send("PUT", "/subscriptions/"+added+"/customized-alerting-tone", `{"tone": "cat1", "volume": 3}`, 400)
	send("PUT", "/subscriptions/nobody@"+realm+"/customized-alerting-tone", `{"tone": "cat1", "active": true}`, 404)
	send("DELETE", "/subscriptions/"+added, "", 204)
	send("DELETE", "/subscriptions/"+added, "", 404)
	send("GET", "/subscribers", "", 404)
	send("POST", "/subscriptions/export", "{}", 405)

	doc.CheckRoutes(t, api)
	doc.CheckCovered(t)
}
