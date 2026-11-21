# The settings that are the same on every cloud. Each cloud's root
# module (deploy/cloud/aws, deploy/cloud/azure) declares the same
# variables and passes them here, so the checks below are written once.
# The roots do not repeat these validations.

variable "allowed_source_cidrs" {
  description = "IPv4 ranges that may reach SIP and RTP on the edge nodes and the Kubernetes API, for example your own line's address as a /32. There is no default: the IMS runs SIP and RTP in cleartext, so that captures can be read, and the test subscribers' passwords are in the repository."
  type        = list(string)
  nullable    = false

  validation {
    condition     = length(var.allowed_source_cidrs) > 0
    error_message = "Set allowed_source_cidrs to the addresses you will connect from, for example [\"192.0.2.10/32\"]."
  }

  validation {
    condition     = alltrue([for c in var.allowed_source_cidrs : can(cidrnetmask(c))])
    error_message = "Each entry of allowed_source_cidrs must be an IPv4 range in CIDR form, such as 192.0.2.10/32."
  }

  # Refuses 0.0.0.0/0 and any range so wide that it amounts to the same,
  # such as 0.0.0.0/1 with 128.0.0.0/1.
  validation {
    condition     = alltrue([for c in var.allowed_source_cidrs : can(cidrnetmask(c)) && tonumber(try(split("/", c)[1], "0")) >= 8])
    error_message = "allowed_source_cidrs must not open SIP and RTP to the whole internet: 0.0.0.0/0 and ranges wider than /8 are refused."
  }

  # 192.0.2.10/24 is most likely a typing mistake for 192.0.2.0/24 or
  # 192.0.2.10/32, so it is refused rather than widened silently.
  validation {
    condition     = alltrue([for c in var.allowed_source_cidrs : can(cidrnetmask(c)) && try(cidrhost(c, 0) == split("/", c)[0], false)])
    error_message = "Write each range of allowed_source_cidrs with its network address, for example 192.0.2.0/24 or 192.0.2.10/32."
  }
}

variable "rtp_ports" {
  description = "The RTP and RTCP ports of the media relay node (agw). The default is the range of deploy/k8s/base/config/agw.yaml (rtp_ports)."
  type = object({
    min = number
    max = number
  })
  default  = { min = 40000, max = 40039 }
  nullable = false

  validation {
    condition     = var.rtp_ports.min >= 1024 && var.rtp_ports.max <= 65535 && var.rtp_ports.min <= var.rtp_ports.max
    error_message = "rtp_ports must be a range inside 1024 to 65535 with min <= max."
  }

  # 5060 belongs to SIP (RFC 3261 19.1.2).
  validation {
    condition     = var.rtp_ports.min > 5060 || var.rtp_ports.max < 5060
    error_message = "rtp_ports must not contain the SIP port 5060."
  }
}

variable "node_network_cidr" {
  description = "The address range of the VPC or VNet. The root module passes its own setting."
  type        = string

  validation {
    condition     = can(cidrnetmask(var.node_network_cidr))
    error_message = "node_network_cidr must be an IPv4 range in CIDR form."
  }
}

variable "pod_cidr" {
  description = "The Pod range when Pods have addresses outside the node network (Azure CNI Overlay). null when Pods take addresses from the node network (Amazon VPC CNI)."
  type        = string
  default     = null

  validation {
    condition     = var.pod_cidr == null || can(cidrnetmask(var.pod_cidr))
    error_message = "pod_cidr must be null or an IPv4 range in CIDR form."
  }
}

variable "service_cidr" {
  description = "The Kubernetes Service range. The default is kind's, so that the fixed ClusterIP of the IMS DNS server in deploy/k8s (10.96.0.53) stays valid."
  type        = string
  default     = "10.96.0.0/16"
  nullable    = false

  # EKS accepts a range inside 10.0.0.0/8, 172.16.0.0/12 or
  # 192.168.0.0/16 between /24 and /12 ("Create an Amazon EKS cluster").
  # AKS is held to the same rule so that both clouds take the same value.
  validation {
    condition = can(cidrnetmask(var.service_cidr)) && try(
      tonumber(split("/", var.service_cidr)[1]) >= 12 &&
      tonumber(split("/", var.service_cidr)[1]) <= 24 &&
      cidrhost(var.service_cidr, 0) == split("/", var.service_cidr)[0] &&
      anytrue([
        for private in ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"] :
        tonumber(split("/", var.service_cidr)[1]) >= tonumber(split("/", private)[1]) &&
        cidrhost("${split("/", var.service_cidr)[0]}/${split("/", private)[1]}", 0) == cidrhost(private, 0)
      ]),
      false
    )
    error_message = "service_cidr must be a network address range between /12 and /24 inside 10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16."
  }
}

variable "ims_dns_cluster_ip" {
  description = "The fixed ClusterIP of the IMS DNS server (Service dns of deploy/k8s/overlays/external-dns). null takes host 53 of service_cidr, which is 10.96.0.53 for the default range."
  type        = string
  default     = null
}

variable "load_balancer" {
  description = "Which traffic goes through a cloud load balancer. \"none\": the edge nodes' public addresses and hostPort only (option 1, recommended). \"sip\": SIP through a load balancer, RTP on the nodes (option 2). \"sip-and-rtp\": both (option 3)."
  type        = string
  default     = "none"
  nullable    = false

  validation {
    condition     = contains(["none", "sip", "sip-and-rtp"], var.load_balancer)
    error_message = "load_balancer must be \"none\", \"sip\" or \"sip-and-rtp\"."
  }
}
