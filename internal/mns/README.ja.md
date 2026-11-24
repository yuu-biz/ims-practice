# 3GPP の管理のサービス

[English](README.md)

このパッケージは、OSS が網の警報を読み、確認し、新しい警報を知らされるときの JSON の本文を持ちます。使うのは 3GPP の管理のサービス（MnS）です。故障の監視は TS 28.111 が定めます。それを運ぶのは、管理するオブジェクトへの汎用の操作で、TS 28.532 のプロビジョニングのサービス（`ProvMnS`）を TS 32.158 の HTTP の決まりで使います。OSS は `SubNetwork` の `AlarmList` を読み、警報の記録を書き換えて確認し、`NtfSubscriptionControl` を作って警報の通知を受けます。

属性の名前は、3GPP の OpenAPI のファイルのとおりです。ファイルはリポジトリに写していません。試験が、ファイルから書き写したスキーマごとの名前を持ち、Go の型をそれと照らします。ファイルの名前と版は、パッケージのコメントにあります。

```sh
go test ./internal/mns
go doc ./internal/mns
```

## このパッケージで簡略化していること

どの行の場所にも、コードに `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| パッケージ `mns`、TS 28.111 6.1 | 警報へのコメント、警報の相関、警報の履歴と、その通知と属性。セキュリティの警報、しきい値の警報の属性、MnS consumer が消す警報。 | ノードはどの警報も自分で上げて自分で消し、しきい値の警報を上げず、加入者の名前を出しません。セキュリティの警報は加入者を名指しします。警報の履歴は、OpenAPI のファイルにありません。 |
| `AlarmType`、TS 28.111 7.3.1.3、8.2.2 | セキュリティの警報の 5 つの種類。 | どのノードもセキュリティの警報を上げません。 |
| `ProbableCause` と `SpecificProblem`、TS 28.111 7.4.1 | どちらも、意味をベンダーが決める整数の形。 | ノードはどちらも文字列で書きます。 |
| `AlarmRecord`、TS 28.111 7.3.1.2 | ノードが持たない任意の属性（`backedUpStatus`、`trendIndication`、`monitoredAttributes` など）と、このパッケージが省いた部品の属性。 | どのノードの警報の一覧も、これらを持ちません。 |
| `Top`、TS 28.622 4.3.29 | オブジェクトの下に置くベンダー独自のデータ `VsDataContainer`。 | ここのオブジェクトは、どれも持ちません。 |
| `AlarmListAttributes`、TS 28.111 7.3.2.2、6.13 | 一覧を止める `administrativeState`。 | 任意の属性で、ここでは何も一覧を止めません。 |
| `NotificationHeader`、TS 28.532 11.0.2 | `sequenceNo` と `subscriptionId`。 | 通知を確実に届ける仕組みを持つ MnS producer だけが入れます。その仕組みは作っていません。 |
| `NotifyNewAlarm`、`NotifyClearedAlarm`、`NotifyChangedAlarmGeneral`、TS 28.111 8.2.2、8.3.2、8.4.2 | `AlarmRecord` が省いた属性を運ぶパラメーターと、変わる前の値を運ぶ `changedAlarmAttributes`。 | 記録がこれらを持ちません。 |
| `NtfSubscriptionControlSingle`、TS 28.622 4.3.21 | 購読の下に置ける `HeartbeatControl`。 | 故障の監視には heartbeat の通知が要りません。 |
| `Scope`、TS 28.622 4.3.23 | 購読の範囲を `dataNodeSelector` で書く形。 | ここの購読は、`notificationFilter` で通知を選びます。 |
| `ScopeType`、TS 32.158 6.1.2 | `BASE_NTH_LEVEL` と `BASE_SUBTREE`。 | 警報の一覧も購読も下にオブジェクトを持たないので、範囲はそのオブジェクトだけか、`SubNetwork` の下のすべてです。 |
