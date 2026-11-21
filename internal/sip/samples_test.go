package sip

// Sample messages of an IMS registration. The identities are the test
// identities of the home network with MCC 001 and MNC 01 (TS 23.003),
// and the addresses come from the documentation ranges.

const imsDomain = "ims.mnc001.mcc001.3gppnetwork.org"

// initialRegister is the first REGISTER from the UE. Its Authorization
// has an empty nonce and response, which TS 24.229 asks for so that the
// network learns the private user identity before it challenges.
const initialRegister = "REGISTER sip:ims.mnc001.mcc001.3gppnetwork.org SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bKnashds7;rport\r\n" +
	"Max-Forwards: 70\r\n" +
	"From: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;tag=4fa3\r\n" +
	"To: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>\r\n" +
	"Call-ID: apb03a0s09dkjdfglkj49111\r\n" +
	"CSeq: 1 REGISTER\r\n" +
	"Contact: <sip:001010000000001@192.0.2.10:5060>;expires=600000\r\n" +
	`Authorization: Digest username="001010000000001@ims.mnc001.mcc001.3gppnetwork.org", realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="", uri="sip:ims.mnc001.mcc001.3gppnetwork.org", response=""` + "\r\n" +
	"Supported: path\r\n" +
	"Expires: 600000\r\n" +
	"Content-Length: 0\r\n" +
	"\r\n"

const initialRegisterAuthorization = `Digest username="001010000000001@ims.mnc001.mcc001.3gppnetwork.org", realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="", uri="sip:ims.mnc001.mcc001.3gppnetwork.org", response=""`

const unauthorized = "SIP/2.0 401 Unauthorized\r\n" +
	"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bKnashds7;rport=5060;received=192.0.2.10\r\n" +
	"From: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;tag=4fa3\r\n" +
	"To: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;tag=5ef4\r\n" +
	"Call-ID: apb03a0s09dkjdfglkj49111\r\n" +
	"CSeq: 1 REGISTER\r\n" +
	`WWW-Authenticate: Digest realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="a1b2c3d4e5f60718293a4b5c6d7e8f90", algorithm=MD5, qop="auth"` + "\r\n" +
	"Content-Length: 0\r\n" +
	"\r\n"

const registerOK = "SIP/2.0 200 OK\r\n" +
	"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK9a8b7c;rport=5060;received=192.0.2.10\r\n" +
	"From: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;tag=4fa3\r\n" +
	"To: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;tag=6ab1\r\n" +
	"Call-ID: apb03a0s09dkjdfglkj49111\r\n" +
	"CSeq: 2 REGISTER\r\n" +
	"Contact: <sip:001010000000001@192.0.2.10:5060>;expires=600000\r\n" +
	"Path: <sip:term@pcscf.ims.mnc001.mcc001.3gppnetwork.org;lr>\r\n" +
	"Service-Route: <sip:orig@scscf.ims.mnc001.mcc001.3gppnetwork.org;lr>\r\n" +
	"P-Associated-URI: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>, <sip:+0010000000001@ims.mnc001.mcc001.3gppnetwork.org;user=phone>, <tel:+0010000000001>\r\n" +
	"Content-Length: 0\r\n" +
	"\r\n"

// compactRegister uses compact header names, a Via list that spans two
// rows with two values in the first, and a folded header line.
const compactRegister = "REGISTER sip:ims.mnc001.mcc001.3gppnetwork.org SIP/2.0\r\n" +
	"v: SIP/2.0/UDP pcscf.ims.mnc001.mcc001.3gppnetwork.org;branch=z9hG4bKp1, SIP/2.0/UDP 198.51.100.7:5070;branch=z9hG4bKp2\r\n" +
	"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bKue;received=203.0.113.9;rport=40000\r\n" +
	"Max-Forwards: 68\r\n" +
	"f: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;tag=4fa3\r\n" +
	"t: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>\r\n" +
	"i: cmp9f00a1\r\n" +
	"CSeq: 3 REGISTER\r\n" +
	"m: \"UE, test\" <sip:001010000000001@192.0.2.10:5060>;expires=600000\r\n" +
	"s: registration\r\n" +
	"  in progress\r\n" +
	"l: 0\r\n" +
	"\r\n"
