package xcap

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Client is the XCAP client of a UE (TS 24.623 5.3.1): it reads and
// changes the simservs document of one user and authenticates with
// HTTP Digest (RFC 7616).
type Client struct {
	// Root is the XCAP Root URI, such as
	// http://xcap.ims.mnc001.mcc001.3gppnetwork.org, the form TS 23.003
	// 13.9.1.2 gives when none is configured (GSMA IR.92 2.3.2).
	Root string
	// XUI is the XCAP User Identifier, the user's default public
	// identity (GSMA IR.92 2.3.2).
	XUI string
	// Username and Password answer the Digest challenge.
	Username, Password string
	// UserAgent goes into every request, as GSMA IR.92 2.6 asks.
	UserAgent string
	// HTTP sends the requests; nil means http.DefaultClient.
	HTTP *http.Client

	mu sync.Mutex
	// session is the last challenge, which the client answers in later
	// requests too, counting nc up, without waiting for a 401 each time
	// (RFC 7616 3.6).
	session *digestSession
}

type digestSession struct {
	challenge sip.Challenge
	algorithm Algorithm
	nc        uint32
}

// Selector names a node of the document: a node selector (RFC 4825
// 6.3) and the namespaces of its prefixes, which go into the query
// component (6.4). The zero Selector names the whole document.
type Selector struct {
	Path string
	// Namespaces bind the prefixes of Path, such as "cp" to
	// simservs.NamespaceCommonPolicy.
	Namespaces map[string]string
}

// Response is a successful answer of the server.
type Response struct {
	Status      int
	ETag        string
	ContentType string
	Body        []byte
}

// StatusError is an answer that did not succeed. For 409 the Body is
// the conflict report of RFC 4825 11.
type StatusError struct {
	Method, URI string
	Status      int
	ContentType string
	Body        []byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("xcap: %s %s: %d %s: %s", e.Method, e.URI, e.Status, http.StatusText(e.Status), strings.TrimSpace(string(e.Body)))
}

// Get reads the document, or the node sel names (RFC 4825 7.3, 7.6,
// 7.9).
func (c *Client) Get(ctx context.Context, sel Selector) (Response, error) {
	return c.do(ctx, http.MethodGet, sel, nil, "", "")
}

// Put replaces the document, or creates or replaces the node sel names
// (RFC 4825 7.1, 7.4, 7.7), if its entity tag is still ifMatch (7.11).
// GSMA IR.92 2.3.2 has the UE always send it; empty sends none.
func (c *Client) Put(ctx context.Context, sel Selector, body []byte, ifMatch string) (Response, error) {
	contentType := simservs.MIMEType
	switch {
	case sel.Path == "":
	case attributeSelector(sel.Path):
		contentType = simservs.AttributeMIMEType
	default:
		contentType = simservs.ElementMIMEType
	}
	return c.do(ctx, http.MethodPut, sel, body, contentType, ifMatch)
}

// Delete removes the node sel names (RFC 4825 7.5, 7.8), if the entity
// tag is still ifMatch.
func (c *Client) Delete(ctx context.Context, sel Selector, ifMatch string) (Response, error) {
	return c.do(ctx, http.MethodDelete, sel, nil, "", ifMatch)
}

func attributeSelector(path string) bool {
	i := strings.LastIndex(path, "/")
	return strings.HasPrefix(path[i+1:], "@")
}

// URI is the XCAP URI of sel in the user's document (RFC 4825 6): the
// XCAP root, the document selector with the XUI percent-encoded (6.2),
// and the node selector percent-encoded step by step, so that its
// slashes stay.
func (c *Client) URI(sel Selector) string {
	u := strings.TrimSuffix(c.Root, "/") + "/" + simservs.AUID + "/users/" + url.PathEscape(c.XUI) + "/" + simservs.DocumentName
	if sel.Path == "" {
		return u
	}
	steps := strings.Split(sel.Path, "/")
	for i, s := range steps {
		steps[i] = url.PathEscape(s)
	}
	u += "/~~/" + strings.Join(steps, "/")
	if len(sel.Namespaces) > 0 {
		prefixes := make([]string, 0, len(sel.Namespaces))
		for p := range sel.Namespaces {
			prefixes = append(prefixes, p)
		}
		slices.Sort(prefixes)
		u += "?"
		for _, p := range prefixes {
			u += "xmlns(" + p + "=" + sel.Namespaces[p] + ")"
		}
	}
	return u
}

func (c *Client) do(ctx context.Context, method string, sel Selector, body []byte, contentType, ifMatch string) (Response, error) {
	uri := c.URI(sel)
	// A 401 is answered once. A second 401 is answered too when it
	// offers another realm or algorithm than the one the client has
	// just used: the first challenge of a server may name no user, and
	// the next one that of the user's credential. A third is the answer.
	for answered := 0; ; {
		req, err := http.NewRequestWithContext(ctx, method, uri, bytes.NewReader(body))
		if err != nil {
			return Response{}, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		creds, authorized := c.authorization(req)
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return Response{}, err
		}
		got, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if err != nil {
			return Response{}, err
		}
		if resp.StatusCode == http.StatusUnauthorized && answered < 2 {
			changed, err := c.answerChallenge(resp.Header.Values("WWW-Authenticate"))
			if err != nil {
				return Response{}, err
			}
			if answered++; answered == 1 || changed {
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return Response{}, &StatusError{Method: method, URI: uri, Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: got}
		}
		if authorized {
			if err := c.checkAuthenticationInfo(resp.Header.Get("Authentication-Info"), creds); err != nil {
				return Response{}, err
			}
		}
		return Response{Status: resp.StatusCode, ETag: resp.Header.Get("ETag"), ContentType: resp.Header.Get("Content-Type"), Body: got}, nil
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// answerChallenge takes the first challenge of a 401 whose algorithm
// the client supports: the server lists them most preferred first (RFC
// 7616 3.7). changed tells that its realm or its algorithm is not that
// of the challenge the client answered before.
func (c *Client) answerChallenge(values []string) (changed bool, _ error) {
	for _, v := range values {
		ch, err := sip.ParseChallenge(v)
		if err != nil {
			continue
		}
		a := Algorithm(ch.Algorithm)
		if a == "" {
			a = MD5
		}
		if (a != SHA256 && a != MD5) || !slices.Contains(ch.QOP, qopAuth) {
			continue
		}
		c.mu.Lock()
		before := c.session
		c.session = &digestSession{challenge: ch, algorithm: a}
		c.mu.Unlock()
		return before == nil || before.challenge.Realm != ch.Realm || before.algorithm != a, nil
	}
	return false, errors.New("xcap: no Digest challenge with SHA-256 or MD5 and qop auth")
}

// authorization adds the Authorization header field for the challenge
// of the session, if there is one (RFC 7616 3.4).
func (c *Client) authorization(req *http.Request) (sip.Credentials, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.session
	if s == nil {
		return sip.Credentials{}, false
	}
	s.nc++
	var b [16]byte
	cryptorand.Read(b[:])
	creds := sip.Credentials{
		Username:  c.Username,
		Realm:     s.challenge.Realm,
		Nonce:     s.challenge.Nonce,
		URI:       req.URL.RequestURI(),
		Algorithm: string(s.algorithm),
		CNonce:    base64.StdEncoding.EncodeToString(b[:]),
		Opaque:    s.challenge.Opaque,
		QOP:       qopAuth,
		NC:        fmt.Sprintf("%08x", s.nc),
	}
	ha1 := s.algorithm.h(c.Username + ":" + creds.Realm + ":" + c.Password)
	creds.Response = response(s.algorithm, ha1, req.Method, creds)
	req.Header.Set("Authorization", creds.String())
	return creds, true
}

// checkAuthenticationInfo checks the rspauth of the server, by which
// it proves it knows H(A1) too (RFC 7616 3.5). A server that sends no
// Authentication-Info is taken as it is.
func (c *Client) checkAuthenticationInfo(value string, creds sip.Credentials) error {
	if value == "" {
		return nil
	}
	info, err := sip.ParseCredentials("Digest " + value)
	if err != nil {
		return fmt.Errorf("xcap: Authentication-Info: %w", err)
	}
	var rspauth string
	for _, p := range info.AuthParams {
		if strings.EqualFold(p.Name, "rspauth") {
			rspauth = sip.Unquote(p.Value)
		}
	}
	a := Algorithm(creds.Algorithm)
	ha1 := a.h(c.Username + ":" + creds.Realm + ":" + c.Password)
	if rspauth != response(a, ha1, "", creds) {
		return errors.New("xcap: the rspauth of the server is wrong")
	}
	return nil
}
