# The optional load balancer variant (load_balancer = "sip" or
# "sip-and-rtp", options 2 and 3 of the variable load_balancer). It
# is off by default.
#
# On AKS the cluster's own cloud provider makes the load balancer rules
# from a Kubernetes Service of type LoadBalancer, on the Standard Load
# Balancer AKS already has. What is made here is only the fixed public
# address of each Service. The Service names it with the annotations
# service.beta.kubernetes.io/azure-pip-name and
# service.beta.kubernetes.io/azure-load-balancer-resource-group ("Use a
# static public IP address and DNS label with the AKS load balancer");
# those Services belong to the cloud overlay of deploy/k8s
# (components/azure-load-balancer-sip and -rtp).
#
# What the load balancer does with UDP, as the Azure Load Balancer
# documentation describes it:
# floating IP is on by default, so the Pod sees the UE's address; the
# documents give no idle time for UDP rules; one network interface takes
# at most 300 rules (load balancing, inbound NAT and outbound together).

locals {
  lb_sip = module.ims_network.load_balancer != "none"
  lb_rtp = module.ims_network.load_balancer == "sip-and-rtp"

  # SIP takes one rule for UDP and one for TCP; RTP one per port; and the
  # core nodes' outbound rule counts against the same limit.
  lb_rule_count = (local.lb_sip ? 2 : 0) + (local.lb_rtp ? length(module.ims_network.rtp_port_list) : 0) + 1
  lb_rule_quota = 300
}

resource "azurerm_public_ip" "sip" {
  count = local.lb_sip ? 1 : 0

  name                = "${var.cluster_name}-sip"
  location            = azurerm_resource_group.ims.location
  resource_group_name = azurerm_resource_group.ims.name
  allocation_method   = "Static"
  sku                 = "Standard"
  tags                = module.ims_network.tags
}

resource "azurerm_public_ip" "rtp" {
  count = local.lb_rtp ? 1 : 0

  name                = "${var.cluster_name}-rtp"
  location            = azurerm_resource_group.ims.location
  resource_group_name = azurerm_resource_group.ims.name
  allocation_method   = "Static"
  sku                 = "Standard"
  tags                = module.ims_network.tags

  lifecycle {
    precondition {
      condition     = local.lb_rule_count <= local.lb_rule_quota
      error_message = "This needs ${local.lb_rule_count} rules on each node's network interface, and Azure takes at most ${local.lb_rule_quota}. Narrow rtp_ports or use load_balancer = \"sip\"."
    }
  }
}
