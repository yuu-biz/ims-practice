# SMS

この記録は SMS over IP をどの役割で運ぶかの設計です。調査の記録は [sms.md](../background/sms.md) にあります。

## 選んだこと

- SMS は `ims` コマンドの役割 `ipsmgw`（IP-SM-GW）と、その中の SC のスタブで運びます。
- IP-SM-GW は Go のインタフェースを通して SC を呼びます。この境界の後ろの実装を差し替えられます。
- S-CSCF が iFC で IP-SM-GW を呼び出します。

| 主題 | 作り |
| --- | --- |
| IP-SM-GW の置き場所 | `ims` コマンドの役割 `ipsmgw` にします。AS の URI は DNS で引くので、複数台で動かせます。 |
| SC | IP-SM-GW と同じプロセスの中のスタブにします。TS 24.341 B.5 の例も、IP-SM-GW が SC に直接届くと仮定しています。SGd、S6c、蓄積と再送は作らず、`SIMPLIFIED:` に書きます。 |
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
| 知らせる引き金 | IP-SM-GW が PUR で登録したときです。これが「IMS に登録したので IP で受け取れる」という知らせにあたります。UNRI を落とし、MWD が空でなく MCEF も落ちていれば ALR を送ります。MCEF が立っている間は送りません。 | TS 23.040 3.2.6 の 2c。TS 23.204 6.5b step 2。 |
| UNRI の扱い | 立っている間、HSS は SRA に serving node を入れません。IP-SM-GW が登録されたままでも入れません。5.2.1.3 が、到達しないと印の付いたノードを外すと定めているためです。登録のときと、短メッセージが届いたときに落とします。 | TS 29.338 5.2.1.3。TS 23.040 3.2.6 が UNRI を、MWD に不在を理由とする項目がある間 TRUE と定めます。 |
| MCEF の落とし方 | 短メッセージが届いたという報告（SM-Delivery-Cause が SUCCESSFUL_TRANSFER の RDR）で落とします。届いた以上、UE にメモリがあり到達もするためです。同時に、残っている SC に ALR を送ります。MCEF が立っていなかったときも送ります。UE が届く状態にあると分かったので、IP-SM-GW が届くようになったと知らせたとき（2c）と同じ扱いにするためです。3.2.6 には IP-SM-GW 経由の配送が成功したときの項目が無いので、MSC 経由（2g）と SGSN 経由（2h）に従います。メモリに空きができた知らせ（2f）は IP-SM-GW が送らないので、この道だけが出口です。 | TS 23.040 3.2.6 の 2g と 2h。TS 29.338 5.2.3.1 が、成功の報告もこの手順で送ると定めます。 |
| 知らせたあと | ALA が返った SC のアドレスを MWD から消します。返らなかったものは残し、次に利用者が戻ったときにもう一度知らせます。設定に Diameter 名が無い SC のアドレスは、通知が決して届かないので消します。 | TS 23.040 3.2.6 の 2c。TS 29.338 5.2.2.2 は、繰り返しの回数と間隔を運用者に任せ、通知が続けて失敗するならアドレスを消すとします。 |
| ALR を送る経路 | SC は HSS に Diameter の接続を張る側なので、その接続を使って送り返します。HSS が S-CSCF へ RTR を送るのと同じ仕組みです。 | － |
| 競合の防ぎ方 | MWD を変えるときは加入者のロックを取り、ロックの下で読み直してから書きます。SRR で serving node が無いと分かったときも、ロックの下でもう一度判断します。 | － |

## 残っている限界

HSS の S6c には、今の構成で残る限界があります。手順書の「このプロジェクトが省いていること」にも、学習者向けに同じことを書いてあります。

- **ALR は、その HSS のインスタンスにつながっている SC にしか届きません。** HSS が S-CSCF へ RTR を送るのと同じで、Destination-Host をエージェントが振り分ける形ではないためです。届かなかったアドレスは MWD に残るので、次に利用者が戻ったときに、その要求に答えたインスタンスが通知します。
- **同じアドレスへの RDR と ALA が重なると、報告が通知に負けることがあります。** RDR がアドレスを入れたあとに、先に始まっていた通知の後始末がそれを消す順序です。SC は自分の再送を持っているので、短メッセージは失われません。
- **PUR を書いたあとノードが落ちると、その通知は送られません。** 通知は PUR の応答を待たせないために、別のゴルーチンで送ります。MWD は残るので、次の登録で通知します。

## SC を境界の後ろに置く理由

SC のスタブを、差し替えられる境界に含めました。インタフェースが 1 つ増えるだけで、あとで SGd に替える道を残せるためです。

## 採らなかった案

ODI の状態をトークンに埋め込む案は採りません。署名の鍵を全台で配る仕組みが要り、学習者がキャプチャで見る値も長くなるためです。
