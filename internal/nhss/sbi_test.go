package nhss

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// serveH2C serves h with NewServer on a loopback port and returns its
// apiRoot. Every request that reaches h is checked to have come as
// HTTP/2.
func serveH2C(t *testing.T, h http.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("%s %s came as %s, want HTTP/2", r.Method, r.URL.Path, r.Proto)
		}
		h.ServeHTTP(w, r)
	}))
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return "http://" + l.Addr().String()
}

func contextWithTimeout(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// roundTrip decodes body into a value of type T, encodes it again and
// checks that the JSON says the same: every attribute of the example
// has its field, with the name the OpenAPI gives it.
func roundTrip[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	again, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip changed the body\nwant %s\n got %s", body, again)
	}
	return v
}

// request sends a raw request over h2c and reads the answer.
func request(t *testing.T, method, uri, contentType, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, uri, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := sbi.NewHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

// problemOf checks that an error answer is Problem Details and reads it.
func problemOf(t *testing.T, header http.Header, body []byte) ExtendedProblemDetails {
	t.Helper()
	if ct := header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("error answer as %q", ct)
	}
	var p ExtendedProblemDetails
	if err := json.Unmarshal(body, &p); err != nil {
		t.Errorf("error body %q: %v", body, err)
	}
	return p
}

func params(p ExtendedProblemDetails) string {
	var s []string
	for _, ip := range p.InvalidParams {
		s = append(s, ip.Param)
	}
	return strings.Join(s, " ")
}

func TestImsUeId(t *testing.T) {
	id := ImsUeIdFromImpu("sip:alice@ims.example.org")
	if id != "impu-sip:alice@ims.example.org" {
		t.Errorf("got %q", id)
	}
	if impu, ok := id.Impu(); !ok || impu != "sip:alice@ims.example.org" {
		t.Errorf("Impu() = %q, %v", impu, ok)
	}
	if _, ok := id.Impi(); ok {
		t.Error("an IMPU is also an IMPI")
	}
	id = ImsUeIdFromImpi("001010000000001@ims.mnc001.mcc001.3gppnetwork.org")
	if impi, ok := id.Impi(); !ok || impi != "001010000000001@ims.mnc001.mcc001.3gppnetwork.org" {
		t.Errorf("Impi() = %q, %v", impi, ok)
	}
	if got := segment(string(ImsUeIdFromImpu("tel:+12025550123"))); got != "impu-tel:+12025550123" {
		t.Errorf("segment %q", got)
	}
}

func TestNewProblem(t *testing.T) {
	cases := map[string]int{
		CauseUserNotFound:                       404,
		CauseIdentityAlreadyRegistered:          403,
		CauseIdentityNotRegistered:              404,
		CauseUnsupportedSIPAuthenticationScheme: 501,
		CauseDataNotFound:                       404,
		"SOMETHING_ELSE":                        500,
	}
	for cause, status := range cases {
		p := NewProblem(cause, "")
		if p.Status != status || p.Problem.Status != status || p.Problem.Cause != cause {
			t.Errorf("NewProblem(%s) = %+v", cause, p)
		}
	}
}
