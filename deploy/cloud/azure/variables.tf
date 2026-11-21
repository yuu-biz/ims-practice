# The settings of the Azure definitions. The names are the same as in
# deploy/cloud/aws. The common ones are checked in
# deploy/cloud/modules/ims-network.

variable "region" {
  description = "The Azure region (location), for example eastus2. There is no default: the prices and the place of your data depend on it."
  type        = string
}

variable "cluster_name" {
  description = "The name of the AKS cluster. It also names the resource group and starts the names of the other resources."
  type        = string
  default     = "ims-practice"

  # The same rule as on AWS, so that one name works on both clouds.
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,19}$", var.cluster_name))
    error_message = "cluster_name must be 1 to 20 lower-case letters, digits or hyphens, starting with a letter."
  }
}

variable "vnet_cidr" {
  description = "The address range of the virtual network. The core and edge subnets each take a /24 of it."
  type        = string
  default     = "10.20.0.0/16"
}

variable "edge_vm_size" {
  description = "The VM size of the edge nodes, which run the P-CSCF and the media relay node and have public addresses."
  type        = string
  default     = "Standard_D2s_v5"
}

variable "edge_node_count" {
  description = "The number of edge nodes. Each edge node runs at most one P-CSCF and one media relay node, because both take their ports on the node (hostPort)."
  type        = number
  default     = 1

  # The edge nodes take their addresses from one public IP prefix, and
  # the smallest prefix (/31) holds two; the largest allowed here (/28)
  # holds 16.
  validation {
    condition     = var.edge_node_count >= 1 && var.edge_node_count <= 16 && floor(var.edge_node_count) == var.edge_node_count
    error_message = "edge_node_count must be a whole number from 1 to 16."
  }
}

variable "core_vm_size" {
  description = "The VM size of the core nodes (the system node pool), which run the other roles."
  type        = string
  default     = "Standard_D2s_v5"
}

variable "core_node_count" {
  description = "The number of core nodes."
  type        = number
  default     = 1

  validation {
    condition     = var.core_node_count >= 1 && floor(var.core_node_count) == var.core_node_count
    error_message = "core_node_count must be a whole number of at least 1."
  }
}

variable "allowed_source_cidrs" {
  description = "IPv4 ranges that may reach SIP, RTP and the Kubernetes API. Required; 0.0.0.0/0 is refused. See deploy/cloud/modules/ims-network."
  type        = list(string)
}

variable "rtp_ports" {
  description = "The RTP and RTCP ports of the media relay node."
  type = object({
    min = number
    max = number
  })
  default = { min = 40000, max = 40039 }
}

variable "service_cidr" {
  description = "The Kubernetes Service range. The default keeps the addresses deploy/k8s uses on kind."
  type        = string
  default     = "10.96.0.0/16"
}

variable "pod_cidr" {
  description = "The Pod range of Azure CNI Overlay. The default is kind's Pod range, which the capture filter of deploy/k8s uses."
  type        = string
  default     = "10.244.0.0/16"
}

variable "ims_dns_cluster_ip" {
  description = "The fixed ClusterIP of the IMS DNS server. null takes host 53 of service_cidr."
  type        = string
  default     = null
}

variable "load_balancer" {
  description = "\"none\" (default), \"sip\" or \"sip-and-rtp\": which traffic goes through AKS's Standard Load Balancer. Here only the static public addresses are made; the LoadBalancer Services of the cloud overlay of deploy/k8s make the rules."
  type        = string
  default     = "none"
}
