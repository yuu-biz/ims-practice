package hss

import (
	"context"

	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// This file holds the database usage measurements of TS 32.409 4.2.1
// that the HSS serves: how many subscriptions and public user
// identities it holds, and how many of them are registered. They are
// gauges, which TS 32.409 4.2.1.1.1 and 4.2.1.2.1 move when a
// subscription is provisioned or removed and when a public user
// identity enters or leaves the registered state. The HSS does not
// write them as those changes happen; it reads them from its Store
// when Prometheus scrapes, and the Store keeps them without walking its
// subscribers (Counts). Two registrations at once therefore cannot
// leave a gauge holding the number that the earlier of them read.
//
// They carry no role label, unlike the measurements that several roles
// count (internal/ims/measurements.go): their names say which role
// counts them, and no other role registers them, so nothing can
// collide in the registry of a node.
//
// TS 32.409 gives the HSS these further measurements, which it does not
// serve.
//
//   - The provisioned private user identities (4.2.1.1.2,
//     DBU.NbrPriUserId). A subscription of this HSS has exactly one
//     private user identity (Subscription), so the number would be the
//     subscriptions (4.2.1.1.1) under a second name.
//   - The public user identities in the unregistered state (4.2.1.2.2,
//     DBU.NbrUnregPubUserId), that is, those whose profile an S-CSCF
//     holds for services to a user who is not registered.
//   - The private and public service identities (4.2.1.1.5 to
//     4.2.1.1.7). The public service identities of this network are
//     wildcarded ranges (PSIUser), so the HSS holds no number of them:
//     one range stands for every conference URI of a subdomain.
//   - The requests it receives and the answers it sends (4.2.12.1
//     UR.Request and 4.2.12.2 UR.Answer). These are the Diameter
//     messages of every role, which internal/diameter counts in one
//     place for all of them.

// databaseUsageGauges are the database usage measurements of TS 32.409
// 4.2.1 that the HSS serves, each from the numbers of one read of the
// store.
var databaseUsageGauges = []struct {
	name   string
	help   string
	labels []metrics.Label
	// samples gives the value of each series of the measurement from
	// the numbers read at that scrape.
	samples func(emit func(value float64, labelValues ...string), c Counts)
}{
	{
		name:    "ims_hss_subscriptions",
		help:    "IMS subscriptions provisioned in the HSS (TS 32.409 DBU.NbrImsSubscription).",
		samples: func(emit func(float64, ...string), c Counts) { emit(float64(c.Subscriptions)) },
	},
	{
		name: "ims_hss_public_identities",
		help: "Public User Identities provisioned in the HSS, by the format of the URI (TS 32.409 DBU.NbrSipPubUserId, DBU.NbrTelPubUserId).",
		// The URI formats that TS 32.409 4.2.1.1.3 and 4.2.1.1.4 count
		// separately.
		labels: []metrics.Label{{Name: "format", Values: []string{"sip", "tel"}}},
		samples: func(emit func(float64, ...string), c Counts) {
			emit(float64(c.SIPPublicIdentities), "sip")
			emit(float64(c.TelPublicIdentities), "tel")
		},
	},
	{
		name:    "ims_hss_registered_public_identities",
		help:    "Public User Identities in the registered state (TS 32.409 DBU.NbrRegPubUserId).",
		samples: func(emit func(float64, ...string), c Counts) { emit(float64(c.RegisteredPublicIdentities)) },
	},
	{
		name:    "ims_hss_registered_private_identities",
		help:    "Private User Identities with a registered Public User Identity (TS 32.409 DBU.NbrRegPriUsrId).",
		samples: func(emit func(float64, ...string), c Counts) { emit(float64(c.RegisteredPrivateIdentities)) },
	},
}

// registerDatabaseUsage registers the gauges of TS 32.409 4.2.1 in reg,
// each read from the store at the scrape. A store that cannot be read
// then gives no sample at all, so that Prometheus shows the gap rather
// than a number the HSS cannot vouch for; the next scrape has the
// numbers again. The read takes no deadline: a Store keeps these
// numbers rather than working them out, so reading them costs the same
// however many subscribers it holds (Counts).
func (h *HSS) registerDatabaseUsage(reg *metrics.Registry) error {
	for _, g := range databaseUsageGauges {
		read := func(emit func(value float64, labelValues ...string)) {
			c, kept, err := h.store.Counts(context.Background())
			if err != nil {
				h.log.Warn("cannot read the numbers of the subscribers", "error", err.Error())
				return
			}
			if kept {
				g.samples(emit, c)
			}
		}
		if err := reg.NewGaugeFunc(g.name, g.help, read, g.labels...); err != nil {
			return err
		}
	}
	return nil
}
