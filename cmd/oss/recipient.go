package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// notificationPath is where the recipient takes the notifications. A
// subscription names it in its notificationRecipientAddress, such as
// http://oss:8080/notifications.
const notificationPath = "/notifications"

// maxNotification is the largest body the recipient reads. A
// notification about one alarm is a few hundred bytes.
const maxNotification = 16 << 10

// keep is how many notifications, and how many refused bodies, the
// recipient keeps; beyond it the oldest go. A scenario that stops and
// starts a node a few times sends some tens. With the limits on the
// size of a body, what it keeps stays below 16 MiB of notifications and
// 1 MiB of refused bodies.
const keep = 1000

// maxRefusedBody is how much of a refused body the recipient keeps:
// enough to see what it was.
const maxRefusedBody = 1 << 10

// received is a notification that the recipient took, as it came.
type received struct {
	ReceivedAt       time.Time            `json:"received_at"`
	NotificationType mns.NotificationType `json:"notification_type"`
	Notification     json.RawMessage      `json:"notification"`
}

// refused is a POST that the recipient answered with an error, and why.
// Body is the first maxRefusedBody bytes of the body, and BodyLength the
// length of the whole of what was read.
type refused struct {
	ReceivedAt time.Time `json:"received_at"`
	Status     int       `json:"status"`
	Reason     string    `json:"reason"`
	Body       string    `json:"body"`
	BodyLength int       `json:"body_length"`
}

// recipient is the notification recipient of the OSS: the notification
// target of TS 28.532 12.1.1.3.2.4, the notification sink of TS 32.158
// 5.5.4. It keeps what it takes, in memory and in the order it came,
// for whoever reads /received.
type recipient struct {
	log *slog.Logger
	now func() time.Time

	mu       sync.Mutex
	received []received
	refused  []refused
}

func newRecipient(log *slog.Logger) *recipient {
	return &recipient{log: log, now: time.Now}
}

// Handler serves
//
//	POST /notifications   takes one notification and answers 204
//	GET  /received        the notifications taken, oldest first; with
//	                      ?notification_type=notifyNewAlarm, those of
//	                      that type only
//	GET  /refused         the bodies answered with an error, and why
//
// Every error answer, also to another path or method, is an
// ErrorResponse, as the table 12.1.1.3.2.4.3.1-3 of TS 28.532 has it
// for an error of the notification target.
func (r *recipient) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(notificationPath, only(http.MethodPost, r.notify))
	mux.Handle("/received", only(http.MethodGet, r.listReceived))
	mux.Handle("/refused", only(http.MethodGet, r.listRefused))
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		writeError(w, http.StatusNotFound, "oss serves no "+req.URL.Path)
	})
	return mux
}

// only serves handler for method and answers 405 to the others.
func only(method string, handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != method {
			w.Header().Set("Allow", method)
			writeError(w, http.StatusMethodNotAllowed, req.URL.Path+" takes "+method+" only")
			return
		}
		handler(w, req)
	})
}

// notify takes one notification. Success is 204 with no body (TS 32.158
// 5.5.4, the 204 answer of TS28111_FaultNotifications.yaml); a body the
// recipient cannot take is answered with an error code and an
// ErrorResponse that says why.
func (r *recipient) notify(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxNotification))
	if err != nil {
		status := http.StatusBadRequest
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			status = http.StatusRequestEntityTooLarge
		}
		r.refuse(w, status, err.Error(), body)
		return
	}
	// The request body of TS28111_FaultNotifications.yaml is
	// application/json.
	if mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		r.refuse(w, http.StatusUnsupportedMediaType, "the Content-Type is not application/json", body)
		return
	}
	notificationType, err := checkNotification(body)
	if err != nil {
		r.refuse(w, http.StatusBadRequest, err.Error(), body)
		return
	}
	r.mu.Lock()
	r.received = appendKept(r.received, received{ReceivedAt: r.now(), NotificationType: notificationType, Notification: body})
	r.mu.Unlock()
	r.log.Info("notification received", "notification_type", notificationType, "notification", json.RawMessage(body))
	w.WriteHeader(http.StatusNoContent)
}

func (r *recipient) refuse(w http.ResponseWriter, status int, reason string, body []byte) {
	kept := refused{ReceivedAt: r.now(), Status: status, Reason: reason, Body: string(body[:min(len(body), maxRefusedBody)]), BodyLength: len(body)}
	r.mu.Lock()
	r.refused = appendKept(r.refused, kept)
	r.mu.Unlock()
	r.log.Warn("notification refused", "status", status, "reason", reason)
	writeError(w, status, reason)
}

func writeError(w http.ResponseWriter, status int, errorInfo string) {
	writeJSON(w, status, mns.ErrorResponse{Error: mns.ErrorResponseError{ErrorInfo: errorInfo}})
}

func appendKept[T any](list []T, v T) []T {
	list = append(list, v)
	if len(list) > keep {
		list = list[len(list)-keep:]
	}
	return list
}

func (r *recipient) listReceived(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	notificationType := mns.NotificationType(query.Get("notification_type"))
	query.Del("notification_type")
	if len(query) > 0 {
		writeError(w, http.StatusBadRequest, "the only query parameter is notification_type")
		return
	}
	r.mu.Lock()
	out := []received{}
	for _, n := range r.received {
		if notificationType == "" || n.NotificationType == notificationType {
			out = append(out, n)
		}
	}
	r.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (r *recipient) listRefused(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	out := append([]refused{}, r.refused...)
	r.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is out already, so an error here can only be the
	// connection's.
	_ = json.NewEncoder(w).Encode(v)
}
