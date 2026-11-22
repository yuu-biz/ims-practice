package mmtel

import (
	"slices"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds which header fields go from one leg of a call to the
// other. TS 24.229 5.7.5.1 has a routeing B2BUA pass every signalling
// element on, supported or not, except those that a service changes.
// What the AS does not pass on are the header fields that belong to one
// leg (b2bua.LegFields): the UA writes its own for each leg.

// legFields are the fields of each leg of the AS: those of every B2BUA,
// and P-Served-User, which the S-CSCF writes for the AS only (RFC 5502
// 6), and P-Charging-Vector, which the AS writes anew for each leg
// (chargingVector).
var legFields = append(slices.Clone(b2bua.LegFields), "P-Served-User", "P-Charging-Vector")

// carried returns the header fields of m that go on to the other leg,
// as a message to read and change them with. keep names leg fields that
// go on all the same.
func carried(m *sip.Message, keep ...string) *sip.Message {
	return &sip.Message{Headers: b2bua.CrossingFields(m, legFields, keep)}
}

// chargingVector is the P-Charging-Vector of a request that the AS
// sends on: TS 24.229 5.7.5.1 has a routeing B2BUA keep the orig-ioi of
// the request it received and take it out of the request it sends. The
// icid-value stays, so that both legs are charged as one call.
func chargingVector(req *sip.Message) string {
	c, ok := sip.ChargingVectorOf(req)
	if !ok {
		return ""
	}
	c.OrigIOI, c.TermIOI = "", ""
	return c.String()
}

// responseChargingVector is the P-Charging-Vector of a response that
// the AS sends to req (TS 24.229 5.7.5.1): the icid-value and the
// orig-ioi that req carried, and a type 3 term-ioi that names the home
// network.
func responseChargingVector(req *sip.Message, homeDomain string) string {
	c, ok := sip.ChargingVectorOf(req)
	if !ok {
		return ""
	}
	return c.Response(homeDomain).String()
}
