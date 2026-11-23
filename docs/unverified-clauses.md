# 確かめられなかった条項

条項番号を確かめられなかった記述の一覧です。手順書、コードのコメント、設定ファイル、設計の記録、調査の記録には、確かめた条項番号だけを書きます。条項を確かめられなかった記述は、そこでは仕様の番号だけを残し、この表に記録します。確かめたら、元の場所に条項番号を書き、この表から行を消します。


| 場所 | 記述 | 仕様 | 確かめること |
| --- | --- | --- | --- |
| `deploy/announcements/announcements.go` のパッケージのコメント | notice は特殊情報音（SIT）の上がっていく 3 つの音で、網の音声ガイダンスの前に流す。 | ITU-T E.180 | SIT の周波数と長さを定める条項の番号。 |
| `internal/epc/api.go` の `SystemInformation` | UE はセルから網、トラッキングエリア、セルの識別子を知る。 | TS 36.331 の SystemInformationBlockType1 | SystemInformationBlockType1 を定める条項の番号。 |
| `internal/fivegc/api.go` の `SystemInformation` | UE は NR のセルから網、トラッキングエリア、セルの識別子を知る。 | TS 38.331 の SIB1 | SIB1 を定める条項の番号。 |
| `deploy/interconnect/profiles/example-ttc-jj-90.30.yaml` の `transports` | SIP は UDP の 5060 番が必須。長いメッセージを TCP で送るかどうかは書かれていない。 | TTC JJ-90.30 第 13 版 4.1 | 4.1 の本文と、TCP の扱いを定める箇所。 |
| `deploy/interconnect/profiles/example-ttc-jj-90.30.yaml` の `methods` | MESSAGE が網のあいだを通るか。 | TTC JJ-90.30 第 13 版 | MESSAGE の扱いを定める条項。 |
| `deploy/interconnect/profiles/example-ttc-jj-90.30.yaml` の `option_tags` | 100rel は必須。プリコンディションは事業者のあいだで決める。 | TTC JJ-90.30 第 13 版 4.3.4.2、4.3.4.3 | 2 つの条項の本文。 |
| `deploy/interconnect/profiles/example-ttc-jj-90.30.yaml` の `number_format` | 発信者番号は P-Asserted-Identity で運び、tel URI の PAI は 1 つだけにする。Request-URI の番号の形。 | TTC JJ-90.30 第 13 版 4.3.4.1 | 4.3.4.1 の本文と、Request-URI の番号の形を定める条項。 |
| `deploy/interconnect/profiles/example-ttc-jj-90.30.yaml` の `codecs` | 網のあいだの offer に G.711 μ-law を必ず入れ、移動網どうしでは AMR も入れる。 | TTC JJ-90.30 第 13 版 4.3.5.1.4.1 | 4.3.5.1.4.1 の本文。 |
| `deploy/interconnect/profiles/example-ttc-jj-90.30.yaml` の冒頭と `docs/guide/{en,ja}/interconnect.md` の TS 29.165、GSMA IR.95、TTC の節 | すべてのセッションに 180 秒から 300 秒のセッションタイマーを掛ける。 | TTC JJ-90.30 第 13 版 4.3.4.8 | 4.3.4.8 の本文。www.ttc.or.jp が 503 を返したので、これまでの値は docs/background/profiles.md の Internet Archive の写しの記録による。 |
| `docs/guide/{en,ja}/interconnect.md` の TS 29.165、GSMA IR.95、TTC の節 | GSMA IR.95 が TS 29.165 をどう絞っているか。 | GSMA IR.95 | 本文を読む。www.gsma.com が 403 を返した。 |
| `docs/guide/en/mrf.md`、`docs/guide/ja/mrf.md` の「MRF とは」と、用語集の Mr / Mr' と Cr | Mr' は AS と MRFC の間の SIP、Mr は S-CSCF と MRFC の間の SIP、Cr は AS と MRFC の間で文書と音声を取ったりメディア制御の命令を交わしたりする参照点。 | TS 23.218 | 3 つの参照点を定める条項の番号。docs/background/mmtel-mrf.md は 8.2.2 と 9.2.4 を挙げているが、本文は読んでいない。 |
| `docs/design/state-redundancy.md` の「名前と DNS」の DNS の問い合わせ | `internal/sipdns` は、1 秒で答えの無い問い合わせを送り直し、3 回まで試す。 | 特定していない | 送り直しの回数の根拠にする条項。 |
| `docs/background/mmtel-mrf.md` の「Ut/XCAP の最小の形」 | GAA を使わない網で Ut に TLS が要る場面。TS 24.623 は、GAA を使わない網で ETSI TS 183 038 が TLS を求める箇所に TS 33.310 Annex E の TLS の型を求めている。 | TS 24.623 5.2.3.1、ETSI TS 183 038、TS 33.310 Annex E | どの手順が TLS を求めるか。ETSI TS 183 038 の該当の条項の番号。 |
| `docs/background/k8s.md` の「各ノードが書くアドレスと設定」 | PCO に P-CSCF のアドレスを入れる。 | TS 24.229、TS 24.008 | PCO で P-CSCF のアドレスを渡す条項の番号と本文。 |
| `docs/background/oam.md` の「役割ごとの指標の案」 | 登録の試行、成功、失敗の 3 つの測定に「2 out of 3 approach」を当てている。3 つのうち 2 つがあれば残りが分かる、という意味だと読める。 | TS 32.409 4.1.1.1、4.1.1.2、4.1.1.3.1 | 「2 out of 3 approach」の定義の箇所。 |
| `docs/background/redundancy.md` の 6 章「P-CSCF の reg イベントの購読」 | P-CSCF は、UE の再登録の 200 のたびに reg イベントの購読を更新する形にでき、更新のタイマーを持たなくてよい。 | TS 24.229 5.2.3 | 5.2.3 の本文が、この更新の仕方を許すか。 |
| `docs/background/5gc.md` の 1 章「仕様が求めること」 | IMS の DNN の名前は、NG.113 の「IMS well-known APN/DNN」が定める。値は `ims`。 | GSMA NG.113 | NG.113 の該当する節の番号と、値が `ims` であること。 |
| `docs/background/5gc.md` の 3 章の表（UAA の S-CSCF の能力） | S-CSCF の能力は ImsSDM_Get で `GET /{imsUeId}/ims-data/location-data/scscf-capabilities` を呼んで得る。 | TS 29.562 | この操作を定める節の番号。 |
| `docs/background/5gc.md` の 3 章の表（LIR と LIA） | LIR に当たる操作は ImsUECM_Authorize で、S-CSCF の能力は ImsSDM_Get で得る。`GET /{imsUeId}/ims-data/location-data/server-name` もある。 | TS 29.562 | 着信のときに何を送るかを定める節の番号。 |
| `docs/background/5gc.md` の 3 章の表（SAA の User-Data） | プロファイルは ImsSDM_Get で `GET /{imsUeId}/ims-data/profile-data` を呼んで得る。 | TS 29.562 | この操作を定める節の番号。 |
| `docs/background/5gc.md` の 3 章の表（PPR と PPA） | プロファイルの変更の通知は ImsSDM_Notification で、S-CSCF が申し込みで渡した URI への POST で届く。 | TS 29.562 | 申し込みと通知を定める節の番号。 |
| `docs/background/5gc.md` の 3 章の表（Sh の UDR と PUR） | リポジトリのデータの取得は `GET /{imsUeId}/repository-data/{serviceIndication}`、更新は同じ資源への PUT。 | TS 29.562 | これらの操作を定める節の番号。 |
| `docs/background/5gc.md` の 7 章「`internal/sdp` の EVS の扱い」 | answer の `br` と `bw` を、offer と自分の構成の共通部分にすると、NG.114 の answer の選び方の表と一致する。 | GSMA NG.114 3.2.2.3 の Table 10 | 表の組み合わせと、この操作が一致するか。 |
| `docs/background/5gc.md` の 7 章「このプロジェクトの EVS の方針」 | 端末が最初の answer に `evs-mode-switch=1` を書いてよいかについて、NG.114 が何か決めているか。 | GSMA NG.114 の EVS の SDP の節 | 該当する条項の番号と本文。 |
| `docs/background/cloud.md` の 1 章「開けるのは 2 つだけ」 | NAT 越しの IPsec には UDP のカプセル化が要り、その手順が TS 33.203 と TS 24.229 にある。 | TS 33.203、TS 24.229 | UDP のカプセル化を定める条項の番号。 |
| `docs/background/cloud.md` の 3 章「キープアライブと、NAT とロードバランサーの寿命」 | TS 24.229 に、UE と P-CSCF が NAT を越えるための付録がある。 | TS 24.229 の NAT 越えの付録 | 付録の番号と、UE と P-CSCF の手順。 |
| `docs/background/cloud.md` の 5 章「インターネットから引く名前」 | 3GPP は、PCO や DHCP で P-CSCF を見つける方法を定めている。 | TS 24.229 の P-CSCF の見つけ方 | 該当する条項の番号。 |
| `internal/config/config.go` の `checkMediaControl` の `SIMPLIFIED:` と、`internal/h248/README.md`、`README.ja.md` の同じ行 | コントローラは、持っているメディアゲートウェイとそのコンテキストを、ほかのコントローラに渡さない。実網では、コントローラがゲートウェイを別のコントローラへ移す手順（ServiceChangeMgcId を使う Handoff）と、ゲートウェイが自分でほかのコントローラへ移る手順がある。 | H.248.1 (05/2002)、TS 29.334 | コンテキストを持ったままの結び付きを別のコントローラへ移せるかどうかと、それを定める条項の番号。`internal/h248/mediactl/gateway.go` が挙げる 11.2 と 11.5 は、予備のコントローラの一覧についての条項で、本文は読んでいない。 |
| `cmd/ims/main.go` の `startMediaController` の `SIMPLIFIED:` と、`internal/h248/README.md`、`README.ja.md` の同じ行 | 止まるノードは、サービスを外れることを ServiceChange で相手に伝えない。実網のコントローラとゲートウェイは、Graceful か Forced の ServiceChange で伝える。 | H.248.1 (05/2002) | 止まる側が ServiceChange を送ることを定める条項の番号と、そのとき使う Method。`internal/h248/servicechange.go` は Graceful と Forced を持っているが、どの条項がこの場面を定めるかは読んでいない。 |
| `internal/h248/mediactl/profile.go` の `errorNames` の 502 | 502 の名前は「Not ready」。運用から外した媒体ノードが、コンテキストを作らないときに返す誤りの符号。 | H.248.8 (03/2013) 4.2 | 502 を定める条項の番号。名前は TS 29.334 5.7.10 の Table 5.7.10.2 で確かめたが、ほかの符号に添えてある 4.2 の枝番は読んでいない。 |
