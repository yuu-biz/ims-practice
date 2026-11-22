package fivegc

import (
	"context"

	"github.com/yuu-biz/ims-practice/internal/accessapi"
)

// Client is the UE's side of the API: it posts the UE's messages and
// reads the network's from the event stream. A UE simulator puts it
// behind its access network interface, as it does the EPC stub's
// client, so that a client that speaks real NAS can take its place.
type Client struct {
	link *accessapi.Client
}

// NewClient returns the client of the UE with imsi, for the stub at
// baseURL, such as http://5gc.ims.mnc001.mcc001.3gppnetwork.org:8080.
func NewClient(baseURL, imsi string) *Client {
	return &Client{link: accessapi.NewClient("fivegc", baseURL, imsi)}
}

// SystemInformation reads the identity of the NR cell the UE camps on.
func (c *Client) SystemInformation(ctx context.Context) (SystemInformation, error) {
	var si SystemInformation
	return si, c.link.Get(ctx, SystemInformationPath, &si)
}

// Register sends REGISTRATION REQUEST and returns the network's
// REGISTRATION ACCEPT. Open the event stream first, so that the network
// can reach the UE as soon as it is registered.
func (c *Client) Register(ctx context.Context, m RegistrationRequest) (RegistrationAccept, error) {
	var accept RegistrationAccept
	return accept, c.link.Post(ctx, TypeRegistrationRequest, m, &accept)
}

// Deregister sends DEREGISTRATION REQUEST and, unless the UE is
// switching off, waits for DEREGISTRATION ACCEPT.
func (c *Client) Deregister(ctx context.Context, switchOff bool) error {
	m := DeregistrationRequest{DeregistrationType: DeregistrationType{SwitchOff: switchOff, AccessType: Access3GPP}}
	if switchOff {
		return c.link.Post(ctx, TypeDeregistrationRequest, m, nil)
	}
	return c.link.Post(ctx, TypeDeregistrationRequest, m, &DeregistrationAccept{})
}

// AcceptDeregistration answers the network's DEREGISTRATION REQUEST.
func (c *Client) AcceptDeregistration(ctx context.Context) error {
	return c.link.Post(ctx, TypeDeregistrationAccept, DeregistrationAccept{}, nil)
}

// EstablishPDUSession sends PDU SESSION ESTABLISHMENT REQUEST in UL NAS
// TRANSPORT and returns the PDU SESSION ESTABLISHMENT ACCEPT.
func (c *Client) EstablishPDUSession(ctx context.Context, m ULNASTransport[PDUSessionEstablishmentRequest]) (DLNASTransport[PDUSessionEstablishmentAccept], error) {
	var accept DLNASTransport[PDUSessionEstablishmentAccept]
	return accept, c.link.Post(ctx, TypePDUSessionEstablishmentRequest, m, &accept)
}

// ReleasePDUSession sends PDU SESSION RELEASE REQUEST and returns the
// network's PDU SESSION RELEASE COMMAND, which the UE then completes.
func (c *Client) ReleasePDUSession(ctx context.Context, m ULNASTransport[PDUSessionReleaseRequest]) (DLNASTransport[PDUSessionReleaseCommand], error) {
	var command DLNASTransport[PDUSessionReleaseCommand]
	return command, c.link.Post(ctx, TypePDUSessionReleaseRequest, m, &command)
}

// CompletePDUSessionModification answers the network's PDU SESSION
// MODIFICATION COMMAND.
func (c *Client) CompletePDUSessionModification(ctx context.Context, m ULNASTransport[PDUSessionModificationComplete]) error {
	return c.link.Post(ctx, TypePDUSessionModificationComplete, m, nil)
}

func (c *Client) RejectPDUSessionModification(ctx context.Context, m ULNASTransport[PDUSessionModificationCommandReject]) error {
	return c.link.Post(ctx, TypePDUSessionModificationCommandReject, m, nil)
}

func (c *Client) CompletePDUSessionRelease(ctx context.Context, m ULNASTransport[PDUSessionReleaseComplete]) error {
	return c.link.Post(ctx, TypePDUSessionReleaseComplete, m, nil)
}

// NetworkMessage is one message from the network on the event stream.
// Message is a pointer to the struct of Type, such as
// *DLNASTransport[PDUSessionModificationCommand].
type NetworkMessage = accessapi.NetworkMessage

// networkMessages are the messages the network sends on the event
// stream, each with the struct it decodes into.
var networkMessages = map[MessageType]func() any{
	TypePDUSessionModificationCommand: func() any { return &DLNASTransport[PDUSessionModificationCommand]{} },
	TypePDUSessionReleaseCommand:      func() any { return &DLNASTransport[PDUSessionReleaseCommand]{} },
	TypeDeregistrationRequest:         func() any { return &DeregistrationRequest{} },
}

// Events opens the UE's event stream. The channel closes when ctx ends
// or the stream breaks.
func (c *Client) Events(ctx context.Context) (<-chan NetworkMessage, error) {
	return c.link.Events(ctx, networkMessages)
}

// APIError is an answer of the stub that is not a success. StatusCode
// tells the cases apart: 404 for an unknown IMSI or QoS flow, 409 for a
// UE that is not registered or has no PDU session, 400 for a message
// the stub refuses.
type APIError = accessapi.APIError
