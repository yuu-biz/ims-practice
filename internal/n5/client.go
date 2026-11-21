package n5

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// contentTypeMergePatch is the media type of the body of an Update, a
// JSON Merge Patch (TS 29.514 5.2.2.2, RFC 7396). The other bodies are
// sbi.ContentTypeJSON.
const contentTypeMergePatch = "application/merge-patch+json"

// Client is the AF side of Npcf_PolicyAuthorization: the requests the
// P-CSCF sends to the PCF. It keeps no state; an application session
// context is named by the resource URI that Create returns, which the
// caller keeps and gives back to Update and Delete. Any instance of the
// caller can therefore go on with a context another one created.
//
// SIMPLIFIED: TS 29.513 8.4. The client sends to the one PCF it is
// given; there is no BSF to find the PCF that serves the UE's PDU
// session.
type Client struct {
	apiRoot string
	http    *http.Client
}

// NFTypePCSCF is the NF type the client names in User-Agent (TS 29.500
// Table 5.2.2.2-1; the value is from the NFType enumeration of TS
// 29.510).
const NFTypePCSCF = "PCSCF"

// NewClient returns a client for the PCF whose apiRoot is apiRoot: the
// scheme, the authority and an optional prefix, such as
// "http://pcf.5gc.example" (the servers section of the OpenAPI of TS
// 29.514 refers to TS 29.501 4.4 for its definition).
func NewClient(apiRoot string) *Client {
	return &Client{apiRoot: strings.TrimSuffix(apiRoot, "/"), http: sbi.NewHTTPClient()}
}

// AppSession is an Individual Application Session Context the PCF
// created: its resource URI, from the Location header, and the
// representation the PCF answered with.
type AppSession struct {
	URI string
	// Context is empty when the PCF answered 303: a context with the
	// same service information exists already, and URI names it (TS
	// 29.514 Annex A.2, the 303 of PostAppSessions).
	Context AppSessionContext
}

// Create is Npcf_PolicyAuthorization_Create (TS 29.514 4.2.2.2): a POST
// of the service information to the Application Sessions collection.
// The PCF answers 201 with the new context; a failed session binding
// comes back as a *ProblemError with CausePDUSessionNotAvailable, and a
// refused service as one with CauseRequestedServiceNotAuthorized.
func (c *Client) Create(ctx context.Context, req AppSessionContextReqData) (AppSession, error) {
	uri := c.apiRoot + AppSessionsPath
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:      http.MethodPost,
		URI:         uri,
		Body:        AppSessionContext{AscReqData: &req},
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   NFTypePCSCF,
	})
	if err != nil {
		return AppSession{}, err
	}
	if resp.Status != http.StatusCreated && resp.Status != http.StatusSeeOther {
		return AppSession{}, fmt.Errorf("n5: Create answered %d", resp.Status)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return AppSession{}, fmt.Errorf("n5: Create answered %d without Location", resp.Status)
	}
	s := AppSession{}
	if s.URI, err = resolve(uri, location); err != nil {
		return AppSession{}, err
	}
	if resp.Status == http.StatusCreated && len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &s.Context); err != nil {
			return AppSession{}, fmt.Errorf("n5: Create answer: %w", err)
		}
	}
	return s, nil
}

// Update is Npcf_PolicyAuthorization_Update (TS 29.514 4.2.3.2): a
// PATCH of the context at appSessionURI with a JSON Merge Patch. It
// returns the context when the PCF answers 200 with it, and nil when
// it answers 204.
func (c *Client) Update(ctx context.Context, appSessionURI string, update AppSessionContextUpdateData) (*AppSessionContext, error) {
	patch := AppSessionContextUpdateDataPatch{AscReqData: &update}
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:      http.MethodPatch,
		URI:         appSessionURI,
		Body:        patch,
		ContentType: contentTypeMergePatch,
		UserAgent:   NFTypePCSCF,
	})
	if err != nil {
		return nil, err
	}
	return optionalContext("Update", resp)
}

// Delete is Npcf_PolicyAuthorization_Delete (TS 29.514 4.2.4.2): a POST
// to the delete custom operation of the context at appSessionURI.
// evSubsc, when not nil, asks for events to be reported in the answer;
// the context then comes back with them (200), otherwise the answer is
// 204 and the result nil. A context the PCF no longer knows gives a
// *ProblemError with CauseApplicationSessionContextNotFound.
func (c *Client) Delete(ctx context.Context, appSessionURI string, evSubsc *EventsSubscReqData) (*AppSessionContext, error) {
	var body any
	if evSubsc != nil {
		body = evSubsc
	}
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:      http.MethodPost,
		URI:         appSessionURI + "/delete",
		Body:        body,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   NFTypePCSCF,
	})
	if err != nil {
		return nil, err
	}
	return optionalContext("Delete", resp)
}

// resolve turns a Location into an absolute URI. TS 29.514 4.2.2.2
// sends an absolute one; a relative one is read against the request.
func resolve(requestURI, location string) (string, error) {
	base, err := url.Parse(requestURI)
	if err != nil {
		return "", err
	}
	loc, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("n5: Location %q: %w", location, err)
	}
	return base.ResolveReference(loc).String(), nil
}

// optionalContext reads the answer of Update and Delete: 200 with the
// context, or 204 without a body.
func optionalContext(operation string, resp sbi.Response) (*AppSessionContext, error) {
	switch resp.Status {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var c AppSessionContext
		if err := json.Unmarshal(resp.Body, &c); err != nil {
			return nil, fmt.Errorf("n5: %s answer: %w", operation, err)
		}
		return &c, nil
	}
	return nil, fmt.Errorf("n5: %s answered %d", operation, resp.Status)
}

// EventsSubscriptionPath is the path segment that turns the URI of a
// context into that of its Events Subscription sub-resource (TS 29.514
// 4.2.2.2).
const EventsSubscriptionPath = "/events-subscription"

// AppSessionURI returns the context that the Events Subscription at
// evSubsUri belongs to: the URI a Create returned. An AF uses it to
// find its session from the evSubsUri of an EventsNotification.
func AppSessionURI(evSubsUri string) string {
	return strings.TrimSuffix(evSubsUri, EventsSubscriptionPath)
}
