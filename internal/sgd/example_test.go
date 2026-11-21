package sgd

import (
	"bytes"
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// overTheWire stands for the Diameter connection between two nodes in
// the examples: it serializes m and reads it back.
func overTheWire(m *diam.Message) *diam.Message {
	b, err := m.Serialize()
	if err != nil {
		panic(err)
	}
	received, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		panic(err)
	}
	return received
}

// ExampleMOForwardShortMessageRequest shows a mobile originated short
// message on SGd. The MME has taken an RP-DATA from the UE apart: the
// RP-Destination Address is the SC, and the RP-User-Data is the TPDU.
func ExampleMOForwardShortMessageRequest() {
	if err := LoadDictionary(); err != nil {
		panic(err)
	}
	const realm = "epc.mnc001.mcc001.3gppnetwork.org"
	smsSubmit := []byte{0x01, 0x2A, 0x0B, 0x91, 0x21, 0x20, 0x55, 0x05, 0x21, 0xF3, 0x00, 0x00, 0x05, 0xE8, 0x32, 0x9B, 0xFD, 0x06}

	// The MME.
	ofr := MOForwardShortMessageRequest{
		Header: Header{
			SessionID:        "mme." + realm + ";1;1",
			OriginHost:       "mme." + realm,
			OriginRealm:      realm,
			DestinationRealm: realm,
		},
		SCAddress:      "12025550100",
		UserIdentifier: UserIdentifier{UserName: "001010000000001", MSISDN: "12025550112"},
		SMRPUI:         smsSubmit,
	}.Message()

	// The SC.
	request := overTheWire(ofr)
	received, err := ParseMOForwardShortMessageRequest(request)
	if err != nil {
		panic(err)
	}
	fmt.Printf("to SC %s from IMSI %s, MSISDN %s: TPDU of %d octets\n",
		received.SCAddress, received.UserIdentifier.UserName, received.UserIdentifier.MSISDN, len(received.SMRPUI))
	ofa := MOForwardShortMessageAnswer{Result: Base(DiameterSuccess)}.
		Message(request, Identity{OriginHost: "sc." + realm, OriginRealm: realm})

	// The MME again: a success becomes the RP-ACK for the UE.
	answer, err := ParseMOForwardShortMessageAnswer(overTheWire(ofa))
	if err != nil {
		panic(err)
	}
	fmt.Println(answer.Result, answer.Result.Success())
	// Output:
	// to SC 12025550100 from IMSI 001010000000001, MSISDN 12025550112: TPDU of 18 octets
	// Result-Code 2001 true
}
