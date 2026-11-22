# MMTel AS の補足サービス

[English](../en/mmtel.md)

この手順書では、マルチメディア電話（MMTel）の補足サービスが IMS の中でどう働くかを追います。次のことが分かります。

- S-CSCF がすべての通話を MMTel AS に渡すしくみ。
- AS が B2BUA として通話をどう扱うか。
- 利用者が Ut の XCAP でサービスを設定する方法。
- サービスごとの通話の流れ。識別子の通知と非通知、着信転送（CFU、CFB、CFNR、CFNL）、規制を、キャプチャで 1 つずつ読みます。

この手順書は次の版に沿っています。TS 24.229 V20.1.0、TS 24.604 V19.0.0（CDIV）、TS 24.607 V19.1.0（OIP、OIR）、TS 24.608 V19.0.0（TIP、TIR）、TS 24.611 V19.0.0（規制）、TS 24.615 V19.0.0（CW）、TS 24.623 V19.0.0（Ut）、TS 29.364 V19.1.0（サービスのデータ）、TS 29.328 V20.0.0（Sh）、TS 29.228（フィルタ条件）、GSMA IR.92 v24.0 です。以下の条項番号はこれらの版のものです。

## 始める前に

- [音声通話の流れを追う](call.md) と [SMS の流れを追う](sms.md) を読んでおくこと。この手順書はそこで説明した通話の INVITE、PRACK、ACK と、IP-SM-GW のところで説明した初期フィルタ条件（iFC）、オリジナルダイアログ識別子（ODI）、P-Served-User をそのまま使います。
- 略語の説明は [用語集](../../glossary.md) にあります。

## MMTel と、AS が要る理由

IMS の電話は MMTel のサービスです（TS 24.173）。その補足サービスは、電話網が通話のほかに昔から提供してきたものです。発信者を通知するか隠すか（OIP、OIR）、応答した相手を通知するか隠すか（TIP、TIR）、着信を転送すること（CDIV）、規制すること（CB）、通話を保留すること（HOLD）、通話中に 2 本目の着信を受けること（CW）があります。VoLTE の網がどれを提供しなければならないかは、GSMA IR.92 2.3 にあります。

CSCF は経路を決めるだけです。サービスには、利用者の設定を知り、それに従って通話を変えるノードが要ります。そこでどのサービスもアプリケーションサーバー（AS）である MMTel AS の手順になっています。S-CSCF は利用者の INVITE をすべて iFC で MMTel AS に送り、AS は通話ごとに利用者の設定を Sh で HSS から読みます（TS 29.364）。AS は設定の写しを持たないので、どのインスタンスでもどの通話を受けられます。利用者は UE と AS の間の HTTP のインタフェースである Ut で、XCAP を使って設定を変えます（TS 24.623）。役割 `mmtel` はその両方を動かします。SIP のポート 5060 で AS を、HTTP のポート 80 で XCAP サーバーを動かします。

IR.92 が網の対応を任意としている MWI と ECT（2.3.5、2.3.11）は作っていません。

## 登場するノード

| ノード | アドレス | MMTel での役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 発信者。`tel:+12025550101`。 |
| UE 2 | 172.28.0.101 | サービスを設定する利用者（served user）。`tel:+12025550102`。 |
| UE 3 | 172.28.0.103 | UE 2 が着信を転送する先。`tel:+12025550103`。 |
| P-CSCF | 172.28.0.40 | 誰が発信し、誰が応答したかを網として示す。隠された識別子を UE に渡さない。 |
| S-CSCF | 172.28.0.30 | 3 人の利用者をすべて担当する。iFC に従って、すべての INVITE を MMTel AS に送る。 |
| I-CSCF | 172.28.0.20 | 着信者の S-CSCF を探す（LIR）。着信者が登録していないときも探す。 |
| MMTel AS | 172.28.0.75 | 役割 `mmtel`。SIP は `mmtel.ims.mnc001.mcc001.3gppnetwork.org`、Ut は `xcap.ims.mnc001.mcc001.3gppnetwork.org`。 |
| HSS | 172.28.0.10 | iFC を持つ（Cx）。利用者ごとの `MMTelServices` の文書と Ut のパスワードを、Sh の repository data として持つ。 |

## S-CSCF が MMTel AS を選ぶしくみ

### 4 つのフィルタ条件

`deploy/compose/hss.yaml` は、どの加入者にも SMS の 2 つの条件のほかに、AS のための 4 つの条件を与えています。

```yaml
- priority: 10
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: ORIGINATING_REGISTERED}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_TERMINATED}
- priority: 11
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: TERMINATING_REGISTERED}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED}
- priority: 12
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: TERMINATING_UNREGISTERED}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED}
- priority: 13
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: ORIGINATING_CDIV}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_TERMINATED}
```

要素ごとに読みます（TS 29.228 Annex E。SMS と共通の要素は [SMS の手順書](sms.md) で説明しています）。

| 要素 | ここでの値 | 意味 |
| --- | --- | --- |
| `Priority` | 10 から 13 | SMS の条件（0 と 1）のあとに評価する。どの条件も 1 つの session case にしか一致しないので、4 つが競合することはない。 |
| `ConditionTypeCNF` | `true` | グループ 0 とグループ 1 の AND（TS 29.228 B.2.3）。 |
| `SPT` の `Method` | `INVITE` | 通話だけ。IR.92 のサービスは INVITE にだけ働く（TS 24.604 Annex B）。 |
| `SPT` の `SessionCase` | `0`、`1`、`2`、`4` | ORIGINATING_REGISTERED は利用者がかける通話。TERMINATING_REGISTERED は利用者への通話。TERMINATING_UNREGISTERED は登録していない利用者への通話で、CFNL に要る。ORIGINATING_CDIV は利用者への通話を AS が転送したもので、転送先への発信の規制のような、利用者の発信側のサービスを受ける（TS 24.229 5.4.3.3 step 3）。 |
| `ServerName` | `sip:mmtel...:5060` | AS。DNS でアドレスを引く。 |
| `DefaultHandling` | 発信側は `1`（SESSION_TERMINATED）、着信側は `0`（SESSION_CONTINUED） | 規制は確実に効かせる必要があるので、AS が答えなければ利用者の発信は終わる。利用者への通話は AS 無しで続ける（TS 24.229 5.4.3.2 step 5、5.4.3.3 step 4）。 |

着信側の条件に TERMINATING_UNREGISTERED があるので、HSS はどの試験用の加入者も、未登録の状態のサービスを持つ利用者として扱います。登録していない利用者の LIR は、DIAMETER_ERROR_IDENTITY_NOT_REGISTERED ではなく、S-CSCF の能力を付けた DIAMETER_UNREGISTERED_SERVICE（2003）になります（TS 29.228 6.1.4.1 step 3）。S-CSCF は UNREGISTERED_USER の SAR で利用者のプロファイルを取り、これらの条件を評価します（TS 29.228 6.1.2.1）。着信転送を設定していなければ、AS は INVITE をそのまま送り返し、S-CSCF が 480 を返します。

### P-Served-User と session case

1 つの AS が通話の両端を受け持つので、AS はそれぞれの INVITE が誰のための、どの場合のものかを知る必要があります。S-CSCF はそれを P-Served-User で伝えます（RFC 5502、RFC 8498。TS 24.229 5.4.3.2 step 5 b、5.4.3.3 step 4 c）。

```text
P-Served-User: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;sescase=orig;regstate=reg
P-Served-User: <sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org>;sescase=term;regstate=reg
P-Served-User: <sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org>;sescase=term;regstate=unreg
P-Served-User: <sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org>;orig-cdiv;regstate=reg
```

AS はこれで served user を決め（TS 24.229 5.7.1.3A）、その場合のサービスを決まった順に動かします（`internal/mmtel/sessioncase.go`）。

- **orig**。まず OCB、次に OIR、応答には TIP。
- **term**。まず ICB と ACR、次に CFU と CFNL、次に OIP と TIR、そのあと利用者への通話。その通話がどう終わったかで CFB か CFNR。
- **orig-cdiv**。転送元の利用者の OCB。

P-Served-User の無い INVITE は S-CSCF から来たものではないので、AS は 403 で断ります。S-CSCF は、要求が網の外に出る前に P-Served-User を取り除きます（5.4.3.2 step 14A、5.4.3.3 step 13A）。

### オリジナルダイアログ識別子

IP-SM-GW のときと同じように、S-CSCF は Route の先頭に AS を、その次に ODI を付けた自分を置きます（TS 24.229 5.4.3.2 step 5 a）。

```text
Route: <sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>,<sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr;odi=...>
```

今度は要求が戻ってきます。AS は自分の Route を取り除き、残りの Route に沿って新しい INVITE を送るので、INVITE は ODI を持って S-CSCF に届きます（TS 24.229 5.7.5.2.1 step 1、4）。S-CSCF は ODI を見て、一致した条件の次から処理を続けます。転送のあとのように Request-URI が変わっていれば、代わりに転送元の利用者の ORIGINATING_CDIV の条件を評価します（5.4.3.3 step 3）。

## プロキシではなく B2BUA。1 つの通話に 3 つのダイアログ

MMTel AS は routeing B2BUA として動きます（TS 24.229 5.7.5）。S-CSCF から届いた INVITE に UA として答え、受けた INVITE をもとに、自分の Call-ID を付けた新しい INVITE を送ります。どのサービスも変えないものはそのまま渡し、P-Charging-Vector の icid-value も残します。取り除くのは orig-ioi だけです（5.7.5.1）。TS 24.604 4.5.2.6.0 は、To を変えずに転送する AS ならプロキシとして動いてもよいとしています。この AS はどのサービスでも B2BUA として動きます。転送した通話がほかの場所で鳴っているあいだも、発信者のダイアログを持ち続けるためです。

利用者ごとに経路の上に AS があるので、UE 1 から UE 2 への通話は 3 つのダイアログになります。それぞれのダイアログが自分の Call-ID、タグ、CSeq を持ちます。

```text
 UE 1 ── P-CSCF ── S-CSCF ── MMTel AS ── S-CSCF ── I-CSCF ── S-CSCF ── MMTel AS ── S-CSCF ── P-CSCF ── UE 2
 |<--------- dialog C1 ---------->|<------------ dialog C2 ------------>|<--------- dialog C3 ---------->|
            (UE 1's services: sescase=orig)                (UE 2's services: sescase=term)
```

キャプチャには次のように表れます。

- **1 つの通話に Call-ID が 3 つ。** 1 つの Call-ID で絞り込むと、1 つのダイアログだけが見えます。網の中では、3 つとも同じ icid-value を持っています（P-CSCF は、UE に送るものからこの値を運ぶヘッダを消します）。ダッシュボードはこれで 3 つを 1 つの通話として表示します（[ダッシュボードの手順書](dashboard.md) の「ラダー図の組み立て方」）。転送した通話ではさらに 3 つ増えます。転送先への AS の INVITE、転送のあとに S-CSCF が AS へ送る orig-cdiv の INVITE、転送先の利用者の AS の INVITE です。
- **LIR はダイアログ C2 のもの。** S-CSCF が I-CSCF に送るのは、AS の INVITE だからです。
- **ダイアログの中の要求は、ダイアログごとに、届いた順に進む。** UE 1 の PRACK と ACK は UE 1 の AS で終わり、AS が C2 に自分の PRACK と ACK を送ります。UE 2 に PRACK を送るのは UE 2 の AS です。AS が送る PRACK には、UE 1 の PRACK のヘッダのうち、どちらかのレグだけのものではないヘッダ（P-Charging-Vector など）が入ります。UE 1 が PRACK に新しいオファーを入れたときは、そのオファーも AS の PRACK に入って進みます（RFC 3262 5）。AS は、ボディの無い PRACK には UA と同じようにすぐ 200 を返します。そのうえで、それぞれの側の要求を、受け取った順に、前の要求に応答が来てから 1 つずつ送ります。UE と同じく、PRACK に 200 が来てから UPDATE を送るのはこのためです。そうしないと、TCP で送る大きな UPDATE が UDP で送る PRACK より先に着信側に届き、着信側は CSeq の小さい PRACK を拒否します（RFC 3261 12.2.2）。PRACK に入れた answer と、UPDATE に入れた offer が行き違うこともあります（RFC 3264）。順番は発信側と着信側で別々に守るので、着信側の要求が発信側の要求を待つことはありません。AS は自分の側で受けた 2xx にすぐ ACK を返すので、200 が UE 1 に届くより先に、ACK が UE 2 に届くことがあります。BYE は、届いたダイアログの上で AS が答え、AS は同じ Reason を付けた新しい BYE を次のダイアログに送ります（RFC 3326 2）。
- **SDP を持つ re-INVITE と UPDATE は、もう一方の側に渡り**、その答えが戻ります。HOLD の re-INVITE もこの道を通ります（TS 24.610）。セッションタイマーを更新するボディの無い UPDATE は AS で終わります。タイマーはダイアログごとにあるためです。

AS が送る INVITE は、その先で分かれる（フォークする）ことがあります。着信者の何台かの UE が、それぞれ自分の early dialog で答えるときです（RFC 3261 13.2.2.4）。AS は、発信者から見れば 1 つの UAS です。UAS は、1 つの要求へのすべての応答に、同じ To の tag を付けます（8.2.6.2）。そのため、向こう側の early dialog がいくつあっても、発信者と AS の early dialog は 1 つです。発信者は、そのダイアログで最初に届いたセッション記述を answer とし、あとのものは無視します（13.2.1）。そこで AS は、最初にセッション記述を送ってきたダイアログのものを渡し、ほかのダイアログの応答はセッション記述を外して渡します。ほかのダイアログの 200 も、SDP を外して発信者に届きます。ただし、信頼できる暫定応答が answer を運んでいなければ、2xx が answer を運ばなければなりません（13.2.1）。そのときの 200 には、最初のダイアログの answer をもう一度入れます。ここまでは、最初のダイアログが続いているあいだのことです。199（RFC 6228）か転送でそのダイアログが終わったあとは、発信者の offer への answer として次に届いたセッション記述を、そのまま発信者に渡します。プリコンディションを使う UAS は、発信者が資源を確保したと分かってから利用者を呼び出すので、発信者がその answer を受け取る必要があるためです（RFC 3312）。AS は、ダイアログごとに最後のセッション記述を覚えておきます。応答と UPDATE に入っていたものと、発信者の UPDATE と PRACK への答えに入っていたものです。そして、応えたダイアログのセッション記述で、発信者のメディアをそのダイアログに向けます。CAT があれば、トーンを止める UPDATE がこれを行います（[MRF の手順書](mrf.md)）。CAT が無ければ、発信者が 200 に ACK を返したあとで、AS は応えたダイアログのセッションを発信者に offer します。発信者が Allow に UPDATE を挙げていれば UPDATE で（RFC 3311）、挙げていなければ re-INVITE で送ります（RFC 3261 14.1）。この offer の `o=` 行は、発信者が持っているセッション記述のものと同じで、版の番号（version）だけを 1 上げます（RFC 3264 8）。この offer が発信者の offer と行き違うと、発信者は 491（Request Pending）で断ります。そのときは、RFC 3261 14.1 の時間だけ待ってから送り直します。offer の無い INVITE のあとでは、応えたダイアログの 200 が、その UAS の offer を運ぶことがあります。そのときは、AS の offer への発信者の answer を、ACK に入れてその UAS に送ります（13.2.2.4）。`internal/mmtel` の試験が、この場合を見せます（`TestForkBeyondTheAS`、`TestSessionOfTheBranchThatAnswered`、`TestSessionOfTheBranchThatAnsweredAfterGlare`、`TestOfferOfTheBranchThatAnswered`、`TestToneOfAForkedCall`）。このプロジェクトのノードは、どれも INVITE をフォークしません。

## Ut。XCAP でサービスを設定する

### 文書

利用者ごとに、XCAP の simservs の application usage の XML 文書が 1 つあります（TS 24.623、RFC 4825）。XCAP サーバーは文書を持ちません。要求ごとに、利用者の `MMTelServices` の文書を、Service-Indication が `MMTEL-Services` の repository data として Sh で HSS から読みます（TS 29.364 7.2）。利用者の部分を見せたり変えたりしたあと、PUR で書き戻します（TS 29.328 6.1.2.1）。repository data には Sequence Number があり、HSS は更新のたびにそれを確かめます。XCAP サーバーはこれを文書のエンティティタグ（ETag）として使います（RFC 4825 7.11）。

文書の場所は次のとおりです。

```text
http://xcap.ims.mnc001.mcc001.3gppnetwork.org/simservs.ngn.etsi.org/users/sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org/simservs.xml
```

`http://xcap.<ホームドメイン>` は、設定が無いときに UE が作る XCAP Root URI です（TS 23.003 13.9.1.2）。利用者の部分は利用者の公開ユーザー識別子（XUI）です。`hss.yaml` が用意する文書には、IR.92 が求めるサービスがすべて入っています。CDIV と CB の規則は `<rule-deactivated/>` を付けて止めた状態で、転送先を空にして入れてあります。UE は規則を足すのではなく、規則を変えてサービスを有効にします（GSMA IR.92 2.3.2 Note 3）。

### 1 回の変更。CFU を有効にする

`ue xcap set cfu on tel:+12025550103` は次の要求を送ります。

1. 資格情報を付けずに、文書の全体を **GET** します。AS は利用者の Ut のパスワードを、Service-Indication が `Ut-Digest-Credential` の repository data として Sh で読み（UDR）、`WWW-Authenticate: Digest` の 2 つのチャレンジを付けた **401** を返します。1 つ目は SHA-256、2 つ目は MD5 です（RFC 7616 3.3、TS 24.623 5.2.3.2.1）。
2. 1 つ目のチャレンジに対する `Authorization: Digest` を付けて、もう一度 **GET** します。AS は資格情報と文書を読み（2 つの UDR）、文書と `ETag: "0"` を付けた **200** を返します。
3. 規則を 1 つだけ **PUT** します。URI の `~~` のあとがノードセレクタで、`If-Match: "0"` を付けます（RFC 4825 6、7.11。GSMA IR.92 2.3.2）。

   ```text
   PUT /simservs.ngn.etsi.org/users/sip:+12025550102@.../simservs.xml/~~/simservs/communication-diversion/cp:ruleset/cp:rule%5B@id=%22cfu%22%5D?xmlns(cp=urn:ietf:params:xml:ns:common-policy)
   Content-Type: application/xcap-el+xml
   If-Match: "0"

   <cp:rule xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy" id="cfu"><cp:conditions/><cp:actions><forward-to><target>tel:+12025550103</target></forward-to></cp:actions></cp:rule>
   ```

   規則から `<rule-deactivated/>` が消え、転送先が入っています。AS は資格情報と文書をもう一度読んで規則を置き換え、結果を確かめてから **PUR** で書き込みます。PUR の Sh-User-Data には `<SequenceNumber>1</SequenceNumber>` が入ります（TS 29.328 7.6.1、6.1.2.1）。**PUA** が返ると、AS は `ETag: "1"` を付けた **200** を返します。
4. 文書の全体を **GET** します。`ue xcap` はこれを設定として表示します。

GET と PUT の間にほかの誰かが文書を変えていれば、HSS は PUR を断り、AS は 412 を返します。UE は文書を読み直してやり直します。`ue xcap noreply 5` も同じように `NoReplyTimer` の要素を置き換えます。CFNR で転送するまでに呼び出す秒数で、5 から 180 の間です（TS 24.604 4.9.1.1A）。

## 動かしてみる

`scripts/e2e-mmtel.sh` は以下のことをすべて行い、この手順書で説明するメッセージをキャプチャで 1 つずつ確かめます。足りないものがあればエラーで終わります。キャプチャは `captures/` に、通話、Ut、Sh のフレームの一覧は `e2e-out/mmtel-ladder.txt` に、UE のログは `e2e-out/` に残ります。通話にかかる時間は 90 秒ほどです。

サービスを 1 つだけ手で試すには、システムを起動して UE 2 のサービスを設定します。`ue xcap` は登録を必要としません。

```sh
docker compose up -d --build
docker compose run --rm ue2 xcap \
  --impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-2 set cfu on tel:+12025550103
```

次に UE 3 に着信を待たせます。このコマンドは通話が終わるまで戻りません。

```sh
docker compose run --rm ue3 answer \
  --impi 001010000000003@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-3
```

別の端末で、UE 1 から UE 2 に発信します。

```sh
docker compose run --rm ue call tel:+12025550102
```

UE 1 は `"msg":"call progress","status":181` を、続いて `"msg":"call answered"` をログに出します。UE 3 は着信をログに出します。`set cfu off` でサービスを止めます。ほかのコマンドは `ue xcap get`、`set SERVICE on|off [TARGET]`（`cfu`、`cfb`、`cfnr`、`cfnrc`、`cfnl`、`baic`、`bic-roam`、`acr`、`baoc`、`boic`、`boic-exhc`）、`oir restricted|not-restricted`、`noreply SECONDS` です。サービス `ue2` のアドレスは決まっているので、`ue2` のほかのコンテナが動いているあいだは `ue2 xcap` を動かせません。

ダッシュボードの UE のカードからも同じことができます。[ダッシュボードの手順書](dashboard.md) の「UE シミュレータを操作する」を見てください。ラダー図は通話のすべてのダイアログを MMTel AS の列も含めて 1 つの図に描きます。

## キャプチャを開く

`captures/` の一番新しいファイルを Wireshark で開き、次の表示フィルタを使います。

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (http && ip.addr == 172.28.0.75) || diameter.cmd.code in {301, 302, 306, 307}
```

通話、Ut、AS の Sh の読み出しと更新、LIR と SAR が見えます。**Telephony > VoIP Calls** は、ダイアログを 1 つずつ別の通話として並べます。

## AS を通る通常の通話

スクリプトの最初の通話で、サービスはすべて止まっています。フレームの番号はスクリプトを 1 回動かしたときのものです。

```text
#468  UE 1   -> P-CSCF  C1 INVITE tel:+12025550102
#589  S-CSCF -> MMTel   C1 INVITE  P-Served-User: <UE 1>;sescase=orig;regstate=reg, Route: mmtel, scscf;odi
#596  MMTel  -> HSS        UDR  Data-Reference 0 (RepositoryData), Service-Indication MMTEL-Services, UE 1
#612  HSS    -> MMTel      UDA 2001 with UE 1's MMTelServices
#618  MMTel  -> S-CSCF  C2 INVITE  same icid-value, Route: scscf;odi
#632  I-CSCF -> HSS        LIR  sip:+12025550102@...
#645  S-CSCF -> MMTel   C2 INVITE  P-Served-User: <UE 2>;sescase=term;regstate=reg
#658  MMTel  -> S-CSCF  C3 INVITE  same icid-value
#670  P-CSCF -> UE 2    C3 INVITE
      ... 183, PRACK, 180, PRACK dialog by dialog ...
#886  P-CSCF -> S-CSCF  C3 200 OK  P-Asserted-Identity: <sip:+12025550102@...>
#941  P-CSCF -> UE 1    C1 200 OK  P-Asserted-Identity: <sip:+12025550102@...>
#1164 UE 1   -> P-CSCF  C1 BYE
#1239 P-CSCF -> UE 2    C3 BYE
```

1. **UE 1 の S-CSCF が INVITE を AS に送ります**（TS 24.229 5.4.3.2 step 5）。P-Served-User と ODI を付けます。
2. **AS が UE 1 のサービスを UDR で読みます**（TS 29.364 7.2.0、TS 29.328 6.1.1）。既定の文書では、通話は何も変わりません。
3. **AS が新しい INVITE を送ります。これがダイアログ C2 です**（TS 24.229 5.7.5.2.1）。残りの Route、P-Asserted-Identity、どのサービスも変えないヘッダを写します。
4. **S-CSCF は ODI から通話の処理を続けます。** ENUM を引き、I-CSCF とその LIR に進みます。
5. **UE 2 の S-CSCF が、今度は `sescase=term` で INVITE をもう一度 AS に送り**（5.4.3.3 step 4）、AS がダイアログ C3 を送り返します。そのあとは [通話の手順書](call.md) のとおりに進みます。
6. **200 には応答した相手が入っています。** UE 2 の P-CSCF は UE 2 の応答の P-Asserted-Identity に INVITE の P-Called-Party-ID を入れ（TS 24.229 5.2.6.4.4 step 1）、UE 1 がそれを受け取ります。これが TIP で、どの利用者も既定で持っています（TS 24.608 4.5.2.4）。

## OIR。発信者が識別子を隠す

UE 1 は OIR の既定を非通知にしてから（`ue xcap oir restricted`。`simservs/originating-identity-presentation-restriction/default-behaviour` を PUT します）発信します。hss.yaml の OIR は一時的なモードです。既定は利用者が選び、UE は `Privacy: none` を付ければ、その通話だけ識別子を通知できます（TS 24.607 4.5.2.4）。

- **UE 1 の AS** は `Privacy: id` を足し、From を `"Anonymous" <sip:anonymous@anonymous.invalid>` にします（TS 24.607 4.5.2.4、RFC 3323 4.1.1.3）。P-Asserted-Identity は残します。網の中では、識別子は網が保証したままです。
- **UE 2 の AS** は OIP を動かします（TS 24.607 4.5.2.9）。UE 2 は OIP を持っているので、`id` を残します。発信者が識別子を隠したことを UE 2 が知るためです。
- **UE 2 の S-CSCF** は P-Asserted-Identity を取り除きます。UE は信頼ドメインの外にあり、Privacy に `id` があるからです（TS 24.229 5.4.3.3 step 12、4.4.2。RFC 3325）。

UE 2 に届く INVITE は、匿名の From と `Privacy: id` を持ち、UE 1 を示すものは何も入っていません。TIR はこの逆向きで同じように働きます。UE 2 の AS が UE 2 の応答の Privacy に `id` を入れ（TS 24.608 4.5.2.9）、UE 1 の P-CSCF が応答を UE 1 に送る前に P-Asserted-Identity を取り除きます（RFC 3325 7）。

## CFU。すべての着信を転送する

UE 2 は上の手順で Ut から CFU を UE 3 に設定し、登録したままにしておきます。UE 1 が UE 2 に発信します。

```text
UE 1  P-CSCF S-CSCF  MMTel AS(UE 1)  I-CSCF  MMTel AS(UE 2)        UE 3
 |INVITE|     |           |            |          |                 |
 |----->|---->|---------->| C1         |          |                 |
 |      |     |<----------| C2         |          |                 |
 |      |     |------------------------>  LIR     |                 |
 |      |     |<------------------------|         |                 |
 |      |     |----------------------------------->| term: CFU      |
 |      |     |<-----------------------------------| 181 on C2      |
 |<-----|<----|<----------| 181 on C1  |          |                 |
 |      |     |<-----------------------------------| INVITE C4, cause=302, History-Info
 |      |     |----------------------------------->| orig-cdiv (C4) |
 |      |     |<-----------------------------------| INVITE C5      |
 |      |     |  ... I-CSCF, UE 3's S-CSCF, UE 3's AS (C6) ...      |
 |      |     |------------------------------------------------------->| INVITE with History-Info
 |<-----|<----|<---------------------------------------------------------| 200 OK
```

- **UE 2 の AS は、INVITE が届いた時点で転送します**（TS 24.604 4.5.2.6.3 item 1、4.6.7）。INVITE は UE 2 に届きません。
- **発信者には 181 Call Is Being Forwarded で知らせます**（TS 24.604 4.5.2.6.4）。P-Asserted-Identity に UE 2 を、History-Info に転送の経歴を入れます。転送先の項目には `Privacy=history` が付きます。UE 3 が自分の識別子を見せてよいかを、AS は知らないためです。
- **転送の INVITE がダイアログ C4 です**（TS 24.604 4.5.2.6.2）。
  - Request-URI は転送先を `user=phone` の SIP URI にしたもので、転送の理由（RFC 4458）が付きます。`sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org;user=phone;cause=302` です。
  - History-Info には 2 つの項目があります。index 1 の UE 2 と、index 1.1 で `mp=1` を持つ UE 3 です。`mp=1` は、1.1 が 1 から宛先を変えたものであることを示します（RFC 7044 10.3）。

    ```text
    History-Info: <sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org>;index=1
    History-Info: <sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org;user=phone;cause=302>;index=1.1;mp=1
    ```

  - 理由の値は CFU が 302、CFB が 486、CFNR が 408、CFNL が 404、CFNRc が 503 です（TS 24.604 Annex C）。
- **S-CSCF は ODI の付いた要求の Request-URI が変わったことに気づき**、転送のあとの UE 2 の発信側のサービスを動かします。INVITE は `orig-cdiv` を付けてもう一度 AS に届きます（TS 24.229 5.4.3.3 step 3、RFC 8498）。UE 2 の発信の規制はここで UE 3 への通話に掛かります（TS 24.611 4.6.7）。そのあと S-CSCF は番号を ENUM で UE 3 の SIP URI に変え（5.4.3.2 step 10）、通話はほかの通話と同じように、UE 3 の AS を通って UE 3 に届きます。

AS は 1 つの通話の転送を、hss.yaml の事業者の設定どおり 5 回までにします。回数は History-Info のうち理由の付いた項目で数えます。それを超えると、`Warning: 399 ... "Too many diversions appeared"` を付けた 480 を返します（TS 24.604 4.5.2.6.1）。

## CFB。着信者が話し中

UE 2 はダイアログ C3 の INVITE に **486 Busy Here** を返します。UE 2 の AS はそれに ACK を返し、UE 1 に 181 を送って、`cause=486` で転送します。History-Info の UE 2 の項目には、応答の Reason が `Reason=SIP%3Bcause%3D486...` として付きます（TS 24.604 4.5.2.6.2.2 b、4.5.2.6.3）。

## CFNR。誰も応答しない

UE 2 は CFNR を UE 3 に設定し、無応答のタイマーを 5 秒にしてから、応答せずに鳴り続けます。これはスクリプトを 1 回動かしたときのラダー図です。ダイアログには現れた順に C1 から C6 の番号を付けています。

```text
#7660  34.861 UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#7775  34.876 S-CSCF -> MMTel  C1 INVITE   P-Served-User: <UE 1>;sescase=orig;regstate=reg
#7779  34.876 MMTel  -> HSS       UDR      (UE 1's MMTEL-Services)
#7803  34.880 MMTel  -> S-CSCF C2 INVITE   tel:+12025550102
#7817  34.884 I-CSCF -> HSS       LIR      (UE 2)
#7830  34.887 S-CSCF -> MMTel  C2 INVITE   P-Served-User: <UE 2>;sescase=term;regstate=reg
#7834  34.887 MMTel  -> HSS       UDR      (UE 2's MMTEL-Services)
#7843  34.891 MMTel  -> S-CSCF C3 INVITE   sip:+12025550102@...
#7855  34.894 P-CSCF -> UE 2   C3 INVITE
#7858  34.896 UE 2   -> P-CSCF C3 183      then PRACK and 200 dialog by dialog
#7953  34.919 UE 2   -> P-CSCF C3 180      UE 2 rings
#7964  34.922 S-CSCF -> MMTel  C3 180      P-Asserted-Identity: <UE 2>; the no reply timer starts
#7990  34.926 P-CSCF -> UE 1   C1 180      then PRACK and 200 dialog by dialog
#8082  39.924 MMTel  -> S-CSCF C3 CANCEL   Reason: SIP;cause=408;text="Request Timeout"
#8094  39.925 P-CSCF -> UE 2   C3 CANCEL   Reason: SIP;cause=408;text="Request Timeout"
#8101  39.926 UE 2   -> P-CSCF C3 487
#8125  39.928 MMTel  -> S-CSCF C2 199      Reason: SIP;cause=408
#8127  39.928 MMTel  -> S-CSCF C2 181      History-Info: <UE 2>;index=1, <...+12025550103...;cause=408?Privacy=history>;index=1.1;mp=1
#8164  39.932 P-CSCF -> UE 1   C1 181
#8256  39.941 MMTel  -> S-CSCF C4 INVITE   sip:+12025550103@...;user=phone;cause=408, History-Info
#8294  39.946 S-CSCF -> MMTel  C4 INVITE   P-Served-User: <UE 2>;orig-cdiv;regstate=reg
#8314  39.950 MMTel  -> S-CSCF C5 INVITE
#8326  39.958 S-CSCF -> I-CSCF C5 INVITE   sip:+12025550103@... (after ENUM)
#8343  39.961 S-CSCF -> MMTel  C5 INVITE   P-Served-User: <UE 3>;sescase=term;regstate=reg
#8356  39.964 MMTel  -> S-CSCF C6 INVITE
#8369  39.968 P-CSCF -> UE 3   C6 INVITE   History-Info
       ... UE 3's 183, 180 and 200 back to UE 1 on C1 ...
```

- **最初の 180 で無応答のタイマーが始まります**（TS 24.604 4.5.2.6.3 item 2）。フォークのほかの分岐から来た 180 では、タイマーをやり直しません。UE 2 は利用者を呼び出すときに 180 を送ります。183 のあとで、プリコンディションを使うときはそれが満たされてからです（RFC 3261 21.1.2。[通話の手順書](call.md) の手順 9）。タイマーの長さは UE 2 の `NoReplyTimer` で、それが無ければ hss.yaml の事業者の既定値の 20 秒です（4.8.1）。
- **タイマーが切れると、AS は UE 2 への通話を `Reason: SIP;cause=408` を付けた CANCEL で止めます**（4.5.2.6.3 item 2）。この回では 180 の 5.0 秒あとでした。途中の CSCF はどれも、自分の CANCEL にこの Reason を写すので（RFC 3326 2）、UE 2 にも理由が伝わります。
- **199 Early Dialog Terminated** が、ダイアログ C2 の上で UE 2 の初期ダイアログを終わらせます（RFC 6228）。UE 1 の AS は UE 1 との初期ダイアログを 1 つのまま保ち、199 を UE 1 には送りません。そのため UE 1 には、181 と UE 3 の応答が同じ初期ダイアログの上で届きます。
- 181 から先は CFU と同じように `cause=408` で転送します。

UE 2 が Q.850 の cause 19（利用者が応答しない）の Reason を付けた 480 を返したときも、AS は無応答の時間が終わったものとして扱います（4.5.2.6.6）。

## CFNL。着信者が登録していない

UE 2 は CFNL を UE 3 に設定し、登録していません。AS に届くまでの流れは、未登録の状態のサービスを持つ着信者への通話の流れです。

1. I-CSCF の UE 2 の **LIR** は能力を付けた **DIAMETER_UNREGISTERED_SERVICE（2003）** になり、I-CSCF が S-CSCF を選びます（TS 29.228 6.1.4.1 step 3、6.7）。
2. その S-CSCF は **UNREGISTERED_USER** の **SAR** で UE 2 のプロファイルを取り（TS 29.228 6.1.2.1）、TERMINATING_UNREGISTERED の条件を評価します（TS 24.229 5.4.3.3）。
3. INVITE は `P-Served-User: <UE 2>;sescase=term;regstate=unreg` を付けて AS に届きます。AS の CFNL の規則には `<not-registered/>` の条件があるので、AS はすぐに `cause=404` で転送します（TS 24.604 4.5.2.6.2、4.6.7）。

CFNL が無ければ、AS は INVITE をそのまま送り返し、S-CSCF が 480 を返します。[音声通話の流れを追う](call.md) で、登録していない加入者 3 に発信したときに見えるのがこれです。

## 規制

- **着信の規制（BAIC）。** UE 2 が `baic on` にします。UE 2 の AS はほかのどのサービスよりも先に着信の規制の規則を評価し（TS 24.611 4.5.2.6.1、4.6.7）、INVITE に **603 Decline** を返します。通話は UE 2 に届きません。`<anonymous/>` の条件を持つ規則（ACR）なら、代わりに **433 Anonymity Disallowed** を返します（RFC 5079）。
- **発信の規制（BAOC）。** UE 1 が `baoc on` にします。UE 1 の AS は通話を何も変えないうちに規制し（TS 24.611 4.5.2.4.1）、**603** を返します。AS は着信者に向けた INVITE を送りません。緊急通報は規制しません。

どちらの 603 の前にも、発信者にはアナウンスが流れます。`deploy/compose/mmtel.yaml` に、MRFC（`mrfc:`）とアナウンス（`announcement: notice`）を書いてあるためです。AS は、流す音声を Request-URI で指定した INVITE を SDP なしで MRFC に送ります（RFC 4240 3）。そのうえで、MRFP を指す SDP と `P-Early-Media: sendonly` を付けた 183 を、高信頼の暫定応答として発信者に返します（TS 24.628 4.2.4、Annex G）。発信者が PRACK で 183 を受け取ったことを伝えると、AS は MRFC に ACK を送り、MRFP がアナウンスを流し始めます。アナウンスが終わると MRFC が BYE を送り、AS は `Reason: SIP;cause=603` を付けた 603 を返します。着信の規制では、キャプチャは次のようになります。

```text
#11978 51.547 UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#12124 51.580 S-CSCF -> MMTel  C2 INVITE   P-Served-User: <UE 2>;sescase=term
#12140 51.584 MMTel  -> MRFC   M  INVITE   sip:annc@mrfc...;play=/provisioned/notice, no SDP
#12161 51.588 MRFC   -> MMTel  M  200 OK   SDP offer: the MRFP 172.28.0.86, PCMU, sendonly
#12164 51.589 MMTel  -> S-CSCF C2 183      SDP answer from the MRFP, P-Early-Media: sendonly
#12187 51.591 P-CSCF -> UE 1   C1 183      then PRACK and 200 dialog by dialog
#12232 51.603 MMTel  -> MRFC   M  ACK      SDP answer: the caller's media, recvonly
#12248 51.626 MRFP   -> agw       RTP      PCMU, the announcement, on to UE 1
#12378 52.709 MRFC   -> MMTel  M  BYE      the announcement has ended
#12383 52.710 MMTel  -> S-CSCF C2 603      Reason: SIP;cause=603;text="Decline"
#12427 52.714 P-CSCF -> UE 1   C1 603      Reason: SIP;cause=603;text="Decline"
```

INVITE が高信頼の暫定応答を受け付けない発信者や、プリコンディションを必須にした発信者には、アナウンスを流さずにすぐ 603 を返します。MRFC と MRFP の動きは、MRF の手順書で詳しく追います。

BOIC と BOIC-exHC は着信者の番号の国番号をホーム網の国番号と比べます。ホーム網の国番号は `deploy/compose/mmtel.yaml` の `1` です（TS 24.611 4.9.1.4）。

## CW と HOLD

IR.92 は割込通話を網ではなく UE で行うよう求めています（2.3.4）。着信者の UE は `Alert-Info: <urn:alert:service:call-waiting>` を付けた 180 で、着信が待っていることを知らせます（TS 24.615 4.5.5.3.2、RFC 7462）。着信者の AS はそれをそのまま渡します。発信者に知らせないという事業者の設定があれば、hss.yaml のように Alert-Info を取り除きます（TS 24.615 4.5.5.2.3）。`ue serve` は通話中にも 2 本目の着信を受けます。UE はこの Alert-Info を付けて呼び出すだけで、自分からは応答しません。`POST /answer` を送ると、1 本目を保留にしてから待っている着信に応答します（TS 24.615 4.5.5.3.3）。3 本目の着信には 486 を返します。CW は `scripts/e2e-mrf.sh` がキャプチャで確かめていて、その流れは [MRF の手順書](mrf.md) で追っています。1 つのプロセスの中では、`internal/mmtel/waiting_test.go` の `TestCommunicationWaiting` と、`internal/ue/phone_test.go` の `TestServeTwoCalls` が確かめています。HOLD は `a=sendonly` を持つ re-INVITE で、AS はこれを届いたままもう一方の側に渡します（TS 24.610 4.5.2.1）。1 つのプロセスで動かす試験 `TestHoldThroughTheIMS` で確かめています。

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.P-Served-User` | S-CSCF が AS に送る INVITE。`sip.P-Served-User contains "orig-cdiv"` は転送のあとの INVITE、`contains "regstate=unreg"` は登録していない利用者のための INVITE。 |
| `sip.Route contains "odi="` | S-CSCF のオリジナルダイアログ識別子を付けた、AS への要求。 |
| `sip.P-Charging-Vector contains "icid-value=..."` | 網の中の、1 つの通話のすべてのダイアログ。P-CSCF は UE に送るものから P-Charging-Vector を消します。 |
| `sip.History-Info` | 転送した INVITE と、転送を知らせる 181 と 200。 |
| `sip.r-uri contains "cause="` | RFC 4458 の理由を付けた、転送した INVITE。 |
| `sip.Status-Code == 181` | 転送の通知。 |
| `sip.Status-Code == 199` | 終わった初期ダイアログ。 |
| `sip.Privacy` | 識別子を隠す要求と応答。 |
| `sip.from.addr == "sip:anonymous@anonymous.invalid"` | 匿名の発信者。 |
| `sip.Method == "CANCEL" && sip.Reason` | CFNR のものなど、Reason を付けた CANCEL。 |
| `http && ip.addr == 172.28.0.75` | Ut。`http.www_authenticate` はチャレンジ、`http.request.method == "PUT"` は変更、`http.response.line matches "^etag:"` は版を見つける。 |
| `diameter.cmd.code in {306, 307} && ip.addr == 172.28.0.75` | MMTel AS の UDR と PUR。`diameter.Service-Indication == "MMTEL-Services"` はサービスのデータの読み出し、`== "Ut-Digest-Credential"` は Ut のパスワードの読み出しを見つける。 |
| `diameter.Experimental-Result-Code == 2003` | 未登録の状態のサービスを持つ利用者の LIA。 |

これらのフィルタは `scripts/e2e-mmtel.sh` のキャプチャを使い、tshark 4.2 で確かめました。

## コードの場所

| パッケージ、ファイル | 内容 |
| --- | --- |
| `internal/mmtel/mmtel.go`、`call.go` | routeing B2BUA としての AS。AS の UA、呼ごとの手順、着信側への INVITE、AS 自身が返す応答。 |
| `internal/mmtel/hooks.go`、`fields.go` | 2 つの側のあいだを渡るメッセージに AS が入れるもの。もう一方の側に渡すヘッダ、課金ベクタ、通話の記録。 |
| `internal/b2bua` | 呼の 2 つのダイアログのあいだの中継。応答と PRACK、2xx とその ACK、届いた順に渡すダイアログの中の要求、フォークしたときの early dialog、呼の終わり。何を渡すかは AS が決める（`Hooks`）。 |
| `internal/mmtel/sessioncase.go` | session case ごとのサービスの順番。 |
| `internal/mmtel/identity.go` | OIP、OIR、TIP、TIR。 |
| `internal/mmtel/diversion.go` | CDIV。CFU、CFB、CFNR、CFNRc、CFNL、History-Info、181、無応答のタイマー。 |
| `internal/mmtel/barring.go` | OCB、ICB、ACR。 |
| `internal/mmtel/waiting.go` | CW。 |
| `internal/mmtel/services.go`、`store.go` | 通話ごとの Sh の読み出しと、Store に置く通話の記録。 |
| `internal/simservs` | simservs と MMTelServices の文書、その規則（RFC 4745）、XCAP のノードセレクタ、規則の評価。 |
| `internal/xcap` | Ut の XCAP サーバー、HTTP Digest、UE のクライアント。 |
| `internal/ue/xcap.go`、`cmd/ue/xcap.go` | `ue xcap` と UE の補足サービス。 |
| `internal/scscf/services.go` | P-Served-User、ODI、ORIGINATING_CDIV の条件。 |
| `internal/scscf/session.go` | 登録していない着信者のサービスと、S-CSCF が着信者の UE に渡さない識別子。 |
| `internal/pcscf/identity.go` | P-CSCF が着信者の応答で示す識別子と、発信者の UE に渡さない識別子。 |
| `internal/hss`、`deploy/compose/hss.yaml` | repository data としてのサービスのデータと Ut のパスワード、4 つの条件。 |
| `internal/mmtel/network_test.go` | すべてのサービスを、IMS 全体ごと 1 つのプロセスで動かす試験。 |

## このプロジェクトで簡略化していること

各項目には、コードに `SIMPLIFIED:` のコメントがあります。`grep -rn "SIMPLIFIED:" internal cmd` ですべてを一覧できます。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| AS、TS 24.604 4.5.2.6.2.3 | History-Info の最後の項目に理由（cause-param）があれば、served user と比べずに served user の項目とみなす。 | この網では、転送した要求が転送先の利用者の AS に届くのはその場合だけ。比べるには利用者の別名の集合が要る。 |
| AS、TS 24.604 4.5.2.6.4 | 181 で音声ガイダンス（TS 24.628）を流さない。 | AS は 181 のあとすぐに呼を転送先へ進める。先に音声ガイダンスを流すには、発信者のメディアを MRFP に向けてから転送先に向け直す必要がある。その early dialog は、転送のときに 199 で終わることがある。 |
| AS、TS 29.364 8.1 | AS はサービスのデータの変更を購読せず、通話ごとに読み直す。 | そうすれば Sh-Subs-Notif も通知も要らない。 |
| AS、TS 24.611 4.9.1.4 NOTE 3 | served user はホーム網にいるものとみなすので、BIC-Roam の条件は成り立たない。 | ローミング中かどうかは利用者の登録から分かるが、AS はそれを受け取らない。 |
| AS、RFC 3262 5 | offer の無い INVITE が AS の先でフォークしたとき、発信者は最初の early dialog の offer に answer する。AS は、ほかの early dialog の offer を、発信者との early dialog の UPDATE で発信者に offer しない。信頼できる暫定応答に入ったその offer には answer が返らず、その応答には PRACK が返らない。その UAS は、5xx で自分の early dialog を終える（RFC 3262 3）。 | そのとき、最初の offer への発信者の answer が、まだ届いていないことがある。また、UPDATE を受け付けない発信者は、200 より前にほかの offer を受け取れない。このプロジェクトのノードは、どれも INVITE をフォークしない。 |
| XCAP、RFC 4825 8 | xcap-caps の文書は認証せずに読ませる。 | Sh は資格情報を公開ユーザー識別子で探すが、それが分かるのは利用者のディレクトリの文書だけ。この文書には利用者の情報が無い。 |
| XCAP、RFC 7616 3.4 と 5.5 | 一度見た nonce count に気づかないので、nonce が切れるまでは同じ要求を繰り返されても受け付ける。 | count を保つには、すべてのインスタンスが共有する Store が要る。PUT と DELETE にはいつも If-Match が付くので、文書が変わったあとの繰り返しは 412 になる。 |
| XCAP、TS 24.623 5.2.2 と 5.2.3.1、GSMA IR.92 2.2.2 | GBA（TS 33.222、TS 24.109）と認証プロキシを作らない。 | GBA には BSF と Zh が要り、ここで学ぶには大きすぎる。IR.92 も推奨しているだけ。 |
| XCAP、TS 24.623 5.2.1.2 と 5.2.3.3 | 文書の変更を購読しない（xcap-diff、RFC 5875）。 | 代わりに、UE は変更のたびに文書を読み直す。 |
| XCAP、TS 24.623 5.3.1.3 と 5.3.2.5 | POST によるパスワードの確認と変更、サービスのパスワード管理に対応しない。パスワード管理の下にあるサービスは変えられない。 | IR.92 2.3.9 は網が 5.3.2.5 を省くことを認めている。利用者の認証は Digest でできている。 |
| XCAP、TS 24.623 5.2.3.1 と RFC 4825 8 | Ut は TLS を使わない平文の HTTP にする。 | SIP や Diameter と同じく、既定では信号を暗号化しない方針のため。キャプチャで中身を読めるようにしている。 |
| simservs、TS 24.604 4.9.1.3、TS 24.611 4.9.1.4 | 条件 cp:sphere、cp:validity、ss:presence-status、ocp:external-list、ocp:other-identity を受け付けない。 | IR.92 2.3.8 と 2.3.9 は求めていない。プレゼンスのサーバーもリストのサーバーも無い。 |
| simservs、TS 24.623 5.2.3.4 と 6.2 e | サービスの能力の断片（*-serv-cap の要素）を持たない。 | 変更は、サーバーが受け付けるサービスと条件に照らして確かめる。 |
| simservs、TS 24.611 4.9.1.4 | 番号の国は国番号で決めるので、国番号 1 の北米番号計画の国々は 1 つの国になる。国番号の無い番号は利用者の国とみなす。 | 国ごとの番号計画は対象外。 |
| simservs、TS 24.611 4.9.1.4 | BOIC-exHC のホーム網は、ホーム網の国とみなす。 | 網ごとの番号の範囲を持たない。 |
| simservs、RFC 3261 19.1.4、RFC 3966 4 | 番号は区切りの記号とパラメータを除いて比べ、SIP URI はスキーム、ユーザー、ホストで比べる。 | IMS の網が保証する識別子なら、これで足りる。 |
| simservs、RFC 4745 7.1.3 | ドメインは、パーセントエンコードを戻さず、ToASCII もせずに比べる。 | IMS のドメインは ASCII。 |
| simservs、TS 29.364 7.2.1 | OIP、OIR、TIP、TIR、CDIV、CW、HOLD、CB だけを扱い、文書のほかのサービスは残すが読まない。 | IR.92 2.3 が網に求めるのはこれらのサービス。 |
| UE、GSMA IR.92 2.6 | Ut の User-Agent は、GSMA PRD RCC.07 C.4.1 のとおりには作らず、ただの製品トークンにする。 | このプロジェクトは RCC.07 を読んでいない。 |
| UE、GSMA IR.92 2.3.2 | `ue xcap` は、最後の登録の既定の識別子ではなく、コマンドラインの `-impu` を XUI にする。`ue serve` は既定の識別子を使う。 | `ue xcap` は登録を必要としない。ソフトフォンではどちらも同じ。VoLTE の UE は一時的な識別子で登録するので、補足サービスは `ue serve` で設定する。 |
| S-CSCF、TS 24.229 5.4.3.3 step 12 | History-Info の項目に非通知（RFC 7044 10.1.2）を掛けない。 | 転送した通話は、AS が書いたとおりの転送元の利用者の項目を運ぶ。 |
| S-CSCF、TS 24.229 5.4.3.1 と 5.4.3.2 | 登録している利用者だけを担当する。AS が P-Served-User で利用者を示し、その利用者の代わりに送る要求も、利用者が登録していなければ 403 を返す。ORIGINATING_UNREGISTERED の条件は評価しない。 | この網には、利用者の代わりに要求を始める AS が無く、その条件に当たる要求が来ない。 |
| I-CSCF、TS 24.229 5.3.2.1、5.3.2.1A、5.3.2.2 | 利用者の代わりに送る AS のための「orig」の経路と、選んだ S-CSCF が答えないときの別の S-CSCF を扱わない。 | 利用者の代わりに送るサービスが無く、選び直す冗長な S-CSCF も無い。 |
| HSS、TS 29.228 6.1.4.1 step 2、2a、3 | 公開サービス識別子と IMS restoration を扱わず、Originating-Request の AVP を読まない。 | このプロジェクトの I-CSCF は発信側の要求を経路づけない。 |

## 後片付け

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。
