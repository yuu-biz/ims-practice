---
title: VoLTE の呼の流れ
footer: ims-practice 勉強会 9. VoLTE
lang: ja
---

# VoLTE の呼の流れ

ims-practice 勉強会 第 9 回（アクセス網、IMS-AKA、Rx でのポリシー制御、VoLTE の通話）

---

[layout: summary]

## この回で分かること

最初の問い。携帯電話から発信すると、相手の電話が鳴る前に、網は何を用意しているでしょうか。

- VoLTE の端末が、SIP Digest で登録するソフトフォンとどこで違うか。
- UE が EPC に attach し、P-CSCF のアドレスを受け取るしくみ。
- IMS-AKA で、UE と網が互いを確かめるしくみ。
- ISIM の無い端末が IMSI から作る識別子と、そのうち網が規制するもの。
- P-CSCF が Rx で PCRF に音声の資源を求め、網が bearer を作るしくみ。
- 資源がそろうまで着信側を鳴らさない、プリコンディションのしくみ。
- 網が bearer を失ったときに、P-CSCF が通話を終わらせるしくみ。

> 手順書 [VoLTE の呼の流れ](../../../docs/guide/ja/volte.md)（`docs/guide/ja/volte.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.229 V20.1.0、TS 29.214 V20.0.0、TS 24.301 V20.1.0、TS 33.203、TS 33.102、TS 35.206、TS 35.208、RFC 3310、RFC 3329、RFC 3312、RFC 4867、GSMA IR.92 v24.0 などです。

---

## ソフトフォンの通話から変わること

| | ソフトフォン（SIP Digest、PCMU） | VoLTE の端末 |
| --- | --- | --- |
| アクセス網 | 無い。P-CSCF は設定で与える | EPC に attach する。P-CSCF は網が教える |
| 認証 | SIP Digest とパスワード | IMS-AKA と sec-agree。パスワードは無い |
| 資源 | 考えない | P-CSCF が Rx で PCRF に求める |
| 呼び出し | 183 のあとすぐ | 両側の bearer がそろってから |
| 音声 | PCMU の RTP | AMR-WB、RTCP、telephone-event |
| 切断 | UE の BYE だけ | 網が bearer を失うと P-CSCF が切る |
| 識別子 | 設定で与え、登録したものを使う | IMSI から作る。登録したものは規制される |

- すべて左の列のままだと何が困るでしょうか。音声の通り道ができる前に着信側が鳴り、出ても何も聞こえません。
- ソフトフォンはそのまま動きます。EPC スタブを通らない UE は PCRF が知らないので、P-CSCF はポリシー制御なしで扱います。

> 手順書 [「VoLTE の端末とソフトフォンの違い」](../../../docs/guide/ja/volte.md#volte-の端末とソフトフォンの違い)。IR.92 4.4、2.4.1、2.4.2.3、3.2.1。TS 33.203 6.1.1、7。TS 29.214 4.4.1。TS 24.229 5.2.8.1.2。RFC 3312。

---

## 登場するノード

| ノード | アドレス | VoLTE の通話での役割 |
| --- | --- | --- |
| volte10 | 172.28.0.120 | 発信側。`tel:+12025550110`。加入者 10。 |
| volte12 | 172.28.0.121 | 着信側。`tel:+12025550112`。加入者 12。 |
| ue2、SIPp | 172.28.0.101、102 | PCMU だけのソフトフォンと、IMS-AKA の試験相手。 |
| EPC スタブと PCRF | 172.28.0.80 | 役割 `epc`。UE の API は HTTP の 8080、Rx は 3868。 |
| P-CSCF | 172.28.0.40 | 5060 番と保護サーバーポート 5064 番。Rx のクライアント。 |
| S-CSCF、HSS | 172.28.0.30、10 | IMS-AKA の challenge と、認証ベクトル。 |
| MMTel AS、中継ノード | 172.28.0.75、50 | MMTel AS の回と同じ。中継は RTCP も通す。 |

- volte10 と volte12 は `compose.yaml` のプロファイル `volte` のサービスです。鍵は `deploy/compose/hss.yaml` に、加入者ごとのコンテナのアドレスは `deploy/compose/epc.yaml` にあります。
- ラボ（172.28.0.60）は volte10 と volte12 を動かし、シーケンス図に Rx を出し、網の手順を始める EPC スタブのパスをホストへ中継します。

> 手順書 [「登場するノード」](../../../docs/guide/ja/volte.md#登場するノード)

---

## EPC スタブが残すものと省くもの

```text
 UE ==(HTTP + JSON: ATTACH REQUEST, ...)==> EPC stub: PDN GW and PCRF (epc role)
 |                                           ^
 |                                           |  Rx (Diameter)
 +------------ SIP ------------> P-CSCF -----+
```

- メッセージの名前と項目は TS 24.301（8.2、8.3）の ESM と EMM のとおりです。URL では小文字です。例は `POST /ues/001010000000010/attach-request` です。
- 網から送るものは、UE のイベントストリーム `GET /ues/<IMSI>/events` に Server-Sent Events で届きます。
- アドレスは加入者ごとに静的です（TS 23.401 5.3.1.1）。トンネルは無く、パケットはそのまま届きます。
- 無線、S1、NAS の符号化とセキュリティ、Gx はありません。PCRF と PDN GW は、`epc` の役割の中で状態を共有します。bearer は記録だけで、QoS の制御もパケットのフィルタもしません。
- 本物の Diameter は Rx だけです。学習者が Wireshark で読むのは IMS の側なので、そこを残しました。NAS のメッセージの名前を使うので、無線も NAS のデコーダも無しに attach を読めます。

> 手順書 [「スタブが省くこと」](../../../docs/guide/ja/volte.md#スタブが省くこと)

---

## キャプチャを用意する

- `scripts/` にある VoLTE の通話の e2e のスクリプトが、この回の手順をすべて行います。SIPp の登録、VoLTE の通話と ue2 への通話、ラボからの DTMF と保留、網による bearer の解放を行い、キャプチャのメッセージを 1 つずつ確かめます。スクリプトの名前は手順書の [「動かしてみる」](../../../docs/guide/ja/volte.md#動かしてみる) にあります。
- 手順書のフレーム番号は、このスクリプトを 1 回動かしたときのものです。手元の番号とは違います。
- キャプチャは `captures/`、フレームの一覧は `e2e-out/`、AMR-WB の録音は `captures/volte-*.amr` に残ります。2 分ほどかかります。

この回の全体の表示フィルタです。

```text
(sip && !(sip.CSeq.method in {"SUBSCRIBE", "NOTIFY", "MESSAGE"})) || diameter.cmd.code in {265, 274, 275, 303} || (http && ip.addr == 172.28.0.80)
```

- 登録、通話、Rx（265 は AAR と AAA、274 は ASR と ASA、275 は STR と STA）、MAR（303）、EPC スタブの API が残ります。

> 手順書 [「動かしてみる」](../../../docs/guide/ja/volte.md#動かしてみる) と [「キャプチャを開く」](../../../docs/guide/ja/volte.md#キャプチャを開く)

---

## attach と P-CSCF の発見

```ladder
volte10, EPC スタブ
volte10 -> EPC スタブ: GET /system-information // セル。MCC 001、MNC 01、TAC 1
volte10 -> EPC スタブ: GET /ues/001010000000010/events // イベントストリームは開いたまま
volte10 -> EPC スタブ: POST /ues/001010000000010/attach-request // ATTACH REQUEST、APN ims
volte10 <-- EPC スタブ: 200、ATTACH ACCEPT と ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST
volte10 -> EPC スタブ: POST /ues/001010000000010/attach-complete
```

| ATTACH ACCEPT の中 | 見どころ |
| --- | --- |
| `eps_bearer_identity` 5、`qci` 5 | IMS の信号のための default bearer。 |
| `pdn_address` | 加入者の静的なアドレス 172.28.0.120。 |
| PCO の `000CH` のコンテナ | P-CSCF のアドレス 172.28.0.40。ポートは 5060。 |

> 手順書 [「attach」](../../../docs/guide/ja/volte.md#attach)。IR.92 4.4.1、4.5。TS 23.203 6.1.7。TS 24.008 10.5.6.3。TS 24.229 L.2.2.1、7.2A.4.3、L.3.1.1。

---

## attach のほかの見どころ

- フィルタは `http && ip.addr == 172.28.0.80` です。HTTP のボディの JSON を開きます。
- PCO で P-CSCF を伝えるのは TS 24.229 L.2.2.1 の方法 II で、IR.92 4.5 が求める方法です。一覧の最初のアドレスが優先です。
- ポートは標準の 5060 です。Wireshark が Decode As なしで SIP をデコードできるようにするためです。
- UE は、PDN のアドレスが P-CSCF に届くアドレスと同じかを確かめ、違えば止まります。
- UE はセルを P-Access-Network-Info に書きます。`3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101` には、MCC 001、MNC 01、16 進 4 桁の TAC、7 桁の ECI が並んでいます。

---

## UE とアクセス網の境界をコードで見る

[internal/ue/access.go](../../../internal/ue/access.go)

```go
type AccessNetwork interface {
	// Attach connects the UE to the IMS APN and returns what the network
	// gave it (TS 24.229 L.2.2.1).
	Attach(ctx context.Context) (Attachment, error)
	// Events delivers what the network tells the UE after the attach. It
	// is closed once the UE has detached or the network has detached it.
	Events() <-chan AccessEvent
	// Detach disconnects the UE from the network.
	Detach(ctx context.Context) error
}
```

- 実装は 1 つで、EPC スタブへの HTTP のクライアントです（`access_epc.go`）。
- イベントは bearer の活性化、解放、網からの detach の 3 つです。
- UE はこの 3 つのメソッドを通してしかアクセス網を見ません。だから UE のほかの部分を変えずに、同じインタフェースの後ろに NAS の実装を置けます。

---

## IMS-AKA の鍵

| 持つもの | 持っている値 | 書いてある場所 |
| --- | --- | --- |
| volte10 の USIM | K、OPc、SQN_MS | `ue` の `-k`、`-opc`、`-sqn`（`compose.yaml`） |
| HSS | 同じ加入者の K、OPc、AMF、SQN_HE | `deploy/compose/hss.yaml` の加入者 10 の `aka:` |

- K と OPc は MILENAGE の試験データ 1 の値です（TS 35.208 4.3.1）。
- OPc は事業者の OP と K から作ります（TS 35.206 4.1）。SIPp のように OP を渡すときは `-op` を使います。
- 共有するのはこれだけです。ほかの値は、網と UE がそれぞれ、この鍵とランダムな challenge から計算します。SIP Digest で登録する加入者と違い、パスワードはどこにもありません。
- volte12 は試験データ 3（4.3.3）の K と OPc を持ちます。SIPp 3.7 は K と OP を文字列のバイトとして読むので、SIPp 用の加入者 11 の鍵は印字できる文字です。手順書の「SIPp」の節にあります。

> 手順書 [「鍵」](../../../docs/guide/ja/volte.md#鍵)

---

## ISIM の無い端末の識別子

| 識別子 | 値 | 現れる場所 |
| --- | --- | --- |
| 秘密の識別子（private user identity） | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | Authorization の `username` と、Cx の User-Name。 |
| 一時的な公開の識別子（temporary public user identity） | `sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | REGISTER の From と To、Cx の Public-Identity。ほかには現れない。 |
| 既定の公開の識別子（default public user identity） | `sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org` | 200 OK の最初の P-Associated-URI。そのあとの From と P-Asserted-Identity。 |
| 番号の tel URI | `tel:+12025550110` | 2 つ目の P-Associated-URI。 |

- volte10 と volte12 のカードには USIM があり、ISIM はありません。USIM には IMSI と鍵があっても、IMS の識別子はありません。そこで端末は IMSI（ここでは 001010000000010）から識別子を作ります。
- 一時的な識別子には IMSI が入っているので、HSS はこれを規制します。登録にだけ使われ、見えるのは CSCF と HSS だけです。`deploy/compose/hss.yaml` では `barred_identities` に挙げてあり、SAA のユーザープロファイルでは `<BarringIndication>` が付きます。

> 手順書 [「ISIM の無い端末の識別子」](../../../docs/guide/ja/volte.md#isim-の無い端末の識別子)。TS 24.229 5.1.1.1A。TS 23.003 13.2、13.3、13.4B。TS 23.228 E.3.1。TS 29.228 B.2.1。

---

## 識別子をキャプチャで見る

```text
REGISTER  volte10 -> P-CSCF  To: <sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org>
200 OK    P-CSCF -> volte10  P-Associated-URI: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
                             P-Associated-URI: <tel:+12025550110>
INVITE    volte10 -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
INVITE    P-CSCF -> S-CSCF   P-Asserted-Identity: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
```

- S-CSCF は、P-Associated-URI に規制されていない識別子だけを、既定の識別子を先頭にして並べます。UE は最初のものを既定の公開の識別子として覚えます。登録した識別子は一覧に無いので、規制されたものとして扱います。
- UE は P-Preferred-Identity を送らないので、P-CSCF は既定の識別子を P-Asserted-Identity に入れます。規制された識別子からのリクエストには 403、その識別子あてには 404 が返ります。
- `sip && !(sip.CSeq.method == "REGISTER") && frame contains "sip:001010000000010@ims"` に合うパケットはありません。Contact にも IMSI は入らず、ユーザー部は UUID です。ソフトフォンには SIM がありません。識別子は IMC に入っているのと同じように設定で与えられ、登録したものをそのまま使います。

> 手順書 [「ISIM の無い端末の識別子」](../../../docs/guide/ja/volte.md#isim-の無い端末の識別子)。TS 24.229 5.4.1.2.2F、5.1.1.2.1、5.2.6.3.1、5.4.3.2、5.4.3.3、5.1.1.1B.1。

---

## IMS-AKA の最初の REGISTER と 401

| # | 区間 | 見どころ |
| --- | --- | --- |
| 698 | UE:5060 → P-CSCF:5060 | `Security-Client`、`Require: sec-agree`。空の nonce と response。 |
| 709 | P-CSCF → I-CSCF | Security-Client を外して保存。`integrity-protected="no"`。 |
| 724 | S-CSCF → HSS（MAR） | 方式 `Digest-AKAv1-MD5` の項目を 1 つ求める。 |
| 726 | HSS → S-CSCF（MAA） | RAND と AUTN、XRES、CK、IK。 |
| 727 | S-CSCF → I-CSCF（401） | nonce は RAND と AUTN の base64。`ik` と `ck`。 |
| 737 | P-CSCF → UE（401） | `ik` と `ck` を外し、`Security-Server` を足す。 |

> 手順書 [「シーケンス図」](../../../docs/guide/ja/volte.md#シーケンス図) の 1 から 6。TS 24.229 5.1.1.2.2、5.2.2.1、5.2.2.2、5.4.1.1、5.4.1.2.1A。RFC 3329 2.3.1。TS 33.203 Annex H、7.2。TS 29.229 6.3.9。TS 33.102 6.3.2。TS 29.228 6.3。RFC 3310 3.2。

---

## IMS-AKA で網を確かめて答える

| # | 区間 | 見どころ |
| --- | --- | --- |
| 740 | UE | AUTN の MAC と SQN を確かめ、RES、CK、IK を計算する。 |
| 740 | UE:5062 → P-CSCF:5064 | `Security-Verify` に Security-Server を写す。RES で計算した response。 |
| 749 | P-CSCF → I-CSCF | 取り決めを確かめ、`integrity-protected="yes"`。 |
| 760、764 | S-CSCF、SAR | XRES で Digest を計算して比べる。一致したので登録する。 |
| 783 | P-CSCF:5064 → UE:5062 | 200 OK。アソシエーションが確立する。 |
| 786 | P-CSCF → PCRF | AAR。信号の経路（Rx の節）。 |

> 手順書 [「シーケンス図」](../../../docs/guide/ja/volte.md#シーケンス図) の 7 から 12。TS 33.102 6.3.3、C.2。TS 33.203 7.1。RFC 3329。RFC 3310 3.3。TS 24.229 5.4.1.2.2、5.2.2.2。TS 29.214 A.4。

---

## IMS-AKA の前半と後半で見えること

- 保護されていない 5060 番に UE が送る REGISTER は、最初の 1 回だけです。200 OK のあと、UE はすべての要求を P-CSCF の保護ポート 5064 に送り、P-CSCF は UE の 5062 に送ります。
- P-CSCF は 401 から `ik` と `ck` を取り除くので、鍵は UE まで届きません。UE は自分の K と RAND から同じ鍵を計算します。
- AUTN の MAC が合えば、網が K を知っていることが分かります。網の側も認証されるわけで、SIP Digest には無かった向きです。UE は SQN が新しいことも確かめます。
- Security-Verify で、P-CSCF は、途中の誰かが強い方式の申し出を消していないかを確かめます。200 OK と #786 の AAR は、どちらが先に P-CSCF を出ることもあります。
- 再登録と登録の解除は、アソシエーションの上で、新しい challenge なしに行います（TS 24.229 5.1.1.4.2、5.1.1.6.2）。
- フィルタ。`sip.Security-Client || sip.Security-Server || sip.Security-Verify`、`sip.WWW-Authenticate contains "AKAv1-MD5"`（`contains "ck="` にすると、S-CSCF からの 401 だけが残ります）、保護ポートは `udp.port == 5064 || tcp.port == 5064` です。

---

## 認証ベクトルをコードで見る

[internal/aka/aka.go](../../../internal/aka/aka.go)

```go
func NewAuthenticationVector(secret Secret, rand RAND, sqn SQN, amf AMF) AuthenticationVector {
	m := secret.milenage()
	mac := m.F1(rand, sqn.octets(), amf)
	res := m.F2(rand)
	return AuthenticationVector{
		RAND: rand,
		XRES: res[:],
		CK:   m.F3(rand),
		IK:   m.F4(rand),
		AUTN: newAUTN(conceal(sqn, m.F5(rand)), amf, mac),
	}
}
```

```sh
go test ./internal/aka -run TestAuthenticationVectorFromTestSet1 -v
```

- f1 から f5 は MILENAGE の関数です（TS 35.206）。AUTN は SQN ⊕ AK（6 オクテット）、AMF（2 オクテット）、MAC-A（8 オクテット）です。
- キャプチャの RAND はランダムなので、出力は TS 35.208 の値になりません。試験データは計算そのものを確かめるものです。`go test ./internal/milenage -run TestConformanceTestSets -v` で 4.3 の 20 組をすべて確かめます。

> 手順書 [「認証ベクトルを手で確かめる」](../../../docs/guide/ja/volte.md#認証ベクトルを手で確かめる)。TS 33.102 6.3.2。TS 35.208 4.3.1。

---

## ESP を使わない sec-agree

- 標準では、401 のあとのメッセージをすべて IPsec の ESP で保護します（TS 33.203 7.1）。
- このプロジェクトは、アソシエーションを取り決めて保存し、保護ポートも使います。ESP だけをかけません（`ealg=null`）。キャプチャを読めるようにするためです。
- 保護しない IMS-AKA は標準にありません。これは独自の簡略化です。IPsec を使わない形は、TLS の上の AKAv2-SHA-256 だけです（TS 24.229 4.2B.1）。

| 本物の網と違う点 | 理由 |
| --- | --- |
| 保護クライアントポート（`port-c`）を使わない | どちらも保護サーバーポートから送る |
| TCP の要求は Via の sent-by で UE を見分ける | INVITE と、1300 バイトを超える REGISTER は TCP になる（RFC 3261 18.1.1） |

- ESP が無いので、VoLTE の UE の SIP もソフトフォンと同じように Wireshark で読めます。鍵を読み込む必要もなく、コンテナに NET_ADMIN も要りません。アソシエーションは IK と CK と一緒に P-CSCF の SA の表に入ります。
- AKAv1-MD5 だけなのは、S-CSCF を確かめる独立した相手の SIPp がそれしか話さないからです。SIPp は RES と Digest を別の人が書いたコードで計算します。

> 手順書 [「ESP を使わない sec-agree」](../../../docs/guide/ja/volte.md#esp-を使わない-sec-agree)

---

# Rx でのポリシー制御と VoLTE の通話

P-CSCF はポリシーと課金の制御の AF（application function）です。アクセス網が何を運ぶかは PCRF が決めます（TS 29.214 4）。

---

## 信号の経路の AAR（#786）

| AVP | 値 |
| --- | --- |
| Media-Component-Number、Flow-Number | 0、0 |
| `Flow-Usage` | AF_SIGNALLING |
| `Specific-Action` | 2 は INDICATION_OF_LOSS_OF_BEARER、4 は INDICATION_OF_RELEASE_OF_BEARER |
| `Framed-IP-Address` | 172.28.0.120。これで IP-CAN セッションを見つける |

- 登録の直後の AAR です。attach していない UE（ue2、pjsua）には、PCRF が 5065（IP-CAN_SESSION_NOT_AVAILABLE）で答え、P-CSCF はその UE をポリシー制御なしで扱います。前の回までのキャプチャでも、ソフトフォンが登録するたびにこの組があります。
- Rx のセッションは、それぞれ別の Diameter のセッションです。信号の経路には登録した UE ごとに 1 つ、メディアには通話と側ごとに 1 つです。

フィルタは `diameter.Flow-Usage == 2` と `diameter.Experimental-Result-Code == 5065` です。

> 手順書 [「信号の経路」](../../../docs/guide/ja/volte.md#信号の経路)。TS 29.214 4.4.5、A.4。

---

## 通話のメディアの AAR

両側の P-CSCF は、SDP の answer がそろうと AAR を送ります。着信側は volte12 からの 183、発信側は volte10 に向かう 183 がきっかけです。

| AVP | 着信側の AAR（#1036）の値 |
| --- | --- |
| `AF-Application-Identifier` | `urn:urn-7:3gpp-service.ims.icsi.mmtel` |
| `Media-Sub-Component` | フロー 1 と 2。RTP と RTCP の `Flow-Description` |
| `Max-Requested-Bandwidth-UL`、`-DL` | 80000 と 41000。b=AS から。単位は bit/s |
| `Flow-Status` | `ENABLED`（2）。sendrecv から |
| `Codec-Data` | offer と answer の m= の行と属性 |
| `Specific-Action` | 2、4、9。bearer の喪失、解放、割り当ての失敗 |
| `Rx-Request-Type` | INITIAL_REQUEST。以後の offer と answer は UPDATE_REQUEST |

> 手順書 [「通話のメディア」](../../../docs/guide/ja/volte.md#通話のメディア)。TS 29.214 A.1。TS 29.213 6.2 Table 6.2.1、6.3。

---

## 通話のメディアの AAR を読む

- フィルタは `diameter.Codec-Data` です。AAR を開き、Media-Component-Description を順に見ます。番号 1 が最初の m= の行です。
- Flow-Description の 1 つは `permit in 17 from 172.28.0.121 to 172.28.0.50 40004` です。メディアの相手は、もう一方の UE ではなく中継ノードです。
- AF-Application-Identifier を送るのは着信側の P-CSCF だけです。発信側は Feature-Caps から取るはずですが、それを書くノードがありません。簡略化の一覧に入っています。
- Service-Info-Status は FINAL_SERVICE_INFORMATION です。情報が offer だけでなく answer に基づいているからです。
- 保留すると、発信側の Flow-Status は `ENABLED-UPLINK`（0）、着信側は `ENABLED-DOWNLINK`（1）になります。

---

## 音声を運ぶ dedicated bearer

PCRF が音声を許可すると、スタブがイベントストリームで送ります。

```text
event: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
data: {"eps_bearer_identity":6,"linked_eps_bearer_identity":5,
       "eps_qos":{"qci":1,"maximum_bit_rate_for_uplink":80, ... },
       "tft":{"tft_operation_code":"create new TFT","packet_filters":[ ... ]}}
```

- QCI 1 は、帯域を保証する会話音声の種類です。速度は kbit/s で、P-CSCF が求めた帯域から決まります。
- TFT のフィルタは 4 つです。UE と中継ノードの間の RTP と RTCP の上りと下りです。
- UE は `activate-dedicated-eps-bearer-context-accept` で答え、通話の資源とみなします。通話が終わると DEACTIVATE EPS BEARER CONTEXT REQUEST が来ます。
- default bearer は 5、音声の bearer は 6 です。どちらも同じ PDN 接続に属するので、`linked_eps_bearer_identity` は 5 です。中継ノードが無ければ、フィルタの相手のアドレスはもう一方の UE になります。
- フィルタは `frame contains "EPS BEARER CONTEXT"` です。イベントは 1 つの HTTP の応答の中で少しずつ届きます。

> 手順書 [「bearer」](../../../docs/guide/ja/volte.md#bearer)。TS 24.229 L.2.2.5.1。TS 29.213 6.3。IR.92 4.4.2、2.4.3.1。

---

## プリコンディション（資源がそろうまで鳴らさない）

```ladder
volte10, P-CSCF, PCRF, volte12
volte10 -> P-CSCF: INVITE // curr:qos local none
P-CSCF -> volte12: INVITE。各 S-CSCF と AS を通る
P-CSCF <-- volte12: 183 // conf:qos remote sendrecv
P-CSCF <-> PCRF: 両側の AAR と AAA // bearer を 1 つずつ
volte10 <-- P-CSCF: 183。続いて PRACK とその 200
volte10 -> P-CSCF -> volte12: UPDATE // curr:qos local sendrecv
volte10 <-- P-CSCF <-- volte12: 200 (UPDATE) // 両方の区間が sendrecv
volte10 <-- P-CSCF <-- volte12: 180 // ここで初めて
volte10 <-- P-CSCF <-- volte12: 200 (INVITE)。続いて ACK
```

- INVITE は `Supported: 100rel, timer, 199, precondition` です。Require には入れません。183 は `Require: 100rel, precondition` です。UPDATE は、PRACK に 200 が返ってから送ります。
- 発信側では、UPDATE への 200 より先に 180 が来ることがあります。P-CSCF が新しい answer の AAR のために、その 200 を止めておくからです。フィルタは `sip.Require contains "precondition"`、プリコンディションの行をすべて探すなら `sdp.media_attr contains "qos"` です。

> 手順書 [「プリコンディション」](../../../docs/guide/ja/volte.md#プリコンディション)。RFC 3312。IR.92 2.4.1。TS 24.229 5.1.3.1。シーケンス図の全体とフレームの番号は手順書にあります。

---

## プリコンディションの行を読む

| 段階 | 送った側 | `curr` local | `curr` remote | `des` の強さ（local、remote） | `conf` |
| --- | --- | --- | --- | --- | --- |
| INVITE | volte10 | none | none | mandatory、optional | |
| 183 | volte12 | none | none | mandatory、mandatory | remote |
| UPDATE | volte10 | sendrecv | none | mandatory、mandatory | |
| 200 (UPDATE) | volte12 | sendrecv | sendrecv | mandatory、mandatory | |

- 値は SDP を送った側から見たものです。発信側は segmented status type です（TS 24.229 6.1.2）。
- 着信側は 183 で発信側の区間を mandatory に上げ、`conf:qos remote sendrecv` で知らせを求めます（RFC 3312 7）。183 の local と remote は volte12 から見たもので、volte12 の remote は volte10 の区間です。
- UE は dedicated bearer を受け取ると、資源を確保したとみなします（TS 24.229 L.2.2.5.1B、IR.92 2.4.3.1）。UPDATE がそれを伝えます。ue2 のように EPC スタブを使わない UE は、最初から確保済みです。向きはどの行も sendrecv です。

> 手順書 [「プリコンディション」](../../../docs/guide/ja/volte.md#プリコンディション) の表。RFC 3312 6。

---

## 呼び出しの条件をコードで見る

[internal/precondition/table.go](../../../internal/precondition/table.go)

```go
func (t *Table) Met() bool {
	for _, r := range t.rows {
		if r.Strength == Mandatory && !r.Current {
			return false
		}
	}
	return true
}
```

- 表の行は、区間（local と remote）と向き（send と recv）の組です。
- 着信側の UE は、この関数が true になるまで呼び出しを始めません。関数のすぐ上のコメントが RFC 3312 6 を指しています。
- 同じファイルの `NeedsOffer` は、UE 自身の資源が変わったときに新しい offer（UPDATE）が要るかを決めます。発信側の UPDATE はここから出ます。表を使うのは `internal/ue/session.go` です。

> 手順書 [「コードの場所」](../../../docs/guide/ja/volte.md#コードの場所)。RFC 3312 5、6。

---

## コーデックの offer と answer

```text
m=audio 53946 RTP/AVP 96 97 98 99 0 100 101
b=AS:80
b=RS:0
b=RR:4000
a=rtpmap:96 AMR-WB/16000/1
a=fmtp:96 mode-change-capability=2; max-red=0
a=rtpmap:97 AMR-WB/16000/1
a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1
```

- 96 と 98 は bandwidth-efficient、97 と 99 は octet-aligned です。発信側は前者を優先します（IR.92 3.2.5）。mode-set は付けません（IR.92 2.4.3.2）。
- PCMU を最後に残します。PCMU しか知らないソフトフォンとも話せるようにするためです。b=AS:80 は PCMU に合わせた値で、上りの帯域もこの値になります。
- volte12 は `m=audio 60734 RTP/AVP 96 100` と `b=AS:41` で答えます。PCMU しか知らない ue2 は `RTP/AVP 0 101` で答え、VoLTE の UE は PCMU で話します。
- b=RS:0 と b=RR:4000 は、送信者と受信者の RTCP の帯域で、単位は bit/s です（RFC 3556 2）。UE はこれから報告の間隔を計算します（RFC 3550 6.3.1）。5 秒より短くはなりません。

> 手順書 [「offer と answer」](../../../docs/guide/ja/volte.md#offer-と-answer)。offer の全体は手順書にあります。AMR が 98 と 99、PCMU が 0、telephone-event が 100 と 101 で、telephone-event は音声のコーデックごとのクロックレートです（TS 26.114 Annex G）。ptime は 20、maxptime は 240 です。RFC 4867。

---

## AMR のフレーム、RTCP、DTMF

```text
Adaptive Multi-Rate
    [Payload decoded as: RFC 3267 bandwidth-efficient mode (1)]
    1111 .... = CMR: No mode request (15)
    .... 0... = F bit: Last frame in this payload
    .... .100  0... .... = FT bits: AMR-WB 23.85 kbit/s (8) / Frame OK
    .1.. .... = Q bit: Ok
```

- 1 つのパケットに 20 ms のフレームが 1 つです。CMR、目次（F、FT、Q）、音声のビットの順です（RFC 4867 4.3）。
- UE は AMR を符号化も復号もしません。RFC 4867 5 の格納形式のフレームを送ります。送ったものと受けたものは、ビット単位で比べられます。
- RTCP は RTP の 1 つ上のポートです（RFC 3550 6.4.1、IR.92 3.2.4）。DTMF は telephone-event です（RFC 4733）。フィルタは `amr`、`rtcp`、`rtpevent` です。

> 手順書 [「フレーム」](../../../docs/guide/ja/volte.md#フレーム) と [「RTCP と DTMF」](../../../docs/guide/ja/volte.md#rtcp-と-dtmf)

---

## Wireshark で AMR のフレームを比べる

- Wireshark は、見た SDP に従って AMR-WB のフレームをデコードします。SDP が無いときは、**Preferences > Protocols > AMR** で Wideband AMR とペイロードの形式を選び、RTP のポートに **Decode As** を使います。
- `amr.wb.cmr` と `amr.wb.toc.ft` で、AMR-WB の CMR とフレームの種類を絞れます。
- 比べるフレームは `-sent-out` と `-received-out` で書き出します。e2e のスクリプトがそれを比べます。
- 送るフレームは `internal/ue/speech` に埋め込んだトーンです。製品のビルドの外にある cgo のツール `tools/amrgen` で作りました。
- DTMF の 1 桁はパケットの連なりで、最後の 3 つに終わりのビットが立ちます。

---

## セッションタイマー、Reason、早期メディア、保留

| 項目 | キャプチャで見るもの |
| --- | --- |
| セッションタイマー | INVITE の `Session-Expires: 1800`。200 の `refresher=uac`。発信側が 900 秒ごとに UPDATE。 |
| UE の Reason | BYE の `RELEASE_CAUSE;cause=1;text="User ends call"`。 |
| 早期メディア | INVITE の `P-Early-Media: supported`。許可するノードは無い。 |
| 保留と再開 | `a=sendonly` の re-INVITE と `a=recvonly` の 200。両方の Rx が更新される。 |

フィルタは `sip.Session-Expires` と `sip.Reason` です。

- メディアの bearer を失って終わった通話では、UE の Reason の cause は 3 です。P-CSCF の BYE は `Reason: SIP;cause=503` です。早期メディアを許可するノードが無いので、発信側は 180 のあと自分で呼び出し音を鳴らします。
- 保留の re-INVITE は、そのまま MMTel AS を通ります。保留と DTMF のある通話は、e2e のスクリプトがラボから動かすものです。

> 手順書 [「セッションタイマー、Reason、早期メディア」](../../../docs/guide/ja/volte.md#セッションタイマーreason早期メディア) と [「保留、再開、DTMF」](../../../docs/guide/ja/volte.md#保留再開dtmf)。IR.92 2.2.8、2.2.4、2.2.7。RFC 4028 9。TS 24.229 7.2A.18.11。RFC 5009。TS 24.610 4.5.2.1。

---

## 網が bearer を落とす

```sh
curl -X DELETE http://localhost:8080/api/epc/network-initiated/ues/001010000000010/bearers/6
```

```ladder
volte10, P-CSCF, S-CSCF, volte12, EPC と PCRF, ラボ
EPC と PCRF <- ラボ: DELETE .../bearers/6
volte10 <- EPC と PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST（イベントストリーム）
P-CSCF <- EPC と PCRF: ASR、Abort-Cause BEARER_RELEASED（0） // 続いて ASA
P-CSCF -> EPC と PCRF: STR // 続いて STA
P-CSCF -> S-CSCF: volte10 の名前の BYE // cause=503
volte10 <- P-CSCF: BYE // cause=503
P-CSCF -> volte12: BYE。S-CSCF と MMTel AS を通ったあと
volte12 <- EPC と PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST
```

> 手順書 [「網が bearer を落とす」](../../../docs/guide/ja/volte.md#網が-bearer-を落とす)。TS 29.214 4.4.6.1、4.4.6.2。TS 24.229 5.2.8.1.2。IR.92 2.4.2.3。

---

## bearer が落ちたときの見え方

- 音声の bearer がメディアをすべて運んでいたので、PCRF は RAR ではなく ASR で Rx のセッションを終えます。volte10 は自分では BYE を送りません。
- P-CSCF は `Store` に置いたダイアログの情報から BYE を作ります。相手の側への BYE は、サービスを受ける利用者の名前で送ります。
- サービスを受ける UE への BYE は、このプロジェクトが足したものです。音声の bearer だけを失った UE は信号の経路を持ったままなので、BYE が無いと、網が終えた通話に残り続けます（`internal/pcscf/release.go` のコメント）。
- 普通の BYE で終わる通話では、両側の P-CSCF が STR で Rx のセッションを終えます。Termination-Cause は DIAMETER_LOGOUT です（TS 29.214 4.4.4）。
- フィルタは `diameter.Abort-Cause` です。Rx の全体は `diameter.cmd.code in {265, 258, 274, 275}` で、AAR と AAA、RAR と RAA、ASR と ASA、STR と STA です。この実行では RAR と RAA は出ません。

---

## IR.92 が求めること

| IR.92 | 内容 | このプロジェクト |
| --- | --- | --- |
| 4.4.1、4.4.2、4.5 | 信号は QCI 5、音声は QCI 1。P-CSCF は PCO で | EPC スタブ |
| 2.4.1 | プリコンディション。状態の更新は UPDATE | 対応 |
| 2.4.2.1、2.4.2.3 | bearer を失ったら網がセッションを終える | P-CSCF の BYE |
| 3.2.1、3.2.5、2.4.3.2 | AMR と AMR-WB。2 つのペイロード形式 | 符号化済みのフレーム |
| 3.2.4、2.2.4、2.2.8 | RTCP、Reason、セッションタイマー | 対応 |

- 作っていないもの。登録の Contact の mmtel の ICSI、+sip.instance、audio のタグ。CMR に従うこと。IPsec の ESP。IPv6。理由は次のスライドにあります。

---

## 作っていないもの

- REGISTER の Contact には ICSI も +sip.instance も audio のタグもありません。INVITE とその応答の Contact には ICSI と audio があります。
- CMR に従うには、符号化できる UE が要ります。この UE は、前もって符号化したフレームを送ります。
- ESP は、キャプチャを読めるようにするために省きました。
- どのノードも IPv4 で、1 つの Docker のネットワークにいます。
- 国の標準（日本では TTC）は網どうしの接続を扱うので、1 つの網の中の通話の形は変えません。セッションタイマーの長さのような違いは、接続する相手の網ごとのプロファイルに置きます。網間接続の手順書で扱います。

> 手順書 [「VoLTE の端末とソフトフォンの違い」](../../../docs/guide/ja/volte.md#volte-の端末とソフトフォンの違い) と [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/volte.md#このプロジェクトで簡略化していること)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| EPC スタブ | NAS の符号化、S1-AP、無線、GTP-U、Gx、attach の認証が無い。bearer は記録だけ。 |
| PCRF | 許可するのは音声だけで、QCI 1。DRA を使わない。 |
| P-CSCF、UE、S-CSCF | ESP をかけない。AKAv1-MD5 だけ。 |
| P-CSCF | bearer を失うとすぐに解放する（通話ごとに音声の流れは 1 つだから）。BYE なしで終わったダイアログは `Store` に残る（UE ごとのダイアログの索引を持たず、複数の P-CSCF が同じ利用者を受け持てるようにするため）。 |
| UE と AMR | CMR と mode-set に従わない。PDN 接続を失うと止まる。 |

```sh
grep -rn "SIMPLIFIED:" internal/epc internal/pcscf internal/ue internal/aka internal/amr internal/rtcp internal/precondition
```

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/volte.md#このプロジェクトで簡略化していること)。項目ごとの仕様の条項と理由は手順書の表にあります。

---

## ラボで動かす

```sh
docker compose up -d --build
docker compose --profile volte up -d volte10 volte12
curl http://localhost:8080/api/epc/ues/001010000000010
```

1. <http://localhost:8080> を開きます。volte10 と volte12 のカードに、登録済みと出ます。
2. volte10 から `tel:+12025550112` に発信します。volte12 は呼び出し時間のあとで自分で応答します。
3. カードの保留、再開、DTMF のボタンを押します。コーデックとプリコンディションの表も出ます。
4. 前のスライドの `curl -X DELETE` で音声の bearer を解放します。

- 通話のシーケンス図には、P-CSCF の隣に PCRF の列があります。
- `curl` は volte10 の bearer を並べます。通話中は 5 と 6 です。ラボは、UE の状態と、網の手順を始める EPC スタブのパスを、`/api/epc` の下で中継します。

> 手順書 [「ラボで」](../../../docs/guide/ja/volte.md#ラボで)

---

## コマンドラインで動かす

```sh
docker compose --profile volte rm -sf volte10 volte12
docker compose run --rm volte12 answer -epc http://172.28.0.80:8080 \
  -impi 001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3 \
  -received-out /captures/volte12-received.amr
docker compose run --rm volte10 call -epc http://172.28.0.80:8080 \
  -impi 001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf \
  -sent-out /captures/volte10-sent.amr tel:+12025550112
```

- `-epc` で attach し、`-k` と `-opc` で IMS-AKA を使います。
- `-codecs` で offer のコーデックを選べます。`-no-preconditions` はプリコンディションを使いません。
- 1 行目は、プロファイルの UE を止めます。この 2 つのコマンドと volte10、volte12 のアドレスが同じだからです。volte12 と volte10 は別々の端末で動かします。SIPp を IMS-AKA で登録するコマンドは手順書にあります。

> 手順書 [「コマンドラインで」](../../../docs/guide/ja/volte.md#コマンドラインで)。フラグの一覧は手順書の表にあります。

---

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Security-Client \|\| sip.Security-Server \|\| sip.Security-Verify` | sec-agree |
| `diameter.3GPP-SIP-Authentication-Scheme == "Digest-AKAv1-MD5"` | IMS-AKA の MAR、MAA |
| `diameter.cmd.code in {265, 258, 274, 275}` | Rx。AAR と AAA、RAR と RAA、ASR と ASA、STR と STA |
| `diameter.Flow-Usage == 2`、`diameter.Codec-Data` | 信号の経路と通話の AAR |
| `http && ip.addr == 172.28.0.80` | EPC スタブの API |
| `sip.Require contains "precondition"` | プリコンディションの 183 |
| `amr`、`rtcp`、`rtpevent` | AMR、RTCP、DTMF |

- 手順書は、これらのフィルタを tshark 4.2 で e2e のスクリプトのキャプチャに当てて確かめています。

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/volte.md#wireshark-の表示フィルタ)。表の全体は手順書にあります。

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| EPC スタブと PCRF | `internal/epc` の `http.go`、`api.go`、`network.go`、`pcrf.go` |
| UE のアクセス網 | `internal/ue/access.go`、`access_epc.go` |
| MILENAGE と認証ベクトル | `internal/milenage`、`internal/aka` |
| IMS-AKA の challenge と sec-agree | `internal/scscf/aka.go`、`internal/pcscf/secagree.go`、`internal/ue/aka.go` |
| ポリシー制御と Rx | `internal/policy`、`internal/rx`、`internal/pcscf/policy.go` |
| 網による解放 | `internal/pcscf/release.go`、`dialog.go` |
| プリコンディション、AMR、RTCP | `internal/precondition`、`internal/amr`、`internal/rtcp` |

- `internal/ue/volte_epc_test.go` と `volte_aka_test.go` は、VoLTE の通話を IMS 全体を通して 1 つのプロセスで動かします。
- `internal/policy` の `PolicyControl` の 3 つのメソッド、`SubscribeSignallingPath`、`AuthorizeMedia`、`Release` を、この回の AAR と STR と照らし合わせます。

> 手順書 [「コードの場所」](../../../docs/guide/ja/volte.md#コードの場所)

---

[layout: exercise]

## 演習

1. 401 の nonce を base64 で戻し、MAA の SIP-Authenticate と比べます。AUTN の中の AMF はどこにありますか。
2. 着信側の通話の AAR の `Flow-Description` を読み、どの 2 つのアドレスの間のフローかを言います。
3. volte12 の 180 と、UPDATE への 200 のフレームの番号を比べます。
4. 保留したあとの AAR で、両側の `Flow-Status` を読みます。
5. volte10 から ue2 に発信します。answer のコーデックと、通話の AAR があるかを確かめます。
6. bearer を解放したあとの BYE を探し、誰が送ったか、Reason が何かを読みます。

1 には、手順書の「認証ベクトルを手で確かめる」のコマンドを使います。同じ節で、2 回目の REGISTER の response をシェルで計算します。

---

[layout: answers]

## 演習の答え

1. 最初の 16 オクテットが RAND、残りが AUTN です。AUTN の 7 と 8 オクテット目が AMF で、0000 です。
2. UE（172.28.0.121）と中継ノード（172.28.0.50）の間です。もう一方の UE ではありません。
3. 180 のほうがあとです。着信側は、mandatory のプリコンディションがすべてそろってから呼び出します。
4. 発信側が `ENABLED-UPLINK`（0）、着信側が `ENABLED-DOWNLINK`（1）です。
5. PCMU です。ue2 は attach していないので、ue2 の側には通話の AAR がありません。volte10 の側の AAR を探します。
6. P-CSCF が `Reason: SIP;cause=503` で送ります。volte10 は自分では BYE を送りません。

---

[layout: exercise]

## 振り返り

1. UE は、P-CSCF のアドレスをどこから知りますか。
2. 401 の `ik` と `ck` が UE に届かないのは、なぜですか。
3. UE が網を確かめるのは、どの値を使ってですか。
4. P-CSCF が通話の AAR を送るのは、どの応答を見たときですか。
5. RAR ではなく ASR が来たのは、なぜですか。
6. attach していないソフトフォンが登録すると、Rx では何が起きますか。

---

[layout: answers]

## 振り返りの答え

1. ATTACH ACCEPT の PCO にある、識別子 `000CH` のコンテナからです（TS 24.229 L.2.2.1 の方法 II）。
2. P-CSCF が取り除くからです。IK と CK はアソシエーションの鍵で、UE は自分の K と RAND から同じ鍵を計算するので、送る必要がありません。
3. AUTN の MAC と SQN です。MAC が合えば、網が K を知っていることが分かります。
4. SDP の answer を持ったときです。着信側では volte12 からの 183、発信側では volte10 へ向かう 183 です。
5. 音声の bearer がそのセッションのメディアをすべて運んでいたので、PCRF が Rx のセッション全体を終えるからです。
6. PCRF が信号の経路の AAR に 5065（IP-CAN_SESSION_NOT_AVAILABLE）で答え、P-CSCF はその UE をポリシー制御なしで扱います。

---

## 次の回

- 次が最後の回です。境界でつながる 2 つの IMS 網を扱います。通話、ショートメッセージ、会議が、IBCF と TrGW を通ってほかの事業者の網へ出ていきます。
- この回の 2 つの境界（UE の `AccessNetwork` と P-CSCF の `PolicyControl`）は、ほかのアクセス網をつなぐ場所です。前者の後ろには NAS を、後者の後ろには 5GC の N5 を置けます。

```sh
docker compose --profile ue --profile volte down --rmi all -v --remove-orphans
```

- これは手順書の「後片付け」です。ラボの `phones` のプロファイルも使ったなら、`--profile phones` を足します。
- 5GC では、SIP の信号は 5QI 5 の QoS フローを、音声は 5QI 1 を使います。EPS の QCI 5 と QCI 1 と同じ考え方です。

> 次の回の手順書は [IMS 網どうしを相互接続する](../../../docs/guide/ja/interconnect.md) です。手順書の一覧は [docs/guide/](../../../docs/guide/) にあります。
