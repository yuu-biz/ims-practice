# The settings of the AWS definitions. The names are the same as in
# deploy/cloud/azure. The common ones are checked in
# deploy/cloud/modules/ims-network.

variable "region" {
  description = "The AWS Region, for example us-east-2. There is no default: the prices and the place of your data depend on it."
  type        = string
}

variable "cluster_name" {
  description = "The name of the EKS cluster. It also starts the names of the other resources."
  type        = string
  default     = "ims-practice"

  # The longest name built from it is the target group of an RTP port,
  # "<cluster_name>-rtp-NNNNN", and target group names stop at 32
  # characters.
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,19}$", var.cluster_name))
    error_message = "cluster_name must be 1 to 20 lower-case letters, digits or hyphens, starting with a letter."
  }
}

variable "vpc_cidr" {
  description = "The address range of the VPC. Each public subnet takes a /24 of it."
  type        = string
  default     = "10.20.0.0/16"
}

variable "edge_instance_type" {
  description = "The EC2 instance type of the edge nodes, which run the P-CSCF and the media relay node and have public addresses."
  type        = string
  default     = "t3.medium"
}

variable "edge_node_count" {
  description = "The number of edge nodes. Each edge node runs at most one P-CSCF and one media relay node, because both take their ports on the node (hostPort)."
  type        = number
  default     = 1

  validation {
    condition     = var.edge_node_count >= 1 && floor(var.edge_node_count) == var.edge_node_count
    error_message = "edge_node_count must be a whole number of at least 1."
  }
}

variable "core_instance_type" {
  description = "The EC2 instance type of the core nodes, which run the other roles."
  type        = string
  default     = "t3.medium"
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
  description = "The Kubernetes Service range. It can be set only when the cluster is created. The default keeps the addresses deploy/k8s uses on kind."
  type        = string
  default     = "10.96.0.0/16"
}

variable "ims_dns_cluster_ip" {
  description = "The fixed ClusterIP of the IMS DNS server. null takes host 53 of service_cidr."
  type        = string
  default     = null
}

variable "load_balancer" {
  description = "\"none\" (default), \"sip\" or \"sip-and-rtp\": which traffic goes through a Network Load Balancer. An NLB forgets a UDP flow after 120 seconds without packets, and that time cannot be changed; one NLB takes at most 50 listeners."
  type        = string
  default     = "none"
}
