---
title: キャプチャの読み方とラボ
footer: ims-practice 勉強会 3. キャプチャとラボ
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: ue, label: UE, role: 端末, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: 最初の中継点, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: 網の入口, at: [1, 0]}
  - {id: scscf, label: S-CSCF, role: 登録とサービス, at: [2, 1]}
  - {id: hss, label: HSS, role: 加入者のデータ, at: [2, 0]}
  - {id: bridge, label: ims0, role: Docker のブリッジ, at: [1, 2]}
  - {id: capture, label: capture, role: tcpdump, at: [0, 2]}
  - {id: pcap, label: pcap, role: captures/ のファイル, at: [0, 3]}
  - {id: lab, label: Lab, role: ラボ。HEP を受ける, at: [2, 2]}
  - {id: ue-a, label: UE 1, role: 発信側, at: [0, 3]}
  - {id: as-o, label: MMTel AS, role: 発信側の AS, at: [1, 3]}
  - {id: as-t, label: MMTel AS, role: 着信側の AS, at: [2, 3]}
  - {id: ue-b, label: UE 2, role: 着信側, at: [3, 3]}
events:
  # ネットワークは 1 つ、ファイルも 1 つ
  - {id: ue-on, rel: つながる, from: ue, to: bridge}
  - {id: pcscf-on, rel: つながる, from: pcscf, to: bridge}
  - {id: scscf-on, rel: つながる, from: scscf, to: bridge}
  - {id: hss-on, rel: つながる, from: hss, to: bridge}
  - {id: listens, rel: 聞く, from: capture, to: bridge}
  - {id: writes, rel: 書く, from: capture, to: pcap}
  # HEP のコピー
  - id: invite
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
  - id: copy-sent
    msg: HEP
    from: pcscf
    to: lab
    proto: UDP 9060
    note: (送った側のコピー)
  - id: copy-received
    msg: HEP
    from: scscf
    to: lab
    proto: UDP 9060
    note: (受け取った側のコピー)
  - id: join
    do: 1 本の矢印にまとめる
    at: lab
    fields:
      - まとめる条件: 送信元、宛先、中身が同じ
      - 別の矢印になるもの: まったく同じ中身の再送
  # ラボが推定するもの
  - id: register
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
  - id: mar
    msg: MAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Session-Id: SIP の Call-ID とは無関係
  - id: link
    do: Cx を SIP に結び付ける
    at: lab
    fields:
      - Cx: 同じノードが 2 秒以内に最後に受け取った、同じ利用者を指す SIP のリクエスト
      - Rx: P-CSCF がそのアドレスから最後に受け取った SDP 付きのメッセージか REGISTER
  # 1 つの通話と、複数のダイアログ
  - {id: dialog-1, rel: ダイアログ 1, from: ue-a, to: as-o, proto: SIP}
  - {id: dialog-2, rel: ダイアログ 2, from: as-o, to: as-t, proto: SIP}
  - {id: dialog-3, rel: ダイアログ 3, from: as-t, to: ue-b, proto: SIP}
  - id: one-call
    do: icid-value で 1 つの通話にする
    at: lab
    fields:
      - icid-value: 3 つのダイアログで同じ
      - Dialogs: 3
```

---

# キャプチャの読み方とラボ

ims-practice 勉強会 第 3 回（キャプチャはどこから来るか、どう絞るか、ラボは何を描くか）

---

[layout: summary]

## この回で分かること

最初の問い。どちらの道具も同じ信号を見せます。ラボがラダー図を描いてくれるのに、なぜキャプチャを開くのでしょうか。

- キャプチャのファイルがどこから来て、何が入っているか。
- 表示フィルタの組み立て方と、どの回のフィルタがどこに書いてあるか。
- Wireshark からラダー図を出す方法と、音声を聞く方法。
- ラボが何を組み立て、何を推定しているか。
- ラボには出て、キャプチャには無いメッセージ。

> 手順書 [信号をキャプチャして Wireshark で開く](../../../docs/guide/ja/capture.md)（`docs/guide/ja/capture.md`）と [ラボで IMS を見る](../../../docs/guide/ja/lab.md)（`docs/guide/ja/lab.md`）
> この回では IMS の仕組みを説明しません。これより先の回は、どれもキャプチャを読み、多くはラボも開きます。

---

## ネットワークは 1 つ、ファイルも 1 つ

```ims-scene
id: one-file
nodes: [ue, pcscf, scscf, hss, bridge, capture, pcap]
lead: ノードはすべて 1 つのブリッジにつながっているので、ノードのあいだのやり取りの全体が 1 つのファイルに入ります。
steps:
  - id: bridge
    title: ノードはすべて 1 つのネットワークにいる
    show: [ue-on, pcscf-on, scscf-on, hss-on]
    focus: [bridge]
    text: 1 つの Docker のネットワーク（172.28.0.0/24）です。図では 4 つだけを描いています。
  - id: listen
    title: capture がブリッジを聞く
    show: [listens]
    focus: [capture, bridge]
    text: キャプチャ用のコンテナが最初に起動し、ノードはそのあとで起動します。ノードが最初に送るメッセージもファイルに入ります。
  - id: file
    title: 起動するたびに新しいファイル
    show: [writes]
    focus: [capture, pcap]
    text: ファイルの名前は起動した時刻です。第 10 回では、3 つのブリッジにそれぞれ capture とファイルができます。
```


```text
            captures/ims-<UTC の起動時刻>.pcap
                        ^
                        |
                 capture。ブリッジ ims0 の tcpdump
                        ^
                        |  1 つの Docker のネットワーク（172.28.0.0/24）
  dns  hss  icscf  scscf  pcscf  agw  lab  ipsmgw  mmtel  epc  mrfc  mrfp  UE
```

- ノードはすべて 1 つの Docker のネットワークで通信します。そのため、ノード間のやり取りの全体が 1 つのファイルに入ります。
- キャプチャ用のコンテナが最初に起動し、ノードはそのあとで起動します。ノードが最初に送るメッセージもファイルに入ります。
- このコンテナは起動するたびに新しいファイルを作ります。名前は起動した時刻です。
- 第 10 回では、2 つめの網と、この網とのあいだのブリッジが加わります。3 つのブリッジそれぞれに、キャプチャ用のコンテナとファイルが 1 つずつできます。

```sh
docker compose up -d --build
```

> 手順書 [「システムを起動する」](../../../docs/guide/ja/capture.md#システムを起動する)と [「キャプチャのファイルを開く」](../../../docs/guide/ja/capture.md#キャプチャのファイルを開く)

---

## Wireshark で開く

1. **File > Open** で、`captures/` のいちばん新しいファイルを開きます。
2. 表示フィルタの欄に、その回のフィルタを入力して Enter キーを押します。
3. ラダー図を見るには、**Statistics > Flow Graph** を選び、**Limit to display filter** に印を付けます。

- キャプチャの途中でもファイルを開けます。そのあとに届いたパケットを見るには **View > Reload** を選びます。
- パケットを発生と同時に見るには、キャプチャを Wireshark に流し込みます。Git Bash では `scripts/capture-live.sh`、PowerShell 7.4 以降では `scripts/capture-live.ps1` です。
- ファイルは削除するまで `captures/` に残ります。その回のキャプチャを、あとから読み直せます。

> 手順書 [「キャプチャのファイルを開く」](../../../docs/guide/ja/capture.md#キャプチャのファイルを開く)と [「通信をライブで見る」](../../../docs/guide/ja/capture.md#通信をライブで見る)

---

## 表示フィルタ

登録の回で使うフィルタです。

```text
sip || diameter.cmd.code >= 300 || dns.qry.type == 35 || dns.qry.type == 33
```

| 書き方 | 残るもの |
| --- | --- |
| `sip`、`diameter`、`rtp` | そのプロトコルのパケットのすべて。 |
| `diameter.cmd.code == 302` | フィールドを値と比べる。 |
| `diameter.cmd.code in {302, 306, 307}` | 1 つのフィールドの複数の値。 |
| `ip.addr == 172.28.0.70` | 1 つのノードの両方向。`ip.src` と `ip.dst` は片方向。 |
| `gsm_sms.sms_text contains "Hello"` | そのバイト列を含むフィールド。 |

- `&&` と `||` がこれらをつなぎ、`not` か `!` が裏返します。上のフィルタは登録の SIP、Cx、DNS の問い合わせを残し、Diameter のウォッチドッグを隠します。
- 各手順書の「キャプチャを開く」に、その手順のフィルタがあります。いくつかの手順書には、もっと細かく絞るフィルタの表もあり、何が見えるかが書いてあります。

> 手順書 [登録の手順書「キャプチャを開く」](../../../docs/guide/ja/registration.md#キャプチャを開く)と [キャプチャの手順書「SMS over IP の表示フィルタ」](../../../docs/guide/ja/capture.md#sms-over-ip-の表示フィルタ)

---

## UE が登録する前からファイルに入っているもの

フィルタは `diameter` です。

| 見るもの | 何が見えるか |
| --- | --- |
| CER と CEA | 3868 番への 7 本の接続。Diameter で問い合わせる 6 つのノードが開く。IP-SM-GW は HSS と SC への 2 本を開く。 |
| その中のアプリケーション | I-CSCF と S-CSCF は Cx、2 つの AS は Sh、IP-SM-GW から SC へは SGd、SC から HSS へは S6c、P-CSCF は Rx。 |
| DWR と DWA | 約 30 秒なにも受け取らなかった側が、相手が動いているかを確かめる。 |
| DPR と DPA | 止まるノードが、接続を閉じる前に、切断することを相手に伝える。 |

```sh
docker compose stop scscf
```

- S-CSCF が Disconnect-Cause `REBOOTING` を付けた DPR を送り、HSS が DPA で応えます。`docker compose start scscf` で起動し直すと、新しい CER と CEA が続きます。

> 手順書 [「ノードを起動すると見えるもの」](../../../docs/guide/ja/capture.md#ノードを起動すると見えるもの)。7 本の接続の一覧もここにあります。RFC 6733。RFC 3539 3.4.1。

---

## ラダー図と音声

- **Statistics > Flow Graph** で **Limit to display filter** に印を付けると、フィルタで残ったメッセージがラダー図になります。
- **Telephony > VoIP Calls** は、SIP のダイアログを 1 つずつ通話として並べます。**Flow Sequence** でそのダイアログのラダー図を描き、**Play Streams** で両方向の音声を聞けます。
- **Telephony > RTP > RTP Streams** には、MRFP のものなど、ストリームそのものが並びます。
- RTP は SDP でしか知らされないポートを使います。同じキャプチャに通話の SIP も入っていれば、Wireshark はそのポートを見つけられます。この構成のキャプチャには入っています。それでもただの UDP と表示されるときは、**Analyze > Enabled Protocols > RTP > rtp_udp**（RTP の推定）を有効にします。

> 手順書 [通話の手順書「キャプチャを開く」](../../../docs/guide/ja/call.md#キャプチャを開く)と [MRF の手順書「キャプチャを開く」](../../../docs/guide/ja/mrf.md#キャプチャを開く)

---

## ファイルの中のコピー

```ims-scene
id: copies
nodes: [pcscf, scscf, lab]
lead: ラボが知っているのは、ノードが送るコピーだけです。
steps:
  - id: message
    title: ノードからノードへのメッセージ
    show: [invite]
    text: キャプチャには、このメッセージが 1 つ入ります。
  - id: sent
    title: 送った側がコピーを送る
    show: [copy-sent]
    text: 設定に `hep` の項目があるノードは、送受信した SIP と Diameter のコピーを UDP の 9060 番でラボに送ります。
  - id: received
    title: 受け取った側もコピーを送る
    show: [copy-received]
    checkpoint: true
    text: コピーも 9060 番あての UDP としてキャプチャに入ります。`not udp.port == 9060` で隠します。
```


- ノードは、送受信した SIP と Diameter のメッセージのコピーを、UDP の 9060 番で待つラボに送れます。コピーの形式は HEP のバージョン 3 です。
- ノードは、設定に `hep` の項目があるとコピーを送ります。UE シミュレータは `-hep lab:9060` を付けると送ります。どちらも無ければ、何も送りません。
- コピーも 9060 番あての UDP のパケットとしてキャプチャに入ります。Wireshark には HEP を解読する機能が標準では無いので、`not udp.port == 9060` で隠します。
- ラボが知っているのは、このコピーだけです。ノードに状態を問い合わせることはなく、ノードもラボの応答を待ちません。

> 手順書 [「ラボに送るコピー（HEP）」](../../../docs/guide/ja/capture.md#ラボに送るコピーhep)

---

## ラボを開く

```sh
docker compose up -d --build
```

- <http://localhost:8080> を開きます。ページがラボのサーバーから更新を受け取っているあいだ、題の横の点が `live` と表示します。
- ラボはホストのループバックアドレスにだけ公開します。パスワードを聞かないので、たどり着いた人は誰でも、HSS が持っているものを読み、UE を操作し、UE をアクセス網から切り離せます。
- ラボは IMS の一部ではなく、呼の経路にも入っていません。ラボが無くても、ノードは起動して動きます。

> 手順書 [「ラボを開く」](../../../docs/guide/ja/lab.md#ラボを開く)

---

## 各パネルの内容

| パネル | 表示する内容 |
| --- | --- |
| UE simulators | UE シミュレータごとのカード。識別子、登録しているかどうか、今の通話、送受信したショートメッセージ、付加サービスの設定。ボタンとフォームで UE を操作する。 |
| Calls | 通話ごとの行。発信者、着信者、状態、最終応答のコード、ダイアログの数。 |
| Ladder diagram | 選んだ通話か登録の、SIP と Cx のメッセージ。ノードごとに縦の線を引く。 |
| Registrations | 登録を確認できた公開ユーザー識別子ごとの行。Contact、有効期限、S-CSCF。 |
| Subscribers (HSS) | HSS にある加入の一覧。このパネルは加入を変えない。加入を作るのは運用者で、OAM から行う。 |
| Access network (EPC) | EPC のスタブに接続した UE ごとの行。アドレス、EPS bearer、通話の Rx セッション。 |
| Faults | 障害注入のエージェントが持つ障害の一覧と、注入するフォーム。エージェントを起動するまで使えない。 |

- ラダー図の矢印を選ぶと、メッセージの全文と、それを定めた仕様の条項が出ます。
- ラダー図の下の **Copy as text** を押すと、手順書のラダー図と同じ形式の等幅のテキストにして書き出します。

> 手順書 [「各パネルの内容」](../../../docs/guide/ja/lab.md#各パネルの内容)と [「ラダー図をテキストでコピーする」](../../../docs/guide/ja/lab.md#ラダー図をテキストでコピーする)

---

## ラダー図の組み立て方。2 つのコピーを 1 本の矢印に

```ims-scene
id: one-arrow
nodes: [pcscf, scscf, lab]
lead: 1 本の矢印が、ネットワーク上の 1 つのメッセージにあたります。
steps:
  - id: two
    title: 1 つのメッセージに、コピーが 2 つ
    only: [invite, copy-sent, copy-received]
    active: copy-received
    text: 送った側と受け取った側の両方から届きます。
  - id: join
    title: ラボが 1 本の矢印にまとめる
    show: [join]
    focus: [lab]
    text: 送信元、宛先、中身が同じなら 1 本です。まったく同じ中身の再送は、別の矢印になります。
```


```text
  送信側のコピー  ---+
                     +--> 送信元、宛先、中身が同じなら 1 本の矢印
  受信側のコピー  ---+
```

- ノードからノードへのメッセージは、送った側と受け取った側の両方から届きます。ラボはこの 2 つをまとめるので、1 本の矢印がネットワーク上の 1 つのメッセージにあたります。
- まったく同じ中身の再送は、別の矢印になります。
- 例外は 3 つのパネルです。UE のカード、Access network、Faults は、UE シミュレータ、EPC のスタブ、障害注入のエージェントがそれぞれの API で返す状態です。そのため、カードと Registrations パネルの表示が一時的に食い違うことがあります。

> 手順書 [「ラダー図の組み立て方」](../../../docs/guide/ja/lab.md#ラダー図の組み立て方)と [「各パネルの内容」](../../../docs/guide/ja/lab.md#各パネルの内容)

---

## ラボが推定するもの。Cx と Rx

```ims-scene
id: infer
nodes: [icscf, scscf, hss, lab]
lead: Cx のメッセージには、どの SIP のリクエストのために送ったかが書かれていません。ラボは手順の決まりで結び付けます。
steps:
  - id: register
    title: S-CSCF が REGISTER を受け取る
    show: [register]
    text: SIP のリクエストには Call-ID があります。
  - id: mar
    title: S-CSCF が MAR を送る
    show: [mar]
    text: Diameter の Session-Id は、SIP の Call-ID と無関係です。
  - id: link
    title: ラボが結び付ける
    show: [link]
    focus: [lab]
    text: |
      結び付きはメッセージには書かれていません。ラボが推定したものです。
      矢印を選ぶと、詳細の **Link to this ladder** に出ます。
```


- Cx のメッセージには、どの SIP のリクエストのために送ったかを示すものがありません。Diameter の Session-Id と SIP の Call-ID は無関係です。
- そこでラボは手順の決まりで結び付けます。Cx のリクエストは、同じノードが 2 秒以内に最後に受け取った SIP のリクエストのうち、同じ利用者を指すものに属するとします。
- Rx のセッションも同じように結び付けます（TS 29.214 A.1、A.4）。最初の AAR が UE を示すのはアドレスだけなので、P-CSCF がそのアドレスから最後に受け取った SDP 付きのメッセージか REGISTER に属するとします。
- その矢印を選ぶと、詳細の **Link to this ladder** に、ラボが推定した結び付きで、メッセージには書かれていないことが出ます。

> 手順書 [「ラダー図の組み立て方」](../../../docs/guide/ja/lab.md#ラダー図の組み立て方)。決まりは `internal/lab/causes.go` にあります。

---

## 1 つの通話と、複数のダイアログ

```ims-scene
id: dialogs
nodes: [ue-a, as-o, as-t, ue-b, lab]
lead: MMTel AS は routeing B2BUA なので、2 人のあいだの通話が 3 つのダイアログになります。途中の CSCF は省いて描いています。
steps:
  - id: first
    title: UE 1 と発信側の AS のダイアログ
    show: [dialog-1]
    text: AS は S-CSCF から届いた INVITE に自分で答えます。
  - id: more
    title: AS が自分の Call-ID で新しい INVITE を送る
    show: [dialog-2, dialog-3]
    checkpoint: true
    text: 着信側の AS も同じようにします。ダイアログは 3 つになります。
  - id: icid
    title: ラボは icid-value で 1 つの通話にまとめる
    show: [one-call]
    focus: [lab]
    text: |
      網の中では、どのダイアログにも発信側の P-CSCF が入れた同じ icid-value があります。
      ラボは Dialogs の列に数を出し、1 つのラダー図にすべてを描きます。
```


```text
  UE 1 --- ダイアログ 1 --- MMTel AS --- 2 --- MMTel AS --- 3 --- UE 2
            3 つとも P-Charging-Vector の icid-value が同じ
```

- MMTel AS は routeing B2BUA です（TS 24.229 5.7.5）。S-CSCF から届いた INVITE に自分で答え、自分の Call-ID を付けた新しい INVITE を送ります。そのため、2 人のあいだの通話が 3 つのダイアログになります。
- 網の中では、どのダイアログにも同じ icid-value が入っています。発信側の P-CSCF が入れ、それぞれの AS が残した値です（TS 24.229 5.2.6.3.3 step 7、5.7.5.1）。P-CSCF は、UE に送るものからこのヘッダを消します。
- ラボは、icid-value が同じダイアログを 1 つの通話として表示し、Dialogs の列に数を出し、1 つのラダー図にすべてを描きます。

> 手順書 [「ラダー図の組み立て方」](../../../docs/guide/ja/lab.md#ラダー図の組み立て方)

---

## キャプチャに無い、ただ 1 つのメッセージ

```text
  S-CSCF            ラボのラダー図              pcap
    |
    +---+  INVITE   同じ縦の線に戻る矢印        何も無い。メッセージが
    |<--+                                       コンテナの外に出ないため
    |
```

- ノードが自分自身にメッセージを送ることがあります。S-CSCF が発信側と着信側の両方を受け持つときです。
- ラボには表示されて pcap には無いメッセージは、これだけです。ラダー図のほかのメッセージは、キャプチャにも入っています。
- 逆に、どのラダー図にも描かれないものがキャプチャにはあります。登録中の加入者を削除したときに HSS が送る RTR がそうです。HSS は SIP のリクエストを受けて送るわけではないので、ラボはこれをどこにも結び付けません。

> 手順書 [「ラダー図の組み立て方」](../../../docs/guide/ja/lab.md#ラダー図の組み立て方)と [「網が登録を取り消す」](../../../docs/guide/ja/lab.md#網が登録を取り消す)

---

## UE シミュレータを操作する

```sh
docker compose --profile phones up -d --build
```

1. `phone2` のカードで、着信の応答方針を `manual` にします。応答するまで鳴り続けます。
2. `phone1` のカードで **Call** を押します。`phone2` のカードに着信が出るので、**Answer** を押します。
3. 通話の横の **ladder** を選びます。通話の最中でもラダー図を描きます。

- `phone1`、`phone2`、`phone3` が使う加入者は 1、2、3 で、`ue`、`ue2`、`ue3` と同じです。2 つのプロファイルは、どちらか一方だけを動かします。
- ラボの HTTP の API と UE シミュレータの制御用の API は、どの仕様にもなく、このプロジェクト独自のものです。ラダー図が描くものは、仕様どおりの IMS の信号です。

> 手順書 [「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)と [「このプロジェクト独自の API」](../../../docs/guide/ja/lab.md#このプロジェクト独自の-api)

---

[layout: exercise]

## 振り返り

1. キャプチャのファイルはどこから来ますか。どのノードの最初のメッセージも入っているのはなぜですか。
2. 1 つのノードだけを残すフィルタと、ラボへのコピーを隠すフィルタは、それぞれどう書きますか。
3. Cx のリクエストと REGISTER が同じラダー図にあります。この 2 つを結び付けたのは何ですか。
4. MMTel AS を通る通話には Call-ID が 3 つあります。ラボがこれを 1 つの通話として描くのはなぜですか。
5. ラボには出てキャプチャには無いメッセージは何ですか。それはなぜですか。

---

[layout: answers]

## 振り返りの答え

1. キャプチャ用のコンテナからです。Docker のブリッジで tcpdump を動かしています。ノードより先に起動します。
2. 1 つのノードの両方向は `ip.addr == 172.28.0.70`、コピーを隠すのは `not udp.port == 9060` です。
3. 手順の決まりです。同じノードが、同じ利用者について、2 秒以内に受け取ったことで結び付けます。ラボが推定した結び付きで、そのことは矢印の詳細に出ます。
4. P-Charging-Vector の icid-value が同じだからです。発信側の P-CSCF が入れ、それぞれの AS が残した値です。
5. ノードが自分自身に送るメッセージです。たとえば、S-CSCF が通話の両側を受け持つときに自分に送る INVITE です。コンテナの外に出ないので、ブリッジでは捕らえられません。

---

## 次回

- 次は登録です。キャプチャで読む最初の流れで、ラボで開く最初のラダー図でもあります。
- その前に、一度システムを起動し、`captures/` のいちばん新しいファイルをフィルタ `diameter` で開いて、7 つの CER を見つけておいてください。

```sh
docker compose up -d --build
```

> 次回の手順書は [UE が IMS に登録する流れを追う](../../../docs/guide/ja/registration.md) です。システムの止め方は [「システムを止める」](../../../docs/guide/ja/capture.md#システムを止める)にあります。
