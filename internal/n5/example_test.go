package n5_test

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/n5"
)

// pcscf is the P-CSCF's side of the notifications. A real one finds
// the IMS session of the context in its store and ends it.
type pcscf struct{ client *n5.Client }

func (p pcscf) Notify(ctx context.Context, n n5.EventsNotification) error {
	log.Printf("events for %s: %+v", n5.AppSessionURI(n.EvSubsUri), n.EvNotifs)
	return nil
}

func (p pcscf) Terminate(ctx context.Context, t n5.TerminationInfo) error {
	go p.client.Delete(context.Background(), t.ResUri, nil)
	return nil
}

// The P-CSCF subscribes to the status of the signalling path of a UE
// that has registered (TS 29.514 4.2.6.7), and serves the
// notifications on its own SBI port.
func Example_signallingPath() {
	client := n5.NewClient("http://pcf.5gc.example")

	mux := http.NewServeMux()
	mux.Handle("/n5-callbacks/", n5.NewNotificationHandler(pcscf{client}))
	go n5.NewServer(":80", mux).ListenAndServe()

	notifUri := "http://pcscf.ims.example/n5-callbacks/ue-1"
	s, err := client.Create(context.Background(), n5.AppSessionContextReqData{
		EvSubsc: &n5.EventsSubscReqData{
			Events:   []n5.AfEventSubscription{{Event: n5.FailedResourcesAllocation}},
			NotifUri: notifUri,
		},
		MedComponents: map[string]n5.MediaComponent{"0": {
			MedCompN:    0,
			MedSubComps: map[string]n5.MediaSubComponent{"0": {FNum: 0, FlowUsage: n5.FlowUsageAFSignalling}},
		}},
		NotifUri: notifUri,
		SuppFeat: n5.NewSupportedFeatures(n5.IMSSBI, n5.PatchCorrection),
		UeIpv4:   "192.0.2.10",
	})
	var p *n5.ProblemError
	switch {
	case errors.As(err, &p) && p.Problem.Cause == n5.CausePDUSessionNotAvailable:
		// The UE does not come through the 5GC: serve it without policy
		// control, as the P-CSCF does when the PCRF answers over Rx that
		// the UE has no IP-CAN session.
	case err != nil:
		log.Print(err)
	default:
		log.Printf("signalling path context %s", s.URI)
	}
}
