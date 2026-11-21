# The security group of the edge nodes. With public addresses on the
# nodes, this is the only wall between the IMS and the internet, so it
# opens exactly the ports of
# module.ims_network.openings to exactly the allowed ranges.
#
# A security group made here starts with no rules at all. The edge nodes
# also carry the cluster security group, which lets the nodes and the
# control plane talk and lets every node send to the internet.

resource "aws_security_group" "edge" {
  name        = "${var.cluster_name}-edge"
  description = "SIP and RTP from the allowed ranges to the edge nodes"
  vpc_id      = aws_vpc.ims.id

  tags = { Name = "${var.cluster_name}-edge" }
}

locals {
  # One rule for each opening and each allowed range.
  edge_ingress = {
    for pair in setproduct(keys(module.ims_network.openings), module.ims_network.allowed_source_cidrs) :
    "${pair[0]} ${pair[1]}" => merge(module.ims_network.openings[pair[0]], { name = pair[0], cidr = pair[1] })
  }
}

resource "aws_vpc_security_group_ingress_rule" "edge" {
  for_each = local.edge_ingress

  security_group_id = aws_security_group.edge.id
  description       = each.value.name
  cidr_ipv4         = each.value.cidr
  ip_protocol       = each.value.protocol
  from_port         = each.value.from_port
  to_port           = each.value.to_port
}

# With a load balancer, its health checks come from its own addresses in
# the VPC ("Health checks for Network Load Balancer target groups"). Every
# target group is checked on the P-CSCF's TCP port (load-balancer.tf).
resource "aws_vpc_security_group_ingress_rule" "edge_health_check" {
  count = module.ims_network.load_balancer == "none" ? 0 : 1

  security_group_id = aws_security_group.edge.id
  description       = "load balancer health checks"
  cidr_ipv4         = var.vpc_cidr
  ip_protocol       = "tcp"
  from_port         = module.ims_network.sip_port
  to_port           = module.ims_network.sip_port
}
