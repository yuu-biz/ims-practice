package pcscf

import "github.com/yuu-biz/ims-practice/internal/sip"

// This file holds what the P-CSCF writes in P-Charging-Vector. The
// header field does not cross the edge to the UE either way
// (crossBoundary), so the P-CSCF writes it into what it passes on from
// the UE: its own icid-value in a request that starts a dialog or a
// transaction, the icid-value of the INVITE in a later request of that
// dialog, and in a response from the UE the icid-value and the orig-ioi
// of the request that the UE answers. The IOI that it writes names the
// network of the P-CSCF.

// chargeRequest gives out, a request from the UE, the P-Charging-Vector
// with icid and a type 1 orig-ioi, and no term-ioi (TS 24.229 5.2.2.1
// item 3, 5.2.6.3.3 step 7, 5.2.6.3.5 step 7, 5.2.6.3.7 step 5,
// 5.2.6.3.9 step 3).
func (p *PCSCF) chargeRequest(out *sip.Message, icid string) {
	out.Set("P-Charging-Vector", sip.ChargingVector{ICID: icid, OrigIOI: p.cfg.VisitedNetworkID}.String())
}

// chargeResponse gives resp, a response from the UE, the
// P-Charging-Vector of the request that it answers, as the P-CSCF
// stored it before it removed the header field from the request
// (5.2.6.4.3 item 9, 5.2.6.4.5 item 7, 5.2.6.4.7 item 5, 5.2.6.4.9 item
// 6), with a type 1 term-ioi: 5.2.6.4.4 and 5.2.6.4.6 for each kind of
// response, 5.2.6.4.8 step 3 and 5.2.6.4.10 item 2. A request without
// an icid-value gives the response none.
func (p *PCSCF) chargeResponse(resp *sip.Message, request sip.ChargingVector) {
	if request.ICID == "" {
		return
	}
	resp.Set("P-Charging-Vector", request.Response(p.cfg.VisitedNetworkID).String())
}
