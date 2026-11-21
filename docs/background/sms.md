# SMS over IP、iFC、IP-SM-GW、Sh の調査

この文書は、SMS over IP と、それに関わる iFC、IP-SM-GW、Sh について、仕様が定めることと、この実装の作りを調べた記録です。決めた設計は [docs/design/sms.md](../design/sms.md) にあります。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは版も書きます。

## 読んだ資料

| 仕様 | 版 |
| --- | --- |
| TS 23.204（SMS over IP、Stage 2） | V19.1.0 |
| TS 24.341（SMS over IP、Stage 3） | V19.0.0 |
| TS 24.011（RP 層） | V20.0.0 |
| TS 23.040（TP 層） | V20.0.0 |
| TS 23.038（文字の符号化） | V20.0.0 |
| TS 29.228（iFC の XML、Sh のデータ） | V20.0.0 |
| TS 24.229 | V20.1.0 |
| TS 23.218 | V19.0.0 |
| TS 29.328、TS 29.329（Sh） | V20.0.0、V19.1.0 |
| TS 29.338（SGd、S6c） | V19.3.0 |
| GSMA IR.92 | v24.0 |

Wireshark はソース（master、release-4.2、release-3.6）を読みました。実行はしていません。

## MO の流れ（UE-A が送る）

根拠は TS 24.341 5.3.1.2、5.3.3.4.1、B.5 と、TS 23.204 6.3 です。

1. UE-A が MESSAGE を送ります。Request-URI と To は SC の PSI、From は送信者の IMPU、Content-Type は `application/vnd.3gpp.sms` です。本文は RP-DATA（MS→N）で、中に SMS-SUBMIT が入ります。UE は Call-ID を覚えておきます。
2. S-CSCF は発信側の iFC が一致するので、Route を「AS の URI、ODI 付きの自分の URI」にして IP-SM-GW へ送ります（TS 24.229 5.4.3.2 step 5）。このとき tel URI の P-Asserted-Identity を足します（step 9a）。
3. IP-SM-GW は終端の UA として 202 を返します（TS 24.229 5.7.2）。要求を S-CSCF に戻しません。
4. IP-SM-GW は TPDU を SC に渡します。送信者の E.164 番号は tel URI の P-Asserted-Identity から取ります。
5. SC が受け付けると、IP-SM-GW は新しい MESSAGE で submit report を UE-A に送ります（TS 24.341 5.3.3.4.3）。`Accept-Contact: *;+g.3gpp.smsip;require;explicit`、`Request-Disposition: fork`、元の Call-ID を入れた `In-Reply-To` を付け、本文は RP-ACK か RP-ERROR です。UE-A は 200 を返します。

## MT の流れ（UE-B が受ける）

根拠は TS 24.341 5.3.3.4.2、5.3.2.4、B.6 と、TS 23.204 6.4、6.5 です。

1. IP-SM-GW は MESSAGE を UE-B の IMPU 宛てに送ります。同じ Accept-Contact と `Request-Disposition: no-fork` を付け、本文は RP-DATA（N→MS）で、中に SMS-DELIVER が入ります。
2. S-CSCF は RFC 3841 の caller preferences で連絡先を選びます（TS 24.229 5.4.3.3 step 9）。
3. UE-B は 200 を返します。これはまだ delivery report ではありません。
4. UE-B は新しい MESSAGE で delivery report を IP-SM-GW に送ります。宛先は受けた MESSAGE の P-Asserted-Identity から取り、`In-Reply-To` に受けた Call-ID を入れます。本文は RP-ACK（SMS-DELIVER-REPORT）か RP-ERROR です。
5. UE-B の S-CSCF はこの MESSAGE を iFC で IP-SM-GW に送ります。delivery report は SM を送ったのと同じ IP-SM-GW に届く必要があります（TS 23.204 6.5 step 3）。
6. TP-SRR が立っていれば、SC は SMS-STATUS-REPORT を作り、IP-SM-GW が RP-DATA（N→MS）で UE-A に送ります（TS 24.341 5.3.3.4.4）。

## 登録

根拠は TS 23.204 6.1、TS 24.341 5.3.3.2、B.3 です。

- UE は Contact に `+g.3gpp.smsip` を付けます（TS 24.341 5.3.2.2）。IR.92 でも必須です。
- REGISTER に一致する iFC で、S-CSCF は IP-SM-GW へ第三者 REGISTER を送ります（TS 24.229 5.4.1.7）。iFC の ServiceInfo に入れた MSISDN が、本文の `application/3gpp-ims+xml` で届きます（5.4.1.7A）。
- IP-SM-GW は 200 を返し、reg イベントを購読し、HSS に「この MSISDN は IP-SM-GW 経由で受けられる」と登録します。Diameter では Sh の PUR を使います。

## IP-SM-GW と SC

- IP-SM-GW は E、Gd、Gdd、SGd で SMS-IWMSC や SMS-GMSC につなぎます（TS 23.204 5.2.4）。Diameter では SGd（TS 29.338 6.2）の OFR と TFR、経路の問い合わせは S6c の SRR です。
- TS 24.341 B.5 は、例の中で IP-SM-GW が SC に直接届くと仮定しています。これを根拠に、SC を IP-SM-GW の中のスタブにできます。その場合は、SGd、S6c、SRI-for-SM、CS と PS の振り分け、蓄積と再送を省いたことを `SIMPLIFIED:` に書きます。

## 本文の RP 層と TP 層

- RP 層（TS 24.011 V20.0.0）
  - 1 オクテット目の下位 3 ビットが MTI です（8.2.2）。0 と 1 が RP-DATA（MS→N と N→MS）、2 と 3 が RP-ACK、4 と 5 が RP-ERROR、6 が RP-SMMA です。
  - 次が Message Reference です（8.2.3）。RP-ACK と RP-ERROR を元の要求に対応づけます。
  - RP-DATA は MTI、MR、RP-OA、RP-DA、RP-User-Data の順です。MS→N では RP-OA が空で RP-DA が SC のアドレス、N→MS ではその逆です。アドレスの長さはオクテット数です（8.2.5.1、8.2.5.2）。
  - RP-ERROR は RP-Cause が必須です（8.2.5.4）。
- TP 層（TS 23.040 V20.0.0）
  - SMS-SUBMIT（9.2.2.2）、SMS-DELIVER（9.2.2.1）、SMS-SUBMIT-REPORT（9.2.2.2a）、SMS-DELIVER-REPORT（9.2.2.1a）、SMS-STATUS-REPORT（9.2.2.3）。
  - TP のアドレスの長さは「意味のある半オクテットの数」です（9.1.2.5）。RP のアドレスと数え方が違うので、取り違えやすいところです。
  - TP-UDL は 7 ビットなら septet の数、UCS2 ならオクテットの数です（9.2.3.16）。
- 符号化（TS 23.038 V20.0.0）
  - DCS の 0x00 が GSM 7 ビットの既定アルファベット、0x08 が UCS2 です（4 章）。7 ビットの詰め方は 6.1.2.1.1、文字表は 6.2.1 です。
- テキストの SMS に最小限要るもの。RP-DATA、SUBMIT、DELIVER、RP-ACK です。SUBMIT-REPORT と DELIVER-REPORT は、学習のために付けます。連結 SMS（UDH）と有効期間は後回しにできます。

## iFC と S-CSCF

- iFC の XML は TS 29.228 の Annex B.2.2、B.2.3（説明）、Annex C（例）、Annex E（スキーマ）、Annex F（照合の規則）にあります。
  - `InitialFilterCriteria` は `Priority`、`TriggerPoint`（`ConditionTypeCNF` と `SPT`）、`ApplicationServer`（`ServerName`、`DefaultHandling`、`ServiceInfo`）を持ちます。
  - SPT は `RequestURI`、`Method`、`SIPHeader`、`SessionCase`、`SessionDescription` のどれか 1 つを持ちます。SessionCase の 0 は ORIGINATING_REGISTERED、1 は TERMINATING_REGISTERED です。
- SMS で使う iFC
  - MO 用。`Method=MESSAGE` かつ Content-Type が `application/vnd.3gpp.sms` かつ `SessionCase=0`。
  - 第三者 REGISTER 用。`Method=REGISTER`。ServiceInfo に MSISDN を入れます。
  - 着信側（SessionCase=1）の MESSAGE を拾う iFC は入れません。IP-SM-GW が出した MT の MESSAGE が IP-SM-GW に戻り、ループするためです（TS 23.218 5.2.3 NOTE 5）。ほかの網から届く短メッセージのためには、IP-SM-GW 自身の MESSAGE を否定の条件で除いた着信側の iFC を入れます（[docs/design/sms.md](../design/sms.md)）。
- S-CSCF の手順（TS 24.229 V20.1.0）
  - 発信側は 5.4.3.2、着信側は 5.4.3.3 です。Route の先頭に自分の ODI が無ければ iFC の一覧を作り、ODI があればその続きから評価します。
  - AS が応答しないときは DefaultHandling に従います。
  - ODI（5.4.3.4）から処理を続けるために、S-CSCF は served user、発信か着信か、iFC の一覧と次の位置、着信側なら保存した Request-URI、icid を覚えておく必要があります。今の `session()` は Route の user part の `orig` で発信と着信を分けているので、ODI にもこれを持たせる必要があります。
  - AS が自分で出す要求（TS 24.229 5.7.3）は、Request-URI を served user、P-Asserted-Identity を AS の URI にし、登録先の S-CSCF か網の入口（I-CSCF）へ送ります。

## Sh（TS 29.328 V20.0.0、TS 29.329 V19.1.0）

- Application-ID は 16777217 です。コマンドは UDR/UDA 306、PUR/PUA 307、SNR/SNA 308、PNR/PNA 309 です。
- IP-SM-GW の登録は PUR に Data-Reference 24（SMSRegistrationInfo）を入れて送ります（TS 29.328 6.1.2）。
- UDR では MSISDN（17）、IMSPublicIdentity（10、MSISDN から IMPU を引ける）、S-CSCFName（12）を引けます。

## Wireshark

- `packet-gsm_a_rp.c` が `application/vnd.3gpp.sms` を media_type の表に登録しています。SIP の本文は RP の dissector に渡り、その中の TPDU は `gsm_sms` に渡ります。設定は要らない見込みです。
- フィルタの例。`gsm_a.rp.msg_type`、`gsm_sms.tp-mti`、`gsm_sms.tp-da`、`gsm_sms.sms_text`。
- 実際にデコードできるかは、SMS の e2e（`scripts/e2e-sms.sh`）で確かめます。

## IR.92 v24.0 の SMS の必須の要件（6 章）

- UE は SMS over IP の送信と受信の役割を持ちます。status report、delivery report、memory available の通知（RP-SMMA）は、UE と網の両方に必須です。
- 網は IP-SM-GW の役割を持ちます。経路の問い合わせ（TS 24.341 5.3.3.3）と、transport layer interworking（5.3.3.4）が必須です。

## 今のコードとの差

この節は、SMS の実装を始める前のコードとの差を記録したものです。ここに挙げた差は、どれも埋めました。

- `internal/cx/userdata.go` のユーザプロファイルに iFC がありません。S-CSCF はプロファイルを保存していません。
- `sip` に MESSAGE がありません（RFC 3428）。
- S-CSCF は Contact のパラメータを捨てているので、RFC 3841 の照合と、reg イベントの `<unknown-param>` ができません。tel URI の P-Asserted-Identity も足していません。
- reg イベントの購読は本人と Path にしか許していません。TS 24.229 5.4.2.1.1 は、iFC に載っている AS にも許しています。

## 設計の選択

決めた設計は [docs/design/sms.md](../design/sms.md) にあります。ここには、選んだものと、検討したほかの案を並べます。

| 決めること | 選んだもの | ほかの案 |
| --- | --- | --- |
| IP-SM-GW の置き場所 | `cmd/ims` の新しい役割 `ipsmgw`（役割は設定で切り替える）。複数台で動かせる | なし |
| SC | `ipsmgw` の中の別パッケージのスタブ（TS 24.341 B.5 の仮定）。SGd と S6c は `SIMPLIFIED:` | 役割 `smsc` を別に作り、SGd と S6c を実装する（SMS over NAS で扱う） |
| MT の送り先 | I-CSCF（網の入口）へ送り、LIR で S-CSCF を探す。今の着信の流れをそのまま使える | 第三者 REGISTER で知った S-CSCF へ直接送る。Sh の S-CSCFName で引く |
| 受信者の決め方 | SC スタブが TP-DA の MSISDN から Sh UDR で IMPU を引く。SRI-for-SM の代わりであることを `SIMPLIFIED:` に書く | なし |
| iFC の範囲 | Priority、CNF と DNF、ConditionNegated、SPT の Method、SIPHeader、SessionCase（0 と 1）、RequestURI、DefaultHandling、ServiceInfo、第三者 REGISTER | SessionDescription、RegistrationType、未登録の場合などは後回しにして `SIMPLIFIED:` |
| ODI の状態（各役割を複数台で動かせるようにするため） | ランダムな ID にし、状態を S-CSCF の Store に期限付きで置く | 状態をトークンに埋め込み、HMAC で守る |
| IP-SM-GW の状態 | すべて Store の境界の向こうに置き、どの台が delivery report を受けても処理できるようにする | なし |

SC のスタブは、HSS とアクセス網のスタブと同じく、差し替えられる境界に含めます。

## 実装で決めたこと（IP-SM-GW と SC）

IP-SM-GW と SC のスタブを作るときに、仕様の読み方を次のように決めました。

- 登録。IP-SM-GW は第三者 REGISTER に 200 を返し、service-info の MSISDN を覚えて、reg イベントを購読します（TS 24.341 5.3.3.2、TS 24.229 5.7.1.1）。HSS に PUR を送るのは、`+g.3gpp.smsip` を付けた連絡先があるかどうかが NOTIFY で変わったときです。第三者 REGISTER では HSS を更新しません。購読の状態は Store に置くので、NOTIFY はどの台が受けても処理できます。購読の期間は、登録の期間に RFC 3680 4.4 の 161 秒を足した長さにします。再登録の第三者 REGISTER が届くたびにダイアログの中で購読を更新するので、購読のためのタイマーは要りません。登録が終わると、S-CSCF が NOTIFY で購読を終わらせます。PUR の User-Identity は service-info の MSISDN で、MSISDN が無いときだけ To の公開識別子にします。
- 送り先。IP-SM-GW が出す MESSAGE は Route を付けず、ホームドメインを DNS で引いて見つけた I-CSCF へ送ります。submit report は MO の MESSAGE の 1 つ目の P-Asserted-Identity へ送り、SMS-DELIVER と status report は SC が Sh で引いた tel URI へ送ります（TS 23.204 6.4 step 6）。
- 状態。配達の途中の状態（MESSAGE の Call-ID をキーにした配達と、RP-MR の数）は IP-SM-GW の Store に置き、status report に要る値は SC の Store に置きます。delivery report を受けた台が配達を Store から取り出して終わらせるので、MESSAGE を送った台と別の台でも処理できます。delivery report を待つタイマー（TR1N、64 秒）だけは、送った台のプロセスの中で動きます。

誤りのときに返す値は次の表のとおりです。

| 場合 | 決める部品 | 返す値 | 根拠 |
| --- | --- | --- | --- |
| RP-DATA か RP-SMMA を読めない | IP-SM-GW | RP-ERROR（RP-Cause 96） | TS 24.341 5.3.3.4.1、TS 24.011 9.3.4 |
| RP-MTI が予約の値 | IP-SM-GW | RP-ERROR（RP-Cause 97） | TS 24.011 9.3.3 |
| UE から N→MS の RP-DATA が来た | IP-SM-GW | RP-ERROR（RP-Cause 98） | TS 24.011 9.3.3 |
| RP-DA の番号計画が E.164 でない | IP-SM-GW | RP-ERROR（RP-Cause 96） | TS 23.040 9.1.2.5 |
| RP-DA が設定した SC の番号と違う | IP-SM-GW | RP-ERROR（RP-Cause 21） | TS 24.011 に決まりがないので、このプロジェクトで決めた |
| SMS-SUBMIT を読めない | IP-SM-GW | RP-ERROR（RP-Cause 96） | TS 24.011 Table 8.4 part 1 |
| P-Asserted-Identity に tel URI がない | IP-SM-GW | RP-ERROR（RP-Cause 50） | MSISDN のない利用者として扱う |
| TP-DA の番号を持つ利用者がいない、または TP-DA が国際番号でない | SC | RP-ERROR（RP-Cause 1、TP-FCS C3） | TS 24.011 E-2、TS 23.040 9.1.2.5、9.2.3.22 |
| Sh の UDR に答えがない | SC | RP-ERROR（RP-Cause 41、TP-FCS C2） | TS 24.011 E-2、TS 23.040 9.2.3.22 |
| MT の MESSAGE が失敗した | IP-SM-GW | 応答コードを MAP の user error に変える。480 は Absent Subscriber SM、404 と 604 は Unidentified subscriber、486、600、603 は Subscriber busy for MT SMS、401 と 407 は Illegal Subscriber、ほかのコードと応答がないときは System Failure | TS 24.341 5.3.3.4.2、TS 29.311 Table 6.1.4.4.1.1 |
| UE が RP-ERROR を返した | IP-SM-GW | RP-Cause 22 は memory capacity exceeded、ほかは protocol error | TS 24.011 Table 8.4 part 2 |
| delivery report が来ない | IP-SM-GW | System Failure | このプロジェクトで決めた |
| 配達に失敗したときの status report | SC | 不在、話中、System Failure、メモリ不足は TP-ST 0x62、ほかは 0x43 | TS 23.040 9.2.3.15。スタブは再送しないので、どちらも SC がもう送らないことを示す値 |

実装のときに、TS 29.311 V19.0.0、RFC 3428 の 7 章、RFC 3841 の 9.1 と 9.2、RFC 3261 の 8.2.3 と 20.21 も読みました。

## 確かめられなかったこと

- TS 29.002（MAP）、RFC 3840、TS 24.229 5.7.1.3A は読んでいません。RFC 3428 と RFC 3841 は、上の「実装で決めたこと」に書いた章だけを読みました。
- UCS2 のバイト順と、BMP の外の文字の扱い。
- IR.92 Annex C.3 の SMS_Over_IP_Networks_Indication の既定値。
- IP-SM-GW が HSS への登録に MAP と Sh のどちらをよく使うか。仕様からは決められません。
- Sh-Data の XML で SMSRegistrationInfo の要素がどういう形か。
- 今の P-CSCF と I-CSCF が MESSAGE を正しく中継するか。
