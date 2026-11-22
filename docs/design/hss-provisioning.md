# HSS のプロビジョニング API

この記録は、HSS が持つ加入者のプロビジョニングの API を決めます。1 件ずつの操作、ページに分けた一覧、一括の登録、書き出しと読み戻し、試験用の利用者の印、サービスのプロファイルの選び方、収容の方針が対象です。OAM がこの API を使います。OAM 自体の作りは [lab-oam.md](lab-oam.md)、状態の置き場所とページに分けた一覧の仕組みは [state-redundancy.md](state-redundancy.md)、調査は [oam.md](../background/oam.md) の 7 章から 9 章にあります。

## 選んだこと

- API は HSS の側で 1 つに決めます。OAM は HSS が持つ口に合わせ、HSS が持たない口を当てにしません。取りかかる前の OAM は、`PUT` と `PATCH`、書き出し、`test_user` と `service_profile` の項目、`hss.service_profiles` と `hss.admission` の設定を当てにしていて、それらを持つのは OAM の試験の中の偽の HSS だけでした。
- 1 件の操作は HSS の HTTP の API に置きます。`GET`（一覧と 1 件）、`POST`（追加）、`PUT`（作成か置き換え）、`PATCH`（印とプロファイルの名前だけ）、`DELETE` です。
- 一括の登録は OAM が受け持ちます。HSS に一括の口は足しません。OAM が 1 行を 1 回の `PUT` にして送ります。
- 書き出しは `GET /subscriptions/export` です。1 行に 1 件の JSON で、資格情報、SQN_HE、Sh の Repository Data を含みます。
- ページの分け方は API の全体で 1 つにします。`after` と `limit` で位置と件数を受け取り、続きがあるときだけ `Link` ヘッダの `rel="next"` で返します。一覧も書き出しも同じ決まりです。
- 試験用の利用者の印（`test_user`）とサービスのプロファイルの名前（`service_profile`）を、加入者のデータの項目にします。判定はその加入者のデータを引いたときに行うので、索引も全件の走査も増えません。
- サービスのプロファイルは、設定 `hss.service_profiles` で名前を付けて定義します。SAA の User-Data には、選んだプロファイルの iFC を入れます。プロファイルの名前は Cx に出ません。
- 収容の方針（`hss.admission`）は HSS の設定の項目にし、保守用の API で動かしたまま変えられる項目にします。断るのは UAR で、UAA に DIAMETER_AUTHORIZATION_REJECTED を入れます。

## API

本文と答えは JSON です。誤りは `http.Error` の文のままで、OAM がそれを読んで理由にします。加入者を読み書きできなかった要求には 500 を返します（[state-redundancy.md](state-redundancy.md) の「Store の形と書き込みの決まり」）。経路にない方法には、`http.ServeMux` が `Allow` を付けた 405 を返します。

| メソッドと経路 | 本文 | 答え | 状態コード |
| --- | --- | --- | --- |
| `GET /subscriptions?after={IMPI}&limit={n}` | なし | 加入の配列（資格情報を含まない）。続きがあれば `Link` | 200、400（`limit` の形）、500 |
| `GET /subscriptions/{IMPI}` | なし | 加入 1 件 | 200、404、500 |
| `POST /subscriptions` | 加入の要求 | 加入 1 件と `Location` | 201、400、409（識別子が別の加入のもの）、500 |
| `PUT /subscriptions/{IMPI}`（`?dry_run=true` を付けられる） | 加入の要求に `repository_data`、`test_user`、`service_profile` を足したもの | `{"result": "created" \| "updated" \| "unchanged", "subscription": {…}}` | 201（作ったとき）、200（置き換え、変化なし、`dry_run`）、400、409、500 |
| `PATCH /subscriptions/{IMPI}` | `{"test_user": true, "service_profile": "trial"}`。どちらも省ける | 加入 1 件 | 200、400、404、500 |
| `DELETE /subscriptions/{IMPI}` | なし | なし | 204、404、500 |
| `GET /subscriptions/export?after={IMPI}&limit={n}` | なし | 1 行に 1 件の JSON（`application/x-ndjson`）。資格情報、SQN_HE、Repository Data を含む。続きがあれば `Link` | 200、400、500 |
| `PUT /subscriptions/{IMPI}/customized-alerting-tone` | CAT | CAT | 200、400、404、500 |

- `PUT` は置き換えです。項目ごとの更新ではありません。本文に書かれていない加入者のデータは消えます。`repository_data`、`mmtel_services`、`ut_password` を 3 つとも省いた `PUT` は、`POST` と同じく、設定の既定の MMTelServices だけを入れます。利用者が Ut で変えた補足サービスの設定は、そのような `PUT` で設定の既定に戻ります。書庫を戻す操作を「書いてあるとおりにする」にするためです。
- `PUT` が残すのは、動いているあいだの状態です。登録の状態、S-CSCF の名前、認証の途中の印、IP-SM-GW の番号、SQN_HE は、置き換えても変わりません。`aka.sqn` を読むのは、新しい加入者を作るときだけです。
- `unchanged` は、置き換えても加入者のデータが変わらないことです。資格情報と Repository Data も比べます。比べないのは、上の行で残すものです。OAM は資格情報を読み戻せないので、同じかどうかを言えるのは HSS だけです。
- `PATCH` があるのは、資格情報を知らずに印とプロファイルの名前だけを変えるためです。HSS は資格情報を返しません（TS 29.228 Table 6.3.5、Table 6.3.7）。`PUT` で印だけを変えるには、持っていない資格情報を書く必要があります。
- `dry_run` を付けた `PUT` は、何も変えずに同じ答えを返します。`DELETE` の試しは、OAM が `GET` で加入の有無を見るので、HSS は何もしません。

### `PUT` が `Store` に書く順

1 件の操作は、どれもその加入者のロックの中で、その加入者のデータだけを書きます。`PUT` は、今の `Store` の操作では足りないので、`Store` に `Replace` を足します。`Put` は版を見ない書き込みで、呼ぶ側がロックを持つことを求め、保存されている AKA をそのまま残すので（`internal/hss/store.go` と `internal/kvstore/hssstore`）、資格情報と公開の識別子を書き換えられません。

- `PUT` は、その加入者のロックを取り、加入者を読み直し、置き換えるかどうかを決めてから `Replace` を呼び、ロックを放します。`Add` と `Delete` と同じく、公開の識別子の索引は主の項目より先に書き、要らなくなったものは主の項目より後に消します（[state-redundancy.md](state-redundancy.md) の「索引」）。識別子が別の加入のものなら、何も書かずに 409 を返します。
- Repository Data は、`CompareAndSwapRepositoryData` で Service-Indication ごとに書きます。`Replace` には入れません。Sh の PUR はロックを取ってから比べて入れ替え、CAT の `PUT` はロックを取らずに比べて入れ替えるので（`internal/hss/provisioning.go` の `setCustomizedAlertingTone`）、版を見ずに書くと、どちらの更新も黙って消えます。書く値には、Sh の更新と同じく次の順序番号を与えます（TS 29.328 6.1.2.1）。入れ替えが負けたら、CAT の `PUT` と同じく読み直してやり直します。
- 加入者のロックを保存先に置く構成では、ロックの期限は 15 秒で、期限のあとに動き出した古い台の書き込みを止める仕組みがありません（[state-redundancy.md](state-redundancy.md)）。処理の途中で 15 秒より長く止まった台の `PUT` は、次の台の書き込みを上書きすることがあります。この制約は、その節が `SIMPLIFIED:` に書くとしたもので、`PUT` にもそのまま当てはまります。

## ページの分け方

- `after` は前のページが終わった IMPI、`limit` は返す件数です。`limit` を省くと 1000 件で、それより大きい値も 1000 件になります。`after` の加入者が消えていても、その次から返します。
- 続きのページがあるときだけ、`Link` ヘッダに次のページの問い合わせを入れます（RFC 8288 3）。最後のページの見分け方は、このヘッダの有無だけです。ちょうど `limit` 件のページには必ずヘッダが付くので、次のページが空になることがあります。
- 1 ページを読む手間は、加入者が 100 人でも 100 万人でも変わりません。保存先に置く実装は IMPI の並べ替えた索引を範囲で引き、`MemoryStore` は並んだ列を 2 分探索します（[state-redundancy.md](state-redundancy.md) の「並べ替えた索引」）。
- 書き出しも同じ `after`、`limit`、`Link` を使います。違うのは本文の形だけです。OAM は 1 行ずつ読んで書庫に書くので、ページの全体をメモリに持ちません。

## 一括の登録と書き出し

一括の登録は OAM の `POST /oam/v1/provisioning` です。HSS は 1 件ずつの `PUT` を受けます。

| 主題 | 作り |
| --- | --- |
| 形式 | 1 行に 1 件の JSON（JSON Lines）。項目は `PUT` の本文と同じです。`"delete": true` の行は削除で、ほかの項目を持ちません。削除は OAM が `DELETE` に直すので、この項目は HSS に送りません。 |
| 大きさの上限 | 1 行は 64 KiB です。HSS が受ける本文の上限と同じにします。行の数に上限は設けません。OAM は 1 行ずつ読み、同時に送るのは既定で 8 件までなので、使うメモリは行の数によりません。 |
| 不可分の単位 | 加入者 1 件です。1 回の `PUT` が、その加入者のロックの中で、その加入者のデータだけを書きます。複数の加入者にまたがる不可分な書き込みは作りません。保存先の 1 回の操作が 1 つのキーだけを触る決まり（[state-redundancy.md](state-redundancy.md)）に合わせるためです。 |
| 途中の誤り | 止めません。行ごとに結果（`created`、`updated`、`unchanged`、`deleted`、`rejected`、`failed`）と理由を返し、最後に集計を返します。`rejected` は送り直しても同じで、`failed` は送り直せば通るかもしれないものです。同じファイルを送り直すとすべて `unchanged` になるので、`failed` の行だけを直して送り直せます。 |
| 行の順 | 結果は行の順に返します。HSS に届く順は決まっていません。1 つのファイルは、1 つの加入を 1 回だけ書きます。 |
| 書き出しの中身 | `PUT` の本文と同じ項目に、`password`、`aka`（`k`、`opc`、`amf`、`sqn`）、`repository_data`、`test_user`、`service_profile` を含めます。Ut の資格情報は、HSS が H(A1) しか持たないので、`ut_password` ではなく `repository_data` の中の Ut-Credential として出ます。CAT も Repository Data なので一緒に出ます。 |
| 書き出しに入れないもの | 登録の状態と S-CSCF の名前です。戻すと S-CSCF が持つ登録と食い違います。戻した加入者は Not Registered で、利用者が登録し直します。 |
| 一貫性 | 書き出しのあいだ、全体のロックは取りません。1 件ごとには整合していて、全体には書き出しのあいだの変更が混じります。手順書に書きます。 |
| 書き出しの宛先 | 書き出しは、K、OPc、SIP Digest のパスワード、SQN_HE を、8081 番に届く相手なら誰にでも返します。プロビジョニングの待ち受けは内部の網だけに置き、ホストにもインターネットにも公開しません。Compose の `hss` のサービスはポートを 1 つも公開せず、同じ網の中の 172.28.0.10:8081 で届きます。公開しているのは、P-CSCF の 5060 番、中継ノードの RTP、学習用の画面の 8080 番で、どれも 127.0.0.1 に結び付けています。Kubernetes の `hss` の Service は `clusterIP: None` で、クラスタの中だけです。LoadBalancer の Service は P-CSCF の SIP と RTP のものだけです。手順書に書きます。 |
| SQN_HE | 書き出した値をそのまま戻します。古い SQN_HE で動き出すのは、消えた加入者を書庫から作り直したときだけです。加入者が残っていれば、`PUT` は持っている SQN_HE を残すためです。 |
| 古い SQN_HE | HSS が古い SQN_HE からベクタを作ると、UE が AUTS を返し、HSS が同期を取り直します（TS 33.102 6.3.5）。この様子はキャプチャで見られます。ただし、UE が配列の仕組みを使うときは、受け取った SQN は、その IND の要素に入っている番号より新しければ受け入れられます（TS 33.102 C.2.2）。全体でいちばん新しい番号より古くても受け入れられるので、作り直した加入者が必ず再同期を見せるとはかぎりません。この UE シミュレータも配列を持っています（`internal/aka/usim.go`）。 |

## 試験用の利用者の印と収容の方針

| 主題 | 作り |
| --- | --- |
| 印 | 加入者のデータの `test_user` です。設定のファイル、`POST`、`PUT`、`PATCH` で付けられます。`hss.Subscription` の項目なので、保存先に置く実装では加入者の記録が 1 項目増えるだけで、キーも索引も増えません。 |
| 方針の置き場所 | HSS の設定の `hss.admission`（`all` か `test_users`）です。保守用の API で動かしたまま変えられる項目にします（`maintenance.NewSetting`）。OAM が役割のすべての台に同じ世代で配り、変更を履歴に残し、前の値に戻せます。設定の変更を記録して前の設定に戻せることが要件にあるためです（[requirements.md](../requirements.md) の「運用と保守（OAM）」）。 |
| 断る場所 | UAR です。方針が `test_users` で、加入者の `test_user` が false なら、UAA の Result-Code を DIAMETER_AUTHORIZATION_REJECTED にします。TS 29.228 6.1.1.1 の step 5 が、登録を認可されていない利用者にこの結果を求めています。この値は基本のプロトコルの Result-Code で、TS 29.229 6.2 にはありません（RFC 6733 7.1.5、`internal/cx/cx.go` と同じ）。I-CSCF は、否定の UAA に 403 を返します（TS 24.229 5.3.1.3。できています）。 |
| 断る範囲 | HSS が登録を持っていない利用者の UAR です。能力を求める要求（REGISTRATION_AND_CAPABILITIES）も、同じ検査を通ってから答えます。6.1.1.1 の step 5 は、この 2 つの枝だけで登録の認可を確かめ、DE_REGISTRATION の枝では確かめません。すでに登録している利用者と DE_REGISTRATION の扱いは、「決めていないこと」で決めます。 |
| 今のコード | `internal/hss/hss.go` の `userAuthorization` にある `SIMPLIFIED:`（6.1.1.1 の step 5。この HSS はすべての利用者の登録を通す）を、この検査に置き換えます。 |

作り直した HSS の台は、設定のファイルの値で起動します。OAM は、あるべき世代より古い台を見つけて、今の設定を送り直します（TS 32.600 4.2.1.3 の監査）。送り直すまでのあいだ、その台だけが前の方針で答えます。「残っている課題」に書きます。

## サービスのプロファイル

| 主題 | 作り |
| --- | --- |
| 定義 | 設定 `hss.service_profiles` が、名前ごとに iFC の一覧を持ちます。各 iFC に、運用者がその機能を呼ぶ名前（`feature`）を付けられます。プロファイルの機能の一覧は、その名前を重複なく集めたものです。機能の一覧を別に書かないので、iFC と機能の一覧が食い違いません。`feature` は設定の層だけのもので、`ifc.InitialFilterCriteria`（TS 29.228 Annex B の XML）には入れません。 |
| 今の設定の移し方 | 今の `hss.initial_filter_criteria` を、`hss.service_profiles` の `default` に移します。2 つの書き方は残しません。`deploy/compose`、`deploy/k8s`、`deploy/redundancy`、`deploy/interconnect/b` の設定を同じコミットで移します。 |
| 加入者のプロファイルの決め方 | 加入者に `service_profile` があればそれ、無くて `test_user` なら `hss.test_user_service_profile`、ほかは `hss.default_service_profile` です。2 つの名前の設定も、動かしたまま変えられる項目にします。機能を全員に広げるときは `hss.default_service_profile` を変えます。 |
| SAA | User-Data に、選んだプロファイルの iFC と、加入の識別子（barring を含む）を入れます（TS 29.228 6.1.2.1、Annex B.2）。S-CSCF が User-Data-Already-Available で持っていると言えば、プロファイルを返しません（6.6）。 |
| 名前の扱い | プロファイルの名前は Cx に出ません。User-Data の XML の `ServiceProfile` は、公開の識別子、CoreNetworkServicesAuthorization、iFC、Extension だけを持ち、名前の要素がないためです（TS 29.228 Annex E の Table E.2、Annex B.2 の Figure B.2.1）。S-CSCF が見るのは iFC だけです。 |
| 無い名前 | `POST` と `PUT` は、`hss.service_profiles` に無い名前を 400 で断ります。2 つの名前の設定の変更も、無い名前を断ります。 |
| 今のコード | `internal/hss/profile.go` の `SIMPLIFIED:`（すべての加入に同じ iFC を渡す）は外れます。ServiceInfo の MSISDN の置き換えは残ります。 |

機能を試験用の利用者だけに有効にするのは、その機能の iFC を持つプロファイルを `hss.test_user_service_profile` にすることです。ほかの利用者には、その AS を呼ぶ iFC が渡りません。キャプチャには、試験用の利用者の呼にだけ AS への INVITE が写ります。

## OAM の変更

OAM の API（`/oam/v1/…`）と画面（`web/oam`）は変わりません。変わるのは、HSS を呼ぶ側だけです。

- `internal/oam/hssclient.go` と `records.go` のコメントから、「`internal/hss` に無い」という断りを外します。
- `hssError` が 404 と 405 を「この操作が無い」と読む分岐を外します。404 は「その加入が無い」だけになります。
- 書き出しのページの終わりを、`Link` ヘッダの有無で判断します。今は、返った件数が `limit` 未満かどうかで判断しています。ちょうど `limit` 件のページには必ず `Link` が付くので、最後に 0 件のページを読むことがあります。OAM は、それを誤りにせず、書き出しの終わりとして扱います。件数で判断する今の道は、HSS が `Link` を返すようになるまで残し、同じ `export` の中で両方を見ます。
- `internal/oam/fakehss_test.go` の `basic`（`PUT`、`PATCH`、書き出しを持たない HSS）と、それに対して 502 を確かめる試験を外します。HSS が持つ操作なので、持たない HSS は試験の相手になりません。
- 学習用の画面（`web/lab`）と手順書は、新しい項目と経路を書きます。手順書は英語と日本語の両方を同じコミットで直します。

## 採らなかった案

| 案 | 採らなかった理由 |
| --- | --- |
| HSS に `POST /subscriptions/batch` を足す（調査の 7 章の案） | 1 行ずつ読み、順に結果を返し、同時に送る件数を抑える仕組みは、OAM にすでにあります。HSS にもう 1 組作ると、結果の語彙と大きさの上限が 2 か所になります。1 件の `PUT` はその加入者のロックだけを取るので、一括でも役割全体を止めるロックは要りません。 |
| 一覧に資格情報を出す問い合わせ（`GET /subscriptions?credentials=true`） | 間違って付けると、学習用の画面に資格情報が出ます。本文の形も違います（JSON の配列と、1 行に 1 件）。経路を分ければ、どちらを呼んでいるかが入り口で分かります。 |
| `POST` をやめて `PUT` だけにする | `POST` は識別子が取られていれば 409 で断る「追加」、`PUT` は送り直せる「置き換え」で、使う場面が違います。学習用の画面と手順書は追加を見せます。検査と変換は 1 つの関数で共有するので、写しにはなりません。 |
| `PUT` を項目ごとの更新にする | 書庫を戻す操作が「書いてあるとおりにする」になりません。印とプロファイルの名前だけを変えるには `PATCH` があります。 |
| `hss.admission` を保存先に置く | 設定の出どころが 2 つになり、同じ項目を変える道が 2 つできます。保存先の値には世代が無いので、OAM の履歴と戻しが効きません。作り直した台がすぐ同じ値を読める利点はありますが、OAM の監査で埋まる差です。 |
| 識別子の範囲（IMSI の先頭の桁など）で試験用の利用者を決める | 1 人ずつ足したり外したりできません（調査の 9 章）。 |
| 各ノードの設定に試験用の利用者の一覧を書く | ノードの数だけ写しができ、利用者の数に比例して設定が増えます（調査の 9 章）。 |
| 戻す SQN_HE に幅を足す（調査の 8 章の案） | 足す幅の根拠がありません。AUTS による同期は実装してあるので、古い SQN_HE は UE の再同期で直り、その様子は手順書の題材になります。 |

## 作らないもの

- 登録中の利用者へのプロファイルの push（PPR、TS 29.228 6.2.2、6.6.1）。「決めていないこと」の答えで範囲を決めます。
- 1 つの加入に複数のサービスプロファイルを持たせること。公開の識別子ごとのプロファイルと、複数の暗黙の登録の集合は、`internal/hss/store.go` の `SIMPLIFIED:` のままです。
- プロファイルの中身（iFC）を動かしたまま変えること。設定のファイルの項目にします。変えられるのは、どのプロファイルを使うかだけです。
- プロビジョニングの API の認証と認可。保守用の API と OAM の API と同じく、平文の HTTP で動かし、内部の網からだけ届くことに頼ります（`internal/oam` のパッケージのコメントの `SIMPLIFIED:`）。
- 書き出した内容の暗号化。試験用の値だけを使う決まりがあるためです（調査の 8 章）。
- 加入者を複数の HSS に分けて置くこと（SLF）。HSS は状態を共有するサーバーファームです（[state-redundancy.md](state-redundancy.md)）。

## 決めていないこと

次の 5 つは、運用の方針に関わるので、メンテナーに確かめてから決めます。収容の検査を入れる作業は、1 と 2 の答えを待ちます。

1. 収容の方針を `test_users` にしたとき、すでに登録している試験用でない利用者をどうするか。案は次のとおりです。(a) 何もしない。登録は期限まで続き、再登録も通る。(b) 再登録の UAR も断る。利用者は期限が切れたときに落ちる。(c) RTR を送って登録を解除する。
2. User-Authorization-Type が DE_REGISTRATION の UAR を断ることがあるか。TS 29.228 6.1.1.1 の step 5 は、この枝で登録の認可を確かめないので、断らない形が仕様に沿います。断ると、試験用でない利用者は登録を終えられません。
3. 試験用でない利用者への着信（LIR）と、未登録の利用者へのサービス（SAR の UNREGISTERED_USER）も断るか。断ると、試験用でない利用者は呼び出せなくなります。断らないと、登録はできないのに着信の手順は進みます。
4. 加入者の `test_user` を変えたとき、すでに登録している利用者にいつから効くか。印はプロファイルの選び方と収容の両方に関わります。次の登録からにするか、1 と同じ手段（再登録を断る、RTR を送る）で今の登録に効かせるかを決めます。
5. 登録中の利用者の `service_profile` を変えたとき、S-CSCF にすぐ知らせるか（PPR）。TS 29.228 6.6.1 は、登録または未登録の識別子のプロファイルが変わったとき、HSS が完全なプロファイルをただちに push することを求めています（should）。知らせないなら、S-CSCF は次の登録まで前のプロファイルで動きます。PPR の手順は TS 29.228 6.2.2 です。

## 作る順番

1 つの PR は、1 つの確かめ方で終わる大きさにします。

1. この設計の記録。
2. 名前の付いたサービスのプロファイル。`hss.service_profiles` と 2 つの名前の設定を足し、SAA が選んだプロファイルの iFC を返すようにします。設定の読み取り（`internal/config/ifc.go`、`roles.go`）、その試験（`internal/config/ifc_test.go`）、4 つの設定ファイル（`deploy/compose`、`deploy/k8s`、`deploy/redundancy`、`deploy/interconnect/b`）、この鍵を載せている手順書（`docs/guide/en/sms.md` と `docs/guide/ja/sms.md`）を、1 つのコミットで直します。分けると、どこかの段階で設定が読めなくなるためです。確かめ方は `internal/hss` と `internal/config` の試験と、登録と通話の e2e で信号が変わらないことです。
3. 加入者のデータの `test_user` と `service_profile`。設定のファイル、`POST`、`GET` の答え、`Store` の形に足し、プロファイルの選び方をこの 2 つで決めます。確かめ方は `internal/hss` と `internal/kvstore/hssstore` の試験です。
4. `PUT /subscriptions/{IMPI}`（`dry_run` と `repository_data` を含む）。`Store` に `Replace` を足し、`MemoryStore` と `internal/kvstore/hssstore` の両方に実装します。確かめ方は、作成、置き換え、`unchanged`、409、SQN_HE と登録の状態が残ること、Sh の更新と同時に走らせても取り違えないことの試験です。
5. `PATCH /subscriptions/{IMPI}`。確かめ方は、資格情報が変わらないことの試験です。
6. `GET /subscriptions/export`。確かめ方は、書き出した内容を `PUT` で戻すと `unchanged` になることの試験です。
7. `hss.admission` と UAR の検査。設定のファイルの項目として入れ、`SIMPLIFIED:` を置き換えます。「決めていないこと」の 1 と 2 の答えが要ります。確かめ方は `internal/hss` の試験と、試験用でない利用者の REGISTER が 403 になる e2e です。この e2e は Compose と pcap を使うので、手元では流さず、PR の CI の結果で確かめます。
8. `hss.admission` と 2 つのプロファイルの名前を、保守用の API で変えられる項目にします。HSS の役割が `internal/maintenance` をつなぎ、設定の変更を受けられるようになってからにします。その前では、変えられる項目を載せる先がありません。確かめ方は、OAM から方針を変え、履歴から戻せることです。
9. OAM を HSS の API に合わせます（上の「OAM の変更」）。偽の HSS の `basic` をなくすのはここです。
10. 手順書（英語と日本語）と学習用の画面。

## 読んだ仕様

この記録の条項は、次の版の本文で確かめました。ETSI TS 129 228 V19.2.0（6.1.1.1、6.1.2.1、6.2.2、6.6、6.6.1、Table 6.3.5、Table 6.3.7、Annex B.2、Annex E の Table E.2）、ETSI TS 129 229 V19.1.0（6.2 に基本の Result-Code が無いこと）、ETSI TS 124 229 V19.7.0（5.3.1.2、5.3.1.3）、ETSI TS 133 102 V19.1.0（6.3.5、C.2.2）、RFC 6733（7.1.5）です。

## 残っている課題

- 作り直した HSS の台は、OAM が今の世代を送り直すまで、設定のファイルの収容の方針で答えます。そのあいだ、役割の台のあいだで方針が食い違います。
- 一括の登録は、OAM が 1 台の HSS に送ります。宛先に組の名前を書けば、DNS が台を選びます。行ごとに台を選び分けることはしません。
