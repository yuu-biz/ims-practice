package n5

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// NewServer returns a server that serves h on addr with cleartext
// HTTP/2 only (sbi.NewServer). TS 29.500 5.3 makes TCP port 80 the
// default for an "http" apiRoot, so addr is usually ":80".
func NewServer(addr string, h http.Handler) *http.Server {
	return sbi.NewServer(addr, h)
}

// PCF is what a PCF stub implements behind NewHandler: the three
// operations of Npcf_PolicyAuthorization that the AF invokes (TS
// 29.514 4.2.2, 4.2.3, 4.2.4). The handler has checked the media type
// and the mandatory attributes before it calls.
//
// An error that is a *ProblemError becomes the answer as it is; build
// it with NewProblem, such as NewProblem(CausePDUSessionNotAvailable,
// "") when session binding fails (4.2.2.2). Any other error is answered
// with 500 SYSTEM_FAILURE.
type PCF interface {
	// Create makes an Individual Application Session Context and returns
	// its appSessionId and its representation. The handler answers 201
	// with the context and the Location built from the appSessionId.
	Create(ctx context.Context, c AppSessionContext) (appSessionID string, created AppSessionContext, err error)
	// Update applies a JSON Merge Patch to the context appSessionID. It
	// returns the modified context for a 200, or nil for a 204.
	Update(ctx context.Context, appSessionID string, patch AppSessionContextUpdateDataPatch) (*AppSessionContext, error)
	// Delete removes the context appSessionID. evSubsc, when not nil,
	// lists the events to report in the answer; the method returns the
	// context with them for a 200, or nil for a 204 (TS 29.514 4.2.4.2).
	// An unknown context is NewProblem(CauseApplicationSessionContextNotFound, "").
	Delete(ctx context.Context, appSessionID string, evSubsc *EventsSubscReqData) (*AppSessionContext, error)
}

// NewHandler returns the HTTP handler of the Npcf_PolicyAuthorization
// API of a PCF, to serve with NewServer. It decodes each request,
// calls pcf and encodes the answer. It keeps no state.
//
// SIMPLIFIED: TS 29.514 5.3. The GET of a context, the PUT and DELETE
// of the Events Subscription sub-resource (the Subscribe and
// Unsubscribe operations) and the PcscfRestoration custom operation are
// not served: the P-CSCF subscribes within Create and Update, and P-CSCF
// restoration is not in this project. Go's mux answers them with 404 or
// 405.
func NewHandler(pcf PCF) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+AppSessionsPath, func(w http.ResponseWriter, r *http.Request) {
		var c AppSessionContext
		if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &c); p != nil {
			api.WriteError(w, p)
			return
		}
		if p := checkCreate(c); p != nil {
			api.WriteError(w, p)
			return
		}
		id, created, err := pcf.Create(r.Context(), c)
		if err != nil {
			api.WriteError(w, err)
			return
		}
		// TS 29.514 4.2.2.2: the Location is the URI of the new
		// resource, {apiRoot}/npcf-policyauthorization/v1/app-sessions/{appSessionId}.
		// The apiRoot is the authority the AF reached, so that any
		// instance behind it can serve the next request.
		w.Header().Set("Location", "http://"+r.Host+AppSessionsPath+"/"+url.PathEscape(id))
		api.WriteJSON(w, http.StatusCreated, created)
	})
	mux.HandleFunc("PATCH "+AppSessionsPath+"/{appSessionId}", func(w http.ResponseWriter, r *http.Request) {
		var patch AppSessionContextUpdateDataPatch
		if p := api.ReadJSON(w, r, contentTypeMergePatch, &patch); p != nil {
			api.WriteError(w, p)
			return
		}
		modified, err := pcf.Update(r.Context(), r.PathValue("appSessionId"), patch)
		writeOptionalContext(w, modified, err)
	})
	mux.HandleFunc("POST "+AppSessionsPath+"/{appSessionId}/delete", func(w http.ResponseWriter, r *http.Request) {
		// The body is optional (TS 29.514 4.2.4.2): without one there is
		// no Content-Type either.
		var evSubsc *EventsSubscReqData
		if r.Header.Get("Content-Type") != "" {
			evSubsc = new(EventsSubscReqData)
			if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, evSubsc); p != nil {
				api.WriteError(w, p)
				return
			}
		} else {
			io.Copy(io.Discard, io.LimitReader(r.Body, sbi.MaxBodySize))
		}
		deleted, err := pcf.Delete(r.Context(), r.PathValue("appSessionId"), evSubsc)
		writeOptionalContext(w, deleted, err)
	})
	return mux
}

// checkCreate looks for the attributes that Annex A.2 makes required
// in a Create: notifUri and suppFeat, and one of the UE addresses.
func checkCreate(c AppSessionContext) *ProblemError {
	r := c.AscReqData
	if r == nil {
		return api.Missing("/ascReqData")
	}
	var absent []string
	if r.NotifUri == "" {
		absent = append(absent, "/ascReqData/notifUri")
	}
	if r.SuppFeat == "" {
		absent = append(absent, "/ascReqData/suppFeat")
	}
	if r.UeIpv4 == "" && r.UeIpv6 == "" {
		absent = append(absent, "/ascReqData/ueIpv4")
	}
	if len(absent) > 0 {
		return api.Missing(absent...)
	}
	return nil
}

func writeOptionalContext(w http.ResponseWriter, c *AppSessionContext, err error) {
	switch {
	case err != nil:
		api.WriteError(w, err)
	case c == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		api.WriteJSON(w, http.StatusOK, c)
	}
}

// Notifier sends the notifications of a PCF to an AF
// (Npcf_PolicyAuthorization_Notify, TS 29.514 4.2.5). Like the client it
// keeps no state: the caller gives the notification URI the AF put in
// its request.
type Notifier struct {
	http *http.Client
}

// NFTypePCF is the NF type the notifier names in User-Agent (TS 29.500
// Table 5.2.2.2-1).
const NFTypePCF = "PCF"

// The 3gpp-Sbi-Callback values of the two notifications: the API name
// of TS 29.514 Annex A.2 and the name of the callback in its OpenAPI,
// as TS 29.500 Annex B builds them.
const (
	callbackEventNotification  = "Npcf_PolicyAuthorization_eventNotification"
	callbackTerminationRequest = "Npcf_PolicyAuthorization_terminationRequest"
)

// NewNotifier returns a notifier that speaks cleartext HTTP/2.
func NewNotifier() *Notifier { return &Notifier{http: sbi.NewHTTPClient()} }

// Notify posts events to {notifUri}/notify, where notifUri is the one
// of the subscription, evSubsc.notifUri (TS 29.514 4.2.5.2). The AF
// answers 204.
func (n *Notifier) Notify(ctx context.Context, notifUri string, events EventsNotification) error {
	_, err := api.Send(ctx, n.http, sbi.Request{
		Method:      http.MethodPost,
		URI:         notifUri + NotifyPath,
		Body:        events,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   NFTypePCF,
		Callback:    callbackEventNotification,
	})
	return err
}

// Terminate posts a termination request to {notifUri}/terminate, where
// notifUri is the one of ascReqData (TS 29.514 4.2.5.3). The AF answers
// 204 and then deletes the context.
func (n *Notifier) Terminate(ctx context.Context, notifUri string, t TerminationInfo) error {
	_, err := api.Send(ctx, n.http, sbi.Request{
		Method:      http.MethodPost,
		URI:         notifUri + TerminatePath,
		Body:        t,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   NFTypePCF,
		Callback:    callbackTerminationRequest,
	})
	return err
}
