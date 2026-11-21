# Running the IMS on Kubernetes (kind)

[日本語](README.ja.md)

This directory runs the same IMS as `compose.yaml` on a local Kubernetes cluster made with [kind](https://github.com/kubernetes-sigs/kind), so that the same nodes can be seen under an orchestrator as well as under Docker Compose.

| Path | What it is |
| --- | --- |
| `kind-cluster.yaml` | A kind cluster with one node |
| `base/` | One workload and one Service per role, and the role configuration files |
| `dns/` | The zones of the home network domain and of ENUM |
| `environments/` | The values that the manifests depend on, one directory per cluster: `kind`, `aws`, `azure` (below). A cloud's values are in `values.local.env`, which you make from the example next to it |
| `components/` | Parts that several overlays share: the name server's address, DNS option (b), the capture, the edge nodes of a cloud, the image registry of a cloud, the load balancer variant |
| `overlays/cluster-dns/` | DNS option (a): the cluster's own CoreDNS serves the zones |
| `overlays/external-dns/` | DNS option (b): a DNS server of the IMS's own, like the `dns` service of Compose |
| `overlays/cloud-aws/`, `overlays/cloud-azure/` | The clusters of `deploy/cloud/` ("In the cloud") |
| `capture/` | A tcpdump on the node that writes every packet of the Pods to a pcap |
| `changes.go`, `copies.go`, `gencopies/` | Make the copies in `base/config/` and `dns/` from `deploy/compose/` |
| `sync_test.go`, `environments_test.go`, `cloud_test.go`, `overlays_test.go` | Fail when a copy is out of date, when a role has no workload, when a value of the cluster is written in more than one place, when an overlay misses a value, or when `check-overlays.sh` leaves a kustomization out |
| `check-overlays.sh` | Builds every overlay and checks the output against the schemas of Kubernetes, without a cluster |
| `e2e-kind.sh` | Registers, calls and sends a short message on a cluster of its own, and checks the capture |

## What differs from Docker Compose

- **Pods have no fixed addresses.** Every SIP and Diameter node is a StatefulSet with a *headless* Service (`clusterIP: None`). Its name resolves to the Pod's own address, so packets go from Pod to Pod with no NAT, and each node sees its peer at the address the peer wrote in Via and Contact, as in Compose. A normal ClusterIP Service would put kube-proxy's NAT in between.
- **Names, not addresses, in the configuration.** The files in `base/config/` are copies of `deploy/compose/` with the addresses replaced by Service names. Two values must be addresses and are filled in when the Pod starts: the relay's `rtp_address` (its own Pod address) and the EPC's `pcscf_addresses`.
- **The zone points to Services.** In `dns/ims.mnc001.mcc001.3gppnetwork.org.zone`, each node is a CNAME to its Service in `cluster.local`, for example `icscf IN CNAME icscf.ims.svc.cluster.local.`. The NAPTR and SRV records are the same as in Compose.
- **No ims0 bridge.** kind connects each Pod with its own veth, so the capture listens on all interfaces of the node.

## Values that depend on the cluster

A few values differ from one cluster to another. Each cluster writes them once, as the ConfigMap `ims-environment` of `environments/<name>/kustomization.yaml`. The kustomizations for that cluster list the directory among their resources, and the components in `components/` copy each value to where it is used, with the `replacements` of kustomize. kustomize leaves the ConfigMap out of what it builds (the annotation `config.kubernetes.io/local-config`), so it never reaches the cluster.

kind writes its values in the kustomization itself, since they are those of `kind-cluster.yaml` and the same for everybody. A cloud's values are those of one person's cluster and line, so `environments/aws` and `environments/azure` read them from `values.local.env`, which Git ignores (`.gitignore`), and the repository has only `values.local.env.example` next to it ("In the cloud").

| Value | On kind | Where it goes |
| --- | --- | --- |
| `ims_dns_cluster_ip` | 10.96.0.53 | The fixed ClusterIP of the DNS server's Service (`dns`, or `ims-dns` with option a), the `nameservers` of the IMS Pods with option (b), and the address of the name server in the zone |
| `pod_cidr` | 10.244.0.0/16 | The filter of the capture |

`environments/kind` repeats what `kind-cluster.yaml` makes, and `go test ./...` fails when the two disagree, or when a manifest writes one of the values itself. The zone in `dns/` shows kind's address of the name server, which `go generate ./deploy/k8s/...` takes from `environments/kind`: run it again after changing that file. kustomize cannot change one line inside a file, so `components/name-server` splits the zone at ` IN A `, which only the name server's record has, and replaces what follows. That is why the generator writes this record last.

## When you change deploy/compose

kustomize reads no file outside its own directory, so `base/config/` and `dns/` hold copies of the role files and the zones of `deploy/compose/`. Do not edit them. After changing a file in `deploy/compose/`, make the copies again from the root of the repository, and commit them with your change:

```sh
go generate ./deploy/k8s/...
```

Without Go on the host, run it in a Go container:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -v ims-practice-gomod:/go/pkg/mod -w /src golang:1.27.4-alpine go generate ./deploy/k8s/...
```

The copies differ from the originals only by the table in `changes.go`: which keys get a Service name or a placeholder instead of an address, and the comments that say why. `go test ./...` fails while a copy is out of date, and prints the command. It also fails when a copy still holds an address of the compose network (`172.28.0.x`); add an entry for that key to `changes.go`. A new role file gets its copy on its own, but its workload and Service in `base/` are written by hand, after the pattern of the others, and listed in `base/kustomization.yaml`; the test says so when they are missing.

## Before you start

You need Docker, [kind](https://github.com/kubernetes-sigs/kind/releases) and [kubectl](https://kubernetes.io/docs/tasks/tools/). kubectl includes kustomize; nothing else is needed. Run the commands from the root of the repository.

The cluster publishes no ports on the host, so it can run next to the Compose project; stop that one first if your machine is short of memory.

## 1. Create the cluster and load the images

```sh
kind create cluster --config deploy/k8s/kind-cluster.yaml

docker build -t ims-practice/ims:dev .
docker build -t ims-practice/capture:dev images/capture
kind load docker-image ims-practice/ims:dev ims-practice/capture:dev --name ims-practice
```

The manifests use `imagePullPolicy: IfNotPresent`, so the Pods use the images you loaded. For option (b), the node pulls `coredns/coredns:1.12.1` from Docker Hub, or you can load it the same way.

## 2. Start the capture

Start it first, so that it records from the beginning:

```sh
kubectl apply -k deploy/k8s/capture
```

## 3. Start the IMS with one of the DNS options

Option (b), a DNS server of its own (`dns` Deployment, Service address 10.96.0.53). The IMS Pods ask it, and it forwards the other names to the cluster DNS:

```sh
kubectl apply -k deploy/k8s/overlays/external-dns
```

Option (a), the cluster's CoreDNS. The second command mounts the zones into the CoreDNS Pods:

```sh
kubectl apply -k deploy/k8s/overlays/cluster-dns
kubectl -n kube-system patch deployment coredns \
  --patch-file deploy/k8s/overlays/cluster-dns/coredns-patch.yaml
```

Option (a) replaces the Corefile of kube-system with the one in `overlays/cluster-dns/kube-system/Corefile`, which was written for kind v0.33.0. Use one option per cluster; to switch, delete the cluster and create it again.

Wait until every Pod runs, and the I-CSCF and the S-CSCF have opened their Diameter connections to the HSS:

```sh
kubectl -n ims get pods
kubectl -n ims logs icscf-0 | grep '"to":"I-Open (OKAY)"'
```

## 4. Register, call and send a short message

The UE simulators wait in the Pods `ue` and `ue2`; each command runs inside them. Give the P-CSCF by its name, since the default of `-pcscf` is the address it has in Compose:

```sh
P="-pcscf pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060"
U2="-impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org -impu sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org -password test-password-2"

# Registration
kubectl -n ims exec deploy/ue -- ue register $P -deregister-after 2s

# A call: ue2 answers, ue calls its tel URI
kubectl -n ims exec deploy/ue2 -- ue answer $P $U2 -ring 1s -tone 5s &
kubectl -n ims exec deploy/ue -- ue call $P -tone 3s tel:+1-202-555-0102
wait

# A short message with a status report
kubectl -n ims exec deploy/ue2 -- ue register $P $U2 -deregister-after 15s &
kubectl -n ims exec deploy/ue -- ue sms $P -status-report +12025550102 "Hello from kind"
wait
```

## 5. The dashboard

```sh
kubectl -n ims port-forward svc/dashboard 8080:8080
```

Open http://localhost:8080. To drive calls from the dashboard, start the simulators that stay up. They use the same subscribers as `ue` and `ue2`, so stop those first:

```sh
kubectl -n ims scale deployment ue ue2 --replicas=0
kubectl -n ims scale deployment ue-a ue-b --replicas=1
```

## 6. Look at the capture

### A pcap file

The capture Pod writes one pcap per start to `/var/log/ims-captures` on the node. Copy the newest one out and open it in Wireshark:

```sh
mkdir -p captures
pod=$(kubectl -n ims get pod -l app.kubernetes.io/name=capture -o name | head -n 1)
file=$(kubectl -n ims exec "$pod" -- sh -c 'ls -t /captures/ims-*.pcap | head -n 1')
kubectl -n ims cp "${pod#pod/}:$file" captures/kind.pcap
```

### Live

Pipe a second tcpdump in the capture Pod to Wireshark on your machine:

```sh
kubectl -n ims exec ds/capture -- tcpdump -i any -Q in -U -w - net 10.244.0.0/16 | wireshark -k -i -
```

### One Pod only

An ephemeral container joins the network of one Pod, and sees only what goes in and out of it:

```sh
kubectl -n ims debug pcscf-0 --image=ims-practice/capture:dev --profile=netadmin --container=tap -- sleep 3600
kubectl -n ims exec pcscf-0 -c tap -- tcpdump -i eth0 -U -w - | wireshark -k -i -
```

An ephemeral container stays in the Pod until the Pod is replaced.

### Reading the addresses

- Each node has the address of its Pod, from 10.244.0.0/16: `kubectl -n ims get pods -o wide` lists them. They change when a Pod starts again.
- The capture keeps each packet once, as it enters the node from the sending Pod (`-Q in`). A packet to a ClusterIP still shows the ClusterIP as its destination: the DNS queries go to 10.96.0.10 (option a) or 10.96.0.53 (option b), and the HEP copies to the dashboard's ClusterIP. Their answers show the address of the Pod that sent them, since kube-proxy changes the source back only after the packet has left that Pod. Wireshark therefore does not pair such a DNS answer with its query; match them by the DNS ID. SIP and Diameter do not go through a ClusterIP, so their addresses are those of the Pods on both sides.
- With option (b), every name in the home network domain shows up twice: the node asks the DNS server, and the DNS server asks the cluster DNS for the Service name of the CNAME.
- Option (a) logs every query of the IMS zones: `kubectl -n kube-system logs deployment/coredns`.

## Check everything with one script

`e2e-kind.sh` does sections 1 to 4 on a cluster of its own, and then checks the capture with tshark, as the end-to-end scripts of registration, calls and short messages in `scripts/` do with Docker Compose:

- the registration, the deregistration and a wrong password
- a call with its RTP through the relay, a busy callee, a cancelled call and an unregistered callee
- a short message with a status report

Give it one of the DNS options:

```sh
deploy/k8s/e2e-kind.sh external-dns
deploy/k8s/e2e-kind.sh cluster-dns
```

It names its cluster `ims-practice-e2e`, uses a kubeconfig of its own, and builds its images as `ims-practice/ims:kind-e2e` and `ims-practice/capture:kind-e2e`, so the cluster of section 1 and the images of Compose stay as they are. It refuses to start when its cluster exists already. At the end it deletes the cluster, the images it built or pulled, and the Docker network `kind` when kind made it for this run; `--keep` leaves them, and prints how to remove them. The pcap goes to `e2e-out/kind-<option>/`, and on failure the logs of every Pod too.

| Variable | What it does |
| --- | --- |
| `KIND_E2E_CLUSTER` | Another name for the cluster |
| `KIND_E2E_EXTRA_CONFIG` | A file of top-level keys to add to `kind-cluster.yaml`, such as the two patches of "When it does not work" |
| `IMS_IMAGE`, `CAPTURE_IMAGE` | Images built already, for the Dockerfile and `images/capture`; the script then builds none and removes none |
| `KIND_E2E_OUT` | Another directory for the pcap and the logs |
| `KIND`, `KUBECTL` | Other paths of the two commands |

The CI workflow `.github/workflows/kind.yml` runs the script for both options.

## Check the overlays without a cluster

`check-overlays.sh` builds with `kubectl kustomize` everything that a person applies: `capture/`, each overlay in `overlays/` and its `capture/`, and `overlays/cloud-azure` with the load balancer components taken in (from a kustomization of its own, so no file changes). When [kubeconform](https://github.com/yannh/kubeconform) is installed, it checks the output with `-strict` against the schemas of Kubernetes 1.37.2, which it fetches from raw.githubusercontent.com. It ends with the names of the kustomizations that failed.

```sh
deploy/k8s/check-overlays.sh                        # build; check too when kubeconform is there
deploy/k8s/check-overlays.sh --require-kubeconform  # fail without kubeconform
deploy/k8s/check-overlays.sh --list                 # what it builds
```

The script builds a copy of `deploy/k8s` in which each `values.local.env` is its example, so that it needs no file of your own and gives the same result everywhere, CI included; your own file is not used. To see what your own values build, run `kubectl kustomize deploy/k8s/overlays/cloud-aws` (or `cloud-azure`). The example's addresses are documentation ranges (RFC 5737); they build all the same, and the init container of the P-CSCF refuses them at start, not kustomize. `go test ./...` fails when a kustomization in `deploy/k8s` is built by none of them. The CI job `kustomize` of `.github/workflows/ci.yml` runs the script, with no cluster.

## When it does not work

- **kubelet does not start, and `kind create cluster` waits for the control plane until it fails.** Recent versions of Kubernetes (v1.37 with kind v0.33.0) refuse a host with cgroup v1. Check with `docker info | grep Cgroup`. Add this to a copy of `kind-cluster.yaml`:

  ```yaml
  kubeadmConfigPatches:
    - |
      kind: KubeletConfiguration
      failCgroupV1: false
  ```

- **Pods stay in ContainerCreating with `failed to update /proc/self/oom_score_adj: Permission denied`.** The host does not let even root lower an OOM score (no CAP_SYS_RESOURCE). Add this as well:

  ```yaml
  containerdConfigPatches:
    - |-
      [plugins."io.containerd.grpc.v1.cri"]
        restrict_oom_score_adj = true
  ```

- **`kind load docker-image` fails with `content digest ... not found`.** The image has several platforms, but Docker holds only one. Save that one and load the archive: `docker save --platform linux/amd64 -o coredns.tar coredns/coredns:1.12.1 && kind load image-archive coredns.tar --name ims-practice`.
- **tshark in the capture Pod says `You don't have permission to read the file "/captures/..."`.** The host's AppArmor has a profile for `/usr/bin/tshark` (AppArmor 5.0 has one, and Ubuntu 26.04 loads it). The Pods of kind run with no AppArmor profile of their own, so that profile confines the tshark of the capture Pod too. It lets tshark read files it owns under `/tmp`, and nothing under `/captures`. Copy the pcap to `/tmp` first, as `e2e-kind.sh` does, or copy it out of the Pod as in section 6:

  ```sh
  kubectl -n ims exec ds/capture -- sh -c 'cp "$(ls -t /captures/ims-*.pcap | head -n 1)" /tmp/ims.pcap && tshark -r /tmp/ims.pcap'
  ```

  `e2e-kind.sh` prints which profile its tshark runs under.

## In the cloud

`deploy/cloud/` makes an EKS or an AKS cluster with two node pools: **edge** nodes with public addresses, and **core** nodes that the internet cannot reach ([deploy/cloud/README.md](../cloud/README.md)). `overlays/cloud-aws` and `overlays/cloud-azure` put the IMS on it, with the UEs reaching the edge nodes' own addresses (option 1 of `load_balancer` in `deploy/cloud`):

- DNS option (b), as on kind.
- The P-CSCF and the media relay node on an edge node (`components/edge`): a nodeSelector and a toleration for `ims-practice/pool=edge:NoSchedule`, which no other Pod has, and the relay next to the P-CSCF. They take SIP (5060 over UDP and TCP) and the RTP ports (40000 to 40039) on the node's own ports with `hostPort`, as on kind. A hostPort is one port, so each RTP port has an entry; hostNetwork is not used, so that the IMS nodes keep the Pod network and depend on no network setting of the host.
- No Service of type LoadBalancer. Every other role stays private on the core nodes.
- The capture (`overlays/cloud-*/capture`) runs on the edge nodes too, and also keeps SIP and RTP from the internet.

Nothing of this has been run on a real cloud; only the builds are checked offline.

### What to set from tofu output

The values of your cluster and your line go into a file of your own, which Git ignores, so that the repository never holds your addresses or your registry, whose name holds your account. Make it from the example, in the directory of your cloud:

```sh
cd deploy/k8s/environments/aws          # or deploy/k8s/environments/azure
cp values.local.env.example values.local.env
# edit values.local.env
```

The file has one `KEY=value` per line. A line that starts with `#` is a comment; a `#` after a value is part of the value. While the file is missing, kustomize stops with an error that ends in `values.local.env: no such file or directory`. Each value of the example is made up and reaches nobody; replace them all:

| Value | Where it comes from |
| --- | --- |
| `ims_dns_cluster_ip` | `tofu output -json kubernetes`: `ims_dns_cluster_ip` (10.96.0.53 for the default Service range) |
| `pod_cidr` | `tofu output -json kubernetes`: `pod_cidr` (on EKS, the VPC's range) |
| `ue_public_address` | Your line's public address, the one of `allowed_source_cidrs` without `/32` |
| `edge_public_address` | The edge node's public address: the command of `tofu output -raw edge_public_addresses_command` |
| `image_prefix` | Your registry and the path you pushed the images to. It takes the place of `ims-practice` in `ims-practice/ims:dev` and `ims-practice/capture:dev` (`components/image-registry`) |
| `load_balancer_*` (Azure only) | `tofu output -json load_balancer_public_ips`, for the load balancer variant |

The P-CSCF gets `ue_public_address` and `edge_public_address` as `host_access`, its setting for UEs behind NAT: to a UE that sends from `ue_public_address`, it shows the relay at `edge_public_address` in SDP. The init container stops the P-CSCF with a message while either is empty or still an address of the example.

kind loads the images onto its node by hand. A cloud node pulls them, so push them to a registry the nodes can reach, under the names that `image_prefix` gives. For example, with `image_prefix=<registry>/ims-practice`:

```sh
docker tag ims-practice/ims:dev <registry>/ims-practice/ims:dev
docker tag ims-practice/capture:dev <registry>/ims-practice/capture:dev
docker push <registry>/ims-practice/ims:dev
docker push <registry>/ims-practice/capture:dev
```

Then, with kubectl pointed at the cluster:

```sh
kubectl apply -k deploy/k8s/overlays/cloud-aws/capture
kubectl apply -k deploy/k8s/overlays/cloud-aws
# from your own machine, with a password you changed (deploy/cloud/README.md):
go run ./cmd/ue register -pcscf <edge_public_address>:5060 -password <new-password> -deregister-after 5s
```

### Limits

- Only the UE that sends from `ue_public_address` gets the relay's public address. The relay has no setting of its own for the address its access side is reached at, so any other UE sees the relay's Pod address in SDP and hears nothing.
- One P-CSCF and one relay per edge node, since a node has each port once. The StatefulSets keep one replica.
- When the edge node is replaced, it gets another public address on AWS: set `edge_public_address` in `values.local.env` again and apply.
- A namespace that enforces the baseline level of the Pod Security Standards refuses hostPort Pods.
- On EKS the Pods have addresses of the VPC, so the capture keeps the nodes' own packets inside the VPC as well.
- The load balancer variant, for comparison only: on AWS, `deploy/cloud/aws` makes the NLB itself and sends to the same hostPorts, so only `edge_public_address` changes. On Azure, take `components/azure-load-balancer-sip` (and `-rtp`) into `overlays/cloud-azure`; AKS makes the rules from those Services. Delete them before `tofu destroy`.

## Clean up

```sh
kind delete cluster --name ims-practice
docker image rm ims-practice/ims:dev ims-practice/capture:dev
```

Deleting the cluster also deletes the pcaps on its node; copy out the ones you want to keep first.
