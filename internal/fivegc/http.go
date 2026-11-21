package fivegc

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
// of TS 24.501: the network-requested PDU session modification that
// deletes the voice QoS flow (6.3.2), the network-requested PDU session
// release (6.3.3), and the network-initiated de-registration (5.5.2.3).
const (
	statusPath                        = "/ues/{imsi}"
	networkInitiatedQoSFlowRelease    = "/network-initiated/ues/{imsi}/pdu-sessions/{psi}/qos-flows/{qfi}"
	networkInitiatedPDUSessionRelease = "/network-initiated/ues/{imsi}/pdu-sessions/{psi}"
	networkInitiatedDeregistration    = "/network-initiated/ues/{imsi}/deregistration"
	maxRequestBodySize                = 64 << 10
)

// Handler serves the UE API, and the procedures that the network
// initiates:
//
//	GET    /system-information                       SystemInformation
//	GET    /ues/{imsi}/events                        the UE's event stream
//	POST   /ues/{imsi}/registration-request          RegistrationRequest -> RegistrationAccept
//	POST   /ues/{imsi}/deregistration-request        DeregistrationRequest -> DeregistrationAccept
//	POST   /ues/{imsi}/deregistration-accept         DeregistrationAccept
//	POST   /ues/{imsi}/pdu-session-establishment-request
//	       UL NAS TRANSPORT -> DL NAS TRANSPORT with PDU SESSION ESTABLISHMENT ACCEPT
//	POST   /ues/{imsi}/pdu-session-modification-complete        UL NAS TRANSPORT
//	POST   /ues/{imsi}/pdu-session-modification-command-reject  UL NAS TRANSPORT
//	POST   /ues/{imsi}/pdu-session-release-request
//	       UL NAS TRANSPORT -> DL NAS TRANSPORT with PDU SESSION RELEASE COMMAND
//	POST   /ues/{imsi}/pdu-session-release-complete  UL NAS TRANSPORT
//	GET    /ues/{imsi}                               Status
//	DELETE /network-initiated/ues/{imsi}/pdu-sessions/{psi}/qos-flows/{qfi}
//	       the network releases the QoS flow
//	DELETE /network-initiated/ues/{imsi}/pdu-sessions/{psi}
//	       the network releases the PDU session
//	POST   /network-initiated/ues/{imsi}/deregistration
//	       the network deregisters the UE
//
// A message the UE posts gets 200 with the network's answer, or 204 when
// the procedure has none. Errors are JSON {"error": "..."}.
func (g *FiveGC) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+SystemInformationPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, g.cfg.Cell)
	})
	mux.HandleFunc("GET "+EventsPath("{imsi}"), g.serveEvents)
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
	post(TypeRegistrationRequest, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m RegistrationRequest) (any, error) { return g.Register(ctx, imsi, m) })
	})
	post(TypeDeregistrationRequest, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m DeregistrationRequest) (any, error) {
			accept, err := g.Deregister(ctx, imsi, m)
			if accept == nil {
				return nil, err
			}
			return accept, err
		})
	})
	post(TypeDeregistrationAccept, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(DeregistrationAccept) (any, error) { return nil, g.DeregistrationAccepted(imsi) })
	})
	post(TypePDUSessionEstablishmentRequest, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m ULNASTransport[PDUSessionEstablishmentRequest]) (any, error) {
			return g.EstablishPDUSession(ctx, imsi, m)
		})
	})
	post(TypePDUSessionModificationComplete, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m ULNASTransport[PDUSessionModificationComplete]) (any, error) {
			return nil, g.ModificationCompleted(ctx, imsi, m)
		})
	})
	post(TypePDUSessionModificationCommandReject, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m ULNASTransport[PDUSessionModificationCommandReject]) (any, error) {
			return nil, g.ModificationRejected(ctx, imsi, m)
		})
	})
	post(TypePDUSessionReleaseRequest, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m ULNASTransport[PDUSessionReleaseRequest]) (any, error) {
			return g.ReleasePDUSession(ctx, imsi, m)
		})
	})
	post(TypePDUSessionReleaseComplete, func(ctx context.Context, imsi string, body []byte) (any, error) {
		return withMessage(body, func(m ULNASTransport[PDUSessionReleaseComplete]) (any, error) {
			return nil, g.PDUSessionReleaseCompleted(imsi, m)
		})
	})

	mux.HandleFunc("GET "+statusPath, func(w http.ResponseWriter, r *http.Request) {
		s, err := g.Status(r.PathValue("imsi"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	})
	mux.HandleFunc("DELETE "+networkInitiatedQoSFlowRelease, func(w http.ResponseWriter, r *http.Request) {
		psi, err1 := identifier(r, "psi", "PDU session ID")
		qfi, err2 := identifier(r, "qfi", "QFI")
		if err := errors.Join(err1, err2); err != nil {
			writeError(w, err)
			return
		}
		if err := g.ReleaseQoSFlow(r.Context(), r.PathValue("imsi"), psi, qfi); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE "+networkInitiatedPDUSessionRelease, func(w http.ResponseWriter, r *http.Request) {
		psi, err := identifier(r, "psi", "PDU session ID")
		if err != nil {
			writeError(w, err)
			return
		}
		if err := g.NetworkRequestedPDUSessionRelease(r.Context(), r.PathValue("imsi"), psi); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+networkInitiatedDeregistration, func(w http.ResponseWriter, r *http.Request) {
		if err := g.NetworkInitiatedDeregistration(r.Context(), r.PathValue("imsi")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// serveEvents streams the network's messages to a UE as Server-Sent
// Events: "event:" names the message, "data:" holds it as JSON.
func (g *FiveGC) serveEvents(w http.ResponseWriter, r *http.Request) {
	imsi := r.PathValue("imsi")
	if _, err := g.address(imsi); err != nil {
		writeError(w, err)
		return
	}
	st := g.streams.connect(imsi)
	defer g.streams.disconnect(imsi, st)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}
	g.log.Info("UE event stream open", "imsi", imsi, "remote_addr", r.RemoteAddr)
	defer g.log.Info("UE event stream closed", "imsi", imsi)
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

// identifier reads a path segment that is a one-octet identifier, such
// as a PDU session ID or a QFI.
func identifier(r *http.Request, name, what string) (uint8, error) {
	n, err := strconv.ParseUint(r.PathValue(name), 10, 8)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q", ErrInvalidMessage, what, r.PathValue(name))
	}
	return uint8(n), nil
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
	case errors.Is(err, ErrUnknownSubscriber), errors.Is(err, ErrUnknownQoSFlow):
		status = http.StatusNotFound
	case errors.Is(err, ErrNotRegistered), errors.Is(err, ErrNoPDUSession):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalidMessage):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
