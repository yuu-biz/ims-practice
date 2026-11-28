# 旧デッキの内容の行き先

勉強会の 1 回分ずつだったデッキを、読むだけで分かる Book とスライドに組み直したときの、内容の行き先の記録です。作りの理由は [book.md](book.md) にあります。`python3 scripts/check_deck_content.py docs/design/book-migration.md` が、この記録と、基準のコミットのデッキと、いまのファイルを突き合わせます。

基準のコミット: `bfb57a7611a0632a51f56ceba892141f43d22d96`

## 書き方

デッキごとに 1 つの表を置き、見出しを `## <言語>/<デッキのファイル>` にします。基準のコミットのデッキのスライドのうち、見出しがいまのデッキに無いものと、中身を別の場所へ移したものに、1 行以上を書きます。そのまま残したスライドには書きません。

| 列 | 書くこと |
| --- | --- |
| 旧の見出し | 基準のコミットのスライドの見出し。モデルのブロックは `ims-model` |
| 内容 | 移した知識と、そう扱った理由。訂正と削除では、その根拠 |
| 扱い | 保持、再編集、統合、移動、課題から解説へ、訂正、削除 のどれか |
| 行き先 | 移した先のファイルと見出しのアンカー、またはデッキのシーンの id。`docs/book/ja/x.md#見出し` のようにバッククォートで囲み、複数なら並べる |
| 確かめる語 | 行き先のその節に残っているべき語。訂正と削除では、消してよい語 |

検査は、行き先のファイルと見出しやシーンがあること、確かめる語がその節にあること、旧スライドのリンク、行の中のコード、コードの行、表のセルが、いまのデッキか行き先のファイルに残っていることを確かめます。表の行の意味が旧スライドと同じかは、行を書いた人が確かめます。

## ja/04-registration.md

| 旧の見出し | 内容 | 扱い | 行き先 | 確かめる語 |
| --- | --- | --- | --- | --- |
| この回で分かること | 最初の問いを、登録が何をするかを言う文に書き直した。学ぶことの一覧に、登録のあとに続くことと、通話のデッキが使うものを足し、本の章への案内を置いた | 再編集 | `slides/decks/ja/04-registration.md#このデッキで分かること` | `登録で決まる 3 つのこと`、`REGISTER が 2 回送られる理由`、`docs/guide/ja/registration.md` |
| 進め方 | 構成を起動し、キャプチャを開いて読む手順。手順は手順書にあり、デッキは任意の再現として手順書へ案内する | 移動 | `docs/guide/ja/registration.md#キャプチャを開く`、`slides/decks/ja/04-registration.md#自分で動かす場合` | `Flow Graph`、`capture.md` |
| 実行する | 構成の起動、Diameter の接続の確かめ、5 秒後に解除する登録の実行と、UE のログの 3 行。手順書の同じ節に、同じコマンドと説明がある | 移動 | `docs/guide/ja/registration.md#登録を実行する`、`slides/decks/ja/04-registration.md#自分で動かす場合` | `docker compose run --rm ue register -deregister-after 5s`、`I-Open`、`"msg":"reg event"`、`"msg":"deregistered"`、`Tc`、`登録を実行する` |
| 実行する | コマンドの行の後ろの注釈。手順書は同じことを手順の文で説明している | 削除 |  | `docker compose logs scscf \| grep I-Open        # Diameter の接続が開いたか` |
| キャプチャを開く | 表示フィルタ、残るものと消えるもの、Flow Graph の作り方、手順書の番号との対応 | 移動 | `docs/guide/ja/registration.md#キャプチャを開く`、`docs/guide/ja/registration.md#シーケンス`、`slides/decks/ja/04-registration.md#自分で動かす場合` | `sip \|\| diameter.cmd.code >= 300 \|\| dns.qry.type == 35 \|\| dns.qry.type == 33`、`Limit to display filter`、`DWR`、`キャプチャを開く` |
| コードで確かめる | 「検索します」「探します」で終わっていたページを、ノードごとのファイルと判断する関数を名指す説明に変えた。関数の判断の中身は本の章で抜き出しとともに説明する | 課題から解説へ | `slides/decks/ja/04-registration.md#code`、`docs/book/ja/registration-lifecycle.md#ue-が登録を解除する` | `判断する関数`、`bindingResponse`、`SIMPLIFIED:`、`deregister` |
| 振り返り | 5 つの問い。答えは、問いに当たるスライドの説明と本の章の文にある（振り返りの答えの行） | 課題から解説へ | `docs/book/ja/registration.md#本人であること`、`docs/book/ja/registration.md#このあとの要求の経路`、`docs/book/ja/registration.md#登録のあとに続くこと` | `HSS は S-CSCF にパスワードを渡しません`、`I-CSCF は 1 回の登録で HSS に 2 回尋ねます`、`401 は返りません` |
| 振り返りの答え | 1 の答え。1 回目への答えとして網が nonce を出し、2 回目で UE が答える | 課題から解説へ | `slides/decks/ja/04-registration.md#overview`、`docs/book/ja/registration.md#本人であること` | `2 回目で「本人であること」を証明します`、`1 回目の REGISTER は「誰か」を伝え` |
| 振り返りの答え | 2 の答え。I-CSCF は 2 回尋ね、1 回目は DIAMETER_FIRST_REGISTRATION と Server-Capabilities、2 回目は DIAMETER_SUBSEQUENT_REGISTRATION と Server-Name | 課題から解説へ | `slides/decks/ja/04-registration.md#second-register`、`docs/book/ja/registration.md#本人であること`、`docs/book/ja/registration-two-registers.md#2-回目が同じ-s-cscf-に届く理由` | `DIAMETER_SUBSEQUENT_REGISTRATION`、`Server-Name` |
| 振り返りの答え | 3 の答え。HSS はパスワードを渡さず、H(A1) を渡す | 課題から解説へ | `slides/decks/ja/04-registration.md#challenge`、`docs/book/ja/registration.md#本人であること` | `H(A1)`、`パスワードを渡しません` |
| 振り返りの答え | 4 の答え。S-CSCF は Path で UE あての要求をその UE の P-CSCF 経由で送り、UE は Service-Route で自分の要求を自分の S-CSCF へ送る | 課題から解説へ | `slides/decks/ja/04-registration.md#routes`、`docs/book/ja/registration.md#このあとの要求の経路`、`docs/book/ja/registration-routes.md#登録で決まる-2-つの経路` | `Service-Route`、`UE あての要求の経路` |
| 振り返りの答え | 5 の答え。P-CSCF がその UE の IP の対応付けを持ち、`ip-assoc-yes` を付けるので、解除で 401 が返らない | 課題から解説へ | `slides/decks/ja/04-registration.md#deregister`、`docs/book/ja/registration-lifecycle.md#ue-が登録を解除する` | `ip-assoc-yes`、`401 が返らない理由` |
| 次の回 | 次に読むのは通話で、ここで決まる経路と IP の対応付けを使うこと。後片付けの 2 つのコマンドと、その違い | 移動 | `slides/decks/ja/04-registration.md#このデッキで分かること`、`slides/decks/ja/04-registration.md#自分で動かす場合` | `Path、Service-Route、IP の対応付け`、`docker compose --profile ue down --rmi all -v --remove-orphans`、`call.md` |
| 失敗の例 | 表の「試し方」の列の、操作を指示する文を、起きることの言い方（運用者が加入を削除する）に書き直した。操作の手順はラボの手順書へのリンクに残る | 削除 |  | `ラボの手順書の「網が登録を取り消す」のとおり、登録中の加入を OAM から削除する。` |
| ラボで同じ流れを見る | 画面を開いて選ぶ指示を、パネルが見せるものの説明に変えた。表の組み立て方と、Cx との結び付きが推定であることは本の章で説明する | 再編集 | `slides/decks/ja/04-registration.md#ラボで同じ流れを見る`、`docs/book/ja/registration-lifecycle.md#ラボが見せる登録` | `Registrations`、`HEP`、`推定` |

## en/04-registration.md

| 旧の見出し | 内容 | 扱い | 行き先 | 確かめる語 |
| --- | --- | --- | --- | --- |
| What you will know after this session | The opening question became a sentence that says what registration does. The list of what the deck covers gained what follows registration and what the call deck uses, and a pointer to the chapters of the book | 再編集 | `slides/decks/en/04-registration.md#what-this-deck-covers` | `Three things that registration settles`、`Why there are two REGISTERs`、`docs/guide/en/registration.md` |
| How the session goes | The steps of starting the system and reading the capture. They are in the guide, and the deck points to it as an optional reproduction | 移動 | `docs/guide/en/registration.md#open-the-capture`、`slides/decks/en/04-registration.md#if-you-want-to-run-it` | `Flow Graph`、`capture.md` |
| Run it | Starting the system, checking the Diameter connections, a registration that deregisters after 5 seconds, and the three lines of the UE's log. The same section of the guide has the same commands and explanation | 移動 | `docs/guide/en/registration.md#run-a-registration`、`slides/decks/en/04-registration.md#if-you-want-to-run-it` | `docker compose run --rm ue register -deregister-after 5s`、`I-Open`、`"msg":"reg event"`、`"msg":"deregistered"`、`Tc`、`Run a registration` |
| Run it | The comment after a command. The guide says the same in the text of the step | 削除 |  | `docker compose logs scscf \| grep I-Open        # are the Diameter connections open?` |
| Open the capture | The display filter, what stays and what goes, how to draw the Flow Graph, and the numbers of the guide | 移動 | `docs/guide/en/registration.md#open-the-capture`、`docs/guide/en/registration.md#the-sequence`、`slides/decks/en/04-registration.md#if-you-want-to-run-it` | `sip \|\| diameter.cmd.code >= 300 \|\| dns.qry.type == 35 \|\| dns.qry.type == 33`、`Limit to display filter`、`watchdogs`、`Open the capture` |
| Check it in the code | The page that ended in "search" and "find" now names the file and the functions that decide, for each node. What the functions decide is explained in the chapters of the book, with excerpts | 課題から解説へ | `slides/decks/en/04-registration.md#code`、`docs/book/en/registration-lifecycle.md#the-ue-deregisters` | `Functions that decide`、`bindingResponse`、`SIMPLIFIED:`、`deregister` |
| Looking back | Five questions. The answers are statements on the slides they concern and in the chapters of the book (the rows of the answers) | 課題から解説へ | `docs/book/en/registration.md#that-the-user-is-who-it-claims-to-be`、`docs/book/en/registration.md#the-routes-of-later-requests`、`docs/book/en/registration.md#what-follows-registration` | `The HSS does not give the password to the S-CSCF`、`The I-CSCF asks the HSS twice in one registration`、`no 401 comes back` |
| Looking back: answers | The answer to 1. The network gives the nonce in answer to the first, and the UE answers it in the second | 課題から解説へ | `slides/decks/en/04-registration.md#overview`、`docs/book/en/registration.md#that-the-user-is-who-it-claims-to-be` | `The second proves it`、`The first REGISTER says who the user is` |
| Looking back: answers | The answer to 2. The I-CSCF asks twice: first DIAMETER_FIRST_REGISTRATION with Server-Capabilities, then DIAMETER_SUBSEQUENT_REGISTRATION with Server-Name | 課題から解説へ | `slides/decks/en/04-registration.md#second-register`、`docs/book/en/registration.md#that-the-user-is-who-it-claims-to-be`、`docs/book/en/registration-two-registers.md#why-the-second-register-reaches-the-same-s-cscf` | `DIAMETER_SUBSEQUENT_REGISTRATION`、`Server-Name` |
| Looking back: answers | The answer to 3. The HSS does not give the password but H(A1) | 課題から解説へ | `slides/decks/en/04-registration.md#challenge`、`docs/book/en/registration.md#that-the-user-is-who-it-claims-to-be` | `H(A1)`、`does not give the password` |
| Looking back: answers | The answer to 4. The S-CSCF uses Path to send requests for the UE through its P-CSCF, and the UE uses Service-Route to send its own requests to its S-CSCF | 課題から解説へ | `slides/decks/en/04-registration.md#routes`、`docs/book/en/registration.md#the-routes-of-later-requests`、`docs/book/en/registration-routes.md#the-two-routes-that-registration-sets` | `Service-Route`、`the route of requests to the UE` |
| Looking back: answers | The answer to 5. The P-CSCF has an IP association for the UE and adds `ip-assoc-yes`, so no 401 comes back at a deregistration | 課題から解説へ | `slides/decks/en/04-registration.md#deregister`、`docs/book/en/registration-lifecycle.md#the-ue-deregisters` | `ip-assoc-yes`、`Why no 401 comes back` |
| Next session | A call is next, and it uses the routes and the IP association set up here. The two cleanup commands and how they differ | 移動 | `slides/decks/en/04-registration.md#what-this-deck-covers`、`slides/decks/en/04-registration.md#if-you-want-to-run-it` | `the Path, the Service-Route and the IP association`、`docker compose --profile ue down --rmi all -v --remove-orphans`、`call.md` |
| Watching it fail | The cell of the column "How to try it" that told the reader what to do now says what happens (the operator removes the subscription). The steps stay behind the link to the Lab guide | 削除 |  | `Remove the subscription of a registered user through the OAM, as "The network cancels a registration" in the Lab guide does.` |
| The same flow in the Lab | The instructions to open the screen and pick an identity became a description of what the panel shows. How the table is built, and that the tie to Cx is inferred, are in the chapter of the book | 再編集 | `slides/decks/en/04-registration.md#the-same-flow-in-the-lab`、`docs/book/en/registration-lifecycle.md#how-the-lab-shows-a-registration` | `Registrations`、`HEP`、`inference` |
