# 自分で動かす

この本は、構成を動かさなくても読めるように書いています。この付録は、本で読んだ信号を自分のキャプチャで見たい人のための案内です。手順そのものは手順書にあり、ここでは章ごとに、どの手順書のどの節を開けばよいかをまとめます。

## 要るもの

Docker と Docker Compose、キャプチャを開く Wireshark があれば動きます。ホストに Go を入れる必要はありません。最初のビルドには数分かかります。手順書の入口 [docs/guide/ja/README.md](../../guide/ja/README.md) の「最初のコマンド」で構成を起動し、1 回の登録を流せます。

動かさずにキャプチャだけを見たいときは、このリポジトリの CI が、シナリオごとのキャプチャを成果物に残しています。`registration-capture`、`sms-capture`、`mmtel-capture`、`mrf-capture`、`volte-capture`（AMR の録音と UE のログも）、`interconnect-capture`（両方の網と網のあいだ）です。

## 章ごとの手順書

| 章 | 開く手順書と節 |
| --- | --- |
| [IMS が受け持つ仕事](overview.md) | [手順書の入口](../../guide/ja/README.md) の「Compose のプロジェクトが起動するもの」 |
| [信号を観測する](observing.md) | [信号をキャプチャして Wireshark で開く](../../guide/ja/capture.md)、[ラボで IMS を見る](../../guide/ja/lab.md) |
| 登録の章（[登録で決まる 3 つのこと](registration.md) から [登録の更新、解除、購読、網からの取り消し](registration-lifecycle.md) まで） | [UE が IMS に登録する流れを追う](../../guide/ja/registration.md) の「登録を実行する」「キャプチャを開く」 |
| [番号から相手の電話へ](call.md)、[通話のトランザクション、ダイアログ、メディアの交渉](call-signaling.md) | [音声通話の流れを追う](../../guide/ja/call.md) の「発信する」「キャプチャを開く」 |
| [iFC と MMTel AS の付加サービス](mmtel.md) | [MMTel AS の付加サービス](../../guide/ja/mmtel.md) の「動かしてみる」「キャプチャを開く」 |
| [SMS の配達と報告](sms.md) | [SMS の流れを追う](../../guide/ja/sms.md) の「ショートメッセージを送る」「キャプチャを開く」 |
| [MRF の音声ガイダンス、呼び出し音、会議](mrf.md) | [MRF による音声ガイダンス、呼び出し音、会議](../../guide/ja/mrf.md) の「動かしてみる」「キャプチャを開く」 |
| [VoLTE の登録と通話](volte.md) | [VoLTE の呼の流れ](../../guide/ja/volte.md) の「動かしてみる」「キャプチャを開く」 |
| [網間接続](interconnect.md) | [IMS 網どうしを相互接続する](../../guide/ja/interconnect.md) の「動かす」 |
| 上級の章 | 各章の「自分で動かす場合」。複数台の構成は [手順書の入口](../../guide/ja/README.md) の「利用者の状態をノードの外の保存先に置く」 |

## 試験を動かす

章の「この説明の根拠」に挙げた試験は、Go の試験です。ホストに Go があれば `GOTOOLCHAIN=auto go test -timeout 500s ./internal/<パッケージ>/ -run <試験の名前>` で、無ければ手順書の入口にあるコンテナの Go で動かせます。どの試験も IMS の全体か一部を 1 つのプロセスの中で動かし、ネットワークの準備は要りません。

## 片付け

手順書はどれも、最後に起動したものを止める手順を持っています。全体をまとめて止める方法は、[手順書の入口](../../guide/ja/README.md) の「片付け」にあります。`-v` を付けたコマンドは、キャプチャ以外の保存データ（ボリューム）を消します。
