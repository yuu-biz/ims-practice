package n5

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubPCF is a PCF for the tests: it keeps the contexts in a map and
// refuses UEs it has no PDU session for, as session binding would.
type stubPCF struct {
	mu       sync.Mutex
	next     int
	pdu      map[string]bool // UE addresses with a PDU session
	contexts map[string]AppSessionContextReqData
	deleted  []string
	err      error // returned by Create when set
}

func newStubPCF(ues ...string) *stubPCF {
	p := &stubPCF{pdu: map[string]bool{}, contexts: map[string]AppSessionContextReqData{}}
	for _, ue := range ues {
		p.pdu[ue] = true
	}
	return p
}

func (p *stubPCF) Create(_ context.Context, c AppSessionContext) (string, AppSessionContext, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return "", AppSessionContext{}, p.err
	}
	if !p.pdu[c.AscReqData.UeIpv4] {
		return "", AppSessionContext{}, NewProblem(CausePDUSessionNotAvailable, "no PDU session for "+c.AscReqData.UeIpv4)
	}
	p.next++
	id := strconv.Itoa(p.next)
	p.contexts[id] = *c.AscReqData
	c.AscRespData = &AppSessionContextRespData{SuppFeat: NewSupportedFeatures(IMSSBI)}
	return id, c, nil
}

func (p *stubPCF) Update(_ context.Context, id string, patch AppSessionContextUpdateDataPatch) (*AppSessionContext, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.contexts[id]
	if !ok {
		return nil, NewProblem(CauseApplicationSessionContextNotFound, "")
	}
	if u := patch.AscReqData; u != nil {
		// A JSON Merge Patch replaces what it holds (RFC 7396).
		if u.ServInfStatus != "" {
			c.ServInfStatus = u.ServInfStatus
		}
		for k, m := range u.MedComponents {
			c.MedComponents[k] = m
		}
	}
	p.contexts[id] = c
	return nil, nil
}

func (p *stubPCF) Delete(_ context.Context, id string, _ *EventsSubscReqData) (*AppSessionContext, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.contexts[id]; !ok {
		return nil, NewProblem(CauseApplicationSessionContextNotFound, "")
	}
	delete(p.contexts, id)
	p.deleted = append(p.deleted, id)
	return nil, nil
}

func voiceRequest(t *testing.T, notifUri string) AppSessionContextReqData {
	t.Helper()
	r := *roundTrip[AppSessionContext](t, voiceCreate).AscReqData
	r.NotifUri = notifUri
	r.EvSubsc.NotifUri = notifUri
	return r
}

// TestClientAndServer runs the AF client against the PCF skeleton: a
// call's context is created, put on hold and deleted.
func TestClientAndServer(t *testing.T) {
	pcf := newStubPCF("192.0.2.10")
	root := serveH2C(t, NewHandler(pcf))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	c := NewClient(root)

	s, err := c.Create(ctx, voiceRequest(t, "http://pcscf.ims.example/n5-callbacks/call-1"))
	if err != nil {
		t.Fatal(err)
	}
	if s.URI != root+"/npcf-policyauthorization/v1/app-sessions/1" {
		t.Errorf("URI %q", s.URI)
	}
	if s.Context.AscRespData == nil || s.Context.AscRespData.SuppFeat != "10" {
		t.Errorf("answer %+v", s.Context)
	}

	modified, err := c.Update(ctx, s.URI, AppSessionContextUpdateData{
		MedComponents: map[string]MediaComponentRm{"1": {MedCompN: 1, FStatus: FlowStatusDisabled}},
	})
	if err != nil || modified != nil {
		t.Fatalf("Update = %v, %v", modified, err)
	}
	pcf.mu.Lock()
	if got := pcf.contexts["1"].MedComponents["1"].FStatus; got != FlowStatusDisabled {
		t.Errorf("fStatus after the PATCH %q", got)
	}
	pcf.mu.Unlock()

	if _, err := c.Delete(ctx, s.URI, nil); err != nil {
		t.Fatal(err)
	}
	// Deleting again: the PCF no longer knows the context.
	_, err = c.Delete(ctx, s.URI, nil)
	var p *ProblemError
	if !errors.As(err, &p) || p.Status != 404 || p.Problem.Cause != CauseApplicationSessionContextNotFound {
		t.Errorf("second Delete: %v", err)
	}
}

func TestServerErrors(t *testing.T) {
	pcf := newStubPCF("192.0.2.10")
	root := serveH2C(t, NewHandler(pcf))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	c := NewClient(root)

	t.Run("session binding fails", func(t *testing.T) {
		r := voiceRequest(t, "http://af/n")
		r.UeIpv4 = "192.0.2.99"
		_, err := c.Create(ctx, r)
		var p *ProblemError
		if !errors.As(err, &p) || p.Status != 500 || p.Problem.Cause != CausePDUSessionNotAvailable || p.Problem.Status != 500 {
			t.Errorf("got %v", err)
		}
	})
	t.Run("mandatory attributes", func(t *testing.T) {
		_, err := c.Create(ctx, AppSessionContextReqData{})
		var p *ProblemError
		if !errors.As(err, &p) || p.Status != 400 || p.Problem.Cause != CauseMandatoryIEMissing {
			t.Fatalf("got %v", err)
		}
		var params []string
		for _, ip := range p.Problem.InvalidParams {
			params = append(params, ip.Param)
		}
		if got := strings.Join(params, " "); got != "/ascReqData/notifUri /ascReqData/suppFeat /ascReqData/ueIpv4" {
			t.Errorf("invalidParams %s", got)
		}
	})
	t.Run("no ascReqData", func(t *testing.T) {
		status, p := post(t, "POST", root+AppSessionsPath, "application/json", `{}`)
		if status != 400 || p.Cause != CauseMandatoryIEMissing || len(p.InvalidParams) != 1 || p.InvalidParams[0].Param != "/ascReqData" {
			t.Errorf("got %d %+v", status, p)
		}
	})
	t.Run("PATCH as plain JSON", func(t *testing.T) {
		status, _ := post(t, "PATCH", root+AppSessionsPath+"/1", "application/json", updatePatch)
		if status != http.StatusUnsupportedMediaType {
			t.Errorf("got %d", status)
		}
	})
	t.Run("PATCH of an unknown context", func(t *testing.T) {
		_, err := c.Update(ctx, root+AppSessionsPath+"/77", AppSessionContextUpdateData{ServInfStatus: ServiceInfoStatusFinal})
		var p *ProblemError
		if !errors.As(err, &p) || p.Status != 404 || p.Problem.Cause != CauseApplicationSessionContextNotFound {
			t.Errorf("got %v", err)
		}
	})
	t.Run("other errors of the PCF", func(t *testing.T) {
		pcf.mu.Lock()
		pcf.err = errors.New("database down")
		pcf.mu.Unlock()
		defer func() { pcf.mu.Lock(); pcf.err = nil; pcf.mu.Unlock() }()
		_, err := c.Create(ctx, voiceRequest(t, "http://af/n"))
		var p *ProblemError
		if !errors.As(err, &p) || p.Status != 500 || p.Problem.Cause != CauseSystemFailure {
			t.Errorf("got %v", err)
		}
	})
}

// terminatingAF deletes a context after it has acknowledged the
// termination request, as TS 29.514 4.2.5.3 has the AF do.
type terminatingAF struct {
	recordingAF
	client  *Client
	deleted chan error
}

func (a *terminatingAF) Terminate(ctx context.Context, t TerminationInfo) error {
	a.recordingAF.Terminate(ctx, t)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := a.client.Delete(ctx, t.ResUri, nil)
		a.deleted <- err
	}()
	return nil
}

// TestNotificationFlow runs the whole N5 exchange of a call that loses
// its resources: Create, a notification of FAILED_RESOURCES_ALLOCATION,
// a termination request, and the Delete that answers it.
func TestNotificationFlow(t *testing.T) {
	pcf := newStubPCF("192.0.2.10")
	pcfRoot := serveH2C(t, NewHandler(pcf))
	af := &terminatingAF{client: NewClient(pcfRoot), deleted: make(chan error, 1)}

	var callbacks sync.Mutex
	var seen []string
	afRoot := serveH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbacks.Lock()
		seen = append(seen, r.Header.Get("User-Agent")+" "+r.Header.Get("3gpp-Sbi-Callback"))
		callbacks.Unlock()
		NewNotificationHandler(af).ServeHTTP(w, r)
	}))
	notifUri := afRoot + "/n5-callbacks/call-1"

	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	s, err := af.client.Create(ctx, voiceRequest(t, notifUri))
	if err != nil {
		t.Fatal(err)
	}

	// The PCF learns that the SDFs of component 1 are gone.
	pcf.mu.Lock()
	evNotifUri := pcf.contexts["1"].EvSubsc.NotifUri
	termNotifUri := pcf.contexts["1"].NotifUri
	pcf.mu.Unlock()
	n := NewNotifier()
	err = n.Notify(ctx, evNotifUri, EventsNotification{
		EvSubsUri: s.URI + EventsSubscriptionPath,
		EvNotifs:  []AfEventNotification{{Event: FailedResourcesAllocation, Flows: []Flows{{MedCompN: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Terminate(ctx, termNotifUri, TerminationInfo{TermCause: AllSDFDeactivation, ResUri: s.URI}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-af.deleted:
		if err != nil {
			t.Fatalf("Delete after the termination request: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the AF did not delete the context")
	}

	af.mu.Lock()
	if len(af.notified) != 1 || AppSessionURI(af.notified[0].EvSubsUri) != s.URI {
		t.Errorf("notified %+v", af.notified)
	}
	if len(af.terminate) != 1 || af.terminate[0].ResUri != s.URI {
		t.Errorf("terminate %+v", af.terminate)
	}
	af.mu.Unlock()
	pcf.mu.Lock()
	if len(pcf.deleted) != 1 || pcf.deleted[0] != "1" {
		t.Errorf("deleted %v", pcf.deleted)
	}
	pcf.mu.Unlock()
	callbacks.Lock()
	defer callbacks.Unlock()
	want := []string{"PCF Npcf_PolicyAuthorization_eventNotification", "PCF Npcf_PolicyAuthorization_terminationRequest"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("callbacks %q, want %q", seen, want)
	}
}

// TestNotifierError: an AF that refuses a notification is reported to
// the PCF with its ProblemDetails.
func TestNotifierError(t *testing.T) {
	af := &recordingAF{err: NewProblem(CauseMandatoryIEMissing, "")}
	root := serveH2C(t, NewNotificationHandler(af))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	err := NewNotifier().Terminate(ctx, root+"/cb", TerminationInfo{TermCause: PDUSessionTermination, ResUri: "http://pcf/x"})
	var p *ProblemError
	if !errors.As(err, &p) || p.Status != 400 {
		t.Errorf("got %v", err)
	}
}
