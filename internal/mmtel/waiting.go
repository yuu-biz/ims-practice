package mmtel

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// callWaiting is the Alert-Info value with which the callee's UE tells
// that the call waits behind another one (RFC 7462, TS 24.615
// 4.5.5.3.2).
const callWaiting = "<urn:alert:service:call-waiting>"

// notifyWaiting is the terminal based CW at the AS of the callee (TS
// 24.615 4.5.5.2.3): a 180 (Ringing) with the call-waiting Alert-Info
// tells the AS that the call is waiting. When the subscription says
// that the caller does not learn of it, the Alert-Info goes; otherwise
// the 180 goes on as it is. By default the caller learns of it. The
// network based CW is optional for the network (GSMA IR.92 2.3.4) and
// is not here.
//
// The caller also hears an announcement, when the configuration names
// one (announceWaiting).
func (c *call) notifyWaiting(resp, fields *sip.Message) {
	if resp.StatusCode != 180 {
		return
	}
	var kept []string
	waiting := false
	for _, row := range fields.GetAll("Alert-Info") {
		for _, v := range strings.Split(row, ",") {
			if v = strings.TrimSpace(v); strings.EqualFold(v, callWaiting) {
				waiting = true
			} else if v != "" {
				kept = append(kept, v)
			}
		}
	}
	if !waiting {
		return
	}
	c.actedOn("CW")
	if c.callerLearnsOfWaiting() {
		c.log.Info("CW: the call waits, and the caller learns of it")
		return
	}
	c.log.Info("CW: the call waits, without telling the caller")
	fields.Remove("Alert-Info")
	if len(kept) > 0 {
		fields.Set("Alert-Info", strings.Join(kept, ", "))
	}
}

// callerLearnsOfWaiting is the subscription option of TS 24.615 4.3.1:
// whether the calling user receives notification that its call is
// waiting. A user without the operator part of CW gets it.
func (c *call) callerLearnsOfWaiting() bool {
	cw := c.services.CompleteCommunicationWaiting
	return cw == nil || !cw.OperatorCommunicationWaiting.Authorized || cw.OperatorCommunicationWaiting.CallingUserReceivesNotificationHisCallIsWaiting
}
