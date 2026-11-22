package b2bua

import "github.com/yuu-biz/ims-practice/internal/sip"

// earlyDialogs are the early dialogs of the callee's side as the caller
// sees them. The INVITE of out may fork beyond the B2BUA, and each UAS
// that answers it has an early dialog of its own (RFC 3261 13.2.2.4).
// The caller has one early dialog with the B2BUA, which is one UAS: all
// its responses have one To tag (RFC 3261 8.2.6.2). Guarded by the mu
// of the Call.
type earlyDialogs struct {
	// from is the To tag of the dialog of out whose session description
	// the caller got, the first that sent one; the caller takes the
	// first as the answer and ignores the rest (13.2.1), so the Call
	// passes on the responses of the other dialogs without theirs.
	from string
	// last holds the last response with a session description of each
	// dialog of out, by To tag, as it came: the role sends the media to
	// the dialog that answers (Hooks.Answer).
	last map[string]*sip.Message
}

// toCaller is resp, a response of a dialog of out, as the caller may
// get it: without its session description when another dialog of out
// gave the caller one already. It notes the session description of resp
// for its dialog.
func (c *Call) toCaller(resp *sip.Message) *sip.Message {
	if !sip.HasSessionDescription(resp) {
		return resp
	}
	tag := toTag(resp)
	c.mu.Lock()
	c.early.last[tag] = resp
	if c.early.from == "" {
		c.early.from = tag
	}
	from := c.early.from
	c.mu.Unlock()
	if from == tag {
		return resp
	}
	c.log.Info("session description of another early dialog of the callee's side not passed on to the caller",
		"status", resp.StatusCode, "to_tag", tag, "caller_has", from)
	return withoutBody(resp)
}

// answerFrom is resp, the 2xx of a dialog of out, as the caller gets it,
// and the session description of that dialog when it is not the one
// whose session description the caller got: the 2xx then reaches the
// caller without its own, since the caller ignores a second one (RFC
// 3261 13.2.1).
//
// SIMPLIFIED: RFC 3261 13.2.1 and 14.1. The caller keeps the session
// description of the dialog that sent one first. The B2BUA does not
// offer the caller the session of the dialog that answered in a
// re-INVITE, so the call goes on with the codec that the first dialog
// chose; the media flows when both chose the same. After an INVITE
// without an offer, the offer of a dialog that the caller did not
// answer gets no answer from the B2BUA, and that UAS ends its dialog.
func (c *Call) answerFrom(resp *sip.Message) (toCaller, session *sip.Message) {
	tag := toTag(resp)
	c.mu.Lock()
	from, kept := c.early.from, c.early.last[tag]
	c.mu.Unlock()
	if from == "" || from == tag {
		return resp, nil
	}
	if sip.HasSessionDescription(resp) {
		kept = resp
	}
	c.log.Info("answered in another early dialog than the one whose session description the caller has",
		"to_tag", tag, "caller_has", from)
	return withoutBody(resp), kept
}

// toTag is the To tag of m.
func toTag(m *sip.Message) string {
	na, err := sip.ParseNameAddr(m.Get("To"))
	if err != nil {
		return ""
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// withoutBody is a copy of m without its body.
func withoutBody(m *sip.Message) *sip.Message {
	m = m.Clone()
	m.Body = nil
	m.Remove("Content-Type")
	return m
}
