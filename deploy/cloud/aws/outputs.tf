# What the manual steps after "tofu apply" need (deploy/cloud/README.md).
# "tofu output -json" prints all of them.

output "cluster_name" {
  description = "The EKS cluster."
  value       = aws_eks_cluster.ims.name
}

output "region" {
  description = "The AWS Region."
  value       = var.region
}

output "kubeconfig_command" {
  description = "Run this to point kubectl at the cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${aws_eks_cluster.ims.name}"
}

# The edge nodes take their public addresses when they start, so they
# are not known to the definitions.
output "edge_public_addresses_command" {
  description = "Run this to list the public addresses of the edge nodes. The UE sends SIP to one of them unless load_balancer is set."
  value       = "aws ec2 describe-instances --region ${var.region} --filters Name=tag:Name,Values=${aws_eks_cluster.ims.name}-edge Name=instance-state-name,Values=running --query 'Reservations[].Instances[].PublicIpAddress' --output text"
}

output "load_balancer_address" {
  description = "The NLB's fixed public address for SIP (and RTP with \"sip-and-rtp\"), or null without a load balancer."
  value       = local.lb_enabled ? aws_eip.load_balancer[0].public_ip : null
}

output "load_balancer_listener_count" {
  description = "How many of the NLB's 50 listeners are used."
  value       = local.lb_listener_count
}

# The values the Kubernetes overlays for the cloud need
# (deploy/k8s/overlays/cloud-aws).
output "kubernetes" {
  description = "Values for deploy/k8s: the Service range, the cluster DNS and IMS DNS addresses, the Pod range for the capture filter, the edge node label and taint, and the RTP ports."
  value = {
    service_cidr       = module.ims_network.service_cidr
    cluster_dns_ip     = module.ims_network.cluster_dns_ip
    ims_dns_cluster_ip = module.ims_network.ims_dns_cluster_ip
    # Amazon VPC CNI takes Pod addresses from the VPC.
    pod_cidr        = var.vpc_cidr
    edge_node_label = module.ims_network.edge_node_label
    edge_node_taint = module.ims_network.edge_node_taint
    rtp_ports       = module.ims_network.rtp_ports
    address_ranges  = module.ims_network.address_ranges_checked
  }
}
