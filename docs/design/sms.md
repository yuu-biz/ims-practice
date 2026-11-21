# SMS

この記録は SMS over IP をどの役割で運ぶかの設計です。調査の記録は [sms.md](../background/sms.md) にあります。

## 選んだこと

- SMS は `ims` コマンドの役割 `ipsmgw`（IP-SM-GW）と、その中の SC のスタブで運びます。
- IP-SM-GW は Go のインタフェースを通して SC を呼びます。この境界の後ろの実装を差し替えられます。
- S-CSCF が iFC で IP-SM-GW を呼び出します。

| 主題 | 作り |
| --- | --- |
| IP-SM-GW の置き場所 | `ims` コマンドの役割 `ipsmgw` にします。AS の URI は DNS で引くので、複数台で動かせます。 |
| SC | IP-SM-GW と同じプロセスの中のスタブにします。TS 24.341 B.5 の例も、IP-SM-GW が SC に直接届くと仮定しています。SGd、S6c、蓄積と再送は作らず、`SIMPLIFIED:` に書きます。 |
| SC との境界 | IP-SM-GW は Go のインタフェースを通して SC を呼びます。SC は周辺ノードのスタブなので、インタフェースの境界で本格的な実装に差し替えられるようにします。SC を独立した役割にし、SGd と S6c で通信するときは、このインタフェースを Diameter のクライアントで実装します。境界の後ろの実装を替えるだけなので、IP-SM-GW の作りは変わりません。 |
| MT の送り先 | IP-SM-GW は MESSAGE を網の入口の I-CSCF へ送ります。I-CSCF が LIR で S-CSCF を探すので、通話の着信と同じ流れになります。 |
| 受信者の決め方 | SC のスタブが、TP-DA の番号から Sh の UDR で IMPU を引きます。SRI-for-SM の代わりであることを `SIMPLIFIED:` に書きます。 |
| iFC の範囲 | Priority、CNF と DNF、ConditionNegated、SPT の Method、SIPHeader、SessionCase、RequestURI、DefaultHandling、ServiceInfo、第三者 REGISTER に対応します。SessionCase は、未登録の利用者の場合を含めて扱います。SessionDescription と RegistrationType は作らず、`SIMPLIFIED:` に書きます。 |
| ODI の状態 | ODI はランダムな ID にし、続きの評価に要る状態を S-CSCF の `Store` に期限付きで置きます。 |
| IP-SM-GW の状態 | 配達の途中の状態を `Store` の後ろに置きます。どの台が delivery report を受けても処理できます。外部の保存先は [state-redundancy.md](state-redundancy.md) にあります。 |
| 着信側の iFC | ほかの網から届いた短メッセージを、利用者を担当する IP-SM-GW に渡すため、着信側（SessionCase が 1）の MESSAGE を拾う iFC を 1 つ入れます（TS 23.204 6.4a）。P-Asserted-Identity が自網の IP-SM-GW の URI である MESSAGE は、否定の条件で除きます。この条件が無いと、IP-SM-GW が出した MESSAGE が IP-SM-GW に戻るためです（TS 23.218 5.2.3 NOTE 5）。 |
| HSS への登録 | IP-SM-GW は第三者 REGISTER を受けると、Sh の PUR で HSS に自分のアドレスを登録します。MAP は使いません。 |
| 文字の符号化 | GSM 7 ビットの既定アルファベットと UCS2 に対応します。連結 SMS（UDH）と有効期間は作らず、`SIMPLIFIED:` に書きます。 |

## SC を境界の後ろに置く理由

SC のスタブを、差し替えられる境界に含めました。インタフェースが 1 つ増えるだけで、あとで SGd に替える道を残せるためです。

## 採らなかった案

ODI の状態をトークンに埋め込む案は採りません。署名の鍵を全台で配る仕組みが要り、学習者がキャプチャで見る値も長くなるためです。
