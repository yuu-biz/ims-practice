# VoLTE の調査（EPC スタブ、PCRF と Rx、プリコンディション、IMS-AKA、IR.92）

この文書は、VoLTE の呼に必要なアクセス網、PCC、プリコンディション、IMS-AKA、IR.92 の必須の要件について、仕様が定めることと、この実装の作りを調べた記録です。決めた設計は [docs/design/volte-access.md](../design/volte-access.md) にあります。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは版も書きます。版を書いていない条項は「読んだ資料」の表にある版のものです。

各章の「今のコードとの差」と、5 章の表の「今のコード」の列は、実装を始める前のコードとの差の記録です。埋めた差と残っている差は [profiles.md](profiles.md) の表にあります。各章の「設計の候補」は調べた時点の案で、決めた設計との違いは 8 章に書きます。

## 読んだ資料

| 仕様 | 版 |
| --- | --- |
| TS 23.401（EPC） | V20.1.0 |
| TS 23.203（PCC の枠組み、QCI） | V20.1.0 |
| TS 24.008（PCO の符号化） | V20.1.0 |
| TS 24.301（EPS の NAS） | V20.1.0 |
| TS 24.229 | V20.1.0 |
| TS 24.173（MMTel） | V19.0.0 |
| TS 26.114（MTSI のメディア） | V20.1.1 |
| TS 29.212（Gx） | V20.0.0 |
| TS 29.213（PCC の信号の流れ、QoS の対応づけ） | V19.3.0 |
| TS 29.214（Rx） | V20.0.0 |
| TS 29.228、TS 29.229（Cx） | V20.0.0、V19.1.0 |
| TS 33.102（3G のセキュリティ、AKA） | V19.1.0 |
| TS 33.203（IMS のアクセスセキュリティ） | V20.0.0 |
| TS 35.206、TS 35.208（Milenage、試験データ） | V19.0.0、V19.0.0 |
| GSMA IR.92 | v24.0 |
| GSMA NG.114 | v11.0 |
| RFC 3310、3312、3329、3556、4032、4169 | |

RFC 3262、3311、3326、3550、4028、4733、4867、5009、6228 は、節の構成と、IR.92 と TS 24.229 が引いている箇所だけを読みました。SIPp は Alpine の版と同じ v3.7.7 のソース（`src/auth.cpp`、`src/milenage.c`、`src/message.cpp`）を読みました。Wireshark は手元の tshark 4.2.2 の既定の設定（`tshark -G heuristic-decodes`、`tshark -G defaultprefs`）を見ました。

## 全体の流れ

この実装で学習者が追える VoLTE の呼は次のとおりです。項目ごとの根拠は後の節に書きます。

1. UE は EPC スタブに IMS の APN への接続を求めます。スタブは UE の IP アドレスと、P-CSCF のアドレスの一覧（PCO）を返し、QCI 5 の default bearer を作ります。
2. UE は Security-Client を付けた REGISTER を P-CSCF に送ります。S-CSCF は MAR で HSS から認証ベクトル（AV）を受け、401 に RAND と AUTN を入れます。P-CSCF は 401 から CK と IK を外し、Security-Server を足して UE に送ります。
3. UE は保護ポートに 2 回目の REGISTER を送り、200 を受けます。P-CSCF は Rx の AAR で、信号の経路の状態の通知を PCRF に申し込みます。
4. 発信では INVITE、183、PRACK、UPDATE、180、200、ACK が流れます。P-CSCF は SDP answer を見て AAR を送り、PCRF スタブが QCI 1 の dedicated bearer を作って UE に知らせます。UE はそれを資源の確保とみなし、UPDATE で相手に知らせます。
5. 通話中は AMR か AMR-WB の RTP と、RTCP が中継ノードを通ります。DTMF は telephone-event で送ります。
6. 切断の BYE には Reason を付けます。P-CSCF は STR で Rx のセッションを終え、PCRF スタブは dedicated bearer を消します。
7. 異常系として、スタブから dedicated bearer を落とすと、PCRF が RAR か ASR を送り、P-CSCF が BYE で呼を切ります。

## 1. EPC スタブ

### 仕様が求めること

- UE は IMS と通信する前に EPS に attach し、SIP の信号に使う EPS bearer context を用意し、P-CSCF のアドレスを得ます（TS 24.229 L.2.2.1）。P-CSCF を見つける方法は 4 つあり、方法 II は bearer の活性化の中でアドレスを受け取る方法です。IR.92 4.5 は方法 II を求めます。
- 方法 II では、UE が PDN CONNECTIVITY REQUEST の PCO で P-CSCF のアドレスを求め、ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST の PCO で一覧を受け取ります。一覧は先頭ほど優先度が高いとみなします（L.2.2.1）。
- PCO の P-CSCF IPv4 Address Request と P-CSCF IPv4 Address は、どちらもコンテナの識別子が 000CH です。要求は中身が空で、応答は 1 つのコンテナに 1 つのアドレスを入れ、複数あるときはコンテナを並べます（TS 24.008 10.5.6.3）。
- IP アドレスは default bearer の活性化のときに割り当てます。HPLMN が動的または静的なアドレスを割り当てる方法などがあり、同じ PDN 接続の dedicated bearer も同じアドレスを使います（TS 23.401 5.3.1.1）。
- IMS の APN の default bearer は QCI 5 で、SIP の信号に使います（IR.92 4.4.1）。音声には、網が dynamic PCC で QCI 1 の dedicated bearer を作ります。音声の dedicated bearer は 1 つだけで、複数の呼の音声をその 1 つで運びます。呼が終わると網が消します（IR.92 4.4.2）。QCI 1 は GBR の Conversational Voice、QCI 5 は non-GBR の IMS Signalling です（TS 23.203 6.1.7 Table 6.1.7-A）。
- 網が始めた dedicated bearer を受けた UE は、TFT を見て、進行中の SIP セッションと対応づけます（TS 24.229 L.2.2.5.1B）。網が bearer を作るのは、P-CSCF が Rx で IP フローを承認し、QoS の要求を PCRF に渡したあとです（L.2.2.5.1 NOTE 3）。
- dedicated bearer が無いあいだ、UE は自分の資源が無いとみなし、メディアを送りません（IR.92 2.4.3.1）。
- UE は TS 24.229 5.1 が示す場所で、必ず P-Access-Network-Info を付けます（L.3.1.1）。E-UTRAN では access-type を 3GPP-E-UTRAN-FDD か 3GPP-E-UTRAN-TDD にし、utran-cell-id-3gpp に MCC、MNC、TAC（EPC では 16 進 4 桁）、ECI（16 進 7 桁）を続けて書きます（7.2A.4.3 item 12）。
- 手順の条項は、attach が TS 23.401 5.3.2.1、PDN 接続の追加が 5.10.2、dedicated bearer の活性化が 5.4.1、PDN GW が始める bearer の解放が 5.4.4.1 です（5.10.2 と 5.4.4.1 は目次で確かめただけです）。attach の中で PDN GW は IP-CAN Session Establishment を行い、PCRF から default の PCC rule を受けます（5.3.2.1 step 14）。
- NAS のメッセージは TS 24.301 の 8.3.20（PDN CONNECTIVITY REQUEST）、8.3.6（ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST）、8.3.3（ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST）、8.3.12（DEACTIVATE EPS BEARER CONTEXT REQUEST）です。PCO は 9.9.4.11、TFT は 9.9.4.16 です。
- Gx（TS 29.212）では、PCEF（PDN GW）が IP-CAN session の確立時に CCR（INITIAL_REQUEST）を送ります（4.5.1）。PCRF は Rx で受けた情報をきっかけに、PCEF の要求を待たずに PCC rule を送れます（4.5.2.0 の PUSH）。

### 今のコードとの差

- アクセス網のスタブがありません。UE は P-CSCF のアドレスを設定で受け取っています（`internal/ue` の `Config.PCSCF` にある SIMPLIFIED: TS 24.229 9.2.1）。手順書 registration.md の簡略化の表にも同じ行があります。
- UE は P-Access-Network-Info を付けていません。S-CSCF は受けたものを第三者 REGISTER に写すだけです。
- IMSI、IP アドレス、bearer の対応を持つ場所がありません。PCRF がセッションを結びつけるには、この対応が要ります（2 章）。

### 設計の候補

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| スタブの置き場所 | `ims` コマンドの新しい役割 `epc`（役割は設定で切り替える）。MME、SGW、PGW をまとめた 1 つのスタブにし、PCRF のスタブを同じプロセスの別パッケージとして入れる | MME、PGW、PCRF をそれぞれ役割に分け、Gx を Diameter で実装する（拡張の候補） |
| UE との境界（差し替えられる境界） | `internal/ue` に `AccessNetwork` のインタフェースを置き、EPC スタブへの HTTP のクライアントで実装する。メッセージの名前と項目は TS 24.301 の ESM に合わせ、本文は JSON にする。NAS と S1 を省いたことを SIMPLIFIED に書く。SMS over NAS の作業で NAS の実装に替える | ESM を TS 24.301 の符号化で運ぶ。eNB と S1AP が無いと Wireshark で NAS として読めない |
| 網からの通知 | UE が接続のときに開いた HTTP の接続（Server-Sent Events）で、スタブが bearer の活性化と解放を送る。UE は ACCEPT に当たる要求を返す | スタブが UE の操作用 API を呼ぶ。スタブが UE の API のアドレスを知る必要がある |
| IP アドレス | 加入者ごとの静的なアドレス（TS 23.401 5.3.1.1 の静的な HPLMN のアドレス）。スタブの設定と Compose の `ipv4_address` に同じ値を書き、UE は受け取ったアドレスが自分のインタフェースにあるかを確かめる。GTP-U と SGi を省いたことを SIMPLIFIED に書く | (a) スタブがプールから払い出し、UE が TUN で使う。NET_ADMIN が要るので、Open5GS と UERANSIM の構成で扱う。(b) 要求の送信元アドレスをそのまま登録する。設定は要らないが、払い出しに見えない |
| P-CSCF の一覧 | スタブの設定に P-CSCF の IPv4 アドレスを順に書き、PCO で返す。UE は先頭から使う。冗長構成での P-CSCF の選び直しにもこの一覧を使える | DNS の名前で書き、スタブが解決して返す |
| bearer | default bearer（QCI 5）と dedicated bearer（QCI 1、GBR）を帳簿として持つ。QoS の強制とパケットの振り分けはせず、SIMPLIFIED に書く | tc などで帯域を絞る（試作が要る） |
| 異常系 | スタブの操作用 API に、dedicated bearer の解放（無線の切断の代わり）と PDN 接続の解放を置く | なし |
| 状態（各役割を複数台で動かせるようにするため） | UE の文脈、IP から IMSI への索引、bearer、Rx のセッションを `Store` の後ろに置く。通知の接続は UE を 1 台のスタブに結びつけるが、MME が UE の S1 の接続を持つのと同じ形とみなす | なし |
| Docker のネットワーク | 今の 1 つのブリッジ（ims0）のままにする。キャプチャ（ブリッジ全体を 1 つのコンテナで取る）を変えずに済む | アクセス側とコア側でネットワークを分け、P-CSCF と中継ノードを両方につなぐ。キャプチャが 2 つになる |

## 2. PCRF スタブと Rx

### 仕様が求めること

- Rx は 3GPP の vendor-specific の Diameter アプリケーションで、Application-ID は 16777236 です（TS 29.214 5.1）。コマンドは AAR と AAA が 265、RAR と RAA が 258、STR と STA が 275、ASR と ASA が 274 です（5.6）。
- AF セッションの開始（4.4.1）。メディアの情報がそろい、PCC の監視が要るとき、AF（P-CSCF）は AAR で Rx のセッションを開きます。AF セッションごとに新しい Rx のセッションを作り、信号の経路の通知に使うセッションとは分けます。
  - PCRF がセッションを結びつけられるように、AF は UE の IP アドレスを Framed-IP-Address で渡します。結びつけに失敗すると、PCRF は Experimental-Result-Code の IP-CAN_SESSION_NOT_AVAILABLE を返します。サービスを断るときは REQUESTED_SERVICE_NOT_AUTHORIZED です。
  - SDP answer をもとにした情報には Service-Info-Status の FINAL_SERVICE_INFORMATION を付けます。SDP offer をもとにした途中の情報を、PRELIMINARY_SERVICE_INFORMATION で先に送ることもできます。
  - AF は AF-Charging-Identifier と、通知を受けたい事象の Specific-Action を入れられます。
- SDP から Media-Component-Description を作る規則は、P-CSCF には必須です（TS 29.213 6.2 Table 6.2.1）。m= の行の位置が Media-Component-Number、m= のメディアの種類が Media-Type です。ポートが 0 なら Flow-Status は REMOVED で、ほかは方向の属性から決めます。Max-Requested-Bandwidth-UL と DL は b=AS の値から計算します（1000 倍、RTCP を含める場合の 1050 倍など。条件は表にあります）。b=RS と b=RR は RS-Bandwidth と RR-Bandwidth に写します。RTP と RTCP は別々の Media-Sub-Component にし、rtcp-mux なら 1 つにまとめます。コーデックは Codec-Data で渡します。
- PCRF には、audio のフローの QCI を決める規則があります（TS 29.213 6.3）。IR.92 4.4.2 は音声の bearer に QCI 1 を求めます。
- AF セッションの終わり（4.4.4）。AF は STR を送り、PCRF は STA を返してから資源を解放します。
- 信号の経路の状態の通知（4.4.5）。AF は AF の信号用の Rx のセッションを開き、UE の IP アドレスと Specific-Action（INDICATION_OF_LOSS_OF_BEARER、INDICATION_OF_RELEASE_OF_BEARER）を AAR で送ります。このとき Media-Sub-Component の Flow-Number は 0 です。IMS の復旧の機能（ProvAFsignalFlow）を使うときは、同じセッションで AF の信号の IP フローを、Media-Component-Number が 0 の Media-Component-Description で渡せます（4.4.5A）。
- 網からの通知。
  - IP-CAN session が終わると、PCRF は有効な Rx のセッションのそれぞれに ASR を送ります。AF は ASA を返し、STR でセッションを終えます（4.4.6.1）。
  - SDF の一部が止まったときは、PCRF は止まったフローを Flows に、理由を Specific-Action に入れた RAR を送ります。すべてのフローが止まったときは ASR です（4.4.6.2）。
  - 信号の bearer の資源を失ったときは RAR で知らせます。ただし QCI 5 は non-GBR なので、INDICATION_OF_LOSS_OF_BEARER は報告されない、と NOTE にあります（4.4.6.3）。
- Specific-Action の値（5.3.13）は、2 が INDICATION_OF_LOSS_OF_BEARER、3 が INDICATION_OF_RECOVERY_OF_BEARER、4 が INDICATION_OF_RELEASE_OF_BEARER、8 が INDICATION_OF_SUCCESSFUL_RESOURCES_ALLOCATION、9 が INDICATION_OF_FAILED_RESOURCES_ALLOCATION です。Abort-Cause（5.3.1）の 0 は BEARER_RELEASED です。
- P-CSCF の動き（TS 24.229）。
  - 発信側の INVITE で、資源を許可できないと IP-CAN から知らされたとき、P-CSCF は 500 を返します。P-CSCF が過負荷なら 503 です（5.2.7.2）。方針によっては、Reason の FAILURE_CAUSE（cause=1）と、Response-Source の fe=`<urn:3gpp:fe:p-cscf.orig>` を付けます（5.2.7.2）。UE はこれで、発信側の資源が無いと分かります（5.1.3.1）。
  - 確立中の呼で信号の bearer が無くなったと知らされたとき、P-CSCF は発信側の利用者のためなら CANCEL を、着信側の利用者のためなら 500 を送ります（5.2.8.1.1）。
  - 確立済みの呼でメディアの資源か信号の bearer が無くなったと知らされたとき、P-CSCF は自分で BYE を作ります。BYE には保存したダイアログの情報（相手の Contact、To と From、CSeq に 1 を足した値、Route）と、Reason を入れます（5.2.8.1.2）。
- IR.92 は PDN 接続を失ったときと、音声の GBR bearer が作れないか失われたときに、網がセッションを終えることを求めます。どちらも P-CSCF が PCRF から知らされる形です（2.4.2.1、2.4.2.3）。

Diameter の辞書について確かめたことがあります。go-diameter v4.6.0 の既定の辞書（`dict.Default`）には、Rx（TGPP_Rx。Application-ID 16777236、4 つのコマンド、Rx の AVP）と Gx が最初から入っています。このプロジェクトは `dict.Default` に Cx と Sh の XML を足して読み込むので、Rx の AVP はもう復号できます。Rx の XML を自分で書いて足すと、同じアプリケーションを 2 回読み込むことになります。

### 今のコードとの差

- P-CSCF は Diameter を話さず、pcscf の役割には Diameter の設定がありません。
- P-CSCF はダイアログを覚えていません。Call-ID ごとに中継のコンテキストを持つだけです。P-CSCF が自分で BYE を出すには、ダイアログの情報を `Store` に置く必要があります。
- P-CSCF が SDP を書き換えるのは、INVITE の offer と、その応答の answer だけです（`internal/pcscf/session.go` の SIMPLIFIED）。UPDATE の SDP も中継ノードのアドレスに書き換える必要があります。
- `internal/sdp` のパーサーは m= の後ろの b= の行を捨てます。P-CSCF が SDP を組み立て直すと、UE が書いた b=AS が消えます。

### 設計の候補

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| P-CSCF の側の境界（差し替えられる境界） | `pcscf.PolicyControl` のインタフェースを置く。登録の申し込み、呼の承認、更新、終わりと、網からの通知を受ける関数を持たせる。型はアクセス網に依らない名前（MediaComponent、Flow など）にし、Rx の実装を `internal/rx` に置く。5GC の作業で N5 の実装を足す | Rx を P-CSCF に直接書く。5GC を足すときに P-CSCF を書き直すことになる |
| Rx の辞書 | go-diameter に同梱の TGPP_Rx を使う。使う AVP のコードと旗（M、V）を TS 29.214 と照らす試験を書く。型と Parse と Message は Cx と同じ形で `internal/rx` に書く | 自分で rx.xml を書く。同梱の辞書と重なるので、読み込み方を変える必要がある |
| AAR を送るとき | SDP answer を見たときに FINAL で送る。UPDATE などで offer と answer をやり直すたびに、同じ Rx のセッションで送り直す | offer のときにも PRELIMINARY を送る。Rx の往復が増える |
| 登録のとき | 200 の後で、信号の経路の状態の通知を申し込む AAR（Flow-Number 0）を送る。結果が IP-CAN_SESSION_NOT_AVAILABLE なら、その UE の呼では Rx を使わない。ホストのソフトフォンのように EPC を通らない UE のため | Rx を使えない UE の登録を 403 で断る |
| 網からの通知 | RAR（INDICATION_OF_LOSS_OF_BEARER、INDICATION_OF_RELEASE_OF_BEARER）と ASR を受けたら、TS 24.229 5.2.8.1.1 と 5.2.8.1.2 のとおりに CANCEL、500、BYE を出す。登録の Rx のセッションに ASR が来たら、その UE の呼と IP の対応付けをすべて終える | なし |
| PCRF スタブ | AAR を受けて PCC rule を作り、PGW のスタブに dedicated bearer を作らせる。Gx は Go の関数の呼び出しにし、SIMPLIFIED に書く。bearer の結果で AAA を返し、申し込まれた Specific-Action を RAR で送る | Gx を Diameter で実装する |
| PCRF の選び方 | P-CSCF の設定に Rx の接続先を書く。DRA による選び方（TS 29.213 7.3.7）は冗長構成で扱い、SIMPLIFIED に書く | DNS で探す |
| 状態（各役割を複数台で動かせるようにするため） | P-CSCF は、Rx の Session-Id と、呼の通過ごとの中継のコンテキストとの対応、ダイアログの情報、登録の Rx のセッションを `Store` に置く。PCRF は Session-Id と UE の IP アドレスで引く | なし |

## 3. プリコンディション

### 仕様が求めること

- 仕組みは RFC 3312 で、RFC 4032 が更新しています（TS 24.229 5.1.3.1）。発信側の UE は対応するべき（should）とされ、使うときは INVITE の Supported に 100rel と precondition を入れ、Require には入れません（5.1.3.1）。
- IR.92 2.4.1 は UE に対応を求めます。プリコンディションを使うときは状態の更新に UPDATE を使い、18x の SDP で選ばれたコーデックを受けたなら、そのコーデックとその設定だけを UPDATE の SDP に入れます。
- 発信側の SDP（TS 24.229 6.1.2）。資源がまだ無いときは、segmented status type で local の状態を満たされていないものとして示します。strength は local を mandatory、remote を optional（または RFC 3312 と RFC 4032 の決まりの値）にし、着信側に確認を求めません。資源があれば local を満たされたものとして示します。
- 着信側（5.1.4.1、6.1.3）。INVITE の Supported に precondition があり、自分も資源の確保が要るときは、SDP を含む応答に Require: precondition を付けます。answer では自分の local の状態を示し、発信側に確認を求めます（RFC 3312 7 章の a=conf）。
- 資源を確保できたら、発信側はその後の最初の要求（PRACK か UPDATE）で知らせます（5.1.3.1）。UPDATE の 200 より先に 180 や INVITE の 200 が来ても、UE は誤りとして扱いません（5.1.3.1 NOTE 5）。
- 発信側がプリコンディションを使わないときでも、着信側の資源が無ければ、着信側は SDP 付きの 183 を返します。INVITE の Supported に 100rel があれば、183 は信頼性のある応答にします。資源を確保するまでは 180 を送らず、利用者を呼び出しません（IR.92 2.4.1）。着信側は、発信側がプリコンディションを使うことを前提にしてはいけません（同）。
- 方向の属性。初めの INVITE の SDP は sendrecv か方向の属性なしにし、そう offer された UE はプリコンディションや資源の状態にかかわらず sendrecv で答えます（IR.92 2.2.4）。以前の版の TS 24.229 にあった inactive は求められていません（6.1.2 NOTE 1）。
- RFC 3312 13.2 に segmented status type の例があります（`a=curr:qos local sendrecv`、`a=des:qos mandatory local sendrecv` など）。

### 流れ

想定する流れです。A が発信側、B が着信側で、どちらも EPC スタブを通ります。

1. A は INVITE を送ります。Supported は 100rel と precondition で、SDP は `curr:qos local none`、`curr:qos remote none`、`des:qos mandatory local sendrecv`、`des:qos optional remote sendrecv` です。
2. B は 183 を返します。Require は 100rel と precondition で、SDP answer は 1 つのコーデックと、`curr:qos local none`、`curr:qos remote none`、`des:qos mandatory local sendrecv`、`des:qos mandatory remote sendrecv`、`conf:qos remote sendrecv` です。B の P-CSCF はこの answer を見て AAR を送り、PCRF スタブが B に dedicated bearer を作ります。
3. A の P-CSCF も 183 の answer を見て AAR を送り、PCRF スタブが A に dedicated bearer を作ります。
4. A が PRACK を送り、B が 200 を返します。
5. A は dedicated bearer を受けると、UPDATE で local を sendrecv にし、B の確認の求めに答えます。B は 200 を返します。B に bearer があれば、この 200 の SDP で B の local も sendrecv にします。
6. B のプリコンディションがすべて満たされたら、B は 180 を返して利用者を呼び出します。応答すると INVITE の 200 を返し、A が ACK を送ります。

資源の確保が済んだことは、次のように決めます。UE は自分宛ての ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST を受け、その TFT が呼のメディアを含むとき、local の current status を sendrecv にします（IR.92 2.4.3.1、TS 24.229 L.2.2.5.1B）。EPC スタブを使わない構成（ソフトフォン、パッケージの試験）では、UE は最初から資源があるとみなし、offer で local を満たされたものとして示します（TS 24.229 6.1.2）。

### 今のコードとの差

- `sip.UA` は UPDATE（RFC 3311）を送れず、受けられません（`allowed` に無い）。PRACK と ACK に本文を載せず、offer を INVITE に入れる前提です（`internal/sip/ua.go` の SIMPLIFIED）。
- `sip.UA` の Supported は 100rel だけです。precondition、timer、199 がありません。
- UE の `Answer` は 183 を送るとすぐに呼び出しの状態になり、資源を待ちません。180 は、1 分ごとに送り直す暫定応答としてだけ送ります。
- `internal/sdp` は curr、des、conf の属性の意味を扱いません。a= の行としては残ります。
- UE は SDP の無い INVITE を 488 で断ります（`internal/ue/call.go` の「INVITE without usable SDP」）。

### 設計の候補

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| 状態の表 | RFC 3312 5 章の状態の表と、SDP の属性との変換を、純粋なパッケージ `internal/precondition` にする | `internal/ue` の中に書く |
| UPDATE | `sip.UA` に UPDATE の UAC と UAS を足す。早期のダイアログと確立後のダイアログの両方で使えるようにする。セッションタイマーの更新にも使う | re-INVITE だけを使う。早期のダイアログでは使えない |
| 資源の確保 | 上のとおり dedicated bearer の通知で決める | 一定の時間のあとで確保したとみなす |
| 使うかどうか | UE の設定で切り替えられるようにし、既定は使う。TS 24.229 5.1.5A の precondition disabling policy の代わり（5.1.5A は読んでいない） | 常に使う |
| 発信側が使わない場合 | IR.92 2.4.1 のとおり、着信側は SDP 付きの信頼性のある 183 を返し、bearer を受けてから 180 を返す | なし |

## 4. IMS-AKA

### 仕様が求めること

- 全体の流れ（TS 33.203 6.1.1）。
  - UE が REGISTER を送り、S-CSCF は HSS から AV（RAND、XRES、CK、IK、AUTN）を受けます。AV は 1 回だけ使います。
  - S-CSCF は 401 で RAND と AUTN を UE に、IK と CK を P-CSCF に渡します。P-CSCF は鍵を外して UE に送ります。
  - UE は AUTN の MAC と SQN の範囲を確かめ、RES を計算して次の REGISTER で返します。S-CSCF は XRES と比べます。
  - 登録済みの利用者は認証に失敗しても登録を解除しません。
  - S-CSCF は AKAv2-SHA-256 と AKAv1-MD5 の両方を、AKAv2-SHA-256 を先にして示します。UE は AKAv2-SHA-256 に対応する必要があります。
- UE の最初の REGISTER（TS 24.229 5.1.1.2.2）。Authorization には username（IMPI）、realm、uri と、空の nonce と response を入れます。Security-Client には ipsec-3gpp、アルゴリズム、SA の値（SPI、ポート）を入れます。SA で保護した REGISTER では Contact と Via に保護したサーバーポートを書きます。
- P-CSCF は、Security-Client があり、Require と Proxy-Require に sec-agree があれば AKA の手順を選びます。Security-Client が無ければ、Authorization から SIP Digest などを見分けます（5.2.2.1）。
- P-CSCF（5.2.2.2）。
  - 保護されていない REGISTER では、Security-Client を外して保存し、Authorization に integrity-protected="no" を入れます。Security-Client が無ければ 4xx を返します。
  - 401 からは ck と ik のパラメータを外して保存し、Security-Server を足し、一時的な SA を作ります。一時的な SA の寿命は reg-await-auth です。ck と ik を外せたときだけ、401 を UE に送ります。
  - 保護された REGISTER では Security-Verify と送った Security-Server、Security-Client と最初の Security-Client を照らし合わせ、integrity-protected="yes" を入れます。IMPI が最初と違えば 403 です。
  - 200 で、一時的な SA を新しい SA にします。寿命は前の SA の寿命と、登録の期限に 30 秒を足した値の長いほうです。
- S-CSCF（5.4.1.1、5.4.1.2.1、5.4.1.2.1A、5.4.1.2.2、5.4.1.2.3A）。
  - integrity-protected="no" なら AKA の最初の登録、"yes" なら保護された登録です（5.4.1.1 item 1、2）。
  - 401 の WWW-Authenticate には、realm、nonce（RAND と AUTN と任意のサーバーのデータ）、algorithm、ik、ck を入れます。RAND は再同期のために保存します（5.4.1.2.1A）。
  - 応答を確かめるときは、Call-ID が 401 と同じかを見てから、XRES を使って RFC 3310（AKAv1）か RFC 4169（AKAv2）で期待する値を計算します（5.4.1.2.2）。
  - auts があれば、保存した RAND と auts で HSS から新しい AV を受け、新しい 401 を送るか 403 で終えます。応答が違えば 403 です（5.4.1.2.3A）。
- UE の異常系（5.1.1.5.3）。MAC が違えば auts も response も無い REGISTER を、SQN が範囲外なら auts を付けた REGISTER を送ります。どちらでも、新しい Security-Client を入れ、一時的な SA は作りません。
- RFC 3310。nonce は RAND と AUTN（と任意のデータ）をつないだものの base64 です（3.2）。auts は AUTS の base64 で、auts を送るときは空の password で計算します（3.4）。応答の password は RES です。RFC 4169 の AKAv2 は PRF(RES||IK||CK, "http-digest-akav2-password") の base64 を password にします。
- SQN（TS 33.102）。
  - AuC は利用者ごとに SQN_HE = SEQ_HE || IND_HE を持ち、AV を作るたびに SEQ_HE を 1 増やします（C.1.1.2）。
  - USIM は IND ごとの配列を持ち、受けた SEQ が配列の値より大きければ新しいとみなします。そうでなければ同期の失敗を返します（C.2.2）。
  - Profile 2（C.3.2）は、IND が 5 ビット、配列の長さが 32、Δ が 2^28 です。
  - AUTS は SQN_MS を f5* で隠した値と、MAC-S（f1*、AMF はすべて 0）をつないだものです（6.3.3）。HSS は AUTS を確かめて SQN_HE を SQN_MS に合わせ、新しい AV を返します（6.3.5）。
- Milenage（TS 35.206 4 章）は、f1、f1*、f2 から f5、f5* を、AES（Rijndael）と OPc から作ります。TS 35.208 4.3 には 20 組の試験データがあり、各組に K、RAND、SQN、AMF、OP、OPc と、関数の出力があります（4.2）。
- Cx。
  - SIP-Authentication-Scheme は、AKAv1 と AKAv2 のどちらでも Digest-AKAv1-MD5 です（TS 29.229 6.3.9）。
  - MAA の SIP-Auth-Data-Item は、SIP-Authenticate（RAND と AUTN の 32 オクテット、6.3.10）、SIP-Authorization（XRES、6.3.11）、Confidentiality-Key（AVP コード 625、6.3.27）、Integrity-Key（626、6.3.28）を持ちます（TS 29.228 Table 6.3.5）。
  - MAR の SIP-Authorization は再同期のときだけ入れ、RAND と AUTS の 30 オクテットです（TS 29.228 Table 6.3.2、TS 29.229 6.3.11）。
  - HSS は、再同期の要求の S-CSCF 名が保存した名前と同じときに、AUTS を処理して AV を返します（TS 29.228 6.3.1 step 4）。
- sec-agree の値（TS 33.203 Annex H）。mechanism は ipsec-3gpp です。alg は hmac-sha-1-96、hmac-sha2-256-128、aes-gmac、aes-gmac-us、null、ealg は aes-cbc、aes-gcm、aes-gcm-us、null で、ほかに prot、mod、spi-c、spi-s、port-c、port-s があります。alg の null は aes-gcm と組むときだけ使えます。hmac-sha-1-96 と aes-cbc は推奨されません。鍵の伸ばし方は Annex I で、HMAC-SHA-1-96 では IK の後ろに 32 ビットの 0 を足します。
- SA（TS 33.203 7.1）。
  - UE と P-CSCF のあいだに 2 組の一方向の SA を作り、UDP と TCP で共有します。1 組は UE のクライアントポートから P-CSCF のサーバーポートへ、もう 1 組は P-CSCF のクライアントポートから UE のサーバーポートへの向きです。
  - 保護ポートは 5060 と 5061 以外です。保護されていないポートで P-CSCF が受けるのは、REGISTER、緊急の呼に関わるもの、エラーの応答だけです。
  - P-CSCF は SA の表（UE の IP、ポート、SPI、IMPI、IMPU、寿命）を持ちます。
- IR.92 2.2.2 は IMS-AKA、Sec-Agree、IPsec を求めます。完全性の保護は UE と網の両方で必須、暗号化は網では任意です。IR.92 2.2.1 は 401 で ealg=null を受けた UE に、暗号化なしで次の REGISTER を送ることを求めます。
- IPsec を使わない AKA は、標準では TLS の上の AKAv2-SHA-256 だけで、WebRTC 向けです（TS 24.229 4.2B.1、5.4.1.1 item 2A、5.4.1.2.1A d）。既定では暗号化しない構成は、標準には無い形になります。

### 今のコードとの差

- HSS は SIP Digest の H(A1) しか出さず、ほかの認証の方式を DIAMETER_ERROR_AUTH_SCHEME_NOT_SUPPORTED で断ります。加入者に K、OPc、AMF、SQN がありません。
- `internal/diameter/cx.xml` に Confidentiality-Key と Integrity-Key がありません（SIP-Authenticate、SIP-Authorization、SIP-Item-Number はある）。`cx.MultimediaAuthAnswer` は SIP Digest の項目だけで、`cx.MultimediaAuthRequest` に SIP-Authorization がありません。
- S-CSCF の `register` は SIP Digest without TLS だけです（ip-assoc-yes と ip-assoc-pending）。`Challenge` に RAND、XRES、CK、IK がありません。
- `sip.DigestResponse` は MD5 だけです。AKAv2-SHA-256 には SHA-256 の Digest が要ります。
- P-CSCF は Security-Client、Security-Server、Security-Verify を扱わず、保護ポートを開きません。`sip.Stack` は 1 つのアドレスで待ち受けます。
- UE に USIM の働き（Milenage、SQN の確認、AUTS）がありません。
- `images/peer` に SIPp はありますが、AKA のシナリオがありません。

SIPp v3.7.7 のソースで、次のことを確かめました。

- AKAv1-MD5 に対応し、AKAv2 には対応していません。認証の関数が受けるのは MD5、AKAv1-MD5、SHA-256 の 3 つです。
- `aka_OP` は OP で、SIPp が Milenage の中で OPc を計算します。HSS に OPc を入れるなら、SIPp には同じ試験データの組の OP を渡します。
- MAC は確かめますが、SQN の範囲は確かめません。AUTS を送る分岐は、条件が常に真のため通りません。
- シナリオの `[last_<ヘッダ名>:]` は任意のヘッダを写す仕組みなので、Security-Verify に `[last_Security-Server:]` を使える見込みです。試してはいません。

### 設計の候補

| 決めること | 調べた時点の案 | ほかの案 |
| --- | --- | --- |
| Milenage | Go の標準ライブラリ（crypto/aes）だけで書くパッケージ `internal/milenage`。TS 35.208 の 20 組で試験する | 既存のライブラリを使う |
| AV と再同期 | `internal/aka` に、AV と AUTN の作り方、RES と AUTS の作り方と確かめ方を置き、HSS と UE で共有する。独立した試験相手には SIPp を使う | HSS と UE に別々に書く |
| 加入者のデータ | HSS の加入者に `aka`（K、OPc、AMF、SQN）を足す。password と aka のどちらか、または両方を持てる。SQN は `Store` に置き、加入者ごとのロックの中で増やす（`Store` に不可分の操作が無いため） | OP を持たせる |
| Digest と AKA の選び方 | 加入者が持つ資格情報と、UE の要求で決まる。UE が Security-Client と sec-agree を送れば AKA、送らなければ Digest（TS 24.229 5.2.2.1）。HSS は持っていない認証の方式を DIAMETER_ERROR_AUTH_SCHEME_NOT_SUPPORTED で断る | 設定で網全体を切り替える |
| AKA の版 | AKAv2-SHA-256 と AKAv1-MD5 の両方の WWW-Authenticate を、AKAv2 を先にして出す（TS 33.203 6.1.1）。UE シミュレータは AKAv2 を選び、SIPp は AKAv1-MD5 を使う。`sip` に SHA-256 の Digest を足す | AKAv1-MD5 だけにして SIMPLIFIED に書く |
| 既定の平文 | sec-agree の手順と保護ポートはそのまま行い、ESP をかけずに平文で送る。P-CSCF は SA の表（UE の IP とポートの組）で、保護されたものとみなして integrity-protected="yes" を付ける。標準に無い動きなので SIMPLIFIED に書く。tshark 4.2.2 は SIP の UDP と TCP のヒューリスティックが既定で有効なので、5060 以外のポートでも SIP として読める | sec-agree も省き、5060 で AKA だけを行う。Security- のヘッダが見えなくなる |
| IPsec を有効にする | 設定（P-CSCF と UE の両方）で有効にすると、Linux の xfrm に ESP の SA と SP を入れる。コンテナに NET_ADMIN が要る。Wireshark で読むには、ESP の NULL 暗号を推測して復号する設定（tshark 4.2.2 では既定で無効）か、SA の鍵の登録が要る | 拡張の候補「TLS と IPsec を有効にした構成」に回し、既定の平文だけを作る |
| SIPp | `images/peer` の SIPp で、AKAv1-MD5 の登録のシナリオを e2e で流す。Security-Client は固定の値、Security-Verify は `[last_Security-Server:]` にする。2 回目の REGISTER を Security-Server の port-s へ送れるかは試作で確かめる | Alpine の pjsua を AKA で使う（対応しているかは確かめていない） |
| 状態（各役割を複数台で動かせるようにするため） | S-CSCF の `Challenge` に RAND、XRES、CK、IK を足す（`Store`）。P-CSCF の SA の表も `Store` に置く。IPsec を有効にすると SA はカーネルにあるので、UE はその P-CSCF の台に結びつく。台が落ちたときは再登録で戻る（冗長構成、TS 23.380） | なし |

SIPp のシナリオは次の形を想定しています。

1. REGISTER を送る。Authorization は空の nonce と response、Security-Client は固定の値、Require と Proxy-Require に sec-agree。
2. 401 を受ける（`auth="true"`）。
3. `[authentication username=... aka_K=... aka_OP=... aka_AMF=...]` で Authorization を作り、同じ Security-Client と、`[last_Security-Server:]` の Security-Verify を付けた REGISTER を、保護ポートへ送る。
4. 200 を受ける。

## 5. IR.92 の必須の要件

### profiles.md の表の VoLTE の行

[profiles.md](profiles.md) の表の VoLTE に関わる行を、作り方に落とします。

| 要件 | IR.92 の節 | 今のコード | 作り方 |
| --- | --- | --- | --- |
| RTCP。複合パケットに報告と SDES（CNAME）を入れる。対称にする。早期メディアを含め、メディアを送受信するあいだは送る。b=RS と b=RR を SDP に入れ、受けた側に 5 秒に 1 回以上届くようにする。両方が 0 なら送らない | 3.2.4 | 送らない。中継ノードは RTCP を通さない（`internal/agw/relay.go` の SIMPLIFIED） | `internal/rtcp`（SR、RR、SDES と、RFC 3550 6 章の送る間隔）。UE が送受信し、中継ノードは奇数のポートで RTCP を中継する。`internal/sdp` が m= ごとの b= の行を持つ。b=RS と b=RR の値は、5 秒の条件を満たすように試作で計算して決める |
| b=AS で帯域を交渉する | 3.2.2.4、2.4.3.2 | 送らない。`internal/sdp` が m= の後ろの b= の行を捨てる | UE は offer と answer で、含めたコーデックの最も高いモードに合う値を書く。TS 26.114 6.2.5.2 の Table 6.7 と 6.8 で、20 ms、bandwidth-efficient、IPv4 なら AMR の 12.2 kbit/s が 29、AMR-WB の 23.85 kbit/s が 41。P-CSCF はこれを Max-Requested-Bandwidth に写す |
| DTMF の telephone-event（TS 26.114 Annex G）。telephone-event の無い offer を拒否しない | 3.3 | 無い | offer には、音声のコーデックと同じクロックの telephone-event（16000 と 8000）を入れる。answer では、選んだコーデックのうち最も高いクロックのものを入れる（TS 26.114 G.3）。送り方は RFC 4733 2.5。UE の操作用 API に、数字を送る操作を足す |
| SDP の無い INVITE を受け、最初の信頼性のある応答で offer を出す | 2.2.4 | 488 で断る | `sip.UA` で、信頼性のある 183 に offer を、PRACK に answer を載せられるようにする |
| CANCEL と BYE に Reason を付ける。protocol は RELEASE_CAUSE | 2.2.4 | 付けない | UE は `Reason: RELEASE_CAUSE;cause=1;text="User ends call"` を付ける（cause の値は TS 24.229 7.2A.18.11.2 の Table 7.2A.18.11-2）。P-CSCF が bearer を失って出す BYE は TS 24.229 5.2.8.1.2 のとおり |
| セッションタイマー（RFC 4028）。Session-Expires は 1800 秒か付けない | 2.2.8 | 無い | UE は Supported に timer を入れ、Session-Expires を 1800 にするか付けない。422 を受けたら RFC 4028 7.4 のとおりにする。着信側の 2xx では refresher を uac にする。更新は UPDATE で送る |
| P-Early-Media と 199 | 2.2.7、2.2.5 | 無い | INVITE に `P-Early-Media: supported` と、Supported の 199 を入れる。ダイアログごとに早期メディアの許可の状態を持ち、自分で呼び出し音を鳴らすかを決めて、UE の状態に出す。199 を受けたら、その早期ダイアログの資源を解放する。S-CSCF は分岐しないので（`internal/scscf/callerprefs.go` の SIMPLIFIED）、網は 199 を作らない |
| プリコンディション。状態の更新は UPDATE | 2.4.1 | 無い | 3 章 |
| 登録の Contact に mmtel の ICSI と +sip.instance を入れる | 2.2.1 | 入れていない（+g.3gpp.smsip だけ） | Contact に `+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`、`audio`、`+sip.instance`（IMEI の URN）を入れる。INVITE では Contact、Accept-Contact、P-Preferred-Service に ICSI を入れる（TS 24.229 5.1.2A.1.1、TS 24.173 5.2）。S-CSCF は P-Preferred-Service を確かめて P-Asserted-Service に替える（TS 24.229 5.4.3.2 step 4C） |
| IMS-AKA、Sec-Agree、IPsec、ISIM | 2.2.2 | SIP Digest だけ | 4 章 |
| AMR と AMR-WB、RFC 4867 のペイロード形式 | 3.2.1、3.2.5、2.4.3.2 | G.711 だけ | 下の「AMR と AMR-WB」 |

### profiles.md の表に無かった必須の要件

| 要件 | 根拠 | 今のコード | 作り方 |
| --- | --- | --- | --- |
| REGISTER と INVITE とその応答の Contact に、audio の機能タグ（RFC 3840）を入れる | IR.92 2.2.1、2.2.4 | 入れていない | `internal/ue` |
| Contact の user part を一意にし、私的な情報を出さない。時刻をもとにした UUID を使える | IR.92 2.2.1 | 秘密の識別子から HMAC-SHA-256 で導くバージョン 8 の UUID（RFC 9562）を user part にしている（`NewContact`）。鍵は USIM の K かパスワードで、起動し直しても同じ値になる | なし |
| すべての要求に User-Agent、すべての応答に Server を付ける。形は GSMA RCC.07 に IR.92 の拡張（`PRD-IR92/<主な版>` など）を加えたもの | IR.92 2.6 | 付けていない | `internal/ue`。RCC.07 は読んでいない |
| OPTIONS の要求と 200 の Contact に mmtel の ICSI を入れる | IR.92 2.2.9 | `sip.UA` の受け付ける方法に OPTIONS が無い | `sip.UA` が OPTIONS に答えるようにする |
| 発信の 486 での拒否（UDUB） | IR.92 2.2.4 | 対応している | なし |
| SIP の信号は IMS の APN の QCI 5 の default bearer、音声は QCI 1 の dedicated bearer。P-CSCF は PCO で見つける | IR.92 4.4.1、4.4.2、4.5 | 無い | 1 章 |
| PDN 接続や音声の GBR bearer を失ったら、網がセッションを終える | IR.92 2.4.2.1、2.4.2.3 | 無い | 2 章 |
| 着信側の資源が無いときは、資源を確保するまで 180 を返さない | IR.92 2.4.1 | 183 を送るとすぐに呼び出しの状態になる | 3 章 |
| ptime は 20、maxptime は 240。1 つの RTP に 12 フレームまで受ける。重なったフレームを捨てる。CMR に従う | IR.92 3.2.5、3.2.1 | 無い | 下の「AMR と AMR-WB」 |
| P-Access-Network-Info を付ける | TS 24.229 L.3.1.1 | 付けていない | 1 章 |

IPv4 と IPv6 の両方（IR.92 5.1）には対応せず、IPv4 だけで作ります（[profiles.md](profiles.md)）。

### AMR と AMR-WB

IR.92 が UE に求めることは次のとおりです。

- AMR の 8 つのモードと AMR-WB の 9 つのモードに対応し、どの部分集合でも動けること（3.2.1）。受けた RTP の CMR に従えること（3.2.1。TS 26.114 7.5.2.1.2.2 を引いている）。
- 最初の offer に、mode-set の無い AMR-WB と AMR のペイロードタイプを 1 つずつ以上入れること（2.4.3.2）。
- bandwidth-efficient と octet-aligned の両方に対応し、発信のときは bandwidth-efficient を求めること（3.2.5）。

中継ノード（IMS-AGW の役割）は RTP を中継するだけで、user plane を終端しません。IR.92 が「user plane を終端する網の実体」に求めることは当たらないので、中継ノードは符号化も復号もしません。

実装の方法の候補を比べました。AMR と AMR-WB の特許の扱いと、EVS の実装の方法は、未決の論点として残っています。

| 案 | 中身 | よい点 | 困る点 |
| --- | --- | --- | --- |
| A. 符号化済みのフレーム（採用） | RFC 4867 5 章の格納形式で、AMR と AMR-WB のファイルを試験用に用意する。UE は Go だけで、フレームを RTP のペイロード（bandwidth-efficient と octet-aligned、CMR、ToC）に詰める。受けた側はフレームを格納形式で書き出す | 製品のイメージは CGO_ENABLED=0 のまま。送ったフレームと受けたフレームをビットで比べられ、G.711 の試験より厳しく確かめられる | 任意の WAV は送れない。CMR に応じたモードの切り替えは、モードごとのファイルを用意するか、SIMPLIFIED に書く |
| B. cgo で opencore-amr と vo-amrwbenc を使う | UE が WAV を符号化し、受けた音声を復号する | 任意の音声を送れ、CMR にも従える | ビルドを cgo にする必要がある。ホストの Go だけでは試験しにくくなる |
| C. Go に移植する | | | 大きすぎる |

案 A のファイルは Alpine 3.24 の community にある opencore-amr 0.1.6-r1 と vo-amrwbenc 0.1.3-r3（どちらも Apache-2.0、`-dev` のパッケージもある）を使う小さな道具で、今の試験音（`ue.Tone`）から作ります。道具は cgo を使うので、ビルドタグで製品から外します。作ったファイルはコミットし、作ったコマンドをコミットの本文に書きます。Alpine 3.24 の ffmpeg-libavcodec 8.1.3 は、この 2 つのライブラリに依存していません。ffmpeg では符号化できない見込みで、受けたファイルを WAV に戻すのには ffmpeg 自身の復号器が使える見込みです。

SDP の offer は AMR-WB（bandwidth-efficient、octet-aligned）、AMR（同じ 2 つ）、PCMU、telephone-event の 16000 と 8000 の順にします。mode-set は付けず、ptime は 20、maxptime は 240 です。PCMU を残す理由は、国内の網間接続（JJ-90.30）がこれを求めることと、PCMU だけのソフトフォンと話すためです。PCMU を offer に残すと、b=AS は最も高い PCMU に合わせることになり、PCRF が求める帯域も大きくなります（8 章）。

Wireshark で確かめたことが 1 つあります。tshark 4.2.2 の AMR の既定は「RFC 3267 octet aligned」と「Narrowband AMR」です。SDP の fmtp を見て AMR-WB や bandwidth-efficient に切り替えるかは確かめていません。切り替えなければ、既定の設定で bandwidth-efficient の AMR-WB を読み違えます。標準のポート番号のままで読めるかを、e2e で確かめます。

### NG.114 との違い

NG.114 v11.0 でも、SIP の信号には 5QI 5 の QoS flow を使い（4.5.1.1）、音声には 5QI 1 の GBR の QoS flow を使います（4.5.2）。P-CSCF は TS 24.229 Annex U.2.2.1 の方法 II で見つけます（4.7）。考え方は EPS と同じなので、5GC では、`AccessNetwork`（UE の側）と `PolicyControl`（P-CSCF の側）の境界に 5GC の実装を足せば足りる見込みです。EVS が必須になること（[profiles.md](profiles.md)）は、5GC の側で扱います。

## 6. 国内標準（TTC）

[profiles.md](profiles.md) のとおり、TTC の標準は事業者網どうしの接続（NNI）を決めたもので、端末と網のあいだ（UNI）は決めていません。この調査は UNI とアクセス網の手順を扱うので、TTC の規定を直接は当てません。

この作りが、網間接続で TTC と食い違う点は次のとおりです。いずれも接続先ごとのプロファイルで扱います（[interconnect.md](interconnect.md)）。

- セッションタイマー。JJ-90.30 は網間で 180 秒から 300 秒を求め、IR.92 の UE の 1800 秒とは別の規定です。
- プリコンディション。JJ-90.30 では使うかを事業者間で決めます。100rel は必須です。
- コーデック。JJ-90.30 は網間の offer に G.711 μ-law を必ず入れ、移動網どうしでは AMR を必ず入れることを求めます。UE の offer に PCMU を残すのは、これと合います。

ttc.or.jp は調べたときに 503 を返したので、2026 年の改版の有無は確かめられませんでした。

## 7. 確かめること（e2e）と簡略化

`scripts/e2e-volte.sh` では、pcap から次のことを確かめます。

- 登録。REGISTER に Security-Client がある。MAR の SIP-Authentication-Scheme が Digest-AKAv1-MD5 で、MAA に Confidentiality-Key と Integrity-Key がある。S-CSCF から P-CSCF への 401 には ck と ik があり、P-CSCF から UE への 401 には無く、Security-Server がある。2 回目の REGISTER が保護ポートに届き、Security-Verify がある。最後に 200。
- 登録のあとの Rx。Specific-Action を持つ AAR（コマンドコード 265）と AAA。
- SIPp の AKAv1-MD5 の登録が 200 で終わる。
- 呼。INVITE の Supported に precondition がある。183 の Require に precondition があり、SDP に curr、des、conf がある。PRACK、UPDATE とその 200、180、200、ACK の順。2 つの P-CSCF がそれぞれ AAR を送る。
- メディア。AMR か AMR-WB の RTP、両方の UE からの RTCP、telephone-event の RTP。受けた AMR のフレームが送ったものとビットで一致する。
- 切断。BYE に Reason がある。2 つの STR（275）と STA。
- 異常系。EPC スタブの操作で dedicated bearer を落とすと、RAR か ASR のあとで P-CSCF が BYE を送る。

手順書「VoLTE の呼の流れ」は、上の流れのラダー図、仕様の参照箇所、簡略化した所の一覧を載せます。

簡略化する所は次のとおりです。

- EPC スタブ。NAS、S1、GTP-U、SGi を省き、UE とは HTTP と JSON で話す。
- IP アドレス。加入者ごとの静的な値を、Compose の設定と同じにする。
- bearer。帳簿だけで、QoS の強制とパケットの振り分けをしない。
- Gx。PCRF と PGW のスタブのあいだを Go の呼び出しにする。
- PCRF の選び方。DRA を使わず、設定した 1 つの PCRF に送る。
- 平文の AKA。sec-agree と保護ポートを使い、ESP をかけない。
- AMR。符号化済みのフレームを送り、CMR に応じた切り替えを省く。
- 網の 199。S-CSCF が分岐しないので作らない。

## 8. 決めた設計と、調査時の案との違い

決めた設計は [docs/design/volte-access.md](../design/volte-access.md) にあります。調べた時点で論点になったことと、結論は次のとおりです。

1. UE と EPC スタブの境界の形。`AccessNetwork` のインタフェースと、ESM の名前を付けた HTTP と JSON の API にします。NAS の実装に替えるときの差し替え口になります。
2. UE の IP アドレスの模擬。静的なアドレスと 1 つのブリッジにします。TUN は使いません。
3. IPsec を有効にする設定。ESP をかける設定は主な作業に入れません。時間を区切って試し、動かせなければ拡張の候補に残します。既定は無効です。
4. 平文の AKA の形。sec-agree と保護ポートを残し、ESP だけを省きます。
5. AKA の版。調べた時点の案は AKAv2-SHA-256 と AKAv1-MD5 の両方を出すことでしたが、AKAv1-MD5 だけにします。独立した試験相手の SIPp が AKAv2 に対応していないためです。AKAv2 を省いたことは SIMPLIFIED に書きます。
6. AMR の実装の方法。案 A（符号化済みのフレームと Go だけのペイロードの処理）にします。cgo を使う案とは違います。
7. UE の offer に PCMU を残すか。残します。b=AS と PCRF の求める帯域が PCMU に合わせて大きくなることは、受け入れます。
8. PCRF のスタブは `epc` の役割の中に置き、Gx はプロセスの中の呼び出しにします。
9. EPC を通らない UE（ホストのソフトフォン）では、PCRF が IP-CAN_SESSION_NOT_AVAILABLE を返し、P-CSCF はその UE の呼で Rx を使わずに進めます。
10. P-CSCF にダイアログの情報を持たせます（`Store` の後ろ）。TS 24.229 5.2.8.1.2 の BYE を出すために要ります。

## 確かめられなかったこと

- TS 29.212 は 4.5.1 と 4.5.2.0 だけを読みました。CCR、CCA、RAR の AVP と、PCC rule の中身（Charging-Rule-Install、QoS-Information）は読んでいません。
- TS 24.301 はメッセージと IE の条項を目次で確かめただけです。各メッセージの IE の一覧と、ESM の手順は読んでいません。TS 23.401 の 5.10.2 と 5.4.4.1 も目次だけです。
- RFC 3262、3311、3326、3550、4028、4733、4867、5009、6228 の細部は読まず、IR.92 と TS 24.229 の要約に頼りました。
- TS 24.229 の 5.1.5A（precondition disabling policy）、5.2.8.3（セッションの期限切れ）、P-CSCF の P-Early-Media の扱い、5.2.6 のうち PCC に関わる部分は読んでいません。
- TS 33.203 Annex M（NAT 越しの UDP-enc-tun）は読んでいません。ホストのソフトフォンは SIP Digest のままにする前提です。
- GSMA RCC.07（User-Agent の形）と IR.92 Annex C.3 の既定値（RateSet など）は読んでいません。
- SIPp が 2 つの WWW-Authenticate のどちらを使うか、2 回目の REGISTER を保護ポートへ送れるか、`[last_Security-Server:]` で写せるかは試していません。
- Wireshark が SDP の fmtp を見て AMR の形式と AMR-WB を切り替えるか、RTP Player で AMR を再生できるかは確かめていません。
- Windows の Docker Desktop のコンテナで、NET_ADMIN と xfrm が使えるかは確かめていません。
- b=RS と b=RR の具体的な値。RFC 3550 6.3 の間隔の計算で試作して決めます。
- AMR と AMR-WB の特許の扱い。
- Alpine の pjsua が AKA や AMR に対応しているか。
- TTC の 2026 年の改版の有無（ttc.or.jp が 503）。
