---
title: このリポジトリのパッケージ構成
footer: ims-practice 勉強会 1. パッケージ構成
lang: ja
---

# このリポジトリのパッケージ構成

ims-practice 勉強会 第 1 回（どのノードとどの仕様が、コードのどこにあるか）

---

[layout: summary]

## この回で分かること

- 実行ファイルが 3 つだけで、その 1 つが IMS のどのノードにもなること。
- どのノードが、どのパッケージに書いてあるか。
- どの仕様が、どのパッケージに書いてあるか。
- コードをどこから読み始めればよいか。

> 信号の流れは次の回から読みます。この回は、そのときに開くファイルを探せるようにする回です。
> 全体の説明は [README.ja.md](../../../README.ja.md) に、略語は [用語集](../../../docs/glossary.md) にあります。

---

## リポジトリの一番上

| 場所 | 中身 |
| --- | --- |
| `cmd/` | 実行ファイルの入口。`ims`、`ue`、`oss` の 3 つ。 |
| `internal/` | 実装のすべて。この回の主題。 |
| `deploy/compose/` | ノードごとの設定ファイルと、DNS のゾーン。 |
| `compose.yaml` | ノードを 1 つの Docker のネットワークに並べる。 |
| `docs/` | 手順書（`docs/guide/`）と用語集。 |
| `scripts/` | シナリオごとの e2e の試験と、ライブのキャプチャ。 |
| `web/` | ラボ（`lab/`）と OAM のサーバー（`oam/`）の画面。 |
| `images/` | キャプチャ用のコンテナなど、補助のイメージ。 |

---

## 実行ファイルは 3 つ

- `cmd/ims` は IMS のノード 1 台です。何のノードになるかは、設定ファイルの `roles` で決まります。1 つのイメージが、どのノードにもなります。
- `cmd/ue` は UE シミュレータです。`register`、`call`、`answer`、`serve`、`sms`、`xcap`、`ctl` のサブコマンドがあります。
- `cmd/oss` は OSS の代わりです。OAM のサーバーの警報を読み、確認します。IMS のノードではありません。

```yaml
# deploy/compose/scscf.yaml
node: scscf
roles: [scscf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
```

- コアの役割は `hss`、`icscf`、`scscf`、`pcscf`、`agw`、`lab`、`ipsmgw`、`sc` です。VoLTE とサービスには `epc`、`mmtel`、`mrfc`、`mrfp` を使います。ほかの網との境界には `ibcf` を使います。ノードの運用と、わざと壊すことには `oam` と `fault` を使います。
- `roles` に 2 つ書くと、1 台が 2 つの役割を兼ねます。

> 役割の一覧は `internal/config/config.go` の `knownRoles` にあります。

---

## Compose のノードと役割

| ノード | アドレス | 役割 | 設定 |
| --- | --- | --- | --- |
| `dns` | 172.28.0.2 | CoreDNS | `deploy/compose/dns/` |
| `hss` | 172.28.0.10 | `hss` | `hss.yaml` |
| `icscf` | 172.28.0.20 | `icscf` | `icscf.yaml` |
| `scscf` | 172.28.0.30 | `scscf` | `scscf.yaml` |
| `pcscf` | 172.28.0.40 | `pcscf` | `pcscf.yaml` |
| `agw` | 172.28.0.50 | `agw` | `agw.yaml` |
| `lab`、`oam` | 172.28.0.60、65 | `lab`、`oam` | `lab.yaml`、`oam.yaml` |
| `ipsmgw`、`sc`、`mmtel` | 172.28.0.70、71、75 | `ipsmgw`、`sc`、`mmtel` | `ipsmgw.yaml`、`sc.yaml`、`mmtel.yaml` |
| `epc`、`mrfc`、`mrfp` | 172.28.0.80、85、86 | `epc`、`mrfc`、`mrfp` | `epc.yaml`、`mrfc.yaml`、`mrfp.yaml` |
| `ue`、`ue2`、`ue3` | 172.28.0.100、101、103 | `cmd/ue` | コマンドの引数 |

- 表に無いものは、ラボと VoLTE の UE（`phone1` から `phone3`、`volte10`、`volte12`）、`softphone`、`oss`、`capture` です。障害注入のエージェントは、`deploy/chaos/` に自分の Compose のファイルがあります。
- IBCF ともう 1 つの網は、別の Compose のプロジェクト `deploy/interconnect/compose.yaml` にあります。

> 手順書 [信号をキャプチャして Wireshark で開く「ノードを起動すると見えるもの」](../../../docs/guide/ja/capture.md#ノードを起動すると見えるもの)

---

## internal/ を層で見る

| 層 | パッケージ |
| --- | --- |
| 役割。ノードの振る舞い。 | `pcscf` `icscf` `scscf` `hss` `ipsmgw` `sc` `smsc` `agw` `lab` `ue` `epc` `mmtel` `mrfc` `ibcf` `oam` |
| IMS の決まりとインタフェース | `ims` `ifc` `reginfo` `cx` `sh` `rx` `sgd` `policy` `simservs` `interconnect` `netann` `confinfo` `b2bua` |
| プロトコル | `sip` `sipdns` `diameter` `sdp` `rtp` `rtcp` `sms` `hep` `xcap` `h248` `ipsec` `accessapi` |
| 符号化と計算 | `g711` `amr` `evs` `wav` `mixer` `milenage` `aka` `precondition` |
| 起動、状態、運用 | `config` `hepagent` `metrics` `kvstore` `maintenance` `fault` `mns` `jex` |
| `cmd/` のどのコマンドも読み込まないもの | `fivegc` `n5` `nhss` `sbi` `nas` `rtptest` `openapitest` `yamledit` |

- 役割のパッケージが、下の層を組み合わせて使います。役割 `mrfp` はパッケージ `agw` で、音声ガイダンスと会議に `mixer` を使います。役割 `sc` はパッケージ `smsc` です。
- 層は仕様の文書で分かれます。RFC 3261 はパッケージ `sip`、TS 24.229 は役割のパッケージです。
- プロトコルの層は役割を知りません。`sip` は IMS のことを何も知らない SIP です。

---

## 役割のパッケージ（CSCF と HSS）

| パッケージ | ノード | 主な仕様 |
| --- | --- | --- |
| `pcscf` | P-CSCF。UE から見て最初の SIP の中継点。 | TS 24.229 |
| `icscf` | I-CSCF。ホーム網の入口。 | TS 24.229、TS 29.228 |
| `scscf` | S-CSCF。レジストラ。iFC の評価もここ。 | TS 24.229、TS 29.228 |
| `hss` | HSS のスタブ。Cx、Sh、S6c に答える。 | TS 29.228、TS 29.328、TS 29.338 |

- 1 つの役割の中は、手順ごとにファイルが分かれています。
  - `scscf.go` は登録、`session.go` は通話、`services.go` は iFC、`regevent.go` は reg イベントです。

> 手順書 [UE が IMS に登録する流れを追う「登場するノード」](../../../docs/guide/ja/registration.md#登場するノード)

---

## 役割のパッケージ（AS、メディア、UE、観測）

| パッケージ | ノード | 主な仕様 |
| --- | --- | --- |
| `ipsmgw` | IP-SM-GW。SMS over IP のアプリケーションサーバー。 | TS 24.341、TS 23.204 |
| `smsc` | SMS のサービスセンタ。役割 `sc`。ショートメッセージを預かり、送り直す。 | TS 23.040、TS 29.338 |
| `sc` | SC のスタブ。設定にサービスセンタが無い IP-SM-GW が使う。 | TS 23.204 |
| `mmtel` | MMTel AS。付加サービス。 | TS 24.173、TS 24.604 から 24.611、TS 24.623 |
| `agw` | メディア中継ノード。IMS-AGW の役割。 | RFC 3550。制御の API はこのプロジェクト独自 |
| `ue` | UE シミュレータ。 | TS 24.229、TS 24.341 |
| `lab` | HEP の写しから登録と通話を組み立てる。 | このプロジェクト独自 |
| `oam` | OAM のサーバー。保守の API でノードを操作する。 | OSS に向けては TS 28.111 |

- スタブは、仕様のインタフェースだけを本物にして、中身を簡単にしたものです。HSS と `sc` がスタブです。
- ラボは IMS の一部ではありません。ノードが送るものを見せるだけです。

> 手順書 [ラボで IMS を見る「このプロジェクト独自の API」](../../../docs/guide/ja/lab.md#このプロジェクト独自の-api)

---

## プロトコルのパッケージ

| パッケージ | 中身 | 仕様 |
| --- | --- | --- |
| `sip` | メッセージ、トランスポート、トランザクション、ダイアログ、UA | RFC 3261 |
| `sipdns` | NAPTR、SRV、A で SIP のサーバーを探す | RFC 3263、RFC 6116 |
| `diameter` | 基本プロトコル、ピアの状態、辞書 | RFC 6733 |
| `cx`、`sh` | Cx と Sh のメッセージ | TS 29.228、29.229、29.328、29.329 |
| `sdp` | SDP と、オファーとアンサー | RFC 3264 |
| `rtp` | RTP のパケット | RFC 3550 |
| `sms` | RP 層、TP 層、文字の符号化 | TS 24.011、23.040、23.038 |
| `reginfo`、`ifc` | reg イベントの XML と、初期フィルタ条件 | RFC 3680、TS 29.228 |

- 識別子は仕様の用語のままです。たとえば `cx.ServerAssignmentRequest` です。`cx` と `sh` では、AVP が仕様の順に Go の構造体のフィールドになります。

---

## そのほかのパッケージ

| パッケージ | 中身 |
| --- | --- |
| `ims` | 複数の役割が共有する IMS の決まり。 |
| `config` | YAML の設定を、検証済みの値にする。誤った設定は起動のときに断る。 |
| `hep`、`hepagent` | 信号の写しをラボへ送る（HEP3）。 |
| `g711`、`wav` | G.711 μ-law と WAV。UE シミュレータの音声。 |
| `epc`、`policy`、`rx`、`milenage`、`aka`、`amr`、`rtcp`、`precondition` | VoLTE の通話で使う部品。この回では扱わない。 |
| `nas`、`fivegc`、`n5`、`nhss`、`sbi` など | どの役割も起動しない部品。使うのはそれぞれの試験だけ。 |

- どの役割も起動しない部品は、`cmd/` のどのコマンドからも import されていません。`go list -deps ./cmd/...` に出てこないことで確かめられます。

> 手順書 [信号をキャプチャして Wireshark で開く「ラボに送るコピー（HEP）」](../../../docs/guide/ja/capture.md#ラボに送るコピーhep)

---

## 状態は Store に置く

- 役割は、利用者の状態を `Store` というインタフェースを通して読み書きします。状態をノードの外に置けるようにするためです。
- Compose の構成で使う実装は `MemoryStore` で、プロセスの中に置きます。

| ファイル | 置いているもの |
| --- | --- |
| `internal/scscf/store.go` | 登録、認証の途中の状態、iFC の評価の続き |
| `internal/pcscf/pcscf.go` | UE ごとの IP の対応付け |
| `internal/hss/store.go` | 加入 |
| `internal/ipsmgw/store.go` | 配達の途中のショートメッセージ、購読 |
| `internal/smsc/store.go` | サービスセンタが預かるショートメッセージ。宛先ごと。 |

- `Store` の後ろに共有の保存先を置けば、役割を複数台で動かせます。
- S-CSCF が登録をただの map に入れないのはなぜでしょうか。2 台目の S-CSCF が同じ登録を見られなくなるからです。IMS が PBX と違うのはここです。

> 手順書 [SMS の流れを追う「12. delivery report が UE 2 の条件で IP-SM-GW に届く」](../../../docs/guide/ja/sms.md#12-delivery-report-が-ue-2-の条件で-ip-sm-gw-に届く)

---

## 差し替えられる境界

インタフェースにしてあるのは、スタブが本物のノードの代わりをしている所と、1 つの部品に 2 つのやり方がある所だけです。

| 境界 | コードでの形 | 差し替えられる相手 |
| --- | --- | --- |
| HSS | Cx、Sh、S6c。Diameter のメッセージそのもの。 | 本格的な HSS |
| メディア中継ノードの制御 | `pcscf` の `Relay` | Iq の H.248 による制御（`h248/mediactl`）。設定で選ぶ。 |
| SC | `ipsmgw` の `ServiceCentre` | スタブの代わりに、SGd（`sgd`）でつなぐ `sc` のノードのサービスセンタ。設定で選ぶ。 |
| 役割の組み合わせ | 設定の `roles` | 1 台に複数の役割 |

- これ以外の場所には、差し替えのための抽象化を置いていません。使う場所が 1 つしか無い抽象化は、読む層を増やすだけだからです。
- HSS のスタブは本物と同じ Cx、Sh、S6c を話します。キャプチャに見えるものが、本物の HSS が送るものと同じになります。

---

## コードを読む 3 つの手がかり

1. **パッケージのコメント。** 何のノードで、どの仕様に沿っているかが、最初に書いてあります。
2. **条項番号。** 手順に対応する箇所のコメントに、仕様の条項があります。コードの横に仕様を開いて読めます。
3. **`SIMPLIFIED:`。** 仕様を簡略化した箇所の印です。何を省いたかと、その理由が書いてあります。

```sh
grep -rn "SIMPLIFIED:" internal cmd
```

- `internal/scscf/scscf.go` の先頭で、3 つとも見つかります。

> 手順書 [UE が IMS に登録する流れを追う「このプロジェクトで簡略化していること」](../../../docs/guide/ja/registration.md#このプロジェクトで簡略化していること)

---

## 1 つの REGISTER をコードで追う

| 順 | ノード | 開くファイル | 探すもの |
| --- | --- | --- | --- |
| 1 | UE | `internal/ue/ue.go` | REGISTER を組み立てるところ |
| 2 | P-CSCF | `internal/pcscf/pcscf.go`、`internal/sipdns/locate.go` | Path を付けるところ、I-CSCF を DNS で探すところ |
| 3 | I-CSCF | `internal/icscf/icscf.go` | UAR を送るところ |
| 4 | S-CSCF | `internal/scscf/scscf.go` | MAR と SAR を送るところ、401 と 200 を返すところ |
| 5 | HSS | `internal/hss/hss.go` | UAR、MAR、SAR に答えるところ |

- メッセージの形は `internal/cx/messages.go` にあります。
- 2 人 1 組で、表の「探すもの」を見つけます。見つけたら、その近くのコメントにある条項番号を読み上げます。

> 登録の回で、同じ流れをキャプチャで読みます。手順書は [UE が IMS に登録する流れを追う](../../../docs/guide/ja/registration.md) です。

---

## 試験で全体を動かす

- `internal/ue` の試験は、IMS 全体を 1 つのプロセスの中で動かし、登録と通話を確かめます。
- `internal/ipsmgw` の試験は、同じ方法で SMS を確かめます。
- `scripts/e2e-*.sh` のスクリプトは、シナリオごとに Compose の構成を動かし、キャプチャに期待するメッセージがあるかを確かめます。

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -v ims-practice-gomod:/go/pkg/mod \
  -w /src golang:1.27.4-alpine go test ./...
```

---

[layout: exercise]

## 振り返り

1. S-CSCF と I-CSCF を 1 台で動かすには、何を変えますか。
2. UAR の AVP を並べているのは、どのパッケージですか。UAR を送るのは、どのパッケージですか。
3. `SIMPLIFIED:` のコメントは、今いくつありますか。
4. P-CSCF のコードが `sip` を使うのと、`sip` が `pcscf` を使うのと、どちらが正しい向きですか。

---

[layout: answers]

## 振り返りの答え

1. 設定ファイルの `roles` に両方を書きます。コードは変えません。
2. 並べているのは `cx`、UAR を送るのは `icscf` です。
3. 「コードを読む 3 つの手がかり」の `grep` で数えます。数はコードが変わると変わります。
4. `pcscf` が `sip` を使います。逆はありません。

---

## 次の回と参照

- 次の回は網の地図です。ノードと、その間のインタフェースと、地図のどこをどの回が開くかを見ます。
- 登録の回までに、[信号をキャプチャして Wireshark で開く](../../../docs/guide/ja/capture.md) の手順を一度試しておいてください。

| 文書 | 場所 |
| --- | --- |
| このプロジェクトの説明と動かし方 | [README.ja.md](../../../README.ja.md) |
| 用語集 | [docs/glossary.md](../../../docs/glossary.md) |
| 手順書 | [docs/guide/ja/](../../../docs/guide/ja/) |
