package mmtel_test

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// wire keeps every SIP message that a node or a UE of the network
// received, with the name of the one that received it, and the
// addresses of the UEs.
type wire struct {
	mu  sync.Mutex
	got []arrival
	ues map[netip.AddrPort]bool
}

type arrival struct {
	at   string
	from netip.AddrPort
	m    *sip.Message
}

func (w *wire) observer(at string) sip.Observer {
	return func(c sip.Capture) {
		if c.Sent {
			return
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if isUE(at) {
			if w.ues == nil {
				w.ues = map[netip.AddrPort]bool{}
			}
			w.ues[c.Destination] = true
		}
		w.got = append(w.got, arrival{at, c.Source, c.Message})
	}
}

// arrivals are the messages that the nodes and the UEs received, and
// fromUE tells those that a UE sent.
func (w *wire) arrivals() (all []arrival, fromUE func(arrival) bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ues := map[netip.AddrPort]bool{}
	for addr := range w.ues {
		ues[addr] = true
	}
	return append([]arrival(nil), w.got...), func(a arrival) bool { return ues[a.from] }
}

func isUE(name string) bool { return strings.HasPrefix(name, "UE ") }

// TestOneICIDThroughTheIMS is the charging vector of a call through the
// whole IMS. The P-CSCF of the caller writes the icid-value (TS 24.229
// 5.2.6.3.3 step 7), and every node after it, in each of the three
// dialogs of the call, gets the INVITE and the responses to it with
// that icid-value: the S-CSCF (5.4.3.2 step 6, 5.4.3.3 step 6), the
// MMTel AS (5.7.5.1), the I-CSCF (5.3.2.1) and the P-CSCF of the
// callee. No UE gets a P-Charging-Vector (5.2.1).
func TestOneICIDThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		answered <- callee.ue.Answer(context.Background(), ic, callee.media, ue.Speech{PCM: ue.Tone(880, 200*time.Millisecond)}, ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond)})
	}()
	if err := caller.ue.Call(context.Background(), caller.ua, caller.reg, callee.tel, caller.media, ue.Speech{PCM: ue.Tone(440, 200*time.Millisecond)}, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, answered, "end of the callee's call"); err != nil {
		t.Fatal(err)
	}

	var icid string
	reached := map[string]bool{}
	arrivals, fromUE := n.wire.arrivals()
	for _, a := range arrivals {
		cseq, _ := sip.ParseCSeq(a.m.Get("CSeq"))
		if isUE(a.at) {
			if pcv := a.m.Get("P-Charging-Vector"); pcv != "" {
				t.Errorf("%s got a message of %s %d with P-Charging-Vector %q", a.at, cseq.Method, a.m.StatusCode, pcv)
			}
			continue
		}
		// What a UE sends carries no vector of the network, and each hop
		// writes its 100 for itself.
		if cseq.Method != sip.INVITE || fromUE(a) || a.m.StatusCode == 100 {
			continue
		}
		vector, ok := sip.ChargingVectorOf(a.m)
		if icid == "" && ok {
			icid = vector.ICID
		}
		if !ok || vector.ICID != icid {
			t.Errorf("%s got an INVITE or a response to one (%d) with P-Charging-Vector %q, want the icid-value %q", a.at, a.m.StatusCode, a.m.Get("P-Charging-Vector"), icid)
		}
		if a.m.Method == sip.INVITE {
			reached[a.at] = true
		}
	}
	for _, node := range []string{"S-CSCF", "MMTel AS", "I-CSCF", "P-CSCF"} {
		if !reached[node] {
			t.Errorf("no INVITE of the call reached the %s", node)
		}
	}
}
