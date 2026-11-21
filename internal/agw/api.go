package agw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// The control API, with the H.248 command each call stands for
// (H.248.1 (03/2013) 7):
//
//	POST   /contexts                               Add to a new context: {"terminations": [request, ...]}
//	POST   /contexts/{id}/terminations             Add to context id: request
//	PUT    /contexts/{id}/terminations/{tid}       Modify: request
//	DELETE /contexts/{id}/terminations/{tid}       Subtract one termination
//	DELETE /contexts/{id}                          Subtract every termination
//	GET    /contexts                               the contexts, as AuditValue would read them
//	GET    /announcements/{name}                   a provisioned announcement, as AuditCapabilities would read it
//	POST   <Events.Notify>                         Notify, from the node to the controller
//
// A request is a TerminationRequest as JSON, such as
// {"id": "access", "mode": "sendrecv", "remote": "192.0.2.10:4000"}, or
// for an MRFP {"mode": "sendonly", "remote": "192.0.2.20:5004",
// "signals": {"play": {"announcement": "notice"}}, "events":
// {"request_id": 7, "detect": ["signal_completed"], "notify":
// "http://mrfc:8081/notify"}}.
// Add to a new context and Subtract of a context answer with the
// Context, Add and Modify of a termination with the Termination, and
// Subtract of a termination with {"termination": ..., "context_deleted":
// true or false}. An error is {"error": code, "message": text}, with
// the codes of errorCodes.

// errorCodes name the errors of the API on the wire, so that a client
// gets back the error that the relay returned.
var errorCodes = map[string]error{
	"no_context":           ErrNoContext,
	"no_termination":       ErrNoTermination,
	"no_port":              ErrNoPort,
	"bad_request":          ErrBadRequest,
	"unknown_announcement": ErrUnknownAnnouncement,
}

// errorStatus is the HTTP status of each error code.
var errorStatus = map[string]int{
	"no_context":           http.StatusNotFound,
	"no_termination":       http.StatusNotFound,
	"no_port":              http.StatusServiceUnavailable,
	"bad_request":          http.StatusBadRequest,
	"unknown_announcement": http.StatusNotFound,
}

type addRequest struct {
	Terminations []TerminationRequest `json:"terminations"`
}

type subtracted struct {
	Termination    Termination `json:"termination"`
	ContextDeleted bool        `json:"context_deleted"`
}

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Handler serves the control API of r.
func Handler(r *Relay) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /contexts", func(w http.ResponseWriter, req *http.Request) {
		var body addRequest
		if !decode(w, req, &body) {
			return
		}
		c, err := r.Add(body.Terminations...)
		reply(w, c, err)
	})
	mux.HandleFunc("POST /contexts/{id}/terminations", func(w http.ResponseWriter, req *http.Request) {
		var body TerminationRequest
		if !decode(w, req, &body) {
			return
		}
		t, err := r.AddTermination(req.PathValue("id"), body)
		reply(w, t, err)
	})
	mux.HandleFunc("PUT /contexts/{id}/terminations/{tid}", func(w http.ResponseWriter, req *http.Request) {
		var body TerminationRequest
		if !decode(w, req, &body) {
			return
		}
		t, err := r.Modify(req.PathValue("id"), TerminationID(req.PathValue("tid")), body)
		reply(w, t, err)
	})
	mux.HandleFunc("DELETE /contexts/{id}/terminations/{tid}", func(w http.ResponseWriter, req *http.Request) {
		t, deleted, err := r.SubtractTermination(req.PathValue("id"), TerminationID(req.PathValue("tid")))
		reply(w, subtracted{Termination: t, ContextDeleted: deleted}, err)
	})
	mux.HandleFunc("DELETE /contexts/{id}", func(w http.ResponseWriter, req *http.Request) {
		c, err := r.Subtract(req.PathValue("id"))
		reply(w, c, err)
	})
	mux.HandleFunc("GET /contexts", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, r.Contexts(), nil)
	})
	mux.HandleFunc("GET /announcements/{name}", func(w http.ResponseWriter, req *http.Request) {
		name := req.PathValue("name")
		a, ok := r.Announcement(name)
		if !ok {
			reply(w, nil, fmt.Errorf("%w: %q", ErrUnknownAnnouncement, name))
			return
		}
		reply(w, a, nil)
	})
	return mux
}

func decode(w http.ResponseWriter, req *http.Request, v any) bool {
	if err := json.NewDecoder(req.Body).Decode(v); err != nil {
		reply(w, nil, fmt.Errorf("%w: %v", ErrBadRequest, err))
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err == nil {
		json.NewEncoder(w).Encode(v)
		return
	}
	code := "internal"
	for name, known := range errorCodes {
		if errors.Is(err, known) {
			code = name
		}
	}
	status, ok := errorStatus[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiError{Error: code, Message: err.Error()})
}

// Client drives a media node over the control API. The P-CSCF uses it
// as its IMS-AGW and the MRFC as its MRFP: it is the one place that
// talks to the media node, so that H.248 over Iq and Mp can stand in for
// it later.
type Client struct {
	base string // such as http://agw:8080
	http *http.Client
}

// RequestTimeout bounds each request to the relay, so that a relay that
// accepts the connection and then stalls cannot hold up a call for
// good. The relay answers from memory in far less than a millisecond;
// 2 seconds only run out when something is wrong.
const RequestTimeout = 2 * time.Second

func NewClient(base string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: RequestTimeout}}
}

// Add creates a context with terminations.
func (c *Client) Add(ctx context.Context, terminations ...TerminationRequest) (Context, error) {
	var out Context
	return out, c.do(ctx, http.MethodPost, "/contexts", addRequest{Terminations: terminations}, &out)
}

// AddTermination adds a termination to the context id.
func (c *Client) AddTermination(ctx context.Context, id string, t TerminationRequest) (Termination, error) {
	var out Termination
	return out, c.do(ctx, http.MethodPost, "/contexts/"+id+"/terminations", t, &out)
}

// Modify changes a termination.
func (c *Client) Modify(ctx context.Context, id string, tid TerminationID, change TerminationRequest) (Termination, error) {
	var out Termination
	return out, c.do(ctx, http.MethodPut, "/contexts/"+id+"/terminations/"+string(tid), change, &out)
}

// SubtractTermination removes a termination, and tells whether that
// deleted the context.
func (c *Client) SubtractTermination(ctx context.Context, id string, tid TerminationID) (Termination, bool, error) {
	var out subtracted
	err := c.do(ctx, http.MethodDelete, "/contexts/"+id+"/terminations/"+string(tid), nil, &out)
	return out.Termination, out.ContextDeleted, err
}

// Subtract removes a context.
func (c *Client) Subtract(ctx context.Context, id string) (Context, error) {
	var out Context
	return out, c.do(ctx, http.MethodDelete, "/contexts/"+id, nil, &out)
}

// Announcement tells whether the node has the announcement name.
func (c *Client) Announcement(ctx context.Context, name string) (Announcement, bool, error) {
	var out Announcement
	err := c.do(ctx, http.MethodGet, "/announcements/"+url.PathEscape(name), nil, &out)
	if errors.Is(err, ErrUnknownAnnouncement) {
		return Announcement{}, false, nil
	}
	return out, err == nil, err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, &body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agw: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e apiError
		var raw bytes.Buffer
		raw.ReadFrom(resp.Body)
		if json.Unmarshal(raw.Bytes(), &e) == nil {
			if known, ok := errorCodes[e.Error]; ok {
				return fmt.Errorf("%w: %s %s: %s", known, method, path, e.Message)
			}
		}
		return fmt.Errorf("agw: %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(raw.Bytes()))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
