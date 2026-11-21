package sip

import (
	"bytes"
	"testing"

	sipgo "github.com/emiago/sipgo/sip"
)

// sipgo is only an oracle. Its parser is written independently of
// ours, so where both read the same fields from a sample, a mistake in
// one of them shows up as a difference.

func TestParserAgreesWithSipgo(t *testing.T) {
	for name, s := range map[string]string{
		"initial REGISTER":  initialRegister,
		"401 Unauthorized":  unauthorized,
		"200 OK":            registerOK,
		"compact and lists": compactRegister,
	} {
		t.Run(name, func(t *testing.T) {
			ours := mustParse(t, s)
			theirs, err := sipgo.ParseMessage([]byte(s))
			if err != nil {
				t.Fatalf("sipgo: %v", err)
			}
			compareStartLine(t, ours, theirs)

			if got, want := ours.Get("Call-ID"), theirs.CallID().Value(); got != want {
				t.Errorf("Call-ID: ours %q, sipgo %q", got, want)
			}
			cseq, err := ParseCSeq(ours.Get("CSeq"))
			if err != nil {
				t.Fatal(err)
			}
			if cseq.Number != theirs.CSeq().SeqNo || string(cseq.Method) != string(theirs.CSeq().MethodName) {
				t.Errorf("CSeq: ours %v, sipgo %v", cseq, theirs.CSeq())
			}
			compareVias(t, ours.GetAll("Via"), theirs.GetHeaders("Via"))
			compareAddress(t, "From", ours.Get("From"), theirs.From().DisplayName, theirs.From().Address, theirs.From().Params)
			compareAddress(t, "To", ours.Get("To"), theirs.To().DisplayName, theirs.To().Address, theirs.To().Params)
			compareContacts(t, ours.GetAll("Contact"), theirs.GetHeaders("Contact"))

			// sipgo keeps these as raw rows; parse each of our list
			// elements with its address parser instead.
			for _, header := range []string{"Path", "Service-Route", "P-Associated-URI"} {
				for _, value := range ours.GetAll(header) {
					var uri sipgo.Uri
					var params sipgo.HeaderParams
					displayName, err := sipgo.ParseAddressValue(value, &uri, &params)
					if err != nil {
						t.Errorf("%s %q: sipgo: %v", header, value, err)
						continue
					}
					compareAddress(t, header, value, displayName, uri, params)
				}
			}
			for _, header := range []string{"Authorization", "WWW-Authenticate"} {
				o, h := ours.GetAll(header), theirs.GetHeaders(header)
				if len(o) != len(h) {
					t.Errorf("%s: ours %d rows, sipgo %d", header, len(o), len(h))
					continue
				}
				for i := range o {
					if o[i] != h[i].Value() {
						t.Errorf("%s: ours %q, sipgo %q", header, o[i], h[i].Value())
					}
				}
			}
			if !bytes.Equal(ours.Body, theirs.Body()) {
				t.Errorf("body: ours %q, sipgo %q", ours.Body, theirs.Body())
			}
		})
	}
}

func compareStartLine(t *testing.T, ours *Message, theirs sipgo.Message) {
	t.Helper()
	switch m := theirs.(type) {
	case *sipgo.Request:
		if string(ours.Method) != string(m.Method) {
			t.Errorf("method: ours %q, sipgo %q", ours.Method, m.Method)
		}
		uri, err := ParseURI(ours.RequestURI)
		if err != nil {
			t.Fatal(err)
		}
		compareURI(t, "Request-URI", uri, m.Recipient)
	case *sipgo.Response:
		if ours.StatusCode != m.StatusCode || ours.Reason != m.Reason {
			t.Errorf("status: ours %d %q, sipgo %d %q", ours.StatusCode, ours.Reason, m.StatusCode, m.Reason)
		}
	default:
		t.Fatalf("sipgo returned a %T", theirs)
	}
}

func compareVias(t *testing.T, ours []string, theirs []sipgo.Header) {
	t.Helper()
	if len(ours) != len(theirs) {
		t.Fatalf("Via: ours %d values, sipgo %d", len(ours), len(theirs))
	}
	for i, value := range ours {
		v, err := ParseVia(value)
		if err != nil {
			t.Fatal(err)
		}
		h := theirs[i].(*sipgo.ViaHeader)
		if v.Transport != h.Transport || v.Host != h.Host || v.Port != h.Port {
			t.Errorf("Via %d: ours %s %s %d, sipgo %s %s %d", i, v.Transport, v.Host, v.Port, h.Transport, h.Host, h.Port)
		}
		compareParams(t, "Via", v.Params, h.Params)
	}
}

func compareContacts(t *testing.T, ours []string, theirs []sipgo.Header) {
	t.Helper()
	if len(ours) != len(theirs) {
		t.Fatalf("Contact: ours %d values, sipgo %d", len(ours), len(theirs))
	}
	for i, value := range ours {
		h := theirs[i].(*sipgo.ContactHeader)
		compareAddress(t, "Contact", value, h.DisplayName, h.Address, h.Params)
	}
}

func compareAddress(t *testing.T, header, ours, displayName string, uri sipgo.Uri, params sipgo.HeaderParams) {
	t.Helper()
	na, err := ParseNameAddr(ours)
	if err != nil {
		t.Fatal(err)
	}
	if na.DisplayName != displayName {
		t.Errorf("%s display name: ours %q, sipgo %q", header, na.DisplayName, displayName)
	}
	if u, err := ParseURI(na.URI); err == nil {
		compareURI(t, header, u, uri)
	} else if na.URI != uri.Scheme+":"+uri.Raw() {
		t.Errorf("%s URI: ours %q, sipgo %s:%s", header, na.URI, uri.Scheme, uri.Raw())
	}
	compareParams(t, header, na.Params, params)
}

func compareURI(t *testing.T, where string, ours URI, theirs sipgo.Uri) {
	t.Helper()
	if ours.Scheme != theirs.Scheme || ours.User != theirs.User || ours.Host != theirs.Host || ours.Port != theirs.Port {
		t.Errorf("%s URI: ours %s %q %q %d, sipgo %s %q %q %d", where,
			ours.Scheme, ours.User, ours.Host, ours.Port, theirs.Scheme, theirs.User, theirs.Host, theirs.Port)
	}
	compareParams(t, where+" URI", ours.Params, theirs.UriParams)
}

func compareParams(t *testing.T, where string, ours Params, theirs sipgo.HeaderParams) {
	t.Helper()
	if len(ours) != len(theirs) {
		t.Errorf("%s parameters: ours %v, sipgo %v", where, ours, theirs)
		return
	}
	for _, p := range ours {
		if v, ok := theirs.Get(p.Name); !ok || v != p.Value {
			t.Errorf("%s parameter %s: ours %q, sipgo %q (present %v)", where, p.Name, p.Value, v, ok)
		}
	}
}
