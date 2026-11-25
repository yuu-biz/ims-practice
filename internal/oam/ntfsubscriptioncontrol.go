package oam

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/jex"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// DefaultMaxNtfSubscriptions is the default of
// Config.MaxNtfSubscriptions.
const DefaultMaxNtfSubscriptions = 16

// maxNtfSubscriptionID bounds the id of a subscription.
const maxNtfSubscriptionID = 64

// The notifications that the OAM sends (TS 28.111 8.2 to 8.5, 8.8,
// 8.10), which are what a subscription can name in notificationTypes,
// and what one without notificationTypes is sent.
var alarmNotificationTypes = []mns.NotificationType{
	mns.NotifyNewAlarmType,
	mns.NotifyChangedAlarmGeneralType,
	mns.NotifyClearedAlarmType,
	mns.NotifyAckStateChangedType,
	mns.NotifyAlarmListRebuiltType,
	mns.NotifyPotentialFaultyAlarmListType,
}

// The values of the schema ScopeType that a subscription does not take:
// it is contained in the SubNetwork, and the alarms are about objects
// below it.
var narrowScopeTypes = []mns.ScopeType{mns.BaseOnly, "BASE_NTH_LEVEL", "BASE_SUBTREE"}

// isNtfSubscriptionID says whether id may name a subscription: up to 64
// letters, digits, hyphens, underscores and dots, which a path segment
// and a DN carry as they are.
func isNtfSubscriptionID(id string) bool {
	if id == "" || len(id) > maxNtfSubscriptionID {
		return false
	}
	for _, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// ntfSubscriptionResource is the Resource of a subscription
// (NtfSubscriptionControl-Single), as the GET and the PUT answer it.
func (nb *northbound) ntfSubscriptionResource(s NtfSubscriptionControl) mns.NtfSubscriptionControlSingle {
	attrs := s.Attributes
	return mns.NtfSubscriptionControlSingle{
		Top: mns.Top{
			Id:             s.ID,
			ObjectClass:    mns.ClassNtfSubscriptionControl,
			ObjectInstance: nb.subNetwork.Child(mns.ClassNtfSubscriptionControl, s.ID).String(),
		},
		Attributes: &attrs,
	}
}

// serveNtfSubscription serves the NtfSubscriptionControl id below the
// SubNetwork: createMOI with PUT, getMOIAttributes with GET and
// deleteMOI with DELETE (TS 28.532 11.1.1.1, 11.1.1.2, 11.1.1.4;
// 12.1.1.1.2.1, 12.1.1.1.3, 12.1.1.1.5). An OSS subscribes to the alarm
// notifications by creating one, and unsubscribes by deleting it (TS
// 28.622 4.3.22.1).
//
// SIMPLIFIED: TS 28.622 V20.4.0 4.3.22.1. A subscription without
// notificationTypes is sent every kind of notification that the
// producer can send; here that is the six alarm notifications, as the
// OAM sends none of the configuration notifications of TS 28.532, such
// as notifyMOICreation. So the creation and deletion of a subscription
// is never itself notified. The scope of a subscription is the whole
// SubNetwork, absent or BASE_ALL; an OSS that wants the alarms of some
// nodes only selects them with notificationFilter.
func (s *Server) serveNtfSubscription(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPut, http.MethodGet, http.MethodHead, http.MethodDelete:
	default:
		w.Header().Set("Allow", "DELETE, GET, HEAD, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.ntfSubscriptions == nil {
		// TS 32.158 6.6.4: the producer cannot keep subscriptions.
		attributeError(mns.ServiceDisabled, "", "The producer keeps no subscriptions").write(w)
		return
	}
	switch r.Method {
	case http.MethodPut:
		s.putNtfSubscription(w, r, id)
	case http.MethodDelete:
		s.deleteNtfSubscription(w, r, id)
	default:
		s.getNtfSubscription(w, r, id)
	}
}

func (s *Server) getNtfSubscription(w http.ResponseWriter, r *http.Request, id string) {
	if problem := refuseQuery(r.URL.RawQuery); problem != nil {
		problem.write(w)
		return
	}
	if !isNtfSubscriptionID(id) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	sub, err := s.ntfSubscriptions.NtfSubscription(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	case err != nil:
		s.storeFailed(w, "read", id, err)
	default:
		writeJSON(w, http.StatusOK, s.mns.ntfSubscriptionResource(sub))
	}
}

// refuseQuery answers a GET of a subscription that has a query: it
// reads one object, so the query parameters of ProvMnS, which pick
// parts of a tree of objects, ask for more than the OAM evaluates
// there, and any other name is unknown (TS 32.158 6.6.5.2).
func refuseQuery(rawQuery string) *mnsError {
	if rawQuery == "" {
		return nil
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return queryError(mns.ValidationError, mns.QueryMalformed, "The query is malformed")
	}
	var known, unknown []string
	for name := range values {
		switch name {
		case scopeParameter, filterParameter, attributesParameter, fieldsParameter, dataNodeSelectorParameter:
			known = append(known, name)
		default:
			unknown = append(unknown, name)
		}
	}
	slices.Sort(known)
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return queryError(mns.ValidationError, mns.QueryParamNamesInvalid, "Unknown query parameters", unknown...)
	}
	return queryError(mns.ServerLimitation, mns.QueryParamsTooComplex, "Query parameters beyond what the producer evaluates", known...)
}

func (s *Server) deleteNtfSubscription(w http.ResponseWriter, r *http.Request, id string) {
	if r.URL.RawQuery != "" {
		// TS 32.158 5.4: the target URI of a DELETE has no query.
		attributeError(mns.ValidationError, "", "A DELETE has no query").write(w)
		return
	}
	if !isNtfSubscriptionID(id) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	err := s.ntfSubscriptions.DeleteNtfSubscription(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	case err != nil:
		s.storeFailed(w, "delete", id, err)
	default:
		s.log.Info("subscription to the alarm notifications deleted", "id", id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// putNtfSubscription creates the subscription, or replaces it (TS
// 32.158 5.1.2, 5.3): 201 with Location and the subscription for a new
// one, 200 with it for one that was there.
func (s *Server) putNtfSubscription(w http.ResponseWriter, r *http.Request, id string) {
	if r.URL.RawQuery != "" {
		// TS 32.158 5.1.2: the target URI of a PUT has no query.
		attributeError(mns.ValidationError, "", "A PUT has no query").write(w)
		return
	}
	if t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || t != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	var body ntfSubscriptionBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	if err == nil {
		if _, trailing := dec.Token(); !errors.Is(trailing, io.EOF) {
			err = errors.New("more than one JSON value")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		// TS 32.158 6.6.5.4: the body is no representation of the object.
		attributeError(mns.ValidationError, mns.NewObjectRepresentationInvalid, "The body is no NtfSubscriptionControl").write(w)
		return
	}
	sub, problem := s.checkNtfSubscription(id, body)
	if problem != nil {
		problem.write(w)
		return
	}
	created, err := s.ntfSubscriptions.PutNtfSubscription(r.Context(), sub, s.maxNtfSubscriptions)
	switch {
	case errors.Is(err, ErrTooManySubscriptions):
		attributeError(mns.ServerLimitation, "", "More subscriptions than the producer keeps").write(w)
		return
	case err != nil:
		s.storeFailed(w, "write", id, err)
		return
	}
	s.log.Info("subscription to the alarm notifications put", "id", id, "created", created,
		"notification_recipient_address", sub.Attributes.NotificationRecipientAddress)
	resource := s.mns.ntfSubscriptionResource(sub)
	if !created {
		writeJSON(w, http.StatusOK, resource)
		return
	}
	w.Header().Set("Location", s.mns.root.ResourceURI(s.mns.subNetwork.Child(mns.ClassNtfSubscriptionControl, id)))
	writeJSON(w, http.StatusCreated, resource)
}

// ntfSubscriptionBody is the body of a PUT of a subscription, with
// every name of the schema NtfSubscriptionControl-Single of
// TS28623_SubscriptionControlNrm.yaml. Those that
// mns.NtfSubscriptionControlSingle leaves out are kept as they came, so
// that a body with one, which is a subscription that the OAM does not
// take, is told from a body with a name that the schema does not have,
// which is no subscription at all. JSON null leaves a name out.
type ntfSubscriptionBody struct {
	mns.Top
	VsDataContainer  any `json:"VsDataContainer"`
	HeartbeatControl any `json:"HeartbeatControl"`
	Attributes       *struct {
		mns.NtfSubscriptionControlAttributes
		Scope *struct {
			mns.Scope
			DataNodeSelector any `json:"dataNodeSelector"`
		} `json:"scope"`
	} `json:"attributes"`
}

// checkNtfSubscription checks the body of a PUT of the subscription id.
// The representation names the object's id and class (TS 32.158 5.1.2),
// and has the attributes of the schema NtfSubscriptionControl-Single,
// of which notificationRecipientAddress is mandatory (TS 28.622
// 4.3.22.2).
//
// It answers the first problem that the consumer can mend, and only
// then the first part of the body that the OAM does not take: TS 32.158
// 6.6.4 gives SERVER_LIMITATION to a request that is well formed and
// understood, and 6.6.5.1 puts the more fundamental reason first.
func (s *Server) checkNtfSubscription(id string, body ntfSubscriptionBody) (NtfSubscriptionControl, *mnsError) {
	invalid := func(tokens ...string) *mnsError {
		return attributeError(mns.ValidationError, mns.NewAttributeValueInvalid, "Invalid values of attributes", pointer(tokens...))
	}
	missing := func(tokens ...string) *mnsError {
		return attributeError(mns.ValidationError, mns.NewObjectAttributeValueMissing, "Attributes that a new object needs", pointer(tokens...))
	}
	var limit *mnsError
	beyond := func(title string, tokens ...string) {
		if limit == nil {
			limit = attributeError(mns.ServerLimitation, "", title, pointer(tokens...))
		}
	}
	if body.VsDataContainer != nil {
		beyond("Vendor-specific data, which the producer does not keep", "VsDataContainer")
	}
	if body.HeartbeatControl != nil {
		beyond("Heartbeat notifications, which the producer does not send", "HeartbeatControl")
	}
	switch {
	case !isNtfSubscriptionID(id) || body.Id != id:
		return NtfSubscriptionControl{}, invalid("id")
	case body.ObjectClass == "":
		return NtfSubscriptionControl{}, missing("objectClass")
	case body.ObjectClass != mns.ClassNtfSubscriptionControl:
		return NtfSubscriptionControl{}, attributeError(mns.ValidationError, mns.NewObjectClassNameInvalid, "Object classes that cannot be created here", pointer("objectClass"))
	case body.ObjectInstance != "" && body.ObjectInstance != s.mns.subNetwork.Child(mns.ClassNtfSubscriptionControl, id).String():
		return NtfSubscriptionControl{}, invalid("objectInstance")
	case body.Attributes == nil || body.Attributes.NotificationRecipientAddress == "":
		return NtfSubscriptionControl{}, missing("attributes", "notificationRecipientAddress")
	}
	attrs := body.Attributes.NtfSubscriptionControlAttributes
	u, err := url.Parse(attrs.NotificationRecipientAddress)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return NtfSubscriptionControl{}, invalid("attributes", "notificationRecipientAddress")
	}
	if attrs.NotificationTypes != nil {
		seen := map[mns.NotificationType]bool{}
		for _, t := range attrs.NotificationTypes {
			if !slices.Contains(alarmNotificationTypes, t) || seen[t] {
				return NtfSubscriptionControl{}, invalid("attributes", "notificationTypes")
			}
			seen[t] = true
		}
		if len(seen) == 0 {
			return NtfSubscriptionControl{}, invalid("attributes", "notificationTypes")
		}
	}
	if scope := body.Attributes.Scope; scope != nil {
		switch t := scope.ScopeType; {
		case t != "" && t != mns.BaseAll && !slices.Contains(narrowScopeTypes, t):
			return NtfSubscriptionControl{}, invalid("attributes", "scope")
		case scope.DataNodeSelector != nil:
			beyond("Scopes that select data nodes", "attributes", "scope", "dataNodeSelector")
		case t != mns.BaseAll:
			// No scopeType is BASE_ONLY (TS 32.158 6.1.2).
			beyond("Scopes narrower than the SubNetwork", "attributes", "scope")
		}
		attrs.Scope = &scope.Scope
	}
	if attrs.NotificationFilter != "" {
		if _, err := jex.ParseCondition(attrs.NotificationFilter); err != nil {
			var syntax *jex.SyntaxError
			if errors.As(err, &syntax) {
				return NtfSubscriptionControl{}, invalid("attributes", "notificationFilter")
			}
			beyond("Filters beyond what the producer evaluates", "attributes", "notificationFilter")
		}
	}
	if limit != nil {
		return NtfSubscriptionControl{}, limit
	}
	return NtfSubscriptionControl{ID: id, Attributes: attrs}, nil
}

// storeFailed answers a request whose subscription store failed: 500
// without a body, as no type of TS 32.158 6.6.4 says more than the
// status.
func (s *Server) storeFailed(w http.ResponseWriter, op, id string, err error) {
	s.log.Error("the subscription store failed", "operation", op, "id", id, "error", err.Error())
	w.WriteHeader(http.StatusInternalServerError)
}
