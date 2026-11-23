package ipsmgw_test

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file holds the short messages between IMS networks:
// one that the SC hands over for a user of another network, and one
// that another network hands over for a user of this one.

const peerUser = "sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone"

func withIBCF(t *testing.T) ipsmgw.Config {
	t.Helper()
	u, err := sip.ParseURI("sip:" + ibcfHost + ":5060")
	if err != nil {
		t.Fatal(err)
	}
	return ipsmgw.Config{IBCF: u}
}

// TestDeliveryToAnotherNetworkLeavesThroughTheIBCF: the MESSAGE for a
// user of another network has the IBCF on top of Route and the SC as
// its sender, by its number, and the delivery ends when that network
// accepts the MESSAGE.
func TestDeliveryToAnotherNetworkLeavesThroughTheIBCF(t *testing.T) {
	h := newHarnessWith(t, ipsmgw.NewMemoryStore(), time.Minute, func(*sip.Message) int { return 202 }, withIBCF(t))
	h.g.Deliver(ipsmgw.Delivery{Reference: "ref-peer", Recipient: peerUser, TPDU: deliver})
	mt := h.next()
	checkHeaders(t, mt, map[string]string{
		"Route":               "<sip:" + ibcfHost + ":5060;lr>",
		"To":                  "<" + peerUser + ">",
		"P-Asserted-Identity": "<tel:+12025550100>",
		"Accept-Contact":      "*;+g.3gpp.smsip;require;explicit",
	})
	if routes := mt.GetAll("Route"); len(routes) != 1 || routes[0] != "<sip:"+ibcfHost+":5060;lr>" {
		t.Errorf("Route = %q, want the IBCF alone", routes)
	}
	if from, err := sip.ParseNameAddr(mt.Get("From")); err != nil || from.URI != "tel:+12025550100" {
		t.Errorf("From = %q, want the SC", mt.Get("From"))
	}
	if mt.RequestURI != peerUser {
		t.Errorf("Request-URI = %s", mt.RequestURI)
	}
	if data, ok := parseRP(t, mt).(sms.RPDataNetworkToMS); !ok || data.OriginatorAddress != scAddress {
		t.Errorf("RP = %+v, want RP-DATA from the SC", parseRP(t, mt))
	}
	if got := h.nextReport(); got != (reported{"ref-peer", ipsmgw.Forwarded}) {
		t.Errorf("SC heard %+v", got)
	}
}

// TestDeliveryInTheHomeNetworkWithAnIBCF: a user of the home network
// gets the MESSAGE through the entry point, as without an IBCF.
func TestDeliveryInTheHomeNetworkWithAnIBCF(t *testing.T) {
	h := newHarnessWith(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK, withIBCF(t))
	h.g.Deliver(ipsmgw.Delivery{Reference: "ref", Recipient: bobTel, TPDU: deliver})
	checkHeaders(t, h.next(), map[string]string{"Route": "<sip:" + domain + ";lr>", "P-Asserted-Identity": "<" + ownURI + ">"})
}

// TestShortMessageFromAnotherNetworkIsDelivered is TS 23.204 6.4a: the
// S-CSCF brings a short message from another network for Bob, in the
// terminating case. The IP-SM-GW answers 202 and delivers the
// SMS-DELIVER to Bob with the SC of the other network in the RP-OA,
// and the outcome goes to the SC under the Call-ID of that MESSAGE.
func TestShortMessageFromAnotherNetworkIsDelivered(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	peerSC := sms.InternationalNumber("+12025550150")
	m := message(marshal(t, sms.RPDataNetworkToMS{MessageReference: 7, OriginatorAddress: peerSC, UserData: marshal(t, deliver)}))
	m.RequestURI = bobTel
	m.Set("To", "<"+bobTel+">")
	m.Set("P-Asserted-Identity", "<sip:+12025550150@ims.mnc002.mcc001.3gppnetwork.org;user=phone>")
	m.Set("P-Served-User", "<"+bobTel+">;sescase=term;regstate=reg")
	if resp := h.send(m); resp.StatusCode != 202 {
		t.Fatalf("got %d, want 202", resp.StatusCode)
	}
	mt := h.next()
	if mt.RequestURI != bobTel || mt.Get("P-Asserted-Identity") != "<"+ownURI+">" || mt.Get("In-Reply-To") != "" {
		t.Errorf("delivery to %s from %s, In-Reply-To %q", mt.RequestURI, mt.Get("P-Asserted-Identity"), mt.Get("In-Reply-To"))
	}
	data, ok := parseRP(t, mt).(sms.RPDataNetworkToMS)
	if !ok || data.OriginatorAddress != peerSC {
		t.Fatalf("RP = %+v, want RP-DATA with the other network's SC", parseRP(t, mt))
	}
	if tpdu, err := sms.ParseTPDU(data); err != nil || tpdu.(sms.Deliver).OriginatingAddress != deliver.OriginatingAddress {
		t.Errorf("TPDU = %+v, %v", tpdu, err)
	}
	h.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference}))
	if got := h.nextReport(); got != (reported{m.Get("Call-ID"), ipsmgw.Delivered}) {
		t.Errorf("SC heard %+v", got)
	}
}
