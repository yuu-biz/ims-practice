package smsc

import (
	"context"

	"github.com/yuu-biz/ims-practice/internal/sgd"
)

// diameterUnableToComply is the Result-Code of RFC 6733 7.1.5 for a
// request that the node could not carry out for a reason of its own.
const diameterUnableToComply uint32 = 5012

// AlertServiceCentre takes the ALR of the HSS: a user for whom the MWD
// held this SC's address can take short messages again (TS 29.338
// 5.2.2, TS 23.040 3.2.7). The SC makes the queue of the user due at
// once and starts delivering it, ahead of its retry schedule.
//
// TS 29.338 5.2.2.3 has the SMS-IWMSC do nothing for an SC address it
// does not know, and S6c defines no error for ALA, so such an alert is
// answered with success like any other. So is the alert for a user the
// SC holds nothing for: the messages may have expired, or another
// instance may have delivered them on a retry.
//
// SIMPLIFIED: TS 23.040 3.2.7. The SC finds the queue by the MSISDN that
// the alert carries, which TS 23.040 calls the MSISDN-Alert. A user with
// several MSISDNs would be alerted by one of them only; the users of
// this project have one each.
func (c *SMSC) AlertServiceCentre(ctx context.Context, req sgd.AlertServiceCentreRequest) sgd.AlertServiceCentreAnswer {
	ok := sgd.AlertServiceCentreAnswer{Result: sgd.Base(sgd.DiameterSuccess)}
	msisdn := req.UserIdentifier.MSISDN
	log := c.log.With("session_id", req.SessionID, "sc_address", req.SCAddress, "msisdn", msisdn)
	if req.SCAddress != c.cfg.Address {
		log.Info("alert ignored: unknown SC address")
		return ok
	}
	if msisdn == "" {
		log.Info("alert ignored: it names no MSISDN")
		return ok
	}
	found, err := c.store.Alert(ctx, msisdn, c.clock.Now())
	if err != nil {
		log.Warn("alert lost: the store failed", "error", err.Error())
		return sgd.AlertServiceCentreAnswer{Result: sgd.Base(diameterUnableToComply)}
	}
	if !found {
		log.Info("alert for a user the SC holds nothing for")
		return ok
	}
	log.Info("alert: delivering the short messages held for the user")
	c.deliverSoon(msisdn)
	return ok
}
