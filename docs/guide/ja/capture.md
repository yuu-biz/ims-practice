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

各ノードのアドレスは次のとおりです。

| ノード | アドレス |
| --- | --- |
| DNS | 172.28.0.2 |
| HSS | 172.28.0.10 |
| I-CSCF | 172.28.0.20 |
| S-CSCF | 172.28.0.30 |
| P-CSCF | 172.28.0.40 |
| メディア中継ノード（IMS-AGW） | 172.28.0.50 |
| ダッシュボード | 172.28.0.60 |
| IP-SM-GW | 172.28.0.70 |
| UE シミュレータ | 172.28.0.100 |

I-CSCF、S-CSCF、IP-SM-GW はそれぞれ HSS の TCP 3868 番ポートに Diameter の接続を開きます。フィルタ `diameter` で、次のメッセージが見えます。

| メッセージ | 送信元 | 意味 |
| --- | --- | --- |
| Capabilities-Exchange Request（CER） | I-CSCF、S-CSCF、IP-SM-GW | 接続した側が、自分が誰であるかと、対応するアプリケーションを伝える。CSCF は Cx、IP-SM-GW は Sh。 |
| Capabilities-Exchange Answer（CEA） | HSS | HSS が Result-Code 2001（DIAMETER_SUCCESS）で応え、対応するアプリケーションとして Cx と、AS が使う Sh を示す。 |
| Device-Watchdog Request と Answer（DWR、DWA） | どちらの側も | 約 10 秒間なにも受け取らなかった側が、相手が動いているかを確かめる。 |
| Disconnect-Peer Request と Answer（DPR、DPA） | 止まるノード | 接続を閉じる前に、切断することを相手に伝える。 |

DPR と DPA を見るには、ノードを 1 つ止めます。

```sh
docker compose stop scscf
```

S-CSCF が Disconnect-Cause REBOOTING を付けた DPR を送り、HSS が DPA で応えます。`docker compose start scscf` でノードを起動し直すと、新しい CER と CEA が続きます。

これらのメッセージは RFC 6733（Diameter の基本プロトコル）で規定されています。ウォッチドッグは RFC 3539 に従っています。

## SMS over IP の表示フィルタ

SMS は SIP の MESSAGE の本文に入れて運びます。本文のメディアタイプは `application/vnd.3gpp.sms` です。Wireshark はこの本文を設定なしでデコードします。RP 層（TS 24.011）は GSM A-I/F RP のディセクタが、その中の TPDU（TS 23.040）とテキストは GSM SMS のディセクタが表示します。

| フィルタ | 表示されるもの |
| --- | --- |
| `sip.Content-Type == "application/vnd.3gpp.sms"` | SMS か報告を運ぶ MESSAGE のすべて。 |
| `gsm_a.rp.msg_type == 0x00` | UE からの RP-DATA、つまり UE が送る SMS。`0x01` は UE への RP-DATA、`0x02` と `0x03` は UE からと UE への RP-ACK、`0x04` と `0x05` は RP-ERROR。 |
| `gsm_sms.tp-mti == 1` | UE からの SMS-SUBMIT と、UE への SMS-SUBMIT-REPORT。`0` は SMS-DELIVER と SMS-DELIVER-REPORT、`2` は SMS-STATUS-REPORT。組になる 2 つは、送る向きで見分ける。 |
| `gsm_sms.sms_text contains "Hello"` | そのテキストを含む SMS。GSM 7 ビットのアルファベットでも UCS2 でも使える。 |
| `gsm_sms.tp-da == "12025550102"` | その番号に送った SMS。 |
| `sip.In-Reply-To` | submit report と delivery report。どちらも、答える相手の MESSAGE を示す。 |
| `diameter.cmd.code == 306 \|\| diameter.cmd.code == 307` | Sh のメッセージ（アプリケーション 16777217）。UDR と UDA、PUR と PUA。 |

`scripts/e2e-sms.sh` は、送った SMS のフレームの一覧を `e2e-out/sms-ladder.txt` に書き出します。

## ダッシュボードに送るコピー（HEP）

ノードは送受信した SIP と Diameter のメッセージのコピーを、UDP の 9060 番ポートで待つダッシュボードにも送れます。コピーの形式は HEP（Homer Encapsulation Protocol）のバージョン 3 です。ノードの設定に `hep: {collector: dashboard:9060}` のような `hep` の項目があると、ノードはコピーを送ります。UE シミュレータは `-hep dashboard:9060` を指定すると送ります。どちらも無ければ、何も送りません。

ダッシュボードは呼の経路に入っていないので、ダッシュボードがまだ起動していないなどの理由で `collector` の名前を解決できなくても、ノードは起動します。ノードは警告をログに出し、そのあいだのコピーは捨てます。コピーを送れなかったときは、多くても 5 秒に 1 回、名前を解決し直します。そのため、あとから起動したダッシュボードにもコピーが届きます。

- コピーもポート 9060 宛ての UDP パケットとしてキャプチャのファイルに入ります。Wireshark には HEP を解読する機能が標準では無いため、解読するには [sipcapture/hep-wireshark](https://github.com/sipcapture/hep-wireshark) の Lua プラグインを入れます。SIP と Diameter のメッセージ自体はこれまでどおり解読されます。コピーを表示から外すには、フィルタ `not udp.port == 9060` を使います。
- SIP のコピーには、通信路を流れたとおりのメッセージが入ります。
- Diameter のコピーには、メッセージを JSON にしたものが入り、プロトコルの種類は 0x38「DIAMETER (JSON)」です。HEP3 rev. 37 には、Diameter のメッセージをバイナリのまま運ぶ種類が定義されていないためです。

HEP の符号化では仕様の次の部分を省いています。

| 箇所 | 省略していること | 理由 |
| --- | --- | --- |
| HEP3 rev. 37 の Generic chunk types | IPv6 のアドレス（チャンク 0x0005 と 0x0006）。 | このプロジェクトのノードはすべて IPv4 のアドレスを持つため。 |
| HEP のリポジトリの docs/HEP3-extended-packet-length.md | 65535 オクテットを超えるパケットのための 32 ビットの長さ。 | このプロジェクトの SIP と Diameter のメッセージは、その大きさに遠く及ばないため。 |

## システムを止める

- すべてのプロファイルのノードと UE を止め、イメージは残すときは `docker compose --profile ue --profile sim --profile volte down -v --remove-orphans` を実行します。ほかの手順書も最後に同じコマンドを打つので、次の手順書をビルドし直さずに始められます。
- 手順書をすべて終えたら、ビルドで作ったイメージも削除します。

  ```sh
  docker compose --profile ue --profile sim --profile volte down --rmi all -v --remove-orphans
  ```

キャプチャのファイルは削除するまで `captures/` に残ります。
