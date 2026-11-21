package oam

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testRealm = "ims.mnc001.mcc001.3gppnetwork.org"

// fakeHSS is the HSS provisioning API in the shape of internal/hss: the
// paths, the JSON of the requests and answers, and errors as text from
// http.Error. It also serves the operations that the OAM uses beyond
// those of internal/hss (PUT, PATCH and the export), unless it is
// basic.
type fakeHSS struct {
	server *httptest.Server

	mu   sync.Mutex
	subs map[string]fakeSubscription // by private identity, lower case
	// byPublic is the private key of each public identity.
	byPublic map[string]string

	// echo makes a refusal quote the request, as a careless HSS might,
	// to show that the OAM redacts what the HSS says.
	echo atomic.Bool
	// delay is how long each PUT takes.
	delay          time.Duration
	inFlight, most atomic.Int64
	puts           atomic.Int64
}

// fakeSubscription is what the fake keeps: the request as the HSS would
// store it, with the tel URI among the public identities and the
// MMTelServices document and Ut credential as repository data.
type fakeSubscription struct {
	PrivateIdentity  string                 `json:"private_identity"`
	PublicIdentities []string               `json:"public_identities"`
	Password         SecretString           `json:"password,omitempty"`
	AKA              *AKARecord             `json:"aka,omitempty"`
	RepositoryData   []RepositoryDataRecord `json:"repository_data,omitempty"`
	TestUser         bool                   `json:"test_user,omitempty"`
	ServiceProfile   string                 `json:"service_profile,omitempty"`
}

// newFakeHSS serves the HSS; basic leaves out what the HSS of
// internal/hss does not have.
func newFakeHSS(t *testing.T, basic bool) *fakeHSS {
	t.Helper()
	h := &fakeHSS{subs: map[string]fakeSubscription{}, byPublic: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /subscriptions", h.list)
	mux.HandleFunc("GET /subscriptions/{impi}", h.get)
	mux.HandleFunc("POST /subscriptions", h.post)
	mux.HandleFunc("DELETE /subscriptions/{impi}", h.delete)
	if !basic {
		mux.HandleFunc("PUT /subscriptions/{impi}", h.put)
		mux.HandleFunc("PATCH /subscriptions/{impi}", h.patch)
		mux.HandleFunc("GET /subscriptions/export", h.export)
	}
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

func (h *fakeHSS) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *fakeHSS) stored(impi string) (fakeSubscription, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.subs[strings.ToLower(impi)]
	return s, ok
}

// response is the subscriptionResponse of internal/hss.
func (s fakeSubscription) response() Subscription {
	r := Subscription{PrivateIdentity: s.PrivateIdentity, PublicIdentities: s.PublicIdentities,
		RegistrationState: "Not Registered", TestUser: s.TestUser, ServiceProfile: s.ServiceProfile}
	if s.Password != "" {
		r.AuthenticationSchemes = append(r.AuthenticationSchemes, "SIP Digest")
	}
	if s.AKA != nil {
		r.AuthenticationSchemes = append(r.AuthenticationSchemes, "Digest-AKAv1-MD5")
	}
	return r
}

func (h *fakeHSS) list(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := []Subscription{}
	for _, k := range slices.Sorted(maps.Keys(h.subs)) {
		list = append(list, h.subs[k].response())
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *fakeHSS) get(w http.ResponseWriter, r *http.Request) {
	s, ok := h.stored(r.PathValue("impi"))
	if !ok {
		http.Error(w, "no such subscription", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, s.response())
}

func (h *fakeHSS) delete(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := strings.ToLower(r.PathValue("impi"))
	s, ok := h.subs[k]
	if !ok {
		http.Error(w, "no such subscription", http.StatusNotFound)
		return
	}
	delete(h.subs, k)
	for _, id := range s.PublicIdentities {
		delete(h.byPublic, strings.ToLower(id))
	}
	w.WriteHeader(http.StatusNoContent)
}

// decode reads a request as the HSS does: unknown fields refused.
func decode(w http.ResponseWriter, r *http.Request, v any) ([]byte, bool) {
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// subscription checks a request as Subscription.Validate and
// subscriptionRequest.subscription of internal/hss do, and returns it as
// the fake keeps it.
func (h *fakeHSS) subscription(req hssSubscriptionRequest) (fakeSubscription, error) {
	s := fakeSubscription{PrivateIdentity: req.PrivateIdentity, PublicIdentities: slices.Clone(req.PublicIdentities),
		Password: req.Password, AKA: req.AKA, TestUser: req.TestUser, ServiceProfile: req.ServiceProfile}
	if req.Tel != "" {
		s.PublicIdentities = append(s.PublicIdentities, req.Tel)
	}
	switch {
	case s.PrivateIdentity == "":
		return s, errors.New("private_identity: required")
	case len(s.PublicIdentities) == 0:
		return s, errors.New("public_identities: at least one is required")
	case s.Password == "" && s.AKA == nil:
		return s, errors.New("password or aka: at least one is required")
	}
	s.RepositoryData = slices.Clone(req.RepositoryData)
	if req.RepositoryData == nil {
		if req.MMTelServices != "" {
			if !strings.Contains(req.MMTelServices, "<simservs") {
				return s, errors.New("mmtel_services: not an MMTelServices document")
			}
			s.RepositoryData = append(s.RepositoryData, RepositoryDataRecord{ServiceIndication: "MMTEL-Services", ServiceData: req.MMTelServices})
		}
		if req.UtPassword != "" {
			// H(A1), never the password, as UtCredentialData keeps it.
			ha1 := md5.Sum([]byte(req.PrivateIdentity + ":" + testRealm + ":" + string(req.UtPassword)))
			s.RepositoryData = append(s.RepositoryData, RepositoryDataRecord{ServiceIndication: "Ut-Credential", ServiceData: "<credential>" + hex.EncodeToString(ha1[:]) + "</credential>"})
		}
	}
	return s, nil
}

func (h *fakeHSS) post(w http.ResponseWriter, r *http.Request) {
	var req hssSubscriptionRequest
	if _, ok := decode(w, r, &req); !ok {
		return
	}
	s, err := h.subscription(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, taken := h.subs[strings.ToLower(s.PrivateIdentity)]; taken {
		http.Error(w, "identity already belongs to a subscription: "+s.PrivateIdentity, http.StatusConflict)
		return
	}
	h.store(s)
	writeJSON(w, http.StatusCreated, s.response())
}

func (h *fakeHSS) store(s fakeSubscription) {
	k := strings.ToLower(s.PrivateIdentity)
	if old, ok := h.subs[k]; ok {
		for _, id := range old.PublicIdentities {
			delete(h.byPublic, strings.ToLower(id))
		}
	}
	h.subs[k] = s
	for _, id := range s.PublicIdentities {
		h.byPublic[strings.ToLower(id)] = k
	}
}

// put is PUT /subscriptions/{impi}, which internal/hss lacks: create
// or replace, with SQN_HE taken from the request only for a new
// subscription.
func (h *fakeHSS) put(w http.ResponseWriter, r *http.Request) {
	now := h.inFlight.Add(1)
	defer h.inFlight.Add(-1)
	for m := h.most.Load(); now > m && !h.most.CompareAndSwap(m, now); m = h.most.Load() {
	}
	h.puts.Add(1)
	time.Sleep(h.delay)

	var req hssSubscriptionRequest
	body, ok := decode(w, r, &req)
	if !ok {
		return
	}
	if !strings.EqualFold(req.PrivateIdentity, r.PathValue("impi")) {
		http.Error(w, "private_identity: not the one of the path", http.StatusBadRequest)
		return
	}
	s, err := h.subscription(req)
	if err != nil {
		msg := err.Error()
		if h.echo.Load() {
			msg += " in " + string(body)
		}
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	dry := r.URL.Query().Get("dry_run") == "true"

	h.mu.Lock()
	defer h.mu.Unlock()
	k := strings.ToLower(s.PrivateIdentity)
	for _, id := range s.PublicIdentities {
		if owner, taken := h.byPublic[strings.ToLower(id)]; taken && owner != k {
			http.Error(w, "identity already belongs to a subscription: "+id, http.StatusConflict)
			return
		}
	}
	old, exists := h.subs[k]
	result := Created
	if exists {
		if s.AKA != nil && old.AKA != nil {
			aka := *s.AKA
			aka.SQN = old.AKA.SQN
			s.AKA = &aka
		}
		result = Updated
		if a, b := mustJSON(old), mustJSON(s); bytes.Equal(a, b) {
			result = Unchanged
		}
	}
	if !dry && result != Unchanged {
		h.store(s)
	}
	status := http.StatusOK
	if result == Created && !dry {
		status = http.StatusCreated
	}
	writeJSON(w, status, hssPutAnswer{Result: result, Subscription: s.response()})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (h *fakeHSS) patch(w http.ResponseWriter, r *http.Request) {
	var p hssPatch
	if _, ok := decode(w, r, &p); !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	k := strings.ToLower(r.PathValue("impi"))
	s, ok := h.subs[k]
	if !ok {
		http.Error(w, "no such subscription", http.StatusNotFound)
		return
	}
	if p.TestUser != nil {
		s.TestUser = *p.TestUser
	}
	if p.ServiceProfile != nil {
		s.ServiceProfile = *p.ServiceProfile
	}
	h.subs[k] = s
	writeJSON(w, http.StatusOK, s.response())
}

// export is GET /subscriptions/export, which internal/hss lacks: limit
// subscriptions after the private identity of after, in the order of
// the private identities, credentials and SQN_HE included, one per
// line.
func (h *fakeHSS) export(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 || limit > 1000 {
		http.Error(w, "limit: from 1 to 1000", http.StatusBadRequest)
		return
	}
	after := strings.ToLower(r.URL.Query().Get("after"))
	h.mu.Lock()
	var page []fakeSubscription
	for _, k := range slices.Sorted(maps.Keys(h.subs)) {
		if k > after && len(page) < limit {
			page = append(page, h.subs[k])
		}
	}
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	for _, s := range page {
		_ = enc.Encode(s)
	}
}
