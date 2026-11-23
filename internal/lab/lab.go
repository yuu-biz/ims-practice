// Package lab is the lab role. It collects the
// HEP copies that the nodes send of every SIP and Diameter message,
// derives the calls and the registrations from the SIP messages alone,
// and serves them to the web UI over HTTP.
//
// The HTTP API answers JSON. Times are RFC 3339 with nanoseconds, in
// UTC.
//
//	GET /api/messages[?call_id=<id>...][&correlation_id=<id>]
//	    The messages in capture order, which is the time in the HEP
//	    packet with the id as tie-breaker: all that are kept, or those
//	    with one of the call_ids or the HEP correlation ID given. A message seen
//	    by its sender and its receiver appears twice, once per node,
//	    with direction "send" and "recv". The ids number the messages
//	    as they arrive, which is not the capture order when several
//	    nodes send copies. A Cx message has the call_id of the SIP
//	    request it was sent for, found by the rule at causeWindow, so
//	    call_id returns a call or a registration with its Cx messages.
//	    A MESSAGE of SMS over IP also has sms, its body decoded (see
//	    ShortMessage).
//	GET /api/calls
//	    One entry per call, oldest first. A call is an INVITE session,
//	    or the sessions that share the icid-value of P-Charging-Vector:
//	    the dialogs of a call that passes routeing B2BUAs, such as the
//	    MMTel AS, each with a Call-ID of its own. legs lists their
//	    Call-IDs, the caller's first, which is also call_id; the other
//	    fields are those of the caller's session, but messages, which
//	    counts them all. call_id=<each leg> gives the ladder of the
//	    whole call.
//	GET /api/registrations
//	    One entry per public user identity, by IMPU. Its call_id is the
//	    Call-ID of the last REGISTER whose 200 was read, for the ladder
//	    of that registration, or "" when only NOTIFYs showed it.
//	GET /api/events
//	    Server-Sent Events. "event: message" carries a message as
//	    /api/messages has it. "event: state" carries {"call": {...}} or
//	    {"registration": {...}}: the entry that changed, as its list has
//	    it. A browser that falls behind misses events and can reload the
//	    lists.
//	GET /api/subscribers
//	    The subscriptions of the HSS, forwarded to the listing of the
//	    HSS provisioning API (GET /subscriptions), whose answer comes
//	    back as it is. 503 when no provisioning API is configured, 502
//	    when it does not answer. The Lab shows the subscribers and
//	    changes none of them: provisioning a subscription is an
//	    operator's work, which the OAM does (package oam).
//	GET /api/epc/ues, GET /api/epc/ues/{imsi},
//	DELETE /api/epc/network-initiated/ues/{imsi}/bearers/{ebi},
//	POST /api/epc/network-initiated/ues/{imsi}/detach
//	    Forwarded with the same method and body to the EPC stub, at the
//	    path without /api/epc (see epc.EPC.Handler): the UEs that are
//	    attached, the state of one UE, the network deactivating one of
//	    its bearers, the network detaching it. 503 when no EPC stub is
//	    configured, 502 when it does not answer.
//	GET /api/fault/faults, POST /api/fault/faults,
//	DELETE /api/fault/faults/{id}
//	    Forwarded with the same method and body to the fault agent, at
//	    the paths of its API (see the fault package): the faults it
//	    holds, injecting one, clearing one. 503 when no fault agent is
//	    configured, 502 when it does not answer, which is what a Compose
//	    deployment without deploy/chaos/agent.yaml gives: its
//	    configuration names the agent, and that file is what starts the
//	    agent and puts the Lab on its network.
//	GET /api/ues
//	    The UE simulators that the Lab controls, in the order of
//	    its config: [{"name", "reachable", "status", "error"}]. status is
//	    what the UE's control API answers to GET /status, or null with
//	    reachable false and the reason in error when the UE does not
//	    answer; the list itself still answers 200.
//	POST /api/ues/{name}/{register|deregister|call|answer|hangup|hold|resume|dtmf|swap|merge|sms},
//	PUT /api/ues/{name}/answer-policy, GET /api/ues/{name}/events
//	    Forwarded with the same method, body and answer to the control
//	    API of that UE (see ue.Phone.Handler), /api/ues/{name}/call to
//	    /call and so on. The events stream through as they come. 404
//	    for a name that is not configured, 502 when the UE does not
//	    answer.
//	GET /api/ues/{name}/xcap,
//	PUT /api/ues/{name}/xcap/services/{service},
//	PUT /api/ues/{name}/xcap/oir
//	    The supplementary services, forwarded in the same way to the
//	    control API of that UE (see ue.SupplementaryServices.Handler):
//	    the UE reads and changes its settings with XCAP over Ut. The
//	    answers are those of the UE, and the errors are those above.
//	GET /
//	    The web UI, embedded from web/.
//
// The Lab keeps a bounded number of messages and drops a call
// with the last of its messages.
package lab

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hep"
)

// Config is how one Lab runs.
type Config struct {
	// MaxMessages bounds the message log. It must be at least 1.
	MaxMessages int
	// HSSProvisioning is the URL of the HSS provisioning API, whose
	// subscriptions the Lab lists, or "".
	HSSProvisioning string
	// EPC is the URL of the API of the EPC stub, whose state of a UE and
	// procedures that the network initiates the Lab passes on, or
	// "".
	EPC string
	// FaultAgent is the URL of the API of the fault agent, whose faults
	// the Lab passes on, or "".
	FaultAgent string
	// UEs are the UE simulators that the web UI drives.
	UEs []UESimulator
}

type Lab struct {
	store       *store
	events      *broker
	subscribers http.Handler
	epc         http.Handler
	faults      http.Handler
	ues         *ueSimulators
	log         *slog.Logger
}

func New(cfg Config, log *slog.Logger) (*Lab, error) {
	// The summaries name the Cx and Sh commands from the dictionary.
	if err := diameter.LoadDictionary(); err != nil {
		return nil, err
	}
	subscribers, err := subscriberAPI(cfg.HSSProvisioning, log)
	if err != nil {
		return nil, err
	}
	epc, err := epcAPI(cfg.EPC, log)
	if err != nil {
		return nil, err
	}
	faults, err := faultAPI(cfg.FaultAgent, log)
	if err != nil {
		return nil, err
	}
	ues, err := newUESimulators(cfg.UEs, log)
	if err != nil {
		return nil, err
	}
	return &Lab{store: newStore(cfg.MaxMessages), events: newBroker(), subscribers: subscribers, epc: epc, faults: faults, ues: ues, log: log}, nil
}

// ingest keeps one HEP packet that came from the address from, and tells
// the open event streams.
func (l *Lab) ingest(p hep.Packet, from netip.Addr) {
	c, err := newMessage(p, from)
	if err != nil {
		l.log.Warn("HEP packet skipped", "from", from.String(), "node", p.NodeName, "error", err.Error())
		return
	}
	m, changes := l.store.add(c)
	l.events.publish("message", m)
	for _, c := range changes {
		l.events.publish("state", c)
	}
}

// Handler serves the API and the web UI.
func (l *Lab) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/messages", l.getMessages)
	mux.HandleFunc("GET /api/calls", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, l.store.calls())
	})
	mux.HandleFunc("GET /api/registrations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, l.store.registrationList())
	})
	mux.HandleFunc("GET /api/events", l.streamEvents)
	mux.Handle("GET /api/subscribers", l.subscribers)
	for _, route := range epcRoutes {
		mux.Handle(route, l.epc)
	}
	for _, route := range faultRoutes {
		mux.Handle(route, l.faults)
	}
	mux.HandleFunc("GET /api/ues", l.ues.getList)
	for _, route := range ueRoutes {
		method, path, _ := strings.Cut(route, " ")
		mux.HandleFunc(method+" /api/ues/{name}"+path, l.ues.forward)
	}
	mux.Handle("GET /", uiHandler())
	return mux
}

func (l *Lab) getMessages(w http.ResponseWriter, r *http.Request) {
	// A Session-Id holds ";", which a client must escape. Query would
	// drop such a parameter silently and answer every message.
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{err.Error()})
		return
	}
	callIDs, correlationID := q["call_id"], q.Get("correlation_id")
	writeJSON(w, http.StatusOK, l.store.messages(func(m Message) bool {
		return (len(callIDs) == 0 || slices.Contains(callIDs, m.CallID)) && (correlationID == "" || m.CorrelationID == correlationID)
	}))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
