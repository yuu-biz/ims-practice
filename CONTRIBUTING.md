# 開発の進め方

Issue、ブランチ、コミット、PR のルールです。人間の開発者と AI エージェントの両方が従います。

## 開発の記録

開発者とエージェントが読む文書です。学習者向けの README、手順書、用語集、コードからは、これらを指しません。

- [AGENTS.md](AGENTS.md)。AI エージェントと開発者が守るルール。
- [要件定義](docs/requirements.md)。目的、作るもの、作らないもの、未決事項。
- [ロードマップ](docs/roadmap.md)。できていることと、これから作るもの、それぞれの完了条件。
- [設計の記録](docs/design/README.md)。主題ごとに、何を選び、なぜそうしたか。
- [調査の記録](docs/background/)。GSMA のプロファイルと国内標準の調査（[profiles.md](docs/background/profiles.md)）と、主題ごとの調査（[SMS](docs/background/sms.md)、[VoLTE](docs/background/volte.md)、[MMTel と MRF](docs/background/mmtel-mrf.md)、[網間接続](docs/background/interconnect.md)、[冗長構成](docs/background/redundancy.md) など）。
- [確かめられなかった条項](docs/unverified-clauses.md)。条項番号を確かめられなかった記述の一覧。

## Issue とブランチと PR

`main` は常に起動と試験が通る状態に保ちます。

- 作業は Issue から始めます。1 つの Issue を 1 つの PR で閉じます。
- `main` へ直接 push しないでください。変更はすべて PR で入れます。
- CI が通るまで、PR をマージしません。PR を出す前に、ビルドと試験を手元で実行します。
- マージの条件は CI の `all-passed` のチェックです。`ci` のワークフローのすべてのジョブの結果をまとめ、失敗か取り消しが 1 つでもあれば落ちます。
- 変わったファイルが文書（`.md`、`docs/`、ライセンスのファイル）だけなら、`ci` のワークフローは文書の検査（`docs` のジョブ）だけを流し、Go の試験と e2e を飛ばします。ただし、試験が読む手順書が変わったときは、Go の試験と e2e を含むすべてのジョブを流します。`docs/guide/` の `en` と `ja` の `capture.md` と `README.md` は、`deploy/compose` の Go の試験が Compose の定義と突き合わせます。`docs/guide/en/` の `mmtel.md`、`mrf.md`、`oss.md`、`sms.md`、`volte.md` は、それぞれのシナリオの e2e のスクリプトが、表に書いた表示フィルタをキャプチャに当てます。読むのは英語版だけで、日本語版のフィルタが同じであることは `docs` のジョブが確かめます。飛ばしたジョブは成功したものとして数えます。
- `kind` と `slides` のワークフローは、関わるファイルが変わったときだけ動きます。マージの条件には入っていませんが、動いたときは通してからマージします。
- Issue、PR、コミットメッセージは公開されます。開発の記録と同じく、番号で引く書き方、決めた日付、人や作業の分担を指す言葉を書きません（[AGENTS.md](AGENTS.md) の「開発の記録と、学習者が読むものを分ける」）。
- ブランチ名は `<type>/<内容>` にします。`<type>` にはコミットメッセージと同じ種類を使います。例は `feat/cx-mar` や `docs/registration-guide` です。
- 1 つの PR にはロードマップの 1 項目程度のまとまりを入れます。大きくなるときは、順番に積み重ねる複数の PR に分けます。
- PR のマージには「Rebase and merge」を使います。Squash すると、下に書くコミットの単位が 1 つにまとまって失われるためです。
- PR のタイトルはコミットメッセージと同じ形式で、英語で書きます。説明は [PR テンプレート](.github/pull_request_template.md) の見出しに沿って日本語で書きます。

## コミットの単位

1 つのコミットには、検証できる変更を 1 つだけ入れます。

- どのコミットでも、ビルドと試験が通る状態にします。`git bisect` で問題のコミットを探せるようにするためです。
- 目安は仕様の手順 1 つ、またはメッセージ 1 種類への対応です。たとえば「Cx の MAR を送り MAA を処理する」で 1 コミットにします。
- 試験は対象のコードと同じコミットに入れます。バグ修正では、修正と、そのバグを再現する試験を同じコミットに入れます。
- 振る舞いを変える変更と、振る舞いを変えない変更（名前の変更、ファイルの移動、整形）は、別のコミットにします。
- 依存関係の追加と更新は単独のコミットにします。
- 信号の流れを変えたときは、手順書とラダー図の更新を同じコミットに入れます。
- 自動生成したファイルは生成元の変更と同じコミットに入れ、生成に使ったコマンドを本文に書きます。

## コミットメッセージ

[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) の形式を使い、英語で書きます。

```text
<type>(<scope>): <subject>

<body>

Refs: <spec number and clause>
```

`<type>` は次のどれかです。

| type | 使う場面 |
| --- | --- |
| `feat` | 機能の追加 |
| `fix` | バグの修正 |
| `refactor` | 振る舞いを変えないコードの変更 |
| `test` | 試験だけの追加と変更 |
| `docs` | ドキュメントだけの変更 |
| `build` | ビルド、依存関係、コンテナイメージの変更 |
| `ci` | CI の変更 |
| `chore` | 上のどれにも当たらない雑務 |

`<scope>` は変更する部分の名前です。`pcscf`、`icscf`、`scscf`、`hss`、`epc`、`ue`、`media`、`lab`、`sip`、`diameter`、`config`、`ims`（`cmd/ims`）、`capture`、`deploy`、`cloud`、`fault`、`chaos`、`ibcf`、`slides`、`docs` などを使います。コードの構成が決まったら、この一覧を更新します。

- 件名は命令形で、何をしたかを 1 行で書きます。末尾にピリオドを付けません。
- 本文にはなぜその変更をしたかを書きます。件名の繰り返しは書きません。
- 仕様の手順を実装したときは、`Refs:` 行に仕様番号と条項を書きます。複数あるときは `Refs:` 行を複数書きます。
- AI エージェントが作ったコミットには `Co-Authored-By:` の行を残します。会話へのリンクは書きません。公開の履歴に、非公開の会話の場所を残さないためです。

例:

```text
feat(scscf): send SAR to the HSS on initial registration

After authentication succeeds, the S-CSCF tells the HSS that it now
serves the user. The answer carries the user profile, including the
iFCs.

Refs: TS 24.229 <clause for S-CSCF registration>
Refs: TS 29.228 <clause for SAR>
```

例の `<...>` には実際に確認した条項番号を書きます。

## 言語

文書とメッセージの言語は、対象ごとに分けます。一覧は [AGENTS.md](AGENTS.md) の「言語」にあります。学習者向けの文書（手順書、用語集、README）は英語と日本語の両方で用意します。コードのコメント、コミットメッセージ、PR のタイトルは英語です。Issue と PR の説明、プロジェクトの管理文書は日本語です。

コードのコメントを英語にしたのは、英語版と日本語版の両方の学習者が同じコードを読むためです。コメントを 2 言語で書くと、片方だけが古くなります。

## 試験用のデータ

IMSI、MSISDN、鍵（K、OPc）は試験用の値だけを使います。詳しくは [AGENTS.md](AGENTS.md) を参照してください。
