# MRF による音声ガイダンス、呼び出し音、会議

[English](../en/mrf.md)

この手順書では、網が自分で流したり混ぜたりするメディアを追います。規制した通話を断る前の音声ガイダンス、着信側の利用者が発信者のために選ぶ呼び出し音（CAT、customized alerting tone）、通話中の利用者への 2 つ目の着信（割込通話、CW）、3 人の会議の 4 つです。何を流すかは MMTel AS が決め、制御の部分（MRFC）とメディアの処理の部分（MRFP）からなる MRF が流します。どの手順もキャプチャで確かめられます。

この手順書は次の版に従います。TS 23.228 V20.1.0、TS 24.229 V20.1.0、TS 24.182 V19.0.0（CAT）、TS 24.628 V19.0.0（音声ガイダンスと early media）、TS 24.611 V19.0.0（規制）、TS 24.615 V19.0.0（CW）、TS 24.147 V19.0.0（会議）、TS 24.605 V19.0.0（CONF のサービス）、TS 24.607 V19.1.0（OIR）、TS 24.608 V19.0.0（TIR）、TS 23.333 V19.0.0（MRFP）、RFC 4240（netann）、RFC 4575（会議イベントパッケージ）、GSMA IR.92 v24.0 です。以下の条項番号はこれらの版のものです。

## 始める前に

- [MMTel AS の付加サービス](mmtel.md) を読んでおいてください。この手順書は、そこで説明した内容を前提にします。1 つの通話に 3 つのダイアログを持つ B2BUA としての AS、利用者が Ut で設定するサービス、P-Served-User です。
- ここでの通話が使う高信頼の暫定応答（PRACK）とプリコンディションは、[音声通話の流れを追う](call.md) にあります。
- 略語の意味は [用語集](../../glossary.md) にあります。

## MRF とは

MRF は、IMS の中で自分からメディアを作るノードです（TS 23.228 4.7）。音声ガイダンス、トーン、会議の混ぜ合わせた音声を作ります。MRF は 2 つに分かれます。

- **MRFC** は SIP を話します。AS が INVITE でサービスを頼むと、MRFC はそれに要るメディアの資源を決めます。
- **MRFP** は RTP を扱います。ターミネーションに音声ガイダンスを流したり、複数のターミネーションの音声を足し合わせて、それぞれに混ぜ合わせた音声を返したりします（TS 23.333 6.2.9）。

この 2 つと AS は、3 つの参照点でつながります（TS 23.218）。

| 参照点 | どこの間か | このプロジェクトでは |
| --- | --- | --- |
| Mr' | AS と MRFC | SIP。MMTel AS から MRFC に直接送ります。S-CSCF からの Mr は使いません。 |
| Mp | MRFC と MRFP | 標準では H.248 です（TS 29.333）。ここでは 8080 番ポートの小さな HTTP API で、H.248 のコマンドごとに操作が 1 つあります。ターミネーションの追加、変更（モード、相手のアドレス、再生）、削除と、再生が終わったことを知らせる Notify です。 |
| Cr | AS と MRFC | 流す音声の取得と、メディア制御の命令。作っていません。MRFP は WAV ファイルとして持っている音声だけを流します。 |

MRFP は、P-CSCF のメディアゲートウェイ（agw ノード）と同じ中継の実装に、音声ガイダンスと混ぜ合わせの機能を加えたものです。

## RFC 4240 で AS が MRF に頼む

AS は、MRFC への INVITE の Request-URI の user part でサービスを指定します。形は RFC 4240 が決めています。

| Request-URI | サービス |
| --- | --- |
| `sip:annc@mrfc.ims.mnc001.mcc001.3gppnetwork.org:5060;play=/provisioned/notice` | 音声ガイダンス `notice` を 1 回流します（RFC 4240 3）。 |
| `sip:annc@...;play=/provisioned/cat1;repeat=forever` | AS がダイアログを終えるまで `cat1` を流し続けます。 |
| `sip:conf=conf-pcn6hpat5alr@mrfc...` | 会議 `conf-pcn6hpat5alr` に加わります。最初の INVITE がこの会議を作ります（RFC 4240 5）。 |

音声ガイダンスを頼む INVITE には SDP を付けません。そのため MRFC は、MRFP のターミネーションの offer を 200 に入れて返し、AS は answer を ACK に入れます。MRFP は ACK を受けてから流し始め（TS 24.229 5.8.2.2.1）、終わりのある再生が済むと MRFC が BYE でダイアログを終えます（5.8.3.2.1）。こうして AS は、発信者が受け取れるようになってから再生を始められます。

通話に応答する前に発信者へ音声を届けるため、AS は TS 24.628 Annex G のゲートウェイのモデルを使います。AS は発信者の offer に MRFP の SDP で自分から答え、`P-Early-Media: sendonly` で early media を認めます（RFC 5009）。発信者の UE は、自分の呼び出し音の代わりに届いた音声を流します（TS 24.628 4.7.2.1）。

## 登場するノード

| ノード | アドレス | ここでの役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 音声ガイダンスと CAT の発信者。`tel:+12025550101`。 |
| UE 2 | 172.28.0.101 | CAT を持つ着信者。`tel:+12025550102`。 |
| phone1、phone2、phone3 | 172.28.0.110、.111、.112 | `phones` プロファイルの UE シミュレータ。加入者 1、2、3 で、ラボから操作します。2 つ目の着信と会議に使います。 |
| P-CSCF と agw | 172.28.0.40、172.28.0.50 | ほかの通話と同じです。UE と MRFP の間の RTP は agw を通ります。 |
| MMTel AS | 172.28.0.75 | 何を流すかを決めます。会議ではフォーカスになります。 |
| MRFC | 172.28.0.85 | 役割 `mrfc`。`mrfc.ims.mnc001.mcc001.3gppnetwork.org`。 |
| MRFP | 172.28.0.86 | 役割 `mrfp`。制御の API は 8080 番ポート、RTP は 40100 番から 40199 番のポートです。 |

`deploy/compose/mmtel.yaml` には、MRFC（`mrfc:`）と、通話を断る前に流す音声ガイダンス（`announcement: notice`）を書いてあります。`deploy/compose/mrfp.yaml` は、`notice`、`cat1`、`cat2` の名前を `deploy/announcements` の WAV ファイルに対応させます。これらのファイルは `go generate ./deploy/announcements` で作ります。`notice` は上がっていく 3 つの音、CAT 用の 2 つは短い旋律で、どれも 8 kHz、16 ビット、モノラルです。

## 動かしてみる

`scripts/e2e-mrf.sh` は、以下のすべてを実行し、キャプチャで確かめます。キャプチャは `captures/` に、通話と MRF のフレームは `e2e-out/mrf-ladder.txt` に、CAT のあいだに UE 1 が受け取った音声は `captures/mrf-cat-received.raw` に、UE のログは `e2e-out/` に残ります。ビルドのあと 1 分ほどかかります。

手で動かすときは、ラボを使います（[ラボの手順書](lab.md) の「UE シミュレータを操作する」を参照）。

```sh
docker compose up -d --build
docker compose --profile phones up -d phone1 phone2 phone3
```

加入者 2 に呼び出し音 `cat1` を設定します。設定するのは事業者で、OAM から行います。OAM のポートはホストのどのアドレスにもないので、要求は OAM のコンテナの中から送ります。BusyBox の `wget` は `PUT` を送れないので、要求を書き下して `nc` に渡します。

```sh
docker compose exec oam sh -c '
body="{\"tone\":\"cat1\",\"active\":true}"
printf "PUT /oam/v1/subscriptions/001010000000002@ims.mnc001.mcc001.3gppnetwork.org/customized-alerting-tone HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8080'
```

そのあと http://localhost:8080 を開き、`phone1` から `phone2` に発信します。2 つ目の着信と会議は、ラボの手順書にある、2 つ目の着信を受けて会議を作る手順のとおりに進めます。通話のシーケンス図には、通話のダイアログと並んで、AS と MRFC のダイアログも描かれます。どちらも P-Charging-Vector の icid-value が同じだからです。

## キャプチャを開く

`captures/` の最新のファイルを Wireshark で開き、次の表示フィルタを使います。

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (sip.Event contains "conference") || (http && ip.addr == 172.28.0.86)
```

**Telephony > RTP > RTP Streams** に、MRFP（172.28.0.86）のストリームが並びます。**Play Streams** で再生できます。RTP の中には、それを説明する SDP を Wireshark が読む前に流れ始めるものがあります。すべてを復号するには、**Analyze > Enabled Protocols > RTP > rtp_udp** を有効にします（tshark では `-o rtp.heuristic_rtp:TRUE`）。

## 通話を断る前の音声ガイダンス

UE 1 は `ue xcap set baoc on` で発信を規制し、UE 2 に発信します。UE 1 の AS は通話を規制します（TS 24.611 4.5.2.4.1）が、その前に `notice` を流します（TS 24.628 4.2.4）。

```text
#361  UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#525  MMTel  -> MRFC   M  INVITE   sip:annc@mrfc...;play=/provisioned/notice, no SDP
#546  MRFC   -> MMTel  M  200 OK   SDP offer: the MRFP 172.28.0.86, PCMU, sendonly
#559  P-CSCF -> UE 1   C1 183      Require: 100rel, P-Early-Media: sendonly, SDP answer from the MRFP
#564  UE 1   -> P-CSCF C1 PRACK
#582  MMTel  -> MRFC   M  ACK      SDP answer: UE 1's media, recvonly
#598  MRFP   -> agw       RTP      PCMU, notice, on to UE 1 (#601)
#728  MRFC   -> MMTel  M  BYE      notice has played, 1.08 s after the ACK
#746  P-CSCF -> UE 1   C1 603      Reason: SIP;cause=603;text="Decline"
```

- **AS は PRACK を待ってから ACK を送ります。** UE 1 が SDP を受け取る前に、音声ガイダンスのフレームが失われないようにするためです。
- **603 には Reason が付きます。** 発信者にはすでに early dialog があるので、断る理由をそこで伝えます（TS 24.628 4.2.4）。
- INVITE が高信頼の暫定応答を受け付けない発信者や、プリコンディションを必須にした発信者には、すぐに 603 を返します。MRFC はプリコンディションの属性を書かないためです。

## CAT（着信側の利用者が選ぶ呼び出し音）

利用者の CAT は、事業者が HSS に設定します（TS 24.182 4.5.2）。ここでは OAM が、`PUT /oam/v1/subscriptions/{秘密の識別子}/customized-alerting-tone` に `{"tone": "cat1", "active": true}` を送り、それを HSS へ渡して設定します。利用者が自分で設定する標準の手段はありません（4.8）。UE 2 の着信側の AS は、着信のたびにこの設定を Sh で読みます（Service-Indication が `Customized-Alerting-Tone` の UDR）。そして、UE 2 に INVITE を送る前に MRFC に呼び出し音を頼み、UE 2 が呼び出し中のあいだ UE 1 に流します（TS 24.182 4.5.5.3.7）。

```text
#1288 12.471 UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#1489 12.530 MMTel  -> MRFC   M  INVITE   sip:annc@mrfc...;play=/provisioned/cat1;repeat=forever
#1497 12.532 MRFC   -> MMTel  M  200 OK   SDP offer of the MRFP
#1517 12.539 P-CSCF -> UE 2   C3 INVITE
#1560 12.543 P-CSCF -> UE 1   C1 183      UE 2's answer with the precondition table, as it came
#1627 12.558 UE 2   -> P-CSCF C3 180      UE 2 alerts its user
#1670 12.572 P-CSCF -> UE 1   C1 UPDATE   P-Early-Media: sendonly, SDP of the MRFP: sendonly, a=content:g.3gpp.cat
#1695 12.577 MMTel  -> MRFC   M  ACK      the tone starts
#1795 12.600 MRFP   -> agw       RTP      cat1, on to UE 1 (#1796)
#2105 15.540 UE 2   -> P-CSCF C3 200 OK   UE 2 answers
#2137 15.551 MMTel  -> MRFC   M  BYE      the tone stops
#2175 15.573 P-CSCF -> UE 1   C1 UPDATE   SDP of UE 2: UE 1's media moves to UE 2
#2221 15.580 P-CSCF -> UE 1   C1 200 OK   (INVITE)
```

- **UE 1 はプリコンディションを使います。** そのため UE 2 の 183 はそのまま UE 1 に届き、呼び出し音は UE 2 が呼び出しを始めたあとの UPDATE で届きます（TS 24.182 4.5.5.3.7 b 1）。プリコンディションを使わない発信者には、18x そのものに呼び出し音の SDP が入ります。
- **`a=content:g.3gpp.cat`**（RFC 4796）は、その SDP が呼び出し音のものだと示します。`P-Early-Media: sendonly` はその音声を認めます（TS 24.182 4.5.5.3.7 b 2）。
- **応答のとき**、AS は MRFC とのダイアログを終え、200 の前に UPDATE で UE 1 のメディアを UE 2 に移します。TS 24.628 Annex G のゲートウェイのモデルです。P-CSCF は、UE 1 の中継ノードのコンテキストの網の側に、この UPDATE のアドレスを渡します。網の側のターミネーションはラッチしないので、UE 1 の声はここから UE 2 に届き、呼び出し音を送ってきていた MRFP には行きません（[音声通話の流れを追う](call.md)）。スクリプトは、応答のあとにそれぞれの UE が相手の送ったものを受け取ったかを確かめます。
- **UE 1 のログ**には `"msg":"ringback before the answer","ringback":"network"` が出ます。スクリプトは、UE 1 が受け取った最初の 1 秒を、`deploy/announcements/cat1.wav` を G.711 μ-law で符号化したものと比べます。1 バイトも違いません。

続いてスクリプトは呼び出し音を止め（`"active": false`）、もう一度発信します。UE 2 の 180 は P-Early-Media なしで UE 1 に届き、MRFC への INVITE も MRFP からの RTP もありません。UE 1 のログには `"ringback":"local"` が出て、UE 1 は自分の呼び出し音を流します（TS 24.628 4.7.2.1）。

## 割込通話と保留

phone1 が phone2 に発信し、そのあと phone3 が phone1 に発信します。IR.92 は、待たせる着信の扱いを網ではなく UE に求めています（2.3.4）。

```text
#4772 29.049 phone1   -> P-CSCF A1 INVITE tel:+12025550102
#5171 30.097 P-CSCF -> phone1   A1 200 OK   phone2 answered
#5869 34.181 phone3   -> P-CSCF    INVITE tel:+12025550101
#5986 34.212 P-CSCF -> phone1   A2 INVITE
#6093 34.228 phone1   -> P-CSCF A2 180      Alert-Info: <urn:alert:service:call-waiting>
#6134 34.232 P-CSCF -> phone3      180      without that Alert-Info
#6278 38.256 phone1   -> P-CSCF A1 INVITE   re-INVITE, a=sendonly: phone2 on hold
#6389 38.277 phone1   -> P-CSCF A2 200 OK   phone1 answers phone3
#7172 42.361 phone1   -> P-CSCF A2 INVITE   Swap: phone3 on hold
#7283 42.394 phone1   -> P-CSCF A1 INVITE   Swap: phone2 back, a=sendrecv
```

- **phone1 は、call-waiting の Alert-Info を付けて呼び出しを返します**（TS 24.615 4.5.5.3.2、RFC 7462）。自動では応答しません。
- **phone1 の AS は、phone3 への 180 から Alert-Info を取り除きます。** `deploy/compose/hss.yaml` の CW の事業者の設定が、発信者に待たされていることを知らせないとしているためです（TS 24.615 4.5.5.2.3）。知らせる設定なら、AS は Alert-Info をそのまま渡し、音声ガイダンスを設定してあれば、待たせているあいだ発信者にそれを流します。
- **待たせた着信に応答すると、先の通話は保留になります。** offer を `sendonly` にした re-INVITE を送ります（TS 24.615 4.5.5.3.3、TS 24.610）。**Swap** は一方の通話を保留にし、もう一方を戻します。

## 3 人の会議

phone1 は応答済みの通話を 2 つ持っていて、**Merge** を押します。phone1 は会議ファクトリの URI に会議を作り、フォーカスである MMTel AS に、相手ごとに REFER を送ります。REFER の Refer-To には、phone1 とその相手の通話を置き換える Replaces が入っています（TS 24.147 5.3.1.3.3 の option 2b。IR.92 2.3.3 が求める方法です）。

```text
#7480 40.121 phone1   -> P-CSCF F  INVITE sip:mmtel@conf-factory.ims.mnc001.mcc001.3gppnetwork.org
#7511 40.131 MMTel  -> MRFC   M1 INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., with phone1's offer
#7534 40.136 P-CSCF -> phone1   F  200 OK  Contact: <sip:conf-o47ha7ifxyni@conf.ims.mnc001.mcc001.3gppnetwork.org>;isfocus
#7549 40.138 phone1   -> P-CSCF F  SUBSCRIBE sip:conf-o47ha7ifxyni@conf...  Event: conference
#7565 40.142 S-CSCF -> DNS       NAPTR? conf.ims.mnc001.mcc001.3gppnetwork.org
#7568 40.143 DNS    -> S-CSCF    SRV _sip._udp.conf...: 5060 mmtel.ims.mnc001.mcc001.3gppnetwork.org
#7571 40.144 S-CSCF -> MMTel  F  SUBSCRIBE sip:conf-o47ha7ifxyni@conf...
#7602 40.150 phone1   -> P-CSCF F  REFER   Refer-To: <sip:+12025550102@...;user=phone?Replaces=...>
#7635 40.156 P-CSCF -> phone1   F  200 OK  (REFER), not 202
#7662 40.164 MMTel  -> MRFC   M2 INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., no SDP
#7667 40.165 MRFC   -> MMTel  M2 200 OK  SDP offer of a new termination of the MRFP
#7712 40.179 P-CSCF -> phone2      INVITE  Replaces: phone2's call with phone1, the MRFP's offer
#7716 40.180 phone2   -> P-CSCF    183     phone2's answer
#7825 40.196 phone2   -> P-CSCF    200 OK  without SDP
#7867 40.201 phone2   -> P-CSCF    BYE     the call that was replaced
#7887 40.204 MMTel  -> MRFC   M2 ACK     phone2's answer from its 183
#7943 40.213 P-CSCF -> phone1   F  NOTIFY  Event: conference, phone1 and phone2
#8040 40.225 phone1   -> P-CSCF F  REFER   the same for phone3
#8398 40.306 P-CSCF -> phone1   F  NOTIFY  Event: conference, three users
```

- **会議の URI はホーム網の公開サービス識別子（PSI）です**（TS 23.228 4.3.6）。形は `sip:conf-<id>@conf.ims.mnc001.mcc001.3gppnetwork.org` です。ドメインは事業者が決め、ポート番号は付けません。網の外に出る可能性がある PSI には、ポート番号を付けないほうがよいためです（TS 23.003 13.5）。この URI はノードの名前を含みません。サブドメインの `conf` は、AS が受け持つ会議の全体を表します。このサブドメインの NAPTR と SRV のレコードが、会議を受け持つ AS を指します（`deploy/compose/dns`）。S-CSCF は、phone1 の SUBSCRIBE も、フォーカスとのダイアログの中の要求も、ほかのホストと同じく RFC 3263 でフォーカスへ届けます。これは TS 23.228 5.4.12.3 のサブドメインを使う方法です。ほかの網からの要求は I-CSCF から入ります。I-CSCF はこのサブドメインを知っていて（`deploy/compose/icscf.yaml` の `psi_subdomains`）、HSS に問い合わせずに、やはり DNS で要求を送ります（TS 24.229 5.3.2.1 step 3）。1 つの会議はそのダイアログとともに AS の 1 台にあります。AS を複数台にするときは台ごとにサブドメインを分け、DNS が会議ごとの要求を、その会議を持つ台へ送るようにします。
- **ファクトリの URI も PSI です。** こちらは発信側の PSI です（TS 23.228 5.4.12.1）。ファクトリの URI への INVITE は、作る人のほかの INVITE と同じく、作る人の発信側のフィルタ条件で AS に届きます。この INVITE は会議になる前に、作る人の発信側のサービスを通ります。S-CSCF は最初の要求を Request-URI で先へ送る前に、その利用者のフィルタ条件と照らし合わせるからです（TS 24.229 5.4.3.2 step 5）。フォーカスが独立したアプリケーションサーバーのときも、発信側のサービスを通った INVITE が同じように届きます。発信規制もほかの発信と同じようにこの INVITE に当てはまります。発信を規制している利用者が会議を作ろうとすると、自分の AS が 603（Decline）を返し、会議はできません（TS 24.611 4.5.2.4.1）。このような PSI を使えるのは、ホーム網の利用者だけです。
- **AS は会議のフォーカスであり、通知サービスでもあります**（TS 24.147 4）。参加者それぞれとダイアログを持ち、参加者ごとにメディアのための MRFC とのダイアログも持ちます。
- **MRFC は、最初の `conf=` の INVITE で混ぜ合わせを作り**、そのあとの INVITE ごとにターミネーションを加えます。MRFP は、それぞれの参加者にほかの参加者の音声の和を送ります（TS 23.333 6.2.9）。キャプチャでは、MRFP から agw への 3 つのストリームと、その逆の 3 つが見えます。スクリプトは、ある参加者が受け取る混ぜ合わせた音声が、どの参加者 1 人のトーンよりも大きいことを確かめます。
- **招かれた相手もプリコンディションを使います。** そのため、それぞれが MRFP の offer に高信頼の 183 で答え、200 には SDP を付けません。AS はその answer を M2 の ACK に入れて MRFC に渡します（RFC 3262 5）。
- **フォーカスは REFER を 200（OK）で受け付けます。** RFC 3515 では 202（Accepted）で応答していました。RFC 7647 5 がこれを改め、REFER を受け付ける側は 200 を送り、202 を送ってはいけないことになりました。古い UA から 202 を受けた側は、200 と同じに扱います。RFC 6665 8.3.1 が SUBSCRIBE について決めたのと同じです。改めた理由は、202 が 200 と違うことを何も伝えず、途中のプロキシに吸収されることもあるからです。TS 24.147 は RFC 3515 を、RFC 6665 と RFC 7647 による改訂を含めて参照していて、その流れの例でも 200 を返しています（A.4.4.1）。
- **REFER はそれぞれ refer イベントの購読を作ります**（RFC 3515 2.4.4）。フォーカスはすぐに、試していること（sipfrag の本文の `SIP/2.0 100 Trying`）を知らせます。相手への INVITE に最終応答が来たら、その応答を最後の NOTIFY で知らせ、Subscription-State を `terminated;reason=noresource` にします（2.4.7）。購読の長さは INVITE にかかりうる時間より長い 4 分です（3.4）。先に 4 分が過ぎたら、フォーカスは `terminated;reason=timeout` で購読を終えます（RFC 6665 4.2.1.4）。phone1 の側も、最後の `expires` が示した時刻に購読を終え、`active;expires=0` を受けたときはすぐに終えます（4.1.3）。
- **NOTIFY** には conference-info の XML（RFC 4575）が入っていて、参加者ごとに `<user>` と、状態が `connected` の `<endpoint>` があります。`<user>` の `entity` は、網が保証した参加者の識別子です。`<endpoint>` の `entity` はフォーカスが作るキーで、`urn:uuid:` の URI です。RFC 4575 5.7 はこのキーの作り方をフォーカスに任せ、例として Contact と GRUU を挙げています。フォーカスは Contact を使いません。Contact は参加者の UE のアドレスなので、NOTIFY に入れると、ほかの網の購読者を含むすべての購読者に見えてしまいます（[境界を越える会議](interconnect.md#境界を越える会議)）。ラボの各カードは、これを **Conference** の行に並べます。phone1 は購読が切れる前に更新します。更新されなかった購読は期限の時刻に終わり、フォーカスはそのとき `terminated;reason=timeout` の NOTIFY を送ります。そのあと、この購読には何も送りません（RFC 6665 4.2.1.4）。
- **購読ごとに、待つのは自分の購読者だけです。** フォーカスは、1 つの購読への NOTIFY を、前の NOTIFY に最終応答が来てから 1 つずつ送ります。こうして NOTIFY は順に届きます。応答しない購読者がいても、参加する利用者への 200 も、ほかの購読者への NOTIFY も遅れません。応答しない購読者は、期限より早く購読を失います。NOTIFY を送ってから 32 秒が過ぎて Timer F が切れても応答が無ければ、フォーカスはその購読を終えます。408（Request Timeout）を受けたときも終えます。408 は時間切れと同じ扱いになるためです（RFC 5057 5.1）。481 のように、購読者が購読を失ったことを示す応答を受けたときも同じです（RFC 6665 4.2.2）。そのあと、フォーカスはこの購読に何も送りません。最後の NOTIFY も送りません。戻ってきた UE は、もう一度購読するものとされています（4.2.2）。会議のセッションのダイアログの中の購読なら、こうした失敗で終わるのはその購読だけです。セッションは同じダイアログで続き、参加者は会議に残ります（RFC 5057 5.1、5.2）。481 が示すのは、相手がダイアログのその使い方の状態を失ったことで、ダイアログを失ったことではありません。408 は時間切れと同じ扱いで、時間切れが影響するのはその要求が属する使い方だけです。`internal/mmtel` の `TestA481ToANOTIFYWithinTheDialogEndsOnlyThatSubscription` は、そうした NOTIFY に 481 で答えます。そのあと参加者は同じダイアログの re-INVITE で会議を保留にし、ほかの購読者にはそれが見えます。フォーカスはこの参加者には、保留を知らせる NOTIFY を送りません。
- **匿名を求めた参加者は、識別子なしで会議の状態に載ります。** フォーカスはこの求めに応じなければならず、文書のどの URI にも識別子を出しません（RFC 4575 8.2）。利用者は Privacy ヘッダーでこれを求めます（RFC 3323 4.2）。`id`、`user`、`header` のどれも、その相手の識別子が制限されていることを表します。利用者の識別子の制限は、この値に乗ってフォーカスに届きます。会議を作る利用者と自分から入る利用者では OIR が（TS 24.605 4.6.5）、フォーカスが招いた利用者では TIR が（4.6.3）そうします。招かれた側は 2xx でこれを求めます。UE が自分でこれを求める必要はありません。フォーカスが読む前に、その利用者のサービスがこの値を入れるからです。網が識別子を隠す利用者の INVITE には OIR が（TS 24.607 4.5.2.4）、応答する利用者の 2xx には TIR が（TS 24.608 4.5.2.9）、この値を入れます。OIR の AS は `id` か `header` を入れます。識別子だけを隠すなら `id`、UE が自分で隠せない私的な情報をすべて隠すなら `header` で、どちらにするかはその利用者の契約の設定で決まります（TS 24.607 4.5.2.4 item 1）。着信側の AS も `header` を同じ制限として読み、代わりに `id` を入れます（4.5.2.9）。ですからフォーカスは、`header` が付いた相手の識別子も会議の状態に載せません。OIR のモードが temporary で、既定が非通知の利用者は、`Privacy: none` を付ければ、その会議だけ識別子を通知できます。この参加者の `entity` は `sip:anonymous1@anonymous.invalid` になり、display-text は `Anonymous1` になります。次の参加者からは大きい番号になり、一度使った番号をフォーカスがもう一度使うことはありません。これは RFC 4575 5.6 の書き方です。ホストは RFC 2606 2 が予約した `.invalid` のトップレベルドメインにあり、DNS はこれを解決しません。ユーザー部は、参加者の中で URI が重ならないようにするためのものです。この参加者もほかと同じく会議の状態の 1 人で、自分の `<endpoint>` と状態を持ちます。ほかの参加者には、誰かがいることは見えて、それが誰かは見えません。`user` で匿名を求めた参加者は、自分が頼んだ招待にも識別子を残しません。フォーカスは、招く相手への INVITE に、REFER の Referred-By を入れません。この値はその参加者を名指しするためです（TS 24.605 4.6.5）。識別子はフォーカスの中に留まります。どの NOTIFY にも出ませんが、フォーカスはどの購読者がどの参加者かを変わらず知っています。`internal/mmtel` の `TestPrivacyKeepsTheIdentityOutOfTheConferenceState` が、会議に入る 3 通りの道をすべて試します。`TestTheCreatorsIdentityRestrictionReachesTheConferenceState` は、UE が何も求めない利用者で会議を作ります。OIR のモードが permanent で契約の設定が 2 通りの場合と、モードが temporary で既定が非通知の場合です。`Privacy: none` で識別子を通知する利用者でも会議を作ります。この場面の電話は匿名を求めないので、キャプチャにはそれぞれの識別子が出ます。
- **会議は作った人が抜けると終わります**（TS 24.147 5.3.2.7）。phone1 が切ると、AS は phone2 と phone3 に BYE を送り、参加者ごとの MRFC とのダイアログにも BYE を送ります。
- **会議が終わると、すべての購読が終わります。** フォーカスは、まだ持っている購読ごとに、Subscription-State が `terminated;reason=noresource` の最後の NOTIFY を送ります（TS 24.147 5.3.3.4、RFC 4575 3.3）。参加者によっては、会議のセッションのダイアログの中で購読しています。UE は、ダイアログの外の SUBSCRIBE に 403 が返ると、ダイアログの中で購読し直します（TS 24.147 5.3.1.2）。この参加者には、最後の NOTIFY がダイアログの中で届き、その NOTIFY に答えてから BYE が届きます。BYE で終わるのはセッションだけで、同じダイアログの購読は終わらないためです（RFC 5057 5.5）。このシナリオの電話は、ダイアログの外で購読します。ダイアログの中の購読は、`internal/mmtel` の `TestTheLastNOTIFYWithinADialogComesBeforeTheBYE` が試します。網が識別子を保証した参加者と、保証された識別子なしで入った参加者の両方です。ただし、NOTIFY に応答が無かったためにフォーカスがすでに外した購読には、最後の NOTIFY が届きません。
- **フォーカスは会議を抜けた参加者の購読を、最後の NOTIFY で終わらせます**（TS 24.147 5.3.3.3）。Subscription-State は `terminated;reason=noresource` です。参加者が会議のセッションのダイアログの外で購読していても、中で購読していても同じです。ほかの購読者の購読は続き、抜けた参加者は `disconnected` として載ります。BYE でセッションが終わったダイアログに、この NOTIFY がどう届くかは、[ダイアログの中で購読したまま抜ける](#ダイアログの中で購読したまま抜ける)で説明します。

## フォーカスが相手を招く方法

phone2 を会議に招く INVITE（上の #7712）は、AS から phone2 へ直接は届きません。フォーカスはこの INVITE を S-CSCF に送り、S-CSCF は利用者の発信と同じように送り先を決めます。

```text
focus    S-CSCF   HSS      I-CSCF   MMTel AS  P-CSCF   phone2
                                    services
 |        |        |        |        |         |        |  INVITE tel:+12025550102 with Replaces, Route: <sip:scscf...;lr;orig>,
 |        |        |        |        |         |        |  P-Asserted-Identity: <sip:conf-o47ha7ifxyni@conf...>
 |------->|        |        |        |         |        |
 |        |------->|        |        |         |        |  SAR UNREGISTERED_USER, sip:conf-o47ha7ifxyni@conf...
 |        |<-------|        |        |         |        |  SAA, the profile of the PSI user, with no iFCs
 |        |---------------->|        |         |        |  INVITE sip:+12025550102@ims...;user=phone, from ENUM
 |        |<----------------|        |         |        |  after the LIR: to the S-CSCF of phone2
 |        |------------------------->|         |        |  P-Served-User: phone2;sescase=term
 |        |<-------------------------|         |        |  the INVITE of the B2BUA
 |        |----------------------------------->|        |
 |        |        |        |        |         |------->|  INVITE, Contact: <sip:conf-o47ha7ifxyni@conf...>;isfocus
```

- **フォーカスは会議の URI の代わりに要求を送る発信側の UA です**（TS 24.147 5.3.2.2.1、TS 24.229 5.7.3）。会議の URI は AS が受け持つ PSI です。フォーカスはこの URI を TS 24.147 5.3.2.5.4 step 2 のとおり P-Asserted-Identity に入れ、S-CSCF が担当する識別子もこの URI になります。AS は INVITE の送り先を自分では決めず、S-CSCF の URI に `orig` パラメータを付けて Route の先頭に入れます。この S-CSCF は、phone1 がファクトリの URI に送った INVITE を AS に届けた S-CSCF です。
- **`orig` があると、S-CSCF は発信側を担当します**（TS 24.229 5.4.3.1）。UE の Service-Route のユーザー部の `orig` と同じ働きです。ここでの発信側は会議の URI で、登録はありません。S-CSCF は、登録していない利用者の代わりにアプリケーションサーバーが送る要求と同じく、`UNREGISTERED_USER` の SAR で HSS からプロファイルを受け取り（TS 24.229 5.4.3.2、NOTE 24）、`ORIGINATING_UNREGISTERED` の iFC を評価します。
- **HSS はすべての会議の URI を 1 つの PSI ユーザーとして持ちます**（TS 23.228 5.4.12.4）。ワイルドカードの PSI `sip:!.*!@conf.ims.mnc001.mcc001.3gppnetwork.org` と、私的サービス識別子 `conference@ims.mnc001.mcc001.3gppnetwork.org` です。PSI の 2 つの `!` のあいだは正規表現で、私的サービス識別子は PSI を Cx の手順に結び付けるためだけのものです（TS 23.003 13.5 と 13.5A。`deploy/compose/hss.yaml` の `psi_users`）。SAA のプロファイルは会議の URI に `IdentityType` 2（`WILDCARDED_PSI`）と、一致したワイルドカードの PSI を添えて示し（TS 29.228 B.2.1）、iFC を持ちません。S-CSCF はこのプロファイルを数分のあいだ持っているので、同じ会議へ次に招くときには SAR が要りません。
- **そこから先、INVITE は phone1 の発信と同じ道を通ります**（TS 23.228 5.4.12.5 c）。ENUM が番号を SIP URI に変え（TS 24.229 5.4.3.2 step 10）、S-CSCF は自分を Record-Route に入れ、I-CSCF が phone2 の S-CSCF を見つけます。それは同じ S-CSCF で、今度は phone2 を担当し、phone2 の着信側のサービスのために INVITE を MMTel AS に渡します。ほかの網の番号なら、S-CSCF は代わりに IBCF へ INVITE を送ります（[境界を越える会議](interconnect.md#境界を越える会議)）。

## 会議に戻る

会議を抜けた参加者は、会議が続いているあいだなら戻れます。フォーカスの Contact で知った会議の URI に INVITE を送ります（TS 24.147 5.3.1.4.1）。phone2 が電話を切り、そのあと会議の URI に発信したとします。

```text
phone2     P-CSCF   S-CSCF   MMTel AS              MRFC
                           services  focus
 | INVITE sip:conf-o47ha7ifxyni@conf...            |
 |-------->|------->|------->|        |            |  P-Served-User: phone2;sescase=orig
 |         |        |<-------|        |            |  the INVITE of the B2BUA, to the same URI
 |         |        |---------------->|            |  by DNS, without P-Served-User
 |         |        |        |        |----------->|  INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., phone2's offer
 |         |        |        |        |<-----------|  200 OK, the answer of the MRFP
 |         |        |<----------------|            |  200 OK, Contact: <sip:conf-o47ha7ifxyni@conf...>;isfocus
 |         |        |------->|        |            |
 |<--------|<-------|<-------|        |            |  200 OK, the same Contact
```

- **INVITE は phone2 のほかの発信と同じ道を通ります。** S-CSCF は phone2 の発信側のサービスのために、この INVITE を MMTel AS に渡します。B2BUA である AS は同じ URI に宛てた新しい INVITE を S-CSCF に返します。S-CSCF は上の SUBSCRIBE と同じく、DNS でフォーカスを見つけます。ほかの網からの INVITE は I-CSCF から入り、I-CSCF も DNS でフォーカスに送ります。フォーカスに INVITE を届けるのはフィルタ条件ではないので、この INVITE には P-Served-User がありません。AS は P-Served-User の有無で、フォーカスに宛てた INVITE と phone2 のサービスのための INVITE を見分けます。どちらも Request-URI は同じです。
- **フォーカスは作った人と同じやり方で phone2 を迎え入れます**（5.3.2.4.1）。phone2 の offer で、混ぜ合わせの新しいターミネーションを MRFC に頼みます。MRFC が応答したら、MRFP の answer を入れ、Contact を `isfocus` 付きの会議の URI にして phone2 に応答します。phone2 のサービスの AS は、招かれた相手のときと同じく、その Contact をそのまま渡し、自分を Record-Route に入れます。phone2 が会議の URI に送る要求が、そのあとも AS を通るようにするためです（TS 24.229 5.7.5.1）。
- **NOTIFY** は phone2 を再び `connected` として並べます。joining-method は作った人と同じ `dialed-in` になります。フォーカスが phone2 を招いたときは `dialed-out` でした。
- **網が識別子を保証した利用者なら、誰でも会議に入れます。** Privacy に `id` か `user` を持つ利用者も入れます。AS はこの利用者を匿名として扱い、迎え入れるのに保証された識別子は要りません（TS 24.229 5.7.1.4 a）。前に会議にいたかどうかは問いません。上限は参加者 6 人で、NOTIFY の maximum-user-count と同じです。7 人目には 403（Forbidden）を返します。どの会議にも使われていない会議の URI に INVITE が来たら、404（Not Found）を返します（5.3.2.4.1 step 1）。会議を作った人が戻ろうとしても同じです。作った人が抜けた時点で会議は終わり、フォーカスはその URI を手放しているためです（5.3.2.7）。

スクリプトではこの場面を動かしません。`internal/mmtel/conference_network_test.go` の `TestRejoinWithTheConferenceURI` が、1 つのプロセスの中の IMS 全体でこの流れを動かします。B が A の会議を抜け、会議の URI で戻り、混ぜ合わせを通して A の声を聞きます。

## ダイアログの中で購読したまま抜ける

ダイアログの外の SUBSCRIBE に 403 が返った UE は、代わりに会議のセッションのダイアログの中で購読します（TS 24.147 5.3.1.2）。するとダイアログには使い方が 2 つできます。INVITE が作ったセッションと、SUBSCRIBE が作った購読です（RFC 6665 4.5.2）。ダイアログは最後の使い方が終わったときに終わり（RFC 5057 4）、BYE で終わるのはセッションだけです（5.5）。そのため、こうした参加者が電話を切っても、購読を終わらせる NOTIFY が通るまで、ダイアログは残ります。上の phone2 と同じく自分から会議に入った UE が、ダイアログの中で購読してから電話を切るとします。

```text
UE         P-CSCF   S-CSCF   MMTel AS
                           services  focus
 |-------->|------->|------->|        |  BYE
 |<--------|<-------|<-------|        |  200 OK from the B2BUA: the session ends, the dialog stays
 |         |        |<-------|        |  BYE on the other leg of the B2BUA
 |         |        |---------------->|
 |         |        |<----------------|  200 OK
 |         |        |------->|        |
 |         |        |<----------------|  NOTIFY Event: conference, Subscription-State: terminated;reason=noresource
 |         |        |------->|        |
 |<--------|<-------|<-------|        |  the same NOTIFY, on the leg of the B2BUA to the UE
 |-------->|------->|------->|        |  200 OK: the UE lets the dialog go
 |         |        |<-------|        |
 |         |        |---------------->|  200 OK: the B2BUA lets both legs go, and the focus the dialog
```

- **UE のサービスの AS が BYE に答え**、もう一方の側のセッションを自分の BYE で終わらせます。B2BUA として受け持つ通話では、いつもこうします。ただし、両側のダイアログは残します。SUBSCRIBE がダイアログの中で AS を通ったので、AS は購読がそのダイアログを共有していることを知っています。フォーカスは AS の BYE に 200 で答え、そのあと同じダイアログで最後の NOTIFY を送ります（TS 24.147 5.3.3.3）。B2BUA はこの NOTIFY を UE に渡し、UE の 200 をフォーカスに返します。
- **両端ともダイアログを手放すのは BYE のときではなく購読が終わったときです。** フォーカスは、NOTIFY に応答が来たらダイアログを忘れます。UE はその NOTIFY に答えたらダイアログを手放します。NOTIFY が届かなければ、B2BUA と同じく、購読の期限が切れてから 32 秒（T1 の 64 倍）が過ぎたときに手放します（RFC 5057 4.2）。
- **経路の上の B2BUA も同じです。** B2BUA は、ダイアログの中の SUBSCRIBE が作った購読が続くあいだ、両側のダイアログを持ち続けます。手放すのは、購読を終わらせる NOTIFY に応答が来たとき、NOTIFY か購読の更新が失敗して購読が終わったとき、または購読の期限が切れてから 32 秒（T1 の 64 倍）が過ぎたときです。この 32 秒のあいだに、期限が切れた購読を終わらせる NOTIFY が通れます（RFC 6665 4.2.1.4）。利用者のサービスの MMTel AS はこうした B2BUA で、境界を越える会議の IBCF もそうです（[境界を越える会議](interconnect.md#境界を越える会議)）。
- **BYE のあと、ダイアログは新しい使い方を受け付けません。** 新しい購読を作ろうとする SUBSCRIBE には 603（Decline）を返します。refer イベントの購読を作ることになる REFER にも 603 を返します。603 はダイアログとほかの使い方をそのままにして、新しい使い方だけを断る応答です（RFC 5057 5.6）。re-INVITE のようなセッションの要求には 481 を返します。セッションはもう終わっているためです。

このシナリオの電話はダイアログの外で購読するので、この流れはキャプチャに出ません。`internal/mmtel` の `TestAParticipantThatLeavesGetsTheLastNOTIFYWithinItsDialog` が、フォーカスでこの流れを試します。あとから入った参加者と、作った人の両方です。作った人の BYE では会議も終わります。B2BUA を通る流れは `internal/b2bua` の `TestASubscriptionWithinTheDialogsOutlivesTheBYE` が、2 つの網の IBCF を通る流れは `internal/ibcf` の `TestConferenceStateAcrossTheBorder` が、UE での流れは `internal/ue` の `TestTheUEKeepsItsDialogForTheLastNOTIFY` が試します。

## MRFC を運用から外す

運用者は、ノードが受け持っているものを切らずに、そのノードを運用から外せます。ノードは新しいものを受けず、始めたものは続け、それが無くなるのを待ちます。これをドレインと呼びます。

MRFC が始めたものを続けなければならないのは、ダイアログがその INVITE を受けた台の中にあるからです。ダイアログの Contact はその台の名前を持ち、MRFP の termination、タイマー、音声ガイダンスが終わったという知らせを待つ処理も、その台にあります。商用の網は MRFC を複数台で動かし、AS の設定に書く 1 つの名前の後ろに並べて、新しい要求を DNS でその台に振り分けます。

MRFC のノードはそのための保守用の API をポート 8085 で出しています。`deploy/compose/mrfc.yaml` の `maintenance` の節の待ち受けです。この API はパスワードを求めないので、ポートはホストに公開していません。要求は MRFC のコンテナの中から送ります。管理上の状態は TS 28.622 5.3.1 の値を取ります。`UNLOCKED` は新しい仕事を受け、`SHUTTINGDOWN` は持っているものだけを扱い、`LOCKED` は新しいものを何も受けません。

1. MRFC を shut down します。

   ```sh
   docker compose exec mrfc sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfc/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   答えの最後に MRFC の状態が付きます。自分のダイアログが 1 つも無ければ、すぐに `LOCKED` になります。

   ```json
   {"role":"mrfc","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   `users` は、この台が持っているダイアログの数です。ダイアログの外の INVITE のうち、受けてまだ終わっていないものが 1 つずつ数に入ります。音声ガイダンスは流し終わるか AS が止めるまで、会議の参加者はその参加者が混合にいるあいだです。Request-URI が指すサービスや SDP を見て断る INVITE も、答えるあいだは数に入ります。Request-URI そのものを読めない INVITE は、数に入る前に 400 Bad Request で返します。音声ガイダンスを流しているあいだに shut down をすると `SHUTTINGDOWN` にとどまり、最後のダイアログが終わったときに `LOCKED` になります。混合そのものは MRFP にあって、このノードより長く残ります。`operational_state` は MRFC が働けるかどうかを表します（TS 28.622 5.3.3）。ノードが動いているあいだは `ENABLED` です。MRFC は状態を知らせられるような結び付きを MRFP と持っていません。MRFP への操作は 1 つずつが別の要求で、答えない MRFP はその操作を必要とした INVITE の断りに現れます。ノード全体は `docker compose exec mrfc wget -qO- http://127.0.0.1:8085/maintenance/v1/state` で見られます。

2. [通話を断る前の音声ガイダンス](#通話を断る前の音声ガイダンス)と同じように UE 1 の発信を禁止して、UE 2 に発信します。

   ```sh
   docker compose run --rm ue xcap set baoc on
   docker compose run --rm ue call tel:+12025550102
   ```

   禁止した発信と同じように `603 Decline` で終わりますが、その前に UE 1 は音声ガイダンスを聞きません。

```text
 UE 1        P-CSCF      S-CSCF      MMTel AS      MRFC
 |  INVITE     |           |            |            |
 |------------>|---------->|----------->|            |  tel:+12025550102。OCB が禁止する発信
 |             |           |            |----------->|  INVITE sip:annc@mrfc...;play=/provisioned/notice
 |             |           |            |  503 Retry-After: 30
 |             |           |            |<-----------|
 |  603        |  603      |  603       |            |  早期メディアも Reason も無し
 |<------------|<----------|<-----------|            |
```

- **AS は RFC 4240 3 の Request-URI で MRFC に音声ガイダンスを頼みます**。MRFC が運用に入っているときに通話を断る流れと同じです。
- **MRFC は 503 Service Unavailable に Retry-After: 30 を付けて返し**、MRFP には何も用意させません。ダイアログの外の要求は新しいものを始めるので、`UNLOCKED` でないあいだ MRFC はこれを受けません。Retry-After は、クライアントがこのサーバーにほかの要求を送らずにいる秒数です（RFC 3261 21.5.4）。この値を決めた仕様はありません。30 秒はこのプロジェクトが選んだ値です。この応答は頼まれたサービスの P-Asserted-Identity を持ちます。MRFC が最初の INVITE に返す応答はどれもこれを持ちます（TS 24.229 5.8.2.1.1.1）。
- **AS は、MRFC の名前について DNS が返した次の台へ INVITE を送ります**（RFC 3263 4.3）。503 が AS 自身に届くのは、試す台が残っていないときだけです。この網は MRFC を 1 台で動かしています。
- **AS は音声ガイダンスなしで、禁止した発信を断ります**。UE 1 には 603 がすぐ届きます。MRFP の早期メディアを認める確実な 183 も、音声ガイダンスを聞かせてから断るときに AS が付ける `Reason` も付きません（TS 24.628 4.2.4）。サービスそのものは変わりません。発信はどちらでも禁止され、失われるのは音声ガイダンスだけです。着信側の利用者が選ぶ呼び出し音も同じで、発信者は着信者を呼び出すあいだ、自分の UE の呼び出し音を聞きます。
- **会議を作る INVITE と、会議に入る INVITE は断られます**。混合する MRFP がなければ会議のメディアがないので、フォーカスはつなげない参加者を入れません。AS は MRFC が返した応答コード、つまり 503 で断りますが、参加者に届くのは 500 Server Internal Error です。503 はそれを返したサーバーについてのもので、途中のプロキシについてのものではないので、プロキシは 503 をそのまま渡さず、自分の 500 を返します（RFC 3261 16.7 の手順 6）。

`UNLOCKED` でないあいだ、MRFC は始めたものを続け、新しく始めるものを断ります。

続けるのは次のものです。

- **持っているダイアログの中の要求**。音声ガイダンスは最後まで流れ、参加者は混合に残ります。保留の re-INVITE と、混合から離れて戻る参加者の re-INVITE（TS 24.229 5.8.4.1.1.1）、AS が流すのを止める BYE（5.8.3.1.1）、MRFP が流し終わったと知らせたときに MRFC 自身が送る BYE（5.8.3.2.1）です。
- **ダイアログの外の OPTIONS**。これまでと同じように 200 を返します（RFC 3261 11.2）。ノードに何ができるかを尋ねる要求で、ノードは生きています。

503 を返すのは **ダイアログの外の INVITE** です。RFC 4240 3 の音声ガイダンスと、RFC 4240 5 の会議の参加者で、その会議の参加者をすでに持っていても断ります。ほかの台がその参加者を受けられるなら、この最後の断りで参加者が失うものはなく、そこが AS との違いです。MRFC の会議がどの台にも結び付かないのは、台が 1 つの `Store` を共有しているあいだです。混合を持つ MRFP とコンテキストはそこにあり、どの台もすべての MRFP を知っているので、ほかの台が受けた参加者は同じ混合に入り、運用から外した台の参加者もその混合に残ります。`deploy/redundancy/redundancy.yaml` の構成がこれで、各ノードの `store` の節が 1 つの Valkey サーバーを指します。会議を自分のプロセスの記憶に置く台は、これを共有しません。断られた参加者を受けた台は、その会議のメディアを見つけられず、MRFP に別のコンテキストを作らせるので、混合が 2 つに分かれます。この手順書の Compose のプロジェクトは MRFC を 1 台で動かし、会議をその台の記憶に置いているので、運用から外しているあいだに、混合から離れた参加者が戻ろうとすると、そのまま断られ、混合はその参加者なしで続きます。会議のフォーカスは MMTel AS で、そのままの場所にあります（[3 人の会議](#3-人の会議)）。MRFC は SUBSCRIBE をまったく受けません。会議のイベントパッケージはフォーカスにあるので、MRFC に届いた SUBSCRIBE には、運用から外れているかどうかによらず 405 Method Not Allowed を返します。

3. 同じ要求に `UNLOCKED` を入れて MRFC を戻し、UE 1 からもう一度発信します。

   ```sh
   docker compose exec mrfc sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfc/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue call tel:+12025550102
   docker compose run --rm ue xcap set baoc off
   ```

   UE 1 は 603 の前に音声ガイダンスをまた聞きます。このあとのシナリオのために、最後のコマンドで発信の禁止を解きます。

Wireshark では、`sip.Status-Code == 503 && ip.src == 172.28.0.85` が MRFC の 503 を、`sip.r-uri contains "annc@"` が音声ガイダンスを頼んだ INVITE を見せます。PUT の要求はコンテナのループバックを通るので、キャプチャには入りません。

管理上の状態はノードのプロセスの中にあります。起動し直したノードは `UNLOCKED` です。`scripts/e2e-maintenance-sip.sh` はこの手順を実行し、キャプチャを確かめます。

## MRFP を運用から外す

MRFP は呼の音声を運ぶノードと同じメディア中継ノードです。運用から外す手順も同じで、`deploy/compose/mrfp.yaml` が決める 8085 番の保守用の API を使い、パスの役割の名前を `mrfp` にします。状態の意味と、中継ノードが続けるものは、[メディア中継ノードを運用から外す](call.md#メディア中継ノードを運用から外す)にあります。`users` は、この MRFP が持っているコンテキストの数です。流している音声ガイダンス 1 つにつき 1 つ、混合している会議 1 つにつき 1 つです。

MRFP だけのものは、断りがどこに届くかです。MRFC は音声ガイダンスの INVITE を受けたときと、会議の最初の参加者が来たときに、MRFP にコンテキストを求めます。

1. [MRFC を運用から外す](#mrfc-を運用から外す)と同じように UE 1 の発信を Ut で禁止してから、MRFP を shut down して UE 2 に発信します。

   ```sh
   docker compose exec mrfp sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfp/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue xcap set baoc on
   docker compose run --rm ue call tel:+12025550102
   ```

   呼は発信を禁止した呼のとおり `603 Decline` で終わり、その前に UE 1 は音声ガイダンスを聞きません。MRFC 自身を運用から外したときとまったく同じです。

```text
 MMTel AS        MRFC             MRFP
 |   INVITE        |               |  sip:annc@mrfc...;play=/provisioned/notice
 |---------------->|               |
 |                 | GET /announcements/notice
 |                 |-------------->|
 |                 | 200 OK        |  MRFP は持っていて、1 回の長さも答える
 |                 |<--------------|
 |                 | POST /contexts|  Add。発信側に向けたターミネーション
 |                 |-------------->|
 |                 | 503 Service Unavailable
 |                 |<--------------|  運用から外れているので、コンテキストを作らない
 |   503           |               |  一覧にほかの MRFP が無い
 |<----------------|               |
```

- **MRFC は、その音声ガイダンスを MRFP が持っているかを尋ねます。** MRFP は持っていると答えます。これはコンテキストを作る操作ではなく、持っているものを読む Audit なので、ドレイン中の MRFP もいつもどおり答えます。
- **MRFC はその音声ガイダンスを流すコンテキストを求めます**（TS 29.333 5.17.2.2）。MRFP は 503（Service Unavailable）を返し、誤りの符号は `out_of_service` です。
- **MRFC は一覧の次の MRFP を試します。** Call-ID の hash が選んだ台から始め、最後の台のあとは最初の台に戻ります。P-CSCF が中継ノードを試す順と同じです。TS 23.333 6.1.2 は新しい呼に使えなくなった MRFP に新しい接続を作らないよう求めています。この網の MRFP は 1 台なので、一覧はすぐ尽きます。
- **MRFC は AS に 503 を返します。** MRFP が 1 台も残らなくなってからです。この応答に `Retry-After` は付きません。MRFC 自身は運用に入っていて、`Retry-After` を付けると、そのあいだ MRFC には何も送らないよう AS に求めることになるためです（RFC 3261 21.5.4）。
- **AS は MRF なしで呼を扱います。** 禁止された呼は本来なら許可する早期メディアなしで断られ、603 に `Reason` は付きません。CAT も同じで、会議に入ろうとした利用者は断られます。混合するものが無ければ、会議のメディアがないためです。

混合がすでにドレイン中の MRFP にある会議には、参加者はそのまま入れます。混合はその MRFP にしか無いので、MRFC はその MRFP に、持っているコンテキストへのターミネーションを求め、MRFP はそれを足します。これでコンテキストが増えることはないため、この MRFP の最後の会議と最後の音声ガイダンスが終われば、ドレインも終わります。

2. 同じ要求に `UNLOCKED` を入れて MRFP を unlock し、UE 1 からもう一度発信します。

   ```sh
   docker compose exec mrfp sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfp/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue call tel:+12025550102
   docker compose run --rm ue xcap set baoc off
   ```

   UE 1 は、603 の前に音声ガイダンスをまた聞きます。最後のコマンドは、このあとの場面のために、UE 1 の発信を通すように戻します。

Wireshark では、`http.response.code == 503 && ip.src == 172.28.0.86` でドレイン中の MRFP の断りが、`sip.Status-Code == 503 && ip.src == 172.28.0.85` で MRFC が AS に渡す 503 が見られます。

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.r-uri contains "annc@"` | 音声ガイダンスや呼び出し音を頼む要求。`contains "play=/provisioned/cat"` で CAT だけを選べます。 |
| `sip.r-uri contains "conf="` | MRFC の会議に加わる参加者。 |
| `sip.P-Early-Media` | early media を認める応答と UPDATE。 |
| `sdp.media_attr == "content:g.3gpp.cat"` | CAT の SDP。 |
| `sip.Alert-Info contains "call-waiting"` | 待たせる着信の 180。 |
| `sip.Refer-To contains "Replaces"` | 相手を会議に招く REFER。 |
| `sip.Replaces` | 通話を引き継ぐフォーカスの INVITE。 |
| `sip.Event contains "conference"` | 会議イベントパッケージの SUBSCRIBE と NOTIFY。 |
| `sip.Contact contains "isfocus"` | フォーカスの応答。 |
| `dns.qry.name contains "conf."` | 会議のフォーカスへの経路を引く DNS の問い合わせ。 |
| `rtp && ip.addr == 172.28.0.86` | MRFP の RTP。RTP のヒューリスティックを有効にして使います。 |
| `http && ip.addr == 172.28.0.86` | MRFP の制御の API。ターミネーションの POST、PUT、DELETE と、再生が終わったことを知らせる Notify です。 |

`scripts/e2e-mrf.sh` は、この表のフィルタをすべて、このシナリオのキャプチャに当てます。フレームが 1 つも見つからないフィルタがあれば、試験は失敗します。

## コードの場所

| ファイル | 内容 |
| --- | --- |
| `internal/mmtel/mrf.go`、`internal/mmtel/announcement.go` | AS と MRFC のダイアログと、通話を断る前の音声ガイダンス。 |
| `internal/mmtel/cat.go`、`internal/mmtel/catdata.go` | 着信側の AS の CAT と、HSS にあるそのデータ。 |
| `internal/mmtel/waiting.go` | AS の CW。 |
| `internal/mmtel/conference.go`、`internal/mmtel/conferenceevent.go` | フォーカスと会議イベントパッケージ。 |
| `internal/scscf/session.go`、`internal/scscf/unregistered.go`、`internal/hss/psi.go` | 会議の URI の代わりに送る要求を担当する S-CSCF と、HSS の PSI ユーザー。 |
| `internal/mrfc`、`internal/netann` | MRFC と、RFC 4240 の Request-URI。 |
| `internal/mrfc/maintenance.go` | MRFC がノードの保守用の API に伝えるものと、運用者が運用から外しているあいだに断るもの。 |
| `internal/agw` | MRFP。ターミネーション、音声ガイダンス、混ぜ合わせと、その制御の API。 |
| `internal/agw/maintenance.go` | メディア中継ノードがノードの保守用の API に伝えるものと、運用者が運用から外しているあいだに断るもの。 |
| `internal/ue/phone_conference.go`、`internal/ue/conference.go` | Swap、Merge と、会議の UE の側。 |
| `internal/sip/call.go`、`internal/b2bua/subscription.go` | ダイアログを共有する購読のために BYE のあとも残るダイアログと、そのために両側のダイアログを持ち続ける B2BUA。 |
| `internal/mmtel/mrf_network_test.go`、`internal/mmtel/conference_network_test.go`、`internal/mmtel/conference_border_test.go` | 同じ流れを、1 つのプロセスの中の IMS 全体で通す試験。最後のものは、ほかの網の相手と通す。 |

## このプロジェクトで簡略化していること

どの項目にも、コードに `SIMPLIFIED:` のコメントがあります。`grep -rn "SIMPLIFIED:" internal cmd` ですべてを一覧できます。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| MRFC、TS 23.333 5.12 | MRFP が流したり混ぜたりできるのは G.711 PCMU だけで、トランスコードはしません。5.12.1 は MRFP にトランスコードと、少なくとも AMR への対応を求め、AMR-WB など TS 26.114 のほかのコーデックへの対応を任意としています。PCMU の無い offer には、Warning 305 を付けた 488 を返します。 | 会議で混ぜるのは復号した音声で、製品には AMR の復号器がありません。復号器を加えると手間が大きく、その割に、この手順書で追う信号はあまり変わりません。UE シミュレータは今も PCMU を offer に入れます。 |
| MRFC、RFC 4240 3、TS 24.229 10.3.2.2 | 用意してある音声だけを流し、Cr で URL から取ってくることはしません。 | WAV ファイルは MRFP の設定にあります。 |
| MRFC、TS 24.229 5.8.1 | Contact は GRUU ではなく、その台の SIP URI です。 | MRFC とのダイアログの外から MRFC に要求を送るものはありません。 |
| MRFC、TS 24.229 5.8.2.1.1.1、RFC 4240 2 と 8 | 課金のデータを持たず、sips: を強制せず、AS を認証しません。 | このプロジェクトに課金の記録を作るノードは無く、キャプチャで中身が見えるように何も暗号化しないためです。 |
| MRFC、RFC 4240 3 | RFC が勧める理由の文言は Warning に入れます。 | ステータス行には UA が標準の文言を書きます。 |
| MRFC、TS 23.333 6.1.2 と 6.1.3 | MRFC は使えない MRFP を覚えず、戻ったかどうかを Audit で確かめることもしません。6.1.2 は戻るまでその MRFP を使わないよう求めています。要求ごとに Call-ID か会議の ID が選んだ MRFP をまず試し、その MRFP が止まっていれば、Add が失敗してから次の MRFP に移ります。 | MRFP はメモリから答えるので、待つのは失敗した 1 回分だけです。キャプチャには断りと、その次の要求の両方が残ります。 |
| MRFC、TS 23.333 6.1.5.1 と TS 29.333 5.17.3.5 | 強制的に止められた MRFC の台や、ダイアログがターミネーションを取り除く前にプロセスが終わった台は、そのターミネーションを MRFP に残します。止まるよう指示されたノードは、BYE を送らずにダイアログを終えますが、ダイアログがターミネーションを取り除き終わるのを待ちません。同じ名前で起動し直した台も、起動し直したことを ServiceChange で知らせません。残ったターミネーションはポートを開いたまま MRFP にあり、会議を混合するコンテキストのクロックも動き続けます。コンテキストが空にならないので会議は Store に残り、あとから参加した人は、残ったターミネーションと一緒に混合されます。 | ターミネーションを取り除くのは、それを加えたダイアログだけです。ダイアログが終わるときと、MRFC が作ったばかりの会議のコンテキストを手放すときに取り除きます。MRFC はプロセスの外にターミネーションの一覧を持ちません。MRFC の Mp の操作にも、ノードが MRFP の制御に使う HTTP の API にも、起動し直したことを知らせる手段がありません。 |
| netann、RFC 4240 3 | locale、content-type、param1 から param9 は読み飛ばします。 | どの音声ガイダンスも 1 種類しかありません。 |
| MRFP、TS 23.333 5.10、RFC 3550 6 と 7.1 | 混ぜ合わせは PCMU だけを扱い、ジッタのために 3 フレームだけをためます。CSRC の一覧は書かず、RTCP も送りません。 | 1 つのブリッジにつないだ数台の UE の会議には、これで足ります。 |
| MRFP、RFC 3550 6 | 自分で作ったメディアの RTCP の送信者レポートを送りません。 | UE シミュレータには要りません。 |
| MRFP、TS 29.333 5.17.2.9 | 同じ名前で作り直した MRFP は、コンテキストのポートを開き直しますが、その中では何も再生しません。流れていた音声ガイダンスやトーンは、もう一度は始まらず、終わりの通知も MRFC に届きません。自分で作るメディアの RTP の送信元は作り直したときに変わり、ターミネーションが数えたパケットの数も 0 に戻ります。 | ターミネーションが音声ガイダンスのどこまで再生したかは、コンテキストと一緒には残しません。呼の次の音声ガイダンスは MRFC が始めます。会議の音声は、作り直したあとも混ぜ合わせます。 |
| AS、TS 24.182 4.5.5.3.7 | 着信者が 180 のあとに送る offer の付いた UPDATE を、そのまま発信者に渡します。発信者のメディアは呼び出し音から離れます。 | UE シミュレータはそのときに UPDATE を送りません。 |
| AS、TS 24.182 4.5.5.3.7 b 2 と b 3 | プリコンディションの付いた着信者の 183 を、そのまま発信者に渡します。発信者のプリコンディションを確かめずに呼び出し音を始めます。 | 着信者が呼び出しを始めた時点で、両方の資源はそろっています。 |
| AS、TS 24.147 5.3.2.3.1 step 2、TS 24.229 5.7.1.4 と 5.7.1.5 | AS が受け持つ利用者は誰でも会議を作れます。会議のポリシーも課金のアドレスもありません。 | 会議のポリシーのサーバーが無く、このプロジェクトのノードは課金の記録を作りません。 |
| AS、TS 24.147 5.3.2.4.1 step 2、TS 24.229 5.7.1.4 と 5.7.1.5 | フォーカスは INVITE の P-Asserted-Identity を利用者の識別子として受け取ります。作った人について S-CSCF が保証した識別子を受け取るのと同じで、それ以上は何も確かめません。INVITE が信頼ドメインのノードから来たかどうかは確かめません（TS 24.229 5.7.1.4 b i、RFC 3325 8）。空きのある会議には誰でも入れます。P-Asserted-Identity が無く、Privacy に `id` も `user` も無い INVITE には、認証のチャレンジをせずに 403 を返します。 | 会議のポリシーが無く、AS は利用者の認証情報を持ちません。このプロジェクトの網では、フォーカスに届く要求はどれも、P-CSCF か信頼できる相手網が保証した識別子を持っています。 |
| AS、TS 24.229 5.7.3 NOTE 4 | AS は、会議の URI を受け持つ S-CSCF を、作った人の INVITE を届けた S-CSCF とみなし、Sh で HSS に問い合わせません。 | 設定に書く S-CSCF は 1 つで、網のすべての利用者と PSI を受け持つためです。その S-CSCF を何台かで動かすときも、台どうしは 1 つの名前を共有し、状態を 1 つの保存先に置きます。 |
| AS、TS 24.147 5.3.2.6.2.2 | 参加者を外すようフォーカスに頼む REFER（method が BYE）は 403 で断ります。 | IR.92 2.3.3 は求めていません。 |
| AS、RFC 4575 3.9 | 変化があるたびにすぐ通知し、5 秒に 1 回までにまとめません。 | IR.92 2.3.3 は 5 秒以内の通知を求めていて、小さな会議はあまり変わりません。 |
| AS、TS 24.147 5.3.3.2 step 2、RFC 4575 3.5 | 網が識別子を保証した購読者なら誰でも購読できます。 | 会議のポリシーがありません。 |
| HSS、TS 29.228 6.1.2.1 | HSS は PSI ユーザーの状態を持ちません。S-CSCF の名前も、登録の状態も、PSI Activation State も持ちません。そのため、どの S-CSCF の SAR にも答えます。PSI ユーザーは SIP URI の範囲で、iFC を持ちません。 | S-CSCF が PSI のプロファイルを求めるのは、その PSI を受け持つ AS が PSI の代わりに要求を送るときだけです。PSI への要求は DNS で AS に届くので、PSI をどこが受け持つかを HSS に問い合わせるノードはありません。 |
| UE、RFC 3891 3、RFC 3892 | Referred-By に Authenticated Identity Body を付けません。 | 識別子は、REFER を送った人の網がフォーカスに保証しています。 |

## 後片付け

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

すべてのプロファイルのノードと UE を止め、イメージは残します。次の手順書をビルドし直さずに始められます。イメージも削除するときは、[システムを止める](capture.md#システムを止める)を見てください。
