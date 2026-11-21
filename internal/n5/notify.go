package n5

import (
	"context"
	"net/http"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// AF is what the AF does with the notifications of the PCF
// (Npcf_PolicyAuthorization_Notify, TS 29.514 4.2.5). Both bodies name
// the context they are about: EventsNotification.EvSubsUri through
// AppSessionURI, and TerminationInfo.ResUri directly. So any instance
// of the AF can handle them, by looking the context up in its store.
//
// The handler answers 204 only after the method returns nil; an error
// that is a *ProblemError is sent as it is, any other as 500.
type AF interface {
	// Notify receives the events of a subscription (TS 29.514 4.2.5.2):
	// resources allocated or lost for some flows (4.2.5.5, 4.2.5.8), or
	// the loss of the signalling path (4.2.5.10).
	Notify(ctx context.Context, n EventsNotification) error
	// Terminate receives the request to delete a context that is no
	// longer valid (TS 29.514 4.2.5.3). After the 204 the AF deletes the
	// context with Client.Delete, so an implementation starts that after
	// it has returned, for example on a goroutine of its own.
	Terminate(ctx context.Context, t TerminationInfo) error
}

// The path segments the PCF appends to the notification URIs (TS
// 29.514 5.5.2.2, 5.5.3.2).
const (
	NotifyPath    = "/notify"
	TerminatePath = "/terminate"
)

// NewNotificationHandler returns the handler the AF serves at the
// notifUri it put in its requests: a POST to {notifUri}/notify goes to
// af.Notify, one to {notifUri}/terminate to af.Terminate. The handler
// matches only the last path segment, so the AF is free to choose the
// rest of notifUri.
func NewNotificationHandler(af AF) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			api.WriteError(w, &ProblemError{Status: http.StatusMethodNotAllowed, Problem: ProblemDetails{}})
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, NotifyPath):
			var n EventsNotification
			if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &n); p != nil {
				api.WriteError(w, p)
				return
			}
			var absent []string
			if n.EvSubsUri == "" {
				absent = append(absent, "/evSubsUri")
			}
			if len(n.EvNotifs) == 0 {
				absent = append(absent, "/evNotifs")
			}
			if len(absent) > 0 {
				api.WriteError(w, api.Missing(absent...))
				return
			}
			answer(w, af.Notify(r.Context(), n))
		case strings.HasSuffix(r.URL.Path, TerminatePath):
			var t TerminationInfo
			if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &t); p != nil {
				api.WriteError(w, p)
				return
			}
			var absent []string
			if t.TermCause == "" {
				absent = append(absent, "/termCause")
			}
			if t.ResUri == "" {
				absent = append(absent, "/resUri")
			}
			if len(absent) > 0 {
				api.WriteError(w, api.Missing(absent...))
				return
			}
			answer(w, af.Terminate(r.Context(), t))
		default:
			api.WriteError(w, &ProblemError{Status: http.StatusNotFound, Problem: ProblemDetails{
				Detail: "a notification URI ends in " + NotifyPath + " or " + TerminatePath,
			}})
		}
	})
}

// answer acknowledges a notification with 204 No Content (TS 29.514
// 4.2.5.2, 4.2.5.3), or sends the error.
func answer(w http.ResponseWriter, err error) {
	if err != nil {
		api.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
