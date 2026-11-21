package mmtel_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// documents is an HSS that has an MMTelServices document for some
// users, by public identity, and notes whom the AS asked for.
type documents struct {
	mu    sync.Mutex
	docs  map[string]string
	err   error
	asked []string
}

func (d *documents) RepositoryData(_ context.Context, publicID, serviceIndication string) (sh.TransparentData, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asked = append(d.asked, publicID+" "+serviceIndication)
	if d.err != nil {
		return sh.TransparentData{}, false, d.err
	}
	doc, ok := d.docs[publicID]
	if !ok {
		return sh.TransparentData{}, false, nil
	}
	return sh.TransparentData{ServiceIndication: serviceIndication, SequenceNumber: 3, ServiceData: &sh.ServiceData{XML: []byte(doc)}}, true, nil
}

func (d *documents) askedFor() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.asked...)
}

// mmtelServices wraps the complete-* elements in an MMTelServices
// document.
func mmtelServices(elements string) string {
	return `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">` +
		elements + `</MMTelServices>`
}

func TestServiceDataOfTheServedUser(t *testing.T) {
	hss := &documents{docs: map[string]string{}}
	as := startAS(t, hss, mmtel.Config{})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Accept(answer) })
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Fatalf("caller got %v, want the callee's 200: no document means no service", got)
	}
	out.Bye()
	if got, want := hss.askedFor(), []string{"sip:alice@" + domain + " " + simservs.ServiceIndication}; !reflect.DeepEqual(got, want) {
		t.Errorf("AS asked the HSS for %q, want %q", got, want)
	}
}

// TestServiceDataUnavailable: without the settings the AS answers 500,
// which lets the S-CSCF apply the default handling of the criterion.
func TestServiceDataUnavailable(t *testing.T) {
	for name, hss := range map[string]*documents{
		"HSS fails":    {err: errors.New("no answer")},
		"bad document": {docs: map[string]string{"sip:alice@" + domain: "<MMTelServices"}},
	} {
		t.Run(name, func(t *testing.T) {
			as := startAS(t, hss, mmtel.Config{})
			callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Accept(answer) })
			caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
			out, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
			if err != nil {
				t.Fatal(err)
			}
			if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{500}) {
				t.Errorf("caller got %v, want 500", got)
			}
		})
	}
}
