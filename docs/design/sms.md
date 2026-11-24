# SMS

この記録は SMS over IP をどの役割で運ぶかの設計です。調査の記録は [sms.md](../background/sms.md) にあります。

## 選んだこと

- SMS は `ims` コマンドの役割 `ipsmgw`（IP-SM-GW）と、その中の SC のスタブで運びます。
- IP-SM-GW は Go のインタフェースを通して SC を呼びます。この境界の後ろの実装を差し替えられます。
- S-CSCF が iFC で IP-SM-GW を呼び出します。

| 主題 | 作り |
| --- | --- |
| IP-SM-GW の置き場所 | `ims` コマンドの役割 `ipsmgw` にします。AS の URI は DNS で引くので、複数台で動かせます。 |
| SC | IP-SM-GW と同じプロセスの中のスタブにします。TS 24.341 B.5 の例も、IP-SM-GW が SC に直接届くと仮定しています。スタブには SGd、S6c、蓄積と再送を作らず、`SIMPLIFIED:` に書きます。IP-SM-GW が外の SC から SGd で受ける側は「IP-SM-GW の SGd の作り」にあります。 |
| SC との境界 | IP-SM-GW は Go のインタフェースを通して SC を呼びます。SC は周辺ノードのスタブなので、インタフェースの境界で本格的な実装に差し替えられるようにします。SC を独立した役割にし、SGd と S6c で通信するときは、このインタフェースを Diameter のクライアントで実装します。境界の後ろの実装を替えるだけなので、IP-SM-GW の作りは変わりません。 |
| MT の送り先 | IP-SM-GW は MESSAGE を網の入口の I-CSCF へ送ります。I-CSCF が LIR で S-CSCF を探すので、通話の着信と同じ流れになります。 |
| 受信者の決め方 | SC のスタブが、TP-DA の番号から Sh の UDR で IMPU を引きます。SRI-for-SM の代わりであることを `SIMPLIFIED:` に書きます。 |
| iFC の範囲 | Priority、CNF と DNF、ConditionNegated、SPT の Method、SIPHeader、SessionCase、RequestURI、DefaultHandling、ServiceInfo、第三者 REGISTER に対応します。SessionCase は、未登録の利用者の場合を含めて扱います。SessionDescription と RegistrationType は作らず、`SIMPLIFIED:` に書きます。 |
| ODI の状態 | ODI はランダムな ID にし、続きの評価に要る状態を S-CSCF の `Store` に期限付きで置きます。 |
| IP-SM-GW の状態 | 配達の途中の状態を `Store` の後ろに置きます。どの台が delivery report を受けても処理できます。外部の保存先は [state-redundancy.md](state-redundancy.md) にあります。 |
| 着信側の iFC | ほかの網から届いた短メッセージを、利用者を担当する IP-SM-GW に渡すため、着信側（SessionCase が 1）の MESSAGE を拾う iFC を 1 つ入れます（TS 23.204 6.4a）。P-Asserted-Identity が自網の IP-SM-GW の URI である MESSAGE は、否定の条件で除きます。この条件が無いと、IP-SM-GW が出した MESSAGE が IP-SM-GW に戻るためです（TS 23.218 5.2.3 NOTE 5）。 |
| HSS への登録 | IP-SM-GW は第三者 REGISTER を受けると、Sh の PUR で HSS に自分のアドレスを登録します。MAP は使いません。 |
| HSS の S6c | HSS は SMS の中心の機能（SMS-GMSC と SMS-IWMSC）と S6c で話します。SRR に答え、RDR で message waiting data を書き換え、利用者が受け取れるようになったら ALR で知らせます。 |
| 文字の符号化 | GSM 7 ビットの既定アルファベットと UCS2 に対応します。連結 SMS（UDH）と有効期間は作らず、`SIMPLIFIED:` に書きます。 |

## HSS の S6c の作り

標準の道では、SC は配送のたびに HSS へ SRR を送り、届かなかったときは RDR を送ります。HSS は、宛先が受け取れるようになったら ALR で SC に知らせます。この 3 つを HSS に入れるにあたって決めたことです。

| 主題 | 作り | 根拠 |
| --- | --- | --- |
| serving node の返し方 | HSS は SRA の Serving-Node に IP-SM-GW を入れて自分で答えます。SRR を IP-SM-GW に回して、IP-SM-GW が自分のアドレスと MT Correlation ID を返す形は採りません。回すのは、ほかの網から来た短メッセージを宛先のホーム網を通して配送させるためで、このプロジェクトはそれを行いません。 | TS 29.338 Table 5.2.1.1-2 は、Serving-Node に IP-SM-GW の Diameter 名、realm、ISDN 番号を入れられるとします。TS 29.338 5.2.1.1 は、回す形が宛先のホーム網を通す強制のためだとします。TS 23.204 6.4 step 3a から 3c が回す形です。 |
| IP-SM-GW の Diameter 名の入手 | PUR の Origin-Host と Origin-Realm から覚えます。Sh-Data の中には Diameter 名の要素がありません。覚えるのは、IP-SM-GW が S6c に対応していると示したときだけです。 | TS 29.328 6.1.2.1 step 4b。Annex D の tSMSRegistrationInfo は IP-SM-GW-Number、SCAddress、SBI と S6c の対応の表示だけを持ちます。 |
| ALR の宛先 | 設定の `hss.service_centres` で、Service Centre のアドレス（E.164）ごとに `host`（Diameter 名）と `realm` を持ちます。DNS は使いません。このリポジトリのノードの解決は設定で統一されているためです。`host` は、SC が CER の Origin-Host に入れる名前と同じでなければなりません。HSS はその名前で接続を探すためです。同じアドレスを 2 回書いた設定は読み込みのときに断ります。 | TS 29.338 5.3.1.2 は、HSS が message waiting data に SMS-SC の名前とドメイン名を持つなら、Destination-Host と Destination-Realm を入れるとします。 |
| message waiting data の置き場所 | 加入者のレコードの中に、SC のアドレスの一覧と UNRI、MCEF を置きます。全加入者を走査する処理は入れません。 | TS 23.040 3.2.6 が MWD、UNRI、MCEF を定めます。 |
| 知らせる引き金 | 4 つあります。IP-SM-GW が PUR で登録したとき、UE がまた届くようになった知らせ、メモリに空きができた知らせ、短メッセージが届いたという報告です。登録のときは UNRI を落とし、MWD が空でなく MCEF も落ちていれば ALR を送ります。MCEF が立っている間は送りません。あとの 2 つは「Sh の PUR の拡張」にあります。 | TS 23.040 3.2.6 の 2c。TS 23.204 6.5b step 2。 |
| UNRI の扱い | 立っている間、HSS は SRA に serving node を入れません。IP-SM-GW が登録されたままでも入れません。5.2.1.3 が、到達しないと印の付いたノードを外すと定めているためです。登録のとき、届くようになった知らせとメモリの知らせ、短メッセージが届いたときに落とします。 | TS 29.338 5.2.1.3。TS 23.040 3.2.6 が UNRI を、MWD に不在を理由とする項目がある間 TRUE と定めます。 |
| MCEF の落とし方 | メモリに空きができた知らせ（2f）と、短メッセージが届いたという報告（SM-Delivery-Cause が SUCCESSFUL_TRANSFER の RDR）で落とします。届いた以上、UE にメモリがあり到達もするためです。報告のときは、残っている SC に ALR を送ります。MCEF が立っていなかったときも送ります。UE が届く状態にあると分かったので、IP-SM-GW が届くようになったと知らせたとき（2c）と同じ扱いにするためです。3.2.6 には IP-SM-GW 経由の配送が成功したときの項目が無いので、MSC 経由（2g）と SGSN 経由（2h）に従います。 | TS 23.040 3.2.6 の 2f、2g と 2h。TS 29.338 5.2.3.1 が、成功の報告もこの手順で送ると定めます。 |
| 知らせたあと | ALA が返った SC のアドレスを MWD から消します。返らなかったものは残し、次に利用者が戻ったときにもう一度知らせます。設定に Diameter 名が無い SC のアドレスは、通知が決して届かないので消します。 | TS 23.040 3.2.6 の 2c。TS 29.338 5.2.2.2 は、繰り返しの回数と間隔を運用者に任せ、通知が続けて失敗するならアドレスを消すとします。 |
| ALR を送る経路 | SC は HSS に Diameter の接続を張る側なので、その接続を使って送り返します。HSS が S-CSCF へ RTR を送るのと同じ仕組みです。 | － |
| 競合の防ぎ方 | MWD を変えるときは加入者のロックを取り、ロックの下で読み直してから書きます。SRR で serving node が無いと分かったときも、ロックの下でもう一度判断します。 | － |

## Sh の PUR の拡張（ReadyForSM）

UE にまたショートメッセージが届くようになったことと、UE のメモリに空きができたことを IP-SM-GW から HSS へ知らせる手段は、Sh にありません。標準ではこれを MAP の READY_FOR_SM が運びます。このプロジェクトは MAP を話さないので、Sh の PUR に要素を 1 つ足して運びます。

**この要素は 3GPP の Sh の仕様には無く、このプロジェクトが独自に決めたものです。** 標準に準拠した知らせ方ではありません。コードの `SIMPLIFIED:` と、手順書の「このプロジェクトが省いていること」の表にも、同じことを書いてあります。

| 主題 | 作り | 根拠 |
| --- | --- | --- |
| 標準の動き | IP-SM-GW は MAP の READY_FOR_SM で HLR に知らせます。alert reason は ms-Present と memoryAvailable の 2 つで、前者は「message waiting flag が立っている利用者に IMS で届くようになった」、後者は「利用者がメモリの空きを示した」です。HLR は MWD の SC に ALR を送ります。 | TS 29.002 12.4 が、IP-SM-GW と HLR のあいだでもこのサービスを使うと定めます。alert reason の値は 7.6.8.8 と ASN.1 の AlertReason です。TS 23.204 6.5b step 2d（届くようになった知らせ）と 6.6 step 3（メモリの知らせ）。 |
| 運ぶ場所 | Data-Reference が SMSRegistrationInfo（24）の PUR の、Sh-Data の `SMSRegistrationInfo` の最後の子に置きます。新しいコマンドも新しい Data-Reference も作りません。 | TS 29.328 Annex D の tSMSRegistrationInfo。 |
| 名前空間と要素 | 名前空間は `https://github.com/yuu-biz/ims-practice/ns/sh`（このリポジトリの URL に `/ns/sh` を足したもの）、要素は `ReadyForSM`、値は `msPresent` と `memoryAvailable` です。値の名前は MAP の alert reason にならいました（MAP の綴りは `ms-Present` です）。標準の要素と取り違えないよう、名前空間にプロジェクトの URL を入れます。 | － |
| 標準の HSS から見た姿 | tSMSRegistrationInfo は `xs:any namespace="##other" processContents="lax"` で終わります。ほかの名前空間の要素を 1 つ置ける場所がそこにあり、lax なので、名前空間を知らない HSS は中身を調べずに読み飛ばします。XSD での検査も通ります。S6c の対応を示す `Extension3` には拡張の場所が無いので、そちらには置けません。 | TS 29.328 Annex D。 |
| 登録の更新との区別 | この要素がある PUR は、知らせとしてだけ扱います。IP-SM-GW の番号も Diameter の名前も書き換えません。PUR の番号が、保存されている IP-SM-GW の番号と同じときだけ動きます。違えば 2001 で答え、何もしません（そのことはログに残します）。この要素が無い PUR は、これまでどおり登録の更新です。 | TS 23.204 6.5b step 2d は、UE が IMS に登録していなければ IP-SM-GW 自身が知らせを捨てるとします。HSS でも確かめるのは、知らせと応答のあいだに登録が外れることがあるためです。何も変えずに成功を返すのは、TS 23.040 3.2.6 の 2i にならいます。 |
| 重複した知らせ | 加入者のロックの下で印を読み直し、その理由の印を落とします。MWD に SC が残っていれば、印がすでに落ちていても ALR を送ります（`msPresent` は MCEF が落ちているときだけです）。SC が ALR を断ったときや、ALR が届かなかったときは、その SC のアドレスが MWD に残ります。そのため、次の知らせで送り直せなければなりません。重複した知らせや同時に届いた知らせは、SC に ALR を重ねて送ることがあります。ALR は SC に配達を試させるだけなので、重なっても害はありません。MWD が空なら、何もせずに応答だけ返します。 | TS 23.040 3.2.6 の 2c、2f、2i。 |
| 印の落とし方 | `msPresent` は UNRI を落とし、MWD が空でなく MCEF も落ちていれば ALR を送ります（2c）。`memoryAvailable` は MCEF と UNRI を落とし、MWD が空でなければ ALR を送ります。2f は UNRI を落とすとだけ書き、MCEF には触れていませんが、MSC 経由（2d）と SGSN 経由（2e）は同じ知らせで MCEF を落とすので、それに合わせます。 | TS 23.040 3.2.6 の 2c、2d、2e、2f。 |
| 知らない値が来たとき | DIAMETER_UNABLE_TO_COMPLY で答えます。この HSS が扱える理由は 2 つだけで、ほかの値では何をすればよいか決まらないためです。 | － |
| XSD を置かないこと | 3GPP の文書の再配布の条件を確かめられないので、`ShDataType_Rel20.xsd` の原本はリポジトリに置きません。代わりに、決まった文字列と比べる試験で、要素の位置と名前空間を確かめます。 | AGENTS.md の「仕様は公開資料だけを使う」。 |

## IP-SM-GW の SGd の作り

標準の道では、SC は SRA で得たノードに TFR を送り、ノードは配達が終わってから TFA を返します。この受け側を IP-SM-GW に入れるにあたって決めたことです。TS 23.204 5.2.4 は、IP-SM-GW がこの経路で SMS-GMSC から見て serving node に見えるとしています。

| 主題 | 作り | 根拠 |
| --- | --- | --- |
| 宛先の見つけ方 | 登録のときに Sh の UDR（Data-Reference 32）で IMSI を読み、reg イベントの購読に入れます。IMSI から購読を引く索引を Store に置き、TFR の User-Name でそれを引きます。全加入者を走査する処理は入れません。 | TS 29.328 7.6.24。TS 29.338 Table 6.2.2.1/1 が SM RP DA を User-Name の IMSI とします。 |
| 読む順 | IMSI を読み、IMSI と「短いメッセージを受け取る」ことを購読の記録に書いてから、PUR を送ります。HSS は PUR を受けると SC に通知するので、IP-SM-GW が PUR の答えを受け取る前に TFR が来ることがあります。そのときも、利用者を見つけて配達できるようにするためです。PUR が失敗したら、受け取ることを書き戻し、次の NOTIFY でもう一度 HSS に伝えます。 | TS 29.328 6.1.2.1 step 4b。 |
| IMSI を索引にする理由 | 索引を購読に向けると、IMSI から MSISDN と公開識別子、受け取れるかどうかまで 1 回で引けます。登録が解けると購読と一緒に消えるので、落とし忘れもありません。 | － |
| 配達の宛先 | IMSI に結び付いた tel URI を Request-URI にします。MSISDN が無ければ公開識別子を使います。 | TS 23.204 6.4 step 6。 |
| 登録のない IMSI | DIAMETER_ERROR_USER_UNKNOWN で答えます。HSS がこの IP-SM-GW を教えるのは登録した利用者だけなので、知らない IMSI は解けた登録を意味します。 | TS 29.338 6.2.2.2 が、IMSI を知らない serving node にこれを返させます。 |
| 受け取れない利用者 | 登録はあるが `+g.3gpp.smsip` の連絡先が無いときは、MESSAGE を送らずに DIAMETER_ERROR_ABSENT_USER で答え、自分の UNRI を立てます。 | TS 23.204 6.5b。 |
| TFA を待たせる場所 | 配達の結果を Store に置き、TFR を持つ台が取りに行きます。鍵は、TFR の Session-Id に、その要求ごとの tag を足したものです。同じ session で TFR が送り直されても、それぞれが自分の配達の結果を待ち、前の結果を取りません。delivery report がどの台に届くかは分からないので、結果を出す台と TFA を返す台は別になります。結果を読めなかったときは、期限まで読み直します。 | RFC 6733 8.8 が Session-Id を 1 つの session の名前とします。TS 23.204 6.5 step 3 は同じ台に届くことを求めますが、DNS はそれを約束しません。送り直された TFR を重複として見分けないことは、`SIMPLIFIED:` に書いています（RFC 6733 3 の End-to-End Identifier と Appendix C）。 |
| 待ち方 | Store を一定の間隔で見に行きます。結果を取れるのは 1 台だけなので、TFA は 1 回です。 | － |
| 待つ時間 | TR1N に、期限切れを拾う掃除の間隔と 1 秒を足した長さです。報告が来なかった配達を終わらせるのは掃除の回なので、その回が来るまでを見込みます。SMS-GMSC のタイマーに合わせて短くはしません。 | TS 24.011 6.2.2.2。TS 23.204 6.5a NOTE 1 が、足りなければ SMS-GMSC のタイマーを延ばすとします。 |
| System Failure の結果コード | TS 29.338 に当たるものが無いので、基本プロトコルの DIAMETER_UNABLE_TO_COMPLY を使います。要求を読めなかったときと、結果が分からないままになったときも同じです。 | TS 29.338 7.3.1 が、この文書の結果コードの横で基本プロトコルの permanent failure も使うとします。RFC 6733 7.1.5。 |
| 運用から外した台 | TFR を DIAMETER_TOO_BUSY で断ります。配達はまだ何も始まっていないので、ほかの台が丸ごと引き取れます。 | RFC 6733 7.1.3 が、送り手に同じ realm のほかのノードへ持っていかせます。 |
| UNRI の置き場所 | IP-SM-GW 自身の Store に、公開識別子ごとに置きます。登録より長く残るので、reg イベントの購読の中には入れません。期限は 72 時間で、SC がショートメッセージを抱えていられる長さに合わせました。期限を決めたのは、保存先が期限の無い項目を持たないためです。 | TS 23.040 2.1.1 が UNRI を IP-SM-GW と HSS の両方に置くものと定めます。3.2.6。 |
| UNRI を落とす引き金 | 登録（PUR）のときは落とすだけで、知らせは送りません。PUR 自体が 2c の知らせだからです。利用者からの SIP 要求のときは落として `msPresent` を送ります。印を取れるのは 1 台だけなので、知らせも 1 回です。利用者が登録していなければ捨てます。知らせを HSS が受け取らなかったときは印を戻し、次の要求でもう一度試します。 | TS 23.040 3.2.6 の 2c。TS 23.204 6.5b step 2 と 2d。 |
| メモリの知らせ | RP-SMMA を受けたら `memoryAvailable` を HSS に送り、そのあとで UE に RP-ACK を返します。送れなかったときは RP-Cause 41 を返すので、UE は知らせを送るべきものとして持ち続けます。 | TS 24.341 5.3.3.4.1 の手順 d と e。TS 23.204 6.6。TS 24.011 Table 8.4 part 1。 |
| 配送の報告を送らないこと | IP-SM-GW は HSS に RDR を送らず、UE がまた届くようになる 1 回限りの通知も申し込みません。同じ失敗を SMS-GMSC が報告し、HSS はそれで MWD を書きます。UE が戻ったことは、もともと持っている reg イベントの購読で分かります。 | TS 23.204 6.5a step 17 と 18。`SIMPLIFIED:` にも書いています。 |
| ドメイン選択 | IMS だけに配達します。ほかのドメインのノードがこのプロジェクトに無いためです。 | TS 23.204 6.4 step 5 と 6.5a。`SIMPLIFIED:` にも書いています。 |

TFA の結果コードは次のとおりです。左の列は配達の結果で、SIP の応答コードから TS 29.311 Table 6.1.4.4.1.1 で写した MAP の user error か、UE の RP-ERROR の原因です。右の列は、同じことを表す TS 29.338 7.3 の結果コードです。

| 配達の結果 | TFA |
| --- | --- |
| 届いた（RP-ACK） | DIAMETER_SUCCESS（2001） |
| Absent Subscriber SM（480） | DIAMETER_ERROR_ABSENT_USER（5550）。自分の UNRI も立てます |
| Unidentified subscriber（404、604） | DIAMETER_ERROR_USER_UNKNOWN（5001） |
| Subscriber busy for MT SMS（486、600、603） | DIAMETER_ERROR_USER_BUSY_FOR_MT_SMS（5551） |
| Illegal Subscriber（401、407） | DIAMETER_ERROR_ILLEGAL_USER（5553） |
| メモリ不足（RP-Cause 22） | DIAMETER_ERROR_SM_DELIVERY_FAILURE（5555）と SM-Delivery-Failure-Cause の MEMORY_CAPACITY_EXCEEDED |
| そのほかの RP-ERROR | DIAMETER_ERROR_SM_DELIVERY_FAILURE（5555）と EQUIPMENT_PROTOCOL_ERROR |
| System Failure（そのほかの応答コード、応答が無い、報告が来ない） | DIAMETER_UNABLE_TO_COMPLY（5012） |

## 残っている限界

HSS の S6c には、今の構成で残る限界があります。手順書の「このプロジェクトが省いていること」にも、学習者向けに同じことを書いてあります。

- **ALR は、その HSS のインスタンスにつながっている SC にしか届きません。** HSS が S-CSCF へ RTR を送るのと同じで、Destination-Host をエージェントが振り分ける形ではないためです。届かなかったアドレスは MWD に残るので、次に利用者が戻ったときに、その要求に答えたインスタンスが通知します。
- **同じアドレスへの RDR と ALA が重なると、報告が通知に負けることがあります。** RDR がアドレスを入れたあとに、先に始まっていた通知の後始末がそれを消す順序です。SC は自分の再送を持っているので、短メッセージは失われません。
- **PUR を書いたあとノードが落ちると、その通知は送られません。** 通知は PUR の応答を待たせないために、別のゴルーチンで送ります。MWD は残るので、次の登録で通知します。

## SC を境界の後ろに置く理由

SC のスタブを、差し替えられる境界に含めました。インタフェースが 1 つ増えるだけで、あとで SGd に替える道を残せるためです。

## 採らなかった案

ODI の状態をトークンに埋め込む案は採りません。署名の鍵を全台で配る仕組みが要り、学習者がキャプチャで見る値も長くなるためです。
