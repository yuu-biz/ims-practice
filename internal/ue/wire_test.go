package ue_test

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// wireLog keeps the SIP messages that a UE sent and received, so that a
// test can check the sequence of a call as a capture would show it.
type wireLog struct {
	mu       sync.Mutex
	messages []wired
}

// wired is one message on the wire: "> INVITE" for a request the UE
// sent, "< 183 INVITE" for a response it received.
type wired struct {
	label   string
	message *sip.Message
}

func (w *wireLog) observe(c sip.Capture) {
	m := c.Message
	label := "< "
	if c.Sent {
		label = "> "
	}
	if m.Method != "" {
		label += string(m.Method)
	} else {
		cseq, _ := sip.ParseCSeq(m.Get("CSeq"))
		label += strconv.Itoa(m.StatusCode) + " " + string(cseq.Method)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// A retransmission repeats what the log has already.
	for _, old := range w.messages {
		if old.label == label && old.message.Get("CSeq") == m.Get("CSeq") && old.message.Get("Call-ID") == m.Get("Call-ID") &&
			old.message.Get("RSeq") == m.Get("RSeq") {
			return
		}
	}
	w.messages = append(w.messages, wired{label, m})
}

// calls is the log of the messages of calls, without REGISTER and the
// reg event.
func (w *wireLog) calls() []wired {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []wired
	for _, m := range w.messages {
		if strings.Contains(m.label, "REGISTER") || strings.Contains(m.label, "SUBSCRIBE") || strings.Contains(m.label, "NOTIFY") {
			continue
		}
		out = append(out, m)
	}
	return out
}

// find returns the first message with label, waiting for it.
func (w *wireLog) find(t *testing.T, label string) *sip.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range w.calls() {
			if m.label == label {
				return m.message
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %q among %v", label, w.labels())
	return nil
}

func (w *wireLog) labels() []string {
	var labels []string
	for _, m := range w.calls() {
		labels = append(labels, m.label)
	}
	return labels
}

// inOrder checks that the labels come in this order, with others in
// between allowed.
func (w *wireLog) inOrder(t *testing.T, labels ...string) {
	t.Helper()
	got := w.labels()
	i := 0
	for _, l := range got {
		if i < len(labels) && l == labels[i] {
			i++
		}
	}
	if i < len(labels) {
		t.Errorf("messages %v; want %v in this order", got, labels)
	}
}

func has(m *sip.Message, header, value string) bool {
	for _, v := range m.GetAll(header) {
		if v == value {
			return true
		}
	}
	return false
}

func sdpHas(m *sip.Message, lines ...string) error {
	body := string(m.Body)
	for _, l := range lines {
		if !strings.Contains(body, l+"\r\n") {
			return fmt.Errorf("no %q in\n%s", l, body)
		}
	}
	return nil
}
