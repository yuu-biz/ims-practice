# 障害の注入

[English](README.md)

このディレクトリの道具で、IMS をわざと壊して、何が起きるかを観察できます。対象は、ノードの停止、ノードの凍結、パケットの損失、2 つのノードのあいだの分断です。

IMS のコンテナは `compose.yaml` のままで、何も変えません。ネットワークの障害は、使い捨てのコンテナが入れます。このコンテナは、対象のノードのネットワーク名前空間に入り、`NET_ADMIN` の権限で規則を足して、すぐに消えます。`fault.sh` は、どのコンテナにも Docker のソケットを渡しません。ソケットを持つのは、ダッシュボードから障害を入れるための障害注入のエージェントだけです。それも、専用の上書きのファイルで起動したときだけです（[障害注入のエージェント](#障害注入のエージェント)）。

どのノードも対象にできます。`compose.yaml` のノードも、[deploy/interconnect](../interconnect/compose.yaml) の 2 つ目の網のノードも、ほかの上書きのファイルのノードも同じです。`fault.sh` は、Docker Compose がコンテナに付けるラベルでコンテナを探します。そのため、ノードの一覧を持って直し続ける必要はありません。

## 始める前に

- [キャプチャの手順書](../../docs/guide/ja/capture.md)のとおりに、システムが動いていること。
- 道具のイメージを、リポジトリのルートで一度だけ作ります。

  ```sh
  docker compose -f compose.yaml -f deploy/chaos/chaos.yaml build fault-tools
  ```

- 以下のコマンドは、どれもリポジトリのルートで実行します。Git Bash では、`deploy/chaos/fault.sh` が `MSYS_NO_PATHCONV=1` を設定します。
- `fault.sh` は、`docker compose` と同じく、`COMPOSE_FILE` の構成を相手にします。IBCF でつないだ 2 つの IMS の網では、起動したときと同じファイルを指定します。

  ```sh
  export COMPOSE_FILE=compose.yaml:deploy/interconnect/compose.yaml:deploy/chaos/chaos.yaml
  ```

## 対象の選び方

| 対象 | 例 | 指すもの |
| --- | --- | --- |
| サービス | `mmtel`、`epc`、`volte-a`、`ibcf-a`、`peer-scscf` | そのサービスの 1 つのコンテナ。 |
| `docker compose run` の UE | `ue3` | `docker compose run ue3 ...` で起動し、動いているコンテナ。終わった UE は対象になりません。 |
| 複数のうちの 1 つのコンテナ | `agw:2` | 複数のコンテナで動くサービス（`docker compose up --scale agw=2`）の 2 つ目。番号を書かないと、`fault.sh` がコンテナを並べて、どれかを選ぶように求めます。 |

分断には相手を書きます。相手に書けるのは、サービス（そのすべてのコンテナ）、`agw:2` のような 1 つのコンテナ、`'scscf-*'` のような名前のパターン（名前が合うすべてのサービス）、IPv4 のアドレスです。

ネットワークの障害は、対象のすべてのインタフェースに入ります。`--network NET` を付けると、Compose のネットワーク `NET` の側のインタフェースだけに入ります。`deploy/interconnect` の IBCF と TrGW には、インタフェースが 2 つあります。自分の網の側と、2 つの網をつなぐ `nni` の側です。

## 障害の種類

| コマンド | 起きること | 元に戻す方法 |
| --- | --- | --- |
| `fault.sh stop scscf` | `docker stop`。ノードは SIGTERM を受け、別れの挨拶をしてから終わります。 | `fault.sh start scscf` |
| `fault.sh kill scscf` | `docker kill`。ノードは SIGKILL を受け、その場で止まります。 | `fault.sh start scscf` |
| `fault.sh crash scscf` | ノードのプロセスが落ちます（SIGQUIT）。`restart: unless-stopped` なので、Docker がすぐに起動し直します。 | 戻す操作は要りません |
| `fault.sh recreate scscf` | コンテナを kill して消し、同じ名前と同じアドレスで作り直します。落ちたノードをオーケストレーターが置き換えるときと同じです。ノードがメモリに持っていたものは消えます。 | 戻す操作は要りません |
| `fault.sh pause scscf` | `docker pause`。ノードが凍ります。ソケットは開いたままですが、何も答えません。 | `fault.sh unpause scscf` |
| `fault.sh loss agw 10` | ノードが送る IP パケットの 10 % と、受ける IP パケットの 10 % を捨てます。 | `fault.sh clear agw` |
| `fault.sh partition pcscf icscf` | ノードは、相手に届かず、相手からも聞こえなくなります。相手を書かなければ、すべてから切り離します。 | `fault.sh clear pcscf` |
| `fault.sh partition --network nni ibcf-a` | IBCF を、2 つの網をつなぐ回線から切り離します。自分の網からは届いたままです。 | `fault.sh clear ibcf-a` |
| `fault.sh delay pcscf 200 20` | ノードが送るパケットを 200 ms 遅らせます。ずれの幅は ±20 ms です（netem）。 | `fault.sh clear pcscf` |

`fault.sh show <対象>` は、ノードのネットワークの障害を、規則ごとに捨てたパケットの数と一緒に表示します。`fault.sh heal` は、構成のすべてのコンテナの障害をまとめて戻します。ノードを再起動しても、そのノードのネットワークの障害は消えます。コンテナに新しいネットワーク名前空間ができるためです。

どの障害も、実行する `docker` のコマンドを先に表示します。同じコマンドを自分で打てます。ネットワークの障害は、最初にノードの名前空間で `ip -o -4 addr show` を実行し、ノードのアドレスを持つインタフェースを探します。

### 捨てたパケットはどこに写るか

キャプチャのコンテナは、ノードをつなぐブリッジを記録します（[キャプチャの手順書](../../docs/guide/ja/capture.md)）。ネットワークの障害は、ノードとブリッジのあいだ、つまりノード自身のネットワークインタフェースに置きます。

- ノードが送って捨てられたパケットは、ブリッジに届きません。キャプチャにはありません。
- ノードが受けて捨てられたパケットは、すでにブリッジを通っています。キャプチャにはありますが、ノードには届いていません。

ダッシュボードの見え方は違います。各ノードは、送受信した SIP と Diameter のメッセージの写しをダッシュボードに送ります。障害はこの写しを通します。そのため、ダッシュボードはノードが思っていることを表示します。ノードが送ったのに障害で捨てられたメッセージは、ダッシュボードにはあり、キャプチャにはありません。

## 練習

[キャプチャの手順書](../../docs/guide/ja/capture.md)のとおりにシステムを起動し、Wireshark でキャプチャを開きます。登録と発信のしかたは、[登録](../../docs/guide/ja/registration.md)と[通話](../../docs/guide/ja/call.md)の手順書にあります。

### S-CSCF を止める、殺す、凍らせる

1. `deploy/chaos/fault.sh stop scscf`。フィルタ `diameter` で見ると、S-CSCF が Disconnect-Peer Request（DPR）を送り、HSS が DPA を返します。そのあと TCP の接続が閉じます（FIN）。`fault.sh start scscf` で、新しい CER と CEA が続きます。
2. `deploy/chaos/fault.sh kill scscf`。今度は DPR がありません。止まったプロセスの TCP の接続を、カーネルが FIN で閉じます。kill したノードを Docker は起動し直しません。`fault.sh start scscf` で起動します。
3. `deploy/chaos/fault.sh crash scscf`。S-CSCF のログ（`docker compose logs scscf`）が `SIGQUIT: quit` とすべての goroutine の一覧で終わり、すぐにノードが戻ります。`docker inspect -f '{{.RestartCount}}' ims-practice-scscf-1` の値が 1 つ増えています。

### P-CSCF が凍っているあいだに登録する

```sh
deploy/chaos/fault.sh pause pcscf
docker compose run --rm ue register --deregister-after 2s
```

数秒たったら、別の端末で `deploy/chaos/fault.sh unpause pcscf` を実行します。フィルタ `sip.Method == "REGISTER"` で見ると、UE は 0.5 秒、1 秒、2 秒の間をあけて REGISTER を送り直しています。間は毎回 2 倍になります。RFC 3261 17.1.2.2 のタイマー E です。Wireshark は送り直しに `sip.resend == 1` の印を付けます。凍結が解けると、P-CSCF は REGISTER に一度だけ答えます。ソケットに溜まっていた写しは、同じトランザクションのものだからです。

### HSS が凍っているあいだに登録する

```sh
deploy/chaos/fault.sh pause hss
docker compose run --rm ue register --deregister-after 2s
deploy/chaos/fault.sh unpause hss
```

フィルタ `diameter || sip.Status-Code >= 400` で見ます。I-CSCF が User-Authorization Request（UAR）を送りますが、答えがありません。I-CSCF は 5 秒で諦めて P-CSCF に 480 を返し、P-CSCF は UE に 504 Server Time-out を返します。そのあいだ、I-CSCF、S-CSCF、IP-SM-GW の Device-Watchdog Request（DWR）にも DWA が返りません。IP-SM-GW は接続を閉じ、HSS が戻ってから新しい接続を開きます（CER と CEA）。HSS は戻ったあとで、もう誰も待っていない UAA も送ります。

### 通話中にメディア中継ノードでパケットを失う

```sh
deploy/chaos/fault.sh loss agw 10
```

発信して、終わったら `deploy/chaos/fault.sh clear agw` を実行します。Wireshark で **Telephony > RTP > RTP Streams** を選び、中継ノード（172.28.0.50）から着信側へのストリームを分析します。失ったパケットの数と、シーケンス番号の抜けが分かります。失ったパケットには、キャプチャにあるもの（中継ノードが受けて捨てたもの）と、無いもの（中継ノードが送って捨てたもの）があります。通話そのものはつながります。

### S-CSCF でパケットを失う

```sh
deploy/chaos/fault.sh loss scscf 20
```

登録と発信を何度か行ってから、障害を戻します。`sip.resend == 1` と `tcp.analysis.retransmission` で探すと、失ったものをノードが送り直しているのが分かります。S-CSCF は DNS の問い合わせも失います。ノードは答えを 2 秒待ち、問い合わせ直しません。そのため、ENUM や NAPTR の問い合わせを 1 つ失うと、発信が 404 で失敗することがあります。損失を P-CSCF に入れたときは、問い合わせを失うと、登録が 504 で失敗します。理由はログの `cannot route the request` と `i/o timeout` に出ます。

### P-CSCF を I-CSCF から切り離す

```sh
deploy/chaos/fault.sh partition pcscf icscf
docker compose run --rm ue register --deregister-after 2s
deploy/chaos/fault.sh clear pcscf
```

キャプチャには、UE の REGISTER と、その送り直しがあります。送り直しの間は、4 秒（T2）まで広がります。32 秒たつと（タイマー F）、UE は諦めます。同じ時に P-CSCF も 504 を返しますが、間に合いません。P-CSCF から I-CSCF へのパケットは、キャプチャに 1 つもありません。一方、ダッシュボードには、P-CSCF が I-CSCF に REGISTER を何度も送ったことが出ています。

### S-CSCF を HSS から切り離す

```sh
deploy/chaos/fault.sh partition scscf hss
docker compose run --rm ue register --deregister-after 2s
```

REGISTER には、5 秒後に 500 が返ります。S-CSCF が送った Multimedia-Auth Request（MAR）は、ダッシュボードにはあり、キャプチャにはありません。分断を 30 秒ほどそのままにして、`docker compose logs hss` の `peer state` を読みます。RFC 3539（3.4）の監視（watchdog）が、接続を `OKAY` から `Pending` に移しています。`fault.sh clear scscf` のあと、S-CSCF は新しい接続を開き（SYN、CER、CEA）、次の登録は成功します。

### 通話中に MMTel AS を凍らせる

```sh
docker compose run -d --name ue2-answer ue2 answer --impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2 --ring 1s --tone 6s
docker compose run --rm ue call --tone 6s tel:+12025550102
```

通話のあいだに、別の端末で `deploy/chaos/fault.sh pause mmtel` を実行し、10 秒ほどたってから `deploy/chaos/fault.sh unpause mmtel` を実行します。音声は続きます。RTP は AS ではなく、メディア中継ノードを通るためです。UE 1 が切ると、BYE は AS に届きますが、答えがありません。フィルタ `sip.Method == "BYE"` で見ると、UE、P-CSCF、S-CSCF が、0.5 秒、1 秒、2 秒、4 秒の間をあけて BYE を送り直しています（タイマー E）。AS は凍結が解けると、ソケットで待っていた写しのそれぞれに 200 を返します。そのあとで初めて、呼のもう片方の脚で UE 2 に自分の BYE を送ります（AS は B2BUA です）。最後に `docker rm -f ue2-answer` を実行します。

### 相手の網への通話中に IBCF を殺す

2 つの IMS の網を起動し（[網間接続の手順書](../../docs/guide/ja/interconnect.md)）、[始める前に](#始める前に)のとおりに `COMPOSE_FILE` を設定します。網 B の UE B1 が着信を待ち、UE A1 が発信します。

```sh
b1=(--domain ims.mnc002.mcc001.3gppnetwork.org --pcscf 172.28.1.40:5060
  --pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060 --sc +12025550150
  --impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org
  --impu sip:001020000000001@ims.mnc002.mcc001.3gppnetwork.org --password test-password-b1)
docker compose run -d --name b1-answer peer-ue answer "${b1[@]}" --ring 1s --tone 12s
docker compose run --rm ue call --tone 10s tel:+12025550151
```

通話のあいだに `deploy/chaos/fault.sh kill ibcf-a` を実行し、3 秒後に `deploy/chaos/fault.sh start ibcf-a` を実行します。音声は続きます。回線のキャプチャ（`captures/nni/`）を見ると、TrGW のあいだの RTP に途切れがありません。止まったのは IBCF だけだからです。最後の BYE は、起動し直した IBCF A を通り、両方の網で呼を終わらせます。IBCF はプロキシで、BYE の行き先は BYE の Route に書いてあるためです。ただし、新しい IBCF A は、前の IBCF A が持っていた呼の記録を失っています。そのため、TrGW A のこの呼のコンテキストを、誰も解放しません。

```sh
docker compose exec trgw-a wget -qO- http://127.0.0.1:8080/contexts
```

通話のあとでも、このコンテキストが一覧に残っています。IBCF が動き続けていた TrGW B には、何も残っていません。

### 2 つの網のあいだの回線を切る

```sh
docker compose run -d --name b1-answer peer-ue answer "${b1[@]}" --ring 1s --tone 30s
docker compose run --rm ue call --tone 6s tel:+12025550151
```

通話が始まって数秒たったら、別の端末で、網 A の側の回線を、信号とメディアの両方について切ります。

```sh
deploy/chaos/fault.sh partition --network nni ibcf-a
deploy/chaos/fault.sh partition --network nni trgw-a
```

網 A からは、どちらのノードにも、もう片方のインタフェースで届いたままです。回線のキャプチャでは、TrGW B から TrGW A への RTP が続きます（TrGW A がブリッジのあとで捨てています）。網 A からは何も来なくなります。IBCF B は 15 秒ごとに IBCF A へ TCP の keep-alive を送りますが、答えがありません。UE A1 が切ると、MMTel AS はその BYE にすぐ 200 を返し、網 B へ向けて自分の BYE を送ります。網 A のキャプチャをフィルタ `sip.Method == "BYE" || sip.CSeq.method == "BYE"` で見ると、S-CSCF が IBCF A に BYE を何度も送り直し、32 秒後（タイマー F）に IBCF A が 504 を返します。UE B1 には BYE が届かず、網 B の側の呼は続いたままです。

回線を切っているあいだに新しくかけた呼には、IBCF A から 100 Trying が返り、約 34 秒後に 504 Server Time-out が返ります。INVITE が 1300 バイトより大きいので、IBCF A はまず IBCF B に TCP で送ろうとし、2 秒で諦めて UDP で送ります。そのあとタイマー B がトランザクションを終わらせます。`deploy/chaos/fault.sh show ibcf-a` で、規則ごとに捨てたパケットの数が分かります。`deploy/chaos/fault.sh clear ibcf-a` と `deploy/chaos/fault.sh clear trgw-a` で戻すと、次の呼はつながります。最後に `docker rm -f b1-answer` を実行します。

### 同じ役割の複数の台と、共有の保存先

冗長構成では、同じ役割の台を、`scscf-1` と `scscf-2` のように別々のサービスとして動かします。台は状態を共有の保存先（たとえばサービス `valkey`）に置きます。どれも、ほかのノードと同じように対象にできます。

- `fault.sh recreate pcscf-1` は、P-CSCF の 1 台を置き換えます。ほかの台と保存先が状態を持ち続けます。
- `fault.sh kill valkey` のあとで `fault.sh start valkey` を実行すると、保存先が起動し直します。ボリュームに追記のファイル（AOF）があれば、データを読み直します。永続化が無ければ、空で起動し、どの台もそこに置いた状態を失います。
- `fault.sh partition valkey 'scscf-*'` は、保存先を S-CSCF のすべての台から切り離し、ほかの役割はそのままにします。

### ノードをネットワークから外す

Docker は、コンテナをネットワークから丸ごと外すこともできます。

```sh
docker network disconnect ims-practice_ims ims-practice-icscf-1
docker network connect --ip 172.28.0.20 ims-practice_ims ims-practice-icscf-1
```

つなぎ直すときは、アドレスを指定し直します。アドレスは[キャプチャの手順書](../../docs/guide/ja/capture.md)にあります。I-CSCF が外れているあいだ、キャプチャには、誰も答えない 172.28.0.20 の ARP の問い合わせと、P-CSCF の REGISTER の送り直しと、HSS の TCP の再送が写ります。I-CSCF の側では、HSS への接続が失敗します。ログで監視の状態が `Closed` まで下がり、つなぎ直したあとで `I-Open` に戻るのが分かります。

## 遅延には netem が要る

`fault.sh delay` は `tc qdisc ... netem` を使います。Docker を動かしているカーネルに、`sch_netem` のモジュールが要ります。`Error: Specified qdisc kind is unknown.` と出たら、そのカーネルには netem が無く、この障害は使えません。損失と分断は nftables を使うので、netem は要りません。

## 権限と Docker Desktop

- IMS のコンテナには、権限も道具も足しません。`NET_ADMIN` を持つのは、使い捨ての `fault-tools` のコンテナだけです。それも、ノードの名前空間の中で、コマンド 1 つのあいだだけです。
- キャプチャのサービス（`capture` と、`deploy/interconnect` の `capture-peer` と `capture-nni`）へのネットワークの障害は断ります。これらのコンテナはホストのネットワークを共有しているので、規則がホストを変えてしまうためです。
- 損失と分断の規則は、nftables の egress のフックを使います。カーネルが `CONFIG_NETFILTER_EGRESS` 付きで作られている必要があります。`fault.sh loss` が `egress` のチェインで失敗するなら、そのカーネルにはこのフックがありません。Docker Desktop でも、ノードの障害（`stop`、`kill`、`pause`、`crash`）は Docker のコマンドだけで動きます。ネットワークの障害は、Docker Desktop が動かす Linux の仮想マシンのカーネルしだいです。この手順書では試していません。そのカーネルに egress のフックや netem が無いかどうかは、上の 2 つのエラーで分かります。

## 障害注入のエージェント

`fault.sh` は、端末からノードを壊します。障害注入のエージェント（fault agent）は、同じことをダッシュボードのために行います。学習者がダッシュボードの画面からノードを壊し、その結果を信号と並べて見られるようにするためです。エージェントは、`internal/fault` の HTTP の API を専用のコンテナ（`agent.yaml`）で動かし、`fault.sh` と同じように、Compose のプロジェクトのコンテナに障害を入れます。エージェントが入れる障害には必ず期限があり、時間が来ると自動で戻ります。

### 明示したときだけ起動する理由

エージェントは、Docker のソケット `/var/run/docker.sock` を通して Docker Engine を操作します。このソケットを使える者は、ホストのディスクとネットワークをつないだコンテナを起動できます。つまり、ホストの root と同じことができます。そのため、次のようにしています。

- ソケットを渡すのは `agent.yaml` だけで、渡す先はエージェントだけです。`compose.yaml` は `agent.yaml` を読み込みません。IMS のノードには、ソケットを渡しません。`compose.yaml` か `deploy/` の上書きのファイルで、ほかのサービスがソケットをつなぐと、`go test ./internal/fault` が失敗します。
- エージェントは、専用のネットワーク `chaos`（172.28.9.0/24）だけにつなぎます。このネットワークにいるのは、エージェントのほかはダッシュボードだけです。IMS のノードからは届きません。エージェントの HTTP は、`ims0` のキャプチャにも入りません。
- API にはパスワードがありません。API に届く者は、プロジェクトのどのコンテナでも止められます。そのため、ホストに公開するポートは、`curl` のための `127.0.0.1:8070` だけです。ホストの外からは届きません。
- エージェントは、Docker のソケットを持つコンテナへの障害を断ります。エージェント自身も含みます。止まったり凍ったりしたエージェントは、入れた障害を期限で戻せなくなるためです。`fault.sh` と同じく、キャプチャのコンテナへのネットワークの障害も断ります。

### 起動と停止

リポジトリのルートで、道具のイメージを一度だけ作り、エージェントと一緒に IMS を起動します。

```sh
docker compose -f compose.yaml -f deploy/chaos/chaos.yaml build fault-tools
docker compose -f compose.yaml -f deploy/chaos/agent.yaml up -d
curl http://127.0.0.1:8070/fault/v1/faults
```

IMS がすでに動いているときに同じ `up -d` を実行すると、エージェントが起動し、ダッシュボードだけが作り直されます。ダッシュボードはネットワーク `chaos` にもつながります。この構成では、`docker compose` のコマンドのたびに 2 つのファイルを指定します。`export COMPOSE_FILE=compose.yaml:deploy/chaos/agent.yaml` で一度に設定しても構いません。

エージェントは、障害の記録をメモリにだけ持ちます。止める前に、障害を戻すか、期限が来るのを待ってください。エージェントが入れた障害は、エージェントを止めても残ります。残ったときは `deploy/chaos/fault.sh heal` で戻します。IMS を動かしたままエージェントだけを止めるには、`agent.yaml` を指定せずに起動し直し、ネットワークを消します。エージェントが消え、ダッシュボードはネットワーク `chaos` から外れます。

```sh
docker compose -f compose.yaml up -d --remove-orphans
docker network rm ims-practice_chaos
```

すべてを止めるときは `docker compose -f compose.yaml -f deploy/chaos/agent.yaml down` を実行します。

エージェントの設定は `fault-agent.yaml` です。待ち受けるアドレス、ソケットの場所、操作してよい Compose のプロジェクト（`compose.yaml` で付けた名前の `ims-practice`）、道具のイメージを書きます。別のプロジェクト名（`docker compose -p`）で起動するときは、そこの `compose_project` を変えます。

### ノードを壊すことと、運用から外すこと

ノードのドレインは、障害ではなく保守の操作です。新しい登録と呼をほかの台に回し、通話中の呼は終わるまで続けます。これは OAM が、ノードの管理状態を変えて行います（`internal/oam`）。障害注入のエージェントにはドレインがなく、OAM は障害を入れません。

### HTTP の API

ダッシュボードからは `http://172.28.9.10:8080`、ホストからは `http://127.0.0.1:8070` で API に届きます。本文は JSON で、応答にはすべて `Content-Type: application/json` が付きます。時刻は UTC の RFC 3339 で、`"2026-11-20T23:57:48.449Z"` のように書きます。時間の長さは Go の形の文字列で、`"200ms"`、`"20s"`、`"5m0s"` のように書きます。

| メソッドとパス | 本文 | 応答 |
| --- | --- | --- |
| `GET /fault/v1/faults` | | 200 と障害の配列。古いものが先です。終わった障害も、終わってから 1 時間は一覧に残ります |
| `POST /fault/v1/faults` | 要求（下の表） | 201 と、状態が `active` の障害。`Location: /fault/v1/faults/{id}` が付きます |
| `GET /fault/v1/faults/{id}` | | 200 と障害 |
| `DELETE /fault/v1/faults/{id}` | | 200 と、状態が `cleared` の障害。すでに終わった障害には、その状態のまま 200 で答えます |

`POST` の要求です。

| フィールド | 型 | 使う種類 | 意味 |
| --- | --- | --- | --- |
| `target` | 文字列 | すべて | Compose のサービス（`pcscf`、`ibcf-a` など）か、複数のコンテナを動かすサービスの 1 つのコンテナ（`agw:2` など）。使える文字は、英小文字、数字、`_`、`.`、`-` です |
| `kind` | 文字列 | | `stop`、`kill`、`pause`、`delay`、`loss`、`partition` のどれか。中身は[障害の種類](#障害の種類)のとおりです |
| `network` | 文字列 | `delay`、`loss`、`partition` | 障害を入れるインタフェースの Compose のネットワーク（`nni` など）。書かなければ、対象のすべてのインタフェースに入れます |
| `delay` | 時間の長さ | `delay` | 0 より長く、`10s` 以下 |
| `jitter` | 時間の長さ | `delay` | 0 から `delay` まで |
| `loss_percent` | 整数 | `loss` | 1 から 100。送るパケットと受けるパケットのそれぞれを捨てる確率です |
| `peers` | 文字列の配列 | `partition` | 対象が届かなくなり、聞こえなくなる相手。サービス、コンテナ（`agw:2`）、パターン（`scscf-*`）、IPv4 のアドレスで書きます。書かなければ、すべての相手です。同じ相手を 2 回書くことと、対象自身を書くことはできません |
| `duration` | 時間の長さ | すべて | `1s` から `1h` まで。書かなければ `5m` です |

ほかの種類のフィールドと、表に無いフィールドは断ります。例を示します。

```sh
curl -X POST -d '{"target":"pcscf","kind":"pause","duration":"20s"}' http://127.0.0.1:8070/fault/v1/faults
curl -X POST -d '{"target":"agw","kind":"loss","loss_percent":10,"duration":"2m"}' http://127.0.0.1:8070/fault/v1/faults
curl -X POST -d '{"target":"ibcf-a","kind":"partition","network":"nni","duration":"1m"}' http://127.0.0.1:8070/fault/v1/faults
```

障害は、要求（`duration` を補ったもの）に次のフィールドを足したものです。

| フィールド | 意味 |
| --- | --- |
| `id` | 16 進数の 8 桁 |
| `state` | エージェントが処理しているあいだは `applying` と `clearing`、障害が入っているあいだは `active`。終わると、`cleared`（`DELETE` で戻した）、`expired`（期限が来た）、`failed` のどれかになります |
| `error` | 失敗した理由。状態が `failed` のときだけ付きます |
| `created_at`、`expires_at` | 要求を受けた時刻と、自動で戻る時刻 |
| `ended_at` | 終わった時刻。終わったときだけ付きます |

```json
{"id":"113e54e0","target":"pcscf","kind":"pause","duration":"5s","state":"active",
 "created_at":"2026-11-20T23:57:48.449036798Z","expires_at":"2026-11-20T23:57:53.449036798Z"}
```

エージェントは、`POST` と `DELETE` に、変更が終わってから答えます。`stop` は、ノードが終わるまで待ちます。待つのは最長で、`compose.yaml` の `stop_grace_period`（5 秒）です。ネットワークの障害では、ノードの名前空間で使い捨てのコンテナをいくつか動かすので、1 秒ほどかかります。答えが来るまで、要求を切らないでください。エージェントは期限の来た障害を 1 秒ごとに探すので、障害が戻るのは `expires_at` から最大 1 秒あとです。戻った時刻は `ended_at` で分かります。

誤りの応答の本文は、どれも `{"error": "..."}` です。

| 状態コード | 返すとき |
| --- | --- |
| 400 | 要求が上の表に合わないとき。知らない種類、ほかの種類のフィールド、範囲の外の値、知らないフィールド、1 つの JSON のオブジェクトでない本文、16 KiB を超える本文です |
| 404 | その id の障害が無いとき、または終わってから 1 時間を過ぎたとき。`/fault/v1/` の下のほかのパスにも 404 を返します |
| 405 | そのパスが受けないメソッドのとき。ヘッダ `Allow` に、受けるメソッドを並べます |
| 409 | `POST` では、対象にまだ終わっていない障害があり、新しい障害とぶつかるとき。`stop`、`kill`、`pause` は互いにぶつかります。止まったノードは、殺すことも凍らせることもできないためです。`delay`、`loss`、`partition` は、それぞれ同じ種類とだけぶつかります。`DELETE` では、その障害を入れている途中か、戻している途中のとき |
| 500 | Docker で実行できなかったとき。本文には `fault` も入ります。状態が `failed` の障害で、一覧にも残ります。たとえば、対象のコンテナが無い、複数のコンテナを持つサービスに番号が無い、キャプチャのコンテナへのネットワークの障害、エージェントへの障害、道具のイメージを作っていない、カーネルに netem（`delay`）か egress のフック（`loss`、`partition`）が無い、といった場合です。`DELETE` が失敗したときは、障害が残っているかもしれません。`deploy/chaos/fault.sh heal` で戻します |

```text
{"error":"executor failed: no container of the service \"nosuch\" in the project \"ims-practice\"",
 "fault":{"id":"febb1f33","target":"nosuch","kind":"stop","duration":"5s","state":"failed",
  "error":"apply: no container of the service \"nosuch\" in the project \"ims-practice\"", ...}}
```

`crash` と `recreate` は `fault.sh` にだけあります。どちらもすぐに終わり、戻すものが無いので、期限のある障害にはしていません。
