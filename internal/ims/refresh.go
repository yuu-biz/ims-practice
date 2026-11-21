package ims

import "time"

// RefreshAfter is when to refresh a registration or a subscription to
// the reg event package that lasts expires: 600 s before it ends if it
// lasts more than 1200 s, otherwise when half of it has passed. TS 24.229
// gives the same rule for the UE's registration (5.1.1.4.1), the UE's
// subscription (5.1.1.3) and the P-CSCF's subscription (5.2.3).
func RefreshAfter(expires time.Duration) time.Duration {
	if expires > 1200*time.Second {
		return expires - 600*time.Second
	}
	return expires / 2
}

// RegEventMargin is how much longer a network node asks its
// subscription to the reg event package to last than the registration
// it watches, so that the subscription does not end first: TS 24.229
// 5.2.3 item e asks the P-CSCF for that. It is the margin that RFC 3680
// 4.4 puts between the default registration, 3600 s, and the default
// subscription, 3761 s.
const RegEventMargin = 161 * time.Second

// RegAwaitAuth is the reg-await-auth timer of TS 24.229 Table 7.7.1, 4
// minutes: how long the S-CSCF waits for the answer to a challenge
// (5.4.1.2.1 step 7), and with IMS AKA the temporary lifetime of the
// security associations that the challenge sets up in the UE and in the
// P-CSCF (5.1.1.5.1, 5.2.2.2).
const RegAwaitAuth = 4 * time.Minute
