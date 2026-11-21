# The EKS cluster and its two managed node groups: edge (public
# addresses, the P-CSCF and the media relay node) and core (the other
# roles). The Kubernetes manifests stay in deploy/k8s and are applied with
# kubectl, as on kind, so that the same manifests serve both.

resource "aws_iam_role" "cluster" {
  name = "${var.cluster_name}-cluster"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "eks.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "cluster" {
  role       = aws_iam_role.cluster.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

resource "aws_eks_cluster" "ims" {
  name     = var.cluster_name
  role_arn = aws_iam_role.cluster.arn

  # Whoever runs "tofu apply" becomes the cluster's administrator, so that
  # kubectl works right after "aws eks update-kubeconfig".
  access_config {
    authentication_mode                         = "API"
    bootstrap_cluster_creator_admin_permissions = true
  }

  # The public endpoint answers only the allowed ranges. The nodes use the
  # private endpoint, since their public addresses are not in that list
  # ("Cluster API server endpoint").
  vpc_config {
    subnet_ids              = aws_subnet.public[*].id
    endpoint_public_access  = true
    endpoint_private_access = true
    public_access_cidrs     = module.ims_network.allowed_source_cidrs
  }

  kubernetes_network_config {
    service_ipv4_cidr = module.ims_network.service_cidr
  }

  depends_on = [aws_iam_role_policy_attachment.cluster]
}

# By default the VPC CNI gives a new
# outgoing flow of a Pod a random source port on the node. A request the
# P-CSCF starts after the UE's flow has expired would then leave from a
# port other than 5060. "none" turns the random ports off (the README of
# aws/amazon-vpc-cni-k8s). Whether the port 5060 is then kept has not
# been checked on a real cluster.
resource "aws_eks_addon" "vpc_cni" {
  cluster_name                = aws_eks_cluster.ims.name
  addon_name                  = "vpc-cni"
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  configuration_values = jsonencode({
    env = {
      AWS_VPC_K8S_CNI_RANDOMIZESNAT = "none"
    }
  })
}

resource "aws_iam_role" "node" {
  name = "${var.cluster_name}-node"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "node" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
  ])

  role       = aws_iam_role.node.name
  policy_arn = each.value
}

# The launch templates exist so that the instances, their disks and their
# network interfaces carry the project's tag too; the provider's default
# tags reach only the resources it creates itself.
resource "aws_launch_template" "core" {
  name_prefix = "${var.cluster_name}-core-"

  tag_specifications {
    resource_type = "instance"
    tags          = merge(module.ims_network.tags, { Name = "${var.cluster_name}-core" })
  }
  tag_specifications {
    resource_type = "volume"
    tags          = module.ims_network.tags
  }
  tag_specifications {
    resource_type = "network-interface"
    tags          = module.ims_network.tags
  }
}

# A launch template that names security groups replaces the cluster
# security group, so it is listed again here ("Customize managed nodes
# with launch templates").
resource "aws_launch_template" "edge" {
  name_prefix = "${var.cluster_name}-edge-"

  vpc_security_group_ids = [
    aws_eks_cluster.ims.vpc_config[0].cluster_security_group_id,
    aws_security_group.edge.id,
  ]

  tag_specifications {
    resource_type = "instance"
    tags          = merge(module.ims_network.tags, { Name = "${var.cluster_name}-edge" })
  }
  tag_specifications {
    resource_type = "volume"
    tags          = module.ims_network.tags
  }
  tag_specifications {
    resource_type = "network-interface"
    tags          = module.ims_network.tags
  }
}

resource "aws_eks_node_group" "core" {
  cluster_name    = aws_eks_cluster.ims.name
  node_group_name = "core"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = aws_subnet.public[*].id
  instance_types  = [var.core_instance_type]

  labels = { (module.ims_network.core_node_label.key) = module.ims_network.core_node_label.value }

  launch_template {
    id      = aws_launch_template.core.id
    version = aws_launch_template.core.latest_version
  }

  scaling_config {
    desired_size = var.core_node_count
    min_size     = var.core_node_count
    max_size     = var.core_node_count
  }

  depends_on = [aws_iam_role_policy_attachment.node, aws_eks_addon.vpc_cni]
}

resource "aws_eks_node_group" "edge" {
  cluster_name    = aws_eks_cluster.ims.name
  node_group_name = "edge"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = aws_subnet.public[*].id
  instance_types  = [var.edge_instance_type]

  labels = { (module.ims_network.edge_node_label.key) = module.ims_network.edge_node_label.value }

  taint {
    key    = module.ims_network.edge_node_taint.key
    value  = module.ims_network.edge_node_taint.value
    effect = "NO_SCHEDULE"
  }

  launch_template {
    id      = aws_launch_template.edge.id
    version = aws_launch_template.edge.latest_version
  }

  scaling_config {
    desired_size = var.edge_node_count
    min_size     = var.edge_node_count
    max_size     = var.edge_node_count
  }

  depends_on = [aws_iam_role_policy_attachment.node, aws_eks_addon.vpc_cni]
}
