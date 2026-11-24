# HTTP の API

[English](../en/apis.md)

このプロジェクトのノードは、SIP と Diameter のほかに HTTP でも話します。その API の一部は 3GPP の仕様に従います。仕様はそれらの API を OpenAPI のファイルで定めていて、実際の網でもノードのあいだで使われます。残りはこのプロジェクト独自のものです。製品が独自に持つインタフェースと同じように、網を運用したり、学習の道具を動かしたりします。このページは両方を並べて、標準の API と、このプロジェクトにしか無い API を見分けられるようにします。

## 仕様に従う API

Ut は XCAP で、XML の API です。その下に並べたサービスベースのインタフェース（SBI）の API は、仕様の OpenAPI のファイルで定義されています。リポジトリにはそのファイルを置いていません。3GPP のファイルサーバーにある仕様書の書庫に入っています。各パッケージの説明には、仕様の番号と版、OpenAPI のファイル名、そのファイルのどの操作を作ったかが書いてあります。JSON の型はファイルの属性の名前を使います。試験は、型ごとの名前を、ファイルの同じ名前のスキーマから写した一覧と比べます。

| API | 使うもの | 仕様 | コード |
| --- | --- | --- | --- |
| Ut、付加サービスの XCAP | UE シミュレータが設定を読み書きし、MMTel AS が応じる | TS 24.623（XCAP、RFC 4825） | [`internal/xcap`](../../../internal/xcap/doc.go) |
| Npcf_PolicyAuthorization、N5 の参照点 | 5GS で、P-CSCF が通話のメディアを PCF に求める。Rx に当たる | TS 29.514、`TS29514_Npcf_PolicyAuthorization.yaml` | パッケージ `n5` |
| Nhss_imsUEContextManagement、Nhss_imsUEAuthentication、Nhss_imsSubscriberDataManagement、N70 と N71 の参照点 | I-CSCF と S-CSCF が Cx の代わりに N70 で、AS が Sh の代わりに N71 で HSS に問い合わせる | TS 29.562、`TS29562_Nhss_imsUECM.yaml`、`TS29562_Nhss_imsUEAU.yaml`、`TS29562_Nhss_imsSDM.yaml` | パッケージ `nhss` |
| どの SBI にも共通の部分。HTTP/2、JSON、誤りの答え | パッケージ `n5` と `nhss` | TS 29.500 と、TS 29.571 の ProblemDetails（`TS29571_CommonData.yaml`） | パッケージ `sbi` |

このうち Compose のプロジェクトで動くのは Ut だけです。残りの 3 つは[どのノードも動かさないコード](README.md#どのノードも動かさないコード)なので、キャプチャには出ません。パッケージは `internal` の下にあります。

## このプロジェクト独自の API

3GPP は、要素管理システムとネットワーク要素のあいだのインタフェース（TS 32.600 4.2.1.3）や HSS のプロビジョニングのインタフェースのように、一部のインタフェースを製品ごとに任せています。ほかに、試験用の UE の制御の API やラボそのもののように、学習用の環境だからあるものもあります。これらの API には、どれもコードの隣に OpenAPI 3.1 のファイルがあります。パッケージの試験は、すべての要求を検査の部品に通し、要求と答えをファイルと突き合わせます。ハンドラーとファイルのあいだで経路、フィールド、状態コードが食い違えば試験が落ちるので、ファイルはコードのとおりです。

| API | 応じるもの | OpenAPI のファイル |
| --- | --- | --- |
| ノードの保守の API。役割の状態、ドレイン、設定、ログのレベル、アラームの一覧 | 各ノードの保守用のリスナー | [`internal/maintenance/openapi.yaml`](../../../internal/maintenance/openapi.yaml) |
| OAM の API。全ノードの操作、設定の履歴、加入、試験用の利用者、バックアップ | `oam` の役割 | [`internal/oam/openapi.yaml`](../../../internal/oam/openapi.yaml) |
| HSS のプロビジョニングの API | `hss` の役割のプロビジョニング用のリスナー | [`internal/hss/openapi.yaml`](../../../internal/hss/openapi.yaml) |
| ラボの API。HEP の写しから得たメッセージ、通話、登録と、ラボが HSS、EPC スタブ、障害のエージェント、UE シミュレータに取り次ぐ経路 | `lab` の役割 | [`internal/lab/openapi.yaml`](../../../internal/lab/openapi.yaml) |
| UE シミュレータの制御の API | `ue serve` | [`internal/ue/openapi.yaml`](../../../internal/ue/openapi.yaml) |
| メディア中継ノードの制御の API。呼び出しのそれぞれが H.248 のコマンドに当たる | IMS-AGW、TrGW、MRFP | [`internal/agw/openapi.yaml`](../../../internal/agw/openapi.yaml) |

アクセス網のスタブと障害のエージェントにも独自の API があり、パッケージの説明に書いてあります。EPC スタブの API の要求は TS 24.301 の NAS のメッセージにちなんだ名前を持ちます（[`internal/epc`](../../../internal/epc/http.go)）。5GC のスタブの要求は TS 24.501 のメッセージにちなみます（パッケージ `fivegc`。どのノードも動かしません）。障害のエージェントの API は [`internal/fault`](../../../internal/fault/http.go) にあります。

加入者の管理には OSS に向けた標準のインタフェースがあります。ただ、その情報モデルには、加入の長期の鍵、OPc、SIP Digest のパスワードを入れる属性がありません。詳しいことと、HSS のプロビジョニングの API がこのプロジェクト独自のものである理由は、[UE が IMS に登録する流れを追う](registration.md)にあります。

## 保守の API で省いたこと

保守の API のアラームの一覧は TS 28.111 に従い、次のものを省いています。それぞれに、パッケージ `maintenance` の `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| アラームの一覧、TS 28.111 7.3.1.3 と 7.4.1 | セキュリティのアラームの種類。 | セキュリティのアラームはサービスの利用者を名指しし、それは加入者になります。ここのアラームは加入者を名指ししないので、一覧が加入者の数とともに大きくなりません。 |
| アラームの一覧、TS 28.111 7.3.1.2 | アラームの記録の任意の属性のほとんど（backedUpStatus、trendIndication、相関の属性、誰がアラームを解除したかの属性など）。alarmChangedTime、specificProblem、additionalText と、`ackSystemId` を含む確認の属性は残し、必須の notificationId も持ちます。 | どの役割も、これらの値を持っていません。ここのアラームは、どれも上げたノード自身が解除し、アラームどうしを結び付ける役割もありません。 |
| アラームの一覧、TS 28.111 7.3.2.2 と 6.13 | 一覧の operationalState、administrativeState、unreliableAlarmScope。 | 一覧はノードが答えるあいだ必ず動くので、運用状態はいつも `ENABLED` になります。代わりに `dropped_records` が、一覧がすべてのアラームを持てなくなったことを示します。 |
| アラームの一覧、TS 28.111 6.3 と 7.4.1 | `object_instance` は Distinguished Name に見えるだけです。クラスは TS 28.622 の汎用の ManagedElement と ManagedFunction で、ネットワーク資源のモデルのクラスではない Peer も使い、値をエスケープしません。 | ここのノードはネットワーク資源のモデルの管理対象ではなく、すべての役割に TS 28.705 の IMS のクラスがあるわけでもないためです。 |

解除したアラームは、確認されるまで一覧に残ります（TS 28.111 6.14）。また、運用者か機械が確認しないまま、記録を一覧から消すべきではありません（6.7）。解除したアラームを 1 時間のうちに誰も確認しなければ、ノードがその機械の役をします。ノードは `ack_user_id` を `retention`、`ack_system_id` を自分の名前にしてアラームを確認し、記録は、確認した解除済みのアラームと同じように一覧から消えます。そうしないと、誰も見ていない一覧は、終わった障害をいっぱいになるまで持ち続けます。TS 28.111 は、外からの確認を受けない producer に、自分でアラームを確認することを求めています（5 章の `REQ-FM-MC-12`）。外からの確認を受ける producer については何も書いていないので、この確認はこのプロジェクトの決めごとです。`retention` という名前で、運用者の確認と区別できます。
