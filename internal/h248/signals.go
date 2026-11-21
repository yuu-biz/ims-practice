package h248

import (
	"fmt"
	"strings"
)

// SignalsDescriptor tells the media gateway which signals to apply to
// a termination, such as a tone or an announcement (RFC 3525 7.1.11).
// It replaces the signals that were set before, so a descriptor
// without signals stops them. The signals and the lists of one
// descriptor play at the same time.
type SignalsDescriptor struct {
	Signals []Signal
	// SeqSigLists are lists of signals that play one after another.
	// Of the three profiles only Mp uses them (TS 29.333 5.7.4, Table
	// 5.7.4.2).
	SeqSigLists []SeqSigList
}

// Signal is one signal to apply.
type Signal struct {
	// SignalName names the signal, such as ipnapt/latch.
	SignalName PkgdName
	// StreamID limits the signal to one stream. Zero, the default,
	// means that the signal belongs to no stream in particular (RFC
	// 3525 7.1.11) and is not written.
	StreamID uint16
	// SigType overrides the type that the package gives the signal.
	// It is empty when the default stands.
	SigType SignalType
	// Duration is in hundredths of a second. nil leaves it out.
	Duration *uint16
	// NotifyCompletion lists the ways of ending that the media
	// gateway is to report with the event g/sc.
	NotifyCompletion []NotificationReason
	// KeepActive keeps the signal playing when this descriptor
	// replaces one in which it is already playing.
	KeepActive bool
	SigParList []SigParameter
}

// SeqSigList is a list of signals that play one after another.
type SeqSigList struct {
	ID         uint16
	SignalList []Signal
}

// SigParameter is a parameter of a signal (sigOther in RFC 3525 Annex
// B.2). The package that defines the signal defines its parameters.
type SigParameter struct {
	Name string
	// Value is the value, unless Sublist is used.
	Value   Value
	Sublist []Value
}

// SignalType says how a signal ends.
type SignalType string

const (
	// Brief signals stop on their own.
	Brief SignalType = "Brief"
	// OnOff signals last until they are turned off.
	OnOff SignalType = "OnOff"
	// TimeOut signals last until they are turned off or their
	// duration has passed.
	TimeOut SignalType = "TimeOut"
)

// NotificationReason is a way in which a signal can end.
type NotificationReason string

const (
	// OnTimeOut: the signal timed out or completed on its own.
	OnTimeOut NotificationReason = "TimeOut"
	// OnInterruptByEvent: an event stopped the signal.
	OnInterruptByEvent NotificationReason = "IntByEvent"
	// OnInterruptByNewSignalDescr: a new Signals descriptor replaced
	// the one with the signal.
	OnInterruptByNewSignalDescr NotificationReason = "IntBySigDescr"
	// OtherReason: the signal stopped, or never started, for another
	// reason.
	OtherReason NotificationReason = "OtherReason"
)

// signalsDescriptor reads what follows the Signals token:
// "[ { [ signalParm, ... ] } ]". Corrigendum 1 (03/2004) to H.248.1
// version 2 made the braces optional (Annex B.2, signalsDescriptor),
// and version 3 writes a descriptor without signals that way, so a
// bare Signals token stands for an empty descriptor. The braces are
// still written: RFC 3525, the text of version 1, requires them.
func (s *scanner) signalsDescriptor() (*SignalsDescriptor, error) {
	d := &SignalsDescriptor{}
	s.skipLWSP()
	if s.peek() != '{' {
		return d, nil
	}
	err := s.listOrEmpty(func() error {
		text, pkgd, err := s.pkgdNameOrWord()
		if err != nil {
			return err
		}
		if pkgd {
			signal, err := s.signal(PkgdName(text))
			d.Signals = append(d.Signals, signal)
			return err
		}
		if name, _ := longToken(text); name != tokSignalList {
			return s.errorf("%q cannot stand in a %s descriptor", text, tokSignals)
		}
		list, err := s.seqSigList()
		d.SeqSigLists = append(d.SeqSigLists, list)
		return err
	})
	return d, err
}

// seqSigList reads what follows the SignalList token:
// "= signalListId { signalRequest, ... }".
func (s *scanner) seqSigList() (SeqSigList, error) {
	var list SeqSigList
	if err := s.expect('='); err != nil {
		return list, err
	}
	id, err := s.uint(16)
	if err != nil {
		return list, err
	}
	list.ID = uint16(id)
	err = s.list(func() error {
		name, err := s.pkgdName()
		if err != nil {
			return err
		}
		signal, err := s.signal(name)
		list.SignalList = append(list.SignalList, signal)
		return err
	})
	return list, err
}

// signal reads the parameters of the signal called name, if it has
// any: "[ { sigParameter, ... } ]".
func (s *scanner) signal(name PkgdName) (Signal, error) {
	signal := Signal{SignalName: name}
	// "package/*" names every item of a package. That is allowed for
	// events, not for signals (RFC 3525 7.1.11).
	if strings.HasSuffix(string(name), "/*") {
		return signal, s.errorf("the signal %q is a wildcard", string(name))
	}
	s.skipLWSP()
	if s.peek() != '{' {
		return signal, nil
	}
	hasStream := false
	err := s.list(func() error {
		var err error
		word := s.word()
		switch name, _ := longToken(word); {
		case name == tokStream && !hasStream:
			hasStream = true
			signal.StreamID, err = s.streamID()
		case name == tokSignalType && signal.SigType == "":
			signal.SigType, err = s.signalType()
		case name == tokDuration && signal.Duration == nil:
			if err = s.expect('='); err == nil {
				var duration uint64
				duration, err = s.uint(16)
				signal.Duration = new(uint16(duration))
			}
		case name == tokNotifyCompletion && signal.NotifyCompletion == nil:
			signal.NotifyCompletion, err = s.notifyCompletion()
		case name == tokKeepActive && !signal.KeepActive:
			signal.KeepActive = true
		case name == tokStream || name == tokSignalType || name == tokDuration || name == tokNotifyCompletion || name == tokKeepActive:
			return s.errorf("%s appears twice in a signal", name)
		default:
			p := SigParameter{Name: word}
			if !validName(word) {
				return s.errorf("expected the name of a parameter, found %s", s.found())
			}
			p.Value, p.Sublist, err = s.parmValue()
			signal.SigParList = append(signal.SigParList, p)
		}
		return err
	})
	return signal, err
}

// signalType reads "= signalType".
func (s *scanner) signalType() (SignalType, error) {
	if err := s.expect('='); err != nil {
		return "", err
	}
	name, err := s.token()
	if err != nil {
		return "", err
	}
	if !isSignalType(name) {
		return "", s.errorf("%s is not a signal type", name)
	}
	return SignalType(name), nil
}

func isSignalType(name string) bool {
	switch SignalType(name) {
	case Brief, OnOff, TimeOut:
		return true
	}
	return false
}

// notifyCompletion reads "= { notificationReason, ... }".
func (s *scanner) notifyCompletion() ([]NotificationReason, error) {
	if err := s.expect('='); err != nil {
		return nil, err
	}
	var reasons []NotificationReason
	err := s.list(func() error {
		name, err := s.token()
		if err != nil {
			return err
		}
		if !isNotificationReason(name) {
			return s.errorf("%s is not a reason for a signal to end", name)
		}
		reasons = append(reasons, NotificationReason(name))
		return nil
	})
	return reasons, err
}

func isNotificationReason(name string) bool {
	switch NotificationReason(name) {
	case OnTimeOut, OnInterruptByEvent, OnInterruptByNewSignalDescr, OtherReason:
		return true
	}
	return false
}

func (w *writer) signalsDescriptor(d *SignalsDescriptor) {
	var items []func()
	for _, signal := range d.Signals {
		items = append(items, func() { w.item(w.signal(signal)) })
	}
	for _, list := range d.SeqSigLists {
		if len(list.SignalList) == 0 {
			w.fail("signal list %d is empty", list.ID)
		}
		var signals []func()
		for _, signal := range list.SignalList {
			signals = append(signals, func() { w.item(w.signal(signal)) })
		}
		items = append(items, func() { w.block(fmt.Sprintf("%s = %d", tokSignalList, list.ID), signals) })
	}
	if len(items) == 0 {
		w.item(tokSignals + " { }")
		return
	}
	w.block(tokSignals, items)
}

// signal returns a signal with its parameters on one line.
func (w *writer) signal(signal Signal) string {
	var parms []string
	if signal.StreamID != 0 {
		parms = append(parms, fmt.Sprintf("%s = %d", tokStream, signal.StreamID))
	}
	if signal.SigType != "" {
		if !isSignalType(string(signal.SigType)) {
			w.fail("%q is not a signal type", string(signal.SigType))
		}
		parms = append(parms, fmt.Sprintf("%s = %s", tokSignalType, signal.SigType))
	}
	if signal.Duration != nil {
		parms = append(parms, fmt.Sprintf("%s = %d", tokDuration, *signal.Duration))
	}
	if signal.NotifyCompletion != nil {
		reasons := make([]string, len(signal.NotifyCompletion))
		for i, reason := range signal.NotifyCompletion {
			if !isNotificationReason(string(reason)) {
				w.fail("%q is not a reason for a signal to end", string(reason))
			}
			reasons[i] = string(reason)
		}
		if len(reasons) == 0 {
			w.fail("%s of %s lists no reason", tokNotifyCompletion, signal.SignalName)
		}
		parms = append(parms, fmt.Sprintf("%s = { %s }", tokNotifyCompletion, strings.Join(reasons, ", ")))
	}
	if signal.KeepActive {
		parms = append(parms, tokKeepActive)
	}
	for _, p := range signal.SigParList {
		if !validName(p.Name) {
			w.fail("%q is not the name of a parameter", p.Name)
		}
		parms = append(parms, w.parm(p.Name, p.Value, p.Sublist))
	}
	if strings.HasSuffix(string(signal.SignalName), "/*") {
		w.fail("the signal %q is a wildcard", string(signal.SignalName))
	}
	return w.packageItem(signal.SignalName, parms)
}
