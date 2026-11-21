package sdp

// evsPayloadType is the dynamic payload type of EVS in VoNRCodecs, next
// to the 96 to 101 of VoLTECodecs.
const evsPayloadType = 102

// OfferParams are the a=fmtp parameters of an initial offer of EVS in
// configuration c: br, bw and max-red and nothing else (NG.114
// 3.2.2.3). This also keeps out what TS 26.114 Table 6.2a forbids in a
// terminal's initial offer (evs-mode-switch, dtx, dtx-recv, mode-set,
// mode-change-period, mode-change-neighbor). max-red, which Table 6.2a
// asks for, is 0 as in the AMR offers: the UE sends no redundancy.
func (c EVSConfiguration) OfferParams() EVSParams {
	return EVSParams{BR: Some(c.BitRates), BW: Some(c.Bandwidths), MaxRed: Some(0)}
}

// VoNRCodecs is what a VoNR UE offers, most preferred first: EVS in
// configuration c, then the codecs of VoLTECodecs with the same
// payload types. NG.114 3.2.2.1 asks the first offer for EVS up to
// super-wideband and for AMR-WB and AMR without mode-set. TS 26.114
// 5.2.1.6 lists the payload type that offers super-wideband before
// those of narrower bandwidths, so EVS comes before AMR-WB, which comes
// before AMR. EVS and AMR-WB use different payload types (TS 26.114
// 6.2.2.2).
func VoNRCodecs(c EVSConfiguration) []Codec {
	evsCodec := Codec{PayloadType: evsPayloadType, Name: NameEVS, ClockRate: 16000, Channels: 1, Fmtp: c.OfferParams().String()}
	return append([]Codec{evsCodec}, VoLTECodecs()...)
}

// UEEVS is how this project's UE uses EVS: it offers VoNRCodecs(UEEVS.Configuration), answers with
// AnswerEVSParams(offer, UEEVS), and sends AMR-WB frames as EVS AMR-WB
// IO frames from the start of the session, which evs-mode-switch=1 in
// the answer sets for both directions (TS 26.445 A.3.1). It has no EVS
// encoder, so it never sends EVS Primary. A2 is the configuration TS
// 26.114 A.14.1.3 shows for NR access, and with it no mode-set limits
// the AMR-WB IO modes.
//
// SIMPLIFIED: TS 26.114 6.2.2.2 Table 6.2a and 6.2.2.3 Table 6.3b. A
// terminal that supports EVS supports the whole bit rate range; this
// UE offers and answers EVS Primary bit rates and bandwidths that it
// cannot send. The project uses EVS in signalling only and carries
// AMR-WB in the payload, as it has no EVS codec.
//
// SIMPLIFIED: TS 26.445 A.3.1 evs-mode-switch and TS 26.114 Table 6.3a.
// Only calls between UEs of this project work. As the offerer, the UE
// starts in AMR-WB IO only when the answer has evs-mode-switch=1, which
// another UE of this project sends. An EVS terminal answers without it
// and starts in EVS Primary, which the UE would have to send as well;
// nor can the UE follow a request to switch to EVS Primary during the
// session (a CMR of Table A.3, or the RTCP-APP of TS 26.114 10.2.1).
var UEEVS = EVSCapabilities{Configuration: EVSConfigA2, AMRWBIOOnly: true}
