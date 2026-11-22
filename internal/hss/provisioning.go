package hss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// listLimit bounds a listing of the subscriptions. A product HSS pages
// through its subscribers instead; the dashboard of this project shows
// a handful.
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
	s := Subscription{PrivateIdentity: r.PrivateIdentity, PublicIdentities: r.PublicIdentities, BarredIdentities: r.BarredIdentities, Password: r.Password}
	data, err := config.RepositoryData(r.PrivateIdentity, r.MMTelServices, r.UtPassword, r.CustomizedAlertingTone)
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
	}
}

// Provisioning is the HTTP API that adds, shows and removes
// subscriptions, which the dashboard uses. It is this
// project's own API, not one of 3GPP: product HSSs have a provisioning
// interface of their own too.
//
//	GET    /subscriptions          the subscriptions, at most listLimit
//	GET    /subscriptions/{impi}   one subscription
//	POST   /subscriptions          add one, from a subscriptionRequest
//	DELETE /subscriptions/{impi}   remove one
//	PUT    /subscriptions/{impi}/customized-alerting-tone
//	                               provision or change its CAT, from
//	                               {"tone": "cat1", "active": true}
//
// Removing a subscription whose user is registered deregisters the user
// at its S-CSCF, which scscfs reaches. A request whose subscription the
// HSS cannot read or write gets 500 (Internal Server Error).
func (h *HSS) Provisioning(scscfs SCSCFs) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /subscriptions", h.listSubscriptions)
	mux.HandleFunc("GET /subscriptions/{impi}", h.getSubscription)
	mux.HandleFunc("POST /subscriptions", h.addSubscription)
	mux.HandleFunc("DELETE /subscriptions/{impi}", func(w http.ResponseWriter, r *http.Request) {
		removed, err := h.removeSubscription(r.Context(), scscfs, r.PathValue("impi"))
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

func (h *HSS) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := h.store.List(r.Context(), listLimit)
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
	writeJSON(w, http.StatusOK, list)
}

// responseWithCAT is the response for s with the CAT of its repository
// data, so that the dashboard shows which tone the callers of the user
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
		if errors.Is(err, ErrIdentityTaken) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		h.writeStoreFailure(w, err)
		return
	}
	h.log.Info("subscription added", "private_identity", sub.PrivateIdentity, "public_identities", sub.PublicIdentities)
	w.Header().Set("Location", "/subscriptions/"+sub.PrivateIdentity)
	writeJSON(w, http.StatusCreated, response(Subscriber{Subscription: sub}))
}

// removeSubscription removes the subscription of privateID, and reports
// whether there was one. If its user was registered, the HSS then tells
// the S-CSCF (TS 29.228 6.1.3). The subscription is gone even when that
// fails: the HSS answers any later Cx request for the user as for an
// unknown one. When the store fails, no RTR goes: the HSS cannot tell
// whether the subscription is gone. A lock that is not taken leaves the
// subscription itself alone: another instance may be changing the
// subscriber.
func (h *HSS) removeSubscription(ctx context.Context, scscfs SCSCFs, privateID string) (bool, error) {
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
	if err := h.registrationTermination(ctx, scscfs, s); err != nil {
		log.Warn("RTR failed", "scscf", s.SCSCF.OriginHost, "error", err.Error())
	}
	return true, nil
}

// SCSCFs is how the HSS reaches an S-CSCF, by the Origin-Host the S-CSCF
// sent in its MAR and SAR. *diameter.Server is one.
type SCSCFs interface {
	Request(ctx context.Context, originHost string, request *diam.Message) (*diam.Message, error)
}

// registrationTermination is the network initiated de-registration by
// the HSS, TS 29.228 6.1.3, for a removed subscription: the S-CSCF of a
// user that is Registered or Unregistered learns with
// PERMANENT_TERMINATION that it no longer serves the user, and that no
// other S-CSCF will (6.1.3.1). The HSS has already forgotten the user,
// which is the Not Registered state with no S-CSCF name.
func (h *HSS) registrationTermination(ctx context.Context, scscfs SCSCFs, s Subscriber) error {
	if s.State == NotRegistered || s.SCSCF.OriginHost == "" {
		return nil
	}
	rtr := cx.RegistrationTerminationRequest{
		Header:   cx.NewHeader(h.node, s.SCSCF.OriginHost, s.SCSCF.OriginRealm),
		UserName: s.PrivateIdentity,
		// 6.1.3.1: when a private identity goes with all of its public
		// identities, no Public-Identity is sent.
		DeregistrationReason: cx.DeregistrationReason{ReasonCode: cx.PermanentTermination},
	}
	answer, err := scscfs.Request(ctx, s.SCSCF.OriginHost, rtr.Message())
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
