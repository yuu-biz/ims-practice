# B2BUA の中核

この記録は、IBCF と MMTel AS が同じ形で持つ「2 つのレグを中継する部分」を 1 か所にまとめる設計です。

## 選んだこと

- 2 つのレグを中継する中核を、新しいパッケージ `internal/b2bua` に置きます。`internal/sip` には入れません。`internal/sip` は UA とトランザクションの層で、2 つのレグを対応づけるのは別の受け持ちだからです。
- 中核は、呼の流れ全体を握る枠組みにはしません。両方の役割が同じ形で行う手順だけを持つ道具にします。呼の順序（IBCF の境界の検査と TrGW の予約、MMTel AS のサービスと転送の試し直し）は、それぞれの役割が持ちます。
- 役割ごとに違う部分は、差し込み口（`Hooks`）で中核に渡します。差し込み口はどれも、IBCF と MMTel AS の 2 つが実装します。
- ダイアログの中の要求は、届いた順に中継します。これを中核の受け持ちにします。
- 着信側の early dialog ごとに、SDP を着信側のダイアログの tag で分けて持ちます（RFC 3261 13.2.1、13.2.2.4）。応答したダイアログの SDP を使います。
- 呼の状態はこれまでどおりプロセスの中の、呼ごとの値（`Call`）に置きます。保存先（`Store`）の形は変えません。役割全体を止めるロックも、全件の走査も足しません。

## 重なっていたもの

取りかかる前の IBCF（`internal/ibcf/call.go`、`internal/interconnect/leg.go`）と MMTel AS（`internal/mmtel/call.go`、`internal/mmtel/fields.go`）には、次のものがほぼ同じ形で写されていました。どれも、今は中核に 1 つだけあります。

| もの | ずれていたこと |
| --- | --- |
| `leg` のインタフェース | 同じでした。 |
| 呼の状態 | IBCF は SDP を To の tag ごとに持ちました。MMTel AS は、どの枝のものでも最後の SDP を 1 つ持ちました。 |
| `fromCaller`、`fromCallee` | MMTel AS は、レグを作らなかった呼（拒否の前の音声ガイダンス）で、発信側の要求に答えないまま待ち続けました。IBCF は 481 で答えました。届いた順に中継するのは、IBCF の、アンサーを運ぶ PRACK のあとの要求だけでした。IBCF はダイアログの中の要求のたびに呼の記録を書き直し、MMTel AS は書き直しませんでした。 |
| `forward`、`answerRequest` | 方法ごとの振り分けと応答コード（491、481、500）は同じでした。IBCF だけが、自分で断った数を数えました。 |
| `relayProvisional`、`answer` | 199、オファー、PRACK の扱いは同じでした。MMTel AS はどの early dialog の SDP も発信側に渡し、IBCF は最初のものだけを渡しました。 |
| CANCEL の見張り | IBCF は、応答の前に発信側のレグが終われば、いつでも着信側のレグを取り消しました。MMTel AS は、CANCEL のときだけ取り消しました。 |
| Reason の写し | IBCF はそのまま写し、MMTel AS は読み直して書き直しました。 |
| レグのヘッダの表 | 共通の 17 個の名前は同じでした。役割ごとに足すものがあります（IBCF は Path と Service-Route、MMTel AS は P-Served-User と P-Charging-Vector）。共通の表、オプションタグ、絞り込みは `internal/b2bua/fields.go` に置き、役割ごとの表をそこに渡します。 |
| `hasSDP` | IBCF（`internal/ibcf/media.go`）と P-CSCF（`internal/pcscf/session.go`）に同じものがありました。`sip.HasSessionDescription` に置き換えました。 |

`errText` は、IBCF と MMTel AS に同じものが残っています。どちらのパッケージからも使える置き場所が無いためです（`internal/b2bua` は中継の受け持ちで、エラーの文には関わりません）。

会議のフォーカスのメディアの変更（`internal/mmtel/conference.go` の `changeMedia`）にも同じ振り分けがあります。ただ、フォーカスには呼の 2 つ目のレグが無いので、中核には入れません。`leg` のインタフェースは、そのためにフォーカスの側に残しています。

## 中核の形

```go
// Direction is which way a message crosses the B2BUA: toward the
// callee or toward the caller.
type Direction int

// Message is what a role adds to a message it relays: header fields and
// the body.
type Message struct {
	Headers []sip.HeaderField
	Body    sip.Body
}

// Refusal is the response with which a role refuses a request instead of
// relaying it. Release has the Call end the call after it.
type Refusal struct {
	Status  int
	Headers []sip.HeaderField
	Release bool
}

type Hooks interface {
	Request(req *sip.Message, d Direction) (Message, *Refusal)
	// ok false: the role cannot pass resp on, and the Call releases
	// the call.
	Response(resp, req *sip.Message, d Direction) (m Message, ok bool)
	// Answer is the callee's 2xx. session is the last SDP of the early
	// dialog that answered, when the caller holds that of another one.
	Answer(resp, session *sip.Message) (m Message, ok bool)
	Ack(ack *sip.Message, d Direction) (body sip.Body, ok bool)
	Contact(resp *sip.Message) (c sip.NameAddr, recordRoute, ok bool)
	Reasons(ended *sip.Message, d Direction) []sip.HeaderField
	KeepDialogs(in, out sip.Dialog)
	// Unanswered: req got no 2xx on the other leg. ok false: the role
	// cannot put back what it changed for req, and the Call releases
	// the call.
	Unanswered(req *sip.Message, d Direction) (ok bool)
	// Refused: the Call answered req with status itself, for the role.
	Refused(req *sip.Message, d Direction, status int)
}
```

- `Request` と `Response` で、IBCF は境界の検査と TrGW の書き換えを、MMTel AS はサービスごとのヘッダを足します。IBCF は、境界の検査で断るときと、SDP を TrGW に通せないときに、`Request` が `Refusal` を返します。MMTel AS の `Request` は断りません。
- 応答と ACK は断れません。役割が SDP を書き換えられないなど、先へ渡せないときは、`Response`、`Answer`、`Ack` が `ok` を false で返します。`Refusal` の `Release` も同じく、呼を続けられないことを知らせます。そのとき `Call` は `release` で呼を終わらせます。発信側の INVITE に最終応答がまだ無ければ、500 を返します（RFC 3261 21.5.1）。着信側の 2xx が届いたあと、発信側に渡す前でも同じで、`Answer` は INVITE に答えが出ていると知って、着信側に BYE を送ります。応答の前なら、着信側への INVITE を取り消します（RFC 3261 9.1）。発信側の INVITE に最終応答が出ていれば、発信側に BYE を送り、`Answer` が着信側にも BYE を送ります。先へ渡せない 2xx の代わりに、その要求には 500 を返します。役割が断った PRACK は、着信側の信頼できる 1xx に PRACK が届かなくなるので、いつも INVITE を終わらせます。
- `Unanswered` は、ダイアログの中の要求に、もう一方のレグで 2xx が返らなかったときに呼びます。セッションは要求の前のままなので（RFC 3261 14.1、RFC 3311 5.3）、IBCF は offer を受けて TrGW に伝えた宛先を、最後の offer と answer のものに戻します。戻せなければ `ok` を false で返し、`Call` が呼を終わらせます。
- `Refused` は、`Call` が役割のために自分の応答（`Refusal` の応答、先へ渡せない 2xx の代わりの 500、解放のときの INVITE への 500）を送ったあとで呼びます。IBCF は、自分で断った数をここで数えます。送らなかった応答を数えないためです。
- この 3 つの結果と 2 つの差し込み口を意味のある形で実装するのは、メディアを中継させる IBCF だけです。MMTel AS は、どれも「先へ渡す」「戻すものは無い」「数えるものは無い」と答えます。それでも中核に置くのは、どの応答をどちらのレグに送るか、呼をどう終わらせるかが、2 つのレグを持つ中核の受け持ちだからです。役割の側で同じことをすると、レグの状態を役割が読むことになります。
- `KeepDialogs` は、応答したとき、そのあとはダイアログの中の要求を中継するたびに呼びます。両方の役割が、ここで呼の記録を書き直します。
- `EarlyDialogs` が、着信側の early dialog ごとの SDP と、発信側がどの SDP を持っているかを覚えます。
- `Call` が 2 つのレグを持ちます。`Attempt` で着信側へ INVITE を送り、その応答の扱いを役割に返します。MMTel AS は転送のときに `Attempt` を 2 回呼びます。`NoLeg` は、レグを作らないことを知らせます。`Progress` は、役割が自分で送る 1xx（CAT、音声ガイダンス、181）です。

ダイアログの中の要求 1 つを、中核は次の順に扱います。

1. 届いた順の番を待ちます。待つあいだも、どちらかのレグが終わったかを見ています。
2. 役割が `Progress` で送った信頼できる 1xx への PRACK には、中核が 200 で答えます（RFC 3262 4）。
3. 着信側へ送るときは、レグができるのを待ちます。`NoLeg` のあとのレグの無い呼でも、役割が自分の 1xx で作った early dialog の中の要求には 481 で答えません。ダイアログはあるからです。オファーを運ぶ UPDATE は 488 で、本文の無い UPDATE は 200 で断ります。そのほかの要求は、レグもダイアログも無いときに 481 で答えます。
4. 発信側の PRACK は、それが確認する着信側の信頼できる 1xx があれば、着信側への PRACK にします（RFC 3262 4、5）。
5. `Request` で整えてから、UPDATE、re-INVITE、そのほかの要求として送ります。
6. もう一方のレグが最終応答を返したら、次の要求の番に進みます。オファーの無い re-INVITE は、アンサーを運ぶ ACK を中継するまで番を持ちます。
7. 応答を返してから `KeepDialogs` を呼びます。

番は `Call` の中に、レグごと、向きごとに持ちます。発信側の要求の番が、着信側の要求を止めないためです。番は、中継の処理を始めるときに必ず `defer` で返します。早く戻る道や panic があっても、次の要求が待ち続けないようにするためです。レグが終わって番を諦めた PRACK は、発信側が確認した順から自分の分を外します。外さないと、次の PRACK がその分を自分のものと取り違え、終わったレグの応答への PRACK として送ってしまいます。

### 役割の側に残したもの

- 発信側に与えたセッションを役割が自分で取り戻す offer（CAT のトーンを止める UPDATE、TS 24.182 4.5.5.3.7 a 2）は、MMTel AS が自分で送ります。`Call.OfferCaller` が渡せるのは、`Call` が中継した応答のセッション記述だけです。CAT のあいだ、着信側の暫定応答は MRF の答えに差し替えて `Progress` で送るので、`Call` はそのダイアログのセッション記述を見ません。中核に渡すには、役割が自分で答えた応答のセッション記述を `Call` に知らせる口が要ります。差し込み口を 1 つの役割のために増やさないので、offer は AS に残します。
- 転送の見込みがある呼で着信側のアンサーを 2xx まで止める扱い（TS 24.604 4.5.2.6.3）も、AS が `Progress` で状態コードだけを送ります。`RelayProvisional` に渡すと、`Call` が「発信側はこの early dialog の SDP を持っている」と覚えてしまうためです。

### 比べた形

| 形 | 採らなかった理由、採った理由 |
| --- | --- |
| 呼全体を握る枠組み（`b2bua.Run(in, Role)`） | 転送の試し直し、無応答のタイマー、CAT、音声ガイダンス、TrGW の予約が、どれも 1 つの役割しか実装しない差し込み口になります。使う場所が 1 か所しかない抽象化になるので採りません。2 つの保存先は読み方が違う（IBCF は起動のときにインスタンスごとに読み、MMTel AS はダイアログごとに読む）ので、共通の呼の記録も作れません。 |
| 両方が同じ形で行う手順だけを持つ道具（採った形） | 差し込み口のどれにも、実装が 2 つあります。呼の順序は役割に残るので、読む人は役割のコードから手順を追えます。 |

## `internal/sip` に足すもの

- `InDialogRequest.Arrival`。ダイアログの中の要求に、呼ごと、向きごとに 1 から振る届いた順の番号です。`internal/sip` は要求を受けた順にダイアログの状態を変えていますが、要求ごとに別の goroutine に渡すので、受け取る側には順番が分かりません。番号は、受け取る側の処理を実際に始める要求にだけ振ります。順番の検査のあとで断った要求に番号を振ると、次の要求が来ない番を待ち続けるためです。
- `IncomingCall.RelayPRACK`。本文の無い PRACK を、`internal/sip` が自分で 200 で答えずに、受け取る側に渡します。B2BUA が PRACK と、それに続く UPDATE の順を守るためです。使う側が求めたときだけ変わるので、UE、MRFC、フォーカスの動きは変わりません。
- `HasSessionDescription`。`internal/ibcf` と `internal/pcscf` の `hasSDP` を置き換えます。

`internal/sip` が要求を 1 つずつ渡す形は採りません。UE、MRFC、フォーカスの動きも変わるうえ、中核が応答を返す前に次の要求へ進めなくなるためです。

## 振る舞いをそろえるときに選んだこと

2 つの役割でずれている所は、実際の網の B2BUA の動きに合わせて、次のようにそろえます。

| 主題 | 選んだこと | 理由 |
| --- | --- | --- |
| 応答の前に発信側のレグが終わったとき | いつでも着信側のレグを取り消す（今の IBCF の形）。MMTel AS は、応答したかどうかの印も IBCF と同じに持つ | 取り消さないと、発信側がいないのに着信側が鳴り続けます。応答のあとに取り消さないよう、印で分けます。 |
| レグもダイアログも無いときの発信側の要求 | 481 で答える（今の IBCF の形） | 答えない要求は、発信側でタイムアウトまで残ります。 |
| 届いた順の中継 | ダイアログの中のすべての要求で守る | UA は CSeq の順で要求を受けます（RFC 3261 12.2.2）。順番が入れ替わると、相手は古い CSeq の要求を 500 で断り、オファーとアンサーの組も崩れます（RFC 3264）。 |
| 発信側に渡す early dialog の SDP | 最初の early dialog のものだけを渡し、ほかの early dialog の SDP は外す（今の IBCF の形）。その early dialog が 199 で終わったあとと、役割が発信側の early dialog を 199 で終えて次のレグに移ったあとは、次に届いた SDP をそのまま渡す（今の MMTel AS の形）。発信側の early dialog が続いているあいだは、レグを移しても渡したものを数える | 発信側とのダイアログは 1 つで、1 つのオファーに返すアンサーは 1 つだけだからです（RFC 3264）。終わったダイアログの SDP にこだわると、プリコンディションを使う次の UAS は、発信側が資源を確保したと分からず、利用者を呼び出しません（RFC 3312）。発信側の early dialog が続いていれば、オファーの無い INVITE のあとに届く 2 つ目のオファーは、発信側が無視します（RFC 3261 13.2.1）。 |
| 発信側が SDP を持つのと別の early dialog が応答したとき | 発信側が 2xx に ACK を返したあとで、応答したダイアログのセッションを発信側にオファーする。発信側が Allow に UPDATE を挙げていれば UPDATE で、挙げていなければ re-INVITE で送る。`o=` 行は発信側が持っているものを使い、版の番号だけを 1 上げる（RFC 3264 8）。2xx が着信側のオファーを運んだときは、発信側のアンサーを ACK に入れる（今の MMTel AS の形） | 発信側は最初に受け取った SDP をアンサーとし、あとのものを無視します（RFC 3261 13.2.1）。オファーし直さないと、発信側のメディアは、応答しなかった UAS に向いたままになります。 |
| Reason | 受けたヘッダをそのまま写す（今の IBCF の形） | 読めない値を落とすと、相手に原因が伝わりません（RFC 3326 2）。 |
| 本文の無い PRACK への 200 | 今までどおりすぐに返す | 着信側の応答を待つと、発信側の再送のタイマーにかかります。 |
| 次の要求に進む時点 | もう一方のレグの最終応答。オファーの無い re-INVITE は、ACK を中継したとき | 最初の 1xx で進めるには、`internal/sip` にもう 1 つ機能が要ります。違いが出るのは、18x を返す re-INVITE のときだけです。オファーの無い re-INVITE では、アンサーが ACK で届きます。 |
| early dialog の SDP | 着信側の UPDATE と PRACK の SDP も含める（今の MMTel AS の形） | CAT のプリコンディションが、それを使うためです。 |
| ヘッダの表 | 共通の表に、役割ごとのものを足す | 和集合にすると、IBCF が、方針で通している P-Charging-Vector を落とします。 |

## 移す順番

コードは、次の順に小さく移します。まず `internal/sip` に上の 3 つを足し、IBCF を中核に載せます。IBCF は最初に今の順序のまま移し、そのあとで届いた順の中継と `RelayPRACK` を入れます。次に、MMTel AS が early dialog の SDP を着信側のダイアログの tag ごとに持ち、応答したダイアログのものを使い、呼の記録をダイアログの中の要求ごとに書き直すようにします。そのあとで MMTel AS を中核に載せます。振る舞いをそろえる所は、それぞれ試験を足した別のコミットにします。最後に、重なっていた関数と表を消します。

どの段階でも、`internal/ibcf` と `internal/mmtel` の今の試験と、`scripts/e2e-interconnect.sh`、`scripts/e2e-mmtel.sh`、`scripts/e2e-mrf.sh` が通るようにします。キャプチャに出るメッセージは、振る舞いをそろえる所のほかは変えません。
