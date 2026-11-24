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

HSS はこの記録を数えています。利用者が登録しているあいだ、その公開識別子は `ims_hss_registered_public_identities` に、私的識別子は `ims_hss_registered_private_identities` に入り、登録の解除で外れます。HSS のノードはコンテナの中の `http://127.0.0.1:8085/metrics` に、この 2 つと `ims_hss_subscriptions`、`ims_hss_public_identities` を出します。後の 2 つは、登録している利用者がいるかどうかに関わらず、HSS が持っているものの数です。同じ待ち受けに、HSS を運用から外すための保守用の API も出ます（[HSS を運用から外す](#hss-を運用から外す)）。

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

HSS には HTTP で加入を登録、削除する API（プロビジョニングの API）があります。hss の設定キー `provisioning_listen` に書いたアドレス（`:8081` など）で待ち受けます。この API は 3GPP のインタフェースではなく、このプロジェクト独自のものです。加入者のデータを 1 つの保存先に集める構成（UDC）では、その保存先が運用者の OSS に向けたプロビジョニングのインタフェースを持ちます。ただし、そのインタフェース自体は仕様の対象外です（TS 23.335 4.2.3）。加入者の管理（SuM）には OSS に面した標準のインタフェースがあります。Itf-N の IRP（TS 32.141 4.2.1）で、形は Bulk CM の XML のファイルです（TS 28.753 Annex A.3.1）。ただし、その情報モデル（TS 28.752）には、この API が運ぶ長期の鍵、OPc、SIP Digest のパスワードを入れる項目がありません。標準の形だけではこの網の加入を入れられません。そのため、プロビジョニングは独自の API で行っています。JSON のフィールド名は設定ファイルのキーと同じです。

| 要求 | 内容 |
| --- | --- |
| `GET /subscriptions` | 加入の一覧を、登録の状態と一緒に、ページに分けて返す。1 ページは 1000 件で、`?limit=` にそれより小さい件数を書けばその件数になる。`?after={秘密の識別子}` を付けると、識別子の並び順で、その識別子の次から返す。続きのページがあるときは、`Link` ヘッダに、そのページを取るための問い合わせを `rel="next"` 付きで入れる（RFC 8288）。たとえば `<?after=001010000000001%40ims.mnc001.mcc001.3gppnetwork.org&limit=2>; rel="next"` が入る。HSS は秘密の識別子の索引から 1 ページ分だけを読むので、加入が 100 万件ある網でも、1 ページにかかる手間はこの網と変わらない。 |
| `GET /subscriptions/{秘密の識別子}` | 1 件の加入を返す。`test_user` と `service_profile` は、その加入が持つときだけ入る。 |
| `POST /subscriptions` | 加入を 1 件追加する。本文は `{"private_identity": "...", "public_identities": ["sip:...", "tel:..."], "password": "..."}`。`"tel": "tel:+..."` を付けると、その tel URI も公開の識別子に加わる。IMS-AKA を使う加入には、16 進数で書いた `"aka": {"k": "...", "opc": "...", "amf": "...", "sqn": "..."}` を、password と一緒に、または password の代わりに付ける。`"ut_password": "..."` を付けると、利用者が Ut で付加サービスを設定できる。HSS はその H(A1) だけを Sh の Repository Data として持つ。`"mmtel_services": "<MMTelServices ...>"` は付加サービスの最初の文書で、省くと hss のキー `mmtel_services` の文書が入る。`"test_user": true` を付けると、運用者が機能を試すために使う加入という印が付く。`"service_profile": "..."` はその加入が受け取るサービスプロファイルの名前で、どちらも S-CSCF に渡るフィルタ条件を決める（[SMS の流れを追う](sms.md)）。hss のキー `service_profiles` に無い名前は 400 で断る。 |
| `PUT /subscriptions/{秘密の識別子}` | 加入を 1 件追加するか、まるごと置き換える。本文は `POST` と同じで、`repository_data` を足せる。本文の `private_identity` には、経路に書いた識別子と同じものを書く。本文に書かなかったものは消える。`repository_data`、`mmtel_services`、`ut_password` のどれも書かなければ、その加入が持つ文書は hss のキー `mmtel_services` のものになり、利用者が Ut で設定した付加サービスもその既定に戻る。`"repository_data": [{"service_indication": "MMTEL-Services", "service_data": "<MMTelServices ...>"}]` は、Sh の Repository Data をそのまま渡すもので、`mmtel_services`、`ut_password`、`customized_alerting_tone` の代わりに使う。空の配列を書くと、Repository Data を 1 つも持たない加入になる。置き換えても変わらないものがある。登録の状態、割り当てられている S-CSCF、届けられずに待っている短いメッセージの記録、SQN_HE は、そのまま残る。`aka.sqn` は、加入を新しく作るときだけ読む。同じファイルを送り直しても SQN_HE が戻らないので、認証ベクタを 2 度使うことがない。答えは `{"result": "created", "subscription": {…}}` で、中身が変わったときは `updated`、資格情報と Repository Data を含めて本文のとおりだったときは `unchanged` を返す。状態コードは、加入を作ったときが 201 で、ほかは 200 になる。`?dry_run=true` を付けると、何も変えずに、その要求が何をするかだけを返す。`created` と答えるときも、状態コードは 200 になる。登録中の利用者のユーザープロファイルが変わる置き換えでも、`PATCH` と同じように、新しいプロファイルがすぐにその利用者の S-CSCF へ届く。サービスプロファイルを変える、識別子を禁止にするか禁止を解く、組に識別子を足すか外す、のどれも当たる。例外は、登録中の利用者の既定の公開の識別子が変わる置き換えである。既定の識別子は、組の中で禁止されていない最初の識別子で（TS 29.228 6.5.1.1）、最初の識別子を禁止にするのもこれに当たる。 |
| `PATCH /subscriptions/{秘密の識別子}` | 加入 1 件の印とサービスプロファイルの名前だけを変える。本文は `{"test_user": true, "service_profile": "..."}` で、片方だけでも書ける。本文に書かなかった項目はそのまま残るので、`{}` を送っても何も変わらない。この 2 つをこの要求で変えるのは、`PUT` で変えるとその加入の資格情報を一緒に書くことになり、その資格情報を API が誰にも渡さないためである。hss のキー `service_profiles` に無い名前は 400 で断る。答えは `GET` と同じ、1 件の加入になる。登録中の利用者のサービスプロファイルが変わる要求では、新しいプロファイルがすぐにその利用者の S-CSCF へ届く（[登録中の利用者のサービスプロファイルを変える](#登録中の利用者のサービスプロファイルを変える)）。 |
| `DELETE /subscriptions/{秘密の識別子}` | 加入を 1 件削除する。 |
| `GET /subscriptions/export` | 網の控えを取るために、加入を書き出す。1 行に 1 件の JSON で返し、メディア型は `application/x-ndjson` になる。1 行は、その加入をそのまま書き戻す `PUT` の本文で、資格情報も入っている。`password` と、`k`、`opc`、`amf`、`sqn` を持つ `aka` と、`repository_data` が入る。`sqn` は、与えた値ではなく、そのときの SQN_HE になる。`repository_data` には、H(A1) の形の Ut の資格情報と、CAT も一緒に入る。だから、控えの行を送り直すと、どの行も `unchanged` を返し、消えた加入は元のとおりに戻る。ページの分け方は `GET /subscriptions` と同じで、`?after=` と `?limit=` を取り、続きのページがあるあいだは `rel="next"` の `Link` ヘッダを返す。運用者が与えたものではなく、動いている加入者が持つものは入らない。登録の状態と、割り当てられている S-CSCF が入らないので、書き戻した加入は Not Registered になり、その利用者が登録し直す。網の全体をロックすることはしない。1 行には、その行を書いた時点の加入が入るので、何ページかに分けて読むと、読んでいるあいだの変更が混じる。ページを読んでいる途中で消された加入は、そのページに入れない。**この応答だけは、長期鍵、OPc、SIP Digest のパスワード、SQN_HE を、プロビジョニングの待ち受けに届く相手なら誰にでも渡す。** だからこの待ち受けは、内部の網だけに置き、ホストのどのアドレスにも出さない。`compose.yaml` の `hss` のサービスはポートを 1 つも公開せず、Kubernetes でも HSS に届くのはクラスタの中だけになっている。 |

書き出し以外の応答に、パスワードと AKA の鍵（K と OPc）は入りません。その加入で使える認証の方式を `authentication_schemes` に並べます。その加入が受け取るサービスプロファイルの名前は `effective_service_profile` に入ります。自分の `service_profile` を持つ加入はその名前、持たない加入は種類ごとの設定が指す名前で、印の付いた加入なら `test_user_service_profile`、ほかの加入なら `default_service_profile` です。加入のデータだけではどの名前になるかが分かりません。2 つの設定が HSS を動かしたまま変わるためで、だから応答がこの名前を書きます。追加する加入には設定ファイルと同じ決まりを当てはめます。項目が足りないときや、公開の識別子が SIP か tel の URI でないときは 400 を返します。識別子がすでにほかの加入にあるときは 409 を返します。

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

## 登録中の利用者のサービスプロファイルを変える

運用者は、利用者が登録しているあいだに、その利用者のサービスプロファイルを変えられます。`service_profile` か `test_user` を `PATCH` で変えるか、`PUT` で加入ごと置き換えます。`PUT` では、識別子を禁止にしたり、組の識別子を変えたりしても、ユーザープロファイルが変わります。S-CSCF が持っているのは、登録のときの SAA で受け取った前のプロファイルの iFC です。HSS は、次の登録までそのままにはしません。ユーザープロファイル全体を **Push-Profile-Request**（PPR、Cx のコマンド 305）ですぐに送ります（TS 29.228 6.2.2、6.6.1）。Wireshark では、`diameter.cmd.code == 305` でこの流れが見られます。

加入者 3 が登録しているあいだに、iFC を 1 つも持たないプロファイル `no-services` に移してみます。プロビジョニングの API はホストに公開していないので、要求は HSS のコンテナの中から送ります。BusyBox の `wget` は `PATCH` を送れないので、要求を書き下して `nc` に渡します。

```sh
docker compose exec hss sh -c '
body="{\"service_profile\":\"no-services\"}"
printf "PATCH /subscriptions/001010000000003@ims.mnc001.mcc001.3gppnetwork.org HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8081'
```

```text
 S-CSCF          HSS
 |                |  PATCH /subscriptions/...
 |  PPR (1)       |
 |<---------------|
 |  PPA (2)       |
 |--------------->|
 |                |  200 OK（加入を返す）(3)
```

1. HSS は、その利用者について名前を覚えている S-CSCF に PPR を送ります。宛先は、その S-CSCF が送った SAR の Origin-Host で、RTR と同じです（TS 29.228 Table 6.2.2.1）。User-Name は秘密の識別子です。**User-Data** にはユーザープロファイル全体、つまり暗黙登録の組のすべての公開の識別子と、新しいサービスプロファイルの iFC が入ります。変わっていない識別子も入れます（6.5.2.1）。
2. S-CSCF は、要求に入っている識別子について、持っているものを受け取ったもので置き換え、DIAMETER_SUCCESS（2001）の **Push-Profile-Answer**（PPA）で答えます（6.2.2.1）。登録そのものは、期限も Contact の対応付けもそのまま残ります。UE はこの変更を知らされず、その利用者の次の要求から新しい iFC で判定されます。S-CSCF がプロファイルを持っていない利用者の PPR には DIAMETER_ERROR_USER_UNKNOWN（5001）、読めないユーザー情報には DIAMETER_ERROR_NOT_SUPPORTED_USER_DATA（5009）を返し、持っているものはそのままにします（Table 6.2.2.1.1）。
3. HSS は PPA を受け取ってから、プロビジョニングの要求に答えます。PPR に答えが無かったときと、PPA が誤りを返したときは、HSS のログに書きます。それでも要求には 200 と新しい加入を返します。加入はどちらの場合も書き込まれていて、新しいプロファイルは次の登録で S-CSCF に届きます。

`no-services` にすると、加入者 3 の iFC はどれも一致しなくなります。この利用者の短いメッセージは IP-SM-GW に届かず、呼も MMTel AS に届きません。同じ `PATCH` を `"service_profile":"default"` で送れば、元の iFC が push されて戻ります。

HSS が push するのは、登録中の利用者と、未登録の状態の利用者です。未登録の状態は、登録していない利用者への呼のために S-CSCF がプロファイルを持っているあいだを指します（6.6.1）。ほかの利用者については、加入を書くだけです。プロファイルは次の登録で届きます。push するのは、送ることになるユーザープロファイルが実際に変わったときだけです。前と同じサービスプロファイルを選ぶ `test_user` の変更では、送るものがありません。

加入の種類ごとにサービスプロファイルを決める 2 つの設定、`default_service_profile` と `test_user_service_profile` を変えたときも、同じように push します（設定を動かしたまま変える手順は[試験用の利用者だけを収容する](#試験用の利用者だけを収容する)にあります）。ここに別のプロファイルを書くと、その設定から名前を取る加入のユーザープロファイルがどれも変わるので、HSS は、そのうち S-CSCF がプロファイルを持っている利用者それぞれに、1 人 1 通の PPR を送ります。送る相手はこれだけです。自分の `service_profile` を持つ加入はどちらの設定も使いませんし、どの S-CSCF も扱っていない利用者には、置き換えるプロファイルがどこにもありません。HSS は、S-CSCF がプロファイルを持っている利用者を、サービスプロファイルの名前をどこから取るかで分けて索引に入れています。変えた設定の組だけをページごとに読むので、1 回の変更の手間は、関わる利用者 1 人につき PPR 1 通で、関わらない加入を読むことはありません。

PPR は、保守用の要求への答えのあとに続きます。設定が効き始めたことは、効いた時点で運用者に返るので、登録している利用者が多い網でも、答えはそれを待ちません。PPR は送る時点のプロファイルを運ぶので、続けて 2 回変えたときは、あとの変更のプロファイルが S-CSCF に残ります。失敗した PPR は、加入 1 件の変更のときと同じく HSS のログに書き、その S-CSCF には、利用者の次の登録でプロファイルが届きます。

## OAM から網を運用する

`oam` のサービスは OAM のサーバーです。ノード 1 台ずつはそのノードの保守用の API で、加入者は HSS のプロビジョニングの API で操作します。OAM の画面も同じ待ち受けで配ります。管理する側と管理されるノードのあいだのインタフェースは、製品ごとに決めてよいことになっています（TS 32.600 4.2.1.3）。ここでは、このプロジェクトが決めたものを `/oam/v1/` の下に置いています。

このポートはホストのどのアドレスにもありません。この API はノードの設定を変え、どの加入の資格情報も入っている書庫を渡します。そのうえパスワードを求めません。そのため以下の要求は、ノードの保守用の API への要求と同じく、OAM のコンテナの中から送ります。

`deploy/compose/oam.yaml` に書いた一覧には、保守用の API を出す 10 台が入っています。3 つの CSCF、HSS、MMTel AS、MRFC、IP-SM-GW、サービスセンタと、2 つのメディア中継のノードです。1 回の要求で全台の状態を読めます。

```sh
docker compose exec oam wget -qO - http://127.0.0.1:8080/oam/v1/state
```

答えには一覧のノードごとに 1 件ずつ入ります。そのノードの状態か、答えなかった理由です。失敗したノードが落ちることはなく、その数は `succeeded` と `failed` にあります。ノードを 1 台ずつ運用から外す節で行うことは、OAM なら `/oam/v1/roles/{role}/administrative-state` で、ある役割の全台にまとめて行えます。

### 加入者を一括で登録する

運用者は、たくさんの加入をファイルで一度に登録します。1 行に JSON を 1 つ書きます。1 行は、プロビジョニングの API の `PUT` の本文です。私的識別子に `"delete": true` を添えた形だけは OAM 自身が読み、その加入を消します。行に書く識別子はすべて試験用のものでなければなりません。試験用の IMSI の番号か、架空の番号の範囲の番号です。そうでない行は、HSS に尋ねる前に OAM が断ります。

```sh
docker compose exec oam sh -c 'cat >/tmp/new-subscribers.jsonl <<"EOF"
{"private_identity":"001010000000120@ims.mnc001.mcc001.3gppnetwork.org","public_identities":["sip:+12025550120@ims.mnc001.mcc001.3gppnetwork.org"],"tel":"tel:+12025550120","password":"test-password-0120","test_user":true}
{"private_identity":"001010000000121@ims.mnc001.mcc001.3gppnetwork.org","public_identities":["sip:+12025550121@ims.mnc001.mcc001.3gppnetwork.org"],"tel":"tel:+12025550121","password":"test-password-0121","test_user":true}
EOF
length="$(wc -c </tmp/new-subscribers.jsonl | tr -d " ")"
{ printf "POST /oam/v1/provisioning HTTP/1.0\r\nContent-Type: application/x-ndjson\r\nContent-Length: %s\r\n\r\n" "$length"
  cat /tmp/new-subscribers.jsonl
} | nc -w 30 127.0.0.1 8080'
```

BusyBox の `wget` は `GET` と `POST` のほかのメソッドを送れず、本文も自分では作れません。そのため要求を書き出して `nc` に渡しています。答えは、できたところから返ってきます。記録ごとに 1 行、最後にまとめです。

```text
{"outcome":{"line":1,"private_identity":"001010000000120@ims.mnc001.mcc001.3gppnetwork.org","result":"created"}}
{"outcome":{"line":2,"private_identity":"001010000000121@ims.mnc001.mcc001.3gppnetwork.org","result":"created"}}
{"summary":{"lines":2,"created":2,"updated":0,"unchanged":0,"deleted":0,"rejected":0,"failed":0}}
```

OAM は 1 件ずつ読み、送り、報告します。そのため、ファイルが何行でも使うメモリは変わりません。1 件の結果は `created`、`updated`、`unchanged`、`deleted`、`rejected`（行が誤っていて、理由にどう誤っているかが入ります）、`failed`（HSS が受け取らなかったもので、送り直せば通ることがあります）のどれかです。同じファイルをもう一度送ると、資格情報も含めてどの行も `unchanged` になるので、2 回送っても構いません。経路に `?dry_run=true` を付けると、何も変えずに、各行が何をするかだけを答えます。理由に資格情報が出ることはありません。HSS が理由の中に書き返してきたときも伏せます。

この加入者の 1 人目は、行に書いたパスワードで、ほかの加入者と同じように登録します。

```sh
docker compose run --rm ue register \
  --impi 001010000000120@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:+12025550120@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-0120 --deregister-after 5s
```

キャプチャには、この構成のどの設定ファイルにも書いていない加入者の UAR、MAR、SAR が入ります。`"delete": true` の行を並べたファイルを送れば、また消せます。

### 加入を書き出して戻す

バックアップは、網が持ち続けるデータを区分ごとに書き出します。ここにある区分は `hss.subscriptions` の 1 つで、OAM はこれをプロビジョニングの API のページ分けされた書き出しで読みます。書き出したものは、1 行に JSON を 1 つ並べた書庫 1 つと、各手順がどう進んだかを残す目録になります。

```sh
docker compose exec oam sh -c '
body="{\"operator\":\"alice\",\"comment\":\"before the change\"}"
printf "POST /oam/v1/backups HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 30 127.0.0.1 8080'
```

答えには、始まった手順と終わった手順が順に流れ、最後に目録が来ます。目録にはこのバックアップの識別子が入っています。`GET /oam/v1/backups` は、新しいものから順にバックアップを並べます。戻すときは、まず書庫を最後まで読んで欠けが無いことを確かめ、そのうえで、各行が何をするかを答えるか、登録と同じ `PUT` で 1 件ずつ戻します。

```sh
docker compose exec oam sh -c '
printf "POST /oam/v1/backups/$1/restore?dry_run=true HTTP/1.0\r\n\r\n" |
  nc -w 60 127.0.0.1 8080' sh <識別子>
```

`?dry_run=true` を外すと実際に戻します。まだ網にあるものを戻すと、どの行も `unchanged` になります。同じ書庫を 2 回戻しても、2 回目は何も変わりません。戻した加入は Not Registered になり、その利用者は登録し直します。ノードが動いているあいだだけあるもの、つまり登録、ダイアログ、メディアのコンテキストは、どのバックアップにも入りません。

**書庫の 1 行には、その加入の長期鍵、OPc、パスワードが入っています。** HSS に戻すのに要るからです。書庫は OAM 自身のボリュームの中のファイルで、ほかの誰にも読めません。一方で OAM は、この API で、待ち受けに届く相手には誰にでも渡します。待ち受けをホストのどのアドレスにも置いていないのはそのためです。

## 試験用の利用者だけを収容する

機能を試しているあいだ、その機能を試す利用者だけを網に収容しておけます。どの加入者を収容するかは `admission` で決めます。

- `all`。この構成が起動時に使う値です。HSS が持つ加入者を全員収容します。
- `test_users`。`test_user` の印が付いた加入者だけを収容します。この印は、プロビジョニングの API の `POST`、`PUT`、`PATCH` で付けられます。試験用の利用者のサービスプロファイルを選ぶのも、同じ印です。

`admission` は `deploy/compose/hss.yaml` の `hss` にあり、HSS が起動時に使う値を決めます。これは保守用の API で動かしたまま変えられる設定でもあるので、網を狭めるのに起動し直す必要はありません。

`test_users` のとき、HSS は印の付いていない加入者の UAR を断ります。登録の状態の問い合わせの手順 5 は、識別子が登録を認可されていることを求め、認可されていないときの答えを **DIAMETER_AUTHORIZATION_REJECTED**（5003）としています（TS 29.228 6.1.1.1）。この値は Cx ではなく基本のプロトコルのものなので（RFC 6733 7.1.5）、Experimental-Result-Code ではなく **Result-Code** に入ります。Cx にも 5003 の DIAMETER_ERROR_IDENTITY_NOT_REGISTERED があり（TS 29.229 6.2.2.3）、こちらは必ず Experimental-Result-Code に入るので、どちらの AVP に入っているかで見分けます。I-CSCF は、失敗の UAA を **403 Forbidden** にして返します（TS 24.229 5.3.1.3）。

```text
 UE             P-CSCF         I-CSCF          HSS
 |  REGISTER (1) |              |              |
 |-------------->|------------->|              |
 |               |              |  UAR (2)     |
 |               |              |------------->|
 |               |              |  UAA (3)     |
 |               |              |<-------------|
 |  403 (4)      |              |              |
 |<--------------|<-------------|              |
```

1. UE が最初の REGISTER を送ります。I-CSCF に届くまでは、上の手順 1 と 2 のとおりです。
2. I-CSCF は、この利用者がここで登録してよいかを HSS に尋ねます。**User-Authorization-Type** は `REGISTRATION` です。この AVP が無い要求も同じ意味になります。
3. 加入者に `test_user` の印が無いので、HSS は Result-Code を DIAMETER_AUTHORIZATION_REJECTED にして答えます。S-CSCF の名前も能力も返しません。Wireshark では `diameter.Result-Code == 5003` でこの応答が見つかります。
4. I-CSCF は 403 で答え、P-CSCF が UE に返します。S-CSCF には問い合わせが行かないので、MAR も出ず、UE は認証を求められません。`ue register` は、加入を削除されたときと同じように、エラーとして終了します。

**REGISTRATION_AND_CAPABILITIES** の UAR も手順 5 の同じ枝を通るので、同じ答えになります。断られる利用者は、I-CSCF が S-CSCF を選ぶ前に止まります。**DE_REGISTRATION** の UAR は確かめません。手順 5 がこの枝で登録の認可を求めていないうえ、断ると、S-CSCF と P-CSCF に残っている登録を利用者が終えられなくなるからです。

登録を断るだけでは足りません。登録していない利用者への呼は HSS の位置の問い合わせに届き、終端の未登録の枝の iFC を持つ識別子には答えが返ります（TS 29.228 6.1.4.1）。そのあと I-CSCF が選んだ S-CSCF が、**UNREGISTERED_USER** の SAR でプロファイルを取りに来ます。そこで HSS は、収容の対象外で、有効な登録を持たない加入者には、この SAR も DIAMETER_AUTHORIZATION_REJECTED で断ります。S-CSCF は呼を扱うプロファイルを持てず、**500 Server Internal Error** で終えます。成功しなかった SAA のときと同じ振る舞いで、発信側に理由は伝わりません。ほかの Server-Assignment-Type は通します。終わりかけの登録が HSS に中途半端に残らないようにするためです。

収容の方針を狭めたときと、`test_users` のあいだに加入者の印を外したときは、そのときに有効な登録をそのままにします。HSS は RTR を送らず、その利用者への呼も S-CSCF に届き続けます。登録の更新は断られるので、利用者は期限が切れたときに落ちます。期限は S-CSCF が持っていて、HSS はそれを数えることも、終わらせるよう頼むこともしません。

断られる様子を見るには、HSS のノードの保守用の API で方針を狭めます。HSS を運用から外すのと同じ 8085 番の待ち受けです（[HSS を運用から外す](#hss-を運用から外す)）。`/maintenance/v1/config` への `PATCH` に、変える設定を名前で書き、誰が何のために変えるかを添えます。

```sh
docker compose exec hss sh -c '
body="{\"settings\":{\"hss.admission\":\"test_users\"},\"operator\":\"learner\",\"comment\":\"trying a feature out\"}"
printf "PATCH /maintenance/v1/config HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8085'
```

答えは変更の記録です。設定ごとに、変える前と変えたあとの値が入ります。あとで元に戻すために、運用者が取っておくものです。

```json
{"node":"hss","instance":"...","generation":1,"time":"...","operator":"learner","comment":"trying a feature out","changes":[{"name":"hss.admission","previous":"all","value":"test_users"}]}
```

新しい方針は、そのあとに届く要求からすぐに効きます。`GET /maintenance/v1/config` で読み直すと、`changeable` にこの方針が入っています。加入のサービスプロファイルを決める 2 つの設定も並び、`hss` には網のサービスプロファイルが、それぞれが有効にする機能と一緒に入っています。

```sh
docker compose exec hss wget -qO- http://127.0.0.1:8085/maintenance/v1/config
```

HSS が受け取れない値は断り、その要求の設定を 1 つも変えません。2 つのどちらでもない方針と、`service_profiles` に無いサービスプロファイルです。ノードは起動し直すと設定のファイルの方針で答えるので、作り直したコンテナは、運用者が設定し直すまで `all` で答えます。

サービスプロファイルを決める 2 つの設定も、同じ `PATCH` で、`hss.default_service_profile` と `hss.test_user_service_profile` として変えます。3 つの設定を 1 回の要求にまとめても構いません。どちらの名前を変えても、そのときに登録している利用者に変更が届きます。HSS が、加入 1 件を変えたときと同じように、新しいプロファイルをその利用者の S-CSCF に push するためです（[登録中の利用者のサービスプロファイルを変える](#登録中の利用者のサービスプロファイルを変える)）。試験用の利用者だけ、あるいは全員に機能を有効にするとき、利用者が登録し直すのを待たずに済みます。

次に、加入者 2 に試験用の印を付けます。プロビジョニングの API もホストに公開していないので、この要求も HSS のコンテナの中から、8081 番に送ります。

```sh
docker compose exec hss sh -c '
body="{\"test_user\":true}"
printf "PATCH /subscriptions/001010000000002@ims.mnc001.mcc001.3gppnetwork.org HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8081'
```

加入者 1 は断られ、加入者 2 は登録できます。

```sh
docker compose run --no-deps --rm ue register --deregister-after 2s
docker compose run --no-deps --rm ue register --deregister-after 2s \
  --impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2
```

この手順書のほかの節と同じ構成に戻すには、同じ `PATCH` を `"hss.admission":"all"` で送ります。上の記録の `previous` の値です。加入者 2 の印も `{"test_user":false}` で外します。`scripts/e2e-admission.sh` は、この流れを動かして、UAA と 403 を自分のキャプチャから読みます。

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

- I-CSCF は HSS に何も尋ねないまま **503 Service Unavailable** と **Retry-After: 30** を返します。UAR は出ず、S-CSCF は REGISTER を見ません。Retry-After の値を決めた仕様はありません。30 秒はこのプロジェクトが選んだ値です。
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

## HSS を運用から外す

CSCF とアプリケーションサーバーは、HSS に Diameter で届きます。そのため、運用から外した HSS は CSCF のように応答で知らせるのではなく、Diameter の接続を自分から終わらせます。相手ごとに **Disconnect-Peer-Request**（DPR）を送り、`Disconnect-Cause` に `REBOOTING` を入れ、相手の **Disconnect-Peer-Answer**（DPA）が返ったところで接続を閉じます（RFC 6733 5.4）。運用者が lock を解くまで、ノードはポート 3868 で待ち受けません。接続しに来た相手は TCP の段階で断られ、Tc のあと、ここでは 30 秒後にもう一度試します（RFC 6733 2.1）。

`REBOOTING` は、相手を呼び戻す理由です。`REBOOTING` の DPR を受け取ったノードは接続し直してよく、`BUSY` と `DO_NOT_WANT_TO_TALK_TO_YOU` の DPR を受け取ったノードは接続し直さないほうがよいとされています（RFC 6733 5.4.3）。運用から外した HSS は作業のあとに戻るので、`REBOOTING` を送ります。相手は、lock が解けたあとの次の試行で戻ってきます。

ポート 3868 で待ち受けないことは、RFC 6733 2.1 から外れています。2.1 は、ノードがいつでもこのポートで接続を受けられることを求めています。運用から外しているあいだに限って、そこから外れると決めました。根拠は先に送った `REBOOTING` です。この理由は、ノードがこれから再起動することを表します（5.4.3）。再起動中のノードも待ち受けていないので、接続を断られて Tc のあとにもう一度試すことは、相手が元から見込んでいる動きです。ほかの案はこれより劣ります。CER を CEA で断る案では、運用から外れていることを表す Result-Code がありません。5.3 にあるのは `DIAMETER_NO_COMMON_APPLICATION`、`DIAMETER_NO_COMMON_SECURITY`、`DIAMETER_UNKNOWN_PEER` で、どれもこのノードには当てはまりません。接続を受けてからまた DPR を送る案では、運用から外しているあいだ、CER、CEA、DPR、DPA のやり取りを Tc ごとに繰り返します。

商用の網には HSS が複数台あり、どの台も全加入者を持っています（TS 29.228 6.4）。クライアントは各台への接続を保ち、Cx と Sh の要求を、開いている最初の接続に送ります。外した台への要求は次の台へ行くので、利用者が登録し直す必要はありません。この網の HSS は 1 台なので、外しているあいだ、I-CSCF と S-CSCF には HSS がありません。

HSS のノードも同じ保守用の API をポート 8085 で出しています。`deploy/compose/hss.yaml` の `maintenance` の節の待ち受けで、Prometheus が読む metrics と同じところです。HSS の `users` は処理中の Cx と Sh の要求の数です。処理中のものが無ければ、shut down を送るとすぐに `LOCKED` になります。処理中のものがあれば、その応答が送り出されるまで `SHUTTINGDOWN` のままで、DPR は応答のあとに出ます。応答を途中で切らないためです。

`SHUTTINGDOWN` の HSS は今ある接続をそのまま持ちます。相手には止まるようにと伝えないので、相手は Cx と Sh の要求をその接続で送り続けます。そのため、処理中の要求が 0 になるのは要求の流れが途切れたときで、登録が続けて来ているあいだは 0 にならないこともあります。そこを区切る期限が `timeout_seconds` です。指定した秒数が過ぎると、HSS は処理中の要求を残したまま `LOCKED` になります。shut down ではなく lock を送ったときも、その時点で同じことが起きます。どちらの場合も、処理中の要求を残して接続を閉じるので、送り出す途中だった応答は捨てられ、送り手には応答が届きません。受けた要求すべてに応答を返してから DPR を送るのは、最後の要求の応答で `LOCKED` になった shut down だけです。

`operational_state` は `ENABLED` のままです。HSS は自分が持つ加入者から答えるので、ほかのノードへの接続を必要としません。

運用から外した HSS が新しい要求すべてに `DIAMETER_TOO_BUSY` を返す作りも考えられます。この応答は、送り手に別のサーバーを試すよう伝えるものです。しかし、そうはしません。この応答を使ってよいのは、特定のサーバーを指した要求を、そのサーバーが扱えないときだけだからです（RFC 6733 7.1.3）。サーバーを指すのは `Destination-Host` です。UAR、LIR、MAR、未登録の利用者への SAR、Sh の要求は `Destination-Realm` だけを持ち、ホストを指しません。これらはレルムのどの台が扱ってもよい要求なので、`DIAMETER_TOO_BUSY` は誤った応答になります。

1. HSS を shut down します。

   ```sh
   docker compose exec hss sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/hss/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   応答の最後に HSS の状態が出ます。処理中の Cx と Sh の要求が無いので、すぐに `LOCKED` になります。

   ```json
   {"role":"hss","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |              |              |  DPR, DPA     |              |
 |              |              |<---------------------------->|
 |              |              |               |  DPR, DPA    |
 |              |              |               |<------------>|
```

- HSS が DPR を送り、相手が DPA を返し、DPA を受け取った側が TCP の接続を閉じます（RFC 6733 5.4）。ラダー図にあるのは Cx の 2 本です。IP-SM-GW と MMTel AS も Sh の接続を 1 本ずつ持っていて、そちらにも DPR が出るので、このやり取りは全部で 4 回です。`docker compose logs hss` には、そのあとに `stopped accepting connections` が出ます。
- Wireshark では、`diameter.cmd.code == 282` で DPR と DPA が見られます。どの DPR も `Disconnect-Cause` AVP は `REBOOTING (0)` です。
- I-CSCF と S-CSCF には、要求を受ける HSS が 1 台も無くなりました。`docker compose exec icscf wget -qO- http://127.0.0.1:8085/maintenance/v1/state` を見ると、I-CSCF の `"operational_state":"DISABLED"` が出ます。運用者は I-CSCF を外していませんが、HSS が無ければ I-CSCF は動けないためです。
- どちらも Tc ごと、つまり 30 秒ごとに接続し直そうとして、lock が解けるまで断られます。`docker compose logs icscf` には、試すたびに `connection failed` が出ます。Wireshark では、`tcp.flags.reset == 1 && tcp.srcport == 3868` で、HSS が接続を断るときのリセットが見られます。
- 最初に断られた試行で、どちらも HSS についてのアラームを上げます。DPR から Tc たったころです。DPR だけではアラームは上がりません。DPR は相手に戻ってくるよう求めているためです。アラームの中身は [HSS についてのアラームを読む](#hss-についてのアラームを読む) にあります。

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
 |              |  480         |               |              |
 |              |<-------------|               |              |
 |  504         |              |               |              |
 |<-------------|              |               |              |
```

- I-CSCF には UAR を送る先の HSS がありません。どの S-CSCF が利用者を扱うかを調べられないので、**480 Temporarily Unavailable** を返します（TS 24.229 5.3.1.3）。
- P-CSCF は 480 を受けると REGISTER を次の I-CSCF へ送ります（TS 24.229 5.2.2.1 の 7 番目の項目）。この網の DNS のゾーンには I-CSCF が 1 つしかないので、次がありません。そこで P-CSCF は UE に **504 Server Time-out** を返します。
- Wireshark では、`sip.Status-Code == 480` で I-CSCF の応答が見られます。その前に UAR は出ていません。Cx の接続が無いためです。

3. 同じ要求に `UNLOCKED` を入れて、HSS の lock を解きます。

   ```sh
   docker compose exec hss sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/hss/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   ノードはまた待ち受けを始めます。I-CSCF と S-CSCF は遅くとも Tc 後の次の試行で戻ってきます。`docker compose logs icscf` には、新しい CER と CEA、それに `"to":"I-Open (OKAY)"` が出ます。次の `ue register` は前と同じように登録します。CEA を受けると、各ノードはアラームを解除します。

lock を解くとき、ノードはポート 3868 をもう一度確保しますが、運用から外しているあいだにほかのものがそのポートを取っていれば失敗します。失敗しても HSS の管理状態は `UNLOCKED` で、`"operational_state":"DISABLED"` を返し、`docker compose logs hss` には `cannot listen for Diameter connections again` が出ます。HSS はすでに `UNLOCKED` なので、もう一度 lock を解いても何も起きません。ポートを取り直させるには、lock をかけて、もう一度解きます。

管理状態はノードのプロセスのものです。起動し直したノードは `UNLOCKED` です。`scripts/e2e-observability.sh` はこの手順も実行し、キャプチャで DPR を、保守用の API でノードのアラームを確かめます。

### HSS についてのアラームを読む

各ノードは、見つけた障害を一覧にして持っています。TS 28.111 のアラームの一覧です。アラームは、役割が障害を見つけたときに上げ、その状態が無くなったときに解除します。運用者は一覧を読んでアラームを確認できますが、解除はできません（TS 28.111 6.8）。I-CSCF、S-CSCF、IP-SM-GW、MMTel AS、Service Centre は、届かなくなった HSS のインスタンスごとに 1 つずつアラームを上げます。HSS を運用から外しているあいだに、S-CSCF の保守用の API で一覧を読みます。

```sh
docker compose exec scscf wget -qO- 'http://127.0.0.1:8085/maintenance/v1/alarms?cleared=false'
```

```json
{"node":"scscf","instance":"3f1c0e2a9b7d","num_of_alarm_records":1,"last_modification":"2026-11-24T10:15:02Z","dropped_records":0,
 "alarm_records":[{"alarm_id":"9f3c2a7b1d4e6058","object_instance":"ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
  "role":"scscf","peer":"hss.ims.mnc001.mcc001.3gppnetwork.org","notification_id":4711093825561044,"alarm_raised_time":"2026-11-24T10:15:02Z",
  "alarm_type":"COMMUNICATIONS_ALARM","probable_cause":"COMMUNICATIONS_SUBSYSTEM_FAILURE","specific_problem":"Diameter peer down",
  "perceived_severity":"MAJOR","additional_text":"no transport connection to the peer","ack_state":"UNACKNOWLEDGED"}]}
```

| フィールド | 値 | 理由 |
| --- | --- | --- |
| `object_instance` | `ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org` | アラームの対象は、HSS のインスタンス 1 つです。Origin-Host で示します。インスタンスが複数ある網では、届かなくなったインスタンスがそれぞれ自分のアラームを持ち、直すべきインスタンスがわかります。S-CSCF にまだ届くインスタンスが残っているかは、アラームではなく `operational_state` が示します。 |
| `alarm_type` | `COMMUNICATIONS_ALARM` | ある地点から別の地点へ情報を運ぶところの障害です（ITU-T X.733 8.1.1）。 |
| `probable_cause` | `COMMUNICATIONS_SUBSYSTEM_FAILURE` | リンクの上で通信を運ぶ部分の故障です（X.733 8.1.2.1）。TS 28.111 は表 B.2 にこの原因を通信のアラームの種類とともに挙げ、OpenAPI の定義でこの綴りにしています。 |
| `specific_problem` | `Diameter peer down` | 原因が同じほかのアラーム、たとえばメディア中継ノードについてのアラームと区別します。 |
| `perceived_severity` | `MAJOR` | サービスに影響があり、急いで対処が要ります（X.733 8.1.2.3）。ただし、ほかの HSS のインスタンスを持つノードはサービスを続けられます。 |
| `additional_text` | `no transport connection to the peer` | 何が失敗したか。ここでは HSS が TCP の接続を断りました。`no capabilities exchange with the peer` は、接続はできたが CEA が返ってこなかったことを示します。 |

アラームが上がるのは、接続を開く試行が失敗したときで、接続が閉じたときではありません。接続を失ったノードは Tc 後に接続し直します（RFC 6733 2.1）。それまでに相手が戻っていればアラームは上がらないので、短い再接続のたびにアラームが上がって消えることはありません。ウォッチドッグだけでもアラームは上がりません。ウォッチドッグが疑った接続（RFC 3539 3.4.1）は、Tw のあとで閉じ、Tc 後に接続し直します。アラームが上がるのは、それが失敗したときだけです。そうしないと、答えの遅い相手のせいで、Tw の数倍ごとにアラームが上がったり消えたりします。最初の失敗のあとの試行が失敗しても、一覧は何も変わりません。

アラームが上がる道は、もう 1 つあります。DPR です。相手が `Disconnect-Cause` に `BUSY` か `DO_NOT_WANT_TO_TALK_TO_YOU` を入れた DPR で接続を終えると、それは接続し直さないでほしいという意味です（RFC 6733 5.4.3）。ノードはもう接続を試みません。そのためアラームはすぐに上がり、`additional_text` は `the peer disconnected and asked not to be connected to again` になります。このアラームは解除されません。ノードが起動し直すまで、接続が開くことはないためです。この網の HSS はいつも `REBOOTING` を送るので、HSS のドレインでこの形のアラームが上がることはありません。

次の接続の CEA でアラームは解除されます。`perceived_severity` が `CLEARED` になり、`alarm_cleared_time` に解除した時刻が入ります。記録は、運用者が `PUT /maintenance/v1/alarms/{alarm_id}/ack-state` で確認するまで一覧に残ります。あとから一覧を見ても、何が起きたかがわかります。1 時間のうちに誰も確認しなければ、ノードが `ack_user_id` を `retention` にして自分で確認します（[HTTP の API](apis.md#保守の-api-で省いたこと)）。HSS が待ち受けを始める前に起動したノードは、最初の試行で上げて解除した記録を、すでに持っていることがあります。

OAM は、インベントリのすべてのノードのアラームの一覧を集めます。

```sh
docker compose exec oam wget -qO - 'http://127.0.0.1:8080/oam/v1/alarms?cleared=false'
```

`alarms` には 5 つのノードのアラームが、重いものから順に入り、記録ごとに `node` が付きます。OAM の画面のアラームの部分も、同じ一覧を見せます。答えなかったノードは、理由とともに `nodes` に入ります。そのノードのアラームは「無い」のではなく「わからない」のです。OAM は、読めないノードについてアラームを作りません。

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
| HSS。TS 29.228 6.2.2.1 | 誤りを返す PPA を受けても、登録の状態をそのままにする。S-CSCF が知らない利用者を未登録にすることも、SERVER_CHANGE で登録を解除することもしない。 | 送れなかった push のために、利用者の登録を取り上げないため。S-CSCF は次の登録でプロファイルを読み直し、知らない利用者のことは HSS に問い合わせ直す。 |
| HSS。TS 29.228 6.5.2.1 | 登録中の利用者の既定の公開の識別子を `PUT` で変えたとき（組の順番を変える、最初の識別子を禁止にする）、HSS は何も送らない。仕様がこの場合に使うことを禁じている PPR も、代わりに求めている RTR も送らない。 | 新しい既定の識別子は、変わったプロファイルのほかの部分と一緒に、次の登録か次の PPR で S-CSCF に届く。 |
| HSS。TS 29.228 6.5.2.3、6.5.2.4、6.5.2.5 | PPR が運ぶのはユーザープロファイルだけ。課金のアドレスも、変わった SIP Digest のパスワードも、PPR が運べるほかのユーザー情報も送らない。 | HSS は課金のアドレスを持たない。パスワードは、登録と登録のあいだ S-CSCF が持っていないので、新しい値で置き換えるものが無いため。 |
| 利用者のプロファイル。TS 29.228 Annex E | サービスプロファイルは 1 つ。任意の要素は、規制の表示、公開サービス識別子の種類、iFC のほかに入れない。 | 試験用の加入者と PSI ユーザーにはそれ以上要らないため。 |
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
| OAM。TS 32.600 4.2.1.1 | 網の設定を変えてよいのは、権限のある運用者だけとされているが、OAM の API には認証も認可も無い。要求の `operator` は名乗った名前で、確かめた身元ではない。OAM が OSS に向けて出す 3GPP の管理のサービスにも、認証と認可は無い。そこに届く相手なら誰でも、警報を読んで確認でき、http か https の任意の宛先で購読を作れる。OAM は、その宛先に警報の通知を送る。 | この待ち受けは、ノードの保守用の API の待ち受けと同じく、ノードのネットワークにだけあり、ホストのどのアドレスにも置いていない。また、キャプチャで中身を読めるように、このプロジェクトのどのインタフェースも平文で動くため。 |
| OAM。TS 33.102 6.3.1 | バックアップの書庫を暗号化しない。ファイルの許可だけが守りになる。 | 長期鍵を持ってよいのは USIM とホーム網の認証センターだけなので、HSS の外に置く写しは、その認証センターと同じだけ守る必要があり、製品はこうした書庫を暗号化する。この網にあるのは試験用の資格情報だけで、暗号化には鍵そのものとその扱いが要る。それはバックアップと復元で学ぶことではないため。 |
| OAM。TS 28.111 6.11、6.12 | OAM が OSS に見せる警報の一覧の `unreliableAlarmScope` は、答えないノードが 1 台ならそのノードを、2 台以上なら `SubNetwork` 全体を示す。答えないノードを並べることはない。 | 警報の一覧の OpenAPI のファイルは、この属性を DN 1 つの型にしている。TS 28.111 7.4.1 では複数の値を持てるが、一覧全体も、6.11 がこの属性で示してよいとする範囲の 1 つであるため。 |
| OAM。TS 32.158 6.1.2、6.2.2 | その警報の一覧の読み出しは、`attributes` と、`BASE_ONLY` 以外の scope を受けない。`fields` で選べるのは警報の記録だけ。それ以外には 500 と `QUERY_PARAMS_TOO_COMPLEX` を返す。 | 警報の一覧の下にはオブジェクトが無いので、どの scope も一覧そのものより先には届かない。また、OSS が一覧から読むのは警報の記録であるため。 |
| OAM。TS 32.158 6.3.1 | 2 台以上のノードの警報の記録を確認する patch は、500 と `SERVER_LIMITATION` で断る。本文をすべて確かめたあと、どのノードにも聞く前に断るので、どの一覧も変わらない。 | patch は、全体を当てるか、何も当てないかのどちらかでなければならない。ノードをまたいでそうするには、ノードのあいだで 2 相の確定が要る。OSS は、代わりにノードごとに patch を送るため。 |
| OAM。TS 28.111 A.1.4 | 警報を確認する patch の 3 つの形のうち、JSON Merge Patch だけを受ける。JSON Patch と 3GPP の 2 つの形には 415 を返す。 | 3GPP の形の media type の綴りが、TS 28.532 と TS 32.158 で違う。また、確認には Merge Patch で足りるため。 |
| OAM。TS 28.622 4.3.22.1 | `notificationTypes` の無い OSS の購読に送るのは、警報の 6 つの通知だけ。OAM は TS 28.532 の構成の通知を送らないので、購読を作ったことも消したことも通知しない。購読の範囲は `SubNetwork` 全体。 | 故障の監視に要るのは警報の通知だけで、一部のノードの警報だけが欲しい OSS は `notificationFilter` で選べるため。 |

## 後片付け

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。
