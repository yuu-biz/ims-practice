package hss

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// MSISDNPlaceholder in the ServiceInfo of a configured criterion stands
// for the MSISDN of the subscriber whose profile the HSS builds. The
// S-CSCF hands ServiceInfo to the application server in the body of the
// third-party REGISTER (TS 24.229 5.4.1.7A). That is how the
// IP-SM-GW learns the number of the user whose registration the S-CSCF
// tells it of (TS 23.204 6.1 step 4).
const MSISDNPlaceholder = "{msisdn}"

// userProfile is the profile of u that the SAA carries in User-Data (TS
// 29.228 6.1.2.1): the identities of the subscription and the criteria
// of the configuration.
//
// SIMPLIFIED: TS 29.228 B.2.2. The HSS stub gives every subscription the
// same criteria; only the MSISDN in ServiceInfo differs. A product HSS
// keeps the criteria of each service profile. The stub has no service
// subscriptions that would differ between users for SMS.
func (h *HSS) userProfile(u Subscriber) cx.UserProfile {
	var criteria []ifc.InitialFilterCriteria
	for _, c := range h.config.InitialFilterCriteria {
		c.ApplicationServer.ServiceInfo = strings.ReplaceAll(c.ApplicationServer.ServiceInfo, MSISDNPlaceholder, u.MSISDN())
		criteria = append(criteria, c)
	}
	p := cx.UserProfile{PrivateIdentity: u.PrivateIdentity, PublicIdentities: u.PublicIdentities, InitialFilterCriteria: criteria}
	for _, id := range u.PublicIdentities {
		if u.Barred(id) {
			p.BarredIdentities = append(p.BarredIdentities, id)
		}
	}
	return p
}

// MSISDN is the MSISDN of the subscription (TS 29.328 7.6.9): the
// number of the first tel URI among the public identities in its
// canonical form, so without visual separators, parameters or the "+"
// of the global number. It is empty when the subscription has no global
// tel URI of 1 to 15 digits.
//
// SIMPLIFIED: TS 29.328 7.6.9. An HSS holds the MSISDN as subscription
// data of its own. The stub takes it from the first tel URI among the
// public identities, so that the configuration states each number once
// and a user is found by MSISDN through the tel URI.
func (s Subscription) MSISDN() string {
	for _, id := range s.PublicIdentities {
		canonical, err := ims.CanonicalURI(id)
		if err != nil {
			continue
		}
		if number, ok := strings.CutPrefix(canonical, "tel:+"); ok && sh.ValidMSISDN(number) {
			return number
		}
	}
	return ""
}
