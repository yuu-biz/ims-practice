# "tofu test" in deploy/cloud/aws. The AWS provider is replaced by a mock,
# so the plans below need no credentials and call no AWS API. The mock
# fills the computed attributes with made-up values; the ones below are
# set where the provider checks their form (ARNs) or where the
# definitions index into them. 123456789012 is the account number of
# the AWS documentation's examples. The source addresses are from the
# documentation ranges of RFC 5737.

mock_provider "aws" {
  mock_data "aws_availability_zones" {
    defaults = {
      names = ["example-zone-a", "example-zone-b", "example-zone-c"]
    }
  }

  mock_resource "aws_iam_role" {
    defaults = {
      arn = "arn:aws:iam::123456789012:role/mock"
    }
  }

  mock_resource "aws_launch_template" {
    defaults = {
      id             = "lt-0123456789abcdef0"
      latest_version = 1
    }
  }

  mock_resource "aws_lb" {
    defaults = {
      arn = "arn:aws:elasticloadbalancing:us-east-2:123456789012:loadbalancer/net/mock/0123456789abcdef"
    }
  }

  mock_resource "aws_lb_target_group" {
    defaults = {
      arn = "arn:aws:elasticloadbalancing:us-east-2:123456789012:targetgroup/mock/0123456789abcdef"
    }
  }

  mock_resource "aws_eks_node_group" {
    defaults = {
      resources = [{
        autoscaling_groups              = [{ name = "eks-edge-mock" }]
        remote_access_security_group_id = ""
      }]
    }
  }
}

variables {
  region               = "us-east-2"
  allowed_source_cidrs = ["192.0.2.10/32", "198.51.100.0/24"]
}

run "opens_only_sip_and_rtp_to_the_allowed_ranges" {
  command = plan

  assert {
    condition     = length(aws_vpc_security_group_ingress_rule.edge) == 6
    error_message = "There must be one rule for each of the three openings and each of the two allowed ranges."
  }

  assert {
    condition = toset([
      for r in values(aws_vpc_security_group_ingress_rule.edge) : "${r.ip_protocol} ${r.from_port}-${r.to_port}"
    ]) == toset(["udp 5060-5060", "tcp 5060-5060", "udp 40000-40039"])
    error_message = "The edge security group may open only SIP 5060 over UDP and TCP and the RTP range."
  }

  assert {
    condition = alltrue([
      for r in values(aws_vpc_security_group_ingress_rule.edge) : contains(var.allowed_source_cidrs, r.cidr_ipv4)
    ])
    error_message = "Every rule must take its source from allowed_source_cidrs."
  }

  assert {
    condition     = length(aws_vpc_security_group_ingress_rule.edge_health_check) == 0
    error_message = "Without a load balancer there must be no health check rule."
  }

  assert {
    condition     = aws_eks_cluster.ims.vpc_config[0].public_access_cidrs == toset(var.allowed_source_cidrs)
    error_message = "The Kubernetes API must answer only the allowed ranges."
  }

  assert {
    condition     = aws_eks_cluster.ims.vpc_config[0].endpoint_private_access
    error_message = "The nodes must reach the API through the private endpoint."
  }
}

run "edge_nodes_have_public_addresses_and_the_taint" {
  command = plan

  assert {
    condition     = alltrue([for s in aws_subnet.public : s.map_public_ip_on_launch])
    error_message = "The subnets must give the nodes public addresses."
  }

  assert {
    condition     = length(aws_subnet.public) == 2 && aws_subnet.public[0].availability_zone != aws_subnet.public[1].availability_zone
    error_message = "EKS needs subnets in two Availability Zones."
  }

  assert {
    condition     = aws_eks_node_group.edge.labels == tomap({ "ims-practice/pool" = "edge" })
    error_message = "The edge nodes must carry the edge label."
  }

  assert {
    condition = [for t in aws_eks_node_group.edge.taint : t] == [{
      key = "ims-practice/pool", value = "edge", effect = "NO_SCHEDULE"
    }]
    error_message = "The edge nodes must carry the edge taint."
  }

  assert {
    condition     = length(aws_eks_node_group.core.taint) == 0 && aws_eks_node_group.core.labels == tomap({ "ims-practice/pool" = "core" })
    error_message = "The core nodes must have the core label and no taint."
  }

  assert {
    condition     = contains(aws_launch_template.edge.vpc_security_group_ids, aws_security_group.edge.id)
    error_message = "The edge launch template must put the edge security group on the nodes."
  }

  assert {
    condition     = aws_launch_template.core.vpc_security_group_ids == null || length(aws_launch_template.core.vpc_security_group_ids) == 0
    error_message = "The core nodes must not get the edge security group."
  }

  assert {
    condition     = jsondecode(aws_eks_addon.vpc_cni.configuration_values).env.AWS_VPC_K8S_CNI_RANDOMIZESNAT == "none"
    error_message = "The VPC CNI must not randomise the source ports of Pod flows."
  }
}

run "keeps_the_kind_service_range_by_default" {
  command = plan

  assert {
    condition     = aws_eks_cluster.ims.kubernetes_network_config[0].service_ipv4_cidr == "10.96.0.0/16"
    error_message = "The Service range must default to kind's."
  }

  assert {
    condition     = output.kubernetes.ims_dns_cluster_ip == "10.96.0.53" && output.kubernetes.cluster_dns_ip == "10.96.0.10"
    error_message = "The DNS addresses must be those deploy/k8s uses."
  }
}

run "has_no_load_balancer_by_default" {
  command = plan

  assert {
    condition     = length(aws_lb.edge) == 0 && length(aws_eip.load_balancer) == 0 && length(aws_lb_listener.sip) == 0 && length(aws_lb_listener.rtp) == 0
    error_message = "No load balancer and no extra address may be made by default."
  }

  assert {
    condition     = output.load_balancer_address == null && output.load_balancer_listener_count == 0
    error_message = "The outputs must show that there is no load balancer."
  }
}

run "sip_load_balancer" {
  command = plan

  variables {
    load_balancer = "sip"
  }

  assert {
    condition     = length(aws_lb.edge) == 1 && aws_lb.edge[0].load_balancer_type == "network" && !aws_lb.edge[0].internal
    error_message = "\"sip\" must make one internet-facing NLB."
  }

  assert {
    condition     = aws_lb_listener.sip[0].protocol == "TCP_UDP" && aws_lb_listener.sip[0].port == 5060 && aws_lb_target_group.sip[0].protocol == "TCP_UDP"
    error_message = "SIP must use one TCP_UDP listener on 5060."
  }

  assert {
    condition     = length(aws_lb_listener.rtp) == 0 && output.load_balancer_listener_count == 1
    error_message = "\"sip\" must leave RTP on the nodes."
  }

  assert {
    condition     = length(aws_vpc_security_group_ingress_rule.edge_health_check) == 1 && aws_vpc_security_group_ingress_rule.edge_health_check[0].cidr_ipv4 == "10.20.0.0/16"
    error_message = "The health checks must be allowed from the VPC only."
  }

  assert {
    condition     = length(aws_vpc_security_group_ingress_rule.edge) == 6
    error_message = "The load balancer must not add openings to the internet."
  }

  assert {
    condition     = keys(aws_autoscaling_attachment.edge) == ["sip"]
    error_message = "The edge nodes must be registered in the SIP target group."
  }
}

run "sip_and_rtp_load_balancer" {
  command = plan

  variables {
    load_balancer = "sip-and-rtp"
  }

  assert {
    condition     = length(aws_lb_listener.rtp) == 40 && output.load_balancer_listener_count == 41
    error_message = "\"sip-and-rtp\" must add one UDP listener per RTP port."
  }

  assert {
    condition     = alltrue([for tg in values(aws_lb_target_group.rtp) : tg.protocol == "UDP" && tg.health_check[0].port == "5060"])
    error_message = "The RTP target groups must be UDP and checked on the P-CSCF's TCP port."
  }

  assert {
    condition     = length(aws_autoscaling_attachment.edge) == 41
    error_message = "The edge nodes must be registered in every target group."
  }
}

run "fifty_listeners_fit" {
  command = plan

  variables {
    load_balancer = "sip-and-rtp"
    rtp_ports     = { min = 40000, max = 40048 }
  }

  assert {
    condition     = output.load_balancer_listener_count == 50
    error_message = "49 RTP ports and SIP must take exactly the 50 listeners."
  }
}

run "refuses_more_listeners_than_an_nlb_takes" {
  command = plan

  variables {
    load_balancer = "sip-and-rtp"
    rtp_ports     = { min = 40000, max = 40049 }
  }

  expect_failures = [aws_lb.edge]
}

run "wide_rtp_range_without_a_load_balancer" {
  command = plan

  variables {
    rtp_ports = { min = 40000, max = 40999 }
  }

  assert {
    condition     = contains([for r in values(aws_vpc_security_group_ingress_rule.edge) : "${r.from_port}-${r.to_port}"], "40000-40999")
    error_message = "A security group rule takes the whole range at once."
  }
}

run "refuses_a_long_cluster_name" {
  command = plan

  variables {
    cluster_name = "ims-practice-cluster-1"
  }

  expect_failures = [var.cluster_name]
}
