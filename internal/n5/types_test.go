package n5

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The example bodies below are written from the schemas of TS 29.514
// Annex A.2 for the IMS procedures; the addresses are documentation
// addresses (RFC 5737).

// signallingPathCreate is the Create of TS 29.514 4.2.6.7: media
// component 0 with one sub-component for all the AF signalling.
const signallingPathCreate = `{
  "ascReqData": {
    "evSubsc": {
      "events": [{"event": "FAILED_RESOURCES_ALLOCATION"}],
      "notifUri": "http://pcscf.ims.example/n5-callbacks/ue-1"
    },
    "medComponents": {
      "0": {
        "medCompN": 0,
        "medSubComps": {"0": {"fNum": 0, "flowUsage": "AF_SIGNALLING"}}
      }
    },
    "notifUri": "http://pcscf.ims.example/n5-callbacks/ue-1",
    "suppFeat": "10",
    "ueIpv4": "192.0.2.10"
  }
}`

// voiceCreate is the Create of TS 29.514 4.2.2.2 for one audio stream,
// built from an SDP answer (B.1).
const voiceCreate = `{
  "ascReqData": {
    "afAppId": "urn:urn-7:3gpp-service.ims.icsi.mmtel",
    "evSubsc": {
      "events": [
        {"event": "FAILED_RESOURCES_ALLOCATION"},
        {"event": "SUCCESSFUL_RESOURCES_ALLOCATION"}
      ],
      "notifUri": "http://pcscf.ims.example/n5-callbacks/call-1"
    },
    "medComponents": {
      "1": {
        "codecs": ["uplink\noffer\nm=audio 49170 RTP/AVP 96", "downlink\nanswer\nm=audio 50000 RTP/AVP 96"],
        "fStatus": "ENABLED",
        "marBwDl": "41 Kbps",
        "marBwUl": "41 Kbps",
        "medCompN": 1,
        "medSubComps": {
          "1": {
            "fNum": 1,
            "fDescs": [
              "permit out 17 from 198.51.100.20 50000 to 192.0.2.10 49170",
              "permit in 17 from 192.0.2.10 49170 to 198.51.100.20 50000"
            ]
          },
          "2": {
            "fNum": 2,
            "fDescs": [
              "permit out 17 from 198.51.100.20 50001 to 192.0.2.10 49171",
              "permit in 17 from 192.0.2.10 49171 to 198.51.100.20 50001"
            ],
            "flowUsage": "RTCP"
          }
        },
        "medType": "AUDIO",
        "rrBw": "600 bps",
        "rsBw": "800 bps"
      }
    },
    "notifUri": "http://pcscf.ims.example/n5-callbacks/call-1",
    "servInfStatus": "FINAL",
    "suppFeat": "8000010",
    "ueIpv4": "192.0.2.10"
  }
}`

// createAnswer is the body of the 201 that answers a Create.
const createAnswer = `{
  "ascReqData": {
    "notifUri": "http://pcscf.ims.example/n5-callbacks/ue-1",
    "suppFeat": "10",
    "ueIpv4": "192.0.2.10"
  },
  "ascRespData": {"suppFeat": "10"}
}`

// updatePatch is the PATCH of TS 29.514 4.2.3.2 after a re-INVITE put
// the stream on hold.
const updatePatch = `{
  "ascReqData": {
    "medComponents": {
      "1": {"fStatus": "DISABLED", "medCompN": 1}
    },
    "servInfStatus": "FINAL"
  }
}`

// eventsNotification is the notification of TS 29.514 4.2.5.8 that
// the resources of media component 1 could not be kept.
const eventsNotification = `{
  "evSubsUri": "http://pcf.5gc.example/npcf-policyauthorization/v1/app-sessions/42/events-subscription",
  "evNotifs": [
    {"event": "FAILED_RESOURCES_ALLOCATION", "flows": [{"fNums": [1, 2], "medCompN": 1}]}
  ],
  "failedResourcAllocReports": [
    {"mcResourcStatus": "INACTIVE", "flows": [{"medCompN": 1}]}
  ]
}`

// terminationInfo is the termination request of TS 29.514 4.2.5.3.
const terminationInfo = `{
  "termCause": "PDU_SESSION_TERMINATION",
  "resUri": "http://pcf.5gc.example/npcf-policyauthorization/v1/app-sessions/42"
}`

// problemDetails is the 500 of TS 29.514 4.2.2.2 when session binding
// fails.
const problemDetails = `{
  "status": 500,
  "detail": "no PDU session for 192.0.2.10",
  "cause": "PDU_SESSION_NOT_AVAILABLE"
}`

// roundTrip decodes body into a value of type T, encodes it again and
// checks that the JSON says the same: every attribute of the example
// has its field, with the name the OpenAPI gives it.
func roundTrip[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	again, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip changed the body\nwant %s\n got %s", body, again)
	}
	return v
}

func TestRoundTrips(t *testing.T) {
	t.Run("signalling path create", func(t *testing.T) {
		c := roundTrip[AppSessionContext](t, signallingPathCreate)
		sub := c.AscReqData.MedComponents["0"].MedSubComps["0"]
		if sub.FlowUsage != FlowUsageAFSignalling || sub.FNum != 0 {
			t.Errorf("sub-component = %+v", sub)
		}
	})
	t.Run("voice create", func(t *testing.T) {
		c := roundTrip[AppSessionContext](t, voiceCreate)
		r := c.AscReqData
		if r.ServInfStatus != ServiceInfoStatusFinal || r.MedComponents["1"].MedType != MediaTypeAudio {
			t.Errorf("request = %+v", r)
		}
		if !r.SuppFeat.Has(IMSSBI) || !r.SuppFeat.Has(PatchCorrection) || r.SuppFeat.Has(ProvAFsignalFlow) {
			t.Errorf("suppFeat %q", r.SuppFeat)
		}
	})
	t.Run("create answer", func(t *testing.T) { roundTrip[AppSessionContext](t, createAnswer) })
	t.Run("update patch", func(t *testing.T) {
		p := roundTrip[AppSessionContextUpdateDataPatch](t, updatePatch)
		if p.AscReqData.MedComponents["1"].FStatus != FlowStatusDisabled {
			t.Errorf("patch = %+v", p.AscReqData)
		}
	})
	t.Run("events notification", func(t *testing.T) {
		n := roundTrip[EventsNotification](t, eventsNotification)
		if n.EvNotifs[0].Event != FailedResourcesAllocation || n.FailedResourcAllocReports[0].McResourcStatus != MediaComponentResourcesInactive {
			t.Errorf("notification = %+v", n)
		}
	})
	t.Run("termination info", func(t *testing.T) {
		ti := roundTrip[TerminationInfo](t, terminationInfo)
		if ti.TermCause != PDUSessionTermination {
			t.Errorf("termCause %q", ti.TermCause)
		}
	})
	t.Run("problem details", func(t *testing.T) { roundTrip[ProblemDetails](t, problemDetails) })
}

// TestRequiredAttributesAreSent checks that the attributes the OpenAPI
// marks as required go out even when they are zero: medCompN 0 and
// fNum 0 are the AF signalling of TS 29.514 4.2.6.7.
func TestRequiredAttributesAreSent(t *testing.T) {
	b, err := json.Marshal(MediaComponent{MedSubComps: map[string]MediaSubComponent{"0": {}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"medCompN":0,"medSubComps":{"0":{"fNum":0}}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	b, err = json.Marshal(AppSessionContextReqData{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"notifUri":"","suppFeat":""}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestUnknownAttributesAreIgnored: a peer that sends attributes this
// package leaves out must still be understood.
func TestUnknownAttributesAreIgnored(t *testing.T) {
	var c AppSessionContextReqData
	body := `{"notifUri":"http://af.example/n","suppFeat":"10","ueIpv4":"192.0.2.1","sliceInfo":{"sst":1},"resPrio":"PRIO_1"}`
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatal(err)
	}
	if c.UeIpv4 != "192.0.2.1" {
		t.Errorf("ueIpv4 %q", c.UeIpv4)
	}
}

func TestSupportedFeatures(t *testing.T) {
	cases := []struct {
		features []Feature
		want     SupportedFeatures
	}{
		{nil, ""},
		{[]Feature{1}, "1"},
		{[]Feature{4}, "8"},
		{[]Feature{IMSSBI}, "10"},
		{[]Feature{IMSSBI, ProvAFsignalFlow}, "50"},
		{[]Feature{IMSSBI, PatchCorrection}, "8000010"},
		// TS 29.571 5.2.2: the last of 32 features alone is "80000000".
		{[]Feature{32}, "80000000"},
	}
	for _, c := range cases {
		got := NewSupportedFeatures(c.features...)
		if got != c.want {
			t.Errorf("NewSupportedFeatures(%v) = %q, want %q", c.features, got, c.want)
		}
		for _, f := range c.features {
			if !got.Has(f) {
				t.Errorf("%q.Has(%d) = false", got, f)
			}
		}
	}
	// Leading zeros change nothing, and upper case is allowed.
	if s := SupportedFeatures("0008000010"); !s.Has(IMSSBI) || !s.Has(PatchCorrection) || s.Has(ProvAFsignalFlow) {
		t.Errorf("%q", s)
	}
	if s := SupportedFeatures("F"); !s.Has(4) || s.Has(5) || s.Has(0) {
		t.Errorf("%q", s)
	}
}

func TestBitRate(t *testing.T) {
	cases := []struct {
		bps  uint64
		text BitRate
	}{
		{0, "0 bps"},
		{600, "600 bps"},
		{41000, "41 Kbps"},
		{41500, "41.5 Kbps"},
		{125000000, "125 Mbps"},
		{1000000000001, "1.000000000001 Tbps"},
	}
	for _, c := range cases {
		if got := BitRateFromBps(c.bps); got != c.text {
			t.Errorf("BitRateFromBps(%d) = %q, want %q", c.bps, got, c.text)
		}
		got, err := c.text.Bps()
		if err != nil || got != c.bps {
			t.Errorf("%q.Bps() = %d, %v, want %d", c.text, got, err, c.bps)
		}
	}
	// The examples of TS 29.571 5.2.2 are the same rate.
	for _, r := range []BitRate{"125 Mbps", "0.125 Gbps", "125000 Kbps"} {
		if got, err := r.Bps(); err != nil || got != 125000000 {
			t.Errorf("%q.Bps() = %d, %v", r, got, err)
		}
	}
	for _, bad := range []BitRate{"", "64", "64 kbps", "x Kbps", "1.x Kbps"} {
		if _, err := bad.Bps(); err == nil {
			t.Errorf("%q.Bps() did not fail", bad)
		}
	}
}

func TestNewProblem(t *testing.T) {
	cases := map[string]int{
		CausePDUSessionNotAvailable:            500,
		CauseRequestedServiceNotAuthorized:     403,
		CauseApplicationSessionContextNotFound: 404,
		CauseInvalidServiceInformation:         400,
		"SOMETHING_ELSE":                       500,
	}
	for cause, status := range cases {
		p := NewProblem(cause, "")
		if p.Status != status || p.Problem.Status != status || p.Problem.Cause != cause {
			t.Errorf("NewProblem(%s) = %+v", cause, p)
		}
	}
}
