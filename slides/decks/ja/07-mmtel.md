---
title: MMTel AS の付加サービス
footer: ims-practice 勉強会 7. 付加サービス
lang: ja
---

# MMTel AS の付加サービス

ims-practice 勉強会 第 7 回（MMTel AS、Ut/XCAP での設定、キャプチャで見るサービス）

---

[layout: summary]

## この回で分かること

最初の問い。着信を転送している電話にかけると、別の電話が鳴ります。それを決めているのは、どのノードでしょうか。

- 付加サービスとは何か。どのノードで動くか。
- S-CSCF が通話を AS に渡すしくみ。iFC、P-Served-User、ODI。
- AS が B2BUA として通話を 3 つのダイアログに分けること。
- 利用者の設定がどこにあり、UE が Ut の XCAP でそれをどう変えるか。
- サービスごとに、キャプチャで何を見るか。識別子、着信転送、規制。

> 手順書 [MMTel AS の付加サービス](../../../docs/guide/ja/mmtel.md)（`docs/guide/ja/mmtel.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.229 V20.1.0、TS 24.604 V19.0.0、TS 24.607 V19.1.0、TS 24.611 V19.0.0、TS 24.623 V19.0.0、TS 29.364 V19.1.0、GSMA IR.92 v24.0 などです。

---

## 付加サービスはどこで動くか

- 付加サービスは、電話網が通話のほかに提供してきたものです。番号の通知と非通知、着信転送、規制、保留、割込通話などです。
- IMS の電話は MMTel のサービスです（TS 24.173）。網が何を提供するかは GSMA IR.92 2.3 にあります。
- CSCF は経路を決めるだけです。利用者の設定を知って通話を変えるのは、アプリケーションサーバー（AS）の MMTel AS です。

```text
 UE --- P-CSCF --- S-CSCF ===(SIP, iFC)===> MMTel AS ---(Sh)--- HSS
  |                                            |
  +---------------(Ut, HTTP + XCAP)------------+
```

- S-CSCF と AS の間の SIP の参照点を ISC と呼びます。`mmtel` の役割は、AS として SIP の 5060 番、XCAP のサーバーとして HTTP の 80 番で待ちます。
- IR.92 が網には任意としている MWI と ECT は作っていません。音声ガイダンスを流すのも会議の音声を混ぜるのも MRF です。Compose では `mrfc` と `mrfp` として動かしています。

> 手順書 [「MMTel と、AS が要る理由」](../../../docs/guide/ja/mmtel.md#mmtel-とas-が要る理由)

---

## 登場するノード

| ノード | アドレス | 役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 発信者。`tel:+12025550101`。 |
| UE 2 | 172.28.0.101 | サービスを設定する利用者。`tel:+12025550102`。 |
| UE 3 | 172.28.0.103 | UE 2 の着信の転送先。`tel:+12025550103`。 |
| P-CSCF、S-CSCF、I-CSCF | 172.28.0.40、30、20 | 識別子を示し、隠す。iFC で AS に送る。 |
| MMTel AS | 172.28.0.75 | 役割 `mmtel`。SIP は `mmtel.…`、Ut は `xcap.…`。 |
| HSS | 172.28.0.10 | iFC（Cx）。設定と Ut のパスワード（Sh）。 |

> 手順書 [「登場するノード」](../../../docs/guide/ja/mmtel.md#登場するノード)

---

## 要求が AS に届くしくみ。4 つのフィルタ条件

| 優先度 | 一致する要求 | DefaultHandling |
| --- | --- | --- |
| 10 | INVITE、ORIGINATING_REGISTERED | SESSION_TERMINATED |
| 11 | INVITE、TERMINATING_REGISTERED | SESSION_CONTINUED |
| 12 | INVITE、TERMINATING_UNREGISTERED | SESSION_CONTINUED |
| 13 | INVITE、ORIGINATING_CDIV | SESSION_TERMINATED |

- 送り先はどれも `sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060` です。
- 発信側は、AS が答えないと通話を終えます。規制を確実に効かせるためです。
- SMS の 2 つの条件（優先度 0 と 1）のあとに並びます。どれも 1 つのセッションケースにしか一致しないので、互いに競いません。TERMINATING_UNREGISTERED の条件は CFNL のためのものです。

> 手順書 [「4 つのフィルタ条件」](../../../docs/guide/ja/mmtel.md#4-つのフィルタ条件)。TS 29.228 Annex E、B.2.3。TS 24.229 5.4.3.2 step 5、5.4.3.3 step 4。

---

## 発信側か着信側か。P-Served-User

```text
P-Served-User: <sip:+12025550101@…>;sescase=orig;regstate=reg
P-Served-User: <sip:+12025550102@…>;sescase=term;regstate=reg
P-Served-User: <sip:+12025550102@…>;sescase=term;regstate=unreg
P-Served-User: <sip:+12025550102@…>;orig-cdiv;regstate=reg
```

| 場合 | AS が動かすサービスの順 |
| --- | --- |
| orig | OCB、OIR、応答に TIP |
| term | ICB と ACR、CFU と CFNL、OIP と TIR、利用者への通話。終わり方で CFB か CFNR |
| orig-cdiv | 転送元の利用者の OCB |

フィルタは `sip.P-Served-User` です。

- 1 つの AS が通話の両端を受け持つので、どの INVITE が、どの利用者の、どのケースで来たかを知る必要があります。P-Served-User の無い INVITE は S-CSCF から来たものではないので、AS は 403 を返します。

> 手順書 [「P-Served-User と session case」](../../../docs/guide/ja/mmtel.md#p-served-user-と-session-case)。RFC 5502、RFC 8498。TS 24.229 5.7.1.3A。

---

## コードで見る順番。着信側

[internal/mmtel/sessioncase.go](../../../internal/mmtel/sessioncase.go)

```go
	info := c.callInfo(fields, inv.RequestURI)
	if c.barIncoming(info) {
		return
	}
	// CFU, and CFNL for a user who is not registered (TS 24.604 4.6.7).
	if d, ok := c.divertAtSetup(info); ok {
		c.divert(d)
		return
	}
```

- 規制が先です。規制した通話は転送しません。
- CFU と CFNL は、INVITE が届いた時点で転送します。利用者を呼び出しません。
- このあとに OIP と TIR が続きます。そのあと `c.attempt` が利用者への区間を試し、その終わり方で CFB、CFNRc、CFNR が決まります。コメントの条項は、サービスどうしの関係を定めた条項です。

---

## ODI と、1 つの通話に 3 つのダイアログ

```text
Route: <sip:mmtel.…:5060;lr>,<sip:scscf.…:5060;lr;odi=...>
```

```text
 UE 1 ... S-CSCF -- AS(UE 1) -- S-CSCF ... I-CSCF ... S-CSCF -- AS(UE 2) -- S-CSCF ... UE 2
 |<------ C1 ------>|<------------------ C2 ------------------->|<--------- C3 ---------->|
```

- AS は routeing B2BUA です。受けた INVITE に UA として答え、自分の Call-ID で新しい INVITE を送ります。
- 新しい INVITE は ODI を持って S-CSCF に戻り、S-CSCF は次の条件から続けます。SMS の回の IP-SM-GW は何も送り返しませんでしたが、MMTel AS は送り返します。
- プロキシではなく B2BUA なのはなぜでしょうか。CFNR、To を変える転送、From を匿名にする OIR、音声ガイダンス、会議が、どれも B2BUA を必要とするからです。

> 手順書 [「オリジナルダイアログ識別子」](../../../docs/guide/ja/mmtel.md#オリジナルダイアログ識別子) と [「プロキシではなく B2BUA」](../../../docs/guide/ja/mmtel.md#プロキシではなく-b2bua1-つの通話に-3-つのダイアログ)。TS 24.229 5.7.5、5.7.5.2.1。

---

## 3 つのダイアログがキャプチャに与えるもの

| 見るもの | 見どころ |
| --- | --- |
| Call-ID | 1 つの通話に 3 つ。転送した通話ではさらに 3 つ増える。 |
| P-Charging-Vector の icid-value | 網の中では 3 つとも同じ。ラボはこれで 1 つの通話にまとめる。 |
| LIR | ダイアログ C2 のもの。 |
| PRACK と ACK | ダイアログごとに進む。ACK が 200 より先に UE 2 に届くことがある。 |
| BYE | AS が答え、同じ Reason を付けた新しい BYE を次に送る。 |

フィルタは `sip.P-Charging-Vector contains "icid-value=..."` です。`...` には、UE 1 の P-CSCF が S-CSCF に送る INVITE から写した値を入れます。

- このヘッダは P-CSCF で止まります。UE は付けず、P-CSCF は UE に送る要求と応答のすべてから消します。ims0 ブリッジのキャプチャには UE との区間も入っていますが、このフィルタでは出てきません。
- Wireshark の **VoIP Calls** は、ダイアログを 1 つずつ別の通話として並べます。1 つの通話が、一見 3 つに見えます。

> 手順書 [「プロキシではなく B2BUA」](../../../docs/guide/ja/mmtel.md#プロキシではなく-b2bua1-つの通話に-3-つのダイアログ)。TS 24.229 5.7.5.1、5.2.1。RFC 3326 2。

---

## 設定はどこにあるか。simservs の文書

- 利用者ごとに XML の文書が 1 つあります。XCAP の simservs です（TS 24.623、RFC 4825）。
- 置き場所は HSS だけです。Sh の repository data で、Service-Indication は `MMTEL-Services` です（TS 29.364 7.2）。
- AS は通話ごとに UDR で読み、写しを持ちません。AS のどのインスタンスでも、どの通話でも受けられるようにするためです。

[deploy/compose/hss.yaml](../../../deploy/compose/hss.yaml)

```xml
            <cp:rule id="cfu">
              <cp:conditions><rule-deactivated/></cp:conditions>
              <cp:actions><forward-to><target/></forward-to></cp:actions>
            </cp:rule>
```

- 最初の文書には、IR.92 が求めるサービスすべての規則があります。CDIV と CB の規則は `rule-deactivated` で止めてあり、転送先もありません。UE は規則を足しません。ある規則を変えて、サービスを有効にします。
- 文書のアドレスは `http://xcap.ims.mnc001.mcc001.3gppnetwork.org/simservs.ngn.etsi.org/users/<XUI>/simservs.xml` です。XUI は利用者の公開識別子です。

> 手順書 [「文書」](../../../docs/guide/ja/mmtel.md#文書)。GSMA IR.92 2.3.2 Note 3。

---

## Ut で変える。CFU を有効にする

`ue xcap set cfu on tel:+12025550103` が送るもの。

| 順 | 要求 | 応答と、その間の Sh |
| --- | --- | --- |
| 1 | GET。資格情報なし | UDR は無し。401 とチャレンジ 2 つ（SHA-256、MD5）。どの利用者でも同じ。 |
| 2 | GET。`Authorization: Digest` | UDR が 2 つ。Ut のパスワードと文書。200 と `ETag: "0"`。 |
| 3 | 規則 1 つの PUT。`~~` のノードセレクタ、`If-Match: "0"` | PUR に `<SequenceNumber>1</SequenceNumber>`。PUA のあと 200 と `ETag: "1"`。 |
| 4 | GET | 設定として表示する文書。 |

- 途中で文書が変わっていれば、If-Match が合わず、AS は PUR を送らずに 412 を返します。ETag は、HSS が更新のたびに確かめる Sequence Number です。読んでから PUR までの間にほかの書き込みが入ると、HSS が DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC で PUR を断り、AS はやはり 412 を返します。
- フィルタは `http && ip.addr == 172.28.0.75` です。`http.www_authenticate` でチャレンジを、`http.request.method == "PUT"` で変更を探します。PUT の URI とボディの全体は手順書にあります。

> 手順書 [「1 回の変更。CFU を有効にする」](../../../docs/guide/ja/mmtel.md#1-回の変更cfu-を有効にする)。RFC 7616 3.3。TS 24.623 5.2.3.2.1。RFC 4825 6、7.11。TS 29.328 7.6.1、6.1.2.1。

---

## コードで見る HTTP Digest のチャレンジ

[internal/xcap/digest.go](../../../internal/xcap/digest.go)

```go
func (n nonces) challenge(h http.Header, realm string, offered []Algorithm, stale bool) {
	nonce := n.issue()
	for _, a := range offered {
		h.Add("WWW-Authenticate", sip.Challenge{
			Realm: realm, Nonce: nonce, Stale: stale, Algorithm: string(a), QOP: []string{qopAuth},
		}.String())
	}
}
```

- 最初のチャレンジは、ホームドメインで `algorithms`（SHA-256、MD5 の順）を示します。利用者によらないので、AS は利用者について何も読まずに送り、どの XUI でも同じになります。どのチャレンジも同じ nonce を使います。
- 認証できなかった資格情報のあとは、`realm` と `offered` が利用者の資格情報のものになります。運用者がほかの realm や MD5 だけで与えたのでなければ、同じです。
- 資格情報は HSS の repository data です。Service-Indication は `Ut-Digest-Credential` です。入っているのはパスワードではなく HA1 の値です（`internal/xcap/credential.go` のコメント）。
- WWW-Authenticate は SIP Digest と同じ形なので、コードは `internal/sip` の `Challenge` をそのまま使います。登録の回の 401 と比べてみます。

---

## 実行する

```sh
docker compose up -d --build
docker compose run --rm ue2 xcap \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2 set cfu on tel:+12025550103

# UE 3 が着信を待つ
docker compose run --rm ue3 answer -impi 001010000000003@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org -password test-password-3
docker compose run --rm ue call tel:+12025550102
```

- UE 1 のログに `"msg":"call progress","status":181` と `"msg":"call answered"` が出ます。
- 止めるときは `set cfu off` です。`ue xcap` は登録を必要としません。ほかのコマンドは `get`、`set SERVICE on|off [TARGET]`、`oir restricted|not-restricted`、`noreply SECONDS` です。
- `ue2` のサービスはアドレスが決まっているので、ほかの `ue2` のコンテナが動いている間は `ue2 xcap` を実行できません。

> 手順書 [「動かしてみる」](../../../docs/guide/ja/mmtel.md#動かしてみる)。全部のサービスを、`scripts/` にある付加サービスの e2e のスクリプトが順に行います。

---

## キャプチャを開く

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (http && ip.addr == 172.28.0.75) || diameter.cmd.code in {301, 302, 306, 307}
```

- 通話、Ut、AS の Sh の読み出しと更新、LIR と SAR が残ります。
- **Telephony > VoIP Calls** は、ダイアログを 1 つずつ別の通話として並べます。
- e2e のスクリプトは、すべてのサービスを 1 つのキャプチャに残します。表示フィルタで 1 つのサービスに絞ります。

| サービス | きっかけ | キャプチャで見るもの |
| --- | --- | --- |
| OIR | 発信者の既定が非通知 | 匿名の From、`Privacy: id` |
| CFU、CFB、CFNR、CFNL | 着信、486、無応答、未登録 | 181、History-Info、`cause=` |
| BAIC、ACR、BAOC | 着信、匿名の着信、発信 | 183 で音声ガイダンス、そのあと 603 か 433 |

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/mmtel.md#キャプチャを開く)

---

## AS を通る通常の通話と TIP

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF から AS への C1 の INVITE | `sescase=orig` の P-Served-User と、`odi` の付いた Route。 |
| AS の UDR | Data-Reference 0、Service-Indication `MMTEL-Services`。 |
| AS の C2 の INVITE | 同じ icid-value。このあと ENUM と LIR。 |
| 2 回目の AS | `sescase=term` で届き、C3 を送り返す。 |
| 200 OK | P-Asserted-Identity に UE 2。これが TIP。 |

- 既定の文書では、AS は通話を何も変えません。それでも、どの INVITE も AS を 2 回通ります。どの利用者も既定で TIP を持ち、P-CSCF が P-Called-Party-ID を P-Asserted-Identity にします。
- 手順書のフレーム番号はスクリプトを 1 回動かしたときのものなので、手元のキャプチャとは違います。

> 手順書 [「AS を通る通常の通話」](../../../docs/guide/ja/mmtel.md#as-を通る通常の通話)。TS 24.229 5.4.3.2 step 5、5.7.5.2.1、5.2.6.4.4 step 1。TS 29.364 7.2.0。TS 24.608 4.5.2.4。

---

## OIR。発信者が識別子を隠す

`ue xcap oir restricted` で、UE 1 の既定を非通知にしてから発信します。

| ノード | すること |
| --- | --- |
| UE 1 の AS | `Privacy: id` を足し、From を `"Anonymous" <sip:anonymous@anonymous.invalid>` にする。P-Asserted-Identity は残す。 |
| UE 2 の AS | OIP を動かす。`id` を残す。 |
| UE 2 の S-CSCF | P-Asserted-Identity を取り除く。UE は信頼ドメインの外にある。 |

フィルタは `sip.Privacy` と `sip.from.addr == "sip:anonymous@anonymous.invalid"` です。

- 網の中では、識別子は確かめられたまま残ります。隠すのは、信頼の範囲の外にある UE に対してだけです。OIR は一時モードなので、UE は `Privacy: none` で 1 回だけ番号を通知できます。
- TIR は同じしくみの逆向きです。UE 2 の AS が応答の Privacy に `id` を入れ、UE 1 の P-CSCF が P-Asserted-Identity を取り除きます（TS 24.608 4.5.2.9、RFC 3325 7）。

> 手順書 [「OIR。発信者が識別子を隠す」](../../../docs/guide/ja/mmtel.md#oir発信者が識別子を隠す)。TS 24.607 4.5.2.4、4.5.2.9。RFC 3323 4.1.1.3。TS 24.229 5.4.3.3 step 12、4.4.2。RFC 3325。

---

## CFU。すべての着信を転送する

| 見るもの | 見どころ |
| --- | --- |
| UE 2 の AS | INVITE が届いた時点で転送する。UE 2 には届かない。 |
| 181 Call Is Being Forwarded | 発信者への通知。転送先の項目に `Privacy=history`。 |
| 転送の INVITE（C4）の Request-URI | `sip:+12025550103@…;user=phone;cause=302` |
| History-Info | index 1 が UE 2、index 1.1 が UE 3 で `mp=1`。 |
| S-CSCF から AS への INVITE | `orig-cdiv`。UE 2 の発信の規制がここで掛かる。 |

- 転送は 1 つの通話で 5 回までです。超えると `Warning: 399` を付けた 480 です。
- cause は CFU が 302、CFB が 486、CFNR が 408、CFNL が 404、CFNRc が 503 です（TS 24.604 Annex C）。手順書のラダー図にはダイアログが C1 から C6 まで 6 つあります。
- UE 3 の項目に `Privacy=history` が付くのはなぜでしょうか。UE 3 が番号の表示を許すかどうかを、UE 2 の AS は知らないからです。

フィルタは `sip.History-Info`、`sip.r-uri contains "cause="`、`sip.Status-Code == 181` です。

> 手順書 [「CFU。すべての着信を転送する」](../../../docs/guide/ja/mmtel.md#cfuすべての着信を転送する)。TS 24.604 4.5.2.6.2、4.5.2.6.3、4.5.2.6.4、4.5.2.6.1。RFC 4458。RFC 7044 10.3。TS 24.229 5.4.3.3 step 3。

---

## CFB と CFNR。話し中と無応答

| 場合 | 見どころ |
| --- | --- |
| CFB | UE 2 が C3 に 486。AS は ACK を返し、181 を送り、`cause=486` で転送する。 |
| CFNR のタイマー | 最初の 180 で始まる。長さは `NoReplyTimer`。e2e では 5 秒。 |
| CFNR の CANCEL | `Reason: SIP;cause=408;text="Request Timeout"`。途中の CSCF も写す。 |
| 199 Early Dialog Terminated | C2 で UE 2 の初期ダイアログを終える。UE 1 には届かない。 |
| そのあと | 181 を送り、`cause=408` で転送する。 |

フィルタは `sip.Method == "CANCEL" && sip.Reason` と `sip.Status-Code == 199` です。

- NoReplyTimer が無ければ、事業者の既定の 20 秒になります。キャプチャで 180 から CANCEL までの時間を測ってみます。
- UE 1 の AS は UE 1 との初期ダイアログを 1 つのまま保つので、UE 1 は 181 と UE 3 の応答を同じ初期ダイアログで受けます。

> 手順書 [「CFB」](../../../docs/guide/ja/mmtel.md#cfb着信者が話し中) と [「CFNR」](../../../docs/guide/ja/mmtel.md#cfnr誰も応答しない)。TS 24.604 4.5.2.6.2.2 b、4.5.2.6.3 item 2、4.8.1。RFC 3326 2。RFC 6228。

---

## CFNL。着信者が登録していない

| 順 | 見るもの | 見どころ |
| --- | --- | --- |
| 1 | I-CSCF の LIR | DIAMETER_UNREGISTERED_SERVICE（2003）と S-CSCF の能力。 |
| 2 | S-CSCF の SAR | UNREGISTERED_USER でプロファイルを取る。 |
| 3 | AS への INVITE | `sescase=term;regstate=unreg` |
| 4 | AS の転送 | `<not-registered/>` の規則で、すぐに `cause=404`。 |

- CFNL が無ければ、AS は INVITE をそのまま返し、I-CSCF ではなく S-CSCF が 480 を返します。
- LIA が 2003 なのは、どの試験用の加入者にも未登録のときのサービス（TERMINATING_UNREGISTERED の MMTel AS の iFC）があるからです。それが無い加入者なら DIAMETER_ERROR_IDENTITY_NOT_REGISTERED になり、I-CSCF が 480 を返します。

フィルタは `diameter.Experimental-Result-Code == 2003` と `sip.P-Served-User contains "regstate=unreg"` です。

> 手順書 [「CFNL。着信者が登録していない」](../../../docs/guide/ja/mmtel.md#cfnl着信者が登録していない)。TS 29.228 6.1.4.1 step 3、6.7、6.1.2.1。TS 24.229 5.4.3.3。TS 24.604 4.5.2.6.2、4.6.7。

---

## 規制

| サービス | 設定 | 見どころ |
| --- | --- | --- |
| BAIC | UE 2 の `baic on` | UE 2 の AS が 603 Decline。ほかのどのサービスよりも先。 |
| ACR | `<anonymous/>` の規則 | 433 Anonymity Disallowed。 |
| BAOC | UE 1 の `baoc on` | UE 1 の AS が 603。自分の INVITE を送らない。緊急通報は規制しない。 |
| BOIC、BOIC-exHC | `boic on` など | 着信者の国番号をホーム網の `1` と比べる。 |

- 603 や 433 の前に、発信者には音声ガイダンスが流れます。AS は MRFP の SDP と `P-Early-Media: sendonly` を付けた 183 を、高信頼の暫定応答で返します。MRFC が BYE で音声ガイダンスを終えると、`Reason: SIP;cause=603` を付けた 603 が続きます。
- 603 は `sip.Status-Code == 603` で探します。BAOC のときは C2 がありません。ホーム網の国番号は `deploy/compose/mmtel.yaml` にあります。

> 手順書 [「規制」](../../../docs/guide/ja/mmtel.md#規制)。TS 24.611 4.5.2.6.1、4.6.7、4.5.2.4.1、4.9.1.4。RFC 5079。

---

## CW と HOLD

- IR.92 は、割込通話を網ではなく UE で行うよう求めています（2.3.4）。
  - 着信者の UE が `Alert-Info: <urn:alert:service:call-waiting>` の 180 を送ります。
  - AS はそれを渡すか、事業者の設定で Alert-Info を取り除きます。
- HOLD は `a=sendonly` の re-INVITE です。AS はもう一方の側にそのまま渡します。
- `ue serve` は、通話中に 2 本目の着信を受けられます。その Alert-Info で呼び出し、`POST /answer` で 1 本目を保留にして、待っている着信に出ます。CW は `scripts/e2e-mrf.sh` がキャプチャで見せます。1 つのプロセスの試験でも確かめています。

```sh
go test -run 'TestCommunicationWaiting|TestHoldThroughTheIMS' -v ./internal/mmtel
go test -run TestServeTwoCalls -v ./internal/ue
```

- `ue serve` は 3 本目の着信に 486 で答えます。ラボの **Hold** のボタンも HOLD を送ります。

> 手順書 [「CW と HOLD」](../../../docs/guide/ja/mmtel.md#cw-と-hold)。TS 24.615 4.5.5.3.2、4.5.5.2.3。RFC 7462。TS 24.610 4.5.2.1。

---

## IR.92 が求めること

| IR.92 | 内容 | このプロジェクト |
| --- | --- | --- |
| 2.3 | VoLTE の網が提供する付加サービス | OIP、OIR、TIP、TIR、CDIV、CB、HOLD、CW |
| 2.3.2 | Ut の XCAP で設定する。1 回に規則 1 つ。If-Match | `ue xcap` |
| 2.3.2 Note 3 | 事業者が最初の文書に規則を入れておく | hss.yaml の止めた規則 |
| 2.3.4 | CW は UE で行う | AS は Alert-Info を渡す |
| 2.3.5、2.3.11 | MWI と ECT は網の対応が任意 | 作らない |
| 2.2.2 | GBA は推奨 | 作らない。Digest |

- このプロジェクトは、3GPP の共通の部分に従います。国ごとの違い（日本では TTC）は網どうしの接続にかかわるので、接続する相手の網ごとのプロファイルに置きます。網間接続の手順書で扱います。

> 手順書 [「MMTel と、AS が要る理由」](../../../docs/guide/ja/mmtel.md#mmtel-とas-が要る理由) と [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/mmtel.md#このプロジェクトで簡略化していること)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| AS | 181 で音声ガイダンスを流さない。設定を購読せず、通話ごとに読む。BIC-Roam は成り立たない。 |
| XCAP | GBA が無い。キャプチャで XML を読めるように平文の HTTP。nonce count の繰り返しに気づかない。xcap-diff の購読が無い。 |
| simservs | 一部の条件と serv-cap が無い。国は国番号で決め、国番号 1 の国々は 1 つになる。 |
| CSCF と HSS | History-Info の非通知を掛けない。公開サービス識別子と ORIGINATING_UNREGISTERED が無い。 |

```sh
grep -rn "SIMPLIFIED:" internal/mmtel internal/xcap internal/simservs internal/config/xcap.go cmd/ue/xcap.go
```

- 出力を手順書の表と 1 つずつ突き合わせます。CSCF と HSS の項目は `internal/scscf`、`internal/icscf`、`internal/hss` にあります。
- nonce count を確かめないのは、複数のインスタンスで動かすための代わりです。数を覚えるには、どのインスタンスも共有する Store が要ります。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/mmtel.md#このプロジェクトで簡略化していること)。項目ごとの仕様の条項と理由は手順書の表にあります。

---

## ラボで設定する

```sh
docker compose --profile phones up -d --build
```

1. `phone2` のカードで **Supplementary services (Ut/XCAP)** を開きます。どのサービスも最初は `off` です。
2. **CFU (unconditional)** を選び、転送先を書いて **On** を押します。
3. 一覧に `CFU (unconditional): on to …` と出て、ETag が 1 つ増えます。
4. **Off** で止めます。転送先は消えずに残ります。

- ラダー図には XCAP も Sh も出ません。どちらも SIP の要求には属さないからです。キャプチャには、80 番の HTTP と 3868 番の Diameter として残ります。
- ラボは 1 つの通話のダイアログをすべて 1 つのラダー図に描き、MMTel AS の列も置きます。`phones` と `ue` のプロファイルを同時に動かさないでください。同じ加入者の UE が 2 台になります。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| B2BUA、通話ごとの 2 つの側 | `internal/mmtel` の `mmtel.go`、`call.go` |
| サービスの順番 | `internal/mmtel/sessioncase.go` |
| 識別子、転送、規制、CW | `internal/mmtel` の `identity.go`、`diversion.go`、`barring.go`、`waiting.go` |
| 文書と規則の評価 | `internal/simservs` |
| Ut の XCAP サーバーと Digest | `internal/xcap` |
| UE の `ue xcap` | `internal/ue/xcap.go`、`cmd/ue/xcap.go` |
| P-Served-User、ODI、iFC | `internal/scscf/services.go`、`internal/ifc` |
| HSS の設定と Ut のパスワード | `internal/hss` の `servicedata.go`、`sh.go`、`deploy/compose/hss.yaml` |

- `internal/mmtel/network_test.go` は、すべてのサービスを IMS 全体を通して 1 つのプロセスで動かします。`internal/mmtel/diversion.go` で cause の値を探し、キャプチャの `cause=302` と照らし合わせます。

> 手順書 [「コードの場所」](../../../docs/guide/ja/mmtel.md#コードの場所)

---

[layout: exercise]

## 演習

1. CFNR を設定し、`noreply 5` を `noreply 10` に変えて発信します。180 から CANCEL までの時間を測ります。
2. 転送した通話の icid-value で絞り込み、ダイアログの数を数えます。
3. `boic on` にして、UE 1 から UE 2 に発信します。通話は規制されるでしょうか。
4. `hss.yaml` で、発信側の条件の DefaultHandling だけが SESSION_TERMINATED なのはなぜかを説明します。
5. XCAP の 2 回目の GET の `Authorization` を読み、どちらのアルゴリズムで答えたかを確かめます。

1 と 3 では、「実行する」のスライドの形で、UE 1 か UE 2 として `ue xcap` を実行します。

---

[layout: answers]

## 演習の答え

1. 10 秒ほどです。タイマーは最初の 180 で始まります。
2. C1 から C6 までの 6 つです。
3. 規制されません。UE 2 の番号の国番号は 1 で、ホーム網と同じだからです。
4. 規制を確実に効かせるためです。AS が答えないときに AS を通さず通話を続けると、規制をすり抜けてしまいます。
5. SHA-256 です。最初のチャレンジです。

---

[layout: exercise]

## 振り返り

1. S-CSCF は、INVITE を MMTel AS に送ることを、どうやって知りますか。
2. AS は、その INVITE が発信側のものか着信側のものかを、何で知りますか。
3. 1 回の通話に Call-ID が 3 つあるのは、なぜですか。
4. AS が利用者の設定の写しを持たないのは、なぜですか。
5. 2 人が同時に設定を変えたとき、どちらかの PUT が 412 になるのは、なぜですか。
6. CFU と CFNR で、UE 2 の電話が鳴るのはどちらですか。

---

[layout: answers]

## 振り返りの答え

1. 登録のときの SAA で届いた iFC のうち、優先度 10 から 13 の条件に INVITE が一致するからです。
2. P-Served-User の `sescase` と `orig-cdiv` で知ります。
3. 利用者ごとに AS が経路にいて、B2BUA の AS が新しいダイアログを作るからです。
4. どのインスタンスでもどの通話でも受けられるようにし、AS を複数台で動かせるようにするためです。設定は HSS にだけあります。
5. PUT は、読んだ版の ETag を If-Match に入れて送ります。先に書いた人が Sequence Number を進めたので、版が合いません。AS が If-Match で気づくか、間に合わなければ HSS が PUR を断ります。
6. CFNR です。CFU は INVITE が届いた時点で転送するので、UE 2 は鳴りません。

---

## 次の回

- 次は MRF の回です。通話を断る前の音声ガイダンス、着信側が選ぶ呼び出し音、2 つ目の着信、3 人の会議を扱います。どれを頼むのもこの回の AS で、会議ではフォーカスにもなります。
- その次が VoLTE の回です。VoLTE の通話も、この回の MMTel AS を通ります。

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```

- このコマンドは `ue` と `phones` の両方のプロファイルを止めます。手順書の「後片付け」は `ue` だけを止めます。ラボを使っていなければ、それで足ります。

> 手順書の一覧は [docs/guide/](../../../docs/guide/) にあります。
