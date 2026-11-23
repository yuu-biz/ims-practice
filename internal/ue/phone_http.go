package ue

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Handler is the control API of the phone. It is this project's own
// API, of the kind a test tool offers to drive it, and no 3GPP or IETF
// specification defines it; nothing of it goes between the UE and the
// IMS. Bodies are JSON.
//
//	GET  /status          the Status
//	POST /register        register, or refresh the registration
//	POST /deregister      deregister
//	POST /call            {"target": URI, "give_up_after_seconds": n,
//	                       "codecs": ["AMR-WB", "AMR", "PCMU"]}: codecs,
//	                      when given, are offered in this call instead
//	                      of those of the UE, the most preferred first
//	POST /answer          answer the incoming call that rings; a call
//	                      that waits behind another is taken after the
//	                      other goes on hold (TS 24.615 4.5.5.3.3)
//	POST /hangup          {"reject_status": n, "call_id": id}: end the
//	                      call in the foreground, or the call with that
//	                      Call-ID; reject_status is 603 when left out
//	PUT  /answer-policy   {"mode": "auto"|"reject"|"manual",
//	                       "ring_seconds": n, "reject_status": n}
//	GET  /events          the Status on each change, short messages
//	                      included, as server-sent events (HTML
//	                      Living Standard)
//	POST /sms             {"to": "+12025550102", "text": text,
//	                       "status_report": bool}: send a short message
//	                      (TS 24.341 5.3.1.2); answers with the SentSMS
//	                      once the MESSAGE has its 202
//	GET  /sms             the SMSHistory: {"sent": [SentSMS...],
//	                      "received": [ReceivedSMS...]}, oldest first
//	PUT  /sms-memory      {"full": bool}: take the memory of the UE for
//	                      incoming short messages away, or give it back;
//	                      giving it back notifies the network when the UE
//	                      owes a notification (TS 24.341 5.3.2.5)
//	POST /sms-memory/notify  notify the network once more that the UE has
//	                      memory, whatever it owes
//	POST /hold            put the answered call in the foreground on
//	                      hold (re-INVITE with sendonly)
//	POST /resume          take the held call back (re-INVITE with
//	                      sendrecv) once no other call is answered
//	POST /swap            hold the call in the foreground and take the
//	                      held one back
//	POST /merge           make a three-way session of the two answered
//	                      calls (TS 24.147 5.3.1.3.3, IR.92 2.3.3)
//	GET  /conference      the conference the UE takes part in:
//	                      {"uri": URI, "users": [{"entity", "display_text",
//	                      "status"}...]}, 404 without one
//	POST /dtmf            {"digits": "123#"}: send DTMF digits, 0-9, *,
//	                      # and A-D, as telephone events
//
// A call goes out while another is on the line only once that one is on
// hold, and the line holds two calls at most (TS 24.615). The Status
// has the call in the foreground in "call", all of them in "calls", and
// the conference in "conference".
//
// The status of a call has "session": the codec, the directions, the
// precondition table, the media resources, hold, early media, the RTP
// and RTCP counts and the DTMF digits sent and received (SessionStatus).
//
// The requests of the memory for short messages answer with the
// SMSReception that follows them. A UE that is not registered cannot
// notify the network that it has memory again, so those requests answer
// 409 (Conflict) after they have set the state.
//
// The other POSTs answer with the Status. A request that the state of the
// phone does not allow, such as a second call, gets 409 (Conflict); one
// that the UE fails to carry out, such as a REGISTER that the network
// refuses or a re-INVITE that the peer turns down, gets 502 (Bad
// Gateway). A short message that cannot be sent,
// such as a text too long for one, gets 400. Errors come as {"error":
// text}.
func (p *Phone) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, p.Status())
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		p.respond(w, p.changeRegistration(r.Context(), true))
	})
	mux.HandleFunc("POST /deregister", func(w http.ResponseWriter, r *http.Request) {
		p.respond(w, p.changeRegistration(r.Context(), false))
	})
	mux.HandleFunc("POST /call", p.postCall)
	mux.HandleFunc("POST /answer", func(w http.ResponseWriter, r *http.Request) {
		p.respond(w, p.PickUp())
	})
	mux.HandleFunc("POST /hangup", p.postHangUp)
	mux.HandleFunc("PUT /answer-policy", p.putAnswerPolicy)
	mux.HandleFunc("GET /events", p.events)
	mux.HandleFunc("POST /sms", p.postSMS)
	mux.HandleFunc("POST /hold", func(w http.ResponseWriter, r *http.Request) { p.respond(w, p.Hold()) })
	mux.HandleFunc("POST /resume", func(w http.ResponseWriter, r *http.Request) { p.respond(w, p.Resume()) })
	mux.HandleFunc("POST /dtmf", p.postDTMF)
	mux.HandleFunc("POST /swap", func(w http.ResponseWriter, r *http.Request) { p.respond(w, p.Swap()) })
	mux.HandleFunc("POST /merge", func(w http.ResponseWriter, r *http.Request) { p.respond(w, p.Merge()) })
	mux.HandleFunc("GET /conference", func(w http.ResponseWriter, r *http.Request) {
		if c := p.Status().Conference; c != nil {
			writeJSON(w, http.StatusOK, c)
			return
		}
		writeError(w, http.StatusNotFound, errors.New("the UE takes part in no conference"))
	})
	mux.HandleFunc("GET /sms", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, p.ue.SMS())
	})
	mux.HandleFunc("PUT /sms-memory", p.putSMSMemory)
	mux.HandleFunc("POST /sms-memory/notify", func(w http.ResponseWriter, r *http.Request) {
		state, err := p.ue.NotifyMemoryAvailable(r.Context())
		p.respondReception(w, state, err)
	})
	return mux
}

func (p *Phone) putSMSMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Full *bool `json:"full"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Full == nil {
		writeError(w, http.StatusBadRequest, errors.New(`the body needs "full": true or false`))
		return
	}
	state, err := p.ue.SetSMSMemoryFull(r.Context(), *req.Full)
	p.respondReception(w, state, err)
}

// respondReception answers a request that changed how the UE takes short
// messages: the state it left behind, or the error of the notification
// that the change set off.
func (p *Phone) respondReception(w http.ResponseWriter, state SMSReception, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, state)
	case errors.Is(err, ErrNoServiceCentre):
		writeError(w, http.StatusConflict, err)
	default:
		p.respond(w, err)
	}
}

func (p *Phone) postCall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target             string   `json:"target"`
		GiveUpAfterSeconds *float64 `json:"give_up_after_seconds"`
		Codecs             []string `json:"codecs"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !isCallTarget(req.Target) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("target %q is neither a sip, sips nor tel URI", req.Target))
		return
	}
	var giveUp time.Duration
	if s := req.GiveUpAfterSeconds; s != nil {
		if *s < 0 {
			writeError(w, http.StatusBadRequest, errors.New("give_up_after_seconds must not be negative"))
			return
		}
		giveUp = time.Duration(*s * float64(time.Second))
	}
	var codecs []sdp.Codec
	if req.Codecs != nil {
		var err error
		if codecs, err = CodecsNamed(req.Codecs); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("codecs: %w", err))
			return
		}
	}
	p.respond(w, p.Dial(req.Target, giveUp, codecs))
}

func (p *Phone) postDTMF(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Digits string `json:"digits"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Digits == "" {
		writeError(w, http.StatusBadRequest, errors.New("no digits"))
		return
	}
	for _, d := range req.Digits {
		if _, ok := rtp.DTMFEvent(d); !ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%q is no DTMF digit", d))
			return
		}
	}
	p.respond(w, p.SendDTMF(req.Digits))
}

func isCallTarget(target string) bool {
	if _, err := sip.ParseURI(target); err == nil {
		return true
	}
	scheme, number, ok := strings.Cut(target, ":")
	return ok && strings.EqualFold(scheme, "tel") && number != ""
}

func (p *Phone) postSMS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		To           string `json:"to"`
		Text         string `json:"text"`
		StatusReport bool   `json:"status_report"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sent, err := p.SendSMS(r.Context(), OutgoingSMS{To: req.To, Text: req.Text, StatusReport: req.StatusReport})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, sent)
	case errors.Is(err, ErrBadShortMessage):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, ErrNoServiceCentre):
		writeError(w, http.StatusConflict, err)
	default:
		p.respond(w, err)
	}
}

func (p *Phone) postHangUp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RejectStatus int    `json:"reject_status"`
		CallID       string `json:"call_id"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// RFC 3261 21.6.2: 603 (Decline) says that the user does not wish to
	// take the call.
	reject := 603
	if req.RejectStatus != 0 {
		if !isRejectStatus(req.RejectStatus) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("reject_status %d is not a status from 400 to 699", req.RejectStatus))
			return
		}
		reject = req.RejectStatus
	}
	p.respond(w, p.HangUpCall(req.CallID, reject))
}

func (p *Phone) putAnswerPolicy(w http.ResponseWriter, r *http.Request) {
	var req answerPolicyJSON
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	policy, err := req.policy()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p.SetAnswerPolicy(policy)
	writeJSON(w, http.StatusOK, policy)
}

func (p *Phone) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("the connection cannot stream"))
		return
	}
	updates := p.watch()
	defer p.unwatch(updates)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(s Status) bool {
		data, _ := json.Marshal(s)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send(p.Status()) {
		return
	}
	for {
		select {
		case s := <-updates:
			if !send(s) {
				return
			}
		case <-r.Context().Done():
			return
		case <-p.done:
			return
		}
	}
}

// respond answers a POST: the Status after a success, or the error.
func (p *Phone) respond(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, p.Status())
	case errors.Is(err, ErrBusy), errors.Is(err, ErrNotRegistered), errors.Is(err, ErrNoCall), errors.Is(err, ErrNotRinging),
		errors.Is(err, ErrNotAnswered), errors.Is(err, ErrNoTelephoneEvent), errors.Is(err, ErrNotHeld), errors.Is(err, ErrNoTwoCalls):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, ErrStopping):
		writeError(w, http.StatusServiceUnavailable, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

// decode reads a JSON body into v. An empty body leaves v as it is.
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
