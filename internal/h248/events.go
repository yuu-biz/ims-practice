package h248

import (
	"fmt"
	"strings"
)

// EventsDescriptor tells the media gateway which events to look for
// and to report with Notify (RFC 3525 7.1.9). It replaces the events
// that were set before, so a descriptor without events stops all
// reporting.
type EventsDescriptor struct {
	// RequestID comes back in the ObservedEvents descriptor of the
	// Notify, so that the controller knows which request the report
	// answers.
	RequestID uint32
	EventList []RequestedEvent
}

// RequestedEvent is one event to look for.
type RequestedEvent struct {
	// PkgdName names the event, such as hangterm/thb.
	PkgdName PkgdName
	// StreamID limits the event to one stream. Zero, the default,
	// means that the event belongs to no stream in particular (RFC
	// 3525 7.1.9) and is not written.
	StreamID uint16
	// KeepActive lets the signals that are playing go on when the
	// event is detected. Of the three profiles only Mp uses it (TS
	// 29.333 5.7.2, Table 5.7.2.3).
	KeepActive bool
	EvParList  []EventParameter
}

// EventParameter is a parameter of an event, in a request or in a
// report (eventOther in RFC 3525 Annex B.2). The package that defines
// the event defines its parameters.
type EventParameter struct {
	Name string
	// Value is the value, unless Sublist is used.
	Value   Value
	Sublist []Value
}

// ObservedEventsDescriptor reports events in a Notify (RFC 3525
// 7.1.17).
type ObservedEventsDescriptor struct {
	// RequestID is that of the Events descriptor that asked for the
	// events.
	RequestID         uint32
	ObservedEventList []ObservedEvent
}

// ObservedEvent is one event that the media gateway detected.
type ObservedEvent struct {
	EventName PkgdName
	// StreamID is zero when the event belongs to no stream.
	StreamID uint16
	// TimeNotation is when the event was detected. The zero value
	// leaves it out, as Iq and Ix do (TS 29.334 5.7.8); Mp sends it
	// (TS 29.333 5.7.7).
	TimeNotation TimeNotation
	EventParList []EventParameter
}

// TimeNotation is a time stamp (TimeStamp in RFC 3525 Annex B.2).
type TimeNotation struct {
	// Date is yyyymmdd.
	Date string
	// Time is hhmmssss, down to hundredths of a second.
	Time string
}

// eventsDescriptor reads what follows the Events token:
// "[ = RequestID { requestedEvent, ... } ]".
func (s *scanner) eventsDescriptor() (*EventsDescriptor, error) {
	e := &EventsDescriptor{}
	s.skipLWSP()
	if s.peek() != '=' {
		return e, nil
	}
	s.pos++
	id, err := s.uint(32)
	if err != nil {
		return nil, err
	}
	e.RequestID = uint32(id)
	err = s.list(func() error {
		event, err := s.requestedEvent()
		e.EventList = append(e.EventList, event)
		return err
	})
	return e, err
}

func (s *scanner) requestedEvent() (RequestedEvent, error) {
	var e RequestedEvent
	var err error
	if e.PkgdName, err = s.pkgdName(); err != nil {
		return e, err
	}
	s.skipLWSP()
	if s.peek() != '{' {
		return e, nil
	}
	hasStream := false
	err = s.list(func() error {
		word := s.word()
		switch name, _ := longToken(word); {
		case name == tokStream && !hasStream:
			hasStream = true
			e.StreamID, err = s.streamID()
		case name == tokKeepActive && !e.KeepActive:
			e.KeepActive = true
		case name == tokStream || name == tokKeepActive:
			return s.errorf("%s appears twice in an event", name)
		case name == tokEmbed || name == tokDigitMap:
			// No profile embeds events or signals in an event (TS
			// 29.334 5.7.3, Table 5.7.3.4) or uses digit maps
			// (5.7.6).
			return s.errorf("%s in an event is not supported", name)
		default:
			var p EventParameter
			p, err = s.eventParameter(word)
			e.EvParList = append(e.EvParList, p)
		}
		return err
	})
	return e, err
}

// eventParameter reads the value of the parameter called name.
func (s *scanner) eventParameter(name string) (EventParameter, error) {
	p := EventParameter{Name: name}
	if !validName(name) {
		return p, s.errorf("expected the name of a parameter, found %s", s.found())
	}
	var err error
	p.Value, p.Sublist, err = s.parmValue()
	return p, err
}

// streamID reads "= StreamID".
func (s *scanner) streamID() (uint16, error) {
	if err := s.expect('='); err != nil {
		return 0, err
	}
	id, err := s.uint(16)
	return uint16(id), err
}

func (w *writer) eventsDescriptor(e *EventsDescriptor) {
	if len(e.EventList) == 0 {
		if e.RequestID != 0 {
			w.fail("an %s descriptor without events has no request ID", tokEvents)
		}
		w.item(tokEvents)
		return
	}
	var items []func()
	for _, event := range e.EventList {
		var parms []string
		if event.StreamID != 0 {
			parms = append(parms, fmt.Sprintf("%s = %d", tokStream, event.StreamID))
		}
		if event.KeepActive {
			parms = append(parms, tokKeepActive)
		}
		parms = append(parms, w.eventParameters(event.EvParList)...)
		items = append(items, func() { w.item(w.packageItem(event.PkgdName, parms)) })
	}
	w.block(fmt.Sprintf("%s = %d", tokEvents, e.RequestID), items)
}

func (w *writer) eventParameters(list []EventParameter) []string {
	var parms []string
	for _, p := range list {
		if !validName(p.Name) {
			w.fail("%q is not the name of a parameter", p.Name)
		}
		parms = append(parms, w.parm(p.Name, p.Value, p.Sublist))
	}
	return parms
}

// packageItem returns an event or a signal with its parameters on one
// line: "name { parm, parm }".
func (w *writer) packageItem(name PkgdName, parms []string) string {
	if !validPkgdName(name) {
		w.fail("%q is not the name of a package item", string(name))
	}
	if len(parms) == 0 {
		return string(name)
	}
	return fmt.Sprintf("%s { %s }", name, strings.Join(parms, ", "))
}

// observedEventsDescriptor reads what follows the ObservedEvents token:
// "= RequestID { observedEvent, ... }".
func (s *scanner) observedEventsDescriptor() (*ObservedEventsDescriptor, error) {
	o := &ObservedEventsDescriptor{}
	if err := s.expect('='); err != nil {
		return nil, err
	}
	id, err := s.uint(32)
	if err != nil {
		return nil, err
	}
	o.RequestID = uint32(id)
	err = s.list(func() error {
		event, err := s.observedEvent()
		o.ObservedEventList = append(o.ObservedEventList, event)
		return err
	})
	return o, err
}

func (s *scanner) observedEvent() (ObservedEvent, error) {
	var e ObservedEvent
	var err error
	s.skipLWSP()
	if isDigit(s.peek()) {
		if e.TimeNotation, err = s.timeNotation(); err != nil {
			return e, err
		}
		if err := s.expect(':'); err != nil {
			return e, err
		}
	}
	if e.EventName, err = s.pkgdName(); err != nil {
		return e, err
	}
	s.skipLWSP()
	if s.peek() != '{' {
		return e, nil
	}
	hasStream := false
	err = s.list(func() error {
		word := s.word()
		if name, _ := longToken(word); name == tokStream {
			if hasStream {
				return s.errorf("%s appears twice in an event", name)
			}
			hasStream = true
			e.StreamID, err = s.streamID()
			return err
		}
		p, err := s.eventParameter(word)
		e.EventParList = append(e.EventParList, p)
		return err
	})
	return e, err
}

// timeNotation reads a TimeStamp: eight digits, "T" and eight digits.
func (s *scanner) timeNotation() (TimeNotation, error) {
	var t TimeNotation
	s.skipLWSP()
	t.Date = s.takeWhile(isDigit)
	if s.peek() != 'T' && s.peek() != 't' {
		return t, s.errorf("expected a time stamp, found %s", s.found())
	}
	s.pos++
	t.Time = s.takeWhile(isDigit)
	if len(t.Date) != 8 || len(t.Time) != 8 {
		return t, s.errorf("a time stamp is yyyymmdd, T and hhmmssss")
	}
	return t, nil
}

func (w *writer) timeNotation(t TimeNotation) string {
	for _, part := range []string{t.Date, t.Time} {
		if len(part) != 8 || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			w.fail("the time stamp %sT%s is not yyyymmdd, T and hhmmssss", t.Date, t.Time)
			break
		}
	}
	return t.Date + "T" + t.Time
}

func (w *writer) observedEventsDescriptor(o *ObservedEventsDescriptor) {
	if len(o.ObservedEventList) == 0 {
		w.fail("an %s descriptor reports no event", tokObservedEvents)
	}
	var items []func()
	for _, event := range o.ObservedEventList {
		var parms []string
		if event.StreamID != 0 {
			parms = append(parms, fmt.Sprintf("%s = %d", tokStream, event.StreamID))
		}
		parms = append(parms, w.eventParameters(event.EventParList)...)
		text := w.packageItem(event.EventName, parms)
		if event.TimeNotation != (TimeNotation{}) {
			text = w.timeNotation(event.TimeNotation) + ": " + text
		}
		items = append(items, func() { w.item(text) })
	}
	w.block(fmt.Sprintf("%s = %d", tokObservedEvents, o.RequestID), items)
}
