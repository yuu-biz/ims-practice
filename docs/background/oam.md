# 運用と保守（OAM）の調査

この文書は、IMS の網を運用し保守するための仕組み（保守用の API、OAM のサーバー、指標、警報、バックアップ、試験用の加入者の扱い、呼の解析）について、標準が定めることと、この実装の作りを調べた記録です。仕様の本文は写さず、要点を言い換えて書きます。条項を引くときは「読んだ資料」の表にある版のものです。条項番号は、ダウンロードした仕様書の本文で確かめたものだけを書き、確かめられなかった所は [unverified-clauses.md](../unverified-clauses.md) に記録します。

各章は、標準が決めていること、最初に考えた案、作ったもの、の順に並びます。保守用の API は `internal/maintenance`、OAM のサーバーは `internal/oam`、OAM の画面は `web/oam`、指標は `internal/metrics`、Prometheus と Grafana の構成は `deploy/observability/` にあります。5 章の「役割ごとの指標の案」と、3 章から 6 章の「作った」節は、案を作ったあとにその実装に合わせて足したものです。

## 読んだ資料

| 仕様 | 版 | 表題（表紙で確かめたもの） | 読んだ所 |
| --- | --- | --- | --- |
| TS 32.101 | V19.0.0 | Telecommunication management; Principles and high level requirements | 7.1 |
| TS 32.102 | V19.0.0 | Telecommunication management; Architecture | 7.3.2 の Itf-N の説明 |
| TS 32.600 | V19.0.0 | Configuration Management (CM); Concept and high-level requirements | 4.2.1、7.1。`internal/oam` を作るときに、4.2.1.1、4.2.1.3、5.1、7.1 を読み直しました |
| TS 32.401 | V19.0.0 | Performance Management (PM); Concept and requirements | 4.2.2 |
| TS 32.409 | V19.0.0 | Performance Management (PM); Performance measurements; IP Multimedia Subsystem (IMS) | 3.1、4.1.1.1、4.1.2.4、4.1.2.8。ほかは見出し。5 章の「役割ごとの指標の案」を足すときに、4.1.1 から 4.1.3、4.1.6 から 4.1.10、4.2.1、4.2.12、4.7.1 も読みました |
| TS 32.454 | V19.0.0 | Key Performance Indicators (KPI) for the IP Multimedia Subsystem (IMS); Definitions | 5 章の見出し、5.1.1、5.2.1 |
| TS 32.421 | V19.4.0 | Subscriber and equipment trace; Trace concepts and requirements | 4.1、4.3 |
| TS 32.422 | V20.3.0 | Subscriber and equipment trace; Trace control and configuration management | 4.1.2.9 の先頭 |
| TS 32.423 | V20.1.0 | Subscriber and equipment trace; Trace data definition and management | 4 章の見出しと先頭 |
| TS 32.111-1 | V19.0.0 | Fault Management; Part 1: 3G fault management requirements | 表題だけ |
| TS 28.533 | V20.1.0 | Management and orchestration; Architecture framework | 4.1、4.2 |
| TS 28.532 | V20.2.0 | Management and orchestration; Generic management services | 4 章、11.1.0、11.1.1 の見出し、11.2a |
| TS 28.622 | V20.4.0 | Generic Network Resource Model (NRM) Integration Reference Point (IRP); Information Service (IS) | 4.3 の見出し、5.3.1、5.3.3 の見出し。`internal/maintenance` を作るときに、4.3.4 の属性の表、4.4.1 の `administrativeState` と `operationalState` の説明、5.3.1 から 5.3.3 の表も読みました |
| ITU-T X.731 | 01/92 | Information technology – Open Systems Interconnection – Systems Management: State management function | 7.1.3、7.1.3.1 から 7.1.3.4、8.1.1.3。`internal/maintenance` を作るときに読みました |
| TS 28.705 | V19.0.0 | IP Multimedia Subsystem (IMS) Network Resource Model (NRM) Integration Reference Point (IRP); Information Service (IS) | 4.3 の見出し、4.3.4、4.3.12 |
| TS 28.111 | V20.0.0 | Management and orchestration; Fault Management (FM) | 6.3、6.4、6.5、6.12、6.13、6.14、7.3.1 の先頭、8 章の見出し。警報を作るときに、6.1 から 6.14、7.3.1（7.3.1.1 から 7.3.1.3）、7.3.2（7.3.2.1、7.3.2.2）、7.4.1 の属性の説明、Annex B の表 B.1 から B.4 を読みました。同じ書庫の OpenAPI の定義 `TS28111_FaultNrm.yaml`（中の版は 19.3.0）で、値の綴りを確かめました |
| ITU-T X.733 | 02/92 | Information technology – Open Systems Interconnection – Systems Management: Alarm reporting function | 8.1.1、8.1.2.1 の終わり、8.1.2.2、8.1.2.3、8.1.2.13。警報を作るときに読みました |
| TS 28.550 | V20.0.0 | Management and orchestration; Performance assurance | 4.3 の表 |
| TS 28.552、TS 28.554 | V20.4.0、V20.3.0 | 5G performance measurements、5G end to end Key Performance Indicators (KPI) | 表題だけ |
| TS 33.102 | V19.1.0 | 3G security; Security architecture | 3.2 の K の説明、6.3.1。`internal/oam` を作るときに読みました |
| RFC 3966 | 2004 年 12 月 | The tel URI for Telephone Numbers | 3 の `visual-separator`、5.1.1。`internal/oam` を作るときに読みました |

Prometheus については、公式の Go のクライアント（`github.com/prometheus/client_golang` v1.25.0）と、それが使う `github.com/prometheus/common` v0.72.0 のソースを読み、リポジトリの外で動かしました。prometheus.io と grafana.com には届きませんでした（接続できず）。

「役割ごとの指標の案」を書くときには、次のものも読みました。prometheus.io はプロキシに断られました（403）。同じ文書を、GitHub の公式のリポジトリから読んでいます。

- Prometheus の文書のリポジトリ（`prometheus/docs` の `main`）の `instrumenting/exposition_formats.md`、`instrumenting/writing_clientlibs.md`、`instrumenting/content_negotiation.md`、`concepts/data_model.md`、`practices/naming.md`。
- Prometheus のリポジトリのタグ `v3.15.1` の `LICENSE`、`Dockerfile`、`docs/configuration/configuration.md`。
- Grafana のリポジトリのタグ `v13.3.0` の `LICENSE`、`LICENSING.md`、`Dockerfile`、`conf/defaults.ini`、`docs/sources/administration/provisioning/index.md`、`docs/sources/setup-grafana/configure-grafana/_index.md`。
- Docker Hub のタグの情報（`prom/prometheus`、`grafana/grafana`）。版を選ぶために使いました。

同じ日のあとの見直しで、prometheus.io と grafana.com に届くようになりました。次のページを読み、5 章の決まりと指標の名前、`deploy/observability/` のイメージとライセンスの記述を照らし合わせています。題は各ページの表題です。

| サイト | ページ |
| --- | --- |
| prometheus.io | 「Metric and label naming」、「Data model」、「Metric types」、「Exposition formats」、「Histograms and summaries」、「Instrumentation」（best practices）、「Prometheus 3.0 migration guide」、「Download」 |
| grafana.com | 「Licensing」、「Run Grafana Docker image」、「Configure a Grafana Docker image」、「Grafana Enterprise」、「Grafana OSS」 |
| hub.docker.com | `prom/prometheus:v3.15.1`、`grafana/grafana:13.3.0`、`grafana/grafana-oss:13.3.0` のタグと、層の一覧 |

## 全体の姿

この仕組みが揃うと、学習者ができることは次のとおりです。

1. OAM の画面で、すべてのノードの状態を一覧する。ノードごとに、役割、管理上の状態、動作の状態、設定の世代、上がっている警報が見える。
2. OAM からノードをドレインし、通話が終わるのを見てから止める。ログの詳しさを上げて、1 つの呼を追う。
3. 加入者を 1 件ずつ、または 100 件まとめて登録する。
4. 登録と呼の成功率を見る。Prometheus と Grafana を足すと、時間の推移をグラフで見られる。
5. HSS を止めてデータを失わせ、退避しておいたデータを戻し、網が正しい状態に戻ったことを確かめる。
6. 新しい機能を、試験用の利用者だけに有効にする。ほかの利用者には効かないことを確かめる。
7. 設定を変え、変更の記録を見て、前の設定に戻す。
8. 失敗した呼を理由ごとに数え、その 1 つを選んでラダー図を開く。

## 1. 標準が決めていることと、決めていないこと

### 管理の枠組み

- 3GPP は、網の管理を機能ごとに分けています。性能、障害、構成、課金、セキュリティ、ソフトウェア、加入者、ローミング、不正利用などの管理です（TS 32.101 7.1）。この記録が扱うのは、障害の管理（FM）、性能の管理（PM）、構成の管理（CM）、トレースの 4 つです。
- 古い枠組み（IRP）では、網の管理の仕組み（NM）と、装置の管理の仕組み（EM）のあいだの Itf-N を標準にしています（TS 32.102 7.3.2）。EM と装置（NE）のあいだは、製品ごとの情報モデルを使います。これは開発した組織の中のもので、公開する必要はないと書かれています（TS 32.600 4.2.1.3）。標準になっているのは Itf-N で、EM と NE のあいだはプロトコルだけです。
- このプロジェクトは、保守用の API を標準ではなく独自のものとしています。これはこの区分に合っています。ノードの保守用の API は EM と NE のあいだに当たり、標準は中身を決めていません。手順書には、TS 32.600 4.2.1.3 を根拠として書けます。
- 新しい枠組み（SBMA）では、管理の働きを Management Service（MnS）として出します。MnS を出す側が producer、使う側が consumer です（TS 28.533 4.1）。MnS は 3 種類の部品でできています（4.2）。
  - 種類 A。操作と通知。管理する相手に依らない、作る、読む、変える、消すなどの操作です。
  - 種類 B。管理する相手の情報モデル（NRM）。
  - 種類 C。性能の情報と、障害の情報。
- 種類 A の共通の操作は TS 28.532 にあります。`createMOI`、`getMOIAttributes`、`modifyMOIAttributes`、`deleteMOI` と、変更の通知です（11.1.1）。障害の管理は TS 28.111 に移っています（11.2a）。
- 情報モデルの共通の部分は TS 28.622 にあります。`ManagedElement`（4.3.3）、`ManagedFunction`（4.3.4）、`ThresholdMonitor`（4.3.16）、`HeartbeatControl`（4.3.21）、`TraceJob`（4.3.30）、`PerfMetricJob`（4.3.31）などのクラスがあります。
- 管理上の状態の値は `LOCKED`、`UNLOCKED`、`SHUTTINGDOWN` です（TS 28.622 5.3.1）。動作の状態（5.3.3）の値は `ENABLED` と `DISABLED` です（TS 28.111 7.4.1 の属性の説明で確かめました）。`SHUTTINGDOWN` は、このプロジェクトのドレイン（新しい登録と呼を断り、残っている呼が終わるのを待ってから止める操作）に当たる言葉として使えます。
- TS 28.622 は値の名前だけを決めています。`administrativeState` は使ってよいかどうかを表し、管理する側（MnS consumer）が決めます。`operationalState` は動けるかどうかを表し、管理される側が決め、管理する側からは読むだけです（4.4.1）。`ManagedFunction` の属性の表（4.3.4）には、どちらの状態もありません。状態を持つのは、それを継承する個々のクラスです。
- 状態の移り方は ITU-T X.731 にあります（7.1.3）。出来事は 4 つです。unlock は LOCKED か SHUTTINGDOWN から UNLOCKED にします（7.1.3.1）。lock は UNLOCKED か SHUTTINGDOWN から LOCKED にします（7.1.3.2）。shut down は UNLOCKED からだけ起こせます。使っている利用者がいれば SHUTTINGDOWN に、いなければすぐ LOCKED にします（7.1.3.3）。user quit は利用者が使い終えたことで、SHUTTINGDOWN のときに最後の 1 人が抜けると LOCKED になります（7.1.3.4）。SHUTTINGDOWN は、今の利用者にだけ使わせる状態で、管理する側はいつでも UNLOCKED に戻せます（8.1.1.3）。
- IMS の情報モデルは TS 28.705 にあります。`PCSCFFunction`（4.3.12）、`ICSCFFunction`（4.3.6）、`HSSFunction`（4.3.5）、`MRFCFunction`（4.3.9）、`ASFunction`（4.3.1）などのクラスと、ノードのあいだのつながりを表す `Link_HSS_SCSCF`（4.3.30）などのクラスです。`PCSCFFunction` には、固有の属性がありません。標準のモデルは、ノードがあることと、つながりがあることを表すだけで、設定の中身は製品に任せています。

### 障害の管理（FM）

- 警報は、属性の組（alarm record）で表します（TS 28.111 6.3）。
  - 対象のオブジェクト。識別名（DN）で書きます。
  - 警報の種類。`COMMUNICATIONS_ALARM`、`PROCESSING_ERROR_ALARM`、`EQUIPMENT_ALARM`、`ENVIRONMENTAL_ALARM`、`QUALITY_OF_SERVICE_ALARM` です。
  - 推定される原因（probable cause）と、それを細かくした specific problem。
  - 重さ（perceived severity）。critical、major、minor、warning、indeterminate があり、cleared は警報の条件が無くなったことを表します（6.3、7.4.1）。X.733 は、サービスに響く重さとして critical から warning までの 4 つに順を付けています。indeterminate は重さを決められないことを表し、順はありません（X.733 8.1.2.3）。
- 対象、種類、原因、specific problem の 4 つが同じ警報は、同じ警報とみなします（6.4）。新しい障害を見つけたら、この 4 つが同じ記録が一覧にあるかを調べ、あれば更新し、無ければ足します（6.14）。
- 今の状態を表す警報の記録は、producer の警報の一覧（alarm list）に置きます。過去の記録は一覧に置きません（6.5）。
- 通知には `notifyNewAlarm`、`notifyClearedAlarm`、`notifyChangedAlarmGeneral`、`notifyAckStateChanged`、`notifyComments` などがあります（6.12、8 章）。
- 性能の値がしきい値を越えたときに警報を出す考え方もあります（TS 28.622 の `ThresholdMonitor`、TS 28.111 の `ThresholdCrossing`）。中身は読んでいません。

### 性能の管理（PM）

- 測定の種類は 4 つです（TS 32.401 4.2.2）。Cumulative Counter（CC）、Gauge、Discrete Event Registration（DER）、Status Inspection（SI）です。測定は granularity period という区切りごとに結果を出し、CC は区切りの始めに 0 に戻します。
- IMS の測定は TS 32.409 にあります。名前は、機能を表す族の名前で始まります（3.1）。UR が登録、SC がセッションの制御、MA が認証、LIQ が場所の問い合わせ、DBU がデータベースの使用です。
- 登録の測定の例（4.1.1.1）。
  - `UR.AttInitReg`。P-CSCF か S-CSCF が、初期登録の REGISTER を受けたときに数えます。
  - `UR.SuccInitReg`。初期登録に 200 を返したときに数えます。
  - `UR.FailInitReg.Cause`。失敗の応答を返したときに、応答コードごとに数えます。コードごとの値の合計は、失敗の総数に等しくなります。
  - `UR.MeanInitRegSetupTime`。P-CSCF が REGISTER を受けてから 200 を返すまでの時間の平均（ミリ秒）です。
  - `UR.AttInitReg.AT`。P-Access-Network-Info の access-type ごとの数です。
- セッションの測定の例。`SC.FailSession.Cause` は、失敗したセッションの確立を応答コードごとに数えます（4.1.2.4）。`SC.DroppedSession` は、期限切れで消したセッションの数です（4.1.2.8）。試行、成功、応答、発信と着信の別、確立の時間、同時のセッション数の測定もあります（4.1.2.1 から 4.1.2.20 の見出しで確かめました）。
- ほかに、認証（4.1.6）、購読と通知（4.1.8、4.1.9）、即時メッセージ（4.1.10）、HSS のプロビジョニングと登録の状態（4.2.1）、MRFC（4.6）、RTP のパケット数とオクテット数（4.7.1）の測定があります。
- KPI は TS 32.454 にあります。測定の値から計算する比や平均です。
  - 使いやすさ（Accessibility）。S-CSCF の初期登録の成功率（5.1.1。`UR.SuccInitReg` を `UR.AttInitReg` で割る）、セッションの確立の時間の平均（5.1.2）、セッションの確立の成功率（5.1.3）、第三者登録の成功率（5.1.4）、再登録の成功率（5.1.5）、即時メッセージの成功率（5.1.8）など。
  - 保持（Retainability）。IMS のセッションの切断率（5.2.1。`SC.DroppedSession` を成功したセッションの確立の数で割る）。
  - 利用（Utilization）。セッションの利用の平均（5.3.1）。
- 5GS の PM は、測定のジョブを作り、結果をファイルかストリームで報告する MnS として決められています（TS 28.550 4.3）。

### 構成の管理（CM）

- 構成を変えるときに管理の仕組みが支えるべき面は、セキュリティ、データの正しさ、データの一貫性、資源の管理です（TS 32.600 4.2.1）。構成を変えられるのは、権限を持つ運用者だけです（4.2.1.1）。
- 管理の仕組みと装置で情報が食い違わないように、監査の手順を決めます。管理の側が装置から情報を読み、自分の持つ情報と比べ、違いを運用者に知らせ、どちらかを直します。どちらを正とするかは製品によります（4.2.1.3）。
- CM には Basic CM と Bulk CM の 2 つの進め方があります。Bulk CM は、設定をファイルでまとめて取り出し、まとめて送り、1 つの操作で網全体に有効にし、1 つの操作で前の安定した構成に戻せることを特徴とします（7.1）。一括のプロビジョニングと、前の設定への戻しは、この考え方に当たります。

### トレース

- トレースは、特定の利用者や端末について、信号のインタフェースのデータを呼の単位で記録する働きです。範囲（どの NE のどのインタフェースか）と深さ（どこまで細かく記録するか）で決めます。深さは 6 段階で、Maximum は信号のメッセージを符号化されたまま、Minimum は IE の一部を復号して記録します（TS 32.421 4.1）。
- IMS には Service Level Tracing があります。起動の方法は、UE で起動する方法と、IMS の NE で起動する方法の 2 つです（TS 32.421 4.3）。NE で起動するときは、管理の仕組みが HSS に指示し、HSS が S-CSCF、I-CSCF、AS に、S-CSCF と I-CSCF が P-CSCF に伝えます（TS 32.422 4.1.2.9）。指示には、対象の Public User Identity、サービスの識別、Trace Reference が入ります。
- 記録の中身と形式は TS 32.423 にあります。記録の対象に S-CSCF と P-CSCF が挙がっています（4 章の先頭）。

### このプロジェクトとの対応

| このプロジェクトの機能 | 標準の考え方 | 借りるもの | 借りないもの |
| --- | --- | --- | --- |
| 保守用の API | EM と NE のあいだ。製品ごと（TS 32.600 4.2.1.3） | 状態の名前（`administrativeState`、`operationalState` とその値） | MnS の操作と NRM の全体。DN の決まり |
| OAM のサーバー | EM | 監査の考え方（4.2.1.3） | Itf-N |
| 警報 | TS 28.111 | 警報の記録の属性、同じ警報の決め方、一覧と通知の分け方、確認（ack） | コメント、履歴、相関、通知の購読 |
| 指標 | TS 32.409、TS 32.454 | 測定の名前と数える時点、KPI の式 | 測定のジョブ、granularity period、XML のファイル |
| プロビジョニング | Bulk CM（TS 32.600 7.1） | まとめて送り、結果を 1 件ずつ返す考え方 | Bulk CM IRP のファイルの形式 |
| 設定の履歴と戻し | Bulk CM の fallback（7.1） | 1 つの操作で前の構成に戻す考え方 | なし |
| 呼の解析 | トレース（TS 32.421） | 利用者を指定して信号を記録する考え方、深さの段階 | トレースの起動の信号、TS 32.423 の形式 |

## 2. OAM を作る前のコードとの差

この章は、OAM の機能を作る前のコードを読んで、足りないものを挙げたものです。その後に作ったものは 3 章以降にあります。

- 保守に使える HTTP の口を持つのは、HSS、中継ノード、EPC のスタブの 3 つです。HSS は加入者の追加、表示、削除（`internal/hss/provisioning.go` の `Provisioning`）、中継ノードはコンテキストの一覧（`internal/agw/api.go` の `GET /contexts`）を持ちます。EPC のスタブは、UE 向けの API と同じ待ち受けに、UE の状態の表示、bearer の解放、UE の切り離しを `/maintenance/ues/{imsi}` の下に持ちます（`internal/epc/http.go`）。この経路は、表示が `GET /ues/{imsi}`、bearer の解放が `DELETE /network-initiated/ues/{imsi}/bearers/{ebi}`、切り離しが `POST /network-initiated/ues/{imsi}/detach` です。CSCF と IP-SM-GW には HTTP の待ち受けがありません。
- ログは `cmd/ims/main.go` で `slog.NewJSONHandler(os.Stdout, nil)` を作っていて、詳しさは INFO に固定です。動かしたまま変える手段がありません。
- 設定は起動のときに 1 回読むだけです（`internal/config/config.go` の `Load`）。動かしたまま変える項目も、変更の記録もありません。
- ドレインはありません。冗長構成の作業で作ります。
- 指標を数える所がありません。数を持っているのは、HEP の送信の取りこぼし（`internal/hep/sender.go` の `Dropped`）だけです。
- ダッシュボード（`internal/dashboard`）は、HEP で届いたメッセージだけから登録と呼の状態を組み立てます（[lab-oam.md](../design/lab-oam.md)）。ノードの内部の状態は読みません。持つメッセージの数には上限があり（`Config.MaxMessages`）、古い呼はメッセージと一緒に消えます。呼ごとの状態（`Call.State`）と最後の応答コード（`Call.Status`）は持っていますが、理由ごとの件数は持っていません。
- HSS のプロビジョニングの API には、変更（PUT）と一括がありません。一覧は全件を並べ替えて最大 1000 件を返します（`listLimit`。規模を広げる妨げとして一覧にしてあるもの。[state-redundancy.md](../design/state-redundancy.md)）。
- HSS の加入者は、起動のときに設定ファイルから `MemoryStore` に入ります。API で足した加入者と、登録の状態、SQN は、HSS を止めると消えます。
- 状態を持つ `Store` は 5 つあります（`internal/hss`、`internal/scscf`、`internal/pcscf`、`internal/ipsmgw`、`internal/sc`）。どれも実装はプロセスの中の `MemoryStore` だけで、中身を外に書き出す操作はありません（状態をノードの外に置けるようにする方針。[state-redundancy.md](../design/state-redundancy.md)）。
- HSS は、すべての加入者に同じ iFC を返します（`internal/hss/profile.go` の `SIMPLIFIED: TS 29.228 B.2.2`）。加入者ごとにサービスを変える手段がありません。
- 画面（`web/dashboard`）は 1 枚のページで、呼、登録、加入者、UE の操作を並べています。

## 3. 保守用の API

### 設計の候補

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 置き場所 | すべての `ims` のプロセスに、保守用の待ち受けを 1 つ足す。設定の `maintenance.listen` に書く。役割が複数あるノードでも待ち受けは 1 つで、経路で役割を分ける。処理は新しいパッケージ `internal/maintenance` に置く | 役割ごとに別の待ち受けを持つ。今の HSS と中継ノードの形の延長だが、OAM がノードごとに複数の口を知ることになる |
| 今ある API との関係 | HSS のプロビジョニング（8081 番）と中継ノードの制御（8080 番）は残す。プロビジョニングは加入者のデータ、制御は呼の処理のための口で、保守とは目的が違うため。EPC のスタブの `/maintenance/ues/…` は、異常系を起こすための口なので、そのまま残す（`/network-initiated/ues/…` と `GET /ues/{imsi}` の形。5GC のスタブも同じ。`internal/epc/http.go`、`internal/fivegc/http.go`）。OAM は、障害の注入の操作としてこれを呼ぶ。経路の先頭が保守用の API と同じ `/maintenance` なので、保守用の API は `/maintenance/v1/` で始めて区別する | 保守用の待ち受けにまとめる。P-CSCF が呼の処理で使う口と、運用者の口が同じになる |
| 状態の表し方 | ノードを `ManagedElement`、役割を `ManagedFunction` に見立て、役割ごとに `administrativeState`（`UNLOCKED`、`SHUTTINGDOWN`、`LOCKED`）と `operationalState`（`ENABLED`、`DISABLED`）を返す。名前と値は TS 28.622 5.3 に合わせる | 独自の名前（`draining` など）にする。標準の言葉を学べない |
| ドレイン | `administrativeState` を `SHUTTINGDOWN` にする操作として出す。新規の登録と呼を断り、残っている呼が無くなったら `LOCKED` になる。解除は `UNLOCKED` に戻す。中身は冗長構成で作るドレインを呼ぶ | 専用の `POST /drain` にする。動きは同じで、名前だけが違う |
| 状態に含めるもの | 役割、2 つの状態、起動した時刻、ビルドの版、設定の世代、接続先ごとの状態（Diameter の相手、中継ノード、HEP の送り先）、残っている呼の数 | 登録している利用者の一覧まで返す。全件の走査になる。数だけを返し、一覧は `Store` の索引で 1 件ずつ引く |
| 設定の参照 | 読み込んだ設定を JSON で返す。パスワード、K、OPc は伏せる | YAML のファイルをそのまま返す。秘密の値が出る |
| 設定の変更 | 動かしたまま変えられる項目を決め、その項目だけを `PATCH` で受ける。ほかの項目は、変えるには再起動が要ると答える。最初の項目は、ログの詳しさ、管理上の状態、HSS の収容の方針（9 章）にしぼる | 設定の全体を受けて、ノードが自分で再読み込みする。待ち受けや役割のように、動かしたまま変えられない項目の扱いが難しい |
| ログの詳しさ | `slog.LevelVar` を使い、`PUT` で DEBUG、INFO、WARN、ERROR を選ぶ。役割ごとに変えられるようにする | 環境変数で決める。変えるには再起動が要る |
| 最近のログ | ノードが最近のログを決まった件数だけメモリに持ち、Call-ID で絞って返す（11 章） | なし |
| 認可 | 既定では付けない。待ち受けを Compose の内側のネットワークだけに置き、ホストに公開しない。TS 32.600 4.2.1.1 は権限のある運用者だけが変えられることを求めているので、省いたことを手順書に書く | 共有のトークンをヘッダで渡す。暗号化や認可は後から有効にできるオプションにする方針なので、あとから足せる |

API の形の案です。経路は確定ではありません。

| 操作 | 経路 |
| --- | --- |
| ノードと役割の状態 | `GET /maintenance/v1/state` |
| 役割の管理上の状態を変える | `PUT /maintenance/v1/functions/{role}/administrative-state` |
| 設定を読む | `GET /maintenance/v1/config` |
| 動かしたまま変えられる項目を変える | `PATCH /maintenance/v1/config` |
| ログの詳しさ | `GET` と `PUT /maintenance/v1/log-level` |
| 最近のログ | `GET /maintenance/v1/logs?call_id=…` |
| 警報の一覧 | `GET /maintenance/v1/alarms` |
| 指標 | `GET /metrics` |
| 退避と復元（状態を持つ役割だけ） | `GET` と `PUT /maintenance/v1/functions/{role}/data` |

### 作った API

`internal/maintenance` に、状態、管理上の状態（ドレイン）、設定、ログの詳しさの 4 つを作りました。最近のログ、退避と復元は作っていません。警報は、同じハンドラに経路を足して作りました（6 章の「作ったもの」）。方法と経路はパッケージの文書にも書いてあります。本文は JSON で、時刻は UTC の RFC 3339 です。

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `GET` | `/maintenance/v1/state` | なし | `NodeState`。ノードの名前、`instance`、ビルドの版、起動した時刻、設定の世代、ログの詳しさと、役割ごとの `FunctionState`（`administrative_state`、`operational_state`、残っている利用者の数、ドレインの期限、役割の `summary`） |
| `PUT` | `/maintenance/v1/functions/{role}/administrative-state` | `AdministrativeStateRequest`。`administrative_state`、`timeout_seconds`（`SHUTTINGDOWN` のときだけ）、`operator` | その役割の `FunctionState` |
| `GET` | `/maintenance/v1/config` | なし | `Configuration`。`generation`、秘密の値を伏せた `configuration`、変えられる項目の一覧 `changeable` |
| `PATCH` | `/maintenance/v1/config` | `ConfigurationChangeRequest`。`settings`（項目の名前と新しい値）、`generation`、`operator`、`comment` | `ConfigurationChange`。ノード、世代、時刻、操作した人、理由、変わった項目ごとの前の値と後の値 |
| `GET` | `/maintenance/v1/log-level` | なし | `LogLevel`。`level` |
| `PUT` | `/maintenance/v1/log-level` | `LogLevelRequest`。`level`、`operator` | `LogLevelChange`。`level`、`previous` |

エラーの応答は、どれも `{"error": "…"}` です。設定の変更を断ったときは、断った項目と理由を `settings` に入れます。状態コードは次のとおりです。

- 400。本文を読めない、知らない欄がある、値が範囲の外にある、変えられない項目を指定した。
- 404。ノードが持たない役割か、知らない経路。
- 405。経路が受けない方法。`Allow` を付けます。
- 409。X.731 が許さない移り方（`LOCKED` の役割の shut down）か、ノードの世代より大きくない世代の変更。
- 500。設定の見え方や `summary` に、伏せていない秘密の値があった。

案から変えたこと、決めたことは次のとおりです。

- 管理上の状態は X.731 の 4 つの出来事に合わせました。`PUT` の値は、その状態に向かう出来事を表します。`UNLOCKED` が unlock、`SHUTTINGDOWN` が shut down、`LOCKED` が lock です。今の状態と同じ値を送っても何も変えず、成功を返します。OAM が同じ役割のすべてのノードに同じ要求を送れるようにするためです。
- X.731 には期限がありません。ドレインの期限（`timeout_seconds`）が過ぎたら、運用者が lock を送ったのと同じに扱い、`LOCKED` にします。残っている呼をどうするかは、役割が決めます。
- 役割が渡すものは、動作の状態、残っている利用者の数、`summary`、状態が変わったことの知らせの 4 つのコールバックです。役割は、新しい登録や呼を受ける前に `ManagedFunction.AdministrativeState` を読み、呼が終わるたびに `UserQuit` を呼びます。管理上の状態はプロセスの中にあり、再起動すると `UNLOCKED` から始まります。
- 設定の見え方は、ノードが `any` で渡します。秘密の値は `maintenance.Secret` にします。`Secret` は値を持たず、設定されているかどうかだけを持ちます。ハンドラは答える前に、秘密の名前（`password`、`k`、`opc`、`nonce_key` など）の欄に値が無いことを確かめます。役割の試験は `maintenancetest.AssertRedacted` で同じことを確かめます。
  - `maintenancetest` は消しました。どこからも import されず、`maintenance.CheckRedacted` を呼ぶだけだったためです。役割の試験は `CheckRedacted` を直接呼べます。
- 動かしたまま変えられる項目は、役割が `NewSetting` で宣言します。値の確かめと、失敗しない設定の関数を分けて受け、すべての項目を確かめてから設定します。1 つでも断れば何も変えません。秘密の値は項目にできません。
- 変更の記録は、OAM が設定の履歴に持つ形（10 章）にしました。OAM が世代の番号を付けて送った変更は、ノードの世代より大きいときだけ受けます。番号を付けなければ、ノードが 1 つ増やします。
- ログの詳しさと管理上の状態は、専用の経路だけで変えます。案のように `PATCH /config` の項目にはしていません。同じものを 2 つの経路で変えられないようにするためです。
- ログの詳しさは、ノードに 1 つです。案の「役割ごとに変える」はしていません。`cmd/ims` のログの handler が 1 つだからです。
- JSON の欄の名前は、このリポジトリのほかの API に合わせて snake_case にしました（`administrative_state`）。値は TS 28.622 5.3.1 と 5.3.3 のままです。
- ノードの名前（`node`）とは別に `instance` を持ちます。同じ設定のファイルで複数台を動かすと、`node` は同じになるためです。ホスト名や Pod の名前を入れます。
- `internal/maintenance` は `internal/metrics` を import しません。ノードが 1 つの `http.ServeMux` に `maintenance.PathPrefix` と `GET /metrics` の両方を載せます。パッケージの Example がその形です。片方だけを使う役割も作れるようにするためです。
- 認可はありません。TS 32.600 4.2.1.1 は、権限のある運用者だけが構成を変えられることを求めています。この API は、保守用の待ち受けがノードと OAM の内側のネットワークにだけあり、ホストやインターネットに公開しないことを前提にします。パッケージの文書に `SIMPLIFIED:` として書きました。要求の `operator` は名乗った名前で、確かめた身元ではありません。

## 4. OAM のサーバー

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 置き場所 | `ims` コマンドの新しい役割 `oam`（1 つのイメージが設定で役割を切り替える方針に従う）。処理は `internal/oam` に置く。今の `dashboard` の役割は `oam` に含め、`internal/dashboard` のハンドラを `oam` の HTTP の経路の下に載せる | `dashboard` と `oam` を別の役割のままにし、画面からリンクでつなぐ。要件は、ダッシュボードが OAM の画面の一部になることを求めている |
| ノードの見つけ方 | OAM の設定に、役割ごとの DNS の名前を書く。OAM は名前を引き直して、今あるノードを知る。Compose で同じ役割を複数台にしたときと、Kubernetes の headless Service の両方で働く | (a) ノードの一覧を設定に固定で書く。台数を変えるたびに設定を直す。(b) ノードが起動のときに OAM に名乗り出る。ノードが OAM の場所を知る必要があり、OAM が落ちていると名乗れない |
| OAM が持つ状態 | 設定の履歴（10 章）と、退避したデータの置き場所だけを、OAM のボリュームに持つ。ノードの状態と警報は持たず、画面を開くたびにノードに尋ねる | OAM がすべてを写して持つ。ノードが増えると、写しを合わせる処理が要る |
| 呼の経路との関係 | OAM は呼の経路に入らない。OAM が止まっても、登録と呼は続く（ダッシュボードと同じ。[lab-oam.md](../design/lab-oam.md)） | なし |
| 画面 | `web/dashboard` に、ノード、警報、指標、加入者、設定、解析のページを足す。今のページは「信号」のページとして残す | 別の Web アプリを作る。ビルドが 2 つになる |
| まとめて操作する | 役割を選んで、その役割のすべてのノードに同じ操作を送る。結果はノードごとに返す | なし |

役割の名前を `dashboard` から `oam` に変えると、Compose の設定と手順書を直すことになります。名前を変えずに `dashboard` の役割を広げる案もあります。13 章の未決の論点に挙げます。

### 作った API

`internal/oam` に、OAM のサーバーの本体を作りました。HTTP のハンドラと、その後ろの処理です。画面はありません。`cmd/ims`、設定、Compose へのつなぎ込みは作っていません。経路はすべて `/oam/v1/` で始まり、役割はハンドラを `oam.PathPrefix` の下に載せます。本文は JSON です。エラーの本文は、保守用の API と同じ `maintenance.ErrorBody`（`{"error": "…"}`）です。経路と型はパッケージの文書にも書いてあります。

OAM が import するのは `internal/maintenance` と標準ライブラリだけです。HSS のプロビジョニングの API は、`internal/hss` を import せずに、JSON の形を自分の型に写して話します。

#### ノードの操作

ノードの一覧（`Endpoint`）は設定で渡します。1 件は、OAM の中の名前、役割、保守用の待ち受けの URL です。同じ役割のノードを何台でも書けます。上の表の最初の案（役割ごとの DNS の名前を引き直す）は作っていません。ノードの一覧は設定で渡す形にしました。DNS で見つける形にするときは、一覧を作る所だけを替えます。

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `GET` | `/oam/v1/inventory` | なし | `Inventory`。設定にあるノードの一覧 |
| `GET` | `/oam/v1/state` | なし | すべてのノードの `maintenance.NodeState` の `FanOut` |
| `GET` | `/oam/v1/nodes/{node}/state`、`/oam/v1/roles/{role}/state` | なし | 1 台か、その役割のすべてのノードの `FanOut` |
| `PUT` | `/oam/v1/nodes/{node}/functions/{role}/administrative-state`、`/oam/v1/roles/{role}/administrative-state` | `maintenance.AdministrativeStateRequest` | `maintenance.FunctionState` の `FanOut`。`SHUTTINGDOWN` でドレインを始め、`UNLOCKED` で取り消す |
| `GET`、`PUT` | `/oam/v1/nodes/{node}/log-level`、`/oam/v1/roles/{role}/log-level` | `PUT` は `maintenance.LogLevelRequest` | `LogLevel` か `LogLevelChange` の `FanOut` |
| `GET` | `/oam/v1/nodes/{node}/config`、`/oam/v1/roles/{role}/config` | なし | `maintenance.Configuration` の `FanOut`。秘密の値は伏せてある |

決めたことは次のとおりです。

- 複数のノードへの操作の答えは `FanOut` です。対象のノードごとに 1 件あり、ノードの答えか、失敗の理由（ノードの状態コードと `ErrorBody`、または答えが無かったこと）が入ります。失敗したノードを省くことはありません。一部のノードが失敗しても、状態コードは 200 です。利用者は `succeeded` と `failed` を読みます。
- 同時に呼ぶノードの数に上限（`NodeConcurrency`、既定は 16）を、1 台ごとに期限（`NodeTimeout`、既定は 5 秒）を設けました。期限までに答えないノードは「no answer within 5s」として失敗に数えます。
- ノードの役割は、一覧ではなくノード自身が知っています。1 台を指定してそのノードに無い役割をドレインすると、そのノードの 404 が答えに入ります。
- ログの詳しさはノードに 1 つなので、役割を指定して変えると、そのノードのほかの役割の分も変わります。
- 設定を読んだ答えも、OAM が `maintenance.CheckRedacted` で確かめ直します。伏せていない秘密の値があれば、そのノードを失敗にして、値は返しません。

#### 設定の変更の記録と戻し

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `PATCH` | `/oam/v1/nodes/{node}/config`、`/oam/v1/roles/{role}/config` | `maintenance.ConfigurationChangeRequest`。`generation` は付けない | `ChangeRecord` の `FanOut` |
| `GET` | `/oam/v1/nodes/{node}/config/history?limit=n` | なし | `History`。そのノードの記録を新しい順に。既定は 50 件 |
| `GET` | `/oam/v1/config/history/{id}` | なし | `ChangeRecord` |
| `POST` | `/oam/v1/config/history/{id}/rollback` | `RollbackRequest`（`operator`、`comment`、`force`）。省略できる | 戻した変更の `ChangeRecord` |
| `POST` | `/oam/v1/config/change-sets/{id}/rollback` | `RollbackRequest` | `ChangeRecord` の `FanOut`。役割を 1 度に変えた記録を、ノードごとに戻す |

- `ChangeRecord` は、ノードが返した `maintenance.ConfigurationChange`（ノード、世代、時刻、操作した人、理由、項目ごとの前の値と後の値）に、記録の ID、`change_set`、一覧でのノードの名前、記録した時刻、戻した元の ID（`rollback_of`）、押し切ったかどうか（`forced`）を足したものです。`change_set` は、1 回の操作で変えたノードの記録に共通の ID です。
- 世代の番号は OAM が付けます。対象のノードの状態を読み、いちばん大きい世代に 1 を足した番号を、すべての対象に送ります。同じ役割のノードは同じ世代になります（10 章）。2 つの OAM が同時に変えたときは、ノードが「今より大きくない世代」を 409 で断るので、片方だけが通ります。要求に `generation` を付けると 400 です。
- 戻しは、変更の前の値を、新しい世代の変更として送ります。履歴は書き換えません。TS 32.600 V19.0.0 は、変更が安定しないときに前の安定した構成へ戻すこと（5.1）と、Bulk CM で 1 つの操作で戻せること（7.1 の d）を挙げています。
- 戻しを断る条件は 2 つです。同じノードのあとの記録が同じ項目を変えたとき、または、ノードの今の値が、その変更で入れた値と違うとき（OAM を通さずに変えられたとき）です。どちらも 409 で、`ErrorBody.settings` に項目ごとの理由を入れます。`force` を付けると押し切り、記録に `forced` が付きます。
- 履歴は、追記だけのファイル（`FileChangeStore`、1 行に 1 件の JSON、0600）に置きます。秘密の値は入りません。変えられる項目は秘密にできないためです（`maintenance.NewSetting`）。一覧や ID での検索はファイル全体を読みます。履歴の長さは運用者の操作の数に比例し、加入者の数には比例しないので、全加入者の走査には当たりません。外部の保存先に替えるときは、ノードごとの索引を持たせます。

#### 加入者のプロビジョニング

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `POST` | `/oam/v1/provisioning?dry_run=true` | `SubscriptionRecord` を 1 行に 1 件 | `StreamLine` を 1 行に 1 件（`application/x-ndjson`）。行ごとの `outcome`、最後に `summary` |
| `GET` | `/oam/v1/subscriptions/{impi}` | なし | `Subscription`。HSS が見せる形で、資格情報は無い |
| `PUT` | `/oam/v1/subscriptions/{impi}?dry_run=true` | `SubscriptionRecord` | `Outcome`。作ったときは 201、ほかは 200 |
| `DELETE` | `/oam/v1/subscriptions/{impi}?dry_run=true` | なし | `Outcome`。無いときは 404 |

1 行の形式（`SubscriptionRecord`）は、HSS の `subscriptionRequest` と同じ名前の欄に、HSS に用意してもらう欄を足したものです。

| 欄 | 中身 | 今の HSS |
| --- | --- | --- |
| `private_identity` | 必須。`<IMSI>@<ドメイン>` など | ある |
| `public_identities` | SIP か tel の URI。最初が既定の公開の識別子 | ある |
| `password`、`aka`（`k`、`opc`、`amf`、`sqn`） | どちらか 1 つは必須。16 進数 | ある |
| `tel`、`ut_password`、`mmtel_services` | HSS の API と同じ | ある |
| `repository_data` | Sh の透過データ（`service_indication` と XML の `service_data`）。退避した記録が持つ。`mmtel_services` と `ut_password` とは一緒に使えない | 無い |
| `test_user`、`service_profile` | 試験用の利用者の印と、サービスのプロファイルの名前 | 無い |
| `delete` | `true` の行は、その加入者を消す。`private_identity` のほかの欄を持てない。OAM だけが読み、HSS には送らない | 対象外 |

決めたことは次のとおりです。

- 結果は 6 種類です。`created`、`updated`、`unchanged`、`deleted`、`rejected`、`failed` です。初めの 4 種類（`created`、`updated`、`unchanged`、`rejected`）に、`delete` の行のための `deleted` と、HSS が答えないなど記録のせいではない失敗の `failed` を足しました。`rejected` は送り直しても変わらず、`failed` は送り直せば通るかもしれない、という区別です。
- 同じファイルを送り直すと、すべての行が `unchanged` になります。変わったかどうかは HSS が決めます。HSS は資格情報を外に出さないので、OAM には比べられないためです。そのため、作るか置き換える `PUT /subscriptions/{impi}` が HSS に要ります（14 章）。
- `dry_run=true` では、HSS に「どうなるか」だけを尋ね、何も変えません。消す行は、HSS で引いてみて、あれば `deleted`、無ければ `unchanged` と答えます。
- OAM は 1 行ずつ読み、確かめ、HSS に送り、結果を返します。同時に HSS に送る件数は `ProvisioningConcurrency`（既定は 8）までで、結果を待つ件数も同じだけです。ファイルの長さに関わらず、使うメモリは一定です。HTTP/1.1 で読みながら書くために、`http.ResponseController.EnableFullDuplex` を使っています。
- 結果は行の順に返します。ただし、HSS に届く順は決まっていません。同じ加入者を 2 つの行に書かないでください。
- 1 行は 64 KiB までです。HSS が 1 件の本文に設けている上限と同じです。長すぎる行は、その行だけ `rejected` にして続けます。空の行は数えるだけで、結果を返しません。
- 識別子はすべて試験用のデータでなければなりません。このプロジェクトは試験用の値だけを使います。数字の識別子は、PLMN 001/01 の試験用の IMSI（15 桁、`00101` で始まる）か、`+1 NXX 555 01XX` の架空の番号に限ります。`alice` のような数字でない名前は通します。tel URI の区切り（`-`、`.`、`(`、`)`）は、RFC 3966 の 3 と 5.1.1 のとおり、番号の一部でないものとして外してから確かめます。
- 資格情報（`password`、`ut_password`、`aka.k`、`aka.opc`）は HSS にだけ送ります。型 `SecretString` は、JSON では値を出し、`fmt` と `slog` では `REDACTED` を出します。HSS の断りの文がそれを引用しても、OAM は結果に入れる前に伏せます。試験では、断りの文に本文をそのまま入れる HSS を使って確かめています。
- 経路を `/oam/v1/subscriptions/bulk` にしなかったのは、Go の `http.ServeMux` で `/oam/v1/subscriptions/{impi}` とぶつかるためです。

#### 退避と復元

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `POST` | `/oam/v1/backups` | `BackupRequest`（`operator`、`comment`）。省略できる | `StreamLine` の流れ。手順ごとの `step`、最後に `manifest` |
| `GET` | `/oam/v1/backups` | なし | `Backups`。新しい順 |
| `GET` | `/oam/v1/backups/{id}` | なし | `Manifest`。手順と、それまでの復元の記録を含む |
| `POST` | `/oam/v1/backups/{id}/restore?dry_run=true` | `RestoreRequest`（`operator`）。省略できる | `StreamLine` の流れ。`step`、加入者ごとの `outcome`、最後に `summary` |

- 退避の手順は、区分（`Section`）ごとの `export` と、書庫を閉じる `store` です。復元の手順は、書庫を最後まで読んで確かめる `verify` と、`diff`（dry run）か `apply` です。どの手順も、始まり、途中（1000 件ごと）、終わりを `step` で流し、`Manifest` に残します。
- 書庫は 1 行に 1 件の JSON です。先頭の行に形式の名前と版、ID、時刻、区分の一覧を、続く行に `{"section": "hss.subscriptions", "record": {…}}` を、最後の行に区分ごとの件数を書きます。最後の行が無い書庫と、件数が合わない書庫は、`verify` で断ります。1 件も戻しません。
- 復元は、退避した記録を、一括のプロビジョニングと同じ `PUT` で戻します。同じ書庫を 2 回戻すと、2 回目はすべて `unchanged` です。戻した加入者は Not Registered です（8 章の最初の案）。HSS に今ある加入者で書庫に無いものは、消しません。
- SQN_HE は、退避したときの値のまま戻します。13 章の未決の論点の 2（幅を足すか）が決まっていないので、足していません。
- 退避の対象は、今は HSS の加入者だけです。ほかの役割の持ち続けるデータは、`Config.Sections` に `Section` を足して加えます。8 章の表のとおり、次の候補は SC の未配達の SMS です。SC の保守用の API がそれを出すようになってから作ります。登録やダイアログのような動いているあいだの状態は、退避しません。13 章の 3 の判断を待っています。
- HSS には、すべての加入者を一度に返す口がありません。今の一覧は最大 1000 件で、資格情報を含みません。そこで、ページに分けた `GET /subscriptions/export?after={impi}&limit={n}` が HSS に要ります（14 章の `Store.List` と同じ操作）。OAM は 500 件ずつ読み、次のページは前のページの最後の識別子から始めます。
- 書庫には、K、OPc、パスワードがそのまま入ります。HSS に戻すために要るからです。`FileBackupStore` は、ディレクトリを 0700、書庫と記録のファイルを 0600 にします。書き終えるまでは `<id>.jsonl.partial` に書き、終わったら名前を変えるので、途中で止まった退避が書庫として残ることはありません。
- 書庫は暗号化しません。TS 33.102 V19.1.0 6.3.1 は、鍵 K を USIM と AuC だけが持つものとしています。製品では、K を含む退避は暗号化します。ここでは試験用の値だけを使う決まりがあり、暗号化には鍵とその扱いが別に要るので、`SIMPLIFIED:` として省きました。
- 同じ書庫を 2 つの OAM が同時に復元すると、`Manifest` の復元の記録が 1 つ失われることがあります。記録は復元が終わってから書くためです。

#### 試験用の利用者

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `GET` | `/oam/v1/test-users` | なし | `TestUsers`。収容の方針、2 つのプロファイルの名前、プロファイルごとの機能 |
| `PUT` | `/oam/v1/test-users/service-profile` | `ServiceProfileRequest`（`service_profile`、`operator`、`comment`） | `ChangeRecord` の `FanOut` |
| `PUT` | `/oam/v1/test-users/admission` | `AdmissionRequest`（`admission` は `all` か `test_users`） | `ChangeRecord` の `FanOut` |
| `PUT` | `/oam/v1/subscriptions/{impi}/test-user` | `TestUserRequest`（`test_user`、`operator`） | `Subscription` |
| `GET` | `/oam/v1/subscriptions/{impi}/features/{feature}` | なし | `FeatureState`。有効かどうかと、有効でない理由 |

9 章の最初の案どおり、印もプロファイルも HSS のデータにしました。OAM が持つものはありません。

- 印は、加入者の `test_user` です。一括の形式でも、1 件の `PUT .../test-user` でも変えられます。1 件の変更には、資格情報に触れない `PATCH /subscriptions/{impi}` が HSS に要ります。
- サービスのプロファイルは、HSS の設定に名前付きで置き、それぞれが機能の名前の一覧を持つ形を考えました。機能は、iFC の組に名前を付けたものです。たとえば `mmtel` は、MMTel AS を呼ぶ iFC です。
- 加入者のプロファイルは、HSS が次の順に決めます。加入者に `service_profile` があればそれ、試験用の利用者なら `hss.test_user_service_profile`、ほかは `hss.default_service_profile` です。
- 「機能を試験用の利用者だけに有効にする」は、その機能を持つプロファイルを `hss.test_user_service_profile` にすることです。すべての HSS のノードへの設定の変更なので、履歴に残り、`change_set` で戻せます。全員に広げるときは、`hss.default_service_profile` を同じように変えます（`PATCH /oam/v1/roles/hss/config`）。試験用の利用者を 1 人ずつ足していく広げ方もできます。
- 「試験用の利用者だけを収容する」は、HSS の `hss.admission` を `test_users` にすることです。HSS がほかの利用者の UAR を断る作り（9 章）が HSS に要ります。
- 「機能 X は加入者 Y に有効か」には、その加入者の 1 件と、HSS のノード 1 台の設定だけから答えます。収容されていること、決まったプロファイルが X を持つことの両方を満たせば有効です。全加入者を走査しません。
- HSS のノードの設定の見え方には、次の形が要ります。`changeable` には、上の 3 つの項目が文字列で入ります。

```json
{"hss": {"service_profiles": {"default": {"features": ["sms"]}, "trial": {"features": ["sms", "mmtel"]}}}}
```

#### 状態コード

- 400。本文を読めない、知らない欄がある、値が範囲の外にある、`dry_run` が真偽値でない。
- 404。一覧に無いノードや役割、無い加入者、記録、退避、機能。知らない経路。
- 405。経路が受けない方法。`Allow` を付けます。
- 409。HSS が、識別子がほかの加入者のものだとして断った。戻しの衝突。完了していない退避からの復元。
- 502。HSS かノードが答えない、または使えない答えを返した。HSS に足りない操作があるときもこれで、理由にそう書きます。
- 503。HSS のプロビジョニングの API、履歴、退避の置き場所、HSS のノードのどれかが、設定に無い。

#### 複数台の OAM

ハンドラは、要求のあいだに何も持ちません。OAM が持つのは、設定の履歴（`ChangeStore`）と退避（`BackupStore`）の 2 つだけです。OAM を複数台にするときは、この 2 つを共有します。このパッケージのファイルの実装なら、1 つのボリュームを共有します。履歴は 1 件を `O_APPEND` の 1 回の書き込みで足すので、同じホストのボリュームなら混ざりません。ネットワークのファイルシステムでの振る舞いは確かめていません。2 台が同時にドレイン、設定の変更、復元をしたときは、ノードと HSS が、管理上の状態、世代、加入者の 1 件ごとに決着を付けます。

#### SIMPLIFIED

- `internal/oam/doc.go`。TS 32.600 V19.0.0 4.2.1.1。認証と認可がありません。保守用の API と同じく、OAM の待ち受けを内側のネットワークにだけ置くことを前提にします。
- `internal/oam/backupstore.go`。TS 33.102 V19.1.0 6.3.1。書庫を暗号化しません。

### 作った画面

`web/oam` に、OAM の簡易な画面を作りました。上の表の最初の案（`web/dashboard` にページを足す）とは違い、別のフロントエンドです。OAM の画面を、ダッシュボードとは別の部品として持つ方針にしたためです。作りは `web/dashboard` に合わせました。TypeScript、Vite、Vitest を同じ版で使い、`tsconfig.json` は同じ中身です。`package-lock.json` も、パッケージの名前のほかは同じです。依存は増やしていません。ビルドと試験は、ダッシュボードと同じく `npm ci && npm test && npm run build` です。画面の文字は、ダッシュボードと同じく英語です。

画面は 1 枚のページで、6 つの部分に分かれます。どれも `/oam/v1/` の API を呼ぶだけで、画面は何も持ちません。Alarms は、警報の API ができてから足した部分です（6 章の「作ったもの」）。

| 部分 | 使う API | 見せるもの、できること |
| --- | --- | --- |
| Nodes | `inventory`、`state` | 役割ごとのまとめ（ノードの数、失敗したノード、管理上の状態と動作の状態の数、利用者の数、設定の世代）と、ノードごと、役割ごとの行（管理上の状態、動作の状態、利用者の数、ドレインの期限、`summary`、ノードの名前と `instance`、世代、ログの詳しさ、ビルド、起動した時刻）。5 秒ごとに読み直す |
| Alarms | `alarms`、`alarms/{id}/ack-state` | すべてのノードの警報をまとめた一覧（重さ、ノード、役割、相手、種類、原因、specific problem、時刻、確認）。cleared と重さの下限で絞る。5 秒ごとに読み直す。答えなかったノードの名前と理由を出す。確認と、確認の取り消し |
| Operations | `administrative-state`、`log-level` | 1 台か役割のすべてのノードに、ドレインの開始（`SHUTTINGDOWN` と期限）、取り消し（`UNLOCKED`）、lock を送る。ログの詳しさを変える。ノードごとの結果を出す |
| Configuration | `config`、`PATCH config`、`config/history`、`rollback` | ノードが伏せたままの設定を見る。変えられる項目を JSON で打って変える。ノードの履歴を見て、1 件か `change_set` ごとに戻す |
| Subscribers | `provisioning`、`subscriptions/{impi}`、`test-user`、`features`、`test-users` | 一括の登録（ファイルか貼り付け、dry run のあとに適用）、1 件の表示、作成と置き換え、削除、試験用の利用者の印、機能が有効かの問い合わせ、収容の方針と試験用の利用者のプロファイルの変更 |
| Backup and restore | `backups`、`backups/{id}`、`restore` | 退避を作り、手順を流れてくるとおりに出す。一覧と詳細（手順と、それまでの復元）。復元の dry run（変わる記録の一覧）と、復元 |

決めたことは次のとおりです。

- 失敗したノードは隠しません。`FanOut` で失敗したノードは、表に「failed」と理由を付けて残します。役割のまとめでは、失敗したノードを状態の数に入れず、別に名前を出します。同じ役割のノードの世代が違うときは「differ」と出します（10 章）。
- ネットワークを変える操作は、すべて確認のダイアログを通します。ダイアログには、対象のノードの名前をすべて書き、何が起きるかを書きます。役割のドレイン、設定の変更、戻し、一括の適用、復元、削除、試験用の利用者の印、収容の方針がこれに当たります。ダイアログを開いたときは「Cancel」に焦点があるので、Enter だけでは実行されません。ダイアログは HTML の `dialog` 要素です。
- 戻しを OAM が 409 で断ったときは、項目ごとの理由を出し、「Roll back anyway (force)」を押してもう一度確認したときだけ `force` を送ります。
- 一括の登録は、dry run が先です。dry run が最後まで通るまで「Apply」は押せず、入力を変えると押せなくなります。適用の確認には、ファイルの名前と行の数と、dry run の結果を書きます。
- ファイルは 200 行（または 256 KiB）ずつに分けて送ります。ブラウザは、要求の本文を送り終えるまで答えを読みません。一方、OAM は 1 行ずつ読みながら答えを書きます。1 回の要求で大きなファイルを送ると、OAM の送信の待ち行列が埋まり、OAM が本文を読めなくなり、両方が止まります。Chromium で確かめると、1 回の要求で 30000 行は 3.5 秒で終わり、60000 行は 60 秒たっても答えが来ませんでした。分けて送る形では、1003 行が 6 回の要求になり、答えの行の番号は画面がファイルの先頭から数え直します。ファイルは少しずつ読むので、長さに関わらず全体を持ちません。
- 流れてくる答えは、1 行ずつ処理します。結果の種類ごとの数を数え、`rejected` と `failed` の行（復元の dry run では、変わる記録）を 500 件まで表にします。それより多い分は数だけを出します。画面の描き直しは、1 フレームに 1 回までです。
- 資格情報は出しません。ファイルの行は画面に出さず、OAM が返す行の番号、秘密の識別子、理由だけを出します（理由の中の資格情報は OAM が伏せます）。設定は、ノードが伏せた形（`REDACTED` か `null`）のまま出します。1 件の置き換えでは、HSS が資格情報を見せないので、パスワードか AKA の K と OPc を打ち直す形にしました。入力欄は `type=password` で、送ったあとに空にします。画面はコンソールにも記録を書きません。
- 操作した人の名前は、ページの上の「Operator」の欄に書いたものを、`operator` として送ります。OAM は名乗った名前を記録するだけです（4 章の SIMPLIFIED）。
- 警報の画面は、警報の API ができるまで作りませんでした。API と一緒に足しました（6 章の「作ったもの」）。
- 色はダッシュボードと同じ値を使い、明るい表示と暗い表示は OS の設定に従います。外部への要求（CDN、フォント、解析）はありません。表は狭い画面では横に送れます。

確かめたことは次のとおりです。

- Vitest の試験。API の型の読み方（本物の形の JSON、Go の `null` の一覧、エラーの本文と `settings`）、流れてくる答えの読み方（行と文字の途中で切れた塊、次の行が届く前に前の行を処理すること）、ファイルの分け方と行の番号の数え直し、画面の表示の作り方です。
- ブラウザでの確認。Playwright の Chromium で、組み立てたページを 2 つの相手に対して動かしました。1 つは `web/oam/mock` の偽の API です。もう 1 つは、本物の `internal/oam` のサーバーに、本物の `internal/maintenance` のノード 3 台と答えないノード 1 台、`PUT`、`PATCH`、export を持つ小さな偽の HSS をつないだものです。こちらはリポジトリの外の一時的なプログラムで、コミットしていません。両方で、ドレイン、設定の変更と断られた項目、戻しの 409 と `force`、1003 行の一括の登録（dry run と適用）、1 件の操作、収容の方針、退避、復元の dry run と復元が、期待どおりに動きました。幅 1280 の明るい表示と、幅 375 の暗い表示で画面を撮って見ています。
- ダッシュボードにはブラウザでの試験が無いので、`web/oam` にもブラウザの試験は入れていません。偽の API は `node mock/serve.ts`（`npm run build` のあと）で、組み立てたページと一緒に 127.0.0.1 で動きます。`npm run dev` でも同じ偽の API を使い、`OAM_URL` を付けると、動いている OAM に中継します。

## 5. 指標

### どの指標を出すか

TS 32.409 の測定を元にします。名前は Prometheus の決まりに合わせ、HELP に TS 32.409 の測定の名前を書いて、標準と照らせるようにします。条項を確かめた測定だけ、右の列に書きます。

| 指標の名前（案） | 種類 | ラベル | TS 32.409 |
| --- | --- | --- | --- |
| `ims_registration_attempts_total` | counter | `role`、`type`（initial、re、de） | `UR.AttInitReg`（4.1.1.1） |
| `ims_registration_successes_total` | counter | `role`、`type` | `UR.SuccInitReg`（4.1.1.1） |
| `ims_registration_failures_total` | counter | `role`、`type`、`status` | `UR.FailInitReg.Cause`（4.1.1.1） |
| `ims_registration_setup_seconds` | histogram | `role` | `UR.MeanInitRegSetupTime`（4.1.1.1。標準はミリ秒の平均） |
| `ims_session_attempts_total` | counter | `role`、`direction` | 4.1.2.1、4.1.2.9、4.1.2.11 |
| `ims_session_successes_total` | counter | `role`、`direction` | 4.1.2.2 |
| `ims_session_answers_total` | counter | `role` | 4.1.2.3 |
| `ims_session_failures_total` | counter | `role`、`status` | `SC.FailSession.Cause`（4.1.2.4） |
| `ims_sessions_dropped_total` | counter | `role` | `SC.DroppedSession`（4.1.2.8） |
| `ims_session_setup_seconds` | histogram | `role` | 4.1.2.6 |
| `ims_sessions` | gauge | `role` | 4.1.2.5、4.1.2.7 |
| `ims_authentication_attempts_total` と `…_failures_total` | counter | `role`、`scheme` | 4.1.6 |
| `ims_message_attempts_total` と `…_failures_total` | counter | `role`、`status` | 4.1.10 |
| `ims_hss_subscriptions` | gauge | なし | 4.2.1 |
| `ims_diameter_requests_total` | counter | `application`、`command`、`result` | 4.2.12 |
| `ims_rtp_packets_total`、`ims_rtp_octets_total` | counter | `direction` | 4.7.1 |
| `ims_sip_messages_total` | counter | `role`、`method`、`status_class`、`direction` | なし（独自） |
| `ims_diameter_peer_up` | gauge | `peer` | なし（独自） |
| `ims_hep_dropped_total` | counter | なし | なし（独自） |
| `ims_alarms` | gauge | `severity` | なし（独自） |

KPI は指標から計算します。たとえば TS 32.454 5.1.1 の初期登録の成功率は、`type="initial"` の成功の数を試行の数で割った値です。5.2.1 の切断率は、`ims_sessions_dropped_total` を成功の数で割った値です。

標準の CC は granularity period ごとに 0 に戻しますが（TS 32.401 4.2.2）、Prometheus の counter は増える一方で、区切りごとの増分は集める側が計算します。この違いを手順書に書きます。

### 名前の決まり

公式のクライアントに付いている検査（`promlint`、v1.25.0 の `prometheus/testutil/promlint/validations`）のソースで確かめた決まりです。

- counter の名前は `_total` で終える。counter でないものは `_total` で終えない。
- 名前とラベルは snake_case で書く。名前に `:` を入れない。
- 単位は基本の単位を使い、略さない。時間は `seconds`、大きさは `bytes` にする。
- 名前に指標の種類（`counter` など）を入れない。
- histogram でないものに `_bucket` と `le` を使わない。histogram と summary でないものに `_count` と `_sum` を使わない。
- HELP を必ず書く。
- `__` で始まるラベルは予約されている（`prometheus/common` の `model.ReservedLabelPrefix`）。

名前の先頭は、このプロジェクトの指標であることが分かるように `ims_` にします。

prometheus.io の公式のページで確かめ直した結果です。

- 「Data model」。名前は UTF-8 の文字を使ってよいが、互換のために `[a-zA-Z_:][a-zA-Z0-9_:]*` に合わせるべき、とあります。`:` は運用者の recording rule のために予約されていて、exporter と計装は使うべきでない、とあります。ラベルの名前は `[a-zA-Z_][a-zA-Z0-9_]*` に合わせるべきで、`__` で始まる名前は Prometheus の内部の用です。空の値のラベルは、ラベルが無いのと同じに扱われます。
- 「Metric and label naming」。名前には、分野を表す 1 語の接頭辞を付けるべき、とあります。単位は 1 つにしなければならず、基本の単位（秒、バイト、メートル）を使い、単位を複数形の接尾辞で付けるべき、とあります。数え上げる値には、単位に加えて `total` を付けます（例 `process_cpu_seconds_total`）。connections や notifications のような「数えられる物」は、この決まりの単位に当たりません。ラベルの名前を指標の名前に入れないこと、ラベルを付けても `sum()` か `avg()` に意味があること、加入者の ID のように値の種類が限られないものをラベルにしないこと、も書かれています。ビットの量も、バイトで表します。
- 「Exposition formats」。テキストの形式の `Content-Type` は `text/plain` に `version=0.0.4` を付けたものです。HELP の行では `\` と改行を、ラベルの値ではそれに加えて `"` をエスケープします。histogram の `_bucket` には `le="+Inf"` が要り、その値は `_count` と同じです。`le` の値は小さい順に並べます。
- 「Prometheus 3.0 migration guide」。Prometheus 3 は、`Content-Type` が無いか読めない対象の取り込みを失敗にします。

`internal/metrics` が守らせる決まり（5 章の「役割ごとの指標の案」）は、これらに反していません。名前とラベルの名前を小文字、数字、`_` にしぼるのは、公式のページが勧める文字の集合よりも狭い決まりです。gauge と histogram が `_total` で終わることを断る決まりは、公式のページには無く、`promlint` の検査から取ったものです。

「役割ごとの指標の案」の 38 の名前も、上の決まりに合っています。時間は `_seconds`（`ims_registration_setup_seconds`、`ims_session_setup_seconds`）、累積の秒数は `_seconds_total`（`ims_session_answered_seconds_total`）、オクテットは `_bytes_total`（`ims_rtp_bytes_total`）です。直す名前はありませんでした。

### ラベルの決まり

ラベルの値の組ごとに、1 つの系列ができます。値の種類が加入者の数に比例して増えるラベルは、全加入者を走査する処理と同じ問題を、指標を集める側に持ち込みます。次のように決めます。

- 使えるラベルは、値の種類が設定か標準で決まるものだけにする。役割、SIP のメソッド、応答コード、Diameter のアプリケーションとコマンドと結果コード、認証方式、方向、access-type、設定に書いた接続先、警報の重さ。
- 使えないラベルは、利用者や呼ごとに変わるもの。IMPU、IMPI、IMSI、MSISDN、Call-ID、UE の IP アドレス、Contact、Diameter の Session-Id、N5 の資源の URI。
- 特定の利用者を追う用途には、指標ではなく、呼の解析（11 章）とログを使う。
- 決まりを型で守る。指標のパッケージは、ラベルの値に文字列をそのまま受けず、値の集合が決まった型（たとえば `metrics.Role`、`metrics.Status`）だけを受ける。応答コードは 100 から 699 の範囲に丸める。
- 試験で守る。違う利用者を 1000 人分流したあとで、系列の数が増えていないことを確かめる。

### 公式のクライアントが要るか

リポジトリの外で 2 つの小さなプログラムを書いて確かめました。

- 公式のクライアント v1.25.0 で counter と histogram を出すと、`Content-Type` は `text/plain; version=0.0.4; charset=utf-8; escaping=underscores` で、本文は `# HELP`、`# TYPE`、`名前{ラベル="値"} 値` の行でした。histogram は `_bucket`（`le` のラベル付き。最後は `+Inf`）、`_sum`、`_count` の 3 種類の行になります。
- 同じ形の本文を、標準ライブラリだけで書きました（`fmt`、`strconv`、`strings`、`sort`）。約 80 行です。それを公式の解析器（`prometheus/common` の `expfmt.TextParser`）に読ませると、4 つの族として誤りなく読め、`promlint` の指摘は 0 件でした。

つまり、テキストの形式は標準ライブラリで書けます。公式のクライアントは必須ではありません。

| 案 | 中身 | 良い点 | 悪い点 |
| --- | --- | --- | --- |
| A. 自作の `internal/metrics` | counter、gauge、決まった境界の histogram と、テキストの形式の出力を標準ライブラリで書く。200 行ほどの見込み | 依存が増えない。ラベルの決まりを型で守れる。学習者が形式を読める | 形式の細部（値のエスケープ、並び順、`+Inf`）を自分で試験する必要がある。Go のランタイムの指標は自分で足す |
| B. 公式のクライアント | `github.com/prometheus/client_golang` v1.25.0 を使う | 実務で見る API と同じ。ランタイムとプロセスの指標が付く。`promlint` を試験に使える | `go.mod` に 8 つのモジュールが増える（下の表）。ラベルの値は文字列で、決まりを型で守れない |

案 B で増える依存と、モジュールに同梱の LICENSE で確かめたライセンスです。

| モジュール | 版 | ライセンス |
| --- | --- | --- |
| github.com/prometheus/client_golang | v1.25.0 | Apache-2.0 |
| github.com/prometheus/client_model | v0.6.3 | Apache-2.0 |
| github.com/prometheus/common | v0.72.0 | Apache-2.0 |
| github.com/prometheus/procfs | v0.22.0 | Apache-2.0 |
| github.com/beorn7/perks | v1.0.1 | MIT |
| github.com/cespare/xxhash/v2 | v2.3.0 | MIT |
| github.com/munnerz/goautoneg | v0.0.0-20191010083416-a7dc8b61c822 | BSD の 3 条項の形（LICENSE の文面から） |
| google.golang.org/protobuf | v1.36.12 | BSD の 3 条項の形（LICENSE の文面から） |

`golang.org/x/sys` も使いますが、すでに `go.mod` にあります。

案 A を採りました。出したい指標は counter、gauge、histogram の 3 種類だけで、形式は小さく、ラベルの決まりを型で守れるためです。出力は、試験のなかで公式の解析器に読ませて確かめられます。その場合、`client_golang` は試験だけの依存になります。試験にも依存を足したくなければ、Prometheus のイメージに入っている `promtool check metrics` を e2e で使います。このプロジェクトは、学習の対象でない部分には既存のライブラリを使う方針なので（[implementation.md](../design/implementation.md)）、案 B もこの方針に合います。

### Prometheus と Grafana

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 既定の構成 | Prometheus と Grafana は、Compose の profile（たとえば `observability`）に入れ、既定では起動しない。1 コマンドの起動を重くしないため | 既定で起動する |
| Prometheus がノードを見つける方法 | Compose では、設定のファイルにノードの名前を書く。Kubernetes では、Pod の注釈か ServiceMonitor を使う | OAM が一覧を HTTP で配る |
| Grafana の画面 | データの元と画面の定義をファイルで置き、起動したら画面がある状態にする。画面は、登録、呼、Diameter、メディア、ノードの 5 枚ほど | 手で作る手順を書く |
| OAM の画面の指標 | OAM がノードの `/metrics` を読み、今の値と、TS 32.454 の式で計算した比を表にする。時間の推移は持たない。グラフは Grafana に任せる（OAM の画面は簡易でよいとしているため） | OAM が時系列を持ってグラフを描く。Prometheus の作り直しになる |

Prometheus と Grafana は別のコンテナとして動かすだけで、`ims` のバイナリには組み込みません。イメージの版は、作るときに固定します。

### 役割ごとの指標の案

`internal/metrics` と `deploy/observability/` は、案 A（依存なしの自作）で作りました。この節は、5 章の最初の表を、`internal/metrics` が守らせる決まりと、TS 32.409 V19.0.0 の本文に合わせて直した案です。条項は、ダウンロードした本文で測定の名前と数える時点を確かめたものだけを書きます。

#### `internal/metrics` が守らせる決まり

`internal/metrics` は次の決まりを守らせます。名前、ラベル、値の集合の決まりに反すると、登録のときに誤りを返します。指標を数える処理は、ロックを取りません。

- 名前は小文字、数字、`_` だけにする。数字で始めない。`:` は recording rule のための予約なので使わない。
- counter の名前は `_total` で終える。gauge と histogram は `_total` で終えない。
- どの名前も `_bucket`、`_count`、`_sum` で終えない。histogram の行の名前と重なるためです。
- HELP を必ず書く。
- ラベルの名前は小文字、数字、`_` だけにする。`__` で始まる名前、`le`、`quantile` は使わない。
- ラベルごとに、取れる値の集合を登録のときに決める。空の値、重なった値、`other` は登録できない。
- 集合に無い値で数えると、値 `other` の系列に数える。捨てずに数えるのは、原因ごとの数の合計を総数に合わせるためです（TS 32.409 4.1.1.1.3、4.1.2.4）。`other` の系列が出ていたら、役割が宣言した集合が足りないことが分かります。
- 1 つの指標の系列の数（ラベルごとの値の数に `other` を足したものの積）は 10000 までにする。誤って加入者ごとの集合を宣言したときに、登録で止めるためです。
- ラベルの付いた系列は、初めて数えたときに出力に現れる。ラベルの無い指標は、登録したときから 0 で出力に出る。

この節の 38 の指標を、この決まりでリポジトリの外で実際に登録してみました。全部が登録でき、出力は公式の解析器、`promlint`、`promtool check metrics` で指摘が 0 件でした。

#### ラベルの値の集合

| ラベル | 値 | 根拠 |
| --- | --- | --- |
| `role` | `pcscf`、`icscf`、`scscf`、`hss`、`agw`、`ipsmgw`、`epc`、`dashboard` のうち、そのノードの設定にある役割 | `internal/config` の役割の名前。1 つのノードが複数の役割を持てるので付ける |
| `direction`（SIP と Diameter） | `received`、`sent` | ノードから見た向き |
| `direction`（セッション） | `originating`、`terminating` | TS 32.409 4.1.2.9、4.1.2.11 の発信と着信 |
| `type` | `initial`、`re`、`de` | TS 32.409 4.1.1.1、4.1.1.2、4.1.1.3.1 |
| `method` | `REGISTER`、`INVITE`、`ACK`、`BYE`、`CANCEL`、`PRACK`、`UPDATE`、`OPTIONS`、`SUBSCRIBE`、`NOTIFY`、`MESSAGE`、`INFO`、`REFER`、`PUBLISH` | 応答では CSeq のメソッド |
| `status_class` | `1xx` から `6xx` | RFC 3261 7.2。`metrics.StatusClass` で数字から変える |
| `status` | 指標ごとに、数えたい応答コードを並べる（下の表） | TS 32.409 の `.Cause` の分け方 |
| `command` | `CER`、`DWR`、`DPR`、`UAR`、`SAR`、`LIR`、`MAR`、`RTR`、`PPR`、`UDR`、`PUR`、`SNR`、`PNR`、`AAR`、`RAR`、`STR`、`ASR` | Cx、Sh、Rx と基本のプロトコル。S6c と SGd は、SMS over NAS に対応するときに足す |
| `result` | `DIAMETER_SUCCESS`、`DIAMETER_FIRST_REGISTRATION`、`DIAMETER_SUBSEQUENT_REGISTRATION`、`DIAMETER_UNREGISTERED_SERVICE`、`DIAMETER_SUCCESS_SERVER_NAME_NOT_STORED`、`DIAMETER_ERROR_USER_UNKNOWN`、`DIAMETER_ERROR_IDENTITIES_DONT_MATCH`、`DIAMETER_ERROR_IDENTITY_NOT_REGISTERED`、`DIAMETER_ERROR_ROAMING_NOT_ALLOWED`、`DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED`、`DIAMETER_ERROR_AUTH_SCHEME_NOT_SUPPORTED`、`DIAMETER_ERROR_IN_ASSIGNMENT_TYPE`、`DIAMETER_UNABLE_TO_COMPLY`、`DIAMETER_UNABLE_TO_DELIVER`、`DIAMETER_TOO_BUSY` | 数字ではなく名前にする。Result-Code の 2001（DIAMETER_SUCCESS）と Experimental-Result-Code の 2001（DIAMETER_FIRST_REGISTRATION）が同じ数字になるためです。Cx の結果コードの数字は `internal/cx/cx.go` の定数にあります |
| `peer` | 設定に書いた Diameter の相手の名前 | 設定で決まる |
| `response` | `180`、`200` | TS 32.409 4.1.2.2 の `.type`。最初の成功を示す応答 |
| `phase` | `before_ringing`、`while_ringing` | TS 32.409 4.1.2.19、4.1.2.20 |
| `format` | `sip`、`tel` | TS 32.409 4.2.1.1.3、4.2.1.1.4 |
| `severity` | `critical`、`major`、`minor`、`warning` | 1 章の「障害の管理」に書いた perceived severity。cleared の警報は一覧に無いので数えない |

`status` の集合の案です。

| 指標 | 値 |
| --- | --- |
| `ims_registration_failures_total` | `400`、`403`、`404`、`408`、`420`、`423`、`480`、`500`、`503`、`504` |
| `ims_session_failures_total` | `400`、`403`、`404`、`408`、`410`、`480`、`484`、`486`、`487`、`488`、`500`、`503`、`504`、`580`、`600`、`603` |
| そのほかの `…_failures_total` | `400`、`403`、`404`、`408`、`480`、`486`、`488`、`500`、`503`、`504` |

チャレンジを運ぶ 401 は、登録の失敗に数えません。TS 32.409 4.1.1.1.3 は「登録の失敗を示す応答」を数えるとしていて、401 のあとに認証付きの REGISTER が続くのは、失敗ではなく手順の途中だからです。これは本文からの解釈です。

#### 指標の一覧

「役割」の列は、その指標を数える役割です。

| 名前 | 種類 | ラベル | 役割 | TS 32.409 V19.0.0 の測定 |
| --- | --- | --- | --- | --- |
| `ims_sip_requests_total` | counter | `role`、`direction`、`method` | SIP を扱うすべて | なし（独自） |
| `ims_sip_responses_total` | counter | `role`、`direction`、`method`、`status_class` | SIP を扱うすべて | なし（独自） |
| `ims_diameter_requests_total` | counter | `role`、`direction`、`command` | Diameter を扱うすべて | `UR.AttUAR`（4.1.1.5.1）、`UR.AttSAR`（4.1.1.6.1）、`LIQ.AttLIR`（4.1.3.1）、`MA.AttMAR`（4.1.6.1）、`UP.AttPPR`（4.1.7.1）、`UR.AttDeRegHss`（4.1.1.3.2.1、RTR）、HSS の `UR.Request`（4.2.12.1） |
| `ims_diameter_answers_total` | counter | `role`、`direction`、`command`、`result` | Diameter を扱うすべて | `UR.SuccUAA.ResultCode` と `UR.FailUAA.Cause`（4.1.1.5.2、4.1.1.5.3）、`UR.SuccSAA` と `UR.FailSAA.Cause`（4.1.1.6.2、4.1.1.6.3）、`LIQ.SuccLIA` と `LIQ.FailLIA.Cause`（4.1.3.2、4.1.3.3）、`MA.SuccMAA` と `MA.FailMAA.Cause`（4.1.6.2、4.1.6.3）、HSS の `UR.Answer`（4.2.12.2） |
| `ims_diameter_peer_up` | gauge | `peer` | Diameter を扱うすべて | なし（独自）。接続が開いていれば 1 |
| `ims_registration_attempts_total` | counter | `role`、`type` | P-CSCF、S-CSCF | `UR.AttInitReg`（4.1.1.1.1）、`UR.AttReReg`（4.1.1.2.1）、`UR.AttDeRegUe`（4.1.1.3.1.1） |
| `ims_registration_successes_total` | counter | `role`、`type` | P-CSCF、S-CSCF | `UR.SuccInitReg`（4.1.1.1.2）、`UR.SuccReReg`（4.1.1.2.2）、`UR.SuccDeRegUe`（4.1.1.3.1.2） |
| `ims_registration_failures_total` | counter | `role`、`type`、`status` | P-CSCF、S-CSCF | `UR.FailInitReg.Cause`（4.1.1.1.3）、`UR.FailReReg.Cause`（4.1.1.2.3）、`UR.FailDeRegUe.Cause`（4.1.1.3.1.3） |
| `ims_registration_setup_seconds` | histogram | `role` | P-CSCF | `UR.MeanInitRegSetupTime`（4.1.1.1.4）。初期登録だけを数える |
| `ims_third_party_registration_attempts_total` | counter | `role` | S-CSCF | `UR.Att3rdPartyReg`（4.1.1.4.1） |
| `ims_third_party_registration_failures_total` | counter | `role`、`status` | S-CSCF | `UR.Fail3rdPartyReg.Cause`（4.1.1.4.3） |
| `ims_session_attempts_total` | counter | `role`、`direction` | P-CSCF、I-CSCF、S-CSCF | `SC.AttSession`（4.1.2.1）、`SC.AttSessionOrig`（4.1.2.9）、`SC.AttSessionTerm`（4.1.2.11） |
| `ims_session_successes_total` | counter | `role`、`direction`、`response` | P-CSCF、I-CSCF、S-CSCF | `SC.SuccSession.type`（4.1.2.2）、`SC.SuccSessionOrig.type`（4.1.2.10）、`SC.SuccSessionTerm.type`（4.1.2.12） |
| `ims_session_answers_total` | counter | `role`、`direction` | P-CSCF、S-CSCF | `SC.AnsSession`（4.1.2.3） |
| `ims_session_failures_total` | counter | `role`、`direction`、`status` | P-CSCF、S-CSCF | `SC.FailSession.Cause`（4.1.2.4） |
| `ims_session_setup_seconds` | histogram | `role` | P-CSCF、S-CSCF | `SC.SessionEstabTimeMean.MediaName`（4.1.2.6）。標準はメディアの種類ごとに分けるが、この案では分けない |
| `ims_answered_sessions` | gauge | `role` | P-CSCF、S-CSCF | `SC.NbrSimulAnsSessionMax`（4.1.2.5）と `SC.NbrSimulAnsSessionMean`（4.1.2.7）。Prometheus で `max_over_time` と `avg_over_time` を使う |
| `ims_sessions_dropped_total` | counter | `role` | P-CSCF、I-CSCF、S-CSCF | `SC.DroppedSession`（4.1.2.8） |
| `ims_session_cancels_total` | counter | `role`、`phase` | P-CSCF、S-CSCF | `SC.RelBeforeRing`（4.1.2.19）、`SC.RelAfterRing`（4.1.2.20） |
| `ims_session_answered_seconds_total` | counter | `role`、`direction` | P-CSCF、S-CSCF | `SC.AnsTrafOrig`（4.1.2.13）、`SC.AnsTrafTerm`（4.1.2.14）。BYE のときに通話の秒数を足す |
| `ims_subscription_attempts_total` | counter | `role` | S-CSCF | `SUB.AttSubscribe`（4.1.8.1） |
| `ims_subscription_failures_total` | counter | `role`、`status` | S-CSCF | `SUB.FailedSubscribe.Cause`（4.1.8.3） |
| `ims_notify_sent_total` | counter | `role` | S-CSCF | `NOTIF.EmittedNotify`（4.1.9.1） |
| `ims_notify_failures_total` | counter | `role`、`status` | S-CSCF | `NOTIF.FailedNotify.Cause`（4.1.9.3） |
| `ims_message_attempts_total` | counter | `role` | S-CSCF | `SC.AttImMsg`（4.1.10.1） |
| `ims_message_successes_total` | counter | `role` | S-CSCF | `SC.SuccImMsg`（4.1.10.2） |
| `ims_message_failures_total` | counter | `role`、`status` | S-CSCF | `SC.FailImMsg.Cause`（4.1.10.3） |
| `ims_hss_subscriptions` | gauge | なし | HSS | `DBU.NbrImsSubscription`（4.2.1.1.1） |
| `ims_hss_private_identities` | gauge | なし | HSS | `DBU.NbrPriUserId`（4.2.1.1.2） |
| `ims_hss_public_identities` | gauge | `format` | HSS | `DBU.NbrSipPubUserId`（4.2.1.1.3）、`DBU.NbrTelPubUserId`（4.2.1.1.4） |
| `ims_hss_registered_public_identities` | gauge | なし | HSS | `DBU.NbrRegPubUserId.RoamingDomain`（4.2.1.2.1）。標準は訪問先の網ごとに分けるが、この案では分けない |
| `ims_hss_registered_private_identities` | gauge | なし | HSS | `DBU.NbrRegPriUsrId`（4.2.1.2.3） |
| `ims_rtp_packets_total` | counter | `role`、`direction` | 中継ノード | `RTP.IncPacket`（4.7.1.1）、`RTP.OutgPacket`（4.7.1.2）。標準では MRFP の測定 |
| `ims_rtp_bytes_total` | counter | `role`、`direction` | 中継ノード | `RTP.IncOctet`（4.7.1.3）、`RTP.OutgOctet`（4.7.1.4）。Prometheus の基本の単位に合わせて bytes にする |
| `ims_rtp_lost_packets_total` | counter | `role` | 中継ノード | `RTP.LostIncPacket`（4.7.1.5） |
| `ims_media_contexts` | gauge | `role` | 中継ノード | なし（独自） |
| `ims_hep_dropped_total` | counter | なし | HEP を送るすべて | なし（独自） |
| `ims_alarms` | gauge | `severity` | すべて | なし（独自。6 章の警報の一覧の件数） |

ノードが動いているかは、Prometheus が対象ごとに書く `up` で見ます。ノードが自分で出す必要はありません。

5 章の最初の表から変えた所は次のとおりです。

- `ims_sip_messages_total` を、要求と応答の 2 つに分けました。要求には応答コードが無く、`status_class` に入れる値が無いためです。
- `ims_diameter_requests_total` から `application` と `result` を外し、結果は `ims_diameter_answers_total` に移しました。`command` でアプリケーションは決まります。`ims_diameter_answers_total` に `application` も持たせると、役割が 1 つのノードでも系列の数の上限（10000）を超えます。
- `ims_sessions` を `ims_answered_sessions` にしました。TS 32.409 4.1.2.5 と 4.1.2.7 が数えるのは、応答済みのセッションだけです。
- `ims_rtp_octets_total` を `ims_rtp_bytes_total` にしました。Prometheus の名前の決まりは、基本の単位として bytes を使うことを勧めています。
- `ims_authentication_attempts_total` は作らず、`ims_diameter_requests_total{command="MAR"}` で数えます。TS 32.409 4.1.6 が数えるのは、S-CSCF が送る MAR と、受ける MAA です。
- `ims_registration_setup_seconds` から `type` を外しました。TS 32.409 4.1.1.1.4 が測るのは、P-CSCF での初期登録だけです。

#### 気を付けること

- TS 32.409 は、登録の試行、成功、失敗の 3 つの測定に「2 out of 3 approach」を当てています（4.1.1.1、4.1.1.2、4.1.1.3.1 の先頭）。3 つのうち 2 つがあれば残りが分かる、という意味だと読めますが、定義の箇所は確かめていません。この案では 3 つとも出します。
- I-CSCF の `UR.FailUAA.Cause` には、必ず持つ原因として 5002（DIAMETER_ERROR_IDENTITIES_DONT_MATCH）と 5004（DIAMETER_ERROR_ROAMING_NOT_ALLOWED）が挙がっています（4.1.1.5.3）。どちらも `result` の集合に入れています。
- HSS の gauge（`ims_hss_…`）を、加入者を数え上げて作ってはいけません（全加入者を走査しないため）。TS 32.409 4.2.1.1 も、加入者を足したときに 1 増やし、消したときに 1 減らす形で決めています。ただし、HSS を複数台にして保存先を共有すると、1 台が自分の操作だけで増減させた数は全体の数になりません。全体の数は保存先が持つべきもので、冗長構成の保存先を決めるときに一緒に決めます。それまでは、1 台の HSS の `MemoryStore` の件数を出します。
- 同じように、`ims_answered_sessions` はそのノードの中のセッションの数です。網全体の数は、Prometheus の `sum` で足します。ノードどうしで数を合わせる処理は作りません。
- 系列の数の目安。prometheus.io の「Instrumentation」は、1 つの指標の系列の数を 10 未満に保つよう勧めています。100 を超えるか超えそうなら、次元を減らすか、分析を監視の外に移すことを考えるように、とあります。役割が 1 つのノードで、宣言した集合から計算した系列の数の上限は、`ims_sip_responses_total` が 630（2 × 3 × 15 × 7）、`ims_diameter_answers_total` が 1728（2 × 3 × 18 × 16）、`ims_diameter_requests_total` が 108、`ims_session_failures_total` が 102 です。`other` を含みます。系列は数えたときに初めて現れるので、実際に出る数はずっと少なくなります。たとえば `result` は、コマンドごとに返りうる値が数個です。それでも目安の 100 を超えることはありえます。OAM の e2e（12 章）で、登録と通話のあとに出た系列の数を数えます。100 を超えたら、`ims_sip_responses_total` から `direction` を外すなどの案を出します。`internal/metrics` の上限（10000）は、誤りを止めるためのもので、この目安とは別のものです。
- 数える前の系列。「Instrumentation」は、あると分かっている系列は 0 で出しておくよう勧めています。系列が無いと、比を計算する式が値を返さないためです。`internal/metrics` は、公式のクライアントと同じく、ラベルの無い指標だけを最初から 0 で出します。ラベルの付いた系列は、数えたときに現れます。役割は、起動のときに分かっている組（たとえば `type` の 3 つの値）を `Add(0, …)` で作っておけます。どの組を作るかは、役割ごとに決めます。
- histogram の種類。「Histograms and summaries」は、使えるなら native histogram を使うよう勧めています。ただし、今の native histogram は protobuf の形式でしか出せず、テキストの形式での対応は OpenMetrics v2 で進んでいる、とあります。`internal/metrics` はテキストの形式だけを出すので、classic histogram になります。Prometheus の側で、classic histogram を NHCB（決まった境界の native histogram）として取り込む設定もできる、とあります。
- Prometheus がノードに付けるラベルは `node` にします。ノードが自分の指標に `role` を付けるので、同じ名前を対象のラベルに使うと、ノードの側のラベルが `exported_role` に名前を変えられてしまうためです（`deploy/observability/prometheus/prometheus.yml`）。

#### `deploy/observability/` に置いたもの

- `compose.observability.yaml`。`docker compose -f compose.yaml -f deploy/observability/compose.observability.yaml up -d` で、Prometheus（`prom/prometheus:v3.15.1`）と Grafana（`grafana/grafana:13.3.0`）を足します。`compose.yaml` は変えません。既定では起動しない（13 章の「採った選択」の 6）という方針を、profile ではなく別のファイルで満たしています。
- `prometheus/prometheus.yml`。ノードは `<サービス名>:8085` の `/metrics` で指標を出すと仮に決めています。ポートは仮の値で、13 章の未決の論点の 3 で決める番号です。決まるまで、どのノードも down に見えます。
- Grafana のデータソースとダッシュボード 1 枚の案。ノードの稼働、登録（TS 32.454 5.1.1 の成功率を含む）、セッションと原因ごとの失敗、Diameter の結果を並べます。
- ライセンスは、Prometheus が Apache-2.0、Grafana が AGPL-3.0-only（一部のディレクトリは Apache-2.0）です。それぞれのリポジトリのタグにある `LICENSE` と、Grafana の `LICENSING.md` で確かめました。Grafana の扱いは 13 章の「採った選択」の 8 に書きました。
- 版。Docker Hub に `prom/prometheus:v3.15.1`（2026 年 10 月 21 日に push）と `grafana/grafana:13.3.0`（2026 年 10 月 27 日に push）があることを確かめました。prometheus.io の「Download」のページでは、今の最新は 3.16.0、LTS は 3.13.5 です。固定した 3.15.1 は誤りではないので、変えていません。LTS に合わせるかは、作るときに決めます。
- Grafana のイメージの版の種類。grafana.com の「Run Grafana Docker image」は、`grafana/grafana` を Grafana Open Source、`grafana/grafana-enterprise` を Grafana Enterprise としています。`grafana/grafana-oss` は 12.4.0 から更新されず、`grafana/grafana` と同じイメージだった、とあります。Docker Hub の `grafana/grafana-oss` には 13.3.0 がありません。したがって、置いたファイルの `grafana/grafana:13.3.0` は、オープンソース（AGPL）の版です。名前は直していません。同じページは、Enterprise を推奨の既定の版としています。
- 元にしたイメージ。Docker Hub の層の一覧では、Prometheus のイメージは BusyBox 1.38.0（uClibc、Buildroot で作ったもの）、Grafana のイメージ（amd64）は Alpine Linux 3.24.3 に glibc を足したものです。「Configure a Grafana Docker image」にも、既定のイメージは Alpine を元にすると書いてあります。

## 6. 警報

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 警報の形 | TS 28.111 6.3 の属性を使う。`alarmId`、`objectInstance`、`alarmType`、`probableCause`、`specificProblem`、`perceivedSeverity`、`alarmRaisedTime`。`objectInstance` は「ノードの名前と役割」を DN に似せた形で書く | 独自の形にする |
| 同じ警報の決め方 | 対象、種類、原因、specific problem の 4 つが同じなら同じ警報（6.4）。条件が無くなったら cleared にして一覧から外す | 起きるたびに新しい記録を作る。同じ障害で記録があふれる |
| ノードが見つける警報 | Diameter の相手との接続が切れた。中継ノードに届かない。DNS が引けない。HEP の送り先に届かず取りこぼしている。`Store` に届かない（冗長構成のあと）。どれも `COMMUNICATIONS_ALARM` | なし |
| ノードの中の置き場所 | 新しいパッケージ `internal/alarm` に、警報の一覧（上げる、下げる、読む）を置く。一覧はノードのプロセスの中に持つ。今の状態を表すもので、再起動したら条件を調べ直せば作り直せるため | 外部の保存先に置く |
| OAM への届け方 | OAM がノードの `GET /maintenance/v1/alarms` を読む。画面を開いているあいだは、数秒ごとに読み直す | ノードが OAM に通知を送る（TS 28.111 の `notifyNewAlarm` に当たる）。ノードが OAM の場所を知る必要がある |
| 指標のしきい値の警報 | Prometheus の alerting rule として書く（登録の成功率が下がった、切断率が上がった、など）。`QUALITY_OF_SERVICE_ALARM` に当たる。profile を起動しているとき、OAM の警報のページに Prometheus の警報も並べる | OAM が自分でしきい値を見る。時系列を持つことになる |
| 作らないもの | 確認（ack）、コメント、履歴、相関 | なし |

しきい値の警報を Prometheus に任せると、profile を起動していないときは、ノードが見つける警報だけになります。「警報を OAM で見られる」という必須の要件をそれで満たすとみなすかは、13 章の未決の論点に挙げます。

### 作ったもの

上の表の最初の案のうち、警報の形、同じ警報の決め方、ノードの中の一覧、OAM への届け方を作りました。画面にも警報の部分を足しました。決めたことは次のとおりです。

- 置き場所は、最初の案の新しいパッケージ `internal/alarm` ではなく、`internal/maintenance` の中にしました。標準でも、警報の一覧（AlarmList）は ManagedElement か SubNetwork に含まれます（TS 28.111 7.3.2.1）。ノードの保守用の API と同じ持ち主です。
- 確認（ack）は、表では「作らないもの」に入れていましたが、作りました。確認を作らないと、cleared の警報はすぐに一覧から消えます。作ると、運用者が確認するまで残ります（6.14）。運用者が「何が起きて、もう戻ったか」を見られるのは後者です。足すものは、記録の 3 つの属性と経路 1 つで済みました。
- ノードが見つける警報（Diameter、中継ノード、DNS、HEP）は、どの役割も上げていません。役割ごとの案を、この節の最後に書きました。

#### 保守用の API

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `GET` | `/maintenance/v1/alarms` | なし。問い合わせで `cleared=true` か `cleared=false`、`min_severity=CRITICAL`（`MAJOR`、`MINOR`、`WARNING`）を付けられる | `AlarmList`。`node`、`instance`、`num_of_alarm_records`、`last_modification`、`dropped_records`、`alarm_records` |
| `PUT` | `/maintenance/v1/alarms/{id}/ack-state` | `AckStateRequest`。`ack_state`（`ACKNOWLEDGED` か `UNACKNOWLEDGED`）、`operator` | `AlarmRecord` |

状態コードは、400（知らない問い合わせの名前、範囲の外の値、本文の知らない欄）、404（一覧に無い `id`）、405（`Allow` を付ける）です。問い合わせの名前を間違えたときに、黙って全件を返さないように、知らない名前は 400 にしました。

記録（`AlarmRecord`）の欄と、TS 28.111 7.4.1 の属性の対応です。

| 欄 | 属性 | 中身 |
| --- | --- | --- |
| `alarm_id` | alarmId | 64 ビットの乱数を 16 進で書いたもの。ノードをまたいでも、時がたっても重ならないようにするため（6.4 の推奨。TS 28.111 の本文が勧める作り方） |
| `object_instance` | objectInstance | `ManagedElement=<ノード>,ManagedFunction=<役割>,Peer=<相手>`。DN に似せた形 |
| `role`、`peer` | なし | `object_instance` の部分。画面に出すため。ノード全体の警報には `role` が無い |
| `alarm_raised_time` | alarmRaisedTime | 上げた時刻 |
| `alarm_changed_time` | alarmChangedTime | 役割が重さか文字を変えた時刻。運用者の確認では変わらない（7.4.1） |
| `alarm_cleared_time` | alarmClearedTime | cleared にした時刻 |
| `alarm_type` | alarmType | `COMMUNICATIONS_ALARM`、`QUALITY_OF_SERVICE_ALARM`、`PROCESSING_ERROR_ALARM`、`EQUIPMENT_ALARM`、`ENVIRONMENTAL_ALARM`、`OTHER` |
| `probable_cause` | probableCause | Annex B の原因を、OpenAPI の定義と同じ綴り（大文字と `_`）で書く。Annex B に無い原因も、同じ綴りなら使える（Annex B の前書き） |
| `specific_problem` | specificProblem | 原因を細かくした短い句。`Diameter peer down` など |
| `perceived_severity` | perceivedSeverity | `CRITICAL`、`MAJOR`、`MINOR`、`WARNING`、`INDETERMINATE`、`CLEARED` |
| `additional_text` | additionalText | 運用者向けの自由な文。512 文字まで |
| `ack_state`、`ack_time`、`ack_user_id` | ackState、ackTime、ackUserId | 確認の状態、変えた時刻、変えた人（要求の `operator`） |

#### 役割が使う Go の API

役割は、障害を見つけたら `RaiseAlarm` を呼び、条件が無くなったら `ClearAlarm` を呼びます。運用者は読むことと確認することしかできません。どの警報も、上げた役割が下げる ADAC の警報です（6.8）。

```go
var hssDown = maintenance.AlarmIdentity{
	Role:            "scscf",
	Peer:            "hss.ims.mnc001.mcc001.3gppnetwork.org",
	AlarmType:       maintenance.CommunicationsAlarm,
	ProbableCause:   maintenance.CommunicationsSubsystemFailure,
	SpecificProblem: "Diameter peer down",
}

// The connection went down.
err := node.RaiseAlarm(maintenance.Alarm{AlarmIdentity: hssDown, PerceivedSeverity: maintenance.Major, AdditionalText: "no answer to 3 DWR"})
// A CEA came back.
err = node.ClearAlarm(hssDown)
```

パッケージの `ExampleNode_RaiseAlarm` が、同じ形を動かして見せます。決まりは次のとおりです。

- `AlarmIdentity` の 5 つの欄（役割、相手、種類、原因、specific problem）が同じなら、同じ警報です。役割と相手は、ノードと合わせて objectInstance になるので、6.4 の 4 つの属性と同じ決め方です。同じ警報を上げると、記録を更新します（6.14）。重さも文字も同じなら何も変えません。役割は、失敗するたびに同じ警報を上げて構いません。
- cleared にした警報を同じ識別でまた上げると、同じ記録が戻ります。`alarm_id` と上げた時刻はそのままです。
- 新しい記録は `UNACKNOWLEDGED` で始まります（6.7）。確認した記録が cleared になると、すぐに一覧から消えます。cleared の記録は、確認されると消えます（7.3.1.1）。
- 一覧には上限（`Config.MaxAlarmRecords`、既定は 256 件）があります。いっぱいのときは、cleared の記録のうち一番古いものを、cleared が無ければ一番古いものを捨てます（7.3.2.1）。捨てた数は `dropped_records` に出ます。条件が入れ替わり続けても、識別が同じなら記録は 1 件のままです。上限に届くのは、新しい識別を作り続ける障害のときだけです。
- 確認されない cleared の記録も、`Config.ClearedAlarmRetention`（既定は 1 時間）がたつと消えます（下の SIMPLIFIED）。
- 警報は加入者を名乗りません。識別には加入者の欄がありません。そのうえで `RaiseAlarm` は、相手、原因、specific problem、追加の文に、SIP や tel の URI、`user@realm`、`+` で始まる番号、6 桁以上の数字の並び（IMSI や MSISDN の長さ）があると断ります。相手はホスト名か IP アドレス（ポートは付けても付けなくてもよい）に限ります。数字だけの 4 文字以上のラベルも断ります。一覧が大きくなるのは、ノードのつながりが増えたときだけで、利用者が増えても大きくなりません。
- 追加の文に秘密を入れさせません。設定の伏せ字と同じ秘密の名前（`password`、`k`、`opc` など）のあとに値が続く文（`password=…`、`k: …`）と、32 文字以上の 16 進の並び（K や OPc の長さ）を断ります。断ったときのエラーには、秘密の値を書きません。
- 一覧のロックは、そのノードの警報だけを守ります。期限の過ぎた記録を探す走査は、上限の 256 件までです。加入者を走査することはありません。
- 一覧はプロセスの中にあります。再起動すると空から始まり、役割がまだ見つけている障害をまた上げます。警報は今の状態を表すものなので、外に置かなくても作り直せます（上の表の最初の案のとおり）。
- 通知（`notifyNewAlarm` など）は送りません。OAM がノードの一覧を読みます（上の表の最初の案のとおり）。

#### OAM の API

| 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- |
| `GET` | `/oam/v1/alarms` | なし。問い合わせは保守用の API と同じ | `Alarms`。`succeeded`、`failed`、`nodes`（ノードごとの `AlarmListInfo` か失敗の理由）、`alarms`（`node` を付けた記録） |
| `PUT` | `/oam/v1/nodes/{node}/alarms/{id}/ack-state` | `maintenance.AckStateRequest` | `maintenance.AlarmRecord` の `FanOut`。ノードに無い `id` は、そのノードの 404 が入る |

- 答えの形は `FanOut` に合わせました。インベントリのすべてのノードに 1 件ずつ入り、失敗したノードを省きません。記録はすべてのノードの分をまとめ、重い順、同じ重さなら新しい順に並べます。
- `AlarmListInfo` は、ノードの一覧の記録の数、絞ったあとの数（`listed`）、最後に変わった時刻、捨てた数です。
- 絞り込みは、OAM が先に確かめてから、そのままノードに渡します。間違った問い合わせは、ノードに聞く前に 1 つの 400 になります。
- 答えないノードの警報を、OAM は作りません。失敗したノードとして出すだけです。TS 28.111 では、管理する側が装置とのつながりを失ったとき、その部分の警報の一覧を「信頼できない」と示します（6.11）。新しい警報は作りません。警報を見つけるのは、警報の対象の装置です（6.14）。ノードが止まったときは、そのノードと話す役割が、つながりの警報を上げます。HSS が止まれば、S-CSCF と I-CSCF が Diameter の警報を上げます（12 章の e2e の項目）。画面は、答えなかったノードの警報を「無い」ではなく「分からない」と書きます。
- ノードの答えの記録も、OAM が確かめ直します。秘密や加入者に見える欄があれば、そのノードを失敗にし、記録は渡しません。設定の答えと同じ扱いです。

#### 画面

警報の部分を、Nodes と Operations のあいだに足しました（4 章の「作った画面」の表）。

- 列は、重さ、ノード、役割、相手、種類、原因、specific problem、上げた時刻、cleared の時刻、確認、追加の文です。重さには objectInstance を `title` で付けました。役割の無い警報は「(node)」と出します。
- 絞り込みは「All in the lists」「Not cleared」「Cleared only」と、重さの下限です。下限を選ぶと、順の無い `INDETERMINATE` も残します。
- 5 秒ごとに読み直します。ノードの状態と同じ間隔です。
- 答えなかったノードは、名前と理由を出します。一覧がいっぱいで記録を捨てたノードも出します。
- 確認と確認の取り消しは、ほかの操作と同じ確認のダイアログを通します。ダイアログには、ノード、警報、objectInstance、確認したあとどうなるか（cleared なら一覧から消える、そうでなければ残る）を書きます。

#### SIMPLIFIED

- `internal/maintenance/alarm.go` の `AlarmType`。TS 28.111 V20.0.0 7.3.1.3、7.4.1。セキュリティの警報の種類を省きました。セキュリティの警報は serviceUser を持つ必要があり、それは加入者になるためです。
- `internal/maintenance/alarm.go` の `AlarmRecord`。TS 28.111 V20.0.0 7.3.1.2。必須の notificationId を省きました。通知を送らないためです。任意の属性は、alarmChangedTime、specificProblem、additionalText と確認の 3 つだけを持ちます。ackSystemId、ADMC の属性（clearUserId、clearSystemId、clearingType）、コメント、相関、しきい値などは持ちません。
- `internal/maintenance/alarm.go` の `AlarmList`。TS 28.111 V20.0.0 7.3.2.2、6.13。一覧の必須の operationalState を省きました。ノードが答える限り一覧は動くので、いつも ENABLED になるためです。任意の administrativeState と unreliableAlarmScope（6.11）も省きました。一覧が警報を捨てたことは `dropped_records` で示します。
- `internal/maintenance/alarm.go` の `ObjectInstance`。TS 28.111 V20.0.0 6.3、7.4.1。objectInstance は DN ですが、DN に似せた文字列にしました。ノードは NRM の管理対象ではないためです。クラスは TS 28.622 の一般の ManagedElement と ManagedFunction を使い、TS 28.705 の IMS のクラスは使っていません。すべての役割にクラスがあるわけではないためです。`Peer` はどの NRM のクラスでもありません。値の escape もしていません。
- `internal/maintenance/alarmlist.go` の `expire`。TS 28.111 V20.0.0 6.14。確認がある場合、cleared の警報は確認されるまで一覧に残ります。ここでは、保持の時間がたっても消します。誰も見ていない一覧が、終わった障害を持ち続けないようにするためです。

#### 確かめたこと

- Go の試験。上げる、同じ警報の更新、cleared、確認で消えること、cleared のあとの再度の上げ、保持の時間、上限と捨てる順、1000 回の入れ替わりで記録が 1 件のままのこと、絞り込みと並べ方、JSON の名前、確認のエラー、加入者や秘密に見える値を断ること、同時の利用（`-race`）。OAM では、すべてのノードの統合と失敗したノード、絞り込みの受け渡しと間違った問い合わせ、確認の受け渡し、秘密を含む答えのノードを失敗にすること。
- 画面の Vitest の試験。本物の形の答え（一時的なプログラムで本物の OAM から取ったもの）の読み方、問い合わせの組み立て、確認の要求、重さの数え方、答えなかったノードと捨てたノード、確認の説明。
- ブラウザでの確認。Playwright の Chromium で、組み立てたページを 2 つの相手に対して動かしました。1 つは `web/oam/mock` の偽の API です。もう 1 つは、本物の `internal/oam` に本物の `internal/maintenance` のノード 4 台と答えないノード 1 台をつないだ、リポジトリの外の一時的なプログラムです（`go build -overlay` で組み立てました。コミットしていません）。両方で、並び順、答えなかったノードの表示、絞り込み、確認のダイアログ（焦点が Cancel にあること、Escape で何も変わらないこと）、cleared の警報が確認で消えること、cleared でない警報が確認のあとも残ること、20 秒後に下がった警報が自動の読み直しで CLEARED になることを確かめました。幅 1280 の明るい表示と、幅 375 の暗い表示で画面を撮って見ています。幅 375 で、長い理由（URL）がページを横に押し広げていたので、手順の一覧の折り返しを直しました。本物の相手では、HSS を持たない構成なので、ほかの部分の 502 と 503 がコンソールに出ます。警報の部分のエラーはありません。

#### 役割ごとの警報の案

1 章と上の表をもとに、役割が上げる警報の案です。値は TS 28.111 7.4.1 と Annex B のものです。種類は、Annex B の表がその原因に付けている種類に合わせました。どの役割もまだ上げていないので、案として残します。

| 役割 | 条件 | alarmType | probableCause | specificProblem | 重さ | 相手 | 上げる、下げる |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `icscf`、`scscf`（Cx）、`pcscf`（Rx）、`ipsmgw`、`mmtel`（Sh） | Diameter の相手との接続が切れた。DWR に答えない | `COMMUNICATIONS_ALARM` | `COMMUNICATIONS_SUBSYSTEM_FAILURE`（B.2） | `Diameter peer down` | `MAJOR` | 相手の Origin-Host | 切れたときに上げる。CER と CEA がまた通ったら下げる |
| 同じ | 同じアプリケーションの相手が 1 つも残っていない。たとえば S-CSCF から HSS に届かない | `PROCESSING_ERROR_ALARM` | `UNDERLYING_RESOURCE_UNAVAILABLE`（B.2） | `no Cx peer left`（`no Rx peer left`、`no Sh peer left`） | `CRITICAL` | なし | 最後の相手が切れたときに上げる。役割の operationalState を DISABLED にするのと同じ時点。1 つ戻ったら下げる |
| `pcscf` | 中継ノード（`agw`）の制御の API に届かない | `COMMUNICATIONS_ALARM` | `CONNECTION_ESTABLISHMENT_ERROR`（B.3） | `media relay unreachable` | `MAJOR`。中継ノードが 1 台だけなら `CRITICAL` | 中継ノードの `host:port` | 要求が接続の失敗か期限切れで終わったときに上げる。次に成功したら下げる |
| `pcscf`、`icscf`、`scscf` | 次の hop の DNS の名前が引けない（`internal/sipdns`） | `COMMUNICATIONS_ALARM` | `ROUTING_FAILURE`（B.3） | `DNS name does not resolve` | `MAJOR` | 引いた名前 | 引けなかったときに上げる。引けたら下げる |
| すべて（冗長構成のあと） | 状態の保存先（`internal/kvstore`）に届かない | `PROCESSING_ERROR_ALARM` | `UNDERLYING_RESOURCE_UNAVAILABLE`（B.2） | `store unreachable` | `CRITICAL` | 保存先の `host:port` | 読み書きが接続の失敗か期限切れで終わったときに上げる。次に成功したら下げる |
| ノード全体（`role` なし） | HEP の送り先に届かず、写しを取りこぼしている（`internal/hep` の `Dropped`） | `QUALITY_OF_SERVICE_ALARM` | `REDUCED_EVENT_REPORTING`（B.3） | `HEP copies dropped` | `WARNING` | HEP の送り先 | 一定の時間（たとえば 60 秒）に取りこぼしが増えたら上げる。増えない時間が続いたら下げる。追加の文に数を書く |

気を付けることです。

- 警報は、状態が変わったときに上げ下げします。要求ごとに新しい識別を作らないでください。相手と原因が同じなら、同じ識別を使います。
- 追加の文と specific problem に、加入者の識別子、Call-ID、秘密を書かないでください。`RaiseAlarm` は断りますが、すべての書き方を見分けられるわけではありません。
- ドレイン中を `WARNING` の警報にする案は、勧めません。TS 28.111 6.7 は、警報を「運用者か機械の注意や対応が要る障害、誤り、失敗」としています。ドレインは運用者が自分で始めたもので、障害ではありません。状態はすでに `administrative_state` の `SHUTTINGDOWN` として画面に出ています。それでも警報にするなら、種類は `OTHER`、原因は Annex B に無い `SHUTTING_DOWN`（Annex B の前書きのとおり、表に無い原因も使える）、重さは `WARNING` とし、`UNLOCKED` か `LOCKED` になったら下げます。

## 7. プロビジョニング

今の API は `internal/hss/provisioning.go` にあり、1 件ずつの追加、表示、削除ができます。足すものは次のとおりです。

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 変更 | `PUT /subscriptions/{impi}` で 1 件を置き換える。登録中の利用者のプロファイルが変わるときは、S-CSCF に知らせる必要がある（Cx の PPR）。PPR は今は無いので、補足サービスの作りを確かめてから範囲を決める | 削除して追加し直す。登録が切れる |
| 一括の形式 | 1 行に 1 件の JSON（JSON Lines）。項目は今の `subscriptionRequest` と同じ。HSS は 1 行ずつ読んで処理し、1 行ずつ結果を返す | (a) JSON の配列。全体を読み終えるまで処理を始められない。(b) CSV。公開の識別子が複数あるので、列の決め方が要る |
| 一括の処理の場所 | HSS に `POST /subscriptions/batch` を足す。1 件ごとに、その利用者のロックだけを取る。全体のロックは取らない | OAM が 1 件ずつの API を繰り返し呼ぶ。HSS は変えずに済むが、件数の分だけ往復する |
| 途中の失敗 | 1 件の失敗で止めない。行の番号と理由を結果に返す。同じファイルをもう一度送っても同じ結果になるように、「あれば置き換える」の指定を持つ | 最初の失敗で止め、全部を取り消す。全体をまとめて不可分に書く操作は、今の `Store` に無い |
| 一覧 | ページに分ける。`GET /subscriptions?after={impi}&limit={n}` の形にし、`Store.List` に「この識別子の次から」を足す（規模を広げる妨げとしての一覧の直し方と同じ。[state-redundancy.md](../design/state-redundancy.md)） | なし |
| 画面 | OAM の加入者のページに、ファイルを選んで送る欄と、連番で作る欄を置く。連番は、試験用の IMSI の範囲と、555-0100 から 555-0199 の番号を使う | なし |
| 複数台の HSS | 一括の要求は 1 台の HSS が受け、`Store` に書く。HSS が加入者ごとに分かれるとき（冗長構成の SLF）は、OAM が行を振り分ける | なし |

HSS のプロビジョニングの API は、3GPP の標準にはありません。今のコードのコメントにも、製品の HSS はそれぞれのプロビジョニングの口を持つと書いてあります。TS 32.409 には、HSS のプロビジョニングに関わる測定があります（4.2.1.1）。

## 8. バックアップとリストア

### 何を退避するか

状態は 2 種類に分けられます。

| 種類 | 中身 | 置き場所 | 失ったとき |
| --- | --- | --- | --- |
| 持ち続けるデータ | 加入者の識別子、認証の情報（パスワード、K、OPc、AMF、SQN）、サービスのプロファイル、補足サービスの設定（Sh の透過データに置く）、試験用の利用者の印、SC が預かっている未配達の SMS | `hss.Store`、`sc.Store` | 戻さないと、利用者が使えない |
| 動いているあいだの状態 | 登録、認証の途中の情報、購読、ダイアログ、P-CSCF の IP の対応付け、中継のコンテキスト、HSS が持つ登録の状態と S-CSCF の名前 | `scscf.Store`、`pcscf.Store`、`ipsmgw.Store`、`hss.Store` の一部 | UE の再登録と、IMS の復旧手順（TS 23.380）で作り直せる |

最初の案は、退避の対象を「持ち続けるデータ」にすることです。動いているあいだの状態は、退避した時点のものを戻しても、すでに古くなっています。期限が切れた登録や、終わった呼を戻すと、網の状態と食い違います。こちらは冗長構成の外部の保存先（[state-redundancy.md](../design/state-redundancy.md)）と復旧手順に任せます。

要件は「加入者データとノードの状態を退避し」と書いています。「ノードの状態」は、持ち続けるデータだけに絞りました（13 章の「採った選択」の 2）。

### 設計の候補

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 退避の方法 | `Store` の境界を通して、論理的に書き出す。HSS の保守用の API が、加入者を 1 行に 1 件の JSON で順に返す。形式は一括のプロビジョニング（7 章）と同じにし、認証の情報と SQN を足す。先頭の行に、形式の版と書き出した時刻を書く | 保存先のスナップショットを取る。冗長構成で決める保存先に依る。速いが、中身を学習者が読めない |
| `Store` に足す操作 | 「この識別子の次から n 件」を返す操作だけを足す（7 章の一覧と同じもの）。書き出しのあいだ、全体のロックは取らない。1 件ごとには整合していて、全体としては書き出しのあいだの変更が混じりうる。そのことを手順書に書く | 全体を止めて書き出す。役割全体を止めるロックになる |
| 復元の方法 | 空の HSS に、書き出したファイルを一括で入れる。登録の状態は Not Registered で入れる | 登録の状態も戻す。S-CSCF が持つ登録と食い違う |
| SQN | 書き出した SQN に、決めた幅を足して戻す。退避のあとに使われた番号を、もう一度使わないため。足さずに戻して、UE の再同期（AUTS）で直る様子を見せる構成も、手順書の題材になる | そのまま戻す。UE が AUTN を拒み、再同期が起きる。再同期を作っていれば直る |
| 退避したファイルの置き場所 | OAM のボリューム。認証の情報が入るので、試験用の値だけを使うことを手順書に書く | ホストに書き出す |
| SC の未配達の SMS | `sc.Store` にも同じ形の書き出しと読み込みを足す。SC が独立した役割になったあとに作る | 対象にしない |
| 設定 | ノードの設定のファイルは Git で管理しているので、退避の対象にしない。動かしたまま変えた項目は、OAM の設定の履歴（10 章）にある | なし |

### 戻したあとの確かめ方

完了条件は「HSS を止めてデータを戻したあとに登録と通話が成功する」です。手順書には、次の順で確かめる方法を書きます。

1. 件数。書き出したファイルの行の数と、HSS の `ims_hss_subscriptions` を比べる。
2. 抜き取り。何人かの加入者を `GET /subscriptions/{impi}` で読み、識別子と認証方式を比べる。
3. 警報。HSS と CSCF のあいだの Diameter の警報が下がっている。
4. 登録。登録済みだった UE が、次の再登録で 200 を受ける。HSS の登録の状態が、S-CSCF の持つ登録と合う。
5. 通話。試験用の UE で発着信する。
6. 指標。登録の成功率が、障害の前の値に戻っている。

4 は、TS 32.600 4.2.1.3 の監査（管理の側と装置の側の情報を比べる）に当たります。HSS を空から戻したとき、S-CSCF には登録が残っています。S-CSCF の再登録の SAR を HSS がどう扱うか（S-CSCF の名前を持たない利用者への RE_REGISTRATION）は、今の `internal/hss/hss.go` の `serverAssignment` を読んで、試験で確かめる必要があります。

## 9. 試験用の利用者

要件は 2 つの運用を求めています。試験用の利用者だけを収容する運用と、特定の機能を試験用の利用者だけに有効にする運用です。

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 試験用の利用者の印 | HSS の加入者のデータに、試験用であることを表す項目を足す。プロビジョニングの API と一括の形式で設定できる | (a) 識別子の範囲（IMSI の先頭の桁など）で決める。設定は少ないが、1 人ずつ足したり外したりしにくい。(b) 各ノードの設定に利用者の一覧を書く。ノードの数だけ写しができ、利用者の数に比例して設定が増える |
| 試験用の利用者だけの収容 | HSS に、収容の方針（すべて、試験用だけ）を持たせる。動かしたまま変えられる項目にする。試験用だけのとき、ほかの利用者の UAR に、認可を断る結果を返す。I-CSCF は 403 を返す（`internal/icscf/icscf.go` は、否定の UAA に 403 を返している） | P-CSCF か S-CSCF で断る。印を HSS から運ぶ手段が要る |
| 機能ごとの有効化 | サービスのプロファイルで分ける。HSS が名前の付いたプロファイル（iFC の組）を複数持ち、加入者ごとにどれを使うかを決める。新しい AS は、試験用のプロファイルにだけ iFC を足す。加入者ごとに違う iFC を返すのは、標準の仕組みそのもの（TS 29.228 Annex B。今は `SIMPLIFIED` で全員同じ） | ノードの設定に「試験用の利用者だけ」の旗を機能ごとに持つ。iFC で呼ぶ AS のほかの機能にも使えるが、ノードが試験用かどうかを知る手段が要る |
| 広げ方 | プロファイルの割り当てを、一括のプロビジョニングで変える。試験用から全員へ広げる操作を、手順書の題材にする | なし |
| 規模 | 印とプロファイルの名前は、加入者のデータの 1 項目として `Store` にある。判定は、その利用者のデータを引いたときに行う。全加入者の走査は要らない | なし |

完了条件の「試験用の利用者だけに有効にした機能が、ほかの利用者には効かない」は、iFC で確かめられます。試験用のプロファイルにだけ AS の iFC を置き、試験用の利用者の呼では AS への INVITE が流れ、ほかの利用者の呼では流れないことを、pcap で見ます。

CSCF の中の機能（たとえば 5GC（VoNR）の対応で加える EVS の offer）を試験用の利用者だけに効かせる需要があるかは分かりません。使わない設定は作らない決まりなので、需要が出たときに加えます。

## 10. 設定の履歴と戻し

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 対象 | 動かしたまま変えられる項目（3 章）だけ。起動のときに読む設定のファイルは、Git が履歴を持つ | 設定のファイルも OAM が書き換える。Compose では、OAM がホストのファイルを書けない |
| 正とする所 | OAM。OAM が「あるべき設定」を世代の番号付きで持ち、ノードに送る。ノードは、今の世代の番号を状態に入れて返す | ノード。ノードごとに履歴を持つと、同じ役割のノードで食い違う |
| 記録するもの | 世代の番号、時刻、変えた項目の前の値と後の値、対象の役割、操作した人の名前（認可が無いあいだは、画面に入れた文字列） | 全体の写しだけを持つ |
| 置き場所 | OAM のボリュームのファイルに、追記だけで書く | 外部の保存先 |
| 戻し | 前の世代の内容を、新しい世代として送り直す。履歴は消さない（TS 32.600 7.1 の「1 つの操作で前の安定した構成に戻す」） | 履歴を巻き戻す。何が起きたかが残らない |
| 食い違いの検出 | OAM がノードの世代の番号を読み、あるべき番号と違うノードを画面に出す。再起動したノードには、OAM が今の世代を送り直す（TS 32.600 4.2.1.3 の監査） | なし |
| 複数台 | 設定は役割ごとに 1 つで、その役割のすべてのノードに同じ世代を送る。冗長構成の外部の保存先ができたら、ノードが保存先から読む形に替えられる | なし |

## 11. 呼の解析

今のダッシュボードは、HEP で届いたメッセージを Call-ID ごとにまとめ、ラダー図を描きます。Cx のメッセージは、原因になった SIP の要求に結びつけています（`internal/dashboard/causes.go`）。この上に足すものは次のとおりです。

| 決めること | 最初の案 | ほかの案 |
| --- | --- | --- |
| 失敗の理由 | 呼、登録、SMS のそれぞれについて、最後の応答コード、Reason ヘッダ（VoLTE の対応で付く）、応答を最初に送ったノード、結びついた Diameter の結果コードを組にして、理由とする。たとえば「403、I-CSCF、UAA の認可の拒否」 | 応答コードだけで分ける。どのノードが断ったかが分からない |
| 応答を作ったノードの見つけ方 | HEP の写しは、送った側と受けた側の両方から届く。その応答を「送った」写しがあって「受けた」写しが無いノードが、応答を作ったノード | Server ヘッダで見分ける。VoLTE の対応で付くが、同じ役割の複数台を見分けられない |
| 件数の持ち方 | 理由ごと、1 分ごとの件数を、メッセージの記録とは別に持つ。メッセージが上限で消えても、件数は残る。理由の種類は、応答コード、役割、結果コードの組なので、数は限られる | メッセージの記録から毎回数える。古い呼が消えると件数が変わる |
| 置き場所 | `internal/dashboard` に足す。観察役のままで、ノードの内部の状態は読まない（[lab-oam.md](../design/lab-oam.md)） | 新しい役割にする |
| 利用者で探す | 記録の中を、公開の識別子で探す。ダッシュボードの記録には上限があるので、網の全加入者を走査することにはならない | なし |
| ログ | ノードが最近のログをメモリに決まった件数だけ持ち、保守用の API が Call-ID で絞って返す。OAM は、呼を選んだときに、その呼が通ったノードにだけ尋ねる。通ったノードは HEP の写しのノードの名前で分かる | (a) ログを集める仕組み（Grafana Loki など）を profile に足す。道具が増える。(b) コンテナのログを OAM が読む。Docker のソケットが要り、Kubernetes では別の方法になる |
| 指標との関係 | ノードが自分で数える指標（5 章）と、観察役が数える件数を、別の物として両方持つ。2 つを比べると、HEP の取りこぼしや、数える時点の違いが分かる | 片方だけにする |
| トレースとの関係 | 標準のトレース（TS 32.421）は、ノードが記録して管理の仕組みに送る。このプロジェクトでは、HEP の写しがその役をしている。違いを手順書に書く | TS 32.423 の形式で書き出す |

## 12. 実装の分け方

冗長構成（ドレインを含む）ができていることを前提にします。ドレインの中身は冗長構成で作るものを呼びます。

### 先に決める共通の約束

並行して作る前に、中身の無い型と設定の項目だけを 1 つのコミットで決めます。

- 設定の項目（`internal/config`）。すべてのノードに `maintenance.listen`。役割 `oam` とその節（HTTP の待ち受け、役割ごとの DNS の名前、退避と履歴の置き場所）。
- `internal/maintenance` の JSON の型。状態、管理上の状態、設定の変更、警報。
- `internal/metrics` の関数の形と、指標の名前の一覧（5 章の表）。
- 警報の型（TS 28.111 の属性）。
- 加入者の書き出しの形式（1 行に 1 件の JSON の項目と、先頭の行）。

### 作るもの

| 作るもの | 触るパッケージ | 先に要るもの |
| --- | --- | --- |
| 指標の部品（counter、gauge、histogram、テキストの出力、ラベルの型、公式の解析器との突き合わせ） | `internal/metrics` | 共通の約束 |
| SIP と Diameter の共通の指標。今の観察役（`sip.Observer`、`diameter.Observer`）に、HEP の送信と並べてつなぐ | `internal/hepagent` の隣の新しいパッケージ、`cmd/ims` | 指標の部品 |
| 手順ごとの指標（登録、セッション、認証、メッセージ）を各役割で数える | `internal/pcscf`、`internal/icscf`、`internal/scscf`、`internal/hss`、`internal/ipsmgw`、`internal/agw` | 指標の部品 |
| 警報の一覧と、ノードが見つける警報（Diameter、中継ノード、DNS、HEP） | 警報のパッケージ、`internal/diameter`、`internal/hep`、`cmd/ims` | 共通の約束 |
| 保守用の API（状態、ログの詳しさ、設定の参照と変更、最近のログ、警報、指標の出口） | `internal/maintenance`、`cmd/ims` | 指標の部品、警報の型 |
| 管理上の状態と、冗長構成のドレインをつなぐ | `internal/maintenance`、ドレインを持つ各役割 | 保守用の API |
| HSS のプロビジョニングの拡張（変更、ページに分けた一覧、一括、試験用の印、プロファイル） | `internal/hss`、`internal/config` | 共通の約束 |
| HSS の書き出しと読み込み（退避と復元） | `internal/hss` | HSS のプロビジョニングの拡張 |
| 収容の方針（試験用だけ）と、加入者ごとのプロファイルの iFC | `internal/hss` | HSS のプロビジョニングの拡張 |
| OAM のサーバー（ノードの発見、まとめて操作、ダッシュボードの取り込み） | `internal/oam`、`internal/dashboard`、`cmd/ims`、`internal/config` | 保守用の API |
| 設定の履歴と戻し、食い違いの検出 | `internal/oam` | OAM のサーバー |
| 退避と復元の進め方（ファイルの置き場所、確かめの手順の自動化） | `internal/oam` | OAM のサーバー、HSS の書き出しと読み込み |
| 呼の解析（理由、件数、応答を作ったノード） | `internal/dashboard` | なし |
| 画面（ノード、警報、指標、加入者、設定、解析のページ） | `web/dashboard` | OAM の API の型、呼の解析 |
| Prometheus と Grafana の profile、画面の定義、alerting rule | `compose.yaml`、`deploy` | 共通の指標、手順ごとの指標 |
| OAM の e2e のスクリプト、手順書「OAM で運用する」の英語版と日本語版、用語集 | `scripts`、`docs` | すべて |

`internal/hss` を触る 3 つ（プロビジョニングの拡張、書き出しと読み込み、収容の方針）と、`cmd/ims` を触る 4 つ（共通の指標、警報、保守用の API、OAM のサーバー）は、それぞれ表の順に 1 つずつ進めます。手順ごとの指標は役割ごとに分けて並行できます。

### 順番

1. 共通の約束。呼の解析は、これを待たずに始められます。
2. 並行して、指標の部品、警報、HSS のプロビジョニングの拡張。
3. 並行して、共通の指標、手順ごとの指標、保守用の API、HSS の書き出しと読み込み、収容の方針。
4. 並行して、ドレインとの接続、OAM のサーバー、Prometheus と Grafana の構成。
5. 並行して、設定の履歴と戻し、退避と復元の進め方、画面。
6. e2e と手順書。

### e2e と手順書

OAM の e2e のスクリプトで確かめることです。

- OAM から 100 人の加入者を一括で登録し、結果がすべて成功である。
- すべてのノードの状態が OAM から読め、`administrativeState` が `UNLOCKED` である。
- 登録と通話のあと、`/metrics` の登録とセッションの数が増えている。出力が `promtool check metrics` を通る。
- 1000 人分の違う識別子で登録を試みたあと、`/metrics` の系列の数が増えていない。
- HSS を書き出し、HSS のコンテナを作り直し、読み込んだあとで、登録と通話が成功する。
- HSS を止めているあいだ、CSCF に Diameter の警報が上がり、戻すと下がる。
- 試験用のプロファイルの利用者の呼では AS への INVITE があり、ほかの利用者の呼には無い。
- 収容の方針を「試験用だけ」にすると、ほかの利用者の登録が 403 になり、戻すと 200 になる。
- 設定を変えると履歴に 1 行増え、戻すと元の値になる。
- 失敗させた呼が、解析の件数に理由付きで現れる。

手順書「OAM で運用する」には、標準の OAM（1 章）との対応、保守用の API が独自であること、認可を省いたこと、退避したファイルに認証の情報が入ること、を書きます。用語集には、OAM、FM、PM、CM、NE、EM、NM、Itf-N、MnS、NRM、MOI、警報、perceived severity、KPI、granularity period、トレース、ドレイン、プロビジョニング、バックアップ、リストア、Prometheus、Grafana を足します。

## 13. 採った選択と未決の論点

### 採った選択

1. 指標は、依存なしの自作の `internal/metrics` で出します。公式のクライアントは 8 つのモジュールが増えるためです（5 章）。
2. 「ノードの状態」の退避は、持ち続けるデータ（加入者、サービスの設定、未配達の SMS）だけにします。登録やダイアログは、冗長構成の保存先と復旧手順に任せます（8 章）。
3. 保守用の API には、認可を付けません。ホストに公開しない前提で、省いたことを `SIMPLIFIED:` に書きます（3 章）。
4. 試験用の加入者の印は、HSS の加入者のデータに置きます（9 章）。
5. 機能ごとの有効化は、サービスのプロファイル（iFC の組）で行います。CSCF の中の機能にも要るなら、別の仕組みが要ります（9 章）。
6. Prometheus と Grafana は、既定では起動しません。別のファイルで足します（5 章）。
7. ログを集める仕組みは足しません。ノードが最近のログを持ち、OAM が呼ごとに尋ねます（11 章）。
8. Grafana は、AGPL-3.0-only のオープンソースのイメージ `grafana/grafana` を、変えずにそのまま動かします。Grafana のコードはリポジトリに入れません。このプロジェクトが公開のイメージを動かすだけで、Grafana を変えたり配ったりしないためです。判断の材料として、grafana.com で確かめた事実を書きます（法律の助言ではありません）。
   - 「Licensing」のページ。Grafana は 2021-04-20 の発表で、Apache License 2.0 から AGPLv3 に移りました。最初に当たる版は Grafana 8.0 です。プラグイン、エージェント、一部のライブラリは Apache のままです。FAQ は、AGPL でない選択肢として、Grafana のコードを変えない利用者向けに Enterprise の配布物を挙げています。無料で使える、独自のライセンスのバイナリで、機能は AGPL の版と同じだとしています。AGPL の版を変えて配るか、ネットワーク越しのサービスとして出し、しかも AGPL の義務を負いたくない場合には、商用のライセンスを相談するよう案内しています。
   - 「Run Grafana Docker image」のページ。`grafana/grafana` が Open Source の版、`grafana/grafana-enterprise` が Enterprise の版です。既定の版は Enterprise だとしています。
   - 選べる形は 3 つでした。(a) `grafana/grafana`（AGPL）を変えずに動かす。(b) `grafana/grafana-enterprise`（独自のライセンス、無料）に替える。(c) Grafana を使わない。(c) の場合は、Prometheus（Apache-2.0）の画面のグラフと PromQL だけで推移を見ます。このプロジェクトは (a) を選びました。

### 未決の論点

1. 役割の名前。`dashboard` を `oam` に変えて含めるか、`dashboard` のまま広げるか。`oam` に変えるのが最初の案です。
2. 復元のときの SQN の扱い。幅を足して戻すか、そのまま戻して再同期を見せるか。
3. 保守用の待ち受けのポートの番号。8080（中継ノード、ダッシュボード、EPC のスタブ）、8081（HSS）、8090（UE）と重ならない番号にします。今は仮に 8085 としています。
4. しきい値の警報を Prometheus に任せてよいか。任せると、profile を起動していないときは、ノードが見つける警報だけになります。

## 14. ほかの部分に要る口

OAM とその画面、指標、警報は、ほかの部分に次のものを求めます。どれも、この文書を書いた時点では作っていません。

### 冗長構成、ログ、指標のために要るもの

- 冗長構成のドレインは、役割ごとの「管理上の状態を変える関数」として作ります。保守用の API が、その関数を呼ぶだけで済むようにするためです。状態の名前は TS 28.622 5.3.1 の `UNLOCKED`、`SHUTTINGDOWN`、`LOCKED` を使うと、あとで名前を変えずに済みます。
- 規模を広げる妨げとして一覧にしてある「HSS の加入者の一覧」を直すときに、`Store.List` を「この識別子の次から n 件」の形にします。一括のプロビジョニングと退避が、同じ操作を使います。
- `cmd/ims/main.go` のログの作り方を、`slog.LevelVar` を使う形にします。振る舞いを変えない変更です。
- 要件の「参照する主な仕様」に、TS 32.409、TS 32.454、TS 28.111、TS 28.622、TS 32.600 を足します。
- 保守用の待ち受け（3 章）で `GET /metrics` に `metrics.NewHandler` を載せます。ポートが決まったら、`deploy/observability/prometheus/prometheus.yml` の targets と、`deploy/observability/observability_test.go` の `metricsPort` を同じ番号に直します（今は仮に 8085）。
- 各役割で、5 章の「役割ごとの指標の案」の指標を登録して数えます。`metrics.Registry` はノードごとに 1 つ作り、役割の部品に渡します。

### `internal/oam` のために要る HSS の口

`internal/oam` は、HSS の次の口を使います。今の HSS にはどれも無いので、OAM はその操作を失敗にし、足りない操作があることを理由に書きます。1 件の操作では 502、一括のプロビジョニングでは行ごとの `failed` です。形は `internal/oam/hssclient.go` の型と、試験の偽の HSS（`internal/oam/fakehss_test.go`）にあります。

- `PUT /subscriptions/{impi}`。1 件を作るか置き換える。本文は今の `subscriptionRequest` に、`repository_data`、`test_user`、`service_profile` を足したもの。答えは `{"result": "created" | "updated" | "unchanged", "subscription": {…}}` で、作ったときは 201、ほかは 200。`unchanged` は、資格情報を含めて、持っている加入者と同じことを表す。`?dry_run=true` では何も変えずに同じ答えを返す。`aka.sqn` は新しい加入者にだけ使い、置き換えのときは持っている SQN_HE を残す（同じファイルを送り直したときに SQN_HE を戻さないため）。登録の状態と S-CSCF の名前は残す。登録中の利用者のプロファイルが変わるときの Cx の PPR は、7 章のとおり範囲を決めてから。
- `PATCH /subscriptions/{impi}`。本文は `{"test_user": true, "service_profile": "trial"}` の形で、どちらも省ける。資格情報は変えない。答えは `subscriptionResponse`。
- `GET /subscriptions/export?after={impi}&limit={n}`。`after` より後の加入者を、秘密の識別子（private identity）の順に `n` 件まで、1 行に 1 件の JSON（`application/x-ndjson`）で返す。1 件は、上の `PUT` の本文と同じ形で、資格情報、`aka.sqn`（今の SQN_HE）、`repository_data`（今の MMTelServices の文書と Ut の資格情報）を含む。`tel` は使わず、`public_identities` に入れる。`Store.List` を「この識別子の次から n 件」にすれば作れる。全体のロックは取らない（8 章）。
- `subscriptionRequest` の `repository_data`。`[{"service_indication": "…", "service_data": "<XML>"}]` を受け、`RepositoryData` にそのまま入れる。退避した Ut の資格情報は H(A1) なので、`ut_password` からは作り直せないため。
- 加入者の `test_user` と `service_profile`。`Subscription`、`Store`、`subscriptionRequest`、`subscriptionResponse` に足す。
- HSS の設定の `hss.service_profiles`。名前ごとに、機能の名前の一覧と、機能ごとの iFC を持つ。`userProfile` は、加入者の `service_profile`、試験用の利用者なら `hss.test_user_service_profile`、ほかは `hss.default_service_profile` の順にプロファイルを選び、その iFC を返す（今の `SIMPLIFIED: TS 29.228 B.2.2` を外せる）。保守用の API の設定の見え方には、プロファイルごとの機能の名前の一覧を `{"hss": {"service_profiles": {"<名前>": {"features": [...]}}}}` の形で入れる。
- 動かしたまま変えられる項目を 3 つ。`hss.admission`（`all` か `test_users`）、`hss.default_service_profile`、`hss.test_user_service_profile`。値は文字列。`test_users` のとき、試験用でない利用者の UAR を断る（9 章）。
- 役割 `oam` のつなぎ込み。`internal/config` に、ノードの一覧（名前、役割、保守用の待ち受けの URL）、HSS のプロビジョニングの URL、履歴のファイルの場所、退避の置き場所を足す。`cmd/ims` で `oam.New` を呼び、`oam.Handler` を `oam.PathPrefix` の下に載せる。履歴と退避は `oam.NewFileChangeStore` と `oam.NewFileBackupStore` で作る。Compose では、OAM に名前付きのボリュームを付け、ホストには公開しない。

### `web/oam` のために要るもの

- 役割 `oam` で、組み立てた画面を `/oam/v1/` と同じオリジンから配る。画面は API を `/oam/v1/…` の絶対の経路で呼び、ページの資源は相対の経路（Vite の `base: "./"`）で読むので、ページを置く経路はどこでも構いません（`/` でも `/oam/` でも）。形は `internal/dashboard` と同じにできます。`Dockerfile` に `web/oam` を `npm ci` と `npm run build` で組み立てる段を足し、`dist/` を Go のパッケージの `web` ディレクトリに写してから `go build` する。リポジトリには、組み立てていないことを知らせる仮のページを置き、`//go:embed` で埋め込み、`http.FileServerFS` で配る。
- CI に `web/oam` のジョブを足す。今の `web` のジョブと同じ形で、`working-directory: web/oam`、Node 26.13.1、`npm ci --no-fund --no-audit`、`npm test`、`npm run build` です。
- Docker が使えない環境でのフロントエンドの試験の手順に、`web/oam` を足す。
- 警報の API。保守用の API に `GET /maintenance/v1/alarms`（3 章の案）を足し、OAM に、すべてのノードの警報の `FanOut` を返す経路（たとえば `GET /oam/v1/alarms`）を足す。形は 6 章の最初の案（TS 28.111 6.3 の属性）です。この項目は作りました（6 章の「作ったもの」）。
- 試験用の利用者の部分と退避の部分は、HSS に 14 章の口（`PATCH`、export、設定の `service_profiles` と 3 つの項目）が入るまで、OAM の 502 を画面に出すだけになります。

### 警報のために要るもの

- 各役割で、6 章の「役割ごとの警報の案」の警報を上げ下げする。役割は、`cmd/ims` が作る `*maintenance.Node` を受け取り、`RaiseAlarm` と `ClearAlarm` を呼びます。Diameter の相手の状態は `internal/diameter` の中にあるので、接続が切れたこととつながったことを役割に知らせる口が要るかもしれません。
- これは警報を上げ下げするだけです。`cmd/ims` が保守用の待ち受けに `maintenance.Node` の `Handler` を載せれば、警報の経路も一緒に載ります。設定の項目は足していません。一覧の上限と保持の時間を設定で変えたいときは、`maintenance.Config` の `MaxAlarmRecords` と `ClearedAlarmRetention` に渡してください。
- OAM の e2e のスクリプトに、12 章の「HSS を止めているあいだ、CSCF に Diameter の警報が上がり、戻すと下がる」を入れる。`GET /oam/v1/alarms?cleared=false` で、`scscf` と `icscf` の `Diameter peer down` が現れ、HSS を戻すと `cleared=true` の側に移ることを見ます。
- 手順書「OAM で運用する」に、警報の項を足します。用語集には、12 章の一覧の「警報」と「perceived severity」に加えて、alarm list、probable cause、specific problem、ADAC、acknowledgement を足すとよいです。


## 確かめられなかったこと

- イメージに入っている元のイメージのパッケージ（BusyBox、Alpine Linux、glibc など）のライセンスは、1 つずつは確かめていません。元のイメージが何かは、Docker Hub の層の一覧で確かめました（5 章）。
- Docker のデーモンが動いていない環境だったので、Prometheus と Grafana のイメージを取ってきて動かすことはしていません。確かめたのは、`docker compose config` での構文、`promtool check config` での Prometheus の設定、`promtool check rules` でのダッシュボードの PromQL です。Prometheus が設定を読み込めるか、Grafana がデータソースを用意してダッシュボードの JSON を読めるかは、動かして確かめていません。
- TS 28.111 は、6 章、7.3.1、7.3.2、7.4.1、Annex B を読みました。`ThresholdCrossing` の使い方、通知（8 章）の中身、警報の履歴（7.3.8）は読んでいません。
- TS 28.622 の `ManagedElement` の属性の意味（`ManagedFunction` は 4.3.4 の表だけを読みました）、`ThresholdMonitor`、`PerfMetricJob`、`TraceJob` の中身は読んでいません。警報の属性の元になっている X.733 は読んでいません。X.731 は `www.itu.int` から読みました（1 章）。X.733 も、警報を作るときに読みました（「読んだ資料」の表）。
- TS 32.409 は、4.1.1.1、4.1.2.4、4.1.2.8 のほかは見出しだけです。5 章の表で条項だけを書いた行は、測定の名前と数える時点を確かめていません。測定の定義の書き方（a から i の項目）を決めている仕様は確かめていません。
- TS 32.454 は 5.1.1 と 5.2.1 だけを読みました。ほかの KPI の式は読んでいません。
- TS 32.111-1、TS 28.552、TS 28.554 は表題だけです。TS 28.545 は、テキストに直せず読めませんでした。
- TS 28.532 のうち、REST の解決策（12 章）と、性能のデータのファイルの形式（12.3.2）は読んでいません。
- 加入者の管理（Subscription Management）の 3GPP の仕様があるかは確かめていません。
- TS 32.422 の Service Level Tracing は、4.1.2.9 の先頭だけを読みました。SIP の信号でトレースの指示を運ぶ方法は読んでいません。
- HSS を空から戻したあと、S-CSCF の再登録の SAR を今の HSS がどう扱うかは、試していません。
- I-CSCF が否定の UAA に 403 を返す根拠（TS 24.229 5.3.1.3）と、登録を認可しないときの結果コード（TS 29.228 6.1.1.1 の step 5 の DIAMETER_AUTHORIZATION_REJECTED。基本のプロトコルの Result-Code で、RFC 6733 7.1.5）は、[hss-provisioning.md](../design/hss-provisioning.md) を書くときに本文で確かめました。読んだ版はその記録の「読んだ仕様」にあります。
- 自作の指標の部品が 200 行ほどで書けるという見込みは、試作の 80 行（出力だけ）からの推定です。
- `internal/oam` は、本物の HSS とはつないでいません。HSS にその口が無いためで、試験は `internal/hss` の形に合わせた偽の HSS で行いました。今の HSS にある口（`GET`、`POST`、`DELETE`）の形は、`internal/hss/provisioning.go` と照らしています。
- 一括のプロビジョニングで読みながら書く流れは、Go の HTTP のクライアントで確かめました。curl やブラウザのような、ほかのクライアントでは確かめていません。HTTP/1.1 のクライアントの中には、要求の本文を送り終えるまで答えを読まないものがあります。その場合、OAM が答えを書けずに止まり、要求の本文も読めなくなります。そのようなクライアントでは、ファイルを小さく分けて送ります。ブラウザ（Chromium）がそのようなクライアントであることも確かめました（4 章の「作った画面」）。
- 複数の OAM が 1 つの履歴のファイルを共有する形は、1 つのプロセスの中の同時の書き込みでしか確かめていません。別々のプロセスと、ネットワークのファイルシステムでは確かめていません。
- 国内標準（TTC）に、IMS の保守と運用を決めたものがあるかは確かめていません。`www.ttc.or.jp` は 503 を返しました。[profiles.md](profiles.md) のとおり、TTC の標準は網のあいだの接続を決めたもので、保守用の API や OAM には当たらない見込みです。
- `web/oam` の画面は、Chromium でしか動かしていません。Firefox と Safari、スクリーンリーダーでの読み上げは確かめていません。
- `web/oam` の画面は、Compose の上の本物のノードと HSS にはつないでいません。役割 `oam` のつなぎ込みと、HSS の口が無いためです。確かめた相手は、偽の API と、本物の `internal/oam` に偽の HSS をつないだ一時的なプログラムです。
- 1 回の要求で送れる行の数の境目（30000 行は通り、60000 行は止まった）は、手元の 1 台の Chromium と loopback での値です。OS のソケットの大きさで変わります。画面が 200 行ずつ送るのは、この境目より十分小さくするためです。
- TS 28.111 の Annex B の表 B.2 では、いくつかの行で、原因と種類の列が合わないように見えます。たとえば Bandwidth Reduction に Security Service or Mechanism Violation、Excessive Vibration に Integrity Violation、File Error に Environmental が付いています。表の誤りか、テキストに直したときのずれかは確かめていません。6 章の案で B.2 から使った 2 つの原因の行（Communication Subsystem Failure と Underlying Resource Unavailable）は、ずれていないように見えます。Underlying Resource Unavailable の意味は、X.733 8.1.2.1 の説明とも合っています。
- objectInstance の DN の書き方の決まり（TS 32.300）は読んでいません。この記録の objectInstance は、DN に似せた形だと書くに留めています（6 章の SIMPLIFIED）。
- 警報の部分も、Compose の上の本物のノードにはつないでいません。どの役割も警報を上げないためです。確かめた相手は、偽の API と、本物の `internal/oam` に本物の `internal/maintenance` のノードをつないだ一時的なプログラムです。ノードの警報は、そのプログラムが `RaiseAlarm` で上げました。
- 加入者に見える値を断る確かめ（URI、`@`、`+` の番号、6 桁以上の数字）は、よくある書き方を見分けるだけです。`202-555-0123` のように区切った番号は見分けません。役割が識別子を書かないことが前提です。
