---
title: 境界でつながる 2 つの IMS 網
footer: ims-practice 勉強会 10. 2 つの網
lang: ja
---

# 境界でつながる 2 つの IMS 網

ims-practice 勉強会 第 10 回（IBCF と TrGW、事業者どうしの取り決め、網をまたぐ通話とショートメッセージ）

---

[layout: summary]

## この回で分かること

最初の問い。通話がほかの事業者の網へ出ていきます。相手の事業者には、こちらの網の何が見えるのでしょうか。

- 境界に立つノード。SIP を受け持つ IBCF と、メディアを受け持つ TrGW。
- 2 つの網をつなぐ参照点と、IBCF を自網につなぐ参照点。
- 境界が B2BUA である理由と、それによって隠れるもの。
- 事業者どうしの取り決めが決めることと、2 つの取り決めが食い違うと何が失敗するか。
- 通話、ショートメッセージ、どの相手網でもない送信元からの要求を、3 つのキャプチャのどこで見るか。

> 手順書 [IMS 網どうしを相互接続する](../../../docs/guide/ja/interconnect.md)（`docs/guide/ja/interconnect.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.229 V20.1.0、TS 29.165 V20.0.0、TS 23.204 V19.1.0、TS 24.341 V19.0.0、TS 23.040 V20.0.0、TS 32.409 V19.0.0、TS 24.147 V19.0.0、RFC 3261、RFC 3312、RFC 3966 などです。

---

## 2 つの網

```text
      網 A（ims0）            網のあいだ（nni0）         網 B（ims1）
     172.28.0.0/24              172.28.2.0/24           172.28.1.0/24

  S-CSCF --- IBCF-A (.90) --- .10 -- Ici -- .20 --- IBCF-B (.90) --- I-CSCF
     agw --- TrGW-A (.91) --- .11 -- Izi -- .21 --- TrGW-B (.91) --- UE B1
```

- 網 A は `compose.yaml` の網で、これまでの回で使ってきた網です。網 B は別の事業者の 2 つ目の IMS で、同じイメージを別のドメイン、別の番号、別の加入者で動かします。
- 網のあいだのブリッジには IBCF と TrGW しかつながっていません。網 A の中のノードは、網 B の中のノードに直接は届きません。
- 網 B は、シナリオに要る最小限のノードだけです。MMTel AS、EPC と PCRF、P-CSCF のメディア中継ノード、ラボはありません。
- キャプチャはブリッジごとに 1 つです。網 A は `captures/`、網 B は `captures/b/`、網のあいだは `captures/nni/` にあります。

> 手順書 [「2 つの網」](../../../docs/guide/ja/interconnect.md#2-つの網)

---

## 両方の網のノード

| | 網 A | 網 B |
| --- | --- | --- |
| ドメイン | `ims.mnc001.mcc001.3gppnetwork.org` | `ims.mnc002.mcc001.3gppnetwork.org` |
| 発信する UE | UE A1、172.28.0.100、`+1-202-555-0101` | UE B1、172.28.1.100、`+1-202-555-0151` |
| IBCF | 網の中は 172.28.0.90、網のあいだは 172.28.2.10 | 網の中は 172.28.1.90、網のあいだは 172.28.2.20 |
| TrGW | 網の中は 172.28.0.91、網のあいだは 172.28.2.11 | 網の中は 172.28.1.91、網のあいだは 172.28.2.21 |
| ショートメッセージの SC | `+12025550100` | `+12025550150` |

- `deploy/interconnect/compose.yaml` が、網 B と網のあいだを `compose.yaml` に重ねて足します。`compose.yaml` は変わりません。網 B のサービスの名前は `-b` で終わり、`scscf-b` は網 B の、`scscf` は網 A の S-CSCF です。
- ノードの設定ファイルは `deploy/interconnect/a` と `deploy/interconnect/b` にあります。どちらの網の番号も、北米番号計画が架空の番号に取ってある範囲のもので、鍵も試験用の値です。

> 手順書 [「2 つの網」](../../../docs/guide/ja/interconnect.md#2-つの網)

---

## IBCF は B2BUA

- TS 29.165 は、IMS 網どうしのあいだのインタフェースを II-NNI と呼びます。そこに立つノードが IBCF で、側ごとに UA を 1 つ持ちます。内側の UA は自網のアドレスで、外側の UA は網のあいだのアドレスで待ちます。
- 内側に届いた要求は網から出ていくので、IBCF はその**出口**です（TS 24.229 5.10.2）。外側に届いた要求は網に入ってくるので、IBCF はその**入口**です（5.10.3）。
- IBCF は、届いた要求を送ってきた側とのダイアログで受け止め、もう一方の側とのダイアログで新しい要求を送ります（TS 24.229 5.10.5、TS 29.165 5.2.1）。ダイアログごとに Call-ID、tag、CSeq、Via、Contact が別々です。
- 一方の網の中でメッセージを運ぶための値は、相手網とのダイアログに入りません。B2BUA は、こうして自網の構成を隠します。
- TrGW は、ここでも役割 `agw` のメディア中継ノードです。ターミネーション `core` は自網を、`access` は相手網の TrGW を向きます。

> 手順書 [「IBCF と TrGW の仕事」](../../../docs/guide/ja/interconnect.md#ibcf-と-trgw-の仕事)

---

## 境界の 4 つの参照点

| 参照点 | どこの間か | このプロジェクトでは |
| --- | --- | --- |
| Mx | CSCF と自網の IBCF | SIP。S-CSCF の設定 `ibcf` が指定し、Route の先頭に載ります。 |
| Ici | 2 つの網の IBCF | 網のあいだの SIP。SDP 付きの INVITE は 1300 バイトを超えるので TCP で送ります（RFC 3261 18.1.1）。 |
| Izi | 2 つの網の TrGW | 境界を越える RTP。 |
| Ix | IBCF と自網の TrGW | 標準では H.248（TS 29.238）。ここでは P-CSCF が中継ノードに使うのと同じ HTTP の制御 API です。 |

- Ici と Izi が II-NNI です（TS 29.165 5.1）。Mx と Ix は 1 つの網の中にあります（TS 29.165 3.2）。
- 第 2 回は、この 4 つを網の地図の上に描きました。この回は、そこを流れるものを読みます。

> 手順書 [「IBCF と TrGW の仕事」](../../../docs/guide/ja/interconnect.md#ibcf-と-trgw-の仕事)

---

## 実行する

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml up -d --build

# UE B1 が着信を待つ。UE シミュレータには網 B の設定をフラグで渡す
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue-b1 answer \
  -domain ims.mnc002.mcc001.3gppnetwork.org \
  -impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org \
  -impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org \
  -password test-password-b1 \
  -pcscf 172.28.1.40:5060 -pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060

# UE A1 がその番号にかける。網 A の設定はこれまでどおり
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue call tel:+12025550151
```

- 網 B の `ue sms` には、フラグがもう 1 つ要ります。網 B の SC の番号を渡す `-sc` です。
- `scripts/e2e-interconnect.sh` は、この回のすべてを実行して 3 つのキャプチャで確かめます。

> 手順書 [「動かす」](../../../docs/guide/ja/interconnect.md#動かす)

---

## 3 つのキャプチャを開く

| キャプチャ | 入っているもの |
| --- | --- |
| `captures/ims-*.pcap` | 網 A。ENUM、S-CSCF から IBCF-A への INVITE、IBCF-A が渡してくる網 B のメッセージ。 |
| `captures/nni/ims-*.pcap` | 網のあいだ。2 つの IBCF と 2 つの TrGW だけです。 |
| `captures/b/ims-*.pcap` | 網 B。IBCF-B から I-CSCF へ渡ったあとの INVITE と、TrGW-B の RTP。 |

- キャプチャ用のコンテナはブリッジごとに 1 つです。3 つとも開いてください。同じ通話が、どのファイルにも別々の Call-ID で入っています。
- これまでの回のフィルタは、どのファイルでもそのまま使えます。`sip.Method == "INVITE" && count(sip.Via) == 1` は、網のあいだから IBCF の INVITE を選び出します。
- 網のあいだのキャプチャで **Statistics > Flow Graph** を開くと、2 つの IBCF のあいだのシーケンス図になります。

> 手順書 [「各キャプチャに見えるもの」](../../../docs/guide/ja/interconnect.md#各キャプチャに見えるもの)

---

## 網 A から網 B への通話

```ladder
UE A1, S-CSCF, DNS-A, IBCF-A, IBCF-B, I-CSCF, UE B1
UE A1 -> S-CSCF: INVITE tel:+12025550151。P-CSCF と MMTel AS を通る // 1
S-CSCF <-> DNS-A: ENUM。網 B のドメインの SIP URI が返る // 2
S-CSCF -> IBCF-A: INVITE。Route の先頭に IBCF // 3
IBCF-A -> IBCF-B: 網のあいだの INVITE。IBCF-A 自身のダイアログ // 4
IBCF-B -> I-CSCF: INVITE。番号は SIP URI のまま // 5
I-CSCF -> UE B1: INVITE tel:+12025550151。S-CSCF と P-CSCF を通る // 6
UE A1 <-- S-CSCF <-- IBCF-A <-- IBCF-B <-- I-CSCF <-- UE B1: answer を載せた 183 // 7
```

- S-CSCF は、AS がもう残っていない段階で番号を ENUM で変換し（TS 24.229 5.4.3.2 step 10）、自網のドメインでない宛先を自網の IBCF へ送ります（step 11）。
- 網 B の HSS は UE B1 を tel URI で知っているので、I-CSCF は LIR の前に Request-URI を tel URI に置き換えます（TS 24.229 5.3.2.1 item 1 b）。
- PRACK、ACK、BYE はダイアログごとに別々です。RTP は UE A1 から網 A のメディア中継ノード、TrGW-A、TrGW-B を通って UE B1 に届きます。
- どちらの UE も相手網を知りません。UE A1 の相手は 172.28.0.50、UE B1 の相手は 172.28.1.91 です。

> 手順書 [「網 A から網 B への通話」](../../../docs/guide/ja/interconnect.md#網-a-から網-b-への通話)

---

## 境界が要求に加える変更

一方のダイアログから他方へ何を写すかは、相手網との取り決め（相互接続プロファイル）が決めます。

| 何をするか | 参照 |
| --- | --- |
| 信頼ドメインの外に出してはいけないヘッダを消す | TS 24.229 4.4 |
| メソッド、option tag、ヘッダを調べる | TS 24.229 5.10.6 |
| 番号を取り決めの形式に直す | TS 29.165 8.1 |
| 相手網が受けないコーデックを SDP から外す | TS 29.165 7.1 |
| メディアを TrGW に通し、そのアドレスを SDP に書く | TS 24.229 6.7.1.4.1 |

- 2 つの事業者には取り決めがあるので、互いに相手の信頼ドメインに入っています（`trusted: true`、TS 24.229 4.4.1）。P-Asserted-Identity は境界を越え、UE B1 には誰からの着信かが分かります（4.4.2）。
- P-Charging-Function-Addresses は越えません（5.10.2.2 step 9）。`trusted: false` にすると、UE B1 には発信者の番号が付かないまま着信します。

> 手順書 [「IBCF と TrGW の仕事」](../../../docs/guide/ja/interconnect.md#ibcf-と-trgw-の仕事)と [「信頼ドメイン」](../../../docs/guide/ja/interconnect.md#信頼ドメイン)

---

## 3 つのダイアログ、3 つの Call-ID

網のあいだの INVITE を短くしたものです。

```text
INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0
Via: SIP/2.0/TCP ibcf.nni.mnc001.example.net:5060;branch=z9hG4bKW6NI6...;rport
Max-Forwards: 65
Route: <sip:ibcf.nni.mnc002.example.net:5060;lr>
From: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;tag=5MWO4Z6R4C
Call-ID: FMZQJ3BZL2YI4ILP6CEIPAUT5Z
Contact: <sip:ibcf.nni.mnc001.example.net:5060>
```

- **Via** は IBCF-A の 1 つだけです。網 A のノードの Via は IBCF-A に届いた INVITE に付いていて、その INVITE は IBCF-A で終わっています。
- **Record-Route** はありません。このダイアログの相手端は IBCF-A なので、網 B からの要求は Contact に届きます。
- **Call-ID** は IBCF-A のものです。IBCF-A に届いた INVITE、IBCF-B が送り出した INVITE と見比べてください。1 つの通話が、3 つのキャプチャでそれぞれ別の Call-ID を持ちます。
- **Max-Forwards** は、IBCF-A がプロキシと同じく 1 つ減らしています（RFC 3261 16.6 step 3）。2 つの網が要求を回し合っても、いつかは終わるようにするためです。

> 手順書 [「キャプチャで見る 3 つのダイアログ」](../../../docs/guide/ja/interconnect.md#キャプチャで見る-3-つのダイアログ)

---

## 網の構成を隠す。キャプチャに出てはいけないもの

| キャプチャ | 出てはいけないもの |
| --- | --- |
| 網 A | 172.28.1.0/24 のアドレスと、網 B のノードのホスト名。 |
| 網のあいだ | どちらの網の内側のアドレスも、そのノードのホスト名も、網 A の IMSI から作った識別子も、Record-Route も。 |
| 網 B | 172.28.0.0/24 のアドレス、網 A のノードのホスト名、網 A の IMSI から作った識別子。 |

- スクリプトは、アドレスを IP ヘッダとフレームの中身の両方で探し、ノードの名前を相手網のドメインの手前で探します。どれも見つかりません。
- 自網の名前のうち、相手網に出てよいものもあります。ドメインそのものです。From と P-Asserted-Identity の識別子に入っています。相手網は、折り返し電話をかけるために利用者の番号が要ります。
- 番号とドメインからは、中のノードのことも IMSI のことも分かりません。

> 手順書 [「各キャプチャに見えるもの」](../../../docs/guide/ja/interconnect.md#各キャプチャに見えるもの)

---

## 事業者どうしの取り決め

| 項目 | 決めること |
| --- | --- |
| `name`、`domain` | 相手網と、番号が SIP URI で出ていくときのホスト（TS 29.165 8.1）。 |
| `trusted` | 相手網が信頼ドメインに入るかどうか（TS 24.229 4.4）。 |
| `transports`、`methods` | 送るときのトランスポートと、取り決めで認めた任意のメソッド（MESSAGE など。TS 29.165 6.2、Table 6.1）。 |
| `option_tags` | option tag ごとに `pass`、`remove`、`reject`。`reject` なら、その拡張を要求する要求に 420 を返します。 |
| `headers` | 事業者が決めた、ヘッダを消す規則と書き換える規則（TS 24.229 5.10.6.2）。 |
| `number_format`、`local_numbers` | 番号の形式と、ローカル番号をグローバル番号にする方法（TS 29.165 8.1）。 |
| `codecs` | 相手網が受けるペイロード形式（TS 29.165 7.1）。 |

- `profiles/network-b.yaml` が網 A の網 B との取り決め、`network-a.yaml` が網 B の網 A との取り決めです。TS 29.165 Annex C は、こうした選択を 2 つの事業者が埋める option item として並べています。
- `IBCF_A_PROFILE` と `IBCF_B_PROFILE` で別のファイルを選べます。Compose はその IBCF を選び直したファイルで起動し直します。

> 手順書 [「プロファイル」](../../../docs/guide/ja/interconnect.md#プロファイル)

---

## 2 つの取り決めが食い違うとき

| 食い違い | 起きること | 合わせた取り決め |
| --- | --- | --- |
| 網 A の取り決めに MESSAGE が無い | IBCF-A がショートメッセージに 405（Method Not Allowed）を返します。網 A の SC は TP-ST 98、「SME から応答が無い」を報告します。 | `network-b.yaml`。TP-ST 1 になります |
| 網 B が AMR-WB しか受けない | IBCF-B が UE A1 の PCMU の offer から音声のコーデックを全部外し、488（Not Acceptable Here）を返します。 | PCMU を挙げた `network-a.yaml` |
| ローカル番号がそのまま越える | 網 B の取り決めはグローバル番号しか認めないので、IBCF-B がローカルポリシーで INVITE を 404（Not Found）で断ります。 | `network-b-global-numbers.yaml`。IBCF-A が出す前にグローバル番号にします |

- 失敗させているのは UE ではなく、取り決めに従った IBCF です。網のあいだの INVITE と、網 B に届いた INVITE を見比べてください。
- スクリプトは食い違いを 1 つずつ試し、そのあとで合う取り決めに替えます。キャプチャには、失敗した通話と通った通話が続けて残ります。

> 手順書 [「プロファイルが食い違うとき」](../../../docs/guide/ja/interconnect.md#プロファイルが食い違うとき)。TS 29.165 7.1、Table 6.1。TS 24.229 5.10.1。

---

## 相手網でない送信元からの要求

```text
172.28.2.99 のホスト        IBCF-A ║ 網 A
  |  INVITE                    |   ║
  |--------------------------->| x ║   （捨てる。172.28.2.99 から送る相手網は無い）
  |  MESSAGE                   |   ║
  |--------------------------->| x ║
  |                            |   ║
  |  （何も返ってこない）      |   ║   （網 A には何も入らない）
```

- IBCF-A が外側で受けるのは、網 B のプロファイルが挙げたアドレスからの要求だけです（`ibcf.yaml` の `sources`）。
- どちらの要求も、何も返さずに捨てます。100（Trying）もエラー応答も返しません。送信元には網のことが何も分かりません。そこで SIP のノードが待っていることさえ分かりません。
- SIP のスタックは、自分の持っているトランザクションに属さない要求を、扱う前に毎回 IBCF に尋ねます（`IBCF.Screen`）。どのメソッドでも、再送でも同じです。
- 取り決めの中にいる相手網には応答を返します。取り決めに無いメソッドへの 405 がその例です。TS 24.229 5.10.1 は、IBCF がローカルポリシーで要求を 4xx で断ることを認めています。

> 手順書 [「相手網でない送信元からの要求」](../../../docs/guide/ja/interconnect.md#相手網でない送信元からの要求)

---

## 網をまたぐショートメッセージ

```ladder
UE A1, IP-SM-GW-A, IBCF-A, IBCF-B, IP-SM-GW-B, UE B1
UE A1 -> IP-SM-GW-A: SMS-SUBMIT を載せた MESSAGE // 1
IP-SM-GW-A -> IBCF-A: SMS-DELIVER を載せた MESSAGE。差出人は SC の番号 // 3
IBCF-A -> IBCF-B: 網のあいだの MESSAGE
IBCF-B -> IP-SM-GW-B: MESSAGE。I-CSCF と S-CSCF を通る // 4
IP-SM-GW-A <-- IBCF-A <-- IBCF-B <-- IP-SM-GW-B: 202 // 5
UE A1 <-- IP-SM-GW-A: MESSAGE。TP-ST 1 の status report
IP-SM-GW-B -> UE B1: SMS-DELIVER を載せた MESSAGE // 6
```

- SC のスタブは網 A の HSS に番号を尋ねて「利用者が分からない」と返され、設定に `ibcf` があると、次は ENUM で網 B のドメインに番号を見つけます。どちらのプロファイルも MESSAGE を認めています。
- 境界を越える MESSAGE の差出人は、番号で表した SC です。網 A のホスト名は越えません。配送報告は戻らず、網 A の SC は網 B が受け取った時点で役目を終えます。TP-ST 1 は、転送はしたが配送を確かめられないという意味です（TS 23.040 9.2.3.15）。
- 網 B では、着信側のフィルタ条件がその利用者の IP-SM-GW に MESSAGE を渡し（TS 23.204 6.4a step 2）、IP-SM-GW が RP-OA に網 A の SC を残したまま配送します。

> 手順書 [「網をまたぐショートメッセージ」](../../../docs/guide/ja/interconnect.md#網をまたぐショートメッセージ)

---

## 境界を越える会議

- 置き換えない Contact もあります。TS 24.229 5.10.5 は、GRUU の Contact と、その URI で相手に何ができるかを示す feature tag の付いた Contact を、届いたまま通すとしています。挙げている例は、会議のフォーカスを示す `isfocus` です。
- その Contact の URI は会議の URI です。参加者はこれを控えて購読し、会議に戻るときにも使います（TS 24.147 5.3.1.3.2）。そのため、参加者にはそのまま届く必要があります。
- IBCF は、この Contact をそのまま通すとき、Record-Route に自分の URI を入れます。会議の URI に宛てた要求が、やはり自分に届くようにするためです。網のあいだでは、これだけが Record-Route です。
- ダイアログの外の SUBSCRIBE は越えません。IBCF が 403 で答え、参加者は会議のセッションのダイアログの中で購読し直します。TS 24.147 5.3.1.2 が、403 のあとにそうするとしています。
- このプロジェクトのフォーカスは、会議の状態の `<endpoint>` を `urn:uuid:` の URI で表します。そのため NOTIFY からは、網 A のアドレスもノードの名前も網 B に見えません。

> 手順書 [「境界を越える会議」](../../../docs/guide/ja/interconnect.md#境界を越える会議)。スクリプトのシナリオにはありません。`internal/ibcf` の試験が 1 つのプロセスで動かします。

---

## IBCF の台が起動し直したとき

- 呼の 2 つのダイアログは、その INVITE を受けた台のプロセスの中にあります。そのプロセスが止まると呼も止まりますが、両側はそれを知りません。どちらも自分のダイアログを持ったままで、TrGW もコンテキストを持ったままです。
- TS 24.229 5.10.2.2 step 2 と 5.10.3.2 step 2 は、転送部を制御する IBCF が、自分でセッションを解放できるように、呼の Contact、CSeq、Record-Route を保存するとしています。
- 各台は、自分の呼の記録を自分の名前の下に置きます。起動し直した台は自分の記録だけを読み、TrGW のコンテキストを解放して、2 つのダイアログそれぞれに BYE を送ります。網全体の呼を探し回る台はありません。
- 設定に kind が `valkey` の `store` があるノードは、この記録をそのサーバーに置きます。コンテナを作り直しても、そこから読み出せます。
- この 2 つの網の IBCF に `store` はありません。通話中に IBCF-A を作り直すと、その呼を終わらせるものがありません。通話の一方からの BYE には新しいプロセスが 481 を返し、TrGW-A はその呼のコンテキストを確保したままになります。

> 手順書 [「IBCF の台が起動し直したとき」](../../../docs/guide/ja/interconnect.md#ibcf-の台が起動し直したとき)

---

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Method == "INVITE" && count(sip.Via) == 1` | 網のあいだの IBCF の INVITE。Via は自分の 1 つだけです。 |
| `sip.Route contains "ibcf"` | IBCF へ向かう要求と、IBCF から出た要求。 |
| `sip.r-uri contains "user=phone"` | 番号を SIP URI で書いた要求。 |
| `sip.r-uri == "tel:+12025550151"` | 網 B の I-CSCF が tel URI に直したあとの INVITE。 |
| `sdp.connection_info.address == 172.28.2.11` | 網のあいだの TrGW-A のアドレスが入った SDP。 |
| `rtp && ip.addr == 172.28.2.0/24` | TrGW どうしの RTP。RTP のヒューリスティックを有効にして使います。 |
| `dns.qry.name contains "5.1.0.5.5.5.2.0.2.1.e164.arpa"` | 網 B の番号の ENUM。 |
| `sip.Status-Code in {404, 405, 420, 488}` | シナリオで断られたときの応答。 |

- 手順書にはこのほかに 5 つあります。復号したショートメッセージや、ほかの 2 つのキャプチャで網 A の内側のものを探すフィルタなどです。後者は、1 つも見つからないのが正しい結果です。
- フィルタは、3 つのキャプチャのどれか 1 つに当てます。スクリプトは、キャプチャごとにシナリオの順でフレームを確かめ、あってはならないフレームも確かめます。

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/interconnect.md#wireshark-の表示フィルタ)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| IBCF | ローミングなしの II-NNI だけ。REGISTER は越えず（405）、Path も `iotl` も無い。ダイアログの外の要求は INVITE と MESSAGE だけが越える。課金の情報も、IBCF 自身のセッションタイマーも無い。 |
| 境界 | トランスコードをしない。相手網が受けないコーデックは外すだけ。IPv4 だけで、TrGW を通るのは最初の音声ストリームだけ。 |
| Ix | IBCF は TrGW を、H.248 ではなくメディア中継ノードの HTTP の制御 API で動かす。 |
| SMS | SC は相手網の利用者を、SMS-GMSC で相手網に尋ねるのではなく ENUM で見つける。配送報告は境界を越えて戻らない。 |

```sh
grep -rn "SIMPLIFIED:" internal/ibcf internal/b2bua internal/interconnect internal/sc internal/ipsmgw
```

- トランスコードをしないのはなぜか。TrGW にコーデックが要り、このプロジェクトのノードは AMR を符号化も復号もしないからです。

> 手順書 [「このプロジェクトで簡略化したこと」](../../../docs/guide/ja/interconnect.md#このプロジェクトで簡略化したこと)。項目ごとの条項と理由は手順書の表にあります。

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| プロファイルと、IBCF がメッセージごとに行うこと | `internal/interconnect` の `screen.go`、`trust.go`、`number.go`、`codec.go`、`leg.go`、`border.go` |
| 呼の 2 つのダイアログのあいだの中継 | `internal/b2bua` |
| 役割 `ibcf`。2 つの UA、呼、ショートメッセージ、TrGW | `internal/ibcf` |
| 網から出ていく要求 | `internal/scscf/session.go` の `exitIBCF` |
| SIP URI の番号が tel URI になるところ | `internal/icscf/session.go` の `telURIOf` |
| 網をまたぐショートメッセージ | `internal/sc/sc.go`、`internal/ipsmgw` の `deliver.go` と `message.go` |
| 2 つの網、そのノード、取り決め | `deploy/interconnect/` |

- `internal/ibcf` の試験は、2 つの網を 1 つのプロセスで動かします。境界の向こうでの分岐、境界を越える会議、起動し直して残した呼を終える台も含みます。

> 手順書 [「コードのある場所」](../../../docs/guide/ja/interconnect.md#コードのある場所)

---

[layout: exercise]

## 演習

1. 3 つのキャプチャそれぞれで、通話の INVITE を探します。1 つの通話がいくつの Call-ID を持ち、それはなぜでしょうか。
2. 網のあいだの INVITE を開きます。Via はいくつ付いていて、ほかの Via はどこにあるでしょうか。
3. IBCF-B に取り決め `network-a-amr-wb-only.yaml` を与えて UE B1 にかけます。どの応答が返り、送ったのはどのノードでしょうか。
4. 網 B のキャプチャで 172.28.0.0/24 のアドレスを探します。どういう結果になるのが正しいでしょうか。
5. どのプロファイルにも無いアドレスから IBCF-A に要求を送ります。何が返るでしょうか。

3 のコマンドは、手順書の「プロファイルが食い違うとき」にあります。5 では、スクリプトが 172.28.2.99 から INVITE と MESSAGE を送っています。

---

[layout: answers]

## 演習の答え

1. 3 つです。ダイアログごとに 1 つあります。IBCF は B2BUA で、届いた側で要求を受け止め、もう一方の側で新しい要求を送るからです。
2. IBCF-A の 1 つだけです。網 A のノードの Via は、IBCF-A で終わった INVITE に付いています。
3. IBCF-B からの 488（Not Acceptable Here）です。網 B は AMR-WB しか受けず、UE A1 は PCMU を offer するからです。
4. 1 つも見つからないのが正しい結果です。網 A の内側のアドレスは境界を越えません。
5. 何も返りません。100（Trying）もエラー応答もありません。IBCF-A が要求を捨てるので、送信元には網 A のことが何も分かりません。

---

[layout: exercise]

## 振り返り

1. 自網から届いた要求を、IBCF はどう扱いますか。
2. 境界を越える 1 つの通話に Call-ID が 3 つあるのはなぜですか。
3. II-NNI を作る 2 つの参照点はどれですか。
4. MESSAGE が境界を越えてよいかを決めるのは何ですか。
5. 発信した UE に相手網のアドレスが見えないのはなぜですか。
6. プロセスが止まった IBCF の台の呼は、どうなりますか。

---

[layout: answers]

## 振り返りの答え

1. 自網の出口として受け止め、相手網へ向けて自分のダイアログで新しい要求を送ります。
2. ダイアログごとに 1 つだからです。発信側、網のあいだ、着信側の 3 つで、IBCF はそれぞれ一方を終えて他方を始めます。
3. SIP を運ぶ IBCF どうしの Ici と、メディアを運ぶ TrGW どうしの Izi です。
4. 相手網との取り決めです。MESSAGE は任意のメソッドで、プロファイルの `methods` が認めるかどうかを決めます。
5. IBCF がメディアを自網の TrGW に通し、それぞれの側の SDP に、その網での TrGW のアドレスを書くからです。
6. 呼はプロセスと一緒に止まり、両側はダイアログを持ったままになります。記録を持った台が起動し直すと、2 つのダイアログそれぞれに BYE を送って終わらせます。

---

## この先に読むもの

- ここが最後の回です。手順書は回の先まで続き、どの手順書も、簡略化したことと、コードのある場所で終わります。

| 文書 | 場所 |
| --- | --- |
| プロジェクトの説明と動かし方 | [README.ja.md](../../../README.ja.md) |
| 用語集 | [docs/glossary.md](../../../docs/glossary.md) |
| 手順書 | [docs/guide/ja/](../../../docs/guide/ja/) |

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml --profile ue down -v --remove-orphans
```

- 手順書の「後片付け」です。両方の網の UE と、両方の網と網のあいだのノードを止めます。

> 手順書 [「後片付け」](../../../docs/guide/ja/interconnect.md#後片付け)
