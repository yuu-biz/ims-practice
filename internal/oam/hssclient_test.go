package oam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestNextPageReadsTheLinkField: the page after an answer of the export
// is the after of its link of rel="next" (RFC 8288 3), wherever that
// link stands in the Link fields.
func TestNextPageReadsTheLinkField(t *testing.T) {
	const after = "001010000000001@ims.example"
	tests := []struct {
		name  string
		links []string
		want  string
		fails bool
	}{
		{"no Link", nil, "", false},
		{"one link", []string{`<?after=001010000000002%40ims.example&limit=2>; rel="next"`}, "001010000000002@ims.example", false},
		{"a token value", []string{`<?after=b%40ims.example>; rel=next`}, "b@ims.example", false},
		{"another relation type only", []string{`<?after=b%40ims.example>; rel="prev"`}, "", false},
		{"two links in one field", []string{`<?after=0%40ims.example>; rel="prev", <?after=b%40ims.example>; rel="next"`}, "b@ims.example", false},
		{"next before another in one field", []string{`<?after=b%40ims.example>; rel="next", <?after=z%40ims.example>; rel="last"`}, "b@ims.example", false},
		{"two fields", []string{`<?after=0%40ims.example>; rel="prev"`, `<?after=b%40ims.example>; rel="next"`}, "b@ims.example", false},
		{"several relation types", []string{`<?after=b%40ims.example>; rel="next last"`}, "b@ims.example", false},
		{"relation type in capitals", []string{`<?after=b%40ims.example>; rel="NEXT"`}, "b@ims.example", false},
		{"a comma in a quoted value", []string{`<?after=b%40ims.example>; title="a, b"; rel="next"`}, "b@ims.example", false},
		{"no target between < and >", []string{`?after=b%40ims.example; rel="next"`}, "", true},
		{"no after in the target", []string{`<?limit=2>; rel="next"`}, "", true},
		{"a page that does not come after this one", []string{`<?after=001010000000001%40IMS.example>; rel="next"`}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, _ := url.Parse("http://hss.ims.example:8081/subscriptions/export?after=" + url.QueryEscape(after))
			resp := &http.Response{Header: http.Header{"Link": tt.links}, Request: &http.Request{URL: base}}
			got, err := nextPage(resp, after)
			if tt.fails {
				if !errors.Is(err, ErrUpstream) {
					t.Errorf("nextPage = %q, %v; want the failure of the HSS", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("nextPage = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

// TestA404IsAMissingSubscriptionOnlyForOneThatIsThere: a GET, a PATCH
// or a DELETE of one subscription that answers 404 finds it missing. A
// PUT makes the subscription, and the export is of the whole store, so
// their 404 says that what answered is not the provisioning API.
func TestA404IsAMissingSubscriptionOnlyForOneThatIsThere(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	c := &hssClient{base: srv.URL, http: srv.Client()}
	ctx := context.Background()
	if _, err := c.get(ctx, "001010000000001@ims.example"); !errors.Is(err, errNoSubscription) {
		t.Errorf("GET: %v, want no such subscription", err)
	}
	if err := c.delete(ctx, "001010000000001@ims.example"); !errors.Is(err, errNoSubscription) {
		t.Errorf("DELETE: %v, want no such subscription", err)
	}
	var notThisAPI *hssError
	if _, err := c.put(ctx, hssSubscriptionRequest{PrivateIdentity: "001010000000001@ims.example"}, false); !errors.As(err, &notThisAPI) {
		t.Errorf("PUT: %v, want the HSS's 404 as it is", err)
	}
	if _, err := c.export(ctx, "", 10, func(json.RawMessage) error { return nil }); !errors.As(err, &notThisAPI) {
		t.Errorf("export: %v, want the HSS's 404 as it is", err)
	}
}
