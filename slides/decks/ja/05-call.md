---
title: 音声通話の流れ
footer: ims-practice 勉強会 5. 通話
lang: ja
---

# 音声通話の流れ

ims-practice 勉強会 第 5 回（中継ノードを通る通話と、その途中のプリコンディションと MMTel AS）

---

[layout: summary]

## この回で分かること

最初の問い。UE 1 は UE 2 の IP アドレスを知っているでしょうか。知りません。知っているのは電話番号だけです。

- 電話番号が SIP のアドレスに変わるしくみ。
- 各 CSCF が、次の CSCF を見つける方法。
- 2 台の UE が、メディアを合意する方法。
- 音声が、メディア中継ノードを通って流れる様子。

> 手順書 [音声通話の流れを追う](../../../docs/guide/ja/call.md)（`docs/guide/ja/call.md`）
> 仕様の版は Release 18 です。TS 24.229 V18.11.0、TS 29.228 V18.0.0 と、各手順で挙げる RFC。

---

## 前の回から引き継ぐもの

通話は、登録で決まった次の 3 つを使います。

| 登録で決まったもの | 持っているノード | 通話で使う場面 |
| --- | --- | --- |
| Service-Route | UE | UE の要求を、自分の S-CSCF へ届ける。 |
| Path | S-CSCF | UE への要求を、その UE の P-CSCF へ届ける。 |
| IP の対応付け | P-CSCF | 要求が、登録した UE から来たことを確かめる。 |

- 通話の前に、2 台の UE が前の回のとおりに登録します。キャプチャの最初にその REGISTER があります。

> 手順書 [登録の「10. S-CSCF が登録を受け付ける（200 OK）」](../../../docs/guide/ja/registration.md#10-s-cscf-が登録を受け付ける200-ok)

---

## 登場するノード

| ノード | アドレス | 通話での役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 発信者。`+1-202-555-0101`。 |
| UE 2 | 172.28.0.101 | 着信者。`+1-202-555-0102`。 |
| P-CSCF | 172.28.0.40 | 両方の UE を担当する。1 回の通話で 2 回通る。 |
| S-CSCF | 172.28.0.30 | 発信側と着信側の S-CSCF を兼ねる。 |
| I-CSCF | 172.28.0.20 | 着信者のホーム網の入口。HSS に所在を尋ねる。 |
| DNS | 172.28.0.2 | 番号を SIP URI に変える ENUM のレコードを持つ。 |
| メディア中継ノード | 172.28.0.50 | RTP を中継する。P-CSCF が制御する。 |
| MMTel AS | 172.28.0.75 | 発信者と着信者の INVITE を、それぞれ受けて送り返す。 |

- 1 台のノードが、発信側と着信側の 2 つの立場で動きます。これがこの回のキャプチャを読む鍵です。実際の網では、2 つの側は別のノードで、別の網にあることも多いです。
- ここでの MMTel AS は、INVITE を何も変えずに送り返します。AS が何をするかは補足サービスの回で見ます。

> 手順書 [「登場するノード」](../../../docs/guide/ja/call.md#登場するノード)

---

## 実行する

```sh
docker compose up -d --build

# 1 つ目の端末。UE 2 が着信を待つ。
docker compose run --rm ue2 answer \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2 -wav-out /captures/ue2-heard.wav

# 2 つ目の端末。UE 1 が番号で発信し、3 秒のトーンを流して切る。
docker compose run --rm ue call -tone 3s tel:+1-202-555-0102
```

- `captures/ue2-heard.wav` に、UE 2 が聞いた音声が残ります。
- 番号は 555-0100 から 555-0199 の範囲です。北米の番号計画（NANP）が架空の用途に取ってある番号です。

> 手順書 [「発信する」](../../../docs/guide/ja/call.md#発信する)

---

## キャプチャを開く

```text
sip || diameter.cmd.code == 302 || dns.qry.type == 35 || rtp || rtcp
```

- **Telephony > VoIP Calls** に通話が出ます。
  - **Flow Sequence** でラダー図を見ます。
  - **Play Streams** で両方向の音声を聞きます。
- RTP がただの UDP と表示されるときは、**Analyze > Enabled Protocols** で **rtp_udp** を有効にします。
- 手順書のラダー図の番号 1 から 13 が、次からのスライドの番号です。

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/call.md#キャプチャを開く) と [「シーケンス」](../../../docs/guide/ja/call.md#シーケンス)。ラダー図の全体は手順書にあります。

---

## 全体の形

INVITE は、次の順でノードを通ります。

```ladder
UE 1, P-CSCF, S-CSCF, MMTel AS, I-CSCF, HSS, UE 2
UE 1 -> P-CSCF -> S-CSCF: INVITE (orig) // 1 - 2
S-CSCF -> MMTel AS: INVITE (orig) // 3
S-CSCF <- MMTel AS: 新しい INVITE
S-CSCF -> I-CSCF: INVITE // 4
I-CSCF <-> HSS: LIR, LIA // 5
I-CSCF -> S-CSCF: INVITE (term) // 6
S-CSCF -> MMTel AS: INVITE (term)
S-CSCF <- MMTel AS: 新しい INVITE
S-CSCF -> P-CSCF: INVITE (term) // 6
P-CSCF -> UE 2: INVITE (term) // 7
```

- `orig` は発信側の立場（originating）、`term` は着信側の立場（terminating）です。
- AS はルーティングの B2BUA です（TS 24.229 5.7.5）。AS の新しい INVITE は自分の Call-ID を持つので、1 つの通話にダイアログが 3 つできます。

---

## 通話の区間

| 区間 | 手順 | 起きること |
| --- | --- | --- |
| 発信側 | 1 から 3 | 発信者を確かめ、AS を通し、番号を SIP URI に変える。 |
| 網の入口 | 4 と 5 | 着信者を担当する S-CSCF を、HSS に尋ねる。 |
| 着信側 | 6 と 7 | AS を通し、Path に沿って着信者の UE に届ける。 |
| 応答 | 8 から 11 | 183、PRACK、180、PRACK、200 OK、ACK。 |
| 音声と切断 | 12 と 13 | RTP が中継ノードを通る。BYE で終わる。 |

- この表がこの回の地図です。新しい区間に入るたびに戻ってきます。

---

## 1 と 2。INVITE と、発信側の P-CSCF

| 見るもの | 見どころ |
| --- | --- |
| Request-URI | `tel:+1-202-555-0102`。UE は番号をダイヤルするだけ。 |
| Route | 2 つ。P-CSCF と、登録で受け取った Service-Route。 |
| Supported | `100rel`、`timer`、`199`、`precondition`。 |
| Session-Expires | 1800。UE 1 が UPDATE でセッションを更新する。 |
| SDP のオファー | UE 1 のアドレスとポート。PCMU と telephone-event。 |
| オファーの `a=curr` と `a=des` | プリコンディション。UE 1 の資源はもうある。 |
| P-CSCF が足すヘッダ | P-Asserted-Identity と Record-Route。 |
| P-CSCF を出たあとの SDP | `c=` と `m=` が中継ノードを指す。 |

- 網が信じるのは From ではなく P-Asserted-Identity です。UE が From に何を書いても関係ありません。
- UE 1 には資源を確保するアクセス網が無いので、オファーは資源がもうあると伝えます。`-epc` を付けて起動した UE は VoLTE の UE で、PCMU より先に AMR-WB と AMR を提示します。その通話は VoLTE の回で追います。

> 手順書 [1](../../../docs/guide/ja/call.md#1-ue-1-が-invite-を送る) と [2](../../../docs/guide/ja/call.md#2-p-cscf-が発信者の-s-cscf-に送る)。TS 24.229 5.1.3.1、5.1.2A.1.1、6.1.2、5.2.6.2、5.2.6.3.3。RFC 3262 4。RFC 3312。RFC 3264 5.1。

---

## 3。S-CSCF が番号を SIP URI に変える

- Route の先頭に `orig` があるので、S-CSCF は発信者を担当する側として動きます。

| 見るもの | 見どころ |
| --- | --- |
| P-Asserted-Identity | 2 つ目として、UE 1 の tel URI が加わる。 |
| P-Asserted-Service | MMTel の ICSI。P-Preferred-Service は消える。 |
| AS に送る INVITE | Route の先頭に AS、次に `odi` 付きの S-CSCF。`sescase=orig`。 |
| DNS の問い合わせ | `2.0.1.0.5.5.5.2.0.2.1.e164.arpa` の NAPTR。番号を逆順にしたもの。 |
| NAPTR の答え | サービスは `E2U+sip`。正規表現が SIP URI を作る。 |
| S-CSCF を出たあとの Request-URI | `sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`。 |

フィルタは `dns.qry.type == 35` です。レコードは `deploy/compose/dns/e164.arpa.zone` にあります。

- 番号から ENUM のドメインを手で組み立ててみます。数字を逆に並べ、1 桁ずつラベルにします。
- AS は発信者のサービスを Sh で HSS から読みます。既定の設定では何も変えず、新しい INVITE を送り返します。S-CSCF はその中に自分の `odi` を見つけて次の iFC へ進み、そのあとで番号を SIP URI に変えます。

> 手順書 [3](../../../docs/guide/ja/call.md#3-s-cscf-が番号を-sip-uri-に変える)。TS 24.229 5.4.3.1、5.4.3.2、5.7.5。RFC 6050。RFC 6116。

---

## 4 と 5。ホーム網の入口と、LIR

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF の DNS の問い合わせ | 登録のときと同じ NAPTR と SRV。I-CSCF が見つかる。 |
| S-CSCF から先の Via | `SIP/2.0/TCP`。INVITE が 1300 バイトを超えた。 |
| LIR | 着信者の公開の識別子が入る。 |
| LIA | DIAMETER_SUCCESS と、UE 2 が登録している S-CSCF の Server-Name。 |
| I-CSCF が転送する INVITE | Route の先頭にその S-CSCF。Record-Route は付かない。 |

フィルタは `diameter.cmd.code == 302` です。

- I-CSCF は Record-Route を付けないので、あとの PRACK、ACK、BYE は I-CSCF を通りません。
- LIR が尋ねることは、登録の UAR と同じです。どの S-CSCF がその利用者を担当しているか、です。

> 手順書 [4](../../../docs/guide/ja/call.md#4-s-cscf-が着信者のホーム網を見つける) と [5](../../../docs/guide/ja/call.md#5-i-cscf-が着信者の所在を-hss-に問い合わせるlir-と-lia)。TS 24.229 5.3.2.1。TS 29.228 6.1.4。RFC 3261 18.1.1。

---

## 6 と 7。着信側の S-CSCF と P-CSCF

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF に届いた Route | `orig` が無い。今度は着信者を担当する側として動く。 |
| AS に送る INVITE | `sescase=term`。AS が新しい INVITE を送り返す。 |
| P-Called-Party-ID | 発信された宛先。 |
| S-CSCF を出たあとの Request-URI | UE 2 が登録した Contact。 |
| S-CSCF を出たあとの Route | UE 2 の REGISTER にあった Path。 |
| P-CSCF を出たあとの SDP | 2 つ目のコンテキストの、アクセス側のターミネーションを指す。 |
| INVITE の送り先 | UE 2 の REGISTER が来たアドレス。 |

- INVITE は S-CSCF から I-CSCF へ出て、同じ S-CSCF に戻ってきます。
- P-CSCF が着信側だと分かるのは、Route の先頭が自分の Path の URI で、ユーザー部が `term` だからです。
- 送り先は Contact ではなく、REGISTER が来たアドレスです。NAT の後ろの UE に届くのは、そのアドレスだけだからです。

> 手順書 [6](../../../docs/guide/ja/call.md#6-着信側の-s-cscf-が-path-に沿って-invite-を送る) と [7](../../../docs/guide/ja/call.md#7-p-cscf-が-invite-を-ue-2-に届ける)。TS 24.229 5.4.3.3、5.2.6.4.3。

---

## 8 と 9。183 と PRACK

| 見るもの | 見どころ |
| --- | --- |
| 183 の SDP | アンサー。PCMU と、UE 2 のアドレスとポート。 |
| 183 の Require と RSeq | `100rel` と `precondition`。信頼性のある暫定応答。 |
| 183 のプリコンディション | 両端とも資源がある。`a=conf` は無い。 |
| 183 の To のタグ、Contact、Record-Route | 初期ダイアログができる。 |
| UE 1 に届いた 183 の SDP | P-CSCF が 2 回書き換えている。中継ノードを指す。 |
| PRACK の RAck | 183 の RSeq と、INVITE の CSeq。 |
| PRACK の経路 | 発信者の AS で終わり、AS が次のダイアログに送る。 |

- PRACK はダイアログの中の要求なので、そのダイアログの相手で終わります。AS は次のダイアログの PRACK を送ってから、受け取った PRACK に 200 で答えます。3 つのダイアログに、PRACK と 200 が 1 組ずつあります。Record-Route を付けない I-CSCF は、どれも通りません。

> 手順書 [8](../../../docs/guide/ja/call.md#8-ue-2-がオファーに答える信頼性のある-183) と [9](../../../docs/guide/ja/call.md#9-ue-1-が-183-を確認するprack)。RFC 3264 6.1。RFC 3262 3、4。TS 24.229 5.1.4.1。RFC 3261 12.2.1.1。

---

## 10 と 11。180、200 OK、ACK

| 見るもの | 見どころ |
| --- | --- |
| 180 Ringing | プリコンディションがそろってから送る。これにも PRACK が返る。 |
| INVITE への 200 OK | ダイアログが確定する。 |
| 200 OK の SDP | 無い。アンサーは信頼性のある 183 で届いている。 |
| ACK | ルートセットに沿って進む。INVITE のトランザクションの一部ではない。 |

- UE 2 は ACK が届くまで、間隔を広げながら 200 OK を送り直します。
- 資源がまだ無い着信側は、資源がそろうまで 180 を送りません。この構成の UE はどちらも資源があるので、183 のすぐあとに 180 が続きます。
- AS は UE 2 の 200 に、自分のダイアログですぐ ACK を返します。だから UE 2 への ACK が、UE 1 への 200 より先に来ることがあります。

> 手順書 [10](../../../docs/guide/ja/call.md#10-ue-2-が鳴らしてから応答する180-と-200-ok) と [11](../../../docs/guide/ja/call.md#11-ue-1-が確認するack)。RFC 3312 6。TS 24.229 5.1.4.1。IR.92 2.4.1。RFC 3261 13.3.1.4、13.2.1、13.2.2.4。RFC 3262 5。

---

## 12。音声が中継ノードを通る

```text
UE 1 -> relay access (context 1) -> relay core (context 1)
     -> relay core (context 2) -> relay access (context 2) -> UE 2
```

| 見るもの | 見どころ |
| --- | --- |
| RTP の間隔と大きさ | 20 ms ごと。G.711 μ-law が 160 バイト。 |
| RTP の送信元と宛先 | UE と中継ノードの間だけ。UE どうしは直接やり取りしない。 |
| 送信と受信のポート | 同じポート。対称 RTP。 |
| RTCP | RTP の 1 つ上のポート。5 秒に 1 回以上。 |

- 中継ノードは、最初に受け取ったパケットの送信元を送り先にします。ラッチングと呼びます。

フィルタは `rtp || rtcp` です。**Play Streams** で音声を聞きます。

> 手順書 [12](../../../docs/guide/ja/call.md#12-音声が中継ノードを通って流れる)。RFC 3550 5.1、6。RFC 3551 4.5.14。RFC 4961。IR.92 3.2.4。

---

## 12。2 つのコンテキストとラッチング

- 1 つの通話に、中継ノードのコンテキストが 2 つあります。発信側を通る P-CSCF が 1 つ、着信側を通る P-CSCF がもう 1 つを作ります。P-CSCF はどちらの回も、自分の側だけを扱います。
- だから RTP は、2 つの網の IMS-AGW を通るのと同じように、中継ノードを 2 回通ります。キャプチャでも 2 つの側を見分けられます。2 台の UE が別々の P-CSCF の後ろにいても、通話は同じに見えます。
- ラッチングは NAT の後ろの UE のためです。ホストのソフトフォンは、SDP には書けないアドレスから送ってきます。実際の網の P-CSCF と IMS-AGW も同じことをします。
- RTCP の複合パケットには、送信の報告（UE が何も送っていない間は受信の報告）と、CNAME の入った SDES があります。RTCP の BYE で流れが終わります。

---

## 13。切断

| 見るもの | 見どころ |
| --- | --- |
| BYE | ダイアログの中の要求。ルートセットに沿って進む。 |
| BYE の Reason | `RELEASE_CAUSE;cause=1`。通話を終えた理由。 |
| BYE への 200 | ダイアログごとに相手が返す。最後は UE 2。 |
| P-CSCF の動き | 200 が通るときに、中継のコンテキストを削除する。 |

- P-CSCF が応答を待つのは、BYE が断られることもあるからです。
- コンテキストの削除は、SIP のメッセージには出ません。P-CSCF は中継ノードの API を呼びます。このプロジェクト独自の小さな HTTP の API で、H.248 のような大きなプロトコルより中継ノードを読みやすくするためのものです。
- AS は UE 1 の BYE に自分のダイアログで答え、次のダイアログに自分の BYE を送ります。だから UE 1 への 200 と UE 2 への BYE は、どちらが先に来ることもあります。

> 手順書 [13](../../../docs/guide/ja/call.md#13-ue-1-が切断するbye)。TS 24.229 5.1.5。IR.92 2.2.4。RFC 3261 15.1.1、15.1.2、12.2.2。

---

## 失敗を見る

| 場合 | 試し方 | 終わり方 | 返すノード |
| --- | --- | --- | --- |
| 話中 | UE 2 を `-reject 486` 付きで起動する。 | 486 Busy Here | UE 2 |
| 未登録 | `tel:+12025550103` に発信する。 | 480 Temporarily Unavailable | S-CSCF |
| 存在しない番号 | `tel:+12025550198` に発信する。 | 404 Not Found | S-CSCF |

- 未登録の利用者にも、MMTel AS を呼ぶ iFC があります。LIA は DIAMETER_UNREGISTERED_SERVICE（2003）になります。I-CSCF が選んだ S-CSCF は、AS が INVITE を送り返したところで 480 を返します。
- 存在しない番号には ENUM のレコードがありません。このプロジェクトには BGCF が無いので、S-CSCF が 404 で答えます。
- 応答を作ったノードは、その応答を運ぶ最初のパケットの送信元で分かります。AS は受け取った応答を自分のダイアログに写すので、AS より先を探します。

> 手順書 [「発信する」](../../../docs/guide/ja/call.md#発信する)、[3](../../../docs/guide/ja/call.md#3-s-cscf-が番号を-sip-uri-に変える)、[5](../../../docs/guide/ja/call.md#5-i-cscf-が着信者の所在を-hss-に問い合わせるlir-と-lia)。TS 29.228 6.1.4.1。TS 24.229 5.4.3.3。

---

## ラボから通話をかける

```sh
docker compose --profile phones up -d --build
```

1. <http://localhost:8080> を開きます。`phone2` のカードで、応答方針を `manual` にします。
2. `phone1` のカードで **Call** を押します。
3. `phone2` のカードで **Answer** を押します。どちらかで **Hang up** を押します。
4. 通話の横の **ladder** を選ぶと、通話中でもラダー図が出ます。

- 応答の前に切ると、発信側は CANCEL を送り、着信側は 603 で断ります。
- `phones` と `ue` のプロファイルを同時に動かさないでください。同じ加入者の UE が 2 台になり、互いの登録を上書きします。UE を動かす HTTP の API はこのプロジェクト独自のもので、IMS には何も流れません。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する) と、[通話の「HTTP で UE を操作する」](../../../docs/guide/ja/call.md#http-で-ue-を操作する)

---

## コードで確かめる

| 手順 | 開くファイル |
| --- | --- |
| 1、8 から 13。UE の発信、応答、音声 | `internal/ue/call.go`、`internal/ue/media.go` |
| 2、7。P-CSCF の 2 回の通過と SDP の書き換え | `internal/pcscf/session.go` |
| 3、6。S-CSCF の発信側と着信側 | `internal/scscf/session.go` |
| 3、6。MMTel AS（routeing B2BUA） | `internal/mmtel/call.go` |
| 8 から 10。プリコンディション | `internal/precondition` |
| 3。ENUM | `internal/sipdns/enum.go` |
| 5。LIR | `internal/icscf/session.go` |
| 12。中継とラッチング | `internal/agw/relay.go` |
| ダイアログとルートセット | `internal/sip/dialog.go` |

- `internal/scscf/session.go` で、発信側と着信側を見分ける条件を探します。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/call.md#このプロジェクトで簡略化していること)

---

[layout: exercise]

## 振り返り

1. UE 1 の INVITE の Request-URI は、UE 2 に届くまでに何回変わりますか。
2. S-CSCF は、自分が発信側か着信側かを、何で見分けますか。
3. I-CSCF が PRACK と BYE の経路にいないのは、なぜですか。
4. UE 1 が受け取る SDP のアドレスは、誰のものですか。
5. 480 と 404 は、それぞれどのノードが、何を根拠に返しますか。
6. UE 2 の 200 OK に SDP が無いのは、なぜですか。

---

[layout: answers]

## 振り返りの答え

1. 2 回です。S-CSCF が tel URI を SIP URI に変え、着信側の S-CSCF が UE 2 の Contact にします。
2. Route の先頭の URI にある `orig` で見分けます。
3. Record-Route を付けないからです。
4. メディア中継ノードのものです。UE 1 のコンテキストのアクセス側です。
5. どちらも S-CSCF です。480 は、LIA が DIAMETER_UNREGISTERED_SERVICE で、未登録の利用者のサービスである MMTel AS が通話を受けなかったからです。404 は、ENUM にレコードが無いからです。
6. 答えが信頼できる 183 で届いているからです（RFC 3262 5）。

---

## 次の回

- 次の回は SMS です。登録と、I-CSCF が S-CSCF を見つけるしくみを、そのまま使います。
- この回で MMTel AS を呼んだ iFC が、次の回の主役です。ショートメッセージは iFC で IP-SM-GW に届きます。

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```

> 次の回の手順書は [SMS の流れを追う](../../../docs/guide/ja/sms.md) です。
