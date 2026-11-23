# SMS の流れを追う

[English](../en/sms.md)

この手順書では UE 1 から UE 2 へ IMS でショートメッセージ（SMS）を 1 通送り、キャプチャに記録されたメッセージを順に読んでいきます。次のことが分かります。

- 利用者がショートメッセージを受けられることを IP-SM-GW が知るしくみ。
- S-CSCF がショートメッセージを IP-SM-GW に渡す方法。
- メッセージがもう一方の UE に届くまでの流れ。
- 送った側と受けた側が、メッセージがどうなったかを知る方法。

最後に、MESSAGE のボディを 1 オクテットずつ読みます。

この手順書は次の版に沿っています。TS 24.341 V19.0.0 と TS 23.204 V19.1.0（SMS over IP）、TS 24.011 V20.0.0（中継層）、TS 23.040 V20.0.0（転送層）、TS 23.038 V20.0.0（文字の符号化）、TS 24.229 V20.1.0、TS 29.228 V20.0.0（フィルタ条件）、TS 29.328 V20.0.0 と TS 29.329 V19.1.0（Sh）、TS 29.338 V19.3.0（S6c）です。以下の条項番号はこれらの版のものです。

## 始める前に

- [UE が IMS に登録する流れを追う](registration.md) と [音声通話の流れを追う](call.md) を読んでおくこと。ショートメッセージはそこで説明した登録、Path と Service-Route、I-CSCF が利用者の S-CSCF を見つけるしくみをそのまま使います。
- 略語の説明は [用語集](../../glossary.md) にあります。

## SMS over IP の位置づけ

ショートメッセージを端末へ運ぶ方法はいくつかあります。この手順書で扱う IMS では、SIP の MESSAGE のボディに入れて運びます（TS 24.341）。端末とコア網の間の信号である NAS で運ぶ方法もあり、SMS over SGs では MSC を、SMS in MME では MME 自身を、5GS では SMSF を通ります。端末とこれらの各ノードの間の手順は TS 24.011 にあります。この実装は、IMS で運ぶ方法だけを扱います。どの方法でも、中のショートメッセージは同じ 2 つの層でできています。TS 24.011 の中継層（RP）と、TS 23.040 の転送層（TP）です。SIP はそれを運ぶ部分を置き換えているだけです。

## 登場するノード

| ノード | アドレス | SMS での役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 送信者。`+1-202-555-0101`。TS 24.341 では SM-over-IP sender と呼ぶ。 |
| UE 2 | 172.28.0.101 | 受信者。`+1-202-555-0102`。SM-over-IP receiver である。 |
| P-CSCF | 172.28.0.40 | 通話と同じように、UE とその S-CSCF の間で MESSAGE を中継する。 |
| S-CSCF | 172.28.0.30 | 両方の UE を担当する。初期フィルタ条件（iFC）に従って、登録とショートメッセージを IP-SM-GW へ送る。 |
| I-CSCF | 172.28.0.20 | IP-SM-GW が送る MESSAGE が入ってくるホーム網の入口。利用者がどこにいるかを HSS に問い合わせる（LIR）。 |
| IP-SM-GW | 172.28.0.70 | SMS over IP のアプリケーションサーバー（AS）（TS 24.341 5.3.3）。MESSAGE を SC 向けのショートメッセージに変え、逆の向きにも変える。 |
| SC（スタブ） | IP-SM-GW の中 | ショートメッセージを受け取って配達するサービスセンター。ここでは IP-SM-GW のプロセスの中にあるスタブで、Go のインタフェースの後ろにある。 |
| HSS | 172.28.0.10 | iFC を持つ（Cx）。ほかの AS と同じく、IP-SM-GW の問い合わせにも Sh で答える。 |
| DNS | 172.28.0.2 | iFC に書いた AS の名前 `ipsmgw.ims.mnc001.mcc001.3gppnetwork.org` のアドレスを返す。 |

番号がほかに 2 つあります。SC のアドレス `+12025550100` は、各 UE がショートメッセージに自分の SC として書く番号で、UE シミュレータでは `-sc` で指定します。IP-SM-GW は `+12025550199` という番号を持ち、担当する利用者ごとにこの番号を HSS に登録します。番号はすべて、北米番号計画が架空の用途に取ってある 555-0100 から 555-0199 の範囲のものです。

### AS が要る理由と SC の役割

CSCF は SIP を中継するだけです。ショートメッセージには、SIP と SMS の網の両方と話せるノードが要ります。SMS の網では、SC が各メッセージを蓄積して転送します（TS 23.040）。このノードが IP-SM-GW です。実際の網では、IP-SM-GW は SMS-IWMSC や SMS-GMSC を通して、MAP か Diameter で SC とやり取りします（TS 23.204 5.2.4）。このプロジェクトには SMS の網が無いので、SC を IP-SM-GW の中のスタブにしました。IP-SM-GW は `internal/ipsmgw/servicecentre.go` の `ServiceCentre` インタフェースを通して SC を呼びます。本物の SGd のクライアントはここに差し替えられます。スタブは何も蓄積しません。すぐに配達できないショートメッセージは捨て、そのことを status report で伝えます。

IP-SM-GW は Sh（TS 29.328、TS 29.329）で HSS とやり取りします。どの IP-SM-GW が利用者を担当するかを Profile-Update-Request（PUR）で書き込み、SC のスタブは番号を持つ公開ユーザー識別子を User-Data-Request（UDR）で読みます。いつ書き込むかを知るために、IP-SM-GW は P-CSCF と同じように、利用者ごとに登録の状態を購読します。

## S-CSCF が IP-SM-GW を選ぶしくみ

### フィルタ条件

S-CSCF は SMS について何も知りません。どの要求をどの AS へ送るかは、登録のときに SAA と一緒に届くユーザープロファイルで HSS が伝えます。この規則が初期フィルタ条件（iFC）です（TS 29.228 B.2.2、スキーマは Annex E）。iFC に名前を付けてまとめたものがサービスプロファイルで、加入はそのどれか 1 つを受け取ります。HSS のスタブでは、どの加入も `default` というプロファイルを受け取ります。中身は `deploy/compose/hss.yaml` にあり、そのうち SMS over IP に関わるのは次の 2 つの条件です。

```yaml
default_service_profile: default
service_profiles:
  default:
    - priority: 0
      feature: sms
      trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: REGISTER}]}
      application_server: {server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED, service_info: "{msisdn}"}
    - priority: 1
      feature: sms
      trigger_point:
        condition_type_cnf: true
        spt:
          - {group: [0], method: MESSAGE}
          - {group: [1], sip_header: {header: ^Content-Type$, content: '^[Aa][Pp][Pp][Ll][Ii][Cc][Aa][Tt][Ii][Oo][Nn]/[Vv][Nn][Dd]\.3[Gg][Pp][Pp]\.[Ss][Mm][Ss]([[:space:]]*;.*)?$'}}
          - {group: [2], session_case: ORIGINATING_REGISTERED}
      application_server: {server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060}
```

プロファイルの名前は運用者が付けるものです。Cx が運ぶのはプロファイルの中の iFC だけなので、S-CSCF が名前を知ることはありません。

`feature` も運用者が付けるもので、その条件が何を有効にするかを表す言葉です。プロファイルの機能の一覧は、この言葉を重複なく集めたものです。HSS のノードの保守用の API が、プロファイルの名前と並べてこの一覧を返すので、運用者はどのプロファイルが SMS over IP を有効にするかを、条件を 1 つずつ読まなくても分かります。Annex E にもこれに当たる要素は無いので、名前と同じく HSS の中だけに残ります。

どのプロファイルを渡すかは、加入のデータから決めます。加入が `service_profile` にプロファイルの名前を持っていれば、それを渡します。持っていなければ、`test_user` の印が付いている加入には `test_user_service_profile`、ほかの加入には `default_service_profile` が指すプロファイルを渡します。そのため、ある機能を 1 人だけに使わせるときは、その加入にプロファイルの名前を書きます。試験用の利用者だけに使わせるときは `test_user_service_profile` に、全員に使わせるときは `default_service_profile` に、その機能の iFC を持つプロファイルの名前を書きます。HSS は、答えようとしている加入のデータから名前を読むだけなので、加入者が増えても、この処理にかかる手間は変わりません。

SAA では、これが User-Data の AVP に TS 29.228 Annex E のスキーマの XML で入ります。2 つ目の条件は次のとおりです。

```xml
<InitialFilterCriteria>
  <Priority>1</Priority>
  <TriggerPoint>
    <ConditionTypeCNF>true</ConditionTypeCNF>
    <SPT><ConditionNegated>false</ConditionNegated><Group>0</Group><Method>MESSAGE</Method></SPT>
    <SPT><ConditionNegated>false</ConditionNegated><Group>1</Group>
      <SIPHeader><Header>^Content-Type$</Header><Content>^[Aa][Pp][Pp][Ll][Ii][Cc][Aa][Tt][Ii][Oo][Nn]/[Vv][Nn][Dd]\.3[Gg][Pp][Pp]\.[Ss][Mm][Ss]([[:space:]]*;.*)?$</Content></SIPHeader></SPT>
    <SPT><ConditionNegated>false</ConditionNegated><Group>2</Group><SessionCase>0</SessionCase></SPT>
  </TriggerPoint>
  <ApplicationServer>
    <ServerName>sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060</ServerName>
    <DefaultHandling>0</DefaultHandling>
  </ApplicationServer>
</InitialFilterCriteria>
```

要素ごとの意味は次のとおりです。

| 要素 | ここでの値 | 意味 |
| --- | --- | --- |
| `Priority` | 0 と 1 | S-CSCF は値の小さい条件から順に調べる。 |
| `TriggerPoint` | | 条件の本体。トリガーポイントの無い条件は、すべての要求に一致する。 |
| `ConditionTypeCNF` | `true` | サービスポイントトリガー（SPT）を連言標準形で並べる。同じ `Group` の SPT は OR で結び、グループどうしは AND で結ぶ（TS 29.228 B.2.3）。 |
| `Method` の `SPT` | `REGISTER`、`MESSAGE` | SIP のメソッドを調べる。 |
| `SIPHeader` の `SPT` | `Content-Type` が `application/vnd.3gpp.sms` | ヘッダの名前と内容を正規表現で調べる。テキストのチャットなど、ほかのボディの MESSAGE は IP-SM-GW へ行かない。SIP のヘッダ名は大文字と小文字を区別しない（RFC 3261 7.3.1）ので、名前の照合でも区別せず、`content-type` も一致する。内容は書かれたとおりに照合する。メディアタイプにも大文字と小文字の区別はない（RFC 2045 5.1）ので、パターンでは各文字を両方の形で書く。`;` のあとのパラメータも許す。 |
| `SessionCase` の `SPT` | `0`（ORIGINATING_REGISTERED） | 登録済みの利用者が送る要求にだけ一致する。 |
| `ConditionNegated` | `false` | SPT ごとに否定にできる。ここに示した条件では使わない。利用者に宛てた MESSAGE の条件は、あとで述べるように、最後の SPT を否定にしている。 |
| `ApplicationServer` と `ServerName` | `sip:ipsmgw...:5060` | 要求を送る AS。S-CSCF はそのアドレスを DNS で引く。 |
| `DefaultHandling` | `0`（SESSION_CONTINUED） | AS が応答しないときの扱い。AS を飛ばして続ける（SESSION_CONTINUED）か、あきらめる（SESSION_TERMINATED）。 |
| `ServiceInfo` | 利用者の MSISDN（`12025550101` など） | S-CSCF が第三者 REGISTER で AS にそのまま渡す文字列。HSS のスタブは `{msisdn}` を各利用者の MSISDN に置き換える。 |

つまり 1 つ目の条件は「REGISTER はすべて MSISDN を付けて IP-SM-GW へ送る」、2 つ目は「登録済みの利用者が SMS のボディで送る MESSAGE は IP-SM-GW へ送る」という意味です。

3 つ目の条件は、利用者に宛てた MESSAGE（着信側の SessionCase）を対象にします。ただし、IP-SM-GW が自分で送った MESSAGE は除きます。最後の条件が否定になっていて、P-Asserted-Identity が IP-SM-GW の URI でないことを求めるためです。IP-SM-GW は自分で利用者に MESSAGE を送るので、この条件が無いと、送った MESSAGE がすべて IP-SM-GW に戻ってしまいます（TS 23.218 5.2.3 NOTE 5）。この条件に一致するのは、ほかの網から届いた短メッセージです。利用者を担当する IP-SM-GW が受け取り、利用者に届けます（[IMS 網どうしを相互接続する](interconnect.md)）。

### 第三者 REGISTER

登録が成功すると、S-CSCF は、REGISTER に一致する条件を持つ各 AS へ自分で REGISTER を送ります（TS 24.229 5.4.1.7）。この第三者 REGISTER では、Request-URI が AS、From と Contact が S-CSCF、To が利用者です。Expires は UE が 200 OK で受け取った値で、登録の解除では 0 です。条件に ServiceInfo があるので、ボディは `application/3gpp-ims+xml` になり、`<service-info>` にその文字列が入ります（TS 24.229 5.4.1.7A）。

```xml
<?xml version="1.0" encoding="UTF-8"?>
<ims-3gpp version="1"><service-info>12025550101</service-info></ims-3gpp>
```

AS が応答しないか、408 や 5xx を返したときは、条件の DefaultHandling に従います。SESSION_CONTINUED なら登録はそのまま残り、SESSION_TERMINATED なら利用者の登録を解除します（TS 24.229 5.4.1.7）。

### オリジナルダイアログ識別子

MESSAGE が 2 つ目の条件に一致すると、S-CSCF はそれを Request-URI へは送りません。Route の先頭に、AS と、オリジナルダイアログ識別子（ODI）を付けた自分の URI の 2 つを入れます（TS 24.229 5.4.3.2 step 5 a、5.4.3.4）。

```text
Route: <sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>
Route: <sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr;odi=W46DU74CLAQFEFRO3XYECCE5TS>
```

プロキシとして動く AS はこの Route に沿って要求を送り返します。S-CSCF は ODI を見て、最初からではなく次の条件から評価を続けます（5.4.3.2 step 3）。IP-SM-GW はプロキシではなく、UA として MESSAGE を終端するので（TS 24.229 5.7.2）、要求は戻ってきません。それでも S-CSCF は、AS が何をするかを知らないので ODI を付けます。ODI はランダムな値で、続きの評価に要る状態は S-CSCF の `Store` に一定の時間だけ置きます。同じ Store を使うほかの S-CSCF のノードも、戻ってきた要求を引き継げます。

S-CSCF は P-Served-User（RFC 5502）も入れます。AS が担当する利用者を、session case と、その利用者が登録しているかどうかとあわせて示すヘッダです（TS 24.229 5.4.3.2 step 5 b）。

```text
P-Served-User: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;sescase=orig;regstate=reg
```

MMTel AS（[MMTel AS の付加サービス](mmtel.md)）のように発信者と着信者の両方を担当する AS は、このヘッダで場合を見分けます（5.7.1.3A）。利用者の着信側のサービスへ送る要求では `sescase=term` になります。S-CSCF は要求を宛先や UE へ送る前に P-Served-User を取り除き（5.4.3.2 step 14A、5.4.3.3 step 13A）。UE が入れた P-Served-User は S-CSCF に届きません。P-CSCF が信頼のドメインの境界で取り除くからです（TS 24.229 4.4.9）。

IP-SM-GW が Timer F（32 秒）のあいだに MESSAGE に応答しないか、408 や 5xx を返したときは、条件の DefaultHandling に従います（TS 24.229 5.4.3.2）。SESSION_CONTINUED なら、S-CSCF は MESSAGE を Request-URI、つまり SC の tel URI へ送ります。この番号には ENUM のレコードが無いので、その番号への通話と同じように、S-CSCF は 404 を返します（step 10）。

## ショートメッセージを送る

この節の操作は、すべて `scripts/e2e-sms.sh` が行います。スクリプトは、この手順書で説明するメッセージがキャプチャにあるかを 1 つずつ確かめ、1 つでも無ければエラーで終わります。キャプチャは `captures/` に、そのフレームの一覧は `e2e-out/sms-ladder.txt` に残ります。以下の本文と番号はスクリプトと同じなので、スクリプトのキャプチャはこの手順書のとおりになります。手で実行するときは次のようにします。

1. 登録の手順書と同じように、システムを起動して Diameter の接続を待ちます。

   ```sh
   docker compose up -d --build
   ```

2. 1 つ目の端末で UE 2 を登録し、登録したままにします。UE シミュレータはどのコマンドでも、登録しているあいだはショートメッセージを受けます。

   ```sh
   docker compose run --rm ue2 register \
     -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
     -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
     -password test-password-2
   ```

3. 2 つ目の端末で、UE 1 からショートメッセージを送り、status report を頼みます。フラグは番号と本文より前に書きます。

   ```sh
   docker compose run --rm ue sms -status-report +12025550102 "Hello UE-B, this is UE-A."
   ```

   UE 1 は登録し、メッセージを送り、`"state":"accepted"` を付けた `"msg":"submit report"` をログに出します。続いて status report を待ち、`"status":0` を付けた `"msg":"status report"` を出して、登録を解除します。UE 2 は `"from":"+12025550101"` と `"text":"Hello UE-B, this is UE-A."` を付けた `"msg":"sms received"` をログに出します。

4. UE 2 を Ctrl+C で止めます。UE 2 は登録を解除します。

本文は GSM 7 ビットのアルファベットなら 160 文字まで、日本語のように UCS2 が要るなら 70 文字まで書けます。スクリプトは UCS2 のショートメッセージを逆の向きに、UE 2 から UE 1 へ送ります。1 つ目の端末で `docker compose run --rm ue register` を実行し、2 つ目の端末で `docker compose run --rm ue2 sms -impi ... -impu ... -password test-password-2 +12025550101 "こんにちは、UE-A さん"` を実行します。これより長い本文は何も送らずに断ります。

失敗の場合も試せます。どちらも、ほかの UE は要りません。

- **番号が無い。** `docker compose run --rm ue sms +12025550198 "Nobody has this number."`。この番号を持つ加入者はいません。submit report は RP-ERROR になり、`ue sms` は `the SC refused the short message: RP-Cause 1, TP-FCS 0xc3` で終わります。
- **受信者が登録していない。** `docker compose run --rm ue sms -status-report +12025550103 "Are you there?"`。加入者 3 は存在しますが、登録しません。SC はメッセージを受け付けますが、配達は 480 で失敗します。UE 1 は `"status":98`（TP-ST の 0x62）を付けた `"msg":"status report"` をログに出します。

同じことはラボでもできます。[ラボの手順書](lab.md) の「UE シミュレータを操作する」を見てください。UE のカードからショートメッセージを送れ、各 UE が送ったものと受けたものが一覧に出ます。MESSAGE の詳細には、デコードしたショートメッセージが表示されます。

## キャプチャを開く

`captures/` の一番新しいファイルを Wireshark で開き、次の表示フィルタを使います。

```text
sip.CSeq.method in {"MESSAGE", "REGISTER"} || (sip && ip.addr == 172.28.0.70) || diameter.cmd.code in {302, 306, 307}
```

REGISTER（第三者 REGISTER を含む）、MESSAGE とその応答、IP-SM-GW の SUBSCRIBE と NOTIFY、LIR、UDR、PUR とその応答が表示されます。MESSAGE のボディは Wireshark が自分でデコードします。Info の列の最後に `(RP) RP-DATA (MS to Network)` のような RP のメッセージが表示され、パケットの詳細には`GSM A-I/F RP` の木と、その中の `GSM SMS TPDU` の木があります。**Statistics > Flow Graph** で **Limit to display filter** を選ぶと、ラダー図を描けます。

## IP-SM-GW が利用者の登録を知る

この流れは [登録の手順書](registration.md) のステップ 15 から 18 で詳しく説明しています。ここでは、ショートメッセージにとっての意味を説明します。

```text
 UE 2      P-CSCF     I-CSCF     S-CSCF    IP-SM-GW     HSS
  |          |          |          |          |          |
  |  REGISTER, 401, REGISTER,      |          |          |
  |  200 OK (R1)                   |          |          |
  |<==============================>|          |          |
  |          |          |          | REGISTER (R2)       |
  |          |          |          |--------->|          |
  |          |          |          |  200 OK  |          |
  |          |          |          |<---------|          |
  |          |          |  SUBSCRIBE (R3)     |          |
  |          |          |<--------------------|          |
  |          |          |  LIR, LIA           |          |
  |          |          |<------------------------------>|
  |          |          |--------->|          |          |
  |          |          |  200 OK  |          |          |
  |          |          |<---------|          |          |
  |          |          |-------------------->|          |
  |          |          |          | NOTIFY (R4)         |
  |          |          |          |--------->|          |
  |          |          |          |  200 OK  |          |
  |          |          |          |<---------|          |
  |          |          |          |          | PUR (R5) |
  |          |          |          |          |--------->|
  |          |          |          |          |  PUA     |
  |          |          |          |          |<---------|
```

### R1. UE が SMS over IP の機能タグを付けて登録する

登録は登録の手順書のとおりで、違いは 1 つだけです。REGISTER の Contact に機能タグ `+g.3gpp.smsip` が付いています（TS 24.341 5.3.2.2、RFC 3840）。この連絡先が IP でショートメッセージを受けられることを示すタグで、GSMA の IR.92 はすべての VoLTE 端末にこれを求めています。S-CSCF は連絡先のパラメータを覚えておき、NOTIFY の reginfo に載せ（R4）、あとでショートメッセージの送り先を選ぶときにも使います（下の手順 9）。

### R2. S-CSCF が第三者 REGISTER を送る

200 OK を返した直後に、S-CSCF は前に説明した第三者 REGISTER を IP-SM-GW へ送ります（TS 24.229 5.4.1.7、5.4.1.7A）。`<service-info>` には利用者の MSISDN が入っています。IP-SM-GW は Contact に登録を並べた 200 OK を返します（TS 24.341 5.3.3.2、RFC 3261 10.3。TS 24.341 の表 B.3-2 の例も同じです）。MSISDN は R3 の購読と一緒に保存し、R5 の PUR で利用者を示すのに使います。

IP-SM-GW のほかの応答と同じく、この 200 OK は要求の P-Charging-Vector を繰り返し、term-ioi を足します（TS 24.229 5.7.2）。

### R3. IP-SM-GW が reg イベントを購読する（SUBSCRIBE）

UE が `+g.3gpp.smsip` を登録したかどうかは、第三者 REGISTER からは分かりません。そこで IP-SM-GW は利用者の reg イベントパッケージを購読します（TS 24.341 5.3.3.2、TS 24.229 5.7.1.1）。Event は `reg` です。Expires は 3761 で、登録の 3600 秒に、RFC 3680 4.4 が 2 つの既定値の間に置いた 161 秒を足した値です。P-Asserted-Identity には自分の URI を入れます。S-CSCF は、これが利用者の iFC の AS なので購読を受け付けます（TS 24.229 5.4.2.1.1）。IP-SM-GW は自分から要求を始める AS なので、SUBSCRIBE に新しい icid-value と orig-ioi を持つ P-Charging-Vector を付けます（TS 24.229 5.7.3）。

IP-SM-GW のほかの要求と同じく、SUBSCRIBE はホーム網の入口の I-CSCF へ送り、I-CSCF が LIR で S-CSCF を探します（下の手順 6 を参照）。購読の状態は IP-SM-GW の `Store` に置くので、NOTIFY は IP-SM-GW のどのノードが受けても処理できます。

### R4. S-CSCF が状態を知らせる（NOTIFY）

S-CSCF はすぐに、IP-SM-GW へ直接 NOTIFY を送ります。SUBSCRIBE への 200 OK は I-CSCF を通って戻るので、キャプチャでは NOTIFY がその前にも後にも来ることがあります。購読のダイアログを作るのは、200 OK ではなく NOTIFY です（RFC 6665 4.4.1）。reginfo には利用者の連絡先が並び、連絡先の機能タグは `<unknown-param>` の要素で示されます（TS 24.229 5.4.2.1.2）。

```xml
<registration aor="sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org" id="reg1" state="active">
  <contact id="LJDTDE6YNM" state="active" event="registered" expires="3600" ...>
    <uri>sip:ab6af11f-a43e-860e-9064-207615ae1f12@172.28.0.101:5060</uri>
    <unknown-param name="+g.3gpp.smsip"></unknown-param>
  </contact>
</registration>
```

IP-SM-GW は 200 OK を返し、`+g.3gpp.smsip` を持つ active の連絡先が利用者にあるかを調べます（TS 24.341 5.3.3.2）。

### R5. IP-SM-GW が HSS に自分を登録する（PUR と PUA）

そのような連絡先があるので、IP-SM-GW は、これからこの利用者のショートメッセージを自分が配達することを HSS に伝えます。TS 24.341 5.3.3.2 と TS 23.204 6.1 では MAP の更新（TS 29.002）で行いますが、このプロジェクトでは代わりに Sh の更新を使います（TS 29.328 6.1.2）。PUR（コマンド 307）は利用者を R2 の MSISDN で示し、Data-Reference に 24（SMSRegistrationInfo、TS 29.328 7.6.16）を使い、Sh-Data の `<IP-SM-GW-Number>` に IP-SM-GW の番号 `12025550199` を入れます。HSS は DIAMETER_SUCCESS（2001）の PUA で答えます。

IP-SM-GW が PUR を送るのは、そのような連絡先があるかどうかが NOTIFY で変わったときだけです。`+g.3gpp.smsip` を付けずに登録した UE には PUR が送られないので、HSS にはその利用者の IP-SM-GW が登録されません。

### 再登録と登録の解除

再登録のたびに、第三者 REGISTER がまた届きます。IP-SM-GW はそのたびに、購読のダイアログの中で S-CSCF へ直接 SUBSCRIBE を送って購読を更新するので、自分のタイマーは要りません。状態が変わらない NOTIFY では PUR を送りません。

UE が登録を解除すると、S-CSCF は Subscription-State が `terminated;reason=noresource` の NOTIFY で購読を終わらせます（TS 24.229 5.4.2.1.2）。IP-SM-GW は 200 OK を返し、`<IP-SM-GW-Number>` を空にした PUR を送ります。利用者を示すのは、ここでも MSISDN です。これで HSS から IP-SM-GW の登録が消えます。S-CSCF は Expires が 0 でボディの無い第三者 REGISTER も送り、IP-SM-GW は 200 OK を返すだけです。スクリプトのキャプチャでは、NOTIFY がこの REGISTER より先に来ています。

## 送信と配達（Hello UE-B, this is UE-A.）

```text
 UE 1     P-CSCF    S-CSCF    I-CSCF   IP-SM-GW+SC    HSS      UE 2
  | MESSAGE |         |         |          |           |         |
  |-------->| (1)     |         |          |           |         |
  |         |-------->| (2)     |          |           |         |
  |         |         |------------------->| (3)       |         |
  |  202    |  202    |  202    |          |           |         |
  |<--------|<--------|<-------------------| (4)       |         |
  |         |         |         |          |  UDR (5)  |         |
  |         |         |         |          |---------->|         |
  |         |         |         |          |  UDA      |         |
  |         |         |         |          |<----------|         |
  |         |         |         | MESSAGE submit report (6)      |
  |         |         |         |<---------|           |         |
  |         |         |         |  LIR, LIA            |         |
  |         |         |         |<-------------------->|         |
  |         |         |<--------|          |           |         |
  |<--------|<--------|         |          |           |         |
  |  200 (7)|         |         |          |           |         |
  |-------->|-------->|-------->|--------->|           |         |
  |         |         |         | MESSAGE SMS-DELIVER (8)        |
  |         |         |         |<---------|           |         |
  |         |         |         |  LIR, LIA            |         |
  |         |         |         |<-------------------->|         |
  |         |         |<--------| (9)      |           |         |
  |         |<--------|         |          |           |         |
  |         |---------------------------------------------------->|
  |         |  200 (10)         |          |           |         |
  |         |<----------------------------------------------------|
  |         |-------->|-------->|--------->|           |         |
  |         | MESSAGE delivery report (11) |           |         |
  |         |<----------------------------------------------------|
  |         |-------->| (12)    |          |           |         |
  |         |         |------------------->|           |         |
  |         |  202    |  202    |          |           |         |
  |         |         |<-------------------|           |         |
  |         |<--------|         |          |           |         |
  |         |---------------------------------------------------->|
```

I-CSCF は Record-Route を付けず、MESSAGE はダイアログを作らないので、どの MESSAGE とその応答も 1 ホップずつ進みます。IP-SM-GW が送る MESSAGE は新しい要求で、それぞれが別の Call-ID を持ちます。ラボのラダー図は 1 つの Call-ID ずつ描きます。

ラダー図は順に並べて描いていますが、同時に進むものもあります。IP-SM-GW は 202 を送るとすぐに SC へメッセージを渡すので、キャプチャでは手順 5 の UDR が 202 より先に来ることがあります。SC がメッセージを受け取ると、submit report（手順 6）と SMS-DELIVER（手順 8）は一緒に送られ、それぞれの LIR と応答は入り混じります。

UE 1 の `ue sms` は送る直前に登録するので、キャプチャには UE 1 についても前の節の流れがあります。その流れは手順 1 の MESSAGE を送るときにまだ続いていることがあります。

### 1. UE 1 がショートメッセージを送る

TS 24.341 5.3.1.2 の手順です。次の点を見てください。

- **Request-URI と To.** `tel:+12025550100` で、SC の公開サービス識別子（PSI）です。UE はショートメッセージを受信者に直接送らず、自分の SC へ送ります。無線で送る端末と同じです。
- **From.** UE 1 の公開ユーザー識別子です。
- **Content-Type: application/vnd.3gpp.sms** と 47 オクテットのボディ。ボディは RP-DATA で、受信者の番号と本文を持つ SMS-SUBMIT が入っています。下の「ボディを読む」で分解します。
- **Route.** INVITE と同じで、P-CSCF と、UE 1 の Service-Route です。

UE 1 は Call-ID を覚えておきます。submit report がこの Call-ID を示すからです。

### 2. P-CSCF が転送する

ダイアログの外の MESSAGE は単独のトランザクションです（TS 24.229 5.2.6.3.7）。INVITE と同じように、P-CSCF は自分の Route を取り除き、P-Asserted-Identity を入れて（step 4）、Service-Route に沿って MESSAGE を送ります。INVITE と違い、あとにダイアログが続かないので Record-Route は付けず、メディア中継ノードも使いません。

### 3. S-CSCF が IP-SM-GW に送る

一番上の Route に `orig` があるので、S-CSCF は送信者を担当します。UE 1 の tel URI を 2 つ目の P-Asserted-Identity として加えます（TS 24.229 5.4.3.2 step 9 a）。IP-SM-GW が送信者を SC に伝えるには、SIP URI ではなく番号が要るからです（TS 24.341 5.3.1.2 NOTE 2）。2 つ目の条件に一致するので（step 5）、S-CSCF は前に示したとおり IP-SM-GW と ODI 付きの自分の URI を Route の先頭に入れ、UE 1 の SIP URI と `sescase=orig`、`regstate=reg` を書いた P-Served-User を加えて、MESSAGE を IP-SM-GW へ送ります。

### 4. IP-SM-GW が受け付ける（202）

IP-SM-GW は **202 Accepted** を返します（TS 24.341 5.3.3.4.1）。要求の P-Charging-Vector を繰り返し、term-ioi を足します（TS 24.229 5.7.2）。MESSAGE を中継する UA は、まだ配達していないので 200 ではなく 202 を返します（RFC 3428 7）。202 はショートメッセージそのものについては何も伝えません。それを伝えるのは submit report です。

続いて IP-SM-GW は RP-DATA を確かめます。デコードできること、RP-DA が SC の番号であること、P-Asserted-Identity に tel URI があることです。どれかが満たされないと、submit report は RP-ERROR になります（「失敗するとき」を参照）。

### 5. SC が受信者を調べる（UDR と UDA）

IP-SM-GW は SMS-SUBMIT と送信者の番号を SC のスタブに渡します。SC は TP-DA の番号 `+12025550102` を持つ利用者を UDR（コマンド 306）で調べます。利用者を MSISDN で指定し、Data-Reference は 10（IMSPublicIdentity、TS 29.328 7.6.2）です。UDA には `sip:+12025550102@...` と `tel:+12025550102` が入っています。実際の網では、SC の側は代わりに経路の情報を問い合わせます（TS 23.204 6.4 step 3）。ここでは Sh の読み出しがそれに代わります。

### 6. submit report が UE 1 へ戻る

SC がメッセージを受け取ったので、IP-SM-GW は submit report を入れた新しい MESSAGE を UE 1 へ送ります（TS 24.341 5.3.3.4.3）。

- **Request-URI.** UE 1 の SIP URI です。手順 1 の MESSAGE の 1 つ目の P-Asserted-Identity から取ります。
- **P-Asserted-Identity.** IP-SM-GW の SIP URI です。
- **Accept-Contact: \*;+g.3gpp.smsip;require;explicit** と **Request-Disposition: fork**。機能タグを登録した連絡先だけが受け取れます（手順 9 を参照）。
- **In-Reply-To.** 手順 1 の Call-ID です。UE 1 はこれで、どのショートメッセージの報告かを知ります（TS 24.341 5.3.1.2）。
- **P-Charging-Vector.** 新しい icid-value と orig-ioi を持ちます。IP-SM-GW は発信側の UA として動く AS なので、自分の MESSAGE はどれもこの形で始めます（TS 24.229 5.7.3）。
- **ボディ.** RP-DATA と同じ RP-MR を持つ RP-ACK で、中に SC のタイムスタンプ（TP-SCTS）を持つ SMS-SUBMIT-REPORT が入っています。

IP-SM-GW は S-CSCF の Route を使いません。利用者に要求を送る AS は、その利用者の S-CSCF か利用者の網の入口を Route に入れるので（TS 24.229 5.7.3）、IP-SM-GW は自分が出す要求に `Route: <sip:ims.mnc001.mcc001.3gppnetwork.org;lr>` を付けてホーム網の入口へ送ります。ホームドメインの名前は DNS で I-CSCF に解決され、I-CSCF はこの Route を自分のものとして取り除きます（5.3.2.1）。I-CSCF は通話と同じように LIR で HSS に問い合わせ、UE 1 の S-CSCF へ MESSAGE を転送します。S-CSCF はこれを着信側の要求として扱い、UE 1 の Path に沿って送ります。

### 7. UE 1 が 200 を返す

UE 1 は In-Reply-To を自分のショートメッセージと照らし合わせ、**200** を返し（TS 24.341 5.3.1.2）、メッセージを受け付けられたものとして記録します。

### 8. SC がメッセージを配達する（SMS-DELIVER）

SC は SMS-SUBMIT を SMS-DELIVER に作り変えます。送信者の番号を TP-OA に、メッセージを受け取った時刻を TP-SCTS に入れ、本文はそのままです。UE 1 が status report を頼んだので、TP-SRI を立てます。IP-SM-GW はこれを MESSAGE に入れて送ります（TS 24.341 5.3.3.4.2）。

- **Request-URI.** `tel:+12025550102` で、UDA が返した tel URI です（TS 23.204 6.4 step 6）。
- **Accept-Contact: \*;+g.3gpp.smsip;require;explicit** と **Request-Disposition: no-fork**。
- **P-Asserted-Identity.** IP-SM-GW の SIP URI です。UE 2 は delivery report をここへ送ります。
- **ボディ.** 網から送る RP-DATA で、RP-OA に SC のアドレスがあり、中に SMS-DELIVER が入っています。

この MESSAGE も I-CSCF へ送られ、I-CSCF は LIR で UE 2 の S-CSCF を見つけます。

### 9. S-CSCF が発信者の希望で連絡先を選ぶ

今度は S-CSCF が UE 2 を担当します（TS 24.229 5.4.3.3）。P-Asserted-Identity が IP-SM-GW のものなので、着信側の条件には一致しません（step 4）。S-CSCF は要求の Accept-Contact を、UE 2 が登録した連絡先のパラメータと照らし合わせます（step 9、RFC 3841 7.2.4）。`require` は一致しない連絡先を外し、`explicit` は連絡先が `+g.3gpp.smsip` を自分で明示していることを求めます。UE 2 の連絡先はこれを持っているので残ります。音声だけを登録した UE なら一致せず、S-CSCF は 480 を返します（`internal/ue/sms_ims_test.go` の `TestShortMessageToAUEWithoutSMSOverIP`）。`no-fork` は連絡先を 1 つだけにすることを求めます（step 10 a、RFC 3841 9.1）。そのあとは通話と同じで、S-CSCF は P-Called-Party-ID を入れ、UE 2 の Path に沿って MESSAGE を送ります。

### 10. UE 2 が 200 を返す

UE 2 は **200** を返します（TS 24.341 5.3.2.3）。これは MESSAGE が届いたことを示すだけです。UE 2 がショートメッセージを受け取ったかどうかは、delivery report で伝えます。

### 11. UE 2 が delivery report を送る

UE 2 は新しい MESSAGE を送ります（TS 24.341 5.3.2.4）。

- **Request-URI と To.** IP-SM-GW です。手順 8 の P-Asserted-Identity から取ります。
- **In-Reply-To.** 手順 8 の Call-ID です。
- **ボディ.** RP-DATA の RP-MR を持つ、端末からの RP-ACK で、中に SMS-DELIVER-REPORT が入っています。メモリが一杯などの理由でメッセージを受け取れない UE は、代わりに RP-ERROR を送ります。

### 12. delivery report が UE 2 の条件で IP-SM-GW に届く

delivery report は、UE 2 が発信する要求で、SMS のボディを持つ MESSAGE です。そのため、UE 2 自身の 2 つ目の条件によって、手順 3 とまったく同じように ODI 付きで IP-SM-GW へ送られます。IP-SM-GW は 202 Accepted を返し（TS 24.341 5.3.3.4.1）、In-Reply-To の Call-ID で配達を見つけ、メッセージが届いたことを SC に伝えます。

TS 23.204 6.5 step 3 は、delivery report がショートメッセージを送った IP-SM-GW に届くことを求めています。ここでは DNS がほかの IP-SM-GW のノードを返すこともあります。そこで、IP-SM-GW のどのノードも配達の状態を共有の `Store` 1 つに置き、delivery report を受けたノードが配達を終わらせます（`internal/ipsmgw/network_test.go` の `TestDeliveryReportThroughAnotherIPSMGW`）。delivery report を待つ 64 秒も、配達と一緒に置きます。どのノードも 1 秒ごとに、待ち時間の過ぎた配達を探し、見つけたものを「確認できなかった」として SC に伝えます。そのため、メッセージを送ったノードが止まっても、配達の結果が分からないままにはなりません。

## status report

UE 1 は SMS-SUBMIT の TP-SRR で status report を頼んだので、delivery report が届くと、SC は UE 1 に SMS-STATUS-REPORT を送ります。

```text
 UE 1     P-CSCF    S-CSCF    I-CSCF   IP-SM-GW+SC    HSS
  |         |         |         |          | UDR (13)  |
  |         |         |         |          |---------->|
  |         |         |         |          |  UDA      |
  |         |         |         |          |<----------|
  |         |         |         | MESSAGE status report (14)
  |         |         |         |<---------|           |
  |         |         |         |  LIR, LIA            |
  |         |         |         |<-------------------->|
  |         |         |<--------|          |           |
  |<--------|<--------|         |          |           |
  |  200    |         |         |          |           |
  |-------->|-------->|-------->|--------->|           |
  | MESSAGE delivery report (15)|          |           |
  |-------->|-------->|------------------->|           |
  |  202    |  202    |  202    |          |           |
  |<--------|<--------|<-------------------|           |
```

13. 手順 12 の delivery report が届くとすぐに、SC は手順 5 と同じように、送信者の番号 `+12025550101` を UDR で調べます。キャプチャでは、この UDR が手順 12 の 202 より先に来ることがあります。
14. IP-SM-GW は status report をショートメッセージと同じように送ります（TS 24.341 5.3.3.4.4）。宛先は UE 1 の tel URI で、Accept-Contact と `Request-Disposition: no-fork` を付け、中に SMS-STATUS-REPORT の入った RP-DATA を入れます。SMS-SUBMIT の TP-MR を繰り返すので、UE 1 はどのメッセージの報告かが分かります。TP-ST の 0x00 は、受信者がメッセージを受け取ったことを示します（TS 23.040 9.2.3.15）。
15. UE 1 は 200 を返し、手順 11 の UE 2 と同じように、delivery report で status report を受け取ったことを伝えます（TS 24.341 5.3.1.3）。

## 失敗するとき

送信者に何を返すかは、IP-SM-GW と SC が決めます。試せる場合は次のとおりです。

| 場合 | 起きること | UE 1 が受け取るもの |
| --- | --- | --- |
| 番号を持つ加入者がいない（`+12025550198`） | 手順 5 の UDA が DIAMETER_ERROR_USER_UNKNOWN（5001）になる。 | RP-Cause 1（unassigned number）の RP-ERROR で、中の SMS-SUBMIT-REPORT の TP-FCS は 0xC3（invalid SME address）である submit report（TS 24.011 Table 8.4、TS 23.040 9.2.3.22）。配達は試みない。 |
| 受信者が登録していない（`+12025550103`） | SC はメッセージを受け付けるので、submit report は RP-ACK になる。受信者には未登録の状態のサービス（TERMINATING_UNREGISTERED の場合に MMTel AS を呼ぶ iFC）があるので、I-CSCF の LIR は DIAMETER_UNREGISTERED_SERVICE（2003）になり、I-CSCF が S-CSCF を選ぶ（TS 29.228 6.1.4.1）。その S-CSCF は UNREGISTERED_USER の SAR で利用者のプロファイルを取り、手順 8 の MESSAGE に合う iFC が無いので、480 を返す（TS 24.229 5.4.3.3）。IP-SM-GW はこれを不在の加入者（Absent Subscriber）として SC に伝える（TS 24.341 5.3.3.4.2、TS 29.311 6.1.4.4.1）。 | TP-ST が 0x62 の status report。一時的な誤りで、SC がもう配達を試みないことを示す。スタブはメッセージを蓄積して再送しない。 |
| 受信者のメモリが一杯 | UE 2 が RP-DATA に RP-Cause 22（memory capacity exceeded）の RP-ERROR を返し、中の報告の TP-FCS は 0xD3 になる。 | TP-ST が 0x62 の status report。 |
| RP-DATA をデコードできない | IP-SM-GW は 202 を返してから断る。 | RP-Cause 96（invalid mandatory information）の RP-ERROR（TS 24.341 5.3.3.4.1、TS 24.011 9.3.4）。 |

UE シミュレータのメモリは一杯にならないので、メモリが一杯の場合は試験の中にしかありません。`internal/ipsmgw/network_test.go` の `TestShortMessagesThatFail` が、デコードできない RP-DATA の場合と一緒に、IMS 全体を通して確かめています。

## ボディを読む

MESSAGE のボディはバイナリです。TS 24.341 では、UE はオクテットをそのまま送ります。次は手順 1 の MESSAGE のボディで、UE シミュレータが `internal/sms` で作ったものです。ラボでは、MESSAGE の詳細の「Body (hex)」に同じオクテットが表示されます。

```text
00 00 00 07 91 21 20 55 05 01 f0 23 21 00 0b 91 21 20 55 05 01 f2 00 00 19 c8 32 9b fd 06 55 8b 2d 21 0b
44 47 a7 e7 a0 f4 1c 54 2d b6 82 2e
```

### 中継層（RP）

TS 24.011 7.3.1.2 の Table 7.5 と、8.2 です。

| オクテット | フィールド | 値 |
| --- | --- | --- |
| `00` | RP-MTI（8.2.2）、下位 3 ビット | 0。端末から網への RP-DATA。1 は網からの RP-DATA、2 と 3 は RP-ACK、4 と 5 は RP-ERROR、6 は RP-SMMA。 |
| `00` | RP-MR（8.2.3） | 0。答えの RP-ACK や RP-ERROR は同じ値を持つ。 |
| `00` | RP-OA の長さ（8.2.5.1） | 0。端末からの RP-DATA では発信元のアドレスは空。 |
| `07` | RP-DA の長さ（8.2.5.2） | このあとに 7 **オクテット**続く。 |
| `91` | アドレスの種別 | 拡張ビット 1、番号の種別 001（国際番号）、番号計画 0001（E.164）。 |
| `21 20 55 05 01 f0` | RP-DA の数字 | 1 オクテットに 2 桁で、下位の半分が先。最後の余りは `f` で埋める。1 2 0 2 5 5 5 0 1 0 0 で、SC の `+12025550100`。 |
| `23` | RP-User-Data の長さ（8.2.5.3） | このあとに 35 オクテット続く。これが TPDU。 |

RP-ACK と RP-ERROR では RP-User-Data は省略できる要素なので、長さの前に要素の識別子 `41` も付きます。

### 転送層（TP）の SMS-SUBMIT

TS 23.040 9.2.2.2 と 9.2.3 です。

| オクテット | フィールド | 値 |
| --- | --- | --- |
| `21` | 1 オクテット目 | `0010 0001`。TP-MTI 01（ビット 1-0、SMS-SUBMIT、9.2.3.1）、TP-RD 0（ビット 2）、TP-VPF 00（ビット 4-3、有効期間なし、9.2.3.3）、TP-SRR 1（ビット 5、status report を頼む、9.2.3.5）、TP-UDHI 0（ビット 6、ヘッダなし）、TP-RP 0（ビット 7）。 |
| `00` | TP-MR（9.2.3.6） | 0。status report はこの値を繰り返す。 |
| `0b` | TP-DA の長さ（9.1.2.5） | 11 **桁**（半オクテットの数）。 |
| `91` | アドレスの種別 | 国際番号、E.164。 |
| `21 20 55 05 01 f2` | TP-DA の数字 | 受信者の `+12025550102`。 |
| `00` | TP-PID（9.2.3.9） | 0。普通のショートメッセージ。 |
| `00` | TP-DCS（9.2.3.10） | 0。GSM 7 ビットの既定のアルファベット（TS 23.038 4）。 |
| `19` | TP-UDL（9.2.3.16） | 25 **セプテット**。25 文字。 |
| `c8 32 9b fd 06 55 ... b6 82 2e` | TP-UD | 22 オクテットに詰めて並べた本文。 |

2 つのアドレスは同じ 11 桁の番号を同じ形で持っていますが、長さの値は違います。RP は長さのあとに続くオクテットの数を、アドレスの種別も含めて数え（7）、TP は桁の数を数えます（11）。ショートメッセージを手で組み立てるときに間違えやすいところです。

TP-MTI も向きによって意味が変わります。01 は端末からなら SMS-SUBMIT、網からなら SMS-SUBMIT-REPORT です。00 は網からなら SMS-DELIVER、端末からなら SMS-DELIVER-REPORT です。10 は SMS-STATUS-REPORT です（9.2.3.1）。どれなのかは TPDU を包む RP のメッセージで分かります。

### GSM 7 ビットの詰め方と UCS2

GSM 7 ビットの既定のアルファベットには、7 ビットの文字が 128 個あります（TS 23.038 6.2.1）。「Hello UE-B, this is UE-A.」の文字は ASCII と同じ値ですが、すべての文字がそうではありません。たとえば `@` は 0x00 です。セプテットは下位のビットから順にオクテットへ詰めます（TS 23.038 6.1.2.1.1）。

```text
H = 100 1000, e = 110 0101, l = 110 1100, ...
octet 1: lowest bit of 'e' (1), then 'H' (1001000)          = 1100 1000 = c8
octet 2: lowest 2 bits of 'l' (00), then rest of 'e' (110010) = 0011 0010 = 32
```

25 セプテットは 175 ビットなので、22 オクテットに収まります。160 文字が TP-UD の 140 オクテットに入るのはこのためです。

日本語のように、アルファベットに無い文字を含む本文は UCS2 で送ります。TP-DCS は 0x08 で、1 文字が上位のオクテットから並ぶ 2 オクテットになり、TP-UDL はオクテットの数を数えます（9.2.3.16）。スクリプトで UE 2 が送る `こんにちは、UE-A さん` は、TP-DCS が `08`、TP-UDL が `1a`（26 オクテット）、TP-UD が `30 53 30 93 30 6b 30 61 30 6f 30 01 00 55 00 45 00 2d 00 41 00 20 30 55 30 93` です。70 文字まで入ります。

### Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `gsm_a.rp.msg_type == 0x00` | UE からの RP-DATA。UE が送ったショートメッセージ。UE へのものは `0x01`、RP-ACK は `0x02` と `0x03`、RP-ERROR は `0x04` と `0x05`。 |
| `gsm_a.rp.cause` | RP-Cause を持つ RP-ERROR。 |
| `gsm_sms.tp-mti == 2` | SMS-STATUS-REPORT。`gsm_sms.tp-mti == 1` は、前に説明したとおり SMS-SUBMIT と SMS-SUBMIT-REPORT の両方に一致する。 |
| `gsm_sms.tp-da == "12025550102"` | その番号に宛てたショートメッセージ。`gsm_sms.tp-oa` は SMS-DELIVER の送信者、`gsm_sms.tp-ra` は status report の受信者。 |
| `gsm_sms.sms_text contains "Hello"` | 本文。GSM 7 ビットも UCS2 もデコードしたうえで調べる。 |
| `gsm_sms.tp-srr == 1` | status report を頼んだ SMS-SUBMIT。 |
| `gsm_sms.tp-fcs` | TP-Failure-Cause を持つ報告。 |
| `sip.In-Reply-To` | submit report と delivery report。 |
| `sip.Accept-Contact` | IP-SM-GW が送る MESSAGE。 |

`scripts/e2e-sms.sh` は、この表のフィルタをすべて、このシナリオのキャプチャに当てます。フレームが 1 つも見つからないフィルタがあれば、試験は失敗します。

## コードの場所

| パッケージ、ファイル | 内容 |
| --- | --- |
| `internal/sms` | ボディ。`rp.go`（TS 24.011）、`tp.go`（TS 23.040）、`text.go`（TS 23.038）、`address.go`（2 種類のアドレスのフィールド）。 |
| `internal/ifc` | 初期フィルタ条件と、要求がそれに一致するかの判定（TS 29.228 Annex B、E、F）。 |
| `internal/cx/userdata.go`、`internal/hss/profile.go` | 条件を含むユーザープロファイルと、HSS のスタブが `{msisdn}` を埋めるところ。 |
| `internal/sh`、`internal/hss/sh.go` | Sh のメッセージと Sh-Data、IP-SM-GW が使うクライアント、HSS の側。 |
| `internal/scscf/services.go` | S-CSCF が条件を評価し、ODI を付け、DefaultHandling に従うところ。 |
| `internal/scscf/thirdparty.go` | 第三者 REGISTER。 |
| `internal/scscf/callerprefs.go` | Accept-Contact の照合（RFC 3841）。 |
| `internal/ipsmgw` | IP-SM-GW。`ipsmgw.go`（第三者 REGISTER）、`regevent.go`（reg イベントの購読と PUR）、`message.go`（UE からの MESSAGE）、`deliver.go`（UE への MESSAGE）、`servicecentre.go`（SC との境界）、`store.go`（配達の途中の状態）。 |
| `internal/sc` | SC のスタブ。 |
| `internal/ue/sms.go` | SM-over-IP sender と receiver としての UE。 |
| `internal/lab/sms.go` | ラボが詳細の表示のためにショートメッセージをデコードするところ。 |
| `internal/ipsmgw/network_test.go`、`internal/ue/sms_ims_test.go` | 流れの全体を 1 つのプロセスで動かす試験。上の失敗の場合も含む。 |

## このプロジェクトで簡略化していること

各項目には、コードに `SIMPLIFIED:` のコメントがあります。`grep -rn "SIMPLIFIED:" internal cmd` ですべてを一覧できます。いくつかは登録と通話の手順書にも載っています。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| IP-SM-GW、TS 24.229 5.7.1.1 と 5.7.3 | 第三者 REGISTER の icid-value と orig-ioi も、IP-SM-GW が自分で送る要求への応答の term-ioi も保存しない。 | どれも IP-SM-GW の課金の記録のためのもので、このプロジェクトのノードは課金の記録を作らない。 |
| IP-SM-GW、RFC 6665 4.1.2.4 | Timer N を使わない。NOTIFY が来なかった SUBSCRIBE は、次の第三者 REGISTER のときに新しい購読に置き換える。 | S-CSCF は受け付けた SUBSCRIBE のすべてにすぐ NOTIFY を送る。 |
| IP-SM-GW、TS 24.341 5.3.3.2 | service-info に MSISDN が無いとき、PUR では利用者を公開ユーザー識別子で示す。UE の REGISTER の IMSI は使わない。 | IMSI は message/sip のボディに入った UE の REGISTER から取るが、S-CSCF はそのボディを送らない。Sh は公開ユーザー識別子も受け付ける（TS 29.328 Table 7.6.1）。 |
| IP-SM-GW、TS 24.341 5.3.3.4.1 | In-Reply-To の無い delivery report は捨てる。 | TS 24.341 5.3.2.4 は UE に In-Reply-To を付けさせている。RP-MR だけで配達を探すには、Store に 2 つ目のキーが要る。 |
| IP-SM-GW、TS 24.341 5.3.3.4.1 step d と e、TS 23.204 6.6 | RP-SMMA（メモリに空きができた知らせ）を HSS に渡さず、すぐに RP-ACK で答える。 | SC のスタブはメッセージを蓄積しないので、知らせる相手がいない。 |
| SC、TS 23.204 6.3 と 6.4、TS 23.040 3.2.1 と 3.2.6 | SGd、S6c、MAP を話さず、IMS と CS や PS のドメインを選ばず、蓄積と再送もしない。すぐに配達できないメッセージは捨て、そのことを status report で伝える。Message Waiting のデータは無く、有効期間は読まない。 | SC は IP-SM-GW の中のスタブである。 |
| SC、TS 23.204 6.4 step 3 | 経路の情報の問い合わせ（SRI for SM や S6c）の代わりに、番号を持つ公開ユーザー識別子を Sh で読む。 | スタブは利用者を IMS の利用者としてだけ知っている。 |
| SMS、TS 23.040 9.1.2.5 | 送信者の名前のような英数字のアドレス（番号の種別 101）は断る。 | SC のスタブは E.164 の番号しか使わない。 |
| SMS、TS 23.040 9.2.2.4 | SMS-COMMAND は断る。 | SC が持っているメッセージについて問い合わせるもので、スタブは何も持っていない。 |
| SMS、TS 23.040 9.2.3.12.2 と 9.2.3.12.3 | 「有効期間なし」と相対形式だけを読み書きし、絶対形式と拡張形式は断る。 | SC のスタブは何も蓄積しないので、有効期間を使わない。 |
| SMS、TS 23.040 9.2.3.24.1 | 連結 SMS は扱わない。1 通に収まらない本文は断る。 | このプロジェクトはショートメッセージを 1 通ずつ送る。 |
| SMS、TS 23.040 9.2.3.24 と TS 23.042 | 各国語の文字表（TS 23.038 6.2.1.2）などの User-Data-Header と、圧縮した本文はデコードしない。 | このプロジェクトではどれも送らない。 |
| UE、TS 24.341 5.3.2.5 | UE はメモリ不足でショートメッセージを断ることがないので、RP-SMMA を送らない。 | シミュレータは受けたショートメッセージをすべて保持する。 |
| UE、RFC 3428 7 | ショートメッセージだけを受け、text/plain を含むほかのボディの MESSAGE には 415（Unsupported Media Type）を返す。 | シミュレータは SMS over IP の UE で、インスタントメッセージのクライアントではない。 |
| UE、TS 23.040 9.2.3.12 と 9.2.3.24 | SMS-SUBMIT は有効期間を求めず、User-Data-Header の無い 1 通のショートメッセージだけを持つ。 | SC のスタブは有効期間を使わない。 |
| UE、TS 24.011 9.3.2 | RP-MR が RP-DATA と違う報告は捨てる。端末なら送る cause 81 の RP-ERROR は送らない。 | IP では、それを送る接続が無い。 |
| Sh、TS 29.328 6.1.3 と 6.1.4 | 通知の購読（SNR/SNA、PNR/PNA、コマンド 308 と 309）は無い。 | IP-SM-GW は自分を登録し、利用者を調べるだけである。MMTel AS は初期の要求ごとに Repository Data を読み、写しを持たない。TS 29.364 8.1 はそのような AS に購読を勧めている。 |
| Sh、TS 29.328 7.1.3 | External-Identifier は無い。 | マシン型の通信のためのもので、このプロジェクトでは扱わない。 |
| Sh、TS 29.328 Annex D | Sh-Data には PublicIdentifiers（IMSPublicIdentity、MSISDN）と RepositoryData、Sh-IMS-Data の SCSCFName と SMSRegistrationInfo だけがある。 | 残りは、使う役割ができたときに加える。 |
| Sh、TS 29.328 7.6.16 | SMSRegistrationInfo に SCAddress と、SBI に対応しているかの表示が無い。 | HSS は IP-SM-GW にサービスセンタのアドレスを渡さず、IP-SM-GW は SC に Diameter だけで届く。 |
| HSS、TS 29.338 5.3.1.2 | 通知は、サービスセンタがこの HSS のインスタンスに張った接続で返します。エージェントが Diameter 名で振り分ける形ではありません。サービスセンタがつながっていないインスタンスは通知を送りません。 | HSS は S-CSCF にも同じ方法で届き、このプロジェクトに Diameter のエージェントはありません。アドレスは message waiting data に残るので、次に利用者が戻ったとき、その要求に答えたインスタンスが通知します。 |
| HSS、TS 23.204 6.5b step 2 と 6.6、TS 23.040 3.2.6 の 2c と 2f | 利用者がまたショートメッセージを受け取れるようになる 2 つのきっかけのうち、HSS が知るのは IP-SM-GW の登録だけです。登録し直さずに届くようになった UE と、メモリに空きができた UE の知らせは、どこからも来ません。 | IP-SM-GW は到達性の通知を購読せず、メモリに空きができた知らせも渡しません。メモリが一杯だったことを示すフラグは、ショートメッセージが届いたときに落ちます。 |
| HSS、TS 23.204 6.5a step 19 | SMS-GMSC が送る配送状態の報告を HSS が処理します。条項では、IP-SM-GW が既に報告しているので無視するとあります。 | このプロジェクトの IP-SM-GW は S6c を話さないので、HSS に届く報告は SMS-GMSC のものだけです。 |
| HSS、TS 23.040 3.2.6 の 1a と 1b | 利用者が不在だったとき、どのノードからの報告でも IP で届かないことを示すフラグを立てます。MSC からの報告と SGSN からの報告でフラグを分けません。 | HSS はそのような serving node を示さないので、失敗するのは IP-SM-GW を通る配送だけです。 |
| HSS、TS 29.338 5.2.1.3 | 配送先の問い合わせに答えるとき、着信ショートメッセージのテレサービスに加入しているかと、着信を禁止されていないかを確かめない。そのため DIAMETER_ERROR_SERVICE_NOT_SUBSCRIBED と DIAMETER_ERROR_SERVICE_BARRED を返さない。 | この HSS の加入者はどれもショートメッセージを受け取れ、事業者が決める発着信規制を持たない。 |
| HSS、TS 29.338 Table 5.2.1.1-2 | 非公開ユーザー識別子が IMSI から作られていない加入、たとえばソフトフォンが番号で登録する加入では、成功の応答に IMSI を入れない。 | HSS は IMSI を独立して持たず、TS 23.003 13.3 の IMSI を非公開ユーザー識別子から読む。 |
| HSS、TS 23.204 6.4 step 3a から 3c | 配送先の問い合わせを IP-SM-GW に回さず、HSS が自分で答える。回した場合、IP-SM-GW は自分のアドレスと MT Correlation ID を返す。 | 問い合わせを回すのは、ほかの網から来たショートメッセージを宛先のホーム網を通して配送させるためで、このプロジェクトはそれを行わない。IP-SM-GW の Diameter 名は登録のときに HSS が覚えている。 |
| HSS、TS 29.329 7.1 | Supported-Features で機能を取り決めない。HSS は、省略できる機能にどれも対応しない AS と同じように扱うので、要求は Data-Reference を 1 つだけ指定し、Repository Data では Service-Indication か RepositoryData も 1 つだけにする。HSS にデータが無いときの応答には User-Data が無い。 | このプロジェクトの IP-SM-GW は、省略できる機能をどれも示さない。 |
| HSS、TS 29.328 6.2 | AS ごとの権限が無く、接続したピアならどれでも Sh を使える。 | このプロジェクトの AS は IP-SM-GW だけである。 |
| HSS、TS 29.328 6.1.1.1 と 6.1.2.1 step 2a | User-Name の非公開ユーザー識別子を読まず、確かめない。 | IP-SM-GW はそれを送らない。 |
| HSS、TS 29.328 7.6.9 | MSISDN は独立したデータではなく、公開ユーザー識別子の中の最初の tel URI から取る。 | 設定に各番号を 1 回だけ書けば済み、tel URI を通して MSISDN で利用者を見つけられる。 |
| HSS、TS 29.328 7.6.2 | IMPLICIT_IDENTITIES と ALIAS_IDENTITIES は ALL_IDENTITIES と同じ集合を返し、利用者を MSISDN で指定したときも断らない。 | 試験用の加入はどれも、1 つのサービスプロファイルを持つ 1 つの暗黙の登録セットであるため。 |
| ユーザープロファイル、TS 29.228 Annex E | ServiceProfile は 1 つで、省略できる要素は BarringIndication、公開サービス識別子の `IdentityType` と `WildcardedPSI`、条件のほかに無く、スキーマの XML の名前空間も無い。 | 試験用の加入と PSI ユーザーには、それ以上は要らない。 |
| iFC、TS 29.228 Annex E と B.2.3 | SessionDescription の SPT は断り、ProfilePartIndicator、RegistrationType、IncludeRegisterRequest、IncludeRegisterResponse は読み飛ばす。REGISTER の SPT はすべての REGISTER に一致する。 | SMS と MMTel AS の iFC には、どれも要らない。RegistrationType は S-CSCF にとって省略できる。 |
| S-CSCF、TS 24.229 5.4.0 | S-CSCF は、最初の第三者 REGISTER を送ったアドレスを覚えておかず、要求のたびに AS を DNS で引く。 | AS の各役割は、すべてのノードで共有する Store に状態を置くので、どのノードでも次の要求を受けられる。 |
| S-CSCF、TS 24.229 5.4.1.7 item c | 第三者 REGISTER の To は、いつも既定の公開ユーザー識別子にする。item i には写すものが無く、item j、l、m はこのプロジェクトに無い機能のためのものである。 | どの識別子にするかはオペレータが選べる。HSS のスタブは課金のアドレスを送らない。 |
| S-CSCF、RFC 3261 10.2 | 第三者 REGISTER ごとに新しい Call-ID を使う。 | 利用者と AS の組ごとに Call-ID を 1 つ保つと、Store の状態が増える。レジストラは新しい Call-ID を受け付ける（RFC 3261 10.3 step 7）。 |
| S-CSCF、TS 24.229 5.4.1.7 | 第三者 REGISTER の応答の term-ioi を保存しない。 | この IOI は S-CSCF の課金の記録のためのもので、このプロジェクトのノードは課金の記録を作らない。 |
| S-CSCF、TS 24.229 5.4.3.2 と 5.4.3.3 | AS からの 100（Trying）は、DefaultHandling を止める 1xx として数えない。 | プロキシは 100 を、それが来たホップで止める（RFC 3261 16.7 step 5）。 |
| S-CSCF、RFC 3841 7.2.4 と 9.1、TS 24.229 5.4.3.3 step 10 a | 宛先の候補は、非公開ユーザー識別子ごとに保つ 1 つの連絡先だけなので、照合はそれを残すかどうかだけを決める。Qa と q 値は計算せず、Request-Disposition にも従わない。 | 連絡先が 1 つなら、どの指示でも同じ連絡先に届く。 |
| S-CSCF、RFC 3841 8 | 機能タグの否定の値（`!x`）と数値（`#>=4`）は、ただのトークンとして比べる。 | SMS over IP と TS 24.229 7.9 のタグは、どちらも使わない。 |

## 後片付け

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。
