# メディアの制御

この記録は呼を処理するノードがメディア中継ノードをどう制御するかと、網の境界の装置をどう振る舞わせるかの設計です。

## 選んだこと

- P-CSCF はこのプロジェクト独自の簡単な HTTP の API でメディア中継ノードを制御します。MRFC が MRFP を制御するときと、IBCF が TrGW を制御するときも同じです。
- 制御する側のノードは、参照点ごとに操作のインタフェースを 1 つ宣言します。操作の名前は H.248 のコマンドに合わせ、型は `internal/agw` のものを使います。境界の後ろの実装は、このインタフェースで差し替えます。
- H.248 で制御する実装（`mediactl.Controller`）を、同じインタフェースの後ろに 2 つ目の実装として並べます。既定は独自の API のままです。
- P-CSCF を 1 回通るごとに、メディア中継ノードの中に 1 つのコンテキストを作ります。
- IBCF は B2BUA にします。
- IBCF は相手として登録されていない送信元の要求に応答せず、捨てます。

## 独自の API を既定にする理由

H.248 は仕様が大きく、最初から実装すると通話の基本を学ぶ前に時間を使うためです。H.248 は符号化がテキストと 2 進（ASN.1）の 2 つあり、使えるライブラリも少ないという弱みもあります。ゲートウェイを制御するだけなら、HTTP の API のほうが軽く、読みやすくなります。5GC でも、機能どうしの制御は HTTP/2 の SBI で行います。

## H.248 も並べる理由

H.248 も並べるのは、標準のメディア制御のプロトコルを学べるようにするためです。H.248 には次の利点があります。

- 語彙が標準で決まっています。コンテキストとターミネーションの考え方に加えて、トーン、音声ガイダンス、DTMF の検出、RTP の統計、会議の音声の合成を扱うパッケージがあります。
- 1 つのトランザクションに複数のコマンドを入れて、まとめて実行できます。CHOOSE を使うと、コンテキストやターミネーションの ID をゲートウェイに選ばせられます。
- ゲートウェイから制御する側への通知（Notify）が、プロトコルの仕組みに含まれています。
- 保守の手順（ServiceChange、Audit）があります。

そのため既定は変えず、H.248 はプロトコルを学ぶための 2 つ目の実装にします。学習者は同じ通話を両方の制御で行い、キャプチャを比べられます。ServiceChange と Audit は、冗長構成での障害の観察と組み合わせて見せます。

H.248 の範囲は、3GPP のプロファイルが使うものに限ります。Iq（TS 29.334）、Ix（TS 29.238）、Mp（TS 29.333）です。ITU-T の勧告は H.248.1 です。IETF での名前は Megaco（RFC 3525）で、今は Historic です。

独自の API も H.248 の Add、Modify、Subtract、Notify に写せる形にしてあります（[mmtel-mrf.md](mmtel-mrf.md)）。

## 参照点ごとのインタフェース

制御する側のノードが、自分の参照点の操作をインタフェースとして宣言します。P-CSCF の `pcscf.Relay`（Iq）、MRFC の `mrfc.MRFP`（Mp）、IBCF の `ibcf.TrGW`（Ix）です。HTTP のクライアント `*agw.Client` と、H.248 の `*mediactl.Controller` の両方が、3 つとも実装します。どちらをノードに渡しても動きます。

操作の名前は H.248 のコマンドの名前です。コンテキストを丸ごと消す操作は `Subtract`、1 つのターミネーションを足す操作と消す操作は `AddTermination` と `SubtractTermination` です。型は `internal/agw` のもの（`agw.TerminationRequest`、`agw.Context`、`agw.Termination` など）を使い、`mediactl` には同じ意味の型を置きません。`mediactl` は `agw` を参照しますが、`agw` は `mediactl` も `h248` も参照しません。

各操作が当たる手順は次のとおりです。

| 参照点 | インタフェース | 操作 | 手順 |
| --- | --- | --- | --- |
| Iq（TS 29.334） | `pcscf.Relay` | `Add` | Reserve AGW Connection Point（5.17.2.2）を、アクセス側とコア側に 1 回ずつ。宛先があれば Reserve and Configure AGW Connection Point（5.17.2.4） |
| | | `Modify` | Configure AGW Connection Point（5.17.2.3） |
| | | `Subtract` | Release AGW Termination（5.17.2.5）を、両方のターミネーションに `*` で |
| Mp（TS 29.333） | `mrfc.MRFP` | `Add` | Reserve IMS Resources（5.17.2.2）。宛先があれば Reserve and Configure IMS Resources（5.17.2.4）、信号があれば Start Announcement（5.17.2.9） |
| | | `AddTermination` | 今あるコンテキストへの Reserve IMS Resources（5.17.2.2、5.17.2.30） |
| | | `Modify` | Configure IMS Resources（5.17.2.3）。信号があれば Start Announcement（5.17.2.9）、空の信号なら Stop Announcement（5.17.2.10） |
| | | `SubtractTermination` | Release IMS Termination（5.17.2.5） |
| Ix（TS 29.238） | `ibcf.TrGW` | `Add` | Reserve TrGW Connection Point（Table 5.17.2.1.1） |
| | | `Modify` | Configure TrGW Connection Point（同） |
| | | `Subtract` | Release TrGW Termination（同） |

再生の終わり（Announcement Completed、TS 29.333 5.17.2.11）は、逆向きの Notify で届きます。HTTP では `agw.Events.Notify` の URL への POST で、H.248 では `ControllerConfig.Notify` に渡る `mediactl.Notification` です。`Notification` は `agw.Notify` を含みます。

### 両方の実装が守ること

- `Modify` と `Subtract` は error だけを返します。H.248 の Modify の応答は、求めた記述子のほかに何も持たず、Iq の Subtract の応答には統計がないためです（TS 29.334 5.7.7、Table 5.7.7.2）。HTTP の API はどちらにもターミネーションやコンテキストを返しますが、クライアントは読みません。
- `SubtractTermination` は、ターミネーションが運んだ RTP のパケットの数を、`agw.Termination` の `Packets`（rtp/pr）と `PacketsSent`（rtp/ps）で返します。コンテキストが一緒に消えたかどうかも返しますが、Mp の Subtract の応答はこれを伝えません。そのため Controller はいつも false を返します。MRFC は、次の `AddTermination` が `agw.ErrNoContext` で失敗したときに、会議のコンテキストが消えたことを知ります（`attach`）。それまで、会議の記録は Store に残ります。
- 空の `Mode` は、Add では sendrecv、Modify では今のモードのままです。H.248 では、モードの無い Add は Inactive になるので、Controller は Add に必ず StreamMode を書きます。Mp の Modify にモードが無ければ、LocalControl の記述子を書きません。MRFC が ACK のあとで音声ガイダンスを始める Modify がこれに当たります。Iq の IMS-AGW はメディアを両方向に通すので、Iq の Controller は Modify にも sendrecv を書きます。
- ラッチは `agw.TerminationRequest` の `Latch` で決めます。値は ipnapt/latch の napt の 3 つ（`off`、`latch`、`relatch`）と、信号の無い Signals に当たる `stop` です（H.248.37 6.3.1.1.1、6.6.4）。空の `Latch` は、Add ではラッチ（`latch`）、Modify では今のラッチのままです。H.248 では、信号の無い Add はラッチしないので（6.6.1）、Iq の Controller は `Latch` の無い Add に `napt = LATCH` を書き、`off` と `stop` の Add には Signals を書きません。Modify には `Latch` があるときだけ Signals を書き、`stop` は空の Signals にします。Gateway は逆に、信号の無い Add を `off` として、Signals の無い Modify を空の `Latch` として中継ノードに渡します。Mp には ipnapt が無いので、Mp の Controller は `Latch` を断り、Mp の Gateway は Add を `off` で渡します。P-CSCF は、UE 側のターミネーションを `Latch` 無しで（ラッチして）、網の側を `off` で Add します。NAT の内側にいる可能性があるのは UE だけで、網の側の相手は通話の途中で替わるためです。たとえば CAT の呼では、応答のあと MRFP に代わって着信者の SDP が届きます。網の側がラッチしていると、新しい SDP の宛先より MRFP の送信元が勝ち、発信者の声が着信者に届きません。UE 側のターミネーションの宛先を替える Modify には、P-CSCF が `relatch` を付けます。ラッチしたターミネーションは、あとから届いた Remote の記述子によらず、ラッチしたアドレスへ送り続けるためです（6.6.2、6.6.4）。ゲートウェイが相手の移動に付いていくのは、コントローラが移動のたびにそれを求めたときだけで（1.1.1）、UE が再オファーや応答の SDP で知らせる移動がこれに当たります。`latch` ではなく `relatch` にしたのは、`latch` が最初に届いたパケットの送信元を取るので（6.6.2）、UE が前のポートから送ったパケットがまだ飛んでいるとそこに留まるためです。再交渉のオファーが 2xx をもらえず、セッションがその前の状態に戻るときも（RFC 3261 14.1）、戻る先への移動として `relatch` を付けます。宛先の替わらない Modify には付けません。ラッチし直しが待ち続け、ほかの送信元から届いたパケットで宛先が動くためです。IBCF も、ホーム網の側を `off` で Add します。相手網の側と MRFC は `Latch` を決めないので、中継ノードはラッチします。中継ノードは `off` のターミネーションを保存先に残し、作り直したあとも `off` で開き直します。
- コーデックと帯域（m= 行の形式と b=AS）は、`agw.TerminationRequest` の `LocalMedia` と `RemoteMedia` に入れます。中継ノードはトランスコードしないので、HTTP の API はこれを運びません。Controller は Local と Remote の記述子に書きます。
- Mp の Add では、ターミネーションの ID を MRFP が選びます（CHOOSE）。Controller は、ID を指定した要求を断ります。
- 拒否されたコマンドは、`errors.Is` で `agw` のエラーと見分けられます。411 は `agw.ErrNoContext`、430 と 435 は `agw.ErrNoContext` と `agw.ErrNoTermination`、510 は `agw.ErrNoPort`、449 は `agw.ErrUnknownAnnouncement` に当たります。どれも、Gateway が中継ノードのそのエラーに返すコードです。449 は知らないパラメータの値を表すコードです。Controller は、送る信号のほかのパラメータを送る前に確かめるので、Controller の呼び出し元にとって 449 は、MRFP が持たない音声ガイダンスを意味します。Controller が使えるゲートウェイを持たないときと、505 のときは `mediactl.ErrUnavailable` です。

### インタフェースに入れないもの

- 音声ガイダンスがあるかの問い合わせ（`Announcement`）は、`mrfc.MRFP` に入れず、別のインタフェース `mrfc.Announcements` にしました。コンテキストを変えない問い合わせで、Mp にはこれに当たる手順がないためです（AuditCapabilities は TS 29.333 5.8.6 で今後の検討とされています）。`*agw.Client` は両方を実装します。
  - `mrfc.New` は、MRFP ごとに `mrfc.Processor` の `Control` と `Announcements` の両方を求めます。そのため、H.248 の Mp の Controller を `Control` に渡す MRFC にも、`Announcements` を別に用意する必要があります。Mp には、MRFP がどの音声ガイダンスを持つかを問うコマンドがないためです。MRFC は、MRFP が持つ音声ガイダンスの一覧を、プロビジョニングで知っておくことになります。この一覧を `mrfc.Announcements` として渡す設定は、まだありません。
- IBCF が SDP に書く TrGW のアドレスの置き換えは、IBCF の側に置きます（`ibcf.Config` の `TrGWCoreAddress` と `TrGWPeerAddress`）。TrGW は網ごとにアドレスを持ちますが、TrGW を務めるメディア中継ノードは、両方のターミネーションに 1 つのアドレスを書くためです。
- Ix の H.248 のプロファイルはありません。Controller は Iq のプロファイルで `ibcf.TrGW` を実装します。IBCF はターミネーションに Iq の名前を使い、`core` を自網の側、`access` を相手網の側にあてます。

### ゲートウェイの側

`mediactl.Gateway` は、受けたコマンドを、`mediactl.Relay`（Iq）か `mediactl.Processor`（Mp）を呼んで実行します。これはゲートウェイが自分で宣言するインタフェースで、操作と型は `pcscf.Relay` と `mrfc.MRFP` と同じです。そのため、`*agw.Client` を Gateway の後ろに置くこともできます。写しを置くのは意図してのことです。ゲートウェイの側が、メディアを制御するノードのパッケージ（`pcscf`、`mrfc`）に依存しないようにするためです。Mp の Gateway は、Processor に頼むターミネーションに `t<n>` の ID を付けます。`n` は H.248 のターミネーションの ID（`ip/<context>/<n>`）の番号です。Add が返すコンテキストは ID の map なので、こうして各ターミネーションを見つけます。

### 設定でどちらの実装を使うかを決める

ノードは、設定ファイルの節で、どちらの実装を使うかを決めます。既定は独自の HTTP の API です。

| 節 | 役割 | 読む項目 |
| --- | --- | --- |
| `pcscf.media_relays` | P-CSCF | 中継ノードの制御 API の URL の一覧 |
| `pcscf.h248` | P-CSCF | H.248 のメッセージを受けるアドレス（`listen`） |
| `agw.control` | メディア中継ノード | HTTP の制御 API を配信するアドレス |
| `agw.h248` | メディア中継ノード | H.248 のメッセージを受けるアドレス（`listen`）、自分の名前（`host`）、登録するコントローラ（`controller`） |

どちらの側も、2 つの節のうち 1 つだけを読みます。1 つの中継ノードに 2 つのコントローラが付くと、互いのコンテキストを書き換えるためです。中継ノードを H.248 で制御するときは、HTTP の制御 API を配信しません。

ほかの決めごとは次のとおりです。

- コントローラの名前（MId）は、P-CSCF の `sip.host` です。DNS がそのノードに解決する名前なので、ゲートウェイの `controller` に書けば届きます。ゲートウェイの名前は `agw.h248.host` です。どちらも TS 29.334 5.11 が勧めるドメイン名にします。
- H.248 で制御する P-CSCF が持つ `pcscf.MediaRelay` は 1 つで、名前は `iq` です。1 つの Controller が、登録したすべてのゲートウェイを代表し、コンテキストの ID が `<ContextID>@<MId>` でゲートウェイを指すためです。そのため、ダイアログが覚えるのは参照点であって、ゲートウェイではありません。中継ノードを増やしても減らしても、P-CSCF の設定は変わりません。
- `mrfp` の役割は `h248` の節を読みません。Mp には Mp のコマンドがあり、ゲートウェイの側はそれを Relay ではなく Processor のために話すためです。
- ゲートウェイは `controller` の名前を、登録のたびに引き直します。引くたびに得るアドレスは 1 つなので、名前の指すアドレスが変わったときに、ゲートウェイはそれに付いていきます。

### H.248 で制御するときの P-CSCF の台数

メディアゲートウェイは、1 つのコントローラにだけ登録します。そのため、H.248 で制御する P-CSCF を複数の台で動かすと、1 つのゲートウェイは 1 台の P-CSCF にしか登録しません。別の台が、そのゲートウェイのコンテキストを変えたり解放したりすることはできません。独自の HTTP の API では、どの台からでも、どの中継ノードにも要求を送れます。

これは H.248 の仕組みそのものです。各台が自分のゲートウェイの組を持つ形なら、役割としては横に広げられます。ただし、保存先を共有してダイアログを引き継ぐ構成（[state-redundancy.md](state-redundancy.md)）とは組み合わせられません。引き継いだ台が、そのコンテキストを持つゲートウェイとの結び付きを持たないためです。

そこで設定は、H.248 で制御する P-CSCF に共有の保存先（`store` の kind が valkey）を許しません（`internal/config` の `checkMediaControl`）。ほかの台が引き継げることを前提にしているためです。P-CSCF には、組の名前の設定 `sip.pool` がありません。H.248 で命令を受けるメディア中継ノードにも、共有の保存先を許しません。ゲートウェイは、どの H.248 のコンテキストが中継ノードのどのコンテキストかを自分のメモリに持つので、前の処理が残したコンテキストを中継ノードが開き直すと、どのコントローラも解放できないポートが残るためです。実網でゲートウェイを別のコントローラへ移す手順は作っていません。省いたことは `internal/h248/README.md` の表にあります。

### 確かめ方

実装の組み合わせは、各インタフェースの隣の試験のファイル（`internal/pcscf/relay_test.go`、`internal/mrfc/mrfp_test.go`、`internal/ibcf/trgw_test.go`）で、コンパイルのときに確かめます。確かめを試験のファイルに置くので、P-CSCF、MRFC、IBCF の本体は `mediactl` を参照しません。

ゲートウェイの側の写しは、`internal/h248/mediactl/interfaces_test.go` で確かめます。`mediactl.Relay` と `pcscf.Relay`、`mediactl.Processor` と `mrfc.MRFP` を、互いに代入できることを確かめるので、どちらかにだけメソッドが増えるとコンパイルが通りません。こうして写しとノードのインタフェースを同じに保ちます。`*agw.Client` が `mediactl.Relay` と `mediactl.Processor` を実装することも、同じファイルで確かめます。このファイルは外部の試験のパッケージ（`mediactl_test`）なので、`mediactl` の本体は `pcscf` と `mrfc` を参照しません。

## P-CSCF の通過ごとのコンテキスト

P-CSCF は、発信側の UE と着信側の UE の両方を担当することがあります。このとき INVITE は P-CSCF を 2 回通ります。通過ごとに、メディア中継ノードの中に 1 つのコンテキストを作ります。コンテキストには、UE の側（アクセス側）と網の側（コア側）の 2 つのターミネーションがあります。コンテキストとターミネーションは H.248 の用語で、ターミネーションはメディアの出入り口の 1 つを指します。

その結果、同じ P-CSCF の UE どうしの通話では、RTP はメディア中継ノードを 2 回通ります（UE 1、発信側のアクセス側、発信側のコア側、着信側のコア側、着信側のアクセス側、UE 2）。実網で発信側と着信側の IMS-AGW が別の装置になるときと同じ形で、キャプチャで両側の中継を見分けられます。

1 つのコンテキストで両方の UE をつなぐ方式は採りません。P-CSCF が、2 回の通過が同じ通話だと知る必要があり、別の P-CSCF に分かれたときと動きが変わるためです。

## フォークした INVITE の、中継ノードに通せない別のダイアログの 2xx

発信側の UE が 2xx を受け取ったあとに、フォークした INVITE の別のダイアログから 2xx が届くことがあります。その SDP を中継ノードに通せないとき、P-CSCF はこの 2xx を UE に送りません。通せないのは、次のどれかのときです。

- 中継ノードがアンサーを受け取らない。
- P-CSCF がダイアログを保存できない。
- P-CSCF がそのダイアログをすでに解放している。
- 中継ノードがアンサーを受け取れる場合も、中継ノードのコンテキストと Rx のセッションは呼に 1 つずつで、どちらも UE が受け取った 2xx のダイアログに使っている。別のダイアログのアンサーを通すと、その呼のメディアが別のダイアログの相手に移る。

最後の項目は、UE が 2xx を受け取ったあとに届く別のダイアログの 2xx のすべてに当てはまります。そのため P-CSCF は、中継ノードにも PCRF にも問い合わせません。

このとき P-CSCF は、UE の代わりに ACK を返し、そのダイアログに BYE を送って終えます（`internal/pcscf/proxy.go`）。UE は、先に受け取った 2xx の呼をそのまま続けます。呼の中継ノードのコンテキスト、Rx のセッション、P-CSCF のダイアログは変わりません。

この場合の手順は、TS 24.229 にありません。RFC 3261 16.7 step 5 は、プロキシが INVITE への 2xx をすべて転送するとしています。それでも P-CSCF がそのダイアログを自分で終えるのは、次の理由からです。

- 転送すると、中継ノードが書き換えていない SDP が UE に届きます。UE は中継ノードを通らずに、相手へメディアを送ることになります。IMS-ALG として P-CSCF がメディアを中継ノードに通す作り（TS 24.229 6.7.2.1）が崩れます。
- 2xx を捨てると、着信側は ACK を待って、64*T1（32 秒）のあいだ 2xx を再送し続けます。そのあと着信側が自分でダイアログを終えるまで、着信側に呼が残ります（RFC 3261 13.3.1.4）。

ほかに、次の 3 つの案も考えましたが、採りませんでした。

- SDP を中継ノードのアドレスに書き換えて 2xx を転送し、ACK と BYE は UE に任せる案。中継ノードにはこのダイアログのアンサーが通っていないので、UE の RTP を相手へ送れません。それなのに、SDP には中継ノードのアドレスを書くことになります。
- 別のダイアログにも中継ノードのコンテキストと Rx のセッションを足して、2xx を UE に送る案。1 つの INVITE に確立したダイアログがいくつもでき、P-CSCF がダイアログを保存する形と解放の手順が変わります。この網の S-CSCF は INVITE を分岐しないので、そこまで作る理由がありません。
- 未決の論点として残す案。根拠になる条項は見つかりませんでした。それでも、ほかの選び方には上の問題があるので、残さずに今の作りに決めました。

別のダイアログの中の要求も、呼を変えません。P-CSCF はダイアログを Call-ID と UE のタグを鍵にして保存するので、そうした要求も呼のダイアログに行き着きます。UE が 2xx を受け取ったあとは、P-CSCF は相手のタグでダイアログを見分けます（`internal/pcscf/dialog.go`、`internal/pcscf/session.go`）。BYE は先へ送り、その応答が 481 でも、呼のダイアログは終えません。SDP の入った要求には、P-CSCF が自分で 500 を返します。中継ノードのコンテキストに通せば呼のメディアが移り、そのまま送れば中継ノードを通らないためです。

学習者向けの説明は、手順書（`docs/guide/en/call.md` と `docs/guide/ja/call.md`）とコードのコメントにあります。

## 網の境界は実際の装置の動きに合わせる

- IBCF を B2BUA にするのは、実際の網の境界の装置がそうであるためです。
- 知らない相手からの要求を捨てたことは、ログと指標に残します。学びやすさのために、実際の運用で起きない動きは見せません。

網間接続の調査と作りは [interconnect.md](../background/interconnect.md) にあります。
