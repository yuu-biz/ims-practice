package config

// Unread lists the sections that the node checks but no role reads, by
// their YAML names, such as "store" or "scscf.sip.pool". A node with
// them runs as it would without them: every role keeps its state in
// its own process. The caller warns about each, so that a node
// configured for a shared store does not appear to have one.
//
// The roles that keep the state of the users read the store section
// (readsStore). The rest of a node keeps its state in its process even
// then: the I-CSCF holds none, the EPC and PCRF stubs run as a single
// instance, and the fault agent holds the handful of faults of a lab.
func (c Config) Unread() []string {
	var unread []string
	if c.Store != nil && !c.readsStore() {
		unread = append(unread, "store")
	}
	sip := func(section string, s SIP, poolRead bool) {
		if s.Pool != "" && !poolRead {
			unread = append(unread, section+".pool")
		}
	}
	if c.PCSCF != nil {
		sip("pcscf.sip", c.PCSCF.SIP, false)
	}
	if c.ICSCF != nil {
		sip("icscf.sip", c.ICSCF.SIP, false)
	}
	if c.SCSCF != nil {
		sip("scscf.sip", c.SCSCF.SIP, false)
		if c.SCSCF.Restoration {
			unread = append(unread, "scscf.restoration")
		}
	}
	if c.MRFC != nil {
		sip("mrfc.sip", c.MRFC.SIP, false)
	}
	if c.IPSMGW != nil {
		sip("ipsmgw.sip", c.IPSMGW.SIP, false)
	}
	if c.MMTel != nil {
		sip("mmtel.sip", c.MMTel.SIP, false)
	}
	if c.IBCF != nil {
		// The IBCF puts the pool of each side in its Record-Route.
		sip("ibcf.sip", c.IBCF.SIP, true)
		sip("ibcf.external_sip", c.IBCF.ExternalSIP, true)
	}
	return unread
}

// readsStore reports whether a role of the node keeps its state in the
// store section: the IBCF the records of its calls, the S-CSCF its
// registrar state, the P-CSCF its IP associations and dialogs, the HSS
// its subscribers, the IP-SM-GW its deliveries and subscriptions, the
// MMTel AS the records of its calls and conferences, the MRFC the media
// of each conference, and a media relay in the agw or the mrfp role the
// contexts whose ports it holds.
func (c Config) readsStore() bool {
	return c.IBCF != nil || c.SCSCF != nil || c.PCSCF != nil || c.HSS != nil || c.IPSMGW != nil ||
		c.MMTel != nil || c.MRFC != nil || c.AGW != nil || c.MRFP != nil
}
