package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// errCtlUsage is a "ue ctl" whose arguments name no request.
var errCtlUsage = errors.New("ue ctl: unknown action")

const ctlUsage = `usage: ue ctl [-control host:port] ACTION
  Sends one request to the control API of a running "ue serve" (see
  ue.Phone.Handler) and prints the JSON answer.

  status            GET  /status
  call URI          POST /call       {"target": URI}
  answer            POST /answer     (a waiting call holds the other first)
  hangup [CALL-ID]  POST /hangup     the call in the foreground, or CALL-ID
  hold              POST /hold
  resume            POST /resume
  swap              POST /swap
  merge             POST /merge      three-way session of the two calls
  conference        GET  /conference the conference and its users
`

// ctlRequest is the request of the control API that a "ue ctl" action
// sends: its method, path and JSON body.
func ctlRequest(args []string) (method, path string, body any, err error) {
	if len(args) == 0 {
		return "", "", nil, errCtlUsage
	}
	action, rest := args[0], args[1:]
	switch {
	case action == "status" && len(rest) == 0:
		return http.MethodGet, "/status", nil, nil
	case action == "conference" && len(rest) == 0:
		return http.MethodGet, "/conference", nil, nil
	case action == "call" && len(rest) == 1:
		return http.MethodPost, "/call", map[string]string{"target": rest[0]}, nil
	case action == "hangup" && len(rest) == 1:
		return http.MethodPost, "/hangup", map[string]string{"call_id": rest[0]}, nil
	case len(rest) == 0 && (action == "answer" || action == "hangup" || action == "hold" || action == "resume" ||
		action == "swap" || action == "merge"):
		return http.MethodPost, "/" + action, nil, nil
	}
	return "", "", nil, errCtlUsage
}

// runCtl is "ue ctl": it sends the request of args to the control API
// at control, an address such as :8090, and writes the answer to out.
// An answer other than 2xx is an error, after it is written.
func runCtl(control string, args []string, out io.Writer) error {
	method, path, body, err := ctlRequest(args)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(control)
	if err != nil {
		return fmt.Errorf("-control: %w", err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	var payload io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+net.JoinHostPort(host, port)+path, payload)
	if err != nil {
		return err
	}
	// A merge waits for both REFERs; a REFER subscription may take some
	// seconds.
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s answered %s", method, path, resp.Status)
	}
	return nil
}
