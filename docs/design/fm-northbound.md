# OSS に警報を渡す口（故障管理の northbound）

この記録は、OAM がノードから集めている警報を、網の管理の仕組み（OSS、TS 32.101 の Network Manager）に標準の形で渡す口を決めます。対象は、警報の一覧の読み出し、確認（acknowledge）、警報の通知の 3 つです。OAM とノードのあいだ（southbound）は、今の独自の保守用の API のままです。調べた結果は [oam.md](../background/oam.md) の 15 章にあります。

「（案）」を付けたところは、まだ決めていません。「決めていないこと」の表に、論点の名前と勧める案があります。本文は、勧める案で書いています。

## 選んだこと

- 3GPP SA5 の故障管理（TS 28.111 V20.0.0）を、TS 28.532 V20.2.0 の汎用の provisioning の操作（ProvMnS）の REST の形で出します。経路と型の名前は、仕様書の書庫にある OpenAPI に合わせます。`TS28111_FaultNrm.yaml`（`info.version` は 19.3.0）、`TS28111_FaultNotifications.yaml`（19.3.0）、`TS28532_ProvMnS.yaml`（19.2.0）、`TS28623_SubscriptionControlNrm.yaml`（19.4.0）、`TS28623_ComDefs.yaml`（19.6.0）です。
- OAM を、1 つの SubNetwork を受け持つ MnS producer にします。警報の一覧（`AlarmList`）は SubNetwork の下に 1 つだけ置き、すべてのノードの警報をそこにまとめます。TS 28.111 6.11 が例に挙げる、管理する装置とのつながりを失う subnetwork manager の形です。
- 作る操作は 3 つです。`AlarmList` の読み出し（`GET`）、確認と確認の取り消し（`PATCH`）、通知の購読（`NtfSubscriptionControl` の `PUT`、`GET`、`DELETE`）です。通知は、購読に書かれた宛先に `POST` で送ります。
- Release 17 までの `TS28532_FaultMnS.yaml`（`/alarms`、`/subscriptions` などの専用の経路）は使いません。TS 28.532 の変更の履歴によると、FM の購読の操作は 18.0.0 で非推奨になり、FM に関わる部分を削って改める変更が 18.2.0 に入り、`TS28532_FaultMnS.yaml` への参照は 18.3.0 で消えました。今の版では、警報の一覧は NRM の 1 つのクラスで、読み書きは ProvMnS の汎用の操作で行います（TS 28.111 6.2、6.6）。
- TM Forum の TMF642（Alarm Management）には合わせません。理由は「採らなかった案」にあります。
- 通知を送るのは、ノードを見張る OAM のインスタンスです。どのインスタンスがどのノードを見張るかは、共有の保存先に置いたリースで決めます。インスタンスは、利用者の状態も、どこまで送ったかもプロセスの中に持ちません。

## OSS との境目

TS 32.101 V19.0.0 5.1.1 は、管理のインタフェースを種類で分けています。NE と EM のあいだが種類 1、EM と NM のあいだが種類 2 で、種類 2 を Itf-N と呼び、標準にする主な対象にしています。5.1.2.2 は、種類 2 では情報モデルをプロトコルから切り離して決め、いくつかのプロトコルに写せるようにする方針を書いています。TS 32.102 V19.0.0 7.3.2 も、Itf-N を開いたものとし、情報モデルを標準にするとしています。

この実装の OAM は EM に当たります（`internal/oam/doc.go`）。ノードとのあいだ（種類 1）は、TS 32.600 4.2.1.3 が製品に任せているので、独自の保守用の API のままにします。OSS とのあいだ（種類 2）が、この記録で作る口です。新しい枠組み（TS 28.533）では、OAM が MnS producer、OSS が MnS consumer です。

## 経路と操作

`{MnSRoot}` は、OAM の待ち受けの URL に `/3GPPManagement` を付けたものです。Compose では `http://oam:8080/3GPPManagement` になります。`/3GPPManagement` は、TS 32.158 V20.1.0 4.4.2 の `{root}` に当たり、OpenAPI の `MnSRoot` の既定値と同じ綴りです。`/oam/v1/` の独自の API と、同じ待ち受けで並びます。

`{MnSVersion}` は `v19` です。TS 32.158 4.6.2 は、component A の定義のファイルの版の最初の数に `v` を付けたものとしています。FM の component A は ProvMnS で、`TS28532_ProvMnS.yaml` の版は 19.2.0 です。

`{sn}` は設定で与える SubNetwork の名前です。例では `ims` と書きます（案。「SubNetwork の名前」）。

| 操作（TS 28.532） | 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- | --- |
| getMOIAttributes（11.1.1.2、12.1.1.1.3） | `GET` | `{MnSRoot}/ProvMnS/v19/SubNetwork={sn}/AlarmList=1` | 問い合わせ（下の「読み出しの問い合わせ」） | 200。`Resource`（中身は `AlarmList-Single`） |
| modifyMOIAttributes（11.1.1.3、12.1.1.1.4.2） | `PATCH` | 同じ | `Content-Type: application/merge-patch+json` の `Resource`（案。「確認で受ける形」） | 204 |
| createMOI（11.1.1.1、12.1.1.1.2.1） | `PUT` | `{MnSRoot}/ProvMnS/v19/SubNetwork={sn}/NtfSubscriptionControl={id}` | `Resource`（中身は `NtfSubscriptionControl-Single`） | 201（作った）か 200（置き換えた）。作ったものの `Resource` |
| getMOIAttributes | `GET` | 同じ | なし | 200。`Resource` |
| deleteMOI（11.1.1.4、12.1.1.1.5） | `DELETE` | 同じ | なし | 204 |
| 通知 | `POST` | 購読の `notificationRecipientAddress` | `TS28111_FaultNotifications.yaml` の `oneOf` の 1 つ | OSS が 204 を返す |

通知の行は、本文の型を `TS28111_FaultNotifications.yaml` に、宛先への `POST` と 204 の答えを TS 28.532 12.1.1.3.2.4 と TS 32.158 5.5.4 によっています。

- `AlarmList` の id は `1` に固定します。OSS は作れず、消せません（TS 28.111 7.3.2.1）。`PUT` と `DELETE` は 403 で、`type` は `MODIFICATION_NOT_ALLOWED` です。`AlarmList=1` はいつもあるので、`PUT` は作成ではなく置き換えになり、読むだけの属性を書き換えようとすることになります。`reason` は `ATTRIBUTE_NOT_WRITABLE` です（TS 32.158 6.6.5.3.2 は PUT にもこの理由を当てています）。`DELETE` の `reason` は `OBJECT_DELETION_NOT_ALLOWED` です（6.6.5.4）。
- 購読を作るのは `PUT` だけにします。TS 28.532 12.1.1.1.2.1 は、特に断りが無い限り、作るときは `PUT` を使うとしています。購読をやめるときは、その `NtfSubscriptionControl` を消します（TS 28.622 V20.4.0 4.3.22.1）。TS 28.532 のファイルの報告のサービスも、購読を同じ形の createMOI と deleteMOI で行うとしています（11.6.1.3、11.6.1.4）。
- これ以外の経路と方法は 404 か 405 です。SubNetwork そのもの、ManagedElement、`scope` で木をたどる読み出しは出しません（「作らないもの」）。

### `AlarmList` の中身

`GET` の答えは次の形です。値は Compose の例です。

```json
{
  "id": "1",
  "objectClass": "AlarmList",
  "objectInstance": "SubNetwork=ims,AlarmList=1",
  "attributes": {
    "operationalState": "ENABLED",
    "numOfAlarmRecords": 1,
    "lastModification": "2026-11-24T10:15:02Z",
    "alarmRecords": {
      "scscf.9f3c2a7b1d4e6058": {
        "objectInstance": "SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
        "notificationId": 4711,
        "alarmRaisedTime": "2026-11-24T10:15:02Z",
        "alarmType": "COMMUNICATIONS_ALARM",
        "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
        "specificProblem": "Diameter peer down",
        "perceivedSeverity": "MAJOR",
        "additionalText": "no answer to 3 DWR",
        "ackState": "UNACKNOWLEDGED"
      }
    }
  }
}
```

| 属性（TS 28.111 7.3.2.2） | 中身 |
| --- | --- |
| `operationalState` | いつも `ENABLED`。必須の属性です。一覧の中身は答えたノードの警報の記録で、OAM の側には一覧を止める理由が無いためです |
| `administrativeState` | 出しません。任意の属性で、一覧を lock する操作を作らないためです |
| `numOfAlarmRecords` | 答えたノードの一覧の警報の記録の数の和。絞り込みの前の数です |
| `lastModification` | 答えたノードの `last_modification` のうち、いちばん新しいもの |
| `alarmRecords` | 警報の id を鍵にした、警報の記録の対応表。OpenAPI は id を記録の属性ではなく対応表の鍵にしています（`AlarmRecord` の説明） |
| `unreliableAlarmScope` | 信頼できない部分の DN（下の「答えないノード」） |

警報の id は `<ノード>.<保守用の API の alarm_id>` にします。ノードは、OAM のインベントリの名前です。小文字、数字、`-` だけでできているので、`.` で区切れます。`PATCH` の鍵から、OAM はどのノードに確認を渡すかを、何も覚えずに決められます。保守用の API の `alarm_id` は 64 ビットの乱数なので、つないだ id も、一覧をまたいでも時がたっても重なりません（TS 28.111 6.4 の勧め）。

### 警報の記録の対応

保守用の API の `AlarmRecord` と、OpenAPI の `AlarmRecord` の対応です。

| 保守用の API | OpenAPI | 違い |
| --- | --- | --- |
| `alarm_id` | 対応表の鍵 | ノードの名前を前に付ける |
| `object_instance` | `objectInstance` | OAM が `SubNetwork={sn},ManagedElement=<インベントリの名前>` に `role` と `peer` を足して作り直す。ノードが自分で書く `ManagedElement=` は、同じ役割の 2 台が設定のファイルを共有すると同じ名前になるためです。そのままでは、1 つの一覧に識別の 4 つの属性が同じ記録が 2 つ並び、TS 28.111 6.5 に反します |
| なし | `notificationId` | 必須の属性（7.3.1.2）。その警報について最後に送った通知の id。ノードが持つように足す |
| `alarm_raised_time`、`alarm_changed_time`、`alarm_cleared_time` | `alarmRaisedTime`、`alarmChangedTime`、`alarmClearedTime` | なし |
| `alarm_type`、`probable_cause`、`specific_problem`、`perceived_severity`、`additional_text` | `alarmType`、`probableCause`、`specificProblem`、`perceivedSeverity`、`additionalText` | 値の綴りはすでに OpenAPI と同じ |
| `ack_state`、`ack_time`、`ack_user_id` | `ackState`、`ackTime`、`ackUserId` | なし |
| なし | `ackSystemId` | 確認を作るなら持つ属性（7.3.1.3）。ノードが持つように足す。OSS の `PATCH` が書いた値か、OAM の画面からの確認なら OAM の名前 |
| `role`、`peer` | なし | `objectInstance` の部分なので出さない |
| `dropped_records` | なし | 一覧の属性に当たるものが無い。捨てたことは通知で伝える（下の「仕様に無い振る舞い」） |

TS 28.111 の残りの任意の属性（`backedUpStatus`、`trendIndication`、`thresholdInfo`、`comments`、相関、ADMC の属性、セキュリティの警報の属性など）は、保守用の API と同じく持ちません。保守用の API の `SIMPLIFIED:` が理由を書いています。そこから外すのは `notificationId` と `ackSystemId` だけです。

### 答えないノード

答えないノードがあっても、`GET` は 200 で、答えたノードの警報の記録を返します。今の `/oam/v1/alarms` と同じです。答えないノードの記録は入りません。OAM は写しを持たないためです（下の「仕様に無い振る舞い」）。信頼できない部分は `unreliableAlarmScope` で示します（TS 28.111 6.11）。

答えないノードとして数えるのは、この `GET` に答えなかったノードと、見張りの状態に「答えない」と書かれたノード（下の「複数のインスタンス」）です。後者は通知の作業で入ります。それまでの `GET` は、その要求で答えなかったノードだけを数えます（下の「実装の分け方」）。

答えないノードの集まりと、属性と通知の対応は次のとおりです。

| 答えないノード | `unreliableAlarmScope` |
| --- | --- |
| なし | 出さない |
| 1 台（A） | A の DN（`SubNetwork={sn},ManagedElement=A`） |
| 2 台以上 | `systemDN`（`SubNetwork={sn}`）。一覧全体を信頼できないと示す |

通知は、いつもノードごとに送ります。ノードが答えなくなったら、そのノードを基点にした `notifyPotentialFaultyAlarmList` を、戻ったら `notifyAlarmListRebuilt` を送ります（下の「通知」）。たとえば A と B が答えないとき、属性は `systemDN` です。A が戻ると、A の `notifyAlarmListRebuilt` を送り、属性は B の DN に変わります。

OpenAPI の `unreliableAlarmScope` は DN 1 つの型（`DnRo`）です。TS 28.111 7.4.1 の多重度は 0 以上なので、仕様書と OpenAPI が食い違っています。型は OpenAPI に従い、複数の部分が信頼できないときは一覧全体として示します。これは 6.11 が許す書き方です。ただし 6.12 は、属性に値が足されたときと外されたときに、その値を基点にした通知を送るとしています。2 台以上のあいだは、属性の値（`systemDN`）と通知の基点（ノードの DN）が一致しません（`SIMPLIFIED:`）。

### 読み出しの問い合わせ

- `filter` は Jex の条件の式（TS 32.161 V19.0.0 7.6、A.4）です。OpenAPI の型は `JexConditions` です。TS 28.111 A.1.4 の例の形、`/AlarmList[id="1"]/attributes/alarmRecords/*[<条件>]` を受け付け、条件に合う警報の記録だけを返します。条件には、比べる式、`and`、`or`、`not`、括弧、`contains` を使えます。
- `dataNodeSelector` も、同じ形の式を同じ意味で受け付けます。TS 32.158 6.7 は、警報の記録のように属性の要素を条件で選ぶのは `dataNodeSelector` で、`filter` はオブジェクトを選ぶものとしています。TS 28.111 A.1.4 は同じことを `filter` で書いています。FM の仕様である A.1.4 に従って `filter` を受け、6.7 に従う consumer のために `dataNodeSelector` も受けます（下の「仕様のあいだの食い違い」）。
- `fields` は、`/attributes/alarmRecords/<id>` の形と、`(<id> | <id>)` で並べた形を受け付けます（A.1.4 の例）。
- `scope` は、OpenAPI のとおり 1 つの問い合わせで、中身は JSON のオブジェクトです。受けるのは `{"scopeType":"BASE_ONLY"}` だけです。`AlarmList` には下のオブジェクトが無いためです。TS 32.158 6.1.2 が書く `scopeType` と `scopeLevel` の 2 つの問い合わせの形は受けず、知らない名前として扱います。
- それ以外の形の `filter` と `dataNodeSelector`、`attributes`、`BASE_ONLY` 以外の `scope` は、500 で `type` を `SERVER_LIMITATION`、`reason` を `QUERY_PARAMS_TOO_COMPLEX` にします。TS 32.158 6.6.5.2 は、この理由を「正しいが、producer の限りを越えて処理できない」問い合わせに当てています。読めない式は 400 で、`reason` を `QUERY_PARAM_VALUES_INVALID` にします。
- 4096 バイトより長い式と、括弧、`not()`、述語を 64 段より深く入れ子にした式も、500 の `QUERY_PARAMS_TOO_COMPLEX` にします。`fields` も 4096 バイトまでです。式を読む処理は入れ子の段ごとに自分を呼ぶので、限りが無いと、深い式 1 つでスタックを使い切り、OAM のプロセスが止まります。購読の `notificationFilter` にも同じ限りを当てます。
- 知らない名前の問い合わせ（`scopeType`、`scopeLevel` を含む）は、400 で `type` を `VALIDATION_ERROR`、`reason` を `QUERY_PARAM_NAMES_INVALID` にし、`badQueryParams` に名前を書きます（6.6.5.2）。今の保守用の API と同じく、黙って全件を返しません。

絞り込みは、すべてのノードの警報の記録を読んだあとに OAM が評価します。読む数は、ノードの数と、ノードごとの一覧の上限（256 件）で決まります。加入者の数には依りません。

### 確認

確認は JSON Merge Patch（RFC 7396、TS 32.158 6.3.2）で送ります。TS 28.111 A.1.4 が挙げる 3 つの形のうちの 1 つです（案。「確認で受ける形」）。

```http
PATCH /3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1 HTTP/1.1
Content-Type: application/merge-patch+json

{
  "id": "1",
  "attributes": {
    "alarmRecords": {
      "scscf.9f3c2a7b1d4e6058": {
        "ackState": "ACKNOWLEDGED",
        "ackUserId": "noc-operator",
        "ackSystemId": "oss-1"
      }
    }
  }
}
```

本文の決まりは次のとおりです。誤りの `type` と `reason` は TS 32.158 6.6.4 と 6.6.5.3.2 の値です。

| 本文 | 答え |
| --- | --- |
| `id` が無い | 400。`VALIDATION_ERROR`。TS 32.158 6.3.2 は `id` を必須にしています |
| `id` が経路の id と違う | 403。`MODIFICATION_NOT_ALLOWED`、`reason` は `ATTRIBUTE_INVARIANT`、`badAttributes` は `#/id`。6.3.2 は同じ値を求めています。違う値は、変えられない id を書き換える要求になります |
| `ackState`、`ackUserId`、`ackSystemId` を書く | 受ける。書き換えられるのはこの 3 つだけです（TS 28.111 7.3.1.2 の `isWritable`） |
| `ackUserId` が無い | 400。`VALIDATION_ERROR`、`reason` は `NEW_ATTRIBUTE_VALUE_INVALID`。`notifyAckStateChanged` で必須の値だからです（8.8.2） |
| `perceivedSeverity` か、ほかの読むだけの属性を書く | 403。`MODIFICATION_NOT_ALLOWED`、`reason` は `ATTRIBUTE_NOT_WRITABLE`、`badAttributes` に場所を書く。ここの警報はどれも、上げた役割が下げる ADAC の警報で、OSS は消せません（6.8） |
| 値が `null`（RFC 7396 の削除） | 403。`ATTRIBUTE_NOT_WRITABLE`。警報の記録を消すのはシステムだけで（6.5）、確認の属性を空にする操作もありません。記録そのもの（`"<id>": null`）も同じです |
| 一覧に無い警報の id を鍵にする | 400。`type` は `IE_NOT_FOUND`、`reason` は `ATTRIBUTE_NOT_FOUND`、`badAttributes` は `#/attributes/alarmRecords/<id>`。6.6.5.3.2 はこの理由を削除に当てていますが、`type` の意味（6.6.4）は変える対象が無いことで、6.6.5.1 は理由の一覧を閉じたものとしていません。確認の前に、その警報がほかの確認で一覧から消えたときも、この答えになります |
| 2 台以上のノードの警報の記録を並べる | 500。`SERVER_LIMITATION`（下の段落） |

1 つの `PATCH` に並べてよいのは、1 台のノードの警報の記録だけです。そのノードは、自分の一覧のロックの中で全部を確かめてから変えます。

TS 32.158 6.3.1 は `PATCH` を不可分とし、一部だけを当てることを禁じています。ノードをまたぐ確認を不可分にするには、ノードのあいだの 2 相の確定が要ります。そこで、2 台以上にまたがる `PATCH` は 500 で断ります（`SIMPLIFIED:`）。OAM は、本文の形と、鍵がどのノードを指すかを、どのノードにも聞く前に確かめます。断るときは、どのノードの一覧も変わっていません。

ほかの Patch の形（`application/json-patch+json` と 3GPP の 2 つの形）は 415 で、`Accept-Patch` に `application/merge-patch+json` を書きます（RFC 5789 2.2、3.1）。3GPP の 2 つの形は、仕様書のあいだで media type の綴りが揃っていません（下の「仕様のあいだの食い違い」）。

OAM は、鍵の前のノードの名前で宛先を決め、そのノードの保守用の API で確認を渡します。保守用の `AckStateRequest` には、`ackSystemId` に当たる欄と、複数の警報の記録をまとめて確かめる形を足します。

### 購読

`NtfSubscriptionControl` の属性は、OpenAPI（`TS28623_SubscriptionControlNrm.yaml` 19.4.0）の 4 つです。

| 属性 | 扱い |
| --- | --- |
| `notificationRecipientAddress` | 必須。`http` か `https` の URL |
| `notificationTypes` | 下の「通知」の表の 6 つのうちのいくつか。無ければ 6 つすべて。ほかの名前は 400 で `NEW_ATTRIBUTE_VALUE_INVALID` |
| `scope` | 無いか、`{"scopeType":"BASE_ALL"}` だけ。基点は購読を含む SubNetwork です（TS 28.622 4.3.22.1）。ノードで分けたいときは `notificationFilter` を使う |
| `notificationFilter` | Jex の条件の式。通知の本文のどの値にも当てられる（4.3.22.1）。例は `contains(href,"ManagedElement=scscf") and perceivedSeverity="CRITICAL"`。読めない式を書いた `PUT` には、400 の `NEW_ATTRIBUTE_VALUE_INVALID` を返す（TS 32.158 6.6.5.3.2） |

TS 28.622 4.3.22.1 では、`notificationTypes` が無い購読は、producer が出せるすべての種類の通知を受けます。この口が出せるのは故障管理の 6 つの通知だけで、TS 28.532 の構成の通知（`notifyMOICreation`、`notifyMOIDeletion`、`notifyMOIAttributeValueChanges`、`notifyMOIChanges`）は出しません（`SIMPLIFIED:`）。そのため、4.3.22.1 の、購読の作成と削除を構成の通知で知らせる決まりも当たりません。

TS 28.622 4.3.22.2 には、ほかに `lastSequenceNo`、`administrativeState`、`operationalState`、`availabilityStatus` があります。この版の OpenAPI にはどれも無いので、出しません。

購読は OAM の共有の保存先に置きます（下の「複数のインスタンス」）。購読の数は OSS の数で決まり、加入者の数には依りません。それでも上限を設定で持ち、超える `PUT` は 500 で `SERVER_LIMITATION` にします（TS 32.158 6.6.4）。

## 通知

| 通知（TS 28.111） | 送るとき | `eventTime` |
| --- | --- | --- |
| `notifyNewAlarm`（8.2） | 一覧に警報の記録が足された | `alarmRaisedTime` |
| `notifyChangedAlarmGeneral`（8.4） | 重さ（`CLEARED` 以外へ）か追加の文が変わった。cleared の警報が同じ識別でまた上がったときもこれ | `alarmChangedTime` |
| `notifyClearedAlarm`（8.3） | 重さが `CLEARED` になった | `alarmClearedTime` |
| `notifyAckStateChanged`（8.8） | 確認の状態が変わった | `ackTime` |
| `notifyPotentialFaultyAlarmList`（8.10） | 見張っているノードが答えなくなった | 答えなくなったと決めた時刻 |
| `notifyAlarmListRebuilt`（8.5） | 答えなくなったノードが戻った。ノードが再起動した。ノードの出来事を通知せずに失った | 戻ったと決めた時刻 |

- 警報の記録が一覧から消えたことは、直接は通知しません。cleared と確認の 2 つの通知から、OSS が分かります（6.12）。
- 本文の名前は OpenAPI の `NotifyNewAlarm` などのとおりです。共通の見出しは `TS28623_ComDefs.yaml` の `NotificationHeader` で、`href`、`notificationId`、`notificationType`、`eventTime`、`systemDN` を必ず入れます（TS 28.532 11.0.2、12.0.2.1）。
- `href` は、objectClass と objectInstance を合わせて URI に写したもの（canonical URI）です（TS 28.111 A.1.2、TS 28.532 12.0.2.1、TS 32.158 4.2.4）。
  - 警報の通知では、警報の対象の DN です。例は `http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org` です。
  - `notifyPotentialFaultyAlarmList` と `notifyAlarmListRebuilt` では、信頼できない部分の基点です（6.12、8.5.2、8.10.2）。1 台のノードならそのノードの DN（`.../SubNetwork=ims/ManagedElement=scscf`）、一覧全体なら `systemDN` です。
  - DN の前置き（`DC=`）を持たないので、TS 32.158 4.2.3 の終わりのとおり、URI の host は producer の自分のもの（OAM の名前）にします（案。「DN の前置き」）。
- `systemDN` は `SubNetwork={sn}` です。MnsAgent の MOI を出さないので、11.0.2 のとおり根の MOI の DN にします。
- `notificationId` は、警報の通知ならノードが決め、警報の記録の `notificationId` にも書きます。一覧の通知（`notifyPotentialFaultyAlarmList` と `notifyAlarmListRebuilt`）なら OAM が決めます。どちらも乱数で、JSON の数として失われない 53 ビットに収めます。11.0.2 は、同じ MOI の通知のあいだで重ならないことを求めています。
- `sequenceNo` と `subscriptionId` は入れません。11.0.2 は、この 2 つを、確実に届ける仕組み（reliable notification transport）を持つ producer に限って持つものとしています。この口はその仕組みを持ちません（下の「届かなかった通知」）。
- `notifyAlarmListRebuilt` の `reason` は、8.5.2 の値から選びます。答えなくなったノードが戻ったときは `System-NE communication error`、ノードが再起動したときは `System restarts`、出来事を失ったときと警報の記録を捨てたときは `indeterminate` です。`alarmListAlignmentRequirement` は、どれも通知を送れなかった警報があるので `ALIGNMENT_REQUIRED` にします。綴りは OpenAPI の列挙のものです（下の「仕様のあいだの食い違い」）。
- `notifyPotentialFaultyAlarmList` の `reason` は、8.10.2 の値の `serviceprovider-NE communication error` です。
- 送らない通知は、`notifyNewSecAlarm` と `notifyChangedSecAlarmGeneral`（セキュリティの警報を持たない）、`notifyComments`（コメントを作らない）、`notifyCorrelatedNotificationChanged`（相関を作らない）、TS 28.111 7.5.2 の構成の通知（`AlarmList` の状態の属性が変わらない）です。

### 届かなかった通知

ある OSS への `POST` が失敗しても（期限切れ、接続できない、2xx 以外）、その出来事は送り終えたものとして扱い、見張りの状態を進めます。1 つの OSS が止まっても、ほかの OSS への通知とほかの出来事を止めないためです。失敗した通知を送り直すことはしません。

代わりに、その購読に「通知を落とした」印を、共有の保存先の購読の項目に付けます。次にその購読へ何かの通知を送るとき、OAM は先に `notifyAlarmListRebuilt` を `systemDN` を基点に `ALIGNMENT_REQUIRED` で送ります。`reason` は `indeterminate` です。この通知が届いたら印を外します。OSS は、それを受けて一覧を読み直します（TS 28.111 5 章の REQ-FM-MC-5 の動機、8.5.2）。この使い方は仕様に無いものです（下の「仕様に無い振る舞い」）。

同じ通知がもう一度届くのは、OAM のインスタンスが入れ替わったときだけです（下の「複数のインスタンス」）。そのときの通知は、前と同じ `notificationId` を持ちます。

## 仕様に無い振る舞い

次のことは、仕様が求めていないか、仕様の外の使い方です。OSS の写しが、通知なしにずれないようにするために足します。

| 振る舞い | 仕様との関係 |
| --- | --- |
| 確認されない cleared の警報が保持の時間を過ぎたら、ノードが自分で確認する。`ackUserId` は保持の時間で消したことを示す固定の名前（`retention`）、`ackSystemId` はノードの名前。`notifyAckStateChanged` が出るので、OSS は cleared と確認の 2 つの通知から、警報の記録が消えたと分かる（案。「保持の時間の消去」） | 今は黙って消している（`internal/maintenance/alarmlist.go` の `expire` の `SIMPLIFIED:`）。6.14 では、確認を持つ producer は、cleared の記録を確認されるまで残す。producer が自分で確認するという動きは仕様にあり、TS 28.111 5 章の REQ-FM-MC-12 は、確認の操作を持たない producer に、警報を自分で確認できることを求めている。確認の操作を持つ producer が保持の時間で自分で確認することは、仕様に書かれていない |
| 一覧がいっぱいで警報の記録を捨てたら、そのノードを基点に `notifyAlarmListRebuilt` を `ALIGNMENT_REQUIRED` で送る | 7.3.2.1 は捨てることを許しているが、通知は求めていない。一覧は作り直していないが、通知を送れなかった警報があるという 8.5.2 の `alarmListAlignmentRequirement` の意味に合わせて使う |
| 届かなかった通知のあと、その購読だけに `notifyAlarmListRebuilt` を送る（上の「届かなかった通知」） | 8.5.1 の、一覧を作り直したときの通知ではない。購読ごとに送るのも、仕様に無い使い方 |
| 答えないノードの警報の記録を `GET` の答えに入れない | 6.11 は、その部分の記録が更新されなくなり信頼できないとしているが、記録を外すかどうかは書いていない。OAM は写しを持たないので、出せる記録が無い |

## 複数のインスタンス

OAM は複数のインスタンスで動けます（`internal/oam/doc.go`）。読み出しと確認は、今の `/oam/v1/alarms` と同じく、要求のたびにノードに聞くので、どのインスタンスでも答えられます。通知には、次のものをインスタンスの外に置きます。

| 置くもの | 中身 | 期限 | 数を決めるもの |
| --- | --- | --- | --- |
| 購読 | `NtfSubscriptionControl` の id と属性、「通知を落とした」印 | なし | OSS の数 |
| 見張りのリース | ノードごとに、見張っているインスタンスと世代 | あり（15 秒） | ノードの数 |
| 見張りの状態 | ノードごとに、世代、ノードのリングバッファの識別、送り終えた最後の出来事の番号、答えないと決めたかどうか | なし | ノードの数 |
| インスタンスの在席 | インスタンスごとに、動いていることを示す項目 | あり（15 秒） | OAM のインスタンスの数 |

### 流れ

1. ノードは、警報の一覧が変わるたびに、出来事（足した、変えた、cleared にした、確認の状態が変わった、捨てた）を、番号を付けてリングバッファに積みます。リングバッファは上限があり、古い出来事から消えます。リングバッファには、プロセスが起動したときに選ぶ乱数の識別を付けます。保守用の API の `instance` はホストや pod の名前で、同じコンテナを起動し直しても変わらないので、再起動の印には使えません。
2. OAM の各インスタンスは、インベントリのノードごとに見張りのリースを取りにいきます（下の「リースの分け方」）。取れたインスタンスは、見張りの状態の世代を 1 つ上げてから、そのノードの保守用の API に、見張りの状態の番号より後の出来事を長く待つ `GET` で聞きます。
3. 出来事が届いたら、そのインスタンスが購読を読み、`notificationTypes` と `notificationFilter` で選んだ宛先に通知を送ります。送り終えたら見張りの状態の番号を進め、次を聞きます。
4. ノードが答えなくなったら、リースを持つインスタンスが `notifyPotentialFaultyAlarmList` を送り、見張りの状態に「答えない」と書きます。戻ったら `notifyAlarmListRebuilt` を送ります。リングバッファの識別が変わっていたら、ノードが再起動したと分かります。見張りの状態の番号がリングバッファからもう消えていたら、出来事を失ったと分かります。どちらも `notifyAlarmListRebuilt` を送ります。

### リースと見張りの状態

期限は、どれも保存先が決めます。リースは期限付きの項目で、期限が来ると保存先が消します。インスタンスの時計は使いません。

- リースを取るのは、項目が無いときだけ書く `CompareAndSwap`（版を空にする）です。延ばすのは、自分が読んだ版での `CompareAndSwap` で、同じ期限を付け直します。
- 見張りの状態は期限の無い項目です。リースを取ったインスタンスは、まず見張りの状態の世代を 1 つ上げて書きます。そのあと番号を進めるたびに、自分が書いた版での `CompareAndSwap` で書きます。
- ほかのインスタンスがリースを取ると、見張りの状態の世代が上がり、版が変わります。古いリースの持ち主が番号を書こうとしても `CompareAndSwap` が失敗するので、番号を戻せません。失敗したら、その持ち主はそのノードの見張りをすぐにやめます。
- 古い持ち主が止まっていたあいだに新しい持ち主が同じ出来事を送ると、同じ通知が 2 回届きます。どちらも同じ `notificationId` を持つので、OSS は重なりを見分けられます。この重なりは許します。出来事は失いません。新しい持ち主は、最後に書かれた番号から続けるためです。

時間の関係は次のとおりです。

| 値 | 長さ | 決まり |
| --- | --- | --- |
| リースの期限 | 15 秒 | 下の 3 つより長い |
| リースを延ばす間隔 | 5 秒 | 期限の 3 分の 1。長く待つ `GET` や通知の送信とは別に延ばすので、それらが長引いても切れない。2 回続けて延ばせなくても期限の中に収まる |
| 長く待つ `GET` の長さ | 5 秒 | リースを延ばす間隔以下。リースを失ったと分かってから、見張りをやめるまでの遅れの上限になる |
| 1 つの通知を送る期限 | 5 秒 | 宛先ごと。1 つの出来事の通知は、購読の数に上限を付けて同時に送る |

### リースの分け方

インスタンスを足したときに、ノードの見張りが分かれるようにします。

- 各インスタンスは、在席の項目を期限付きで書き、リースと同じ間隔で延ばします。在席の項目の名前は、保存先の並べた索引にも入れます。読むときに項目が無い名前は、索引から外します。
- 自分の取り分は「ノードの数 ÷ 在席のインスタンスの数」を切り上げた数です。取り分より多いリースは、出来事を送り終えたところで手放します（`CompareAndDelete`）。取り分に届くまでは、まだ持ち主の無いリースを、毎回並びを変えた順で取りにいきます。
- 1 回の間隔あたりの手間は、すべてのインスタンスを合わせて、インスタンスの数とノードの数の積に比例します。ノードの数で決まり、加入者の数にも購読の数にも依りません。

### この形にした理由

- 1 台のノードの出来事を送る持ち主は、どの時点でも 1 つのインスタンスです。持ち主が替わるときだけ重なりがあり、そのときも番号は戻りません。届く順は、重なりを除けば、ノードで起きた順のままです。
- ノードは OAM の場所を知らなくて済みます。今の保守用の API と同じく、OAM がノードに聞く向きのままです。ノードから OAM に送る形では、同じ設定のファイルを共有する 2 台のノードが、OAM のインベントリでどちらなのかを名乗れません。
- ノードが答えなくなったことに、見張るインスタンスが気付けます。ノードから送る形では、止まったノードは何も送らないので、OAM は気付けません。

保存先は、ほかの OAM の保存先と同じく、インタフェースの後ろに置きます。1 つのインスタンスなら `data_dir` のファイルに置き、リースはいつもそのインスタンスのものです。複数のインスタンスなら `internal/kvstore` に置きます。`internal/kvstore/kvlock` は延ばせないロックなので、見張りのリースには使えません。

届かなかった通知はログに書き、指標で数えます。遅い OSS は、そのノードの出来事の流れを送る期限の分だけ遅らせますが、ほかのノードの流れは止めません。

## 誤りの形

誤りの本文は TS 32.158 6.6.3 の形にし、型は OpenAPI の `ErrorResponseGet`（`GET`）、`ErrorResponseDefault`（`PUT`、`DELETE`、JSON Merge Patch）を使います。`type` と `reason` の値は 6.6.4 と 6.6.5 から選びます。上の各節に書いたもののほか、ノードに届かなかった確認は 502 にします。TS 32.158 6.6.2 の 502 は、ゲートウェイとして働く producer が、中継した先から正しい答えを得られなかったことを表します。OAM がノードに確認を渡す形に合い、今の `/oam/v1/` の API とも同じです。

本文の `Content-Type` は `application/json` にします。TS 32.158 6.6.3.1 は誤りの本文の media type を `application/vnd.3gpp.error+json` としていますが、OpenAPI はどの誤りの答えも `application/json` で定義しています。OpenAPI から作った consumer が読めるように、OpenAPI に合わせます。

## 仕様のあいだの食い違い

読んだ版のあいだで、次のことが揃っていません。どれも、この記録の選び方に関わります。

| 食い違い | 選んだこと |
| --- | --- |
| `TS28111_FaultNrm.yaml` の `servers` は `{MnSRoot}/FaultSupervisionMnS/{MnSversion}` だが、`paths` は空。TS 28.111 6.6 は、読み出しに TS 28.532 の getMOIAttributes を使うとしている | 経路は `TS28532_ProvMnS.yaml` の `{MnSRoot}/ProvMnS/{MnSVersion}/{URI-LDN-first-part}/{className}={id}` にする |
| 読み出しの範囲の問い合わせが、TS 32.158 6.1.2 と 6.6.5.2 では `scopeType` と `scopeLevel` の 2 つ、`TS28532_ProvMnS.yaml` では中身が JSON のオブジェクトの `scope` 1 つ | OpenAPI の `scope` だけを受ける。`scopeType` と `scopeLevel` は知らない名前として 400 |
| 警報の記録を条件で選ぶ問い合わせが、TS 32.158 6.7 では `dataNodeSelector`（`filter` はオブジェクトを選ぶもの）、TS 28.111 A.1.4 では `filter` | A.1.4 に従って `filter` で記録を選ぶ。同じ形の `dataNodeSelector` も受ける |
| TS 28.532 12.1.1.1.3 は `dataNodeSelector` の詳しい説明を TS 32.158 の 6.2a に送っているが、TS 32.158 V20.1.0 に 6.2a は無く、説明は 6.7 にある | 6.7 を読む |
| 3GPP の Merge Patch の media type が、TS 28.532 12.1.1.1.4.2 と TS 32.158 4.3.1 では `application/vnd.3gpp.merge-patch+json`、TS 28.532 12.1.1.3.2.1.3.3 と `TS28532_ProvMnS.yaml` では `application/3gpp-merge-patch+json` | 3GPP の形は受けず、RFC 7396 の形だけを受ける |
| 誤りの本文の media type（上の「誤りの形」） | OpenAPI の `application/json` |
| `unreliableAlarmScope` の多重度（上の「答えないノード」） | OpenAPI の 1 つの DN |
| `alarmListAlignmentRequirement` の値が、TS 28.111 8.5.2 では `alignmentRequired` と `alignmentNotRequired`、`TS28111_FaultNrm.yaml` では `ALIGNMENT_REQUIRED` と `ALIGNMENT_NOT_REQUIRED` | OpenAPI の綴り |
| `TS28623_SubscriptionControlNrm.yaml` 19.4.0 に、TS 28.622 V20.4.0 4.3.22.2 の 4 つの属性が無い | OpenAPI の 4 つの属性だけ |
| OpenAPI の `AlarmRecord` の `thresholdinfo` と、通知の `thresholdInfo` で綴りが違う | どちらも使わない |
| TS 28.111 8.4.2 は notifyChangedAlarmGeneral の `perceivedSeverity` を必須とするが、`TS28111_FaultNrm.yaml` の `NotifyChangedAlarmGeneral` は必須としていない | いつも入れる |
| TS 28.111 7.3.1.2 と 7.3.2.2 が必須とする `AlarmRecord` と `AlarmList` の属性を、OpenAPI はどれも `required` にしていない | 必須の属性はいつも入れる。ただし `alarmClearedTime` は、警報が消えるまで値が無いので入れない |
| TS 28.111 7.3.1.2 が任意の属性として挙げる `comments` が、OpenAPI の `AlarmRecord` に無い | `comments` は作らない |
| 読み出しの `fields` が、TS 32.158 6.2.2 ではコンマで区切った JSON Pointer、TS 28.111 A.1.4 では `(a \| b)` の形。後者の空白は TS 32.161 A.3 の文法に無い | どちらの形も受ける |
| 誤りの本文の `status` が、OpenAPI では文字列、TS 32.158 の例では数 | `status` と `badObjects` は入れない。`status` は複数の結果をまとめる答えに、`badObjects` は 3GPP の Merge Patch（6.6.3.3）に限るため |
| TS 28.111 V20.0.0 は警報の履歴（6.15、6.16、7.3.8 の `AlarmHistory`）を足したが、同じ書庫の `TS28111_FaultNrm.yaml` は 19.3.0 のままで、`AlarmHistory` が無い。3GPP Forge の `Tag_Rel20_SA113` の同じファイルも 19.3.0 | 履歴は作らない |

## 名前を OpenAPI で確かめる試験

OpenAPI のファイルは 3GPP の著作物なので、リポジトリに入れません。試験は、ファイルの名前と版を書いた手書きの一覧で確かめます。`internal/n5/types_test.go` と同じ考え方です。

- スキーマごとの属性の名前の一覧を、試験の中に持ちます。たとえば `AlarmRecord` の 30 個、`NotifyNewAlarm` の見出しと本体の属性、`NtfSubscriptionControl-Single` の 4 つです。各一覧に、写したファイルと `info.version`（`TS28111_FaultNrm.yaml` 19.3.0 など）を書きます。
- Go の型の `json` の名前を reflect で読み、2 つのことを確かめます。Go の型にある名前は、どれも一覧にあること（綴りの誤りや作った名前が無いこと）。一覧の名前は、Go の型にあるか、持たない理由を書いた省略の一覧にあること（黙って抜けていないこと）。省略の一覧の名前は、コードの `SIMPLIFIED:` と対応させます。
- OpenAPI の `required` の名前は、Go の型で `omitempty` を付けていないことを確かめます。
- 列挙（`AlarmType`、`PerceivedSeverity`、`AckState`、`AlarmNotificationTypes`、`AlarmListAlignmentRequirement`、`ScopeType`）の値を、一覧と突き合わせます。
- 経路と問い合わせの名前（`ProvMnS`、`v19`、`AlarmList=1`、`NtfSubscriptionControl=`、`scope`、`filter`、`dataNodeSelector`、`fields`）を、ルーティングの試験で確かめます。
- 例の本文を OpenAPI のスキーマから書き、往復で中身が変わらないことを確かめます（`internal/n5` の `roundTrip`）。
- 一覧を作り直すときのために、ダウンロードした OpenAPI のファイルと一覧を比べる試験 `TestOpenAPIFiles`（`internal/mns/openapi_test.go`）を置きます。環境変数 `MNS_OPENAPI_DIR` にファイルを置いたディレクトリを指したときだけ動き、CI では流しません。ファイルを取ってくる処理がネットワークに頼るためです。

## 作らないもの

| 作らないもの | 理由 |
| --- | --- |
| Release 17 の `TS28532_FaultMnS.yaml` の経路（`/alarms`、`/alarms/alarmCount`、`/subscriptions`） | 今の版の仕様から消えている（上の「選んだこと」） |
| 警報の消去（clear）の操作 | ここの警報はどれも ADAC で、OSS が消す警報（ADMC）が無い。消去は ADMC の警報があるときだけ必須（TS 28.111 6.8） |
| コメント、相関、しきい値の警報、セキュリティの警報 | 保守用の API が持たない。TS 28.111 6.1 の任意の部品 |
| 警報の履歴（`AlarmHistory`、`exportAlarmHistory`） | 同じ版の OpenAPI に無い。履歴は 6.5 のとおり一覧に置かず、別の置き場所とファイルの書き出しが要る |
| `AlarmList` の lock（`administrativeState`） | 任意の属性。lock のあいだの決まり（6.13）を作るほどの使い道が無い |
| SubNetwork、ManagedElement、ManagedFunction の MOI と、`scope` で木をたどる読み出し | ノードは NRM の管理対象ではない（保守用の API の `ObjectInstance` の `SIMPLIFIED:`）。故障管理に要るのは `AlarmList` と購読だけ |
| 構成の通知（`notifyMOICreation` など） | 故障管理の要件に入っていない。購読の作成と削除は `PUT` と `DELETE` の答えで分かる（上の「購読」） |
| 購読の一覧の読み出し | ProvMnS では SubNetwork を `scope` で読む形になり、SubNetwork の MOI が要る。OSS は自分で作った購読の id を知っている |
| `POST` による購読の作成 | TS 28.532 12.1.1.1.2.1 のとおり、作るのは `PUT` が既定 |
| JSON Patch と 3GPP の 2 つの Patch の形 | 確認には Merge Patch で足りる。3GPP の形は media type の綴りが揃っていない（案。「確認で受ける形」） |
| 確実に届ける仕組み（`sequenceNo`、`subscriptionId`、送り直し） | TS 28.532 11.0.2 の条件付きの部品。OSS は一覧の読み直しで揃えられる |
| heartbeat の通知（`HeartbeatControl`） | 購読の下の `HeartbeatControl` が別に制御するもので（TS 28.622 4.3.21）、TS 28.111 5 章の要件に入っていない |
| `SupportedNotifications` の MOI | 送る通知の種類は、この記録と手順書に書く |
| ONAP VES の形（TS 28.111 A.2）と YANG と NETCONF の形（A.3） | REST の形が 1 つあれば足りる。この実装のほかの口と同じく HTTP と JSON にそろえる |
| 認証と認可、TLS | OAM の API の `SIMPLIFIED:`（TS 32.600 4.2.1.1）と同じ。キャプチャで中身を読めるように平文にし、OAM の待ち受けはホストに出さない |

## 採らなかった案

| 案 | 採らなかった理由 |
| --- | --- |
| Release 17 の Fault Supervision MnS（`TS28532_FaultMnS.yaml` 17.3.0。読んだのは TS 28.532 V17.12.0 の書庫） | 経路が分かりやすいが、Release 18 で消えた形。学習者が今の仕様と照らし合わせられない |
| TM Forum の TMF642（Alarm Management API。TM Forum が Apache-2.0 で公開しているリポジトリのタグ `v5.0.0` の `TMF642_Alarm_v5.0.1.oas.yaml` を読んだ） | 業務システムのあいだで警報を扱う API で、3GPP の Itf-N ではない。原因と種類の値は X.733 と古い TS 32.111-2 を指していて、属性の名前（`alarmedObject` など）も TS 28.111 と違う。確認や消去は、`/ackAlarm`、`/clearAlarm` のような別の資源に作業を作る形になる |
| ノードごとに `AlarmList` を置く（`SubNetwork={sn}/ManagedElement=<ノード>/AlarmList=1`） | ノードの数だけ一覧を読むことになり、ManagedElement の MOI も要る。答えないノードを `unreliableAlarmScope` で示す 6.11 の形が使えない |
| 警報の id を保守用の API の `alarm_id` のままにする | 確認を渡すノードを決めるために、すべてのノードに聞くか、id とノードの対応を覚えておく必要がある |
| ノードが OAM に出来事を送る | ノードが OAM の場所を知る必要がある。同じ設定のファイルを共有する 2 台が名乗り分けられない。止まったノードに気付けない（上の「複数のインスタンス」） |
| OAM が一定の間隔でノードの一覧を読み、前回との差を通知にする | 間隔のあいだに上がって下がった警報を通知できない。前回の一覧をインスタンスの外に持つ必要があり、複数のインスタンスが同じ差を送らないためのリースも結局要る |
| 購読をプロセスの中に持つ | 購読を受けたインスタンスと通知を送るインスタンスが違うと届かない。インスタンスを作り直すと購読が消える |
| 届かなかった通知を、届くまで送り直す | 送り直しの順と期限を購読ごとに持つことになり、確実に届ける仕組みの半分を作ることになる。OSS は一覧の読み直しで揃えられる |

## 実装の分け方

PR の単位で分けます。大きさは、試験を含めた行数の目安です。

### 役割の警報

e2e で通知を受け取るには、上がる警報が要ります。今は、どの役割も警報を上げていません。[oam.md](../background/oam.md) の 6 章の「役割ごとの警報の案」のうち、Diameter の相手との接続が切れたときの警報を、I-CSCF と S-CSCF から上げます。HSS を止めると上がり、戻すと下がります。この記録の範囲の外の作業として、先に入れます（案。「役割の警報を別の作業にするか」）。約 400 行。

### 保守用の API の出来事

警報の記録に `notification_id` と `ack_system_id` を足します。確認を複数の警報の記録でまとめて受け、1 つのロックの中で確かめてから変えます。保持の時間の消去を、ノードの確認に変えます（案。「保持の時間の消去」）。出来事のリングバッファと、長く待つ `GET /maintenance/v1/alarm-events` を足します。`SIMPLIFIED:` を直し、[oam.md](../background/oam.md) の 6 章の表と手順書の「簡略化したこと」を合わせます。確かめ方は、出来事の番号と順、リングバッファの上限と失った印、リングバッファの識別、長く待つ答えの期限、`-race` の試験です。約 700 行。

### 3GPP の型

新しいパッケージ `internal/mns` に、`AlarmRecord`、`AlarmList-Single` の `Resource`、6 つの通知と `NotificationHeader`、`NtfSubscriptionControl-Single`、`Scope`、`ErrorResponseGet` と `ErrorResponseDefault`、DN と canonical URI の組み立てを置きます。上の「名前を OpenAPI で確かめる試験」と、一覧を比べるスクリプトを入れます。OAM と、e2e の OSS の代わりの両方が使うので、`internal/oam` の外に置きます。約 600 行。

### Jex の条件の式

新しいパッケージ `internal/jex` に、TS 32.161 A.4 の文法の字句解析、構文解析、JSON の値への評価を置きます。読み出しの `filter` と `dataNodeSelector`、購読の `notificationFilter` で使います。確かめ方は、文法の各規則、比べる式、`contains`、読めない式の誤りの試験です。試験の式は、TS 32.161 Annex B.3 の例を写さず、警報の属性で書き起こします。約 600 行。

### 警報の一覧の読み出しと確認

`{MnSRoot}/ProvMnS/v19/SubNetwork={sn}/AlarmList=1` の `GET` と `PATCH`、問い合わせ、`unreliableAlarmScope`、誤りの本文を作ります。OAM の設定に、SubNetwork の名前と `{MnSRoot}` を足します。この作業の `unreliableAlarmScope` は、その要求で答えなかったノードだけを数えます。見張りの状態を足すのは、通知（1 つのインスタンス）の作業です。確かめ方は、本物の `internal/maintenance` のノードと答えないノードを使った `internal/oam` の試験と、確認の本文の各行の誤りの試験です。約 800 行。

### 購読

`NtfSubscriptionControl` の `PUT`、`GET`、`DELETE`、検査、上限、保存先のインタフェースと `data_dir` のファイルの実装を作ります。約 500 行。

### 通知（1 つのインスタンス）

ノードごとの見張り、出来事から通知への写し、購読ごとの選び分け、送る期限と同時の数の上限、`notifyPotentialFaultyAlarmList` と `notifyAlarmListRebuilt`、「通知を落とした」印、送った数と失敗の指標を作ります。見張りの状態は `data_dir` に置き、`GET` の `unreliableAlarmScope` にも使います。確かめ方は、`httptest` の OSS の代わりで、通知の種類、順、本文の名前、ノードの再起動、リングバッファからの欠落、答えないノード、落とした通知のあとの `notifyAlarmListRebuilt` を確かめる試験です。約 900 行。

### 通知（複数のインスタンス）

リース、見張りの状態、在席の `internal/kvstore` の実装を作ります。確かめ方は、Valkey を使う CI の適合試験と、次のことの試験です。2 つのインスタンスのどちらかを止めても出来事を失わないこと。持ち主が替わったときに重なって届く通知は、同じ `notificationId` を持つこと。古いリースの持ち主は、見張りの状態の番号を戻せないこと。インスタンスを足すと、リースが取り分に分かれること。約 500 行。

### e2e と手順書

Compose に OSS の代わり（通知を受けて残す小さなプログラム。案。「OSS の代わり」）を足し、HSS を止めて `notifyNewAlarm`、確認して `notifyAckStateChanged`、戻して `notifyClearedAlarm` が届くこと、一覧の `GET` の形を確かめます。pcap で、OAM と OSS のあいだの `GET`、`PATCH`、`POST` を確かめます。手順書（英語と日本語）に、OSS の代わりで警報を受け取る例、ラダー図、「簡略化したこと」を書き、用語集に MnS、NRM、MOI、DN、Itf-N、OSS、EM、NM、Jex などを足します。`internal/oam/doc.go` に経路を足します。約 600 行と文書。

### 作業のあいだの順

| 作業 | 先に要る作業 |
| --- | --- |
| 保守用の API の出来事、3GPP の型、Jex の条件の式 | なし。互いに依らない |
| 警報の一覧の読み出しと確認 | 保守用の API の出来事（`ack_system_id` とまとめた確認）、3GPP の型、Jex の条件の式 |
| 購読 | 3GPP の型、Jex の条件の式 |
| 通知（1 つのインスタンス） | 保守用の API の出来事、3GPP の型、Jex の条件の式、警報の一覧の読み出しと確認（`unreliableAlarmScope` に見張りの状態を足すため）、購読 |
| 通知（複数のインスタンス） | 通知（1 つのインスタンス） |
| e2e と手順書 | 役割の警報、警報の一覧の読み出しと確認、購読、通知（1 つのインスタンス） |

## 決めていないこと

製品の向きに関わるので、ここでは決めずに、勧める案を書きます。本文で「（案）」を付けたところは、この表の論点の名前で指しています。

| 論点 | 勧める案 | 理由 |
| --- | --- | --- |
| 役割の警報を別の作業にするか | 別の作業として先に入れる | 警報を上げる役割の作りは、northbound とは別の主題。e2e で通知を受け取るのに要る |
| DN の前置き | 持たない。DN は `SubNetwork={sn}` から始め、`href` の host は OAM の名前 | `DC=ims.mnc001.mcc001.3gppnetwork.org` を前置きにすると、DN が世界で一意になる。一方で `href` の host が、名前を引けない名前になる（TS 32.158 4.2.3） |
| SubNetwork の名前 | 設定の項目にし、Compose では `ims` | 1 つの網を 1 つの SubNetwork として見せる。網間接続の構成では網ごとに別の名前にする |
| 保持の時間の消去 | ノードの確認に変える | 通知を受ける OSS の写しが、黙って消えた警報の記録でずれないため。OAM の画面では、消える前に確認済みに見えるようになる |
| OSS の代わり | `cmd/` に小さな Go のプログラムを足し、`internal/mns` の型で本文を確かめて残す | 汎用の HTTP サーバーでは、受けた本文の名前が OpenAPI のとおりかを確かめられない。OSS は IMS の役割ではないので、`ims` の役割にはしない |
| 確認で受ける形 | RFC 7396 の Merge Patch だけ | 3GPP の形は media type の綴りが仕様書のあいだで揃っていない。JSON Patch は、確認には要らない |

## 読んだ仕様

この記録の条項は、次の版の本文で確かめました。TS 28.111 V20.0.0（4 章、5 章の表、6.1 から 6.16、7.1、7.3.1.1 から 7.3.1.3、7.3.2.1 から 7.3.2.4、7.3.8.1、7.3.8.2、7.3.8.5.1、7.4.1 のうち一覧と確認の属性、7.5.1、7.5.2、8.1 から 8.8、8.10、A.1.1 から A.1.4、Annex D の変更の履歴）、TS 28.532 V20.2.0（11.0.2、11.1.0、11.1.1.1、11.1.1.2、11.1.1.3 の先頭、11.1.1.4 の見出し、11.2a、11.6.1.3、11.6.1.4、12.0.2.1、12.1.1.1.1、12.1.1.1.2、12.1.1.1.3、12.1.1.1.4.2、12.1.1.1.5、12.1.1.2.1、12.1.1.3.1、12.1.1.3.2.1、12.1.1.3.2.4、12.1.1.3.2.5、変更の履歴）、TS 28.532 V17.12.0（書庫の `TS28532_FaultMnS.yaml` の経路だけ）、TS 28.622 V20.4.0（4.3.21、4.3.22、4.3.23、4.4.1 の購読の属性）、TS 28.623 V20.3.0（4.3）、TS 32.158 V20.1.0（4.2.1、4.2.3、4.2.4、4.3.1、4.4.2 から 4.4.4、4.6、5.5、6.1.1 から 6.1.4、6.3.1、6.3.2、6.6.1 から 6.6.4、6.6.5.1、6.6.5.2、6.6.5.3.2、6.6.5.4、6.6.7、6.7）、TS 32.161 V19.0.0（7.6、A.4、B.3）、TS 32.101 V19.0.0（5.1.1、5.1.2.2）、TS 32.102 V19.0.0（7.3.2）、RFC 5789（2.2、3.1）です。TS 32.600 4.2.1.3 と TS 28.533 4.1 は [oam.md](../background/oam.md) の「読んだ資料」の版で確かめています。

OpenAPI は、各仕様書の書庫に入っているものを読みました。TS 28.111 V20.0.0 の書庫の `TS28111_FaultNrm.yaml` と `TS28111_FaultNotifications.yaml`（どちらも 19.3.0）、TS 28.532 V20.2.0 の書庫の `TS28532_ProvMnS.yaml`（19.2.0）、TS 28.623 V20.3.0 の書庫の `TS28623_SubscriptionControlNrm.yaml`（19.4.0）、`TS28623_ComDefs.yaml`（19.6.0）、`TS28623_GenericNrm.yaml`（20.1.0）です。3GPP Forge（`forge.3gpp.org/rep/sa5/MnS`）のタグ `Tag_Rel19_SA113` の同じ名前のファイルと比べ、`TS28623_GenericNrm.yaml` のほかは中身が同じことを確かめました。`TS28623_GenericNrm.yaml` はタグ `Tag_Rel20_SA113` のものと同じです。TS 28.111 V19.5.0 の書庫の 2 つのファイルも、V20.0.0 のものと同じです。

## 残っている課題

- 2 台以上が答えないときは、一覧全体を信頼できないと示します。OpenAPI の型が DN 1 つのためで、どのノードかは `notifyPotentialFaultyAlarmList` でしか分かりません（上の「答えないノード」）。
- 通知を OSS に送り直しません。OSS が止まっていたあいだの通知は失われます。OSS は、次に届く `notifyAlarmListRebuilt` を受けて、一覧を読み直す必要があります。
- OSS が自分の購読を忘れると、その id を知る方法がありません。購読の一覧の読み出しを作らないためです。
