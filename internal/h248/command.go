package h248

import (
	"fmt"
	"slices"
	"strings"
)

// Command names one of the commands of H.248.1 (RFC 3525 7.2). The
// value is the token as it is written in a message.
type Command string

const (
	// Add puts a termination into a context.
	Add Command = "Add"
	// Move takes a termination from one context to another. Of the
	// three profiles only Mp uses it (TS 29.333 5.8.4).
	Move Command = "Move"
	// Modify changes the properties, events and signals of a
	// termination.
	Modify Command = "Modify"
	// Subtract takes a termination out of its context.
	Subtract Command = "Subtract"
	// AuditValue asks for the current state of a termination.
	AuditValue Command = "AuditValue"
	// AuditCapability asks what a termination could do.
	AuditCapability Command = "AuditCapability"
	// Notify reports events that the media gateway has observed.
	Notify Command = "Notify"
	// ServiceChange says that a termination, or with ROOT the whole
	// media gateway or its controller, goes out of or comes into
	// service. A media gateway registers with it.
	ServiceChange Command = "ServiceChange"
)

// TerminationID names a termination. It may hold the wildcards "$" and
// "*" in place of parts of the name, as in "ip/1/access/$" (RFC 3525
// Annex B.1, TS 29.334 5.6.1.1.1).
type TerminationID string

const (
	// TerminationRoot stands for the media gateway as a whole.
	TerminationRoot TerminationID = "ROOT"
	// TerminationChoose asks the media gateway to pick a termination.
	TerminationChoose TerminationID = "$"
	// TerminationAll stands for every termination, of the context
	// that the action names.
	TerminationAll TerminationID = "*"
)

// CommandRequest is one command of a TransactionRequest, with the
// descriptors that are its parameters. Which descriptors a command may
// carry is in requestDescriptors.
type CommandRequest struct {
	// Optional ("O-") lets the commands after this one run even when
	// this one fails.
	Optional bool
	// WildcardResponse ("W-") asks for one reply in place of one for
	// each termination that a wildcard matches. The profiles use it to
	// release every termination of a context (TS 29.334 5.10, Table
	// 5.10.5).
	WildcardResponse bool
	Command          Command
	TerminationID    TerminationID

	Media   *MediaDescriptor
	Events  *EventsDescriptor
	Signals *SignalsDescriptor
	// Audit says what the reply is to report. AuditValue and
	// AuditCapability cannot do without it.
	Audit *AuditDescriptor
	// ObservedEvents is what a Notify reports.
	ObservedEvents *ObservedEventsDescriptor
	// ServiceChange holds the parameters of a ServiceChange.
	ServiceChange *ServiceChangeParm
}

// CommandReply is the reply to one command. Which descriptors it may
// carry is in replyDescriptors.
type CommandReply struct {
	Command       Command
	TerminationID TerminationID

	// Media holds what the media gateway chose where the request left
	// a choice, such as the address and port in the Local descriptor
	// (TS 29.334 5.8.1, Table 5.8.1.2).
	Media *MediaDescriptor
	// Events, Signals and ObservedEvents come back in the reply to an
	// audit.
	Events         *EventsDescriptor
	Signals        *SignalsDescriptor
	ObservedEvents *ObservedEventsDescriptor
	Statistics     StatisticsDescriptor
	Packages       PackagesDescriptor
	// ServiceChangeResult holds the parameters of the reply to a
	// ServiceChange, when it has any.
	ServiceChangeResult *ServiceChangeResParm
	// Error says why the command failed.
	Error *ErrorDescriptor
}

// descriptors holds the descriptors of one command, whether a request
// or a reply, so that reading, writing and checking them is written
// once.
type descriptors struct {
	media          *MediaDescriptor
	events         *EventsDescriptor
	signals        *SignalsDescriptor
	observedEvents *ObservedEventsDescriptor
	audit          *AuditDescriptor
	statistics     StatisticsDescriptor
	packages       PackagesDescriptor
	// serviceChange and serviceChangeResult are the two forms of the
	// Services descriptor, in a request and in a reply.
	serviceChange       *ServiceChangeParm
	serviceChangeResult *ServiceChangeResParm
	errorDescriptor     *ErrorDescriptor
}

// requestDescriptors lists, by its token, each descriptor that a
// command request may carry (ammParameter, subtractRequest,
// auditRequest, notifyRequest and serviceChangeRequest in RFC 3525
// Annex B.2), as far as this package supports them.
var requestDescriptors = map[Command][]string{
	Add:             {tokMedia, tokEvents, tokSignals, tokAudit},
	Move:            {tokMedia, tokEvents, tokSignals, tokAudit},
	Modify:          {tokMedia, tokEvents, tokSignals, tokAudit},
	Subtract:        {tokAudit},
	AuditValue:      {tokAudit},
	AuditCapability: {tokAudit},
	Notify:          {tokObservedEvents},
	ServiceChange:   {tokServices},
}

// requiredInRequest names the descriptor that a command request is
// nothing without.
var requiredInRequest = map[Command]string{
	AuditValue:      tokAudit,
	AuditCapability: tokAudit,
	Notify:          tokObservedEvents,
	ServiceChange:   tokServices,
}

// terminationAudit lists the descriptors that report the state of a
// termination in a reply (auditReturnParameter in the grammar).
var terminationAudit = []string{tokMedia, tokEvents, tokSignals, tokObservedEvents, tokStatistics, tokPackages, tokError}

// replyDescriptors does the same for a command reply (ammsReply,
// auditReply, notifyReply and serviceChangeReply).
var replyDescriptors = map[Command][]string{
	Add:             terminationAudit,
	Move:            terminationAudit,
	Modify:          terminationAudit,
	Subtract:        terminationAudit,
	AuditValue:      terminationAudit,
	AuditCapability: terminationAudit,
	Notify:          {tokError},
	ServiceChange:   {tokServices, tokError},
}

func (c CommandRequest) descriptors() descriptors {
	return descriptors{
		media:          c.Media,
		events:         c.Events,
		signals:        c.Signals,
		audit:          c.Audit,
		observedEvents: c.ObservedEvents,
		serviceChange:  c.ServiceChange,
	}
}

func (c *CommandRequest) setDescriptors(d descriptors) {
	c.Media = d.media
	c.Events = d.events
	c.Signals = d.signals
	c.Audit = d.audit
	c.ObservedEvents = d.observedEvents
	c.ServiceChange = d.serviceChange
}

func (c CommandReply) descriptors() descriptors {
	return descriptors{
		media:               c.Media,
		events:              c.Events,
		signals:             c.Signals,
		observedEvents:      c.ObservedEvents,
		statistics:          c.Statistics,
		packages:            c.Packages,
		serviceChangeResult: c.ServiceChangeResult,
		errorDescriptor:     c.Error,
	}
}

func (c *CommandReply) setDescriptors(d descriptors) {
	c.Media = d.media
	c.Events = d.events
	c.Signals = d.signals
	c.ObservedEvents = d.observedEvents
	c.Statistics = d.statistics
	c.Packages = d.packages
	c.ServiceChangeResult = d.serviceChangeResult
	c.Error = d.errorDescriptor
}

// present lists the descriptors that are set, by their tokens.
func (d descriptors) present() []string {
	var names []string
	if d.media != nil {
		names = append(names, tokMedia)
	}
	if d.events != nil {
		names = append(names, tokEvents)
	}
	if d.signals != nil {
		names = append(names, tokSignals)
	}
	if d.observedEvents != nil {
		names = append(names, tokObservedEvents)
	}
	if d.audit != nil {
		names = append(names, tokAudit)
	}
	if d.statistics != nil {
		names = append(names, tokStatistics)
	}
	if d.packages != nil {
		names = append(names, tokPackages)
	}
	if d.serviceChange != nil || d.serviceChangeResult != nil {
		names = append(names, tokServices)
	}
	if d.errorDescriptor != nil {
		names = append(names, tokError)
	}
	return names
}

// check reports a descriptor that the command may not carry. A media
// gateway answers such a command with error 447, "Descriptor not legal
// in this command".
func (d descriptors) check(c Command, legal map[Command][]string) error {
	allowed, ok := legal[c]
	if !ok {
		return fmt.Errorf("%q is not a command", string(c))
	}
	for _, name := range d.present() {
		if !slices.Contains(allowed, name) {
			return fmt.Errorf("a %s descriptor is not legal in this %s", name, c)
		}
	}
	return nil
}

// checkRequest checks the descriptors of a command request, including
// the one that the command cannot do without.
func (d descriptors) checkRequest(c Command) error {
	if err := d.check(c, requestDescriptors); err != nil {
		return err
	}
	if required, ok := requiredInRequest[c]; ok && !slices.Contains(d.present(), required) {
		return fmt.Errorf("a %s request needs a %s descriptor", c, required)
	}
	return nil
}

// checkReply checks the descriptors of a command reply.
func (d descriptors) checkReply(c Command) error {
	if err := d.check(c, replyDescriptors); err != nil {
		return err
	}
	// serviceChangeReply in the grammar holds one or the other.
	if d.serviceChangeResult != nil && d.errorDescriptor != nil {
		return fmt.Errorf("the reply to a %s carries either a %s descriptor or an %s descriptor", c, tokServices, tokError)
	}
	return nil
}

// commandRequest reads a command request whose prefixes and first
// token, name, have been taken already.
func (s *scanner) commandRequest(name string) (CommandRequest, error) {
	c := CommandRequest{Command: Command(name)}
	if _, ok := requestDescriptors[c.Command]; !ok {
		return c, s.errorf("%q is not a command", name)
	}
	s.reach(inTerminationID)
	var d descriptors
	var err error
	if c.TerminationID, d, err = s.commandParameters(false); err != nil {
		return c, err
	}
	if err := d.checkRequest(c.Command); err != nil {
		return c, s.errorf("%v", err)
	}
	c.setDescriptors(d)
	return c, nil
}

// commandReply reads a command reply whose first token, name, has been
// taken already.
func (s *scanner) commandReply(name string) (CommandReply, error) {
	c := CommandReply{Command: Command(name)}
	if _, ok := replyDescriptors[c.Command]; !ok {
		return c, s.errorf("%q is not a command", name)
	}
	var d descriptors
	var err error
	if c.TerminationID, d, err = s.commandParameters(true); err != nil {
		return c, err
	}
	if err := d.checkReply(c.Command); err != nil {
		return c, s.errorf("%v", err)
	}
	c.setDescriptors(d)
	return c, nil
}

// commandParameters reads what follows the token of a command, in a
// request as in a reply: "= TerminationID [ { descriptor, ... } ]".
func (s *scanner) commandParameters(reply bool) (TerminationID, descriptors, error) {
	var d descriptors
	if err := s.expect('='); err != nil {
		return "", d, err
	}
	id, err := s.terminationID()
	if err != nil {
		return "", d, err
	}
	s.reach(inCommand)
	s.skipLWSP()
	if s.peek() != '{' {
		return id, d, nil
	}
	err = s.list(func() error { return s.descriptor(&d, reply) })
	return id, d, err
}

// descriptor reads one descriptor into d. reply tells the two forms of
// the ServiceChange descriptor apart.
func (s *scanner) descriptor(d *descriptors, reply bool) error {
	name, err := s.token()
	if err != nil {
		return err
	}
	// A media gateway answers a repeated descriptor with error 448,
	// "Descriptor appears twice in a command".
	if slices.Contains(d.present(), name) {
		return s.errorf("the %s descriptor appears twice in a command", name)
	}
	switch name {
	case tokMedia:
		d.media, err = s.mediaDescriptor()
	case tokEvents:
		d.events, err = s.eventsDescriptor()
	case tokSignals:
		d.signals, err = s.signalsDescriptor()
	case tokObservedEvents:
		d.observedEvents, err = s.observedEventsDescriptor()
	case tokAudit:
		d.audit, err = s.auditDescriptor()
	case tokStatistics:
		d.statistics, err = s.statisticsDescriptor()
	case tokPackages:
		d.packages, err = s.packagesDescriptor()
	case tokServices:
		if reply {
			d.serviceChangeResult, err = s.serviceChangeResParm()
		} else {
			d.serviceChange, err = s.serviceChangeParm()
		}
	case tokError:
		d.errorDescriptor, err = s.errorDescriptor()
	default:
		return s.errorf("%s is not a descriptor that this package supports", name)
	}
	return err
}

func (w *writer) commandRequest(c CommandRequest) {
	d := c.descriptors()
	if err := d.checkRequest(c.Command); err != nil {
		w.fail("%v", err)
		return
	}
	head := string(c.Command)
	if c.WildcardResponse {
		head = "W-" + head
	}
	if c.Optional {
		head = "O-" + head
	}
	w.command(head, c.TerminationID, d)
}

func (w *writer) commandReply(c CommandReply) {
	d := c.descriptors()
	if err := d.checkReply(c.Command); err != nil {
		w.fail("%v", err)
		return
	}
	w.command(string(c.Command), c.TerminationID, d)
}

func (w *writer) command(head string, id TerminationID, d descriptors) {
	if !validTerminationID(id) {
		w.fail("%q is not a termination ID", string(id))
	}
	head = fmt.Sprintf("%s = %s", head, id)
	var items []func()
	if d.media != nil {
		items = append(items, func() { w.mediaDescriptor(d.media) })
	}
	if d.events != nil {
		items = append(items, func() { w.eventsDescriptor(d.events) })
	}
	if d.signals != nil {
		items = append(items, func() { w.signalsDescriptor(d.signals) })
	}
	if d.observedEvents != nil {
		items = append(items, func() { w.observedEventsDescriptor(d.observedEvents) })
	}
	if d.audit != nil {
		items = append(items, func() { w.auditDescriptor(d.audit) })
	}
	if d.statistics != nil {
		items = append(items, func() { w.statisticsDescriptor(d.statistics) })
	}
	if d.packages != nil {
		items = append(items, func() { w.packagesDescriptor(d.packages) })
	}
	if d.serviceChange != nil {
		items = append(items, func() { w.serviceChangeParm(d.serviceChange) })
	}
	if d.serviceChangeResult != nil {
		items = append(items, func() { w.serviceChangeResParm(d.serviceChangeResult) })
	}
	if d.errorDescriptor != nil {
		items = append(items, func() { w.errorDescriptor(d.errorDescriptor) })
	}
	if len(items) == 0 {
		w.item(head)
		return
	}
	w.block(head, items)
}

// eatPrefix takes one of the prefixes "O-" and "W-" of a command
// request, in either case.
func (s *scanner) eatPrefix(prefix string) bool {
	s.skipLWSP()
	if len(s.b)-s.pos < len(prefix) || !strings.EqualFold(string(s.b[s.pos:s.pos+len(prefix)]), prefix) {
		return false
	}
	s.pos += len(prefix)
	return true
}

func (s *scanner) terminationID() (TerminationID, error) {
	s.skipLWSP()
	start := s.pos
	id := TerminationID(s.takeWhile(isPathNameChar))
	if !validTerminationID(id) {
		s.pos = start
		return "", s.errorf("expected a termination ID, found %s", s.found())
	}
	// Termination IDs are case insensitive (RFC 3525 Annex B.2). Only
	// ROOT is brought to one spelling, so that it can be compared
	// with TerminationRoot.
	if strings.EqualFold(string(id), string(TerminationRoot)) {
		return TerminationRoot, nil
	}
	return id, nil
}

func validTerminationID(id TerminationID) bool {
	return id == TerminationChoose || id == TerminationAll || validPathName(string(id))
}
