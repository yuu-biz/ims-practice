package oam

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// The HSS provisioning API (internal/hss, Provisioning) as the OAM
// speaks it:
//
//	GET    /subscriptions/{impi}   one subscription
//	POST   /subscriptions          add one
//	PUT    /subscriptions/{impi}[?dry_run=true]
//	       create or replace one, answering hssPutAnswer
//	PATCH  /subscriptions/{impi}
//	       change test_user and service_profile only (hssPatch)
//	DELETE /subscriptions/{impi}   remove one
//	GET    /subscriptions/export?after={impi}&limit={n}
//	       the subscriptions after impi, credentials included, one JSON
//	       object per line, in the order of the private identities
//	PUT    /subscriptions/{impi}/customized-alerting-tone
//	       provision or change the CAT of one, from an AlertingTone
//
// The types here mirror the JSON of the HSS; the OAM does not import
// internal/hss.

// Subscription is a subscription as the HSS provisioning API shows it
// (subscriptionResponse of internal/hss): never with a credential.
type Subscription struct {
	PrivateIdentity       string   `json:"private_identity"`
	PublicIdentities      []string `json:"public_identities"`
	AuthenticationSchemes []string `json:"authentication_schemes"`
	RegistrationState     string   `json:"registration_state"`
	ServerName            string   `json:"server_name,omitempty"`
	// TestUser and ServiceProfile are the mark and the profile name of
	// the subscription, absent where it has neither.
	TestUser       bool   `json:"test_user,omitempty"`
	ServiceProfile string `json:"service_profile,omitempty"`
	// CustomizedAlertingTone is the tone that the callers of the user
	// hear while it rings, absent where the operator provisioned none.
	CustomizedAlertingTone *AlertingTone `json:"customized_alerting_tone,omitempty"`
}

// AlertingTone is the customized alerting tone of a subscription (CAT,
// TS 24.182): the announcement that the MRFP plays to the callers of
// the user while it rings, and whether they hear it. The HSS keeps it
// as Sh repository data, which the MMTel AS of the user reads for each
// call to it.
type AlertingTone struct {
	// Tone names an announcement that the MRFPs have provisioned (RFC
	// 4240 3.3), such as cat1.
	Tone string `json:"tone"`
	// Active says whether the callers hear the tone. A tone that is
	// provisioned and not active leaves them their own ringing tone.
	Active bool `json:"active"`
}

// hssSubscriptionRequest is the body of PUT /subscriptions/{impi}: the
// subscriptionRequest of internal/hss, without the
// customized_alerting_tone, which the OAM never provisions on its own
// and carries in the repository data of an archive instead.
type hssSubscriptionRequest struct {
	PrivateIdentity  string       `json:"private_identity"`
	PublicIdentities []string     `json:"public_identities"`
	BarredIdentities []string     `json:"barred_identities,omitempty"`
	Password         SecretString `json:"password,omitempty"`
	AKA              *AKARecord   `json:"aka,omitempty"`
	Tel              string       `json:"tel,omitempty"`
	UtPassword       SecretString `json:"ut_password,omitempty"`
	MMTelServices    string       `json:"mmtel_services,omitempty"`
	// RepositoryData carries no omitempty, so that the HSS is asked for
	// what the record means: an empty array asks for a subscription with
	// no repository data at all, where leaving the field out asks for the
	// MMTelServices document of the HSS configuration. A nil slice goes
	// out as null, which the HSS reads as the field left out.
	RepositoryData []RepositoryDataRecord `json:"repository_data"`
	TestUser       bool                   `json:"test_user,omitempty"`
	ServiceProfile string                 `json:"service_profile,omitempty"`
}

func (r SubscriptionRecord) hssRequest() hssSubscriptionRequest {
	return hssSubscriptionRequest{
		PrivateIdentity:  r.PrivateIdentity,
		PublicIdentities: r.PublicIdentities,
		BarredIdentities: r.BarredIdentities,
		Password:         r.Password,
		AKA:              r.AKA,
		Tel:              r.Tel,
		UtPassword:       r.UtPassword,
		MMTelServices:    r.MMTelServices,
		RepositoryData:   r.RepositoryData,
		TestUser:         r.TestUser,
		ServiceProfile:   r.ServiceProfile,
	}
}

// hssPutAnswer is the answer to PUT /subscriptions/{impi}: what the
// HSS did, or would do on a dry run, and the subscription after it.
// unchanged means that the stored subscription is the request already,
// credentials included, which only the HSS can tell.
type hssPutAnswer struct {
	Result       Result       `json:"result"`
	Subscription Subscription `json:"subscription"`
}

// hssPatch is the body of PATCH /subscriptions/{impi}: the attributes
// that change without the credentials. An absent field stays.
type hssPatch struct {
	TestUser       *bool   `json:"test_user,omitempty"`
	ServiceProfile *string `json:"service_profile,omitempty"`
}

// hssError is an answer of the HSS that is not a success. The HSS
// answers errors as text (http.Error).
type hssError struct {
	StatusCode int
	Message    string
}

func (e *hssError) Error() string {
	return fmt.Sprintf("HSS: %d: %s", e.StatusCode, e.Message)
}

// errNoSubscription is the HSS's 404 for a request about one
// subscription that is there already (GET, PATCH, DELETE), which says
// that the subscription is not there and nothing else: every operation
// here is one that the HSS has. A 404 to a PUT or a POST, which make
// one, or to the export says that what answered is not this API, and
// is an hssError.
var errNoSubscription = errors.New("no such subscription")

type hssClient struct {
	base string
	http *http.Client
}

func subscriptionPath(impi string) string { return "/subscriptions/" + url.PathEscape(impi) }

func (c *hssClient) get(ctx context.Context, impi string) (Subscription, error) {
	var s Subscription
	err := c.do(ctx, http.MethodGet, subscriptionPath(impi), nil, &s)
	return s, err
}

func (c *hssClient) put(ctx context.Context, req hssSubscriptionRequest, dryRun bool) (hssPutAnswer, error) {
	path := subscriptionPath(req.PrivateIdentity)
	if dryRun {
		path += "?dry_run=true"
	}
	var a hssPutAnswer
	err := c.do(ctx, http.MethodPut, path, req, &a)
	return a, err
}

func (c *hssClient) patch(ctx context.Context, impi string, p hssPatch) (Subscription, error) {
	var s Subscription
	err := c.do(ctx, http.MethodPatch, subscriptionPath(impi), p, &s)
	return s, err
}

func (c *hssClient) delete(ctx context.Context, impi string) error {
	return c.do(ctx, http.MethodDelete, subscriptionPath(impi), nil, nil)
}

func (c *hssClient) setAlertingTone(ctx context.Context, impi string, t AlertingTone) (AlertingTone, error) {
	var stored AlertingTone
	err := c.do(ctx, http.MethodPut, subscriptionPath(impi)+"/customized-alerting-tone", t, &stored)
	return stored, err
}

// export reads one page of GET /subscriptions/export and calls emit
// with each record as the HSS wrote it. It returns the private identity
// that the page after this one starts at, and an empty string for the
// last page (nextPage).
func (c *hssClient) export(ctx context.Context, after string, limit int, emit func(json.RawMessage) error) (string, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if after != "" {
		q.Set("after", after)
	}
	resp, err := c.send(ctx, http.MethodGet, "/subscriptions/export?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	next, err := nextPage(resp, after)
	if err != nil {
		return "", err
	}
	n, last := 0, after
	r := bufio.NewReaderSize(resp.Body, maxRecordLine)
	for {
		line, err := readLine(r)
		if errors.Is(err, io.EOF) {
			return next, nil
		}
		if err != nil {
			return "", fmt.Errorf("%w: export: %v", ErrUpstream, err)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var id struct {
			PrivateIdentity string `json:"private_identity"`
		}
		if err := json.Unmarshal(line, &id); err != nil || id.PrivateIdentity == "" {
			return "", fmt.Errorf("%w: export: record %d after %q is not a subscription", ErrUpstream, n+1, last)
		}
		if last != "" && strings.ToLower(id.PrivateIdentity) <= strings.ToLower(last) {
			return "", fmt.Errorf("%w: export: %q does not come after %q", ErrUpstream, id.PrivateIdentity, last)
		}
		if err := emit(json.RawMessage(bytes.Clone(line))); err != nil {
			return "", err
		}
		n++
		last = id.PrivateIdentity
	}
}

// nextPage is the private identity that the page after this answer
// starts at: the "after" of the query that the HSS names in a Link
// header of rel="next" while pages are left (RFC 8288 3). The target is
// a relative reference, read against the URL the page came from (RFC
// 8288 3.1, RFC 3986 5.1.3), so the OAM follows it wherever it reaches
// the API. An answer without the header is the last page and gives an
// empty private identity.
//
// The header is the only end of the export. A page of exactly the limit
// always carries it, so the page that follows can hold no record at all;
// that page is the end and not a failure. A page can also hold fewer
// records than the limit it was asked for, as the HSS leaves out a
// subscription that went while the page was read, so the page that
// follows starts where the HSS says and not after the last record read.
func nextPage(resp *http.Response, after string) (string, error) {
	var links []string
	for _, field := range resp.Header.Values("Link") {
		links = append(links, linkValues(field)...)
	}
	for _, link := range links {
		target, params, ok := strings.Cut(link, ";")
		if !ok || !relNext(params) {
			continue
		}
		target = strings.TrimSpace(target)
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			return "", fmt.Errorf("%w: export: Link: %q has no target between < and >", ErrUpstream, link)
		}
		ref, err := url.Parse(target[1 : len(target)-1])
		if err != nil {
			return "", fmt.Errorf("%w: export: Link: %q: %v", ErrUpstream, target, err)
		}
		next := resp.Request.URL.ResolveReference(ref).Query().Get("after")
		switch {
		case next == "":
			return "", fmt.Errorf("%w: export: Link: %q names no page to come after", ErrUpstream, target)
		case after != "" && strings.ToLower(next) <= strings.ToLower(after):
			// A page that followed itself would be read for ever.
			return "", fmt.Errorf("%w: export: the next page starts at %q, which does not come after %q", ErrUpstream, next, after)
		}
		return next, nil
	}
	return "", nil
}

// linkValues splits one Link field into its links. A field may hold
// several, separated by commas (RFC 8288 3), and a comma inside the
// target or inside a quoted parameter value is not a separator.
func linkValues(field string) []string {
	var links []string
	inTarget, inQuotes, start := false, false, 0
	for i, c := range field {
		switch {
		case inQuotes && c == '"':
			inQuotes = false
		case inQuotes:
		case c == '<':
			inTarget = true
		case c == '>':
			inTarget = false
		case c == '"' && !inTarget:
			inQuotes = true
		case c == ',' && !inTarget:
			links = append(links, strings.TrimSpace(field[start:i]))
			start = i + 1
		}
	}
	links = append(links, strings.TrimSpace(field[start:]))
	return slices.DeleteFunc(links, func(l string) bool { return l == "" })
}

// relNext reports whether the parameters of a link say that its target
// is the next page. The value of rel is a token or a quoted string,
// which are the same thing (RFC 8288 3), and holds one relation type or
// several separated by spaces, compared without case (RFC 8288 2.1.1,
// 3.3).
func relNext(params string) bool {
	for _, param := range strings.Split(params, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
		if strings.EqualFold(strings.TrimSpace(name), "rel") {
			return slices.ContainsFunc(strings.Fields(strings.Trim(strings.TrimSpace(value), `"`)),
				func(t string) bool { return strings.EqualFold(t, "next") })
		}
	}
	return false
}

func (c *hssClient) do(ctx context.Context, method, path string, body, answer any) error {
	resp, err := c.send(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if answer == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(answer); err != nil {
		return fmt.Errorf("%w: %s %s: %v", ErrUpstream, method, path, err)
	}
	return nil
}

// send makes the request and returns a response with a success status,
// or the error: errNoSubscription for a 404 about one subscription,
// hssError for another failure status.
func (c *hssClient) send(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxRequestBody))
	// Only a request about one subscription that is there already can
	// find it missing; the export is of the whole store, and a PUT or a
	// POST of a subscription makes it. The PUT of an alerting tone is
	// about a subscription that is there, so its 404 is the missing
	// subscription like that of a GET.
	aboutOne := strings.HasSuffix(path, "/customized-alerting-tone") ||
		(method != http.MethodPut && method != http.MethodPost && !strings.HasPrefix(path, "/subscriptions/export"))
	if resp.StatusCode == http.StatusNotFound && aboutOne {
		return nil, errNoSubscription
	}
	return nil, &hssError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(b))}
}
