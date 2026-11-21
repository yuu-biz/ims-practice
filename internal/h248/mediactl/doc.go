// Package mediactl is media control with H.248, in two profiles: Iq
// between the IMS-ALG and the IMS-AGW (TS 29.334), and Mp between the
// MRFC and the MRFP (TS 29.333). It holds both sides of each profile
// and the operations at each boundary (Relay and Processor below). Its
// tests and examples run a Controller and a Gateway against each other
// over UDP, so the H.248 messages can be captured.
//
// The nodes of this project do not use this package. The P-CSCF and
// the MRFC control the media node through the HTTP API of internal/agw,
// with the operations of pcscf.Relay and mrfc.MRFP. Those are not the
// operations of Relay and Processor: pcscf.Relay has Add, Modify and
// Subtract on the types of internal/agw, where Relay has Reserve,
// Configure and Release on the types of this package. A Controller
// cannot take the place of the HTTP client in the P-CSCF or the MRFC.
//
// Over the Iq reference point (TS 29.334), Relay is the set of
// operations at that boundary, in the terms of the call related
// procedures of Iq. Two types stand on either side of the H.248
// messages:
//
//   - Controller, the IMS-ALG side, implements Relay by sending Add,
//     Modify and Subtract to a media gateway, and accepts the
//     registrations of media gateways.
//   - Gateway, the IMS-AGW side, registers with a controller, carries
//     out the commands it receives by calling the Relay it is given,
//     and reports events with Notify.
//
// Over the Mp reference point (TS 29.333), Processor is the set of
// operations with which the MRFC has the MRFP play announcements and
// mix conferences. The same two types serve: a Controller set up with
// MpProfile implements Processor, and hands the end of each
// announcement to its caller; a Gateway set up with a Processor carries
// out the commands, and reports the end of an announcement that the
// processor tells it of with Gateway.SignalCompleted.
//
// Both watch the control association as TS 29.334 5.12 (Table 5.12.3)
// and TS 29.333 5.12 (Table 5.12.3) ask over UDP: the controller sends
// an empty AuditValue on ROOT when it has had nothing else to send for
// a while, and the gateway sets the inactivity timer it/ito that the
// controller gives it (H.248.14). A gateway that loses its controller
// registers again with ServiceChange Disconnected, and the controller
// fails the calls for the contexts of a gateway that has stopped
// answering.
//
// The package h248 below reads and writes the messages and carries the
// transactions; this package holds the procedures of the profiles only,
// so that each can be read on its own.
package mediactl
