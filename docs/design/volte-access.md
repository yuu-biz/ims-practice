# VoLTE のアクセス網

この記録は VoLTE の手順を見せるためのアクセス網（EPC と PCRF のスタブ）と、IMS-AKA の扱いの設計です。調査の記録は [volte.md](../background/volte.md) にあります。

## 選んだこと

- 最初のアクセス網は EPC（4G）にしました。公開されている解説資料が多く、Diameter で一貫して見られるためです。
- EPC と PCRF はスタブにし、役割 `epc` に置きます。
- UE と EPC スタブのあいだは TS 24.301 の ESM のメッセージに合わせた HTTP の API にします。
- UE の IP アドレスは加入者ごとの静的なアドレスにします。
- IMS-AKA は既定では sec-agree と保護ポートの手順だけを行い、ESP をかけません。ESP（暗号化は null）は上書きの構成で有効にします。
- AKA の版は AKAv1-MD5（RFC 3310）だけにします。
- P-CSCF と PCRF のあいだは Diameter の Rx（TS 29.214）です。

| 主題 | 作り |
| --- | --- |
| UE と EPC スタブの境界 | UE の側に `AccessNetwork` のインタフェースを置き、EPC スタブへの HTTP のクライアントで実装します。メッセージの名前と項目は TS 24.301 の ESM のメッセージに合わせ、本文は JSON にします。bearer の活性化や解放のような網からの通知は、Server-Sent Events で送ります。NAS と S1 を省いたことを `SIMPLIFIED:` に書きます。SMS を NAS で届けるときは、同じインタフェースの後ろに NAS の実装を置きます。スタブを、インタフェースの境界で本格的な実装に差し替えられるようにするためです。 |
| UE の IP アドレス | 加入者ごとの静的なアドレス（TS 23.401 5.3.1.1）にし、今の 1 つのブリッジ（ims0）に置きます。TUN は使いません。TUN はコンテナに NET_ADMIN が要るので、Open5GS と UERANSIM の構成で扱います。1 つのブリッジのままなら、キャプチャ（[environment-capture.md](environment-capture.md)）を変えずに済み、ホスト固有のネットワーク設定にも依りません。GTP-U と SGi を省いたことを `SIMPLIFIED:` に書きます。 |
| IPsec | ESP は上書きの構成（`deploy/esp/`）で有効にし、既定は無効のままにします。暗号化は null にし、完全性の保護だけをかけます。TS 33.203 6.3 は完全性の保護をどの場合にも求め、秘匿は P-CSCF の方針が求めるときだけです（6.2）。`ealg=null` は sec-agree が運ぶ値の 1 つなので（Annex H）、実際の網にある組み合わせです。カーネルへの設定は `internal/ipsec` が `ip xfrm` のコマンドで行います。学習者が同じコマンドを手で打って `ip xfrm state` で結果を見られるようにするためで、netlink のライブラリを足さずに済みます。P-CSCF と UE のコンテナには NET_ADMIN が要ります。既定にしない理由は下の「ESP を既定にしない理由」にあります。 |
| 平文の AKA | 既定では sec-agree（Security-Client、Security-Server、Security-Verify）と保護ポートの手順はそのまま行い、ESP だけを省きます。学習者がキャプチャで sec-agree の手順を見られるようにするためです。sec-agree を行って ESP だけを省く形は標準に無いので、`SIMPLIFIED:` に理由を書きます。IPsec を使わない AKA は、標準では TLS の上の AKAv2-SHA-256 だけです（TS 24.229 4.2B.1）。ESP を有効にすると、暗号化が null なのでキャプチャの中身は読めたまま、組み合わせは標準どおりになります。 |
| AKA の版 | AKAv1-MD5（RFC 3310）だけにします。独立した試験相手の SIPp（[ue-simulator.md](ue-simulator.md)）が AKAv2 に対応していないので、同じ AKAv1-MD5 で比べるためです。TS 33.203 6.1.1 は、S-CSCF が AKAv2-SHA-256 と AKAv1-MD5 の両方を示し、UE が AKAv2-SHA-256 に対応することを求めています。AKAv2 を省いたことを `SIMPLIFIED:` に書きます。 |
| PCRF スタブ | 役割 `epc` の中に置きます。PGW のスタブとのあいだの Gx はプロセスの中の関数の呼び出しにし、`SIMPLIFIED:` に書きます。P-CSCF とのあいだの Rx は Diameter（TS 29.214）です。 |
| EPC を通らない UE | ホストのソフトフォンのように EPC を通らない UE では、PCRF が IP-CAN_SESSION_NOT_AVAILABLE を返します。そのとき P-CSCF は、その UE の呼で Rx を使わずに進めます。 |
| P-CSCF のダイアログ | P-CSCF は、自分で BYE を作るのに要るダイアログの情報（相手の Contact、To と From、CSeq、Route）を `Store` の後ろに置きます。bearer を失ったときに、網の側から呼を切るためです（TS 24.229 5.2.8.1.2）。どの台でもこの情報を読めるように、`Store` の後ろに置きます（[state-redundancy.md](state-redundancy.md)）。 |

コーデック（AMR と AMR-WB、UE の offer の順）は [codecs.md](codecs.md) にあります。

## ESP の試作の結果

ESP（暗号化は null）を上書きの構成で有効にする形を試しました。

| 確かめたこと | 結果 |
| --- | --- |
| TS 33.203 が null の暗号化を認めるか | 認めます。Annex H の `ealg` の値に `null` があり、`ealg` が無いときの既定も null です。6.2 は秘匿を P-CSCF の方針が求めるときだけかけるものとし、7.2 は UE が秘匿のアルゴリズムを示さなかったときに P-CSCF が null を選ぶか手順を中断するかを方針で決めるとしています。完全性の保護は 6.3 がどの場合にも求めます。 |
| 鍵の導出 | Annex I が、HMAC-SHA2-256-128 のとき IK_ESP を TS 33.220 Annex B.2.0 の鍵導出関数で作ります。入力の鍵は CK \|\| IK、S は FC = 0x5A、P0 = "HMAC-SHA2-256-128"、L0 = 0x0011 で、出力は 256 ビットです。これは RFC 4868 の AUTH_HMAC_SHA2_256_128 の鍵長と一致します。 |
| カーネルへの入れ方 | `ip xfrm` のコマンドを 1 つずつ実行します。寿命はカーネルに渡さず、SIP のアプリケーションが持ちます（TS 33.203 7.1 NOTE 4、7.4）。1 回の認証で state が 4 つ、policy が 8 つ（4 つのアソシエーション × UDP と TCP）です。policy から state を指すのは reqid で、SPI と同じ値を入れます。同じ 2 つのアドレスの間に出ていく state が 2 つあり、ポートと SPI しか違わないためです。`ip xfrm` のセレクタはポートを `proto udp` か `proto tcp` の下でしか書けないので、policy をトランスポートごとに分けます。`-batch` でまとめて渡すのはやめました。理由は下の「カーネルで分かったこと」にあります。 |
| 保護クライアントポート | 取り決めるだけでなく開きます。アソシエーションの組は、片方の端のクライアントポートともう片方のサーバーポートの間にあるので（7.1）、ほかのポートから出たパケットはどのアソシエーションにも当たりません。`internal/sip` の transport に、要求を送るためのクライアントポートを足しました。応答は、要求が届いたポートから返します。 |
| Go の試験（この環境） | 通ります。合意からカーネルのパラメータへの変換と IK_ESP の導出は純粋な関数で、`internal/ipsec` の試験で確かめます。P-CSCF と UE が、challenge のとき・登録を解除したとき・認証に失敗したときにカーネルを呼ぶことも試験で確かめます。 |
| カーネルでの動作（この環境） | 確かめられません。`ip xfrm state add` が「Requested type not found」で失敗します。ESP の xfrm がこのカーネルに無いためです。`ecb(cipher_null)` は `/proc/crypto` にあり、`ip` も ESP には `enc` か `aead` が要ると言うので、コマンドの形だけは確かめました。 |
| Wireshark での読み方 | ESP の設定で完全性のアルゴリズムを教えると、中の SIP を読めます。ESP SAs の表に `IPv4,*,*,*,NULL,,HMAC-SHA-256-128 [RFC4868],` の 1 行を足し、`esp.enable_encryption_decode` を入れます。鍵はどちらも要りません。暗号化は null なので復号するものが無く、完全性の検査は別の設定です。設定が無ければ、キャプチャには `ESP (SPI=0x...)` しか出ません。 |
| 推定の設定だけで読めるか | 読めます。`esp.enable_null_encryption_decode_heuristic`（GUI では **Attempt to detect/decode NULL encrypted ESP payloads**）は、完全性チェック値の長さを 12、16、24、32 オクテットの順に試し、padding と次ヘッダが整う最初の長さを採ります。tshark 4.2.2 の設定の説明文にそう書いてあり、`libwireshark.so.17` の文字列でも確かめました。16 オクテットの HMAC-SHA2-256-128 の ESP パケットを 500 個組み立てて与えると、500 個とも中の SIP を読みました。パケットを組み立てたのは、この環境のカーネルに ESP の xfrm が無く（`state add` が `Requested type not found`）、カーネルに作らせられないためです。完全性チェック値を 0 で埋めたパケットでは読めません。12 オクテットと見た場合の padding と次ヘッダがどちらも 0 になり、推定がそこで止まるからです。本物の完全性チェック値は HMAC を切り詰めた値なので、この止まり方はしません。それでも長さを推定することに変わりはないので、確認の側（`scripts/e2e-esp.sh`）はアルゴリズムを明示する ESP SAs の行を使います。 |
| Compose と CI | `scripts/e2e-esp.sh` と CI の `e2e-esp` のジョブで確かめます。Docker の無い環境で作ったので、Compose での結果はありません。CI では、カーネルが 4 つのアソシエーションと 8 つの policy を持ち、REGISTER、INVITE、200 (OK)、BYE が ESP の中を通り、登録を解除したあとにアソシエーションが残らないことを確かめました。そこに至るまでに直したことは、下の「カーネルで分かったこと」にあります。 |
| Windows の Docker Desktop | 通ります。Windows 11 Pro 25H2、Docker Desktop 4.94.0（Engine 29.8.2、Compose v5.5.1）、バックエンドは WSL2 で、カーネルは 6.18.33.1-microsoft-standard-WSL2 です。`docker compose -f compose.yaml -f deploy/esp/esp.yaml up -d --build` のあとで `scripts/e2e-esp.sh` を流すと、17 項目がすべて通りました。ESP のパケットの数、ESP の中の REGISTER、INVITE、200 (OK)、BYE の数、state と policy の形は、CI と同じでした。登録を解除したあとには、state も policy も残りませんでした。このカーネルでは `CONFIG_XFRM_USER` と `CONFIG_INET_ESP` がモジュールですが、`NET_ADMIN` を付けたコンテナで `ip xfrm` を使うと、手で読み込まなくても読み込まれました。 |

## ESP を既定にしない理由

試作が通ったあとも、ESP は上書きの構成（`deploy/esp/`）で有効にする形のままにし、既定にはしません。理由は 3 つです。

- 要件では、既定で信号を暗号化しません。キャプチャをそのまま読めるようにするためです。ESP の暗号化は null で、かかるのは完全性の保護だけですが、それでも Wireshark に ESP の中を読ませる設定が要ります。設定が無ければ、見えるのは `ESP (SPI=0x...)` だけです。既定の構成は、設定なしで読める状態に保ちます。
- ESP はホストのカーネルの xfrm に頼ります。Linux と、WSL2 を使う Windows の Docker Desktop では通りました。macOS の Docker Desktop とクラウドの環境では確かめていません。要件の「ホスト固有のネットワーク設定に依存しない」を、既定の構成で崩さないためです。
- 実際の VoLTE の端末は ESP を使います（GSMA IR.92 の IMS-AKA と sec-agree）。この動きは、上書きの構成で有効にすれば見られます。既定にしなくても学べるので、既定を平文のままにする方を採ります。

手順書（`docs/guide/{en,ja}/volte.md`）は、有効にする手順、キャプチャの読み方、ESP が動く環境を、学習者向けに書きます。

## カーネルで分かったこと

CI の実行と、xfrm を持たないカーネルでの `ip` の試しで分かったことです。コマンドの形は、ここに書いた形に直しました。

| 分かったこと | 直し方 |
| --- | --- |
| `ip -batch` の各行は `ip` のコマンドそのもので、オブジェクトの名前から始まります。`state add ...` とだけ書くと `Object "state" is unknown` で失敗します。 | 各コマンドを `xfrm` から書きます。すべてのコマンドが `xfrm state` か `xfrm policy` で始まることを試験で確かめます。 |
| `ip` は `state update` を XFRM_MSG_UPDSA として送ります。カーネルはその SPI の state が無ければ ESRCH を返すので、新しい SPI を update で作ることはできません。 | state は `add` で作ります。同じ SPI の state が残っていると `add` は失敗するので、その SPI の `deleteall` を前に置きます。policy の `update` は、無ければ作るので、そのままにします。 |
| `enc ecb(cipher_null) 0x` の `0x` は、iproute2 では 2 文字の鍵として読まれます。16 進として読むのは 2 文字より長いときだけです。 | 空の引数を渡します。`ip -batch` が引用符の中の空文字列を空の引数にすることは、`link show dev ""` の出力で確かめました。 |
| `state deleteall src A dst B proto esp` は、その 2 つのアドレスの間の ESP の state をすべて消します。再登録の途中では、新しい組まで消えます。 | state は SPI ごとに消します。 |
| `ip -force -batch` は、最初に失敗したコマンドで止まります。`ip` は rtnl の失敗で `exit(2)` するためで、`-force` は効きません。 | 失敗しうるコマンドを並べません。消すときは `deleteall` を使います。これは消すものが無くても成功します。 |
| 1 回の `ip -batch` の中では、最初の `deleteall` しか効きません。8 つの `policy deleteall` を渡すと 1 つだけ消えました。`ip` が dump と delete に同じ netlink のソケットを使うためと見られます。 | コマンドを 1 つずつ実行します。消すときは最後まで続け、失敗はログに書きます。 |
| policy のコマンドは、xfrm の ESP が無いカーネルでも通ります。 | 生成したコマンドをそのまま実行し、8 つの policy が入り、`deleteall` で 8 つとも消えることを確かめました。state の方は `Requested type not found`（カーネルに ESP の xfrm が無い）までで、構文は通ります。 |
| Wireshark は、ESP の中の TCP を SIP として読むのに手がかりが要ります。SIP が登録されているのはポート 5060 だけです。 | キャプチャの確認と手順書のコマンドに `-d tcp.port==5062,sip -d tcp.port==5064,sip` を付けます。 |
| `limit time-hard` は、state を足した時刻から数えます（`xfrm_timer_handler` が `lft.hard_add_expires_seconds` に `curlft.add_time` を足します）。state の update は限度を置き換えますが、この時刻は置き換えません（`xfrm_state_update` は `lft` だけを写します）。登録を更新しても、寿命は最初の認証のときのままです。 | 寿命をカーネルに渡すのをやめました。TS 33.203 7.1 NOTE 4 と 7.4 が、寿命を SIP のアプリケーションに任せています。P-CSCF は UE ごとにタイマーを 1 つ持ち、set を入れ替えるときにそのタイマーも入れ替えます。 |
| policy にも `limit time-hard` を付けていたので、登録が続いているのに policy だけが消えていました。`ip xfrm policy update ... limit time-hard 3` で確かめました。 | 同じく、寿命を渡しません。policy は、set を消すときと、次の set で上書きするときに動きます。 |
| 1 つの UE の新旧の set は同じポートに結び付くので、selector で消す policy の削除は、入れ替わった新しい set の policy まで消します。 | set を入れ替えるときは state だけを消し、policy は新しい set の `update` に上書きさせます。タイマーは set ごとなので、古い set のタイマーが新しい set を消すこともありません。 |

## 残っていること

- ノードが落ちて立ち上がり直すと、前の実行が入れた state と policy がカーネルに残ります。カーネルはプロセスより長く生きるためです。UE が登録し直すと policy は上書きされ、state は SPI ごとに消されますが、前の実行の SPI の state はそのまま残ります。全体を消す `ip xfrm state flush` は、同じ netns のほかの用途まで消すので使いません。
- UE ごとのタイマーは 1 つのプロセスの中にあります。アソシエーションもそのホストのカーネルにしかないので、この 2 つは同じ場所にあります（`SecurityAssociations.PCSCF`）。

## 採らなかった案

調査は、S-CSCF が AKAv2-SHA-256 と AKAv1-MD5 の両方を出し、UE シミュレータは AKAv2 を選ぶ案を勧めていました。上の「AKA の版」の理由で、AKAv1-MD5 だけにしました。
