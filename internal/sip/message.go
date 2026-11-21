// Package sip holds the SIP layers that the IMS nodes share: the
// message codec (RFC 3261 7, 20, 25), the transport layer over UDP and
// TCP (RFC 3261 18, RFC 3581), the transaction layer (RFC 3261 17),
// dialogs (RFC 3261 12), and the user agent core that makes and answers
// calls (RFC 3261 13) and changes them: reliable provisional responses
// (RFC 3262), UPDATE (RFC 3311), re-INVITE (RFC 3261 14) and the
// session timer (RFC 4028). A UE and a B2BUA are both built on that
// core.
package sip

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Method is the method of a request (RFC 3261 7.1). Extensions define
// more methods than the ones listed here; any token is a valid Method.
type Method string

const (
	REGISTER Method = "REGISTER"
	INVITE   Method = "INVITE"
	ACK      Method = "ACK"
	CANCEL   Method = "CANCEL"
	BYE      Method = "BYE"
	OPTIONS  Method = "OPTIONS"
	PRACK    Method = "PRACK"  // RFC 3262 6
	UPDATE   Method = "UPDATE" // RFC 3311 7
	// RFC 6665 8.1
	SUBSCRIBE Method = "SUBSCRIBE"
	NOTIFY    Method = "NOTIFY"
	// MESSAGE carries an instant message, or an SMS in the IMS (TS
	// 24.341). It does not create a dialog (RFC 3428 4).
	MESSAGE Method = "MESSAGE"
)

// SIPVersion is the only protocol version this package speaks (RFC
// 3261 7.1).
const SIPVersion = "SIP/2.0"

// HeaderField is one header field row (RFC 3261 7.3.1). Name is spelled
// as received, except that a compact form such as "v" is expanded to
// its long form "Via" (RFC 3261 7.3.3). Names compare case-insensitively.
// Value is the field value as received, with line folding replaced by a
// single space.
type HeaderField struct {
	Name  string
	Value string
}

// Message is a SIP request or response. A request has Method set and
// StatusCode zero; a response has StatusCode set and Method empty.
// Parse and NewResponse keep to that rule.
type Message struct {
	// Request-Line (RFC 3261 7.1).
	Method     Method
	RequestURI string

	// Status-Line (RFC 3261 7.2).
	StatusCode int
	Reason     string

	// Headers keeps every row in the order it was received or added.
	Headers []HeaderField
	Body    []byte
}

func (m *Message) IsRequest() bool { return m.Method != "" }

// Clone returns a copy that shares nothing with m, so that one goroutine
// can keep a message while another changes the original.
func (m *Message) Clone() *Message {
	c := *m
	c.Headers = append([]HeaderField(nil), m.Headers...)
	c.Body = append([]byte(nil), m.Body...)
	return &c
}

// Get returns the first value of the named header field, or "" when
// there is none. For a header whose value is a comma-separated list,
// such as Via, that is the first element of the list.
func (m *Message) Get(name string) string {
	if v := m.GetAll(name); len(v) > 0 {
		return v[0]
	}
	return ""
}

// GetAll returns every value of the named header field in order. A
// header whose grammar is a comma-separated list is split into its
// elements, so "Via: a, b" and two Via rows give the same result (RFC
// 3261 7.3.1).
func (m *Message) GetAll(name string) []string {
	name = expandCompactForm(name)
	var values []string
	for _, h := range m.Headers {
		if !strings.EqualFold(h.Name, name) {
			continue
		}
		if isListHeader(name) {
			values = append(values, splitList(h.Value)...)
		} else {
			values = append(values, h.Value)
		}
	}
	return values
}

// Set replaces every row of the named header field with one row that
// holds value. The row takes the place of the first row it replaces, or
// goes at the end when there was none.
func (m *Message) Set(name, value string) {
	name = expandCompactForm(name)
	i := m.index(name)
	if i < 0 {
		m.Add(name, value)
		return
	}
	m.Headers[i].Value = value
	rest := removeRows(m.Headers[i+1:], name)
	m.Headers = m.Headers[:i+1+len(rest)]
}

// SetFirst replaces the first value of the named header field and keeps
// the rest. A server transport uses it to add received and rport to the
// top Via (RFC 3261 18.2.1).
func (m *Message) SetFirst(name, value string) {
	name = expandCompactForm(name)
	i := m.index(name)
	if i < 0 {
		m.Add(name, value)
		return
	}
	if !isListHeader(name) {
		m.Headers[i].Value = value
		return
	}
	elements := splitList(m.Headers[i].Value)
	if len(elements) == 0 {
		elements = []string{value}
	} else {
		elements[0] = value
	}
	m.Headers[i].Value = strings.Join(elements, ", ")
}

// Add appends a row for the named header field at the end of the
// header.
func (m *Message) Add(name, value string) {
	m.Headers = append(m.Headers, HeaderField{Name: expandCompactForm(name), Value: value})
}

// Prepend inserts a row before the first row of the named header field,
// or at the top of the header when there is none. A client puts its own
// Via on top this way (RFC 3261 8.1.1.7, 16.6 step 8).
func (m *Message) Prepend(name, value string) {
	name = expandCompactForm(name)
	i := max(m.index(name), 0)
	m.Headers = slices.Insert(m.Headers, i, HeaderField{Name: name, Value: value})
}

// Remove deletes every row of the named header field.
func (m *Message) Remove(name string) {
	m.Headers = removeRows(m.Headers, expandCompactForm(name))
}

func (m *Message) index(name string) int {
	for i, h := range m.Headers {
		if strings.EqualFold(h.Name, name) {
			return i
		}
	}
	return -1
}

func removeRows(rows []HeaderField, name string) []HeaderField {
	kept := rows[:0]
	for _, h := range rows {
		if !strings.EqualFold(h.Name, name) {
			kept = append(kept, h)
		}
	}
	return kept
}

// Bytes is the message as it goes on the wire: CRLF line ends, and a
// Content-Length that matches Body. An existing Content-Length row is
// rewritten where it stands; otherwise one is added after the other
// header fields.
func (m *Message) Bytes() []byte {
	var b bytes.Buffer
	if m.IsRequest() {
		fmt.Fprintf(&b, "%s %s %s\r\n", m.Method, m.RequestURI, SIPVersion)
	} else {
		fmt.Fprintf(&b, "%s %d %s\r\n", SIPVersion, m.StatusCode, m.Reason)
	}
	contentLength := strconv.Itoa(len(m.Body))
	wroteLength := false
	for _, h := range m.Headers {
		value := h.Value
		if strings.EqualFold(h.Name, "Content-Length") {
			if wroteLength {
				continue
			}
			value, wroteLength = contentLength, true
		}
		fmt.Fprintf(&b, "%s: %s\r\n", h.Name, value)
	}
	if !wroteLength {
		fmt.Fprintf(&b, "Content-Length: %s\r\n", contentLength)
	}
	b.WriteString("\r\n")
	b.Write(m.Body)
	return b.Bytes()
}

func (m *Message) String() string { return string(m.Bytes()) }

// ErrTruncatedBody reports a datagram that ends before the number of
// body bytes its Content-Length announces (RFC 3261 18.3).
var ErrTruncatedBody = errors.New("sip: body shorter than Content-Length")

// Parse reads one message from a datagram.
//
// RFC 3261 18.3: bytes after the body that Content-Length announces are
// discarded, and without a Content-Length the body runs to the end of
// the datagram.
func Parse(datagram []byte) (*Message, error) {
	// RFC 3261 7.5 has stream transports ignore CRLFs before the start
	// line. A datagram gets the same tolerance.
	data := datagram
	for bytes.HasPrefix(data, []byte("\r\n")) {
		data = data[2:]
	}
	head, body, found := bytes.Cut(data, []byte("\r\n\r\n"))
	if !found {
		return nil, errors.New("sip: no empty line after the header")
	}
	m, err := parseHeader(head)
	if err != nil {
		return nil, err
	}
	n, ok, err := m.contentLength()
	if err != nil {
		return nil, err
	}
	if ok {
		if n > len(body) {
			return nil, ErrTruncatedBody
		}
		body = body[:n]
	}
	m.Body = append([]byte(nil), body...)
	return m, nil
}

// parseHeader reads the start line and the header fields: everything
// before the empty line.
func parseHeader(head []byte) (*Message, error) {
	lines := strings.Split(string(head), "\r\n")
	m := &Message{}
	if err := m.parseStartLine(lines[0]); err != nil {
		return nil, err
	}
	rows, err := unfold(lines[1:])
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		name, value, ok := strings.Cut(row, ":")
		name = strings.TrimRight(name, " \t")
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("sip: malformed header field %q", row)
		}
		m.Add(name, strings.Trim(value, " \t"))
	}
	return m, nil
}

// contentLength reads Content-Length, and reports false when the
// message has none.
func (m *Message) contentLength() (int, bool, error) {
	v, ok := m.first("Content-Length")
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("sip: malformed Content-Length %q", v)
	}
	return n, true, nil
}

func (m *Message) first(name string) (string, bool) {
	if i := m.index(expandCompactForm(name)); i >= 0 {
		return m.Headers[i].Value, true
	}
	return "", false
}

func (m *Message) parseStartLine(line string) error {
	if rest, ok := strings.CutPrefix(line, SIPVersion+" "); ok {
		// Status-Line = SIP-Version SP Status-Code SP Reason-Phrase
		code, reason, _ := strings.Cut(rest, " ")
		n, err := strconv.Atoi(code)
		if err != nil || len(code) != 3 || n < 100 {
			return fmt.Errorf("sip: malformed Status-Line %q", line)
		}
		m.StatusCode, m.Reason = n, reason
		return nil
	}
	// Request-Line = Method SP Request-URI SP SIP-Version
	parts := strings.Split(line, " ")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] != SIPVersion {
		return fmt.Errorf("sip: malformed start line %q", line)
	}
	m.Method, m.RequestURI = Method(parts[0]), parts[1]
	return nil
}

// unfold joins continuation lines to the row they continue. RFC 3261
// 7.3.1: the line break and the leading whitespace count as one space.
func unfold(lines []string) ([]string, error) {
	var rows []string
	for _, line := range lines {
		if line != "" && (line[0] == ' ' || line[0] == '\t') {
			if len(rows) == 0 {
				return nil, errors.New("sip: continuation line before the first header field")
			}
			rows[len(rows)-1] += " " + strings.TrimLeft(line, " \t")
			continue
		}
		rows = append(rows, line)
	}
	return rows, nil
}

// NewResponse builds a response to req as RFC 3261 8.2.6.2 asks: From,
// Call-ID, CSeq and every Via are copied in order, and To is copied with
// toTag added when the request's To has no tag yet. The same toTag must
// be used for every response to one request; it may be "" for a 100
// (Trying). A 100 also copies Timestamp (RFC 3261 8.2.6.1).
func NewResponse(req *Message, statusCode int, toTag string) *Message {
	resp := &Message{StatusCode: statusCode, Reason: ReasonPhrase(statusCode)}
	copied := []string{"Via", "From", "To", "Call-ID", "CSeq"}
	if statusCode == 100 {
		copied = append(copied, "Timestamp")
	}
	for _, h := range req.Headers {
		for _, name := range copied {
			if strings.EqualFold(h.Name, name) {
				resp.Headers = append(resp.Headers, h)
			}
		}
	}
	if toTag == "" {
		return resp
	}
	to, err := ParseNameAddr(resp.Get("To"))
	if err != nil {
		return resp
	}
	if _, hasTag := to.Params.Get("tag"); !hasTag {
		to.Params.Set("tag", toTag)
		resp.Set("To", to.String())
	}
	return resp
}
