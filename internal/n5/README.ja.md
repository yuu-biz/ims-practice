# N5: Npcf_PolicyAuthorization

[English](README.md)

このパッケージは、TS 29.514 の Npcf_PolicyAuthorization のサービスです。P-CSCF などの AF と、5GC の PCF のあいだの参照点 N5 にあたります。5GS で Rx（パッケージ `rx`）に当たるものです。AF は、IMS のセッションのサービスの情報を入れて Individual Application Session Context を作り、SDP が変わると更新し、セッションが終わると削除します。PCF は、イベントを AF に知らせ、有効でなくなったコンテキストを終えるよう AF に求めます（TS 29.514 4.2.1）。JSON の型は、TS 29.514 Annex A.2 の OpenAPI のファイルの属性の名前を使います。キャプチャの `ascReqData` は、ここでは `AscReqData` です。

このプロジェクトのノードは、このパッケージを動かしていません。`cmd` のどのバイナリにも組み込まれていません。5GC のスタブ（パッケージ `fivegc`）の PCF が、このサービスを提供します。両側が動く様子は試験で見られます。

```sh
go test ./internal/n5
go doc ./internal/n5
```

## このパッケージで簡略化していること

どの行の場所にも、コードに `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| パッケージ `n5`、TS 29.514 Annex A.2 | Annex B の P-CSCF の手順も、4.2.6.7 の信号の経路の状態も使わない属性（トラフィックの経路の制御、TSC、QoS の監視、スポンサー付きのデータ、MPS、MCPTT など）と、パッケージのコメントの一覧で作っていないとした操作。 | P-CSCF は、コンテキストを作り、更新し、削除し、そのイベントの知らせを受けるだけで、ほかには何も使いません。SBI の受け手は、拡張できる API のために、知らない属性を無視しなければなりません。このパッケージも無視して読むので、もっと多くを持つ相手ともやり取りできます。 |
| `Client`、TS 29.513 8.4 | UE の PDU セッションを受け持つ PCF を見つける BSF。クライアントは、渡された 1 つの PCF に送ります。 | 5GC のスタブには、PCF が 1 つしかありません。 |
| `NewHandler`、TS 29.514 5.3 | コンテキストの GET、Events Subscription のサブリソースの PUT と DELETE（Subscribe と Unsubscribe の操作）、独自の操作 PcscfRestoration。これらには 404 か 405 で答えます。 | P-CSCF は、Create と Update の中でイベントを購読します。P-CSCF の復旧は、このプロジェクトにありません。 |
| `AppSessionContextReqData`、TS 29.514 5.6.2.3 | Ethernet の PDU セッションのための `ueMac`。 | IMS は IP の PDU セッションの上で動きます。 |
| `AppSessionContextRespData`、TS 29.514 5.6.2.4 | `servAuthInfo`、`directNotifReports`、`ueIds`。 | バックグラウンドのデータ転送、QoS の監視、5GS の UE の識別子の公開のためのものです。ここでの IMS は、どれも使いません。 |
| 「Rm」の型、TS 29.514 5.6.2.25 から 5.6.2.27 | 「Rm」の型は、元の型と同じ Go の型です。そのため PATCH で属性を null にできません。省いた属性は、前の値のままです。 | JSON Merge Patch は null で属性を消します。P-CSCF は属性を消しません。 |
| `EventsSubscReqData`、TS 29.514 5.6.2.6 | 使用量、QoS の監視、アクセス網の報告のしきい値とパラメータ。 | このパッケージが名前を付けたイベントは、IMS の手順が使うリソースの割り当てのものだけです。どれも、これらを使いません。 |
| `EventsNotification`、TS 29.514 5.6.2.9 | アクセス網、位置、課金、QoS の監視、使用量の報告。 | このパッケージは、名前を付けたイベントに当たる、リソースの割り当ての報告だけを持ちます。 |
| `AfEventSubscription`、TS 29.514 5.6.2.10 | `notifMethod`、`repPeriod`、`waitTime`。 | `notifMethod` が無ければ、PCF はイベントを見つけたときに報告します。IMS に要るのは、その報告です。 |
