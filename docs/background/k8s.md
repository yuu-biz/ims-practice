# Kubernetes で動かす構成の調査

この文書は、IMS のノードを Kubernetes で動かすときに関わる仕様と Kubernetes の振る舞いを調べ、kind で試した記録です。この実装の作りとの関係は、マニフェストが `deploy/k8s/` にあり、使い方が `deploy/k8s/README.ja.md` にあります。仕様の本文は写さず、要点を言い換えて書きます。クラウドの Kubernetes に載せる構成は [cloud.md](cloud.md) にあります。

マニフェストは、EPC と PCRF のスタブ（役割 `epc`）を含む構成を元にしています。その後、写しを作り直す道具（2 章）、kind で確かめるスクリプト（9 章）、クラスタごとに違う値を 1 か所に書く形、クラウドの overlay（[cloud.md](cloud.md) の 9 章）を足しました。MMTel AS が呼の経路に入ったときに、`e2e-kind.sh` のキャプチャの確認を直しました（9 章）。

## 読んだ資料

| 資料 | 版、読んだ所 |
| --- | --- |
| RFC 3261（SIP） | 16.6 の手順 4（Record-Route）、18.1.1（大きな要求は TCP）、18.2.1（received） |
| RFC 3263（SIP の DNS） | 4.1（NAPTR で転送方式を選ぶ）、4.2（SRV と A でポートとアドレスを決める） |
| RFC 3581（rport） | 4（サーバーの振る舞い） |
| RFC 3327（Path） | 5.2（中継するプロキシの手順） |
| RFC 2181 | 10.3（NS の値は別名にしない） |
| RFC 2782（SRV） | SRV の名前の形と、優先度と重みの使い方 |
| RFC 6733（Diameter） | 2.1（ポート 3868） |
| Kubernetes の文書 | 「DNS for Services and Pods」「Service ClusterIP allocation」「Virtual IPs and Service Proxies」。kubernetes.io に届かなかったので、GitHub の kubernetes/website にある原稿（main）を読みました |
| kind の文書 | 「Configuration」。kind.sigs.k8s.io に届かなかったので、GitHub の kubernetes-sigs/kind にある原稿（main）を読みました |
| CoreDNS の file プラグイン | GitHub の coredns/coredns にある README（master） |

試した環境は次のとおりです。

| もの | 版 |
| --- | --- |
| kind | v0.33.0（GitHub のリリースから取得） |
| ノードのイメージ | kindest/node v1.37.0（kind v0.33.0 の既定） |
| kubectl | v1.37.2（kustomize v5.8.1 を内蔵） |
| containerd（ノードの中） | v2.3.4 |
| クラスタの CoreDNS | v1.14.6 |
| ホスト | Linux 6.18、Docker 29.10.0、cgroup v1、overlayfs |

## 1. kind を動かすまで

このホストでは、kind の既定の設定ではクラスタを作れませんでした。原因は 2 つあり、ログから突き止めました。

1. kubelet が起動しません。Kubernetes v1.37 の kubelet は、既定で cgroup v1 のホストを拒みます（KubeletConfiguration の `failCgroupV1: true`）。kubelet のログに「cgroup v1 のホストでは動かない設定になっている」とありました。
2. 1 を直すと、今度は Pod のサンドボックスが作れません。runc が `oom_score_adj` を負の値にしようとして、Permission denied になります。このホストでは root でも CAP_SYS_RESOURCE がありません（`/proc/self/status` の CapEff でビット 24 が 0）。特権コンテナの中からも、負の値は書けませんでした。

kind の設定に次の 2 つを足すと、クラスタができました。どちらもこのホストのためだけのもので、cgroup v2 の普通のホスト（Docker Desktop を含む）では要りません。そのため、リポジトリの `deploy/k8s/kind-cluster.yaml` には入れず、README の「うまく動かないとき」に書きました。

- `kubeadmConfigPatches` で KubeletConfiguration の `failCgroupV1: false`。
- `containerdConfigPatches` で CRI の `restrict_oom_score_adj = true`。containerd は、要求された値を自分の値（ここでは 0）より下げなくなります。kind が rootless で動くときに使う設定と同じです。

ほかに分かったことです。

- イメージの取得は、ホストの Docker では通りました。BuildKit のコンテナ（ビルダー `ims-practice`）からは、プロキシの CA を信頼しないため Docker Hub に届きませんでした。既定のビルダーでも、ビルドの中の `go mod download` が同じ理由で失敗しました。このため、プロジェクトのイメージは、ホストで Go のバイナリとダッシュボードの画面を作り、`alpine:3.24` に入れて作りました。中身は Dockerfile の最後の段と同じです。
- `kind load docker-image` は、複数のプラットフォームを持つイメージ（`coredns/coredns:1.12.1`）で失敗しました。`docker save --platform linux/amd64` で保存し、`kind load image-archive` で入れると通りました。

## 2. Kustomize か Helm か

Kustomize にしました。

- kubectl に入っているので、`kubectl apply -k` のほかに道具が要りません。Helm は helm のコマンドが要ります。
- マニフェストは普通の YAML のままで、テンプレートの言語がありません。初学者が Kubernetes の文書と照らし合わせて読めます。
- DNS の 2 つの構成は、同じ土台（base）に重ねる overlay で書けます。Helm の values で分岐させるより、違いがファイルとして見えます。

Kustomize には、自分のディレクトリの外のファイルを読まない制限（load restrictor）があります。`deploy/compose/` の設定ファイルとゾーンは直接読めません。`--load-restrictor LoadRestrictionsNone` を付ければ読めますが、`kubectl apply -k` には付けられず、`kubectl kustomize ... | kubectl apply -f -` と長くなります。そこで、`deploy/k8s/` の中に写しを置くことにしました。

### 写しの作り方

写しは手で直さず、リポジトリのルートで `go generate ./deploy/k8s/...` を実行して作ります。`deploy/compose/` を変えた人が、1 つのコマンドで写しを直せるようにするためです。Go はすべての開発者が使う道具なので、ほかの道具を足さずに済みます。

- 元と写しの違いは、`deploy/k8s/changes.go` の表にすべて書きます。キーごとに、置き換える値、書き換えるコメント、消すかどうかを持ちます。ファイルを名指ししない項目（`hep.collector` など）は、そのキーを持つすべての役割のファイルに当てます。ゾーンの規則（A レコードを Service への CNAME にし、名前サーバーだけアドレスを残して最後の行に置く）も同じファイルにあります。名前サーバーのアドレスは、道具が `deploy/k8s/environments/kind` から読みます（[cloud.md](cloud.md) の 9 章）。
- 道具（`deploy/k8s/gencopies`）と試験（`deploy/k8s/sync_test.go`）は、同じ関数 `k8s.Copies` を呼びます。試験は、その出力とリポジトリの写しを比べ、違えば `go generate ./deploy/k8s/...` を実行するように表示して失敗します。変換の定義は表の 1 か所だけなので、道具と試験の判断が食い違うことはありません。
- YAML は行ごとに書き換えます。YAML の解析器（`go.yaml.in/yaml/v3`）はキーの位置を探すためだけに使います。`yaml.Node` をそのまま書き出すと、コメントとキーの順は残りますが、`hss.yaml` のフロー形式（`{...}`）の中にある、引用符の無い文字列に引用符が付くなど、誰も変えていない所が元と違ってきました。行ごとに書き換えれば、表に無い所は元と 1 文字も違いません。
- 書き換えたあとの写しは、もう一度 YAML として読めることを確かめます。表のキーが元に無いときと、どのファイルにも当たらない項目があるときは、道具が失敗します。
- 試験は、ほかに次のことを確かめます。写しに compose のネットワークのアドレス（172.28.0.x）が残っていないこと（EPC の加入者を除きます。5 章）。表のキーを除けば、元と写しのデータが同じであること。役割ごとに `base/` の workload と ConfigMap があること。ゾーンの CNAME の先に Service があること。
- 新しい役割の設定ファイルは、道具が自動で写します。ただし workload と Service のマニフェストは役割ごとに違うので、手で書きます。足りないときは試験が知らせます。

写しの先頭には、元のファイルと道具のコマンドを書いたコメントを付けました。写しを開いた人に、手で直してはいけないと分かるようにするためです。

## 3. 役割ごとの workload

| 役割 | 種類 | Service | 理由 |
| --- | --- | --- | --- |
| P-CSCF、I-CSCF、S-CSCF、IP-SM-GW | StatefulSet | headless（UDP と TCP の 5060） | 自分の名前を Via、Path、Service-Route、Record-Route に書きます。その名前で後から届く要求は、同じノードに着かなければなりません（RFC 3261 16.6 の手順 4）。StatefulSet なら、台を増やしても `scscf-0`、`scscf-1` と Pod ごとに変わらない名前が付きます |
| HSS | StatefulSet | headless（3868、8081） | Diameter のピアは Origin-Host で互いを見分けます。名前と Origin-Host が Pod ごとに決まっている必要があります |
| EPC（PCRF を含む） | StatefulSet | headless の `epc`（8080）と `pcrf`（3868） | HSS と同じです。PCRF は P-CSCF の Diameter のピアです |
| メディア中継ノード（agw） | StatefulSet | headless（8080 と RTP の 40000〜40039） | P-CSCF が名前で制御 API を呼びます。RTP は Pod のアドレスへ直接送ります |
| ダッシュボード | Deployment | ClusterIP（8080、9060/UDP） | 信号の中で名前を使われず、ピアが頼る識別子もありません |
| UE の模擬（`ue`、`ue2`、`ue-a`、`ue-b`） | Deployment | `ue-a` と `ue-b` だけ ClusterIP（8090） | 網の一部ではなく、名前で探されません。Contact には Pod のアドレスを書きます |
| 外付けの DNS サーバー（overlay b） | Deployment | 固定の ClusterIP（kind では 10.96.0.53） | Pod の resolv.conf にはアドレスを書くので、変わらないアドレスが要ります |
| キャプチャ | DaemonSet | なし | ノードごとに 1 つ、ノードのネットワークで動きます（8 章） |

StatefulSet は `podManagementPolicy: Parallel` にしました。Compose の `depends_on` のような起動の順番は付けていません。各ノードは Diameter の再接続（Tc）と、要求のたびの DNS の問い合わせで、相手が後から起きても追いつきます。kind で試すと、I-CSCF は最初の接続で HSS の名前を引けずに失敗し、5 秒後の再接続で開きました。

Docker Compose の `ue` と `ue2` は、コマンドごとにコンテナを作ります。Kubernetes では待っている Pod を置き、`kubectl exec deploy/ue -- ue register ...` で中で実行します。`ue-a` と `ue-b` は Compose の `sim` プロファイルに当たるので、replicas を 0 にしておき、`kubectl scale` で起こします。

## 4. SIP over UDP と Service

### ClusterIP の Service に送るとき

ClusterIP の Service は仮想のアドレスで、kube-proxy が iptables の DNAT で Pod のアドレスに書き換えます。UDP にも conntrack の項目ができ、応答の向きでは送信元が ClusterIP に戻されます。kind で、UE から P-CSCF の ClusterIP（試験用に作った Service）へ REGISTER を送って確かめました。

- UE には、応答が ClusterIP から来たように見えます。P-CSCF の Pod が送った応答の送信元が、途中で ClusterIP に書き換わるためです。
- P-CSCF が受けた REGISTER の送信元のポートは 43578 でした。UE は 5060 から送っています。この UE と P-CSCF の間には、headless の名前で送った前の登録の conntrack の項目（UE の 5060 と P-CSCF の 5060）が残っていました。DNAT のあとの組が既存の項目とぶつかるので、カーネルが送信元のポートを付け替えました。P-CSCF は Via に `rport=43578;received=<UE の Pod のアドレス>` を書き、応答をそのポートへ返しました（RFC 3581 4）。応答は conntrack で元に戻って UE に届きました。

つまり、ClusterIP を挟むと、相手が見る送信元のアドレスとポートが、ノードが Via や Contact に書いたものと違ってきます。登録や通話は通りましたが、次の点で困ります。

- 学習者がキャプチャで見るアドレスが、ノードの設定やログと合いません。
- P-CSCF は、UE を受けた送信元のアドレスとポートで覚えます（IP の対応付け）。NAT が組を付け替えると、同じ UE が別の送信元に見えることがあります。
- 台を増やすと、ClusterIP は conntrack の項目ごとに Pod を選びます。項目が消えたあとの要求や、別の 5-tuple の要求は、別の台に着くことがあります。Record-Route や Path の名前が ClusterIP を指すと、RFC 3261 16.6 の手順 4 が求める「同じ要素に着く」が守れません。

### headless の Service に送るとき

headless の Service（`clusterIP: None`）には仮想のアドレスがありません。名前を引くと、選ばれた Pod のアドレスが返ります。Pod から Pod へ NAT 無しで届くので、相手が見る送信元は、送った Pod のアドレスとポートそのものです。kind で登録、通話、SMS を試し、キャプチャの順番を `scripts/e2e-registration.sh` と同じ条件で調べると、登録の順番の 18 の確認がすべて通りました（9 章）。

そこで、SIP と Diameter のノードはすべて headless にしました。HEP だけは ClusterIP にしました。HEP は片方向の UDP で、応答が無いので NAT で困りません。ダッシュボードの Pod が入れ替わってもアドレスが変わらないので、各ノードは起動時に一度引いたアドレスに送り続けられます。

### 台を増やすとき

今の土台は各役割 1 台です。kind で S-CSCF を 2 台にすると、2 台目の Diameter の接続は、HSS に「同じピアからの 2 本目の接続」として拒まれました。2 台とも同じ設定ファイルを読み、同じ Origin-Host を名乗るためです。台を増やすには、Pod ごとに次のものを変える必要があります。

- SIP の `host`（Via、Path、Service-Route、Record-Route の名前）。たとえば `scscf-0.scscf.ims.mnc001.mcc001.3gppnetwork.org`。
- Diameter の `origin_host`。
- その名前を Pod のアドレスにつなぐ DNS のレコード。StatefulSet と headless の Service で、クラスタの DNS は `scscf-0.scscf.ims.svc.cluster.local` を答えます。ゾーンには Pod ごとの CNAME を置くか、CoreDNS の rewrite プラグインで名前を写します。

Pod の名前は downward API で取れるので、設定ファイルに書き込むのは、今の agw の init コンテナと同じ方法でできます。

## 5. 各ノードが書くアドレスと設定

| どこに | 何を書くか | 設定とマニフェスト |
| --- | --- | --- |
| Via、Path、Service-Route、Record-Route | ノードの FQDN（`pcscf.ims.mnc001.mcc001.3gppnetwork.org` など） | `sip.host`。Compose と同じ値です。DNS がそれを Pod のアドレスにつなぎます（7 章） |
| UE の Contact と Via | UE の Pod のアドレス | UE が P-CSCF への経路から自分のアドレスを求めます（`cmd/ue`）。設定は要りません |
| SDP（P-CSCF が書き換えたもの） | agw の Pod のアドレス | `agw.rtp_address`。Pod は起動するまで自分のアドレスを知らないので、init コンテナが `${POD_IP}` を downward API の値に置き換えます |
| PCO の P-CSCF のアドレス | P-CSCF の Pod のアドレス | `epc.pcscf_addresses`。init コンテナが `pcscf.ims.svc.cluster.local` を引いて `${PCSCF_IP}` に入れます |
| HEP の送り先 | ダッシュボードの Service 名 | `hep.collector: dashboard.ims.svc.cluster.local:9060` |
| Diameter の接続先 | HSS の FQDN（P-CSCF の Rx がつながれば PCRF の FQDN も） | `diameter.peers`。Compose と同じ値です |
| ダッシュボードの接続先 | HSS と UE の Service 名 | `dashboard.hss_provisioning`、`dashboard.ues` |

まだ解けていないことが 2 つあります。

- EPC は P-CSCF のアドレスを起動時に 1 回だけ決めます。kind で P-CSCF の Pod を消すと、新しい Pod は別のアドレス（10.244.0.13 から 10.244.0.16）になりました。EPC の設定は古いアドレスのままでした。PCO にはアドレスを入れるので、EPC のスタブが接続のたびに名前を引くようにするのが素直です。
- EPC の加入者ごとの静的なアドレス（[volte-access.md](../design/volte-access.md) の EPC のスタブ）は、Pod には付けられません。kind の既定の CNI（kindnet）で、Pod のアドレスを指定する方法は見つかりませんでした。この調べの時点では UE がまだ EPC に接続しなかったので、この写しには Compose のアドレスを残しました。

## 6. メディアの経路

### クラスタの中の UE

UE の Pod は、SDP に書かれた agw の Pod のアドレスへ RTP を直接送ります。kind で通話を試すと、UE から agw の 40000 へ 150 パケット、agw の 40004 から相手の UE へ 150 パケットが流れ、相手は送った 24000 バイトをすべて受けました。Service もポートの公開も要りません。agw の Service の RTP のポートは、今は説明のためだけにあります。

### クラスタの外の UE

ホストのソフトフォンなど、クラスタの外の UE には、P-CSCF の 5060 と agw の RTP のポートをノードに出す必要があります。候補は次のとおりです。

| 方法 | 長所 | 短所 |
| --- | --- | --- |
| hostPort（Pod のポートをノードのポートにつなぐ） | ポートの番号がそのまま使えます。Linux のホストなら、ノードのアドレス（kind のネットワークの 172.18.0.x）へ直接届きます | ノードごとに 1 台しか置けません。ポートを 1 つずつ書きます |
| kind の extraPortMappings | Docker Desktop のように、ホストからノードのアドレスに届かないときにも使えます | ポートを 1 つずつ書きます。クラスタを作るときにしか決められず、Compose と同時には使えません（5060 が重なります） |
| NodePort | 標準の仕組みです | 既定の範囲は 30000〜32767 です。SDP のポートと合わせるには、RTP の範囲をその中に置き、nodePort をポートと同じにします。kube-proxy の NAT が入ります |
| hostNetwork | NAT がありません | IMS のノードには使いません。ホスト固有のネットワーク設定に依存しない方針だからです |
| LoadBalancer | クラウドでは標準です | kind には無く、クラウドの構成で扱います（[cloud.md](cloud.md)） |

hostPort を kind で試しました。P-CSCF に hostPort 5060/UDP、agw に hostPort 40000〜40039/UDP を足し、P-CSCF の `host_access` を、`gateway` が kind のネットワークのゲートウェイ（172.18.0.1）、`address` がノードのアドレス（172.18.0.2）になるよう設定しました。ホストで動かした `ue call -pcscf 172.18.0.2:5060` から、クラスタの中の `ue2` へ発信でき、RTP は両方向に 150 パケットずつ流れました。P-CSCF には、UE が 172.18.0.1:5060 から来たように見えました。これはホストの kind のネットワークのアドレスで、NAT ではありません。Docker Desktop では、ホストとコンテナの間の通信が NAT を通る前提で作ってあるとおり（[environment-capture.md](../design/environment-capture.md)）、NAT のあとのアドレスになるはずです（確かめていません）。

## 7. DNS

`internal/sipdns` は、RFC 3263 のとおり NAPTR、SRV、A の順に引きます。P-CSCF は、REGISTER の Request-URI のドメインの NAPTR（RFC 3263 4.1）を引き、`_sip._udp` の SRV（RFC 3263 4.2）から I-CSCF の名前とポートを得ます。S-CSCF は tel URI を ENUM の NAPTR で SIP URI に変えます（RFC 6116）。どちらの NAPTR も、Kubernetes の DNS（CoreDNS の kubernetes プラグイン）は作りません。ゾーンのファイルが要ります。

一方、Kubernetes の DNS は、名前の付いたポートに SRV を作ります（「DNS for Services and Pods」）。UDP のポートの名前を `sip` にしたので、`_sip._udp.icscf.ims.svc.cluster.local` を引くと、kind では `0 100 5060 icscf-0.icscf.ims.svc.cluster.local` が返りました。RFC 3263 と同じ形の SRV が、クラスタの DNS から自動でできます。

ゾーンの写し（`deploy/k8s/dns/`）では、NAPTR と SRV を Compose と同じにし、各ノードの A レコードを、namespace `ims` の headless の Service への CNAME にしました。たとえば `icscf IN CNAME icscf.ims.svc.cluster.local.` です。CoreDNS は CNAME をたどり、CNAME と A を 1 つの応答で返しました。`internal/sipdns` は応答から A だけを拾うので、そのまま動きます。名前サーバー `dns` だけは CNAME にできないので（RFC 2181 10.3）、A レコードを 10.96.0.53 にしました。どちらの overlay でも、このアドレスは DNS サーバーの Service の固定の ClusterIP です。10.96.0.53 は、kind の Service の範囲 10.96.0.0/16 のうち、手で選ぶアドレスのために残される下の帯（10.96.0.1〜10.96.1.0）に入ります（「Service ClusterIP allocation」）。このアドレスは、あとでクラスタごとの値として `deploy/k8s/environments/kind` の `ims_dns_cluster_ip` に移しました。Service、Pod の resolv.conf、ゾーンへは、kustomize の `replacements` が写します（[cloud.md](cloud.md) の 9 章）。

### 構成 (a) クラスタの DNS にゾーンを載せる

`overlays/cluster-dns` です。kube-system の ConfigMap `coredns` の Corefile を置き換え、kind の既定の `.:53` のあとに、ホームドメインと `e164.arpa` のサーバーブロック（file プラグイン）を足します。ゾーンは ConfigMap `ims-zones` に入れ、CoreDNS の Deployment に `kubectl patch` でマウントします。kubeadm が作った Deployment は kustomize の外にあるので、この 1 手は kustomize でできません。

- Pod は `dnsPolicy: ClusterFirst` のまま、kube-dns（10.96.0.10）に問い合わせます。
- kind で試すと、登録、通話、SMS が通りました。キャプチャには、P-CSCF から 10.96.0.10 への NAPTR、SRV、A の問い合わせがあり、応答は CoreDNS の Pod のアドレスから返っていました（8 章の「-Q in」のため）。
- 置き換える Corefile は kind の版に結び付きます。kind を上げるときは、既定の Corefile と比べる必要があります。
- 学習者は `kubectl -n kube-system logs deployment/coredns` で、IMS の問い合わせを見られます（`log` プラグイン）。

### 構成 (b) 外付けの DNS サーバー

`overlays/external-dns` です。Compose の `dns` と同じ CoreDNS のイメージを Deployment で動かし、固定の ClusterIP 10.96.0.53 の Service を付けます。ゾーンは (a) と同じ ConfigMap `ims-zones` です。IMS の Pod は `dnsPolicy: None` と `dnsConfig.nameservers: [10.96.0.53]` で、この DNS サーバーに問い合わせます。cluster.local など残りの名前は、DNS サーバーがクラスタの DNS へ転送します。

- kind で試すと、登録、通話、SMS が通りました。キャプチャには、IMS のノードから 10.96.0.53 への問い合わせと、DNS サーバーからクラスタの DNS への転送の両方が見えます。名前解決の流れが 2 段になることを学べます。
- kube-system に手を入れません。クラスタを壊す心配が少なく、ほかのクラスタにも持っていけます。

### ndots

Kubernetes の既定の resolv.conf は `ndots:5` です。ドットが 5 つ未満の名前は、先に検索リストを付けて引かれます。`dashboard.ims.svc.cluster.local` を引くと、`dashboard.ims.svc.cluster.local.ims.svc.cluster.local` などの NXDOMAIN がキャプチャに並びます。Compose のコンテナの resolv.conf は `ndots:0` で、検索リストもありません（このホストの Docker 29.10.0 で確かめました）。そこで、すべての IMS の Pod を `ndots:1` にしました。ドットを含む名前はそのまま引かれ、`hss` のような 1 語の名前だけが検索リストで補われます。`internal/sipdns` は名前を FQDN にして引くので、もともと検索リストを使いません。

### (c) ゾーンを持たない構成

このプロジェクトは、NAPTR と SRV が無い構成でも、RFC 3263 のとおり A レコードと既定のポートで動くことを求めています。Kubernetes のクラスタの DNS だけを使う構成では、ホームドメインの名前がありません。ノードの名前を `icscf.ims.svc.cluster.local` のような cluster.local の名前にし、ホームドメインの頂点の A レコードの代わりに、P-CSCF が I-CSCF を設定で知る必要があります。今の P-CSCF は Request-URI のドメインから I-CSCF を探すので、設定か P-CSCF の変更が要ります。この構成は作っていません（11 章）。

## 8. キャプチャ

### ノードの tcpdump

`deploy/k8s/capture` の DaemonSet です。ノードのネットワーク（hostNetwork）で tcpdump を動かし、pcap をノードの `/var/log/ims-captures` に書きます。kind の CNI は Pod ごとに veth を作って経路でつなぎ、Compose の `ims0` のようなブリッジがありません。そこで `-i any` で全インタフェースを聞きます。

- Pod から Pod への 1 つのパケットは、ノードに入る veth と、ノードから出る veth の 2 か所を通ります。`-i any` だけで取ると、REGISTER が各段で 2 回ずつ pcap に入りました（入る向きと出る向き）。`-Q in` で入る向きだけを取ると、各段で 1 回ずつになりました。
- 入る向きのパケットは、kube-proxy の NAT の前です。ClusterIP への要求は宛先が ClusterIP のまま、応答は送信元が Pod のアドレスのまま記録されます。DNS の問い合わせは 10.96.0.10 宛て、応答は CoreDNS の Pod から UE 宛てに見えます。アドレスの組が違うので、Wireshark は問い合わせと応答を組にしません（応答に `dns.response_to` が付きませんでした）。ID と時刻で見比べることになります。逆の `-Q out` にすると、NAT のあとが記録されます。手順書でこの非対称を説明する必要があります。
- フィルタ `net 10.244.0.0/16` で Pod のネットワークだけにしました。この範囲も、あとで `deploy/k8s/environments/kind` の `pod_cidr` に移しました。API サーバーなど、ノード自身の通信が入りません。CoreDNS が API サーバーを見張る TLS の通信は Pod のネットワークなので入ります。
- `kubectl -n ims exec ds/capture -- tcpdump -i any -Q in -U -w - net 10.244.0.0/16` の出力を Wireshark に渡すと、ライブで見られました。Compose でコンテナの標準出力を Wireshark に渡す方法と同じ形です（[environment-capture.md](../design/environment-capture.md)）。
- ファイルは `kubectl cp` で取り出せました。キャプチャのイメージ（`images/capture`）は手を入れずに使いました。ただし、その `capture.sh` は名前の付いた 1 つのインタフェースを待つので、DaemonSet ではコマンドを書き直しています。
- ノードが複数あるクラスタでは、ノードごとに pcap ができます。`mergecap` で 1 つにまとめる必要があります。今の kind の設定はノード 1 つです。

### エフェメラルコンテナ

`kubectl debug` で、1 つの Pod のネットワークに tcpdump のコンテナを足せます。その Pod の eth0 だけを見るので、NAT の前後で迷いません。kind では、`kubectl debug pcscf-0 --image=ims-practice/capture:dev --profile=netadmin --container=tap -- sleep 600` のあと、`kubectl exec pcscf-0 -c tap -- tcpdump -i eth0 -U -w -` で P-CSCF の出入りを取れました。tcpdump を直接 `kubectl debug -i` で動かすと、出力が届きませんでした。エフェメラルコンテナは Pod を作り直すまで消せません。

### HEP とダッシュボード

HEP のエージェントは変えずに動きました。各ノードが `dashboard.ims.svc.cluster.local:9060` へ送り、ダッシュボードの API（`/api/calls`、`/api/registrations`、`/api/ues`）に呼と登録が出ました。`kubectl port-forward svc/dashboard` を通して、画面の HTML も返りました。`ue-a` から `ue-b` への発信を、ダッシュボードの API から行えました。

## 9. 確かめた結果

kind（1 ノード）で、`scripts/e2e-phase*.sh` と同じ UE のコマンドを `kubectl exec` で実行しました。e2e のスクリプトそのものは実行していません。

| 確かめたこと | (a) cluster-dns | (b) external-dns |
| --- | --- | --- |
| `ue register --deregister-after 2s` | 成功 | 成功 |
| 間違ったパスワードの登録が拒まれる | 成功（403） | 成功（UE が終了コード 1） |
| `ue2 answer` と `ue call tel:+1-202-555-0102`（RTP は agw を通る） | 成功、相手は 150 パケットを受信 | 成功、相手は 150 パケットを受信 |
| `ue2 register` と `ue sms --status-report +12025550102` | 成功、受信と status report あり | 成功、受信と status report あり |
| キャプチャで e2e-registration の登録の順番の 18 の確認（アドレスだけ読み替え） | すべて通過 | すべて通過 |
| SIP、DNS、Diameter の malformed と警告 | なし | 1 件（EPC の init コンテナの nslookup の再送。登録の前） |
| ダッシュボードの API と `ue-a` から `ue-b` への発信 | 成功 | API だけ確認 |
| `kubectl apply --dry-run=server` | 通過 | 通過 |
| P-CSCF の Pod を消したあとの登録 | 試していない | 成功（EPC の設定は古いアドレスのまま） |
| S-CSCF を 2 台にしたとき | 試していない | 2 台目の Cx の接続を HSS が拒否 |
| UE から P-CSCF の ClusterIP への登録（4 章） | 成功、送信元のポートが付け替わる | 試していない |
| ホストの UE から hostPort 経由の発信（6 章） | 試していない | 成功 |

### e2e のスクリプト

`deploy/k8s/e2e-kind.sh` が、ここまでの確認を 1 つのコマンドで行います。引数に `cluster-dns` か `external-dns` を渡します。

- 専用のクラスタ（既定の名前は `ims-practice-e2e`）と専用の kubeconfig を使います。README の手順で作ったクラスタと、使う人の kubectl の設定には触りません。同じ名前のクラスタがあると、始めずに終わります。
- イメージは `ims-practice/ims:kind-e2e` と `ims-practice/capture:kind-e2e` の名前で作ります。Compose の `ims-practice/ims:dev` を上書きしないためです。ノードの中では、`ctr images tag` でマニフェストの名前（`ims-practice/ims:dev`）を付けます。環境変数 `IMS_IMAGE` と `CAPTURE_IMAGE` で、作り済みのイメージを渡せます。
- 最後に、自分が作ったものだけを消します。クラスタ、自分で作ったイメージと取ってきたイメージ（kind のノードのイメージを含みます）、kind が作った Docker のネットワーク、作業用のディレクトリです。始める前に無かったものかどうかを、イメージの ID とネットワークの有無で見分けます。`--keep` を付けると消さずに残し、消し方を表示します。
- UE のコマンドは `kubectl exec` で、`scripts/e2e-registration.sh`、`e2e-call.sh`、`e2e-sms.sh` と同じ順に実行します。登録、登録の解除、間違ったパスワード、通話、話中（486）、取り消し（487）、未登録の相手（480）、status report を求める SMS です。pjsua と SIPp は載せていないので、それを使う確認は省きました。
- キャプチャは capture の DaemonSet の pcap を、その Pod の中の tshark で読みます。e2e のスクリプトと同じ `expect` の形で、要の信号の順番を確かめます。アドレスは Pod から取ります。DNS の宛先は、クラスタの Service から読みます。(a) では kube-dns の 10.96.0.10、(b) では `dns` の 10.96.0.53 です。
- malformed と警告の確認は、最初の REGISTER より後だけにしました。(b) では、EPC の init コンテナの nslookup が起動のときに DNS を再送し、それが警告になるためです（上の表）。今回の (b) の pcap でも、`pcscf.ims.svc.cluster.local` の問い合わせの再送が 1 件ありました。
- pcap は毎回 `e2e-out/kind-<構成>/` に取り出します。失敗したときは、すべての Pod のログ、Pod の一覧、イベントも置きます。
- このホストのためのパッチは、環境変数 `KIND_E2E_EXTRA_CONFIG` で渡すファイルに書き、`kind-cluster.yaml` の後ろに足します。リポジトリには入れません。

このホストで、スクリプトを次のように実行しました。プロジェクトの Dockerfile はこのホストでは作れないので（1 章）、ホストで作ったバイナリを `alpine:3.24` に入れたイメージと、プロキシの CA をビルドのときだけ渡して作ったキャプチャのイメージを、`IMS_IMAGE` と `CAPTURE_IMAGE` で渡しました。どちらのイメージの作り方も、リポジトリには入れていません。`KIND_E2E_EXTRA_CONFIG` には 1 章の 2 つのパッチを渡しました。

| 実行したこと | 結果 |
| --- | --- |
| `e2e-kind.sh external-dns` | 成功。UE の 10 の確認、キャプチャの 61 の確認、malformed なしがすべて通過。終わったあとにクラスタ、ノードのイメージ、ネットワーク `kind` が消えた |
| `e2e-kind.sh --keep cluster-dns` | 成功。確認は上と同じですべて通過。クラスタが残り、表示された 3 つのコマンドで消せた |
| 同じ名前のクラスタがあるときの実行 | 始めずに終了コード 2 |
| 無いイメージを `IMS_IMAGE` に渡したとき | クラスタを作る前に終了コード 2 |
| クラスタを作ったあとに失敗したとき（イメージの読み込みで失敗させた） | Pod の一覧、イベント、ログを集めてからクラスタを消した。ほかのクラスタが使っていたネットワーク `kind` は残した |
| イメージを自分で作る道（`IMS_IMAGE` も `CAPTURE_IMAGE` も無し） | 試していない。このホストではビルドが通らないため |

MMTel AS が呼の経路に入ったあとは、UE 2 への ACK が UE 1 への 200 より先に出ることがあり、キャプチャの確認が ACK の所で失敗しました。MMTel AS は B2BUA なので、UE 2 の 200 に自分ですぐ ACK を返すからです。`scripts/e2e-call.sh` と同じく、ACK と BYE の応答の順番を問わない確認に直しました。直したあとは、`external-dns` と `cluster-dns` のどちらも、UE の 10 の確認とキャプチャの 58 の確認がすべて通りました（[cloud.md](cloud.md) の 9 章）。

1 回の実行は、このホストで 2 分から 4 分ほどでした。ノードのイメージを取ってくる最初の回が長く、イメージがすでにあった 2 回目は短く済みました。キャプチャを始めてから確認を終えるまでは、どちらも 1 分半ほどです。

CI のジョブの案は `deploy/k8s/ci/kind-e2e.yml.proposed` に置きました。2 つの構成を matrix で回し、pcap とログを成果物として上げます。ワークフローには入れていません。

### GitHub の runner で tshark が pcap を読めなかったこと

CI の `kind` のワークフローの実行で、2 つの構成のどちらも、UE の確認はすべて通りました。そのあと、キャプチャの最初の確認で、tshark が `You don't have permission to read the file "/captures/ims-....pcap"` と言って終了コード 3 で終わりました。tshark がファイルを開いたときに EACCES が返ったという意味です。runner は Ubuntu 26.04.1（カーネル 7.0.0-1019-azure、イメージ `ubuntu-26.04` の 20261111.196.1）でした。

原因は、runner のホストの AppArmor にある tshark のプロファイルです。キャプチャの Pod の中の tshark にも、このプロファイルが当たります。根拠は次のとおりです。

| 確かめたこと | 結果 |
| --- | --- |
| 書く側と読む側のユーザーと権限（この環境の kind で測定） | 本物の Dockerfile と同じ作り方で作ったキャプチャのイメージを使いました。`alpine:3.24` は CI と同じダイジェストで、tcpdump 4.99.6-r1 と tshark 4.6.9-r0 も CI のビルドのログと同じ版です。イメージに `tcpdump` の利用者は無く、tcpdump は root のまま書きます。pcap は所有者 0:0、モード 0644 でした。umask は 0022 です。`kubectl exec` の読む側は uid 0 で、CapEff は `a80435fb`（CAP_DAC_OVERRIDE を含みます）でした。ファイルの権限では拒まれません |
| Compose との違い | `ci` の e2e は、同じ runner と同じイメージで `docker compose exec capture tshark -r` を実行して通っています。Docker はコンテナに `docker-default` のプロファイルを付けます。その中で動く tshark は、ほかのプロファイルに移りません |
| ホストのプロファイル | Ubuntu 26.04 の `resolute-updates` の `apparmor` 5.0.2-0ubuntu1~26.04.1 のパッケージを取ってきて中を見ました。`/etc/apparmor.d/tshark` があり、`/usr/bin/tshark` に付く enforce のプロファイルです。Alpine の tshark はちょうどこのパスにあります。このプロファイルで読めるファイルは、`abstractions/base`、`nameservice`、`user-tmp`（`/tmp/**`、`/var/tmp/**`、`~/tmp/**` のうち自分が所有者のもの）、`/usr/share/wireshark/**`、journal だけです。`/captures` は入りません。pcap を読む規則（`/**.pcap`）は、子のプロファイル `dumpcap` にしかありません。同じパッケージに、tcpdump、busybox、tar のプロファイルはありません |
| プロファイルの出どころ | AppArmor の上流のコミット `594f3915`（2025-02-07、「add tshark profile」）で入り、最初の版は v5.0.0 です |
| kind のノードと Pod | kind v0.33.0 は、ノードのコンテナを `--privileged` と `apparmor=unconfined` で作ります（`pkg/cluster/internal/providers/docker/provision.go`）。この環境では、ノードの中でも Pod の中でも `/sys/kernel/security` は空でした。containerd は、ここに AppArmor が見えないと Pod にプロファイルを付けないはずです（containerd の作りからの推測で、runner では確かめていません）。プロファイルを持たないプロセスが `/usr/bin/tshark` を実行すると、パスが合うプロファイルに移ります |

このため、ファイルの所有者やモードを変えても、ケーパビリティを足しても直りません。AppArmor はそれとは別に拒むからです。

直し方は、tshark が読んでよい場所にファイルを置くことです。`e2e-kind.sh` は、シナリオが終わったあと、キャプチャの Pod の中で pcap を `/tmp/ims-e2e.pcap` に `cp` で写し、tshark にはその写しを読ませます。`cp` も `kubectl exec` も root で動くので、写しの所有者は tshark と同じ root です。これで `user-tmp` の `owner /tmp/** rwkl` に当たります。`kubectl cp` で取り出すのは、今までどおり元の pcap です。`kubectl cp` は Pod の中の `tar` を使います。このイメージの `/bin/tar` は `/bin/busybox` へのリンクで、上のパッケージには、どちらのパスのプロファイルもありません。

ほかの案は採りませんでした。

- Pod に `appArmorProfile` の `RuntimeDefault` を付けると、Compose と同じ形になります。ただ、AppArmor の無いホスト（この環境もそうです）では、kubelet がその Pod を起動しないはずです。
- tshark を別のパスに置けば、プロファイルは当たりません。しかし、キャプチャのイメージ（`images/capture`）を変えることになり、ホストの決まりを避けるだけになります。
- pcap をノードから取り出し、Docker のコンテナの tshark で読む方法もあります。ただ、学習者が `kubectl exec` で読むときの形から離れます。

次の CI の実行でこの見立てを確かめられるように、`e2e-kind.sh` がキャプチャの確認の始めに `tshark in the capture Pod: uid 0, AppArmor profile: ...` の 1 行を表示するようにしました。プロファイルの名前は、空のパイプを読んで待っている tshark のプロセスの `/proc/<pid>/attr` から読みます。tshark が読めなかったときは、元の pcap と写しの所有者とモード（`ls -ln`）も表示します。runner では `tshark (enforce)` と出るはずです。違う値が出れば、この原因の見立てが誤りです。README の「うまく動かないとき」には、同じエラーと、`/tmp` に写して読む方法を足しました。

この環境で実行した結果です。この環境のカーネルには AppArmor が無いので、CI の失敗は再現できませんでした。直したあとのスクリプトで、2 つの構成とも通ることだけを確かめました。

| 実行したこと | 結果 |
| --- | --- |
| `e2e-kind.sh external-dns` | 成功。68 の確認がすべて通過。表示は `uid 0, AppArmor profile: none` |
| `e2e-kind.sh --keep cluster-dns` | 成功。68 の確認がすべて通過。残したクラスタで、元の pcap と写しがどちらも 0:0、0644 であることを `ls -ln` で見てから、クラスタを消した |

## 10. 設計の選択

| 決めること | 採った案 | ほかの案 |
| --- | --- | --- |
| マニフェストの道具 | Kustomize（2 章） | Helm |
| SIP と Diameter の Service | headless | ClusterIP（4 章の問題） |
| SIP のノードの種類 | StatefulSet | Deployment（台を増やすと名前が決まらない） |
| Pod のアドレスを設定に入れる方法 | 設定の読み込みで環境変数を展開する（`${POD_IP}` を downward API の値に）。作っていない | 今の init コンテナと sed |
| 設定とゾーンの写し | `go generate` で作る写しと、それを確かめる `sync_test.go`（2 章） | load restrictor を外して `kubectl kustomize` を使う |
| DNS の既定 | (b) external-dns。Compose と同じ形で、kube-system に触らない。未決の論点でもある（12 章） | (a) cluster-dns。名前解決が 1 段で、クラスタの運用に近い |
| キャプチャ | ノードの DaemonSet（`-i any -Q in`）を既定にし、1 つの Pod を見るときはエフェメラルコンテナ | Pod ごとのサイドカー（pcap が Pod の数だけできる） |
| クラスタの外の UE | hostPort と `host_access` の overlay（6 章で試したもの） | NodePort と RTP の範囲の変更、extraPortMappings |
| 台を増やすとき | Pod 名から SIP の host と Origin-Host を作り、ゾーンに Pod ごとの CNAME を置く。作っていない | CoreDNS の rewrite で名前を写す |

## 11. 残っている作業

この調査のあとに残っている作業です。

| 作業 | 場所 | 前提 |
| --- | --- | --- |
| 設定ファイルで環境変数を展開し、init コンテナを無くす | `internal/config`、`deploy/k8s` | なし |
| EPC が P-CSCF の名前を接続のたびに引く。UE の Pod のアドレスを EPC に知らせる方法を決める | `internal/epc`、`internal/config` | 12 章の 4 |
| kind の e2e を CI で動かす（案は `deploy/k8s/ci/kind-e2e.yml.proposed`） | `.github/workflows/` | 12 章の 5 |
| クラスタの外の UE のための overlay（hostPort と `host_access`） | `deploy/k8s` | 12 章の 3 |
| ゾーンを持たない構成 (c)。P-CSCF が I-CSCF を設定で知る | `internal/pcscf`、`internal/config`、`deploy/k8s` | なし |
| 台を増やす構成。Pod ごとの SIP の host と Origin-Host、ゾーンの Pod ごとの名前 | `deploy/k8s`、`internal/config` | 環境変数の展開、役割の状態を外に置く Store の形 |
| Kubernetes で動かす手順書とキャプチャの手順（英語と日本語） | `docs/guide/` | なし |
| Compose の側の DNS の 2 構成（Docker のネットワークの DNS だけ、外付けの DNS サーバー） | `compose.yaml`、`deploy/compose` | なし |
| すべての overlay を `kubectl kustomize` でビルドし、kubeconform で確かめる。スクリプトは `deploy/k8s/check-overlays.sh`。CI のジョブの案は `deploy/k8s/ci/kustomize.yml.proposed` | `deploy/k8s`、`.github/workflows/` | なし |

## 12. 未決の論点

1. DNS の既定の構成を (b) external-dns にするか、(a) cluster-dns にするか。(b) は Compose と同じ形で、kube-system に触りません。(a) は名前解決が 1 段で済み、クラスタの運用に近い形です。
2. Pod のアドレスを設定に入れる方法として、設定ファイルで環境変数を展開してよいか。今は init コンテナと sed を使っています。
3. クラスタの外の UE を扱うか。扱うなら hostPort にするか、NodePort にするか。
4. EPC の加入者の静的なアドレスを Kubernetes でどう模擬するか。UE が接続するときに自分のアドレスを EPC に伝え、EPC がそれを払い出したことにする案と、Multus などで Pod にアドレスを付ける案があります。
5. kind の e2e を CI に入れるか。入れると CI の時間が延びます。

## 確かめられなかったこと

- kubernetes.io と kind.sigs.k8s.io にはプロキシで届きませんでした（403）。どちらも GitHub にある原稿を読みました。公開されている版と違う所があるかもしれません。
- CoreDNS の file プラグインが、ゾーンの外を指す CNAME をたどることは、README に書かれていません。kind（CoreDNS v1.14.6 と v1.12.1）で動くことだけを確かめました。
- PCO で P-CSCF のアドレスを渡す条項（TS 24.229 と TS 24.008）は読んでいません。
- Pod を作り直したときに、UDP の conntrack の古い項目が残って届かなくなる問題は、headless の構成では起きませんでした。ClusterIP の構成で Pod を作り直す試験はしていません。
- Docker Desktop（Windows、macOS）の kind は試していません。extraPortMappings と、ホストから見た送信元のアドレスは確かめていません。
- 複数ノードの kind は試していません。ノードをまたぐ通信では、CNI の経路とキャプチャの見え方が変わるはずです。
- プロジェクトの Dockerfile でのイメージのビルドは、この環境ではプロキシの CA のため通りませんでした。ホストで作ったバイナリを入れたイメージで試しています。このため、`e2e-kind.sh` がイメージを自分で作る道と、CI のジョブの案は、どちらも動かしていません。
- `e2e-kind.sh` の `docker save --platform` は、このホストの Docker 29.10.0 で通りました。古い Docker でこの指定が使えないときは、指定なしで保存し直します。その道は試していません。
- pjsua と SIPp（`images/peer`）は Kubernetes に載せていません。
- ダッシュボードの画面をブラウザで開いて見てはいません。API と HTML の応答だけを確かめました。
- runner で tshark が拒まれたことは、手元の環境では再現できませんでした。手元のカーネルには AppArmor がありません。tshark のプロファイルが原因だという見立ては、プロファイルの中身、パッケージ、kind の作り、Compose との違いから組み立てたものです。次の CI の実行で、`e2e-kind.sh` が表示するプロファイルの名前を見て確かめます。
- 失敗した実行の成果物とジョブのログの全文は、取ってこられませんでした。置き場所の `productionresultssa3.blob.core.windows.net` に、プロキシが 403 を返しました。ジョブのログは GitHub の API から読みました。成果物はどちらも 22 個のファイルで、数を数えると pcap が入っていないようにも見えます。ただ、上のとおり `kubectl cp` の tar は拒まれないはずで、中身を見られないので確かめていません。
- Ubuntu 26.04 の `apparmor` の版を Launchpad の API で調べようとしましたが、`api.launchpad.net` はプロキシに拒まれました（403）。`archive.ubuntu.com` のパッケージの一覧とパッケージそのもので確かめました。
