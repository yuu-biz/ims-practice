# The resource group, the virtual network with a core and an edge
# subnet, and the network security group of the edge subnet.

module "ims_network" {
  source = "../modules/ims-network"

  allowed_source_cidrs = var.allowed_source_cidrs
  rtp_ports            = var.rtp_ports
  node_network_cidr    = var.vnet_cidr
  pod_cidr             = var.pod_cidr
  service_cidr         = var.service_cidr
  ims_dns_cluster_ip   = var.ims_dns_cluster_ip
  load_balancer        = var.load_balancer
}

resource "azurerm_resource_group" "ims" {
  name     = var.cluster_name
  location = var.region
  tags     = module.ims_network.tags
}

resource "azurerm_virtual_network" "ims" {
  name                = var.cluster_name
  location            = azurerm_resource_group.ims.location
  resource_group_name = azurerm_resource_group.ims.name
  address_space       = [var.vnet_cidr]
  tags                = module.ims_network.tags
}

resource "azurerm_subnet" "core" {
  name                 = "core"
  resource_group_name  = azurerm_resource_group.ims.name
  virtual_network_name = azurerm_virtual_network.ims.name
  address_prefixes     = [cidrsubnet(var.vnet_cidr, 8, 0)]
}

resource "azurerm_subnet" "edge" {
  name                 = "edge"
  resource_group_name  = azurerm_resource_group.ims.name
  virtual_network_name = azurerm_virtual_network.ims.name
  address_prefixes     = [cidrsubnet(var.vnet_cidr, 8, 1)]
}

# The ports the node pool allows on the host (allowed_host_ports, in
# cluster.tf) become allow rules in the cluster's own security group. A
# security group on a subnet of one's own network must allow the same
# traffic too ("Use instance-level public IPs in AKS"). This one is
# where the sources are restricted: the default rules of a security group
# let the virtual network and the load balancer's probes in and refuse
# everything else from the internet.
resource "azurerm_network_security_group" "edge" {
  name                = "${var.cluster_name}-edge"
  location            = azurerm_resource_group.ims.location
  resource_group_name = azurerm_resource_group.ims.name
  tags                = module.ims_network.tags
}

locals {
  # Priorities 100, 110, 120 in the order of the opening names.
  edge_rules = {
    for i, name in sort(keys(module.ims_network.openings)) : name => merge(module.ims_network.openings[name], {
      priority = 100 + 10 * i
    })
  }
}

resource "azurerm_network_security_rule" "edge" {
  for_each = local.edge_rules

  name                        = each.key
  resource_group_name         = azurerm_resource_group.ims.name
  network_security_group_name = azurerm_network_security_group.edge.name
  direction                   = "Inbound"
  access                      = "Allow"
  priority                    = each.value.priority
  protocol                    = title(each.value.protocol)
  source_address_prefixes     = module.ims_network.allowed_source_cidrs
  source_port_range           = "*"
  destination_address_prefix  = "*"
  destination_port_range      = each.value.from_port == each.value.to_port ? tostring(each.value.from_port) : "${each.value.from_port}-${each.value.to_port}"
}

resource "azurerm_subnet_network_security_group_association" "edge" {
  subnet_id                 = azurerm_subnet.edge.id
  network_security_group_id = azurerm_network_security_group.edge.id
}
