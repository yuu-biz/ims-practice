package oam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/jex"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// The AlarmList of the SubNetwork (TS 28.111 7.3.2) holds the alarm
// records of every node of the inventory. The OAM keeps no copy: a GET
// reads the list of each node, as GET /oam/v1/alarms does, and a PATCH
// hands the acknowledgement to the node whose record it is.
//
// A record is keyed by <node>.<alarm_id>: the name of the node in the
// inventory, which holds no dot, then the alarm_id of the node's
// maintenance API, which is random. The key thus differs across the
// lists of all nodes and over time (TS 28.111 6.4), and a PATCH tells
// from it, without asking anyone, which node to hand each record to.

// alarmRecordKey is the key of the record alarmID of node.
func alarmRecordKey(node, alarmID string) string { return node + "." + alarmID }

// nodeAlarmList is the alarm list of one node as the AlarmList of the
// SubNetwork has it.
type nodeAlarmList struct {
	info    maintenance.AlarmList
	records map[string]mns.AlarmRecord
}

// alarmRecord is r, a record of the node of the inventory named node,
// as the AlarmList of the SubNetwork has it. Its objectInstance is the
// node's below the ManagedElement of the inventory name: two instances
// of a role that share their configuration file write the same
// ManagedElement=, which would make two records with the same four
// identifying attributes in one list (TS 28.111 6.4, 6.5).
func (nb *northbound) alarmRecord(node string, r maintenance.AlarmRecord) (mns.AlarmRecord, error) {
	own, err := mns.ParseDN(r.ObjectInstance)
	if err != nil || own[0].ClassName != mns.ClassManagedElement {
		return mns.AlarmRecord{}, fmt.Errorf("alarm %s: object instance %q is not below a ManagedElement", r.AlarmID, r.ObjectInstance)
	}
	instance := slices.Concat(nb.managedElement(node), own[1:])
	return mns.AlarmRecord{
		ObjectInstance:    instance.String(),
		NotificationId:    r.NotificationID,
		AlarmRaisedTime:   r.AlarmRaisedTime,
		AlarmChangedTime:  r.AlarmChangedTime,
		AlarmClearedTime:  r.AlarmClearedTime,
		AlarmType:         mns.AlarmType(r.AlarmType),
		ProbableCause:     mns.ProbableCause(r.ProbableCause),
		SpecificProblem:   mns.SpecificProblem(r.SpecificProblem),
		PerceivedSeverity: mns.PerceivedSeverity(r.PerceivedSeverity),
		AdditionalText:    r.AdditionalText,
		AckTime:           r.AckTime,
		AckUserId:         r.AckUserID,
		AckSystemId:       r.AckSystemID,
		AckState:          mns.AckState(r.AckState),
	}, nil
}

// AlarmList reads the alarm list of every node and merges them into the
// AlarmList of the SubNetwork (TS 28.111 7.3.2). It is ErrUnavailable
// for an OAM without the 3GPP management services.
//
// A node that does not answer, or whose list the OAM cannot use, adds
// none of its records: the OAM has no copy to show. The part of the list
// that it would hold is unreliable (TS 28.111 6.11). So is the part of a
// node that answers this request, but that the instance that watches it
// for the notifications has found not to answer (RunNotifications),
// until the notification that it is back has been sent.
// unreliableAlarmScope names those parts: the DN of its ManagedElement for one
// such node, the SubNetwork for more.
//
// SIMPLIFIED: TS 28.111 V20.0.0 6.11 and 6.12. unreliableAlarmScope is
// one DN in the schema AlarmList-Single of TS28111_FaultNrm.yaml,
// although 7.4.1 lets it hold several. When two or more nodes do not
// answer, it names the whole list, the SubNetwork, which 6.11 allows;
// then the attribute no longer names the node that each notification
// about the reliability of a part of the list (6.12) is about.
func (s *Server) AlarmList(ctx context.Context) (mns.AlarmListSingle, error) {
	nb := s.mns
	if nb == nil {
		return mns.AlarmListSingle{}, fmt.Errorf("%w: no SubNetwork for the 3GPP management services", ErrUnavailable)
	}
	lists := fanOut(ctx, s, s.nodes, func(ctx context.Context, c *maintenance.Client, e Endpoint) (nodeAlarmList, error) {
		l, err := readAlarmList(ctx, c, maintenance.AlarmFilter{})
		if err != nil {
			return nodeAlarmList{}, err
		}
		n := nodeAlarmList{info: l, records: make(map[string]mns.AlarmRecord, len(l.AlarmRecords))}
		for _, r := range l.AlarmRecords {
			rec, err := nb.alarmRecord(e.Name, r)
			if err != nil {
				return nodeAlarmList{}, fmt.Errorf("the node's alarm list: %w", err)
			}
			n.records[alarmRecordKey(e.Name, r.AlarmID)] = rec
		}
		return n, nil
	})
	attrs := &mns.AlarmListAttributes{OperationalState: mns.Enabled, AlarmRecords: map[string]mns.AlarmRecord{}}
	var unreliable []string
	for _, n := range lists.Nodes {
		if n.Result == nil {
			unreliable = append(unreliable, n.Node)
			continue
		}
		if s.watchedUnreachable(ctx, n.Node) {
			unreliable = append(unreliable, n.Node)
		}
		attrs.NumOfAlarmRecords += n.Result.info.NumOfAlarmRecords
		if n.Result.info.LastModification.After(attrs.LastModification) {
			attrs.LastModification = n.Result.info.LastModification
		}
		maps.Copy(attrs.AlarmRecords, n.Result.records)
	}
	switch len(unreliable) {
	case 0:
	case 1:
		attrs.UnreliableAlarmScope = nb.managedElement(unreliable[0]).String()
	default:
		attrs.UnreliableAlarmScope = nb.subNetwork.String()
	}
	if lists.Succeeded == 0 {
		attrs.LastModification = s.started
	}
	return mns.AlarmListSingle{
		Top:        mns.Top{Id: alarmListID, ObjectClass: mns.ClassAlarmList, ObjectInstance: nb.alarmList.String()},
		Attributes: attrs,
	}, nil
}

// watchedUnreachable says whether the watch state of node says that it
// does not answer. A state that cannot be read says nothing.
func (s *Server) watchedUnreachable(ctx context.Context, node string) bool {
	if s.watches == nil {
		return false
	}
	st, err := s.watches.AlarmWatchState(ctx, node)
	if err != nil {
		s.log.Warn("the watch state of a node could not be read", "node", node, "error", err.Error())
		return false
	}
	return st.Unreachable
}

func (s *Server) serveAlarmList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.getAlarmList(w, r)
	case http.MethodPatch:
		s.patchAlarmList(w, r)
	case http.MethodPut:
		// AlarmList=1 always exists, so a PUT would replace it, and
		// every attribute of the list is read-only (TS 32.158
		// 6.6.5.3.2).
		attributeError(mns.ModificationNotAllowed, mns.AttributeNotWritable,
			"The alarm list cannot be replaced", pointer("attributes")).write(w)
	case http.MethodDelete:
		// The producer alone creates and deletes the list (TS 28.111
		// 7.3.2.1, TS 32.158 6.6.5.4).
		attributeError(mns.ModificationNotAllowed, mns.ObjectDeletionNotAllowed,
			"The alarm list cannot be deleted").write(w)
	default:
		// Allow lists the methods that the list supports (RFC 9110
		// 10.2.1), and so not PUT and DELETE, which it always refuses.
		w.Header().Set("Allow", "GET, HEAD, PATCH")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// The query parameters of a GET of the alarm list
// (TS28532_ProvMnS.yaml).
const (
	scopeParameter            = "scope"
	filterParameter           = "filter"
	attributesParameter       = "attributes"
	fieldsParameter           = "fields"
	dataNodeSelectorParameter = "dataNodeSelector"
)

// The values of the schema ScopeType of TS28532_ProvMnS.yaml that
// package mns leaves out. A scope with one of them is valid, and asks
// for more than the alarm list, which contains no objects, needs.
var otherScopeTypes = []mns.ScopeType{"BASE_NTH_LEVEL", "BASE_SUBTREE"}

// alarmListQuery is what a GET of the alarm list asks for.
type alarmListQuery struct {
	// selectors are the filter and the dataNodeSelector; a record is
	// kept when every one of them picks it.
	selectors []*jex.AlarmRecordSelector
	// fields, when set, keeps the records it names and drops every other
	// attribute (TS 32.158 6.2.3).
	fields map[string]bool
}

// parseAlarmListQuery reads the query of a GET of the alarm list before
// any node is asked. A problem that the consumer can mend (400) is
// answered before one that the producer cannot handle (500): TS 32.158
// 6.6.4 gives SERVER_LIMITATION only to a request that is well formed
// and understood, which a query with a 400 problem is not.
//
// filter and dataNodeSelector take the same form, that of TS 28.111
// A.1.4, which picks alarm records. TS 32.158 6.7 names
// dataNodeSelector the parameter that picks attribute elements such as
// the records, and filter the one that picks objects; A.1.4 picks the
// records with filter. Both are taken, for a consumer of either.
//
// SIMPLIFIED: TS 32.158 V20.1.0 6.1.2 and 6.2.2. Of the scopes, only
// BASE_ONLY is taken, since the alarm list contains no objects, and
// attributes, which selects whole attributes, is not taken; either
// answers 500 with QUERY_PARAMS_TOO_COMPLEX. fields picks alarm records
// only, by their ids. The two query parameters scopeType and scopeLevel
// of 6.1.2 are unknown names: TS28532_ProvMnS.yaml carries the scope as
// one parameter, scope, whose value is a JSON object.
func parseAlarmListQuery(rawQuery string) (alarmListQuery, *mnsError) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return alarmListQuery{}, queryError(mns.ValidationError, mns.QueryMalformed, "The query is malformed")
	}
	var unknown []string
	for name := range values {
		switch name {
		case scopeParameter, filterParameter, attributesParameter, fieldsParameter, dataNodeSelectorParameter:
		default:
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return alarmListQuery{}, queryError(mns.ValidationError, mns.QueryParamNamesInvalid, "Unknown query parameters", unknown...)
	}
	invalid := func(name string) *mnsError {
		return queryError(mns.ValidationError, mns.QueryParamValuesInvalid, "Invalid values of query parameters", name)
	}
	tooComplex := func(name string) *mnsError {
		return queryError(mns.ServerLimitation, mns.QueryParamsTooComplex, "Query parameters beyond what the producer evaluates", name)
	}
	// jexError tells a Jex expression that breaks the grammar from one
	// that the package does not evaluate (package jex).
	jexError := func(name string, err error) *mnsError {
		var syntax *jex.SyntaxError
		if errors.As(err, &syntax) {
			return invalid(name)
		}
		return tooComplex(name)
	}
	var q alarmListQuery
	var limit *mnsError
	for _, name := range []string{scopeParameter, filterParameter, dataNodeSelectorParameter, fieldsParameter, attributesParameter} {
		vs, ok := values[name]
		if !ok {
			continue
		}
		if len(vs) != 1 {
			return alarmListQuery{}, invalid(name)
		}
		v := vs[0]
		var problem *mnsError
		switch name {
		case scopeParameter:
			problem = checkScope(v, invalid, tooComplex)
		case filterParameter, dataNodeSelectorParameter:
			sel, err := jex.ParseAlarmRecordSelector(v)
			if err != nil {
				problem = jexError(name, err)
			} else {
				q.selectors = append(q.selectors, sel)
			}
		case fieldsParameter:
			ids, err := jex.ParseAlarmRecordFields(v)
			if err != nil {
				problem = jexError(name, err)
			} else {
				q.fields = map[string]bool{}
				for _, id := range ids {
					q.fields[id] = true
				}
			}
		case attributesParameter:
			problem = tooComplex(name)
		}
		switch {
		case problem == nil:
		case problem.status < http.StatusInternalServerError:
			return alarmListQuery{}, problem
		case limit == nil:
			limit = problem
		}
	}
	if limit != nil {
		return alarmListQuery{}, limit
	}
	return q, nil
}

// checkScope reads the scope query parameter: the JSON object of the
// schema Scope of TS28532_ProvMnS.yaml. No scopeType is BASE_ONLY (TS
// 32.158 6.1.2), which also ignores scopeLevel.
func checkScope(v string, invalid, tooComplex func(string) *mnsError) *mnsError {
	dec := json.NewDecoder(strings.NewReader(v))
	dec.DisallowUnknownFields()
	var scope mns.Scope
	if err := dec.Decode(&scope); err != nil {
		return invalid(scopeParameter)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return invalid(scopeParameter)
	}
	switch {
	case scope.ScopeType == "" || scope.ScopeType == mns.BaseOnly:
		return nil
	case scope.ScopeType == mns.BaseAll || slices.Contains(otherScopeTypes, scope.ScopeType):
		return tooComplex(scopeParameter)
	}
	return invalid(scopeParameter)
}

// alarmRecordsOnly is the answer to a GET with fields: the identifiers
// of the list and the records that fields named, with no other
// attribute (TS 32.158 6.2.3).
type alarmRecordsOnly struct {
	mns.Top
	Attributes struct {
		AlarmRecords map[string]mns.AlarmRecord `json:"alarmRecords,omitempty"`
	} `json:"attributes"`
}

// apply keeps the records of list that the query picks, and answers
// what the GET returns.
//
// A query that picks no record is no error (TS 32.158 6.6.5.2): the
// answer is the list without records. TS 32.158 6.2.3 would answer 404
// when fields names nothing that the list holds; the list itself still
// exists, so it is answered with its identifiers and no record.
func (q alarmListQuery) apply(list mns.AlarmListSingle) (any, error) {
	records := list.Attributes.AlarmRecords
	if len(q.selectors) > 0 {
		doc, err := jex.Document(list)
		if err != nil {
			return nil, err
		}
		picked := maps.Clone(records)
		for _, sel := range q.selectors {
			kept := map[string]mns.AlarmRecord{}
			for _, id := range sel.Select(doc) {
				if r, ok := picked[id]; ok {
					kept[id] = r
				}
			}
			picked = kept
		}
		records = picked
	}
	if q.fields == nil {
		attrs := *list.Attributes
		attrs.AlarmRecords = records
		list.Attributes = &attrs
		return list, nil
	}
	answer := alarmRecordsOnly{Top: list.Top}
	answer.Attributes.AlarmRecords = map[string]mns.AlarmRecord{}
	for id, r := range records {
		if q.fields[id] {
			answer.Attributes.AlarmRecords[id] = r
		}
	}
	return answer, nil
}

// getAlarmList is getMOIAttributes of the alarm list (TS 28.532
// 11.1.1.2, 12.1.1.1.3). The query is checked first, so that a bad one
// is one answer and asks no node. The filters are evaluated on the
// records of every node, which the number of nodes and the bound of
// each node's list limit; the subscribers do not.
func (s *Server) getAlarmList(w http.ResponseWriter, r *http.Request) {
	q, problem := parseAlarmListQuery(r.URL.RawQuery)
	if problem != nil {
		problem.write(w)
		return
	}
	list, err := s.AlarmList(r.Context())
	if err == nil {
		var answer any
		if answer, err = q.apply(list); err == nil {
			writeJSON(w, http.StatusOK, answer)
			return
		}
	}
	s.log.Error("the alarm list could not be answered", "error", err.Error())
	w.WriteHeader(http.StatusInternalServerError)
}

// The attributes of an alarm record that a consumer may write (the
// isWritable of TS 28.111 7.3.1.2): the acknowledgement.
const (
	ackStateAttribute    = "ackState"
	ackUserIdAttribute   = "ackUserId"
	ackSystemIdAttribute = "ackSystemId"
)

// mergePatch is the media type of a JSON Merge Patch (RFC 7396 4, TS
// 32.158 6.3.2).
const mergePatch = "application/merge-patch+json"

// patchAlarmList is modifyMOIAttributes of the alarm list with a JSON
// Merge Patch (TS 28.532 11.1.1.3, 12.1.1.1.4.2; TS 32.158 6.3.2),
// which acknowledges alarm records or takes their acknowledgement back
// (TS 28.111 6.7). The OAM checks the whole body, and which node each
// record is of, before it asks any node; the node then checks each of
// its records under one hold of the lock of its list before it changes
// any (maintenance.Node.SetAckStates). The patch is thus applied whole
// or not at all, as TS 32.158 6.3.1 requires.
//
// SIMPLIFIED: TS 32.158 V20.1.0 6.3.1. A patch that names the records
// of two or more nodes is refused with 500 and SERVER_LIMITATION, after
// the whole body is checked and before any node is asked, so that no
// list changes. Applying it as a whole would take a two-phase commit
// between the nodes; the consumer sends one patch per node instead.
//
// SIMPLIFIED: TS 28.111 V20.0.0 A.1.4. Of the three forms of patch that
// A.1.4 shows for an acknowledgement, only the JSON Merge Patch is
// taken; JSON Patch and the two forms of 3GPP are answered with 415 and
// Accept-Patch (RFC 5789 2.2). The media type of the 3GPP forms is
// spelt differently in TS 28.532 and TS 32.158, and an acknowledgement
// needs no more than the Merge Patch.
func (s *Server) patchAlarmList(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		// TS 32.158 6.3.2: the target URI of a patch has no query.
		attributeError(mns.ValidationError, "", "A patch has no query").write(w)
		return
	}
	if t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || t != mergePatch {
		w.Header().Set("Accept-Patch", mergePatch)
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		attributeError(mns.ValidationError, "", "The body cannot be read").write(w)
		return
	}
	nodes, problem := s.readAckPatch(body)
	if problem != nil {
		problem.write(w)
		return
	}
	if len(nodes) > 1 {
		attributeError(mns.ServerLimitation, "", "A patch of the records of more than one node").write(w)
		return
	}
	for node, acks := range nodes {
		if problem := s.ackAlarmRecords(r.Context(), node, acks); problem != nil {
			problem.write(w)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// readAckPatch checks a JSON Merge Patch of the alarm list, and returns
// the acknowledgements it asks for, by the node of the inventory and
// then the alarm_id of the node. It answers the first problem it finds,
// in the order of the names; TS 32.158 6.6.3.1 lets a producer stop
// there.
//
// The patch merges into the resource (RFC 7396 2): only the three
// attributes of the acknowledgement of a record may be written. null
// removes a member, and nothing here may be removed: the system alone
// removes a record (TS 28.111 6.5), and an acknowledgement cannot be
// emptied.
func (s *Server) readAckPatch(body []byte) (map[string]map[string]maintenance.AckStateRequest, *mnsError) {
	notWritable := func(tokens ...string) *mnsError {
		return attributeError(mns.ModificationNotAllowed, mns.AttributeNotWritable, "Attributes that the consumer cannot write", pointer(tokens...))
	}
	invalidValue := func(tokens ...string) *mnsError {
		return attributeError(mns.ValidationError, mns.NewAttributeValueInvalid, "Invalid values of attributes", pointer(tokens...))
	}
	invariant := func(token string) *mnsError {
		return attributeError(mns.ModificationNotAllowed, mns.AttributeInvariant, "Attributes that never change", pointer(token))
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil || top == nil {
		return nil, attributeError(mns.ValidationError, "", "The body is no JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, attributeError(mns.ValidationError, "", "The body is no JSON object")
	}
	// TS 32.158 6.3.2: the id is present and that of the target, or the
	// patch would change the id.
	id, ok := top["id"]
	if !ok {
		return nil, attributeError(mns.ValidationError, "", "A patch without the id", pointer("id"))
	}
	if id != alarmListID {
		return nil, invariant("id")
	}
	identifiers := map[string]string{"objectClass": mns.ClassAlarmList, "objectInstance": s.mns.alarmList.String()}
	nodes := map[string]map[string]maintenance.AckStateRequest{}
	for _, name := range slices.Sorted(maps.Keys(top)) {
		v := top[name]
		switch {
		case name == "id":
		case identifiers[name] != "":
			if v != identifiers[name] {
				return nil, invariant(name)
			}
		case name != "attributes":
			return nil, notWritable(name)
		case v == nil:
			return nil, notWritable(name)
		default:
			attrs, ok := v.(map[string]any)
			if !ok {
				return nil, invalidValue(name)
			}
			if problem := s.readAckAttributes(attrs, nodes, notWritable, invalidValue); problem != nil {
				return nil, problem
			}
		}
	}
	return nodes, nil
}

// readAckAttributes reads the attributes member of an acknowledgement
// patch into nodes.
func (s *Server) readAckAttributes(attrs map[string]any, nodes map[string]map[string]maintenance.AckStateRequest, notWritable, invalidValue func(...string) *mnsError) *mnsError {
	for _, name := range slices.Sorted(maps.Keys(attrs)) {
		if name != "alarmRecords" || attrs[name] == nil {
			return notWritable("attributes", name)
		}
		records, ok := attrs[name].(map[string]any)
		if !ok {
			return invalidValue("attributes", name)
		}
		for _, key := range slices.Sorted(maps.Keys(records)) {
			at := []string{"attributes", name, key}
			node, alarmID, _ := strings.Cut(key, ".")
			if _, known := s.byName[node]; !known || alarmID == "" {
				// The type is that of a change whose target does not exist
				// (TS 32.158 6.6.4); 6.6.5.3.2 gives the reason to a
				// removal, and 6.6.5.1 leaves the list of reasons open.
				return attributeError(mns.IENotFound, mns.AttributeNotFound, "Alarm records that are not in the list", pointer(at...))
			}
			if records[key] == nil {
				return notWritable(at...)
			}
			record, ok := records[key].(map[string]any)
			if !ok {
				return invalidValue(at...)
			}
			req, problem := readAck(record, at, notWritable, invalidValue)
			if problem != nil {
				return problem
			}
			if nodes[node] == nil {
				nodes[node] = map[string]maintenance.AckStateRequest{}
			}
			nodes[node][alarmID] = req
		}
	}
	return nil
}

// readAck reads the acknowledgement of one record of a patch. ackUserId
// is mandatory, as the schema NotifyAckStateChanged of
// TS28111_FaultNrm.yaml requires it, although TS 28.111 6.7 lets a
// consumer leave its identity out; ackSystemId is not. ackState is
// mandatory too: the node changes the state, and the two others with
// it.
func readAck(record map[string]any, at []string, notWritable, invalidValue func(...string) *mnsError) (maintenance.AckStateRequest, *mnsError) {
	in := func(name string) []string { return append(slices.Clone(at), name) }
	var req maintenance.AckStateRequest
	for _, name := range slices.Sorted(maps.Keys(record)) {
		v := record[name]
		switch name {
		case ackStateAttribute, ackUserIdAttribute, ackSystemIdAttribute:
		default:
			return req, notWritable(in(name)...)
		}
		if v == nil {
			return req, notWritable(in(name)...)
		}
		text, ok := v.(string)
		if !ok {
			return req, invalidValue(in(name)...)
		}
		switch name {
		case ackStateAttribute:
			req.AckState = maintenance.AckState(text)
		case ackUserIdAttribute:
			req.Operator = text
		case ackSystemIdAttribute:
			req.AckSystemID = text
		}
	}
	if req.AckState != maintenance.Acknowledged && req.AckState != maintenance.Unacknowledged {
		return req, invalidValue(in(ackStateAttribute)...)
	}
	if req.Operator == "" {
		return req, invalidValue(in(ackUserIdAttribute)...)
	}
	return req, nil
}

// ackAlarmRecords hands the acknowledgements of one node to the node,
// and answers the node's refusal as a problem of the patch: a record
// that the node does not have is IE_NOT_FOUND, a value that it refuses
// NEW_ATTRIBUTE_VALUE_INVALID. A node that does not answer, or fails,
// is 502, without a body: the OAM is the gateway to the node, which
// gave it no answer that it could use (TS 32.158 6.6.2).
func (s *Server) ackAlarmRecords(ctx context.Context, node string, acks map[string]maintenance.AckStateRequest) *mnsError {
	e := s.byName[node]
	ids := slices.Sorted(maps.Keys(acks))
	for _, id := range ids {
		s.log.Info("alarm acknowledgement requested", "node", node, "alarm_id", id,
			"ack_state", string(acks[id].AckState), "operator", acks[id].Operator, "ack_system_id", acks[id].AckSystemID)
	}
	f := fanOut(ctx, s, []Endpoint{e}, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.AckStatesChange, error) {
		return c.SetAckStates(ctx, maintenance.AckStatesRequest{AckStates: acks})
	})
	failed := f.Nodes[0].Error
	switch {
	case failed == nil:
		return nil
	case failed.StatusCode == http.StatusNotFound:
		var pointers []string
		for _, id := range s.missingAlarms(ctx, e, ids) {
			pointers = append(pointers, pointer("attributes", "alarmRecords", alarmRecordKey(node, id)))
		}
		return attributeError(mns.IENotFound, mns.AttributeNotFound, "Alarm records that are not in the list", pointers...)
	case failed.StatusCode == http.StatusBadRequest:
		return attributeError(mns.ValidationError, mns.NewAttributeValueInvalid, "Invalid values of attributes")
	}
	s.log.Warn("alarm acknowledgement failed", "node", node, "error", failed.Error)
	return &mnsError{status: http.StatusBadGateway}
}

// missingAlarms is those of ids that the list of the node e does not
// hold, which the node refused a patch for. The node names only the
// first in its refusal, so the OAM reads the list again. Should that
// fail, or find them all, as when another patch acknowledged a record
// in between, every id is named.
func (s *Server) missingAlarms(ctx context.Context, e Endpoint, ids []string) []string {
	f := fanOut(ctx, s, []Endpoint{e}, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.AlarmList, error) {
		return c.Alarms(ctx, maintenance.AlarmFilter{})
	})
	if f.Nodes[0].Result == nil {
		return ids
	}
	held := map[string]bool{}
	for _, r := range f.Nodes[0].Result.AlarmRecords {
		held[r.AlarmID] = true
	}
	var missing []string
	for _, id := range ids {
		if !held[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return ids
	}
	return missing
}
