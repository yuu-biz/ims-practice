# Cloud resources for the IMS

[日本語](README.ja.md)

> **Applying these definitions costs money, and whether to do it is your decision.**
> Nothing in this repository applies them for you. The CI only checks them offline, with no cloud account.
> If you apply them, you pay for every hour the resources exist, used or not, until you destroy them.
> Read "What it costs while idle" and "Teardown" before you start.

This directory defines, with [OpenTofu](https://opentofu.org/), the cloud resources that the IMS needs to run on a managed Kubernetes cluster: Amazon EKS on AWS, or Azure Kubernetes Service (AKS) on Azure. The comments in the definitions name the pages of the AWS, Azure and OpenTofu documentation they follow.

After `tofu apply` you have the cluster and its network; the IMS goes on it from `deploy/k8s` (`overlays/cloud-aws` or `overlays/cloud-azure`, see "In the cloud" in [deploy/k8s/README.md](../k8s/README.md)). Neither part has been run on a real cloud: they are checked offline only (see "Checks without an account").

## What this creates

The same design on both clouds:

- A network with public subnets only, so there is no NAT gateway to pay for.
- A Kubernetes cluster with two node pools:
  - **edge**: nodes with public addresses for the P-CSCF and the media relay node (agw). They take SIP and RTP on the node's ports with `hostPort`, as on kind. The nodes carry the label and taint `ims-practice/pool=edge`, so only Pods that tolerate it run there.
  - **core**: nodes for the other roles. Nothing on the internet can reach them.
- Firewall rules on the edge nodes that open only SIP (5060 over UDP and TCP) and the RTP range (40000 to 40039 over UDP by default), and only to the address ranges you list in `allowed_source_cidrs`. The Kubernetes API is restricted to the same ranges.
- Optionally, a load balancer for SIP (`load_balancer = "sip"`) or for SIP and RTP (`"sip-and-rtp"`). It is off by default. Its limits:
  - An AWS Network Load Balancer forgets a UDP flow after 120 seconds without packets, and this time cannot be changed. The UE must send more often, or calls to it stop arriving.
  - One NLB takes at most 50 listeners. SIP takes one and every RTP port one more, so the plan stops if the RTP range has more than 49 ports.
  - On Azure, the load balancer rules are made by Kubernetes Services, which `deploy/k8s/components/azure-load-balancer-sip` and `-rtp` hold. These definitions make only their fixed public addresses. One network interface takes at most 300 rules.

| | AWS (`aws/`) | Azure (`azure/`) |
| --- | --- | --- |
| Network | VPC, two public subnets in two zones, internet gateway | Resource group, virtual network, core and edge subnets |
| Cluster | EKS, VPC CNI add-on | AKS (Free tier), Azure CNI Overlay |
| Edge firewall | Security group on the edge nodes | Network security group on the edge subnet, and the node pool's allowed host ports |
| Edge addresses | Assigned to each node when it starts | Taken from a public IP prefix, known before the nodes start |
| Optional load balancer | NLB and one Elastic IP, sending to the edge nodes | Static public IPs for the LoadBalancer Services |

Every resource carries the tag `ims-practice=cloud`, so that anything left behind can be found by it.

## Cleartext SIP and RTP on the internet

The IMS sends SIP and RTP unencrypted, so that captures can be read. On the internet this means:

- Anyone on the path can read the signalling and listen to the calls.
- The test subscribers' passwords are in this repository. With port 5060 reachable, anyone could register as them and call.
- Port 5060 is scanned constantly. Without a source restriction, unknown REGISTER and OPTIONS requests fill the capture.
- The media relay node locks onto the first sender of each stream, so whoever sends first to an open RTP port receives the other side's audio.

So:

1. `allowed_source_cidrs` has no default. Set it to your own line's address (for example `["192.0.2.10/32"]`). The definitions refuse `0.0.0.0/0` and any range wider than `/8`.
2. Change the subscribers' passwords before you expose the IMS, and do not write the new ones into the repository.
3. Keep each experiment short and destroy everything right after it.

## What it costs while idle

These are the figures of the providers' pricing pages as read in November 2026. Prices change; read the pages again before you apply.

| | AWS | Azure |
| --- | --- | --- |
| Kubernetes control plane | EKS: USD 0.10 per cluster per hour (standard support) | AKS Free tier: no charge for cluster management |
| Nodes (2 by default) | EC2 instances and their EBS volumes: see "Amazon EC2 On-Demand Pricing" | VMs and their managed disks: see "Linux Virtual Machines Pricing" |
| Public addresses | USD 0.005 per public IPv4 address per hour, attached or not (2 nodes: USD 0.01 per hour) | See "IP Addresses pricing" (the edge prefix, and the outbound address of AKS's load balancer) |
| Load balancer | Only with `load_balancer`: NLB USD 0.0225 per hour plus NLCU, and one more Elastic IP | AKS always has a Standard Load Balancer for outbound traffic: see "Load Balancer pricing" |
| NAT gateway | Not created | Not created |

On AWS, the figures that are known add up to about USD 0.11 per hour without the instances and disks. The Azure amounts could not be confirmed. Look them up on "Amazon EKS pricing", "Amazon VPC pricing", "Elastic Load Balancing pricing", "Azure Kubernetes Service (AKS) pricing", "Load Balancer pricing" and "IP Addresses pricing". Both clouds can send you budget alerts; set one before you start.

## Checks without an account

These need no credentials and create nothing. They are what the CI job `cloud` of `.github/workflows/ci.yml` runs:

```sh
for dir in deploy/cloud/modules/ims-network deploy/cloud/aws deploy/cloud/azure; do
  tofu -chdir=$dir fmt -check -recursive
  tofu -chdir=$dir init -backend=false -input=false -lockfile=readonly
  tofu -chdir=$dir validate
  tofu -chdir=$dir test
done
```

`init` downloads the providers (about 180 MB for AWS and 70 MB for Azure) from registry.opentofu.org. `test` makes plans with the providers replaced by mocks (`mock_provider`), so it never talks to a cloud. The tests check that only SIP and the RTP range are opened, only to the allowed ranges; that the load balancer variants switch as described; and that bad settings are refused.

Install OpenTofu 1.13.0 from its [GitHub release](https://github.com/opentofu/opentofu/releases/tag/v1.13.0) and compare the zip with `tofu_1.13.0_SHA256SUMS` of the same release, as the [OpenTofu documentation](https://opentofu.org/docs/intro/install/standalone/) describes.

With Terraform, `fmt`, `init` and `validate` work as well. The tests do not: Terraform leaves the mocked values unknown during a plan unless the mock says `override_during = plan`, which OpenTofu does not accept. Terraform also rewrites `.terraform.lock.hcl` for its own registry.

## Applying (by hand, at your cost)

You need OpenTofu, kubectl, and the cloud's command line tool (AWS CLI or Azure CLI) signed in to **your own** account. The definitions take the credentials from that tool's environment; they name no account or subscription. On Azure, set `ARM_SUBSCRIPTION_ID` and have the right to assign roles (the cluster's identity is given Network Contributor on the resource group).

1. Go to the cloud's directory and make your settings:

   ```sh
   cd deploy/cloud/aws          # or deploy/cloud/azure
   cp terraform.tfvars.example terraform.tfvars
   # edit terraform.tfvars: region, allowed_source_cidrs, load_balancer
   ```

   `terraform.tfvars` holds your line's address and your choice of region, so Git ignores it (`deploy/cloud/.gitignore`), as it ignores every `*.auto.tfvars`; OpenTofu reads both names by itself. The example holds documentation addresses (RFC 5737) that reach nobody. Keep the account and the subscription out of these files as well: they come from the cloud's command line tool and from `ARM_SUBSCRIPTION_ID`.

2. Download the providers and make a plan. Read the plan: it lists every resource that will be created.

   ```sh
   tofu init
   tofu plan -out=ims.tfplan
   ```

3. Only if you accept the plan and its cost:

   ```sh
   tofu apply ims.tfplan
   ```

   Creating the cluster takes several minutes.

4. Read the outputs and point kubectl at the cluster:

   ```sh
   tofu output
   tofu output -raw kubeconfig_command    # run the command it prints
   kubectl get nodes -o wide
   ```

   `edge_public_addresses_command` (AWS) or `edge_public_ip_prefix` (Azure) gives the address the UE sends SIP to; with a load balancer, `load_balancer_address` (AWS) or `load_balancer_public_ips` (Azure). The output `kubernetes` holds values that the cloud overlay of `deploy/k8s` needs: the IMS DNS address and the Pod range go into `values.local.env` of `deploy/k8s/environments/aws` or `environments/azure`; the edge label and taint are those of `deploy/k8s/components/edge`.

5. Write the values into `deploy/k8s/environments/aws/values.local.env` (or `azure`), which you make from `values.local.env.example` next to it and which Git ignores too. Then install the IMS with `kubectl apply -k deploy/k8s/overlays/cloud-aws/capture` and then `kubectl apply -k deploy/k8s/overlays/cloud-aws` (or `cloud-azure`), as "In the cloud" in [deploy/k8s/README.md](../k8s/README.md) describes.

The state (`terraform.tfstate`) stays in the directory, and Git ignores it. Keep it until you have destroyed everything: without it, OpenTofu no longer knows what it created.

## Teardown

Destroy everything as soon as the experiment is over.

1. Delete the Kubernetes Services of type LoadBalancer first, if you made any. On AKS they own load balancer rules that OpenTofu does not know about.

   ```sh
   kubectl get svc --all-namespaces      # look for an EXTERNAL-IP
   kubectl delete svc <name> -n <namespace>
   ```

2. Destroy what OpenTofu created, in the same directory, and confirm when asked:

   ```sh
   tofu destroy
   tofu state list                       # must print nothing
   ```

3. Look for anything left with the project's tag:

   ```sh
   # AWS (also lists resources that had the tag before)
   aws resourcegroupstaggingapi get-resources --region <region> --tag-filters Key=ims-practice,Values=cloud
   aws ec2 describe-addresses --region <region>       # an Elastic IP is billed even when unattached

   # Azure
   az resource list --tag ims-practice=cloud
   ```

   On Azure, deleting the cluster also deletes the node resource group (`MC_...`) that AKS made.

4. The next day, check on the cloud's billing page that the charges have stopped.

## What these definitions simplify

The definitions have a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| The RTP target groups of `aws/load-balancer.tf`, Health checks for Network Load Balancer target groups | A health check of the media relay node. Nothing on that node answers TCP on the RTP ports, so a UDP target group is checked on the TCP port of the P-CSCF on the same node, which shows that the P-CSCF is up, not that the media relay node is. | The two run together on every edge node. |

## Files

| Path | Contents |
| --- | --- |
| `modules/ims-network/` | The part that is the same on every cloud: which ports are opened, the checks of the settings, the DNS addresses, the node labels, the tags. It creates nothing. |
| `aws/`, `azure/` | One root module per cloud, with the same variable names. `tests/` holds the mocked plans; `.terraform.lock.hcl` pins the provider builds. |
