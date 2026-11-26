# 信号をキャプチャして Wireshark で開く

[English](../en/capture.md)

このプロジェクトのノードはすべて 1 つの Docker のネットワークで通信します。キャプチャ用のコンテナがこのネットワークの通信をすべて記録するので、ノード間のやり取りの全体が 1 つのファイルに入ります。

## 始める前に

- Docker と Docker Compose が動いていること。
- Wireshark がインストールされていること。

## システムを起動する

リポジトリのルートで次のコマンドを実行します。

```sh
docker compose up -d --build
```

キャプチャ用のコンテナが最初に起動し、ノードはそのあとで起動します。そのため、ノードが最初に送るメッセージもキャプチャに入ります。

## キャプチャのファイルを開く

キャプチャ用のコンテナは起動するたびに `captures/` に新しいファイルを作ります。ファイル名は UTC の起動時刻で、たとえば `captures/ims-20261119T042120Z.pcap` です。

1. Wireshark の **File > Open** で、いちばん新しいファイルを開きます。
2. 表示フィルタの欄に `diameter` と入力し、Enter キーを押します。
3. メッセージをラダー図で見るには、**Statistics > Flow Graph** を選びます。

キャプチャの途中でもファイルを開けます。新しいパケットを見るには、ファイルを開き直すか、**View > Reload** を選びます。

## 通信をライブで見る

パケットを発生と同時に見るには、キャプチャを Wireshark に流し込みます。

- Git Bash では `scripts/capture-live.sh` を実行します。`wireshark` が `PATH` に無いときは、先に `WIRESHARK` に実行ファイルのフルパスを設定します。
- PowerShell 7.4 以降では `scripts/capture-live.ps1` を実行します。`-Wireshark <パス>` を指定しなければ、`C:\Program Files\Wireshark\Wireshark.exe` を使います。

## ノードを起動すると見えるもの

各ノードのアドレスは次のとおりです。「サービス」の列は `compose.yaml` での名前です。プロファイルのあるノードは `docker compose up` では起動しません。`--profile <名前>` でプロファイルごと起動するか、UE は `docker compose run --rm <サービス>` で実行します。

| ノード | サービス | アドレス | プロファイル |
| --- | --- | --- | --- |
| DNS | `dns` | 172.28.0.2 | |
| HSS | `hss` | 172.28.0.10 | |
| I-CSCF | `icscf` | 172.28.0.20 | |
| S-CSCF | `scscf` | 172.28.0.30 | |
| P-CSCF | `pcscf` | 172.28.0.40 | |
| メディア中継ノード（IMS-AGW） | `agw` | 172.28.0.50 | |
| ラボ | `lab` | 172.28.0.60 | |
| OAM のサーバー | `oam` | 172.28.0.65 | |
| OSS の代わり | `oss` | 172.28.0.66 | `oss` |
| IP-SM-GW | `ipsmgw` | 172.28.0.70 | |
| SMS のサービスセンタ | `sc` | 172.28.0.71 | |
| MMTel AS | `mmtel` | 172.28.0.75 | |
| EPC スタブ（PCRF スタブを含む） | `epc` | 172.28.0.80 | |
| MRFC | `mrfc` | 172.28.0.85 | |
| MRFP | `mrfp` | 172.28.0.86 | |
| UE シミュレータ（加入者 1） | `ue` | 172.28.0.100 | `ue` |
| UE シミュレータ（加入者 2） | `ue2` | 172.28.0.101 | `ue` |
| pjsua と SIPp | `softphone` | 172.28.0.102 | `ue` |
| UE シミュレータ（加入者 3） | `ue3` | 172.28.0.103 | `ue` |
| ラボが操作する UE シミュレータ（加入者 1） | `phone1` | 172.28.0.110 | `phones` |
| ラボが操作する UE シミュレータ（加入者 2） | `phone2` | 172.28.0.111 | `phones` |
| ラボが操作する UE シミュレータ（加入者 3） | `phone3` | 172.28.0.112 | `phones` |
| VoLTE の UE（加入者 10） | `volte10` | 172.28.0.120 | `volte` |
| VoLTE の UE（加入者 12） | `volte12` | 172.28.0.121 | `volte` |

`capture` サービスにはアドレスがありません。ホストのネットワークをそのまま使い、ブリッジ `ims0` を捕らえます。

次の 6 つのノードが、「サーバー」の列のノードの TCP ポートに Diameter の接続を開きます。

| クライアント | サーバー | アプリケーション | ポート |
| --- | --- | --- | --- |
| `icscf` | `hss` | Cx | 3868 |
| `scscf` | `hss` | Cx | 3868 |
| `ipsmgw` | `hss` | Sh | 3868 |
| `ipsmgw` | `sc` | SGd | 3868 |
| `sc` | `hss` | S6c | 3868 |
| `mmtel` | `hss` | Sh | 3868 |
| `pcscf` | `epc` | Rx | 3868 |

PCRF スタブは `epc` ノードの中にあります。P-CSCF の Rx の接続は HSS ではなく、そこに向かいます。フィルタ `diameter` で、次のメッセージが見えます。

| メッセージ | 送信元 | 意味 |
| --- | --- | --- |
| Capabilities-Exchange Request（CER） | I-CSCF、S-CSCF、IP-SM-GW、MMTel AS、P-CSCF | 接続した側が、自分が誰であるかと、対応するアプリケーションを伝える。I-CSCF と S-CSCF は Cx、2 つの AS は Sh、P-CSCF は Rx。 |
| Capabilities-Exchange Answer（CEA） | HSS、EPC スタブ | 接続された側が Result-Code 2001（DIAMETER_SUCCESS）で応え、対応するアプリケーションを示す。HSS は Cx と、AS が使う Sh と、短メッセージの SC が使う S6c（TS 29.338）を示す。PCRF スタブは Rx を示す。 |
| Device-Watchdog Request と Answer（DWR、DWA） | どちらの側も | 約 30 秒間なにも受け取らなかった側が、相手が動いているかを確かめる。30 秒は監視タイマーの既定値で（RFC 3539 3.4.1 [1]）、タイマーごとに最大 2 秒早まったり遅れたりする。 |
| Disconnect-Peer Request と Answer（DPR、DPA） | 止まるノード | 接続を閉じる前に、切断することを相手に伝える。 |

DPR と DPA を見るには、ノードを 1 つ止めます。

```sh
docker compose stop scscf
```

S-CSCF が Disconnect-Cause `REBOOTING` を付けた DPR を送り、HSS が DPA で応えます。`docker compose start scscf` でノードを起動し直すと、新しい CER と CEA が続きます。

これらのメッセージは RFC 6733（Diameter の基本プロトコル）で規定されています。ウォッチドッグは RFC 3539 に従っています。

Diameter の接続では、仕様の次の部分を省いています。

| 箇所 | 省略していること | 理由 |
| --- | --- | --- |
| Diameter、TS 29.229 5.4 と TS 29.329 5 | Cx と Sh は SCTP で運ぶことになっているが、どの接続も TCP で運ぶ。Rx も TCP で運ぶが、TS 29.214 5.2 は Rx の接続を基本プロトコルに任せているので、こちらは何も省いていない。 | RFC 6733 2.1 では基本プロトコルをどちらの上でも動かせ、このプロジェクトのコンテナを動かすどのマシンでも、SCTP と違って TCP は使えるため。 |
| Diameter、RFC 6733 5.4.3 | Disconnect-Cause が `BUSY` か `DO_NOT_WANT_TO_TALK_TO_YOU` の DPR を受けたノードは、相手の準備が整ったあとも、自分が起動し直すまでその相手につなぎ直さない。 | この条項はつなぎ直さないよう求めるだけで、いつからつなぎ直してよいかを決めていないため。このプロジェクトのノードが送るのは `REBOOTING` だけなので、こうなるのは別の実装の相手のときだけ。 |

## SMS over IP の表示フィルタ

SMS は SIP の MESSAGE の本文に入れて運びます。本文のメディアタイプは `application/vnd.3gpp.sms` です。Wireshark はこの本文を設定なしでデコードします。RP 層（TS 24.011）は `GSM A-I/F RP` のディセクタが、その中の TPDU（TS 23.040）とテキストは `GSM SMS` のディセクタが表示します。

| フィルタ | 表示されるもの |
| --- | --- |
| `sip.Content-Type == "application/vnd.3gpp.sms"` | SMS か報告を運ぶ MESSAGE のすべて。 |
| `gsm_a.rp.msg_type == 0x00` | UE からの RP-DATA、つまり UE が送る SMS。`0x01` は UE への RP-DATA、`0x02` と `0x03` は UE からと UE への RP-ACK、`0x04` と `0x05` は RP-ERROR。 |
| `gsm_sms.tp-mti == 1` | UE からの SMS-SUBMIT と、UE への SMS-SUBMIT-REPORT。`0` は SMS-DELIVER と SMS-DELIVER-REPORT、`2` は SMS-STATUS-REPORT。組になる 2 つは、送る向きで見分ける。 |
| `gsm_sms.sms_text contains "Hello"` | そのテキストを含む SMS。GSM 7 ビットのアルファベットでも UCS2 でも使える。 |
| `gsm_sms.tp-da == "12025550102"` | その番号に送った SMS。 |
| `sip.In-Reply-To` | submit report と delivery report。どちらも、答える相手の MESSAGE を示す。 |
| `diameter.cmd.code == 306 \|\| diameter.cmd.code == 307` | Sh のメッセージ（アプリケーション 16777217）。UDR と UDA、PUR と PUA。 |

`scripts/e2e-sms.sh` は、この表のフィルタをすべて、SMS のシナリオのキャプチャに当てます。フレームが 1 つも見つからないフィルタがあれば、試験は失敗します。送った SMS のフレームの一覧は `e2e-out/sms-ladder.txt` に書き出します。

## ESP の中の SIP を読む

VoLTE の UE と P-CSCF のあいだの信号は、IPsec の ESP で保護できます。[VoLTE の呼の流れ](volte.md#esp-をかけた-sec-agree)の構成でこれを有効にします。中身は暗号化されていないので SIP はそのまま入っていますが、Wireshark に中を見させる設定が 1 つ要ります。設定を入れるまで、これらのパケットは `ESP (SPI=0x...)` としか表示されず、`sip` の表示フィルタにも当たりません。

**Edit > Preferences > Protocols > ESP** で **Attempt to detect/decode NULL encrypted ESP payloads** にチェックを入れます。Wireshark はアルゴリズムが使う完全性チェック値の長さ（12、16、24、32 オクテット）を試してペイロードの終わりを割り出し、中にあるものを解読します。VoLTE の手順書には、もう 1 つの方法として、長さを Wireshark に割り出させる代わりに完全性のアルゴリズムを示す **ESP SAs** の行と、保護ポートを TCP の SIP として読ませるオプションがあります。

## ラボに送るコピー（HEP）

ノードは送受信した SIP と Diameter のメッセージのコピーを、UDP の 9060 番ポートで待つラボにも送れます。コピーの形式は HEP（Homer Encapsulation Protocol）のバージョン 3 です。ノードの設定に `hep: {collector: lab:9060}` のような `hep` の項目があると、ノードはコピーを送ります。UE シミュレータは `-hep lab:9060` を指定すると送ります。どちらも無ければ、何も送りません。

ラボは呼の経路に入っていないので、ラボがまだ起動していないなどの理由で `collector` の名前を解決できなくても、ノードは起動します。ノードは警告をログに出し、そのあいだのコピーは捨てます。コピーを送れなかったときは、多くても 5 秒に 1 回、名前を解決し直します。そのため、あとから起動したラボにもコピーが届きます。

- コピーもポート 9060 宛ての UDP パケットとしてキャプチャのファイルに入ります。Wireshark には HEP を解読する機能が標準では無いため、解読するには [sipcapture/hep-wireshark](https://github.com/sipcapture/hep-wireshark) の Lua プラグインを入れます。SIP と Diameter のメッセージ自体はこれまでどおり解読されます。コピーを表示から外すには、フィルタ `not udp.port == 9060` を使います。
- SIP のコピーには、通信路を流れたとおりのメッセージが入ります。
- Diameter のコピーには、メッセージを JSON にしたものが入り、プロトコルの種類は 0x38 の `DIAMETER (JSON)`です。HEP3 rev. 37 には、Diameter のメッセージをバイナリのまま運ぶ種類が定義されていないためです。

HEP の符号化では仕様の次の部分を省いています。

| 箇所 | 省略していること | 理由 |
| --- | --- | --- |
| HEP3 rev. 37 の Generic chunk types | IPv6 のアドレス（チャンク 0x0005 と 0x0006）。 | このプロジェクトのノードはすべて IPv4 のアドレスを持つため。 |
| HEP のリポジトリの docs/HEP3-extended-packet-length.md | 65535 オクテットを超えるパケットのための 32 ビットの長さ。 | このプロジェクトの SIP と Diameter のメッセージは、その大きさに遠く及ばないため。 |

## システムを止める

- すべてのプロファイルのノードと UE を止め、イメージは残すときは `docker compose --profile ue --profile phones --profile volte down -v --remove-orphans` を実行します。ほかの手順書も最後に同じコマンドを打つので、次の手順書をビルドし直さずに始められます。
- 手順書をすべて終えたら、ビルドで作ったイメージも削除します。

  ```sh
  docker compose --profile ue --profile phones --profile volte --profile oss down --rmi all -v --remove-orphans
  ```

キャプチャのファイルは削除するまで `captures/` に残ります。
