# The AKS cluster: the system node pool is the core pool (no public
# addresses), and a second pool is the edge pool (a public address on
# every node, the P-CSCF and the media relay node). The Kubernetes
# manifests stay in deploy/k8s and are applied with kubectl, as on kind,
# so that the same manifests serve both.

locals {
  # The smallest public IP prefix that holds one address per edge node:
  # /31 holds 2 and /28 holds 16 ("Public IP address prefix").
  edge_prefix_length = 32 - max(1, ceil(log(var.edge_node_count, 2)))
}

# The edge nodes take their public addresses from this prefix. The range
# is known as soon as the prefix exists, before any node starts, so it
# can be given to the UE and to the API server's allowed list. Which node
# gets which address of it cannot be chosen.
resource "azurerm_public_ip_prefix" "edge" {
  name                = "${var.cluster_name}-edge"
  location            = azurerm_resource_group.ims.location
  resource_group_name = azurerm_resource_group.ims.name
  prefix_length       = local.edge_prefix_length
  sku                 = "Standard"
  tags                = module.ims_network.tags
}

resource "azurerm_kubernetes_cluster" "ims" {
  name                = var.cluster_name
  location            = azurerm_resource_group.ims.location
  resource_group_name = azurerm_resource_group.ims.name
  dns_prefix          = var.cluster_name
  # The Free tier does not charge for managing the cluster ("Free,
  # Standard, and Premium pricing tiers").
  sku_tier = "Free"
  tags     = module.ims_network.tags

  default_node_pool {
    name           = "core"
    vm_size        = var.core_vm_size
    node_count     = var.core_node_count
    vnet_subnet_id = azurerm_subnet.core.id
    node_labels    = { (module.ims_network.core_node_label.key) = module.ims_network.core_node_label.value }
    tags           = module.ims_network.tags
    # Changing the VM size replaces the system pool through a temporary
    # one of this name.
    temporary_name_for_rotation = "coretmp"
  }

  identity {
    type = "SystemAssigned"
  }

  # azurerm 5.11 requires this block. "Manual" keeps to the two node
  # pools defined here; AKS does not add nodes of its own choosing.
  node_provisioning_profile {
    mode = "Manual"
  }

  # Azure CNI Overlay gives the Pods addresses from pod_cidr, outside the
  # virtual network. The cluster DNS takes host 10 of the Service range,
  # as on kind. The core nodes reach the internet through AKS's Standard
  # Load Balancer ("Customize cluster egress with outbound types"); the
  # edge nodes through their own public addresses.
  network_profile {
    network_plugin      = "azure"
    network_plugin_mode = "overlay"
    pod_cidr            = var.pod_cidr
    service_cidr        = module.ims_network.service_cidr
    dns_service_ip      = module.ims_network.cluster_dns_ip
    load_balancer_sku   = "standard"
    outbound_type       = "loadBalancer"
  }

  # The API server answers only the allowed ranges and the edge nodes'
  # prefix. AKS adds its own outbound address, which the core nodes use
  # ("Secure access to the API server using authorized IP address
  # ranges").
  api_server_access_profile {
    authorized_ip_ranges = concat(module.ims_network.allowed_source_cidrs, [azurerm_public_ip_prefix.edge.ip_prefix])
  }
}

resource "azurerm_kubernetes_cluster_node_pool" "edge" {
  name                  = "edge"
  kubernetes_cluster_id = azurerm_kubernetes_cluster.ims.id
  vm_size               = var.edge_vm_size
  node_count            = var.edge_node_count
  vnet_subnet_id        = azurerm_subnet.edge.id
  mode                  = "User"
  tags                  = module.ims_network.tags

  node_public_ip_enabled   = true
  node_public_ip_prefix_id = azurerm_public_ip_prefix.edge.id

  node_labels = { (module.ims_network.edge_node_label.key) = module.ims_network.edge_node_label.value }
  node_taints = ["${module.ims_network.edge_node_taint.key}=${module.ims_network.edge_node_taint.value}:${module.ims_network.edge_node_taint.effect}"]

  # Traffic to a hostPort is refused by the cluster's default security
  # rules unless the node pool allows the port ("Use instance-level
  # public IPs in AKS").
  node_network_profile {
    dynamic "allowed_host_ports" {
      for_each = module.ims_network.openings
      content {
        protocol   = upper(allowed_host_ports.value.protocol)
        port_start = allowed_host_ports.value.from_port
        port_end   = allowed_host_ports.value.to_port
      }
    }
  }

  depends_on = [azurerm_subnet_network_security_group_association.edge]
}

# The cluster's identity joins the nodes to the subnets of this network
# and, with a load balancer, uses the public addresses below, which are
# outside the node resource group ("Use a static public IP address and
# DNS label with the AKS load balancer").
resource "azurerm_role_assignment" "cluster_network" {
  scope                = azurerm_resource_group.ims.id
  role_definition_name = "Network Contributor"
  principal_id         = azurerm_kubernetes_cluster.ims.identity[0].principal_id
}
