package oam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
