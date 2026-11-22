# B2BUA の中核

この記録は、IBCF と MMTel AS が同じ形で持つ「2 つのレグを中継する部分」を 1 か所にまとめる設計です。

## 選んだこと

- 2 つのレグを中継する中核を、新しいパッケージ `internal/b2bua` に置きます。`internal/sip` には入れません。`internal/sip` は UA とトランザクションの層で、2 つのレグを対応づけるのは別の受け持ちだからです。
- 中核は、呼の流れ全体を握る枠組みにはしません。両方の役割が同じ形で行う手順だけを持つ道具にします。呼の順序（IBCF の境界の検査と TrGW の予約、MMTel AS のサービスと転送の試し直し）は、それぞれの役割が持ちます。
- 役割ごとに違う部分は、差し込み口（`Hooks`）で中核に渡します。差し込み口はどれも、IBCF と MMTel AS の 2 つが実装します。
- ダイアログの中の要求は、届いた順に中継します。これを中核の受け持ちにします。
- 着信側の early dialog ごとに、SDP を To の tag で分けて持ちます（RFC 3261 13.2.1、13.2.2.4）。応答したダイアログの SDP を使います。
- 呼の状態はこれまでどおりプロセスの中に置きます。保存先（`Store`）の形は変えません。

## 今の作りで重なっているもの

IBCF（`internal/ibcf/call.go`、`internal/interconnect/leg.go`）と MMTel AS（`internal/mmtel/call.go`、`internal/mmtel/fields.go`）には、次のものがほぼ同じ形で写されています。

| もの | ずれていること |
| --- | --- |
| `leg` のインタフェース | 無し。 |
| 呼の状態 | IBCF は SDP を To の tag ごとに持ちます。MMTel AS は、どの枝のものでも最後の SDP を 1 つ持ちます。 |
| `fromCaller`、`fromCallee` | MMTel AS は、レグを作らなかった呼（拒否の前の音声ガイダンス）で、発信側の要求に答えないまま待ち続けます。IBCF は 481 で答えます。届いた順に中継するのは、IBCF の、答えを含む PRACK のあとの要求だけです。IBCF はダイアログの中の要求のたびに呼の記録を書き直しますが、MMTel AS は書き直しません。 |
| `forward`、`answerRequest` | 方法ごとの振り分けと応答コード（491、481、500）は同じです。IBCF だけが、自分で断った数を数えます。 |
| `relayProvisional`、`answer` | 199、オファー、PRACK の扱いは同じです。MMTel AS は、どの early dialog の SDP も発信側に渡します。 |
| CANCEL の見張り | IBCF は、応答の前に発信側のレグが終われば、いつでも着信側のレグを取り消します。MMTel AS は、CANCEL のときだけ取り消します。 |
| Reason の写し | IBCF はそのまま写し、MMTel AS は読み直して書き直します。 |
| レグのヘッダの表 | 共通の 17 個の名前は同じです。役割ごとに足すものがあります（IBCF は Path と Service-Route、MMTel AS は P-Served-User と P-Charging-Vector）。 |
| `hasSDP`、`toTag`、`errText` | 同じものです。`hasSDP` は `internal/pcscf` にもあります。 |

会議のフォーカスのメディアの変更（`internal/mmtel/conference.go` の `changeMedia`）にも同じ振り分けがあります。ただ、フォーカスには呼の 2 つ目のレグが無いので、中核には入れません。

## 中核の形

```go
type Hooks interface {
	Request(req *sip.Message, d Direction) (Message, *Refusal)
	Response(resp, req *sip.Message, d Direction) Message
	Answer(resp, session *sip.Message) Message
	Ack(ack *sip.Message, d Direction) sip.Body
	Contact(resp *sip.Message) (c sip.NameAddr, recordRoute, ok bool)
	Reasons(ended *sip.Message, d Direction) []sip.HeaderField
	Dialogs(in, out sip.Dialog)
	Refused(req *sip.Message, d Direction, code int)
}
```

- `Request` と `Response` で、IBCF は境界の検査と TrGW の書き換えを、MMTel AS はサービスごとのヘッダを足します。
- `Dialogs` は、応答したとき、そのあとはダイアログの中の要求を中継するたびに呼びます。両方の役割が、ここで呼の記録を書き直します。
- `EarlyDialogs` が、着信側の early dialog ごとの SDP と、発信側がどの SDP を持っているかを覚えます。
- `Call` が 2 つのレグを持ちます。`Attempt` で着信側へ INVITE を送り、その応答の扱いを役割に返します。MMTel AS は転送のときに `Attempt` を 2 回呼びます。`NoLeg` は、レグを作らないことを知らせます。そのあとの発信側の要求には 481 で答えます。`Progress` は、役割が自分で送る 1xx（CAT、音声ガイダンス、181）です。

ダイアログの中の要求 1 つを、中核は次の順に扱います。

1. 届いた順の番を待ちます。待つあいだも、どちらかのレグが終わったかを見ています。
2. 着信側へ送るときは、レグができるのを待ちます。レグが無ければ 481 で答えます。
3. 発信側の PRACK は、それが確認する着信側の信頼できる 1xx があれば、着信側への PRACK にします（RFC 3262 4、5）。
4. `Request` で整えてから、UPDATE、re-INVITE、そのほかの要求として送ります。
5. もう一方のレグが最終応答を返したら、次の要求の番に進みます。
6. 応答を返してから `Dialogs` を呼びます。

### 比べた形

| 形 | 採らなかった理由、採った理由 |
| --- | --- |
| 呼全体を握る枠組み（`b2bua.Run(in, Role)`） | 転送の試し直し、無応答のタイマー、CAT、音声ガイダンス、TrGW の予約が、どれも 1 つの役割しか実装しない差し込み口になります。使う場所が 1 か所しかない抽象化になるので採りません。2 つの保存先は読み方が違う（IBCF は起動のときにインスタンスごとに読み、MMTel AS はダイアログごとに読む）ので、共通の呼の記録も作れません。 |
| 両方が同じ形で行う手順だけを持つ道具（採った形） | 差し込み口のどれにも、実装が 2 つあります。呼の順序は役割に残るので、読む人は役割のコードから手順を追えます。 |

## `internal/sip` に足すもの

- `InDialogRequest.Arrival`。ダイアログの中の要求に、呼ごとに 1 から振る届いた順の番号です。`internal/sip` は要求を受けた順にダイアログの状態を変えていますが、要求ごとに別の goroutine に渡すので、受け取る側には順番が分かりません。
- `IncomingCall.RelayPRACK`。本文の無い PRACK を、`internal/sip` が自分で 200 で答えずに、受け取る側に渡します。B2BUA が PRACK と、それに続く UPDATE の順を守るためです。
- `HasSessionDescription`。`internal/ibcf` と `internal/pcscf` の `hasSDP` を置き換えます。

`internal/sip` が要求を 1 つずつ渡す形は採りません。UE、MRFC、フォーカスの動きも変わるうえ、中核が応答を返す前に次の要求へ進めなくなるためです。

## 振る舞いをそろえるときに選んだこと

2 つの役割でずれている所は、実際の網の B2BUA の動きに合わせて、次のようにそろえます。

| 主題 | 選んだこと | 理由 |
| --- | --- | --- |
| 応答の前に発信側のレグが終わったとき | いつでも着信側のレグを取り消す（今の IBCF の形） | 取り消さないと、発信側がいないのに着信側が鳴り続けます。 |
| レグができる前の発信側の要求 | 481 で答える（今の IBCF の形） | 答えない要求は、発信側でタイムアウトまで残ります。 |
| 届いた順の中継 | ダイアログの中のすべての要求で守る | 順番が入れ替わると、オファーとアンサーの組が崩れます（RFC 3264）。 |
| Reason | 受けたヘッダをそのまま写す（今の IBCF の形） | 読めない値を落とすと、相手に原因が伝わりません（RFC 3326 2）。 |
| 本文の無い PRACK への 200 | 今までどおりすぐに返す | 着信側の応答を待つと、発信側の再送のタイマーにかかります。 |
| 次の要求に進む時点 | もう一方のレグの最終応答 | 最初の 1xx で進めるには `internal/sip` にもう 1 つ口が要ります。違いが出るのは、18x を返す re-INVITE のときだけです。 |
| early dialog の SDP | 着信側の UPDATE と PRACK の SDP も含める（今の MMTel AS の形） | CAT のプリコンディションが、それを使うためです。 |
| ヘッダの表 | 共通の表に、役割ごとのものを足す | 和集合にすると、IBCF が、方針で通している P-Charging-Vector を落とします。 |

## 移す順番

1. この設計の記録。
2. `internal/sip` に上の 3 つを足し、IBCF を中核に載せます。最初は今の順序のまま移し、そのあとで届いた順の中継と `RelayPRACK` を入れます。
3. MMTel AS の early dialog の SDP を To の tag ごとに持ち、応答したダイアログのものを使い、呼の記録をダイアログの中の要求ごとに書き直します。
4. MMTel AS を中核に載せます。振る舞いをそろえる所は、それぞれ試験を足した別のコミットにします。
5. 重なっていた関数と表を消します。

どの段階でも、`internal/ibcf` と `internal/mmtel` の今の試験と、`scripts/e2e-interconnect.sh`、`scripts/e2e-mmtel.sh`、`scripts/e2e-mrf.sh` が通るようにします。キャプチャに出るメッセージは変えません。
