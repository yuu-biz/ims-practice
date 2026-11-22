package b2bua

import (
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

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
	// passes on the responses of the other dialogs without theirs while
	// that dialog lasts (keepsSession).
	from string
	// inTag is the tag of the early dialog of the caller in which it got
	// that session description. A role that ends that early dialog with
	// a 199, to send the INVITE on to another callee (Attempt again),
	// gives the caller a new early dialog with a tag of its own (RFC 6228
	// 4), in which the caller has nothing yet.
	inTag string
	// last holds the last response with a session description of each
	// dialog of out, by To tag, as it came: the role sends the media to
	// the dialog that answers (Hooks.Answer), and the Call offers the
	// caller its session (OfferCaller).
	last map[string]*sip.Message
	// origin is the o= line of the last session description that went to
	// the caller, in whose session an offer of the Call's own goes
	// (sentToCaller).
	origin sdp.Origin
}

// toCaller is resp, a response of a dialog of out, as the caller may
// get it: without its session description when another dialog that the
// caller still follows gave it one already. It notes the session
// description of resp for its dialog.
func (c *Call) toCaller(out *sip.OutgoingCall, resp *sip.Message) *sip.Message {
	if !sip.HasSessionDescription(resp) {
		return resp
	}
	tag := toTag(resp)
	keeps := c.keepsSession(out)
	c.mu.Lock()
	c.early.last[tag] = resp
	if !keeps {
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

// keepsSession tells whether the caller keeps the session description
// that it has, so that those of the other early dialogs of out do not
// go to it. It keeps it while the dialog that sent it lasts. A 199 ends
// that dialog (RFC 6228 4), and the next session description then goes
// to the caller as it comes, when it answers the offer of the INVITE:
// with preconditions the UAS that sent it alerts its user only once the
// caller has confirmed its resources, which needs that answer (RFC 3312
// 6). After an INVITE without an offer the next session description of
// the same leg is the offer of another of its UASs, and the Call, whose
// caller has answered one already, sends no further offer in the early
// dialog of the caller (RFC 3261 13.2.1).
//
// A leg that the role gives up on, to send the INVITE on to another
// callee (Attempt again), leaves all the dialogs of that leg behind,
// whatever the INVITE carried: what the caller has of it is stale, and
// a role that ended the early dialog of the caller along with the leg
// has the offer of its INVITE open again, so the UAS of the new leg
// answers or offers into a dialog of its own.
func (c *Call) keepsSession(out *sip.OutgoingCall) bool {
	inTag := c.callerDialogTag()
	c.mu.Lock()
	from, was := c.early.from, c.early.inTag
	c.mu.Unlock()
	switch {
	case from == "":
		return false
	case was != inTag:
		// The early dialog in which the caller got it has ended, and the
		// caller has nothing in the one it has now.
		return false
	case len(c.in.Request.Body) == 0:
		return true
	}
	dialog, ok := dialogOf(out, from)
	return ok && dialog.State != sip.DialogTerminated
}

// dialogOf is the dialog of out whose remote tag is tag, and false when
// out has none: a leg that the role gave up on left all of its dialogs
// behind, so no tag of that leg is one of this leg's.
func dialogOf(out *sip.OutgoingCall, tag string) (sip.Dialog, bool) {
	for _, d := range out.Dialogs() {
		if dialog, ok := d.Dialog(); ok && dialog.ID.RemoteTag == tag {
			return dialog, true
		}
	}
	return sip.Dialog{}, false
}

// noteCallerDialog notes the early dialog of the caller as it is now:
// the one in which the caller has the session description it just got
// (earlyDialogs.inTag). The Call notes it once the response has gone,
// since the first response is what creates that early dialog.
func (c *Call) noteCallerDialog() {
	tag := c.callerDialogTag()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.early.inTag = tag
}

// callerDialogTag is the tag that the B2BUA gave the early dialog of
// the caller, "" while there is none.
func (c *Call) callerDialogTag() string {
	d, ok := c.in.Dialog()
	if !ok {
		return ""
	}
	return d.ID.LocalTag
}

// answerFrom is resp, the 2xx of a dialog of out, as the caller gets it,
// and the session description of that dialog when it is not the one
// whose session description the caller got: the 2xx then reaches the
// caller without its own, since the caller ignores a second one (RFC
// 3261 13.2.1). The Call offers it that session once the caller has
// acknowledged the 2xx (moveSession).
//
// What the caller got in an early dialog that has ended counts for
// nothing, and the 2xx brings it the session description of the dialog
// that answered, whichever that is: a role that gave a leg up and sent
// the INVITE on to another callee (Attempt again) ended that early
// dialog with a 199 (earlyDialogs.inTag).
func (c *Call) answerFrom(resp *sip.Message) (toCaller, session *sip.Message) {
	tag := toTag(resp)
	inTag := c.callerDialogTag()
	c.mu.Lock()
	if c.early.inTag != inTag {
		c.early.from = ""
	}
	if sip.HasSessionDescription(resp) {
		c.early.last[tag] = resp
	}
	from, kept := c.early.from, c.early.last[tag]
	c.mu.Unlock()
	if from == "" || from == tag {
		return resp, nil
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
