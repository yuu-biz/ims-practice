package fivegc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/accessapi"
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
	mux := accessapi.NewRouter(ErrInvalidMessage, status)
	mux.HandleFunc("GET "+SystemInformationPath, func(w http.ResponseWriter, r *http.Request) {
		accessapi.WriteJSON(w, http.StatusOK, g.cfg.Cell)
	})
	mux.HandleFunc("GET "+EventsPath("{imsi}"), func(w http.ResponseWriter, r *http.Request) {
		imsi := r.PathValue("imsi")
		if _, err := g.address(imsi); err != nil {
			mux.WriteError(w, err)
			return
		}
		g.streams.Serve(w, r, imsi, g.log)
	})
	accessapi.HandleMessage(mux, TypeRegistrationRequest, func(ctx context.Context, imsi string, m RegistrationRequest) (any, error) {
		return g.Register(ctx, imsi, m)
	})
	accessapi.HandleMessage(mux, TypeDeregistrationRequest, func(ctx context.Context, imsi string, m DeregistrationRequest) (any, error) {
		accept, err := g.Deregister(ctx, imsi, m)
		if accept == nil {
			return nil, err
		}
		return accept, err
	})
	accessapi.HandleMessage(mux, TypeDeregistrationAccept, func(ctx context.Context, imsi string, _ DeregistrationAccept) (any, error) {
		return nil, g.DeregistrationAccepted(imsi)
	})
	accessapi.HandleMessage(mux, TypePDUSessionEstablishmentRequest, func(ctx context.Context, imsi string, m ULNASTransport[PDUSessionEstablishmentRequest]) (any, error) {
		return g.EstablishPDUSession(ctx, imsi, m)
	})
	accessapi.HandleMessage(mux, TypePDUSessionModificationComplete, func(ctx context.Context, imsi string, m ULNASTransport[PDUSessionModificationComplete]) (any, error) {
		return nil, g.ModificationCompleted(ctx, imsi, m)
	})
	accessapi.HandleMessage(mux, TypePDUSessionModificationCommandReject, func(ctx context.Context, imsi string, m ULNASTransport[PDUSessionModificationCommandReject]) (any, error) {
		return nil, g.ModificationRejected(ctx, imsi, m)
	})
	accessapi.HandleMessage(mux, TypePDUSessionReleaseRequest, func(ctx context.Context, imsi string, m ULNASTransport[PDUSessionReleaseRequest]) (any, error) {
		return g.ReleasePDUSession(ctx, imsi, m)
	})
	accessapi.HandleMessage(mux, TypePDUSessionReleaseComplete, func(ctx context.Context, imsi string, m ULNASTransport[PDUSessionReleaseComplete]) (any, error) {
		return nil, g.PDUSessionReleaseCompleted(imsi, m)
	})

	mux.HandleFunc("GET "+statusPath, func(w http.ResponseWriter, r *http.Request) {
		s, err := g.Status(r.PathValue("imsi"))
		if err != nil {
			mux.WriteError(w, err)
			return
		}
		accessapi.WriteJSON(w, http.StatusOK, s)
	})
	mux.HandleFunc("DELETE "+networkInitiatedQoSFlowRelease, func(w http.ResponseWriter, r *http.Request) {
		psi, err1 := identifier(r, "psi", "PDU session ID")
		qfi, err2 := identifier(r, "qfi", "QFI")
		if err := errors.Join(err1, err2); err != nil {
			mux.WriteError(w, err)
			return
		}
		if err := g.ReleaseQoSFlow(r.Context(), r.PathValue("imsi"), psi, qfi); err != nil {
			mux.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE "+networkInitiatedPDUSessionRelease, func(w http.ResponseWriter, r *http.Request) {
		psi, err := identifier(r, "psi", "PDU session ID")
		if err != nil {
			mux.WriteError(w, err)
			return
		}
		if err := g.NetworkRequestedPDUSessionRelease(r.Context(), r.PathValue("imsi"), psi); err != nil {
			mux.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+networkInitiatedDeregistration, func(w http.ResponseWriter, r *http.Request) {
		if err := g.NetworkInitiatedDeregistration(r.Context(), r.PathValue("imsi")); err != nil {
			mux.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
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

// status is the HTTP status of the stub's own errors; 0 is for the ones
// it does not know.
func status(err error) int {
	switch {
	case errors.Is(err, ErrUnknownSubscriber), errors.Is(err, ErrUnknownQoSFlow):
		return http.StatusNotFound
	case errors.Is(err, ErrNotRegistered), errors.Is(err, ErrNoPDUSession):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidMessage):
		return http.StatusBadRequest
	}
	return 0
}
