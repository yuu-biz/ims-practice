# SMS over NAS の調査（CP 層、SGd、S6c）

この文書は、SMS を NAS で届ける方式について、関わる標準が定めることを調べた記録です。EPS では MME が運ぶ方式、5GS では AMF と SMSF を使う方式を扱い、CP 層、SGd、S6c を含みます。調べた内容を元に、この実装の部品 `internal/nas` と `internal/sgd` を作りました。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは版も書きます。

## 読んだ資料

3GPP の仕様は、3GPP のアーカイブから取り出した本文を読みました。

| 仕様 | 版 |
| --- | --- |
| TS 24.011（CP 層と RP 層） | V20.0.0 |
| TS 24.007（L3 のメッセージの形） | V20.0.0 |
| TS 24.301（EPS の NAS） | V20.1.0 |
| TS 24.501（5GS の NAS） | V20.1.0 |
| TS 23.272（Annex C、SMS in MME） | V20.0.0 |
| TS 23.040（SMS の Stage 2。Annex I、Annex K、10 章） | V20.0.0 |
| TS 23.501、TS 23.502（5GS の SMS over NAS） | V20.3.0 |
| TS 23.204（IP-SM-GW） | V19.1.0 |
| TS 29.338（SGd、S6c） | V19.3.0 |
| TS 29.272（S6a）、TS 29.336（S6m）、TS 29.173（SLh） | V19.6.0、V20.0.0、V19.0.0 |
| TS 29.229（Cx）、TS 29.329（Sh）、TS 29.230（AVP の一覧） | V19.1.0、V19.1.0、V19.3.0 |
| TS 23.003（識別子） | V20.1.0 |
| RFC 6733（Diameter） | 2012 年 10 月 |

Wireshark は 4.2.2 の tshark と text2pcap を実際に動かしました。ソースは v4.2.2 の `packet-nas_eps.c` と `packet-nas_5gs.c` を読みました。

## 層の重なり

SMS over IP では、RP 層のメッセージがそのまま SIP の MESSAGE の本文になります（TS 24.341）。SMS over NAS では、RP 層の下に CP 層が入り、その CP 層のメッセージを NAS のメッセージが運びます。

```text
EPS                               5GS
UPLINK NAS TRANSPORT (TS 24.301)  UL NAS TRANSPORT (TS 24.501)
  CP-DATA (TS 24.011 7.2.1)         CP-DATA
    RP-DATA (TS 24.011 7.3.1)         RP-DATA
      SMS-SUBMIT (TS 23.040)            SMS-SUBMIT
```

RP 層と TP 層は SMS over IP と同じです。`internal/sms` の符号化はそのまま使えます。SMS over NAS で新しく要るのは、CP 層、NAS のメッセージ、網の中の SGd と S6c です。

## CP 層（TS 24.011 V20.0.0）

CP 層は CM 副層の手順で、SMC（Short Message Control）という実体が受け持ちます（5.1）。UE の相手は、EPS で SMS in MME を使うときは MME、5GS では SMSF です（5.2.4.1）。AMF は CP 層を見ません。

### メッセージ

メッセージは 3 つです（7.2）。

| メッセージ | 種別の値（8.1.3） | 中身 |
| --- | --- | --- |
| CP-DATA | 0x01 | CP-User data。RPDU を 1 つ運ぶ（7.2.1） |
| CP-ACK | 0x04 | なし。CP-DATA が届いたことだけを伝える（7.2.2） |
| CP-ERROR | 0x10 | CP-Cause。CP 層の異常を伝える（7.2.3） |

- 1 オクテット目は、上位 4 ビットが Transaction Identifier（TI）、下位 4 ビットが Protocol Discriminator です（8.1.1、図 8.1）。SMS の PD は 1001 です（TS 24.007 11.2.3.1.1、表 11.2）。
- TI は、ビット 8 が TI flag、ビット 7 から 5 が TI value です（TS 24.007 11.2.3.1.3）。TI value はトランザクションを始めた側が選びます。TI flag は、始めた側が送るメッセージで 0、相手が送るメッセージで 1 です。UE と網が同じ値を選んでも、flag で区別できます。
- TI value の 111 は TI の拡張のための値で、CP 層は使いません。受けた側は無視します（TS 24.011 9.2.2）。
- CP-DATA の CP-User data は LV の形で、RPDU は最大 248 オクテットです（8.1.4.1）。メッセージの中では IEI を付けません（表 7.1）。
- CP-ERROR の CP-Cause は 1 オクテットの V の形で、下位 7 ビットが原因です（8.1.4.2）。表 8.2 の値は 17、22、81、95、96、97、98、99、111 で、表に無い値は 111 として扱います。

### SMC の状態

EPS と 5GS の SMC（SMC-EP、SMC-5G）の状態は 5.2.2（MS 側）と 5.2.4（網側）にあります。回線交換の SMC とは状態が違います。

| 側 | 向き | 状態 |
| --- | --- | --- |
| MS | MO | MO-Idle（0）、MO-Wait for CP-ACK（2）、MO-Wait for CP-Data（3）、MO-EMM-connection pending（4、S1 モードだけ） |
| MS | MT | MT-Idle（0）、MT-Wait for RP-ACK（1）、MT-Wait for CP-ACK（2） |
| 網 | MO | MO-Idle（0）、MO-Wait for RP-ACK（1）、MO-Wait for CP-ACK（2） |
| 網 | MT | MT-Idle（0）、MT-Wait for CP-ACK（1）、MT-Wait for CP DATA（2） |

Annex B に SDL の図がありますが、図は本文に変換できなかったので読めていません。

### 流れ

MO の 1 通は、NAS のメッセージ 4 つで運びます（5.3.2.2）。

1. UE が CP-DATA（RP-DATA）を送り、TC1\* を始めて MO-Wait for CP-ACK に入ります。
2. 網が CP-ACK を返します。RPDU は RP 層へ渡します。
3. SC の答えが出ると、網が CP-DATA（RP-ACK か RP-ERROR）を送ります。
4. UE が CP-ACK を返します。

MT は向きが逆になるだけで、同じ 4 つです。網が CP-DATA（RP-DATA）を送り、UE が CP-ACK と CP-DATA（RP-ACK）を返し、網が CP-ACK で終えます。

- UE がアイドルのときは、先に NAS の信号用の接続を作ります。S1 モードでは、SMC が EMM に接続を頼み、できるまで MO-EMM-connection pending で待ちます（5.3.2.2）。
- TC1\* が切れると CP-DATA を送り直します。送り直しの回数は実装が 1、2、3 のどれかに決めます。それでも CP-ACK が来なければ、RP 層に誤りを伝えてアイドルに戻ります（5.3.2.2）。
- TC1\* の値は仕様に数字がありません。CP-DATA の長さに応じて決めるとだけ書いてあります（5.3.2.2、10 章）。
- 続けて送るときは、UE は最後の CP-ACK を送らずに、次の CP-DATA を別の TI value で送れます。受けた側は、別の TI の CP-DATA を、待っていた CP-ACK の代わりとして扱います（5.4）。

### 異常系

- CP-ERROR を受けた SMC は、RP 層に誤りを伝えてアイドルに戻ります。送った側もアイドルに戻ります（5.3.4、9.2）。
- メッセージ種別を読めないほど短いメッセージは無視します（9.2.1）。
- 知らないメッセージ種別には、原因 97 の CP-ERROR を返します（9.2.3）。状態に合わないメッセージには原因 98 です。
- 必須の情報要素の誤りには、原因 96 の CP-ERROR を返します。ただし、誤りのある CP-ERROR には CP-ERROR を返さず、無視します（9.2.4）。
- 進行中でないトランザクションへの CP-ACK には、原因 81 の CP-ERROR を返します（9.2.2）。
- 9.2 の手順は MS には必須で、網には任意です（9.1）。

### RP 層との関係

- RP 層の実体は SMR で、CP 層の上にあります。MS 側のタイマーは TR1M が 35 秒より長く 45 秒より短い値、TRAM が 25 秒より長く 35 秒より短い値、TR2M が 12 秒より長く 20 秒より短い値です（10.1）。網側には TR1N と TR2N があります（6.2.2）。
- CP-ACK は CP-DATA が届いたことしか伝えません。SC が受け付けたかどうかは、あとから来る RP-ACK か RP-ERROR で分かります。

## EPS の NAS（TS 24.301 V20.1.0）

### メッセージ

SMS を運ぶのは「NAS メッセージの転送」の手順です（5.6.3）。UE からは UPLINK NAS TRANSPORT（8.2.30）、MME からは DOWNLINK NAS TRANSPORT（8.2.12）を使います。

| オクテット | 中身 | 根拠 |
| --- | --- | --- |
| 1 | 上位 4 ビットが Security header type、下位 4 ビットが Protocol discriminator（0111、EMM） | 9.2、9.3.1 |
| 2 | Message type。UPLINK は 0x63、DOWNLINK は 0x62 | 9.8、表 9.8.1 |
| 3 以降 | NAS message container。LV の形で 3 から 252 オクテット | 9.9.3.22 |

NAS message container の中身は CP 層のメッセージ 1 つです。中身は 2 から 251 オクテットで、CP-ACK から、最大の RPDU を運ぶ CP-DATA までが入ります。どちらのメッセージにも、任意の情報要素はありません。

### 使える条件

- この手順を使えるのは、UE が EPS と非 EPS のサービス、または EPS と「SMS only」でアタッチしていて、EMM-CONNECTED モードにいるときです（5.6.3.1）。
- アタッチは combined attach です。UE は EPS attach type を「combined EPS/IMSI attach」にし、SMS だけが要るときは Additional update type で「SMS only」を示します（5.5.1.3.2、9.9.3.11、9.9.3.0B）。
- 網は EPS attach result を「combined EPS/IMSI attach」にし、Additional update result で「SMS only」を返します。SMS in MME で提供するときは、どの VLR にも属さない LAI を返します（5.5.1.3.4.2、9.9.3.10、9.9.3.0A）。
- EPS attach result が「EPS only」なら、SMS の分は失敗しています（5.5.1.3.4.1）。
- MT で UE がアイドルのときは、網がページングをします（5.6.2.4）。
- CIoT の最適化を使う UE は、CONTROL PLANE SERVICE REQUEST に最初の SMS を載せられます（5.6.3.1 NOTE 1）。この実装の対象にはしません。

### セキュリティ

- アタッチのセキュリティモード制御が終わると、NAS のメッセージはすべて完全性保護され、4.4.5 の例外を除いて暗号化されます（4.4.2.3、4.4.4.1、4.4.5）。UPLINK と DOWNLINK の NAS TRANSPORT は例外に入りません。
- 保護されたメッセージは、Security header type、MAC（4 オクテット）、Sequence number（1 オクテット）のあとに、平文のメッセージが続きます（9.1、図 9.1.2）。Security header type は、完全性保護だけなら 0001、暗号化もするなら 0010 です（表 9.3.1）。
- 暗号を使わない運用では EEA0 を使います。そのときも Security header type は暗号化ありの値のままです（4.4.5）。
- `internal/nas` は平文（Security header type が 0000）だけを作ります。理由は「設計の候補」に書きます。

## SMS in MME（TS 23.272 V20.0.0 Annex C）

- SMS in MME は、MSC を置かずに EPS の NAS で SMS を届ける構成です（C.1）。UE と E-UTRAN には影響がありません（C.2）。
- MME が SMC と SM-RL の機能を持ちます（C.4.2）。MME は TS 23.040 の流れの中で、MSC と VLR の役をします（TS 23.040 Annex I）。
- MME と SC のあいだが SGd、SC と HSS のあいだが S6c です。どちらも Diameter です（C.3.3、C.3.4）。TS 23.040 の参照点 3 が SGd、参照点 2 が S6c に当たります（TS 23.040 Annex I、表 I.1）。
- MME は S6a の Update Location で、SMS in MME に対応すること、MT SMS の宛先になる自分のアドレス、SMS の登録の希望、UE が「SMS only」を示したかを HSS に伝えます。HSS は MME を MT SMS の宛先として登録し、結果と SMS の加入者データを返します（C.8.1）。Diameter では ULR の SMS-Register-Request と MME-Number-for-MT-SMS、ULA-Flags の「MME Registered for SMS」が当たります（TS 29.272 V19.6.0 5.2.1.1.2、5.2.1.1.3）。
- HSS は MME を MSC の代わりに登録するので、経路の問い合わせには MME の識別子を返します（C.4.3）。
- 届けられなかったとき、MME は SC に失敗を返します。UE に届かなかったのなら MNRF-MME を立て、UE が戻ったら HSS に Ready for SM を送ります（C.6）。
- MO と MT の流れそのものは Annex C に無く、TS 23.040 の 10.1（MT）と 10.2（MO）を、MSC を MME に読み替えて使います（TS 23.040 Annex I）。
- SMS over SGs は MSC が要るので、ロードマップのとおり対象外です。

## 5GS の SMS over NAS

### 構成（TS 23.501 V20.3.0）

- UE と AMF のあいだが N1、AMF と SMSF のあいだが N20、SMSF と UDM のあいだが N21、AMF と UDM のあいだが N8 です（4.4.2.2）。
- SMSF は UE とのあいだで SM-RP と SM-CP を終端し、SC とのあいだで SM を中継します（6.2.13）。AMF は N1 の中身を SMSF に渡すだけです。
- 4.4.2 は「従来の」構成で、SMSF と SC のあいだは MAP か Diameter です（4.4.2.0）。SBI だけで組む構成は TS 23.540 にあり、今回は読んでいません。
- TS 23.040 の Annex K は、5GS でも参照点 3 に SGd、参照点 2 に S6c を使えるとしています。SMSF は MSC と VLR の役をします。
- IMS に対応した UE は、SMS を IP で送るか NAS で送るかを、事業者の設定に従って選びます（5.16.3.8.1）。

### 登録（TS 23.502 V20.3.0 4.13.3.1、TS 24.501 V20.1.0）

1. UE は REGISTRATION REQUEST の 5GS update type で、SMS requested のビットを「SMS over NAS supported」にします（TS 24.501 5.5.1.2.2、9.11.3.9A）。
2. AMF は加入者データで SMS が許されているかを確かめ、SMSF を選びます（TS 23.502 4.13.3.1 step 3）。
3. AMF は SMSF に Nsmsf_SMService_Activate を送ります。SMSF は UDM に自分を登録します（step 5 から 7）。
4. AMF は SMSF の答えを受けてから、REGISTRATION ACCEPT の 5GS registration result で、SMS allowed のビットを「SMS over NAS allowed」にします（TS 24.501 5.5.1.2.4、9.11.3.6）。

### メッセージ（TS 24.501 V20.1.0）

SMS を運ぶのは NAS transport の手順です（5.4.5）。UE からは UL NAS TRANSPORT（8.2.10）、AMF からは DL NAS TRANSPORT（8.2.11）を使います。

| オクテット | 中身 | 根拠 |
| --- | --- | --- |
| 1 | Extended protocol discriminator。5GMM は 0x7E | 9.2、TS 24.007 11.2.3.1.1A |
| 2 | 下位 4 ビットが Security header type、上位 4 ビットは予備 | 9.3、9.5 |
| 3 | Message type。UL は 0x67、DL は 0x68 | 9.7、表 9.7.1 |
| 4 | 下位 4 ビットが Payload container type（SMS は 0010）、上位 4 ビットは予備 | 9.11.3.40、9.5 |
| 5 以降 | Payload container。LV-E の形で、長さは 2 オクテット | 9.11.3.39 |

- Payload container type が SMS のとき、Payload container の中身は CP-DATA、CP-ACK、CP-ERROR のどれかです（表 9.11.3.39.1）。
- SMS では任意の情報要素を付けません。PDU session ID などは、ほかの種類の中身のためのものです（8.2.10.2）。
- AMF は UL NAS TRANSPORT の中身を、その UE の SMSF に渡します（5.4.5.2.3）。SMSF が無いか、渡せないときは、手順をやめます（5.4.5.2.5 の b）。
- SMSF から SMS を受けた AMF は、Payload container type を SMS にして DL NAS TRANSPORT を送ります（5.4.5.3.2）。UE は中身を SMS の実体に渡します（5.4.5.3.3）。
- セキュリティは EPS と同じ考え方です。5G NAS のセキュリティコンテキストができたあとは、完全性保護と暗号化が必須です（4.4.4.1、4.4.5）。保護されたメッセージは、EPD、Security header type、MAC、Sequence number のあとに平文のメッセージが続きます（9.1.1、図 9.1.1.2）。

### 流れ（TS 23.502 V20.3.0）

- MO（4.13.3.3）。UE が UL NAS TRANSPORT で CP-DATA を送ります。AMF は Nsmsf_SMService_UplinkSMS で SMSF に渡します。SMSF は Namf_Communication_N1N2MessageTransfer で CP-ACK を返し、SC に SM を送ります。SC の答えは、同じ道で submit report として UE に戻ります。
- MT（4.13.3.6、4.13.3.7）。SC は UDM に経路を問い合わせ、SMSF に SM を送ります。SMSF は Namf_MT_EnableUEReachability で AMF に UE を呼び出させてから、N1N2MessageTransfer で CP-DATA を送ります。UE の delivery report は UplinkSMS で SMSF に戻ります。
- 届けられなかったとき、SMSF は SC に失敗を返します（4.13.3.9）。

## SGd と S6c（TS 29.338 V19.3.0）

### 共通の決まり（4 章）

- どちらも 3GPP のベンダ固有のアプリケーションです。S6c は 16777312、SGd は 16777313 です（4.8）。
- セッションの状態は持ちません。要求にも応答にも Auth-Session-State を NO_STATE_MAINTAINED で入れます（4.5）。
- トランスポートは SCTP です（4.6）。このプロジェクトの Diameter は TCP なので（Docker Desktop では SCTP が使えない場合があるためです）、つなぐときに `SIMPLIFIED:` が要ります。
- Vendor-Specific-Application-Id は、どのコマンドにも入れません（5.3.2.2、6.3.2.2）。Cx と Sh は入れるので、ここが違います。
- 独自の結果は Experimental-Result に入れ、Result-Code は付けません（7.3.1）。

### コマンド

| インタフェース | コマンド | コード | 範囲 |
| --- | --- | --- | --- |
| SGd | MO-Forward-Short-Message（OFR、OFA） | 8388645 | 作った |
| SGd | MT-Forward-Short-Message（TFR、TFA） | 8388646 | 作った |
| S6c | Send-Routing-Info-for-SM（SRR、SRA） | 8388647 | 作った |
| S6c と SGd | Alert-Service-Centre（ALR、ALA） | 8388648 | 作らない |
| S6c | Report-SM-Delivery-Status（RDR、RDA） | 8388649 | 作らない |

コードは表 5.3.2.2/1 と表 6.3.2.2/1 にあります。ALR と RDR は、HSS の Message Waiting Data を保つためのものです。SC が SM を蓄えて送り直すときに要ります。今の SC は蓄積も再送もしないので（[sms.md](../design/sms.md)）、範囲から外しました。入れるかどうかは「未決の論点」に書きます。

### SGd の中身

SGd が運ぶのは RP 層のメッセージではなく、その部品です。ここが `internal/nas` との違いです。

| RP-DATA の部品 | OFR（MO、6.3.2.3） | TFR（MT、6.3.2.5） |
| --- | --- | --- |
| RP-Destination Address | SC-Address（SC の番号） | User-Name（受信者の IMSI） |
| RP-Originator Address | User-Identifier（送信者の IMSI と MSISDN） | SC-Address（SC の番号） |
| RP-User-Data | SM-RP-UI（TPDU） | SM-RP-UI（TPDU） |
| RP-Message Reference | 運ばない | 運ばない |

- SM-RP-UI は TPDU で、最大 200 オクテットです（6.3.3.3）。tshark も SM-RP-UI を TPDU としてデコードします。
- SC-Address は SC の E.164 番号を TBCD で入れます。番号計画などのオクテットは付けません（6.3.3.2）。
- TFR は Destination-Host が必須で、SRA が返したノードを指します（6.3.1.2）。MME に送るときは MME-Number-for-MT-SMS を付けます（表 6.2.2.1/1）。
- 応答の SM-RP-UI は任意で、SMS-SUBMIT-REPORT か SMS-DELIVER-REPORT が入ります（表 6.2.1.1/2、表 6.2.2.1/2）。
- OFA の誤りは FACILITY_NOT_SUPPORTED と SM_DELIVERY_FAILURE です（6.2.1.3）。TFA の誤りは USER_UNKNOWN、ABSENT_USER、USER_BUSY_FOR_MT_SMS、ILLEGAL_USER、ILLEGAL_EQUIPMENT、SM_DELIVERY_FAILURE、FACILITY_NOT_SUPPORTED です（6.2.2.2）。
- SM_DELIVERY_FAILURE のときは SM-Delivery-Failure-Cause が必須です。原因は 0 から 6 の列挙で、0 から 2 は UE の側、3 から 6 は SC の側の原因です（6.3.3.5、6.3.3.6）。
- 結果の値は 7.3 にあります。5001 と、5550 から 5558 です。
- 1 章は、SGd を MME と SMSF のインタフェースとしています。手順の本文（6.2）は MME、SGSN、IP-SM-GW について書かれていて、SMSF だけの手順はありません。SMSF は MME と同じに扱うと読みました。

### S6c の中身

- SRR は MSISDN と SC-Address を運びます（表 5.2.1.1-1）。コマンドの形の上では、どちらも必須ではありません（5.3.2.3）。
- SRA は、成功のときに User-Name（IMSI）と Serving-Node を返します（表 5.2.1.1-2）。
- Serving-Node は、MME なら MME-Name、MME-Realm、MME-Number-for-MT-SMS の組、IP-SM-GW なら IP-SM-GW-Number と IP-SM-GW-Name です（5.3.3.6）。
- SMSF は SMSF-3GPP-Address で返します。中は SMSF-3GPP-Number、SMSF-3GPP-Name、SMSF-3GPP-Realm です（5.3.3.35）。HSS がこれを返すのは、要求の側が Supported-Features で SMSF-Support（Feature-List-ID 1 のビット 0）を示したときだけです（5.3.3.13、表 5.2.1.1-2 の NOTE）。
- HSS の誤りは USER_UNKNOWN、SERVICE_NOT_SUBSCRIBED、SERVICE_BARRED、ABSENT_USER です（5.2.1.3）。MT SMS の宛先が 1 つも登録されていないときが ABSENT_USER です。
- 利用者に IP-SM-GW が登録されていると、HSS は SRR をその IP-SM-GW に転送します（5.3.1.1.3）。
- MME-Realm と MME-Number-for-MT-SMS は、元の仕様では M ビットを立てませんが、S6c では立てます（表 5.3.3.1/2）。SGd の MME-Number-for-MT-SMS は元のままです（表 6.3.3.1/2）。

### 気づいた食い違い

- SMSMI-Correlation-ID の AVP コードは、表 6.3.3.1/1 では 3324、6.3.3.13 の AVP の形では 3308 です。3308 は SM-RP-MTI のコードです。今回は使いません。
- Serving-Node の AVP の形（5.3.3.6）には IP-SM-GW-Realm がありますが、許される組み合わせの一覧には出てきません。`internal/sgd` は 3 つとも任意で扱います。

## IP-SM-GW のドメイン選択（TS 23.204 V19.1.0）

ロードマップが挙げている 6.4 と 6.5a を確かめました。6.4 は IP-SM-GW を通る着信の流れ、6.5a は届かなかったときの流れで、ロードマップの引き方は合っています。

- IP-SM-GW の機能に、届けるドメインの選択と、HSS から MSC、SGSN、MME、SMSF のアドレスを得ることが入っています（5.3.1.1）。
- IP-SM-GW は SC から見ると MSC、SGSN、MME、SMSF のどれかに見えます。SC とは E、Gd、Gdd、SGd でつなぎます（5.2.4）。HSS とのあいだには、J か S6c が必須です（5.2.3）。
- 着信の流れ（6.4）。
  1. SC が HSS に経路を問い合わせます。HSS は問い合わせを IP-SM-GW に転送します（step 3a）。
  2. IP-SM-GW は自分で HSS に問い合わせ、IMSI と、MME や SMSF のアドレスを得ます（step 3b）。
  3. IP-SM-GW は MT Correlation ID を作り、自分のアドレスだけを SC に返します（step 3c）。MT Correlation ID は IMSI の場所に入れる値です（TS 23.040 3.2.7a）。
  4. SC は SM を IP-SM-GW に送ります（step 4）。
  5. IP-SM-GW は、事業者の方針と利用者の設定に従って、届けるドメインを選びます。選び方は実装に任されています（step 5）。
- 届かなかったとき（6.5a）。
  - IP-SM-GW は、優先順の次のドメインで届け直します。例の順は IMS、SGSN、MSC、MME、SMSF です。順は事業者の方針で決めます。
  - UE のメモリがいっぱいで失敗したときは、ほかのドメインを試さずに、失敗を SC に返します（step 4）。
  - すべて失敗したら、失敗を SC に返し、HSS に Report SM Delivery Status を送ります（step 16、17）。
  - SC のタイマーが短いと、全部のドメインを試す時間がありません（NOTE 1）。
- UE が IMS に登録していないときは、IP では送りません。方針に従って CS か PS で送るか、誤りを返します（6.5b）。この実装で目指す「IMS に登録していない UE に宛てた SMS で NAS に切り替える」は、これに当たります。

## Wireshark で確かめたこと（tshark 4.2.2）

### NAS

`internal/nas` が作ったバイト列を、次の方法で tshark に読ませました。どれでも、NAS、CP 層、RP 層、TPDU までデコードできました。Malformed は出ませんでした。

| 包み方 | 対象 | 設定 | 結果 |
| --- | --- | --- | --- |
| text2pcap の `-l 252 -P nas-eps`（Exported PDU） | EPS | 不要 | デコードできた |
| text2pcap の `-l 252 -P nas-5gs` | 5GS | 不要 | デコードできた |
| UDP 4729 番の GSMTAP v2。種別 0x12（LTE NAS） | EPS | 不要 | デコードできた |
| UDP。ペイロードの先頭に文字列 `nas-eps` | EPS | `--enable-heuristic nas_eps_udp` | デコードできた |
| UDP。ペイロードの先頭に文字列 `nas-5gs` | 5GS | `--enable-heuristic nas_5gs_udp` | デコードできた |
| TCP。2 オクテットの長さのあとに NAS | 5GS | `-d tcp.port==<ポート>,nas-5gs` | デコードできた |
| リンク種別 147（USER0） | 両方 | `-o 'uat:user_dlts:...'` | デコードできた |
| UDP のペイロードが NAS だけ | 両方 | Decode As | できない。UDP の Decode As に nas-eps が無い |

一番簡単なのは Exported PDU です。部品のバイト列を確かめるだけなら、これで足ります。

```sh
# 1 行に 1 メッセージ。行の先頭はオフセットの 000000
echo '000000 07 63 05 09 01 02 06 05' > eps.txt
text2pcap -q -l 252 -P nas-eps eps.txt eps.pcapng
tshark -r eps.pcapng -V

echo '000000 7e 00 67 02 00 05 09 01 02 06 05' > 5gs.txt
text2pcap -q -l 252 -P nas-5gs 5gs.txt 5gs.pcapng
tshark -r 5gs.pcapng -V
```

Compose のキャプチャで見るには、UE とスタブのあいだを実際に流れる形が要ります。先頭に文字列を付けた UDP は、EPS と 5GS の両方で同じ形にでき、付けるのは 7 オクテットだけです。Wireshark のソースでは、この形のためのヒューリスティックが `nas_eps_udp` と `nas_5gs_udp` の名前で登録されていて、既定では無効です。

```sh
# "nas-eps" は 6e 61 73 2d 65 70 73、"nas-5gs" は 6e 61 73 2d 35 67 73
echo '000000 6e 61 73 2d 65 70 73 07 63 05 09 01 02 06 05' > sig.txt
text2pcap -q -u 50000,50001 sig.txt sig.pcapng
tshark -r sig.pcapng --enable-heuristic nas_eps_udp
```

- リンク種別 147 を使うときの設定は `-o 'uat:user_dlts:"User 0 (DLT=147)","nas-eps","0","","0",""'` です。5GS では `nas-eps` を `nas-5gs` にします。
- GSMTAP は設定なしで読めますが、4.2.2 の GSMTAP には 5GS の NAS の種別がありません。5GS の NAS を LTE NAS の種別で入れても、デコードできませんでした。
- 保護されたヘッダも試しました。Security header type が 0001 のものは、EPS でも 5GS でも、そのまま中までデコードできました。0010 のものは、EPS ではそのままデコードでき、5GS では `-o nas-5gs.null_decipher:TRUE` を付けるとデコードできました。MAC は 0 のままでも、tshark は検証しません。
- フィルタの例。`nas-eps.nas_msg_emm_type == 0x63`、`nas-5gs.mm.pld_cont_type == 2`、`gsm_a.dtap.msg_sms_type`、`gsm_a.dtap.cp_cause`、`gsm_a.rp.msg_type`、`gsm_sms.tp-mti`。
- S1AP と NGAP に包む方法は、範囲の外なので試していません。

### Diameter

`internal/sgd` が作った OFR、OFA、TFR、TFA、SRR、SRA を、SCTP と TCP の両方で tshark に読ませました。

```sh
text2pcap -q -S 3868,3868,46 sgd.txt sgd_sctp.pcapng   # SCTP、PPID 46
text2pcap -q -T 3868,3868 sgd.txt sgd_tcp.pcapng       # TCP
tshark -r sgd_sctp.pcapng -V
```

- アプリケーションは「3GPP SGd (16777313)」と「3GPP S6c (16777312)」、コマンドは名前で表示されました。
- 定義した AVP はすべて名前で表示されました。知らない AVP の扱いになったものはありません。Malformed も出ませんでした。
- SM-RP-UI は TPDU（SMS-SUBMIT、SMS-DELIVER、SMS-SUBMIT REPORT、SMS-DELIVER REPORT）としてデコードされました。MSISDN は番号としてデコードされました。
- フィルタの例。`diameter.applicationId == 16777313`、`diameter.cmd.code == 8388645`、`diameter.SM-RP-UI`、`diameter.Serving-Node`。

### 確かめた手順と結果

バイト列は、コミットしない一時的な試験で作りました。試験は、両方のパッケージの `Marshal` と `Message().Serialize()` の結果を 16 進で書き出します。1 行が 1 メッセージで、先頭はオフセットの `000000` です。そのあと、上の text2pcap の行で pcap にし、`tshark -r <pcap> -V` の出力から Malformed、Unknown、Expert Info の行を数えました。

| 入力 | 中身 | フレーム | Malformed | Unknown | Expert Info |
| --- | --- | --- | --- | --- | --- |
| EPS（Exported PDU） | MO と MT の CP-DATA と CP-ACK を合わせて 8 つ、CP-ERROR を 1 つ。UPLINK と DOWNLINK の NAS TRANSPORT に入れた | 9 | 0 | 0 | 0 |
| 5GS（Exported PDU） | 同じ 9 つを、UL と DL の NAS TRANSPORT に入れた | 9 | 0 | 0 | 0 |
| Diameter（SCTP） | OFR と OFA を 2 組、SRR と SRA を 3 組、TFR と TFA を 2 組 | 14 | 0 | 2 | 0 |
| Diameter（TCP） | 同じ 14 | 14 | 0 | 16 | 0 |

- Diameter の Unknown は、列挙の値の名前 `UNKNOWN_SERVICE_CENTRE` と、TCP の `Window size scaling factor: -1 (unknown)` だけでした。AVP の名前が分からなかったものはありません。
- RP-DATA を運ぶ CP-DATA は、RP 層と TPDU（SMS-SUBMIT、SMS-DELIVER）まで、宛先の番号と本文の「hello」も含めてデコードされました。
- コードを直したあとにもう一度バイト列を作り、前と同じバイト列になることを確かめました。

## 作った部品

### `internal/nas`

- CP 層。`CPData`、`CPAck`、`CPError` と、読み取りの `ParseCP`。TI は `TransactionIdentifier`、原因は `CPCause` です。
- EPS。`UplinkNASTransport`、`DownlinkNASTransport` と、それぞれの読み取り。
- 5GS。`ULNASTransport`、`DLNASTransport` と、それぞれの読み取り。Payload container type は `PayloadContainerTypeSMS` だけに名前を付けました。
- RPDU は `[]byte` で受け渡します。`internal/sms` は読み込みません。
- `ParseCP` の誤りが `*MalformedCPMessage` なら、TS 24.011 9.2 が CP-ERROR を返してよいとしている誤りです。返す原因と TI が入っています。それ以外の誤りは、無視するメッセージです。

`SIMPLIFIED:` は次のとおりです。

- 保護された NAS のメッセージは作らず、読みません（TS 24.301 4.4.2.3、4.4.4.1、4.4.5、TS 24.501 4.4.4.1、4.4.5）。
- 5GS の UL と DL の NAS TRANSPORT は、SMS の中身だけを書きます。任意の情報要素は書かず、読むときは飛ばします（TS 24.501 8.2.10、8.2.11、7.6.1）。
- EPS の NAS TRANSPORT で、NAS message container のあとに続くオクテットは読まずに無視します（TS 24.301 7.6.1、TS 24.007 11.2.5）。
- CP 層のメッセージで、最後の情報要素のあとに続くオクテットも、読まずに無視します。TS 24.007 11.2.4 と 11.2.5 では、知らない情報要素があること自体は誤りになりません。TS 24.011 の CP 層のメッセージには、任意の情報要素がありません。

### `internal/sgd`

- SGd。`MOForwardShortMessageRequest` と `Answer`、`MTForwardShortMessageRequest` と `Answer`。
- S6c。`SendRoutingInfoForSMRequest` と `Answer`、`ServingNode`、`SMSF3GPPAddress`、`FeatureList`。
- 辞書は `sgd.xml` と `s6c.xml` で、`LoadDictionary` が go-diameter の既定の辞書に足します。書き方は `internal/diameter/cx.xml` に合わせました。
- 番号は数字だけの文字列で受け渡し、TBCD に直します。SM-RP-UI は TPDU の `[]byte` です。
- 結果の型、要求と応答の先頭の AVP、3GPP のベンダ ID、TBCD の変換は、`internal/diameter` のものを使います。`internal/cx` と `internal/sh` は読み込みません。

`SIMPLIFIED:` は次のとおりです。

- ALR、ALA、RDR、RDA は作りません（TS 29.338 5.2.2、5.2.3、6.2.3）。
- 任意の AVP のうち、構造体に無いものは書かず、読みません（TS 29.338 5.3.2、6.3.2）。パッケージの説明に一覧があります。
- User-Identifier の External-Identifier と LMSI は扱いません（TS 29.336 6.4.2）。
- Serving-Node の SGSN、MSC、SBI の表示は扱いません（TS 29.338 5.3.3.6）。
- SMSF-3GPP-Address の SBI の表示と、SMSF-Non-3GPP-Address は扱いません（TS 29.338 5.3.3.35、5.3.3.36）。

## 未決の論点

SMS over NAS を実装するときに決めることと、有力な案です。どれもまだ決めていません。

| 決めること | 有力な案 | ほかの案 |
| --- | --- | --- |
| NAS を UE とスタブのあいだでどう運ぶか | UDP のペイロードを、文字列 `nas-eps` か `nas-5gs` と NAS のメッセージにする。EPS と 5GS で同じ形になり、tshark は `--enable-heuristic` を付ければ読める | EPS だけ GSMTAP にする（設定なしで読めるが、5GS に使えない）。S1AP と NGAP を作る（本物だが、ASN.1 の符号化が要る）。今の HTTP と JSON に NAS を載せる（Wireshark が読めない） |
| NAS のセキュリティ | 平文のままにし、`SIMPLIFIED:` に書く。EPS AKA、鍵の導出、セキュリティモード制御、EIA と EEA は、SMS の流れを見る目的には大きすぎる | 保護されたヘッダだけを付け、MAC は 0 にする（見た目は実網に近づくが、偽の値を流すことになる）。EPS AKA から作る |
| アタッチと登録 | 今の HTTP と JSON の境界（UE とアクセス網のスタブのあいだ。[volte-access.md](../design/volte-access.md)）に、「SMS only」と「SMS over NAS supported」に当たる項目を足す。NAS のバイト列で流すのは NAS TRANSPORT だけにする | ATTACH REQUEST と REGISTRATION REQUEST も NAS のバイト列で作る |
| SMC の置き場所 | CP 層の状態機械を 1 つのパッケージにし、UE、MME のスタブ、SMSF のスタブで使う。状態は `Store` の後ろに置く（各役割を複数台で動かせるようにするためです）。状態の名前は TS 24.011 の 5.2.2 と 5.2.4 に合わせる | それぞれのノードに書く |
| TC1\* の値と送り直しの回数 | TC1\* を 10 秒、送り直しを 2 回にする。合わせて 30 秒で、TR1M の下限の 35 秒より短い | 仕様に数字が無いので、決めが要る |
| SC | 役割 `sc` にし、SGd のサーバーとクライアント、S6c のクライアントを持たせる。`ipsmgw.ServiceCentre` は SGd のクライアントで実装する（[sms.md](../design/sms.md)） | なし |
| HSS への MME と SMSF の登録 | スタブが HSS の保守用の API で登録し、`SIMPLIFIED:` に書く。本来は S6a の ULR と、N21 の Nudm_UECM_Registration | S6a の ULR と ULA を、SMS に要る AVP だけで作る |
| AMF と SMSF のあいだ（N20） | 5GC のスタブの中の関数の呼び出しにし、`SIMPLIFIED:` に書く | Nsmsf と Namf を HTTP/2 で作る |
| SGd と S6c のトランスポート | 今と同じ TCP にし、`SIMPLIFIED:` に書く。Docker Desktop では SCTP が使えない場合があるためです | SCTP |
| ドメインを選ぶ順 | IMS、MME、SMSF の順を既定にし、設定で変えられるようにする。メモリがいっぱいの失敗では次を試さない（TS 23.204 6.5a step 4） | なし |
| IP-SM-GW が NAS の宛先を知る方法 | IP-SM-GW が S6c の SRR で HSS に聞く（TS 23.204 6.4 step 3b）。IMS に届くかどうかは、今ある reg イベントの状態で分かる | Sh の UDR で聞く。標準に、MME のアドレスを引く Data-Reference があるかは確かめていない |
| ALR と RDR | 入れない。入れると、6.5a の最後（HSS への Report SM Delivery Status）と、UE が戻ったときの再送まで見られるが、SC に蓄積と再送が要り、SC は蓄積も再送もしないという今の設計を広げることになる | 入れる |
| RP 層と SGd の対応 | 上の表のとおりに、MME と SMSF のスタブが RP-DATA を分解して OFR を作り、TFR から RP-DATA を組み立てる。RP-MR はスタブが覚えておく | なし |

ロードマップは、経路の選択に「IP-SM-GW が Sh で HSS に登録した情報」を使うとしています。標準の流れでは、この登録は HSS が SRR を IP-SM-GW に転送するために使われ、MME と SMSF のアドレスは IP-SM-GW が S6c で HSS に聞きます。上の表はこの流れに合わせました。

失敗を UE に返すときの RP-Cause は、今の IP-SM-GW の対応（[sms.md](sms.md) の表）に合わせるのがよいと考えます。TS 29.338 には、結果の値と RP-Cause の対応がありません。

## 作る部品と順序

部品（`internal/nas`、`internal/sgd`）は、どれにも先に要ります。そのあとは次のように分けられます。

| 作るもの | 触る場所 | 先に要るもの |
| --- | --- | --- |
| SMC の状態機械（CP 層の送り直しと異常系） | 新しいパッケージ | `internal/nas` |
| SC を役割にする。SGd のサーバーとクライアント、S6c のクライアント | `internal/sc`、`cmd/ims`、`internal/config` | `internal/sgd` |
| HSS の S6c。MT SMS の宛先の登録と、SRR への答え | `internal/hss` | `internal/sgd` |
| IP-SM-GW を SGd につなぐ。OFR のクライアントと TFR のサーバー | `internal/ipsmgw` | SC の役割 |
| EPC のスタブの SMS in MME。NAS TRANSPORT の受け口、SMC、SGd | `internal/epc` | SMC、SC、HSS の S6c |
| UE シミュレータの NAS の SMS | `internal/ue` | SMC、EPC のスタブの受け口の形 |
| 5GC のスタブの AMF と SMSF | 5GC のスタブ | SMC、SC、HSS の S6c、5GC のスタブ |
| IP-SM-GW のドメイン選択と切り替え | `internal/ipsmgw` | HSS の S6c、IP-SM-GW の SGd、EPC のスタブ |
| 手順書、ラダー図、e2e、pcap の確認 | `docs/guide`、`scripts` | 上のすべて |

- SMC の状態機械は、他のパッケージを読み込まずに作れます。
- SC、HSS の S6c、IP-SM-GW の SGd は、互いに別のパッケージなので、インタフェースを先に決めれば並べられます。
- EPC のスタブと UE の NAS は、NAS の運び方を決めてから始めます。
- 5GC のスタブの AMF と SMSF は、5GC のスタブができてからです。EPS の経路だけを先に通すこともできます。

## 確かめられなかったこと

- TS 29.002（MAP）、TS 29.540（Nsmsf）、TS 29.518（Namf）、TS 23.540（SBI の SMS）、TS 33.401 と TS 33.501（NAS のセキュリティ）は読んでいません。
- TS 24.011 Annex B の SDL の図は、本文に変換できず読めていません。状態の遷移は 5 章の文章から読みました。
- TC1\* の値と、網側の TR1N、TR2N の値。10 章に数字があるのは MS 側のタイマーだけでした。
- SGd の結果の値と RP-Cause の対応。TS 29.338 には見つかりませんでした。MAP との対応を定めた仕様にあると考えますが、確かめていません。
- SMSF が SGd で MME とまったく同じに振る舞うのかどうか。TS 29.338 の 1 章は SMSF を SGd のノードに挙げていますが、手順の本文に SMSF だけの記述がありません。
- TFR を SMSF に送るとき、MME-Number-for-MT-SMS に当たる AVP を付けるのかどうか。
- MT Correlation ID の作り方の詳細。TS 23.040 3.2.7a は、IMSI と同じ形とだけ読みました。
- S1AP と NGAP に包んだときの Wireshark の見え方。実網のキャプチャとは比べていません。
- Wireshark の画面でヒューリスティックを有効にする操作。tshark の `--enable-heuristic` だけを確かめました。
- 実際に暗号化された NAS を tshark がどう見せるか。試したのは、平文の前に保護のヘッダを付けたものだけです。
- GSMA の IR.92 と NG.114 が SMS over NAS をどう求めているか。`www.gsma.com` は読めなかったので、取っていません。TTC の標準も同じです。
- `internal/nas` と `internal/sgd` は、ほかの実装とつないで確かめていません。確かめたのは、自分の読み書きの往復と、tshark のデコードです。
