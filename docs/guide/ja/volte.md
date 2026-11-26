# VoLTE の呼の流れ

[English](../en/volte.md)

この手順書では、[UE が IMS に登録する流れを追う](registration.md) と [音声通話の流れを追う](call.md) のソフトフォンに、VoLTE の端末が加えるものを追います。アクセス網、sec-agree を伴う IMS-AKA、Rx でのポリシー制御と、VoLTE の端末がかける通話です。2 台の UE シミュレータが EPC のスタブに attach し、IMS-AKA で登録し、プリコンディションを使って AMR-WB で互いに通話します。PCRF はそれぞれの UE に音声用の bearer を作ります。最後に網が片方の bearer を取り上げ、P-CSCF が通話を終わらせます。

この手順書は次の版に沿っています。TS 24.229 V20.1.0、TS 29.214 V20.0.0（Rx）、TS 29.213（SDP から Rx への対応）、TS 24.301 V20.1.0（EPS のメッセージ）、TS 26.114 V20.1.1（メディアの扱い）、TS 33.203 と TS 33.102（IMS-AKA）、TS 35.206 と TS 35.208（MILENAGE）、RFC 3310（Digest AKA）、RFC 3329（sec-agree）、RFC 3312（プリコンディション）、RFC 4867（AMR のペイロード形式）、RFC 4028（セッションタイマー）、GSMA IR.92 v24.0 です。以下の条項番号はこれらの版のものです。

## 始める前に

- [UE が IMS に登録する流れを追う](registration.md)、[音声通話の流れを追う](call.md)、[MMTel AS の付加サービス](mmtel.md) を読んでおくこと。この手順書はそこで説明したことをそのまま使います。I-CSCF と S-CSCF を通る REGISTER、通話の INVITE、PRACK、ACK、メディアの中継、MMTel AS が 1 つの通話から作る 3 つのダイアログです。
- 略語の説明は [用語集](../../glossary.md) にあります。

## VoLTE の端末とソフトフォンの違い

ほかの手順書の UE はソフトフォンです。P-CSCF のアドレスは設定で与えられ、SIP Digest とパスワードで認証し、G.711（PCMU）を offer します。VoLTE の端末（IR.92）は、次の 5 つの点で違います。

1. **アクセス網。** 端末はまず EPC に attach します。網は IP アドレス、P-CSCF のアドレス、SIP に使う default bearer を渡します。通話ごとに、音声用の 2 本目の bearer を作ります（IR.92 4.4）。
2. **IMS-AKA。** 端末は USIM の鍵を持っていることを示し、網も同じ鍵を知っていることを示し、両方でセキュリティアソシエーションを取り決めます（TS 33.203 6.1.1、7）。パスワードはありません。既定では、両端はアソシエーションを取り決めて保護ポートを使うところまでを行い、IPsec の ESP はかけません。そのため、登録した端末の SIP も、ソフトフォンのものと同じようにキャプチャで読めます。[ESP をかけた sec-agree](#esp-をかけた-sec-agree)で、実際の VoLTE の端末と同じように保護をかけられます。
3. **ポリシー制御。** P-CSCF は通話ごとに要るメディアを Rx で PCRF に伝え、PCRF はアクセス網にその資源を確保させます（TS 29.214 4.4.1）。アクセス網が bearer を失うと PCRF が P-CSCF に知らせ、P-CSCF が通話を終わらせます（TS 24.229 5.2.8.1.2、IR.92 2.4.2.3）。
4. **VoLTE の通話。** 着信側は、両方の bearer がそろうまで呼び出しを始めません（RFC 3312、IR.92 2.4.1）。音声は AMR-WB で（IR.92 3.2.1）、RTCP、telephone-event の DTMF、セッションタイマー、BYE の Reason が付きます。
5. **識別子。** 端末のカードには USIM があり ISIM が無いので、端末は IMSI から識別子を作ります。網が規制する一時的な公開の識別子で登録し、網が返す既定の公開の識別子で通話します（「ISIM の無い端末の識別子」を参照）。

ソフトフォンはこれまでどおり動きます。EPC スタブを通らない UE は PCRF が知らないので、P-CSCF はその UE をポリシー制御なしで扱います。

## 登場するノード

| ノード | アドレス | この手順書での役割 |
| --- | --- | --- |
| volte10 | 172.28.0.120 | 発信側。`tel:+12025550110`。加入者 10 で、USIM には MILENAGE の試験データ 1（TS 35.208 4.3.1）の K と OPc がある。 |
| volte12 | 172.28.0.121 | 着信側。`tel:+12025550112`。加入者 12 で、試験データ 3（4.3.3）の K と OPc を持つ。 |
| ue2 | 172.28.0.101 | ソフトフォンに近い UE。PCMU だけを offer し、SIP Digest を使う。`tel:+12025550102`。 |
| SIPp | 172.28.0.102 | ほかの人が書いたユーザーエージェント。加入者 11 として IMS-AKA で登録する。 |
| EPC スタブと PCRF | 172.28.0.80 | 役割 `epc`。UE 向けの API を HTTP のポート 8080 で、PCRF を Diameter のポート 3868 で動かす（`pcrf.ims.mnc001.mcc001.3gppnetwork.org`）。 |
| P-CSCF | 172.28.0.40 | SIP の 5060 番と、IMS-AKA の UE のための保護サーバーポート 5064 番。PCRF に対する Rx のクライアント。 |
| S-CSCF | 172.28.0.30 | IMS-AKA で認証を求める。 |
| HSS | 172.28.0.10 | 認証ベクトルを作る（MAR と MAA）。 |
| MMTel AS | 172.28.0.75 | [MMTel AS の付加サービス](mmtel.md) と同じく、すべての通話を受け持つ。 |
| メディアの中継ノード | 172.28.0.50 | RTP、RTCP、telephone-event を復号せずに中継する。 |
| ラボ | 172.28.0.60 | volte10 と volte12 を操作し、ラダー図に Rx を出し、網の手順を始める EPC スタブのパスをホストに取り次ぐ。 |

volte10 と volte12 は `compose.yaml` のプロファイル `volte` のサービスです。鍵は `deploy/compose/hss.yaml` にあり、`deploy/compose/epc.yaml` が加入者ごとにコンテナのアドレスを割り当てています。

## アクセス網。EPC スタブ

### スタブが省くこと

本物の EPC では、UE、eNodeB、MME の間を NAS のメッセージが S1 で運ばれ、UE のパケットは GTP-U のトンネルで PDN GW に届きます。スタブはメッセージとその名前を残し、ほかを省きます。

- UE とスタブは TS 24.301（8.2、8.3）の ESM と EMM のメッセージを、HTTP の上の JSON でやり取りします。名前は仕様のとおりで、URL では小文字にしています。たとえば `POST /ues/001010000000010/attach-request` です。網が自分から送るものは、UE のイベントストリーム `GET /ues/<IMSI>/events` に Server-Sent Events で届きます。
- 加入者ごとに静的なアドレスがあり（TS 23.401 5.3.1.1）、コンテナは Docker のネットワークでそのアドレスをすでに持っています。トンネルは使わず、UE のパケットは P-CSCF と中継ノードにそのまま届きます。
- PCRF と PDN GW は Gx（TS 29.212）でやり取りする代わりに、役割 `epc` の中で PDN 接続の状態を共有します。
- 無線、NAS のセキュリティ、attach の認証はありません。bearer は記録だけで、スタブは QoS を強制せず、パケットも振り分けません。

`ue` コマンドは `-epc` にスタブの URL を渡すと attach します。IMSI は `-impi` のユーザー部です。

### attach

`scripts/e2e-volte.sh` を 1 回動かしたときのキャプチャです。

```text
#681  volte10 -> EPC  GET  /system-information                 the cell: MCC 001, MNC 01, TAC 1, ECI 257
#685  volte10 -> EPC  GET  /ues/001010000000010/events         the event stream stays open
#690  volte10 -> EPC  POST /ues/001010000000010/attach-request ATTACH REQUEST with a PDN CONNECTIVITY REQUEST for APN ims
#692  EPC -> volte10  200  ATTACH ACCEPT with ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST
#694  volte10 -> EPC  POST /ues/001010000000010/attach-complete
```

ATTACH ACCEPT は ESM のメッセージのコンテナに IMS の APN の default bearer を入れています。

```json
"esm_message_container": {
  "eps_bearer_identity": 5,
  "eps_qos": {"qci": 5},
  "access_point_name": "ims",
  "pdn_address": {"pdn_type_value": "IPv4", "ipv4_address": "172.28.0.120"},
  "protocol_configuration_options": {"containers": [{"id": "000CH", "contents": "172.28.0.40"}]}
}
```

- EPS bearer 5 が default bearer です。QCI は IMS の信号の種類である 5 です（IR.92 4.4.1、TS 23.203 6.1.7）。
- PDN のアドレスは加入者の静的なアドレスです。UE はこれが P-CSCF に届くときの自分のアドレスと同じかを確かめ、違えば止まります。
- プロトコル設定オプション（PCO）は、識別子 `000CH` のコンテナで P-CSCF のアドレスを運びます（TS 24.008 10.5.6.3）。これは TS 24.229 L.2.2.1 の方法 II で、IR.92 4.5 が求める方法です。一覧の先頭のアドレスがいちばん優先されます。UE は一覧を受け取りますが、使うのは先頭のアドレスだけです。PCO にはアドレスしか無いので、UE はそのアドレスのポート 5060 に送ります。

UE はセルの情報を P-Access-Network-Info にも書きます。`3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101` は、MCC 001、MNC 01、16 進 4 桁の TAC、16 進 7 桁の ECI です（TS 24.229 7.2A.4.3、L.3.1.1）。

### bearer

音声用の bearer は、PCRF が通話のメディアを許可したときに、通話ごとに作られます（TS 24.229 L.2.2.5.1）。スタブはそれを UE のイベントストリームで送ります。

```text
event: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
data: {"eps_bearer_identity":6,"linked_eps_bearer_identity":5,
       "eps_qos":{"qci":1,"maximum_bit_rate_for_uplink":80,"maximum_bit_rate_for_downlink":41,
                  "guaranteed_bit_rate_for_uplink":80,"guaranteed_bit_rate_for_downlink":41},
       "tft":{"tft_operation_code":"create new TFT","packet_filters":[ ... RTP and RTCP to and from the relay ... ]}}
```

QCI 1 は帯域を保証する会話音声の種類です（IR.92 4.4.2）。速度の単位は kbit/s で、P-CSCF が求めた帯域から決まります。トラフィックフローテンプレート（TFT）には 4 つのパケットフィルタがあります。UE と、中継ノードの UE 側の終端との間の RTP と RTCP を、それぞれ上りと下りで指定します。UE は `POST .../activate-dedicated-eps-bearer-context-accept` で答え、この bearer を通話の資源として扱います（IR.92 2.4.3.1）。通話が終わると、ストリームに `DEACTIVATE EPS BEARER CONTEXT REQUEST` が届き、UE は `deactivate-eps-bearer-context-accept` で答えます。

## IMS-AKA を 1 段ずつ追う

### 鍵

| 持つもの | 持っている値 | 書いてある場所 |
| --- | --- | --- |
| volte10 の USIM | K、OPc、SQN_MS | `ue` コマンドの `-k`、`-opc`、`-sqn`（`compose.yaml`） |
| HSS | 同じ加入者の K、OPc、AMF、SQN_HE | `deploy/compose/hss.yaml` の加入者 10 の `aka:` |

K と OPc は MILENAGE の試験データ 1 の値です。OPc は事業者の OP と K から作ります（TS 35.206 4.1）。SIPp のように OP を渡すときは、`-opc` の代わりに `-op` を使います。共有するのはこれだけで、ほかの値は網と UE がそれぞれ、これらの値とランダムな challenge から計算します。

### ラダー図

```text
#698  volte10:5060 -> P-CSCF:5060  REGISTER   Security-Client, Require: sec-agree, Authorization with an empty response
#709  P-CSCF -> I-CSCF             REGISTER   Authorization: ... integrity-protected="no"
#712  I-CSCF -> HSS                UAR / UAA
#721  I-CSCF -> S-CSCF             REGISTER
#724  S-CSCF -> HSS                MAR        SIP-Auth-Data-Item: SIP-Authentication-Scheme Digest-AKAv1-MD5
#726  HSS -> S-CSCF                MAA 2001   SIP-Authenticate (RAND, AUTN), SIP-Authorization (XRES), Confidentiality-Key, Integrity-Key
#727  S-CSCF -> I-CSCF             401        WWW-Authenticate: nonce, algorithm=AKAv1-MD5, ik, ck
#734  I-CSCF -> P-CSCF             401
#737  P-CSCF:5060 -> volte10:5060  401        ik and ck taken out, Security-Server added
#740  volte10:5062 -> P-CSCF:5064  REGISTER   Security-Client, Security-Verify, Authorization with the response from RES
#749  P-CSCF -> I-CSCF             REGISTER   Authorization: ... integrity-protected="yes"
#752  I-CSCF -> HSS                UAR / UAA
#760  I-CSCF -> S-CSCF             REGISTER
#764  S-CSCF -> HSS                SAR / SAA
#771  S-CSCF -> I-CSCF             200 OK
#776  I-CSCF -> P-CSCF             200 OK
#783  P-CSCF:5064 -> volte10:5062  200 OK
#786  P-CSCF -> PCRF               AAR        the signalling path of 172.28.0.120 (see "Policy control")
#793  PCRF -> P-CSCF               AAA 2001
```

1. **保護されていない REGISTER（#698）。** UE は最初の REGISTER を、自分のポート 5060 から P-CSCF のポート 5060 に送ります。そこへ送る REGISTER はこれだけです（TS 24.229 5.1.1.2.2）。`Require: sec-agree` と `Proxy-Require: sec-agree` を付け、自分のセキュリティアソシエーションを `Security-Client: ipsec-3gpp;alg=hmac-sha2-256-128;ealg=null;spi-c=...;spi-s=...;port-c=52975;port-s=5062` で提示します（RFC 3329 2.3.1、TS 33.203 Annex H）。Authorization の username には私的な識別子を入れ、`algorithm=AKAv1-MD5` と、空の nonce と response も入れます。
2. **P-CSCF が Security-Client を預かる（#709）。** P-CSCF は sec-agree を伴う Security-Client を見て、IMS-AKA を選びます（TS 24.229 5.2.2.1）。Security-Client を外して保存し、Authorization に `integrity-protected="no"` を入れます。この REGISTER はまだ何にも保護されていないからです（5.2.2.2）。
3. **S-CSCF がベクトルを求める（#724）。** `integrity-protected="no"` は、IMS-AKA の最初の登録という意味です（5.4.1.1）。MAR は方式 `Digest-AKAv1-MD5` の項目を 1 つ求めます（TS 29.229 6.3.9）。
4. **HSS がベクトルを作る（#726）。** HSS は SQN_HE を進め、RAND を作り、MILENAGE で XRES、CK、IK、AUTN を計算します（TS 33.102 6.3.2、TS 35.206）。SIP-Authenticate は RAND と AUTN を並べた 32 オクテット、SIP-Authorization は XRES、Confidentiality-Key と Integrity-Key は CK と IK です（TS 29.228 6.3）。
5. **401（#727）。** S-CSCF は RAND と AUTN を base64 にして nonce に入れ（RFC 3310 3.2）、IK と CK を P-CSCF 向けに `ik` と `ck` のパラメータに入れます（TS 24.229 5.4.1.2.1A）。答えを確かめるために、RAND と XRES を取っておきます。
6. **P-CSCF が一時的なアソシエーションを作る（#737）。** P-CSCF は 401 から `ik` と `ck` を外します。鍵が UE まで運ばれることはありません。そのうえで自分のアソシエーションを `Security-Server: ipsec-3gpp;q=0.1;alg=hmac-sha2-256-128;ealg=null;spi-c=...;spi-s=...;port-c=50711;port-s=5064` で提示します（5.2.2.2、TS 33.203 7.2）。
7. **UE が網を確かめる（#740）。** USIM は AUTN の MAC を計算し、AUTN の中の MAC と比べます。一致すれば、網が K を知っていると分かります。SQN が新しいことも確かめ（TS 33.102 6.3.3、C.2）、RES、CK、IK を計算します。
8. **アソシエーションを通る REGISTER（#740）。** UE は保護サーバーポート 5062 から、P-CSCF の保護サーバーポート 5064 に答えを送ります（TS 33.203 7.1）。Security-Client をもう一度入れ、Security-Server を `Security-Verify` に写します。途中で誰かがもっと強い提案を消していないかを、P-CSCF が確かめられるようにするためです（RFC 3329）。Digest の response は RES をパスワードにして計算します（RFC 3310 3.3）。
9. **P-CSCF が取り決めを確かめる（#749）。** REGISTER は UE のアドレスとポートから保護ポートに届き、Security-Verify は P-CSCF が送ったものと一致します。1300 バイトを超える REGISTER は TCP で別のポートから届きます。そのときは、P-CSCF は UE のポートを Via から取ります。P-CSCF は Authorization に `integrity-protected="yes"` を入れます。
10. **S-CSCF が RES を確かめる（#760、#764）。** S-CSCF は XRES で Digest を計算して比べます（TS 24.229 5.4.1.2.2）。一致したので、SAR を送って UE を登録します。
11. **200 OK（#783）。** 200 はアソシエーションを通って戻り、アソシエーションはこれで確立します。寿命は登録に合わせて決まります（5.2.2.2）。これ以降、UE はすべての要求をポート 5064 に送り、Route の P-CSCF の URI にもそのポートを書きます。P-CSCF は UE の 5062 番に送ります。P-CSCF が足した Path も保護ポートを指します。
12. **信号の経路（#786）。** UE が登録されると、P-CSCF は、UE が信号用の bearer を失ったら知らせるように PCRF に頼みます（TS 29.214 A.4）。TS 29.213 B.1 の流れのとおり、頼むのは最初の登録が成功したあとなので、認証に失敗した UE のために Rx のセッションを開くことはありません。200 OK とこの AAR は、どちらが先に P-CSCF を出るかが決まっていません。

再登録と登録の解除は、新しい challenge なしにアソシエーションを通ります（TS 24.229 5.1.1.4.2、5.1.1.6.2）。

### 網が登録を終わらせたとき

S-CSCF は、自分から登録を終わらせることがあります。そのときは reg イベントの NOTIFY で、UE の contact を `terminated` にして知らせます。S-CSCF は、同じ知らせを、P-CSCF が自分で持つ購読の NOTIFY でも P-CSCF に送ります。P-CSCF はその NOTIFY で、UE とのアソシエーションを終わらせ（TS 24.229 5.2.5.2）、信号の経路の Rx のセッションを解放します。アソシエーションは、UE への NOTIFY が届くあいだだけ残します。UE は、自分への NOTIFY を受けたら、自分のアソシエーションを消します（5.1.1.7）。`event="deactivated"` なら、UE は登録をやり直します（5.1.1.7）。アソシエーションはもう残っていないので、最初と同じように登録します。上の 1 から 12 がもう一度起き、AAR も新しく送られます。ラダー図は、P-CSCF への NOTIFY と Rx のメッセージを省いています。

```text
 UE                         P-CSCF                     S-CSCF
 |                            |  NOTIFY                  |
 |  NOTIFY (to 5062)          |  event="deactivated"     |
 |<---------------------------|<-------------------------|
 |  200 OK                    |                          |
 |--------------------------->|------------------------->|
 |                            |                          |
 |  REGISTER from 5060 to 5060: empty nonce,             |
 |  no Security-Verify        |                          |
 |--------------------------->|------------------------->|
 |  401, a new Security-Server|                          |
 |<---------------------------|<-------------------------|
 |  REGISTER from 5062 to 5064|                          |
 |--------------------------->|------------------------->|
 |  200 OK                    |                          |
 |<---------------------------|<-------------------------|
 |  SUBSCRIBE (reg event)     |                          |
 |--------------------------->|------------------------->|
```

最初の REGISTER は保護されておらず、Authorization の nonce と response は空です。IMS-AKA の初期登録は、いつもこの形です（5.1.1.2.2）。S-CSCF は新しいベクトルで challenge を返し、P-CSCF は新しいアソシエーションを Security-Server で提示します。UE はそのアソシエーションを通して答え、Security-Verify にその Security-Server を写します。REGISTER は、上のラダー図と同じく I-CSCF を通ります。200 OK のあと、UE は自分の登録状態をもう一度購読します（5.1.1.3）。

### 認証ベクトルを手で確かめる

計算に使う値はすべてキャプチャにあります。上の実行では次のとおりです。

- 401 の nonce は `sUr3Jqwn3AiGpqDGx4msLEZCHVKz2wAAlJATffjznNc=` です。`echo sUr3Jqwn3AiGpqDGx4msLEZCHVKz2wAAlJATffjznNc= | base64 -d | od -An -tx1` で、MAA の SIP-Authenticate と同じ 32 オクテットになります。前半が RAND の `b14af726ac27dc0886a6a0c6c789ac2c`、後半が AUTN の `46421d52b3db 0000 9490137df8f39cd7` です。
- AUTN は SQN ⊕ AK（6 オクテット）、AMF（2 オクテット）、MAC-A（8 オクテット）を並べたものです（TS 33.102 6.3.2）。AMF は hss.yaml でこの加入者に設定した `0000` です。SQN は AK で隠されていて、AK は K と RAND からしか計算できません。
- S-CSCF が出す 401 の `ck` と `ik` は、MAA の Confidentiality-Key と Integrity-Key と同じです。
- 2 回目の REGISTER の response は、XRES の 8 オクテットをパスワードにした RFC 2617 の Digest です（RFC 3310 3.3）。その REGISTER の値（username、realm、nonce、nc、cnonce、qop、uri）と、MAA の SIP-Authorization `927f738d28dc1790` を使うと、シェルで同じ response を計算できます。

  ```sh
  user=001010000000010@ims.mnc001.mcc001.3gppnetwork.org
  realm=scscf.ims.mnc001.mcc001.3gppnetwork.org
  nonce=sUr3Jqwn3AiGpqDGx4msLEZCHVKz2wAAlJATffjznNc=
  ha1=$( { printf '%s:%s:' "$user" "$realm"; printf '\x92\x7f\x73\x8d\x28\xdc\x17\x90'; } | md5sum | cut -d' ' -f1)
  ha2=$(printf 'REGISTER:sip:ims.mnc001.mcc001.3gppnetwork.org' | md5sum | cut -d' ' -f1)
  printf '%s:%s:00000001:OPSWEFQXM5HAX4ZR:auth:%s' "$ha1" "$nonce" "$ha2" | md5sum
  # 3f158cbe664b17c52742ad50ff25d9f5, the response in the capture
  ```

キャプチャの RAND はランダムなので、MILENAGE の出力は TS 35.208 の表の値になりません。表で確かめられるのは計算そのものです。加入者 10 の K と OPc は試験データ 1 と同じなので、その組の RAND、SQN、AMF を与えると、HSS のコードは TS 35.208 4.3.1 の出力をそのまま出します。

```sh
go test ./internal/aka -run TestAuthenticationVectorFromTestSet1 -v
go test ./internal/milenage -run TestConformanceTestSets -v   # all 20 sets of TS 35.208 4.3
```

### ESP を使わない sec-agree

標準では 401 のあとのメッセージをすべて IPsec の ESP で保護します。UE と P-CSCF の保護ポートの間に 2 組のセキュリティアソシエーションを作り、その鍵を IK と CK から導きます（TS 33.203 7.1）。このプロジェクトは既定では、アソシエーションを取り決め、IK と CK とともに P-CSCF の SA の表に保存し、保護ポートも使いますが、ESP はかけません。メッセージはアソシエーションのアドレスとポートから保護サーバーポートに届いたときに、保護されたものとみなします。Security-Client は `ealg=null` で、通信路の上では暗号化も完全性の保護もされていません。

このプロジェクトは既定では信号を暗号化しません。キャプチャで中身を読めるようにするためで、登録した VoLTE の UE の SIP も、ソフトフォンのものと同じように Wireshark で読めます。保護しない IMS-AKA は標準にありません。IPsec を使わない形は、TLS の上の AKAv2-SHA-256 だけです（TS 24.229 4.2B.1）。そのため、これはこのプロジェクト独自の簡略化です。暗号化を null にした ESP なら、キャプチャの中身は同じように読めます。下の「ESP をかけた sec-agree」でその形に切り替えられます。TLS はどちらの形でも使いません。

ESP が無いので、本物の網と 2 つの点が違います。

- どちらの側も保護サーバーポートから送るので、保護クライアントポート（`port-c`）は取り決めるだけで、使いません。
- TCP の要求はそのときどきのポートから届きます。VoLTE の INVITE は 1300 バイトより大きいので TCP で送られ（RFC 3261 18.1.1）、P-CSCF は TCP の要求の UE を Via の sent-by で見分けます。challenge に答える REGISTER は 1300 バイト前後の大きさなので、TCP で送られるかどうかは SPI の桁数で変わります。TCP と UDP は同じアソシエーションを使うので（TS 33.203 7.1）、P-CSCF はこの REGISTER のアソシエーションも Via の sent-by で探します。この sent-by を認証するものは無いので、UE と同じ IP アドレスを持つホストなら、UE の保護サーバーポートを sent-by に書けます。ESP をかければ、送り手はアソシエーションで認証されます。

保護されたとみなすのは、保護サーバーポートに届いたメッセージだけです。そのため、確立したアソシエーションを持つ UE のアドレスとポートから REGISTER 以外の要求がポート 5060 に届いたら、その要求は保護されていません。P-CSCF はこの要求を捨て、100 (Trying) も含めて何も返さず、どこにも転送しません。ログには `discarded an unprotected request from a UE with security associations` と書きます（TS 24.229 5.2.1 NOTE 8、TS 33.203 7.1）。

### ESP をかけた sec-agree

同じ網で、信号を本当に保護することもできます。完全性の保護は両端が取り決めたアルゴリズムで行い、暗号化は null にします。キャプチャで中身を読めるのは、この暗号化のおかげです。ESP の完全性の保護は、TS 33.203 6.3 がどの場合にも求めています。一方、秘匿は P-CSCF の方針が求めるときにだけかけます（6.2）。`ealg=null` も、sec-agree が運ぶ値の 1 つです（Annex H）。つまり、暗号化を null にした網は実際の事業者も運用できる形で、その SIP はそのまま読めます。

ESP を有効にした構成で起動します。

```sh
docker compose -f compose.yaml -f deploy/esp/esp.yaml --profile volte up -d --build
```

この構成は、P-CSCF と 2 台の VoLTE の UE に NET_ADMIN と保護クライアントポートを与えます。P-CSCF では `sec_agree.esp.protected_client_port`、UE では `-port-c` です。どちらの端も、このポートを開いて自分の要求をそこから送り、認証を伴う登録のたびに 4 つのセキュリティアソシエーションをカーネルに入れます。カーネルは両端の保護ポートの間のパケットを保護し、そこに ESP 無しで届いたパケットを捨てます。

コンテナは自分のカーネルを持ちません。エンジンが動くマシンのカーネルを使うので、セキュリティアソシエーションもそのカーネルに入り、アソシエーションを保持する Linux カーネルの XFRM と、その ESP への対応が要ります。たいていの Linux のカーネルには、どちらもあります。Windows でも、Docker Desktop が WSL2 をバックエンドにすると、エンジンは専用の Linux カーネルの上で動くので、やはりあります。ノードは NET_ADMIN で必要なものを読み込むので、手で用意するものはありません。カーネルにこれらが無い環境では、ノードはアソシエーションを入れられません。P-CSCF は `ip xfrm` の誤りを添えて `the store failed` をログに書き、REGISTER に 500 を返します。保護のない信号に切り替わるのではなく、登録そのものが失敗します。

キャプチャでは、最初の REGISTER と 401、そしてほかのノードどうしのやり取りは今までどおりに見えます。登録した VoLTE の UE と P-CSCF の間には SIP が無くなり、`ESP (SPI=0x...)` だけが並びます。SPI は Security-Client と Security-Server に書かれていた値なので、どのパケットがどのアソシエーションを通ったかが分かります。ポートでも同じことが分かります。UE の要求は保護クライアントポート 5063 から P-CSCF の保護サーバーポート 5064 へ、その応答は逆向きに流れます。P-CSCF の要求は保護クライアントポート 5065 から UE の保護サーバーポート 5062 へ、その応答もまた逆向きです。

アソシエーションは各コンテナのカーネルの中にあり、UE が登録しているあいだ残ります。

```sh
docker compose exec pcscf ip xfrm state
docker compose exec pcscf ip xfrm policy
```

state の 1 つ 1 つがアソシエーションです。両端のアドレス、`proto esp` と SPI、`mode transport`、IK_ESP と完全性チェック値の 128 ビットを持つ完全性のアルゴリズム、そして空の鍵による null の暗号化が並びます。IK_ESP は IK そのものではありません。Annex I が、TS 33.220 Annex B.2.0 の鍵導出関数で CK と IK から作ります。policy は、アソシエーションを 1 組のポートに結び付けるもので、UDP と TCP に 1 つずつあります。2 つのトランスポートが同じアソシエーションを使うためです（TS 33.203 7.1）。ノードは、読み手が手で打てるのと同じコマンドを実行します。その 1 つは次のようなものです。

```sh
ip xfrm state add src 172.28.0.120 dst 172.28.0.40 proto esp spi 0x3b9b0ea3 \
  reqid 1000017571 mode transport replay-window 32 \
  auth-trunc 'hmac(sha256)' 0x<IK_ESP> 128 enc 'ecb(cipher_null)' ''
```

カーネルに寿命は渡しません。TS 33.203 7.1 は、セキュリティアソシエーションの寿命を SIP のアプリケーションに任せていて、アプリケーションがそれを持ちます（7.4）。UE が challenge に答えるあいだは認証を終えるのに要る時間、200 (OK) で確立したあとは登録の残りの時間です。その時間が過ぎると P-CSCF と UE が消すので、返事をしなくなった UE のアソシエーションは、登録が終わらなくても消えます。

ESP の中の SIP を読むには、完全性チェック値の長さを Wireshark に教えます。長さは完全性のアルゴリズムから決まります。**Edit > Preferences > Protocols > ESP** で **Attempt to detect/decode encrypted ESP payloads** にチェックを入れ、**ESP SAs** を開いて、次の 1 行を足します。

| Protocol | Src IP | Dest IP | SPI | Encryption | Encryption Key | Authentication | Authentication Key |
| --- | --- | --- | --- | --- | --- | --- | --- |
| IPv4 | `*` | `*` | `*` | `NULL` | | `HMAC-SHA-256-128 [RFC4868]` | |

鍵はどちらも要りません。暗号化されていませんし、Wireshark は完全性を確かめないためです。同じ設定をコマンドで書くと、キャプチャの SIP はこう読めます。

```sh
tshark -r captures/ims-*.pcap -o esp.enable_encryption_decode:TRUE \
  -o 'uat:esp_sa:"IPv4","*","*","*","NULL","","HMAC-SHA-256-128 [RFC4868]",""' \
  -d tcp.port==5062,sip -d tcp.port==5064,sip \
  -Y 'esp && sip'
```

2 つの `-d` は、保護ポートを TCP の SIP として扱うためのものです。SIP はポート 5060 に登録されていて、それ以外のポートでは推定で見つけますが、ESP の中では手がかりが減ります。Wireshark では、そのストリームのパケットを選んで **Decode As** するのと同じことです。

同じ画面には、行を足さずに済む設定もあります。**Attempt to detect/decode NULL encrypted ESP payloads**（`esp.enable_null_encryption_decode_heuristic`）です。この設定は、完全性チェック値の長さを教わる代わりに自分で割り出します。12、16、24、32 オクテットの順に試し、padding と次ヘッダが整う最初の長さを採ります。キャプチャを 1 つ覗くだけなら、これが一番早い方法です。上の行はアルゴリズムを示すので、長さは推定せずに決まります。このプロジェクトの確認が行を使うのは、そのためです。

ESP をかけても、本物の網と 3 つの点が違います。保護クライアントポートは、どちらの端でも設定に書いた 1 つのポートで、UE が変わっても認証をやり直しても同じです。TS 33.203 7.4.0 は、認証を伴う再登録のたびに変えることを求めています。2 つ目は、P-CSCF がコアへの転送を、要求が届いたトランスポートからそのまま行うことです。そのためコアには保護サーバーポートからの要求が届き、そのパケットを守るアソシエーションはありません。P-CSCF 自身が UE に送る要求は保護クライアントポートから、それ以外は待ち受けているポートから出ます。3 つ目は、セキュリティアソシエーションが、それを作った 1 台の P-CSCF のカーネルにしか無いことです。SA の表は複数の P-CSCF で共有できますが、ある 1 台を通して登録した UE が別の 1 台に送ると、アソシエーションが無いものとして扱われ、登録をやり直します。

### SIPp

SIPp は `images/softphone/sipp/register-aka.xml` のシナリオで、IMS-AKA で登録します。SIPp はすべてを 1 つのポート 5060 から送るので、そのポートが保護サーバーポートにもなります。SIPp 3.7 は K と OP を文字列のバイトとして受け取るので、加入者 11 の鍵は印字できる文字列の `ims-practice-K01` と `ims-practice-OP1` にしました。hss.yaml にはその 16 進と、その OP から MILENAGE で作った OPc を書いています。SIPp は RES と Digest を自分で計算するので、S-CSCF の計算をほかの人が書いたコードと照らし合わせられます。

## ISIM の無い端末の識別子

volte10 と volte12 のカードには USIM があり、ISIM はありません。USIM には IMSI と鍵があっても、IMS の識別子はありません。そこで端末は IMSI（ここでは 001010000000010）から識別子を作ります（TS 24.229 5.1.1.1A、TS 23.003 13.2、13.3、13.4B）。

| 識別子 | 値 | 現れる場所 |
| --- | --- | --- |
| ホーム網のドメイン | `ims.mnc001.mcc001.3gppnetwork.org` | REGISTER の Request-URI。 |
| 秘密の識別子（private user identity） | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | Authorization の `username` と、Cx の User-Name。 |
| 一時的な公開の識別子（temporary public user identity） | `sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | REGISTER の From と To、Cx の Public-Identity。ほかには現れない。 |

一時的な公開の識別子には IMSI が入っているので、網の外には出しません。TS 23.228 E.3.1 は、運用者がこの識別子を規制（barring）することを強く勧めています。規制すると、この識別子は登録にだけ使われ、見えるのは CSCF と HSS だけになり、端末も利用者に見せません。この網の HSS はこの識別子を規制しています。`deploy/compose/hss.yaml` の加入では `barred_identities` に挙げてあり、SAA のユーザープロファイルでは `<BarringIndication>` が付きます（TS 29.228 B.2.1）。同じ暗黙の登録セットには利用者の番号の識別子 `sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org` と `tel:+12025550110` があり、こちらは規制されていません。

それぞれのノードは次のように動きます。

1. **HSS。** 一時的な識別子の UAR は成功します。その暗黙の登録セットに、規制されていない識別子があるためです。規制された識別子しかないセットなら、DIAMETER_AUTHORIZATION_REJECTED を返します（TS 29.228 6.1.1.1 の手順 4）。
2. **登録のときの S-CSCF。** プロファイルの識別子のうち、規制されていない最初のものが既定の公開の識別子です（TS 29.228 6.5.1.1）。200 OK の P-Associated-URI には規制されていない識別子だけを、既定の識別子を先頭にして並べます（TS 24.229 5.4.1.2.2F）。reg イベントでも、それらだけを知らせます（5.4.2.1.2）。AS への第三者 REGISTER の To には、既定の識別子を入れます（5.4.1.7）。
3. **UE。** P-Associated-URI の最初の URI を、既定の公開の識別子として覚えます。登録した識別子はこの一覧に無いので、規制されたものとして扱います（5.1.1.2.1）。INVITE と MESSAGE の From には既定の識別子を書き、reg イベントもこの識別子で購読します（5.1.1.3）。端末の状態にも既定の識別子を表示します。登録を終えるまでは、識別子を何も表示しません。規制された一時的な識別子は、UE がだれにも見せないためです（5.1.1.1A）。それまでは、Ut の要求も断ります。Ut の要求には、既定の識別子を XUI として使うためです。
4. **P-CSCF。** UE の P-Preferred-Identity が P-Associated-URI のどれかを指していれば、その識別子を、そうでなければ既定の識別子を P-Asserted-Identity に入れます（5.2.6.3.1）。UE シミュレータは P-Preferred-Identity を送らないので、P-Asserted-Identity は既定の識別子になります。
5. **ほかのリクエストでの S-CSCF。** 規制された識別子が利用者（served user）として発信するリクエストには 403 を（5.4.3.2 の手順 1）、規制された識別子あてのリクエストには 404 を返します（5.4.3.3 の手順 1）。番号の識別子を Sh で HSS に問い合わせた AS にも、規制された識別子は返りません（TS 29.328 7.6.2）。

VoLTE の通話のキャプチャでは次のようになります。

```text
REGISTER  volte10 -> P-CSCF  To: <sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org>
200 OK    P-CSCF -> volte10  P-Associated-URI: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
                             P-Associated-URI: <tel:+12025550110>
INVITE    volte10 -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
INVITE    P-CSCF -> S-CSCF   P-Asserted-Identity: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
MESSAGE   volte10 -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
```

Wireshark のフィルタ `sip && !(sip.CSeq.method == "REGISTER") && frame contains "sip:001010000000010@ims"` に合うパケットはありません。Contact にも IMSI は入りません。`sip.Contact contains "001010000000010"` に合うパケットもありません。TS 24.229 5.1.1.2.1 の項目 c は、Contact の URI のユーザー部を、世界で一意で、私的な情報を明かさないものにするよう求めています。そこで UE は秘密の識別子から K を鍵にして導いた UUID をユーザー部に入れます。このプロジェクトのほかの UE も同じで、ソフトフォンはパスワードを鍵にします。ほかの手順書のソフトフォンには、SIM カードがそもそもありません。識別子は IMC に入っているのと同じように設定で与えられます（TS 24.229 5.1.1.1B.1）。ソフトフォンは規制されていない公開の識別子を登録し、それをそのまま使います。

ENUM は VoLTE の UE の番号を、一時的な識別子ではなく番号の SIP URI に変えます。`tel:+12025550112` への通話が着信してよい識別子に届くようにするためです（`deploy/compose/dns/e164.arpa.zone`）。

## Rx でのポリシー制御

P-CSCF はポリシーと課金の制御のアプリケーション機能（AF）で、アクセス網が何を運ぶかは PCRF が決めます（TS 29.214 4 章）。Rx のセッションは 1 つずつが Diameter のセッションです。登録した UE ごとに信号の経路のセッションが 1 つ、通話ごとに両側のメディアのセッションが 1 つずつあります。

### 信号の経路

登録のすぐあとの AAR（#786）には Media-Component-Description が 1 つあります。Media-Component-Number は 0、Flow-Number も 0 で、`Flow-Usage` は AF_SIGNALLING です。`Specific-Action` は INDICATION_OF_LOSS_OF_BEARER（2）と INDICATION_OF_RELEASE_OF_BEARER（4）の 2 つで、`Framed-IP-Address` は 172.28.0.120 です（TS 29.214 4.4.5）。PCRF はこのアドレスで UE の IP-CAN セッションと結びつけ、2001 で答えます。

ue2 や pjsua のように attach していない UE では、PCRF は IP-CAN セッションを見つけられず、Experimental-Result-Code の IP-CAN_SESSION_NOT_AVAILABLE（5065）で答えます。P-CSCF はその UE をポリシー制御なしで扱い、通話の AAR も送りません。そのため、ソフトフォンの登録でも、この AAR と 5065 がキャプチャに出ます。

### 通話のメディア

両側の P-CSCF は、通話の SDP の answer がそろうと AAR を送ります（TS 29.214 A.1）。着信側の P-CSCF は volte12 から 183 を受けたとき、発信側の P-CSCF は volte10 に向かう 183 を受けたときです。上の実行の、着信側の AAR（#1036）は次のとおりです。

| AVP | 値 | 意味 |
| --- | --- | --- |
| `AF-Application-Identifier` | `urn:urn-7:3gpp-service.ims.icsi.mmtel` | サービス。S-CSCF が付けた P-Asserted-Service から取る。送るのは着信側の P-CSCF だけ（「このプロジェクトで簡略化していること」を参照）。 |
| `Media-Component-Description` | 番号 1 | 1 つ目の m= の行（TS 29.213 6.2 Table 6.2.1）。 |
| `Media-Sub-Component` | フロー 1 と 2 | RTP と RTCP。それぞれに 2 つの `Flow-Description` がある。`permit in 17 from 172.28.0.121 to 172.28.0.50 40004` と `permit out 17 from 172.28.0.50 to 172.28.0.121 60734`。メディアのもう一方の端は中継ノード。 |
| `Media-Type` | `AUDIO` | |
| `Max-Requested-Bandwidth-UL` と `-DL` | 80000 と 41000 | offer と answer の b=AS から決めた値。単位は bit/s。 |
| `RR-Bandwidth`、`RS-Bandwidth` | 4000、0 | b=RR と b=RS から。 |
| `Flow-Status` | `ENABLED`（2） | 方向の属性 sendrecv から。 |
| `Codec-Data` | `uplink offer m=audio ...` と `downlink answer m=audio ...` | offer と answer の m= の行と属性。 |
| `Specific-Action` | 2、4、9 | bearer の喪失と解放、資源の割り当ての失敗を知らせてもらう。 |
| `Service-Info-Status` | FINAL_SERVICE_INFORMATION | offer だけでなく answer に基づいた情報。 |
| `Framed-IP-Address` | 172.28.0.121 | UE。PCRF はこれで IP-CAN セッションを見つける。 |
| `Rx-Request-Type` | INITIAL_REQUEST | 通話のための新しい Rx のセッション。 |

PCRF は音声を QCI 1 で許可し（TS 29.213 6.3、IR.92 4.4.2）、スタブが dedicated bearer を作り、PCRF が 2001 で答えます。そのあと UPDATE や保留で新しい SDP の offer と answer があるたびに、同じセッションで `Rx-Request-Type: UPDATE_REQUEST` の AAR が送られます。保留中は、発信側の `Flow-Status` が `ENABLED-UPLINK`（0）、着信側が `ENABLED-DOWNLINK`（1）になります。

### 通話の終わり

BYE が通ると、両側の P-CSCF は STR で Rx のセッションを終えます。`Termination-Cause` は DIAMETER_LOGOUT です（TS 29.214 4.4.4）。PCRF は STA で答え、スタブは DEACTIVATE EPS BEARER CONTEXT REQUEST で bearer を解放します。

### 網が bearer を落とす

スタブは、無線の切断と同じように bearer を非活性化できます。網が始める EPS bearer コンテキストの非活性化です（TS 24.301 6.4.4.2）。`/network-initiated/ues/<IMSI>/bearers/<EBI>` への DELETE でこの手順が始まり、`/network-initiated/ues/<IMSI>/detach` への POST で網が始める detach（5.5.2.3）が始まります。ラボの **Access network (EPC)** のパネルには、bearer の隣と UE の隣に、それぞれを起こすボタンがあります（[ラボの手順書](lab.md) の「bearer を落とし、UE を切り離す」）。ラボは要求をそのまま取り次ぐので、ホストから次のように操作することもできます。

```sh
curl http://localhost:8080/api/epc/ues/001010000000010                       # the bearers of volte10
curl -X DELETE http://localhost:8080/api/epc/network-initiated/ues/001010000000010/bearers/6    # deactivate the voice bearer
```

```text
#7775 Lab -> EPC        DELETE /network-initiated/ues/001010000000010/bearers/6
#7776 EPC -> volte10    DEACTIVATE EPS BEARER CONTEXT REQUEST (event stream)
#7778 PCRF -> P-CSCF    ASR   Abort-Cause BEARER_RELEASED (0)
#7785 P-CSCF -> PCRF    ASA 2001
#7787 P-CSCF -> PCRF    STR
#7804 P-CSCF -> S-CSCF  BYE   in volte10's name, Reason: SIP;cause=503;text="Service Unavailable"
#7806 P-CSCF -> volte10 BYE   Reason: SIP;cause=503
#7872 P-CSCF -> volte12 BYE   through the S-CSCF and the MMTel AS, with the same Reason
#7885 EPC -> volte12    DEACTIVATE EPS BEARER CONTEXT REQUEST
```

音声の bearer がセッションのメディアをすべて運んでいたので、PCRF は失ったフローを RAR で知らせるのではなく ASR で Rx のセッション全体を終えます（TS 29.214 4.4.6.1、4.4.6.2）。P-CSCF は ASR に答え、STR でセッションを終え、自分で通話を解放します。served user の名前で相手側に BYE を送り、このプロジェクトでは served user の UE にも BYE を送ります（TS 24.229 5.2.8.1.2、IR.92 2.4.2.3）。BYE は、P-CSCF が `Store` の後ろに保存したダイアログの情報（Contact、タグ、CSeq、経路）から作ります。P-CSCF は 2 つの BYE に応答が来るまでダイアログを残し、その間にどちらかの側がダイアログの中で送った要求には、自分で 481 を返します（TS 24.229 5.2.8.1.3）。volte10 は自分では BYE を送りません。volte12 の通話が終わると、その P-CSCF が volte12 の Rx のセッションを終え、スタブが volte12 の bearer も解放します。

通話に答える前に bearer を失うこともあります。そのとき P-CSCF は INVITE をあきらめ、発信側に 500 を返し、着信側への INVITE を 503 を示す Reason 付きの CANCEL で取り消します（TS 24.229 5.2.8.1.1）。着信側が CANCEL を受け取る直前に 200 を返していると、200 は CANCEL と行き違います（RFC 3261 9.1）。発信側は 500 を受け取っていてこの 200 を見ないので、P-CSCF が発信側の名前で、200 とその送り直しのそれぞれに ACK を送ります。そのあとで P-CSCF は上と同じ形の BYE を着信側に送り、200 で確立したダイアログを解放します（5.2.8.1.2）。

## プリコンディション

VoLTE の発信側は、音声がまだ流れないうちに相手の端末が鳴ることを望みません。プリコンディションのしくみ（RFC 3312）では、両側が SDP で自分の資源を確保したかを示し、着信側は必須のプリコンディションがすべて満たされてから呼び出しを始めます。IR.92 2.4.1 はこのしくみを求め、状態の更新に UPDATE を使うよう求めています。INVITE は `Supported: 100rel, timer, 199, precondition` で、Require には入れません（TS 24.229 5.1.3.1）。

```text
#871  volte10 -> P-CSCF  INVITE       offer, curr:qos local none
#1026 P-CSCF -> volte12  INVITE       through the S-CSCFs and the MMTel AS of each user
#1030 volte12 -> P-CSCF  183          Require: 100rel, precondition; answer, conf:qos remote sendrecv
#1036 P-CSCF -> PCRF     AAR          volte12's media; the stub sets up volte12's bearer (#1040, #1046)
#1083 P-CSCF -> PCRF     AAR          volte10's media; the stub sets up volte10's bearer (#1087, #1089)
#1094 P-CSCF -> volte10  183
#1099 volte10 -> P-CSCF  PRACK        and its 200 (#1120)
#1126 volte10 -> P-CSCF  UPDATE       curr:qos local sendrecv
#1212 P-CSCF -> volte12  UPDATE
#1216 volte12 -> P-CSCF  200 (UPDATE) curr:qos local sendrecv, curr:qos remote sendrecv
#1219 volte12 -> P-CSCF  180          only now: every mandatory precondition is met
#1370 volte12 -> P-CSCF  200 (INVITE)
#1429 P-CSCF -> volte10  200 (INVITE) and the ACK (#1432)
```

SDP ごとのプリコンディションの行です。どの行もその SDP を送った側から見た値です（local は送った側の区間です）。

| 段階 | 送った側 | `curr:qos local` | `curr:qos remote` | `des:qos` | `conf:qos` |
| --- | --- | --- | --- | --- | --- |
| INVITE | volte10 | none | none | mandatory local sendrecv、optional remote sendrecv | |
| 183 | volte12 | none | none | mandatory local sendrecv、mandatory remote sendrecv | remote sendrecv |
| UPDATE | volte10 | sendrecv | none | mandatory local sendrecv、mandatory remote sendrecv | |
| UPDATE への 200 | volte12 | sendrecv | sendrecv | mandatory local sendrecv、mandatory remote sendrecv | |

- 発信側は segmented status type を使います。言えるのは自分の区間を確保したかどうかだけです（TS 24.229 6.1.2）。
- 着信側は 183 で、発信側の区間の強さを mandatory に上げ、`conf:qos remote sendrecv` で、発信側の資源がそろったら知らせるように求めます（RFC 3312 7 章）。
- UE は通話のメディアを TFT に含む dedicated bearer を受けると、自分の資源を確保したものとみなします（TS 24.229 L.2.2.5.1B、IR.92 2.4.3.1）。UPDATE は PRACK への 200 を受けてから送ります。
- 着信側は、必須のプリコンディションがすべて満たされるまで呼び出しを始めません（RFC 3312 6 章）。キャプチャでも、volte12 の 180 は UPDATE への 200 のあとです。発信側では、180 が UPDATE への 200 より先に届くことがあります。P-CSCF が新しい answer の AAR のあいだ、その 200 を止めておくためです。

ue2 のように EPC スタブを使わない UE は、最初から資源があるとみなし、`curr:qos local sendrecv` で答えます。`-no-preconditions` を付けた UE はこのしくみを使いません。

## コーデック

### offer と answer

```text
m=audio 53946 RTP/AVP 96 97 98 99 0 100 101
b=AS:80
b=RS:0
b=RR:4000
a=rtpmap:96 AMR-WB/16000/1
a=fmtp:96 mode-change-capability=2; max-red=0
a=rtpmap:97 AMR-WB/16000/1
a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1
a=rtpmap:98 AMR/8000/1
a=fmtp:98 mode-change-capability=2; max-red=0
a=rtpmap:99 AMR/8000/1
a=fmtp:99 mode-change-capability=2; max-red=0; octet-align=1
a=rtpmap:0 PCMU/8000
a=rtpmap:100 telephone-event/16000
a=fmtp:100 0-15
a=rtpmap:101 telephone-event/8000
a=fmtp:101 0-15
a=ptime:20
a=maxptime:240
```

- AMR-WB を先に、次に AMR を、それぞれ RFC 4867 の 2 つのペイロード形式で並べます。IR.92 3.2.5 が発信側に優先させる bandwidth-efficient（96、98）と、octet-aligned（97、99）です。mode-set は付けないので、すべてのモードを使えます（IR.92 2.4.3.2）。
- PCMU はソフトフォンと JJ-90.30 の網間接続のために最後に残しています。そのため b=AS はヘッダを含めた PCMU の帯域の 80 kbit/s になり、P-CSCF が PCRF に求める上りの帯域もこの値になります。
- telephone-event は音声のコーデックそれぞれのクロックで入れます（TS 26.114 Annex G）。
- b=RS:0 と b=RR:4000 は送信者と受信者の RTCP の帯域です。単位は bit/s です（RFC 3556 2 章）。UE は報告の間隔をこれから計算し（RFC 3550 6.3.1）、5 秒より短くはしません。両方が 0 なら RTCP を送りません（IR.92 3.2.4）。

volte12 は `m=audio 60734 RTP/AVP 96 100` で答えます。bandwidth-efficient の AMR-WB と telephone-event/16000 で、`b=AS:41` です。PCMU しか知らない ue2 は `m=audio ... RTP/AVP 0 101` で答え、VoLTE の UE はそのあと PCMU で話します（並びは `ue -codecs` で変えられます）。

### フレーム

UE は AMR の符号化も復号もしません。RFC 4867 5 章の格納形式で用意したフレームを送ります。`internal/ue/speech` に組み込んだ試験音（cgo を使う道具 `tools/amrgen` で作ったもので、製品のビルドには入りません）か、`-amr-in` で渡したファイルです。RTP への詰め込みは UE が自分で行います。

```text
Adaptive Multi-Rate
    [Payload decoded as: RFC 3267 bandwidth-efficient mode (1)]
    1111 .... = CMR: No mode request (15)
    .... 0... = F bit: Last frame in this payload
    .... .100  0... .... = FT bits: AMR-WB 23.85 kbit/s (8) / Frame OK
    .1.. .... = Q bit: Ok
```

1 つのパケットに 20 ms のフレームが 1 つ入ります。4 ビットのコーデックモード要求（CMR）、F、FT、Q を持つ目次（ToC）の項目、音声のビットの順です（RFC 4867 4.3）。受けた側は `-received-out` で、受け取ったフレームを格納形式に戻して書き出します。送った側が `-sent-out` で書いたものとビット単位で比べられ、e2e のスクリプトがその比較をしています。

### 通話の音声を聞く

ここには音声を復号するものがありません。UE はフレームを運ぶだけで、音にするのは手元の機械の再生ソフトです。その再生ソフトは FFmpeg です。AMR と AMR-WB の復号器を自分で持っていて、`ffplay` も付いてきます。入れるかどうかは学習者が決めます。このプロジェクトは FFmpeg を同梱も取得もせず、コンテナの中では音声を復号しません。

下の 3 つのやり方は、どれもこの手順書の網が動いていること（`docker compose up -d --build`）が前提です。そして、どれか 1 つだけを動かします。`volte` プロファイルの UE と、`docker compose run` で 1 回だけ動かす UE は、volte10 と volte12 のアドレスを共有するためです。

**手元の機械で聞く。** まず `volte` プロファイルの volte12 を起動します。この UE は着信を待ち、自分で応答します。次に UE を手元の機械向けにビルドし、コンテナの隣で動かします。やり方は [音声通話の流れを追う](call.md) の「自分のマシンから話す」にあります。`-codecs` で AMR-WB を求めます（`-epc` の無い UE は自分からは AMR-WB を offer しません）。`-play` を付けると、返ってきた音声が届くたびに鳴ります。

```sh
docker compose --profile volte up -d --no-deps volte12
docker run --rm -v "$PWD:/src" -w /src golang:1.27.4-alpine go build -o ue ./cmd/ue
./ue call -pcscf 127.0.0.1:5060 -listen 127.0.0.1:0 -media-listen 127.0.0.1:0 \
  -codecs AMR-WB,AMR,PCMU -play tel:+12025550112
```

volte12 は AMR-WB で応答して試験音を鳴らし、その音が聞こえます。UE は `ffplay` を自分で起動し、通話のコーデックに合った指定を渡して、フレームをパケットごとに書き込みます。`PATH` に無いときは `-player /path/to/ffplay` で場所を教えます。渡せるのは `ffplay` か、ffplay と同じ指定を受け取れるプログラムです。再生ソフトが無ければ、UE はそれを 1 度伝え、通話はそのまま続きます。再生ソフトが止まったときや、通話に追いつかなくなったときも同じです。

**コンテナから聞く。** コンテナの中の UE には音を出す装置がありません。スピーカーは手元の機械にあります。`-received-out -` は、パケットが届くたびにストリームを標準出力へ書くので、手元の再生ソフトがパイプで読めます。アドレスはプロファイルの UE が持っているので、先に止めます。

```sh
docker compose --profile volte rm -sf volte10 volte12
```

1 つ目の端末で、volte12 に着信を待たせ、聞こえたものを再生ソフトに渡します。

```sh
docker compose run --rm -T --no-deps volte12 answer -epc http://172.28.0.80:8080 \
  -impi 001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3 \
  -received-out - | ffplay -nodisp -autoexit -i -
```

2 つ目の端末で、volte10 から volte12 を呼びます。volte10 は 3 秒間、試験音を鳴らして切ります。

```sh
docker compose run --rm --no-deps volte10 call -epc http://172.28.0.80:8080 \
  -impi 001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf \
  -tone 3s tel:+12025550112
```

`-T` は、Docker が端末を割り当てないようにするものです。端末があると、音声のバイトが書き換わります。UE の JSON のログは標準エラー出力に移るので、パイプを通るのは音声だけです。再生ソフトは最初にストリームの中身を調べます。ffplay では 1 秒ほどかかり、その分だけ聞こえる音は通話より遅れます。再生ソフトを閉じても、止まるのは音だけで、通話は続きます。PCMU の通話では、ペイロードがそのまま流れるので、`ffplay -f mulaw -ar 8000 -ac 1 -i -` で鳴らせます。AMR と AMR-WB は、RFC 4867 5 章の格納形式を読める再生ソフトなら、ほかのものでも鳴らせます。

**通話のあとで聞く。** `-` の代わりにパスを渡すと、UE は通話が終わるときに録音をまとめて書きます。`scripts/e2e-volte.sh` は、volte10 が送ったものと volte12 が受け取ったものを `captures/` に残します。

```sh
ffplay captures/volte12-received.amr
ffmpeg -i captures/volte12-received.amr captures/volte12-received.wav
```

聞こえるのは、相手の UE が鳴らした試験音か、相手に `-amr-in` で渡したファイルです。1 つも受け取らなかった通話では、ファイルはマジックナンバーだけになり、ストリームは空になります。マジックナンバーは最初のフレームと一緒に出ていくためです。保留や、試験音より長い通話のように、通話の中に音の無い区間があると、そこは NO_DATA のフレームとして格納され、録音の時間の進みが保たれます（RFC 4867 5.3）。AMR-WB の復号器は、これを無音にします。FFmpeg の狭帯域の AMR の復号器は読み飛ばすので、音の無い区間がある AMR の通話は、通話より短くなります。その場所が再生ソフトに流れたあとで届いたフレームは、聞こえる音には入りません。通話が終わってから書くファイルには、その場所に入ります。

Wireshark はキャプチャの中のフレームを見せます。RTP を再生できるのは、そのビルドが復号できるコーデックだけです（**Help > About Wireshark > Plugins** に一覧があります）。

### RTCP と DTMF

両方の UE は中継ノードを通して RTP の 1 つ上のポートに RTCP の送信者レポートを送ります（RFC 3550 6.4.1、IR.92 3.2.4）。DTMF の数字は RTP のストリームの中の telephone-event で送ります（RFC 4733）。1 つの数字は続けて送るパケットで表し、最後の 3 つに終わりのビットが立ちます。中継ノードはそのまま渡します。

## セッションタイマー、Reason、早期メディア

- **セッションタイマー。** INVITE には `Supported: timer` と `Session-Expires: 1800` があります（IR.92 2.2.8）。着信側の 200 には `Require: timer` と `Session-Expires: 1800;refresher=uac` があります（RFC 4028 9 章）。発信側は 900 秒ごとに UPDATE でセッションを更新し、更新が来なければどちらの側もセッションを終えます。
- **Reason。** UE の BYE には `Reason: RELEASE_CAUSE;cause=1;text="User ends call"` があります（TS 24.229 7.2A.18.11、IR.92 2.2.4）。メディアの bearer を失って終えた通話では cause が 3 になります。P-CSCF が送る BYE には `Reason: SIP;cause=503` があります。
- **早期メディア。** INVITE には `P-Early-Media: supported` があります（RFC 5009、IR.92 2.2.7）。この網には早期メディアを許可するノードが無いので、180 のあと、発信側の状態には自分で呼び出し音を鳴らしていることが出ます。UE は 199（RFC 6228）にも対応しています。199 は CFNR で MMTel AS が送ります（[mmtel.md](mmtel.md)）。

## 保留、再開、DTMF

`scripts/e2e-volte.sh` がラボから操作する通話です。

```text
#4604 volte10 -> P-CSCF  INVITE      to volte12, set up as above
#5820 volte10 -> relay   telephone-event 1, end   ... then 5, 9 and # (event 11)
#5953 volte10 -> P-CSCF  re-INVITE   a=sendonly: on hold
#6003 volte12 -> P-CSCF  200         a=recvonly
#6151 volte10 -> P-CSCF  re-INVITE   a=sendrecv: resumed
#6201 volte12 -> P-CSCF  200         a=sendrecv
#6347 volte10 -> P-CSCF  BYE
```

re-INVITE は MMTel AS をそのまま通り（TS 24.610 4.5.2.1）、新しい answer のたびに両方の Rx のセッションが更新されます。

## 動かしてみる

`scripts/e2e-volte.sh` は、この手順書のことをすべて行い、キャプチャで各メッセージを順に確かめます。キャプチャは `captures/` に、登録、通話、Rx、EPC スタブの API のフレームは `e2e-out/volte-ladder.txt` に、AMR-WB の録音は `captures/volte-*.amr` に、UE のログと、手元の機械の UE が聞いたものは `e2e-out/` に残ります。かかる時間は 2 分ほどです。

`scripts/e2e-esp.sh` は、UE と P-CSCF のあいだの信号に IPsec の ESP をかけて登録と通話を行い、キャプチャに加えてカーネルのセキュリティアソシエーションも確かめます。その構成を手で起動する方法と、取れたキャプチャの読み方は、[ESP をかけた sec-agree](#esp-をかけた-sec-agree)にあります。

### ラボで

```sh
docker compose up -d --build
docker compose --profile volte up -d --no-deps volte10 volte12
```

`--no-deps` を付けるのは、UE が依存する P-CSCF や EPC などのノードを Compose に作り直させないためです。新しくビルドしたあとは、Compose がそれらのノードのイメージを変わったものとみなして作り直すことがあり、作り直したノードは Diameter の接続を失っています。

http://localhost:8080 を開きます。volte10 と volte12 のカードには、登録済みと出ます。volte10 から `tel:+12025550112` に発信すると、volte12 は呼び出し時間のあとで自分で応答します。カードには保留、再開、DTMF のボタンがあり、通話のコーデックとプリコンディションの表が出ます。通話のラダー図には、P-CSCF の隣に PCRF の列があります。上の `curl` で音声の bearer を解放すると、P-CSCF が通話を終わらせるところを見られます。

### コマンドラインで

プロファイルの VoLTE の UE と単発のコマンドは、volte10 と volte12 のアドレスを共有します。先にプロファイルを止めてください（`docker compose --profile volte rm -sf volte10 volte12`）。そのあとで、volte12 に着信を待たせます。このコマンドは通話が終わるまで戻りません。

```sh
docker compose run --rm --no-deps volte12 answer -epc http://172.28.0.80:8080 \
  -impi 001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3 \
  -received-out /captures/volte12-received.amr
```

別の端末で、volte10 から volte12 に発信します。

```sh
docker compose run --rm --no-deps volte10 call -epc http://172.28.0.80:8080 \
  -impi 001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf \
  -sent-out /captures/volte10-sent.amr tel:+12025550112
```

VoLTE の UE に使うフラグです。

| フラグ | 意味 |
| --- | --- |
| `-epc URL` | 先に EPC スタブに attach し、P-CSCF を PCO から取る。 |
| `-k`、`-opc` か `-op`、`-sqn` | SIP Digest の代わりに、これらの鍵で IMS-AKA を使う。 |
| `-port-s` | 保護サーバーポート。既定は 5062。 |
| `-codecs` | offer する音声のコーデック。`AMR-WB,AMR,PCMU` のように書く。既定は、`-epc` があればすべて、無ければ PCMU。 |
| `-no-preconditions` | プリコンディションのしくみを使わない。 |
| `-amr-in`、`-sent-out`、`-received-out` | AMR か AMR-WB のファイルを再生し、送ったものと受けたものを書き出す。`-received-out -` は、受け取ったものを通話の最中に標準出力へ流す。 |
| `-play`、`-player` | 受け取ったものを通話の最中に手元の再生ソフトで鳴らす。鳴らすプログラムを指定する。 |

SIPp は IMS-AKA で登録し、登録を解除します。

```sh
docker compose --profile ue build softphone
docker compose run --rm --entrypoint sipp softphone -sf /etc/sipp/register-aka.xml -m 1 -t u1 -p 5060 172.28.0.40:5060
```

## キャプチャを開く

`captures/` の最新のファイルを Wireshark で開き、次の表示フィルタをかけます。

```text
(sip && !(sip.CSeq.method in {"SUBSCRIBE", "NOTIFY", "MESSAGE"})) || diameter.cmd.code in {265, 274, 275, 303} || (http && ip.addr == 172.28.0.80)
```

登録、通話、Rx、MAR と、EPC スタブの API が出ます。Wireshark は AMR-WB のフレームを、見た SDP に従って復号します。ペイロードタイプ、AMR-WB であること、bandwidth-efficient であることは、rtpmap と fmtp の行から決まります。その SDP が無いときは、**Preferences > Protocols > AMR** で Wideband AMR とペイロード形式を選び、RTP のポートに **Decode As** を使います。**Telephony > VoIP Calls** は、ダイアログを 1 つずつ別の通話として並べます。

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Security-Client \|\| sip.Security-Server \|\| sip.Security-Verify` | sec-agree のメッセージ。 |
| `sip.WWW-Authenticate contains "AKAv1-MD5"` | IMS-AKA の challenge。`contains "ck="` で、S-CSCF が P-CSCF に送るものが見つかる。 |
| `udp.port == 5064 \|\| tcp.port == 5064` | P-CSCF の保護サーバーポートを通るもの。 |
| `diameter.3GPP-SIP-Authentication-Scheme == "Digest-AKAv1-MD5"` | IMS-AKA の MAR と MAA。 |
| `diameter.cmd.code in {265, 258, 274, 275}` | Rx。AAR と AAA、RAR と RAA、ASR と ASA、STR と STA。 |
| `diameter.Flow-Usage == 2` | 信号の経路の AAR。 |
| `diameter.Codec-Data` | 通話のメディアの AAR。 |
| `diameter.Experimental-Result-Code == 5065` | IP-CAN_SESSION_NOT_AVAILABLE。attach していない UE。 |
| `diameter.Abort-Cause` | PCRF の ASR。 |
| `http && ip.addr == 172.28.0.80` | EPC スタブの API。`frame contains "EPS BEARER CONTEXT"` で bearer のメッセージが見つかる。 |
| `sip.Require contains "precondition"` | プリコンディションの 183。`sdp.media_attr contains "qos"` でプリコンディションの行がすべて見つかる。 |
| `amr` | AMR と AMR-WB のフレーム。`amr.wb.cmr` と `amr.wb.toc.ft` は AMR-WB の CMR とフレームの種類。 |
| `rtcp`、`rtpevent` | RTCP と telephone-event。 |
| `sip.Session-Expires`、`sip.Reason` | セッションタイマーと、BYE や CANCEL の理由。 |

`scripts/e2e-volte.sh` は、この表のフィルタをすべて、このシナリオのキャプチャに当てます。フレームが 1 つも見つからないフィルタがあれば、試験は失敗します。

## コードの場所

| パッケージかファイル | 中身 |
| --- | --- |
| `internal/accessapi` | アクセス網のスタブ（EPC と 5GC）が、UE の側で共通に使う部分。JSON のリクエスト、UE へのイベントの流れ、UE が使うクライアント。 |
| `internal/epc` | EPC スタブと PCRF。網が始める手順の経路（`http.go`）、メッセージ（`api.go`）、PDN 接続と bearer（`network.go`）、Rx（`pcrf.go`）。 |
| `internal/ue/access.go`、`access_epc.go` | UE の `AccessNetwork` と、スタブの API のクライアント。 |
| `internal/milenage`、`internal/aka` | MILENAGE（TS 35.206）、認証ベクトル、USIM の確認、再同期、RFC 3310 の Digest。 |
| `internal/hss`、`internal/cx` | 加入者の AKA の情報、MAR と MAA。 |
| `internal/scscf/aka.go` | S-CSCF の IMS-AKA の challenge と確認。 |
| `internal/pcscf/secagree.go` | P-CSCF の sec-agree と SA の表。 |
| `internal/ue/aka.go` | UE の USIM と、UE 側の sec-agree。 |
| `internal/policy`、`internal/rx` | P-CSCF のポリシー制御の境界、TS 29.213 の SDP から Rx への対応、Rx のメッセージとクライアント。 |
| `internal/pcscf/policy.go`、`setup.go`、`session.go`、`release.go`、`dialog.go` | AF としての P-CSCF。信号の経路、通話の AAR、STR、保存したダイアログからの網による解放。 |
| `internal/precondition` | RFC 3312 のプリコンディションの表と、その SDP の属性。 |
| `internal/sdp` | VoLTE の offer と answer。複数のコーデック、fmtp、メディアごとの b= の行。 |
| `internal/amr`、`internal/rtcp`、`internal/rtp` | RFC 4867 のペイロード形式と格納形式、RTCP、telephone-event。 |
| `internal/ue/session.go`、`call.go`、`media.go` | UE の VoLTE の通話。offer と answer、プリコンディション、保留、DTMF、AMR、RTCP。 |
| `internal/sip` | UPDATE、re-INVITE、セッションタイマー、199、Reason と、MMTel AS が使う B2BUA の部品。 |
| `internal/lab/epc.go`、`causes.go` | ラボによるスタブのパスの取り次ぎと、Rx のセッションと通話との結びつけ。 |
| `internal/ue/volte_epc_test.go`、`volte_aka_test.go` | 1 つのプロセスの中で IMS 全体を通る VoLTE の通話。 |

## このプロジェクトで簡略化していること

各項目には、コードに `SIMPLIFIED:` のコメントがあります。`grep -rn "SIMPLIFIED:" internal cmd` ですべてを一覧できます。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| EPC スタブ、TS 24.301、TS 36.413 | NAS の符号化、S1-AP、無線が無い。メッセージは HTTP と JSON で、URL の IMSI が S1 の接続の代わりになる。セキュリティ、GUTI、トラッキングエリアの更新、網の側のタイマーも無い。 | 学習者が読むのは手順で、符号化ではない。 |
| EPC スタブ、TS 36.331 | システム情報は HTTP のリソースにした。 | それを報知する無線が無い。 |
| EPC スタブ、TS 23.401 5.3.1.1 | アドレスは加入者ごとの静的なもので、プールも GTP-U も SGi も無い。 | コンテナがそのアドレスを持っているので、パケットにトンネルは要らない。 |
| EPC スタブ、TS 23.401 5.3.2.1 | attach に認証、NAS のセキュリティ、S6a が無く、ATTACH REJECT も送らない。 | スタブは設定にある IMSI だけを扱う。 |
| EPC スタブ、TS 29.212 | Gx が無い。PCRF と PDN GW は `Store` の中の PDN 接続を共有する。 | スタブは両方を受け持つ 1 つのノードなので、2 つは同じ `Store` を使い、あいだに網が無いため。 |
| EPC スタブ、ITU-T X.731 8.1.1.3 | `hss` と `epc` の役割を持つノードは、両方の役割で 1 つの Diameter の待ち受けを使う。HSS を運用から外すと、Rx で PCRF につなぐ P-CSCF も断られ、HSS が接続を閉じるときには、その Rx の接続も閉じる。`hss` と `sc` の役割を持つノードも、同じように待ち受けを共有する。 | HSS の管理状態は、HSS のサービスだけを止めるべきもの。ノードは、相手がつなぐすべての役割のために、基本プロトコルの 1 つのポートで待ち受け、ドレインはその待ち受けに働くため。このプロジェクトの構成では、これらの役割はどれも別々のノードで動く。 |
| EPC スタブ、TS 24.301 6.4.3、6.4.4.2 | 規則が変わっても MODIFY EPS BEARER CONTEXT REQUEST を送らない。bearer は解放を送った時点で忘れ、T3495 も動かさない。 | スタブは QoS を強制せず、UE は bearer があることを知れば足りる。 |
| PCRF、TS 29.213 6.3 | 許可するのは音声だけで、QCI は 1。保証する速度は最大の速度と同じで、RTCP の分は別にしない。 | この網の通話は音声のストリームが 1 本だけ。 |
| PCRF、TS 29.214 4.4.2、5.3.16 | どの AAR もサービスの情報をすべて含むとみなす。 | P-CSCF は毎回 SDP 全体から情報を作る。 |
| PCRF、TS 24.008 10.5.6.12 | アドレスのマスクに対応しない。マスクのある Flow-Description は FILTER_RESTRICTIONS で断る。 | スタブの TFT では、パケットフィルタが相手のアドレスをマスク無しで持つ。また、P-CSCF はホストのアドレスしか書かない。 |
| P-CSCF、TS 29.213 7.3.7 | DRA を使わず、設定した 1 つの PCRF に送る。 | PCRF は EPC スタブのもので、スタブは 1 つのノードとして動き、すべての UE の IP-CAN セッションを持つため。 |
| P-CSCF、RFC 6733 8.5.2 と 7.1.5 | Rx のクライアントは、P-CSCF が知らないセッションの RAR と ASR にも DIAMETER_SUCCESS で答える。本来の答えは DIAMETER_UNKNOWN_SESSION_ID。そうした ASR のあとも STR を送る。P-CSCF はその知らせのダイアログを見つけられず、ログに残すだけ。 | クライアントは自分の状態を持たず、Session-Id は P-CSCF がダイアログと一緒に持つものを使うため。EPC のスタブの PCRF は、RAA の結果をログに残すだけで、ASA は何も読まない。 |
| P-CSCF、TS 29.213 Table 6.2.1、6.2.2 | TCP のメディア、b=TIAS に対応しない。ストリームが片方向になったときは、新しい向きのフローだけを残す。 | この網の通話はどれも使わない。 |
| P-CSCF、TS 29.214 A.1 | AF-Application-Identifier を送るのは着信側の P-CSCF だけ。 | 発信側は Feature-Caps から取るが、それを書くノードが無い。 |
| P-CSCF、TS 29.214 4.4.2 | サービスの情報の変更を断られても、ログに書くだけ。 | そのときには相手がもう答えている。TS 24.229 が断られた資源を扱うのは最初の INVITE だけ。 |
| P-CSCF、TS 29.214 A.3 | 分岐した INVITE の早期ダイアログを区別しない。 | S-CSCF は分岐しない。 |
| P-CSCF、TS 24.229 5.2.8.1.1、5.2.8.1.2 | メディアを取り除く SIP のメッセージを待たずに、すぐに解放する。信号を失った UE のほかのセッションは、それぞれの事象に任せる。 | 通話ごとに音声のストリームは 1 本で、P-CSCF は UE ごとのダイアログの索引を持たない。 |
| P-CSCF、TS 24.229 5.2.8.1.2 | 確立した通話が資源を失ったとき、bearer を失ったことに served user の UE が自分で気づくのを待たない。この節は相手側にだけ BYE を送るが、P-CSCF は相手側の名前で UE にも BYE を送る。 | 網が音声の bearer だけを解放したあとも、UE には信号の経路が残っているため。bearer を失ったことに対処しない UE は、BYE が来なければ、網が終えた通話にとどまってしまう。 |
| P-CSCF、TS 24.229 5.2.8.1.4、5.2.8.3 | P-CSCF を通る BYE なしに終わったダイアログは `Store` に残る。応答済みのものは `ims_answered_sessions` にも残る。 | 利用者ごとのダイアログの索引か、P-CSCF のセッションタイマーが要る。 |
| P-CSCF、RFC 3264、RFC 3262 | SDP の無い INVITE と re-INVITE には、中継のコンテキストも許可も無い。 | UE シミュレータはいつも INVITE で offer する。 |
| P-CSCF と UE、TS 33.203 7.1、7.2 | 既定では IPsec の ESP をかけない。アソシエーションは取り決めて保存し、保護サーバーポートを使い、クライアントポートは使わない。`deploy/esp` の構成では ESP をかけ、このポートも開く。アソシエーションは、それを作った 1 台の P-CSCF のカーネルにあり、共有した Store の SA の表には付いてこない。 | どちらでもキャプチャを読めるようにするため。保護しない IMS-AKA は標準に無い。カーネルが持てるのは、その 1 台のアソシエーションだけ。 |
| P-CSCF と UE、TS 33.203 7.4.0 | ESP をかけるとき、保護クライアントポートはどちらの端も設定に書いた 1 つのポートで、UE が変わっても認証をやり直しても同じ。標準は、認証を伴う再登録のたびに変えることを求めている。 | アソシエーションの組ごとに別のポートにすると、組ごとにソケットが要るため。UE が違えば、アソシエーションはアドレスで区別できる。 |
| P-CSCF、TS 24.229 5.2.2.3 | UE は要求が届いたアドレスとポートで見分ける。TCP の要求と、ESP をかけたときのすべての要求は、Via の sent-by で見分ける。 | ESP が無いと、TCP の接続はそのときどきのポートから来て、その sent-by を認証するものも無い。ESP をかけると、すべての要求が保護クライアントポートから来る。このポートはどの記録の鍵でもなく、送り手はセキュリティアソシエーションで認証される。 |
| S-CSCF、TS 33.203 6.1.1、TS 24.229 5.4.1.2.1A | AKAv1-MD5 だけで、AKAv2-SHA-256 は無い。 | SIPp が AKAv1-MD5 しか話さない。 |
| AKA、TS 33.102 C.2.2、C.3.4 | 受け入れる SQN の古さの限度 L を使わず、IND を領域ごとに分けない。 | Profile 2 は L の値を示していない。IMS は 1 つの領域で、MAR ごとにベクトルは 1 つ。 |
| UE、TS 24.301、TS 36.331 | UE のパケットは Docker のネットワークにそのまま出る。 | 無線もトンネルも無い。 |
| UE、TS 24.229 7.2A.4.3 | アクセスの種類はいつも `3GPP-E-UTRAN-FDD`。 | スタブはセルの複信方式を伝えない。 |
| UE、TS 24.229 L.2.2.5.1B | 音声の bearer を、TFT と照らし合わせずにすべての通話の資源とみなす。 | IR.92 4.4.2 では、1 本の音声の bearer がすべての通話の音声を運ぶ。 |
| UE、TS 24.229 L.2.2.1 | UE は PCO で P-CSCF のアドレスの一覧を受け取るが、使うのは先頭のアドレスだけ。TS 24.229 5.1.1.2.1 と 5.1.2A.1.6 が一覧のほかの P-CSCF を使わせる登録の失敗でも、UE は同じ P-CSCF を通って登録し直す。 | このプロジェクトのどの構成でも、EPC のスタブが渡すアドレスは 1 つのため。 |
| UE、TS 24.301 5.5.2.3.4、IR.92 2.4.2.1 | 網が「re-attach not required」で EMM cause を付けずに detach したあと、T3402（既定は 12 分）が切れてから attach をやり直し（TS 24.301 5.2.2.3.3）、登録し直すこと。アクセス網のイベントストリームが途切れたあと、PDN 接続を張り直すこと。UE シミュレータはエラーで止まり、IMS に届かなくなった登録の解除も送らない。 | シミュレータの 1 回の実行が扱う attach は 1 回だけで、SIP のトランスポート、Contact、通話のメディアの資源を、その attach の PDN アドレスに合わせて一度だけ用意するため。 |
| UE、TS 24.229 6.1.1 | answer の前に bearer を失っても、プリコンディションの表を変えるだけで、通話は網が終わらせる。 | 解放は P-CSCF が行う（5.2.8.1.1）。 |
| UE、TS 24.229 5.1.3.1、5.1.4A.1 | ダイアログの中のすべての要求の Supported に precondition を入れる。 | すべての通話に 1 つの Supported を使う。プリコンディションを使わない相手は無視する。 |
| UE、IR.92 3.2.1、RFC 4867 8.3.1 | フレームは符号化したときのモードのまま送り、CMR と mode-set に従わない。 | UE は符号化できない。 |
| UE、IR.92 2.6 | User-Agent は IR.92 の例に従い、GSMA RCC.07 には従っていない。 | RCC.07 は読んでいない。 |
| UE、TS 24.229 9.2.1、5.1.2A.1.1 | `-epc` の無い UE は、P-CSCF のアドレスを設定から取る。P-CSCF の URI はいつも設定から取る。 | ソフトフォンの経路を残すため。P-CSCF は自分の Route をホスト名で見分ける。 |
| UE、RFC 3264 6.1、RFC 3550 8.2 | UE は相手の SDP のペイロードタイプの番号で送り、新しい SSRC が来ると統計をやり直す。 | この網の UA はどれも offer の番号で答え、相手は唯一の送信元。 |
| AMR、RFC 4867 4.4 | フレームの CRC、robust sorting、インタリーブ、複数のチャネルに対応しない。 | IR.92 はどれも offer しない。 |
| AMR、IR.92 3.2.5 | 最初に受けたフレームより前のフレームは捨てる。 | それが届くのは、ストリームの始めの順序が入れ替わったときだけ。 |
| RTCP、RFC 3550 6.2、6.3.4 から 6.3.7、A.1 | 短い最小間隔、reconsideration、新しい送信元の様子見をしない。 | 通話のメンバーはちょうど 2 つで、変わらない。 |
| DTMF、RFC 4733 2.5.1.3 | 65535 タイムスタンプ単位より長い数字は断る。 | UE が送るのは短い数字だけ。 |
| 中継ノード、RFC 3605、RFC 5761、RFC 4961 | RTCP は RTP の 1 つ上のポートにあるとみなし、UE 側のターミネーションは、制御する側がラッチし直しを求めない限り、ラッチしたアドレスを通話のあいだ変えない。 | この網の UE は a=rtcp も rtcp-mux も送らず、通話中に移動しない。 |
| プリコンディション、RFC 4032 4.1、4.2 | ストリームが新しいアドレスに移ったことを見つけない。 | UE は通話のあいだアドレスを変えない。 |
| SDP、RFC 8866 5.7 | c= には IPv4 のユニキャストのアドレスだけを受け付ける。 | この網のノードはどれも IPv4 を使う。 |
| SDP、TS 26.114 5.2.1.1 | NR のアクセスで使う offer（`sdp.VoNRCodecs`）に EVS を入れない。そのため超広帯域の音声も offer しない。LTE と同じ offer になる。 | UE には EVS のコーデックが無い。EVS のペイロードタイプを offer すると、UE が送れない EVS Primary のビットレートを示すことになる。 |
| UA、RFC 3261 12.2.1.2 | 早期ダイアログを失っても、PRACK が失敗しても、ログに書くだけ。 | INVITE への最終応答がどのみち通話を終わらせる。 |
| UA、RFC 3261 13.2.2.4 | 2xx に offer があったダイアログを BYE で終えるときは、answer の無い ACK を先に送る。 | そのとき UA は自分の SDP を持っていない。 |
| UA、RFC 4028 7.1、10 | timer を Supported に入れるのは INVITE とそのダイアログの要求だけ。更新に失敗してもログに書くだけで、期限切れでセッションが終わる。 | セッションタイマーは通話のダイアログのもので、ダイアログの中の要求に認証を求めるノードは無い。 |

## 後片付け

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。
