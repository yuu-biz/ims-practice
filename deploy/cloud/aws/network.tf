# The VPC: two public subnets in two Availability Zones (EKS needs
# subnets in at least two), an internet gateway, and nothing private.
# Without private subnets there is no NAT gateway to pay for; every
# node reaches the internet
# through its own public address, which the internet gateway maps one to
# one ("Enable internet access for a VPC using an internet gateway").

module "ims_network" {
  source = "../modules/ims-network"

  allowed_source_cidrs = var.allowed_source_cidrs
  rtp_ports            = var.rtp_ports
  node_network_cidr    = var.vpc_cidr
  # Amazon VPC CNI gives each Pod an address of the VPC, so there is no
  # separate Pod range.
  pod_cidr           = null
  service_cidr       = var.service_cidr
  ims_dns_cluster_ip = var.ims_dns_cluster_ip
  load_balancer      = var.load_balancer
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  availability_zones = slice(data.aws_availability_zones.available.names, 0, 2)
}

resource "aws_vpc" "ims" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = { Name = var.cluster_name }
}

resource "aws_internet_gateway" "ims" {
  vpc_id = aws_vpc.ims.id

  tags = { Name = var.cluster_name }
}

# A managed node group in a public subnet needs the subnet to give each
# instance a public address at launch ("Simplify node lifecycle with
# managed node groups").
resource "aws_subnet" "public" {
  count = length(local.availability_zones)

  vpc_id                  = aws_vpc.ims.id
  availability_zone       = local.availability_zones[count.index]
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  map_public_ip_on_launch = true

  tags = { Name = "${var.cluster_name}-public-${count.index}" }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.ims.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.ims.id
  }

  tags = { Name = "${var.cluster_name}-public" }
}

resource "aws_route_table_association" "public" {
  count = length(aws_subnet.public)

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}
