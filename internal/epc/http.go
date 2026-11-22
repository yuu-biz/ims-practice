package epc

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
// of TS 24.301: the EPS bearer context deactivation that the network
// initiates (6.4.4.2), and the network initiated detach (5.5.2.3).
const (
	attachedPath                 = "/ues"
	statusPath                   = "/ues/{imsi}"
	networkInitiatedDeactivation = "/network-initiated/ues/{imsi}/bearers/{ebi}"
	networkInitiatedDetach       = "/network-initiated/ues/{imsi}/detach"
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
//	GET    /ues                                          []Status, the UEs that are attached
//	GET    /ues/{imsi}                                   Status
//	DELETE /network-initiated/ues/{imsi}/bearers/{ebi}   the network deactivates the bearer
//	POST   /network-initiated/ues/{imsi}/detach          the network detaches the UE
//
// A message the UE posts gets 200 with the network's answer, or 204 when
// the procedure has none. Errors are JSON {"error": "..."}.
func (e *EPC) Handler() http.Handler {
	mux := accessapi.NewRouter(ErrInvalidMessage, status)
	mux.HandleFunc("GET "+SystemInformationPath, func(w http.ResponseWriter, r *http.Request) {
		accessapi.WriteJSON(w, http.StatusOK, e.cfg.Cell)
	})
	mux.HandleFunc("GET "+EventsPath("{imsi}"), func(w http.ResponseWriter, r *http.Request) {
		imsi := r.PathValue("imsi")
		if _, err := e.address(imsi); err != nil {
			mux.WriteError(w, err)
			return
		}
		e.streams.Serve(w, r, imsi, e.log)
	})
	accessapi.HandleMessage(mux, TypeAttachRequest, func(ctx context.Context, imsi string, m AttachRequest) (any, error) { return e.Attach(ctx, imsi, m) })
	accessapi.HandleMessage(mux, TypeAttachComplete, func(ctx context.Context, imsi string, m AttachComplete) (any, error) {
		return nil, e.AttachComplete(imsi, m)
	})
	accessapi.HandleMessage(mux, TypeDetachRequest, func(ctx context.Context, imsi string, m DetachRequest) (any, error) { return e.Detach(ctx, imsi, m) })
	accessapi.HandleMessage(mux, TypeDetachAccept, func(ctx context.Context, imsi string, _ DetachAccept) (any, error) {
		return nil, e.DetachAccepted(imsi)
	})
	accessapi.HandleMessage(mux, TypeActivateDedicatedEPSBearerContextAccept, func(ctx context.Context, imsi string, m ActivateDedicatedEPSBearerContextAccept) (any, error) {
		return nil, e.DedicatedBearerAccepted(ctx, imsi, m)
	})
	accessapi.HandleMessage(mux, TypeActivateDedicatedEPSBearerContextReject, func(ctx context.Context, imsi string, m ActivateDedicatedEPSBearerContextReject) (any, error) {
		return nil, e.DedicatedBearerRejected(ctx, imsi, m)
	})
	accessapi.HandleMessage(mux, TypeDeactivateEPSBearerContextAccept, func(ctx context.Context, imsi string, m DeactivateEPSBearerContextAccept) (any, error) {
		return nil, e.BearerDeactivated(imsi, m)
	})

	mux.HandleFunc("GET "+attachedPath, func(w http.ResponseWriter, r *http.Request) {
		accessapi.WriteJSON(w, http.StatusOK, e.Statuses())
	})
	mux.HandleFunc("GET "+statusPath, func(w http.ResponseWriter, r *http.Request) {
		s, err := e.Status(r.PathValue("imsi"))
		if err != nil {
			mux.WriteError(w, err)
			return
		}
		accessapi.WriteJSON(w, http.StatusOK, s)
	})
	mux.HandleFunc("DELETE "+networkInitiatedDeactivation, func(w http.ResponseWriter, r *http.Request) {
		ebi, err := strconv.ParseUint(r.PathValue("ebi"), 10, 8)
		if err != nil {
			mux.WriteError(w, fmt.Errorf("%w: EPS bearer identity %q", ErrInvalidMessage, r.PathValue("ebi")))
			return
		}
		if err := e.DeactivateEPSBearerContext(r.Context(), r.PathValue("imsi"), uint8(ebi)); err != nil {
			mux.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+networkInitiatedDetach, func(w http.ResponseWriter, r *http.Request) {
		if err := e.NetworkInitiatedDetach(r.Context(), r.PathValue("imsi")); err != nil {
			mux.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// status is the HTTP status of the stub's own errors; 0 is for the ones
// it does not know.
func status(err error) int {
	switch {
	case errors.Is(err, ErrUnknownSubscriber), errors.Is(err, ErrUnknownBearer):
		return http.StatusNotFound
	case errors.Is(err, ErrNoPDNConnection):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidMessage):
		return http.StatusBadRequest
	}
	return 0
}
