# HSS のサービスベースのインタフェース

[English](README.md)

このパッケージは、HSS のサービスベースのインタフェース（TS 29.562）のうち、IMS の部分です。Nhss_imsUEContextManagement（UECM）、Nhss_imsUEAuthentication（UEAU）、Nhss_imsSubscriberDataManagement（SDM）があります。I-CSCF と S-CSCF は Cx の代わりに N70 で、IP-SM-GW などの AS は Sh の代わりに N71 で、これらを使います（TS 23.228 AA.1.2）。このプロジェクトが使う Cx と Sh の手順が、ここのどの操作に当たるかは、TS 23.228 Table AA.2.2.2-1 と Table AA.2.2.3-1 に沿って、パッケージのコメントにまとめてあります。

このプロジェクトのノードは、このパッケージを動かしていません。`cmd` のどのバイナリにも組み込まれていません。両側が動く様子は試験で見られます。

```sh
go test ./internal/nhss
go doc ./internal/nhss
```

## このパッケージで簡略化していること

どの行の場所にも、コードに `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| パッケージ `nhss`、TS 29.562 Annex A | パッケージのコメントの一覧に無い操作（復旧、IMS の AS の登録、データの変化の購読（PPR/PPA に当たるもの）、SDM のほかのリソースなど）と、一覧の操作の属性のうち、その手順が使わないもの。 | このプロジェクトが使う Cx と Sh の手順は、一覧の操作だけで足ります。知らない属性は無視して読むので、もっと多くを持つ相手ともやり取りできます。 |
| `ScscfRegistration`、TS 29.562 6.1.6.2.10 | 複数の登録、復旧、選び直し、ワイルドカード、ルーズルートの属性。`scscfInstanceId` は、表では必須で、OpenAPI のファイルでは必須ではありません。呼ぶ側が持っているときだけ送ります。 | NRF が無いので、S-CSCF には NF のインスタンスの ID がありません。 |
| `ImsProfileData`、TS 29.562 6.2.6.2.4 | 課金の情報、サービスレベルのトレース、優先度、同時に登録できる数の上限。 | このプロジェクトの S-CSCF は、どれも使いません。 |
| `Ifcs`、TS 29.562 6.2.6.2.14 | 共有の iFC の組を指す `cscfFilterSetIdList`。 | HSS のスタブは、条件をすべて書いて送ります。 |
| `IpSmGwAddress`、TS 29.562 6.2.6.2.44 | S6c のための IP-SM-GW の Diameter の識別子とレルム、SBI に対応していることの表示、NF のインスタンスの ID。 | IP-SM-GW が Sh で書く SMS の登録には、IP-SM-GW の番号しかありません。 |
| `Client.GetProfileData`、TS 29.562 6.2.3.5.3.1 | クエリのパラメータ `dataset-names` と `supported-features`。プロファイルの全体が返ります。 | この操作に当たる SAA も、ユーザプロファイルの全体を返します。 |
| `Client.GetMsisdns`、TS 29.562 6.2.3.3.3.1 | 複数の秘密の識別子のうち 1 つの C-MSISDN を求める、クエリのパラメータ `private-id`。 | この操作に当たる Sh の読み出しは、秘密の識別子を運びません。IP-SM-GW が送らないためです。 |
| `SipAuthenticationInfoResult`、TS 29.562 6.3.6.2.3 | NBA と GIBA のための `lineIdentifierList` と `ipAddress`。 | このプロジェクトの認証は、IMS-AKA と SIP Digest だけです。 |
