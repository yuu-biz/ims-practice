---
title: この IMS の地図
footer: ims-practice 2. 網の地図
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: ue, label: UE, role: 電話, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: 最初の中継点, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: 網の入口, at: [2, 1]}
  - {id: scscf, label: S-CSCF, role: 登録とサービス, at: [3, 1]}
  - {id: hss, label: HSS, role: 加入者のデータ, at: [3, 0]}
  - {id: ipsmgw, label: IP-SM-GW, role: SMS の AS, at: [4, 0]}
  - {id: sc, label: SC, role: SMS のサービスセンタ, at: [4, -1]}
  - {id: mmtel, label: MMTel AS, role: 付加サービスの AS, at: [4, 2]}
  - {id: mrfc, label: MRFC, role: MRF の SIP の側, at: [5, 2]}
  - {id: mrfp, label: MRFP, role: MRF のメディアの側, at: [5, 3]}
  - {id: agw, label: IMS-AGW, role: メディア中継ノード, at: [1, 2]}
  - {id: epc, label: EPC, role: スタブと PCRF, at: [0, 2]}
  - {id: ibcf, label: IBCF, role: 網の出入口, at: [3, 2]}
  - {id: trgw, label: TrGW, role: 境界のメディア中継, at: [2, 2]}
  - {id: peer-ibcf, label: IBCF, role: 相手の網の出入口, at: [3, 3]}
  - {id: peer-trgw, label: TrGW, role: 相手の網のメディア中継, at: [2, 3]}
groups:
  - {id: peer, label: 相手の網, nodes: [peer-ibcf, peer-trgw]}
events:
  # SIP を運ぶ参照点
  - {id: gm, rel: Gm, from: ue, to: pcscf, proto: SIP}
  - {id: mw-p-i, rel: Mw, from: pcscf, to: icscf, proto: SIP}
  - {id: mw-i-s, rel: Mw, from: icscf, to: scscf, proto: SIP}
  - {id: isc-mmtel, rel: ISC, from: scscf, to: mmtel, proto: SIP}
  - {id: isc-ipsmgw, rel: ISC, from: scscf, to: ipsmgw, proto: SIP}
  - {id: mr, rel: Mr', from: mmtel, to: mrfc, proto: SIP}
  - {id: mx, rel: Mx, from: scscf, to: ibcf, proto: SIP}
  - {id: ici, rel: Ici, from: ibcf, to: peer-ibcf, proto: SIP}
  # Diameter を運ぶ参照点
  - {id: cx-i, rel: Cx, from: icscf, to: hss, proto: Diameter}
  - {id: cx-s, rel: Cx, from: scscf, to: hss, proto: Diameter}
  - {id: sh-ipsmgw, rel: Sh, from: ipsmgw, to: hss, proto: Diameter}
  - {id: sh-mmtel, rel: Sh, from: mmtel, to: hss, proto: Diameter}
  - {id: sgd, rel: SGd, from: ipsmgw, to: sc, proto: Diameter}
  - {id: s6c, rel: S6c, from: sc, to: hss, proto: Diameter}
  - {id: rx, rel: Rx, from: pcscf, to: epc, proto: Diameter}
  # HTTP、H.248、メディア
  - {id: ut, rel: Ut, from: ue, to: mmtel, proto: HTTP}
  - {id: mp, rel: Mp, from: mrfc, to: mrfp, proto: HTTP}
  - {id: iq, rel: Iq, from: pcscf, to: agw, proto: HTTP}
  - {id: izi, rel: Izi, from: trgw, to: peer-trgw, proto: RTP}
```

---

# この IMS の地図

（ノード、ノードの間の参照点、地図のどこをどの章が開くか）

---

[layout: summary]

## このデッキで分かること

- IMS がどのノードでできていて、それぞれが何のためにあるか。
- どのインタフェースが、どのノードとどのノードをつなぎ、何のプロトコルを運ぶか。
- SIP はどこまでで、Diameter、HTTP、H.248、RTP はどこから始まるか。
- 地図のどの部分を、本のどの章とどの手順書が開くか。

> このデッキはキャプチャを読みません。ほかのデッキと本の章がたどる地図を描きます。
> 本 [IMS が受け持つ仕事](../../../docs/book/ja/overview.md)
> 手順書は [docs/guide/ja/](../../../docs/guide/ja/) に、略語は [用語集](../../../docs/glossary.md) にあります。

---

## IMS が SIP のプロキシに足したもの

```ims-scene
id: jobs
nodes: [ue, pcscf, icscf, scscf, hss, ipsmgw, sc, mmtel, mrfc, mrfp, agw, epc]
lead: IMS は SIP のプロキシの仕事を、役目が 1 つずつのノードに分けました。
steps:
  - id: pcscf
    title: 電話から見て最初の SIP の中継点
    focus: [pcscf]
    zoom: [ue, pcscf, icscf]
    text: P-CSCF です。UE の要求はどれもここから入ります。
  - id: icscf
    title: この利用者を担当するノードを探す
    focus: [icscf]
    zoom: [icscf, scscf, hss]
    text: I-CSCF です。自分では状態を持たず、HSS に尋ねます。
  - id: scscf
    title: 登録を保持し、サービスを呼び出す
    focus: [scscf]
    zoom: [icscf, scscf, hss]
    text: S-CSCF です。認証し、登録を保持し、iFC を評価します。
  - id: hss
    title: 加入者のデータを持つ
    focus: [hss]
    zoom: [scscf, hss, ipsmgw]
    text: HSS です。識別子、鍵、iFC、サービスの設定を持ちます。
  - id: as
    title: 通話を変える
    focus: [mmtel, ipsmgw]
    zoom: [scscf, mmtel, ipsmgw]
    checkpoint: true
    text: アプリケーションサーバーです。MMTel AS は転送や規制を、IP-SM-GW はショートメッセージを受け持ちます。
  - id: media
    title: メディアを中継し、流し、混ぜる
    focus: [agw, mrfc, mrfp]
    text: |
      メディア中継ノード（IMS-AGW）と MRF です。
      役目ごとにノードが分かれているので、役目ごとに必要なだけ台数を増やせます。
  - id: core
    title: 登録と網の中の通話にかかわる 4 つ
    focus: [pcscf, icscf, scscf, hss]
    text: |
      登録と、この網の中の通話には、P-CSCF、I-CSCF、S-CSCF、HSS がかかわります。
      ほかのノードは、サービスやアクセスや網の境界に応じて加わります。
```


- 2 台の電話をつなぐだけなら、SIP のプロキシでもできます。IMS はその仕事を、役目が 1 つずつのノードに分け、2 つのノードの間のインタフェースに名前を付けました（TS 23.228）。
- 名前が付いていることが大事です。作る会社の違うノードどうしも、事業者の違う網どうしも、名前の付いたインタフェースで出会います。
- 仕事を分けたので、役目ごとに必要なだけ台数を増やせます。キャプチャを見れば、どのノードが何をしたかも分かります。

| 仕事 | ノード |
| --- | --- |
| 電話から見て最初の SIP の中継点になる | P-CSCF |
| この利用者を担当するノードを探す | I-CSCF |
| 登録を保持し、サービスを呼び出す | S-CSCF |
| 加入者のデータを持つ | HSS |
| 通話を変える。転送、規制、ショートメッセージ | アプリケーションサーバー |
| メディアを中継し、流し、混ぜる | メディア中継ノード、MRF |

> 手順書 [手順書の入口「構成全体の作り」](../../../docs/guide/ja/README.md#構成全体の作り)

---

## 信号の通り道

```ims-scene
id: backbone
nodes: [ue, pcscf, icscf, scscf, mmtel, ipsmgw]
lead: UE の要求は P-CSCF から入り、Mw で CSCF を渡り、ISC で AS に届きます。どれも SIP です。
steps:
  - id: gm
    title: 電話と網が出会う Gm
    show: [gm]
    zoom: [ue, pcscf]
    text: 電話が出会うインタフェースは Gm と、自分の設定のための Ut の 2 つです。
  - id: mw
    title: CSCF どうしの Mw
    show: [mw-p-i, mw-i-s]
    zoom: [pcscf, icscf, scscf]
    text: 中継点はどれも SIP を 5060 番で運ぶので、1 つのメッセージはどの中継点でもだいたい同じに見えます。
  - id: isc
    title: AS へ向かう ISC
    show: [isc-mmtel, isc-ipsmgw]
    focus: [scscf, mmtel, ipsmgw]
    text: |
      AS は経路の途中にいて、終点ではありません。MMTel AS は要求を S-CSCF に返します。
      IP-SM-GW は返さずに応答します。ショートメッセージはそこで終わるからです。
```


```text
  UE --- Gm --- P-CSCF --- Mw --- I-CSCF --- Mw --- S-CSCF --- ISC --- AS
```

- UE の要求はどれも P-CSCF から入ります。その先の中継点も SIP を 5060 番で運ぶので、1 つのメッセージはどの中継点でもだいたい同じに見えます。
- たとえば REGISTER は、Gm で P-CSCF に入り、Mw で I-CSCF と S-CSCF に進みます。その途中で、I-CSCF と S-CSCF が Cx で HSS に問い合わせます。
- 電話が出会うインタフェースは 2 つです。Gm と、自分の設定のための Ut です。P-CSCF から先は事業者の側の話です。
- AS は経路の途中にいて、終点ではありません。MMTel AS は要求を S-CSCF に返し、S-CSCF は次の条件に進みます。IP-SM-GW は返さずに応答します。ショートメッセージはそこで終わるからです。

> 手順書 [UE が IMS に登録する流れを追う「登場するノード」](../../../docs/guide/ja/registration.md#登場するノード)

---

## UE の近くのノード

| ノード | アドレス | 何のためにあるか |
| --- | --- | --- |
| UE | 172.28.0.100、101、103 | 電話。SIM の無いソフトフォンか、EPC スタブにつながる VoLTE の UE。 |
| P-CSCF | 172.28.0.40 | 最初の SIP の中継点。Path を足し、識別子を保証し、UE ごとに IP の対応付けを持つ。 |
| メディア中継ノード | 172.28.0.50 | すべての通話の RTP を中継する。IMS-AGW の役割で、P-CSCF が制御する。 |
| EPC スタブと PCRF | 172.28.0.80 | VoLTE の UE にアドレスと P-CSCF を渡し、通話ごとにメディアを許可する。 |

- UE が知っていなければならないアドレスは、P-CSCF だけです。ソフトフォンは設定で持ち、VoLTE の UE は接続のときにアクセス網から受け取ります。
- P-CSCF は、担当する UE が送るものと受け取るものすべての経路に残ります。登録も含みます。着信のときにそこに残るのは、REGISTER に足した Path のおかげです。

> 手順書 [VoLTE の呼の流れ「アクセス網（EPC スタブ）」](../../../docs/guide/ja/volte.md#アクセス網epc-スタブ)

---

## ホーム網のノード

| ノード | アドレス | 何のためにあるか |
| --- | --- | --- |
| I-CSCF | 172.28.0.20 | 網の入口。どの S-CSCF が担当かを HSS に聞く。自分では状態を持たない。 |
| S-CSCF | 172.28.0.30 | レジストラ。認証し、登録を保持し、iFC を評価する。 |
| HSS | 172.28.0.10 | 加入者のデータ。識別子、鍵、iFC、サービスの設定。 |
| DNS | 172.28.0.2 | ノードを探す NAPTR、SRV、A（RFC 3263）と、番号を SIP URI にする ENUM（RFC 6116）。 |

- S-CSCF と HSS は、利用者自身の網のノードです。訪問先の網に置けるのは P-CSCF で、だから REGISTER は P-Visited-Network-ID を運びます。
- アドレスの末尾の数字が、キャプチャの中でノードを教えます。HSS が 10、I-CSCF が 20、S-CSCF が 30、P-CSCF が 40 です。

> 手順書 [信号をキャプチャして Wireshark で開く「ノードを起動すると見えるもの」](../../../docs/guide/ja/capture.md#ノードを起動すると見えるもの)

---

## アプリケーションサーバー、SC、MRF

| ノード | アドレス | 何のためにあるか |
| --- | --- | --- |
| IP-SM-GW | 172.28.0.70 | SMS over IP。片側で SIP を、もう片側で SMS の網の言葉を話す。 |
| SC | 172.28.0.71 | SMS のサービスセンタ。ショートメッセージを、配達できるか有効期間が切れるまで預かり、配達する。 |
| MMTel AS | 172.28.0.75 | 付加サービスと、UE が Ut で自分で行う設定。 |
| MRFC | 172.28.0.85 | MRF の SIP の側。AS が音声ガイダンス、トーン、会議をここに頼む。 |
| MRFP | 172.28.0.86 | MRF のメディアの側。音声を流し、混ぜる。MRFC が操作する。 |

- CSCF は経路を決めるだけです。通話を変えるのはアプリケーションサーバー、自分でメディアを作るのは MRF です（TS 23.228 4.7）。
- SC は IMS ではなく SMS の網のノードです。IP-SM-GW は Diameter で SC とつながります。

> 手順書 [SMS の流れを追う「AS が要る理由と SC の役割」](../../../docs/guide/ja/sms.md#as-が要る理由と-sc-の役割)と [MRF による音声ガイダンス、呼び出し音、会議「MRF とは」](../../../docs/guide/ja/mrf.md#mrf-とは)

---

## 網の境界と、網を外から見る道具

| ノード | アドレス | 何のためにあるか |
| --- | --- | --- |
| IBCF | 172.28.0.90 | 網から出る要求の出口、入る要求の入口。網の構成を隠す。 |
| TrGW | 172.28.0.91 | 境界のメディア中継ノード。IBCF が制御する。 |
| ラボ | 172.28.0.60 | ノードが送る複製（HEP）から、登録と通話をシーケンス図にする。IMS の一部ではない。 |
| OAM のサーバー | 172.28.0.65 | 各ノードを保守の API で操作し、OAM の画面を出す。IMS の一部ではなく、信号を送らない。 |
| capture | ホストのネットワーク | ブリッジ上の tcpdump。どのデッキでも読むキャプチャのファイルを書く。 |

- IBCF と TrGW は、2 つめの網と一緒に現れます。`deploy/interconnect/compose.yaml` が `compose.yaml` の上に足します。網間接続のデッキは、この境界を両方の網と網のあいだのキャプチャで読みます。
- ラボ、OAM のサーバー、キャプチャは IMS の外です。どの通話も、これらの応答を待ちません。ラボへの複製は UDP で送りっぱなしで、tcpdump は聞いているだけです。

> 手順書 [IMS 網どうしを相互接続する「2 つの網」](../../../docs/guide/ja/interconnect.md#2-つの網)と [ラボで IMS を見る「このプロジェクト独自の API」](../../../docs/guide/ja/lab.md#このプロジェクト独自の-api)

---

## SIP を運ぶ参照点

```ims-scene
id: sip-points
nodes: [ue, pcscf, icscf, scscf, mmtel, ipsmgw, mrfc, ibcf, peer-ibcf]
lead: どれも SIP を平文で運びます。どの参照点かは、キャプチャのアドレスで分かります。
steps:
  - id: core
    title: Gm と Mw
    show: [gm, mw-p-i, mw-i-s]
    zoom: [ue, pcscf, icscf, scscf]
    text: P-CSCF は UE の側に Gm を、網の側に Mw を持ちます。ここではどちらも同じアドレスとポートです。
  - id: services
    title: ISC と Mr'
    show: [isc-mmtel, isc-ipsmgw, mr]
    focus: [scscf, mmtel, ipsmgw, mrfc]
    zoom: [scscf, mmtel, ipsmgw, mrfc]
    text: ISC で第三者 REGISTER と iFC が送る INVITE が、Mr' で音声ガイダンスや会議を頼む INVITE が流れます。
  - id: border
    title: 網の外へ向かう Mx と Ici
    show: [mx, ici]
    focus: [scscf, ibcf, peer-ibcf]
    text: Mx で網から出ていく要求が IBCF に届き、Ici で 2 つの網の IBCF がつながります。
```


| 参照点 | どことどこの間か | どこで見えるか |
| --- | --- | --- |
| Gm | UE と P-CSCF | UE の REGISTER と INVITE のすべて |
| Mw | CSCF どうし | 同じ要求の、1 つ先の中継点 |
| ISC | S-CSCF と AS | 第三者 REGISTER と、iFC が送る INVITE |
| Mr' | AS と MRFC | 音声ガイダンス、トーン、会議を頼む INVITE |
| Mx | 同じ網の CSCF と IBCF | 網から出ていく要求 |
| Ici | 2 つの網の IBCF | 網の間のリンクのキャプチャ |

- どれも SIP を平文で運びます。Wireshark はプロトコルでは区別できません。どの参照点かは、アドレスで分かります。
- P-CSCF は UE の側に Gm を、網の側に Mw を持ちます。ここではどちらも同じアドレスとポートで、要求のあて先の URI で区別します。

> 手順書 [UE が IMS に登録する流れを追う「このプロジェクトで簡略化していること」](../../../docs/guide/ja/registration.md#このプロジェクトで簡略化していること)

---

## Diameter を運ぶ参照点

```ims-scene
id: diameter-points
nodes: [pcscf, epc, icscf, scscf, hss, ipsmgw, sc, mmtel]
lead: SIP は要求を先へ運び、Diameter は問いを出して答えを待ちます。Diameter の線の多くは HSS に集まります。
steps:
  - id: cx
    title: CSCF と HSS の Cx
    show: [cx-i, cx-s]
    zoom: [icscf, scscf, hss]
    text: どの S-CSCF が担当か、認証の情報、ユーザープロファイルを求めます。
  - id: sh
    title: AS と HSS の Sh
    show: [sh-ipsmgw, sh-mmtel]
    focus: [ipsmgw, mmtel, hss]
    zoom: [hss, ipsmgw, mmtel, scscf]
    text: 利用者の設定、その番号、Ut で使うパスワードを読み書きします。
  - id: sms
    title: SMS の SGd と S6c
    show: [sgd, s6c]
    focus: [ipsmgw, sc, hss]
    checkpoint: true
    text: SGd はショートメッセージそのものを、S6c は宛先を担当するノードと、メッセージを抱える SC を運びます。
  - id: rx
    title: P-CSCF と PCRF の Rx
    show: [rx]
    focus: [pcscf, epc]
    zoom: [pcscf, epc, icscf]
    text: 通話のメディアの許可と、そのメディアを失ったときの知らせです。接続は 7 本、どれも 3868 番です。
```


```text
  I-CSCF   --- Cx  ---> HSS <--- Cx  --- S-CSCF
  IP-SM-GW --- Sh  ---> HSS <--- Sh  --- MMTel AS
  IP-SM-GW --- SGd ---> SC  --- S6c ---> HSS
  P-CSCF   --- Rx  ---> PCRF (EPC stub)
```

| 参照点 | どことどこの間か | 何を求めるか |
| --- | --- | --- |
| Cx | CSCF と HSS | どの S-CSCF が担当か、認証の情報、ユーザープロファイル |
| Sh | AS と HSS | 利用者の設定、その番号、Ut で使うパスワード |
| SGd | IP-SM-GW と SC | ショートメッセージそのもの。UE から送られたものと UE 宛のもの |
| S6c | SC と HSS | 宛先をどのノードが担当しているか。また受け取れるようになった利用者宛のショートメッセージを、どの SC が抱えているか |
| Rx | P-CSCF と PCRF | 通話のメディアの許可と、そのメディアを失ったときの知らせ |

- 接続は 7 本、どれも 3868 番で、最初の問い合わせの前に CER と CEA で開きます。キャプチャの手順書に一覧があります。
- SIP は要求を先へ運び、Diameter は問いを出して答えを待ちます。2 つの分かれ目はそこです。

> 手順書 [信号をキャプチャして Wireshark で開く「ノードを起動すると見えるもの」](../../../docs/guide/ja/capture.md#ノードを起動すると見えるもの)

---

## HTTP、H.248、メディアの参照点

```ims-scene
id: other-points
nodes: [ue, pcscf, agw, mmtel, mrfc, mrfp, trgw, peer-trgw]
lead: メディアは信号の経路をたどりません。RTP の行き先は双方の SDP が決めます。
steps:
  - id: ut
    title: 利用者が自分の設定を読み書きする Ut
    show: [ut]
    text: XCAP を載せた HTTP です（TS 24.623）。
  - id: mp
    title: MRFC が MRFP を操作する Mp
    show: [mp]
    focus: [mrfc, mrfp]
    zoom: [mmtel, mrfc, mrfp]
    text: 標準では H.248 です。ここでは命令 1 つに操作 1 つの簡単な HTTP の API です。
  - id: iq
    title: P-CSCF が中継ノードを操作する Iq
    show: [iq]
    focus: [pcscf, agw]
    zoom: [ue, pcscf, agw]
    checkpoint: true
    text: 既定では同じ HTTP の API です。`deploy/h248/h248.yaml` を使うと H.248 になります。
  - id: izi
    title: 境界を越えるメディアの Izi
    show: [izi]
    focus: [trgw, peer-trgw]
    text: 2 つの網の TrGW のあいだを、RTP が通ります。
```


| 参照点 | どことどこの間か | プロトコル |
| --- | --- | --- |
| Ut | UE と MMTel AS | XCAP を載せた HTTP。利用者が自分の設定を読み書きする（TS 24.623） |
| Mp | MRFC と MRFP | 標準では H.248（TS 29.333）。ここでは命令 1 つに操作 1 つの簡単な HTTP の API |
| Iq | P-CSCF とメディア中継ノード | 既定では同じ HTTP の API。`deploy/h248/h248.yaml` を使うと H.248（TS 29.334） |
| Izi | 2 つの網の TrGW | 境界を越えるメディア（TS 29.165 5.1） |

- メディアは信号の経路をたどりません。RTP の行き先は双方の SDP が決め、その経路にいるのは中継ノードと MRFP だけです。
- VoLTE の通話では RTCP も流れます。電話が鳴る前に、P-CSCF が Rx でそのメディアのための bearer を求めます。

> 手順書 [手順書の入口「メディア中継ノードを H.248 で制御する」](../../../docs/guide/ja/README.md#メディア中継ノードを-h248-で制御する)

---

## この地図に無いもの

| 無いもの | 何のためのものか |
| --- | --- |
| 電話網 | ENUM のレコードが無い番号にはつながりません。大きな網なら BGCF へ出すところを、S-CSCF が 404 で答えます。 |
| Gx | bearer 側のゲートウェイと PCRF が EPC スタブの中で状態を共有するので、その間に見るものがありません。 |
| Mr | S-CSCF が自分で MRFC に頼む道です。ここではメディアの依頼はすべて AS から Mr' で出ます。 |
| Cr | AS が音声やメディア制御の命令を取りにいく道です。MRFP は手元にあるファイルを流します。 |
| GBA | AKA から Ut 用の鍵を作るしくみです。UE は、AS が Sh で読むパスワードを使います。 |

- どの手順書にも最後に「簡略化したこと」があり、その流れで何を省いたかと、その理由が書いてあります。

---

## 地図のどこをどこで読むか

| 地図の部分 | どこで読むか |
| --- | --- |
| Gm、Mw、Cx と、CSCF および HSS | 登録のデッキ。本の「登録で決まる 3 つのこと」 |
| ENUM、メディア中継ノード、1 つの通話の両側 | 通話のデッキ。本の「番号から相手の電話へ」 |
| ISC、Sh、SGd、S6c と、IP-SM-GW および SC | SMS のデッキ。本の「SMS の配達と報告」 |
| Ut と MMTel AS | 付加サービスのデッキ。本の「iFC と MMTel AS の付加サービス」 |
| Rx、EPC スタブ、IMS-AKA | VoLTE のデッキ。本の「VoLTE の登録と通話」 |
| Mr'、Mp、MRF | MRF のデッキ。本の「MRF の音声ガイダンス、呼び出し音、会議」 |
| Mx、Ici、Izi、境界 | 網間接続のデッキ。本の「網間接続」 |
| HEP とラボ | キャプチャとラボのデッキ。本の「信号を観測する」 |

- 本の入門の章（[目次](../../../docs/book/ja/README.md)）は、この地図の全体を、登録と通話の 1 つずつの例で先に通します。

> 手順書 [手順書の入口「読む順」](../../../docs/guide/ja/README.md#読む順)

---

## 地図はリポジトリのどこにあるか

| 何が | どこに |
| --- | --- |
| ノードとそのアドレス | `compose.yaml` |
| 各ノードが何であるか | `deploy/compose/` のノードのファイルの `roles` |
| どの Diameter の相手につなぐか | 同じファイルの `peers` |
| S-CSCF がどの AS に送るか | `deploy/compose/hss.yaml` のフィルタ条件 |
| 名前、SRV のレコード、ENUM のレコード | `deploy/compose/dns/` |

- イメージは 1 つで、どのノードにもなります。どの役割を取るかはそのノードのファイルが決めるので、地図は設定であって、コードではありません。
- キャプチャの手順書の表に、サービスごとのアドレスがあります。その下の表には Diameter の接続があります。どちらも `compose.yaml` と突き合わせて確かめているので、古くなりません。

> 手順書 [手順書の入口「Compose のプロジェクトが起動するもの」](../../../docs/guide/ja/README.md#compose-のプロジェクトが起動するもの)
