# メディアの再交渉が失敗したとき

通話中の re-INVITE や UPDATE は、新しいオファーで音声の行き先を変えようとします。P-CSCF は、そのオファーを先へ渡す前に、メディア中継ノードの送り先をオファーのアドレスに変えます。ところがオファーが 2xx 以外の最終応答で終わると、SIP のセッションは要求の前のままです（RFC 3261 14.1、RFC 3311 5.3）。中継ノードだけが新しいアドレスへ送っている状態が残ります。

この実装の答えは次のとおりです。P-CSCF は、2xx 以外の応答をオファーした側へ渡す前に、中継ノードを最後に成立したオファーとアンサーのアドレスへ戻します。中継ノードを戻せないとき、または中継ノードが本当に変えたのかが分からないときは、SIP の合意とメディアの経路が食い違ったままになります。そのときは、両方の当事者に BYE を送って呼を終わらせます。

この章では、失敗を 4 つに分けます。オファーが断られたとき、応答が来ないとき、中継ノードが変えたかどうか分からないとき、戻す操作そのものが失敗したときです。そのうえで、先に戻す、呼を終える、新しい設定のままにする、の 3 つの方法を比べます。

## この章で扱う構成と前提

例にするのは `deploy/compose` の構成で、P-CSCF が IMS-ALG として、メディア中継ノード（IMS-AGW）を制御します。P-CSCF は中継ノードに、呼ごとのコンテキストを `Add` で作らせ、ターミネーションの送り先を `Modify` で伝え、`Subtract` で解放させます。アクセス側のターミネーションは UE に面し、コア側のターミネーションは網の側に面します。`deploy/interconnect` の IBCF と TrGW も、同じ規則で動きます。違うところは章の後半で述べます。

オファーとアンサー、re-INVITE、信頼性のある暫定応答と PRACK は、[通話のトランザクション、ダイアログ、メディアの交渉](call-signaling.md) で扱います。IBCF と TrGW は [網間接続](interconnect.md) で、P-CSCF の台が共有する保存先は [複数台と障害で引き継ぐもの](design-shared-state.md) で扱います。

この章が引く仕様は RFC 3261、RFC 3262、RFC 3264、RFC 3311、RFC 6141、TS 24.229 V18.11.0 です。表の中のポート A、B は説明のための例で、キャプチャの値ではありません。

## 合意と中継ノードの設定が同時に決まらない理由

SIP の合意は、アンサーが届いたときに成立します。re-INVITE のオファーへのアンサーは 2xx か、信頼性のある暫定応答に入ります（RFC 3261 13.2.1、RFC 3262 5）。中継ノードの設定は、P-CSCF が `Modify` を送ったときに変わります。こちらは中継ノードへの別の要求です。2 つは別のノードで、別の時刻に起きます。

P-CSCF は、届いたオファーをそのまま先へ渡しません。IMS-ALG は、中継ノードのアドレスを書いた自分のオファーを作って渡します（TS 24.229 6.7.2.1）。この実装はそのときに、オファーした側に面したターミネーションへ、オファーのアドレスを `Modify` で伝えます。そのため、オファーが P-CSCF を通ってから応答が戻るまでのあいだ、中継ノードは合意より一歩先にいます。

オファーした側にとって、この先回りは問題になりません。RFC 3264 8.3.1 は、ポートを変えるオファーを送った側に、オファーを送った時点から古いポートと新しいポートの両方で受けられるよう求めています。困るのはオファーが断られたときです。同じ節は、オファーが断られたら、断られたことを受け取った時点で新しいポートの用意をやめてよいとしています。断られた応答が UE に届いたあとも中継ノードが新しいポートへ送り続けると、UE はその音声を受けないかもしれません。SIP の信号には何の誤りも出ないまま、片方向の音声が消えます。

## 3 つの状態を分ける

この問題を考えるには、次の 3 つを分けておく必要があります。

| 状態 | 意味 | P-CSCF での置き場 | IBCF での置き場 |
| --- | --- | --- | --- |
| 最後に成立したオファーとアンサー | SIP のうえで、各当事者がメディアを受けるアドレス | ダイアログの記録の `AccessRemote` と `CoreRemote`。台が共有する保存先にある | 呼の `agreed`。台のプロセスのメモリにある |
| 提案中のオファー | 送ったが、まだアンサーの無いオファーのアドレス | その要求を扱う処理の中の `offered`。信頼性のない暫定応答の SDP は `previewed` | 要求そのものと `previews` |
| 中継ノードのいまの設定 | 各ターミネーションが実際に送っている先 | 中継ノードの中だけ。P-CSCF は尋ねられない | TrGW の中。IBCF は写しを `sends` に持つ |

UE がポート A で受けている通話で、UE がポート B へ移る re-INVITE を送り、相手が 488 で断った場合を、時刻の順に並べます。アクセス側のターミネーションだけを見ます。

| 時点 | 最後に成立した | 提案中 | 中継ノードの設定 |
| --- | --- | --- | --- |
| re-INVITE の前 | A | なし | A |
| re-INVITE が P-CSCF を通った | A | B | B（オファーで `Modify`） |
| 488 が P-CSCF に届いた | A | B（ここで終わる） | B → A（戻す `Modify`） |
| 488 が UE に届いた | A | なし | A |

3 行目と 4 行目の順が大切です。中継ノードを戻すのは、488 を UE へ渡す前です。UE は 488 のすぐあとに次のオファーを送るかもしれません。そのオファーが届くときには、中継ノードはもう元に戻っています。

最後に成立したアドレスは、P-CSCF ではダイアログの記録 `RelayContext` にあります。

[internal/pcscf/dialog.go](../../../internal/pcscf/dialog.go)

```go
	// AccessRemote and CoreRemote are where the access and the core
	// termination send for the last offer and answer that are done: where
	// the UE, and the party on the network side, take their media. A
	// request whose offer gets no 2xx leaves the session as it was before
	// the request (RFC 3261 14.1, RFC 3311 5.3), and the termination that
	// the offer moved goes back there (goBack). The relay cannot be asked,
	// and the next request of the dialog may pass another P-CSCF
	// instance, so the Dialog keeps them. Not valid in a Dialog of a
	// P-CSCF that kept neither.
	AccessRemote netip.AddrPort
	CoreRemote   netip.AddrPort
```

コメントが、この値を保存先に置く理由を 2 つ挙げています。1 つは、中継ノードの制御の操作が `Add`、`Modify`、`Subtract` だけで、いまの設定を尋ねる操作が無いことです。もう 1 つは、同じダイアログの次の要求が、別の P-CSCF の台を通るかもしれないことです。どの台が応答を扱っても戻せるように、戻す先を台の外に置きます。

この値が書き換わるのは、アンサーが成立したときだけです。P-CSCF が応答の SDP を中継ノードに通した直後の処理です。

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
		taken = true
		if resp.StatusCode < 200 && !resp.Requires(sip.Option100rel) {
			previewed = sent
		} else if !settled && d.Relay != nil {
			settled = true
			_, _, err := p.store.UpdateDialog(context.Background(), d.Key, func(d *Dialog) {
				if d.Relay != nil {
					d.Relay = d.Relay.agreeing(facingSender(toUE), offered).agreeing(facingSender(!toUE), sent)
				}
			})
			if err != nil {
				// The response goes on; a later offer that gets no 2xx puts
				// the relay back where the offer and answer before this one
				// said.
				log.Warn("the dialog keeps the offer and answer before this one: the store failed", "error", err.Error())
			}
		}
```

入力は、要求への応答 `resp` と、その SDP を中継ノードに通した結果 `sent`（答えた側がメディアを受けるアドレス）です。信頼性のない暫定応答の SDP は、アンサーではありません（RFC 3261 13.2.1）。同じアンサーを先に見せているだけなので、`previewed` に覚えるだけで、成立したものにはしません。2xx か信頼性のある暫定応答なら、オファーのアドレス `offered` と答えた側のアドレス `sent` を、成立したアドレスとして保存先に書きます。書き込みに失敗しても、応答は先へ進みます。その場合、このあとのオファーが断られると、中継ノードはこの 1 つ前の成立したアドレスへ戻ります。これはこの実装の限界の 1 つです。

## 標準が決めていることと、決めていないこと

標準が決めていることは、セッションの状態です。

- 2xx 以外の最終応答を受け取ると、セッションは要求の前のままです（re-INVITE は RFC 3261 14.1、UPDATE は RFC 3311 5.3）。
- 408 か 481 を受け取ったとき、または応答がまったく来なかったとき、UAC はダイアログを終わらせるべきです（RFC 3261 12.2.1.2。14.1 と RFC 3311 5.3 もこれを繰り返しています）。
- アンサーは 2xx か信頼性のある暫定応答に入ります。同じアンサーを、それより前の暫定応答に入れてもかまいません（RFC 3261 13.2.1）。
- re-INVITE の中で変更をもう実行した UAS は、誤りの応答ではなく 2xx を返すべきです（RFC 6141 3.3）。それでも誤りの応答を受け取った UAC は、両者のセッションの見方をそろえるために、新しいオファーを送ります（RFC 6141 3.4）。
- プリコンディションを使う UE は、セッションの変更に必要な資源を網が確保できないとき、CANCEL で変更をやめます（TS 24.229 6.1.4.2 の項目 b）。取り消された re-INVITE も、2xx 以外の応答で終わります。

決めていないのは、中継ノードの扱いです。TS 24.229 6.7.2.1 は、IMS-ALG がオファーを受けたら中継ノードのアドレスで新しいオファーを作るよう定めていますが、そのオファーが断られたときの手順はありません。中継ノードの送り先を、オファーのときに変えるかアンサーのときに変えるかも、決めていません。中継ノードが答えないときにどうするかも同じです。これらはこの実装が選ぶことです。

呼を終わらせる手順は TS 24.229 にあります。P-CSCF が既存のセッションを解放するときの BYE の作り方は 5.2.8.1.2 にあります。ただし、5.2.8.1.2 が解放のきっかけとして挙げるのは、無線や bearer の資源を失ったとき、信号の bearer を失ったとき、応答に入ったオファーがローカルポリシーに合わないとき、の 3 つです。中継ノードの失敗はその中にありません。この場合に 5.2.8.1.2 の BYE を使うのは、この実装の選択です。IBCF には別の規定があります。伝送の面を制御する IBCF は、TrGW などから伝送の面の誤りを知らされたら、両側に BYE を送ってよい、とされています（5.10.2.4、5.10.3.4）。

## オファーが断られたとき

相手が 488 で断るときも、UE が取り消して相手が 487 を返すときも、P-CSCF から見ると同じ「2xx 以外の最終応答」です。P-CSCF は、ダイアログの中の要求を転送したあと、最終応答を次の順に扱います。

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
	final, err := st.ProxyWith(out, relay, targets...)
	if final != nil {
		// Proxy passes on a 2xx to an INVITE itself, and leaves every
		// other final response to its user.
		relay(final)
	}
	unanswered(final, err)
	ims.Finish(st, final, err)
	answered(final, err)
```

`unanswered` が中継ノードを戻し、`ims.Finish` が応答をオファーした側へ渡し、`answered` が必要なら呼を解放します。戻す処理が、応答を渡す処理より前にあります。`unanswered` の中身は次のとおりです。

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
	unanswered = func(final *sip.Message, err error) {
		// A 2xx comes as final, or, for a re-INVITE, goes through Proxy,
		// which then returns neither a final response nor an error.
		answeredWith2xx := err == nil && (final == nil || final.StatusCode < 300)
		if settled || refused || answeredWith2xx {
			return
		}
		// The session is as it was before the request (RFC 3261 14.1), on
		// both sides when an answer showed in a provisional response only.
		stuck = !p.goBack(d.Relay, facingSender(toUE), offered, log) ||
			!p.goBack(d.Relay, facingSender(!toUE), previewed, log)
	}
```

何もしない場合が 3 つあります。2xx で終わったとき、アンサーがもう成立していたとき（`settled`）、P-CSCF が呼の解放をもう決めていたとき（`refused`）です。2 つ目は、re-INVITE への信頼性のある 183 にアンサーが入っていた場合です。そのオファーとアンサーは終わっています。RFC 6141 3.3 は、このように変更を実行した UAS に 2xx を返すよう求めていますが、それでも re-INVITE が 488 で終わることはあります。そのときも戻すものはありません。両者の見方をそろえるのは、UAC が送る次のオファーです（RFC 6141 3.4）。

それ以外では、オファーした側に面したターミネーションを `offered` から戻します。信頼性のない暫定応答の SDP で動かしたもう一方のターミネーションも、`previewed` から戻します。どちらかが戻らなければ `stuck` が真になります。

戻す操作は `goBack` です。

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
func (p *PCSCF) goBack(rc *RelayContext, side agw.TerminationID, offered netip.AddrPort, log *slog.Logger) bool {
	if rc == nil {
		return true
	}
	agreed := rc.agreed(side)
	if !offered.IsValid() || !agreed.IsValid() || offered == agreed {
		return true
	}
	relay, err := p.relayOf(*rc)
	if err == nil {
		ctx, cancel := relayContext()
		err = relay.Modify(ctx, rc.ID, side, agw.TerminationRequest{Remote: agreed})
		cancel()
	}
	if err != nil {
		log.Warn("the media relay does not go back to the last offer and answer: the P-CSCF releases the dialog",
			"context", rc.ID, "termination", string(side), "error", err.Error())
		return false
	}
	log.Info("the media relay sends where the last offer and answer said again", "context", rc.ID, "termination", string(side), "remote", agreed.String())
	return true
}
```

入力は、ダイアログの記録にある中継ノードのコンテキスト `rc`、戻すターミネーション `side`、オファーが動かしたアドレス `offered` です。最初の条件で、戻す必要の無い場合を除きます。オファーがアドレスを変えなかったとき（ポートを変えない保留など）は、`offered` と成立したアドレスが同じなので、`Modify` を送りません。必要なときだけ、成立したアドレスを `Remote` にした `Modify` を 1 回送ります。この `Modify` は、オファーのときと同じく、中継ノードへの要求ごとの期限の中で答えを待ちます。期限は `agw.RequestTimeout` の 2 秒です。

もう 1 つの断り方があります。オファーが P-CSCF を通るときに、中継ノード自身がオファーの `Modify` を断る場合です。中継ノードが誤りで答えたときの約束は、制御の API が決めています。

[internal/agw/api.go](../../../internal/agw/api.go)

```go
// ErrRejected marks the error with which a media node answered a
// request, over the control API or in an H.248 Error descriptor. A
// media node of this project carries out none of a request that it
// answers with an error, so its controller knows that nothing changed.
// Any other error, such as a request that got no answer in time, leaves
// the controller unable to tell whether the node carried it out.
var ErrRejected = errors.New("agw: the media node rejected the request")
```

このプロジェクトのメディアのノードは、誤りで答えた要求を何も実行しません。そのため、`ErrRejected` の誤りなら、制御する側は何も変わっていないと分かります。P-CSCF は中継ノードのアドレスを書いたオファーを作れないので、要求を先へ渡さず、自分で 500 を返します（RFC 3261 21.5.1）。中継ノードは何も変えていないので、戻すものもありません。呼は最後のオファーとアンサーのまま続きます。

## 応答が来ないとき

re-INVITE や UPDATE に最終応答が来ないときも、セッションは要求の前のままです。P-CSCF では、`unanswered` が最終応答の代わりに誤り `err` を受け取ります。`answeredWith2xx` は偽になるので、中継ノードは断られたときと同じように戻ります。そのあと `ims.Finish` が、オファーした側へ 504（Server Time-out）を返します。この実装の CSCF は、次のノードが答えなかった要求に 504 を返します。

呼が続くかどうかは、オファーした側が何を受け取ったかで決まります。

- 504 を受け取ったなら、それは普通の 2xx 以外の応答です。セッションは前のままで、ダイアログも続きます。
- 自分のトランザクションが時間切れになったとき、または 408 か 481 を受け取ったときは、UAC がダイアログを終わらせます（RFC 3261 12.2.1.2）。この実装の UA は、そのとき BYE を送ります（`internal/sip/indialog.go` の `lost`）。

どちらでも、P-CSCF はもう中継ノードを戻しています。呼が続けば、メディアは最後のオファーとアンサーのとおりに流れます。呼が終われば、BYE が P-CSCF を通るときに、P-CSCF はコンテキストを解放します。

## 中継ノードが変えたかどうか分からないとき

オファーの `Modify` に、中継ノードが時間内に答えないことがあります。答えたが P-CSCF が読めないこともあります。このとき中継ノードは、`Modify` を実行したかもしれません。実行していれば、ターミネーションは、オファーした側がこのあと受けなくなるアドレスへ送っています。中継ノードが、呼のコンテキストやターミネーションを無くしたと答えることもあります。保存先を持たずに起動し直した中継ノードがそうです。このときは、呼のメディアはもう中継ノードを通っていません。

P-CSCF は、要求にどう答え、呼を残すかどうかを、`notRelayed` で決めます。

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
	resp := refusalOf(st.Request, err, "")
	keep := !mediaGone(err) && (errors.Is(err, errBody) || errors.Is(err, agw.ErrRejected))
	if keep && st.Request.Method != sip.PRACK {
		log.Warn("the SDP of the request is not pointed at the media relay: the request goes no further", "status", resp.StatusCode, "error", err.Error())
		st.Respond(resp)
		return
	}
	log.Warn("the SDP of the request is not pointed at the media relay: the P-CSCF releases the dialog", "status", resp.StatusCode, "error", err.Error())
	st.Respond(resp)
	p.release(d.Key, SDPNotRelayed, false, log)
```

入力は、オファーを中継ノードに通せなかった理由 `err` です。呼を残す（`keep`）のは、中継ノードが何も変えていないと分かる場合だけです。P-CSCF が SDP を読めず、中継ノードに何も頼まなかったとき（`errBody`）と、中継ノードが誤りで答えたとき（`ErrRejected`）です。ただし、コンテキストやターミネーションが無いという誤り（`mediaGone`）は、誤りで答えていても除きます。メディアはもう無く、このあとのオファーもすべて同じように断られるからです。PRACK も除きます。UAC は失敗した PRACK を送り直さないので（RFC 3262 4）、信頼性のある応答が確認されないまま残るためです。

それ以外では、P-CSCF は要求に 500 などで答えてから、呼を解放します。解放では、両方の当事者に BYE を送り、PCRF のセッションと中継ノードのコンテキストを解放します。

応答に入ったアンサーを中継ノードが受け取らないときも、呼は終わります。ただし順が違います。アンサーは誰も断れません。答えた側はもうオファーを受け入れているからです。P-CSCF は 2xx をそのまま先へ渡し、re-INVITE ならその ACK のあと、UPDATE なら 2xx のあとに、両方へ BYE を送ります。

IBCF も同じ線を引きます。

[internal/ibcf/call.go](../../../internal/ibcf/call.go)

```go
func callStays(err error) bool {
	switch {
	case errors.Is(err, errMalformedSDP), errors.Is(err, errNotKept):
		return true
	case errors.Is(err, agw.ErrNoContext), errors.Is(err, agw.ErrNoTermination):
		return false
	}
	return errors.Is(err, agw.ErrRejected)
}
```

IBCF に独自なのは `errNotKept` です。TrGW はコンテキストを作れたのに、それを持つ呼の記録を IBCF が保存先に書けなかった場合です。コンテキストは呼に残っているので、呼は続けられます。

## 戻す操作そのものが失敗したとき

相手がオファーを断り、P-CSCF が戻す `Modify` を送ったのに、中継ノードがそれを受け取らないことがあります。ターミネーションは、オファーした側がもう受けないアドレスへ送り続けます。`goBack` は偽を返し、`unanswered` が `stuck` を真にします。

P-CSCF は、まず 2xx 以外の応答をそのままオファーした側へ渡します。応答そのものは正しいからです。そのあとで `answered` が `stuck` を見て、呼を解放します。両方の当事者に BYE が届き、PCRF のセッションと中継ノードのコンテキストが解放されます。

## 4 つの失敗のまとめ

| 失敗 | 中継ノードの設定 | 戻す先 | 呼 |
| --- | --- | --- | --- |
| 相手がオファーを断った（488、取り消しの 487 など） | オファーのアドレスに動いている | 最後に成立したアドレス。応答を渡す前に戻す | 続く |
| 中継ノードがオファーの `Modify` を断った（PRACK を除く） | 変わっていない | 無い。P-CSCF が 500 を返し、要求は先へ進まない | 続く |
| 最終応答が来ない | オファーのアドレスに動いている | 最後に成立したアドレス。そのあと 504 を返す | 504 なら続く。UAC が時間切れか 408、481 を受け取れば UAC が終わらせる |
| 中継ノードが時間内に答えない、または読めない答え | 分からない | 決められない | P-CSCF が 500 を返し、両方へ BYE |
| 中継ノードがコンテキストを無くした | メディアが無い | 無い | P-CSCF が 500 を返し、両方へ BYE |
| 中継ノードが応答のアンサーを受け取らない | 答えた側に面した側が分からない | 決められない | 2xx を渡してから、両方へ BYE |
| 戻す `Modify` が失敗した | オファーのアドレスのまま | 戻せなかった | 2xx 以外の応答を渡してから、両方へ BYE |

呼が続くのは、中継ノードの設定が最後に成立したオファーとアンサーに一致していると P-CSCF に分かる場合です。一致させられないか、一致しているかが分からなければ、呼を終わらせます。

## 3 つの方法を比べる

断られたオファーのあとで中継ノードをどう扱うかには、いくつかの方法があります。どれも上の標準の規則には反しません。違うのは、何を守り、何を失うかです。

| 方法 | 守れるもの | 失うもの、要るもの | 合う条件 |
| --- | --- | --- | --- |
| 先に戻す（この実装） | 呼と、SIP の合意どおりのメディア | 失敗のときに中継ノードへの要求が 1 回増え、応答はその答えを待つ（最長 2 秒）。成立したアドレスを、応答を扱うどの台でも読めるように持つ | 中継ノードが答え、何もしなかったときにはそうと言える。制御する側がダイアログごとに成立したアドレスを持てる |
| 呼を終える | メディアが合意と食い違ったまま残らない。成立したアドレスを持たなくてよい | 普通の拒否や取り消しで呼が切れる。SIP では「何も変わらない」はずの応答が、通話の切断になる | 中継ノードの設定が分からないか、戻せないときだけ |
| 新しい設定のままにする | 中継ノードへの要求が増えない | オファーした側が新しいアドレスで受けるのをやめると、片方向の音声が黙って消える | オファーがアドレスを変えていないとき。または、ターミネーションが SDP ではなく届いたパケットの送信元へ送るとき |

**呼を終える方法**を、すべての失敗に使うのは適しません。プリコンディションを使う UE は、資源を確保できなかった re-INVITE を取り消します（TS 24.229 6.1.4.2 の項目 b）。その 487 のたびに呼を切ると、資源が足りないことが、通話の切断として利用者に見えます。この実装は、呼を終える方法を、中継ノードの状態が合意と一致しないか、一致しているかが分からない場合に限っています。

**新しい設定のままにする方法**が安全なのは、限られた場合です。オファーがアドレスを変えていなければ、戻すものはありません。`goBack` は、そのときは `Modify` を送りません。届いたパケットの送信元へ送る中継ノード（ラッチング）なら、SDP のアドレスへの依存は小さくなります。しかし、この実装のコア側のターミネーションはラッチングをせず、最新の SDP のアドレスへ送ります。網の側に面したこのターミネーションでは、戻すことが音声を正しく保ちます。

もう 1 つ、**アンサーが来てから中継ノードを変える方法**も考えられます。オファーのときは中継ノードを動かさず、アンサーが通るときに両方のターミネーションを変えます。断られたときに戻すものはありません。RFC 3264 8.3.1 は、オファーした側に、アンサーを受け取って新しいポートに音声が届くまで古いポートで受け続けるよう求めているので、そのあいだ古いアドレスへ送ることも規則に合います。代わりに、中継ノードの失敗がアンサーの通る時点に集まります。アンサーは誰も断れないので、そこで中継ノードが失敗すれば、呼を終えるしかありません。オファーのときに動かす方法なら、中継ノードの失敗の多くは、オファーを 500 で断るだけで済みます。この実装は中継ノードについてこの方法を取りませんが、PCRF にはこれに近い扱いをしています。P-CSCF は、変わったメディアを PCRF に、オファーのときではなく、応答の SDP を中継ノードに通したときに伝えます。そのため、SDP の入った応答の無いまま断られたオファーでは、PCRF に戻すものはありません。

中継ノードに、いまの設定を尋ねてから決める方法もあります。分からない場合を減らせます。しかし、この実装の制御の操作には、ターミネーションの設定を読むものがありません。尋ねるあいだ、応答も待たせることになります。

## この実装の選択と限界

**P-CSCF。** オファーが通るときに中継ノードを動かし、2xx 以外の応答を渡す前に戻します。成立したアドレスは、ダイアログの記録と一緒に、台が共有する保存先に置きます。re-INVITE が 1 台目を通り、その次の UPDATE が 2 台目を通っても、2 台目が戻せます。限界は次のとおりです。

- 成立したアドレスを保存先に書けなかったときは、そのあと断られたオファーで、1 つ前の成立したアドレスへ戻ります。
- 中継ノードが答えないときに、中継ノードの状態を確かめる手段がありません。呼を終わらせます。
- 中継ノードの失敗で呼を終わらせるときの BYE は 5.2.8.1.2 の手順ですが、その節はこのきっかけを挙げていません。この実装の選択です。BYE の Reason は `SIP;cause=503;text="Service Unavailable"` です。
- PCRF が変わったメディアを断ったときは、P-CSCF はログに残すだけです。呼は前に許可されたまま続きます（コードの `SIMPLIFIED:`。TS 29.214 4.4.2）。
- 信頼性のない暫定応答の SDP を中継ノードに通したときも、P-CSCF はそのメディアを PCRF に伝えます。そのあと要求が 2xx 以外で終わると、中継ノードは戻りますが、PCRF に伝えたメディアは、次のオファーとアンサーまでそのままです。

**IBCF。** 規則は P-CSCF と同じで、2xx 以外の応答を渡す前に TrGW を戻し、戻せなければ呼を解放します。違うのは、成立したアドレスの置き場です。IBCF は B2BUA で、呼の 2 つのダイアログは、その INVITE を受けた台のプロセスの中にあります。ダイアログの中の要求は、いつも同じ台に戻ってきます。そのため、成立したアドレスもその台のメモリに置けば足ります。台が止まれば呼も止まり、保存先が残っていれば、同じ名前で起動し直した台がその記録から呼を終わらせます（[網間接続](interconnect.md) と [保守で止めることと、突然失うこと](design-maintenance.md)）。

**同時に進むオファー。** 2 つの当事者が、別々の要求でそれぞれオファーを送ることがあります。IBCF は、ある要求が失敗したとき、その要求が動かしたターミネーションだけを戻します（`movedFor`）。もう一方の要求が動かしたターミネーションは、その要求の結果を待ちます。

## この説明の根拠

この章の説明は、次のコードと試験に照らして確かめたものです。どの試験も、このリポジトリの Go の試験として通ることを確かめています。試験の相手役と中継ノードは試験用のもので、試験はそれらが返す応答で流れを作ります。

- `internal/pcscf/rollback_test.go` の `TestRejectedOfferPutsTheRelayBack`。UPDATE と re-INVITE のそれぞれで、どちらの当事者のオファーでも、488 のあとにターミネーションが前のアドレスへ送り、呼が続くことを確かめています。
- 同じファイルの `TestCancelledReInvitePutsTheRelayBack`。re-INVITE のあいだは中継ノードがオファーのアドレスへ送り、取り消しの 487 のあとに前のアドレスへ戻ることと、CANCEL の Reason が相手に届くことを確かめています。
- 同じファイルの `TestRejectedOfferGoesBackToTheLastOfferAndAnswer`。戻る先が INVITE のときのアドレスではなく、最後に 2xx を受けた re-INVITE のオファーとアンサーのアドレスであることを確かめています。
- 同じファイルの `TestRejectedOfferAfterAPreviewPutsBothSidesBack`。信頼性のない 183 で動いた側も含めて、両方のターミネーションが戻ることを確かめています。
- 同じファイルの `TestRejectedOfferEndsTheCallWhenTheRelayDoesNotGoBack`。戻す `Modify` が失敗すると、488 が渡ったあとで両方に BYE が届き、PCRF のセッションとコンテキストが解放されることを確かめています。
- `internal/pcscf/refusedoffer_test.go` の `TestRefusedOfferWithinTheDialogIsRejected`。中継ノードが `Modify` を断ると、P-CSCF が 500 を返して要求を先へ渡さず、呼が続き、次のオファーは通ることを確かめています。
- 同じファイルの `TestOfferWithinTheDialogEndsTheCallWhenTheRelayMayHaveTakenIt`。中継ノードが期限内に答えない場合（試験の中継ノードは変更を実行している）と、コンテキストを無くしたと答える場合に、500 と両方への BYE になることを確かめています。
- `internal/pcscf/refusedanswer_test.go` の `TestRefusedAnswerWithinTheDialogReleasesTheCall`。応答のアンサーを中継ノードが受け取らないと、200 が渡ったあと（re-INVITE なら ACK のあと）に BYE が届くことを確かめています。
- `internal/ibcf/rollback_test.go` の `TestRejectedOfferPutsTheTrGWBack`、`TestRejectedOfferEndsTheCallWhenTheTrGWDoesNotGoBack`、`TestCancelledReInvitePutsTheTrGWBack`、`TestRejectedOfferAfterAPreviewGoesBackToTheLastOfferAndAnswer`、`TestRejectedOfferAfterAReliableAnswerKeepsIt`。IBCF と TrGW で同じ規則が働くことと、信頼性のある 183 でアンサーが成立したあとの 488 では何も戻らないことを確かめています。
- `internal/sip/indialog_test.go` の `TestA481WithinTheDialogEndsTheCall` と `internal/sip/reinviteprack_test.go` の `TestPRACKOfAReINVITEThatGets481EndsTheCall`。ダイアログの中の要求が 481 を受けると、UA が BYE で呼を終わらせることを確かめています。re-INVITE そのものの時間切れを確かめる試験は無く、その場合の UA の動きは、上で引いた `lost` を呼ぶコードから読んだものです。

## 自分で動かす場合

手順書の通常の構成では、オファーを断る相手や、答えない中継ノードを作れません。この章の流れは、上の試験で動かせます。たとえば `go test ./internal/pcscf -run 'TestRejectedOffer|TestRefusedOffer|TestCancelledReInvite'` です。信号の順をシーケンス図で見たいときは、手順書の [中継ノードがオファーを受け取らないとき](../../guide/ja/call.md#中継ノードがオファーを受け取らないとき) と [相手がオファーを断ったとき](../../guide/ja/call.md#相手がオファーを断ったとき)、網間接続の [呼の中の re-INVITE](../../guide/ja/interconnect.md#呼の中の-re-invite) と [TrGW が SDP を受け取らないとき](../../guide/ja/interconnect.md#trgw-が-sdp-を受け取らないとき) にあります。

次の章では、ノードを運用から外すときと、ノードを突然失うときに、処理中の仕事がどうなるかを見ます。[保守で止めることと、突然失うこと](design-maintenance.md)
