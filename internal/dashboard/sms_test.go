package dashboard

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

// shortMessage is a MESSAGE of SMS over IP with the body given in hex.
// The bodies below are the ones the UE simulator and the IP-SM-GW sent
// for "Hello, Bob!" through the in-process IMS of package ipsmgw.
func shortMessage(callID, body string) string {
	b, err := hex.DecodeString(strings.ReplaceAll(body, " ", ""))
	if err != nil {
		panic(err)
	}
	return sipText("MESSAGE tel:+12025550100 SIP/2.0", string(b),
		"Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bKsms;rport",
		"Max-Forwards: 70",
		"From: <"+caller+">;tag=sms1",
		"To: <tel:+12025550100>",
		"Call-ID: "+callID,
		"CSeq: 1 MESSAGE",
		"Content-Type: application/vnd.3gpp.sms")
}

func messageOf(t *testing.T, h *harness, callID string) Message {
	t.Helper()
	var got []Message
	h.get("/api/messages?call_id="+callID, &got)
	if len(got) == 0 {
		t.Fatalf("no message with Call-ID %s", callID)
	}
	return got[0]
}

func TestShortMessageIsDecoded(t *testing.T) {
	h := newHarness(t, Config{})
	const submit = "00 00 00 07 91 21 20 55 05 01 f0 17 21 00 0b 91 21 20 55 05 01 f2 00 00 0b c8 32 9b fd 66 81 84 6f 71 08"
	h.hop(ue1, pcscf, shortMessage("sms-submit", submit))

	got := messageOf(t, h, "sms-submit").SMS
	want := &ShortMessage{
		Hex: strings.ToLower(submit),
		Fields: []SMSField{
			{"RP-MTI", "0, RP-DATA (MS to network)"},
			{"RP-MR", "0"},
			{"RP-DA", "+12025550100"},
			{"TP-MTI", "SMS-SUBMIT"},
			{"TP-SRR", "true"},
			{"TP-MR", "0"},
			{"TP-DA", "+12025550102"},
			{"TP-DCS", "0x00, GSM 7 bit"},
			{"TP-UDL", "11 septets"},
		},
		Text: "Hello, Bob!",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SMS = %+v\nwant %+v", got, want)
	}
}

func TestReportsAreDecoded(t *testing.T) {
	h := newHarness(t, Config{})
	tests := []struct {
		name, body string
		want       []SMSField
	}{
		{"submit-report", "03 00 41 09 01 00 62 11 02 30 01 92 00", []SMSField{
			{"RP-MTI", "3, RP-ACK (network to MS)"},
			{"RP-MR", "0"},
			{"TP-MTI", "SMS-SUBMIT-REPORT"},
			{"TP-SCTS", "2026-11-20 03:10:29 +00:00"},
		}},
		{"refused", "05 01 01 01 41 0a 01 c3 00 62 11 02 30 01 92 00", []SMSField{
			{"RP-MTI", "5, RP-ERROR (network to MS)"},
			{"RP-MR", "1"},
			{"RP-Cause", "1"},
			{"TP-MTI", "SMS-SUBMIT-REPORT"},
			{"TP-FCS", "0xc3"},
			{"TP-SCTS", "2026-11-20 03:10:29 +00:00"},
		}},
		{"status-report", "01 00 07 91 21 20 55 05 01 f0 00 19 06 00 0b 91 21 20 55 05 01 f2 62 11 02 30 01 92 00 62 11 02 30 01 92 00 00", []SMSField{
			{"RP-MTI", "1, RP-DATA (network to MS)"},
			{"RP-MR", "0"},
			{"RP-OA", "+12025550100"},
			{"TP-MTI", "SMS-STATUS-REPORT"},
			{"TP-MR", "0"},
			{"TP-RA", "+12025550102"},
			{"TP-SCTS", "2026-11-20 03:10:29 +00:00"},
			{"TP-DT", "2026-11-20 03:10:29 +00:00"},
			{"TP-ST", "0x00"},
		}},
		{"delivery-report", "02 00 41 02 00 00", []SMSField{
			{"RP-MTI", "2, RP-ACK (MS to network)"},
			{"RP-MR", "0"},
			{"TP-MTI", "SMS-DELIVER-REPORT"},
		}},
	}
	for _, tt := range tests {
		h.hop(ue1, pcscf, shortMessage(tt.name, tt.body))
		got := messageOf(t, h, tt.name).SMS
		if got == nil || !reflect.DeepEqual(got.Fields, tt.want) || got.Text != "" || got.Error != "" {
			t.Errorf("%s: SMS = %+v\nwant fields %+v", tt.name, got, tt.want)
		}
	}
}

// A body that does not decode keeps what was read before the error, and
// says what went wrong; the message itself is still shown.
func TestBrokenShortMessageSaysWhy(t *testing.T) {
	h := newHarness(t, Config{})
	h.hop(ue1, pcscf, shortMessage("broken", "00 09 07"))
	got := messageOf(t, h, "broken").SMS
	if got == nil || got.Hex != "00 09 07" || len(got.Fields) != 0 || got.Error == "" {
		t.Errorf("SMS = %+v, want the hex and an error", got)
	}
}

func TestOtherMessagesHaveNoSMS(t *testing.T) {
	h := newHarness(t, Config{})
	h.hop(ue1, pcscf, invite("call-no-sms"))
	if got := messageOf(t, h, "call-no-sms").SMS; got != nil {
		t.Errorf("an INVITE has SMS %+v", got)
	}
}
