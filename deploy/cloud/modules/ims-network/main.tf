# What the IMS needs from the network of any cloud: which ports the
# internet may reach, the address plan of the cluster, and the labels
# that keep the P-CSCF and the media relay node on the edge nodes.
# It creates no resources and needs no provider, so the values that come
# from the specifications and from deploy/k8s are written only here.

locals {
  # RFC 3261 19.1.2: SIP on the standard port, over UDP and TCP, so that
  # Wireshark decodes it without settings.
  # TCP carries the requests that do not fit in UDP (RFC 3261 18.1.1).
  sip_port = 5060

  # The only two things a UE on the internet talks to: the P-CSCF and
  # the access side of
  # the media relay node. Everything else stays inside the cluster.
  openings = {
    sip-udp = { protocol = "udp", from_port = local.sip_port, to_port = local.sip_port }
    sip-tcp = { protocol = "tcp", from_port = local.sip_port, to_port = local.sip_port }
    rtp-udp = { protocol = "udp", from_port = var.rtp_ports.min, to_port = var.rtp_ports.max }
  }

  rtp_port_list = range(var.rtp_ports.min, var.rtp_ports.max + 1)

  # The default Service range of kind gives 10.96.0.10 to the cluster DNS
  # and leaves 10.96.0.53 to the IMS DNS server. The same host numbers
  # are kept for any other range.
  cluster_dns_ip     = cidrhost(var.service_cidr, 10)
  ims_dns_cluster_ip = coalesce(var.ims_dns_cluster_ip, cidrhost(var.service_cidr, 53))

  # Two ranges overlap when they are equal once both are cut to the
  # shorter prefix. cidrcontains() would say this directly, but it is not
  # in every version of both tools.
  named_ranges = merge(
    { node_network_cidr = var.node_network_cidr, service_cidr = var.service_cidr },
    var.pod_cidr == null ? {} : { pod_cidr = var.pod_cidr },
  )
  range_names = sort(keys(local.named_ranges))
  range_parts = [
    for name in local.range_names : {
      name    = name
      address = split("/", local.named_ranges[name])[0]
      prefix  = tonumber(split("/", local.named_ranges[name])[1])
    }
  ]
  overlapping_ranges = flatten([
    for i, a in local.range_parts : [
      for j, b in local.range_parts : "${a.name} and ${b.name}"
      if i < j && cidrhost("${a.address}/${min(a.prefix, b.prefix)}", 0) == cidrhost("${b.address}/${min(a.prefix, b.prefix)}", 0)
    ]
  ])

  service_prefix_length = tonumber(split("/", var.service_cidr)[1])
  ims_dns_in_service_range = can(cidrhost("${local.ims_dns_cluster_ip}/32", 0)) && try(
    cidrhost("${local.ims_dns_cluster_ip}/${local.service_prefix_length}", 0) == cidrhost(var.service_cidr, 0),
    false
  )

  # Kubernetes takes host 1 of the Service range for the API server's
  # Service, and host 10 is the cluster DNS above.
  ims_dns_reserved = contains([
    cidrhost(var.service_cidr, 0),
    cidrhost(var.service_cidr, 1),
    local.cluster_dns_ip,
  ], local.ims_dns_cluster_ip)
}
