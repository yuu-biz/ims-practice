# 手順書

[English](../en/README.md)

この手順書では、手元のマシンで IMS を動かし、そのメッセージをキャプチャで 1 つの手順ずつ追います。最初にこのページを読んでください。手順書を読む順、最初に打つコマンド、片付けの方法、Docker Compose のプロジェクトが起動するものをまとめています。

## 読む順

1. [信号をキャプチャして Wireshark で開く](capture.md)。システムを起動してキャプチャを開き、Diameter の接続ができるところを見ます。
2. [UE が IMS に登録する流れを追う](registration.md)。P-CSCF、I-CSCF、S-CSCF を通る REGISTER と、SIP Digest、Cx。加入のプロビジョニング、登録中の利用者への新しいサービスプロファイルの push、試験用の利用者だけの収容もここにあります。
3. [音声通話の流れを追う](call.md)。電話番号で別の UE にかける INVITE と、ENUM、メディア中継。
4. [SMS の流れを追う](sms.md)。IP-SM-GW を通る SMS over IP と、その報告。
5. [MMTel AS の付加サービス](mmtel.md)。番号通知、転送、規制と、Ut での設定。MMTel AS は業界では TAS（Telephony Application Server）や T-AS とも呼ばれます。
6. [MRF による音声ガイダンス、呼び出し音、会議](mrf.md)。規制した通話の前の音声ガイダンス、CAT、割込通話、3 人の会議。
7. [VoLTE の呼の流れ](volte.md)。EPC スタブ、IMS-AKA、Rx でのポリシー制御と、プリコンディションを使った AMR-WB の通話。
8. [IMS 網どうしを相互接続する](interconnect.md)。境界の IBCF と TrGW、構成の秘匿、相互接続プロファイル。
9. [ラボで IMS を見る](lab.md)。HEP の写しから描くラダー図、加入者の一覧、ブラウザから操作する UE シミュレータ、アクセス網の bearer、わざと入れる障害。
10. [アラームを OSS に渡す](oss.md)。運用者の網の管理に向けた OAM のインタフェース。3GPP が定める形での、網全体のアラームの一覧、その確認、アラームごとの通知。

略語は[用語集](../../glossary.md)で説明しています。

### 最初の手順書の UE はアクセス網を持たない

2 から 6 の手順書の UE は、固定のアクセスやパソコンで使うようなソフトフォンです。SIM カードも無線のアクセスも持ちません。Docker のネットワークを通って P-CSCF に届き、SIP Digest で登録し、G.711 で通話します。そのため最初の手順書では、EPC、IMS-AKA、音声ベアラを待つことを抜きにして、IMS だけを見られます。

携帯電話は、それぞれの手順の前と途中で、もっと多くのことをします。EPC にアタッチしてアドレスと P-CSCF を受け取り、USIM の鍵で IMS-AKA の登録をし、着信側が鳴る前に音声ベアラができるのを待ちます。[VoLTE の呼の流れ](volte.md)は、最初の手順書で見たことに、これらの手順を加えます。

## 最初のコマンド

リポジトリのルートで実行します。最初のビルドには数分かかります。

```sh
docker compose up -d --build
docker compose run --rm ue register -deregister-after 5s
```

1 つ目のコマンドで、ノードとキャプチャが起動します。2 つ目を打つ前に、I-CSCF と S-CSCF が HSS への Diameter の接続を開くまで待ちます。HSS が接続を受け付ける前に起動したノードは、30 秒後（Tc、RFC 6733 2.1）にもう一度つなぎに行くので、長くて 1 分ほどかかります。接続ができると、`docker compose logs scscf | grep I-Open` が 1 行を表示します。2 つ目のコマンドは、加入者 1 を登録し、5 秒後に登録を解除します。そのあと、`captures/` のいちばん新しいファイルを Wireshark で開きます。開き方は[信号をキャプチャして Wireshark で開く](capture.md)にあります。

## 片付け

どの手順書も、最後に、起動したものをすべて止めます。

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

このコマンドはイメージを残すので、次の手順書をビルドし直さずに始められます。手順書をすべて終えたら、イメージも削除します。

```sh
docker compose --profile ue --profile phones --profile volte --profile oss down --rmi all -v --remove-orphans
```

キャプチャのファイルは、削除するまで `captures/` に残ります。

## Compose のプロジェクトが起動するもの

`docker compose up` は次のサービスを起動します。IMS のノードはすべて同じイメージで動き、`deploy/compose/` にある各ノードの設定ファイルが役割を決めます。

| サービス | アドレス | 何か |
| --- | --- | --- |
| `dns` | 172.28.0.2 | ホーム網のドメインの DNS。NAPTR、SRV、A のレコードと ENUM。 |
| `hss` | 172.28.0.10 | HSS のスタブ。CSCF には Cx、アプリケーションサーバーには Sh、SMS のサービスセンタには S6c で応えます。 |
| `icscf` | 172.28.0.20 | I-CSCF。 |
| `scscf` | 172.28.0.30 | S-CSCF。 |
| `pcscf` | 172.28.0.40 | P-CSCF。 |
| `agw` | 172.28.0.50 | IMS-AGW の役割のメディア中継。P-CSCF が制御します。 |
| `lab` | 172.28.0.60 | http://localhost:8080 のラボと、HEP の収集先。 |
| `oam` | 172.28.0.65 | OAM のサーバー。ノードを保守用の API で、加入者を HSS のプロビジョニングの API で操作します。 |
| `ipsmgw` | 172.28.0.70 | IP-SM-GW。SMS over IP のアプリケーションサーバーです。 |
| `sc` | 172.28.0.71 | SMS のサービスセンタ。ショートメッセージを預かり、配達します。 |
| `mmtel` | 172.28.0.75 | MMTel AS。付加サービスのアプリケーションサーバーです。 |
| `epc` | 172.28.0.80 | PCRF を含む EPC のスタブ。VoLTE の UE が使います。 |
| `mrfc` | 172.28.0.85 | MRFC。MRF の制御の側です。 |
| `mrfp` | 172.28.0.86 | MRFP。メディアを流し、混ぜます。 |
| `capture` | ホストのネットワーク | tcpdump。キャプチャのファイルを `captures/` に書きます。 |

### プロファイル

UE はプロファイルに入れてあるので、`docker compose up` では起動しません。

| プロファイル | サービス | 動かし方 |
| --- | --- | --- |
| `ue` | `ue`、`ue2`、`ue3`、`softphone` | `docker compose run --rm ue register` のように、1 回のコマンドごとに動かします。`softphone` には、ほかの人が作ったソフトフォンの pjsua と SIPp が入っています。 |
| `phones` | `phone1`、`phone2`、`phone3` | 起動したままになり、ラボの指示で動きます。`docker compose --profile phones up -d` で起動します。 |
| `volte` | `volte10`、`volte12` | VoLTE の UE です。`phones` と同じく起動したままになります。`docker compose --profile volte up -d` で起動します。 |
| `oss` | `oss` | OSS の代わりです。OAM からアラームの通知を受け、OSS の要求を送ります。`docker compose --profile oss up -d` で起動します。 |

`ue` と `phones` は同じ加入者を使うので、一度にどちらか一方だけを動かします。

### 加入者、UE、サービスの対応

この網の試験用の加入者は `deploy/compose/hss.yaml` に、網間接続の手順書に出てくる網 B の加入者は `deploy/interconnect/b/hss.yaml` にあります。手順書では、加入者 1 の UE を UE 1 と呼ぶように、使っている加入者の番号で UE を呼びます。コマンドやラボのカードで要るところには、サービスの名前をそのまま書きます。網が 2 つ出てくるところでは、網の文字を先に付けます。UE A1 は網 A の UE 1 で、UE B1 は網 B の 1 人目の加入者の UE です。網 B のサービスの名前は `scscf-b` のように `-b` で終わり、UE の名前の数字は使っている加入者を指します。

| 加入者 | IMPI（private identity） | 番号 | サービス | 手順書での呼び名 |
| --- | --- | --- | --- | --- |
| 1 | `001010000000001@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0101 | `ue`、`phone1` | UE 1、網間接続の手順書では UE A1 |
| 2 | `001010000000002@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0102 | `ue2`、`phone2` | UE 2、網間接続の手順書では UE A2 |
| 3 | `001010000000003@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0103 | `ue3`、`phone3` | UE 3 |
| 4 | `+12025550104@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0104 | `softphone`（pjsua） | pjsua |
| 10 | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0110 | `volte10` | `volte10` |
| 11 | `001010000000011@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0111 | `softphone`（SIPp） | SIPp |
| 12 | `001010000000012@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0112 | `volte12` | `volte12` |
| 網 B の 1 | `001020000000001@ims.mnc002.mcc001.3gppnetwork.org` | +1-202-555-0151 | `ue-b1` | UE B1 |
| 網 B の 2 | `001020000000002@ims.mnc002.mcc001.3gppnetwork.org` | +1-202-555-0152 | `ue-b2` | UE B2 |

### どのノードも動かさないコード

`internal` の下のパッケージには、リポジトリにはあっても、上のどのノードにも入っていないものがあります。`cmd` のどのバイナリにも組み込まれていないので、その動きはキャプチャに出ません。それぞれのパッケージの説明にも、そう書いてあります。

| パッケージ | 何か |
| --- | --- |
| `fivegc`、`n5`、`nhss`、`sbi` | 5GS の側です。5GC のスタブ、PCF の N5 のポリシーの API、HSS のサービスベースのインタフェース、それらの API が共有する部分です。 |
| `nas` | IMS を通らない SMS です。UE と MME（5GS では AMF）のあいだでショートメッセージを運ぶ NAS のメッセージです。 |
| `h248/h248test`、`kvstore/storetest`、`openapitest`、`rtptest` | 試験だけが使う道具です。 |

### HTTP の API

ノードは、SIP と Diameter のほかに HTTP でも話します。各ノードの保守の API、OAM の API と OSS に向けた OAM のインタフェース、HSS のプロビジョニングの API、ラボ、UE シミュレータ、メディア中継ノードの API です。どれが 3GPP の仕様に従い、どれがこのプロジェクト独自のものかを、それぞれの OpenAPI のファイルとともに [HTTP の API](apis.md) に並べています。

## ほかの動かし方

| ディレクトリ | 何をするか |
| --- | --- |
| [deploy/chaos](../../../deploy/chaos/README.ja.md) | わざと障害を起こします。ノードの停止と停滞、パケットの損失、網の分断。 |
| [deploy/observability](../../../deploy/observability/README.ja.md) | Compose のプロジェクトの横で Prometheus と Grafana を動かします。 |
| [deploy/k8s](../../../deploy/k8s/README.ja.md) | 同じ IMS を、kind で作った手元の Kubernetes のクラスタで動かします。 |
| [deploy/cloud](../../../deploy/cloud/README.ja.md) | クラウドの資源の定義です。適用すると料金がかかります。 |

### 利用者の状態をノードの外の保存先に置く

`deploy/redundancy/redundancy.yaml` は、プロジェクトに Valkey のサーバーを足し、利用者の状態を持つノードに、その保存先を指す `store` の節を与えます。状態を持つのは HSS、S-CSCF、P-CSCF、IP-SM-GW、MMTel AS、MRFC と、2 つのメディア中継ノード（IMS-AGW と MRFP）です。`compose.yaml` にこのファイルを重ねて起動します。

```sh
export COMPOSE_FILE=compose.yaml:deploy/redundancy/redundancy.yaml COMPOSE_PATH_SEPARATOR=:
docker compose up -d --build
```

これで、手順書のどのコマンドもこの構成で動きます。この変数が無いシェルでは、`docker compose` のたびに `-f compose.yaml -f deploy/redundancy/redundancy.yaml` を付けます。ノードのアドレスも、設定も、やり取りするメッセージも変わらないので、キャプチャは手順書のとおりに読めます。変わるのは、各ノードが知ったことをどこに置くかです。

```sh
docker compose run --no-deps -d --name registered-ue ue register
docker compose exec valkey valkey-cli --scan --pattern 'ims:*'
```

`-deregister-after` を付けない `ue register` は、止めるまで登録したままです。そのため、キーを見ているあいだも登録は残っています。キーは、この構成の接頭辞、書いた役割、項目の種類、その項目を引くための名前を、この順に並べたものです。その中に、次の 3 つがあります。どれも加入者 1 の秘密の識別子で引くキーです。

```text
ims:hss:subscriber:001010000000001@ims.mnc001.mcc001.3gppnetwork.org
ims:scscf:registration:001010000000001@ims.mnc001.mcc001.3gppnetwork.org
ims:pcscf:reg-subscription:001010000000001@ims.mnc001.mcc001.3gppnetwork.org
```

名前の中に SIP の URI や Call-ID のように `:` が入るときは、`%3A` と書きます。キーの区切りと混ざらないようにするためです。項目は、値と版を持つハッシュとして置かれ、値は JSON です。

```sh
docker compose exec valkey valkey-cli hget \
  ims:scscf:registration:001010000000001@ims.mnc001.mcc001.3gppnetwork.org value
```

これは、保存先が無ければ S-CSCF が自分のメモリに持っている登録です。UE の Contact、登録の期限、HSS が Cx で送った利用者のプロファイルが入っています。`docker stop registered-ue` を実行すると UE は登録を解除し、次の scan では登録のキーが消えています。HSS の加入者のキーはそのまま残ります。終わったら、変数を設定したまま、手順書と同じように片付けます。

```sh
docker rm registered-ue
docker compose --profile ue down -v --remove-orphans
```

`-v` を付けると、ノードと一緒に保存先のボリュームも消えます。次に起動したときは、何も入っていない保存先から始まります。

### メディア中継ノードを H.248 で制御する

メディア中継ノードは、呼を扱うノードが外から制御するメディアゲートウェイです。P-CSCF はこれを、このプロジェクトの小さな HTTP の制御 API で制御します。`deploy/h248/h248.yaml` は、これを H.248（ITU-T H.248.1）に切り替えます。H.248 はこの用途の標準のプロトコルで、3GPP はこの参照点を Iq としてプロファイルしています（TS 29.334）。運ぶ手順は同じなので、同じ通話のほかの部分は何も変わらず、2 つのノードの間の制御だけが変わります。このファイルを `compose.yaml` の上に重ねて起動します。

```sh
export COMPOSE_FILE=compose.yaml:deploy/h248/h248.yaml COMPOSE_PATH_SEPARATOR=:
docker compose up -d --build
```

これで、手順書のどのコマンドもこの構成で動きます。変数を設定していないシェルでは、`docker compose` のたびに `-f compose.yaml -f deploy/h248/h248.yaml` を付けます。

変わる節は 2 つです。P-CSCF は、`media_relays` の代わりに `h248` を読みます。

```yaml
pcscf:
  h248:
    listen: 0.0.0.0:2944
```

ここに中継ノードは書きません。中継ノードのほうが P-CSCF に登録し、コンテキストの ID が、そのコンテキストを持つ中継ノードを指します。そのため、このファイルを変えずに中継ノードを増やしたり減らしたりできます。ただし、中継ノードが登録する P-CSCF は 1 台で、そこに持つコンテキストを変えたり解放したりできるのもその 1 台だけです。そのため、この方法でメディアを制御する P-CSCF は 1 台で動き、設定は `pool` も上の構成の保存先も受け付けません。コンテキストを解放できないノードに呼が届くより、断るほうを選んでいます。メディア中継ノードは、HTTP の API のアドレスである `control` の代わりに `h248` を読み、HTTP の API は配信しなくなります。

```yaml
agw:
  h248:
    listen: 0.0.0.0:2944
    host: agw.ims.mnc001.mcc001.3gppnetwork.org
    controller: pcscf.ims.mnc001.mcc001.3gppnetwork.org:2944
```

2944 番は、H.248 のテキストの符号化の標準のポートです。メッセージは暗号化しないので、Wireshark はキャプチャの中でこれを `megaco` として読みます。

```sh
docker compose exec capture sh -c 'tshark -r "$(ls -t /captures/ims-*.pcap | head -n 1)" -Y megaco'
```

先に通話をしておくと、2 つのノードの間の制御が最初から最後まで出てきます。

- 中継ノードからの `ServiceChange=ROOT`。起動したことと、コンテキストを 1 つも持っていないことを P-CSCF に伝えるメッセージです。P-CSCF は応答でこれを受け入れます。`ROOT` は、ゲートウェイ全体を表す終端です。
- P-CSCF からの `Modify=ROOT`。P-CSCF から一定の時間なにも届かなかったら報告するように、中継ノードに頼みます。
- P-CSCF からの `ROOT` への `AuditValue`。10 秒ごとに、何も問い合わせずに送ります。ほかに送るものが無いときに送るメッセージで、互いを失ったと思わないためにあります。
- 通話ごとの `Add=ip/1/access/$` と `Add=ip/1/core/$`。1 つのトランザクションに 2 つ入っています。P-CSCF は、UE の側の終端と網の側の終端を持つコンテキストを求め、識別子とアドレスとポートは中継ノードに選ばせます。`$` がその意味です。中継ノードは選んだ値を応答で返し、P-CSCF はそのアドレスとポートを、次に渡す SDP に書きます。HTTP の API のときと同じです。
- `Modify=ip/1/access/…` と `Modify=ip/1/core/…`。それぞれの終端に、相手がメディアを受け取る場所を伝えます。場所は相手の SDP から取ります。
- 通話が終わるときの `Subtract=*`。コンテキストの 2 つの終端を一度に解放します。

HTTP の API での同じ通話は[音声通話](call.md)にあります。2 つのキャプチャを並べて読むと、1 つの通話が 2 つのプロトコルで制御されるようすが見られます。こちらは `Add`、`Modify`、`Subtract` で、あちらは HTTP の API の要求です。

## 構成全体の作り

どの手順書でも、次のことは変わりません。

- **既定では信号を暗号化しません。** 鍵や追加の設定が無くても、Wireshark はキャプチャの SIP、Diameter、DNS のメッセージをすべて解読します。
- **ノードは標準のポート番号を使います。** SIP は 5060、Diameter は 3868、DNS は 53 です。そのため Wireshark は、ポートから正しい解析器を選びます。
- **役割は設定で決まります。** 1 つのイメージが IMS のすべての役割を動かします。P-CSCF、I-CSCF、S-CSCF、アプリケーションサーバーなどのどれになるかは、ノードの設定ファイルが決めます。1 つのノードがいくつかの役割を同時に持つこともできます。
- **1 つの役割を何台にも広げられます。** 商用網は役割ごとに多くのノードを動かします。ここの役割も、そのように作っています。Compose のプロジェクトは役割ごとに 1 台を動かします。メッセージを追うにはそれで足ります。Cx か Sh で HSS につなぐノードは、`peers` に HSS の台をいくつでも、`origin_host` を台ごとに分けて、`kind: hss` を付けて書けます。`kind` は、同じノードの P-CSCF が Rx でつなぐ `kind: pcrf` の相手と区別するためにあります。どの台も全加入者を扱うので、ノードは、接続が開いている台のうち先に書いたものへ送ります。その台が答えない、接続が閉じた、DIAMETER_TOO_BUSY を返した、のどれかなら、次の台へ同じ要求をもう一度送ります。送り直す要求には Diameter のヘッダの T ビットが立っていて、受け取った台は、最初の台がまだ処理している要求の写しかもしれないと分かります（RFC 6733 5.5.4）。S-CSCF は、どの台が利用者を扱っているかが分かると、その登録の要求の Destination-Host にその台の名前を入れます（TS 29.229 5.5）。宛先の決まった要求はその台にだけ届き、ほかの台は代わりに答えられません。そのため、始まった登録は答えた台に結び付いたままで、その台が止まった利用者は登録し直します。登録し直すと、次に答えた台から始まります。
- **利用者の状態をどこに置くかは設定で決まります。** そうした状態を持つ役割は、それを `Store` の後ろに持ちます。S-CSCF は登録を、P-CSCF は UE ごとの IP の対応付けと、その UE の登録の状態への購読を、HSS は加入者を、IP-SM-GW は届ける途中のショートメッセージを、SMS のサービスセンタは宛先ごとに預かっているショートメッセージを、MMTel AS と IBCF は呼を、MMTel AS はほかに、フォーカスを務める会議を参加者と購読と一緒に、MRFC は会議ごとに音声を混ぜる MRFP のコンテキストを、メディア中継ノードは RTP のポートを開いているコンテキストを持ちます。設定ファイルに種類が `valkey` の `store` の節があるノードは、状態をその節が指す Valkey のサーバーに置きます。同じ名前で作り直したノードは、前のノードが残した状態をそこで見つけます。節が無ければ、この手順書の Compose のプロジェクトのように、ノードは状態を自分のプロセスのメモリに置き、状態はプロセスと一緒に消えます。S-CSCF、HSS、IP-SM-GW は、利用者ごとのロックもそこに置きます。そのため、1 人の利用者の登録や加入者のデータを扱うノードは、役割ごとに一度に 1 台だけになります。ほかのノードは、その間もほかの利用者を扱えます。時刻が来たときに動作が要る期限、たとえば登録の終わりや、その登録への購読の終わりも、その状態と一緒に時刻として置きます。各ノードは 1 秒ごとに時刻の来たものを探し、見つけたものを自分で処理します。そのため、ほかのノードの代わりに起動したノードが、そのノードの見ていた期限を処理します。
