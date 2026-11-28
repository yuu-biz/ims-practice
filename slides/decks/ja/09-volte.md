---
title: VoLTE の呼の流れ
footer: ims-practice 勉強会 9. VoLTE
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: volte10, label: volte10, role: 発信側の端末, at: [0, 0]}
  - {id: epc, label: EPC + PCRF, role: アクセス網のスタブ, at: [1, 2]}
  - {id: volte12, label: volte12, role: 着信側の端末, at: [0, 2]}
  - {id: pcscf, label: P-CSCF, role: 最初の中継点, at: [1, 1]}
  - {id: ue2, label: ue2, role: ソフトフォン, at: [2, 2]}
  - {id: icscf, label: I-CSCF, role: ホーム網の入口, at: [2, 0]}
  - {id: hss, label: HSS, role: 認証ベクトル, at: [3, 0]}
  - {id: scscf, label: S-CSCF, role: 認証と登録, at: [3, 1]}
  - {id: mmtel, label: MMTel AS, role: 通話の AS, at: [4, 1]}
events:
  # attach と P-CSCF の発見
  - id: at-si
    msg: GET /system-information
    from: volte10
    to: epc
    proto: HTTP
    fields:
      - セル: MCC 001、MNC 01、TAC 1、ECI 257
  - id: at-stream
    msg: GET /ues/<IMSI>/events
    from: volte10
    to: epc
    proto: HTTP
    fields:
      - パス: "`/ues/001010000000010/events`"
      - 届くもの: 網が自分から送るメッセージ
  - id: at-req
    msg: ATTACH REQUEST
    from: volte10
    to: epc
    proto: HTTP
    note: (POST .../attach-request)
    fields:
      - ESM のコンテナ: PDN CONNECTIVITY REQUEST
      - access_point_name: "`ims`"
      - PCO: P-CSCF のアドレスを求める
  - id: at-accept
    msg: ATTACH ACCEPT
    from: epc
    to: volte10
    proto: HTTP
    note: (200)
    fields:
      - eps_bearer_identity: 5。信号のための default bearer
      - eps_qos: QCI 5。IMS の信号の種類
      - pdn_address: 172.28.0.120。加入者の静的なアドレス
      - PCO: 識別子 000CH のコンテナに 172.28.0.40
  - id: at-complete
    msg: ATTACH COMPLETE
    from: volte10
    to: epc
    proto: HTTP
    fields:
      - ESM のコンテナ: ACTIVATE DEFAULT EPS BEARER CONTEXT ACCEPT
  - id: ue-pcscf
    do: P-CSCF のアドレスを決める
    at: volte10
    fields:
      - 確かめること: PDN のアドレスが、P-CSCF に届く自分のアドレスと同じか
      - 使うもの: 一覧の先頭のアドレスと、ポート 5060
  # IMS-AKA の 1 回目（手順 1 から 6）
  - id: reg1-ue-p
    msg: REGISTER
    from: volte10
    to: pcscf
    proto: SIP
    note: (1 回目)
    fields:
      - 送り先: UE の 5060 番から P-CSCF の 5060 番へ
      - Security-Client: 自分のアソシエーションの申し出
      - Require: "`sec-agree`。Proxy-Require にも入る"
      - Authorization: "algorithm は入れない。nonce と response は空"
  - id: pcscf-keep
    do: Security-Client を預かる
    at: pcscf
    fields:
      - 選ぶ方式: sec-agree があるので IMS-AKA
      - 外して保存するもの: Security-Client
      - Authorization に足すもの: '`integrity-protected="no"`'
  - id: reg1-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (1 回目)
  - id: reg1-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (1 回目)
  - id: mar
    msg: MAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - 見たもの: '`integrity-protected="no"`。IMS-AKA の最初の登録'
      - SIP-Auth-Data-Item: "方式 `Digest-AKAv1-MD5` の項目を 1 つ"
  - id: hss-vector
    do: 認証ベクトルを作る
    at: hss
    fields:
      - 進めるもの: SQN_HE
      - 計算するもの: RAND から MILENAGE で XRES、CK、IK、AUTN
  - id: maa
    msg: MAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - SIP-Authenticate: RAND と AUTN を並べた 32 オクテット
      - SIP-Authorization: XRES
      - Confidentiality-Key: CK
      - Integrity-Key: IK
  - id: scscf-challenge
    do: 問いを組み立てる
    at: scscf
    fields:
      - nonce: RAND と AUTN の base64
      - P-CSCF 向けのもの: "`ik` と `ck` のパラメータ"
      - 取っておくもの: RAND、XRES、IK、CK
  - id: r401-s-i
    msg: "401"
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - WWW-Authenticate: "algorithm ごとに 1 つ。`AKAv2-SHA-256` が先、`AKAv1-MD5` が後。どちらにも nonce、`ik`、`ck`"
  - id: r401-i-p
    msg: "401"
    from: icscf
    to: pcscf
    proto: SIP
  - id: pcscf-sa
    do: 鍵を外し、自分の申し出を足す
    at: pcscf
    fields:
      - 外すもの: "`ik` と `ck`。鍵は UE まで運ばれない"
      - 足すもの: Security-Server。保護サーバーポートは 5064 番
  - id: r401-p-ue
    msg: "401"
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - 届く先: UE の 5060 番
  # IMS-AKA の 2 回目（手順 7 から 12）
  - id: ue-verify
    do: 網を確かめる
    at: volte10
    fields:
      - 比べるもの: AUTN の MAC と、USIM が計算した MAC
      - もう 1 つの確認: SQN が新しいこと
      - 計算するもの: RES、CK、IK
  - id: reg2-ue-p
    msg: REGISTER
    from: volte10
    to: pcscf
    proto: SIP
    note: (2 回目)
    fields:
      - 送り先: UE の 5062 番から P-CSCF の 5064 番へ
      - Security-Verify: P-CSCF の Security-Server を写したもの
      - Authorization: "`algorithm=AKAv2-SHA-256`。RES と IK と CK から作るパスワードの response"
  - id: pcscf-verify
    do: 取り決めを確かめる
    at: pcscf
    fields:
      - 届いた先: 保護サーバーポート 5064 番
      - Security-Verify: 自分が送った Security-Server と同じ
      - Authorization に足すもの: '`integrity-protected="yes"`'
  - id: reg2-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (2 回目)
  - id: reg2-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (2 回目)
  - id: scscf-verify
    do: RES を確かめる
    at: scscf
    fields:
      - 計算: UE が書いた algorithm の Digest。パスワードは XRES と IK と CK から作る
      - 一致したとき: SAR で登録を HSS に知らせる
  - id: sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
  - id: saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
  - id: ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: ok-p-ue
    msg: 200 OK
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - 送り先: P-CSCF の 5064 番から UE の 5062 番へ
      - このあと: UE はすべての要求を 5064 番に送る
      - P-Associated-URI: 規制されていない識別子。先頭が既定
  # Rx の信号の経路
  - id: aar-sig
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (信号の経路)
    fields:
      - Flow-Usage: AF_SIGNALLING
      - Media-Component-Number: 0。Flow-Number も 0
      - Specific-Action: 2 と 4。bearer の喪失と解放
      - Framed-IP-Address: 172.28.0.120
  - id: aaa-sig
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (信号の経路)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - PCRF がしたこと: このアドレスの IP-CAN セッションと結び付けた
  - id: reg-ue2
    msg: REGISTER
    from: ue2
    to: pcscf
    proto: SIP
    note: (ソフトフォン)
    fields:
      - 認証: SIP Digest とパスワード
      - アクセス網: attach していない
  - id: aar-ue2
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (ue2 の信号の経路)
    fields:
      - Framed-IP-Address: 172.28.0.101
  - id: aaa-ue2
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (ue2 の信号の経路)
    fields:
      - Experimental-Result-Code: IP-CAN_SESSION_NOT_AVAILABLE (5065)
  - id: pcscf-nopolicy
    do: ポリシー制御なしで扱う
    at: pcscf
    fields:
      - この UE については: 通話の AAR も送らない
  # 通話のメディアと dedicated bearer
  - id: inv-10-p
    msg: INVITE
    from: volte10
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`tel:+12025550112`"
      - Supported: "`100rel, timer, 199, precondition`。Require には入れない"
      - SDP: offer。AMR-WB が先で、PCMU は最後
      - "curr:qos local": "`none`。まだ資源を確保していない"
      - "des:qos": "local は `mandatory`、remote は `optional`"
  - id: inv-p-12
    msg: INVITE
    from: pcscf
    to: volte12
    proto: SIP
    note: (両側の S-CSCF と MMTel AS を通ったあと)
  - id: r183-12-p
    msg: "183"
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - Require: "`100rel`、`precondition`"
      - SDP: "answer。`m=audio 60734 RTP/AVP 96 100` と `b=AS:41`"
      - "conf:qos": "`remote sendrecv`。発信側の資源がそろったら知らせてほしい"
      - "des:qos": 発信側の区間も mandatory に上げる
  - id: aar-12
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (着信側のメディア)
    fields:
      - AF-Application-Identifier: "`urn:urn-7:3gpp-service.ims.icsi.mmtel`"
      - Media-Sub-Component: フロー 1 と 2。RTP と RTCP
      - Flow-Description: "`permit in 17 from 172.28.0.121 to 172.28.0.50 40004`"
      - Max-Requested-Bandwidth-UL: "80000。`b=AS` から。単位は bit/s"
      - Rx-Request-Type: INITIAL_REQUEST
  - id: pcrf-grant
    do: 音声を許可する
    at: epc
    fields:
      - QCI: 1。帯域を保証する会話音声
      - 速度: P-CSCF が求めた帯域から。単位は kbit/s
  - id: aaa-12
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (着信側のメディア)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: bearer-12
    msg: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte12
    proto: HTTP
    fields:
      - eps_bearer_identity: 6。linked は default bearer の 5
      - eps_qos: QCI 1。上り 80、下り 41 kbit/s
      - tft: UE と中継ノードの間の RTP と RTCP、上下で 4 つ
  - id: ue12-reserved
    do: 資源を確保したとみなす
    at: volte12
    fields:
      - 答え: "`activate-dedicated-eps-bearer-context-accept`"
      - プリコンディション: 自分の区間を sendrecv にする
  - id: aar-10
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (発信側のメディア)
    fields:
      - きっかけ: volte10 へ向かう 183。同じ answer を持っている
      - Framed-IP-Address: 172.28.0.120
  - id: aaa-10
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (発信側のメディア)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: bearer-10
    msg: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte10
    proto: HTTP
    fields:
      - tft: 相手のアドレスは中継ノードの 172.28.0.50
  - id: ue10-reserved
    do: 資源を確保したとみなす
    at: volte10
    fields:
      - 次に送るもの: UPDATE。新しい offer で知らせる
  - id: r183-p-10
    msg: "183"
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - 送るとき: 発信側の AAR に答えが返ってから
  # プリコンディション
  - id: prack
    msg: PRACK
    from: volte10
    to: pcscf
    proto: SIP
    fields:
      - RAck: 183 の RSeq と、INVITE の CSeq
  - id: prack-ok
    msg: 200 (PRACK)
    from: pcscf
    to: volte10
    proto: SIP
  - id: update-10-p
    msg: UPDATE
    from: volte10
    to: pcscf
    proto: SIP
    fields:
      - "curr:qos local": "`sendrecv`。dedicated bearer を受け取ったから"
      - 送るとき: PRACK に 200 が返ってから
  - id: update-p-12
    msg: UPDATE
    from: pcscf
    to: volte12
    proto: SIP
  - id: update-ok-12
    msg: 200 (UPDATE)
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - "curr:qos local": "`sendrecv`"
      - "curr:qos remote": "`sendrecv`。両方の区間がそろった"
  - id: update-ok-10
    msg: 200 (UPDATE)
    from: pcscf
    to: volte10
    proto: SIP
  - id: pre-met
    do: 必須のプリコンディションがそろう
    at: volte12
    fields:
      - 表の行: 区間（local と remote）と向き（send と recv）の組
      - 鳴らす条件: mandatory の行がすべて current
  - id: r180-12-p
    msg: "180"
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - ここで初めて: 資源がそろったので呼び出しを始める
  - id: r180-p-10
    msg: "180"
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - 発信側で見ると: UPDATE への 200 より先に届くことがある
  - id: ok-inv-12
    msg: 200 (INVITE)
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - SDP: 無い。answer は 183 で届いている
  - id: ok-inv-10
    msg: 200 (INVITE)
    from: pcscf
    to: volte10
    proto: SIP
  - id: ack
    msg: ACK
    from: volte10
    to: pcscf
    proto: SIP
  # 網が bearer を落とす
  - id: epc-deact
    do: 音声の bearer を非活性化する
    at: epc
    fields:
      - 始めるもの: 網が始める EPS bearer コンテキストの非活性化
      - きっかけ: bearer への DELETE。ラボのボタンか `curl`
  - id: deact-10
    msg: DEACTIVATE EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte10
    proto: HTTP
    note: (イベントストリーム)
  - id: asr
    msg: ASR
    from: epc
    to: pcscf
    proto: Rx
    fields:
      - Abort-Cause: BEARER_RELEASED (0)
      - RAR でない理由: 音声の bearer がこのセッションのメディアをすべて運んでいた
  - id: asa
    msg: ASA
    from: pcscf
    to: epc
    proto: Rx
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: str
    msg: STR
    from: pcscf
    to: epc
    proto: Rx
    note: (続いて STA)
    fields:
      - 終えるもの: この通話の Rx のセッション
  - id: pcscf-bye
    do: 保存したダイアログから BYE を作る
    at: pcscf
    fields:
      - 材料: Contact、タグ、CSeq、経路
      - 送る先: 相手の側と、サービスを受ける UE
  - id: bye-core
    msg: BYE
    from: pcscf
    to: scscf
    proto: SIP
    note: (volte10 の名前で)
    fields:
      - Reason: '`SIP;cause=503;text="Service Unavailable"`'
  - id: bye-10
    msg: BYE
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - Reason: "`SIP;cause=503`"
      - このプロジェクトが足したもの: 信号の経路が残る UE にも知らせる
  - id: bye-12
    msg: BYE
    from: pcscf
    to: volte12
    proto: SIP
    note: (S-CSCF と MMTel AS を通ったあと)
  - id: deact-12
    msg: DEACTIVATE EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte12
    proto: HTTP
    note: (イベントストリーム)
    fields:
      - その前に: volte12 の側の P-CSCF が STR でセッションを終える
```

---

# VoLTE の呼の流れ

ims-practice 勉強会 第 9 回（アクセス網、IMS-AKA、Rx でのポリシー制御、VoLTE の通話）

---

[layout: summary]

## この回で分かること

最初の問い。携帯電話から発信すると、相手の電話が鳴る前に、網は何を用意しているでしょうか。

- VoLTE の端末が、SIP Digest で登録するソフトフォンとどこで違うか。
- UE が EPC に attach し、P-CSCF のアドレスを受け取るしくみ。
- IMS-AKA で、UE と網が互いを確かめるしくみ。
- ISIM の無い端末が IMSI から作る識別子と、そのうち網が規制するもの。
- P-CSCF が Rx で PCRF に音声の資源を求め、網が bearer を作るしくみ。
- 資源がそろうまで着信側を鳴らさない、プリコンディションのしくみ。
- 網が bearer を失ったときに、P-CSCF が通話を終わらせるしくみ。

> 手順書 [VoLTE の呼の流れ](../../../docs/guide/ja/volte.md)（`docs/guide/ja/volte.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.229 V20.1.0、TS 29.214 V20.0.0、TS 24.301 V20.1.0、TS 33.203、TS 33.102、TS 35.206、TS 35.208、RFC 3310、RFC 3329、RFC 3312、RFC 4867、GSMA IR.92 v24.0 などです。

---

## ソフトフォンの通話から変わること

| | ソフトフォン（SIP Digest、PCMU） | VoLTE の端末 |
| --- | --- | --- |
| アクセス網 | 無い。P-CSCF は設定で与える | EPC に attach する。P-CSCF は網が教える |
| 認証 | SIP Digest とパスワード | IMS-AKA と sec-agree。パスワードは無い |
| 資源 | 考えない | P-CSCF が Rx で PCRF に求める |
| 呼び出し | 183 のあとすぐ | 両側の bearer がそろってから |
| 音声 | PCMU の RTP | AMR-WB、RTCP、telephone-event |
| 切断 | UE の BYE だけ | 網が bearer を失うと P-CSCF が切る |
| 識別子 | 設定で与え、登録したものを使う | IMSI から作る。登録したものは規制される |

- すべて左の列のままだと何が困るでしょうか。音声の通り道ができる前に着信側が鳴り、出ても何も聞こえません。
- ソフトフォンはそのまま動きます。EPC スタブを通らない UE は PCRF が知らないので、P-CSCF はポリシー制御なしで扱います。

> 手順書 [「VoLTE の端末とソフトフォンの違い」](../../../docs/guide/ja/volte.md#volte-の端末とソフトフォンの違い)。IR.92 4.4、2.4.1、2.4.2.3、3.2.1。TS 33.203 6.1.1、7。TS 29.214 4.4.1。TS 24.229 5.2.8.1.2。RFC 3312。

---

## 登場するノード

```ims-scene
id: nodes
nodes: [volte10, epc, volte12, pcscf, ue2, icscf, hss, scscf, mmtel]
lead: VoLTE の端末が増やすのは左の 3 つです。アクセス網と、そこにつながる 2 台の端末です。
steps:
  - id: volte10
    title: volte10（172.28.0.120）
    focus: [volte10]
    text: |
      発信側です。番号は `tel:+12025550110` で、加入者 10 の USIM に K と OPc があります。
  - id: volte12
    title: volte12（172.28.0.121）
    focus: [volte12]
    text: 着信側です。番号は `tel:+12025550112` で、呼び出し時間のあとに自分で応答します。
  - id: epc
    title: EPC スタブと PCRF（172.28.0.80）
    focus: [epc]
    checkpoint: true
    text: |
      役割 `epc` の 1 つのノードです。端末には HTTP の 8080 番で、P-CSCF には Rx の 3868 番で答えます。
      本物の網では PDN GW と PCRF が別のノードで、あいだに Gx があります。
  - id: pcscf
    title: P-CSCF（172.28.0.40）
    focus: [pcscf]
    text: |
      SIP の 5060 番と、IMS-AKA の端末のための保護サーバーポート 5064 番で待ちます。
      PCRF に対する Rx のクライアントでもあります。
  - id: core
    title: I-CSCF、S-CSCF、HSS（172.28.0.20、30、10）
    focus: [icscf, scscf, hss]
    checkpoint: true
    text: |
      S-CSCF が IMS-AKA で認証を求め、HSS が認証ベクトルを作ります。
      REGISTER は 1 回目も 2 回目も I-CSCF を通ります。
  - id: others
    title: ue2 と MMTel AS（172.28.0.101、75）
    focus: [ue2, mmtel]
    text: |
      ue2 は attach しないソフトフォンで、PCMU だけを offer します。
      MMTel AS は付加サービスの回と同じで、VoLTE の通話もすべてここを通ります。
```

| ノード | アドレス | VoLTE の通話での役割 |
| --- | --- | --- |
| volte10 | 172.28.0.120 | 発信側。`tel:+12025550110`。加入者 10。 |
| volte12 | 172.28.0.121 | 着信側。`tel:+12025550112`。加入者 12。 |
| ue2、SIPp | 172.28.0.101、102 | PCMU だけのソフトフォンと、IMS-AKA の試験相手。 |
| EPC スタブと PCRF | 172.28.0.80 | 役割 `epc`。UE の API は HTTP の 8080、Rx は 3868。 |
| P-CSCF | 172.28.0.40 | 5060 番と保護サーバーポート 5064 番。Rx のクライアント。 |
| S-CSCF、HSS | 172.28.0.30、10 | IMS-AKA の challenge と、認証ベクトル。 |
| MMTel AS、中継ノード | 172.28.0.75、50 | MMTel AS の回と同じ。中継は RTCP も通す。 |

- volte10 と volte12 は `compose.yaml` のプロファイル `volte` のサービスです。鍵は `deploy/compose/hss.yaml` に、加入者ごとのコンテナのアドレスは `deploy/compose/epc.yaml` にあります。
- ラボ（172.28.0.60）は volte10 と volte12 を動かし、シーケンス図に Rx を出し、網の手順を始める EPC スタブのパスをホストへ中継します。

> 手順書 [「登場するノード」](../../../docs/guide/ja/volte.md#登場するノード)

---

## EPC スタブが残すものと省くもの

```text
 UE ==(HTTP + JSON: ATTACH REQUEST, ...)==> EPC stub: PDN GW and PCRF (epc role)
 |                                           ^
 |                                           |  Rx (Diameter)
 +------------ SIP ------------> P-CSCF -----+
```

- メッセージの名前と項目は TS 24.301（8.2、8.3）の ESM と EMM のとおりです。URL では小文字です。例は `POST /ues/001010000000010/attach-request` です。
- 網から送るものは、UE のイベントストリーム `GET /ues/<IMSI>/events` に Server-Sent Events で届きます。
- アドレスは加入者ごとに静的です（TS 23.401 5.3.1.1）。トンネルは無く、パケットはそのまま届きます。
- 無線、S1、NAS の符号化とセキュリティ、Gx はありません。PCRF と PDN GW は、`epc` の役割の中で状態を共有します。bearer は記録だけで、QoS の制御もパケットのフィルタもしません。
- 本物の Diameter は Rx だけです。学習者が Wireshark で読むのは IMS の側なので、そこを残しました。NAS のメッセージの名前を使うので、無線も NAS のデコーダも無しに attach を読めます。

> 手順書 [「スタブが省くこと」](../../../docs/guide/ja/volte.md#スタブが省くこと)

---

## キャプチャを用意する

- `scripts/` にある VoLTE の通話の e2e のスクリプトが、この回の手順をすべて行います。SIPp の登録、VoLTE の通話と ue2 への通話、ラボからの DTMF と保留、網による bearer の解放を行い、キャプチャのメッセージを 1 つずつ確かめます。スクリプトの名前は手順書の [「動かしてみる」](../../../docs/guide/ja/volte.md#動かしてみる) にあります。
- 手順書のフレーム番号は、このスクリプトを 1 回動かしたときのものです。手元の番号とは違います。
- キャプチャは `captures/`、フレームの一覧は `e2e-out/`、AMR-WB の録音は `captures/volte-*.amr` に残ります。2 分ほどかかります。

この回の全体の表示フィルタです。

```text
(sip && !(sip.CSeq.method in {"SUBSCRIBE", "NOTIFY", "MESSAGE"})) || diameter.cmd.code in {265, 274, 275, 303} || (http && ip.addr == 172.28.0.80)
```

- 登録、通話、Rx（265 は AAR と AAA、274 は ASR と ASA、275 は STR と STA）、MAR（303）、EPC スタブの API が残ります。

> 手順書 [「動かしてみる」](../../../docs/guide/ja/volte.md#動かしてみる) と [「キャプチャを開く」](../../../docs/guide/ja/volte.md#キャプチャを開く)

---

## attach と P-CSCF の発見

```ims-scene
id: attach
view: ladder
nodes: [volte10, epc, pcscf]
lead: 端末は P-CSCF のアドレスをアクセス網から受け取ります。設定には書いてありません。
steps:
  - id: cell
    title: セルの情報を読む
    show: [at-si]
    text: |
      報知する無線が無いので、スタブはシステム情報を HTTP のリソースにしています。
      端末はこの値を P-Access-Network-Info に書きます。
  - id: stream
    title: attach の前にイベントストリームを開く
    show: [at-stream]
    checkpoint: true
    text: |
      網が自分から送るものは開いたままのこの応答に少しずつ届きます。
      attach より先に開くので、attach した直後に網が送るものも受け取れます。
  - id: request
    title: ATTACH REQUEST で IMS の APN につなぐ
    show: [at-req]
    text: EMM の ATTACH REQUEST が、ESM の PDN CONNECTIVITY REQUEST を運びます。PCO で P-CSCF のアドレスを求めます。
  - id: accept
    title: ATTACH ACCEPT がアドレスと bearer を渡す
    show: [at-accept]
    checkpoint: true
    text: |
      default bearer は QCI 5 で、IMS の信号のためのものです。
      P-CSCF のアドレスは PCO の識別子 000CH のコンテナに入っています。
  - id: complete
    title: ATTACH COMPLETE で attach が終わる
    show: [at-complete]
    text: PDN 接続ができました。トンネルは無く、端末のパケットはそのまま相手に届きます。
  - id: decide
    title: 端末が P-CSCF を決める
    show: [ue-pcscf]
    text: |
      一覧の先頭のアドレスだけを使います。PCO にポートは無いので、標準の 5060 番に送ります。
  - id: register
    title: 最初の REGISTER がそのアドレスへ
    show: [reg1-ue-p]
    detail: []
    checkpoint: true
    text: ここから登録が始まります。中身は次のシーンで 1 区間ずつ読みます。
```

```ladder
volte10, EPC スタブ
volte10 -> EPC スタブ: GET /system-information // セル。MCC 001、MNC 01、TAC 1
volte10 -> EPC スタブ: GET /ues/001010000000010/events // イベントストリームは開いたまま
volte10 -> EPC スタブ: POST /ues/001010000000010/attach-request // ATTACH REQUEST、APN ims
volte10 <-- EPC スタブ: 200、ATTACH ACCEPT と ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST
volte10 -> EPC スタブ: POST /ues/001010000000010/attach-complete
```

| ATTACH ACCEPT の中 | 見どころ |
| --- | --- |
| `eps_bearer_identity` 5、`qci` 5 | IMS の信号のための default bearer。 |
| `pdn_address` | 加入者の静的なアドレス 172.28.0.120。 |
| PCO の `000CH` のコンテナ | P-CSCF のアドレス 172.28.0.40。ポートは 5060。 |

> 手順書 [「attach」](../../../docs/guide/ja/volte.md#attach)。IR.92 4.4.1、4.5。TS 23.203 6.1.7。TS 24.008 10.5.6.3。TS 24.229 L.2.2.1、7.2A.4.3、L.3.1.1。

---

## attach のほかの見どころ

- フィルタは `http && ip.addr == 172.28.0.80` です。HTTP のボディの JSON を開きます。
- PCO で P-CSCF を伝えるのは TS 24.229 L.2.2.1 の方法 II で、IR.92 4.5 が求める方法です。一覧の最初のアドレスが優先です。
- ポートは標準の 5060 です。Wireshark が Decode As なしで SIP をデコードできるようにするためです。
- UE は、PDN のアドレスが P-CSCF に届くアドレスと同じかを確かめ、違えば止まります。
- UE はセルを P-Access-Network-Info に書きます。`3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101` には、MCC 001、MNC 01、16 進 4 桁の TAC、7 桁の ECI が並んでいます。

---

## UE とアクセス網の境界をコードで見る

[internal/ue/access.go](../../../internal/ue/access.go)

```go
type AccessNetwork interface {
	// Attach connects the UE to the IMS APN and returns what the network
	// gave it (TS 24.229 L.2.2.1).
	Attach(ctx context.Context) (Attachment, error)
	// Events delivers what the network tells the UE after the attach. It
	// is closed once the UE has detached or the network has detached it.
	Events() <-chan AccessEvent
	// Detach disconnects the UE from the network.
	Detach(ctx context.Context) error
}
```

- 実装は 1 つで、EPC スタブへの HTTP のクライアントです（`access_epc.go`）。
- イベントは bearer の活性化、解放、網からの detach の 3 つです。
- UE はこの 3 つのメソッドを通してしかアクセス網を見ません。だから UE のほかの部分を変えずに、同じインタフェースの後ろに NAS の実装を置けます。

---

## IMS-AKA の鍵

| 持つもの | 持っている値 | 書いてある場所 |
| --- | --- | --- |
| volte10 の USIM | K、OPc、SQN_MS | `ue` の `-k`、`-opc`、`-sqn`（`compose.yaml`） |
| HSS | 同じ加入者の K、OPc、AMF、SQN_HE | `deploy/compose/hss.yaml` の加入者 10 の `aka:` |

- K と OPc は MILENAGE の試験データ 1 の値です（TS 35.208 4.3.1）。
- OPc は事業者の OP と K から作ります（TS 35.206 4.1）。SIPp のように OP を渡すときは `-op` を使います。
- 共有するのはこれだけです。ほかの値は、網と UE がそれぞれ、この鍵とランダムな challenge から計算します。SIP Digest で登録する加入者と違い、パスワードはどこにもありません。
- volte12 は試験データ 3（4.3.3）の K と OPc を持ちます。SIPp 3.7 は K と OP を文字列のバイトとして読むので、SIPp 用の加入者 11 の鍵は印字できる文字です。手順書の「SIPp」の節にあります。

> 手順書 [「鍵」](../../../docs/guide/ja/volte.md#鍵)

---

## ISIM の無い端末の識別子

| 識別子 | 値 | 現れる場所 |
| --- | --- | --- |
| 秘密の識別子（private user identity） | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | Authorization の `username` と、Cx の User-Name。 |
| 一時的な公開の識別子（temporary public user identity） | `sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | REGISTER の From と To、Cx の Public-Identity。ほかには現れない。 |
| 既定の公開の識別子（default public user identity） | `sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org` | 200 OK の最初の P-Associated-URI。そのあとの From と P-Asserted-Identity。 |
| 番号の tel URI | `tel:+12025550110` | 2 つ目の P-Associated-URI。 |

- volte10 と volte12 のカードには USIM があり、ISIM はありません。USIM には IMSI と鍵があっても、IMS の識別子はありません。そこで端末は IMSI（ここでは 001010000000010）から識別子を作ります。
- 一時的な識別子には IMSI が入っているので、HSS はこれを規制します。登録にだけ使われ、見えるのは CSCF と HSS だけです。`deploy/compose/hss.yaml` では `barred_identities` に挙げてあり、SAA のユーザープロファイルでは `<BarringIndication>` が付きます。

> 手順書 [「ISIM の無い端末の識別子」](../../../docs/guide/ja/volte.md#isim-の無い端末の識別子)。TS 24.229 5.1.1.1A。TS 23.003 13.2、13.3、13.4B。TS 23.228 E.3.1。TS 29.228 B.2.1。

---

## 識別子をキャプチャで見る

```text
REGISTER  volte10 -> P-CSCF  To: <sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org>
200 OK    P-CSCF -> volte10  P-Associated-URI: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
                             P-Associated-URI: <tel:+12025550110>
INVITE    volte10 -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
INVITE    P-CSCF -> S-CSCF   P-Asserted-Identity: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
```

- S-CSCF は、P-Associated-URI に規制されていない識別子だけを、既定の識別子を先頭にして並べます。UE は最初のものを既定の公開の識別子として覚えます。登録した識別子は一覧に無いので、規制されたものとして扱います。
- UE は P-Preferred-Identity を送らないので、P-CSCF は既定の識別子を P-Asserted-Identity に入れます。規制された識別子からのリクエストには 403、その識別子あてには 404 が返ります。
- `sip && !(sip.CSeq.method == "REGISTER") && frame contains "sip:001010000000010@ims"` に合うパケットはありません。Contact にも IMSI は入らず、ユーザー部は UUID です。ソフトフォンには SIM がありません。識別子は IMC に入っているのと同じように設定で与えられ、登録したものをそのまま使います。

> 手順書 [「ISIM の無い端末の識別子」](../../../docs/guide/ja/volte.md#isim-の無い端末の識別子)。TS 24.229 5.4.1.2.2F、5.1.1.2.1、5.2.6.3.1、5.4.3.2、5.4.3.3、5.1.1.1B.1。

---

## IMS-AKA の最初の REGISTER と 401（1〜6）

```ims-scene
id: challenge
nodes: [volte10, pcscf, icscf, scscf, hss]
lead: HSS が渡すのはパスワードではなく、1 回だけ使う認証ベクトルです。
steps:
  - id: send
    title: 保護されていない REGISTER
    only: [reg1-ue-p]
    checkpoint: true
    text: |
      端末が 5060 番に送る REGISTER は、この 1 通だけです。
      Authorization の nonce と response は空で、誰を認証するかだけを伝えます。
  - id: keep
    title: P-CSCF が Security-Client を預かる
    show: [pcscf-keep]
    zoom: [volte10, pcscf]
    text: |
      sec-agree の Security-Client を見て、IMS-AKA を選びます。
      外して保存し、まだ何にも保護されていないことを `integrity-protected` で伝えます。
  - id: forward
    title: I-CSCF を通って S-CSCF へ
    show: [reg1-p-i, reg1-i-s]
    active: reg1-i-s
    text: |
      I-CSCF は UAR と UAA で担当の S-CSCF を尋ねます。登録の回と同じなので、図では省いています。
  - id: mar
    title: S-CSCF が認証ベクトルを求める（MAR）
    only: [reg1-i-s, mar]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      方式を `Unknown` のままにせず、IMS-AKA を名指しします。
      そう決められるのは、P-CSCF が付けた `integrity-protected="no"` を見たからです。
  - id: vector
    title: HSS がベクトルを計算する
    show: [hss-vector]
    zoom: [icscf, hss, scscf]
    text: SQN_HE を 1 つ進め、新しい RAND を選び、USIM と同じ鍵から 5 つの値を作ります。
  - id: maa
    title: HSS が問いと答えを返す（MAA）
    show: [maa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      RAND と AUTN は端末への問い、XRES は答え合わせの元、CK と IK はアソシエーションの鍵です。
      パスワードはどこにもありません。
  - id: nonce
    title: S-CSCF が問いを組み立てる
    show: [scscf-challenge]
    zoom: [icscf, hss, scscf]
    text: |
      RAND と AUTN をそのまま base64 にして nonce にします。鍵は P-CSCF 向けの別のパラメータに入れます。
      WWW-Authenticate は示す algorithm ごとに 1 つで、優先する順に並べます。先が AKAv2-SHA-256、後が後方互換のための AKAv1-MD5 です。
  - id: back
    title: 401 が P-CSCF まで戻る
    only: [r401-s-i, r401-i-p]
    active: r401-i-p
    text: 401 は失敗ではなく、決まった手順の一部です。各ノードは自分の Via を取り除きます。
  - id: strip
    title: P-CSCF が鍵を外し、自分の申し出を足す
    show: [pcscf-sa]
    checkpoint: true
    text: |
      `ik` と `ck` はここで消えます。端末は同じ鍵を自分の K と RAND から計算できるので、送る必要がありません。
  - id: ue
    title: 401 が端末に届く
    show: [r401-p-ue]
    text: 行きも帰りも 5060 番です。保護ポートを使うのはこのあとの REGISTER からです。
```

| # | 区間 | 見どころ |
| --- | --- | --- |
| 698 | UE:5060 → P-CSCF:5060 | `Security-Client`、`Require: sec-agree`。空の nonce と response。 |
| 709 | P-CSCF → I-CSCF | Security-Client を外して保存。`integrity-protected="no"`。 |
| 724 | S-CSCF → HSS（MAR） | 方式 `Digest-AKAv1-MD5` の項目を 1 つ求める。この方式名は AKA のどちらの版でも同じ。 |
| 726 | HSS → S-CSCF（MAA） | RAND と AUTN、XRES、CK、IK。 |
| 727 | S-CSCF → I-CSCF（401） | WWW-Authenticate が 2 つ。`AKAv2-SHA-256` が先。nonce は RAND と AUTN の base64。`ik` と `ck`。 |
| 737 | P-CSCF → UE（401） | `ik` と `ck` を外し、`Security-Server` を足す。 |

> 手順書 [「シーケンス図」](../../../docs/guide/ja/volte.md#シーケンス図) の 1 から 6。TS 24.229 5.1.1.2.2、5.2.2.1、5.2.2.2、5.4.1.1、5.4.1.2.1A。RFC 3329 2.3.1。TS 33.203 Annex H、7.2。TS 29.229 6.3.9。TS 33.102 6.3.2。TS 29.228 6.3。RFC 3310 3.2。

---

## IMS-AKA で網を確かめて答える（7〜12）

```ims-scene
id: answer
nodes: [volte10, epc, pcscf, icscf, scscf, hss]
lead: 端末も網を確かめます。SIP Digest には無かった向きの認証です。
steps:
  - id: verify
    title: 端末が AUTN を確かめる
    only: [r401-p-ue, ue-verify]
    active: ue-verify
    zoom: [volte10, pcscf]
    checkpoint: true
    text: |
      MAC が合えば、網が同じ K を知っていると分かります。
      SQN が新しいことも確かめ、そのうえで RES、CK、IK を計算します。
  - id: respond
    title: アソシエーションを通る 2 回目の REGISTER
    show: [reg2-ue-p]
    zoom: [volte10, pcscf]
    checkpoint: true
    text: |
      送り先が保護サーバーポートに変わります。response は RES をパスワードにした Digest です。
      Security-Verify は途中の誰かが強い申し出を消していないかを確かめるためのものです。
  - id: check
    title: P-CSCF が取り決めを確かめる
    show: [pcscf-verify]
    zoom: [volte10, pcscf]
    text: 届いたポートと Security-Verify が合っていたので、保護されたものとして扱います。
  - id: forward
    title: また I-CSCF を通って S-CSCF へ
    show: [reg2-p-i, reg2-i-s]
    active: reg2-i-s
    text: 2 回目の UAR と UAA も登録の回と同じです。図では省いています。
  - id: res
    title: S-CSCF が RES を確かめる
    only: [reg2-i-s, scscf-verify]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: 取っておいた XRES、IK、CK から、UE が書いた algorithm のパスワードを作り、同じ Digest を計算して届いた response と比べます。
  - id: sar
    title: HSS に登録を知らせる（SAR と SAA）
    show: [sar, saa]
    active: sar
    zoom: [icscf, hss, scscf]
    text: ここから先は登録の回と同じです。SAA のプロファイルに、一緒に登録される識別子と iFC が入っています。
  - id: ok
    title: 200 OK が端末まで戻る
    only: [ok-s-i, ok-i-p, ok-p-ue]
    active: ok-p-ue
    checkpoint: true
    text: |
      200 OK は保護ポートの組を通って戻り、アソシエーションはこれで確立します。
      このあと端末はすべての要求を 5064 番に送ります。
  - id: aar
    title: P-CSCF が信号の経路を見張る
    show: [aar-sig]
    detail: []
    text: |
      登録できた端末について、信号の bearer を失ったら知らせるよう PCRF に頼みます。
      中身は後半の Rx の節で読みます。
  - id: aaa
    title: PCRF が IP-CAN セッションと結び付ける
    show: [aaa-sig]
    detail: []
    text: 200 OK とこの AAR はどちらが先に P-CSCF を出ることもあります。
```

| # | 区間 | 見どころ |
| --- | --- | --- |
| 740 | UE | AUTN の MAC と SQN を確かめ、RES、CK、IK を計算する。 |
| 740 | UE:5062 → P-CSCF:5064 | `Security-Verify` に Security-Server を写す。RES で計算した response。 |
| 749 | P-CSCF → I-CSCF | 取り決めを確かめ、`integrity-protected="yes"`。 |
| 760、764 | S-CSCF、SAR | UE が書いた algorithm の Digest を計算して比べる。一致したので登録する。 |
| 783 | P-CSCF:5064 → UE:5062 | 200 OK。アソシエーションが確立する。 |
| 786 | P-CSCF → PCRF | AAR。信号の経路（Rx の節）。 |

> 手順書 [「シーケンス図」](../../../docs/guide/ja/volte.md#シーケンス図) の 7 から 12。TS 33.102 6.3.3、C.2。TS 33.203 7.1。RFC 3329。RFC 3310 3.3。TS 24.229 5.4.1.2.2、5.2.2.2。TS 29.214 A.4。

---

## IMS-AKA の前半と後半で見えること

- 保護されていない 5060 番に UE が送る REGISTER は、最初の 1 回だけです。200 OK のあと、UE はすべての要求を P-CSCF の保護ポート 5064 に送り、P-CSCF は UE の 5062 に送ります。
- P-CSCF は 401 から `ik` と `ck` を取り除くので、鍵は UE まで届きません。UE は自分の K と RAND から同じ鍵を計算します。
- AUTN の MAC が合えば、網が K を知っていることが分かります。網の側も認証されるわけで、SIP Digest には無かった向きです。UE は SQN が新しいことも確かめます。
- Security-Verify で、P-CSCF は、途中の誰かが強い方式の申し出を消していないかを確かめます。200 OK と #786 の AAR は、どちらが先に P-CSCF を出ることもあります。
- 再登録と登録の解除は、アソシエーションの上で、新しい challenge なしに行います（TS 24.229 5.1.1.4.2、5.1.1.6.2）。
- フィルタ。`sip.Security-Client || sip.Security-Server || sip.Security-Verify`、`sip.WWW-Authenticate contains "AKAv2-SHA-256" || sip.WWW-Authenticate contains "AKAv1-MD5"`（`contains "ck="` にすると、S-CSCF からの 401 だけが残ります）、保護ポートは `udp.port == 5064 || tcp.port == 5064` です。

---

## 認証ベクトルをコードで見る

[internal/aka/aka.go](../../../internal/aka/aka.go)

```go
func NewAuthenticationVector(secret Secret, rand RAND, sqn SQN, amf AMF) AuthenticationVector {
	m := secret.milenage()
	mac := m.F1(rand, sqn.octets(), amf)
	res := m.F2(rand)
	return AuthenticationVector{
		RAND: rand,
		XRES: res[:],
		CK:   m.F3(rand),
		IK:   m.F4(rand),
		AUTN: newAUTN(conceal(sqn, m.F5(rand)), amf, mac),
	}
}
```

```sh
go test ./internal/aka -run TestAuthenticationVectorFromTestSet1 -v
```

- f1 から f5 は MILENAGE の関数です（TS 35.206）。AUTN は SQN ⊕ AK（6 オクテット）、AMF（2 オクテット）、MAC-A（8 オクテット）です。
- キャプチャの RAND はランダムなので、出力は TS 35.208 の値になりません。試験データは計算そのものを確かめるものです。`go test ./internal/milenage -run TestConformanceTestSets -v` で 4.3 の 20 組をすべて確かめます。

> 手順書 [「認証ベクトルを手で確かめる」](../../../docs/guide/ja/volte.md#認証ベクトルを手で確かめる)。TS 33.102 6.3.2。TS 35.208 4.3.1。

---

## ESP を使わない sec-agree

- 標準では、401 のあとのメッセージをすべて IPsec の ESP で保護します（TS 33.203 7.1）。
- このプロジェクトは、アソシエーションを取り決めて保存し、保護ポートも使います。ESP だけをかけません（`ealg=null`）。キャプチャを読めるようにするためです。
- 保護しない IMS-AKA は標準にありません。これは独自の簡略化です。IPsec を使わない形は、TLS の上の AKAv2-SHA-256 だけです（TS 24.229 4.2B.1）。

| 本物の網と違う点 | 理由 |
| --- | --- |
| 保護クライアントポート（`port-c`）を使わない | どちらも保護サーバーポートから送る |
| TCP の要求は Via の sent-by で UE を見分ける | INVITE と、1300 バイトを超える REGISTER は TCP になる（RFC 3261 18.1.1） |

- ESP が無いので、VoLTE の UE の SIP もソフトフォンと同じように Wireshark で読めます。鍵を読み込む必要もなく、コンテナに NET_ADMIN も要りません。アソシエーションは IK と CK と一緒に P-CSCF の SA の表に入ります。
- 電話機は AKAv2-SHA-256 で答えます。この版のパスワードは RES と IK と CK を結び付けた値です（RFC 4169 2.1）。S-CSCF を確かめる独立した相手の SIPp は、最初の REGISTER で AKAv1-MD5 を指定するので、challenge もその 1 つになります。SIPp は RES と Digest を別の人が書いたコードで計算します。

> 手順書 [「ESP を使わない sec-agree」](../../../docs/guide/ja/volte.md#esp-を使わない-sec-agree)

---

# Rx でのポリシー制御と VoLTE の通話

P-CSCF はポリシーと課金の制御の AF（application function）です。アクセス網が何を運ぶかは PCRF が決めます（TS 29.214 4）。

---

## 信号の経路の AAR（#786）

```ims-scene
id: signalling-path
nodes: [volte10, epc, pcscf, ue2]
lead: 登録した端末ごとに、Rx のセッションが 1 つできます。attach していない端末にはできません。
steps:
  - id: aar
    title: 登録の直後の AAR
    only: [aar-sig]
    checkpoint: true
    text: |
      Media-Component-Description は 1 つで、番号はどちらも 0 です。
      メディアではなく信号の流れだと、`Flow-Usage` が言っています。
  - id: aaa
    title: PCRF がアドレスで端末を見つける
    show: [aaa-sig]
    checkpoint: true
    text: Framed-IP-Address で IP-CAN セッションと結び付け、bearer を失ったら知らせると答えます。
  - id: softphone
    title: attach していないソフトフォンが登録する
    only: [reg-ue2]
    text: ue2 は EPC スタブを通りません。登録そのものはこれまでの回と同じように成功します。
  - id: notfound
    title: PCRF は IP-CAN セッションを見つけられない
    show: [aar-ue2, aaa-ue2]
    active: aaa-ue2
    detail: [aar-ue2, aaa-ue2]
    checkpoint: true
    text: 同じ AAR を送っても、このアドレスの PDN 接続がありません。
  - id: nopolicy
    title: P-CSCF はその端末をポリシー制御なしで扱う
    show: [pcscf-nopolicy]
    text: |
      そのため、ソフトフォンの登録のたびに、この組がキャプチャに出ます。
      前の回までのキャプチャにもあります。
```

| AVP | 値 |
| --- | --- |
| Media-Component-Number、Flow-Number | 0、0 |
| `Flow-Usage` | AF_SIGNALLING |
| `Specific-Action` | 2 は INDICATION_OF_LOSS_OF_BEARER、4 は INDICATION_OF_RELEASE_OF_BEARER |
| `Framed-IP-Address` | 172.28.0.120。これで IP-CAN セッションを見つける |

- 登録の直後の AAR です。attach していない UE（ue2、pjsua）には、PCRF が 5065（IP-CAN_SESSION_NOT_AVAILABLE）で答え、P-CSCF はその UE をポリシー制御なしで扱います。前の回までのキャプチャでも、ソフトフォンが登録するたびにこの組があります。
- Rx のセッションは、それぞれ別の Diameter のセッションです。信号の経路には登録した UE ごとに 1 つ、メディアには通話と側ごとに 1 つです。

フィルタは `diameter.Flow-Usage == 2` と `diameter.Experimental-Result-Code == 5065` です。

> 手順書 [「信号の経路」](../../../docs/guide/ja/volte.md#信号の経路)。TS 29.214 4.4.5、A.4。

---

## 通話のメディアの AAR

```ims-scene
id: media
view: ladder
nodes: [volte10, volte12, pcscf, epc]
lead: 音声の通り道は SDP の answer を見た P-CSCF が頼んで作られます。
steps:
  - id: answer
    title: 183 が answer を運ぶ
    only: [inv-p-12, r183-12-p]
    active: r183-12-p
    detail: [r183-12-p]
    checkpoint: true
    text: |
      INVITE は通話の回と同じ経路で届きます。図は両側の S-CSCF と MMTel AS を省いています。
      183 の answer で、どのコーデックをどのポートで使うかが決まります。
  - id: aar
    title: 着信側の P-CSCF が資源を求める（AAR）
    show: [aar-12]
    checkpoint: true
    text: |
      どの 2 つのアドレスの間にどれだけの帯域が要るかを SDP から組み立てて渡します。
      offer と answer の m= の行は `Codec-Data` にそのまま入ります。
  - id: grant
    title: PCRF が音声を許可する
    show: [pcrf-grant]
    text: 許可するのは音声だけで、種類は QCI 1 です。帯域は P-CSCF が求めた値から決まります。
  - id: aaa
    title: AAA は bearer より先に返る
    show: [aaa-12]
    checkpoint: true
    text: 規則を入れる前か、入れるのと並行して答えます（TS 29.214 4.4.1）。
  - id: bearer
    title: スタブが dedicated bearer を作る
    show: [bearer-12]
    checkpoint: true
    text: |
      イベントストリームに届きます。TFT の 4 つのフィルタは端末と中継ノードの間の RTP と RTCP です。
      default bearer は残り、音声の bearer がもう 1 本増えます。
      スタブは AAA を返してから bearer を作るので、P-CSCF が 183 を先へ送るのと前後します。
  - id: reserved
    title: 端末が資源を確保したとみなす
    show: [ue12-reserved]
    text: 着信側はここでプリコンディションの自分の区間を満たします。呼び出しはまだ始めません。
  - id: caller
    title: 同じ answer が発信側でも AAR になる
    show: [aar-10, aaa-10]
    active: aar-10
    detail: [aar-10]
    checkpoint: true
    text: |
      183 は同じ P-CSCF をもう一度、こんどは発信側として通ります。
      1 回の通話で、メディアの Rx のセッションが 2 つできます。信号の経路のセッションとは別です。
  - id: caller-bearer
    title: 発信側にも音声の bearer ができる
    show: [bearer-10, ue10-reserved]
    active: ue10-reserved
    checkpoint: true
    text: フィルタの相手のアドレスはもう一方の端末ではなく中継ノードです。この bearer と次の 183 は、どちらが先に端末に届くこともあります。
  - id: forward
    title: AAA が返ると 183 が発信側に届く
    show: [r183-p-10]
    text: P-CSCF は AAR に答えが返るまで 183 を渡しません。待つのは AAA だけで、bearer ができるのは待ちません。許可されなければ、通話をここで終わらせます。
```

両側の P-CSCF は、SDP の answer がそろうと AAR を送ります。着信側は volte12 からの 183、発信側は volte10 に向かう 183 がきっかけです。

| AVP | 着信側の AAR（#1036）の値 |
| --- | --- |
| `AF-Application-Identifier` | `urn:urn-7:3gpp-service.ims.icsi.mmtel` |
| `Media-Sub-Component` | フロー 1 と 2。RTP と RTCP の `Flow-Description` |
| `Max-Requested-Bandwidth-UL`、`-DL` | 80000 と 41000。b=AS から。単位は bit/s |
| `Flow-Status` | `ENABLED`（2）。sendrecv から |
| `Codec-Data` | offer と answer の m= の行と属性 |
| `Specific-Action` | 2、4、9。bearer の喪失、解放、割り当ての失敗 |
| `Rx-Request-Type` | INITIAL_REQUEST。以後の offer と answer は UPDATE_REQUEST |

> 手順書 [「通話のメディア」](../../../docs/guide/ja/volte.md#通話のメディア)。TS 29.214 A.1。TS 29.213 6.2 Table 6.2.1、6.3。

---

## 通話のメディアの AAR を読む

- フィルタは `diameter.Codec-Data` です。AAR を開き、Media-Component-Description を順に見ます。番号 1 が最初の m= の行です。
- Flow-Description の 1 つは `permit in 17 from 172.28.0.121 to 172.28.0.50 40004` です。メディアの相手は、もう一方の UE ではなく中継ノードです。
- AF-Application-Identifier を送るのは着信側の P-CSCF だけです。発信側は Feature-Caps から取るはずですが、それを書くノードがありません。簡略化の一覧に入っています。
- Service-Info-Status は FINAL_SERVICE_INFORMATION です。情報が offer だけでなく answer に基づいているからです。
- 保留すると、発信側の Flow-Status は `ENABLED-UPLINK`（0）、着信側は `ENABLED-DOWNLINK`（1）になります。

---

## 音声を運ぶ dedicated bearer

PCRF が音声を許可すると、スタブがイベントストリームで送ります。

```text
event: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
data: {"eps_bearer_identity":6,"linked_eps_bearer_identity":5,
       "eps_qos":{"qci":1,"maximum_bit_rate_for_uplink":80, ... },
       "tft":{"tft_operation_code":"create new TFT","packet_filters":[ ... ]}}
```

- QCI 1 は、帯域を保証する会話音声の種類です。速度は kbit/s で、P-CSCF が求めた帯域から決まります。
- TFT のフィルタは 4 つです。UE と中継ノードの間の RTP と RTCP の上りと下りです。
- UE は `activate-dedicated-eps-bearer-context-accept` で答え、通話の資源とみなします。通話が終わると DEACTIVATE EPS BEARER CONTEXT REQUEST が来ます。
- default bearer は 5、音声の bearer は 6 です。どちらも同じ PDN 接続に属するので、`linked_eps_bearer_identity` は 5 です。中継ノードが無ければ、フィルタの相手のアドレスはもう一方の UE になります。
- フィルタは `frame contains "EPS BEARER CONTEXT"` です。イベントは 1 つの HTTP の応答の中で少しずつ届きます。

> 手順書 [「bearer」](../../../docs/guide/ja/volte.md#bearer)。TS 24.229 L.2.2.5.1。TS 29.213 6.3。IR.92 4.4.2、2.4.3.1。

---

## プリコンディション（資源がそろうまで鳴らさない）

```ims-scene
id: precondition
view: ladder
nodes: [volte10, pcscf, volte12]
lead: 着信側が鳴り始めるのは、両方の区間の資源がそろってからです。
steps:
  - id: invite
    title: INVITE が offer を運ぶ
    only: [inv-10-p, inv-p-12]
    active: inv-10-p
    detail: [inv-10-p]
    checkpoint: true
    text: |
      `curr:qos local` が `none` です。発信側はまだ音声の bearer を持っていません。
      このしくみは Supported で申し出るだけで、Require には入れません。
  - id: answer
    title: 183 が answer を返し、知らせを求める
    show: [r183-12-p]
    checkpoint: true
    text: |
      着信側は発信側の区間も mandatory に上げ、資源がそろったら知らせてほしいと求めます。
      自分の資源はまだ `none` です。
  - id: resources
    title: AAA が返ると 183 が先へ進む
    show: [r183-p-10]
    text: |
      このあいだに、前のシーンの 2 つの AAR が入ります。P-CSCF が待つのは AAA だけで、bearer ができるのは待ちません。
      発信側の資源がそろったことは、このあとの UPDATE で伝えます。
  - id: prack
    title: PRACK で 183 の受信を確認する
    show: [prack, prack-ok]
    active: prack-ok
    detail: [prack]
    text: 183 は信頼性のある暫定応答なので、確認が要ります。UPDATE はこの 200 を待ちます。
  - id: update
    title: UPDATE が自分の区間を伝える
    show: [update-10-p, update-p-12]
    active: update-10-p
    detail: [update-10-p]
    checkpoint: true
    text: |
      発信側は dedicated bearer を受け取ったので、`curr:qos local` が `sendrecv` になります。
      状態の更新に使うのは UPDATE です。
  - id: both
    title: 200 で両方の区間がそろう
    show: [update-ok-12]
    checkpoint: true
    text: 着信側から見た local と remote が、どちらも `sendrecv` になりました。
  - id: alert
    title: ここで初めて呼び出しが始まる
    show: [pre-met, r180-12-p]
    active: r180-12-p
    detail: [pre-met, r180-12-p]
    checkpoint: true
    text: 必須の行がすべて current になったので、着信側は 180 を返します。
  - id: caller
    title: 発信側では 2 つの応答が行き違う
    show: [update-ok-10, r180-p-10]
    active: r180-p-10
    checkpoint: true
    text: |
      P-CSCF は新しい answer の AAR のあいだ 200 を止めるので、180 が先に届くことがあります。
      フレームの番号で順番を確かめます。
  - id: answered
    title: 応答と ACK で通話が確定する
    show: [ok-inv-12, ok-inv-10, ack]
    active: ack
    detail: [ok-inv-12]
    text: 200 (INVITE) に SDP はありません。answer は信頼性のある 183 で届いています。
```

```ladder
volte10, P-CSCF, PCRF, volte12
volte10 -> P-CSCF: INVITE // curr:qos local none
P-CSCF -> volte12: INVITE。各 S-CSCF と AS を通る
P-CSCF <-- volte12: 183 // conf:qos remote sendrecv
P-CSCF <-> PCRF: 両側の AAR と AAA // bearer を 1 つずつ
volte10 <-- P-CSCF: 183。続いて PRACK とその 200
volte10 -> P-CSCF -> volte12: UPDATE // curr:qos local sendrecv
volte10 <-- P-CSCF <-- volte12: 200 (UPDATE) // 両方の区間が sendrecv
volte10 <-- P-CSCF <-- volte12: 180 // ここで初めて
volte10 <-- P-CSCF <-- volte12: 200 (INVITE)。続いて ACK
```

- INVITE は `Supported: 100rel, timer, 199, precondition` です。Require には入れません。183 は `Require: 100rel, precondition` です。UPDATE は、PRACK に 200 が返ってから送ります。
- 発信側では、UPDATE への 200 より先に 180 が来ることがあります。P-CSCF が新しい answer の AAR のために、その 200 を止めておくからです。フィルタは `sip.Require contains "precondition"`、プリコンディションの行をすべて探すなら `sdp.media_attr contains "qos"` です。

> 手順書 [「プリコンディション」](../../../docs/guide/ja/volte.md#プリコンディション)。RFC 3312。IR.92 2.4.1。TS 24.229 5.1.3.1。シーケンス図の全体とフレームの番号は手順書にあります。

---

## プリコンディションの行を読む

| 段階 | 送った側 | `curr` local | `curr` remote | `des` の強さ（local、remote） | `conf` |
| --- | --- | --- | --- | --- | --- |
| INVITE | volte10 | none | none | mandatory、optional | |
| 183 | volte12 | none | none | mandatory、mandatory | remote |
| UPDATE | volte10 | sendrecv | none | mandatory、mandatory | |
| 200 (UPDATE) | volte12 | sendrecv | sendrecv | mandatory、mandatory | |

- 値は SDP を送った側から見たものです。発信側は segmented status type です（TS 24.229 6.1.2）。
- 着信側は 183 で発信側の区間を mandatory に上げ、`conf:qos remote sendrecv` で知らせを求めます（RFC 3312 7）。183 の local と remote は volte12 から見たもので、volte12 の remote は volte10 の区間です。
- UE は dedicated bearer を受け取ると、資源を確保したとみなします（TS 24.229 L.2.2.5.1B、IR.92 2.4.3.1）。UPDATE がそれを伝えます。ue2 のように EPC スタブを使わない UE は、最初から確保済みです。向きはどの行も sendrecv です。

> 手順書 [「プリコンディション」](../../../docs/guide/ja/volte.md#プリコンディション) の表。RFC 3312 6。

---

## 呼び出しの条件をコードで見る

[internal/precondition/table.go](../../../internal/precondition/table.go)

```go
func (t *Table) Met() bool {
	for _, r := range t.rows {
		if r.Strength == Mandatory && !r.Current {
			return false
		}
	}
	return true
}
```

- 表の行は、区間（local と remote）と向き（send と recv）の組です。
- 着信側の UE は、この関数が true になるまで呼び出しを始めません。関数のすぐ上のコメントが RFC 3312 6 を指しています。
- 同じファイルの `NeedsOffer` は、UE 自身の資源が変わったときに新しい offer（UPDATE）が要るかを決めます。発信側の UPDATE はここから出ます。表を使うのは `internal/ue/session.go` です。

> 手順書 [「コードの場所」](../../../docs/guide/ja/volte.md#コードの場所)。RFC 3312 5、6。

---

## コーデックの offer と answer

```text
m=audio 53946 RTP/AVP 96 97 98 99 0 100 101
b=AS:80
b=RS:0
b=RR:4000
a=rtpmap:96 AMR-WB/16000/1
a=fmtp:96 mode-change-capability=2; max-red=0
a=rtpmap:97 AMR-WB/16000/1
a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1
```

- 96 と 98 は bandwidth-efficient、97 と 99 は octet-aligned です。発信側は前者を優先します（IR.92 3.2.5）。mode-set は付けません（IR.92 2.4.3.2）。
- PCMU を最後に残します。PCMU しか知らないソフトフォンとも話せるようにするためです。b=AS:80 は PCMU に合わせた値で、上りの帯域もこの値になります。
- volte12 は `m=audio 60734 RTP/AVP 96 100` と `b=AS:41` で答えます。PCMU しか知らない ue2 は `RTP/AVP 0 101` で答え、VoLTE の UE は PCMU で話します。
- b=RS:0 と b=RR:4000 は、送信者と受信者の RTCP の帯域で、単位は bit/s です（RFC 3556 2）。UE はこれから報告の間隔を計算します（RFC 3550 6.3.1）。5 秒より短くはなりません。

> 手順書 [「offer と answer」](../../../docs/guide/ja/volte.md#offer-と-answer)。offer の全体は手順書にあります。AMR が 98 と 99、PCMU が 0、telephone-event が 100 と 101 で、telephone-event は音声のコーデックごとのクロックレートです（TS 26.114 Annex G）。ptime は 20、maxptime は 240 です。RFC 4867。

---

## AMR のフレーム、RTCP、DTMF

```text
Adaptive Multi-Rate
    [Payload decoded as: RFC 3267 bandwidth-efficient mode (1)]
    1111 .... = CMR: No mode request (15)
    .... 0... = F bit: Last frame in this payload
    .... .100  0... .... = FT bits: AMR-WB 23.85 kbit/s (8) / Frame OK
    .1.. .... = Q bit: Ok
```

- 1 つのパケットに 20 ms のフレームが 1 つです。CMR、目次（F、FT、Q）、音声のビットの順です（RFC 4867 4.3）。
- UE は AMR を符号化も復号もしません。RFC 4867 5 の格納形式のフレームを送ります。送ったものと受けたものは、ビット単位で比べられます。
- RTCP は RTP の 1 つ上のポートです（RFC 3550 6.4.1、IR.92 3.2.4）。DTMF は telephone-event です（RFC 4733）。フィルタは `amr`、`rtcp`、`rtpevent` です。

> 手順書 [「フレーム」](../../../docs/guide/ja/volte.md#フレーム) と [「RTCP と DTMF」](../../../docs/guide/ja/volte.md#rtcp-と-dtmf)

---

## Wireshark で AMR のフレームを比べる

- Wireshark は、見た SDP に従って AMR-WB のフレームをデコードします。SDP が無いときは、**Preferences > Protocols > AMR** で Wideband AMR とペイロードの形式を選び、RTP のポートに **Decode As** を使います。
- `amr.wb.cmr` と `amr.wb.toc.ft` で、AMR-WB の CMR とフレームの種類を絞れます。
- 比べるフレームは `-sent-out` と `-received-out` で書き出します。e2e のスクリプトがそれを比べます。
- 送るフレームは `internal/ue/speech` に埋め込んだトーンです。製品のビルドの外にある cgo のツール `tools/amrgen` で作りました。
- DTMF の 1 桁はパケットの連なりで、最後の 3 つに終わりのビットが立ちます。

---

## セッションタイマー、Reason、早期メディア、保留

| 項目 | キャプチャで見るもの |
| --- | --- |
| セッションタイマー | INVITE の `Session-Expires: 1800`。200 の `refresher=uac`。発信側が 900 秒ごとに UPDATE。 |
| UE の Reason | BYE の `RELEASE_CAUSE;cause=1;text="User ends call"`。 |
| 早期メディア | INVITE の `P-Early-Media: supported`。許可するノードは無い。 |
| 保留と再開 | `a=sendonly` の re-INVITE と `a=recvonly` の 200。両方の Rx が更新される。 |

フィルタは `sip.Session-Expires` と `sip.Reason` です。

- メディアの bearer を失って終わった通話では、UE の Reason の cause は 3 です。P-CSCF の BYE は `Reason: SIP;cause=503` です。早期メディアを許可するノードが無いので、発信側は 180 のあと自分で呼び出し音を鳴らします。
- 保留の re-INVITE は、そのまま MMTel AS を通ります。保留と DTMF のある通話は、e2e のスクリプトがラボから動かすものです。

> 手順書 [「セッションタイマー、Reason、早期メディア」](../../../docs/guide/ja/volte.md#セッションタイマーreason早期メディア) と [「保留、再開、DTMF」](../../../docs/guide/ja/volte.md#保留再開dtmf)。IR.92 2.2.8、2.2.4、2.2.7。RFC 4028 9。TS 24.229 7.2A.18.11。RFC 5009。TS 24.610 4.5.2.1。

---

## 網が bearer を落とす

```ims-scene
id: release
view: ladder
nodes: [volte10, volte12, pcscf, scscf, epc]
lead: 端末が切らなくても、資源を取り上げた網が通話を終わらせます。
steps:
  - id: deactivate
    title: 網が音声の bearer を落とす
    only: [epc-deact]
    checkpoint: true
    text: |
      無線が切れたときと同じ、網が始める非活性化です。
      始めるのはラボのボタンか、詳しい説明の `curl` です。図は網の中だけを描いています。
  - id: tell
    title: 端末に非活性化を知らせる
    show: [deact-10]
    text: イベントストリームに届きます。volte10 は自分では BYE を送りません。
  - id: asr
    title: PCRF が Rx のセッションを中止する（ASR）
    show: [asr, asa]
    active: asr
    detail: [asr]
    checkpoint: true
    text: |
      失ったフローを RAR で知らせるのではなく、セッションごと終わらせます。
      P-CSCF は ASA で答えます。
  - id: str
    title: P-CSCF も STR でセッションを終える
    show: [str]
    text: 普通の BYE で終わる通話でも、両側の P-CSCF が STR を送ります。Termination-Cause は別の値です。
  - id: build
    title: 保存したダイアログから BYE を作る
    show: [pcscf-bye]
    checkpoint: true
    text: 端末が送るはずだった BYE を P-CSCF が代わりに組み立てます。
  - id: remote
    title: 相手の側へ、サービスを受ける端末の名前で
    show: [bye-core]
    checkpoint: true
    text: Reason で理由が分かります。`cause=503` は、サービスが使えなくなったことを表します。
  - id: ue
    title: サービスを受ける端末にも BYE
    show: [bye-10]
    checkpoint: true
    text: |
      この BYE は、このプロジェクトが足したものです。
      音声の bearer だけを失った端末は信号の経路を持ったままなので、知らせないと通話に残り続けます。
  - id: far
    title: BYE が着信側に届く
    show: [bye-12]
    text: 相手側へは S-CSCF と MMTel AS を通ってから届きます。Reason は同じです。
  - id: release12
    title: 着信側の bearer も解放される
    show: [deact-12]
    text: volte12 の通話が終わると、その側の Rx のセッションも終わり、スタブが bearer を解放します。
```

```sh
curl -X DELETE http://localhost:8080/api/epc/network-initiated/ues/001010000000010/bearers/6
```

```ladder
volte10, P-CSCF, S-CSCF, volte12, EPC と PCRF, ラボ
EPC と PCRF <- ラボ: DELETE .../bearers/6
volte10 <- EPC と PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST（イベントストリーム）
P-CSCF <- EPC と PCRF: ASR、Abort-Cause BEARER_RELEASED（0） // 続いて ASA
P-CSCF -> EPC と PCRF: STR // 続いて STA
P-CSCF -> S-CSCF: volte10 の名前の BYE // cause=503
volte10 <- P-CSCF: BYE // cause=503
P-CSCF -> volte12: BYE。S-CSCF と MMTel AS を通ったあと
volte12 <- EPC と PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST
```

> 手順書 [「網が bearer を落とす」](../../../docs/guide/ja/volte.md#網が-bearer-を落とす)。TS 29.214 4.4.6.1、4.4.6.2。TS 24.229 5.2.8.1.2。IR.92 2.4.2.3。

---

## bearer が落ちたときの見え方

- 音声の bearer がメディアをすべて運んでいたので、PCRF は RAR ではなく ASR で Rx のセッションを終えます。volte10 は自分では BYE を送りません。
- P-CSCF は `Store` に置いたダイアログの情報から BYE を作ります。相手の側への BYE は、サービスを受ける利用者の名前で送ります。
- サービスを受ける UE への BYE は、このプロジェクトが足したものです。音声の bearer だけを失った UE は信号の経路を持ったままなので、BYE が無いと、網が終えた通話に残り続けます（`internal/pcscf/release.go` のコメント）。
- 普通の BYE で終わる通話では、両側の P-CSCF が STR で Rx のセッションを終えます。Termination-Cause は DIAMETER_LOGOUT です（TS 29.214 4.4.4）。
- フィルタは `diameter.Abort-Cause` です。Rx の全体は `diameter.cmd.code in {265, 258, 274, 275}` で、AAR と AAA、RAR と RAA、ASR と ASA、STR と STA です。この実行では RAR と RAA は出ません。

---

## IR.92 が求めること

| IR.92 | 内容 | このプロジェクト |
| --- | --- | --- |
| 4.4.1、4.4.2、4.5 | 信号は QCI 5、音声は QCI 1。P-CSCF は PCO で | EPC スタブ |
| 2.4.1 | プリコンディション。状態の更新は UPDATE | 対応 |
| 2.4.2.1、2.4.2.3 | bearer を失ったら網がセッションを終える | P-CSCF の BYE |
| 3.2.1、3.2.5、2.4.3.2 | AMR と AMR-WB。2 つのペイロード形式 | 符号化済みのフレーム |
| 3.2.4、2.2.4、2.2.8 | RTCP、Reason、セッションタイマー | 対応 |

- 作っていないもの。登録の Contact の mmtel の ICSI、+sip.instance、audio のタグ。CMR に従うこと。IPsec の ESP。IPv6。理由は次のスライドにあります。

---

## 作っていないもの

- REGISTER の Contact には ICSI も +sip.instance も audio のタグもありません。INVITE とその応答の Contact には ICSI と audio があります。
- CMR に従うには、符号化できる UE が要ります。この UE は、前もって符号化したフレームを送ります。
- ESP は、キャプチャを読めるようにするために省きました。
- どのノードも IPv4 で、1 つの Docker のネットワークにいます。
- 国の標準（日本では TTC）は網どうしの接続を扱うので、1 つの網の中の通話の形は変えません。セッションタイマーの長さのような違いは、接続する相手の網ごとのプロファイルに置きます。網間接続の手順書で扱います。

> 手順書 [「VoLTE の端末とソフトフォンの違い」](../../../docs/guide/ja/volte.md#volte-の端末とソフトフォンの違い) と [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/volte.md#このプロジェクトで簡略化していること)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| EPC スタブ | NAS の符号化、S1-AP、無線、GTP-U、Gx、attach の認証が無い。bearer は記録だけ。 |
| PCRF | 許可するのは音声だけで、QCI 1。DRA を使わない。 |
| P-CSCF、UE、S-CSCF | ESP をかけない。 |
| P-CSCF | bearer を失うとすぐに解放する（通話ごとに音声の流れは 1 つだから）。BYE なしで終わったダイアログは `Store` に残る（UE ごとのダイアログの索引を持たず、複数の P-CSCF が同じ利用者を受け持てるようにするため）。 |
| UE と AMR | CMR と mode-set に従わない。PDN 接続を失うと止まる。 |

```sh
grep -rn "SIMPLIFIED:" internal/epc internal/pcscf internal/ue internal/aka internal/amr internal/rtcp internal/precondition
```

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/volte.md#このプロジェクトで簡略化していること)。項目ごとの仕様の条項と理由は手順書の表にあります。

---

## ラボで動かす

```sh
docker compose up -d --build
docker compose --profile volte up -d volte10 volte12
curl http://localhost:8080/api/epc/ues/001010000000010
```

1. <http://localhost:8080> を開きます。volte10 と volte12 のカードに、登録済みと出ます。
2. volte10 から `tel:+12025550112` に発信します。volte12 は呼び出し時間のあとで自分で応答します。
3. カードの保留、再開、DTMF のボタンを押します。コーデックとプリコンディションの表も出ます。
4. 前のスライドの `curl -X DELETE` で音声の bearer を解放します。

- 通話のシーケンス図には、P-CSCF の隣に PCRF の列があります。
- `curl` は volte10 の bearer を並べます。通話中は 5 と 6 です。ラボは、UE の状態と、網の手順を始める EPC スタブのパスを、`/api/epc` の下で中継します。

> 手順書 [「ラボで」](../../../docs/guide/ja/volte.md#ラボで)

---

## コマンドラインで動かす

```sh
docker compose --profile volte rm -sf volte10 volte12
docker compose run --rm volte12 answer -epc http://172.28.0.80:8080 \
  -impi 001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3 \
  -received-out /captures/volte12-received.amr
docker compose run --rm volte10 call -epc http://172.28.0.80:8080 \
  -impi 001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf \
  -sent-out /captures/volte10-sent.amr tel:+12025550112
```

- `-epc` で attach し、`-k` と `-opc` で IMS-AKA を使います。
- `-codecs` で offer のコーデックを選べます。`-no-preconditions` はプリコンディションを使いません。
- 1 行目は、プロファイルの UE を止めます。この 2 つのコマンドと volte10、volte12 のアドレスが同じだからです。volte12 と volte10 は別々の端末で動かします。SIPp を IMS-AKA で登録するコマンドは手順書にあります。

> 手順書 [「コマンドラインで」](../../../docs/guide/ja/volte.md#コマンドラインで)。フラグの一覧は手順書の表にあります。

---

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Security-Client \|\| sip.Security-Server \|\| sip.Security-Verify` | sec-agree |
| `diameter.3GPP-SIP-Authentication-Scheme == "Digest-AKAv1-MD5"` | IMS-AKA の MAR、MAA |
| `diameter.cmd.code in {265, 258, 274, 275}` | Rx。AAR と AAA、RAR と RAA、ASR と ASA、STR と STA |
| `diameter.Flow-Usage == 2`、`diameter.Codec-Data` | 信号の経路と通話の AAR |
| `http && ip.addr == 172.28.0.80` | EPC スタブの API |
| `sip.Require contains "precondition"` | プリコンディションの 183 |
| `amr`、`rtcp`、`rtpevent` | AMR、RTCP、DTMF |

- 手順書は、これらのフィルタを tshark 4.2 で e2e のスクリプトのキャプチャに当てて確かめています。

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/volte.md#wireshark-の表示フィルタ)。表の全体は手順書にあります。

---

## コードで確かめる

```ims-scene
id: code
nodes: [volte10, epc, volte12, pcscf, scscf, hss]
lead: この回で増えたものは、ノードごとに読む場所が分かれています。
steps:
  - id: epc
    title: アクセス網のスタブと PCRF
    focus: [epc]
    text: "`internal/epc` の `http.go`、`api.go`、`network.go`、`pcrf.go`"
  - id: access
    title: 端末から見たアクセス網
    focus: [volte10, volte12]
    text: "`internal/ue/access.go`、`access_epc.go`"
  - id: vector
    title: MILENAGE と認証ベクトル
    focus: [hss]
    checkpoint: true
    text: "`internal/milenage`、`internal/aka`"
  - id: aka
    title: IMS-AKA の問いと sec-agree
    focus: [scscf, pcscf, volte10]
    text: "`internal/scscf/aka.go`、`internal/pcscf/secagree.go`、`internal/ue/aka.go`"
  - id: policy
    title: ポリシー制御と Rx
    focus: [pcscf, epc]
    checkpoint: true
    text: "`internal/policy`、`internal/rx`、`internal/pcscf/policy.go`"
  - id: release
    title: 網による解放
    focus: [pcscf]
    text: "`internal/pcscf/release.go`、`dialog.go`"
  - id: media
    title: プリコンディション、AMR、RTCP
    focus: [volte10, volte12]
    text: "`internal/precondition`、`internal/amr`、`internal/rtcp`"
```

| 見たもの | 開く場所 |
| --- | --- |
| EPC スタブと PCRF | `internal/epc` の `http.go`、`api.go`、`network.go`、`pcrf.go` |
| UE のアクセス網 | `internal/ue/access.go`、`access_epc.go` |
| MILENAGE と認証ベクトル | `internal/milenage`、`internal/aka` |
| IMS-AKA の challenge と sec-agree | `internal/scscf/aka.go`、`internal/pcscf/secagree.go`、`internal/ue/aka.go` |
| ポリシー制御と Rx | `internal/policy`、`internal/rx`、`internal/pcscf/policy.go` |
| 網による解放 | `internal/pcscf/release.go`、`dialog.go` |
| プリコンディション、AMR、RTCP | `internal/precondition`、`internal/amr`、`internal/rtcp` |

- `internal/ue/volte_epc_test.go` と `volte_aka_test.go` は、VoLTE の通話を IMS 全体を通して 1 つのプロセスで動かします。
- `internal/policy` の `PolicyControl` の 3 つのメソッド、`SubscribeSignallingPath`、`AuthorizeMedia`、`Release` を、この回の AAR と STR と照らし合わせます。

> 手順書 [「コードの場所」](../../../docs/guide/ja/volte.md#コードの場所)

---

[layout: exercise]

## 演習

1. 401 の nonce を base64 で戻し、MAA の SIP-Authenticate と比べます。AUTN の中の AMF はどこにありますか。
2. 着信側の通話の AAR の `Flow-Description` を読み、どの 2 つのアドレスの間のフローかを言います。
3. volte12 の 180 と、UPDATE への 200 のフレームの番号を比べます。
4. 保留したあとの AAR で、両側の `Flow-Status` を読みます。
5. volte10 から ue2 に発信します。answer のコーデックと、通話の AAR があるかを確かめます。
6. bearer を解放したあとの BYE を探し、誰が送ったか、Reason が何かを読みます。

1 には、手順書の「認証ベクトルを手で確かめる」のコマンドを使います。同じ節で、2 回目の REGISTER の response をシェルで計算します。

---

[layout: answers]

## 演習の答え

1. 最初の 16 オクテットが RAND、残りが AUTN です。AUTN の 7 と 8 オクテット目が AMF で、0000 です。
2. UE（172.28.0.121）と中継ノード（172.28.0.50）の間です。もう一方の UE ではありません。
3. 180 のほうがあとです。着信側は、mandatory のプリコンディションがすべてそろってから呼び出します。
4. 発信側が `ENABLED-UPLINK`（0）、着信側が `ENABLED-DOWNLINK`（1）です。
5. PCMU です。ue2 は attach していないので、ue2 の側には通話の AAR がありません。volte10 の側の AAR を探します。
6. P-CSCF が `Reason: SIP;cause=503` で送ります。volte10 は自分では BYE を送りません。

---

[layout: exercise]

## 振り返り

1. UE は、P-CSCF のアドレスをどこから知りますか。
2. 401 の `ik` と `ck` が UE に届かないのは、なぜですか。
3. UE が網を確かめるのは、どの値を使ってですか。
4. P-CSCF が通話の AAR を送るのは、どの応答を見たときですか。
5. RAR ではなく ASR が来たのは、なぜですか。
6. attach していないソフトフォンが登録すると、Rx では何が起きますか。

---

[layout: answers]

## 振り返りの答え

1. ATTACH ACCEPT の PCO にある、識別子 `000CH` のコンテナからです（TS 24.229 L.2.2.1 の方法 II）。
2. P-CSCF が取り除くからです。IK と CK はアソシエーションの鍵で、UE は自分の K と RAND から同じ鍵を計算するので、送る必要がありません。
3. AUTN の MAC と SQN です。MAC が合えば、網が K を知っていることが分かります。
4. SDP の answer を持ったときです。着信側では volte12 からの 183、発信側では volte10 へ向かう 183 です。
5. 音声の bearer がそのセッションのメディアをすべて運んでいたので、PCRF が Rx のセッション全体を終えるからです。
6. PCRF が信号の経路の AAR に 5065（IP-CAN_SESSION_NOT_AVAILABLE）で答え、P-CSCF はその UE をポリシー制御なしで扱います。

---

## 次の回

- 次が最後の回です。境界でつながる 2 つの IMS 網を扱います。通話、ショートメッセージ、会議が、IBCF と TrGW を通ってほかの事業者の網へ出ていきます。
- この回の 2 つの境界（UE の `AccessNetwork` と P-CSCF の `PolicyControl`）は、ほかのアクセス網をつなぐ場所です。前者の後ろには NAS を、後者の後ろには 5GC の N5 を置けます。

```sh
docker compose --profile ue --profile volte down --rmi all -v --remove-orphans
```

- これは手順書の「後片付け」です。ラボの `phones` のプロファイルも使ったなら、`--profile phones` を足します。
- 5GC では、SIP の信号は 5QI 5 の QoS フローを、音声は 5QI 1 を使います。EPS の QCI 5 と QCI 1 と同じ考え方です。

> 次の回の手順書は [IMS 網どうしを相互接続する](../../../docs/guide/ja/interconnect.md) です。手順書の一覧は [docs/guide/](../../../docs/guide/) にあります。
