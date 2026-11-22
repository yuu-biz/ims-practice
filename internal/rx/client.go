package rx

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/policy"
)

// PCRF is how the AF reaches the PCRF. *diameter.Peer is one.
type PCRF interface {
	Request(ctx context.Context, request *diam.Message) (*diam.Message, error)
}

// Client is policy control over Rx: the AF side of TS 29.214 for the
// P-CSCF. It keeps no state of its own; the Session-Id is the
// policy.SessionID the P-CSCF keeps.
//
// SIMPLIFIED: TS 29.213 7.3.7. The P-CSCF sends to the one PCRF of its
// configuration; there is no DRA to pick the PCRF that serves the UE.
// The PCRF is that of the EPC stub, which runs as one node and so holds
// the IP-CAN session of every UE.
type Client struct {
	node   *diameter.Node
	pcrf   PCRF
	realm  string
	notify policy.Notify
	log    *slog.Logger
}

var _ policy.PolicyControl = (*Client)(nil)

// NewClient returns a client that sends from node to pcrf. realm is the
// Destination-Realm, the realm of the PCRF. notify gets the events of
// the PCRF's RARs and ASRs; give the node Handle as the handler of its
// Rx requests.
func NewClient(node *diameter.Node, pcrf PCRF, realm string, notify policy.Notify, log *slog.Logger) *Client {
	return &Client{node: node, pcrf: pcrf, realm: realm, notify: notify, log: log}
}

// ResultError is an answer from the PCRF that did not succeed, other
// than those package policy has errors for.
type ResultError struct {
	Command string // "AAA" or "STA"
	Result  Result
}

func (e *ResultError) Error() string { return fmt.Sprintf("rx: %s with %v", e.Command, e.Result) }

// eventActions are the Specific-Action values of the events a P-CSCF
// can subscribe to (TS 29.214 5.3.13).
var eventActions = map[policy.EventKind]SpecificAction{
	policy.LossOfBearer:                  IndicationOfLossOfBearer,
	policy.ReleaseOfBearer:               IndicationOfReleaseOfBearer,
	policy.SuccessfulResourcesAllocation: IndicationOfSuccessfulResourcesAllocation,
	policy.FailedResourcesAllocation:     IndicationOfFailedResourcesAllocation,
}

// SubscribeSignallingPath opens the Rx session of TS 29.214 4.4.5: media
// component 0, flow 0 for the AF signalling, and the loss and release
// of its bearer.
func (c *Client) SubscribeSignallingPath(ctx context.Context, ue netip.Addr) (policy.SessionID, error) {
	initial := InitialRequest
	return c.authorize(ctx, AARequest{
		Header:          cx.NewHeader(c.node, "", c.realm),
		MediaComponents: []policy.MediaComponent{policy.SignallingComponent()},
		SpecificActions: []SpecificAction{IndicationOfLossOfBearer, IndicationOfReleaseOfBearer},
		FramedIPAddress: ue,
		RxRequestType:   &initial,
	})
}

// AuthorizeMedia sends the service information that TS 29.213 6.2
// derives from the SDP: FINAL once there is an answer, PRELIMINARY for
// an offer alone (TS 29.214 5.3.25).
func (c *Client) AuthorizeMedia(ctx context.Context, r policy.MediaRequest) (policy.SessionID, error) {
	components, err := policy.MediaComponents(r.SDP)
	if err != nil {
		return "", err
	}
	header := cx.NewHeader(c.node, "", c.realm)
	requestType := InitialRequest
	if r.Session != "" {
		header.SessionID = string(r.Session)
		requestType = UpdateRequest
	}
	status := FinalServiceInformation
	if r.SDP.Answer == nil {
		status = PreliminaryServiceInformation
	}
	aar := AARequest{
		Header:                  header,
		AFApplicationIdentifier: r.ApplicationID,
		MediaComponents:         components,
		ServiceInfoStatus:       &status,
		FramedIPAddress:         r.UE,
		RxRequestType:           &requestType,
	}
	for _, k := range r.Subscribe {
		if a, ok := eventActions[k]; ok {
			aar.SpecificActions = append(aar.SpecificActions, a)
		}
	}
	return c.authorize(ctx, aar)
}

func (c *Client) authorize(ctx context.Context, aar AARequest) (policy.SessionID, error) {
	answer, err := c.pcrf.Request(ctx, aar.Message())
	if err != nil {
		return "", err
	}
	aaa, err := ParseAAAnswer(answer)
	if err != nil {
		return "", err
	}
	switch {
	case aaa.Result.Success():
		return policy.SessionID(aar.SessionID), nil
	case aaa.Result == cx.Experimental(IPCANSessionNotAvailable):
		return "", policy.ErrIPCANSessionNotAvailable
	case aaa.Result == cx.Experimental(RequestedServiceNotAuthorized):
		return "", policy.ErrRequestedServiceNotAuthorized
	}
	return "", &ResultError{Command: "AAA", Result: aaa.Result}
}

// Release ends the Rx session with an STR (TS 29.214 4.4.4). A session
// the PCRF no longer knows is released already.
func (c *Client) Release(ctx context.Context, s policy.SessionID) error {
	return c.terminate(ctx, s, DiameterLogout)
}

func (c *Client) terminate(ctx context.Context, s policy.SessionID, cause TerminationCause) error {
	header := cx.NewHeader(c.node, "", c.realm)
	header.SessionID = string(s)
	answer, err := c.pcrf.Request(ctx, SessionTerminationRequest{Header: header, TerminationCause: cause}.Message())
	if err != nil {
		return err
	}
	sta, err := ParseSessionTerminationAnswer(answer)
	if err != nil {
		return err
	}
	if !sta.Result.Success() && sta.Result != cx.Base(DiameterUnknownSessionID) {
		return &ResultError{Command: "STA", Result: sta.Result}
	}
	return nil
}

// Handle answers the RARs and ASRs of the PCRF; it is the node's
// handler for Rx requests. The P-CSCF hears of them only after the
// answer has gone. After an ASA the client ends the session itself with
// an STR, as TS 29.214 4.4.6.1 has the AF do, so a SessionTerminated
// event names a session that is over.
func (c *Client) Handle(request *diam.Message) *diam.Message {
	local := c.node.Identity()
	switch request.Header.CommandCode {
	case CommandReAuth:
		rar, err := ParseReAuthRequest(request)
		if err != nil {
			c.log.Warn("unreadable RAR", "error", err.Error())
			return ReAuthAnswer{Result: cx.Base(DiameterUnableToComply)}.Message(request, local)
		}
		session := policy.SessionID(rar.SessionID)
		c.node.AfterAnswer(request, func() {
			for _, a := range rar.SpecificActions {
				if k, ok := lookupEvent(a); ok {
					c.notify(policy.Event{Session: session, Kind: k})
				}
			}
		})
		return ReAuthAnswer{Result: cx.Base(DiameterSuccess)}.Message(request, local)
	case CommandAbortSession:
		asr, err := ParseAbortSessionRequest(request)
		if err != nil {
			c.log.Warn("unreadable ASR", "error", err.Error())
			return AbortSessionAnswer{Result: cx.Base(DiameterUnableToComply)}.Message(request, local)
		}
		session := policy.SessionID(asr.SessionID)
		c.node.AfterAnswer(request, func() {
			if err := c.terminate(context.Background(), session, DiameterAdministrative); err != nil {
				c.log.Warn("STR after the ASR failed", "session_id", asr.SessionID, "error", err.Error())
			}
			c.notify(policy.Event{Session: session, Kind: policy.SessionTerminated})
		})
		return AbortSessionAnswer{Result: cx.Base(DiameterSuccess)}.Message(request, local)
	}
	return diameter.CommandUnsupportedAnswer(local, request)
}

func lookupEvent(a SpecificAction) (policy.EventKind, bool) {
	for k, v := range eventActions {
		if v == a {
			return k, true
		}
	}
	return 0, false
}
