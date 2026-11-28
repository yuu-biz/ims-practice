# UE シミュレータ

この記録は UE をどう用意するかの設計です。自作の UE シミュレータと、独立した試験相手として使う既存の道具の役目を書きます。

## 選んだこと

- UE は自作の UE シミュレータを基本にします。IMS 固有のヘッダや IMS-AKA の手順を見せるためです。
- ソフトフォン（Linphone など）は、マイクとスピーカーで会話を試すための補助として、SIP Digest 認証で使います。
- SIPp と pjsua を、自作の UE とは独立した試験相手として使います。`images/softphone` のイメージに pjsua と SIPp を入れてあります。

## 既存の UE が IMS-AKA にどこまで対応しているか

最初は、一般的なソフトフォンは IMS-AKA に対応していないと考えていました。調べると、前提の一部が違いました。

- PJSIP（pjsua）は `PJSIP_HAS_DIGEST_AKA_AUTH` を有効にしてビルドすると、K、OP、AMF を設定して AKAv1-MD5 で登録できます。
- SIPp もシナリオの `[authentication aka_K=... aka_OP=... aka_AMF=...]` で AKAv1-MD5 を使えます。
- どちらも sec-agree と IPsec には対応していません。
- Linphone、baresip、MicroSIP、Zoiper では、AKA に対応しているという資料が見つかりませんでした。

## 自作を続ける理由

sec-agree と IPsec を含む IMS-AKA の全体を見せられるのは、自作の UE だけです。

## 独立した試験相手を置く理由

UE と CSCF の両方を自作すると、同じ誤解が両方に入っても試験が通ってしまいます。SIPp で AKA の登録が通ることを確かめれば、その誤りを見つけられます。

Alpine の pjsua（画面の無いソフトフォン）も、同じ理由で試験相手に加えています。e2e 試験では、pjsua が SIP Digest で登録し、UE シミュレータからの着信に応答し、UE シミュレータに発信します。

SIPp が AKAv1-MD5 しか話さないことは、AKA の版の選び方にも関わります（[volte-access.md](volte-access.md)）。
