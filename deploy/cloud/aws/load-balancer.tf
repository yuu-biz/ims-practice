# The optional Network Load Balancer (load_balancer = "sip" or
# "sip-and-rtp", options 2 and 3 of the variable load_balancer). It
# is off by default.
#
# The NLB is made here, not by a Kubernetes Service, and sends to the
# edge nodes' instances on the same ports the Pods take with hostPort. So
# no AWS Load Balancer Controller has to be installed, and both options
# reach the same Pods as option 1.
#
# What the NLB does with UDP, as the Elastic Load Balancing
# documentation describes it:
# - The client address is always kept for UDP and TCP_UDP target groups,
#   so the P-CSCF still sees the UE's public address, and the edge
#   security group still filters by the allowed ranges.
# - A UDP flow without packets for 120 seconds is forgotten, and the time
#   cannot be changed. The UE has to send more often than that.
# - One NLB takes at most 50 listeners, and that cannot be raised.

locals {
  lb_enabled   = module.ims_network.load_balancer != "none"
  lb_rtp_ports = module.ims_network.load_balancer == "sip-and-rtp" ? module.ims_network.rtp_port_list : []

  # One TCP_UDP listener for SIP and one UDP listener per RTP port.
  lb_listener_count  = local.lb_enabled ? 1 + length(local.lb_rtp_ports) : 0
  nlb_listener_quota = 50
}

# A fixed address, so that the UE can be given it before the NLB exists
# and it stays the same until teardown. It is billed by the hour like any
# public IPv4 address.
resource "aws_eip" "load_balancer" {
  count = local.lb_enabled ? 1 : 0

  domain = "vpc"
  tags   = { Name = "${var.cluster_name}-load-balancer" }
}

# One subnet and one address keep a single SIP address for the UE. The
# edge node may be in the other zone, so the NLB sends across zones.
resource "aws_lb" "edge" {
  count = local.lb_enabled ? 1 : 0

  name                             = "${var.cluster_name}-edge"
  load_balancer_type               = "network"
  internal                         = false
  enable_cross_zone_load_balancing = true

  subnet_mapping {
    subnet_id     = aws_subnet.public[0].id
    allocation_id = aws_eip.load_balancer[0].id
  }

  lifecycle {
    precondition {
      condition     = local.lb_listener_count <= local.nlb_listener_quota
      error_message = "This needs ${local.lb_listener_count} NLB listeners, and one NLB takes at most ${local.nlb_listener_quota}. Narrow rtp_ports or use load_balancer = \"sip\"."
    }
  }
}

# The same port takes UDP and TCP only through a TCP_UDP listener and a
# TCP_UDP target group ("Listeners for your Network Load Balancers").
resource "aws_lb_target_group" "sip" {
  count = local.lb_enabled ? 1 : 0

  name        = "${var.cluster_name}-sip"
  vpc_id      = aws_vpc.ims.id
  target_type = "instance"
  protocol    = "TCP_UDP"
  port        = module.ims_network.sip_port

  health_check {
    protocol = "TCP"
    port     = tostring(module.ims_network.sip_port)
  }
}

resource "aws_lb_listener" "sip" {
  count = local.lb_enabled ? 1 : 0

  load_balancer_arn = aws_lb.edge[0].arn
  protocol          = "TCP_UDP"
  port              = module.ims_network.sip_port

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.sip[0].arn
  }
}

# Nothing on the media relay node answers TCP on the RTP ports, so a UDP
# target group is checked on another port of the same target ("Health
# checks for Network Load Balancer target groups"). The P-CSCF's TCP
# port is used.
# SIMPLIFIED: the check shows that the
# P-CSCF on the node is up, not that the media relay node is; the two run
# together on every edge node.
resource "aws_lb_target_group" "rtp" {
  for_each = toset([for p in local.lb_rtp_ports : tostring(p)])

  name        = "${var.cluster_name}-rtp-${each.value}"
  vpc_id      = aws_vpc.ims.id
  target_type = "instance"
  protocol    = "UDP"
  port        = tonumber(each.value)

  health_check {
    protocol = "TCP"
    port     = tostring(module.ims_network.sip_port)
  }
}

resource "aws_lb_listener" "rtp" {
  for_each = aws_lb_target_group.rtp

  load_balancer_arn = aws_lb.edge[0].arn
  protocol          = "UDP"
  port              = each.value.port

  default_action {
    type             = "forward"
    target_group_arn = each.value.arn
  }
}

# The edge node group's Auto Scaling group registers its instances in
# every target group, also the ones that replace a node later.
locals {
  lb_target_groups = merge(
    { for tg in aws_lb_target_group.sip : "sip" => tg.arn },
    { for port, tg in aws_lb_target_group.rtp : "rtp-${port}" => tg.arn },
  )
}

resource "aws_autoscaling_attachment" "edge" {
  for_each = local.lb_target_groups

  autoscaling_group_name = aws_eks_node_group.edge.resources[0].autoscaling_groups[0].name
  lb_target_group_arn    = each.value
}
