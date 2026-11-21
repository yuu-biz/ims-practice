# IMS を置くクラウドの資源

[English](README.md)

> **この定義を適用すると費用がかかります。適用するかどうかは、あなたが決めることです。**
> このリポジトリの中に、自動で適用するものはありません。CI は、クラウドのアカウントを使わずに定義を検査するだけです。
> 適用すると、資源を消すまでのあいだ、使っていない時間も含めて 1 時間ごとに課金されます。
> 始める前に「使っていなくてもかかる費用」と「後始末」を読んでください。

このディレクトリは、IMS をクラウドの Kubernetes に置くための資源を、[OpenTofu](https://opentofu.org/) で定義したものです。対象は AWS の Amazon EKS と、Azure の Azure Kubernetes Service（AKS）です。定義のコメントに、従った AWS、Azure、OpenTofu の文書のページの名前を書いています。

`tofu apply` でできるのは、クラスタとネットワークまでです。IMS は `deploy/k8s` の `overlays/cloud-aws` か `overlays/cloud-azure` で、その上に置きます（[deploy/k8s/README.ja.md](../k8s/README.ja.md) の「クラウドで動かす」）。どちらも、実際のクラウドでは動かしていません。確かめているのは、アカウントを使わない検査だけです（「アカウントを使わない検査」）。

## 作るもの

どちらのクラウドも同じ形です。

- 公開サブネットだけのネットワーク。私的なサブネットを作らないので、NAT ゲートウェイの費用がかかりません。
- ノードプールを 2 つ持つ Kubernetes のクラスタ。
  - **edge**。公開アドレスを持つノードで、P-CSCF とメディア中継ノード（agw）を置きます。kind と同じく、`hostPort` でノードのポートから SIP と RTP を受けます。ノードにはラベルと taint の `ims-practice/pool=edge` を付けるので、これを許す Pod だけが載ります。
  - **core**。ほかの役割を置くノードです。インターネットからは届きません。
- edge のノードのファイアウォールの規則。開けるのは SIP（UDP と TCP の 5060）と RTP の範囲（既定は UDP の 40000〜40039）だけで、送信元は `allowed_source_cidrs` に書いた範囲だけです。Kubernetes の API も同じ範囲に絞ります。
- 必要なら、SIP のためのロードバランサー（`load_balancer = "sip"`）、または SIP と RTP のためのロードバランサー（`"sip-and-rtp"`）。既定では作りません。次の制限があります。
  - AWS の Network Load Balancer（NLB）は、パケットが 120 秒流れなかった UDP の流れを忘れます。この時間は変えられません。UE がそれより短い間隔で送らないと、UE への着信が届かなくなります。
  - 1 つの NLB のリスナーは 50 までです。SIP で 1 つ、RTP のポートごとに 1 つ使うので、RTP の範囲が 49 ポートを超えると計画の段階で止まります。
  - Azure では、ロードバランサーの規則は Kubernetes の Service が作ります。その Service は `deploy/k8s/components/azure-load-balancer-sip` と `-rtp` にあります。この定義が作るのは、Service に使う固定の公開アドレスだけです。1 つのネットワークインタフェースの規則は 300 までです。

| | AWS（`aws/`） | Azure（`azure/`） |
| --- | --- | --- |
| ネットワーク | VPC、2 つのゾーンの公開サブネット 2 つ、インターネットゲートウェイ | 資源グループ、仮想ネットワーク、core と edge のサブネット |
| クラスタ | EKS、VPC CNI のアドオン | AKS（Free の tier）、Azure CNI Overlay |
| edge のファイアウォール | edge のノードのセキュリティグループ | edge のサブネットのネットワークセキュリティグループと、ノードプールの allowed host ports |
| edge のアドレス | ノードが起動したときに割り当てられる | 公開 IP prefix から取る。ノードの起動より前に範囲が分かる |
| 選べるロードバランサー | NLB と Elastic IP 1 つ。edge のノードへ送る | LoadBalancer の Service に使う固定の公開 IP |

すべての資源にタグ `ims-practice=cloud` を付けます。消し残しをタグで探せるようにするためです。

## 平文の SIP と RTP をインターネットに出すこと

この IMS は、キャプチャで中身を読めるように、SIP と RTP を暗号化せずに送ります。インターネットに出すと、次のことが起きます。

- 経路の途中のどこからでも、信号を読み、通話を聞けます。
- 試験用の加入者のパスワードは、このリポジトリに書いてあります。5060 に届けば、誰でもその加入者として登録し、発信できます。
- 5060 は常に走査されています。送信元を絞らないと、知らない REGISTER と OPTIONS がキャプチャに並びます。
- メディア中継ノードは、ストリームごとに最初に送ってきた相手に固定します。開いている RTP のポートに先に送った者に、相手の音声が流れます。

そこで、次のようにします。

1. `allowed_source_cidrs` には既定値がありません。自分の回線のアドレスを書きます（例 `["192.0.2.10/32"]`）。`0.0.0.0/0` と、`/8` より広い範囲は拒みます。
2. IMS を公開する前に、加入者のパスワードを変えます。新しいパスワードはリポジトリに書きません。
3. 実験は短くし、終わったらすぐに全部を消します。

## 使っていなくてもかかる費用

2026 年 11 月に、各社の料金のページで確かめた値です。料金は変わるので、適用する前にページを読み直してください。

| | AWS | Azure |
| --- | --- | --- |
| Kubernetes の制御面 | EKS。クラスタ 1 つに 1 時間 0.10 ドル（標準サポートの版） | AKS の Free の tier。クラスタの管理は無料 |
| ノード（既定で 2 台） | EC2 のインスタンスと EBS のボリューム。「Amazon EC2 On-Demand Pricing」を見てください | VM とマネージドディスク。「Linux Virtual Machines Pricing」を見てください |
| 公開アドレス | 公開 IPv4 アドレス 1 つに 1 時間 0.005 ドル。付いていてもいなくても同じ（ノード 2 台で 1 時間 0.01 ドル） | 「IP Addresses pricing」を見てください（edge の prefix と、AKS のロードバランサーの外向きのアドレス） |
| ロードバランサー | `load_balancer` を使うときだけ。NLB は 1 時間 0.0225 ドルと NLCU の分、それに Elastic IP がもう 1 つ | AKS は外向きの通信のために常に Standard Load Balancer を持ちます。「Load Balancer pricing」を見てください |
| NAT ゲートウェイ | 作りません | 作りません |

AWS で分かっている値を足すと、インスタンスとディスクを除いて 1 時間 0.11 ドルほどです。Azure の金額は確かめられていません。「Amazon EKS pricing」、「Amazon VPC pricing」、「Elastic Load Balancing pricing」、「Azure Kubernetes Service (AKS) pricing」、「Load Balancer pricing」、「IP Addresses pricing」で調べてください。どちらのクラウドにも予算の警告の仕組みがあります。始める前に設定してください。

## アカウントを使わない検査

資格情報は要らず、何も作りません。`.github/workflows/ci.yml` の CI のジョブ `cloud` が行うのと同じものです。

```sh
for dir in deploy/cloud/modules/ims-network deploy/cloud/aws deploy/cloud/azure; do
  tofu -chdir=$dir fmt -check -recursive
  tofu -chdir=$dir init -backend=false -input=false -lockfile=readonly
  tofu -chdir=$dir validate
  tofu -chdir=$dir test
done
```

`init` は、プロバイダー（AWS は約 180 MB、Azure は約 70 MB）を registry.opentofu.org から取ります。`test` は、プロバイダーを作り物（`mock_provider`）に置き換えて計画を作るので、クラウドには届きません。試験で確かめるのは、開けるのが SIP と RTP の範囲だけで、送信元が許した範囲だけであること、ロードバランサーの切り替えが上の説明どおりに働くこと、誤った設定を拒むことです。

OpenTofu 1.13.0 は、[GitHub のリリース](https://github.com/opentofu/opentofu/releases/tag/v1.13.0)から取り、同じリリースの `tofu_1.13.0_SHA256SUMS` と照らして確かめます。手順は [OpenTofu の文書](https://opentofu.org/docs/intro/install/standalone/)にあります。

Terraform でも、`fmt`、`init`、`validate` は通ります。試験は通りません。Terraform は、mock に `override_during = plan` と書かない限り、計画の段階で mock の値を「まだ決まらない」のままにします。OpenTofu はこの書き方を受け付けません。また、Terraform は `.terraform.lock.hcl` を自分の registry の内容に書き換えます。

## 適用する（手で行い、費用はあなたが払う）

OpenTofu、kubectl、クラウドのコマンド（AWS CLI か Azure CLI）が要ります。クラウドのコマンドは、**あなた自身の**アカウントにサインインしておきます。定義は、そのコマンドの環境から資格情報を取ります。定義の中に、アカウントやサブスクリプションの名前はありません。Azure では `ARM_SUBSCRIPTION_ID` を設定し、ロールを割り当てる権限も要ります（クラスタの ID に、資源グループの Network Contributor を割り当てるため）。

1. クラウドのディレクトリに移り、設定を作ります。

   ```sh
   cd deploy/cloud/aws          # Azure なら deploy/cloud/azure
   cp terraform.tfvars.example terraform.tfvars
   # terraform.tfvars の region、allowed_source_cidrs、load_balancer を直す
   ```

   `terraform.tfvars` には、自分の回線のアドレスと、選んだリージョンが入ります。そのため Git の対象から外してあります（`deploy/cloud/.gitignore`）。`*.auto.tfvars` も同じです。どちらの名前も、OpenTofu は指定しなくても読み込みます。例のファイルのアドレスは文書用の範囲（RFC 5737）で、どこにも届きません。アカウントとサブスクリプションも、これらのファイルに書かないでください。クラウドのコマンドと `ARM_SUBSCRIPTION_ID` から取ります。

2. プロバイダーを取り、計画を作ります。計画を読んでください。作る資源がすべて並びます。

   ```sh
   tofu init
   tofu plan -out=ims.tfplan
   ```

3. 計画と費用に納得したときだけ、適用します。

   ```sh
   tofu apply ims.tfplan
   ```

   クラスタができるまで数分かかります。

4. 出力を読み、kubectl をクラスタに向けます。

   ```sh
   tofu output
   tofu output -raw kubeconfig_command    # 表示されたコマンドを実行する
   kubectl get nodes -o wide
   ```

   UE が SIP を送る先は、`edge_public_addresses_command`（AWS）か `edge_public_ip_prefix`（Azure）で分かります。ロードバランサーを使うときは、`load_balancer_address`（AWS）か `load_balancer_public_ips`（Azure）です。出力 `kubernetes` には、`deploy/k8s` のクラウドの overlay が使う値があります。IMS の DNS のアドレスと Pod の範囲は、`deploy/k8s/environments/aws` か `environments/azure` の `values.local.env` に書きます。edge のラベルと taint は、`deploy/k8s/components/edge` が使うものと同じです。

5. 値を `deploy/k8s/environments/aws/values.local.env`（か `azure`）に書きます。このファイルは、隣の `values.local.env.example` から作ります。これも Git の対象から外してあります。そのあと `kubectl apply -k deploy/k8s/overlays/cloud-aws/capture`、続けて `kubectl apply -k deploy/k8s/overlays/cloud-aws`（か `cloud-azure`）で IMS を入れます。手順は [deploy/k8s/README.ja.md](../k8s/README.ja.md) の「クラウドで動かす」にあります。

state（`terraform.tfstate`）はこのディレクトリに残り、Git の対象からは外してあります。全部を消し終えるまで残してください。state が無いと、OpenTofu は自分が何を作ったかを知りません。

## 後始末

実験が終わったら、すぐに全部を消します。

1. LoadBalancer の Service を作ったなら、先に消します。AKS では、その Service が、OpenTofu の知らないロードバランサーの規則を持っています。

   ```sh
   kubectl get svc --all-namespaces      # EXTERNAL-IP のあるものを探す
   kubectl delete svc <名前> -n <namespace>
   ```

2. 同じディレクトリで、OpenTofu が作ったものを消します。確認を求められたら答えます。

   ```sh
   tofu destroy
   tofu state list                       # 何も表示されなければよい
   ```

3. このプロジェクトのタグが付いた資源が残っていないかを探します。

   ```sh
   # AWS（前にタグが付いていた資源も並びます）
   aws resourcegroupstaggingapi get-resources --region <リージョン> --tag-filters Key=ims-practice,Values=cloud
   aws ec2 describe-addresses --region <リージョン>    # Elastic IP は付いていなくても課金されます

   # Azure
   az resource list --tag ims-practice=cloud
   ```

   Azure では、クラスタを消すと、AKS が作ったノードの資源グループ（`MC_` で始まるもの）も消えます。

4. 次の日に、クラウドの請求の画面で、課金が止まったことを確かめます。

## ファイル

| 場所 | 中身 |
| --- | --- |
| `modules/ims-network/` | どのクラウドでも同じ部分。開けるポート、設定の検査、DNS のアドレス、ノードのラベル、タグ。資源は作りません |
| `aws/`、`azure/` | クラウドごとのルートのモジュール。変数の名前は同じです。`tests/` に mock を使う計画の試験、`.terraform.lock.hcl` にプロバイダーの版とハッシュがあります |
