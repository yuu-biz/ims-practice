// Package ibcf is the IBCF role: the border of the home network towards
// the IMS networks it interconnects with (TS 24.229 5.10, TS 29.165
// 5.2.1). The IBCF is a B2BUA (5.10.5): a call that crosses the border
// has one dialog between the home network and the IBCF, and another
// between the IBCF and the peer, each with its own Call-ID, tags, CSeq,
// Via and Contact. On the way from one dialog to the other the IBCF
// applies the agreement with the peer, an interconnect.Profile, through
// interconnect.Border: the screening of methods, option tags and header
// fields, the trust domain boundary, privacy, the format of telephone
// numbers and the codecs. It puts the media of each call through a
// TrGW, whose addresses it writes into the SDP. Nothing that routes a
// message inside one network crosses into the other, so the topology
// of the home network stays hidden without the encryption of 5.10.4.
// The one exception is a Contact that 5.10.5 has cross as it came, a
// GRUU or a conference URI, with the IBCF in Record-Route: what that
// URI names, the other network sees.
//
// The IBCF has two UAs. The internal one faces the home network on the
// Mx reference point (TS 29.165 3.2), and a request that comes to it
// leaves for a peer: the IBCF is its exit point (TS 24.229 5.10.2). The
// external one faces the peers on Ici, and a request that comes to it
// enters the home network: the IBCF is its entry point (5.10.3). The
// IBCF finds the peer of a request that leaves by the host of its next
// hop, and the peer of one that enters by the address it comes from
// (Peers). A request from an address of no peer gets no response at
// all: the external UA drops it before it is handled (IBCF.Screen).
//
// Several IBCF instances can share the work. Each instance names itself
// in the Contact of its dialogs, so that the requests within a call
// come back to the instance that holds its two dialogs, and the initial
// requests go to any instance of the pool. What an instance keeps of
// its calls beyond its process is a record per call in the Store, under
// its name: the TrGW context, and the two dialogs once the call is
// answered, as they are after each request within the call (TS 24.229
// 5.10.2.2 step 2, 5.10.2.3, 5.10.3.2 step 2, 5.10.3.3). An instance
// that starts again under the same name ends the calls of its earlier
// process from those records (ReleaseLeftovers), and any instance finds
// there the call that a Replaces names. Only a Store outside
// the process, such as the one of internal/kvstore/ibcfstore, outlives
// the process; a MemoryStore goes with it.
//
// The IBCF counts what it lets across and what it turns away, per
// peer, in the Registry of Config.Metrics (metrics.go).
//
// SIMPLIFIED: TS 24.229 5.10.2.4 and 5.10.3.4. The IBCF ends a call by
// itself only when a leg fails, when the TrGW does not take an SDP of the
// call (notThroughTrGW, notPassedOn), and when it takes the place of its
// earlier process (ReleaseLeftovers). The TrGW reports nothing by
// itself, so a call whose TrGW context is lost goes on without media
// until a party ends it, or until the next SDP of the call finds the
// context gone.
//
// SIMPLIFIED: TS 24.229 5.10.2.1, 5.10.3.1 and TS 29.165 5.3.1. Only the
// non-roaming II-NNI is handled. A REGISTER does not cross (the border
// answers it 405, TS 29.165 Table 6.1), and neither the Path of
// 5.10.3.1 nor the restoration procedures of 5.10.3.2 (P-CSCF, step 6A
// and the end of the clause) and 5.10.3.5 (S-CSCF) are there.
//
// SIMPLIFIED: TS 24.229 5.10.2.2 steps 5A, 6 and 7, 5.10.3.2 steps 1A,
// 1B, 2C, 2D, 5 to 9 and the steps for its responses. No node of this
// project writes charging records, and the project has no private
// network traffic, no emergency calls and no calling number
// verification, so the IBCF neither stores
// P-Charging-Function-Addresses nor handles fe-identifier,
// P-Private-Network-Indication, Resource-Priority or received-realm,
// and it adds orig only where step 4 passes it on; the border removes
// what may not cross.
//
// The Replaces of an INVITE (RFC 3891) names a dialog as the IBCF has
// it on the side where the INVITE comes in. When that dialog is one
// side of a call that crosses the border here, the IBCF passes the
// Replaces on naming the dialog of the other side, the one that the
// next hop knows, as an IMS-ALG that correlates the dialog identifiers
// of its two dialogs (TS 24.229 5.10.5). The call may be one that
// another instance holds: the Store finds it by the dialog
// (Store.CallOf). Any other Replaces goes on as it came. That is how a
// conference focus of one network takes over the call of a user of the
// other (TS 24.147 5.3.2.5.4).
//
// SIMPLIFIED: RFC 3515 2.1. A Refer-To that holds a Replaces goes on as
// it came. When the dialog that it names crosses this IBCF and the
// referee's INVITE does not, the target finds no dialog of its own
// there. Only a call transfer between two users of the other network
// sends such a REFER, and this project has no call transfer (TS
// 24.629); a UE that merges its calls refers the focus of its own
// network.
//
// SIMPLIFIED: TS 24.229 6.7.1.4.1. IPv4 only: the TrGW
// translates addresses and ports (NA(P)T) but not between IPv4 and IPv6
// (NA(P)T-PT), and the IBCF inserts no altc attributes.
package ibcf
