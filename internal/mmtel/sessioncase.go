package mmtel

import "github.com/yuu-biz/ims-practice/internal/sip"

// This file holds the order in which the AS runs its services, one
// function for each session case of P-Served-User (TS 24.229
// 5.7.1.3A). The order follows the interactions that the service
// specifications give.

// run serves the call in its session case.
func (c *call) run() {
	switch c.served.SessionCase {
	case sip.SessionCaseOrig:
		c.originating()
	case sip.SessionCaseOrigCDIV:
		c.originatingAfterDiversion()
	default:
		c.terminating()
	}
}

// originating serves a call that the served user makes. A call to the
// conference factory URI creates a conference, whose focus the AS is
// (conference.go).
func (c *call) originating() {
	if c.as.isFactoryURI(c.in.Request.RequestURI) {
		c.createConference()
		return
	}
	inv, fields, err := c.newInvitation(c.in.Request.RequestURI)
	if err != nil {
		c.storeFailed(err)
		return
	}
	// OCB first: it decides on the called identity, before any service
	// changes the request (TS 24.611 4.5.2.4.1).
	if c.barOutgoing(c.callInfo(fields, inv.RequestURI)) {
		return
	}
	// OIR (TS 24.607 4.5.2.4), then TIP (TS 24.608 4.5.2.4) for the
	// responses.
	c.restrictOriginatingIdentity(&inv, fields)
	c.offerTerminatingIdentity(fields)
	inv.Headers = fields.Headers
	c.relay(inv)
}

// originatingAfterDiversion serves a call that the AS diverted away
// from the served user, which the S-CSCF sends back with the
// originating services of the diverting user (TS 24.229 5.4.3.3 step 3,
// RFC 8498).
func (c *call) originatingAfterDiversion() {
	inv, fields, err := c.newInvitation(c.in.Request.RequestURI)
	if err != nil {
		c.storeFailed(err)
		return
	}
	// The OCB of the diverting user applies to the call to the
	// diverted-to user (TS 24.611 4.6.7).
	if c.barOutgoing(c.callInfo(fields, inv.RequestURI)) {
		return
	}
	inv.Headers = fields.Headers
	c.relay(inv)
}

// terminating serves a call to the served user, registered or not.
func (c *call) terminating() {
	inv, fields, err := c.newInvitation(c.in.Request.RequestURI)
	if err != nil {
		c.storeFailed(err)
		return
	}
	// ICB and ACR first: they decide on the caller's identity, which OIP
	// may take out of the request (TS 24.607 4.3.3 NOTE 2), and they come
	// before diversion (TS 24.611 4.6.7).
	info := c.callInfo(fields, inv.RequestURI)
	if c.barIncoming(info) {
		return
	}
	// CFU, and CFNL for a user who is not registered (TS 24.604 4.6.7).
	if d, ok := c.divertAtSetup(info); ok {
		c.divert(d)
		return
	}
	// OIP (TS 24.607 4.5.2.9), then TIR (TS 24.608 4.5.2.9) for the
	// responses.
	c.presentOriginatingIdentity(&inv, fields)
	c.withholdTerminatingIdentity(fields)
	c.withhold = c.withholdSession(info)
	// CAT (TS 24.182 4.5.5.3.7), from the MRF before the INVITE goes on.
	c.startAlertingTone(fields)
	inv.Headers = fields.Headers
	r := c.attempt(inv, c.noReplyTimer(info))
	// The callee did not answer: the tone stops before the caller hears
	// how the call goes on.
	c.stopAlertingTone("the leg to the served user ended")
	// CFB, CFNRc and CFNR, on how the leg to the served user ended (TS
	// 24.604 4.5.2.6.3).
	if d, ok := c.divertOn(r, info); ok {
		c.divert(d)
		return
	}
	c.finish(r)
}

// servicesOnResponse runs the services that act on a response to req
// that goes back to the caller's side: TIP at the AS of the caller;
// TIR, the History-Info of a diverted call and CW at the AS of the
// callee.
func (c *call) servicesOnResponse(resp, req, fields *sip.Message) {
	switch c.served.SessionCase {
	case sip.SessionCaseOrig:
		c.presentTerminatingIdentity(fields)
	case sip.SessionCaseTerm:
		c.restrictTerminatingIdentity(fields)
		if req == c.in.Request {
			c.keepHistory(resp, fields)
			c.notifyWaiting(resp, fields)
		}
	}
}
