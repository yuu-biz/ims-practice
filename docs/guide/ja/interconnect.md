# IMS 網どうしを相互接続する

[English](../en/interconnect.md)

この手順書では、2 つの事業者の IMS 網をつなぎ、一方の網からもう一方の網への通話と折り返しの通話、両方の向きのショートメッセージを追います。各網の境界には、SIP を扱う IBCF と、メディアを中継する TrGW があります。それぞれがメッセージのどこを変えるか、各網のキャプチャに相手網の何が見えるか、2 つの事業者の取り決めが食い違うと何が起きるかを見ていきます。

次の版に従います。TS 24.229 V20.1.0、TS 29.165 V20.0.0（II-NNI）、TS 23.204 V19.1.0 と TS 24.341 V19.0.0（SMS over IP）、TS 23.040 V20.0.0、TS 32.409 V19.0.0（性能の測定）、TS 24.147 V19.0.0（会議）、RFC 3261、RFC 3312、RFC 3966 です。以下の条項の番号は、これらの版のものです。

## 始める前に

- [音声通話の流れを追う](call.md)、[SMS の流れを追う](sms.md)、[MMTel AS の付加サービス](mmtel.md) を読んでおいてください。この手順書はそこで説明したことを前提にします。網 A の INVITE は、今までどおりすべて MMTel AS を通ります。
- 略語の意味は [用語集](../../glossary.md) にあります。

## 2 つの網

網 A は `compose.yaml` の網で、ほかの手順書の自網です。網 B は相手網で、別の事業者の 2 つ目の IMS です。同じイメージを別の設定で動かし、ドメイン、番号、加入者を網 A と分けています。`deploy/interconnect/compose.yaml` を `compose.yaml` に重ねて足します。`compose.yaml` は変えていません。網 B のサービスの名前は `-b` で終わります。`scscf-b` は網 B の S-CSCF で、`scscf` は網 A の S-CSCF です。ノードの設定ファイルは、網ごとに `deploy/interconnect/a` と `deploy/interconnect/b` にあります。

```text
       網 A（ims0）                    網のあいだ（nni0）           網 B（ims1）
     172.28.0.0/24                   172.28.2.0/24               172.28.1.0/24

 UE A1  P-CSCF  S-CSCF            .10         .20               I-CSCF  S-CSCF  P-CSCF  UE B1
   |      |       |    IBCF-A  ---------- Ici ----------  IBCF-B    |       |       |      |
   |      |       |-----(.90)                                (.90)--|       |       |      |
   |     AGW            TrGW-A ---------- Izi ----------  TrGW-B                          |
   |    (.50)-----------(.91) .11                    .21  (.91)---------------------------|
```

網のあいだのブリッジ `nni0` には、IBCF と TrGW しかつながっていません。網 A の中のノードは、網 B の中のノードに直接は届きません。すべて境界を通ります。キャプチャはブリッジごとに 1 つずつ取ります。網 A は `captures/ims-*.pcap`、網 B は `captures/b/ims-*.pcap`、網のあいだは `captures/nni/ims-*.pcap` です。

| ノード | 網 A | 網 B |
| --- | --- | --- |
| ドメイン | `ims.mnc001.mcc001.3gppnetwork.org`（試験用の PLMN 001/01） | `ims.mnc002.mcc001.3gppnetwork.org`（試験用の PLMN 001/02） |
| UE 1 | UE A1、172.28.0.100、`+1-202-555-0101` | UE B1、172.28.1.100、`+1-202-555-0151` |
| UE 2 | UE A2、172.28.0.101、`+1-202-555-0102` | UE B2、172.28.1.101、`+1-202-555-0152` |
| P-CSCF | 172.28.0.40。メディア中継ノードは 172.28.0.50 | 172.28.1.40。メディア中継ノードは無い |
| I-CSCF | 172.28.0.20 | 172.28.1.20 |
| S-CSCF | 172.28.0.30 | 172.28.1.30 |
| MMTel AS | 172.28.0.75 | 無い |
| IP-SM-GW と SC のスタブ | 172.28.0.70。SC は `+12025550100` | 172.28.1.70。SC は `+12025550150` |
| HSS | 172.28.0.10 | 172.28.1.10 |
| DNS | 172.28.0.2 | 172.28.1.2 |
| IBCF | IBCF-A。網の中は 172.28.0.90、網のあいだは 172.28.2.10 | IBCF-B。網の中は 172.28.1.90、網のあいだは 172.28.2.20 |
| TrGW | TrGW-A。網の中は 172.28.0.91、網のあいだは 172.28.2.11 | TrGW-B。網の中は 172.28.1.91、網のあいだは 172.28.2.21 |

網 B は、シナリオに要る最小限のノードだけにしました。MMTel AS、EPC と PCRF、P-CSCF のメディア中継ノード、ラボはありません。番号はすべて、北米番号計画が架空の番号に取ってある 555-0100 から 555-0199 の範囲です。鍵もすべて試験用の値です。

## IBCF と TrGW の仕事

TS 29.165 は、IMS 網どうしのあいだのインタフェースを II-NNI と呼びます。参照点は 2 つあります（TS 29.165 5.1）。IBCF どうしの Ici は SIP を、TrGW どうしの Izi はメディアを運びます。自網の中では、IBCF は Mx で CSCF とやり取りします。TrGW へ向かう参照点は Ix です（TS 29.165 3.2）。

IBCF には 2 つの側があります。このプロジェクトの IBCF は **B2BUA** で（TS 24.229 5.10.5、TS 29.165 5.2.1）、側ごとに UA を 1 つ持ちます。内側の UA は自網のアドレスで、外側の UA は網のあいだのアドレスで待ちます。内側に届いた要求は網から出ていくので、IBCF はその**出口**です（TS 24.229 5.10.2）。外側に届いた要求は網に入ってくるので、IBCF はその**入口**です（5.10.3）。IBCF は、届いた要求を送ってきた側とのダイアログで受け止め、もう一方の側とのダイアログで新しい要求を送ります。ダイアログごとに、Call-ID、tag、CSeq、Via、Contact が別々です。一方のダイアログから他方へ何を写すかは、相手網との取り決めで決まります。このプロジェクトでは、この取り決めを**相互接続プロファイル**と呼びます。

- 信頼ドメインの外に出してはいけないヘッダを消します（TS 24.229 4.4）。
- メソッド、option tag、ヘッダを調べます（5.10.6）。
- 番号を取り決めの形式に直します（TS 29.165 8.1）。
- 相手網が受けないコーデックを SDP から外します（TS 29.165 7.1）。
- メディアを TrGW に通し、TrGW のアドレスを SDP に書きます（TS 24.229 6.7.1.4.1）。

一方の網の中でメッセージを運ぶための値は、相手網とのダイアログに入りません。網 A のノードの Via、Route、Record-Route、Contact と、網 A の Call-ID と tag は、網 B に届きません。B2BUA は、こうして自網の構成を隠します。TS 24.229 5.10.4 は、要求をプロキシとして通す IBCF のための別のやり方を書いています。そうした値を暗号化したトークンに封じるやり方です。B2BUA には封じるものがありません。届いたまま境界を越える Contact は、GRUU と会議のフォーカスのものだけです（「境界を越える会議」を見てください）。

TrGW は、ここでも役割 `agw` のメディア中継ノードです。2 つのブリッジにつながっています。ターミネーション `core` は自網を、`access` は相手網の TrGW を向きます。TS 29.165 3.2 は、IBCF と TrGW のあいだの参照点を Ix と呼び、そのプロトコルは H.248 です（TS 29.238）。このプロジェクトの IBCF は、P-CSCF がメディア中継ノードに使うのと同じ HTTP の制御 API と同じクライアントで、中継ノードを動かします。操作の名前は H.248 のコマンドのものです。Add は両側にターミネーションを 1 つずつ持つコンテキストを確保し、Modify はターミネーションにその側のメディアの宛先を伝え、Subtract はコンテキストを解放します（`internal/ibcf/trgw.go` のインタフェース `TrGW`）。中継ノードは両方のターミネーションに同じアドレスを書きます。そこで IBCF は、それぞれの側の SDP に、その網での TrGW のアドレスと、中継ノードが選んだポートを書きます。

呼の 2 つのダイアログは、その INVITE を受けた IBCF のプロセスの中にあります。IBCF の各台は、自分のダイアログの Contact に自分の台の名前を書きます。そのため、呼のあとの要求はその台に戻り、新しい呼は網のどの台に届いてもかまいません。複数の台で仕事を分けられるように、IBCF は全部の呼の表を持ちません。各台は、自分の呼の記録を自分の名前の下に置きます。

## 動かす

`scripts/e2e-interconnect.sh` が、この手順書のことを全部行い、3 つのキャプチャで確かめます。手で動かすときは次のようにします。

1. 2 つの網を起動します。

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml up -d --build
   ```

   IBCF は、2 つの URI と相手網の名前を付けて `"msg":"IBCF ready"` をログに出します。

2. 1 つ目の端末で、UE B1 に着信を待たせます。UE シミュレータには網 B の設定をフラグで渡します。

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue-b1 answer \
     -domain ims.mnc002.mcc001.3gppnetwork.org \
     -impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org \
     -impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org \
     -password test-password-b1 \
     -pcscf 172.28.1.40:5060 -pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060
   ```

3. 2 つ目の端末で、UE A1 から番号で電話をかけます。

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue call tel:+12025550151
   ```

   UE A1 は `183`、`180`、`200` をログに出し、続けて `"msg":"call answered"` を出します。相手のアドレスは網 A のメディア中継ノード `172.28.0.50` です。UE B1 は `"msg":"media negotiated"` をログに出し、相手のアドレスは TrGW-B の `172.28.1.91` です。どちらの UE にも、相手網のことは何も伝わりません。

4. 折り返すときは、UE A2 に着信を待たせ（`run --rm ue2 answer -impi 001010000000002@... -impu sip:+12025550102@... -password test-password-2`）、UE B1 から手順 2 のフラグで `tel:+12025550102` にかけます。

5. ショートメッセージを送るときは、UE B1 を登録したままにし（手順 2 のフラグで `run --rm ue-b1 register`）、UE A1 から送ります。

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue sms -status-report +12025550151 "Hello UE-B1"
   ```

6. 返事を送るときは、手順 5 の UE B1 の登録を止め、UE A1 を登録したままにし（`run --rm ue register`）、UE B1 から、手順 2 のフラグに網 B の SC の番号 `-sc +12025550150` を加えて送ります。

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue-b1 sms <手順 2 のフラグ> -sc +12025550150 -status-report +12025550101 "Hello UE-A1"
   ```

   UE B1 は、送るときに自分で登録します。別の登録が残っていると、status report がそちらに届くことがあります。

## 網 A から網 B への通話

```text
UE A1  P-CSCF  S-CSCF  MMTel  DNS-A  IBCF-A ║ IBCF-B  I-CSCF  HSS-B  S-CSCF  P-CSCF  UE B1
  |INVITE |      |       |      |      |    ║   |       |       |       |       |      |
  |------>|----->|------>| (1)  |      |    ║   |       |       |       |       |      |
  |       |      |<------|      |      |    ║   |       |       |       |       |      |
  |       |      | ENUM (2)     |      |    ║   |       |       |       |       |      |
  |       |      |------------->|      |    ║   |       |       |       |       |      |
  |       |      |   INVITE (3) |      |    ║   |       |       |       |       |      |
  |       |      |-------------------->|    ║   |       |       |       |       |      |
  |       |      |       |      |      |  INVITE (4)    |       |       |       |      |
  |       |      |       |      |      |=============>|(5)     |       |       |      |
  |       |      |       |      |      |    ║   |------>| LIR (6)       |       |      |
  |       |      |       |      |      |    ║   |       |<----->|       |       |      |
  |       |      |       |      |      |    ║   |       |-------------->|------>|----->|
  |       |      |       |      |      |    ║   |       |       |  183  |       | (7)  |
  |       |      |       |      |      |    ║   |<------|<--------------|<------|<-----|
  |<------|<-----|<------|<-----------------|<=============|      |       |       |      |
  | PRACK、180、PRACK、200、ACK (8)。A1-IBCF-A、IBCF-A-IBCF-B、IBCF-B-B1 の 3 つのダイアログ |
  |<================= RTP。AGW、TrGW-A、TrGW-B を通る (9) ==============================>|
```

二重線は網のあいだの `nni0` です。そこを通るメッセージは、IBCF-A と IBCF-B のあいだのダイアログのもので、どちらの UE にも見えません。

### 1. UE A1 が番号に電話をかける

UE A1 は、いつもの通話と同じように `tel:+12025550151` へ INVITE を送ります。P-CSCF は S-CSCF に渡します。S-CSCF は発信側のフィルタ条件で MMTel AS に送り、AS から戻ってきます（MMTel の手順書を見てください）。ここまでは、番号が別の網のものだと誰も知りません。

### 2. ENUM で網 B が分かる

呼び出す AS が残っていなければ、S-CSCF は番号を ENUM で変換します（TS 24.229 5.4.3.2 の手順 10）。網 A の DNS には、網 B の番号のためのゾーン `5.1.0.5.5.5.2.0.2.1.e164.arpa` があります。`1.5.1.0.5.5.5.2.0.2.1.e164.arpa` を引くと、次の答えが返ります。

```text
NAPTR 10 100 "u" "E2U+sip" "!^.*$!sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone!" .
```

結果は、網 B のドメインの SIP URI に、国際形式の番号と `user=phone` を入れたものです。non-roaming の II-NNI ならどこでも使える形です（TS 29.165 8.1）。レコードに書くのは番号で、加入者の私的な識別子ではありません。それは網 B の中だけのものです。

### 3. S-CSCF が IBCF-A に送る

ドメインは網 A のものではありません。TS 24.229 5.4.3.2 の手順 11 は、相互接続の機能が要る宛先への要求を、自網の IBCF を通して送るように求めます。手順 12 は、網が構成を隠すときに IBCF を Route の先頭に入れるとしています。IBCF の URI は S-CSCF の設定 `ibcf` にあります（`deploy/interconnect/a/scscf.yaml`）。

```text
INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0
Route: <sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>
Record-Route: <sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>
```

S-CSCF は今までどおり Record-Route に自分を入れます（手順 13）。ダイアログの中の要求も S-CSCF を通ります。設定 `ibcf` が無ければ、S-CSCF は網 B のドメインに自分で送ろうとします。しかし網 B には届きません。

### 4. IBCF-A が出口として働く

IBCF-A は、網 A とのダイアログの UAS として内側で要求を受け、TS 24.229 5.10.2.2 の手順を行います。

- Route の先頭から自分の URI を外します（手順 1A）。
- Request-URI のホストから相手網を探し、プロファイル `profiles/network-b.yaml` を選びます。
- 要求の写しにプロファイルを当てます（下の節で説明します）。信頼ドメイン、ヘッダの規則、番号、コーデックです。P-Charging-Function-Addresses は消します（手順 9）。
- TrGW-A にコンテキストを作ります。ターミネーション `core` には、網 A がメディアを受けるアドレス（メディア中継ノードの 172.28.0.50）を教えます。SDP の `c=` と `o=` には、もう一方のターミネーションのアドレス 172.28.2.11 を書きます（TS 24.229 6.7.1.4.1）。
- 網 B とのダイアログの UAC として、外側から新しい INVITE を送ります。Call-ID と From の tag は新しく作り、Via と Contact は自分のもの、CSeq は 1 です。Route には、プロファイルにある入口の IBCF-B を入れます（5.10.2.2 NOTE 3）。新しい INVITE には、写しのヘッダのうち 1 つのダイアログに属さないもの、本文、発信者の Supported と Session-Expires を入れます。

Record-Route には自分を入れません（手順 2B）。呼のあとの要求は、それぞれのダイアログの相手として IBCF に届くためです。ただし、Contact を届いたまま渡すときは別です（「境界を越える会議」を見てください）。SDP 付きの INVITE は 1300 バイトを超えるので、TCP で送ります（RFC 3261 18.1.1。TS 24.229 4.2A がこれを指しています）。

### 5. IBCF-B が入口として働く

IBCF-B は外側で要求を受けます。送信元のアドレス 172.28.2.10 が網 A のプロファイルにあるので、網 A からの要求だと分かります。プロファイル `profiles/network-a.yaml` を当て、Route から自分の URI を外します（TS 24.229 5.10.3.2 の手順 1C）。Route が残っていないので、設定した I-CSCF へ、自分のダイアログで新しい INVITE を送ります（手順 4）。メディアも同じように TrGW-B に通します。網 B に届く SDP は 172.28.1.91 を指します。

### 6. 網 B の I-CSCF が着信者を探す

Request-URI は、国際形式の番号と `user=phone` を持つ SIP URI です。HSS は UE B1 を tel URI で知っていて、この SIP URI では知りません。そこで I-CSCF は、HSS に LIR を送る前に Request-URI を `tel:+12025550151` に置き換えます（TS 24.229 5.3.2.1 の手順 1 の b）。網 B の S-CSCF には tel URI が届き、あとはいつもの着信と同じように、網 B の P-CSCF を通って UE B1 に届きます。

### 7. 答えが戻る

UE B1 は、SDP の answer を付けた信頼性のある 183 で答えます。各 IBCF は、それをもう一方のダイアログの自分の 183 として渡します。そのとき、プロファイルが通すヘッダだけを写します。IBCF-B は TrGW-B に UE B1 のアドレスを教え、SDP に 172.28.2.21 を書きます。IBCF-A は TrGW-A に 172.28.2.21 を教え、網 A へ向けた SDP に 172.28.0.91 を書きます。

### 8. 呼の残り

PRACK、ACK、BYE は、ダイアログごとにあります。IBCF は、着信側の信頼性のある応答に、発信側がその写しに PRACK を返してから PRACK を返します。そのため PRACK は、今までどおり端から端まで届きます（RFC 3262）。SDP を持つ PRACK、UPDATE、re-INVITE は、INVITE と同じように TrGW を通ります。一方の側が切ると、IBCF はそのダイアログで BYE に答え、もう一方のダイアログで自分の BYE を同じ Reason で送ります（RFC 3326）。一方のダイアログが失われたとき、たとえば相手が要求に 481 を返したときも同じです（RFC 3261 12.2.1.2）。そのあとで TrGW のコンテキストを解放します。

IBCF が送る INVITE は、その先で分かれる（フォークする）ことがあります。1 人の利用者の何台かの UE が、それぞれ自分の early dialog で答えるときです（RFC 3261 13.2.2.4）。IBCF は、発信側から見れば 1 つの UAS です。UAS は、1 つの要求へのすべての応答に、同じ To の tag を付けます（8.2.6.2）。そのため、向こう側の early dialog がいくつあっても、発信側と IBCF の early dialog は 1 つです。発信側は、そのダイアログで最初に届いたセッション記述を answer とし、あとのものは無視します（13.2.1）。そこで IBCF は、最初にセッション記述を送ってきたダイアログのものを渡し、ほかのダイアログの応答はセッション記述を外して渡します。外したものは、ダイアログごとに覚えておきます。ここまでは、最初のダイアログが続いているあいだのことです。199（RFC 6228）でそのダイアログが終わったあとは、発信側の offer への answer として次に届いたセッション記述を、そのまま発信側に渡します。プリコンディションを使う UAS は、発信側が資源を確保したと分かってから利用者を呼び出すので、発信側がその answer を受け取る必要があるためです（RFC 3312）。発信側と IBCF の early dialog は 1 つのまま続くので、発信側はこの 199 を見ません。そのため、すでに answer を受け取っている発信側は、次のセッション記述を無視して、持っているものを使い続けます（13.2.1）。IBCF は、そのセッション記述を発信側が持っているものとして数えるので、そのダイアログが応えても、自分からは offer しません。どれかのダイアログが 200 で応えたら、IBCF は TrGW をそのダイアログに向けます。200 の SDP か、そのダイアログが最後に送った SDP を使います。応えたダイアログが、発信側がセッション記述を受け取ったダイアログでなければ、IBCF は、発信側が 200 に ACK を返したあとで、応えたダイアログのセッションを発信側に offer します。発信側の INVITE が Allow に UPDATE を挙げていれば UPDATE で（RFC 3311）、挙げていなければ re-INVITE で送ります（RFC 3261 14.1）。offer を送らないと、発信側のメディアは最初のダイアログが選んだコーデックのままになるためです。この offer の `o=` 行は、発信側が受け取ったセッション記述のものと同じで、版の番号（version）だけを 1 上げます（RFC 3264 8）。SDP は、ほかのものと同じく TrGW を通ります。offer の無い INVITE のあとでは、応えたダイアログの 200 が、その UAS の offer を運びます。そのときは、発信側の answer を ACK に入れて、その UAS に送ります（13.2.2.4）。発信側の offer と行き違いになった offer には 491（Request Pending）が返るので、少し待ってから送り直します。送るのは合わせて 3 回までです（14.1）。`internal/ibcf` の試験が、両方の場合を見せます（`TestForkBeyondTheBorder`）。スクリプトにはフォークの場面はありません。

### 9. メディア

RTP は、UE A1、網 A のメディア中継ノード、TrGW-A、網のあいだ、TrGW-B、UE B1 の順に流れます。スクリプトは、UE A1 が送った PCMU のペイロードと、UE B1 が受けたものが同じであることを確かめます。

UE B1 から UE A2 への折り返しも、網の役割を入れ替えるだけで同じです。網 B の ENUM は `sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org;user=phone` を返し、網 B の S-CSCF は IBCF-B に送ります。網 A の I-CSCF が `tel:+12025550102` に直します。網 A の中では、着信側として UE A2 の MMTel AS を通ります。

## 各キャプチャに見えるもの

| キャプチャ | 見えるもの | 見えてはいけないもの |
| --- | --- | --- |
| `captures/ims-*.pcap`（網 A） | 網 B の番号の ENUM、IBCF を Route に入れた S-CSCF から IBCF-A への INVITE、IBCF-A が中継する網 B のメッセージ | 172.28.1.0/24 のアドレスと、網 B のノードのホスト名 |
| `captures/nni/ims-*.pcap`（網のあいだ） | 2 つの IBCF と 2 つの TrGW だけ。IBCF の外側の名前を持つ 1 つの Via と Contact。IBCF が作った Call-ID。網のあいだの TrGW のアドレスを持つ SDP。TrGW どうしの RTP | どちらの網の中のアドレスも、ノードのホスト名も、網 A の IMSI から作った識別子も、Record-Route も |
| `captures/b/ims-*.pcap`（網 B） | IBCF-B から I-CSCF への INVITE とその先、tel URI、TrGW-B を指す SDP | 172.28.0.0/24 のアドレス、網 A のノードのホスト名、網 A の IMSI から作った識別子 |

これがトポロジーの秘匿の確認です。相手網のキャプチャに、自網の中のアドレスが現れてはいけません。スクリプトは、IP のヘッダとフレームの全体からアドレスを探します。また、相手網のドメインの前に `pcscf`、`scscf`、`icscf`、`mmtel`、`agw`、`ipsmgw`、`hss`、`ibcf`、`trgw` などのノードの名前が付いていないかも探します。網 B のキャプチャと網のあいだのキャプチャからは、網 A の IMSI で始まるユーザー部も探し、1 つも見つかりません。

自網の名前のうち、相手網に見えてよいものもあります。ドメインそのものは、利用者の識別子に現れます。たとえば From と P-Asserted-Identity の `sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org` です。網をまたぐのは、利用者の番号の識別子です。相手網は、折り返すためにこれが要ります。番号とドメインからは、網の中のノードも IMSI も分かりません。

## キャプチャで見る 3 つのダイアログ

網のあいだのキャプチャを開き、172.28.2.10 から出た INVITE を見ます。ヘッダは次のとおりです（一部を省いています）。

```text
INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0
Via: SIP/2.0/TCP ibcf.nni.mnc001.example.net:5060;branch=z9hG4bKW6NI6...;rport
Max-Forwards: 65
Route: <sip:ibcf.nni.mnc002.example.net:5060;lr>
From: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;tag=5MWO4Z6R4C
To: <tel:+12025550151>
Call-ID: FMZQJ3BZL2YI4ILP6CEIPAUT5Z
CSeq: 1 INVITE
Contact: <sip:ibcf.nni.mnc001.example.net:5060>
Supported: 100rel, timer, 199, precondition, replaces
```

- **Via**。IBCF-A の 1 つだけです。網 A のノードは、IBCF-A に届いた INVITE に自分の Via を入れました。その INVITE は IBCF-A で終わっています。
- **Record-Route**。ありません。IBCF-A がこのダイアログの相手なので、網 B の要求は Contact を見て IBCF-A に届きます。
- **Contact**。IBCF-A の外側の名前です。網 B からの PRACK、ACK、BYE はここに届きます。
- **Call-ID、From の tag、CSeq**。IBCF-A が作ったものです。網 A のキャプチャで S-CSCF から IBCF-A への INVITE と、網 B のキャプチャで IBCF-B から I-CSCF への INVITE と、Call-ID を比べてください。1 つの呼が、3 つのキャプチャでそれぞれ別の Call-ID を持っています。
- **Max-Forwards**。IBCF-A は、プロキシと同じように 1 つ減らしています（RFC 3261 16.6 の手順 3）。2 つの網が呼を回し続けても、いずれ止まるようにするためです。
- **Supported**。UE A1 のものを、プロファイルの規則を当てたあとで写しています。UE A1 の使えない拡張を網 B が使わないようにするためです。`replaces` もこの中にあります。どちらの IBCF も渡す Replaces にもう一方の側のダイアログを書くためです（「境界を越える会議」を見てください）。

UE B1 に届く INVITE の Contact は、IBCF-B の内側の名前 `sip:ibcf.ims.mnc002.mcc001.3gppnetwork.org:5060` です。発信側がそこに付けていた機能タグも付いています（RFC 3840）。どちらの UE も、自網の IBCF としかやり取りしません。

## 境界を越える会議

Contact をいつも替えるわけではありません。TS 24.229 5.10.5 は、GRUU の Contact と、その URI で相手が何をできるかを示す機能タグを持つ Contact を、届いたまま渡すよう IBCF に求めています。例に挙がっているのは、会議のフォーカスのタグ `isfocus` です。その Contact の URI は会議の URI です。参加者はこの URI を覚えて会議の状態を購読し（TS 24.147 5.3.1.3.2）、この URI で会議に入り直します（5.10.5 の NOTE）。こうした Contact を渡すとき、IBCF は Record-Route に自分の URI を入れます。相手が会議の URI に送る要求が、IBCF を通るようにするためです。

それでも IBCF は B2BUA のままです。網 A のフォーカスが網 B の利用者を招く場合で見ます。フォーカスは自分が受け持つ PSI である会議の URI の代わりに、`orig` を付けて網 A の S-CSCF へ INVITE を送ります。S-CSCF は HSS から受け取った PSI ユーザーのプロファイルでこの PSI を担当し、UE A1 の発信と同じように INVITE の送り先を決めます。ENUM で番号が網 B の SIP URI になるので、INVITE は IBCF-A から出ていきます（[フォーカスが相手を招く方法](mrf.md#フォーカスが相手を招く方法)）。

```text
フォーカス（MMTel AS）  S-CSCF-A   IBCF-A ║ IBCF-B   UE B1
  | INVITE tel:+12025550151 |    ║   |        |
  | Route: <S-CSCF-A;lr;orig>    ║   |        |
  | Contact: <conf>;isfocus |    ║   |        |
  |---------------->|       |    ║   |        |  P-Asserted-Identity: <conf>、S-CSCF が担当する PSI
  |                 |------>|==========>|------->|  どの INVITE も Contact: <conf>;isfocus
  |                 |       | Record-Route:     |  Record-Route は網のあいだで IBCF-A、網 B で IBCF-B
  |                 |       |    ║   |        |
  |   SUBSCRIBE sip:conf（ダイアログの中）        |
  |<----------------|<------|<==========|<-------|
  |   NOTIFY        |       |    ║   |        |
  |---------------->|------>|==========>|------->|
```

- 網のあいだの INVITE も、UE B1 に届く INVITE も、Contact に `isfocus` 付きの会議の URI を持ちます。UE B1 は、自分が入った会議の URI を知ります。
- 各 IBCF は Record-Route に自分の名前を入れます。IBCF-A は網のあいだで外側の名前を、IBCF-B は網 B の中で内側の名前を入れます。網のあいだの Record-Route は、これだけです。網 A の中では S-CSCF-A も Record-Route に入るので、IBCF-A とフォーカスのあいだのダイアログの中の要求は S-CSCF-A を通ります。
- UE B1 がダイアログの中で送る要求は、Request-URI が会議の URI で、Route に IBCF-B があります。IBCF-B は、呼のほかの要求と同じく、網 B とのダイアログでこれを受け止めます。そして IBCF-A とのダイアログで、会議の URI に宛てた自分の要求を送ります。IBCF-A も、フォーカスに向けて同じことをします。
- フォーカスから見た UE B1 の側の Contact は、IBCF-A の内側の名前のままです。UE B1 の Contact にはそうしたタグが無いので、IBCF-A が替えます。

向きが逆でも同じです。フォーカスが 200 の Contact に `isfocus` を付けて応えると、各 IBCF は自分の 200 でその Contact を渡し、その Record-Route に自分を入れます。

GSMA IR.92 2.3.3 の 3 者通話では、フォーカスの INVITE に Replaces も入ります。呼をまとめる UE と招かれる利用者との呼を指す Replaces で（TS 24.147 5.3.2.5.4）、招かれた利用者はその呼の代わりに新しい呼を受けます。Replaces はダイアログを Call-ID と両端の tag で指し、その値は INVITE を受ける UA から見たものです。to-tag はその UA 自身の tag です（RFC 3891 3）。途中の B2BUA はそれぞれの側に自分のダイアログを持ち、Call-ID も tag も側ごとに違います。そこで B2BUA はどれも、Replaces に書くダイアログを、もう一方の側のもの、つまり次の宛先が知っているものに替えます。UE A1 の呼では MMTel AS がこれをします（TS 24.229 5.7.5.1）。IBCF も、IMS-ALG として自分の 2 つのダイアログの識別子を対応づけるので（5.10.5）、同じことをします。IBCF は Replaces が指すダイアログから `Store` の中の呼を探し、呼を持つのが IBCF の別の台でも見つけます。見つけた呼のもう一方の側のダイアログを書いて、INVITE を送ります。UE B1 は自分のダイアログを見つけ、新しい呼を受けて、古い呼を BYE で終えます。

```text
UE A1     フォーカス（MMTel AS） S-CSCF-A   IBCF-A ║ IBCF-B    UE B1
  | REFER: Refer-To は B1、ダイアログ 1 の Replaces  |          |
  |-------------->|               |         |  ║   |          |  ダイアログ 1: UE A1 と AS
  |               | INVITE、ダイアログ 2 の Replaces |          |
  |               |-------------->|-------->|  ║   |          |  ダイアログ 2: AS と IBCF-A
  |               |               |         | INVITE、ダイアログ 3 の Replaces
  |               |               |         |=======>|          |  ダイアログ 3: 網のあいだの IBCF-A と IBCF-B
  |               |               |         |  ║   | INVITE、ダイアログ 4 の Replaces
  |               |               |         |  ║   |--------->|  ダイアログ 4: IBCF-B と UE B1
  |               |               |         |  ║   |<---------|  200 OK: UE B1 が新しい呼を受ける
  |               |               |         |  ║   |<---------|  ダイアログ 4 の BYE: 古い呼が終わる
```

ダイアログ 1 から 4 はどれも UE A1 と UE B1 の古い呼のもので、この呼は途中の UA と B2BUA の 2 つずつのあいだにダイアログを 1 つずつ持ちます。各網のプロキシは図から省いています。IBCF の呼を指さない Replaces は届いたまま渡します。TS 24.229 5.7.5.1 も、AS が自分のダイアログを指さない Replaces を届いたまま渡すとしています。IBCF の先の B2BUA がそのダイアログを持っているかもしれないためです。どのノードも持っていなければ、端の UA が 481（Call/Transaction Does Not Exist）を返します（RFC 3891 3）。IBCF が `Store` を読めないときは、どちらなのか分からないので、INVITE に 500（Server Internal Error）を返します。

スクリプトはこうして UE A1 の UE B1 との呼と UE A2 との呼をまとめます。見えるものは次のとおりです。

- **網 A のキャプチャ。** S-CSCF から IBCF-A へのフォーカスの INVITE があります。Contact に `isfocus` を持ち、Replaces の Call-ID は、UE A1 が UE B1 にかけた呼で S-CSCF が IBCF-A に送った INVITE のものです。ダイアログ 2 です。
- **網のあいだのキャプチャ。** IBCF-A から IBCF-B への INVITE があります。Replaces の Call-ID は、網のあいだでの同じ呼のものに替わっています。ダイアログ 3 です。
- **網 B のキャプチャ。** UE B1 に届く INVITE があります。Replaces の Call-ID は、UE B1 自身の呼のものです。ダイアログ 4 です。UE B1 はこれに 200 を返し、古い呼は BYE で終わります。

網 B の利用者は会議の URI に INVITE を送って会議に入ります（TS 24.147 5.3.1.4.1）。会議を抜けた参加者が戻るときも同じです。スクリプトでは、呼をまとめたあとで UE B1 が会議を抜け、会議の URI に発信して戻ります。スクリプトは会議の URI を UE A1 から読み取り（`ue ctl conference`）、UE B1 に渡します。この INVITE はどのダイアログにも属さない要求で、新しい呼です。ほかの呼と同じく、網 B の P-CSCF と S-CSCF、IBCF-B を通って網のあいだに出て、IBCF-A から網 A に入ります。IBCF-A はこれを網 A の I-CSCF に渡します。URI が公開サービス識別子のサブドメインにあるので、I-CSCF は HSS に何も尋ねず、`conf.ims.mnc001.mcc001.3gppnetwork.org` の NAPTR と SRV のレコードを引いて（RFC 3263）、そこに書かれた MMTel AS へ INVITE を送ります（TS 24.229 5.3.2.1 step 3）。フォーカスは自分の網の利用者と同じように UE B1 を迎え入れ（[会議に戻る](mrf.md#会議に戻る)）、`isfocus` を付けた会議の URI を Contact に入れて応えます。

```text
UE B1   P-CSCF-B  S-CSCF-B  IBCF-B ║ IBCF-A   I-CSCF-A   DNS    MMTel AS（フォーカス）
  | INVITE sip:conf-...@conf.ims.mnc001.mcc001.3gppnetwork.org     |
  |------->|-------->|------->|=========>|------->|        |        |
  |        |         |        |    ║     |        |------->|        |  NAPTR conf.ims.mnc001...
  |        |         |        |    ║     |        |<-------|        |  SRV _sip._udp.conf...: mmtel
  |        |         |        |    ║     |        |---------------->|  HSS への LIR は無い
  |        |         |        |    ║     |        |<----------------|  200 OK、Contact: <conf>;isfocus
  |<-------|<--------|<-------|<=========|<-------|        |        |  Record-Route は網のあいだで IBCF-A、網 B で IBCF-B
```

見えるものは次のとおりです。

- **網 B のキャプチャ。** UE B1 が会議の URI に送る INVITE と、S-CSCF から IBCF-B への INVITE があります。UE B1 に届く 200 は Contact に `isfocus` 付きの会議の URI を、Record-Route に IBCF-B を持ちます。UE A1 が会議を抜けると、フォーカスが BYE で UE B1 の呼を終えます。
- **網のあいだのキャプチャ。** IBCF-B から IBCF-A への INVITE と、IBCF-A の 200 があります。200 の Contact は網 B と同じ `isfocus` 付きの会議の URI です。Record-Route は IBCF-A の外側の名前の 1 つだけです。
- **網 A のキャプチャ。** IBCF-A から I-CSCF への INVITE、I-CSCF がサブドメインを引く NAPTR の問い合わせ、MMTel AS を示す SRV の答え、I-CSCF から AS への INVITE とその 200 があります。会議の URI について HSS に尋ねる LIR はありません。

IBCF は、その URI の中を何も隠しません。フォーカスが書いたものが、そのまま相手網に見えます。このプロジェクトの MMTel AS は、会議の URI をホームドメインのサブドメイン `conf.ims.mnc001.mcc001.3gppnetwork.org` に作り、ポート番号を付けません。このサブドメインはノードの名前ではないので、会議の URI が網 B に網 A のノードの名前を見せることはありません。スクリプトは網 B と網のあいだのキャプチャで `isfocus` 付きの Contact をすべて調べます。ノードの名前を含むものも、ポート番号を持つものもありません。

参加者は、まずダイアログの外で、会議の URI に SUBSCRIBE を送ります（TS 24.147 5.3.1.2）。これは境界を越えません。参加者の網の IBCF が、自分で 403（Forbidden）を返します。5.10.1 は、局所的な方針で要求を断ることを IBCF に認めています（[このプロジェクトで簡略化したこと](#このプロジェクトで簡略化したこと)）。403 を受けた参加者は、TS 24.147 5.3.1.2 のとおり、会議のセッションのダイアログの中で SUBSCRIBE を送り直します。こちらは、取り決めが SUBSCRIBE と NOTIFY を許していれば、呼のほかの要求と同じく境界を越えます。取り決めに SUBSCRIBE が無ければ、IBCF はどちらにも 405（Method Not Allowed）を返します。取り決めの外のほかのメソッドと同じです。境界を越えるフォーカスの INVITE は、`internal/ibcf` の試験が 1 つのプロセスの中で両方の向きに動かします（`TestConferenceFocusAcrossTheBorder`、`TestConferenceFocusAnswersAcrossTheBorder`）。`internal/mmtel` の `TestConferenceInvitesAUserOfAnotherNetwork` は、このプロジェクトのフォーカスと網 A の S-CSCF を 1 つのプロセスで動かし、網 B の利用者には IBCF-A の代わりに UA を置きます。UE A1 がその利用者との通話と網 A の利用者との通話を 1 つの会議にまとめ、3 人が互いの声を聞きます。`internal/ibcf` の `TestReplacesNamesTheDialogOfTheNextHop` は、Replaces を入れた INVITE を、呼を持つ台とは別の IBCF-A の台を通して、両方の IBCF に通します。`TestAReplacesOfNoCallCrossesAsItCame` は、どの呼も指さない Replaces を通します。`TestConferenceMergesAUserOfTheOtherNetwork` は、このプロジェクトの会議の AS を網 A のフォーカスにして、両方の網の IBCF を通して呼をまとめます。網 B の利用者は、網 A の UE との呼の代わりにフォーカスの呼を受けます。

IBCF は会議の状態も読みません。会議の状態は NOTIFY の本文で運ばれます。TS 24.229 5.10.6.3 は、メディアのアドレスを変換するときに SDP を書き換えるよう IBCF に求め、ほかの本文の扱いは局所的な方針に任せています。例に挙がっているのは、そのまま渡すこと、本文を取り除くこと、要求を断ることです。このプロジェクトの IBCF は SDP 以外の本文をそのまま渡します。そのため、網 B に何が見えるかはフォーカスが決めます。RFC 4575 5.7 は `<endpoint>` ごとのキーをフォーカスが作るとし、例として Contact と GRUU を挙げています。網 A のフォーカスが参加者の Contact として持っているのは、網 A の UE のアドレスです。網 B の参加者では、参加者の Contact の代わりに IBCF-A が入れた、IBCF-A の内部の名前です。そこで、このプロジェクトの MMTel AS は `<endpoint>` を Contact ではなく `urn:uuid:` の URI で名付けます。会議の状態には、網 A のアドレスもノードの名前も載りません。`internal/ibcf` の `TestConferenceStateAcrossTheBorder` が、このプロジェクトの会議の AS を網 A のフォーカスにして、これを確かめます。

## 相手網でない送信元からの要求

IBCF-A は、外側では、網 B のプロファイルに挙げたアドレス（`ibcf.yaml` の `sources`）からの要求だけを受けます。スクリプトは、網のあいだにあり、IBCF-B でも TrGW でもない 172.28.2.99 から、IBCF-A に INVITE と MESSAGE を送ります。IBCF-A は、どちらにも何も答えずに捨てます。100（Trying）も、誤りの応答も返しません。網の境界は、取り決めを結んでいない送り手には何も答えません。そこに SIP のノードがあることさえ、相手に知らせないためです。

```text
172.28.2.99 のホスト        IBCF-A ║ 網 A
  |  INVITE                    |   ║
  |--------------------------->| x ║   （捨てる。172.28.2.99 から送る相手網は無い）
  |  MESSAGE                   |   ║
  |--------------------------->| x ║
  |                            |   ║
  |  （何も返ってこない）      |   ║   （網 A には何も入らない）
```

IBCF-A の SIP のスタックは、手持ちのどのトランザクションにも属さない要求を受けると、何かをする前に、その要求を入れてよいかを IBCF に尋ねます（`IBCF.Screen`）。相手網でない送信元からの要求なら、答えは「いいえ」です。スタックはその要求を捨てます。トランザクションを作らず、応答を返さず、要求を先へ渡しません。CANCEL と ACK を含むどのメソッドでも同じです。再送も、そのたびに尋ねて捨てます。捨てた要求のことは何も覚えていないためです。一方、IBCF がすでに通したトランザクションに属する要求（相手網の INVITE の再送など）は、改めて尋ねません。

見えるものは次のとおりです。

- **網のあいだのキャプチャ。** 172.28.2.99 から IBCF-A への INVITE と MESSAGE があります。172.28.2.99 へ返るのは、IBCF-A の MAC アドレスを知らせる ARP の応答だけです。これは、網のあいだのどのホストにも返ります（`ip.dst == 172.28.2.99` では何も見つかりません）。**Statistics > Flow Graph** では、どちらの要求も答えの無い矢印として描かれます。
- **網 A のキャプチャ。** このホストのものは何もありません。From が `+12025550198` の要求はありません。
- **IBCF-A のログ。** 要求ごとに、`"msg":"discarded a request from an address of no peer network"` が、メソッド、送信元、Call-ID と一緒に出ます。続いて、SIP のスタックの `"msg":"dropped request that the screen refused"` が出ます。

相手網からの要求を境界が断るときは、応答を返します。取り決めの外のメソッドへの 405 などです。相手網は取り決めの内側にいるので、応答で何がいけなかったかを伝えます。TS 24.229 5.10.1 は、IBCF が局所的な方針で 4xx を返して要求を断ることを認めています。TS 24.229 には、P-CSCF にも似た決まりがあります（5.2.6.3.2A）。REGISTER 以外の要求で、UE の IP の対応付け（IP association）のどれにも当たらないものは、100（Trying）を返さずに捨てます。

## IBCF の台が起動し直したとき

呼の 2 つのダイアログは、その INVITE を受けた IBCF の台のプロセスの中にあります。そのプロセスが止まると、呼も止まります。しかし両側はそれを知りません。どちらも自分のダイアログを持ち続け、TrGW も呼のコンテキストを持ち続けます。TS 24.229 5.10.2.2 の手順 2 と 5.10.3.2 の手順 2 は、伝送の面を制御する IBCF に、呼の Contact、CSeq、Record-Route を覚えておくよう求めています。IBCF が自分でセッションを解放できるようにするためです（5.10.2.4、5.10.3.4）。

IBCF の各台は、こうした記録を自分の呼ごとに `Store` に置きます。置き場所は自分の名前の下です。記録には、TrGW のコンテキストと、呼が応答されたあとは 2 つのダイアログが入ります。呼の中の要求のたびに、ダイアログを書き直します。TS 24.229 5.10.2.3 と 5.10.3.3 が、後続の要求ごとに Contact と CSeq を覚えるよう求めているためです。re-INVITE や UPDATE で、相手が別の Contact に移ることがあります（RFC 3261 12.2）。同じ名前で起動し直した台は、最初に自分の記録を読みます。記録ごとに TrGW のコンテキストを解放し、2 つのダイアログのそれぞれで BYE を送ります。BYE の CSeq は、記録の値より十分に大きくします。記録を書いたあとも、ダイアログが進んでいたかもしれないためです（RFC 3261 12.2.2）。IBCF は、記録ごとに `"msg":"ending a call that an earlier process of this instance left"` をログに出します。

1 つの台の記録はその台の名前の下にあるので、各台は自分の記録だけを、起動したときに 1 回読みます。網の全部の呼を探すことはしません。応答された呼の記録は 2 つのダイアログそれぞれの下にも置きます。Replaces を入れた INVITE を受けた台は、Replaces が指すダイアログから、その 1 件の記録を読みます。呼を持つのがどの台でも同じです（「境界を越える会議」を見てください）。

記録が残るのは、記録を持つ `Store` が残るあいだだけです。ノードの設定に種類が `valkey` の `store` の節があると、ノードは IBCF に `ibcfstore.Store` を渡します。この `Store` は、記録をその Valkey のサーバーに置きます。記録は `ibcf:call:<台>:<ID>` に、台の呼の ID は整列集合（sorted set）の `ibcf:call-ids:<台>` に、ダイアログを持つ呼の台と ID は `ibcf:dialog:<Call-ID>:<自分の tag>:<相手の tag>` に置きます。どのキーも節の `prefix` のあとに続きます。`<台>` は、自網での IBCF の URI のホストです。呼が始まるときと終わるときは、整列集合に自分の ID を 1 つ足すか消すだけです。そのため、同じ台の呼どうしが 1 つの一覧を取り合うことはありません。作り直した IBCF のコンテナは、サーバーから整列集合を 1 ページずつ読んで、その ID ごとに記録を読み、止まったプロセスの呼を上のとおりに終わらせます。

新しいプロセスは、UA を動かす前に自分の記録を読みます。読めなければ、起動しません。記録を持たずに呼を受けた台は、前のプロセスの呼をいつまでも終わらせないためです。このときノードは `"msg":"node failed"` を、`ibcf: cannot read the calls that an earlier process of <台> left` で始まるエラーと一緒にログに出し、止まります。保存先が答えるようになってから、もう一度起動してください。IBCF の台がいくつもある網では、そのあいだ、ほかの台が新しい呼を受けます。

`store` の節の無いノードは、記録をプロセスのメモリの中に置きます（`ibcf.MemoryStore`）。記録はプロセスと一緒に消えます。この手順書の網の IBCF のノードには `store` の節が無いので、作り直した IBCF のコンテナは空の `Store` で起動し、何も読まず、何も終わらせません。通話中に IBCF-A を作り直すと、次のようになります。

- IBCF-A は `"msg":"ending a call that an earlier process of this instance left"` をログに出しません。
- その呼の中で当事者が送る要求、たとえば UE A1 が切るときの BYE には、新しいプロセスが 481（Call/Transaction Does Not Exist）を返します。新しいプロセスは、そのダイアログを知らないためです（RFC 3261 12.2.2）。IBCF-A の向こう側の当事者には、BYE が届きません。
- TrGW-A は、その呼のコンテキストを持ち続けます。解放するものがいません。

解放そのものは、`internal/ibcf` の試験が見せます。`TestRestartedInstanceEndsTheCallsItLeft` では、台の最初のプロセスが境界を越える呼を持ち、呼を終えずに止まります。次のプロセスは、2 つのダイアログの両方で BYE を送り、TrGW のコンテキストを解放します。この試験は 2 通りに動きます。1 つは、何代かのプロセスがプログラムの中の `Store` を共有する形です。もう 1 つは、Valkey のサーバーの代わりの共有の保存先の上で、プロセスごとに自分の `ibcfstore.Store` を作る形です。`TestRestartedInstanceEndsACallAtItsNewTarget` は、re-INVITE で当事者が移った先の Contact に BYE が届くことを見せます。

## 境界のトラヒックの統計

IBCF の各台は、通したものと断ったものを、相手網ごとに数えます。事業者が見るための統計です。TS 32.409 4.11 は、IBCF が数える測定として、他の網のドメインからのセッションとそこへのセッション、それらへの 403（Forbidden）を挙げています。次のカウンタは、それを相手網ごとに分け、ほかの断り方も数えます。

| カウンタ | ラベル | 数えるもの |
| --- | --- | --- |
| `ims_ibcf_session_attempts_total` | `peer`、`direction` | IBCF が境界の向こうへ送った最初の INVITE（TS 32.409 4.11.1、4.11.3） |
| `ims_ibcf_session_failures_total` | `peer`、`direction`、`status` | それに境界の向こうから戻った、2xx 以外の最終応答。4.11.4 の 403 もここに入る |
| `ims_ibcf_requests_rejected_total` | `peer`、`direction`、`method`、`status` | IBCF が先へ送らず、自分で答えた要求。プロファイルによる拒否（405、420、488、404）、ダイアログの外の SUBSCRIBE への 403、相手が答えないときの 504、どの相手網にも向かわない要求への 404（相手網は `other`） |
| `ims_ibcf_requests_discarded_total` | `reason`、`method` | 何も答えずに捨てた要求。理由は、相手網でないアドレスからの要求なら `unknown_source` です。IBCF の SIP のスタックが、要求を入れてよいかを IBCF に尋ねたとき（`IBCF.Screen`）に数えます。応答が無いので、1 つ上の行では数えません。 |

`direction` は、自網から出ていく要求なら `exit`、入ってくる要求なら `entry` です。どのラベルも、値の集合が決まっています。設定した相手網の名前、TS 29.165 Table 6.1 のメソッド、IBCF が返す応答コードです。それ以外の値は `other` で数えます。IBCF は、渡された `internal/metrics` のレジストリにカウンタを登録します。レジストリは Prometheus のテキストの形式で書き出します。Compose が動かす IBCF のノードは、そのレジストリを外に出しません。数はプロセスの中にとどまり、この手順書の網では見えません。`internal/ibcf` の試験は、レジストリから値を読んでいます。

## 信頼ドメイン

2 つの事業者には取り決めがあるので、互いに相手の信頼ドメインに入ります（プロファイルの `trusted: true`、TS 24.229 4.4.1）。P-Asserted-Identity は境界を越え、UE B1 は発信者が誰かを知ります（4.4.2）。P-Access-Network-Info と P-Charging-Vector も越えます。P-Charging-Function-Addresses は決して越えません（5.10.2.2 の手順 9）。

`trusted: false` にすると、IBCF は TS 24.229 4.4 のヘッダを境界で消します。UE B1 には、発信者の番号の無い着信が届きます。P-Charging-Vector も消え（5.10.3.2）、着信した網の I-CSCF が新しい icid-value を付けます（5.3.2.1）。スクリプトのシナリオでは使っていません。プロファイルを書き換えて試してください。

## プロファイル

網 A の網 B との取り決めは `deploy/interconnect/profiles/network-b.yaml`、網 B の網 A との取り決めは `network-a.yaml` です。TS 29.165 の Annex C は、こうした選択を、2 つの事業者が書き込む選択項目（option item）として並べています。プロファイルの項目は、このプロジェクトの IBCF が使うものだけです。

| 項目 | 決めること | 参照 |
| --- | --- | --- |
| `name`、`domain` | 相手網と、SIP URI にして送る番号のホスト | TS 29.165 8.1 |
| `trusted` | 相手が信頼ドメインに入るか | TS 24.229 4.4 |
| `transports` | 送るときのトランスポート。先に書いたものを優先する | TS 29.165 6.2 |
| `methods` | 取り決めで使う任意のメソッド（MESSAGE など） | TS 29.165 Table 6.1 |
| `option_tags` | option tag ごとの `pass`、`remove`、`reject`。`remove` は要求から tag を外し、`reject` では、その機能を必須にした要求に IBCF が 420 を返す | TS 29.165 Table 6.1.3.1（100rel は項目 18、precondition は項目 20）、RFC 3261 8.2.2.3 |
| `headers` | ヘッダを消したり書き換えたりする事業者の規則 | TS 24.229 5.10.6.2 |
| `number_format`、`local_numbers`、`allow_local_numbers` | 番号の形式と、国内形式の番号を国際形式にする方法 | TS 29.165 8.1、RFC 3966 5.1.5 |
| `codecs` | 相手網が受けるペイロードの形式 | TS 29.165 7.1 |

`IBCF_A_PROFILE` と `IBCF_B_PROFILE` で、IBCF に別のファイルを選べます。Compose はその IBCF を新しいプロファイルで起動し直します。その IBCF を通る通話中の呼は失われます。呼のダイアログは止まったプロセスの中にあり、新しいプロセスにはその記録が無いためです（「IBCF の台が起動し直したとき」の節）。プロファイルは呼のあいだに変えてください。

`profiles/example-ttc-jj-90.30.yaml` は、TTC JJ-90.30 にならった、日本の網間接続の形の例です。どのシナリオも使いません。示しているのはプロファイルの形で、標準の値ではありません（[TS 29.165、GSMA IR.95、TTC との対応](#ts-29165gsma-ir95ttc-との対応)）。

## プロファイルが食い違うとき

次のシナリオは、食い違った取り決めで呼が失敗する様子と、取り決めを直して通る様子を見せます。スクリプトは次の 3 つを行います。コマンドの前に `C="docker compose -f compose.yaml -f deploy/interconnect/compose.yaml"` としておきます。UE B1 には、「動かす」の節のように着信を待たせるか、ショートメッセージのために登録したままにしておきます。

| シナリオ | 食い違ったプロファイル | 起きること | 直したプロファイル |
| --- | --- | --- | --- |
| MESSAGE | `IBCF_A_PROFILE=network-b-no-message.yaml $C up -d ibcf-a` | MESSAGE には取り決めが要ります（TS 29.165 Table 6.1）。IBCF-A は、網 B へのショートメッセージに 405（Method Not Allowed）を返します。網 A の SC は TP-ST 98（SME から応答が無い）を報告します。 | `network-b.yaml`。TP-ST 1 になる |
| コーデック | `IBCF_B_PROFILE=network-a-amr-wb-only.yaml $C up -d ibcf-b` | 網 B は AMR-WB だけを受け、UE A1 は PCMU を offer します。IBCF-B は offer から音声のコーデックを全部外し、488（Not Acceptable Here）を返します（TS 29.165 7.1）。 | PCMU を挙げた `network-a.yaml` |
| 番号 | `IBCF_A_PROFILE=network-b-local-numbers.yaml $C up -d ibcf-a` のあと `$C run --rm ue call "sip:2025550151;phone-context=+1@ims.mnc002.mcc001.3gppnetwork.org;user=phone"` | IBCF-A は国内形式の番号をそのまま通します。網 B のプロファイルは国際形式の番号しか認めないので（TS 29.165 8.1）、IBCF-B は局所的な方針で INVITE に 404（Not Found）を返します（TS 24.229 5.10.1）。 | `network-b-global-numbers.yaml`。IBCF-A が送る前に `+12025550151` に直す |

失敗させているのは、取り決めに従った IBCF です。UE ではありません。網のあいだの INVITE と網 B に届く INVITE を比べたり、405 や 488 を送った IBCF のキャプチャを見たりしてください。

プリコンディションのしくみも、II-NNI では任意の機能です（TS 29.165 Table 6.1.3.1 の項目 20）。使うかどうかは事業者が取り決めます（Table C.3.1.2）。ただし、呼がこのしくみを使うかどうかを決めるのは、境界ではなく呼の両端です。Supported に `precondition` を挙げるだけの INVITE には、このしくみを持たない着信者が、プリコンディションの無い answer を返します（RFC 3312 11）。このしくみを必須にした INVITE には、その着信者が 420 を返します（RFC 3261 8.2.2.3）。プロファイルに `precondition: reject` と書くと、発信側のダイアログの UAS として、IBCF が自分でその 420 を返します。プロファイルで precondition を `remove` にはできません。tag だけが消え、SDP にはプリコンディションの属性が残るためです。

## 網をまたぐショートメッセージ

```text
UE A1   S-CSCF-A  IP-SM-GW-A+SC  IBCF-A ║ IBCF-B  I-CSCF-B  S-CSCF-B  IP-SM-GW-B   UE B1
  |MESSAGE |          |             |    ║   |        |         |          |          |
  |------->|--------->| (1)         |    ║   |        |         |          |          |
  |  202、submit report（RP-ACK）   |    ║   |        |         |          |          |
  |<-------|<---------| ENUM (2)    |    ║   |        |         |          |          |
  |        |          | MESSAGE (3) |    ║   |        |         |          |          |
  |        |          |------------>|===========>|--->|-------->| (4)      |          |
  |        |          |             |    ║   |        |         |--------->| 202      |
  |        |          |<------------|<===========|<---|<--------|<---------|          |
  |  MESSAGE status report。TP-ST 1 (5) ║   |        |         | MESSAGE SMS-DELIVER (6)
  |<-------|<---------|             |    ║   |        |<--------|<---------|          |
  |        |          |             |    ║   |        |-------->|--------------------->|
  |        |          |             |    ║   |        | delivery report は IP-SM-GW-B へ (7)
```

1. UE A1 は、SMS の手順書と同じようにショートメッセージを SC に送ります。
2. SC のスタブは、網 A の HSS に Sh で番号を問い合わせ、「利用者がいない」という答えを受けます。設定 `ibcf` がある（`deploy/interconnect/a/ipsmgw.yaml`）ので、続けて ENUM を引き、番号が網 B のドメインにあると分かります。SC はメッセージを受け付け、UE A1 には submit report が届きます。
3. IP-SM-GW は、`sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone` 宛ての MESSAGE に SMS-DELIVER を入れ、IBCF-A を Route に入れて送ります。送信者は番号で表した SC（`tel:+12025550100`）です。網 A のホスト名を境界の外に出さないためです。プロファイルは MESSAGE を認めています（TS 29.165 Table 6.1.3.1 の項目 29）。
4. 網 B では、I-CSCF が Request-URI を tel URI に直し、S-CSCF が着信側のフィルタ条件に当てます。ショートメッセージを運ぶ MESSAGE で、P-Asserted-Identity が網 B 自身の IP-SM-GW でないものは、その IP-SM-GW に送る条件です（`deploy/interconnect/b/hss.yaml`）。TS 23.204 6.4a の手順 2 は、別の IMS 網からのショートメッセージが、利用者を担当する IP-SM-GW に届くとしています。
5. 網 B の IP-SM-GW は 202 を返します。delivery report は境界を越えて戻ってこないので、網 A の SC の仕事はここで終わります。status report は TP-ST 1 です。「SC は SME に転送したが、届いたかは確かめられない」という意味です（TS 23.040 9.2.3.15）。
6. 網 B の IP-SM-GW は、自分の MESSAGE で UE B1 に SMS-DELIVER を届けます。RP-OA には網 A の SC が残ります。UE B1 は `"from":"+12025550101"` を付けて `"msg":"sms received"` をログに出します。
7. UE B1 の delivery report は網 B の IP-SM-GW に届き、そこで配送が終わります。

### 逆の向き（網 B から網 A へ）

UE B1 から UE A1 へのショートメッセージは、網を入れ替えた同じ手順で届きます。

1. 網 B の SC は、自網の HSS で `+12025550101` の利用者を見つけられません。網 B の ENUM は `sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org;user=phone` を返します。網 B の IP-SM-GW にも設定 `ibcf` がある（`deploy/interconnect/b/ipsmgw.yaml`）ので、IP-SM-GW は SC（`tel:+12025550150`）を送信者にして、MESSAGE を IBCF-B に送ります。
2. IBCF-B は、P-Asserted-Identity の番号を自網のドメインの SIP URI `sip:+12025550150@ims.mnc002.mcc001.3gppnetwork.org;user=phone` に書き直します。取り決めでは、番号は SIP URI で境界を越えるからです（TS 29.165 8.1）。IBCF-A は MESSAGE を網 A の I-CSCF に渡します。I-CSCF は Request-URI を `tel:+12025550101` に直し、利用者の居場所を HSS に問い合わせます（LIR）。
3. 網 A の S-CSCF は、`deploy/compose/hss.yaml` の優先度 2 の条件に当て、MESSAGE を網 A の IP-SM-GW に送ります。網 B と同じ条件です。P-Served-User には `sescase=term` が付きます。IP-SM-GW は 202 を返し、202 は網 B まで戻ります。UE B1 への status report は TP-ST 1 です。
4. 網 A の IP-SM-GW は、自分の MESSAGE で UE A1 に SMS-DELIVER を届けます。RP-OA には網 B の SC（`12025550150`）が残ります。UE A1 は `"from":"+12025550151"` を付けて `"msg":"sms received"` をログに出し、delivery report を網 A の IP-SM-GW に送ります。

網 A の IP-SM-GW は、この MESSAGE を、ほかのショートメッセージと同じように I-CSCF と S-CSCF を通して UE A1 に送ります。その P-Asserted-Identity は IP-SM-GW 自身の URI です。条件にはこれを否定する項目があるので、S-CSCF は MESSAGE を IP-SM-GW に戻しません。この項目が無いと、利用者へのショートメッセージがすべて S-CSCF と IP-SM-GW のあいだを回り続けます。TS 23.218 5.2.3 NOTE 5 は、フィルタ条件のこうしたループに気をつけるよう求めています。網 A の中のショートメッセージは、今までどおり SMS の手順書のとおりに届きます。

このプロジェクトが網のあいだのショートメッセージをこの形で運ぶのは、IBCF を通る道がこれだけで、網のあいだのキャプチャにメッセージが写るためです。MSISDN を持つ利用者の標準の道では、SC から SMS-GMSC に渡り、SMS-GMSC が網 B の HSS に問い合わせ、MAP か SGd で網 B の IP-SM-GW に渡します（TS 23.204 6.4）。このプロジェクトにはどちらもありません。そこで、TS 23.204 6.3a と 6.4a が MSISDN の無い利用者のために IMS 網のあいだで運ぶ方法にならい、SIP で II-NNI を通します。TS 24.341 5.3.3.5.2 が足す multipart の本文は使いません。

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Method == "INVITE" && count(sip.Via) == 1` | 網のあいだで、IBCF が送った INVITE。どれも自分の Via だけを持つ。 |
| `sip.Call-ID == "<Call-ID>"` | 1 つのダイアログ。網のあいだの Call-ID は、どちらの網のキャプチャにも現れない。 |
| `sip.Route contains "ibcf"` | IBCF へ向かう要求と、IBCF から出た要求。 |
| `sip.r-uri contains "user=phone"` | SIP URI に番号を入れた要求。S-CSCF から、相手網の I-CSCF までに現れる。 |
| `sip.r-uri == "tel:+12025550151"` | 網 B の I-CSCF が tel URI に直したあとの INVITE。 |
| `sdp.connection_info.address == 172.28.2.11` | 網のあいだの TrGW-A のアドレスを持つ SDP。 |
| `rtp && ip.addr == 172.28.2.0/24` | TrGW どうしの RTP（`rtp.heuristic_rtp` を有効にする）。 |
| `dns.qry.name contains "5.1.0.5.5.5.2.0.2.1.e164.arpa"` | 網 B の番号の ENUM。 |
| `sip.Replaces` | UE B1 の呼を引き継ぐフォーカスの INVITE。Replaces の Call-ID は、キャプチャごとに、その側での UE B1 の呼のもの。 |
| `sip.Status-Code in {404, 405, 420, 488}` | シナリオの拒否の応答。 |
| `gsm_sms.sms_text` | デコードしたショートメッセージ。網のあいだでも見える。 |
| `sip.Method == "MESSAGE" && sip.P-Served-User contains "sescase=term"` | 相手網からのショートメッセージ。S-CSCF から IP-SM-GW へ向かうところ。 |
| `ip.addr == 172.28.0.0/24 \|\| frame contains "172.28.0."` | 網 B と網のあいだのキャプチャで、網 A の中のもの。何も見つからないのが正しい。 |
| `ip.addr == 172.28.2.99` | 網のあいだで、相手網でないホストの要求。答えは 1 つも無い。 |

網のあいだのキャプチャで **Statistics > Flow Graph** を開くと、2 つの IBCF のあいだのラダー図が描けます。

## TS 29.165、GSMA IR.95、TTC との対応

| TS 29.165 | このプロジェクト |
| --- | --- |
| 5.1、Ici と Izi | 2 つの IBCF と 2 つの TrGW がつながる `nni0` |
| 6.1.1.2、Table 6.1（メソッド） | プロファイルの `methods`。必須のメソッドには取り決めが要らない |
| 6.1.3、Table 6.1.3.1（機能） | `option_tags`、`trusted`、`headers` |
| 6.2（トランスポート） | `transports`。1300 バイトを超えるメッセージは、それでも TCP で送る |
| 7.1（コーデック） | `codecs`。IBCF はコーデックを外すだけで、変換はしない |
| 8.1（番号） | `number_format` と `local_numbers`。I-CSCF の tel URI への置き換え |
| Annex C（選択項目） | プロファイルのファイル全体 |

GSMA IR.95 は、IMS 網のあいだの SIP と SDP の GSMA のプロファイルです。IR.95 が TS 29.165 をどう絞っているかは、この手順書では扱いません。

日本の IMS 事業者網どうしの相互接続の標準は TTC JJ-90.30 です。`profiles/example-ttc-jj-90.30.yaml` は、こうした国の標準が TS 29.165 の上に加える取り決めの例です。たとえば、SIP は UDP だけにし、どの offer にも G.711 を入れます。示しているのはファイルの形で、標準の中身ではありません。値は標準の本文で確かめてください。国の標準が決めることのうち 2 種類は、このプロジェクトのプロファイルでは書けません。すべてのセッションにセッションタイマーを掛けることと、P-Asserted-Identity の tel URI の数を限ることです。JJ-90.31（キャリア ENUM）と JJ-90.32（事業者のあいだの DNS）は、ここで各網の DNS が行っていることについての日本の標準です。

## コードのある場所

| パッケージかファイル | 中身 |
| --- | --- |
| `internal/interconnect` | プロファイルと、IBCF が各メッセージに行うこと。`screen.go`（メソッド、option tag、ヘッダの規則）、`trust.go`（信頼ドメイン、プライバシー）、`number.go`（番号）、`codec.go`（コーデック）、`leg.go`（一方のダイアログから他方へ写すヘッダ）、`border.go`（TS 24.229 5.10.2 と 5.10.3 の順番）。 |
| `internal/b2bua` | 呼の 2 つのダイアログのあいだの中継。応答と PRACK、2xx とその ACK、ダイアログの中の要求、フォークしたときの early dialog、呼の終わり。何を渡すかは IBCF が決める（`Hooks`）。 |
| `internal/ibcf` | 役割 IBCF。2 つの UA、出口と入口（`ibcf.go`）、呼の INVITE と、2 つのダイアログのあいだで渡すもの（`call.go`）、ショートメッセージ（`message.go`）、TrGW（`trgw.go`、`media.go`）。試験では 2 つの網を 1 つのプロセスの中で動かす。 |
| `internal/scscf/session.go` | `exitIBCF`。別の網への要求を IBCF に送る。 |
| `internal/icscf/session.go` | `telURIOf`。SIP URI の番号を tel URI に直す。 |
| `internal/sc/sc.go`、`internal/ipsmgw/deliver.go`、`internal/ipsmgw/message.go` | 網をまたぐショートメッセージ。SC の ENUM、IBCF を通る MESSAGE、別の網からの MESSAGE。 |
| `internal/config/ibcf.go` | 役割 ibcf の設定とプロファイルのファイル。 |
| `deploy/interconnect/` | Compose の上書きのファイル、2 つの網の設定、プロファイル。 |
| `scripts/e2e-interconnect.sh` | この手順書のシナリオと確認。 |

## このプロジェクトで簡略化したこと

それぞれ、コードに `SIMPLIFIED:` のコメントがあります。`grep -rn "SIMPLIFIED:" internal/ibcf internal/b2bua internal/interconnect internal/sc internal/ipsmgw` で、SMS の手順書のものと一緒に一覧できます。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| IBCF、TS 24.229 5.10.2.4、5.10.3.4 | IBCF が自分から呼を終えるのは、2 つのダイアログの一方が失われたときと、同じ台の前のプロセスの代わりに起動したときだけです。TrGW からは何も知らされないので、TrGW のコンテキストを失った呼は、どちらかが切るまで音声の無いまま続きます。二度と起動しない台の記録は、TrGW のコンテキストと一緒に `Store` に残ります。 | このプロジェクトの TrGW は異常を IBCF に知らせず、このプロジェクトの台は同じ名前で起動し直すため。 |
| IBCF、TS 24.229 5.10.5 | 3xx の Contact は境界を越えません。相手網からの転送の応答は、転送先を持たずに発信者に届きます。 | 3xx の Contact は次に試す宛先で、ダイアログのものではないため。このプロジェクトのノードは、どれも 3xx を返しません。 |
| IBCF、RFC 3891 3 | early dialog を指す Replaces は、届いたまま渡します。そのため、向こう端の UA はそのダイアログを見つけられません。 | IBCF が呼の記録にダイアログを書くのは、2 つとも確定してからです。このプロジェクトには、early dialog を置き換えるノードがありません。 |
| IBCF、RFC 3515 2.1 | Replaces を入れた Refer-To は、届いたまま渡します。相手網の利用者を相手網の別の利用者へ転送すると、その Replaces を入れた INVITE を受けた利用者は、自分のダイアログを見つけられません。 | このプロジェクトには呼の転送がありません（TS 24.629）。呼を会議にまとめる UE は自分の網のフォーカスに REFER を送ります。 |
| IBCF、TS 24.229 5.10.2.2、5.10.3.2 | ダイアログの外の要求のうち、境界を越えるのは INVITE と MESSAGE だけです。OPTIONS には IBCF が自分で答え、SUBSCRIBE には 403 を、PUBLISH には 405 を返します。相手網の会議の参加者は、TS 24.147 5.3.1.2 が 403 のあとに求めるとおり、会議のセッションのダイアログの中で会議を購読します。MESSAGE への暫定応答は、その側にとどまります。 | ダイアログの外の SUBSCRIBE は、それ自身のダイアログを作ります。IBCF は、呼のダイアログと同じく、それを両側で持たなければなりません。B2BUA のその部分は作っていません。 |
| IBCF、TS 29.238、TS 29.165 3.2 | IBCF は、Ix の H.248 ではなく、メディア中継ノードの HTTP の制御 API で TrGW を動かします。 | P-CSCF が同じ API で動かしている中継ノードを使うため。H.248 のメッセージは `internal/h248` で見られます。 |
| IBCF、TS 24.229 5.10.2.1、5.10.3.1、TS 29.165 5.3.1 | non-roaming の II-NNI だけです。REGISTER は通しません（405）。Path、復旧の手順、`iotl` はありません。 | ローミングはこのプロジェクトの範囲の外のため。 |
| IBCF、TS 24.229 5.10.2.2、5.10.3.2 | 課金のデータ（P-Charging-Function-Addresses、fe-identifier）、私設網の通信、緊急呼、発信者番号の検証を扱わず、IBCF からセッションタイマーも求めません。 | このプロジェクトのノードは課金の記録を作らず、ほかのものもこのプロジェクトに無いため。 |
| IBCF、TS 24.229 6.7.1.4.1 | IPv4 だけです。IPv4 と IPv6 の変換と `altc` はありません。 | 2 つの網が IPv4 だけのため。 |
| IBCF、TS 24.229 6.7.1.4.1、RFC 3264 6 | TrGW に通すのは最初の音声のストリームだけです。 | UE は音声のストリームを 1 つだけ offer するため。 |
| IBCF、TS 29.238 Table 5.17.2.1.1 | TrGW のコンテキストの解放に失敗しても、やり直しません。1 台の IBCF が使う TrGW は 1 台です。 | 残ったコンテキストが使うのは TrGW のポートだけで、相互接続を見せるには網ごとに 1 台の TrGW で足りるため。 |
| IBCF、TS 24.229 Annex I、RFC 4694 | ホストの無い Request-URI（tel URI や `rn` 付きのもの）からは相手網を選べません。 | S-CSCF が ENUM の SIP URI を送り、番号の範囲の表は無いため。 |
| 境界、TS 29.165 7.1 | コーデックの変換をしません。応答の SDP に相手網が受けるコーデックが残らなくても、そのまま送ります。 | 変換には TrGW にコーデックが要り、このプロジェクトのノードは AMR の符号化も復号もしないため。 |
| 境界、TS 29.165 6.1.4 | multipart の本文の中の SDP は絞りません。 | このプロジェクトのノードは、それを境界の外に送らないため。 |
| 境界、TS 26.114 6.2.5.2 | 残ったコーデックに合わせて `b=AS` を計算し直しません。 | いちばん広いコーデックの値で足りるため。 |
| 境界、TS 24.229 5.10.8 | Privacy の無い要求を、Privacy を求めたものとして扱う局所的な方針はありません。 | シナリオに要らないため。 |
| SC、TS 23.204 6.4 の手順 3 | 別の網の利用者を、SMS-GMSC を通して相手網の HSS に問い合わせず、ENUM で探します。 | このプロジェクトに SMS-GMSC、MAP、S6c が無いため。 |
| IP-SM-GW、TS 23.204 6.4 の手順 2 から 4 | ショートメッセージは、RP-DATA だけを入れた SIP の MESSAGE で境界を越えます。MAP や SGd は使わず、TS 24.341 5.3.3.5.2 の multipart の形にもしません。 | IBCF を通る道はこれだけで、ショートメッセージが境界を越える様子がキャプチャに写るため。 |
| IP-SM-GW、TS 24.341 5.3.3.5.4、5.3.3.5.6、TS 23.204 6.5 の手順 6 | delivery report は境界を越えて戻りません。送る側は、相手網が MESSAGE を受け付けた時点で配送を終えます。受ける側は、自網の SC にだけ報告します。 | 2 つの IP-SM-GW が今あることだけで動くようにするため。 |

## 後片付け

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml --profile ue --profile phones --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。

`scripts/e2e-interconnect.sh` はプロジェクト名 `ims-practice-interconnect` で動き、`E2E_KEEP=1` を付けないかぎり、終わるとコンテナを自分で片付けます。
