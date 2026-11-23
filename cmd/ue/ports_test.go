package main

import "testing"

// TestProtectedPorts: a protected port is a port of its own, never one
// of the standard SIP ports and never the other protected port of the
// same UE (TS 33.203 7.1). A -port-c of 0 leaves IPsec ESP off.
func TestProtectedPorts(t *testing.T) {
	for _, c := range []struct {
		portS, portC int
		ok           bool
	}{
		{5062, 0, true},
		{5062, 5063, true},
		{5062, 5062, false},
		{5062, 5060, false},
		{5062, 5061, false},
		{5062, 70000, false},
		{5062, -1, false},
		{5060, 0, false},
		{0, 0, false},
	} {
		err := protectedPorts(c.portS, c.portC)
		if c.ok && err != nil {
			t.Errorf("-port-s %d -port-c %d: %v", c.portS, c.portC, err)
		}
		if !c.ok && err == nil {
			t.Errorf("-port-s %d -port-c %d was accepted", c.portS, c.portC)
		}
	}
}
