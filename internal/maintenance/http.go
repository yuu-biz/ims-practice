package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxRequestBody bounds the body of a request; the largest, a
// configuration change, is far smaller.
const maxRequestBody = 64 << 10

// Handler serves the API at the paths of the package documentation.
// The node mounts it on its maintenance listener under PathPrefix,
// next to its /metrics.
func (n *Node) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+StatePath, func(w http.ResponseWriter, r *http.Request) {
		s, err := n.State()
		if err != nil {
			n.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	})
	mux.HandleFunc("PUT "+administrativeStatePattern, func(w http.ResponseWriter, r *http.Request) {
		var req AdministrativeStateRequest
		if err := readJSON(w, r, &req); err != nil {
			writeError(w, err)
			return
		}
		s, err := n.SetAdministrativeState(r.PathValue("role"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	})
	mux.HandleFunc("GET "+ConfigurationPath, func(w http.ResponseWriter, r *http.Request) {
		c, err := n.Configuration()
		if err != nil {
			n.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("PATCH "+ConfigurationPath, func(w http.ResponseWriter, r *http.Request) {
		var req ConfigurationChangeRequest
		if err := readJSON(w, r, &req); err != nil {
			writeError(w, err)
			return
		}
		change, err := n.ChangeConfiguration(req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, change)
	})
	mux.HandleFunc("GET "+LogLevelPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, LogLevel{Level: n.logLevel.Level()})
	})
	mux.HandleFunc("PUT "+LogLevelPath, func(w http.ResponseWriter, r *http.Request) {
		var req LogLevelRequest
		if err := readJSON(w, r, &req); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, n.SetLogLevel(req))
	})

	mux.HandleFunc("GET "+AlarmsPath, func(w http.ResponseWriter, r *http.Request) {
		f, err := ParseAlarmFilter(r.URL.Query())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, n.Alarms(f))
	})
	mux.HandleFunc("PUT "+ackStatePattern, func(w http.ResponseWriter, r *http.Request) {
		var req AckStateRequest
		if err := readJSON(w, r, &req); err != nil {
			writeError(w, err)
			return
		}
		a, err := n.SetAckState(r.PathValue("id"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	})

	// The patterns without a method take the methods that the ones
	// above do not, so that a 405 has a JSON body like every other
	// error.
	for path, allow := range map[string]string{
		StatePath:                  "GET, HEAD",
		ConfigurationPath:          "GET, HEAD, PATCH",
		administrativeStatePattern: "PUT",
		LogLevelPath:               "GET, HEAD, PUT",
		AlarmsPath:                 "GET, HEAD",
		ackStatePattern:            "PUT",
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{Error: r.Method + " is not allowed on " + r.URL.Path})
		})
	}
	mux.HandleFunc(PathPrefix, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, ErrorBody{Error: "no such resource: " + r.URL.Path})
	})
	return mux
}

// readJSON decodes the body of r into v, refusing unknown fields and
// anything after the value.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: body: %v", ErrInvalidRequest, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: body: more than one JSON value", ErrInvalidRequest)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError answers an error of the operations with its status.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	body := ErrorBody{Error: err.Error()}
	var refused *SettingsRefusedError
	switch {
	case errors.As(err, &refused):
		status = http.StatusBadRequest
		body.Settings = refused.Settings
	case errors.Is(err, ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, ErrUnknownFunction), errors.Is(err, ErrUnknownAlarm):
		status = http.StatusNotFound
	case errors.Is(err, ErrInvalidTransition), errors.Is(err, ErrStaleGeneration):
		status = http.StatusConflict
	}
	writeJSON(w, status, body)
}

// internalError answers a failure of the node's own callbacks, such as
// a view that holds a secret. The details go to the log, where the
// node's operator can read them, but not the secret's value, which
// CheckRedacted never puts in its error.
func (n *Node) internalError(w http.ResponseWriter, r *http.Request, err error) {
	n.log.Error("maintenance API failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
	writeJSON(w, http.StatusInternalServerError, ErrorBody{Error: "the node could not build the answer: " + err.Error()})
}
