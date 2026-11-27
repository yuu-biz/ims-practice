---
title: MRF による音声ガイダンス、呼び出し音、会議
footer: ims-practice 勉強会 8. MRF
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: dns, label: DNS, role: 名前を引く, at: [1, 0]}
  - {id: hss, label: HSS, role: 加入者のデータ, at: [2, 0]}
  - {id: focus, label: MMTel AS, role: 会議のフォーカス, at: [3, 0]}
  - {id: scscf, label: S-CSCF, role: 登録とサービス, at: [1, 1]}
  - {id: mmtel, label: MMTel AS, role: 利用者のサービス, at: [2, 1]}
  - {id: mrfc, label: MRFC, role: MRF の SIP の側, at: [3, 1]}
  - {id: ue1, label: UE 1, role: 発信者, at: [0, 2]}
  - {id: pcscf, label: P-CSCF, role: 最初の中継点, at: [1, 2]}
  - {id: ue2, label: UE 2, role: 着信者, at: [0, 3]}
  - {id: agw, label: IMS-AGW, role: メディア中継ノード, at: [1, 3]}
  - {id: mrfp, label: MRFP, role: 音声を作る, at: [3, 3]}
  - {id: phone1, label: phone1, role: 会議を作る, at: [0, 2]}
  - {id: phone2, label: phone2, role: 招かれる, at: [0, 3]}
  - {id: phone3, label: phone3, role: 招かれる, at: [0, 4]}
events:
  # 参照点
  - {id: gm, rel: Gm, from: ue1, to: pcscf, proto: SIP}
  - {id: mw, rel: Mw, from: pcscf, to: scscf, proto: SIP}
  - {id: isc, rel: ISC, from: scscf, to: mmtel, proto: SIP}
  - {id: mr, rel: "Mr'", from: mmtel, to: mrfc, proto: SIP}
  - {id: mp, rel: Mp, from: mrfc, to: mrfp, proto: HTTP}
  - {id: media-ue, rel: RTP, from: ue1, to: agw, proto: RTP}
  - {id: media-mrfp, rel: RTP, from: agw, to: mrfp, proto: RTP}
  # 通話を断る前の音声ガイダンス
  - id: an-inv-ue-p
    msg: INVITE
    from: ue1
    to: pcscf
    proto: SIP
    note: (C1)
    fields:
      - Request-URI: "`tel:+12025550102`"
  - {id: an-inv-p-s, msg: INVITE, from: pcscf, to: scscf, proto: SIP}
  - {id: an-inv-s-as, msg: INVITE, from: scscf, to: mmtel, proto: SIP}
  - id: an-ocb
    do: 発信規制を当てる
    at: mmtel
    fields:
      - 発信規制: "UE 1 は `baoc` を設定している。通話を断る（TS 24.611 4.5.2.4.1）"
      - 断る前: "`notice` を流す（TS 24.628 4.2.4）"
  - id: an-annc
    msg: INVITE
    from: mmtel
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - Request-URI: "`sip:annc@mrfc...;play=/provisioned/notice`"
      - SDP: 付けない
  - id: an-annc-200
    msg: 200 OK
    from: mrfc
    to: mmtel
    proto: SIP
    fields:
      - SDP: "MRFP（172.28.0.86）の offer。PCMU、`sendonly`"
  - {id: an-183-as-s, msg: "183", from: mmtel, to: scscf, proto: SIP}
  - {id: an-183-s-p, msg: "183", from: scscf, to: pcscf, proto: SIP}
  - id: an-183-p-ue
    msg: "183"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - Require: "`100rel`"
      - P-Early-Media: "`sendonly`。early media を認める（RFC 5009）"
      - SDP: UE 1 の offer への、MRFP の SDP の answer
  - {id: an-prack-ue-p, msg: PRACK, from: ue1, to: pcscf, proto: SIP}
  - {id: an-prack-p-s, msg: PRACK, from: pcscf, to: scscf, proto: SIP}
  - {id: an-prack-s-as, msg: PRACK, from: scscf, to: mmtel, proto: SIP}
  - id: an-ack
    msg: ACK
    from: mmtel
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - SDP: "answer。UE 1 のメディアで `recvonly`"
  - id: an-play
    do: notice を流し始める
    at: mrfp
    fields:
      - きっかけ: MRFC が受けた ACK（TS 24.229 5.8.2.2.1）
      - 符号化: PCMU
  - {id: an-rtp-p-agw, msg: RTP, from: mrfp, to: agw, proto: RTP}
  - id: an-rtp-agw-ue
    msg: RTP
    from: agw
    to: ue1
    proto: RTP
    fields:
      - 中身: "`notice`。UE 1 は自分の呼び出し音の代わりにこれを流す（TS 24.628 4.7.2.1）"
  - id: an-bye
    msg: BYE
    from: mrfc
    to: mmtel
    proto: SIP
    note: (M)
    fields:
      - いつ: "ACK から 1.08 秒。`notice` の WAV ファイルの長さ（TS 24.229 5.8.3.2.1）"
  - {id: an-603-as-s, msg: "603", from: mmtel, to: scscf, proto: SIP}
  - {id: an-603-s-p, msg: "603", from: scscf, to: pcscf, proto: SIP}
  - id: an-603-p-ue
    msg: "603"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - Reason: '`SIP;cause=603;text="Decline"`'
  # CAT
  - id: cat-inv-s-as
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (着信側)
    fields:
      - 宛先: "UE 2（`tel:+12025550102`）"
  - id: cat-udr
    msg: UDR
    from: mmtel
    to: hss
    proto: Sh
    fields:
      - Service-Indication: "`Customized-Alerting-Tone`"
  - id: cat-uda
    msg: UDA
    from: hss
    to: mmtel
    proto: Sh
    fields:
      - 中身: "加入者 2 の呼び出し音の設定（`cat1`）"
  - id: cat-annc
    msg: INVITE
    from: mmtel
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - Request-URI: "`sip:annc@mrfc...;play=/provisioned/cat1;repeat=forever`"
  - id: cat-annc-200
    msg: 200 OK
    from: mrfc
    to: mmtel
    proto: SIP
    fields:
      - SDP: MRFP の offer
  - {id: cat-inv-as-s, msg: INVITE, from: mmtel, to: scscf, proto: SIP}
  - {id: cat-inv-s-p, msg: INVITE, from: scscf, to: pcscf, proto: SIP}
  - {id: cat-inv-p-ue, msg: INVITE, from: pcscf, to: ue2, proto: SIP, note: (C3)}
  - {id: cat-180-ue-p, msg: "180", from: ue2, to: pcscf, proto: SIP}
  - {id: cat-180-p-s, msg: "180", from: pcscf, to: scscf, proto: SIP}
  - {id: cat-180-s-as, msg: "180", from: scscf, to: mmtel, proto: SIP}
  - {id: cat-update-as-s, msg: UPDATE, from: mmtel, to: scscf, proto: SIP}
  - {id: cat-update-s-p, msg: UPDATE, from: scscf, to: pcscf, proto: SIP}
  - id: cat-update-p-ue
    msg: UPDATE
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - P-Early-Media: "`sendonly`"
      - SDP: "MRFP の SDP。`sendonly` と `a=content:g.3gpp.cat`（RFC 4796）"
  - {id: cat-ack, msg: ACK, from: mmtel, to: mrfc, proto: SIP, note: (M)}
  - {id: cat-rtp-p-agw, msg: RTP, from: mrfp, to: agw, proto: RTP}
  - id: cat-rtp-agw-ue
    msg: RTP
    from: agw
    to: ue1
    proto: RTP
    fields:
      - 中身: "`cat1`。最初の 1 秒は、MRFP が PCMU に符号化した `cat1.wav` と 1 バイトも違わない"
  - {id: cat-200-ue-p, msg: 200 OK, from: ue2, to: pcscf, proto: SIP}
  - {id: cat-200-p-s, msg: 200 OK, from: pcscf, to: scscf, proto: SIP}
  - {id: cat-200-s-as, msg: 200 OK, from: scscf, to: mmtel, proto: SIP}
  - {id: cat-bye, msg: BYE, from: mmtel, to: mrfc, proto: SIP, note: (M)}
  - {id: cat-move-as-s, msg: UPDATE, from: mmtel, to: scscf, proto: SIP}
  - {id: cat-move-s-p, msg: UPDATE, from: scscf, to: pcscf, proto: SIP}
  - id: cat-move-p-ue
    msg: UPDATE
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - SDP: UE 2 の SDP。UE 1 のメディアが UE 2 へ移る
  # 会議の URI は PSI
  - id: psi-sub-ue-p
    msg: SUBSCRIBE
    from: phone1
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`sip:conf-o47ha7ifxyni@conf.ims.mnc001.mcc001.3gppnetwork.org`"
      - Event: "`conference`"
  - {id: psi-sub-p-s, msg: SUBSCRIBE, from: pcscf, to: scscf, proto: SIP}
  - id: psi-naptr
    msg: NAPTR
    from: scscf
    to: dns
    proto: DNS
    note: (問い合わせ)
    fields:
      - 名前: "`conf.ims.mnc001.mcc001.3gppnetwork.org`"
  - {id: psi-naptr-ans, msg: NAPTR, from: dns, to: scscf, proto: DNS, note: (答え)}
  - {id: psi-srv, msg: SRV, from: scscf, to: dns, proto: DNS, note: (問い合わせ)}
  - id: psi-srv-ans
    msg: SRV
    from: dns
    to: scscf
    proto: DNS
    note: (答え)
    fields:
      - 答え: "`mmtel.ims.mnc001.mcc001.3gppnetwork.org` の 5060 番"
  - {id: psi-sub-s-f, msg: SUBSCRIBE, from: scscf, to: focus, proto: SIP}
  # フォーカスと混ぜ合わせ
  - id: mix-conf
    msg: INVITE
    from: focus
    to: mrfc
    proto: SIP
    note: (参加者ごと)
    fields:
      - Request-URI: "`sip:conf=conf-o47ha7ifxyni@mrfc...`"
      - 最初の INVITE: 混ぜ合わせを作る
      - そのあとの INVITE: ターミネーションを足す
  - {id: mix-up-1, msg: RTP, from: phone1, to: agw, proto: RTP}
  - {id: mix-up-2, msg: RTP, from: phone2, to: agw, proto: RTP}
  - {id: mix-up-3, msg: RTP, from: phone3, to: agw, proto: RTP}
  - {id: mix-up-agw, msg: RTP, from: agw, to: mrfp, proto: RTP, note: (3 本)}
  - id: mix-sum
    do: 参加者ごとに和を作る
    at: mrfp
    fields:
      - phone1 へ: phone2 と phone3 の音声の和
      - phone2 へ: phone1 と phone3 の音声の和
      - phone3 へ: phone1 と phone2 の音声の和
  - {id: mix-down-agw, msg: RTP, from: mrfp, to: agw, proto: RTP, note: (3 本)}
  - {id: mix-down-1, msg: RTP, from: agw, to: phone1, proto: RTP}
  - {id: mix-down-2, msg: RTP, from: agw, to: phone2, proto: RTP}
  - {id: mix-down-3, msg: RTP, from: agw, to: phone3, proto: RTP}
  # 会議に戻る
  - id: rj-inv-ue-p
    msg: INVITE
    from: phone2
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`sip:conf-o47ha7ifxyni@conf...`。フォーカスの Contact で知った会議の URI"
  - {id: rj-inv-p-s, msg: INVITE, from: pcscf, to: scscf, proto: SIP}
  - id: rj-inv-s-as
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    fields:
      - P-Served-User: "`phone2;sescase=orig`"
  - id: rj-inv-as-s
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (B2BUA)
    fields:
      - Request-URI: 同じ会議の URI
  - id: rj-inv-s-f
    msg: INVITE
    from: scscf
    to: focus
    proto: SIP
    fields:
      - 送り先: DNS で見つけたフォーカス
      - P-Served-User: 無い
  - id: rj-tell
    do: フォーカス宛てと見分ける
    at: focus
    fields:
      - 見るもの: P-Served-User の有無。Request-URI はどちらも同じ
      - 無いとき: フォーカス宛て。フィルタ条件で届いたものではない
  - id: rj-conf
    msg: INVITE
    from: focus
    to: mrfc
    proto: SIP
    fields:
      - Request-URI: "`sip:conf=conf-o47ha7ifxyni@mrfc...`"
      - SDP: phone2 の offer
  - {id: rj-conf-200, msg: 200 OK, from: mrfc, to: focus, proto: SIP}
  - id: rj-200-f-s
    msg: 200 OK
    from: focus
    to: scscf
    proto: SIP
    fields:
      - Contact: "`<sip:conf-o47ha7ifxyni@conf...>;isfocus`"
      - SDP: MRFP の answer
  - {id: rj-200-s-as, msg: 200 OK, from: scscf, to: mmtel, proto: SIP}
  - {id: rj-200-as-s, msg: 200 OK, from: mmtel, to: scscf, proto: SIP}
  - {id: rj-200-s-p, msg: 200 OK, from: scscf, to: pcscf, proto: SIP}
  - {id: rj-200-p-ue, msg: 200 OK, from: pcscf, to: phone2, proto: SIP}
```

---

# MRF による音声ガイダンス、呼び出し音、会議

ims-practice 勉強会 第 8 回（MRFC と MRFP、音声ガイダンス、着信側が選ぶ呼び出し音、3 人の会議）

---

[layout: summary]

## この回で分かること

最初の問い。電話をかけると、相手が鳴っているあいだに短い旋律が聞こえます。その旋律は相手が選んだものです。流しているのはどのノードで、流すと決めたのはどのノードでしょうか。

- MRF とは何か。なぜ MRFC と MRFP の 2 つのノードに分かれるか。
- AS がサービスを頼む方法。Mr' で送る RFC 4240 の Request-URI。
- 通話に応答する前に、網が発信者へ音声を届けるしくみ。
- 音声ガイダンス、着信側が選んだ呼び出し音、2 つ目の着信を、キャプチャのどこで見るか。
- 3 人の会議の作り方と、音声を混ぜるノード。

> 手順書 [MRF による音声ガイダンス、呼び出し音、会議](../../../docs/guide/ja/mrf.md)（`docs/guide/ja/mrf.md`）
> 仕様の版は手順書の冒頭にあります。TS 23.228 V20.1.0、TS 24.229 V20.1.0、TS 24.182 V19.0.0、TS 24.628 V19.0.0、TS 24.615 V19.0.0、TS 24.147 V19.0.0、TS 23.333 V19.0.0、RFC 4240、RFC 4575、GSMA IR.92 v24.0 などです。

---

## MRF とは

```ims-scene
id: roles
nodes: [scscf, mmtel, mrfc, ue1, pcscf, agw, mrfp]
lead: 何を流すかを決めるのは AS で、音声を作るのは MRF です。MRF は SIP の側の MRFC と、RTP の側の MRFP に分かれます。
steps:
  - id: route
    title: CSCF は経路を決め、AS は通話を変える
    show: [gm, mw, isc]
    text: どちらも音声は作りません。IMS の中で自分からメディアを作るノードが MRF です（TS 23.228 4.7）。
  - id: mrfc
    title: MRFC は SIP を話す
    show: [mr]
    focus: [mrfc]
    text: AS が Mr' の INVITE でサービスを頼むと、MRFC はそれに要るメディアの資源を決めます。
  - id: mrfp
    title: MRFC は Mp で MRFP を動かす
    show: [mp]
    focus: [mrfp]
    text: MRFP はターミネーションに音声を流し、複数のターミネーションの音声を足し合わせます（TS 23.333 6.2.9）。
  - id: media
    title: UE と MRFP の RTP は IMS-AGW を通る
    show: [media-ue, media-mrfp]
    focus: [agw, mrfp]
    text: MRFP は IMS-AGW と同じ中継に、音声ガイダンスと混ぜ合わせを加えたものです。
```

- CSCF は経路を決め、前の回の AS は通話を変えます。どちらも音声は作りません。IMS の中で自分からメディアを作るノードが MRF です（TS 23.228 4.7）。音声ガイダンス、トーン、会議の混ぜ合わせた音声を作ります。
- **MRFC** は SIP を話します。AS が INVITE でサービスを頼むと、MRFC はそれに要るメディアの資源を決めます。
- **MRFP** は RTP を扱います。ターミネーションに音声を流し、複数のターミネーションの音声を足し合わせて、それぞれに混ぜ合わせた音声を返します（TS 23.333 6.2.9）。

```text
  S-CSCF --- MMTel AS ---(Mr', SIP)--- MRFC
                                         |
                                       (Mp)
                                         |
  UE --- P-CSCF --- agw -----(RTP)---- MRFP
```

- MRFP は、P-CSCF のメディアゲートウェイ（agw）と同じ中継に、音声ガイダンスと混ぜ合わせを加えたものです。UE と MRFP の間の RTP は agw を通ります。

> 手順書 [「MRF とは」](../../../docs/guide/ja/mrf.md#mrf-とは)

---

## 3 つの参照点

| 参照点 | どこの間か | このプロジェクトでは |
| --- | --- | --- |
| Mr' | AS と MRFC | SIP。MMTel AS から直接送ります。S-CSCF からの Mr は使いません。 |
| Mp | MRFC と MRFP | 標準では H.248（TS 29.333）。ここでは 8080 番ポートの小さな HTTP API で、H.248 のコマンドごとに操作が 1 つあります。 |
| Cr | AS と MRFC | 流す音声の取得とメディア制御の命令。作っていません。MRFP は持っている WAV ファイルだけを流します。 |

- ここでの Mp の操作は、ターミネーションの追加、変更（モード、相手のアドレス、再生）、削除と、再生が終わったことを知らせる Notify です。キャプチャでは `http && ip.addr == 172.28.0.86` で見えます。
- ここでメディアを頼むのは S-CSCF ではなく AS です（TS 23.218）。そのため Mr には何も流れません。Cr はそもそも作っていません。

> 手順書 [「MRF とは」](../../../docs/guide/ja/mrf.md#mrf-とは)

---

## 登場するノード

| ノード | アドレス | 役割 |
| --- | --- | --- |
| UE 1、UE 2 | 172.28.0.100、.101 | 音声ガイダンスと呼び出し音の、発信者と着信者。 |
| phone1、phone2、phone3 | 172.28.0.110、.111、.112 | `phones` プロファイルの UE。ラボから操作します。2 つ目の着信と会議に使います。 |
| P-CSCF と agw | 172.28.0.40、.50 | ほかの通話と同じです。MRFP との RTP も agw を通ります。 |
| MMTel AS | 172.28.0.75 | 何を流すかを決めます。会議ではフォーカスになります。 |
| MRFC | 172.28.0.85 | 役割 `mrfc`。 |
| MRFP | 172.28.0.86 | 役割 `mrfp`。制御の API は 8080 番ポート、RTP は 40100 番から 40199 番のポートです。 |

- `deploy/compose/mmtel.yaml` に、MRFC と、通話を断る前に流す音声ガイダンスを書いてあります。`deploy/compose/mrfp.yaml` は `notice`、`cat1`、`cat2` の名前を `deploy/announcements` の WAV ファイルに対応させます。

> 手順書 [「登場するノード」](../../../docs/guide/ja/mrf.md#登場するノード)

---

## RFC 4240 で AS が MRF に頼む

| Request-URI | サービス |
| --- | --- |
| `sip:annc@mrfc...:5060;play=/provisioned/notice` | `notice` を 1 回流します。 |
| `sip:annc@...;play=/provisioned/cat1;repeat=forever` | AS がダイアログを終えるまで `cat1` を流し続けます。 |
| `sip:conf=conf-pcn6hpat5alr@mrfc...` | その会議に加わります。最初の INVITE が会議を作ります。 |

- サービスは Request-URI の user part で指定します。`annc` は再生、`conf=` は会議への参加です。`play=` は MRFP の設定にあるファイルの名前です。
- 最初の `conf=` の INVITE が混ぜ合わせを作り、そのあとの INVITE はそこにターミネーションを足します。

フィルタは `sip.r-uri contains "annc@"` と `sip.r-uri contains "conf="` です。

> 手順書 [「AS が MRF に頼む方法。RFC 4240」](../../../docs/guide/ja/mrf.md#as-が-mrf-に頼む方法rfc-4240)。RFC 4240 3、5。

---

## 応答の前に届く音声

```ims-scene
id: early
nodes: [ue1, pcscf, scscf, mmtel, mrfc, agw, mrfp]
lead: AS は MRFP の SDP で発信者に答え、ACK を送る時機で再生の始まりを決めます。
steps:
  - id: invite
    title: UE 1 の INVITE が AS に届く
    show: [an-inv-ue-p, an-inv-p-s, an-inv-s-as]
    active: an-inv-s-as
    detail: [an-inv-ue-p]
    text: UE 1 の発信は、ほかの通話と同じ道で UE 1 の AS に届きます。
  - id: ask
    title: AS が SDP なしで MRFC に頼む
    show: [an-annc]
    text: 音声ガイダンスを頼む INVITE には SDP が付きません。
  - id: offer
    title: MRFC が MRFP の offer を返す
    show: [an-annc-200]
    checkpoint: true
    text: MRFC は MRFP のターミネーションの offer を 200 に入れて返します。
  - id: early
    title: AS が発信者の offer に MRFP の SDP で答える
    show: [an-183-as-s, an-183-s-p, an-183-p-ue]
    active: an-183-p-ue
    checkpoint: true
    text: |
      通話に応答する前に音声を届けるため、AS は自分から答え、early media を認めます。
      TS 24.628 Annex G のゲートウェイのモデルです。
  - id: ack
    title: ACK を受けて MRFP が流し始める
    show: [an-ack, an-play]
    active: an-play
    detail: [an-ack, an-play]
    text: AS は answer を ACK に入れます。MRFP は ACK のあとで流し始めます。
  - id: rtp
    title: 音声は IMS-AGW を通って UE 1 に届く
    show: [an-rtp-p-agw, an-rtp-agw-ue]
    active: an-rtp-agw-ue
    checkpoint: true
    text: UE 1 は自分の呼び出し音の代わりに、届いた音声を流します。
  - id: bye
    title: 流し終えたら MRFC が BYE を送る
    show: [an-bye]
    text: |
      終わりのある再生が済むと、MRFC が BYE でダイアログを終えます。
      こうして AS は、発信者が受け取れるようになってから再生を始められます。
```

- 音声ガイダンスを頼む INVITE には SDP が付きません。そのため MRFC は MRFP のターミネーションの offer を 200 に入れて返し、AS は answer を ACK に入れます。
- MRFP は ACK を受けてから流し始め（TS 24.229 5.8.2.2.1）、終わりのある再生が済むと MRFC が BYE でダイアログを終えます（5.8.3.2.1）。こうして AS は、発信者が受け取れるようになってから再生を始められます。
- 通話に応答する前に発信者へ音声を届けるため、AS は発信者の offer に MRFP の SDP で自分から答え、`P-Early-Media: sendonly` で early media を認めます（RFC 5009）。TS 24.628 Annex G のゲートウェイのモデルです。
- 発信者の UE は、自分の呼び出し音の代わりに届いた音声を流します（TS 24.628 4.7.2.1）。

フィルタは `sip.P-Early-Media` です。

> 手順書 [「AS が MRF に頼む方法。RFC 4240」](../../../docs/guide/ja/mrf.md#as-が-mrf-に頼む方法rfc-4240)

---

## 実行する

```sh
docker compose up -d --build

# 音声ガイダンス。UE 1 が発信を規制してから UE 2 にかける
docker compose run --rm ue xcap set baoc on
docker compose run --rm ue call tel:+12025550102
docker compose run --rm ue xcap set baoc off

# 2 つ目の着信と会議。ラボから操作する
docker compose --profile phones up -d phone1 phone2 phone3
```

- 呼び出し音は、OAM から加入者 2 に `cat1` を設定してから、http://localhost:8080 のラボで `phone1` から `phone2` にかけます。
- `scripts/e2e-mrf.sh` は、この回のすべてを実行してキャプチャで確かめます。キャプチャ、通話と MRF のフレーム、呼び出し音のあいだに UE 1 が受け取った音声、UE のログが残ります。

> 手順書 [「動かしてみる」](../../../docs/guide/ja/mrf.md#動かしてみる) と [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## キャプチャを開く

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (sip.Event contains "conference") || (http && ip.addr == 172.28.0.86)
```

- 通話、会議の SUBSCRIBE と NOTIFY、MRFP の制御の API が残ります。
- **Telephony > RTP > RTP Streams** に MRFP のストリームが並び、**Play Streams** で再生できます。
- RTP の中には、それを説明する SDP を Wireshark が読む前に流れ始めるものがあります。すべてを復号するには **Analyze > Enabled Protocols > RTP > rtp_udp** を有効にします。

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/mrf.md#キャプチャを開く)

---

## 通話を断る前の音声ガイダンス

```ims-scene
id: announce
view: ladder
nodes: [ue1, pcscf, scscf, mmtel, mrfc]
lead: AS は通話を断る前に音声ガイダンスを流し、PRACK を待ってから再生を始めさせます。
steps:
  - id: invite
    title: UE 1 が UE 2 にかける
    show: [an-inv-ue-p, an-inv-p-s, an-inv-s-as]
    detail: []
    text: UE 1 の INVITE が UE 1 の AS に届きます。
  - id: decide
    title: AS が発信規制を当てる
    show: [an-ocb]
    text: UE 1 は自分の発信を規制しているので、AS は通話を断ります。その前に `notice` を流します。
  - id: annc
    title: AS が MRFC に音声ガイダンスを頼む
    show: [an-annc, an-annc-200]
    detail: []
    text: SDP の無い INVITE に、MRFC が MRFP の offer で応答します。
  - id: early
    title: 183 で MRFP の SDP が UE 1 に届く
    show: [an-183-as-s, an-183-s-p, an-183-p-ue]
    detail: []
    checkpoint: true
    text: "183 には `P-Early-Media: sendonly` が付き、高信頼の応答なので PRACK を求めます。"
  - id: prack
    title: UE 1 の PRACK が AS に届く
    show: [an-prack-ue-p, an-prack-p-s, an-prack-s-as]
    detail: []
    text: UE 1 が SDP を受け取ったことが、これで AS に分かります。
  - id: ack
    title: AS が ACK を送り、再生が始まる
    show: [an-ack]
    text: AS は PRACK を待ってから ACK を送ります。UE 1 が SDP を受け取る前に、音声ガイダンスのフレームが失われないようにするためです。
  - id: bye
    title: 流し終えた MRFC が BYE を送る
    show: [an-bye]
    checkpoint: true
    text: ACK から BYE までを測ります。1.08 秒で、WAV ファイルの長さと同じです。
  - id: reject
    title: AS が 603 で通話を断る
    show: [an-603-as-s, an-603-s-p, an-603-p-ue]
    active: an-603-p-ue
    text: 音声ガイダンスを流し終えてから、AS は UE 1 の通話を断ります。
```

```ladder
UE 1, P-CSCF, MMTel AS, MRFC
UE 1 -> P-CSCF -> MMTel AS: INVITE // C1
MMTel AS -> MRFC: INVITE annc、SDP なし // M
MMTel AS <-- MRFC: 200、MRFP の offer
UE 1 <-- P-CSCF <-- MMTel AS: 183、P-Early-Media // C1
UE 1 -> P-CSCF -> MMTel AS: PRACK
MMTel AS -> MRFC: ACK、answer を入れて再生が始まる
MMTel AS <-- MRFC: BYE、notice を流し終えた
UE 1 <-- P-CSCF <-- MMTel AS: 603、Reason cause=603
```

- UE 1 は自分の発信を規制しているので、UE 1 の AS は通話を断ります（TS 24.611 4.5.2.4.1）。断る前に `notice` を流します（TS 24.628 4.2.4）。
- AS は PRACK を待ってから ACK を送ります。UE 1 が SDP を受け取る前に、音声ガイダンスのフレームが失われないようにするためです。
- ACK から BYE までを測ります。1.08 秒で、WAV ファイルの長さと同じです。

> 手順書 [「通話を断る前の音声ガイダンス」](../../../docs/guide/ja/mrf.md#通話を断る前の音声ガイダンス)

---

## CAT（着信側が選ぶ呼び出し音）

```ims-scene
id: cat
nodes: [ue1, pcscf, scscf, mmtel, hss, mrfc, ue2, agw, mrfp]
lead: 呼び出し音を決めるのは着信側の AS です。着信のたびに HSS から設定を読み、UE 2 を呼び出す前に MRFC に頼みます。
steps:
  - id: arrive
    title: 着信側の AS に INVITE が届く
    only: [cat-inv-s-as]
    text: UE 1 からの INVITE が、UE 2 のサービスのために AS に届きます。
  - id: read
    title: AS が Sh で呼び出し音の設定を読む
    show: [cat-udr, cat-uda]
    active: cat-udr
    detail: [cat-udr, cat-uda]
    text: |
      利用者の呼び出し音は、事業者が HSS に設定します（TS 24.182 4.5.2）。ここではラボが設定します。
      AS は着信のたびに読みます。
  - id: ask
    title: UE 2 を呼び出す前に MRFC に頼む
    show: [cat-annc, cat-annc-200]
    active: cat-annc
    detail: [cat-annc]
    checkpoint: true
    text: AS は UE 2 への INVITE より前に MRFC に頼みます（4.5.5.3.7）。`repeat=forever` なので、MRF は AS がダイアログを終えるまで流し続けます。
  - id: call
    title: AS が UE 2 を呼び出す
    only: [cat-inv-as-s, cat-inv-s-p, cat-inv-p-ue]
    active: cat-inv-p-ue
    detail: []
    text: INVITE は S-CSCF と P-CSCF を通って UE 2 に届きます。
  - id: alert
    title: UE 2 が呼び出しを始める
    only: [cat-180-ue-p, cat-180-p-s, cat-180-s-as]
    active: cat-180-s-as
    detail: []
    text: UE 2 の 180 が AS に届きます。
  - id: tone
    title: UPDATE で呼び出し音の SDP が UE 1 に届く
    show: [cat-update-as-s, cat-update-s-p, cat-update-p-ue]
    active: cat-update-p-ue
    checkpoint: true
    text: |
      UE 1 はプリコンディションを使うので、呼び出し音は UE 2 が呼び出しを始めたあとの UPDATE で届きます（4.5.5.3.7 b 1）。
      プリコンディションを使わない発信者には、18x そのものに入ります。
  - id: play
    title: ACK を受けて MRFP が呼び出し音を流す
    only: [cat-ack, cat-rtp-p-agw, cat-rtp-agw-ue]
    active: cat-rtp-agw-ue
    checkpoint: true
    text: MRFP の音声が、応答より前に IMS-AGW を通って UE 1 に届きます。
  - id: answer
    title: UE 2 が応答し、AS が呼び出し音を止める
    only: [cat-200-ue-p, cat-200-p-s, cat-200-s-as, cat-bye]
    active: cat-bye
    detail: []
    checkpoint: true
    text: UE 2 の 200 が届くと、AS は MRFC への BYE で呼び出し音を止めます。
  - id: move
    title: UE 1 のメディアを UE 2 へ移す
    only: [cat-move-as-s, cat-move-s-p, cat-move-p-ue]
    active: cat-move-p-ue
    text: AS は UE 1 に 200 を送る前に、UE 2 の SDP を入れた UPDATE を送ります。
```

| 見るもの | 見どころ |
| --- | --- |
| UE 2 の AS の UDR | Service-Indication は `Customized-Alerting-Tone`。着信のたびに読みます。 |
| AS から MRFC への INVITE | `play=/provisioned/cat1;repeat=forever`。UE 2 への INVITE より前に送ります。 |
| UE 1 に届く 183 | UE 2 の answer が、プリコンディションの表とともにそのまま届きます。 |
| UE 2 の 180 のあとの UPDATE | `P-Early-Media: sendonly` と、`a=content:g.3gpp.cat` の付いた MRFP の SDP。 |
| 応答のとき | MRFC への BYE と、200 の前に UE 1 のメディアを UE 2 へ移す UPDATE。 |

- 利用者の呼び出し音は、事業者が HSS に設定します（TS 24.182 4.5.2）。ここではラボが設定します。着信側の AS は着信のたびに Sh で読み、UE 2 を呼び出す前に MRFC に頼みます（4.5.5.3.7）。
- UE 1 はプリコンディションを使うので、呼び出し音は UE 2 が呼び出しを始めたあとの UPDATE で届きます（4.5.5.3.7 b 1）。プリコンディションを使わない発信者には、18x そのものに入ります。

フィルタは `sdp.media_attr == "content:g.3gpp.cat"` です（RFC 4796）。

> 手順書 [「CAT。着信側の利用者が選ぶ呼び出し音」](../../../docs/guide/ja/mrf.md#cat着信側の利用者が選ぶ呼び出し音)。TS 24.182 4.5.5.3.7 b 2。

---

## 呼び出し音を止めて比べる

| 見るもの | `cat1` のとき | 止めたとき |
| --- | --- | --- |
| UE 1 のログ | `"ringback":"network"` | `"ringback":"local"` |
| UE 1 に届くもの | P-Early-Media の付いた UPDATE | 180 だけ |
| MRFC | `repeat=forever` の INVITE | 無し |
| MRFP | 応答前に UE 1 へ届く RTP | 無し |

- 呼び出し音を止めると、UE 1 は自分の呼び出し音を流します（TS 24.628 4.7.2.1）。e2e のスクリプトのキャプチャには、この 2 つの通話が続けて残ります。見比べてください。
- なぜ見比べるのか。呼び出し音が増やすフレームと、ほかは何も変わらないことの、両方が分かるからです。
- スクリプトは、UE 1 が受け取った最初の 1 秒を、MRFP が PCMU に符号化した `cat1.wav` と比べます。1 バイトも違いません。

> 手順書 [「CAT。着信側の利用者が選ぶ呼び出し音」](../../../docs/guide/ja/mrf.md#cat着信側の利用者が選ぶ呼び出し音)

---

## 割込通話と保留

| 見るもの | 見どころ |
| --- | --- |
| 2 つ目の通話での phone1 の 180 | `Alert-Info: <urn:alert:service:call-waiting>`（TS 24.615 4.5.5.3.2、RFC 7462）。 |
| phone3 に届く 180 | この Alert-Info はありません。`hss.yaml` の CW の事業者の設定が、発信者に知らせないとしています（4.5.5.2.3）。 |
| 待たせた着信への応答 | 先の通話に `a=sendonly` の re-INVITE を送り、phone2 を保留にします（4.5.5.3.3、TS 24.610）。 |
| **Swap** | 一方が保留になり、もう一方が `a=sendrecv` で戻ります。 |

- IR.92 は、待たせる着信の扱いを網ではなく UE に求めています（2.3.4）。phone1 は呼び出しを返すだけで、自動では応答しません。
- 知らせる設定なら、AS は Alert-Info をそのまま渡し、音声ガイダンスを設定してあれば、待たせているあいだ発信者にそれを流します。ここに MRF が出てくるのは、その設定のときです。

フィルタは `sip.Alert-Info contains "call-waiting"` です。

> 手順書 [「割込通話と保留」](../../../docs/guide/ja/mrf.md#割込通話と保留)

---

## 3 人の会議

phone1 は応答済みの通話を 2 つ持っていて、**Merge** を押します。

| 段階 | 見どころ |
| --- | --- |
| 1 | 会議ファクトリの URI `sip:mmtel@conf-factory...` への INVITE。 |
| 2 | AS は `conf=` と phone1 の offer で MRFC に頼み、`isfocus` を付けた会議の URI を Contact に入れて phone1 に応答します。 |
| 3 | phone1 がその会議の URI を SUBSCRIBE します。Event は `conference` です。 |
| 4 | 相手ごとの REFER。Refer-To には、phone1 とその相手の通話を置き換える Replaces が入ります。 |
| 5 | フォーカスは、その Replaces と MRFP の新しい offer を付けて相手に INVITE します。相手は置き換えられた通話を BYE で終えます。 |

- 作った人が相手ごとに REFER で招く方法です。TS 24.147 5.3.1.3.3 の option 2b で、IR.92 2.3.3 が求めるものです。
- フォーカスは REFER を 200 で受け付け、202 は使いません。RFC 7647 5 が RFC 3515 の 202 を改めました。SUBSCRIBE について RFC 6665 8.3.1 が改めたのと同じです。TS 24.147 の流れの例でも 200 を返しています（A.4.4.1）。

フィルタは `sip.Refer-To contains "Replaces"` と `sip.Replaces` です。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## 会議の URI は PSI

```ims-scene
id: psi
nodes: [phone1, pcscf, scscf, dns, focus]
lead: 会議の URI はノードの名前を含みません。S-CSCF はほかのホストと同じく DNS でフォーカスを見つけます。
steps:
  - id: subscribe
    title: phone1 が会議の URI を SUBSCRIBE する
    show: [psi-sub-ue-p, psi-sub-p-s]
    active: psi-sub-ue-p
    text: 会議の URI はホーム網の PSI で、ドメインは事業者が決めます。ポート番号は付けません。
  - id: naptr
    title: S-CSCF がサブドメインの NAPTR を引く
    show: [psi-naptr, psi-naptr-ans]
    active: psi-naptr
    text: サブドメインの `conf` は、AS が受け持つ会議の全体を表します。
  - id: srv
    title: SRV の答えが会議を受け持つ AS を指す
    show: [psi-srv, psi-srv-ans]
    active: psi-srv-ans
    checkpoint: true
    text: S-CSCF は RFC 3263 でフォーカスを見つけます。TS 23.228 5.4.12.3 のサブドメインを使う方法です。
  - id: deliver
    title: SUBSCRIBE がフォーカスに届く
    show: [psi-sub-s-f]
    text: S-CSCF は HSS に問い合わせません。ほかの網からの要求も、I-CSCF が HSS に問い合わせずに DNS で送ります（TS 24.229 5.3.2.1 step 3）。
```

- 会議の URI は、ホーム網の公開サービス識別子（PSI）です（TS 23.228 4.3.6）。形は `sip:conf-<id>@conf.ims.mnc001.mcc001.3gppnetwork.org` で、ドメインは事業者が決めます。ノードの名前は含まず、ポート番号も付けません。網の外に出る可能性がある PSI には、ポート番号を付けないほうがよいためです（TS 23.003 13.5）。
- このサブドメインの NAPTR と SRV のレコードが、会議を受け持つ AS を指します。S-CSCF はほかのホストと同じく RFC 3263 でフォーカスを見つけます。TS 23.228 5.4.12.3 のサブドメインを使う方法です。
- ほかの網からの要求は I-CSCF から入ります。I-CSCF はこのサブドメインを知っていて、HSS に問い合わせずに、やはり DNS で送ります（TS 24.229 5.3.2.1 step 3）。
- ファクトリの URI は発信側の PSI です（TS 23.228 5.4.12.1）。ここへの INVITE は、作る人のほかの INVITE と同じく、作る人の発信側のフィルタ条件で AS に届きます。

フィルタは `dns.qry.name contains "conf."` と `sip.Contact contains "isfocus"` です。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## フォーカスと混ぜ合わせ

```ims-scene
id: mix
nodes: [phone1, phone2, phone3, agw, mrfp, mrfc, focus]
lead: MRFP はそれぞれの参加者に、ほかの参加者の音声の和を送ります。
steps:
  - id: ask
    title: フォーカスが参加者ごとに MRFC に頼む
    show: [mix-conf]
    text: 参加者のメディアのために、フォーカスは参加者ごとに MRFC とのダイアログを持ちます。
  - id: send
    title: 参加者がそれぞれ自分の音声を送る
    show: [mix-up-1, mix-up-2, mix-up-3, mix-up-agw]
    active: mix-up-agw
    detail: []
    text: RTP は IMS-AGW を通って MRFP に届きます。
  - id: sum
    title: MRFP が参加者ごとに和を作る
    show: [mix-sum]
    checkpoint: true
    text: 自分の音声は自分に返しません（TS 23.333 6.2.9）。
  - id: back
    title: 和がそれぞれの参加者に届く
    show: [mix-down-agw, mix-down-1, mix-down-2, mix-down-3]
    active: mix-down-agw
    detail: []
    text: キャプチャでは、MRFP から IMS-AGW への 3 つのストリームと、その逆の 3 つが見えます。
```

- AS は会議のフォーカスであり、通知サービスでもあります（TS 24.147 4）。参加者それぞれとダイアログを持ち、参加者のメディアのための MRFC とのダイアログも持ちます。
- MRFC は最初の `conf=` の INVITE で混ぜ合わせを作り、そのあとの INVITE ごとにターミネーションを足します。MRFP は、それぞれの参加者にほかの参加者の音声の和を送ります（TS 23.333 6.2.9）。
- 招かれた相手もプリコンディションを使うので、それぞれが MRFP の offer に高信頼の 183 で答え、200 には SDP を付けません。AS はその answer を ACK に入れて MRFC に渡します（RFC 3262 5）。
- キャプチャでは、MRFP から agw への 3 つのストリームと、その逆の 3 つが見えます。ある参加者が受け取る混ぜ合わせた音声は、どの参加者 1 人のトーンよりも大きくなります。
- 会議は作った人が抜けると終わります（TS 24.147 5.3.2.7）。phone1 が切ると、AS は phone2 と phone3 に BYE を送り、参加者ごとの MRFC とのダイアログにも BYE を送ります。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## 会議イベントパッケージ

| 見るもの | 見どころ |
| --- | --- |
| NOTIFY | conference-info の XML（RFC 4575）。参加者ごとに `<user>` と、状態が `connected` の `<endpoint>` があります。 |
| `<user>` の `entity` | 網がその参加者に保証した識別子です。プライバシーを求めた参加者では、匿名の URI です（RFC 4575 8.2）。 |
| `<endpoint>` の `entity` | フォーカスが作るキーで、`urn:uuid:` の URI です。RFC 4575 5.7 はキーの作り方をフォーカスに任せています。 |
| maximum-user-count | 6 人です。7 人目には 403 を返します。 |
| REFER の NOTIFY | REFER はそれぞれ refer イベントの購読を作ります（RFC 3515 2.4.4）。すぐに `100 Trying` を、最終応答は `terminated;reason=noresource` を付けた最後の NOTIFY で知らせます（2.4.7）。 |

- キーに UE の Contact を使わないのはなぜか。Contact はその UE のアドレスで、NOTIFY はほかの網の購読者を含むすべての購読者に届くからです。
- この NOTIFY の内容は、ラボの各カードの **Conference** の行に並びます。

フィルタは `sip.Event contains "conference"` です。

> 手順書 [「3 人の会議」](../../../docs/guide/ja/mrf.md#3-人の会議)

---

## 会議に戻る

```ims-scene
id: rejoin
view: ladder
nodes: [phone2, pcscf, scscf, mmtel, focus, mrfc]
lead: 同じ Request-URI の INVITE が AS に 2 回届きます。AS は P-Served-User の有無で、どちらがフォーカス宛てかを見分けます。
steps:
  - id: out
    title: phone2 が会議の URI に発信する
    show: [rj-inv-ue-p, rj-inv-p-s, rj-inv-s-as]
    active: rj-inv-s-as
    detail: [rj-inv-ue-p, rj-inv-s-as]
    text: S-CSCF は phone2 の発信側のサービスのために、この INVITE を AS に渡します。
  - id: back
    title: AS が同じ URI に INVITE を返し、S-CSCF が DNS でフォーカスを見つける
    show: [rj-inv-as-s, rj-inv-s-f]
    active: rj-inv-s-f
    detail: [rj-inv-s-f]
    text: B2BUA である AS は、同じ URI に宛てた新しい INVITE を S-CSCF に返します。
  - id: tell
    title: フォーカスが自分宛ての INVITE だと見分ける
    show: [rj-tell]
    checkpoint: true
    text: フォーカスに INVITE を届けるのはフィルタ条件ではないので、P-Served-User がありません。
  - id: mix
    title: フォーカスが MRFC にターミネーションを頼む
    show: [rj-conf, rj-conf-200]
    active: rj-conf
    detail: [rj-conf]
    text: 作った人と同じやり方で迎え入れます（TS 24.147 5.3.2.4.1）。
  - id: answer
    title: isfocus の Contact を付けた 200 が phone2 に届く
    show: [rj-200-f-s, rj-200-s-as, rj-200-as-s, rj-200-s-p, rj-200-p-ue]
    active: rj-200-p-ue
    detail: [rj-200-f-s]
    text: NOTIFY はその参加者を再び `connected` として並べ、joining-method は `dialed-in` になります。
```

- 会議を抜けた参加者は、会議が続いているあいだなら戻れます。フォーカスの Contact で知った会議の URI に INVITE を送ります（TS 24.147 5.3.1.4.1）。
- この INVITE は、その利用者のほかの発信と同じ道を通ります。S-CSCF が発信側のサービスのために AS に渡し、AS が同じ URI に宛てた新しい INVITE を返すと、S-CSCF は DNS でフォーカスを見つけます。
- フォーカスに INVITE を届けるのはフィルタ条件ではないので、フォーカス宛ての INVITE には P-Served-User がありません。AS はこれで 2 つを見分けます。Request-URI はどちらも同じです。
- フォーカスは、作った人と同じやり方でその参加者を迎え入れます（5.3.2.4.1）。NOTIFY はその参加者を再び `connected` として並べ、joining-method は `dialed-out` ではなく `dialed-in` になります。
- どの会議にも使われていない会議の URI への INVITE には、404 を返します。作った人が戻ろうとしても同じです。作った人が抜けた時点で会議は終わり、その URI も手放しているためです（5.3.2.7）。

> 手順書 [「会議に戻る」](../../../docs/guide/ja/mrf.md#会議に戻る)。e2e のスクリプトは動かしません。`internal/mmtel` の `TestRejoinWithTheConferenceURI` が 1 つのプロセスで動かします。

---

## Wireshark の表示フィルタ

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.r-uri contains "annc@"` | 音声ガイダンスや呼び出し音を頼む要求。 |
| `sip.r-uri contains "conf="` | MRFC の会議に加わる参加者。 |
| `sip.P-Early-Media` | early media を認める応答と UPDATE。 |
| `sdp.media_attr == "content:g.3gpp.cat"` | 呼び出し音の SDP。 |
| `sip.Alert-Info contains "call-waiting"` | 待たせる着信の 180。 |
| `sip.Event contains "conference"` | 会議イベントパッケージの SUBSCRIBE と NOTIFY。 |
| `rtp && ip.addr == 172.28.0.86` | MRFP の RTP。RTP のヒューリスティックを有効にして使います。 |
| `http && ip.addr == 172.28.0.86` | Mp を流れる MRFP の制御の API。 |

- 手順書にはこのほかに 4 つあります。会議の Replaces、フォーカスの応答、フォーカスへの経路を引く DNS の問い合わせ、CAT だけを選ぶフィルタです。
- e2e のスクリプトは、手順書の表のフィルタをすべてキャプチャに当てます。フレームが 1 つも見つからないフィルタがあれば失敗します。

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/mrf.md#wireshark-の表示フィルタ)

---

## ラボから動かす

```sh
docker compose --profile phones up -d --build
```

1. OAM から加入者 2 に呼び出し音 `cat1` を設定すると、Subscribers の欄にあるその行の Alerting tone (CAT) が変わります。`phone1` で `phone2` にかけると、`phone1` の Session の行に、網からの early media で呼び出したと表示されます。
2. もう一度 `phone1` で `phone2` にかけて応答させ、次に `phone3` で `phone1` にかけます。`phone1` のカードに、新しい着信が `waiting` と表示されます。
3. **Answer** を押すと、`phone2` との通話を保留にして `phone3` に応答します。**Swap** で通話を切り替えます。
4. **Merge** を押すと会議ができます。3 台のカードの **Conference** の行に、3 人の利用者が並びます。

- 通話のシーケンス図には、通話のダイアログと並んで、AS と MRFC のダイアログも描かれます。どちらも P-Charging-Vector の icid-value が同じだからです。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| MRFC | PCMU だけで、トランスコードをしない。PCMU の無い offer には Warning を付けた 488 を返す。用意してある音声だけを流し、Cr が無い。Contact はその台の URI で、GRUU ではない。 |
| MRFP | 混ぜ合わせは PCMU だけを扱い、ジッタのために 3 フレームだけをためる。CSRC の一覧を書かず、RTCP も送らない。 |
| AS。呼び出し音 | プリコンディションの付いた着信者の 183 をそのまま発信者に渡し、発信者のプリコンディションを確かめずに呼び出し音を始める。 |
| AS。会議 | AS が受け持つ利用者は誰でも会議を作れ、会議のポリシーが無い。参加者を外すようフォーカスに頼む REFER は 403 で断る。 |

```sh
grep -rn "SIMPLIFIED:" internal/mmtel internal/mrfc internal/netann internal/agw
```

- PCMU だけなのはなぜか。会議で混ぜるのは復号した音声で、ここには AMR の復号器が無いからです。UE シミュレータは PCMU を offer に入れます。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/mrf.md#このプロジェクトで簡略化していること)。項目ごとの仕様の条項と理由は手順書の表にあります。

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| AS と MRFC のダイアログ | `internal/mmtel` の `mrf.go`、`announcement.go` |
| 着信側の AS の呼び出し音 | `internal/mmtel` の `cat.go`、`catdata.go` |
| AS の CW | `internal/mmtel/waiting.go` |
| フォーカスと会議イベントパッケージ | `internal/mmtel` の `conference.go`、`conferenceevent.go` |
| MRFC と、RFC 4240 の Request-URI | `internal/mrfc`、`internal/netann` |
| MRFP。ターミネーション、音声ガイダンス、混ぜ合わせ | `internal/agw` |
| Swap、Merge と、会議の UE の側 | `internal/ue` の `phone_conference.go`、`conference.go` |

- `internal/mmtel` の `mrf_network_test.go` と `conference_network_test.go` が、同じ流れを 1 つのプロセスの中の IMS 全体で動かします。

> 手順書 [「コードの場所」](../../../docs/guide/ja/mrf.md#コードの場所)

---

[layout: exercise]

## 演習

1. UE 1 の発信を規制して UE 2 にかけます。AS から MRFC への ACK から、MRFC の BYE までの時間を測ります。何秒で、それを決めているのは何でしょうか。
2. 加入者 2 に呼び出し音 `cat1` を設定してかけます。呼び出し音だと示す SDP の属性と、それを認めるヘッダはどれでしょうか。
3. 会議のあいだ、MRFP と agw の間の RTP のストリームを数えます。どちら向きにも 3 つあるのはなぜでしょうか。
4. 会議イベントパッケージの NOTIFY を開きます。`<endpoint>` の `entity` が、その UE の Contact ではないのはなぜでしょうか。
5. 終わった会議の URI に発信します。何が返るでしょうか。

1 では、「実行する」のスライドの形で `ue xcap` と `ue call` を実行します。

---

[layout: answers]

## 演習の答え

1. 1.08 秒ほどです。`notice` の WAV ファイルの長さで、再生が終わると MRFC が BYE を送ります。
2. 示すのは `a=content:g.3gpp.cat`、認めるのは `P-Early-Media: sendonly` です。
3. 参加者が 3 人だからです。それぞれが自分の音声を MRFP に送り、ほかの 2 人の和を受け取ります。
4. Contact はその UE のアドレスで、NOTIFY はほかの網の購読者を含む、会議のすべての購読者に届くからです。
5. 404 です。作った人が抜けた時点で会議は終わり、その URI も残っていません。

---

[layout: exercise]

## 振り返り

1. 何を流すかを決めるのはどのノードで、流すのはどのノードですか。
2. 音声ガイダンスを頼む INVITE に SDP が付かないのはなぜですか。
3. 通話に応答する前に、発信者はどうやって音声を聞くのですか。
4. AS が PRACK を待ってから MRFC に ACK を送るのはなぜですか。
5. AS は、フォーカス宛ての INVITE と、利用者のサービスのための INVITE をどうやって見分けますか。
6. MRFP は会議の参加者それぞれに何を送りますか。

---

[layout: answers]

## 振り返りの答え

1. 決めるのは MMTel AS で、Mr' で頼みます。MRFC がその要求を受け、MRFP が音声を作ります。
2. MRFC が MRFP のターミネーションの offer を 200 で返し、AS が ACK で答えるためです。こうして AS が再生を始める時機を決められます。
3. AS が発信者の offer に MRFP の SDP で自分から答え、`P-Early-Media: sendonly` で early media を認めるからです。
4. 発信者が SDP を受け取る前に、音声ガイダンスのフレームが失われないようにするためです。
5. P-Served-User で見分けます。フォーカスに INVITE を届けるフィルタ条件は無いので、フォーカス宛てのものには付きません。
6. ほかの参加者が送る音声の和です。参加者ごとに 1 つずつ作ります。

---

## 次の回

- 次は VoLTE の回です。アクセス網、IMS-AKA、Rx でのポリシー制御、VoLTE の通話を扱います。
- 網どうしの境界と、参加者が両方の網にいる会議は、最後の回で [網間接続の手順書](../../../docs/guide/ja/interconnect.md) と一緒に読みます。

```sh
docker compose --profile ue --profile phones down -v --remove-orphans
```

- このコマンドは、両方のプロファイルの UE とノードを止め、イメージは残します。次の回を、ビルドし直さずに始められます。

> 手順書の一覧は [docs/guide/](../../../docs/guide/) にあります。
