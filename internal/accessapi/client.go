package accessapi

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
// reads the network's from the event stream. The client of a stub wraps
// it with the messages of that stub.
type Client struct {
	stub string // the name of the stub, which begins its error messages
	base string // such as http://epc.ims.mnc001.mcc001.3gppnetwork.org:8080
	imsi string
	http *http.Client
}

// NewClient returns the client of the UE with imsi, for the stub called
// stub at baseURL.
func NewClient(stub, baseURL, imsi string) *Client {
	return &Client{stub: stub, base: strings.TrimSuffix(baseURL, "/"), imsi: imsi, http: &http.Client{}}
}

// Get reads the JSON at path into answer.
func (c *Client) Get(ctx context.Context, path string, answer any) error {
	return c.do(ctx, http.MethodGet, path, nil, answer)
}

// Post sends message as the message of type t. The network's answer goes
// into answer, unless answer is nil because the procedure has none.
func (c *Client) Post(ctx context.Context, t MessageType, message, answer any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, Path(c.imsi, t), body, answer)
}

// NetworkMessage is one message from the network on the event stream.
// Message is a pointer to the struct of Type.
type NetworkMessage struct {
	Type    MessageType
	Message any
}

// Events opens the UE's event stream. messages gives, for each message
// the network sends, the struct it decodes into. The channel closes when
// ctx ends or the stream breaks.
func (c *Client) Events(ctx context.Context, messages map[MessageType]func() any) (<-chan NetworkMessage, error) {
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
		return nil, c.responseError(resp)
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
			newMessage, known := messages[MessageType(name)]
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
		return c.responseError(resp)
	}
	if answer == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(answer)
}

// APIError is an answer of the stub that is not a success. StatusCode is
// the status the stub gave for the error; each stub says which statuses
// it uses.
type APIError struct {
	Stub       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: %d: %s", e.Stub, e.StatusCode, e.Message) }

func (c *Client) responseError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if json.Unmarshal(b, &body) != nil || body.Error == "" {
		body.Error = strings.TrimSpace(string(b))
	}
	return &APIError{Stub: c.stub, StatusCode: resp.StatusCode, Message: body.Error}
}
