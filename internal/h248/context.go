package h248

import "fmt"

// ContextRequest holds the attributes of a context that a request sets
// (RFC 3525 6.1.1). Iq and Ix use the priority and the emergency
// indicator, and the topology for access transfer only (TS 29.334 5.5,
// 5.7.9); Mp uses the topology with all three directions (TS 29.333
// 5.7.8).
//
// Asking for these attributes back (ContextAudit) is not supported:
// Iq and Ix do not audit them (TS 29.334 5.8.9), and tshark 4.2.2
// stops dissecting a message that carries a ContextAudit.
type ContextRequest struct {
	// Priority runs from 0, the lowest, to 15. nil leaves it out.
	Priority *uint16
	// Emergency marks the context as carrying an emergency call.
	Emergency bool
	// TopologyReq says between which terminations media flows. By
	// default, every termination of a context receives from all the
	// others (RFC 3525 7.1.18).
	TopologyReq []TopologyRequest
}

// TopologyRequest is one triple of the Topology descriptor: how media
// flows between two terminations, each of which may be a wildcard.
type TopologyRequest struct {
	TerminationFrom   TerminationID
	TerminationTo     TerminationID
	TopologyDirection TopologyDirection
}

// TopologyDirection is the direction of the flow in a triple.
type TopologyDirection string

const (
	// Isolate: neither termination receives from the other.
	Isolate TopologyDirection = "Isolate"
	// Oneway: TerminationTo receives from TerminationFrom only.
	Oneway TopologyDirection = "Oneway"
	// Bothway: each termination receives from the other.
	Bothway TopologyDirection = "Bothway"
)

// contextProperty reads into a the context property that the token
// name starts. It reports false when name does not start one.
func (s *scanner) contextProperty(name string, a *ActionRequest) (bool, error) {
	switch name {
	case tokTopology, tokPriority, tokEmergency:
	case tokContextAudit:
		return true, s.errorf("%s is not supported", name)
	default:
		return false, nil
	}
	if a.ContextRequest == nil {
		a.ContextRequest = &ContextRequest{}
	}
	c := a.ContextRequest
	switch {
	case name == tokTopology && c.TopologyReq == nil:
		return true, s.list(func() error {
			var t TopologyRequest
			var err error
			if t.TerminationFrom, err = s.terminationID(); err != nil {
				return err
			}
			if err = s.expect(','); err != nil {
				return err
			}
			if t.TerminationTo, err = s.terminationID(); err != nil {
				return err
			}
			if err = s.expect(','); err != nil {
				return err
			}
			direction, err := s.token()
			if err != nil {
				return err
			}
			if !isTopologyDirection(direction) {
				return s.errorf("%s is not a direction of a topology", direction)
			}
			t.TopologyDirection = TopologyDirection(direction)
			c.TopologyReq = append(c.TopologyReq, t)
			return nil
		})
	case name == tokPriority && c.Priority == nil:
		if err := s.expect('='); err != nil {
			return true, err
		}
		priority, err := s.uint(16)
		c.Priority = new(uint16(priority))
		return true, err
	case name == tokEmergency && !c.Emergency:
		c.Emergency = true
		return true, nil
	}
	return true, s.errorf("%s appears twice in an action", name)
}

func isTopologyDirection(name string) bool {
	switch TopologyDirection(name) {
	case Isolate, Oneway, Bothway:
		return true
	}
	return false
}

// contextRequest returns the context properties as items of an action.
// They come before the commands (RFC 3525 7.1.18).
func (w *writer) contextRequest(c *ContextRequest) []func() {
	var items []func()
	if c.Priority != nil {
		items = append(items, func() { w.item(fmt.Sprintf("%s = %d", tokPriority, *c.Priority)) })
	}
	if c.Emergency {
		items = append(items, func() { w.item(tokEmergency) })
	}
	if len(c.TopologyReq) > 0 {
		var triples []string
		for _, t := range c.TopologyReq {
			if !validTerminationID(t.TerminationFrom) || !validTerminationID(t.TerminationTo) {
				w.fail("%q and %q are not two termination IDs", string(t.TerminationFrom), string(t.TerminationTo))
			}
			if !isTopologyDirection(string(t.TopologyDirection)) {
				w.fail("%q is not a direction of a topology", string(t.TopologyDirection))
			}
			triples = append(triples, fmt.Sprintf("%s, %s, %s", t.TerminationFrom, t.TerminationTo, t.TopologyDirection))
		}
		items = append(items, func() { w.parmBlock(tokTopology, triples) })
	}
	return items
}
