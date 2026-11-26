# ロードマップ

この文書はこのプロジェクトでできていることと、これから作るものの一覧です。開発は主題ごとに段階を分けて進め、各段階は、学習者が起動して動きを確認し、キャプチャできる状態で終わります。要件は [requirements.md](requirements.md)、作りの理由は [設計の記録](design/README.md) にあります。

## 順番の考え方

- 信号の流れは「登録」が先で「通話」が後です。登録を理解していないと、通話の経路（Path、Service-Route）が読めません。
- キャプチャと自動試験の仕組みは最初に作ります。後の段階はすべてその上で確認するためです。
- MMTel AS（付加サービス）と MRF は VoLTE の手順のあと、相互接続の前に置きます。MRF の音声ガイダンスと CAT は、VoLTE の段階で入る early media（P-Early-Media、プリコンディション）、AMR、DTMF を使うためです。また、転送で付く History-Info と、発信者番号の非通知で付く Privacy は、網の境界（IBCF）での扱いが問題になる代表です。相互接続より前に入れておくと、相互接続の段階で試せることが増えます。
- IMS 網どうしの相互接続は、通話、SMS、VoLTE、付加サービスの手順がそろってから取り組みます。網の間で食い違うのは、主にそれらの手順の細部だからです。
- 冗長構成と障害観察は、通話と SMS が動いてから取り組みます。ただし、状態をノードの外に置く設計は最初から守ります。
- H.248 は冗長構成と障害観察のすぐあとに置きます。H.248 の ServiceChange と Audit は、ゲートウェイの再起動や切り離しと組み合わせて見ると働きが分かるためです。
- 5GC（VoNR）は H.248 のあと、OAM の前に置きます。VoLTE の段階で EPC と Rx を先に作り、P-CSCF とアクセス網の結びつきを 1 つの境界の後ろにまとめてあるので、5GC はその境界の 2 つ目の実装として加えられます。OAM より前にするのは、OAM がまとめて保守するノードに 5GC のスタブも含めるためです。
- SMS を NAS で届ける段階は 5GC のすぐあとに置きます。SMS in MME と 5GS の SMS over NAS は、EPC のスタブと 5GC のスタブの両方を使うためです。
- 各段階の最初に、その段階に関わる標準を調べ、実装の方針を決めてから作ります。3GPP の仕様に加えて、GSMA の IR.92 と NG.114、国ごとの標準（日本では TTC）も確かめます。各段階の項目は計画で、細部はその調査で決めます。
- 運用と保守（OAM）は、冗長構成と障害観察のあとに置きます。ドレインや復旧の手順を、OAM からの操作としてまとめられるためです。保守用の API を前提にした作りは、それより前の段階でも妨げないようにします。
- Kubernetes 対応は最後に近い位置に置きます。Docker Compose で動く構成をそのまま移せるように作るためです。

各段階の詳しい作業計画はその段階を始めるときに書きます。

## できていること

手順書は [docs/guide/ja/](guide/ja/)（英語は [docs/guide/en/](guide/en/)）に、Compose で動きを確かめるスクリプトは `scripts/` にあります。

### 開発の基盤

**できること.** 空の構成を 1 コマンドで起動し、コンテナ間の通信を pcap に保存して Wireshark で開ける。

- リポジトリ構成、ビルド、CI（lint とテスト）を用意する。
- Docker Compose の骨組みと、キャプチャ用のコンテナを用意する。
- 試作で決められる論点を試作で決める。実装の言語（[implementation.md](design/implementation.md)）、SIP スタックのどの層を自作するか（同じ記録）、一般的なソフトフォンが IMS-AKA に対応しているか（[ue-simulator.md](design/ue-simulator.md)）、Windows ホストから Docker 内への RTP の到達性（[environment-capture.md](design/environment-capture.md)）。
- 用語集の初版を作る。

手順書は「キャプチャ」（[capture.md](guide/ja/capture.md)）です。ライブで見るには `scripts/capture-live.sh` か `scripts/capture-live.ps1` を使います。

**完了条件.** CI が通り、`docker compose up` で起動した構成から pcap を取り出せる。

### 登録

**できること.** UE シミュレータが IMS に登録する流れを、Wireshark とログで追える。

- 1 つのイメージで P-CSCF、I-CSCF、S-CSCF の役割を設定で切り替える。
- HSS スタブを作る。Diameter Cx の UAR/UAA、MAR/MAA、SAR/SAA に対応する。
- DNS サーバーを置き、I-CSCF の発見を DNS（NAPTR、SRV）で行う。ノードは既定で `/etc/resolv.conf` の DNS に問い合わせ、設定で外付けの DNS サーバーに切り替えられる。
- SIP Digest 認証で登録する。
- Via の `rport`（RFC 3581）に対応し、ホスト上のソフトフォンから NAT 越しに登録できるようにする。
- UE シミュレータの CLI 版を作る。
- 手順書「登録の流れ」を書く。ラダー図と仕様の参照箇所を載せる。

手順書は [registration.md](guide/ja/registration.md)、スクリプトは `scripts/e2e-diameter.sh`（Diameter の接続）と `scripts/e2e-registration.sh` です。

**完了条件.** CI の試験で登録が成功し、pcap に REGISTER、401、Cx の各メッセージ、200 OK が順に記録されている。

### 音声通話

**できること.** UE どうしで発着信し、音声を聞ける。Wireshark の RTP Player で音声を再生できる。

- INVITE、183、PRACK、180、200、ACK、BYE の基本シーケンスに対応する。
- SIP の TCP に対応する。1300 バイトを超える要求は TCP で送る（RFC 3261 18.1.1）。REGISTER でも、I-CSCF から S-CSCF への要求はこの大きさを超える。
- reg イベントの購読（RFC 3680）に対応する。UE と P-CSCF が登録の後に購読し、S-CSCF が通知する（TS 24.229 5.1.1.3、5.2.3、5.4.2.1）。
- SDP のオファーとアンサーで G.711 のメディアを確立する。
- メディア中継ノードを作り、RTP を経由させる。NAT 越しのソフトフォンのために、RTP のラッチングに対応する。
- UE シミュレータが WAV ファイルを送り、受信した音声を保存する。
- ソフトフォン（Linphone など）から登録と通話ができる。
- 電話番号（tel URI）で発信できるようにする。DNS に ENUM のゾーンを置き、S-CSCF が番号を SIP URI に変換する。HSS には 1 人の加入者の SIP URI と tel URI を同じ登録の組として持たせる。
- 相手不在、話中、未登録の異常系に対応する。
- 手順書「通話の流れ」を書く。

手順書は [call.md](guide/ja/call.md)、スクリプトは `scripts/e2e-call.sh` です。

**完了条件.** CI の試験で通話が成立し、受信側の RTP ペイロードが、送信側で G.711 に符号化したデータと一致する。G.711 は非可逆の符号化なので、元の WAV とは比較しない。

### ラボ

**できること.** ブラウザで登録状態と通話状態を確認し、呼ごとのラダー図を表示できる。

- 各ノードが送受信した信号を HEP で収集する。
- ラボで登録状態、通話状態、加入者情報を表示する。
- Call-ID ごとのラダー図を表示し、各メッセージの中身と仕様の参照箇所を見られる。
- ラボで加入者の一覧を見られる。加入者の追加、削除、CAT の設定は OAM から行う（[lab-oam.md](design/lab-oam.md)）。
- ラボから UE シミュレータを操作できる。

手順書は [lab.md](guide/ja/lab.md)、スクリプトは `scripts/e2e-lab.sh` です。

**完了条件.** 通話を 1 回行うと、ラボにその呼のラダー図が表示され、pcap の内容と一致する。

### SMS

**できること.** UE どうしで SMS を送受信し、iFC で AS が呼び出される流れを追える。

- S-CSCF で iFC を評価し、AS へ信号を転送する。
- IP-SM-GW を AS として作る。
- SIP MESSAGE に `application/vnd.3gpp.sms` で SMS を入れて送る。
- HSS スタブに Sh を追加する。
- SC を独立した役割にする。IP-SM-GW とは SGd（TS 29.338 6 章）で、HSS とは S6c（TS 29.338 5 章）でやり取りし、届かなかったショートメッセージを預かって、HSS の知らせで送り直す。
- 手順書「SMS の流れ」を書く。

手順書は [sms.md](guide/ja/sms.md)、スクリプトは `scripts/e2e-sms.sh` です。作りは [sms.md](design/sms.md) にあります。

**完了条件.** CI の試験で SMS が届き、pcap で Wireshark が SMS の中身（RP-DATA、TPDU）をデコードできる。

### VoLTE（アクセス網、IMS-AKA、PCC）

**できること.** EPC のスタブを含めた構成で、VoLTE に近い手順（音声用ベアラ、プリコンディション、IMS-AKA）を追える。

- EPC スタブを作る。UE の接続、IP アドレスの払い出し、P-CSCF アドレスの通知を模擬する。UE との境界は TS 24.301 の ESM のメッセージに合わせた HTTP の API にし、UE の IP アドレスは加入者ごとの静的な値にする。
- PCRF スタブを役割 `epc` の中に作り、P-CSCF との間で Rx を使う。P-CSCF がアクセス網と結びつく所（Rx）は 1 つの境界の後ろにまとめ、5GC の実装（N5）を加えられるようにする。
- 網の側から始まる Rx の通知を P-CSCF で処理する。IR.92 は、PDN の接続や音声のベアラが失われたときに、網がセッションを終えることを求めている（2.4.2.1、2.4.2.3。TS 24.229 5.2.8 の手順）。調べた結果は [volte.md](background/volte.md) の 2 章にある。
- 異常系として、音声の dedicated bearer を EPC スタブの操作で落としたとき、PCRF が RAR か ASR で知らせ、P-CSCF が BYE で呼を切る（IR.92 2.4.2.3、TS 24.229 5.2.8.1.2）。P-CSCF は BYE を作るためにダイアログの情報を `Store` に置く。
- UE が P-Access-Network-Info を付ける（TS 24.229 L.3.1.1、7.2A.4.3）。IR.92 v24.0 には記述が見つからなかった。
- プリコンディション（RFC 3312）を使った呼の確立に対応する。
- IMS-AKA（Milenage、AKAv1-MD5）で認証する。sec-agree と保護ポートの手順は行い、ESP はかけない。ESP をかける設定は試さず、拡張の候補に残した。
- SIPp を独立した試験相手にして、AKA の登録を確かめる。
- GSMA IR.92 の必須の要件を満たす。今の実装との差は [profiles.md](background/profiles.md) の表にまとめてある。
  - AMR と AMR-WB と、その RTP ペイロードの形式（RFC 4867）に対応する。符号化済みのフレームを送り、製品は cgo を使わない。UE の offer には PCMU も残す。
  - RTCP、b=AS、b=RS と b=RR、DTMF の telephone-event に対応する。
  - セッションタイマー、Reason ヘッダ、P-Early-Media、199 応答、登録の機能タグに対応する。
- `internal/sip` の基盤（UPDATE、セッションタイマー、199、Reason、OPTIONS、re-INVITE、B2BUA に要る部品）を入れ、付加サービスもその上に作る。
- 手順書「VoLTE の呼の流れ」を書く。

手順書は [volte.md](guide/ja/volte.md)、スクリプトは `scripts/e2e-volte.sh` です。ESP（暗号化は null）を有効にした構成は `deploy/esp/`、その確認は `scripts/e2e-esp.sh` で、カーネルのアソシエーションとキャプチャの ESP の中の SIP を読みます。作りは [volte-access.md](design/volte-access.md) と [codecs.md](design/codecs.md) にあります。

**完了条件.** CI の試験で IMS-AKA の登録と、プリコンディション付きの通話が成功する。

### 付加サービス（MMTel AS、Ut）

**できること.** S-CSCF が iFC で MMTel AS を呼び出し、発信者番号の通知と非通知、着信転送、着信規制などの付加サービスが働く流れを追える。

- MMTel AS を `ims` コマンドの役割 `mmtel` として作る。AS は最初から全体を routeing B2BUA として作る。
- 発信側と着信側の呼を、iFC で MMTel AS に通す。AS を加えるときは、HSS の iFC に AS の URI を書けば済むようにする。S-CSCF が P-Served-User（RFC 5502）を入れ、AS は発信と着信をそれで見分ける。
- 未登録の利用者への着信を、HSS（LIA）、I-CSCF、S-CSCF（SAR と TERMINATING_UNREGISTERED の iFC）に作る。CFNL と、未登録の利用者への CFU に要る。
- IR.92 2.3 の表 2.1 にある付加サービスのうち、メディアの資源を使わないものに対応する。
  - OIP と OIR（TS 24.607）。OIR では Privacy ヘッダで発信者の識別子を隠す。
  - TIP と TIR（TS 24.608）。
  - 着信転送（CDIV、TS 24.604）。CFU、CFB、CFNR、CFNRc、CFNL に対応し、転送の経歴を History-Info で運ぶ。
  - 着信と発信の規制（CB、TS 24.611）。BAIC、BAOC、BOIC、BOIC-exHC に対応する。ローミングは対象外なので、AS は served user をホーム網にいるものとみなし、BIC-Roam の条件は成り立たない（手順書の「簡略化したこと」）。
  - 保留（HOLD、TS 24.610）。
  - 割込通話（CW、TS 24.615）。IR.92 は端末で行う方式だけを求め、網で行う方式は求めていない（2.3.4）。UE シミュレータが通話中に 2 本目の着信を受けられるようにする。
- 表 2.1 の MWI（TS 24.606）と ECT（TS 24.629）は、IR.92 が網の対応を任意としている（2.3.5、2.3.11）ので、網の側は作らず、拡張の候補に残す。会議は MRF を使うので MRF の段階で扱う。
- 付加サービスの設定データ（TS 29.364 の `MMTelServices`）を、Sh の Repository Data として HSS にだけ置く。AS は初期の要求ごとに UDR で読み、利用者ごとの状態を持たない。
- 利用者が Ut の XCAP（TS 24.623）で付加サービスを設定できるようにする。範囲は、IR.92 が求める最小限（2.3.2 と、CDIV と CB の規則の 2.3.8、2.3.9）にする。認証は AS で行う HTTP Digest にし、資格情報は HSS に置く。GBA は作らない。UE シミュレータに XCAP のクライアントを加える。
- 手順書「付加サービスの流れ」を書く。

手順書は [mmtel.md](guide/ja/mmtel.md)、スクリプトは `scripts/e2e-mmtel.sh` です。作りは [mmtel-mrf.md](design/mmtel-mrf.md) にあります。

**完了条件.** CI の試験で、CFU、CFB、CFNR の各条件で呼が転送先につながり、規制した呼が拒否され、OIR を使った呼で着信側に発信者の識別子が届かない。pcap に History-Info と Privacy ヘッダが記録されている。

### MRF（音声ガイダンス、会議、CAT）

**できること.** MRF が音声ガイダンス、会議、CAT（利用者が選ぶ呼び出し音）を提供する流れを追える。180 を受けた UE が自分で呼び出し音を鳴らす場合と、網が early media で音を流す場合を、キャプチャで比べられる。

- MRFC を SIP の役割として作る。MMTel AS は RFC 4240 の netann（`annc` と `conf`）で MRFC を動かす。
- MRFP はメディア中継ノードの実装（`internal/agw`）を N 個のターミネーションに広げて作り、音声ファイルの再生、音声の合成、通知を加える。役割は `mrfp` にした。MRFC は独自の制御 API で MRFP を制御する。API は H.248 の Add、Modify、Subtract、Notify に写せる形にする。
- 音声ガイダンスを流す（TS 24.628）。たとえば、規制した呼や、つながらなかった呼の発信者に流す。
- 会議に対応する（TS 24.147、TS 24.605）。IR.92 2.3.3 が求める三者通話の作り方と、会議の状態の購読を対象にする。
- CAT に対応する（TS 24.182）。着信側の利用者が選んだ呼び出し音を、MRF が early media として発信者に流す。FRBT（flexible RBT）と呼ばれるものも、このサービスに当たる。TS 24.182 にある方式のうち、ゲートウェイのモデルで作った。
- UE シミュレータが P-Early-Media に従って、自分で鳴らす呼び出し音と網からの early media を切り替える。
- 手順書「MRF の流れ」を書く。

手順書は [mrf.md](guide/ja/mrf.md)、スクリプトは `scripts/e2e-mrf.sh` です。作りは [mmtel-mrf.md](design/mmtel-mrf.md) にあります。

**完了条件.** CI の試験で、発信者が音声ガイダンスと CAT を early media として受け、受けた RTP のペイロードが元の音声ファイルを符号化したデータと一致する。三者の会議で、各参加者がほかの 2 人の音声を合成したものを受ける。

### 網間接続

**できること.** 自網と相手網の 2 つの IMS を IBCF と TrGW でつなぎ、網をまたぐ通話と SMS を追える。接続先ごとのプロファイルを変えて、網の間で SIP の仕様が食い違ったときに何が起き、どこで吸収するかを試せる。

- 相手網として、2 つ目の IMS ドメインを Compose に用意する。自網とはレルム、番号、加入者を分ける。2 つ目の網は `deploy/interconnect/` の上書きのファイルで足す。
- ENUM で相手網の番号を相手網の SIP URI に変換し、S-CSCF から出口の IBCF へ送る。相手網の入口の IBCF は、受けた要求を相手網の I-CSCF へ送る。
- IBCF を作る。接続先ごとのプロファイルで、ヘッダとパラメータの許可と書き換え、番号の形式の正規化、トランスポートを設定する。
- 信頼ドメインの境界を扱う。P-Asserted-Identity などのヘッダを、信頼できない相手との間で削除する。
- トポロジーを秘匿する。Via、Record-Route などに現れる自網のアドレスを隠す。
- TrGW を作る。メディア中継ノードを網の境界に置き、IBCF が制御する。SDP のアドレスを書き換え、相手網が受けないコーデックを SDP から外す。
- 自網と相手網のプロファイルを食い違わせたシナリオを用意する。たとえば 100rel とプリコンディションの扱い、トランスポート、コーデック、番号の形式を変える。食い違いで呼が失敗する様子と、プロファイルを調整して通る様子をキャプチャで比べる。
- 手順書「IMS 網どうしの相互接続」を書く。網の間の SIP のプロファイル（TS 29.165、GSMA IR.95）との対応も載せる。

手順書は [interconnect.md](guide/ja/interconnect.md)、スクリプトは `scripts/e2e-interconnect.sh` です。調査と作りは [interconnect.md](background/interconnect.md) にあります。

**完了条件.** CI の試験で網をまたぐ通話と SMS が成功し、相手網で取った pcap に自網の内部のアドレスが現れない。食い違ったプロファイルで呼が失敗し、調整したプロファイルで成功することを、CI の試験で確認できる。

## これから作るもの

次の順で作ります。

### 冗長構成

**できるようになること.** 通話中にノードを止めたり切り離したりして、影響と復旧を観察できる。

設計は [state-redundancy.md](design/state-redundancy.md) にあります。

- P-CSCF と S-CSCF を複数台で動かす。DNS の SRV レコードで振り分け、503、トランスポートの異常、時間切れのときは次の台へ送り直す（RFC 3263 4.3）。同じ役割の台は、台ごとの名前と、SRV で引く組の名前を持つ。
- 規模を広げる妨げになっている作りを取り除く。状態とタイマーを外部の保存先（Valkey）に移し、全加入者を走査する処理と、役割全体を止めるロックをなくす。期限切れは時刻の索引で 1 台だけが処理する。HSS を複数台にし、状態を共有するサーバーファームにする（TS 29.228 6.4）。CSCF と AS は、答えない HSS から次の HSS へ Diameter の接続を切り替える。SLF（Dx）は作らず、拡張の候補にする。メディア中継ノード、AS（IP-SM-GW、MMTel AS）、MRF も複数台にする。
- 登録と呼の継続性を作る。調査で外部の保存先を試作して、呼の処理の遅延と、障害から戻るまでの様子を測り、外部の保存先に決めた。作り直したノードは、同じ名前とアドレスで保存先の状態から続ける。MMTel AS は、作り直した台が呼の記録から 2 本のダイアログを組み立て直す。組み立て直した呼から送る要求の CSeq は、前のプロセスが送ったどの番号よりも大きくする（RFC 3261 12.2.2）。メディア中継ノードは、作り直した台がコンテキストを読み直し、同じポートを開き直す。MRFC の台が止まったときは、その台のターミネーションを MRFP から解放する（TS 23.333 6.1.5.1）。
- ドレイン操作を作る。各ノードの保守用の API（`internal/maintenance`）で始め、新規の登録と呼を他のノードに回し、通話中の呼は維持する。
- UE シミュレータが P-CSCF の一覧を持ち、REGISTER の時間切れ、503、305 と、504 の復旧の指示で、別の P-CSCF に最初の登録を行う（IR.92 2.2.1、TS 24.229 4.14.2、5.1.2A.1.6）。
- 障害注入の仕組みを作る。ノード停止、遅延、パケット損失、ネットワーク分断を対象にする。IMS のノードは Docker のソケットに依らない。
- TS 23.380 の復旧手順のうち、S-CSCF の組の障害（復旧用の情報を HSS に預ける、I-CSCF が別の組を選ぶ、P-CSCF の 504 で登録し直させる）と、UE が P-CSCF の障害に気づいて登録し直す流れを再現する。P-CSCF の網からの復旧（5.4、5.5）は拡張の候補にする。
- 学習用の画面から障害注入を操作する。ドレインは OAM の側の操作にする（[lab-oam.md](design/lab-oam.md)）。
- 手順書「障害を起こして観察する」を書く。

今あるもの。保守用の API の共通部分（`internal/maintenance`）、障害注入の部品（`internal/fault`、`deploy/chaos/`）、保存先のクライアント（`internal/kvstore`）があります。保守用の API のドレインは、P-CSCF、I-CSCF、S-CSCF、HSS、MMTel AS、MRFC、IP-SM-GW、メディア中継ノード、MRFP につないであります。HSS は Diameter の相手に DPR を送って接続を閉じ、MMTel AS はダイアログの外の INVITE と購読を始める SUBSCRIBE、それに Ut の新しい要求に 503 を返し、MRFC はダイアログの外の INVITE に 503 を返し、IP-SM-GW は第三者 REGISTER と新しい短いメッセージの MESSAGE に 503 を返します。メディア中継ノードと MRFP は、新しいコンテキストを作る `Add` に 503 を返し、持っているコンテキストを続けます。P-CSCF と MRFC は一覧の次の台を試します（[state-redundancy.md](design/state-redundancy.md)）。

作っていないもの。MMTel AS の作り直した台は、前のプロセスの呼を組み立て直しません。その呼のダイアログの中の要求には 481 を返すので、呼は切れます。台が強制的に止められたときや、呼の記録を消し終わる前にプロセスが終わったときは、記録が保存先に残ります。強制的に止められた MRFC の台や、ダイアログがターミネーションを取り除く前にプロセスが終わった台のターミネーションも、MRFP から解放する者がいません。ターミネーションはポートを開いたまま残り、会議の項目も保存先に残ります。

**完了条件.** ドレイン中のノードを経由する通話が切れずに終わり、その後の新規の呼が他のノードを経由することを、CI の試験で確認できる。通話中に P-CSCF か S-CSCF のコンテナを削除し、同じ名前とアドレスで作り直しても、その呼が切れず、登録済みの UE が再登録なしで着信できることを、CI の試験で確認できる。メディア中継ノードの障害は、設計の記録で決めた範囲を確認する。作り直した中継ノードを通る呼で、RTP が途切れたあとで同じポートから戻り、呼が BYE まで続く。

### H.248

**できるようになること.** メディアの制御を、このプロジェクト独自の HTTP の API と H.248 のどちらでも行える。同じ通話を両方の制御で行い、キャプチャを比べられる。

- メディアの制御の境界の後ろに、H.248（ITU-T H.248.1）で制御する 2 つ目の実装を作る（[media-control.md](design/media-control.md)）。既定は独自の API のままにする。
- 範囲は 3GPP のプロファイルが使うものに限る。Iq（P-CSCF とメディア中継ノード、TS 29.334）、Ix（IBCF と TrGW、TS 29.238）、Mp（MRFC と MRFP、TS 29.333）が対象になる。
- H.248 には符号化がテキストと 2 進（ASN.1）の 2 つある。どちらを使うかはこの段階の調査で決める。
- ServiceChange と Audit を、冗長構成の障害の観察と組み合わせる。ゲートウェイの再起動や切り離しを、制御する側がどう知るかを見られるようにする。
- 手順書「H.248 でメディアを制御する」を書く。独自の API と H.248 で、同じ操作がどう見えるかを並べる。

今あるもの。H.248 の MG の側と MGC の側の部品が `internal/h248` にあります。Iq は役割につないであります。`pcscf.h248` の節を読む P-CSCF はコントローラの側を、`agw.h248` の節を読むメディア中継ノードはゲートウェイの側を動かし、どちらの節も無ければ独自の HTTP の API のままです。構成は `deploy/h248/`、確認は `scripts/e2e-h248.sh` で、音声通話のシナリオを H.248 の制御で通し、キャプチャの megaco のフレームを tshark で読みます。Mp は Mp のコマンドを持つので、MRF の通話の制御は独自の API のままです。調査は [h248.md](background/h248.md) にあります。

**完了条件.** CI の試験で、音声通話と MRF の通話のシナリオが H.248 の制御でも成功し、pcap で Wireshark が H.248 のメッセージをデコードできる。

### 5GC（VoNR）

**できるようになること.** 5GC のスタブを含めた構成で、VoNR の手順を追える。P-CSCF は Rx の代わりに N5 で PCF と通信する。

- 5GC のスタブを作る。PDU セッションの確立で、UE に IP アドレスを払い出し、P-CSCF のアドレスを通知する。
- PCF のスタブを作り、P-CSCF との間で N5（TS 29.514、HTTP/2 の SBI）を使う。VoLTE で作ったアクセス網の境界に、Rx と並ぶ 2 つ目の実装として加え、EPC と 5GC を設定で選べるようにする。
- HSS に Nhss（TS 29.562）を加える。
- GSMA NG.114 の必須の要件を満たす。IR.92 との違いは [profiles.md](background/profiles.md) にまとめてある。
  - EVS のコーデックを加える（[codecs.md](design/codecs.md)）。先に利用条件と特許の扱いを確かめる（[requirements.md](requirements.md) の未決事項）。
- 手順書「VoNR の呼の流れ」を書く。

今あるもの。5GC のスタブ（`internal/fivegc`）、N5 と Nhss の型と骨組み（`internal/n5`、`internal/nhss`、`internal/sbi`）、EVS の RTP ペイロードの形式（`internal/evs`）の部品があります。役割にはつないでいません。調査は [5gc.md](background/5gc.md) にあります。

**完了条件.** CI の試験で、5GC のスタブと N5 を使い、Rx を使わない構成で、登録と通話が成功する。

### SMS over NAS

**できるようになること.** SMS が IMS を通らずに、EPC と 5GC の NAS で届く流れを追える。IP-SM-GW が IP と NAS のどちらで届けるかを選び、IP で届かないときに NAS へ切り替える様子を見られる。

- SMS in MME に対応する（TS 23.272 Annex C）。EPC のスタブの MME が NAS で SMS を運び、SC と SGd で通信する。
- 5GS の SMS over NAS に対応する（TS 23.502 4.13.3）。5GC のスタブに AMF と SMSF を置き、SMSF が SC と SGd で通信する。
- UE シミュレータと EPC、5GC のスタブの間の NAS を、SMS に要る範囲で作る（TS 24.301、TS 24.501）。CP 層（TS 24.011）は今の RP 層と TP 層の符号化（`internal/sms`）の下に加える。
- IP-SM-GW に、届ける経路の選択（ドメイン選択）と、IP で届かなかったときの NAS への切り替えを加える（TS 23.204 6.4、6.5a）。経路の選択には、IP-SM-GW が Sh で HSS に登録した情報を使う。
- 手順書「SMS を NAS で届ける」を書く。

今あるもの。NAS（`internal/nas`）の部品があります。SC は役割 `sc` になっていて、IP-SM-GW と SGd で、HSS と S6c でやり取りします。IMS を通る SMS は、この SC がショートメッセージを預かり、配達し、届かなければ HSS の知らせを待って送り直します（[sms.md](design/sms.md) の「SC の役割の作り」）。NAS の側はまだありません。調査は [sms-nas.md](background/sms-nas.md) にあります。

**完了条件.** CI の試験で、SMS in MME の経路と 5GS の経路の両方で SMS が NAS で届く。IMS に登録していない UE に宛てた SMS で、IP-SM-GW が IP から NAS に切り替える。pcap で Wireshark が NAS と SGd のメッセージをデコードできる。

### 運用と保守（OAM）

**できるようになること.** OAM のサーバーから IMS を保守し、運用できる。障害のあとにデータを戻し、網が正しく動いていることを確かめられる。

- 各ノードに保守用の API を作る。状態、設定、ドレイン、ログの詳しさを扱う。
- OAM のサーバーを作る。学習用の画面とは分ける（[lab-oam.md](design/lab-oam.md)）。
- 加入者データのプロビジョニングを、1 件ずつと一括の両方で行えるようにする。
- 加入者データとノードの状態のバックアップとリストアを作る。冗長構成の障害の観察と組み合わせ、障害、リストア、確認までの流れを手順書にする。
- 稼働状態、指標、警報を見られるようにする。指標は Prometheus の形式で公開し、Grafana で見る。OAM の画面は簡易なフロントエンドで作る。
- 呼ごとの信号の流れと、失敗した呼の理由ごとの件数を見る解析の機能を作る。
- 試験用の利用者を指定し、試験用の利用者だけの収容と、機能ごとの試験用の利用者だけへの有効化を作る。
- 設定の変更の記録と、前の設定への戻しを作る。
- 手順書「OAM で運用する」を書く。

今あるもの。OAM のサーバー（`internal/oam`）は `ims` コマンドの役割 `oam` で動き、同じ待ち受けで画面（`web/oam`）も配ります。Compose の `oam` のサービスが、保守用の API を出す 10 台の一覧と HSS のプロビジョニングの URL を持ち、設定の履歴とバックアップを名前付きのボリュームに置きます。この API は設定を変え、資格情報の入った書庫を渡すので、ホストには公開していません。確認は `scripts/e2e-oam.sh` で行います。一括の登録、HSS がその加入を持つこと、その加入者の登録、バックアップと復元を確かめます。保守用の API（`internal/maintenance`）は P-CSCF、I-CSCF、S-CSCF、HSS、MMTel AS、MRFC、IP-SM-GW、SMS のサービスセンタ、メディア中継ノード、MRFP に、指標（`internal/metrics`）はこのうち MRFC と 2 つのメディアの役割を除く 7 つにつないであります。`maintenance.listen` を持つノードは、その待ち受けで `/metrics` を出し、保守用の API につないだ役割を持てばその API も出します。P-CSCF と S-CSCF は、登録の試行、成功、失敗を種類ごとに数えます（TS 32.409 4.1.1）。登録の無い利用者の登録解除に返す 481 も、ほかの応答コードと分けて数えます。P-CSCF は、最初の登録にかかった時間（4.1.1.1.4）と、セッションの試行、成功、応答、失敗、応答済みのセッションの数（4.1.2）も数えます。I-CSCF はホーム網に入る着信のセッションの確立の試行と成功を数え（4.1.2.11、4.1.2.12）、S-CSCF はセッションの確立の試行と成功を数えます（4.1.2.1、4.1.2.2）。確立が通ったと数えるのは、最初にそれを示す応答（180 か、180 の無いときの 2xx）を送るときです。Cx、Sh、Rx で要求を送る役割（I-CSCF、S-CSCF、MMTel AS、IP-SM-GW、P-CSCF）は、送った要求と返ってきた応答を、コマンドと結果ごとに数えます（4.1.1.5、4.1.1.6、4.1.3、4.1.6）。設定に書いた Diameter の相手ごとに、接続が要求を通せるかも出します。要求を受けて答える側も、同じ指標で数えます。HSS は受けた要求と送った応答（4.2.12）を、S-CSCF は HSS から受ける RTR（4.1.1.3.2）と PPR（4.1.7）を、応答の結果コードごとに数えます。HSS は、持っている加入者と公開識別子の数（4.2.1.1）と、そのうち登録の状態にあるものの数（4.2.1.2）も出します。加入者をプロセスの外の保存先に置いた HSS は、どれも出しません。数は保存先のもので、1 台が自分の操作から出せるものではないためです。shut down か lock をしたあいだ、S-CSCF はダイアログの外の新しい要求に、P-CSCF は登録を始める REGISTER と更新する REGISTER に、I-CSCF はすべての要求に、MMTel AS はダイアログの外の INVITE と購読を始める SUBSCRIBE、それに Ut の新しい要求に、MRFC はダイアログの外の INVITE に、IP-SM-GW は第三者 REGISTER と新しい短いメッセージの MESSAGE に、503 と Retry-After を返します。MRFC は、流している音声ガイダンスと、この台を通って混合にいる参加者を残します。IP-SM-GW は配達の報告と reg event の NOTIFY を扱い続けます。メディア中継ノードと MRFP は、新しいコンテキストを作る `Add` に Retry-After の無い 503 を返し、持っているコンテキストへの操作を続けます。HSS は Diameter の相手に DPR を送って接続を閉じ、`UNLOCKED` でないあいだは 3868 で待ち受けません（[state-redundancy.md](design/state-redundancy.md) の「HSS のドレインで何を返すか」）。サービスセンタも、serving node が開いた接続に DPR を送って閉じ、`UNLOCKED` でないあいだは 3868 で待ち受けません。始めた配達の試行は終え、再送と次のショートメッセージは保存先を通して運用中の台に任せます（同じ文書の「SC のドレインで何を返すか」）。Compose では Prometheus が 7 つのノードの指標を集め、Grafana のダッシュボードに出ます（`deploy/observability/`）。MRFC、メディア中継ノード、MRFP のノードは、保守用の API のために同じ待ち受けを持つので、指標を 1 つも出さないまま up に見えます。ダッシュボードには、登録の成功率とセッションの確立の成功率の欄があります。HSS を相手に持つ I-CSCF、S-CSCF、IP-SM-GW、MMTel AS、サービスセンタは、HSS のインスタンスに接続し直せなくなると、そのインスタンスについての警報を保守用の API の警報の一覧に上げ、接続が戻ると解除します。確認は `scripts/e2e-maintenance-hss.sh` で行います。ほかの役割はまだつないでいません。OAM は、ノードから集めた警報を、TS 28.111 の故障の監視と TS 28.532 の ProvMnS の形で OSS に渡します。警報の一覧の読み出し、確認、`NtfSubscriptionControl` による購読と通知です（[fm-northbound.md](design/fm-northbound.md)）。確認は `scripts/e2e-fm.sh` で行い、OSS の代わり（`cmd/oss`）が購読し、HSS を止めて上がる警報の通知、確認、解除を受け取ります。調査は [oam.md](background/oam.md) にあります。

**完了条件.** CI の試験で、OAM から加入者を一括で登録し、HSS を止めてデータを戻したあとに登録と通話が成功する。試験用の利用者だけに有効にした機能が、ほかの利用者には効かないことを確かめられる。

### Kubernetes

**できるようになること.** kind などのローカル Kubernetes で、Docker Compose と同じシナリオを動かせる。

- マニフェスト（Helm または Kustomize）を用意する。
- DNS の構成を選べるようにする。クラスタの DNS（CoreDNS）にゾーンを載せる構成と、外付けの DNS サーバーを使う構成を用意する。Docker Compose でも、DNS コンテナを使わず Docker のネットワークの DNS だけで動く構成と、外付けの DNS サーバーを使う構成を用意する。
- SIP の UDP 通信と Record-Route のアドレスが、Service と Pod の構成で正しく動くようにする。
- Kubernetes 上でのキャプチャ手順を書く。

今あるもの。Kustomize のマニフェスト、2 つの DNS の構成、キャプチャ、kind で登録、発着信、SMS を確かめる `deploy/k8s/e2e-kind.sh` が `deploy/k8s/` にあり、CI の `kind` のジョブが動かします（[deploy/k8s/README.ja.md](../deploy/k8s/README.ja.md)）。調査は [k8s.md](background/k8s.md) にあります。

**完了条件.** kind 上で登録、通話、SMS の試験が成功する。

### クラウド

**できるようになること.** AWS や Azure のようなクラウドに IMS を置き、インターネット越しの UE から登録と通話ができる。

- クラウドの Kubernetes（EKS、AKS など）に、Kubernetes 用のマニフェストで載せる。
- クラウドの資源（ネットワーク、クラスタ、ロードバランサー）を作る定義をコードで書く。
- SIP と RTP の UDP を通すロードバランサー、公開アドレス、NAT を、クラウドごとの設定として分ける。
- 手順書「クラウドで動かす」を書く。費用がかかることと、使い終わったら資源を消す手順も書く。

今あるもの。OpenTofu で書いた EKS と AKS の定義が `deploy/cloud/` に、クラウド用の overlay が `deploy/k8s/overlays/` にあります。どちらも実際のクラウドでは動かしていません（[deploy/cloud/README.ja.md](../deploy/cloud/README.ja.md)）。調査は [cloud.md](background/cloud.md) にあります。

**完了条件.** CI で資源の定義の検査（文法と計画）が通る。実際のクラウドへの配置は費用がかかるので、メンテナーが確認してから手で行い、登録と通話が成功することを確かめる。

### スライド

**できるようになること.** 初学者向けの勉強会をこのリポジトリのスライドで開ける。

- スライドをいくつかのデッキに分けて作る。候補は次のとおり。
  - Go と Rust の簡単な説明。
  - このリポジトリのパッケージ構成。
  - 標準と対応させた IMS の説明（登録、通話、SMS、VoLTE、付加サービス、MRF、相互接続、障害と復旧、VoNR）。
  - このリポジトリの個別の実装の説明。
- 元データを Markdown などのテキストで管理し、HTML で表示し、pptx を出力する仕組みを選ぶ。自前の生成の道具にした（[implementation.md](design/implementation.md)）。
- 各段階の手順書とラダー図をスライドから参照する。

今あるもの。Go、パッケージ構成、網の地図、キャプチャとラボ、登録、通話、SMS、付加サービス、MRF、VoLTE、2 つの網の相互接続のデッキが英語と日本語で `slides/decks/` にあり、CI の `slides` のジョブが HTML と pptx を作ります（[slides/README.ja.md](../slides/README.ja.md)）。調査は [slides.md](background/slides.md) にあります。

**完了条件.** CI でスライドの HTML と pptx が生成され、成果物として保存される。

## 拡張の候補

上の段階が終わったあと、必要に応じて取り組む候補です。

- Open5GS と UERANSIM を使う構成。
- ブラウザから通話できる WebRTC の UE。
- PSTN への発信（BGCF、MGCF）。
- TLS を有効にした構成と、暗号化された信号を Wireshark で復号する手順。IMS-AKA の IPsec は `deploy/esp/` の構成で有効にでき、暗号化が null なので復号は要らない。秘匿までかける構成は、ここに残っている。
- 網の側の MWI（TS 24.606）と ECT（TS 24.629）。IR.92 は網の対応を任意としている。
- 保存先の冗長（Valkey の Sentinel、クラスタ）。
- SLF（Dx）。加入者を別々の HSS に分けて置くときに要る。
- P-CSCF の網からの復旧（TS 23.380 5.1.2、5.4、5.5）。
- SMS over SGs。MSC が必要で、CS 網は対象外なので、段階には入れない。
