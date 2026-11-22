package mmtel

import (
	"context"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the B2BUA of a call does for a conference that
// one of its legs belongs to (TS 24.229 5.7.5.1): it names the dialog
// on its other side in a Replaces, and it passes on the Contact of a
// conference focus. The requests of a conference within a dialog, such
// as REFER, SUBSCRIBE and NOTIFY, go on like any other (forward).

// isfocus is the feature parameter of the Contact of a conference focus
// (RFC 3840 9, TS 24.147 5.3.2.3.1).
const isfocus = "isfocus"

// focusContact is the Contact of m when it is a conference focus's,
// which TS 24.229 5.7.5.1 has a routeing B2BUA pass on as it came: the
// URI there is the conference URI, which the user keeps (TS 24.147
// 5.3.1.3.2). The B2BUA then record-routes itself so that the requests
// to that URI still pass it (NOTE 3 there).
func focusContact(m *sip.Message) (sip.NameAddr, bool) {
	na, err := sip.ParseNameAddr(m.Get("Contact"))
	if err != nil {
		return sip.NameAddr{}, false
	}
	if _, ok := na.Params.Get(isfocus); !ok {
		return sip.NameAddr{}, false
	}
	return na, true
}

// passFocusContact gives the leg to the caller the Contact of resp
// when it is a focus's, before the response goes on to the caller.
func (c *call) passFocusContact(resp *sip.Message) {
	if contact, ok := focusContact(resp); ok {
		c.in.SetContact(contact, true)
	}
}

// replacesOnTheOtherSide is TS 24.229 5.7.5.1 for an initial INVITE
// with Replaces. When the dialog that it names is one leg of a call of
// this AS, the INVITE that goes on names the other leg instead: that is
// the dialog the next hop knows. Otherwise the Replaces goes on as it
// came (carried). fields are the header fields of the new INVITE. The
// error is a failure of the Store.
func (c *call) replacesOnTheOtherSide(fields *sip.Message) error {
	r, err := sip.ParseReplaces(fields.Get("Replaces"))
	if err != nil {
		return nil
	}
	// sip.IncomingCall carries no context; the Store bounds the read.
	translated, ok, err := otherSide(context.Background(), c.as.store, r)
	if err != nil || !ok {
		return err
	}
	fields.Set("Replaces", translated.String())
	c.log.Info("Replaces names the other leg of a call", "replaces", r.String(), "now", translated.String())
	return nil
}

// storeFailed answers the INVITE with 500 (RFC 3261 21.5.1) when the
// Store failed as the AS read the call that its Replaces names.
func (c *call) storeFailed(err error) {
	c.log.Warn("call store failed", "error", err.Error())
	c.in.Reject(500, c.as.warning("Call records unavailable"))
}
