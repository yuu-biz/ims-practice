package ue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Phone is the UE of "ue serve": it stays up, keeps its registration,
// takes calls by its answer policy, and makes the calls a controller,
// such as the dashboard, asks for. Handler is its HTTP API.
type Phone struct {
	ue   *UE
	ua   *sip.UA
	line *Line
	cfg  PhoneConfig
	log  *slog.Logger

	// life is the context of every call; stopping the phone ends it,
	// which hangs the calls up.
	life     context.Context
	endLife  context.CancelFunc
	calls    sync.WaitGroup
	requests chan regRequest
	done     chan struct{} // closed when Serve returns
	// unanswered tells Serve that a request of the UE got no response
	// at all (noteUnanswered).
	unanswered chan struct{}

	mu         sync.Mutex
	reg        Registration
	registered bool
	policy     AnswerPolicy
	stopping   bool
	watchers   map[chan Status]bool
}

// PhoneConfig is what a Phone does with each call.
type PhoneConfig struct {
	// MediaListen is the local UDP address of the RTP socket that each
	// call opens, such as "0.0.0.0:0".
	MediaListen string
	// Speech is what the UE plays once in each call.
	Speech Speech
	// Policy is the answer policy until the controller sets another.
	Policy AnswerPolicy
}

// AnswerMode is how a Phone takes an incoming call.
type AnswerMode string

const (
	// AnswerAuto answers after the call has rung for AnswerPolicy.Ring.
	AnswerAuto AnswerMode = "auto"
	// AnswerReject turns every call down with AnswerPolicy.Reject.
	AnswerReject AnswerMode = "reject"
	// AnswerManual lets the call ring until the controller answers it or
	// hangs up.
	AnswerManual AnswerMode = "manual"
)

// AnswerPolicy is how a Phone takes the next incoming call.
type AnswerPolicy struct {
	Mode   AnswerMode
	Ring   time.Duration // for AnswerAuto
	Reject int           // for AnswerReject, from 400 to 699
}

// DefaultRing is how long a UE rings before it answers when nothing else
// is asked for.
const DefaultRing = time.Second

// Status is the state of a Phone: GET /status returns it, and GET
// /events sends it on each change, a short message sent or received
// included.
type Status struct {
	// IMPU is the public identity the phone shows: the default public
	// identity once registered, the configured one before.
	IMPU       string `json:"impu"`
	Registered bool   `json:"registered"`
	// Call is the call in the foreground: the answered call that is not
	// on hold, or else the newest call; null while the line is free.
	Call *CallInfo `json:"call"`
	// Calls are all the calls on the line, oldest first: with call
	// waiting or a held call there are two (TS 24.615, TS 24.610).
	Calls []CallInfo `json:"calls"`
	// Conference is the conference that the UE takes part in, null
	// without one (TS 24.147 5.3.1).
	Conference   *ConferenceStatus `json:"conference"`
	AnswerPolicy AnswerPolicy      `json:"answer_policy"`
	SMS          SMSHistory        `json:"sms"`
}

// The errors a controller can cause.
var (
	ErrBusy          = errors.New("there is a call on the line already")
	ErrNotRegistered = errors.New("the UE is not registered")
	ErrNoCall        = errors.New("there is no call on the line")
	ErrNotRinging    = errors.New("there is no incoming call that rings")
	ErrStopping      = errors.New("the UE is stopping")
	ErrNotHeld       = errors.New("the call on the line is not on hold")
	ErrNoTwoCalls    = errors.New("there are not two answered calls on the line")
)

// regRequest asks Serve to register (or refresh) or to deregister.
type regRequest struct {
	register bool
	done     chan error
}

// NewPhone runs u on ua. line must be the one whose Ring is the
// UAConfig.OnCall of ua, made to take calls.
func NewPhone(u *UE, ua *sip.UA, line *Line, cfg PhoneConfig, log *slog.Logger) *Phone {
	life, endLife := context.WithCancel(context.Background())
	line.takeWaitingCalls()
	return &Phone{
		ue: u, ua: ua, line: line, cfg: cfg, log: log,
		life: life, endLife: endLife,
		requests:   make(chan regRequest),
		done:       make(chan struct{}),
		unanswered: make(chan struct{}, 1),
		policy:     cfg.Policy,
		watchers:   map[chan Status]bool{},
	}
}

// Serve registers the UE and keeps it registered until ctx ends; then it
// hangs up the call, deregisters and returns. After a registration or a
// refresh that fails, the UE waits and makes an initial registration
// again, waiting longer after each failure in a row (TS 24.229
// 5.1.1.2.1, 5.1.1.4.1), until it registers or the controller
// deregisters it.
func (p *Phone) Serve(ctx context.Context) error {
	defer close(p.done)
	taking := make(chan struct{})
	go func() {
		defer close(taking)
		p.takeCalls(ctx)
	}()
	go p.followSMS(ctx)
	r := &registrar{p: p}
	r.register(ctx)
	for {
		select {
		case <-r.refreshed():
			r.register(ctx)
		case <-r.retried():
			r.register(ctx)
		case state := <-r.lost:
			r.unregistered()
			// SIMPLIFIED: TS 24.229 5.1.1.7. With the event rejected, the
			// UE should also release the dialogs of the identities it
			// lost; here the call goes on until one side hangs up. Only
			// removing the subscription from the HSS rejects a
			// registration, and the call of the UE then ends when the
			// controller hangs up.
			if state.Event != reginfo.Deactivated {
				p.log.Info("deregistered by the network", "event", string(state.Event))
				continue
			}
			// TS 24.229 5.1.1.7: deactivated asks for a new initial
			// registration, after which the UE subscribes again.
			p.log.Info("registering again after a network-initiated deregistration")
			r.register(ctx)
		case <-p.unanswered:
			if r.refresh == nil {
				continue
			}
			// TS 24.229 5.1.2A.1.6: the contact is bound to nothing any
			// more, and the UE registers again from the start.
			//
			// SIMPLIFIED: TS 24.229 5.1.2A.1.6 items 1 B to 1 D. The UE
			// should mark the P-CSCF as unavailable and register through
			// another one, or find the P-CSCFs again. It knows one P-CSCF
			// address, which a new discovery would give again, so it
			// registers through the same P-CSCF at once.
			p.log.Info("a request got no response; registering again")
			r.unregistered()
			p.ue.unbind()
			r.register(ctx)
		case req := <-p.requests:
			if req.register {
				req.done <- r.register(ctx)
			} else {
				req.done <- r.deregister(ctx)
			}
		case <-ctx.Done():
			p.stop(taking)
			return r.final()
		}
	}
}

// stop hangs up the call and waits for it to end.
func (p *Phone) stop(taking <-chan struct{}) {
	p.mu.Lock()
	p.stopping = true
	p.mu.Unlock()
	p.endLife()
	<-taking
	p.calls.Wait()
}

// registrar is the registration of a Phone. Only the goroutine of Serve
// uses it, so that no two REGISTERs of the UE overlap.
type registrar struct {
	p        *Phone
	refresh  *time.Timer // nil while not registered
	regEvent *RegEvent
	lost     <-chan RegState // regEvent.Lost, nil without a subscription
	// retry is the wait before the next initial registration after one
	// failed; nil when none is due.
	retry   *time.Timer
	backoff RegistrationBackoff
}

func (r *registrar) refreshed() <-chan time.Time {
	if r.refresh == nil {
		return nil
	}
	return r.refresh.C
}

func (r *registrar) retried() <-chan time.Time {
	if r.retry == nil {
		return nil
	}
	return r.retry.C
}

// retryTimer starts the wait before the next registration; a test
// replaces it to see the waits without spending them.
var retryTimer = time.NewTimer

// register registers the UE, or refreshes its registration (TS 24.229
// 5.1.1.2, 5.1.1.4.1). A refresh that fails is followed at once by an
// initial registration (5.1.1.4.1). After an initial registration it
// subscribes to the reg event (5.1.1.3). After an initial registration
// that fails, unless the phone is stopping, it starts the wait before
// the next one (5.1.1.2.1).
func (r *registrar) register(ctx context.Context) error {
	r.stopRetry()
	initial := r.refresh == nil
	reg, err := r.p.ue.Register(ctx)
	if err != nil && !initial && ctx.Err() == nil {
		r.p.log.Warn("re-registration failed; registering again from the start", "error", err.Error())
		r.unregistered()
		initial = true
		reg, err = r.p.ue.Register(ctx)
	}
	if err != nil {
		r.unregistered()
		if ctx.Err() != nil {
			return err
		}
		wait := r.backoff.Failed(err)
		r.retry = retryTimer(wait)
		r.p.log.Warn("registration failed", "error", err.Error(), "retry_after_ms", wait.Milliseconds())
		return err
	}
	r.backoff.Reset()
	if r.refresh != nil {
		r.refresh.Stop()
	}
	r.refresh = time.NewTimer(ims.RefreshAfter(reg.Expires))
	r.p.setRegistration(reg, true)
	r.p.log.Info("registered", "expires", int(reg.Expires/time.Second), "public_identities", reg.PublicIdentities)
	if initial {
		r.subscribe(reg)
	}
	return nil
}

// subscribe is TS 24.229 5.1.1.3. A UE whose SUBSCRIBE fails stays
// registered, and only logs the failure.
func (r *registrar) subscribe(reg Registration) {
	r.regEvent, r.lost = nil, nil
	ev, err := r.p.ue.SubscribeRegEvent(r.p.ua, reg)
	if err != nil {
		r.p.log.Warn("reg event SUBSCRIBE not sent", "error", err.Error())
		return
	}
	r.regEvent, r.lost = ev, ev.Lost()
}

// deregister is TS 24.229 5.1.1.6.1. A UE that is not registered has
// nothing to do. The S-CSCF ends the reg event subscription; a NOTIFY
// that ends it no longer matters.
func (r *registrar) deregister(ctx context.Context) error {
	r.stopRetry()
	r.backoff.Reset()
	if r.refresh == nil {
		return nil
	}
	if err := r.p.ue.Deregister(ctx); err != nil {
		return err
	}
	r.p.log.Info("deregistered")
	r.unregistered()
	return nil
}

// stopRetry drops the wait for the next registration, if one is due.
func (r *registrar) stopRetry() {
	if r.retry != nil {
		r.retry.Stop()
		r.retry = nil
	}
}

func (r *registrar) unregistered() {
	if r.refresh != nil {
		r.refresh.Stop()
	}
	r.refresh, r.lost = nil, nil
	r.p.setRegistration(Registration{}, false)
}

// final deregisters as the phone stops, with a deadline of its own since
// the context of Serve has ended.
func (r *registrar) final() error {
	if r.refresh == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ev := r.regEvent
	if err := r.deregister(ctx); err != nil {
		return fmt.Errorf("deregistration: %w", err)
	}
	// The UE need not wait for the NOTIFY that ends the subscription,
	// but answers it when it comes soon, rather than leave the network
	// retransmitting it to an address the UE no longer listens on.
	if ev != nil {
		select {
		case <-ev.Done():
		case <-time.After(time.Second):
		}
	}
	return nil
}

func (p *Phone) setRegistration(reg Registration, registered bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reg, p.registered = reg, registered
	p.publishLocked()
}

// changeRegistration asks Serve to register or to deregister, and waits
// for the answer. A UE with a call does not deregister: the P-CSCF
// would take none of its requests after that, the BYE among them (TS
// 24.229 5.2.5.1).
func (p *Phone) changeRegistration(ctx context.Context, register bool) error {
	if c, _ := p.line.current(); !register && c != nil {
		return ErrBusy
	}
	req := regRequest{register: register, done: make(chan error, 1)}
	select {
	case p.requests <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrStopping
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// noteUnanswered tells Serve when err is the end of a request that got
// no response at all: Timer B or Timer F fired (TS 24.229 5.1.2A.1.6).
// A P-CSCF that has lost the registration of the UE discards its
// requests in this way (5.2.6.3.2A NOTE 2).
func (p *Phone) noteUnanswered(err error) {
	if !errors.Is(err, sip.ErrTimeout) {
		return
	}
	select {
	case p.unanswered <- struct{}{}:
	default:
	}
}

// startCall counts a call that is about to run, unless the phone stops.
func (p *Phone) startCall() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping {
		return false
	}
	p.calls.Add(1)
	return true
}

// endCall takes c off the line once it has ended.
func (p *Phone) endCall(c *lineCall) {
	p.line.free(c)
	p.publish()
	p.calls.Done()
}

func (p *Phone) progress(c *lineCall) Progress {
	return func(state CallState, callID string) {
		p.line.progress(c, state, callID)
		p.publish()
	}
}

// Dial calls target, a SIP or tel URI, and returns once the INVITE is
// out. The call goes on until the controller hangs up, the callee hangs
// up or declines, or giveUpAfter, when not 0, passes without an answer.
//
// codecs, when not nil, are offered instead of those of the UE, the
// most preferred first.
func (p *Phone) Dial(target string, giveUpAfter time.Duration, codecs []sdp.Codec) error {
	p.mu.Lock()
	reg, registered := p.reg, p.registered
	p.mu.Unlock()
	if !registered {
		return ErrNotRegistered
	}
	// A second call goes out once the user has put the first on hold
	// (TS 24.610), as before a three-way session (TS 24.147 5.3.1.3.3).
	for _, other := range p.line.all() {
		if !p.line.isHeld(other) {
			return ErrNotHeld
		}
	}
	ctx, hangUp := context.WithCancel(p.life)
	c := &lineCall{
		info:   CallInfo{Direction: Outgoing, Peer: target, State: CallCalling, Started: time.Now()},
		hangUp: hangUp,
		peers:  []string{target},
	}
	if !p.line.seize(c) {
		hangUp()
		return ErrBusy
	}
	if !p.startCall() {
		hangUp()
		p.line.free(c)
		return ErrStopping
	}
	media, err := ListenMedia(p.cfg.MediaListen)
	if err != nil {
		hangUp()
		p.endCall(c)
		return err
	}
	p.publish()
	invited := make(chan error, 1)
	var once sync.Once
	report := p.progress(c)
	go func() {
		err := p.ue.Call(ctx, p.ua, reg, target, media, p.cfg.Speech, CallOptions{
			GiveUpAfter: giveUpAfter,
			StayUp:      true,
			Codecs:      codecs,
			Progress: func(state CallState, callID string) {
				report(state, callID)
				once.Do(func() { invited <- nil })
			},
			Session: func(s *Session) { p.line.setSession(c, s) },
			Changed: p.publish,
			// TIP: the callee's network asserts who answered (TS 24.608).
			Answered: func(_ *sip.OutgoingCall, ok *sip.Message) { p.line.addPeers(c, AssertedIdentities(ok)...) },
		})
		hangUp()
		media.Close()
		p.logEnd(c, err)
		p.endCall(c)
		once.Do(func() { invited <- err })
	}()
	return <-invited
}

// takeCalls answers each call that the line takes, by the answer policy
// at the time the call comes.
func (p *Phone) takeCalls(ctx context.Context) {
	for {
		select {
		case ic := <-p.line.Calls():
			if IsReplacement(ic) {
				if !p.startCall() {
					ic.Reject(480)
					continue
				}
				go p.replace(ic)
				continue
			}
			// The line holds this call until endCall frees it.
			c := p.line.of(ic)
			if !p.startCall() {
				ic.Reject(480)
				p.line.free(c)
				continue
			}
			go p.answer(c)
		case <-ctx.Done():
			return
		}
	}
}

func (p *Phone) answer(c *lineCall) {
	defer p.endCall(c)
	p.publish()
	ctx, hangUp := context.WithCancel(p.life)
	defer hangUp()
	p.line.setHangUp(c, hangUp)

	p.mu.Lock()
	policy := p.policy
	p.mu.Unlock()
	waiting := p.line.infoOf(c).Waiting
	opts := AnswerOptions{PickUp: c.pickUp, Progress: p.progress(c), Waiting: waiting,
		Session: func(s *Session) { p.line.setSession(c, s) }, Changed: p.publish}
	var media *Media
	switch {
	case policy.Mode == AnswerReject:
		opts.Reject = policy.Reject
	case policy.Mode == AnswerAuto && !waiting:
		// A waiting call rings until the user takes it (TS 24.615
		// 4.5.5.3.3).
		t := time.AfterFunc(policy.Ring, func() { pickUp(c) })
		defer t.Stop()
	}
	if opts.Reject == 0 {
		m, err := ListenMedia(p.cfg.MediaListen)
		if err != nil {
			c.ic.Reject(500)
			p.logEnd(c, err)
			return
		}
		defer m.Close()
		media = m
	}
	p.logEnd(c, p.ue.Answer(ctx, c.ic, media, p.cfg.Speech, opts))
}

func pickUp(c *lineCall) {
	select {
	case c.pickUp <- time.Now():
	default:
	}
}

// logEnd logs the end of call c, before endCall frees it. A call that
// the controller hung up ends with context.Canceled, which is no
// failure.
func (p *Phone) logEnd(c *lineCall, err error) {
	p.noteUnanswered(err)
	info := p.line.infoOf(c)
	attrs := []any{"call_id", info.CallID, "direction", string(info.Direction), "peer", info.Peer}
	if err != nil && !errors.Is(err, context.Canceled) {
		attrs = append(attrs, "error", err.Error())
	}
	p.log.Info("call ended", attrs...)
}

// PickUp answers the incoming call that rings. A call that waits is
// taken after the call in progress goes on hold (TS 24.615 4.5.5.3.3
// case A).
func (p *Phone) PickUp() error {
	c := p.line.ringing()
	if c == nil {
		return ErrNotRinging
	}
	if active := p.line.active(); active != nil {
		if err := p.line.sessionOf(active).Hold(); err != nil {
			return err
		}
		p.publish()
	}
	pickUp(c)
	return nil
}

// HangUp ends the call in the foreground (HangUpCall).
func (p *Phone) HangUp(reject int) error { return p.HangUpCall("", reject) }

// HangUpCall ends the call on the line with Call-ID callID, or the call
// in the foreground when callID is empty: BYE once it is answered,
// CANCEL while it rings at the callee, and, for an incoming call that
// rings, a final response of reject, such as 486 (Busy Here) or 603
// (Decline).
func (p *Phone) HangUpCall(callID string, reject int) error {
	c, info := p.line.current()
	if callID != "" {
		if c = p.line.byID(callID); c != nil {
			info = p.line.infoOf(c)
		}
	}
	if c == nil {
		return ErrNoCall
	}
	if info.Direction == Incoming && info.State == CallRinging {
		if err := c.ic.Reject(reject); !errors.Is(err, sip.ErrAnswered) {
			return nil
		}
		// The 200 went out first; the call is answered.
	}
	p.line.hangUp(c)
	return nil
}

// Hold puts the answered call in the foreground on hold (Session.Hold).
func (p *Phone) Hold() error {
	c := p.line.active()
	if c == nil {
		return ErrNoCall
	}
	err := p.line.sessionOf(c).Hold()
	p.publish()
	return err
}

// Resume takes the held call back (Session.Resume), once no other call
// is in the foreground.
func (p *Phone) Resume() error {
	if p.line.active() != nil {
		return ErrBusy
	}
	c := p.line.heldCall()
	if c == nil {
		return ErrNoCall
	}
	err := p.line.sessionOf(c).Resume()
	p.publish()
	return err
}

// SendDTMF sends digits in the answered call on the line.
func (p *Phone) SendDTMF(digits string) error {
	return p.withSession(func(s *Session) error { return s.SendDTMF(digits) })
}

func (p *Phone) withSession(do func(*Session) error) error {
	s := p.line.session()
	if s == nil {
		return ErrNoCall
	}
	err := do(s)
	p.publish()
	return err
}

// SendSMS submits a short message (TS 24.341 5.3.1.2) and returns once
// the MESSAGE has its 202; History follows what becomes of it.
func (p *Phone) SendSMS(ctx context.Context, out OutgoingSMS) (SentSMS, error) {
	p.mu.Lock()
	registered := p.registered
	p.mu.Unlock()
	if !registered {
		return SentSMS{}, ErrNotRegistered
	}
	sent, err := p.ue.SendSMS(ctx, out)
	p.noteUnanswered(err)
	return sent, err
}

// followSMS publishes the status each time the short messages change, so
// that a controller on /events sees a submit report, a status report or
// a new short message as it comes, as it sees a call change.
func (p *Phone) followSMS(ctx context.Context) {
	last := p.ue.SMS()
	for {
		h, err := p.ue.WaitSMS(ctx, func(h SMSHistory) bool { return !reflect.DeepEqual(h, last) })
		if err != nil {
			return
		}
		last = h
		p.publish()
	}
}

// SetAnswerPolicy applies from the next incoming call on.
func (p *Phone) SetAnswerPolicy(policy AnswerPolicy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policy = policy
	p.publishLocked()
}

// Status is the state of the phone now.
func (p *Phone) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statusLocked()
}

func (p *Phone) statusLocked() Status {
	// Once registered, the phone shows the default public identity: a
	// UE does not show the user a temporary public identity that is
	// barred (TS 24.229 5.1.1.1A).
	impu := p.ue.cfg.PublicIdentity
	if p.registered && len(p.reg.PublicIdentities) > 0 {
		impu = p.reg.DefaultIdentity()
	}
	s := Status{IMPU: impu, Registered: p.registered, AnswerPolicy: p.policy, SMS: p.ue.SMS(),
		Calls: []CallInfo{}}
	foreground, _ := p.line.current()
	for _, c := range p.line.all() {
		info := p.line.infoOf(c)
		if session := p.line.sessionOf(c); session != nil {
			st := session.Status()
			info.Session = &st
		}
		s.Calls = append(s.Calls, info)
		if c == foreground {
			s.Call = &info
		}
		if conf := p.line.conferenceOf(c); conf != nil {
			s.Conference = &ConferenceStatus{URI: conf.URI, Users: conf.Users()}
		}
	}
	return s
}

// watch delivers the status on each change until unwatch.
func (p *Phone) watch() chan Status {
	w := make(chan Status, 16)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watchers[w] = true
	return w
}

func (p *Phone) unwatch(w chan Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.watchers, w)
}

func (p *Phone) publish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishLocked()
}

// publishLocked sends the status to every watcher. The status is taken
// and sent under p.mu, so that watchers see the changes in order.
func (p *Phone) publishLocked() {
	s := p.statusLocked()
	for w := range p.watchers {
		select {
		case w <- s:
		default:
			p.log.Warn("an /events client fell behind and missed a status")
		}
	}
}

// MarshalJSON writes the policy as PUT /answer-policy takes it.
func (a AnswerPolicy) MarshalJSON() ([]byte, error) {
	j := answerPolicyJSON{Mode: a.Mode}
	switch a.Mode {
	case AnswerAuto:
		ring := a.Ring.Seconds()
		j.RingSeconds = &ring
	case AnswerReject:
		j.RejectStatus = a.Reject
	}
	return json.Marshal(j)
}

type answerPolicyJSON struct {
	Mode         AnswerMode `json:"mode"`
	RingSeconds  *float64   `json:"ring_seconds,omitempty"`
	RejectStatus int        `json:"reject_status,omitempty"`
}

// policy checks what a controller asked for and fills in the defaults.
func (j answerPolicyJSON) policy() (AnswerPolicy, error) {
	a := AnswerPolicy{Mode: j.Mode}
	switch j.Mode {
	case AnswerAuto:
		a.Ring = DefaultRing
		if j.RingSeconds != nil {
			if *j.RingSeconds < 0 {
				return AnswerPolicy{}, errors.New("ring_seconds must not be negative")
			}
			a.Ring = time.Duration(*j.RingSeconds * float64(time.Second))
		}
	case AnswerReject:
		a.Reject = 486
		if j.RejectStatus != 0 {
			if !isRejectStatus(j.RejectStatus) {
				return AnswerPolicy{}, fmt.Errorf("reject_status %d is not a status from 400 to 699", j.RejectStatus)
			}
			a.Reject = j.RejectStatus
		}
	case AnswerManual:
	default:
		return AnswerPolicy{}, fmt.Errorf("mode %q is none of auto, reject and manual", j.Mode)
	}
	return a, nil
}

func isRejectStatus(code int) bool { return code >= 400 && code <= 699 }
