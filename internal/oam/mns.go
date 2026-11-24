package oam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// northbound is what the OAM needs to offer the 3GPP management
// services to an OSS (package mns): the {MnSRoot} that the URIs start
// with, and the DNs of the objects it serves.
type northbound struct {
	root mns.MnSRoot
	// subNetwork is the DN of the SubNetwork, the root of the object
	// tree, which is also the systemDN of the notifications.
	subNetwork mns.DN
	alarmList  mns.DN
}

// alarmListID is the id of the one AlarmList of the SubNetwork. The
// producer makes it, and a consumer can neither create nor delete it
// (TS 28.111 7.3.2.1).
const alarmListID = "1"

func newNorthbound(subNetwork, root string, nodes []Endpoint) (*northbound, error) {
	if subNetwork == "" || root == "" {
		return nil, errors.New("oam: the 3GPP management services need both the SubNetwork name and the MnSRoot")
	}
	r, err := mns.ParseMnSRoot(root)
	if err != nil {
		return nil, fmt.Errorf("oam: %w", err)
	}
	nb := &northbound{root: r, subNetwork: mns.SubNetworkDN(subNetwork)}
	nb.alarmList = nb.subNetwork.Child(mns.ClassAlarmList, alarmListID)
	// The names come from the configuration, so the DNs built from them
	// are checked here, once, and not with each request.
	if err := nb.alarmList.Check(); err != nil {
		return nil, fmt.Errorf("oam: the SubNetwork name: %w", err)
	}
	for _, e := range nodes {
		if err := nb.managedElement(e.Name).Check(); err != nil {
			return nil, fmt.Errorf("oam: node %s: %w", e.Name, err)
		}
	}
	return nb, nil
}

// managedElement is the DN of the ManagedElement that stands for the
// node of the inventory named node.
func (nb *northbound) managedElement(node string) mns.DN {
	return nb.subNetwork.Child(mns.ClassManagedElement, node)
}

// MnSPathPrefix is the path under which MnSHandler serves: the {root}
// of Config.MnSRoot, then /ProvMnS/v19/, percent-encoded as a request
// sends it. It is empty for an OAM without the 3GPP management
// services.
func (s *Server) MnSPathPrefix() string {
	if s.mns == nil {
		return ""
	}
	return s.mns.root.ResourcePath(nil) + "/"
}

// MnSHandler serves the 3GPP management services under MnSPathPrefix,
// at the paths of the package documentation. A role mounts it there,
// beside Handler; nil for an OAM without them.
//
// The answers follow TS 32.158. An error with a type of 6.6.4 has the
// body of 6.6.3 (mns.ErrorResponseGet or mns.ErrorResponseDefault);
// one without, such as 404 for a path that names no object here, 405
// with Allow, and 502 for a node that did not answer, has no body,
// since its status says all there is (6.6.4).
//
// As with Handler, an operation runs to its end once its request is
// read, whatever the client does with its side of the connection.
func (s *Server) MnSHandler() http.Handler {
	if s.mns == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithoutCancel(r.Context()))
		d, err := s.mns.root.ParseResourcePath(r.URL.EscapedPath())
		switch {
		case err == nil && slices.Equal(d, s.mns.alarmList):
			s.serveAlarmList(w, r)
		case err == nil && len(d) == 2 && d[0] == s.mns.subNetwork[0] && d[1].ClassName == mns.ClassNtfSubscriptionControl:
			s.serveNtfSubscription(w, r, d[1].Id)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// mnsError is an error answer of TS 32.158 6.6: the status and the body
// of 6.6.3, an mns.ErrorResponseGet or an mns.ErrorResponseDefault, or
// no body for a status that 6.6.4 gives no type.
type mnsError struct {
	status int
	body   any
}

// statusOf is the status code that TS 32.158 6.6.4 gives each type.
var statusOf = map[mns.ErrorType]int{
	mns.ValidationError:        http.StatusBadRequest,
	mns.IENotFound:             http.StatusBadRequest,
	mns.ModificationNotAllowed: http.StatusForbidden,
	mns.ServerLimitation:       http.StatusInternalServerError,
	mns.ServiceDisabled:        http.StatusServiceUnavailable,
}

// queryError is the answer to a GET whose query parameters params are
// at fault. The title names the kind of problem and never its values,
// which badQueryParams carries (6.6.3.1).
func queryError(t mns.ErrorType, reason mns.ErrorReason, title string, params ...string) *mnsError {
	return &mnsError{status: statusOf[t], body: mns.ErrorResponseGet{Type: t, Reason: reason, Title: title, BadQueryParams: params}}
}

// attributeError is the answer to a JSON Merge Patch or a PUT whose
// attributes at the pointers are at fault.
func attributeError(t mns.ErrorType, reason mns.ErrorReason, title string, pointers ...string) *mnsError {
	return &mnsError{status: statusOf[t], body: mns.ErrorResponseDefault{Type: t, Reason: reason, Title: title, BadAttributes: pointers}}
}

func (e *mnsError) write(w http.ResponseWriter) {
	if e.body == nil {
		w.WriteHeader(e.status)
		return
	}
	writeJSON(w, e.status, e.body)
}

// pointer is the pointer of TS 32.158 6.6.3.3 to the attribute that the
// tokens name inside the resource, such as
// #/attributes/alarmRecords/<id>/ackState. Each token is escaped as RFC
// 6901 3 has it, so that an id with a slash stays one token.
func pointer(tokens ...string) string {
	escape := strings.NewReplacer("~", "~0", "/", "~1")
	var b strings.Builder
	b.WriteString("#")
	for _, t := range tokens {
		b.WriteString("/" + escape.Replace(t))
	}
	return b.String()
}
