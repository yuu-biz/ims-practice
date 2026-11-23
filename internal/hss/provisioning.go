package hss

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// listLimit bounds one page of the subscriptions, and is the page an
// operator who asks for no limit gets.
const listLimit = 1000

// maxRequestBody bounds the body of a POST; a subscription is far
// smaller.
const maxRequestBody = 64 << 10

// subscriptionRequest is a new subscription in the provisioning API.
// The fields have the names of the configuration file, except Tel: a
// form that adds a subscriber with a telephone number can give the tel
// URI apart, and it joins the public identities after the others.
type subscriptionRequest struct {
	PrivateIdentity  string      `json:"private_identity"`
	PublicIdentities []string    `json:"public_identities"`
	BarredIdentities []string    `json:"barred_identities,omitempty"`
	Password         string      `json:"password,omitempty"`
	AKA              *akaRequest `json:"aka,omitempty"`
	Tel              string      `json:"tel,omitempty"` // such as tel:+12025550109
	// UtPassword lets the user change the supplementary services over
	// Ut; without it the XCAP server answers 403 (TS 24.623 5.3.2.3).
	UtPassword string `json:"ut_password,omitempty"`
	// MMTelServices is the first MMTelServices document, as XML text.
	// Without it the subscription gets Config.MMTelServices.
	MMTelServices string `json:"mmtel_services,omitempty"`
	// CustomizedAlertingTone provisions CAT (TS 24.182 4.5.2).
	CustomizedAlertingTone *mmtel.CustomizedAlertingTone `json:"customized_alerting_tone,omitempty"`
	// TestUser marks the subscription as one the operator keeps for
	// trying features out (Subscription.TestUser).
	TestUser bool `json:"test_user,omitempty"`
	// ServiceProfile names the service profile of the subscription, one
	// of those of the configuration. Without it the subscription takes
	// the profile of its kind of user.
	ServiceProfile string `json:"service_profile,omitempty"`
	// RepositoryData is the Sh repository data of the subscription as it
	// is to be stored, which a PUT that puts an archive back carries. It
	// takes the place of MMTelServices, UtPassword and
	// CustomizedAlertingTone, so a request carries either it or those. An
	// empty array, which is not the same as leaving the field out, asks
	// for a subscription with no repository data at all.
	RepositoryData []repositoryDataRequest `json:"repository_data,omitempty"`
}

// repositoryDataRequest is one piece of the Sh repository data in the
// provisioning API: the Service-Indication it is stored under and the
// data as XML text (TS 29.328 7.4). The HSS stores the data without
// reading it. There is no sequence number: the HSS gives new data 0 and
// counts the updates itself (TS 29.328 6.1.2.1).
type repositoryDataRequest struct {
	ServiceIndication string `json:"service_indication"`
	ServiceData       string `json:"service_data"`
}

// akaRequest is the AKA credentials of a new subscription, in the form
// of ParseAKACredentials.
type akaRequest struct {
	K   string `json:"k"`
	OPc string `json:"opc"`
	AMF string `json:"amf,omitempty"`
	SQN string `json:"sqn,omitempty"`
}

func (r subscriptionRequest) subscription(config Config) (Subscription, error) {
	s := Subscription{PrivateIdentity: r.PrivateIdentity, PublicIdentities: r.PublicIdentities, BarredIdentities: r.BarredIdentities, Password: r.Password,
		TestUser: r.TestUser, ServiceProfile: r.ServiceProfile}
	if err := config.CheckServiceProfile(r.ServiceProfile); err != nil {
		return Subscription{}, err
	}
	data, err := r.repositoryData(config)
	if err != nil {
		return Subscription{}, err
	}
	s.RepositoryData = data
	if r.AKA != nil {
		creds, err := ParseAKACredentials(r.AKA.K, r.AKA.OPc, r.AKA.AMF, r.AKA.SQN)
		if err != nil {
			return Subscription{}, err
		}
		s.AKA = creds
	}
	if r.Tel != "" {
		if !strings.HasPrefix(r.Tel, "tel:") {
			return Subscription{}, fmt.Errorf("tel: %q is not a tel URI", r.Tel)
		}
		s.PublicIdentities = append(slices.Clone(s.PublicIdentities), r.Tel)
	}
	return s, s.Validate()
}

// repositoryData is the repository data that the request asks the HSS
// to store: the pieces it carries as repository_data, or else what the
// HSS makes of mmtel_services, ut_password and
// customized_alerting_tone, which is the MMTelServices document of the
// configuration for a request that carries none of the three. A request
// that carries repository_data and one of the three asks for the data in
// two ways at once, and is refused rather than having one of them
// quietly dropped.
func (r subscriptionRequest) repositoryData(config Config) ([]sh.TransparentData, error) {
	if r.RepositoryData == nil {
		return config.RepositoryData(r.PrivateIdentity, r.MMTelServices, r.UtPassword, r.CustomizedAlertingTone)
	}
	if r.MMTelServices != "" || r.UtPassword != "" || r.CustomizedAlertingTone != nil {
		return nil, errors.New("repository_data: not with mmtel_services, ut_password or customized_alerting_tone")
	}
	data := make([]sh.TransparentData, 0, len(r.RepositoryData))
	for _, d := range r.RepositoryData {
		piece := sh.TransparentData{ServiceIndication: d.ServiceIndication}
		if d.ServiceData != "" {
			// Data that is empty is no data; Subscription.Validate refuses it.
			piece.ServiceData = &sh.ServiceData{XML: []byte(d.ServiceData)}
		}
		data = append(data, piece)
	}
	return data, nil
}

// subscriptionResponse is a subscription as the provisioning API shows
// it. It leaves out the password and the AKA keys, which the HSS never
// hands out (it gives the S-CSCF only H(A1) or vectors, TS 29.228 Table
// 6.3.5 and Table 6.3.7). It names the authentication schemes that the
// credentials allow, as SIP-Authentication-Scheme spells them, and adds
// the registration state of TS 29.228 6.1.1.1.
type subscriptionResponse struct {
	PrivateIdentity       string   `json:"private_identity"`
	PublicIdentities      []string `json:"public_identities"`
	BarredIdentities      []string `json:"barred_identities,omitempty"`
	AuthenticationSchemes []string `json:"authentication_schemes"`
	RegistrationState     string   `json:"registration_state"`
	ServerName            string   `json:"server_name,omitempty"`
	// CustomizedAlertingTone is the CAT that the operator provisioned,
	// absent without one.
	CustomizedAlertingTone *mmtel.CustomizedAlertingTone `json:"customized_alerting_tone,omitempty"`
	// TestUser and ServiceProfile are the mark and the profile name of
	// the subscription, absent where it has neither.
	TestUser       bool   `json:"test_user,omitempty"`
	ServiceProfile string `json:"service_profile,omitempty"`
}

func response(s Subscriber) subscriptionResponse {
	var schemes []string
	if s.Password != "" {
		schemes = append(schemes, cx.SchemeSIPDigest)
	}
	if s.AKA != nil {
		schemes = append(schemes, cx.SchemeDigestAKAv1MD5)
	}
	return subscriptionResponse{
		PrivateIdentity:       s.PrivateIdentity,
		PublicIdentities:      s.PublicIdentities,
		BarredIdentities:      s.BarredIdentities,
		AuthenticationSchemes: schemes,
		RegistrationState:     s.State.String(),
		ServerName:            s.ServerName,
		TestUser:              s.TestUser,
		ServiceProfile:        s.ServiceProfile,
	}
}

// Provisioning is the HTTP API that adds, shows and removes
// subscriptions, which the Lab uses. It is this
// project's own API, not one of 3GPP: product HSSs have a provisioning
// interface of their own too.
//
//	GET    /subscriptions[?after={impi}&limit={n}]
//	                               one page of the subscriptions
//	GET    /subscriptions/{impi}   one subscription
//	POST   /subscriptions          add one, from a subscriptionRequest
//	PUT    /subscriptions/{impi}[?dry_run=true]
//	                               add one or replace the whole of it,
//	                               from a subscriptionRequest
//	DELETE /subscriptions/{impi}   remove one
//	PUT    /subscriptions/{impi}/customized-alerting-tone
//	                               provision or change its CAT, from
//	                               {"tone": "cat1", "active": true}
//
// Removing a subscription whose user is registered deregisters the user
// at its S-CSCF, which peers reaches. A request whose subscription the
// HSS cannot read or write gets 500 (Internal Server Error).
func (h *HSS) Provisioning(peers Peers) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /subscriptions", h.listSubscriptions)
	mux.HandleFunc("GET /subscriptions/{impi}", h.getSubscription)
	mux.HandleFunc("POST /subscriptions", h.addSubscription)
	mux.HandleFunc("PUT /subscriptions/{impi}", h.putSubscription)
	mux.HandleFunc("DELETE /subscriptions/{impi}", func(w http.ResponseWriter, r *http.Request) {
		removed, err := h.removeSubscription(r.Context(), peers, r.PathValue("impi"))
		switch {
		case err != nil:
			h.writeStoreFailure(w, err)
		case !removed:
			http.Error(w, "no such subscription", http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("PUT /subscriptions/{impi}/customized-alerting-tone", h.setCustomizedAlertingTone)
	return mux
}

// setCustomizedAlertingTone provisions or changes the CAT of a
// subscription: the operator does it for the user (TS 24.182 4.5.2),
// as the user has no standard way to set it (4.8). The data gets the
// next sequence number, as a change over Sh would (TS 29.328 6.1.2.1).
func (h *HSS) setCustomizedAlertingTone(w http.ResponseWriter, r *http.Request) {
	impi := r.PathValue("impi")
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	var t mmtel.CustomizedAlertingTone
	if err := dec.Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := CATData(t)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	_, ok, err := h.store.Subscriber(ctx, impi)
	if err != nil {
		h.writeStoreFailure(w, err)
		return
	}
	if !ok {
		http.Error(w, "no such subscription", http.StatusNotFound)
		return
	}
	for {
		var old *sh.TransparentData
		stored, ok, err := h.store.RepositoryData(ctx, impi, mmtel.CATServiceIndication)
		if err != nil {
			h.writeStoreFailure(w, err)
			return
		}
		if ok {
			old = &stored
			data.SequenceNumber = sh.NextSequenceNumber(stored.SequenceNumber)
		}
		swapped, err := h.store.CompareAndSwapRepositoryData(ctx, impi, old, data)
		if err != nil {
			h.writeStoreFailure(w, err)
			return
		}
		if swapped {
			break
		}
		// A swap that lost may have lost to a DELETE of the subscription;
		// then nothing can ever win, so the request ends here.
		if _, ok, err := h.store.Subscriber(ctx, impi); err != nil {
			h.writeStoreFailure(w, err)
			return
		} else if !ok {
			http.Error(w, "no such subscription", http.StatusNotFound)
			return
		}
	}
	h.log.Info("customized alerting tone", "private_identity", impi, "tone", t.Tone, "active", t.Active)
	writeJSON(w, http.StatusOK, t)
}

// listSubscriptions answers with one page of the subscriptions, in the
// order of their private identities. "after" is the private identity
// the page before it ended at, and "limit" how many to return, at most
// listLimit. While pages are left, the answer carries the query of the
// request for the next one as a Link header with rel="next" (RFC 8288
// 3); the body is the same list of subscriptions whether the request
// pages or not.
func (h *HSS) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	limit, err := pageLimit(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	subs, next, err := h.store.List(r.Context(), r.URL.Query().Get("after"), limit)
	if err != nil {
		h.writeStoreFailure(w, err)
		return
	}
	list := []subscriptionResponse{}
	for _, s := range subs {
		resp, err := h.responseWithCAT(r.Context(), s)
		if err != nil {
			h.writeStoreFailure(w, err)
			return
		}
		list = append(list, resp)
	}
	if next != "" {
		// A target of the query alone, which is read against the request's
		// own URL (RFC 3986 4.2), so that it holds wherever the API is
		// reached from.
		query := url.Values{"after": {next}, "limit": {strconv.Itoa(limit)}}
		w.Header().Set("Link", `<?`+query.Encode()+`>; rel="next"`)
	}
	writeJSON(w, http.StatusOK, list)
}

// pageLimit is how many subscriptions the request asks for: listLimit
// when it names no limit, and never more than that.
func pageLimit(r *http.Request) (int, error) {
	text := r.URL.Query().Get("limit")
	if text == "" {
		return listLimit, nil
	}
	limit, err := strconv.Atoi(text)
	if err != nil || limit < 1 {
		return 0, fmt.Errorf("limit: %q is not a number of subscriptions", text)
	}
	return min(limit, listLimit), nil
}

// responseWithCAT is the response for s with the CAT of its repository
// data, so that the Lab shows which tone the callers of the user
// hear.
func (h *HSS) responseWithCAT(ctx context.Context, s Subscriber) (subscriptionResponse, error) {
	resp := response(s)
	d, ok, err := h.store.RepositoryData(ctx, s.PrivateIdentity, mmtel.CATServiceIndication)
	if err != nil {
		return subscriptionResponse{}, err
	}
	if ok && d.ServiceData != nil {
		if t, err := mmtel.ParseCustomizedAlertingTone(d.ServiceData.XML); err == nil {
			resp.CustomizedAlertingTone = &t
		}
	}
	return resp, nil
}

func (h *HSS) getSubscription(w http.ResponseWriter, r *http.Request) {
	s, ok, err := h.store.Subscriber(r.Context(), r.PathValue("impi"))
	if err != nil {
		h.writeStoreFailure(w, err)
		return
	}
	if !ok {
		http.Error(w, "no such subscription", http.StatusNotFound)
		return
	}
	resp, err := h.responseWithCAT(r.Context(), s)
	if err != nil {
		h.writeStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *HSS) addSubscription(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	var req subscriptionRequest
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sub, err := req.subscription(h.config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.Add(r.Context(), sub); err != nil {
		h.writeAddFailure(w, err)
		return
	}
	h.log.Info("subscription added", "private_identity", sub.PrivateIdentity, "public_identities", sub.PublicIdentities)
	w.Header().Set("Location", "/subscriptions/"+sub.PrivateIdentity)
	writeJSON(w, http.StatusCreated, response(Subscriber{Subscription: sub}))
}

// putAnswer is the answer to a PUT of a subscription: what the HSS did,
// or would do on a dry run, and the subscription as it stands
// afterwards.
type putAnswer struct {
	Result       string               `json:"result"`
	Subscription subscriptionResponse `json:"subscription"`
}

// What a PUT did. unchanged means that the stored subscription is the
// one the request carries already, credentials and repository data
// included, which only the HSS can tell: it hands out neither.
const (
	resultCreated   = "created"
	resultUpdated   = "updated"
	resultUnchanged = "unchanged"
)

// putSubscription adds the subscription of the path or replaces the
// whole of it, so that sending the same body again leaves the same
// subscription and an operator can hold a file of them beside the
// network. What it does not touch is what the running subscriber has:
// the registration state and its S-CSCF, the mark of an authentication
// under way, the IP-SM-GW of the user with the short messages waiting
// for it, and SQN_HE. The aka.sqn of the request is therefore read only
// where the subscription is new; replacing one never takes SQN_HE back,
// which would let an authentication vector be used twice.
//
// Everything else the request leaves out goes: a PUT with none of
// repository_data, mmtel_services and ut_password leaves the
// subscription with the MMTelServices document of the configuration, so
// what the user set over Ut is back to the configured default. The
// answer says which of created, updated and unchanged it did. With
// dry_run it changes nothing and answers the same, except that a
// creation it did not make answers 200 rather than 201.
func (h *HSS) putSubscription(w http.ResponseWriter, r *http.Request) {
	impi := r.PathValue("impi")
	dry, err := dryRun(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	var req subscriptionRequest
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(req.PrivateIdentity, impi) {
		http.Error(w, "private_identity: not the one of the path", http.StatusBadRequest)
		return
	}
	sub, err := req.subscription(h.config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	want := sub.RepositoryData

	ctx := r.Context()
	// The lock is held over the read and the write, so that the request
	// replaces the subscription it has read and no Cx or Sh request of
	// the user is halfway through one of its own.
	unlock, err := h.lockSubscriber(impi)
	if err != nil {
		h.writeStoreFailure(w, err)
		return
	}
	defer unlock()
	stored, exists, err := h.store.Subscriber(ctx, impi)
	if err != nil {
		h.writeStoreFailure(w, err)
		return
	}
	if dry {
		// The Store answers the 409 of a real request; a dry run asks it
		// the same question, so that it answers what the request would.
		taken, err := h.takenIdentity(ctx, sub)
		if err != nil {
			h.writeStoreFailure(w, err)
			return
		}
		if taken != "" {
			http.Error(w, fmt.Sprintf("%s: %s", ErrIdentityTaken, taken), http.StatusConflict)
			return
		}
	}

	if !exists {
		if !dry {
			if err := h.store.Add(ctx, sub); err != nil {
				h.writeAddFailure(w, err)
				return
			}
			h.log.Info("subscription added", "private_identity", sub.PrivateIdentity, "public_identities", sub.PublicIdentities)
		}
		status := http.StatusCreated
		if dry {
			status = http.StatusOK
		}
		writePutAnswer(w, status, resultCreated, Subscriber{Subscription: sub}, want)
		return
	}

	next := stored
	next.Subscription = sub
	if next.AKA != nil && stored.AKA != nil {
		next.AKA.SQN = stored.AKA.SQN
	}
	data, err := h.store.AllRepositoryData(ctx, impi)
	if err != nil {
		h.writeStoreFailure(w, err)
		return
	}
	if sameSubscription(stored.Subscription, next.Subscription) && sameRepositoryData(data, want) {
		writePutAnswer(w, http.StatusOK, resultUnchanged, stored, data)
		return
	}
	if !dry {
		if err := h.store.Replace(ctx, next); err != nil {
			h.writeAddFailure(w, err)
			return
		}
		// The repository data follows the subscription. A store that fails
		// in between leaves the subscription written and its data not,
		// which the same PUT sent again finishes.
		ok, err := h.setRepositoryData(ctx, impi, data, want)
		if err != nil {
			h.writeStoreFailure(w, err)
			return
		}
		if !ok {
			http.Error(w, "no such subscription", http.StatusNotFound)
			return
		}
		h.log.Info("subscription replaced", "private_identity", sub.PrivateIdentity, "public_identities", sub.PublicIdentities,
			"state", stored.State.String())
	}
	writePutAnswer(w, http.StatusOK, resultUpdated, next, want)
}

// dryRun reads the dry_run of a request. Absent is false.
func dryRun(r *http.Request) (bool, error) {
	text := r.URL.Query().Get("dry_run")
	if text == "" {
		return false, nil
	}
	dry, err := strconv.ParseBool(text)
	if err != nil {
		return false, fmt.Errorf("dry_run: %q is not true or false", text)
	}
	return dry, nil
}

// takenIdentity is the public identity of sub that belongs to another
// subscription, or "". It looks each identity up by its key, so it costs
// the same however many subscriptions the HSS holds.
func (h *HSS) takenIdentity(ctx context.Context, sub Subscription) (string, error) {
	for _, id := range sub.PublicIdentities {
		owner, ok, err := h.store.SubscriberOf(ctx, id)
		if err != nil {
			return "", err
		}
		if ok && privateKey(owner.PrivateIdentity) != privateKey(sub.PrivateIdentity) {
			return id, nil
		}
	}
	return "", nil
}

// sameSubscription reports whether what a PUT would store is the stored
// subscription already. It compares the whole of a Subscription, so that
// a field added to one later is compared without this check changing,
// and leaves the repository data to sameRepositoryData: a stored
// subscription carries none in that field.
func sameSubscription(stored, next Subscription) bool {
	stored.RepositoryData, next.RepositoryData = nil, nil
	return reflect.DeepEqual(stored, next)
}

// sameRepositoryData reports whether the repository data that a PUT
// carries is what is stored: the same Service-Indications holding the
// same data. The sequence numbers are not compared, as they count the
// updates the data has had rather than saying what is in it (TS 29.328
// 6.1.2.1).
func sameRepositoryData(stored, want []sh.TransparentData) bool {
	if len(stored) != len(want) {
		return false
	}
	for _, d := range want {
		if s, ok := piece(stored, d.ServiceIndication); !ok || !sameData(s, d) {
			return false
		}
	}
	return true
}

// piece is the repository data of data under serviceIndication, and
// whether there is any. A Subscription holds each Service-Indication
// once (Subscription.Validate).
func piece(data []sh.TransparentData, serviceIndication string) (sh.TransparentData, bool) {
	i := slices.IndexFunc(data, func(d sh.TransparentData) bool { return d.ServiceIndication == serviceIndication })
	if i < 0 {
		return sh.TransparentData{}, false
	}
	return data[i], true
}

// sameData reports whether two pieces of repository data hold the same
// data, whatever sequence number either carries.
func sameData(a, b sh.TransparentData) bool {
	return a.ServiceData != nil && b.ServiceData != nil && bytes.Equal(a.ServiceData.XML, b.ServiceData.XML)
}

// setRepositoryData makes the repository data of the subscription the
// one that a PUT carries: every piece of want whose data is not stored
// already written, and every Service-Indication of stored that want
// does not name removed. A piece that is stored as it is goes
// unwritten, so that the sequence number of data nobody changed stays
// where an AS reading over Sh left it. It reports whether the
// subscription is still there.
func (h *HSS) setRepositoryData(ctx context.Context, privateID string, stored, want []sh.TransparentData) (bool, error) {
	for _, d := range want {
		if s, found := piece(stored, d.ServiceIndication); found && sameData(s, d) {
			continue
		}
		if ok, err := h.swapRepositoryData(ctx, privateID, d); err != nil || !ok {
			return ok, err
		}
	}
	for _, d := range stored {
		if _, found := piece(want, d.ServiceIndication); found {
			continue
		}
		// Data with no ServiceData is a removal (Store.
		// CompareAndSwapRepositoryData).
		if ok, err := h.swapRepositoryData(ctx, privateID, sh.TransparentData{ServiceIndication: d.ServiceIndication}); err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

// swapRepositoryData stores one piece of repository data, or removes
// what is under its Service-Indication when it has no ServiceData. The
// piece goes in with the next sequence number, as an update over Sh
// would give it (TS 29.328 6.1.2.1), and with a compare and swap against
// what is stored, so that an update which the operator's tone or an Sh
// request made meanwhile is seen rather than overwritten. A swap that
// loses is read and tried again, as the tone is
// (setCustomizedAlertingTone). It reports whether the subscription is
// still there: once it is gone, no swap can ever win.
func (h *HSS) swapRepositoryData(ctx context.Context, privateID string, data sh.TransparentData) (bool, error) {
	for {
		stored, found, err := h.store.RepositoryData(ctx, privateID, data.ServiceIndication)
		if err != nil {
			return false, err
		}
		var old *sh.TransparentData
		if found {
			old = &stored
			data.SequenceNumber = sh.NextSequenceNumber(stored.SequenceNumber)
		} else if data.ServiceData == nil {
			return true, nil // nothing to remove
		}
		swapped, err := h.store.CompareAndSwapRepositoryData(ctx, privateID, old, data)
		if err != nil {
			return false, err
		}
		if swapped {
			return true, nil
		}
		if _, ok, err := h.store.Subscriber(ctx, privateID); err != nil || !ok {
			return false, err
		}
	}
}

// writePutAnswer answers a PUT with the subscription it leaves behind
// and the tone of the repository data that goes with it.
func writePutAnswer(w http.ResponseWriter, status int, result string, sub Subscriber, data []sh.TransparentData) {
	resp := response(sub)
	resp.CustomizedAlertingTone = catOf(data)
	writeJSON(w, status, putAnswer{Result: result, Subscription: resp})
}

// catOf is the customized alerting tone of data, if it holds one.
func catOf(data []sh.TransparentData) *mmtel.CustomizedAlertingTone {
	for _, d := range data {
		if d.ServiceIndication != mmtel.CATServiceIndication || d.ServiceData == nil {
			continue
		}
		if t, err := mmtel.ParseCustomizedAlertingTone(d.ServiceData.XML); err == nil {
			return &t
		}
	}
	return nil
}

// writeAddFailure answers a request that would have given a public
// identity of one subscription to another.
func (h *HSS) writeAddFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrIdentityTaken) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	h.writeStoreFailure(w, err)
}

// removeSubscription removes the subscription of privateID, and reports
// whether there was one. If its user was registered, the HSS then tells
// the S-CSCF (TS 29.228 6.1.3). The subscription is gone even when that
// fails: the HSS answers any later Cx request for the user as for an
// unknown one. When the store fails, no RTR goes: the HSS cannot tell
// whether the subscription is gone. A lock that is not taken leaves the
// subscription itself alone: another instance may be changing the
// subscriber.
func (h *HSS) removeSubscription(ctx context.Context, peers Peers, privateID string) (bool, error) {
	unlock, err := h.lockSubscriber(privateID)
	if err != nil {
		return false, err
	}
	s, ok, err := h.store.Delete(ctx, privateID)
	// The lock is not held over the RTR: the S-CSCF may be waiting on a
	// SAA for this user before it can take up the RTR.
	unlock()
	if err != nil || !ok {
		return false, err
	}
	log := h.log.With("private_identity", s.PrivateIdentity)
	log.Info("subscription removed", "state", s.State.String())
	if err := h.registrationTermination(ctx, peers, s); err != nil {
		log.Warn("RTR failed", "scscf", s.SCSCF.OriginHost, "error", err.Error())
	}
	return true, nil
}

// Peers is how the HSS reaches a node that connected to it, by the
// Origin-Host of that node: an S-CSCF for a Registration-Termination-
// Request, and an SMS-IWMSC for an Alert-Service-Centre-Request.
// *diameter.Server is one.
type Peers interface {
	Request(ctx context.Context, originHost string, request *diam.Message) (*diam.Message, error)
}

// registrationTermination is the network initiated de-registration by
// the HSS, TS 29.228 6.1.3, for a removed subscription: the S-CSCF of a
// user that is Registered or Unregistered learns with
// PERMANENT_TERMINATION that it no longer serves the user, and that no
// other S-CSCF will (6.1.3.1). The HSS has already forgotten the user,
// which is the Not Registered state with no S-CSCF name.
func (h *HSS) registrationTermination(ctx context.Context, peers Peers, s Subscriber) error {
	if s.State == NotRegistered || s.SCSCF.OriginHost == "" {
		return nil
	}
	rtr := cx.RegistrationTerminationRequest{
		Header:   diameter.NewHeader(h.node, s.SCSCF.OriginHost, s.SCSCF.OriginRealm),
		UserName: s.PrivateIdentity,
		// 6.1.3.1: when a private identity goes with all of its public
		// identities, no Public-Identity is sent.
		DeregistrationReason: cx.DeregistrationReason{ReasonCode: cx.PermanentTermination},
	}
	answer, err := peers.Request(ctx, s.SCSCF.OriginHost, rtr.Message())
	if err != nil {
		return err
	}
	rta, err := cx.ParseRegistrationTerminationAnswer(answer)
	if err != nil {
		return err
	}
	if !rta.Result.Success() {
		return fmt.Errorf("RTA with %s", rta.Result)
	}
	return nil
}

// writeStoreFailure answers a request whose subscription the HSS cannot
// read or write. It is not 404: the subscription may well be there.
func (h *HSS) writeStoreFailure(w http.ResponseWriter, err error) {
	h.log.Warn("subscriber store failed", "error", err.Error())
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
