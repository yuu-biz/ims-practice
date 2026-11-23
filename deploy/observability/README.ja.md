# Prometheus と Grafana で指標を見る

[English](README.md)

このディレクトリは、Docker Compose のプロジェクトに [Prometheus](https://github.com/prometheus/prometheus) と [Grafana](https://github.com/grafana/grafana) を足します。IMS のノードが数えた値を、広く使われている道具で、時間の推移として見るためです。IMS のノードのうち指標を出すのは P-CSCF、I-CSCF、S-CSCF、HSS だけなので（[できないこと](#できないこと)）、ダッシュボードではほかのノードが落ちているように見えます。指標の名前は TS 32.409 の測定項目に合わせてあり、`internal/metrics` が書きます。

| パス | 中身 |
| --- | --- |
| `compose.observability.yaml` | 2 つのサービス。ルートの `compose.yaml` に重ねて使うファイル |
| `prometheus/prometheus.yml` | Prometheus が何を、どの間隔で集めるか。中の `ims` のジョブは生成したもの |
| `prometheus.go`、`genprometheus/` | ルートの `compose.yaml` のサービスから `ims` のジョブを作る生成器 |
| `grafana/provisioning/` | Prometheus のデータソースと、ダッシュボードのフォルダ。Grafana の起動時に読み込まれる |
| `grafana/dashboards/ims-overview.json` | ダッシュボード 1 枚。ノードの稼働、登録、セッション、Diameter の結果 |
| `observability_test.go` | `prometheus.yml` が `compose.yaml` に追いついていることと、ダッシュボードが用意したデータソースを使っていることを確かめる試験 |

## 起動と停止

リポジトリのルートで実行します。2 つ目の `-f` で、IMS に 2 つのサービスを足します。`compose.yaml` そのものは変えません。

```sh
docker compose -f compose.yaml -f deploy/observability/compose.observability.yaml up -d
```

- Prometheus は <http://localhost:9090> です。<http://localhost:9090/targets> に、ノードごとに最後に集めたときの結果が並びます。
- Grafana は <http://localhost:3000> です。最初の画面がダッシュボード **IMS overview** です。サインインしなくても見られます。設定を変えるときは、`admin` とパスワード `admin` でサインインし、新しいパスワードを決めます。

どちらのポートも `127.0.0.1` にだけ公開します。ここではどちらの道具もパスワードを求めないためです。

UE を登録すると、数えた値が見えます。

```sh
docker compose run --rm ue register -deregister-after 5s
```

Prometheus は 15 秒ごとに集めます。その時間のうちに、パネル **REGISTER attempts and successes** に P-CSCF と S-CSCF がそれぞれの `role` で数えた登録と登録の解除が出て、**Initial registration success ratio** に最初の登録のうち成功したものの割合が、**Initial registration set-up time** に P-CSCF での最初の登録にかかった時間が出ます。[音声通話の流れを追う](../../docs/guide/ja/call.md) のとおりに通話すると、パネル **Session attempts and answers** と **Answered sessions** に値が出ます。P-CSCF は通話の発信側と着信側の両方の区間で数え、I-CSCF はホーム網に入る着信のセッションとして 1 回数えます。パネル **Registered identities** は、同じ登録を HSS の側から見たものです。加入者の SIP URI と tel URI がそろって登録済みになり、登録の解除でそろって外れます。パネル **Provisioned subscriptions and identities** は、登録している利用者がいるかどうかに関わらず、HSS が持っている加入者と公開識別子の数を出します。

止めて、取ってきたイメージと Prometheus のデータも含めて消すには、次のコマンドを使います。

```sh
docker compose -f compose.yaml -f deploy/observability/compose.observability.yaml down --rmi all -v --remove-orphans
```

## compose.yaml を変えたあと

`prometheus/prometheus.yml` の `ims` のジョブは、`# BEGIN generated` の行と `# END generated` の行の間にあり、ルートの `compose.yaml` から作ります。`compose.yaml` でノードを足したり除いたりしたら、リポジトリのルートで次のコマンドを実行します。

```sh
go generate ./deploy/observability/...
```

変わった `prometheus.yml` をコミットします。`go generate ./deploy/...` でも同じことができ、こちらは `deploy/k8s` の写しも作り直します（`go generate ./deploy/k8s/...` と同じです）。1 つのコマンドで両方がそろいます。作り直すまでは `observability_test.go` が失敗し、実行するコマンドを示します。

ノードとして扱うのは、リポジトリのルートの `Dockerfile` から作るイメージ（IMS の役割を動かすイメージ）を動かすサービスのうち、プロファイルに入っていないものです。このイメージは `build: .` のサービスが作り、ほかのサービスはその名前で使います。つまり `docker compose up` で起動するものです。ノードの一覧を手で持つことはしません。UE シミュレータは同じイメージを使いますが、プロファイルの中でしか起動しないので、対象から外れます。2 つの目印の行の外（`global` など）は手で書いた部分で、生成器はそのまま残します。

## できないこと

`internal/metrics` で数える役割は P-CSCF、I-CSCF、S-CSCF、HSS、IBCF です。Compose のプロジェクトの 3 つの CSCF と HSS は、`deploy/compose/pcscf.yaml`、`deploy/compose/icscf.yaml`、`deploy/compose/scscf.yaml`、`deploy/compose/hss.yaml` の `maintenance` の節が決める待ち受けで、`http://pcscf:8085/metrics`、`http://icscf:8085/metrics`、`http://scscf:8085/metrics`、`http://hss:8085/metrics` に数えた値を出します。HSS のノードはこの待ち受けに指標だけを出し、保守用の API は出しません。HSS には、運用者が設定する管理状態がないためです。パネル **Dropped sessions** は空のままです。P-CSCF も I-CSCF もセッションを期限切れで消さないので、数えるものがありません。ほかのノードは指標を出しません。IBCF のノード（`deploy/interconnect`）にはこの待ち受けが無いので、IBCF が数えた値はプロセスの中にとどまります。`prometheus/prometheus.yml` は、各ノードが保守用の API の待ち受けで、`http://<サービス名>:8085/metrics` に指標を出す形に合わせて書いてあります。このポート番号を決めた仕様はありません。8085 はこのプロジェクトが選んだ番号で、`prometheus.go` の `MetricsPort` に置いています。ほかの番号にするときは、`MetricsPort` を直し、上のコマンドを実行します。

指標は `internal/metrics` が Prometheus のテキスト形式（版 0.0.4）で書きます。ラベルの値は、決まった集合の中からしか取れません。そのため、加入者ごとの系列はできず、加入者が何人いても系列の数は変わりません。

コードは Prometheus の文書と TS 32.409 にあることのうち、次のものを省いています。どの場所にも、コードに `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| `metrics.NewHandler`、prometheus/docs instrumenting/content_negotiation.md | Accept ヘッダの形式から選ぶことと、gzip。ハンドラはいつも、圧縮しないテキスト形式 0.0.4 で答えます。 | どの Prometheus もテキスト形式を求め、gzip は任意だからです。ほかの形式は、ここの指標が使うものを何も足しません。 |
| `metrics.Histogram`、prometheus/docs instrumenting/exposition_formats.md | 観測が続くあいだに、バケット、合計、件数をそろえて写し取ること。"+Inf" のバケットはいつも `_count` と同じですが、`_sum` には、バケットにまだ入っていない観測が次の収集まで含まれることがあります。 | 写し取るには、もう 1 組のカウンタと入れ替えの仕組みが要るためです。平均のずれは観測 1 回分までで、続くのも 1 回の収集のあいだだけです。 |
| P-CSCF。TS 32.409 4.1.1.1.1 と 4.1.1.1.3 | P-CSCF が UE の IP の対応付けを読む前に答えた REGISTER は、登録のどの測定にも数えません。読めない REGISTER（400 を返す）、Max-Forwards を使い切った REGISTER（483 を返す）、対応付けを読めなかった REGISTER（500 を返す）です。 | REGISTER が最初の登録を始めるか再登録を始めるかは、UE が P-CSCF と IP の対応付けを持っているかどうかで決まるためです。 |
| I-CSCF。TS 32.409 4.1.2.12 と 4.1.2.8 | I-CSCF は、着信のセッションの確立に成功した数も、期限切れで消えたセッションの数も数えません。 | 成功の数を読むパネルがないためです。P-CSCF と S-CSCF の成功の数も読んでいません。I-CSCF は Record-Route に入らないので、ダイアログの 2 つ目以降の要求を見ず、期限切れになるセッションを持ちません。 |
| I-CSCF。TS 32.409 4.1.2.11 | shut down か lock のあいだに I-CSCF が断った最初の INVITE は、どの測定にも数えません。 | 送り手が次の I-CSCF を試し、その台が数えるためです。台ごとの数を足すと、網の中の試行の数になります。 |
| S-CSCF。TS 32.409 4.1.1.1.1 と 4.1.1.1.3 | S-CSCF が利用者について持っているものを読む前に答えた REGISTER は、登録のどの測定にも数えません。読めない REGISTER（400 を返す）、利用者のロックを取れなかった REGISTER（500 を返す）、運用者が shut down か lock をしているあいだに断った REGISTER（503 を返す）です。 | REGISTER が最初の登録か再登録かは、S-CSCF がその利用者の登録を持っているかどうかで決まるためです。 |
| HSS。TS 32.409 4.2.1.1.2、4.2.1.1.5、4.2.1.1.6、4.2.1.1.7 | HSS は、持っている IMPI の数も、PSI と私的サービス識別子の数も数えません。 | ここでは 1 つの加入者が IMPI を 1 つだけ持つので、その数は加入者の数に名前を付け替えただけになるためです。PSI はワイルドカードの範囲で、1 つの範囲がサブドメインの会議 URI すべてを表すので、数えられる個数がありません。 |
| HSS。TS 32.409 4.2.1.2.2 | HSS は、未登録（unregistered）の状態にある IMPU の数を数えません。登録していない利用者にサービスを提供するために、S-CSCF がプロファイルを持っている IMPU のことです。 | 読むパネルがないためです。 |
| HSS。TS 32.409 4.2.1.1 と 4.2.1.2 | 加入者をプロセスの外の保存先に置いた HSS は、データベースの gauge をどれも出しません。`http://hss:8085/metrics` に `ims_hss_` の系列は出ず、HSS の 2 つのパネルは空のままです。 | 数を持つのは、どの台も共有している保存先だからです。起動した台が、設定にある加入者をすでに保存先に見つけたときは、その台が足した加入者は 1 人もいないので、自分がした操作から数を出せません。 |

## 知っておくこと

- **集める通信はキャプチャに写ります。** Prometheus は `ims` のネットワークに入るので、15 秒ごとに各ノードへ HTTP の要求を 1 つ送り、それが `ims0` のブリッジを通ります。Wireshark では、表示フィルタ `!(tcp.port == 8085)` で隠せます。
- **対象のラベルは `role` ではなく `node` です。** 1 つのノードが複数の役割を持てるので、ノードは自分の指標に `role` のラベルを付けます。Prometheus はすべての系列に `job` と `instance` を足します。`prometheus.yml` は、それに加えて `node` を足します。
- **カウンタは 0 に戻りません。** TS 32.401 の Cumulative Counter は、granularity period ごとに 0 から数え直します。Prometheus のカウンタは増える一方で、ある期間の増分は Prometheus が計算します（`increase()`、`rate()`）。ダッシュボードは、2 つの増分を割って TS 32.454 の成功率を出しています。
- **Grafana は外へ連絡しません。** 環境変数で、利用状況の送信、更新の確認、ニュースの表示を止めています。

## ライセンス

| イメージ | ライセンス | 確かめた所 |
| --- | --- | --- |
| `prom/prometheus:v3.15.1` | Apache-2.0 | prometheus のリポジトリのタグ `v3.15.1` にある `LICENSE` と、`Dockerfile` のラベル `org.opencontainers.image.licenses`。prometheus.io のどのページの末尾にも、すべての部品が Apache 2 License だと書いてあります |
| `grafana/grafana:13.3.0` | AGPL-3.0-only。一部のディレクトリは Apache-2.0 | grafana のリポジトリのタグ `v13.3.0` にある `LICENSE` と `LICENSING.md`、grafana.com の「Licensing」のページ |

**Grafana のイメージの版の種類。** Grafana の文書の「Run Grafana Docker image」のページは、2 つの版を挙げています。`grafana/grafana` が Grafana Open Source で、`grafana/grafana-enterprise` が Grafana Enterprise です。同じページによると、`grafana/grafana-oss` は 12.4.0 から更新されていません。中身は `grafana/grafana` と同じイメージでした。Docker Hub の `grafana/grafana-oss` には、`13.3.0` のタグがありません。つまり `grafana/grafana:13.3.0` は、オープンソース（AGPL）の版です。このページは、Enterprise を推奨の既定の版としています。Enterprise はライセンスキーなしで無料で動かせます。「Licensing」のページの FAQ は、Grafana のコードを変えるつもりのない利用者向けに、Enterprise の配布物を案内しています。無料で使える、独自のライセンスのバイナリだという説明です。このディレクトリでは、公開のリポジトリのコードがそのまま動くように、オープンソースの版を使います。

**元にしたイメージ。** イメージには、元にしたイメージのパッケージも入っています。そのライセンスは 1 つずつは確かめていません。Docker Hub に載っている層の一覧によると、`prom/prometheus:v3.15.1` は BusyBox のファイルシステム（BusyBox 1.38.0、uClibc、Buildroot で作ったもの）を元にしています。`grafana/grafana:13.3.0`（amd64）は Alpine Linux 3.24.3 を元にし、glibc を足しています。「Configure a Grafana Docker image」のページにも、既定のイメージは Alpine を元にすると書いてあります。このプロジェクトはイメージを動かすだけで、Grafana のコードを変えたり配ったりはしません。

ここに書いたのは公開のページで確かめた事実で、法律の助言ではありません。
