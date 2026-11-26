package n5_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"

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

// pcf stands for the PCF of the 5GC, which knows the PDU session of one
// UE address. Update and Delete are those of the embedded interface,
// which is nil: the example does not use them.
type pcf struct{ n5.PCF }

func (pcf) Create(_ context.Context, c n5.AppSessionContext) (string, n5.AppSessionContext, error) {
	if c.AscReqData.UeIpv4 != "192.0.2.10" {
		return "", n5.AppSessionContext{}, n5.NewProblem(n5.CausePDUSessionNotAvailable, "")
	}
	return "ue-1-signalling", c, nil
}

// serve serves h with cleartext HTTP/2 on a free port of the loopback,
// where a node serves it on its SBI port, ":80" for an "http" apiRoot
// (TS 29.500 5.3), and returns the apiRoot.
func serve(h http.Handler) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	go n5.NewServer(l.Addr().String(), h).Serve(l)
	return "http://" + l.Addr().String()
}

// The P-CSCF subscribes to the status of the signalling path of a UE
// that has registered (TS 29.514 4.2.6.7), and serves the
// notifications on its own SBI port. A UE that does not come through
// the 5GC has no PDU session at the PCF.
func Example_signallingPath() {
	client := n5.NewClient(serve(n5.NewHandler(pcf{})))

	mux := http.NewServeMux()
	mux.Handle("/n5-callbacks/", n5.NewNotificationHandler(pcscf{client}))
	callbacks := serve(mux)

	for _, ue := range []string{"192.0.2.10", "192.0.2.99"} {
		notifUri := callbacks + "/n5-callbacks/ue-1"
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
			UeIpv4:   ue,
		})
		var p *n5.ProblemError
		switch {
		case errors.As(err, &p) && p.Problem.Cause == n5.CausePDUSessionNotAvailable:
			// Serve the UE without policy control, as the P-CSCF does when
			// the PCRF answers over Rx that the UE has no IP-CAN session.
			fmt.Println(ue, "has no PDU session")
		case err != nil:
			log.Print(err)
		default:
			u, _ := url.Parse(s.URI)
			fmt.Println(ue, "signalling path context", u.Path)
		}
	}
	// Output:
	// 192.0.2.10 signalling path context /npcf-policyauthorization/v1/app-sessions/ue-1-signalling
	// 192.0.2.99 has no PDU session
}
