package diameter

const (
	// ApplicationIDCx is the Cx application (IANA AAA parameters,
	// 3GPP TS 29.228 and TS 29.229).
	ApplicationIDCx uint32 = 16777216
	// ApplicationIDSh is the Sh application (IANA AAA parameters,
	// 3GPP TS 29.328 and TS 29.329 6.4.4).
	ApplicationIDSh uint32 = 16777217
	// ApplicationIDRx is the Rx application between the P-CSCF and the
	// PCRF (TS 29.214 5.1). go-diameter's default dictionary has it.
	ApplicationIDRx uint32 = 16777236
	// applicationIDRelay is advertised by relay agents (RFC 6733 2.4).
	applicationIDRelay uint32 = 0xffffffff
	// VendorID3GPP is the IANA enterprise number of 3GPP.
	VendorID3GPP uint32 = 10415
)

// Result-Code values (RFC 6733 7.1).
const (
	DiameterSuccess            uint32 = 2001 // 7.1.2
	DiameterCommandUnsupported uint32 = 3001 // 7.1.3
	// DiameterTooBusy is the answer of a node that is up but cannot
	// serve this request. RFC 6733 7.1.3 tells its sender to try an
	// alternate peer.
	DiameterTooBusy               uint32 = 3004 // 7.1.3
	DiameterUnknownSessionID      uint32 = 5002 // 7.1.5
	DiameterAuthorizationRejected uint32 = 5003 // 7.1.5
	DiameterAVPOccursTooManyTimes uint32 = 5009 // 7.1.5
	DiameterNoCommonApplication   uint32 = 5010 // 7.1.5
	DiameterUnableToComply        uint32 = 5012 // 7.1.5
)

// DisconnectCause is the Disconnect-Cause AVP of a DPR (RFC 6733 5.4.3).
type DisconnectCause uint32

const (
	Rebooting            DisconnectCause = 0
	Busy                 DisconnectCause = 1
	DoNotWantToTalkToYou DisconnectCause = 2
)
