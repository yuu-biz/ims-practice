# IMS の仕組みを実装から読む

[English](../en/README.md)

3GPP の IMS を学習用に実装したこのリポジトリを題材に、IMS が何をどう決めているかを、図と短いコードで説明する本です。読むだけで分かるように書いています。構成を動かしたり、問いに答えたりする必要はありません。自分で動かしたい人のために、各章の終わりと 自分で動かす に、手順書への案内があります。

読む順は 3 つあります。どれも前から順に読めますが、必要なところから読んでも構いません。各章は、前提にする章を最初に挙げます。

## 入門（全体像）

IMS のノードが何を受け持ち、登録と通話で何が決まるかを、最小の地図と代表的な信号で見ます。

1. [この本の読み方](preface.md)
2. [IMS が受け持つ仕事](overview.md)
3. [信号を観測する](observing.md)
4. [登録で決まる 3 つのこと](registration.md)
5. [番号から相手の電話へ](call.md)
6. [アプリケーションサーバーが加えるサービス](services.md)
7. [携帯のアクセスと、別の事業者の網](access-and-borders.md)

## 中級（仕様の条件と実装の対応）

条件が変わると、どの判断、状態、メッセージが変わるかを、仕様の条項と、判断するコードと、それを確かめる試験で追います。

- 登録
  - [REGISTER が 2 回送られる理由](registration-two-registers.md)
  - [Path と Service-Route の使い分け](registration-routes.md)
  - [遅れて届いた登録の解除が、新しい登録を消さない理由](registration-stale-deregistration.md)
  - [登録の更新、解除、購読、網からの取り消し](registration-lifecycle.md)
- [通話のトランザクション、ダイアログ、メディアの交渉](call-signaling.md)
- [iFC と MMTel AS の付加サービス](mmtel.md)
- [SMS の配達と報告](sms.md)
- [MRF の音声ガイダンス、呼び出し音、会議](mrf.md)
- [VoLTE の登録と通話](volte.md)
- [網間接続](interconnect.md)

## 上級（仕様を踏まえた設計の判断）

標準が決めていることと、実装や運用が選ぶことを分け、選べる方法を比べて、この実装の判断と限界を説明します。各章は、前提にする中級の章を挙げます。

- [登録の順序と期限](design-registration-order.md)
- [複数台と障害で引き継ぐもの](design-shared-state.md)
- [メディアの再交渉が失敗したとき](design-media-rollback.md)
- [保守で止めることと、突然失うこと](design-maintenance.md)

## 付録

- [コードを読むための Go](go-reading.md)

## スライドと手順書

同じ問いを図と段階で見せるスライドが [slides/](../../../slides/README.ja.md) にあり、各章は対応するシーンを挙げます。構成を動かす手順と、キャプチャで見るヘッダの一覧は、手順書 [docs/guide/ja/](../../guide/ja/README.md) にあります。
