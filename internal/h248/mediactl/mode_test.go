package mediactl

import (
	"context"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/h248/h248test"
)

// modeOnTheWire checks that every Add and Modify of an ephemeral
// termination that the controller sent carries its stream mode in the
// encoded text. H.248.1 (05/2002) 7.1.7 makes Inactive the mode of a
// termination whose LocalControl leaves it out, and the HTTP API of the
// media relay takes a missing mode as sendrecv, so the H.248 side may
// never leave it out. It returns the modes it found, in order.
func modeOnTheWire(t *testing.T, ds []h248test.Datagram) []h248.StreamMode {
	t.Helper()
	var modes []h248.StreamMode
	seen := map[uint32]bool{}
	for _, d := range ds {
		if d.From != controllerAddr {
			continue
		}
		m, err := h248.Parse(d.Payload)
		if err != nil {
			t.Fatalf("datagram %q: %v", d.Payload, err)
		}
		for _, tr := range m.Transactions {
			r, ok := tr.(h248.TransactionRequest)
			if !ok || seen[r.TransactionID] {
				continue
			}
			seen[r.TransactionID] = true
			commands := 0
			for _, a := range r.Actions {
				for _, c := range a.Commands {
					if (c.Command != h248.Add && c.Command != h248.Modify) || strings.EqualFold(string(c.TerminationID), string(h248.TerminationRoot)) {
						continue
					}
					commands++
					if c.Media == nil || c.Media.OneStream == nil || c.Media.OneStream.LocalControl == nil || c.Media.OneStream.LocalControl.StreamMode == "" {
						t.Errorf("%s = %s has no stream mode:\n%s", c.Command, c.TerminationID, d.Payload)
						continue
					}
					modes = append(modes, c.Media.OneStream.LocalControl.StreamMode)
				}
			}
			if got := strings.Count(string(d.Payload), "Mode = "); got < commands {
				t.Errorf("%d commands but %d modes in the text:\n%s", commands, got, d.Payload)
			}
		}
	}
	return modes
}

func TestIqWritesTheModeOnEveryAddAndModify(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.Datagrams())
	ctx := context.Background()
	r, err := reserve(ctx, b.controller, agw.Media{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.controller.Modify(ctx, r.ID, agw.Access, agw.TerminationRequest{Remote: mustAddrPort("192.0.2.30:49170")}); err != nil {
		t.Fatal(err)
	}
	if err := b.controller.Modify(ctx, r.ID, agw.Core, agw.TerminationRequest{Remote: mustAddrPort("203.0.113.40:50000")}); err != nil {
		t.Fatal(err)
	}
	if err := b.controller.Subtract(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	modes := modeOnTheWire(t, b.net.Datagrams()[before:])
	if len(modes) != 4 {
		t.Fatalf("modes %v, want those of two Adds and two Modifies", modes)
	}
	for _, m := range modes {
		if m != h248.SendRecv {
			t.Errorf("mode %s, want SendReceive", m)
		}
	}
}
