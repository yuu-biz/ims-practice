package ims

// ConferenceFactoryURI is the default conference factory URI for MMTel
// of a home network (TS 23.003 V20.1.0 13.10): the SIP URI with the
// user part mmtel at the host conf-factory.<home domain>, such as
// sip:mmtel@conf-factory.ims.mnc001.mcc001.3gppnetwork.org. A UE that
// has no Conf_Factory_URI configured builds it (GSMA IR.92 2.3.3), and
// the MMTel AS is the focus behind it.
func ConferenceFactoryURI(homeDomain string) string {
	return "sip:mmtel@conf-factory." + homeDomain
}
