# OSS に警報を渡す口（故障管理の northbound）

この記録は、OAM がノードから集めている警報を、網の管理の仕組み（OSS、TS 32.101 の Network Manager）に標準の形で渡す口を決めます。対象は、警報の一覧の読み出し、確認（acknowledge）、警報の通知の 3 つです。OAM とノードのあいだ（southbound）は、今の独自の保守用の API のままです。調べた結果は [oam.md](../background/oam.md) の 15 章にあります。

製品の向きに関わる論点は、末尾の「製品の向きに関わる決定」の表に、決めたことと理由をまとめています。本文で「（「SubNetwork の名前」）」のように論点の名前を括弧に入れたところは、その表の行を指します。

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

この口は、OAM の独自の API とは別のハンドラー（`internal/oam` の `MnSHandler`）が受けます。`oam` の役割は、`/oam/v1/` に独自の API を、`{root}/ProvMnS/v19/` にこのハンドラーを、同じ待ち受けに並べて置きます。独自の API の試験は、自分の `openapi.yaml` と突き合わせます（[openapi.md](openapi.md)）。このハンドラーを分けたので、そこに 3GPP の経路が混ざりません。3GPP の経路と問い合わせの名前は、OpenAPI のファイルの綴りを書き写した試験で確かめます（下の「名前を OpenAPI で確かめる試験」）。

`{MnSVersion}` は `v19` です。TS 32.158 4.6.2 は、component A の定義のファイルの版の最初の数に `v` を付けたものとしています。FM の component A は ProvMnS で、`TS28532_ProvMnS.yaml` の版は 19.2.0 です。

`{sn}` は設定で与える SubNetwork の名前です。例では `ims` と書きます（「SubNetwork の名前」）。設定の項目は、`oam` の節の `subnetwork` と `mns_root` です。`mns_root` には、OSS から見た OAM の待ち受けの URL に `{root}` を付けたもの、Compose なら `http://oam:8080/3GPPManagement` を書きます。2 つは両方を書くか、どちらも書かないかのどちらかです。どちらも無ければ、この口は出しません。どちらの名前も、役割が起動するときに 1 回だけ確かめます。`subnetwork` は DN の id に使えない文字（`,`、`=`、`/`）を持てず、`mns_root` は http の URL に限ります。要求のたびには確かめません。

| 操作（TS 28.532） | 方法 | 経路 | 要求 | 応答 |
| --- | --- | --- | --- | --- |
| getMOIAttributes（11.1.1.2、12.1.1.1.3） | `GET` | `{MnSRoot}/ProvMnS/v19/SubNetwork={sn}/AlarmList=1` | 問い合わせ（下の「読み出しの問い合わせ」） | 200。`Resource`（中身は `AlarmList-Single`） |
| modifyMOIAttributes（11.1.1.3、12.1.1.1.4.2） | `PATCH` | 同じ | `Content-Type: application/merge-patch+json` の `Resource`（「確認で受ける形」） | 204 |
| createMOI（11.1.1.1、12.1.1.1.2.1） | `PUT` | `{MnSRoot}/ProvMnS/v19/SubNetwork={sn}/NtfSubscriptionControl={id}` | `Resource`（中身は `NtfSubscriptionControl-Single`） | 201（作った）か 200（置き換えた）。作ったものの `Resource` |
| getMOIAttributes | `GET` | 同じ | なし | 200。`Resource` |
| deleteMOI（11.1.1.4、12.1.1.1.5） | `DELETE` | 同じ | なし | 204 |
| 通知 | `POST` | 購読の `notificationRecipientAddress` | `TS28111_FaultNotifications.yaml` の `oneOf` の 1 つ | OSS が 204 を返す |

通知の行は、本文の型を `TS28111_FaultNotifications.yaml` に、宛先への `POST` と 204 の答えを TS 28.532 12.1.1.3.2.4 と TS 32.158 5.5.4 によっています。

- `AlarmList` の id は `1` に固定します。OSS は作れず、消せません（TS 28.111 7.3.2.1）。`PUT` と `DELETE` は 403 で、`type` は `MODIFICATION_NOT_ALLOWED` です。`AlarmList=1` はいつもあるので、`PUT` は作成ではなく置き換えになり、読むだけの属性を書き換えようとすることになります。`reason` は `ATTRIBUTE_NOT_WRITABLE` です（TS 32.158 6.6.5.3.2 は PUT にもこの理由を当てています）。`DELETE` の `reason` は `OBJECT_DELETION_NOT_ALLOWED` です（6.6.5.4）。
- 購読を作るのは `PUT` だけにします。TS 28.532 12.1.1.1.2.1 は、特に断りが無い限り、作るときは `PUT` を使うとしています。購読をやめるときは、その `NtfSubscriptionControl` を消します（TS 28.622 V20.4.0 4.3.22.1）。TS 28.532 のファイルの報告のサービスも、購読を同じ形の createMOI と deleteMOI で行うとしています（11.6.1.3、11.6.1.4）。
- これ以外の経路と方法は 404 か 405 です。SubNetwork そのもの、ManagedElement、`scope` で木をたどる読み出しは出しません（「作らないもの」）。`AlarmList=1` の 405 の `Allow` には、受ける方法の `GET`、`HEAD`、`PATCH` だけを書きます。`Allow` は資源が受ける方法を並べるもので（RFC 9110 10.2.1）、いつも 403 で断る `PUT` と `DELETE` は入りません。

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
| なし | `ackSystemId` | 確認を作るなら持つ属性（7.3.1.3）。ノードが持つように足す。OSS の `PATCH` が書いた値か、OAM の画面と `/oam/v1/` からの確認なら OAM の名前（下の「OAM の画面からの確認」） |
| `role`、`peer` | なし | `objectInstance` の部分なので出さない |
| `dropped_records` | なし | 一覧の属性に当たるものが無い。捨てたことは通知で伝える（下の「仕様に無い振る舞い」） |

TS 28.111 の残りの任意の属性（`backedUpStatus`、`trendIndication`、`thresholdInfo`、`comments`、相関、ADMC の属性、セキュリティの警報の属性など）は、保守用の API と同じく持ちません。保守用の API の `SIMPLIFIED:` が理由を書いています。そこから外すのは `notificationId` と `ackSystemId` だけです。

### 答えないノード

答えないノードがあっても、`GET` は 200 で、答えたノードの警報の記録を返します。今の `/oam/v1/alarms` と同じです。答えないノードの記録は入りません。OAM は写しを持たないためです（下の「仕様に無い振る舞い」）。信頼できない部分は `unreliableAlarmScope` で示します（TS 28.111 6.11）。

答えないノードとして数えるのは、この `GET` に答えなかったノードと、見張りの状態に「答えない」と書かれたノード（下の「複数のインスタンス」）です。後者は、この `GET` に答えたノードでも数えます。見張るインスタンスが「戻った」という `notifyAlarmListRebuilt` を送り、見張りの状態を書き直すまで、その部分は信頼できないままです。その間も、答えたノードの警報の記録は答えに入れます。見張りの状態を読めなかったノードは、信頼できないとは数えません。

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
- `fields` は、`/attributes/alarmRecords/<id>` の形と、`(<id> | <id>)` で並べた形を受け付けます（A.1.4 の例）。`fields` があると、答えは一覧の識別（`id`、`objectClass`、`objectInstance`）と、名指しされた警報の記録だけになります。TS 32.158 6.2.3 のとおり、名指しされないほかの属性（`numOfAlarmRecords` や `unreliableAlarmScope` など）は外します。`filter` も一緒にあれば、両方が選んだ記録だけを返します。
- `fields` が一覧に無い記録だけを名指ししたときも、200 で、記録の無い一覧を返します。6.2.3 は、名指しした属性を持たない資源を外し、何も残らなければ 404 を返すとしています。一方 6.6.5.2 は、問い合わせが何も選ばないことは誤りではないとしています。一覧そのものはいつもあり、記録が消えたことも OSS が確かめたい答えなので、6.6.5.2 に従います（下の「仕様のあいだの食い違い」）。`filter` と `dataNodeSelector` が何も選ばないときも同じです。
- `scope` は、OpenAPI のとおり 1 つの問い合わせで、中身は JSON のオブジェクトです。受けるのは `{"scopeType":"BASE_ONLY"}` と、`scopeType` の無い `{}` だけです。`AlarmList` には下のオブジェクトが無いためです。`{}` を受けるのは、6.1.2 が、`scopeType` が無ければ `BASE_ONLY` とするからです。TS 32.158 6.1.2 が書く `scopeType` と `scopeLevel` の 2 つの問い合わせの形は受けず、知らない名前として扱います。
- それ以外の形の `filter` と `dataNodeSelector`、`attributes`、`BASE_ONLY` 以外の `scope` は、500 で `type` を `SERVER_LIMITATION`、`reason` を `QUERY_PARAMS_TOO_COMPLEX` にします。TS 32.158 6.6.5.2 は、この理由を「正しいが、producer の限りを越えて処理できない」問い合わせに当てています。読めない式、JSON として読めない `scope`、OpenAPI の列挙に無い `scopeType`、2 回書かれた問い合わせは 400 で、`reason` を `QUERY_PARAM_VALUES_INVALID` にします。問い合わせそのものを読めない（`%` の後ろが 16 進でないなど）ときは 400 で、`reason` は `QUERY_MALFORMED` です。400 に当たる問題と 500 に当たる問題が 1 つの要求にあれば、400 を答えます。6.6.5.1 は、いちばん根本の理由を答えるとしています。OSS が直せる誤りのほうが先です。
- 4096 バイトより長い式と、括弧、`not()`、述語を 64 段より深く入れ子にした式も、500 の `QUERY_PARAMS_TOO_COMPLEX` にします。`fields` も 4096 バイトまでです。式を読む処理は入れ子の段ごとに自分を呼ぶので、限りが無いと、深い式 1 つでスタックを使い切り、OAM のプロセスが止まります。購読の `notificationFilter` にも同じ限りを当てます。
- 知らない名前の問い合わせ（`scopeType`、`scopeLevel` を含む）は、400 で `type` を `VALIDATION_ERROR`、`reason` を `QUERY_PARAM_NAMES_INVALID` にし、`badQueryParams` に名前を書きます（6.6.5.2）。今の保守用の API と同じく、黙って全件を返しません。

絞り込みは、すべてのノードの警報の記録を読んだあとに OAM が評価します。読む数は、ノードの数と、ノードごとの一覧の上限（256 件）で決まります。加入者の数には依りません。

### 確認

確認は JSON Merge Patch（RFC 7396、TS 32.158 6.3.2）で送ります。TS 28.111 A.1.4 が挙げる 3 つの形のうちの 1 つです（「確認で受ける形」）。

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
| `id` が経路の id と違う（文字列でない、`null` を含む） | 403。`MODIFICATION_NOT_ALLOWED`、`reason` は `ATTRIBUTE_INVARIANT`、`badAttributes` は `#/id`。6.3.2 は同じ値を求めています。違う値は、変えられない id を書き換える要求になります |
| `objectClass` か `objectInstance` を書く | 今の値と同じなら受け、何も変えない。違えば 403。`ATTRIBUTE_INVARIANT` |
| `ackState`、`ackUserId`、`ackSystemId` を書く | 受ける。書き換えられるのはこの 3 つだけです（TS 28.111 7.3.1.2 の `isWritable`） |
| `ackUserId` が無いか空 | 400。`VALIDATION_ERROR`、`reason` は `NEW_ATTRIBUTE_VALUE_INVALID`。`notifyAckStateChanged` で必須の値だからです（8.8.2） |
| `ackState` が無いか、`ACKNOWLEDGED` と `UNACKNOWLEDGED` のどちらでもない | 400。`NEW_ATTRIBUTE_VALUE_INVALID`。確認の状態はノードが変え、ほかの 2 つはそれと一緒に変わるので、状態の無い確認は当てられません |
| 3 つの属性の値が文字列でない。警報の記録がオブジェクトでない | 400。`NEW_ATTRIBUTE_VALUE_INVALID` |
| `perceivedSeverity` か、ほかの読むだけの属性を書く | 403。`MODIFICATION_NOT_ALLOWED`、`reason` は `ATTRIBUTE_NOT_WRITABLE`、`badAttributes` に場所を書く。ここの警報はどれも、上げた役割が下げる ADAC の警報で、OSS は消せません（6.8）。一覧の属性（`operationalState` など）、記録の 3 つのほかの属性、資源の知らない名前も、同じ答えにします。書き換えられるのは 3 つの属性だけなので、知らない名前も書き換えられないものとして扱います |
| 値が `null`（RFC 7396 の削除） | 403。`ATTRIBUTE_NOT_WRITABLE`。警報の記録を消すのはシステムだけで（6.5）、確認の属性を空にする操作もありません。記録そのもの（`"<id>": null`）も同じです |
| 一覧に無い警報の id を鍵にする | 400。`type` は `IE_NOT_FOUND`、`reason` は `ATTRIBUTE_NOT_FOUND`、`badAttributes` は `#/attributes/alarmRecords/<id>`。6.6.5.3.2 はこの理由を削除に当てていますが、`type` の意味（6.6.4）は変える対象が無いことで、6.6.5.1 は理由の一覧を閉じたものとしていません。確認の前に、その警報がほかの確認で一覧から消えたときも、この答えになります。鍵の前のノードの名前がインベントリに無いか、鍵に `.` が無ければ、どのノードにも聞かずにこの答えにします。ノードが 404 で断ったときは、ノードは最初の 1 つしか名前を挙げないので、OAM がそのノードの一覧をもう一度読み、無かった記録をすべて `badAttributes` に並べます。読めなかったか、どれもあったときは、その要求のノードの記録をすべて並べます |
| ノードが 400 で断る | 400。`VALIDATION_ERROR`、`reason` は `NEW_ATTRIBUTE_VALUE_INVALID`。`badAttributes` は付けない。ノードは `operator` と `ack_system_id`、つまり `ackUserId` と `ackSystemId` を 256 文字までに限っていて（`internal/maintenance` の `maxAckName`）、超えると 400 を返します。上限はノードが決めるもので、OAM は同じ数を持ちません。どちらの属性かは、ノードの答えからは分かりません |
| ノードが答えない、2xx・400・404 のほかで答える | 502。本文は付けない（下の「誤りの形」） |
| 経路に問い合わせを付ける | 400。`VALIDATION_ERROR`。6.3.2 は、patch の宛先の URI に問い合わせを付けないとしています |
| 2 台以上のノードの警報の記録を並べる | 500。`SERVER_LIMITATION`（下の段落） |

1 つの `PATCH` に並べてよいのは、1 台のノードの警報の記録だけです。そのノードは、自分の一覧のロックの中で全部を確かめてから変えます。

TS 32.158 6.3.1 は `PATCH` を不可分とし、一部だけを当てることを禁じています。ノードをまたぐ確認を不可分にするには、ノードのあいだの 2 相の確定が要ります。そこで、2 台以上にまたがる `PATCH` は 500 で断ります（`SIMPLIFIED:`）。OAM は、本文の形と、鍵がどのノードを指すかを、どのノードにも聞く前に確かめます。断るときは、どのノードの一覧も変わっていません。

ほかの Patch の形（`application/json-patch+json` と 3GPP の 2 つの形）と、`Content-Type` の無い要求は 415 で、`Accept-Patch` に `application/merge-patch+json` を書きます（RFC 5789 2.2、3.1）。`charset` などの引数は見ません。3GPP の 2 つの形は、仕様書のあいだで media type の綴りが揃っていません（下の「仕様のあいだの食い違い」）。

OAM は、鍵の前のノードの名前で宛先を決め、そのノードの保守用の API で確認を渡します。保守用の `AckStateRequest` には、`ackSystemId` に当たる欄と、複数の警報の記録をまとめて確かめる形を足します。

#### OAM の画面からの確認

OAM の画面は、`/oam/v1/` の独自の API（`PUT /oam/v1/nodes/{node}/alarms/{id}/ack-state`）で確認します。この要求にも、OSS の `PATCH` と同じく運用者の名前（`operator`）を必ず求めます。TS 28.111 7.4.1 の `ackUserId` は、確認の状態を最後に変えた利用者です。`notifyAckStateChanged` は、これを必須（M）の値として運びます（8.8.2）。`TS28111_FaultNrm.yaml` の `NotifyAckStateChanged` も、`ackUserId` を `required` にしています。名前の無い確認を通すと、OSS には `"ackUserId":""` の通知が届きます。確認の取り消しも同じです。TS 28.111 6.7 は、consumer が自分の名前を渡すかどうかを任意（may）にしています。それでも、どの入口でも名前を求めることにしました。通知が必須の値を欠かないようにするためです。名前の無い要求は、どのノードにも聞かずに 400 で断ります。ノードの保守用の API も、`operator` の無い確認を 400 で断ります。これで、OSS の `PATCH`、`/oam/v1/`、保守用の API のどの入口から確認しても、記録と通知は変えた人の名前を持ちます。

OAM は、その確認の `ackSystemId` に自分の名前を入れて、ノードに渡します。運用者は OAM を通して確認の状態を変えるので、7.4.1 の「確認の状態を最後に変えたシステム」は OAM です。名前は、`oam` の役割の設定のファイルの `node` です。Compose では `oam` です。ノードが保持の時間を過ぎた警報を自分で確認するときも、自分の `node` を `ackSystemId` にしています。それと同じ形です。SubNetwork の DN（通知の `systemDN`）は使いません。3GPP の口を出さない OAM でも、画面から確認できるためです。要求の本文には `ack_system_id` を書けず、書けば 400 です。システムの名前を決めるのは OAM で、呼び出し側ではないためです。

### 購読

`NtfSubscriptionControl` の属性は、OpenAPI（`TS28623_SubscriptionControlNrm.yaml` 19.4.0）の 4 つです。

| 属性 | 扱い |
| --- | --- |
| `notificationRecipientAddress` | 必須。`http` か `https` の URL で、host を持ち、利用者の情報と fragment を持たない。無ければ 400 で `NEW_OBJECT_ATTRIBUTE_VALUE_MISSING`、ほかの形なら 400 で `NEW_ATTRIBUTE_VALUE_INVALID` |
| `notificationTypes` | 下の「通知」の表の 6 つのうちのいくつか。無ければ 6 つすべて。ほかの名前、同じ名前の 2 回目、空の配列は 400 で `NEW_ATTRIBUTE_VALUE_INVALID`。空の配列は、何の通知も受けない購読になるので断る |
| `scope` | 無いか、`{"scopeType":"BASE_ALL"}` だけ。基点は購読を含む SubNetwork です（TS 28.622 4.3.22.1）。ノードで分けたいときは `notificationFilter` を使う。OpenAPI の列挙にあるほかの値（`BASE_ONLY`、`BASE_NTH_LEVEL`、`BASE_SUBTREE`）と、`scopeType` の無い scope（`BASE_ONLY` に当たる）は 500 で `SERVER_LIMITATION`。OpenAPI のもう 1 つの形、`dataNodeSelector` で選ぶ scope も 500 で `SERVER_LIMITATION`、`badAttributes` は `#/attributes/scope/dataNodeSelector`。列挙に無い値は 400 で `NEW_ATTRIBUTE_VALUE_INVALID` |
| `notificationFilter` | Jex の条件の式。通知の本文のどの値にも当てられる（4.3.22.1）。例は `contains(href,"ManagedElement=scscf") and perceivedSeverity="CRITICAL"`。文法に合わない式を書いた `PUT` には、400 の `NEW_ATTRIBUTE_VALUE_INVALID` を返す（TS 32.158 6.6.5.3.2）。文法には合うが `internal/jex` が評価しない式（配列の位置を選ぶ式など）は、500 で `SERVER_LIMITATION`。読み出しの問い合わせの 400 と 500 の分け方と同じ |

`PUT` の本文は、ほかに次のように確かめます。誤りの `reason` は TS 32.158 6.6.5.4 の値です。

| 本文 | 答え |
| --- | --- |
| JSON として読めない、OpenAPI に無い名前（`lastSequenceNo` など）がある | 400。`VALIDATION_ERROR`、`reason` は `NEW_OBJECT_REPRESENTATION_INVALID`。`badAttributes` は付けない |
| OpenAPI にはあるが、OAM が受けない名前がある。`HeartbeatControl`（TS 28.622 4.3.21）と `VsDataContainer`（4.3.9。`Top`（4.3.29）が含むので、どのオブジェクトも持てる） | 500。`SERVER_LIMITATION`、`badAttributes` は `#/HeartbeatControl` か `#/VsDataContainer`。本文は正しく、意味も分かるが、producer の限りを越える要求です（TS 32.158 6.6.4）。中身の形は確かめません。値が `null` なら、その名前は無いものとして扱います |
| `id` が経路の id と違う | 400。`NEW_ATTRIBUTE_VALUE_INVALID`、`badAttributes` は `#/id` |
| `objectClass` が無い | 400。`NEW_OBJECT_ATTRIBUTE_VALUE_MISSING`。TS 32.158 5.1.2 は、作るときの本文に id とクラスの名前を求めています |
| `objectClass` が `NtfSubscriptionControl` でない | 400。`NEW_OBJECT_CLASS_NAME_INVALID` |
| `objectInstance` があり、経路の DN と違う | 400。`NEW_ATTRIBUTE_VALUE_INVALID` |
| `Content-Type` が `application/json` でない | 415。本文は付けない |
| 経路に問い合わせを付ける | 400。`VALIDATION_ERROR`。5.1.2 は、作るときの宛先の URI に問い合わせを付けないとしています。`DELETE` も同じ（5.4） |

400 に当たる問題と 500 に当たる問題が 1 つの本文にあれば、400 を答えます。読み出しの問い合わせと同じく、OSS が直せる誤りを先にします（6.6.5.1）。`SERVER_LIMITATION` は、正しく読めて意味も分かる要求に当てる型だからです（6.6.4）。

購読の id は、64 文字までの英字、数字、`-`、`_`、`.` に限ります。この範囲なら、経路の 1 区切りにも DN の id にもそのまま書けます。ほかの id の `PUT` は 400 で `NEW_ATTRIBUTE_VALUE_INVALID`、`badAttributes` は `#/id` です。`GET` と `DELETE` は 404 です。

購読の `GET` は 1 つのオブジェクトを読むので、問い合わせを受けません。ProvMnS の問い合わせの名前（`scope`、`filter`、`attributes`、`fields`、`dataNodeSelector`）は 500 で `QUERY_PARAMS_TOO_COMPLEX`、ほかの名前は 400 で `QUERY_PARAM_NAMES_INVALID` です。

TS 28.622 4.3.22.1 では、`notificationTypes` が無い購読は、producer が出せるすべての種類の通知を受けます。この口が出せるのは故障管理の 6 つの通知だけで、TS 28.532 の構成の通知（`notifyMOICreation`、`notifyMOIDeletion`、`notifyMOIAttributeValueChanges`、`notifyMOIChanges`）は出しません（`SIMPLIFIED:`）。そのため、4.3.22.1 の、購読の作成と削除を構成の通知で知らせる決まりも当たりません。

TS 28.622 4.3.22.2 には、ほかに `lastSequenceNo`、`administrativeState`、`operationalState`、`availabilityStatus` があります。この版の OpenAPI にはどれも無いので、出しません。

購読は OAM の共有の保存先に置きます（下の「複数のインスタンス」）。購読の数は OSS の数で決まり、加入者の数には依りません。それでも上限を設定で持ち、超える `PUT` は 500 で `SERVER_LIMITATION` にします（TS 32.158 6.6.4）。設定の項目は `oam` の節の `max_ntf_subscriptions` で、既定は 16 です。今ある購読の置き換えは、上限のときも受けます。

保存先は、すべての購読を 1 つの項目にまとめて持ちます。1 つのインスタンスなら `data_dir` の `ntf-subscriptions.json`、複数なら `internal/kvstore` の 1 つの鍵（`oam:ntf-subscriptions`）です。変えるときは、まとめて読み、変えて、読んだ版での `CompareAndSwap` で書き戻します。ファイルなら、プロセスのロックの中で書き、名前を変えて置き換えます。上限を確かめることと購読を足すことが 1 回の書き込みになるので、2 つのインスタンスが同時に作っても、合わせて上限を超えません。すべての購読を読むのも 1 回の読み出しです。上限があるので、項目は大きくなりません。

保存先を持たない OAM（共有の保存先も `data_dir` も無い）の購読の経路は、503 で `SERVICE_DISABLED` を返します。6.6.4 の、producer が自分で止めていて要求を扱えないときの値です。保存先が失敗したときは 500 で、本文は付けません。どの `type` も、状態コードより多くは伝えないためです。失敗の中身はログに書きます。

`internal/mns` の `ErrorType` と `ErrorReason` には、この作業で `SERVICE_DISABLED` と、6.6.5.4 の `NEW_OBJECT_REPRESENTATION_INVALID`、`NEW_OBJECT_CLASS_NAME_INVALID`、`NEW_OBJECT_ATTRIBUTE_VALUE_MISSING` を足しました。

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
  - DN の前置き（`DC=`）を持たないので、TS 32.158 4.2.3 の終わりのとおり、URI の host は producer の自分のもの（OAM の名前）にします（「DN の前置き」）。
- `systemDN` は `SubNetwork={sn}` です。MnsAgent の MOI を出さないので、11.0.2 のとおり根の MOI の DN にします。
- `notificationId` は、警報の通知ならノードが決め、警報の記録の `notificationId` にも書きます。ノードの値は乱数です。一覧の通知（`notifyPotentialFaultyAlarmList` と `notifyAlarmListRebuilt`）なら OAM が決めます。OAM の値は乱数ではなく、通知が何についてのものかを表す値の hash です。ノードの再起動ならリングバッファの新しい識別、出来事の欠けならリングバッファの識別と欠けた位置、捨てた記録なら捨てた出来事の番号を使います。ノードが答えなくなったときと戻ったときは、見張りの状態に置いた乱数の種と、それまでに答えなくなって戻った回数を使います。同じ通知をどのインスタンスが送っても、同じ id になります（下の「届かなかった通知」）。どちらの値も、JSON の数として失われない 53 ビットに収めます。11.0.2 は、同じ MOI の通知のあいだで重ならないことを求めています。購読ごとに送る `notifyAlarmListRebuilt`（下の「届かなかった通知」）だけは、1 つのインスタンスしか送らないので、乱数にします。
- `sequenceNo` と `subscriptionId` は入れません。11.0.2 は、この 2 つを、確実に届ける仕組み（reliable notification transport）を持つ producer に限って持つものとしています。この口はその仕組みを持ちません（下の「届かなかった通知」）。
- `notifyAlarmListRebuilt` の `reason` は、8.5.2 の値から選びます。答えなくなったノードが戻ったときは `System-NE communication error`、ノードが再起動したときは `System restarts`、出来事を失ったときと警報の記録を捨てたときは `indeterminate` です。`alarmListAlignmentRequirement` は、どれも通知を送れなかった警報があるので `ALIGNMENT_REQUIRED` にします。綴りは OpenAPI の列挙のものです（下の「仕様のあいだの食い違い」）。
- `notifyPotentialFaultyAlarmList` の `reason` は、8.10.2 の値の `serviceprovider-NE communication error` です。
- 送らない通知は、`notifyNewSecAlarm` と `notifyChangedSecAlarmGeneral`（セキュリティの警報を持たない）、`notifyComments`（コメントを作らない）、`notifyCorrelatedNotificationChanged`（相関を作らない）、TS 28.111 7.5.2 の構成の通知（`AlarmList` の状態の属性が変わらない）です。

### 届かなかった通知

ある OSS への `POST` が失敗しても（期限切れ、接続できない、2xx 以外）、その出来事は送り終えたものとして扱い、見張りの状態を進めます。1 つの OSS が止まっても、ほかの OSS への通知とほかの出来事を止めないためです。失敗した通知を送り直すことはしません。

転送（3xx）の答えも失敗です。OAM は転送に従いません。TS 32.158 5.5.4 では、OSS は購読の宛先への `POST` に答えます。301 と 302 に従うクライアントは、`POST` を `GET` に変えて送り直すことがあり（RFC 9110 15.4.2、15.4.3）、その `GET` は通知を運びません。従うと、通知の無い `GET` への 200 を、届いたと数えることになります。

ただし、それは落とした宛先に印を付けられたときだけです。購読の保存先を読めなかったとき、または印を書けなかったときは、見張りの状態を進めません。そのままでは、通知も、一覧を読み直させる印も無いまま、警報の変化が消えてしまうためです。見張りは、待ってから同じ出来事の通知を送り直します。待ちは長く待つ `GET` の長さの 8 分の 1 から始め、失敗のたびに倍にし、長く待つ `GET` の長さで止めます。保存先が答えないあいだも、聞く回数は待ちごとに 1 回で済みます。送り直すと、前の回で受け取れた宛先にも同じ通知がもう一度、同じ `notificationId` で届きます。

代わりに、その購読に「通知を落とした」印を、共有の保存先の購読の項目に付けます。印の付いた購読には、ほかの通知を送りません。代わりに `notifyAlarmListRebuilt` を `systemDN` を基点に `ALIGNMENT_REQUIRED` で送ります。`reason` は `indeterminate` です。この通知が届いたら印を外し、次の通知からまた送ります。OSS は、それを受けて一覧を読み直します（TS 28.111 5 章の REQ-FM-MC-5 の動機、8.5.2）。この使い方は仕様に無いものです（下の「仕様に無い振る舞い」、`SIMPLIFIED:`）。

印の付いた宛先を飛ばすのは、答えない OSS がほかの OSS を遅らせないためです。1 つの出来事の通知は、すべての宛先が答えてから次に進みます。答えない宛先に送り続けると、通知のたびに送る期限（5 秒）だけ待つことになります。そのあいだに出来事が続くと、見張りがノードのリングバッファに追いつけず、出来事が欠けます。そうなると、答えている OSS にも `notifyAlarmListRebuilt` を送ることになります。印で飛ばせば、答えない OSS が見張りに課す待ちは、印を付けるまでの 1 回の期限だけです。

- 印の付いた宛先への `notifyAlarmListRebuilt` は、警報が変わるかどうかに関わらず試します。各インスタンスが、見張りを取り直す回（5 秒ごと）に購読を 1 回読み、試す時刻が来た宛先に送ります。通知を送るついでに試す形では、落としたあとに警報が変わらなければ、OSS が戻っても読み直しを求められないためです。試すのは、どのインスタンスでも、30 秒（`oam.Config.MissedRetryInterval`）に 1 回だけです。購読の項目に次に試してよい時刻を置き、試す見張りは、その時刻を `CompareAndSwap` で先に進めてから送ります。試す `POST` は別の goroutine で送るので、まだ答えない宛先でも、通知も見張りの回も待ちません。この時刻はインスタンスの時計で決めます。試す回数を抑えるだけの値で、ずれても誤った通知にはならないためです。
- 印には世代（`MissedEpoch`）があり、落とすたびに 1 つ上がります。印を外すのは、送った `notifyAlarmListRebuilt` が試したときの世代のままのときだけです。ある見張りが印を外そうとするあいだに、古い写しで送っていたほかの見張りがまた落としたときは、世代が上がっているので印が残ります。印は真偽の値だけだと、後から落とした分の読み直しを求めないまま印が消えてしまいます。
- 試す順番を取った見張りは、試す時刻と一緒に「順番を取った」（`Claimed`）と書いてから送ります。印の付いた購読を飛ばした見張りは、読んだ写しで順番が取られていれば、その世代を 1 つ上げます（`MarkNtfSubscriptionSkipped`）。OSS は、届いた `notifyAlarmListRebuilt` を受けて一覧を読み、それから答えるかもしれません。そのあいだに上がった警報を飛ばしても世代が変わらなければ、OSS の答えで印が外れ、OSS の写しはその警報を欠いたままになります。世代が上がっていれば、答えが来ても印は外れず、次の順番でもう一度 `notifyAlarmListRebuilt` を送ります。飛ばした見張りが書く前に答えが着いて印が外れていたときも、世代が同じなら印を付け直します。この印は試す時刻を持たないので、次の回にすぐ送ります。
- 読んだ写しで順番がまだ取られていなければ、飛ばしても世代を上げません。飛ばした警報は、見張りが購読の写しを読む前に上がっています。順番はそのあとに取られ、`notifyAlarmListRebuilt` もそのあとに送られるので、OSS がそれを受けて読む一覧にはその警報があります。飛ばすたびに書くと、OSS が止まっているあいだ、どのノードの警報も、すべてのインスタンスが共有する購読の項目に書き込むことになります。順番が取られたときだけ書けば、書くのは、順番が取られてから世代が上がるまでの購読の読み出しだけで、1 つの読み出しにつき 1 回です。上げたあとの世代はまだ順番が取られていないので、そのあとの読み出しでは書きません。飛ばしたことを書けなかったときは、印を書けなかったときと同じく、見張りの状態を進めずに送り直します。
- `notifyAlarmListRebuilt` の `POST` のあいだに警報が上がり続けると、印は外れず、OSS は順番ごとに一覧の読み直しを求められます。警報は失いません。
- この `notifyAlarmListRebuilt` も、購読の `notificationTypes` と `notificationFilter` で選びます。購読が選ばないなら送らずに、印を外します。購読がその通知を受けないと決めているので、OSS には何も知らせないまま、次の通知からまた送ります。
- 印は、OSS が同じ id の購読を `PUT` で置き換えても残ります。置き換えた購読も、落とした通知を受けていないためです。属性を変える置き換えは、印の世代を 1 つ上げます。属性が同じなら宛先も同じなので、世代も取られた順番もそのままにします。上げると、置き換えのたびに印が外れなくなり、`notifyAlarmListRebuilt` に答える前に購読を置き直す OSS は、ほかの通知をずっと受けられなくなります。試す見張りは、順番を取る前に読んだ写しの宛先に送ります。世代が変わらなければ、読んだあとで宛先が替わっても順番を取れてしまい、古い宛先の 2xx で印が外れて、新しい宛先は読み直しを求められません。世代が上がっていれば、古い写しでは順番を取れず、順番を取ったあとで替わったときも、古い宛先の答えでは印が外れません。新しい宛先には、次の順番で送ります。印の付いた購読の属性は同じ世代のあいだ変わらないので、順番を取れた写しの宛先が、そのときの宛先です。
- 1 回の読み出しで読んだ購読の写しは、その出来事の答えを送り終えるまで使います。ほかの見張りが付けた印を知らずに送ると、その見張りもその宛先で 1 回の期限を待ち、印を付け直します。見張りごとに 1 回の待ちで済むので、写しを読み直しはしません。
- インスタンスが止まるときに途中で切れた `POST` は、落としたとは数えません。印を付けず、見張りの状態の番号も進めません。次に見張るインスタンス（1 つのインスタンスなら、起動し直したもの）が、同じ通知を同じ `notificationId` で送り直します。OSS は何も落としていないので、`notifyAlarmListRebuilt` は要りません。

印を外す前後に上がった警報を必ず届けるには、もう 1 つ作り方がありました。印を先に外してから `notifyAlarmListRebuilt` を送り、送れなければ付け直す作りです。印が外れていれば、見張りは警報をそのまま送ります。この作りは選びません。複数のインスタンスで動かすと成り立たないためです。印を外してから OSS が答えるまでのあいだに、送っていたインスタンスが落ちたり止められたりすると、印は付け直されません。付け直しを保存先に書けなかったときも同じです。OSS は、落とした通知の分を読み直すよう求められないままになります。世代を上げる作りでは、印は OSS が受け取るまで共有の保存先に残ります。送っていたインスタンスが止まっても、次の順番でほかのインスタンスが送ります。また、印を先に外すと、OSS がまだ答えないとき、そのあいだに出来事を送るすべてのインスタンスの見張りが、その宛先で 1 回ずつ期限を待ちます。試すたびにこの待ちが起き、答えない OSS が見張りに課す待ちが、印を付けるまでの 1 回では済まなくなります。

同じ通知がもう一度届くのは、OAM のインスタンスが入れ替わったとき、送っている途中でインスタンスが止まったとき、購読の保存先が失敗して送り直したときだけです（下の「複数のインスタンス」）。そのときの通知は、前と同じ `notificationId` を持ちます。

## 仕様に無い振る舞い

次のことは、仕様が求めていないか、仕様の外の使い方です。OSS の写しが、通知なしにずれないようにするために足します。

| 振る舞い | 仕様との関係 |
| --- | --- |
| 確認されない cleared の警報が保持の時間を過ぎたら、ノードが自分で確認する。`ackUserId` は保持の時間で消したことを示す固定の名前（`retention`）、`ackSystemId` はノードの名前。`notifyAckStateChanged` が出るので、OSS は cleared と確認の 2 つの通知から、警報の記録が消えたと分かる（「保持の時間の消去」） | 作る前は黙って消していた。今は `internal/maintenance/alarmlist.go` の `expire` がノードの確認にしている。6.14 では、確認を持つ producer は、cleared の記録を確認されるまで残す。producer が自分で確認するという動きは仕様にあり、TS 28.111 5 章の REQ-FM-MC-12 は、確認の操作を持たない producer に、警報を自分で確認できることを求めている。確認の操作を持つ producer が保持の時間で自分で確認することは、仕様に書かれていない |
| 一覧がいっぱいで警報の記録を捨てたら、そのノードを基点に `notifyAlarmListRebuilt` を `ALIGNMENT_REQUIRED` で送る | 7.3.2.1 は捨てることを許しているが、通知は求めていない。一覧は作り直していないが、通知を送れなかった警報があるという 8.5.2 の `alarmListAlignmentRequirement` の意味に合わせて使う。コードに `SIMPLIFIED:` を書き、手順書の「簡略化したこと」にも載せる |
| 届かなかった通知のあと、その購読には `notifyAlarmListRebuilt` のほかを送らず、それをその購読だけに送る（上の「届かなかった通知」） | 8.5.1 の、一覧を作り直したときの通知ではない。購読ごとに送るのも、仕様に無い使い方。コードに `SIMPLIFIED:` を書き、手順書の「簡略化したこと」にも載せる |
| 答えないノードの警報の記録を `GET` の答えに入れない | 6.11 は、その部分の記録が更新されなくなり信頼できないとしているが、記録を外すかどうかは書いていない。OAM は写しを持たないので、出せる記録が無い |

## 複数のインスタンス

OAM は複数のインスタンスで動けます（`internal/oam/doc.go`）。読み出しと確認は、今の `/oam/v1/alarms` と同じく、要求のたびにノードに聞くので、どのインスタンスでも答えられます。通知には、次のものをインスタンスの外に置きます。

| 置くもの | 中身 | 期限 | 数を決めるもの |
| --- | --- | --- | --- |
| 購読 | `NtfSubscriptionControl` の id と属性、「通知を落とした」印 | なし | OSS の数 |
| 見張りのリース | ノードごとに、見張っているインスタンスと世代 | あり（15 秒） | ノードの数 |
| 見張りの状態 | ノードごとに、世代、ノードのリングバッファの識別、送り終えた最後の出来事の番号、答えないと決めたかどうか、OAM が決める `notificationId` の種と、答えなくなって戻った回数 | なし | ノードの数 |
| インスタンスの在席 | インスタンスごとに、動いていることを示す項目 | あり（15 秒） | OAM のインスタンスの数 |

### 流れ

1. ノードは、警報の一覧が変わるたびに、出来事（足した、変えた、cleared にした、確認の状態が変わった、捨てた）を、番号を付けてリングバッファに積みます。リングバッファは上限があり、古い出来事から消えます。リングバッファには、プロセスが起動したときに選ぶ乱数の識別を付けます。保守用の API の `instance` はホストや pod の名前で、同じコンテナを起動し直しても変わらないので、再起動の印には使えません。
2. OAM の各インスタンスは、インベントリのノードごとに見張りのリースを取りにいきます（下の「リースの分け方」）。取れたインスタンスは、見張りの状態の世代を 1 つ上げてから、そのノードの保守用の API に、見張りの状態の番号より後の出来事を長く待つ `GET` で聞きます。
3. 出来事が届いたら、そのインスタンスが購読を読み、`notificationTypes` と `notificationFilter` で選んだ宛先に通知を送ります。1 つの出来事の通知を送り終えるたびに見張りの状態の番号を進め、答えにあった出来事を送り終えたら、次を聞きます。
4. ノードが答えなくなったら、リースを持つインスタンスが `notifyPotentialFaultyAlarmList` を送り、見張りの状態に「答えない」と書きます。1 回の `GET` に答えない（つながらない、期限までに答えない、200 のほかで答える）ことを、答えないとします。そのあとは、長く待つ `GET` の長さだけ待って聞き直します。戻ったら `notifyAlarmListRebuilt` を送ります。リングバッファの識別が変わっていたら、ノードが再起動したと分かります。見張りの状態の番号がリングバッファからもう消えていたら、出来事を失ったと分かります。どちらも `notifyAlarmListRebuilt` を送ります。このどれかに当たる答えでは、出来事の通知の前に `notifyAlarmListRebuilt` を 1 つだけ送ります。いくつも当たるときの `reason` は、再起動、戻った、出来事を失った、の順に選びます。再起動したときは、その答えの出来事を送らず、新しいリングバッファの終わりから見張ります。新しいプロセスがそれまでに上げた警報は、OSS が読み直す一覧にあります。`notifyAlarmListRebuilt` の後でそれらを送ると、OSS には、読み直した一覧と重なって、しかも順が前後して届くためです。
5. まだどのインスタンスも見張ったことのないノード（見張りの状態にリングバッファの識別が無い）は、最初の答えのリングバッファの終わりから見張ります。それより前の出来事の通知は送りません。前の出来事の結果は警報の一覧にあり、OSS は購読したあとで一覧を読むためです。保存先が見張りの状態を読めなかったとき（ファイルが空など）も、同じく終わりから見張ります。そのときは、最後に書いた状態からの出来事を送れていないので、そのノードを基点にした `notifyAlarmListRebuilt` を `indeterminate` で送り、ログに書きます。
6. 見張りの状態は、変わったときだけ書きます。長く待つ `GET` が出来事なしで終わっても、書き込みはありません。何も起きていないノードは、保存先に書き込みを生みません。

### リースと見張りの状態

期限は、どれも保存先が決めます。リースは期限付きの項目で、期限が来ると保存先が消します。インスタンスの時計は使いません。

- リースを取るのは、項目が無いときだけ書く `CompareAndSwap`（版を空にする）です。延ばすのは、自分が読んだ版での `CompareAndSwap` で、同じ期限を付け直します。
- 見張りの状態は期限の無い項目です。リースを取ったインスタンスは、まず見張りの状態の世代を 1 つ上げて書きます。そのあと番号を進めるたびに、自分が書いた版での `CompareAndSwap` で書きます。
- 世代を上げるときは、見張りの状態を読んでから書くまでのあいだに、取ったときの版でリースを延ばします。延ばせなければ、世代を上げず、見張りを取りません。リースを取ったあと、期限より長く止まっていたインスタンスは、そのあいだにほかのインスタンスがリースを取り、世代を上げているかもしれません。版だけを確かめて書くと、その持ち主の状態の上で世代をもう 1 つ上げて見張りを奪い、本当の持ち主の書き込みを止めてしまいます。延ばしたあと書く前にまた止まり、そのあいだにほかのインスタンスが世代を上げたときは、読んだ版が古くなっているので書けず、読み直したあとの延長で気付きます。
- ほかのインスタンスがリースを取ると、見張りの状態の世代が上がり、版が変わります。古いリースの持ち主が番号を書こうとしても `CompareAndSwap` が失敗するので、番号を戻せません。失敗したら、その持ち主はそのノードの見張りをすぐにやめます。
- 古い持ち主が止まっていたあいだに新しい持ち主が同じ出来事を送ると、同じ通知が 2 回届きます。どちらも同じ `notificationId` を持つので、OSS は重なりを見分けられます。この重なりは許します。出来事は失いません。新しい持ち主は、最後に書かれた番号から続けるためです。

時間の関係は次のとおりです。

| 値 | 長さ | 決まり |
| --- | --- | --- |
| リースの期限 | 15 秒 | 下の 3 つより長い |
| リースを延ばす間隔 | 5 秒 | 期限の 3 分の 1。長く待つ `GET` や通知の送信とは別に延ばすので、それらが長引いても切れない。2 回続けて延ばせなくても期限の中に収まる。`oam.New` は、延ばす間隔がリースの期限の半分より短いことを確かめ、そうでなければ起動しない |
| 長く待つ `GET` の長さ | 5 秒 | リースを延ばす間隔以下。リースを失ったと分かってから、見張りをやめるまでの遅れの上限になる |
| 1 つの通知を送る期限 | 5 秒 | 宛先ごと。1 つの出来事の通知は、購読の数に上限を付けて同時に送る |
| 通知を落とした宛先に `notifyAlarmListRebuilt` を試す間隔 | 30 秒 | すべての見張りを合わせて、宛先ごとに。試す `POST` は通知を待たせない |

### リースの分け方

インスタンスを足したときに、ノードの見張りが分かれるようにします。

- 各インスタンスは、在席の項目を期限付きで書き、リースと同じ間隔で延ばします。在席の項目の名前は、保存先の並べた索引にも入れます。読むときに項目が無い名前は、索引から外します。
- 自分の取り分は「ノードの数 ÷ 在席のインスタンスの数」を切り上げた数です。取り分より多いリースは、出来事を送り終えたところで手放します（`CompareAndDelete`）。取り分に届くまでは、まだ持ち主の無いリースを、毎回並びを変えた順で取りにいきます。取り分と比べる数には、手放している途中のリースを入れません。入れると、手放し終わる前に次の回が来たとき、もう 1 つ余分に手放してしまいます。
- 1 回の間隔あたりの手間は、すべてのインスタンスを合わせて、インスタンスの数とノードの数の積に比例します。ノードの数で決まり、加入者の数にも購読の数にも依りません。

### この形にした理由

- 1 台のノードの出来事を送る持ち主は、どの時点でも 1 つのインスタンスです。持ち主が替わるときだけ重なりがあり、そのときも番号は戻りません。届く順は、重なりを除けば、ノードで起きた順のままです。
- ノードは OAM の場所を知らなくて済みます。今の保守用の API と同じく、OAM がノードに聞く向きのままです。ノードから OAM に送る形では、同じ設定のファイルを共有する 2 台のノードが、OAM のインベントリでどちらなのかを名乗れません。
- ノードが答えなくなったことに、見張るインスタンスが気付けます。ノードから送る形では、止まったノードは何も送らないので、OAM は気付けません。

保存先は、ほかの OAM の保存先と同じく、インタフェースの後ろに置きます。1 つのインスタンスなら `data_dir` のファイル（ノードごとの `alarm-watch/<ノード>.json`）に置き、書き込みのたびにファイルとディレクトリを同期してから置き換えます。リースはいつもそのインスタンスのものです。複数のインスタンスなら `internal/kvstore` に置きます（`internal/kvstore/oamstore`）。鍵は、ノードごとのリース（`oam:watch-lease:<ノード>`、値は持ち主のインスタンスの名前）、ノードごとの見張りの状態（`oam:watch-state:<ノード>`）、インスタンスごとの在席（`oam:presence:<インスタンス>`）と、在席の並べた索引（`presence`）です。インスタンスの名前は、プロセスが起動するときに選ぶ乱数です。在席は「動いていること」だけを表すので、起動し直したインスタンスは別のインスタンスとして数え、前の名前の在席は期限で消えます。`internal/kvstore/kvlock` は延ばせないロックなので、見張りのリースには使えません。`data_dir` のファイルの保存先は 1 つのインスタンスのためのものです。2 つ目のインスタンスが同じディレクトリを使うと、どちらもすべてのノードを見張り、同じ通知が 2 回ずつ届きます。これを見つける仕組み（ロックのファイルなど）は作りません。プロセスが止まったあとに残るロックを、どのプロセスのものか確かめる手段が要り、OS ごとに作りが違うためです。複数のインスタンスには、設定の `store` の節で共有の保存先を与えます。

見張る側は、リースを延ばす goroutine を止めてから、リースを手放します。手放したあとで延ばす書き込みが届くと、ほかのインスタンスが取ったリースを上書きしてしまうためです。

届かなかった通知はログに書き、指標で数えます。指標は `oam_alarm_notifications_total` で、ラベルは `notification_type`（6 つの通知の名前）と `result`（2xx を受けた `delivered` と、受けなかった `failed`）です。インスタンスが止まるときに途中で切れた `POST` も、`failed` に数えます。遅い OSS は、そのノードの出来事の流れを送る期限の分だけ遅らせますが、ほかのノードの流れは止めません。

## 誤りの形

誤りの本文は TS 32.158 6.6.3 の形にし、型は OpenAPI の `ErrorResponseGet`（`GET`）、`ErrorResponseDefault`（`PUT`、`DELETE`、JSON Merge Patch）を使います。`type` と `reason` の値は 6.6.4 と 6.6.5 から選びます。上の各節に書いたもののほか、ノードに届かなかった確認は 502 にします。TS 32.158 6.6.2 の 502 は、ゲートウェイとして働く producer が、中継した先から正しい答えを得られなかったことを表します。OAM がノードに確認を渡す形に合い、今の `/oam/v1/` の API とも同じです。

本文を付けるのは、6.6.4 が `type` を決めている状態コード（400、403、409、422、500、503）だけです。404（経路がここのオブジェクトを指さない）、405（`Allow` を付ける）、413（本文が 64 KiB を超える）、415（`PATCH` の答えには `Accept-Patch` を付ける）、502 には本文を付けません。6.6.4 はこれらに `type` を決めていません。404 については、状態コードのほかに足す情報が無いとも書いています。`ErrorResponseGet` と `ErrorResponseDefault` は `type` を必須にしているので、`type` の無い本文は書けません。独自の値を作るより、状態コードとヘッダだけで答えます。

`title` は、問題の種類ごとに決めた短い英語の文です。値そのものは入れず、`badQueryParams` と `badAttributes` に書きます。6.6.3.1 は、`title` が出るたびに変わらないことを求めています。

`internal/mns` の `ErrorType` と `ErrorReason` には、使う値を定数として足します。この作業で足したのは、6.6.5.2 の `QUERY_MALFORMED` です。

本文の `Content-Type` は `application/json` にします。TS 32.158 6.6.3.1 は誤りの本文の media type を `application/vnd.3gpp.error+json` としていますが、OpenAPI はどの誤りの答えも `application/json` で定義しています。OpenAPI から作った consumer が読めるように、OpenAPI に合わせます。

## 仕様のあいだの食い違い

読んだ版のあいだで、次のことが揃っていません。どれも、この記録の選び方に関わります。

| 食い違い | 選んだこと |
| --- | --- |
| `TS28111_FaultNrm.yaml` の `servers` は `{MnSRoot}/FaultSupervisionMnS/{MnSversion}` だが、`paths` は空。TS 28.111 6.6 は、読み出しに TS 28.532 の getMOIAttributes を使うとしている | 経路は `TS28532_ProvMnS.yaml` の `{MnSRoot}/ProvMnS/{MnSVersion}/{URI-LDN-first-part}/{className}={id}` にする |
| 読み出しの範囲の問い合わせが、TS 32.158 6.1.2 と 6.6.5.2 では `scopeType` と `scopeLevel` の 2 つ、`TS28532_ProvMnS.yaml` では中身が JSON のオブジェクトの `scope` 1 つ | OpenAPI の `scope` だけを受ける。`scopeType` と `scopeLevel` は知らない名前として 400 |
| 警報の記録を条件で選ぶ問い合わせが、TS 32.158 6.7 では `dataNodeSelector`（`filter` はオブジェクトを選ぶもの）、TS 28.111 A.1.4 では `filter` | A.1.4 に従って `filter` で記録を選ぶ。同じ形の `dataNodeSelector` も受ける |
| TS 28.532 12.1.1.1.3 は `dataNodeSelector` の詳しい説明を TS 32.158 の 6.2a に送っているが、TS 32.158 V20.1.0 に 6.2a は無く、説明は 6.7 にある | 6.7 を読む |
| `fields` が何も選ばないとき、TS 32.158 6.2.3 では 404、6.6.5.2 では誤りではない | 6.6.5.2 に従い、200 で記録の無い一覧を返す（上の「読み出しの問い合わせ」） |
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
| JSON Patch と 3GPP の 2 つの Patch の形 | 確認には Merge Patch で足りる。3GPP の形は media type の綴りが揃っていない（「確認で受ける形」） |
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

e2e で通知を受け取るには、上がる警報が要ります。[oam.md](../background/oam.md) の 6 章の「役割ごとの警報の案」のうち、Diameter の相手との接続が切れたときの警報を上げます。HSS を止めると上がり、戻すと下がります。この記録の範囲の外の作業として、先に入れました（「役割の警報を別の作業にするか」）。約 400 行。作りました。上げるのは、HSS に接続する I-CSCF、S-CSCF、IP-SM-GW、MMTel AS、Service Centre の 5 つの役割です。上げ下げの決まりは [oam.md](../background/oam.md) の 6 章の「役割が上げる警報」にあります。

### 保守用の API の出来事

警報の記録に `notification_id` と `ack_system_id` を足します。確認を複数の警報の記録でまとめて受け、1 つのロックの中で確かめてから変えます。保持の時間の消去を、ノードの確認に変えます（「保持の時間の消去」）。出来事のリングバッファと、長く待つ `GET /maintenance/v1/alarm-events` を足します。`SIMPLIFIED:` を直し、[oam.md](../background/oam.md) の 6 章の表と手順書の「簡略化したこと」を合わせます。確かめ方は、出来事の番号と順、リングバッファの上限と失った印、リングバッファの識別、長く待つ答えの期限、`-race` の試験です。約 700 行。作りました。この記録が決めていなかった細部（問い合わせの名前、出来事の形、上限、失った印の決まり）は、[oam.md](../background/oam.md) の 6 章の「出来事のジャーナル」にあります。

### 3GPP の型

新しいパッケージ `internal/mns` に、`AlarmRecord`、`AlarmList-Single` の `Resource`、6 つの通知と `NotificationHeader`、`NtfSubscriptionControl-Single`、`Scope`、`ErrorResponseGet` と `ErrorResponseDefault`、DN と canonical URI の組み立てを置きます。上の「名前を OpenAPI で確かめる試験」と、一覧を比べるスクリプトを入れます。OAM と、e2e の OSS の代わりの両方が使うので、`internal/oam` の外に置きます。約 600 行。作りました。

### Jex の条件の式

新しいパッケージ `internal/jex` に、TS 32.161 A.4 の文法の字句解析、構文解析、JSON の値への評価を置きます。読み出しの `filter` と `dataNodeSelector`、購読の `notificationFilter` で使います。確かめ方は、文法の各規則、比べる式、`contains`、読めない式の誤りの試験です。試験の式は、TS 32.161 Annex B.3 の例を写さず、警報の属性で書き起こします。約 600 行。作りました。

### 警報の一覧の読み出しと確認

`{MnSRoot}/ProvMnS/v19/SubNetwork={sn}/AlarmList=1` の `GET` と `PATCH`、問い合わせ、`unreliableAlarmScope`、誤りの本文を作ります。OAM の設定に、SubNetwork の名前と `{MnSRoot}` を足します。この作業の `unreliableAlarmScope` は、その要求で答えなかったノードだけを数えます。見張りの状態を足すのは、通知（1 つのインスタンス）の作業です。確かめ方は、本物の `internal/maintenance` のノードと答えないノードを使った `internal/oam` の試験と、確認の本文の各行の誤りの試験です。約 800 行。作りました。この記録が決めていなかった細部（置く場所と設定の項目、`fields` の答え、確認の本文の残りの決まり、本文の無い答え）は、上の「経路と操作」「読み出しの問い合わせ」「確認」「誤りの形」にあります。

### 購読

`NtfSubscriptionControl` の `PUT`、`GET`、`DELETE`、検査、上限、保存先のインタフェースと `data_dir` のファイルの実装を作ります。約 500 行。作りました。保存先は、`internal/kvstore` の実装（`internal/kvstore/oamstore`）もこの作業で作りました。検査の細部と、すべての購読を 1 つの項目に置く形は、上の「購読」にあります。

### 通知（1 つのインスタンス）

ノードごとの見張り、出来事から通知への写し、購読ごとの選び分け、送る期限と同時の数の上限、`notifyPotentialFaultyAlarmList` と `notifyAlarmListRebuilt`、「通知を落とした」印、送った数と失敗の指標を作ります。見張りの状態は `data_dir` に置き、`GET` の `unreliableAlarmScope` にも使います。確かめ方は、`httptest` の OSS の代わりで、通知の種類、順、本文の名前、ノードの再起動、リングバッファからの欠落、答えないノード、落とした通知のあとの `notifyAlarmListRebuilt` を確かめる試験です。約 900 行。作りました。この記録が決めていなかった細部（最初に見張るときの始まり、OAM が決める `notificationId`、印と止まるときの扱い、指標の名前）は、上の「通知」と「複数のインスタンス」にあります。

### 通知（複数のインスタンス）

リース、見張りの状態、在席の `internal/kvstore` の実装を作ります。確かめ方は、Valkey を使う CI の適合試験と、次のことの試験です。2 つのインスタンスのどちらかを止めても出来事を失わないこと。持ち主が替わったときに重なって届く通知は、同じ `notificationId` を持つこと。古いリースの持ち主は、見張りの状態の番号を戻せないこと。インスタンスを足すと、リースが取り分に分かれること。約 500 行。作りました。鍵の名前とインスタンスの名前は、上の「この形にした理由」の後ろにあります。

### e2e と手順書

Compose に OSS の代わり（通知を受けて残す小さなプログラム。「OSS の代わり」）を足し、HSS を止めて `notifyNewAlarm`、確認して `notifyAckStateChanged`、戻して `notifyClearedAlarm` が届くこと、一覧の `GET` の形を確かめます。pcap で、OAM と OSS のあいだの `GET`、`PATCH`、`POST` を確かめます。手順書（英語と日本語）に、OSS の代わりで警報を受け取る例、ラダー図、「簡略化したこと」を書き、用語集に足りない略語を足します。`internal/oam/doc.go` に経路を足します。約 600 行と文書。作りました。

- OSS の代わりは `cmd/oss` です。`oss serve` が通知を受けて `internal/mns` の型で確かめ、`oss subscribe`、`oss unsubscribe`、`oss alarms`、`oss ack` が OSS の要求を送ります。Compose では、プロファイル `oss` のサービス `oss`（172.28.0.66）です。
- e2e は `scripts/e2e-fm.sh` で、CI のジョブ `e2e-fm` が流します。成果物は pcap と、OSS と OAM のあいだのやり取りと HSS の DPR と CEA を並べた `e2e-out/fm-ladder.txt` です。流れは次のとおりです。購読を作って読み返す。MRFP のコンテナを `docker compose pause` で止め、`notifyPotentialFaultyAlarmList`、`unreliableAlarmScope`、戻したあとの `notifyAlarmListRebuilt` を確かめる。HSS を保守用の API で止め、5 つの役割の `notifyNewAlarm` を `href` と識別の属性で確かめる。filter を付けた `GET` で 5 つの記録を読み、通知の `alarmId` と記録の鍵が同じことを確かめる。S-CSCF の警報を確認し、`notifyAckStateChanged` を確かめる。HSS を戻し、5 つの `notifyClearedAlarm` と、解除と確認が済んだ記録が一覧から消えることを確かめる。購読を消し、そのあとの確認に通知が来ないことを確かめる。`oss serve` が断った通知が無いことも確かめる。pcap では、HTTP の各やり取りを順に、表示フィルタで確かめます（OAM と OSS はどちらもポート 8080 で待ち受けるので、Wireshark が HTTP と JSON を解読します）。どの待ちにも期限を付けています。HSS を止めてから警報が上がるまでと、戻してから解除されるまでに、それぞれ Tc（30 秒）ほどかかります。
- 手順書は `docs/guide/{en,ja}/oss.md` です。手順書の入口の読む順と、プロファイルの表に足しました。「簡略化したこと」の表のうち、この口の行は登録の手順書から移しました。HTTP の API の手順書（`apis.md`）の仕様に従う API の表に、経路と版を足しました。
- 用語集には NE、EM、NM、ProvMnS、REST を足しました。MnS、NRM、DN、Itf-N、OSS、Jex は前の作業で入っています。MOI は、手順書では `createMOI` のような操作の名前の中にだけ出て、コードとして書くので、足していません。

e2e を組むときに分かったことは次のとおりです。

- HSS より先に起動したノードは、最初の接続の試行で警報を上げ、CEA で解除します。その記録は確認されるまで一覧に残るので（TS 28.111 6.14）、HSS を止めてまた上がる警報は、同じ記録の変化として `notifyChangedAlarmGeneral` になります（6.12）。`notifyNewAlarm` を確かめるために、e2e は購読の前に、解除済みの記録をノードごとに確認しておきます。手順書も同じ手順を踏み、理由を書いています。
- 確認の `PATCH` への 204 と、その変化の `notifyAckStateChanged` は、別々の経路で OSS に届くので、順が決まりません。e2e は、どちらも `PATCH` のあとにあることだけを確かめます。
- 答えないノードの経路は、ノードを止めると試せません。止めて起動し直すと、ノードのリングバッファの識別が変わり、`notifyAlarmListRebuilt` の `reason` が `System restarts` になるためです。コンテナを一時停止すると、接続は開いたまま答えだけが無くなり、`System-NE communication error` の経路になります。

### 作業のあいだの順

| 作業 | 先に要る作業 |
| --- | --- |
| 保守用の API の出来事、3GPP の型、Jex の条件の式 | なし。互いに依らない |
| 警報の一覧の読み出しと確認 | 保守用の API の出来事（`ack_system_id` とまとめた確認）、3GPP の型、Jex の条件の式 |
| 購読 | 3GPP の型、Jex の条件の式 |
| 通知（1 つのインスタンス） | 保守用の API の出来事、3GPP の型、Jex の条件の式、警報の一覧の読み出しと確認（`unreliableAlarmScope` に見張りの状態を足すため）、購読 |
| 通知（複数のインスタンス） | 通知（1 つのインスタンス） |
| e2e と手順書 | 役割の警報、警報の一覧の読み出しと確認、購読、通知（1 つのインスタンス） |

## 製品の向きに関わる決定

製品の向きに関わる論点です。どれも、記録を書いたときに勧めた案のとおりに決めました。本文で論点の名前を括弧に入れたところは、この表の行を指します。

| 論点 | 決めたこと | 理由 |
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

- e2e が確かめるのは、Compose の 1 つのインスタンスの OAM だけです。複数のインスタンスでの見張りの分け方と引き継ぎは、Valkey を使う CI の適合試験と、`internal/kvstore/oamstore` の、いくつかの OAM のサーバーを 1 つの保存先で動かす試験だけで確かめています。
- 2 台以上が答えないときは、一覧全体を信頼できないと示します。OpenAPI の型が DN 1 つのためで、どのノードかは `notifyPotentialFaultyAlarmList` でしか分かりません（上の「答えないノード」）。
- 通知を OSS に送り直しません。OSS が止まっていたあいだの通知は失われます。OSS は、次に届く `notifyAlarmListRebuilt` を受けて、一覧を読み直す必要があります。
- OSS が自分の購読を忘れると、その id を知る方法がありません。購読の一覧の読み出しを作らないためです。
