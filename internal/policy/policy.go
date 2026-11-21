// Package policy is the P-CSCF's boundary to policy and charging
// control. The P-CSCF asks the network to watch the signalling
// path of a registered UE, to authorise the media of a session from its
// SDP offer and answer, and to release what it asked for; the network
// tells it when a session loses its resources.
//
// The types do not depend on the protocol that carries them. Over EPC
// the P-CSCF is an AF talking Rx to a PCRF (TS 29.214, package rx);
// over 5GC the AF talks N5 to a PCF, whose service information has the
// same media components and flows.
package policy

import (
	"context"
	"errors"
	"net/netip"
)

// PolicyControl is what the P-CSCF needs from the PCRF.
type PolicyControl interface {
	// SubscribeSignallingPath asks to be told when the UE at ue loses the
	// bearer that carries its SIP signalling (TS 29.214 4.4.5). The
	// P-CSCF calls it once a UE has registered (TS 29.214 A.4) and
	// releases the session when the UE deregisters.
	SubscribeSignallingPath(ctx context.Context, ue netip.Addr) (SessionID, error)
	// AuthorizeMedia provides the service information of an IMS session
	// so that the network can reserve resources for its media: a new
	// policy session when r.Session is empty (TS 29.214 4.4.1), or an
	// update of r.Session (4.4.2).
	AuthorizeMedia(ctx context.Context, r MediaRequest) (SessionID, error)
	// Release ends a policy session (TS 29.214 4.4.4). Releasing a
	// session the network has already ended is not an error, so a
	// P-CSCF may release on every BYE.
	Release(ctx context.Context, s SessionID) error
}

// SessionID names one policy session: today the Session-Id of an Rx
// Diameter session. It is plain text, so the P-CSCF can keep it in its
// Store and any P-CSCF instance can release it.
type SessionID string

// Errors of SubscribeSignallingPath and AuthorizeMedia that a caller
// acts on. Other failures, such as a PCRF that cannot be reached, come
// back as other errors.
var (
	// ErrIPCANSessionNotAvailable: the network knows no IP-CAN session
	// for the UE's address (TS 29.214 5.5.1, IP-CAN_SESSION_NOT_AVAILABLE).
	// A UE that does not come through the EPC, such as a softphone on
	// the host, gets this; the P-CSCF then serves it without policy
	// control.
	ErrIPCANSessionNotAvailable = errors.New("policy: no IP-CAN session for the UE")
	// ErrRequestedServiceNotAuthorized: the network refuses the media
	// (TS 29.214 5.5.1, REQUESTED_SERVICE_NOT_AUTHORIZED).
	ErrRequestedServiceNotAuthorized = errors.New("policy: requested service not authorized")
)

// MediaRequest is the service information of one IMS session.
type MediaRequest struct {
	// Session is empty for a new session, or the session to update.
	Session SessionID
	// UE is the IP address of the UE, as the P-CSCF sees it in the
	// signalling. The network finds the UE's IP-CAN session by it
	// (session binding, TS 29.213 5.2).
	UE netip.Addr
	// SDP is the offer and answer the media components are derived from.
	SDP OfferAnswer
	// ApplicationID is the IMS communication service identifier (ICSI)
	// of the session, such as urn:urn-7:3gpp-service.ims.icsi.mmtel (TS
	// 29.214 A.1 NOTE 3). Empty leaves it out.
	ApplicationID string
	// Subscribe lists the events the P-CSCF wants to hear about for this
	// session. SessionTerminated needs no subscription.
	Subscribe []EventKind
}

// OfferAnswer is an SDP offer and its answer as the UE sees them: the
// SDP the UE sent, and the SDP the P-CSCF sent to the UE. With a media
// relay in the path the SDP to the UE is the one after the P-CSCF wrote
// the relay's address into it, because the bearer carries the UE's
// traffic to and from that address.
type OfferAnswer struct {
	Offer []byte
	// Answer is nil while only the offer is known. The service
	// information is then preliminary (TS 29.214 5.3.25).
	Answer []byte
	// UEOffered is true when the UE sent the offer and false when the UE
	// received it. TS 29.213 6.2 calls the SDP from the UE "uplink SDP"
	// and the SDP to the UE "downlink SDP".
	UEOffered bool
}

// Uplink returns the SDP the UE sent, or nil when it is not known yet.
func (oa OfferAnswer) Uplink() []byte {
	if oa.UEOffered {
		return oa.Offer
	}
	return oa.Answer
}

// Downlink returns the SDP the UE received, or nil when it is not known
// yet.
func (oa OfferAnswer) Downlink() []byte {
	if oa.UEOffered {
		return oa.Answer
	}
	return oa.Offer
}

// EventKind is what the network reports about a policy session. Except
// for SessionTerminated, the P-CSCF only hears what it subscribed to in
// MediaRequest.Subscribe (TS 29.214 4.4.6.2).
type EventKind int

const (
	// SessionTerminated: the network has ended the session, for example
	// because the UE's IP-CAN session ended or the bearer of all its
	// media was released (TS 29.214 4.4.6.1, 4.4.6.2). The session is
	// already released; the P-CSCF ends the IMS session.
	SessionTerminated EventKind = iota + 1
	// LossOfBearer: a bearer of the session lost its resources for a
	// while (TS 29.214 5.3.13, INDICATION_OF_LOSS_OF_BEARER).
	LossOfBearer
	// ReleaseOfBearer: a bearer of the session was released (TS 29.214
	// 5.3.13, INDICATION_OF_RELEASE_OF_BEARER).
	ReleaseOfBearer
	// SuccessfulResourcesAllocation: the network has set up the
	// resources for the media (TS 29.214 5.3.13,
	// INDICATION_OF_SUCCESSFUL_RESOURCES_ALLOCATION).
	SuccessfulResourcesAllocation
	// FailedResourcesAllocation: the network could not set up the
	// resources for the media (TS 29.214 4.4.1,
	// INDICATION_OF_FAILED_RESOURCES_ALLOCATION).
	FailedResourcesAllocation
)

func (k EventKind) String() string {
	switch k {
	case SessionTerminated:
		return "session terminated"
	case LossOfBearer:
		return "loss of bearer"
	case ReleaseOfBearer:
		return "release of bearer"
	case SuccessfulResourcesAllocation:
		return "successful resources allocation"
	case FailedResourcesAllocation:
		return "failed resources allocation"
	}
	return "unknown event"
}

// Event is one report from the network about a policy session.
type Event struct {
	Session SessionID
	Kind    EventKind
}

// Notify receives the events of the sessions a PolicyControl opened. It
// is called on a goroutine of its own, after the network has been
// answered.
type Notify func(Event)
