package fivegc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client is the UE's side of the API: it posts the UE's messages and
// reads the network's from the event stream. A UE simulator puts it
// behind its access network interface, as it does the EPC stub's
// client, so that a client that speaks real NAS can take its place.
type Client struct {
	base string // such as http://5gc.ims.mnc001.mcc001.3gppnetwork.org:8080
	imsi string
	http *http.Client
}

// NewClient returns the client of the UE with imsi, for the stub at
// baseURL.
func NewClient(baseURL, imsi string) *Client {
	return &Client{base: strings.TrimSuffix(baseURL, "/"), imsi: imsi, http: &http.Client{}}
}

// SystemInformation reads the identity of the NR cell the UE camps on.
func (c *Client) SystemInformation(ctx context.Context) (SystemInformation, error) {
	var si SystemInformation
	return si, c.do(ctx, http.MethodGet, SystemInformationPath, nil, &si)
}

// Register sends REGISTRATION REQUEST and returns the network's
// REGISTRATION ACCEPT. Open the event stream first, so that the network
// can reach the UE as soon as it is registered.
func (c *Client) Register(ctx context.Context, m RegistrationRequest) (RegistrationAccept, error) {
	var accept RegistrationAccept
	return accept, c.post(ctx, TypeRegistrationRequest, m, &accept)
}

// Deregister sends DEREGISTRATION REQUEST and, unless the UE is
// switching off, waits for DEREGISTRATION ACCEPT.
func (c *Client) Deregister(ctx context.Context, switchOff bool) error {
	m := DeregistrationRequest{DeregistrationType: DeregistrationType{SwitchOff: switchOff, AccessType: Access3GPP}}
	if switchOff {
		return c.post(ctx, TypeDeregistrationRequest, m, nil)
	}
	return c.post(ctx, TypeDeregistrationRequest, m, &DeregistrationAccept{})
}

// AcceptDeregistration answers the network's DEREGISTRATION REQUEST.
func (c *Client) AcceptDeregistration(ctx context.Context) error {
	return c.post(ctx, TypeDeregistrationAccept, DeregistrationAccept{}, nil)
}

// EstablishPDUSession sends PDU SESSION ESTABLISHMENT REQUEST in UL NAS
// TRANSPORT and returns the PDU SESSION ESTABLISHMENT ACCEPT.
func (c *Client) EstablishPDUSession(ctx context.Context, m ULNASTransport[PDUSessionEstablishmentRequest]) (DLNASTransport[PDUSessionEstablishmentAccept], error) {
	var accept DLNASTransport[PDUSessionEstablishmentAccept]
	return accept, c.post(ctx, TypePDUSessionEstablishmentRequest, m, &accept)
}

// ReleasePDUSession sends PDU SESSION RELEASE REQUEST and returns the
// network's PDU SESSION RELEASE COMMAND, which the UE then completes.
func (c *Client) ReleasePDUSession(ctx context.Context, m ULNASTransport[PDUSessionReleaseRequest]) (DLNASTransport[PDUSessionReleaseCommand], error) {
	var command DLNASTransport[PDUSessionReleaseCommand]
	return command, c.post(ctx, TypePDUSessionReleaseRequest, m, &command)
}

// CompletePDUSessionModification answers the network's PDU SESSION
// MODIFICATION COMMAND.
func (c *Client) CompletePDUSessionModification(ctx context.Context, m ULNASTransport[PDUSessionModificationComplete]) error {
	return c.post(ctx, TypePDUSessionModificationComplete, m, nil)
}

func (c *Client) RejectPDUSessionModification(ctx context.Context, m ULNASTransport[PDUSessionModificationCommandReject]) error {
	return c.post(ctx, TypePDUSessionModificationCommandReject, m, nil)
}

func (c *Client) CompletePDUSessionRelease(ctx context.Context, m ULNASTransport[PDUSessionReleaseComplete]) error {
	return c.post(ctx, TypePDUSessionReleaseComplete, m, nil)
}

// NetworkMessage is one message from the network on the event stream.
// Message is a pointer to the struct of Type, such as
// *DLNASTransport[PDUSessionModificationCommand].
type NetworkMessage struct {
	Type    MessageType
	Message any
}

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+EventsPath(c.imsi), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, responseError(resp)
	}
	out := make(chan NetworkMessage)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		var name string
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				name = v
				continue
			}
			data, ok := strings.CutPrefix(line, "data: ")
			newMessage, known := networkMessages[MessageType(name)]
			if !ok || !known {
				continue
			}
			m := newMessage()
			if json.Unmarshal([]byte(data), m) != nil {
				continue
			}
			select {
			case out <- NetworkMessage{Type: MessageType(name), Message: m}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (c *Client) post(ctx context.Context, t MessageType, message, answer any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, Path(c.imsi, t), body, answer)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, answer any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return responseError(resp)
	}
	if answer == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(answer)
}

// APIError is an answer of the stub that is not a success. StatusCode
// tells the cases apart: 404 for an unknown IMSI or QoS flow, 409 for a
// UE that is not registered or has no PDU session, 400 for a message
// the stub refuses.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return fmt.Sprintf("fivegc: %d: %s", e.StatusCode, e.Message) }

func responseError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if json.Unmarshal(b, &body) != nil || body.Error == "" {
		body.Error = strings.TrimSpace(string(b))
	}
	return &APIError{StatusCode: resp.StatusCode, Message: body.Error}
}
