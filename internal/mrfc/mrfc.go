// Package mrfc is the MRFC, the control side of the Multimedia Resource
// Function (TS 23.228 4.7). An AS reaches it over Mr' with
// the basic network media services of RFC 4240 (TS 24.229 10.3.2.2,
// 10.3.3.2), and it drives an MRFP over Mp with the control API of the
// media node:
//
//   - sip:annc@<mrfc>;play=/provisioned/<name> plays an announcement:
//     the MRFC answers the INVITE with the SDP of an MRFP termination,
//     starts the announcement when the ACK comes (TS 24.229 5.8.2.2.1),
//     and sends BYE when the MRFP reports that it has ended
//     (5.8.3.2.1, RFC 4240 3);
//   - sip:conf=<id>@<mrfc> joins the conference id: the first INVITE
//     for an ID creates its mix on an MRFP, each later one adds a
//     termination to the same context, and a BYE takes the party out
//     again (RFC 4240 5, TS 24.229 5.8.2.1.1.3).
//
// The MRFC is the media focus of a conference; the conference focus,
// its policy and its event package are in the AS (TS 24.147 4).
//
// Each dialog lives in the instance that took its INVITE: the Contact
// names that instance, and the dialog, its timers and its wait for the
// Notify of the MRFP live in its process, as they do in the MMTel AS.
// What two instances share is in the Store: which MRFP and which context
// hold each conference, so that a party that reaches another instance
// joins the same mix. An MRFP is chosen for a new context by a
// hash of the conference ID, or of the Call-ID of an announcement, and
// a party of a conference goes to the MRFP that the Store names.
package mrfc

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/netann"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// MRFP is how the MRFC drives an MRFP over Mp: the operations of the
// control API of the media node, which H.248 can carry.
// *agw.Client is one.
type MRFP interface {
	Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error)
	AddTermination(ctx context.Context, id string, t agw.TerminationRequest) (agw.Termination, error)
	Modify(ctx context.Context, id string, tid agw.TerminationID, change agw.TerminationRequest) error
	SubtractTermination(ctx context.Context, id string, tid agw.TerminationID) (agw.Termination, bool, error)
	Announcement(ctx context.Context, name string) (agw.Announcement, bool, error)
}

// Processor is an MRFP that the MRFC can use. Name is how the Store
// remembers it, the same in every MRFC instance, such as the URL of its
// control API.
type Processor struct {
	Name    string
	Control MRFP
}

// Config is how the MRFC names itself.
type Config struct {
	// Host and Port are where the requests within the dialogs of the
	// MRFC reach it: the Contact of each dialog. Host is the name of
	// this instance, which DNS resolves to it.
	Host string
	Port int
	// NotifyURL is where the MRFPs post the Notify of the terminations
	// of this instance (NotifyHandler).
	NotifyURL string
}

// MRFC is the mrfc role.
type MRFC struct {
	cfg        Config
	processors []Processor
	byName     map[string]MRFP
	store      Store
	locator    ims.Locator
	log        *slog.Logger
	// ua is set by Listen before any call can arrive.
	ua atomic.Pointer[sip.UA]

	// nextRequestID numbers the Events descriptors of this instance, so
	// that a Notify finds the leg it is for.
	nextRequestID atomic.Uint32
	mu            sync.Mutex
	waiting       map[uint32]chan agw.Notify
}

// New returns the MRFC. processors are the MRFPs it may use, in the
// same order in every instance; locator finds the hosts of the requests
// it sends within its dialogs.
func New(cfg Config, processors []Processor, store Store, locator ims.Locator, log *slog.Logger) (*MRFC, error) {
	if len(processors) == 0 {
		return nil, errors.New("mrfc: no MRFP")
	}
	byName := make(map[string]MRFP, len(processors))
	for _, p := range processors {
		if _, dup := byName[p.Name]; dup {
			return nil, fmt.Errorf("mrfc: MRFP %q is listed twice", p.Name)
		}
		byName[p.Name] = p.Control
	}
	m := &MRFC{cfg: cfg, processors: processors, byName: byName, store: store, locator: locator, log: log, waiting: map[uint32]chan agw.Notify{}}
	m.nextRequestID.Store(rand.Uint32())
	return m, nil
}

// Listen starts the UA of the MRFC on transport. The MRFC is the
// terminating UA of each INVITE (TS 24.229 5.8.2.1.1.2, 5.8.2.1.1.3);
// the stack sends the 100 (Trying) for it.
//
// SIMPLIFIED: TS 24.229 5.8.1. The Contact is the SIP URI of the
// instance, not a GRUU. Nothing in this network sends a request to the
// MRFC outside a dialog that it has with it.
func (m *MRFC) Listen(transport *sip.TransportLayer) error {
	contact := "sip:" + m.cfg.Host + ":" + strconv.Itoa(m.cfg.Port)
	ua, err := sip.NewUA(transport, sip.UAConfig{
		SentBy:  m.cfg.Host + ":" + strconv.Itoa(m.cfg.Port),
		Contact: contact,
		OnCall:  m.serve,
		Locate: func(u sip.URI) (sip.Destination, error) {
			return ims.Destination(context.Background(), m.locator, u)
		},
	}, m.log)
	if err != nil {
		return err
	}
	m.ua.Store(ua)
	return nil
}

// Close stops the UA. Dialogs in progress end without a BYE.
func (m *MRFC) Close() {
	if ua := m.ua.Load(); ua != nil {
		ua.Close()
	}
}

// NotifyHandler serves Config.NotifyURL: the Notify of the MRFPs, the
// Announcement Completed of TS 29.333 5.17.2.11.
func (m *MRFC) NotifyHandler() http.Handler {
	return agw.NotifyHandler(func(n agw.Notify) {
		m.mu.Lock()
		ch, ok := m.waiting[n.RequestID]
		m.mu.Unlock()
		if !ok {
			m.log.Info("Notify for no leg of this instance", "context", n.Context, "termination", string(n.Termination), "request_id", n.RequestID)
			return
		}
		select {
		case ch <- n:
		default:
		}
	})
}

// events is the Events descriptor of a leg that waits for the end of
// what it plays, with the channel its Notify comes on. Call forget once
// the leg is done.
func (m *MRFC) events() (*agw.Events, <-chan agw.Notify) {
	id := m.nextRequestID.Add(1)
	ch := make(chan agw.Notify, 1)
	m.mu.Lock()
	m.waiting[id] = ch
	m.mu.Unlock()
	return &agw.Events{RequestID: id, Detect: []agw.EventName{agw.SignalCompleted}, Notify: m.cfg.NotifyURL}, ch
}

func (m *MRFC) forget(e *agw.Events) {
	m.mu.Lock()
	delete(m.waiting, e.RequestID)
	m.mu.Unlock()
}

// choose is the MRFP for a new context: the same for the same key in
// every instance.
func (m *MRFC) choose(key string) Processor {
	h := fnv.New32a()
	h.Write([]byte(key))
	return m.processors[h.Sum32()%uint32(len(m.processors))]
}

// serve answers an INVITE outside a dialog.
//
// SIMPLIFIED: TS 24.229 5.8.2.1.1.1 and RFC 4240 2 and 8. The MRFC keeps
// no charging data: it neither stores the P-Charging-Vector and
// P-Charging-Function-Addresses of the INVITE nor adds its fe-addr, as
// no node of this project writes charging records. It takes sip: and
// sips: Request-URIs alike over the plain transports of the network
// (nothing is encrypted, so that the capture shows the content), where RFC 4240 asks a media server to support sips: for
// conferences, and it authenticates no AS, which RFC 4240 8 recommends.
func (m *MRFC) serve(c *sip.IncomingCall) {
	log := m.log.With("call_id", c.Request.Get("Call-ID"), "request_uri", c.Request.RequestURI)
	u, err := sip.ParseURI(c.Request.RequestURI)
	if err != nil {
		m.refuse(c, sip.URI{}, &netann.Error{Status: 400, Reason: "Bad Request-URI"}, log)
		return
	}
	req, err := netann.Parse(u)
	var refusal *netann.Error
	switch {
	case errors.As(err, &refusal):
		m.refuse(c, u, refusal, log)
	case err != nil:
		m.refuse(c, u, &netann.Error{Status: 500, Reason: err.Error()}, log)
	case req.Announcement != nil:
		m.announce(c, u, *req.Announcement, log)
	case req.Conference != nil:
		m.join(c, u, *req.Conference, log)
	}
}

// refuse answers the INVITE with the response that RFC 4240 gives for
// the reason.
//
// SIMPLIFIED: RFC 4240 3. The reason phrases it suggests, such as
// "Mandatory play parameter missing", go into a Warning (RFC 3261 20.43,
// code 399), and the status line keeps the standard phrase: sip.UA
// writes the status line itself.
func (m *MRFC) refuse(c *sip.IncomingCall, u sip.URI, e *netann.Error, log *slog.Logger) {
	log.Info("INVITE refused", "status", e.Status, "reason", e.Reason)
	headers := []sip.HeaderField{m.warning(399, e.Reason)}
	if u.Host != "" {
		headers = append(headers, assertedIdentity(u))
	}
	if err := c.Reject(e.Status, headers...); err != nil {
		log.Warn("refusal not sent", "error", err.Error())
	}
}

// warning is a Warning header field from this instance (RFC 3261 20.43).
func (m *MRFC) warning(code int, text string) sip.HeaderField {
	return sip.HeaderField{Name: "Warning", Value: fmt.Sprintf("%d %s %s", code, m.cfg.Host, strconv.Quote(text))}
}

// assertedIdentity is the P-Asserted-Identity that the MRFC gives in its
// responses to an initial INVITE (TS 24.229 5.8.2.1.1.1): the
// Request-URI that the AS sent, without its parameters.
func assertedIdentity(u sip.URI) sip.HeaderField {
	id := sip.URI{Scheme: u.Scheme, User: u.User, Host: u.Host, Port: u.Port}
	return sip.HeaderField{Name: "P-Asserted-Identity", Value: "<" + id.String() + ">"}
}
