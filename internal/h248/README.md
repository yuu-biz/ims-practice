# H.248 (Megaco)

[日本語](README.ja.md)

This package reads and writes H.248 messages in the text encoding of ITU-T H.248.1 Annex B, and carries their transactions over UDP with the procedures of Annex D.1. Package `mediactl` holds both sides of two 3GPP profiles of H.248: Iq between the IMS-ALG and the IMS-AGW (TS 29.334), and Mp between the MRFC and the MRFP (TS 29.333).

No node of this project uses these packages. The P-CSCF, the IBCF and the MRFC drive the media relay over its HTTP control API (`internal/agw`), not over H.248. The tests and examples here run a controller and a gateway against each other over UDP, so that the H.248 messages can be read and captured:

```sh
go test ./internal/h248/...
go doc ./internal/h248
go doc ./internal/h248/mediactl
```

The package comments (`doc.go`, and `mediactl/doc.go`) explain the types and which clauses they follow.

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places. H.248.1 here is the version of May 2002 with its Corrigendum 1, which the three profiles reference.

| Where | What is left out | Why |
| --- | --- | --- |
| `IndAuditParameter`, H.248.1 Annex B.2 (indAudterminationAudit) | Auditing the items of the LocalControl, Events, Signals, Statistics and Packages descriptors one by one. Only the items of the TerminationState descriptor can be audited one by one. | Iq and Ix audit none of them, and Mp audits one optional property of LocalControl (TS 29.333 5.8.5). Whole descriptors can be audited with `AuditToken`. |
| `Endpoint`, TS 29.334 5.12, TS 29.238 5.12, TS 29.333 5.12 | SCTP. The Endpoint carries H.248 over UDP only, so the parts of H.248.4 that replace parts of Annex D.1 over SCTP are not there either. | The Go standard library has no SCTP. All three profiles allow UDP as an option. |
| `Timers`, H.248.1 Annex E.2.1 | Changing four of the timers with a Modify of the ROOT termination. The Endpoint keeps the values it was made with. | A Modify that changes them would need a new Endpoint, made by whoever handles that Modify. |
| `Controller`, TS 29.334 5.17.2.6 | Releasing a context that the P-CSCF has lost track of, when `TerminationHeartbeat` is nil. Every context then counts as in use and stays in the gateway. | Only the caller of the controller knows which contexts are still in use. |
| `Controller`, TS 29.334 5.17.3.19 | Clearing the contexts when a termination goes out of service with ContextID ALL or a wildcarded termination, or with a wildcard on Mp. The controller clears a context that the report names, and nothing for these. | The controller keeps no list of the contexts and terminations of a gateway. |
| `Controller`, H.248.1 7.2.8 | Auditing the gateway after a Disconnected ServiceChange. | The clause allows the audit and does not require it. |
| `Controller`, TS 29.334 5.17.2.7 | Handing a released bearer to the caller of the controller. The controller answers the Notify as the clause asks, and logs the event only. | Add, Modify and Subtract of the controller have no way to tell their caller, which owns the session. |
| Local and Remote descriptors, H.248.1 7.1.8 | Alternatives for the gateway to choose from, as several session descriptions in one descriptor or several values in one field. They are refused. | The controllers of this project offer one. |
| `Gateway`, H.248.1 11.2, 11.5 | Trying an ordered list of secondary controllers when the controller does not answer. The gateway is provisioned with one controller, and starts again from it after a failure. It does follow the ServiceChangeMgcId of a reply. | 11.2 makes the list of secondary controllers optional. |
| `Gateway`, TS 29.334 5.17.2.7 | Reporting a released bearer with g/cause. The event is accepted and never reported. | The relay keeps the ports of a context until Subtract, so no bearer is released before it. |
| `Gateway`, H.248.37 6.6 | Latching only when the controller sends ipnapt/latch, and the modes OFF and RELATCH, which TS 29.334 asks a gateway to support (Table 5.14.3.12.1). OFF and RELATCH are refused, and the relay latches every termination on the first packet it receives, whether the signal is sent or not. | `Relay` has no way to set the mode of a termination. |
| `Gateway`, TS 29.334 5.8.5 | Reporting the properties of ROOT (root/*) in an audit. | Their values are the timers of the Endpoint, which are provisioned (TS 29.334 5.14.3.2). |
| `Gateway`, TS 29.334 5.17.3.7 | IMS-ALG Ordered Re-register, a Handoff that sends the gateway to another controller. It is refused with error 501. | The controllers of this project do not send it. |
| `Gateway`, TS 29.334 5.5, 5.17.2.2 | Treating a context marked as an emergency call or given a priority differently. The attributes are accepted and have no effect. | The relay treats every context alike. |
| `Gateway`, TS 29.334 5.17.2.5, 5.17.3.10 | ContextID ALL in a Subtract or an AuditValue. It is refused with error 501. | It would walk every context of the gateway. The controllers of this project name one context at a time. |
| `Gateway`, H.248.1 | Adding the terminations of a context one at a time. Both Adds must come in the action that creates the context, and a later Add is refused with error 434. | The relay makes a context with both of its terminations at once. |
| `Gateway`, TS 29.334 5.17.3.10 | Auditing the terminations of a context. | The controllers of this project audit ROOT only. |
| `Gateway`, TS 29.334 5.17.2.3 | Changing the local resources of a termination with a Local descriptor in Modify. | The relay keeps the ports it chose for the whole call. |
| `Gateway`, TS 29.334 5.17.2.5 | Subtracting one termination at a time. The gateway asks for the wildcard `*`, which subtracts both. | The relay releases a context as a whole. |
| `Gateway` on Mp, TS 29.333 5.7.8 | The topologies isolate, oneway and bothway. They are refused with error 522. | The media node sends each termination the media of all the others, the default topology of H.248.1 7.1.18, and has no other. |
| `Gateway` on Mp, TS 29.333 5.5 | Treating a context with a priority for MPS differently. The attribute is accepted and has no effect. | The media node treats every context alike. |
| `Gateway` on Mp, TS 29.333 5.17.3.8 | ContextID ALL in an AuditValue. It is refused with error 501. | It would walk every context of the gateway. |
| `Gateway` on Mp, TS 29.333 5.8.4, 5.8.5 | Move, and AuditValue of the signals, events and statistics of a termination. | The MRFC of this project uses neither. |
| `Gateway` on Mp, H.248.1 7.2.3 | A Subtract that names its terminations with a wildcard. | Release IMS Termination names one termination, and so does the MRFC of this project. |
| `Gateway` on Mp, TS 29.333 5.17.2.3 | Changing the local resources of a termination with a Local descriptor in Modify. | The media node keeps the ports it chose until the termination is subtracted. |
| `Gateway` on Mp, TS 29.333 5.17.2.9, 5.7.4 | Signal lists, and KeepActive on signals. Both are refused. | The media node plays one announcement at a time and starts it anew with each Signals descriptor. |
| `Gateway` on Mp, H.248.7 4.5 | The number of cycles and the duration provisioned for each announcement. The media node plays once, with no limit; the variant (av) is refused, and the direction must be external. | The media node has no provisioning of announcements and plays to the remote party of the termination only. |
| `Gateway` on Mp, H.248.1 7.1.11, E.1.2 | Reporting a signal that a new Signals descriptor halted (SD), or that stopped for another reason (NC). Only TO, and EV for an announcement that the heartbeat stopped, are reported. | The MRFC of this project stops an announcement only by subtracting its termination. |
| `MpProfile`, H.248.9 6.2.5.1.2 | Announcements made of a list of segments, of variables, or of segments on other hosts. They are refused. | The MRFP of this project plays one provisioned announcement at a time. |
| `Endpoint`, H.248.1 Annex D.1.3 | Estimating the retransmission timer from the time replies take, which the annex says should normally be done; a note gives the AAD and ADEV of TCP as one way. Every request starts from `InitialRetransmissionTimer`, a provisioned value, and ADEV is taken as zero. | Measuring needs state for each peer that outlives its transactions, and the Endpoint keeps state for a transaction only. |
| `Endpoint`, H.248.1 Annex D.1.2.2 | Confirming the other replies in any message, such as the next request to the same peer. Only replies after a Pending or with ImmAckRequired are confirmed. | The peer keeps the other replies until LONG-TIMER. That costs it memory but changes no procedure. |
| Parser, H.248.1 Annex B.2 (parmValue, alternativeValue) | The alternatives `{a, b}`, the range `[a:b]` and the relations `<`, `>` and `#`. | They let a controller offer several values to choose from, and the three profiles hand over choices in the SDP of the Local descriptor instead (TS 29.334 5.17.2.2). |
