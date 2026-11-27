---
title: 境界でつながる 2 つの IMS 網
footer: ims-practice 勉強会 10. 2 つの網
lang: ja
format: visual
---

```ims-model
nodes:
  # 網 A（ims0、172.28.0.0/24）
  - {id: mmtel-a, label: MMTel AS, role: 会議のフォーカス, at: [0, 0]}
  - {id: dns-a, label: DNS-A, role: ENUM を返す, at: [0.9, 0]}
  - {id: ue-a1, label: UE A1, role: 発信する端末, at: [0, 1]}
  - {id: scscf-a, label: S-CSCF-A, role: 発信を受け持つ, at: [0.9, 1]}
  - {id: ibcf-a, label: IBCF-A, role: 網 A の境界, at: [1.8, 1]}
  - {id: ipsmgw-a, label: IP-SM-GW-A, role: SC も同じノード, at: [0, 2]}
  - {id: agw-a, label: IMS-AGW, role: 網 A の中継ノード, at: [0.9, 2]}
  - {id: trgw-a, label: TrGW-A, role: 網 A のメディア, at: [1.8, 2]}
  # 網のあいだ（nni0、172.28.2.0/24）にいて、どちらの網にも属さないホスト
  - {id: host99, label: 172.28.2.99, role: 取り決めの外, at: [2.25, 0]}
  # 網 B（ims1、172.28.1.0/24）
  - {id: icscf-b, label: I-CSCF-B, role: 網 B の入口, at: [3.6, 0]}
  - {id: hss-b, label: HSS-B, role: 加入者のデータ, at: [4.5, 0]}
  - {id: ibcf-b, label: IBCF-B, role: 網 B の境界, at: [2.7, 1]}
  - {id: scscf-b, label: S-CSCF-B, role: 着信を受け持つ, at: [3.6, 1]}
  - {id: ue-b1, label: UE B1, role: 着信する端末, at: [4.5, 1]}
  - {id: trgw-b, label: TrGW-B, role: 網 B のメディア, at: [2.7, 2]}
  - {id: ipsmgw-b, label: IP-SM-GW-B, role: 網 B の SMS の AS, at: [4.5, 2]}
groups:
  - {id: net-a, label: 網 A（ims0）, nodes: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a]}
  - {id: net-b, label: 網 B（ims1）, nodes: [icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]}
events:
  # 境界の 4 つの参照点
  - id: mx
    rel: Mx
    from: scscf-a
    to: ibcf-a
    proto: SIP
  - id: ici
    rel: Ici
    from: ibcf-a
    to: ibcf-b
    proto: SIP
  - id: izi
    rel: Izi
    from: trgw-a
    to: trgw-b
    proto: RTP
  - id: ix
    rel: Ix
    from: ibcf-a
    to: trgw-a
    proto: HTTP
  # 通話。網 A から出ていくまで（手順 1 から 4）
  - id: inv-ue-s
    msg: INVITE
    from: ue-a1
    to: scscf-a
    proto: SIP
    note: (P-CSCF 経由)
    fields:
      - Request-URI: "`tel:+12025550151`"
      - 通る AS: 発信側のフィルタ条件の MMTel AS
  - id: enum-q
    msg: NAPTR
    from: scscf-a
    to: dns-a
    proto: DNS
    note: (問い合わせ)
    fields:
      - 名前: "`1.5.1.0.5.5.5.2.0.2.1.e164.arpa`"
  - id: enum-a
    msg: NAPTR
    from: dns-a
    to: scscf-a
    proto: DNS
    note: (答え)
    fields:
      - サービス: "`E2U+sip`"
      - 書き換えの結果: 網 B のドメインの SIP URI に `user=phone`
  - id: scscf-exit
    do: 自網の IBCF へ送る
    at: scscf-a
    fields:
      - 宛先のドメイン: 網 A のものではない
      - Route の先頭: "`sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr`"
      - Record-Route: 今までどおり自分を入れる
  - id: inv-s-ia
    msg: INVITE
    from: scscf-a
    to: ibcf-a
    proto: SIP
    fields:
      - Request-URI: 番号の SIP URI に `user=phone`
      - Call-ID: 網 A のダイアログのもの
      - Via: 網 A のノードのものが並ぶ
  - id: ibcfa-exit
    do: 出口として要求を作り直す
    at: ibcf-a
    fields:
      - Route: 先頭から自分の URI を外す
      - 相手網: Request-URI のホストから `network-b.yaml` を選ぶ
      - 消すヘッダ: P-Charging-Function-Addresses
      - 番号とコーデック: 取り決めの形に直し、相手網が受けないものを外す
      - Max-Forwards: 1 つ減らす
  - id: trgw-add
    msg: Add
    from: ibcf-a
    to: trgw-a
    proto: HTTP
    note: (Ix)
    fields:
      - ターミネーション: "`core` は自網、`access` は相手網を向く"
      - コンテキスト: 呼に 1 つ
  - id: trgw-mod-a
    msg: Modify
    from: ibcf-a
    to: trgw-a
    proto: HTTP
    note: (Ix)
    fields:
      - core が送る先: 172.28.0.50。網 A のメディア中継ノード
      - 網のあいだの SDP に書くアドレス: 172.28.2.11
  - id: inv-nni
    msg: INVITE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (網のあいだ)
    fields:
      - Via: IBCF-A の外側の名前の 1 つだけ
      - Call-ID: IBCF-A が作ったもの。CSeq は 1
      - Contact: 自分の外側の名前 `ibcf.nni.mnc001.example.net`
  # 通話。網 B に入ってから（手順 5 から 7）
  - id: ibcfb-entry
    do: 入口として要求を作り直す
    at: ibcf-b
    fields:
      - 送信元の 172.28.2.10: 網 A の取り決めの `sources` にある
      - Route: 先頭から自分の URI を外す
      - 次の宛先: 設定した I-CSCF
      - メディア: TrGW-B に通し、網 B の SDP は 172.28.1.91 を指す
  - id: inv-ib-i
    msg: INVITE
    from: ibcf-b
    to: icscf-b
    proto: SIP
    fields:
      - Via: IBCF-B の内側の名前の 1 つだけ
      - Call-ID: IBCF-B が作ったもの
  - id: icscfb-tel
    do: Request-URI を tel URI に直す
    at: icscf-b
    fields:
      - 届いた Request-URI: 番号の SIP URI に `user=phone`
      - 直したあと: "`tel:+12025550151`"
      - 理由: HSS は UE B1 を tel URI で知っている
  - id: lir
    msg: LIR
    from: icscf-b
    to: hss-b
    proto: Cx
    fields:
      - Public-Identity: "`tel:+12025550151`"
  - id: lia
    msg: LIA
    from: hss-b
    to: icscf-b
    proto: Cx
    fields:
      - Server-Name: UE B1 が登録している S-CSCF
  - id: inv-i-sb
    msg: INVITE
    from: icscf-b
    to: scscf-b
    proto: SIP
    fields:
      - Request-URI: "`tel:+12025550151`"
  - id: inv-sb-ue
    msg: INVITE
    from: scscf-b
    to: ue-b1
    proto: SIP
    note: (P-CSCF 経由)
    fields:
      - 経路: UE B1 の Path に沿って網 B の P-CSCF へ
      - SDP: "`c=` は TrGW-B の 172.28.1.91"
  # 通話。answer が戻る（手順 7）
  - id: r183-ue-sb
    msg: "183"
    from: ue-b1
    to: scscf-b
    proto: SIP
    note: (answer)
    fields:
      - SDP: UE B1 のアドレスとポート
  - id: r183-sb-i
    msg: "183"
    from: scscf-b
    to: icscf-b
    proto: SIP
    note: (Via に沿って)
  - id: r183-i-ib
    msg: "183"
    from: icscf-b
    to: ibcf-b
    proto: SIP
  - id: r183-ib-ia
    msg: "183"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (網のあいだ)
    fields:
      - SDP: "`c=` は 172.28.2.21。TrGW-B の網のあいだ側"
  - id: r183-ia-s
    msg: "183"
    from: ibcf-a
    to: scscf-a
    proto: SIP
    fields:
      - SDP: "`c=` は 172.28.0.91。TrGW-A の網 A 側"
  - id: r183-s-ue
    msg: "183"
    from: scscf-a
    to: ue-a1
    proto: SIP
    note: (P-CSCF 経由)
    fields:
      - SDP: "`c=` は 172.28.0.50。網 A のメディア中継ノード"
  # メディア（手順 9）
  - id: trgwb-mod
    msg: Modify
    from: ibcf-b
    to: trgw-b
    proto: HTTP
    note: (Ix)
    fields:
      - core が送る先: 172.28.1.100。UE B1
      - 網のあいだの SDP に書くアドレス: 172.28.2.21
  - id: trgwa-mod-back
    msg: Modify
    from: ibcf-a
    to: trgw-a
    proto: HTTP
    note: (Ix)
    fields:
      - access が送る先: 172.28.2.21。TrGW-B
      - 網 A へ渡す SDP に書くアドレス: 172.28.0.91
  - id: rtp-ue-agw
    msg: RTP
    from: ue-a1
    to: agw-a
    proto: RTP
    note: (音声)
  - id: rtp-agw-trgw
    msg: RTP
    from: agw-a
    to: trgw-a
    proto: RTP
  - id: rtp-nni
    msg: RTP
    from: trgw-a
    to: trgw-b
    proto: RTP
    note: (Izi)
  - id: rtp-trgw-ue
    msg: RTP
    from: trgw-b
    to: ue-b1
    proto: RTP
  # IBCF の 2 つの側
  - id: ibcfa-legs
    do: 受け止めて、新しい要求を送る
    at: ibcf-a
    fields:
      - 内側の UA: 自網のアドレスで待つ
      - 外側の UA: 網のあいだのアドレスで待つ
      - 側ごとに別のもの: Call-ID、tag、CSeq、Via、Contact
  - id: ibcfb-legs
    do: 受け止めて、新しい要求を送る
    at: ibcf-b
    fields:
      - 受け止める側: 網のあいだのダイアログ
      - 新しく送る側: 網 B のダイアログ
  # 取り決めが食い違うとき
  - id: ibcfa-no-message
    do: 取り決めに無いメソッドを断る
    at: ibcf-a
    fields:
      - 取り決め: "`network-b-no-message.yaml` の `methods` に MESSAGE が無い"
      - 返す応答: 405（Method Not Allowed）
      - 網 A の SC: TP-ST 98。SME から応答が無い
  - id: r405
    msg: "405"
    from: ibcf-a
    to: ipsmgw-a
    proto: SIP
    note: (Method Not Allowed)
  - id: ibcfb-codec
    do: 相手網が受けないコーデックを外す
    at: ibcf-b
    fields:
      - 網 B の取り決めの codecs: AMR-WB だけ
      - UE A1 の offer: PCMU
      - 残る音声のコーデック: 無い
  - id: r488
    msg: "488"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (Not Acceptable Here)
  - id: inv-nni-local
    msg: INVITE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (国内形式の番号)
    fields:
      - Request-URI: "`phone-context=+1` の付いた国内形式の番号"
      - 網 A の取り決め: "`network-b-local-numbers.yaml` はそのまま通す"
  - id: ibcfb-number
    do: 国際形式でない番号を断る
    at: ibcf-b
    fields:
      - 網 B の取り決め: 国際形式の番号だけを認める
      - 返す応答: 404（Not Found）。局所的な方針による
  - id: r404
    msg: "404"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (Not Found)
  # 相手網でない送信元からの要求
  - id: inv-99
    msg: INVITE
    from: host99
    to: ibcf-a
    proto: SIP
    note: (取り決めの外から)
  - id: msg-99
    msg: MESSAGE
    from: host99
    to: ibcf-a
    proto: SIP
    note: (取り決めの外から)
  - id: ibcfa-screen
    do: 入れてよいかを尋ねる
    at: ibcf-a
    fields:
      - 尋ねるもの: SIP のスタックが `IBCF.Screen` を呼ぶ
      - 送信元のアドレス: どの相手網の `sources` にも無い
      - スタックの動き: トランザクションも応答も作らずに捨てる
  # 網をまたぐショートメッセージ
  - id: sms-ue-s
    msg: MESSAGE
    from: ue-a1
    to: scscf-a
    proto: SIP
    note: (P-CSCF 経由)
    fields:
      - Request-URI: "`tel:+12025550100`。SC の PSI"
      - 本文: SMS-SUBMIT。TP-DA は `+12025550151`
  - id: sms-s-ip
    msg: MESSAGE
    from: scscf-a
    to: ipsmgw-a
    proto: SIP
    note: (発信側の iFC)
  - id: sms-enum-q
    msg: NAPTR
    from: ipsmgw-a
    to: dns-a
    proto: DNS
    note: (問い合わせ)
    fields:
      - 名前: "`1.5.1.0.5.5.5.2.0.2.1.e164.arpa`"
  - id: sms-enum-a
    msg: NAPTR
    from: dns-a
    to: ipsmgw-a
    proto: DNS
    note: (答え)
    fields:
      - 書き換えの結果: 網 B のドメインの SIP URI に `user=phone`
  - id: sms-ip-ia
    msg: MESSAGE
    from: ipsmgw-a
    to: ibcf-a
    proto: SIP
    note: (SMS-DELIVER)
    fields:
      - Request-URI: 網 B のドメインの番号の SIP URI
      - From と P-Asserted-Identity: "`tel:+12025550100`。番号で表した SC"
      - Route: 自網の IBCF
  - id: sms-nni
    msg: MESSAGE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (網のあいだ)
    fields:
      - 取り決め: どちらの `methods` にも MESSAGE がある
      - 差出人: 番号の SC。網 A のホスト名は越えない
  - id: sms-ib-i
    msg: MESSAGE
    from: ibcf-b
    to: icscf-b
    proto: SIP
  - id: sms-i-sb
    msg: MESSAGE
    from: icscf-b
    to: scscf-b
    proto: SIP
    fields:
      - Request-URI: "`tel:+12025550151`"
  - id: sms-sb-ip
    msg: MESSAGE
    from: scscf-b
    to: ipsmgw-b
    proto: SIP
    note: (着信側の iFC)
    fields:
      - 当たる条件: P-Asserted-Identity が自網の IP-SM-GW でない MESSAGE
  - id: sms-202-nni
    msg: "202"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (網のあいだ)
  - id: sms-202-ip
    msg: "202"
    from: ibcf-a
    to: ipsmgw-a
    proto: SIP
  - id: sms-report
    msg: MESSAGE
    from: ipsmgw-a
    to: scscf-a
    proto: SIP
    note: (status report)
    fields:
      - TP-ST: 1。転送はしたが配送を確かめられない
  - id: sms-report-ue
    msg: MESSAGE
    from: scscf-a
    to: ue-a1
    proto: SIP
    note: (P-CSCF 経由)
  - id: sms-deliver-i
    msg: MESSAGE
    from: ipsmgw-b
    to: icscf-b
    proto: SIP
    note: (SMS-DELIVER)
  - id: sms-deliver-sb
    msg: MESSAGE
    from: icscf-b
    to: scscf-b
    proto: SIP
  - id: sms-deliver-ue
    msg: MESSAGE
    from: scscf-b
    to: ue-b1
    proto: SIP
    note: (P-CSCF 経由)
    fields:
      - RP-OA: 網 A の SC の `12025550100`
      - UE B1 のログ: 差出人は `+12025550101`
  # 境界を越える会議
  - id: conf-inv-m-s
    msg: INVITE
    from: mmtel-a
    to: scscf-a
    proto: SIP
    note: (フォーカス)
    fields:
      - Request-URI: "`tel:+12025550151`"
      - Route: 自網の S-CSCF に `orig` を付ける
      - Contact: 会議の URI に `isfocus`
  - id: conf-inv-s-ia
    msg: INVITE
    from: scscf-a
    to: ibcf-a
    proto: SIP
    fields:
      - P-Asserted-Identity: S-CSCF が担当する PSI の会議の URI
  - id: conf-inv-nni
    msg: INVITE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (網のあいだ)
    fields:
      - Contact: 届いたまま。`isfocus` が付いている
      - Record-Route: IBCF-A が自分の外側の名前を入れる
      - 網のあいだの Record-Route: これだけ
  - id: conf-inv-ib-i
    msg: INVITE
    from: ibcf-b
    to: icscf-b
    proto: SIP
    fields:
      - Record-Route: IBCF-B が自分の内側の名前を入れる
  - id: conf-sub
    msg: SUBSCRIBE
    from: scscf-b
    to: ibcf-b
    proto: SIP
    note: (ダイアログの外)
    fields:
      - Request-URI: 会議の URI
  - id: ibcfb-sub
    do: ダイアログの外の購読を断る
    at: ibcf-b
    fields:
      - 返す応答: 403（Forbidden）。局所的な方針による
      - 参加者: 会議のセッションのダイアログの中で購読し直す
  - id: conf-403
    msg: "403"
    from: ibcf-b
    to: scscf-b
    proto: SIP
    note: (Forbidden)
```

---

# 境界でつながる 2 つの IMS 網

ims-practice 勉強会 第 10 回（IBCF と TrGW、事業者どうしの取り決め、網をまたぐ通話とショートメッセージ）

---

[layout: summary]

## この回で分かること

最初の問い。通話がほかの事業者の網へ出ていきます。相手の事業者には、こちらの網の何が見えるのでしょうか。

- 境界に立つノード。SIP を受け持つ IBCF と、メディアを受け持つ TrGW。
- 2 つの網をつなぐ参照点と、IBCF を自網につなぐ参照点。
- 境界が B2BUA である理由と、それによって隠れるもの。
- 事業者どうしの取り決めが決めることと、2 つの取り決めが食い違うと何が失敗するか。
- 通話、ショートメッセージ、どの相手網でもない送信元からの要求を、3 つのキャプチャのどこで見るか。

> 手順書 [IMS 網どうしを相互接続する](../../../docs/guide/ja/interconnect.md)（`docs/guide/ja/interconnect.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.229 V20.1.0、TS 29.165 V20.0.0、TS 23.204 V19.1.0、TS 24.341 V19.0.0、TS 23.040 V20.0.0、TS 32.409 V19.0.0、TS 24.147 V19.0.0、RFC 3261、RFC 3312、RFC 3966 などです。

---

## 2 つの網

```ims-scene
id: networks
nodes: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a, icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]
lead: 2 つの網をつなぐのは境界のノードだけです。網の中のノードは、相手網のノードに直接は届きません。
steps:
  - id: net-a
    title: 網 A はこれまでの回で使ってきた網
    focus: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a]
    zoom: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a]
    checkpoint: true
    text: |
      ドメインは `ims.mnc001.mcc001.3gppnetwork.org`、UE A1 の番号は `+1-202-555-0101` です。
      この回に出てくるノードだけを描いています。P-CSCF と HSS もこれまでどおりいます。
  - id: net-b
    title: 網 B は相手の事業者の網
    focus: [icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]
    zoom: [icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]
    checkpoint: true
    text: |
      同じイメージを別のドメイン、別の番号、別の加入者で動かします。
      シナリオに要る最小限のノードだけです。MMTel AS、EPC と PCRF、P-CSCF のメディア中継ノード、ラボはありません。
  - id: border
    title: 網のあいだにつながるのは境界のノードだけ
    focus: [ibcf-a, trgw-a, ibcf-b, trgw-b]
    zoom: [ibcf-a, trgw-a, ibcf-b, trgw-b]
    checkpoint: true
    text: |
      ブリッジ `nni0` にいるのは、両方の網の IBCF と TrGW です。
      IBCF は SIP を、TrGW はメディアを運びます。
  - id: both
    title: どの信号も境界を通る
    focus: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      網 A のノードは網 B のノードのアドレスに届きません。通話もショートメッセージも境界を通ります。
      キャプチャはブリッジごとに 1 つです。
```

```text
      網 A（ims0）            網のあいだ（nni0）         網 B（ims1）
     172.28.0.0/24              172.28.2.0/24           172.28.1.0/24

  S-CSCF --- IBCF-A (.90) --- .10 -- Ici -- .20 --- IBCF-B (.90) --- I-CSCF
     agw --- TrGW-A (.91) --- .11 -- Izi -- .21 --- TrGW-B (.91) --- UE B1
```

- 網 A は `compose.yaml` の網で、これまでの回で使ってきた網です。網 B は別の事業者の 2 つ目の IMS で、同じイメージを別のドメイン、別の番号、別の加入者で動かします。
- 網のあいだのブリッジには IBCF と TrGW しかつながっていません。網 A の中のノードは、網 B の中のノードに直接は届きません。
- 網 B は、シナリオに要る最小限のノードだけです。MMTel AS、EPC と PCRF、P-CSCF のメディア中継ノード、ラボはありません。
- キャプチャはブリッジごとに 1 つです。網 A は `captures/`、網 B は `captures/b/`、網のあいだは `captures/nni/` にあります。

> 手順書 [「2 つの網」](../../../docs/guide/ja/interconnect.md#2-つの網)

---

## 両方の網のノード

| | 網 A | 網 B |
| --- | --- | --- |
| ドメイン | `ims.mnc001.mcc001.3gppnetwork.org` | `ims.mnc002.mcc001.3gppnetwork.org` |
| 発信する UE | UE A1、172.28.0.100、`+1-202-555-0101` | UE B1、172.28.1.100、`+1-202-555-0151` |
| IBCF | 網の中は 172.28.0.90、網のあいだは 172.28.2.10 | 網の中は 172.28.1.90、網のあいだは 172.28.2.20 |
| TrGW | 網の中は 172.28.0.91、網のあいだは 172.28.2.11 | 網の中は 172.28.1.91、網のあいだは 172.28.2.21 |
| ショートメッセージの SC | `+12025550100` | `+12025550150` |

- `deploy/interconnect/compose.yaml` が、網 B と網のあいだを `compose.yaml` に重ねて足します。`compose.yaml` は変わりません。網 B のサービスの名前は `-b` で終わり、`scscf-b` は網 B の、`scscf` は網 A の S-CSCF です。
- ノードの設定ファイルは `deploy/interconnect/a` と `deploy/interconnect/b` にあります。どちらの網の番号も、北米番号計画が架空の番号に取ってある範囲のもので、鍵も試験用の値です。

> 手順書 [「2 つの網」](../../../docs/guide/ja/interconnect.md#2-つの網)

---

## IBCF は B2BUA

```ims-scene
id: legs
nodes: [scscf-a, ibcf-a, ibcf-b]
lead: IBCF は側ごとに UA を 1 つ持ちます。届いた要求はそこで終わり、もう一方の側には IBCF 自身の新しい要求が出ていきます。
steps:
  - id: sides
    title: 2 つの側で待つ
    focus: [ibcf-a]
    zoom: [scscf-a, ibcf-a, ibcf-b]
    text: |
      内側の UA は自網のアドレス 172.28.0.90 で、外側の UA は網のあいだのアドレス 172.28.2.10 で待ちます。
      どちらも標準のポート 5060 です。
  - id: exit
    title: 内側に届いた要求は出口を通って出ていく
    show: [inv-s-ia]
    checkpoint: true
    text: |
      網から出ていく要求なので、IBCF-A はこの要求の出口です（TS 24.229 5.10.2）。
      S-CSCF は `orig` の側の扱いを終えて、自網の IBCF へ送っています。
  - id: b2bua
    title: 届いた側で受け止め、もう一方の側で送り直す
    show: [ibcfa-legs]
    focus: [ibcf-a]
    text: |
      IBCF-A は届いた要求を網 A とのダイアログで受け止めます。
      網 B へ出ていくのは網 B とのダイアログで送る IBCF-A 自身の要求です。
  - id: entry
    title: 外側に届いた要求は入口を通って入ってくる
    show: [inv-nni]
    focus: [ibcf-b]
    checkpoint: true
    text: |
      網のあいだの要求は IBCF-B の外側に届きます。網に入ってくる要求なので、IBCF-B はその入口です（5.10.3）。
      網 A のノードの Via、Route、Record-Route、Contact は、この要求に 1 つも入っていません。
  - id: hidden
    title: 網のあいだの要求が持つもの
    only: [inv-nni]
    detail: [inv-nni]
    checkpoint: true
    text: |
      一方の網の中でメッセージを運ぶための値は、相手網とのダイアログに入りません。
      B2BUA はこうして自網の構成を隠します。
```

- TS 29.165 は、IMS 網どうしのあいだのインタフェースを II-NNI と呼びます。そこに立つノードが IBCF で、側ごとに UA を 1 つ持ちます。内側の UA は自網のアドレスで、外側の UA は網のあいだのアドレスで待ちます。
- 内側に届いた要求は網から出ていくので、IBCF はその**出口**です（TS 24.229 5.10.2）。外側に届いた要求は網に入ってくるので、IBCF はその**入口**です（5.10.3）。
- IBCF は、届いた要求を送ってきた側とのダイアログで受け止め、もう一方の側とのダイアログで新しい要求を送ります（TS 24.229 5.10.5、TS 29.165 5.2.1）。ダイアログごとに Call-ID、tag、CSeq、Via、Contact が別々です。
- 一方の網の中でメッセージを運ぶための値は、相手網とのダイアログに入りません。B2BUA は、こうして自網の構成を隠します。
- TrGW は、ここでも役割 `agw` のメディア中継ノードです。ターミネーション `core` は自網を、`access` は相手網の TrGW を向きます。

> 手順書 [「IBCF と TrGW の仕事」](../../../docs/guide/ja/interconnect.md#ibcf-と-trgw-の仕事)

---

## 境界の 4 つの参照点

```ims-scene
id: refpoints
nodes: [scscf-a, ibcf-a, trgw-a, ibcf-b, trgw-b]
lead: 2 つは網と網のあいだにあり、2 つは 1 つの網の中にあります。II-NNI を作るのはあいだの 2 つです。
steps:
  - id: mx
    title: Mx は CSCF と自網の IBCF のあいだ
    show: [mx]
    text: |
      SIP を運びます。S-CSCF の設定 `ibcf` が指定し、出ていく要求の Route の先頭に載ります。
      1 つの網の中の参照点です（TS 29.165 3.2）。
  - id: ici
    title: Ici は 2 つの網の IBCF のあいだ
    show: [ici]
    checkpoint: true
    text: |
      網のあいだの SIP です。SDP 付きの INVITE は 1300 バイトを超えるので、TCP で送ります（RFC 3261 18.1.1）。
  - id: izi
    title: Izi は 2 つの網の TrGW のあいだ
    show: [izi]
    checkpoint: true
    text: |
      境界を越える RTP が流れます。Ici と Izi の 2 つが II-NNI です（TS 29.165 5.1）。
  - id: ix
    title: Ix は IBCF と自網の TrGW のあいだ
    show: [ix]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a, ibcf-b, trgw-b]
    checkpoint: true
    text: |
      標準では H.248 です（TS 29.238）。ここでは、P-CSCF が中継ノードに使うのと同じ HTTP の制御 API です。
      これも 1 つの網の中の参照点です。
```

| 参照点 | どこの間か | このプロジェクトでは |
| --- | --- | --- |
| Mx | CSCF と自網の IBCF | SIP。S-CSCF の設定 `ibcf` が指定し、Route の先頭に載ります。 |
| Ici | 2 つの網の IBCF | 網のあいだの SIP。SDP 付きの INVITE は 1300 バイトを超えるので TCP で送ります（RFC 3261 18.1.1）。 |
| Izi | 2 つの網の TrGW | 境界を越える RTP。 |
| Ix | IBCF と自網の TrGW | 標準では H.248（TS 29.238）。ここでは P-CSCF が中継ノードに使うのと同じ HTTP の制御 API です。 |

- Ici と Izi が II-NNI です（TS 29.165 5.1）。Mx と Ix は 1 つの網の中にあります（TS 29.165 3.2）。
- 第 2 回は、この 4 つを網の地図の上に描きました。この回は、そこを流れるものを読みます。

> 手順書 [「IBCF と TrGW の仕事」](../../../docs/guide/ja/interconnect.md#ibcf-と-trgw-の仕事)

---

## 実行する

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml up -d --build

# UE B1 が着信を待つ。UE シミュレータには網 B の設定をフラグで渡す
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue-b1 answer \
  -domain ims.mnc002.mcc001.3gppnetwork.org \
  -impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org \
  -impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org \
  -password test-password-b1 \
  -pcscf 172.28.1.40:5060 -pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060

# UE A1 がその番号にかける。網 A の設定はこれまでどおり
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue call tel:+12025550151
```

- 網 B の `ue sms` には、フラグがもう 1 つ要ります。網 B の SC の番号を渡す `-sc` です。
- `scripts/e2e-interconnect.sh` は、この回のすべてを実行して 3 つのキャプチャで確かめます。

> 手順書 [「動かす」](../../../docs/guide/ja/interconnect.md#動かす)

---

## 3 つのキャプチャを開く

| キャプチャ | 入っているもの |
| --- | --- |
| `captures/ims-*.pcap` | 網 A。ENUM、S-CSCF から IBCF-A への INVITE、IBCF-A が渡してくる網 B のメッセージ。 |
| `captures/nni/ims-*.pcap` | 網のあいだ。2 つの IBCF と 2 つの TrGW だけです。 |
| `captures/b/ims-*.pcap` | 網 B。IBCF-B から I-CSCF へ渡ったあとの INVITE と、TrGW-B の RTP。 |

- キャプチャ用のコンテナはブリッジごとに 1 つです。3 つとも開いてください。同じ通話が、どのファイルにも別々の Call-ID で入っています。
- これまでの回のフィルタは、どのファイルでもそのまま使えます。`sip.Method == "INVITE" && count(sip.Via) == 1` は、網のあいだから IBCF の INVITE を選び出します。
- 網のあいだのキャプチャで **Statistics > Flow Graph** を開くと、2 つの IBCF のあいだのシーケンス図になります。

> 手順書 [「各キャプチャに見えるもの」](../../../docs/guide/ja/interconnect.md#各キャプチャに見えるもの)

---

## 網 A から網 B への通話

```ims-scene
id: overview
view: ladder
nodes: [ue-a1, scscf-a, ibcf-a, ibcf-b, icscf-b, hss-b, scscf-b, ue-b1]
lead: 1 つの通話が、2 つの網と、そのあいだのリンクに分かれて進みます。境界では、要求がいったん終わって作り直されます。
steps:
  - id: out
    title: 網 A の中を進み、境界まで届く（1 から 3）
    show: [inv-ue-s, inv-s-ia]
    detail: []
    checkpoint: true
    text: |
      UE A1 はいつもの通話と同じように番号へ INVITE を送ります。P-CSCF と MMTel AS はこの図に出していません。
      S-CSCF は、番号が自網のものでないと分かると、自網の IBCF へ送ります。あいだの ENUM も図には出していません。
  - id: across
    title: 境界を越え、網 B の入口へ（4、5）
    show: [inv-nni, inv-ib-i]
    detail: []
    checkpoint: true
    text: |
      網のあいだの INVITE は IBCF-A 自身のもので、網 B へ入る INVITE は IBCF-B 自身のものです。
      各 IBCF は届いたダイアログを終え、自分のダイアログを始めます。この図に出していない MMTel AS のダイアログを入れると、通話全体で 4 つです。
  - id: find
    title: 網 B が着信者を探す（6）
    show: [lir, lia, inv-i-sb, inv-sb-ue]
    active: lir
    detail: []
    checkpoint: true
    text: |
      I-CSCF は番号を tel URI に直してから HSS に尋ね、UE B1 の S-CSCF を教わります。
      そこから先は網 B の中のいつもの着信です。
  - id: answer
    title: answer が UE A1 まで戻る（7）
    only: [r183-ue-sb, r183-sb-i, r183-i-ib, r183-ib-ia, r183-ia-s, r183-s-ue]
    active: r183-s-ue
    detail: []
    checkpoint: true
    text: |
      183 は Via に沿って戻るので、Record-Route を付けていない I-CSCF も通ります。
      各 IBCF は受け取った 183 をもう一方のダイアログの自分の 183 として渡します。
  - id: rest
    title: PRACK、ACK、BYE はダイアログごとにある
    only: [inv-s-ia, inv-nni, inv-ib-i]
    active: inv-nni
    detail: []
    checkpoint: true
    text: |
      4 つのダイアログには、それぞれの PRACK、ACK、BYE があります（8）。図にない 4 つめは、UE A1 と MMTel AS のあいだです。
      RTP は UE A1、網 A のメディア中継ノード、TrGW-A、TrGW-B、UE B1 の順に流れます（9）。
```

```ladder
UE A1, S-CSCF, DNS-A, IBCF-A, IBCF-B, I-CSCF, UE B1
UE A1 -> S-CSCF: INVITE tel:+12025550151。P-CSCF と MMTel AS を通る // 1
S-CSCF <-> DNS-A: ENUM。網 B のドメインの SIP URI が返る // 2
S-CSCF -> IBCF-A: INVITE。Route の先頭に IBCF // 3
IBCF-A -> IBCF-B: 網のあいだの INVITE。IBCF-A 自身のダイアログ // 4
IBCF-B -> I-CSCF: INVITE。番号は SIP URI のまま // 5
I-CSCF -> UE B1: INVITE tel:+12025550151。S-CSCF と P-CSCF を通る // 6
UE A1 <-- S-CSCF <-- IBCF-A <-- IBCF-B <-- I-CSCF <-- UE B1: answer を載せた 183 // 7
```

- S-CSCF は、AS がもう残っていない段階で番号を ENUM で変換し（TS 24.229 5.4.3.2 step 10）、自網のドメインでない宛先を自網の IBCF へ送ります（step 11）。
- 網 B の HSS は UE B1 を tel URI で知っているので、I-CSCF は LIR の前に Request-URI を tel URI に置き換えます（TS 24.229 5.3.2.1 item 1 b）。
- PRACK、ACK、BYE はダイアログごとに別々です。RTP は UE A1 から網 A のメディア中継ノード、TrGW-A、TrGW-B を通って UE B1 に届きます。
- どちらの UE も相手網を知りません。UE A1 の相手は 172.28.0.50、UE B1 の相手は 172.28.1.91 です。

> 手順書 [「網 A から網 B への通話」](../../../docs/guide/ja/interconnect.md#網-a-から網-b-への通話)

---

## 網 A が相手網を見つけて送り出す（1〜4）

```ims-scene
id: exit
nodes: [dns-a, ue-a1, scscf-a, ibcf-a, trgw-a, ibcf-b]
lead: 番号が相手網のものだと分かるのは ENUM の答えからです。そこから先の宛先は自網の IBCF です。
steps:
  - id: invite
    title: UE A1 が番号に電話をかける
    show: [inv-ue-s]
    checkpoint: true
    text: |
      いつもの通話と同じ INVITE です。P-CSCF を通り、発信側のフィルタ条件で MMTel AS を通ってから S-CSCF に戻ります。
      ここまでは、番号が別の網のものだと誰も知りません。
  - id: enum
    title: ENUM で網 B のドメインが分かる
    hide: [inv-ue-s]
    show: [enum-q, enum-a]
    active: enum-a
    detail: [enum-q, enum-a]
    zoom: [dns-a, scscf-a, ibcf-a]
    checkpoint: true
    text: |
      呼び出す AS が残っていないので、S-CSCF は番号を ENUM で変換します。
      レコードに書いてあるのは番号で、加入者の私的な識別子ではありません。
  - id: route
    title: S-CSCF が自網の IBCF を Route の先頭に入れる
    show: [scscf-exit]
    focus: [scscf-a]
    zoom: [dns-a, scscf-a, ibcf-a]
    text: |
      ドメインが自網のものでないので、相互接続の機能を持つ自網の IBCF を通します。
      設定 `ibcf` が無ければ、S-CSCF は網 B のドメインに自分で送ろうとして、どこにも届きません。
  - id: arrive
    title: 要求が IBCF-A の内側に届く
    hide: [enum-q, enum-a]
    show: [inv-s-ia]
    checkpoint: true
    text: S-CSCF は今までどおり Record-Route に自分を入れているので、ダイアログの中の要求も S-CSCF を通ります。
  - id: border
    title: IBCF-A が出口として要求を作り直す
    show: [ibcfa-exit]
    focus: [ibcf-a]
    zoom: [scscf-a, ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      届いた要求の写しに、相手網との取り決めを当てます。
      何を写し、何を消し、どう書き換えるかは、この取り決めが決めます。
  - id: trgw
    title: 自網の TrGW にコンテキストを作る
    show: [trgw-add]
    detail: [trgw-add]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a]
    checkpoint: true
    text: |
      呼のメディアは、両方の網に面した 2 つのターミネーションを通ります。
      コンテキストは呼に 1 つで、最初の SDP を見たときに確保します。
  - id: trgw-sdp
    title: 側ごとにアドレスを書き分ける
    hide: [trgw-add]
    show: [trgw-mod-a]
    detail: [trgw-mod-a]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a]
    checkpoint: true
    text: |
      中継ノードは両方のターミネーションに同じアドレスを書くので、IBCF が側ごとに書き分けます。
      網のあいだへ出ていく SDP には、TrGW-A の網のあいだ側のアドレスが入ります。
  - id: send
    title: IBCF-A が自分の INVITE を網のあいだへ送る
    only: [inv-s-ia, inv-nni]
    detail: [inv-nni]
    checkpoint: true
    text: |
      Route には取り決めに書かれた相手網の入口 IBCF-B が入ります。
      Record-Route に自分は入れません。網 B からの要求は Contact で IBCF-A に届きます。
```

一方のダイアログから他方へ何を写すかは、相手網との取り決め（相互接続プロファイル）が決めます。

| 何をするか | 参照 |
| --- | --- |
| 信頼ドメインの外に出してはいけないヘッダを消す | TS 24.229 4.4 |
| メソッド、option tag、ヘッダを調べる | TS 24.229 5.10.6 |
| 番号を取り決めの形式に直す | TS 29.165 8.1 |
| 相手網が受けないコーデックを SDP から外す | TS 29.165 7.1 |
| メディアを TrGW に通し、そのアドレスを SDP に書く | TS 24.229 6.7.1.4.1 |

- 2 つの事業者には取り決めがあるので、互いに相手の信頼ドメインに入っています（`trusted: true`、TS 24.229 4.4.1）。P-Asserted-Identity は境界を越え、UE B1 には誰からの着信かが分かります（4.4.2）。
- P-Charging-Function-Addresses は越えません（5.10.2.2 step 9）。`trusted: false` にすると、UE B1 には発信者の番号が付かないまま着信します。

> 手順書 [2](../../../docs/guide/ja/interconnect.md#2-enum-で網-b-が分かる)、[3](../../../docs/guide/ja/interconnect.md#3-s-cscf-が-ibcf-a-に送る)、[4](../../../docs/guide/ja/interconnect.md#4-ibcf-a-が出口として働く)、[「信頼ドメイン」](../../../docs/guide/ja/interconnect.md#信頼ドメイン)

---

## 網 B に入って UE B1 に届くまで（5〜7）

```ims-scene
id: entry
nodes: [ibcf-a, ibcf-b, trgw-b, icscf-b, hss-b, scscf-b, ue-b1]
lead: 相手網から来た要求だと分かるのは送信元のアドレスからです。そこから先は網 B の中のいつもの着信です。
steps:
  - id: arrive
    title: 要求が IBCF-B の外側に届く
    show: [inv-nni]
    zoom: [ibcf-a, ibcf-b, icscf-b]
    checkpoint: true
    text: 送信元は IBCF-A の網のあいだのアドレスです。網 B の中からは、この要求が見えません。
  - id: border
    title: IBCF-B が入口として要求を作り直す
    show: [ibcfb-entry]
    focus: [ibcf-b]
    zoom: [ibcf-a, ibcf-b, icscf-b]
    checkpoint: true
    text: |
      送信元のアドレスで相手網が分かるので、網 A との取り決めを当てます。
      Route はもう残っていないので、次の宛先は設定した I-CSCF です。
  - id: icscf
    title: 要求が網 B の入口に入る
    show: [inv-ib-i]
    text: ここから先は網 B の中です。Via も Call-ID も、IBCF-B が作ったものだけです。
  - id: tel
    title: I-CSCF が Request-URI を tel URI に直す
    show: [icscfb-tel]
    focus: [icscf-b]
    zoom: [icscf-b, hss-b, scscf-b]
    checkpoint: true
    text: |
      相手網からの番号は `user=phone` の付いた SIP URI で届きます。
      HSS はこの形では利用者を知らないので、尋ねる前に tel URI に直します。
  - id: lir
    title: HSS に着信者の所在を尋ねる（LIR と LIA）
    show: [lir, lia]
    active: lia
    detail: [lia]
    zoom: [icscf-b, hss-b, scscf-b]
    text: 登録の回と通話の回と同じ Cx の問い合わせです。網 A からの通話でも変わりません。
  - id: deliver
    title: 網 B の中を UE B1 まで進む
    show: [inv-i-sb, inv-sb-ue]
    active: inv-sb-ue
    detail: [inv-sb-ue]
    checkpoint: true
    text: |
      S-CSCF は着信側として扱い、UE B1 の Path に沿って網 B の P-CSCF へ送ります。
      UE B1 に届く SDP が指すのは TrGW-B です。相手網のアドレスは入っていません。
  - id: answer
    title: UE B1 が answer を載せた 183 を返す
    show: [r183-ue-sb, r183-sb-i, r183-i-ib]
    active: r183-i-ib
    detail: [r183-ue-sb]
    checkpoint: true
    text: |
      信頼性のある 183 です。Via に沿って戻るので、I-CSCF も通ります。
      IBCF-B はこれを網のあいだのダイアログの自分の 183 として渡します。
```

- IBCF-B は外側で要求を受け、送信元のアドレスが網 A の取り決めにあるので、網 A からの要求だと分かります。プロファイル `network-a.yaml` を当て、Route から自分の URI を外します（TS 24.229 5.10.3.2 の手順 1C）。
- Route が残っていないので、設定した I-CSCF へ、自分のダイアログで新しい INVITE を送ります（手順 4）。メディアも同じように TrGW-B に通すので、網 B に届く SDP は 172.28.1.91 を指します。
- 網 B の HSS は UE B1 を tel URI で知っていて、`user=phone` の付いた SIP URI では知りません。そこで I-CSCF は、LIR を送る前に Request-URI を `tel:+12025550151` に置き換えます（TS 24.229 5.3.2.1 の手順 1 の b）。
- 網 B の S-CSCF には tel URI が届き、あとはいつもの着信と同じように、網 B の P-CSCF を通って UE B1 に届きます。

> 手順書 [5](../../../docs/guide/ja/interconnect.md#5-ibcf-b-が入口として働く)、[6](../../../docs/guide/ja/interconnect.md#6-網-b-の-i-cscf-が着信者を探す)、[7](../../../docs/guide/ja/interconnect.md#7-答えが戻る)

---

## 境界まわりの 3 つのダイアログと 3 つの Call-ID

```ims-scene
id: dialogs
view: ladder
nodes: [scscf-a, ibcf-a, ibcf-b, icscf-b]
lead: 通話の 4 つのダイアログのうち 3 つが境界のまわりにあり、キャプチャごとに 1 つずつ、別の Call-ID を持ちます。4 つめは UE A1 と MMTel AS のあいだです。
steps:
  - id: first
    title: 網 A のキャプチャの INVITE
    show: [inv-s-ia]
    detail: [inv-s-ia]
    checkpoint: true
    text: |
      S-CSCF から IBCF-A への INVITE で、通話の 2 つめのダイアログです。MMTel AS が作りました。Call-ID も Via も網 A のものです。
      この INVITE は IBCF-A で終わります。
  - id: legs-a
    title: IBCF-A が受け止めて送り直す
    show: [ibcfa-legs]
    focus: [ibcf-a]
    text: 2 つの UA のあいだで写すものを、相手網との取り決めが決めます。
  - id: nni
    title: 網のあいだのキャプチャの INVITE
    show: [inv-nni]
    detail: [inv-nni]
    checkpoint: true
    text: |
      Via は IBCF-A の外側の名前の 1 つだけで、Record-Route はありません。
      Max-Forwards は 1 つ減っています。2 つの網が要求を回し合っても、いつかは終わるようにするためです。
  - id: legs-b
    title: IBCF-B も同じことをする
    show: [ibcfb-legs]
    focus: [ibcf-b]
    text: 網のあいだのダイアログで受け止め、網 B とのダイアログで自分の要求を送ります。
  - id: third
    title: 網 B のキャプチャの INVITE
    show: [inv-ib-i]
    detail: [inv-ib-i]
    checkpoint: true
    text: Via も Call-ID も IBCF-B のものです。網 A の値は 1 つも入っていません。
  - id: compare
    title: 3 つの Call-ID を見比べる
    active: ''
    detail: [inv-s-ia, inv-nni, inv-ib-i]
    checkpoint: true
    text: ダイアログごとに Call-ID が違うので、追うときは、キャプチャごとに Call-ID を取り直します。
```

網のあいだの INVITE を短くしたものです。

```text
INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0
Via: SIP/2.0/TCP ibcf.nni.mnc001.example.net:5060;branch=z9hG4bKW6NI6...;rport
Max-Forwards: 65
Route: <sip:ibcf.nni.mnc002.example.net:5060;lr>
From: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;tag=5MWO4Z6R4C
Call-ID: FMZQJ3BZL2YI4ILP6CEIPAUT5Z
Contact: <sip:ibcf.nni.mnc001.example.net:5060>
```

- **Via** は IBCF-A の 1 つだけです。網 A のノードの Via は IBCF-A に届いた INVITE に付いていて、その INVITE は IBCF-A で終わっています。
- **Record-Route** はありません。このダイアログの相手端は IBCF-A なので、網 B からの要求は Contact に届きます。
- **Call-ID** は IBCF-A のものです。IBCF-A に届いた INVITE、IBCF-B が送り出した INVITE と見比べてください。この 3 つは別の Call-ID を持ちます。網 A のキャプチャにある UE A1 から P-CSCF への INVITE には、4 つめの Call-ID があります。
- **Max-Forwards** は、IBCF-A がプロキシと同じく 1 つ減らしています（RFC 3261 16.6 step 3）。2 つの網が要求を回し合っても、いつかは終わるようにするためです。

> 手順書 [「キャプチャで見る境界まわりの 3 つのダイアログ」](../../../docs/guide/ja/interconnect.md#キャプチャで見る境界まわりの-3-つのダイアログ)

---

## メディアが 2 つの TrGW を通る（9）

```ims-scene
id: media
nodes: [ue-a1, scscf-a, ibcf-a, agw-a, trgw-a, ibcf-b, trgw-b, ue-b1]
lead: どちらの UE も自分の網のアドレスへメディアを送ります。相手網のアドレスはどちらの SDP にも入りません。
steps:
  - id: modify-b
    title: IBCF-B が TrGW-B に UE B1 のアドレスを教える
    show: [trgwb-mod]
    focus: [ibcf-b, trgw-b]
    zoom: [ibcf-b, trgw-b, ue-b1]
    checkpoint: true
    text: |
      UE B1 の answer は網 B の P-CSCF、S-CSCF、I-CSCF を通って IBCF-B に戻っています。この区間は図に出していません。
      網 B の SDP が指すのは UE B1 です。網 B には P-CSCF のメディア中継ノードがありません。
  - id: nni
    title: 網のあいだの 183 は TrGW-B のアドレスを運ぶ
    show: [r183-ib-ia]
    detail: [r183-ib-ia]
    zoom: [ibcf-a, ibcf-b, trgw-a, trgw-b]
    checkpoint: true
    text: 網のあいだに出ていく SDP には、TrGW-B の網のあいだ側のアドレスが入ります。
  - id: modify-a
    title: IBCF-A が TrGW-A に相手の TrGW を教える
    show: [trgwa-mod-back]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a, trgw-b]
    text: TrGW-A の access 側はこれから TrGW-B へ送ります。
  - id: to-a
    title: 網 A に戻る 183 は TrGW-A のアドレスを運ぶ
    show: [r183-ia-s, r183-s-ue]
    active: r183-s-ue
    detail: [r183-ia-s, r183-s-ue]
    checkpoint: true
    text: |
      網 A へ渡す SDP には TrGW-A の網 A 側のアドレスが入り、P-CSCF がさらに自網の中継ノードに書き換えます。
      UE A1 が受け取るアドレスは 172.28.0.50 です。
  - id: rtp
    title: RTP が 4 つの区間を通る
    only: [rtp-ue-agw, rtp-agw-trgw, rtp-nni, rtp-trgw-ue]
    active: rtp-nni
    checkpoint: true
    text: |
      UE A1 から UE B1 への向きです。逆の向きも同じ区間を戻ります。
      スクリプトは、UE A1 が送った PCMU のペイロードと UE B1 が受けたものが同じことを確かめます。
  - id: ends
    title: どちらの UE も相手網を知らない
    focus: [ue-a1, ue-b1]
    checkpoint: true
    text: |
      UE A1 の相手は 172.28.0.50、UE B1 の相手は 172.28.1.91 です。どちらも自分の網のアドレスです。
      境界を越える区間のアドレスは 2 つの TrGW のものだけです。
```

| 見るもの | 見どころ |
| --- | --- |
| 網 A のキャプチャの SDP | `c=` は 172.28.0.50 か 172.28.0.91。網 A の中のアドレスだけ。 |
| 網のあいだのキャプチャの SDP | `c=` は 172.28.2.11 か 172.28.2.21。2 つの TrGW の網のあいだ側。 |
| 網 B のキャプチャの SDP | `c=` は 172.28.1.91 か UE B1 のアドレス。 |
| 境界を越える RTP | TrGW どうしのあいだだけ。UE のアドレスは出てこない。 |

フィルタは `sdp.connection_info.address == 172.28.2.11` と `rtp && ip.addr == 172.28.2.0/24` です。

- IBCF は SDP ごとに、送った側に面したターミネーションへ、その側がこれからどこで受けるかを伝えます。そのあとで、もう一方の側に向けた TrGW のアドレスを SDP に書きます（TS 24.229 6.7.1.4.1）。
- この伝達が失敗すると、IBCF はその SDP を境界の向こうへ渡しません。越えると、向こう側が TrGW を通らずにそのアドレスへ直接 RTP を送ってしまい、相手網に自網のアドレスが分かるためです。手順書の「TrGW が SDP を受け取らないとき」にあります。

> 手順書 [7](../../../docs/guide/ja/interconnect.md#7-答えが戻る) と [9](../../../docs/guide/ja/interconnect.md#9-メディア)

---

## 網の構成を隠す

| キャプチャ | 出てはいけないもの |
| --- | --- |
| 網 A | 172.28.1.0/24 のアドレスと、網 B のノードのホスト名。 |
| 網のあいだ | どちらの網の内側のアドレスも、そのノードのホスト名も、網 A の IMSI から作った識別子も、Record-Route も。 |
| 網 B | 172.28.0.0/24 のアドレス、網 A のノードのホスト名、網 A の IMSI から作った識別子。 |

- スクリプトは、アドレスを IP ヘッダとフレームの中身の両方で探し、ノードの名前を相手網のドメインの手前で探します。どれも見つかりません。
- 自網の名前のうち、相手網に出てよいものもあります。ドメインそのものです。From と P-Asserted-Identity の識別子に入っています。相手網は、折り返し電話をかけるために利用者の番号が要ります。
- 番号とドメインからは、中のノードのことも IMSI のことも分かりません。

> 手順書 [「各キャプチャに見えるもの」](../../../docs/guide/ja/interconnect.md#各キャプチャに見えるもの)

---

## 事業者どうしの取り決め

| 項目 | 決めること |
| --- | --- |
| `name`、`domain` | 相手網と、番号が SIP URI で出ていくときのホスト（TS 29.165 8.1）。 |
| `trusted` | 相手網が信頼ドメインに入るかどうか（TS 24.229 4.4）。 |
| `transports`、`methods` | 送るときのトランスポートと、取り決めで認めた任意のメソッド（MESSAGE など。TS 29.165 6.2、Table 6.1）。 |
| `option_tags` | option tag ごとに `pass`、`remove`、`reject`。`reject` なら、その拡張を要求する要求に 420 を返します。 |
| `headers` | 事業者が決めた、ヘッダを消す規則と書き換える規則（TS 24.229 5.10.6.2）。 |
| `number_format`、`local_numbers` | 番号の形式と、ローカル番号をグローバル番号にする方法（TS 29.165 8.1）。 |
| `codecs` | 相手網が受けるペイロード形式（TS 29.165 7.1）。 |

- `profiles/network-b.yaml` が網 A の網 B との取り決め、`network-a.yaml` が網 B の網 A との取り決めです。TS 29.165 Annex C は、こうした選択を 2 つの事業者が埋める option item として並べています。
- `IBCF_A_PROFILE` と `IBCF_B_PROFILE` で別のファイルを選べます。Compose はその IBCF を選び直したファイルで起動し直します。

> 手順書 [「プロファイル」](../../../docs/guide/ja/interconnect.md#プロファイル)

---

## 2 つの取り決めが食い違うとき

```ims-scene
id: mismatch
nodes: [ipsmgw-a, scscf-a, ibcf-a, ibcf-b]
lead: 失敗させているのは UE ではなく、取り決めに従った IBCF です。断ったノードは応答を運ぶ最初のパケットの送信元で分かります。
steps:
  - id: message
    title: 網 A の取り決めに MESSAGE が無い
    only: [sms-ip-ia, ibcfa-no-message]
    focus: [ibcf-a]
    zoom: [ipsmgw-a, scscf-a, ibcf-a]
    text: |
      MESSAGE は任意のメソッドなので、境界を越えるには取り決めが要ります。
      取り決めに無いメソッドなので、IBCF-A はこの要求を先へ送りません。
  - id: m405
    title: IBCF-A が 405 を返す
    show: [r405]
    detail: [ibcfa-no-message]
    zoom: [ipsmgw-a, scscf-a, ibcf-a]
    checkpoint: true
    text: |
      網 A の SC は転送できなかったと UE A1 に報告します。
      取り決めを `network-b.yaml` に戻すと、同じショートメッセージが越えて TP-ST 1 になります。
  - id: codec
    title: 網 B が AMR-WB しか受けない
    only: [inv-nni, ibcfb-codec]
    focus: [ibcf-b]
    zoom: [ibcf-a, ibcf-b]
    text: |
      相手網が受けるコーデックは取り決めの `codecs` にあります。
      IBCF-B はそこに無いコーデックを offer から外します。
  - id: c488
    title: IBCF-B が 488 を返す
    show: [r488]
    detail: [ibcfb-codec]
    zoom: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      音声のコーデックが 1 つも残らないので、このセッションは成り立ちません。
      コーデックの変換はしません。TrGW にコーデックが要り、このプロジェクトのノードは AMR を符号化も復号もしないためです。
  - id: number
    title: ローカル番号がそのまま越える
    only: [inv-nni-local, ibcfb-number]
    active: inv-nni-local
    detail: [inv-nni-local]
    focus: [ibcf-a, ibcf-b]
    zoom: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      網 A の取り決めが国内形式の番号をそのまま通すので、境界を越えた番号も国内形式のままです。
      網 B の取り決めは国際形式の番号だけを認めます。
  - id: n404
    title: IBCF-B が 404 を返す
    show: [r404]
    detail: [ibcfb-number]
    zoom: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      局所的な方針による拒否です（TS 24.229 5.10.1）。
      `network-b-global-numbers.yaml` にすると、IBCF-A が送る前に国際形式に直します。
```

| 食い違い | 起きること | 合わせた取り決め |
| --- | --- | --- |
| 網 A の取り決めに MESSAGE が無い | IBCF-A がショートメッセージに 405（Method Not Allowed）を返します。網 A の SC は TP-ST 98、「SME から応答が無い」を報告します。 | `network-b.yaml`。TP-ST 1 になります |
| 網 B が AMR-WB しか受けない | IBCF-B が UE A1 の PCMU の offer から音声のコーデックを全部外し、488（Not Acceptable Here）を返します。 | PCMU を挙げた `network-a.yaml` |
| ローカル番号がそのまま越える | 網 B の取り決めはグローバル番号しか認めないので、IBCF-B がローカルポリシーで INVITE を 404（Not Found）で断ります。 | `network-b-global-numbers.yaml`。IBCF-A が出す前にグローバル番号にします |

- 失敗させているのは UE ではなく、取り決めに従った IBCF です。網のあいだの INVITE と、網 B に届いた INVITE を見比べてください。
- スクリプトは食い違いを 1 つずつ試し、そのあとで合う取り決めに替えます。キャプチャには、失敗した通話と通った通話が続けて残ります。

> 手順書 [「プロファイルが食い違うとき」](../../../docs/guide/ja/interconnect.md#プロファイルが食い違うとき)。TS 29.165 7.1、Table 6.1。TS 24.229 5.10.1。

---

## 相手網でない送信元からの要求

```ims-scene
id: screen
nodes: [host99, ibcf-a, scscf-a]
lead: 取り決めを結んでいない送り手には、境界は何も答えません。そこに SIP のノードがあることさえ知らせません。
steps:
  - id: invite
    title: 網のあいだのホストが INVITE を送る
    show: [inv-99]
    zoom: [host99, ibcf-a]
    text: |
      172.28.2.99 は網のあいだにいて、IBCF-B でも TrGW でもありません。
      スクリプトはこのアドレスから IBCF-A に INVITE と MESSAGE を送ります。
  - id: screen
    title: スタックが IBCF に入れてよいかを尋ねる
    show: [ibcfa-screen]
    focus: [ibcf-a]
    zoom: [host99, ibcf-a]
    checkpoint: true
    text: |
      手持ちのどのトランザクションにも属さない要求は、扱う前に毎回この問いを通ります。
      答えが「いいえ」なら、スタックはその要求を捨てます。
  - id: message
    title: MESSAGE も同じように捨てる
    show: [msg-99]
    active: msg-99
    zoom: [host99, ibcf-a]
    text: |
      どのメソッドでも、再送でも同じです。捨てた要求のことは何も覚えていません。
      100（Trying）も誤りの応答も返りません。
  - id: nothing
    title: 網 A には何も入らない
    focus: [scscf-a]
    checkpoint: true
    text: |
      網 A のキャプチャにこのホストのものは 1 つも残りません。
      網のあいだのキャプチャに残るのは、答えの無い 2 つの矢印だけです。
```

```text
172.28.2.99 のホスト        IBCF-A ║ 網 A
  |  INVITE                    |   ║
  |--------------------------->| x ║   （捨てる。172.28.2.99 から送る相手網は無い）
  |  MESSAGE                   |   ║
  |--------------------------->| x ║
  |                            |   ║
  |  （何も返ってこない）      |   ║   （網 A には何も入らない）
```

- IBCF-A が外側で受けるのは、網 B のプロファイルが挙げたアドレスからの要求だけです（`ibcf.yaml` の `sources`）。
- どちらの要求も、何も返さずに捨てます。100（Trying）もエラー応答も返しません。送信元には網のことが何も分かりません。そこで SIP のノードが待っていることさえ分かりません。
- SIP のスタックは、自分の持っているトランザクションに属さない要求を、扱う前に毎回 IBCF に尋ねます（`IBCF.Screen`）。どのメソッドでも、再送でも同じです。
- 取り決めの中にいる相手網には応答を返します。取り決めに無いメソッドへの 405 がその例です。TS 24.229 5.10.1 は、IBCF がローカルポリシーで要求を 4xx で断ることを認めています。

> 手順書 [「相手網でない送信元からの要求」](../../../docs/guide/ja/interconnect.md#相手網でない送信元からの要求)

---

## 網をまたぐショートメッセージ

```ims-scene
id: sms
nodes: [dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, ibcf-b, icscf-b, scscf-b, ue-b1, ipsmgw-b]
lead: 境界を越える MESSAGE の差出人は、番号で表した SC です。網 A のホスト名は越えません。
steps:
  - id: submit
    title: UE A1 が自分の SC にショートメッセージを送る
    show: [sms-ue-s, sms-s-ip]
    active: sms-s-ip
    detail: [sms-ue-s]
    zoom: [ue-a1, scscf-a, ipsmgw-a, ibcf-a]
    checkpoint: true
    text: |
      SMS の回と同じ MESSAGE です。発信側のフィルタ条件がこれを IP-SM-GW に渡します。
      SC のスタブはまず網 A の HSS に番号を尋ね、利用者がいないと返されます。この問い合わせは図に出していません。
  - id: enum
    title: SC が ENUM で相手網を見つける
    show: [sms-enum-q, sms-enum-a]
    active: sms-enum-a
    detail: [sms-enum-a]
    zoom: [dns-a, scscf-a, ipsmgw-a, ibcf-a]
    checkpoint: true
    text: |
      設定に `ibcf` があるので、SC は自網で見つからない番号を ENUM で引きます。
      SC はここでメッセージを受け付け、UE A1 には submit report が届きます。
  - id: deliver
    title: IP-SM-GW が IBCF-A へ MESSAGE を送る
    only: [sms-ip-ia]
    detail: [sms-ip-ia]
    zoom: [ipsmgw-a, ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      本文は SMS-DELIVER です。宛先は、ENUM が返した網 B のドメインの SIP URI です。
      差出人を番号の SC にするのは網 A のホスト名を境界の外に出さないためです。
  - id: cross
    title: MESSAGE が境界を越える
    show: [sms-nni, sms-ib-i, sms-i-sb]
    active: sms-nni
    detail: [sms-nni]
    checkpoint: true
    text: |
      どちらのプロファイルも MESSAGE を認めています。
      網 B では、I-CSCF が Request-URI を tel URI に直し、S-CSCF に渡します。図はそのあいだの LIR と LIA を省いています。
```

```ladder
UE A1, IP-SM-GW-A, IBCF-A, IBCF-B, IP-SM-GW-B, UE B1
UE A1 -> IP-SM-GW-A: SMS-SUBMIT を載せた MESSAGE // 1
IP-SM-GW-A -> IBCF-A: SMS-DELIVER を載せた MESSAGE。差出人は SC の番号 // 3
IBCF-A -> IBCF-B: 網のあいだの MESSAGE
IBCF-B -> IP-SM-GW-B: MESSAGE。I-CSCF と S-CSCF を通る // 4
IP-SM-GW-A <-- IBCF-A <-- IBCF-B <-- IP-SM-GW-B: 202 // 5
UE A1 <-- IP-SM-GW-A: MESSAGE。TP-ST 1 の status report
IP-SM-GW-B -> UE B1: SMS-DELIVER を載せた MESSAGE // 6
```

- SC のスタブは網 A の HSS に番号を尋ねて「利用者が分からない」と返され、設定に `ibcf` があると、次は ENUM で網 B のドメインに番号を見つけます。どちらのプロファイルも MESSAGE を認めています。
- 境界を越える MESSAGE の差出人は、番号で表した SC です。網 A のホスト名は越えません。配送報告は戻らず、網 A の SC は網 B が受け取った時点で役目を終えます。TP-ST 1 は、転送はしたが配送を確かめられないという意味です（TS 23.040 9.2.3.15）。
- 網 B では、着信側のフィルタ条件がその利用者の IP-SM-GW に MESSAGE を渡し（TS 23.204 6.4a step 2）、IP-SM-GW が RP-OA に網 A の SC を残したまま配送します。

> 手順書 [「網をまたぐショートメッセージ」](../../../docs/guide/ja/interconnect.md#網をまたぐショートメッセージ)

---

## 網 B が受け取り、網 A に 202 が戻る

```ims-scene
id: sms-b
nodes: [ue-a1, scscf-a, ipsmgw-a, ibcf-a, ibcf-b, icscf-b, scscf-b, ue-b1, ipsmgw-b]
lead: 配送報告は境界を越えて戻りません。網 A の SC の仕事は、網 B が MESSAGE を受け取った時点で終わります。
steps:
  - id: ifc
    title: 着信側のフィルタ条件が相手網からの MESSAGE を選ぶ
    only: [sms-sb-ip]
    detail: [sms-sb-ip]
    zoom: [scscf-b, ue-b1, ipsmgw-b]
    checkpoint: true
    text: |
      別の IMS 網からのショートメッセージは、利用者を担当する IP-SM-GW に届きます（TS 23.204 6.4a の手順 2）。
      自網の IP-SM-GW が送ったものを除く条件なので、MESSAGE が S-CSCF と IP-SM-GW のあいだを回り続けることはありません。
  - id: accepted
    title: 網 B の IP-SM-GW が 202 を返す
    only: [sms-202-nni, sms-202-ip]
    active: sms-202-ip
    zoom: [ipsmgw-a, ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      202 は網 B の IP-SM-GW から、来た道を戻ります。
      網 A の SC はここで配送を終えたものとして扱います。配送報告は越えてきません。
  - id: report
    title: 網 A の status report は TP-ST 1 になる
    show: [sms-report, sms-report-ue]
    active: sms-report
    detail: [sms-report]
    checkpoint: true
    text: |
      TP-ST 1 は、転送はしたが配送を確かめられないという意味です（TS 23.040 9.2.3.15）。
      status report は SMS の回と同じように MESSAGE で UE A1 に届きます。
  - id: b-deliver
    title: 網 B の IP-SM-GW が自分の MESSAGE で届ける
    only: [sms-deliver-i, sms-deliver-sb]
    active: sms-deliver-i
    checkpoint: true
    text: |
      網 B の中のショートメッセージと同じ道です。I-CSCF が利用者の S-CSCF を探し、S-CSCF が UE B1 の経路に沿って送ります。
      本文は SMS-DELIVER です。
  - id: b-ue
    title: UE B1 に SMS-DELIVER が届く
    only: [sms-deliver-ue]
    detail: [sms-deliver-ue]
    checkpoint: true
    text: RP-OA には網 A の SC が残り、UE B1 の画面に出る差出人は UE A1 の番号です。
```

- 網 B の IP-SM-GW は 202 を返します。配送報告は境界を越えて戻ってこないので、網 A の SC の仕事はここで終わります。
- 網 B では、着信側のフィルタ条件がその利用者の IP-SM-GW に MESSAGE を渡し、IP-SM-GW が RP-OA に網 A の SC を残したまま配送します。
- 逆の向き、UE B1 から UE A1 へのショートメッセージも、網を入れ替えた同じ手順で届きます。網 B の SC が ENUM で網 A のドメインを見つけ、IBCF-B から出ていきます。

> 手順書 [「網をまたぐショートメッセージ」](../../../docs/guide/ja/interconnect.md#網をまたぐショートメッセージ)と [「逆の向き（網 B から網 A へ）」](../../../docs/guide/ja/interconnect.md#逆の向き網-b-から網-a-へ)

---

## 境界を越える会議

```ims-scene
id: conference
nodes: [mmtel-a, scscf-a, ibcf-a, ibcf-b, icscf-b, scscf-b, ue-b1]
lead: 置き換えない Contact もあります。会議の URI は参加者にそのまま届かなければならないからです。
steps:
  - id: invite
    title: フォーカスが相手網の利用者を招く
    show: [conf-inv-m-s, conf-inv-s-ia]
    active: conf-inv-s-ia
    detail: [conf-inv-m-s]
    zoom: [mmtel-a, scscf-a, ibcf-a]
    checkpoint: true
    text: |
      フォーカスは、自分が受け持つ PSI である会議の URI の代わりに、`orig` を付けて自網の S-CSCF へ INVITE を送ります。
      ENUM で番号が網 B の SIP URI になるので、この INVITE は IBCF-A から出ていきます。
  - id: contact
    title: isfocus の付いた Contact は届いたまま渡る
    show: [conf-inv-nni]
    detail: [conf-inv-nni]
    checkpoint: true
    text: |
      TS 24.229 5.10.5 は GRUU の Contact と、その URI で相手に何ができるかを示す feature tag の付いた Contact を、届いたまま通すとしています。
      挙げている例が会議のフォーカスを示す `isfocus` です。
  - id: record-route
    title: Contact を通す IBCF は Record-Route に自分を入れる
    show: [conf-inv-ib-i]
    detail: [conf-inv-ib-i]
    focus: [ibcf-b, icscf-b]
    checkpoint: true
    text: |
      会議の URI に宛てた要求が、やはり自分に届くようにするためです。IBCF-B は網 B の中で自分の内側の名前を入れます。
      INVITE はこのあと、ほかの通話と同じように網 B の中を進んで UE B1 に届きます。
  - id: subscribe
    title: ダイアログの外の SUBSCRIBE は越えない
    only: [conf-sub, ibcfb-sub]
    focus: [ibcf-b]
    zoom: [ibcf-a, ibcf-b, scscf-b, ue-b1]
    text: |
      UE B1 は控えた会議の URI に SUBSCRIBE を送ります（TS 24.147 5.3.1.2）。
      IBCF-B はこれを境界の向こうへ渡しません。
  - id: forbidden
    title: IBCF-B が自分で 403 を返す
    show: [conf-403]
    detail: [ibcfb-sub]
    zoom: [ibcf-a, ibcf-b, scscf-b, ue-b1]
    checkpoint: true
    text: |
      403 を受けた参加者は会議のセッションのダイアログの中で購読し直します。
      そちらは、取り決めが SUBSCRIBE と NOTIFY を認めていれば、呼のほかの要求と同じように越えます。
```

- 置き換えない Contact もあります。TS 24.229 5.10.5 は、GRUU の Contact と、その URI で相手に何ができるかを示す feature tag の付いた Contact を、届いたまま通すとしています。挙げている例は、会議のフォーカスを示す `isfocus` です。
- その Contact の URI は会議の URI です。参加者はこれを控えて購読し、会議に戻るときにも使います（TS 24.147 5.3.1.3.2）。そのため、参加者にはそのまま届く必要があります。
- IBCF は、この Contact をそのまま通すとき、Record-Route に自分の URI を入れます。会議の URI に宛てた要求が、やはり自分に届くようにするためです。網のあいだでは、これだけが Record-Route です。
- ダイアログの外の SUBSCRIBE は越えません。IBCF が 403 で答え、参加者は会議のセッションのダイアログの中で購読し直します。TS 24.147 5.3.1.2 が、403 のあとにそうするとしています。
- このプロジェクトのフォーカスは、会議の状態の `<endpoint>` を `urn:uuid:` の URI で表します。そのため NOTIFY からは、網 A のアドレスもノードの名前も網 B に見えません。

> 手順書 [「境界を越える会議」](../../../docs/guide/ja/interconnect.md#境界を越える会議)。スクリプトのシナリオにはありません。`internal/ibcf` の試験が 1 つのプロセスで動かします。

---

## IBCF が起動し直したとき

- 呼の 2 つのダイアログは、その INVITE を受けた台のプロセスの中にあります。そのプロセスが止まると呼も止まりますが、両側はそれを知りません。どちらも自分のダイアログを持ったままで、TrGW もコンテキストを持ったままです。
- TS 24.229 5.10.2.2 step 2 と 5.10.3.2 step 2 は、転送部を制御する IBCF が、自分でセッションを解放できるように、呼の Contact、CSeq、Record-Route を保存するとしています。
- 各台は、自分の呼の記録を自分の名前の下に置きます。起動し直した台は自分の記録だけを読み、TrGW のコンテキストを解放して、2 つのダイアログそれぞれに BYE を送ります。網全体の呼を探し回る台はありません。
- 設定に kind が `valkey` の `store` があるノードは、この記録をそのサーバーに置きます。コンテナを作り直しても、そこから読み出せます。
- この 2 つの網の IBCF に `store` はありません。通話中に IBCF-A を作り直すと、その呼を終わらせるものがありません。通話の一方からの BYE には新しいプロセスが 481 を返し、TrGW-A はその呼のコンテキストを確保したままになります。

> 手順書 [「IBCF の台が起動し直したとき」](../../../docs/guide/ja/interconnect.md#ibcf-の台が起動し直したとき)

---

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Method == "INVITE" && count(sip.Via) == 1` | 網のあいだの IBCF の INVITE。Via は自分の 1 つだけです。 |
| `sip.Route contains "ibcf"` | IBCF へ向かう要求と、IBCF から出た要求。 |
| `sip.r-uri contains "user=phone"` | 番号を SIP URI で書いた要求。 |
| `sip.r-uri == "tel:+12025550151"` | 網 B の I-CSCF が tel URI に直したあとの INVITE。 |
| `sdp.connection_info.address == 172.28.2.11` | 網のあいだの TrGW-A のアドレスが入った SDP。 |
| `rtp && ip.addr == 172.28.2.0/24` | TrGW どうしの RTP。RTP のヒューリスティックを有効にして使います。 |
| `dns.qry.name contains "5.1.0.5.5.5.2.0.2.1.e164.arpa"` | 網 B の番号の ENUM。 |
| `sip.Status-Code in {404, 405, 420, 488}` | シナリオで断られたときの応答。 |

- 手順書にはこのほかに 5 つあります。復号したショートメッセージや、ほかの 2 つのキャプチャで網 A の内側のものを探すフィルタなどです。後者は、1 つも見つからないのが正しい結果です。
- フィルタは、3 つのキャプチャのどれか 1 つに当てます。スクリプトは、キャプチャごとにシナリオの順でフレームを確かめ、あってはならないフレームも確かめます。

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/interconnect.md#wireshark-の表示フィルタ)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| IBCF | ローミングなしの II-NNI だけ。REGISTER は越えず（405）、Path も `iotl` も無い。ダイアログの外の要求は INVITE と MESSAGE だけが越える。課金の情報も、IBCF 自身のセッションタイマーも無い。 |
| 境界 | トランスコードをしない。相手網が受けないコーデックは外すだけ。IPv4 だけで、TrGW を通るのは最初の音声ストリームだけ。 |
| Ix | IBCF は TrGW を、H.248 ではなくメディア中継ノードの HTTP の制御 API で動かす。 |
| SMS | SC は相手網の利用者を、SMS-GMSC で相手網に尋ねるのではなく ENUM で見つける。配送報告は境界を越えて戻らない。 |

```sh
grep -rn "SIMPLIFIED:" internal/ibcf internal/b2bua internal/interconnect internal/sc internal/ipsmgw
```

- トランスコードをしないのはなぜか。TrGW にコーデックが要り、このプロジェクトのノードは AMR を符号化も復号もしないからです。

> 手順書 [「このプロジェクトで簡略化したこと」](../../../docs/guide/ja/interconnect.md#このプロジェクトで簡略化したこと)。項目ごとの条項と理由は手順書の表にあります。

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| プロファイルと、IBCF がメッセージごとに行うこと | `internal/interconnect` の `screen.go`、`trust.go`、`number.go`、`codec.go`、`leg.go`、`border.go` |
| 呼の 2 つのダイアログのあいだの中継 | `internal/b2bua` |
| 役割 `ibcf`。2 つの UA、呼、ショートメッセージ、TrGW | `internal/ibcf` |
| 網から出ていく要求 | `internal/scscf/session.go` の `exitIBCF` |
| SIP URI の番号が tel URI になるところ | `internal/icscf/session.go` の `telURIOf` |
| 網をまたぐショートメッセージ | `internal/sc/sc.go`、`internal/ipsmgw` の `deliver.go` と `message.go` |
| 2 つの網、そのノード、取り決め | `deploy/interconnect/` |

- `internal/ibcf` の試験は、2 つの網を 1 つのプロセスで動かします。境界の向こうでの分岐、境界を越える会議、起動し直して残した呼を終える台も含みます。

> 手順書 [「コードのある場所」](../../../docs/guide/ja/interconnect.md#コードのある場所)

---

[layout: exercise]

## 演習

1. 3 つのキャプチャそれぞれで通話の INVITE を探します。網 A のキャプチャでは、UE A1 から P-CSCF への INVITE も探します。この通話は全部でいくつの Call-ID を持ち、それはなぜでしょうか。
2. 網のあいだの INVITE を開きます。Via はいくつ付いていて、ほかの Via はどこにあるでしょうか。
3. IBCF-B に取り決め `network-a-amr-wb-only.yaml` を与えて UE B1 にかけます。どの応答が返り、送ったのはどのノードでしょうか。
4. 網 B のキャプチャで 172.28.0.0/24 のアドレスを探します。どういう結果になるのが正しいでしょうか。
5. どのプロファイルにも無いアドレスから IBCF-A に要求を送ります。何が返るでしょうか。

3 のコマンドは、手順書の「プロファイルが食い違うとき」にあります。5 では、スクリプトが 172.28.2.99 から INVITE と MESSAGE を送っています。

---

[layout: answers]

## 演習の答え

1. 4 つです。ダイアログごとに 1 つあります。UE A1 と MMTel AS、MMTel AS と IBCF-A、IBCF-A と IBCF-B、IBCF-B と UE B1 のあいだです。MMTel AS と各 IBCF は B2BUA で、届いた側で要求を受け止め、もう一方の側で新しい要求を送るからです。キャプチャが並べて見せるのは後ろの 3 つで、網 B にはこの通話の AS がありません。
2. IBCF-A の 1 つだけです。網 A のノードの Via は、IBCF-A で終わった INVITE に付いています。
3. IBCF-B からの 488（Not Acceptable Here）です。網 B は AMR-WB しか受けず、UE A1 は PCMU を offer するからです。
4. 1 つも見つからないのが正しい結果です。網 A の内側のアドレスは境界を越えません。
5. 何も返りません。100（Trying）もエラー応答もありません。IBCF-A が要求を捨てるので、送信元には網 A のことが何も分かりません。

---

[layout: exercise]

## 振り返り

1. 自網から届いた要求を、IBCF はどう扱いますか。
2. 境界を越える 1 つの通話に Call-ID が 4 つあるのはなぜですか。
3. II-NNI を作る 2 つの参照点はどれですか。
4. MESSAGE が境界を越えてよいかを決めるのは何ですか。
5. 発信した UE に相手網のアドレスが見えないのはなぜですか。
6. プロセスが止まった IBCF の台の呼は、どうなりますか。

---

[layout: answers]

## 振り返りの答え

1. 自網の出口として受け止め、相手網へ向けて自分のダイアログで新しい要求を送ります。
2. ダイアログごとに 1 つだからです。発信側と MMTel AS、MMTel AS と IBCF-A、網のあいだ、IBCF-B と着信側の 4 つで、B2BUA はそれぞれ一方を終えて他方を始めます。
3. SIP を運ぶ IBCF どうしの Ici と、メディアを運ぶ TrGW どうしの Izi です。
4. 相手網との取り決めです。MESSAGE は任意のメソッドで、プロファイルの `methods` が認めるかどうかを決めます。
5. IBCF がメディアを自網の TrGW に通し、それぞれの側の SDP に、その網での TrGW のアドレスを書くからです。
6. 呼はプロセスと一緒に止まり、両側はダイアログを持ったままになります。記録を持った台が起動し直すと、2 つのダイアログそれぞれに BYE を送って終わらせます。

---

## この先に読むもの

- ここが最後の回です。手順書は回の先まで続き、どの手順書も、簡略化したことと、コードのある場所で終わります。

| 文書 | 場所 |
| --- | --- |
| プロジェクトの説明と動かし方 | [README.ja.md](../../../README.ja.md) |
| 用語集 | [docs/glossary.md](../../../docs/glossary.md) |
| 手順書 | [docs/guide/ja/](../../../docs/guide/ja/) |

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml --profile ue down -v --remove-orphans
```

- 手順書の「後片付け」です。両方の網の UE と、両方の網と網のあいだのノードを止めます。

> 手順書 [「後片付け」](../../../docs/guide/ja/interconnect.md#後片付け)
