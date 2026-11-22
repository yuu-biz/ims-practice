package mmtel

import (
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds communication diversion (TS 24.604): CFU and CFNL,
// which divert a call when it arrives, and CFB, CFNRc and CFNR, which
// divert it on what happens to the leg to the served user. The AS
// sends the diverted INVITE to the S-CSCF as it sends any outgoing leg,
// with a new Request-URI; the S-CSCF then runs the originating services
// of the diverting user (orig-cdiv) and routes it on (TS 24.229 5.4.3.3
// step 3). By default a user has no diversion.

// The causes of RFC 4458 that tell why a call was diverted, in the
// Request-URI and in History-Info (TS 24.604 4.5.2.6.2.2 a).
const (
	causeCFU   = 302
	causeCFB   = 486
	causeCFNR  = 408
	causeCFNL  = 404
	causeCFNRc = 503
)

// defaultDiversionLimit is how many diversions a call may have when the
// operator part of the user's service gives no limit of its own (TS
// 24.604 4.5.2.6.1; the operator sets it in TS 29.364 7.2.8.2.2).
const defaultDiversionLimit = 5

// diversion is a diversion that a rule of the served user asks for.
type diversion struct {
	// service is CFU, CFNL, CFB, CFNRc or CFNR.
	service string
	cause   int
	to      simservs.ForwardTo
	// response is the response of the served user that the diversion is
	// based on, nil when there is none (TS 24.604 4.5.2.6.2.2 b 1).
	response *sip.Message
}

// communicationDiversion is the CDIV service of the served user when
// the operator has authorized it, nil otherwise.
func (c *call) communicationDiversion() *simservs.CompleteCommunicationDiversion {
	cdiv := c.services.CompleteCommunicationDiversion
	if cdiv == nil || !cdiv.OperatorCommunicationDiversion.Authorized {
		return nil
	}
	return cdiv
}

// divertAtSetup is the evaluation of the rules when the INVITE arrives
// (TS 24.604 4.6.7): CFU, and CFNL when the served user is not
// registered. The first rule that matches is the one (4.9.1.2), so the
// order of the rules gives CFU its precedence.
func (c *call) divertAtSetup(info simservs.CallInfo) (diversion, bool) {
	cdiv := c.communicationDiversion()
	if cdiv == nil {
		return diversion{}, false
	}
	r := simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, simservs.AtCommunicationSetup, info)
	if !r.Diverts() {
		return diversion{}, false
	}
	if r.Rule.Conditions != nil && r.Rule.Conditions.NotRegistered != nil {
		return diversion{service: "CFNL", cause: causeCFNL, to: *r.ForwardTo}, true
	}
	return diversion{service: "CFU", cause: causeCFU, to: *r.ForwardTo}, true
}

// divert sends the call on to the target of d, or rejects it when it
// has had as many diversions as the operator allows (TS 24.604
// 4.5.2.6.1). The caller first learns of the diversion when the rule
// says so (4.5.2.6.4).
//
// The operator option of 4.5.2.6.1 is "reject the communication"; the
// AS does not deliver the call to the latest diverting user instead.
func (c *call) divert(d diversion) {
	limit := c.communicationDiversion().OperatorCommunicationDiversion.TotalNumberOfDiversionsForEachCommunication
	if limit <= 0 {
		limit = defaultDiversionLimit
	}
	if n := diversionsIn(c.in.Request); n >= limit {
		code := 480
		if d.service == "CFB" {
			code = 486
		}
		c.log.Info("CDIV: too many diversions", "service", d.service, "diversions", n, "limit", limit)
		c.in.Reject(code, c.as.warning("Too many diversions appeared"))
		return
	}
	target := c.diversionTarget(d)
	c.actedOn(d.service)
	c.log.Info("CDIV: diverting the call", "service", d.service, "target", target)
	c.leaveServedUser(d)
	c.mu.Lock()
	c.heldAnswer = sip.Body{}
	c.mu.Unlock()
	if d.to.NotifyCallerOrDefault() {
		c.notifyCaller(d, target)
	}
	inv, fields, err := c.newInvitation(target)
	if err != nil {
		c.storeFailed(err)
		return
	}
	fields.Remove("History-Info")
	for _, h := range sip.HistoryInfoHeaders(c.diversionHistory(target, d, c.hiddenFromTarget(d), false)) {
		fields.Add(h.Name, h.Value)
	}
	if c.hiddenFromTarget(d) {
		// 4.5.2.6.2.2 c: the diverted-to user sees no To of the served
		// user.
		inv.To = target
	}
	inv.Headers = fields.Headers
	c.finish(c.attempt(inv, 0))
}

// diversionTarget is the Request-URI of the diverted INVITE (TS 24.604
// 4.5.2.6.2.2 a): the target as a SIP URI, a tel URI turned into one
// with user=phone (RFC 3261 19.1.6), with the cause-param of the
// diversion (RFC 4458).
func (c *call) diversionTarget(d diversion) string {
	target := d.to.Target
	if number, ok := ims.TelNumber(target); ok {
		target = "sip:" + number + "@" + c.as.cfg.HomeDomain + ";user=phone"
	}
	return target + ";cause=" + strconv.Itoa(d.cause)
}

// diversionHistory is the History-Info of the diverted INVITE, or of
// the 181 to the caller: the entries the INVITE came with, and those of
// this diversion (TS 24.604 4.5.2.6.2.2 b, 4.5.2.6.2.3 b). The entry of
// the served user carries a Reason when a response of the served user
// caused the diversion, and "history" privacy when hideServed. The new
// entry of the target has "history" privacy when hideTarget. Indexes
// follow RFC 7044 10.3: each new entry is one level below the entry it
// was retargeted from, which its mp names.
//
// SIMPLIFIED: TS 24.604 4.5.2.6.2.3. That clause applies when the last
// entry received is the served user's. The AS takes the last entry to
// be the served user's when it records a diversion, with a cause-param:
// in this network a diverted request reaches the diverted-to user's AS
// only that way. Comparing the identities would need the user's alias
// set, since the entry has the number in a SIP URI and P-Served-User
// the canonical form that the S-CSCF found with ENUM.
func (c *call) diversionHistory(target string, d diversion, hideServed, hideTarget bool) []sip.HistoryInfoEntry {
	entries, _ := sip.ParseHistoryInfo(c.in.Request.GetAll("History-Info"))
	var reasons []sip.Reason
	if d.response != nil {
		reasons = []sip.Reason{sip.NewReason(sip.ReasonSIP, d.response.StatusCode, sip.ReasonPhrase(d.response.StatusCode))}
	}
	var servedPrivacy, targetPrivacy sip.Privacy
	if hideServed {
		servedPrivacy = sip.Privacy{sip.PrivacyHistory}
	}
	if hideTarget {
		targetPrivacy = sip.Privacy{sip.PrivacyHistory}
	}
	var served sip.HistoryInfoEntry
	if n := len(entries); n > 0 && recordsDiversion(entries[n-1]) {
		// 4.5.2.6.2.3: the entry of the served user is there, and gets
		// the Reason and the privacy of this diversion.
		last := entries[n-1]
		served = sip.NewHistoryInfoEntry(last.Target(), last.Index, append(last.Reasons(), reasons...), append(last.Privacy(), servedPrivacy...))
		served.DisplayName, served.Params = last.DisplayName, last.Params
		entries = entries[:n-1]
	} else {
		// 4.5.2.6.2.2: an entry for the served user, at a new level.
		index := "1"
		if n > 0 {
			index = entries[n-1].Index + ".1"
		}
		served = sip.NewHistoryInfoEntry(c.in.Request.RequestURI, index, reasons, servedPrivacy)
	}
	next := sip.NewHistoryInfoEntry(target, served.Index+".1", nil, targetPrivacy)
	next.Params.Set("mp", served.Index)
	return append(entries, served, next)
}

// recordsDiversion tells whether a History-Info entry is the target of
// a diversion: its URI has a cause-param (RFC 4458).
func recordsDiversion(e sip.HistoryInfoEntry) bool {
	u, err := sip.ParseURI(e.Target())
	if err != nil {
		return false
	}
	_, ok := u.Params.Get("cause")
	return ok
}

// diversionsIn counts the diversions that the History-Info of a request
// records, as TS 24.604 4.5.2.6.1 counts them: its entries with a
// cause-param.
func diversionsIn(m *sip.Message) int {
	entries, err := sip.ParseHistoryInfo(m.GetAll("History-Info"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if recordsDiversion(e) {
			n++
		}
	}
	return n
}

// restrictsOriginatingIdentity tells whether the served user withholds
// its identity by default, with OIR in the permanent mode or in the
// temporary mode with restriction by default: the served user "wishes
// privacy" of TS 24.604 4.5.2.6.2.2.
func (c *call) restrictsOriginatingIdentity() bool {
	oir := c.services.CompleteOriginatingIdentityRestriction
	if oir == nil || !oir.OperatorOriginatingIdentityPresentationRestriction.Authorized {
		return false
	}
	user := oir.OriginatingIdentityPresentationRestriction
	return oir.OperatorOriginatingIdentityPresentationRestriction.Mode == simservs.ModePermanent ||
		temporaryDefault(user.SimservType, user.DefaultBehaviourOrDefault()) == simservs.PresentationRestricted
}

// hiddenFromTarget tells whether the diverted-to user is not to see the
// served user (TS 24.604 4.5.2.6.2.2 b 1 and c).
func (c *call) hiddenFromTarget(d diversion) bool {
	return c.restrictsOriginatingIdentity() || d.to.RevealIdentityToTargetOrDefault() == simservs.RevealFalse
}

// hiddenFromCaller tells whether the caller is not to see the served
// user in the notification of the diversion (TS 24.604 4.5.2.6.4 b).
func (c *call) hiddenFromCaller(d diversion) bool {
	restricted, _ := c.terminatingIdentityRestricted()
	return restricted || d.to.RevealServedUserIdentityToCallerOrDefault() == simservs.RevealFalse
}

// notifyCaller sends the caller the 181 (Call Is Being Forwarded) of TS
// 24.604 4.5.2.6.4: the served user in P-Asserted-Identity, and the
// History-Info of the diversion. The entry of the diverted-to user has
// "history" privacy, since the AS does not know whether that user
// allows its identity to be shown (4.6.2).
//
// SIMPLIFIED: TS 24.604 4.5.2.6.4. The notification has no
// announcement (TS 24.628): the call goes on to the target right after
// the 181. An announcement first would need the caller's media moved to
// the MRFP and then to the target, in an early dialog that the
// diversion may end with a 199.
func (c *call) notifyCaller(d diversion, target string) {
	fields := &sip.Message{}
	fields.Add("P-Asserted-Identity", "<"+c.served.URI+">")
	hidden := c.hiddenFromCaller(d)
	if hidden {
		fields.Add("Privacy", sip.PrivacyID)
	}
	for _, h := range sip.HistoryInfoHeaders(c.diversionHistory(target, d, hidden, true)) {
		fields.Add(h.Name, h.Value)
	}
	if pcv := responseChargingVector(c.in.Request, c.as.cfg.HomeDomain); pcv != "" {
		fields.Set("P-Charging-Vector", pcv)
	}
	c.mu.Lock()
	c.toCaller = true
	c.mu.Unlock()
	if err := c.in.Progress(181, sip.Body{}, fields.Headers...); err != nil {
		c.log.Info("181 not sent", "error", err.Error())
	}
}

// keepHistory is TS 24.604 4.5.2.7 at the AS of the diverted-to user:
// a 180, 181 or 200 to the INVITE that comes without History-Info gets
// the one the INVITE came with.
func (c *call) keepHistory(resp, fields *sip.Message) {
	switch resp.StatusCode {
	case 180, 181, 200:
		if resp.Get("History-Info") == "" {
			for _, v := range c.in.Request.GetAll("History-Info") {
				fields.Add("History-Info", v)
			}
		}
	}
}

// divertOn is the evaluation of the rules on how the leg to the served
// user ended (TS 24.604 4.5.2.6.3): CFB on its 486 (Busy Here), CFNRc
// when the served user could not be reached, CFNR when nobody answered.
// The response that came first decides (4.6.7).
func (c *call) divertOn(r attemptResult, info simservs.CallInfo) (diversion, bool) {
	cdiv := c.communicationDiversion()
	if cdiv == nil || r.answered || r.cancelled {
		return diversion{}, false
	}
	d := diversion{response: r.final}
	var event simservs.DiversionEvent
	switch code := statusOf(r.final); {
	case r.noReply:
		// The timer, not a response, decided it.
		event, d.service, d.cause, d.response = simservs.OnNoAnswer, "CFNR", causeCFNR, nil
	case code == 480 && noAnswer(r.final):
		event, d.service, d.cause = simservs.OnNoAnswer, "CFNR", causeCFNR
	case code == 486:
		event, d.service, d.cause = simservs.OnBusy, "CFB", causeCFB
	case !r.provisional && (code == 0 || code == 408 || code == 500 || code == 503):
		// 4.5.2.6.6: no answer at all, or one of these before any
		// provisional response other than 100, tells that the served
		// user is not reachable.
		event, d.service, d.cause = simservs.OnNotReachable, "CFNRc", causeCFNRc
	default:
		return diversion{}, false
	}
	res := simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, event, info)
	if !res.Diverts() {
		return diversion{}, false
	}
	d.to = *res.ForwardTo
	return d, true
}

// noAnswer tells that a 480 (Temporarily Unavailable) says that the
// user did not answer: it has a Reason with Q.850 cause 19, which the
// AS takes as the end of the no reply time (TS 24.604 4.5.2.6.3 item
// 2, 4.5.2.6.6).
func noAnswer(resp *sip.Message) bool {
	for _, r := range sip.Reasons(resp) {
		if r.Protocol == sip.ReasonQ850 && r.HasCause && r.Cause == 19 {
			return true
		}
	}
	return false
}

func statusOf(resp *sip.Message) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// mayDivertLater tells whether a rule could divert the call once the
// leg to the served user has rung: CFNR, or CFB on a 486 after the
// callee alerted its user.
func (c *call) mayDivertLater(info simservs.CallInfo) bool {
	cdiv := c.communicationDiversion()
	if cdiv == nil {
		return false
	}
	return simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, simservs.OnNoAnswer, info).Diverts() ||
		simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, simservs.OnBusy, info).Diverts()
}

// leaveServedUser ends what the caller has of the leg to the served
// user before the call goes to the target. The caller holds one early
// dialog with the AS, which may carry the served user's answer already.
// With 199 (Early Dialog Terminated) that early dialog ends, and the
// target's responses come in a new one, with the target's answer (TS
// 24.604 4.5.2.6.3 item 2 b, RFC 6228).
//
// A caller that does not take 199 keeps its early dialog. For such a
// caller the AS withholds the served user's answer while a later
// diversion is possible (withholdSession), so the target's answer is
// the first that the caller gets.
func (c *call) leaveServedUser(d diversion) {
	c.mu.Lock()
	early := c.toCaller
	c.mu.Unlock()
	if !early || !c.in.Request.Supports(sip.Option199) {
		return
	}
	if err := c.in.EarlyDialogTerminated(d.cause); err != nil {
		c.log.Info("199 not sent", "error", err.Error())
		return
	}
	c.mu.Lock()
	c.toCaller, c.sessionToCaller, c.pending, c.sessionFrom = false, false, nil, ""
	c.mu.Unlock()
}

// withholdSession tells whether the AS keeps the served user's answer
// from the caller until the 2xx, because a diversion after alerting is
// possible and the caller cannot take a second early dialog: it does
// not support 199 (leaveServedUser). The AS then acknowledges the
// reliable responses of the leg itself and sends the caller their
// status codes without a session description, and puts the answer in
// the 2xx.
//
// The INVITE must have an offer, so that the answer is the callee's
// to keep. With preconditions the answer cannot wait for the 2xx: the
// callee does not alert its user until the caller has confirmed its
// resources, which needs the answer (RFC 3312). Such a call keeps its
// early dialog as it is; an IR.92 UE, which uses preconditions, takes
// 199 (IR.92 2.2.5).
func (c *call) withholdSession(info simservs.CallInfo) bool {
	req := c.in.Request
	return !req.Supports(sip.Option199) && len(req.Body) > 0 &&
		!req.Supports(sip.OptionPrecondition) && !req.Requires(sip.OptionPrecondition) &&
		c.mayDivertLater(info)
}

// noReplyTimer is how long the leg to the served user may ring before
// CFNR diverts the call: the NoReplyTimer of the user, or else the
// operator's default (TS 24.604 4.8.1, 4.9.1.1A). It is 0 when no rule
// for no answer can match this call, so that no timer runs. An operator
// default below the least time a user may set (5 s) counts as that
// time.
func (c *call) noReplyTimer(info simservs.CallInfo) time.Duration {
	cdiv := c.communicationDiversion()
	if cdiv == nil || !simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, simservs.OnNoAnswer, info).Diverts() {
		return 0
	}
	return time.Duration(max(cdiv.NoReplyTimer(), simservs.MinNoReplyTimer)) * time.Second
}
