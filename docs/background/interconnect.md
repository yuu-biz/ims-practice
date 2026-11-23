# IMS 網どうしの相互接続の調査（IBCF、TrGW、II-NNI）

この文書は、IMS 網どうしの相互接続について、仕様が定めることと、この実装の作りを調べた記録です。対象は、2 つの網をつなぐ IBCF と TrGW、網をまたぐ通話と SMS、接続先ごとのプロファイルです。要件は [docs/requirements.md](../requirements.md)、ロードマップは [docs/roadmap.md](../roadmap.md)、メディア中継ノードの制御と網の境界の設計は [docs/design/media-control.md](../design/media-control.md) にあります。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは、表「読んだ資料」の版の条項です。

9 章と 10 章は、調べた時点のコードとの差と、そのときの設計の案です。決めた設計と、案との違いは 12 章に、作りは 13 章に書きます。

## 読んだ資料

| 仕様 | 版 | 読んだ範囲 |
| --- | --- | --- |
| TS 23.228（IMS、Stage 2） | V20.1.0 | 4.3.5、4.14、4.18、Annex I、Annex K |
| TS 24.229（SIP と SDP、Stage 3） | V20.1.0 | 4.2A、4.4、5.3.2.1、5.4.3.2、5.10、6.7.1、7.2.11 |
| TS 29.165（II-NNI） | V20.0.0 | 5 章から 9 章、16 章、25 章、Annex C の前半 |
| TS 29.238（Ix の H.248 プロファイル） | V19.0.0 | 1 章と 5 章の表（目次、5.4、5.6.1.1、5.9、5.12、5.14.1、5.17.2.1） |
| TS 23.204（SMS over IP、Stage 2） | V19.1.0 | 5.2、6.0a、6.3、6.3a、6.4、6.4a |
| RFC 3263（SIP サーバーの探し方） | 本文 | 4 章、5 章 |
| RFC 4694（tel URI の番号ポータビリティのパラメータ） | 本文 | 5.1、5.2 |
| RFC 6116（ENUM） | 以前に読んだもの | `internal/sipdns/enum.go` の参照箇所 |

GSMA IR.95 と TTC の文書は、本文を読めませんでした。8 章と「確かめられなかったこと」に書きます。

## 全体の流れ

自網（網 A）の UE-A が、相手網（網 B）の UE-B に電話をかけるときの流れです。括弧の中は根拠の条項です。

1. UE-A の INVITE が P-CSCF を通って網 A の S-CSCF に届きます。Request-URI は UE-B の tel URI です。
2. S-CSCF は ENUM で番号を SIP URI に変えます（TS 24.229 5.4.3.2 step 10）。結果のドメインは網 B のものです。
3. S-CSCF は、宛先が相互接続の機能を要るとき、要求を自網の IBCF 経由で送ります。網の構成を隠すときは、IBCF の URI を Route の先頭に置きます（5.4.3.2 step 11、12）。
4. 網 A の IBCF は出口（exit point）として働きます（5.10.2.2）。自分の URI を Route から外し、Record-Route に自分を入れ、P-Charging-Function-Addresses を消し、網の構成を隠し（5.10.4）、信頼できない相手なら P-Asserted-Identity などを消します（4.4）。SDP のアドレスを TrGW のものに書き換えます（5.10.5、6.7.1）。
5. 網 A の IBCF は、網 B の入口を RFC 3263 か設定で見つけて送ります（5.10.2.2 NOTE 3）。IBCF どうしのあいだが Ici、TrGW どうしのあいだが Izi で、この 2 つを合わせたものが II-NNI です（TS 23.228 Annex K.2、TS 29.165 5.1）。
6. 網 B の IBCF は入口（entry point）として働きます（5.10.3.2）。相手が信頼ドメインの外なら、P-Charging-Vector などを消します。自分の URI を Route から外し、I-CSCF を選んで送ります（step 4）。
7. 網 B の I-CSCF は LIR で S-CSCF を探し、S-CSCF は UE-B の連絡先へ送ります。ここからは今の着信の流れと同じです。
8. 応答は同じ道を戻ります。ダイアログの中の要求は、Record-Route に入った 2 つの IBCF を通ります。
9. RTP は UE-A、網 A の中継ノード（P-CSCF が制御）、網 A の TrGW、網 B の TrGW、網 B の中継ノード、UE-B の順に流れます。

## 1. IBCF と TrGW の役割

### 仕様が求めること

- IBCF の機能は TS 23.228 4.14 にあります。トランスポート層の制御、アドレスの領域が違う網のあいだの橋渡し、コーデックが違う網のあいだの橋渡し、網の構成の秘匿、SIP の信号のスクリーニング、課金の記録の作成、信号の相互接続先の選択です。どれを使うかは事業者が決めます。
- 参照点は TS 23.228 Annex I.2 にあります。Mx は自網の CSCF と IBCF のあいだ、Mm は IBCF とほかの IMS のあいだ、Ici は IBCF とほかの SIP 網のあいだ、Ix は IBCF と TrGW のあいだです。Ix の機能は TS 29.162 が決めます。
- TS 29.165 5.2.1 は、IBCF が IMS-ALG の機能を行うときは B2BUA として動くと書いています。5.2.2 は、TrGW が網の境界のメディアの経路にあり、IBCF に制御され、アドレスとポートの変換と IPv4 と IPv6 の変換を行うと書いています。
- 網の構成の秘匿（THIG）は、Release 5 と Release 6 では I-CSCF の機能でした。今は IBCF の機能です（TS 24.229 5.10.4.1 NOTE 1）。

### II-NNI の通り方の種類

TS 29.165 5.3.1 は、II-NNI の通り方を 3 つに分けています。

| 通り方 | 意味 | この実装での扱い |
| --- | --- | --- |
| non-roaming | 発信側の自網と着信側の自網のあいだ | 作る |
| roaming | 在圏網と自網のあいだ。REGISTER、Path、Service-Route が通る | 作らない。ローミングは対象外（[docs/requirements.md](../requirements.md)） |
| loopback | 自網から在圏網へ呼を戻す（ローカルブレークアウト） | 作らない |

どの通り方かは、`iotl` という SIP URI のパラメータ（RFC 7549）か、実装ごとの方法で見分けます。`iotl` が無いときは non-roaming とみなします（Table 5.3.2.1 NOTE 1）。`iotl` は付けません。

## 2. IBCF の手順（TS 24.229 5.10）

### 出口の IBCF（5.10.2）

初期の要求（5.10.2.2）では、次のことを行います。

- INVITE なら 100 を返す（step 1）。自分の URI を Route の先頭から外す（step 1A）。
- ALG かトランスポート層の制御を行うなら、あとで IBCF からセッションを切れるように、Contact、CSeq、Record-Route を覚える（step 2）。
- あとの要求にも IBCF の機能を当てるなら、Record-Route に自分を入れる（step 2B）。
- P-Charging-Vector の一部か全部を事業者の方針で消し（step 8）、P-Charging-Function-Addresses を消す（step 9）。
- Via の `received-realm` を消す（step 10）。
- RFC 3261 のとおりに転送する。決まった経路が無いときの次の宛先は、RFC 3263 で探すか、IBCF に設定しておく（NOTE 3）。
- セッションが残り続けないように、セッションタイマー（RFC 4028）で更新を求めてよい。
- 305 を除く 3xx を、自分で追いかけない。

ダイアログの中の要求（5.10.2.3）は、Route の先頭に従って転送します。TrGW の異常などを知ったときは、覚えておいた情報から両側に BYE を送れます（5.10.2.4）。

REGISTER の手順（5.10.2.1）は roaming の通り方だけで使うので、作りません。

### 入口の IBCF（5.10.3）

初期の要求（5.10.3.2）では、まず要求が信頼ドメインから来たかを確かめます。信頼ドメインの外から来たときは、次のようにします。

- Route の先頭に `orig` があれば 403 を返す。
- P-Charging-Vector、P-Charging-Function-Addresses、Feature-Caps を消す。

そのあとの手順は次のとおりです。

- INVITE なら 100 を返す（step 1）。自分の URI を Route から外す（step 1C）。
- 出口と同じく、必要ならダイアログの情報を覚え（step 2）、Record-Route に自分を入れる（step 2B）。
- Route が自分だけを指していたら、I-CSCF を選んで送る（step 4）。I-CSCF は同じ場所にあるもの、設定したもの、選んだもののどれかです。
- 事業者の方針で、隣の網を示す `received-realm` を Via に入れる（step 6）。

### 網の構成の秘匿（5.10.4）

- 対象は、構成が分かるヘッダです。Via、Route、Record-Route、Service-Route、Path が挙がっています（5.10.4.1）。
- 方法は暗号化です（5.10.4.2）。隠す網が加えたヘッダの値を、順番を変えずに暗号化します。続いている値は 1 つの文字列にまとめます。暗号化した文字列から、どの網が暗号化したかが分かるホスト名を作り、`tokenized-by` というパラメータに網の名前を入れます。Via や Route の値を暗号化したときは、その手前に IBCF 自身の値を入れます。
- 戻ってきたメッセージでは、自網が暗号化した値を見つけて元に戻します（5.10.4.3）。応答を自網の中で正しく届けるためです。
- 要求と応答を別々に処理できるので、IBCF は状態を持たずに済みます（5.10.4.1 NOTE 2）。
- 初期の要求に Record-Route があれば、IBCF は自分の URI を Record-Route の先頭に入れます（5.10.4.1）。
- 暗号の方式は決まっていません。作った IBCF だけが読めればよいためです（5.10.4.2 NOTE 2）。

5.10.4 が挙げるヘッダに、Contact と SDP は入っていません。この 2 つは次の IMS-ALG とプライバシーの機能が扱います。

### IMS-ALG（5.10.5、6.7.1）

- IMS-ALG の機能を行うとき、IBCF は B2BUA として動き、2 つのダイアログのあいだでヘッダを写します（5.10.5）。
- Contact は、GRUU でなければ IBCF 自身の URI に置き換えてよく、GRUU や、相手がその URI を使う機能タグ（`isfocus` など）があるときは、そのまま通します。そのまま通すときは、Record-Route に自分を入れます。
- TrGW でアドレスやポートを変えるとき、IBCF は TrGW から受けたアドレスとポートで、SDP の offer と answer を書き換えます（6.7.1.4.1）。
- コーデックの変換の制御は 5.10.7 と 6.7.1.3 にあります。事業者の方針と信号の中身から、offer のどのメディアを変換するかを決めます。
- ローミングの境界の B2BUA は、Call-ID と From と To のタグを変えてはいけません（5.10.1）。non-roaming の境界には、この決まりがありません。

### スクリーニング（5.10.6）

- IBCF は、事業者の方針で、受けたヘッダを消したり書き換えたりできます（5.10.6.2）。ただし Authorization と WWW-Authenticate は変えないほうがよく、登録の経路にあるときは Path と Service-Route も変えないほうがよいとされています。
- 本文も調べられます（5.10.6.3）。長さ、Content-Type などの性質、中身を見て、通す、消す、要求を断るのどれかを選びます。`handling=required` の本文を消すことになるときは、415 で断ります。
- 多くのヘッダは端から端まで運ぶものなので、消すと利用者のあいだの動作に影響します（5.10.6.1 NOTE）。構成の秘匿で目的を果たせるなら、そちらを使うほうがよいとされています（5.10.6.2 NOTE 2）。

接続先ごとのプロファイルと、プロファイルを食い違わせたシナリオは、このスクリーニングの上に作ります。

### プライバシー（5.10.8）

信頼ドメインの外へ送る前に、IBCF は RFC 3323 と RFC 7044 のプライバシーの処理を行います。Privacy が `header` なら、Via を消して自分の Via だけにし、Contact を発信者にたどり着かない URI に置き換え、Record-Route を消します。`user` なら From を匿名にします。ダイアログを見分けるヘッダを変えたときは、透過的な B2BUA として動きます。構成の秘匿と食い違うときは、構成の秘匿が先です。

OIR（発信者番号の非通知）を作るので、網をまたぐ非通知の呼がここを通ります。

## 3. II-NNI のプロファイル（TS 29.165）

TS 29.165 は、II-NNI で何を通すかを決めた文書です。TS 24.229 の Annex A の表をもとに、境界で使うものを選び直しています（6.1.1.1）。運用の中身は、多くを事業者のあいだの取り決めに任せています。

### メソッド（6.1.1.2、Table 6.1）

| 区分 | メソッド |
| --- | --- |
| 必須 | ACK、BYE、CANCEL、INVITE、OPTIONS、PRACK、UPDATE |
| 任意（取り決めによる） | INFO、MESSAGE、REFER |
| roaming では必須、それ以外は任意 | NOTIFY、PUBLISH、SUBSCRIBE |
| roaming では必須、それ以外は使わない | REGISTER |

表の「必須」は、境界の装置がそのメソッドを通せることを意味します（Table 6.3）。網の中の装置や UE が対応することまでは求めていません。使えないとされたメッセージは、IBCF が捨てます。

SMS を SIP で網のあいだに通すなら、任意の MESSAGE を取り決めで使うことになります。

### ヘッダ（6.1.1.3）

- 使えるヘッダは、TS 24.229 Annex A のメソッドごとの表から導きます（6.1.1.3.2）。Annex A に無いヘッダは使えません。Annex A で任意のヘッダは、取り決めがあれば使えます。
- roaming の境界だけで使うヘッダが決まっています（6.1.1.3.3）。Authorization、Path、Service-Route、P-Associated-URI、P-Called-Party-ID、P-Visited-Network-ID、P-Served-User、WWW-Authenticate などです。non-roaming の境界には現れません。
- non-roaming の境界だけで使うヘッダは、Identity、Attestation-Info、Origination-Id、Priority-Verstat、P-Refused-URI-List です（6.1.1.3.4）。
- Annex A にヘッダの一覧のまとめがあり、Annex B にメソッドごとの表があります。この 2 つは参考の扱いです。

今の S-CSCF は、着信側で P-Called-Party-ID を入れます（`internal/scscf/session.go` の `routeTerminating`）。これは着信側の網の中だけで付くので、境界には現れません。

### 主な機能の表（6.1.3、Table 6.1.3.1）

Table 6.1.3.1 は、RFC ごとに境界での扱いを決めています。相互接続に関わる行を抜き出します。番号は表の項目の番号です。

| 項目 | 機能 | 境界での扱い |
| --- | --- | --- |
| 2、3 | セッションの開始と終了 | 必須 |
| 18 | PRACK と 100rel（RFC 3262） | 必須 |
| 20 | プリコンディション（RFC 3312、RFC 4032） | 任意 |
| 21 | UPDATE（RFC 3311） | 必須 |
| 23 | SUBSCRIBE と NOTIFY（RFC 6665） | roaming では必須、それ以外は任意 |
| 25、27 | P-Asserted-Identity（RFC 3325） | 信頼関係があれば必須、無ければ使わない |
| 26 | P-Preferred-Identity | 使わない |
| 28 | Privacy（RFC 3323 など） | 必須 |
| 29 | MESSAGE（RFC 3428） | 任意 |
| 36 | P-Access-Network-Info | 信頼関係があれば必須、無ければ使わない |
| 37 | P-Charging-Function-Addresses | 使わない |
| 38 | P-Charging-Vector | roaming では必須、それ以外は任意 |
| 40 | Reason（RFC 3326） | 任意 |
| 43 | caller preferences（RFC 3841） | 必須 |
| 45 | セッションタイマー（RFC 4028） | 必須 |
| 50 | History-Info（RFC 7044） | 任意 |
| 69 | P-Early-Media（RFC 5009） | 信頼関係があれば必須、無ければ使わない |
| 70 | tel URI の番号ポータビリティのパラメータ（RFC 4694） | 任意 |
| 84 | 199 応答（RFC 6228） | 必須 |
| 96 | Record-Route の 2 重の記入（RFC 5658） | 任意 |
| 112 | `iotl`（RFC 7549） | 任意 |

プリコンディションが任意で、100rel が必須という組み合わせは、プロファイルを食い違わせたシナリオの題材になります。片方の網がプリコンディションを必須（Require）にし、もう片方が対応しないと、呼は 420 で失敗します。

### option tag

TS 29.165 には、option tag だけをまとめた表はありません。扱いは 2 つの形で決まっています。

- Require、Supported、Unsupported の扱いそのものは必須です（Table 6.1.3.1 の項目 11、12）。
- 個々の tag は、上の表の RFC の行で決まります。たとえば `100rel` は項目 18、`precondition` は項目 20、`timer` は項目 45 です。補足サービスの章にも個別の記述があります（`histinfo`、`early-session`、`from-change`、`replaces`、`tdialog` など）。

接続先ごとに何を選んだかを書き出すための表が Annex C にあります。「選択項目（option item）」と呼び、事業者どうしが条件を書き込んで交換する使い方を想定しています（C.1、C.2）。プリコンディション、セッションタイマーの条件、音声のコーデックの名前、DTMF の送り方、MESSAGE を使うかどうか、などが項目です（C.3.1）。接続先ごとのプロファイルの設定の形は、この表をなぞると標準との対応が分かりやすくなります。

### 本文（6.1.4）

- `application/sdp` と multipart の本文は必須です。ほかの MIME タイプは取り決めによります。
- INVITE の SDP は、境界では暗号化しません。
- INVITE には SDP があるのが原則です。SDP の無い INVITE は、第三者呼制御のようにサービスに要るときだけです（NOTE 4）。
- Table 6.1.4.1 の MIME タイプの一覧には、`application/vnd.3gpp.sms`（項目 22）と `application/3gpp-ims+xml`（項目 1）があります。

### SDP とメディア（6.1.2、7 章）

- offer と answer は RFC 3264 のとおりです（6.1.2.1）。
- コーデックは取り決めで決めます（7.1）。端から端までの交渉が、共通のコーデックが無くて失敗することがあるので、IBCF や MRFC がコーデックを足したり外したりできます。コーデックの変換を何度も重ねないように、取り決めでコーデックを絞りすぎないことを勧めています。
- DTMF は RFC 4733 の telephone-event で運びます（7.1 NOTE 2）。
- メディアのトランスポートは、RTP（RFC 3550）、UDP、RTP/AVP（RFC 3551）、RTCP の帯域の指定（RFC 3556）が必須です（Table 7.2.1）。RTP/AVPF と TCP は任意です。

### トランスポートとタイマー（6.2、6.3）

- 信号のトランスポートは TS 24.229 4.2A に従います（6.2.1）。4.2A は RFC 3261 18 章のとおりで、1300 バイトを超えるメッセージは RFC 3261 18.1.1 の手順（TCP などへの切り替え）で送ります。SCTP は任意です。
- SIP のタイマーの推奨値は Table 6.3.1 にあります。T1 は 500 ミリ秒、T2 は 4 秒、T4 は 5 秒で、TS 24.229 Table 7.7.1 の網の中の値と同じです。

### 番号と名前（8 章）

- non-roaming の境界では SIP URI が必須で、tel URI は取り決めによります（8.1）。
- Request-URI と P-Asserted-Identity の番号は、グローバルな番号（国際形式の E.164）にします。tel URI か、`user=phone` を付けた SIP URI の user part に入れます。それ以外の番号は、取り決めがあるときだけです。
- 国際形式にしておくと、着信側の網は番号を変換せずに HSS で加入者を探せます（8.1 NOTE 5）。
- `rn` と `npdi`（RFC 4694）は、取り決めがあれば Request-URI に入れて渡せます。国ごとの番号ポータビリティの決まりによっては、`rn` で着信側の網の入口を指します（8.1 の NOTE）。

### そのほか

- IP の版は取り決めによります（9 章）。IPv4 だけにします。
- 補足サービスごとの境界の要件が 12 章にあります。OIP と OIR（12.3）、CDIV（12.6）、HOLD（12.8）、CAT（12.14）、会議（12.19）、音声ガイダンス（12.21）などです。補足サービスと MRF のサービスが網をまたぐときに読みます。この調査では題名だけを確かめました。
- P-CSCF の復旧（25 章）は roaming の境界の話なので、扱いません。

## 4. 信頼ドメイン

- 信頼ドメインは、同じ事業者の網の機能（P-CSCF、I-CSCF、S-CSCF、IBCF、AS など）からなります（TS 24.229 4.4.1）。相互接続の合意がある相手の網の機能は、信頼ドメインに入ります。合意が無ければ入りません。どちらにするかは、機能ごとに設定しておく事業者の方針です。
- 信頼ドメインはヘッダごとに決まり、境界が同じとは限りません。対象は P-Asserted-Identity、P-Access-Network-Info、History-Info、Resource-Priority、P-Asserted-Service、応答の Reason、P-Early-Media、Feature-Caps、Server、User-Agent などです（4.4.1 の最後の段落）。
- P-Asserted-Identity は、信頼ドメインの境界で RFC 3325 に従って消すかどうかを決めます（4.4.2）。Privacy の `id` は、境界を越えるときも消しません。
- TS 29.165 Table 6.2 は、信頼関係があるときと無いときの扱いを、ヘッダごとに並べています。P-Asserted-Identity などは TS 24.229 4.4 に、P-Charging-Vector は 5.10 に従います。

この実装では、接続先のプロファイルに「信頼する」「信頼しない」の 2 つの値を持たせれば、4.4 と 5.10.3.2 の手順の分かれ目を再現できます。信頼しない相手との呼では、着信側の UE に発信者番号が届かないことを、キャプチャで確かめられます。

## 5. ENUM と番号ポータビリティ

### 仕様が求めること

- S-CSCF は、tel URI の国際形式の番号を ENUM かほかのデータベースで SIP URI に変えます（TS 24.229 5.4.3.2 step 10、TS 23.228 4.3.5.2）。変換できたら Request-URI を置き換えます。
- ENUM の最上位のドメインは `e164.arpa` に限りません。事業者が設定できるようにします（TS 23.228 4.3.5.1）。今の `internal/sipdns` の `ENUM` も suffix を引数に取ります。
- ENUM のデータベースは、番号ポータビリティ（NP）の情報を持てます（TS 23.228 4.3.5.1、4.18.1）。NP を使うかどうかと、情報の取り方は事業者の方針で、仕様は必須にしていません。
- S-CSCF が NP に対応していて、SIP URI への変換に失敗したが NP の情報は取れたときは、Request-URI に RFC 4694 のパラメータを足します（5.4.3.2 step 10 の後半）。`npdi` は NP のデータベースを引いたことを示し、番号が移転済みなら `rn` に移転先のルーティング番号を入れます。そのあと BGCF などに渡します。
- RFC 4694 5.1 によれば、`npdi` がある tel URI を受けたノードは、NP のデータベースをもう一度引きません。経路を決めるときは `cic`、`rn`、番号の順に見ます。
- 入口の側では、I-CSCF が、`+` で始まる user part と `user=phone` を持つ SIP URI を tel URI に置き換えてから HSS に問い合わせます（TS 24.229 5.3.2.1 の b）。HSS が利用者を知らないときの NP の扱いも同じ条項にあります。

### この実装での使い方

国境の経路の決め方は 2 通りあります。

| 方式 | 動き | 見えるもの |
| --- | --- | --- |
| ENUM が SIP URI を返す | 番号ごとの NAPTR が、今その番号を持つ網のドメインの SIP URI を返す。移転した番号は、レコードを書き換えるだけで移転先へ届く | ENUM の問い合わせと、Request-URI のドメイン |
| `rn` と `npdi` で渡す | ENUM（か別のデータベース）が移転先のルーティング番号を返し、S-CSCF が `tel:+...;npdi;rn=...` にして IBCF に渡す。IBCF が `rn` から接続先を選ぶ | Request-URI の `rn` と `npdi`、IBCF の経路の表 |

1 つ目は今の `sipdns.ENUM` のままで動きます。2 つ目は、ENUM の結果が tel URI のときの扱い（RFC 4769 の PSTN の Enumservice を TS 24.229 が例に挙げています）と、`rn` で経路を選ぶ部品が要ります。BGCF を作らない今の範囲では、2 つ目は IBCF の経路の表で代わりにすることになります。

## 6. Ix（TS 29.238）

Ix は IBCF が TrGW を制御する参照点です。H.248 の実装は別に扱うので、ここでは独自の HTTP の API（[docs/design/media-control.md](../design/media-control.md)）が何に対応するかだけを確かめました。

- TS 29.238 は、H.248 を Ix に使うときのプロファイルです（1 章）。段階 2 の内容は TS 23.228 Annex I と TS 29.162 にあります。
- 1 つのコンテキストに入るのは IP のターミネーション 2 つです（Table 5.4.1）。3 つになるのは、アクセスの切り替えの途中などの一時的な場合だけです。今の `internal/agw` のコンテキスト（`access` と `core` の 2 つのターミネーション）と同じ形です。
- ターミネーションの名前は `ip/<group>/<interface>/<id>` の形です（5.6.1.1.1）。
- 符号化はテキストと 2 進の両方を挙げています（Table 5.9.1）。トランスポートは SCTP を勧め、UDP は任意です（Table 5.12.1）。
- 必須のパッケージは、Generic、Base root、RTCP の扱い（rtcph）、送信元のアドレスとポートで通すかを決めるゲート（gm）の 4 つです（Table 5.14.1.1）。
- 呼に関わる手順は、接続点の予約（Reserve TrGW Connection Point）、設定（Configure）、予約と設定を同時に行うもの、ターミネーションの解放が必須です（Table 5.17.2.1.1）。今の API の `Add` が予約、`Modify` が設定、`Subtract` が解放に当たります。
- 呼に関わらない手順に、TrGW の停止と復旧、IBCF の停止、Audit があります（5.17.3 の目次）。冗長構成と H.248 の題材です。
- 目次には、IP の領域（IP realm）を扱うパッケージ（ipdc、ipra）があります。TrGW は 2 つの網にまたがるので、ターミネーションごとに「どちらの網の側か」を指定する必要があります。中身は読んでいません。

## 7. 網をまたぐ SMS

### 仕様が求めること

SMS が網をまたぐ道は、仕様の上で 2 つあります。

1. SC と SMS-GMSC を通る道（TS 23.204 6.3、6.4）。送信側の IP-SM-GW は SM を SC に渡します。SC は SMS-GMSC に渡し、SMS-GMSC は受信者の HSS に経路を問い合わせます（C か S6c、5.2.2）。HSS は問い合わせを受信者の IP-SM-GW に回し、IP-SM-GW は自分のアドレスを返します。SMS-GMSC は SM を受信者の IP-SM-GW に届けます（E、Gd、Gdd、SGd のどれか、5.2.4）。受信者の IP-SM-GW は、SIP の MESSAGE で UE に届けます。この道では、網のあいだを通るのは MAP か Diameter（S6c と SGd）で、SIP の II-NNI は通りません。
2. IMS のあいだを SIP で通る道（6.0a、6.3a、6.4a）。MSISDN を持たない UE のための手順です。送信側の IP-SM-GW が、まず着信側の IMS 網へ直接届けようとします（6.0a.1）。着信側の網の IP-SM-GW が受け、UE-B に届けます（6.4a）。届けられなかったときは、あとで再送できるように、相関のための ID（MSISDN-less correlation ID）を返します。受信者は SIP URI で指します。プロトコルの上でどう指すかは Stage 3 に任されています（6.3a.1 の NOTE）。

II-NNI の側では、MESSAGE は取り決めがあれば使え（TS 29.165 Table 6.1、Table 6.1.3.1 の項目 29）、`application/vnd.3gpp.sms` は本文の一覧にあります（Table 6.1.4.1 の項目 22）。本文を運ぶ MESSAGE と、応答の 202 と 415 は、境界で通せなければなりません（16.2）。

### 今の実装との関係

今の SC はスタブで、受信者の番号から Sh の UDR で IMPU を引きます（[docs/design/sms.md](../design/sms.md)、`internal/sc/sc.go`）。Sh で引けるのは自網の HSS の加入者だけなので、相手網の番号は「利用者がいない」として RP-ERROR になります。

SGd と S6c は、`internal/sgd/` に部品があり、SMS over NAS で使います。網をまたぐ SMS の道としては使っていません。設計の候補は 10 章に書きます。

## 8. GSMA IR.95 と国内標準（TTC）

### GSMA IR.95

ロードマップ（[docs/roadmap.md](../roadmap.md)）は、手順書に IR.95 との対応を載せるとしています。IR.95 は GSMA の PRD で、題名は「SIP-SDP Inter-IMS NNI Profile」として知られています。この環境から www.gsma.com は 403 を返したので、本文も版も確かめられませんでした。題名も、今回は GSMA のページで確かめていません。題名から分かるのは、IMS の網のあいだの SIP と SDP のプロファイルであることだけです。TS 29.165 のどの選択項目をどう絞っているかは、本文を読んでから書きます。

IR.92 と NG.114 は、入手した版と入手先が [profiles.md](profiles.md) に記録されています。IR.95 は、同じ入手の方法を 1 回試しただけで、本文を読めていません。

### TTC

TTC の文書は、[profiles.md](profiles.md) の「国内標準（TTC）」に記録があります。そこでの確認は Internet Archive の写しによるものです。今回 ttc.or.jp は 503 を返したので、本文を読み直せませんでした。次の表の「分かっていること」は、[profiles.md](profiles.md) の記録を写したもので、今回は確かめていません。

| 番号 | 題名 | 分かっていること | 相互接続との関係 |
| --- | --- | --- | --- |
| JJ-90.30 | IMS 事業者網間の相互接続共通インタフェース | TS 29.165 の II-NNI を国内向けに補う。SIP は UDP の 5060 番が必須。100rel は必須で、プリコンディションは事業者間で決める。セッションタイマーを全セッションに掛け、値は 180 秒から 300 秒。発信者番号は P-Asserted-Identity で運び、tel URI の PAI は 1 つ。網間の offer に G.711 μ-law を必ず入れ、移動網どうしでは AMR を必ず入れる | 接続先のプロファイルの「日本向け」の例になる |
| JJ-90.31 | キャリア ENUM の相互接続共通インタフェース | 題名だけ | 事業者のあいだで ENUM を引く方式。5 章の経路の決め方に関わる |
| JJ-90.32 | SIP ドメイン解決のための DNS 相互接続共通インタフェース | 題名だけ | 相手網の入口を DNS で探す方式。IBCF の次の宛先の探し方に関わる |
| JJ-90.27 | 着信転送サービス（CDIV）に関する NNI 仕様 | 題名だけ | CDIV が網をまたぐとき |
| JJ-90.28 | 緊急通報呼に関する NNI 仕様 | 題名だけ | 緊急呼は拡張の候補なので、今は関係しない |
| JJ-90.26 | IMS における SDP メディアネゴシエーション手順 | 題名だけ | コーデックの交渉 |

JJ-90.30 の記録と TS 29.165 を並べると、次の点が「3GPP の共通の部分」と「国ごとの違い」の境目になります。

- セッションタイマー。TS 29.165 は RFC 4028 への対応を必須にするだけで、値は選択項目です。JJ-90.30 は値の範囲を決め、全セッションに掛けます。
- プリコンディション。どちらも任意です。JJ-90.30 は事業者のあいだで決めるとしています。
- コーデック。TS 29.165 は取り決めに任せます。JJ-90.30 は必ず入れるコーデックを決めています。
- 発信者番号。TS 29.165 は信頼関係があれば P-Asserted-Identity を必須にします。JJ-90.30 は tel URI の数まで決めています。

## 9. 今のコードとの差

### 2 つ目の IMS ドメイン

- 設定はノードごとに `home_domain` を持ち、役割はそこから名前を作ります（`internal/config/config.go`）。HSS の加入者も設定にあります。同じイメージに別の設定を渡せば、別のドメインの IMS が動きます。コードの変更は要りません。
- `compose.yaml` は、1 つのブリッジ `ims0`（172.28.0.0/24）に全部のノードを置き、アドレスを固定しています。DNS は CoreDNS が 1 台で、自網のゾーンと `e164.arpa` を持ちます（`deploy/compose/dns/`）。
- キャプチャは `ims0` を 1 つの pcap に書きます（ブリッジ全体を 1 つのコンテナで取る。`images/capture/capture.sh`）。インタフェースの名前は環境変数で変えられますが、ファイル名の先頭は `ims-` に決まっています。
- 試験用の PLMN は MCC 001、MNC 01 です。相手網には MCC 001、MNC 02 を使えます。ドメインは `ims.mnc002.mcc001.3gppnetwork.org` になります。番号は 555-0100 から 555-0199 の範囲を 2 つの網で分けます。

### S-CSCF から外へ出る道

- `internal/scscf/session.go` の `routeOriginating` は、ENUM のあと、Request-URI を DNS で引いて直接送ります（`forward`、`ims.Destination`）。宛先が自網かどうかを見ていません。IBCF を Route に入れる手順（TS 24.229 5.4.3.2 step 11、12）がありません。
- ENUM の suffix は `scscf.Config.ENUMSuffix` で変えられますが、設定ファイルの項目がありません（`internal/config/roles.go` の `rawSCSCF`、`cmd/ims/main.go`）。事業者のあいだの ENUM を別の suffix で引くには、項目を足す必要があります。
- ENUM に失敗すると 404 を返します。NP のパラメータは扱いません。

### 入口の側

- `internal/icscf/session.go` は、Request-URI を `ims.CanonicalURI` で正規化して LIR を送ります。`user=phone` の SIP URI を tel URI に変える手順（TS 24.229 5.3.2.1 の b）がありません。相手網が `sip:+12025550151@<ドメイン>;user=phone` の形で送ってくると、HSS は利用者を見つけられません。
- I-CSCF は入口として、誰からの要求でも受けます。信頼ドメインの確認は IBCF の仕事なので、IBCF を置くなら、I-CSCF を相互接続のブリッジに出さない構成にします。

### 構成が外に見える所

相手網の pcap に自網の内部のアドレスを出さないために、今のメッセージのどこに内部の名前やアドレスがあるかを調べました。

| 場所 | 今の中身 | 隠す機能 |
| --- | --- | --- |
| Via | P-CSCF、S-CSCF などのホスト名（`sip.Config.SentBy`）と、`received` の IP アドレス | THIG（5.10.4） |
| Record-Route | P-CSCF と S-CSCF の URI（`ownURI`） | THIG（5.10.4） |
| Contact | UE の IP アドレスとポート | IMS-ALG（5.10.5）。IBCF の URI に置き換える |
| SDP の `c=` と `o=` | 自網の中継ノードのアドレス（`sdp.SetAudio` が両方を書き換える） | TrGW（6.7.1.4.1） |
| P-Charging-Vector | `icid-value` と `orig-ioi`（自網のドメイン） | 5.10.2.2 step 8、5.10.3.2。`orig-ioi` は網の名前で、内部の構成ではない |
| P-Asserted-Identity | 加入者の IMPU | 信頼ドメイン（4.4.2）。内部の構成ではない |
| Call-ID | 乱数（`sip.NewCallID`）。アドレスは入らない | なし |
| RTP の送信元 | 自網の中継ノードのアドレス | TrGW |

VoLTE の手順で加わる P-Access-Network-Info、User-Agent、Server と、補足サービスで加わる History-Info と P-Served-User も、同じ表で扱う必要があります。

### 中継ノード（TrGW）

- `internal/agw` のコンテキストは `access` と `core` の 2 つのターミネーションを持ちます。SDP に書くアドレスは、中継ノードに 1 つだけです（`config.AGW.RTPAddress`）。ソケットは全インタフェースで待ち受けます（`relay.go` の `listen`）。
- TrGW は 2 つの網にまたがるので、ターミネーションの側ごとに別のアドレスを SDP に書く必要があります。
- 制御は `pcscf.Relay` のインタフェースと `agw.Client` です。SDP を書き換える処理（`internal/pcscf/session.go` の `media`、`advertise`、`endMedia`）は P-CSCF の中にあります。IBCF も同じことをするので、共通の部品にする必要があります。
- コーデックを SDP から外す機能（ロードマップ）はありません。`internal/sdp` には、`m=` 行のコーデックの一覧を読む `Codecs` と、書き換える `SetCodecs` があります（`codec.go`、`audio.go`）。外す処理は、この上に作れます。
- コンテキストの ID は `c1`、`c2` のように、中継ノードごとに 1 から数えます。IBCF が複数の TrGW を使い分けるなら、どの TrGW の ID かを一緒に覚える必要があります（[redundancy.md](redundancy.md) の 5 章と同じ問題です）。

### SIP の基盤

- `cmd/ims/main.go` の `startSIP` は、役割ごとに 1 つのスタックを作り、Via に書く名前は 1 つです。IBCF は内側と外側で別の名前を見せる必要があります。
- B2BUA の部品は、補足サービスの作業で入ります。
- SMS の MESSAGE は、P-CSCF と S-CSCF がプロキシとして通します。IBCF も同じ形で通せます。

## 10. 設計の候補

### 構成

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| 役割 | `ims` コマンドに役割 `ibcf` を足す（役割は設定で切り替える）。TrGW は役割 `agw` のまま、設定で網の境界に置く。1 台の IBCF が出口と入口の両方を行う（5.10.1） | 出口用と入口用で役割を分ける |
| Compose のネットワーク | ブリッジを 3 つにする。自網の `ims0`、相手網の `ims1`、網のあいだの `nni0`。IBCF と TrGW だけが 2 つのブリッジにつながる。キャプチャも 3 つ取る | ブリッジは 1 つのままにし、アドレスの範囲で網を分ける |
| 相手網のノード | 同じイメージで、`deploy/compose/peer/` の設定を使うサービスを足す（`peer-hss`、`peer-icscf` など）。Compose のプロファイル `peer` に入れ、既存の e2e には影響させない | 別の Compose ファイルにする |
| DNS | 網ごとに CoreDNS を置く。`nni0` にも 1 台置き、相手網の入口（IBCF）の名前と、事業者のあいだの ENUM だけを持たせる | 1 台の CoreDNS に全部のゾーンを置く |

ブリッジを 3 つにする案の理由は 3 つあります。

- 完了条件の「相手網で取った pcap に自網の内部のアドレスが現れない」を、pcap を分けて確かめられます。1 つのブリッジでは、フィルタで分けるしかありません。
- 内部のノードどうしが網をまたいで直接届かなくなります。IBCF を通さない経路が誤って動くことがありません。
- DNS の見え方が網ごとに違う様子を学べます。網のあいだの DNS は、TTC の JJ-90.31 と JJ-90.32 が扱う領域です。

弱みは、キャプチャのコンテナとファイルが増えることと、IBCF と TrGW が 2 つのインタフェースを持つことです。ホスト固有のネットワーク設定に依らない、という方針には反しません。Kubernetes での分け方は、Kubernetes の構成で決めます。

### IBCF の動作のモード

| 案 | 動き | 強み | 弱み |
| --- | --- | --- | --- |
| A. プロキシと THIG の暗号化 | 今の P-CSCF と同じく、プロキシとして要求を通す。Via、Record-Route を 5.10.4.2 のとおりに暗号化し、戻りで復号する。Contact は暗号化した文字列を user part に持つ IBCF の URI に置き換える。SDP は `ProxyWith` のフックで書き換える | 状態を持たない（5.10.4.1 NOTE 2。各役割を複数台で動かせるようにする方針にも合う）。どの IBCF の台でも復号できる。`tokenized-by` の付いた値がキャプチャに見え、仕様の例と見比べられる。Call-ID が変わらないので、網をまたいで呼を追いやすい | 5.10.5 は IMS-ALG を B2BUA としている。プロキシのままで SDP と Contact を書き換えるのは、その簡略化になる。IBCF から BYE を送る手順（5.10.2.4）には、ダイアログの情報を別に覚える必要がある |
| B. B2BUA | 内側と外側で別のダイアログを持つ。外側には IBCF の Via と Contact しか出ない | 5.10.5 のとおり。隠し忘れが起きにくい。MMTel AS の B2BUA の部品を使える | ダイアログの状態を `Store` の後ろに置く必要がある（複数台で動かせるようにするため）。5.10.4 の暗号化の手順が見えなくなる。コードが大きい |
| C. A を基本にし、スクリーニングで本文やヘッダを大きく変えるときだけ B | 仕様の書き方に最も近い | 2 つのモードを作ることになる |

案は A でした。理由は次のとおりです。

- 今の P-CSCF が同じ形（プロキシのまま SDP を書き換える）なので、部品を共有できます。
- 状態を持たないので、IBCF を複数台にするときに、外部の保存先が要りません。
- 5.10.4 の暗号化は、学習者がキャプチャで見て仕様と照らせる手順です。B ではこれが消えます。

A を選ぶときは、次の `SIMPLIFIED:` を書きます。

- TS 24.229 5.10.5。IMS-ALG を B2BUA にせず、プロキシのまま Contact と SDP を書き換える。
- TS 24.229 5.10.2.4。TrGW の異常のときに IBCF から BYE を送らない（P-CSCF に入るダイアログの `Store` と同じ仕組みを、足せます）。
- TS 24.229 5.10.8。Privacy が `header` のときの Via の扱いは、THIG の暗号化で代える。

暗号の方式は仕様に無いので、このプロジェクトで決めます。候補は、鍵を設定に置く AES-GCM と、結果を base32 にした文字列です。鍵は同じ網の IBCF の全台で同じものを使います。試験用の鍵だけをリポジトリに置きます。

### 内側と外側の名前

IBCF は、内側には `ibcf.<自網のドメイン>`、外側には別の名前（たとえば `ibcf.nni.example` のような、`nni0` の DNS にある名前）を見せます。

| 案 | 内容 |
| --- | --- |
| 案。スタックを 2 つ持つ | 内側のアドレスで待つスタックと、外側のアドレスで待つスタックを `startSIP` で 2 つ作る。Via の名前がスタックごとに決まる。Record-Route には 2 つの URI を入れる（RFC 5658。TS 29.165 Table 6.1.3.1 の項目 96 で任意） |
| スタックは 1 つ | 送り先ごとに Via の名前を変える。`internal/sip` のスタックの変更が大きい |

設定は、`ibcf` の節に `sip`（内側）と `external_sip`（外側）を持たせます。`config.checkPorts` は、待ち受けのアドレスが違えば同じポートを許すように直します。

### 接続先のプロファイル

設定の形は、TS 29.165 Annex C の選択項目をなぞります。接続先は、相手の入口のアドレスかドメインで見分けます。

```yaml
ibcf:
  peers:
    - name: peer-b
      domain: ims.mnc002.mcc001.3gppnetwork.org
      next_hop: sip:ibcf.peer-b.nni.example   # 無ければ RFC 3263 で探す
      sources: [172.28.2.20]                   # 入口で相手を見分ける
      profile: default
  profiles:
    default:
      trust: true                # TS 24.229 4.4。false なら PAI などを消す
      topology_hiding: true      # 5.10.4
      transport: udp             # udp か tcp
      number_format: global      # 8.1。tel URI か user=phone の SIP URI
      methods: {message: true, refer: false, info: false}
      option_tags:
        precondition: pass       # pass、remove、reject
        100rel: pass
      session_timer: {min_se: 90, session_expires: 0}   # 0 は変えない
      codecs: [AMR-WB, AMR, PCMU, telephone-event]      # これ以外を SDP から外す
      headers:
        remove: [P-Access-Network-Info, User-Agent, Server]
```

- 項目は、相互接続のシナリオが使うものだけにします。上の例は、ロードマップが挙げる 100rel とプリコンディション、トランスポート、コーデック、番号の形式に対応します。
- 「日本向け」のプロファイルは、8 章の JJ-90.30 の値（セッションタイマー 180 秒から 300 秒、G.711 を必ず入れる、UDP）を入れた例として用意できます。本文を確かめていないので、設定の例には、確かめていないことを書きます。
- 国ごとの振る舞いをコードで足す仕組み（プラグインなど）は作りません。国ごとの振る舞いを加えられればよいので、プロファイルの項目を足せる形にしておけば足ります。

食い違いのシナリオの候補は次のとおりです。

| シナリオ | 食い違い | 起きること | 直し方 |
| --- | --- | --- | --- |
| プリコンディション | 網 A の UE が `Require: precondition` で発信し、網 B が対応しない | 420（Bad Extension）で失敗する | IBCF のプロファイルで `precondition` を Require から Supported に落とす。または網 A の UE の設定を変える |
| コーデック | 網 A の UE が AMR-WB だけを offer し、網 B の UE が PCMU だけ | 488 で失敗する | 網 A の offer に PCMU を残す。コーデックの変換は作らない |
| 番号の形式 | 網 A が国内形式の番号（`phone-context` 付き）で送る | 網 B の I-CSCF が 404 を返す | IBCF のプロファイルで国際形式に直す（8.1） |
| トランスポート | 網 B の入口が TCP だけで待つ | 網 A からの UDP に応答が無く、504 になる | プロファイルの `transport` を tcp にする |
| 信頼 | 網 B が網 A を信頼しない | 呼はつながるが、UE-B に発信者番号が届かない | プロファイルの `trust` を true にする |

プリコンディションのシナリオには、UE のプリコンディションへの対応が要ります。

### S-CSCF から IBCF への渡し方

| 案 | 内容 |
| --- | --- |
| 案。S-CSCF の設定に IBCF の URI を持たせる | Request-URI のドメインが自網でないとき、IBCF の URI を Route の先頭に置く（5.4.3.2 step 11、12）。キャプチャに `Route: <sip:ibcf...;lr>` が見える |
| DNS に任せる | 自網の DNS が、相手網のドメインの NAPTR と SRV で自網の IBCF を返す。S-CSCF のコードは変わらないが、Route に IBCF が現れない |

AS が出す要求（IP-SM-GW の MESSAGE など）は I-CSCF から入るので、I-CSCF にも同じ判断が要るかは、確かめる必要があります。

### ENUM と番号ポータビリティ

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| 相手網の番号をどこで引くか | 自網の DNS の ENUM のゾーンに、相手網の番号のレコードを置く。結果は `sip:+<番号>@<相手網のドメイン>;user=phone` にする（TS 29.165 8.1） | `nni0` の DNS に事業者のあいだの ENUM を置き、S-CSCF が 2 つ目の suffix で引く（JJ-90.31 の方式に近いと見られるが、本文は未確認） |
| 入口での番号の扱い | I-CSCF に TS 24.229 5.3.2.1 の b を足す。`user=phone` の SIP URI を tel URI にしてから LIR を送る | 相手網の ENUM に IMPU の SIP URI（`sip:<IMSI>@...`）を書く。相手網の内部の識別子を自網の DNS に置くことになる |
| 番号ポータビリティ | 1 つ目の方式（ENUM のレコードを書き換える）だけをシナリオにする。`rn` と `npdi` は作らず、手順書で説明する | `rn` と `npdi` を S-CSCF と IBCF に作る |

`rn` と `npdi` を作らない案にしたのは、BGCF が無い今の範囲では、使う場所が IBCF の経路の表 1 か所だけになるためです。作るかどうかは、12 章に書きます。

### TrGW

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| SDP を書き換える部品 | `internal/pcscf/session.go` の `media`、`advertise`、`endMedia`、`subtract` を、P-CSCF と IBCF が使う 1 つのパッケージに移す（振る舞いを変えないコミット）。`Relay` のインタフェースもそこに置く | IBCF に同じ処理を書き写す |
| 側ごとのアドレス | `agw` の設定で、ターミネーションの側ごとに SDP に書くアドレスを指定できるようにする。TrGW では `core` が自網の側、`access` が相手網の側になる | `internal/agw` を N 個のターミネーションに広げるときに、ターミネーションごとに IP の領域の名前を持たせる |
| コーデックを外す | IBCF が offer の `m=` 行からプロファイルに無いコーデックを外し、対応する `a=rtpmap` と `a=fmtp` も消す。全部が消えるときは 488 を返す | 何もしないで通す |
| コーデックの変換 | 作らない。TS 24.229 5.10.7 と TS 29.165 7.1 の紹介にとどめる | TrGW で変換する。AMR の符号化には cgo が要る（[docs/design/codecs.md](../design/codecs.md)） |

側ごとのアドレスは、MRFP のための `internal/agw` の変更と重なります。その変更に合わせるなら、2 つ目の案になります。

### 網をまたぐ SMS

| 案 | 内容 | 強み | 弱み |
| --- | --- | --- | --- |
| A. SIP で網のあいだを通す | 網 A の SC のスタブが、受信者の番号を ENUM で引く。相手網の番号なら、IP-SM-GW が RP-DATA を入れた MESSAGE を、相手網の番号の SIP URI 宛てに送る。IBCF を通り、網 B の I-CSCF と S-CSCF が、着信側の iFC で網 B の IP-SM-GW に渡す。網 B の IP-SM-GW が UE-B に届ける | 今ある部品で作れる。SMS が IBCF と II-NNI を通る様子を見られる。TS 23.204 6.0a と 6.4a に近い | MSISDN のある利用者では、標準の道（SMS-GMSC と SGd）ではない。着信側の MESSAGE を拾う iFC は、ループを避けるために入れていない（[docs/design/sms.md](../design/sms.md)）。網 B の側で、網 A から来た MESSAGE だけを IP-SM-GW に渡す条件が要る |
| B. SGd で網のあいだを通す | 網 A の SC が、S6c で網 B の HSS に経路を問い合わせ、SGd で網 B の IP-SM-GW に届ける（TS 23.204 6.4） | 標準の道そのもの | SGd、S6c、SMS-GMSC が要る。SMS over NAS の部品を先に作ることになる。Diameter が網をまたぐので、IBCF は通らない。Diameter の境界（DEA）は範囲に無い |
| C. 通話だけにする | SMS は SMS over NAS のあとで B を作る | 余計なものを作らない | 網をまたいで通話と SMS ができる、という要件とロードマップの完了条件を満たさない |

案は A でした。要件は「IBCF と TrGW でつなぎ、網をまたいで通話と SMS ができる」ことで、IBCF を通るのは A だけだからです。A を選ぶときは、標準の道が B であることを `SIMPLIFIED:` と手順書に書きます。SGd が入ったら、B を 2 つ目の道として足せます。

A の細かい点は、TS 24.341 を読み直して決める必要があります。特に、網 B の IP-SM-GW が受けた RP-DATA の向きと、delivery report を網 A へ返す道です。

## 11. 確かめること（e2e）

相互接続の確認は `scripts/e2e-interconnect.sh` で行います。pcap から次のことを確かめます。

- 網 A の pcap。S-CSCF の INVITE の Route に IBCF がある。
- 網のあいだの pcap。Via に IBCF だけがあり、内部のホスト名とアドレスが無い。Contact が IBCF の URI になっている。SDP のアドレスが TrGW のものになっている。P-Charging-Function-Addresses が無い。
- 網 B の pcap。172.28.0.0/24（網 A の内部）のアドレスが、IP のヘッダにも SIP と SDP の本文にも現れない。これは `tshark` でフィールドを取り出して調べます。
- RTP が 2 つの TrGW を通る。
- 信頼しないプロファイルで、網 B の UE に届く INVITE に P-Asserted-Identity が無い。
- 食い違いのシナリオが決まった応答コードで失敗し、プロファイルを直すと成功する。
- SMS の MESSAGE が網のあいだの pcap にあり、Wireshark が RP 層と TP 層をデコードできる。

## 12. 決めた設計と、調査時の案との違い

調べた時点で論点になったことと、結論は次のとおりです。

1. Compose のブリッジは 3 つに分けます。キャプチャのファイルは 3 つになります。
2. IBCF は、調べた時点の案（プロキシと THIG の暗号化）ではなく、B2BUA で作ります。実際の網の境界の装置が B2BUA であるためです（[docs/design/media-control.md](../design/media-control.md)）。最初はプロキシで作り、あとで B2BUA に作り直しました。13 章に違いを書きます。
3. THIG の暗号は、プロキシの案のときは AES-GCM で、鍵は呼び出し側が渡す形にしました。B2BUA にしたあとは、1 つのダイアログに属するヘッダをもう一方の側に写さないので、暗号化したトークンは使いません。
4. 接続先のプロファイルの項目は、100rel とプリコンディション、トランスポート、コーデック、番号の形式と、信頼、ヘッダの削除に絞ります。
5. 番号ポータビリティの `rn` と `npdi` は作りません。ホストの無い Request-URI からは接続先を選べません（`SIMPLIFIED:`）。
6. 網をまたぐ SMS は、SIP で IBCF を通る道（案 A）で作ります。標準の道（SGd）は作りません。
7. コーデックの変換は作りません。TrGW は SDP からコーデックを外すだけです。
8. roaming と loopback の通り方は作りません。`iotl` も付けません。
9. 「日本向け」のプロファイルの例は、JJ-90.30 の本文を確かめていないことを書いて用意します。
10. IR.95 の本文は入手できていません。手順書の IR.95 の節は、確かめられていないことを書きます。

## 13. 実装の記録

IBCF は `internal/ibcf`、境界での書き換えは `internal/interconnect` にあります。この章は、作りと、作りながら確かめたことを書きます。

### 実装のときに読み直した資料

| 仕様 | 版 | 読んだ範囲 |
| --- | --- | --- |
| TS 24.229 | V20.1.0 | 4.2A、4.4.1、4.4.2、5.3.2.1、5.4.3.2 の step 10 から 13、5.10.1 から 5.10.8、6.7.1.1、6.7.1.4.1、6.7.2.1 |
| TS 29.165 | V20.0.0 | 3.2、5.1、5.2.1、5.2.2、6.1.1.2、6.1.3（Table 6.1.3.1 の項目 18、20、29、45、96）、6.2、7.1、8.1、B.8、Table C.3.1.2 |
| TS 29.238 | V19.0.0 | 1 章、Table 5.4.1、Table 5.17.2.1.1 |
| TS 23.204 | V19.1.0 | 6.0a、6.3a、6.4、6.4a、6.5 |
| TS 24.341 | V19.0.0 | 5.3.3.5 |
| TS 23.040 | V20.0.0 | 9.2.3.15 |
| TS 32.409 | V19.0.0 | 4.11（4.11.1 から 4.11.4） |
| TS 24.147 | V19.0.0 | 5.3.1.2、5.3.1.3.2、5.3.1.4.3、5.3.2.3.1、5.3.2.5.4 |
| RFC 3261 | 本文 | 8.2.2.3、8.2.6.2、12、13.2.1、13.2.2.4、15.1.1、16.6、16.7、16.10、17.1.1.2、18.1.1、18.2.1、21.5.3 |
| RFC 3262、RFC 3312、RFC 3323、RFC 3326 | 本文 | RFC 3262 の 5 章、RFC 3312 の 4 章の見出しと 11 章、RFC 3323 の 5.3 の見出し、RFC 3326 の 2 章 |
| RFC 3840、RFC 3966、RFC 4028、RFC 5627 | 本文 | RFC 3840 の 9 章と 10.18、RFC 3966 の 5.1.5、RFC 4028 の 8 章の見出し、RFC 5627 の 3.1 と 4.5 |

本文で確かめた条項は次のとおりです。

- 5.10.2.2 の step 2 と 5.10.3.2 の step 2 は、ALG か伝送の面の制御を行う IBCF に、INVITE の Contact、CSeq、Record-Route を覚えるよう求めます。IBCF が自分でセッションを解放できるようにするためです。5.10.2.3 と 5.10.3.3 の手順 2 と 3 は、後続の要求の Contact と CSeq も、target refresh の要求でもそれ以外でも覚えるよう求めます。
- 5.10.2.4 と 5.10.3.4 は、伝送の面の異常を知った IBCF が、覚えた情報から両側に BYE を送ってよいとし、両方の 2xx のあとで情報を消すとしています。
- 5.10.5 は、IMS-ALG の IBCF が B2BUA として 2 つのダイアログのあいだでヘッダを写し、GRUU でない Contact を自分の URI に替えてよいとしています。GRUU の Contact と、相手がその URI で使える能力を示す機能タグを持つ Contact は、そのまま渡すよう求めています。そのまま渡すのがダイアログを作る要求の Contact なら、Record-Route に自分の URI を入れます。NOTE は、機能タグの例として `isfocus` を挙げ、会議に入り直すときに使う一時的な会議の識別子だとしています。
- 5.10.1 は、在圏網と自網のあいだの B2BUA にだけ、Call-ID と tag を変えないことを求めます。non-roaming の II-NNI には、この決まりがありません。
- TS 32.409 4.11 は、IBCF の測定として、他の網のドメインからのセッション（4.11.1）、それへの 403（4.11.2）、他の網のドメインへのセッション（4.11.3）、それへの 403（4.11.4）を、ドメインごとの内訳に分けて挙げています。
- TS 24.147 5.3.1.2 は、ダイアログの外の SUBSCRIBE が 403 で断られたら、参加者は INVITE のダイアログの中で SUBSCRIBE を送るべきだとしています。それも失敗したら、購読なしで会議を続けます。NOTE は、Release 12 より前の網がダイアログの中の SUBSCRIBE しか扱えない場合を挙げています。
- TS 24.147 5.3.2.5.4 の手順 3 は、フォーカスが利用者を招く INVITE の Contact に、`isfocus` 付きの会議の URI を入れるとしています。5.3.2.3.1 は、フォーカスの 1xx と 2xx の Contact に `isfocus` を付けるとしています。5.3.1.3.2 は、参加者が 200 の Contact を会議の URI として覚え、その URI で購読してよいとしています。
- RFC 3261 8.2.6.2 は、UAS が 1 つの要求へのすべての応答（100 を除く）に同じ To の tag を使うとしています。13.2.1 は、UAC が最初に受けたセッション記述を answer とし、そのあとの応答のものは無視するとしています。13.2.2.4 は、フォークした INVITE に UAS ごとの 2xx が返ることを書いています。12.2.1.2 と 12.2.2 は、target refresh の要求で remote target を替えるとしています。UAC は 2xx の Contact で、UAS は要求の Contact で替えます。
- RFC 5627 3.1 と 4.5 は、GRUU が必ず `gr` の URI パラメータを持ち、それで GRUU と分かるとしています。RFC 3840 10.18 は `sip.isfocus` を定義し、9 章は Contact の機能パラメータの文法です。
- TS 24.229 5.10.6.2 の NOTE 3 は、フォークで early media が複数のダイアログにあるとき、IBCF がダイアログごとのメディアを見分けられれば、選んだものだけのゲートを開けられるとしています。B2BUA の IBCF が発信側にいくつの early dialog を見せるかを決める文は、5.10 にはありませんでした。

### IBCF の作り

IBCF は SIP の UA を 2 つ持ちます。内側の UA は自網に向き（Mx。TS 29.165 3.2）、外側の UA は相手網に向きます（Ici）。内側に届いた要求は相手網へ出ていくので、IBCF は出口として働きます（TS 24.229 5.10.2）。外側に届いた要求は自網に入るので、入口として働きます（5.10.3）。どちらの UA に届いたかで向きが決まります。

最初はプロキシと THIG の暗号化で作りましたが、実際の網の境界の装置が B2BUA であるため、B2BUA に作り直しました。違いは次のとおりです。

| 項目 | プロキシ | B2BUA |
| --- | --- | --- |
| SIP の土台 | 側ごとに `sip.Stack`。中継の処理を自分で持つ | 側ごとに `sip.UA`。2 つのレグの中継は `internal/b2bua` の中核に載せ、IBCF は境界の検査と TrGW を差し込み口で渡す（`internal/ibcf/call.go`） |
| ダイアログ | 端から端まで 1 つ。Call-ID と tag はそのまま | 呼ごとに、自網の側と相手網の側の 2 つ。Call-ID、tag、CSeq、Via、Contact は側ごとに別 |
| 構成の秘匿 | 5.10.4 のトークン（AES-GCM）で Via、Route、Record-Route を封じ、Contact をトークン付きの URI に替える | 1 つのダイアログに属するヘッダを、もう一方の側に写さない（`interconnect.CrossingFields`）。トークンは使わない |
| Record-Route | 内側と外側の 2 つの URI を入れる | 入れない。IBCF はダイアログの相手なので、あとの要求は Contact で届く |
| ダイアログの中の要求 | Route に従って中継する | re-INVITE、UPDATE、PRACK、BYE、そのほかを、もう一方のダイアログの新しい要求にする。2xx への ACK は側ごと。answer を持つ ACK は TrGW を通して写す |
| CANCEL | スタックが 200 を返し、中継の処理が向こうへ CANCEL を送る | UA が 200 と 487 を返し、IBCF がもう一方の側を CANCEL する |
| 片側の失敗 | IBCF から BYE は出ない | 一方のダイアログが終わる（BYE、ACK が来ない、481 や応答なし）と、もう一方に BYE を送る |
| MESSAGE | 中継する | IBCF が自分の Call-ID と From の tag で新しい MESSAGE を送り、その最終応答で答える |
| TrGW のコンテキスト | offer を持つ最初の INVITE だけ | 音声を持つ最初の SDP（応答の offer でもよい）で作る。呼のプロセスの中に持つ |
| 複数台 | どの台でもダイアログの中の要求を扱える | ダイアログの中の要求は、Contact に名前を書いた台に戻る。最初の要求は組の名前でどの台にも届く |

B2BUA の IBCF の手順は次のとおりです。

| 手順 | 条項 | 作り |
| --- | --- | --- |
| 100 (Trying) | 5.10.2.2 step 1、5.10.3.2 step 1 | UA が INVITE に自分で返す |
| 自分の Route を外す | 5.10.2.2 step 1A、5.10.3.2 step 1C | 内側と外側の 2 つの URI を、先頭から続くかぎり外す |
| 出口の次の宛先 | 5.10.2.2 NOTE 3 | 設定した相手の入口（`Peer.EntryPoint`）を Route に入れて送る。無ければ Request-URI のホストを DNS で引く |
| 入口の次の宛先 | 5.10.3.2 step 4 | Route が残らなければ、設定した I-CSCF を Route に入れる。外した Route に `orig` があれば、I-CSCF の URI にも付ける |
| 境界の書き換え | 4.4、5.10.6、5.10.8 | `interconnect.Border` の `ExitRequest`、`EntryRequest`、`ExitResponse`、`EntryResponse`。入口では `EntryRequest` を自分の Route を外す前に呼ぶ |
| 拒否 | 5.10.1 | `Border` の `Rejection` の応答を返す（405、420、488、404、483、500 など） |
| SDP のアドレス | 6.7.1.4.1 | TrGW のアドレスで offer と answer を書き換える |

接続先は `Peers` で見分けます。入口では送信元のアドレス（`Peer.Sources`）で、出口では次の宛先のホスト（Route の先頭か Request-URI）で探します。ホストは、プロファイルのドメインとその下の名前、入口の URI のホスト、`Peer.Hosts` のどれかです。`PeerTable` は一覧で引く実装です。接続先は加入者ごとではなく事業者の数だけなので、一覧を順に見ます。プロファイルは呼び出し側が渡すデータです。

#### 境界を越えるヘッダ

`interconnect.Border` の手続きは、受けたメッセージの写しに当てます。そのあとで `CrossingFields` が、1 つのダイアログに属さないヘッダだけを選びます。属するのは、Via、From、To、Call-ID、CSeq、Contact、Record-Route、Route、Max-Forwards、本文の枠（Content-Length、Content-Type）、RSeq、RAck、Allow、Supported、Session-Expires、Min-SE、Path、Service-Route と、Require の 100rel と timer です。MMTel AS の `carried` と同じ考え方ですが、境界の規則に属するので `internal/interconnect` に置きました。

- 最初の INVITE だけは、Allow、Supported、Session-Expires、Min-SE を写します。着信側が、発信側の使えない拡張を使わないようにするためです。プロファイルの option tag の規則は、この Supported に当たります。Supported の `replaces` もそのまま写します。IBCF は、渡す Replaces にもう一方の側のダイアログを書くためです。
- Max-Forwards は 1 つ減らして写します（RFC 3261 16.6 の step 3）。2 つの網が呼を回し続けても止まるようにするためです。
- Contact の URI は IBCF の台のものにし、機能タグ（`+g.3gpp.icsi-ref` など）は写します。`isfocus` と `expires` は外します。ただし、GRUU（`gr` を持つ URI）か `isfocus` を持つ Contact は、5.10.5 のとおりそのまま通します。
- プライバシーの `header` は、B2BUA なら何もしなくても満たされます（5.10.8 の item 1）。`user` の匿名の From は相手網の側のダイアログにだけ入り、自網の側は元の From のままです。

#### TrGW の制御

- `TrGW` のインタフェースは、TS 29.238 Table 5.17.2.1.1 の呼に関わる手順のうち、IBCF が使う 3 つです。操作の名前は H.248 のコマンドに合わせて、`Add`（Reserve TrGW Connection Point）、`Modify`（Configure TrGW Connection Point）、`Subtract`（Release TrGW Termination）とし、型は `internal/agw` のものを使います。コンテキストには、自網の側（`CoreSide`）と相手網の側（`PeerSide`）のターミネーションがあります（Table 5.4.1）。
- 実装には `agw.Client` をそのまま使います。中継ノードの `core` を自網の側、`access` を相手網の側にあてます。中継ノードは SDP に書くアドレスを 1 つしか持ちません。そこで、側ごとのアドレスを IBCF の設定（`Config.TrGWCoreAddress` と `Config.TrGWPeerAddress`）で渡し、IBCF が SDP のアドレスをそれに置き換えます。
- 音声のストリームがある最初の SDP があれば、1 回の通過ごとに 1 つのコンテキストを作ります。コーデックは、その前に `Border` が絞り込みます（`interconnect.FilterBody`。TS 29.165 7.1）。
- 書き換えの向きは次のとおりです。出ていくメッセージでは、自網の側のターミネーションに SDP のアドレスを教え、SDP には相手網の側のアドレスを書きます。入ってくるメッセージでは、その逆です。offer と answer で同じです。
- 解放の契機は 4 つです。ダイアログを終える BYE（RFC 3261 15.1.1 のとおり、2xx、481、408、応答なし）、2xx 以外の最終応答（CANCEL のあとの 487 を含む）、最終応答が来ないまま Timer B で終わったとき、コンテキストを作ったあとで INVITE を送れなかったときです。

#### トランスポート

出口では、`Profile.PreferredTransport` で送ります。次の宛先の URI に `transport` があり、プロファイルがそれを挙げていれば、その指定に従います。TS 29.165 6.2.1 は TS 24.229 4.2A に従うとしていて、4.2A は 1300 バイトを超えるメッセージを RFC 3261 18.1.1 の手順で送ることを求めます。スタックはこれを自分で行い、UDP で送るはずの長い要求を TCP で送ります。相手が TCP の接続を断れば UDP に戻ります。SDP 付きの INVITE は 1300 バイトを超えるので、UDP だけを挙げた相手にも TCP で届きます。これを止める手段はスタックに無いので、要るなら `internal/sip` の変更になります。

#### 状態と複数台

冗長構成の設計（[docs/design/state-redundancy.md](../design/state-redundancy.md)）に合わせて、IBCF は、ダイアログの中の要求を最初に INVITE を受けた台に戻し、呼の記録を保存先に置きます。B2BUA は片方の脚の要求をもう片方の脚のトランザクションにするので、2 つのトランザクションが同じプロセスに要るためです。

- 各台は、Contact に自分の台の名前（`Config.Internal`、`Config.External`）を書きます。組の名前（`Config.InternalPool`、`Config.ExternalPool`。設定の `sip.pool`）は、Route の先頭から自分の値として外すだけです。Compose の 2 つの網は 1 台ずつなので、台の名前と組の名前が同じです。
- 呼の 2 つのダイアログと TrGW のコンテキストは、その台のプロセスの中にあります。保存先を引く要求は、Replaces を持つ INVITE だけです。そのときも、Replaces が指すダイアログの索引から記録を 1 件だけ読みます。全部の呼を走査する処理も、役割全体のロックもありません。
- 保存先に置くのは、台ごとの呼の記録だけです（次の項）。`sip.UA` にはまだ `ResumeCall` が無いので、作り直した台が呼を続けることはできません。

#### TrGW のコンテキストの後始末

IBCF の台を作り直したときに残る TrGW のコンテキストは、メディア中継ノードの扱い（台の名前の下に自分のコンテキストを置き、起動したときに自分のものだけを読み直す）にならって片付けます。

| 決めたこと | 中身 |
| --- | --- |
| 記録 | `ibcf.CallRecord`。台の名前、呼の ID（乱数）、向き、相手網、TrGW のコンテキスト、応答のあとは 2 つの `sip.Dialog`。5.10.2.2 の step 2 と 5.10.3.2 の step 2 が覚えるよう求める Contact、CSeq、Record-Route は、`sip.Dialog` の `RemoteTarget`、`LocalSeq`、`RouteSet` に入っています |
| 書く時 | コンテキストを作ったときと、2xx を渡したときに `PutCall`。呼が終わったら TrGW を解放して `DeleteCall`。要求ごとに 2 つのダイアログを書き直し、呼が終わったら `ended` を立てて、遅れて終わった要求が消した記録を戻さないようにします。`Close` で止めた台は、止まったプロセスと同じく記録を残します |
| 読む時 | `Listen` が、UA を作る前に自分の台の名前の記録を `CallsOf` で 1 回読みます。UA ができたら、別の goroutine で `ReleaseLeftovers` を行います |
| 片付け | 記録ごとに TrGW のコンテキストを解放し、2 つのダイアログのそれぞれで BYE を送り、記録を消します。BYE の CSeq は、記録の `LocalSeq` に 65536 を足した値の次です。記録は 2xx のときに書くので、そのあとの要求で CSeq が進んでいるかもしれないためです。受ける側は、前より大きな CSeq なら受け付けます（RFC 3261 12.2.2）。MMTel AS の `ResumeCall` と同じ考え方です |
| 規模 | 1 つの台の記録は、その台が同時に持っていた呼の数までです。キーは台の名前と呼の ID で、全部の呼を走査しません |
| 実装 | `MemoryStore` と、`internal/kvstore/ibcfstore` の `Store` の 2 つです。ノードの設定に種類が `valkey` の `store` の節があれば、`cmd/ims` は IBCF に `ibcfstore` を渡します。キーは `<prefix>ibcf:call:<台の名前>:<呼の ID>` と、台ごとの ID の集合 `<prefix>ibcf:calls-of:<台の名前>` で、メディア中継ノードの形に合わせました。ダイアログから呼を引く索引 `<prefix>ibcf:dialog:<Call-ID>:<自分の tag>:<相手の tag>` は、記録のあとに書き、記録の前に消します。消すときに記録を読んで索引を見つけるので、途中で止まっても、記録から見つけられない索引は残りません。`store` の節が無ければ `MemoryStore` を使い、記録はプロセスと一緒に消えます。Compose の網の IBCF には `store` の節が無いので、作り直した台は何も見つけません |

試験 `TestRestartedInstanceEndsTheCallsItLeft` は、網 A の 1 台目を `Close` して、同じアドレスで新しいプロセスを起動します。保存先は 2 通りで、同じ `MemoryStore` を渡す形と、共有の保存先の上にプロセスごとに `ibcfstore` を作り直す形です。発信者と着信者の呼が BYE で終わり、両方の網の TrGW のコンテキストが解放され、記録が消えることを確かめています。

まだ片付かないものがあります。

- 同じ名前で二度と起動しない台の記録とコンテキスト。別の台が引き取る仕組み（台の生死を知る方法と、1 台だけが引き取るリース）は作っていません。
- TrGW（中継ノード）の側から見た、持ち主のいないコンテキスト。TS 29.238 の監査（Audit）に当たる仕組みはありません。
- 応答の前に止まった呼。記録にダイアログが無いので、TrGW のコンテキストだけを解放します。発信者の INVITE は時間切れで終わります。

#### 相手網でない送信元からの要求

IBCF は、相手網として設定していない送信元の要求に、応答せずに捨てます。実際の網の境界の装置がそう動くためです（[docs/design/media-control.md](../design/media-control.md)）。TS 24.229 V20.1.0 の 5.10 に、IBCF が送信元を理由に要求を応答なしで捨てる決まりは見つかりませんでした。5.10.1 は、局所的な方針で 4xx を返して断ることを認めているだけです。似た決まりは P-CSCF にあります。5.2.6.3.2A は、IP の対応付け（IP association）に当たらない REGISTER 以外の要求を、100 を返さずに捨てるとしています。

- 外側の UA のスタックは、どのサーバートランザクションにも当たらない要求を受けると、何かをする前に `IBCF.Screen` を呼びます。ACK と CANCEL も含みます。相手網でない送信元なら、`IBCF.Screen` は `"msg":"discarded a request from an address of no peer network"` をログに出し、`ims_ibcf_requests_discarded_total{reason="unknown_source",method}` を増やし、false を返します。スタックはその要求を捨てます。100 も、CANCEL への 200 や 481 も、ダイアログの外の OPTIONS への 200 も、許さないメソッドへの 405 も返しません。再送も、そのたびに捨てます。
- 内側の UA には渡しません。自網のノードは送信元で見分けないためです。
- 相手網からの要求を、プロトコルや取り決めの理由で断るときは、応答を返します（405、420、488、404、483、500 など）。
- 入口で `Peers` が同じアドレスに違う答えを返したときだけ、エラーのログを出して 500 を返します。誤りの応答を捨てると、トランザクションがスタックに残るためです。
- `IBCF.Screen` は、スタックの goroutine の中で `Peers.BySource` を引き、カウンタを足し、ログを出すだけです。`PeerTable` は作ったあとで変わらない一覧なので、ロックを取りません。カウンタは原子的な加算です。

#### 指標

`internal/ibcf/metrics.go` が、`Config.Metrics` のレジストリに 4 つのカウンタを登録します。名前は [oam.md](oam.md) の決まり（`ims_` で始め、counter は `_total` で終える）に合わせ、役割の名前を名前に入れました。ラベルの値の集合は設定した相手網の名前などで決まり、加入者の数では増えません。

| 名前 | ラベル | 数えるもの | TS 32.409 V19.0.0 |
| --- | --- | --- | --- |
| `ims_ibcf_session_attempts_total` | `peer`、`direction`（exit、entry） | 境界の向こうへ送った最初の INVITE | 4.11.1（entry）、4.11.3（exit） |
| `ims_ibcf_session_failures_total` | `peer`、`direction`、`status` | それに戻った 2xx 以外の最終応答 | 4.11.4（exit の 403） |
| `ims_ibcf_requests_rejected_total` | `peer`、`direction`、`method`、`status` | IBCF が自分で答えた要求。どの相手網にも向かわない出口の要求への 404 は `peer="other"` | 4.11.2（entry の INVITE への 403） |
| `ims_ibcf_requests_discarded_total` | `reason`（unknown_source）、`method` | 何も答えずに捨てた要求。相手網でない送信元の要求は、ここだけで数える | なし |

`method` の値は TS 29.165 Table 6.1 のメソッド、`status` の値は IBCF が返す応答コードとよくある失敗のコードです。ほかは `other` で数えます。UA が自分で返す応答（420、481、405 など）は数えていません。

### プリコンディション

TS 24.229 の 5.10 には、IBCF がプリコンディションのしくみに手を入れる手順がありません。TS 29.165 は、このしくみを II-NNI で任意とし（Table 6.1.3.1 の項目 20）、使うかどうかを事業者の取り決めの選択項目にしています（Table C.3.1.2）。RFC 3312 11 は、Supported に挙げるだけの offer に、しくみを持たない着信者がプリコンディションの無い answer を返すとしています。必須にした INVITE には、着信者が 420 を返します（RFC 3261 8.2.2.3）。どちらも呼の両端で決まり、境界の動きではありません。

そこでプロファイルの方針は次のようにしています。

- 方針 `reject` は、その tag を必須にした要求に 420（Unsupported 付き）を返すだけです。B2BUA の IBCF は、受けた側のダイアログの UAS なので、RFC 3261 8.2.2.3 の UAS の規則に当たります。Supported に挙げるだけの要求は、そのまま通します。
- プロファイルで precondition を `remove` にすることは、検証で断ります。
- SDP の `a=curr` と `a=des` を外す、境界でプリコンディションを終端する、といった作りは、確かめた仕様の本文に根拠が無いので作りません。

試験 `TestPreconditionsOutsideTheAgreement` が、方針 `pass` では網 B の UE が 420 を返すこと、`reject` では IBCF-A が Unsupported 付きの 420 を返し、Supported だけの INVITE は通ることを確かめています。

### 境界を越える会議

- GRUU（`gr` を持つ URI）か `isfocus` を持つ Contact は、そのまま通します（5.10.5）。INVITE を出す側では `Invitation.RecordRoute`、応答を返す側では `IncomingCall.SetContact` の `recordRoute` で、IBCF の台の URI を Record-Route に入れます。相手のダイアログの中の要求は、Request-URI が会議の URI で、Route に IBCF があります。IBCF はそのダイアログでそれを受け止め、もう一方のダイアログの remote target（届いたままの Contact）に自分の要求を送ります。B2BUA のままです。ほかの Contact は、IBCF の URI に相手の機能タグを付けたものです。応答ごとに設定します。
- ダイアログの外の SUBSCRIBE は、IBCF が境界の検査（取り決めに SUBSCRIBE が無ければ 405）のあとで、局所的な方針として 403 を返します（5.10.1）。TS 24.147 5.3.1.2 の参加者は 403 のときだけダイアログの中に切り替えるので、405 では相手網の会議を購読できません。数えて、ログに出します。知らないダイアログの中の SUBSCRIBE は 481 です。ダイアログの外の購読そのものを境界の両側で持つ作りは作っていません（`SIMPLIFIED:`）。
- Replaces を持つ INVITE では、Replaces が指すダイアログを自分の呼の片側として保存先から探します（`Store.CallOf`）。呼を持つのが別の台でも見つかります。見つかれば、もう一方の側のダイアログの Call-ID と tag に書き換えます。TS 24.229 5.10.5 は IMS-ALG の IBCF が 2 つのダイアログの識別子を対応づけるとし、5.7.5.1 は routeing B2BUA の AS が同じ書き換えをするよう定めています。見つからなければ、5.7.5.1 の AS と同じく届いたまま渡します。IBCF の先の B2BUA がそのダイアログを持っているかもしれないためです。保存先を読めなければ 500 を返します。early dialog は記録に無いので対応づけません（`SIMPLIFIED:`）。Refer-To の中の Replaces も対応づけません（`SIMPLIFIED:`）。

### フォークのときに発信側に見せるもの

IBCF の先で INVITE がフォークすると、向こう側に early dialog がいくつもできます。B2BUA の IBCF が発信側に見せる形には、2 つの案があります。

1. 向こう側の early dialog ごとに、発信側にも別の To の tag の early dialog を見せる。
2. 発信側には 1 つの early dialog だけを見せる。

2 を選びました。IBCF は発信側から見れば 1 つの UAS で、RFC 3261 8.2.6.2 は、UAS が 1 つの要求へのすべての応答に同じ To の tag を使うとしているためです。`internal/sip` の `IncomingCall` も、同時には 1 つの early dialog しか持てません（199 で終えて次の tag に移ることはできます）。TS 24.229 5.10.6.2 の NOTE 3 は、IBCF がフォークの複数の early media を扱う場合に触れていますが、B2BUA が何を見せるかは決めていません。向こう側の early dialog ごとに発信側にも別の early dialog を見せる SBC もありますが、そのためには `internal/sip` の UAS が同時に複数の To の tag で応答できる必要があります。

そのうえで、次のようにしています。

- 発信側の early dialog のセッション記述は、向こう側で最初にセッション記述を送ってきたダイアログのものです。発信側は最初の answer を取り、あとのものは無視するので（13.2.1）、ほかのダイアログの応答は、セッション記述を外して渡します。この扱いは `internal/b2bua` の中核が持ちます。
- 向こう側のダイアログごとに、最後にセッション記述を持っていた応答を To の tag で覚えます（`calleeSDP`）。
- 200 が来たら、そのダイアログに TrGW を向けます。200 に SDP があればそれで、無ければ覚えたもので設定します。応えたのが、発信側にセッション記述を渡したのと違うダイアログなら、200 はセッション記述を外して発信側に渡します。

試験 `TestForkBeyondTheBorder` は、網 B で INVITE を 2 つの枝に分け、どちらも信頼性のある 183 で answer を返し、片方が SDP の無い 200 を返します。どちらの枝が応えても、TrGW B は応えた枝を向きます。発信側が受けるセッション記述は 1 つだけで、どの応答も同じ To の tag です。

`SIMPLIFIED:`（RFC 3261 13.2.1、14.1）にしたのは次のことです。応えたダイアログが、発信側がセッション記述を受け取ったダイアログでないとき、IBCF は応えたダイアログのセッションを re-INVITE で発信側に offer し直しません。呼は最初のダイアログが選んだコーデックで続きます。offer の無い INVITE のあとでは、発信側が answer しなかったダイアログの offer には answer が返りません。

### PRACK と次の要求の順

出る側の PRACK と、発信側の次の要求が、逆の順で出ることがあります。

- 本文の無い PRACK（183 が answer を運び、PRACK は確認だけ）には、`internal/sip` の `acceptPRACK` が自分で 200 を返し、IBCF の手には渡りません。IBCF は `in.Progress` が戻ったあとで出る側の PRACK を送ります。その間に試験が割り込める場所が無いので、決まった順では再現できませんでした。
- answer を運ぶ PRACK と、offer を運ぶ UPDATE を続けて送る発信者では、UPDATE も TrGW を通るので、`mediaMu` で PRACK のあとに並びます。逆になりませんでした。
- SDP を持たない要求は、毎回再現しました。発信者が answer を運ぶ PRACK を送り、その 200 を待たずに INFO を送ります。RFC 3261 と RFC 3262 の読んだ範囲には、これを禁じる決まりがありませんでした。試験の TrGW が PRACK の answer の設定を止めているあいだに、INFO は TrGW を通らずに出る側へ出ていきます。20 回のうち 20 回、網 B に INFO が PRACK より先に届きました（`TestPRACKGoesBeforeTheRequestThatFollowsIt`、`-race -count=20`）。

今の作りでは、`internal/b2bua` の中核がダイアログの中の要求を、側ごとに届いた順に中継します。`internal/sip` が要求に届いた順の番号（`InDialogRequest.Arrival`）を振り、中核は、もう一方のレグが最終応答を返すまで次の要求を送りません。本文の無い PRACK も、`IncomingCall.RelayPRACK` で中核に渡るので、そのあとの UPDATE や INFO が先に出ることはありません。作りは [b2bua.md](../design/b2bua.md) にあります。

### 網をまたぐ SMS

| 案 | 内容 | 強み | 弱み |
| --- | --- | --- | --- |
| A. SIP で網のあいだを通す | 網 A の SC のスタブが、受信者の番号を ENUM で引く。相手網の番号なら、IP-SM-GW が RP-DATA を入れた MESSAGE を、相手網の番号の SIP URI 宛てに送る。IBCF を通り、網 B の I-CSCF と S-CSCF が、着信側の iFC で網 B の IP-SM-GW に渡す。網 B の IP-SM-GW が UE-B に届ける | 今ある部品で作れる。SMS が IBCF と II-NNI を通る様子を見られる。TS 23.204 6.0a と 6.4a に近い | MSISDN のある利用者では、標準の道（SMS-GMSC と SGd）ではない。着信側の MESSAGE を拾う iFC は、ループを避けるために入れていない（[docs/design/sms.md](../design/sms.md)）。網 B の側で、網 A から来た MESSAGE だけを IP-SM-GW に渡す条件が要る |
| B. SGd で網のあいだを通す | 網 A の SC が、S6c で網 B の HSS に経路を問い合わせ、SGd で網 B の IP-SM-GW に届ける（TS 23.204 6.4） | 標準の道そのもの | SGd、S6c、SMS-GMSC が要る。SMS over NAS の部品を先に作ることになる。Diameter が網をまたぐので、IBCF は通らない。Diameter の境界（DEA）は範囲に無い |
| C. 通話だけにする | SMS は SMS over NAS のあとで B を作る | 余計なものを作らない | 網をまたいで通話と SMS ができる、という要件とロードマップの完了条件を満たさない |

案は A でした。要件は「IBCF と TrGW でつなぎ、網をまたいで通話と SMS ができる」ことで、IBCF を通るのは A だけだからです。A を選ぶときは、標準の道が B であることを `SIMPLIFIED:` と手順書に書きます。SGd が入ったら、B を 2 つ目の道として足せます。

A の細かい点は、TS 24.341 を読み直して決める必要があります。特に、網 B の IP-SM-GW が受けた RP-DATA の向きと、delivery report を網 A へ返す道です。

## 11. 確かめること（e2e）

相互接続の確認は `scripts/e2e-interconnect.sh` で行います。pcap から次のことを確かめます。

- 網 A の pcap。S-CSCF の INVITE の Route に IBCF がある。
- 網のあいだの pcap。Via に IBCF だけがあり、内部のホスト名とアドレスが無い。Contact が IBCF の URI になっている。SDP のアドレスが TrGW のものになっている。P-Charging-Function-Addresses が無い。
- 網 B の pcap。172.28.0.0/24（網 A の内部）のアドレスが、IP のヘッダにも SIP と SDP の本文にも現れない。これは `tshark` でフィールドを取り出して調べます。
- RTP が 2 つの TrGW を通る。
- 信頼しないプロファイルで、網 B の UE に届く INVITE に P-Asserted-Identity が無い。
- 食い違いのシナリオが決まった応答コードで失敗し、プロファイルを直すと成功する。
- SMS の MESSAGE が網のあいだの pcap にあり、Wireshark が RP 層と TP 層をデコードできる。

## 12. 決めた設計と、調査時の案との違い

調べた時点で論点になったことと、結論は次のとおりです。

1. Compose のブリッジは 3 つに分けます。キャプチャのファイルは 3 つになります。
2. IBCF は、調べた時点の案（プロキシと THIG の暗号化）ではなく、B2BUA で作ります。実際の網の境界の装置が B2BUA であるためです（[docs/design/media-control.md](../design/media-control.md)）。最初はプロキシで作り、あとで B2BUA に作り直しました。13 章に違いを書きます。
3. THIG の暗号は、プロキシの案のときは AES-GCM で、鍵は呼び出し側が渡す形にしました。B2BUA にしたあとは、1 つのダイアログに属するヘッダをもう一方の側に写さないので、暗号化したトークンは使いません。
4. 接続先のプロファイルの項目は、100rel とプリコンディション、トランスポート、コーデック、番号の形式と、信頼、ヘッダの削除に絞ります。
5. 番号ポータビリティの `rn` と `npdi` は作りません。ホストの無い Request-URI からは接続先を選べません（`SIMPLIFIED:`）。
6. 網をまたぐ SMS は、SIP で IBCF を通る道（案 A）で作ります。標準の道（SGd）は作りません。
7. コーデックの変換は作りません。TrGW は SDP からコーデックを外すだけです。
8. roaming と loopback の通り方は作りません。`iotl` も付けません。
9. 「日本向け」のプロファイルの例は、JJ-90.30 の本文を確かめていないことを書いて用意します。
10. IR.95 の本文は入手できていません。手順書の IR.95 の節は、確かめられていないことを書きます。

## 13. 実装の記録

IBCF は `internal/ibcf`、境界での書き換えは `internal/interconnect` にあります。この章は、作りと、作りながら確かめたことを書きます。

### 実装のときに読み直した資料

| 仕様 | 版 | 読んだ範囲 |
| --- | --- | --- |
| TS 24.229 | V20.1.0 | 4.2A、4.4.1、4.4.2、5.3.2.1、5.4.3.2 の step 10 から 13、5.10.1 から 5.10.8、6.7.1.1、6.7.1.4.1、6.7.2.1 |
| TS 29.165 | V20.0.0 | 3.2、5.1、5.2.1、5.2.2、6.1.1.2、6.1.3（Table 6.1.3.1 の項目 18、20、29、45、96）、6.2、7.1、8.1、B.8、Table C.3.1.2 |
| TS 29.238 | V19.0.0 | 1 章、Table 5.4.1、Table 5.17.2.1.1 |
| TS 23.204 | V19.1.0 | 6.0a、6.3a、6.4、6.4a、6.5 |
| TS 24.341 | V19.0.0 | 5.3.3.5 |
| TS 23.040 | V20.0.0 | 9.2.3.15 |
| TS 32.409 | V19.0.0 | 4.11（4.11.1 から 4.11.4） |
| TS 24.147 | V19.0.0 | 5.3.1.2、5.3.1.3.2、5.3.1.4.3、5.3.2.3.1、5.3.2.5.4 |
| RFC 3261 | 本文 | 8.2.2.3、8.2.6.2、12、13.2.1、13.2.2.4、15.1.1、16.6、16.7、16.10、17.1.1.2、18.1.1、18.2.1、21.5.3 |
| RFC 3262、RFC 3312、RFC 3323、RFC 3326 | 本文 | RFC 3262 の 5 章、RFC 3312 の 4 章の見出しと 11 章、RFC 3323 の 5.3 の見出し、RFC 3326 の 2 章 |
| RFC 3840、RFC 3966、RFC 4028、RFC 5627 | 本文 | RFC 3840 の 9 章と 10.18、RFC 3966 の 5.1.5、RFC 4028 の 8 章の見出し、RFC 5627 の 3.1 と 4.5 |

本文で確かめた条項は次のとおりです。

- 5.10.2.2 の step 2 と 5.10.3.2 の step 2 は、ALG か伝送の面の制御を行う IBCF に、INVITE の Contact、CSeq、Record-Route を覚えるよう求めます。IBCF が自分でセッションを解放できるようにするためです。5.10.2.3 と 5.10.3.3 の手順 2 と 3 は、後続の要求の Contact と CSeq も、target refresh の要求でもそれ以外でも覚えるよう求めます。
- 5.10.2.4 と 5.10.3.4 は、伝送の面の異常を知った IBCF が、覚えた情報から両側に BYE を送ってよいとし、両方の 2xx のあとで情報を消すとしています。
- 5.10.5 は、IMS-ALG の IBCF が B2BUA として 2 つのダイアログのあいだでヘッダを写し、GRUU でない Contact を自分の URI に替えてよいとしています。GRUU の Contact と、相手がその URI で使える能力を示す機能タグを持つ Contact は、そのまま渡すよう求めています。そのまま渡すのがダイアログを作る要求の Contact なら、Record-Route に自分の URI を入れます。NOTE は、機能タグの例として `isfocus` を挙げ、会議に入り直すときに使う一時的な会議の識別子だとしています。
- 5.10.1 は、在圏網と自網のあいだの B2BUA にだけ、Call-ID と tag を変えないことを求めます。non-roaming の II-NNI には、この決まりがありません。
- TS 32.409 4.11 は、IBCF の測定として、他の網のドメインからのセッション（4.11.1）、それへの 403（4.11.2）、他の網のドメインへのセッション（4.11.3）、それへの 403（4.11.4）を、ドメインごとの内訳に分けて挙げています。
- TS 24.147 5.3.1.2 は、ダイアログの外の SUBSCRIBE が 403 で断られたら、参加者は INVITE のダイアログの中で SUBSCRIBE を送るべきだとしています。それも失敗したら、購読なしで会議を続けます。NOTE は、Release 12 より前の網がダイアログの中の SUBSCRIBE しか扱えない場合を挙げています。
- TS 24.147 5.3.2.5.4 の手順 3 は、フォーカスが利用者を招く INVITE の Contact に、`isfocus` 付きの会議の URI を入れるとしています。5.3.2.3.1 は、フォーカスの 1xx と 2xx の Contact に `isfocus` を付けるとしています。5.3.1.3.2 は、参加者が 200 の Contact を会議の URI として覚え、その URI で購読してよいとしています。
- RFC 3261 8.2.6.2 は、UAS が 1 つの要求へのすべての応答（100 を除く）に同じ To の tag を使うとしています。13.2.1 は、UAC が最初に受けたセッション記述を answer とし、そのあとの応答のものは無視するとしています。13.2.2.4 は、フォークした INVITE に UAS ごとの 2xx が返ることを書いています。12.2.1.2 と 12.2.2 は、target refresh の要求で remote target を替えるとしています。UAC は 2xx の Contact で、UAS は要求の Contact で替えます。
- RFC 5627 3.1 と 4.5 は、GRUU が必ず `gr` の URI パラメータを持ち、それで GRUU と分かるとしています。RFC 3840 10.18 は `sip.isfocus` を定義し、9 章は Contact の機能パラメータの文法です。
- TS 24.229 5.10.6.2 の NOTE 3 は、フォークで early media が複数のダイアログにあるとき、IBCF がダイアログごとのメディアを見分けられれば、選んだものだけのゲートを開けられるとしています。B2BUA の IBCF が発信側にいくつの early dialog を見せるかを決める文は、5.10 にはありませんでした。

### IBCF の作り

IBCF は SIP の UA を 2 つ持ちます。内側の UA は自網に向き（Mx。TS 29.165 3.2）、外側の UA は相手網に向きます（Ici）。内側に届いた要求は相手網へ出ていくので、IBCF は出口として働きます（TS 24.229 5.10.2）。外側に届いた要求は自網に入るので、入口として働きます（5.10.3）。どちらの UA に届いたかで向きが決まります。

最初はプロキシと THIG の暗号化で作りましたが、実際の網の境界の装置が B2BUA であるため、B2BUA に作り直しました。違いは次のとおりです。

| 項目 | プロキシ | B2BUA |
| --- | --- | --- |
| SIP の土台 | 側ごとに `sip.Stack`。中継の処理を自分で持つ | 側ごとに `sip.UA`。2 つのレグの中継は `internal/b2bua` の中核に載せ、IBCF は境界の検査と TrGW を差し込み口で渡す（`internal/ibcf/call.go`） |
| ダイアログ | 端から端まで 1 つ。Call-ID と tag はそのまま | 呼ごとに、自網の側と相手網の側の 2 つ。Call-ID、tag、CSeq、Via、Contact は側ごとに別 |
| 構成の秘匿 | 5.10.4 のトークン（AES-GCM）で Via、Route、Record-Route を封じ、Contact をトークン付きの URI に替える | 1 つのダイアログに属するヘッダを、もう一方の側に写さない（`interconnect.CrossingFields`）。トークンは使わない |
| Record-Route | 内側と外側の 2 つの URI を入れる | 入れない。IBCF はダイアログの相手なので、あとの要求は Contact で届く |
| ダイアログの中の要求 | Route に従って中継する | re-INVITE、UPDATE、PRACK、BYE、そのほかを、もう一方のダイアログの新しい要求にする。2xx への ACK は側ごと。answer を持つ ACK は TrGW を通して写す |
| CANCEL | スタックが 200 を返し、中継の処理が向こうへ CANCEL を送る | UA が 200 と 487 を返し、IBCF がもう一方の側を CANCEL する |
| 片側の失敗 | IBCF から BYE は出ない | 一方のダイアログが終わる（BYE、ACK が来ない、481 や応答なし）と、もう一方に BYE を送る |
| MESSAGE | 中継する | IBCF が自分の Call-ID と From の tag で新しい MESSAGE を送り、その最終応答で答える |
| TrGW のコンテキスト | offer を持つ最初の INVITE だけ | 音声を持つ最初の SDP（応答の offer でもよい）で作る。呼のプロセスの中に持つ |
| 複数台 | どの台でもダイアログの中の要求を扱える | ダイアログの中の要求は、Contact に名前を書いた台に戻る。最初の要求は組の名前でどの台にも届く |

B2BUA の IBCF の手順は次のとおりです。

| 手順 | 条項 | 作り |
| --- | --- | --- |
| 100 (Trying) | 5.10.2.2 step 1、5.10.3.2 step 1 | UA が INVITE に自分で返す |
| 自分の Route を外す | 5.10.2.2 step 1A、5.10.3.2 step 1C | 内側と外側の 2 つの URI を、先頭から続くかぎり外す |
| 出口の次の宛先 | 5.10.2.2 NOTE 3 | 設定した相手の入口（`Peer.EntryPoint`）を Route に入れて送る。無ければ Request-URI のホストを DNS で引く |
| 入口の次の宛先 | 5.10.3.2 step 4 | Route が残らなければ、設定した I-CSCF を Route に入れる。外した Route に `orig` があれば、I-CSCF の URI にも付ける |
| 境界の書き換え | 4.4、5.10.6、5.10.8 | `interconnect.Border` の `ExitRequest`、`EntryRequest`、`ExitResponse`、`EntryResponse`。入口では `EntryRequest` を自分の Route を外す前に呼ぶ |
| 拒否 | 5.10.1 | `Border` の `Rejection` の応答を返す（405、420、488、404、483、500 など） |
| SDP のアドレス | 6.7.1.4.1 | TrGW のアドレスで offer と answer を書き換える |

接続先は `Peers` で見分けます。入口では送信元のアドレス（`Peer.Sources`）で、出口では次の宛先のホスト（Route の先頭か Request-URI）で探します。ホストは、プロファイルのドメインとその下の名前、入口の URI のホスト、`Peer.Hosts` のどれかです。`PeerTable` は一覧で引く実装です。接続先は加入者ごとではなく事業者の数だけなので、一覧を順に見ます。プロファイルは呼び出し側が渡すデータです。

#### 境界を越えるヘッダ

`interconnect.Border` の手続きは、受けたメッセージの写しに当てます。そのあとで `CrossingFields` が、1 つのダイアログに属さないヘッダだけを選びます。属するのは、Via、From、To、Call-ID、CSeq、Contact、Record-Route、Route、Max-Forwards、本文の枠（Content-Length、Content-Type）、RSeq、RAck、Allow、Supported、Session-Expires、Min-SE、Path、Service-Route と、Require の 100rel と timer です。MMTel AS の `carried` と同じ考え方ですが、境界の規則に属するので `internal/interconnect` に置きました。

- 最初の INVITE だけは、Allow、Supported、Session-Expires、Min-SE を写します。着信側が、発信側の使えない拡張を使わないようにするためです。プロファイルの option tag の規則は、この Supported に当たります。Supported の `replaces` もそのまま写します。IBCF は、渡す Replaces にもう一方の側のダイアログを書くためです。
- Max-Forwards は 1 つ減らして写します（RFC 3261 16.6 の step 3）。2 つの網が呼を回し続けても止まるようにするためです。
- Contact の URI は IBCF の台のものにし、機能タグ（`+g.3gpp.icsi-ref` など）は写します。`isfocus` と `expires` は外します。ただし、GRUU（`gr` を持つ URI）か `isfocus` を持つ Contact は、5.10.5 のとおりそのまま通します。
- プライバシーの `header` は、B2BUA なら何もしなくても満たされます（5.10.8 の item 1）。`user` の匿名の From は相手網の側のダイアログにだけ入り、自網の側は元の From のままです。

#### TrGW の制御

- `TrGW` のインタフェースは、TS 29.238 Table 5.17.2.1.1 の呼に関わる手順のうち、IBCF が使う 3 つです。操作の名前は H.248 のコマンドに合わせて、`Add`（Reserve TrGW Connection Point）、`Modify`（Configure TrGW Connection Point）、`Subtract`（Release TrGW Termination）とし、型は `internal/agw` のものを使います。コンテキストには、自網の側（`CoreSide`）と相手網の側（`PeerSide`）のターミネーションがあります（Table 5.4.1）。
- 実装には `agw.Client` をそのまま使います。中継ノードの `core` を自網の側、`access` を相手網の側にあてます。中継ノードは SDP に書くアドレスを 1 つしか持ちません。そこで、側ごとのアドレスを IBCF の設定（`Config.TrGWCoreAddress` と `Config.TrGWPeerAddress`）で渡し、IBCF が SDP のアドレスをそれに置き換えます。
- 音声のストリームがある最初の SDP があれば、1 回の通過ごとに 1 つのコンテキストを作ります。コーデックは、その前に `Border` が絞り込みます（`interconnect.FilterBody`。TS 29.165 7.1）。
- 書き換えの向きは次のとおりです。出ていくメッセージでは、自網の側のターミネーションに SDP のアドレスを教え、SDP には相手網の側のアドレスを書きます。入ってくるメッセージでは、その逆です。offer と answer で同じです。
- 解放の契機は 4 つです。ダイアログを終える BYE（RFC 3261 15.1.1 のとおり、2xx、481、408、応答なし）、2xx 以外の最終応答（CANCEL のあとの 487 を含む）、最終応答が来ないまま Timer B で終わったとき、コンテキストを作ったあとで INVITE を送れなかったときです。

#### トランスポート

出口では、`Profile.PreferredTransport` で送ります。次の宛先の URI に `transport` があり、プロファイルがそれを挙げていれば、その指定に従います。TS 29.165 6.2.1 は TS 24.229 4.2A に従うとしていて、4.2A は 1300 バイトを超えるメッセージを RFC 3261 18.1.1 の手順で送ることを求めます。スタックはこれを自分で行い、UDP で送るはずの長い要求を TCP で送ります。相手が TCP の接続を断れば UDP に戻ります。SDP 付きの INVITE は 1300 バイトを超えるので、UDP だけを挙げた相手にも TCP で届きます。これを止める手段はスタックに無いので、要るなら `internal/sip` の変更になります。

#### 状態と複数台

冗長構成の設計（[docs/design/state-redundancy.md](../design/state-redundancy.md)）に合わせて、IBCF は、ダイアログの中の要求を最初に INVITE を受けた台に戻し、呼の記録を保存先に置きます。B2BUA は片方の脚の要求をもう片方の脚のトランザクションにするので、2 つのトランザクションが同じプロセスに要るためです。

- 各台は、Contact に自分の台の名前（`Config.Internal`、`Config.External`）を書きます。組の名前（`Config.InternalPool`、`Config.ExternalPool`。設定の `sip.pool`）は、Route の先頭から自分の値として外すだけです。Compose の 2 つの網は 1 台ずつなので、台の名前と組の名前が同じです。
- 呼の 2 つのダイアログと TrGW のコンテキストは、その台のプロセスの中にあります。保存先を引く要求は、Replaces を持つ INVITE だけです。そのときも、Replaces が指すダイアログの索引から記録を 1 件だけ読みます。全部の呼を走査する処理も、役割全体のロックもありません。
- 保存先に置くのは、台ごとの呼の記録だけです（次の項）。`sip.UA` にはまだ `ResumeCall` が無いので、作り直した台が呼を続けることはできません。

#### TrGW のコンテキストの後始末

IBCF の台を作り直したときに残る TrGW のコンテキストは、メディア中継ノードの扱い（台の名前の下に自分のコンテキストを置き、起動したときに自分のものだけを読み直す）にならって片付けます。

| 決めたこと | 中身 |
| --- | --- |
| 記録 | `ibcf.CallRecord`。台の名前、呼の ID（乱数）、向き、相手網、TrGW のコンテキスト、応答のあとは 2 つの `sip.Dialog`。5.10.2.2 の step 2 と 5.10.3.2 の step 2 が覚えるよう求める Contact、CSeq、Record-Route は、`sip.Dialog` の `RemoteTarget`、`LocalSeq`、`RouteSet` に入っています |
| 書く時 | コンテキストを作ったときと、2xx を渡したときに `PutCall`。呼が終わったら TrGW を解放して `DeleteCall`。要求ごとに 2 つのダイアログを書き直し、呼が終わったら `ended` を立てて、遅れて終わった要求が消した記録を戻さないようにします。`Close` で止めた台は、止まったプロセスと同じく記録を残します |
| 読む時 | `Listen` が、UA を作る前に自分の台の名前の記録を `CallsOf` で 1 回読みます。UA ができたら、別の goroutine で `ReleaseLeftovers` を行います |
| 片付け | 記録ごとに TrGW のコンテキストを解放し、2 つのダイアログのそれぞれで BYE を送り、記録を消します。BYE の CSeq は、記録の `LocalSeq` に 65536 を足した値の次です。記録は 2xx のときに書くので、そのあとの要求で CSeq が進んでいるかもしれないためです。受ける側は、前より大きな CSeq なら受け付けます（RFC 3261 12.2.2）。MMTel AS の `ResumeCall` と同じ考え方です |
| 規模 | 1 つの台の記録は、その台が同時に持っていた呼の数までです。キーは台の名前と呼の ID で、全部の呼を走査しません |
| 実装 | `MemoryStore` と、`internal/kvstore/ibcfstore` の `Store` の 2 つです。ノードの設定に種類が `valkey` の `store` の節があれば、`cmd/ims` は IBCF に `ibcfstore` を渡します。キーは `<prefix>ibcf:call:<台の名前>:<呼の ID>` と、台ごとの ID の集合 `<prefix>ibcf:calls-of:<台の名前>` で、メディア中継ノードの形に合わせました。ダイアログから呼を引く索引 `<prefix>ibcf:dialog:<Call-ID>:<自分の tag>:<相手の tag>` は、記録のあとに書き、記録の前に消します。消すときに記録を読んで索引を見つけるので、途中で止まっても、記録から見つけられない索引は残りません。`store` の節が無ければ `MemoryStore` を使い、記録はプロセスと一緒に消えます。Compose の網の IBCF には `store` の節が無いので、作り直した台は何も見つけません |

試験 `TestRestartedInstanceEndsTheCallsItLeft` は、網 A の 1 台目を `Close` して、同じアドレスで新しいプロセスを起動します。保存先は 2 通りで、同じ `MemoryStore` を渡す形と、共有の保存先の上にプロセスごとに `ibcfstore` を作り直す形です。発信者と着信者の呼が BYE で終わり、両方の網の TrGW のコンテキストが解放され、記録が消えることを確かめています。

まだ片付かないものがあります。

- 同じ名前で二度と起動しない台の記録とコンテキスト。別の台が引き取る仕組み（台の生死を知る方法と、1 台だけが引き取るリース）は作っていません。
- TrGW（中継ノード）の側から見た、持ち主のいないコンテキスト。TS 29.238 の監査（Audit）に当たる仕組みはありません。
- 応答の前に止まった呼。記録にダイアログが無いので、TrGW のコンテキストだけを解放します。発信者の INVITE は時間切れで終わります。

#### 相手網でない送信元からの要求

IBCF は、相手網として設定していない送信元の要求に、応答せずに捨てます。実際の網の境界の装置がそう動くためです（[docs/design/media-control.md](../design/media-control.md)）。TS 24.229 V20.1.0 の 5.10 に、IBCF が送信元を理由に要求を応答なしで捨てる決まりは見つかりませんでした。5.10.1 は、局所的な方針で 4xx を返して断ることを認めているだけです。似た決まりは P-CSCF にあります。5.2.6.3.2A は、IP の対応付け（IP association）に当たらない REGISTER 以外の要求を、100 を返さずに捨てるとしています。

- 外側の UA のスタックは、どのサーバートランザクションにも当たらない要求を受けると、何かをする前に `IBCF.Screen` を呼びます。ACK と CANCEL も含みます。相手網でない送信元なら、`IBCF.Screen` は `"msg":"discarded a request from an address of no peer network"` をログに出し、`ims_ibcf_requests_discarded_total{reason="unknown_source",method}` を増やし、false を返します。スタックはその要求を捨てます。100 も、CANCEL への 200 や 481 も、ダイアログの外の OPTIONS への 200 も、許さないメソッドへの 405 も返しません。再送も、そのたびに捨てます。
- 内側の UA には渡しません。自網のノードは送信元で見分けないためです。
- 相手網からの要求を、プロトコルや取り決めの理由で断るときは、応答を返します（405、420、488、404、483、500 など）。
- 入口で `Peers` が同じアドレスに違う答えを返したときだけ、エラーのログを出して 500 を返します。誤りの応答を捨てると、トランザクションがスタックに残るためです。
- `IBCF.Screen` は、スタックの goroutine の中で `Peers.BySource` を引き、カウンタを足し、ログを出すだけです。`PeerTable` は作ったあとで変わらない一覧なので、ロックを取りません。カウンタは原子的な加算です。

#### 指標

`internal/ibcf/metrics.go` が、`Config.Metrics` のレジストリに 4 つのカウンタを登録します。名前は [oam.md](oam.md) の決まり（`ims_` で始め、counter は `_total` で終える）に合わせ、役割の名前を名前に入れました。ラベルの値の集合は設定した相手網の名前などで決まり、加入者の数では増えません。

| 名前 | ラベル | 数えるもの | TS 32.409 V19.0.0 |
| --- | --- | --- | --- |
| `ims_ibcf_session_attempts_total` | `peer`、`direction`（exit、entry） | 境界の向こうへ送った最初の INVITE | 4.11.1（entry）、4.11.3（exit） |
| `ims_ibcf_session_failures_total` | `peer`、`direction`、`status` | それに戻った 2xx 以外の最終応答 | 4.11.4（exit の 403） |
| `ims_ibcf_requests_rejected_total` | `peer`、`direction`、`method`、`status` | IBCF が自分で答えた要求。どの相手網にも向かわない出口の要求への 404 は `peer="other"` | 4.11.2（entry の INVITE への 403） |
| `ims_ibcf_requests_discarded_total` | `reason`（unknown_source）、`method` | 何も答えずに捨てた要求。相手網でない送信元の要求は、ここだけで数える | なし |

`method` の値は TS 29.165 Table 6.1 のメソッド、`status` の値は IBCF が返す応答コードとよくある失敗のコードです。ほかは `other` で数えます。UA が自分で返す応答（420、481、405 など）は数えていません。

### プリコンディション

TS 24.229 の 5.10 には、IBCF がプリコンディションのしくみに手を入れる手順がありません。TS 29.165 は、このしくみを II-NNI で任意とし（Table 6.1.3.1 の項目 20）、使うかどうかを事業者の取り決めの選択項目にしています（Table C.3.1.2）。RFC 3312 11 は、Supported に挙げるだけの offer に、しくみを持たない着信者がプリコンディションの無い answer を返すとしています。必須にした INVITE には、着信者が 420 を返します（RFC 3261 8.2.2.3）。どちらも呼の両端で決まり、境界の動きではありません。

そこでプロファイルの方針は次のようにしています。

- 方針 `reject` は、その tag を必須にした要求に 420（Unsupported 付き）を返すだけです。B2BUA の IBCF は、受けた側のダイアログの UAS なので、RFC 3261 8.2.2.3 の UAS の規則に当たります。Supported に挙げるだけの要求は、そのまま通します。
- プロファイルで precondition を `remove` にすることは、検証で断ります。
- SDP の `a=curr` と `a=des` を外す、境界でプリコンディションを終端する、といった作りは、確かめた仕様の本文に根拠が無いので作りません。

試験 `TestPreconditionsOutsideTheAgreement` が、方針 `pass` では網 B の UE が 420 を返すこと、`reject` では IBCF-A が Unsupported 付きの 420 を返し、Supported だけの INVITE は通ることを確かめています。

### 境界を越える会議

- GRUU（`gr` を持つ URI）か `isfocus` を持つ Contact は、そのまま通します（5.10.5）。INVITE を出す側では `Invitation.RecordRoute`、応答を返す側では `IncomingCall.SetContact` の `recordRoute` で、IBCF の台の URI を Record-Route に入れます。相手のダイアログの中の要求は、Request-URI が会議の URI で、Route に IBCF があります。IBCF はそのダイアログでそれを受け止め、もう一方のダイアログの remote target（届いたままの Contact）に自分の要求を送ります。B2BUA のままです。ほかの Contact は、IBCF の URI に相手の機能タグを付けたものです。応答ごとに設定します。
- ダイアログの外の SUBSCRIBE は、IBCF が境界の検査（取り決めに SUBSCRIBE が無ければ 405）のあとで、局所的な方針として 403 を返します（5.10.1）。TS 24.147 5.3.1.2 の参加者は 403 のときだけダイアログの中に切り替えるので、405 では相手網の会議を購読できません。数えて、ログに出します。知らないダイアログの中の SUBSCRIBE は 481 です。ダイアログの外の購読そのものを境界の両側で持つ作りは作っていません（`SIMPLIFIED:`）。
- Replaces を持つ INVITE では、Replaces が指すダイアログを自分の呼の片側として保存先から探します（`Store.CallOf`）。呼を持つのが別の台でも見つかります。見つかれば、もう一方の側のダイアログの Call-ID と tag に書き換えます。TS 24.229 5.10.5 は IMS-ALG の IBCF が 2 つのダイアログの識別子を対応づけるとし、5.7.5.1 は routeing B2BUA の AS が同じ書き換えをするよう定めています。見つからなければ、5.7.5.1 の AS と同じく届いたまま渡します。IBCF の先の B2BUA がそのダイアログを持っているかもしれないためです。保存先を読めなければ 500 を返します。early dialog は記録に無いので対応づけません（`SIMPLIFIED:`）。Refer-To の中の Replaces も対応づけません（`SIMPLIFIED:`）。

### フォークのときに発信側に見せるもの

IBCF の先で INVITE がフォークすると、向こう側に early dialog がいくつもできます。B2BUA の IBCF が発信側に見せる形には、2 つの案があります。

1. 向こう側の early dialog ごとに、発信側にも別の To の tag の early dialog を見せる。
2. 発信側には 1 つの early dialog だけを見せる。

2 を選びました。IBCF は発信側から見れば 1 つの UAS で、RFC 3261 8.2.6.2 は、UAS が 1 つの要求へのすべての応答に同じ To の tag を使うとしているためです。`internal/sip` の `IncomingCall` も、同時には 1 つの early dialog しか持てません（199 で終えて次の tag に移ることはできます）。TS 24.229 5.10.6.2 の NOTE 3 は、IBCF がフォークの複数の early media を扱う場合に触れていますが、B2BUA が何を見せるかは決めていません。向こう側の early dialog ごとに発信側にも別の early dialog を見せる SBC もありますが、そのためには `internal/sip` の UAS が同時に複数の To の tag で応答できる必要があります。

そのうえで、次のようにしています。

- 発信側の early dialog のセッション記述は、向こう側で最初にセッション記述を送ってきたダイアログのものです。発信側は最初の answer を取り、あとのものは無視するので（13.2.1）、ほかのダイアログの応答は、セッション記述を外して渡します。この扱いは `internal/b2bua` の中核が持ちます。
- 向こう側のダイアログごとに、最後にセッション記述を持っていた応答を To の tag で覚えます（`calleeSDP`）。
- 200 が来たら、そのダイアログに TrGW を向けます。200 に SDP があればそれで、無ければ覚えたもので設定します。応えたのが、発信側にセッション記述を渡したのと違うダイアログなら、200 はセッション記述を外して発信側に渡します。

試験 `TestForkBeyondTheBorder` は、網 B で INVITE を 2 つの枝に分け、どちらも信頼性のある 183 で answer を返し、片方が SDP の無い 200 を返します。どちらの枝が応えても、TrGW B は応えた枝を向きます。発信側が受けるセッション記述は 1 つだけで、どの応答も同じ To の tag です。

`SIMPLIFIED:`（RFC 3261 13.2.1、14.1）にしたのは次のことです。応えたダイアログが、発信側がセッション記述を受け取ったダイアログでないとき、IBCF は応えたダイアログのセッションを re-INVITE で発信側に offer し直しません。呼は最初のダイアログが選んだコーデックで続きます。offer の無い INVITE のあとでは、発信側が answer しなかったダイアログの offer には answer が返りません。

### PRACK と次の要求の順

出る側の PRACK と、発信側の次の要求が、逆の順で出ることがあります。

- 本文の無い PRACK（183 が answer を運び、PRACK は確認だけ）には、`internal/sip` の `acceptPRACK` が自分で 200 を返し、IBCF の手には渡りません。IBCF は `in.Progress` が戻ったあとで出る側の PRACK を送ります。その間に試験が割り込める場所が無いので、決まった順では再現できませんでした。
- answer を運ぶ PRACK と、offer を運ぶ UPDATE を続けて送る発信者では、UPDATE も TrGW を通るので、`mediaMu` で PRACK のあとに並びます。逆になりませんでした。
- SDP を持たない要求は、毎回再現しました。発信者が answer を運ぶ PRACK を送り、その 200 を待たずに INFO を送ります。RFC 3261 と RFC 3262 の読んだ範囲には、これを禁じる決まりがありませんでした。試験の TrGW が PRACK の answer の設定を止めているあいだに、INFO は TrGW を通らずに出る側へ出ていきます。20 回のうち 20 回、網 B に INFO が PRACK より先に届きました（`TestPRACKGoesBeforeTheRequestThatFollowsIt`、`-race -count=20`）。

今の作りでは、`internal/b2bua` の中核がダイアログの中の要求を、側ごとに届いた順に中継します。`internal/sip` が要求に届いた順の番号（`InDialogRequest.Arrival`）を振り、中核は、もう一方のレグが最終応答を返すまで次の要求を送りません。本文の無い PRACK も、`IncomingCall.RelayPRACK` で中核に渡るので、そのあとの UPDATE や INFO が先に出ることはありません。作りは [b2bua.md](../design/b2bua.md) にあります。

### 網をまたぐ SMS

10 章の案 A（SIP で網のあいだを通す）で作りました。

- `internal/sc`。`SetPeerNetworks` で ENUM と自網のドメインを渡すと、Sh で利用者が見つからない番号を ENUM で引き、自網の外のドメインの SIP URI が返れば、その URI に届けます。status report は、配送の結果が `Forwarded` のとき TP-ST 1（SC は転送したが、届いたかは確かめられない。TS 23.040 9.2.3.15）にします。
- `internal/ipsmgw`。設定 `IBCF` があり、宛先が自網の外の SIP URI なら、IBCF を Route の先頭に入れて送ります。送信者は IP-SM-GW の URI ではなく、番号で表した SC にします。自網のホスト名を外に出さないためです。相手網が 2xx を返した時点で配送を終え、SC に `Forwarded` を伝えます。受ける側では、着信側（P-Served-User の `sescase=term`）で届いた RP-DATA（網から MS の向き、SMS-DELIVER）を相手網からのショートメッセージとして受け、202 を返して利用者に届けます。RP-OA には相手網の SC を残します。UE からの RP-DATA（発信側）は、cause 98 で断ります。
- 着信側の MESSAGE を IP-SM-GW に送るフィルタ条件を、HSS の iFC に足します。P-Asserted-Identity が自網の IP-SM-GW であるものを否定の条件で除き、IP-SM-GW 自身の MESSAGE が戻ってこないようにします。網 A の IP-SM-GW が利用者に送る MESSAGE も、同じ S-CSCF の着信側の処理を通るので、条件が無いと、その MESSAGE が IP-SM-GW に戻ってループします。TS 23.218 V19.0.0 の 5.2.3 NOTE 5 は、AS が出した要求が同じ AS に戻るループに気をつけるよう求めています。P-Asserted-Identity の比べ方は部分一致なので、ポートの有無にかかわらず当たります。IP-SM-GW を何台にしても、どの台も同じホスト名を名乗るので、条件は 1 つで足ります。

標準の道は TS 23.204 6.4 の SMS-GMSC と MAP または SGd です。手順書と `SIMPLIFIED:` に書いています。TS 24.341 5.3.3.5.2 の multipart の本文、XML、Feature-Caps は使っていません。delivery report は境界を越えて戻りません。

どちらの向きでも、網 A の中の流れは同じ部品で動きます。網 B から網 A への向きでは、IBCF-B から I-CSCF へ、LIR、I-CSCF から S-CSCF へ、S-CSCF から IP-SM-GW へ（P-Served-User は `sescase=term`）、202 が網 B まで戻ります。続いて IP-SM-GW から I-CSCF、LIR、S-CSCF、P-CSCF、UE A1 へ SMS-DELIVER が届きます。この MESSAGE は IP-SM-GW に戻らず、P-CSCF へ進みます。UE A1 の delivery report（RP-ACK）は発信側の条件で IP-SM-GW に届きます。

IBCF-B は、P-Asserted-Identity の SC の tel URI を、網 B のドメインの SIP URI（`sip:+12025550150@ims.mnc002.mcc001.3gppnetwork.org;user=phone`）に書き直して送ります。取り決めの番号の形式が SIP URI だからです（TS 29.165 8.1）。

RP-OA を確かめる tshark のフィールドの名前は、版で違います。キャプチャのイメージの Wireshark 4.6.9 では `gsm_a.dtap.clg_party_bcd_num`、ホストの 4.2.2 では `gsm_a.dtap.cld_party_bcd_num` です。スクリプトはキャプチャのコンテナの中で tshark を動かすので、4.6.9 の名前を使います。

### 2 つ目の網の構成

ブリッジは 3 つです。網 A は `compose.yaml` の `ims0`（172.28.0.0/24）、網 B は `ims1`（172.28.1.0/24）、網のあいだは `nni0`（172.28.2.0/24）です。キャプチャも 3 つで、`captures/`、`captures/b/`、`captures/nni/` に書きます。`images/capture` は変えず、`CAPTURE_INTERFACE` と書き込む先のボリュームだけを変えます。

10 章の案では網 B を Compose のプロファイル `peer` に入れるとしていましたが、`compose.yaml` を変えずに済むように、別の上書きのファイル（`deploy/interconnect/`）にしました。網 B のサービスの名前は、`ibcf-b` や `scscf-b` のように `-b` で終わらせます。網 A の同じ役のノードと見分けるためです。

| 決めたこと | 中身 | 理由 |
| --- | --- | --- |
| 網 B の大きさ | HSS、3 つの CSCF、IP-SM-GW、DNS、UE 2 台。MMTel AS、EPC と PCRF、P-CSCF の中継ノード、ラボの画面は無い | シナリオに要る最小限。網 B の UE の RTP は TrGW-B に直接届く |
| 網 B の番号 | `+1 202 555 0150` から `0159`。UE は `0151` と `0152`、SC は `0150`、IP-SM-GW は `0159` | 網 A の番号（`0100` から `0112`、`0199`）と重ならず、ENUM のゾーン `5.1.0.5.5.5.2.0.2.1.e164.arpa` に収まる |
| 網 A の DNS | `deploy/compose/dns` のゾーンはそのままにし、`Corefile` だけを差し替える。IBCF-A と TrGW-A の名前は `hosts` プラグイン（`fallthrough` でゾーンへ渡す）、網 B の番号は別のゾーン、網のあいだの名前は `example.net` のゾーン | `deploy/compose/` を変えると `deploy/k8s/` の写しも作り直すことになるため |
| 網のあいだの名前 | `ibcf.nni.mnc001.example.net` と `ibcf.nni.mnc002.example.net`。網のあいだ専用の DNS は置かず、両方の網の DNS が持つ | ノードを増やさないため。JJ-90.32 の DNS はこの役割に当たると見られるが、本文は未確認 |
| プロファイルの切り替え | `IBCF_A_PROFILE` と `IBCF_B_PROFILE` で、`profiles/` のファイルを `/etc/ims/peer-profile.yaml` に載せる。IBCF は起動し直す | 取り決めをファイル 1 つで差し替えられ、ノードの設定は変わらない |
| 信頼 | 既定の 2 つのプロファイルは `trusted: true` | 相互接続の取り決めがある相手の網は信頼ドメインに入る（TS 24.229 4.4.1）。TS 29.165 は信頼関係があるときの P-Asserted-Identity を必須にしている |

役割 `ibcf` の設定は、内側の `sip` と外側の `external_sip`、`icscf`、`home_number_format`、`topology_hiding`（鍵、ドメイン、アドレスの範囲。プロキシで作った時の項目）、`trgw`（制御 API と側ごとのアドレス）、`peers`（`profile_file`、`entry_point`、`hosts`、`sources`）です。プロファイルは別のファイルにし、ノードの設定のファイルからの相対の道で指します。同じノードの中でポートが重なるかの確認は、アドレスとポートの組で行います。

S-CSCF の設定 `ibcf` は、ENUM の結果か Request-URI のホストが自網のドメインの外なら、IBCF を Route の先頭に入れます（TS 24.229 5.4.3.2 step 11、12）。AS が入れた Route、アドレスだけのホスト、自網のドメインとその下の名前は、そのままです。I-CSCF は、GRUU でなく、user part が `+` で始まり、`user=phone` を持つ SIP URI を tel URI に置き換えてから LIR を送ります（5.3.2.1 の項目 1 の b）。

### 食い違いのシナリオ

10 章の候補のうち、今のコードで失敗と成功を見せられる 3 つを作りました。

| シナリオ | 食い違い | 結果 | 直し方 |
| --- | --- | --- | --- |
| MESSAGE | 網 A の取り決めに MESSAGE が無い（`network-b-no-message.yaml`） | IBCF-A が 405。SC の status report は TP-ST 98 | `network-b.yaml` |
| コーデック | 網 B が AMR-WB だけを受ける（`network-a-amr-wb-only.yaml`） | IBCF-B が 488 | `network-a.yaml` |
| 番号の形式 | 網 A が国内形式の番号をそのまま通す（`network-b-local-numbers.yaml`） | IBCF-B が 404。10 章の表では網 B の I-CSCF が 404 を返すと書いたが、実際には先に網 B の IBCF が自分の取り決めで断る | `network-b-global-numbers.yaml`。IBCF-A が `+1` を付けて国際形式にする |

プリコンディションの食い違いは、場面として作りません。このプロジェクトの UE は INVITE の Supported に precondition を挙げるだけで、Require にしないので、420 にならず、決めるのは両端であるためです（前の「プリコンディション」）。トランスポートと信頼のシナリオも作っていません。トランスポートは、IBCF が UDP と TCP の両方で待つため、TCP だけの入口を作れません。信頼は、発信者番号が届かないだけで呼は失敗しないので、手順書でプロファイルを書き換えて試す方法を書きました。

### 試験

`internal/ibcf` の試験は、2 つの網を 1 つのプロセスの中で動かします。網ごとにループバックのアドレスを分け（127.0.1.0/24 が網 A の中、127.0.2.0/24 が網 B の中、127.0.3.0/24 が網のあいだ）、どの網が何を受けたかを見分けられるようにしました。Linux では 127.0.0.0/8 のどのアドレスでも待ち受けられます。待ち受けられない環境では、試験は失敗します。網 A には IBCF を 2 台置きます。鍵と `Store` を共有し、同じ名前と同じポートで、アドレスだけが違います。UE は `internal/sip` の UA、I-CSCF と S-CSCF は `internal/sip` の上の小さなプロキシ、TrGW は払い出したアドレスと設定を覚えるだけの偽物です。

| 試験 | 確かめること |
| --- | --- |
| `TestCallAcrossTheBorder` | 3 つのダイアログの Call-ID が別々で、網のあいだの INVITE の From の tag も発信者と違う。網のあいだの INVITE の Via は IBCF-A の 1 つだけで、Record-Route が無い。各 UE と網のあいだの Contact が、それぞれの側の IBCF の台の名前で、MMTel の機能タグを持つ。組の名前を 2 台目に移しても、re-INVITE は 1 台目に届く。着信者の UPDATE（offer 付き）が TrGW を通って発信者に届く。BYE で両側が終わり、コンテキストが解放される。新しい呼は 2 台目に届く。信頼しない相手に P-Asserted-Identity などが届かない。どちらの網も、相手網の中のアドレス、ノードの名前、UE の私的な識別子を受け取らない。指標の数 |
| `TestMessageAcrossTheBorder` | 3 つの MESSAGE の Call-ID が別々で、網のあいだの Via が 1 つ |
| `TestTrustedPeerGetsTheAssertedIdentity` | 信頼する相手には P-Asserted-Identity、P-Access-Network-Info、P-Charging-Vector が届き、P-Charging-Function-Addresses は届かない |
| `TestMismatchedProfiles` | 食い違ったプロファイルで 405（MESSAGE が取り決めに無い）、420（着信側がプリコンディションに対応しない）、488（共通のコーデックが無い）、404（国内形式の番号）になり、プロファイルを直すと 200 になる |
| `TestFailedLegEndsTheOtherLeg` | 網 B の UE が作り直されて呼を忘れると、re-INVITE に 481 が返り、網 B の IBCF がその脚を終え、もう一方の脚の BYE が網 A の IBCF を通って発信者に届く |
| `TestCancelReleasesTheTrGW`、`TestTimeoutReleasesTheTrGW` | CANCEL で両側が 487 になり、両方の TrGW のコンテキストが消える。相手網が答えないとき、Timer B のあとに 504 を返し、コンテキストが消える |
| `TestPreferredTransport` | 短い MESSAGE が、UDP を先に挙げた相手には UDP で、TCP だけを挙げた相手には TCP で届く |
| `TestRequestFromNoPeerIsDiscarded`、`TestScreen` | 網のあいだの知らないホストが送る INVITE（2 回）、CANCEL、ACK、MESSAGE に、何も返らない（100 も 481 も無い）。どれも網 A に入らず、TrGW のコンテキストも呼の記録もできない。捨てた数がメソッドごとに増える。相手網 B の MESSAGE は通る。待ち方は時間に頼りません。知らないホストの要求を送ったあとで、相手網 B の MESSAGE を同じ外側のソケットに通し、その 200 が返った時点で前の要求はすべて捨て終わっているとみなします |
| `TestRestartedInstanceEndsTheCallsItLeft`、`TestRestartedInstanceEndsACallAtItsNewTarget` | 前の「TrGW のコンテキストの後始末」。網 B の UE が re-INVITE で別の Contact に移ったあとで網 B の IBCF を作り直すと、BYE が新しい Contact に届き、古い Contact には届かない |
| `TestConferenceFocusAcrossTheBorder`、`TestConferenceFocusAnswersAcrossTheBorder` | 網 A のフォーカスが網 B の利用者を招く。網のあいだと網 B の INVITE が、`isfocus` 付きの会議の URI を Contact に持ち、各 IBCF が Record-Route にいる。ダイアログの中の SUBSCRIBE が両方の境界を越えて会議の URI に届き、フォーカスの NOTIFY が参加者に届く。ダイアログの外の SUBSCRIBE は IBCF-B が 403 で断り、取り決めに SUBSCRIBE が無ければ 405。網 B のフォーカスが 200 の Contact に `isfocus` を付けて応えると、発信者がその Contact と、IBCF-A の入った Record-Route を受け取る |
| `TestOfferInAReliable183AndAnswerInThePRACK`、`TestOfferInThe200AndAnswerInTheACK` | offer の無い INVITE の 2 つの道。各側の SDP と、TrGW の各終端に伝えた宛先 |
| `TestForkBeyondTheBorder`、`TestPRACKGoesBeforeTheRequestThatFollowsIt`、`TestPreconditionsOutsideTheAgreement` | 前の「フォーク」「PRACK と次の要求の順」「プリコンディション」の項 |

### tshark で確かめたこと

`TestCallAcrossTheBorder` で網 B のノードが受けたメッセージを、環境変数 `IBCF_CAPTURE_DIR` のディレクトリに 1 メッセージずつ書き出し、pcap に直して tshark で読みました。

```sh
mkdir -p /tmp/ibcf-cap
IBCF_CAPTURE_DIR=/tmp/ibcf-cap GOTOOLCHAIN=auto go test -count=1 -run TestCallAcrossTheBorder ./internal/ibcf
# ファイル名の送信元と宛先ごとに、メッセージを 16 進の行に直して 1 つの入力にまとめ、
# TCP は -T、UDP は -u で本物のポート番号を渡す
text2pcap -q -t "%H:%M:%S." -4 <送信元>,<宛先> -T <送信元のポート>,<宛先のポート> <入力> <出力>.pcap
mergecap -w b-call.pcap *.pcap
tshark -r b-call.pcap -V
```

- プロキシで作ったときの結果です。39 フレームのすべてが SIP として読まれ、そのうち 15 フレームに SDP があります。`Malformed` は 0 件です。`unknown` は 23 件ありますが、すべて text2pcap が作った TCP のヘッダの `Window size scaling factor` です。
- 網 A の中のアドレス（`127.0.1.`）は、デコードした結果のどこにもありません。SDP の `o=` と `c=` にあるのは、網のあいだでは網 A の TrGW の相手網の側（127.0.3.50）、網 B の中では網 B の TrGW の自網の側（127.0.2.50）と網 B の UE（127.0.2.10）だけです。
- プロキシのときの Via のトークンは、63 文字ごとに区切ったラベルのホスト名として `Sent-by Address` に読まれ、`tokenized-by=ims.mnc001.mcc001.3gppnetwork.org` も読まれました。
- 網のあいだの初期の INVITE は 2140 バイトで、TCP で送られました。

ポートをすべて 5060 にして text2pcap に渡すと、別の TCP の接続が同じ組に重なり、Wireshark が 1 つのフレームを再送とみなして SIP として読みませんでした。本物のポートを渡すと、SIP のヒューリスティックで全部が読まれます。

境界を越える会議の場面は Compose に無いので、`TestConferenceFocusAcrossTheBorder` が書き出したメッセージを、UDP の 5060 番のフレームにして pcap にし、tshark で読みました。網のあいだの INVITE の Contact は `<sip:conf-7f3a@conference.ims.mnc001.mcc001.3gppnetwork.org>;isfocus`、Record-Route は IBCF-A の外側の台の名前だけでした。網 B の UE に届いた INVITE の Contact も同じで、Record-Route は網 B の S-CSCF と IBCF-B の内側の台の名前です。ダイアログの中の SUBSCRIBE は、どの区間でも Request-URI が会議の URI でした。51 のメッセージに Malformed と警告はありません。

### e2e で確かめたこと

`scripts/e2e-interconnect.sh` は、2 つの網を起動し、次を確かめます。

- 網 A から網 B への通話。UE A1 が送った PCMU のペイロードを、UE B1 がそのまま受け取る。網 B から網 A への折り返しの通話。
- 網のあいだの INVITE の Via が IBCF-A の 1 つだけであること、Record-Route が無いこと、Contact が IBCF-A であること。S-CSCF から IBCF-A への INVITE、網のあいだの INVITE、IBCF-B から I-CSCF への INVITE の Call-ID の集合が重ならないこと。
- 網 A から網 B へのショートメッセージ。status report は TP-ST 1。UE B1 が本文を受け取る。網 B から網 A へのショートメッセージ。status report の TP-ST 1、UE A1 が本文を受けて delivery report を送ること。
- 3 つのシナリオ。
- 3 つのダイアログ（網 A の S-CSCF と IBCF-A、網のあいだ、網 B の IBCF-B と I-CSCF）それぞれで、INVITE、着信側からの 200、ACK、BYE（切った側から）、BYE への 200。網のあいだのダイアログで、信頼性のある 183、IBCF-A からの PRACK、PRACK への 200。tshark で見ると、網のあいだの SIP の順番は INVITE、183、PRACK、200、180、PRACK、200、200、ACK、BYE、200 で、各脚の CSeq は 1 から始まります（網のあいだの BYE は CSeq 4）。
- 網 A のキャプチャ。網 B の番号の ENUM、S-CSCF から IBCF-A への INVITE の Route、SC の番号から出る MESSAGE、IBCF-A から I-CSCF への INVITE、405。
- 網のあいだのキャプチャ。IBCF-A を指す Contact、TrGW-A と TrGW-B を指す SDP、TrGW どうしの RTP、RP と TP をデコードできる MESSAGE、404 と 488、P-Charging-Function-Addresses が無いこと。
- 網 B のキャプチャ。IBCF-B から I-CSCF への `user=phone` の INVITE、I-CSCF から S-CSCF への tel URI、TrGW-B を指す SDP、TrGW-B から UE B1 への RTP、IP-SM-GW に届く着信側の MESSAGE。
- 網 B のキャプチャと網のあいだのキャプチャに、172.28.0.0/24 のアドレスと網 A のノードのホスト名が無いこと。網 A のキャプチャと網のあいだのキャプチャに、172.28.1.0/24 のアドレスと網 B のノードのホスト名が無いこと。3 つのキャプチャに Malformed と警告が無いこと。
- 知らない送信元（172.28.2.99）から INVITE と MESSAGE を送り、何も返らないこと。IBCF-A が両方を捨てたとログに出すこと。網のあいだのキャプチャに両方の要求があり、172.28.2.99 への IP のパケットが無いこと。網 A のキャプチャにそのホストの From の要求が無いこと。
- すべての場面のあとで、TrGW-A と TrGW-B の制御の API（`GET /contexts`）が、コンテキストを 1 つも返さないこと。

`some` の先頭の行を取るときは、`tshark | head -n 1` が pipefail の下で SIGPIPE になりうるので、bash で取ります。

### SIMPLIFIED の一覧

`grep -rn SIMPLIFIED: internal/ibcf internal/interconnect` で一覧できます。主なものは次のとおりです。

| 場所 | 条項 | 省いたこと |
| --- | --- | --- |
| `ibcf/doc.go` | TS 24.229 5.10.2.4、5.10.3.4 | IBCF が自分から呼を終えるのは、片方のダイアログが失われたときと、作り直した台が前のプロセスの呼を片付けるときだけ。TrGW の異常は知らない。二度と起動しない台の記録は残る |
| `ibcf/doc.go` | TS 24.229 5.10.2.1、5.10.3.1、TS 29.165 5.3.1 | non-roaming だけ。REGISTER は 405、Path と復旧の手順（5.10.3.2 の P-CSCF、5.10.3.5 の S-CSCF）は無い |
| `ibcf/doc.go` | TS 24.229 5.10.2.2 step 5A、6、7、5.10.3.2 step 1A、1B、2C、2D、5 から 9 | 課金、私設網、緊急呼、発信者番号の検証が無いので、その手順を行わない |
| `ibcf/doc.go` | TS 24.229 6.7.1.4.1 | IPv4 だけ。IPv4 と IPv6 の変換と `altc` は無い |
| `ibcf/call.go` | RFC 3891 3 | early dialog を指す Replaces は、対応づけずに届いたまま渡す。記録にダイアログを書くのは 2 つとも確定してからで、early dialog を置き換えるノードは無い |
| `ibcf/doc.go` | RFC 3515 2.1 | Refer-To の中の Replaces を、もう一方のダイアログに対応づけない。そうした REFER を境界の向こうへ送る呼の転送（TS 24.629）は無い |
| `ibcf/call.go` | TS 24.229 5.10.5 | 3xx の Contact を写さない。3xx の Contact は次に試す宛先でダイアログのものではなく、このプロジェクトのノードは 3xx を返さない |
| `ibcf/call.go` | TS 24.229 5.10.2.2、5.10.3.2、RFC 4028 8 | IBCF からセッションタイマーを求めない。発信者の Session-Expires と Min-SE を写し、ダイアログごとに両端が決める |
| `b2bua/early.go` の `answerFrom` | RFC 3261 13.2.1、14.1 | 前の「フォーク」の項 |
| `ibcf/message.go` | TS 24.229 5.10.2.2、5.10.3.2 | ダイアログの外で越えるのは INVITE と MESSAGE だけ。OPTIONS は IBCF が答え、SUBSCRIBE は 403、PUBLISH は 405。ダイアログの外の購読を両側で持つ作りが無い。MESSAGE への暫定応答は渡さない |
| `ibcf/ibcf.go` の `peerOfNextHop` | TS 24.229 Annex I、RFC 4694 | ホストの無い Request-URI（tel URI、`rn` 付き）からは接続先を選べない |
| `ibcf/media.go` の `throughTrGW` | TS 24.229 6.7.1.4.1、RFC 3264 6 | 最初の音声のストリームだけを TrGW に通す |
| `ibcf/media.go` の `release` | TS 29.238 Table 5.17.2.1.1 | 解放に失敗しても再試行しない |
| `ibcf/recovery.go` | TS 24.229 5.10.2.4、5.10.3.4 | 同じ名前で二度と起動しない台の記録とコンテキストは残る |
| `interconnect/border.go`、`interconnect/trust.go` | TS 29.165 7.1、TS 24.229 5.10.8 | non-roaming だけ。応答の SDP にコーデックが残らないときに呼を切らない。multipart の中の SDP を絞らない |

### 残る課題

- MMTel AS は、まだ `internal/b2bua` の中核に載っていません。`internal/mmtel` の `call.go`、`focusrelay.go`、`fields.go` には、中核と同じ形の部品が残っています。移す順番は [b2bua.md](../design/b2bua.md) にあります。
- `ibcf.Store` の kvstore の実装。役割の `Store` の形が `context.Context` と `error` を取る形に変わったあとに、`internal/kvstore` の上に作ります。
- カウンタは、ノードのレジストリを `ibcf.Config.Metrics` に渡し、保守用の API の待ち受けで `/metrics` を出すまで、ノードの外から見えません。
- ダイアログの外の購読（SUBSCRIBE のダイアログ）を、IBCF が両側で持つこと。今は 403 で断り、参加者はダイアログの中で購読します。
- 会議の URI は `sip:<ID>@<AS のホスト名>:<ポート>` で、Compose では `mmtel.ims.mnc001.mcc001.3gppnetwork.org` です。5.10.5 により、IBCF はこの Contact をそのまま相手網に渡すので、相手網の会議の参加者になると、AS のノードの名前が相手網に見えます。網の構成を見せない名前（ドメインの PSI など）にするかは、未決の論点です。
- 複数の TrGW を使い分けるなら、`Media` にどの TrGW かを持たせる必要があります。コンテキストの ID が中継ノードごとの番号なので、[redundancy.md](redundancy.md) の中継ノードと同じ問題です。

## 14. 確かめられなかったこと

- GSMA IR.95 の本文と版。www.gsma.com が 403 を返しました。1 回試しただけで、ほかの入手先は探していません。
- TTC の JJ-90.30、JJ-90.31、JJ-90.32、JJ-90.26、JJ-90.27、JJ-90.28 の本文。www.ttc.or.jp が 503 を返しました。8 章の JJ-90.30 の内容は [profiles.md](profiles.md) の記録を写したもので、ほかの 5 つは題名しか分かっていません。
- TS 29.162（IBCF と TrGW の段階 2 の手順。IMS-ALG としての IBCF と TrGW の内部の手順）は読んでいません。5.10.5 はここを指しています。Ix の手順の名前は TS 29.238 の表から取りました。IBCF と TrGW のあいだの SDP の扱いの細かい点（6.7.1.4.1 が参照する部分）は確かめていません。
- TS 29.238 は、1 章と 5 章の表だけを読みました。IP の領域のパッケージ（ipdc、ipra）の中身は読んでいません。
- TS 29.165 の 12 章（補足サービスごとの要件）と Annex A、Annex B は、題名と目次だけです。
- TS 24.229 Annex A の表そのものは確かめていません。Annex I（追加の経路の機能）は題名だけです。
- TS 24.229 の、IBCF が SUBSCRIBE のダイアログを B2BUA として扱うときの手順（5.10.2.2 の step 2A の NOTE）は、SUBSCRIBE を通さないので読み込んでいません。
- TS 33.210（網のあいだのセキュリティ）は読んでいません。既定では暗号化しない方針なので、網のあいだも平文です。
- RFC 3323、RFC 3325、RFC 5658、RFC 7549、RFC 4769 は読んでいません。TS 24.229 と TS 29.165 の引用から書きました。
- TS 24.341 の、網をまたぐ MESSAGE に関わる Stage 3 の手順は、5.3.3.5 だけを読みました。7 章は TS 23.204 を中心に書いています。
- THIG で暗号化したホスト名を、`internal/sip` のパーサーと Wireshark がどう扱うかは、プロキシで作ったときの試験と tshark で確かめました。長いホスト名を DNS で引く経路は試していません。
- 試験の TrGW は偽物で、RTP を中継しません。網 A の偽物は、中継ノードと同じく両方のターミネーションに 1 つのアドレスを書き、IBCF が設定のアドレスに置き換えます。網 B の偽物は網ごとのアドレスを書き、IBCF はそれをそのまま使います。`agw.Client` そのものは、`internal/agw` の試験で本物の中継ノードに対して試しています。IBCF が `agw.Client` で本物の中継ノードを TrGW として使うこと（IBCF が使うターミネーションの名前と、設定のアドレスへの置き換え）は、`internal/ibcf/relay_trgw_test.go` で試しています。RTP が 2 つの TrGW を通ることは、Compose の e2e で確かめます。
- 同じ名前で作り直した台が残りを片付ける動きは、1 つのプロセスの中の試験でだけ確かめました。`ibcfstore` の上でも試験で確かめましたが、Valkey の代わりにプロセスの中の保存先を使っています。Compose で台を作り直す e2e は作っていません。Compose の網の IBCF には `store` の節が無く、プロセスと一緒に記録が消えるためです。Compose で通話中に IBCF を作り直したときの動き（ログの行が出ない、古い呼の要求に 481、相手に BYE が届かない、TrGW のコンテキストが残る）は、コードから書きました。Compose で実際に作り直しては確かめていません。
- 複数の IBCF の台を Compose で動かす構成は作っていません。2 台の振る舞いは、1 つのプロセスの中の試験で確かめました。
- Compose には、境界を越える会議の場面がありません。`internal/ue` の参加者が 403 のあとでダイアログの中の購読に切り替え、相手網のフォーカスの NOTIFY を受けるところは、通しでは動かしていません。
- 本文の無い PRACK と次の UPDATE の順が逆になる場合は、決まった順で再現できませんでした。起きないとは言えません。
- 127.0.0.1 以外のループバックのアドレスで待ち受けられない環境（macOS の既定など）では、網の試験は失敗します。CI の Linux では動きます。
- 相手網のノードを足したときの、Compose の起動の時間とメモリの量は測っていません。
- `THIG` の正式な名前（略さない形）は、読んだ TS 24.229 と TS 29.165 の中に見つけられませんでした。用語集には「network topology hiding」と書いています。
