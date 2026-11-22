package k8s

import "github.com/yuu-biz/ims-practice/internal/yamledit"

// changes is every difference between a role configuration file of
// deploy/compose and its copy in base/config. Pods have no fixed
// addresses, so what Docker Compose gives as an address Kubernetes gives
// as a Service name, or an init container fills in when the Pod starts.
// The comments change with the values, so that a learner who reads the
// copy reads why it differs. A change with no File applies to every
// role file that has its key.
//
// After changing this table or a file in deploy/compose, run
//
//	go generate ./deploy/k8s/...
var changes = []yamledit.Change{
	{
		File: "agw.yaml",
		Key:  "agw.rtp_address",
		Comment: `The Pod's own address, which the P-CSCF writes into SDP. Kubernetes
gives a Pod a new address each time it starts, so the init container
in agw.yaml (the StatefulSet) writes it in place of ${POD_IP}. The UE
Pods send RTP to it directly, without a Service in between.`,
		Value: "${POD_IP}",
	},
	{
		File: "agw.yaml",
		Key:  "agw.rtp_ports",
		Comment: `The same range as in Docker Compose. A UE outside the cluster needs
these ports on the node, which the component edge opens with
hostPort.`,
	},
	{
		File: "mrfp.yaml",
		Key:  "mrfp.rtp_address",
		Comment: `The Pod's own address, which the MRFC writes into SDP. The init
container in mrfp.yaml (the StatefulSet) writes it in place of
${POD_IP}, as for the agw.`,
		Value: "${POD_IP}",
	},
	{
		File: "mrfp.yaml",
		Key:  "mrfp.announcements",
		Comment: `The WAV files that the image carries, the same as those that
compose.yaml mounts from deploy/announcements.`,
	},
	{
		File: "lab.yaml",
		Key:  "lab.hss_provisioning",
		Comment: `Names of Services instead of the fixed addresses of Docker Compose,
since Pod addresses change.`,
		Value: "http://hss.ims.svc.cluster.local:8081",
	},
	{
		File:  "lab.yaml",
		Key:   "lab.epc",
		Value: "http://epc.ims.svc.cluster.local:8080",
	},
	{
		File: "lab.yaml",
		Key:  "lab.ues",
		Comment: `The UE simulators ue-a and ue-b of ue.yaml. They start with zero
replicas, like the sim profile of compose.yaml; until they are
scaled up, the web UI shows them as not reachable. volte-a and
volte-b have no Pods: a VoLTE UE needs the static address that the
EPC stub hands its subscriber, which a Pod cannot ask for, so the
web UI always shows them as not reachable.`,
	},
	{
		File:  "lab.yaml",
		Key:   "lab.ues.0.control",
		Value: "http://ue-a.ims.svc.cluster.local:8090",
	},
	{
		File:  "lab.yaml",
		Key:   "lab.ues.1.control",
		Value: "http://ue-b.ims.svc.cluster.local:8090",
	},
	{
		File:  "lab.yaml",
		Key:   "lab.ues.4.control",
		Value: "http://ue-c.ims.svc.cluster.local:8090",
	},
	{
		File:  "lab.yaml",
		Key:   "lab.ues.2.control",
		Value: "http://volte-a.ims.svc.cluster.local:8090",
	},
	{
		File:  "lab.yaml",
		Key:   "lab.ues.3.control",
		Value: "http://volte-b.ims.svc.cluster.local:8090",
	},
	{
		File: "epc.yaml",
		Key:  "epc.pcscf_addresses",
		Comment: `Handed to the UE in the PCO, the first one preferred (TS 24.229
L.2.2.1). The PCO carries addresses, and the P-CSCF Pod's address
changes when it starts again, so the init container in epc.yaml (the
StatefulSet) looks it up and writes it in place of ${PCSCF_IP}.`,
		Value: []string{"${PCSCF_IP}"},
	},
	{
		// The one place where addresses of the compose network stay
		// (TestNoComposeAddresses).
		File: "epc.yaml",
		Key:  "epc.subscribers",
		Comment: `Test IMSIs, as in the private identities of hss.yaml. The stub does
not route packets, so each UE must already have its address, a static
one per subscriber (TS 23.401 5.3.1.1). These are the addresses of
Docker Compose: Pods cannot ask for an address, so on Kubernetes they
match no UE.`,
	},
	{
		File:   "pcscf.yaml",
		Key:    "pcscf.host_access",
		Remove: true,
		Comment: `No host_access: the UEs run as Pods in the cluster and reach the
P-CSCF and the relay without NAT. A UE on the host needs it again,
with the gateway of the kind network.`,
	},
	{
		Key: "hep",
		Comment: `Copies of every SIP and Diameter message for the Lab.
The Lab's Service keeps one ClusterIP while its Pod comes and
goes, so the name resolves even when the Lab is down; the
Lab is not on the call path. HEP is one-way UDP, and the
NAT of kube-proxy in front of a ClusterIP does no harm to it.`,
	},
	{
		Key:   "hep.collector",
		Value: "lab.ims.svc.cluster.local:9060",
	},
}

// The home network zone changes in two ways: each A record of a node
// becomes a CNAME to the node's headless Service in the namespace ims,
// which the cluster DNS answers with the Pod's current address, and an
// SRV record that points to a node points to that Service instead,
// since the target of an SRV record must not be an alias (RFC 2782).
// The other zones are copied as they are.
const (
	homeZone = "ims.mnc001.mcc001.3gppnetwork.org.zone"
	// serviceDomain follows the label of each node: icscf becomes
	// icscf.ims.svc.cluster.local.
	serviceDomain = ".ims.svc.cluster.local."
	// nameServer keeps an A record, since the name of a name server must
	// not be a CNAME (RFC 2181 10.3). Its address is the fixed ClusterIP
	// of the DNS server's Service, which differs from one cluster to
	// another: the zone takes kind's from environments/kind, and
	// components/name-server replaces it with the environment's.
	nameServer = "dns"
	// nameServerAddressKey is the key of that address in the ConfigMap
	// ims-environment of each environment.
	nameServerAddressKey = "ims_dns_cluster_ip"
	// NameServerDelimiter occurs once in the zone, in the name server's
	// record, which is the zone's last line. components/name-server
	// splits the zone at it and replaces the part after it, the address.
	NameServerDelimiter = " IN A "
)

// homeZoneHeader replaces the comment at the top of the home zone.
const homeZoneHeader = `The home network domain on Kubernetes, with the NAPTR and SRV records
of deploy/compose/dns. The nodes have no fixed addresses here, so each
name is a CNAME to the node's headless Service, which the cluster DNS
answers with the Pod's current address. The target of an SRV record
must not be such an alias (RFC 2782), so the SRV records name the
Service itself.`

// nameServerComment goes above the A record of the name server.
const nameServerComment = `The name server of the domain has an address of its own: the fixed
ClusterIP of the DNS server's Service (dns, or ims-dns with the
cluster-dns overlay). A name server must not be a CNAME (RFC 2181
10.3). The address here is kind's, from environments/kind. For another
cluster, components/name-server replaces everything after "IN A" with
the address of its environment, so this record comes last.`
