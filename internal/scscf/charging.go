package scscf

import "github.com/yuu-biz/ims-practice/internal/sip"

// This file holds what the S-CSCF writes in the P-Charging-Vector of an
// initial request and of its responses (TS 24.229 5.4.3.2, 5.4.3.3).
// The icid-value goes on as it came (5.4.3.2 step 6, 5.4.3.3 step 6).
// The IOIs name the network on each side of the hop: the S-CSCF takes
// out the ones that came, and puts in its own home domain. The type of
// the IOI depends on the hop: type 1 between the S-CSCF and the
// P-CSCF, type 2 between the networks of the caller and of the callee,
// and type 3 between the S-CSCF and an application server. The value
// is the same for all three, and the type is not written in the
// message.

// chargeRequest gives out, an initial request on its way to an
// application server or to the callee, this network as its orig-ioi in
// place of the IOIs that it came with: steps 2, 5 d, 5 e and 7 of
// 5.4.3.2, and steps 4 d, 4 f, 7 and 7A of 5.4.3.3. A request without a
// P-Charging-Vector stays without one.
func (s *SCSCF) chargeRequest(out *sip.Message) {
	c, ok := sip.ChargingVectorOf(out)
	if !ok {
		return
	}
	c.OrigIOI, c.TermIOI = s.cfg.HomeDomain, ""
	c.Params.Remove("transit-ioi")
	out.Set("P-Charging-Vector", c.String())
}

// chargeResponses returns what the S-CSCF does with each response that
// it passes back for req, an initial request of the originating case
// or of the terminating case. The response gets the icid-value and the
// orig-ioi that req came with, and this network's term-ioi, in place
// of the IOIs that it brought. 5.4.3.2 asks it for a response that
// brings a term-ioi. 5.4.3.3 asks it for every response that goes back
// to an application server, and for a 1xx or 2xx that goes back
// towards the caller; any other response goes back as it came.
//
// SIMPLIFIED: TS 24.229 5.4.3.2. The term-ioi that a response brings is
// not stored: it names the network that answered in the charging
// records of the S-CSCF, which no node of this project writes, and no
// later message carries it.
func (s *SCSCF) chargeResponses(req *sip.Message, originating bool) func(*sip.Message) {
	request, ok := sip.ChargingVectorOf(req)
	toAS := s.sentBack(req)
	return func(resp *sip.Message) {
		received, _ := sip.ChargingVectorOf(resp)
		switch {
		case !ok:
			return
		case originating && received.TermIOI == "":
			return
		case !originating && !toAS && resp.StatusCode >= 300:
			return
		}
		resp.Set("P-Charging-Vector", request.Response(s.cfg.HomeDomain).String())
	}
}

// sentBack reports whether an application server sent req back to the
// S-CSCF: its top Route is the S-CSCF's own URI with an original dialog
// identifier (5.4.3.4). The responses to req go back to that server.
func (s *SCSCF) sentBack(req *sip.Message) bool {
	top, popped := sip.PopOwnRoute(req.Clone(), s.ownURI())
	_, odi := top.Params.Get(odiParam)
	return popped && odi
}
