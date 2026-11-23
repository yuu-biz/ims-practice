---
title: MRF による音声ガイダンス、呼び出し音、会議
footer: ims-practice 勉強会 8. MRF
lang: ja
---

# MRF による音声ガイダンス、呼び出し音、会議

ims-practice 勉強会 第 8 回（MRFC と MRFP、音声ガイダンス、着信側が選ぶ呼び出し音、3 人の会議）

---

[layout: summary]

## この回で分かること

最初の問い。電話をかけると、相手が鳴っているあいだに短い旋律が聞こえます。その旋律は相手が選んだものです。流しているのはどのノードで、流すと決めたのはどのノードでしょうか。

- MRF とは何か。なぜ MRFC と MRFP の 2 つのノードに分かれるか。
- AS がサービスを頼む方法。Mr' で送る RFC 4240 の Request-URI。
- 通話に応答する前に、網が発信者へ音声を届けるしくみ。
- 音声ガイダンス、着信側が選んだ呼び出し音、2 つ目の着信を、キャプチャのどこで見るか。
- 3 人の会議の作り方と、音声を混ぜるノード。

> 手順書 [MRF による音声ガイダンス、呼び出し音、会議](../../../docs/guide/ja/mrf.md)（`docs/guide/ja/mrf.md`）
> 仕様の版は手順書の冒頭にあります。TS 23.228 V20.1.0、TS 24.229 V20.1.0、TS 24.182 V19.0.0、TS 24.628 V19.0.0、TS 24.615 V19.0.0、TS 24.147 V19.0.0、TS 23.333 V19.0.0、RFC 4240、RFC 4575、GSMA IR.92 v24.0 などです。

---

## MRF とは

- CSCF は経路を決め、前の回の AS は通話を変えます。どちらも音声は作りません。IMS の中で自分からメディアを作るノードが MRF です（TS 23.228 4.7）。音声ガイダンス、トーン、会議の混ぜ合わせた音声を作ります。
- **MRFC** は SIP を話します。AS が INVITE でサービスを頼むと、MRFC はそれに要るメディアの資源を決めます。
- **MRFP** は RTP を扱います。ターミネーションに音声を流し、複数のターミネーションの音声を足し合わせて、それぞれに混ぜ合わせた音声を返します（TS 23.333 6.2.9）。

```text
  S-CSCF --- MMTel AS ---(Mr', SIP)--- MRFC
                                         |
                                       (Mp)
                                         |
  UE --- P-CSCF --- agw -----(RTP)---- MRFP
```

- MRFP は、P-CSCF のメディアゲートウェイ（agw）と同じ中継に、音声ガイダンスと混ぜ合わせを加えたものです。UE と MRFP の間の RTP は agw を通ります。

> 手順書 [「MRF とは」](../../../docs/guide/ja/mrf.md#mrf-とは)

---

## 3 つの参照点

| 参照点 | どこの間か | このプロジェクトでは |
| --- | --- | --- |
| Mr' | AS と MRFC | SIP。MMTel AS から直接送ります。S-CSCF からの Mr は使いません。 |
| Mp | MRFC と MRFP | 標準では H.248（TS 29.333）。ここでは 8080 番ポートの小さな HTTP API で、H.248 のコマンドごとに操作が 1 つあります。 |
| Cr | AS と MRFC | 流す音声の取得とメディア制御の命令。作っていません。MRFP は持っている WAV ファイルだけを流します。 |

- ここでの Mp の操作は、ターミネーションの追加、変更（モード、相手のアドレス、再生）、削除と、再生が終わったことを知らせる Notify です。キャプチャでは `http && ip.addr == 172.28.0.86` で見えます。
- ここでメディアを頼むのは S-CSCF ではなく AS です（TS 23.218）。そのため Mr には何も流れません。Cr はそもそも作っていません。

> 手順書 [「MRF とは」](../../../docs/guide/ja/mrf.md#mrf-とは)

---

## 登場するノード

| ノード | アドレス | 役割 |
| --- | --- | --- |
| UE 1、UE 2 | 172.28.0.100、.101 | 音声ガイダンスと呼び出し音の、発信者と着信者。 |
| phone1、phone2、phone3 | 172.28.0.110、.111、.112 | `phones` プロファイルの UE。ラボから操作します。2 つ目の着信と会議に使います。 |
| P-CSCF と agw | 172.28.0.40、.50 | ほかの通話と同じです。MRFP との RTP も agw を通ります。 |
| MMTel AS | 172.28.0.75 | 何を流すかを決めます。会議ではフォーカスになります。 |
| MRFC | 172.28.0.85 | 役割 `mrfc`。 |
| MRFP | 172.28.0.86 | 役割 `mrfp`。制御の API は 8080 番ポート、RTP は 40100 番から 40199 番のポートです。 |

- `deploy/compose/mmtel.yaml` に、MRFC と、通話を断る前に流す音声ガイダンスを書いてあります。`deploy/compose/mrfp.yaml` は `notice`、`cat1`、`cat2` の名前を `deploy/announcements` の WAV ファイルに対応させます。

> 手順書 [「登場するノード」](../../../docs/guide/ja/mrf.md#登場するノード)

---

## AS が頼む方法。RFC 4240

| Request-URI | サービス |
| --- | --- |
| `sip:annc@mrfc...:5060;play=/provisioned/notice` | `notice` を 1 回流します。 |
| `sip:annc@...;play=/provisioned/cat1;repeat=forever` | AS がダイアログを終えるまで `cat1` を流し続けます。 |
| `sip:conf=conf-pcn6hpat5alr@mrfc...` | その会議に加わります。最初の INVITE が会議を作ります。 |

- サービスは Request-URI の user part で指定します。`annc` は再生、`conf=` は会議への参加です。`play=` は MRFP の設定にあるファイルの名前です。
- 最初の `conf=` の INVITE が混ぜ合わせを作り、そのあとの INVITE はそこにターミネーションを足します。

フィルタは `sip.r-uri contains "annc@"` と `sip.r-uri contains "conf="` です。

> 手順書 [「AS が MRF に頼む方法。RFC 4240」](../../../docs/guide/ja/mrf.md#as-が-mrf-に頼む方法rfc-4240)。RFC 4240 3、5。

---

## 応答より前に届く音声

- 音声ガイダンスを頼む INVITE には SDP が付きません。そのため MRFC は MRFP のターミネーションの offer を 200 に入れて返し、AS は answer を ACK に入れます。
- MRFP は ACK を受けてから流し始め（TS 24.229 5.8.2.2.1）、終わりのある再生が済むと MRFC が BYE でダイアログを終えます（5.8.3.2.1）。こうして AS は、発信者が受け取れるようになってから再生を始められます。
- 通話に応答する前に発信者へ音声を届けるため、AS は発信者の offer に MRFP の SDP で自分から答え、`P-Early-Media: sendonly` で early media を認めます（RFC 5009）。TS 24.628 Annex G のゲートウェイのモデルです。
- 発信者の UE は、自分の呼び出し音の代わりに届いた音声を流します（TS 24.628 4.7.2.1）。

フィルタは `sip.P-Early-Media` です。

> 手順書 [「AS が MRF に頼む方法。RFC 4240」](../../../docs/guide/ja/mrf.md#as-が-mrf-に頼む方法rfc-4240)

---

## 実行する

```sh
docker compose up -d --build

# 音声ガイダンス。UE 1 が発信を規制してから UE 2 にかける
docker compose run --rm ue xcap set baoc on
docker compose run --rm ue call tel:+12025550102
docker compose run --rm ue xcap set baoc off

# 2 つ目の着信と会議。ラボから操作する
docker compose --profile phones up -d phone1 phone2 phone3
```

- 呼び出し音は、http://localhost:8080 のラボを開き、Subscribers の欄で加入者 2 に `cat1` を選んでから、`phone1` で `phone2` にかけます。
- `scripts/e2e-mrf.sh` は、この回のすべてを実行してキャプチャで確かめます。キャプチャ、通話と MRF のフレーム、呼び出し音のあいだに UE 1 が受け取った音声、UE のログが残ります。

> 手順書 [「動かしてみる」](../../../docs/guide/ja/mrf.md#動かしてみる) と [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## キャプチャを開く

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (sip.Event contains "conference") || (http && ip.addr == 172.28.0.86)
```

- 通話、会議の SUBSCRIBE と NOTIFY、MRFP の制御の API が残ります。
- **Telephony > RTP > RTP Streams** に MRFP のストリームが並び、**Play Streams** で再生できます。
- RTP の中には、それを説明する SDP を Wireshark が読む前に流れ始めるものがあります。すべてを復号するには **Analyze > Enabled Protocols > RTP > rtp_udp** を有効にします。

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/mrf.md#キャプチャを開く)

---

## 通話を断る前の音声ガイダンス

```ladder
UE 1, P-CSCF, MMTel AS, MRFC
UE 1 -> P-CSCF -> MMTel AS: INVITE // C1
MMTel AS -> MRFC: INVITE annc、SDP なし // M
MMTel AS <-- MRFC: 200、MRFP の offer
UE 1 <-- P-CSCF <-- MMTel AS: 183、P-Early-Media // C1
UE 1 -> P-CSCF -> MMTel AS: PRACK
MMTel AS -> MRFC: ACK、answer を入れて再生が始まる
MMTel AS <-- MRFC: BYE、notice を流し終えた
UE 1 <-- P-CSCF <-- MMTel AS: 603、Reason cause=603
```

- UE 1 は自分の発信を規制しているので、UE 1 の AS は通話を断ります（TS 24.611 4.5.2.4.1）。断る前に `notice` を流します（TS 24.628 4.2.4）。
- AS は PRACK を待ってから ACK を送ります。UE 1 が SDP を受け取る前に、音声ガイダンスのフレームが失われないようにするためです。
- ACK から BYE までを測ります。1.08 秒で、WAV ファイルの長さと同じです。

> 手順書 [「通話を断る前の音声ガイダンス」](../../../docs/guide/ja/mrf.md#通話を断る前の音声ガイダンス)

---

## CAT。着信側の利用者が選ぶ呼び出し音

| 見るもの | 見どころ |
| --- | --- |
| UE 2 の AS の UDR | Service-Indication は `Customized-Alerting-Tone`。着信のたびに読みます。 |
| AS から MRFC への INVITE | `play=/provisioned/cat1;repeat=forever`。UE 2 への INVITE より前に送ります。 |
| UE 1 に届く 183 | UE 2 の answer が、プリコンディションの表とともにそのまま届きます。 |
| UE 2 の 180 のあとの UPDATE | `P-Early-Media: sendonly` と、`a=content:g.3gpp.cat` の付いた MRFP の SDP。 |
| 応答のとき | MRFC への BYE と、200 の前に UE 1 のメディアを UE 2 へ移す UPDATE。 |

- 利用者の呼び出し音は、事業者が HSS に設定します（TS 24.182 4.5.2）。ここではラボが設定します。着信側の AS は着信のたびに Sh で読み、UE 2 を呼び出す前に MRFC に頼みます（4.5.5.3.7）。
- UE 1 はプリコンディションを使うので、呼び出し音は UE 2 が呼び出しを始めたあとの UPDATE で届きます（4.5.5.3.7 b 1）。プリコンディションを使わない発信者には、18x そのものに入ります。

フィルタは `sdp.media_attr == "content:g.3gpp.cat"` です（RFC 4796）。

> 手順書 [「CAT。着信側の利用者が選ぶ呼び出し音」](../../../docs/guide/ja/mrf.md#cat着信側の利用者が選ぶ呼び出し音)。TS 24.182 4.5.5.3.7 b 2。

---

## 呼び出し音を止めた同じ通話

| 見るもの | `cat1` のとき | 止めたとき |
| --- | --- | --- |
| UE 1 のログ | `"ringback":"network"` | `"ringback":"local"` |
| UE 1 に届くもの | P-Early-Media の付いた UPDATE | 180 だけ |
| MRFC | `repeat=forever` の INVITE | 無し |
| MRFP | 応答前に UE 1 へ届く RTP | 無し |

- 呼び出し音を止めると、UE 1 は自分の呼び出し音を流します（TS 24.628 4.7.2.1）。e2e のスクリプトのキャプチャには、この 2 つの通話が続けて残ります。見比べてください。
- なぜ見比べるのか。呼び出し音が増やすフレームと、ほかは何も変わらないことの、両方が分かるからです。
- スクリプトは、UE 1 が受け取った最初の 1 秒を、MRFP が PCMU に符号化した `cat1.wav` と比べます。1 バイトも違いません。

> 手順書 [「CAT。着信側の利用者が選ぶ呼び出し音」](../../../docs/guide/ja/mrf.md#cat着信側の利用者が選ぶ呼び出し音)

---

## 割込通話と保留

| 見るもの | 見どころ |
| --- | --- |
| 2 つ目の通話での phone1 の 180 | `Alert-Info: <urn:alert:service:call-waiting>`（TS 24.615 4.5.5.3.2、RFC 7462）。 |
| phone3 に届く 180 | この Alert-Info はありません。`hss.yaml` の CW の事業者の設定が、発信者に知らせないとしています（4.5.5.2.3）。 |
| 待たせた着信への応答 | 先の通話に `a=sendonly` の re-INVITE を送り、phone2 を保留にします（4.5.5.3.3、TS 24.610）。 |
| **Swap** | 一方が保留になり、もう一方が `a=sendrecv` で戻ります。 |

- IR.92 は、待たせる着信の扱いを網ではなく UE に求めています（2.3.4）。phone1 は呼び出しを返すだけで、自動では応答しません。
- 知らせる設定なら、AS は Alert-Info をそのまま渡し、音声ガイダンスを設定してあれば、待たせているあいだ発信者にそれを流します。ここに MRF が出てくるのは、その設定のときです。

フィルタは `sip.Alert-Info contains "call-waiting"` です。

> 手順書 [「割込通話と保留」](../../../docs/guide/ja/mrf.md#割込通話と保留)

---

## 3 人の会議

phone1 は応答済みの通話を 2 つ持っていて、**Merge** を押します。

| 段階 | 見どころ |
| --- | --- |
| 1 | 会議ファクトリの URI `sip:mmtel@conf-factory...` への INVITE。 |
| 2 | AS は `conf=` と phone1 の offer で MRFC に頼み、`isfocus` を付けた会議の URI を Contact に入れて phone1 に応答します。 |
| 3 | phone1 がその会議の URI を SUBSCRIBE します。Event は `conference` です。 |
| 4 | 相手ごとの REFER。Refer-To には、phone1 とその相手の通話を置き換える Replaces が入ります。 |
| 5 | フォーカスは、その Replaces と MRFP の新しい offer を付けて相手に INVITE します。相手は置き換えられた通話を BYE で終えます。 |

- 作った人が相手ごとに REFER で招く方法です。TS 24.147 5.3.1.3.3 の option 2b で、IR.92 2.3.3 が求めるものです。
- フォーカスは REFER を 200 で受け付け、202 は使いません。RFC 7647 5 が RFC 3515 の 202 を改めました。SUBSCRIBE について RFC 6665 8.3.1 が改めたのと同じです。TS 24.147 の流れの例でも 200 を返しています（A.4.4.1）。

フィルタは `sip.Refer-To contains "Replaces"` と `sip.Replaces` です。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## 会議の URI は PSI

- 会議の URI は、ホーム網の公開サービス識別子（PSI）です（TS 23.228 4.3.6）。形は `sip:conf-<id>@conf.ims.mnc001.mcc001.3gppnetwork.org` で、ドメインは事業者が決めます。ノードの名前は含まず、ポート番号も付けません。網の外に出る可能性がある PSI には、ポート番号を付けないほうがよいためです（TS 23.003 13.5）。
- このサブドメインの NAPTR と SRV のレコードが、会議を受け持つ AS を指します。S-CSCF はほかのホストと同じく RFC 3263 でフォーカスを見つけます。TS 23.228 5.4.12.3 のサブドメインを使う方法です。
- ほかの網からの要求は I-CSCF から入ります。I-CSCF はこのサブドメインを知っていて、HSS に問い合わせずに、やはり DNS で送ります（TS 24.229 5.3.2.1 step 3）。
- ファクトリの URI は発信側の PSI です（TS 23.228 5.4.12.1）。ここへの INVITE は、作る人のほかの INVITE と同じく、作る人の発信側のフィルタ条件で AS に届きます。

フィルタは `dns.qry.name contains "conf."` と `sip.Contact contains "isfocus"` です。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## フォーカスと混ぜ合わせ

- AS は会議のフォーカスであり、通知サービスでもあります（TS 24.147 4）。参加者それぞれとダイアログを持ち、参加者のメディアのための MRFC とのダイアログも持ちます。
- MRFC は最初の `conf=` の INVITE で混ぜ合わせを作り、そのあとの INVITE ごとにターミネーションを足します。MRFP は、それぞれの参加者にほかの参加者の音声の和を送ります（TS 23.333 6.2.9）。
- 招かれた相手もプリコンディションを使うので、それぞれが MRFP の offer に高信頼の 183 で答え、200 には SDP を付けません。AS はその answer を ACK に入れて MRFC に渡します（RFC 3262 5）。
- キャプチャでは、MRFP から agw への 3 つのストリームと、その逆の 3 つが見えます。ある参加者が受け取る混ぜ合わせた音声は、どの参加者 1 人のトーンよりも大きくなります。
- 会議は作った人が抜けると終わります（TS 24.147 5.3.2.7）。phone1 が切ると、AS は phone2 と phone3 に BYE を送り、参加者ごとの MRFC とのダイアログにも BYE を送ります。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## 会議イベントパッケージ

| 見るもの | 見どころ |
| --- | --- |
| NOTIFY | conference-info の XML（RFC 4575）。参加者ごとに `<user>` と、状態が `connected` の `<endpoint>` があります。 |
| `<user>` の `entity` | 網がその参加者に保証した識別子です。プライバシーを求めた参加者では、匿名の URI です（RFC 4575 8.2）。 |
| `<endpoint>` の `entity` | フォーカスが作るキーで、`urn:uuid:` の URI です。RFC 4575 5.7 はキーの作り方をフォーカスに任せています。 |
| maximum-user-count | 6 人です。7 人目には 403 を返します。 |
| REFER の NOTIFY | REFER はそれぞれ refer イベントの購読を作ります（RFC 3515 2.4.4）。すぐに `100 Trying` を、最終応答は `terminated;reason=noresource` を付けた最後の NOTIFY で知らせます（2.4.7）。 |

- キーに UE の Contact を使わないのはなぜか。Contact はその UE のアドレスで、NOTIFY はほかの網の購読者を含むすべての購読者に届くからです。
- この NOTIFY の内容は、ラボの各カードの **Conference** の行に並びます。

フィルタは `sip.Event contains "conference"` です。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## 会議に戻る

- 会議を抜けた参加者は、会議が続いているあいだなら戻れます。フォーカスの Contact で知った会議の URI に INVITE を送ります（TS 24.147 5.3.1.4.1）。
- この INVITE は、その利用者のほかの発信と同じ道を通ります。S-CSCF が発信側のサービスのために AS に渡し、AS が同じ URI に宛てた新しい INVITE を返すと、S-CSCF は DNS でフォーカスを見つけます。
- フォーカスに INVITE を届けるのはフィルタ条件ではないので、フォーカス宛ての INVITE には P-Served-User がありません。AS はこれで 2 つを見分けます。Request-URI はどちらも同じです。
- フォーカスは、作った人と同じやり方でその参加者を迎え入れます（5.3.2.4.1）。NOTIFY はその参加者を再び `connected` として並べ、joining-method は `dialed-out` ではなく `dialed-in` になります。
- どの会議にも使われていない会議の URI への INVITE には、404 を返します。作った人が戻ろうとしても同じです。作った人が抜けた時点で会議は終わり、その URI も手放しているためです（5.3.2.7）。

> 手順書 [「会議に戻る」](../../../docs/guide/ja/mrf.md#会議に戻る)。e2e のスクリプトは動かしません。`internal/mmtel` の `TestRejoinWithTheConferenceURI` が 1 つのプロセスで動かします。

---

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.r-uri contains "annc@"` | 音声ガイダンスや呼び出し音を頼む要求。 |
| `sip.r-uri contains "conf="` | MRFC の会議に加わる参加者。 |
| `sip.P-Early-Media` | early media を認める応答と UPDATE。 |
| `sdp.media_attr == "content:g.3gpp.cat"` | 呼び出し音の SDP。 |
| `sip.Alert-Info contains "call-waiting"` | 待たせる着信の 180。 |
| `sip.Event contains "conference"` | 会議イベントパッケージの SUBSCRIBE と NOTIFY。 |
| `rtp && ip.addr == 172.28.0.86` | MRFP の RTP。RTP のヒューリスティックを有効にして使います。 |
| `http && ip.addr == 172.28.0.86` | Mp を流れる MRFP の制御の API。 |

- 手順書にはこのほかに 4 つあります。会議の Replaces、フォーカスの応答、フォーカスへの経路を引く DNS の問い合わせ、CAT だけを選ぶフィルタです。
- e2e のスクリプトは、手順書の表のフィルタをすべてキャプチャに当てます。フレームが 1 つも見つからないフィルタがあれば失敗します。

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/mrf.md#wireshark-の表示フィルタ)

---

## ラボから動かす

```sh
docker compose --profile phones up -d --build
```

1. Subscribers の欄で、加入者 2 の **Alerting tone (CAT)** の列に `cat1` を選びます。`phone1` で `phone2` にかけると、`phone1` の Session の行に、網からの early media で呼び出したと表示されます。
2. もう一度 `phone1` で `phone2` にかけて応答させ、次に `phone3` で `phone1` にかけます。`phone1` のカードに、新しい着信が `waiting` と表示されます。
3. **Answer** を押すと、`phone2` との通話を保留にして `phone3` に応答します。**Swap** で通話を切り替えます。
4. **Merge** を押すと会議ができます。3 台のカードの **Conference** の行に、3 人の利用者が並びます。

- 通話のラダー図には、通話のダイアログと並んで、AS と MRFC のダイアログも描かれます。どちらも P-Charging-Vector の icid-value が同じだからです。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| MRFC | PCMU だけで、トランスコードをしない。PCMU の無い offer には Warning を付けた 488 を返す。用意してある音声だけを流し、Cr が無い。Contact はその台の URI で、GRUU ではない。 |
| MRFP | 混ぜ合わせは PCMU だけを扱い、ジッタのために 3 フレームだけをためる。CSRC の一覧を書かず、RTCP も送らない。 |
| AS。呼び出し音 | プリコンディションの付いた着信者の 183 をそのまま発信者に渡し、発信者のプリコンディションを確かめずに呼び出し音を始める。 |
| AS。会議 | AS が受け持つ利用者は誰でも会議を作れ、会議のポリシーが無い。参加者を外すようフォーカスに頼む REFER は 403 で断る。 |

```sh
grep -rn "SIMPLIFIED:" internal/mmtel internal/mrfc internal/netann internal/agw
```

- PCMU だけなのはなぜか。会議で混ぜるのは復号した音声で、ここには AMR の復号器が無いからです。UE シミュレータは PCMU を offer に入れます。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/mrf.md#このプロジェクトで簡略化していること)。項目ごとの仕様の条項と理由は手順書の表にあります。

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| AS と MRFC のダイアログ | `internal/mmtel` の `mrf.go`、`announcement.go` |
| 着信側の AS の呼び出し音 | `internal/mmtel` の `cat.go`、`catdata.go` |
| AS の CW | `internal/mmtel/waiting.go` |
| フォーカスと会議イベントパッケージ | `internal/mmtel` の `conference.go`、`conferenceevent.go` |
| MRFC と、RFC 4240 の Request-URI | `internal/mrfc`、`internal/netann` |
| MRFP。ターミネーション、音声ガイダンス、混ぜ合わせ | `internal/agw` |
| Swap、Merge と、会議の UE の側 | `internal/ue` の `phone_conference.go`、`conference.go` |

- `internal/mmtel` の `mrf_network_test.go` と `conference_network_test.go` が、同じ流れを 1 つのプロセスの中の IMS 全体で動かします。

> 手順書 [「コードの場所」](../../../docs/guide/ja/mrf.md#コードの場所)

---

[layout: exercise]

## 演習

1. UE 1 の発信を規制して UE 2 にかけます。AS から MRFC への ACK から、MRFC の BYE までの時間を測ります。何秒で、それを決めているのは何でしょうか。
2. 加入者 2 に呼び出し音 `cat1` を設定してかけます。呼び出し音だと示す SDP の属性と、それを認めるヘッダはどれでしょうか。
3. 会議のあいだ、MRFP と agw の間の RTP のストリームを数えます。どちら向きにも 3 つあるのはなぜでしょうか。
4. 会議イベントパッケージの NOTIFY を開きます。`<endpoint>` の `entity` が、その UE の Contact ではないのはなぜでしょうか。
5. 終わった会議の URI に発信します。何が返るでしょうか。

1 では、「実行する」のスライドの形で `ue xcap` と `ue call` を実行します。

---

[layout: answers]

## 演習の答え

1. 1.08 秒ほどです。`notice` の WAV ファイルの長さで、再生が終わると MRFC が BYE を送ります。
2. 示すのは `a=content:g.3gpp.cat`、認めるのは `P-Early-Media: sendonly` です。
3. 参加者が 3 人だからです。それぞれが自分の音声を MRFP に送り、ほかの 2 人の和を受け取ります。
4. Contact はその UE のアドレスで、NOTIFY はほかの網の購読者を含む、会議のすべての購読者に届くからです。
5. 404 です。作った人が抜けた時点で会議は終わり、その URI も残っていません。

---

[layout: exercise]

## 振り返り

1. 何を流すかを決めるのはどのノードで、流すのはどのノードですか。
2. 音声ガイダンスを頼む INVITE に SDP が付かないのはなぜですか。
3. 通話に応答する前に、発信者はどうやって音声を聞くのですか。
4. AS が PRACK を待ってから MRFC に ACK を送るのはなぜですか。
5. AS は、フォーカス宛ての INVITE と、利用者のサービスのための INVITE をどうやって見分けますか。
6. MRFP は会議の参加者それぞれに何を送りますか。

---

[layout: answers]

## 振り返りの答え

1. 決めるのは MMTel AS で、Mr' で頼みます。MRFC がその要求を受け、MRFP が音声を作ります。
2. MRFC が MRFP のターミネーションの offer を 200 で返し、AS が ACK で答えるためです。こうして AS が再生を始める時機を決められます。
3. AS が発信者の offer に MRFP の SDP で自分から答え、`P-Early-Media: sendonly` で early media を認めるからです。
4. 発信者が SDP を受け取る前に、音声ガイダンスのフレームが失われないようにするためです。
5. P-Served-User で見分けます。フォーカスに INVITE を届けるフィルタ条件は無いので、フォーカス宛てのものには付きません。
6. ほかの参加者が送る音声の和です。参加者ごとに 1 つずつ作ります。

---

## 次の回

- 次は VoLTE の回です。アクセス網、IMS-AKA、Rx でのポリシー制御、VoLTE の通話を扱います。
- 網どうしの境界と、参加者が両方の網にいる会議は、最後の回で [網間接続の手順書](../../../docs/guide/ja/interconnect.md) と一緒に読みます。

```sh
docker compose --profile ue --profile phones down -v --remove-orphans
```

- このコマンドは、両方のプロファイルの UE とノードを止め、イメージは残します。次の回を、ビルドし直さずに始められます。

> 手順書の一覧は [docs/guide/](../../../docs/guide/) にあります。
