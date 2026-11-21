# What the manual steps after "tofu apply" need (deploy/cloud/README.md).
# "tofu output -json" prints all of them.

output "cluster_name" {
  description = "The AKS cluster."
  value       = azurerm_kubernetes_cluster.ims.name
}

output "resource_group_name" {
  description = "The resource group of the definitions."
  value       = azurerm_resource_group.ims.name
}

output "node_resource_group" {
  description = "The resource group AKS makes for the nodes (MC_...). Deleting the cluster deletes it."
  value       = azurerm_kubernetes_cluster.ims.node_resource_group
}

output "kubeconfig_command" {
  description = "Run this to point kubectl at the cluster."
  value       = "az aks get-credentials --resource-group ${azurerm_resource_group.ims.name} --name ${azurerm_kubernetes_cluster.ims.name}"
}

output "edge_public_ip_prefix" {
  description = "The range the edge nodes take their public addresses from. The UE sends SIP to one of them unless load_balancer is set."
  value       = azurerm_public_ip_prefix.edge.ip_prefix
}

output "edge_public_addresses_command" {
  description = "Run this to see which edge node has which address (the EXTERNAL-IP column)."
  value       = "kubectl get nodes -l ${module.ims_network.edge_node_label.key}=${module.ims_network.edge_node_label.value} -o wide"
}

output "load_balancer_public_ips" {
  description = "The fixed addresses for the LoadBalancer Services of the cloud overlay, with the values of their annotations, or an empty map without a load balancer."
  value = {
    for name, ip in merge(
      { for p in azurerm_public_ip.sip : "sip" => p },
      { for p in azurerm_public_ip.rtp : "rtp" => p },
      ) : name => {
      address = ip.ip_address
      annotations = {
        "service.beta.kubernetes.io/azure-pip-name"                     = ip.name
        "service.beta.kubernetes.io/azure-load-balancer-resource-group" = azurerm_resource_group.ims.name
      }
    }
  }
}

output "load_balancer_rule_count" {
  description = "How many of the 300 rules of a node's network interface the load balancer variant needs."
  value       = local.lb_rule_count
}

# The values the Kubernetes overlays for the cloud need
# (deploy/k8s/overlays/cloud-azure).
output "kubernetes" {
  description = "Values for deploy/k8s: the Service range, the cluster DNS and IMS DNS addresses, the Pod range for the capture filter, the edge node label and taint, and the RTP ports."
  value = {
    service_cidr       = module.ims_network.service_cidr
    cluster_dns_ip     = module.ims_network.cluster_dns_ip
    ims_dns_cluster_ip = module.ims_network.ims_dns_cluster_ip
    pod_cidr           = var.pod_cidr
    edge_node_label    = module.ims_network.edge_node_label
    edge_node_taint    = module.ims_network.edge_node_taint
    rtp_ports          = module.ims_network.rtp_ports
    address_ranges     = module.ims_network.address_ranges_checked
  }
}
