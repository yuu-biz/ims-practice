package sdp

// OfferParams are the a=fmtp parameters of an initial offer of EVS in
// configuration c: br, bw and max-red and nothing else (NG.114
// 3.2.2.3). This also keeps out what TS 26.114 Table 6.2a forbids in a
// terminal's initial offer (evs-mode-switch, dtx, dtx-recv, mode-set,
// mode-change-period, mode-change-neighbor). max-red, which Table 6.2a
// asks for, is 0 as in the AMR offers: the UE sends no redundancy.
func (c EVSConfiguration) OfferParams() EVSParams {
	return EVSParams{BR: Some(c.BitRates), BW: Some(c.Bandwidths), MaxRed: Some(0)}
}

// VoNRCodecs is what the UE offers on NR access, most preferred first:
// the codecs of VoLTECodecs, AMR-WB before AMR (TS 26.114 5.2.1.6),
// which are all the UE can send.
//
// SIMPLIFIED: TS 26.114 5.2.1.1. A terminal is to support
// super-wideband speech, which takes EVS, and GSMA NG.114 asks a VoNR
// UE for EVS in its offer; this offer has none, as the UE has no EVS
// codec. An EVS payload type would offer EVS Primary: a terminal that
// offers EVS supports its whole bit-rate range (Table 6.2a).
func VoNRCodecs() []Codec {
	return VoLTECodecs()
}
