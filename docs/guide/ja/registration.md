# UE が IMS に登録する流れを追う

[English](../en/registration.md)

IMS で電話をかけたり SMS を送ったりする前に、端末は登録をします。登録で、端末がどこにいるかをホーム網に知らせ、利用者が本人であることを証明し、後の要求が通る経路を決めます。この手順書では登録を 1 回実行し、キャプチャに記録されたメッセージを順に読んでいきます。

この手順書は 3GPP の Release 18（TS 24.229 V18.11.0、TS 29.228 V18.0.0、TS 29.229 V18.1.0）に沿っています。条項番号はこれらの版のものです。

## 始める前に

- [信号をキャプチャして Wireshark で開く](capture.md) の手順で、構成を起動してキャプチャを開けること。
- 略語の説明は [用語集](../../glossary.md) にあります。

## 登場するノード

| ノード | アドレス | 登録での役割 |
| --- | --- | --- |
| UE シミュレータ | 172.28.0.100 | 端末。REGISTER を送り、認証の要求に答える。 |
| P-CSCF | 172.28.0.40 | UE から見て最初の SIP の中継点。ホーム網を探し、Path を付ける。 |
| I-CSCF | 172.28.0.20 | ホーム網の入口。利用者を担当する S-CSCF を HSS に問い合わせる。 |
| S-CSCF | 172.28.0.30 | 登録を受け付けるノード（レジストラ）。利用者を認証し、登録を記録する。 |
| HSS | 172.28.0.10 | 加入者のデータベース。I-CSCF と S-CSCF に Diameter Cx で、IP-SM-GW に Sh で答える。 |
| IP-SM-GW | 172.28.0.70 | SMS over IP のアプリケーションサーバー。S-CSCF はここにも利用者を登録する（ステップ 15）。 |
| DNS | 172.28.0.2 | ホーム網のドメインの NAPTR、SRV、A レコードを持つ。 |

ホーム網のドメインは `ims.mnc001.mcc001.3gppnetwork.org` です。試験用の利用者の値は次のとおりです。

- 秘密の識別子（private identity）。`001010000000001@ims.mnc001.mcc001.3gppnetwork.org`。
- 公開の識別子（public identity）。`sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org`。
- パスワード。`test-password-1`。

この手順書の UE は SIM カードの無いソフトフォンです。識別子とパスワードは、IMC に入っているのと同じように設定で与えられます（TS 24.229 5.1.1.1B.1）。そのため UE は与えられた公開の識別子で登録し、ほかのリクエストでもその識別子を使います。試験用のデータでは IMSI から作った識別子と同じ形にしていますが、これは設定で与えた識別子で、規制されていません。USIM があり ISIM の無い VoLTE の端末は、代わりに IMSI から作った一時的な公開の識別子で登録し、網はその識別子を規制します。詳しくは [ISIM の無い端末の識別子](volte.md#isim-の無い端末の識別子) を参照してください。

## 登録を実行する

1. リポジトリのルートで構成を起動します。

   ```sh
   docker compose up -d --build
   ```

2. 10 秒ほど待ちます。I-CSCF と S-CSCF が HSS との Diameter の接続を開くまでの時間です。`docker compose logs scscf | grep I-Open` を実行して 1 行表示されれば、接続が開いています。
3. UE を登録し、5 秒後に登録を解除します。

   ```sh
   docker compose run --rm ue register --deregister-after 5s
   ```

   ログの最後に `"msg":"registered"` が出ます。この行には Service-Route と、一緒に登録された識別子も出ます。続いて `"state":"active"` の `"msg":"reg event"` が出て、その後に `"msg":"deregistered"` が出ます。

4. 登録が拒否される様子を見るには、誤ったパスワードを使います。

   ```sh
   docker compose run --rm ue register --password wrong-password
   ```

   UE は `registration refused with 403` で終わります。

`--deregister-after` を付けないと、UE は Ctrl+C で止めるまで登録したままになります。その間、登録の期限が切れる前に再登録します。

## キャプチャを開く

`captures/` の一番新しいファイルを Wireshark で開き、次の表示フィルタを使います。

```text
sip || diameter.cmd.code >= 300 || dns.qry.type == 35 || dns.qry.type == 33
```

SIP のメッセージ、Cx のメッセージ、NAPTR と SRV の問い合わせが表示されます。Diameter の監視（DWR と DWA）は表示されません。**Statistics > Flow Graph** で **Limit to display filter** を選ぶと、次のラダー図が描かれます。

## シーケンス

```text
 UE           P-CSCF         DNS          I-CSCF          S-CSCF          HSS
 |  REGISTER    |             |              |               |              |
 |------------->|  NAPTR, SRV, A             |               |              |
 |  (1)         |------------>|              |               |              |
 |              |  REGISTER (2)              |               |              |
 |              |--------------------------->|  UAR (3)      |              |
 |              |             |              |----------------------------->|
 |              |             |              |  UAA FIRST_REGISTRATION      |
 |              |             |              |<-----------------------------|
 |              |             |              |  REGISTER (4) |              |
 |              |             |              |-------------->|  MAR (5)     |
 |              |             |              |               |------------->|
 |              |             |              |               |  MAA H(A1)   |
 |              |             |              |  401 (6)      |<-------------|
 |  401         |  401        |              |<--------------|              |
 |<-------------|<---------------------------|               |              |
 |  REGISTER with the digest response (7)    |               |              |
 |------------->|--------------------------->|  UAR, UAA SUBSEQUENT_REGISTRATION (8)
 |              |             |              |<---------------------------->|
 |              |             |              |  REGISTER     |              |
 |              |             |              |-------------->|  SAR, SAA (9)|
 |              |             |              |               |<------------>|
 |              |             |              |  200 OK (10)  |              |
 |  200 OK      |  200 OK     |              |<--------------|              |
 |<-------------|<---------------------------|               |              |
```

### 1. UE が最初の REGISTER を送る

TS 24.229 5.1.1.2.1 と 5.1.1.2.3 です。次のヘッダを見てください。

- **Request-URI.** サーバーではなくホーム網のドメインです。どのサーバーが処理するかは、網の側が決めます。
- **From と To.** 登録する公開の識別子です。
- **Contact.** UE のアドレスで、パラメータが 2 つ付いています。`+g.3gpp.smsip` は機能タグ（RFC 3840）で、UE が IP で SMS を受けられることを表します（TS 24.341 5.3.2.2）。`expires=600000` は、TS 24.229 で UE が求めると決まっている登録の期間です。
- **Authorization.** `username` に秘密の識別子が入り、`nonce` と `response` は空です。まだ答える認証の要求が無いのですが、どの秘密の識別子を認証すればよいかを網に伝えています。
- **Via.** 値の無い `rport` が付いています。応答を、要求が実際に来たアドレスとポートへ返すよう P-CSCF に頼んでいます（RFC 3581）。

### 2. P-CSCF が I-CSCF を探して REGISTER を転送する

P-CSCF は Request-URI のドメインを RFC 3263 の手順で DNS に問い合わせます。

1. **NAPTR** `ims.mnc001.mcc001.3gppnetwork.org` を引きます。UDP の SIP が `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org` で提供されていると分かります。
2. **SRV** `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org` を引きます。`icscf.ims.mnc001.mcc001.3gppnetwork.org` のポート 5060 と分かります。
3. **A** `icscf.ims.mnc001.mcc001.3gppnetwork.org` を引きます。172.28.0.20 と分かります。

レコードは `deploy/compose/dns/ims.mnc001.mcc001.3gppnetwork.org.zone` にあります。

P-CSCF は転送する前に次のヘッダを付けます（TS 24.229 5.2.2.1）。

- **Path.** 自分の URI `sip:term@pcscf...` です。この UE への要求（着信の INVITE など）は、この経路で P-CSCF を通って戻ってきます（RFC 3327）。
- **Require: path.** レジストラに Path への対応を求めます。
- **P-Charging-Vector.** 新しい課金の識別子（`icid-value`）です。
- **P-Visited-Network-ID.** P-CSCF がいる網の名前です。

P-CSCF は UE の Via に `received` と `rport` を書き込みます。要求が実際にどこから来たかの記録です。

### 3. I-CSCF が HSS に問い合わせる（UAR と UAA）

I-CSCF はこの利用者を担当する S-CSCF を知りません。そこで **User-Authorization-Request**（UAR、Cx のコマンド 300）を HSS に送ります。TS 24.229 5.3.1.2 と TS 29.228 6.1.1 です。

Wireshark で Diameter の部分を開き、次の AVP を見てください。

- `User-Name`。秘密の識別子です。
- `Public-Identity`。
- `Visited-Network-Identifier`。
- `User-Authorization-Type`。

HSS は **DIAMETER_FIRST_REGISTRATION**（Experimental-Result-Code 2001、TS 29.229 6.2.1.1）で答えます。まだどの S-CSCF も割り当てられていないという意味です。応答には、S-CSCF の名前の代わりに **Server-Capabilities**（ここでは必須の能力 1）が入っています。I-CSCF は必須の能力をすべて持つ S-CSCF を、設定の一覧から選びます（TS 29.228 6.7）。

この 2001 は Result-Code ではなく **Experimental-Result** の中にあります。同じ番号でも、Result-Code に入っていれば DIAMETER_SUCCESS の意味になります。どの AVP に入っているかで区別します。

### 4. I-CSCF が REGISTER を S-CSCF へ転送する

I-CSCF は Request-URI を、選んだ S-CSCF の `sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060` に書き換えて転送します。一番上に自分の Via を付けます。

これで REGISTER には、UE、P-CSCF、I-CSCF の 3 つの Via が付きます。応答はこの一覧を逆にたどって戻ります。

### 5. S-CSCF が認証の情報を求める（MAR と MAA）

S-CSCF は **Multimedia-Auth-Request**（MAR、Cx のコマンド 303）を送ります。TS 24.229 5.4.1.2.1 と TS 29.228 6.3 です。

MAR の `3GPP-SIP-Authentication-Scheme` は `Unknown` です。REGISTER に `integrity-protected` パラメータが無かったため、S-CSCF は認証方式の選択を HSS に任せています（TS 24.229 5.4.1.1 の 9 番目の項目）。

HSS は方式 `SIP Digest` と **SIP-Digest-Authenticate** の AVP で答えます。中身は次のとおりです。

- `Digest-Realm`。レルムです。
- `Digest-Algorithm`。`MD5` です。
- `Digest-QoP`。`auth` です。
- `Digest-HA1`。ユーザー名、レルム、パスワードのハッシュ H(A1) です（RFC 2617 3.2.2.2）。

HSS はパスワードそのものを渡しません。S-CSCF は H(A1) だけで UE の答えを確かめられます。

HSS はこの S-CSCF を利用者の担当として記録し、認証中の印を付けます。これは手順 8 で使われます。

### 6. S-CSCF が UE に認証を求める（401）

S-CSCF は **401 Unauthorized** で答えます。**WWW-Authenticate** ヘッダには次の値が入っています（TS 24.229 5.4.1.2.1B）。

- レルム。
- S-CSCF が作った `nonce`。
- `algorithm=MD5`。
- `qop="auth"`。

同時に、タイマー **reg-await-auth**（4 分、TS 24.229 Table 7.7.1）を始めます。

401 は I-CSCF と P-CSCF を通って戻ります。各ノードは自分の Via を取り除きます。そのため UE に届く応答には、UE 自身の Via だけが残っています。

### 7. UE が認証の要求に答える

UE はパスワードと nonce からダイジェストの応答を計算し（RFC 2617 3.2.2.1）、2 回目の REGISTER を送ります（TS 24.229 5.1.1.5.4）。

- **Call-ID** は 1 回目と同じで、**CSeq** は 1 つ増えます。
- **Authorization** には、nonce、`cnonce`、`nc=00000001`、`qop=auth`、`response` が入ります。

P-CSCF は Authorization ヘッダに `integrity-protected="ip-assoc-pending"` を付けます（TS 24.229 5.2.2.3）。この REGISTER が認証の要求への答えで、IP の対応付け（IP association）がまだ無いことを S-CSCF に伝えるためです。

### 8. I-CSCF がもう一度 HSS に問い合わせる

REGISTER はすべて I-CSCF を通ります。I-CSCF は登録の状態を持たないので、もう一度 UAR を送ります。

今度は、HSS は **DIAMETER_SUBSEQUENT_REGISTRATION**（2002）で答えます。応答には、手順 5 で記録した S-CSCF の **Server-Name** が入っています（TS 29.228 6.1.1.1）。そのため、要求は同じ S-CSCF に届きます。

この REGISTER は認証の情報を含むため、I-CSCF が転送するときには 1300 バイトを超えます。経路の MTU が分からないとき、RFC 3261 18.1.1 はこの大きさの要求を UDP で送らないように定めています。そのため I-CSCF は、S-CSCF の 5060 番ポートへ TCP の接続を開いて、TCP で送ります。Via は `SIP/2.0/TCP` になります。S-CSCF は、同じ接続で応答を返します（RFC 3261 18.2.2）。I-CSCF のログには `request larger than 1300 bytes sent over TCP` と出ます。決め手は大きさだけなので、手順 4 の REGISTER のように短い要求は、UDP で送られることがあります。

### 9. S-CSCF が答えを確かめて HSS に知らせる（SAR と SAA）

S-CSCF は次のことを確かめます（TS 24.229 5.4.1.2.2A）。

- Call-ID が、認証を求めた REGISTER と同じであること。
- レルムと nonce が自分の出したものであること。
- response が、H(A1) から自分で計算した値と一致すること。

次に、`Server-Assignment-Type` を **REGISTRATION**（1）にした **Server-Assignment-Request**（SAR、Cx のコマンド 301）を送ります。HSS は、利用者がこの S-CSCF に登録されたと記録します。そして、利用者のプロファイルを **User-Data** で返します。Wireshark での名前は `Cx-User-Data` です。プロファイルは XML の文書で（TS 29.228 Annex E）、一緒に登録される識別子の一覧が入っています。利用者の要求にどのアプリケーションサーバーを加えるかを S-CSCF に伝える iFC（initial filter criteria）も入っています。`deploy/compose/hss.yaml` では、すべての利用者に SMS over IP の iFC を 2 つ設定しています。1 つ目の iFC が REGISTER で何をするかは、ステップ 15 で説明します。

### 10. S-CSCF が登録を受け付ける（200 OK）

200 OK には、この後 UE と P-CSCF が使う情報が入っています（TS 24.229 5.4.1.2.2F）。

- **Path.** S-CSCF が記録した経路です。
- **Service-Route.** `sip:orig@scscf...` です。UE はこの後の要求の先頭にこれを付けます。そうすると、要求がこの S-CSCF に届きます（RFC 3608）。
- **P-Associated-URI.** 一緒に登録された識別子です。HSS が規制する識別子は入りません。最初のものが既定の識別子です。
- **Contact.** `+g.3gpp.smsip` と `expires=3600` が付いています。S-CSCF は Contact のパラメータを登録先と一緒に保存し、ここに並べて返します。S-CSCF はあとで、要求の caller preferences をこのパラメータと照らし合わせます（RFC 3841、TS 24.229 5.4.3.3）。IP-SM-GW が送る SMS は、この機能タグを持つ登録先を求めます。UE は 600000 秒を求めましたが、S-CSCF が設定の上限まで短くしました。
- **Authentication-Info.** `rspauth` が入っています。S-CSCF も H(A1) を知っていることの証明です（RFC 2617 3.2.3）。UE はこれを確かめます。

P-CSCF は 200 OK を見て次のことをします（TS 24.229 5.2.2.1、5.2.2.3）。

- UE のアドレスについて、IP の対応付けを作ります。
- Service-Route と識別子を記録します。

## 登録の状態を購読する

登録の後、P-CSCF と UE は、利用者の登録が変わるたびに知らせるよう S-CSCF に頼みます。これには reg イベントパッケージを使います。`Event: reg` の SUBSCRIBE で購読し、S-CSCF は登録の状態を XML の文書にして NOTIFY で送ります（RFC 3680、RFC 6665）。こうして、UE が自分で解除しなかった登録の終わりも分かります。たとえば、期限切れです。

```text
 P-CSCF                       I-CSCF          S-CSCF          HSS
 |  SUBSCRIBE Event: reg (11)  |               |              |
 |---------------------------->|  LIR, LIA     |              |
 |                             |<---------------------------->|
 |                             |  SUBSCRIBE    |              |
 |                             |-------------->|              |
 |  200 OK                     |  200 OK       |              |
 |<----------------------------|<--------------|              |
 |  NOTIFY (12)                                |              |
 |<--------------------------------------------|              |
 |  200 OK                                     |              |
 |-------------------------------------------->|              |
```

### 11. P-CSCF が購読する（SUBSCRIBE）

P-CSCF は、200 OK を UE に渡した直後に SUBSCRIBE を送ります（TS 24.229 5.2.3）。購読は秘密の識別子ごとに 1 回で、最初の登録の後に行います。再登録では繰り返しません。

- **Request-URI と To.** 既定の公開の識別子です。200 OK の最初の P-Associated-URI です。
- **From.** P-CSCF 自身の URI、`sip:pcscf...:5060` です。
- **Event.** `reg` です。
- **Expires.** 3761 です。登録の 3600 秒より長くする決まりです。
- **P-Asserted-Identity.** P-CSCF が Path に入れた URI、`sip:term@pcscf...` です。S-CSCF はこれで購読者を確かめます。

P-CSCF はホーム網の中にあるので、SUBSCRIBE は I-CSCF へ送ります。I-CSCF の場所は、REGISTER のときと同じく DNS で分かります。I-CSCF は、利用者がどこにいるかを **Location-Info-Request**（LIR、Cx のコマンド 302、TS 29.228 6.1.4）で HSS に問い合わせます。そして、応答に書かれた S-CSCF へ SUBSCRIBE を転送します。

S-CSCF は次の 2 つを確かめて、購読を受け付けます（TS 24.229 5.4.2.1.1）。

- 利用者の登録があること。無ければ 480 で答えます。
- 購読者が、利用者本人か、登録の Path にある P-CSCF か、利用者の iFC にあるアプリケーションサーバーであること。違えば 403 で答えます。

S-CSCF は、Expires と自分の Contact を付けた 200 OK で答えます。この 200 で、P-CSCF と S-CSCF の間にダイアログができます。通話の無いダイアログです（RFC 6665 4.4.1）。

### 12. S-CSCF が状態を知らせる（NOTIFY）

S-CSCF はすぐに NOTIFY を送ります（RFC 6665 4.2.1.2）。I-CSCF は経路に残らなかったので、NOTIFY は P-CSCF の Contact へ直接届きます。次のヘッダを見てください。

- **Event.** `reg` です。
- **Subscription-State.** `active;expires=3761` です。
- **Content-Type.** `application/reginfo+xml` です。

Wireshark では、本文が XML として表示されます。

```xml
<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="0" state="full">
  <registration aor="sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org" id="reg1" state="active">
    <contact id="..." state="active" event="registered" expires="3600" callid="..." cseq="2">
      <uri>sip:001010000000001@172.28.0.100:5060</uri>
      <unknown-param name="+g.3gpp.smsip"></unknown-param>
    </contact>
  </registration>
</reginfo>
```

一緒に登録された識別子ごとに `registration` が 1 つあり、その中に登録先（バインディング）ごとの `contact` があります。Contact のパラメータのうち、機能タグのように RFC 3261 で決まっていないものは、1 つずつ `unknown-param` になります（RFC 3680 5.1、TS 24.229 5.4.2.1.2）。`version` は、この購読で送った文書の番号で、0 から数えます。S-CSCF は毎回、状態のすべてを送ります（TS 24.229 5.4.2.1.2）。P-CSCF は 200 OK で答えます。

登録が変わるたびに、S-CSCF は NOTIFY を送ります。再登録の後は、contact が `event="refreshed"` になります。

UE も購読します。UE の購読は別の経路を通ります。

```text
 UE           P-CSCF                        S-CSCF
 |  SUBSCRIBE Event: reg (13)  |              |
 |------------->|  SUBSCRIBE                  |
 |              |---------------------------->|
 |  200 OK      |  200 OK                     |
 |<-------------|<----------------------------|
 |  NOTIFY      |  NOTIFY (14)                |
 |<-------------|<----------------------------|
 |  200 OK      |  200 OK                     |
 |------------->|---------------------------->|
```

### 13. UE が購読する（SUBSCRIBE）

UE は、登録への 200 OK を受けた後に、自分の登録の状態を購読します（TS 24.229 5.1.1.3）。

- **Request-URI、From、To.** 既定の公開の識別子です。
- **Event と Expires.** Event は `reg` です。Expires は 600000 で、5.1.1.3 が UE に決めている値です。
- **Route.** P-CSCF と Service-Route です。UE が送るほかの要求と同じです。

P-CSCF は、この SUBSCRIBE を UE からのほかの要求と同じように扱います。利用者の識別子を入れた P-Asserted-Identity を付けます。ダイアログの経路に残るために、Record-Route も付けます。SUBSCRIBE は Service-Route に沿って S-CSCF に届き、I-CSCF は通りません。S-CSCF は、P-Asserted-Identity が利用者本人の識別子なので購読を受け付け（TS 24.229 5.4.2.1.1）、200 OK で答えます。

### 14. NOTIFY が P-CSCF を通って UE に届く

NOTIFY の Route には P-CSCF が入っています。SUBSCRIBE の Record-Route から作られたものです。P-CSCF は、NOTIFY にも自分の Record-Route を付けます（TS 24.229 5.2.6.4.5）。UE は購読の経路を、200 OK ではなく NOTIFY から作ります（RFC 6665 4.4.1）。そのため、UE が後で送る SUBSCRIBE も P-CSCF を通ります。本文は、手順 12 と同じ reginfo です。UE は状態を `"msg":"reg event"` としてログに出します。購読は、期限の 600 秒前に更新します（TS 24.229 5.1.1.3）。

## アプリケーションサーバーにも登録する（第三者 REGISTER）

プロファイルの 1 つ目の iFC はすべての REGISTER に一致し、SMS over IP のアプリケーションサーバーである IP-SM-GW を指しています。そのため S-CSCF は UE に 200 OK を返したあと、IP-SM-GW にも利用者を登録します。IP-SM-GW は P-CSCF と同じように登録の状態を購読し、この利用者の SMS を受け持つことを Sh で HSS に知らせます。この流れの Sh のメッセージは `diameter.cmd.code == 307` で表示できます。

```text
 S-CSCF            IP-SM-GW          I-CSCF            HSS
 |  REGISTER (15)    |                 |                 |
 |------------------>|                 |                 |
 |  200 OK           |                 |                 |
 |<------------------|                 |                 |
 |                   |  SUBSCRIBE (16) |                 |
 |                   |---------------->|  LIR, LIA       |
 |                   |                 |<--------------->|
 |  SUBSCRIBE        |                 |                 |
 |<------------------------------------|                 |
 |  200 OK           |                 |                 |
 |------------------------------------>|  200 OK         |
 |  NOTIFY (17)      |<----------------|                 |
 |------------------>|                 |                 |
 |  200 OK           |                 |                 |
 |<------------------|  PUR, PUA (18)  |                 |
 |                   |<--------------------------------->|
```

### 15. S-CSCF が IP-SM-GW に利用者を登録する（REGISTER）

S-CSCF は UE の REGISTER を iFC と照らし合わせます。一致した iFC ごとに、S-CSCF は自分で REGISTER を送ります。これを第三者 REGISTER と呼びます（TS 24.229 5.4.1.7）。

- **Request-URI.** iFC の ServerName、`sip:ipsmgw...:5060` です。
- **From と Contact.** S-CSCF です。**To** は既定の公開の識別子です。
- **Expires.** UE に返したのと同じ 3600 です。
- **本文.** `application/3gpp-ims+xml` で、iFC の ServiceInfo が `<service-info>` に入っています（5.4.1.7A）。ここでは MSISDN の `12025550101` です。`hss.yaml` には `{msisdn}` と書いてあり、HSS が利用者ごとの番号に置き換えます。

IP-SM-GW は Contact に登録を並べた 200 OK で答えます（RFC 3261 10.3。TS 24.341 の表 B.3-2 の例も同じです）。UE はこの答えを待ちません。UE はもう 200 OK を受け取っているからです。IP-SM-GW が答えないときは、iFC の DefaultHandling に従います。ここでは SESSION_CONTINUED なので、登録はそのまま残ります。

### 16. IP-SM-GW が購読する（SUBSCRIBE）

IP-SM-GW は To の利用者の登録の状態を購読します（TS 24.341 5.3.3.2、TS 24.229 5.7.1.1）。SUBSCRIBE はステップ 11 の P-CSCF のものと似ています。

- **Request-URI と To.** 公開の識別子です。**From** と **Contact** は IP-SM-GW です。
- **Expires.** 3761 です。登録の 3600 秒に、RFC 3680 4.4 が 2 つの既定値の間に置いた 161 秒を足した値です。
- **P-Asserted-Identity.** IP-SM-GW の URI です。これが利用者の iFC の ServerName なので、S-CSCF は購読を受け付けます（TS 24.229 5.4.2.1.1）。

IP-SM-GW のほかの要求と同じく、SUBSCRIBE はホーム網の入口へ送ります。I-CSCF が LIR で S-CSCF を探します。再登録のたびに第三者 REGISTER が届くので、IP-SM-GW はそのたびに購読のダイアログの中で S-CSCF へ直接 SUBSCRIBE を送り、購読を更新します。

### 17. S-CSCF が状態を知らせる（NOTIFY）

S-CSCF はステップ 12 と同じようにすぐに NOTIFY を送ります。宛先は IP-SM-GW の Contact です。SUBSCRIBE への 200 OK は I-CSCF を通って戻るので、NOTIFY のほうが先に届くことがあります。これは決まりどおりの動きです。購読のダイアログを作るのは、200 OK ではなく NOTIFY だからです（RFC 6665 4.4.1）。

IP-SM-GW は reginfo の中に `<unknown-param name="+g.3gpp.smsip">` を持つ active の contact があるかを調べます。この機能タグは、UE が SMS over IP を受信できることを示します（TS 24.341 5.3.2.2）。

### 18. IP-SM-GW が HSS に知らせる（PUR と PUA）

利用者は IP-SM-GW を通して SMS を受け取れるようになったので、IP-SM-GW は HSS に知らせます（TS 24.341 5.3.3.2）。送るのは **Profile-Update-Request**（PUR、Sh のコマンド 307、TS 29.328 6.1.2）で、次の AVP が入っています。

- **User-Identity.** 第三者 REGISTER の **MSISDN** です。Wireshark は数字の `12025550101` として表示します。
- **Data-Reference.** **SMSRegistrationInfo**（24）です。
- **Sh-User-Data.** XML の文書で、`<IP-SM-GW-Number>` に IP-SM-GW の番号 `12025550199` が入っています。

HSS は DIAMETER_SUCCESS（2001）の **Profile-Update-Answer**（PUA）で答えます。TS 24.341 はこの更新を MAP で説明していますが、このプロジェクトでは Sh を使います。

UE が `+g.3gpp.smsip` を付けずに登録したときは、PUR は送られません。利用者の contact に SMS を受け取れるものが無いことが、NOTIFY から分かるからです。

## 登録の解除

UE は Contact に `expires=0` を付けた REGISTER を送ります（TS 24.229 5.1.1.6.1）。

- P-CSCF は IP の対応付けを見つけ、`integrity-protected="ip-assoc-yes"` を付けます。
- I-CSCF は `User-Authorization-Type` を **DE_REGISTRATION**（1）にした UAR を送ります。
- S-CSCF は、IP の対応付けがあるので、認証を求め直しません（TS 24.229 5.4.1.4.1）。**USER_DEREGISTRATION**（5）の SAR を送り、HSS から SAA が返ってから `expires=0` の 200 OK で答えます（TS 23.228 5.3.1）。
- S-CSCF は 200 OK の後に、それぞれの購読へ最後の NOTIFY を送ります。registration と contact は `terminated` で、contact は `event="unregistered"` です。Subscription-State は `terminated;reason=noresource` です。この NOTIFY で、購読とそのダイアログが終わります（TS 24.229 5.4.2.1.2）。
- IP-SM-GW にもこの NOTIFY が届きます。IP-SM-GW は `<IP-SM-GW-Number>` を空にした PUR を送り、HSS から自分の登録を消します。S-CSCF は Expires を 0 にした第三者 REGISTER も IP-SM-GW に送り、IP-SM-GW は 200 OK で答えます。

UE は、登録の解除に成功した時点で、購読は終わったものとして扱います（TS 24.229 5.1.1.6.1）。それでも、最後の NOTIFY が 1 秒以内に届けば答えます。P-CSCF は、この NOTIFY を UE へ渡します。P-CSCF は UE からの要求をもう受け付けませんが、登録の解除のサーバートランザクションが終わるまでは、IP の対応付けを残すからです。UDP では 32 秒です（5.2.5.1 の 2 番目の項目）。

UE が期限までに再登録しないと、登録の期限が切れます。そのとき S-CSCF は `event="expired"` の NOTIFY を送り、続けて **TIMEOUT_DEREGISTRATION**（4）の SAR を送ります。P-CSCF はこの NOTIFY を受けて、同じように UE の IP の対応付けを終わらせます。UE あての NOTIFY は、それでも届きます（TS 24.229 5.2.5.2 と NOTE 2）。UE も、登録が無くなったことを知ります。`ue register` は、そこでエラーとして終了します。`ue call` と `ue answer` はログに出すだけです。`ue serve` は、登録を頼まれるまで未登録のままです。S-CSCF が自分の判断で `event="deactivated"` として登録を終わらせたときは、UE は登録し直します。TS 24.229 5.1.1.7 の決まりです。

## 加入を削除する（HSS からの登録解除）

HSS には HTTP で加入を登録、削除する API（プロビジョニングの API）があります。hss の設定キー `provisioning_listen` に書いたアドレス（`:8081` など）で待ち受けます。この API は 3GPP のインタフェースではなく、このプロジェクト独自のものです。製品の HSS にも同じような API がありますが、どれも製品ごとに独自です。JSON のフィールド名は設定ファイルのキーと同じです。

| 要求 | 内容 |
| --- | --- |
| `GET /subscriptions` | 加入の一覧を、登録の状態と一緒に返す（最大 1000 件）。 |
| `GET /subscriptions/{秘密の識別子}` | 1 件の加入を返す。 |
| `POST /subscriptions` | 加入を 1 件追加する。本文は `{"private_identity": "...", "public_identities": ["sip:...", "tel:..."], "password": "..."}`。`"tel": "tel:+..."` を付けると、その tel URI も公開の識別子に加わる。IMS-AKA を使う加入には、16 進数で書いた `"aka": {"k": "...", "opc": "...", "amf": "...", "sqn": "..."}` を、password と一緒に、または password の代わりに付ける。`"ut_password": "..."` を付けると、利用者が Ut で補足サービスを設定できる。HSS はその H(A1) だけを Sh の Repository Data として持つ。`"mmtel_services": "<MMTelServices ...>"` は補足サービスの最初の文書で、省くと hss のキー `mmtel_services` の文書が入る。 |
| `DELETE /subscriptions/{秘密の識別子}` | 加入を 1 件削除する。 |

応答にパスワードと AKA の鍵（K と OPc）は入りません。その加入で使える認証の方式を `authentication_schemes` に並べます。追加する加入には設定ファイルと同じ決まりを当てはめます。項目が足りないときや、公開の識別子が SIP か tel の URI でないときは 400 を返します。識別子がすでにほかの加入にあるときは 409 を返します。

登録中の利用者の加入を削除すると、S-CSCF でもその利用者の登録が解除されます。Wireshark では、`diameter.cmd.code == 304` でこの流れの Cx メッセージが見られます。

```text
 UE           P-CSCF                         S-CSCF          HSS
 |             |                               |               |  DELETE /subscriptions/...
 |             |                               |  RTR (1)      |
 |             |                               |<--------------|
 |             |  NOTIFY event="rejected" (2)  |               |
 |  NOTIFY     |<------------------------------|               |
 |<------------|                               |  RTA (3)      |
 |             |                               |-------------->|
```

1. HSS は加入を削除し、利用者を登録した S-CSCF に **Registration-Termination-Request**（RTR、Cx のコマンド 304）を送ります（TS 29.228 6.1.3）。Destination-Host はその S-CSCF が送った SAR の Origin-Host で、User-Name は秘密の識別子です。**Deregistration-Reason** の Reason-Code は **PERMANENT_TERMINATION**（0）です（TS 29.229 6.3.16、6.3.17）。秘密の識別子とそれに属する公開の識別子をすべて解除するので、Public-Identity は付けません（TS 29.228 6.1.3.1）。
2. S-CSCF は登録を消し、それぞれの購読へ最後の NOTIFY を送ります（TS 24.229 5.4.1.5）。registration と contact は `terminated` で、contact は `event="rejected"` です。PERMANENT_TERMINATION はほかの S-CSCF も割り当てられないという意味で、登録し直しても無駄なので rejected にします。P-CSCF は登録の期限が切れたときと同じように IP の対応付けを終わらせます。
3. S-CSCF は DIAMETER_SUCCESS（2001）の **Registration-Termination-Answer**（RTA）で答えます。HSS がすでに登録を解除しているので、SAR は送りません（TS 29.228 A.4.4.2）。S-CSCF が知らない利用者の RTR には、DIAMETER_ERROR_USER_UNKNOWN（5001）で答えます（TS 29.229 6.2.2.1）。

UE は `event="rejected"` を受けると、登録し直しません（TS 24.229 5.1.1.7）。`ue register` はエラーとして終了します。もう一度 `ue register` を実行すると、**403 Forbidden** が返ります。HSS は UAR に DIAMETER_ERROR_USER_UNKNOWN で答え（TS 29.228 6.1.1.1）、I-CSCF は失敗の UAA を 403 にして返すからです（TS 24.229 5.3.1.3）。

## パスワードが誤っているとき

手順 1 から 8 までは同じです。手順 9 で response が一致しません。

- S-CSCF は **403 Forbidden** で答えます（TS 24.229 5.4.1.2.3B）。
- S-CSCF は **AUTHENTICATION_FAILURE**（9）の SAR を送ります。
- HSS は記録していた S-CSCF を消します（TS 29.228 6.1.2.1）。次の試行はまた FIRST_REGISTRATION から始まります。

## IP の対応付けが無い要求

P-CSCF が扱うのは、自分を通って登録した UE だけです。IP の対応付けに当たらない、REGISTER 以外の要求は捨てます。P-CSCF が登録の情報を失った UE からの要求も、これに当たります。P-CSCF は何も返さず、100 Trying も返しません。S-CSCF へも転送しません（TS 24.229 5.2.6.3.2A）。応答を返すと、知らない送り手に、このアドレスで P-CSCF が待ち受けていると教えてしまうからです。UE から見えるのは、応答が来ないことだけです。`ue serve` の要求でタイマー B かタイマー F が切れると、UE は自分の Contact がどの識別子にも結び付いていないとみなし、最初から登録し直します（TS 24.229 5.1.2A.1.6）。`ue call` と `ue sms` は時間切れを報告して終了します。

`scripts/e2e-registration.sh` の登録の試験は最後にこの要求を送ります。UE が登録を解除したあと、UDP で再送する UE と同じように、UE のアドレスから MESSAGE を 2 回送ります。Wireshark で `sip.Call-ID contains "unregistered"` と入力すると、2 つの MESSAGE が P-CSCF に届き、何も返らないことが分かります。P-CSCF のログには `discarded a request that does not map to an IP association` と出ます。

網から UE への要求には、IP の対応付けは要りません。この要求は UE からではなく S-CSCF から届くからです。

## 信頼のドメインの境界

1 つの事業者のノードどうしはいくつかのヘッダを互いに信頼して受け渡します。網が保証する識別子、課金の情報、サービスを受ける利用者などです。UE はこの信頼のドメインの外にあり、P-CSCF が UE に向いた境界です（TS 24.229 4.4.1）。境界を越える要求と応答は、REGISTER とその 401 と 200 も含めて、すべて `internal/pcscf/boundary.go` の 1 つの表を通ります。

| ヘッダ | UE から | UE へ | 仕様 |
| --- | --- | --- | --- |
| P-Charging-Vector、P-Charging-Function-Addresses | 消す | 消す | TS 24.229 5.2.1 |
| P-Media-Authorization | 消す | 消す | TS 24.229 5.2.1 |
| P-Access-Network-Info | UE 自身のものは残す。`network-provided` 付きのものは消す | 消す | TS 24.229 5.2.1 の 3 番目の項目、4.4.3、RFC 7315 |
| Feature-Caps | 消す | 残す | TS 24.229 5.2.1 の 5 番目の項目、4.4.13 |
| Geolocation | `loc-src` パラメータを消す | 残す | TS 24.229 5.2.1 の 8 番目の項目 |
| P-Asserted-Identity、P-Preferred-Identity | 消す。識別子は P-CSCF が自分で保証する | Privacy に `id` があれば P-Asserted-Identity を消す | TS 24.229 4.4.2、5.2.6.3.3 の手順 6、5.2.6.4.4 の手順 1、RFC 3325 |
| P-Served-User、P-Profile-Key | 消す | 消す | TS 24.229 4.4.9、4.4.8、RFC 5502、RFC 5002 |
| P-Asserted-Service、P-Private-Network-Indication | 要求から消す | 要求から消す | TS 24.229 4.4.5、4.4.10、RFC 6050 |
| Resource-Priority | 残す | 消す | TS 24.229 4.4.6 |
| Relayed-Charge、Service-Interact-Info | 消す | 消す | TS 24.229 4.4.17、4.4.18 |
| Cellular-Network-Info | 残す | 要求から消す | TS 24.229 4.4.19 |

UE から受けて転送する要求には、P-CSCF が自分の P-Charging-Vector を付けます（5.2.2.1 の 3 番目の項目、5.2.6.3.3 の手順 7）。Wireshark で `sip.P-Charging-Vector && ip.src == 172.28.0.40 && ip.dst == 172.28.0.100` と入力しても、何も見つかりません。REGISTER への 401 と 200 も、P-Charging-Vector を付けずに UE に届きます。

UE への応答の Reason は残します。アプリケーションサーバーがアナウンスを流してから通話を断るときは、理由を Reason に入れて発信者へ送るからです（TS 24.628 Annex G）。TS 24.229 4.4.1 は、ヘッダごとに信頼のドメインの境界が違ってよいとしています。

## 自分のマシンから NAT 越しに登録する

P-CSCF のポート 5060/udp はホストに公開されています。ホスト上の UE は Docker の NAT を通って P-CSCF に届きます。そのため、P-CSCF から見た送信元のアドレスとポートは、UE が Via に書いたものと違います（Docker はホストとコンテナの間に NAT を置きます）。P-CSCF が書き込む `rport` と `received` に実際の送信元が残り、応答はそこへ送られます。

試すには、UE シミュレータを自分のマシン用にビルドします。Windows の Git Bash では次のとおりです。

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -w /src -e GOOS=windows golang:1.27.4-alpine go build -o ue.exe ./cmd/ue
./ue.exe register -pcscf 127.0.0.1:5060 -listen 127.0.0.1:0 -deregister-after 5s
rm ue.exe
```

キャプチャでは、REGISTER はブリッジのゲートウェイ 172.28.0.1 から P-CSCF に届きます。一方、その Via には `127.0.0.1` と書かれています。

## このプロジェクトで簡略化していること

仕様のうち、次の部分は省略するか短くしています。コードにはそれぞれ `SIMPLIFIED:` のコメントがあり、`grep -rn "SIMPLIFIED:" internal cmd` ですべて一覧できます。

| 箇所 | 省略していること | 理由 |
| --- | --- | --- |
| P-CSCF。TS 24.229 5.2.2.1 の 1 番目の項目 | Path の URI に、フロートークンと `ob` パラメータを付けない。 | SIP outbound（RFC 5626）の機能で、このプロジェクトでは実装しないため。 |
| S-CSCF。TS 24.229 5.4.1.5 | S-CSCF が自分で登録を解除するのは、アプリケーションサーバーが第三者 REGISTER に失敗し、その iFC が SESSION_TERMINATED のときだけ。ほかは HSS からの RTR でだけ解除する。解除の前にセッションを解放することもしない。 | S-CSCF には保守用の API が無い。また、S-CSCF は中継するダイアログの状態を持たないため。 |
| S-CSCF。TS 29.228 6.1.3.1 | NEW_SERVER_ASSIGNED と REMOVE_S-CSCF の RTR には DIAMETER_UNABLE_TO_COMPLY で答える。 | HSS のスタブは PERMANENT_TERMINATION しか送らない。また、登録していない利用者のプロファイルは、S-CSCF が数分たつと自分で手放すため。 |
| 購読者。RFC 6665 4.1.4、5.4.9 | 2 つ目のダイアログから届いた NOTIFY には 481 を返す。 | reg イベントでは、購読が分岐することを禁じているため（RFC 3680 4.9）。 |
| I-CSCF。TS 24.229 5.3.1.3 | S-CSCF が答えないとき、別の S-CSCF を選び直さない。 | この網の S-CSCF は 1 台のため。 |
| UE。TS 24.229 9.2.1 | この手順書の UE やホストのソフトフォンのように、アクセス網を使わない UE には P-CSCF のアドレスを設定で与える。EPC スタブを使う UE（`ue -epc`）は attach のときに PCO からアドレスを受け取る。[VoLTE の呼の流れ](volte.md) を参照。 | ソフトフォンと、attach しない UE のための経路を残すため。 |
| UE。TS 24.229 5.1.2A.1.6 | 応答の無かった要求のあと、UE はすぐに同じ P-CSCF を通って登録し直す。その P-CSCF を使えないものとして扱ったり、別の P-CSCF を探したりはしない。 | UE が知っている P-CSCF のアドレスは 1 つで、P-CSCF を探し直しても同じアドレスになるため。 |
| UE。TS 24.229 5.1.1.2.1 | 再登録に失敗しても、間隔を空けて再試行しない。`ue register` は終了し、`ue serve` は未登録のまま残る。 | 学習用の構成では、失敗がすぐ分かるほうがよいため。 |
| HSS。TS 29.228 6.1.1.1、6.1.2.1 | 登録の状態を、公開の識別子ごとではなく加入ごとに持つ。一部の Server-Assignment-Type に対応しない。 | 試験用の加入はそれぞれ 1 つの暗黙登録の組で、S-CSCF はほかの値を送らないため。 |
| HSS。TS 29.228 6.1.1.1 の手順 5 | 登録してよい利用者かを確かめない。 | 試験用の加入は全員が登録してよいため。 |
| 利用者のプロファイル。TS 29.228 Annex E | サービスプロファイルは 1 つ。任意の要素は iFC のほかに入れない。 | 試験用の加入者にはそれ以上要らないため。 |
| HSS。TS 29.228 B.2.2 | すべての加入者に設定の iFC を渡す。加入者ごとに違うのは ServiceInfo の MSISDN だけ。 | このプロジェクトには、利用者ごとに違うサービスが無いため。 |
| iFC。TS 29.228 B.2.3、Annex E | SPT の SessionDescription、RegistrationType、ProfilePartIndicator、IncludeRegisterRequest、IncludeRegisterResponse を扱わない。 | SMS と MMTel AS の iFC には、どれも要らないため。RegistrationType は S-CSCF にとって任意の機能。 |
| S-CSCF。TS 24.229 5.4.1.7 | 第三者 REGISTER の To はいつも既定の公開識別子にする。Call-ID は毎回新しくする。応答の term-ioi は保存しない。 | どの識別子にするかは運用者が選べる。Call-ID を変えないには、利用者とサーバーごとに状態が要る。S-CSCF は課金をしないため。 |
| S-CSCF。TS 24.229 5.4.0 | アプリケーションサーバーのアドレスを要求のたびに DNS で引く。最初の第三者 REGISTER を送ったアドレスは覚えない。 | アプリケーションサーバーの役割は、すべての台で共有する Store に状態を置くため。 |
| IP-SM-GW。TS 24.229 5.7.3 | IP-SM-GW の要求（SUBSCRIBE など）に、網の入口への Route を付けない。 | 網の入口はホームドメインの名前で表す。I-CSCF が取り除くのは自分のホスト名の Route だけなので（5.3.2.1）、ドメインの Route を付けると要求に残ってしまうため。 |
| IP-SM-GW。RFC 6665 4.1.2.4 | Timer N を使わない。NOTIFY が来なかった SUBSCRIBE は、次の第三者 REGISTER のときに新しい購読に置き換える。 | S-CSCF は受け付けた SUBSCRIBE のすべてにすぐ NOTIFY を送るため。 |
| IP-SM-GW。TS 24.341 5.3.3.2 | service-info に MSISDN が無いとき、PUR では利用者を公開の識別子で示す。UE の REGISTER の IMSI は使わない。 | IMSI は message/sip の本文に入った UE の REGISTER から取るが、S-CSCF はその本文を送らないため。Sh は公開の識別子も受け付ける（TS 29.328 Table 7.6.1）。 |
| UE。TS 24.229 5.1.1.3 | UE の購読の更新に 481 が返ると、UE は次の初回登録の後まで購読し直さない。 | 更新は、購読の期限よりずっと前に行うため。 |
| UE。TS 24.229 5.1.1.7 | `event="rejected"` を受けても、`ue call`、`ue answer`、`ue serve` は通話を解放しない。 | 登録が拒否されるのは HSS から加入を削除したときだけのため。`ue call` と `ue answer` の通話は音声を送り終えると自分で終わり、`ue serve` の通話は操作する側が切ると終わる。 |
| P-CSCF。TS 24.229 5.2.3 | P-CSCF の購読の更新に 481 が返ると、P-CSCF はすぐには購読し直さない。利用者の次の REGISTER で新しい購読を始める。 | 更新は、購読の期限よりずっと前に行うため。 |
| P-CSCF と UE。RFC 3680 5.2 | reginfo の文書の version を確かめない。 | S-CSCF は毎回すべての状態を送る。また、後の NOTIFY より遅れて届いた NOTIFY は、CSeq で断るため。 |
| P-CSCF。TS 24.229 5.2.6.2 | UE と網は、P-CSCF の同じアドレスとポートに要求を送る。P-CSCF が Path に入れた URI、P-CSCF 自身の Contact、P-CSCF が扱う UE の Contact のどれかにあてた要求を、網からの要求とみなす。網からの要求には IP の対応付けを求めない。 | 実際の P-CSCF は、UE の側（Gm）と網の側（Mw）を別のインタフェースで扱うことが多い。このプロジェクトのコンテナは 1 つのネットワークを共有するため。 |
| P-CSCF。TS 24.229 5.2.2.3 | IP の対応付けは、UDP の要求が届いたアドレスとポートで探す。1300 バイトより大きい要求を、UE は TCP で送る。TCP の要求は、Via の sent-by のアドレスが要求の届いたアドレスと同じなら、sent-by だけで照らし合わせ、接続のポートは確かめない。sent-by が別のアドレスを示す TCP の要求は見分けられない。 | IPsec を使わないと、TCP の接続はそのときどきのポートから来て、そのポートを持つ IP の対応付けが無いため。 |
| SIP Digest。TS 24.229 5.4.1.2.1B、RFC 7616、RFC 8760 | S-CSCF は MD5 と qop=auth だけを提示する。仕様は SHA-256 を優先し、MD5 は後方互換のために残している。 | HSS のスタブが渡すのは MD5 の H(A1) だけで、SHA-256 には別の AVP が必要なため。 |
| Diameter。RFC 3539 3.4.1 と Appendix A、RFC 6733 5.6.4 | REOPEN の状態とフェイルオーバーが無い。接続を始めるのは設定で決めた側だけなので、同じ相手への 2 本の接続のどちらを残すかを選ぶ手順（election）も無い。 | 各 CSCF の接続先の HSS が 1 台のため。 |

## 後片付け

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。
