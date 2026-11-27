package ue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the UE as a participant of a conference (TS 24.147
// 5.3.1), as GSMA IR.92 2.3.3 asks: a UE with two calls makes a
// three-way session of them by creating a conference at the conference
// factory URI and asking the focus, with a REFER in the dialog of the
// conference session, to invite each peer with a Replaces of the UE's
// call with that peer (5.3.1.3.3, option 2b of 5.3.1.5.3). A peer's UE
// takes the focus's INVITE with Replaces in the place of its call
// (5.3.1.4.3). Each participant may subscribe to the conference event
// package to learn who takes part (5.3.1.2).

// Conference is a conference session of the UE: the dialog with the
// focus, and the state of the conference that the NOTIFYs of the
// conference event package tell.
type Conference struct {
	// URI is the conference URI: the Contact of the focus, which has
	// isfocus (TS 24.147 5.3.1.3.2).
	URI string

	u       *UE
	ua      *sip.UA
	reg     Registration
	session *Session
	// call is the dialog with the focus; nil for a participant whom the
	// focus invited, which sends no REFER.
	call *sip.OutgoingCall

	mu      sync.Mutex
	doc     *confinfo.Document
	changed chan struct{} // closed and replaced on each new document
	sub     *sip.Subscription
	// inDialog is the subscription within the dialog of the conference
	// session, nil when there is none (Subscribe).
	inDialog *inDialogSubscription
}

// inDialogSubscription is a subscription within the dialog of the
// conference session, which shares that dialog (RFC 6665 4.5.2): a BYE
// of either end ends the session, not the subscription (RFC 5057 5.5).
// The UE keeps the dialog for it until the NOTIFY that ends it, or
// until it expires without one, as when that NOTIFY is lost (RFC 5057
// 4.2): end tells the SIP stack (sip.CallDialog.AddSubscriptionUsage).
type inDialogSubscription struct {
	end func()
	// stopExpiry stops the timer that ends the subscription at its
	// expiry, which each NOTIFY sets anew.
	stopExpiry func() bool
}

// ErrNotFocus is a conference session whose peer did not answer as a
// conference focus: its 200 had no isfocus in Contact.
var ErrNotFocus = errors.New("the answer has no Contact with isfocus")

// CreateConference is TS 24.147 5.3.1.3.2: a call to the conference
// factory URI, the default one of the home network (ims), which the
// focus answers with the conference URI. It returns once the focus has
// answered; the conference session then goes on as a call, with media
// and speech, until ctx ends or the focus ends it, and ended gets the
// result of the call. opts are those of the call; Answered is taken.
func (u *UE) CreateConference(ctx context.Context, ua *sip.UA, reg Registration, media *Media, speech Speech, opts CallOptions) (c *Conference, ended <-chan error, err error) {
	type answer struct {
		call *sip.OutgoingCall
		ok   *sip.Message
	}
	answered := make(chan answer, 1)
	var session *Session
	opts.StayUp = true
	opts.Answered = func(call *sip.OutgoingCall, ok *sip.Message) { answered <- answer{call, ok} }
	keep := opts.Session
	opts.Session = func(s *Session) {
		session = s
		if keep != nil {
			keep(s)
		}
	}
	done := make(chan error, 1)
	go func() { done <- u.Call(ctx, ua, reg, ims.ConferenceFactoryURI(u.cfg.HomeDomain), media, speech, opts) }()
	select {
	case a := <-answered:
		na, err := sip.ParseNameAddr(a.ok.Get("Contact"))
		if _, isFocus := na.Params.Get("isfocus"); err != nil || !isFocus {
			a.call.Bye(session.requestHeaders...)
			<-done
			return nil, nil, ErrNotFocus
		}
		c := &Conference{URI: na.URI, u: u, ua: ua, reg: reg, session: session, call: a.call, changed: make(chan struct{})}
		session.takeOther(c.receiveNotify)
		u.log.Info("conference created", "uri", c.URI)
		return c, done, nil
	case err := <-done:
		if err == nil {
			err = errors.New("the conference session ended before the focus answered")
		}
		return nil, nil, err
	}
}

// Invite is TS 24.147 5.3.1.5.3 as GSMA IR.92 2.3.3 asks it: a REFER
// in the dialog with the focus whose Refer-To is the peer of one of the
// UE's calls, with a Replaces of that call, so that the focus's INVITE
// takes the call over at the peer. peer is the peer's URI, and call the
// UE's dialog with it (Session.Dialog). It returns the status of the
// focus's INVITE, as the last NOTIFY of the REFER tells it: 200 once
// the peer has joined. The UE then releases its call with the peer
// unless the peer did first (5.3.1.3.3 step 3).
func (c *Conference) Invite(peer string, call sip.Dialog) (int, error) {
	if c.call == nil {
		return 0, errors.New("only the UE that created the conference refers here")
	}
	// The recipient of the INVITE with Replaces is the peer, whose local
	// tag is this UE's remote tag (RFC 3891 3).
	replaces := sip.Replaces{CallID: call.ID.CallID, ToTag: call.ID.RemoteTag, FromTag: call.ID.LocalTag}
	headers := with(c.session.requestHeaders, sip.HeaderField{Name: "Referred-By", Value: "<" + c.reg.DefaultIdentity() + ">"})
	r, err := c.call.Refer(sip.ReferTo{URI: c.u.sipURIOf(peer), Replaces: &replaces}, headers...)
	if err != nil {
		return 0, err
	}
	if r.Response.StatusCode >= 300 {
		return r.Response.StatusCode, fmt.Errorf("REFER answered %d %s", r.Response.StatusCode, r.Response.Reason)
	}
	status := 0
	for s := range r.Statuses() {
		status = s.StatusCode
		c.u.log.Info("REFER progress", "peer", peer, "status", s.StatusCode, "subscription_state", s.State.String())
	}
	if err := r.Err(); err != nil {
		return status, err
	}
	return status, nil
}

// sipURIOf is the SIP URI of a peer for a Refer-To with a Replaces: a
// tel URI becomes the SIP URI of its number in the home domain with
// user=phone (TS 24.147 5.3.1.5.3, RFC 3261 19.1.6).
func (u *UE) sipURIOf(peer string) string {
	if number, ok := ims.TelNumber(peer); ok {
		return "sip:" + number + "@" + u.cfg.HomeDomain + ";user=phone"
	}
	return peer
}

// Replacement is RFC 3891 3 for an INVITE with Replaces at this UE:
// the call it replaces and 0, or nil and the status code to refuse it
// with. peers are the identities of the peer of the replaced call that
// the UE knows, such as the URI it called and the P-Asserted-Identity
// of the answer. The UE takes a replacement that that peer asked for:
// the Referred-By of the focus's INVITE names it (TS 24.147 5.3.2.5.4
// step 4, RFC 3892).
//
// SIMPLIFIED: RFC 3891 3 and RFC 3892. The Referred-By carries no
// Authenticated Identity Body; the UE trusts the identity, which the
// referrer's network asserted to the focus.
func Replacement(ic *sip.IncomingCall, peers ...string) (*sip.CallDialog, int) {
	d, refusal := ic.ReplacedDialog()
	if refusal != 0 {
		return nil, refusal
	}
	by, err := sip.ParseNameAddr(ic.Request.Get("Referred-By"))
	if err != nil {
		return nil, 403
	}
	for _, peer := range peers {
		if ims.SamePublicIdentity(by.URI, peer) {
			return d, 0
		}
	}
	return nil, 403
}

// AssertedIdentities are the URIs of the P-Asserted-Identity of m: the
// identities of a peer that its network asserts (RFC 3325 9.1).
func AssertedIdentities(m *sip.Message) []string {
	var ids []string
	for _, v := range m.GetAll("P-Asserted-Identity") {
		if na, err := sip.ParseNameAddr(v); err == nil {
			ids = append(ids, na.URI)
		}
	}
	return ids
}

// IsReplacement reports whether an incoming INVITE asks to replace a
// call of the UE.
func IsReplacement(ic *sip.IncomingCall) bool { return ic.Request.Get("Replaces") != "" }

// FocusOf is the conference URI of an INVITE from a conference focus,
// its Contact with isfocus, and false for any other INVITE (TS 24.147
// 5.3.1.4.3).
func FocusOf(req *sip.Message) (string, bool) {
	na, err := sip.ParseNameAddr(req.Get("Contact"))
	if err != nil {
		return "", false
	}
	_, ok := na.Params.Get("isfocus")
	return na.URI, ok
}

// JoinedConference is the conference session of a participant whom the
// focus invited, on the call that session is in: the UE may subscribe
// to it like the creator (TS 24.147 5.3.1.4.3 step 2).
func (u *UE) JoinedConference(ua *sip.UA, reg Registration, uri string, s *Session) *Conference {
	c := &Conference{URI: uri, u: u, ua: ua, reg: reg, session: s, changed: make(chan struct{})}
	s.takeOther(c.receiveNotify)
	return c
}

// conferenceExpires is the duration the UE asks for its subscription:
// the default of the package (RFC 4575 3.3).
const conferenceExpires = time.Hour

// Subscribe is TS 24.147 5.3.1.2 as GSMA IR.92 2.3.3 asks it: a
// SUBSCRIBE to the conference URI outside the dialog of the conference
// session. When the network refuses it with 403, the UE subscribes
// within that dialog instead, and goes on without the event package if
// that fails too. The documents of the NOTIFYs show in Users.
func (c *Conference) Subscribe() error {
	changed := c.Changed()
	sub, err := c.ua.Subscribe(sip.SubscribeRequest{
		RequestURI: c.URI, From: c.reg.DefaultIdentity(), Route: c.u.routeSet(c.reg),
		Event: confinfo.EventPackage, Accept: confinfo.ContentType, Expires: conferenceExpires,
		Headers:      c.u.requestHeaders(),
		RefreshAfter: ims.RefreshAfter,
		OnNotify:     func(n sip.Notification, _ *sip.Message) { c.take(n.Body.Content) },
	})
	if err != nil {
		return err
	}
	select {
	case <-sub.Done():
	case <-changed:
		c.mu.Lock()
		c.sub = sub
		c.mu.Unlock()
		return nil
	case <-time.After(64 * sip.DefaultTimers().T1):
		return sip.ErrNoNotify
	}
	var refused *sip.SubscribeRefused
	if err := sub.Err(); !errors.As(err, &refused) || refused.StatusCode != 403 {
		return err
	}
	c.u.log.Info("SUBSCRIBE outside the dialog refused with 403; subscribing within the dialog of the conference session")
	d := c.dialog()
	// The usage is there before the SUBSCRIBE goes, since the first
	// NOTIFY may come before the 2xx.
	end, err := d.AddSubscriptionUsage()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.inDialog = &inDialogSubscription{end: end}
	c.expireInDialog(conferenceExpires)
	c.mu.Unlock()
	resp, err := d.Send(sip.SUBSCRIBE, sip.Body{}, with(c.session.requestHeaders,
		sip.HeaderField{Name: "Event", Value: confinfo.EventPackage},
		sip.HeaderField{Name: "Accept", Value: confinfo.ContentType},
		sip.HeaderField{Name: "Expires", Value: fmt.Sprint(int(conferenceExpires / time.Second))})...)
	switch {
	case err != nil:
		c.endInDialog()
		return err
	case resp.StatusCode >= 300:
		c.endInDialog()
		return fmt.Errorf("SUBSCRIBE within the dialog answered %d %s", resp.StatusCode, resp.Reason)
	}
	return nil
}

// withinTheSession is what the UE does within the dialog of the
// conference session: an OutgoingCall or an IncomingCall.
type withinTheSession interface {
	Send(method sip.Method, body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	AddSubscriptionUsage() (end func(), err error)
}

// dialog is where requests within the conference session go.
func (c *Conference) dialog() withinTheSession {
	if c.call != nil {
		return c.call
	}
	return c.session.dialog.(withinTheSession)
}

// expireInDialog has the subscription within the dialog end after d,
// when no NOTIFY has come by then to end it or to set its expiry anew.
// The focus ends a subscription that expires with a NOTIFY (RFC 6665
// 4.2.1.4), which may take a transaction's time to come, so the UE
// waits that long past d. c.mu is held.
func (c *Conference) expireInDialog(d time.Duration) {
	sub := c.inDialog
	if sub.stopExpiry != nil {
		sub.stopExpiry()
	}
	sub.stopExpiry = time.AfterFunc(d+64*sip.DefaultTimers().T1, func() {
		c.u.log.Info("conference subscription within the dialog expired without a NOTIFY that ends it")
		c.endInDialog()
	}).Stop
}

// endInDialog ends the subscription within the dialog, if there is one.
func (c *Conference) endInDialog() {
	c.mu.Lock()
	sub := c.inDialog
	c.inDialog = nil
	c.mu.Unlock()
	if sub == nil {
		return
	}
	if sub.stopExpiry != nil {
		sub.stopExpiry()
	}
	sub.end()
}

// receiveNotify takes a NOTIFY of the conference event package within
// the dialog of the conference session, for a subscription in that
// dialog (Session.takeOther).
func (c *Conference) receiveNotify(r *sip.InDialogRequest) bool {
	if r.Request.Method != sip.NOTIFY || sip.EventType(r.Request.Get("Event")) != confinfo.EventPackage {
		return false
	}
	c.take(r.Request.Body)
	r.Accept(sip.Body{}, c.session.responseHeaders...)
	state, err := sip.ParseSubscriptionState(r.Request.Get("Subscription-State"))
	switch {
	case err != nil:
	case state.State == sip.SubscriptionTerminated:
		c.endInDialog()
	case state.Expires > 0:
		// RFC 6665 4.1.3: the expires of an active or pending state is
		// how long the subscription has left.
		c.mu.Lock()
		if c.inDialog != nil {
			c.expireInDialog(state.Expires)
		}
		c.mu.Unlock()
	}
	return true
}

// take keeps the document of a NOTIFY when it is newer than the one the
// UE has (RFC 4575 4.6); every document of this focus is the full state.
func (c *Conference) take(body []byte) {
	doc, err := confinfo.Parse(body)
	if err != nil {
		c.u.log.Warn("unusable conference document", "error", err.Error())
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.doc != nil && doc.Version <= c.doc.Version {
		return
	}
	c.doc = &doc
	c.u.log.Info("conference state", "uri", c.URI, "version", doc.Version, "users", usersOf(doc))
	close(c.changed)
	c.changed = make(chan struct{})
}

// Changed is closed when the next document comes.
func (c *Conference) Changed() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.changed
}

// ConferenceUser is one user of a conference as the event package tells
// it (RFC 4575 5.6, 5.7).
type ConferenceUser struct {
	Entity      string `json:"entity"`
	DisplayText string `json:"display_text,omitempty"`
	// Status is that of the user's first endpoint, such as connected or
	// on-hold.
	Status string `json:"status"`
}

// Users are the users of the last document, nil before the first.
func (c *Conference) Users() []ConferenceUser {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.doc == nil {
		return nil
	}
	return usersOf(*c.doc)
}

func usersOf(doc confinfo.Document) []ConferenceUser {
	var users []ConferenceUser
	if doc.Users == nil {
		return nil
	}
	for _, u := range doc.Users.Users {
		cu := ConferenceUser{Entity: u.Entity, DisplayText: u.DisplayText}
		if len(u.Endpoints) > 0 {
			cu.Status = string(u.Endpoints[0].Status)
		}
		users = append(users, cu)
	}
	return users
}

// Unsubscribe ends the subscription outside the dialog. TS 24.147
// 5.3.1.6.1 has a participant that leaves let the subscription run out
// instead, since the focus ends it with the next NOTIFY.
func (c *Conference) Unsubscribe() error {
	c.mu.Lock()
	sub := c.sub
	c.mu.Unlock()
	if sub == nil {
		return nil
	}
	return sub.Unsubscribe()
}
