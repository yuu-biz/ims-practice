# 手順書

[English](../en/README.md)

この手順書では、手元のマシンで IMS を動かし、そのメッセージをキャプチャで 1 つの手順ずつ追います。最初にこのページを読んでください。手順書を読む順、最初に打つコマンド、片付けの方法、Docker Compose のプロジェクトが起動するものをまとめています。

## 読む順

1. [信号をキャプチャして Wireshark で開く](capture.md)。システムを起動してキャプチャを開き、Diameter の接続ができるところを見ます。
2. [UE が IMS に登録する流れを追う](registration.md)。P-CSCF、I-CSCF、S-CSCF を通る REGISTER と、SIP Digest、Cx。
3. [音声通話の流れを追う](call.md)。電話番号で別の UE にかける INVITE と、ENUM、メディア中継。
4. [SMS の流れを追う](sms.md)。IP-SM-GW を通る SMS over IP と、その報告。
5. [MMTel AS の補足サービス](mmtel.md)。番号通知、転送、規制と、Ut での設定。
6. [MRF による音声ガイダンス、呼び出し音、会議](mrf.md)。規制した通話の前の音声ガイダンス、CAT、割込通話、3 人の会議。
7. [VoLTE の呼の流れ](volte.md)。EPC スタブ、IMS-AKA、Rx でのポリシー制御と、プリコンディションを使った AMR-WB の通話。
8. [IMS 網どうしを相互接続する](interconnect.md)。境界の IBCF と TrGW、構成の秘匿、相互接続プロファイル。
9. [ダッシュボードで IMS を見る](dashboard.md)。HEP の写しから描くラダー図、加入者の管理、ブラウザから操作する UE シミュレータ。

略語は[用語集](../../glossary.md)で説明しています。

### 最初の手順書の UE はアクセス網を持たない

2 から 6 の手順書の UE は、固定のアクセスやパソコンで使うようなソフトフォンです。SIM カードも無線のアクセスも持ちません。Docker のネットワークを通って P-CSCF に届き、SIP Digest で登録し、G.711 で通話します。そのため最初の手順書では、EPC、IMS-AKA、音声ベアラを待つことを抜きにして、IMS だけを見られます。

携帯電話は、それぞれの手順の前と途中で、もっと多くのことをします。EPC にアタッチしてアドレスと P-CSCF を受け取り、USIM の鍵で IMS-AKA の登録をし、着信側が鳴る前に音声ベアラができるのを待ちます。[VoLTE の呼の流れ](volte.md)は、最初の手順書で見たことに、これらの手順を加えます。

## 最初のコマンド

リポジトリのルートで実行します。最初のビルドには数分かかります。

```sh
docker compose up -d --build
docker compose run --rm ue register --deregister-after 5s
```

1 つ目のコマンドで、ノードとキャプチャが起動します。2 つ目を打つ前に、I-CSCF と S-CSCF が HSS への Diameter の接続を開くまで待ちます。HSS が接続を受け付ける前に起動したノードは、30 秒後（Tc、RFC 6733 2.1）にもう一度つなぎに行くので、長くて 1 分ほどかかります。接続ができると、`docker compose logs scscf | grep I-Open` が 1 行を表示します。2 つ目のコマンドは、加入者 1 を登録し、5 秒後に登録を解除します。そのあと、`captures/` のいちばん新しいファイルを Wireshark で開きます。開き方は[信号をキャプチャして Wireshark で開く](capture.md)にあります。

## 片付け

どの手順書も、最後に、起動したものをすべて止めます。

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

このコマンドはイメージを残すので、次の手順書をビルドし直さずに始められます。手順書をすべて終えたら、イメージも削除します。

```sh
docker compose --profile ue --profile sim --profile volte down --rmi all -v --remove-orphans
```

キャプチャのファイルは、削除するまで `captures/` に残ります。

## Compose のプロジェクトが起動するもの

`docker compose up` は次のサービスを起動します。IMS のノードはすべて同じイメージで動き、`deploy/compose/` にある各ノードの設定ファイルが役割を決めます。

| サービス | アドレス | 何か |
| --- | --- | --- |
| `dns` | 172.28.0.2 | ホーム網のドメインの DNS。NAPTR、SRV、A のレコードと ENUM。 |
| `hss` | 172.28.0.10 | HSS のスタブ。CSCF には Cx、アプリケーションサーバーには Sh で応えます。 |
| `icscf` | 172.28.0.20 | I-CSCF。 |
| `scscf` | 172.28.0.30 | S-CSCF。 |
| `pcscf` | 172.28.0.40 | P-CSCF。 |
| `agw` | 172.28.0.50 | IMS-AGW の役割のメディア中継。P-CSCF が制御します。 |
| `dashboard` | 172.28.0.60 | http://localhost:8080 のダッシュボードと、HEP の収集先。 |
| `ipsmgw` | 172.28.0.70 | IP-SM-GW。SMS over IP のアプリケーションサーバーです。 |
| `mmtel` | 172.28.0.75 | MMTel AS。補足サービスのアプリケーションサーバーです。 |
| `epc` | 172.28.0.80 | PCRF を含む EPC のスタブ。VoLTE の UE が使います。 |
| `mrfc` | 172.28.0.85 | MRFC。MRF の制御の側です。 |
| `mrfp` | 172.28.0.86 | MRFP。メディアを流し、混ぜます。 |
| `capture` | ホストのネットワーク | tcpdump。キャプチャのファイルを `captures/` に書きます。 |

### プロファイル

UE はプロファイルに入れてあるので、`docker compose up` では起動しません。

| プロファイル | サービス | 動かし方 |
| --- | --- | --- |
| `ue` | `ue`、`ue2`、`ue3`、`peer` | `docker compose run --rm ue register` のように、1 回のコマンドごとに動かします。`peer` には、ほかの人が作ったユーザーエージェントの pjsua と SIPp が入っています。 |
| `sim` | `ue-a`、`ue-b`、`ue-c` | 起動したままになり、ダッシュボードの指示で動きます。`docker compose --profile sim up -d` で起動します。 |
| `volte` | `volte-a`、`volte-b` | VoLTE の UE です。`sim` と同じく起動したままになります。`docker compose --profile volte up -d` で起動します。 |

`ue` と `sim` は同じ加入者を使うので、一度にどちらか一方だけを動かします。

### 加入者、UE、サービスの対応

試験用の加入者は `deploy/compose/hss.yaml` にあります。手順書では、UE 1 や UE-A のように、加入者の番号やシナリオでの役で UE を呼びます。その名前とサービスの対応は次の表のとおりです。

| 加入者 | IMPI（private identity） | 番号 | サービス | 手順書での呼び名 |
| --- | --- | --- | --- | --- |
| 1 | `001010000000001@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0101 | `ue`、`ue-a` | UE 1、UE-A、網間接続の手順書では UE A1 |
| 2 | `001010000000002@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0102 | `ue2`、`ue-b` | UE 2、UE-B |
| 3 | `001010000000003@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0103 | `ue3`、`ue-c` | UE 3、UE-C |
| 4 | `001010000000004@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0104 | `peer`（pjsua） | pjsua |
| 10 | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0110 | `volte-a` | volte-a |
| 11 | `001010000000011@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0111 | `peer`（SIPp） | SIPp |
| 12 | `001010000000012@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0112 | `volte-b` | volte-b |

網間接続の手順書の 2 つ目の網には、`deploy/interconnect/peer/hss.yaml` に別の加入者がいます。サービス `peer-ue` の UE B1 が +1-202-555-0151 で、もう 1 人が +1-202-555-0152 です。

## ほかの動かし方

| ディレクトリ | 何をするか |
| --- | --- |
| [deploy/chaos](../../../deploy/chaos/README.ja.md) | わざと障害を起こします。ノードの停止と停滞、パケットの損失、網の分断。 |
| [deploy/observability](../../../deploy/observability/README.ja.md) | Compose のプロジェクトの横で Prometheus と Grafana を動かします。 |
| [deploy/k8s](../../../deploy/k8s/README.ja.md) | 同じ IMS を、kind で作った手元の Kubernetes のクラスタで動かします。 |
| [deploy/cloud](../../../deploy/cloud/README.ja.md) | クラウドの資源の定義です。適用すると料金がかかります。 |

## 構成全体の作り

どの手順書でも、次のことは変わりません。

- **既定では信号を暗号化しません。** 鍵や追加の設定が無くても、Wireshark はキャプチャの SIP、Diameter、DNS のメッセージをすべて解読します。
- **ノードは標準のポート番号を使います。** SIP は 5060、Diameter は 3868、DNS は 53 です。そのため Wireshark は、ポートから正しい解析器を選びます。
- **役割は設定で決まります。** 1 つのイメージが IMS のすべての役割を動かします。P-CSCF、I-CSCF、S-CSCF、アプリケーションサーバーなどのどれになるかは、ノードの設定ファイルが決めます。1 つのノードがいくつかの役割を同時に持つこともできます。
- **1 つの役割を何台にも広げられます。** 商用網は役割ごとに多くのノードを動かします。ここの役割も、そのように作っています。Compose のプロジェクトは役割ごとに 1 台を動かします。メッセージを追うにはそれで足ります。
- **利用者の状態をどこに置くかは設定で決まります。** そうした状態を持つ役割は、それを `Store` の後ろに持ちます。S-CSCF は登録を、P-CSCF は UE ごとの IP の対応付けと、その UE の登録の状態への購読を、HSS は加入者を、IP-SM-GW は届ける途中のショートメッセージを、MMTel AS と IBCF は呼を、MRFC は会議を持ちます。設定ファイルに種類が `valkey` の `store` の節があるノードは、状態をその節が指す Valkey のサーバーに置きます。同じ名前で作り直したノードは、前のノードが残した状態をそこで見つけます。節が無ければ、この手順書の Compose のプロジェクトのように、ノードは状態を自分のプロセスのメモリに置き、状態はプロセスと一緒に消えます。S-CSCF、HSS、IP-SM-GW は、利用者ごとのロックもそこに置きます。そのため、1 人の利用者の登録や加入者のデータを扱うノードは、役割ごとに一度に 1 台だけになります。ほかのノードは、その間もほかの利用者を扱えます。
