package ue

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// RegEventExpires is the duration the UE asks for when it subscribes to
// its registration state: 600 000 s in TS 24.229 5.1.1.3 item e.
const RegEventExpires = 600000 * time.Second

// RegState is what one NOTIFY of the reg event package says about this
// UE (TS 24.229 5.1.2.1).
type RegState struct {
	// Registration is the state of the default public identity.
	Registration reginfo.State
	// Contact is the state of this UE's contact under that identity, and
	// Event what put it there, such as reginfo.Expired.
	Contact reginfo.State
	Event   reginfo.Event
	// Subscription is the Subscription-State of the NOTIFY.
	Subscription sip.SubState
}

// Deregistered reports that the NOTIFY took the UE's registration away:
// the identity is no longer registered, or this UE's contact is not
// (TS 24.229 5.1.1.7, 5.1.2.1). With reginfo.Deactivated the UE is to
// register again; with reginfo.Rejected that would not help.
func (s RegState) Deregistered() bool {
	return s.Registration == reginfo.Terminated || s.Contact == reginfo.Terminated
}

// RegEvent is the UE's subscription to its own registration state.
type RegEvent struct {
	sub *sip.Subscription
	log *slog.Logger

	mu     sync.Mutex
	ended  bool
	states chan RegState
}

// States delivers what each NOTIFY says, the last one included, and is
// closed when the subscription ends. A state that the user does not read
// in time is dropped with a warning.
func (r *RegEvent) States() <-chan RegState { return r.states }

// Lost delivers the first state that takes the registration away
// (RegState.Deregistered), and nothing when the subscription ends
// without one. It reads States, so a user takes one or the other. Each
// subscription has its own, so a late NOTIFY of an old subscription
// cannot end a registration that came after it.
func (r *RegEvent) Lost() <-chan RegState {
	lost := make(chan RegState, 1)
	go func() {
		for state := range r.states {
			if state.Deregistered() {
				lost <- state
				return
			}
		}
		if err := r.Err(); err != nil {
			r.log.Warn("reg event subscription failed", "error", err.Error())
		}
	}()
	return lost
}

// Done is closed when the subscription ends; Err then tells why, as
// sip.Subscription.Err does.
func (r *RegEvent) Done() <-chan struct{} { return r.sub.Done() }
func (r *RegEvent) Err() error            { return r.sub.Err() }

// SubscribeRegEvent subscribes to the reg event package of the default
// public identity of reg, as TS 24.229 5.1.1.3 has the UE do after the
// 2xx to its initial registration. The SUBSCRIBE goes, as every request
// of the UE, to the P-CSCF and along the Service-Route (5.1.2A.1.1), so
// it reaches the S-CSCF that holds the registration. The UE refreshes
// the subscription by the rule of 5.1.1.3.
//
// SIMPLIFIED: TS 24.229 5.1.1.3, last paragraph. When a refresh fails
// with 481, the UE should start a new subscription. Here the
// subscription ends; the caller may subscribe again. The UE simulator
// does so only after its next initial registration.
func (u *UE) SubscribeRegEvent(ua *sip.UA, reg Registration) (*RegEvent, error) {
	defaultID := reg.DefaultIdentity()
	r := &RegEvent{states: make(chan RegState, 8), log: u.log}
	sub, err := ua.Subscribe(sip.SubscribeRequest{
		// Items a to e.
		RequestURI:   defaultID,
		From:         defaultID,
		To:           defaultID,
		Route:        u.routeSet(reg),
		Event:        "reg",
		Expires:      RegEventExpires,
		RefreshAfter: ims.RefreshAfter,
		OnNotify:     func(n sip.Notification, _ *sip.Message) { u.receiveRegEvent(r, defaultID, n) },
	})
	if err != nil {
		return nil, err
	}
	r.sub = sub
	u.log.Info("reg event subscribing", "public_identity", defaultID)
	go func() {
		// The subscription ends after the OnNotify of its last NOTIFY,
		// so that state is in the channel before it closes.
		<-sub.Done()
		r.mu.Lock()
		defer r.mu.Unlock()
		r.ended = true
		close(r.states)
	}()
	return r, nil
}

// receiveRegEvent reads, from a NOTIFY, the registration of the default
// identity and this UE's contact in it.
//
// SIMPLIFIED: RFC 3680 5.2. The version of the document is not checked.
// The S-CSCF sends the full state every time, and a NOTIFY that arrives
// after a later one is already refused for its CSeq (RFC 3261 12.2.2).
func (u *UE) receiveRegEvent(r *RegEvent, defaultID string, n sip.Notification) {
	state := RegState{Subscription: n.State.State}
	if len(n.Body.Content) > 0 {
		doc, err := reginfo.Parse(n.Body.Content)
		if err != nil {
			u.log.Warn("reg event NOTIFY with a body that does not parse", "error", err.Error())
			return
		}
		for _, reg := range doc.Registrations {
			if !ims.SamePublicIdentity(reg.AOR, defaultID) {
				continue
			}
			state.Registration = reg.State
			for _, c := range reg.Contacts {
				if u.isOwnContact(c.URI) {
					state.Contact, state.Event = c.State, c.Event
				}
			}
		}
	}
	u.log.Info("reg event", "public_identity", defaultID, "state", string(state.Registration),
		"contact_state", string(state.Contact), "contact_event", string(state.Event),
		"subscription_state", string(state.Subscription))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	select {
	case r.states <- state:
	default:
		u.log.Warn("dropped a reg event state that was not read")
	}
}

// isOwnContact reports whether uri is the contact this UE registered.
func (u *UE) isOwnContact(uri string) bool {
	got, err := sip.ParseURI(uri)
	if err != nil {
		return false
	}
	own, _ := sip.ParseURI(ContactURI(u.cfg.PublicIdentity, u.contactAddr))
	return got.User == own.User && strings.EqualFold(got.Host, own.Host) && got.Port == own.Port
}
