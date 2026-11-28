# 保守で止めることと、突然失うこと

保守の作業でノードを止めたいとき、運用者はまずそのノードを運用から外します（shut down）。外したノードは新しい仕事を断り、始めた仕事は最後まで続けます。断られた仕事は、送り手がほかの台へ送り直します。運用者の要求への答えはすぐに返りますが、それは「外し始めた」という意味です。始めた仕事が終わったかどうかは、そのあとノードの状態が `LOCKED` になるかで見ます。ただし `LOCKED` が仕事の終わりを表すのは、期限で切られなかったときだけです。

ノードのプロセスを止めたとき、プロセスが突然消えたとき、保存先を失ったときは、ノードは仕事を選べません。何が残るかは、その仕事の状態がどこにあるかで決まります。台が共有する保存先にある状態は、ほかの台が引き継げます。プロセスの中にある状態は、プロセスと一緒に消えます。相手のノードが持っている状態は、相手が送り直すことで戻ります。

この章では、新しい登録、新しい呼、通話中の呼、処理中の Diameter の要求、預かっているショートメッセージの 5 つについて、4 つの場合を比べます。保守で外すとき、正常に止めるとき、プロセスが消えるとき、保存先を失うときです。

## この章で扱う構成と前提

構成は 2 つあります。`deploy/compose` では、各役割が 1 台で動き、状態をプロセスのメモリに置きます。`deploy/redundancy/redundancy.yaml` を重ねると、状態を持つ役割は Valkey の共有の保存先に状態を置き、同じ名前で作り直したノードは、前のノードが残した状態をそこで見つけます。

Compose の各ノードは、保守用の API をポート 8085 で出しています。OAM のサーバーは、同じ要求を役割のすべてのノードへ送れます。この API はこのプロジェクトが決めたものです。管理する側と管理されるノードのあいだのインタフェースは、製品ごとに決めてよいとされています（TS 32.600 4.2.1.3）。

状態の置き場と、複数台での引き継ぎは [複数台と障害で引き継ぐもの](design-shared-state.md) で、アプリケーションサーバーが応答しないときの扱いは [アプリケーションサーバーが使えないとき](design-as-unavailable.md) で、ショートメッセージの再送と重複は [SMS の再送と重複](design-sms-duplicates.md) で扱います。

## 運用から外す操作と、その答え

各役割は、2 つの状態を持ちます。管理上の状態（administrative state）は運用者が決め、動作の状態（operational state）は役割が自分で決めます。値は TS 28.622 5.3.1 と 5.3.3 のもので、状態の移り方は ITU-T X.731 7.1.3 に従います。

| 管理上の状態 | 意味 | この実装で入るとき |
| --- | --- | --- |
| `UNLOCKED` | 利用者に仕えてよい | 起動したとき。unlock を受けたとき |
| `SHUTTINGDOWN` | 今いる利用者にだけ仕えてよい | shut down を受け、利用者が残っているとき |
| `LOCKED` | 誰にも仕えない | lock を受けたとき。`SHUTTINGDOWN` で利用者が 0 になったとき。shut down の期限が過ぎたとき |

動作の状態は `ENABLED` か `DISABLED` です。たとえば S-CSCF と I-CSCF は、Cx の要求を受ける HSS が 1 台も無いあいだ `DISABLED` になります。

shut down を受けた役割は、次のように状態を変えます。

[internal/maintenance/state.go](../../../internal/maintenance/state.go)

```go
	m.shutdowns++
	if users == 0 {
		m.enter(Locked)
		m.dispatchAndUnlock()
		return nil
	}
	m.enter(ShuttingDown)
	if timeout > 0 {
		m.deadline = m.clock.Now().Add(timeout)
		shutdown := m.shutdowns
		m.timer = m.clock.AfterFunc(timeout, func() { m.deadlinePassed(shutdown) })
	}
	m.dispatchAndUnlock()
```

`users` は、この直前に役割に数えさせた利用者の数です。0 なら、その場で `LOCKED` になります。利用者がいれば `SHUTTINGDOWN` になり、要求に期限（`timeout_seconds`）があれば、その時刻に `LOCKED` にするタイマーを置きます。そのあとは、役割が仕事を 1 つ終えるたびに知らせ、数が 0 になったところで `LOCKED` になります。状態はその場で変わるので、要求ごとに状態を読む役割は、次の要求から新しい仕事を断ります。`dispatchAndUnlock` は、変化を知らせてほしい役割（HSS や SC）に、その変化を伝えます。

運用者の要求への答えには、この時点の状態が入ります。

| 答えの状態 | 分かること | 分からないこと |
| --- | --- | --- |
| `SHUTTINGDOWN` | ノードが要求を受け、新しい仕事を断り始めた。数えている仕事が残っている | いつ終わるか |
| `LOCKED`（最後の仕事が終わって） | このプロセスが数えている仕事は残っていない | 数えていない仕事。ほかの台の仕事 |
| `LOCKED`（期限が過ぎて、または lock で） | もう誰にも仕えない | 残っていた仕事は途中で切られた |

つまり、操作が成功したことと、仕事が終わったことは別です。答えが `SHUTTINGDOWN` なら、運用者は状態を読み続け、`LOCKED` になるのを待ちます。`LOCKED` にも 2 通りあるので、期限で切ったのかどうかを、期限を付けたかどうかとログで区別します。OAM のサーバーがノードごとに送った要求への答えも、ノードごとに返ります。ある役割の 1 台が答えなくても、ほかの台の答えは成功のことがあります。

`LOCKED` が約束するものは、役割が何を利用者として数えるかで決まります。数えるのはこのプロセスの数だけで、保存先を探して数えることはしません。全加入者を走査しないためです。

| 役割 | 利用者として数えるもの |
| --- | --- |
| P-CSCF | この台を使う UE の IP の対応付け |
| I-CSCF、S-CSCF | 処理中のトランザクション |
| HSS | 答えている途中の Cx と Sh の要求 |
| MMTel AS | 扱っている呼と、答えている途中の Ut の要求 |
| IP-SM-GW | 手元にある配達の処理 |
| サービスセンタ（SC） | 答えている途中の OFR と ALR、リースを持っている宛先 |
| メディア中継ノード | 持っているコンテキスト |

S-CSCF の `LOCKED` は、処理中のトランザクションが無いことを言うだけです。登録はどの台も共有する保存先にあるので、S-CSCF の台は登録を利用者として数えません。メディア中継ノードの `LOCKED` は、もっと強いことを言います。このプロセスを通る RTP が、もう 1 つも無いことです。このときにノードを止めても、音声は切れません。

管理上の状態はプロセスの中にあります。起動し直したノードは `UNLOCKED` で始まります。

## 役割ごとに断るものと続けるもの

外した役割が何を断り、何を続けるかは、その役割の仕事がどこに結び付いているかで決まります。

| 役割 | 断る新しい仕事 | 続ける仕事 | 送り手が知る方法 |
| --- | --- | --- | --- |
| P-CSCF | 登録を始める REGISTER と、更新する REGISTER | この台を通って登録した UE の呼、ダイアログの中の要求、登録の解除 | 503、Retry-After 300 秒 |
| I-CSCF | すべての要求 | 転送中の INVITE への CANCEL（トランザクションが受ける） | 503、Retry-After 30 秒 |
| S-CSCF | ダイアログの外の要求（REGISTER と新しい INVITE） | ダイアログの中の要求、AS が元のダイアログの識別子を付けて送り返した要求 | 503、Retry-After 30 秒 |
| HSS | 新しい接続（待ち受けをやめる） | `SHUTTINGDOWN` のあいだは今の接続の要求。`LOCKED` で接続を閉じる | `LOCKED` になるときの DPR（Disconnect-Cause は `REBOOTING`） |
| MMTel AS | 新しい呼の INVITE、会議を作る INVITE、新しい Ut の要求 | ダイアログの中の要求、通話中の呼の転送、自分がフォーカスの会議への参加 | 503、Retry-After 30 秒 |
| IP-SM-GW | 第三者登録、UE からのショートメッセージ | delivery report、reg event の NOTIFY、SC から渡された配達 | 503、Retry-After 30 秒 |
| SC | 新しい接続（待ち受けをやめる）、自分から始める配達 | 今の接続に届く OFR と ALR、手元の配達の試み | `LOCKED` になるときの DPR |
| メディア中継ノード | コンテキストを作る `Add` | 今あるコンテキストへの `Modify`、`Subtract` など | 制御の API で 503（H.248 ではエラー 502） |

S-CSCF の判断は、要求を受けた入口の数行です。

[internal/scscf/scscf.go](../../../internal/scscf/scscf.go)

```go
func (s *SCSCF) OnRequest(st *sip.ServerTransaction) {
	s.transactions.Add(1)
	defer s.transactionEnded()
	if !s.unlocked() && !s.carriesOn(st.Request) {
		s.refuse(st)
		return
	}
	if st.Request.Method != sip.REGISTER {
		s.session(st)
		return
	}
	s.register(st)
```

どの要求も、まず処理中のトランザクションとして数えます。終わると `transactionEnded` が数を減らし、shut down を待つ側に知らせます。`UNLOCKED` でなく、要求が始めたものを続けるのでもなければ、503 で断ります。始めたものを続ける要求は `carriesOn` が見分けます。

[internal/scscf/maintenance.go](../../../internal/scscf/maintenance.go)

```go
func (s *SCSCF) carriesOn(req *sip.Message) bool {
	return ims.IsInDialog(req) || s.sentBack(req)
}
```

ダイアログの中の要求と、AS が送り返した要求です。この 2 つは、`LOCKED` になったあとも扱います。通話中の呼の BYE を断ると、その呼が正しく終わらないからです。登録の更新は、ダイアログの外の新しい REGISTER なので断ります。S-CSCF の台は登録を共有しているので、台が複数あれば、I-CSCF が断られた REGISTER を次の台へ送り（RFC 3263 4.3）、次の台がそれを受け付けます。

P-CSCF が断るのは REGISTER だけで、その中でも登録の解除は通します。呼の INVITE やダイアログの中の要求は、外しているあいだも断りません。

[internal/pcscf/pcscf.go](../../../internal/pcscf/pcscf.go)

```go
	if !p.unlocked() && procedure.kind != ims.DeRegistration {
		respond(p.refusal(req, log))
		return
	}
```

UE は、すべての要求を、自分が登録に使った P-CSCF へ送ります。そのため、P-CSCF の利用者はひとりでにはほかの台へ移りません。外した P-CSCF は、登録を始める REGISTER と更新する REGISTER を 503 で断り、UE がほかの P-CSCF で登録し直すのを待ちます。Retry-After を UE のタイマー F（128 秒）より長い 300 秒にしているのは、TS 24.229 5.1.1.2.1 が、それより長い Retry-After の 503 を受けた UE に、その P-CSCF を使えないものとして扱わせるからです。300 秒という値は仕様にはなく、この実装の選択です。

HSS は、SIP のように応答で断れません。CSCF とアプリケーションサーバーは Diameter の接続を通して HSS に届くので、HSS は接続のほうを扱います。

[internal/hss/maintenance.go](../../../internal/hss/maintenance.go)

```go
		AdministrativeStateChanged: func(_, to maintenance.AdministrativeState) {
			d.Change(to == maintenance.Unlocked, to == maintenance.Locked)
		},
```

`d` は Diameter の待ち受けを扱う部品です。1 つ目の引数は待ち受けるかどうか、2 つ目は今ある接続を閉じるかどうかです。`SHUTTINGDOWN` では新しい接続を受けるのをやめ、今ある接続はそのまま使います。`LOCKED` では、相手ごとに DPR を送って接続を閉じます。

## 5 つの仕事と 4 つの場合

この章の問いに、表で答えます。各欄の理由は、続く節で説明します。

| 仕事 | 保守で外す | 正常に止める | プロセスが消える | 保存先を失う |
| --- | --- | --- | --- | --- |
| 新しい登録 | 503 で断る。送り手が次の台へ送り直す | 受けない。送り手は答えが無いことを知ってから次の台へ | 同じ | 保存先を読めなければ 500。中身を失えば登録が消え、UE が次に REGISTER を送るまで着信が届かない |
| 新しい呼 | 503 で断る。送り手が次の台へ送り直す。AS が断ったときは、S-CSCF が次の AS を試し、無ければ iFC の DefaultHandling に従う | 受けない | 同じ | 保存先を読めなければ 500 |
| 通話中の呼 | 最後まで扱う | プロキシ（P-CSCF、S-CSCF）の状態は保存先にあり、共有なら別の台が続けられる。B2BUA（MMTel AS、IBCF）の呼はプロセスと一緒に止まる | 同じ。DPR も BYE も出ない | 記録を失った呼は、BYE が通っても、ノードが中継ノードのコンテキストなどを解放できない |
| 処理中の Diameter の要求 | `SHUTTINGDOWN` では答えてから DPR。lock と期限では答えを捨てて閉じる | 最後に DPR を送って閉じる | 接続が切れる。送り手は次の台へ送り直す | HSS は DIAMETER_UNABLE_TO_COMPLY で答える |
| 預かっているショートメッセージ | 保存先に残り、運用中の台が届ける | 試みを打ち切り、宛先をすぐ運用中の台に渡す | リースが切れたあとで、ほかの台が引き継ぐ | 預かっていたものが消える |

### 新しい登録と新しい呼

保守で外したノードは、新しい仕事を断ります。SIP では 503 に Retry-After を付けます。Retry-After は、その秒数のあいだそのサーバーにほかの要求を送らないよう求めるものです（RFC 3261 21.5.4）。503 を受けた送り手は、DNS で得た次のサーバーへ要求を送ります（RFC 3263 4.3）。

送り手がプロキシなら、受けた 503 をそのまま上流へは返しません（RFC 3261 16.7 の手順 6）。503 をそのまま返すと、そのプロキシ自身が要求を受けないと伝えることになるからです。この網では、ほかに試す台がありません。そのため UE が受け取るのは、I-CSCF が S-CSCF の 503 の代わりに返す 500 や、どの I-CSCF にも REGISTER を送れなかった P-CSCF が返す 504 です（TS 24.229 5.2.2.1 の 7 番目の項目）。

MMTel AS の 503 は、S-CSCF に返ります。S-CSCF は次の AS の台を試し、無ければ iFC の DefaultHandling に従います。SESSION_CONTINUED なら AS を通らずに呼を続け、SESSION_TERMINATED なら呼を終わらせます。

正常に止めたノードとプロセスが消えたノードは、要求に答えません。送り手は、接続できないか答えが無いことを知ってから、次の台を試します（RFC 3263 4.3）。違いは送り手が待つ時間です。断る 503 はすぐに届きますが、UDP で答えが無いときは、送り手のトランザクションのタイマーが切れるまで待ちます。

### 通話中の呼

保守で外したノードは、通話中の呼を最後まで扱います。違いが出るのは、ノードが消えたときです。それは、呼の状態がどこにあるかで決まります。

- **P-CSCF と S-CSCF はプロキシです。** 呼の扱いに要るもの（P-CSCF ならダイアログの記録と、中継ノードのコンテキストの ID、S-CSCF なら登録と iFC の評価の続き）は `Store` の後ろにあります。共有の保存先を使う構成なら、同じ名前で届く別の台が次の要求を扱えます。プロセスのメモリに置く構成では、状態はプロセスと一緒に消えます。
- **MMTel AS は B2BUA です。** 呼の 2 つのダイアログとトランザクションは、その INVITE を受けた台のプロセスの中にあります。止まると呼も止まります。同じ名前で作り直した台は、保存先の呼の記録からダイアログを作り直しません。呼の中の要求には 481（Call/Transaction Does Not Exist）を返し、当事者がそれを受けて呼を終わらせます（RFC 3261 12.2.1.2）。コードの `SIMPLIFIED:` に書いてある簡略化です。正常に止めるときも、AS は UA を閉じるだけで、呼に BYE を送りません。
- **IBCF も B2BUA です。** 違うのは、同じ名前で作り直した台が、前のプロセスが保存先に残した呼の記録を読み、2 つのダイアログの両方に BYE を送り、TrGW のコンテキストを解放することです（TS 24.229 5.10.2.4、5.10.3.4）。記録が残るのは、記録を持つ保存先が残るあいだだけです。プロセスのメモリに置く構成では、作り直した IBCF は何も終わらせず、TrGW のコンテキストは残ったままになります。
- **メディア中継ノードは、RTP のソケットを持っています。** プロセスが止まると、その呼の音声は止まります。P-CSCF はそれを知る手段を持たず、呼を切りません。同じアドレスで戻った中継ノードが保存先からコンテキストを開き直せば、音声は続きます。戻らなければ、当事者が切るまで、信号だけが続きます。次のオファーがコンテキストの無いことに気づけば、P-CSCF はそこで呼を解放します（[メディアの再交渉が失敗したとき](design-media-rollback.md)）。

### 処理中の Diameter の要求

HSS を shut down すると、`SHUTTINGDOWN` の HSS は今ある接続をそのまま持ち、届いた要求に答えます。答えている途中の要求が 0 になると `LOCKED` になり、DPR はその最後の答えのあとに出ます。DPR は、ノードが接続を切ろうとしていることを伝えるもので、持っている要求を捨てることを伝えるものではありません（RFC 6733 5.4）。

ただし、相手は止まるようにと言われていないので、要求を送り続けます。登録が続けて届くあいだは、0 にならないこともあります。そこを区切るのが期限です。期限が過ぎたときと lock を送ったときは、HSS は答えている途中の要求を残したまま接続を閉じます。送り出す途中だった答えは捨てられ、送り手には答えが届きません。

答えの来ない要求を持った送り手は、接続が切れたことを知ると、その要求を次の台へ送り直します。RFC 6733 5.5.4 の failover で、送り直す要求には T フラグを立てます。このプロジェクトの `diameter.PeerGroup` もそうします。送り直せないのは、Destination-Host で特定の台を指した要求です。UAR、LIR、MAR、未登録の利用者への SAR、Sh の要求は Destination-Realm だけを持つので、レルムのどの台に送ってもかまいません。

DPR の Disconnect-Cause は `REBOOTING` です。`REBOOTING` を受けた相手は、あとで接続し直してよいとされています（RFC 6733 5.4.3）。lock を解いた HSS は、相手の次の試行で接続を取り戻します。

正常に止めたノードも、最後に Diameter の接続を DPR で閉じます。プロセスが消えたときは DPR がありません。カーネルが TCP の接続を閉じるか、相手の監視（DWR と DWA）が応答の無いことに気づくまで、相手は接続を使い続けます。どちらの場合も、答えの来ない要求は failover で次の台へ行きます。同じ要求が 2 度届くことがあるので、受け手は End-to-End Identifier と Origin-Host で重複を見分けます（RFC 6733 5.5.4）。

HSS の保存先が読めないときは、HSS は要求ごとに DIAMETER_UNABLE_TO_COMPLY で答えます。役割全体は止めません。

### 預かっているショートメッセージ

SC が預かるショートメッセージは、宛先ごとに保存先にあります。どの台もショートメッセージを受け、どの台も届けます。1 つの台に仕事を結び付けるのは、配達を試みているあいだに持つ宛先のリースだけです。

外した SC は、試みている最中の配達を終えます。そのあと、同じ宛先の次のショートメッセージも、ほかの宛先も取りません。保存先の上で、その宛先はすぐに配達の時期になり、運用中の台が残りを届けます。`LOCKED` の SC にも、今ある接続で OFR が届けば受け、保存先に置きます。届けるのは運用中の台です。ほかの台が保存先を共有していなければ、SC は lock を解くまで預かります。

lock を送ったときと shut down の期限が過ぎたときは、ノードが接続を閉じるので、答えを待っていた TFR には答えが来ません。SC はそれを、過ぎ去るかもしれないシステムの失敗として扱い、ショートメッセージを次の再送まで持ちます。正常に止めるときは、SC は試みを打ち切り、宛先をすぐに運用中の台へ渡します。プロセスが消えたときは、リースが切れるまでその宛先を誰も取りません。切れたあとで、ほかの台が引き継ぎます。

どの場合も、途中だった試みの結果は分からないまま、ショートメッセージは保存先に残り、もう一度送られます。ところが相手のノードはもう届けていたかもしれず、受信者は同じショートメッセージを 2 度受け取ることがあります。その扱いは [SMS の再送と重複](design-sms-duplicates.md) で見ます。

保存先の中身を失えば、預かっていたショートメッセージは消えます。誰も届けず、送信者に結果も伝わりません。

## 正常な停止はドレインではない

ノードのプロセスに SIGTERM を送ると、ノードは次の順に止まります。

[cmd/ims/main.go](../../../cmd/ims/main.go)

```go
func stopNode(work, stacks []func(), stopDiameter func()) {
	var stopping sync.WaitGroup
	for _, stop := range work {
		stopping.Go(stop)
	}
	stopping.Wait()
	for _, stop := range stacks {
		stop()
	}
	stopDiameter()
}
```

`work` は、役割が自分から行う仕事です。登録の期限の掃除と、SC の配達です。まずこれを止め、すべてが止まるのを待ちます。掃除は新しい期限を取らず、取った期限の NOTIFY と SAR を送り終えます。このあと時刻の来る期限は、ほかの台のものになります。SC は試みを打ち切ります。次に SIP のスタックを閉じます。実行中のトランザクションは終わり、役割はそれを相手の答えが無かったものとして扱います。最後に Diameter の接続を閉じます。SIP で届いた要求が、それまで Diameter を使っているかもしれないからです。

この順は、台が共有する状態を壊さないための順です。しかし、仕事を最後まで続ける順ではありません。正常な停止は新しい仕事を断らず、通話中の呼を待ちません。仕事を終えてから止めたいときは、まず shut down を送り、`LOCKED` になるのを待ってから止めます。

## 選べる方法を比べる

ノードを外すときの方法は 4 つに分けられます。この実装は、役割ごとにこれらを組み合わせています。

| 方法 | すること | 合う条件 | 失うもの | この実装で使う役割 |
| --- | --- | --- | --- | --- |
| ドレイン | 新しい仕事を断り、今の仕事が終わるのを待ってから止める | 仕事が台に結び付いていて、ほかの台へ移せない | 時間に上限が無い。P-CSCF は UE が登録し直すまで、AS は呼が切れるまで待つ。期限で区切ると、残った仕事は切られる | P-CSCF、MMTel AS、メディア中継ノード |
| 新しい仕事を断るだけ | 断ればすぐに止められる | 台が自分のものを持たない。持つものがすべて共有の保存先にある | 断られた送り手が送り直す時間 | I-CSCF、S-CSCF |
| 引き継ぐ | 仕事の状態をほかの台が読み、続ける | 状態が共有の保存先にあり、どの台も同じ名前で届く | 引き継ぐ前の途中の試みは、やり直しになる。重複が起きうる | SC、IP-SM-GW、S-CSCF の登録の期限 |
| 送り手に送り直させる | 断るか接続を閉じ、送り手が次の台を選ぶ | 送り手が次の台を知っている（DNS の SRV、ピアの一覧、中継ノードの一覧） | 次の台が無ければ、失敗が利用者に見える | SIP の 503、Diameter の DPR、中継ノードの `Add` の 503 |

**新しい仕事を断るだけで足りる**のは、I-CSCF と S-CSCF です。I-CSCF は要求と要求のあいだに何も持たず、Record-Route にも入らないので、あとの要求が戻ってきません。S-CSCF は登録を保存先で共有しているので、自分の台に結び付いているのは処理中のトランザクションだけです。

**ドレインが要る**のは、仕事がその台から動かせない役割です。メディア中継ノードのコンテキストは、そのポートを開いたプロセスの中にしかありません。MMTel AS の呼は、その台のプロセスの中のダイアログです。P-CSCF の利用者は、UE が自分で登録し直すまで動きません。

**引き継ぐ方法**は、状態が保存先にあり、どの台からも読めることが前提です。B2BUA の呼をほかの台が引き継ぐには、知らないダイアログを受け入れ、前のプロセスが使った CSeq より大きい番号で続け、両方の脚の中継を組み直すことになります。この実装の MMTel AS と IBCF は、そこまでしません。IBCF は、前のプロセスの呼を引き継がずに終わらせます。

**送り手に送り直させる方法**では、何で断るかに標準の制約があります。HSS が新しい要求すべてに DIAMETER_TOO_BUSY を返す作りも考えられますが、この答えを使ってよいのは、特定のサーバーを指した要求をそのサーバーが扱えないときだけです（RFC 6733 7.1.3）。HSS への要求の多くはホストを指さないので、この答えは誤りになります。そのため、HSS と SC は DPR で接続を閉じます。

## この実装の選択と限界

- **HSS と SC は、外しているあいだポート 3868 で待ち受けません。** RFC 6733 2.1 は、ノードがいつでもこのポートで接続を受けられることを求めています。この実装は、運用から外しているあいだに限って、そこから外れます。先に送った `REBOOTING` は、ノードがこれから再起動することを表し（5.4.3）、再起動中のノードも待ち受けないので、接続を断られて Tc のあとにもう一度試すことは、相手が見込んでいる動きだからです。標準を満たした選択ではなく、意図して外れた選択です。
- **メディア中継ノードは、外したことをコントローラに知らせません。** 実際の IMS-AGW と MRFP は、`GRACEFUL` の ServiceChange でコントローラに知らせます。ここでは、コントローラが次の `Add` を断られて気づきます（コードの `SIMPLIFIED:`。TS 29.334 5.17.3.2、TS 23.333 8.30）。
- **MMTel AS は、作り直した台で前のプロセスの呼を続けません。** 呼の中の要求には 481 を返します（コードの `SIMPLIFIED:`。RFC 3261 12.2.2）。
- **IBCF が前のプロセスの呼を終わらせるのは、同じ名前で戻った台だけです。** 戻らない台の記録は、保存先に残ります（コードの `SIMPLIFIED:`。TS 24.229 5.10.2.4、5.10.3.4）。
- **管理上の状態はプロセスの中にあります。** 外したノードを作り直すと、`UNLOCKED` で起動し、すぐに新しい仕事を受けます。
- **この網には、どの役割も 1 台です。** 断られた送り手には次の台が無いので、外しているあいだ、利用者には失敗が見えます。
- **Compose の場面で確かめているのは、仕事の無いときの動きです。** `scripts/e2e-maintenance-sip.sh`、`scripts/e2e-maintenance-hss.sh`、`scripts/e2e-maintenance-sms.sh` は、処理中のものが無い状態でノードを外し、すぐに `LOCKED` になったノードが新しい仕事を断ることと、lock を解いたあとに戻ることを、キャプチャで確かめます。`scripts/e2e-fault.sh` は、登録も呼も持たない MMTel AS のノードを止め、殺し、作り直し、障害注入の仕組みが働くことを確かめます。仕事が残っているときの動きは、次の Go の試験が確かめています。

## この説明の根拠

この章の説明は、次のコードと試験に照らして確かめたものです。どの試験も、このリポジトリの Go の試験として通ることを確かめています。Compose の場面は読んだだけで、この章のために動かしてはいません。

- `internal/maintenance/state_test.go` の `TestShutDownWaitsForTheLastUserToQuit`、`TestShutDownWithoutUsersLocksAtOnce`、`TestShutDownDeadlineLocksWithUsersLeft`。利用者が残れば `SHUTTINGDOWN` で、最後の利用者が去ると `LOCKED` になること、利用者がいなければすぐ `LOCKED` になること、期限が過ぎると利用者を 1 人残したまま `LOCKED` になることを確かめています。
- `internal/scscf/maintenance_test.go` の `TestAShutDownSCSCFRefusesOnlyWhatIsNew` と `TestAShutDownWaitsForTheTransactionsInProgress`。新しい REGISTER と INVITE が 503 と Retry-After 30 を受け、ダイアログの中の BYE は通ること、処理中の REGISTER が答えを受けてから `LOCKED` になることを確かめています。
- `internal/pcscf/maintenance_test.go` の `TestAShutDownPCSCFRefusesOnlyTheRegistrations`。REGISTER が 503 と Retry-After を受けて転送されず、登録済みの UE の要求と登録の解除は通り、IP の対応付けが消えると `LOCKED` になることを確かめています。
- `internal/icscf/maintenance_test.go` の `TestAShutDownICSCFRefusesEveryRequest`。どの要求も 503 を受け、何も転送されないことを確かめています。
- `internal/hss/maintenance_test.go` の `TestPeerGroupMovesToTheSecondHSSWhenTheFirstIsDrained` と `TestShutDownWaitsForTheRequestsItTook`。外した HSS が DPR で接続を閉じ、クライアントの次の要求が 2 台目に行き、lock を解くと 1 台目に戻ること、shut down の前に受けた要求に答えてから接続を閉じることを確かめています。
- `internal/mmtel/maintenance_test.go` の `TestAShutDownASRefusesANewCall`、`TestAShutDownASServesTheDiversionOfACallInProgress`、`TestAShutDownWaitsForTheCallsOfTheAS`。新しい呼を断り、通話中の呼の転送は扱い、呼が終わるまで `SHUTTINGDOWN` のままでいることを確かめています。
- `internal/smsc/maintenance_test.go` の `TestAShutDownSCFinishesTheAttemptInHandAndLeavesTheQueue` と `TestALockedSCLeavesEveryDeliveryToAnInstanceInService`。手元の試みを終えてから `LOCKED` になり、残りをほかの台が届けること、`LOCKED` の SC が OFR と ALR を受けても自分では届けないことを確かめています。
- `internal/agw/maintenance_test.go` の `TestAShutDownRelayMakesNoContext`、`TestTheDrainWaitsForTheContextsOfTheRelay`、`TestTheContextsOfAnEarlierProcessCountAsUsers`。中継ノードが新しいコンテキストを作らず、今あるコンテキストが解放されるまで `SHUTTINGDOWN` でいること、保存先から開き直したコンテキストも利用者に数えることを確かめています。
- `cmd/ims/stop_test.go` の `TestAnExpiryThatComesWhileANodeStopsGoesToAnotherInstance`。止まりかけた S-CSCF の台が期限を取らず、同じ保存先のほかの台がその期限を処理することを確かめています。
- `internal/ibcf/network_test.go` の `TestRestartedInstanceEndsTheCallsItLeft`。起動し直した IBCF の台が、前のプロセスの呼の両方のダイアログに BYE を送り、TrGW のコンテキストを解放することを確かめています。
- `internal/ibcf/storefailure_test.go` の `TestACallGoesOnWhenItsRecordCannotBeWritten` と `TestAnInstanceThatCannotReadItsCallsDoesNotStart`。応答のあとの記録が書けなくても呼が続くことと、前のプロセスの記録を読めない台は起動しないことを確かめています。
- `internal/scscf/storefailure_test.go` と `internal/pcscf/storefailure_test.go` の `TestRegisterWhenTheStoreFails`、`internal/pcscf/storefailure_test.go` の `TestInviteWhenTheStoreFails`。保存先が使えないとき、REGISTER と、UE からの INVITE と UE への INVITE が 500 を受け、先へ進まないことを確かめています。
- `internal/hss/storefailure_test.go` の `TestRequestsWhoseSubscriberCannotBeRead`。保存先を読めない HSS が、各要求に DIAMETER_UNABLE_TO_COMPLY で答えることを確かめています。

## 自分で動かす場合

この章を読むのに、構成を動かす必要はありません。各ノードを運用から外す手順と、キャプチャで見えるものは、手順書の次の節にあります。[S-CSCF を運用から外す](../../guide/ja/registration.md#s-cscf-を運用から外す)、[P-CSCF を運用から外す](../../guide/ja/registration.md#p-cscf-を運用から外す)、[I-CSCF を運用から外す](../../guide/ja/registration.md#i-cscf-を運用から外す)、[HSS を運用から外す](../../guide/ja/registration.md#hss-を運用から外す)、[MMTel AS を運用から外す](../../guide/ja/mmtel.md#mmtel-as-を運用から外す)、[IP-SM-GW を運用から外す](../../guide/ja/sms.md#ip-sm-gw-を運用から外す)、[サービスセンタを運用から外す](../../guide/ja/sms.md#サービスセンタを運用から外す)、[メディア中継ノードを運用から外す](../../guide/ja/call.md#メディア中継ノードを運用から外す) です。IBCF の台が起動し直したときの動きは [IBCF の台が起動し直したとき](../../guide/ja/interconnect.md#ibcf-の台が起動し直したとき) にあります。ノードを止める、殺す、作り直す障害の入れ方は [deploy/chaos の README](../../../deploy/chaos/README.ja.md) にあります。
