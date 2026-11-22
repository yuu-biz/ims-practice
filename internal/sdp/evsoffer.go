package sdp

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
