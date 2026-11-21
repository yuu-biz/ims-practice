// Package interconnect holds what an IBCF does to SIP messages and SDP
// bodies at the border between two IMS networks (TS 24.229 5.10, TS
// 29.165): the interconnect profile of each peer network, the
// screening of methods, option tags and header fields, the trust
// domain boundary (TS 24.229 4.4), privacy (5.10.8), the format of
// telephone numbers (TS 29.165 8.1), the codecs the peer accepts (TS
// 29.165 7.1), and which header fields an IBCF that acts as a B2BUA
// copies from one of its dialogs to the other (5.10.5).
//
// Every function is pure: it changes the message it is given and does
// no network I/O. The IBCF role receives a message on one leg, calls
// the methods of Border on a copy for the direction the message goes,
// and builds the message of its other leg from the result.
//
// A message goes in one of two directions. Exit is from the home
// network to the peer, which the IBCF does as an exit point (TS 24.229
// 5.10.2). Entry is from the peer into the home network, which the IBCF
// does as an entry point (5.10.3). A response goes the other way from
// its request: the 200 OK to an INVITE that left the home network
// enters it.
//
// SIMPLIFIED: TS 29.165 5.3.1. Only the non-roaming II-NNI is handled,
// between the home networks of the caller and the callee. The roaming
// II-NNI (REGISTER, Path, Service-Route) and the loopback traversal
// scenario are out of scope, as this project has no roaming UE, and no
// message carries the iotl parameter that would tell them apart (RFC
// 7549).
package interconnect
