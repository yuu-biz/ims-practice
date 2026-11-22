package mmtel

import (
	"context"
	"time"

	"github.com/yuu-biz/ims-practice/internal/simservs"
)

// shTimeout bounds the wait for the HSS, well within the time the
// S-CSCF gives the AS before its default handling (Timer B).
const shTimeout = 5 * time.Second

// services reads the settings of user's services: the MMTelServices
// document that the HSS keeps as the user's repository data (TS 29.364
// 7.2.0, Sh-Pull of TS 29.328 6.1.1). The AS reads it for every initial
// INVITE and keeps no copy, so a change over Ut applies from the
// next call on, whichever instance of the AS takes it.
//
// A user that has no document gets every service in its default state,
// as one whose document leaves a service out does: each service file
// tells what its default is. Together they change nothing in the call,
// so that a user without services gets the call it would get without
// the AS.
//
// SIMPLIFIED: TS 29.364 8.1. An AS that reads the data should subscribe
// to its changes (Sh-Subs-Notif) and take the notifications. This AS
// reads the data again for each call instead, which needs neither.
func (as *AS) services(user string) (simservs.MMTelServices, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shTimeout)
	defer cancel()
	data, found, err := as.hss.RepositoryData(ctx, user, simservs.ServiceIndication)
	if err != nil {
		return simservs.MMTelServices{}, err
	}
	if !found || data.ServiceData == nil {
		return simservs.MMTelServices{}, nil
	}
	return simservs.ParseMMTelServices(data.ServiceData.XML)
}
