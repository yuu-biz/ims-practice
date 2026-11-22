// Package accessapi is the part of the access network stubs that faces
// the UE. The EPC stub (internal/epc) and the 5GC stub (internal/fivegc)
// both talk to a UE simulator in the same way, so both use it.
//
// The UE posts each NAS message as JSON to /ues/{imsi}/{message}, and
// the network's own messages reach it on a Server-Sent Events stream at
// /ues/{imsi}/events. The stream stands for the UE's NAS signalling
// connection, which is carried over S1-MME or N2, so it lives in the
// instance the UE connected to.
//
// This package holds the transport: the paths, the event stream, the
// JSON handling of the stub's HTTP side and the client of the UE's side.
// What the messages say (EPS bearers or QoS flows, PDN connections or
// PDU sessions) stays in each stub.
//
// There is no NAS encoding, S1-AP or NGAP: the IMSI in the URL stands
// for the UE-associated connection that tells the network which UE a
// message is from. Each stub's api.go records what it leaves out of the
// procedures.
package accessapi
