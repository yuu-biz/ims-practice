# ims-practice

[English](README.md)

ims-practice は 3GPP の IMS（IP Multimedia Subsystem）を学習用に実装するプロジェクトです。手元のマシンで起動して、電話や SMS の最初の SIP メッセージから最後のメッセージまでを、Wireshark とダッシュボードで追えるようにします。

## 学べること

コアネットワークを初めて学ぶ技術者のための教材です。次の 3 つを並べて見られるようにします。

- 端末の登録、通話、SMS のときに、どのノードがどのメッセージをどの順番で送るか。
- それぞれのメッセージが 3GPP 仕様のどこで規定されているか。
- 仕様の記述をコードでどう実装しているか。

## 現在の状態

UE シミュレータは SIP Digest で登録し、電話番号で別の UE に発信できます。呼は P-CSCF、S-CSCF、I-CSCF、ENUM、メディア中継ノードを通ります。SIP、Diameter、DNS、RTP のパケットは、すべてキャプチャで見られます。http://localhost:8080 のダッシュボードは、HEP の写しから呼と登録ごとのラダー図を描きます。HSS の加入者の管理と、2 台の UE シミュレータの操作もできます。UE どうしで SMS over IP のショートメッセージを送り合えます。MMTel AS は、識別子の通知、着信転送、規制などの補足サービスを提供し、UE はそれを Ut で設定します。VoLTE の UE は EPC のスタブに attach し、IMS-AKA で登録し、プリコンディションを使って AMR-WB で通話します。P-CSCF は通話ごとのメディアを Rx で PCRF に許可させます。

## 構成

```text
 UE シミュレータまたはソフトフォン
      │ SIP            │ RTP
      ▼                ▼
   P-CSCF ──────────► メディア中継（IMS-AGW の役割）
      │    制御
      ▼
   I-CSCF ──► S-CSCF ──► アプリケーションサーバー（IP-SM-GW、MMTel AS）
      │          │                  │
      │          │                  ▼
      │          │               MRF（MRFC、MRFP）
      └── Cx ────┴──► HSS スタブ
```

- CSCF のすべての役割を 1 つのコンテナイメージで動かします。設定で役割を選ぶので、1 つのノードは P-CSCF、I-CSCF、S-CSCF のどれにもなれ、複数の役割を兼ねることもできます。
- HSS とアクセス網（EPC と PCRF）はスタブです。IMS からは Diameter の Cx、Sh、Rx などの本物のインタフェースに見えるので、IMS を変えずに本格的な実装と入れ替えられます。
- S-CSCF は iFC に従ってアプリケーションサーバーを呼び出します。SMS には IP-SM-GW を、着信転送や規制などの補足サービスには MMTel AS を呼び出します。
- ほかの IMS 網との境界では、IBCF と TrGW がトポロジーを隠し、メディアを中継します。
- 既定の設定では信号を暗号化しません。Wireshark が追加の設定なしで、すべてのメッセージをデコードできます。

## 必要な環境

- Docker と Docker Compose。基準にする環境は Windows の Docker Desktop です。
- Wireshark。キャプチャを読むのに使います。

## ドキュメント

- [信号をキャプチャして Wireshark で開く](docs/guide/ja/capture.md)。
- [UE が IMS に登録する流れを追う](docs/guide/ja/registration.md)。
- [音声通話の流れを追う](docs/guide/ja/call.md)。
- [SMS の流れを追う](docs/guide/ja/sms.md)。
- [MMTel AS の補足サービス](docs/guide/ja/mmtel.md)。
- [MRF による音声ガイダンス、呼び出し音、会議](docs/guide/ja/mrf.md)。規制した通話の前の音声ガイダンス、CAT、割込通話、3 人の会議。
- [VoLTE の呼の流れ](docs/guide/ja/volte.md)。EPC スタブ、IMS-AKA、Rx でのポリシー制御と、プリコンディションを使った AMR-WB の通話。
- [IMS 網どうしを相互接続する](docs/guide/ja/interconnect.md)。境界の IBCF と TrGW、構成の秘匿、相互接続プロファイルと食い違ったときの動き、網をまたぐショートメッセージ。
- [ダッシュボードで IMS を見る](docs/guide/ja/dashboard.md)。2 台の UE シミュレータもここから操作できます。
- [用語集](docs/glossary.md)。IMS で使う略語の英語と日本語の説明。
- [開発の進め方](CONTRIBUTING.md)。コミットのルールと、開発の記録の一覧。

## ライセンス

コード、設定、スクリプトのライセンスは [Apache License 2.0](LICENSE) です。手順書、用語集、スライドのライセンスは [CC BY 4.0](LICENSE-docs) です。ほかの人が作った部品とデータのファイルは [NOTICE](NOTICE) にまとめました。

この 2 つのライセンスが許すのは、このリポジトリの著作物の利用です。ほかの人の特許の許諾は含みません。たとえば、3GPP の仕様の標準必須特許や、AMR、AMR-WB、EVS のコーデックの特許です。このコードをもとに製品を作るときは、要る特許の許諾を確かめてください。

このプロジェクトを使うことを、だれかに知らせる義務はありません。製品や商用のサービスで使ったときは、Issue で知らせてもらえるとうれしいです。
