# 5GC（VoNR）の調査（5GC スタブ、N5、Nhss、NG.114、EVS）

この文書は、5GC（VoNR）の構成について、仕様が定めることと、この実装の作りを調べた記録です。5GC のスタブ、P-CSCF と PCF のあいだの N5、HSS の SBI（Nhss）、NG.114 の必須の要件、EVS を扱います。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは「読んだ資料」の表にある版のものです。条項番号は、ダウンロードした仕様書の本文で確かめたものだけを書きます。確かめられなかった所は、本文に条項番号を書かず、[確かめられなかった条項](../unverified-clauses.md) に記録します。

コードについての記述は、EPC と PCRF のスタブ（`internal/epc`）、P-CSCF の側の境界（`internal/policy`）、`internal/rx` が入った時点のものです。その時点では、P-CSCF が `policy` を使う所と、UE がアクセス網のスタブを使う所は入っていません。その部分は [volte.md](volte.md) と [VoLTE のアクセス網の設計](../design/volte-access.md) をもとに読んでいます。

## 読んだ資料

| 仕様 | 版 | 読んだ所 |
| --- | --- | --- |
| TS 23.501（5GS のアーキテクチャ） | V20.3.0 | 4.4.3、5.7.4 の表、5.16.3.1、5.16.3.2、5.16.3.4、5.16.3.11、5.16.3.12 |
| TS 23.502（5GS の手順） | V20.3.0 | 4.3.2.2.1 のうち P-CSCF に関わる step 1 と step 11。4.3.3.2、4.3.4.2、4.16.5.2 は見出しだけ |
| TS 23.228（IMS のアーキテクチャ） | V20.1.0 | Annex Y（5GS から IMS を使うときの考え方）の Y.1.1、Y.2.3.1、Y.2.4、Y.12、Y.13。Annex AA（IMS の SBA）の AA.1、AA.2.2 |
| TS 24.229 | V20.1.0 | Annex U の U.2.2.1、U.2.2.1C、U.2.2.5、U.3.1.1。7.2A.4.3。Annex X |
| TS 24.501（5GS の NAS） | V20.1.0 | 6.3.2.2、6.4.1.2、6.4.1.3 のうち P-CSCF に関わる段落。8.2.10、8.3.1、8.3.2、8.3.9、8.3.14 の IE の表。9.11.4.6 |
| TS 24.008（PCO の符号化） | V20.1.0 | 10.5.6.3 のコンテナの識別子、10.5.6.3A の先頭 |
| TS 29.500（SBA の実現方法） | V20.0.0 | 5.1、5.2.1、5.3 |
| TS 29.514（Npcf_PolicyAuthorization） | V20.1.0 | 4.2.1、4.2.2.1、4.2.2.2、4.2.2.7、4.2.2.10、4.2.2.16、4.2.3.1、4.2.4.2、4.2.5.3、4.2.5.5、4.2.5.8、4.2.5.10、4.2.6.7、5.2、5.3.1、5.8 の表の一部、Annex B の B.1、B.4、B.7。Annex A の OpenAPI |
| TS 29.513（PCC の信号の流れと QoS の対応づけ） | V20.1.0 | 7.2.3 の先頭、8.4。Annex B は見出しだけ |
| TS 29.562（HSS の SBI） | V20.1.0 | 5.1、5.2.2.2.2、5.2.2.3.2、5.2.2.4.2、5.2.2.5.2、5.3.2.7.6、5.4.2.2.2、6.3.6.3.3。Annex A の OpenAPI |
| TS 29.510（NRF） | V20.1.0 | 5.2.2 と 5.3.2 の見出し、6.1.6.2.53、6.1.6.2.57 の型の名前、OpenAPI の経路 |
| TS 26.114（MTSI のメディア） | V20.1.1 | 5.2.1.1、5.2.1.4、5.2.1.6、6.2.2.2 の先頭と Table 6.2a、6.2.2.3 の Table 6.3a と Table 6.3c、6.2.5.2 の `b=AS` の段落、A.14.2.1。10.2.1.10 と 10.2.1.11 は見出しだけ。`internal/sdp` の EVS を作るときに、6.2.2.1、6.2.2.3 の Table 6.3 と Table 6.3b、6.2.5.2 の全文と Table 6.7 から Table 6.9、Annex Q、A.14 のすべての例も読みました |
| TS 26.441（EVS の概要） | V19.0.0 | 4 章、6 章 |
| TS 26.445（EVS のアルゴリズム） | V19.1.0 | Annex A の A.0、A.1、A.2、A.2.1 の表、A.2.2、A.2.3、A.2.4、A.2.6、A.3.1、A.3.2、A.3.3.1。`internal/sdp` の EVS を作るときに、A.3.1 から A.3.3.3 を読み直しました |
| TS 26.443（EVS の浮動小数点の参照コード） | V19.0.0 | 付属のソースの readme.txt と、ソースの先頭のコメント。表紙、著作権の表示、4.1。見直しのときに、zip の中のすべてのファイルを著作権とライセンスの語で探しました |
| TS 26.442（EVS の固定小数点の参照コード） | V19.0.0 | 表紙、著作権の表示。付属のソースの readme.txt。zip の中のすべてのファイルを著作権とライセンスの語で探しました |
| TS 26.444（EVS の試験の系列） | V19.0.1 | 表紙、著作権の表示、zip の Readme.txt |
| RFC 4867（AMR と AMR-WB のペイロード形式） | - | 8.1、8.2 の `max-red` と `mode-change-*`。EVS の A.3.1 がこれを参照しています |
| GSMA NG.114 | v11.0（2026-09-22） | 2.2.1.2、2.2.2.1、2.4.2、3.2.1 から 3.2.3、4.4 から 4.9 |

OpenAPI のファイルは、3GPP のファイルサーバーにある各仕様書の zip に入っています（`TS29514_Npcf_PolicyAuthorization.yaml`、`TS29562_Nhss_imsUECM.yaml`、`TS29562_Nhss_imsSDM.yaml`、`TS29562_Nhss_imsUEAU.yaml`、`TS29510_Nnrf_NFDiscovery.yaml` など）。経路、型、列挙の名前は、このファイルで確かめました。

7 章の利用条件と特許は、同じ日のあとの見直しで、次のページも読みました。題は各ページの表題です。

| サイト | ページ |
| --- | --- |
| voiceage.com | 「Enhanced Voice Services (EVS) codec」、「Products Overview」（Licensing の Overview）、「IPR Portfolio Overview」、「patent portfolio by standards」、「(AMR-WB) patent portfolio」（中身は EVS の表）、「AMR-WB/G.722.2」、「AMR (Adaptive Multi-Rate) standard」 |
| www.via-la.com | 「Voice Codec」（https://www.via-la.com/licensing-programs/voice-codec/ 。Overview、License Fees、Licensors、Essentiality Overview、FAQ の各節）、「Licensing Programs」の一覧（https://www.via-la.com/licensing-programs/ ）、ニュースの「Via LA Launches Voice Codec Pool with Deep Market Adoption, Five Large Global Licensors」（2024 年 12 月）、「Via Licensing Alliance Expands Voice Codec Program with New Licensee, New Licensors, Publishes Comprehensive Pool Rate Structure」（2026-05-20）、「MPEG LA Introduces License for EVS」（2017-06-28）、「MPEG LA Facilitating Development of Enhanced Voice Services Patent Pool License」（2016-01-20） |
| www.3gpp.org | 「Legal Matters」 |
| www.itu.int | G.191「Software tools for speech and audio coding standardization」の版の一覧 |
| github.com | `openitu/STL` の `LICENSE.md`（ITU-T Software Tools' General Public License） |

GSMA のサイトは、調べた時点で NG.114 v11.0 と IR.92 v24.0 の PDF を返しました。共通の決まりには 403 になると書いてありましたが、1 回試して取れたので、NG.114 は本文を読み直しています。

## 全体の流れ

5GC の構成で学習者が追える VoNR の呼は次のとおりです。VoLTE の流れと並べて、どこが替わるかを見られるようにします。

1. UE は 5GC のスタブに登録し、IMS の DNN への PDU セッションを求めます。スタブは UE の IP アドレスと、P-CSCF のアドレスの一覧を返し、5QI 5 の QoS flow を作ります。
2. UE は IMS に登録します。SIP の手順は VoLTE と同じです。P-Access-Network-Info の access-type は NR のものになります。
3. P-CSCF は登録のあと、N5 で PCF に信号の経路の状態の通知を申し込みます。Rx の AAR の代わりに、HTTP の POST を送ります。
4. 発信では、P-CSCF が SDP answer を見て、PCF に Individual Application Session Context を作ります。PCF のスタブは 5QI 1 の QoS flow を作り、UE に知らせます。
5. 通話中は EVS、AMR-WB、AMR のどれかの RTP が中継ノードを通ります。
6. 切断で、P-CSCF は N5 の delete を送り、PCF のスタブは QoS flow を消します。
7. 異常系として、スタブから音声の QoS flow を落とすと、PCF が P-CSCF に通知し、P-CSCF が BYE で呼を切ります。
8. I-CSCF と S-CSCF は、設定で Cx の代わりに Nhss を使えます。

## 1. 5GC スタブと P-CSCF の発見

### 仕様が求めること

- 5GS は、P-CSCF のアドレスを UE に届ける働きを持ちます（TS 23.501 5.16.3.1）。IMS のための PDU セッションの確立で、SMF が P-CSCF のアドレスを UE に送ります。アドレスは AMF をそのまま通ります。SMF はアドレスを自分の設定から得るか、NRF で探します（5.16.3.4、5.16.3.11）。
- 手順は PDU セッションの確立です（TS 23.502 4.3.2.2.1）。UE は、接続の確立のなかで P-CSCF を見つける設定なら、SM のコンテナに P-CSCF のアドレスを求める印を入れます（step 1）。SMF は PDU Session Establishment Accept に P-CSCF のアドレスを入れます（step 11）。
- NAS では、UE が PDU SESSION ESTABLISHMENT REQUEST（TS 24.501 8.3.1）を UL NAS TRANSPORT（8.2.10）に入れて送ります。DNN、Request type、PDU session ID は UL NAS TRANSPORT の IE です。網は PDU SESSION ESTABLISHMENT ACCEPT（8.3.2）を返します。この中に、PDU address、Authorized QoS rules、Authorized QoS flow descriptions、Extended protocol configuration options、DNN があります。
- P-CSCF のアドレスは Extended protocol configuration options（ePCO）で運びます。IE の定義は TS 24.008 10.5.6.3A を指しています（TS 24.501 9.11.4.6）。ePCO は長さの欄が 2 オクテットの IE で、中身は PCO の 3 オクテット目からと同じ符号化です（10.5.6.3A）。コンテナの識別子も PCO と同じ表を使います。P-CSCF IPv6 Address Request と P-CSCF IPv6 Address が 0001H、IM CN Subsystem Signaling Flag が 0002H、P-CSCF IPv4 Address Request と P-CSCF IPv4 Address が 000CH、P-CSCF Re-selection support が 0012H です（TS 24.008 10.5.6.3）。EPS の PCO と同じ識別子なので、VoLTE の PCO の符号化の知識がそのまま使えます。
- IMS の信号のための PDU セッションで、UE が P-CSCF のアドレスを求めていたら、SMF は ACCEPT の ePCO にアドレスを入れます（TS 24.501 6.4.1.3）。
- UE の側の決まりは TS 24.229 Annex U にあります。UE は 5GS に登録し、SIP の信号に使う QoS flow を持つ PDU セッションを用意し、P-CSCF のアドレスを得ます（U.2.2.1）。P-CSCF を見つける方法は 4 つで、方法 II が PDU セッションの確立で受け取る方法です。UE は REQUEST の ePCO で IM CN Subsystem Signalling Flag を立て、P-CSCF のアドレスを求めます。受け取った一覧は、先頭ほど優先度が高いとみなします。SIP の信号には、default QoS rule に結びついた QoS flow を使います。
- 網が PDU SESSION MODIFICATION COMMAND の ePCO で P-CSCF の一覧を送り、今の P-CSCF がその中に無ければ、UE は別の P-CSCF を選んで初期登録をやり直します（TS 24.229 U.2.2.1C、TS 24.501 6.3.2.2）。P-CSCF の復旧（TS 23.380）で使う手順です。
- 音声の QoS flow は網が作ります。P-CSCF が QoS の要求を PCF に渡したあとで、網が QoS flow を作ります（TS 24.229 U.2.2.5.1 NOTE 2）。UE は、網から届いた QoS flow を、進行中の SIP セッションと対応づけます（U.2.2.5.1B）。TS 23.228 Y.2.3.1 も、IMS では網が QoS flow の確立、変更、終了を始めると書いています。
- 5QI の値は TS 23.501 5.7.4 の Table 5.7.4-1 にあります。5QI 1 は GBR の Conversational Voice（優先度 20、遅延の上限 100 ms、誤り率 10⁻²）、5QI 5 は Non-GBR の IMS Signalling（優先度 10、100 ms、10⁻⁶）です。EPS の QCI 1 と QCI 5 に当たります。
- AMF は登録の手順のなかで、IMS voice over PS session に対応しているかを UE に知らせます（TS 23.501 5.16.3.2）。UE はこれを見て、音声を IMS で使うかを決めます。
- P-Access-Network-Info は 5GS でも必ず付けます（TS 24.229 U.3.1.1）。NR では access-type を 3GPP-NR-FDD か 3GPP-NR-TDD にし、utran-cell-id-3gpp に MCC、MNC、TAC（16 進 6 桁）、NCI（16 進 9 桁）を続けて書きます（7.2A.4.3）。E-UTRAN の TAC は 4 桁、ECI は 7 桁だったので、桁数が違います。
- NG.114 は、IMS の DNN への PDU セッションに 5QI 5 の QoS flow を求め（4.5.1.1）、P-CSCF の発見に方法 II を求めます（4.7）。UE は IPv6 と IPv4 の両方の P-CSCF のアドレスを求めます。

IMS の DNN の名前は、NG.114 4.5.1.1 が GSMA NG.113 の「IMS well-known APN/DNN」を指しています。NG.113 は読んでいないので、値が `ims` であることは確かめていません。VoLTE の構成の APN と同じ値を使えば、構成のうえでは食い違いません。

### 今のコードとの差

- EPC のスタブは `internal/epc` にあり、役割 `epc` で動きます（`internal/config/config.go` の `RoleEPC`）。5GC の役割はありません。
- UE との API は `internal/epc/api.go` にあります。TS 24.301 のメッセージの名前を付けた JSON を `/ues/{imsi}/{メッセージの名前}` に POST し、網からのメッセージは `/ues/{imsi}/events` の Server-Sent Events で受けます。ATTACH REQUEST が PDN CONNECTIVITY REQUEST を運ぶ形です。5GS では、登録と PDU セッションの確立が別の手順になります。
- PCO は `epc.ProtocolConfigurationOptions` で、コンテナの識別子と中身の一覧として持っています。ePCO の中身は PCO と同じ符号化なので、同じ型で表せます。
- セルの情報は `epc.SystemInformation` で、TAC と、28 ビットの ECI を返します。NR では TAC が 24 ビット、NCI が 36 ビットになります（7.2A.4.3 の桁数から）。
- QoS は `epc.QCI` と `epc.EPSQoS` で表しています。5GS では 5QI と QoS flow になります。
- スタブには、学習者が音声の bearer を落とす操作と、切り離す操作があります。経路は網が始める手順の名前です。EPC のスタブは `DELETE /network-initiated/ues/{imsi}/bearers/{ebi}` と `POST /network-initiated/ues/{imsi}/detach` です（`internal/epc/http.go`）。5GC のスタブの同じ形の操作は `DELETE /network-initiated/ues/{imsi}/pdu-sessions/{psi}/qos-flows/{qfi}`、`DELETE /network-initiated/ues/{imsi}/pdu-sessions/{psi}`、`POST /network-initiated/ues/{imsi}/deregistration` です（`internal/fivegc/http.go`）。
- UE（`internal/ue`）は、まだスタブを使っていません。P-CSCF のアドレスを設定で受け取っています（`internal/ue/ue.go` の `SIMPLIFIED: TS 24.229 9.2.1`）。P-Access-Network-Info も付けていません。UE の側のクライアントは `internal/epc/client.go` にあります。

### 設計の候補

| 決めること | 結論 | ほかの案 |
| --- | --- | --- |
| スタブの置き場所 | `ims` コマンドの新しい役割（設定の名前は `5gc`、パッケージは `internal/fivegc`）。AMF、SMF、UPF をまとめた 1 つのスタブにし、PCF のスタブを同じパッケージに置く。`internal/epc` が MME、PGW、PCRF を 1 つのパッケージに置いているのと同じ形にする | AMF、SMF、PCF を別の役割に分け、N7 や N11 も SBI で実装する。学べることは増えるが、IMS から見えない所の作り込みになる（拡張の候補） |
| UE との境界（アクセス網のスタブを境界で差し替えられるようにする） | VoLTE の `AccessNetwork` の 2 つ目の実装にする。`internal/epc/api.go` と同じ作りで、メッセージの名前と項目を TS 24.501 に合わせ、本文は JSON にする。経路も `/ues/{supi}/{メッセージの名前}` と `/ues/{supi}/events` にする。ePCO は EPC のスタブの PCO と同じ形で書く | NAS の符号化（`internal/nas`。SMS over NAS のために作っている）で運ぶ。gNB と NGAP が無いので、Wireshark は NAS として読めない |
| 5GS への登録 | PDU セッションの前に、登録の往復を 1 つ置く。応答に IMS voice over PS session supported の印を入れ、UE がそれを見てから IMS に登録する。5G-AKA、セキュリティモード、NGAP を省いたことを SIMPLIFIED に書く | 登録を省き、PDU セッションの確立から始める。TS 24.229 U.2.2.1 の a) が見えなくなる |
| IP アドレス | VoLTE の構成と同じく、加入者ごとの静的なアドレスにする（[VoLTE のアクセス網の設計](../design/volte-access.md)）。EPC と 5GC で同じ加入者に同じアドレスを使い、Compose の設定を増やさない | 5GC 用に別のアドレスの範囲を持つ |
| QoS flow | 5QI 5 の QoS flow（default QoS rule）と、5QI 1 の GBR の QoS flow を帳簿として持つ。QoS の強制はしない（EPC のスタブと同じ SIMPLIFIED） | なし |
| P-CSCF の一覧 | スタブの設定に書く。NRF で探す方法は 4 章の判断に従う | P-CSCF が NRF に登録し、SMF が NRF で探す（TS 23.228 Y.12） |
| 異常系 | スタブの操作用 API に、音声の QoS flow の解放と、PDU セッションの解放を置く。EPC のスタブの操作と同じ名前の付け方にする | なし |
| 状態（同じ役割のどのノードでも扱えるようにする） | UE の文脈、IP から SUPI への索引、QoS flow、N5 のセッションを `Store` の後ろに置く | なし |

## 2. PCF スタブと N5

### 仕様が求めること

- 5GS は、P-CSCF と PCF のあいだに N5 と Rx の両方を認めています。Rx は、初期の導入で Diameter を使う構成のために残してあります。SBI を使うとき、P-CSCF は 5GC の trusted AF として動きます（TS 23.501 4.4.3）。
- TS 24.229 は、本体では Rx の手順だけを書き、SBI の使い方は Annex X（informative）にまとめています。P-CSCF が Npcf_PolicyAuthorization を使うときは、Rx の手順の代わりにこのサービスの操作を使い、PCRF の代わりに PCF と通信します（X.3）。N5 は Rx と同じ働きを持つと書かれています（X.2）。P-CSCF が BYE や CANCEL を出す規則（5.2.8）は Rx のときと同じです。
- N5 のサービスは Npcf_PolicyAuthorization です。操作は Create、Update、Delete、Notify、Subscribe、Unsubscribe の 6 つです（TS 29.514 4.2.1）。
- HTTP/2（RFC 9113）と JSON を使います（TS 29.514 5.2.1、5.2.2.2）。PATCH の本文は JSON Merge Patch（RFC 7396）で、Content-Type は `application/merge-patch+json` です。誤りは Problem Details（RFC 9457）で、Content-Type は `application/problem+json` です。
- 資源は 3 つです（TS 29.514 5.3.1）。API の根は `{apiRoot}/npcf-policyauthorization/v1` です。
  - Application Sessions。`/app-sessions` への POST で、Individual Application Session Context を作ります。
  - Individual Application Session Context。`/app-sessions/{appSessionId}` への PATCH で変え、GET で読み、`/app-sessions/{appSessionId}/delete` への POST で消します。
  - Events Subscription。`/app-sessions/{appSessionId}/events-subscription` への PUT で申し込み、DELETE で取り消します。
- Create（4.2.2.2）。メディアの情報がそろい、PCC の制御が要るとき、AF は `/app-sessions` に POST を送ります。本文は `AppSessionContext` で、中の `ascReqData` に要求を書きます。
  - UE の IP アドレスを `ueIpv4` か `ueIpv6` に書きます。PCF はこれでセッションを結びつけます。結びつけに失敗したら、500 と `PDU_SESSION_NOT_AVAILABLE` を返します。
  - メディアは `medComponents` に書きます。メディアごとに `fStatus` でフローを通すかを示し、フィルタを `medSubComps` の `fDescs` に書きます。
  - `notifUri` は必須です。PCF はここに、セッションを終えるように求める通知を送ります。`suppFeat` も必須です。
  - 通知を受けたい事象は `evSubsc` の `events` に書き、通知先を `evSubsc.notifUri` に書きます。
  - 成功すると 201 と Location ヘッダが返ります。Location が、作られた資源の URI です。
  - サービスを断るときは 403 と `REQUESTED_SERVICE_NOT_AUTHORIZED` です。
- Update（4.2.3.1、4.2.3.2）。AF は資源に PATCH を送ります。SDP の offer と answer をやり直したときに使います。
- Delete（4.2.4.2）。AF セッションが終わったら、AF は `…/delete` に POST を送ります。PCF は 204 を返し（事象の情報を返すときは 200）、そのあとで資源を解放します。
- Notify（4.2.5）。PCF が HTTP のクライアントになり、AF が渡した URI に POST を送ります。
  - 事象の通知。`{evSubsc.notifUri}/notify` に `EventsNotification` を送ります（4.2.5.2）。
  - 終了の通知。セッションがもう有効でないとき、`{notifUri}/terminate` に `TerminationInfo` を送ります。`termCause` は、SM Policy の文脈が終わったときが `PDU_SESSION_TERMINATION`、すべての SDF が止まったときが `ALL_SDF_DEACTIVATION` です。AF はこれを受けて Delete を送ります（4.2.5.3）。
  - SDF の一部が止まったとき。事象 `FAILED_RESOURCES_ALLOCATION` と、止まったフローを `flows` に入れて通知します（4.2.5.5）。申し込みは 4.2.2.7 です。
  - 資源の確保の結果。`SUCCESSFUL_RESOURCES_ALLOCATION` と `FAILED_RESOURCES_ALLOCATION` です（4.2.5.8）。申し込みは 4.2.2.10 です。
- 信号の経路の状態の通知（4.2.6.7）。機能 `IMS_SBI` があるとき、AF は信号用の Individual Application Session Context を作ります。UE の IP アドレス、事象 `FAILED_RESOURCES_ALLOCATION`、`medCompN` が 0 のメディア、`flowUsage` が `AF_SIGNALLING` で `fNum` が 0 の副メディアを書きます。通知は 4.2.5.10 です。
- P-CSCF の決まりは Annex B にあります。
  - P-CSCF は、SDP answer を含む SIP メッセージのたびに、サービスの情報を PCF に送ります。情報は offer と answer の両方から作ります。`servInfStatus` を `FINAL` にできます。offer を見た時点で `PRELIMINARY` を送ることもできます（B.1）。
  - 帯域は b=AS から、RTCP の分は b=RR と b=RS から作ります。細かな規則は TS 29.513 7.2 を指しています（B.1）。
  - P-CSCF は、初期の REGISTER を受けたときに信号の経路の状態の通知を申し込めます。登録が解除されたら取り消します（B.4）。
  - 登録に 200 を返したあとで、SIP の信号のフローの情報を届けられます（B.7、4.2.2.16。機能 `ProvAFsignalFlow`）。
- SDP から `medComponents` を作る規則は、TS 29.513 7.2.3 の Table 7.2.3-1 にあります。P-CSCF には必須です。Rx の TS 29.213 6.2 に当たります。
- 機能の交渉（5.8）。`IMS_SBI` は 5 番、`ProvAFsignalFlow` は 7 番です。AF は対応する機能を `suppFeat` で知らせます。`suppFeat` の符号化は TS 29.571 にあり、読んでいません。
- PCF が複数あるときは、BSF（TS 29.521）で、その PDU セッションを持つ PCF を探します（TS 29.513 8.4）。DRA に当たります。

### Rx との対応

P-CSCF から見た対応です。右の列は TS 29.514 の条項です。Rx の列の内容は、[volte.md](volte.md) の 2 章で確かめています。

| Rx（TS 29.214） | N5（TS 29.514） | 条項 |
| --- | --- | --- |
| AAR（新しいセッション）と AAA | `/app-sessions` への POST と 201 | 4.2.2.2 |
| AAR（同じセッションの更新）と AAA | `/app-sessions/{appSessionId}` への PATCH と、200 か 204 | 4.2.3.2 |
| STR と STA | `/app-sessions/{appSessionId}/delete` への POST と 204 | 4.2.4.2 |
| RAR と RAA | `{evSubsc.notifUri}/notify` への POST とその応答 | 4.2.5.2 |
| ASR と ASA。そのあとの STR と STA | `{notifUri}/terminate` への POST と 204。そのあとの delete | 4.2.5.3 |
| Session-Id | Location で返る資源の URI（`appSessionId`） | 4.2.2.2 |
| Framed-IP-Address | `ueIpv4` | 4.2.2.2 |
| Media-Component-Description、Media-Component-Number | `medComponents`、`medCompN` | 4.2.2.2 |
| Media-Sub-Component、Flow-Number、Flow-Description、Flow-Usage | `medSubComps`、`fNum`、`fDescs`、`flowUsage` | 4.2.2.2 |
| Flow-Status | `fStatus` | 4.2.2.2 |
| Max-Requested-Bandwidth-UL と DL、RS-Bandwidth、RR-Bandwidth | `marBwUl`、`marBwDl`、`rsBw`、`rrBw` | Annex A |
| Codec-Data | `codecs` | Annex A |
| Service-Info-Status | `servInfStatus`（`FINAL`、`PRELIMINARY`） | B.1 |
| AF-Charging-Identifier、AF-Application-Identifier | `afChargId`、`afAppId` | 4.2.2.2 |
| Specific-Action | `evSubsc.events` の `event`（`AfEvent`） | 4.2.2.2 |
| Abort-Cause | `termCause`（`TerminationCause`） | 4.2.5.3 |
| IP-CAN_SESSION_NOT_AVAILABLE | 500 と `PDU_SESSION_NOT_AVAILABLE` | 4.2.2.2 |
| REQUESTED_SERVICE_NOT_AUTHORIZED | 403 と `REQUESTED_SERVICE_NOT_AUTHORIZED` | 4.2.2.2 |
| Flow-Number が 0 の AAR（信号の経路） | `medCompN` が 0、`fNum` が 0、`flowUsage` が `AF_SIGNALLING` の POST | 4.2.6.7 |
| Supported-Features | `suppFeat` | 5.8 |

Rx と違う点が 3 つあります。

- 通知の向き。Rx では、同じ Diameter の接続の上で PCRF が RAR を送ります。N5 では、PCF が HTTP のクライアントになって、P-CSCF の URI に POST を送ります。P-CSCF は HTTP/2 のサーバーも持つことになります。
- 事象の名前。Rx の INDICATION_OF_LOSS_OF_BEARER と INDICATION_OF_RELEASE_OF_BEARER に当たる名前は、N5 にはありません。SDF が止まったことは `FAILED_RESOURCES_ALLOCATION` で知らせます（4.2.5.5）。
- 終了の往復。Rx の ASR は PCRF からの要求で、AF は ASA のあとに STR を送ります。N5 では、PCF が terminate の通知を送り、AF が delete を送ります。形は似ていますが、資源を消すのは AF の delete です。

### 今のコードとの差

- 境界は `internal/policy` にあります。`PolicyControl` は 3 つの操作を持ちます。`SubscribeSignallingPath`（UE の IP アドレスを渡す）、`AuthorizeMedia`（セッションの値、UE の IP アドレス、SDP の offer と answer、ICSI、通知を受けたい事象を渡す）、`Release` です。セッションは文字列の `SessionID` で表し、網からの通知は `Notify` の関数に `Event`（セッションと事象の種類）で届きます。
- SDP からサービスの情報を作る処理は `internal/policy/sdp.go` にあり、TS 29.213 6.2 の表に従っています。結果は `policy.MediaComponent` で、Rx の実装（`internal/rx/media.go`）がそれを AVP に直します。
- Rx の実装は `internal/rx/client.go` の `Client` です。RAR と ASR は `Client.Handle` が受け、応答を返したあとで `Notify` を呼びます。
- P-CSCF（`internal/pcscf`）は、まだ `policy` を使っていません。`cmd/ims/main.go` は、P-CSCF の Diameter の接続に Rx のアプリケーションを載せる所まで入っています。
- P-CSCF の HTTP の接続は、中継ノードの制御（`internal/pcscf/session.go` の `Relay`）だけで、HTTP/1.1 です。HTTP のサーバーは持っていません。
- 設定の `pcscf` の節は、PCRF を Diameter の相手として書く形です。PCF の URI と、通知を受ける待ち受けの項目はありません。

### 設計の候補

| 決めること | 結論 | ほかの案 |
| --- | --- | --- |
| P-CSCF の側の実装 | `policy.PolicyControl` の 2 つ目の実装を `internal/n5` に置く。JSON の型は TS 29.514 の名前（`AppSessionContext`、`MediaComponent`、`EventsNotification` など）で、使う属性だけを Go の構造体に書く。`policy.MediaComponent` を JSON に直す所は、`internal/rx/media.go` が AVP に直す所と対になる | OpenAPI から型を生成する。使わない属性が大半で、学習者が読む量が増える。生成の道具も依存に加わる |
| Rx と N5 の選び方 | `pcscf` の設定に、アクセス網の境界の種類（`rx` か `n5`）と接続先を書く。1 つの P-CSCF は片方だけを使う | UE ごとに選ぶ。PCRF と PCF の両方に尋ねることになり、4G と 5G をまたぐ UE を扱うときに要る（拡張の候補） |
| 通知を受ける所 | P-CSCF に SBI の待ち受け（h2c）を足し、`notifUri` に自分の名前とポートを書く。`notifUri` の経路に、呼の通過（中継のコンテキスト）を引く鍵を入れる。届いた通知から `Store` を引けるので、同じ役割のどのノードに届いても処理できる | 通知を受けずに、P-CSCF が定期に GET で状態を読む。標準の流れと違い、異常系が遅れる |
| Create を送るとき | SDP answer を見たときに `FINAL` で送る。やり直しは PATCH。Rx と同じにする | offer のときにも `PRELIMINARY` を送る |
| 登録のとき | 200 のあとで、信号の経路の状態の通知を申し込む（4.2.6.7）。`PDU_SESSION_NOT_AVAILABLE` なら、その UE の呼では N5 を使わない（EPC を通らない UE と同じ扱い） | 登録を断る |
| PCF スタブ | 役割 `5gc` の中に置く。SMF のスタブとのあいだ（N7、TS 29.512）は Go の関数の呼び出しにし、SIMPLIFIED に書く。`internal/epc/pcrf.go` が Gx を省いているのと同じ形にする。N5 は HTTP/2 で実装する | N7 も SBI で実装する |
| PCF の選び方 | P-CSCF の設定に PCF の URI を書く。BSF（TS 29.513 8.4）は省き、SIMPLIFIED に書く。Rx の構成が DRA を省くのと同じ | BSF のスタブを作る |
| 機能の交渉 | `suppFeat` に `IMS_SBI` を立てる。`ProvAFsignalFlow` は、Rx で使うと決めたときだけ立てる | なし |
| 状態（同じ役割のどのノードでも扱えるようにする） | P-CSCF は、資源の URI と呼の通過の対応を `Store` に置く。Rx の Session-Id を置く所と同じ形にする | なし |
| 認可 | NF のあいだの OAuth 2.0 のトークン（TS 29.510 の Nnrf_AccessToken）は使わない。OpenAPI も、認可なしの呼び出しを認めている。省いたことを SIMPLIFIED に書く | NRF のスタブがトークンを出す |

## 3. HSS の Nhss

### 仕様が求めること

- IMS の SBA は TS 23.228 Annex AA にあります。I-CSCF と S-CSCF から HSS への参照点が N70、AS から HSS への参照点が N71 です（AA.1.2）。SBI に対応した IMS のノードは、SBI と Diameter の両方を持てます（AA.1.3）。TS 24.229 X.3 も、I-CSCF、S-CSCF、AS が Cx の手順の代わりに Nhss の操作を使えると書いています。
- HSS が出すサービスは 6 つです（TS 29.562 5.1）。IMS に関わるのは次の 3 つです。API の名前は `nhss-ims-uecm`、`nhss-ims-sdm`、`nhss-ims-ueau` です。
  - Nhss_imsUEContextManagement（6.1）。登録、登録解除、認可、復旧の情報を扱います。
  - Nhss_imsSubscriberDataManagement（6.2）。加入者のデータの取得、変更、変更の通知の申し込みを扱います。
  - Nhss_imsUEAuthentication（6.3）。認証の情報を返します。I-CSCF と AS は使いません（TS 24.229 X.3 NOTE 3、NOTE 4）。
- Cx の操作との対応は TS 23.228 AA.2.2.2 の表にあります。Sh との対応は AA.2.2.3 です。

| Cx と Sh | Nhss の操作 | HTTP（OpenAPI で確かめた経路） | TS 29.562 |
| --- | --- | --- | --- |
| UAR と UAA | ImsUECM_Authorize | POST `/{impu}/authorize` | 5.2.2.5.2 |
| UAA の S-CSCF の能力 | ImsSDM_Get | GET `/{imsUeId}/ims-data/location-data/scscf-capabilities` | 確かめていない |
| LIR と LIA | ImsUECM_Authorize。S-CSCF の能力は ImsSDM_Get | 確かめていない。OpenAPI には GET `/{imsUeId}/ims-data/location-data/server-name` もある | 確かめていない |
| MAR と MAA | ImsUEAuthenticate_Get | POST `/{impi}/security-information/generate-sip-auth-data` | 5.4.2.2.2 |
| SAR と SAA（登録） | ImsUECM_Registration | PUT `/{imsUeId}/scscf-registration` | 5.2.2.2.2 |
| SAR と SAA（登録解除） | ImsUECM_Deregistration | PUT `/{imsUeId}/scscf-registration`（同じ資源） | 5.2.2.4.2 |
| SAA の User-Data | ImsSDM_Get | GET `/{imsUeId}/ims-data/profile-data` | 確かめていない |
| RTR と RTA | ImsUECM_DeregistrationNotification | S-CSCF が登録のときに渡した `deregCallbackUri` への POST | 5.2.2.3.2 |
| PPR と PPA | ImsSDM_Notification | S-CSCF が申し込みで渡した URI への POST | 確かめていない |
| Sh の UDR | ImsSDM_Get | GET `/{imsUeId}/repository-data/{serviceIndication}` など | 確かめていない |
| Sh の PUR（IP-SM-GW の登録） | ImsSDM_Update | PUT `/{imsUeId}/service-data/sms-registration-info` | 5.3.2.7.6 |
| Sh の PUR（透過データ） | ImsSDM_Update | PUT `/{imsUeId}/repository-data/{serviceIndication}` | 確かめていない |

- 登録（5.2.2.2.2）。S-CSCF は `scscf-registration` に PUT を送ります。本文の `ScscfRegistration` に、`imsRegistrationType` と `cscfServerName`（どちらも必須）、`impi`、`deregCallbackUri` などを書きます。資源が無ければ 201、あれば 200 か 204 です。別の S-CSCF が登録済みで、選び直しが認められないときは 403 です。
- `imsRegistrationType` の値は `INITIAL_REGISTRATION`、`RE_REGISTRATION`、`TIMEOUT_DEREGISTRATION`、`USER_DEREGISTRATION`、`ADMINISTRATIVE_DEREGISTRATION`、`AUTHENTICATION_FAILURE`、`AUTHENTICATION_TIMEOUT`、`UNREGISTERED_USER` です。Cx の Server-Assignment-Type に当たります。
- 認証（5.4.2.2.2）。S-CSCF は `generate-sip-auth-data` に POST を送ります。本文に、S-CSCF の名前、認証方式、再同期の情報を書きます。応答の `SipAuthenticationInfoResult` に、AKA なら `3gAkaAvs`（`rand`、`xres`、`autn`、`ck`、`ik`）、SIP Digest なら `digestAuth`（`digestRealm`、`digestAlgorithm`、`digestQop`、`ha1`）が入ります。認証方式の値は `DIGEST-AKAV1-MD5` と `DIGEST-HTTP` などです（6.3.6.3.3）。`DIGEST-HTTP` が SIP Digest を表します。
- 認可（5.2.2.5.2）。I-CSCF は `authorize` に POST を送ります。本文は `impi`、`authorizationType`（`REGISTRATION` か `DEREGISTRATION`）、`visitedNetworkIdentifier` などです。応答に、認可の結果と、その利用者を受け持つ S-CSCF の名前が入ります。
- 網からの登録解除（5.2.2.3.2）。HSS は、S-CSCF が登録のときに渡した URI に POST を送ります。本文の `DeregistrationData` に、理由と IMPI を書きます。理由の値は `PERMANENT_TERMINATION`、`NEW_SERVER_ASSIGNED`、`SERVER_CHANGE`、`REMOVE_S-CSCF` です。Cx の Deregistration-Reason に当たります。
- プロファイル。`ims-data/profile-data` が返す `ImsProfileData` には、`imsServiceProfiles` が入ります。iFC は JSON で、`Ifc` に `priority`、`trigger`（`conditionType` と `sptList`）、`appServer`（`asUri`、`sessionContinue`、`serviceInfoList`）があります。Cx は同じ内容を XML で運びます（TS 29.228 Annex E）。
- 登録の状態。`ImsRegistrationState` の値は `REGISTERED`、`NOT_REGISTERED`、`AUTHENTICATION_PENDING`、`REGISTERED_UNREG_SERVICES` です。

### 今のコードとの差

- HSS（`internal/hss/hss.go`）は、`Handle` が Diameter のメッセージを受け、`userAuthorization`、`multimediaAuth`、`serverAssignment`、`locationInfo` が `internal/cx` の型で処理します。処理そのものは Diameter に依らない Go の型で書かれているので、Nhss の入口を足しても、中の処理はそのまま使えます。Sh（`internal/hss/sh.go`）も同じ形です。
- I-CSCF と S-CSCF が HSS に届く境界は、Diameter のメッセージの高さにあります。`scscf.HSS` と `icscf.HSS` は `Request(ctx, *diam.Message)` だけを持つインタフェースです。この高さのままでは、Nhss の実装を差し込めません。
- HSS から S-CSCF への RTR は、`cmd/ims/main.go` の `cxHandler` が Diameter の接続で受けて S-CSCF に渡しています。Nhss では、S-CSCF が HTTP のサーバーとして受けることになります。
- S-CSCF は、プロファイルを XML で受けて `internal/cx/userdata.go` と `internal/ifc` で読みます。Nhss では JSON で届きます。
- IP-SM-GW の Sh のクライアント（`internal/sh/client.go`）は、UDR と PUR を型のある関数で持っています。こちらは、境界がすでに操作の高さにあります。

### 設計の候補

| 決めること | 結論 | ほかの案 |
| --- | --- | --- |
| HSS の側 | `internal/nhss` に JSON の型を置き、`internal/hss` に Nhss の入口（`nhss.go`）を足す。入口は JSON を今の `cx` の型に直して、今の処理を呼ぶ。Cx と Nhss は同じ `Store` を見るので、同じ加入者を両方から扱える（TS 23.228 AA.1.3 の共存） | HSS を、UDM と UDR に分けた別の役割として作り直す。IMS から見える動きは変わらない |
| I-CSCF と S-CSCF の側 | HSS への境界を、操作の高さ（認可、認証、登録、場所の問い合わせ）に上げる。今の Cx の実装をその後ろに移し、Nhss の実装を `internal/nhss` のクライアントとして足す。どちらを使うかは設定で選ぶ | Cx のまま残し、HSS だけが Nhss も話す。5GC の構成で HSS の SBI を模擬するという要件は満たすが、Nhss を呼ぶノードが無く、キャプチャに現れない |
| 最初に作る範囲 | 登録と通話に要る操作にしぼる。Authorize、generate-sip-auth-data、scscf-registration（登録と登録解除）、profile-data の取得、server-name の取得、DeregistrationNotification。Sh に当たる操作（repository-data、sms-registration-info）は 2 つ目の単位にする | 全部を一度に作る |
| プロファイルの形 | S-CSCF の中の iFC の型（`internal/ifc`）は 1 つのままにし、XML と JSON の読み手を 2 つ持つ | XML を JSON の中に文字列として入れる。標準と違う |
| 網からの通知 | S-CSCF に SBI の待ち受けを足し、`deregCallbackUri` に自分の URI を書く。HSS は、その URI を登録の情報と一緒に `Store` に置く（今の `Subscriber.SCSCF` の Diameter の識別子に当たる） | なし |
| 変更の通知（PPR に当たるもの） | ImsSDM の Subscribe と Notification は、付加サービスの設定の変更で要るかを見てから決める | なし |

Nhss を呼ぶ側まで作るかは、製品の方向に関わる未決の論点です（10 章）。

## 4. NRF

- 5GS では、NF は NRF に自分を登録し（Nnrf_NFManagement、TS 29.510 5.2.2）、相手を NRF で探します（Nnrf_NFDiscovery、5.3.2）。経路は `{apiRoot}/nnrf-nfm/v1/nf-instances/{nfInstanceID}` と `{apiRoot}/nnrf-disc/v1/nf-instances` です。
- IMS に関わる使い方は 3 つあります。
  - SMF が P-CSCF を探す（TS 23.501 5.16.3.11）。P-CSCF は、設定でそうすると決めたときだけ NRF に登録します。ほかの方法で P-CSCF を見つける網では、登録は要りません（TS 23.228 Y.12）。NF のプロファイルの `PcscfInfo` に、Gm と Mw のアドレスが入ります（TS 29.510 6.1.6.2.53）。
  - I-CSCF、S-CSCF、AS が HSS を探す（TS 23.501 5.16.3.12）。プロファイルの `HssInfo` に、受け持つ IMPI と IMPU の範囲が入ります（6.1.6.2.57）。
  - P-CSCF が PCF を探す。PCF が複数あるときは BSF を使います（TS 29.513 8.4）。
- どれも「設定に書く」方法が標準で認められています。

結論は、NRF を作らず、接続先を設定に書くことです。今の Cx も、HSS の接続先を設定に書いています。省いたことは SIMPLIFIED に書きます。NRF で HSS を選ぶ流れは、HSS を複数台にするときの加入者ごとの振り分け（[状態と冗長構成の設計](../design/state-redundancy.md)）と同じ問題なので、5GC の構成でそれを学ぶために足すこともできます。その場合は、役割 `5gc` の中に NRF のスタブを置き、登録と検索の 2 つの操作だけを作ります。

## 5. SBI の共通の作り（HTTP/2、JSON）

### 仕様が求めること

- SBI は HTTP/2（RFC 9113）と JSON を使います（TS 29.500 5.1、5.2.1）。トランスポートは TCP です。NF が NRF にポートを登録しないときは、`http` の URI では 80 番、`https` では 443 番で待ち受けます（5.3）。
- すべての NF は TLS に対応しなければなりません。網のセキュリティがほかの方法で守られていないときは、PLMN の中でも TLS を使います（5.1）。裏を返すと、ほかの方法で守られていれば平文でもよく、既定では暗号化しないという方針と両立します。平文にしたことは SIMPLIFIED に書き、手順書で実網との違いを説明します。

### 試作で確かめたこと

Go の標準ライブラリだけで、平文の HTTP/2（h2c）のサーバーとクライアントが書けるかを、リポジトリの外の小さなプログラムで確かめました。Go は `go.mod` と同じ 1.27.4 です。

- `net/http` の `http.Protocols` に `SetUnencryptedHTTP2(true)` を設定し、`http.Server.Protocols` と `http.Transport.Protocols` に渡すと、サーバーもクライアントも、TLS なしで最初から HTTP/2 を話しました（RFC 9113 3.3 の prior knowledge）。POST、JSON の本文、201 と Location ヘッダの往復ができ、どちらの側でも `r.Proto` と `resp.Proto` は `HTTP/2.0` でした。
- HTTP/1 を有効にしなければ、HTTP/1.1 のクライアントの接続は切られました。SBI の相手が HTTP/1.1 に落ちることはありません。
- 試作のモジュールに、外部の依存はありません。`go list -deps` に出る `golang.org/x/net` は、標準ライブラリに同梱の写し（`vendor/golang.org/x/net/http2/hpack` など）だけです。`go.mod` にある `golang.org/x/net v0.59.0`（indirect）は使いません。`golang.org/x/net/http2/h2c` も要りません。
- tshark 4.2.2 は、ループバックで取った pcap を、80 番と 8080 番のポートでは設定なしで HTTP2 と JSON として読みました。HEADERS の `:path` と、DATA の JSON が見えます。7777 番と 18080 番では TCP としか読まず、`-d tcp.port==18080,http2` を付けると読めました。HTTP/2 の発見は HTTP の解析の下にぶら下がっているので、HTTP のポートでないと働きません。

この結果から、SBI の待ち受けは 80 番にします（TS 29.500 5.3 の既定で、標準のポート番号を使う方針にも合います）。コンテナは root で動いているので（`Dockerfile` に `USER` が無い）、80 番で待ち受けられます。

### 設計の候補

| 決めること | 結論 | ほかの案 |
| --- | --- | --- |
| HTTP/2 の実装 | 標準ライブラリの `net/http` だけを使う。依存は増えない | `golang.org/x/net/http2` を直接使う。フレームまで触れるが、SBI の学習には要らない |
| 共通の部品 | `internal/sbi` に、h2c のサーバーとクライアントの組み立て、Problem Details の型、キャプチャの観察役（HEP の送信）を置く。N5、Nhss、NRF の 3 つが使う | 各パッケージが自分で組み立てる |
| ポート | 80 番。Compose では、役割ごとに別のコンテナなので重ならない | 役割ごとに別の番号。Wireshark に Decode As の設定が要る |
| HEP とダッシュボード | SBI の要求と応答を HEP で送り、ダッシュボードのシーケンス図に並べる。HEP3 の型に HTTP/2 をそのまま運ぶものがあるかは確かめていない。無ければ、Diameter と同じく JSON に直して送る（[学習用の画面と OAM の設計](../design/lab-oam.md)） | SBI はキャプチャだけで見る |
| 型の書き方 | 使う属性だけを Go の構造体に書き、属性の名前は OpenAPI のとおりにする。OpenAPI のファイルはリポジトリに入れず、仕様の番号と版を書く | OpenAPI のファイルを入れて型を生成する。3GPP の著作物をリポジトリに入れることになる |

## 6. NG.114 の必須の要件

[profiles.md](profiles.md) は、NG.114 と IR.92 の違いを「EVS が必須になること」と「冗長なフレームの受信が必須になること」とまとめています。NG.114 v11.0 を読み直して、5GC の構成に関わる必須の要件を拾いました。右の列は、IR.92 との関係と、このプロジェクトでの扱いです。

| 要件 | NG.114 の節 | IR.92 との関係と扱い |
| --- | --- | --- |
| EVS に対応し、offer する。IMS のコアで超広帯域の音声を終端するノードも EVS に対応する | 3.2.1 | IR.92 では、超広帯域を提供する端末だけ。7 章で扱う |
| 最初の offer に、超広帯域の EVS、mode-set の無い AMR-WB、mode-set の無い AMR を入れる | 3.2.2.1 | IR.92 は AMR-WB と AMR。UE の offer に EVS を足す |
| EVS の offer は、決まった 5 つの構成（A1、A2、B0、B1、B2）のどれかにし、br、bw、max-red、ch-aw-recv のほかのパラメータを付けない | 3.2.2.3 | IR.92 に無い。7 章で扱う |
| answer は、受けた offer と自分の構成から、表のとおりに選ぶ。A1、B0、B1 を選んだら mode-set=0,1,2 を付ける | 3.2.2.3 | IR.92 に無い |
| ptime は 20、maxptime は 240。1 つの RTP に 12 フレームまで受ける。冗長なフレームを受けて処理できる | 3.2.3 | 前半は IR.92 と同じ。冗長なフレームの受信は VoLTE の「重なったフレームを捨てる」の延長で扱う |
| CMR に RTCP-APP を使わない | 3.2.3 | IR.92 と同じ |
| 5GC への登録（TS 24.501 5.5）と、UE Policy Container | 4.4 | 5GS だけ。スタブは登録の往復だけを模擬し、UE Policy は作らない（SIMPLIFIED） |
| IMS の DNN への PDU セッションに、5QI 5 の QoS flow を作る | 4.5.1.1 | IR.92 の QCI 5 の default bearer に当たる。1 章で扱う |
| メディアの QoS flow が無いあいだ、UE は自分の資源が無いとみなす | 4.5.1.1 | IR.92 2.4.3.1 に当たる |
| 音声には、網が dynamic PCC で 5QI 1 の GBR の QoS flow を作り、呼が終わると消す。音声の QoS flow は 1 つで、複数の呼の音声をまとめて運ぶ | 4.5.2 | IR.92 4.4.2 に当たる。2 章で扱う |
| PDU セッションを失ったら、網がその UE の SIP セッションをすべて終える（TS 24.229 5.2.8） | 4.6.1 | IR.92 2.4.2.1 に当たる。terminate の通知で扱う |
| 音声の GBR の QoS flow が作れないか失われたら、網がセッションを終える | 4.6.2 | IR.92 2.4.2.3 に当たる。異常系で扱う |
| P-CSCF は TS 24.229 Annex U.2.2.1 の方法 II で見つける。UE は IPv6 と IPv4 の両方のアドレスを求める | 4.7、2.2.1.2 | IR.92 4.5 に当たる。1 章で扱う |
| PDU SESSION MODIFICATION COMMAND で届いた P-CSCF の一覧に今の P-CSCF が無ければ、選び直して初期登録する | 4.7 | 冗長構成の P-CSCF の選び直しと一緒に扱う |
| PDU セッションの確立（TS 24.501 6.4）。SSC mode、PDU session type、IPv4 と IPv6 の両方 | 4.9 | IPv6 は profiles.md で「要検討」のまま |
| IMS-AKA、Sec-Agree、IPsec。完全性の保護は必須 | 2.2.2.1 | IR.92 と同じ。VoLTE の設計で決めたとおりにする |
| SMS over NAS。UE は必須で、網は対応するなら必須 | 2.4.2 | SMS over NAS の調査で扱う |
| EPC につながる E-UTRAN との連携。N26 を使う単一登録モード、アイドル時の移動、ハンドオーバー、EPS fallback | 4.8 | UE と網の両方に必須と書かれている。無線区間と移動の手順が要るので、今の対象外に当たる。10 章の未決の論点にした |

4.8 は、[profiles.md](profiles.md) に無かった必須の要件です。このプロジェクトは無線区間を対象外にしているので（[要件定義](../requirements.md) の「対象外」）、「IR.92 と NG.114 の必須の要件を満たす」という要件から 4.8 を外すかが、未決の論点です（10 章）。

IR.92 v24.0 との 1 行ずつの突き合わせは、音声（3 章）と無線とコア（4 章）だけ行いました。2 章の SIP の要件（登録、呼の確立、付加サービス）は、節の見出しが IR.92 とほぼ同じ並びであることだけを確かめています。

## 7. EVS

### 仕様が求めること

- EVS の仕様は TS 26.441 が入口で、アルゴリズムが TS 26.445、参照の C コードが TS 26.442（固定小数点）と TS 26.443（浮動小数点）です。C コードは必須と書かれています（TS 26.441 6 章）。実装は、試験の系列に対してビットまで一致する出力を返すことを求められます（7 章）。
- EVS には、EVS Primary と、AMR-WB と相互に通じる EVS AMR-WB IO の 2 つの動作があります。AMR-WB IO は AMR-WB の 9 つのレートすべてに対応します（TS 26.441 4 章）。
- RTP のペイロード形式は TS 26.445 Annex A です。
  - RTP の時刻の周波数は、音声の帯域に依らず 16 kHz です。フレームは 20 ms で、タイムスタンプは 320 ずつ進みます（A.1）。
  - 形式は Compact と Header-Full の 2 つです（A.2）。Compact は、1 つのフレームだけを運び、ペイロードの大きさでモードとレートを見分けます。大きさの表は A.2.1 の Table A.1 にあります。たとえば EVS Primary の 13.2 kbit/s は 264 ビット、24.4 kbit/s は 488 ビットです。AMR-WB IO の Compact には、3 ビットの CMR が付きます。
  - Header-Full は、CMR のバイト（任意）、ToC のバイト、音声のデータを並べます。先頭のビット（H）が 1 なら CMR、0 なら ToC です。Compact の大きさと重なるときは、0 のパディングで避けます（A.2.2.1）。
  - `hf-only` が無いか 0 のとき、1 つの EVS Primary のフレームを CMR なしで送るなら Compact を使います。`hf-only=1` なら Header-Full だけを使います（A.2.3）。
  - AMR-WB だけに対応する相手のために、RFC 4867 の形式で AMR-WB IO を運ぶこともできます（A.2.4）。
  - ファイルに置く格納形式があります。先頭は `#!EVS_MC1.0\n` と、チャネル数の 32 ビットです。そのあとに、ToC のバイトで始まるフレームが並びます（A.2.6）。
- SDP では、`a=rtpmap` の名前が EVS、時刻の周波数が 16000 です。ほかのパラメータは `a=fmtp` に書きます（A.3.2）。パラメータは `br`、`bw`、`br-send`、`br-recv`、`bw-send`、`bw-recv`、`ch-aw-recv`、`cmr`、`dtx`、`dtx-recv`、`hf-only`、`evs-mode-switch`、`max-red`、`mode-set`、`mode-change-period`、`mode-change-capability`、`mode-change-neighbor`、`channels`、`ch-send`、`ch-recv` などです（A.3.1）。
- `evs-mode-switch` が無いか 0 なら、セッションの始めは EVS Primary です。1 なら AMR-WB IO で始めます（A.3.1）。
- NG.114 は、最初の offer の EVS を 5 つの構成のどれかにするよう求めます（3.2.2.3）。A1 は `br=5.9-13.2; bw=nb-swb`、A2 は `br=5.9-24.4; bw=nb-swb`、B0 は `br=13.2; bw=swb`、B1 は `br=9.6-13.2; bw=swb`、B2 は `br=9.6-24.4; bw=swb` です。最初の offer に `evs-mode-switch` は書けません。
- TS 26.114 は、超広帯域か全帯域の音声を offer する端末に EVS を求めます（5.2.1.1）。EVS に対応するとき、EVS AMR-WB IO を AMR-WB の実装の代わりにできます（5.2.1.1）。

### 利用条件と特許について、公開されている事実

法律の判断ではなく、公開の資料で確かめられた事実だけを書きます。

- 3GPP の仕様書の著作権は、3GPP の Organizational Partners が共同で持っています。仕様書の表紙には、書面の許可なしに複製できないという著作権の表示があります（TS 26.441 の表紙で確かめました）。教材や発表で仕様の内容を使うときは、3GPP の Copyright Form で許可を求めるように、3GPP の Legal Matters のページが案内しています。
- TS 26.443 の zip には、参照コードのソース、Makefile、Visual Studio のプロジェクト、Windows 用の実行ファイル（`EVS_cod.exe`、`EVS_dec.exe`）が入っています。`readme.txt` とソースの先頭のコメントに、オープンソースのライセンスの記載はありません。`readme.txt` には、ライセンス、著作権、特許という語が 1 つもありません。
- 参照の符号器は、ITU-T G.192 の形式か、EVS の格納形式（MIME）でビット列を書き出せると `readme.txt` にあります。
- 見直しで、TS 26.442 V19.0.0 と TS 26.443 V19.0.0 の zip を取り直しました。どちらも仕様書の docx と、ソースの zip（`26442-i00-ANSI-C_source_code.zip`、`26443-i00-ANSI-C_source_code.zip`）の 2 つが入っています。ソースは Release 18 の版のままで、先頭のコメントは仕様の番号と日付（2021-11-04）と版の番号だけです。
- 3 つの仕様書（TS 26.442、TS 26.443、TS 26.444）の表紙と著作権の表示は、ほかの 3GPP の仕様書と同じです。
  - 著作権は 3GPP の Organizational Partners（ARIB、ATIS、CCSA、ETSI、TSDSI、TTA、TTC）が持ち、書面の許可なしに複製できない、とあります。この制限は、どの媒体での複製にも及ぶ、とあります。
  - 表紙には、この文書は Organizational Partners の承認の手続きを経ておらず、実装してはならない、とあります。3GPP の中での作業のための文書であり、実装に使う仕様は Organizational Partners の出版の窓口から入手するように、と書かれています。
  - ソースのコードに別のライセンスを与える記述は、仕様書にもありません。
- ソースの zip のファイルを、著作権とライセンスの語で探しました。当たったのは基本演算のファイル（TS 26.442 の `basic_op/basop32.c` と `basic_op/basop.rme`、TS 26.443 の `lib_com/basop32.c`）だけでした。この 3 つには、ITU-T General Public License に従うという注記があり、ITU-T G.191（音声と音響の符号化の標準化のためのソフトウェアの道具）を参照しています。そのほかの EVS のファイルには、著作権もライセンスも書かれていません。
- ITU-T Software Tools' General Public License の本文は、`openitu/STL` の `LICENSE.md` で読みました。冒頭には、2000 年に承認された ITU-T の勧告の Annex 1 として出たものだ、とあります。ITU-T の Software Tool Library の部品と、それを含む物について、そのままの複製と配布、変えたものの配布を認める条件が書かれています。変えたものや、部品を含む物を配るときは、全体を同じライセンスで無償で使えるようにする、などの条件が付きます。EVS の参照コードのうち、どこまでがこの条件の対象になるかは、法律の判断になるので、ここでは書きません。
- TS 26.444 の zip は、仕様書の docx と `Readme.txt` だけです。`Readme.txt` によると、試験の系列は 1.7 GB と 2.5 GB ほどあるので仕様書に入れず、3GPP のファイルサーバーの `26.444/test_sequences/` に置いてあります。参照コードの版が変わるたびに、系列も更新されます。
- 3GPP の「Legal Matters」のページによると、3GPP の仕様書と報告書の著作権は Organizational Partners が共同で持っています。社内の研修、発表、教育の記事、教科書などで仕様書を使いたいときは、3GPP Copyright Form を出すよう案内しています。参照コードの扱いを特に書いた所はありません。
- 3GPP では、個々の会員が、必須かもしれない特許を自分の Organizational Partner に申告します（3GPP の Legal Matters のページ）。申告された特許の条件は、各 Organizational Partner の IPR の方針に従います。3GPP が特許の利用を許すわけではありません。
- VoiceAge の公開のページで確かめたこと。
  - 「Enhanced Voice Services (EVS) codec」のページは、EVS を 2014 年 9 月に 3GPP が標準にした符号器として紹介し、TS 26.441 から TS 26.451 を挙げています。ページの末尾に、EVS の特許（IPR）のライセンスの問い合わせ先があります。
  - 「Products Overview」（Licensing の Overview）のページは、ソースコードを無償で手に入れられても、技術を製品に組み込むには、多くの場合、権利者とのライセンスの契約が要る、という VoiceAge の考えを書いています。
  - 「patent portfolio by standards」のページは、VoiceAge の特許の族ごとに、当たる標準と、満了する年を表にしています。3GPP-EVS の列に印がある族は 14 あり（VAC 1000 から VAC 4000）、満了の年は 2023 年から 2030 年です。国によって年が違う族もあり、もっとも遅いのは 2030 年です。3GPP-AMR-WB の列に印がある族は 6 つ（VAC 300 から VAC 800）で、満了の年は 2019 年から 2024 年（米国）です。この表は VoiceAge 1 社の特許だけのもので、ほかの権利者の特許は含みません。
  - 「AMR-WB/G.722.2」と「AMR (Adaptive Multi-Rate) standard」のページにも、特許のライセンスの問い合わせ先があります。どちらのページも、特許のプールには触れていません。読み直したところ、AMR-WB の問い合わせ先は、VoiceAge とは別の会社のアドレスになっていました。AMR の特許の族の満了の年は、このページでは確かめていません。
- Via LA の公開のページで確かめたこと。題とアドレスは「読んだ資料」の表にあります。
  - EVS の特許のプールはあります。名前は「Voice Codec」で、EVS と IVAS の 2 つの標準を 1 つの契約で扱います。2024 年 12 月に始まりました。
  - プールの対象の「EVS の標準」は、TS 26.441 から TS 26.451 までの 11 の仕様と、そのすべての版と拡張だと定義されています。参照コード（TS 26.442、TS 26.443）と、RTP のペイロード形式を含む TS 26.445 も、この一覧に入っています。IVAS は TS 26.250 から TS 26.258 です。
  - ライセンスの対象として挙がっているのは機器です。スマートフォン、タブレット、ウェアラブルと、この標準を実装するほかの製品の種類、と書かれています。料金は製品 1 台ごとです。
  - 料金の既定の表は、R1 の国と地域で 1 台 0.60 米ドル、それ以外（R2）で 0.40 米ドルです。表には「In-Compliance」の欄もあり、R1 で 0.50 米ドル、R2 で 0.35 米ドルです。この欄の条件は、ページには定義されていません。量を約束する方式（CVO）もあり、1 台 0.48 米ドル（In-Compliance は 0.40 米ドル）で、年ごとの量の約束が要ります。どちらの表も、最初の 5 年の期間は料金が変わらないと書かれています。日本は R1 に入っています。
  - 今のページには、無償の台数、ソフトウェアだけの配布、網の機器、非商用や教育の利用について、免除や閾値の記載はありません。EVS だけのライセンスがほしいときは、別に問い合わせるように書かれています。
  - ライセンスを与える側（Licensors）は、Cordial IP KK、Dolby International AB、ETRI、Huawei Technologies、JVCKENWOOD、NovaVoice Limited、NTT の 7 者です。2026-05-20 のニュースは、NovaVoice と Cordial IP が加わったこと、名前を伏せた新しいライセンシーが加わったこと、料金の表を公開したことを伝えています。このニュースは、このプールが EVS と IVAS の唯一の共同のライセンスだと述べています。
  - FAQ によると、プールはすべての必須の特許を含むとは限りません。権利者の参加は任意で、利用者はプールを使わずに、権利者と個別に契約してもよい、とあります。
  - 前身の MPEG LA は、2017-06-28 に EVS Patent Portfolio License を出しています。そのときの権利者は Fraunhofer、NTT、NTT DOCOMO、Panasonic、Ericsson でした。このニュースには、年に 5 万台までの EVS 製品は無償、最初の期間は網の機器の料金を免除、とあります。このライセンスが今も有効か、今のプールとどういう関係かは、Via LA のページには書かれていません。
  - Via LA の「Licensing Programs」の一覧に、AMR と AMR-WB のプールはありません。サイトの検索でも、AMR と AMR-WB は 1 件も当たりませんでした。
- このプロジェクトへの当てはめ。公開のページで分かるのはここまでです。
  - (a) 符号化も復号もせず、符号化済みのフレームを RTP で運ぶだけの場合（今の `internal/evs`）。Via LA のページは、ライセンスの対象を「標準を実装する製品」として機器の例で書くだけです。フレームを運ぶだけのソフトウェアが対象に当たるかは、書かれていません。公開のページからは決められません。
  - (b) 符号器や復号器を配る場合。標準の一覧には参照コードも入っていますが、ソフトウェアを配ること、とくに無償で配ることに料金がかかるかは、書かれていません。料金は製品の台数で決まる形です。これも公開のページからは決められません。
  - 法律の判断ではありません。参照コードやプールをどう扱うかは、プロジェクトの方針として決める必要があります。
- Wireshark には EVS の解析器があります（tshark 4.2.2 の `tshark -G protocols` に `evs` があり、設定に `evs.hf_only` があります）。RTP のペイロードの形式を読むだけで、復号はしません。

確かめられなかったことは、最後の章に書きました。Via LA のプールが、フレームを運ぶだけのソフトウェアや無償の配布に当たるか、プールに入っていない権利者の特許、EVS のオープンソースの実装の有無です。

### 設計の候補

VoLTE の設計（[VoLTE のアクセス網の設計](../design/volte-access.md)）は、AMR と AMR-WB を「符号化済みのフレームを使い、RTP のペイロードの処理は Go だけで書く」と決めました。製品は符号器も復号器も含みません。EVS も同じ形にできます。違いは、フレームを作る道具です。

| 案 | 中身 | 良い点 | 悪い点 |
| --- | --- | --- | --- |
| A. 符号化済みの EVS Primary のフレーム | 3GPP の参照コード（TS 26.443）を手元でビルドし、試験音から EVS の格納形式のファイルを作る。UE は Go だけで書いたパケット化で RTP に載せる。参照コードはリポジトリに入れず、道具は仕様の zip を取ってきてビルドする | NG.114 の構成（A1 など）のとおりの音声が流れる。Wireshark で EVS Primary として読める。送ったフレームと受けたフレームをビットで比べられる | 参照コードには、著作権の表示（書面の許可なしに複製できない）と、基本演算のファイルの ITU-T のライセンスの注記のほかに、利用を許す記載が無い。VoiceAge は EVS の特許を持ち、満了は 2030 年までの族がある。Via LA の EVS のプールは製品 1 台ごとの料金で、フレームのファイルや無償の道具に当たるかは書かれていない。作ったフレームのファイルをリポジトリに入れてよいかも分からない。受けた音声を聞くには、参照の復号器が要る |
| B. EVS AMR-WB IO だけを使う | VoLTE の AMR-WB のフレーム（vo-amrwbenc、Apache-2.0）を、EVS のペイロード形式（AMR-WB IO の Compact か Header-Full）で運ぶ。SDP は NG.114 のとおりに EVS を offer する | 新しい符号器が要らない。参照コードの利用条件の問いが増えない。受けた音声は AMR-WB として聞ける | `evs-mode-switch=1` を交渉できないので、セッションの始めから AMR-WB IO を送るのは TS 26.445 A.3.1 に合わない。SIMPLIFIED になる。超広帯域の音声ではない。Via LA のプールの「EVS の標準」には、AMR-WB IO の仕様（TS 26.446）とペイロード形式の仕様（TS 26.445）も入る。運ぶだけで対象に当たるかは書かれていない |
| C. 大きさだけが正しいフレーム | UE が、選んだレートの大きさで、中身が決まった模様のフレームを送る | 符号器が要らない。SDP と RTP の形は学べる | 音声として聞けない（通話中に音声が双方向に流れ、聞いて確認できることが要件）。Wireshark では正しく見えるが、復号すると雑音になる |
| D. 製品に符号器を入れる | 参照コードか、その派生を cgo で組み込む | 任意の音声を送れる | VoLTE の設計の「製品は cgo を使わない」に反する。利用条件と特許の問いが最も重い。Via LA のプールの料金は、標準を実装する製品の台数ごとにかかる形で、無償で配るソフトウェアの扱いは書かれていない |

結論は、参照コードを使うことを認めるなら A、認めないか、決まるまでは B です。どちらも製品は符号器を含まず、RTP のペイロード形式（`internal/evs`）は同じものを使えるので、B で作り始めて、あとから A のフレームに替えられます。D は勧めません。

どちらの案でも、Go で書くのは次のものです。

- `internal/evs`。Compact と Header-Full のパケット化と復元、ToC と CMR のバイト、Table A.1 の大きさの表、格納形式の読み書き。`internal/amr` と同じ形にします。
- `internal/sdp` の EVS の fmtp（`br`、`bw`、`mode-set`、`ch-aw-recv`、`max-red`）と、NG.114 の表（3.2.2.3 の Table 10）による answer の選び方。
- UE の offer の順番。EVS を offer するときは、EVS、AMR-WB、AMR、PCMU、telephone-event の順にします。今の offer には EVS が無く、AMR-WB から始まります（[codecs.md](../design/codecs.md)）。TS 26.114 5.2.1.6 によると、超広帯域を示すペイロードタイプは、それより狭い帯域のものより前に並べます。EVS を広帯域まで offer するときも、ほかの広帯域のコーデックより前に並べます。この順番はその決まりに合います。

中継ノード（`internal/agw`）は RTP をそのまま通すので、変える所はありません。MRFP が音を混ぜるときに EVS をどう扱うかは、[mmtel-mrf.md](mmtel-mrf.md) の「コーデック」の方針（符号化済みのフレーム）に従います。

### このプロジェクトの EVS の方針

EVS の実装の方法について、このプロジェクトでは次の方針をとりました。

- EVS はあると良いものです。ただ、ライセンスの料金がかかりそうなら、コーデックそのものを実装する理由は小さくなります。
- 代わりに、信号を EVS に合わせる形にします。SDP の offer と answer で EVS を交渉し、運ぶ中身は AMR か AMR-WB にします。このプロジェクトでは、ユーザープレーンの中身は優先しません。

2 つ目の項目の形はやめました。この形では、UE が送れない EVS Primary のビットレートと帯域を SDP に書くことになり、SDP が UE の能力を偽るためです。EVS を offer する端末はビットレートの全範囲を扱える必要があり、端末は最初の offer に `evs-mode-switch` を書けません（TS 26.114 6.2.2.2 の Table 6.2a）。AMR-WB IO しか送れない UE には、事実に合う EVS の offer がありません。今の UE は EVS を offer せず、EVS のコーデックを用意したときに offer に加えます（[codecs.md](../design/codecs.md)）。この節の残りはやめる前の方針と、そのときに本文で確かめたことの記録です。確かめたことは、EVS のコーデックを加えるときにも使えます。

やめる前の方針を設計の候補に当てはめると、次のようになっていました。

- EVS の符号器と復号器は作らず、参照コード（TS 26.442、TS 26.443）も使わないことにしました。案 A と案 D は採りませんでした。
- 方針は案 B に当たっていました。AMR-WB のフレームを、EVS AMR-WB IO のフレームとして EVS のペイロード形式（TS 26.445 Annex A）で運び、SDP では EVS を offer し、answer する形でした。
- `internal/evs` は、AMR-WB IO の 9 つのレートと SID を、Compact と Header-Full の両方で扱え、CMR も読み書きできます。5GC の構成で足すことにしていたのは、SDP の EVS の fmtp と、UE が offer と answer を作る所でした。
- EVS のペイロードに載るのは、EVS Primary と AMR-WB IO のフレームだけです。AMR-WB IO と相互に通じるのは AMR-WB だけで、AMR（狭帯域）は通じません（TS 26.441 4 章）。そのため、方針の「中身は AMR か AMR-WB」のうち、EVS のペイロードで運べるのは AMR-WB です。AMR は今までどおり、別のペイロードタイプ（RFC 4867）で offer します。
- 案 C も採りませんでした。AMR-WB のフレームを運べば、受けた音声を聞けるためです。

この方針で、確かめる点が 1 つありました。

- AMR-WB IO のフレームしか送れない UE が EVS を offer してよいか。TS 26.445 V19.1.0 と TS 26.114 V20.1.1 の本文で、次のことを確かめました。
   - TS 26.445 A.3.1。`evs-mode-switch` が無いか 0 なら、セッションの始めは送りも受けも EVS Primary です。1 なら、両方向とも AMR-WB IO で始めます。
   - TS 26.445 A.3.3.1。offer に `evs-mode-switch` が無いとき、answer の側がそれを加えてもかまいません。offer にあるときは、answer で変えることも消すこともできません。両端がこのプロジェクトの UE なら、着信側が answer に `evs-mode-switch=1` を書けば、始めから AMR-WB IO で送り合えます。案 B の表で「`evs-mode-switch=1` を交渉できない」と書いたのは、offer の側についてです。
   - TS 26.114 6.2.2.2 の Table 6.2a。端末は最初の offer に `evs-mode-switch` を書けません。また、EVS に対応する端末は `br` の範囲をすべて扱える必要があり、offer で狭い範囲や 1 つのレートを示すことは許されています。発信側の UE は、NG.114 の構成（A1 など）で EVS Primary のレートを示しますが、そのレートでは送れません。この点は仕様に合わないので、`SIMPLIFIED:` にしました。
   - TS 26.114 6.2.2.3 の Table 6.3a。`evs-mode-switch` は、始めから AMR-WB IO で送るときや、SRVCC などでモードを切り替えるときに、MTSI MGW が使うと説明されています。answer に `evs-mode-switch` を書くときの決まりは、セッションの途中の offer と answer について書かれています。端末が最初の answer に `evs-mode-switch=1` を書いてよいかは、この表からは読み取れません。あとで本文を読み直した答えは、下の「`internal/sdp` の EVS の扱い」に書きました。
   - TS 26.114 A.14.2.1 の例では、網が offer を書き換えて `evs-mode-switch=1` を加えています。このときも `br=5.9-24.4; bw=nb-wb` は残り、`mode-set=0,1,2` が加わっています。
   - `br` と `bw` は EVS Primary のためのパラメータで、AMR-WB IO のレートを絞るのは `mode-set` です（TS 26.445 A.3.1）。offer に `br` や `bw` があってそのペイロードタイプを受けるなら、answer にも、同じものかその一部を書く必要があります（A.3.3.1）。AMR-WB IO だけで送る場合も、answer には `br` と `bw` が入ります。どの値を書くかは、NG.114 の Table 10 による answer の選び方と合わせて決めます。答えは下の「`internal/sdp` の EVS の扱い」に書きました。
   - `b=AS` は、EVS Primary と AMR-WB IO のうち大きいほうに合わせます。`evs-mode-switch` があるかどうかには左右されません（TS 26.114 6.2.5.2）。
   - 相手が本物の EVS の端末なら、`evs-mode-switch` の無い answer を返し、EVS Primary のフレームを送ってきます。CMR や RTCP の APP の要求（TS 26.114 10.2.1.11 の、AMR-WB IO から EVS Primary への切り替えの要求）で、EVS Primary に戻すよう求めることもあります。このプロジェクトの UE は、どちらにも応えられません。`SIMPLIFIED:` には、このプロジェクトの UE どうしの呼だけを扱うことを書きました。
   - NG.114 が answer の `evs-mode-switch` について何か決めているかは、読み直していません。

### `internal/sdp` の EVS の扱い

`internal/sdp` に EVS の扱いを足しました。変更は EVS のための追加だけです。既にあるファイルで変えたのは `bandwidth.go` だけで、`ASBandwidth` に EVS の場合を 2 行足しました。今まで EVS を渡すと誤りを返していた所が、値を返すようになります。それ以外の入力に対する既存の関数の振る舞いは変えていません。TS 26.445 V19.1.0 の A.3 と、TS 26.114 V20.1.1 の関わる条項を、本文で読み直しています。NG.114 は、GSMA のサイトが 403 を返したので読み直せませんでした。NG.114 については、この文書の 6 章と 7 章に書いたことに頼っています。

このうち、EVS の offer と answer を作る部分は、上の方針の 2 つ目の項目をやめたときに消しました。消したのは、NG.114 の 5 つの構成（`EVSConfigA1` から `EVSConfigB2`）、`OfferParams`、`EVSCapabilities`、`AnswerEVSParams`、`AudioAnswerEVS`、`UEEVS` と、`VoNRCodecs` の EVS です。どのノードも EVS を offer も answer もしないので、使う所が無くなりました。残したのは、EVS のペイロードタイプを読む部分です（`EVSParams`、`ParseEVSParams`、`Codec.EVSParams`、`Codec.EVSFormat`、`EVSParams.StartMode`、`ASBandwidth` の EVS の場合）。下の「作ったもの」から「`SIMPLIFIED:` にしたこと」までは、消したコードを作ったときの記録です。消したコードは Git の履歴にあります。

#### 本文で確かめたこと

- TS 26.445 A.3.1。各パラメータの値の範囲です。`br` の値は 5.9 から 128 までの 12 の値で、範囲なら前の値が小さくなければなりません。`bw` は `nb`、`wb`、`swb`、`fb` と、`nb` から始まる 3 つの範囲だけです。`br` と `br-send` や `br-recv` が両方あるときは同じ値でなければならず、`bw` も同じです。`dtx` と `dtx-recv` も、両方あるときは同じ値です。`cmr` は -1、0、1、`ch-aw-recv` は -1、0、2、3、5、7 です。`mode-set` は AMR-WB IO の 0 から 8 のモードの並びです。`mode-change-capability` は、AMR-WB IO では 2 だけが許されます。`max-red`、`mode-change-period`、`mode-change-neighbor` は RFC 4867 を参照していて、RFC 4867 8.2 で値を確かめました。定義されていないパラメータは、受けた側が無視します。
- TS 26.445 A.3.2。`a=rtpmap` の時刻の周波数は 16000 です。チャネル数は書くか省きます（省けば 1）。
- TS 26.445 A.3.3.1。answer の決まりです。
  - `dtx`、`hf-only`、`evs-mode-switch`、`cmr` は、offer に無ければ answer で加えてよく、offer にあれば変えることも消すこともできません。
  - `br` と `bw` は、offer にあれば answer にも書き、同じ値かその一部にします。範囲を狭めるときは、下の値は上げてよく、上の値は下げてよいです。逆はできません。1 つの値が offer されたら、受けるならその値のまま返します。
  - `br-send` を受けたら answer に `br-recv` を書き、`br-recv` を受けたら `br-send` を書きます。`bw-*` と `ch-*` も同じように入れ替えます。
  - 使う `br` と `bw` の組み合わせは Table A.6 に合わなければなりません。狭帯域は 24.4 まで、超広帯域は 9.6 から、全帯域は 16.4 からです。
- TS 26.445 A.3.3.3 の Table A.7。`dtx` と `dtx-recv` の組み合わせの一覧です。
- TS 26.114 5.2.1.6。超広帯域を示すペイロードタイプは、それより狭い帯域のものより前に並べます。広帯域まで offer する EVS も、ほかの広帯域のコーデックより前です。
- TS 26.114 6.2.2.2 の Table 6.2a。端末の最初の offer に `evs-mode-switch`、`dtx`、`mode-set`、`mode-change-period`、`mode-change-neighbor` は書けません。`dtx-recv` は、端末はいつも書きません。`max-red` は必ず書き、220 以下にします。EVS と AMR-WB は別のペイロードタイプで offer します。
- TS 26.114 6.2.2.3 の Table 6.3 から Table 6.3c。端末に求められるのは 1 チャネルだけで、複数のチャネルの offer は断ってよいです。answer の `max-red` は 20 の倍数で必ず書きます。`mode-change-period` は書かないほうがよく、`mode-change-neighbor` は書きません。AMR-WB IO の `mode-change-capability` は書かなくてかまいません。
- TS 26.114 6.2.5.2。EVS の `b=AS` は、含まれる EVS Primary の最も高いレートと、AMR-WB IO の最も高いモードのうち、大きいほうに合わせます。`evs-mode-switch` には左右されません。EVS Primary のレートは `br` か `br-recv` の最も高い値で、どちらも無ければ帯域で決まります（狭帯域なら 24.4、それより広ければ 128）。5.9（SC-VBR）は 8 として数えます（NOTE 2）。AMR-WB IO は、octet-aligned の AMR-WB と同じに数えます（NOTE 3）。
- TS 26.114 Annex Q。EVS Primary の `b=AS` は、CMR のバイト、フレームごとの ToC のバイト、フレームを足して数えます（Q.3）。Table Q.1（IPv4、ptime 20）では、7.2 は 25 です。Compact の 8 kbit/s と同じ大きさになるので、1 バイトのパディングを足しているためです（Q.4 の NOTE 3）。Table 6.9 は同じ 7.2 を 24 としていて、パディングを数えていません。`hf-only=1` ならパディングは付かないので、24 になります。
- TS 26.114 A.14。EVS の offer と answer の例です。`a=fmtp` のパラメータの並びと `b=AS` の値を、試験で比べました。

#### 作ったもの

| 追加 | 中身 |
| --- | --- |
| `EVSParams`、`ParseEVSParams`、`String`、`Validate` | A.3.1 のパラメータの型。値の範囲と Table A.6 を確かめ、知らないパラメータは残します。書き出す順番は A.14 の例に合わせました |
| `Codec.EVSParams`、`Codec.EVSFormat` | `AMRFormat` と同じ形です。`hf-only=1` を `evs.Format{HeaderFullOnly: true}` にします。複数のチャネルは断ります。`internal/evs` が dual-mono を扱わないためです |
| `EVSConfigA1` から `EVSConfigB2`、`OfferParams` | NG.114 3.2.2.3 の 5 つの構成と、最初の offer の `a=fmtp`（`br`、`bw`、`max-red` だけ） |
| `VoNRCodecs` | EVS を先頭に置き、そのあとに `VoLTECodecs` をそのまま並べます。EVS のペイロードタイプは 102 です |
| `EVSCapabilities`、`AnswerEVSParams` | A.3.3.1 と Table 6.3 から Table 6.3c による answer の `a=fmtp`。断るときは `ErrEVSNotAnswerable` を返します |
| `AudioAnswerEVS` | `AudioAnswer` と同じ選び方で、EVS には `AnswerEVSParams` を使います。断った EVS は飛ばして、次の AMR-WB を選びます。EVS を持たない側なら、`AudioAnswer` と同じ answer になります |
| `ASBandwidth` の EVS の場合 | 上の 6.2.5.2 と Annex Q のとおりです。AMR と PCMU の結果は変わりません |
| `EVSParams.StartMode`、`UEEVS` | セッションの始めの動作と、このプロジェクトの UE の使い方 |

#### 決めたこと

- answer の `br` と `bw` は、offer と自分の構成の共通部分にしました。5 つの構成のどの 2 つから共通部分を取っても、結果は 5 つのどれかになります。たとえば A2 と B2 なら B2、A1 と B2 なら B1、A1 と B0 なら B0 です。NG.114 の Table 10 そのものは今回読めませんでした。表の組み合わせとこの操作が一致するかは、確かめていません。
- 選んだ結果が A1、B0、B1 なら、answer に `mode-set=0,1,2` を付けます。6 章に記録した NG.114 3.2.2.3 の決まりです。offer に `mode-set` があれば、そのまま返します（Table 6.3）。
- `max-red` は offer でも answer でも 0 にしました。AMR の offer（`amrFmtp`）と同じで、UE は冗長なフレームを送らないためです。Table 6.2a の「220 以下」に合います。
- `b=AS` を数えるときは、`internal/evs` が実際にパケットにする大きさを使いました。`hf-only` によってパディングが変わることも、そのまま反映されます。
- UE の構成は A2 にしました。TS 26.114 A.14.1.3 が NR と E-UTRAN の例に使っている構成で、`mode-set` が付かないので AMR-WB IO のすべてのモードを使えます。

#### 残っていた 2 つの点への答え

1. 端末が最初の answer に `evs-mode-switch=1` を書いてよいか。
   - TS 26.445 A.3.3.1 は、offer に `evs-mode-switch` が無ければ、answer の側が加えてよいとしています。端末か網かの区別はありません。
   - TS 26.114 が端末に禁じているのは、最初の offer に書くことだけです（Table 6.2a と Table 6.3a）。answer に書くことを禁じる文はありません。Table 6.3a の説明の欄は、このパラメータを MTSI MGW が使う場面を挙げていますが、使い方の例として書かれていて、制限の形にはなっていません。answer に書いたときの決まり（answer の側がその動作で送る）は「セッションの途中」の場合として書かれていて、最初の answer を外す文もありません。
   - 答え。読んだ本文の範囲では、書いてよいと読めます。禁じる文が無い、という読み方です。NG.114 がこの点に触れているかは、確かめられていません。
   - 実装。着信側の UE は `evs-mode-switch=1` を加えます。両端がこのプロジェクトの UE なら、両方向とも始めから AMR-WB IO で送ります。offer に `evs-mode-switch=0` があれば、answer で変えられないので、その EVS のペイロードタイプを断り、次の AMR-WB を選びます。この動きは仕様に合います。
2. AMR-WB IO だけを送る answer に、どの `br` と `bw` を書くか。
   - A.3.3.1 により、offer に `br` や `bw` があって受けるなら、answer にも同じかその一部を書く必要があります。省くことはできません。
   - `br` と `bw` は EVS Primary のためのもので、AMR-WB IO のモードを絞るのは `mode-set` です（A.3.1）。動作はセッションの途中で適応によって変わりえます（Table 6.3a の注）。EVS に対応する端末は `br` の全範囲を扱える必要があります（Table 6.2a と Table 6.3b）。値を狭めても「EVS Primary を送れない」ことは表せません。
   - 答え。本物の EVS の端末と同じ値を書くのが合います。つまり、offer と自分の構成の共通部分です。`b=AS` は EVS Primary と AMR-WB IO の大きいほうで決まるので、ここでも変わりません。
   - 実装。A2 どうしなら、answer は `br=5.9-24.4; bw=nb-swb; evs-mode-switch=1; max-red=0` で、`b=AS` は 42 です。本物の EVS の端末が同じ構成で返す answer との違いは、`evs-mode-switch=1` です。

#### `SIMPLIFIED:` にしたこと

`internal/sdp/evsoffer.go` の `UEEVS` に、次の 2 つを書いていました。どちらも `UEEVS` と一緒に消しました。今は `internal/sdp/audio.go` の `VoNRCodecs` の `SIMPLIFIED:` が、offer に EVS が無いことを書いています。

- UE は、送れない EVS Primary のレートと帯域を offer し、answer します。TS 26.114 の Table 6.2a と Table 6.3b は、EVS に対応する端末に `br` の全範囲を求めています。このプロジェクトでは EVS を信号だけに使い、中身は AMR-WB にしているためです。
- このプロジェクトの UE どうしの呼だけが動きます。発信側の UE が始めから AMR-WB IO で送れるのは、answer に `evs-mode-switch=1` があるときだけです。本物の EVS の端末は `evs-mode-switch` の無い answer を返し、EVS Primary で始めます。セッションの途中で EVS Primary への切り替えを求められても（Table A.3 の CMR、TS 26.114 10.2.1 の RTCP-APP）、UE は応えられません。

#### 試験と確認

EVS の offer と answer を作るコードを足したときの確認です。

- A.3.1 の値と組み合わせ、A.3.3.1 の answer の決まり（断る場合を含む）、NG.114 の 5 つの構成の offer、Table Q.1、Table Q.3、Table 6.9、A.14 の例の `b=AS` を、表の形の試験にしました。A.3.3.1 の answer の決まりと NG.114 の構成の offer の試験は、そのコードと一緒に消しました。
- `VoLTECodecs`、`AudioOffer`、`AudioAnswer` の出力が変わっていないことを確かめました。EVS を足す前のコードで、offer 1 つと answer 5 つ（今ある試験の offer と、EVS を先頭に置いた offer）をバイト列で書き出し、試験（`evsguard_test.go`）に入れました。足したあとも同じでした。EVS を足す前からある試験のファイルは変えず、すべて通りました。今の `evsguard_test.go` は VoLTE の offer と answer をバイト列で比べる試験です。EVS を先頭に置いた offer に、EVS を持たない UE が AMR-WB で answer することも、この試験で確かめています。
- UE どうしの offer と answer を SIP の INVITE と 200 に入れて pcap にし、tshark 4.2.2 で読みました。`a=fmtp` のパラメータが EVS のものとして 1 つずつ読め、誤りの表示はありませんでした。

#### 確かめられなかったこと

- NG.114 の Table 10（answer の選び方）と、NG.114 が answer の `evs-mode-switch` に触れているか。`www.gsma.com` が 403 を返しました。
- TS 26.114 7.5.2.1（ペイロード形式の既定の使い方）は読んでいません。
- offer に正の `ch-aw-recv` があると、受けた側は 13.2 kbit/s の EVS Primary で channel-aware の冗長を送ることになります。この UE は EVS Primary を送らないので、何もしていません。

## 8. アクセス網の境界に 2 つ目の実装を足す

VoLTE の構成は、P-CSCF とアクセス網の結びつきを 1 つの境界の後ろにまとめています。P-CSCF の側は `policy.PolicyControl` で、UE の側は `AccessNetwork` です（調査の時点では、UE の側は入っていません）。5GC の実装は、この 2 つの境界に 2 つ目の実装として入ります。

| 境界 | VoLTE の実装 | 5GC の実装 |
| --- | --- | --- |
| P-CSCF の `policy.PolicyControl` | `internal/rx` の `Client`（Diameter） | `internal/n5` のクライアント（HTTP/2） |
| 網からの通知（`policy.Notify`） | `rx.Client.Handle` が RAR と ASR を受ける | `internal/n5` の HTTP のハンドラが notify と terminate を受ける |
| SDP からサービスの情報を作る処理 | `internal/policy/sdp.go`（TS 29.213 6.2） | 同じものを使う。TS 29.513 7.2.3 の表との違いを確かめてから |
| アクセス網のスタブ | 役割 `epc`（`internal/epc`。PCRF も同じパッケージ） | 役割 `5gc`（`internal/fivegc`。PCF も同じパッケージ） |
| UE の `AccessNetwork` | `internal/epc/client.go` を使う実装 | `internal/fivegc` のクライアントを使う実装 |
| I-CSCF と S-CSCF の HSS への境界 | Cx（Diameter のメッセージの高さ） | 境界を操作の高さに上げてから、Nhss を足す |

### `policy.PolicyControl` に N5 が入るか

今の `internal/policy/policy.go` を、N5 の操作と 1 つずつ照らしました。

| `policy` の要素 | N5 での実現 | 判定 |
| --- | --- | --- |
| `SubscribeSignallingPath(ctx, ue)` | 信号用の Individual Application Session Context を作る（TS 29.514 4.2.6.7） | そのまま入る |
| `AuthorizeMedia` で `Session` が空 | `/app-sessions` への POST（4.2.2.2） | そのまま入る |
| `AuthorizeMedia` で `Session` がある | 資源への PATCH（4.2.3.2） | そのまま入る |
| `Release` | `…/delete` への POST（4.2.4.2） | そのまま入る |
| `SessionID`（文字列） | Location で返る資源の URI | そのまま入る。コメントは Rx の Session-Id だけを挙げている |
| `MediaRequest.UE` | `ueIpv4` | そのまま入る |
| `MediaRequest.SDP` と `MediaComponents` | `medComponents` | 表の違いを確かめれば入る |
| `MediaRequest.ApplicationID` | `afAppId`（B.1） | そのまま入る |
| `ErrIPCANSessionNotAvailable` | 500 と `PDU_SESSION_NOT_AVAILABLE` | 入る。名前は EPS の言葉 |
| `ErrRequestedServiceNotAuthorized` | 403 と `REQUESTED_SERVICE_NOT_AUTHORIZED` | そのまま入る |
| `SessionTerminated` | terminate の通知（4.2.5.3）と、そのあとの delete | そのまま入る |
| `SuccessfulResourcesAllocation`、`FailedResourcesAllocation` | 同じ名前の `AfEvent`（4.2.5.8） | そのまま入る |
| `LossOfBearer`、`ReleaseOfBearer` | 同じ名前の事象が無い。SDF が止まったことは `FAILED_RESOURCES_ALLOCATION` で届く（4.2.5.5） | 写し方を決める必要がある |
| `Notify`（応答を返したあとで呼ぶ） | HTTP の応答（204）を返したあとで呼ぶ | そのまま入る |

境界の形は、変えずに N5 を載せられます。決める必要があるのは 1 つです。P-CSCF が `LossOfBearer` と `ReleaseOfBearer` を申し込んだとき、N5 の実装は `FAILED_RESOURCES_ALLOCATION` を申し込みます。その通知が届いたとき、どの `EventKind` で P-CSCF に渡すかです。結論は、資源の確保に成功したあとの通知を `ReleaseOfBearer`、成功する前の通知を `FailedResourcesAllocation` として渡すことです。P-CSCF の動き（TS 24.229 5.2.8）は、確立中か確立済みかで分かれるので、この写し方で Rx のときと同じ動きになります。N5 の実装が「確保に成功したか」をセッションごとに覚える必要があり、置き場所は P-CSCF の `Store` です。

N5 の実装は、`rx.NewClient` が `policy.Notify` を受けるのと同じ形で作れます。違いは、通知の入口です。Rx は Diameter のノードのハンドラ（`cmd/ims/main.go` の `diameterHandler`）につなぎますが、N5 は HTTP のサーバーにつなぎます。境界は変わらず、`cmd/ims/main.go` のつなぎ方が増えるだけです。

### UE の側

UE の `AccessNetwork` は、5GC の実装が入りやすい形にします。要点は 3 つです。

- インタフェースに、ATTACH や bearer のような EPS の言葉を出さない。「つなぐ」「切る」「網からの通知を受ける」の高さにし、EPS と 5GS の手順の違いは実装の中に置きます。
- つないだ結果として、UE の IP アドレス、P-CSCF の一覧、P-Access-Network-Info の値を返す。E-UTRAN と NR では、access-type と、セルの識別子の桁数が違うためです。
- 網からの通知は、「音声用の資源ができた」「音声用の資源が無くなった」「接続が切れた」の 3 つにまとめる。EPS では dedicated bearer の活性化と解放、5GS では PDU SESSION MODIFICATION COMMAND と PDU SESSION RELEASE COMMAND が、これに当たります。
- `epc.ProtocolConfigurationOptions` と `epc.Container` は、5GC のスタブからも使える場所に置く。ePCO の中身は PCO と同じ符号化（TS 24.008 10.5.6.3A）なので、同じ型を使えます。`internal/fivegc` が `internal/epc` を取り込む形は避けたいためです。

## 9. 確かめる場面と簡略化する点

Compose の構成では、pcap から次のことを確かめます。目標は、Rx を使わない構成で登録と通話が成功することです。

- pcap に Diameter の Rx（Application-ID 16777236）が 1 つも無い。
- 登録のあと、P-CSCF から PCF への `POST /npcf-policyauthorization/v1/app-sessions` と 201 がある。本文の `flowUsage` が `AF_SIGNALLING` である。
- 呼で、2 つの P-CSCF がそれぞれ POST を送り、本文に `medComponents` と `ueIpv4` がある。
- 切断で、`…/delete` への POST と 204 がある。
- 異常系で、PCF から P-CSCF への `…/notify` か `…/terminate` のあとに、P-CSCF が BYE を送る。
- Nhss を使う構成では、`/nhss-ims-uecm/v1/…/authorize`、`/nhss-ims-ueau/v1/…/generate-sip-auth-data`、`/nhss-ims-uecm/v1/…/scscf-registration` の往復がある。
- SDP の offer に AMR-WB と AMR があり、EVS が無い（UE が EVS を送れないため）。

簡略化する見込みの点は次のとおりです。

- 5GC のスタブ。NAS、NGAP、GTP-U、N6 を省き、UE とは HTTP と JSON で話す。5G-AKA とセキュリティモードを省く。
- QoS flow。帳簿だけで、QoS の強制をしない。
- N7。PCF と SMF のスタブのあいだを Go の呼び出しにする。
- PCF と HSS の選び方。NRF と BSF を使わず、設定した接続先に送る。
- SBI の TLS と OAuth 2.0 を省く。
- EVS。UE は EVS を offer せず、AMR-WB と AMR で通話する。UE に EVS のコーデックが無いためで、NG.114 の EVS の要件を満たさない（7 章の「このプロジェクトの EVS の方針」）。
- EPC との連携（NG.114 4.8）を作らない。


## 10. 未決の論点

選択肢と、その得失です。

1. Nhss を呼ぶ側まで作るか。5GC の構成では、HSS の SBI を模擬することが要件です。案は、I-CSCF と S-CSCF が設定で Cx と Nhss を選べるようにすることです。呼ぶノードが無いと、Nhss がキャプチャに現れません。そのためには、HSS への境界を操作の高さに上げる変更が要ります。
2. EVS の実装の方法。7 章の方針のうち、信号を EVS に合わせ、中身は AMR-WB のフレームを EVS AMR-WB IO として運ぶ形（案 B）は、SDP が UE の能力を偽るのでやめました。今の UE は EVS を offer しません。EVS を加えるには、EVS Primary のフレームを作る手段が要ります。案 A を選ぶなら、参照コードの利用条件を 3GPP（ETSI の Legal Service）に確かめるか、プロジェクトの責任で使うと決める必要があります。作ったフレームのファイルをリポジトリに入れるかも、あわせて決めます。判断の材料は 7 章の「利用条件と特許について、公開されている事実」にまとめました。要点は次の 4 つです。参照コードには著作権の表示があるだけで、利用を許すライセンスは書かれていません（基本演算のファイルだけは ITU-T のライセンス）。VoiceAge は EVS に当たる特許の族を公開していて、もっとも遅い満了は 2030 年です。Via LA には EVS と IVAS の特許のプール（Voice Codec）があり、7 者の権利者が入っていて、料金は機器 1 台ごとに 0.35 から 0.60 米ドルです。今のページには免除や閾値の記載が無く、フレームを運ぶだけのソフトウェアや、無償で配る符号器が対象に当たるかは、公開のページからは決められません。これは法律の判断ではありません。
3. NG.114 4.8（EPC との連携、EPS fallback）を、「IR.92 と NG.114 の必須の要件を満たす」という要件から外すか。無線区間と移動の手順が要るので、案は「対象外」に加えることです。
4. NRF を作るか。案は作らず、接続先を設定に書くことです。
5. 5GC のスタブで、5GS への登録の往復を模擬するか。案は模擬することです。IMS voice over PS session supported の印を学べるためです。
6. 役割の名前。案は、設定では `5gc`、Go のパッケージは `fivegc` にすることです。Go の識別子は数字で始められないためです。
7. SBI のポートを 80 番にするか。Wireshark が設定なしで HTTP/2 と読むのは、試した範囲では 80 番と 8080 番だけでした。
8. SBI のメッセージを HEP でダッシュボードに送るか。案は送ることです。シーケンス図に SIP、Diameter、SBI を並べると、Rx と N5 の違いが見やすくなります。
9. 1 つの P-CSCF が Rx と N5 の片方だけを使う形でよいか。4G と 5G をまたぐ UE を扱うなら、UE ごとに選ぶ形が要ります。

## 確かめられなかったこと

- GSMA NG.113 を読んでいません。IMS の DNN の値（`ims`）は確かめていません。
- TS 23.502 は 4.3.2.2.1 の 2 つの step だけを読みました。PDU セッションの変更（4.3.3.2）で音声の QoS flow が加わる流れ、解放（4.3.4.2）、PCF が始める SM Policy の変更（4.16.5.2）は見出しだけです。
- TS 24.501 は、P-CSCF に関わる段落と IE の表だけを読みました。QoS rules（9.11.4.13）と QoS flow descriptions（9.11.4.12）の符号化、5GSM の原因の値、登録の手順（5.5）は読んでいません。
- TS 29.514 の 4.2.5.2（事象の通知の本文の決まり）、4.2.6.3、5.5（通知の URI の決まり）、5.7.3（アプリケーションの誤りの一覧）は読んでいません。terminate の通知への応答が 204 であることは、OpenAPI の callbacks で確かめました。notify の応答は確かめていません。
- TS 29.513 の Table 7.2.3-1 は、表題と最初の数行だけを読みました。Rx の TS 29.213 6.2 の表と同じ規則かどうかを確かめていません。同じなら、`internal/policy/sdp.go` をそのまま N5 に使えます。Annex B（IMS の信号の流れ）は見出しだけです。
- TS 29.562 は、3 章の表で条項を確かめていない操作（ImsSDM_Get の各資源、Subscribe、Notification、repository-data）の本文を読んでいません。経路は OpenAPI で確かめました。
- LIR に当たる Nhss の呼び出し方。TS 23.228 AA.2.2.2 の表は Authorize を挙げていますが、OpenAPI の `authorizationType` の値は `REGISTRATION` と `DEREGISTRATION` だけです。着信のときに何を送るかは読めていません。
- TS 29.512（N7）、TS 29.521（BSF）、TS 29.571（共通の型）、TS 29.510 の本文は読んでいません。
- TS 33.501 の 13.1（SBI の TLS）は読んでいません。TS 29.500 5.1 の要約に頼りました。
- TS 26.114 の 7.5.2.1（ペイロード形式の使い方）は読んでいません。5.2.1.4、5.2.1.6、Table 6.2a、Table 6.3a は、EVS の方針（7 章）を決めるときに読みました。
- TS 26.445 Annex A の A.2.1.1 から A.2.1.3（Compact の細部）、A.2.2.1.1 と A.2.2.1.2（CMR と ToC のビットの割り当て）は読んでいません。A.3.3.1 は、EVS の方針（7 章）を決めるときに読みました。
- NG.114 が、端末の最初の answer の `evs-mode-switch=1` に触れているか。TS 26.445 と TS 26.114 の本文で読める範囲の答えは、7 章の「`internal/sdp` の EVS の扱い」に書きました。NG.114 は、`internal/sdp` の EVS を作るときに `www.gsma.com` が 403 を返したので、読み直せていません。
- NG.114 の Table 10 の組み合わせが、offer と自分の構成の共通部分を取る選び方と一致するか。AMR-WB IO だけで送る answer の `br` と `bw` の答えは、7 章の「`internal/sdp` の EVS の扱い」に書きました。
- EVS の参照コードを、3GPP の中の作業のほかに使ってよいか。公開の資料には書かれていないので、3GPP（ETSI の Legal Service）に問い合わせないと分かりません。問い合わせていません。
- Via LA の Voice Codec のプールが、符号化済みのフレームを運ぶだけのソフトウェアや、無償で配る符号器と復号器に当たるか。公開のページは機器の例と台数ごとの料金を書くだけです。「In-Compliance」の料金の条件と、契約書そのものも、公開のページにはありません。問い合わせていません。
- 2017 年の MPEG LA の EVS Patent Portfolio License（年 5 万台までの無償と、網の機器の免除があった）が今も有効か。Via LA のページには書かれていません。
- AMR と AMR-WB の特許のプールが、Via LA のほかにあるか。Via LA には無く、VoiceAge のページもプールに触れていません。ほかの管理会社のサイトは調べていません。
- プールに入っていない権利者の EVS、AMR、AMR-WB の特許と、その満了の年。VoiceAge は Via LA の権利者の一覧に入っておらず、VoiceAge の表は 1 社の分だけです。AMR の VoiceAge の特許の満了の年も確かめていません。
- `www.voiceage.com` は、見直しで、試し直しても「CONNECT tunnel failed, response 502」でした。VoiceAge のページは、`www` の無い `voiceage.com` で読みました。
- EVS のオープンソースの実装があるか。調べていません。
- HEP3 に HTTP/2 か JSON の SBI を運ぶ型があるか。
- Wireshark が、NAS を伴わない HTTP/2 の JSON を、3GPP の SBI として細かく読むか（`http2` の設定に 5GC 向けの項目があるか）は確かめていません。JSON としては読めました。
- Windows の Docker Desktop で、コンテナが 80 番で待ち受けられるか。Linux のコンテナなので問題ない見込みですが、試していません。
- TTC の標準に、5GS の音声に関わるものがあるか。`www.ttc.or.jp` は 503 を返しました。[profiles.md](profiles.md) のとおり、TTC の標準は網のあいだの接続を決めたもので、5GC の構成の範囲（端末と網、アクセス網とコア）には直接当たらない見込みです。
