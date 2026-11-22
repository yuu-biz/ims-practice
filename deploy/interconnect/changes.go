package interconnect

import "github.com/yuu-biz/ims-practice/internal/yamledit"

// changes is every difference between a role file of deploy/compose and
// its copy in a: the IBCF through which network A's S-CSCF and
// IP-SM-GW send what is for another network. The S-CSCF does so in
// steps 11 and 12 of TS 24.229 5.4.3.2; the IP-SM-GW does the same for
// a short message to a user of network B.
//
// After changing this table or a file in deploy/compose, run
//
//	go generate ./deploy/interconnect/...
var changes = []yamledit.Change{
	{
		File: "scscf.yaml",
		Key:  "scscf.ibcf",
		Add:  true,
		Comment: `A number that ENUM maps to network B's domain, or a SIP URI of
another domain, goes here, on top of Route (TS 24.229 5.4.3.2
steps 11 and 12).`,
		Value: ibcf,
	},
	{
		File: "ipsmgw.yaml",
		Key:  "ipsmgw.ibcf",
		Add:  true,
		Comment: `With it, the SC finds the users of network B by ENUM, and the
IP-SM-GW sends their short messages out here, as the S-CSCF does
with a request for another network.`,
		Value: ibcf,
	},
}

// ibcf is the IBCF of network A (a/ibcf.yaml).
const ibcf = "sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060"
