package sgd

import (
	"context"
	"fmt"
	"time"

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
// before a delivery (SRR) and the report of how a delivery ended
// (RDR). An IP-SM-GW sends the same two (TS 23.204 6.4 step 3b, 6.5 step
// 7), and Ready-For-SM, the notice that a user can take short messages
// again (readyforsm.go).
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

// ReadyForSM tells the HSS that the user of req can take short messages
// again, so that it alerts the service centres that hold short messages
// for the user (TS 23.040 3.2.6 items 2c and 2f).
func (c *S6cClient) ReadyForSM(ctx context.Context, req ReadyForSMRequest) (ReadyForSMAnswer, error) {
	if err := requireRealm(req.Header); err != nil {
		return ReadyForSMAnswer{}, err
	}
	if err := requireE164("MSISDN", req.UserIdentifier.MSISDN, "IP-SM-GW-Number", req.IPSMGWNumber); err != nil {
		return ReadyForSMAnswer{}, err
	}
	if !req.AlertReason.valid() {
		return ReadyForSMAnswer{}, fmt.Errorf("sgd: %v is not a value of TS 29.272 7.3.83", req.AlertReason)
	}
	req.Header = routing(c.node, req.Header)
	answer, err := c.hss.Request(ctx, req.Message())
	if err != nil {
		return ReadyForSMAnswer{}, err
	}
	return ParseReadyForSMAnswer(answer)
}

// Peers is how the SC reaches one serving node over SGd, by the
// Origin-Host that the node sent in its CER, and finds the serving
// nodes of a realm among the nodes connected to it. Of the two ends,
// the serving node is the one that opens the connection here, since it
// is also the one that brings the mobile originated short messages.
// *diameter.Server is one.
type Peers interface {
	Request(ctx context.Context, originHost string, request *diam.Message) (*diam.Message, error)
	Hosts(realm string, application uint32) []string
}

// SGdClient sends the SGd requests of the SMS-GMSC: a short message for
// a UE, to the node that serves it (TFR).
type SGdClient struct {
	node  *diameter.Node
	peers Peers
	// answerTimeout is how long the client waits for a TFA, or zero to
	// wait the Answer timer of the node.
	answerTimeout time.Duration
}

// NewSGdClient returns a client that sends from node to the serving
// nodes that peers holds.
//
// answerTimeout is how long it waits for a TFA. A serving node answers
// only once the UE has acknowledged the short message, which it waits
// for as long as TR1N (TS 24.011 6.2.2.2), and an IP-SM-GW may try one
// domain after another within that answer (TS 23.204 6.5a): TS 23.204
// 6.5a NOTE 1 has the timer of the SMS-GMSC reconfigured to be longer
// where it leaves the IP-SM-GW too little time. Zero leaves the Answer
// timer of the node, which bounds its base protocol exchanges.
func NewSGdClient(node *diameter.Node, peers Peers, answerTimeout time.Duration) *SGdClient {
	return &SGdClient{node: node, peers: peers, answerTimeout: answerTimeout}
}

// MTForwardShortMessage forwards a short message, or a status report, to
// the serving node that req.DestinationHost names (TS 29.338 6.2.2).
func (c *SGdClient) MTForwardShortMessage(ctx context.Context, req MTForwardShortMessageRequest) (MTForwardShortMessageAnswer, error) {
	// TS 29.338 6.3.1.2: the SMS-GMSC names the serving node that the HSS
	// gave it, and Destination-Host is mandatory in the command format of
	// every request it sends. It is also how the client finds the
	// connection to send on.
	if req.Header.DestinationHost == "" {
		return MTForwardShortMessageAnswer{}, fmt.Errorf("sgd: a TFR names the serving node in Destination-Host")
	}
	if err := requireRealm(req.Header); err != nil {
		return MTForwardShortMessageAnswer{}, err
	}
	// 6.3.2.5: { SC-Address }, the RP-OA that the serving node gives the
	// UE. The number of the MME is optional, and empty towards any other
	// kind of node.
	if err := requireE164("SC-Address", req.SCAddress); err != nil {
		return MTForwardShortMessageAnswer{}, err
	}
	if err := checkE164("MME-Number-for-MT-SMS", req.MMENumberForMTSMS); err != nil {
		return MTForwardShortMessageAnswer{}, err
	}
	req.Header = routing(c.node, req.Header)
	answer, err := c.peers.Request(diameter.WithAnswerTimeout(ctx, c.answerTimeout), req.Header.DestinationHost, req.Message())
	if err != nil {
		return MTForwardShortMessageAnswer{}, err
	}
	return ParseMTForwardShortMessageAnswer(answer)
}

// ServingNodes are the Diameter identities of the nodes of realm that
// are connected to the client over SGd, sorted: the nodes that a TFR for
// that realm can be sent to now.
func (c *SGdClient) ServingNodes(realm string) []string {
	return c.peers.Hosts(realm, ApplicationIDSGd)
}

// ServiceCentre is how a serving node reaches the SC over SGd.
// *diameter.PeerGroup, the instances of the SC, is one, and so is a
// single *diameter.Peer.
type ServiceCentre interface {
	Request(ctx context.Context, request *diam.Message) (*diam.Message, error)
}

// MOClient sends the SGd request of a serving node, such as an
// IP-SM-GW: a short message from a UE, to the SC (OFR).
//
// Whether the request names the SMS-IWMSC in Destination-Host, or only
// its realm, is the caller's, as it is on S6c: TS 29.338 6.3.1.1 has a
// serving node that can turn the SC address of the UE into the name of
// an SMS-IWMSC give both AVPs, and one that cannot give
// Destination-Realm alone.
type MOClient struct {
	node *diameter.Node
	sc   ServiceCentre
}

// NewMOClient returns a client that sends from node to sc.
func NewMOClient(node *diameter.Node, sc ServiceCentre) *MOClient {
	return &MOClient{node: node, sc: sc}
}

// MOForwardShortMessage hands the SC a short message that a UE sent (TS
// 29.338 6.2.1). The answer carries the SMS-SUBMIT-REPORT that the
// serving node turns into the RP-ACK or RP-ERROR for the UE.
func (c *MOClient) MOForwardShortMessage(ctx context.Context, req MOForwardShortMessageRequest) (MOForwardShortMessageAnswer, error) {
	if err := requireRealm(req.Header); err != nil {
		return MOForwardShortMessageAnswer{}, err
	}
	// 6.3.2.3: { SC-Address }, the RP-DA that the UE sent. The MSISDN is
	// one of the identities of User-Identifier, which a serving node may
	// not know (Table 6.2.1.1/1).
	if err := requireE164("SC-Address", req.SCAddress); err != nil {
		return MOForwardShortMessageAnswer{}, err
	}
	if err := checkE164("MSISDN", req.UserIdentifier.MSISDN); err != nil {
		return MOForwardShortMessageAnswer{}, err
	}
	req.Header = routing(c.node, req.Header)
	answer, err := c.sc.Request(ctx, req.Message())
	if err != nil {
		return MOForwardShortMessageAnswer{}, err
	}
	return ParseMOForwardShortMessageAnswer(answer)
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
