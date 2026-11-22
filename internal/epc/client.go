package epc

import (
	"context"

	"github.com/yuu-biz/ims-practice/internal/accessapi"
)

// Client is the UE's side of the API: it posts the UE's messages and
// reads the network's from the event stream. A UE simulator puts it
// behind its access network interface (ue.AccessNetwork).
type Client struct {
	link *accessapi.Client
}

// NewClient returns the client of the UE with imsi, for the stub at
// baseURL, such as http://epc.ims.mnc001.mcc001.3gppnetwork.org:8080.
func NewClient(baseURL, imsi string) *Client {
	return &Client{link: accessapi.NewClient("epc", baseURL, imsi)}
}

// SystemInformation reads the identity of the cell the UE camps on.
func (c *Client) SystemInformation(ctx context.Context) (SystemInformation, error) {
	var si SystemInformation
	return si, c.link.Get(ctx, SystemInformationPath, &si)
}

// Attach sends ATTACH REQUEST and returns the network's ATTACH ACCEPT.
// Open the event stream first, so that the network can reach the UE as
// soon as it is attached.
func (c *Client) Attach(ctx context.Context, m AttachRequest) (AttachAccept, error) {
	var accept AttachAccept
	return accept, c.link.Post(ctx, TypeAttachRequest, m, &accept)
}

func (c *Client) AttachComplete(ctx context.Context, m AttachComplete) error {
	return c.link.Post(ctx, TypeAttachComplete, m, nil)
}

// Detach sends DETACH REQUEST and waits for DETACH ACCEPT.
func (c *Client) Detach(ctx context.Context) error {
	return c.link.Post(ctx, TypeDetachRequest, DetachRequest{DetachType: EPSDetach}, &DetachAccept{})
}

// AcceptDetach answers the network's DETACH REQUEST.
func (c *Client) AcceptDetach(ctx context.Context) error {
	return c.link.Post(ctx, TypeDetachAccept, DetachAccept{}, nil)
}

func (c *Client) AcceptDedicatedBearer(ctx context.Context, m ActivateDedicatedEPSBearerContextAccept) error {
	return c.link.Post(ctx, TypeActivateDedicatedEPSBearerContextAccept, m, nil)
}

func (c *Client) RejectDedicatedBearer(ctx context.Context, m ActivateDedicatedEPSBearerContextReject) error {
	return c.link.Post(ctx, TypeActivateDedicatedEPSBearerContextReject, m, nil)
}

func (c *Client) AcceptDeactivation(ctx context.Context, m DeactivateEPSBearerContextAccept) error {
	return c.link.Post(ctx, TypeDeactivateEPSBearerContextAccept, m, nil)
}

// NetworkMessage is one message from the network on the event stream.
// Message is a pointer to the struct of Type, such as
// *ActivateDedicatedEPSBearerContextRequest.
type NetworkMessage = accessapi.NetworkMessage

// networkMessages are the messages the network sends on the event
// stream, each with the struct it decodes into.
var networkMessages = map[MessageType]func() any{
	TypeActivateDedicatedEPSBearerContextRequest: func() any { return &ActivateDedicatedEPSBearerContextRequest{} },
	TypeDeactivateEPSBearerContextRequest:        func() any { return &DeactivateEPSBearerContextRequest{} },
	TypeDetachRequest:                            func() any { return &DetachRequest{} },
}

// Events opens the UE's event stream. The channel closes when ctx ends
// or the stream breaks.
func (c *Client) Events(ctx context.Context) (<-chan NetworkMessage, error) {
	return c.link.Events(ctx, networkMessages)
}

// APIError is an answer of the stub that is not a success. StatusCode
// tells the cases apart: 404 for an unknown IMSI or bearer, 409 for a UE
// that is not attached, 400 for a message the stub refuses.
type APIError = accessapi.APIError
