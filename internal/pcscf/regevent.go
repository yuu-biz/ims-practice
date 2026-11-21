package pcscf

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the P-CSCF as a subscriber of the reg event package
// (TS 24.229 5.2.3): it learns from the S-CSCF when a registration ends,
// even one that the UE did not end itself, and then forgets the UE
// (5.2.5.2).

// contactURI is the P-CSCF's URI in the From and Contact of its own
// SUBSCRIBEs, where the S-CSCF sends the NOTIFYs.
func (p *PCSCF) contactURI() string { return fmt.Sprintf("sip:%s:%d", p.cfg.Host, p.cfg.Port) }

func (p *PCSCF) locate(u sip.URI) (sip.Destination, error) {
	return ims.Destination(context.Background(), p.locator, u)
}

// subscribeRegEvent is TS 24.229 5.2.3 after the 200 to the first
// initial REGISTER of a private identity: a SUBSCRIBE to the user's
// default public identity, which goes to the I-CSCF of the home network
// (item 4). The subscription is made once per private identity (NOTE);
// a new one follows only once the old one has ended.
//
// SIMPLIFIED: TS 24.229 5.2.3, last paragraph. When a refresh fails with
// 481, the P-CSCF should start a new subscription at once. Here the
// subscription ends, and the next REGISTER of the user starts the new
// one.
func (p *PCSCF) subscribeRegEvent(a Association, registered time.Duration, log *slog.Logger) {
	if len(a.PublicIdentities) == 0 {
		log.Warn("no public identity to subscribe to the reg event package of")
		return
	}
	stack, err := p.stack.Get()
	if err != nil {
		return
	}
	privateID := strings.ToLower(a.PrivateIdentity)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.regEvents[privateID]; ok {
		return
	}
	icid := sip.NewCallID()
	defaultID := a.PublicIdentities[0]
	sub, err := p.subscriber.Subscribe(stack, sip.SubscribeRequest{
		// Items a to e.
		RequestURI: defaultID,
		From:       p.contactURI(),
		Event:      "reg",
		Expires:    registered + ims.RegEventMargin,
		Headers: []sip.HeaderField{
			// Item f: the URI that the P-CSCF put in Path.
			{Name: "P-Asserted-Identity", Value: p.pathURIOf(a)},
			// Item g.
			{Name: "P-Charging-Vector", Value: "icid-value=" + icid + ";orig-ioi=" + p.cfg.VisitedNetworkID},
		},
		RefreshAfter: ims.RefreshAfter,
		OnNotify: func(n sip.Notification, resp *sip.Message) {
			p.receiveRegEvent(a.PrivateIdentity, icid, n, resp, log)
		},
	})
	if err != nil {
		log.Warn("reg event SUBSCRIBE not sent", "error", err.Error())
		return
	}
	log.Info("reg event subscribing", "public_identity", defaultID)
	p.regEvents[privateID] = sub
	go func() {
		<-sub.Done()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.regEvents[privateID] == sub {
			delete(p.regEvents, privateID)
		}
	}()
}

// receiveRegEvent is a NOTIFY of the P-CSCF's subscription (TS 24.229
// 5.2.3 and 5.2.5.2). A registration that has ended, or a contact of the
// UE that the network removed, ends the IP association of the UE: the
// P-CSCF forgets what it stored for the user.
//
// SIMPLIFIED: RFC 3680 5.2. The version of the document is not checked.
// The S-CSCF sends the full state every time, and a NOTIFY that arrives
// after a later one is already refused for its CSeq (RFC 3261 12.2.2).
func (p *PCSCF) receiveRegEvent(privateID, icid string, n sip.Notification, resp *sip.Message, log *slog.Logger) {
	// 5.2.3: the response carries the orig-ioi of the NOTIFY, this
	// network's term-ioi and the icid-value of the SUBSCRIBE.
	pcv := "icid-value=" + icid
	if ioi := chargingParam(n.Request.Get("P-Charging-Vector"), "orig-ioi"); ioi != "" {
		pcv += ";orig-ioi=" + ioi
	}
	resp.Add("P-Charging-Vector", pcv+";term-ioi="+p.cfg.VisitedNetworkID)

	if len(n.Body.Content) == 0 {
		log.Info("reg event", "subscription_state", string(n.State.State))
		return
	}
	doc, err := reginfo.Parse(n.Body.Content)
	if err != nil {
		log.Warn("reg event NOTIFY with a body that does not parse", "error", err.Error())
		return
	}
	for _, r := range doc.Registrations {
		for _, c := range r.Contacts {
			log.Info("reg event", "public_identity", r.AOR, "state", string(r.State), "contact", c.URI,
				"contact_state", string(c.State), "contact_event", string(c.Event), "subscription_state", string(n.State.State))
			if !removedByTheNetwork(r, c) {
				continue
			}
			a, ok := p.store.AssociationOfContact(c.URI)
			if ok && strings.EqualFold(a.PrivateIdentity, privateID) {
				p.endAssociation(a, log)
				log.Info("reg event ended the IP association", "public_identity", r.AOR, "source", a.Source.String())
			}
		}
	}
}

// removedByTheNetwork is the test of TS 24.229 5.2.5.2: the registration
// has ended, or the contact has, by an event that removed it.
func removedByTheNetwork(r reginfo.Registration, c reginfo.Contact) bool {
	if r.State == reginfo.Terminated {
		return true
	}
	if c.State != reginfo.Terminated {
		return false
	}
	switch c.Event {
	case reginfo.Unregistered, reginfo.Rejected, reginfo.Deactivated:
		return true
	}
	return false
}

// chargingParam is a parameter of a P-Charging-Vector value.
func chargingParam(pcv, name string) string {
	for _, p := range strings.Split(pcv, ";") {
		if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
