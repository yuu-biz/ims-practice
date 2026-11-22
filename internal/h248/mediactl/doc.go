// Package mediactl is media control with H.248, in two profiles: Iq
// between the IMS-ALG and the IMS-AGW (TS 29.334), and Mp between the
// MRFC and the MRFP (TS 29.333). It holds both sides of each profile.
// Its tests and examples run a Controller and a Gateway against each
// other over UDP, so the H.248 messages can be captured.
// No node of this project runs it: no binary in cmd builds it in.
//
// The operations carry the names of the H.248 commands, and their
// types are those of the HTTP API of the media node in internal/agw.
// Each node that controls media declares the operations of its
// reference point, and takes either the HTTP client, *agw.Client, or a
// Controller:
//
//   - pcscf.Relay, for Iq: Add is Reserve AGW Connection Point (TS
//     29.334 5.17.2.2), Modify is Configure AGW Connection Point
//     (5.17.2.3), and Subtract is Release AGW Termination (5.17.2.5)
//     for both terminations of the context.
//   - mrfc.MRFP, for Mp: Add and AddTermination are Reserve IMS
//     Resources (TS 29.333 5.17.2.2), Modify is Configure IMS Resources
//     (5.17.2.3) with Start and Stop Announcement (5.17.2.9, 5.17.2.10),
//     and SubtractTermination is Release IMS Termination (5.17.2.5).
//   - ibcf.TrGW, for Ix (TS 29.238): Add, Modify and Subtract, as for
//     Iq. The Controller carries them with the profile of Iq; this
//     package has no profile of Ix.
//
// The nodes of this project run the HTTP client.
//
// Two types stand on either side of the H.248 messages:
//
//   - Controller, the controller side, the IMS-ALG in the P-CSCF or,
//     set up with MpProfile, the MRFC, sends the commands to a media
//     gateway, and accepts the registrations of media gateways.
//   - Gateway, the media gateway side, the IMS-AGW or the MRFP,
//     registers with a controller, carries out the commands it receives
//     by calling the Relay or the Processor it is given, and reports
//     events with Notify. Relay and Processor have the operations of
//     pcscf.Relay and mrfc.MRFP, so that *agw.Client can stand behind a
//     Gateway too.
//
// On Mp, the end of an announcement, Announcement Completed (TS 29.333
// 5.17.2.11), goes the other way: the processor tells its Gateway with
// Gateway.SignalCompleted, and the Controller hands the Notify to the
// function of ControllerConfig.Notify.
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
