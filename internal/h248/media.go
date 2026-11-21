package h248

import (
	"fmt"
	"strings"
)

// MediaDescriptor describes the media streams of a termination (RFC
// 3525 7.1.4). The streams are given either as OneStream or as
// MultiStream, never both.
type MediaDescriptor struct {
	// TerminationState holds properties that are not specific to a
	// stream.
	TerminationState *TerminationStateDescriptor
	// OneStream is the short way to describe a termination with a
	// single stream: the Stream descriptor is left out and the media
	// gateway takes the StreamID to be 1 (TS 29.334 5.7.2.0, Table
	// 5.7.2.1, NOTE 1).
	OneStream *StreamParms
	// MultiStream holds one Stream descriptor for each stream.
	MultiStream []StreamDescriptor
}

// StreamDescriptor describes one stream (RFC 3525 7.1.6). Streams with
// the same StreamID on the terminations of a context are connected to
// each other.
type StreamDescriptor struct {
	StreamID    uint16
	StreamParms StreamParms
}

// StreamParms are the three descriptors a stream is made of.
type StreamParms struct {
	LocalControl *LocalControlDescriptor
	// Local describes the media that the media gateway receives.
	Local *LocalRemoteDescriptor
	// Remote describes the media that the media gateway sends.
	Remote *LocalRemoteDescriptor
}

// LocalControlDescriptor holds the properties of a stream that matter
// between the media gateway and its controller only (RFC 3525 7.1.7).
type LocalControlDescriptor struct {
	// StreamMode is empty when the descriptor does not set the mode.
	StreamMode StreamMode
	// ReserveValue and ReserveGroup tell the media gateway whether to
	// reserve resources for every alternative that Local and Remote
	// offer. nil leaves them out.
	ReserveValue *bool
	ReserveGroup *bool
	// PropertyParms are properties of packages, such as ipdc/realm.
	PropertyParms []PropertyParm
}

// StreamMode says in which directions media flows through a
// termination. Send and receive are meant as seen from outside the
// context: a termination in SendOnly passes no received media into the
// context (RFC 3525 7.1.7).
type StreamMode string

const (
	SendOnly StreamMode = "SendOnly"
	RecvOnly StreamMode = "ReceiveOnly"
	SendRecv StreamMode = "SendReceive"
	Inactive StreamMode = "Inactive"
	LoopBack StreamMode = "Loopback"
)

// LocalRemoteDescriptor is a Local or a Remote descriptor (RFC 3525
// 7.1.8). In the text encoding it holds a session description.
type LocalRemoteDescriptor struct {
	// SDP is the session description as it stands in the message.
	// This package does not look into it. A controller may leave a
	// choice to the media gateway by writing "$" in place of a value,
	// for example "c=IN IP4 $", which is why the text is not always
	// SDP that a parser of RFC 8866 accepts.
	SDP string
}

// TerminationStateDescriptor holds the properties of a termination
// that do not belong to one stream (RFC 3525 7.1.5).
type TerminationStateDescriptor struct {
	// ServiceState is empty when the descriptor does not carry it. The
	// profiles use it on the ROOT termination only (TS 29.334 5.7.1).
	ServiceState  ServiceState
	PropertyParms []PropertyParm
}

// ServiceState says whether a termination can be used.
type ServiceState string

const (
	ServiceStateTest         ServiceState = "Test"
	ServiceStateOutOfService ServiceState = "OutOfService"
	ServiceStateInService    ServiceState = "InService"
)

func (s *scanner) mediaDescriptor() (*MediaDescriptor, error) {
	m := &MediaDescriptor{}
	err := s.list(func() error {
		name, err := s.token()
		if err != nil {
			return err
		}
		switch name {
		case tokTerminationState:
			if m.TerminationState != nil {
				return s.errorf("the %s descriptor appears twice", name)
			}
			m.TerminationState, err = s.terminationStateDescriptor()
			return err
		case tokStream:
			stream, err := s.streamDescriptor()
			m.MultiStream = append(m.MultiStream, stream)
			return err
		}
		if m.OneStream == nil {
			m.OneStream = &StreamParms{}
		}
		return s.streamParm(name, m.OneStream)
	})
	if err != nil {
		return nil, err
	}
	if m.OneStream != nil && m.MultiStream != nil {
		return nil, s.errorf("a %s descriptor describes its streams with or without %s descriptors, not both", tokMedia, tokStream)
	}
	return m, nil
}

func (w *writer) mediaDescriptor(m *MediaDescriptor) {
	var items []func()
	if m.TerminationState != nil {
		items = append(items, func() { w.terminationStateDescriptor(m.TerminationState) })
	}
	if m.OneStream != nil {
		if m.MultiStream != nil {
			w.fail("a %s descriptor describes its streams with or without %s descriptors, not both", tokMedia, tokStream)
		}
		items = append(items, w.streamParms(*m.OneStream)...)
	}
	for _, stream := range m.MultiStream {
		items = append(items, func() {
			w.block(fmt.Sprintf("%s = %d", tokStream, stream.StreamID), w.streamParms(stream.StreamParms))
		})
	}
	if len(items) == 0 {
		w.fail("a %s descriptor is empty", tokMedia)
	}
	w.block(tokMedia, items)
}

// streamDescriptor reads what follows the Stream token:
// "= StreamID { streamParm, ... }".
func (s *scanner) streamDescriptor() (StreamDescriptor, error) {
	var stream StreamDescriptor
	if err := s.expect('='); err != nil {
		return stream, err
	}
	id, err := s.uint(16)
	if err != nil {
		return stream, err
	}
	stream.StreamID = uint16(id)
	err = s.list(func() error {
		name, err := s.token()
		if err != nil {
			return err
		}
		return s.streamParm(name, &stream.StreamParms)
	})
	return stream, err
}

// streamParm reads the descriptor that the token name starts into p.
func (s *scanner) streamParm(name string, p *StreamParms) error {
	var err error
	switch {
	case name == tokLocalControl && p.LocalControl == nil:
		p.LocalControl, err = s.localControlDescriptor()
	case name == tokLocal && p.Local == nil:
		p.Local, err = s.localRemoteDescriptor()
	case name == tokRemote && p.Remote == nil:
		p.Remote, err = s.localRemoteDescriptor()
	case name == tokLocalControl || name == tokLocal || name == tokRemote:
		return s.errorf("the %s descriptor appears twice in a stream", name)
	default:
		return s.errorf("%s cannot stand in a %s descriptor", name, tokMedia)
	}
	return err
}

func (w *writer) streamParms(p StreamParms) []func() {
	var items []func()
	if p.LocalControl != nil {
		items = append(items, func() { w.localControlDescriptor(p.LocalControl) })
	}
	if p.Local != nil {
		items = append(items, func() { w.localRemoteDescriptor(tokLocal, p.Local) })
	}
	if p.Remote != nil {
		items = append(items, func() { w.localRemoteDescriptor(tokRemote, p.Remote) })
	}
	if len(items) == 0 {
		w.fail("a stream has none of %s, %s and %s", tokLocalControl, tokLocal, tokRemote)
	}
	return items
}

func (s *scanner) localControlDescriptor() (*LocalControlDescriptor, error) {
	l := &LocalControlDescriptor{}
	err := s.list(func() error {
		text, pkgd, err := s.pkgdNameOrWord()
		if err != nil {
			return err
		}
		if pkgd {
			p, err := s.propertyParm(PkgdName(text))
			l.PropertyParms = append(l.PropertyParms, p)
			return err
		}
		name, ok := longToken(text)
		switch {
		case ok && name == tokMode && l.StreamMode == "":
			l.StreamMode, err = s.streamMode()
		case ok && name == tokReservedValue && l.ReserveValue == nil:
			l.ReserveValue, err = s.onOff()
		case ok && name == tokReservedGroup && l.ReserveGroup == nil:
			l.ReserveGroup, err = s.onOff()
		case ok && (name == tokMode || name == tokReservedValue || name == tokReservedGroup):
			return s.errorf("%s appears twice in a %s descriptor", name, tokLocalControl)
		default:
			return s.errorf("%q cannot stand in a %s descriptor", text, tokLocalControl)
		}
		return err
	})
	return l, err
}

func (w *writer) localControlDescriptor(l *LocalControlDescriptor) {
	var parms []string
	if l.StreamMode != "" {
		if !isStreamMode(string(l.StreamMode)) {
			w.fail("%q is not a stream mode", string(l.StreamMode))
		}
		parms = append(parms, fmt.Sprintf("%s = %s", tokMode, l.StreamMode))
	}
	if l.ReserveValue != nil {
		parms = append(parms, fmt.Sprintf("%s = %s", tokReservedValue, onOff(*l.ReserveValue)))
	}
	if l.ReserveGroup != nil {
		parms = append(parms, fmt.Sprintf("%s = %s", tokReservedGroup, onOff(*l.ReserveGroup)))
	}
	for _, p := range l.PropertyParms {
		parms = append(parms, w.propertyParm(p))
	}
	w.parmBlock(tokLocalControl, parms)
}

// streamMode reads "= streamModes".
func (s *scanner) streamMode() (StreamMode, error) {
	if err := s.expect('='); err != nil {
		return "", err
	}
	name, err := s.token()
	if err != nil {
		return "", err
	}
	if !isStreamMode(name) {
		return "", s.errorf("%s is not a stream mode", name)
	}
	return StreamMode(name), nil
}

func isStreamMode(name string) bool {
	switch StreamMode(name) {
	case SendOnly, RecvOnly, SendRecv, Inactive, LoopBack:
		return true
	}
	return false
}

// onOff reads `= ( "ON" / "OFF" )`.
func (s *scanner) onOff() (*bool, error) {
	if err := s.expect('='); err != nil {
		return nil, err
	}
	switch word := s.word(); strings.ToUpper(word) {
	case "ON":
		return new(true), nil
	case "OFF":
		return new(false), nil
	default:
		return nil, s.errorf("expected ON or OFF, found %q", word)
	}
}

func onOff(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// localRemoteDescriptor reads what follows the Local or Remote token:
// "{ octetString }". A "}" inside the octet string is escaped as "\}".
func (s *scanner) localRemoteDescriptor() (*LocalRemoteDescriptor, error) {
	if err := s.expect('{'); err != nil {
		return nil, err
	}
	// SDP allows no white space at the start of a line, but the
	// grammar allows it between "{" and the SDP, and a parser should
	// take it (RFC 3525 Annex B.2, the notes at the head of the
	// grammar). A comment is not skipped here: ";" belongs to the SDP.
	for !s.atEnd() && strings.IndexByte(" \t\r\n", s.b[s.pos]) >= 0 {
		s.pos++
	}
	var sdp strings.Builder
	for {
		switch {
		case s.atEnd():
			return nil, s.errorf("a %s or %s descriptor is not closed", tokLocal, tokRemote)
		case s.b[s.pos] == '\\' && s.pos+1 < len(s.b) && s.b[s.pos+1] == '}':
			sdp.WriteByte('}')
			s.pos += 2
		case s.b[s.pos] == '}':
			s.pos++
			// White space on the line of the closing "}" is layout,
			// not SDP. The line end of the last SDP line is kept.
			return &LocalRemoteDescriptor{SDP: strings.TrimRight(sdp.String(), " \t")}, nil
		default:
			sdp.WriteByte(s.b[s.pos])
			s.pos++
		}
	}
}

// localRemoteDescriptor writes the SDP from the start of a line and
// puts the closing "}" right after the last line end. With white space
// in between, tshark 4.2.2 reports an invalid SDP line.
func (w *writer) localRemoteDescriptor(token string, d *LocalRemoteDescriptor) {
	if strings.IndexByte(d.SDP, 0) >= 0 {
		w.fail("the SDP of a %s descriptor holds a zero octet", token)
	}
	w.item(token + " {\n")
	w.buf.WriteString(strings.ReplaceAll(d.SDP, "}", `\}`))
	if d.SDP != "" && !strings.HasSuffix(d.SDP, "\n") && !strings.HasSuffix(d.SDP, "\r") {
		w.buf.WriteByte('\n')
	}
	w.buf.WriteByte('}')
}

func (s *scanner) terminationStateDescriptor() (*TerminationStateDescriptor, error) {
	t := &TerminationStateDescriptor{}
	err := s.list(func() error {
		text, pkgd, err := s.pkgdNameOrWord()
		if err != nil {
			return err
		}
		if pkgd {
			p, err := s.propertyParm(PkgdName(text))
			t.PropertyParms = append(t.PropertyParms, p)
			return err
		}
		// The EventBufferControl property ("Buffer") is not read: no
		// profile uses it (TS 29.334 5.7.1, Table 5.7.1.2).
		if name, ok := longToken(text); !ok || name != tokServiceStates {
			return s.errorf("%q cannot stand in a %s descriptor", text, tokTerminationState)
		}
		if t.ServiceState != "" {
			return s.errorf("%s appears twice in a %s descriptor", tokServiceStates, tokTerminationState)
		}
		if err := s.expect('='); err != nil {
			return err
		}
		name, err := s.token()
		if err != nil {
			return err
		}
		if !isServiceState(name) {
			return s.errorf("%s is not a service state", name)
		}
		t.ServiceState = ServiceState(name)
		return nil
	})
	return t, err
}

func (w *writer) terminationStateDescriptor(t *TerminationStateDescriptor) {
	var parms []string
	if t.ServiceState != "" {
		if !isServiceState(string(t.ServiceState)) {
			w.fail("%q is not a service state", string(t.ServiceState))
		}
		parms = append(parms, fmt.Sprintf("%s = %s", tokServiceStates, t.ServiceState))
	}
	for _, p := range t.PropertyParms {
		parms = append(parms, w.propertyParm(p))
	}
	w.parmBlock(tokTerminationState, parms)
}

func isServiceState(name string) bool {
	switch ServiceState(name) {
	case ServiceStateTest, ServiceStateOutOfService, ServiceStateInService:
		return true
	}
	return false
}

// parmBlock writes "head {" and one parameter on each line.
func (w *writer) parmBlock(head string, parms []string) {
	if len(parms) == 0 {
		w.fail("a %s descriptor is empty", head)
	}
	items := make([]func(), len(parms))
	for i, parm := range parms {
		items[i] = func() { w.item(parm) }
	}
	w.block(head, items)
}
