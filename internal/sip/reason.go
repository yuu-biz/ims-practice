package sip

import (
	"fmt"
	"strconv"
	"strings"
)

// Protocols of a Reason value. RFC 3326 2 defines SIP and Q.850 and
// allows any other token, such as the RELEASE_CAUSE that a VoLTE UE
// puts in its BYE and CANCEL (GSMA IR.92).
const (
	// ReasonSIP has a SIP status code as its cause.
	ReasonSIP = "SIP"
	// ReasonQ850 has an ITU-T Q.850 cause value as its cause.
	ReasonQ850 = "Q.850"
)

// Reason is one value of the Reason header field (RFC 3326 2), which
// tells why a request within a dialog, a CANCEL or a 199 response was
// sent.
//
//	Reason: SIP;cause=486;text="Busy Here"
type Reason struct {
	Protocol string
	// Cause is the cause parameter. HasCause is false when the value
	// has none; cause 0 is a valid Q.850 value.
	Cause    int
	HasCause bool
	// Text is the text parameter without its quotes.
	Text string
	// Params are the other parameters.
	Params Params
}

// NewReason is a Reason with a cause, and a text unless text is "".
func NewReason(protocol string, cause int, text string) Reason {
	return Reason{Protocol: protocol, Cause: cause, HasCause: true, Text: text}
}

// ParseReason reads one Reason value. Message.GetAll("Reason") splits
// the header field into its values.
func ParseReason(value string) (Reason, error) {
	protocol, rest, _ := strings.Cut(value, ";")
	r := Reason{Protocol: strings.TrimSpace(protocol)}
	if r.Protocol == "" || strings.ContainsAny(r.Protocol, " \t") {
		return Reason{}, fmt.Errorf("sip: malformed Reason %q", value)
	}
	for _, p := range parseParams(rest) {
		switch strings.ToLower(p.Name) {
		case "cause":
			n, err := strconv.Atoi(p.Value)
			if err != nil || n < 0 {
				return Reason{}, fmt.Errorf("sip: malformed Reason cause in %q", value)
			}
			r.Cause, r.HasCause = n, true
		case "text":
			r.Text = Unquote(p.Value)
		default:
			r.Params = append(r.Params, p)
		}
	}
	return r, nil
}

func (r Reason) String() string {
	var params Params
	if r.HasCause {
		params.Set("cause", strconv.Itoa(r.Cause))
	}
	if r.Text != "" {
		params.Set("text", Quote(r.Text))
	}
	return r.Protocol + params.String() + r.Params.String()
}

// Header is the Reason header field with this value, to add to a BYE, a
// CANCEL or a 199.
func (r Reason) Header() HeaderField { return HeaderField{Name: "Reason", Value: r.String()} }

// Reasons reads every Reason value of m; a message may carry one per
// protocol (RFC 3326 2). A value that does not parse is left out: an
// implementation is free to ignore the values it does not understand.
func Reasons(m *Message) []Reason {
	var reasons []Reason
	for _, v := range m.GetAll("Reason") {
		if r, err := ParseReason(v); err == nil {
			reasons = append(reasons, r)
		}
	}
	return reasons
}

// ReasonFields are the Reason header fields of m as they came, for a
// request that goes on for the same reason: the CANCEL that a proxy
// sends for the CANCEL it got (RFC 3326 2). None for a nil m.
func ReasonFields(m *Message) []HeaderField {
	if m == nil {
		return nil
	}
	var fields []HeaderField
	for _, v := range m.GetAll("Reason") {
		fields = append(fields, HeaderField{Name: "Reason", Value: v})
	}
	return fields
}
