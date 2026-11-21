output "allowed_source_cidrs" {
  description = "The validated source ranges."
  value       = var.allowed_source_cidrs
}

output "sip_port" {
  description = "The SIP port, the standard one."
  value       = local.sip_port
}

output "openings" {
  description = "The ports the internet may reach on the edge nodes, by name, each with its protocol and port range."
  value       = local.openings
}

output "rtp_ports" {
  description = "The RTP and RTCP port range."
  value       = var.rtp_ports
}

output "rtp_port_list" {
  description = "Each port of the RTP range, for load balancers that take one port per rule."
  value       = local.rtp_port_list
}

output "load_balancer" {
  description = "The validated load balancer variant."
  value       = var.load_balancer
}

output "service_cidr" {
  description = "The Kubernetes Service range."
  value       = var.service_cidr
}

output "cluster_dns_ip" {
  description = "The ClusterIP of the cluster DNS (kube-dns)."
  value       = local.cluster_dns_ip
}

output "ims_dns_cluster_ip" {
  description = "The fixed ClusterIP of the IMS DNS server for deploy/k8s/overlays/external-dns."
  value       = local.ims_dns_cluster_ip

  precondition {
    condition     = local.ims_dns_in_service_range && !local.ims_dns_reserved
    error_message = "ims_dns_cluster_ip must be an address inside service_cidr other than its hosts 0, 1 and 10."
  }
}

output "address_ranges_checked" {
  description = "The node, Service and Pod ranges, after the check that none of them overlaps another."
  value       = local.named_ranges

  precondition {
    condition     = length(local.overlapping_ranges) == 0
    error_message = "These address ranges overlap: ${join(", ", local.overlapping_ranges)}."
  }
}

# The node pool for the P-CSCF and the media relay node. Only Pods that
# tolerate the taint run there (the component edge of deploy/k8s), so
# nothing else is exposed on the public addresses.
output "edge_node_label" {
  description = "The label of the edge nodes."
  value       = { key = "ims-practice/pool", value = "edge" }
}

output "core_node_label" {
  description = "The label of the nodes without public addresses."
  value       = { key = "ims-practice/pool", value = "core" }
}

output "edge_node_taint" {
  description = "The taint of the edge nodes, in Kubernetes terms."
  value       = { key = "ims-practice/pool", value = "edge", effect = "NoSchedule" }
}

# Every resource carries this tag, so that what is left after teardown
# can be found by it.
output "tags" {
  description = "The tags for every resource."
  value       = { "ims-practice" = "cloud" }
}
