---
title: UE が IMS に登録する流れ
footer: ims-practice 4. 登録
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: ue, label: UE, role: 登録する端末, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: 最初の中継点, at: [1, 1]}
  - {id: dns, label: DNS, role: 名前を引く, at: [1, 0]}
  - {id: icscf, label: I-CSCF, role: ホーム網の入口, at: [2, 0]}
  - {id: hss, label: HSS, role: 加入者のデータ, at: [3, 0]}
  - {id: scscf, label: S-CSCF, role: 認証と登録, at: [3, 1]}
  - {id: ipsmgw, label: IP-SM-GW, role: SMS の AS, at: [4, 1]}
events:
  # 1 回目の REGISTER（手順 1 から 4）
  - id: reg1-ue-p
    msg: REGISTER
    from: ue
    to: pcscf
    proto: SIP
    note: (1 回目)
    fields:
      - Request-URI: "`sip:ims.mnc001.mcc001.3gppnetwork.org` ホーム網のドメイン"
      - From, To: 登録する公開の識別子
      - Contact: "ユーザー部に UUID。`+g.3gpp.smsip` と `expires=600000`"
      - Authorization: "`username` に秘密の識別子。`nonce` と `response` は空"
  - id: dns-naptr
    msg: NAPTR
    from: pcscf
    to: dns
    proto: DNS
    note: (問い合わせ)
    fields:
      - 名前: "`ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-naptr-ans
    msg: NAPTR
    from: dns
    to: pcscf
    proto: DNS
    note: (答え)
    fields:
      - 答え: "UDP の SIP は `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-srv
    msg: SRV
    from: pcscf
    to: dns
    proto: DNS
    note: (問い合わせ)
    fields:
      - 名前: "`_sip._udp.ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-srv-ans
    msg: SRV
    from: dns
    to: pcscf
    proto: DNS
    note: (答え)
    fields:
      - 答え: "`icscf.ims.mnc001.mcc001.3gppnetwork.org` のポート 5060"
  - id: dns-a
    msg: A
    from: pcscf
    to: dns
    proto: DNS
    note: (問い合わせ)
    fields:
      - 名前: "`icscf.ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-a-ans
    msg: A
    from: dns
    to: pcscf
    proto: DNS
    note: (答え)
    fields:
      - 答え: 172.28.0.20
  - id: pcscf-path
    do: ヘッダを足す
    at: pcscf
    fields:
      - Path: "`sip:term@pcscf...` 着信がここを通って戻る"
      - Require: "`path`"
      - P-Charging-Vector: "新しい `icid-value`"
      - P-Visited-Network-ID: P-CSCF がいる網の名前
  - id: reg1-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (1 回目)
  - id: uar1
    msg: UAR
    from: icscf
    to: hss
    proto: Cx
    note: (1 回目)
    fields:
      - User-Name: 秘密の識別子
      - Public-Identity: 公開の識別子
      - Visited-Network-Identifier: P-CSCF の網
      - User-Authorization-Type: REGISTRATION (0)
  - id: uaa1
    msg: UAA
    from: hss
    to: icscf
    proto: Cx
    note: (1 回目)
    fields:
      - Experimental-Result: DIAMETER_FIRST_REGISTRATION (2001)
      - Server-Capabilities: 必須の能力 1。S-CSCF の名前ではない
  - id: icscf-select
    do: S-CSCF を選ぶ
    at: icscf
    fields:
      - 求められた能力: 必須の能力 1
      - 設定の候補: "`scscf.ims.mnc001.mcc001.3gppnetwork.org` は能力 1 を持つ"
      - 選んだもの: その S-CSCF。I-CSCF は覚えない
  - id: reg1-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (1 回目)
    fields:
      - Request-URI: "`sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060` に書き換わる"
      - Via: UE、P-CSCF、I-CSCF の 3 つ
  # 認証の要求（手順 5 と 6）
  - id: mar
    msg: MAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - 3GPP-SIP-Authentication-Scheme: "`Unknown` 方式の選択を HSS に任せる"
  - id: maa
    msg: MAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - 3GPP-SIP-Authentication-Scheme: "`SIP Digest`"
      - Digest-Realm: レルム
      - Digest-Algorithm: "`MD5`"
      - Digest-QoP: "`auth`"
      - Digest-HA1: H(A1)。パスワードそのものではない
  - id: hss-assign
    do: 担当の S-CSCF を記録する
    at: hss
    fields:
      - 記録すること: この S-CSCF が担当。認証中の印を付ける
      - 使われるとき: 2 回目の UAR への答え
  - id: scscf-challenge
    do: nonce を作り、タイマーを始める
    at: scscf
    fields:
      - nonce: S-CSCF が作る
      - reg-await-auth: 4 分
  - id: r401-s-i
    msg: "401"
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - WWW-Authenticate: 'レルム、`nonce`、`algorithm=MD5`、`qop="auth"`'
  - id: r401-i-p
    msg: "401"
    from: icscf
    to: pcscf
    proto: SIP
  - id: r401-p-ue
    msg: "401"
    from: pcscf
    to: ue
    proto: SIP
    fields:
      - Via: UE 自身の Via だけが残る
  # 2 回目の REGISTER（手順 7 と 8）
  - id: reg2-ue-p
    msg: REGISTER
    from: ue
    to: pcscf
    proto: SIP
    note: (2 回目)
    fields:
      - Call-ID: 1 回目と同じ
      - CSeq: 1 つ増える
      - Authorization: "`nonce`、`cnonce`、`nc`、`qop`、`response` が入る"
  - id: pcscf-pending
    do: ip-assoc-pending を付ける
    at: pcscf
    fields:
      - Authorization に足すもの: '`integrity-protected="ip-assoc-pending"`'
      - 意味: 認証への答えで、IP の対応付けはまだ無い
  - id: reg2-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (2 回目)
  - id: uar2
    msg: UAR
    from: icscf
    to: hss
    proto: Cx
    note: (2 回目)
    fields:
      - User-Authorization-Type: REGISTRATION (0)
  - id: uaa2
    msg: UAA
    from: hss
    to: icscf
    proto: Cx
    note: (2 回目)
    fields:
      - Experimental-Result: DIAMETER_SUBSEQUENT_REGISTRATION (2002)
      - Server-Name: MAR のときに HSS が記録した S-CSCF
  - id: reg2-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (2 回目)
    fields:
      - トランスポート: 1300 バイトを超えるので TCP
      - Via: "I-CSCF の Via が `SIP/2.0/TCP` になる"
  # 登録の確定（手順 9 と 10）
  - id: scscf-verify
    do: 答えを確かめる
    at: scscf
    fields:
      - Call-ID: 認証を求めた REGISTER と同じか
      - レルムと nonce: 自分が出したものか
      - response: H(A1) から計算した値と一致するか
  - id: sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Server-Assignment-Type: REGISTRATION (1)
  - id: saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - User-Data: "利用者のプロファイル（XML）。Wireshark では `Cx-User-Data`"
      - 中身: 一緒に登録される識別子と iFC
  - id: ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - Path: S-CSCF が記録した経路
      - Service-Route: "`sip:orig@scscf...`"
      - P-Associated-URI: 一緒に登録された識別子。最初が既定
      - Contact: "`+g.3gpp.smsip` と `expires=3600`"
      - Authentication-Info: "`rspauth` 網も H(A1) を知っている証明"
  - id: ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: pcscf-store
    do: 経路と対応付けを覚える
    at: pcscf
    fields:
      - IP の対応付け: UE のアドレスについて作る
      - 記録するもの: Service-Route と識別子
  - id: ok-p-ue
    msg: 200 OK
    from: pcscf
    to: ue
    proto: SIP
  # 登録のあとで使う経路
  - id: route-path-store
    do: Path を保存する
    at: scscf
    fields:
      - 保存するもの: "Contact と Path の `sip:term@pcscf...`"
      - 使うとき: この UE あての要求を送るとき
  - id: route-sr-store
    do: Service-Route を保存する
    at: ue
    fields:
      - 保存するもの: "200 OK の Service-Route の `sip:orig@scscf...`"
      - 使うとき: UE が新しい要求を出すとき
  - id: mt-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    note: (着信)
    fields:
      - Request-URI: UE が登録した Contact
      - Route: "保存した Path の `sip:term@pcscf...`"
  - id: mt-p-ue
    msg: INVITE
    from: pcscf
    to: ue
    proto: SIP
    note: (着信)
    fields:
      - Route: P-CSCF が自分の値を取り除き、残りは無い
  - id: mo-ue-p
    msg: INVITE
    from: ue
    to: pcscf
    proto: SIP
    note: (発信)
    fields:
      - Request-URI: "相手の番号 `tel:+1-202-555-0102`"
      - Route: "P-CSCF の URI と、保存した Service-Route の `sip:orig@scscf...`"
  - id: mo-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    note: (発信)
    fields:
      - Route: P-CSCF が自分の値を取り除き、残りが Service-Route と一致するかを確かめる
  # P-CSCF の購読（手順 11 と 12）
  - id: psub-p-i
    msg: SUBSCRIBE
    from: pcscf
    to: icscf
    proto: SIP
    fields:
      - Request-URI, To: 既定の公開の識別子
      - Event: "`reg`"
      - Expires: 3761。登録の 3600 秒より長い
      - P-Asserted-Identity: "`sip:term@pcscf...`"
  - id: lir1
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
  - id: lia1
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    fields:
      - Server-Name: 利用者の S-CSCF
  - id: psub-i-s
    msg: SUBSCRIBE
    from: icscf
    to: scscf
    proto: SIP
  - id: psub-ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: psub-ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: pnotify
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    fields:
      - Subscription-State: "`active;expires=3761`"
      - Content-Type: "`application/reginfo+xml`"
      - 本文: 識別子ごとの registration、登録先ごとの contact
  # UE の購読（手順 13 と 14）
  - id: usub-ue-p
    msg: SUBSCRIBE
    from: ue
    to: pcscf
    proto: SIP
    fields:
      - Event: "`reg`"
      - Expires: 600000
      - Route: P-CSCF と Service-Route
  - id: usub-p-s
    msg: SUBSCRIBE
    from: pcscf
    to: scscf
    proto: SIP
    fields:
      - 経路: Service-Route に沿って S-CSCF へ。I-CSCF を通らない
  - id: unotify-s-p
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    fields:
      - Route: P-CSCF。SUBSCRIBE の Record-Route から作られた
  - id: unotify-p-ue
    msg: NOTIFY
    from: pcscf
    to: ue
    proto: SIP
    fields:
      - Record-Route: P-CSCF が付ける。UE はこれで購読の経路を作る
  # 第三者 REGISTER（手順 15 から 18）
  - id: tp-register
    msg: REGISTER
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (第三者)
    fields:
      - Request-URI: "iFC の ServerName `sip:ipsmgw...:5060`"
      - From, Contact: S-CSCF
      - To: 既定の公開の識別子
      - Expires: 3600
      - 本文: "`application/3gpp-ims+xml` に MSISDN `12025550101`"
  - id: tp-ok
    msg: 200 OK
    from: ipsmgw
    to: scscf
    proto: SIP
  - id: isub-ip-i
    msg: SUBSCRIBE
    from: ipsmgw
    to: icscf
    proto: SIP
    fields:
      - Expires: 3761
      - P-Asserted-Identity: IP-SM-GW の URI。iFC の ServerName と同じ
  - id: lir2
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
  - id: lia2
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
  - id: isub-i-s
    msg: SUBSCRIBE
    from: icscf
    to: scscf
    proto: SIP
  - id: inotify
    msg: NOTIFY
    from: scscf
    to: ipsmgw
    proto: SIP
  - id: ipsmgw-check
    do: SMS を受けられる登録先を探す
    at: ipsmgw
    fields:
      - 探すもの: '`<unknown-param name="+g.3gpp.smsip">` を持つ active の contact'
  - id: pur
    msg: PUR
    from: ipsmgw
    to: hss
    proto: Sh
    fields:
      - User-Identity: MSISDN `12025550101`
      - Data-Reference: SMSRegistrationInfo (24)
      - Sh-User-Data: "`<IP-SM-GW-Number>` に `12025550199`"
  - id: pua
    msg: PUA
    from: hss
    to: ipsmgw
    proto: Sh
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  # 登録の解除
  - id: dreg-ue-p
    msg: REGISTER
    from: ue
    to: pcscf
    proto: SIP
    note: (解除)
    fields:
      - Contact: "`expires=0`"
  - id: dreg-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (解除)
    fields:
      - Authorization に足すもの: '`integrity-protected="ip-assoc-yes"`'
  - id: duar
    msg: UAR
    from: icscf
    to: hss
    proto: Cx
    note: (解除)
    fields:
      - User-Authorization-Type: DE_REGISTRATION (1)
  - id: duaa
    msg: UAA
    from: hss
    to: icscf
    proto: Cx
    note: (解除)
  - id: dreg-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (解除)
  - id: dsar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    note: (解除)
    fields:
      - Server-Assignment-Type: USER_DEREGISTRATION (5)
  - id: dsaa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    note: (解除)
  - id: dok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (解除)
  - id: dok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
    note: (解除)
  - id: dok-p-ue
    msg: 200 OK
    from: pcscf
    to: ue
    proto: SIP
    note: (解除)
    fields:
      - Contact: "`expires=0`"
  - id: dnotify-s-p
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    note: (最後)
    fields:
      - registration と contact: "`terminated`"
      - contact の event: "`unregistered`"
      - Subscription-State: "`terminated;reason=noresource`"
  - id: dnotify-p-ue
    msg: NOTIFY
    from: pcscf
    to: ue
    proto: SIP
    note: (最後)
  # 失敗
  - id: wp-verify
    do: response が一致しない
    at: scscf
  - id: wp-403-s-i
    msg: "403"
    from: scscf
    to: icscf
    proto: SIP
  - id: wp-403-i-p
    msg: "403"
    from: icscf
    to: pcscf
    proto: SIP
  - id: wp-403-p-ue
    msg: "403"
    from: pcscf
    to: ue
    proto: SIP
  - id: wp-sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    note: (失敗)
    fields:
      - Server-Assignment-Type: AUTHENTICATION_FAILURE (9)
  - id: wp-clear
    do: 記録した S-CSCF を消す
    at: hss
    fields:
      - 次の試行: また DIAMETER_FIRST_REGISTRATION から始まる
  - id: rtr
    msg: RTR
    from: hss
    to: scscf
    proto: Cx
    fields:
      - Reason-Code: PERMANENT_TERMINATION (0)
      - User-Name: 秘密の識別子
  # 遅れて届いた登録の解除（説明のための例）
  - id: ord-stored
    do: 登録を保存している
    at: scscf
    fields:
      - Call-ID: A（例のために短く書いた値）
      - CSeq: 5（再登録で保存した値）
      - Contact: C
      - 期限: 再登録で延びた期限
  - id: ord-late
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (遅れた解除)
    fields:
      - Call-ID: A
      - CSeq: "3"
      - Contact: "C に `expires=0`"
  - id: ord-compare
    do: 順序を比べる
    at: scscf
    fields:
      - Call-ID: 保存した値と同じ A
      - CSeq: 3 は保存した 5 より大きくない
      - 判断: この要求で登録を変えない
  - id: ord-500
    msg: "500"
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - 応答のコード: RFC 3261 10.3 はコードを定めない。この実装は 500
  - id: ord-kept
    do: 登録をそのまま残す
    at: scscf
    fields:
      - Contact と期限: 変えない
      - CSeq: 5 のまま
      - HSS: 解除の SAR を送らない
  - id: ord-other
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (別の Call-ID)
    fields:
      - Call-ID: B
      - CSeq: 2（401 への答え）
  - id: ord-other-compare
    do: CSeq を比べない
    at: scscf
    fields:
      - Call-ID: 保存した A と違う B
      - 判断: 認証を確かめ、再登録として進む
  - id: ord-other-sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Server-Assignment-Type: RE_REGISTRATION (2)
  - id: ord-other-saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
  - id: ord-other-ok
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - 保存する値: Call-ID B と CSeq 2。次の要求はこの値と比べる
  - id: rej-s-p
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    note: (rejected)
    fields:
      - contact の event: "`rejected`"
  - id: rej-p-ue
    msg: NOTIFY
    from: pcscf
    to: ue
    proto: SIP
    note: (rejected)
  - id: rta
    msg: RTA
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
```

---

# UE が IMS に登録する流れ

（SIP Digest での登録と、アプリケーションサーバーへの第三者 REGISTER）

---

[layout: summary]

## このデッキで分かること

電話の電源を入れてから最初の通話までに、端末は IMS に登録します。このデッキは、その登録を 1 区間ずつ見せます。

- 登録で決まる 3 つのこと。端末の場所、本人であること、後の要求が通る経路。
- REGISTER が 2 回送られる理由。
- I-CSCF と S-CSCF が、HSS に何を尋ねるか。
- 200 OK のどのヘッダを、あとの通話と SMS で使うか。
- 登録のあとに続くこと。登録の状態の購読、第三者 REGISTER、解除、期限切れ、網からの取り消し。
- 通話のデッキは、ここで決まる Path、Service-Route、IP の対応付けを使います。

> 本 [登録で決まる 3 つのこと](../../../docs/book/ja/registration.md)、[登録の更新、解除、購読、網からの取り消し](../../../docs/book/ja/registration-lifecycle.md)
> 手順書 [UE が IMS に登録する流れを追う](../../../docs/guide/ja/registration.md)（`docs/guide/ja/registration.md`）
> 仕様の版は Release 18 です。TS 24.229 V18.11.0、TS 29.228 V18.0.0、TS 29.229 V18.1.0。

---

## 登場するノード

```ims-scene
id: nodes
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: アドレスの最後の数で、どのノードかが分かります。
steps:
  - id: ue
    title: UE（172.28.0.100）
    focus: [ue]
    text: |
      REGISTER を送り、認証の要求に答えます。
      SIM カードの無いソフトフォンで、識別子とパスワードは設定で与えます。
  - id: pcscf
    title: P-CSCF（172.28.0.40）
    focus: [pcscf]
    text: UE から見て最初の中継点です。Path を付けます。
  - id: dns
    title: DNS（172.28.0.2）
    focus: [dns]
    checkpoint: true
    text: ホーム網のドメインの NAPTR、SRV、A を持ちます。P-CSCF はここで I-CSCF を探します。
  - id: icscf
    title: I-CSCF（172.28.0.20）
    focus: [icscf]
    text: ホーム網の入口です。担当の S-CSCF を HSS に尋ねます。
  - id: hss
    title: HSS（172.28.0.10）
    focus: [hss]
    text: 加入者のデータベースです。I-CSCF と S-CSCF に Cx で答えます。
  - id: scscf
    title: S-CSCF（172.28.0.30）
    focus: [scscf]
    text: |
      レジストラです。利用者を認証し、登録を記録します。
      HSS は 10、I-CSCF は 20、S-CSCF は 30、P-CSCF は 40 です。
```

- UE は SIM カードの無いソフトフォンです。識別子は IMC に入っているのと同じように設定で与えられるので（TS 24.229 5.1.1.1B.1）、登録した公開の識別子をほかのリクエストでも使います。この識別子は利用者の番号の SIP URI で、規制されていません。VoLTE の端末は、代わりに規制された一時的な識別子で登録します。VoLTE のデッキで見ます。

| ノード | アドレス | 登録での役割 |
| --- | --- | --- |
| UE シミュレータ | 172.28.0.100 | REGISTER を送り、認証の要求に答える。 |
| P-CSCF | 172.28.0.40 | UE から見て最初の中継点。Path を付ける。 |
| I-CSCF | 172.28.0.20 | ホーム網の入口。担当の S-CSCF を HSS に尋ねる。 |
| S-CSCF | 172.28.0.30 | レジストラ。認証し、登録を記録する。 |
| HSS | 172.28.0.10 | 加入者のデータベース。Cx で答える。 |
| DNS | 172.28.0.2 | ホーム網のドメインの NAPTR、SRV、A を持つ。 |

> 手順書 [「登場するノード」](../../../docs/guide/ja/registration.md#登場するノード)。試験用の利用者の識別子とパスワードもここにあります。

---

## 全体の流れ

```ims-scene
id: overview
view: ladder
nodes: [ue, pcscf, icscf, scscf, hss]
lead: 1 回目は「誰か」を伝え、2 回目で「本人であること」を証明します。
steps:
  - id: first
    title: 1 回目の REGISTER が I-CSCF まで届く（1、2）
    show: [reg1-ue-p, reg1-p-i]
    detail: []
    text: UE は REGISTER を P-CSCF に送り、P-CSCF はホーム網の入口の I-CSCF へ中継します。
  - id: ask
    title: I-CSCF が HSS に尋ね、S-CSCF へ送る（3、4）
    show: [uar1, uaa1, reg1-i-s]
    active: uar1
    detail: []
    text: HSS への 1 回目の問い合わせです。I-CSCF は答えをもとに S-CSCF を選びます。
  - id: challenge
    title: S-CSCF が認証を求める（5、6）
    show: [mar, maa, r401-s-i, r401-i-p, r401-p-ue]
    active: r401-p-ue
    detail: []
    checkpoint: true
    text: |
      S-CSCF は HSS から認証の情報を受け取り、401 で UE に問いを出します。
      401 は失敗ではなく、決まった手順の一部です。
  - id: answer
    title: 2 回目の REGISTER（7、8）
    show: [reg2-ue-p, reg2-p-i, uar2, uaa2, reg2-i-s]
    active: reg2-i-s
    detail: []
    text: UE が問いに答えます。I-CSCF はもう一度 HSS に尋ねます。
  - id: done
    title: 登録が決まる（9、10）
    show: [sar, saa, ok-s-i, ok-i-p, ok-p-ue]
    active: ok-p-ue
    detail: []
    checkpoint: true
    text: |
      HSS には 4 回尋ねます。UAR が 2 回、MAR と SAR が 1 回ずつです。
      手順書のシーケンス図の全体には DNS もあります。
  - id: settled
    title: 登録で決まった 3 つのこと
    active: ok-s-i
    detail: [ok-s-i]
    text: 場所は Contact、本人であることは 2 回目の答え、経路は Path と Service-Route で決まります。P-Associated-URI は一緒に登録された識別子です。
```

キャプチャで同じ形を見ると、DNS の問い合わせ（1 と 2 のあいだ）も並びます。手順書のシーケンス図と同じ番号で、次のスライドから 1 区間ずつ見ます。

```ladder
UE, P-CSCF, I-CSCF, S-CSCF, HSS
UE -> P-CSCF -> I-CSCF: REGISTER // 1 - 2
I-CSCF <-> HSS: UAR, UAA // 3
I-CSCF -> S-CSCF: REGISTER // 4
S-CSCF <-> HSS: MAR, MAA // 5
UE <-- P-CSCF <-- I-CSCF <-- S-CSCF: 401 // 6
UE -> P-CSCF -> I-CSCF: REGISTER + response // 7
I-CSCF <-> HSS: UAR, UAA // 8
I-CSCF -> S-CSCF: REGISTER
S-CSCF <-> HSS: SAR, SAA // 9
UE <-- P-CSCF <-- I-CSCF <-- S-CSCF: 200 OK // 10
```

> 本 [登録で決まる 3 つのこと](../../../docs/book/ja/registration.md)、[REGISTER が 2 回送られる理由](../../../docs/book/ja/registration-two-registers.md)

---

## 最初の REGISTER と DNS（1、2）

```ims-scene
id: first-register
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: REGISTER の宛先はサーバーではなくドメインです。どのサーバーに届けるかは網が決めます。
steps:
  - id: send
    title: UE が REGISTER を送る
    show: [reg1-ue-p]
    checkpoint: true
    text: |
      Request-URI はホーム網のドメインです。
      Authorization で、どの秘密の識別子を認証するかを先に伝えます。
  - id: naptr
    title: P-CSCF がドメインの NAPTR を引く
    show: [dns-naptr, dns-naptr-ans]
    active: dns-naptr-ans
    detail: [dns-naptr, dns-naptr-ans]
    text: RFC 3263 の手順です。答えで、UDP の SIP がどの名前で提供されているかが分かります。
  - id: srv
    title: その名前の SRV を引く
    hide: [dns-naptr, dns-naptr-ans]
    show: [dns-srv, dns-srv-ans]
    active: dns-srv-ans
    detail: [dns-srv, dns-srv-ans]
    checkpoint: true
    text: 引く名前は、前の答えが決めます。答えは I-CSCF の名前とポート 5060 です。
  - id: answer
    title: その名前の A を引き、I-CSCF のアドレスが分かる
    hide: [dns-srv, dns-srv-ans]
    show: [dns-a, dns-a-ans]
    active: dns-a-ans
    detail: [dns-a, dns-a-ans]
    checkpoint: true
    text: ホーム網の入口は 172.28.0.20 の 5060 番です。3 つの問い合わせは、どれも前の答えで次の名前が決まります。
  - id: add
    title: P-CSCF がヘッダを足す
    show: [pcscf-path]
    focus: [pcscf]
    text: |
      Path はあとで着信を UE に届けるための経路です。
      P-CSCF の前と後の REGISTER の違いは、この 4 つのヘッダです。
  - id: forward
    title: REGISTER が I-CSCF に届く
    show: [reg1-p-i]
    text: ここから先はホーム網の中です。担当の S-CSCF はまだ決まっていません。
```

| 見るもの | 見どころ |
| --- | --- |
| UE の REGISTER の Request-URI | サーバーではなく、ホーム網のドメイン。 |
| Authorization | `username` に秘密の識別子。`nonce` と `response` は空。 |
| Contact | ユーザー部に UUID。`+g.3gpp.smsip` と `expires=600000`。 |
| DNS の 3 つの問い合わせ | NAPTR、SRV、A の順で I-CSCF が見つかる。 |
| P-CSCF が足すヘッダ | Path、Require: path、P-Charging-Vector、P-Visited-Network-ID。 |

フィルタは `dns.qry.type == 35 || dns.qry.type == 33` です。

- P-CSCF の前と後の REGISTER の違いは、P-CSCF が足すこの 4 つのヘッダです。Path は、あとで着信を UE に届けるためのものです。

> 手順書 [1](../../../docs/guide/ja/registration.md#1-ue-が最初の-register-を送る) と [2](../../../docs/guide/ja/registration.md#2-p-cscf-が-i-cscf-を探して-register-を転送する)。TS 24.229 5.1.1.2.1、5.1.1.2.3、5.2.2.1。RFC 3263、RFC 3327、RFC 3581。

---

## I-CSCF が UAR で HSS に尋ねる（3、4）

```ims-scene
id: select
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: 最初の UAA が返すのは、S-CSCF の名前ではなく、必要な能力です。選ぶのは I-CSCF です。
steps:
  - id: arrive
    title: I-CSCF は担当の S-CSCF を知らない
    only: [reg1-ue-p, reg1-p-i]
    active: reg1-p-i
    focus: [icscf]
    zoom: [icscf, hss, scscf]
    text: I-CSCF は状態を持たない入口です。誰がこの利用者を担当するかを覚えていません。
  - id: uar
    title: HSS に尋ねる（UAR）
    show: [uar1]
    zoom: [icscf, hss, scscf]
    text: Cx のコマンド 300 です。秘密の識別子、公開の識別子、P-CSCF の網を伝えます。
  - id: uaa
    title: HSS が必要な能力を返す（UAA）
    show: [uaa1]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      まだどの S-CSCF も割り当てられていないので、HSS は名前の代わりに Server-Capabilities を返します。
      2001 は Experimental-Result の中です。Result-Code の 2001 なら DIAMETER_SUCCESS です。
  - id: choose
    title: I-CSCF が S-CSCF を選ぶ
    show: [icscf-select]
    focus: [icscf]
    zoom: [icscf, hss, scscf]
    text: |
      設定の一覧から、必須の能力をすべて持つ S-CSCF を選びます。
      この構成の候補は 1 台です。
  - id: forward
    title: 選んだ S-CSCF へ転送する
    show: [reg1-i-s]
    text: |
      Request-URI が S-CSCF に書き換わり、Via は 3 つになります。
      I-CSCF はこの選択を覚えません。覚えるのは HSS で、次の手順の MAR のときです。
```

| 見るもの | 見どころ |
| --- | --- |
| UAR の AVP | User-Name、Public-Identity、Visited-Network-Identifier、User-Authorization-Type。 |
| UAA の結果 | DIAMETER_FIRST_REGISTRATION（2001）。Experimental-Result の中にある。 |
| UAA の Server-Capabilities | S-CSCF の名前の代わりに、必要な能力が返る。 |
| I-CSCF が転送する REGISTER | Request-URI が、選んだ S-CSCF になる。Via は 3 つ。 |

フィルタは `diameter.cmd.code == 300` です。

- 間違えやすい点。Result-Code の 2001 は DIAMETER_SUCCESS です。Experimental-Result の中では DIAMETER_FIRST_REGISTRATION です。
- I-CSCF が S-CSCF を自分で覚えないのはなぜでしょうか。状態を持たない入口だからです。覚えるのは HSS です。

> 手順書 [3](../../../docs/guide/ja/registration.md#3-i-cscf-が-hss-に問い合わせるuar-と-uaa) と [4](../../../docs/guide/ja/registration.md#4-i-cscf-が-register-を-s-cscf-へ転送する)。TS 24.229 5.3.1.2。TS 29.228 6.1.1、6.7。TS 29.229 6.2.1.1。

---

## S-CSCF が認証を求める（5、6）

```ims-scene
id: challenge
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: HSS はパスワードを渡しません。渡すのは H(A1) です。
steps:
  - id: mar
    title: S-CSCF が認証の情報を求める（MAR）
    only: [reg1-i-s, mar]
    zoom: [icscf, hss, scscf]
    text: 方式は `Unknown` です。REGISTER に integrity-protected が無いので、選択を HSS に任せます。
  - id: assign
    title: HSS が担当の S-CSCF を記録する
    show: [hss-assign]
    focus: [hss]
    zoom: [icscf, hss, scscf]
    text: |
      MAR を受けた HSS は、この S-CSCF を担当として覚え、認証中の印を付けます。
      まだ登録は終わっていません。この記録は 2 回目の UAR で使われます。
  - id: maa
    title: HSS が SIP Digest の情報を返す（MAA）
    show: [maa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: H(A1) はユーザー名、レルム、パスワードのハッシュです。S-CSCF はこれだけで UE の答えを確かめられます。
  - id: nonce
    title: S-CSCF が問いを作る
    show: [scscf-challenge]
    focus: [scscf]
    text: nonce を作り、タイマー reg-await-auth（4 分）を始めます。
  - id: back
    title: 401 が UE まで戻る
    show: [r401-s-i, r401-i-p, r401-p-ue]
    text: |
      I-CSCF と P-CSCF を通って戻ります。各ノードは自分の Via を取り除きます。
      401 は失敗ではありません。UE への認証の問いです。
```

| 見るもの | 見どころ |
| --- | --- |
| MAR の 3GPP-SIP-Authentication-Scheme | `Unknown`。方式の選択を HSS に任せている。 |
| MAA の SIP-Digest-Authenticate | Digest-Realm、Digest-Algorithm、Digest-QoP、Digest-HA1。 |
| 401 の WWW-Authenticate | レルム、`nonce`、`algorithm=MD5`、`qop="auth"`。 |
| 401 の Via | 戻るたびに 1 つずつ減り、UE には自分の Via だけが届く。 |

フィルタは `diameter.cmd.code == 303 || sip.Status-Code == 401` です。

- HSS はパスワードを送りません。送るのは H(A1) です。
- S-CSCF は 401 と一緒にタイマー reg-await-auth（4 分）を始めます（手順書の 6）。

> 手順書 [5](../../../docs/guide/ja/registration.md#5-s-cscf-が認証の情報を求めるmar-と-maa) と [6](../../../docs/guide/ja/registration.md#6-s-cscf-が-ue-に認証を求める401)。TS 24.229 5.4.1.1、5.4.1.2.1、5.4.1.2.1B。TS 29.228 6.3。RFC 2617。

---

## 2 回目の REGISTER（7、8）

```ims-scene
id: second-register
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: 2 回目の REGISTER が同じ S-CSCF に届くのは、HSS が覚えているからです。
steps:
  - id: answer
    title: UE が問いに答える
    only: [reg2-ue-p]
    checkpoint: true
    text: パスワードと nonce から response を計算し、2 回目の REGISTER に入れます。Call-ID は 1 回目と同じです。
  - id: pending
    title: P-CSCF が ip-assoc-pending を付ける
    show: [pcscf-pending]
    focus: [pcscf]
    text: 認証への答えで、IP の対応付けはまだ無いことを S-CSCF に伝えます。
  - id: entry
    title: また I-CSCF を通る
    show: [reg2-p-i]
    checkpoint: true
    text: REGISTER はすべて I-CSCF を通ります。I-CSCF は前の選択を覚えていません。
  - id: uar
    title: もう一度 HSS に尋ねる
    show: [uar2]
    zoom: [icscf, hss, scscf]
    text: 2 回目の UAR です。HSS は MAR のときに S-CSCF を記録しています。
  - id: uaa
    title: 今度は Server-Name が返る
    show: [uaa2]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: DIAMETER_SUBSEQUENT_REGISTRATION（2002）と、記録した S-CSCF の名前です。
  - id: compare
    title: 1 回目と 2 回目の UAA を比べる
    show: [uaa1]
    active: uaa2
    detail: [uaa1, uaa2]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      1 回目は能力が返り、I-CSCF が選びました。
      2 回目は名前が返り、I-CSCF はそれに従います。
  - id: forward
    title: 同じ S-CSCF へ、今度は TCP で
    hide: [uaa1]
    show: [reg2-i-s]
    text: 認証の情報で 1300 バイトを超えるので、I-CSCF は TCP で送ります（RFC 3261 18.1.1）。
```

| 見るもの | 見どころ |
| --- | --- |
| Call-ID と CSeq | Call-ID は 1 回目と同じ。CSeq は 1 つ増える。 |
| Authorization | `nonce`、`cnonce`、`nc`、`qop`、`response` が入る。 |
| P-CSCF が足すパラメータ | `integrity-protected="ip-assoc-pending"`。 |
| 2 回目の UAA | DIAMETER_SUBSEQUENT_REGISTRATION（2002）と Server-Name。 |
| I-CSCF から先のトランスポート | 1300 バイトを超えるので TCP。Via が `SIP/2.0/TCP` になる。 |

- 2 回目の REGISTER が同じ S-CSCF に届くのはなぜでしょうか。HSS が MAR のときに S-CSCF を覚え、2 回目の UAA の Server-Name で返すからです。

> 手順書 [7](../../../docs/guide/ja/registration.md#7-ue-が認証の要求に答える) と [8](../../../docs/guide/ja/registration.md#8-i-cscf-がもう一度-hss-に問い合わせる)。TS 24.229 5.1.1.5.4、5.2.2.3。TS 29.228 6.1.1.1。RFC 3261 18.1.1。

---

## SAR と 200 OK（9、10）

```ims-scene
id: accept
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: 答えが正しいと分かってから、S-CSCF は HSS に知らせ、200 OK を返します。
steps:
  - id: verify
    title: S-CSCF が答えを確かめる
    only: [reg2-i-s, scscf-verify]
    focus: [scscf]
    text: Call-ID、レルムと nonce、response の 3 つを確かめます。
  - id: sar
    title: HSS に登録を知らせる（SAR）
    show: [sar]
    zoom: [icscf, hss, scscf]
    text: Server-Assignment-Type は REGISTRATION（1）です。HSS は、利用者がこの S-CSCF に登録されたと記録します。
  - id: saa
    title: プロファイルを受け取る（SAA）
    show: [saa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: User-Data に、一緒に登録される識別子と iFC が入っています。iFC は、要求にどのアプリケーションサーバーを加えるかを決めるものです。
  - id: ok
    title: 200 OK が I-CSCF へ
    show: [ok-s-i]
    text: このあと UE と P-CSCF が使う情報は、すべてこの 200 OK に入っています。
  - id: relay
    title: P-CSCF が経路を覚える
    show: [ok-i-p, pcscf-store]
    active: pcscf-store
    focus: [pcscf]
    text: UE のアドレスについて IP の対応付けを作り、Service-Route と識別子を記録します。
  - id: registered
    title: UE が登録された
    show: [ok-p-ue]
    text: |
      UE は `rspauth` を確かめます。網の側も H(A1) を知っている証明です。
      Contact の期間は 3600 秒です。UE が求めた 600000 秒を、S-CSCF が上限まで短くしました。
```

| 見るもの | 見どころ |
| --- | --- |
| SAR の Server-Assignment-Type | `REGISTRATION`（1）。 |
| SAA の User-Data | 利用者のプロファイル。XML。iFC が入っている。 |
| 200 OK の Path と Service-Route | このあとの要求が通る経路。 |
| 200 OK の P-Associated-URI | 一緒に登録された識別子。HSS が規制する識別子は入らない。最初が既定の識別子。 |
| 200 OK の Contact | `expires=3600`。S-CSCF が上限まで短くした。 |
| 200 OK の Authentication-Info | `rspauth`。網の側も H(A1) を知っている証明。 |

フィルタは `diameter.cmd.code == 301` です。Wireshark では User-Data が `Cx-User-Data` という名前で出ます。

> 手順書 [9](../../../docs/guide/ja/registration.md#9-s-cscf-が答えを確かめて-hss-に知らせるsar-と-saa) と [10](../../../docs/guide/ja/registration.md#10-s-cscf-が登録を受け付ける200-ok)。TS 24.229 5.4.1.2.2A、5.4.1.2.2F。TS 29.228 Annex E。RFC 3608。

---

## Path と Service-Route の使い分け

```ims-scene
id: routes
nodes: [ue, pcscf, icscf, scscf]
lead: どちらも登録で決まる経路ですが、保存する側と使う向きが逆です。
steps:
  - id: register
    title: 登録で 2 つの経路が決まる
    only: [route-path-store, route-sr-store]
    focus: [scscf, ue]
    detail: [route-path-store, route-sr-store]
    checkpoint: true
    text: |
      S-CSCF は、P-CSCF が REGISTER に付けた Path を、登録先と一緒に保存します。
      S-CSCF が 200 OK に付けた Service-Route は、UE が保存します。
  - id: path
    title: Path は UE に届く要求の経路
    only: [mt-s-p, mt-p-ue]
    active: mt-p-ue
    focus: [scscf, pcscf, ue]
    checkpoint: true
    text: |
      S-CSCF は UE あての要求の Request-URI を Contact にし、保存した Path を Route に入れます。
      着信の INVITE は、UE が登録に使った P-CSCF を通って届きます。
  - id: service-route
    title: Service-Route は UE から出ていく要求の経路
    only: [mo-ue-p, mo-p-s]
    active: mo-p-s
    focus: [ue, pcscf, scscf]
    checkpoint: true
    text: |
      UE は自分の要求の先頭に Service-Route を付けます。要求は P-CSCF から自分の S-CSCF に直接届き、I-CSCF を通りません。
      発信の INVITE と、このあとの UE の SUBSCRIBE がこの経路を通ります。
```

- UE は発信するときに Service-Route を使います。Path は着信のときに使います。どちらも、登録への応答が戻る Via の一覧とは別のものです。Via はいま送った要求の応答の戻り道で、Path と Service-Route はこのあとの新しい要求の経路です。
- 着信と発信の INVITE の全体は、通話のデッキでもう一度出てきます。

> 本 [Path と Service-Route の使い分け](../../../docs/book/ja/registration-routes.md)
> 手順書 [10](../../../docs/guide/ja/registration.md#10-s-cscf-が登録を受け付ける200-ok)。RFC 3327、RFC 3608。

---

# ここまでで登録は完了

後半は、登録のあとに続く流れを見ます。

---

## P-CSCF が登録の状態を購読する（11、12）

```ims-scene
id: pcscf-subscribe
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: 登録が変わったら知らせるよう、P-CSCF が S-CSCF に頼みます。reg イベントパッケージです。
steps:
  - id: subscribe
    title: P-CSCF が SUBSCRIBE を I-CSCF へ送る
    only: [psub-p-i]
    text: P-CSCF はホーム網の中にあるので、SUBSCRIBE はホーム網の入口へ送ります。
  - id: lir
    title: I-CSCF が S-CSCF を探す（LIR と LIA）
    show: [lir1, lia1]
    active: lia1
    zoom: [icscf, hss, scscf]
    text: 利用者がどの S-CSCF にいるかを HSS に尋ねます。Cx のコマンド 302 です。
  - id: forward
    title: SUBSCRIBE が S-CSCF に届く
    show: [psub-i-s]
    text: S-CSCF は、登録があること、購読者が Path にある P-CSCF であることを確かめます。
  - id: ok
    title: 200 OK で購読が始まる
    show: [psub-ok-s-i, psub-ok-i-p]
    active: psub-ok-i-p
    checkpoint: true
    text: この 200 で、P-CSCF と S-CSCF の間にダイアログができます。通話の無いダイアログです。
  - id: notify
    title: NOTIFY は P-CSCF へ直接届く
    hide: [lir1, lia1]
    show: [pnotify]
    focus: [scscf, pcscf]
    text: |
      I-CSCF は経路に残らなかったので、NOTIFY は I-CSCF を通りません。
      本文は `application/reginfo+xml` です。
```

| 見るもの | 見どころ |
| --- | --- |
| P-CSCF の SUBSCRIBE | I-CSCF を通る。I-CSCF は LIR で S-CSCF を探す。 |
| NOTIFY の本文 | `application/reginfo+xml`。登録先ごとに `contact` がある。 |
| Subscription-State | `active;expires=...`。 |

フィルタは `sip.CSeq.method == "SUBSCRIBE" || sip.CSeq.method == "NOTIFY" || diameter.cmd.code == 302` です。

> 本 [登録の更新、解除、購読、網からの取り消し](../../../docs/book/ja/registration-lifecycle.md)
> 手順書 [「登録の状態を購読する」](../../../docs/guide/ja/registration.md#登録の状態を購読する)。TS 24.229 5.2.3、5.4.2.1.1、5.4.2.1.2。RFC 3680、RFC 6665。TS 29.228 6.1.4。

---

## UE も登録の状態を購読する（13、14）

```ims-scene
id: ue-subscribe
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: UE の SUBSCRIBE は Service-Route に沿って進み、I-CSCF を通りません。
steps:
  - id: subscribe
    title: UE が SUBSCRIBE を送る
    only: [usub-ue-p]
    text: Route は P-CSCF と Service-Route です。UE が送るほかの要求と同じです。
  - id: direct
    title: P-CSCF から S-CSCF へ直接
    show: [usub-p-s]
    focus: [pcscf, scscf]
    checkpoint: true
    text: P-CSCF は識別子を P-Asserted-Identity に入れ、Record-Route を付けて、Service-Route の S-CSCF に送ります。
  - id: notify
    title: NOTIFY が P-CSCF を通って UE に届く
    show: [unotify-s-p, unotify-p-ue]
    active: unotify-p-ue
    detail: [unotify-s-p, unotify-p-ue]
    text: |
      S-CSCF からの NOTIFY の Route には P-CSCF が入っています。P-CSCF は NOTIFY に自分の Record-Route を付けます。
      UE は購読の経路をこの NOTIFY から作るので、後の SUBSCRIBE も P-CSCF を通ります。
```

| 見るもの | 見どころ |
| --- | --- |
| UE の SUBSCRIBE | Service-Route に沿って進み、I-CSCF を通らない。 |
| NOTIFY の本文 | P-CSCF に届くものと同じ形の reginfo。 |

> 手順書 [13](../../../docs/guide/ja/registration.md#13-ue-が購読するsubscribe) と [14](../../../docs/guide/ja/registration.md#14-notify-が-p-cscf-を通って-ue-に届く)。TS 24.229 5.1.1.3、5.2.6.4.5。RFC 6665 4.4.1。

---

## アプリケーションサーバーへの第三者登録（15〜18）

```ims-scene
id: third-party
nodes: [icscf, hss, scscf, ipsmgw]
lead: S-CSCF は、プロファイルの iFC に従って IP-SM-GW にも REGISTER を送ります。第三者 REGISTER と呼びます。
steps:
  - id: register
    title: S-CSCF が IP-SM-GW に REGISTER を送る
    only: [tp-register]
    text: |
      From と Contact は S-CSCF、To は利用者です。本文に MSISDN が入ります。
      UE はこれを待ちません。200 OK をもう受け取っています。
  - id: ok
    title: IP-SM-GW が 200 OK で答える
    show: [tp-ok]
    checkpoint: true
    text: IP-SM-GW が答えないときは、iFC の DefaultHandling（ここでは SESSION_CONTINUED）に従い、登録は残ります。
  - id: subscribe
    title: IP-SM-GW も reg イベントを購読する
    only: [isub-ip-i, lir2, lia2, isub-i-s]
    active: isub-i-s
    checkpoint: true
    text: P-CSCF と同じく、SUBSCRIBE はホーム網の入口へ送られ、I-CSCF が LIR で S-CSCF を探します。
  - id: notify
    title: NOTIFY を受けて、SMS を受けられる登録先を探す
    only: [inotify, ipsmgw-check]
    active: ipsmgw-check
    focus: [ipsmgw]
    checkpoint: true
    text: '`+g.3gpp.smsip` は、UE が IP で SMS を受けられることを示す機能タグです。'
  - id: pur
    title: IP-SM-GW が HSS に知らせる（PUR と PUA）
    only: [pur, pua]
    active: pur
    detail: [pur, pua]
    text: |
      Sh のコマンド 307 で、この利用者の SMS を受け持つことを HSS に書きます。
      この流れは SMS のデッキで詳しく見ます。
```

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF の REGISTER | From と Contact が S-CSCF。To が利用者。本文に MSISDN。 |
| IP-SM-GW の SUBSCRIBE と NOTIFY | IP-SM-GW も reg イベントを購読する。 |
| PUR と PUA | IP-SM-GW が、自分の番号を Sh で HSS に登録する。 |

- UE はこれを待ちません。200 OK をもう受け取っています。この流れは SMS のデッキで詳しく見ます。

フィルタは `diameter.cmd.code == 307` です。

> 本 [登録の更新、解除、購読、網からの取り消し](../../../docs/book/ja/registration-lifecycle.md)
> 手順書 [「アプリケーションサーバーにも登録する（第三者 REGISTER）」](../../../docs/guide/ja/registration.md#アプリケーションサーバーにも登録する第三者-register)。TS 24.229 5.4.1.7、5.4.1.7A。TS 24.341 5.3.3.2。TS 29.328 6.1.2。

---

## 登録の解除

```ims-scene
id: deregister
view: ladder
nodes: [ue, pcscf, icscf, scscf, hss]
lead: UE は Contact に `expires=0` を付けた REGISTER を送ります。今度は 401 が返りません。
steps:
  - id: request
    title: 解除の REGISTER が I-CSCF へ
    show: [dreg-ue-p, dreg-p-i]
    text: P-CSCF は IP の対応付けを見つけ、`integrity-protected="ip-assoc-yes"` を付けます。
  - id: uar
    title: DE_REGISTRATION の UAR
    show: [duar, duaa, dreg-i-s]
    active: duar
    text: I-CSCF は User-Authorization-Type を DE_REGISTRATION（1）にして尋ね、S-CSCF へ送ります。
  - id: sar
    title: 認証せずに SAR を送る
    show: [dsar, dsaa]
    active: dsar
    checkpoint: true
    text: |
      IP の対応付けがあるので、S-CSCF は認証を求め直しません。
      USER_DEREGISTRATION（5）の SAR を送り、SAA を待ちます。
  - id: ok
    title: expires=0 の 200 OK
    show: [dok-s-i, dok-i-p, dok-p-ue]
    active: dok-p-ue
    text: 200 OK は I-CSCF と P-CSCF を通って UE に戻ります。
  - id: notify
    title: 最後の NOTIFY
    show: [dnotify-s-p, dnotify-p-ue]
    active: dnotify-s-p
    text: |
      registration と contact は `terminated`、contact は `event="unregistered"` です。この NOTIFY で購読が終わります。
      IP-SM-GW にも届き、IP-SM-GW は HSS から自分の登録を消します。
```

| 見るもの | 見どころ |
| --- | --- |
| P-CSCF が足すパラメータ | `integrity-protected="ip-assoc-yes"`。 |
| UAR の User-Authorization-Type | DE_REGISTRATION（1）。 |
| S-CSCF の応答 | 401 を返さない。P-CSCF が IP の対応付けを見つけ、`ip-assoc-yes` を付けたから。 |
| SAR の Server-Assignment-Type | USER_DEREGISTRATION（5）。 |
| 最後の NOTIFY | `terminated`。contact は `event="unregistered"`。 |

- 更新されない登録も同じように終わります。`event="expired"` と、TIMEOUT_DEREGISTRATION の SAR です。手順書の同じ節にあります。

> 本 [登録の更新、解除、購読、網からの取り消し](../../../docs/book/ja/registration-lifecycle.md)
> 手順書 [「登録の解除」](../../../docs/guide/ja/registration.md#登録の解除)。TS 24.229 5.1.1.6.1、5.4.1.4.1、5.4.2.1.2。

---

## 遅れて届いた登録の解除

```ims-scene
id: registration-order
nodes: [icscf, scscf, hss]
basis: illustration
lead: 同じ Call-ID の要求が登録を変えられるのは、CSeq が保存した値より大きいときだけです。
steps:
  - id: stored
    title: 新しい登録が保存されている
    only: [ord-stored]
    focus: [scscf]
    text: 再登録が CSeq 5 で受け付けられました。S-CSCF は、その Call-ID と CSeq を登録先と一緒に保存しています。
  - id: late
    title: 古い解除の REGISTER が届く
    show: [ord-late]
    text: 再登録より前に UE が送った CSeq 3 の解除の再送が、遅れて届きました。手前の P-CSCF と I-CSCF の処理は、図から省いています。
  - id: compare
    title: 保存した CSeq と比べる
    show: [ord-compare]
    text: Call-ID が同じなので、S-CSCF は CSeq を比べます。3 は 5 より大きくありません。
  - id: refuse
    title: 500 で断り、登録を残す
    show: [ord-500, ord-kept]
    active: ord-500
    detail: [ord-500, ord-kept]
    checkpoint: true
    text: |
      S-CSCF は 500 で答え、Contact、期限、CSeq を変えません。HSS へ解除の SAR も送りません。
      CSeq 5 の要求も、大きくないので同じように断られます。
  - id: other
    title: 別の Call-ID の REGISTER は比べない
    only: [ord-stored, ord-other, ord-other-compare]
    focus: [scscf]
    detail: [ord-other, ord-other-compare]
    text: Call-ID が違うので、S-CSCF は CSeq を比べません。
  - id: accepted
    title: SAR を送り、B の値を保存する
    show: [ord-other-sar, ord-other-saa, ord-other-ok]
    active: ord-other-ok
    checkpoint: true
    text: S-CSCF は RE_REGISTRATION の SAR を送り、200 OK で答えます。このあとは Call-ID B と CSeq 2 が、比べる値になります。
```

- 図の Call-ID の A と B、Contact の C は、長い値を短く書いた例です。キャプチャの値ではありません。
- 比べる規則は RFC 3261 10.3 の手順 6 です。Call-ID が同じなら、CSeq が保存した値より大きいときだけ登録先を変え、そうでなければ要求は失敗します。規則は応答のコードを定めていません。この実装は、同じ節の手順 7 がバインディングの更新に失敗したときのコードとして挙げる 500 を返します。
- S-CSCF は、認証の答えを確かめたあと、HSS へ SAR を送る前に比べます。解除の要求は、IP の対応付けがあれば認証を求め直さずに比べます。どちらでも、断った要求は HSS にも登録先にも何も残しません。

> 本 [遅れて届いた登録の解除が、新しい登録を消さない理由](../../../docs/book/ja/registration-stale-deregistration.md)。手順書 [7](../../../docs/guide/ja/registration.md#7-ue-が認証の要求に答える)。

---

## 失敗の例

```ims-scene
id: failures
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: 2 つの失敗は、どちらも正常な登録の途中から分かれます。始まるノードが違います。
steps:
  - id: wrong
    title: パスワードが誤っている
    only: [reg2-i-s, wp-verify]
    focus: [scscf]
    text: 手順 8 までは同じです。手順 9 で、S-CSCF が計算した値と response が一致しません。
  - id: forbidden
    title: 403 と AUTHENTICATION_FAILURE の SAR
    show: [wp-403-s-i, wp-403-i-p, wp-403-p-ue, wp-sar, wp-clear]
    active: wp-sar
    detail: [wp-sar, wp-clear]
    checkpoint: true
    text: |
      S-CSCF は 403 で答え、AUTHENTICATION_FAILURE（9）の SAR を送ります。
      HSS は記録した S-CSCF を消すので、次の試行はまた最初から始まります。
  - id: rtr
    title: HSS から加入を削除する
    only: [rtr]
    focus: [hss, scscf]
    text: |
      今度は HSS から始まります。ここまでの Cx の要求は I-CSCF か S-CSCF が送りましたが、RTR は HSS が送ります。
      Reason-Code は PERMANENT_TERMINATION（0）です。
  - id: rejected
    title: UE に rejected の NOTIFY が届く
    show: [rej-s-p, rej-p-ue, rta]
    active: rej-p-ue
    detail: [rej-s-p, rta]
    text: |
      S-CSCF は登録を消し、`event="rejected"` の NOTIFY を送り、RTA で答えます。
      UE は rejected を受けると、登録し直しません。
```

| 場合 | 起こすもの | 見どころ |
| --- | --- | --- |
| パスワードが誤っている | `ue register -password wrong-password` | 手順 8 までは同じ。S-CSCF は 403 を返し、AUTHENTICATION_FAILURE（9）の SAR を送る。 |
| HSS から加入を削除する | 運用者が、登録中の加入を OAM から削除する。 | HSS が RTR を送る。Reason-Code は PERMANENT_TERMINATION（0）。UE には `event="rejected"` の NOTIFY が届く。 |

- 手順書では、`ue` のコマンドを `docker compose run --rm` に続けて実行します。
- RTR と RTA のフィルタは `diameter.cmd.code == 304` です。
- HSS の加入者管理の API でも加入を削除できます。API の表は手順書にあります。

> 本 [登録の更新、解除、購読、網からの取り消し](../../../docs/book/ja/registration-lifecycle.md)、[REGISTER が 2 回送られる理由](../../../docs/book/ja/registration-two-registers.md)
> 手順書 [「パスワードが誤っているとき」](../../../docs/guide/ja/registration.md#パスワードが誤っているとき) と [「加入を削除する（HSS からの登録解除）」](../../../docs/guide/ja/registration.md#加入を削除するhss-からの登録解除)、ラボの手順書 [「網が登録を取り消す」](../../../docs/guide/ja/lab.md#網が登録を取り消す)。TS 24.229 5.4.1.2.3B。TS 29.228 6.1.2.1、6.1.3。

---

[layout: two-columns]

## ラボで同じ流れを見る

- ラボ（<http://localhost:8080>）の **Registrations** パネルは、登録した識別子ごとに Contact、期限、S-CSCF を並べます。
- 識別子を選ぶと、最後の REGISTER のシーケンス図が出ます。矢印を選ぶと、メッセージの全文と仕様の条項が出ます。
- シーケンス図には SIP と Cx が並びます。DNS は出ません。Cx の要求は、同じノードが直前に受けた REGISTER に、ラボが推定で結び付けます。
- ラボは、ノードが送るコピー（HEP）から図を作ります。IMS の一部ではありません。第三者 REGISTER は行を増やさず、RTR は図に出ません。

[column]

| 道具 | 向いていること |
| --- | --- |
| Wireshark | すべてのパケット。DNS、TCP、AVP の中身まで。 |
| ラボ | 1 つの登録や通話を、すぐに図で見る。 |

> 本 [登録の更新、解除、購読、網からの取り消し](../../../docs/book/ja/registration-lifecycle.md)
> 手順書 [ラボで IMS を見る「各パネルの内容」](../../../docs/guide/ja/lab.md#各パネルの内容) と [「シーケンス図の組み立て方」](../../../docs/guide/ja/lab.md#シーケンス図の組み立て方)

---

## 手順を書いたコード

```ims-scene
id: code
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: ノードごとに、手順を書いたファイルと、判断する関数があります。
steps:
  - id: ue
    title: UE の REGISTER と応答の計算（1、7）
    focus: [ue]
    text: "`internal/ue/ue.go` の `Register` です。"
  - id: pcscf
    title: Path と DNS（2）
    focus: [pcscf, dns]
    text: "`internal/pcscf/pcscf.go` の `register` と `pathURI`、`internal/sipdns/locate.go` の `Locate` です。"
  - id: icscf
    title: UAR（3、8）
    focus: [icscf]
    text: "`internal/icscf/icscf.go` の `register` と `choose` です。"
  - id: scscf
    title: MAR、401、SAR、200 OK（5、6、9、10）
    focus: [scscf]
    text: "`internal/scscf/scscf.go` の `register`、`challenge`、`check`、`accept`、`deregister` です。"
  - id: hss
    title: HSS の答え
    focus: [hss]
    text: "`internal/hss/hss.go` の `userAuthorization`、`multimediaAuth`、`serverAssignment` です。"
  - id: regevent
    title: reg イベント（11 から 14）
    focus: [scscf, pcscf, ue]
    text: "`internal/scscf/regevent.go`、`internal/pcscf/regevent.go`、`internal/ue/regevent.go` です。"
```

| 手順 | 開くファイル | 判断する関数 |
| --- | --- | --- |
| 1、7。UE の REGISTER と応答の計算 | `internal/ue/ue.go` | `Register` |
| 2。Path と DNS | `internal/pcscf/pcscf.go`、`internal/sipdns/locate.go` | `register`、`pathURI`、`Locate` |
| 3、8。UAR | `internal/icscf/icscf.go` | `register`、`choose` |
| 5、6、9、10。MAR、401、SAR、200 OK | `internal/scscf/scscf.go` | `register`、`challenge`、`check`、`accept`、`deregister` |
| HSS の答え | `internal/hss/hss.go` | `userAuthorization`、`multimediaAuth`、`serverAssignment` |
| 11 から 14。reg イベント | `internal/scscf/regevent.go`、`internal/pcscf/regevent.go`、`internal/ue/regevent.go` | `subscribe`、`notifyRegistration`、`endRegistration`、`subscribeRegEvent`、`SubscribeRegEvent` |

- キャプチャで見たヘッダの名前は、コードにもそのまま出てきます。たとえば `Service-Route` は、S-CSCF の `bindingResponse` が 200 OK に入れ、UE の `routeSet` が Route にします。
- 仕様の手順を簡略化した箇所には `SIMPLIFIED:` のコメントがあり、手順書の簡略化の表に同じ内容があります。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/registration.md#このプロジェクトで簡略化していること)

---

## 自分で動かす場合

このデッキは、読むだけで分かるように書いています。同じ流れを自分のキャプチャで見たいときの手順は、手順書にあります。

- 構成の起動と登録の実行は [「登録を実行する」](../../../docs/guide/ja/registration.md#登録を実行する) にあります。キャプチャの開き方と表示フィルタは [「キャプチャを開く」](../../../docs/guide/ja/registration.md#キャプチャを開く) に、番号の付いたシーケンス図の全体は [「シーケンス」](../../../docs/guide/ja/registration.md#シーケンス) にあります。Wireshark の使い方は [信号をキャプチャして Wireshark で開く](../../../docs/guide/ja/capture.md) にあります。
- 片付けは手順書 [「後片付け」](../../../docs/guide/ja/registration.md#後片付け) にあります。

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
docker compose --profile ue down --rmi all -v --remove-orphans
```

1 行目は、ノードと UE を止め、ボリュームを消し、イメージを残します。続けて通話のデッキの構成を、ビルドし直さずに起動できます。2 行目は、イメージも消します。どちらも `-v` で、キャプチャ以外の保存データ（ボリューム）が消えます。

> 通話の手順書は [音声通話の流れを追う](../../../docs/guide/ja/call.md) です。
