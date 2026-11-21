package ibcf

import (
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// onMessage is the UAConfig.OnMessage of side s: a MESSAGE outside a
// dialog (RFC 3428), such as a short message from one network to the
// other (TS 23.204 6.3a). It is a standalone transaction: the IBCF
// answers it with the final response of a new MESSAGE that it sends on
// the other side, with a Call-ID, From tag and CSeq of its own (TS
// 24.229 5.10.2.2, 5.10.3.2).
//
// SIMPLIFIED: TS 24.229 5.10.2.2 and 5.10.3.2. Of the standalone
// requests, only MESSAGE crosses. The IBCF's UA answers an OPTIONS
// outside a dialog itself, with what the IBCF supports (RFC 3261 11.2),
// refuses a SUBSCRIBE outside a dialog (onSubscribe), and answers
// PUBLISH and any other method with 405. A SUBSCRIBE outside a dialog
// makes a dialog of its own, which the IBCF would hold on each side as
// it holds those of a call; that part of the B2BUA is not built. A
// provisional response to the MESSAGE stays on its side; only the final
// response crosses.
func (b *IBCF) onMessage(s side, st *sip.ServerTransaction) {
	c, r := b.prepare(s, st.Request, st.Source.Addr.Addr())
	if r != nil {
		respond(st, r.status, r.headers, sip.Body{})
		return
	}
	ua, err := b.other(s).agent()
	if err != nil {
		respond(st, b.refuse(st.Request, c.peer, c.direction, 500).status, nil, sip.Body{})
		return
	}
	req := c.req
	from, _ := sip.ParseNameAddr(req.Get("From"))
	from.Params = nil
	from.Params.Set("tag", sip.NewTag())
	to, _ := sip.ParseNameAddr(req.Get("To"))
	to.Params = nil
	out := &sip.Message{Method: sip.MESSAGE, RequestURI: req.RequestURI}
	for _, v := range c.route {
		out.Add("Route", v)
	}
	out.Add("Max-Forwards", req.Get("Max-Forwards"))
	out.Add("From", from.String())
	out.Add("To", to.String())
	out.Add("Call-ID", sip.NewCallID())
	out.Add("CSeq", sip.CSeq{Number: 1, Method: sip.MESSAGE}.String())
	for _, h := range interconnect.CrossingFields(req) {
		out.Add(h.Name, h.Value)
	}
	if body := sip.BodyOf(req); len(body.Content) > 0 {
		out.Set("Content-Type", body.Type)
		out.Body = body.Content
	}
	ct, err := ua.SendRequest(out)
	if err != nil {
		c.log.Warn("cannot send the MESSAGE on", "error", err.Error())
		respond(st, b.refuse(st.Request, c.peer, c.direction, 504).status, nil, sip.Body{})
		return
	}
	c.log.Info("MESSAGE across the border", "out_call_id", out.Get("Call-ID"))
	var final *sip.Message
	for resp := range ct.Responses() {
		if resp.StatusCode >= 200 {
			final = resp
		}
	}
	if final == nil {
		c.log.Warn("the other side did not answer", "error", errText(ct.Err()))
		respond(st, b.refuse(st.Request, c.peer, c.direction, 504).status, nil, sip.Body{})
		return
	}
	m := final.Clone()
	c.border.Response(m, reverse(c.direction))
	respond(st, final.StatusCode, interconnect.CrossingFields(m), sip.BodyOf(m))
}

// onSubscribe is the UAConfig.OnSubscribe of side s: a SUBSCRIBE that
// belongs to no dialog of a call. One outside any dialog, such as a
// participant's subscription to the conference URI of a focus in the
// other network, gets 403 (Forbidden) once the border has let it
// through, as TS 24.229 5.10.1 lets the IBCF reject a request by local
// policy: TS 24.147 5.3.1.2 has a participant whose SUBSCRIBE outside
// the dialog gets 403 subscribe within the dialog of its conference
// session instead, which crosses like any request of a call. A
// SUBSCRIBE that the agreement does not allow gets the 405 of the
// border instead (TS 29.165 Table 6.1). One within a dialog that the
// IBCF does not know gets 481 (RFC 3261 12.2.2).
func (b *IBCF) onSubscribe(s side, st *sip.ServerTransaction) {
	if to, err := sip.ParseNameAddr(st.Request.Get("To")); err == nil {
		if _, inDialog := to.Params.Get("tag"); inDialog {
			respond(st, 481, nil, sip.Body{})
			return
		}
	}
	c, r := b.prepare(s, st.Request, st.Source.Addr.Addr())
	if r != nil {
		respond(st, r.status, r.headers, sip.Body{})
		return
	}
	c.log.Info("SUBSCRIBE outside a dialog refused: a subscription crosses only within the dialog of a call", "request_uri", c.req.RequestURI)
	respond(st, b.refuse(st.Request, c.peer, c.direction, 403).status, nil, sip.Body{})
}

// respond answers the request of st with a response of the IBCF's own
// side.
func respond(st *sip.ServerTransaction, code int, headers []sip.HeaderField, body sip.Body) {
	resp := sip.NewResponse(st.Request, code, sip.NewTag())
	for _, h := range headers {
		resp.Add(h.Name, h.Value)
	}
	if len(body.Content) > 0 {
		resp.Set("Content-Type", body.Type)
		resp.Body = body.Content
	}
	st.Respond(resp)
}
