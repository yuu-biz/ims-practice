# Run from deploy/cloud/modules/ims-network with "tofu test". The module
# has no provider, so nothing here reaches a cloud. The addresses are from
# the documentation ranges of RFC 5737.

variables {
  allowed_source_cidrs = ["192.0.2.10/32", "198.51.100.0/24"]
  node_network_cidr    = "10.20.0.0/16"
}

run "defaults_open_only_sip_and_the_rtp_range" {
  command = plan

  assert {
    condition = output.openings == {
      sip-udp = { protocol = "udp", from_port = 5060, to_port = 5060 }
      sip-tcp = { protocol = "tcp", from_port = 5060, to_port = 5060 }
      rtp-udp = { protocol = "udp", from_port = 40000, to_port = 40039 }
    }
    error_message = "Only SIP on 5060 over UDP and TCP and the RTP range of deploy/k8s may be opened."
  }

  assert {
    condition     = length(output.rtp_port_list) == 40 && output.rtp_port_list[0] == 40000 && output.rtp_port_list[39] == 40039
    error_message = "The RTP port list must cover 40000 to 40039."
  }

  assert {
    condition     = output.load_balancer == "none"
    error_message = "The load balancer must be off by default."
  }
}

run "default_service_range_keeps_the_kind_addresses" {
  command = plan

  assert {
    condition     = output.service_cidr == "10.96.0.0/16" && output.cluster_dns_ip == "10.96.0.10" && output.ims_dns_cluster_ip == "10.96.0.53"
    error_message = "The default Service range must give the addresses deploy/k8s uses on kind."
  }
}

run "other_service_range_moves_the_dns_address" {
  command = plan

  variables {
    service_cidr = "172.20.0.0/16"
  }

  assert {
    condition     = output.ims_dns_cluster_ip == "172.20.0.53" && output.cluster_dns_ip == "172.20.0.10"
    error_message = "The DNS addresses must follow the Service range."
  }
}

run "refuses_the_whole_internet" {
  command = plan

  variables {
    allowed_source_cidrs = ["0.0.0.0/0"]
  }

  expect_failures = [var.allowed_source_cidrs]
}

run "refuses_two_halves_of_the_internet" {
  command = plan

  variables {
    allowed_source_cidrs = ["0.0.0.0/1", "128.0.0.0/1"]
  }

  expect_failures = [var.allowed_source_cidrs]
}

run "refuses_an_empty_list" {
  command = plan

  variables {
    allowed_source_cidrs = []
  }

  expect_failures = [var.allowed_source_cidrs]
}

run "refuses_a_bare_address" {
  command = plan

  variables {
    allowed_source_cidrs = ["192.0.2.10"]
  }

  expect_failures = [var.allowed_source_cidrs]
}

run "refuses_a_range_that_is_not_a_network_address" {
  command = plan

  variables {
    allowed_source_cidrs = ["192.0.2.10/24"]
  }

  expect_failures = [var.allowed_source_cidrs]
}

run "refuses_ipv6" {
  command = plan

  variables {
    allowed_source_cidrs = ["2001:db8::/32"]
  }

  expect_failures = [var.allowed_source_cidrs]
}

run "refuses_an_rtp_range_with_the_sip_port" {
  command = plan

  variables {
    rtp_ports = { min = 5000, max = 5100 }
  }

  expect_failures = [var.rtp_ports]
}

run "refuses_a_reversed_rtp_range" {
  command = plan

  variables {
    rtp_ports = { min = 40039, max = 40000 }
  }

  expect_failures = [var.rtp_ports]
}

run "refuses_an_unknown_load_balancer_variant" {
  command = plan

  variables {
    load_balancer = "rtp"
  }

  expect_failures = [var.load_balancer]
}

run "refuses_a_public_service_range" {
  command = plan

  variables {
    service_cidr = "203.0.113.0/24"
  }

  expect_failures = [var.service_cidr]
}

run "refuses_a_service_range_that_overlaps_the_node_network" {
  command = plan

  variables {
    service_cidr = "10.20.128.0/17"
  }

  expect_failures = [output.address_ranges_checked]
}

run "refuses_a_pod_range_that_overlaps_the_service_range" {
  command = plan

  variables {
    pod_cidr = "10.96.0.0/12"
  }

  expect_failures = [output.address_ranges_checked]
}

run "refuses_a_dns_address_outside_the_service_range" {
  command = plan

  variables {
    ims_dns_cluster_ip = "10.97.0.53"
  }

  expect_failures = [output.ims_dns_cluster_ip]
}

run "refuses_the_cluster_dns_address" {
  command = plan

  variables {
    ims_dns_cluster_ip = "10.96.0.10"
  }

  expect_failures = [output.ims_dns_cluster_ip]
}
