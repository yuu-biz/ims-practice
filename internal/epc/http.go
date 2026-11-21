package epc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Paths with which a learner, or an e2e script, reads what the network
// holds of a UE, and makes the network start one of its own procedures
// of TS 24.301: the EPS bearer context deactivation that the network
// initiates (6.4.4.2), and the network initiated detach (5.5.2.3).
const (
	statusPath                   = "/ues/{imsi}"
	networkInitiatedDeactivation = "/network-initiated/ues/{imsi}/bearers/{ebi}"
	networkInitiatedDetach       = "/network-initiated/ues/{imsi}/detach"
	maxRequestBodySize           = 64 << 10
)

// Handler serves the UE API, and the procedures that the network
// initiates:
//
//	GET    /system-information                    SystemInformation
//	GET    /ues/{imsi}/events                     the UE's event stream
//	POST   /ues/{imsi}/attach-request             AttachRequest -> AttachAccept
//	POST   /ues/{imsi}/attach-complete            AttachComplete
//	POST   /ues/{imsi}/detach-request             DetachRequest -> DetachAccept
//	POST   /ues/{imsi}/detach-accept              DetachAccept
//	POST   /ues/{imsi}/activate-dedicated-eps-bearer-context-accept
//	POST   /ues/{imsi}/activate-dedicated-eps-bearer-context-reject
//	POST   /ues/{imsi}/deactivate-eps-bearer-context-accept
//	GET    /ues/{imsi}                                   Status
//	DELETE /network-initiated/ues/{imsi}/bearers/{ebi}   the network deactivates the bearer
//	POST   /network-initiated/ues/{imsi}/detach          the network detaches the UE
//
// A message the UE posts gets 200 with the network's answer, or 204 when
// the procedure has none. Errors are JSON {"error": "..."}.
func (e *EPC) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+SystemInformationPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, e.cfg.Cell)
	})
	mux.HandleFunc("GET "+EventsPath("{imsi}"), e.serveEvents)
	post := func(t MessageType, serve func(ctx context.Context, imsi string, body []byte) (any, error)) {
		mux.HandleFunc("POST "+Path("{imsi}", t), func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(w, r)
			if err != nil {
				writeError(w, err)
				return
			}
			answer, err := serve(r.Context(), r.PathValue("imsi"), body)
			switch {
			case err != nil:
				writeError(w, err)
			case answer == nil:
				w.WriteHeader(http.StatusNoContent)
			default:
				writeJSON(w, http.StatusOK, answer)
			}
		})
	}
	post(TypeAttachRequest, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m AttachRequest) (any, error) { return e.Attach(ctx, imsi, m) })
	})
	post(TypeAttachComplete, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m AttachComplete) (any, error) { return nil, e.AttachComplete(imsi, m) })
	})
	post(TypeDetachRequest, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m DetachRequest) (any, error) { return e.Detach(ctx, imsi, m) })
	})
	post(TypeDetachAccept, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(DetachAccept) (any, error) { return nil, e.DetachAccepted(imsi) })
	})
	post(TypeActivateDedicatedEPSBearerContextAccept, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m ActivateDedicatedEPSBearerContextAccept) (any, error) {
			return nil, e.DedicatedBearerAccepted(ctx, imsi, m)
		})
	})
	post(TypeActivateDedicatedEPSBearerContextReject, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m ActivateDedicatedEPSBearerContextReject) (any, error) {
			return nil, e.DedicatedBearerRejected(ctx, imsi, m)
		})
	})
	post(TypeDeactivateEPSBearerContextAccept, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m DeactivateEPSBearerContextAccept) (any, error) { return nil, e.BearerDeactivated(imsi, m) })
	})

	mux.HandleFunc("GET "+statusPath, func(w http.ResponseWriter, r *http.Request) {
		s, err := e.Status(r.PathValue("imsi"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	})
	mux.HandleFunc("DELETE "+networkInitiatedDeactivation, func(w http.ResponseWriter, r *http.Request) {
		ebi, err := strconv.ParseUint(r.PathValue("ebi"), 10, 8)
		if err != nil {
			writeError(w, fmt.Errorf("%w: EPS bearer identity %q", ErrInvalidMessage, r.PathValue("ebi")))
			return
		}
		if err := e.DeactivateEPSBearerContext(r.Context(), r.PathValue("imsi"), uint8(ebi)); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+networkInitiatedDetach, func(w http.ResponseWriter, r *http.Request) {
		if err := e.NetworkInitiatedDetach(r.Context(), r.PathValue("imsi")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// serveEvents streams the network's messages to a UE as Server-Sent
// Events: "event:" names the message, "data:" holds it as JSON.
func (e *EPC) serveEvents(w http.ResponseWriter, r *http.Request) {
	imsi := r.PathValue("imsi")
	if _, err := e.address(imsi); err != nil {
		writeError(w, err)
		return
	}
	st := e.streams.connect(imsi)
	defer e.streams.disconnect(imsi, st)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}
	e.log.Info("UE event stream open", "imsi", imsi, "remote_addr", r.RemoteAddr)
	defer e.log.Info("UE event stream closed", "imsi", imsi)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-st.closed:
			return
		case ev := <-st.events:
			// json.Marshal writes no newline, so the message fits on the one
			// data line of the event.
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

// withMessage decodes body as the message M and serves it. Unknown
// fields are errors, so a misspelt information element is caught.
func withMessage[M any](body []byte, serve func(M) (any, error)) (any, error) {
	var m M
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return serve(m)
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return body, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrUnknownSubscriber), errors.Is(err, ErrUnknownBearer):
		status = http.StatusNotFound
	case errors.Is(err, ErrNoPDNConnection):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalidMessage):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
