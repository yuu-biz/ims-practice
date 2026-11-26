# SGd と S6c

[English](README.md)

このパッケージは、TS 29.338 の Diameter のメッセージを組み立てて読みます。短いメッセージを、SC と、UE に仕える網のノードのあいだで運ぶメッセージです。SGd では MO-Forward-Short-Message（OFR/OFA）と MT-Forward-Short-Message（TFR/TFA）、S6c では Send-Routing-Info-for-SM（SRR/SRA）、Report-SM-Delivery-Status（RDR/RDA）、Alert-Service-Centre（ALR/ALA）です。S6c では、このプロジェクトが独自に決めた Ready-For-SM も運びます。IP-SM-GW が、利用者がまたショートメッセージを受け取れるようになったことを HSS に知らせるコマンドです。実際の網では、この知らせを MAP-READY-FOR-SM（TS 29.002 12.4.1）で送ります。このプロジェクトは MAP を作っていません。

パッケージ `smsc` の SC は、このパッケージの構造体を使って動きます。このプロジェクトのノードは、どちらも動かしていません。試験と例は、メッセージごとに組み立てて、読み直します。

```sh
go test ./internal/sgd
go doc ./internal/sgd
```

## このパッケージで簡略化していること

どの行の場所にも、コードに `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| パッケージ `sgd`、TS 29.338 5.3.2、6.3.2 | どの構造体にも無い任意の AVP。SGd では、優先のサービス、MSISDN の無い SMS、UE のセル、SGSN、マシン型の通信、メッセージを預かるか SC に送り直しを求めるノードのための AVP です。S6c では、優先のサービス、MSISDN の無い利用者、RP のメッセージの種類と発信元の SME のアドレス、2 つ目のノード、3GPP でないアクセス、VLR、HSS が利用者を不在と覚えた理由のための AVP です。AVP の名前は、パッケージのコメントにあります。 | このパッケージを使うノードは、どの AVP も送らず、受けても使いません。 |
| `AlertServiceCentreRequest`、TS 29.338 5.3.2.5、6.2.3 | HSS のほかのノードからの通知と、AVP の SMSMI-Correlation-ID、Maximum-UE-Availability-Time、Serving-Node を伴う SMS-GMSC-Alert-Event。 | MSISDN の無い SMS と、省電力のモードの UE のためのものです。そういう UE には、MME が SGd で直接知らせます。このプロジェクトには、そういう UE がありません。 |
| `SRRFlags`、TS 29.338 Table 5.3.3.4/1 | GPRS-Indicator のビット（SMS-GMSC が serving node を 2 つ求める）と SM-RP-PRI（message waiting data に自分のアドレスが既にあっても配送を求める）。読むのは Single-Attempt-Delivery だけです。 | このプロジェクトの HSS は serving node を 1 つだけ持ち、優先度を読みません。 |
| `MWDStatus`、TS 29.338 Table 5.3.3.8/1 | HSS が 5G の serving node を待っていることを示すビット 4 と 5 には名前を付けない。このプロジェクトの HSS は、この AVP を成功の SRA だけに入れ、DIAMETER_ERROR_ABSENT_USER のときは入れない。 | HSS は 5G の serving node を返さない。SC がこの AVP を読むのは成功した配達のあとだけで、その配達の前には成功の SRA がある。 |
| `SMDeliveryOutcome`、TS 29.338 5.3.3.14 | MSC、SGSN、3GPP でないアクセスの SMSF を通した配送の結果。 | このプロジェクトには、どのノードもありません。 |
| `ReportSMDeliveryStatusRequest`、TS 29.338 5.3.2.7 | Supported-Features、SMSMI-Correlation-ID、RDR-Flags と、配送に失敗したノード。 | S6c は RDR に機能を定めていません。このプロジェクトには、MSISDN の無い SMS も、1 回だけ試す配送もありません。HSS は、種類ごとに 1 つのノードを覚えます。 |
| `ReportSMDeliveryStatusAnswer`、TS 29.338 5.3.2.8 | Supported-Features と、通知のノード（Serving-Node、Additional-Serving-Node、SMSF のアドレス）。HSS はこれで、SMS-GMSC が知らなかったノードを通して、すぐに送り直すよう伝えます。 | このプロジェクトの HSS は、代わりに ALR で SC に知らせます。 |
| `ServingNode`、TS 29.338 5.3.3.6 | SGSN と MSC、IP-SM-GW-SBI-Support-Indicator。 | このプロジェクトには SGSN も MSC もありません。IP-SM-GW には Diameter だけでつなぎます。 |
| `SMSF3GPPAddress`、TS 29.338 5.3.3.35、5.3.3.36 | SMSF-3GPP-SBI-Support-Indicator と、SMSF-Non-3GPP-Address の全体。 | このプロジェクトは、SMSF に Diameter だけでつなぎます。3GPP でないアクセスもありません。 |
| `ReadyForSMRequest`、TS 23.204 6.5b step 2d と 6.6 step 3、TS 29.002 12.4.1 | MAP-READY-FOR-SM。IP-SM-GW はこれで、利用者にまたショートメッセージが届くようになったことと、利用者のメモリに空きができたことを HLR に知らせます。代わりに、このプロジェクトが独自に決めた S6c のコマンド Ready-For-SM が、2 つの知らせを運びます。コマンドコードは実験用の 16777214 です（RFC 6733 11.2.1）。利用者は IMSI ではなく MSISDN で名指しし、知らせの理由は TS 29.272 7.3.83 の Alert-Reason で、IP-SM-GW は IP-SM-GW-Number で示します。Additional Alert Reason Indicator と Maximum UE Availability Time は省きます。 | このプロジェクトは MAP を作っていません。S6c にも Sh にも、この知らせのコマンドはありません。実験用のコードにしたので、TS 29.338 のコマンドと取り違えられません。Wireshark は知らないコマンドとして表示し、このコマンドを持たない HSS は DIAMETER_COMMAND_UNSUPPORTED で答えます。RFC 6733 に沿う形はありません。S6c にコマンドを足せば 1.3.4 に、新しいアプリケーションを作れば 2.4 に反します。IANA は実験用の Application-Id を取っていないためです。実験用のコマンドコードは、実在のコマンドと決して重なりません。 |
| `UserIdentifier`、TS 29.336 6.4.2 | External-Identifier と LMSI。 | マシン型の通信と VLR のためのものです。このプロジェクトには、どちらもありません。 |
