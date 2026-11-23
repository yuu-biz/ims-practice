package sgd

import (
	"context"
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// This file holds the clients of TS 29.338: the parts that put a request
// of this package on a Diameter connection and read the answer back.
// Each takes the request with its own AVPs already filled in, writes the
// Session-Id and the origin of its node, and sends it where the routing
// AVPs of the request say (5.3.1.1, 5.3.1.2, 6.3.1.1, 6.3.1.2).
//
// A client returns an error when no answer came, and the answer whatever
// its result says, because the caller decides what each result of TS
// 29.338 7.3 means for the short message it holds.

// HSS is how the central SMS functions reach the HSS over S6c.
// *diameter.PeerGroup, the instances of the HSS, is one, and so is a
// single *diameter.Peer.
type HSS interface {
	Request(ctx context.Context, request *diam.Message) (*diam.Message, error)
}

// S6cClient sends the S6c requests of the SMS-GMSC: the routing query
// before a delivery (SRR) and the report of a delivery that failed
// (RDR). An IP-SM-GW sends the same two (TS 23.204 6.4 step 3b, 6.5 step
// 7).
//
// Whether a request names the HSS of the user in Destination-Host, or
// only its realm, is the caller's: TS 29.338 5.3.1.1.2 has a sender that
// knows the HSS name give both AVPs, and 5.3.1.1.3 a sender that knows
// only the realm give Destination-Realm alone.
type S6cClient struct {
	node *diameter.Node
	hss  HSS
}

// NewS6cClient returns a client that sends from node to hss.
func NewS6cClient(node *diameter.Node, hss HSS) *S6cClient {
	return &S6cClient{node: node, hss: hss}
}

// SendRoutingInfoForSM asks the HSS which node serves the user of
// req.MSISDN (TS 29.338 5.2.1).
func (c *S6cClient) SendRoutingInfoForSM(ctx context.Context, req SendRoutingInfoForSMRequest) (SendRoutingInfoForSMAnswer, error) {
	if err := requireRealm(req.Header); err != nil {
		return SendRoutingInfoForSMAnswer{}, err
	}
	// 5.3.2.3 leaves both numbers of an SRR optional.
	if err := checkE164("MSISDN", req.MSISDN, "SC-Address", req.SCAddress); err != nil {
		return SendRoutingInfoForSMAnswer{}, err
	}
	req.Header = routing(c.node, req.Header)
	answer, err := c.hss.Request(ctx, req.Message())
	if err != nil {
		return SendRoutingInfoForSMAnswer{}, err
	}
	return ParseSendRoutingInfoForSMAnswer(answer)
}

// ReportSMDeliveryStatus tells the HSS how a delivery ended, so that it
// keeps the SC address in the message waiting data of the user and
// alerts the SC when the user is reachable again (TS 29.338 5.2.3).
func (c *S6cClient) ReportSMDeliveryStatus(ctx context.Context, req ReportSMDeliveryStatusRequest) (ReportSMDeliveryStatusAnswer, error) {
	if err := requireRealm(req.Header); err != nil {
		return ReportSMDeliveryStatusAnswer{}, err
	}
	if err := checkE164("MSISDN", req.UserIdentifier.MSISDN); err != nil {
		return ReportSMDeliveryStatusAnswer{}, err
	}
	if err := requireE164("SC-Address", req.SCAddress); err != nil {
		return ReportSMDeliveryStatusAnswer{}, err
	}
	req.Header = routing(c.node, req.Header)
	answer, err := c.hss.Request(ctx, req.Message())
	if err != nil {
		return ReportSMDeliveryStatusAnswer{}, err
	}
	return ParseReportSMDeliveryStatusAnswer(answer)
}

// routing completes the routing AVPs of a request: the Session-Id and
// the origin of the node that sends it, keeping the destination that the
// caller named.
func routing(node *diameter.Node, h diameter.Header) diameter.Header {
	return diameter.NewHeader(node, h.DestinationHost, h.DestinationRealm)
}

// requireRealm refuses a request that names no realm, before anything
// goes out. Every command format of TS 29.338 5.3.2 and 6.3.2 has
// Destination-Realm as a mandatory AVP, and a request without one names
// nowhere to go: Destination-Host is optional in all of them but the TFR
// (RFC 6733 6.1).
func requireRealm(h diameter.Header) error {
	if h.DestinationRealm == "" {
		return fmt.Errorf("sgd: a request carries the realm it goes to in Destination-Realm")
	}
	return nil
}

// checkE164 takes pairs of an AVP name and a number, and refuses the
// first that is not one to 15 decimal digits, before anything goes out.
// EncodeTBCD packs the digits of such an AVP without looking at them, so
// a number that another node wrote by hand, with a "+" or a space, would
// otherwise travel as octets that no receiver can read. An empty number
// passes where the command format makes the AVP optional, or where the
// request names the user another way, as an SRR for a user without
// MSISDN does with User-Name.
func checkE164(namesAndNumbers ...string) error {
	for i := 0; i < len(namesAndNumbers); i += 2 {
		if number := namesAndNumbers[i+1]; number != "" && !ValidE164(number) {
			return fmt.Errorf("sgd: %s %q is not 1 to 15 decimal digits", namesAndNumbers[i], number)
		}
	}
	return nil
}

// requireE164 is checkE164 for the AVPs that a command format marks
// mandatory, such as the SC-Address of an RDR, an OFR and a TFR (TS
// 29.338 5.3.2.7, 6.3.2.3, 6.3.2.5): an empty number is refused as well.
// The receiver would answer a request that is missing one with
// DIAMETER_MISSING_AVP (RFC 6733 7.1.5), so the client says so itself
// instead of sending it.
func requireE164(namesAndNumbers ...string) error {
	for i := 0; i < len(namesAndNumbers); i += 2 {
		if namesAndNumbers[i+1] == "" {
			return fmt.Errorf("sgd: a request carries %s, which is missing", namesAndNumbers[i])
		}
	}
	return checkE164(namesAndNumbers...)
}
