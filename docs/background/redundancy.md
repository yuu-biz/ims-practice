# 冗長構成、復旧手順、障害注入、ドレインの調査

この文書は、IMS のノードを複数台で動かし、止まったあとにサービスを戻すことについて、仕様が定めることと、この実装の作りを調べた記録です。IMS の復旧手順（TS 23.380）、SIP と Diameter での切り替え、状態をノードの外に置く方式、障害注入、ドレインが対象です。決めた設計は [状態と冗長構成の設計](../design/state-redundancy.md) にあります。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは、表「読んだ資料」の版の条項です。

対象の要件は、同じ役割のノードを複数台動かせること、通話に影響を出さずにノードを切り離せること（ドレイン）、障害を起こして観察できること、IMS の復旧手順を再現できること、ノードが入れ替わっても登録と確立済みの呼を保つこと、状態をノードの外に置けること、各役割を水平に広げられることです（[要件定義](../requirements.md)）。コードについての記述は、網間接続まで実装した時点のものです。「今の」と書いた所は、その時点のコードを指します。

## 読んだ資料

| 仕様 | 版 | 読んだ範囲 |
| --- | --- | --- |
| TS 23.380（IMS の復旧手順） | V20.0.0 | 4 章の全部、5.0 から 5.5.2 |
| TS 24.229 | V20.1.0 | 4.14、5.1.1.4.1、5.1.2A.1.6、5.2.6.3.2A、5.2.6.4.1、5.3.1.3、5.3.2.2、5.4.3.2、5.4.3.3、7.2.11 |
| TS 29.228（Cx の手順） | V20.0.0 | 6.1.2、6.1.2.1、6.1.4、6.4、7.21、7.22、7.28 |
| TS 29.229（Cx の符号化） | V19.1.0 | 6.2.2.7、6.2.2.12、6.3.15、6.3.24、6.3.46 から 6.3.52、6.3.63、Table 7.1.1、AVP の表 |
| TS 29.214（Rx） | V20.0.0 | 4.4.7 |
| RFC 3263（SIP サーバーの探し方） | 本文 | 4 章、5 章 |
| RFC 3261 | 本文 | 16.6 の step 4、17.1.1.2、17.1.2.2、17.1.4、21.5.4 |
| RFC 3327（Path）、RFC 3608（Service-Route） | 本文 | RFC 3327 の 5.3、RFC 3608 の 6 章の見出し |
| RFC 6733（Diameter） | 本文 | 2.8.3、5.5.4、6.1.8、6.12、6.13 の見出しと 6.1.8 |
| RFC 3539（Diameter の監視） | 本文 | 3.4.1、付録 A の状態の一覧 |

外部の保存先のライセンスは、各プロジェクトのリポジトリのライセンスの文書で確かめました（4 章）。

## 1. TS 23.380 の復旧手順

TS 23.380 は、IMS のノードが止まったあとに、サービスを戻す手順を決めた文書です。Cx の側は TS 29.228 と TS 29.229、SIP の側は TS 24.229 にあります。S-CSCF の復旧と、HSS を使う P-CSCF の復旧は任意の機能で、Cx では対応するかを Supported-Features で伝えます。Cx の機能の表（TS 29.229 Table 7.1.1）では、ビット 2 が IMSRestorationInd（IMS の復旧）、ビット 3 が P-CSCF-Restoration-mechanism（HSS を使う P-CSCF の復旧）です。今の `internal/diameter/cx.xml` には Supported-Features の AVP（628 から 630）がありません。

### S-CSCF の障害

仕様は、S-CSCF が戻らない場合（failure）と、再起動して戻った場合（restart）を分けています。

#### 復旧用の情報を HSS に預ける（4.6）

- S-CSCF は、最初の登録のときに、次の情報を HSS に預けます（4.6.2）。Path にある SIP のプロキシの一覧（ふつうは P-CSCF のアドレス）、Contact のアドレスとパラメータ、認証の方式です。登録の期限の時刻も預けられます。
- 預けるのは SAR です。利用者の情報を求める SAR に、情報要素を 1 つ足します。HSS は、IMPI と、暗黙の登録の組に結びつけて覚えます。
- 中身が変わったときは、Server-Assignment-Type が RE_REGISTRATION で、User-Data-Already-Available が USER_DATA_ALREADY_AVAILABLE の SAR で更新します。登録の期限を毎回更新すると、利用者から見た品質は上がりますが、Cx の通信が増えます（4.6.2 の Note）。
- UE が reg イベントを初めて購読したときは、購読の情報（Call-ID、From、To、Record-Route、Contact）も預けます（4.6.3）。CSeq は預けません。復旧のあとで送る NOTIFY には、UE が受け付けるだけの大きな CSeq を付けます。
- P-CSCF の購読の情報（Call-ID、From、To、Contact）を預けるのは、任意の機能です（4.6.4）。
- 登録の情報と購読の情報は、1 つの復旧用の情報としてまとめて送ります。HSS は、同じ IMPI の前の情報を上書きします。

AVP の形は TS 29.229 にあります。

| AVP | コード | 中身 | 条項 |
| --- | --- | --- | --- |
| SCSCF-Restoration-Info | 639 | User-Name、1 つ以上の Restoration-Info、Registration-Time-Out、SIP-Authentication-Scheme | 6.3.46 |
| Restoration-Info | 649 | Path、Contact、Initial-CSeq-Sequence-Number、Call-ID-SIP-Header、Subscription-Info、P-CSCF-Subscription-Info | 6.3.52 |
| Path | 640 | Path ヘッダのプロキシを、コンマで区切って並べたもの | 6.3.47 |
| Contact | 641 | Contact のアドレスとパラメータ | 6.3.48 |
| Subscription-Info | 642 | Call-ID-SIP-Header（643）、From-SIP-Header（644）、To-SIP-Header（645）、Record-Route（646）、Contact | 6.3.49 |
| P-CSCF-Subscription-Info | 660 | P-CSCF の購読の情報 | AVP の表（6.3.70） |
| SAR-Flags | 655 | ビット 0 が P-CSCF Restoration Indication | 6.3.63 |
| Failed-PCSCF | 664 | 止まった P-CSCF のアドレス | AVP の表（6.3.74） |

SAR と SAA のどこに入るかは、TS 29.228 6.1.2 の表と 7.21 にあります。

#### 再起動した S-CSCF（restart）

再起動した S-CSCF は、利用者のデータをすべて失っているか、持っているデータを信用できません。止まっているあいだに、HSS からのプロファイルの更新を取りこぼしたかもしれないためです。

| 場面 | S-CSCF の動き | HSS の動き | 条項 |
| --- | --- | --- | --- |
| 着信の要求が I-CSCF から届いた | 未登録の利用者として SAR（UNREGISTERED_USER）を送る。復旧用の情報が返ってきたら、登録済みの利用者として iFC を評価し、要求を処理する | 利用者が登録済みで、復旧用の情報があれば、プロファイルと一緒に返す。Result は DIAMETER_ERROR_IN_ASSIGNMENT_TYPE（5007） | TS 23.380 4.3.2 |
| UE からの発信の要求（REGISTER 以外）が届いた | SAR（NO_ASSIGNMENT）を送る。プロファイルが返ってきたら、発信の処理を続ける。DIAMETER_UNABLE_TO_COMPLY なら、UE に登録し直させる応答を返す | S-CSCF の名前が同じなら、プロファイルと復旧用の情報を返す。別の S-CSCF に割り当て直したあとなら、名前を上書きせず、DIAMETER_UNABLE_TO_COMPLY を返す | 4.4.2、TS 29.228 6.1.2.1 |
| AS が利用者の代わりに出した発信の要求が届いた | SAR（UNREGISTERED_USER）を送る | 着信のときと同じ。名前が違えば DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED を返し、AS は I-CSCF へ送り直す | 4.5.2 |
| 登録の途中で、HSS が知っている IMPI を S-CSCF が持っていなかった | SAR（NO_ASSIGNMENT）で復旧用の情報を取る | SAA に、同じ IMPU を共有する登録済みの IMPI をすべて入れる | 4.2.3 |

どの場面でも、復旧用の情報に UE の購読の情報があれば、S-CSCF は NOTIFY を作って UE に送り、登録し直させます。送るのは、要求の通常の処理のあとならいつでも構いません。P-CSCF の購読の情報があれば、P-CSCF にも NOTIFY を送ります。登録の期限がすでに過ぎていたら、未登録の利用者として扱います（4.3.2）。

「登録し直させる応答」は、TS 24.229 5.4.3.2 にあります。504（Server Time-out）に、3GPP IM CN subsystem の XML の本文（`application/3gpp-ims+xml`）を付け、`<alternative-service>` の `<type>` を `restoration`、`<action>` を `initial-registration` にします。

#### 戻らない S-CSCF（failure）

| 場面 | 動き | 条項 |
| --- | --- | --- |
| REGISTER（登録、再登録、登録解除のどれでも） | I-CSCF は、HSS が返した S-CSCF に届かないとき、User-Authorization-Type が REGISTRATION_AND_CAPABILITIES（2）の UAR を送り、能力の一覧を求める。別の S-CSCF を選んで REGISTER を送る。新しい S-CSCF は、最初の登録と同じように処理する | TS 23.380 4.2.2、TS 24.229 5.3.1.3、TS 29.229 6.3.24 |
| 着信の要求 | I-CSCF は LIR で能力の一覧を求め、別の S-CSCF を選んで送る。HSS は、能力を求める LIR が先にあったときだけ、SAR で S-CSCF の名前を上書きする。新しい S-CSCF は、再起動した S-CSCF と同じように復旧用の情報を取る | TS 23.380 4.3.3、TS 24.229 5.3.2.2 |
| UE からの発信の要求 | P-CSCF は、Route にある S-CSCF に届かないとき、UE に登録し直させる応答（504 と XML の本文）を返す。P-Asserted-Identity には、登録のときに Path に入れた自分の URI を入れる | TS 23.380 4.4.3、TS 24.229 5.2.6.3.2A |
| AS が出した要求 | AS は、時間切れのあと、要求を I-CSCF へ送り直す | TS 23.380 4.5.3 |

I-CSCF が選び直した S-CSCF へ REGISTER や要求を送るときは、Request-URI に `scscf-reselection` という SIP URI のパラメータを付けます（TS 24.229 5.3.1.3、5.3.2.2）。SBI を使う構成の決まり（TS 23.380 4.2.2A、4.3.4）も同じ考え方です。

I-CSCF が S-CSCF を選び直せるのは、次のどれかのときです（TS 24.229 5.3.1.3）。REGISTER に `integrity-protected` が無いか `no` のとき。または、I-CSCF が S-CSCF の復旧手順に対応しているとき。選び直す前に、設定した時間だけ待ってもよいとされています（NOTE 2）。

今の I-CSCF は、S-CSCF を選び直しません（`internal/icscf/icscf.go` の `forward` の `SIMPLIFIED:`）。DNS が返した最初の宛先にだけ送ります。

### P-CSCF の障害

P-CSCF は UE の最初の相手で、UE は P-CSCF のアドレスに向けて登録しています。P-CSCF が止まると、UE に別の P-CSCF で登録し直させるしかありません。仕様には、そのきっかけを UE に伝える方法が 5 つあります。

| 方法 | 誰が気づくか | UE への伝え方 | 条項 |
| --- | --- | --- | --- |
| PCO で新しい一覧を送る | PGW が P-CSCF を監視する | 止まった P-CSCF を除いた一覧を、PCO で全部の UE に送る。使っていた P-CSCF が一覧に無い UE は、新しい P-CSCF で最初の登録を行う | TS 23.380 5.1.2 |
| P-CSCF が止まったことを知らせる | PGW が P-CSCF を監視する | P-CSCF を DHCP で見つけた UE に、P-CSCF が使えないことを知らせる。UE は DHCP で探し直して登録する | 5.2.2 |
| UE が自分で確かめる | UE | UE が keep-alive（RFC 6223）で P-CSCF を監視し、止まったら新しい P-CSCF で登録する | 5.3 |
| HSS を使う方法 | 着信側の S-CSCF | S-CSCF が HSS に知らせ、HSS が MME に知らせ、MME が IMS の PDN 接続を「reactivation requested」で解放する。UE は PDN 接続を作り直し、P-CSCF を選び、最初の登録を行う | 5.4 |
| PCRF を使う方法 | 着信側の S-CSCF | S-CSCF が別の P-CSCF に INVITE を送り、その P-CSCF が Rx で PCRF に知らせ、PCRF が Gx で PGW に知らせる。PGW が bearer を解放するか、PCO で新しい一覧を送る | 5.5 |

#### HSS を使う方法（5.4）

着信の要求を届けられないときにだけ動きます。流れは次のとおりです（5.4.2.1）。

1. 着信側の S-CSCF が、UE の P-CSCF へ要求を送ります。
2. S-CSCF は、P-CSCF が要求を処理できないことを知ります。応答が無い場合と、P-CSCF が「この利用者の登録を持っていない」という誤りを返す場合があります。
3. S-CSCF は、P-CSCF Restoration Indication（SAR-Flags のビット 0）を付けた SAR を HSS に送ります。IMPU を登録している IMPI が 1 つなら UNREGISTERED_USER、複数なら ADMINISTRATIVE_DEREGISTRATION です（TS 29.229 6.3.15、6.3.63）。
4. HSS は、利用者の MME か SGSN がこの機能に対応していれば、S6a の IDR で復旧を知らせ、登録の状態を変えて、SAA で成功を返します。対応していなければ誤りを返します（DIAMETER_ERROR_SERVING_NODE_FEATURE_UNSUPPORTED、TS 29.229 6.2.2.12）。
5. S-CSCF は、発信側に誤りの応答を返します。事業者の方針で、あとでかけ直せることを伝えられます。
6. MME は、UE が待ち受け中なら呼び出し、IMS の PDN 接続を NAS の理由「reactivation requested」で解放します。
7. UE は IMS の PDN 接続を作り直し、使える P-CSCF を選んで、最初の登録を行います。

S-CSCF が P-CSCF の障害に気づく方法は 5.4.2.2 にあります。P-CSCF が応答しないときは、決めた回数の再送のあとで「届かない」とみなし、そのあいだは着信の要求をその P-CSCF に送りません。その P-CSCF から REGISTER などの要求が届いたら、届くようになったとみなします。

SIP の側の手順は TS 24.229 にあります。

- P-CSCF は、Request-URI の利用者の登録を持っていないとき、404 を返します（5.2.6.4.1）。再起動して登録を失った P-CSCF が、これに当たります（4.14.2）。
- S-CSCF は、404 を受けたとき、P-CSCF が設定した時間のあいだ応答しないとき、`Restoration-Info: noresponse` の付いた 408 か 504 を受けたときに、480 を返して HSS を使う復旧を始めます（5.4.3.3）。待つ時間は、タイマー B と F より短くします（NOTE 24）。
- Restoration-Info ヘッダは 7.2.11 にあります。

PDN 接続を切らずに済ませる拡張もあります（5.4.3）。PGW が、PCO で新しい P-CSCF の一覧を UE に送ります。

#### PCRF を使う方法（5.5）

着信の INVITE のときにだけ動きます（TS 24.229 4.14.2）。流れは次のとおりです（TS 23.380 5.5.2）。

1. S-CSCF は、登録のときに HSS から得た IMSI を INVITE に入れ（Restoration-Info ヘッダ）、設定で決めた別の P-CSCF へ送ります。
2. 別の P-CSCF は、S-CSCF に誤りの応答を返します。
3. 別の P-CSCF は、Rx の AAR に P-CSCF の復旧を示す値を入れて、PCRF に送ります。Rx-Request-Type を PCSCF_RESTORATION（2）にします（TS 29.214 4.4.7）。PCRF は、UE の IP アドレス、IMSI、APN から UE のセッションを見つけます。
4. PCRF は AAA を返し、Gx の RAR で PGW に知らせます。
5. PGW は、default bearer を「reactivation requested」で解放するか、UE が対応していれば PCO で新しい一覧を送ります。
6. UE は PDN 接続を作り直し、新しい P-CSCF で登録します。

着信の呼の扱いには 2 つの選び方があります。a は、利用者を未登録にして、発信側に誤りを返します。b は、UE が登録し直すまで INVITE を止めておき、登録のあとで新しい P-CSCF へ送ります。

### UE の動き

| きっかけ | UE の動き | 条項 |
| --- | --- | --- |
| 504 に `application/3gpp-ims+xml` の本文があり、`<type>` が `restoration`、`<action>` が `initial-registration`。P-Asserted-Identity が、登録のときの Service-Route か Path の URI と一致する | 最初の登録を行う。P-CSCF を複数知っていれば、別の P-CSCF を選ぶのがよい | TS 24.229 5.1.2A.1.6 |
| reg イベントの NOTIFY（S-CSCF が復旧用の情報から作ったもの） | 登録し直す | TS 23.380 4.3.2、4.4.2 |
| PCO で届いた P-CSCF の一覧に、使っている P-CSCF が無い | 新しい P-CSCF で最初の登録を行う | TS 23.380 5.1.2 |
| IMS の PDN 接続が「reactivation requested」で解放された | PDN 接続を作り直し、P-CSCF を選んで最初の登録を行う | TS 23.380 5.4.2.1 |
| P-CSCF が応答しない | 別の P-CSCF を選べるなら選び、最初の登録を行う | TS 24.229 4.14.2 |
| REGISTER に 305（Use Proxy）が返った | 5.1.1.4.1 の手順に従う。中身は読んでいません | TS 24.229 5.1.1.4.1 |

今の UE シミュレータ（`internal/ue`）は、このどれにも対応していません。`internal/ims/ims3gpp.go` は `<service-info>` だけを扱い、`<alternative-service>` を扱いません。VoLTE のアクセス網の実装（[VoLTE のアクセス網の設計](../design/volte-access.md)）で、P-CSCF の一覧（PCO）と、bearer を失ったときの通知が入ります。P-CSCF の復旧は、その上に作れます。

## 2. SIP の層での切り替え

### DNS で振り分ける（RFC 3263）

- クライアントは、NAPTR でトランスポートを選び、SRV でホストとポートを選び、A でアドレスを得ます（4.1、4.2）。URI にポートがあるときと、ホストが IP アドレスのときは、NAPTR も SRV も引きません（4.2）。
- 失敗したら、次の宛先へ送り直します（4.3）。失敗とは、503 の応答、トランスポートの異常、タイマー B か F の時間切れです。送り直す要求は、Via の branch を変えた新しいトランザクションにします。
- 503 を受けたクライアントは、別のサーバーへ送り直すのがよいとされています（RFC 3261 21.5.4）。Retry-After があれば、そのあいだは同じサーバーに送りません。
- 応答を返す側にも切り替えがあります（RFC 3263 5 章）。要求の送信元に応答を送れなかったときは、Via の sent-by を見ます。ホスト名とポートがあれば A を引き、ポートが無ければ SRV を引いて、順に試します。
- 1 つのトランザクションの再送、ACK、CANCEL は、同じサーバーに送らなければなりません（4.4）。

今のコードとの差は次のとおりです。

- `internal/sipdns/locate.go` の `Locate` は、宛先の一覧を優先度と重みの順に返します。ところが `internal/ims/route.go` の `Destination` は先頭の 1 つしか使いません。P-CSCF の REGISTER だけは、一覧を順に試します（`internal/pcscf/pcscf.go` の `forward`）。
- 設定の `sip.host` にはポートを付けて使っています。`config.SIP.URI()`、各役割の `ownURI()`、Service-Route、Path、I-CSCF の `scscfs[].name`、iFC の AS の名前は、どれも `:5060` の付いた URI です。そのため SRV が引かれません。SRV で振り分けるには、役割の名前の URI からポートを外す必要があります。
- 切り替えまでの時間は、タイマー B と F の 32 秒（64×T1）です。試験やデモには長いので、短くする設定が要ります。TS 24.229 5.4.3.3 も、P-CSCF の障害を見つける時間をタイマー B と F より短くするよう求めています。

### プロキシが入れ替わってもダイアログが続く条件

確立したダイアログの経路（route set）は、ダイアログを作るときの Record-Route で決まり、あとから変わりません。入れ替わったプロキシに要求が届き、処理されるには、3 つの条件が要ります。

1. 名前が届くこと。Record-Route に入れる URI は、RFC 3263 の手順で引いたときに、入れたプロキシか、その代わりを務められるものに届かなければなりません（RFC 3261 16.6 step 4）。「代わりを務められるもの」が認められているので、同じ役割の別の台に届く名前を入れられます。
2. 自分宛てだと分かること。代わりの台は、Route の先頭の URI を自分のものと認めて外す必要があります。今の `sip.PopOwnRoute` は、設定の `host` と `port` に一致する URI だけを外します。
3. 状態があること。代わりの台が、その要求を処理するのに要る状態を持っているか、外から取れること。

役割ごとに、ダイアログの中の要求に要る状態を調べました。

| 役割 | ダイアログの中の要求に要る状態 | 今の置き場所 |
| --- | --- | --- |
| I-CSCF | 無い。Record-Route に入らない | なし |
| S-CSCF | 無い。ダイアログの状態を持たないプロキシなので、Route に従って転送するだけ（`internal/scscf/session.go` の `session` の `IsInDialog` の分岐） | なし |
| P-CSCF | UE の IP の対応付け（Contact から送り先を引く）と、呼のメディアのコンテキスト（BYE のときに解放する） | `pcscf.Store`（`MemoryStore`） |
| P-CSCF（VoLTE のアクセス網を足したあと） | 上に加えて、Rx のセッション、sec-agree の SA の表、網の側から BYE を送るためのダイアログの情報 | どれも `pcscf.Store` にある。ダイアログは `Dialog` で、中継ノードのコンテキストは `Dialog.Relay`、Rx のセッションは `Dialog.PolicySession` と `Association.SignallingSession`、SA の表は `SecurityAssociations` |
| MMTel AS | B2BUA の 2 つのダイアログ | 呼の記録（`CallRecord` の 2 つの `sip.Dialog`）は `mmtel.Store`。ダイアログそのもの（`sip.UA` の中の呼とトランザクション）はプロセスの中 |
| IBCF | 案 A（プロキシと THIG の暗号化）なら無い | SIP の状態は無い。TrGW のコンテキストを `ibcf.Store` の `Media` に持つ（Call-ID、発信側の tag、境界を渡った向きで引く） |
| メディア中継ノード | コンテキスト、ソケット、相手のアドレス | プロセスの中（`agw.Relay`） |

S-CSCF と I-CSCF は、名前と「自分宛てだと分かること」の 2 つを直せば、状態を外に出さなくても、入れ替わったあとでダイアログの中の要求を通せます。P-CSCF は、状態を外に出す必要があります。

処理の途中のトランザクションは、プロセスの中にあります（`internal/sip/stack.go`）。プロキシが止まると、そこを通っていたトランザクションは失われます。呼び出し中の呼は、UE の再送がタイマー B で切れて失敗します。冗長構成で保ちたいのは通話中の呼なので、確立したダイアログが続けば足ります。トランザクションの状態を外に出すことは、候補に入れません。

### Service-Route と Path

- Service-Route（RFC 3608）は、S-CSCF が登録の 200 で返す URI です。UE と P-CSCF が登録ごとに覚え、発信の要求の Route に入れます。今は `sip:orig@<S-CSCF の host>:<port>;lr` です（`internal/scscf/scscf.go` の `accept`）。P-CSCF は、UE の Route が Service-Route と同じかを確かめます（`equalRoutes`）。
- Path（RFC 3327）は、P-CSCF が REGISTER に入れる URI です。S-CSCF が、連絡先と一緒に覚えます（RFC 3327 5.3）。着信の要求は、この URI を通って UE に届きます。今は `sip:term@<P-CSCF の host>:<port>;lr` です（`pathURI`）。HSS に預ける復旧用の情報にも入ります（TS 23.380 4.6.2）。

どちらも、登録のあいだ変わらない名前です。名前が 1 台を指していると、その台が止まったとき、登録し直すまで届きません。TS 23.380 の手順が UE に登録し直させるのは、このためです。

P-CSCF には、もう 1 つ事情があります。UE は、P-CSCF の IP アドレスに向けて登録しています。NAT の対応付け（[ホストとコンテナの通信の設計](../design/environment-capture.md)）も、IPsec の SA（IMS-AKA の登録で作る）も、そのアドレスに結びついています。別のアドレスの P-CSCF は、状態を共有していても、UE に要求を届けられないことがあります。P-CSCF を「同じアドレスで作り直す」場合と、「別のアドレスの台に移す」場合は、分けて考える必要があります。前の場合は、状態を外に置けば UE は気づきません。あとの場合は、標準のとおり UE の登録し直しが要ります。

## 3. 今のコードの状態の一覧

コードで、どの状態がどこにあるかを調べました。

| 役割 | 状態 | 置き場所 | ファイル |
| --- | --- | --- | --- |
| S-CSCF | 認証の途中の状態（Challenge）、登録（Registration）、reg イベントの購読（Subscription）、AS から戻る要求の続き（OriginalDialog）、未登録の利用者のプロファイル（UnregisteredProfile） | `scscf.Store` の後ろ。実装は `MemoryStore` | `internal/scscf/store.go` |
| S-CSCF | reg-await-auth、登録の期限、購読の期限のタイマー | プロセスの中 | `internal/scscf/scscf.go` の `timers`、`regevent.go` |
| S-CSCF | 利用者ごとのロック（IMPI ごと） | プロセスの中 | `internal/ims/locks.go`、`scscf.go` の `users` |
| P-CSCF | IP の対応付け（Association。Rx の信号のセッション `SignallingSession` を含む）、INVITE のダイアログ（Dialog。中継ノードのコンテキストは `Dialog.Relay`、Rx のセッションは `Dialog.PolicySession`）、sec-agree の SA の表（SecurityAssociations） | `pcscf.Store` の後ろ | `internal/pcscf/pcscf.go`、`dialog.go` |
| P-CSCF | 自分の reg イベントの購読（`regEvents`）、対応付けを消すまでの猶予のタイマー、最終の応答を待つ最初の INVITE（`setups`）、2xx の ACK を待つタイマー | プロセスの中 | `internal/pcscf/regevent.go`、`pcscf.go` の `endAssociation`、`release.go` |
| I-CSCF | 無い | なし | `internal/icscf` |
| HSS | 加入者、登録の状態、S-CSCF の名前、SQN、Sh のリポジトリのデータ | `hss.Store` の後ろ。SQN は `CompareAndSwapSQN`、リポジトリのデータは `CompareAndSwapRepositoryData` で更新する | `internal/hss/store.go` |
| HSS | 利用者ごとのロック（IMPI ごと。MAR と SAR、Sh の PUR、保守の操作での削除） | プロセスの中 | `internal/hss/hss.go` の `users`、`sh.go`、`provisioning.go` |
| メディア中継ノード | コンテキスト、ソケット、ポートの空き | プロセスの中。`Store` が無い | `internal/agw/relay.go` |
| IP-SM-GW | 配達の途中の状態、RP-MR の数、reg イベントの購読 | `ipsmgw.Store` の後ろ | `internal/ipsmgw/store.go` |
| IP-SM-GW | delivery report を待つタイマー（TR1N） | プロセスの中 | `internal/ipsmgw/deliver.go` |
| IP-SM-GW | 利用者ごとのロック（公開 ID ごと。reg イベントの購読の作り直し） | プロセスの中 | `internal/ipsmgw/ipsmgw.go` の `users`、`regevent.go` |
| SC | status report に要る値 | `sc.Store` の後ろ | `internal/sc/store.go` |
| MMTel AS | 確立した呼の記録（CallRecord。2 本のダイアログ、サービスを受けた利用者、セッションの種類、働いたサービス） | `mmtel.Store` の後ろ | `internal/mmtel/store.go` |
| MMTel AS | 呼の 2 本の脚（`sip.UA` の中の呼とトランザクション）、CFNR の無応答のタイマー | プロセスの中 | `internal/mmtel/mmtel.go`、`call.go`、`diversion.go` |
| MRFC | 会議ごとのミックスの場所（ConferenceMedia。MRFP の名前とコンテキスト） | `mrfc.Store` の後ろ | `internal/mrfc/store.go` |
| MRFC | ダイアログとそのタイマー、MRFP からの Notify の待ち（`waiting`） | プロセスの中。INVITE を受けた台の中 | `internal/mrfc/mrfc.go` |
| IBCF | 境界を渡る呼ごとの TrGW のコンテキスト（Media。Call-ID、発信側の tag、向きで引く） | `ibcf.Store` の後ろ | `internal/ibcf/store.go` |
| IBCF | INVITE のトランザクションと Timer C | プロセスの中 | `internal/ibcf/proxy.go` |
| EPC スタブ | PDN 接続（bearer、Rx のセッション、PCC の規則）、Rx の Session-Id から PDN 接続への対応（Binding） | `epc.Store` の後ろ | `internal/epc/store.go` |
| EPC スタブ | UE ごとのイベントの流れ（S1 の接続の代わり） | プロセスの中。UE が接続した台の中 | `internal/epc/streams.go` |

`Store` の境界はすでにあります。足りないのは、境界の後ろの実装、タイマー、複数の操作をまとめる手段です。境界の後ろの実装は、11.8 で作りました。このうち MMTel AS と MRFC の `Store` を保存先に載せることと、IBCF と EPC の `Store` を載せないことは、[状態と冗長構成の設計](../design/state-redundancy.md)で決めました。

## 4. 登録と呼の継続性の候補

コンテナが落ちて別のコンテナに置き換わっても、登録と確立済みの呼をできる限り保つことが、この実装の目標です。方式は標準に無いので、試作して測ってから決めます。ここに書くのは、候補と測り方の案です。採用した方式は [状態と冗長構成の設計](../design/state-redundancy.md) にあります。

### 候補

| 案 | 内容 | 強み | 弱み |
| --- | --- | --- | --- |
| A. UE の登録し直しに任せる | 状態はプロセスの中のまま。止まったノードの利用者は、TS 23.380 の手順で登録し直す | 外部の保存先が要らない。標準の手順そのもの | 登録し直すまで着信できない。冗長構成の目標（再登録なしで着信できる）を満たさない。P-CSCF が持つ呼のメディアの情報が失われ、BYE のあとにコンテキストが残る |
| B. HSS に預ける（TS 23.380 4.6） | S-CSCF が、登録と購読の情報を SAR で HSS に預ける。再起動した S-CSCF は、要求が届いたときに HSS から取り戻す | 標準の手順で、Cx のキャプチャに見える。S-CSCF については、UE が登録し直さなくても着信できる（4.3.2） | S-CSCF にしか使えない。P-CSCF、中継ノード、AS には標準の預け先が無い。HSS の状態を外に置くことが前提になる。登録のたびに SAR が大きくなる |
| C. 外部の保存先 | `Store` の実装を足し、Redis と互換の保存先に状態を置く。同じ役割の台が同じ保存先を使う | どの役割にも使える。台を作り直しても、別の台に移しても状態が残る。台を増やしても利用者が 1 台に縛られない（各役割を水平に広げられるという要件を満たす） | 登録と呼の処理のたびに保存先との往復が入る。保存先そのものの冗長が要る。期限と排他の扱いを決める必要がある（6 章） |
| D. ノードのあいだで複製する | 同じ役割の台どうしが、状態の変更を送り合う | 外部の保存先が要らない | 台の発見、順序、分断のときの扱いを自分で作ることになる。学習の対象として大きすぎる。台の数が増えると、複製の量も増える |

### 結論

役割ごとに組み合わせます。

| 対象 | 結論 | 理由 |
| --- | --- | --- |
| S-CSCF の登録 | C を基本にし、B も作る | C で目標を満たす。B は TS 23.380 を再現するために要る。B だけの構成と C の構成を切り替えて、復旧の様子を比べられる |
| P-CSCF の対応付けとメディアの情報 | C | 標準の預け先が無い。目標は、P-CSCF を作り直しても呼が切れないことである |
| HSS | C | HSS は加入者のデータの置き場なので、状態を外に置くのが自然。TS 29.228 6.4 も、HSS を 1 つに見せる例としてサーバーファームを挙げている |
| IP-SM-GW と SC | C | すでに `Store` の後ろにある。実装を足すだけ |
| メディア中継ノード | 5 章の「メディア中継ノード」で扱う。音声が途切れる範囲を設計で決める | ソケットは外に置けない |
| P-CSCF が別のアドレスの台に替わる場合 | A（TS 23.380 の P-CSCF の復旧） | 2 章のとおり、UE は P-CSCF のアドレスに結びついている |

D は作りません。

### 外部の保存先

| 候補 | ライセンス | 向いている点 | 気になる点 |
| --- | --- | --- | --- |
| Valkey | BSD 3-Clause（リポジトリの COPYING で確かめた） | Redis と同じプロトコル。期限付きのキー、ソートされた集合、条件付きの書き込み、スクリプトがある | 1 台では単一障害点になる。複数台の構成（レプリカと自動の切り替え）は別に組む |
| Redis | 8 以降は RSALv2、SSPLv1、AGPLv3 から選ぶ形（リポジトリの LICENSE.txt で確かめた）。7.2 までは BSD | 同上 | ライセンスが版で違う。Compose のイメージの版を固定する必要がある |
| etcd | 確かめていない | 強い一貫性、リース、変更の通知、トランザクション。Kubernetes と相性がよい | 書き込みが遅い。登録のたびに書く使い方には重い見込み。測る必要がある |

Go のクライアントは、go-redis（BSD 2-Clause。リポジトリの LICENSE で確かめた）が Valkey にも使えます。ただし、採用したのは標準ライブラリだけで書いたクライアントです（11.3）。

結論は Valkey と go-redis です。ライセンスが明快で、期限付きのキーとソートされた集合が、6 章のタイマーの直し方に合うためです。

### 測るもの

方式は、試作して測ってから決めます。測るものの案です。

| 測るもの | 測り方 |
| --- | --- |
| 登録の処理の時間 | P-CSCF が REGISTER を受けてから 200 を返すまで。pcap のフレームの時刻の差で測る。`MemoryStore`、外部の保存先、外部の保存先と HSS への預け入れの 3 通りを比べる |
| 呼の接続の時間 | P-CSCF が INVITE を受けてから、着信側の UE に INVITE が届くまで |
| 復旧までの時間 | コンテナを削除してから、次の着信の INVITE が UE に届くまで。A、B、C で比べる |
| 音声の途切れ | P-CSCF、S-CSCF、中継ノードをそれぞれ作り直したとき、受信側で RTP のシーケンス番号が飛んだ長さ |
| 保存先の往復の数 | 登録 1 回、呼 1 回あたりの保存先への要求の数。保存先の側のログで数える |

この実装は性能を求めません。測るのは、方式ごとの差を学習者に見せ、設計の根拠にするためです。

## 5. 規模を広げる妨げを 1 つずつ取り除く

規模を広げる妨げの表（[状態と冗長構成の設計](../design/state-redundancy.md) の元になった一覧）の行ごとに、関わるコードを読み、直し方と大きさを書きます。大きさの目安は、小が 100 行くらいまで、中が数百行、大がそれ以上か複数のパッケージにまたがるものです。試験の行数を含みます。

### S-CSCF の登録の検索

- 今の動き。`MemoryStore.RegistrationOf`（`internal/scscf/store.go`）は、登録の全件を回り、IMPU を 1 つずつ正規化して比べます。発信と着信の要求のたびに呼ばれます（`session.go` の `originating`、`terminating`、`routeTerminating`）。`SubscriptionsOf` は、購読の全件を回って IMPI を比べます。登録が変わるたびに呼ばれます。
- 直し方。`MemoryStore` に索引を 2 つ足します。正規化した IMPU から IMPI への対応と、IMPI から購読の ID の集合への対応です。`PutRegistration` と `DeleteRegistration` で、古い IMPU の索引を消してから新しいものを入れます。外部の保存先でも、同じ 2 つの索引をキーにします（`impu:<URI>` から IMPI、`subs:<IMPI>` の集合）。
- 気をつける点。暗黙の登録の組が変わったとき（再登録で IMPU が減ったとき）に、古い索引を残さないこと。IMPU の正規化は書き込みのときに 1 回だけ行い、引くたびに行わないこと。
- 大きさ。小。`internal/scscf/store.go` と、その試験だけです。`Store` のインタフェースは変わりません。冗長構成の実装を待たずに直せます。

### P-CSCF の IP の対応付けの検索

- 今の動き。`MemoryStore.AssociationOfContact`（`internal/pcscf/pcscf.go`）は、対応付けの全件を回り、Contact を 1 つずつ解析して比べます。UE への要求のたび、ダイアログの中の要求のたび、ACK のたびに呼ばれます（`session.go` の `initial`、`nextHop`）。
- 直し方。Contact の user、host、port から作ったキーで、対応付けの送信元を引く索引を足します。`PutAssociation` と `DeleteAssociation` で索引を保ちます。同じ Contact が別の送信元から登録し直されたとき（NAT のポートが変わったとき）は、新しいほうを指すようにします。
- 大きさ。小。`internal/pcscf/pcscf.go` と、その試験だけです。

### HSS の振り分け

- 今の動き。Diameter の接続先は設定に複数書けますが、`cmd/ims/main.go` の `hssPeer` は先頭の 1 つだけを返します。I-CSCF、S-CSCF、IP-SM-GW は、その 1 つの `*diameter.Peer` に全部の要求を送ります。S-CSCF は、MAA の Origin-Host を覚えて SAR の Destination-Host に入れています（`Challenge.HSSHost`、`Registration.HSSHost`）。ただし、その相手へ送り分ける先がありません。HSS の `MemoryStore` はプロセスの中にあり、2 台の HSS は別々のデータを持つことになります。
- 仕様。HSS が複数あるとき、I-CSCF と S-CSCF は、SLF（Dx）か Diameter のプロキシで、利用者の HSS を見つけます（TS 29.228 6.4）。SLF は Diameter のリダイレクトエージェントで、Cx の要求に、HSS の名前を入れたリダイレクトの応答を返します。I-CSCF と S-CSCF は、その HSS に同じ要求を送り直します。プロキシなら、要求をそのまま HSS に転送します。S-CSCF は、HSS の名前を覚えて、同じ利用者の次の要求に使います。HSS が 1 つに見える構成（サーバーファーム）では、この仕組みは要りません。リダイレクトの手順は RFC 6733 6.1.8 にあり、Redirect-Host は 6.12 です。

直し方の候補は 3 つあります。

| 案 | 内容 | 大きさ |
| --- | --- | --- |
| 1. HSS の台が状態を共有する | HSS の `Store` を外部の保存先に置く。CSCF は、設定した HSS の接続のうち、つながっているものに送る。応答が無ければ次の接続に送り直す（RFC 6733 5.5.4） | 中。接続を選ぶ部品（`internal/diameter` か `cmd/ims`）、`hss.Store` の実装 |
| 2. SLF（Dx）で振り分ける | 役割 `slf` を足す。加入者の ID の範囲ごとに HSS の名前を持ち、リダイレクトの応答を返す。CSCF は、応答の Redirect-Host に送り直し、名前を覚える | 中から大。`slf` の役割、リダイレクトの応答の処理、Destination-Host での接続の選択、設定 |
| 3. Diameter のプロキシ（DRA） | 要求を中継する役割を足す | 大。中継、ループの検出、Route-Record などが要る |

- 結論。1 と 2 の両方です。1 は冗長（HSS の 1 台が止まっても動く）のため、2 は分割（加入者を HSS の組に分ける）のためで、目的が違います。ロードマップは 2 か 3 を挙げています。2 を選ぶのは、Dx の UAR とリダイレクトの応答がキャプチャに見え、仕様の手順と照らせるためです。3 は作りません。
- 気をつける点。`diameterApplications` は、役割ごとのアプリケーションだけを CER に入れます（[実装の方針](../design/implementation.md)）。Dx は Cx と同じアプリケーション ID を使うと見られますが、確かめていません。

### HSS の加入者の一覧

- 今の動き。`MemoryStore.List`（`internal/hss/store.go`）は、IMPI のキーをすべて取り出して並べ替え、先頭の 1000 件（`provisioning.go` の `listLimit`）を返します。呼ばれるのは保守用の `GET /subscriptions` だけです。ダッシュボードがこれを使います。
- 直し方。一覧をページに分けます。`GET /subscriptions?after=<IMPI>&limit=<件数>` の形にし、応答に次のページの `after` を入れます。`Store.List` は、`after` より後ろの `limit` 件を返します。メモリの実装は、並んだキーの列を保ち、追加と削除のときに 2 分探索で入れ替えます。外部の保存先では、ソートされた集合の範囲の検索で引きます。
- 大きさ。小から中。`internal/hss/store.go`、`provisioning.go`、ダッシュボードの Go の側と画面（`internal/dashboard`、`web/dashboard`）です。
- 気をつける点。API の形が変わるので、手順書に API の説明があれば、同じコミットで直します。

### メディア中継ノード

- 今の動き。P-CSCF の設定の `media_relay` は URL が 1 つです（`config.PCSCF.MediaRelay`）。`cmd/ims/main.go` が `agw.NewClient` を 1 つ作ります。呼のコンテキストは、INVITE のダイアログの記録の `Dialog.Relay`（`internal/pcscf/dialog.go` の `RelayContext`。コンテキストの ID と、SDP に書いた両側のアドレス）に入ります。ダイアログは Call-ID と UE の tag で引くので、BYE はその記録から解放するコンテキストを知ります。コンテキストの ID は `c1`、`c2` のように、中継ノードが起動のたびに 1 から数えます（`internal/agw/relay.go` の `Add`）。
- 問題。中継ノードが複数あると、ID だけではどの中継ノードのコンテキストか分かりません。中継ノードが再起動すると、同じ ID が別の呼に使われます。再起動の前の呼の BYE で、P-CSCF が別の呼のコンテキストを消すおそれがあります。
- 直し方。
  1. 設定を一覧にします（`media_relays`）。名前で書けば、DNS の A レコードで台を増やせます。
  2. 呼ごとに中継ノードを選びます。順番に回す方法で足ります。`Add` が失敗したら、次の中継ノードを試します。
  3. `Store` に、コンテキストの ID と一緒に中継ノードの URL を覚えます。`Modify` と `Subtract` は、その中継ノードに送ります。今の形では、`RelayContext` に URL を足すことになります。この形に決まりました（`RelayContext.Relay`。[状態と冗長構成の設計](../design/state-redundancy.md)）。
  4. コンテキストの ID を、起動のたびに変わる乱数にします。再起動の前の ID が、後の呼に当たらなくなります。
  5. ドレイン中の中継ノードは、`Add` に 503 を返し、今あるコンテキストは続けます。P-CSCF は次の中継ノードを選びます。
- 大きさ。中。`internal/pcscf`（選び方、`Store` の形）、`internal/agw`（ID、ドレイン）、`internal/config`、`cmd/ims` です。網間接続の TrGW（[interconnect.md](interconnect.md) の 10 章）も、同じ選び方を使います。

中継ノードが止まったときの音声も決める必要があります。ソケットは外に置けないので、中継ノードが止まると RTP は止まります。候補は 3 つあります。

| 案 | 内容 | 音声 |
| --- | --- | --- |
| 1. 何もしない | 呼の信号は続き、音声だけが止まる。UE が RTCP の途絶などで気づいて切る | 戻らない |
| 2. 同じアドレスで作り直す | 中継ノードがコンテキスト（ID、ポート、相手のアドレス）を外部の保存先に書き、起動のときに同じポートを開き直す | 作り直すあいだだけ途切れる |
| 3. 別の中継ノードに移す | P-CSCF が中継ノードの停止を知り、別の中継ノードにコンテキストを作って、両側に re-INVITE を送る | 切り替えのあいだだけ途切れる。P-CSCF がダイアログの状態を持ち、自分で re-INVITE を出す必要がある |

結論は 2 です。ロードマップは、メディア中継ノードの障害で音声がどうなるかを、設計で決めた範囲で確認することを求めています。2 は、コンテナを作り直す場面で音声が戻ることを見せられ、SIP の信号を変えずに済みます。3 は、P-CSCF が B2BUA に近くなるので作りません。H.248 で中継ノードを制御すると、中継ノードの再起動を ServiceChange で、コンテキストの確認を Audit で見られます（[メディア中継ノードの制御の設計](../design/media-control.md)）。

### タイマー

- 今の動き。期限のタイマーは、`time.AfterFunc` でプロセスの中に持っています。S-CSCF の reg-await-auth、登録の期限、購読の期限（`internal/scscf/scscf.go` の `timers`、`regevent.go`）。P-CSCF の対応付けを消すまでの猶予（`endAssociation`）。IP-SM-GW の delivery report の待ち（`deliver.go`）。`MemoryStore` の中にも、期限で消すためのタイマーがあります（`scscf/store.go` の `PutOriginalDialog`、`ipsmgw/store.go`、`sc/store.go`）。
- 問題。ノードが入れ替わると、タイマーが消えます。登録の期限が切れても、SAR（TIMEOUT_DEREGISTRATION）と NOTIFY が出ません。HSS は、利用者が登録済みだと思い続けます。

期限には 2 種類あります。

| 種類 | 例 | 外部の保存先での扱い |
| --- | --- | --- |
| 消えるだけでよいもの | OriginalDialog、RP-MR の数、status report に要る値、認証の途中の状態 | 期限付きのキーにする。保存先が消す。タイマーは要らない |
| 切れたときに動作が要るもの | 登録の期限（SAR と NOTIFY）、購読の期限（NOTIFY）、reg-await-auth（SAR）、delivery report の待ち（SC への報告） | 期限の索引を持ち、どの台でも処理できるようにする |

直し方は、`Store` に、期限の索引を引く操作を足すことです。

```go
// DueRegistrations returns up to limit private identities whose
// registration expired before now, each claimed for this node for
// the lease, so that no other node handles the same one.
DueRegistrations(now time.Time, limit int, lease time.Duration) []string
```

外部の保存先では、期限の時刻を点数にした、ソートされた集合を使います。各台が短い間隔で、期限が今より前のものを取り出します。取り出すときに、点数を「今の時刻にリースの長さを足した値」に書き換えて、ほかの台が同じものを取らないようにします。処理した台が登録を消し、索引からも消します。処理の途中で台が止まれば、リースが切れたあとで別の台が取ります。取り出すのは期限が来たものだけなので、全件の走査にはなりません。

メモリの実装は、同じ操作を最小ヒープで行います。こうすると、`MemoryStore` を使う 1 台の構成でも、同じ道を通ります。

- 期限切れを 2 回処理しても壊れないようにします。今の `registrationExpired` は、登録がまだあり、期限が同じときだけ動きます。この確認をそのまま使えます。
- 大きさ。中から大。`internal/scscf`（3 つのタイマー）、`internal/pcscf`、`internal/ipsmgw`、各 `Store` です。役割ごとに分けて進められます。

## 6. 状態の境界の残りを取り除く

### `Store` の操作をまとめて不可分に行う

- 今の動き。`Store` の操作は 1 つずつです。S-CSCF は、利用者ごとのロック（`ims.UserLocks`）で順番を守っています。たとえば `register` は、ロックを取ってから、Challenge を読み、MAR か SAR を送り、Registration を書き、NOTIFY を送ります。
- 同じロックを使う役割。S-CSCF のほかに、HSS と IP-SM-GW も `ims.UserLocks` を使います。
  - HSS は IMPI ごとにロックを取ります（`internal/hss/hss.go` の `lock`）。同じ利用者の MAR と SAR が、互いの書き込みを消さないようにするためです。ロックを取ったあとで加入者を読み直します。Sh の PUR（`sh.go`）と、保守の操作での加入者の削除（`provisioning.go` の `removeSubscription`）も、同じロックの中で書きます。削除のあとの RTR は、ロックを放してから送ります。
  - IP-SM-GW は公開 ID ごとにロックを取ります（`internal/ipsmgw/regevent.go`）。第三者の REGISTER を受けて reg イベントの購読を作り直す処理と、SUBSCRIBE が失敗したときの片付けを、同じ利用者について 1 つずつ行うためです。
  - どちらも、ロックの中で `Store` を読んで書くだけで、Cx の往復をまたぐのは S-CSCF だけです。HSS の SQN とリポジトリのデータは、ロックに加えて `Store` の比べて入れ替える操作で更新します。
- 問題。ロックはプロセスの中にあります。2 台の S-CSCF が同じ保存先を使うと、同じ利用者の 2 つの要求が、別の台で同時に進むことがあります。たとえば、再登録と、期限切れの処理です。HSS と IP-SM-GW を複数台にしたときも同じです。2 台の HSS が同じ利用者の MAR と SAR を同時に処理すると、片方の書き込みが残らないおそれがあります。2 台の IP-SM-GW が同じ利用者の購読を同時に作り直すと、SUBSCRIBE が 2 つ出るおそれがあります。どちらもコードから読んだもので、試していません。

候補は 3 つあります。

| 案 | 内容 | 強み | 弱み |
| --- | --- | --- | --- |
| 1. 保存先に利用者ごとのロックを置く | IMPI ごとのキーを、無いときだけ、期限付きで書く。処理が終わったら消す。今の `users.Lock` の中身を差し替える | 今のコードの形がそのまま残る。ロックの範囲が読みやすい | Cx の往復のあいだロックを持つ。台が止まると、期限までその利用者が待たされる。期限が切れたあとに古い台が書き込むのを防ぐ仕組み（番号を付けた書き込み）が要る |
| 2. 版を付けた条件付きの書き込み | Registration に版の番号を持たせ、読んだときの版と同じときだけ書く。違えば読み直してやり直す | ロックを持たない。台が止まっても待たされない | Cx の要求や NOTIFY は取り消せないので、やり直しの設計が難しい。NOTIFY の順序を別に守る必要がある |
| 3. 利用者を台に割り当てる | 同じ利用者の要求が、いつも同じ台に届くようにする | 保存先の排他が要らない | 標準の S-CSCF の割り当てと同じ考え方。台が止まると割り当て直しが要る。P-CSCF には使えない |

- 結論。1 です。S-CSCF の処理は、Cx の往復と NOTIFY の送信を含む長い手順で、途中からやり直せません。利用者ごとのロックは、役割全体を止めないので、規模を広げる要件に反しません。ロックの期限は、Cx の応答の待ち時間（今は 5 秒、`internal/config/config.go` の `answerTimeout`）より長くします。
- 標準の割り当てとの関係。HSS は利用者ごとに S-CSCF の名前を 1 つ覚えます。同じ保存先を使う S-CSCF の台を、HSS に 1 つの名前で見せるか、台ごとの名前で見せるかを決める必要があります。7 章に書きます。
- 大きさ。中。`internal/ims/locks.go` をインタフェースにし、保存先の実装を足します。P-CSCF の対応付けは 1 回の書き込みで済むので、ロックは要りません。
- 決まったこと。[状態と冗長構成の設計](../design/state-redundancy.md)の「利用者ごとのロック」で、`ims.UserLocks` をインタフェース `ims.UserLocker` の実装の 1 つにし、冗長構成では保存先に置くロックを S-CSCF、HSS、IP-SM-GW の 3 つに渡すと決めました。期限は 15 秒です。番号を付けた書き込みは作らず、`SIMPLIFIED:` に書きます。

### P-CSCF の reg イベントの購読

- 今の動き。P-CSCF は、登録のあとで reg イベントを購読します（`internal/pcscf/regevent.go`）。購読は `sip.Subscription` で、更新と期限のタイマーを持ち、`p.regEvents` の表（プロセスの中）にあります。S-CSCF からの NOTIFY は、Contact の URI（`sip:<P-CSCF の host>:<port>`）に届きます。
- 問題。P-CSCF が入れ替わると、購読のダイアログを知りません。S-CSCF の NOTIFY に 481 を返すことになり、S-CSCF は購読を終わらせます。そのあと、網の側からの登録解除（RTR など）を P-CSCF が知らず、対応付けが残ります。今の実装は、次の REGISTER で購読を作り直します（`subscribeRegEvent` の `SIMPLIFIED:`）。

候補は 3 つあります。

| 案 | 内容 | 大きさ |
| --- | --- | --- |
| 1. 購読のダイアログを `Store` に置く | ダイアログの ID、経路、CSeq、期限を `Store` に書く。NOTIFY は、どの台が受けても `Store` から引いて処理する。更新の SUBSCRIBE は、期限の索引（5 章のタイマー）で、どの台からでも送る | 中から大。`internal/sip` の `Subscriber` に、保存した状態から購読を組み立て直す入口が要る |
| 2. 入れ替わった台が作り直す | 起動した P-CSCF が、`Store` の対応付けのうち、購読が無い利用者について SUBSCRIBE を送り直す | 中。全部の対応付けを回る処理になるので、規模を広げる要件に反する |
| 3. 今のまま | 次の REGISTER まで購読が無い状態を認める | なし |

- 結論。1 です。IP-SM-GW は、同じ形の購読をすでに `Store` に置いています（`internal/ipsmgw/regevent.go`、[SMS の設計](../design/sms.md)）。その作りに合わせます。IP-SM-GW は、再登録の第三者 REGISTER のたびに購読を更新するので、更新のタイマーを持ちません。P-CSCF も、UE の再登録の 200 のたびに購読を更新する形にすれば、更新のタイマーが要らなくなります。TS 24.229 の P-CSCF の購読の手順がこの形を許すかは、確かめていません。

## 7. 一覧に無い妨げ

これまでの章で扱った一覧に無いが、複数台で動かすと問題になる所を、コードから拾いました。

| 所 | 今の動き | 直し方 | 大きさ |
| --- | --- | --- | --- |
| 宛先の切り替え | `ims.Destination` が DNS の結果の先頭だけを使う | RFC 3263 4.3 のとおり、503、トランスポートの異常、時間切れで次の宛先へ送り直す。`sip.ServerTransaction.Proxy` の呼び出しを、宛先の一覧を順に試す形にまとめる | 中。全部の役割が使う `internal/ims` の部品にする |
| URI のポート | 役割の URI に `:5060` が付き、SRV が引かれない | Service-Route、Path、Record-Route、`ServerName`、設定の名前からポートを外せるようにする。DNS のゾーンに役割ごとの SRV を足す | 小から中。設定と `ownURI` の形 |
| 自分宛ての Route の見分け | `PopOwnRoute` が、設定の `host` と `port` に一致する URI だけを外す | 役割の共通の名前（たとえば `scscf.<ドメイン>`）も自分のものとして外す | 小 |
| I-CSCF の S-CSCF の選び直し | 選び直さない（`SIMPLIFIED:`） | TS 24.229 5.3.1.3 と 5.3.2.2 を作る。UAR と LIR で能力を求め、`scscf-reselection` を付ける | 中。`internal/icscf`、`internal/hss`（REGISTRATION_AND_CAPABILITIES への応答、名前の上書きの条件） |
| Diameter の接続の切り替え | `hssPeer` が先頭の接続だけを使う | つながっている接続を選び、応答が無ければ次へ送り直す | 小から中（5 章の HSS の案 1 と同じ部品） |
| メディアのコンテキストの ID | 起動のたびに `c1` から数える | 乱数にする（5 章） | 小 |
| IP-SM-GW の TR1N | 送った台のプロセスの中 | 期限の索引に載せる（5 章のタイマー） | 小 |
| UE シミュレータ | P-CSCF は 1 つ。504 と XML の本文、305、503 を扱わない | P-CSCF の一覧を持ち、1 章の「UE の動き」の表を作る | 中。EPC スタブが配る P-CSCF の一覧（PCO）の上に作る |

### S-CSCF の台と名前

同じ保存先を使う S-CSCF が複数あるとき、HSS に見せる名前と、Service-Route と Record-Route に入れる名前を決める必要があります。

| 案 | HSS に見せる名前 | Service-Route と Record-Route | TS 23.380 の手順 |
| --- | --- | --- | --- |
| 1. 台ごとの名前 | `scscf1.<ドメイン>` など。利用者は 1 台に割り当てられる | 台の名前 | そのまま再現できる。台が止まると、I-CSCF が選び直し、HSS の名前を上書きする |
| 2. 組の名前 | `scscf.<ドメイン>` の 1 つ。DNS の SRV が全部の台を返す | 組の名前 | 台が 1 つ止まっても、手順は動かない。組の全部が止まったときにだけ動く |
| 3. 両方 | 組を 2 つ作る（`scscf-a`、`scscf-b`）。組の中は同じ保存先で、組のあいだは標準の割り当て | 組の名前 | 組の中の障害は保存先で、組ごとの障害は TS 23.380 で扱う |

結論は 3 です。1 つの組の中で台を作り直す場面と、組ごと止めて TS 23.380 の選び直しを見る場面を、同じ構成で見せられます。Compose の台の数は増えます。台の数を抑えるなら、組を 2 つにし、組の中の台は 1 台か 2 台にします。

## 8. 障害注入

この章は、試作した結果です。試した環境は Docker Engine 29.10.0、Docker Compose v5.7.1、Linux 6.18（cgroup v1、overlayfs）です。Compose のプロジェクト名を `ims-chaos` にして、ほかの作業と資源を分けました。そのため、この章のコンテナの名前は `ims-chaos-<サービス>-1` です。既定のプロジェクトでは `ims-practice-<サービス>-1` になります。

作ったものは [deploy/chaos/](../../deploy/chaos/README.ja.md)（上書きのファイル、道具のイメージ、`fault.sh`、学習者向けの説明）と、`internal/fault`（HTTP の API と、Docker で実行する部分）です。`compose.yaml` と IMS のイメージは変えていません。

### この環境のカーネルでできること

障害の入れ方は、カーネルが持つ機能で決まります。`/proc/config.gz` を読み、`NET_ADMIN` 付きのコンテナで実際に試しました。`/lib/modules` が無いので、モジュールを足すことはできません。

| 機能 | 結果 | 確かめ方 |
| --- | --- | --- |
| netem（`sch_netem`） | 無い | `CONFIG_NET_SCH_NETEM is not set`。`tc qdisc add dev eth0 root netem delay 200ms` は `Error: Specified qdisc kind is unknown.` で失敗する |
| tbf、htb | ある | `tc qdisc add ... tbf` は通る。帯域を絞った待ちは作れるが、決まった長さの遅延にはならない |
| prio、gact、police | 無い | 設定が `is not set`。tc だけでパケットを捨てる道は無い |
| nftables の netdev の egress と ingress のフック | ある | `CONFIG_NETFILTER_EGRESS=y`、`CONFIG_NETFILTER_INGRESS=y`。規則を足して ping で確かめた |
| nftables の `numgen random` | ある | 50 % の規則で、ping の 20 回のうち 11 回が失われた |
| iptables の `statistic` | ある | 規則は入る。ただし下の「遅延と損失」のとおり、損失の代わりにはならない |
| `NET_ADMIN` が無いとき | 失敗する | tc は `RTNETLINK answers: Operation not permitted`、nft は `Permission denied` |

### ノードの停止

S-CSCF などで試しました。コマンドは `fault.sh` のものと同じです。

| 方法 | キャプチャとログに出るもの | 止まったあと |
| --- | --- | --- |
| `docker stop` | DPR と DPA のあとで、TCP の FIN。止める前に Diameter の別れの挨拶をする | 止まったまま（終了コード 0）。`restart: unless-stopped` は、手で止めたコンテナを起動しない |
| `docker kill` | DPR は無い。プロセスが消えると、カーネルが TCP の接続を FIN で閉じる | 止まったまま（終了コード 137） |
| `docker kill -s QUIT` | Go のランタイムが全 goroutine を出して終わる | 止まったまま（終了コード 2）。`docker kill` は、送ったシグナルによらず手で止めた扱いになる |
| `docker exec <コンテナ> kill -QUIT 1` | 同じく全 goroutine を出して終わる | Docker が自分で起動し直す（RestartCount が 1 増えた）。障害で落ちたときの動きを見せられる |
| ホストから `kill -9 <ホストの PID>` | 同じく落ちる | Docker が起動し直す。ただし Docker Desktop では、プロセスが VM の中にあるので使えない |
| `docker exec <コンテナ> kill -KILL 1` | 何も起きない | 名前空間の PID 1 は、中から送った SIGKILL を受けない |
| `docker pause` | 下の表 | `docker unpause` で続きから動く |

`docker pause` で見えたものです。

| 凍らせたもの | 見えたもの |
| --- | --- |
| P-CSCF（登録の前に凍らせ、7 秒後に戻す） | UE が REGISTER を 0.5 秒、1 秒、2 秒の間で送り直す（RFC 3261 17.1.2.2 のタイマー E）。戻したあと、P-CSCF は 401 を 1 回だけ返す。溜まっていた写しは、同じトランザクションの送り直しとして吸収された |
| HSS（20 秒） | I-CSCF の UAR に答えが無く、I-CSCF は 5 秒で P-CSCF に 480 を返し、P-CSCF は UE に 504 を返した。I-CSCF、S-CSCF、IP-SM-GW の DWR に DWA が無い。IP-SM-GW は、凍結から約 19 秒で接続を FIN で閉じた。HSS が戻ると、もう待たれていない UAA を送り、閉じた接続への DWR には RST が返り、IP-SM-GW が CER から接続し直した |
| 通話中のメディア中継ノード（3 秒） | 中継ノードから着信側への RTP が 3.18 秒途切れた。戻ったあとは続きから流れ、通話は BYE まで続いた |

通話中に S-CSCF を `docker kill` し、2 秒後に `docker start` すると、BYE は通りました（200 OK）。ところが、そのあとの登録の解除には 481 が返りました。S-CSCF が登録の情報をプロセスの中だけに持っているためです。作り直しても再登録なしで着信できるようにするために直すべき点が、そのまま見えます。

### 再起動とネットワーク名前空間

`docker restart`、`docker stop` と `docker start`、再起動の方針による起動し直しのどれでも、コンテナには新しいネットワーク名前空間ができます。名前空間に入れた nftables の規則は、そこで消えました。

対象と名前空間を共有する常駐のコンテナ（サイドカー、`network_mode: "service:pcscf"`）も試しました。P-CSCF を再起動すると、サイドカーは古い名前空間に残り、インタフェースは `lo` だけになりました。エラーにならないまま、何もできなくなります。そのため、サイドカーは使いません。障害を入れるたびに、使い捨てのコンテナを対象の名前空間に入れます（`docker run --rm --network container:<コンテナ> --cap-add NET_ADMIN`）。凍らせたコンテナの名前空間にも入れました。規則は、使い捨てのコンテナが消えたあとも名前空間に残ります。

Compose の `run` で同じことをする案も試しました。`network_mode: "service:${FAULT_TARGET}"` と書くと、変数で対象を選べます。しかし、変数が無いときに `--profile chaos` 付きの `config` と `build` が `depends on undefined service` で失敗しました。道具のイメージを作るたびに対象を指定させるのは不自然なので、採りませんでした。

### 遅延と損失

| 案 | 結果 | 判断 |
| --- | --- | --- |
| 対象の名前空間で tc netem | この環境のカーネルに netem が無く、試せなかった | 遅延はこの方法にする。確かめていないことを、コードと説明に書いた |
| ホストの側の veth に tc netem | 試していない。netem が無いうえ、ホストの名前空間に入る権限が要る。veth に掛けると、キャプチャ（ブリッジ）を通ったあとで遅れるので、キャプチャとノードが受けたものが合わない。Docker Desktop では veth が VM の中にある | 採らない |
| 対象の名前空間で iptables の `OUTPUT` に `statistic` と `DROP` | 送る側の `sendto` が `EPERM` で失敗した（ping は `sendto: Operation not permitted`）。SIP のスタックは、これを送り直しの要る損失ではなく、送信の失敗として受ける。RFC 3261 17.1.4 では、送信の失敗でクライアントのトランザクションはすぐ終わる。`internal/sip` も `TransportError` で終える | 採らない |
| 対象の名前空間で nftables の netdev の egress と ingress に `numgen random` | 送る側には何も知らされず、パケットだけが消えた。実際の回線の損失と同じ | 損失はこの方法にする |

損失は、ノードのインタフェースの egress と ingress の両方に同じ割合で掛けます。ARP は捨てません（`meta protocol ip` に限る）。ダッシュボードへの HEP の写し（UDP の 9060）は捨てません。ダッシュボードが、ノードが送受信したと思っているものを表示し続けるためです。

損失を入れて見えたものです。

| 入れた損失 | 見えたもの |
| --- | --- |
| P-CSCF に 30 % | 登録が 2 秒で 504 になった。P-CSCF の DNS の A の問い合わせが失われ、`sipdns` が 2 秒で `i/o timeout` を返した。`internal/sipdns` は miekg/dns の既定の 2 秒で 1 回だけ問い合わせ、問い合わせ直さない |
| S-CSCF に 30 %、中継ノードに 10 % | P-CSCF から I-CSCF への SUBSCRIBE の送り直し（`sip.resend == 1`、0.5 秒、1 秒、2 秒）と、TCP の再送が写った。発信は S-CSCF の DNS の問い合わせが失われて 404 になった。UE 2 の reg イベントの購読は `no NOTIFY for the SUBSCRIBE` で失敗した |
| S-CSCF と中継ノードに 10 % | 発信は、同じく DNS の問い合わせが失われて 404 になった |
| 中継ノードだけに 10 % | 通話は成立し、BYE まで続いた。UE 1 から中継ノードへの RTP は 250 個、中継ノードから UE 2 へは 206 個がキャプチャにあった。UE 2 へのストリームでは、シーケンス番号が 35 か所で計 44 個抜けた。受けて捨てたもの（キャプチャにある）と、送って捨てたもの（キャプチャに無い）の両方が抜けになる |

DNS の問い合わせ 1 つで発信が失敗するのは、学習者にとっては分かりやすい現象です。ただし、問い合わせ直さないことと、S-CSCF が DNS の失敗に 404 を返すことが標準に合っているかは確かめていません。

### ネットワークの分断

| 案 | 結果 | 判断 |
| --- | --- | --- |
| `docker network disconnect` と `docker network connect --ip` | I-CSCF で試した。インタフェースが消え、`lo` だけになった。`--ip 172.28.0.20` で同じアドレスに戻り、そのあとの登録は成功した。キャプチャには、答えの無い 172.28.0.20 の ARP、P-CSCF の REGISTER の送り直し、HSS の TCP の再送が写った。I-CSCF の側では Diameter の監視が `OKAY` から `OKAY/Pending`、`SUSPECT`、`Closed` に下がり、接続し直しは `Rcv-Conn-Nack` ですぐ失敗し、つなぎ直したあとで `I-Open` に戻った。外れているあいだの登録は、32 秒でタイマー F が切れた | 説明の中で紹介する。ノードを丸ごと外すことしかできず、戻すときにアドレスが要るので、API には入れない |
| 対象の名前空間で iptables の `DROP` | `OUTPUT` は上の損失と同じく `EPERM` になる | 採らない |
| 両方のノードの名前空間で、相手への egress だけを nftables で捨てる | S-CSCF と HSS で試した。キャプチャには、どちらの向きも何も写らなかった。REGISTER は、S-CSCF が MAR に答えを得られず、5 秒で 500 になった。HSS の監視は `R-Open (OKAY)` から `R-Open (OKAY/Pending)` に移った。戻したあと、S-CSCF は約 4 秒後に新しい接続（SYN、CER、CEA）を開き、次の登録は成功した | 採らない。相手のノードの名前空間にも入る必要があり、キャプチャには何も残らない |
| 対象の名前空間だけで、相手への egress と相手からの ingress を nftables で捨てる | P-CSCF と I-CSCF で試した（API から）。キャプチャには、UE の REGISTER の送り直しが 4 秒（T2）まで間を広げて写った。32 秒後に P-CSCF が 504 を返したが、UE も同じ時にタイマー F で諦めていて、504 には ICMP の port unreachable が返った。P-CSCF から I-CSCF へのパケットは 1 つも写らなかった。ダッシュボードには、この間の P-CSCF の SIP の写しが 23 件あり、I-CSCF への REGISTER の送信が送り直しのたびに並んでいた | この方法にする。名前空間が 1 つで済み、相手をアドレスでも書ける。相手から届いたパケットはキャプチャに写るので、送り直しが見える |

相手を書かない分断は、対象のすべての IP パケットを捨てます（HEP の写しを除く）。

### 学習者から見えるもの

| 見るところ | 送って捨てたパケット | 受けて捨てたパケット |
| --- | --- | --- |
| キャプチャ（ブリッジ全体を 1 つのコンテナで取る） | 写らない | 写る。ノードには届いていない |
| ダッシュボード（HEP の写し） | 送ったものとして写る | 写らない |
| ノードのログ | 送ったものとして写る | 写らない |

キャプチャとダッシュボードを並べると、どこで消えたかが分かります。学習者向けの説明には、この見え方を書きました。

### 権限と Docker Desktop

| 障害 | 要る権限 | Docker Desktop |
| --- | --- | --- |
| 停止、kill、pause、crash | Docker を操作できること（ホストのコマンドか、Docker のソケット） | Docker のコマンドだけなので動くはずですが、確かめていません |
| 損失、分断 | 使い捨てのコンテナの `NET_ADMIN`。カーネルに nftables の netdev の egress のフック | VM のカーネルに egress のフックがあるかを確かめていません |
| 遅延 | 使い捨てのコンテナの `NET_ADMIN`。カーネルに `sch_netem` | VM のカーネルに netem があるかを確かめていません |
| ネットワークから外す | Docker を操作できること | 確かめていません |

`capture` のサービスは `network_mode: host` なので、その名前空間はホストのものです。ここに規則を入れるとホストが変わるので、`fault.sh` も API も、ネットワークの障害を断ります。

### ノードの中に作る方法

SIP と Diameter のトランスポートに、受けたメッセージを捨てたり遅らせたりする仕組みを入れる方法もあります。権限が要らず、Kubernetes でも同じように動きます。ただし、DNS の問い合わせや TCP の再送のように、カーネルとライブラリが扱う部分には効きません。受ける側で捨てると、ブリッジのキャプチャには届いたパケットが写るのに、TCP の確認応答は返る、という実際には起きない見え方になります。試作では、損失と分断をカーネルの中で起こせたので、この方法は作りません。

### 決めたこと

- 道具のイメージは `deploy/chaos/tools`（alpine 3.24 に nftables と iproute2-tc）にします。上書きのファイル `deploy/chaos/chaos.yaml` の `fault-tools` のサービスで作ります。このサービスは作るためだけのもので、プロファイル `chaos` に入れ、`network_mode: none` にします。
- 障害は、対象のコンテナの名前空間に入る使い捨てのコンテナで入れます。IMS のイメージには道具も権限も足しません。
- 損失と分断は nftables の netdev の egress と ingress、遅延は tc netem です。規則は障害ごとに 1 つの表（`ims_fault_<id>`）にまとめ、表を消して戻します。nft の 1 回の呼び出しは 1 つのトランザクションなので、表は全部入るか、何も入らないかのどちらかです。
- `fault.sh` を学習者向けの入口にします。どの操作も、実行する `docker` のコマンドを表示します。`heal` で全部を戻します。
- `internal/fault` は、ダッシュボードと OAM から呼ぶ API です。障害には必ず期限があり（既定 5 分、最大 1 時間）、期限が来ると自動で戻します。時計は試験で差し替えられます。終わった障害は 1 時間だけ一覧に残します。
- Compose での実行は `fault.Docker` が行います。Docker Engine API（版 1.44）を Unix のソケットで呼びます。docker の CLI を使わないので、今の IMS のイメージのまま動きます。ログには、同じ操作をする `docker` のコマンドを `equivalent` として出します。
- Docker のソケットは、IMS のノードには渡しません。ソケットを持つプロセスは、ホストの root と同じことができるためです。API を動かすコンテナは、障害注入のための上書きのファイルでだけソケットを読み込み、`compose.yaml` には入れません。このコンテナの定義と、ノードの起動コマンドへのつなぎ込みは作っていません。ホスト固有の設定に依存しないという方針との関係を整理する必要があるためです（この章の「未決の論点」）。

### API

`internal/fault` のパッケージの文書に全体を書きました。

| 操作 | 内容 |
| --- | --- |
| `GET /fault/v1/faults` | 障害の一覧。古い順 |
| `POST /fault/v1/faults` | 障害を入れる。本文は `{"target":"pcscf","kind":"loss","loss_percent":10,"duration":"2m"}` など。成功は 201、要求の誤りは 400、同じ対象の同じ枠に終わっていない障害があれば 409、実行に失敗すれば 500（本文に失敗した障害） |
| `GET /fault/v1/faults/{id}` | 1 つの障害 |
| `DELETE /fault/v1/faults/{id}` | 期限の前に戻す |

種類は `stop`、`kill`、`pause`、`delay`（`delay`、`jitter`）、`loss`（`loss_percent`）、`partition`（`peers`）です。停止と pause と kill は、1 つの対象に同時に 1 つだけです。遅延、損失、分断は、種類ごとに 1 つずつ並べられます。

API のプロセスが落ちると、入れた障害の記録は消えます。規則とコンテナの状態は残るので、`fault.sh heal` で戻します。API は IMS のノードではなく、障害の数も少ないので、記録を外に置くこと（状態をノードの外に置けるようにするという方針の対象）はしません。

### 試した手順

コンテナの名前とプロジェクト名は、この環境のもの（`ims-chaos`）です。

```sh
# カーネルの機能
zcat /proc/config.gz | grep -E "NET_SCH_NETEM|NETFILTER_EGRESS|NFT_NUMGEN"
docker run --rm --cap-add NET_ADMIN <道具のイメージ> tc qdisc add dev eth0 root netem delay 200ms

# ノードの停止
docker stop ims-chaos-scscf-1; docker start ims-chaos-scscf-1
docker kill ims-chaos-scscf-1; docker start ims-chaos-scscf-1
docker exec ims-chaos-scscf-1 kill -QUIT 1
docker inspect -f '{{.State.Status}} {{.RestartCount}}' ims-chaos-scscf-1
docker pause ims-chaos-pcscf-1; docker compose -p ims-chaos run -d ue register --deregister-after 2s; sleep 7; docker unpause ims-chaos-pcscf-1

# 損失（API と同じ規則を fault.sh で）
deploy/chaos/fault.sh loss agw 10
deploy/chaos/fault.sh show agw
deploy/chaos/fault.sh clear agw

# 分断
deploy/chaos/fault.sh partition pcscf icscf
docker network disconnect ims-chaos_ims ims-chaos-icscf-1
docker network connect --ip 172.28.0.20 ims-chaos_ims ims-chaos-icscf-1

# API（Docker の実行部分をホストで動かした試作のプログラムから）
curl -X POST -d '{"target":"pcscf","kind":"partition","peers":["icscf"],"duration":"40s"}' http://127.0.0.1:18090/fault/v1/faults
curl -X POST -d '{"target":"pcscf","kind":"delay","delay":"200ms","jitter":"20ms","duration":"30s"}' http://127.0.0.1:18090/fault/v1/faults

# キャプチャの確かめ方
tshark -r captures/ims-<時刻>.pcap -Y 'sip.resend == 1'
tshark -r captures/ims-<時刻>.pcap -Y 'diameter.cmd.code == 282 || (tcp.port == 3868 && tcp.flags.fin == 1)'
tshark -r captures/ims-<時刻>.pcap -o rtp.heuristic_rtp:TRUE -Y 'rtp && ip.src == 172.28.0.50' -T fields -e rtp.seq
```

UE は e2e のスクリプトと同じく `docker compose run --rm ue register` と、`ue2 answer` と `ue call` で動かしました。上の観察は、MMTel AS と 3 台目の UE を足す前の `compose.yaml` で行いました。そのあと、それらを足した `compose.yaml` に載せ替えて、登録、発信、中継ノードの 10 % の損失の下での通話、P-CSCF と I-CSCF の分断、P-CSCF の pause、S-CSCF の crash、`heal` をもう一度試し、同じ結果になりました。API の試作では、期限が来た障害（40 秒の分断、8 秒の pause、5 秒の kill）が自動で戻ること、DELETE で HSS の停止が戻ること、遅延が `tc exited with 2: Error: Specified qdisc kind is unknown.` で失敗として記録されること、P-CSCF を再起動したあとの損失の DELETE が「すでに無い」として成功することを確かめました。

### 冗長構成と複数のネットワークへの対応

冗長構成と、複数のネットワークにつながるノードに対象を広げました。前の `fault.sh` と `internal/fault` は、サービスごとにコンテナが 1 つ、という構成を前提にしていたためです。対象は、冗長構成で増えるサービスの名前（[状態と冗長構成の設計](../design/state-redundancy.md) の「障害注入」）に合わせました。

#### 変えたこと

前の `fault.sh` と `internal/fault` は、サービスごとにコンテナが 1 つで、インタフェースは `eth0` の 1 つだけ、という前提でした。`deploy/interconnect` の IBCF と TrGW は 2 つのネットワークにつながり、冗長構成は同じ役割の台を複数動かします。そこで、対象を名前の一覧ではなく、次の決まりで選ぶようにしました。

| 決まり | 内容 |
| --- | --- |
| コンテナの探し方 | Compose がコンテナに付けるラベル（プロジェクト、サービス、コンテナの番号）で探す。`compose.yaml` のどのサービス（`mmtel`、`epc`、`volte-a`、`volte-b` など）も、上書きのファイルのサービス（`ibcf-a`、`trgw-b`、`peer-scscf` など）も、名前の一覧を持たずに対象になる |
| 1 つのコンテナ | `サービス:番号`（`agw:2`）。`--scale` で複数のコンテナを動かすサービスの 1 つを選ぶ。番号を書かずに複数あれば、番号の一覧を付けて断る。冗長構成の `scscf-1` と `scscf-2` は別のサービスなので、そのまま名前で選ぶ |
| `docker compose run` の UE | `ue`、`ue2`、`ue3` は `run` で起動する。`up` で作ったコンテナが無いサービスでは、動いている `run` のコンテナを対象にする。終わった `run` のコンテナは対象にしない |
| 分断の相手 | サービス（そのすべてのコンテナ）、1 つのコンテナ、名前のパターン（`path.Match`。`'scscf-*'` で役割のすべての台）、IPv4 のアドレス。相手が 2 つのネットワークにつながっていれば、両方のアドレスを使う |
| インタフェース | 対象の名前空間で `ip -o -4 addr show` を実行し、Docker が報告するアドレスを持つインタフェースを探す。Docker はインタフェースの名前を自分で決めるので、ネットワークの名前から `eth1` などを決め打ちにしない。nftables の規則は、インタフェースごとにチェイン（`egress_eth1` など）を作る |
| ネットワークを選ぶ | `--network NET`（API では `network`）で、Compose のネットワーク `NET`（Docker の名前では `<プロジェクト>_NET`）の側のインタフェースだけに入れる。無ければ、すべてのインタフェースに入れる。`ibcf-a` と `trgw-a` に `--network nni` の分断を入れると、2 つの網のあいだの回線（NNI）を網 A の側で切れる |
| 作り直し | `fault.sh recreate SERVICE` は、`docker compose kill`、`rm -f`、`up -d --no-deps` を続けて行う。落ちたノードを置き換える操作で、10 章の場面と 12 章の測定が使う。名前付きのボリュームは残るので、AOF を持つ保存先はデータを読み直す。期限のある障害ではないので、API には入れていない |
| 保存先の障害 | 保存先（サービスの名前は `valkey`）は、ほかのノードと同じく `stop`、`kill`、`pause`、`recreate`、分断の対象になる。永続化の有無は保存先の設定で決まるので、障害の種類は分けない。永続化を切った構成は、12 章の測定のための上書きのファイル（`deploy/chaos/measure/store-volatile.yaml`）で作る |
| 権限 | Docker のソケットは今までどおり、どこにも渡さない。ホストのネットワークを使うキャプチャのコンテナ（`capture`、`capture-peer`、`capture-nni`）への、ネットワークの障害は断る |

`fault.sh heal` は、サービスの一覧ではなく、プロジェクトのラベルを持つすべてのコンテナを回ります。凍ったものを戻し、止まったもの（`run` のコンテナを除く）を起動し、動いているものの規則を消します。

#### 確かめたこと

どれも、この環境の Docker Engine 29.10.0、Docker Compose v5.7.1、Linux 6.18 で実際に動かしました。IMS のイメージは、ホストで作った Go のバイナリとダッシュボードの画面を `alpine:3.24` に入れて作りました（BuildKit がプロキシの証明書を信用しないので、リポジトリの Dockerfile ではビルドできないため）。Compose のプロジェクトには専用の名前を付けて、ほかの作業の資源とは分けました。

先に、IMS を使わない小さな構成（alpine のコンテナと Valkey 9.0.8）で、決まりの 1 つずつを確かめました。

| 確かめたこと | 結果 |
| --- | --- |
| 2 つのコンテナのサービスを番号なしで選ぶ | `the service scaled has 2 containers (scaled:1, scaled:2); name one` で断った |
| `サービス:番号` | 2 つ目のコンテナだけが凍った |
| `run` の UE | 動いている `run` のコンテナが選ばれた。止めたあとは対象にならなかった |
| 2 つのネットワークのコンテナに `--network` で分断 | 片方のインタフェース（この構成では `eth1`）にだけ規則が入り、もう片方のネットワークへの ping は通った |
| 分断の相手にパターン、番号、`run` の UE、アドレス | 合うコンテナのアドレスがすべて規則の集合に入り、名前の合わない台からは保存先に届いたままだった |
| 保存先の kill と start | AOF を 1 秒ごとに同期する保存先は、書いた値を読み直した。永続化を切った保存先は空になった |
| 保存先の `recreate` | 0.94 秒で終わり、同じアドレスで、ボリュームの値を読み直した |
| ホストのネットワークのコンテナ | ネットワークの障害を断った。凍結と解凍はできた |
| `heal` | 凍ったもの、止まったもの、規則のあるものを、すべて戻した |
| `internal/fault` を本物の Docker で | 試験のときだけ置いたプログラムから `fault.Docker` と `Manager` を動かした。`network` を付けた分断、パターンの相手、`agw:2` のような番号の pause が入り、戻った。遅延は `tc exited with 2: Error: Specified qdisc kind is unknown.` で失敗として記録された。ホストのネットワークと、番号の無い複数のコンテナは断った |

次に、`compose.yaml` に `deploy/interconnect` を重ねた構成で、3 つの障害を通話中に入れ、キャプチャと UE のログを見ました。そのあとで `compose.yaml` だけの構成でも、MMTel AS の凍結をもう一度試し、同じ結果でした。

| 障害 | キャプチャと UE に見えたもの |
| --- | --- |
| 通話中に MMTel AS を凍らせる（`pause mmtel`、約 12 秒） | 音声は途切れなかった。UE 2 への RTP は 6 秒の音で 300 個、間は最大 106 ミリ秒だった。RTP は AS を通らないためです。UE 1 の BYE は AS で止まり、UE、P-CSCF、S-CSCF が 0.5 秒、1 秒、2 秒、4 秒の間で送り直した（`sip.resend == 1`、タイマー E）。解凍すると、AS は溜まっていた BYE の写しのそれぞれに 200 を返し（4 つ）、そのあとでもう片方の脚に自分の BYE を送った。BYE を送ってから UE 1 に 200 が届くまで 8.1 秒で、UE 1 は正常に終わった |
| 網 B への通話中に IBCF A を殺す（`kill ibcf-a`、3 秒後に `start`） | 回線のキャプチャの TrGW のあいだの RTP は、10 秒で 500 個、間は最大 23 ミリ秒で、途切れなかった。起動し直した IBCF A は、最後の BYE を Route に従って IBCF B へ送り、両方の網で呼が終わった。ただし、TrGW A に呼のコンテキストが残った（`GET /contexts`）。新しい IBCF A は、前の台がメモリに持っていた `Media` を知らず、解放しなかったためです。TrGW B には残らなかった。IBCF の `Store` は保存先に載せないので（[状態と冗長構成の設計](../design/state-redundancy.md)）、冗長構成のあとも同じです |
| 通話中に網 A の側の回線を切る（`partition --network nni` を `ibcf-a` と `trgw-a` に） | 回線のキャプチャでは、TrGW B から TrGW A への RTP が続き（1413 個。TrGW A がブリッジのあとで捨てた）、網 A からのパケットは切ったあとで 1 つも無かった。IBCF B から IBCF A への TCP の keep-alive が 15 秒ごとに写り、答えは無かった。UE 1 の BYE には MMTel AS がすぐ 200 を返し、AS が網 B へ送った BYE を、S-CSCF が IBCF A へ送り直し続けた。32 秒後（タイマー F）に IBCF A が 504 を返した。UE B1 には BYE が届かず、網 B の側の呼と TrGW B のコンテキストは残った。切っているあいだの新しい呼は、IBCF A の 100 Trying のあと、約 34 秒で UE に 504 Server Time-out が返った。IBCF A は INVITE が 1300 バイトを超えるので TCP で送ろうとし、2 秒で UDP に替え、タイマー B で諦めて TrGW のコンテキストを解放した。`fault.sh show` の数は、IBCF A が送って捨てたものが 26 個、受けて捨てたものが 10 個、TrGW A が 127 個と 1363 個だった。戻したあとの呼はつながった |

#### 作っただけで確かめていないこと

- 遅延（netem）。この環境のカーネルには `sch_netem` が無く、今回も試せませんでした。インタフェースが複数あるときに 1 つずつ入れ、途中で失敗したら入れた分を戻す作りは、偽の Docker の試験でだけ確かめました。
- 冗長構成の台の名前（`pcscf-1`、`scscf-1`、`valkey` など）。この試作の時点では `deploy/redundancy` が無く、IMS の構成では試していません。小さな構成で、同じ決まり（別のサービスとしての台、パターン、保存先の kill と recreate）を確かめただけです。
- `--scale` で複数のコンテナを持つ IMS のサービス。冗長構成は固定のアドレスの別々のサービスを使うので、IMS では作っていません。
- Docker Desktop での動き。8 章の「権限と Docker Desktop」のとおり、試していません。

#### 未決の論点

- 障害注入の API のコンテナ（`deploy/chaos/agent.yaml`）は作っていません。Docker のソケットを読み込むコンテナを足すことが、ホスト固有のネットワーク設定に依存しないという方針と合うかを、先に決める必要があります。
- IBCF A を作り直すと TrGW のコンテキストが残ります。これは、手順書で見せる現象として扱うか、`SIMPLIFIED:` の一覧に足すかを決める必要があります。IBCF の `Store` は保存先に載せないと決めてあります（[状態と冗長構成の設計](../design/state-redundancy.md)）。

## 9. ドレイン

ドレインは、新しい登録と呼をほかのノードに回し、通話中の呼は終わるまで続ける操作です。

### 新しい要求を断る方法

| 役割 | 新しい要求への応答 | 上流の動き | 根拠 |
| --- | --- | --- | --- |
| P-CSCF | 新しい REGISTER と、ダイアログの外の要求に 503 と Retry-After | UE は別の P-CSCF で登録する | RFC 3261 21.5.4、RFC 3263 4.3。UE の 503 と 305 の扱いは IR.92 2.2.1 が求めている（[profiles.md](profiles.md)） |
| I-CSCF | 503 | P-CSCF は、DNS の次の I-CSCF へ送る。今の P-CSCF の `forward`（`internal/pcscf/pcscf.go`）は、REGISTER を時間切れ、3xx、480 で次へ回すので、503 を足す | RFC 3263 4.3。TS 24.229 の P-CSCF の転送の手順も根拠になるが、条項を確かめていない |
| S-CSCF | REGISTER に 480 か 3xx | I-CSCF が別の S-CSCF を選ぶ | TS 24.229 5.3.1.3 |
| S-CSCF | ダイアログの外の要求に 503 | 組の名前を使っていれば、上流が DNS の次の台へ送る | RFC 3263 4.3 |
| メディア中継ノード | `Add` に 503 | P-CSCF が次の中継ノードを選ぶ | このプロジェクトの API（[メディア中継ノードの制御の設計](../design/media-control.md)） |
| HSS | 新しい要求に DIAMETER_TOO_BUSY か、DPR で接続を閉じる | CSCF が別の接続へ送る | RFC 6733 の条項は確かめていない |

ダイアログの中の要求と、処理の途中のトランザクションは、ドレイン中も処理します。

DNS からそのノードを外す方法もあります。CoreDNS のゾーンのファイルを書き換えて読み直させます。ゾーンの TTL は今 60 秒なので、効くまでに最大 60 秒かかります。503 を返す方法は、すぐに効き、キャプチャに理由が見えます。結論は 503 で、DNS の書き換えは作りません。

### すでに割り当てた利用者

S-CSCF を台ごとの名前で割り当てる構成（7 章の案 1 と 3）では、ドレイン中の S-CSCF に割り当て済みの利用者が残ります。再登録は、HSS がその S-CSCF の名前を返すので、同じ S-CSCF に届きます。

| 案 | 内容 |
| --- | --- |
| 1. 期限まで待つ | 再登録には 480 を返さず、処理を続ける。新しい利用者だけを断る。ドレインが終わるまでに、登録の期限（最大 3600 秒）だけかかる |
| 2. 登録し直させる | S-CSCF が、reg イベントの NOTIFY で `deactivated` を送り、UE に登録し直させる（TS 24.229 5.4.1.5）。SAR は ADMINISTRATIVE_DEREGISTRATION。次の REGISTER は、HSS に S-CSCF の名前が無いので、I-CSCF が別の S-CSCF を選ぶ |

2 は、今の `SCSCF.Deregister`（`internal/scscf/scscf.go`）がすでに行う処理です。今は試験からしか呼ばれません。ただし、利用者に通話中の呼があるときに登録を解除すると、TS 24.229 5.4.1.5 は、その連絡先のセッションを解放するよう求めています（`deregisterByNetwork` の `SIMPLIFIED:`）。通話中の利用者は 1 で待ち、そうでない利用者だけを 2 で移すには、S-CSCF が通話中かどうかを知る必要があります。今の S-CSCF はダイアログの状態を持たないので、分かりません。

結論は、組の名前と共有の保存先（7 章の案 3）を使い、組の中のドレインでは利用者を動かさないことです。台を止めても、同じ組のほかの台が、同じ名前で同じ利用者を扱えます。組ごとのドレインは 1（期限まで待つ）にします。

### ドレインが終わったことの判断

| 役割 | 終わったと言える条件 |
| --- | --- |
| I-CSCF、S-CSCF（組の名前を使うとき） | 処理の途中のトランザクションが無くなった。ダイアログの中の要求は、ほかの台が処理できる |
| P-CSCF | そのアドレスに登録している UE がいなくなり、メディアのコンテキストを持つ呼が無くなった |
| メディア中継ノード | コンテキストが無くなった（`GET /contexts` が空） |

P-CSCF は、UE がアドレスに結びついているので、ドレインに登録の期限と同じだけの時間がかかります。早めるには、P-CSCF が UE に登録し直させる必要があります。PCO で新しい一覧を送る方法（TS 23.380 5.1.2）が、EPC スタブの上で作れます。

### 操作の入口

ノードに、ドレインを始める操作と状態を返す操作が要ります。今のノードには、保守用の入口がありません。HSS の `provisioning_listen`、中継ノードの `control`、ダッシュボードだけが HTTP を持ちます。

どのノードにも共通の保守用の待ち受けを足し、ドレインの開始と状態の取得の操作を置くのがよいと考えました。ダッシュボードがこれを呼びます。最初に考えた形は、設定 `admin_listen` と、`POST /drain`、`DELETE /drain`、`GET /status` の 3 つの操作でした。この実装では、各ノードの保守用の API の部品 `internal/maintenance` を使います。形は次のとおりです（[状態と冗長構成の設計](../design/state-redundancy.md)の「ドレイン」）。

| 最初に考えた形 | この実装の形 |
| --- | --- |
| 設定 `admin_listen` | 設定 `maintenance.listen`。無ければ保守用の API を開かない |
| `POST /drain` | `PUT /maintenance/v1/functions/{role}/administrative-state` に `SHUTTINGDOWN`（`timeout_seconds` も渡せる） |
| `DELETE /drain` | 同じ操作に `UNLOCKED` |
| `GET /status` | `GET /maintenance/v1/state`。役割ごとの管理の状態（administrativeState、TS 28.622 5.3.1）と、役割がまだ扱う利用者の数 |
| ドレインが終わったことの判断（上の表） | 役割の `Users` が 0 になると、`SHUTTINGDOWN` から `LOCKED` に移る。`Users` が数えるものは、役割ごとに設計の表にある |

役割は、新しい登録やセッションを受ける前に `ManagedFunction.AdministrativeState` で状態を読みます。役割ごとの断り方（503 と Retry-After、HSS の DIAMETER_TOO_BUSY）は、上の「新しい要求を断る方法」の表とほぼ同じで、設計の表が正です。S-CSCF の再登録に 480 か 3xx を返す行は採らず、組の中の S-CSCF は利用者を動かしません。ドレインの状態はプロセスの中にあり、作り直した台は `UNLOCKED` で起動します。

未決の論点として、SIGTERM を受けたときにドレインしてから止まる動きを作るかがあります。Kubernetes で使えます。

## 10. 確かめる場面

冗長構成を Compose で組んだときに、次の場面を確かめます。


- 通話中に S-CSCF のコンテナを削除して作り直す。そのあとの re-INVITE か BYE が通り、呼が正常に終わる。登録済みの UE に、再登録なしで着信の INVITE が届く。
- 通話中に P-CSCF のコンテナを削除して作り直す。RTP が続き、BYE で両方の中継のコンテキストが解放される。
- ドレイン中の P-CSCF を通る呼が BYE まで続く。ドレインのあとの新しい登録と呼が、別の P-CSCF を通る。pcap に 503 がある。
- S-CSCF の組を止める。次の REGISTER で、I-CSCF が REGISTRATION_AND_CAPABILITIES の UAR を送り、別の組に `scscf-reselection` の付いた REGISTER を送る。
- 外部の保存先を使わない構成で S-CSCF を再起動する。着信の INVITE で、S-CSCF が SAR（UNREGISTERED_USER）を送り、SAA に SCSCF-Restoration-Info がある。INVITE が UE に届く。
- P-CSCF を別のアドレスの台に替える。着信の INVITE に 480 が返り、SAR に SAR-Flags がある。UE が新しい P-CSCF で登録し直す。
- 遅延とパケット損失を入れる。SIP の再送が pcap にあり、呼が成立する。
- 中継ノードを作り直す。RTP が途切れたあとで戻る（設計判断で決めた範囲）。

## 11. 外部の保存先の試作（`internal/kvstore`）

外部の保存先を試作し、各役割の `Store` を載せて測った記録です。各役割の `Store` は読むだけにして、変えていません。

### 11.1 `Store` ごとに要るもの

各役割の `Store` とメモリの実装を読み、キーと値の保存先に何が要るかを書き出しました。値の大きさは、項目を JSON にしたときの長さです。試験用の値（IMPU を 3 つ、iFC を 3 つなど）を入れて、試験の中で測りました。目安の数字です。

| `Store` | 項目 | キー | 値の大きさ | 期限 | 2 次の検索 | 不可分に要る操作 | 期限が来たとき |
| --- | --- | --- | --- | --- | --- | --- | --- |
| S-CSCF（`internal/scscf/store.go`） | Challenge | IMPI | 約 430 B | reg-await-auth（4 分） | なし | なし（利用者ごとのロックの中で使う） | SAR（AUTHENTICATION_TIMEOUT）を送る。1 台だけが送る |
| | Registration | IMPI | 約 1.8 KB | 登録の期限 | IMPU から IMPI（`RegistrationOf`） | 利用者ごとのロック（6 章の案 1）。Cx の往復をまたぐ | 消して、NOTIFY と SAR（TIMEOUT_DEREGISTRATION）を送る。1 台だけ |
| | Subscription | ダイアログの ID | 約 630 B | 購読の期限 | IMPI から購読の ID の集合（`SubscriptionsOf`） | NOTIFY のたびに CSeq と版を書き戻す。利用者ごとのロックの中 | NOTIFY（terminated）を送る。1 台だけ |
| | OriginalDialog | 乱数の ID | 約 1.5 KB | AS から戻るのを待つ時間 | なし | `Returned` を立てて書き戻す | 消えるだけ |
| | UnregisteredProfile | IMPI | 約 1.4 KB | 設定の時間 | IMPU から IMPI（`UnregisteredProfileOf`） | 期限が同じときだけ消す（`DeleteUnregisteredProfile`） | 消して SAR を送る。1 台だけ |
| P-CSCF（`internal/pcscf`） | Association | 送信元（トランスポート、アドレス、ポート） | 約 550 B | 登録が終わったあとの猶予（64×T1） | Contact から送信元（`AssociationOfContact`）、ポリシーのセッションから送信元 | なし。1 回で書く | 消えるだけ |
| | Dialog | Call-ID と UE の tag | 約 790 B | なし（BYE まで） | ポリシーのセッションからキー | `UpdateDialog`（読んで、変えて、書く）、`TakeDialog`（1 人だけが取る） | 2xx の ACK を待つタイマー（`release.go`）がプロセスの中にある |
| | SecurityAssociations | UE のアドレスと port_ps | 約 530 B | SA の期限 | なし | なし | 消えるだけ |
| IP-SM-GW（`internal/ipsmgw/store.go`） | RP-MR の数 | 宛先 | 数 B | 10 分 | なし | 読んで 1 を足して書く | 消えるだけ |
| | PendingDelivery | Call-ID | 約 160 B | TR1N の 2 倍 | なし | `TakeDelivery`（1 人だけが取る） | TR1N が切れたら、SC に失敗を知らせる。今は送った台のタイマー。1 台だけ |
| | RegSubscription | Call-ID | 約 550 B | 購読の期限 | 公開 ID から Call-ID（`RegSubscriptionOf`） | 同じ公開 ID の古い購読を消して置き換える（2 つのキー） | 消えるだけ（第三者の REGISTER のたびに更新する） |
| MMTel AS（`internal/mmtel/store.go`） | CallRecord | 2 つのダイアログの ID のどちらでも引く | 約 730 B | なし（呼が終わるまで） | 2 本目のダイアログの ID から主のキー | なし。2xx のときに 1 回書き、呼が終わったら消す | なし。CFNR の無応答のタイマーは、INVITE のトランザクションと同じくプロセスの中にある |
| SC（`internal/sc/store.go`） | StatusReportRequest | 配達の参照 | 小さい | 設定の時間 | なし | `TakeStatusReportRequest`（1 人だけが取る） | 消えるだけ |
| EPC（`internal/epc/store.go`） | PDNConnection | UE のアドレス | 約 640 B（Rx のセッションが 2 つ） | なし | なし | `UpdatePDNConnection`、`TakePDNConnection` | なし |
| | Binding | Rx の Session-Id | 約 40 B | なし | なし | なし | なし |
| 5GC（`internal/fivegc/store.go`） | UEContext | SUPI | 約 40 B | なし | なし | `TakeUEContext` | なし |
| | PDUSession | UE のアドレス | 約 320 B（N5 のコンテキストが 1 つ） | なし | なし | `UpdatePDUSession`、`TakePDUSession` | なし |
| | Binding | appSessionId | 約 40 B | なし | なし | なし | なし |
| SC の本体（`internal/smsc/store.go`） | 宛先ごとの待ち行列 | 宛先の MSISDN | 1 通あたり約 660 B | メッセージごとの有効期間 | なし | リースを持つ台だけが変える（`Remove`、`Release`） | 時刻の索引で、時刻の来た宛先を 1 台が借りて配る |
| | 時刻の索引 | 1 つ | 宛先ごとに 1 項目 | なし | なし | 借りるときに、時刻をリースの終わりに書き換える | （上と同じ） |
| | TP-SCTS の最後の値 | 宛先 | 数 B | 2 秒 | なし | 読んで、比べて、書く | 消えるだけ |
| HSS（参考。`internal/hss/store.go`） | Subscriber | IMPI | 測っていない | なし | IMPU から IMPI | `CompareAndSwapSQN`、`CompareAndSwapRepositoryData` | なし |

まとめると、要るものは次の 6 つです。

1. キーで読む、書く、消す。どの検索もキーで引けます。全部の項目を回るのは、今の `MemoryStore` の 3 か所（`RegistrationOf`、`SubscriptionsOf`、`AssociationOfContact`）と HSS の一覧だけです。前の 3 つは、5 章のとおり 2 次の検索の索引で置き換えられます。索引も、値に主の項目のキーを入れた項目にすれば、キーで引けます。
2. 期限。5 章のタイマーの表の「消えるだけでよいもの」は、保存先が消します。
3. 比べて入れ替える操作。`UpdateDialog`、`UpdatePDNConnection`、`UpdatePDUSession`、RP-MR の数、TP-SCTS、HSS の SQN は、どれも「読んだときのままなら書く」で表せます。`Take` の付く操作は、「読んだときのままなら消す」で表せます。同じ版を読んだ台のうち、1 台だけが成功します。
4. 期限切れを 1 台だけが処理する仕組み。S-CSCF の 4 つのタイマー（reg-await-auth、登録、購読、未登録のプロファイル）、IP-SM-GW の TR1N、P-CSCF の ACK の待ち、SC の本体の次に配る時刻です。処理する台が止まったら、別の台が引き継ぐ必要があります。
5. 複数のキーをまとめて変える操作。2 次の検索の索引、IP-SM-GW の購読の置き換え、SC の本体の `Release` に出てきます。どれも、書く順番を決め、読むときに主の項目と照らせば、1 つのキーの操作の組み合わせで足ります（11.2）。
6. 利用者ごとのロック（6 章の案 1）。期限付きで「無いときだけ書く」操作で作れます。3 の比べて入れ替える操作（版が空のときだけ書く）と、2 の期限で表せます。

範囲で引く操作は要りません。要るのは HSS の加入者の一覧だけで、呼の処理には使わないためです。

#### 登録と呼で何回使うか

測るときの操作の組み合わせを決めるため、IMS 全体を 1 つのプロセスで動かす試験（`internal/ue` の試験の網）で、`Store` の呼び出しを数えました。数えるための包みは試験の中に一時的に置き、コミットしていません。網は P-CSCF と S-CSCF が 1 台ずつで、SIP Digest、AS なし、EPC なしです。数えたのは、MMTel AS を足す前の構成です。

| 流れ | 役割 | `Store` の呼び出し |
| --- | --- | --- |
| 1 台の UE の登録（P-CSCF の reg イベントの購読を含む） | S-CSCF | Challenge 2、PutChallenge 1、DeleteChallenge 1、Registration 1、PutRegistration 1、SubscriptionsOf 1、RegistrationOf 2、PutSubscription 1 |
| | P-CSCF | Association 2、PutAssociation 1、SecurityAssociations 1 か 2 |
| 1 回の呼（INVITE から BYE まで。両方の UE が同じ P-CSCF を通る） | P-CSCF | Association 1、AssociationOfContact 7、SecurityAssociations 10、PutDialog 2、UpdateDialog 8、TakeDialog 2 |
| | S-CSCF | RegistrationOf 3 |

これを、索引を使うキーと値の操作に直すと、次のようになります。`RegistrationOf` と `AssociationOfContact` は、索引の項目と主の項目の 2 回の読み出しです。`UpdateDialog` は、読み出しと比べて入れ替える操作の 2 回です。`TakeDialog` は、読み出しと `CompareAndDelete` です。期限は、時刻の索引に入れる操作と外す操作に数えます。

| 流れ | 読む | 書く | 比べて入れ替える | 消す | 時刻の索引 |
| --- | --- | --- | --- | --- | --- |
| 登録 1 回 | 12 | 8（IMPU と Contact の索引の 4 つを含む） | 1（購読の索引） | 1 | 4（reg-await-auth を入れて外す、登録、購読） |
| 呼 1 回 | 41 | 2 | 8 | 2 | 0 |

IMS AKA を使うと、P-CSCF の SA の書き込みが増えます。EPC を通すと、Rx の AAR ごとに PDNConnection の読み出しと入れ替え、Binding の書き込みが足されます。MMTel AS を通すと、発信側と着信側の AS ごとに、S-CSCF の OriginalDialog の書き込みと読み出し、AS の CallRecord の書き込み（索引を含めて 2 回）と消去（2 回）が足されます。どれも、この数には入っていません。

### 11.2 インタフェースの形

`internal/kvstore` の `Store` は、11.1 の 6 つに要る操作だけを持ちます。

| 操作 | 内容 | 11.1 の使いどころ |
| --- | --- | --- |
| `Get`、`Put`、`Delete` | キーで読む、書く、消す。`Put` は期限（TTL）を取る。0 は期限なし | 全部の項目。期限は「消えるだけでよいもの」 |
| `CompareAndSwap` | 読んだときの版のままなら書く。版が空なら、項目が無いときだけ書く | `Update` の付く操作、数の更新、索引の集合、利用者ごとのロック |
| `CompareAndDelete` | 読んだときの版のままなら消す | `Take` の付く操作、期限が同じときだけ消す操作 |
| `Schedule`、`Unschedule` | 時刻の索引に、項目の名前を時刻付きで入れる、外す | 動作が要る期限（登録、購読、TR1N など） |
| `ClaimDue` | 時刻の来た名前を、早いものから決めた数だけ、リースを付けて借りる | どの台が期限切れを処理するかを決める |
| `Done` | 借りた名前を索引から外す。リースがまだ自分のものなら成功する | 処理が終わったとき |

決めたことは次のとおりです。

- 版は、書くたびに新しく作る乱数です。数え上げる番号にすると、項目を消して書き直したときに同じ番号が戻り、古い版で書けてしまいます（ABA）。乱数なら戻りません。乱数は、保存先のスクリプトではなく、クライアントで作ります。スクリプトの結果が引数だけで決まるようにしておくと、複製の台でも同じ結果になるためです。
- 時刻の索引の時刻は、ミリ秒の整数です。Valkey のソートされた集合の点数は倍精度の浮動小数点数で、ミリ秒なら丸めずに持てます。借りたあとの時刻（リースの終わり）が、借りたことの印を兼ねます。`Done` は、点数がその時刻のままのときだけ外します。借りているあいだに `Schedule` で時刻が動いたとき（再登録で期限が延びたとき）は、`Done` が `ErrClaimLost` を返します。
- `ClaimDue` の今の時刻は、呼ぶ側が渡します。`internal/smsc` と同じ形です。試験で時計を止めて確かめられます。期限（TTL）は保存先の時計で切れます。台どうしの時計がずれると、期限の処理がそのぶんずれます。リースは、ずれより十分長くします。
- 期限切れの処理は、2 回動いても壊れないように書く必要があります。借りた台が処理の途中で止まると、リースが切れたあとで別の台が借り直すためです。処理する台は、主の項目を読み、期限が同じかを確かめ、`CompareAndDelete` で消せた台だけが SAR や NOTIFY を送ります。今の `registrationExpired` の確かめ方と同じです。
- 複数のキーをまとめて変える操作は持ちません。Valkey の文書（topics/eval-intro.md、topics/cluster-spec.md）によると、スクリプトが触るキーは引数で渡す必要があり、クラスタでは複数のキーを 1 つの操作で触るには同じ区画（hash slot）に置く必要があります。どのスクリプトも 1 つのキーだけを触るので、クラスタでもそのまま動く形です（クライアントの側の対応は作っていません）。2 次の検索の索引は、索引の項目を先に書き、主の項目をあとで書きます。消すときは逆の順にします。索引から引いた主の項目が無いか、別の ID のものなら、見つからなかったことにします。途中で台が止まって残った索引の項目は、期限を主の項目と同じにすれば消えます。
- 全部のキーを回る操作は持ちません（規模を広げられなくなるため）。範囲で引く操作も持ちません。要るのが HSS の加入者の一覧だけで、呼の処理には使わないためです。
- 時刻の索引は、名前ごとに 1 つのソートされた集合です。1 つの役割の期限をすべて 1 つの集合に入れると、クラスタでは 1 つの区画に集まります。規模を広げるときは、索引を名前で分けます（たとえば IMPI の hash で 16 個に分け、各台が順に引く）。分け方は使う側で決められるので、`Store` には入れていません。

使い方の例は `internal/kvstore/example_test.go` にあります。登録を IMPI で書き、IMPU の索引から引き、期限を時刻の索引に入れて、1 台が借りて消す流れです。

### 11.3 外部の保存先のクライアント

調査の結論は Valkey と go-redis でした。依存を足す前に、リポジトリの外の試作用のモジュールで、次の 2 つを同じ適合試験にかけて比べました。

| | a. go-redis | b. 標準ライブラリだけで書いたクライアント |
| --- | --- | --- |
| 版 | github.com/redis/go-redis/v9 v9.23.0（2026-10-05） | `internal/kvstore/resp.go`、約 230 行 |
| ライセンス | BSD 2-Clause（モジュールの LICENSE で確かめた） | このリポジトリと同じ |
| 一緒に入る依存 | github.com/cespare/xxhash/v2 v2.3.0（MIT）、go.uber.org/atomic v1.12.0（MIT）、golang.org/x/sys（すでに `go.mod` にある）。`go mod tidy` のあとの `go.mod` で確かめた。どちらも LICENSE の文書で確かめた | なし |
| 大きさ | 試験を除いて約 7 万 5 千行 | 上のとおり |
| 適合試験 | 通った（Valkey 9.0.8） | 通った（Valkey 9.0.8、`-race`） |
| 接続を切られたあと | 次の操作は自動で送り直され、失敗しなかった（3 回の `Get` で失敗 0） | 1 回だけ失敗し、次の操作で新しい接続を開く。送り直さない |
| 遅延（11.4） | ほぼ同じ | ほぼ同じ |
| スクリプト | EVALSHA を送り、サーバーが知らなければ EVAL で送り直す | 毎回 EVAL で本文を送る |

b を選び、`internal/kvstore` の外部の実装は b で作りました。依存は足していません。理由は次のとおりです。

- 要る命令が少ないためです。HMGET、DEL、ZADD、ZREM と、4 つの Lua のスクリプトを送る EVAL だけです。RESP2 の書き方と読み方は 1 つのファイルに収まり、学習者が Valkey の文書（topics/protocol.md、「Serialization protocol specification」）と照らして読めます。
- 接続が切れたときと時間切れのときの動きを、試験で確かめられたためです。偽のサーバーを使う試験で、応答しないサーバー、文脈の取り消し、応答のあとで接続を閉じるサーバー、RESP でない応答、エラーの応答を確かめています。これらは Valkey が無くても常に動きます。Valkey がある環境では、`CLIENT KILL` で接続を全部切ったあとの動きも確かめています。
- 送り直さないのは、送ったあとで接続が切れたとき、サーバーが命令を実行したかどうか分からないためです。11.5 の測定で、応答を受け取る前に保存先が落ちた書き込みが、AOF に残っていた例がありました。送り直すかどうかは、操作の意味を知っている呼ぶ側が決めます。`CompareAndSwap` を送り直すと、自分の書き込みと衝突して `ErrConflict` になることがあります。
- 失敗した接続を閉じるときに、待っている接続もすべて閉じます。保存先が再起動すると、待っている接続はたいてい全部死んでいるためです。こうすると、同時に動いている操作が無ければ、1 つのクライアントで失敗するのは 1 回で済みます。

a を使うと増えるものは次のとおりです。自動の送り直し、接続の健全性の確かめ、パイプライン、RESP3、Pub/Sub、Sentinel とクラスタへの対応（MOVED と ASK の扱い）、計測のためのフックです。保存先の冗長を Sentinel かクラスタで組むと決めたとき（11.5）は、b を広げるより a に替えるほうが小さくなる見込みです。`Store` のインタフェースは変わりません。a の実装は試作用のモジュールに置き、適合試験が通ることを確かめただけで、リポジトリには入れていません。差分の要点は 11.7 に書きます。

### 11.4 呼の処理に足される遅延

11.1 の数え方で、登録 1 回と呼 1 回の操作を順に行う時間を、Go のベンチマーク（`internal/kvstore/bench_test.go`）で測りました。ノードは操作を 1 つずつ順に行うので、操作の往復の時間が足し算で効きます。

測り方は次のとおりです。

- 1 台の仮想マシン（4 vCPU、Intel Xeon 2.10GHz、Linux）の中です。Valkey 9.0.8 のコンテナ（`valkey/valkey:9.0.8-alpine`、永続化なし）を動かしました。
- 「ループバック」は、コンテナをホストのネットワーク（`--network host`）で動かし、127.0.0.1 につないだものです。「ポートの公開」は、ブリッジのネットワークで動かし、`-p 127.0.0.1:16379:6379` で公開したポートにつないだものです。Docker の転送の処理（docker-proxy）を通ります。
- 1 回の測定で 5000 回くり返し、1 回ごとの時間の中央値（p50）と 99 パーセンタイル（p99）を取りました。3 回測り、その中央値を書きます。同じ機械で、ほかの作業も動いていました。数字は 2 割から 5 割くらい揺れます。
- 値の大きさは、登録が 1.8 KB、ダイアログが 800 B、索引が 60 B です。

単位はマイクロ秒です。

| 操作 | メモリ p50 / p99 | Valkey、ループバック p50 / p99 | Valkey、ポートの公開 p50 / p99 | go-redis、ループバック p50 / p99 |
| --- | --- | --- | --- | --- |
| `Get` 1 回 | 0.8 / 12 | 43 / 254 | 112 / 272 | 48 / 254 |
| `Put` 1 回 | 1.2 / 20 | 57 / 194 | 123 / 432 | 69 / 426 |
| `CompareAndSwap` 1 回 | 1.5 / 10 | 82 / 286 | 129 / 408 | 72 / 443 |
| `Schedule`、`ClaimDue`、`Done` の 3 回 | 0.5 / 6 | 108 / 811 | 341 / 884 | 177 / 1324 |
| 登録 1 回（26 回の操作） | 16 / 215 | 1438 / 4233 | 3186 / 6795 | 1566 / 6636 |
| 呼 1 回（53 回の操作） | 19 / 70 | 2756 / 7616 | 6911 / 26033 | 2978 / 9268 |

AOF を毎回書き込む設定（`--appendonly yes --appendfsync always`）では、書く操作が遅くなりました。ループバックで、`Put` が 416 / 1826、登録が 6683 / 18140、呼が 8517 / 21880 です。`Get` は 46 / 239 で、変わりません。

この数字から言えること。

- 外部の保存先は、1 回の操作に、この機械のループバックで 40 から 80 マイクロ秒を足します。登録で約 1.4 ミリ秒、呼の確立から BYE までで約 2.8 ミリ秒です。SIP の T1（500 ミリ秒）と比べると小さい値です。
- 自作のクライアントと go-redis の差は、揺れの中に収まりました。
- 時間のほとんどは往復の回数で決まります。呼の 41 回の読み出しのうち、P-CSCF の `SecurityAssociations` が 10 回、`AssociationOfContact` が 7 回（索引を使うと 14 回）です。1 つの要求の処理の中で同じ項目を何度も読んでいます。P-CSCF の実装で、1 つの要求の中では 1 回だけ読む形にすると、往復は大きく減ります。
- AOF を毎回書く設定は、`Put` 1 回を約 7 倍、登録の全体を約 4.6 倍遅くしました。

この数字から言えないこと。

- 機械をまたいだ往復の時間は測っていません。保存先を別の機械に置くと、1 回の操作にネットワークの往復が足されます。往復が 0.2 ミリ秒なら、登録で約 5 ミリ秒、呼で約 11 ミリ秒になる計算です（推定）。
- 負荷をかけたときの時間は測っていません。操作は 1 つずつで、同時に動くのは 1 本だけです。
- Cx の往復や SIP の処理と比べた割合は、測っていません。4 章の「測るもの」の表の、pcap で REGISTER から 200 までを測る方法は、各役割の `Store` の実装ができてから行えます。
- ポートの公開の数字は、docker-proxy の処理の分だけ大きくなっています。Compose の中でノードと保存先が同じブリッジにいるときは、この転送を通りません。

### 11.5 復旧の様子

#### 保存先のコンテナを落として作り直す

保存先のコンテナに、登録 1000 件（1 件 1.8 KB）を書きました。そのあと、1 ミリ秒ごとに 1 件ずつ書き続け、応答を受けた最後の番号を記録しました。書いている途中で `docker kill`（SIGKILL）で落とし、`docker rm` で消しました。同じ名前と同じボリュームで `docker run` し直し、読めるまでの時間と、残ったものを数えました。時間は、`docker run` を始めてから、クライアントの読み出しが応答を受けるまでです。読み出しは 10 ミリ秒ごとに試しました。各設定で 2 回測りました。

| 設定 | 作り直してから応答まで | 残ったもの |
| --- | --- | --- |
| 永続化なし（`--save "" --appendonly no`） | 199、244 ミリ秒 | 何も残らない。登録 1000 件も消えた |
| RDB を 1 秒ごと（`--save "1 1"`） | 208、234 ミリ秒 | 登録 1000 件は残った。最後のスナップショットのあとの書き込み（633 件と 188 件）が消えた |
| AOF を 1 秒ごとに同期（`--appendonly yes --appendfsync everysec`） | 244、325 ミリ秒 | 応答を受けた書き込みは、すべて残った |
| AOF を毎回同期（`--appendfsync always`） | 257、261 ミリ秒 | すべて残った。2 回目は、応答を受ける前に落ちた 1 件も残っていた |

- 時間のほとんどは `docker run` そのもの（約 200 ミリ秒）です。保存先が起動して読めるようになるまでは、その後の数十ミリ秒でした。データは 3 MB くらいなので、AOF を読み込む時間は測れるほどではありません。データが大きいときは測っていません。
- AOF の 1 秒ごとの同期で何も失わなかったのは、落ちたのがプロセスだけで、OS は動いていたためです。書いた内容は OS の中に残り、ファイルに届きます。ホストそのものが落ちたときは、最後の 1 秒ぶんを失うことがあると、Valkey の文書（topics/persistence.md）は説明しています。これは試していません。
- 保存先が落ちているあいだ、ノードの操作は失敗します（接続が拒否される）。保存先に頼る作りでは、保存先の 1 台が単一障害点になります。

#### 保存先を使うノードを落として作り直す

保存先を使う「ノード」の役をする小さなプログラムを、試作用のモジュールに作りました。登録 60 件を書き、それぞれの期限を時刻の索引に入れます。期限の来たものを 1 件ずつ借り（リース 2 秒）、1 件に 50 ミリ秒かけて（SAR の往復の代わり）消します。

1 台目を 1 秒動かしたあと SIGKILL で落とし、すぐに 2 台目を起動しました。2 回測りました。

| 測ったもの | 結果 |
| --- | --- |
| 2 台目が状態を読めるまで | 起動してから 0 から 6 ミリ秒。最初の読み出しで、1 台目が書いた登録が読めた |
| 期限の処理 | 1 台目が 19 件、2 台目が残りの 41 件を処理した。2 回処理されたものは 0 件 |
| 1 台目が借りたまま落ちた 1 件 | リースが切れたあと（約 2 秒後）に、2 台目が借り直して処理した |

- 外部の保存先を使うと、ノードを作り直しても状態は失われません。失うのは、処理の途中だった期限の処理の時間だけで、リースの長さだけ遅れます。リースは、Cx の応答を待つ時間（5 秒）より長くする必要があるので（6 章）、そのぶん遅れます。
- `MemoryStore` を使うノードでは、作り直すと状態はすべて失われます。これは作りから明らかなので、測っていません。
- ノードのプロセスの中にあるもの（処理の途中のトランザクション、P-CSCF の `setups`、reg イベントの購読、ACK の待ち）は、この試作の対象外です。2 章と 6 章のとおり、それぞれ別に扱う必要があります。

#### 保存先そのものの冗長

保存先の 1 台は単一障害点です。試していませんが、候補は次のとおりです。

| 方法 | 内容 | 気になる点 |
| --- | --- | --- |
| レプリカと Sentinel | 1 台の主と、複製を受ける台を置く。Sentinel が主の停止を見つけ、複製の台を主にする | 複製は非同期なので、切り替えのときに最後の書き込みを失うことがある。クライアントが Sentinel に主を尋ねる必要がある |
| クラスタ | キーを区画に分けて複数の主に置き、それぞれに複製を付ける | クライアントが MOVED と ASK の応答を扱う必要がある。11.2 のとおり、1 つのスクリプトが触るキーは 1 つにしてある |
| 永続化と作り直し | 1 台のまま、AOF とボリュームで作り直す | 上の測定のとおり、作り直すあいだ（約 0.3 秒）は使えない |

学習用の Compose では、3 つめ（AOF とボリューム）から始めるのが小さいと考えます。Sentinel かクラスタにするなら、クライアントは go-redis にするほうが小さくなります（11.3）。

#### ほかの候補と比べて

4 章の候補と、この試作で分かったことを比べます。B と D は作っていません。

- A（UE の登録し直しに任せる）。外部の保存先の遅延はかかりません。かわりに、ノードが止まると、そのノードの利用者は TS 23.380 の手順で登録し直すまで着信できません。この試作では、外部の保存先を使ったノードは、作り直した直後から状態を読めました。登録し直しの往復（REGISTER、401、REGISTER、200 と、Cx の MAR と SAR）が要らないぶん、利用者から見た途切れは短くなります。
- B（HSS に預ける）。S-CSCF の登録だけが対象で、P-CSCF のダイアログ、IP-SM-GW の配達、時刻の索引には使えません。11.1 の一覧のうち、登録と購読の行の一部にしか当たりません。外部の保存先と並べて作る理由は、TS 23.380 の手順を見せるためです。
- D（ノードのあいだで複製する）。この試作の比べて入れ替える操作と、期限を 1 台だけが処理する仕組みを、ノードのあいだで作ることになります。どちらも、分断のときにどちらの台が正しいかを決める合意の仕組みが要ります。外部の保存先は、それを保存先の中の 1 つの手順（Lua のスクリプト）に任せています。D を作らない 4 章の判断は変わりません。

### 11.6 次の作業への提案

各役割の `Store` の実装を作るときの提案です。

- 各役割の `Store` を変えずに、`kvstore.Store` の上に各役割の実装を作れます。値は JSON で書きます。`internal/epc` と `internal/fivegc` の `MemoryStore` は、すでに JSON で持っているので、そのまま移せます。
- 期限のタイマーは、`time.AfterFunc` から、時刻の索引と、短い間隔で `ClaimDue` を呼ぶ処理に替えます。その処理は役割ごとに 1 つで足ります。`internal/smsc` の配達の処理と同じ形です。
- `ims.UserLocks` を保存先に置くときは、`CompareAndSwap(key, "", ...)` に期限を付けて取り、`CompareAndDelete` で放します。期限が切れたあとに古い台が書き込むのを防ぐには、ロックの版を主の項目の書き込みの条件にします（6 章の「番号を付けた書き込み」）。
- P-CSCF の、同じ要求の中で同じ項目を何度も読む作り（11.4）を先に直すと、外部の保存先の遅延が小さくなります。
- 試験では、`MemoryStore` に止めた時計を渡せます。適合試験（`internal/kvstore/conformance_test.go`）と同じように、`MemoryStore` と Valkey の両方で同じ試験を流せます。Valkey の試験は、環境変数 `KVSTORE_VALKEY_ADDR` があるときだけ動きます。CI で動かすなら、Valkey のサービスのコンテナを足す必要があります。

### 11.7 結論

採用した結論です（[状態と冗長構成の設計](../design/state-redundancy.md)）。

1. 外部の保存先のクライアントは、標準ライブラリだけの自作（`internal/kvstore/resp.go`）にします。依存は増えません。保存先の冗長を Sentinel かクラスタで組むなら、go-redis（v9.23.0、BSD 2-Clause、ほかに xxhash v2.3.0 と go.uber.org/atomic v1.12.0、どちらも MIT）に替えるかを、そのときに決めます。go-redis で作る場合の差分は、`Valkey` の型を `*redis.Client` を持つ型に替え、4 つのスクリプトを `redis.NewScript` にし、各操作を `HMGet`、`Del`、`ZAdd`、`ZRem` と `Script.Run` で書くだけです。適合試験はそのまま通りました。
2. Compose の保存先は、AOF を 1 秒ごとに同期し、名前付きのボリュームに置きます。ノードと保存先のプロセスが落ちる場面では失うものがなく、書く操作も遅くなりません。
3. 保存先の冗長（Sentinel、クラスタ）は作りません。1 台のままで、AOF とボリュームで作り直します。
4. CI で Valkey の適合試験を動かします。そのために、`.github/workflows/ci.yml` にサービスのコンテナを足します。

### 11.8 各役割の `Store` を `kvstore` に載せる

11.6 の提案のうち、各役割の `Store` の実装を作った記録です。各役割のパッケージは import するだけにし、変えていません。

#### 置き場所

| パッケージ | 中身 |
| --- | --- |
| `internal/kvstore/kvjson` | どの役割にも要る部品。値を JSON で読み書きする、操作ごとの時間の上限、比べて入れ替える更新（`Update`、`Upsert`）、1 台だけが取る `Take`、索引の項目（`PutIndex`、`IndexTarget`、`DeleteIndexIf`、`SwapIndex`）、時刻の索引 |
| `internal/kvstore/scscfstore` | `scscf.Store` |
| `internal/kvstore/pcscfstore` | `pcscf.Store` |
| `internal/kvstore/ipsmgwstore` | `ipsmgw.Store` |
| `internal/kvstore/scstore` | `sc.Store` |
| `internal/kvstore/mmtelstore` | `mmtel.Store` |
| `internal/kvstore/hssstore` | `hss.Store` |
| `internal/kvstore/agwstore` | `agw.ContextStore`（下の「メディア中継ノードのコンテキスト」） |
| `internal/kvstore/storetest` | 共通の試験の組。各役割の `MemoryStore` と、各役割の実装の両方にかける |

EPC と PCRF のスタブは保存先に載せないと決めたので、`internal/kvstore/epcstore` は作りません（[状態と冗長構成の設計](../design/state-redundancy.md)）。`storetest` の `EPC` の組は、`epc.MemoryStore` にかける試験として残しています。

どの実装も、`New(kv, cfg)` で作ります。`cfg.Prefix` が同じで同じ `kvstore.Store` を使う実装どうしは、状態を共有します。`scscfstore` と `hssstore` は、ID を `ims.CanonicalURI` で比べるので、`internal/ims` も import しています。

#### キーと索引

キーは「接頭辞、役割、項目の種類、項目の名前」を `:` でつないだものです。名前の中の `:` と `%` は `%3A` と `%25` に書き換えます。Call-ID に `:` が入りうるためです（RFC 3261 25.1 の word）。`valkey-cli` で見ると、`scscf:impu:tel%3A+12025550101` のような形になります。

| 役割 | 主の項目 | 索引（値は主の項目の名前） | 今の `MemoryStore` の走査から替えたもの |
| --- | --- | --- | --- |
| S-CSCF | `challenge:<IMPI>`、`registration:<IMPI>`、`subscription:<ダイアログ>`、`original-dialog:<ID>`、`unregistered:<IMPI>` | `impu:<IMPU>`、`subscriptions-of:<IMPI>`（購読のダイアログの集合を 1 つの項目に入れる）、`unregistered-impu:<IMPU>` | `RegistrationOf`、`SubscriptionsOf` |
| P-CSCF | `association:<トランスポート>:<アドレス>`、`dialog:<Call-ID>:<UE の tag>`、`sa:<アドレス>` | `association-of-contact:<user>:<host>:<port>`、`association-of-session:<ポリシーのセッション>`、`dialog-of-session:<ポリシーのセッション>` | `AssociationOfContact` |
| IP-SM-GW | `message-reference:<宛先>`、`delivery:<Call-ID>`、`reg-subscription:<Call-ID>` | `reg-subscription-of:<公開 ID>` | なし（もとから索引） |
| SC のスタブ | `status-report-request:<参照>` | なし | なし |
| MMTel AS | `call:<Incoming のダイアログ>` | `call-of-leg:<Outgoing のダイアログ>` | なし。`MemoryStore` は同じ記録を 2 つのキーに書く。ここでは 1 つにし、もう一方を索引にした |
| EPC | `pdn-connection:<UE のアドレス>`、`binding:<Session-Id>` | なし | なし |
| HSS | `subscriber:<IMPI>`（加入者とリポジトリのデータ） | `public:<IMPU>`、`directory:<0 から 15>`（`List` のため） | `List` は走査のまま（下の「載らなかったところ」） |

ID の比べ方は、各役割の `MemoryStore` に合わせました。IMPI は小文字にします。S-CSCF の IMPU は `ims.CanonicalURI` の形の小文字です。HSS の IMPU は `hss` の `publicKey` と同じ形にしました（`publicKey` は公開されていないので、同じ処理を写しています）。P-CSCF の Contact は、`MemoryStore` が比べる user、host（小文字）、port の 3 つです。

#### 書く順番と、途中で止まったとき

1 つの操作は 1 つのキーだけを触るので（11.2）、複数のキーを変える操作は、順番で整合を保ちます。

- 索引の項目は、主の項目より先に書き、主の項目より後に消します。
- 索引から引いたときは、主の項目を読み、その ID を本当に持っているかを確かめます。持っていなければ、見つからなかったことにします。書いている途中の索引か、止まった台が残した索引です。
- 読むときに索引を消すことはしません。別の台が、ちょうど主の項目を書こうとしているかもしれないためです。残った索引は、期限（TTL）で消えるか、次に同じ ID を書いた台が上書きします。S-CSCF の IMPU の索引は、登録の期限に 10 分を足した時刻に消えます。
- S-CSCF の購読の集合は、同じ項目を複数の台が変えるので、比べて入れ替える操作で足し引きします。期限から 10 分以上過ぎた購読は、書くときに集合から落とします。主の項目が無いことを理由には落としません（上と同じ理由）。
- HSS の `Add` は、IMPI を仮の記録（TTL 1 分）で押さえ、IMPU の索引を 1 つずつ「無いときだけ書く」で押さえ、最後に記録を本物にします。同じ IMPU を 2 台が同時に足すと、片方だけが成功します。途中で止まった `Add` の仮の記録は TTL で消え、残った索引は次の `Add` が引き継ぎます。
- 各役割の `Store` と同じく、同じ項目を 2 台が `Put` したときは、あとに書いた台の値が残ります。`Update` の付く操作（`UpdateDialog`、`UpdatePDNConnection` など）は比べて入れ替える操作なので、変更を失いません。そのかわり、`change` の関数が 2 回以上呼ばれることがあります。今の各役割の `change` は、どれも引数を変えるだけなので問題ありません。

試験では、決めた回数だけ書いたあとすべての操作が失敗する包み（`storetest.StopAfter`）で、書く途中で台が止まった状態を作り、見つからないだけで済むことを確かめています。

#### 期限

| 役割 | 項目 | 消え方 |
| --- | --- | --- |
| S-CSCF | Challenge | TTL（reg-await-auth の 2 倍）と、時刻の索引 `reg-await-auth` |
| | Registration | TTL なし。時刻の索引 `registration-expiry` |
| | Subscription | TTL なし。時刻の索引 `subscription-expiry` |
| | OriginalDialog | TTL（`Expires` まで） |
| | UnregisteredProfile | TTL なし。時刻の索引 `unregistered-profile-expiry` |
| P-CSCF | Association | TTL（`Expires` に 64×T1 を足した時刻まで） |
| | Dialog | なし（BYE まで） |
| | SecurityAssociations | TTL（`Expires` まで） |
| IP-SM-GW | RP-MR の数 | TTL（10 分） |
| | PendingDelivery | TTL（`Expires` まで）と、時刻の索引 `report-timeout`（TR1N） |
| | RegSubscription | TTL（`Expires` まで） |
| SC | StatusReportRequest | TTL（`Expires` まで） |
| MMTel、EPC、HSS | すべて | なし |

期限切れに動作が要る項目は、TTL を付けません。消えてしまうと、SAR や NOTIFY を送る台が中身を読めないためです。そのかわり、`Put` のときに時刻の索引へ入れます。実装は `ClaimDue` と `Done` を持ち、時刻の来た項目を 1 台だけに渡します。今の各役割のタイマー（`time.AfterFunc`）はそのまま動くので、ループに替えるのは役割の側の変更です（下の「役割の側に要る変更」）。使い方は `scscfstore` の `Example` にあります。

- reg-await-auth の名前は IMPI と nonce の組です。答えのあった challenge の時刻が、あとの challenge を終わらせないためです。`DeleteChallenge` はその名前を外します。
- 登録と未登録のプロファイルの名前は IMPI です。消すときに時刻の索引から外しません。外すと、同じ時に別の台が書いた新しい登録の時刻まで消してしまうためです。借りた台は主の項目を読み、`Expires` を確かめます。今の `registrationExpired` と同じ確かめ方です。
- 購読の名前はダイアログです。ダイアログは使い回さないので、`DeleteSubscription` で外します。
- TR1N の名前は Call-ID です。今は MESSAGE を送った台だけがタイマーを持ち、その台が止まると、SC は結果を聞けません（`deliver.go`）。時刻の索引を使えば、ほかの台が `TakeDelivery` で取り、失敗を知らせられます。`TakeDelivery` は 1 台だけが成功するので、SC が結果を 2 回聞くことはありません。

#### 載らなかったところ

- 各役割の `Store` は、エラーも `context` も返しません。プロセスの中の保存先は失敗しないためです。保存先が失敗したとき、実装はログに書き、項目が無かったことにします。書き込みの失敗は、呼んだ側に伝わりません。これが各役割の `MemoryStore` との一番大きな違いです。
- HSS の `List` は、16 個の `directory` の項目をすべて読み、全加入者の IMPI を並べ替えます。`kvstore` に範囲の検索が無く（11.2）、`List` に続きの位置を渡す引数が無いためです。`SIMPLIFIED:` を付けました。保守の操作だけで、Cx の処理では使いません。
- S-CSCF の未登録のプロファイルの期限を借りた台は、IMPI からプロファイルを読む必要があります。`scscf.Store` にはこの検索が無いので、実装に `UnregisteredProfile(privateID)` を足しました。
- P-CSCF のダイアログと MMTel AS の呼の記録には、期限がありません。BYE を誰も見なかった呼の項目は残ります。各役割の `MemoryStore` と同じです。
- P-CSCF の IP の対応付けは、`Expires` に 64×T1 を足した時刻に TTL で消えます。各役割の `MemoryStore` は、`DeleteAssociation` まで残します。S-CSCF が NOTIFY を送る前に止まった場合だけ違いが出ます。
- `ims.UserLocks` を保存先に置く作業（11.6 の 3 つめ）は、今回の範囲に入れていません。同じ利用者を 2 台の S-CSCF が同時に処理すると、登録の書き込みは、あとに書いた台の値が残ります。このロックは、あとで 11.9 で作りました。

#### 共通の試験

`storetest` に、役割ごとの試験の組（`SCSCF`、`PCSCF`、`IPSMGW`、`SC`、`MMTel`、`EPC`、`HSS`）を置きました。どの組も、状態を共有する 2 つの `Store` を受け取ります。各役割の `MemoryStore` では同じ値を 2 回渡し、実装では同じ `kvstore` の上に 2 つ作ります。同じ組が、各役割の `MemoryStore`、`kvstore.MemoryStore` の上の実装、Valkey の上の実装（`KVSTORE_VALKEY_ADDR` があるとき）の 3 つで通ります。

各役割の `Store` の試験は、HSS のもの（`store_test.go` と `aka_test.go` の `TestMemoryStoreKeepsSQNOutOfPut`）だけが `Store` を直接試していました。`_test.go` は import できないので、組に写しました。ほかの役割は、役割を通した試験しか無いので、`Store` の文書から書きました。複数の台の場面として、次のことを確かめています。

- 2 台が同じ利用者の項目を同時に変えて、変更を失わない。P-CSCF のダイアログの CSeq、EPC の AF のセッション、S-CSCF の購読の集合、HSS の SQN、リポジトリのデータ、IP-SM-GW の RP-MR。
- 1 つの項目を複数の台が同時に取って、1 台だけが成功する。`TakeDialog`、`TakeDelivery`、`TakeStatusReportRequest`、`TakePDNConnection`、`DeleteUnregisteredProfile`、同じ IMPU を持つ 2 つの `Add`。
- 期限を 1 台だけが処理する。登録の期限と TR1N を、2 台が同時に借りる（実装の試験）。

#### 測った結果

11.4 と同じ機械と同じ測り方で、11.1 の登録 1 回と呼 1 回の `Store` の呼び出しを、実装を通して順に行いました（`internal/kvstore/storetest/bench_test.go`）。SMS 1 通（`ipsmgw.Deliver` と配達の報告、SC の状態報告の要求）も足しました。Valkey は 9.0.8 で、永続化なし、ループバックです。5000 回を 3 回測り、その中央値を書きます。単位はマイクロ秒です。

| 流れ | `kvstore` の操作の数（うち P-CSCF） | 実装と `kvstore.MemoryStore` p50 / p99 | 実装と Valkey p50 / p99 |
| --- | --- | --- | --- |
| 登録 1 回 | 29（7） | 99 / 705 | 1721 / 4903 |
| 呼 1 回 | 55（49） | 183 / 990 | 3251 / 8533 |
| SMS 1 通 | 10（0） | 17 / 70 | 567 / 1971 |

操作の内訳は次のとおりです。

- 登録は、読み出し 16、書き込み 7、消去 1、比べて入れ替える操作 1、時刻の索引 4 です。11.1 の見積もり（26）より 3 回多くなりました。主に、`PutRegistration` と `PutAssociation` が、外す索引を知るために古い項目を読むことと、`DeleteChallenge` が時刻の索引から外す名前を知るために challenge を読むことの分です。
- 呼は、読み出し 43、書き込み 2、比べて入れ替える操作 8、比べて消す操作 2 です。11.1 の見積もり（53）より 2 回多い分は、`PutDialog` が古いダイアログのポリシーのセッションを片付けるために読む分です。`PutDialog` は新しいダイアログにしか使われないので、`Store` の文書がそう約束すれば、この読み出しは省けます。

各役割の `MemoryStore` に同じ組み合わせを流すと、時間が利用者の数に比例して伸びました。登録した利用者が 500 人のとき登録 183、呼 449、2000 人のとき 561 と 2336、5000 人のとき 1636 と 6205 です（p50）。`RegistrationOf` と `AssociationOfContact` が全件を走査するためです。実装では、利用者が 500 人でも 5000 人でも、登録 96 前後、呼 185 から 192 で、ほぼ変わりませんでした。

この数字から言えること。

- Valkey を使うと、1 回の操作に約 59 マイクロ秒かかりました（登録 1721 を 29 で割った値。呼も同じ）。11.4 の `Get` 1 回（43）より大きいのは、JSON の符号化と復号の分です。`kvstore.MemoryStore` の上で 1 回あたり約 3.4 マイクロ秒なので、ほとんどは往復の時間です。
- 呼の 55 回のうち 49 回が P-CSCF です。そのうち 24 回は、同じ 2 つの項目の読み直しです。`AssociationOfContact` を 7 回（索引と主の項目で 14 回）、`SecurityAssociations` を 10 回呼んでいます。1 つの要求の処理の中で 1 回だけ読む形にすれば、呼の往復は約 4 割減る見込みです（計算。測っていません）。役割のコードの変更が要ります。

言えないことは 11.4 と同じです。機械をまたいだ往復と、負荷をかけたときは測っていません。

#### 役割の側に要る変更

各役割のパッケージの変更が要ります。

1. 各役割の `Store` の操作に、`context.Context` を取り、`error` を返す形にすること。今は保存先の失敗が呼んだ側に伝わりません。
2. 各役割のタイマー（S-CSCF の 4 つ、IP-SM-GW の TR1N）を、`time.AfterFunc` から、実装の `ClaimDue` を短い間隔で呼ぶループに替えること。ループは役割ごとに 1 つで足ります。今の時刻の関数（`registrationExpired` など）は、項目を読んで確かめる作りなので、そのまま呼べます。
3. `scscf.Store` に、IMPI で未登録のプロファイルを読む操作を足すこと。期限を借りた台がプロファイルを読むためです。
4. `hss.Store.List` を、続きの位置を受け取り、次の位置を返す形にすること（たとえば `List(after string, limit int) ([]Subscriber, string)`）。並び順を IMPI の順でなく「区画ごとの順」にしてよければ、1 ページで `directory` の項目を 1 つ読むだけになります。
5. `hss` の `publicKey` を公開すること。`hssstore` は同じ処理を写しています。
6. `scscfstore` と `hssstore` が `internal/ims` を import してよいこと。`ims.CanonicalURI` で ID を比べるためです。
7. P-CSCF が 1 つの要求の中で同じ項目を何度も読む作りを、1 回だけ読む形にすること（上の測定）。
8. 役割の組み立て（`cmd/ims`）で、設定により `MemoryStore` と実装を切り替えること。HSS の実装は再起動のあとも加入者を持つので、設定の加入者を `Add` で足し、`ErrIdentityTaken` は無視する形になります。

### 11.9 利用者ごとのロック（`internal/kvstore/kvlock`）

利用者ごとのロックを保存先に置く実装の記録です。ロックのインタフェース `ims.UserLocker` が `internal/ims` に入る前に作ったので、想定した形（`Lock(ctx, user) (unlock func(), err error)`）のメソッドを作り、試験でその形を確かめています。

#### 作り

- ロックは 1 つの項目で、キーは `<store.prefix><役割>:lock:<利用者>` です。値は持っている台の名前です。保存先を読むと、どの台がその利用者を処理しているかが分かります。
- 取るときは、15 秒の期限を付けて「無いときだけ書く」（`CompareAndSwap(key, "", ...)`）を使います。書いたときの版が、ロックを持っている証しになります。
- 放すときは、その版のときだけ消します（`CompareAndDelete`）。期限が切れたあとで遅れて放した台は、次に取った台のロックを消しません。このときは、ログに警告を書きます。
- 取れなければ、間隔を空けて試し直します。間隔は 5 ミリ秒から始めて倍にしていき、100 ミリ秒で止めます。毎回、その間隔の後ろ半分から乱数で選びます。いくつかの台の待ち手が、同じ時に試し直さないようにするためです。`ctx` が終わったら、誤りを返します。
- 同じ台の中の待ち手は、プロセスの中で順番を待ちます。保存先を試すのは、1 つの台につき 1 つの待ち手だけです。この順番も利用者ごとで、持つ goroutine と待つ goroutine がいなくなったら消します。役割全体を止めるロックはありません（規模を広げられなくなるため）。
- 保存先が失敗したときは、すぐに誤りを返します。ロックが取れたことにはしません。呼んだ役割は、SIP なら 500、Diameter なら DIAMETER_UNABLE_TO_COMPLY を返します（[状態と冗長構成の設計](../design/state-redundancy.md)）。
- 利用者の名前は、小文字にしてから比べます。今の `ims.UserLocks` と同じです。

#### 期限を延ばさないこと

持っているあいだに期限を延ばし続ける処理は、作りませんでした。設計で決めたとおり、ロックの中でいちばん長い処理は Cx の往復 2 回（それぞれ応答を 5 秒まで待つ）で、15 秒に収まるためです。延ばす処理を作ると、ロックごとに goroutine が要ります。止まりかけた台のロックが延び続けないように、延ばす処理そのものにも期限が要ります。

期限を過ぎてから放した台は、項目が無いか、別の版になっていることに気づき、ログに警告を書きます。番号を付けた書き込みが無いので、そのあいだに次の台が書いたものを上書きすることは防げません。設計で決めたとおり `SIMPLIFIED:` に書きました。ロックの中で SIP のトランザクションの終わり（最大 32 秒）を待つ処理を足すと、期限を超えます。

#### 測った結果

11.4 と同じ測り方です。Go のベンチマーク（`internal/kvstore/kvlock/bench_test.go`）で、5000 回をくり返す測定を 3 回行い、その中央値を書きます。Valkey は 9.0.8 で、永続化は既定の設定（AOF なし）です。ループバックとポートの公開の意味は 11.4 と同じです。単位はマイクロ秒です。

| 測ったもの | メモリ | Valkey、ループバック | Valkey、ポートの公開 |
| --- | --- | --- | --- |
| 取って放す 1 回、待ち手なし（p50 / p99） | 2.1 / 14 | 98 / 346 | 222 / 436 |
| 2 台の goroutine が同じ利用者を交互に取る、1 回あたり | 5.6 | 134 | 272 |
| 比べるための、同じ時の `kvstore` の `Get` 1 回（p50） | | 50 | 85 |
| 同じく `CompareAndSwap` 1 回（p50） | | 51 | 108 |

この数字から言えること。

- 取って放す 1 回は、保存先の往復 2 回（`CompareAndSwap` と `CompareAndDelete`）の時間です。11.4 と同じく、ほとんどが往復の時間です。
- 今のコードでは、最初の登録 1 回で、少なくとも 5 回ロックを取ります。S-CSCF が REGISTER ごとに 1 回（401 を返すものと 200 を返すもので 2 回）と、P-CSCF からの reg イベントの SUBSCRIBE で 1 回です。HSS が MAR と SAR で 1 回ずつです。UE と AS の reg イベントの SUBSCRIBE と、IP-SM-GW が第三者登録を受けたときの処理でも、それぞれ 1 回ずつ足されます。5 回なら、往復は 10 回増えます。11.8 の 1 回あたり約 59 マイクロ秒で数えると、約 0.6 ミリ秒です（計算。役割に組み込んで測ってはいません）。
- 登録済みの利用者への呼の処理（INVITE から BYE まで）では、S-CSCF も HSS もロックを取りません（`internal/scscf` と `internal/hss`）。そのため、呼の時間は変わりません。ロックを取るのは、未登録の利用者への着信（`unregistered.go`）と、第三者登録が失敗したときの登録の解除（`thirdparty.go`）です。
- 同じ利用者を 2 台が取り合っても、Valkey では、1 回あたりの時間は待ち手なしの p50 の 1.2 倍から 1.4 倍ほどでした。同じ台の中の待ち手は保存先を試さないためです。ほかの台の待ち手は、放されてから最大 100 ミリ秒遅れて取ることがあります。

言えないことは 11.4 と同じです。機械をまたいだ往復と、多くの利用者の負荷をかけたときは測っていません。`Get` 1 回の p50 は、11.4 のときと比べて、ポートの公開で 112 から 85 に、ループバックで 43 から 50 になりました。ほかの作業が同時に動いていたので、2 割から 5 割の揺れは見込んでください。
#### メディア中継ノードのコンテキスト

`agwstore` は、中継ノードが同じ名前とアドレスで作り直されたとき、同じポートを開き直すために使います。この時点では、中継ノードへの組み込みはしていません。

- キーは `<接頭辞>agw:<台の名前>:context:<ID>` と、索引の `<接頭辞>agw:<台の名前>:contexts` です。台の名前は `agwstore.Config.Relay` に書きます。ほかの役割と違い、台ごとにキーを分けます。コンテキストは、それを作った台だけが読み書きするためです。
- 索引は 1 つの項目に ID の一覧を入れたもので、比べて入れ替える操作で足し引きします。同じ台の別々のコンテキストを同時に変えても、一覧の変更を失いません。一覧が空になったら索引の項目を消します。
- `PutContext` は索引を先に書き、`DeleteContext` はコンテキストを先に消します。途中で止まった台が残すのは、コンテキストの無い索引の行だけです。索引から引けないコンテキストは残りません。
- 索引の各行には、最後に `PutContext` を始めた時刻を入れます。`Contexts` は、コンテキストの無い行を飛ばします。その行が 1 分より古ければ、索引から消します。新しい行は、書いている途中かもしれないので消しません。1 分より長く止まってから書き込みを再開した台は、コンテキストを失うことがあります。保存先のロックと同じ種類の弱さです（[状態と冗長構成の設計](../design/state-redundancy.md)）。
- ほかの実装と違い、保存先の失敗を呼んだ側に返します。`agw.ContextStore` がエラーを返す形だからです。読めなかったのに「コンテキストが無い」と答えると、作り直した台が、続くはずの呼の音声を黙って切ってしまいます。
- 期限は付けません（[状態と冗長構成の設計](../design/state-redundancy.md)）。
- 試験は `storetest.AGW` です。各役割の `agw.MemoryContextStore`、`kvstore.MemoryStore` の上の実装、Valkey の上の実装で通ります。2 台が同じ保存先で自分のコンテキストだけを見ること、作り直した台が保存したとおりに読めること、同時の書き込み、途中で止まった台（`storetest.StopAfter`）を確かめます。途中で止まる試験は書く順番に頼るので、`agwstore` の中に置きました。

#### MRFC と MMTel AS の会議

`mrfcstore`（`mrfc.Store`）と、`mmtelstore` の `mmtel.ConferenceStore` も作りました。この時点では、役割への組み込みはしていません。

MRFC の会議は、次のとおりです。

- キーは `<接頭辞>mrfc:conference:<会議の ID>` の 1 つだけです。値は `ConferenceMedia`（MRFP の名前とコンテキストの ID）です。索引も期限もありません（[状態と冗長構成の設計](../design/state-redundancy.md)）。
- `CreateConference` は「無いときだけ書く」（版が空の `CompareAndSwap`）です。同時に作った台のうち 1 台だけが書き、ほかの台は書かれた値を読んで、そのコンテキストに入ります。読む前に会議が消えていたら、最初からやり直します（10 回まで）。
- `RemoveConference` は、読んだ値が渡された値と同じときだけ、読んだ版を条件に消します。最後の参加者が去った台は、そのあとでほかの台が作り直した会議を消しません。
- 保存先が失敗したときの `CreateConference` は、「作らなかった」と答え、値を返しません。MRFC は自分で作ったコンテキストを手放して、やり直します。「作った」と答えると、ほかの台が別のコンテキストに同じ会議を作り、参加者どうしが聞こえなくなるためです。
- 保存先に置かないものは、[状態と冗長構成の設計](../design/state-redundancy.md)のとおりです。音声ガイダンスと会議のダイアログ、そのタイマー、MRFP が再生の終わりを知らせる Notify の待ちは、INVITE を受けた台のプロセスに残ります。Contact がその台を指すので、ほかの台には使い道がありません。
- 参加者は保存先にいません。MRFP のコンテキストのターミネーションです。そのため、2 台から参加と退出が同時に起きても、保存先で失う変更はありません。保存先で競うのは、会議を作ることと消すことだけです。

MMTel AS の会議は、次のとおりです。

- キーは `<接頭辞>mmtel:conference:<会議の ID>` で、値は `ConferenceRecord` の全体です。索引も期限もありません（[状態と冗長構成の設計](../design/state-redundancy.md)）。呼の記録と同じ `mmtelstore.Store` が `mmtel.ConferenceStore` も満たすので、`mmtel.New` の 2 つの引数に同じ値を渡します。今の `cmd/ims` が `mmtel.MemoryStore` を渡すのと同じ形です。
- 記録を書くのは、フォーカスのある台だけです。会議の URI がその台を指し、その台が会議のロックの中で 1 つずつ書きます。ほかの台は書かず、会議を引き継ぎません（[状態と冗長構成の設計](../design/state-redundancy.md)）。そのため、`PutConference` が記録の全体を置き換えても、ほかの台の変更は失われません。
- それでも `PutConference` は、読んでから版を条件に書きます（`kvjson.Upsert`）。答えが返らずに諦めた書き込みが、次の書き込みのあとで保存先に届くことがあるためです。版を条件にすれば、遅れた書き込みは失敗し、記録はあとの状態（参加者と、購読ごとの会議の文書の版）を保ちます。普通の `Put` だと、古い状態で上書きします。試験（`TestALateWriteDoesNotUndoALaterOne`）は、最初の書き込みを止めておいて遅れて届けます。普通の `Put` に替えると失敗することを確かめました。
- 例外は、会議の最初の書き込みが遅れて、`DeleteConference` のあとに届いた場合です。版が空の条件は、項目が無ければ通るので、記録が 1 つ残ります。会議の ID は乱数で使い回さないので、動いている会議と取り違えることはありません。
- 会議の購読の期限には、時刻の索引を使いませんでした。期限が切れたときに動けるのはフォーカスのある台だけで、その台が止まれば会議も終わるので、別の台に渡す意味が無いためです。

途中で止まったとき。

- どの操作も 1 つのキーに 1 回だけ書くので、半分だけ書いた状態は残りません。止まった台の書き込みは、保存先に入ったか、入らなかったかのどちらかです。`StopAfter` を使った試験で、作る前に止まった MRFC の台は何も残さないこと、消す前に止まった台の項目は、ほかの台が同じ値で消せることを確かめました。MMTel AS では、失敗した書き込みのあとも、前の記録がそのまま読めます。
- MRFC の台が止まると、その台の参加者のターミネーションは MRFP のコンテキストに残ります。ほかの台の参加者がすべて去っても、コンテキストは空にならず、会議の項目も残ります。MRFC のダイアログを組み立て直さない作り（[状態と冗長構成の設計](../design/state-redundancy.md)）の結果で、保存先の実装では直せません。
- MMTel AS のフォーカスの台が止まると、会議の記録は残ります。会議を終わらせる手順（TS 24.147 V19.0.0 の 5.3.2.7。参加者を外し、会議の URI を手放す）を行う台がいないためです。呼の記録と同じく、`SIMPLIFIED:` に書きました。

試験は `storetest.MRFC` と `storetest.MMTelConferences` です。各役割の `MemoryStore`、`kvstore.MemoryStore` の上の実装、Valkey の上の実装の 3 つで通ります。複数の台の場面として、2 台が同じ会議を同時に作って 1 台だけが作ること、2 台がくり返し作って消しても、作った台が消すまで自分の値を読めること、フォーカスの台が書き続けるあいだ、ほかの台が読む版が戻らないことを確かめます。各役割の `MemoryStore` にかける試験は、`agwstore` と同じく、それぞれの実装のパッケージに置きました。

気づいたこと。会議の購読は、期限が切れても記録に残り、次の変更でも NOTIFY が送られます。`Subscription-State` は active のままで、expires は 0 です。RFC 6665 の期限切れの扱いに合うかは、確かめていません。直すなら、フォーカスのある台のプロセスの中の処理で足ります。

役割の側に要る変更。

1. `mrfc.Store` と `mmtel.ConferenceStore` に `context.Context` と `error` を足すときは、`mrfcstore` の `CreateConference` を、保存先の失敗を「作らなかった」ではなく誤りとして返す形に直します。MRFC は 503 を返せます。
2. どちらのインタフェースにも、全部の会議を回る操作はありません。足す必要もありません。
3. ほかの台が会議を引き継ぐ形にするときは、`mmtel.ConferenceStore` に、読んで変えて書く操作（たとえば `UpdateConference(ctx, id, change func(*ConferenceRecord)) error`）が要ります。全体を置き換える `PutConference` では、2 台の変更がぶつかったときに片方を失います。会議を引き継がない設計なので、今は要りません。
4. `mmtel.ConferenceStore.Conference` を呼ぶ所は、この時点ではありません。ドレインの `Users` やダッシュボードで使うときも、この実装でそのまま読めます。

## 12. 継続性の測定の準備

冗長構成の測定の準備の記録です。測定そのものは、冗長構成（`deploy/redundancy/`）ができてから行います。冗長構成が無くてもできる準備として、測定の道具と手順を作りました。測るものは、保存先を外に置いたときの登録と呼に足される時間、ノードを作り直したあとで戻るまでの時間、失うものです。

この章の測り方は、4 章の「測るもの」の表を、今の道具で行える形にしたものです。11.4 と 11.8 は `Store` の呼び出しだけを測りました。ここでは、それに加えて、網を通した登録と呼の時間を pcap から測ります。

### 12.1 測るものと道具

| 測るもの | 道具 | 比べるもの |
| --- | --- | --- |
| `Store` の操作に足される時間 | 11.4 と 11.8 の Go のベンチマーク。`go test ./internal/kvstore -run '^$' -bench . -benchtime 2000x` と、同じ形の `./internal/kvstore/storetest`。`KVSTORE_VALKEY_ADDR` があれば Valkey でも流れる | `kvstore.MemoryStore` と Valkey。保存先を別の機械に置くなら、その機械の Valkey |
| 網を通した登録と呼の時間 | `deploy/chaos/measure/latency.sh`。`ue serve` の 2 台の UE（`ue-a`、`ue-b`）を制御の API で動かし、キャプチャから時間を取る | 状態をメモリに置く構成（`compose.yaml`）と、保存先に置く構成（`compose.yaml` に `deploy/redundancy/redundancy.yaml` を重ねたもの） |
| ノードを作り直したあとで戻るまでの時間と、失うもの | `deploy/chaos/measure/recovery.sh SERVICE`。呼のあいだに `fault.sh recreate` でノードを作り直し、ダイアログの中の要求、BYE、UE への新しい呼が通るかと、その時間を記録する | 上と同じ 2 つの構成。保存先は、AOF を 1 秒ごとに同期する設定と、永続化を切った設定（`deploy/chaos/measure/store-volatile.yaml`） |
| 保存先が届かないときの振る舞い | `fault.sh partition valkey 'scscf-*'` などで保存先を 1 つの役割から切り、`latency.sh` か UE の登録を流す | 切っているあいだと、戻したあと |

`latency.sh` の時間は、UE とネットワークのあいだのメッセージで測ります。UE 自身の処理（呼び出し音の時間、応答）が入らないようにするためです。キャプチャはブリッジ全体を 1 つのコンテナで取るもので、UE と最初のノードのあいだのメッセージは必ずブリッジを通ります。

| 名前 | 始まり | 終わり |
| --- | --- | --- |
| register | UE が送る最初の REGISTER（401 が返るもの） | UE に届く 200 |
| register-2 | 認証の情報を付けた REGISTER | UE に届く 200 |
| invite | `ue-a` が送る INVITE | `ue-b` に届く INVITE |
| answer | `ue-b` が送る INVITE への 200 | `ue-a` に届く 200 |
| bye | `ue-a` が送る BYE | `ue-a` に届く BYE への 200 |

`recovery.sh` の手順は次のとおりです。

1. `ue-a` と `ue-b` を登録し直し、`ue-a` が `ue-b` を呼び出して、呼が応答されるのを待ちます。前の測定で作り直したノードが登録を忘れていても、同じ状態から始めるためです。
2. 応答から `SETTLE` 秒（既定 0.5 秒）のあとで、`fault.sh recreate SERVICE` を実行します。コンテナを kill し、消し、同じ名前とアドレスで作り直します。
3. `ue-a` が保留の re-INVITE を、通るまで 1 秒ごとに送ります（`HOLD_FOR` 秒まで、既定 40 秒）。
4. `ue-a` が BYE を送り、`ue-b` に届いたかを見ます。
5. `ue-b` が `ue-a` に 2 秒ごとに発信し、応答されるまでの時間を測ります（`CALL_FOR` 秒まで、既定 60 秒）。`ue-a` は障害の前に登録したままで、登録し直していません。
6. 作り直しから終わりまでのキャプチャで、UE の REGISTER の数、400 以上の応答、`ue-b` への RTP の最長の途切れを数えます。

### 12.2 手順

測る機械、Docker と Compose の版、カーネル、回数を、結果と一緒に書きます。どの手順も、リポジトリのルートで行います。

1. メモリの構成を起動します。`docker compose -f compose.yaml -f deploy/chaos/chaos.yaml build fault-tools` で道具のイメージを作り、`docker compose up -d` で起動します。
2. `COMPOSE_FILE=compose.yaml:deploy/chaos/chaos.yaml LABEL=memory deploy/chaos/measure/latency.sh 50` を実行し、表 12.3.1 の `memory` の行に写します。
3. `LABEL=memory deploy/chaos/measure/recovery.sh SERVICE` を、`pcscf`、`scscf`、`mmtel`、`agw`、`icscf`、`hss` のそれぞれで実行し、表 12.3.2 に写します。
4. 構成を `docker compose down -v` で消し、冗長構成を起動します。`COMPOSE_FILE=compose.yaml:deploy/redundancy/redundancy.yaml:deploy/chaos/chaos.yaml` にします。
5. `LABEL=valkey latency.sh 50` を実行し、表 12.3.1 の `valkey` の行に写します。`memory` の行との差が、保存先が足す時間です。
6. `LABEL=valkey recovery.sh SERVICE` を、`pcscf-1`、`scscf-1`、`mmtel-1`、`agw-1`、`hss-1`、`valkey` で実行し、表 12.3.2 に写します。
7. `COMPOSE_FILE` の最後に `deploy/chaos/measure/store-volatile.yaml` を足して構成を作り直し、`LABEL=valkey-volatile recovery.sh valkey` を実行します。永続化の無い保存先を作り直したときに失うものです。
8. 冗長構成で、`fault.sh partition valkey 'scscf-*'` のあいだに `latency.sh 5` を流し、返った応答とノードのログを表 12.3.4 に書きます。`fault.sh clear valkey` のあとで、もう一度流します。
9. 同じ機械で、12.1 の Go のベンチマークを `kvstore.MemoryStore` と Valkey で流し、表 12.3.3 に写します。Valkey は冗長構成の保存先（ポートを公開しない場合は `docker run --network container:<保存先のコンテナ>` で同じ名前空間から）か、`--network host` で動かしたものを使い、どちらかを書きます。

冗長構成の UE は、P-CSCF の一覧を持ちます。`ue-a` と `ue-b` が冗長構成のどの P-CSCF を使うかは、構成に従い、結果に書きます。

### 12.3 結果の表

結果は、まだ記入していません。`latency.sh` と `recovery.sh` は、同じ列を同じ順で英語の見出しの表に出します。行はそのまま写せます。

#### 12.3.1 網を通した登録と呼の時間

単位はミリ秒です。

| 構成 | 測るもの | 回数 | p50 | p90 | 最大 |
| --- | --- | --- | --- | --- | --- |
| memory | register | | | | |
| memory | register-2 | | | | |
| memory | invite | | | | |
| memory | answer | | | | |
| memory | bye | | | | |
| valkey | register | | | | |
| valkey | register-2 | | | | |
| valkey | invite | | | | |
| valkey | answer | | | | |
| valkey | bye | | | | |

#### 12.3.2 作り直したあとで戻るまでの時間と、失うもの

「通るまで」の時間は、作り直しの始まりから測ります。試した回数が 1 のときは、スクリプトの待ち時間を含む上限です。

| 構成 | ノード | 作り直しにかかった時間 | re-INVITE が通るまで（試した回数） | BYE | UE への新しい呼が通るまで（試した回数） | UE の REGISTER | 400 以上の応答 | `ue-b` への RTP の最長の途切れ（パケットの数） |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| memory | pcscf | | | | | | | |
| memory | scscf | | | | | | | |
| memory | mmtel | | | | | | | |
| memory | agw | | | | | | | |
| memory | icscf | | | | | | | |
| memory | hss | | | | | | | |
| valkey | pcscf-1 | | | | | | | |
| valkey | scscf-1 | | | | | | | |
| valkey | mmtel-1 | | | | | | | |
| valkey | agw-1 | | | | | | | |
| valkey | hss-1 | | | | | | | |
| valkey | valkey | | | | | | | |
| valkey-volatile | valkey | | | | | | | |

#### 12.3.3 `Store` の操作に足される時間

単位はマイクロ秒です。

| 流れ | 実装 | 保存先 | `kvstore` の操作の数 | p50 | p99 |
| --- | --- | --- | --- | --- | --- |
| 登録 1 回 | 各役割の実装 | `kvstore.MemoryStore` | | | |
| 登録 1 回 | 各役割の実装 | Valkey | | | |
| 呼 1 回 | 各役割の実装 | `kvstore.MemoryStore` | | | |
| 呼 1 回 | 各役割の実装 | Valkey | | | |
| SMS 1 通 | 各役割の実装 | `kvstore.MemoryStore` | | | |
| SMS 1 通 | 各役割の実装 | Valkey | | | |

#### 12.3.4 保存先が 1 つの役割から届かないとき

| 切った役割 | 切っているあいだの登録と呼 | ノードのログ | 戻したあと |
| --- | --- | --- | --- |
| `scscf-*` | | | |
| `pcscf-*` | | | |

### 12.4 今の構成での予行

冗長構成がまだ無いので、メモリの構成だけで、スクリプトが動くことを確かめました。下の数字は予行で、12.3 の表の値ではありません。構成は `compose.yaml` に `deploy/interconnect` を重ねたものです。網 A は `compose.yaml` と同じノードで、S-CSCF と IP-SM-GW の設定だけが IBCF への経路を持ちます。機械は 4 vCPU の仮想マシン 1 台で、ほかの作業も動いていました。

`latency.sh 10`（メモリ、ミリ秒）の結果です。

| 測るもの | 回数 | p50 | p90 | 最大 |
| --- | --- | --- | --- | --- |
| register | 10 | 15 | 20 | 20 |
| register-2 | 10 | 7 | 11 | 12 |
| invite | 10 | 23 | 29 | 33 |
| answer | 10 | 7 | 9 | 10 |
| bye | 10 | 4 | 5 | 9 |

`recovery.sh`（メモリ、`HOLD_FOR=20`、`CALL_FOR=30`）の結果です。

| ノード | 作り直しにかかった時間 | re-INVITE が通るまで | BYE | UE への新しい呼が通るまで | UE の REGISTER | 400 以上の応答 | RTP の最長の途切れ |
| --- | --- | --- | --- | --- | --- | --- | --- |
| pcscf | 1038 ms | 1228 ms（1） | 届いた | 30 秒で通らない | 0 | 403/INVITE が 2 | 20 ms（40） |
| scscf | 963 ms | 1134 ms（1） | 届いた | 30 秒で通らない | 0 | 403/INVITE が 4 | 20 ms（43） |
| mmtel | 970 ms | 通らず、呼が切れた（1） | 届かない | 7630 ms（1） | 0 | 481/BYE が 6、481/INVITE が 3 | 21 ms（56） |
| agw | 3661 ms | 3821 ms（1） | 届いた | 6087 ms（1） | 0 | なし | 920 ms（20） |
| icscf | 993 ms | 1163 ms（1） | 届いた | 3422 ms（1） | 0 | なし | 21 ms（44） |
| hss | 999 ms | 1161 ms（1） | 届いた | 20778 ms（2） | 0 | 500/INVITE が 3 | 25 ms（42） |

最後に、コミットした形のスクリプトで、`compose.yaml` だけの構成でももう一度流しました。`latency.sh 10` は、register が p50 16 ms、register-2 が 7 ms、invite が 35 ms、answer が 7 ms、bye が 4 ms でした。invite だけが上の表より大きいのは、機械の負荷の揺れと見ています。`recovery.sh` は、`scscf` と `mmtel` で上の表と同じ結果（403 が 4、481 の BYE と INVITE）になりました。

予行から分かったことです。

- メモリの構成では、P-CSCF か S-CSCF を作り直すと、登録が失われます。UE は登録したつもりのままなので、新しい呼には 403 が返り続けます。UE が次に登録し直すまで、着信できません。完了条件の「再登録なしで着信できる」が冗長構成で満たされるかは、表 12.3.2 の `pcscf-1` と `scscf-1` の行で確かめられます。
- MMTel AS を作り直すと、確立した呼の記録が失われ、re-INVITE と BYE に 481 が返ります。UE は re-INVITE の 481 で呼を終えます。
- メディア中継ノードの作り直しは、ほかのノードより長くかかりました（約 3.7 秒）。公開する 40 個のポートの割り当てのためと見ていますが、確かめていません。作り直したあとで RTP は戻りませんでした。
- HSS を作り直すと、新しい呼の 1 回目は 500 で失敗し、2 回目で通りました。S-CSCF の Diameter の接続が張り直されるまでの失敗と見ていますが、ログで確かめていません。

予行で分かった、スクリプトの限りです。

- `ue serve` は、応答のあとで 3 秒（`-tone` の既定）だけ音を送ります。`compose.yaml` の `ue-a` と `ue-b` は `-tone` を渡していないので、RTP の途切れは、作り直しの前後の 2 秒ほどしか測れません。作り直しのあとで RTP が戻るか（設計で決めた中継ノードの作り直し）を測るには、冗長構成の `ue-a` と `ue-b` に長い `-tone` を渡す必要があります。
- UE の REGISTER の数は、UE の登録の期限が測定のあいだに来ないので、0 になります。UE が自分で気づいて登録し直す動きを数えるには、REGISTER の時間切れか 503 を受ける場面が要ります。
- Go のベンチマークは、`go test ./internal/kvstore/storetest -run '^$' -bench . -benchtime 2000x` で、`kvstore.MemoryStore` の上だけを流しました。登録 1 回が 29 回の操作で p50 が 93.5 マイクロ秒、呼 1 回が 55 回で 184.5 マイクロ秒、SMS 1 通が 10 回で 16.6 マイクロ秒でした。11.8 の値とほぼ同じです。同じ機械にあった Valkey のコンテナはほかの作業のもので、使っていません。

### 12.5 測定に要る構成

1. 冗長構成の `ue-a` と `ue-b`（または測定用の UE）に、`-tone 60s` のような長い音を渡す必要があります。作り直しのあとで RTP が戻るかを、`recovery.sh` で測るためです。
2. 冗長構成の保存先のサービスの名前は `valkey` にします。`store-volatile.yaml` と、この章の手順がこの名前を使います。

## 確かめられなかったこと

- 11.8 の実装は、役割に組み込んで動かしていません。試したのは、`Store` の試験の組と、`Store` の呼び出しを並べた測定だけです。役割の中から使ったときの登録と呼の時間（4 章の pcap での測り方）は、組み込みのあとになります。
- 11 章で、外部の保存先の候補（Valkey）を 1 台の機械の中で試作して測りました。機械をまたいだ往復、負荷をかけたとき、各役割の `Store` の実装を通した登録と呼の時間（4 章の pcap での測り方）は測っていません。方式の残りの判断は、各役割の実装を組み込んだあとになります。
- Valkey のレプリカ、Sentinel、クラスタは試していません。ホストの OS が落ちたときに AOF の 1 秒ごとの同期で失う量は、Valkey の文書の説明だけで、試していません。
- valkey.io は、プロキシに拒まれました（403）。Valkey の文書は、同じプロジェクトの GitHub のリポジトリ（valkey-io/valkey-doc の main）から読みました。
- `valkey/valkey:9.0.8-alpine` のイメージのライセンスは、Valkey の 9.0.8 のタグの COPYING（BSD 3-Clause）で確かめました。イメージに入っている Alpine のパッケージのライセンスは確かめていません。
- 試作用のモジュール（go-redis の実装、測定の道具）は、リポジトリの外に置き、記録のあとで消しました。
- TS 23.380 の 5.6（WLAN）、5.8（5GC）、6 章から 9 章（SCC AS、EPC と 5GC の障害、PCRF の障害、AS の復旧）は読んでいません。5GC（VoNR）の P-CSCF の復旧は、5GC の実装に関わります。
- TS 24.229 5.1.1.4.1（305 を受けた UE の手順）の中身、5.4.1.5、7.6（XML の本文の表）と、P-CSCF の転送と購読の手順は読み直していません。コードのコメントと、ほかの条項の引用から書きました。
- TS 29.228 6.1.2.1 は、復旧に関わる段落だけを読みました。「S-CSCF reassignment pending flag」の細かい条件は読み込んでいません。
- TS 29.229 の 6.3.70（P-CSCF-Subscription-Info）と 6.3.74（Failed-PCSCF）は、AVP の表でコードと型を確かめただけです。
- Dx が Cx と同じアプリケーション ID を使うかは確かめていません。TS 29.228 6.4 は、Dx が Cx と一緒に使われるとだけ書いています。
- RFC 6733 は、見出しと 6.1.8 だけを読みました。DIAMETER_TOO_BUSY、DPR、5.5.4 の中身は確かめていません。
- RFC 2782（SRV の並べ方）、RFC 3608 と RFC 3327 の本文の大半、RFC 5626（outbound）、RFC 6223（keep-alive）は読んでいません。
- IR.92 2.2.1 の、305 と 503 での P-CSCF の選び直しは、[profiles.md](profiles.md) の記録から引きました。本文は読み直していません（www.gsma.com が 403）。
- etcd のライセンスと、Valkey の複数台の構成の作り方は確かめていません。
- 8 章の遅延（tc netem）は試せていません。この環境のカーネルに `sch_netem` が無く、モジュールも足せないためです。netem を掛けたときにブリッジのキャプチャにどう写るか、遅延の下で SIP と Diameter と RTP がどう動くかは、netem のあるカーネルで確かめる必要があります。
- ホストの側の veth に掛ける方法は試していません（8 章の「遅延と損失」）。
- Docker Desktop では何も試していません。VM のカーネルに nftables の netdev の egress のフックと netem があるか、`docker exec ... kill -QUIT 1` で起動し直すかは、確かめが要ります。egress のフックが入った Linux の版は、www.kernel.org、git.kernel.org が 403 で、wiki.nftables.org にもつながらず、確かめられませんでした。
- 8 章の試作は cgroup v1 のホストで行いました。cgroup v2 のホストでの `docker pause` の動きは試していません。
- `internal/sipdns` が DNS を問い合わせ直さないこと、S-CSCF が DNS の失敗に 404 を返すことが標準に合っているかは、確かめていません（8 章）。
- P-CSCF を同じアドレスで作り直したとき、NAT を通るホストのソフトフォン（[ホストとコンテナの通信の設計](../design/environment-capture.md)）への通信が続くかは試していません。
- Kubernetes での冗長構成は、この調査の範囲に入れていません。
