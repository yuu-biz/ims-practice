package ue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what a Phone does with two calls: it goes between
// them (Swap), and makes a three-way session of them (Merge, TS 24.147
// 5.3.1.3.3, GSMA IR.92 2.3.3). It also takes the INVITE of a focus that
// replaces one of its calls (replace, 5.3.1.4.3).

// ConferenceStatus is the conference that the UE takes part in, as its
// Status shows it: the conference URI and the users that the last
// NOTIFY of the conference event package listed.
type ConferenceStatus struct {
	URI   string           `json:"uri"`
	Users []ConferenceUser `json:"users"`
}

// Swap puts the call in the foreground on hold and takes the held call
// back (TS 24.610), as a user goes from one call to the other.
func (p *Phone) Swap() error {
	active, held := p.line.active(), p.line.heldCall()
	if active == nil || held == nil {
		return ErrNoTwoCalls
	}
	if err := p.line.sessionOf(active).Hold(); err != nil {
		return err
	}
	err := p.line.sessionOf(held).Resume()
	p.publish()
	return err
}

// Merge makes a three-way session of the two answered calls on the line
// (TS 24.147 5.3.1.3.3): it creates a conference at the conference
// factory URI, then asks the focus with a REFER for each peer to invite
// it with a Replaces of the call with it (option 2b, which IR.92 2.3.3
// asks for), one after the other (IR.92 2.3.3 NOTE 4). Once a NOTIFY
// reports that a peer has joined, the UE ends its call with that peer,
// unless the peer did first (step 3). Merge returns when both REFERs
// are done; the conference session then stays on the line, and the
// Status shows who takes part, as the UE subscribes to the conference
// event package (5.3.1.2).
func (p *Phone) Merge() error {
	p.mu.Lock()
	reg, registered := p.reg, p.registered
	p.mu.Unlock()
	if !registered {
		return ErrNotRegistered
	}
	var calls []*lineCall
	for _, c := range p.line.all() {
		if p.line.infoOf(c).State == CallActive && p.line.conferenceOf(c) == nil {
			calls = append(calls, c)
		}
	}
	if len(calls) != 2 {
		return ErrNoTwoCalls
	}
	if !p.startCall() {
		return ErrStopping
	}
	media, err := ListenMedia(p.cfg.MediaListen)
	if err != nil {
		p.calls.Done()
		return err
	}
	ctx, hangUp := context.WithCancel(p.life)
	c := &lineCall{info: CallInfo{Direction: Outgoing, Peer: ims.ConferenceFactoryURI(p.ue.cfg.HomeDomain), State: CallCalling,
		Started: time.Now()}, hangUp: hangUp}
	p.line.seizeBeyond(c)
	p.publish()
	conf, ended, err := p.ue.CreateConference(ctx, p.ua, reg, media, p.cfg.Speech, CallOptions{
		Progress: p.progress(c), Session: func(s *Session) { p.line.setSession(c, s) }, Changed: p.publish})
	if err != nil {
		hangUp()
		media.Close()
		p.endCall(c)
		return fmt.Errorf("conference not created: %w", err)
	}
	p.line.setConference(c, conf)
	go func() {
		err := <-ended
		hangUp()
		media.Close()
		p.logEnd(c, err)
		p.endCall(c)
	}()
	go p.follow(conf, ctx)
	if err := conf.Subscribe(); err != nil {
		// 5.3.1.2: the conference goes on without the event package.
		p.log.Warn("no subscription to the conference", "error", err.Error())
	}
	var errs []error
	for _, old := range calls {
		info := p.line.infoOf(old)
		d, ok := p.line.sessionOf(old).Dialog()
		if !ok {
			continue
		}
		status, err := conf.Invite(info.Peer, d)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s not invited: %w", info.Peer, err))
		case status != 200:
			errs = append(errs, fmt.Errorf("%s not invited: the focus got %d", info.Peer, status))
		default:
			p.line.hangUp(old)
		}
	}
	p.publish()
	return errors.Join(errs...)
}

// follow publishes the status each time a NOTIFY of the conference
// event package changes the conference, until ctx ends.
func (p *Phone) follow(conf *Conference, ctx context.Context) {
	for {
		select {
		case <-conf.Changed():
			p.publish()
		case <-ctx.Done():
			return
		}
	}
}

// replace is TS 24.147 5.3.1.4.3 at a peer of a UE that merges its
// calls: the focus's INVITE with Replaces of a call of this UE (RFC
// 3891 3). The UE answers it at once, without alerting, in the place of
// that call, and then ends that call with BYE. When the INVITE comes
// from a focus, the UE subscribes to the conference.
func (p *Phone) replace(ic *sip.IncomingCall) {
	d, refusal := ic.ReplacedDialog()
	var old *lineCall
	if refusal == 0 {
		replaced, _ := d.Dialog()
		for _, c := range p.line.all() {
			if s := p.line.sessionOf(c); s != nil {
				if mine, ok := s.Dialog(); ok && mine.ID == replaced.ID {
					old = c
				}
			}
		}
		if old == nil {
			refusal = 481
		} else {
			_, refusal = Replacement(ic, p.line.peersOf(old)...)
		}
	}
	if refusal != 0 {
		p.log.Info("replacement refused", "status", refusal, "replaces", ic.Request.Get("Replaces"))
		ic.Reject(refusal, p.ue.responseHeaders()...)
		p.calls.Done()
		return
	}
	focus, fromFocus := FocusOf(ic.Request)
	peer := focus
	if !fromFocus {
		peer = p.line.infoOf(old).Peer
	}
	c := &lineCall{info: CallInfo{CallID: ic.Request.Get("Call-ID"), Direction: Incoming, Peer: peer, State: CallRinging,
		Started: time.Now()}, ic: ic, pickUp: make(chan time.Time, 1)}
	p.line.seizeBeyond(c)
	defer p.endCall(c)
	p.publish()
	media, err := ListenMedia(p.cfg.MediaListen)
	if err != nil {
		ic.Reject(500)
		p.logEnd(c, err)
		return
	}
	defer media.Close()
	ctx, hangUp := context.WithCancel(p.life)
	defer hangUp()
	p.line.setHangUp(c, hangUp)
	pickUp(c)
	report := p.progress(c)
	var once sync.Once
	var session *Session
	opts := AnswerOptions{PickUp: c.pickUp, Changed: p.publish,
		Session: func(s *Session) {
			session = s
			p.line.setSession(c, s)
		},
		Progress: func(state CallState, callID string) {
			report(state, callID)
			if state != CallActive {
				return
			}
			once.Do(func() {
				go func() {
					if err := d.Bye(); err != nil && !errors.Is(err, sip.ErrCallEnded) {
						p.log.Warn("BYE of the replaced call failed", "error", err.Error())
					}
				}()
				if !fromFocus {
					return
				}
				p.mu.Lock()
				reg := p.reg
				p.mu.Unlock()
				conf := p.ue.JoinedConference(p.ua, reg, focus, session)
				p.line.setConference(c, conf)
				go p.follow(conf, ctx)
				go func() {
					if err := conf.Subscribe(); err != nil {
						p.log.Warn("no subscription to the conference", "error", err.Error())
					}
				}()
			})
		}}
	p.log.Info("taking a replacement", "replaces", ic.Request.Get("Replaces"), "focus", fromFocus)
	p.logEnd(c, p.ue.Answer(ctx, ic, media, p.cfg.Speech, opts))
}
