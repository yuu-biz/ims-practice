---
title: SMS の流れ
footer: ims-practice 勉強会 5. SMS
lang: ja
---

# SMS の流れ

ims-practice 勉強会 第 5 回（SMS over IP。IP-SM-GW、Sh、3 種類の報告）

---

[layout: summary]

## この回で分かること

最初の問い。ショートメッセージは電話番号に宛てて送ります。その番号の UE を、IMS の中で誰が見つけるのでしょうか。

- 利用者がショートメッセージを受けられることを、IP-SM-GW が知るしくみ。
- S-CSCF が、ショートメッセージを IP-SM-GW に渡す方法。
- メッセージが、もう一方の UE に届くまでの流れ。
- 送った側と受けた側が、メッセージがどうなったかを知る方法。
- MESSAGE のボディの読み方。

> 手順書 [SMS の流れを追う](../../../docs/guide/ja/sms.md)（`docs/guide/ja/sms.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.341 V19.0.0、TS 23.204 V19.1.0、TS 24.011 V20.0.0、TS 23.040 V20.0.0 などです。

---

## SMS over IP の位置づけ

ショートメッセージは、運び方が変わっても中身は同じ 2 つの層でできています。

```text
 SIP MESSAGE   Content-Type: application/vnd.3gpp.sms      TS 24.341
  +-- RP       RP-DATA, RP-ACK, RP-ERROR                   TS 24.011
       +-- TP  SMS-SUBMIT, SMS-DELIVER, SMS-STATUS-REPORT  TS 23.040
            +-- text   GSM 7 bit, UCS2                     TS 23.038
```

- IMS では、SIP の MESSAGE のボディに入れて運びます。
- NAS で運ぶ方法もあります。その場合も、RP と TP は同じです。SIP が置き換えるのは、メッセージを運ぶ部分だけです。
- このプロジェクトは、ショートメッセージを SIP だけで運びます。NAS で運ぶ SMS（MSC、MME、SMSF を通るもの）は作っていません。主題が IMS だからです。

> 手順書 [「SMS over IP の位置づけ」](../../../docs/guide/ja/sms.md#sms-over-ip-の位置づけ)

---

## 登場するノード

| ノード | アドレス | SMS での役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 送信者。`+1-202-555-0101`。 |
| UE 2 | 172.28.0.101 | 受信者。`+1-202-555-0102`。 |
| P-CSCF、S-CSCF、I-CSCF | 172.28.0.40、30、20 | MESSAGE を中継する。S-CSCF は iFC を評価する。 |
| IP-SM-GW | 172.28.0.70 | SMS over IP のアプリケーションサーバー（AS）。 |
| SC（スタブ） | IP-SM-GW の中 | ショートメッセージを受け取って配達する。 |
| HSS | 172.28.0.10 | iFC を渡し（Cx）、IP-SM-GW に Sh で答える。 |

- SC のアドレスは `+12025550100`、IP-SM-GW の番号は `+12025550199` です。
- CSCF は SIP を中継するだけです。ショートメッセージには、SIP と SMS の網の言葉の両方を話すノードが要ります。それが IP-SM-GW です。
- ここには SMS の網が無いので、SC は IMS の側を本物にしたスタブです。MESSAGE、Sh、報告は、本物の IP-SM-GW が送るものと同じです。すぐに届けられないメッセージは捨てます。蓄積と再送は、インタフェース `ServiceCentre` の後ろに置く部分です。

> 手順書 [「登場するノード」](../../../docs/guide/ja/sms.md#登場するノード) と [「AS が要る理由と SC の役割」](../../../docs/guide/ja/sms.md#as-が要る理由と-sc-の役割)。TS 24.341 5.3.3。TS 23.204 5.2.4。

---

## S-CSCF が IP-SM-GW を選ぶしくみ

S-CSCF は SMS を知りません。どの要求をどの AS へ送るかは、HSS が渡す iFC で決まります。

| 優先度 | 一致する要求 | 送り先 |
| --- | --- | --- |
| 0 | REGISTER のすべて | IP-SM-GW。利用者の MSISDN を付ける。 |
| 1 | 登録済みの利用者が送る MESSAGE で、Content-Type が `application/vnd.3gpp.sms` のもの | IP-SM-GW |
| 2 | 登録済みの利用者に宛てた同じ MESSAGE。IP-SM-GW が送ったものは除く | IP-SM-GW |

- iFC は `deploy/compose/hss.yaml` にあり、登録のときの SAA の User-Data で S-CSCF に届きます。手順書に YAML と XML を並べてあります。
- XML の ConditionTypeCNF が条件の組み合わせ方を決めます。同じ Group の SPT は OR、Group どうしは AND です。

> 手順書 [「フィルタ条件」](../../../docs/guide/ja/sms.md#フィルタ条件)。TS 29.228 B.2.2、B.2.3、Annex E。

---

## 利用者に宛てたショートメッセージ。否定の条件

`deploy/compose/hss.yaml` の優先度 2 の条件には、4 つの組があります。

```text
     メソッドが MESSAGE
 かつ Content-Type が application/vnd.3gpp.sms
 かつ セッションケースが TERMINATING_REGISTERED
 かつ P-Asserted-Identity が sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org ではない
```

- IP-SM-GW は、利用者に送る MESSAGE の P-Asserted-Identity に自分の URI を入れます。否定の条件があるので、その MESSAGE は IP-SM-GW に戻らず、ループになりません。
- この条件に一致するのは、ほかの網から届いたショートメッセージです。そのあと、利用者の IP-SM-GW が届けます。

> 手順書 [「フィルタ条件」](../../../docs/guide/ja/sms.md#フィルタ条件) と [IMS 網どうしを相互接続する「網をまたぐショートメッセージ」](../../../docs/guide/ja/interconnect.md#網をまたぐショートメッセージ)。TS 23.218 5.2.3 NOTE 5。TS 23.204 6.4a。

---

## 実行する

```sh
docker compose up -d --build

# 1 つ目の端末。UE 2 が登録したままで待つ。
docker compose run --rm ue2 register \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2

# 2 つ目の端末。UE 1 が送り、status report を頼む。
docker compose run --rm ue sms -status-report +12025550102 "Hello UE-B, this is UE-A."
```

- オプションは、番号と本文の前に書きます。
- UE 1 のログに `"msg":"submit report"` と `"msg":"status report"` が出ます。
- UE 2 のログに `"msg":"sms received"` が出ます。

> 手順書 [「ショートメッセージを送る」](../../../docs/guide/ja/sms.md#ショートメッセージを送る)。同じ操作を、`scripts/` にある SMS の e2e のスクリプトが行います。

---

## キャプチャを開く

```text
sip.CSeq.method in {"MESSAGE", "REGISTER"} || (sip && ip.addr == 172.28.0.70) || diameter.cmd.code in {302, 306, 307}
```

- REGISTER、MESSAGE とその応答、IP-SM-GW の SUBSCRIBE と NOTIFY、LIR、UDR、PUR が残ります。
- Wireshark は MESSAGE のボディを自分でデコードします。
  - Info の列の最後に `(RP) RP-DATA (MS to Network)` のように出ます。
  - 詳細に「GSM A-I/F RP」と「GSM SMS TPDU」の木があります。

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/sms.md#キャプチャを開く)。ラダー図は手順書の [「送信と配達」](../../../docs/guide/ja/sms.md#送信と配達hello-ue-b-this-is-ue-a) にあります。

---

## 全体の形。MESSAGE は 6 通

| 順 | 向き | ボディ | 手順 |
| --- | --- | --- | --- |
| 1 | UE 1 から IP-SM-GW | RP-DATA と SMS-SUBMIT | 1 から 4 |
| 2 | IP-SM-GW から UE 1 | submit report。RP-ACK と SMS-SUBMIT-REPORT | 6 と 7 |
| 3 | IP-SM-GW から UE 2 | RP-DATA と SMS-DELIVER | 8 から 10 |
| 4 | UE 2 から IP-SM-GW | delivery report。RP-ACK と SMS-DELIVER-REPORT | 11 と 12 |
| 5 | IP-SM-GW から UE 1 | status report。RP-DATA と SMS-STATUS-REPORT | 14 |
| 6 | UE 1 から IP-SM-GW | delivery report。RP-ACK | 15 |

- 6 通はそれぞれ別の Call-ID を持ちます。
- SIP の応答（202 と 200）は、MESSAGE が届いたことしか伝えません。ショートメッセージがどうなったかは、報告の MESSAGE が伝えます。
- 2 通目と 3 通目は同時に出るので、キャプチャでの順番は入れ替わることがあります。status report を頼まなければ、5 通目と 6 通目はありません。

---

## R1 から R5。IP-SM-GW が利用者の登録を知る

| 見るもの | 見どころ |
| --- | --- |
| UE の REGISTER の Contact | 機能タグ `+g.3gpp.smsip`。 |
| S-CSCF から IP-SM-GW への REGISTER | 第三者 REGISTER。ボディの `<service-info>` に MSISDN。 |
| IP-SM-GW の SUBSCRIBE | Event は `reg`。I-CSCF を通り、LIR で S-CSCF が見つかる。 |
| NOTIFY の reginfo | `<unknown-param name="+g.3gpp.smsip">`。 |
| PUR | Data-Reference は 24。`<IP-SM-GW-Number>` に `12025550199`。 |

フィルタは `diameter.cmd.code == 307` です。

- 第三者 REGISTER だけでは、UE がショートメッセージを受けられるかが分かりません。だから IP-SM-GW は reg イベントを購読します。`+g.3gpp.smsip` を付けずに登録した UE には、PUR を送りません。

> 手順書 [「IP-SM-GW が利用者の登録を知る」](../../../docs/guide/ja/sms.md#ip-sm-gw-が利用者の登録を知る)。TS 24.341 5.3.2.2、5.3.3.2。TS 24.229 5.4.1.7、5.4.1.7A、5.7.1.1。TS 29.328 6.1.2、7.6.16。

---

## 1 から 4。UE 1 がショートメッセージを送る

| 見るもの | 見どころ |
| --- | --- |
| Request-URI と To | `tel:+12025550100`。受信者ではなく、SC に宛てる。 |
| Content-Type とボディ | `application/vnd.3gpp.sms`。47 オクテット。 |
| P-CSCF を出たあと | P-Asserted-Identity が付く。Record-Route は付かない。 |
| S-CSCF を出たあとの Route | 先頭に IP-SM-GW。次に `odi` の付いた S-CSCF 自身の URI。 |
| S-CSCF を出たあとの P-Asserted-Identity | UE 1 の tel URI が加わる。 |
| IP-SM-GW の応答 | 202 Accepted。 |

- 通話の INVITE と同じもの。Route と P-Asserted-Identity。違うもの。Record-Route が無く、メディア中継ノードも通りません。
- 200 ではなく 202 なのは、まだ何も届けていないからです。AS が要求を送り返せば、`odi` で S-CSCF が次の iFC に進めます。IP-SM-GW は送り返しません。

> 手順書 [1](../../../docs/guide/ja/sms.md#1-ue-1-がショートメッセージを送る) から [4](../../../docs/guide/ja/sms.md#4-ip-sm-gw-が受け付ける202) と [「オリジナルダイアログ識別子」](../../../docs/guide/ja/sms.md#オリジナルダイアログ識別子)。TS 24.341 5.3.1.2、5.3.3.4.1。TS 24.229 5.2.6.3.7、5.4.3.2、5.4.3.4。RFC 3428 7。

---

## 5 から 7。SC が受け付け、submit report が戻る

| 見るもの | 見どころ |
| --- | --- |
| UDR と UDA | SC が、受信者の番号を持つ利用者を Sh で調べる。Data-Reference は 10。 |
| IP-SM-GW の MESSAGE の Request-URI | UE 1 の SIP URI。 |
| Accept-Contact | `*;+g.3gpp.smsip;require;explicit`。 |
| In-Reply-To | 手順 1 の MESSAGE の Call-ID。 |
| ボディ | RP-ACK。中に SMS-SUBMIT-REPORT。 |
| 経路 | I-CSCF から入る。I-CSCF は LIR で UE 1 の S-CSCF を探す。 |

フィルタは `diameter.cmd.code == 306 || sip.In-Reply-To` です。

> 手順書 [5](../../../docs/guide/ja/sms.md#5-sc-が受信者を調べるudr-と-uda) から [7](../../../docs/guide/ja/sms.md#7-ue-1-が-200-を返す)。TS 24.341 5.3.3.4.3、5.3.1.2。TS 29.328 7.6.2。TS 24.229 5.7.3。

---

## 8 から 10。SC が配達する

| 見るもの | 見どころ |
| --- | --- |
| Request-URI | `tel:+12025550102`。UDA が返した tel URI。 |
| Request-Disposition | `no-fork`。 |
| ボディ | 網からの RP-DATA。中に SMS-DELIVER。TP-OA に送信者の番号。 |
| 優先度 2 の条件 | 一致しない。P-Asserted-Identity が IP-SM-GW。 |
| S-CSCF の動き | Accept-Contact を、UE 2 が登録した Contact のパラメータと照らし合わせる。 |
| UE 2 の応答 | 200。MESSAGE が届いたことだけを示す。 |

- `+g.3gpp.smsip` を登録していない UE には届かず、S-CSCF が 480 を返します。登録した機能タグをここで使います。
- SMS-SUBMIT と SMS-DELIVER を比べます。TP-DA が TP-OA になり、TP-SCTS が増えています。

> 手順書 [8](../../../docs/guide/ja/sms.md#8-sc-がメッセージを配達するsms-deliver) から [10](../../../docs/guide/ja/sms.md#10-ue-2-が-200-を返す)。TS 24.341 5.3.3.4.2、5.3.2.3。TS 24.229 5.4.3.3。RFC 3841 7.2.4、9.1。

---

## 11 と 12。delivery report

| 見るもの | 見どころ |
| --- | --- |
| UE 2 の MESSAGE の Request-URI | IP-SM-GW。手順 8 の P-Asserted-Identity から取る。 |
| In-Reply-To | 手順 8 の MESSAGE の Call-ID。 |
| ボディ | 端末からの RP-ACK。中に SMS-DELIVER-REPORT。 |
| S-CSCF を出たあとの Route | 手順 3 と同じ。UE 2 自身の iFC に一致する。 |
| IP-SM-GW の応答 | 202 Accepted。 |

- delivery report は UE 2 が発信する MESSAGE です。だから UE 2 自身の iFC が IP-SM-GW へ運びます。
- 配達の途中の状態は、IP-SM-GW の `Store` にあります。delivery report をどの IP-SM-GW が受けても、配達を終わらせられます。

> 手順書 [11](../../../docs/guide/ja/sms.md#11-ue-2-が-delivery-report-を送る) と [12](../../../docs/guide/ja/sms.md#12-delivery-report-が-ue-2-の条件で-ip-sm-gw-に届く)。TS 24.341 5.3.2.4、5.3.3.4.1。TS 23.204 6.5。

---

## 13 から 15。status report

- UE 1 は SMS-SUBMIT の TP-SRR で、status report を頼みました。

| 見るもの | 見どころ |
| --- | --- |
| UDR | SC が、今度は送信者の番号を調べる。 |
| IP-SM-GW の MESSAGE | UE 1 の tel URI に宛てる。中に SMS-STATUS-REPORT。 |
| TP-MR | SMS-SUBMIT と同じ値。どのメッセージの報告かが分かる。 |
| TP-ST | 0x00。受信者がメッセージを受け取った。 |
| UE 1 の delivery report | status report を受け取ったことを伝える。 |

フィルタは `gsm_sms.tp-mti == 2` です。

- submit report は、SC が受け付けたことを伝えます。status report は、相手に届いたことを伝えます。

> 手順書 [「status report」](../../../docs/guide/ja/sms.md#status-report)。TS 24.341 5.3.3.4.4、5.3.1.3。TS 23.040 9.2.3.15。

---

## 失敗するとき

| 場合 | 試し方 | UE 1 が受け取るもの |
| --- | --- | --- |
| 番号を持つ加入者がいない | `ue sms +12025550198 "Nobody has this number."` | RP-ERROR の submit report。RP-Cause 1、TP-FCS 0xC3。 |
| 受信者が登録していない | `ue sms -status-report +12025550103 "Are you there?"` | RP-ACK の submit report のあと、TP-ST が 0x62 の status report。 |

- コマンドは `docker compose run --rm` に続けて書きます。
- どちらも SIP の応答は 202 です。失敗は RP と TP の層で伝わります。
- 1 つ目は、SC が受け付けません。UDA が DIAMETER_ERROR_USER_UNKNOWN（5001）になります。
- 2 つ目は、SC は受け付けます。受信者には未登録のときのサービス（MMTel AS の iFC）があるので、LIR は DIAMETER_UNREGISTERED_SERVICE（2003）になり、I-CSCF が S-CSCF を選びます（TS 29.228 6.1.4.1）。その S-CSCF の iFC はどれも MESSAGE に一致しないので、480 を返します（TS 24.229 5.4.3.3）。

フィルタは `gsm_a.rp.cause || gsm_sms.tp-fcs` です。

> 手順書 [「失敗するとき」](../../../docs/guide/ja/sms.md#失敗するとき)。TS 24.011 Table 8.4。TS 23.040 9.2.3.22。TS 24.341 5.3.3.4.2。

---

## ボディを読む。RP 層

手順 1 の MESSAGE のボディです。

```text
00 00 00 07 91 21 20 55 05 01 f0 23 21 00 0b 91 21 20 55 05 01 f2 00 00 19 c8 32 9b fd 06 55 8b 2d 21 0b
44 47 a7 e7 a0 f4 1c 54 2d b6 82 2e
```

| オクテット | フィールド | 見どころ |
| --- | --- | --- |
| `00` | RP-MTI | 端末から網への RP-DATA。 |
| `07 91 21 20 55 05 01 f0` | RP-DA | 長さは 7 オクテット。数字は 2 桁ずつ入れ替わる。SC の番号。 |
| `23` | RP-User-Data の長さ | このあとの 35 オクテットが TPDU。 |

- まず手で読み、そのあとで Wireshark の木と比べます。`21 20 55 05 01 f0` を 12025550100 に戻してみます。
- ラボでは、MESSAGE の詳しい表示の **Body (hex)** に同じオクテットが出ます。

> 手順書 [「中継層（RP）」](../../../docs/guide/ja/sms.md#中継層rp)。全部のフィールドの表は手順書にあります。TS 24.011 7.3.1.2、8.2。

---

## ボディを読む。TP 層と文字

| オクテット | フィールド | 見どころ |
| --- | --- | --- |
| `21` | 1 オクテット目 | TP-MTI は 01 で SMS-SUBMIT。TP-SRR は 1 で、status report を頼む。 |
| `0b 91 21 20 55 05 01 f2` | TP-DA | 長さは 11 桁。受信者の番号。 |
| `00` | TP-DCS | GSM 7 ビットの既定のアルファベット。 |
| `19` | TP-UDL | 25 セプテット。25 文字。このあとが、7 ビットずつ詰めた本文。 |

- RP は長さをオクテットで、TP は桁で数えます。同じ番号でも 7 と 11 になります。
- TP-MTI の 01 は、端末からなら SMS-SUBMIT、網からなら SMS-SUBMIT-REPORT です。
- 最初の 2 文字の H と e を、手順書の図に沿って `c8` と `32` に詰めてみます。日本語は UCS2 で、TP-DCS は `08` です。UE 2 から日本語を送ると、TP-DCS と TP-UDL が変わります。

> 手順書 [「転送層（TP）の SMS-SUBMIT」](../../../docs/guide/ja/sms.md#転送層tpの-sms-submit) と [「GSM 7 ビットの詰め方と UCS2」](../../../docs/guide/ja/sms.md#gsm-7-ビットの詰め方と-ucs2)。TS 23.040 9.2.2.2、9.2.3。TS 23.038 6.1.2.1.1。

---

## Wireshark の表示フィルタ

フィルタをかける前に、6 通のうちどれが残るかを予想します。

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Content-Type == "application/vnd.3gpp.sms"` | SMS か報告を運ぶ MESSAGE のすべて。 |
| `gsm_a.rp.msg_type == 0x00` | UE からの RP-DATA。UE が送ったショートメッセージ。 |
| `gsm_sms.tp-mti == 2` | SMS-STATUS-REPORT。 |
| `gsm_sms.sms_text contains "Hello"` | 本文。GSM 7 ビットも UCS2 もデコードして調べる。 |
| `gsm_sms.tp-da == "12025550102"` | その番号に宛てたショートメッセージ。 |
| `sip.In-Reply-To` | submit report と delivery report。 |
| `diameter.cmd.code == 306 \|\| diameter.cmd.code == 307` | Sh のメッセージ。UDR、UDA、PUR、PUA。 |

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/sms.md#wireshark-の表示フィルタ) と、[キャプチャの「SMS over IP の表示フィルタ」](../../../docs/guide/ja/capture.md#sms-over-ip-の表示フィルタ)

---

## ラボで送る

```sh
docker compose --profile phones up -d --build
```

1. `phone1` のカードの SMS のフォームに本文を書き、**status report** に印を付けて、**Send SMS** を押します。
2. **SMS sent** の表示が `waiting for the submit report` から `accepted by the SC` に変わります。
3. `phone2` のカードの **SMS received** に、送信者の番号と本文が出ます。
4. 各行の **ladder** で、その MESSAGE のラダー図が出ます。矢印を選ぶと、RP と TP のフィールドが出ます。

- ラボは Call-ID ごとにラダー図を描くので、6 通の MESSAGE は 6 つのラダー図になります。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| ボディ。RP、TP、文字 | `internal/sms` の `rp.go`、`tp.go`、`text.go` |
| iFC と、その評価 | `internal/ifc`、`internal/scscf/services.go` |
| 第三者 REGISTER | `internal/scscf/thirdparty.go` |
| Accept-Contact の照合 | `internal/scscf/callerprefs.go` |
| IP-SM-GW | `internal/ipsmgw` の `message.go`、`deliver.go`、`regevent.go`、`store.go` |
| SC との境界と、SC のスタブ | `internal/ipsmgw/servicecentre.go`、`internal/sc` |
| Sh | `internal/sh`、`internal/hss/sh.go` |
| UE | `internal/ue/sms.go` |

- 手で読んだフィールドの名前を `internal/sms/tp.go` で探します。`internal/ipsmgw/network_test.go` が、流れの全体を 1 つのプロセスで動かします。

> 手順書 [「コードの場所」](../../../docs/guide/ja/sms.md#コードの場所) と [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/sms.md#このプロジェクトで簡略化していること)

---

[layout: exercise]

## 振り返り

1. UE 1 の MESSAGE の Request-URI は、誰のアドレスですか。
2. S-CSCF は、その MESSAGE を IP-SM-GW へ送ることを、どうやって知りますか。
3. 202 Accepted と submit report は、それぞれ何を伝えますか。
4. IP-SM-GW が UE 2 へ送る MESSAGE は、網のどこから入りますか。
5. delivery report が IP-SM-GW に届くのは、誰の iFC によりますか。
6. RP-DA の長さが 7、TP-DA の長さが 11 なのは、なぜですか。

---

[layout: answers]

## 振り返りの答え

1. SC のアドレスです。UE はショートメッセージを受信者に直接送りません。
2. 登録のときの SAA で届いた iFC のうち、優先度 1 の条件に MESSAGE が一致するからです。
3. 202 は、IP-SM-GW が MESSAGE を受け取ったことを伝えます。submit report は、SC がショートメッセージを受け付けたかを伝えます。
4. I-CSCF です。I-CSCF が LIR で UE 2 の S-CSCF を見つけます。
5. UE 2 の iFC です。delivery report は UE 2 が発信する MESSAGE だからです。
6. RP は長さをオクテットで、TP は桁で数えるからです。

---

## まとめ

IMS の中心となる登録、通話、ショートメッセージの 5 回は、これで終わりです。

| 回 | 題 | 読む文書 |
| --- | --- | --- |
| 1 | パッケージ構成 | [README.ja.md](../../../README.ja.md) |
| 2 | 網の地図 | [docs/guide/ja/README.md](../../../docs/guide/ja/README.md) |
| 3 | 登録 | [docs/guide/ja/registration.md](../../../docs/guide/ja/registration.md) |
| 4 | 通話 | [docs/guide/ja/call.md](../../../docs/guide/ja/call.md) |
| 5 | SMS | [docs/guide/ja/sms.md](../../../docs/guide/ja/sms.md) |

- 次は MMTel AS の補足サービス（[mmtel.md](../../../docs/guide/ja/mmtel.md)）です。その次が、補足サービスの上に立つ VoLTE の通話（[volte.md](../../../docs/guide/ja/volte.md)）です。

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```
