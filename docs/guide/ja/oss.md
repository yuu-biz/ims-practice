# アラームを OSS に渡す

[English](../en/oss.md)

この網のノードは、それぞれ自分のアラームの一覧を持ち、OAM がそれを集めます（[HSS についてのアラームを読む](registration.md#hss-についてのアラームを読む)）。運用者の網の管理の仕組みである OSS は、ノードを 1 台ずつ読みません。網全体のアラームを OAM から読み、OAM で確認し、新しいアラームが上がるたびに知らせを受けます。この手順書では、そのインタフェースを追います。ここでの OSS は `oss` サービスの小さな代わりの仕組みです。OSS と OAM のあいだはすべて平文の HTTP なので、要求、応答、通知のどれもキャプチャで読めます。

この手順書は次の版に従います。TS 28.111 V20.0.0（故障の監視）、TS 28.532 V20.2.0（汎用のプロビジョニングの操作）、TS 28.622 V20.4.0（購読）、TS 32.158 V20.1.0（管理のサービスの REST の決まり）、TS 32.161 V19.0.0（Jex）、TS 32.101 V19.0.0 と TS 32.102 V19.0.0（管理の全体の構成）です。経路と属性の名前は、これらの仕様書の書庫にある OpenAPI のファイルのものです。`TS28111_FaultNrm.yaml` と `TS28111_FaultNotifications.yaml` の 19.3.0、`TS28532_ProvMnS.yaml` の 19.2.0、`TS28623_SubscriptionControlNrm.yaml` の 19.4.0、`TS28623_ComDefs.yaml` の 19.6.0 です。以下の条項番号はこれらの版のものです。

## 始める前に

- [UE が IMS に登録する流れを追う](registration.md) を、[HSS を運用から外す](registration.md#hss-を運用から外す) まで読んでおいてください。この手順書では、同じアラーム「Diameter peer down」を同じ方法で上げます。
- 略語の意味は [用語集](../../glossary.md) にあります。

網は `oss` のプロファイルを付けて起動します。172.28.0.66 に OSS の代わりが加わります。

```sh
docker compose --profile oss up -d --build
```

[最初のコマンド](README.md#最初のコマンド) にあるとおり、各ノードが HSS への Diameter の接続を開くまで待ちます。

## 要素管理システムと網管理システム

TS 32.101 5.1.1 は、網の管理のインタフェースを、何と何をつなぐかで分けています。種類 1 はネットワーク要素（NE）と、それを管理する要素管理システム（EM）のあいだ、種類 2 は要素管理システムと網管理システム（NM）のあいだです。種類 2 を Itf-N と呼び、3GPP が主に標準にしているのはこちらです。情報モデルをプロトコルから切り離して決め、1 つのモデルをいくつかのプロトコルで運べるようにしています（TS 32.102 7.3.2）。

| 層 | この網では | その下のインタフェース |
| --- | --- | --- |
| NM | OSS。ここでは `oss` サービス | Itf-N、種類 2。この手順書で扱う 3GPP の管理のサービス |
| EM | OAM。`oam` サービス | 種類 1。各ノードの保守用の API で、このプロジェクト独自のもの（TS 32.600 4.2.1.3 は製品に任せている） |
| NE | CSCF、HSS、アプリケーションサーバー、メディア中継ノード | |

管理のサービスの新しい枠組み（TS 28.533）では、OAM が故障の監視の MnS producer、OSS がその MnS consumer です。故障の監視には専用の経路がありません。アラームの一覧は網のリソースモデル（NRM）の 1 つのクラスで、OSS は、どの管理するオブジェクトでも読み書きできる汎用の操作で、それを読み書きします。TS 28.532 のプロビジョニングの MnS（ProvMnS）です（TS 28.111 6.2、6.6）。

## アラームの一覧とその URI

OAM は 1 つの SubNetwork `ims` を受け持ち、その下に `AlarmList` を 1 つだけ置きます。中身はすべてのノードのアラームの記録です。TS 28.111 6.11 は、まさにこの形を例に挙げています。管理する要素の 1 つとのつながりを失うことがある、subnetwork の管理システムです。一覧はいつもあり、OSS は作ることも消すこともできません（TS 28.111 7.3.2.1）。id は `1` です。

URI は `http://oam:8080/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1` で、3 つの部分からなります（TS 32.158 4.4.2）。

| 部分 | ここでは | 何か |
| --- | --- | --- |
| `{MnSRoot}` | `http://oam:8080/3GPPManagement` | OAM の待ち受けと、管理のサービスの根の経路。`deploy/compose/oam.yaml` の `mns_root` で決め、SubNetwork の名前は隣の `subnetwork` で決めます。2 つが無ければ、OAM はこのインタフェースを出しません。 |
| サービスと `{MnSVersion}` | `/ProvMnS/v19` | `v19` は、`v` と `TS28532_ProvMnS.yaml` の版 19.2.0 の最初の数です（TS 32.158 4.6.2）。 |
| オブジェクト | `/SubNetwork=ims/AlarmList=1` | 一覧の識別名（DN）`SubNetwork=ims,AlarmList=1` を経路の形で書いたもの。 |

OAM はこれを自分の API と同じ待ち受けで出します。この待ち受けはホストのどのアドレスにも無いので、以下のコマンドはどれも網のコンテナの中で動かします。OSS の代わりのコンテナもその 1 つです。`oss alarms`、`oss ack`、`oss subscribe`、`oss unsubscribe` は、この構成の既定の値で OSS の要求を送り、答えを OpenAPI の名前と厳しく突き合わせて読みます。

一覧を読みます。

```sh
docker compose exec oss oss alarms
```

```json
{
  "id": "1",
  "objectClass": "AlarmList",
  "objectInstance": "SubNetwork=ims,AlarmList=1",
  "attributes": {
    "operationalState": "ENABLED",
    "numOfAlarmRecords": 0,
    "lastModification": "2026-11-24T10:12:40Z",
    "alarmRecords": {}
  }
}
```

HSS が待ち受けを始める前に起動したノードは、最初に接続を試みたときの解除済みの記録を、すでに持っていることがあります。下の[アラームを上げて追う](#アラームを上げて追う)では、まずそれを確認します。

アラームの記録の鍵は、OAM のインベントリでのノードの名前、点、ノードが付けた id をつないだものです。たとえば `scscf.9f3c2a7b1d4e6058` です。OAM はこの鍵から、確認をどのノードに渡すかを、何も覚えずに決められます。記録の `objectInstance` も、ノードが自分を何と呼ぶかにかかわらず、`SubNetwork=ims,ManagedElement=` とこの名前で始まります。同じ役割の 2 台が設定のファイルを共有すると、ノードの名前は 1 つでも、インベントリの名前は 2 つになるためです。

## 通知を購読する

OSS は、SubNetwork の下に `NtfSubscriptionControl` を作って購読し（TS 28.622 4.3.22.1）、それを消して購読をやめます。作るのは `createMOI` の操作で、`PUT` で送ります（TS 28.532 12.1.1.1.2.1）。

```sh
docker compose exec oss oss subscribe
```

```http
PUT /3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1 HTTP/1.1
Host: oam:8080
Content-Type: application/json

{"id":"1","objectClass":"NtfSubscriptionControl","attributes":{"notificationRecipientAddress":"http://oss:8080/notifications"}}
```

OAM は `201 Created` で答えます。`Location` に購読の URI が、本文に購読が入り、`oss subscribe` がその本文を出力します。

```json
{
  "id": "1",
  "objectClass": "NtfSubscriptionControl",
  "objectInstance": "SubNetwork=ims,NtfSubscriptionControl=1",
  "attributes": {
    "notificationRecipientAddress": "http://oss:8080/notifications"
  }
}
```

| 属性 | ここでは | 意味 |
| --- | --- | --- |
| `notificationRecipientAddress` | `http://oss:8080/notifications` | OAM が通知を送る先。`oss` サービスが動かす `oss serve` が、ここで受けます。 |
| `notificationTypes` | 無し。つまり下の 6 つすべて | 送る通知の種類。`oss subscribe -types notifyNewAlarm,notifyClearedAlarm,notifyAlarmListRebuilt` で一部を選べます。`notifyAlarmListRebuilt` は外さないでください。OSS が変化を受け取り損ねたことを知らせる通知は、これだけです（[通知と、それぞれが来るとき](#通知の種類とそれぞれが来るとき)）。 |
| `notificationFilter` | 無し | 通知が満たすべき Jex の条件の式（TS 32.161 7.6）。たとえば `(contains(href,"ManagedElement=scscf") and perceivedSeverity="CRITICAL") or notificationType="notifyAlarmListRebuilt"` です。`oss subscribe -filter` で書きます。最後の条件は、一覧全体についての `notifyAlarmListRebuilt` を通すためのものです。この通知の `href` は SubNetwork を指し、`perceivedSeverity` を持ちません。 |
| `scope` | 無し | どのオブジェクトの通知を送るか。無ければ SubNetwork 全体です。 |

OAM が受けるのは、この 4 つの属性だけです。heartbeat の通知を求める `HeartbeatControl` を持つ購読は、403 と `MODIFICATION_NOT_ALLOWED`、`OBJECT_CREATION_NOT_ALLOWED` で断られます（TS 32.158 6.6.5.4）。OSS が `HeartbeatControl` を作ることに対応するかどうかは producer が選べ（TS 28.622 4.3.21.1）、OAM は対応しないからです。`VsDataContainer` を持つ購読と、`dataNodeSelector` で選ぶ `scope` を持つ購読は、500 と `SERVER_LIMITATION` で断られます。OAM は要求の意味を理解できても、それを実行できないからです（TS 32.158 6.6.4）。ファイルのスキーマに合わない本文は、400 と `NEW_OBJECT_REPRESENTATION_INVALID` で断られます（6.6.4、6.6.5.4）。JSON として読めない本文、ファイルに無い名前（`lastSequenceNo` など）や、ファイルの名前と大文字と小文字だけが違う名前（`heartbeatControl` など）を持つ本文がそうです。スキーマが許さないところに `null` を書いた本文も同じです。`null` を許すのは `id` と `vsData` だけです。`HeartbeatControl`、`VsDataContainer`、`dataNodeSelector` の形がスキーマに合わない本文（`id` の無い `HeartbeatControl` など）も、400 になります。1 つの本文に 400 の問題とほかの問題があれば、OSS が直せる 400 のほうを答えます。`MODIFICATION_NOT_ALLOWED` と `SERVER_LIMITATION` は、正しく書かれ、意味も分かる要求に当てる型だからです（6.6.4）。`HeartbeatControl` と、OAM が実行できない部分の両方を持つ本文には、より根本の理由である 403 を答えます（6.6.5.1）。

同じ id でもう一度 `PUT` すると購読を置き換え、答えは `200 OK` になります。購読は、要求を受けたインスタンスではなく OAM が持つので、ノードを見張るのがどの OAM のインスタンスでも、通知は届きます。読み返すのは `getMOIAttributes` で、同じ URI への `GET` です。

```sh
docker compose exec oss wget -qO- http://oam:8080/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1
```

## 通知の種類と、それぞれが来るとき

OAM は各ノードを見張ります。ノードの保守用の API に、アラームの一覧の変化を聞きます。この要求は、変化が無ければ 5 秒まで待ちます。OAM は変化の 1 つずつを通知にして、それを選ぶすべての購読に送り、OSS は `204 No Content` で答えます（TS 32.158 5.5.4）。ほかの答えは、OSS が通知を受け取らなかったことを表します。`200 OK` でも同じです。TS 32.158 5.5.4 と通知の OpenAPI のファイルが成功とするのは 204 だけだからです。転送（redirect）の答えも同じで、OAM は転送に従いません。301 や 302 に従うクライアントは、`POST` の代わりに `GET` を送ることがあり、その `GET` は通知を運ばないからです（RFC 9110 15.4.2、15.4.3）。307 と 308 は `POST` のまま送り直させます（15.4.8、15.4.9）。それでも従わないのは、購読が名指ししていない宛先に通知を送ることになるからです。OAM は、OSS が購読するときに渡した宛先に通知を送ります（TS 32.158 5.5.2、5.5.4）。宛先を移した OSS は、新しい宛先を書いた `PUT` で購読を置き換えます。

| 通知（TS 28.111） | 来るとき |
| --- | --- |
| `notifyNewAlarm`（8.2） | 一覧に記録が加わった。ノードが、持っていなかったアラームを上げた。 |
| `notifyChangedAlarmGeneral`（8.4） | 記録の重さ（`CLEARED` 以外の値へ）か、追加の文が変わった。解除済みの記録が一覧に残っているあいだに同じアラームがまた上がると、その記録が変わるので、新しいアラームではなくこの通知になる（6.12、6.14）。 |
| `notifyClearedAlarm`（8.3） | 記録の重さが `CLEARED` になった。アラームを上げたノードが、その状態が無くなったと判断した。 |
| `notifyAckStateChanged`（8.8） | 記録が確認された。または、確認が取り消された。 |
| `notifyPotentialFaultyAlarmList`（8.10） | ノードが OAM に答えなくなり、一覧のそのノードの部分が正しくないかもしれない。 |
| `notifyAlarmListRebuilt`（8.5） | その部分をまた信頼できるようになったが、変化を見落としたかもしれない。読み直してほしい。 |

記録が一覧から消えたことを知らせる通知はありません。OSS は、ある記録について、解除と確認の 2 つの通知を受けたことから、それを知ります。2 つの順はどちらでも構いません（TS 28.111 6.12）。

どの通知も、同じ見出しで始まります（TS 28.532 11.0.2）。

| 属性 | 例 | 何か |
| --- | --- | --- |
| `href` | `http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org` | 通知の対象のオブジェクトを canonical URI で書いたもの。OAM のホストのあとに、DN を経路の形で続けます（TS 32.158 4.2.4）。 |
| `notificationId` | `4711093825561044` | 変わったあとの記録の `notificationId` と同じ数。OSS は通知と記録を結び付けられ、同じ通知が 2 回届いたのか、2 つの変化なのかを見分けられます。 |
| `notificationType` | `notifyNewAlarm` | 6 つのどれか。 |
| `eventTime` | `2026-11-24T10:15:02Z` | ノードで変化が起きた時刻。 |
| `systemDN` | `SubNetwork=ims` | OAM が管理する範囲の根のオブジェクトの DN。 |

`oss serve` は、通知を 1 つずつ OpenAPI の名前と突き合わせ、受けたものを持っておき、読み出せるようにします。

```sh
docker compose exec oss wget -qO- http://127.0.0.1:8080/received
docker compose exec oss wget -qO- 'http://127.0.0.1:8080/received?notification_type=notifyNewAlarm'
```

断った本文は、理由とともに `/refused` にあります。

## アラームを上げて追う

1. 起動のときから残っている、HSS についての解除済みの記録を確認しておきます。次の手順のアラームが、新しいアラームとして届くようにするためです。記録は filter で選んで読みます。filter は、TS 28.111 A.1.4 の形で記録を選ぶ Jex の条件の式です。

   ```sh
   docker compose exec oss oss alarms -filter \
     '/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="CLEARED"]'
   ```

   見つかった記録は、手順 4 と同じく、`oss ack` に id を付けて確認します。1 つの要求で確認できるのは 1 台のノードの記録だけです。記録が無ければ、何もしなくて構いません。

2. [HSS を運用から外す](registration.md#hss-を運用から外す) と同じ要求で、HSS を停止に向かわせます。

   ```sh
   docker compose exec hss sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/hss/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   30 秒ほどあと、DPR から Tc がたったところで、HSS に接続する 5 つのノードが接続し直そうとして断られ、それぞれアラームを上げます。1 秒もしないうちに、OSS には各ノードの `notifyNewAlarm` が届きます。

   ```json
   {"href":"http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
    "notificationId":4711093825561044,"notificationType":"notifyNewAlarm","eventTime":"2026-11-24T10:15:02Z","systemDN":"SubNetwork=ims",
    "alarmId":"scscf.9f3c2a7b1d4e6058","alarmType":"COMMUNICATIONS_ALARM","probableCause":"COMMUNICATIONS_SUBSYSTEM_FAILURE",
    "specificProblem":"Diameter peer down","perceivedSeverity":"MAJOR","additionalText":"no transport connection to the peer"}
   ```

   このアラームのあとの通知も、同じ `alarmId` を持ちます。通知の中でアラームを示すのは、この id です（TS 28.111 6.12）。この通知は、その id と、アラームを識別する 4 つの属性を結び付けます（6.14）。`href` が示すオブジェクト、`alarmType`、`probableCause`、`specificProblem` です。

3. HSS についてのアラームを読みます。

   ```sh
   docker compose exec oss oss alarms -filter \
     '/AlarmList[id="1"]/attributes/alarmRecords/*[specificProblem="Diameter peer down" and perceivedSeverity="MAJOR"]'
   ```

   filter は `GET` の問い合わせに、percent-encoding して入ります（`GET /3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1?filter=%2FAlarmList%5Bid%3D%221%22%5D...`）。答えには 5 つの記録が、それぞれ `alarmId` を鍵にして入ります。

   ```json
   "scscf.9f3c2a7b1d4e6058": {
     "objectInstance": "SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
     "notificationId": 4711093825561044,
     "alarmRaisedTime": "2026-11-24T10:15:02Z",
     "alarmType": "COMMUNICATIONS_ALARM",
     "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
     "specificProblem": "Diameter peer down",
     "perceivedSeverity": "MAJOR",
     "additionalText": "no transport connection to the peer",
     "ackState": "UNACKNOWLEDGED"
   }
   ```

   `numOfAlarmRecords` は、filter をかける前の、一覧のすべての記録の数です。filter が何も選ばなくても誤りではなく、記録の無い一覧が返ります。

4. 通知にあった id で、S-CSCF のアラームを確認します。

   ```sh
   docker compose exec oss oss ack scscf.9f3c2a7b1d4e6058
   ```

   確認は `modifyMOIAttributes` の操作で、JSON Merge Patch（RFC 7396、TS 28.532 12.1.1.1.4）で送ります。TS 28.111 A.1.4 が挙げる 3 つの形の 1 つです。patch が書くのは、OSS が変えてよい 3 つの属性だけです（TS 28.111 7.3.1.2）。`ackState`、誰が確認したかの `ackUserId`、どのシステムから確認したかの `ackSystemId` です。

   ```http
   PATCH /3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1 HTTP/1.1
   Host: oam:8080
   Content-Type: application/merge-patch+json

   {"id":"1","attributes":{"alarmRecords":{"scscf.9f3c2a7b1d4e6058":{"ackState":"ACKNOWLEDGED","ackUserId":"noc-operator","ackSystemId":"oss-1"}}}}
   ```

   OAM は確認を S-CSCF に渡し、S-CSCF が記録を変えると、`204 No Content` で答えます。記録の変化はほかの変化と同じなので、OSS には `notifyAckStateChanged` も届き、`ackState`、`ackUserId`、`ackSystemId` が入っています。答えと通知は別々に運ばれるので、どちらが先に届くかは決まっていません。アラームは `MAJOR` のままです。確認は、誰かが対応していることを示すもので、障害が終わったことを示すものではありません。

   `oss ack -unacknowledge` で確認を取り消せます。2 台のノードの記録を並べた patch は、どのノードにも聞く前に 500 と `SERVER_LIMITATION` で断られます。一覧に無い id は 400 と `IE_NOT_FOUND` です。誤りの本文には、TS 32.158 6.6 の `type` と `reason` が入ります。

   確認の状態を変えるときは、いつも誰が変えたかを名乗ります。`ackUserId` の無い patch は、400 と `NEW_ATTRIBUTE_VALUE_INVALID` で断られます。TS 28.111 6.7 は、OSS が名乗らずに確認することも許し、8.8.2 は、記録に `ackUserId` があるときに `notifyAckStateChanged` へ入れるとしています。それでもこのプロジェクトが名前を求めるのは、`TS28111_FaultNrm.yaml` の `NotifyAckStateChanged` のスキーマが `ackUserId` を必須にしているからです。どの変更にも名前があれば、OSS に届く `notifyAckStateChanged` は、どれもこのスキーマに合います。OAM の画面は OAM 独自の API で確認し、同じように運用者の名前を求めます。OAM はその確認を、運用者の名前を `ackUserId` に、自分の名前を `ackSystemId` に入れてノードに渡します。自分の名前は設定のファイルの `node` で、ここでは `oam` です。OSS には同じ `notifyAckStateChanged` が届きます。

5. 同じ要求に `UNLOCKED` を入れて、HSS の lock を解きます。各ノードは、遅くとも Tc 後の次の試行で接続し直し、CEA を受けてアラームを解除します。OSS には各ノードの `notifyClearedAlarm` が届きます。S-CSCF の記録は、解除されて確認も済んだので、一覧から消えます（TS 28.111 6.14）。OSS は、それを 2 つの通知から知ります。ほかの 4 つは、誰かが確認するまで、`CLEARED` かつ `UNACKNOWLEDGED` のまま残ります。

6. 購読をやめます。

   ```sh
   docker compose exec oss oss unsubscribe
   ```

   これは `deleteMOI` で、`DELETE /3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1` を送り、`204 No Content` が返ります（TS 28.532 12.1.1.1.5）。OAM はもうその宛先に何も送りません。ここで解除済みの 4 つの記録を確認しても、`notifyAckStateChanged` は届きません。

```text
OSS                                OAM                       S-CSCF                  HSS
 |  PUT NtfSubscriptionControl=1    |                           |                     |
 |--------------------------------->|                           |                     |
 |  201 Created                     |                           |                     |
 |<---------------------------------|                           |                     |
 |                                  |  GET alarm-events, waits  |                     |
 |                                  |-------------------------->|                     |
 |                                  |                           |  DPR, DPA           |
 |                                  |                           |<------------------->|
 |                                  |                           |  SYN, refused (Tc)  |
 |                                  |                           |-------------------->|
 |                                  |  200, the alarm was added |                     |
 |                                  |<--------------------------|                     |
 |  POST notifyNewAlarm             |                           |                     |
 |<---------------------------------|                           |                     |
 |  204                             |                           |                     |
 |--------------------------------->|                           |                     |
 |  GET AlarmList=1?filter=         |                           |                     |
 |--------------------------------->|                           |                     |
 |                                  |  GET alarms, every node   |                     |
 |                                  |-------------------------->|                     |
 |                                  |  200                      |                     |
 |                                  |<--------------------------|                     |
 |  200 AlarmList                   |                           |                     |
 |<---------------------------------|                           |                     |
 |  PATCH AlarmList=1               |                           |                     |
 |--------------------------------->|                           |                     |
 |                                  |  PATCH alarms             |                     |
 |                                  |-------------------------->|                     |
 |                                  |  200                      |                     |
 |                                  |<--------------------------|                     |
 |  204                             |                           |                     |
 |<---------------------------------|                           |                     |
 |  POST notifyAckStateChanged      |                           |                     |
 |<---------------------------------|                           |                     |
 |  204                             |                           |                     |
 |--------------------------------->|                           |                     |
 |                                  |                           |  CER, CEA (unlock)  |
 |                                  |                           |<------------------->|
 |  POST notifyClearedAlarm         |                           |                     |
 |<---------------------------------|                           |                     |
 |  204                             |                           |                     |
 |--------------------------------->|                           |                     |
 |  DELETE NtfSubscriptionControl=1 |                           |                     |
 |--------------------------------->|                           |                     |
 |  204                             |                           |                     |
 |<---------------------------------|                           |                     |
```

- ラダー図には S-CSCF だけを描きました。I-CSCF、IP-SM-GW、MMTel AS、Service Centre も同じように動き、それぞれのアラームに通知が届きます。
- OAM と S-CSCF のあいだは、ポート 8085 の保守用の API です。Wireshark はこれを自分では HTTP として解読しません。OSS と OAM はどちらもポート 8080 で待ち受けるので、HTTP と JSON の本文まで解読します。`http && ip.addr == 172.28.0.66` でやり取りの全体が、`json.member_with_value == "notificationType:notifyNewAlarm"` で新しいアラームの通知が見えます。
- 一覧の `GET` には、インベントリのすべてのノードに一覧を聞いてから答えます。確認は、鍵が示す 1 台のノードにだけ渡します。

## ノードが答えないとき: unreliableAlarmScope

OAM は、ノードのアラームの写しを持ちません。ノードが答えなければ、OAM にはそのノードがどのアラームを持つかがわからないので、一覧のその部分は信頼できないと示します（TS 28.111 6.11）。MRFP のコンテナを一時停止して、プロセスを終わらせずに答えないようにします。

```sh
docker compose pause mrfp
```

MRFP の変化を聞く OAM の要求は、10 秒ほどのうちに時間切れになり、OSS に `notifyPotentialFaultyAlarmList` が届きます。

```json
{"href":"http://oam:8080/SubNetwork=ims/ManagedElement=mrfp","notificationId":2113906541120775,
 "notificationType":"notifyPotentialFaultyAlarmList","eventTime":"2026-11-24T10:20:31Z","systemDN":"SubNetwork=ims",
 "reason":"serviceprovider-NE communication error"}
```

`href` は、正しくないかもしれない部分の基点です。ここではノード 1 台の全体です。ノードに届かないあいだは、一覧も同じことを示します。

```sh
docker compose exec oss oss alarms
```

```json
"attributes": {
  "operationalState": "ENABLED",
  "numOfAlarmRecords": 0,
  "unreliableAlarmScope": "SubNetwork=ims,ManagedElement=mrfp",
  "alarmRecords": {}
}
```

答えには MRFP の記録がまったく入りません。記録は「無い」のではなく「わからない」のです。MRFP の一時停止を解きます。

```sh
docker compose unpause mrfp
```

MRFP が次に答えると、OSS に同じ `href` の `notifyAlarmListRebuilt` が届きます。`reason` は `System-NE communication error`、`alarmListAlignmentRequirement` は `ALIGNMENT_REQUIRED` で、一覧から `unreliableAlarmScope` が消えます。`ALIGNMENT_REQUIRED` は、その部分の変化に通知していないものがあることを OSS に伝えます（TS 28.111 8.5.2）。OSS は、`contains(objectInstance,"ManagedElement=mrfp")` の `filter` で、その部分を読み直します。

OAM が `ALIGNMENT_REQUIRED` の `notifyAlarmListRebuilt` を送るのは、ほかに 3 つの場合です。どれも、通知していない変化があるためです。

| 場合 | `href` | `reason` |
| --- | --- | --- |
| ノードが起動し直し、アラームの一覧が新しくなった | そのノード | `System restarts` |
| OAM が読む前に、ノードの変化がジャーナルから消えた。または、いっぱいになった一覧が記録を捨てた | そのノード | `indeterminate` |
| 通知がこの OSS に届かなかった。接続できない、5 秒のうちに答えが無い、204 以外が返った | SubNetwork 全体、`http://oam:8080/SubNetwork=ims`。その購読にだけ送る | `indeterminate` |

OAM は通知を送り直しません。届かなかった通知のあと、その OSS にはこの `notifyAlarmListRebuilt` のほかは何も送りません。OAM は、アラームが変わるかどうかに関わらず、OSS が受け取るまで 30 秒ごとにこの通知を送ってみます。受け取ったら、また通知を送るようになり、OSS は一覧を読み直します。ただし、この通知を送っているあいだにアラームが変わったときは、OSS がその変化の前に一覧を読んだかもしれないので、次の回にもう一度送ります。答えない OSS が OAM に課す待ちは 1 回の 5 秒だけで、通知ごとには待たず、ほかの OSS も止めません。

`notificationTypes` か `notificationFilter` で `notifyAlarmListRebuilt` を選ばない購読は、この通知を送らないよう求めたことになります。通知が届かなかったあと、その購読には、次に送ってみるときまで、最長で 30 秒、何も送りません。そのとき OAM は、OSS に知らせずに、届かなかったという記録を消し、また通知を送るようになります。OSS には通知を受け取り損ねた手がかりが無いので、一覧を読み直すべきことが分かりません。

2 台以上のノードが答えないとき、`unreliableAlarmScope` は SubNetwork 全体の `SubNetwork=ims` になります。そのあいだも、通知はノードごとに 2 つずつ届きます。

## OAM を複数のインスタンスで動かすとき

この手順書のデプロイでは、OAM は 1 つです。`oam` の役割は、設定に 1 つの Valkey サーバーを指す `store` の節があれば、複数のインスタンスを並べて動かせます。購読はそこに置き、ノードの見張りはインスタンスのあいだで分けます。1 つのノードを見張るのは、どの時点でも 1 つのインスタンスです。見張りは 15 秒のリースで、見張るインスタンスが 5 秒ごとに延ばします。各インスタンスは、5 秒ごとに、動いていることを示す在席を同じ保存先に書きます。

あるインスタンスと 1 台のノードのあいだだけが切れ、ほかのインスタンスはそのノードに届くことがあります。そのインスタンスの見張りは、ノードが最初の要求に答えなかったところで、前の節と同じく `notifyPotentialFaultyAlarmList` を送ります。3 回続けて答えなければ、そのインスタンスは、そのノードに届かないことを在席に書き、同じことを書いていないインスタンスがほかにあれば、見張りを手放します。ほかのインスタンスがリースを取り、最後に送った出来事より後の出来事をノードに聞いて、答えを受けます。引き取ったインスタンスが `System-NE communication error` の `notifyAlarmListRebuilt` を送り、一覧から `unreliableAlarmScope` が消えます。ノードそのものは止まっていません。OSS は、一覧のその部分を読み直します。見張りを手放したインスタンスは、5 秒ほどごとに、見張りと同じ要求でノードに聞き直し、答えがあれば、届かないと書くのをやめます。

```text
OSS                        oam-1                       oam-2                      Valkey              S-CSCF
 |                           |  GET alarm-events: the link is broken                 |                   |
 |                           |-------------------------------------------------------------------------x |
 |  POST notifyPotentialFaultyAlarmList                  |                           |                   |
 |<--------------------------|                           |                           |                   |
 |  204                      |                           |                           |                   |
 |-------------------------->|                           |                           |                   |
 |                           |  GET alarm-events, twice more                         |                   |
 |                           |-------------------------------------------------------------------------x |
 |                           |  presence: cannot reach scscf                         |                   |
 |                           |------------------------------------------------------>|                   |
 |                           |  release the lease of scscf                           |                   |
 |                           |------------------------------------------------------>|                   |
 |                           |                           |  take the lease of scscf  |                   |
 |                           |                           |-------------------------->|                   |
 |                           |                           |  GET alarm-events, waits  |                   |
 |                           |                           |---------------------------------------------->|
 |                           |                           |  200                      |                   |
 |                           |                           |<----------------------------------------------|
 |  POST notifyAlarmListRebuilt                          |                           |                   |
 |<------------------------------------------------------|                           |                   |
 |  204                      |                           |                           |                   |
 |------------------------------------------------------>|                           |                   |
 |                           |  GET alarm-events, again every 5 s or so              |                   |
 |                           |-------------------------------------------------------------------------x |
 |                           |                           |  GET alarm-events, waits  |                   |
 |                           |                           |---------------------------------------------->|
```

- どのインスタンスもノードに届かないとき（ノードそのものが止まっているときなど）は、すべてのインスタンスが届かないと書き、見張りはいま見張っているインスタンスに残ります。見張りがインスタンスのあいだを回ることはありません。OSS に届くのは、`notifyPotentialFaultyAlarmList` が 1 つと、ノードが戻ったときの `notifyAlarmListRebuilt` が 1 つです。
- OSS に見えるのは前の節の 2 つの通知で、下の表のフィルタで見つかります。出来事を聞く要求は、ポート 8085 の保守用の API です。Wireshark はこれを自分では HTTP として解読しません。
- この手順書のデプロイは OAM を 1 つだけ動かすので、このやり取りは起きません。OAM のインスタンスが共有する保存先の試験 `TestAnInstanceThatCannotReachANodeHandsItsWatchOver` と `TestTheWatchOfANodeThatNoInstanceReachesStays` が、1 つの保存先で 2 つの OAM のサーバーを動かし、このやり取りを確かめます。`KVSTORE_VALKEY_ADDR` が Valkey のサーバーを指していれば、Valkey でも確かめます。

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `http && ip.addr == 172.28.0.66` | OSS と OAM のあいだのやり取りのすべて。 |
| `http.request.method == "PUT" && http.request.uri contains "NtfSubscriptionControl="` | 購読。答えに `Location` が入る。 |
| `http.request.method == "DELETE" && http.request.uri contains "NtfSubscriptionControl="` | 購読の終わり。 |
| `http.request.uri contains "AlarmList=1?filter="` | filter を付けた一覧の読み出し。 |
| `json.key == "unreliableAlarmScope"` | 一覧に信頼できない部分があることを示す答え。 |
| `http.request.method == "PATCH" && http.content_type == "application/merge-patch+json"` | JSON Merge Patch で送る確認。 |
| `http.request.uri == "/notifications"` | OAM が OSS に送る通知。 |
| `json.member_with_value == "notificationType:notifyNewAlarm"` | 新しいアラーム。 |
| `json.member_with_value == "notificationType:notifyAckStateChanged"` | 確認の通知。 |
| `json.member_with_value == "notificationType:notifyClearedAlarm"` | 解除されたアラーム。 |
| `json.member_with_value == "notificationType:notifyPotentialFaultyAlarmList"` | 答えなくなったノード。 |
| `json.member_with_value == "notificationType:notifyAlarmListRebuilt"` | 読み直してほしい一覧の部分。 |

`scripts/e2e-fm.sh` は、この手順書の手順を実行し、やり取りを 1 つずつキャプチャで確かめ、この表のフィルタをすべてそのキャプチャに当てます。フレームが 1 つも見つからないフィルタがあれば、試験は失敗します。キャプチャは `captures/` に、やり取りのフレームは `e2e-out/fm-ladder.txt` に残ります。

## このプロジェクトで簡略化していること

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| OAM。TS 28.111 6.11、6.12 | OAM が OSS に見せるアラームの一覧の `unreliableAlarmScope` は、答えないノードが 1 台ならそのノードを、2 台以上なら `SubNetwork` 全体を示す。答えないノードを並べることはない。 | アラームの一覧の OpenAPI のファイルは、この属性を DN 1 つの型にしている。TS 28.111 7.4.1 では複数の値を持てるが、一覧全体も、6.11 がこの属性で示してよいとする範囲の 1 つであるため。 |
| OAM。TS 32.158 6.1.2、6.2.2 | そのアラームの一覧の読み出しは、`attributes` と、`BASE_ONLY` 以外の scope を受けない。`fields` で選べるのはアラームの記録だけ。それ以外には 500 と `QUERY_PARAMS_TOO_COMPLEX` を返す。 | アラームの一覧の下にはオブジェクトが無いので、どの scope も一覧そのものより先には届かない。また、OSS が一覧から読むのはアラームの記録であるため。 |
| OAM。TS 32.158 6.3.1 | 2 台以上のノードのアラームの記録を確認する patch は、500 と `SERVER_LIMITATION` で断る。本文をすべて確かめたあと、どのノードにも聞く前に断るので、どの一覧も変わらない。 | patch は、全体を当てるか、何も当てないかのどちらかでなければならない。ノードをまたいでそうするには、ノードのあいだで 2 相の確定が要る。OSS は、代わりにノードごとに patch を送るため。 |
| OAM。TS 28.111 A.1.4 | アラームを確認する patch の 3 つの形のうち、JSON Merge Patch だけを受ける。JSON Patch と 3GPP の 2 つの形には 415 を返す。 | 3GPP の形の media type の綴りが、TS 28.532 と TS 32.158 で違う。また、確認には Merge Patch で足りるため。 |
| OAM。TS 28.622 4.3.22.1 | `notificationTypes` の無い OSS の購読に送るのは、アラームの 6 つの通知だけ。OAM は TS 28.532 の構成の通知を送らないので、購読を作ったことも消したことも通知しない。購読の範囲は `SubNetwork` 全体。 | 故障の監視に要るのはアラームの通知だけで、一部のノードのアラームだけが欲しい OSS は `notificationFilter` で選べるため。 |
| OAM。TS 28.111 7.3.2.1、8.5 | ノードのアラームの一覧がいっぱいで記録を捨てたことを、そのノードを基点にした `notifyAlarmListRebuilt`（`ALIGNMENT_REQUIRED`、`indeterminate`）で OSS に知らせる。一覧を作り直してはいない。 | TS 28.111 は、いっぱいの一覧が記録を捨てることを許しているが、それを知らせる通知を持たない。この通知は、アラームの変化の一部を知らせられなかったことを表すので、OSS はノードがもう持たない記録を残さず、その部分の一覧を読み直せるため。 |
| OAM。TS 28.111 8.5.1、8.5.2 | 通知を受け取らなかった宛先には、一覧全体を基点にした `notifyAlarmListRebuilt`（`ALIGNMENT_REQUIRED`、`indeterminate`）を受け取るまで、何も送らない。この通知はその購読だけに送る。受け取らなかった通知は送り直さない。 | TS 28.111 は、一覧を作り直したときに、この通知をすべての購読者に送る。受け取らなかった通知を送り直すには、TS 28.532 の確実に届ける仕組みが要る。代わりに OSS が一覧を読み直す。また、答えない OSS が、ほかの OSS もノードの見張りも止めないため。 |

## 後片付け

```sh
docker compose --profile ue --profile phones --profile volte --profile oss down -v --remove-orphans
```

ノードと OSS の代わりを止め、イメージは残すので、次の手順書は新しくビルドせずに始められます。`-v` で OAM のボリュームも消えるので、そこにある購読も消えます。
