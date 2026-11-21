package sip

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Message {
	t.Helper()
	m, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func TestParseRequestLineAndHeaders(t *testing.T) {
	m := mustParse(t, initialRegister)
	if !m.IsRequest() || m.Method != REGISTER || m.RequestURI != "sip:"+imsDomain {
		t.Fatalf("start line = %q %q, want REGISTER sip:%s", m.Method, m.RequestURI, imsDomain)
	}
	if got := m.Get("call-id"); got != "apb03a0s09dkjdfglkj49111" {
		t.Errorf(`Get("call-id") = %q`, got)
	}
	if got := m.Get("Authorization"); got != initialRegisterAuthorization {
		t.Errorf("Authorization = %q, want it unsplit and as received", got)
	}
	if len(m.Body) != 0 {
		t.Errorf("Body = %q, want empty", m.Body)
	}
}

func TestParseStatusLine(t *testing.T) {
	m := mustParse(t, unauthorized)
	if m.IsRequest() || m.StatusCode != 401 || m.Reason != "Unauthorized" {
		t.Fatalf("status line = %d %q, want 401 Unauthorized", m.StatusCode, m.Reason)
	}
}

func TestBytesReproducesAWellFormedMessage(t *testing.T) {
	for name, s := range map[string]string{
		"initial REGISTER": initialRegister,
		"401":              unauthorized,
		"200":              registerOK,
	} {
		t.Run(name, func(t *testing.T) {
			if got := string(mustParse(t, s).Bytes()); got != s {
				t.Errorf("Bytes() =\n%s\nwant\n%s", got, s)
			}
		})
	}
}

func TestCompactFormsAreExpanded(t *testing.T) {
	m := mustParse(t, compactRegister)
	for _, h := range m.Headers {
		if len(h.Name) == 1 {
			t.Errorf("header %q kept its compact name", h.Name)
		}
	}
	if got := m.Get("Call-ID"); got != "cmp9f00a1" {
		t.Errorf("Call-ID = %q", got)
	}
	// A compact name also works for lookups.
	if got := m.Get("i"); got != "cmp9f00a1" {
		t.Errorf(`Get("i") = %q`, got)
	}
	if got := m.Get("Subject"); got != "registration in progress" {
		t.Errorf("folded Subject = %q, want the continuation joined by one space", got)
	}
}

func TestGetAllSplitsListsAcrossRows(t *testing.T) {
	m := mustParse(t, compactRegister)
	want := []string{
		"SIP/2.0/UDP pcscf.ims.mnc001.mcc001.3gppnetwork.org;branch=z9hG4bKp1",
		"SIP/2.0/UDP 198.51.100.7:5070;branch=z9hG4bKp2",
		"SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bKue;received=203.0.113.9;rport=40000",
	}
	if got := m.GetAll("Via"); !slices.Equal(got, want) {
		t.Errorf("GetAll(Via) =\n%q\nwant\n%q", got, want)
	}
	if got := m.Get("Via"); got != want[0] {
		t.Errorf("Get(Via) = %q, want the top value", got)
	}
	contacts := m.GetAll("Contact")
	if len(contacts) != 1 {
		t.Errorf("GetAll(Contact) = %q, want one value: the comma is inside the display name", contacts)
	}
}

// TestPAssertedIdentityIsAList is RFC 3325 9.1: a SIP URI and a tel URI
// may share one row.
func TestPAssertedIdentityIsAList(t *testing.T) {
	m := &Message{Method: MESSAGE, RequestURI: "sip:sc@" + imsDomain}
	m.Add("P-Asserted-Identity", `"Alice" <sip:001010000000001@`+imsDomain+`>, <tel:+12025550101>`)
	want := []string{`"Alice" <sip:001010000000001@` + imsDomain + `>`, "<tel:+12025550101>"}
	if got := m.GetAll("P-Asserted-Identity"); !slices.Equal(got, want) {
		t.Errorf("GetAll(P-Asserted-Identity) = %q, want %q", got, want)
	}
}

func TestGetAllKeepsNonListHeadersWhole(t *testing.T) {
	m := &Message{Method: REGISTER, RequestURI: "sip:" + imsDomain}
	m.Add("Authorization", `Digest username="a", realm="r", nonce="", uri="sip:r", response=""`)
	m.Add("Authorization", `Digest username="b", realm="s", nonce="", uri="sip:s", response=""`)
	m.Add("Date", "Sat, 13 Nov 2010 23:29:00 GMT")
	m.Add("Route", "<sip:a.example;lr>, <sip:b.example;lr?x=1,2>")
	if got := m.GetAll("Authorization"); len(got) != 2 {
		t.Errorf("GetAll(Authorization) = %q, want 2 rows unsplit", got)
	}
	if got := m.GetAll("Date"); len(got) != 1 {
		t.Errorf("GetAll(Date) = %q, want one value", got)
	}
	want := []string{"<sip:a.example;lr>", "<sip:b.example;lr?x=1,2>"}
	if got := m.GetAll("Route"); !slices.Equal(got, want) {
		t.Errorf("GetAll(Route) = %q, want %q", got, want)
	}
}

func TestHeaderEditing(t *testing.T) {
	m := mustParse(t, compactRegister)

	m.Prepend("Via", "SIP/2.0/UDP 198.51.100.1:5060;branch=z9hG4bKnew")
	if got := m.Get("Via"); got != "SIP/2.0/UDP 198.51.100.1:5060;branch=z9hG4bKnew" {
		t.Errorf("after Prepend, top Via = %q", got)
	}
	if len(m.GetAll("Via")) != 4 {
		t.Errorf("after Prepend, %d Via values, want 4", len(m.GetAll("Via")))
	}

	m.Remove("Via")
	m.Add("Via", "SIP/2.0/UDP a.example;branch=z9hG4bK1, SIP/2.0/UDP b.example;branch=z9hG4bK2")
	m.SetFirst("Via", "SIP/2.0/UDP a.example;branch=z9hG4bK1;received=192.0.2.1")
	want := []string{"SIP/2.0/UDP a.example;branch=z9hG4bK1;received=192.0.2.1", "SIP/2.0/UDP b.example;branch=z9hG4bK2"}
	if got := m.GetAll("Via"); !slices.Equal(got, want) {
		t.Errorf("after SetFirst, Via = %q, want %q", got, want)
	}

	m.Add("Supported", "path")
	m.Add("Supported", "gruu")
	m.Set("supported", "outbound")
	if got := m.GetAll("Supported"); !slices.Equal(got, []string{"outbound"}) {
		t.Errorf("after Set, Supported = %q", got)
	}

	m.Remove("V")
	if got := m.GetAll("Via"); got != nil {
		t.Errorf("after Remove, Via = %q", got)
	}
}

func TestBytesWritesContentLengthFromBody(t *testing.T) {
	m := &Message{Method: "MESSAGE", RequestURI: "sip:" + imsDomain, Body: []byte("hello")}
	m.Add("Call-ID", "x")
	if !strings.Contains(string(m.Bytes()), "\r\nContent-Length: 5\r\n\r\nhello") {
		t.Errorf("missing Content-Length not added:\n%s", m.Bytes())
	}
	m.Add("Content-Length", "99")
	m.Add("Subject", "after")
	want := "MESSAGE sip:" + imsDomain + " SIP/2.0\r\nCall-ID: x\r\nContent-Length: 5\r\nSubject: after\r\n\r\nhello"
	if got := string(m.Bytes()); got != want {
		t.Errorf("wrong Content-Length not rewritten in place:\n%q\nwant\n%q", got, want)
	}
}

func TestParseFraming(t *testing.T) {
	head := "MESSAGE sip:" + imsDomain + " SIP/2.0\r\nCall-ID: x\r\n"
	t.Run("bytes after Content-Length are discarded", func(t *testing.T) {
		m := mustParse(t, head+"Content-Length: 3\r\n\r\nabcdef")
		if string(m.Body) != "abc" {
			t.Errorf("Body = %q, want abc", m.Body)
		}
	})
	t.Run("short body is an error", func(t *testing.T) {
		_, err := Parse([]byte(head + "Content-Length: 10\r\n\r\nabc"))
		if !errors.Is(err, ErrTruncatedBody) {
			t.Errorf("err = %v, want ErrTruncatedBody", err)
		}
	})
	t.Run("without Content-Length the body runs to the end", func(t *testing.T) {
		m := mustParse(t, head+"\r\nabcdef")
		if string(m.Body) != "abcdef" {
			t.Errorf("Body = %q", m.Body)
		}
	})
	t.Run("leading CRLFs are ignored", func(t *testing.T) {
		m := mustParse(t, "\r\n\r\n"+head+"\r\n")
		if m.Method != "MESSAGE" {
			t.Errorf("Method = %q", m.Method)
		}
	})
}

func TestParseRejectsMalformedMessages(t *testing.T) {
	for name, s := range map[string]string{
		"no empty line":      "REGISTER sip:a SIP/2.0\r\nCall-ID: x\r\n",
		"bad version":        "REGISTER sip:a SIP/3.0\r\n\r\n",
		"bad status code":    "SIP/2.0 2000 OK\r\n\r\n",
		"header without :":   "REGISTER sip:a SIP/2.0\r\nCall-ID x\r\n\r\n",
		"leading fold":       "REGISTER sip:a SIP/2.0\r\n folded\r\n\r\n",
		"bad Content-Length": "REGISTER sip:a SIP/2.0\r\nContent-Length: x\r\n\r\n",
	} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("%s: Parse succeeded", name)
		}
	}
}

func TestNewResponse(t *testing.T) {
	req := mustParse(t, compactRegister)
	req.Add("Timestamp", "54")

	resp := NewResponse(req, 401, "srv1")
	if resp.IsRequest() || resp.StatusCode != 401 || resp.Reason != "Unauthorized" {
		t.Fatalf("status line = %d %q", resp.StatusCode, resp.Reason)
	}
	if !slices.Equal(resp.GetAll("Via"), req.GetAll("Via")) {
		t.Errorf("Via = %q, want the request's Via values in order", resp.GetAll("Via"))
	}
	for _, name := range []string{"From", "Call-ID", "CSeq"} {
		if resp.Get(name) != req.Get(name) {
			t.Errorf("%s = %q, want %q", name, resp.Get(name), req.Get(name))
		}
	}
	if got := resp.Get("To"); got != "<sip:001010000000001@"+imsDomain+">;tag=srv1" {
		t.Errorf("To = %q, want the request's To with the tag added", got)
	}
	for _, name := range []string{"Contact", "Max-Forwards", "Subject", "Timestamp"} {
		if resp.Get(name) != "" {
			t.Errorf("%s copied into the response", name)
		}
	}

	trying := NewResponse(req, 100, "")
	if trying.Get("To") != req.Get("To") || trying.Get("Timestamp") != "54" {
		t.Errorf("100 Trying: To = %q, Timestamp = %q", trying.Get("To"), trying.Get("Timestamp"))
	}

	tagged := mustParse(t, registerOK)
	tagged.StatusCode, tagged.Method, tagged.RequestURI = 0, REGISTER, "sip:"+imsDomain
	if got := NewResponse(tagged, 200, "other").Get("To"); got != tagged.Get("To") {
		t.Errorf("To = %q, want the request's tag kept", got)
	}
}

// The codes outside RFC 3261 that the nodes send carry their reason
// phrases too, which is what Wireshark and the dashboard show.
func TestNewResponseReasonPhrasesOfLaterRFCs(t *testing.T) {
	req := mustParse(t, compactRegister)
	for code, want := range map[int]string{202: "SIP/2.0 202 Accepted\r\n", 489: "SIP/2.0 489 Bad Event\r\n"} {
		if got := string(NewResponse(req, code, "").Bytes()); !strings.HasPrefix(got, want) {
			t.Errorf("%d starts %q, want %q", code, strings.SplitAfter(got, "\r\n")[0], want)
		}
	}
}
