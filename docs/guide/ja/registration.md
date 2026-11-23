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
| HSS | 172.28.0.10 | 加入者のデータベース。I-CSCF と S-CSCF に Diameter Cx で、アプリケーションサーバーに Sh で答える。ここでは IP-SM-GW。 |
| IP-SM-GW | 172.28.0.70 | SMS over IP のアプリケーションサーバー。S-CSCF はここにも利用者を登録する（ステップ 15）。 |
| DNS | 172.28.0.2 | ホーム網のドメインの NAPTR、SRV、A レコードを持つ。 |

ホーム網のドメインは `ims.mnc001.mcc001.3gppnetwork.org` です。試験用の利用者の値は次のとおりです。

- 秘密の識別子（private identity）。`001010000000001@ims.mnc001.mcc001.3gppnetwork.org`。
- 公開の識別子（public identity）。`sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org`。
- パスワード。`test-password-1`。

この手順書の UE は SIM カードの無いソフトフォンです。識別子とパスワードは、IMC に入っているのと同じように設定で与えられます（TS 24.229 5.1.1.1B.1）。そのため UE は与えられた公開の識別子で登録し、ほかのリクエストでもその識別子を使います。この識別子は利用者の番号の SIP URI です。ほかの網から利用者が見えるのはこの識別子で、規制されていません。USIM があり ISIM の無い VoLTE の端末は、代わりに IMSI から作った一時的な公開の識別子で登録し、網はその識別子を規制します。詳しくは [ISIM の無い端末の識別子](volte.md#isim-の無い端末の識別子) を参照してください。

## 登録を実行する

1. リポジトリのルートで構成を起動します。

   ```sh
   docker compose up -d --build
   ```

2. I-CSCF と S-CSCF が HSS との Diameter の接続を開くまで待ちます。HSS が接続を受け付ける前に起動したノードは、30 秒後（Tc、RFC 6733 2.1）にもう一度つなぎに行くので、長くて 1 分ほどかかります。`docker compose logs scscf | grep I-Open` を実行して 1 行表示されれば、接続が開いています。
3. UE を登録し、5 秒後に登録を解除します。

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   ログの最後に `"msg":"registered"` が出ます。この行には Service-Route と、一緒に登録された識別子も出ます。続いて `"state":"active"` の `"msg":"reg event"` が出て、その後に `"msg":"deregistered"` が出ます。

4. 登録が拒否される様子を見るには、誤ったパスワードを使います。

   ```sh
   docker compose run --rm ue register -password wrong-password
   ```

   UE は `registration refused with 403` で終わります。

`-deregister-after` を付けないと、UE は Ctrl+C で止めるまで登録したままになります。その間、登録の期限が切れる前に再登録します。

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
- **Contact.** UE のアドレスです。ユーザー部は UUID で、利用者の識別子ではありません。TS 24.229 5.1.1.2.1 の項目 c は、世界で一意で、私的な情報を明かさないユーザー部を求めており、その例に時刻をもとにした UUID を NOTE 3 で挙げています。この UE は例の UUID を使わず、秘密の識別子の HMAC-SHA-256 を、パスワード（IMS-AKA では USIM の K）を鍵にして計算し、バージョン 8 の UUID（RFC 9562 5.8）として書きます。鍵を持たない人はこの値から識別子を割り出せません。UE は起動するたびに同じユーザー部を作るので、起動し直したあとの登録は登録先を置き換えずに更新します。UE はあとの REGISTER でも同じユーザー部を使います（5.1.1.4.1）。Contact にはパラメータが 2 つ付いています。`+g.3gpp.smsip` は機能タグ（RFC 3840）で、UE が IP で SMS を受けられることを表します（TS 24.341 5.3.2.2）。`expires=600000` は、TS 24.229 で UE が求めると決まっている登録の期間です。
- **Authorization.** `username` に秘密の識別子が入り、`nonce` と `response` は空です。まだ答える認証の要求が無いのですが、どの秘密の識別子を認証すればよいかを網に伝えています。
- **Via.** 値の無い `rport` が付いています。応答を、要求が実際に来たアドレスとポートへ返すよう P-CSCF に頼んでいます（RFC 3581）。

### 2. P-CSCF が I-CSCF を探して REGISTER を転送する

P-CSCF は Request-URI のドメインを RFC 3263 の手順で DNS に問い合わせます。

1. **NAPTR** `ims.mnc001.mcc001.3gppnetwork.org` を引きます。UDP の SIP が `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org` で提供されていると分かります。
2. **SRV** `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org` を引きます。`icscf.ims.mnc001.mcc001.3gppnetwork.org` のポート 5060 と分かります。
3. **A** `icscf.ims.mnc001.mcc001.3gppnetwork.org` を引きます。172.28.0.20 と分かります。

レコードは `deploy/compose/dns/ims.mnc001.mcc001.3gppnetwork.org.zone` にあります。

I-CSCF を複数台で動かすときは、SRV のレコードが台ごとに 1 つずつ宛先を返し、よいものから並びます。P-CSCF は REGISTER を最初の宛先に送ります。その I-CSCF に届かないとき、タイマー F までに応答が無いとき、3xx、480、503 が返ったときは、Via の branch を変えた新しいトランザクションで次の宛先へ送り、宛先が残っていなければ 504 で答えます（TS 24.229 5.2.2.1 の 7 番目の項目、RFC 3263 4.3）。ここの SIP のノードはほかの要求でも同じように宛先を順に試します。ただし次の宛先へ送るのは、503 が返ったとき、トランスポートの誤りのとき、タイマー B か F までに応答が何も無いときだけです（RFC 3263 4.3）。INVITE の CANCEL と、INVITE への 300～699 の応答に対する ACK は、その INVITE を受け取った宛先へ送ります。この網ではどの名前も 1 台のサーバーを指すので、キャプチャには最初の宛先だけが写ります。タイマー B と F は既定では 32 秒です。ノードの設定で、役割の `sip` の節に `transaction_timeout` を書くと短くなり、止まったサーバーに送った要求が早く次の宛先へ移ります。

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

次に、`Server-Assignment-Type` を **`REGISTRATION`**（1）にした **Server-Assignment-Request**（SAR、Cx のコマンド 301）を送ります。HSS は、利用者がこの S-CSCF に登録されたと記録します。そして、利用者のプロファイルを **User-Data** で返します。Wireshark での名前は `Cx-User-Data` です。プロファイルは XML の文書で（TS 29.228 Annex E）、一緒に登録される識別子の一覧が入っています。利用者の要求にどのアプリケーションサーバーを加えるかを S-CSCF に伝える iFC（initial filter criteria）も入っています。`deploy/compose/hss.yaml` では、すべての利用者に SMS over IP の iFC を 2 つ設定しています。1 つ目の iFC が REGISTER で何をするかは、ステップ 15 で説明します。

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

P-CSCF は、200 OK を UE に渡した直後に SUBSCRIBE を送ります（TS 24.229 5.2.3）。購読は秘密の識別子ごとに 1 回で、最初の登録の後に行います。再登録では繰り返しません。P-CSCF は購読の期限の 600 秒前に、そのダイアログの中で S-CSCF へ直接更新します。更新の時刻は購読と一緒に置きます。そのため、前のノードの代わりに起動したノードも、前のノードが始めた購読を同じように更新します。

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
  <registration aor="sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org" id="reg1" state="active">
    <contact id="..." state="active" event="registered" expires="3600" callid="..." cseq="2">
      <uri>sip:4218b61b-4e20-8e1c-872d-5b39fca1e64f@172.28.0.100:5060</uri>
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

UE が期限までに再登録しないと、登録の期限が切れます。そのとき S-CSCF は `event="expired"` の NOTIFY を送り、続けて **TIMEOUT_DEREGISTRATION**（4）の SAR を送ります。S-CSCF は、期限の来た登録を 1 秒ごとに探します。そのため、この 2 つのメッセージは、期限そのものより 1 秒ほど遅れることがあります。P-CSCF はこの NOTIFY を受けて、同じように UE の IP の対応付けを終わらせます。UE あての NOTIFY は、それでも届きます（TS 24.229 5.2.5.2 と NOTE 2）。UE も、登録が無くなったことを知ります。`ue register` は、そこでエラーとして終了します。`ue call` と `ue answer` はログに出すだけです。`ue serve` は、登録を頼まれるまで未登録のままです。S-CSCF が自分の判断で `event="deactivated"` として登録を終わらせたときは、UE は登録し直します。TS 24.229 5.1.1.7 の決まりです。

## 加入を削除する（HSS からの登録解除）

HSS には HTTP で加入を登録、削除する API（プロビジョニングの API）があります。hss の設定キー `provisioning_listen` に書いたアドレス（`:8081` など）で待ち受けます。この API は 3GPP のインタフェースではなく、このプロジェクト独自のものです。製品の HSS にも同じような API がありますが、どれも製品ごとに独自です。JSON のフィールド名は設定ファイルのキーと同じです。

| 要求 | 内容 |
| --- | --- |
| `GET /subscriptions` | 加入の一覧を、登録の状態と一緒に、ページに分けて返す。1 ページは 1000 件で、`?limit=` にそれより小さい件数を書けばその件数になる。`?after={秘密の識別子}` を付けると、識別子の並び順で、その識別子の次から返す。続きのページがあるときは、`Link` ヘッダに、そのページを取るための問い合わせを `rel="next"` 付きで入れる（RFC 8288）。たとえば `<?after=001010000000001%40ims.mnc001.mcc001.3gppnetwork.org&limit=2>; rel="next"` が入る。HSS は秘密の識別子の索引から 1 ページ分だけを読むので、加入が 100 万件ある網でも、1 ページにかかる手間はこの網と変わらない。 |
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

## 登録に失敗したとき

登録した UE は、登録が切れる前に更新します（TS 24.229 5.1.1.4.1）。更新に 408、500、504、403 が返ると、UE はそれまでの登録を当てにせず、最初の登録をやり直します。最初と同じ形の REGISTER を送り、S-CSCF がもう一度チャレンジします（5.1.1.4.1）。UE シミュレータは、更新がどの応答で失敗しても、こうしてやり直します。応答に Retry-After が付いていれば、まずその時間だけ待ちます。その間は同じ P-CSCF を通って最初の登録をしないためです（5.1.1.2.1）。

最初の登録も失敗したときは、UE はすぐには試し直しません。試し直すたびに、その前に RFC 5626 4.5 の再試行の間隔だけ待ちます（TS 24.229 5.1.1.2.1）。

- 上限 W は 30 秒に、続けて失敗した回数だけ 2 を掛けた値です。1800 秒を超えません。
- UE は W の半分から W までの間でランダムに選んだ時間だけ待ちます。1 回目の失敗のあとは 30 秒から 60 秒、2 回目のあとは 60 秒から 120 秒で試し直し、6 回目の失敗からは 15 分から 30 分ごとに試します。
- Retry-After の付いた応答には、その時間だけ待ちます。そのあとは失敗の回数を数え直します。

待つ時間をランダムにするのは、同じときに登録を失った UE の試行をばらけさせ、一斉に戻ってこないようにするためです。`ue serve` と `ue register` は、登録し直せるまで試し続けます。失敗のたびに `"msg":"registration failed"` をログに出し、`retry_after_ms` に次の試行までの待ち時間を書きます。`ue serve` は最初の登録に失敗したときも待ってから試し直しますが、ほかのコマンドはそのときエラーで終わります。上で、削除した加入の `ue register` が終わったのと同じです。

## S-CSCF を運用から外す

運用者は、ノードが受け持っているものを切らずに、そのノードを運用から外せます。ノードは新しいものを受けず、始めたものは続け、それが無くなるのを待ちます。これをドレインと呼びます。商用の網は S-CSCF を複数台で動かし、どの台も利用者の状態を共有します。そのため、新しい仕事はほかの台へ行き、利用者は登録し直さなくて済みます。

S-CSCF のノードはそのための保守用の API をポート 8085 で出しています。`deploy/compose/scscf.yaml` の `maintenance` の節の待ち受けで、Prometheus が集める指標と同じ所です。この API はパスワードを求めないので、ポートはホストに公開していません。要求は S-CSCF のコンテナの中から送ります。S-CSCF の管理上の状態は TS 28.622 5.3.1 の値を取ります。`UNLOCKED` は新しい仕事を受け、`SHUTTINGDOWN` は持っているものだけを扱い、`LOCKED` は新しいものを何も受けません。shut down を送ると、S-CSCF は処理中のトランザクションがあるあいだは `SHUTTINGDOWN` になり、最後の 1 つが終わると `LOCKED` になります（ITU-T X.731 7.1.3）。

1. S-CSCF を shut down します。

   ```sh
   docker compose exec scscf sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/scscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   応答の最後に S-CSCF の状態が出ます。処理中のトランザクションが無かったので、すぐに `LOCKED` になっています。

   ```json
   {"role":"scscf","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   `users` は S-CSCF が処理しているトランザクションの数です。`operational_state` は S-CSCF が動けるかを示します（TS 28.622 5.3.3）。HSS のどの台も Cx の要求を受けないあいだは `DISABLED` です。`docker compose exec scscf wget -qO- http://127.0.0.1:8085/maintenance/v1/state` で、ノード全体の状態を見られます。

2. UE を登録します。

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   UE は `REGISTER answered 500 Server Internal Error` で終わります。

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |  REGISTER    |              |               |              |
 |------------->|  REGISTER    |               |              |
 |              |------------->|  UAR, UAA     |              |
 |              |              |<---------------------------->|
 |              |              |  REGISTER     |              |
 |              |              |-------------->|              |
 |              |              |  503 Retry-After: 30         |
 |              |  500         |<--------------|              |
 |  500         |<-------------|               |              |
 |<-------------|              |               |              |
```

- REGISTER は登録の手順 1 から 4 と同じように S-CSCF に届きます。I-CSCF が、利用者を受け持つ S-CSCF を HSS に尋ね、そこへ転送します。
- S-CSCF は **503 Service Unavailable** に **Retry-After: 30** を付けて答えます。ダイアログの外の要求は新しいものを始めるので、S-CSCF は `UNLOCKED` でないあいだは受けません。Retry-After はその秒数のあいだこのサーバーにほかの要求を送らないよう、クライアントに求めます（RFC 3261 21.5.4）。S-CSCF は利用者を認証しないので、HSS へ MAR は送りません。
- 503 を受けたクライアントは、DNS で得た次のサーバーへ要求を送ります（RFC 3263 4.3）。S-CSCF が複数台なら、REGISTER は次の台へ行き、利用者はそこで登録されます。この網の I-CSCF が知る S-CSCF は 1 つなので、ほかに試す先がありません。
- プロキシは受けた 503 を上流へ返しません（RFC 3261 16.7 の手順 6）。この 503 は、S-CSCF が今は要求を受けないことを示します。I-CSCF がそのまま返すと、ほかの S-CSCF への要求は受けているのに、I-CSCF も要求を受けないと伝えることになります。503 が UE に届けば、UE はそれを自分の P-CSCF のことと読みます。Retry-After がタイマー F より長い 503 を受けた UE は、その時間のあいだ自分の P-CSCF を使えないものとして扱います（TS 24.229 5.1.1.2.1）。そこで I-CSCF は自分で **500 Server Internal Error** を返します。Retry-After は S-CSCF についての値なので、500 には付けません。
- P-CSCF はこの 500 を UE へ転送します。3xx、480、503 とは違い、500 では REGISTER を別の I-CSCF へ送り直しません（TS 24.229 5.2.2.1 の 7、RFC 3263 4.3）。

Wireshark で `sip.Status-Code == 503` と入力すると S-CSCF の 503 が、`sip.Status-Code == 500` と入力するとそのあとの 2 つの 500 が見えます。PUT の要求はコンテナのループバックのインタフェースを通るので、キャプチャには入りません。

`UNLOCKED` でないあいだも、S-CSCF はダイアログの中の要求（通話中の呼の BYE など）と、アプリケーションサーバーが元のダイアログの識別子を付けて送り返した要求を扱います。始めたものを、外さなかったときと同じように終わらせるためです。期限の切れた登録も終わらせます。登録の更新はダイアログの外の新しい REGISTER なので、これにも 503 が返ります。

3. 同じ要求に `UNLOCKED` を入れて、S-CSCF を unlock します。

   ```sh
   docker compose exec scscf sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/scscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   次の `ue register` は前と同じように登録します。

管理上の状態はノードのプロセスの中にあります。起動し直したノードは `UNLOCKED` です。`scripts/e2e-observability.sh` はこの手順を実行し、キャプチャを確かめます。

## P-CSCF を運用から外す

UE はすべての要求を、自分が登録に使った P-CSCF へ送ります。そのため、ある P-CSCF を使う UE がひとりでにほかの P-CSCF へ移ることはありません。運用者が運用から外した P-CSCF は、自分を通る登録を始める REGISTER と更新する REGISTER を断り、UE は代わりにほかの P-CSCF を通って最初の登録を行います。それまでのあいだ、P-CSCF は自分を通って登録した UE を扱い続けます。その UE の呼、ダイアログの中の要求、登録を終わらせる登録の解除です。

P-CSCF のノードも同じ保守用の API をポート 8085 で出しています。`deploy/compose/pcscf.yaml` の `maintenance` の節の待ち受けです。P-CSCF の `users` はこの P-CSCF を使う UE の IP の対応付けの数です。shut down を送ると、P-CSCF は対応付けが残っているあいだは `SHUTTINGDOWN` になり、最後の 1 つを忘れると `LOCKED` になります。登録を解除した UE も、その UE へ送っている途中の要求が終わるまで、32 秒のあいだ利用者に数えます。503 を受けてほかの P-CSCF で登録し直した UE は、この P-CSCF に登録の解除を送らないので、この P-CSCF を通した登録の期限が切れるまで利用者に数えます。`operational_state` はいつも `ENABLED` です。P-CSCF は REGISTER ごとに DNS で I-CSCF を探すので、要求を受けるのに決まった相手が要らないからです。

1. P-CSCF を shut down します。

   ```sh
   docker compose exec pcscf sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/pcscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   応答の最後に P-CSCF の状態が出ます。登録している UE が無ければ、すぐに `LOCKED` になります。

   ```json
   {"role":"pcscf","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   登録している UE があれば `SHUTTINGDOWN` になり、`users` にその UE が数えられます。

2. UE を登録します。

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   UE は `REGISTER answered 503 Service Unavailable, Retry-After 300` で終わります。

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |  REGISTER    |              |               |              |
 |------------->|              |               |              |
 |  503 Retry-After: 300       |               |              |
 |<-------------|              |               |              |
```

- P-CSCF は自分で **503 Service Unavailable** に **Retry-After: 300** を付けて答え、何も転送しません。ホームの網のどのノードにも REGISTER は届きません。
- UE は REGISTER への 503 を、自分の P-CSCF のことと読みます。Retry-After がタイマー F より長ければ、UE はその時間のあいだ P-CSCF を使えないものとして扱い、知っているほかの P-CSCF を通って、または P-CSCF を探し直してから、最初の登録を行ってかまいません。それより短ければ、同じ P-CSCF を使い続けます（TS 24.229 5.1.1.2.1）。タイマー F は 64*T1 で、TS 24.229 の表 7.7.1 が UE に与える 2 秒の T1 なら 128 秒です。Retry-After の値を決めた仕様はありません。300 秒はこのプロジェクトが選んだ値です。
- UE シミュレータが知っている P-CSCF のアドレスは 1 つなので、`ue serve` は 300 秒待ってから同じ P-CSCF を通って登録し直します（[このプロジェクトで簡略化していること](#このプロジェクトで簡略化していること)）。`ue register` は、ほかの登録の失敗と同じようにすぐ終わります。
- 登録の更新も REGISTER なので、同じ 503 が返ります。登録済みの UE は遅くとも次の更新のときにほかの P-CSCF へ移ります。
- P-CSCF は断った REGISTER を 503 で失敗した登録の手順に数えます。P-CSCF のコンテナの中の `http://127.0.0.1:8085/metrics` では、`ims_registration_failures_total{role="pcscf",type="initial",status="503"}` に出ます。

Wireshark で `sip.Status-Code == 503 && ip.src == 172.28.0.40` と入力すると、P-CSCF の 503 が見えます。

3. 同じ要求に `UNLOCKED` を入れて、P-CSCF を unlock します。

   ```sh
   docker compose exec pcscf sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/pcscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   次の `ue register` は前と同じように登録します。

`scripts/e2e-observability.sh` はこの手順も実行し、キャプチャで 503 を確かめます。

## I-CSCF を運用から外す

I-CSCF はホーム網の入口です。要求と要求のあいだに何も持たず、Record-Route にも入らないので、ダイアログの 2 つ目以降の要求が戻ってくることもありません。そのため、運用者が運用から外した I-CSCF には、自分で続けるものがありません。すべての要求を断り、送り手は次の I-CSCF を試します。

I-CSCF のノードも同じ保守用の API をポート 8085 で出しています。`deploy/compose/icscf.yaml` の `maintenance` の節の待ち受けです。I-CSCF の `users` は処理中のトランザクションの数です。処理中のものが無ければ、shut down を送るとすぐに `LOCKED` になります。`operational_state` は、Cx の要求を受ける HSS が 1 台も開いていないあいだ `DISABLED` になります。I-CSCF は、REGISTER ごとにどの S-CSCF が利用者を扱うかを、初めの要求ごとに着信先がどこにいるかを、それぞれ HSS に尋ねるので、HSS が無ければどちらも扱えないためです。

1. I-CSCF を shut down します。

   ```sh
   docker compose exec icscf sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/icscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   応答の最後に I-CSCF の状態が出ます。

   ```json
   {"role":"icscf","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

2. UE を登録します。

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   UE は `REGISTER answered 504 Server Time-out` で終わります。

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |  REGISTER    |              |               |              |
 |------------->|  REGISTER    |               |              |
 |              |------------->|               |              |
 |              |  503 Retry-After: 30         |              |
 |              |<-------------|               |              |
 |  504         |              |               |              |
 |<-------------|              |               |              |
```

- I-CSCF は、HSS に何も尋ねないまま **503 Service Unavailable** と **Retry-After: 30** を返します。UAR は出ず、S-CSCF は REGISTER を見ません。Retry-After の値を決めた仕様はありません。30 秒はこのプロジェクトが選んだ値です。
- 503 を受けたクライアントは、DNS が返した次のサーバーへ要求を送ります（RFC 3263 4.3）。REGISTER については、P-CSCF 自身も次の I-CSCF へ移ります（TS 24.229 5.2.2.1 の 7 番目の項目）。I-CSCF が複数台あれば、REGISTER は次の台へ行き、いつもどおり登録できます。この網の DNS のゾーンには I-CSCF が 1 つしかないので、次がありません。
- P-CSCF は **504 Server Time-out** を返します。TS 24.229 5.2.2.1 の 7 番目の項目が、どの I-CSCF にも REGISTER を転送できなかったときに 504 を返すよう求めているためです。503 をそのまま返さない理由は、I-CSCF が S-CSCF の 503 をそのまま返さないのと同じです。
- ほかの網や発信側の S-CSCF から、ホーム網に入ってくる最初の INVITE も同じ 503 を受け、送り手は次の I-CSCF を試します（RFC 3263 4.3）。次が残っていなければ、発信側の S-CSCF か IBCF が **500 Server Internal Error** を返します。プロキシは 503 をそのまま返さないためです（RFC 3261 16.7 の手順 6）。504 になるのは REGISTER だけです。I-CSCF は、断った INVITE を `ims_session_attempts_total{role="icscf",direction="terminating"}` に数えません。その INVITE を扱う次の I-CSCF が数えるので、台ごとの数を足すと網の中の試行の数になります。
- shut down の前から I-CSCF が転送していた INVITE は、そのあとに来た CANCEL で取り消せます。CANCEL は新しい要求ではなく、SIP のスタックが INVITE のトランザクションで答えるためです。

Wireshark では、`sip.Status-Code == 503 && ip.src == 172.28.0.20` で I-CSCF の 503 が、`sip.Status-Code == 504` で P-CSCF が UE に返した 504 が見られます。

3. 同じ要求に `UNLOCKED` を入れて、I-CSCF の lock を解きます。

   ```sh
   docker compose exec icscf sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/icscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   次の `ue register` は前と同じように登録します。

`scripts/e2e-observability.sh` はこの手順も実行し、キャプチャで 503 と 504 を確かめます。

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

UE から受けて転送する要求には、P-CSCF が自分の P-Charging-Vector を付けます（5.2.2.1 の 3 番目の項目、5.2.6.3.3 の手順 7）。UE にあてた要求では、向きが逆になります。P-CSCF はヘッダを消す前に icid-value と orig-ioi を保存しておき、UE の応答に自分の term-ioi と一緒に書き込みます（5.2.6.4.3 の 9 番目の項目、5.2.6.4.4 の 6 番目の項目）。1 つの通話を icid-value でたどる方法は、[音声通話の流れを追う](call.md) にあります。Wireshark で `sip.P-Charging-Vector && ip.src == 172.28.0.40 && ip.dst == 172.28.0.100` と入力しても、何も見つかりません。REGISTER への 401 と 200 も、P-Charging-Vector を付けずに UE に届きます。

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

`scripts/e2e-registration.sh` の登録の試験は、最後にこの UE を使います。コンテナのイメージに入っているシミュレータをそのまま実行できるマシンでだけ動かし、その登録の全体をキャプチャから読み取ります。ゲートウェイから届く REGISTER、P-CSCF が UE の Via に書いた `received` と `rport`、401 と 200 OK、そして同じ NAT を通って戻る、UE 自身の購読への NOTIFY です。

## このプロジェクトで簡略化していること

仕様のうち、次の部分は省略するか短くしています。コードにはそれぞれ `SIMPLIFIED:` のコメントがあり、`grep -rn "SIMPLIFIED:" internal cmd` ですべて一覧できます。

| 箇所 | 省略していること | 理由 |
| --- | --- | --- |
| P-CSCF。TS 24.229 5.2.2.1 の 1 番目の項目 | Path の URI に、フロートークンと `ob` パラメータを付けない。 | SIP outbound（RFC 5626）の機能で、このプロジェクトでは実装しないため。 |
| P-CSCF。TS 24.229 5.2.2.1 の 6 番目の項目 | REGISTER への 200 の P-Charging-Vector にある term-ioi を保存しない。 | この IOI は P-CSCF の課金の記録でホーム網を示すためのもの。このプロジェクトのノードは課金の記録を作らず、後のメッセージもこの IOI を運ばないため。 |
| S-CSCF。TS 24.229 5.4.1.5 | S-CSCF が自分で登録を解除するのは、アプリケーションサーバーが第三者 REGISTER に失敗し、その iFC が SESSION_TERMINATED のときだけ。ほかは HSS からの RTR でだけ解除する。解除の前にセッションを解放することもしない。 | S-CSCF の保守用の API には、登録を終わらせる操作が無い。また、S-CSCF は中継するダイアログの状態を持たないため。 |
| S-CSCF。TS 29.228 6.1.3.1 | NEW_SERVER_ASSIGNED と REMOVE_S-CSCF の RTR には DIAMETER_UNABLE_TO_COMPLY で答える。 | HSS のスタブは PERMANENT_TERMINATION しか送らない。また、登録していない利用者のプロファイルは、S-CSCF が数分たつと自分で手放すため。 |
| 購読者。RFC 6665 4.1.4、5.4.9 | 2 つ目のダイアログから届いた NOTIFY には 481 を返す。 | reg イベントでは、購読が分岐することを禁じているため（RFC 3680 4.9）。 |
| I-CSCF。TS 24.229 5.3.1.3 | S-CSCF が答えないとき、別の S-CSCF を選び直さない。 | 設定に書く S-CSCF が 1 つだけのため。その S-CSCF を何台かで動かすときは、1 台が答えなかった REGISTER は、すでに次の台へ送り直す。 |
| UE。TS 24.229 9.2.1 | この手順書の UE やホストのソフトフォンのように、アクセス網を使わない UE には P-CSCF のアドレスを設定で与える。EPC スタブを使う UE（`ue -epc`）は attach のときに PCO からアドレスを受け取る。[VoLTE の呼の流れ](volte.md) を参照。 | ソフトフォンと、attach しない UE のための経路を残すため。 |
| UE。TS 24.229 5.1.2A.1.6 | 応答の無かった要求のあと、UE はすぐに同じ P-CSCF を通って登録し直す。その P-CSCF を使えないものとして扱ったり、別の P-CSCF を探したりはしない。 | UE が知っている P-CSCF のアドレスは 1 つで、P-CSCF を探し直しても同じアドレスになるため。 |
| UE。TS 24.229 5.1.1.2.1 と 5.1.1.4.1 | REGISTER でタイマー F が切れたときと、タイマー F より長い Retry-After の付いた 503 が返ったときは、待ってから同じ P-CSCF を通って登録する。その P-CSCF を使えないものとして扱ったり、別の P-CSCF を探したりはしない。更新に失敗したあとは、503 の短い Retry-After なら更新してよい場合でも、必ず最初の登録をやり直す。 | UE が知っている P-CSCF のアドレスは 1 つで、P-CSCF を探し直しても同じアドレスになるため。また、短い Retry-After の付いた 503 が UE に届くことは無いため。運用者が shut down した P-CSCF は、タイマー F より長い Retry-After を付けて 503 を返す。S-CSCF が Retry-After を付けた 503 で答えると、I-CSCF は Retry-After の無い 500 を返す。 |
| UE。TS 24.229 5.1.1.2.1 | 再試行の間隔の max-time と base-time は、いつも RFC 5626 4.5 の既定値を使う。 | 網がこれらの値を渡せるのは TS 24.167 の管理オブジェクトを通してだけで、UE はそれを持たないため。 |
| HSS。TS 29.228 6.1.1.1、6.1.2.1 | 登録の状態を、公開の識別子ごとではなく加入ごとに持つ。一部の Server-Assignment-Type に対応しない。 | 試験用の加入はそれぞれ 1 つの暗黙登録の組で、S-CSCF はほかの値を送らないため。 |
| HSS。TS 29.228 6.1.1.1 の手順 5 | 登録してよい利用者かを確かめない。 | 試験用の加入は全員が登録してよいため。 |
| 利用者のプロファイル。TS 29.228 Annex E | サービスプロファイルは 1 つ。任意の要素は、規制の表示、公開サービス識別子の種類、iFC のほかに入れない。 | 試験用の加入者と PSI ユーザーにはそれ以上要らないため。 |
| HSS。TS 29.228 B.2.2 | すべての加入者に設定の iFC を渡す。加入者ごとに違うのは ServiceInfo の MSISDN だけ。 | このプロジェクトには、利用者ごとに違うサービスが無いため。 |
| HSS と S-CSCF。TS 23.228 4.3.3.4、4.2.7.2 | 公開の識別子は 1 つの秘密の識別子にだけ属し、複数の UE で共有できない。公開の識別子への要求は、その識別子のただ 1 つの登録の、ただ 1 つの Contact に届く。S-CSCF はフォークしない。 | このプロジェクトの加入者は、それぞれ UE を 1 台だけ使うため。 |
| iFC。TS 29.228 B.2.3、Annex E | SPT の SessionDescription、RegistrationType、ProfilePartIndicator、IncludeRegisterRequest、IncludeRegisterResponse を扱わない。 | SMS と MMTel AS の iFC には、どれも要らないため。RegistrationType は S-CSCF にとって任意の機能。 |
| S-CSCF。TS 24.229 5.4.1.7 | 第三者 REGISTER の To はいつも既定の公開識別子にする。Call-ID は毎回新しくする。応答の term-ioi は保存しない。 | どの識別子にするかは運用者が選べる。Call-ID を変えないには、利用者とサーバーごとに状態が要る。term-ioi は S-CSCF の課金の記録のためのもので、このプロジェクトのノードは課金の記録を作らないため。 |
| S-CSCF。TS 24.229 5.4.0 | アプリケーションサーバーのアドレスを要求のたびに DNS で引く。最初の第三者 REGISTER を送ったアドレスは覚えない。 | アプリケーションサーバーの役割は、すべての台で共有する Store に状態を置くため。 |
| IP-SM-GW。RFC 6665 4.1.2.4 | Timer N を使わない。NOTIFY が来なかった SUBSCRIBE は、次の第三者 REGISTER のときに新しい購読に置き換える。 | S-CSCF は受け付けた SUBSCRIBE のすべてにすぐ NOTIFY を送るため。 |
| IP-SM-GW。TS 24.341 5.3.3.2 | service-info に MSISDN が無いとき、PUR では利用者を公開の識別子で示す。UE の REGISTER の IMSI は使わない。 | IMSI は message/sip の本文に入った UE の REGISTER から取るが、S-CSCF はその本文を送らないため。Sh は公開の識別子も受け付ける（TS 29.328 Table 7.6.1）。 |
| UE。TS 24.229 5.1.1.3 | UE の購読の更新に 481 が返ると、UE は次の初回登録の後まで購読し直さない。 | 更新は、購読の期限よりずっと前に行うため。 |
| UE。TS 24.229 5.1.1.7 | `event="rejected"` を受けても、`ue call`、`ue answer`、`ue serve` は通話を解放しない。 | 登録が拒否されるのは HSS から加入を削除したときだけのため。`ue call` と `ue answer` の通話は音声を送り終えると自分で終わり、`ue serve` の通話は操作する側が切ると終わる。 |
| S-CSCF。TS 24.229 5.4.2.1.2 | reg イベントの NOTIFY への応答にある term-ioi を保存しない。 | この IOI は S-CSCF の課金の記録のためのもので、このプロジェクトのノードは課金の記録を作らないため。 |
| P-CSCF。TS 24.229 5.2.3 | P-CSCF の購読の更新に 481 が返ると、P-CSCF はすぐには購読し直さない。利用者の次の REGISTER で新しい購読を始める。 | 更新は、購読の期限よりずっと前に行うため。 |
| P-CSCF と UE。RFC 3680 5.2 | reginfo の文書の version を確かめない。 | S-CSCF は毎回すべての状態を送る。また、後の NOTIFY より遅れて届いた NOTIFY は、CSeq で断るため。 |
| P-CSCF。TS 24.229 5.2.6.2 | UE と網は、P-CSCF の同じアドレスとポートに要求を送る。P-CSCF が Path に入れた URI、P-CSCF 自身の Contact、P-CSCF が扱う UE の Contact のどれかにあてた要求を、網からの要求とみなす。網からの要求には IP の対応付けを求めない。 | 実際の P-CSCF は、UE の側（Gm）と網の側（Mw）を別のインタフェースで扱うことが多い。このプロジェクトのコンテナは 1 つのネットワークを共有するため。 |
| P-CSCF。TS 24.229 5.2.2.3 | IP の対応付けは、UDP の要求が届いたアドレスとポートで探す。1300 バイトより大きい要求を、UE は TCP で送る。TCP の要求は、Via の sent-by のアドレスが要求の届いたアドレスと同じなら、sent-by だけで照らし合わせ、接続のポートは確かめない。sent-by が別のアドレスを示す TCP の要求は見分けられない。sent-by のポートを認証するものは無いので、UE と同じ IP アドレスを持つホストなら、そのポートを書ける。 | IPsec を使わないと、TCP の接続はそのときどきのポートから来て、そのポートを持つ IP の対応付けが無いため。 |
| SIP Digest。TS 24.229 5.4.1.2.1B、RFC 7616、RFC 8760 | S-CSCF は MD5 と qop=auth だけを提示する。仕様は SHA-256 を優先し、MD5 は後方互換のために残している。 | HSS のスタブが渡すのは MD5 の H(A1) だけで、SHA-256 には別の AVP が必要なため。 |
| Diameter。RFC 3539 3.4.1 と Appendix A、RFC 6733 5.6.4 | `REOPEN` の状態が無く、つなぎ直した相手をすぐに使う。監視が疑った接続で答えを待つ要求は、自分の時間切れを待ってから HSS のほかの台へ移る。接続を始めるのは設定で決めた側だけなので、同じ相手への 2 本の接続のどちらを残すかを選ぶ手順（election）も無い。 | 答えなかった要求は HSS の次の台へ送り直すので、まだ安定しない台は要求を遅らせるだけで、失わせないため。どちらが接続を始めるかは、各ノードの設定で決まるため。 |

## 後片付け

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。
