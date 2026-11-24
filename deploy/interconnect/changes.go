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
	{
		File: "ipsmgw.yaml",
		Key:  "diameter.peers",
		Comment: `The HSS alone. Neither network of this deployment runs a Service
Centre of its own, so each IP-SM-GW uses the stub of the SC inside
its own process. The stub is what finds a user of the other
network by ENUM and sends the short message out through the IBCF;
a Service Centre would ask the HSS of the recipient instead, which
needs no interconnect of the IMS (TS 23.204 6.4 step 3).`,
		Value: []peer{{Address: hssAddress, OriginHost: hssHost, Kind: "hss"}},
	},
}

// peer is one entry of the diameter.peers sequence, with its keys in the
// order the files of deploy/compose write them.
type peer struct {
	Address    string `yaml:"address"`
	OriginHost string `yaml:"origin_host"`
	Kind       string `yaml:"kind"`
}

// The HSS of network A, as deploy/compose names it.
const (
	hssHost    = "hss.ims.mnc001.mcc001.3gppnetwork.org"
	hssAddress = hssHost + ":3868"
)

// ibcf is the IBCF of network A (a/ibcf.yaml).
const ibcf = "sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060"
