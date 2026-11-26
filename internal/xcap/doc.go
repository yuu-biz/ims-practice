// Package xcap is the Ut reference point of the MMTel supplementary
// services: an XCAP server for the simservs application usage of TS
// 24.623 (RFC 4825), which the MMTel AS runs, and the XCAP client that
// a UE uses to read and change its settings (GSMA IR.92 2.3.2).
//
// The server keeps no document of its own. Each request reads the
// user's MMTelServices document from the HSS as Sh Repository Data
// (TS 29.364), shows or changes its user parts through package
// simservs, and writes it back with the Sequence Number it read, so
// that any instance of the AS can serve any request. The
// Sequence Number is also the entity tag of the document (RFC 4825
// 7.11), so If-Match and the HSS's check of the Sequence Number (TS
// 29.328 6.1.2.1) guard the same version.
//
// Users authenticate with HTTP Digest (RFC 7616), as TS 24.623
// 5.2.3.2.1 has the AS do when GBA is not used. The H(A1) values of
// the user's Ut password are Repository Data in the HSS too, under
// CredentialServiceIndication.
//
// One change of a call forwarding setting, as Wireshark shows it on
// Ut and Sh:
//
//	UE                         XCAP server (MMTel AS)              HSS
//	|  GET .../simservs.xml          |                                |
//	|------------------------------->|                                |
//	|  401, WWW-Authenticate: Digest |                                |
//	|  algorithm=SHA-256, then MD5   |                                |
//	|<-------------------------------|                                |
//	|  GET, Authorization: Digest    |                                |
//	|------------------------------->|  UDR Ut-Digest-Credential      |
//	|                                |  UDR MMTEL-Services            |
//	|                                |------------------------------->|
//	|                                |<-------------------------------|  UDA, Sequence-Number n
//	|  200, ETag "n", simservs.xml   |                                |
//	|<-------------------------------|                                |
//	|  PUT .../~~/.../cp:rule[@id="cfu"], If-Match "n"                |
//	|------------------------------->|  UDR, UDR as above             |
//	|                                |  PUR MMTEL-Services, n+1       |
//	|                                |------------------------------->|
//	|                                |<-------------------------------|  PUA
//	|  200, ETag "n+1"               |                                |
//	|<-------------------------------|                                |
//
// SIMPLIFIED: TS 24.623 5.2.2 and 5.2.3.1, GSMA IR.92 2.2.2. GBA (TS
// 33.222, TS 24.109) and the authentication proxy are left out. GBA
// needs a BSF and the Zh interface, too much for this project to learn
// from, and IR.92 only recommends it.
//
// SIMPLIFIED: TS 24.623 5.2.1.2 and 5.2.3.3. A UE should subscribe to
// changes of its document with the xcap-diff event package (RFC 5875),
// which the AS then answers over SIP. Neither the UE nor the MMTel AS
// does; the UE reads the document again before each change instead.
package xcap
