package ue

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/controlapi"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

// Service is a supplementary service that the UE switches on and off
// over Ut, by the name GSMA IR.92 2.3.8 and 2.3.9 give it, in lower
// case.
type Service string

const (
	CFU      Service = "cfu"
	CFB      Service = "cfb"
	CFNR     Service = "cfnr"
	CFNRc    Service = "cfnrc"
	CFNL     Service = "cfnl"
	BAIC     Service = "baic"
	BICRoam  Service = "bic-roam"
	ACR      Service = "acr"
	BAOC     Service = "baoc"
	BOIC     Service = "boic"
	BOICExHC Service = "boic-exhc"
)

// serviceElement names the service element of a simservs document whose
// rule set holds the rules of a Service.
type serviceElement string

const (
	diversion       serviceElement = "communication-diversion"        // TS 24.604 4.9.2
	incomingBarring serviceElement = "incoming-communication-barring" // TS 24.611 4.9.2
	outgoingBarring serviceElement = "outgoing-communication-barring"
)

// serviceRule is how a rule of a Service is told apart: by the service
// element it is in and the conditions it has besides
// <rule-deactivated/> (GSMA IR.92 2.3.2), not by its id, which the
// operator chooses.
type serviceRule struct {
	element    serviceElement
	conditions []string
}

var services = []struct {
	name Service
	rule serviceRule
}{
	{CFU, serviceRule{diversion, nil}}, // TS 24.604 4.9.1.3
	{CFB, serviceRule{diversion, []string{"busy"}}},
	{CFNR, serviceRule{diversion, []string{"no-answer"}}},
	{CFNRc, serviceRule{diversion, []string{"not-reachable"}}},
	{CFNL, serviceRule{diversion, []string{"not-registered"}}},
	{BAIC, serviceRule{incomingBarring, nil}}, // TS 24.611 4.9.1.3, 4.9.1.4
	{BICRoam, serviceRule{incomingBarring, []string{"roaming"}}},
	{ACR, serviceRule{incomingBarring, []string{"anonymous"}}},
	{BAOC, serviceRule{outgoingBarring, nil}},
	{BOIC, serviceRule{outgoingBarring, []string{"international"}}},
	{BOICExHC, serviceRule{outgoingBarring, []string{"international-exHC"}}},
}

func ruleOf(s Service) (serviceRule, bool) {
	for _, e := range services {
		if e.name == s {
			return e.rule, true
		}
	}
	return serviceRule{}, false
}

// conditionNames lists the conditions of a rule that tell the services
// apart, in the order of the fields of simservs.Conditions. Any other
// condition, such as media or an identity, makes the rule match no
// Service of the list.
func conditionNames(c *simservs.Conditions) []string {
	if c == nil {
		return nil
	}
	var names []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"identity", c.Identity != nil},
		{"anonymous", c.Anonymous != nil},
		{"media", len(c.Media) > 0},
		{"communication-diverted", c.CommunicationDiverted != nil},
		{"not-registered", c.NotRegistered != nil},
		{"busy", c.Busy != nil},
		{"no-answer", c.NoAnswer != nil},
		{"not-reachable", c.NotReachable != nil},
		{"roaming", c.Roaming != nil},
		{"international", c.International != nil},
		{"international-exHC", c.InternationalExHC != nil},
	} {
		if f.set {
			names = append(names, f.name)
		}
	}
	for range c.Unknown {
		names = append(names, "unknown")
	}
	return names
}

// ServiceSetting is the state of one Service in the user's document.
type ServiceSetting struct {
	Name Service `json:"name"`
	// Provisioned is false when the document has no rule for the
	// service: the operator does not offer it to this user (GSMA IR.92
	// 2.3.2).
	Provisioned bool `json:"provisioned"`
	// Active is true when the rule has no <rule-deactivated/> and its
	// service element is active (TS 24.623 6.1).
	Active bool `json:"active"`
	// Target is where a diversion goes. Empty means none is registered
	// (GSMA IR.92 2.3.8).
	Target string `json:"target,omitempty"`
}

// Settings is what the UE knows of its supplementary services after
// reading the simservs document.
type Settings struct {
	// ETag is the entity tag of the document the settings come from.
	ETag     string           `json:"etag"`
	Services []ServiceSetting `json:"services"`
	// OIRDefault is the default of OIR in the temporary mode (TS 24.607
	// 4.10.2), empty when the document has no OIR.
	OIRDefault simservs.DefaultBehaviour `json:"oir_default_behaviour,omitempty"`
	// NoReplyTimer is how many seconds a call may ring before CFNR
	// diverts it, 0 when the document has none and the operator's
	// default applies (TS 24.604 4.8.1, 4.9.1.1A).
	NoReplyTimer int `json:"no_reply_timer,omitempty"`
	// Document is the simservs document as the server sent it, for a
	// learner to read.
	Document string `json:"document"`
}

// ErrInvalidSetting is a change that cannot be asked for: a service
// that has no such name, a diversion without a target, a target that is
// not a URI.
var ErrInvalidSetting = errors.New("invalid setting")

// ErrNotProvisioned is a change of a service whose rule the document
// does not have. GSMA IR.92 2.3.2 has the UE then take the service as
// not provisioned and not insert a rule of its own.
var ErrNotProvisioned = errors.New("the service is not provisioned for the user")

// SupplementaryServices reads and changes the user's supplementary
// services over Ut with an XCAP client (TS 24.623 5.3.1, GSMA IR.92
// 2.3.2). It keeps no copy of the document: each change reads it first
// and sends its entity tag in If-Match.
type SupplementaryServices struct {
	client *xcap.Client
}

func NewSupplementaryServices(client *xcap.Client) *SupplementaryServices {
	return &SupplementaryServices{client: client}
}

// maxChangeAttempts bounds how often a change is tried again after a
// 412, when someone else changed the document in between (RFC 4825
// 7.11).
const maxChangeAttempts = 3

// document reads the whole simservs document.
func (s *SupplementaryServices) document(ctx context.Context) (simservs.Simservs, xcap.Response, error) {
	resp, err := s.client.Get(ctx, xcap.Selector{})
	if err != nil {
		return simservs.Simservs{}, resp, err
	}
	doc, err := simservs.ParseSimservs(resp.Body)
	return doc, resp, err
}

// Settings reads the document and tells the state of every Service.
func (s *SupplementaryServices) Settings(ctx context.Context) (Settings, error) {
	doc, resp, err := s.document(ctx)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{ETag: resp.ETag, Document: string(resp.Body)}
	for _, e := range services {
		setting := ServiceSetting{Name: e.name}
		if rule, active, ok := findRule(doc, e.rule); ok {
			setting.Provisioned = true
			setting.Active = active && !isDeactivated(rule)
			if rule.Actions != nil && rule.Actions.ForwardTo != nil {
				setting.Target = rule.Actions.ForwardTo.Target
			}
		}
		out.Services = append(out.Services, setting)
	}
	if oir := doc.OriginatingIdentityPresentationRestriction; oir != nil {
		out.OIRDefault = oir.DefaultBehaviourOrDefault()
	}
	if cd := doc.CommunicationDiversion; cd != nil && cd.NoReplyTimer != nil {
		out.NoReplyTimer = *cd.NoReplyTimer
	}
	return out, nil
}

// findRule finds the rule of a service in doc, with whether its service
// element is active.
func findRule(doc simservs.Simservs, r serviceRule) (simservs.Rule, bool, bool) {
	var ruleset *simservs.Ruleset
	var element simservs.SimservType
	switch {
	case r.element == diversion && doc.CommunicationDiversion != nil:
		ruleset, element = doc.CommunicationDiversion.Ruleset, doc.CommunicationDiversion.SimservType
	case r.element == incomingBarring && doc.IncomingCommunicationBarring != nil:
		ruleset, element = doc.IncomingCommunicationBarring.Ruleset, doc.IncomingCommunicationBarring.SimservType
	case r.element == outgoingBarring && doc.OutgoingCommunicationBarring != nil:
		ruleset, element = doc.OutgoingCommunicationBarring.Ruleset, doc.OutgoingCommunicationBarring.SimservType
	}
	if ruleset == nil {
		return simservs.Rule{}, false, false
	}
	for _, rule := range ruleset.Rules {
		if slices.Equal(conditionNames(rule.Conditions), r.conditions) {
			return rule, element.IsActive(), true
		}
	}
	return simservs.Rule{}, false, false
}

func isDeactivated(r simservs.Rule) bool {
	return r.Conditions != nil && r.Conditions.RuleDeactivated != nil
}

// SetService activates or deactivates one Service. Activating a
// diversion needs a target, a SIP or tel URI; deactivating keeps the
// target that is registered. As GSMA IR.92 2.3.2 asks, each request
// changes one thing: the active attribute of the service element if it
// is false, then the one rule, with If-Match.
func (s *SupplementaryServices) SetService(ctx context.Context, name Service, active bool, target string) (Settings, error) {
	r, ok := ruleOf(name)
	if !ok {
		return Settings{}, fmt.Errorf("%w: no service is named %q", ErrInvalidSetting, name)
	}
	if r.element == diversion && active && target == "" {
		return Settings{}, fmt.Errorf("%w: %s needs a target", ErrInvalidSetting, name)
	}
	if target != "" && !isCallTarget(target) {
		return Settings{}, fmt.Errorf("%w: target %q is neither a sip, sips nor tel URI", ErrInvalidSetting, target)
	}
	err := s.retry(ctx, func(doc simservs.Simservs, etag string) error {
		rule, elementActive, ok := findRule(doc, r)
		if !ok {
			return ErrNotProvisioned
		}
		if active && !elementActive {
			// The attribute is there and false: a missing one means
			// active (TS 24.623 6.1).
			resp, err := s.client.Put(ctx, xcap.Selector{Path: "simservs/" + string(r.element) + "/@active"}, []byte(`"true"`), etag)
			if err != nil {
				return err
			}
			etag = resp.ETag
		}
		changed := setRule(rule, active, target)
		sel := xcap.Selector{
			Path:       "simservs/" + string(r.element) + `/cp:ruleset/cp:rule[@id="` + rule.ID + `"]`,
			Namespaces: map[string]string{"cp": simservs.NamespaceCommonPolicy},
		}
		_, err := s.client.Put(ctx, sel, simservs.MarshalElement(changed), etag)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx)
}

// setRule returns rule with <rule-deactivated/> taken out or put in
// (GSMA IR.92 2.3.2) and, if target is set, the target of its
// diversion.
func setRule(rule simservs.Rule, active bool, target string) simservs.Rule {
	c := simservs.Conditions{}
	if rule.Conditions != nil {
		c = *rule.Conditions
	}
	if active {
		c.RuleDeactivated = nil
	} else {
		c.RuleDeactivated = &simservs.Empty{}
	}
	rule.Conditions = &c
	if target != "" && rule.Actions != nil && rule.Actions.ForwardTo != nil {
		a := *rule.Actions
		f := *a.ForwardTo
		f.Target = target
		a.ForwardTo = &f
		rule.Actions = &a
	}
	return rule
}

// SetOIRDefault sets the default of OIR in the temporary mode: whether
// the user's identity is restricted unless the UE asks otherwise in a
// call (TS 24.607 4.10.2). GSMA IR.92 2.3.6 does not ask the network to
// take this setting; the MMTel AS of this project reads it.
func (s *SupplementaryServices) SetOIRDefault(ctx context.Context, behaviour simservs.DefaultBehaviour) (Settings, error) {
	if behaviour != simservs.PresentationRestricted && behaviour != simservs.PresentationNotRestricted {
		return Settings{}, fmt.Errorf("%w: default behaviour %q is neither %s nor %s", ErrInvalidSetting, behaviour, simservs.PresentationRestricted, simservs.PresentationNotRestricted)
	}
	const element = "originating-identity-presentation-restriction"
	err := s.retry(ctx, func(doc simservs.Simservs, etag string) error {
		if doc.OriginatingIdentityPresentationRestriction == nil {
			return ErrNotProvisioned
		}
		body := []byte(`<default-behaviour xmlns="` + simservs.NamespaceSimservs + `">` + string(behaviour) + `</default-behaviour>`)
		_, err := s.client.Put(ctx, xcap.Selector{Path: "simservs/" + element + "/default-behaviour"}, body, etag)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx)
}

// SetNoReplyTimer sets how many seconds a call may ring before CFNR
// diverts it, from 5 to 180 (TS 24.604 4.9.1.1A, GSMA IR.92 2.3.8). The
// PUT replaces the NoReplyTimer element of the document. XCAP puts a
// new element after the last child (RFC 4825 8.2.3), and the schema
// wants this one before the rule set, so the operator provisions it.
func (s *SupplementaryServices) SetNoReplyTimer(ctx context.Context, seconds int) (Settings, error) {
	if seconds < simservs.MinNoReplyTimer || seconds > simservs.MaxNoReplyTimer {
		return Settings{}, fmt.Errorf("%w: a no reply timer of %d s is not between %d and %d", ErrInvalidSetting, seconds, simservs.MinNoReplyTimer, simservs.MaxNoReplyTimer)
	}
	err := s.retry(ctx, func(doc simservs.Simservs, etag string) error {
		if doc.CommunicationDiversion == nil || doc.CommunicationDiversion.NoReplyTimer == nil {
			return ErrNotProvisioned
		}
		body := []byte(`<NoReplyTimer xmlns="` + simservs.NamespaceSimservs + `">` + strconv.Itoa(seconds) + `</NoReplyTimer>`)
		_, err := s.client.Put(ctx, xcap.Selector{Path: "simservs/" + string(diversion) + "/NoReplyTimer"}, body, etag)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx)
}

// retry reads the document and runs change on it, and does both again
// when the server answers 412: the document changed since it was read,
// so the client reads it again and retries with the new entity tag
// (RFC 4825 7.11).
func (s *SupplementaryServices) retry(ctx context.Context, change func(doc simservs.Simservs, etag string) error) error {
	for attempt := 1; ; attempt++ {
		doc, resp, err := s.document(ctx)
		if err != nil {
			return err
		}
		err = change(doc, resp.ETag)
		var se *xcap.StatusError
		if errors.As(err, &se) && se.Status == http.StatusPreconditionFailed && attempt < maxChangeAttempts {
			continue
		}
		return err
	}
}

// Handler is the part of the control API of "ue serve" for the
// supplementary services. Like the rest of that API it is this
// project's own, and nothing of it goes to the IMS; each request makes
// the UE send its XCAP requests over Ut.
//
//	GET /xcap                  the Settings
//	PUT /xcap/services/{name}  {"active": bool, "target": URI}: activate
//	                           or deactivate a Service, such as cfu
//	PUT /xcap/oir              {"default_behaviour":
//	                           "presentation-restricted" or
//	                           "presentation-not-restricted"}
//
// The PUTs answer with the Settings after the change. A request that
// names no service, or a diversion without a target, gets 400; a service
// the operator did not provision gets 409; a refusal from the XCAP
// server, or no answer, gets 502 with the server's status and text in
// the error. A PUT whose Content-Type is not application/json gets 415,
// as on the phone's own API.
func (s *SupplementaryServices) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /xcap", func(w http.ResponseWriter, r *http.Request) {
		settings, err := s.Settings(r.Context())
		s.respond(w, settings, err)
	})
	mux.HandleFunc("PUT /xcap/services/{name}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Active bool   `json:"active"`
			Target string `json:"target"`
		}
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		name := Service(strings.ToLower(r.PathValue("name")))
		settings, err := s.SetService(r.Context(), name, req.Active, req.Target)
		s.respond(w, settings, err)
	})
	mux.HandleFunc("PUT /xcap/oir", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DefaultBehaviour simservs.DefaultBehaviour `json:"default_behaviour"`
		}
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		settings, err := s.SetOIRDefault(r.Context(), req.DefaultBehaviour)
		s.respond(w, settings, err)
	})
	return controlapi.RequireJSON(mux)
}

func (s *SupplementaryServices) respond(w http.ResponseWriter, settings Settings, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, settings)
	case errors.Is(err, ErrInvalidSetting):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, ErrNotProvisioned):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}
