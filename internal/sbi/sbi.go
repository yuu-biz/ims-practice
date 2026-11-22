// Package sbi is the part of the service based interface that every
// API here shares: HTTP/2 without TLS (TS 29.500 5.1 to 5.4), JSON
// bodies, and the error answers with ProblemDetails (TS 29.571
// 5.2.4.1) and the application errors of TS 29.500 Table 5.2.7.2-1.
// No node of this project runs it: no binary in cmd builds it in.
//
// The packages of the APIs (n5 for TS 29.514, nhss for TS 29.562) keep
// what differs between them: their resources and JSON types, their
// application errors and the status each is answered with, the NF type
// each sender names in User-Agent, and the names of their callbacks.
// Each describes itself once with an API value and calls its methods.
//
// SIMPLIFIED: TS 29.500 5.1. Every NF supports TLS and uses it inside
// a PLMN unless the network is protected by other means; here the SBI
// runs over cleartext HTTP/2 (h2c with prior knowledge, RFC 9113 3.3),
// because the project defaults to cleartext so that a capture shows
// the messages. There are no OAuth 2.0 access tokens either;
// the security sections of the OpenAPI files of TS 29.514 and TS 29.562
// also allow a request without them. There is no NRF: the caller is
// given the apiRoot of its peer.
package sbi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

// The media types every API here uses for its bodies (each API lists
// them in its own clause, such as TS 29.514 5.2.2.2 and TS 29.562
// 6.1.2.2.2): JSON (RFC 8259) and Problem Details (RFC 9457).
const (
	ContentTypeJSON    = "application/json"
	ContentTypeProblem = "application/problem+json"
)

// MaxBodySize bounds the JSON a handler reads. TS 29.500 Table
// 5.2.7.2-1 answers a larger one with 413 MAX_JSON_SIZE_EXCEEDED.
const MaxBodySize = 1 << 20

// h2cProtocols is HTTP/2 over TCP without TLS and nothing else. A
// client then speaks HTTP/2 from its first octet; a server refuses an
// HTTP/1.1 client, so an SBI peer never falls back to HTTP/1.1 (TS
// 29.500 5.2.1 requires HTTP/2).
func h2cProtocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return p
}

// NewServer returns a server that serves h on addr with cleartext
// HTTP/2 only. TS 29.500 5.3 makes TCP port 80 the default for an
// "http" apiRoot, so addr is usually ":80".
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, Protocols: h2cProtocols()}
}

// NewHTTPClient returns a client that speaks cleartext HTTP/2 to http
// URIs. It does not follow redirects: a 3xx answer comes back to the
// caller, which for a 303 to an N5 Create is the context that exists
// already (TS 29.514 4.2.2.2).
//
// SIMPLIFIED: TS 29.500 6.10.9. Redirection with 307 and 308 to
// another instance of the service is not followed; it comes back as an
// error.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Protocols: h2cProtocols()},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Request is one request of an NF service consumer, or a notification
// of an NF service producer.
type Request struct {
	Method string
	URI    string
	// Body is encoded as JSON with ContentType, unless it is nil.
	Body        any
	ContentType string
	// UserAgent names the NF type of the sender (TS 29.500 5.2.2.2).
	UserAgent string
	// Callback, when not empty, is the 3gpp-Sbi-Callback header of a
	// notification (TS 29.500 5.2.3.2.3).
	Callback string
}

// Response is an answer the client has read to the end.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Send makes one request. An answer that is not 2xx (nor 303, when
// the API answers with it) comes back as a *ProblemError of the API
// with the Response it was read from.
func (a API[B, PB]) Send(ctx context.Context, client *http.Client, req Request) (Response, error) {
	var reader io.Reader
	if req.Body != nil {
		b, err := json.Marshal(req.Body)
		if err != nil {
			return Response{}, err
		}
		reader = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URI, reader)
	if err != nil {
		return Response{}, err
	}
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", req.ContentType)
	}
	httpReq.Header.Set("Accept", ContentTypeJSON+", "+ContentTypeProblem)
	httpReq.Header.Set("User-Agent", req.UserAgent)
	if req.Callback != "" {
		httpReq.Header.Set("3gpp-Sbi-Callback", req.Callback)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodySize))
	if err != nil {
		return Response{}, err
	}
	r := Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
	if (r.Status >= 200 && r.Status < 300) || (a.AnswersSeeOther && r.Status == http.StatusSeeOther) {
		return r, nil
	}
	return r, a.problemFrom(r)
}

// problemFrom reads the ProblemDetails of an error answer. A body that
// is not one still gives the status.
func (a API[B, PB]) problemFrom(r Response) *ProblemError[B, PB] {
	e := &ProblemError[B, PB]{Status: r.Status, apiName: a.Name}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == ContentTypeProblem && json.Unmarshal(r.Body, &e.Problem) == nil {
		return e
	}
	e = &ProblemError[B, PB]{Status: r.Status, apiName: a.Name}
	*e.details() = ProblemDetails{Status: r.Status, Detail: string(r.Body)}
	return e
}

// ReadJSON decodes the body of r into v after checking that its media
// type is contentType. The error is the answer to send.
func (a API[B, PB]) ReadJSON(w http.ResponseWriter, r *http.Request, contentType string, v any) *ProblemError[B, PB] {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != contentType {
		e := &ProblemError[B, PB]{Status: http.StatusUnsupportedMediaType, apiName: a.Name}
		*e.details() = ProblemDetails{
			Status: http.StatusUnsupportedMediaType,
			Detail: fmt.Sprintf("the body must be %s", contentType),
		}
		return e
	}
	err = json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodySize)).Decode(v)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return a.NewProblem(CauseMaxJSONSizeExceeded, "")
	case err != nil:
		return a.NewProblem(CauseInvalidMsgFormat, err.Error())
	}
	return nil
}

// Missing is the 400 for mandatory attributes that are absent. Each
// name is a JSON pointer into the body (TS 29.571 5.2.4.6).
func (a API[B, PB]) Missing(pointers ...string) *ProblemError[B, PB] {
	p := a.NewProblem(CauseMandatoryIEMissing, "")
	d := p.details()
	for _, ptr := range pointers {
		d.InvalidParams = append(d.InvalidParams, InvalidParam{Param: ptr, Reason: "missing"})
	}
	return p
}

// WriteJSON answers with status and v as JSON.
func (a API[B, PB]) WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		a.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	w.Write(b)
}

// WriteError answers with a *ProblemError of the API as it is, and
// with 500 SYSTEM_FAILURE for any other error (TS 29.500 Table
// 5.2.7.2-1). The status in the body is always that of the answer.
func (a API[B, PB]) WriteError(w http.ResponseWriter, err error) {
	var p *ProblemError[B, PB]
	if !errors.As(err, &p) {
		p = a.NewProblem(CauseSystemFailure, err.Error())
	}
	problem := p.Problem
	PB(&problem).problemDetails().Status = p.Status
	b, _ := json.Marshal(problem)
	w.Header().Set("Content-Type", ContentTypeProblem)
	w.WriteHeader(p.Status)
	w.Write(b)
}
