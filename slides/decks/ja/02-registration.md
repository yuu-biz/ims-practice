---
title: UE が IMS に登録する流れ
footer: ims-practice 勉強会 2. 登録
lang: ja
---

# UE が IMS に登録する流れ

ims-practice 勉強会 第 2 回（SIP Digest での登録と、アプリケーションサーバーへの第三者 REGISTER）

---

[layout: summary]

## この回で分かること

最初の問い。電話の電源を入れてから最初の通話までに、網の中で何が起きているでしょうか。

- 登録で決まる 3 つのこと。端末の場所、本人であること、後の要求が通る経路。
- REGISTER が 2 回送られる理由。
- I-CSCF と S-CSCF が、HSS に何を尋ねるか。
- 200 OK のどのヘッダを、あとの通話と SMS で使うか。

> 手順書 [UE が IMS に登録する流れを追う](../../../docs/guide/ja/registration.md)（`docs/guide/ja/registration.md`）
> 仕様の版は Release 18 です。TS 24.229 V18.11.0、TS 29.228 V18.0.0、TS 29.229 V18.1.0。

---

## 進め方

1. 構成を起動して、登録を 1 回実行する。
2. キャプチャを Wireshark で開き、フィルタをかける。
3. ラダー図で全体を見る。
4. メッセージを 1 通ずつ開き、スライドに挙げたヘッダと AVP を探す。
5. 同じ箇所の仕様の条項を、手順書で確かめる。

> 起動とキャプチャの開き方は、手順書 [信号をキャプチャして Wireshark で開く](../../../docs/guide/ja/capture.md) にあります。

---

## 登場するノード

| ノード | アドレス | 登録での役割 |
| --- | --- | --- |
| UE シミュレータ | 172.28.0.100 | REGISTER を送り、認証の要求に答える。 |
| P-CSCF | 172.28.0.40 | UE から見て最初の中継点。Path を付ける。 |
| I-CSCF | 172.28.0.20 | ホーム網の入口。担当の S-CSCF を HSS に尋ねる。 |
| S-CSCF | 172.28.0.30 | レジストラ。認証し、登録を記録する。 |
| HSS | 172.28.0.10 | 加入者のデータベース。Cx で答える。 |
| DNS | 172.28.0.2 | ホーム網のドメインの NAPTR、SRV、A を持つ。 |

- アドレスの最後の数でノードが分かります。HSS は 10、I-CSCF は 20、S-CSCF は 30、P-CSCF は 40 です。
- UE は SIM カードの無いソフトフォンです。識別子は IMC に入っているのと同じように設定で与えられるので（TS 24.229 5.1.1.1B.1）、登録した公開の識別子をほかのリクエストでも使います。IMSI から作った識別子と同じ形ですが、設定で与えたもので、規制されていません。VoLTE の端末は、代わりに規制された一時的な識別子で登録します。VoLTE の回で見ます。

> 手順書 [「登場するノード」](../../../docs/guide/ja/registration.md#登場するノード)。試験用の利用者の識別子とパスワードもここにあります。

---

## 実行する

```sh
docker compose up -d --build
docker compose logs scscf | grep I-Open        # Diameter の接続が開いたか
docker compose run --rm ue register --deregister-after 5s
```

起動してから 10 秒ほど待ちます。I-CSCF と S-CSCF が、先に HSS との Diameter の接続を開くためです。

UE のログで、次の 3 行を探します。

- `"msg":"registered"`。Service-Route と、一緒に登録された識別子が出ます。
- `"msg":"reg event"` と `"state":"active"`。
- `"msg":"deregistered"`。

> 手順書 [「登録を実行する」](../../../docs/guide/ja/registration.md#登録を実行する)

---

## キャプチャを開く

`captures/` の一番新しいファイルを開き、次の表示フィルタを使います。

```text
sip || diameter.cmd.code >= 300 || dns.qry.type == 35 || dns.qry.type == 33
```

- SIP、Cx、NAPTR と SRV の問い合わせが残ります。
- Diameter の監視（DWR と DWA）は消えます。
- **Statistics > Flow Graph** で **Limit to display filter** を選ぶと、ラダー図になります。
- 手順書のラダー図の番号 1 から 10 が、次からのスライドの番号です。

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/registration.md#キャプチャを開く) と [「シーケンス」](../../../docs/guide/ja/registration.md#シーケンス)。ラダー図の全体は手順書にあります。

---

## 全体の形

```ladder
UE, P-CSCF, I-CSCF, S-CSCF, HSS
UE -> P-CSCF -> I-CSCF: REGISTER // 1 - 2
I-CSCF <-> HSS: UAR, UAA // 3
I-CSCF -> S-CSCF: REGISTER // 4
S-CSCF <-> HSS: MAR, MAA // 5
UE <-- P-CSCF <-- I-CSCF <-- S-CSCF: 401 // 6
UE -> P-CSCF -> I-CSCF: REGISTER + response // 7
I-CSCF <-> HSS: UAR, UAA // 8
I-CSCF -> S-CSCF: REGISTER
S-CSCF <-> HSS: SAR, SAA // 9
UE <-- P-CSCF <-- I-CSCF <-- S-CSCF: 200 OK // 10
```

- 1 回目は「誰か」を伝え、2 回目で「本人であること」を証明します。
- HSS には 4 回尋ねます。UAR が 2 回、MAR と SAR が 1 回ずつです。手順書のラダー図の全体には DNS もあります。

---

## 1 と 2。最初の REGISTER と、I-CSCF を探す DNS

| 見るもの | 見どころ |
| --- | --- |
| UE の REGISTER の Request-URI | サーバーではなく、ホーム網のドメイン。 |
| Authorization | `username` に秘密の識別子。`nonce` と `response` は空。 |
| Contact | ユーザー部に UUID。`+g.3gpp.smsip` と `expires=600000`。 |
| DNS の 3 つの問い合わせ | NAPTR、SRV、A の順で I-CSCF が見つかる。 |
| P-CSCF が足すヘッダ | Path、Require: path、P-Charging-Vector、P-Visited-Network-ID。 |

フィルタは `dns.qry.type == 35 || dns.qry.type == 33` です。

- P-CSCF の前と後の REGISTER を比べて、足されたものを探します。Path は、あとで着信を UE に届けるためのものです。

> 手順書 [1](../../../docs/guide/ja/registration.md#1-ue-が最初の-register-を送る) と [2](../../../docs/guide/ja/registration.md#2-p-cscf-が-i-cscf-を探して-register-を転送する)。TS 24.229 5.1.1.2.1、5.1.1.2.3、5.2.2.1。RFC 3263、RFC 3327、RFC 3581。

---

## 3 と 4。I-CSCF が HSS に尋ねる（UAR と UAA）

| 見るもの | 見どころ |
| --- | --- |
| UAR の AVP | User-Name、Public-Identity、Visited-Network-Identifier、User-Authorization-Type。 |
| UAA の結果 | DIAMETER_FIRST_REGISTRATION（2001）。Experimental-Result の中にある。 |
| UAA の Server-Capabilities | S-CSCF の名前の代わりに、必要な能力が返る。 |
| I-CSCF が転送する REGISTER | Request-URI が、選んだ S-CSCF になる。Via は 3 つ。 |

フィルタは `diameter.cmd.code == 300` です。

- 間違えやすい点。Result-Code の 2001 は DIAMETER_SUCCESS です。Experimental-Result の中では DIAMETER_FIRST_REGISTRATION です。
- I-CSCF が S-CSCF を自分で覚えないのはなぜでしょうか。状態を持たない入口だからです。覚えるのは HSS です。

> 手順書 [3](../../../docs/guide/ja/registration.md#3-i-cscf-が-hss-に問い合わせるuar-と-uaa) と [4](../../../docs/guide/ja/registration.md#4-i-cscf-が-register-を-s-cscf-へ転送する)。TS 24.229 5.3.1.2。TS 29.228 6.1.1、6.7。TS 29.229 6.2.1.1。

---

## 5 と 6。S-CSCF が認証を求める（MAR、MAA、401）

| 見るもの | 見どころ |
| --- | --- |
| MAR の 3GPP-SIP-Authentication-Scheme | `Unknown`。方式の選択を HSS に任せている。 |
| MAA の SIP-Digest-Authenticate | Digest-Realm、Digest-Algorithm、Digest-QoP、Digest-HA1。 |
| 401 の WWW-Authenticate | レルム、`nonce`、`algorithm=MD5`、`qop="auth"`。 |
| 401 の Via | 戻るたびに 1 つずつ減り、UE には自分の Via だけが届く。 |

フィルタは `diameter.cmd.code == 303 || sip.Status-Code == 401` です。

- HSS はパスワードを送りません。送るのは H(A1) です。
- S-CSCF は 401 と一緒にタイマー reg-await-auth（4 分）を始めます（手順書の 6）。

> 手順書 [5](../../../docs/guide/ja/registration.md#5-s-cscf-が認証の情報を求めるmar-と-maa) と [6](../../../docs/guide/ja/registration.md#6-s-cscf-が-ue-に認証を求める401)。TS 24.229 5.4.1.1、5.4.1.2.1、5.4.1.2.1B。TS 29.228 6.3。RFC 2617。

---

## 7 と 8。2 回目の REGISTER

| 見るもの | 見どころ |
| --- | --- |
| Call-ID と CSeq | Call-ID は 1 回目と同じ。CSeq は 1 つ増える。 |
| Authorization | `nonce`、`cnonce`、`nc`、`qop`、`response` が入る。 |
| P-CSCF が足すパラメータ | `integrity-protected="ip-assoc-pending"`。 |
| 2 回目の UAA | DIAMETER_SUBSEQUENT_REGISTRATION（2002）と Server-Name。 |
| I-CSCF から先のトランスポート | 1300 バイトを超えるので TCP。Via が `SIP/2.0/TCP` になる。 |

- 2 回目の REGISTER が同じ S-CSCF に届くのはなぜでしょうか。HSS が MAR のときに S-CSCF を覚え、2 回目の UAA の Server-Name で返すからです。

> 手順書 [7](../../../docs/guide/ja/registration.md#7-ue-が認証の要求に答える) と [8](../../../docs/guide/ja/registration.md#8-i-cscf-がもう一度-hss-に問い合わせる)。TS 24.229 5.1.1.5.4、5.2.2.3。TS 29.228 6.1.1.1。RFC 3261 18.1.1。

---

## 9 と 10。SAR、SAA と 200 OK

| 見るもの | 見どころ |
| --- | --- |
| SAR の Server-Assignment-Type | REGISTRATION（1）。 |
| SAA の User-Data | 利用者のプロファイル。XML。iFC が入っている。 |
| 200 OK の Path と Service-Route | このあとの要求が通る経路。 |
| 200 OK の P-Associated-URI | 一緒に登録された識別子。HSS が規制する識別子は入らない。最初が既定の識別子。 |
| 200 OK の Contact | `expires=3600`。S-CSCF が上限まで短くした。 |
| 200 OK の Authentication-Info | `rspauth`。網の側も H(A1) を知っている証明。 |

フィルタは `diameter.cmd.code == 301` です。Wireshark では User-Data が `Cx-User-Data` という名前で出ます。

- UE は発信するときに Service-Route を使います。Path は着信のときに使います。どちらも通話の回でもう一度出てきます。

> 手順書 [9](../../../docs/guide/ja/registration.md#9-s-cscf-が答えを確かめて-hss-に知らせるsar-と-saa) と [10](../../../docs/guide/ja/registration.md#10-s-cscf-が登録を受け付ける200-ok)。TS 24.229 5.4.1.2.2A、5.4.1.2.2F。TS 29.228 Annex E。RFC 3608。

---

# ここまでで登録は完了

後半は、登録のあとに続く流れを見ます。

---

## 11 から 14。登録の状態を購読する

- P-CSCF と UE は、登録が変わったら知らせるよう S-CSCF に頼みます。reg イベントパッケージです。

| 見るもの | 見どころ |
| --- | --- |
| P-CSCF の SUBSCRIBE | I-CSCF を通る。I-CSCF は LIR で S-CSCF を探す。 |
| UE の SUBSCRIBE | Service-Route に沿って進み、I-CSCF を通らない。 |
| NOTIFY の本文 | `application/reginfo+xml`。登録先ごとに `contact` がある。 |
| Subscription-State | `active;expires=...`。 |

フィルタは `sip.CSeq.method == "SUBSCRIBE" || sip.CSeq.method == "NOTIFY" || diameter.cmd.code == 302` です。

> 手順書 [「登録の状態を購読する」](../../../docs/guide/ja/registration.md#登録の状態を購読する)。TS 24.229 5.2.3、5.1.1.3、5.4.2.1.1、5.4.2.1.2。RFC 3680、RFC 6665。TS 29.228 6.1.4。

---

## 15 から 18。アプリケーションサーバーにも登録する

- S-CSCF は、プロファイルの iFC に従って IP-SM-GW にも REGISTER を送ります。第三者 REGISTER と呼びます。

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF の REGISTER | From と Contact が S-CSCF。To が利用者。本文に MSISDN。 |
| IP-SM-GW の SUBSCRIBE と NOTIFY | IP-SM-GW も reg イベントを購読する。 |
| PUR と PUA | IP-SM-GW が、自分の番号を Sh で HSS に登録する。 |

- UE はこれを待ちません。200 OK をもう受け取っています。この流れは SMS の回で詳しく読みます。

フィルタは `diameter.cmd.code == 307` です。

> 手順書 [「アプリケーションサーバーにも登録する（第三者 REGISTER）」](../../../docs/guide/ja/registration.md#アプリケーションサーバーにも登録する第三者-register)。TS 24.229 5.4.1.7、5.4.1.7A。TS 24.341 5.3.3.2。TS 29.328 6.1.2。

---

## 登録の解除

UE は Contact に `expires=0` を付けた REGISTER を送ります。

| 見るもの | 見どころ |
| --- | --- |
| P-CSCF が足すパラメータ | `integrity-protected="ip-assoc-yes"`。 |
| UAR の User-Authorization-Type | DE_REGISTRATION（1）。 |
| S-CSCF の応答 | 401 を返さない。P-CSCF が IP の対応付けを見つけ、`ip-assoc-yes` を付けたから。 |
| SAR の Server-Assignment-Type | USER_DEREGISTRATION（5）。 |
| 最後の NOTIFY | `terminated`。contact は `event="unregistered"`。 |

- 更新されない登録も同じように終わります。`event="expired"` と、TIMEOUT_DEREGISTRATION の SAR です。手順書の同じ節にあります。

> 手順書 [「登録の解除」](../../../docs/guide/ja/registration.md#登録の解除)。TS 24.229 5.1.1.6.1、5.4.1.4.1、5.4.2.1.2。

---

## 失敗を見る

| 場合 | 試し方 | 見どころ |
| --- | --- | --- |
| パスワードが誤っている | `ue register --password wrong-password` | 手順 8 までは同じ。S-CSCF は 403 を返し、AUTHENTICATION_FAILURE（9）の SAR を送る。 |
| HSS から加入を削除する | ダッシュボードの Subscribers パネルで、登録中の加入を削除する。 | HSS が RTR を送る。Reason-Code は PERMANENT_TERMINATION（0）。UE には `event="rejected"` の NOTIFY が届く。 |

- コマンドは `docker compose run --rm` に続けて書きます。
- RTR と RTA のフィルタは `diameter.cmd.code == 304` です。RTR は、この勉強会で初めて HSS から始まる Cx の要求です。
- HSS の加入者管理の API でも加入を削除できます。API の表は手順書にあります。

> 手順書 [「パスワードが誤っているとき」](../../../docs/guide/ja/registration.md#パスワードが誤っているとき) と [「加入を削除する（HSS からの登録解除）」](../../../docs/guide/ja/registration.md#加入を削除するhss-からの登録解除)。TS 24.229 5.4.1.2.3B。TS 29.228 6.1.2.1、6.1.3。

---

[layout: two-columns]

## ダッシュボードで同じ流れを見る

- <http://localhost:8080> を開き、**Registrations** パネルで識別子を選びます。
- 最後の REGISTER のラダー図が出ます。矢印を選ぶと、メッセージの全文と仕様の条項が出ます。
- ラダー図には SIP と Cx が並びます。DNS は出ません。
- ダッシュボードは、ノードが送るコピー（HEP）から図を作ります。IMS の一部ではありません。

[column]

| 道具 | 向いていること |
| --- | --- |
| Wireshark | すべてのパケット。DNS、TCP、AVP の中身まで。 |
| ダッシュボード | 1 つの登録や通話を、すぐに図で見る。 |

> 手順書 [ダッシュボードで IMS を見る「各パネルの内容」](../../../docs/guide/ja/dashboard.md#各パネルの内容) と [「ラダー図の組み立て方」](../../../docs/guide/ja/dashboard.md#ラダー図の組み立て方)

---

## コードで確かめる

| 手順 | 開くファイル |
| --- | --- |
| 1、7。UE の REGISTER と応答の計算 | `internal/ue/ue.go` |
| 2。Path と DNS | `internal/pcscf/pcscf.go`、`internal/sipdns/locate.go` |
| 3、8。UAR | `internal/icscf/icscf.go` |
| 5、6、9、10。MAR、401、SAR、200 OK | `internal/scscf/scscf.go` |
| HSS の答え | `internal/hss/hss.go` |
| 11 から 14。reg イベント | `internal/scscf/regevent.go`、`internal/pcscf/regevent.go`、`internal/ue/regevent.go` |

- キャプチャで見たヘッダの名前、たとえば `Service-Route` でコードを検索します。
- 手順書の簡略化の表から 1 行を選び、その `SIMPLIFIED:` のコメントを探します。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/registration.md#このプロジェクトで簡略化していること)

---

[layout: exercise]

## 振り返り

1. REGISTER が 2 回送られるのは、なぜですか。
2. I-CSCF は 1 回の登録で、HSS に何回尋ねますか。答えはどう変わりますか。
3. HSS は S-CSCF にパスワードを渡しますか。
4. 200 OK の Path と Service-Route は、それぞれ誰が、いつ使いますか。
5. 登録の解除で 401 が返らないのは、なぜですか。

---

[layout: answers]

## 振り返りの答え

1. 1 回目への答えとして網が認証の問い（nonce）を出し、2 回目で UE がそれに答えるからです。
2. 2 回です。1 回目は DIAMETER_FIRST_REGISTRATION と Server-Capabilities、2 回目は DIAMETER_SUBSEQUENT_REGISTRATION と Server-Name です。
3. 渡しません。渡すのは H(A1) です。
4. S-CSCF は Path を使って、UE あての要求をその UE の P-CSCF 経由で送ります。UE は Service-Route を使って、自分の要求を自分の S-CSCF へ送ります。
5. P-CSCF がその UE の IP の対応付けを持っていて、`ip-assoc-yes` を付けるからです。

---

## 次の回

- 次の回は通話です。この回で決まった Path、Service-Route、IP の対応付けを使います。
- 後片付けは次のコマンドです。

```sh
docker compose --profile ue down --rmi all -v --remove-orphans
```

> 次の回の手順書は [音声通話の流れを追う](../../../docs/guide/ja/call.md) です。
