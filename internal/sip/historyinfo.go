package sip

import (
	"fmt"
	"net/url"
	"strings"
)

// This file holds the History-Info header field (RFC 7044), which
// records where a request was sent before it arrived: a diverted call
// tells its callee who diverted it, and why. An entry may carry a
// Reason and a Privacy value of its own. They travel as header fields
// escaped inside the entry's URI (RFC 3261 19.1.1), so the helpers for
// the headers part of a URI are here too.

// HistoryInfoEntry is one hi-entry of History-Info (RFC 7044 5).
//
//	History-Info: <sip:bob@example.com?Reason=SIP%3Bcause%3D302>;index=1,
//	              <sip:carol@example.com;cause=302>;index=1.1;mp=1
type HistoryInfoEntry struct {
	// DisplayName is the display name of the name-addr, "" when it has
	// none.
	DisplayName string
	// URI is the hi-targeted-to-uri as written, with its headers part.
	// Target, Reasons and Privacy read it.
	URI string
	// Index is the hi-index, such as "1.1": numbers separated by dots,
	// which place the entry in the tree of forwarding hops and
	// retargetings.
	Index string
	// Params are the other parameters, such as the mp, rc or np that
	// names the index of the entry this one was retargeted from.
	Params Params
}

// NewHistoryInfoEntry builds an entry for target, a URI without a
// headers part. The reasons and the privacy go into the URI as escaped
// header fields; a tel URI cannot carry them (RFC 7044 5), so they are
// left out for one.
func NewHistoryInfoEntry(target, index string, reasons []Reason, privacy Privacy) HistoryInfoEntry {
	uri := target
	if !strings.HasPrefix(strings.ToLower(target), "tel:") {
		if len(privacy) > 0 {
			uri = withURIHeader(uri, "Privacy", privacy.String())
		}
		for _, r := range reasons {
			uri = withURIHeader(uri, "Reason", r.String())
		}
	}
	return HistoryInfoEntry{URI: uri, Index: index}
}

// ParseHistoryInfo reads the entries of History-Info in order, as
// Message.GetAll("History-Info") gives them.
func ParseHistoryInfo(values []string) ([]HistoryInfoEntry, error) {
	var entries []HistoryInfoEntry
	for _, v := range values {
		na, err := ParseNameAddr(v)
		if err != nil {
			return nil, fmt.Errorf("sip: History-Info: %w", err)
		}
		e := HistoryInfoEntry{DisplayName: na.DisplayName, URI: na.URI}
		for _, p := range na.Params {
			if strings.EqualFold(p.Name, "index") {
				e.Index = p.Value
			} else {
				e.Params = append(e.Params, p)
			}
		}
		if !validHistoryIndex(e.Index) {
			// RFC 7044 5: hi-index is mandatory.
			return nil, fmt.Errorf("sip: History-Info: entry %q has no usable index", v)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// validHistoryIndex checks index-val of RFC 7044 5: numbers separated
// by dots.
func validHistoryIndex(index string) bool {
	if index == "" {
		return false
	}
	for _, n := range strings.Split(index, ".") {
		if n == "" || strings.TrimLeft(n, "0123456789") != "" {
			return false
		}
	}
	return true
}

func (e HistoryInfoEntry) String() string {
	params := Params{{Name: "index", Value: e.Index}}
	return NameAddr{DisplayName: e.DisplayName, URI: e.URI, Params: append(params, e.Params...)}.String()
}

// Target is the hi-targeted-to-uri without its headers part: the
// Request-URI that the request was sent to.
func (e HistoryInfoEntry) Target() string {
	target, _, _ := strings.Cut(e.URI, "?")
	return target
}

// Reasons are the Reason values of the entry: what the response to the
// request sent to this target said, such as SIP;cause=486 for a callee
// that was busy (RFC 7044 5).
func (e HistoryInfoEntry) Reasons() []Reason {
	var reasons []Reason
	for _, v := range uriHeaders(e.URI, "Reason") {
		for _, one := range splitList(v) {
			if r, err := ParseReason(one); err == nil {
				reasons = append(reasons, r)
			}
		}
	}
	return reasons
}

// Privacy is the Privacy value of the entry. An entry with "history"
// has to be anonymized before it is forwarded (RFC 7044 5).
func (e HistoryInfoEntry) Privacy() Privacy {
	var p Privacy
	for _, v := range uriHeaders(e.URI, "Privacy") {
		p = append(p, ParsePrivacy(v)...)
	}
	return p
}

// HistoryInfoHeaders writes the entries as header fields, one row per
// entry, which is easier to read in a capture than one long row.
func HistoryInfoHeaders(entries []HistoryInfoEntry) []HeaderField {
	var rows []HeaderField
	for _, e := range entries {
		rows = append(rows, HeaderField{Name: "History-Info", Value: e.String()})
	}
	return rows
}

// uriHeaders returns the values of the named header in the headers part
// of a URI, "?hname=hvalue&hname=hvalue", with their escapes resolved
// (RFC 3261 19.1.1). Names compare case-insensitively, like the names
// of header fields.
func uriHeaders(uri, name string) []string {
	_, headers, _ := strings.Cut(uri, "?")
	var values []string
	for _, h := range strings.Split(headers, "&") {
		hname, hvalue, _ := strings.Cut(h, "=")
		if !strings.EqualFold(hname, name) {
			continue
		}
		if v, err := url.PathUnescape(hvalue); err == nil {
			values = append(values, v)
		}
	}
	return values
}

// withURIHeader adds a header to the headers part of uri, with every
// character escaped that RFC 3261 25.1 does not allow in an hvalue.
func withURIHeader(uri, name, value string) string {
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return uri + sep + name + "=" + escapeURIHeaderValue(value)
}

// escapeURIHeaderValue escapes value as an hvalue (RFC 3261 25.1):
// unreserved characters and the hnv-unreserved ones stay, anything else
// becomes %XX.
func escapeURIHeaderValue(value string) string {
	const allowed = "-_.!~*'()" + "[]/?:+$"
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(allowed, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
