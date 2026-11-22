# Kubernetes（kind）で IMS を動かす

[English](README.md)

このディレクトリは、`compose.yaml` と同じ IMS を、[kind](https://github.com/kubernetes-sigs/kind) で作ったローカルの Kubernetes で動かします。同じノードを、Docker Compose だけでなく、オーケストレーターの上でも見られるようにするためです。

| 場所 | 中身 |
| --- | --- |
| `kind-cluster.yaml` | ノード 1 つの kind のクラスタ |
| `base/` | 役割ごとの workload と Service、役割の設定ファイル |
| `dns/` | ホームドメインと ENUM のゾーン |
| `environments/` | マニフェストが頼る値。クラスタごとに 1 つのディレクトリで、`kind`、`aws`、`azure` があります（下の節）。クラウドの値は `values.local.env` に書きます。隣にある例から作るファイルです |
| `components/` | いくつかの overlay で使う部品。名前サーバーのアドレス、DNS の構成 (b)、キャプチャ、クラウドの edge のノード、クラウドでイメージを取るレジストリ、ロードバランサーの形 |
| `overlays/cluster-dns/` | DNS の構成 (a)。クラスタの CoreDNS がゾーンを持ちます |
| `overlays/external-dns/` | DNS の構成 (b)。Compose の `dns` と同じ、IMS 専用の DNS サーバーを置きます |
| `overlays/cloud-aws/`、`overlays/cloud-azure/` | `deploy/cloud/` のクラスタに置く形（「クラウドで動かす」） |
| `capture/` | ノードの tcpdump で、Pod のすべてのパケットを pcap に書きます |
| `changes.go`、`copies.go`、`gencopies/` | `deploy/compose/` から `base/config/` と `dns/` の写しを作ります |
| `sync_test.go`、`environments_test.go`、`cloud_test.go`、`overlays_test.go` | 写しが古いとき、workload の無い役割があるとき、クラスタの値を 2 か所以上に書いたとき、overlay に要る値が無いとき、`check-overlays.sh` がビルドしない kustomization があるときに失敗します |
| `check-overlays.sh` | クラスタを使わずに、すべての overlay をビルドし、出力を Kubernetes のスキーマで確かめます |
| `e2e-kind.sh` | 専用のクラスタで登録、発着信、SMS を行い、キャプチャを確かめます |

## Docker Compose との違い

- **Pod には決まったアドレスがありません。** SIP と Diameter のノードは、すべて StatefulSet と headless の Service（`clusterIP: None`）にしました。名前を引くと Pod 自身のアドレスが返るので、パケットは NAT を通らずに Pod から Pod へ届きます。各ノードには、相手が Via と Contact に書いたアドレスから相手が来たように見えます。Compose と同じです。普通の ClusterIP の Service にすると、kube-proxy の NAT が間に入ります。
- **設定にはアドレスではなく名前を書きます。** `base/config/` のファイルは `deploy/compose/` の写しで、アドレスを Service の名前に替えてあります。アドレスでなければならない値が 2 つあり、Pod が起動するときに書き込みます。メディア中継ノードの `rtp_address`（自分の Pod のアドレス）と、EPC の `pcscf_addresses` です。
- **ゾーンは Service を指します。** `dns/ims.mnc001.mcc001.3gppnetwork.org.zone` では、各ノードを `cluster.local` の Service への CNAME にしました。たとえば `icscf IN CNAME icscf.ims.svc.cluster.local.` です。NAPTR は Compose と同じです。SRV の宛先には別名を使えないので（RFC 2782）、SRV は Compose と同じノードを `_sip._udp IN SRV 10 50 5060 icscf.ims.svc.cluster.local.` のように Service の名前で指します。
- **ims0 のブリッジはありません。** kind は Pod ごとに veth でつなぐので、キャプチャはノードのすべてのインタフェースを聞きます。

## クラスタごとに違う値

いくつかの値は、クラスタごとに違います。各クラスタは、その値を `environments/<名前>/kustomization.yaml` の ConfigMap `ims-environment` として 1 回だけ書きます。そのクラスタ用の kustomization は、このディレクトリを resources に並べます。`components/` の部品が、kustomize の `replacements` で、値を使う場所へ写します。この ConfigMap は、kustomize が出力から外すので（annotation の `config.kubernetes.io/local-config`）、クラスタには届きません。

kind の値は `kind-cluster.yaml` のもので、誰にとっても同じです。そのため kustomization の中に直接書きます。クラウドの値は、使う人それぞれのクラスタと回線のものです。そこで `environments/aws` と `environments/azure` は、値を `values.local.env` から読みます。このファイルは Git の対象から外してあり（`.gitignore`）、リポジトリには隣の `values.local.env.example` だけがあります（「クラウドで動かす」）。

| 値 | kind での値 | 写す先 |
| --- | --- | --- |
| `ims_dns_cluster_ip` | 10.96.0.53 | DNS サーバーの Service（`dns`、構成 a では `ims-dns`）の固定の ClusterIP、構成 (b) での IMS の Pod の `nameservers`、ゾーンの名前サーバーのアドレス |
| `pod_cidr` | 10.244.0.0/16 | キャプチャのフィルタ |

`environments/kind` は、`kind-cluster.yaml` が作るものを繰り返して書いています。2 つが食い違うときと、マニフェストがこれらの値を自分で書いているときは、`go test ./...` が失敗します。`dns/` のゾーンには kind での名前サーバーのアドレスが入っています。これは `go generate ./deploy/k8s/...` が `environments/kind` から取ったものです。そのファイルを変えたら、このコマンドをもう一度実行します。kustomize はファイルの中の 1 行だけを変えられません。そこで `components/name-server` は、名前サーバーのレコードにしか無い ` IN A ` でゾーンを分け、その後ろを置き換えます。生成の道具がこのレコードを最後に書くのは、このためです。

## deploy/compose を変えたとき

kustomize は自分のディレクトリの外のファイルを読みません。そこで `base/config/` と `dns/` に、`deploy/compose/` の役割の設定ファイルとゾーンの写しを置いています。写しは手で直さないでください。`deploy/compose/` のファイルを変えたら、リポジトリのルートで次のコマンドを実行して写しを作り直し、変更と同じコミットに入れます。

```sh
go generate ./deploy/k8s/...
```

ホストに Go が無ければ、Go のコンテナで実行します。

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -v ims-practice-gomod:/go/pkg/mod -w /src golang:1.27.4-alpine go generate ./deploy/k8s/...
```

写しと元の違いは、`changes.go` の表にあるものだけです。どのキーのアドレスを Service の名前やプレースホルダーに替えるかと、その理由を書いたコメントです。写しが古いと `go test ./...` が失敗し、このコマンドを表示します。写しに compose のネットワークのアドレス（`172.28.0.x`）が残っているときも失敗します。そのときは、そのキーの項目を `changes.go` に足してください。新しい役割の設定ファイルは自動で写されます。ただし、`base/` の workload と Service はほかの役割にならって手で書き、`base/kustomization.yaml` に足します。足りないときは試験がそう知らせます。

## 始める前に

Docker、[kind](https://github.com/kubernetes-sigs/kind/releases)、[kubectl](https://kubernetes.io/docs/tasks/tools/) が要ります。kubectl には kustomize が入っているので、ほかの道具は要りません。コマンドはリポジトリのルートで実行します。

クラスタはホストのポートを使わないので、Compose と同時に動かせます。メモリが足りないときは、先に Compose を止めてください。

## 1. クラスタを作り、イメージを入れる

```sh
kind create cluster --config deploy/k8s/kind-cluster.yaml

docker build -t ims-practice/ims:dev .
docker build -t ims-practice/capture:dev images/capture
kind load docker-image ims-practice/ims:dev ims-practice/capture:dev --name ims-practice
```

マニフェストは `imagePullPolicy: IfNotPresent` なので、Pod は入れたイメージを使います。構成 (b) では、ノードが `coredns/coredns:1.12.1` を Docker Hub から取ってきます。同じように入れておくこともできます。

## 2. キャプチャを始める

最初から記録するため、先に始めます。

```sh
kubectl apply -k deploy/k8s/capture
```

## 3. DNS の構成を 1 つ選んで IMS を起動する

構成 (b) は、専用の DNS サーバー（Deployment `dns`、Service のアドレス 10.96.0.53）を置きます。IMS の Pod はここに問い合わせ、DNS サーバーはほかの名前をクラスタの DNS に転送します。

```sh
kubectl apply -k deploy/k8s/overlays/external-dns
```

構成 (a) は、クラスタの CoreDNS を使います。2 つ目のコマンドで、ゾーンを CoreDNS の Pod にマウントします。

```sh
kubectl apply -k deploy/k8s/overlays/cluster-dns
kubectl -n kube-system patch deployment coredns \
  --patch-file deploy/k8s/overlays/cluster-dns/coredns-patch.yaml
```

構成 (a) は、kube-system の Corefile を `overlays/cluster-dns/kube-system/Corefile` に置き換えます。このファイルは kind v0.33.0 に合わせて書きました。1 つのクラスタでは 1 つの構成だけを使ってください。構成を替えるときは、クラスタを消して作り直します。

すべての Pod が動き、I-CSCF と S-CSCF が HSS との Diameter の接続を開くまで待ちます。

```sh
kubectl -n ims get pods
kubectl -n ims logs icscf-0 | grep '"to":"I-Open (OKAY)"'
```

## 4. 登録、発着信、SMS

UE の模擬は Pod `ue` と `ue2` で待っていて、コマンドはその中で実行します。`-pcscf` の既定値は Compose でのアドレスなので、P-CSCF を名前で渡します。

```sh
P="-pcscf pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060"
U2="-impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org -password test-password-2"

# 登録
kubectl -n ims exec deploy/ue -- ue register $P -deregister-after 2s

# 通話。ue2 が待ち受け、ue がその tel URI に発信します
kubectl -n ims exec deploy/ue2 -- ue answer $P $U2 -ring 1s -tone 5s &
kubectl -n ims exec deploy/ue -- ue call $P -tone 3s tel:+1-202-555-0102
wait

# status report 付きの SMS
kubectl -n ims exec deploy/ue2 -- ue register $P $U2 -deregister-after 15s &
kubectl -n ims exec deploy/ue -- ue sms $P -status-report +12025550102 "Hello from kind"
wait
```

## 5. ダッシュボード

```sh
kubectl -n ims port-forward svc/dashboard 8080:8080
```

http://localhost:8080 を開きます。ダッシュボードから発信するには、起動したままの模擬の UE を起こします。`ue` と `ue2` と同じ加入者を使うので、先にそちらを止めます。

```sh
kubectl -n ims scale deployment ue ue2 --replicas=0
kubectl -n ims scale deployment ue-a ue-b --replicas=1
```

## 6. キャプチャを見る

### pcap のファイル

キャプチャの Pod は、起動するたびに 1 つの pcap をノードの `/var/log/ims-captures` に書きます。いちばん新しいものを取り出して、Wireshark で開きます。

```sh
mkdir -p captures
pod=$(kubectl -n ims get pod -l app.kubernetes.io/name=capture -o name | head -n 1)
file=$(kubectl -n ims exec "$pod" -- sh -c 'ls -t /captures/ims-*.pcap | head -n 1')
kubectl -n ims cp "${pod#pod/}:$file" captures/kind.pcap
```

### ライブで見る

キャプチャの Pod でもう 1 つ tcpdump を動かし、手元の Wireshark に渡します。

```sh
kubectl -n ims exec ds/capture -- tcpdump -i any -Q in -U -w - net 10.244.0.0/16 | wireshark -k -i -
```

### 1 つの Pod だけを見る

エフェメラルコンテナは 1 つの Pod のネットワークに加わり、その Pod の出入りだけを見ます。

```sh
kubectl -n ims debug pcscf-0 --image=ims-practice/capture:dev --profile=netadmin --container=tap -- sleep 3600
kubectl -n ims exec pcscf-0 -c tap -- tcpdump -i eth0 -U -w - | wireshark -k -i -
```

エフェメラルコンテナは、Pod が作り直されるまで残ります。

### アドレスの読み方

- 各ノードのアドレスは、その Pod の 10.244.0.0/16 のアドレスです。`kubectl -n ims get pods -o wide` で一覧できます。Pod が起動し直すと変わります。
- キャプチャは、送った Pod からノードに入るところで、各パケットを 1 回だけ記録します（`-Q in`）。ClusterIP 宛てのパケットは、宛先が ClusterIP のままです。DNS の問い合わせは 10.96.0.10（構成 a）か 10.96.0.53（構成 b）宛て、HEP の写しはダッシュボードの ClusterIP 宛てに見えます。その応答は、送った Pod のアドレスから来たように見えます。kube-proxy が送信元を戻すのは、パケットがその Pod を出たあとだからです。そのため、Wireshark はこの DNS の応答を問い合わせと組にしません。DNS の ID で見比べてください。SIP と Diameter は ClusterIP を通らないので、両側とも Pod のアドレスです。
- 構成 (b) では、ホームドメインの名前の問い合わせが 2 回見えます。ノードが DNS サーバーに問い合わせ、DNS サーバーが CNAME の先の Service の名前をクラスタの DNS に問い合わせるためです。
- 構成 (a) では、IMS のゾーンへの問い合わせがすべてログに出ます。`kubectl -n kube-system logs deployment/coredns` で見られます。

## スクリプトでまとめて確かめる

`e2e-kind.sh` は、1 から 4 までを専用のクラスタで行い、キャプチャを tshark で確かめます。確かめる内容は、Docker Compose で登録、通話、ショートメッセージを確かめる `scripts/` の e2e のスクリプトと同じです。

- 登録、登録の解除、間違ったパスワード
- RTP が中継ノードを通る通話、話中の相手、取り消した呼、登録していない相手
- status report を求める SMS

引数に DNS の構成を 1 つ渡します。

```sh
deploy/k8s/e2e-kind.sh external-dns
deploy/k8s/e2e-kind.sh cluster-dns
```

クラスタの名前は `ims-practice-e2e` で、専用の kubeconfig を使います。イメージは `ims-practice/ims:kind-e2e` と `ims-practice/capture:kind-e2e` の名前で作ります。1 章のクラスタと Compose のイメージには触りません。同じ名前のクラスタがすでにあると、始めずに終わります。最後に、クラスタと、自分で作ったり取ってきたりしたイメージを消します。kind がこのときに作った Docker のネットワーク `kind` も消します。`--keep` を付けると消さずに残し、消し方を表示します。pcap は `e2e-out/kind-<構成>/` に置きます。失敗したときは、すべての Pod のログも置きます。

| 環境変数 | 働き |
| --- | --- |
| `KIND_E2E_CLUSTER` | クラスタの名前を変えます |
| `KIND_E2E_EXTRA_CONFIG` | `kind-cluster.yaml` に足す最上位のキーを書いたファイルです。「うまく動かないとき」の 2 つのパッチなどに使います |
| `IMS_IMAGE`、`CAPTURE_IMAGE` | Dockerfile と `images/capture` から作り済みのイメージです。このときスクリプトはイメージを作らず、消しもしません |
| `KIND_E2E_OUT` | pcap とログを置く場所を変えます |
| `KIND`、`KUBECTL` | 2 つのコマンドの場所を変えます |

CI のワークフロー `.github/workflows/kind.yml` が、このスクリプトを 2 つの構成で動かします。

## クラスタを使わずに overlay を確かめる

`check-overlays.sh` は、人が apply するものをすべて `kubectl kustomize` でビルドします。対象は `capture/`、`overlays/` の各 overlay とその `capture/`、ロードバランサーの component を加えた `overlays/cloud-azure` です。最後のものは、スクリプトが自分で作る kustomization からビルドするので、リポジトリのファイルは変わりません。[kubeconform](https://github.com/yannh/kubeconform) があれば、出力を `-strict` で Kubernetes 1.37.2 のスキーマと照らし合わせます。スキーマは raw.githubusercontent.com から取ってきます。失敗した kustomization があれば、最後にその名前を出します。

```sh
deploy/k8s/check-overlays.sh                        # ビルドする。kubeconform があれば確かめもする
deploy/k8s/check-overlays.sh --require-kubeconform  # kubeconform が無ければ失敗にする
deploy/k8s/check-overlays.sh --list                 # ビルドするものの一覧
```

スクリプトは `deploy/k8s` の写しを作ってビルドします。写しの中の `values.local.env` は、例のファイルと同じ内容にします。自分のファイルが無くても動き、CI を含めてどこでも同じ結果になるようにするためです。自分のファイルは使いません。自分の値でビルドした結果を見るときは、`kubectl kustomize deploy/k8s/overlays/cloud-aws`（または `cloud-azure`）を実行します。例のアドレスは文書用の範囲（RFC 5737）です。これでもビルドはできます。起動のときに、kustomize ではなく P-CSCF の init コンテナが拒みます。`deploy/k8s` の kustomization のうち、どれからもビルドされないものがあると、`go test ./...` が失敗します。`.github/workflows/ci.yml` の CI のジョブ `kustomize` が、このスクリプトを動かします。クラスタは作りません。

## うまく動かないとき

- **kubelet が起動せず、`kind create cluster` がコントロールプレーンを待って失敗する。** 新しい Kubernetes（kind v0.33.0 の v1.37）は、cgroup v1 のホストを拒みます。`docker info | grep Cgroup` で確かめます。`kind-cluster.yaml` を写し、次を足します。

  ```yaml
  kubeadmConfigPatches:
    - |
      kind: KubeletConfiguration
      failCgroupV1: false
  ```

- **Pod が ContainerCreating のまま、`failed to update /proc/self/oom_score_adj: Permission denied` が出る。** そのホストでは、root でも OOM のスコアを下げられません（CAP_SYS_RESOURCE がありません）。次も足します。

  ```yaml
  containerdConfigPatches:
    - |-
      [plugins."io.containerd.grpc.v1.cri"]
        restrict_oom_score_adj = true
  ```

- **`kind load docker-image` が `content digest ... not found` で失敗する。** イメージは複数のプラットフォームを持ちますが、Docker には 1 つしかありません。その 1 つを保存して入れます。`docker save --platform linux/amd64 -o coredns.tar coredns/coredns:1.12.1 && kind load image-archive coredns.tar --name ims-practice` です。
- **キャプチャの Pod の tshark が `You don't have permission to read the file "/captures/..."` と言う。** ホストの AppArmor に `/usr/bin/tshark` のプロファイルがあります（AppArmor 5.0 に含まれ、Ubuntu 26.04 が読み込みます）。kind の Pod は AppArmor のプロファイルを持たずに動くので、キャプチャの Pod の tshark にも、このプロファイルが当たります。このプロファイルで tshark が読めるのは、`/tmp` の下にある自分のファイルだけです。`/captures` の下は読めません。`e2e-kind.sh` と同じように pcap を `/tmp` に写してから読むか、6 章のように Pod の外へ取り出します。

  ```sh
  kubectl -n ims exec ds/capture -- sh -c 'cp "$(ls -t /captures/ims-*.pcap | head -n 1)" /tmp/ims.pcap && tshark -r /tmp/ims.pcap'
  ```

  `e2e-kind.sh` は、自分の tshark がどのプロファイルで動いているかを表示します。

## クラウドで動かす

`deploy/cloud/` は、EKS か AKS のクラスタを、2 つのノードプールで作ります。公開アドレスを持つ **edge** のノードと、インターネットから届かない **core** のノードです（[deploy/cloud/README.ja.md](../cloud/README.ja.md)）。`overlays/cloud-aws` と `overlays/cloud-azure` は、UE が edge のノードのアドレスに直接届く形（`deploy/cloud` の `load_balancer` の案 1）で、そこに IMS を置きます。

- DNS は kind と同じ構成 (b) です。
- P-CSCF とメディア中継ノードを edge のノードに置きます（`components/edge`）。`ims-practice/pool=edge:NoSchedule` に合う nodeSelector と toleration を付けます。ほかの Pod には付けません。中継ノードは P-CSCF と同じノードに置きます。SIP（UDP と TCP の 5060）と RTP のポート（40000〜40039）は、kind と同じく `hostPort` でノードのポートにつなぎます。hostPort は 1 つのポートしか書けないので、RTP のポートは 1 つずつ書きました。hostNetwork は使いません。IMS のノードが Pod のネットワークのままで、ホストのネットワークの設定に頼らないようにするためです。
- LoadBalancer の Service は作りません。ほかの役割は core のノードに置き、外には出しません。
- キャプチャ（`overlays/cloud-*/capture`）は edge のノードでも動き、インターネットからの SIP と RTP も記録します。

実際のクラウドでは動かしていません。確かめたのは、手元でのビルドの検査だけです。

### tofu output から書く値

自分のクラスタと回線の値は、Git の対象から外した自分用のファイルに書きます。リポジトリに、自分のアドレスやレジストリを残さないためです。レジストリの名前には、アカウントが含まれます。自分のクラウドのディレクトリで、例のファイルから作ります。

```sh
cd deploy/k8s/environments/aws          # Azure なら deploy/k8s/environments/azure
cp values.local.env.example values.local.env
# values.local.env を書き換えます
```

1 行に 1 つ、`KEY=value` の形で書きます。`#` で始まる行は注釈です。値の後ろの `#` は、値の一部になります。ファイルが無いあいだは、kustomize が `values.local.env: no such file or directory` で終わるエラーを出して止まります。例の値はすべて作り物で、どこにも届きません。全部を書き換えてください。

| 値 | 取り出し方 |
| --- | --- |
| `ims_dns_cluster_ip` | `tofu output -json kubernetes` の `ims_dns_cluster_ip`（Service の範囲が既定なら 10.96.0.53） |
| `pod_cidr` | `tofu output -json kubernetes` の `pod_cidr`（EKS では VPC の範囲） |
| `ue_public_address` | 自分の回線の公開アドレス。`allowed_source_cidrs` のアドレスから `/32` を除いたもの |
| `edge_public_address` | edge のノードの公開アドレス。`tofu output -raw edge_public_addresses_command` が示すコマンドで調べます |
| `image_prefix` | イメージを push したレジストリとパス。`ims-practice/ims:dev` と `ims-practice/capture:dev` の `ims-practice` を、この値に置き換えます（`components/image-registry`） |
| `load_balancer_*`（Azure だけ） | ロードバランサーの比較の形で使います。`tofu output -json load_balancer_public_ips` から書きます |

P-CSCF は、`ue_public_address` と `edge_public_address` を `host_access` として受け取ります。NAT の後ろの UE のための設定です。`ue_public_address` から送ってくる UE には、SDP で中継ノードを `edge_public_address` に見せます。どちらかが空のときと、例のアドレスのままのときは、init コンテナがメッセージを出して P-CSCF を止めます。

kind では、イメージを手でノードに読み込ませます。クラウドのノードはイメージを自分で取ってくるので、ノードから届くレジストリに push します。名前は `image_prefix` で決まります。たとえば `image_prefix=<registry>/ims-practice` なら、次のようにします。

```sh
docker tag ims-practice/ims:dev <registry>/ims-practice/ims:dev
docker tag ims-practice/capture:dev <registry>/ims-practice/capture:dev
docker push <registry>/ims-practice/ims:dev
docker push <registry>/ims-practice/capture:dev
```

そのあと、kubectl をクラスタに向けて次のように実行します。

```sh
kubectl apply -k deploy/k8s/overlays/cloud-aws/capture
kubectl apply -k deploy/k8s/overlays/cloud-aws
# 自分の PC から、作り直したパスワードで登録します（deploy/cloud/README.ja.md）。
go run ./cmd/ue register -pcscf <edge_public_address>:5060 -password <new-password> -deregister-after 5s
```

### 制限

- 中継ノードの公開アドレスを SDP で受け取れるのは、`ue_public_address` から送る UE だけです。中継ノードには、access の側にどのアドレスで届くかを書く設定がありません。そのため、ほかの UE には SDP に中継ノードの Pod のアドレスが書かれ、音が届きません。
- 1 つのノードは同じポートを 1 回しか使えないので、edge のノード 1 台に P-CSCF と中継ノードは 1 台ずつです。StatefulSet は 1 台のままにします。
- AWS では、edge のノードが作り直されると公開アドレスが変わります。`values.local.env` の `edge_public_address` を書き直して、もう一度 apply します。
- Pod Security Standards の baseline を強制する namespace では、hostPort の Pod は拒まれます。
- EKS では Pod が VPC のアドレスを持つので、キャプチャには VPC の中のノード自身のパケットも入ります。
- ロードバランサーの形は比較のためだけのものです。AWS では `deploy/cloud/aws` が NLB を自分で作り、同じ hostPort へ送るので、変わるのは `edge_public_address` だけです。Azure では、`components/azure-load-balancer-sip`（と `-rtp`）を `overlays/cloud-azure` に入れます。AKS がその Service から規則を作ります。`tofu destroy` の前に Service を消してください。

## 後始末

```sh
kind delete cluster --name ims-practice
docker image rm ims-practice/ims:dev ims-practice/capture:dev
```

クラスタを消すと、ノードの pcap も消えます。残したいものは先に取り出してください。
