# "tofu test" in deploy/cloud/azure. The azurerm provider is replaced by a
# mock, so the plans below need no credentials and call no Azure API.
# The mock fills the computed attributes with made-up values; the ones
# below are set where the provider checks their form (resource IDs). The
# subscription is all zeros, and the addresses are from the
# documentation ranges of RFC 5737.

mock_provider "azurerm" {
  mock_resource "azurerm_resource_group" {
    defaults = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ims-practice"
    }
  }

  mock_resource "azurerm_subnet" {
    defaults = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ims-practice/providers/Microsoft.Network/virtualNetworks/ims-practice/subnets/mock"
    }
  }

  mock_resource "azurerm_network_security_group" {
    defaults = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ims-practice/providers/Microsoft.Network/networkSecurityGroups/ims-practice-edge"
    }
  }

  mock_resource "azurerm_public_ip_prefix" {
    defaults = {
      id        = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ims-practice/providers/Microsoft.Network/publicIPPrefixes/ims-practice-edge"
      ip_prefix = "203.0.113.0/31"
    }
  }

  mock_resource "azurerm_public_ip" {
    defaults = {
      ip_address = "203.0.113.10"
    }
  }

  mock_resource "azurerm_kubernetes_cluster" {
    defaults = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ims-practice/providers/Microsoft.ContainerService/managedClusters/ims-practice"
      identity = {
        principal_id = "00000000-0000-0000-0000-000000000001"
        tenant_id    = "00000000-0000-0000-0000-000000000002"
      }
    }
  }
}

variables {
  region               = "eastus2"
  allowed_source_cidrs = ["192.0.2.10/32", "198.51.100.0/24"]
}

run "opens_only_sip_and_rtp_to_the_allowed_ranges" {
  command = plan

  assert {
    condition = toset([
      for r in values(azurerm_network_security_rule.edge) : "${r.protocol} ${r.destination_port_range}"
    ]) == toset(["Udp 5060", "Tcp 5060", "Udp 40000-40039"])
    error_message = "The edge security group may open only SIP 5060 over UDP and TCP and the RTP range."
  }

  assert {
    condition = alltrue([
      for r in values(azurerm_network_security_rule.edge) :
      r.direction == "Inbound" && r.access == "Allow" && r.source_address_prefixes == toset(var.allowed_source_cidrs) && r.source_address_prefix == null
    ])
    error_message = "Every rule must allow only the ranges of allowed_source_cidrs."
  }

  assert {
    condition     = length(distinct([for r in values(azurerm_network_security_rule.edge) : r.priority])) == 3
    error_message = "The rules must have distinct priorities."
  }

  assert {
    condition = toset([
      for p in azurerm_kubernetes_cluster_node_pool.edge.node_network_profile[0].allowed_host_ports : "${p.protocol} ${p.port_start}-${p.port_end}"
    ]) == toset(["UDP 5060-5060", "TCP 5060-5060", "UDP 40000-40039"])
    error_message = "The edge pool may allow only the SIP and RTP host ports."
  }

  assert {
    condition     = azurerm_subnet_network_security_group_association.edge.network_security_group_id == azurerm_network_security_group.edge.id
    error_message = "The security group must sit on the edge subnet."
  }

  assert {
    condition = azurerm_kubernetes_cluster.ims.api_server_access_profile[0].authorized_ip_ranges == toset(
      ["192.0.2.10/32", "198.51.100.0/24", "203.0.113.0/31"]
    )
    error_message = "The API server must answer only the allowed ranges and the edge nodes' prefix."
  }
}

run "edge_nodes_have_public_addresses_and_the_taint" {
  command = plan

  assert {
    condition     = azurerm_kubernetes_cluster_node_pool.edge.node_public_ip_enabled && azurerm_kubernetes_cluster_node_pool.edge.node_public_ip_prefix_id == azurerm_public_ip_prefix.edge.id
    error_message = "The edge nodes must take public addresses from the edge prefix."
  }

  assert {
    condition     = azurerm_public_ip_prefix.edge.prefix_length == 31
    error_message = "One edge node needs the smallest prefix, /31."
  }

  assert {
    condition     = azurerm_kubernetes_cluster_node_pool.edge.node_taints == tolist(["ims-practice/pool=edge:NoSchedule"])
    error_message = "The edge nodes must carry the edge taint."
  }

  assert {
    condition     = azurerm_kubernetes_cluster_node_pool.edge.node_labels == tomap({ "ims-practice/pool" = "edge" })
    error_message = "The edge nodes must carry the edge label."
  }

  assert {
    condition     = !coalesce(azurerm_kubernetes_cluster.ims.default_node_pool[0].node_public_ip_enabled, false) && azurerm_kubernetes_cluster.ims.default_node_pool[0].node_labels == tomap({ "ims-practice/pool" = "core" })
    error_message = "The core nodes must have no public addresses and the core label."
  }

  assert {
    condition     = azurerm_kubernetes_cluster.ims.sku_tier == "Free"
    error_message = "The cluster must use the Free tier."
  }
}

run "larger_edge_pool_takes_a_larger_prefix" {
  command = plan

  variables {
    edge_node_count = 5
  }

  assert {
    condition     = azurerm_public_ip_prefix.edge.prefix_length == 29
    error_message = "Five edge nodes need a /29 (eight addresses)."
  }
}

run "refuses_more_edge_nodes_than_a_prefix_holds" {
  command = plan

  variables {
    edge_node_count = 17
  }

  expect_failures = [var.edge_node_count]
}

run "keeps_the_kind_address_plan_by_default" {
  command = plan

  assert {
    condition = (
      azurerm_kubernetes_cluster.ims.network_profile[0].service_cidr == "10.96.0.0/16" &&
      azurerm_kubernetes_cluster.ims.network_profile[0].dns_service_ip == "10.96.0.10" &&
      azurerm_kubernetes_cluster.ims.network_profile[0].pod_cidr == "10.244.0.0/16" &&
      azurerm_kubernetes_cluster.ims.network_profile[0].network_plugin_mode == "overlay"
    )
    error_message = "The Service and Pod ranges and the cluster DNS must default to kind's."
  }

  assert {
    condition     = output.kubernetes.ims_dns_cluster_ip == "10.96.0.53"
    error_message = "The IMS DNS address must be the one deploy/k8s uses."
  }
}

run "has_no_load_balancer_address_by_default" {
  command = plan

  assert {
    condition     = length(azurerm_public_ip.sip) == 0 && length(azurerm_public_ip.rtp) == 0 && output.load_balancer_public_ips == {}
    error_message = "No load balancer address may be made by default."
  }
}

run "sip_load_balancer" {
  command = plan

  variables {
    load_balancer = "sip"
  }

  assert {
    condition     = length(azurerm_public_ip.sip) == 1 && azurerm_public_ip.sip[0].sku == "Standard" && azurerm_public_ip.sip[0].allocation_method == "Static" && length(azurerm_public_ip.rtp) == 0
    error_message = "\"sip\" must make one static Standard address for SIP only."
  }

  assert {
    condition     = output.load_balancer_public_ips.sip.annotations["service.beta.kubernetes.io/azure-pip-name"] == "ims-practice-sip"
    error_message = "The output must give the annotation for the SIP Service."
  }

  assert {
    condition     = length(azurerm_network_security_rule.edge) == 3
    error_message = "The load balancer must not add openings to the internet."
  }
}

run "sip_and_rtp_load_balancer" {
  command = plan

  variables {
    load_balancer = "sip-and-rtp"
  }

  assert {
    condition     = length(azurerm_public_ip.rtp) == 1 && output.load_balancer_rule_count == 43
    error_message = "\"sip-and-rtp\" must add an address for RTP and count 2 + 40 + 1 rules."
  }
}

run "refuses_more_rules_than_a_network_interface_takes" {
  command = plan

  variables {
    load_balancer = "sip-and-rtp"
    rtp_ports     = { min = 40000, max = 40297 }
  }

  expect_failures = [azurerm_public_ip.rtp]
}
