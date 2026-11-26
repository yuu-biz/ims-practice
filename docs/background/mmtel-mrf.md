# MMTel AS、Ut/XCAP、MRF の調査

この文書は、MMTel の付加サービス、Ut の XCAP、MRF（音声ガイダンス、会議、CAT）について、仕様が定めることと、この実装の作りを調べた記録です。決めた設計は [docs/design/mmtel-mrf.md](../design/mmtel-mrf.md) にあります。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは版も書きます。版を書いていない条項は、表「読んだ資料」の版です。

付加サービスは、自作の AS（`ims` コマンドの役割 `mmtel`）で提供します。AS は iFC で呼び出され、サービスは iFC の設定で加え、プラグインの仕組みは作りません。サービスのデータは Sh で扱い、IR.92 が求める最小限の Ut/XCAP を付けます。MRFC は SIP の役割として作り、MRFP は今のメディア中継ノードの上に作って、音声ガイダンス、会議、CAT を扱います。FRBT（flexible RBT、利用者が選べる呼び出し音）は CAT と同じものです。

「今のコードとの差」の節は、実装を始める前のコードとの差の記録です。「設計の候補」の表は調べた時点の案で、決めた設計との違いは「決めた設計と、調査時の案との違い」に書きます。

## 読んだ資料

| 仕様 | 版 |
| --- | --- |
| GSMA IR.92 | v24.0 |
| TS 22.173（MMTel、Stage 1） | V21.0.0 |
| TS 24.173（MMTel、Stage 3） | V19.0.0 |
| TS 24.607（OIP、OIR） | V19.1.0 |
| TS 24.608（TIP、TIR） | V19.0.0 |
| TS 24.604（CDIV） | V19.0.0 |
| TS 24.611（ACR、CB） | V19.0.0 |
| TS 24.615（CW） | V19.0.0 |
| TS 24.610（HOLD） | V19.0.0 |
| TS 24.606（MWI） | V19.0.0（目次だけ） |
| TS 24.605（CONF） | V19.0.0（目次と 4.6 のほかのサービスとの関係） |
| TS 24.629（ECT） | V19.0.0（目次だけ） |
| TS 24.147（会議） | V19.0.0 |
| TS 24.623（Ut 上の XCAP） | V19.0.0 |
| TS 24.628（音声ガイダンスと呼の進行の表示） | V19.0.0 |
| TS 24.182（CAT） | V19.0.0 |
| TS 24.229 | V20.1.0 |
| TS 23.218 | V19.0.0 |
| TS 23.228 | V20.1.0 |
| TS 23.003 | V20.1.0 |
| TS 29.228 | V20.0.0 |
| TS 29.328、TS 29.329（Sh） | V20.0.0、V19.1.0 |
| TS 29.364（Sh で運ぶ MMTel のサービスデータ） | V19.1.0 |
| TS 29.333（Mp の H.248 プロファイル） | V19.0.0 |
| RFC 4240（netann）、RFC 3960（early media のモデル）、RFC 4825（XCAP）、RFC 7044（History-Info） | 本文を読んだ |
| RFC 4575、RFC 4745、RFC 4458、RFC 5009、RFC 6230、RFC 5707、RFC 7616 | 入手したが、必要な箇所を拾い読みしただけ |

TS 24.173 と TS 24.604 は Word 97 の形式（.doc）で配布されていて、表紙の版の欄を変換で読めませんでした。版はファイル名（24173-j00、24604-j00）から読みました。

## MMTel AS

### 必須の付加サービス

IR.92 v24.0 2.3.1 は、MMTel の付加サービスを TS 24.173 のとおりに提供するよう求め、UE と TAS（Telephony Application Server）が対応するサービスを表 2.1 に挙げています。加入者にどのサービスを提供するかは事業者が決めます。サービスごとの制約の節は 2.3.3 から 2.3.12 です。

| サービス | 仕様 | IR.92 の制約 | 網の義務 |
| --- | --- | --- | --- |
| OIP（発信者番号の表示） | TS 24.607 | 2.3.12。UE は P-Asserted-Identity と From の両方から番号を表示できる。From に信頼できる番号を入れるのは網の責任 | 必須 |
| OIR（発信者番号の非通知） | TS 24.607 | 2.3.6。SIP の手順は必須、TS 24.607 4.10 の設定（XCAP）は不要 | 必須 |
| TIP（着信者番号の表示） | TS 24.608 | なし | 必須 |
| TIR（着信者番号の非通知） | TS 24.608 | 2.3.7。SIP の手順は必須、TS 24.608 4.9 の設定は不要 | 必須 |
| CFU、CFB、CFNR、CFNRc、CFNL（着信転送） | TS 24.604 | 2.3.8。XCAP の規則は表 2.2 の条件だけ（busy、media、no-answer、not-registered、not-reachable、rule-deactivated、動作は target、要素は NoReplyTimer）。CD（応答前の転送）は表に無い | 必須 |
| BAIC、BAOC、BOIC、BOIC-exHC、BIC-Roam（着信と発信の規制） | TS 24.611 | 2.3.9。条件は表 2.3 の roaming、international、international-exHC、rule-deactivated だけ。国際の規制は Rel-9 の機能 | 必須 |
| HOLD（保留） | TS 24.610 | 2.3.10。保留した UE はメディアを送らない | 必須 |
| CW（通話中着信） | TS 24.615 | 2.3.4。端末で行う方式だけが必須で、網で行う方式は不要。UE は Alert-Info に対応し、Ut を使わずに設定できる | 端末の方式に網が対応する |
| 会議（3 者通話を含む） | TS 24.605、TS 24.147 | 2.3.3。Three-way session の作り方（TS 24.147 5.3.1.3.3）、UE は 2b（会議のフォーカスに REFER を送る）を使う。会議のイベントパッケージ（RFC 4575）は網が必須で、最小の要素が決まっている。フロア制御は不要 | 必須 |
| MWI（メッセージの待機の表示） | TS 24.606 | 2.3.5。UE は必須、網は任意 | 任意 |
| ECT（相談してからの転送） | TS 24.629 | 2.3.11。UE は必須、網は任意 | 任意 |

表 2.1 の前の文は「UE と TAS は表のサービスに対応する」と書いていますが、MWI（2.3.5）と ECT（2.3.11）の節では網の対応を任意にしています。このプロジェクトでは、各節の書き方に従って MWI と ECT の網の側を任意とみなします。ACR（匿名の着信の拒否）は表 2.1 に無く、任意です。Call Composer は網が任意です（2.3.1）。

TS 24.173 は、各サービスの仕様の場所を示す目次のような文書です。役割の決まり（4.1A）は、UE が MMTel の参加者になり、AS が MMTel AS になるというものです。機能をどの AS に割り振るかは実装が決めます。ICSI は `urn:urn-7:3gpp-service.ims.icsi.mmtel` です（5.1）。MMTel AS は、着信側の利用者へ送る要求と、発信側へ返す 1xx と 2xx の応答に、この ICSI を入れた Feature-Caps ヘッダを付けます（5.2 d、RFC 6809）。サービスどうしの相互作用は、TS 22.173 の 8.2 にサービスごとの節があり、Stage 3 の各仕様の 4.6 にも書いてあります。

### Ut/XCAP の要件

- IR.92 2.3.2 は、付加サービスの設定に Ut の XCAP（TS 24.623）を使うよう求めています。XCAP Root URI が設定されていなければ、UE は TS 23.003 13.9 の形で作ります（`http://xcap.<ドメイン>`、V20.1.0 13.9.1.2）。XUI（XCAP の利用者の識別子）は、REGISTER の 200 の P-Associated-URI にある既定の IMPU です。
- UE は 1 回の要求で 1 つのサービスだけを設定し、CDIV や CB のように `<rule>` が複数あるサービスでは 1 回に 1 つの `<rule>` だけを変えます。PUT と DELETE は If-Match を付けた条件付きの操作にします（RFC 4825 7.11）。
- 規則を加えるのは事業者です。UE は既存の `<rule>` の子要素を変え、一致する `<rule>` が無ければ、そのサービスは提供されていないとみなし、新しい `<rule>` を文書に入れません。事業者は提供するサービスごとに、最初の文書に規則を入れておきます（2.3.2 Note 3）。止めるときは `<rule-deactivated>` を条件に足し、規則は消しません。
- 認証は、IR.92 2.2.2 が TS 24.623 の Ut の認証の手順に従うよう求めています。GBA（TS 33.222、TS 24.109）は推奨にとどまります（2.2.2 Note 1）。UE は、401 で認証を求められずに 2xx が返る場合にも対応します。APN が自網に向いていれば、網が UE を別の方法で確かめられるためです（2.2.2 Note 2）。
- TS 24.623 5.2.3.1 と 5.2.3.2.1 によれば、GAA（GBA）を使わない網では、AS が RFC 7616 の HTTP Digest で利用者を認証します。資格情報をどう配るかは仕様の範囲外です。認証プロキシ（AP）を置く形（5.2.2）もあり、そのときは AP が X-3GPP-Asserted-Identity を付けて AS に渡します。
- 文書の場所と型（TS 24.623 6.2）。AUID は `simservs.ngn.etsi.org`、既定の名前空間は `http://uri.etsi.org/ngn/params/xml/simservs/xcap`、MIME 型は `application/vnd.etsi.simservs+xml` で、文書は利用者のホームの `simservs.xml` です。したがって URI は `<XCAP Root>/simservs.ngn.etsi.org/users/<XUI>/simservs.xml` になります。所有者は `<simservs>` の直下の要素を作ることも消すこともできず、許されない操作には 409 を返します。
- XCAP サーバーは、要求をサービスの能力の断片（serv-cap）と照らし合わせ、許されない変更に 409 を返します（5.2.3.4、5.3.2.4）。
- IR.92 5.5.2 は、PS Data Off のときも Ut の設定を例外として通すよう求めています。このプロジェクトには Data Off が無いので関係しません。

### OIP と OIR（TS 24.607）

- 発信側の網（4.3.2）。UE が P-Preferred-Identity で希望を示し、P-CSCF が登録済みの IMPU と照らして P-Asserted-Identity にします。UE が何も示さなければ既定の IMPU を使います。S-CSCF は SIP URI に対応する tel URI を 2 つ目の P-Asserted-Identity として足します。
- 発信側の UE（4.5.2.1）。一時的に非通知にするときは、From を `"Anonymous" <sip:anonymous@anonymous.invalid>` にし、Privacy に `id`（P-Asserted-Identity だけを隠す）か `header` を入れます。非通知が既定の契約で一時的に通知するときは、Privacy に `none` を入れます。
- 発信側の AS（4.5.2.4）。恒久的な非通知の契約なら、AS が Privacy に `id` か `header` を入れ、`none` を取り除き、From から識別情報を消すか Privacy に `user` を足します。一時的な契約では、既定値と UE の指示を組み合わせて同じことを行います。OIR を契約していない利用者が非通知を求めたときは、事業者の方針で `403` と Warning `399 "OIR not subscribed"` を返せます。
- 着信側の AS（4.5.2.9）。着信者が OIP を契約していなければ、P-Asserted-Identity と Privacy を取り除きます。Privacy が `id` なら、その値は残します。着信側の UE はこの値で、発信者が非通知にしたことを知るためです。`header` なら匿名にできるヘッダを匿名にし、`id` を足して `header` を取り除きます。`user` なら From などを匿名にします。このとき AS は透過的な B2BUA になることがあります。
- S-CSCF は着信側で、RFC 3323 と RFC 3325 の非通知を P-Asserted-Identity に掛けてもよいことになっていますが、Privacy の値は消しません（TS 24.229 5.4.3.3 step 12）。
- 発信者の識別子に基づくサービス（ICB、ACR）は、OIP より前に動かします（4.3.3 NOTE 2）。P-Asserted-Identity が消えると、後ろのサービスが判断できなくなるためです。

### TIP と TIR（TS 24.608）

- 着信側の P-CSCF は、UE の応答の P-Asserted-Identity を、要求の P-Called-Party-ID に入っていた IMPU に置き換えます（TS 24.229 5.2.6.4.4 step 1）。
- 着信側の AS（4.5.2.9）。TIR の恒久的な契約なら、応答の Privacy から `none` を取り除き、`id` を入れます。INVITE の Supported に `from-change` があれば取り除きます。
- 発信側の AS（4.5.2.4）。発信者が TIP を契約していなければ、応答の P-Asserted-Identity と Privacy を取り除きます。

### CDIV（TS 24.604）

- AS の動作のモード（4.5.2.6.0）。To を変える必要があれば、AS は routeing B2BUA（TS 24.229 5.7.5）になります。それ以外なら SIP プロキシ（5.7.4）か routeing B2BUA のどちらでもかまいません。To を変えるのは、転送元の利用者が転送先に自分の番号を見せない場合です。
- 転送の回数の上限（4.5.2.6.1）。History-Info の cause-param 付きの項目か index から回数を数えます。上限を超えたら、事業者の設定に従って 486 か 480 と Warning（例は "Too many diversions appeared"）で断るか、最後の転送元へ届けます。上限は TS 29.364 の事業者のデータ（`total-number-of-diversions-for-each-communication`）にあります。
- 転送する INVITE（4.5.2.6.2）。Request-URI を転送先にし、tel URI なら `user=phone` の SIP URI に直します。Request-URI には RFC 4458 の cause-param を付けます。値は CFB が 486、CFNR が 408、CFU が 302、CFNL が 404、CFNRc が 503、応答前の CD が 480、呼び出し中の CD が 487 です（Annex C）。History-Info には、転送元と転送先の 2 項目を RFC 7044 10.3 の index の規則で足し、転送先の項目に `mp` を付けます。転送元が番号を隠すときは、項目に `history` の Privacy をエスケープして入れ、To を転送先に変えます。
- 転送の続け方（4.5.2.6.3）。CFU、網が判断する CFB、CFNL では、転送先への INVITE を S-CSCF に送ります。CFNR では、最初の 180 で無応答のタイマーを始めます。フォークで届いた 2 つ目以降の 180 では、タイマーをやり直しません。タイマーが切れたら、転送元への INVITE を `Reason: SIP;cause=408` を付けた CANCEL で止めて転送します。CFB（利用者が判断する場合）では、UE の 486 に ACK を返してから転送します。CFNRc では、408、503、500 が 100 以外の暫定応答の前に返ったことを「届かない」とみなします（4.5.2.6.6）。
- 無応答のタイマーの範囲は 5 秒から 180 秒です（XML スキーマの NoReplyTimer）。既定値は事業者が決めます（4.8.1）。
- CFNR で転送先から返った 18x に P-Early-Media が無いときは、元の early dialog で次に送るメッセージに `P-Early-Media: inactive` を付けるか、199 を送って新しい early dialog で 18x を送ります（4.5.2.6.3 item 2）。
- 発信者への通知（4.5.2.6.4）。契約で通知するなら、転送元の IMPU を P-Asserted-Identity に入れた 181 を発信者に送り、History-Info を付けます。通知に音声ガイダンスを付けることもできます（TS 24.628）。
- 転送先の AS（4.5.2.7）は History-Info を保存し、180、181、200 に History-Info が無ければ保存したものを入れます。
- 着信側の UE（IR.92 2.3.8）は History-Info で転送元を表示します。cause-param を持つ次の index の項目があるときだけ、その前の項目を転送元とみなします。
- 規則の評価（4.9.1.2）。規則は上から順に評価し、最初に一致した規則の動作だけを行います。no-answer は無応答を検出したとき、busy は 486 を受けたときに評価します。
- iFC の例（Annex B）は、Method が INVITE という条件だけです。

### CB と ACR（TS 24.611）

- OCB（4.5.2.4.1）。発信側の AS がプロキシか routeing B2BUA として動き、規則が `allow=false` になれば終端の UA として 603 を返します。緊急呼は規制しません。断る前に音声ガイダンスを流すこともできます。
- ICB（4.5.2.6.1）。着信側の AS が、発信者の P-Asserted-Identity（または From）で規則を評価し、規制するなら 603 を返します。
- ACR（4.5.2.6.2）。P-Asserted-Identity があり、Privacy に `id`、`header`、`user` のどれかが入っていれば、433（Anonymity Disallowed、RFC 5079）を返します。
- 規則の評価（4.9.1.2）。RFC 4745 10.2 の組み合わせ方で評価します。CDIV と違って、一致したすべての規則の結果を組み合わせ、`allow=true` の規則が 1 つでもあれば通します。許可の一覧はこの性質を使って作ります。
- 条件の意味（4.9.1.4）。roaming は、利用者が自網以外のアクセス網から登録しているときに真になります。登録時の P-Visited-Network-ID と P-Access-Network-Info で判断します。international は、電話番号の宛先が利用者のいる国の網でないときに真になります。international-exHC は、それに加えて宛先が利用者の自網（home network）の中でもないときに真になります。
- 相互作用（4.6.7）。着信側では ICB と ACR が CDIV より先に効きます。転送先への発信には、転送元の OCB が効きます。

### CW と HOLD（TS 24.615、TS 24.610）

- CW の端末の方式（TS 24.615 4.5.5.3.2）。通話中の UE は 2 つ目の INVITE を受け、180 に `Alert-Info: <urn:alert:service:call-waiting>` を付けます。AS（4.5.5.2.3）は、この 180 を見て通話中着信の状態と判断します。発信者に知らせない契約なら Alert-Info を取り除き、知らせる契約なら音声ガイダンスを流せます。網の方式で使う T_AS-CW は網の任意の機能です（4.7）。
- HOLD の UE（TS 24.610 4.5.2.1）。保留する UE は re-INVITE を送り、sendrecv だった流れを sendonly、recvonly だった流れを inactive にします（RFC 3264）。
- HOLD の AS（4.5.2.4）。緊急通報を受けた機関からの折り返しの呼では、事業者の方針で保留を 403 で断ります。網の任意の機能として、b=AS を小さくし、保留された相手に音声ガイダンスを流せます。音声ガイダンスは、AS の側で sendonly になった流れでしか流せません。
- IR.92 は、保留の音声ガイダンスを求めていません。網の側の HOLD は、re-INVITE を正しく中継できれば足ります。

### AS の動作のモード

TS 23.218 9.1.1 と TS 24.229 5.7 は、AS の動作のモードを 5 つに分けています。

| モード | 仕様 | 使うサービス |
| --- | --- | --- |
| 終端の UA、リダイレクトサーバー | 9.1.1.1、5.7.2 | 規制で断る OCB、ICB、ACR（603、433）、会議のフォーカスが受ける INVITE |
| 発信の UA | 9.1.1.2、5.7.3 | 転送の通知の MESSAGE（CDIV の任意の機能）、会議への INVITE（2b で招く相手） |
| SIP プロキシ | 9.1.1.3、5.7.4 | OIP、OIR、TIP、TIR、To を変えない CDIV、規制しない場合の CB |
| 第三者呼制御（routeing B2BUA、initiating B2BUA） | 9.1.1.4、5.7.5 | To を変える CDIV、CFNR、From を匿名にする OIR、音声ガイダンス、CAT、会議 |
| 関与しない | 9.1.1.5 | Record-Route を入れない場合の後続の要求 |

1 つの AS がすべてのサービスを持つなら、最も強いモードに合わせる必要があります。TS 24.604 4.5.2.6.0 NOTE も、ほかのサービスを合わせて提供するなら routeing B2BUA が要ると書いています。

プロキシのモードで CFNR を作ると、発信者には転送元と転送先の 2 つの early dialog が届きます。今の UE（`sip.OutgoingCall`）は最初の dialog しか残さないので（`sip/ua.go` の `SIMPLIFIED:`、RFC 3261 13.2.2.4）、転送先の 200 を捨ててしまいます。B2BUA なら、AS が発信者の側の dialog を持ち続けるので、この問題は起きません。

### iFC

- 発信側（SessionCase 0、ORIGINATING_REGISTERED）。Method が INVITE（会議の SUBSCRIBE を AS に渡すなら SUBSCRIBE も）。OIR、TIP、OCB を動かします。
- 着信側（SessionCase 1、TERMINATING_REGISTERED）。Method が INVITE。ICB、ACR、CDIV、OIP、TIR、CW の通知を動かします。
- 未登録の着信（SessionCase 2、TERMINATING_UNREGISTERED）。CFU と CFNL を、利用者が登録していないときにも動かすのに要ります。
- 転送後の発信（SessionCase 4、ORIGINATING_CDIV）。AS が転送して S-CSCF に戻した INVITE に、転送元の発信側のサービス（転送先への OCB）を掛けます。TS 24.229 5.4.3.3 step 3 で、S-CSCF は Request-URI が保存した値と違うとき、iFC の評価を打ち切るか（a）、ORIGINATING_CDIV の iFC を作り直して評価するか（b）を選びます。どちらにするかは iFC の情報で決めます（NOTE 6）。
- MESSAGE を拾う iFC とは、Method で分かれるので重なりません。
- 1 つの AS が発信と着信の両方で呼ばれるので、AS はどちらの場合かを知る必要があります。TS 24.229 5.7.1.3A は、P-Served-User（RFC 5502、RFC 8498 の `sescase`、`regstate`、`orig-cdiv`）があればそれで、無ければ History-Info、P-Asserted-Identity（発信）、Request-URI（着信）で served user を決めます。S-CSCF は P-Served-User を入れてもよく、事業者の方針で sescase と regstate を付けます（5.4.3.3 step 4 c）。
- DefaultHandling は、付加サービスを止めずに呼を通すなら SESSION_CONTINUED、規制を確実に効かせるなら SESSION_TERMINATED です。どちらにするかは仕様が決めていません。

### サービスのデータと Sh

- TS 29.364 は、AS が HSS に置くサービスのデータの形を決めています（V19.1.0 1、4、5）。HSS はこのデータの構文だけを理解し、意味は解釈しません（transparent data）。複数の AS が同じ利用者のデータを共有できるように、データは HSS に置き、AS は Sh で読み書きします（5）。
- 形は XML とバイナリの 2 つです。XML の形はすべての MMTel サービスを扱えます。Service-Indication は `MMTEL-Services` で（7.2.0）、文書のルートは `MMTelServices`、サービスごとに `complete-<サービス>` の要素があり、その中に利用者の部分（TS 24.604 などのスキーマ、つまり simservs の断片）と事業者の部分（`operator-<サービス>`、契約の有無と契約の選択肢）があります（7.1、7.2.1）。独自の Service-Indication も使えます（7.2.0）。
- CDIV の事業者の部分には、転送の回数の上限と無応答のタイマーの既定値があります（7.2.8.2）。CAT には利用者の部分が定義されておらず、事業者の部分は契約の有無だけです（7.2.22）。
- Sh の操作（TS 29.328 6.1.2.1、TS 29.329 6.3）。Repository Data は Data-Reference 0 で、Service-Indication と Sequence-Number を持ちます。新しいデータは Sequence-Number 0 で書き、変えるときは元の値に 1 を足して送ります（65535 の次は 1）。番号が合わなければ HSS は DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC（5105、TS 29.329 6.2.2.7）を返します。データが無ければ UDR は失敗します（TS 29.328 6.1.3.1 step 5）。
- TS 29.364 8.1 は、AS がデータを読んだら SNR で変更の通知を受けるよう勧め（should）、通知を受けるなら PNR に対応するよう求めています。

### 状態と規模

- 利用者ごとのサービスのデータは HSS にあるので、AS は初期の要求ごとに UDR で読めば、利用者の状態を持たずに済みます。どの AS の台も、どの利用者の要求も処理できます。
- B2BUA にすると、AS は 1 つの呼で 2 つの dialog を持ちます。dialog の中の要求は Record-Route で AS に戻ってくるので、ここで台を固定するかどうかを決める必要があります。
- 無応答のタイマー（CFNR）は呼ごとのタイマーで、登録や購読の期限のタイマーなど、ほかのタイマーと同じく、今はプロセスの中で動かすしかありません。

### Ut/XCAP の最小の形

- 置き場所。TS 24.623 の図 1 では、Ut は UE と AS を直接つなぎます。XCAP サーバーを `mmtel` の役割に入れ、別のパッケージに分けておけば、あとで別の役割にもできます。
- 文書の扱い。GET は UDR で `MMTelServices` を読み、各 `complete-*` の利用者の部分を集めて `simservs.xml` として返します。PUT は文書か 1 つの `<cp:rule>` を差し替え、PUR で書き戻します。ETag に HSS の Sequence-Number を使えば、If-Match の照合（RFC 4825 7.11）と HSS の楽観的な排他（5105）を同じ番号で扱えます。If-Match が合わなければ 412、文書の制約に反すれば 409 を返します。
- 扱う範囲。文書全体と、`communication-diversion` の `NoReplyTimer`、`<cp:rule id=...>` の節点セレクタだけにします。CB、OIR、TIR も同じ形で扱えます。xcap-diff の購読（RFC 5875、TS 24.623 5.2.1.2）は UE が「すべき」ものなので後回しにできます。
- 認証。候補は 3 つあります。
  - HTTP Digest（RFC 7616）を AS で行う。TS 24.623 5.2.3.2.1 のとおりの方式で、資格情報の配り方は仕様の範囲外です。HSS のスタブに Ut 用の HA1 を置き、独自の Service-Indication の Repository Data として AS が Sh で読めば、資格情報の置き場所が HSS の 1 か所で済みます。これが調べた時点の案です。資格情報の配り方は仕様が決めていないので、加入者のほかのデータと同じ HSS にまとめれば、設定の場所が増えないためです。
  - GBA（BSF、Zh、Ua）。IR.92 の推奨ですが、BSF の役割と Zh を新しく作る必要があり、学習の対象としては大きすぎます。作りません。
  - 認証しない。IR.92 2.2.2 Note 2 の「401 無しで 2xx」の場面にあたりますが、網が UE を別の方法で確かめる仕組みが要ります。そこを省くので `SIMPLIFIED:` になります。
- 最初の文書。HSS のスタブの加入者の設定に、事業者が用意する最初の `MMTelServices` を書きます。CFU、CFB、CFNR、CFNRc、CFNL、BAIC、BAOC などの規則を、`<rule-deactivated/>` 付きで入れておきます（IR.92 2.3.2 Note 3）。
- UE とダッシュボード。UE シミュレータに XCAP のクライアントを入れ、CLI と操作用の API から設定できるようにします（たとえば CFU を有効にして転送先を入れる）。UE は GET で ETag と規則を読み、該当する `<rule>` から `<rule-deactivated/>` を除いて target を書き、If-Match を付けて PUT します。ダッシュボードは UE の操作用の API へ中継するだけにします。そうすれば Ut の HTTP もキャプチャに残ります。
- ポートと暗号化。XCAP は HTTP の 80 番、平文を既定にします（既定では暗号化せず、標準のポート番号を使うため）。TS 24.623 5.2.3.1 は、GAA を使わない網で ETSI TS 183 038 が TLS を求める箇所に TS 33.310 Annex E の TLS の型を求めています。どの手順が TLS を求めるかは読めていません。

### 今のコードとの差

- S-CSCF
  - 着信側で利用者が未登録なら、すぐに 480 を返しています（`scscf/session.go` の `SIMPLIFIED:`）。CFU と CFNL を未登録の利用者に掛けるには、TERMINATING_UNREGISTERED の iFC の評価、SAR（UNREGISTERED_USER）でのプロファイルの取得、AS が残っていないときの 480（TS 24.229 5.4.3.3 の未登録の手順）が要ります。
  - 転送で Request-URI が変わったときは、案 a だけを実装しています（`scscf/services.go` の `SIMPLIFIED:`）。ORIGINATING_CDIV の案 b がありません。
  - P-Served-User を入れていません（`scscf/services.go` の `SIMPLIFIED:`）。
- HSS と I-CSCF
  - 未登録で着信のサービスを持つ利用者に、LIA で S-CSCF の名前か Server-Capabilities を返しません（`hss/hss.go` の `SIMPLIFIED:`）。TS 29.228 6.1.4.1 step 3 では、未登録でも着信のサービスがあれば、割り当て済みの S-CSCF の名前を返し、無ければ能力を返します。I-CSCF は S-CSCF を選ぶ処理（`choose`）をすでに持っています。
  - PSI に対応していません（`icscf/session.go`、`hss/hss.go` の `SIMPLIFIED:`）。MRF の会議の factory URI に要ります。
- Sh
  - Repository Data（Data-Reference 0）、Service-Indication、Sequence-Number、5105 がありません。SNR と PNR は `SIMPLIFIED:` で省いています。
- P-CSCF
  - P-Preferred-Identity を照合せず、いつも最初の IMPU を P-Asserted-Identity にしています。
  - 応答に P-Asserted-Identity を入れていません（TS 24.229 5.2.6.4.4）。TIP に要ります。
  - SDP を書き換えるのは INVITE の offer だけです（`pcscf/session.go` の `SIMPLIFIED:`）。保留の re-INVITE の SDP を IMS-AGW の中継へ向け直せません。
- SIP スタック
  - UA は re-INVITE に 488 を返し、自分からも送りません（`sip/ua.go` の `rejectReINVITE`）。HOLD と B2BUA に要ります。
  - B2BUA の部品がありません。2 つの dialog を対応させ、暫定応答、PRACK、CANCEL、BYE、dialog の中の要求を中継する部品です。
  - REFER（RFC 3515）、Replaces（RFC 3891）、Alert-Info、History-Info、Privacy、Feature-Caps の扱いがありません。
- UE シミュレータ
  - 通話は 1 本だけで、2 本目には 486 を返します（`ue/line.go`）。CFB の試験にはそのまま使えます。CW には 2 本目の着信が要ります。
  - 非通知の発信、History-Info の表示、保留、XCAP のクライアントがありません。

### 設計の候補

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| AS の置き場所 | `ims` コマンドの新しい役割 `mmtel`（役割は設定で切り替える）。AS の URI は DNS で引き、複数台で動かす | なし |
| AS の動作のモード | AS 全体を最初から routeing B2BUA として作る。CFNR、To を変える CDIV、From を匿名にする OIR、MRF の音声ガイダンス、CAT、会議がすべて B2BUA を必要とするため | サービスごとにプロキシと B2BUA を使い分ける。SIP の変更は小さいが、MRF を加えるときに作り直しになる。CFNR には UE のフォーク対応が要る |
| サービスの並べ方 | 1 つの AS の中で決まった順に並べる。着信は ICB と ACR、CFU と CFNL、OIP と OIR の順に掛け、UE へ送ったあとの応答で CFB、CFNRc、CFNR、TIR を掛ける。発信は OCB、OIR、応答に TIP | サービスごとに AS の URI を分け、iFC の Priority で並べる。iFC で拡張できるが、1 つの INVITE が AS を何度も通る |
| 発信と着信の見分け | S-CSCF が P-Served-User に `sescase`、`regstate`、`orig-cdiv` を入れる（TS 24.229 5.4.3.3 step 4 c、RFC 5502、RFC 8498）。キャプチャで場合が見える | iFC の ServerName を場合ごとに変える（`sip:orig@mmtel...` など）。S-CSCF の変更が要らない |
| 未登録の着信 | HSS の LIA、I-CSCF の S-CSCF の選択、S-CSCF の SAR（UNREGISTERED_USER）と TERMINATING_UNREGISTERED の iFC を作る。CFNL と未登録時の CFU に要る | CFNL を省き、`SIMPLIFIED:` に書く。IR.92 の必須を満たさない |
| 転送後の発信 | S-CSCF に ORIGINATING_CDIV（案 b）を加え、転送先への OCB を iFC で掛ける | AS が転送するときに自分で OCB を評価する。仕様の session case が見えなくなる |
| サービスのデータ | HSS の Repository Data に TS 29.364 の `MMTelServices`（Service-Indication `MMTEL-Services`）を置く。AS は初期の要求ごとに UDR で読み、キャッシュしない。SNR と PNR は `SIMPLIFIED:` で省く | AS がキャッシュし、SNR と PNR で更新を受ける。TS 29.364 8.1 の勧めに沿うが、状態が AS の台に残る |
| XCAP サーバー | `mmtel` の役割の中の HTTP のサーバーとして作り、`internal/xcap` のパッケージに分ける。ETag は Sequence-Number | 役割 `xcap` を別に作る |
| Ut の認証 | HTTP Digest（RFC 7616）を AS で行い、HA1 は HSS の独自の Repository Data に置く | GBA。認証しない（`SIMPLIFIED:`） |
| B2BUA の状態 | 呼の状態を AS の `Store` に置き、Record-Route と Contact には受けた台の URI を入れる。dialog の中の要求は同じ台に戻る。タイマーはプロセスの中に置く | Record-Route をサービスの名前にし、どの台でも `Store` から状態を読み直す。トランザクションの状態は台を移れないので、外部の保存先と冗長構成を待つ |
| MWI と ECT | 網の側は作らない（IR.92 で網は任意）。UE が受けたときの動作も後回しにする | MWI だけ作る（RFC 3842 の SUBSCRIBE と NOTIFY。ボイスメールが無いので見せ方が難しい） |
| CW | 端末の方式。UE に 2 本目の着信と Alert-Info を加え、AS は Alert-Info を見て通知の扱いを決める | なし（網の方式は IR.92 で不要） |
| HOLD | AS は re-INVITE を中継するだけにする。b=AS の書き換えと保留の音声ガイダンスは網の任意の機能なので、MRF の音声ガイダンスと合わせて決める | 保留の音声ガイダンスを MRF で加える |

## MRF

### 構成と参照点

- MRF は MRFC と MRFP に分かれます（TS 23.228 4.7）。MRFC は MRFP のメディアの資源を制御し、AS と S-CSCF から来た情報を解釈します。MRFP は Mb のベアラを扱い、メディアを混ぜ、音声ガイダンスを流し、トランスコードを行います。
- Mr（S-CSCF と MRFC）と Mr'（AS と MRFC）は SIP です。Cr は AS と MRFC のあいだで、MRFC が AS から文書と資源を取ってきたり、メディア制御の要求と通知を交わしたりします（TS 23.218 8.2.2、9.2.4）。Mp（MRFC と MRFP）は H.248 で、プロファイルは TS 29.333 です。
- MRB（TS 23.218、RFC 6917）は、複数の MRF から資源を割り当てる任意の機能です。このプロジェクトでは作りません。

### AS が MRFC を動かす方法

TS 24.229 10 は、方法を目的ごとに挙げています。

| 目的 | 方法（TS 24.229 の条項） |
| --- | --- |
| 音声ガイダンスとトーン | RFC 4240 の announcement service（10.2.2.2）、RFC 5552 の VoiceXML（10.2.2.3）、RFC 6230 と RFC 6231（10.2.2.4）のどれか |
| 会議 | RFC 4240 の conference service（10.2.3.2）、RFC 6230 と RFC 6505（10.2.3.3）のどちらか |
| トランスコード | RFC 4240 の conference service、RFC 6230 と RFC 6505、RFC 4117（5.7.5.6）のどれか |

MSML（RFC 5707）は TS 24.229 に出てきません。3GPP が参照する方法のうち最も簡単なのは RFC 4240 です。SIP の Request-URI の user part でサービスを選ぶだけで、新しいプロトコルが要りません。

- 音声ガイダンス（RFC 4240 3）。AS は `sip:annc@<MRFC>;play=<URI>` に INVITE を送ります。`play` は必須で、`repeat`、`delay`、`duration`、`locale` は任意です。MRFC は SDP を交わして 200 を返し、ACK を受けてから流し、流し終えたら BYE を送ります。`play` が無ければ 400、内容が見つからなければ 404 を返します。early media で流す形は RFC 4240 自身が非推奨にしています。
- 会議（RFC 4240 5）。`sip:conf=<識別子>@<MRFC>` に INVITE を送ると、その識別子の混ぜ合わせが作られ、同じ URI への 2 つ目以降の INVITE は参加になります。BYE で抜け、dialog が 1 つでも残るあいだは続きます。識別子が無ければ 404 を返します。
- サービスを知らない、または応じられないときは 488 を返します（RFC 4240 2）。
- MRFC の SIP の手順（TS 24.229 5.8）。音声ガイダンスと会議の INVITE には終端の UA として 100 を返します。音声ガイダンスでは ACK を受けたら流し始め（5.8.2.2.1）、BYE で止めます（5.8.3.1.1）。時間を決めた再生が終わったら、MRFC が BYE を送ります（5.8.3.2.1）。MRFC は Contact に自分を指す GRUU を使います（5.8.1）。
- AS から MRFC への経路は、Mr' で直接送る形と、S-CSCF（ISC と Mr）を通す形があります。

### 音声ガイダンス（TS 24.628）

- MRF でメディアを流す AS は、第三者呼制御（TS 24.229 5.7.5）とメディア制御（10.2）を使います（4.7.2.9.0）。
- 呼の確立中（4.7.2.9.2）と、呼を断るとき（4.7.2.9.3）は、MRF の音声ガイダンスを early media で流します（RFC 3261、RFC 3262、RFC 3960、RFC 5009）。Call-Info、Alert-Info、Error-Info で音声の在りかや呼び出し音の種類を示す方法もあります。確立したあと（4.7.2.9.1）は、今の流れで流すか、SDP を交わし直します。
- 発信側の UE（4.7.2.1）は、網から early media が届けば、自分で鳴らす呼び出し音より優先します。最後の P-Early-Media が sendrecv か sendonly の early dialog があれば、自分では鳴らしません。IR.92 2.2.7 も UE にこの動作と P-Early-Media の対応を求めています。
- AS が発信者と複数の early dialog を作るときの手順は、TS 24.628 Annex D にあります。
- つまり AS は、MRFC とは RFC 4240 のとおり 200 と ACK で確立した dialog を持ち、発信者には MRFP の SDP を入れた 183 を `P-Early-Media: sendonly` 付きで送ります。発信者の側はまだ early dialog です。音声ガイダンスが終わったら、AS は発信者に最終応答（規制なら 603）を返します。

### CAT（TS 24.182）

- CAT は着信側の網のサービスですが、発信側にも機能を持てます。発信者の選んだ音が着信者の選んだ音より優先するかは、発信側の網の設定で決まります（4.2.1）。応答したら止めるか、通話中も流し続けるかは、事業者か利用者が決めます。
- モデルは 3 つです。
  - フォークのモデル（4.5.5.3.2）。着信側の AS は INVITE を着信者に送りながら MRF の資源を取り、自分で作った To のタグの信頼性のある 183 を発信者に送ります。183 には P-Early-Media（sendrecv か sendonly）と、`a=content:g.3gpp.cat`（RFC 4796）を付けた MRF の SDP を入れます。着信者の暫定応答は `P-Early-Media: inactive` を付けて中継します。着信者が応答したら MRF を止め、200 を中継します。発信者は 2 つの early dialog を持つことになり、手順は TS 24.628 Annex D に従います。
  - early session のモデル（4.5.5.3.3）。RFC 3959 の early-session を使います。途中のノードと発信側の UE が対応している必要があります（4.3.1.2 NOTE 1）。
  - ゲートウェイのモデル（4.5.5.3.7）。RFC 3960 と TS 24.628 Annex G に従い、AS は MRF の SDP で 18x に答え、応答のあとで UPDATE などの offer で発信者のメディアを着信者に向け直します。RFC 3960 3 は、UA が early media と通常のメディアを区別できない場合にだけ、このモデルを使うよう勧めています。
- 音の選び方。TS 24.182 4.8 は利用者の設定を決めておらず、TS 29.364 7.2.22 にも利用者のデータがありません。音の選択はこのプロジェクトで決めるサービスのデータになります。

### 会議（TS 24.605、TS 24.147）

- 役割の分け方（TS 24.147 4）。会議のポリシー、会議の通知サービス、最上位のフォーカスは AS にあります。MRFC はメディアのフォーカスを持ち、MRFP は mixer とフロア制御のサーバーを持ちます。会議の AS は、第三者呼制御（TS 24.229 5.7.5）とメディア制御（10.2）で会議を作ります（5.2.3）。
- 作り方。UE は会議の factory URI に INVITE を送ります（5.3.1.3.2）。フォーカスは会議の URI を割り当て、Contact に `isfocus` を付けた 200 を返します（5.3.2.3.1）。UE に factory URI が設定されていなければ、既定の `sip:mmtel@conf-factory.<自網のドメイン>` を使います（TS 23.003 V20.1.0 13.10、IR.92 2.3.3）。
- 3 者通話（5.3.1.3.3）。2 本の通話を持つ UE が会議を作り、相手を会議に招き、NOTIFY で相手の参加（sipfrag の 200 と Subscription-State: terminated）を確かめてから、元の通話を BYE で切ります。IR.92 は、UE が会議のフォーカスとのあいだの既存の dialog で REFER を送り、Refer-To に Replaces を入れる 2b を求めています（5.3.1.5.3）。
- 会議のイベントパッケージ。網は dialog の外の SUBSCRIBE と、古い UE のための dialog の中の SUBSCRIBE の両方を受けます。最小の要素は conference-info の entity、maximum-user-count、users、user の entity、display-text、endpoint の entity、status（connected、disconnected、on-hold）です。NOTIFY は 5 秒以内に送るべきです（IR.92 2.3.3）。
- 混ぜるメディアは音声だけで足ります。フロア制御と、リストサーバーの配信の同意は不要です（IR.92 2.3.3）。
- 会議の factory URI は、AS が持つ PSI です。PSI への経路は、HSS が LIA で AS の名前を返す形（TS 29.228 6.1.4.1 step 2）か、サブドメインを DNS で AS に向ける形にできます。

### MRFP の制御 API（Mp）

TS 29.333 の H.248 の型を、このプロジェクトの HTTP の API に写します。既定は独自の HTTP の API にし（[docs/design/media-control.md](../design/media-control.md)）、H.248 は同じ境界の後ろに足せるようにします。

- 接続の型（5.4）。MRFP は IP の一時的なターミネーションを持ち、1 つのコンテキストに複数のターミネーションを入れられます。
- 必須のパッケージ（5.14.1）は g、root、nt、hangterm です。音声ガイダンスは H.248.9 のパッケージ（aasb など）、トーンは tonegen と cg、DTMF の検出は dd で、どれも任意です（5.14.2）。
- コマンド（5.8）は Add、Modify、Subtract、Move、AuditValue、AuditCapabilities、Notify、ServiceChange です。

今の `internal/agw` は、`access` と `core` の 2 つに決めたターミネーションでパケットを転送するだけです。次のように一般化すれば、IMS-AGW、TrGW、MRFP が 1 つの実装を共有できます。

| HTTP | H.248 | 中身 |
| --- | --- | --- |
| `POST /contexts` | Add（$ のコンテキスト） | コンテキストを作る |
| `POST /contexts/{id}/terminations` | Add（$ のターミネーション） | UDP のポートを 1 つ取り、ターミネーションの ID を返す |
| `PUT /contexts/{id}/terminations/{tid}` | Modify（Remote、LocalControl の Mode） | 送り先と向き（sendrecv、sendonly、recvonly、inactive）を決める |
| `PUT /contexts/{id}/terminations/{tid}/signals` | Modify（Signals） | `{"play": {"uri", "repeat", "duration"}}` で流し始める |
| `DELETE /contexts/{id}/terminations/{tid}/signals` | Modify（空の Signals） | 止める |
| MRFP から MRFC の通知用の URL への `POST` | Notify（ObservedEvents） | 再生の終わり。向きを H.248 と同じ MRFP から MRFC にし、キャプチャで見えるようにする |
| `DELETE /contexts/{id}/terminations/{tid}` | Subtract | 1 つ外す |
| `DELETE /contexts/{id}` | Subtract（全部） | コンテキストを消す |

ターミネーションが 2 つなら今と同じくパケットを転送し、3 つ以上なら復号して足し合わせ、自分の音を引いて符号化し直します。P-CSCF の `agw.Client` は同じ変更の中で新しい API に移し、古い API は消します。

### コーデック

- 今は G.711 μ-law（PCMU）だけです（[docs/design/codecs.md](../design/codecs.md)）。`internal/g711` と `internal/wav`（8 kHz、16 ビット）があるので、MRFP は WAV を PCMU にして流し、PCMU を復号して混ぜられます。
- AMR と AMR-WB を使うと、MRFP はそれらで流すか、混ぜる前に復号する必要があります。C のライブラリが要ります。実装の方法は未決の論点です。それまでは MRFP の offer を PCMU だけにします。IR.92 の UE は G.711 を必須にしていない（[profiles.md](profiles.md)）ので、実機の UE とつなぐにはトランスコードが要ります。
- DTMF は、CAT の copy と stop（TS 24.182 4.5.5.2.5、4.5.5.3.6）と IVR の入力の収集に使いますが、IR.92 の必須の付加サービスには要りません。

### 今のコードとの差

- `internal/agw` は 2 つのターミネーションの転送だけで、再生、混ぜ合わせ、向き、通知がありません。
- SIP スタックに REFER、Replaces、NOTIFY の sipfrag がありません。購読する側の部品（`sip/subscription.go`）と S-CSCF の reg イベントの通知（`scscf/regevent.go`）はありますが、会議のイベントパッケージ（RFC 4575）を通知する側の部品はありません。
- PSI の経路がありません（MMTel AS の差と同じ）。
- UE は最終応答を受けてからメディアを始めるので、early media を鳴らしません（`ue/call.go`）。P-Early-Media も扱いません（VoLTE の手順で扱います）。

### 設計の候補

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| MRFC の置き場所 | `ims` コマンドの新しい役割 `mrfc`（役割は設定で切り替える） | MMTel AS と同じ役割にまとめる。Mr' の SIP が見えなくなる |
| MRFP の置き場所 | `internal/agw` を一般化し、IMS-AGW と同じ実装を使う。役割の名前は `mrfp` を加えるか、`agw` のまま設定で機能を選ぶ | MRFP を別に作る。中継の実装が 2 つになる |
| AS が MRFC を動かす方法 | RFC 4240 の netann（`annc`、`conf`）を Mr' で直接送る（TS 24.229 10.2.2.2、10.2.3.2） | RFC 6230 と RFC 6231、RFC 6505。制御チャネルが TCP の別の接続になり、作る量が大きい |
| 音声の在りか | AS が HTTP で配る URI を `play` に入れ、MRFP が取ってきて覚えておく（Cr の資源の取得、TS 24.229 10.2.2.2） | MRFP のイメージに音声を入れ、名前で指す。Cr が見えない |
| 呼の確立前の音声ガイダンス | AS は MRFC との dialog を 200 と ACK で確立し、発信者には 183 と `P-Early-Media: sendonly` を送る（TS 24.628 4.7.2.9.2、4.7.2.9.3、RFC 4240 3） | 確立したあとだけ流す。規制や転送の通知には使えない |
| CAT のモデル | フォークのモデル。2 つの dialog、`a=content:g.3gpp.cat`、P-Early-Media がキャプチャで見え、IR.92 の UE の early media の要件とも合う。UE が複数の early dialog と P-Early-Media に対応する必要がある（VoLTE の手順） | ゲートウェイのモデル。UE に UPDATE が要る。RFC 3960 は勧めていない。メディアを MRFP に通したままにする形は仕様に無い |
| 音の選択 | 独自の Service-Indication の Repository Data に音の URI を置き、XCAP の `simservs` に独自の名前空間の要素で載せる（TS 24.623 6.1、TS 29.364 7.2.0） | 加入者の設定ファイルで決め、Ut を使わない |
| 会議のフォーカス | AS（`mmtel`）が最上位のフォーカスと通知サービスを持ち、MRFC が `conf=` の混ぜ合わせを持つ（TS 24.147 4） | MRFC にすべてを持たせる。仕様の分担と違う |
| 会議の factory URI の経路 | HSS に PSI を置き、LIA で AS の名前を返して I-CSCF が直接送る（TS 29.228 6.1.4.1 step 2） | `conf-factory.<ドメイン>` を DNS で AS に向ける（`SIMPLIFIED:`） |
| 会議の状態（各役割を複数台で動かせるようにするため） | 会議の URI はそれを作った台を指し、参加者と購読は `Store` に置く。MRFC の `conf=` の識別子と、それを持つ MRFP とコンテキストの対応も `Store` に置く | MRB を作る |
| MRFP のコーデック | PCMU だけ。AMR は、実装の方法が決まってから | なし |

## VoLTE の手順との関係

MMTel AS と MRF の機能には、VoLTE の手順（EPC、IMS-AKA、プリコンディション、AMR、P-Early-Media、199、Reason、セッションタイマー、DTMF。[volte.md](volte.md)）に依らずに作れるものと、これを前提にするものがあります。

VoLTE の手順に依らずに作れるもの

- OIP、OIR、TIP、TIR、CB、ACR。どれも INVITE のヘッダと最終応答だけで完結します。
- CDIV のうち CFU、CFB（UE の 486）、CFNRc、CFNL、181 の通知。
- CFNR。B2BUA が発信者の側の dialog を持ち続けるので、発信者は転送先の 18x も同じ dialog で受けます。ただし、着信者の信頼性のある 183 の SDP の answer を発信者に渡してしまうと、転送先の answer を同じ dialog で渡すには UPDATE が要ります。そこで CFNR の規則があるあいだは、AS が着信者の 183 に自分で PRACK を返し、answer を覚えておき、発信者には SDP の無い 180 を送り、200 で answer を渡します。TS 24.182 4.5.5.3.2 の CAT の AS も、暫定応答を中継しないときに同じことをします。代わりに early media は流れません。TS 24.604 4.5.2.6.3 の `P-Early-Media: inactive` は付けますが、UE がそれを解釈するには VoLTE の手順が要ります。CANCEL の `Reason: SIP;cause=408` は、Reason ヘッダを書けるようにすれば付けられます。
- Ut/XCAP、Sh の Repository Data、未登録の着信、ORIGINATING_CDIV、P-Served-User。
- HOLD（re-INVITE の a=sendonly）。SIP スタックの re-INVITE と P-CSCF の re-INVITE の SDP の扱いが要ります。
- CW（端末の方式）。UE に 2 本目の着信と Alert-Info を加えます。
- MRF のうち、確立したあとの音声ガイダンスと会議（PCMU）。

VoLTE の手順を前提にするもの

- early media の音声ガイダンス（規制の前、転送の通知、通話中着信の通知）と CAT。UE が P-Early-Media に従って early media を鳴らし、複数の early dialog と 199 を扱う必要があります（IR.92 2.2.5、2.2.7）。
- プリコンディションを使う呼に B2BUA が入る場合。B2BUA は UPDATE と、SDP を持つ PRACK を中継する必要があります。B2BUA は、dialog の中の要求をメソッドを問わず中継する形で作ると、UPDATE がそのまま通ります。
- セッションタイマー。B2BUA は両側の dialog で別々に Session-Expires を扱います。
- AMR の再生と混ぜ合わせ。

`internal/sip` には、VoLTE の手順が UPDATE、Reason、セッションタイマー、199 を、MMTel AS が re-INVITE、B2BUA、History-Info、Privacy、Alert-Info を、MRF が REFER と Replaces を入れるので、変更が集まります。

## 確かめること（e2e）

`scripts/e2e-mmtel.sh` では、CFU を XCAP で設定し、転送された呼の History-Info を pcap で確かめます。CFB、CFNR、BAIC、OIR も同じ形で確かめます。MRF は `scripts/e2e-mrf.sh` で、音声ガイダンス、会議、CAT の流れを確かめます。

## 決めた設計と、調査時の案との違い

決めた設計は [docs/design/mmtel-mrf.md](../design/mmtel-mrf.md) にあります。調べた時点で論点になったことと、結論は次のとおりです。

1. MMTel AS は、最初から全体を routeing B2BUA として作ります。プロキシから始めると、MRF を加えるときに作り直しになるためです。
2. 発信と着信の見分けは、S-CSCF が入れる P-Served-User にします。AS の URI を分ける方法は使いません。
3. サービスのデータの形は TS 29.364 の `MMTelServices` にし、HSS の Sh の Repository Data にだけ置きます。
4. Ut の認証は、AS が HTTP Digest（RFC 7616）で行い、資格情報は HSS に置きます。GBA は作らず、SIMPLIFIED に書きます。
5. 未登録の着信（HSS、I-CSCF、S-CSCF）を作ります。CFNL と、未登録の利用者への CFU に要るためです。
6. メディア制御の API は、`internal/agw` を N 個のターミネーションを持てるように広げ、H.248 に写せる形にします。MRFP の役割は `mrfp` として足しました。
7. AS から MRFC の制御には、RFC 4240 の netann を使います。
8. CAT のモデルは、調べた時点では未決でした。実装はゲートウェイのモデル（TS 24.182 4.5.5.3.7）です。
9. MWI と ECT の網の側は作りません。IR.92 は、網の対応を任意としているためです。
10. B2BUA の dialog の中の要求は、調べた時点の案（受けた台の URI を Record-Route と Contact に入れて同じ台に戻す）の方向で、最初に INVITE を受けた台に戻します。その台を同じ名前で作り直しても、新しい台は `Store` の呼の記録から呼を組み立て直さず、前のプロセスの呼は切れます（[docs/design/state-redundancy.md](../design/state-redundancy.md)）。

## 国内標準（TTC）

- [profiles.md](profiles.md) の調査で、JJ-90.27（着信転送サービス（CDIV）に関する NNI 仕様、第 10.0 版）を見つけています。網間で転送の情報（History-Info など）をどう運ぶかを決めた文書と見られますが、本文は読めていません。網間接続で、接続先ごとの設定として扱う候補です（[interconnect.md](interconnect.md)）。
- 発信者番号は P-Asserted-Identity で運び、tel URI の PAI は 1 つにします（JJ-90.30 4.3.4.1、[profiles.md](profiles.md)）。OIP と OIR の網間の扱いに関わります。
- 端末と網のあいだ（UNI）の付加サービスを決めた TTC の標準は、見つかりませんでした。
- 調べたとき、ttc.or.jp はメンテナンス中で（503）、Internet Archive にも該当する文書の写しが見つかりませんでした。

## 確かめられなかったこと

- TTC の付加サービスの標準の本文（上の節）。
- TS 24.606 と TS 24.629 は目次しか読んでいません。TS 24.605 は、目次と、ほかのサービスとの関係を書いた 4.6（TIR の 4.6.3、OIR の 4.6.5）を読みました。会議の AS の詳しい手順は TS 24.147 で確かめました。
- RFC 6230、RFC 6231、RFC 6505、RFC 5552 の中身は読んでいません。RFC 4240 を案にしたのは、TS 24.229 10 の挙げる方法のうち新しい接続が要らないためです。
- ITU-T H.248.1 と H.248.9 は読んでいません。Mp の表は TS 29.333 の目次と表 5.14.1、5.14.2 から作りました。
- ETSI TS 183 038 は読んでいないので、GAA を使わない網で Ut に TLS が要る場面は確かめていません。
- TS 33.222、TS 24.109、TS 33.220 は、GBA の範囲を確かめるために題名と参照だけを見ました。
- RFC 8498（P-Served-User の `orig-cdiv`）、RFC 5502、RFC 3891、RFC 3515、RFC 3959、RFC 4796、RFC 5079 は読んでいません。TS 24.229 と TS 24.611 の引用から書きました。
- IR.92 表 2.1 の前の文（TAS が表のすべてに対応する）と、MWI と ECT の節（網は任意）の食い違いを、ほかの資料で確かめていません。
- IR.92 Annex C.3 の設定（XCAP Root URI、Conf_Factory_URI、FromPreferred）の既定値。
- TS 23.003 13.9 の、XCAP Root URI を IMSI から作るときのドメインの形は、読みかけです。
- Wireshark が simservs の XML と History-Info、P-Served-User をどう表示するかは、試していません。
