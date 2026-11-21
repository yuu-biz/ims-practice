package fivegc

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/n5"
)

// This file is the PCF stub: the server side of Npcf_PolicyAuthorization
// (TS 29.514). It binds each application session context to the PDU
// session of the UE's address, turns the media of the context into PCC
// rules for the voice QoS flow, and tells the AF what happens to them.
//
// SIMPLIFIED: TS 29.513 8.4. The PCF does not register its PDU sessions
// with a BSF (TS 29.521); the AF is given the PCF's apiRoot.

// N5Handler serves Npcf_PolicyAuthorization to the AFs (TS 29.514 5.3):
// the Create, Update and Delete of an Individual Application Session
// Context. Serve it with n5.NewServer, whose cleartext HTTP/2 the API
// needs, at the apiRoot the AF is given. The notifications that a
// request calls for go out after the answer.
func (g *FiveGC) N5Handler() http.Handler {
	h := n5.NewHandler(pcf{g})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := &answering{apiRoot: "http://" + r.Host}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), answeringKey{}, a)))
		if len(a.after) == 0 {
			return
		}
		// TS 29.514 4.2.2.2: the PCF answers the AF, and then sees to the
		// PCC rules, which here may lead to notifications to the same AF.
		ctx := context.WithoutCancel(r.Context())
		go func() {
			for _, fx := range a.after {
				g.carryOut(ctx, fx)
			}
		}()
	})
}

// answering is what the PCF learns of the HTTP request it is answering:
// the apiRoot the AF reached, from which the handler of package n5
// builds the Location, and the effects to carry out after the answer.
type answering struct {
	apiRoot string
	after   []effects
}

type answeringKey struct{}

func answeringFrom(ctx context.Context) *answering {
	a, _ := ctx.Value(answeringKey{}).(*answering)
	return a
}

// pcf is the PCF behind the handler of package n5.
type pcf struct{ g *FiveGC }

var _ n5.PCF = pcf{}

// supportedFeatures are the features of TS 29.514 5.8 that the PCF
// supports. It answers with those the AF supports too (5.8).
var supportedFeatures = []n5.Feature{n5.IMSSBI, n5.PatchCorrection}

// errNotAuthorized: the PCF refuses the service information.
var errNotAuthorized = errors.New("fivegc: requested service not authorized")

// Create makes an Individual Application Session Context (TS 29.514
// 4.2.2.2): session binding by the UE's IPv4 address (TS 29.513 6.2),
// PCC rules from the media (TS 29.513 7.3.3), and the voice QoS flow
// for them. A context of media component 0 only is the subscription to
// the signalling path status (TS 29.514 4.2.6.7), whose flows the
// default QoS flow carries already.
func (p pcf) Create(ctx context.Context, c n5.AppSessionContext) (string, n5.AppSessionContext, error) {
	g := p.g
	a := answeringFrom(ctx)
	if a == nil {
		return "", n5.AppSessionContext{}, errors.New("fivegc: Create outside N5Handler")
	}
	req := *c.AscReqData
	if req.UeIpv4 == "" {
		// SIMPLIFIED: TS 29.513 6.2. The stub serves IPv4 PDU sessions, so
		// a context bound by an IPv6 address finds none.
		return "", n5.AppSessionContext{}, n5.NewProblem(n5.CausePDUSessionNotAvailable, "the stub serves IPv4 PDU sessions only")
	}
	addr, err := netip.ParseAddr(req.UeIpv4)
	if err != nil || !addr.Is4() {
		return "", n5.AppSessionContext{}, n5.NewProblem(n5.CauseInvalidMsgFormat, fmt.Sprintf("ueIpv4 %q is not an IPv4 address", req.UeIpv4))
	}
	rules, err := pccRules(req.MedComponents)
	if err != nil {
		return "", n5.AppSessionContext{}, problemOf(err)
	}
	id := newAppSessionID()
	s := AppSession{
		AppSessionID: id, ResourceURI: a.apiRoot + n5.AppSessionsPath + "/" + url.PathEscape(id),
		NotifURI: req.NotifUri, EvSubsc: req.EvSubsc, MedComponents: req.MedComponents, Rules: rules,
	}
	var fx effects
	_, err = g.store.UpdatePDUSession(addr, func(session *PDUSession) error {
		fx = effects{imsi: session.IMSI}
		session.AppSessions = append(session.AppSessions, s)
		return g.provision(session, s.AppSessionID, &fx)
	})
	switch {
	case errors.Is(err, ErrNoPDUSession):
		// TS 29.514 4.2.2.2: session binding failed. A UE that does not
		// come through the 5GC gets this, and the P-CSCF then serves it
		// without policy control.
		g.log.Info("no PDU session for the application session context", "ue_ipv4", req.UeIpv4)
		return "", n5.AppSessionContext{}, n5.NewProblem(n5.CausePDUSessionNotAvailable, "")
	case err != nil:
		return "", n5.AppSessionContext{}, problemOf(err)
	}
	g.store.PutBinding(id, Binding{Address: addr})
	g.log.Info("application session context created", "app_session_id", id, "ue_ipv4", req.UeIpv4, "pcc_rules", len(rules))
	a.after = append(a.after, fx)
	created := n5.AppSessionContext{AscReqData: &req, AscRespData: &n5.AppSessionContextRespData{SuppFeat: commonFeatures(req.SuppFeat)}}
	return id, created, nil
}

// Update applies a modification of the service information (TS 29.514
// 4.2.3.2) and answers 204.
//
// SIMPLIFIED: TS 29.514 4.2.3.2 and RFC 7396. A media component in the
// patch replaces the one of the same number as a whole, instead of
// having its attributes merged one by one; the P-CSCF of this project
// derives every component from the whole SDP each time.
func (p pcf) Update(ctx context.Context, appSessionID string, patch n5.AppSessionContextUpdateDataPatch) (*n5.AppSessionContext, error) {
	g := p.g
	a := answeringFrom(ctx)
	if a == nil {
		return nil, errors.New("fivegc: Update outside N5Handler")
	}
	binding, ok := g.store.Binding(appSessionID)
	if !ok || binding.Terminated {
		return nil, n5.NewProblem(n5.CauseApplicationSessionContextNotFound, "")
	}
	update := patch.AscReqData
	if update == nil {
		return nil, nil
	}
	var fx effects
	_, err := g.store.UpdatePDUSession(binding.Address, func(session *PDUSession) error {
		fx = effects{imsi: session.IMSI}
		i := slices.IndexFunc(session.AppSessions, func(s AppSession) bool { return s.AppSessionID == appSessionID })
		if i < 0 {
			return n5.NewProblem(n5.CauseApplicationSessionContextNotFound, "")
		}
		s := &session.AppSessions[i]
		if update.EvSubsc != nil {
			s.EvSubsc = update.EvSubsc
		}
		if len(update.MedComponents) > 0 {
			components := maps.Clone(s.MedComponents)
			if components == nil {
				components = map[string]n5.MediaComponent{}
			}
			maps.Copy(components, update.MedComponents)
			rules, err := pccRules(components)
			if err != nil {
				return err
			}
			s.MedComponents, s.Rules = components, rules
		}
		return g.provision(session, appSessionID, &fx)
	})
	switch {
	case errors.Is(err, ErrNoPDUSession):
		return nil, n5.NewProblem(n5.CauseApplicationSessionContextNotFound, "")
	case err != nil:
		return nil, problemOf(err)
	}
	a.after = append(a.after, fx)
	return nil, nil
}

// Delete ends an Individual Application Session Context: the answer
// goes first, then the resources of the context are freed (TS 29.514
// 4.2.4.2).
//
// SIMPLIFIED: TS 29.514 4.2.4.2. Events asked for in the Delete are not
// reported; the answer is always 204.
func (p pcf) Delete(ctx context.Context, appSessionID string, evSubsc *n5.EventsSubscReqData) (*n5.AppSessionContext, error) {
	g := p.g
	a := answeringFrom(ctx)
	if a == nil {
		return nil, errors.New("fivegc: Delete outside N5Handler")
	}
	binding, ok := g.store.Binding(appSessionID)
	if !ok {
		return nil, n5.NewProblem(n5.CauseApplicationSessionContextNotFound, "")
	}
	g.store.DeleteBinding(appSessionID)
	g.log.Info("application session context deleted", "app_session_id", appSessionID)
	if binding.Terminated {
		return nil, nil
	}
	var fx effects
	_, err := g.store.UpdatePDUSession(binding.Address, func(session *PDUSession) error {
		fx = effects{imsi: session.IMSI}
		session.AppSessions = slices.DeleteFunc(session.AppSessions, func(s AppSession) bool { return s.AppSessionID == appSessionID })
		return g.reconcileVoiceQoSFlow(session, &fx)
	})
	if err != nil && !errors.Is(err, ErrNoPDUSession) {
		g.log.Warn("could not free the resources of the context", "app_session_id", appSessionID, "error", err.Error())
	}
	a.after = append(a.after, fx)
	return nil, nil
}

// provision has the SMF carry the PCC rules of the session's contexts
// on the voice QoS flow. Rules added to a flow that the UE has already
// accepted have their resources at once, which the PCF reports to an
// AF that asked (TS 29.514 4.2.5.8).
func (g *FiveGC) provision(session *PDUSession, appSessionID string, fx *effects) error {
	if err := g.reconcileVoiceQoSFlow(session, fx); err != nil {
		return fmt.Errorf("%w: %v", errNotAuthorized, err)
	}
	if session.VoiceQoSFlow == nil || session.State != PDUSessionActive {
		return nil
	}
	for _, s := range session.AppSessions {
		if s.AppSessionID == appSessionID && len(s.Rules) > 0 && s.subscribed(n5.SuccessfulResourcesAllocation) {
			fx.toAF = append(fx.toAF, succeeded(s))
		}
	}
	return nil
}

// pccRules makes one PCC rule of each media component that has flows,
// in the order of the component numbers. The AF signalling of component
// 0 needs none: its flows go on the default QoS flow (TS 29.514
// 4.2.6.7).
//
// SIMPLIFIED: TS 29.513 Table 7.3.3-1. Only audio is authorised, with
// 5QI 1, which the table gives audio when no operator policy applies,
// and which its NOTE 14 asks for IMS services where SRVCC is enabled.
// The stub has no QoS flow for other media, and refuses them.
func pccRules(components map[string]n5.MediaComponent) ([]PCCRule, error) {
	list := slices.SortedFunc(maps.Values(components), func(a, b n5.MediaComponent) int { return cmp.Compare(a.MedCompN, b.MedCompN) })
	var rules []PCCRule
	for _, c := range list {
		if c.MedCompN == 0 || c.FStatus == n5.FlowStatusRemoved {
			continue
		}
		if c.MedType != n5.MediaTypeAudio {
			return nil, fmt.Errorf("%w: media component %d is %q, not audio", errNotAuthorized, c.MedCompN, c.MedType)
		}
		r := PCCRule{MedCompN: c.MedCompN, FiveQI: FiveQIConversationalVoice}
		subs := slices.SortedFunc(maps.Values(c.MedSubComps), func(a, b n5.MediaSubComponent) int { return cmp.Compare(a.FNum, b.FNum) })
		for _, sub := range subs {
			r.FNums = append(r.FNums, sub.FNum)
			var up, down bool
			for _, d := range sub.FDescs {
				f, err := parseFlowDescription(string(d))
				if err != nil {
					return nil, err
				}
				up, down = up || f.Direction == Uplink, down || f.Direction == Downlink
				r.Flows = append(r.Flows, f)
			}
			ul, dl, err := maxDataRates(c, sub.FlowUsage == n5.FlowUsageRTCP, up, down)
			if err != nil {
				return nil, err
			}
			r.MaxBitRateUL += ul
			r.MaxBitRateDL += dl
		}
		// TS 29.513 Table 7.3.3-1: with no minimum bandwidth from the AF
		// and no operator policy, the guaranteed rate is the maximum one.
		r.GuaranteedBitRateUL, r.GuaranteedBitRateDL = r.MaxBitRateUL, r.MaxBitRateDL
		rules = append(rules, r)
	}
	return rules, nil
}

// maxDataRates is the Maximum Authorized Data Rate of one media
// sub-component in bit/s, by TS 29.513 Table 7.3.3-1: the requested
// bandwidth of the component for the media, and the RTCP bandwidths
// for RTCP. A direction without a flow description gets 0.
//
// SIMPLIFIED: TS 29.513 Table 7.3.3-1. The branches the table leaves to
// operator policy, to specific algorithms for codecs and applications,
// and to a rate "as set by the operator" give 0 here.
func maxDataRates(c n5.MediaComponent, rtcp, up, down bool) (ul, dl uint64, err error) {
	rate := func(r n5.BitRate) (uint64, bool, error) {
		if r == "" {
			return 0, false, nil
		}
		bps, err := r.Bps()
		if err != nil {
			return 0, false, fmt.Errorf("%w: %v", errInvalidServiceInformation, err)
		}
		return bps, true, nil
	}
	rs, hasRS, err := rate(c.RsBw)
	if err != nil {
		return 0, 0, err
	}
	rr, hasRR, err := rate(c.RrBw)
	if err != nil {
		return 0, 0, err
	}
	direction := func(present bool, marBw n5.BitRate) (uint64, error) {
		if !present {
			return 0, nil
		}
		mar, hasMar, err := rate(marBw)
		if err != nil || !rtcp {
			return mar, err
		}
		switch {
		case hasRS && hasRR:
			return rs + rr, nil
		case !hasMar:
			return 0, nil
		case hasRS:
			return max(mar/20, rs), nil
		case hasRR:
			return max(mar/20, rr), nil
		}
		return mar / 20, nil // 5 % of the media's bandwidth
	}
	if ul, err = direction(up, c.MarBwUl); err != nil {
		return 0, 0, err
	}
	if dl, err = direction(down, c.MarBwDl); err != nil {
		return 0, 0, err
	}
	return ul, dl, nil
}

// errInvalidServiceInformation: the service information cannot be read,
// which the PCF answers with INVALID_SERVICE_INFORMATION (TS 29.514
// 4.2.2.2).
var errInvalidServiceInformation = errors.New("fivegc: invalid service information")

// problemOf turns a refusal of the service information into the answer
// TS 29.514 4.2.2.2 gives it.
func problemOf(err error) error {
	var p *n5.ProblemError
	switch {
	case errors.As(err, &p):
		return p
	case errors.Is(err, errFilterRestrictions):
		return n5.NewProblem(n5.CauseFilterRestrictions, err.Error())
	case errors.Is(err, errInvalidServiceInformation):
		return n5.NewProblem(n5.CauseInvalidServiceInformation, err.Error())
	case errors.Is(err, errNotAuthorized):
		return n5.NewProblem(n5.CauseRequestedServiceNotAuthorized, err.Error())
	}
	return err
}

// commonFeatures is the subset of the AF's features that the PCF
// supports (TS 29.514 5.8).
func commonFeatures(af n5.SupportedFeatures) n5.SupportedFeatures {
	var common []n5.Feature
	for _, f := range supportedFeatures {
		if af.Has(f) {
			common = append(common, f)
		}
	}
	return n5.NewSupportedFeatures(common...)
}

// newAppSessionID names a new context. It is random, so that several
// PCF instances never give the same name.
func newAppSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// evSubsURI is the Events Subscription of a context: its resource URI
// with "/events-subscription" (TS 29.514 4.2.2.2).
func evSubsURI(s AppSession) string { return s.ResourceURI + n5.EventsSubscriptionPath }

// eventsNotifURI is where the events of s go: evSubsc.notifUri (TS
// 29.514 4.2.5.2).
func eventsNotifURI(s AppSession) string {
	if s.EvSubsc == nil {
		return ""
	}
	return s.EvSubsc.NotifUri
}

// succeeded tells the AF of s that the resources of all its flows are
// allocated (TS 29.514 4.2.5.8). All are, as one QoS flow carries them,
// so the flows are not named.
func succeeded(s AppSession) afMessage {
	return afMessage{appSessionID: s.AppSessionID, notifURI: eventsNotifURI(s), notification: &n5.EventsNotification{
		EvSubsUri: evSubsURI(s),
		EvNotifs:  []n5.AfEventNotification{{Event: n5.SuccessfulResourcesAllocation}},
	}}
}

// failed tells the AF of s that the resources of its flows could not be
// allocated, with the PCC rules of its media components inactive (TS
// 29.514 4.2.5.8).
func failed(s AppSession) afMessage {
	return afMessage{appSessionID: s.AppSessionID, notifURI: eventsNotifURI(s), notification: &n5.EventsNotification{
		EvSubsUri:                 evSubsURI(s),
		EvNotifs:                  []n5.AfEventNotification{{Event: n5.FailedResourcesAllocation}},
		FailedResourcAllocReports: []n5.ResourcesAllocationInfo{{McResourcStatus: n5.MediaComponentResourcesInactive}},
	}}
}
