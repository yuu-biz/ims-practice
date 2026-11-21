# クラウドで動かすことの調査

この文書は、クラウド（AWS と Azure の Kubernetes）に IMS を載せて、インターネットにいる UE と話せるようにするための調査の記録です。公開する口、UDP を通すロードバランサー、NAT とアドレスの扱い、RTP のポートの範囲、DNS、資源の定義のコード、費用と後始末、公開の仕方の案を扱います。仕様と文書の本文は写さず、要点を言い換えて書きます。条項を引くときは、表「読んだ資料」の版の条項です。

対象の要件は、Kubernetes でも動くこと、ホスト固有のネットワーク設定に依存しないこと、DNS の構成を設定で選べること、各役割を水平に広げられること、クラウドを配置先に選べること、既定では暗号化しないこと、標準のポート番号を使うことです（[要件定義](../requirements.md)）。関わる設計判断は、ホストとコンテナの通信が NAT を通る前提と、ブリッジ全体を 1 つのコンテナで取るキャプチャです（[ホストとコンテナの通信とキャプチャの設計](../design/environment-capture.md)）。この文書は、Kubernetes の調査（[k8s.md](k8s.md)）と `deploy/k8s/` を前提にしています。

調査は、クラウド各社と Kubernetes の公式のページと、公開されている文書の原稿を読み、資源の定義の道具を手元で試す形で行いました。最初は公式のサイトに届かず、GitHub にある原稿を読んで書き、届くようになってから公式のページを読み直しました。確かめて分かったことは、2 章、3 章、4 章、7 章に反映しています。特に、UDP では NLB がクライアントのアドレスを必ず保つこと、NLB の UDP の流れの寿命が 120 秒で変えられないこと、EKS のノードが Pod の外向きの通信の送信元ポートを既定で乱数にすることが分かり、3 章の判定が変わりました。

クラウドの資源は何も作っていません。クラウドの API は 1 度も呼んでいません。資格情報も使っていません。動かしたのは、手元での `tofu` と `terraform` の `fmt`、`init`、`validate`、`test`、手元での `kubectl kustomize`、kubeconform、kind だけです。`test` はプロバイダーを作り物（mock）に置き換えて計画を作るだけで、クラウドには届きません。この文書のことは、文書を読んで分かったこと、その試作で分かったこと、そこからの推定です。推定と、確かめていないことは、そう書きます。

## 読んだ資料

この表は、GitHub の原稿などで読んだものです。公式のページは、次の節の表にあります。

| 資料 | 版、読んだ所 |
| --- | --- |
| RFC 3261（SIP） | 18.2.2（応答の送り先）。18.1.1（大きな要求は TCP）は [k8s.md](k8s.md) で読んだもの |
| RFC 3581（rport） | 4（サーバーの振る舞い）。[k8s.md](k8s.md) で読んだもの |
| RFC 4787（NAT の UDP の振る舞い） | 4.3（対応付けの寿命、REQ-5）、5（フィルタの種類） |
| RFC 4961（対称 RTP） | 3（定義）、4（使い方の推奨） |
| RFC 5626（SIP outbound） | 4.4.1（CRLF のキープアライブ）、4.4.2（STUN のキープアライブ） |
| RFC 6223（Via の keep パラメータ） | 4.1、4.4、5 |
| RFC 7362（中継ノードでの NAT 越え） | 4（ラッチング）、5（セキュリティ） |
| Kubernetes の文書 | 「Service」（LoadBalancer、混在プロトコル、allocateLoadBalancerNodePorts）、「Create an External Load Balancer」（externalTrafficPolicy）、「Pod Security Standards」（hostNetwork、hostPort）、フィーチャーゲート MixedProtocolLBService。kubernetes.io に届かないので、GitHub の kubernetes/website にある原稿（main）を読みました |
| AWS Load Balancer Controller の文書 | `docs/guide/service/annotations.md`、`docs/guide/service/nlb.md`、`docs/deploy/security_groups.md`。GitHub の kubernetes-sigs/aws-load-balancer-controller（main） |
| Amazon EKS ユーザーガイド | 「Route TCP and UDP traffic with Network Load Balancers」、「Enable outbound internet access for Pods」、「Delete a cluster」。docs.aws.amazon.com に届かないので、GitHub の awsdocs/amazon-eks-user-guide（mainline）の原稿を読みました |
| Azure Load Balancer の文書 | overview、components、floating IP、TCP reset と idle timeout、outbound connections、health probes、limits。GitHub の MicrosoftDocs/azure-docs（main） |
| AKS の文書 | 「Use a public standard load balancer」、「Configure a public standard load balancer」、「Use instance-level public IPs」、「NAT gateway」、「Free, Standard, and Premium pricing tiers」、「Stop and start a cluster」、「Delete a cluster」。GitHub の MicrosoftDocs/azure-aks-docs（main） |
| Azure DNS の文書 | 「DNS zones and records」（NAPTR と SRV） |
| OpenTofu の文書 | `validate`、`plan`、`init`、`fmt`、`test`（mock_provider）、S3 と azurerm の backend、state の暗号化。GitHub の opentofu/opentofu（main）の `website/docs` |
| Terraform の文書 | `validate`、`plan`、`init`、tests の mocking。GitHub の hashicorp/web-unified-docs の v1.14.x |
| ライセンス | 各リポジトリの LICENSE（hashicorp/terraform、opentofu/opentofu、eksctl-io/eksctl、pulumi/pulumi、Azure/bicep、kubernetes-sigs/aws-load-balancer-controller）。hashicorp/terraform-provider-aws と hashicorp/terraform-provider-azurerm も読みました |

### 公式のページで確かめたもの

公式のページです。版の番号が無いページは、2026-11-20 に公開されていたものです。

| 資料 | ページ（題名と URL） |
| --- | --- |
| Elastic Load Balancing の NLB の利用者ガイド | 「Listeners for your Network Load Balancers」 https://docs.aws.amazon.com/elasticloadbalancing/latest/network/load-balancer-listeners.html |
| 同上 | 「Network Load Balancers」（Connection idle timeout） https://docs.aws.amazon.com/elasticloadbalancing/latest/network/network-load-balancers.html |
| 同上 | 「What is a Network Load Balancer?」（流れの振り分け） https://docs.aws.amazon.com/elasticloadbalancing/latest/network/introduction.html |
| 同上 | 「Target groups for your Network Load Balancers」 https://docs.aws.amazon.com/elasticloadbalancing/latest/network/load-balancer-target-groups.html |
| 同上 | 「Edit target group attributes for your Network Load Balancer」（Client IP preservation） https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-target-group-attributes.html |
| 同上 | 「Health checks for Network Load Balancer target groups」 https://docs.aws.amazon.com/elasticloadbalancing/latest/network/target-group-health-checks.html |
| 同上 | 「Quotas for your Network Load Balancers」 https://docs.aws.amazon.com/elasticloadbalancing/latest/network/load-balancer-limits.html |
| 同上 | 「Update the idle timeout」 https://docs.aws.amazon.com/elasticloadbalancing/latest/network/update-idle-timeout.html |
| 同上 | 「Troubleshoot your Network Load Balancer」 https://docs.aws.amazon.com/elasticloadbalancing/latest/network/load-balancer-troubleshooting.html |
| AWS Load Balancer Controller | 「Annotations」 https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/guide/service/annotations/ 、「Network Load Balancer」 https://kubernetes-sigs.github.io/aws-load-balancer-controller/latest/guide/service/nlb/ |
| Amazon EKS ユーザーガイド | 「Route TCP and UDP traffic with Network Load Balancers」 https://docs.aws.amazon.com/eks/latest/userguide/network-load-balancing.html |
| 同上 | 「Enable outbound internet access for Pods」 https://docs.aws.amazon.com/eks/latest/userguide/external-snat.html |
| 同上 | 「View Amazon EKS networking requirements for VPC and subnets」 https://docs.aws.amazon.com/eks/latest/userguide/network-reqs.html 、「Simplify node lifecycle with managed node groups」 https://docs.aws.amazon.com/eks/latest/userguide/managed-node-groups.html |
| 同上 | 「Create an Amazon EKS cluster」（Service の範囲） https://docs.aws.amazon.com/eks/latest/userguide/create-cluster.html |
| 同上 | 「Manage CoreDNS for DNS in Amazon EKS clusters」 https://docs.aws.amazon.com/eks/latest/userguide/managing-coredns.html 、「Update an Amazon EKS add-on」 https://docs.aws.amazon.com/eks/latest/userguide/updating-an-add-on.html |
| 同上 | 「Delete a cluster」 https://docs.aws.amazon.com/eks/latest/userguide/delete-cluster.html |
| Amazon VPC CNI | リポジトリ aws/amazon-vpc-cni-k8s の README（`AWS_VPC_K8S_CNI_EXTERNALSNAT`、`AWS_VPC_K8S_CNI_RANDOMIZESNAT`）と `misc/10-aws.conflist`（master） |
| Amazon VPC ユーザーガイド | 「Enable internet access for a VPC using an internet gateway」 https://docs.aws.amazon.com/vpc/latest/userguide/VPC_Internet_Gateway.html 、「Security group rules」 https://docs.aws.amazon.com/vpc/latest/userguide/security-group-rules.html 、「Amazon VPC quotas」 https://docs.aws.amazon.com/vpc/latest/userguide/amazon-vpc-limits.html |
| Amazon EC2 ユーザーガイド | 「Amazon EC2 security group connection tracking」 https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/security-group-connection-tracking.html |
| Amazon Route 53 開発者ガイド | 「Supported DNS record types」 https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/ResourceRecordTypes.html |
| eksctl | 「eksctl」 https://eksctl.io/ 、「Dry Run」 https://eksctl.io/usage/dry-run/ （どちらも AWS の文書のサイトに移されたページが返りました） |
| Azure Load Balancer | 「Azure Load Balancer Floating IP configuration」 https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-floating-ip |
| 同上 | 「Load Balancer TCP Reset and Idle Timeout」 https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-tcp-reset |
| 同上 | 「Source Network Address Translation (SNAT) for outbound connections」 https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-outbound-connections 、「Outbound rules Azure Load Balancer」 https://learn.microsoft.com/en-us/azure/load-balancer/outbound-rules |
| 同上 | 「Azure subscription and service limits, quotas, and constraints」（Load Balancer） https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/azure-subscription-service-limits |
| AKS の文書 | 「Use instance-level public IPs in AKS」 https://learn.microsoft.com/en-us/azure/aks/use-node-public-ips |
| 同上 | 「Use a public standard load balancer」 https://learn.microsoft.com/en-us/azure/aks/load-balancer-standard 、「Configure a public standard load balancer」 https://learn.microsoft.com/en-us/azure/aks/configure-load-balancer-standard |
| 同上 | 「Customize cluster egress with outbound types」 https://learn.microsoft.com/en-us/azure/aks/egress-outboundtype |
| 同上 | 「Customize CoreDNS」 https://learn.microsoft.com/en-us/azure/aks/coredns-custom 、「Configure Azure CNI Overlay networking」 https://learn.microsoft.com/en-us/azure/aks/azure-cni-overlay |
| 同上 | 「Stop and start an AKS cluster」 https://learn.microsoft.com/en-us/azure/aks/start-stop-cluster 、「Delete an AKS cluster」 https://learn.microsoft.com/en-us/azure/aks/delete-cluster 、「Free, Standard, and Premium pricing tiers」 https://learn.microsoft.com/en-us/azure/aks/free-standard-pricing-tiers |
| cloud-provider-azure | 「Azure LoadBalancer」 https://cloud-provider-azure.sigs.k8s.io/topics/loadbalancer/ |
| Kubernetes の文書（v1.37 の版） | 「Service」 https://kubernetes.io/docs/concepts/services-networking/service/ 、「Service」の API リファレンス https://kubernetes.io/docs/reference/kubernetes-api/service-resources/service-v1/ |
| 同上 | 「Create an External Load Balancer」 https://kubernetes.io/docs/tasks/access-application-cluster/create-external-load-balancer/ 、「Feature Gates (removed)」 https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates-removed/ |
| 同上 | 「Pod Security Standards」 https://kubernetes.io/docs/concepts/security/pod-security-standards/ 、「kube-proxy Configuration (v1alpha1)」 https://kubernetes.io/docs/reference/config-api/kube-proxy-config.v1alpha1/ |
| OpenTofu の文書（v1.13.x の版） | 「Command: test」 https://opentofu.org/docs/cli/commands/test/ 、「Command: validate」 https://opentofu.org/docs/cli/commands/validate/ 、「Command: plan」 https://opentofu.org/docs/cli/commands/plan/ 、「Command: init」 https://opentofu.org/docs/cli/commands/init/ |
| Terraform の文書 | 「Tests: mocks」 https://developer.hashicorp.com/terraform/language/tests/mocking 、「terraform validate」 https://developer.hashicorp.com/terraform/cli/commands/validate 、「terraform plan」 https://developer.hashicorp.com/terraform/cli/commands/plan |
| AWS の料金のページ | 「Amazon EKS pricing」 https://aws.amazon.com/eks/pricing/ 、「Elastic Load Balancing pricing」 https://aws.amazon.com/elasticloadbalancing/pricing/ 、「Amazon VPC pricing」 https://aws.amazon.com/vpc/pricing/ 、「Amazon EC2 On-Demand Pricing」 https://aws.amazon.com/ec2/pricing/on-demand/ |
| Azure の料金のページ | 「Azure Kubernetes Service (AKS) pricing」 https://azure.microsoft.com/en-us/pricing/details/kubernetes-service/ 、「Load Balancer pricing」 https://azure.microsoft.com/en-us/pricing/details/load-balancer/ 、「Azure NAT Gateway pricing」 https://azure.microsoft.com/en-us/pricing/details/azure-nat-gateway/ 、「IP Addresses pricing」 https://azure.microsoft.com/en-us/pricing/details/ip-addresses/ 、「Linux Virtual Machines Pricing」 https://azure.microsoft.com/en-us/pricing/details/virtual-machines/linux/ |

### 資源の定義を書くときに確かめたもの

`deploy/cloud/` を書くときに読んだ公式のページです。

| 資料 | ページ（題名と URL）と、確かめたこと |
| --- | --- |
| Amazon EKS ユーザーガイド | 「Create an Amazon EKS cluster」 https://docs.aws.amazon.com/eks/latest/userguide/create-cluster.html 。Service の範囲は 10.0.0.0/8、172.16.0.0/12、192.168.0.0/16 のどれかの中で、/24 から /12 まで、VPC の範囲と重ならないこと |
| 同上 | 「Customize managed nodes with launch templates」 https://docs.aws.amazon.com/eks/latest/userguide/launch-templates.html 。起動テンプレートでセキュリティグループを指定すると、EKS はクラスタのセキュリティグループを付けなくなること |
| 同上 | 「Cluster API server endpoint」 https://docs.aws.amazon.com/eks/latest/userguide/cluster-endpoint.html 。公開の口を CIDR で絞るなら、私的な口も有効にするか、ノードが出るアドレスを一覧に入れるよう勧めていること |
| AKS の文書 | 「Use a static public IP address and DNS label with the AKS load balancer」 https://learn.microsoft.com/en-us/azure/aks/static-ip 。annotation の `azure-pip-name` と `azure-load-balancer-resource-group`。ノードの資源グループの外の公開 IP を使うなら、制御面の ID にその資源グループの権限が要ること |
| 同上 | 「Use instance-level public IPs in AKS」（上の表と同じページ）。`allowed_host_ports` はクラスタのセキュリティグループに許可の規則を作ること。自分の仮想ネットワークのサブネットにセキュリティグループがあれば、そこにも許可の規則が要ること |
| 同上 | 「Secure access to the API server using authorized IP address ranges」 https://learn.microsoft.com/en-us/azure/aks/api-server-authorized-ip-ranges 。範囲は 200 まで。AKS は Standard Load Balancer の外向きのアドレスを自分で足すこと |
| Azure の仮想ネットワークの文書 | 「Public IP address prefix」 https://learn.microsoft.com/en-us/azure/virtual-network/ip-services/public-ip-address-prefix 。IPv4 の大きさは /28（16 個）から /31（2 個）。作ったあとで大きさを変えられないこと |
| 同上 | 「Network security groups」 https://learn.microsoft.com/en-us/azure/virtual-network/network-security-groups-overview 。既定の受ける向きの規則は AllowVNetInBound、AllowAzureLoadBalancerInBound、DenyAllInbound の 3 つ |
| AWS CLI のリファレンス | 「get-resources」 https://docs.aws.amazon.com/cli/latest/reference/resourcegroupstaggingapi/get-resources.html 。タグの付いた資源と、前にタグが付いていた資源を一覧にすること。「describe-addresses」 https://docs.aws.amazon.com/cli/latest/reference/ec2/describe-addresses.html |
| Azure CLI のリファレンス | 「az resource」 https://learn.microsoft.com/en-us/cli/azure/resource 。`az resource list --tag key=value` |
| OpenTofu の文書 | 「Installing OpenTofu from GitHub Releases」 https://opentofu.org/docs/intro/install/standalone/ 。GitHub のリリースから zip と `tofu_<版>_SHA256SUMS` を取り、SHA-256 で確かめる手順 |

### 道具の取り先

試作で道具とプロバイダーを取った道です。プロキシが許さないホスト（OpenTofu の導入スクリプトなど）を迂回する方法は使っていません。

| ホストと道 | 結果 |
| --- | --- |
| github.com の opentofu/opentofu のリリースの `releases/download/` | 302 で release-assets.githubusercontent.com に移り、取れました。zip と SHA256SUMS をここから取りました |
| releases.hashicorp.com | 200。Terraform の zip と SHA256SUMS をここから取りました |
| registry.opentofu.org | 200。`tofu init` がプロバイダーの一覧と取り先をここで引きました。プロバイダーの zip そのものは github.com の opentofu/terraform-provider-aws と opentofu/terraform-provider-azurerm のリリースから取れました |
| registry.terraform.io | 200。`terraform init` は releases.hashicorp.com からプロバイダーを取りました |

## 1. インターネットの UE が届く必要があるもの

### 開けるのは 2 つだけ

インターネットにいる UE（手元の PC の `ue` や、ソフトフォン）がクラウドの IMS と話すのに要るのは、次の 2 つだけです。

| 相手 | プロトコルとポート | 理由 |
| --- | --- | --- |
| P-CSCF | SIP、UDP と TCP の 5060（標準のポート番号） | UE の最初の接点です（TS 24.229 の P-CSCF）。UDP に収まらない大きな要求は TCP で送ります（RFC 3261 18.1.1、[k8s.md](k8s.md)） |
| メディア中継ノード（agw）の access の側 | RTP と RTCP、UDP の 40000〜40039（`deploy/k8s/base/config/agw.yaml` の `rtp_ports`） | P-CSCF が SDP に書いた先へ、UE が RTP を送ります |

次のものは、インターネットに出しません。クラスタの中だけで話します。

| ノード | ポート | 出さない理由 |
| --- | --- | --- |
| HSS | Diameter の 3868、プロビジョニングの HTTP 8081 | プロビジョニングの API は加入者を足せます。開けると、誰でも加入者を作れます |
| I-CSCF、S-CSCF、IP-SM-GW | SIP の 5060 | UE は P-CSCF にしか送りません |
| EPC と PCRF | 8080、Diameter の 3868 | インターネットの UE は EPC に接続しません（下を参照） |
| agw の制御 API | HTTP 8080 | 誰でもコンテキストを作れてしまいます（メディア中継ノードの制御 API に認証はありません） |
| ダッシュボード | HTTP 8080、HEP の UDP 9060 | 発信の操作ができます。見るときは `kubectl port-forward` を使います |
| DNS サーバー | 53 | ゾーンは網の中の名前です（5 章） |
| Kubernetes の API サーバー | 443 | クラウドの設定で、送信元を利用者のアドレスに絞ります |

IMS AKA と IPsec（`sec_agree.protected_server_port`）を使うときは、保護されたポートも要ります。ただし、UE の手前に家庭のルーターなどの NAT があると、ESP はそのままでは通りません。網の側では、Azure の VM に付けた公開 IP（instance-level public IP）は ESP も通すと文書にあります（「Source Network Address Translation (SNAT) for outbound connections」）。AWS のインターネットゲートウェイが ESP を通すかは確かめていません。NAT 越しの IPsec には UDP のカプセル化が要り、TS 33.203 と TS 24.229 にその手順があるはずですが、条項は確かめていません。クラウドの構成では、インターネットの UE は SIP Digest で登録する前提にするのが素直です。

インターネットの UE は、EPC のスタブに接続しません。今の UE（`cmd/ue`）は、P-CSCF のアドレスを `-pcscf` で受け取るだけで、EPC を使いません。P-CSCF は登録のあとで PCRF に UE の IP アドレスを問い合わせますが、PCRF はその IP-CAN セッションを知らないので、その UE の呼には Rx のポリシー制御が掛かりません（`internal/pcscf/pcscf.go` の `Association.SignallingSession` のコメント）。つまり、3GPP のアクセス網ではない所から IMS を使う形になります。手順書でこの違いを説明する必要があります。

### 既定で暗号化しないことの帰結

この実装は、既定では信号を暗号化しません。インターネットに出すと、次のことが起きます。

- SIP と RTP は、途中の経路のどこからでも平文で読めます。Digest の応答からパスワードを総当たりで探すこともできます。
- リポジトリにある試験用の加入者のパスワード（`test-password-1` など）は公開されています。インターネットに 5060 を開ければ、誰でもその加入者として登録し、発信できます。クラウドに置くときは、配置のたびにパスワードを作り直し、リポジトリに書かない必要があります。
- 5060 は、SIP の走査をする者に常に叩かれます。送信元を絞らないと、知らない REGISTER と OPTIONS がキャプチャに並びます。
- メディア中継ノードは、最初に届いたパケットの送信元に固定します（ラッチング）。RTP のポートが開いていると、UE より先にパケットを送った者に、相手の音声が流れます。RFC 7362 の 5 章はこの攻撃を挙げ、対策として、信号の送信元と同じアドレスからのパケットにだけラッチする方法（restricted latching）を紹介しています。今の `internal/agw/relay.go` は、どの送信元にもラッチします。

学習者に伝えることは次のとおりです。

1. 送信元のアドレスを、自分の回線のアドレスだけに絞る。絞る場所は 3 つあり、どれか 1 つでは足りないことがあります。
   - Service の `spec.loadBalancerSourceRanges`。AWS Load Balancer Controller も AKS も対応しています。AWS では、クライアントの IP を保つ設定（preserve client IP）が無効だと、この制限は無視されます（Controller の「Annotations」）。ただし、この設定を無効にできるのは TCP と TLS のターゲットグループだけです。UDP と TCP_UDP のターゲットグループでは常に有効なので（NLB の「Edit target group attributes」）、SIP の 5060 と RTP では制限が効きます。
   - AWS のセキュリティグループと Azure の NSG。ノードに公開アドレスを付ける構成（2 章の最後の節、8 章の案 1）では、ここが唯一の壁です。
   - Kubernetes の API サーバーの公開の範囲。
2. 加入者のパスワードを、配置のたびに作り直す。
3. 使い終わったら、すぐに全部を消す（7 章）。短い実験のためだけに使い、置きっぱなしにしない。
4. 自分の PC で取ったキャプチャにも、相手の番号と音声がそのまま入る。

TLS（SIP over TLS、SRTP）は拡張の候補です。クラウドの構成では作らず、既定のまま（暗号化しない）、短い実験に限ることを手順書に書くのがよいと考えます。

## 2. UDP を通すロードバランサー

### Kubernetes の Service の機能

| 機能 | 文書で確かめたこと |
| --- | --- |
| `type: LoadBalancer` | クラウドの部品が外のロードバランサーを作り、割り当てたアドレスを Service に書きます。既定では、まず NodePort を割り当て、ロードバランサーはそのノードのポートへ送ります |
| `spec.allocateLoadBalancerNodePorts: false` | v1.24 から stable です。Pod へ直接送るロードバランサーのためのもので、NodePort を割り当てません |
| 混在プロトコル（1 つの Service に TCP と UDP） | フィーチャーゲート MixedProtocolLBService は v1.24 で既定で有効、v1.26 で stable になり、v1.27 のあとで取り除かれました（「Feature Gates (removed)」）。「Service」のページも、v1.26 から stable で切り替えられないとしています。今の Kubernetes では常に使えます。ただし、クラウドが受け付けるかは別で、各社の制限が掛かります |
| `spec.externalTrafficPolicy: Local` | クライアントの送信元アドレスを保ち、ノードからノードへの 2 段目の転送をしません。既定は `Cluster` で、こちらは送信元を隠します。`Local` では、そのノードに Pod が無いと届かないので、ロードバランサーは `healthCheckNodePort` で生きている Pod のあるノードを見分けます |
| ポートの範囲 | 使えません。API リファレンスの ServicePort は、`port` が 1 つの整数です。範囲を書く欄はありません（v1.37 の版）。今の `deploy/k8s/base/agw.yaml` も 40 個を並べています。Pod の `hostPort` も 1 つの整数です |
| `spec.loadBalancerIP` | v1.24 で非推奨です。アドレスを指定するには、各社の annotation を使います |
| `spec.loadBalancerClass` | 既定のものとは別の実装（AWS Load Balancer Controller など）を選びます |
| finalizer | LoadBalancer の Service には `service.kubernetes.io/load-balancer-cleanup` が付き、クラウドのロードバランサーが消えるまで Service が消えません |

### AWS の NLB（EKS と AWS Load Balancer Controller）

EKS のユーザーガイドは、NLB を新しく作るときは AWS Load Balancer Controller（ライセンスは Apache 2.0）を使うよう勧めています。Service に `service.beta.kubernetes.io/aws-load-balancer-type: external` を付けると、このコントローラーが NLB を作ります。annotation は、組み込みのクラウドの部品と違うので注意が要ります。

| 項目 | 文書で確かめたこと |
| --- | --- |
| UDP | リスナーのプロトコルは TCP、TLS、UDP、TCP_UDP、QUIC、TCP_QUIC で、ポートは 1 から 65535 です（NLB の「Listeners」）。コントローラーも TCP と UDP の両方に対応します（Controller の「Network Load Balancer」） |
| 同じポートの TCP と UDP | 同じポートで TCP と UDP を受けるには TCP_UDP のリスナーを作り、そのターゲットグループも TCP_UDP にします（NLB の「Listeners」）。コントローラーでは `service.beta.kubernetes.io/aws-load-balancer-enable-tcp-udp-listener: "true"` で作ります。既定は false です。5060 の UDP と TCP を 1 つの Service に書くときに要ります |
| ターゲットの種類 | `service.beta.kubernetes.io/aws-load-balancer-nlb-target-type` に `ip` か `instance`。`ip` は Pod のアドレスへ直接送り、Amazon VPC CNI のように Pod が VPC のアドレスを持つ CNI が要ります。`instance` はノードの NodePort へ送り、kube-proxy が Pod へ転送します。`instance` には NodePort が要るので、`allocateLoadBalancerNodePorts: false` とは組み合わせられません |
| クライアントの IP を保つ | target group の属性 `preserve_client_ip.enabled`。UDP と TCP_UDP のターゲットグループでは、`ip` でも `instance` でも常に有効で、無効にできません。無効にできるのは TCP と TLS だけで、`ip` の TCP と TLS では既定で無効です。有効なら、Pod には UE の公開アドレスが見えます（NLB の「Edit target group attributes」）。Controller の文書の「IP ターゲットでは既定で無効」は TCP の話です |
| UDP の流れ | NLB は、送信元と宛先のアドレスとポートで UDP の流れを見分け、同じ流れは同じターゲットへ送ります。流れに 120 秒パケットが無いと、次のパケットは新しい流れとして扱い、別のターゲットへ送ることがあります。この 120 秒は変えられません（NLB の「Network Load Balancers」の Connection idle timeout）。ターゲットは、新しい要求に 30 秒以内に答えないと、戻りの道ができないともあります |
| TCP の流れ | idle timeout は既定で 350 秒、60 から 6000 秒に変えられます。リスナーの属性 `tcp.idle_timeout.seconds` で、コントローラーでは `aws-load-balancer-listener-attributes.TCP-<ポート>` で書きます |
| 公開するか | `service.beta.kubernetes.io/aws-load-balancer-scheme: internet-facing`。既定は `internal` です |
| 固定のアドレス | `service.beta.kubernetes.io/aws-load-balancer-eip-allocations` に Elastic IP を、サブネットの数だけ書きます |
| ヘルスチェック | UDP のターゲットグループも、TCP、HTTP、HTTPS のどれかで調べます。ポートはターゲットのどのポートでもよいとあります（NLB の「Health checks」）。コントローラーの `aws-load-balancer-healthcheck-protocol` は tcp、http、https で、`externalTrafficPolicy` が `Cluster` なら tcp、`Local` なら http が既定です。`Local` では tcp を使ってはいけないとあります。P-CSCF は TCP の 5060 でも待つので、TCP_UDP のリスナーなら TCP の 5060 で調べられます。agw の RTP のポートには TCP で答えるものが無いので、別のポートが要ります |
| 送信元の制限 | 上の 1 章のとおりです。コントローラーの v2.6.0 からは、NLB に専用のセキュリティグループ（フロントエンドとバックエンド）を作って付けます |
| タグ | `service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags` で NLB にタグを付けられます。後始末の確認に使えます（7 章） |
| セキュリティグループの規則の数 | NLB ごとにノードのセキュリティグループに規則が増え、上限に当たると Service の作成が失敗することがあります（EKS のガイド）。セキュリティグループの規則の既定の上限は、内向きと外向きのそれぞれで 60 です（「Amazon VPC quotas」） |
| 上限 | 1 つの NLB のリスナーは 50 までで、上げられません。ターゲットは 1 つの NLB に 3,000、1 つのアベイラビリティーゾーンに 500 までです（NLB の「Quotas」）。RTP の 40 ポートと 5060 の TCP_UDP で 41 のリスナーになり、上限の中には収まります。ポートを増やす余地はほとんどありません（4 章） |

#### Pod が始める UDP の道

3 章で要になるのは、P-CSCF が UE へ自分から送る要求（着信の INVITE など）が、どのアドレスとポートから出るかです。文書で分かったことと、推定を分けて書きます。

文書に書いてあることです。

- NLB の文書は、クライアントが始めた流れと、それへのターゲットの応答だけを説明しています。ターゲットが NLB を通して自分から流れを始められるとは、どこにも書かれていません。流れはクライアントからのパケットで作られ、UDP では 120 秒パケットが無いと消えます（上の表）。
- Pod から VPC の外への通信は、既定ではノードの主の ENI の主のプライベートアドレスに SNAT されます（EKS の「Enable outbound internet access for Pods」）。ノードが公開サブネットにあり公開アドレスを持つなら、インターネットゲートウェイを通ります。ノードが私的なサブネットにあるなら、NAT ゲートウェイを通ります。
- このときの SNAT の規則には、既定で `--random-fully` が付きます（Amazon VPC CNI の README の `AWS_VPC_K8S_CNI_RANDOMIZESNAT`。既定は `prng`）。つまり送信元ポートは乱数になり、5060 にはなりません。`none` にすると乱数をやめます。
- インターネットゲートウェイは、インスタンスの私的なアドレスと公開アドレスを 1 対 1 で変換します（「Enable internet access for a VPC using an internet gateway」）。

ここからの推定です。実際のクラウドでの試験で確かめるまで、どれも推定です。

- UE が送った REGISTER の流れが NLB とノードの両方に残っているあいだは、Pod から同じ相手への UDP は、ノードの conntrack では戻りの向きとして扱われ、SNAT されません。NLB も、それを応答として扱い、NLB のアドレスの 5060 から UE へ送るはずです。UDP には要求と応答の区別が無いので、同じ流れの逆向きのパケットが応答なのか、ターゲットが自分から送ったものなのかを、NLB は見分けられないと考えるからです。
- 流れが消えたあとは、Pod が送るパケットは新しい流れです。ノードで SNAT され、ノードの公開アドレス（または NAT ゲートウェイのアドレス）の乱数のポートから出ます。UE には NLB とは別のアドレスとポートから INVITE が届きます（3 章の問題）。
- つまり NLB を使うなら、UE からのキープアライブを 120 秒より短い間隔で送り続け、流れを消さないことが条件になります。

### Azure の Standard Load Balancer（AKS）

| 項目 | 文書で確かめたこと |
| --- | --- |
| UDP | 負荷分散の規則は TCP か UDP を扱います。1 つの規則は 1 つのプロトコルと 1 つのポートです（内部のロードバランサーの HA ports の規則を除く） |
| 混在プロトコル | AKS の文書は MixedProtocolLBService の Service を前提に、ポートごとのヘルスプローブの設定を説明しています |
| floating IP | 有効にすると、バックエンドには宛先がロードバランサーの公開アドレスのまま届きます。無効だと、バックエンドの VM のアドレスに書き換わります。同じバックエンドのポートを複数の規則で使うときに要ります（「Azure Load Balancer Floating IP configuration」）。AKS の部品（cloud-provider-azure）は既定で有効にします。無効にするのは annotation `service.beta.kubernetes.io/azure-disable-load-balancer-floating-ip: "true"` を付けたときだけです。`allocateLoadBalancerNodePorts: false` は、floating IP が有効で `externalTrafficPolicy: Local` のときに使えます（cloud-provider-azure の「Azure LoadBalancer」） |
| idle timeout | 既定は 4 分です（AKS の外の Standard Load Balancer）。annotation `service.beta.kubernetes.io/azure-load-balancer-tcp-idle-timeout` は TCP のためのもので、4 から 100 分です。UDP の負荷分散の規則は idle timeout に対応していないと、制限の節に書かれています（「Load Balancer TCP Reset and Idle Timeout」）。では UDP の流れがいつ消えるかは、読んだ文書には書かれていませんでした |
| ヘルスプローブ | `externalTrafficPolicy: Cluster` の UDP の Service には、既定でプローブがありません。`Local` では HTTP の `/healthz` で、kube-proxy のヘルスチェックのポートを調べます。プローブが落ちたバックエンドの UDP の流れは、ほかのバックエンドへ移ります。全部が落ちると UDP の流れは切れます |
| 送信元の制限 | `spec.loadBalancerSourceRanges`。NSG の規則になります |
| 固定のアドレス | `service.beta.kubernetes.io/azure-pip-name` で、先に作った公開 IP を名前で指定します |
| 外向きの SNAT | ロードバランサーの outbound 規則で外へ出る UDP は、宛先の IP ごとに 1 つの SNAT のポートを使う port-restricted cone NAT になります。AKS は outbound 規則の idle timeout を 30 分にしています（4 から 120 分に変えられます）。AKS の既定の outbound の種類は `loadBalancer` で、AKS は外向き専用の公開 IP を自分で作り、ロードバランサーの outbound の側に付けます（「Configure a public standard load balancer」）。つまり、Service の公開アドレスとは別のアドレスから出ます |
| VM の公開 IP | ノードのインスタンスに公開 IP があると、外向きの通信はすべてそのアドレスから出ます。SNAT は使わず、1 対 1 の状態を持たない NAT で、TCP と UDP のほか ICMP と ESP も通ります。ロードバランサーの outbound 規則より優先されます（NAT ゲートウェイがあればそちらが優先）。VM の公開 IP の外向きの idle timeout は 4 分に固定です（「Source Network Address Translation (SNAT) for outbound connections」、「Load Balancer TCP Reset and Idle Timeout」） |
| 上限 | Standard Load Balancer 1 つに、負荷分散と inbound NAT の規則が合わせて 1,500、フロントエンドの IP が 600 です。加えて、1 つの NIC に掛かる規則（負荷分散、inbound NAT、outbound の合計）は 300 までです（「Azure subscription and service limits」） |

### ロードバランサーを使わない方法（ノードの公開アドレスと hostPort）

[k8s.md](k8s.md) の 6 章で、kind で hostPort を試しました。ホストの `ue` から hostPort の 5060 と 40000〜40039 を通って発信でき、RTP も両方向に流れました。同じ形を、公開アドレスを持つノードに載せる方法があります。

- AKS は、ノードプールに `--enable-node-public-ip` を付けると、各ノードに公開 IP を付けます。`--allowed-host-ports` に `5060/udp,5060/tcp,40000-40039/udp` のような範囲を書くと、NSG に許可の規則を作ります（AKS 1.29 から）。外向きの通信は、そのノードの公開 IP から出ます（上の表）。`--node-public-ip-prefix-id` で、先に作った公開 IP prefix からアドレスを取らせることもできます。ノードが受け取ったアドレスは `az vmss list-instance-public-ips` で、ノードの資源グループ（`MC_` で始まるもの）を指定して一覧にします（「Use instance-level public IPs in AKS」）。
- EKS では、公開サブネットに置いたノードに公開 IPv4 か Elastic IP があると、Pod はノードを通してインターネットと話せます（EKS のガイド）。マネージドノードグループを公開サブネットに置くには、サブネットの `MapPublicIpOnLaunch` を有効にして、公開アドレスを自動で割り当てる必要があります（「Simplify node lifecycle with managed node groups」）。外向きの通信は、ノードの主のプライベートアドレスに SNAT され、インターネットゲートウェイが 1 対 1 で公開アドレスに変えます（「Enable internet access for a VPC using an internet gateway」）。
- Amazon VPC CNI の既定の CNI の設定（`misc/10-aws.conflist`）は、`portmap` のプラグインをつないでいます。hostPort はこのプラグインで動くので、EKS でも使えるはずです。ただし、これは EKS のユーザーガイドではなく、CNI のリポジトリの設定ファイルで確かめたものです。Azure CNI で hostPort が使えるかは確かめていません。AKS の文書は、hostPort の通信は既定の NSG の規則で止められるので、`--allowed-host-ports` と組み合わせるよう書いています。
- hostNetwork は使いません。ホスト固有のネットワーク設定に依存しない方針のため、IMS のノードには使いません。hostPort は Pod のネットワークのままで、ポートだけをノードにつなぎます。
- Pod Security Standards の baseline は、hostNetwork を禁じ、hostPort を「禁じるのを勧め、許すなら一覧に絞る」としています。IMS の namespace に baseline を強制する設定があれば、hostPort の Pod は拒まれます。EKS と AKS が既定で強制するかは、確かめていません。

## 3. 外から見えるアドレスと、ノードが書くアドレス

### 問題の形

Compose と kind では、UE とノードの間に NAT がほとんどありませんでした（ホストの NAT を除く）。クラウドでは、UE とノードの間に最大で 3 つの変換が入ります。

1. UE の手前の NAT（家庭のルーター、携帯の CGNAT）。UE は自分の公開アドレスを知りません。
2. クラウドの入口。ロードバランサーか、ノードの公開 IP の 1 対 1 の NAT です。
3. ノードの中。hostPort の DNAT、kube-proxy の NAT、Amazon VPC CNI の SNAT です。

このうち 2 と 3 は網の側にあり、網が責任を持って、外から見えるアドレスと中のアドレスの対応を保つ必要があります。

### SIP の向きの要件（P-CSCF）

P-CSCF が書くものと、UE がそれをどう使うかを、今のコードで確かめました。

| 何を | P-CSCF が書くもの | UE がどう使うか |
| --- | --- | --- |
| UE からの要求への応答 | UE の Via に `received` と `rport` を足し、その送信元へ返します（RFC 3581 4） | UE は自分が送ったポートで受けます |
| P-CSCF の Via（UE への要求） | `sip.host` の FQDN と 5060 | UE は応答を、受けた要求の送信元のアドレスと、sent-by のポート 5060 へ送ります（RFC 3261 18.2.2。sent-by が名前なので、UE は `received` に送信元を書きます） |
| Path、Record-Route、Service-Route | `sip.host` の FQDN（`pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060`） | 今の `ue` は、すべての要求を `-pcscf` のアドレスへ送るので、この名前を引きません（`internal/ue/ue.go` の `PCSCF`）。ソフトフォンが Route の名前を引くかは、その設定によります |
| UE への要求の宛先 | 登録の要求を受けた送信元（`Association.Source`）。Contact の私的なアドレスには送りません | UE の NAT は、登録で開いた対応付けを通して受けます |

ここから、P-CSCF の置き方に 1 つの強い条件が出ます。**P-CSCF が UE へ送る要求は、UE が登録で送った先と同じ公開アドレスとポート（5060）から出なければなりません。** そうでないと、次のことが起きます。

- UE の手前の NAT が、アドレスに依存するフィルタ（RFC 4787 5 の Address-Dependent Filtering か Address and Port-Dependent Filtering）なら、知らない相手からの INVITE を捨てます。着信が届きません。
- 届いても、UE は応答を「要求の送信元のアドレス」の 5060 へ送ります。それが NAT ゲートウェイや別の SNAT のアドレスなら、P-CSCF に戻りません。

案ごとに見ると、次のようになります。

| 置き方 | UE から P-CSCF | P-CSCF から UE（P-CSCF が始めるもの） | 判定 |
| --- | --- | --- | --- |
| ノードの公開 IP と hostPort | 公開 IP の 5060 → 1 対 1 の NAT → ノード → hostPort の DNAT → Pod | ノードの conntrack に UE の流れが残っていれば、戻りの向きとして DNAT を戻し、ノードの 5060 → 1 対 1 の NAT で公開 IP の 5060。残っていなければ新しい流れで、ノードの SNAT を通る | 条件付きで合う（推定）。EKS では、新しい流れの SNAT は既定で送信元ポートを乱数にします（2 章）。conntrack の項目より短い間隔のキープアライブが要ります。AKS のノードでの SNAT の振る舞いは確かめていません |
| AWS NLB、`ip`（UDP は client IP を必ず保つ） | NLB → Pod。Pod には UE の公開アドレスが見える | NLB とノードに流れが残っていれば、NLB のアドレスの 5060 から出るはず。消えていれば、ノードの SNAT で別のアドレスの乱数のポートから出る（2 章の「Pod が始める UDP の道」） | 条件付きで合う（推定）。NLB の UDP の流れは 120 秒で消え、変えられません。応答は NLB を通って戻ると文書から読めます |
| AWS NLB、`instance`、`externalTrafficPolicy: Local` | NLB → ノードの NodePort → kube-proxy の DNAT → 同じノードの Pod。UE の公開アドレスが見える | `ip` と同じ。流れが残っていれば DNAT を戻して NLB へ。消えていれば SNAT | `ip` と同じ（推定）。`Cluster` にすると、ノードの間の転送で送信元がノードのアドレスに替わり、P-CSCF は UE のアドレスを失います |
| Azure LB（floating IP は既定で有効）と outbound 規則 | LB → ノード → Pod。宛先は LB の公開アドレスのまま届く | 戻りは LB を通る。P-CSCF が始めるものは、流れが LB に残っていなければ outbound 規則の SNAT を通り、AKS が作った外向き専用の公開 IP と別のポートから出る | 戻りは合う。P-CSCF が始めるものは、流れが LB に残っている間だけ合う（推定）。UDP の流れの寿命は文書にありません |

どの置き方でも、P-CSCF が始める要求が正しいアドレスから出るのは、UE が開いた流れが途中の全部の機器に残っているあいだだけ、という形になりました。流れを保つキープアライブの間隔は、途中の寿命のうちいちばん短いものより短くする必要があります（下の「キープアライブと、NAT とロードバランサーの寿命」）。

ロードバランサーには、もう 1 つの問題があります。P-CSCF を複数台にすると、ロードバランサーは流れごとに台を選びます。UE の流れが idle で消えたあとの次のパケットは、別の台に着くことがあります。AWS の NLB については、文書にそう書かれています。120 秒パケットが無かった流れは新しい流れとして扱い、別のターゲットへ送ることがあります。IMS は、UE が 1 つの P-CSCF のアドレスに向けて登録している前提で作られています（[redundancy.md](redundancy.md) の 2 章「Service-Route と Path」）。台ごとに公開アドレスを持つほうが、標準の形に近く、各役割を水平に広げる要件の「DNS で振り分ける」にも合います。

### SDP のアドレス（メディア中継ノード）

今の作りは次のとおりです。

- agw は `rtp_address` を 1 つだけ持ち、access と core の両方のターミネーションの Local にそのアドレスを書きます（`internal/agw/relay.go` の `listen`。ソケットは 0.0.0.0 で開きます）。
- P-CSCF は、agw が返した Local を SDP に書きます。ただし `host_access` があり、UE の送信元が `host_access.gateway` と同じときだけ、access の側を `host_access.address` に替えます（`internal/pcscf/session.go` の `advertise`）。Compose で、ホストの NAT を通るソフトフォンのための設定です（[ホストとコンテナの通信とキャプチャの設計](../design/environment-capture.md)）。

クラウドでは、これでは足りません。

- インターネットの UE の送信元は、UE ごとに違う公開アドレスです。`host_access.gateway` のような 1 つのアドレスで見分けられません。
- `rtp_address` を公開アドレスにすると、core の側にも公開アドレスが書かれます。クラスタの中の相手（S-CSCF の先の、もう 1 つの P-CSCF の脚）にも公開アドレスを渡すことになり、RTP がいったんクラスタの外へ出て戻る（ヘアピン）か、届きません。

足りないのは、**access の側にだけ使う、外から見えるアドレス**です。置き場所の候補は 2 つあります。

| 案 | 内容 | 長所 | 短所 |
| --- | --- | --- | --- |
| agw が持つ | agw の設定に access の側のアドレス（例 `access_address`）を足し、access のターミネーションの Local にそれを書く | 中継ノード（IMS-AGW）が自分のアドレスを答えるのは、H.248 で MG が Local の記述子を返すのと同じ形です（[メディア中継ノードの制御の設計](../design/media-control.md)） | agw の台ごとに値が違います |
| P-CSCF が持つ | `host_access` を広げ、送信元に関わらず、access の側を決まったアドレスに替える | 今の仕組みの延長です | agw が複数台になると、どの agw の公開アドレスかを P-CSCF が知る必要があります |

結論は agw が持つ案です。`host_access` は Compose のために残します。どちらも `internal/config` と `internal/agw` か `internal/pcscf` の変更です。

その値をどう入れるかも決める必要があります。Pod は downward API でノードのアドレスを取れますが、取れるのは `status.hostIP`（ノードのプライベートアドレス）です。公開アドレスは取れません。候補は、(1) 資源の定義のコード（6 章）が公開アドレスを決め、overlay の ConfigMap に書く、(2) Node の `status.addresses` の ExternalIP を init コンテナが API で読む、(3) クラウドのインスタンスのメタデータを読む、です。(1) が単純で、クラウドの違いが Kubernetes の中に入りません。ただし、ノードに付く公開 IP を先に決められるかが問題です。EKS の公開サブネットのノードは、起動したときに公開アドレスを自動で割り当てられるので、作る前には決まりません。マネージドノードグループのノードに Elastic IP を付ける方法は、読んだ文書には見当たりませんでした。AKS の公開 IP prefix は、アドレスの範囲を先に決められます。ただし、どのノードがどのアドレスを取るかを決める方法は、文書には見当たりませんでした。どちらも、作ったあとでアドレスを読み、ConfigMap に書く手順になります。(2) で ExternalIP が入るかは、各社で確かめていません。`deploy/cloud/azure/` は、edge のノードプールに公開 IP prefix を付けました。範囲はノードより先に決まり、出力 `edge_public_ip_prefix` に出ます。

### UE の側の NAT と対称 RTP

- 今の `ue` は、RTP を送るソケットと受けるソケットが同じです（`internal/ue/media.go` の 1 つの `ListenUDP`）。RFC 4961 3 の対称 RTP です。RFC 4961 4 は、NAT と中継ノードを越えるには対称 RTP が要るとし、いつも使うよう勧めています。
- agw は、RTP と RTCP のそれぞれで、最初に届いたパケットの送信元にラッチし、そこへ返します（`relay.go` の `relay`）。RFC 7362 4 の手順と同じです。ラッチは呼のあいだ変えません（`SIMPLIFIED:` RFC 4961）。RFC 7362 4 の 6 も、新しい offer と answer が無いうちのラッチのし直しを、乗っ取りの恐れから普通はしないとしています。
- UE が先に RTP を送るまで、agw は SDP にある私的なアドレスへ送るので、届きません。UE の NAT の対応付けは、UE が先に送ったときに開きます。今の `ue` は通話が始まるとすぐに音を送るので、困りません。一度も送らない UE には、音がまったく届きません。普通のソフトフォンもすぐに送るので、欠けるのは最初の少しだけのはずです（推定）。
- 1 章の restricted latching を入れるなら、P-CSCF が `Modify` に UE の信号の送信元のアドレスを渡し、agw はそのアドレスからのパケットにだけラッチします。これも `internal/pcscf` と `internal/agw` の変更です。

### キープアライブと、NAT とロードバランサーの寿命

UDP の対応付けは、パケットが流れないと消えます。消えると、網から UE への着信（INVITE、NOTIFY、MESSAGE）が届きません。

| 場所 | 寿命 | 出典 |
| --- | --- | --- |
| UE の手前の NAT | 2 分より短くしてはいけない。既定は 5 分以上を勧める（REQ-5）。ただし RFC 5626 4.4.2 は、30 秒で消す NAT が多いと書いています | RFC 4787 4.3、RFC 5626 4.4.2 |
| AWS NLB の UDP | 120 秒。変えられない | NLB の「Network Load Balancers」 |
| AWS のセキュリティグループの接続の追跡 | 追跡される UDP の流れは、片方向か 1 往復だけなら 30 秒（30 から 60 秒に変えられる）、往復が続く流れなら 180 秒（60 から 180 秒）。送信元を 0.0.0.0/0 にしない規則では流れが追跡されます。UE のアドレスを内向きの規則で許していれば、追跡が切れても UE からのパケットは通ります | EC2 の「security group connection tracking」 |
| Azure LB の UDP の負荷分散の規則 | 設定が無い。値は確かめていません | idle timeout の文書の制限の節 |
| Azure LB の outbound 規則（AKS） | 30 分（AKS の既定。4 から 120 分）。VM の公開 IP の outbound は 4 分に固定 | AKS と outbound の文書 |
| ノードの conntrack | kube-proxy の設定に、返事の無い UDP の項目の寿命（`conntrack.udpTimeout`）と、往復した UDP の項目の寿命（`conntrack.udpStreamTimeout`）があります。既定の値は設定の文書に書かれていません | 「kube-proxy Configuration (v1alpha1)」 |

キープアライブの標準は次のとおりです。

- RFC 5626 4.4.2。UDP では STUN の Binding 要求をキープアライブに使います。Flow-Timer が無ければ、24 秒から 29 秒の間の乱数の間隔で送ります。30 秒で消す NAT に合わせた値です。
- RFC 5626 4.4.1。CRLF のキープアライブは TCP などの接続のためのものです。Flow-Timer が無ければ、上限 120 秒（下限 95 秒）を勧めます。
- RFC 6223 4.4 と 5。Via の `keep` パラメータで、キープアライブを送るかを隣のノードと取り決めます。受ける側は STUN のサーバーとして振る舞わなければなりません。値があれば、その秒数の 80% から 100% の間隔で送ります。

今の作りは次のとおりです。

- `internal/sip/transport.go` は、CRLF だけの UDP のデータグラムを黙って捨てます。pjsua がこれを送るためです。STUN には答えません。
- P-CSCF は outbound に対応していません（`internal/pcscf/pcscf.go` の `SIMPLIFIED:` TS 24.229 5.2.2.1 と RFC 5626）。
- `ue` はキープアライブを送りません。登録の更新は `-expires` の間隔です。S-CSCF の `min_expires` は 60 秒です。

短い実験なら、`ue` の `-expires` を 60 秒にし、更新の REGISTER をキープアライブの代わりにするのが手軽です。60 秒なら NLB の 120 秒より短いので、NLB の流れは保てるはずです。それでも 30 秒で消す NAT では、着信が届かない時間ができます。きちんと扱うなら、P-CSCF が STUN の Binding に答え、`ue` が RFC 5626 4.4.2 の間隔で送る作りになります。24 秒から 29 秒の間隔なら、上の表で分かっている寿命のどれよりも短くなります。3 章の判定では、どの置き方でもキープアライブが P-CSCF からの着信の条件になったので、クラウドの構成ではこちらを作るのがよいと考えます（未決の論点 4）。どちらも `internal/ue` と `internal/sip` などの変更です。TS 24.229 にある NAT 越えの付録（UE と P-CSCF の手順）は、条項を確かめていません。

## 4. RTP の広いポートの範囲

商用の中継ノードは、数千から数万の RTP のポートを使います。今の `rtp_ports` は 40000〜40039 の 40 ポートです。ターミネーション 1 つに RTP と RTCP の 2 ポートを使い、コンテキスト 1 つに access と core の 2 つのターミネーションがあります。両方の UE が同じ P-CSCF を通る呼は、発と着で 2 つのコンテキストを使うので、同時に 5 呼ほどです。

| 方法 | ポートの書き方 | 上限と問題 |
| --- | --- | --- |
| LoadBalancer の Service | ポートを 1 つずつ（2 章。Kubernetes に範囲の書き方はありません） | Azure は規則が 1 LB に 1,500、1 NIC に 300 まで。AWS NLB のリスナーは 1 NLB に 50 までで上げられないので、40 ポートと 5060 でほぼ埋まります。台が複数あると、LB が流れごとに台を選ぶので、SDP に書いたポートを持たない台に着きます |
| 台ごとの LoadBalancer の Service | StatefulSet の Pod の名前のラベル（`statefulset.kubernetes.io/pod-name`）で 1 台を選ぶ | 台の数だけロードバランサーと公開アドレスができ、費用が増えます（7 章の金額） |
| hostPort | コンテナのポートを 1 つずつ | 1 ノードに 1 台です。40 ポートなら困りません。AKS の `--allowed-host-ports` は範囲で書けます。AWS のセキュリティグループの規則も、TCP と UDP のポートを範囲で書けます（「Security group rules」） |
| hostNetwork | 不要 | ホスト固有のネットワーク設定に依存しない方針のため使いません |

結論は、範囲を今の 40 ポートのまま狭く保ち、agw を hostPort でノードの公開アドレスに出すことです。台を増やすときは、ノード（公開アドレス）ごとに 1 台にします。これは「中継ノードごとに 1 つの公開アドレス」と同じ形で、[redundancy.md](redundancy.md) の 5 章の「中継ノードを一覧にする」直し方とも合います。ロードバランサーに RTP を載せる案は、台を 1 つに限る学習用の比較にとどめます。

## 5. クラウドの DNS

### 網の中の DNS

`deploy/k8s` の 2 つの構成のうち、クラウドに合うのは (b) external-dns です。

- (a) cluster-dns は kube-system の Corefile を置き換えます。EKS と AKS の CoreDNS は、クラウドが管理するアドオンです。EKS のアドオンは Corefile を管理していて、直接変えるとアドオンの更新で上書きされます。残すには、アドオンの `configurationValues` の `corefile` に書きます（「Manage CoreDNS for DNS in Amazon EKS clusters」）。AKS では Corefile そのものは変えられず、`coredns-custom` の ConfigMap で上書きします（「Customize CoreDNS」）。どちらもクラウドごとの手順になるので、管理されたものに手を入れない (b) が合います。
- (b) は、DNS サーバーの Service に固定の ClusterIP 10.96.0.53 を使います。これは kind の Service の範囲 10.96.0.0/16 の中のアドレスです。EKS は、指定しなければ 10.100.0.0/16 か 172.20.0.0/16 のどちらかを選びます。指定できるのはクラスタを作るときだけです（「Create an Amazon EKS cluster」の `serviceIPv4CIDR`）。AKS は、Azure CNI Overlay の文書によると、指定しなければ 10.0.0.0/16 です。どちらも kind と違うので、範囲は資源の定義のコードで指定して決めておくのがよいと考えます。この ClusterIP、ゾーンの `dns` の A レコード、Pod の `dnsConfig.nameservers` の 3 か所を、クラウドごとの値にする必要があります。3 か所に同じ値を直書きすると変えにくいので、overlay で 1 か所から変えられる形にします（値や手順をあちこちに直書きしない方針）。`deploy/cloud/` は、Service の範囲の既定を kind と同じ 10.96.0.0/16 にしました。EKS も AKS もこの範囲を受け付けるので（EKS は「Create an Amazon EKS cluster」の条件に合います）、既定のままなら 10.96.0.53 を変えずに済みます。範囲を変えたときの値は、出力 `kubernetes` の `ims_dns_cluster_ip` に出ます（6 章）。この 3 か所（cluster-dns の `ims-dns` の Service を入れると 4 か所）は、`deploy/k8s/environments/<クラスタ>` の 1 か所から変えられます（9 章）。

### インターネットから引く名前

- ホームドメインの `ims.mnc001.mcc001.3gppnetwork.org` は、利用者が持つドメインではありません。公開の DNS に載せられません。
- 今の `ue` は P-CSCF を `-pcscf` のアドレスで受け取るので、公開の DNS が無くても動きます。REGISTER の Request-URI のドメインを引くのは、クラスタの中の P-CSCF です（[k8s.md](k8s.md) の 7 章）。中の DNS だけで足ります。
- ソフトフォンに P-CSCF を名前で渡したいときは、利用者が持つドメインのゾーン（Route 53 か Azure DNS）に A レコードを 1 つ置きます。Azure DNS も Route 53 も、SRV と NAPTR を載せられます（Route 53 は「Supported DNS record types」で確かめました）。
- NAPTR と SRV（RFC 3263）でインターネットの UE に P-CSCF を探させるのは、3GPP の P-CSCF の見つけ方（PCO や DHCP）とは違う形です。学習のために足すなら、利用者のドメインに `_sip._udp` の SRV を置き、ソフトフォンにドメインだけを渡す例になります。必須ではありません。
- `sip.host` を公開の名前に替える案もあります。そのときは、中の DNS にも同じ名前を Pod へ向けて置く必要があります（同じ名前で中と外の答えが違う、split horizon）。今の `sip.host` のままでも動くので、クラウドの構成では替えないのが素直です。

## 6. 資源の定義のコード

### 道具の比較

| 道具 | ライセンス（LICENSE で確かめたもの） | 対象 | CI で資格情報なしにできること |
| --- | --- | --- | --- |
| Terraform | BSL 1.1（1.6.0 から）。追加の利用の許可は、IBM の有償版と競う形で第三者に提供しない限り、本番でも使ってよいというもの。各版の公開から 4 年で MPL 2.0 に変わる | AWS と Azure の両方 | `fmt -check`、`init -backend=false`、`validate`、`test`（mock_provider は v1.7.0 から） |
| OpenTofu | MPL 2.0 | 同上。Terraform と同じ HCL | 同上。加えて state の暗号化があります |
| eksctl | Apache 2.0 | AWS だけ。CloudFormation のスタックで資源を作ります（EKS の「Delete a cluster」の出力の例） | 確かめていない。`--dry-run` は ClusterConfig の YAML を出しますが、資格情報なしで動くかは文書にありません |
| Bicep | MIT | Azure だけ | 確かめていない |
| Pulumi | Apache 2.0 | 両方。汎用の言語で書く | 確かめていない |

CI で何を「計画」と呼べるかを確かめました。

- `validate` は、設定ファイルだけを見て、state やプロバイダーの API などの外のものには触れません。state が要る検査は飛ばします。前に `init -backend=false` で、プロバイダーとモジュールを入れておく必要があります（OpenTofu の「Command: validate」の例も `tofu init -backend=false` です）。プロバイダーを入れるにはレジストリに届く必要があります。
- `plan` は、既にある資源の状態を読み直し（refresh）、変更の案を作ります（「Command: plan」）。本物のプロバイダーでは、資格情報と、state の置き場所（backend）への接続が要ります。データソース（使えるゾーンの一覧など）も API を呼びます。資格情報なしの CI では、本物の `plan` は実行できません（推定。plan の文書は資格情報について直接は書いていませんが、refresh は外の資源を読む手順です）。
- `test` の `run` ブロックは、`command = plan` で plan だけを行えます。`mock_provider` でプロバイダーを置き換えると、資源とデータソースを作らず、計算される属性に作り物の値を入れます（OpenTofu の「Command: test」）。Terraform の文書は、これで資格情報なしにモジュールを試せると書いています（「Tests: mocks」。v1.7.0 から）。OpenTofu の文書には `mock_provider`、`mock_resource`、`mock_data`、`override_resource`、`override_data`、`override_module` があります。

つまり、ロードマップの完了条件「CI で資源の定義の検査（文法と計画）が通る」は、`fmt -check`、`init -backend=false`、`validate`、`test`（`command = plan` と `mock_provider`）の 4 つで満たすのが現実的です。実際のアカウントでの `plan` と `apply` は、利用者が手で行います。

### 試作の結果

OpenTofu と Terraform を手元に入れ、同じ小さな定義（AWS の VPC、公開サブネット、EKS、公開アドレス付きのノードグループ、SIP の規則 1 つ）で 4 つの手順を試しました。そのあと `deploy/cloud/` の本番の定義でも同じことをしました。どれも資格情報のない環境で行いました。AWS と Azure の環境変数は空にし、AWS のインスタンスのメタデータも読まない設定にしました。

| 項目 | OpenTofu | Terraform |
| --- | --- | --- |
| 試した版と取り先 | 1.13.0。GitHub のリリースの zip（35 MB）。SHA256SUMS で確かめました | 1.17.2。releases.hashicorp.com の zip（38 MB）。SHA256SUMS で確かめました |
| ライセンス（zip の中の文書） | MPL 2.0 | BSL 1.1 |
| `fmt -check` | 0.1 秒 | 0.1 秒 |
| `init -backend=false` | aws 6.70.0 で 15 から 20 秒、azurerm 5.11.0 で 7 秒。初回はプロバイダーを取ります | aws で 15 秒、azurerm で 7 秒 |
| `validate` | aws で 4 秒、azurerm で 1 秒 | aws で 4 から 5 秒、azurerm で 1 秒 |
| `test`（`mock_provider`、`command = plan`） | 通りました。AWS の 10 件で 14 秒、Azure の 9 件で 4 秒、共通のモジュールの 17 件で 0.3 秒 | 共通のモジュールの 17 件は通りました。AWS と Azure は、計算される属性に頼る assert が「値が決まらない」で失敗しました（下） |
| 取ったあとのネットワーク | プロキシの設定を届かない宛先に替えても（つまりネットワークなしでも）、`validate` と `test` は通りました。ネットワークが要るのは `init` だけです | 同じでした |
| ロックファイル | `.terraform.lock.hcl` に、registry.opentofu.org のプロバイダーの全部の OS と CPU の `h1:` のハッシュ（aws で 15 個）が入りました | 今の OS と CPU の `h1:` が 1 つだけでした |

プロバイダーの大きさです。zip は registry が示す取り先の Content-Length、展開後は `.terraform/providers` の大きさです。

| プロバイダー | zip | 展開後 |
| --- | --- | --- |
| hashicorp/aws 6.70.0（OpenTofu の取り先） | 178 MB | 796 MB |
| hashicorp/aws 6.70.0（Terraform の取り先） | 181 MB | 795 MB |
| hashicorp/azurerm 5.11.0（OpenTofu の取り先） | 72 MB | 248 MB |

試作で分かったことです。

- mock の値の作り方が 2 つの道具で違います。OpenTofu は、計画の段階で、計算される属性に作り物の値を入れます。Terraform は、計画の段階では値を「まだ決まらない」のままにし、`mock_resource` の `defaults` も `apply` のときにしか使いません。計画の段階で使わせるには `override_during = plan` を書きます。ところが OpenTofu 1.13.0 は、この引数を「知らない引数」として拒みます。つまり、計算される属性（ID など）を調べる試験は、2 つの道具で同じファイルを共有できません。`deploy/cloud/` の試験は、選んだ OpenTofu に合わせました。
- mock の作り物の値にも、プロバイダーの形の検査が掛かります。ARN（`arn:` で始まる）、起動テンプレートの ID（`lt-` で始まる）、Azure の資源の ID（`/subscriptions/...`）は、試験の中で形の合う値を与える必要がありました。値には、AWS の文書の例のアカウント番号 123456789012 と、全部 0 のサブスクリプションを使いました。
- `expect_failures` に書けるのは、ルートのモジュールの変数、出力、資源、check だけです。子のモジュールの変数の検査（`module.x.var.y`）や、子のモジュールそのものは書けません。2 つの道具とも同じでした。そこで、共通の入力の検査は共通のモジュールに置き、そのモジュールを単独で `test` して確かめる形にしました。
- 同じディレクトリで 2 つの道具の `init` を順に動かすと、あとから動かした方が `.terraform.lock.hcl` を自分の registry の項目だけに書き換えました。ロックファイルは道具ごとのものです。
- OpenTofu の registry はプロバイダーを OpenTofu の組織の GitHub のリリース（opentofu/terraform-provider-aws など）から配り、`tofu init` は OpenTofu の鍵の署名で確かめました。Terraform は releases.hashicorp.com から取り、HashiCorp の署名で確かめました。中身は同じ版の同じプロバイダーです（ライセンスはどちらも MPL 2.0。LICENSE で確かめました）。
- azurerm 5.11.0 では、`azurerm_kubernetes_cluster` の `node_provisioning_profile` のブロックが必須でした（`validate` で分かりました）。4 系の書き方の例をそのまま使うと通りません。

### 道具の判断

OpenTofu を選びました。試作から、次のことが言えます。

- 4 つの手順（`fmt -check`、`init -backend=false`、`validate`、`test`）が、資格情報なしで最後まで動きました。ネットワークが要るのは `init` だけで、届く先は registry.opentofu.org と GitHub のリリースです。
- ライセンスが MPL 2.0 です。Terraform の BSL 1.1 の追加の許可の読み方を考えずに済みます。
- ロックファイルに全部の OS と CPU のハッシュが入るので、手元が macOS や Windows の学習者も、同じロックファイルで `init` できます。
- HCL は Terraform と同じです。定義そのもの（`*.tf`）は、Terraform 1.17.2 でも `fmt`、`init`、`validate` が通りました。違うのは、上に書いた試験の mock と、ロックファイルだけです。手順書では、Terraform で動かすときの違いとしてこの 2 つを書きます。
- OpenTofu だけの機能（state の暗号化など）は使っていません。

使わなかった理由です。Terraform は、定義は同じように書けますが、ライセンスが BSL 1.1 で、試験の mock を OpenTofu と共有できません。eksctl と Bicep は、クラウドごとに道具と書き方が変わり、資格情報なしの検査の方法も確かめられていません（表の「確かめていない」のまま）。Pulumi は汎用の言語で書くので、学習者が読む量が増えます。

OpenTofu の取り方には注意が要ります。公式の導入スクリプト（get.opentofu.org）には試した環境から届かず、GitHub のリリースの zip を SHA256SUMS で確かめる、文書にある手作業の方法で入れました。CI の案もこの方法です。スクリプトは cosign か GnuPG で署名も確かめますが、この方法で確かめるのはハッシュだけです。

プロバイダーはリポジトリに入らず、ロックファイルに版とハッシュがあるだけです。プロバイダーのライセンスは MPL 2.0 です。

### ディレクトリの分け方

Kubernetes のマニフェストは `deploy/k8s` の共通のものを使い、資源の定義のコードには入れません（クラウドを配置先にしても、`deploy/k8s` と同じマニフェストを使うため）。Terraform の kubernetes プロバイダーでマニフェストを流す形にはしません。`kubectl apply -k` のままにすると、kind とクラウドで同じ手順になります。

作った形は次のとおりです。

```text
deploy/cloud/
  README.md, README.ja.md        手順書の下書き（作るもの、費用、作り方、後始末）
  .gitignore                     state、.terraform/、利用者の terraform.tfvars
  modules/ims-network/           クラウドに共通の部分。資源を作らず、プロバイダーも使わない
    variables.tf                 送信元の範囲、RTP のポート、Service の範囲などと、その検査
    main.tf, outputs.tf          開けるポートの一覧、DNS のアドレス、範囲の重なりの検査、ラベルと taint、タグ
    tests/ims_network.tftest.hcl 入力の検査の試験（17 件）
  aws/
    versions.tf                  道具とプロバイダーの版、既定のタグ
    variables.tf                 Azure と同じ名前の設定
    network.tf                   共通のモジュールの呼び出し、VPC、公開サブネット 2 つ、インターネットゲートウェイ
    cluster.tf                   IAM のロール、EKS、VPC CNI のアドオン、起動テンプレート、core と edge のノードグループ
    edge.tf                      edge のセキュリティグループと、その規則
    load-balancer.tf             切り替えで作る NLB（既定は作らない）
    outputs.tf                   kubeconfig のコマンド、アドレス、deploy/k8s に渡す値
    terraform.tfvars.example     利用者の設定の例（文書用のアドレス）
    .terraform.lock.hcl          プロバイダーの版とハッシュ
    tests/plan.tftest.hcl        mock_provider で plan を試す（10 件）
  azure/
    （同じ形。network.tf は資源グループ、VNet、core と edge のサブネット、NSG。cluster.tf は AKS、
     edge のノードプール、公開 IP prefix、ロールの割り当て。load-balancer.tf は固定の公開 IP。試験は 9 件）
  ci/cloud.yml.proposed          CI のジョブの案（`.github/workflows/` に置く前の形）
```

- クラウドごとの違いは、`aws/` と `azure/` の中だけにあります。両方の設定は同じ名前です（`region`、`cluster_name`、`allowed_source_cidrs`、`rtp_ports`、`service_cidr`、`ims_dns_cluster_ip`、`load_balancer`、ノードの大きさと数）。値の意味と検査は `modules/ims-network/` の 1 か所に書きました。SIP の 5060（標準のポート番号）、RTP の範囲の既定（`deploy/k8s/base/config/agw.yaml` と同じ 40000〜40039）、DNS のアドレスの決め方は、ここにしかありません（値や手順をあちこちに直書きしない方針）。
- `network/` と `cluster/` のサブモジュールは作らず、ファイルに分けました。どちらも 1 つのクラウドでしか使わないので、モジュールにすると入出力の受け渡しが増えるだけだからです（使う場所が 1 か所の抽象化は作らない方針）。
- 公開の仕方は変数 `load_balancer` で切り替えます。`none`（8 章の案 1、既定）、`sip`（案 2）、`sip-and-rtp`（案 3）です。
  - AWS では NLB を定義の中で作り、edge のノードのインスタンスの、hostPort と同じポートへ送ります。Service から NLB を作るには AWS Load Balancer Controller を入れる必要がありますが、この形なら要りません。案 1 と同じ Pod に届きます。UDP と TCP_UDP のターゲットグループはクライアントのアドレスを保つので、edge のセキュリティグループの送信元の制限がそのまま効きます。リスナーの数が 50 を超えると計画の段階で止めます。
  - Azure では、AKS のクラウドの部品が LoadBalancer の Service から規則を作ります。定義で作るのは、Service に付ける固定の公開 IP と、それを使うためのロールの割り当てだけです。AKS が管理する VM Scale Set を、AKS の外から別のロードバランサーにつなぐ方法は、読んだ文書にありませんでした。1 つの NIC の規則の上限 300 を超えると、計画の段階で止めます。
  - NLB の UDP の流れの 120 秒は変えられないので、変数の説明と README に書きました。
- 送信元の範囲 `allowed_source_cidrs` には既定値がありません。空、0.0.0.0/0、/8 より広い範囲（0.0.0.0/1 と 128.0.0.0/1 を並べるような書き方も含む）、IPv6、ネットワークアドレスでない書き方（192.0.2.10/24 など）を拒みます。同じ範囲を Kubernetes の API の公開の口にも使います。
- edge のノードには、ラベルと taint（`ims-practice/pool=edge:NoSchedule`）を付けました。taint を許す Pod（クラウドの overlay で P-CSCF と agw に付ける）しか載らないので、ほかの役割が公開アドレスのノードに出ません。
- EKS では、ノードを公開サブネットに置き、私的なサブネットと NAT ゲートウェイは作りません（7 章の費用）。edge のセキュリティグループを付けるために起動テンプレートを使い、そこでクラスタのセキュリティグループも並べます（指定すると EKS が付けなくなるため）。core のノードは edge のセキュリティグループを持たないので、インターネットから受ける口がありません。API の公開の口は送信元を絞り、ノードは私的な口を使います。VPC CNI のアドオンで `AWS_VPC_K8S_CNI_RANDOMIZESNAT` を `none` にしました（3 章の推定。実際のクラウドで確かめる必要がある）。
- AKS では、edge のノードの公開アドレスを公開 IP prefix から取らせます。prefix の範囲は、ノードより先に決まるので、API サーバーの許可の一覧に入れられます。edge のノードは自分の公開アドレスで外へ出るので、これが無いと API サーバーに届きません。どのノードがどのアドレスを取るかは選べないので、作ったあとで `kubectl get nodes -o wide` で見ます。edge のサブネットの NSG で送信元を絞ります。既定の規則が、仮想ネットワークの中とロードバランサーのプローブだけを通し、ほかのインターネットからの通信を拒むからです。
- Service の範囲の既定は kind と同じ 10.96.0.0/16 にしました。こうすると、`deploy/k8s` が直書きしている DNS サーバーの ClusterIP 10.96.0.53 が、クラウドでもそのまま使えます。範囲を変えたときのために、出力 `kubernetes` が `ims_dns_cluster_ip`（範囲のホスト 53）、`cluster_dns_ip`（ホスト 10）、Pod の範囲（キャプチャのフィルタに使う）、edge のラベルと taint、RTP のポートを出します。`deploy/k8s` の側では、この値を 1 か所から変えられます（9 章）。
- Kubernetes のマニフェストは、`deploy/k8s` のものを `kubectl apply -k` で入れます。`deploy/k8s/overlays/cloud-aws/` と `cloud-azure/` は 9 章にあります。
- すべての資源にタグ `ims-practice=cloud` を付けます。AWS はプロバイダーの既定のタグと、起動テンプレートのタグ（インスタンス、ディスク、ネットワークインタフェース）です。Azure は資源ごとの `tags` です。

### state の置き場所

- 短い実験を 1 人で行うなら、ローカルの state で足ります。`deploy/cloud/aws/` と `deploy/cloud/azure/` には backend を書いていないので、state は各ディレクトリの `terraform.tfstate` になります。state には資源の識別子のほか、秘密が入ることがあるので、リポジトリに入れません。`deploy/cloud/.gitignore` で、`*.tfstate`、`.terraform/`、利用者の `terraform.tfvars` を Git の対象から外しました。ルートの `.gitignore` に同じものを足すかは、未決です。
- 複数の人や CI から `apply` するなら、S3（`use_lockfile=true` で S3 だけでロックできます）か、Azure Blob Storage（Blob Storage の仕組みでロック）に置きます。その置き場所は、IaC で消す範囲の外に、先に手で作ることになります。
- OpenTofu には state の暗号化があります。使うと、Terraform では同じ state を読めなくなるはずです（推定）。

## 7. 費用と後始末

### 使っていなくても課金されるもの

2026-11-20 に各社の料金のページを読みました。AWS のページは、金額の一部が本文と計算の例に書かれていたので、その値を書きます。表の値は桁を知るためのもので、課金の正確な規則はページで確かめてください。料金は変わるので、作る前に必ず読み直します。

Azure の料金のページ（AKS、Load Balancer、NAT Gateway、IP Addresses、Linux Virtual Machines）は、金額を JavaScript で入れていて、curl で取ったページでは金額の欄が空でした。AWS の EC2 のオンデマンドの料金表も同じでした。これらの金額は確かめていません。

| 資源 | AWS（金額と、ページが示すリージョン） | Azure | 調べるページ |
| --- | --- | --- | --- |
| Kubernetes の制御面 | EKS のクラスタ 1 つに 1 時間 0.10 ドル（標準サポートの版）。延長サポートの版は 0.60 ドル。リージョンの区別はページにありません | AKS の Free の tier は、クラスタの管理が無料です（AKS の「Free, Standard, and Premium pricing tiers」と料金のページ）。Standard と Premium の金額は確かめていません | Amazon EKS pricing、Azure Kubernetes Service (AKS) pricing |
| ノード | EC2 のインスタンスと EBS のボリューム。金額は確かめていません | VM Scale Set の VM とマネージドディスク。金額は確かめていません | Amazon EC2 On-Demand Pricing、Linux Virtual Machines Pricing |
| NAT ゲートウェイ | 1 時間 0.045 ドルと、処理したデータ 1 GB に 0.045 ドル（US East (Ohio) の例）。インターネットへ出すデータには、別にデータ転送の料金が掛かります（例では 1 GB に 0.09 ドル） | 時間とデータ量で課金されることだけ、ページの欄の名前から分かります。金額は確かめていません | Amazon VPC pricing、Azure NAT Gateway pricing |
| ロードバランサー | NLB は 1 時間 0.0225 ドルと、使った NLCU 1 つに 1 時間 0.006 ドル（US East (N. Virginia) の例）。UDP の NLCU 1 つは、新しい流れが毎秒 400、生きている流れが 50,000、処理するデータが 1 時間 1 GB です | 時間と規則の数で課金されることだけ、ページの欄の名前から分かります。金額は確かめていません。AKS の既定の outbound の種類（`loadBalancer`）では、Service が無くても外向きの通信のためにロードバランサーと公開 IP を作ります（「Configure a public standard load balancer」） | Elastic Load Balancing pricing、Load Balancer pricing |
| 公開アドレス | 公開 IPv4 アドレス 1 つに 1 時間 0.005 ドル。資源に付いていても、付いていなくても（idle）同じ金額です。Elastic IP もノードの自動の公開アドレスも、NLB のアドレスも数えます | 金額は確かめていません | Amazon VPC pricing、IP Addresses pricing |
| DNS のゾーン | Route 53 のホストゾーン。金額は確かめていません | Azure DNS のゾーン。金額は確かめていません | Amazon Route 53 pricing、Azure DNS pricing |
| state の置き場所 | S3 | Storage アカウント | 各社のストレージの料金 |

既定の構成（8 章の案 1）は、NAT ゲートウェイとロードバランサーを作りません。ノードを公開サブネットに置くので、外向きの通信にも NAT ゲートウェイが要りません。それでも、制御面（AWS）、ノード、ディスク、公開アドレスは、使っていない時間も課金されます。AWS で分かった値だけを足すと、制御面 0.10 ドルと、ノード 2 台の公開アドレス 0.01 ドルで、ノードの料金を除いて 1 時間 0.11 ドルほどです。案 2 で NLB を 1 つ足すと、0.0225 ドルと NLCU の分、それに NLB のアドレス（アベイラビリティーゾーンの数だけ）の分が増えます。NAT ゲートウェイを使うと、1 つにつき 1 時間 0.045 ドルが増えます。

`deploy/cloud/` が既定（`load_balancer = "none"`）で作る、時間で課金されるものは次のとおりです。

- AWS。EKS の制御面 1 つ、EC2 のインスタンス 2 台（edge と core を 1 台ずつ）とそのディスク、インスタンスの公開 IPv4 アドレス 2 つ。NAT ゲートウェイとロードバランサーは作りません。分かっている金額だけで、インスタンスとディスクを除いて 1 時間 0.11 ドルほどです。`load_balancer` を `sip` か `sip-and-rtp` にすると、NLB 1 つと Elastic IP 1 つが加わります。
- Azure。AKS の制御面（Free の tier）、VM 2 台とそのディスク、edge のノードの公開 IP prefix 1 つ、AKS が外向きの通信のために作る Standard Load Balancer とその公開 IP。`load_balancer` を変えると、固定の公開 IP が 1 つか 2 つ加わります。金額は確かめていません。

AKS はクラスタを止められます（`az aks stop`）。止めると制御面とノードが止まり、計算の費用が掛からなくなります（「Stop and start an AKS cluster」）。ロードバランサー、公開 IP、ディスクの費用が止まるかは書かれていません。止めたクラスタの状態は 12 か月まで保たれます。EKS に同じ機能があるかは確かめていません。

### 消す順番

1. Kubernetes の LoadBalancer の Service を先に消します。EKS のガイドは、クラスタを消す前に `kubectl get svc --all-namespaces` で一覧にし、EXTERNAL-IP のある Service を `kubectl delete svc <名前>` で消して、ロードバランサーと関わる資源を解放するよう書いています。Ingress も同じで、消さないと Application Load Balancer が残るとあります（「Delete a cluster」）。消さずにクラスタを消すと、コントローラーが作ったセキュリティグループが残り、VPC を消せなくなることがあります（推定）。
2. `tofu destroy` で、IaC が作ったものを消します。
3. 残りを探します（下）。
4. AKS では、クラスタを消すと、ノードの資源グループ（`MC_` で始まるもの）と、その中の VM Scale Set、VM、仮想ネットワーク、ストレージも消えます（「Delete an AKS cluster」）。IaC で作った資源グループが残っていないかも確かめます。

IaC を使わずに作ったときのために、文書にある消すコマンドを並べます。この文書のために実行したものはありません。

| クラウド | 消すコマンド | 出典 |
| --- | --- | --- |
| AWS（eksctl で作ったとき） | `eksctl delete cluster --name <クラスタ>`。ノードグループとクラスタの CloudFormation のスタックを順に消します。eksctl の 0.215.0 以降が要ります | EKS の「Delete a cluster」 |
| AWS（AWS CLI） | `aws eks list-nodegroups --cluster-name <クラスタ>` と `aws eks delete-nodegroup --nodegroup-name <ノードグループ> --cluster-name <クラスタ>` でノードグループを消し、`aws eks delete-cluster --name <クラスタ>` でクラスタを消します。自分で作ったノードの CloudFormation のスタックは `aws cloudformation delete-stack --stack-name <スタック>` で消します | 同上 |
| Azure | `az aks delete --name <クラスタ> --resource-group <資源グループ> --yes --no-wait` | AKS の「Delete an AKS cluster」 |

EKS のクラスタは、削除の保護が有効だと消せません。先に無効にします（「Delete a cluster」）。

### 何も残っていないことの確かめ方

- すべての資源に、このプロジェクトのタグ（例 `ims-practice=cloud`）を付けます。IaC の既定のタグと、AWS Load Balancer Controller の `additional-resource-tags` で付けられます。AKS がノードの資源グループに作るものにタグが付くかは確かめていません。
- 後始末のスクリプトは、タグで資源を探し、加えて、タグが付かないことがある種類（ロードバランサー、公開 IP、Elastic IP、NAT ゲートウェイ、ネットワークインタフェース、ディスク、セキュリティグループ、ロググループ）をリージョンごとに一覧にし、残っていれば失敗します。AWS では、付いていない Elastic IP も 1 時間 0.005 ドルで課金され続けるので、特に確かめます。一覧に使う CLI のコマンドは、作るときに各社の文書で確かめます。この文書で確かめたのは、上の表の消すコマンドと、AKS のノードの公開 IP の一覧（`az vmss list-instance-public-ips`）だけです。
- 最後に、各社の請求の画面で、その日の利用が止まったことを翌日に確かめるよう、手順書に書きます。

## 8. 設計の候補

| 案 | SIP（P-CSCF） | RTP（agw） | 長所 | 短所 |
| --- | --- | --- | --- | --- |
| 1. edge のノードに公開アドレス、hostPort | ノードの公開 IP の 5060 を hostPort で Pod へ | 同じノードの 40000〜40039 を hostPort で | 出入りの公開アドレスが 1 つで、間の変換が 1 対 1 の NAT と hostPort だけです（3 章）。kind で試した形と同じです。ロードバランサーと NAT ゲートウェイが要らず安価です。台ごとに公開アドレスを持ち、標準の P-CSCF の形に近いです | 1 ノードに P-CSCF と agw が 1 台ずつです。ノードが直接インターネットに出るので、セキュリティグループと NSG の絞り込みが唯一の壁です。Pod Security の baseline に当たります。ノードの公開アドレスを agw に渡す仕組みが要ります。P-CSCF が始める要求は、ノードの conntrack に流れが残っているあいだだけ 5060 から出ます（EKS では、消えると送信元ポートが乱数になります） |
| 2. SIP はロードバランサー、RTP は案 1 | NLB か Azure LB の UDP と TCP の 5060 | 案 1 と同じ | クラウドの標準の公開の仕方を学べます。UDP では NLB がクライアントのアドレスを必ず保つので、P-CSCF は UE の公開アドレスを見られます | P-CSCF から UE への要求が LB のアドレスから出るのは、LB とノードの両方に流れが残っているあいだだけです（3 章、推定）。NLB の UDP の流れは 120 秒で消え、変えられません。LB の 1 時間の料金と公開アドレスの料金が加わります |
| 3. SIP も RTP もロードバランサー | 案 2 と同じ | LB に 40 ポート | ノードを公開しません | agw を 1 台に限る必要があります。NLB はリスナーが 50 までで上げられず、RTP のポートを増やせません。Azure LB も規則が増えます。案 2 の問題も残ります |
| 4. hostNetwork | | | NAT が最も少ない | ホスト固有のネットワーク設定に依存しない方針に反するので採りません |

結論は案 1 です。公式の文書を読み直して、案 2 が思っていたほど悪くないことが分かりました（応答は LB を通って戻り、UDP ではクライアントのアドレスも保たれます）。一方で、P-CSCF が始める要求の問題は、案 1 にも案 2 にも同じ形で残ることが分かりました。どちらも、UE からのキープアライブで流れを保つことが条件です。そのうえで、案 1 は間の機器が少なく、費用も安く、kind で試した形と同じなので、学習の既定に合います。EKS で案 1 を使うときは、Amazon VPC CNI の `AWS_VPC_K8S_CNI_RANDOMIZESNAT` を `none` にすると、流れが消えたあとも 5060 から出る見込みが高くなります（推定。乱数をやめたときに SNAT が元のポートを保つかは、文書では確かめていません）。案 2 は、ロードバランサーで何が変わるかを学ぶ比較として、試作で確かめてから手順書の付録にします。どちらも、edge のノードプール（公開アドレス付き、P-CSCF と agw だけを置く）と、core のノードプール（公開アドレスなし、ほかの役割）に分けます。ただし core のノードも、制御面への接続とイメージの取得のために外へ出る道が要ります。公開アドレスを付けて受ける向きをすべて閉じるか、NAT ゲートウェイかクラウドのプライベートな接続口を使うかを選びます。前の方法が安価です（7 章の金額では、公開アドレスは 1 時間 0.005 ドル、NAT ゲートウェイは 0.045 ドルとデータ量）。Azure では、2026-03-31 から新しい仮想ネットワークのサブネットが既定で私的になり、既定の外向きの通信（default outbound access）が使えなくなりました。AKS の管理する仮想ネットワークでは、AKS が明示的に設定した外向きの道を使うので影響しないとあります（「Customize cluster egress with outbound types」）。どの道が要るかは、イメージの置き場所（未決の論点 3）と、各社の制御面のつなぎ方で決まります。

## 9. 作ったものと、作っていないもの

### 作ったもの

- `deploy/cloud/`。AWS と Azure の資源の定義、両方に共通のモジュール、`tofu test` の試験、CI のジョブの案です（6 章）。`plan` と `apply` は利用者が手で行います。
- `deploy/k8s/` の、クラスタごとの値を 1 か所に書く仕組みと、クラウドの overlay（下）。

### 作っていないもの

- agw の access の側の公開アドレスの設定と、restricted latching（3 章）。今は P-CSCF の `host_access` で代用していて、SDP に公開アドレスを書けるのは利用者の回線からの UE だけです（下の「クラウドの overlay」）。agw に設定を足せば、この代用は要らなくなります。
- UE のキープアライブ（RFC 5626 4.4.2 の STUN）と、P-CSCF の STUN の応答（3 章）。最初は `-expires` を短くするだけでもよいと考えています。
- 後始末のスクリプト（7 章）。README に、消す順番と、タグで残りを探すコマンドを書きました。どのコマンドも、実際のアカウントでは動かしていません。
- CI のジョブ。案を `deploy/cloud/ci/cloud.yml.proposed` に置きました。手元で同じ手順を、取り直した道具と空のキャッシュで通しました。GitHub Actions のランナーから registry.opentofu.org と GitHub のリリースに届くかは、CI に置いたあとで分かります。
- 手順書「クラウドで動かす」。`deploy/cloud/README.md` と `README.ja.md` に下書きがあります。
- 実際のクラウドでの試験。インターネットの UE から登録と通話を確かめ、UE の側とノードの側で pcap を取ります。流れが消えたあとに P-CSCF が送る INVITE の送信元のアドレスとポートを、案 1 と案 2 で確かめる必要があります（3 章の推定）。案 2 の試作もここで行います。費用が掛かるので、利用者が自分のアカウントで行います。

### `deploy/k8s/` の側


`deploy/k8s/` に書きました。使い方は `deploy/k8s/README.ja.md` の「クラウドで動かす」にあります。クラウドの資源は作っておらず、クラウドの API も呼んでいません。

#### クラスタごとの値を 1 か所に書く

- 値は、クラスタごとのディレクトリ `deploy/k8s/environments/<名前>/` に、ConfigMap `ims-environment` として書きます。`kind`、`aws`、`azure` の 3 つです。annotation の `config.kubernetes.io/local-config` を付けたので、kustomize はビルドの出力からこの ConfigMap を外します。クラスタには届きません。
- 値を使う場所へは、Kustomize の Component が `replacements` で写します。`components/name-server` は DNS サーバーの Service（`dns` と、cluster-dns の `ims-dns`）の ClusterIP と、ゾーンの名前サーバーのアドレスに写します。`components/external-dns` は Pod の `dnsConfig.nameservers` に、`components/capture` はキャプチャの `POD_CIDR` に写します。
- Component にしたのは、Component の `replacements` が、それを使う kustomization の resources を見られるからです。kind の overlay とクラウドの overlay は、同じ Component に別の環境を渡します。クラウドの overlay が kind の overlay を resources に入れる重ね方は、採りませんでした。kind の値の ConfigMap が内側に残り、クラウドの値とぶつかるからです。そのため、DNS の構成 (b) とキャプチャのマニフェストを `components/` に移しました。`kubectl apply -k` に渡すディレクトリは、kind では前と同じです。
- ゾーンは ConfigMap の中の 1 つの文字列なので、kustomize はその中の 1 行を書き換えられません。ただし `replacements` には、区切りの文字列で値を分け、その 1 つの部分を置き換える機能があります（`options.delimiter` と `index`）。そこで、名前サーバーのレコードにしか無い ` IN A ` で分け、後ろの部分を置き換えます。後ろの部分がアドレスだけになるように、生成の道具（`changes.go`、`copies.go`）が名前サーバーのレコードをゾーンの最後に書くようにしました。kind のアドレスは、道具が `environments/kind` から読みます。値を変えたあとに `go generate ./deploy/k8s/...` を 1 回実行すれば、写しもそろいます。
- kind の写しで変わったのは、このレコードの位置と空白だけです。kind の 3 つの kustomization（`overlays/external-dns`、`overlays/cluster-dns`、`capture`）のビルドを前後で比べると、違いは 2 つだけでした。ゾーンの最後の改行が無くなったこと（レコードの中身は同じ）と、キャプチャのコマンドがフィルタの範囲を環境変数 `POD_CIDR` から読むことです。`e2e-kind.sh` は、DNS の宛先のアドレスを直書きせず、クラスタの Service から読むようにしました。
- `go test` で次のことを確かめます。`environments/kind` が `kind-cluster.yaml` と合うこと。kind の値をマニフェストに直書きしていないこと。ゾーンで置き換えを試し、名前サーバーのアドレスだけが変わること。`kubectl apply -k` に渡す各ディレクトリが環境を 1 つだけ並べ、Component が読む値をその環境が持つこと。

#### クラウドの overlay

- `overlays/cloud-aws` と `overlays/cloud-azure` です。違うのは環境だけで、どちらも base、ゾーン、DNS の構成 (b)、`components/edge` を使います。キャプチャは `overlays/cloud-*/capture` で、先に入れます。DNS を (b) にしたのは、5 章の結論のとおりです。
- `components/edge` が、P-CSCF と agw に nodeSelector と toleration（`ims-practice/pool=edge:NoSchedule`）を付けます。SIP の 5060（UDP と TCP）と RTP の 40 ポートは、hostPort でノードのポートにつなぎます。agw には、P-CSCF と同じノードに置く podAffinity を付けました。UE が SIP と RTP を送る先を、ノードの公開アドレス 1 つにするためです。LoadBalancer の Service は作りません。ほかの役割は toleration を持たないので、core のノードに載ります。
- hostNetwork ではなく hostPort にした理由は 3 つです。ホスト固有のネットワーク設定に依存しない方針のため、IMS のノードに hostNetwork は使いません（2 章）。[k8s.md](k8s.md) の 6 章で、kind の hostPort で P-CSCF の 5060 と agw の 40 ポートを試し、ホストの UE から発信できました。hostNetwork では、ノードに出したくないポート（agw の制御 API の 8080 など）までノードのアドレスで待つことになります。hostPort なら、書いたポートだけです。
- hostPort は 1 つのポートしか書けないので、RTP の 40 ポートを 1 つずつ並べました。4 章のとおり、範囲を狭く保つ前提です。範囲を変えるときは、`base/config/agw.yaml`（元は `deploy/compose/agw.yaml`）、`components/edge/agw.yaml`、`components/edge-capture`、`components/azure-load-balancer-rtp`、`deploy/cloud` の既定をそろえます。食い違うと `go test` が知らせます。
- StatefulSet のままにし、DaemonSet にはしませんでした。1 つのノードは同じ hostPort を 1 回しか使えないので、edge のノード 1 台に P-CSCF と agw は 1 台ずつです。DaemonSet にすれば edge のノードごとに 1 台になります。ただし、P-CSCF の名前と Origin-Host を Pod ごとに変える仕組み（[k8s.md](k8s.md)）と、ノードごとに違う公開アドレスを渡す仕組みが、まだありません。StatefulSet は Pod を作り直すとき、古い Pod を消してから新しい Pod を作るので、同じノードでポートがぶつかりません。台数を edge のノードの数より増やすと、余った Pod は Pending のままになります。
- 公開アドレスの入力。agw には、access の側の公開アドレスの設定がありません（上の「作っていないもの」）。今のバイナリが読む設定で使えるのは、P-CSCF の `host_access`（[ホストとコンテナの通信とキャプチャの設計](../design/environment-capture.md)）だけです。`gateway` から来た UE には、access の側のアドレスを `address` に替えて SDP に書きます。そこで、`gateway` に利用者の回線の公開アドレス（`allowed_source_cidrs` の /32）を、`address` に edge のノードの公開アドレスを入れました。環境の値では `ue_public_address` と `edge_public_address` です。P-CSCF の設定は ConfigMap の中のファイルなので、agw の `rtp_address` と同じく init コンテナが書き込みます（awk で `pcscf:` の行のあとに足します）。値が空なら、init コンテナがメッセージを出して止まります。この形で公開アドレスの中継ノードを見られるのは、利用者の回線から来る UE だけです。ほかの UE には Pod のアドレスが書かれ、音が届きません。agw に設定を足せば、この形は要らなくなります。
- 3 章の「ノードの公開アドレスを先に決められない」は残ります。AWS では、ノードが起動したあとで `edge_public_addresses_command` で調べ、環境に書いてから apply します。ノードが作り直されると、アドレスが変わります。
- キャプチャ。インターネットからのパケットは、edge のノードのインタフェースに、ノードのアドレス宛てで入ります。`-Q in` はそこで記録するので、Pod の範囲がノードの範囲と別のとき（Azure CNI Overlay）は、`net <pod_cidr>` に入りません。そこで `components/edge-capture` が、5060 と RTP のポートをフィルタに足し、edge の taint を許します。AWS では Pod の範囲が VPC の範囲なので、VPC の中のノード自身の通信もキャプチャに入ります。
- ロードバランサーは既定で作りません。比較の形として、Azure にだけ `components/azure-load-balancer-sip` と `-rtp` を置き、overlay ではコメントにしました。2 章で確かめた annotation（`azure-pip-name` と `azure-load-balancer-resource-group`）、`externalTrafficPolicy: Local`、`allocateLoadBalancerNodePorts: false`（floating IP が有効で `Local` のときに使えます）、`loadBalancerSourceRanges` を書いています。AWS では `deploy/cloud/aws` が NLB を作り、edge のノードの hostPort へ送るので、Kubernetes の側に足すものはありません。
- イメージは `base/` の名前のままです。置き場所は未決の論点 3 です。決まれば、overlay の `images` で指定できます。

#### tofu output から利用者が書く値

| 環境の値 | 取り出し方 | 使う所 |
| --- | --- | --- |
| `ims_dns_cluster_ip` | 出力 `kubernetes` の `ims_dns_cluster_ip` | DNS サーバーの Service、Pod の resolv.conf、ゾーンの名前サーバー |
| `pod_cidr` | 出力 `kubernetes` の `pod_cidr` | キャプチャのフィルタ |
| `ue_public_address` | 変数 `allowed_source_cidrs` の /32 のアドレス（出力ではありません） | P-CSCF の `host_access.gateway` |
| `edge_public_address` | AWS は出力 `edge_public_addresses_command` のコマンドの結果。Azure は同じ名前の出力のコマンド（`kubectl get nodes`）の EXTERNAL-IP。`sip-and-rtp` では RTP を受けるロードバランサーのアドレス | P-CSCF の `host_access.address`。ロードバランサーを使わないときは、UE の `-pcscf` にも使います |
| `load_balancer_*`（Azure だけ） | 出力 `load_balancer_public_ips` | ロードバランサーの Service の annotation と送信元の範囲 |

出力 `kubernetes` のうち、`service_cidr`、`cluster_dns_ip`、`edge_node_label`、`edge_node_taint`、`rtp_ports`、`address_ranges` は、overlay では使いません。ラベル、taint、RTP の範囲は `deploy/k8s` にも書いてあり、`go test` が `deploy/cloud/modules/ims-network` の値と同じかを確かめます。

#### 確かめたこと

- `kubectl kustomize` で、`kubectl apply -k` に渡す 7 つのディレクトリ（kind の 3 つとクラウドの 4 つ）と、Azure のロードバランサーの Component を入れた形をビルドしました。kubectl は v1.37.2（kustomize v5.8.1）で、dl.k8s.io から取って SHA-256 で確かめました。
- kubeconform v0.7.0 の `-strict` で、ビルドした全部のマニフェストが Kubernetes v1.37.0 のスキーマに通りました。kubeconform は GitHub のリリースから取り、リリースの CHECKSUMS で確かめました。スキーマは、実行のときに yannh/kubernetes-json-schema から取りました。
- `kubectl apply --dry-run=client` は、API サーバーに接続しないと動かないので、できませんでした。
- P-CSCF の init コンテナの awk を alpine:3.24（busybox）で動かしました。できた設定ファイルを `ims` のバイナリが読み、`host_access` 付きで起動しました。
- edge のキャプチャのフィルタ（`net 10.244.0.0/16 or port 5060 or udp portrange 40000-40039`）を、libpcap（dumpcap）で翻訳できることを確かめました。
- kind で `deploy/k8s/e2e-kind.sh external-dns` を実行しました。結果は下の「kind で確かめたこと」にあります。

#### kind で確かめたこと

クラスタごとの値の仕組みを入れても kind の振る舞いが変わっていないことを、[k8s.md](k8s.md) の 9 章と同じ条件で確かめました。kind は v0.33.0（GitHub のリリース、SHA-256 で確かめました）、ノードのイメージは kindest/node v1.37.0 です。`KIND_E2E_EXTRA_CONFIG` に cgroup v1 のための 2 つのパッチを渡しました。IMS のイメージは、ホストで作ったバイナリを `alpine:3.24` に入れたものです。キャプチャのイメージは、プロキシの CA をビルドのあいだだけ入れて作りました。どちらの作り方もリポジトリには入れていません。

| 実行したこと | 結果 |
| --- | --- |
| 仕組みを入れたあとの `e2e-kind.sh external-dns`（キャプチャの確認は直す前） | UE の 10 の確認は通りました。キャプチャの確認は、ACK が UE 2 に届く順番から後の 5 つが失敗しました。MMTel AS が B2BUA として呼の経路に入り、UE 2 への ACK が UE 1 への 200 より先に出るためで、元の `e2e-kind.sh` でも同じになります。`scripts/e2e-call.sh` はすでにこの順番に合わせてありました。DNS（10.96.0.53 への NAPTR の問い合わせを含む）の確認は通りました |
| 確認を `e2e-call.sh` に合わせたあとの `e2e-kind.sh external-dns` | 成功。UE の 10 の確認、キャプチャの 58 の確認、malformed なしがすべて通りました |
| 同じく `e2e-kind.sh cluster-dns` | 成功。確認は上と同じですべて通りました |

どちらの実行も、終わったあとにクラスタ、ノードのイメージ、ネットワーク `kind` が消えました。クラウドの overlay は kind では動かしていません。kind のノードには edge のラベルと taint が無いからです。


## 10. 未決の論点

選択肢と、その得失です。

1. 公開の仕方。案 1（edge のノードの公開アドレスと hostPort）を既定にし、案 2 と案 3 は変数 `load_balancer` で切り替えられるようにしてあります。案 2 を実際に試して手順書の付録にするかは未決です。
2. 対象のクラウド。`deploy/cloud/` には AWS と Azure の両方を書きました。実際に置くのを両方にするか、片方から始めるかは未決です。リージョンには既定値を置かず、必ず決める形にしました。ノードの大きさの既定は、AWS で `t3.medium`、Azure で `Standard_D2s_v5` を、edge と core に 1 台ずつにしています。この既定が適切かは確かめていません。
3. コンテナイメージの置き場所。ECR や ACR に push するか、公開のレジストリ（GHCR など）から取るか。リポジトリを公開する前にイメージを非公開のまま置くなら、ノードにレジストリの資格情報が要ります。
4. キープアライブ。短い `-expires` の再登録で済ませるか、RFC 5626 の STUN のキープアライブを作るか。3 章で、どの置き方でもキープアライブが着信の条件になると分かったので、STUN のキープアライブを作るほうが適切です。
5. インターネットの UE は SIP Digest だけにし、IMS AKA と IPsec はクラウドの構成の範囲外にするか。
6. restricted latching（RFC 7362 5）を、クラウドで使うときの既定にするか。Compose と kind の既定は今のまま（どこからでもラッチ）にします。
7. 費用の上限。実験の 1 回の長さと、資源を消すまでの時間の目安を手順書に書くか。各社の予算の警告の設定を IaC に含めるか。
8. agw に公開アドレスの設定ができるまで、クラウドの overlay で P-CSCF の `host_access` を使い、利用者の回線からの UE 1 台にだけ中継ノードの公開アドレスを見せる形でよいか（9 章の「クラウドの overlay」）。
9. 利用者が `deploy/k8s/environments/aws` などに書く値（自分の回線とノードの公開アドレス）を、どう Git から外すか。今は README で「コミットしない」と書いただけです。Git の対象外のファイルにすると、そのファイルが無いあいだ overlay をビルドできず、CI で検査できなくなります。

## 確かめられなかったこと

### 確かめられたこと

公式のページで確かめられたものです。出典は表「公式のページで確かめたもの」にあります。

- GitHub の原稿で読んだことは、公式のページと食い違いませんでした。ただし、Controller の文書の「IP ターゲットではクライアントの IP を保たないのが既定」は TCP の話で、UDP と TCP_UDP では常に保ちます（2 章）。
- AWS の NLB の利用者ガイドを読みました。UDP の流れの idle timeout は 120 秒で変えられないこと、リスナーは 1 つの NLB に 50 までで上げられないこと、TCP_UDP のリスナーには TCP_UDP のターゲットグループが要ること、UDP のターゲットは TCP、HTTP、HTTPS で調べることを確かめました。
- ターゲットが始めた UDP の道について、文書で分かることを分けて書きました（2 章の「Pod が始める UDP の道」）。NLB の文書は、ターゲットが NLB を通して流れを始めることを説明していません。Pod の新しい外向きの流れは、ノードで SNAT され、既定で送信元ポートが乱数になります。
- AKS の部品（cloud-provider-azure）は floating IP を既定で有効にし、annotation `service.beta.kubernetes.io/azure-disable-load-balancer-floating-ip` で無効にできます。
- AWS のインターネットゲートウェイは 1 対 1 の NAT です。Azure の VM の公開 IP は、SNAT を使わない 1 対 1 の NAT です。
- Amazon VPC CNI の既定の設定は `portmap` をつなぐので、EKS で hostPort が使えるはずです（CNI のリポジトリの設定ファイルで確かめました）。
- EKS の既定の Service の範囲（10.100.0.0/16 か 172.20.0.0/16）と、AKS の範囲（Azure CNI Overlay の文書で 10.0.0.0/16）を確かめました。EKS のアドオンは Corefile を直接変えると更新で上書きし、AKS は Corefile を変えられず `coredns-custom` を使います。
- Route 53 は NAPTR と SRV を扱えます。
- Kubernetes の Service のポートに範囲の書き方が無いこと、混在プロトコルが v1.26 から stable で、ゲートが取り除かれたことを、v1.37 の版の文書で確かめました。
- hashicorp/aws と hashicorp/azurerm のプロバイダーのライセンスは MPL 2.0 です。registry.opentofu.org に届きました。
- AWS の料金のいくつか（EKS の制御面、NLB、NAT ゲートウェイ、公開 IPv4）を、ページにあるリージョンと合わせて 7 章に書きました。
- 後始末の CLI のコマンドのうち、EKS と AKS のクラスタを消すものを文書で確かめました（7 章）。


- OpenTofu 1.13.0 と Terraform 1.17.2 で、資格情報なしに `fmt -check`、`init -backend=false`、`validate`、`test` が動くことを、試作で確かめました（6 章）。OpenTofu 1.13.0 では `mock_provider` が使えます。
- registry.opentofu.org から、`tofu init` でプロバイダーを取れました。
- 起動テンプレートの TagSpecification で、ノードグループのインスタンスにタグを付けられることを、EKS の文書で確かめました。後始末でインスタンスとディスクをタグで探せます。
- 残りを探すコマンドのうち、`aws resourcegroupstaggingapi get-resources`、`aws ec2 describe-addresses`、`az resource list --tag` を、CLI のリファレンスで確かめました（README に書いたもの）。動かしてはいません。

### まだ確かめられないこと

- 試作は、プロバイダーを mock に置き換えた計画までです。本物の `plan` と `apply` は 1 度も動かしていません。インスタンスの種類や VM の大きさがそのリージョンで使えるか、クォータ、ロールの権限、AKS の API が受け付ける値（`node_provisioning_profile` の `mode = "Manual"` など）は、クラウドの API に問い合わせて初めて分かります。
- VPC CNI のアドオンの `configuration_values` に `env` の形で `AWS_VPC_K8S_CNI_RANDOMIZESNAT` を書けることは、アドオンの設定の schema を見ていないので確かめていません。schema を見るには `aws eks describe-addon-configuration`（API の呼び出し）が要ります。
- AKS で、自分で作った仮想ネットワークのサブネットにノードを置くとき、システム割り当ての ID に Network Contributor が要るかは確かめていません。固定の公開 IP のために資源グループの権限が要ることは確かめたので、資源グループに 1 つ割り当てて両方に足りる形にしました。割り当てを作るには、`apply` する人に Owner か User Access Administrator の権限が要ります（推定）。
- azurerm 5.11.0 の `azurerm_kubernetes_cluster` の文書は読んでいません。`node_provisioning_profile` が必須になった理由と、`mode` の値の意味は、schema と AKS の API の名前からの推定です。
- EKS のマネージドノードグループのノードの Node に ExternalIP が入るかは確かめていません。そのため、edge のノードの公開アドレスは、AWS では EC2 のインスタンスの一覧（起動テンプレートで付けたタグ `Name`）で探す出力にしました。
- プロバイダーの版の公開日は、api.opentofu.org に届かず確かめていません。最新（aws 6.73.0、azurerm 5.14.0）ではなく、少し前の aws 6.70.0 と azurerm 5.11.0 を選びました。
- OpenTofu の導入スクリプトが行う署名の確認（cosign か GnuPG）はしていません。SHA256SUMS のハッシュだけで確かめました。SHA256SUMS そのものは同じリリースから取ったものです。
- GitHub Actions のランナーから registry.opentofu.org と GitHub のリリースに届くかは、CI のジョブを写したあとで分かります。
- Terraform で `deploy/cloud/` の試験を通すには、mock に `override_during = plan` を書いた別の試験が要ります。作っていません。

- Azure の料金のページと、AWS の EC2 のオンデマンドの料金表は、金額を JavaScript で入れていて、curl で取ったページに金額がありませんでした。Azure の金額と、ノードの金額は確かめていません。Route 53 と Azure DNS の料金のページは読んでいません。
- www.hashicorp.com は 2 回とも 429 でした。Terraform のライセンスは、hashicorp/terraform の LICENSE で確かめたものです。
- P-CSCF が始めた UDP が、流れが残っているあいだ NLB や Azure LB のアドレスから出ること、流れが消えたあとはノードの SNAT のアドレスと乱数のポートから出ること（3 章の表）は、文書から組み立てた推定です。実際のクラウドでの試験で確かめる必要があります。
- `AWS_VPC_K8S_CNI_RANDOMIZESNAT` を `none` にしたとき、SNAT が Pod の送信元ポート 5060 を保つかは確かめていません。AKS のノード（Azure CNI）が Pod の外向きの通信をどう SNAT するかも確かめていません。
- Azure LB の UDP の負荷分散の規則の流れの寿命は、文書に書かれていませんでした。ノードの conntrack の UDP の寿命は、kube-proxy に設定があることだけ確かめ、既定の値は確かめていません。
- Azure CNI で hostPort が使えるか、Node の ExternalIP に公開アドレスが入るか、EKS のマネージドノードグループのノードに Elastic IP を付けられるかは確かめていません。AWS のインターネットゲートウェイが ESP を通すかも確かめていません。
- EKS と AKS が Pod Security Admission を既定で強制するかは確かめていません（EKS のページの 1 つは中身が返りませんでした）。
- AKS のクラスタを止めたとき、ロードバランサー、公開 IP、ディスクの費用が止まるかは書かれていませんでした。EKS にクラスタを止める機能があるかは確かめていません。
- TS 24.229 と TS 33.203 の NAT 越え（UDP のカプセル化、NAT 越えの付録）と、P-CSCF の見つけ方（PCO、DHCP）の条項は読んでいません。
- eksctl の `--dry-run` が資格情報なしで動くか、Bicep と Pulumi の CI での検査の方法、Pulumi の state の既定の置き場所は確かめていません。OpenTofu の `mock_provider` がどの版から使えるかは、読んだページに書かれていませんでした。定義の `required_version` は、試した 1.13.0 以上にしています。
- 残りを探すための一覧の CLI のコマンドのうち、上の 3 つ以外（ロードバランサー、ディスクなどを種類ごとに一覧にするもの）は確かめていません。後始末のスクリプトは作っていません。
- クラウドの資源は作っておらず、どの案も動かしていません。クラウドの API も呼んでいません。動かして確かめたのは、kind での hostPort の結果（[k8s.md](k8s.md) の 6 章）と、手元での資源の定義の検査と、手元でのマニフェストの検査と kind での e2e だけです。
- 推定です。EKS（Amazon VPC CNI の portmap）と AKS（Azure CNI Overlay）の hostPort が、UE の送信元のアドレスを変えずに Pod へ渡すかは確かめていません。変わると、P-CSCF の `host_access` の `gateway` が合わず、SDP に公開アドレスが書かれません。kind では、ホストの UE の送信元がそのまま見えました（[k8s.md](k8s.md) の 6 章）。
- `components/edge` と `components/azure-load-balancer-*` は、kubeconform でスキーマに合うことだけを確かめました。実際の EKS と AKS の API サーバーが受け付けるか（`kubectl apply --dry-run=server`）と、AKS がロードバランサーの規則を作るかは確かめていません。
