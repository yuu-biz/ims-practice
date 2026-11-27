---
title: SMS の流れ
footer: ims-practice 勉強会 6. SMS
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: ue1, label: UE 1, role: 送信者, at: [0, 0]}
  - {id: ue2, label: UE 2, role: 受信者, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: 最初の中継点, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: ホーム網の入口, at: [2, 0]}
  - {id: scscf, label: S-CSCF, role: iFC を評価する, at: [2, 1]}
  - {id: hss, label: HSS, role: 加入者のデータ, at: [3, 0]}
  - {id: ipsmgw, label: IP-SM-GW, role: SMS の AS, at: [3, 1]}
  - {id: sc, label: SC, role: 預かって配達する, at: [4, 1]}
events:
  - id: r1-ue2-p
    msg: REGISTER
    from: ue2
    to: pcscf
    proto: SIP
    note: (2 回目)
    fields:
      - Contact: 機能タグ `+g.3gpp.smsip` が付く。IP で SMS を受けられる印
  - id: r1-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (2 回目)
  - id: r1-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (2 回目)
  - id: r1ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: r1ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: r1ok-p-ue2
    msg: 200 OK
    from: pcscf
    to: ue2
    proto: SIP
  - id: scscf-ifc0
    do: 優先度 0 の iFC に一致する
    at: scscf
    fields:
      - 条件: REGISTER のすべて
      - 送り先: IP-SM-GW。ServiceInfo に利用者の MSISDN
      - DefaultHandling: SESSION_CONTINUED。IP-SM-GW が答えなくても登録は残る
  - id: tp-register
    msg: REGISTER
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (第三者)
    fields:
      - Request-URI: iFC の ServerName `sip:ipsmgw...:5060`
      - From, Contact: S-CSCF
      - To: UE 2 の公開の識別子
      - 本文: "`<service-info>` に MSISDN `12025550102`"
  - id: tp-ok
    msg: 200 OK
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (第三者)
    fields:
      - Contact: 登録を並べる。`expires=3600`
  - id: isub-ip-i
    msg: SUBSCRIBE
    from: ipsmgw
    to: icscf
    proto: SIP
    fields:
      - Event: "`reg`"
      - Expires: 3761。登録の 3600 秒に 161 秒を足した値
      - P-Asserted-Identity: IP-SM-GW の URI
  - id: isub-i-s
    msg: SUBSCRIBE
    from: icscf
    to: scscf
    proto: SIP
  - id: lir-r
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 2)
  - id: lia-r
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 2)
    fields:
      - Server-Name: UE 2 の S-CSCF
  - id: isubok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: isubok-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
  - id: inotify
    msg: NOTIFY
    from: scscf
    to: ipsmgw
    proto: SIP
    fields:
      - Subscription-State: "`active`"
      - 本文: reginfo。連絡先に `<unknown-param name="+g.3gpp.smsip">`
  - id: inotify-ok
    msg: 200 OK
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (NOTIFY)
  - id: ipsmgw-check
    do: SMS を受けられる連絡先を探す
    at: ipsmgw
    fields:
      - 探すもの: "`+g.3gpp.smsip` を持つ active の連絡先"
      - あれば: HSS に自分を登録する
      - 無ければ: PUR を送らない
  - id: udr
    msg: UDR
    from: ipsmgw
    to: hss
    proto: Sh
    fields:
      - Data-Reference: IMSI (32)
  - id: uda
    msg: UDA
    from: hss
    to: ipsmgw
    proto: Sh
    fields:
      - User-Data: UE 2 の IMSI。SGd は受信者を IMSI で示す
  - id: pur
    msg: PUR
    from: ipsmgw
    to: hss
    proto: Sh
    fields:
      - User-Identity: MSISDN `12025550102`
      - Data-Reference: SMSRegistrationInfo (24)
      - Sh-User-Data: "`<IP-SM-GW-Number>` に `12025550199`"
  - id: pua
    msg: PUA
    from: hss
    to: ipsmgw
    proto: Sh
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: m1-ue1-p
    msg: MESSAGE
    from: ue1
    to: pcscf
    proto: SIP
    note: (1 通目)
    fields:
      - Request-URI, To: "`tel:+12025550100`。受信者ではなく SC"
      - Content-Type: "`application/vnd.3gpp.sms`。ボディは 47 オクテット"
      - Route: P-CSCF と UE 1 の Service-Route
  - id: m1-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (1 通目)
    fields:
      - P-Asserted-Identity: P-CSCF が入れる
      - Record-Route: 付けない。あとにダイアログが続かない
  - id: m1-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (1 通目)
    fields:
      - Route: 先頭に IP-SM-GW、次に `odi` の付いた S-CSCF
      - P-Asserted-Identity: UE 1 の SIP URI と tel URI
  - id: scscf-ifc1
    do: 優先度 1 の iFC に一致する
    at: scscf
    fields:
      - 条件: 登録済みの利用者が送る、SMS のボディの MESSAGE
      - P-Asserted-Identity: UE 1 の tel URI を加える。IP-SM-GW が SC に番号を伝えるため
      - P-Served-User: "`sescase=orig;regstate=reg`"
  - id: m1a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (1 通目)
    fields:
      - 意味: MESSAGE を受け取った。まだ何も届けていない
  - id: m1a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (1 通目)
  - id: m1a-p-ue1
    msg: "202"
    from: pcscf
    to: ue1
    proto: SIP
    note: (1 通目)
  - id: ipsmgw-rp
    do: RP-DATA を確かめる
    at: ipsmgw
    fields:
      - デコード: できるか
      - RP-DA: SC の番号 `12025550100` か
      - P-Asserted-Identity: tel URI があるか
      - 満たさないとき: submit report を RP-ERROR にする
  - id: ofr
    msg: OFR
    from: ipsmgw
    to: sc
    proto: SGd
    fields:
      - SC-Address: "`12025550100`。UE が RP-DA に書いた番号"
      - User-Identifier: 送信者の MSISDN `12025550101`
      - SM-RP-UI: UE 1 の SMS-SUBMIT をそのまま
      - Destination-Realm: "`ims.mnc001.mcc001.3gppnetwork.org`。Destination-Host は無い"
  - id: srr1
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (受け付け)
    fields:
      - MSISDN: 受信者の `12025550102`
      - 尋ねること: その番号を持つ利用者がいるか
  - id: sra1
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (受け付け)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - IP-SM-GW-Name: UE 2 を担当する IP-SM-GW。R5 の登録から
      - User-Name: UE 2 の IMSI
  - id: ofa
    msg: OFA
    from: sc
    to: ipsmgw
    proto: SGd
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - SM-RP-UI: SMS-SUBMIT-REPORT。SC のタイムスタンプ TP-SCTS が入る
  - id: m2-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (2 通目)
    fields:
      - Request-URI: UE 1 の SIP URI。手順 1 の P-Asserted-Identity から
      - Accept-Contact: "`*;+g.3gpp.smsip;require;explicit`"
      - Request-Disposition: "`fork`"
      - In-Reply-To: 手順 1 の MESSAGE の Call-ID
      - ボディ: RP-ACK。中に SMS-SUBMIT-REPORT
  - id: m2-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (2 通目)
  - id: m2-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (2 通目)
  - id: m2-p-ue1
    msg: MESSAGE
    from: pcscf
    to: ue1
    proto: SIP
    note: (2 通目)
  - id: lir-m2
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 1)
  - id: lia-m2
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 1)
    fields:
      - Server-Name: UE 1 の S-CSCF
  - id: ue1-report
    do: submit report を照らし合わせる
    at: ue1
    fields:
      - In-Reply-To: 自分が送ったショートメッセージの Call-ID
      - 記録すること: SC が受け付けた。ログに `"msg":"submit report"`
  - id: m2a-ue1-p
    msg: 200 OK
    from: ue1
    to: pcscf
    proto: SIP
    note: (2 通目)
  - id: m2a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (2 通目)
  - id: m2a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (2 通目)
  - id: m2a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (2 通目)
  - id: sc-make
    do: SMS-DELIVER を作る
    at: sc
    fields:
      - TP-OA: 送信者の番号 `12025550101`
      - TP-SCTS: メッセージを受け取った時刻
      - TP-SRI: 立てる。UE 1 が status report を頼んだ
      - 本文: そのまま
  - id: srr2
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (配達)
    fields:
      - MSISDN: 受信者の `12025550102`
      - 尋ねるもの: 配達する SMS-GMSC。1 回目は受け付けた SMS-IWMSC
  - id: sra2
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (配達)
    fields:
      - IP-SM-GW-Name: また UE 2 の IP-SM-GW
      - User-Name: UE 2 の IMSI
  - id: tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    fields:
      - Destination-Host: SRA が返した IP-SM-GW。TFR は必ず宛先を書く
      - User-Name: UE 2 の IMSI `001010000000002`
      - SC-Address: "`12025550100`。RP-OA になる"
      - SM-RP-UI: SMS-DELIVER
  - id: ipsmgw-find
    do: IMSI から利用者を見つける
    at: ipsmgw
    fields:
      - User-Name: R5 の UDA で読んだ IMSI と照らし合わせる
      - 宛先: "`tel:+12025550102`。IMSI に結び付いた tel URI"
      - 配達の状態: 共有の `Store` に置いてから MESSAGE を送る
  - id: m3-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (3 通目)
    fields:
      - Request-URI: "`tel:+12025550102`"
      - Request-Disposition: "`no-fork`"
      - P-Asserted-Identity: IP-SM-GW。delivery report の宛先になる
      - ボディ: 網からの RP-DATA。中に SMS-DELIVER
  - id: m3-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (3 通目)
  - id: m3-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (3 通目)
  - id: m3-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (3 通目)
  - id: lir-m3
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 2)
  - id: lia-m3
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 2)
    fields:
      - Server-Name: UE 2 の S-CSCF
  - id: scscf-mt
    do: 着信側の条件と連絡先を調べる
    at: scscf
    fields:
      - 優先度 2: 一致しない。P-Asserted-Identity が IP-SM-GW
      - Accept-Contact: UE 2 の連絡先は `+g.3gpp.smsip` を持つので残る
      - Request-Disposition: "`no-fork` 連絡先を 1 つだけ選ぶ"
  - id: m3a-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (3 通目)
    fields:
      - 意味: MESSAGE が届いたことだけを示す
  - id: m3a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (3 通目)
  - id: m3a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (3 通目)
  - id: m3a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (3 通目)
  - id: m4-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (4 通目)
    fields:
      - Request-URI: IP-SM-GW。手順 8 の P-Asserted-Identity から
      - In-Reply-To: 手順 8 の MESSAGE の Call-ID
      - ボディ: 端末からの RP-ACK。中に SMS-DELIVER-REPORT
  - id: m4-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (4 通目)
  - id: m4-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (4 通目)
  - id: scscf-dr
    do: UE 2 の優先度 1 の iFC に一致する
    at: scscf
    fields:
      - 条件: UE 2 が送る、SMS のボディの MESSAGE
      - Route: 手順 3 と同じ。IP-SM-GW と `odi`
  - id: m4a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (4 通目)
  - id: m4a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (4 通目)
  - id: m4a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (4 通目)
  - id: ipsmgw-dr
    do: 配達を見つけて終わらせる
    at: ipsmgw
    fields:
      - In-Reply-To: 手順 8 の Call-ID で `Store` から配達を見つける
      - 受けるノード: どの IP-SM-GW でもよい。配達の状態は共有の `Store` にある
  - id: tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - 意味: ショートメッセージが届いた
  - id: sc-sr
    do: status report を作る
    at: sc
    fields:
      - 宛先: 送信者の番号 `+12025550101`
      - TP-MR: SMS-SUBMIT と同じ値
      - TP-ST: 0x00。受信者がメッセージを受け取った
  - id: srr3
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (status report)
    fields:
      - MSISDN: 送信者の `12025550101`
  - id: sra3
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (status report)
    fields:
      - IP-SM-GW-Name: UE 1 を担当する IP-SM-GW
      - User-Name: UE 1 の IMSI
  - id: tfr3
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (status report)
    fields:
      - User-Name: UE 1 の IMSI `001010000000001`
      - SM-RP-UI: SMS-STATUS-REPORT
  - id: m5-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (5 通目)
    fields:
      - Request-URI: "`tel:+12025550101`"
      - Request-Disposition: "`no-fork`"
      - ボディ: RP-DATA。中に SMS-STATUS-REPORT
      - TP-ST: 0x00
  - id: m5-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (5 通目)
  - id: m5-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (5 通目)
  - id: m5-p-ue1
    msg: MESSAGE
    from: pcscf
    to: ue1
    proto: SIP
    note: (5 通目)
  - id: lir-m5
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 1)
  - id: lia-m5
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 1)
  - id: m5a-ue1-p
    msg: 200 OK
    from: ue1
    to: pcscf
    proto: SIP
    note: (5 通目)
  - id: m5a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (5 通目)
  - id: m5a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (5 通目)
  - id: m5a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (5 通目)
  - id: m6-ue1-p
    msg: MESSAGE
    from: ue1
    to: pcscf
    proto: SIP
    note: (6 通目)
    fields:
      - In-Reply-To: 5 通目の MESSAGE の Call-ID
      - ボディ: 端末からの RP-ACK
  - id: m6-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (6 通目)
  - id: m6-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (6 通目)
  - id: m6a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (6 通目)
  - id: m6a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (6 通目)
  - id: m6a-p-ue1
    msg: "202"
    from: pcscf
    to: ue1
    proto: SIP
    note: (6 通目)
  - id: tfa3
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (status report)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: a1-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (A1)
    fields:
      - User-Name: UE 2 の IMSI
  - id: a1-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (A1)
  - id: a1-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (A1)
  - id: a1-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (A1)
  - id: a1-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (A1)
  - id: a2-ue2-p
    msg: "480"
    from: ue2
    to: pcscf
    proto: SIP
    note: (A2)
    fields:
      - 意味: Temporarily Unavailable。届いたが、今は受けられない
      - delivery report: 送らない
  - id: a2-p-s
    msg: "480"
    from: pcscf
    to: scscf
    proto: SIP
    note: (A2)
  - id: a2-s-i
    msg: "480"
    from: scscf
    to: icscf
    proto: SIP
    note: (A2)
  - id: a2-i-ip
    msg: "480"
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (A2)
  - id: a3-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (A3)
    fields:
      - Experimental-Result: DIAMETER_ERROR_ABSENT_USER (5550)
  - id: ipsmgw-unri
    do: UNRI を立てる
    at: ipsmgw
    fields:
      - 入力: UE 2 の 480
      - UNRI: UE 2 について立てる。IMS での失敗を知るのはこのノードだけ
  - id: a4-rdr
    msg: RDR
    from: sc
    to: hss
    proto: S6c
    note: (A4)
    fields:
      - SM-Delivery-Cause: ABSENT_USER
  - id: hss-absent
    do: MWD に SC を入れる
    at: hss
    fields:
      - MWD: SC のアドレスを入れる
      - UNRI: 立てる。そのあいだ SRA に serving node を入れない
  - id: a4-rda
    msg: RDA
    from: hss
    to: sc
    proto: S6c
    note: (A4)
  - id: a5-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (A5)
    fields:
      - 中身: 何でもよい。ここでは UE 2 自身のショートメッセージ
  - id: a5-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (A5)
  - id: a5-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (A5)
  - id: a5a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (A5)
  - id: a5a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (A5)
  - id: a5a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (A5)
  - id: ipsmgw-present
    do: UE 2 が戻ったと分かる
    at: ipsmgw
    fields:
      - 入力: UE 2 の要求が届いた
      - UNRI: 落とし、HSS に知らせる
  - id: a6-rfsm
    msg: Ready-For-SM
    from: ipsmgw
    to: hss
    proto: S6c
    note: (A6)
    fields:
      - コマンドコード: 16777214。このプロジェクト独自
      - Alert-Reason: UE_PRESENT
      - User-Identifier: UE 2 の MSISDN
      - IP-SM-GW-Number: 知らせる IP-SM-GW
  - id: hss-present
    do: UNRI を落とす
    at: hss
    fields:
      - 確かめること: UE 2 が登録している IP-SM-GW の番号か
      - UNRI: 落とす
      - 知らせる先: MWD のサービスセンタすべて
  - id: a6-ans
    msg: Ready-For-SM
    from: hss
    to: ipsmgw
    proto: S6c
    note: (答え)
  - id: a7-alr
    msg: ALR
    from: hss
    to: sc
    proto: S6c
    note: (A7)
    fields:
      - Destination-Host: "`service_centres` で引いた SC の名前"
  - id: a7-ala
    msg: ALA
    from: sc
    to: hss
    proto: S6c
    note: (A7)
    fields:
      - 結果: 答えた SC のアドレスは MWD から消える
  - id: sc-due
    do: 預かったものをすぐ配達する
    at: sc
    fields:
      - きっかけ: ALR
      - 再送: 1 時間後の `retry_delays` を待たない
  - id: a8-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (A8)
  - id: a8-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (A8)
    fields:
      - IP-SM-GW-Name: また返る。UNRI が落ちたから
      - MWD-Status: たいてい 0。SC は MWD にあり、印は立っていない
  - id: a8-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (A8)
    fields:
      - SM-RP-UI: 預かっていた SMS-DELIVER
  - id: a8-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (A8)
  - id: a8-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (A8)
  - id: a8-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (A8)
  - id: a8-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (A8)
  - id: a8a-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (A8)
  - id: a8a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (A8)
  - id: a8a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (A8)
  - id: a8a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (A8)
  - id: a9-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (A9)
    fields:
      - ボディ: RP-ACK。delivery report
  - id: a9-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (A9)
  - id: a9-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (A9)
  - id: a9a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (A9)
  - id: a9a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (A9)
  - id: a9a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (A9)
  - id: a9-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (A9)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: b1-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (B1)
  - id: b1-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (B1)
  - id: b1-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (B1)
  - id: b1-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (B1)
  - id: b1-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (B1)
  - id: b1a-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (B1)
    fields:
      - 意味: ショートメッセージはここまで届いている
  - id: b1a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (B1)
  - id: b1a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (B1)
  - id: b1a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (B1)
  - id: b2-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (B2)
    fields:
      - ボディ: RP-ERROR。RP-Cause 22 (memory capacity exceeded)
      - TP-FCS: "`0xD3`"
      - UE 2 の状態: 中身を残さず、網へ知らせを送るべき状態になる
  - id: b2-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (B2)
  - id: b2-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (B2)
  - id: b2a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (B2)
  - id: b2a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (B2)
  - id: b2a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (B2)
  - id: b3-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (B3)
    fields:
      - Experimental-Result: DIAMETER_ERROR_SM_DELIVERY_FAILURE (5555)
      - SM-Enumerated-Delivery-Failure-Cause: MEMORY_CAPACITY_EXCEEDED
  - id: b4-rdr
    msg: RDR
    from: sc
    to: hss
    proto: S6c
    note: (B4)
    fields:
      - SM-Delivery-Cause: UE_MEMORY_CAPACITY_EXCEEDED
  - id: hss-mcef
    do: MCEF を立てる
    at: hss
    fields:
      - MWD: SC のアドレスを入れる
      - MCEF: 立てる
      - UNRI: 立てない。UE には届いており、入れる場所が無いだけ
  - id: b4-rda
    msg: RDA
    from: hss
    to: sc
    proto: S6c
    note: (B4)
  - id: b5-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (B5)
    fields:
      - ボディ: RP-SMMA。メモリに空きができた知らせ
      - Request-URI: 蓄えられなかったショートメッセージを運んだ IP-SM-GW
  - id: b5-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (B5)
  - id: b5-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (B5)
  - id: b5a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (B5)
    fields:
      - 答える時: HSS に渡す前にすぐ
  - id: b5a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (B5)
  - id: b5a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (B5)
  - id: b6-rfsm
    msg: Ready-For-SM
    from: ipsmgw
    to: hss
    proto: S6c
    note: (B6)
    fields:
      - Alert-Reason: UE_MEMORY_AVAILABLE
      - MAP なら: alert reason が memoryAvailable の READY_FOR_SM
  - id: hss-mem
    do: MCEF と UNRI を落とす
    at: hss
    fields:
      - MCEF, UNRI: 落とす
      - 知らせる先: MWD のサービスセンタ
  - id: b6-ans
    msg: Ready-For-SM
    from: hss
    to: ipsmgw
    proto: S6c
    note: (答え)
  - id: b7-alr
    msg: ALR
    from: hss
    to: sc
    proto: S6c
    note: (B7)
  - id: b7-ala
    msg: ALA
    from: sc
    to: hss
    proto: S6c
    note: (B7)
  - id: b8-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (B8)
    fields:
      - ボディ: RP-ACK。B5 の知らせへの答え
      - 送る時: HSS が知らせを受け取ったあと
      - HSS が受け取れないとき: RP-ACK の代わりに RP-ERROR
  - id: b8-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (B8)
  - id: b8-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (B8)
  - id: b8-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (B8)
  - id: d1-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (再送)
  - id: d1-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (再送)
    fields:
      - IP-SM-GW-Name: 返る。メモリ不足では UNRI が立たない
      - MWD-Status: ビット 2 の MCEF-Set が立つ。ビット 0 は落ちている
  - id: d2-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (D2)
  - id: d2-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (D2)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: sc-report
    do: 配達を HSS に報告する
    at: sc
    fields:
      - 入力: SRA の MWD-Status に MCEF-Set
      - 決めたこと: 配達の成功を 1 回だけ報告する
  - id: d3-rdr
    msg: RDR
    from: sc
    to: hss
    proto: S6c
    note: (D3)
    fields:
      - SM-Delivery-Cause: SUCCESSFUL_TRANSFER
  - id: hss-success
    do: MCEF を落とす
    at: hss
    fields:
      - MCEF: 落とす
      - MWD: SC のアドレスを消す。残るほかの SC に知らせる
  - id: d3-rda
    msg: RDA
    from: hss
    to: sc
    proto: S6c
    note: (D3)
  - id: f-ofr
    msg: OFR
    from: ipsmgw
    to: sc
    proto: SGd
  - id: f-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    fields:
      - MSISDN: "`12025550198`。この番号を持つ加入者はいない"
  - id: f-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    fields:
      - Experimental-Result: DIAMETER_ERROR_USER_UNKNOWN (5001)
  - id: f-ofa
    msg: OFA
    from: sc
    to: ipsmgw
    proto: SGd
    fields:
      - Experimental-Result: DIAMETER_ERROR_SM_DELIVERY_FAILURE (5555)
      - SM-Enumerated-Delivery-Failure-Cause: INVALID_SME-ADDRESS
  - id: frep-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (submit report)
    fields:
      - ボディ: RP-ERROR。RP-Cause 1 (unassigned number)
      - TP-FCS: 0xC3 (invalid SME address)
  - id: frep-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (submit report)
  - id: frep-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (submit report)
  - id: frep-p-ue1
    msg: MESSAGE
    from: pcscf
    to: ue1
    proto: SIP
    note: (submit report)
  - id: g-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (加入者 3)
    fields:
      - MSISDN: "`12025550103`。加入者 3 はいるが、登録していない"
  - id: g-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (加入者 3)
    fields:
      - Experimental-Result: DIAMETER_ERROR_ABSENT_USER (5550)
      - 理由: 加入者 3 には IP-SM-GW が登録されていない
  - id: hss-keep
    do: MWD に SC を残す
    at: hss
    fields:
      - MWD: SC のアドレスを残す
      - 配達するとき: 加入者 3 が登録したときの知らせで
```

---

# SMS の流れ

ims-practice 勉強会 第 6 回（SMS over IP。IP-SM-GW、SC、3 種類の報告、預かりと知らせ）

---

[layout: summary]

## この回で分かること

最初の問い。ショートメッセージは電話番号に宛てて送ります。その番号の UE を、IMS の中で誰が見つけるのでしょうか。

- 利用者がショートメッセージを受けられることを、IP-SM-GW が知るしくみ。
- S-CSCF が、ショートメッセージを IP-SM-GW に渡す方法。
- メッセージが、もう一方の UE に届くまでの流れ。
- 送った側と受けた側が、メッセージがどうなったかを知る方法。
- UE が受け取れないショートメッセージを SC が預かるしくみと、もう一度送るきっかけ。
- MESSAGE のボディの読み方。

> 手順書 [SMS の流れを追う](../../../docs/guide/ja/sms.md)（`docs/guide/ja/sms.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.341 V19.0.0、TS 23.204 V19.1.0、TS 24.011 V20.0.0、TS 23.040 V20.0.0 などです。

---

## SMS over IP の位置づけ

ショートメッセージは、運び方が変わっても中身は同じ 2 つの層でできています。

```text
 SIP MESSAGE   Content-Type: application/vnd.3gpp.sms      TS 24.341
  +-- RP       RP-DATA, RP-ACK, RP-ERROR                   TS 24.011
       +-- TP  SMS-SUBMIT, SMS-DELIVER, SMS-STATUS-REPORT  TS 23.040
            +-- text   GSM 7 bit, UCS2                     TS 23.038
```

- IMS では、SIP の MESSAGE のボディに入れて運びます。
- NAS で運ぶ方法もあります。その場合も、RP と TP は同じです。SIP が置き換えるのは、メッセージを運ぶ部分だけです。
- このプロジェクトは、ショートメッセージを SIP だけで運びます。NAS で運ぶ SMS（MSC、MME、SMSF を通るもの）は作っていません。主題が IMS だからです。

> 手順書 [「SMS over IP の位置づけ」](../../../docs/guide/ja/sms.md#sms-over-ip-の位置づけ)

---

## 登場するノード

```ims-scene
id: nodes
lead: SIP を中継する CSCF のほかに、SMS の網と話すノードが 2 つあります。IP-SM-GW と SC です。
steps:
  - id: ues
    title: UE 1 と UE 2（172.28.0.100、101）
    focus: [ue1, ue2]
    text: |
      UE 1 が送信者で、番号は `+1-202-555-0101` です。
      UE 2 が受信者で、番号は `+1-202-555-0102` です。
  - id: cscfs
    title: P-CSCF、S-CSCF、I-CSCF（40、30、20）
    focus: [pcscf, scscf, icscf]
    checkpoint: true
    text: |
      MESSAGE を中継します。S-CSCF は iFC を評価します。
      CSCF は SIP を中継するだけで、SMS を知りません。
  - id: ipsmgw
    title: IP-SM-GW（172.28.0.70）
    focus: [ipsmgw]
    text: |
      SMS over IP のアプリケーションサーバー（AS）です。SIP と SMS の網の言葉の両方を話します。
      番号は `+12025550199` です。
  - id: sc
    title: SC（172.28.0.71）
    focus: [sc]
    text: |
      サービスセンタです。ショートメッセージを、配達できるか有効期間が切れるまで預かります。
      アドレスは `+12025550100` です。IP-SM-GW とは SGd で話します。
  - id: hss
    title: HSS（172.28.0.10）
    focus: [hss]
    text: iFC を Cx で渡します。IP-SM-GW には Sh で、SC には S6c で答えます。
```

| ノード | アドレス | SMS での役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 送信者。`+1-202-555-0101`。 |
| UE 2 | 172.28.0.101 | 受信者。`+1-202-555-0102`。 |
| P-CSCF、S-CSCF、I-CSCF | 172.28.0.40、30、20 | MESSAGE を中継する。S-CSCF は iFC を評価する。 |
| IP-SM-GW | 172.28.0.70 | SMS over IP のアプリケーションサーバー（AS）。 |
| SC | 172.28.0.71 | サービスセンタ。ショートメッセージを、配達できるか有効期間が切れるまで預かり、配達する。 |
| HSS | 172.28.0.10 | iFC を渡し（Cx）、IP-SM-GW に Sh で、SC に S6c で答える。 |

- SC のアドレスは `+12025550100`、IP-SM-GW の番号は `+12025550199` です。
- CSCF は SIP を中継するだけです。ショートメッセージには、SIP と SMS の網の言葉の両方を話すノードが要ります。それが IP-SM-GW です。
- IP-SM-GW は、ショートメッセージを SGd で SC に渡します。UE 宛のものは、SC が SGd で戻します。SC は、宛先をどのノードが担当しているかを S6c で HSS に尋ねます。SMS-SC、SMS-IWMSC、SMS-GMSC は 1 つのノードです。

> 手順書 [「登場するノード」](../../../docs/guide/ja/sms.md#登場するノード) と [「AS が要る理由と SC の役割」](../../../docs/guide/ja/sms.md#as-が要る理由と-sc-の役割)。TS 24.341 5.3.3。TS 23.204 5.2.4。TS 23.040 4.1、6.1。

---

## S-CSCF が IP-SM-GW を選ぶしくみ

```ims-scene
id: ifc
lead: S-CSCF は SMS を知りません。HSS が渡した iFC に一致した要求を、IP-SM-GW へ送ります。
steps:
  - id: register
    title: REGISTER が優先度 0 に一致する
    only: [r1-i-s, scscf-ifc0]
    focus: [scscf]
    text: 登録が成功すると、S-CSCF は REGISTER に一致する条件を調べます。優先度 0 はすべての REGISTER に一致します。
  - id: third-party
    title: IP-SM-GW に第三者 REGISTER を送る
    show: [tp-register]
    checkpoint: true
    text: 本文に利用者の MSISDN が入ります。iFC の ServiceInfo の値です。
  - id: submit
    title: UE の MESSAGE が優先度 1 に一致する
    only: [m1-p-s, scscf-ifc1]
    focus: [scscf]
    text: 登録済みの利用者が送り、Content-Type が `application/vnd.3gpp.sms` の MESSAGE です。
  - id: submit-on
    title: MESSAGE が IP-SM-GW へ行く
    show: [m1-s-ip]
    checkpoint: true
    text: Request-URI の SC へは送りません。Route の先頭に IP-SM-GW を入れて送ります。
  - id: deliver
    title: IP-SM-GW の MESSAGE は優先度 2 に一致しない
    only: [m3-i-s, scscf-mt]
    focus: [scscf]
    text: 優先度 2 は利用者に宛てた MESSAGE の条件です。P-Asserted-Identity が IP-SM-GW のものは除きます。
  - id: deliver-on
    title: MESSAGE はそのまま UE 2 へ
    show: [m3-s-p]
    text: 否定の条件があるので、IP-SM-GW が送った MESSAGE は IP-SM-GW に戻りません。
```

S-CSCF は SMS を知りません。どの要求をどの AS へ送るかは、HSS が渡す iFC で決まります。

| 優先度 | 一致する要求 | 送り先 |
| --- | --- | --- |
| 0 | REGISTER のすべて | IP-SM-GW。利用者の MSISDN を付ける。 |
| 1 | 登録済みの利用者が送る MESSAGE で、Content-Type が `application/vnd.3gpp.sms` のもの | IP-SM-GW |
| 2 | 登録済みの利用者に宛てた同じ MESSAGE。IP-SM-GW が送ったものは除く | IP-SM-GW |

- iFC は `deploy/compose/hss.yaml` にあり、登録のときの SAA の User-Data で S-CSCF に届きます。手順書に YAML と XML を並べてあります。
- XML の ConditionTypeCNF が条件の組み合わせ方を決めます。同じ Group の SPT は OR、Group どうしは AND です。

> 手順書 [「フィルタ条件」](../../../docs/guide/ja/sms.md#フィルタ条件)。TS 29.228 B.2.2、B.2.3、Annex E。

---

## 利用者に宛てたショートメッセージ。否定の条件

`deploy/compose/hss.yaml` の優先度 2 の条件には、4 つの組があります。

```text
     メソッドが MESSAGE
 かつ Content-Type が application/vnd.3gpp.sms
 かつ セッションケースが TERMINATING_REGISTERED
 かつ P-Asserted-Identity が sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org ではない
```

- IP-SM-GW は、利用者に送る MESSAGE の P-Asserted-Identity に自分の URI を入れます。否定の条件があるので、その MESSAGE は IP-SM-GW に戻らず、ループになりません。
- この条件に一致するのは、ほかの網から届いたショートメッセージです。そのあと、利用者の IP-SM-GW が届けます。

> 手順書 [「フィルタ条件」](../../../docs/guide/ja/sms.md#フィルタ条件) と [IMS 網どうしを相互接続する「網をまたぐショートメッセージ」](../../../docs/guide/ja/interconnect.md#網をまたぐショートメッセージ)。TS 23.218 5.2.3 NOTE 5。TS 23.204 6.4a。

---

## 実行する

```sh
docker compose up -d --build

# 1 つ目の端末。UE 2 が登録したままで待つ。
docker compose run --rm ue2 register \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2

# 2 つ目の端末。UE 1 が送り、status report を頼む。
docker compose run --rm ue sms -status-report +12025550102 "Hello UE-B, this is UE-A."
```

- オプションは、番号と本文の前に書きます。
- UE 1 のログに `"msg":"submit report"` と `"msg":"status report"` が出ます。
- UE 2 のログに `"msg":"sms received"` が出ます。

> 手順書 [「ショートメッセージを送る」](../../../docs/guide/ja/sms.md#ショートメッセージを送る)。同じ操作を、`scripts/` にある SMS の e2e のスクリプトが行います。

---

## キャプチャを開く

```text
sip.CSeq.method in {"MESSAGE", "REGISTER"} || (sip && ip.addr == 172.28.0.70)
  || diameter.cmd.code in {302, 306, 307, 8388645, 8388646, 8388647, 8388648, 8388649, 16777214}
```

- 1 つのフィルタです。Wireshark では 1 行に書きます。
- REGISTER、MESSAGE とその応答、IP-SM-GW の SUBSCRIBE と NOTIFY、LIR、UDR、PUR が残ります。SGd と S6c の 5 つのコマンドと、このプロジェクト独自の Ready-For-SM も残ります。
- Wireshark は MESSAGE のボディを自分でデコードします。
  - Info の列の最後に `(RP) RP-DATA (MS to Network)` のように出ます。
  - 詳細に「GSM A-I/F RP」と「GSM SMS TPDU」の木があります。

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/sms.md#キャプチャを開く)。シーケンス図は手順書の [「送信と配達」](../../../docs/guide/ja/sms.md#送信と配達hello-ue-b-this-is-ue-a) にあります。

---

## 全体の形。MESSAGE は 6 通

| 順 | 向き | ボディ | 手順 |
| --- | --- | --- | --- |
| 1 | UE 1 から IP-SM-GW | RP-DATA と SMS-SUBMIT | 1 から 4 |
| 2 | IP-SM-GW から UE 1 | submit report。RP-ACK と SMS-SUBMIT-REPORT | 6 と 7 |
| 3 | IP-SM-GW から UE 2 | RP-DATA と SMS-DELIVER | 8 から 10 |
| 4 | UE 2 から IP-SM-GW | delivery report。RP-ACK と SMS-DELIVER-REPORT | 11 と 12 |
| 5 | IP-SM-GW から UE 1 | status report。RP-DATA と SMS-STATUS-REPORT | 14 |
| 6 | UE 1 から IP-SM-GW | delivery report。RP-ACK | 15 |

- 6 通はそれぞれ別の Call-ID を持ちます。
- SIP の応答（202 と 200）は、MESSAGE が届いたことしか伝えません。ショートメッセージがどうなったかは、報告の MESSAGE が伝えます。
- 2 通目と 3 通目は同時に出るので、キャプチャでの順番は入れ替わることがあります。status report を頼まなければ、5 通目と 6 通目はありません。

---

## R1 から R5。IP-SM-GW が利用者の登録を知る

```ims-scene
id: register
lead: 第三者 REGISTER だけでは、UE が SMS を受けられるかは分かりません。IP-SM-GW は reg イベントで確かめてから、HSS に自分を書きます。
steps:
  - id: register
    title: UE 2 が機能タグを付けて登録する（R1）
    only: [r1-ue2-p, r1-p-i, r1-i-s]
    active: r1-ue2-p
    checkpoint: true
    text: |
      登録は登録の回と同じです。1 回目の REGISTER と 401 は省き、2 回目だけを描きます。
      違いは Contact の `+g.3gpp.smsip` だけです。
  - id: ok
    title: 200 OK で登録が終わる
    show: [r1ok-s-i, r1ok-i-p, r1ok-p-ue2]
    detail: []
    checkpoint: true
    text: UE 2 はこれで登録されました。このあとの手順を、UE 2 は待ちません。
  - id: third-party
    title: S-CSCF が第三者 REGISTER を送る（R2）
    only: [tp-register]
    checkpoint: true
    text: From と Contact は S-CSCF、To は利用者です。本文の `<service-info>` に MSISDN が入ります。
  - id: third-party-ok
    title: IP-SM-GW が 200 OK を返す
    show: [tp-ok]
    checkpoint: true
    text: IP-SM-GW は MSISDN を保存します。R5 で利用者を示すのに使います。
  - id: subscribe
    title: IP-SM-GW が reg イベントを購読する（R3）
    only: [isub-ip-i, lir-r, lia-r, isub-i-s]
    active: isub-ip-i
    checkpoint: true
    text: SUBSCRIBE はホーム網の入口へ送ります。I-CSCF が LIR で S-CSCF を探します。
  - id: notify
    title: S-CSCF が NOTIFY で状態を知らせる（R4）
    hide: [lir-r, lia-r]
    show: [isubok-s-i, isubok-i-ip, inotify]
    active: inotify
    checkpoint: true
    text: NOTIFY は IP-SM-GW へ直接届きます。SUBSCRIBE への 200 OK は I-CSCF を通るので、NOTIFY との順は決まっていません。
  - id: check
    title: SMS を受けられる連絡先を探す
    show: [inotify-ok, ipsmgw-check]
    active: ipsmgw-check
    focus: [ipsmgw]
    checkpoint: true
    text: IP-SM-GW は 200 OK を返し、`+g.3gpp.smsip` を持つ active の連絡先を探します。
  - id: udr
    title: 利用者の IMSI を読む（R5、UDR と UDA）
    only: [udr, uda]
    active: uda
    detail: [udr, uda]
    checkpoint: true
    text: SGd で届くショートメッセージは受信者を IMSI で示します。IMSI は、そのときに利用者を見つける手がかりです。
  - id: pur
    title: HSS に自分を登録する（R5、PUR と PUA）
    only: [pur, pua]
    active: pur
    detail: [pur]
    text: Sh のコマンド 307 です。HSS は PUA の 2001 で答え、UE 2 のショートメッセージを運ぶ IP-SM-GW を覚えます。
```

| 見るもの | 見どころ |
| --- | --- |
| UE の REGISTER の Contact | 機能タグ `+g.3gpp.smsip`。 |
| S-CSCF から IP-SM-GW への REGISTER | 第三者 REGISTER。ボディの `<service-info>` に MSISDN。 |
| IP-SM-GW の SUBSCRIBE | Event は `reg`。I-CSCF を通り、LIR で S-CSCF が見つかる。 |
| NOTIFY の reginfo | `<unknown-param name="+g.3gpp.smsip">`。 |
| PUR | Data-Reference は 24。`<IP-SM-GW-Number>` に `12025550199`。 |

フィルタは `diameter.cmd.code == 307` です。

- 第三者 REGISTER だけでは、UE がショートメッセージを受けられるかが分かりません。だから IP-SM-GW は reg イベントを購読します。`+g.3gpp.smsip` を付けずに登録した UE には、PUR を送りません。

> 手順書 [「IP-SM-GW が利用者の登録を知る」](../../../docs/guide/ja/sms.md#ip-sm-gw-が利用者の登録を知る)。TS 24.341 5.3.2.2、5.3.3.2。TS 24.229 5.4.1.7、5.4.1.7A、5.7.1.1。TS 29.328 6.1.2、7.6.16。

---

## 1 から 4。UE 1 がショートメッセージを送る

```ims-scene
id: submit
lead: UE 1 はショートメッセージを受信者ではなく SC に宛てます。それを IP-SM-GW へ運ぶのは S-CSCF の iFC です。
steps:
  - id: send
    title: UE 1 が MESSAGE を送る（1）
    only: [m1-ue1-p]
    checkpoint: true
    text: Request-URI は SC の番号です。ボディは RP-DATA で、中の SMS-SUBMIT に受信者の番号と本文があります。
  - id: pcscf
    title: P-CSCF が転送する（2）
    show: [m1-p-s]
    text: |
      INVITE と同じく P-Asserted-Identity を入れ、Service-Route に沿って送ります。
      Record-Route は付けず、メディア中継ノードも通りません。
  - id: scscf
    title: MESSAGE が優先度 1 の iFC に一致する（3）
    show: [scscf-ifc1]
    focus: [scscf]
    zoom: [pcscf, scscf, ipsmgw]
    checkpoint: true
    text: S-CSCF は送信者を担当します。UE 1 の tel URI を 2 つ目の P-Asserted-Identity として加えます。
  - id: ipsmgw
    title: MESSAGE が IP-SM-GW に届く（3）
    show: [m1-s-ip]
    checkpoint: true
    text: Route の 2 つ目は `odi` の付いた S-CSCF 自身です。AS が要求を送り返せば、S-CSCF は次の iFC から続けます。IP-SM-GW は送り返しません。
  - id: accepted
    title: IP-SM-GW が 202 で受け付ける（4）
    show: [m1a-ip-s, m1a-s-p, m1a-p-ue1]
    active: m1a-ip-s
    text: |
      200 ではなく 202 です。まだ何も届けていないからです。
      ショートメッセージがどうなったかは、202 ではなく submit report が伝えます。
```

| 見るもの | 見どころ |
| --- | --- |
| Request-URI と To | `tel:+12025550100`。受信者ではなく、SC に宛てる。 |
| Content-Type とボディ | `application/vnd.3gpp.sms`。47 オクテット。 |
| P-CSCF を出たあと | P-Asserted-Identity が付く。Record-Route は付かない。 |
| S-CSCF を出たあとの Route | 先頭に IP-SM-GW。次に `odi` の付いた S-CSCF 自身の URI。 |
| S-CSCF を出たあとの P-Asserted-Identity | UE 1 の tel URI が加わる。 |
| IP-SM-GW の応答 | 202 Accepted。 |

- 通話の INVITE と同じもの。Route と P-Asserted-Identity。違うもの。Record-Route が無く、メディア中継ノードも通りません。
- 200 ではなく 202 なのは、まだ何も届けていないからです。AS が要求を送り返せば、`odi` で S-CSCF が次の iFC に進めます。IP-SM-GW は送り返しません。

> 手順書 [1](../../../docs/guide/ja/sms.md#1-ue-1-がショートメッセージを送る) から [4](../../../docs/guide/ja/sms.md#4-ip-sm-gw-が受け付ける202) と [「オリジナルダイアログ識別子」](../../../docs/guide/ja/sms.md#オリジナルダイアログ識別子)。TS 24.341 5.3.1.2、5.3.3.4.1。TS 24.229 5.2.6.3.7、5.4.3.2、5.4.3.4。RFC 3428 7。

---

## 5。SC が受け付ける（OFR、SRR、OFA）

```ims-scene
id: sc-accept
lead: IP-SM-GW はショートメッセージを SGd で SC に渡します。SC は受信者の番号を HSS に確かめてから受け付けます。
steps:
  - id: check
    title: IP-SM-GW が RP-DATA を確かめる
    only: [m1-s-ip, ipsmgw-rp]
    focus: [ipsmgw]
    checkpoint: true
    text: 202 を返したあとで確かめます。どれかが満たされないと、submit report は RP-ERROR になります。
  - id: ofr
    title: SC に渡す（OFR）
    only: [ofr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SGd のコマンド 8388645 です。SM-RP-UI には UE 1 の SMS-SUBMIT がそのまま入ります。
  - id: srr
    title: SC が受信者の番号を HSS に尋ねる（SRR）
    show: [srr1]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: S6c のコマンド 8388647 です。その番号を持つ利用者がいるかを確かめます。
  - id: sra
    title: HSS が UE 2 の IP-SM-GW を返す（SRA）
    show: [sra1]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: R5 の PUR で登録された IP-SM-GW です。番号を持つ加入者がいなければ、ここで DIAMETER_ERROR_USER_UNKNOWN が返ります。
  - id: ofa
    title: SC が受け付ける（OFA）
    show: [ofa]
    zoom: [ipsmgw, sc, hss]
    text: SC はタイムスタンプ TP-SCTS を付けて預かります。ここから先、ショートメッセージは SC のものです。
```

| 見るもの | 見どころ |
| --- | --- |
| OFR と OFA | IP-SM-GW が SMS-SUBMIT を SGd でサービスセンタに渡し、その答えに submit report が入る。 |
| SRR と SRA | サービスセンタが、受信者の番号を持つ利用者がいるかを S6c で HSS に尋ねる。 |

フィルタは `diameter.cmd.code in {8388645, 8388647} || sip.In-Reply-To` です。次のスライドの submit report も残ります。

- IP-SM-GW は 202 を送るとすぐに SC へ渡します。そのため、キャプチャでは OFR が 202 より先に来ることがあります。
- どの加入者も持たない番号には、SRA で DIAMETER_ERROR_USER_UNKNOWN が返ります。送信者は status report を待たずに、その場で知ります（「失敗するとき」）。

> 手順書 [4](../../../docs/guide/ja/sms.md#4-ip-sm-gw-が受け付ける202) と [5](../../../docs/guide/ja/sms.md#5-ip-sm-gw-が-sc-にショートメッセージを渡すofrsrrofa)。TS 23.204 6.3。TS 29.338 6.2.1。TS 23.040 8.2.2、6.1。

---

## 6 と 7。submit report が UE 1 へ戻る

```ims-scene
id: submit-report
lead: submit report は IP-SM-GW が UE 1 へ送る新しい MESSAGE です。AS が利用者に送る要求なので、ホーム網の入口の I-CSCF から入ります。
steps:
  - id: send
    title: IP-SM-GW が submit report を送る（6）
    only: [ofa, m2-ip-i]
    active: m2-ip-i
    text: OFA の SMS-SUBMIT-REPORT を RP-ACK に入れます。どのショートメッセージへの報告かを In-Reply-To が示します。
  - id: lir
    title: I-CSCF が UE 1 の S-CSCF を探す（LIR と LIA）
    show: [lir-m2, lia-m2]
    active: lia-m2
    text: 通話の着信と同じです。I-CSCF は Record-Route を付けません。
  - id: deliver
    title: submit report が UE 1 に届く
    hide: [ofa, lir-m2, lia-m2]
    show: [m2-i-s, m2-s-p, m2-p-ue1]
    active: m2-p-ue1
    detail: []
    checkpoint: true
    text: S-CSCF は着信側の要求として扱い、UE 1 の Path に沿って送ります。
  - id: match
    title: UE 1 が照らし合わせる
    show: [ue1-report]
    focus: [ue1]
    text: UE 1 は手順 1 で Call-ID を覚えていました。In-Reply-To と比べ、受け付けられたものとして記録します。
  - id: ok
    title: UE 1 が 200 を返す（7）
    show: [m2a-ue1-p, m2a-p-s, m2a-s-i, m2a-i-ip]
    active: m2a-ue1-p
    detail: []
    text: 200 は I-CSCF を通って IP-SM-GW に戻ります。応答は Via をたどるからです。
```

| 見るもの | 見どころ |
| --- | --- |
| IP-SM-GW の MESSAGE の Request-URI | UE 1 の SIP URI。 |
| Accept-Contact | `*;+g.3gpp.smsip;require;explicit`。 |
| In-Reply-To | 手順 1 の MESSAGE の Call-ID。 |
| ボディ | RP-ACK。中に SMS-SUBMIT-REPORT。 |
| 経路 | I-CSCF から入る。I-CSCF は LIR で UE 1 の S-CSCF を探す。 |

- IP-SM-GW は S-CSCF の Route を使いません。自分の要求に `Route: <sip:ims.mnc001.mcc001.3gppnetwork.org;lr>` を付けて、ホーム網の入口へ送ります。
- submit report と手順 8 の配達は一緒に送られます。キャプチャでは、それぞれの LIR と応答が入り混じります。

> 手順書 [6](../../../docs/guide/ja/sms.md#6-submit-report-が-ue-1-へ戻る) と [7](../../../docs/guide/ja/sms.md#7-ue-1-が-200-を返す)。TS 24.341 5.3.3.4.3、5.3.1.2。TS 24.229 5.7.3。

---

## 8。SC が配達を始める（SRR と TFR）

```ims-scene
id: sc-deliver
lead: SC は配達の前に、もう一度 HSS に尋ねます。TFR は受信者を IMSI で示します。
steps:
  - id: make
    title: SC が SMS-DELIVER を作る
    only: [sc-make]
    focus: [sc]
    text: SMS-SUBMIT の TP-DA に代わって、TP-OA に送信者の番号が入ります。TP-SCTS が増えます。
  - id: srr
    title: 配達する SMS-GMSC として尋ねる（SRR）
    show: [srr2]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: 1 回目は受け付けた SMS-IWMSC の問い合わせでした。そのあいだに利用者が移ったり、いなくなったりすることがあります。
  - id: sra
    title: また IP-SM-GW が返る（SRA）
    show: [sra2]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SRA は UE 2 を担当する IP-SM-GW と、UE 2 の IMSI を返します。
  - id: tfr
    title: SC が TFR で IP-SM-GW に渡す（8）
    show: [tfr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SGd のコマンド 8388646 です。この TFR への答えは配達が終わるまで返りません。
  - id: find
    title: IP-SM-GW が IMSI から UE 2 を見つける
    show: [ipsmgw-find]
    focus: [ipsmgw]
    text: R5 の UDA で読んだ IMSI を使います。配達の状態を共有の `Store` に置いてから、MESSAGE を送ります。
```

| 見るもの | 見どころ |
| --- | --- |
| TFR | サービスセンタが SMS-DELIVER を SGd で IP-SM-GW へ転送する。受信者は IMSI で表す。 |

- SMS-SUBMIT と SMS-DELIVER を比べます。TP-DA が TP-OA になり、TP-SCTS が増えています。
- TFR は宛先のノードを必ず Destination-Host に書きます。SRA が返した IP-SM-GW です。

> 手順書 [8](../../../docs/guide/ja/sms.md#8-sc-がメッセージを転送しip-sm-gw-が配達するtfr-と-sms-deliver)。TS 23.040 8.1.1。TS 29.338 6.2.2、6.3.1.2。

---

## 8 から 10。MESSAGE が UE 2 に届く

```ims-scene
id: mt
lead: UE 2 への MESSAGE も I-CSCF から入ります。S-CSCF は登録の機能タグで送る連絡先を選びます。
steps:
  - id: send
    title: IP-SM-GW が MESSAGE を送る（8）
    only: [m3-ip-i, lir-m3, lia-m3]
    active: m3-ip-i
    checkpoint: true
    text: 宛先は UE 2 の tel URI です。I-CSCF が LIR で UE 2 の S-CSCF を見つけます。
  - id: scscf
    title: S-CSCF が条件と連絡先を調べる（9）
    hide: [lir-m3, lia-m3]
    show: [m3-i-s, scscf-mt]
    active: scscf-mt
    focus: [scscf]
    text: |
      Accept-Contact を、UE 2 が登録した連絡先のパラメータと照らし合わせます。
      `+g.3gpp.smsip` を登録していない UE なら一致せず、S-CSCF は 480 を返します。
  - id: ue2
    title: MESSAGE が UE 2 に届く
    show: [m3-s-p, m3-p-ue2]
    active: m3-p-ue2
    detail: []
    text: S-CSCF は P-Called-Party-ID を入れ、UE 2 の Path に沿って送ります。
  - id: ok
    title: UE 2 が 200 を返す（10）
    show: [m3a-ue2-p, m3a-p-s, m3a-s-i, m3a-i-ip]
    active: m3a-ue2-p
    checkpoint: true
    text: 200 は MESSAGE が届いたことだけを示します。UE 2 が受け取ったかは delivery report が伝えます。
```

| 見るもの | 見どころ |
| --- | --- |
| Request-URI | `tel:+12025550102`。その IMSI に結び付いた tel URI。 |
| Request-Disposition | `no-fork`。 |
| ボディ | 網からの RP-DATA。中に SMS-DELIVER。TP-OA に送信者の番号。 |
| 優先度 2 の条件 | 一致しない。P-Asserted-Identity が IP-SM-GW。 |
| S-CSCF の動き | Accept-Contact を、UE 2 が登録した Contact のパラメータと照らし合わせる。 |
| UE 2 の応答 | 200。MESSAGE が届いたことだけを示す。 |

- `+g.3gpp.smsip` を登録していない UE には届かず、S-CSCF が 480 を返します。登録した機能タグをここで使います。

> 手順書 [8](../../../docs/guide/ja/sms.md#8-sc-がメッセージを転送しip-sm-gw-が配達するtfr-と-sms-deliver) から [10](../../../docs/guide/ja/sms.md#10-ue-2-が-200-を返す)、[9](../../../docs/guide/ja/sms.md#9-s-cscf-が発信者の希望で連絡先を選ぶ) に S-CSCF の照らし合わせ。TS 24.341 5.3.3.4.2、5.3.2.3。TS 24.229 5.4.3.3。RFC 3841 7.2.4、9.1。

---

## 11 と 12。delivery report

```ims-scene
id: delivery-report
lead: delivery report は UE 2 が発信する MESSAGE です。UE 2 自身の iFC が IP-SM-GW へ運び、TFR への答えがそこで SC に返ります。
steps:
  - id: send
    title: UE 2 が delivery report を送る（11）
    only: [m4-ue2-p]
    text: 宛先は手順 8 の P-Asserted-Identity にあった IP-SM-GW です。
  - id: scscf
    title: UE 2 の iFC に一致する（12）
    show: [m4-p-s, scscf-dr]
    active: scscf-dr
    focus: [scscf]
    text: SMS のボディを持つ、UE 2 が送る MESSAGE です。手順 3 とまったく同じ条件に一致します。
  - id: ipsmgw
    title: delivery report が IP-SM-GW に届く
    show: [m4-s-ip]
    detail: []
    checkpoint: true
    text: Route は IP-SM-GW と `odi` の付いた S-CSCF です。
  - id: accepted
    title: IP-SM-GW が 202 を返す
    show: [m4a-ip-s, m4a-s-p, m4a-p-ue2]
    active: m4a-ip-s
    detail: []
    text: 手順 4 と同じく 202 Accepted です。
  - id: find
    title: 配達を見つけて終わらせる
    show: [ipsmgw-dr]
    focus: [ipsmgw]
    text: In-Reply-To の Call-ID で、共有の `Store` から配達を見つけます。どの IP-SM-GW のノードが受けても、配達を終わらせられます。
  - id: tfa
    title: TFR に答えが返る（TFA）
    only: [tfr, ipsmgw-dr, tfa]
    active: tfa
    zoom: [ipsmgw, sc, hss]
    text: 手順 8 の TFR への答えはここで返ります。TFR は配達が終わるまで、ここでは最長 66 秒のあいだ待ちます。
```

| 見るもの | 見どころ |
| --- | --- |
| UE 2 の MESSAGE の Request-URI | IP-SM-GW。手順 8 の P-Asserted-Identity から取る。 |
| In-Reply-To | 手順 8 の MESSAGE の Call-ID。 |
| ボディ | 端末からの RP-ACK。中に SMS-DELIVER-REPORT。 |
| S-CSCF を出たあとの Route | 手順 3 と同じ。UE 2 自身の iFC に一致する。 |
| IP-SM-GW の応答 | 202 Accepted。 |

- delivery report は UE 2 が発信する MESSAGE です。だから UE 2 自身の iFC が IP-SM-GW へ運びます。
- 配達の途中の状態は、IP-SM-GW の `Store` にあります。delivery report をどの IP-SM-GW が受けても、配達を終わらせられます。

> 手順書 [11](../../../docs/guide/ja/sms.md#11-ue-2-が-delivery-report-を送る) と [12](../../../docs/guide/ja/sms.md#12-delivery-report-が-ue-2-の条件で-ip-sm-gw-に届く)。TS 24.341 5.3.2.4、5.3.3.4.1。TS 23.204 6.5。

---

## 13 から 15。status report

```ims-scene
id: status-report
lead: status report も、SC がふつうのショートメッセージと同じように配達します。宛先は送信者の UE 1 です。
steps:
  - id: make
    title: SC が status report を作る（13）
    only: [tfa, sc-sr]
    active: sc-sr
    focus: [sc]
    checkpoint: true
    text: UE 1 は SMS-SUBMIT の TP-SRR で status report を頼みました。手順 12 の TFA が届くとすぐに作ります。
  - id: srr
    title: 送信者について HSS に尋ねる（SRR と SRA）
    only: [srr3, sra3]
    active: sra3
    zoom: [ipsmgw, sc, hss]
    detail: [srr3, sra3]
    checkpoint: true
    text: 今度の受信者は UE 1 です。HSS は UE 1 を担当する IP-SM-GW を返します。
  - id: tfr
    title: TFR で IP-SM-GW に渡す（13）
    show: [tfr3]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SM-RP-UI は SMS-STATUS-REPORT です。受信者は UE 1 の IMSI で示します。
  - id: send
    title: status report が UE 1 に届く（14）
    only: [m5-ip-i, lir-m5, lia-m5, m5-i-s, m5-s-p, m5-p-ue1]
    active: m5-ip-i
    checkpoint: true
    text: |
      宛先は UE 1 の tel URI です。経路は手順 8 から 10 と同じで、I-CSCF が LIR で S-CSCF を探します。
      TP-MR が SMS-SUBMIT と同じなので、UE 1 はどのメッセージの報告かが分かります。
  - id: ok
    title: UE 1 が 200 を返す
    hide: [lir-m5, lia-m5]
    show: [m5a-ue1-p, m5a-p-s, m5a-s-i, m5a-i-ip]
    active: m5a-ue1-p
    detail: []
    text: ここでも 200 は MESSAGE が届いたことだけを示します。
  - id: report
    title: UE 1 が delivery report を送る（15）
    only: [m6-ue1-p, m6-p-s, m6-s-ip]
    active: m6-ue1-p
    text: 手順 11 の UE 2 と同じく、status report を受け取ったことを伝えます。UE 1 自身の iFC が IP-SM-GW へ運びます。
  - id: end
    title: 202 と TFA で配達が終わる
    show: [m6a-ip-s, m6a-s-p, m6a-p-ue1, tfa3]
    active: tfa3
    text: IP-SM-GW は 202 で答え、TFA で status report の配達が終わったことを SC に伝えます。
```

- UE 1 は SMS-SUBMIT の TP-SRR で、status report を頼みました。

| 見るもの | 見どころ |
| --- | --- |
| SRR と TFR | 手順 12 の TFA が届くと、SC はこの報告をショートメッセージと同じように、送信者の番号へ配達する。 |
| IP-SM-GW の MESSAGE | UE 1 の tel URI に宛てる。中に SMS-STATUS-REPORT。 |
| TP-MR | SMS-SUBMIT と同じ値。どのメッセージの報告かが分かる。 |
| TP-ST | 0x00。受信者がメッセージを受け取った。 |
| UE 1 の delivery report | status report を受け取ったことを伝える。 |

フィルタは `gsm_sms.tp-mti == 2` です。

- submit report は、SC が受け付けたことを伝えます。status report は、相手に届いたことを伝えます。

> 手順書 [「status report」](../../../docs/guide/ja/sms.md#status-report)。TS 24.341 5.3.3.4.4、5.3.1.3。TS 23.040 9.2.3.15。

---

## A1 から A4。UE 2 がショートメッセージを受け取らず、SC が預かる

```ims-scene
id: absent
lead: UE 2 が 480 で断ると、SC はショートメッセージを捨てずに預かります。HSS は SC のアドレスを MWD に書きます。
steps:
  - id: tfr
    title: 配達は手順 8 と同じように始まる（A1）
    only: [a1-tfr]
    zoom: [ipsmgw, sc, hss]
    text: "UE 2 の制御 API で `{\"available\": false}` にしてから、UE 1 がショートメッセージを送ります。"
  - id: message
    title: MESSAGE が UE 2 まで届く
    show: [a1-ip-i, a1-i-s, a1-s-p, a1-p-ue2]
    active: a1-p-ue2
    detail: []
    text: 経路は手順 8 と同じです。I-CSCF の LIR は省きます。
  - id: "480"
    title: UE 2 が 480 で答える（A2）
    show: [a2-ue2-p, a2-p-s, a2-s-i, a2-i-ip]
    active: a2-ue2-p
    checkpoint: true
    text: 相手には届いたが、その状態では要求を受けられない、という意味です。delivery report は来ません。
  - id: tfa
    title: IP-SM-GW が不在の加入者として答える（A3）
    only: [a1-tfr, a3-tfa]
    active: a3-tfa
    zoom: [ipsmgw, sc, hss]
    text: 480 は不在の加入者を表す誤りに写され、TFA の DIAMETER_ERROR_ABSENT_USER で SC に届きます。
  - id: unri
    title: IP-SM-GW が UNRI を立てる
    show: [ipsmgw-unri]
    focus: [ipsmgw]
    zoom: [ipsmgw, sc, hss]
    text: UE-Not-Reachable-for-IP です。UE 2 が戻ったときに、HSS へ知らせるための印です。
  - id: rdr
    title: SC が HSS に伝える（A4、RDR）
    show: [a4-rdr]
    zoom: [ipsmgw, sc, hss]
    text: S6c のコマンド 8388649、Report-SM-Delivery-Status です。
  - id: hold
    title: HSS が SC を MWD に入れる
    show: [hss-absent, a4-rda]
    active: hss-absent
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: HSS も UNRI を立てます。そのあいだ SRA に serving node を入れません。SC をまた失敗する IP-SM-GW へ向かわせないためです。
```

UE 2 の制御 API に `PUT /sms-availability` で `{"available": false}` を送り、UE 1 からショートメッセージを送ります。

```ladder
UE 2, IP-SM-GW, SC, HSS
SC -> IP-SM-GW: TFR // A1
IP-SM-GW -> UE 2: MESSAGE SMS-DELIVER // A1
UE 2 --> IP-SM-GW: 480 // A2
IP-SM-GW --> SC: TFA absent user // A3
SC <-> HSS: RDR, RDA // A4
```

| 手順 | 見どころ |
| --- | --- |
| A2 | 480（Temporarily Unavailable）。UE 2 には届いたが、その状態では要求を受けられない。delivery report は来ない。 |
| A3 | TFA に DIAMETER_ERROR_ABSENT_USER（5550）。IP-SM-GW は UE 2 について自分の UNRI を立てる。 |
| A4 | `SM-Delivery-Cause` は ABSENT_USER。HSS は SC のアドレスを MWD に入れて UNRI を立て、そのあいだは SRA に serving node を入れない。 |

> 手順書 [「UE 2 がショートメッセージを受け取らない」](../../../docs/guide/ja/sms.md#ue-2-がショートメッセージを受け取らない)。TS 24.341 5.3.3.4.2。TS 29.311 6.1.4.4.1。TS 29.338 7.3.3、5.2.3、5.2.1.3。TS 23.040 2.1.1、3.2.8。

---

## A5 から A7。UE 2 が戻る。Ready-For-SM と知らせ

```ims-scene
id: alert
lead: UE 2 の要求が IP-SM-GW に届けば、UE 2 が戻ったと分かります。その知らせは HSS を通って SC に届きます。
steps:
  - id: message
    title: UE 2 が要求を送る（A5）
    only: [a5-ue2-p, a5-p-s, a5-s-ip]
    active: a5-ue2-p
    text: "`{\"available\": true}` のあとです。中身は何でもよく、ここでは UE 2 自身のショートメッセージです。その OFR は省きます。"
  - id: accepted
    title: IP-SM-GW が 202 を返す
    show: [a5a-ip-s, a5a-s-p, a5a-p-ue2]
    active: a5a-ip-s
    detail: []
    text: どの MESSAGE にも同じように 202 で答えます。
  - id: present
    title: UE 2 が戻ったと分かる
    show: [ipsmgw-present]
    focus: [ipsmgw]
    checkpoint: true
    text: IP-SM-GW は A3 で立てた UNRI を持っています。それを落とし、HSS に知らせます。
  - id: ready
    title: IP-SM-GW が HSS に知らせる（A6、Ready-For-SM）
    only: [a6-rfsm]
    zoom: [ipsmgw, sc, hss]
    text: このプロジェクト独自のコマンドです。実際の網はこの知らせを MAP の READY_FOR_SM で送りますが、このプロジェクトは MAP を話しません。
  - id: hss
    title: HSS が UNRI を落とす
    show: [hss-present]
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: 番号が UE 2 の登録している IP-SM-GW のものかを確かめてから動きます。
  - id: alert
    title: HSS が答え、SC に知らせる（A7、ALR）
    hide: [hss-present]
    show: [a6-ans, a7-alr]
    active: a7-alr
    zoom: [ipsmgw, sc, hss]
    text: HSS は ALR を送り終えるのを待たずに答えます。答えと ALR の順は決まっていません。
```

```ladder
UE 2, IP-SM-GW, SC, HSS
UE 2 -> IP-SM-GW: MESSAGE // A5
IP-SM-GW <-> HSS: Ready-For-SM UE_PRESENT, answer // A6
SC <- HSS: ALR // A7
SC <-> HSS: SRR, SRA // A8
SC -> IP-SM-GW -> UE 2: TFR, MESSAGE SMS-DELIVER // A8
UE 2 -> IP-SM-GW: MESSAGE delivery report // A9
IP-SM-GW --> SC: TFA 2001 // A9
```

- **A5.** `{"available": true}` のあと、UE 2 の要求が IP-SM-GW に届けば、UE がそこにいることが分かります。再登録でも同じです（手順書の C1 から C4）。
- **A6.** Ready-For-SM は、このプロジェクト独自のコマンドで、コードは 16777214 です。実際の網はこの知らせを MAP の READY_FOR_SM で送りますが、このプロジェクトは MAP を話しません。Wireshark は `Unknown` と表示します。
- **A7.** HSS は UNRI を落とし、MWD のサービスセンタすべてに知らせます。**A8.** 知らせを受けた SC は、1 時間後の再送（`deploy/compose/sc.yaml` の `retry_delays`）を待たずに、すぐ配達します。

> 手順書 [「UE 2 がショートメッセージを受け取らない」](../../../docs/guide/ja/sms.md#ue-2-がショートメッセージを受け取らない) と [「UE 2 が登録し直すだけのとき」](../../../docs/guide/ja/sms.md#ue-2-が登録し直すだけのとき)。TS 23.204 6.5b。TS 29.002 12.4.1。TS 29.338 5.2.2。TS 23.040 3.2.6。

---

## A8 と A9。SC が送り直す

```ims-scene
id: resend
lead: 知らせを受けた SC はすぐ配達します。1 時間後の再送は待ちません。今度は UE 2 が受け取ります。
steps:
  - id: due
    title: SC が預かったものを配達に回す（A8）
    only: [a7-alr, sc-due]
    active: sc-due
    focus: [sc]
    zoom: [ipsmgw, sc, hss]
    text: その番号の待ち行列を、再送の間隔を待たずに配達に回します。
  - id: srr
    title: SC が HSS に尋ねる（SRR）
    hide: [a7-alr]
    show: [a8-srr]
    zoom: [ipsmgw, sc, hss]
    text: A4 のあいだと違い、UNRI はもう落ちています。
  - id: sra
    title: また IP-SM-GW が返る（SRA と ALA）
    show: [a8-sra, a7-ala]
    active: a8-sra
    zoom: [ipsmgw, sc, hss]
    text: SC は ALR に答える前に配達を始めます。HSS が SC を MWD から消すのは ALA のあとなので、SRA の MWD-Status はたいてい 0 です。
  - id: tfr
    title: 預かっていたものを TFR で送る
    show: [a8-tfr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: ここから先は手順 8 から 12 と同じです。
  - id: deliver
    title: UE 2 が受け取り、200 を返す
    only: [a8-tfr, a8-ip-i, a8-i-s, a8-s-p, a8-p-ue2, a8a-ue2-p, a8a-p-s, a8a-s-i, a8a-i-ip]
    active: a8a-ue2-p
    detail: []
    text: MESSAGE は I-CSCF から入り、UE 2 に届きます。LIR は省きます。
  - id: report
    title: delivery report で配達が終わる（A9）
    only: [a8-tfr, a9-ue2-p, a9-p-s, a9-s-ip, a9a-ip-s, a9a-s-p, a9a-p-ue2, a9-tfa]
    active: a9-tfa
    text: UE 2 の delivery report が IP-SM-GW に届き、TFA は 2001 になります。
```

| 見るもの | 見どころ |
| --- | --- |
| SRA の MWD-Status | たいてい 0。SC のアドレスが MWD にあり、どの印も立っていない。 |
| TFA | 2001。手順 12 と同じく、delivery report のあとに返る。 |

- 印が立っていないので、SC はこの配達を HSS に報告しません。報告するのは、印が立ったままのときです（D1 から D3）。
- UE 2 が何も送らず、登録し直すだけでも、IP-SM-GW は同じ知らせを HSS に送ります（手順書の C1 から C4）。送り直しはこのスライドと同じです。

> 手順書 [「UE 2 がショートメッセージを受け取らない」](../../../docs/guide/ja/sms.md#ue-2-がショートメッセージを受け取らない) と [「UE 2 が登録し直すだけのとき」](../../../docs/guide/ja/sms.md#ue-2-が登録し直すだけのとき)。TS 29.338 5.3.3.8。TS 23.040 3.2.6。

---

## B1 から B4。UE 2 のメモリに空きが無い

```ims-scene
id: memory
lead: メモリが一杯の UE 2 は MESSAGE を受け取り、そのあと RP-ERROR で断ります。HSS が立てる印は UNRI ではなく MCEF です。
steps:
  - id: tfr
    title: 配達が始まる（B1）
    only: [b1-tfr]
    zoom: [ipsmgw, sc, hss]
    text: "`PUT /sms-memory` で `{\"full\": true}` にしてから、UE 1 がショートメッセージを送ります。"
  - id: message
    title: UE 2 が MESSAGE に 200 で答える
    show: [b1-ip-i, b1-i-s, b1-s-p, b1-p-ue2, b1a-ue2-p, b1a-p-s, b1a-s-i, b1a-i-ip]
    active: b1a-ue2-p
    text: ショートメッセージは UE 2 まで届いています。LIR は省きます。
  - id: error
    title: delivery report が RP-ERROR になる（B2）
    only: [b1-tfr, b2-ue2-p, b2-p-s, b2-s-ip, b2a-ip-s, b2a-s-p, b2a-p-ue2]
    active: b2-ue2-p
    checkpoint: true
    text: UE 2 は中身を残さず、網へ知らせを送るべき状態になります。IP-SM-GW は 202 で答えます。
  - id: tfa
    title: IP-SM-GW が配達の失敗を返す（B3）
    only: [b1-tfr, b3-tfa]
    active: b3-tfa
    zoom: [ipsmgw, sc, hss]
    text: TFA に DIAMETER_ERROR_SM_DELIVERY_FAILURE と、理由の MEMORY_CAPACITY_EXCEEDED が入ります。
  - id: rdr
    title: SC が HSS に伝える（B4、RDR）
    show: [b4-rdr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: 理由は UE_MEMORY_CAPACITY_EXCEEDED です。
  - id: hold
    title: HSS が MCEF を立てる
    show: [hss-mcef, b4-rda]
    active: hss-mcef
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: UE には届いており、入れる場所が無いだけなので、UNRI は立てません。
```

`PUT /sms-memory` で `{"full": true}` を送り、UE 1 からショートメッセージを送ります。

| 手順 | 見どころ |
| --- | --- |
| B2 | 200 のあとに delivery report。RP-Cause 22（memory capacity exceeded）の RP-ERROR で、TP-FCS は `0xD3`。 |
| B3 | TFA に DIAMETER_ERROR_SM_DELIVERY_FAILURE（5555）と MEMORY_CAPACITY_EXCEEDED。 |
| B4 | UE_MEMORY_CAPACITY_EXCEEDED の RDR。HSS は MCEF を立てる。UE には届いており、入れる場所が無いだけなので、UNRI は立てない。 |

> 手順書 [「UE 2 のメモリに空きが無い」](../../../docs/guide/ja/sms.md#ue-2-のメモリに空きが無い)。TS 24.011 Table 8.4 part 2。TS 23.040 9.2.3.22、3.2.6。TS 29.338 7.3.8。

---

## B5 から B8。メモリに空きができた知らせ

```ims-scene
id: memory-back
lead: メモリに空きができると、UE 2 が自分から知らせます。RP-ACK が UE 2 に返るのは、HSS が知らせを受け取ったあとです。
steps:
  - id: smma
    title: UE 2 が RP-SMMA を送る（B5）
    only: [b5-ue2-p, b5-p-s, b5-s-ip]
    active: b5-ue2-p
    text: "`{\"full\": false}` で、UE 2 は送るべき知らせを MESSAGE に入れて送ります。"
  - id: accepted
    title: IP-SM-GW がすぐ 202 で答える
    show: [b5a-ip-s, b5a-s-p, b5a-p-ue2]
    active: b5a-ip-s
    checkpoint: true
    text: 知らせを HSS に渡す前です。RP-ACK は別の MESSAGE で送ります。
  - id: ready
    title: IP-SM-GW が HSS に渡す（B6、Ready-For-SM）
    only: [b6-rfsm]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: A6 と同じ独自のコマンドです。Alert-Reason が UE_MEMORY_AVAILABLE になります。
  - id: hss
    title: HSS が MCEF と UNRI を落とす
    show: [hss-mem]
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: そのあとで、MWD のサービスセンタに知らせます。
  - id: alert
    title: HSS が答え、SC に知らせる（B7）
    hide: [hss-mem]
    show: [b6-ans, b7-alr, b7-ala]
    active: b7-alr
    zoom: [ipsmgw, sc, hss]
    text: A7 と同じく、答えと ALR の順は決まっていません。
  - id: ack
    title: UE 2 に RP-ACK が返る（B8）
    only: [b6-ans, b8-ip-i, b8-i-s, b8-s-p, b8-p-ue2]
    active: b8-ip-i
    text: |
      HSS が受け取ったので、IP-SM-GW は RP-ACK を送ります。届いていない知らせを、届いたと UE に伝えることはありません。
      送り直しは A8 と A9 と同じです。
```

| 手順 | 見どころ |
| --- | --- |
| B5 | `{"full": false}` で、UE 2 は送るべき知らせを送る。MESSAGE に入れた RP-SMMA。 |
| B6、B7 | UE_MEMORY_AVAILABLE の Ready-For-SM。HSS は MCEF と UNRI を落とし、MWD のサービスセンタに知らせる。 |
| B8 | 知らせへの RP-ACK を UE 2 に返すのは、HSS が受け取ったあと。 |

フィルタは `diameter.Alert-Reason == 1` です。送り直しは A8 と A9 と同じです。

- 知らせが HSS に届かなかったときは、RP-ACK の代わりに RP-ERROR を返します。原因は、HSS の誤りごとに決まった値です。UE は知らせを、送るべきものとして持ち続けます。

> 手順書 [「UE 2 のメモリに空きが無い」](../../../docs/guide/ja/sms.md#ue-2-のメモリに空きが無い)。TS 24.011 7.3.2。TS 23.204 6.6。TS 23.040 3.2.6、11.2。

---

## D1 から D3。再送と、配達の報告

```ims-scene
id: retry
lead: RP-SMMA が失われると、MCEF が立ったままになります。送り直すのは SC の再送で、配達のあとに SC が HSS へ報告します。
steps:
  - id: srr
    title: 再送のときの SRR（D1）
    only: [d1-srr]
    zoom: [ipsmgw, sc, hss]
    text: B1 から B4 のあと、UE 2 のメモリは空きましたが、RP-SMMA が失われました。`retry_delays` の間隔のあとに再送が来ます。
  - id: sra
    title: SRA に MCEF-Set が立っている
    show: [d1-sra]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: HSS がこの利用者の MCEF を立てている、という意味です。
  - id: tfr
    title: SC が TFR を送る（D2）
    show: [d2-tfr]
    zoom: [ipsmgw, sc, hss]
    text: IP-SM-GW から先の SIP は A8 と A9 と同じなので、この図では省きます。
  - id: tfa
    title: 今度は UE 2 が受け取る
    show: [d2-tfa]
    zoom: [ipsmgw, sc, hss]
    text: delivery report のあとで、TFA は 2001 になります。
  - id: decide
    title: SC が配達を報告すると決める
    show: [sc-report]
    focus: [sc]
    zoom: [ipsmgw, sc, hss]
    text: HSS が印を立てているあいだに配達が成功したので、SC はそれを報告します。
  - id: rdr
    title: SUCCESSFUL_TRANSFER の RDR（D3）
    show: [d3-rdr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SC はこの報告を 1 回だけ送ります。
  - id: clear
    title: HSS が MCEF を落とす
    show: [hss-success, d3-rda]
    active: hss-success
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: SC のアドレスを MWD から消します。D3 が無いと、MCEF は立ったままです。
```

RP-SMMA は途中で失われることがあります。すると MCEF が立ったままになり、UE 2 が登録しても SC には知らせが行きません。ショートメッセージがもう一度送られるのは、SC の再送のときだけです。

| 手順 | 見どころ |
| --- | --- |
| D1 | 再送のときの SRR。SRA には IP-SM-GW が返り、MWD-Status のビット 2 の MCEF-Set が立っている。 |
| D2 | A8 と A9 と同じ配達。TFA は 2001。 |
| D3 | SUCCESSFUL_TRANSFER の RDR。HSS が印を立てていたので、SC が配達を報告する。HSS は MCEF を落とし、SC のアドレスを MWD から消す。 |

- この網では、再送は 1 時間後です。`internal/ipsmgw/chain_test.go` の `TestARetryAfterAFullMemoryClearsTheFlag` が、再送を 1 秒後にしてこの手順を動かします。
- 知らせのあとは印がもう落ちているので、A8 の SRA に MCEF-Set は無く、この RDR も続きません。

> 手順書 [「メモリに空きができた知らせが失われたとき」](../../../docs/guide/ja/sms.md#メモリに空きができた知らせが失われたとき)。TS 29.338 5.3.3.8、5.2.3.1、5.3.3.19。TS 23.040 10.1、3.2.6。

---

## 失敗するとき

```ims-scene
id: failures
lead: SIP の応答はどちらも 202 です。失敗は RP と TP の層で伝わり、SC と HSS のやりとりで分かれます。
steps:
  - id: ofr
    title: 番号を持つ加入者がいない
    only: [f-ofr]
    zoom: [ipsmgw, sc, hss]
    text: UE 1 が `+12025550198` へ送ります。IP-SM-GW は手順 5 と同じく OFR で SC に渡します。
  - id: srr
    title: SC が番号を HSS に尋ねる
    show: [f-srr]
    zoom: [ipsmgw, sc, hss]
    text: 手順 5 と同じ SRR です。
  - id: sra
    title: HSS が USER_UNKNOWN で答える
    show: [f-sra]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: この番号を持つ加入者はいません。
  - id: ofa
    title: SC が受け付けない
    show: [f-ofa]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: OFA がショートメッセージを断ります。配達は試みません。
  - id: report
    title: submit report が RP-ERROR になる
    only: [f-ofa, frep-ip-i, frep-i-s, frep-s-p, frep-p-ue1]
    active: frep-ip-i
    checkpoint: true
    text: 経路は手順 6 と同じです。送信者は status report を待たずに、その場で失敗を知ります。LIR は省きます。
  - id: absent
    title: 受信者が登録していない
    only: [g-srr, g-sra]
    active: g-sra
    zoom: [ipsmgw, sc, hss]
    text: "`+12025550103` の加入者 3 はいるので、SC は受け付け、submit report は RP-ACK になります。配達の SRR には 5550 が返ります。"
  - id: hold
    title: HSS が SC を MWD に残す
    show: [hss-keep]
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: SC は預かります。加入者 3 が登録すると、HSS の知らせで配達されます。
```

| 場合 | 試し方 | UE 1 が受け取るもの |
| --- | --- | --- |
| 番号を持つ加入者がいない | `ue sms +12025550198 "Nobody has this number."` | RP-ERROR の submit report。RP-Cause 1、TP-FCS 0xC3。 |
| 受信者が登録していない | `ue sms -status-report +12025550103 "Are you there?"` | RP-ACK の submit report。そのあとはまだ何も来ない。SC がショートメッセージを預かる。 |

- コマンドは `docker compose run --rm` に続けて書きます。
- どちらも SIP の応答は 202 です。失敗は RP と TP の層で伝わります。
- 1 つ目は、SC が受け付けません。手順 5 の SRA が DIAMETER_ERROR_USER_UNKNOWN（5001）になり、配達は試みません。
- 2 つ目は、SC は受け付けます。加入者 3 には IP-SM-GW が登録されていないので、SC の SRR には毎回 DIAMETER_ERROR_ABSENT_USER（5550）が返り、HSS は SC のアドレスを MWD に残します。加入者 3 が登録すると、HSS の知らせで配達されます。

フィルタは `gsm_a.rp.cause || gsm_sms.tp-fcs || diameter.cmd.code == 8388647` です。

> 手順書 [「失敗するとき」](../../../docs/guide/ja/sms.md#失敗するとき)。TS 24.011 Table 8.4。TS 23.040 9.2.3.22。TS 29.338 5.2.1.3。

---

## ボディを読む。RP 層

手順 1 の MESSAGE のボディです。

```text
00 00 00 07 91 21 20 55 05 01 f0 23 21 00 0b 91 21 20 55 05 01 f2 00 00 19 c8 32 9b fd 06 55 8b 2d 21 0b
44 47 a7 e7 a0 f4 1c 54 2d b6 82 2e
```

| オクテット | フィールド | 見どころ |
| --- | --- | --- |
| `00` | RP-MTI | 端末から網への RP-DATA。 |
| `07 91 21 20 55 05 01 f0` | RP-DA | 長さは 7 オクテット。数字は 2 桁ずつ入れ替わる。SC の番号。 |
| `23` | RP-User-Data の長さ | このあとの 35 オクテットが TPDU。 |

- まず手で読み、そのあとで Wireshark の木と比べます。`21 20 55 05 01 f0` を 12025550100 に戻してみます。
- ラボでは、MESSAGE の詳しい表示の **Body (hex)** に同じオクテットが出ます。

> 手順書 [「中継層（RP）」](../../../docs/guide/ja/sms.md#中継層rp)。全部のフィールドの表は手順書にあります。TS 24.011 7.3.1.2、8.2。

---

## ボディを読む。TP 層と文字

| オクテット | フィールド | 見どころ |
| --- | --- | --- |
| `21` | 1 オクテット目 | TP-MTI は 01 で SMS-SUBMIT。TP-SRR は 1 で、status report を頼む。 |
| `0b 91 21 20 55 05 01 f2` | TP-DA | 長さは 11 桁。受信者の番号。 |
| `00` | TP-DCS | GSM 7 ビットの既定のアルファベット。 |
| `19` | TP-UDL | 25 セプテット。25 文字。このあとが、7 ビットずつ詰めた本文。 |

- RP は長さをオクテットで、TP は桁で数えます。同じ番号でも 7 と 11 になります。
- TP-MTI の 01 は、端末からなら SMS-SUBMIT、網からなら SMS-SUBMIT-REPORT です。
- 最初の 2 文字の H と e を、手順書の図に沿って `c8` と `32` に詰めてみます。日本語は UCS2 で、TP-DCS は `08` です。UE 2 から日本語を送ると、TP-DCS と TP-UDL が変わります。

> 手順書 [「転送層（TP）の SMS-SUBMIT」](../../../docs/guide/ja/sms.md#転送層tpの-sms-submit) と [「GSM 7 ビットの詰め方と UCS2」](../../../docs/guide/ja/sms.md#gsm-7-ビットの詰め方と-ucs2)。TS 23.040 9.2.2.2、9.2.3。TS 23.038 6.1.2.1.1。

---

## Wireshark の表示フィルタ

フィルタをかける前に、6 通のうちどれが残るかを予想します。

| フィルタ | 見つかるもの |
| --- | --- |
| `sip.Content-Type == "application/vnd.3gpp.sms"` | SMS か報告を運ぶ MESSAGE のすべて。 |
| `gsm_a.rp.msg_type == 0x00` | UE からの RP-DATA。UE が送ったショートメッセージ。 |
| `gsm_sms.tp-mti == 2` | SMS-STATUS-REPORT。 |
| `gsm_sms.sms_text contains "Hello"` | 本文。GSM 7 ビットも UCS2 もデコードして調べる。 |
| `gsm_sms.tp-da == "12025550102"` | その番号に宛てたショートメッセージ。 |
| `sip.In-Reply-To` | submit report と delivery report。 |
| `diameter.cmd.code == 306 \|\| diameter.cmd.code == 307` | Sh のメッセージ。UDR、UDA、PUR、PUA。 |
| `diameter.applicationId == 16777313` | IP-SM-GW と SC のあいだの SGd。`16777312` は S6c。 |
| `diameter.cmd.code == 16777214` | このプロジェクト独自のコマンド Ready-For-SM と、その答え。 |

> 手順書 [「Wireshark の表示フィルタ」](../../../docs/guide/ja/sms.md#wireshark-の表示フィルタ) と、[キャプチャの「SMS over IP の表示フィルタ」](../../../docs/guide/ja/capture.md#sms-over-ip-の表示フィルタ)

---

## ラボで送る

```sh
docker compose --profile phones up -d --build
```

1. `phone1` のカードの SMS のフォームに本文を書き、**status report** に印を付けて、**Send SMS** を押します。
2. **SMS sent** の表示が `waiting for the submit report` から `accepted by the SC` に変わります。
3. `phone2` のカードの **SMS received** に、送信者の番号と本文が出ます。
4. 各行の **ladder** で、その MESSAGE のシーケンス図が出ます。矢印を選ぶと、RP と TP のフィールドが出ます。

- ラボは Call-ID ごとにシーケンス図を描くので、6 通の MESSAGE は 6 つのシーケンス図になります。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| ボディ。RP、TP、文字 | `internal/sms` の `rp.go`、`tp.go`、`text.go` |
| iFC と、その評価 | `internal/ifc`、`internal/scscf/services.go` |
| 第三者 REGISTER | `internal/scscf/thirdparty.go` |
| Accept-Contact の照合 | `internal/scscf/callerprefs.go` |
| IP-SM-GW | `internal/ipsmgw` の `message.go`、`deliver.go`、`regevent.go`、`store.go` |
| サービスセンタ | `internal/smsc` の `submit.go`、`deliver.go`、`alert.go`、`store.go` |
| SGd と S6c | `internal/sgd`、`internal/hss/s6c.go` |
| SC との境界と、SC のスタブ | `internal/ipsmgw/servicecentre.go`、`internal/sc` |
| Sh | `internal/sh`、`internal/hss/sh.go` |
| UE | `internal/ue/sms.go` |

- 手で読んだフィールドの名前を `internal/sms/tp.go` で探します。`internal/ipsmgw/network_test.go` が、流れの全体を 1 つのプロセスで動かします。

> 手順書 [「コードの場所」](../../../docs/guide/ja/sms.md#コードの場所) と [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/sms.md#このプロジェクトで簡略化していること)

---

[layout: exercise]

## 振り返り

1. UE 1 の MESSAGE の Request-URI は、誰のアドレスですか。
2. S-CSCF は、その MESSAGE を IP-SM-GW へ送ることを、どうやって知りますか。
3. 202 Accepted と submit report は、それぞれ何を伝えますか。
4. IP-SM-GW が UE 2 へ送る MESSAGE は、網のどこから入りますか。
5. delivery report が IP-SM-GW に届くのは、誰の iFC によりますか。
6. RP-DA の長さが 7、TP-DA の長さが 11 なのは、なぜですか。
7. UE 2 がショートメッセージを受け取れないとき、誰がそれを持っていますか。そのノードに、もう一度送るきっかけを与えるのは何ですか。

---

[layout: answers]

## 振り返りの答え

1. SC のアドレスです。UE はショートメッセージを受信者に直接送りません。
2. 登録のときの SAA で届いた iFC のうち、優先度 1 の条件に MESSAGE が一致するからです。
3. 202 は、IP-SM-GW が MESSAGE を受け取ったことを伝えます。submit report は、SC がショートメッセージを受け付けたかを伝えます。
4. I-CSCF です。I-CSCF が LIR で UE 2 の S-CSCF を見つけます。
5. UE 2 の iFC です。delivery report は UE 2 が発信する MESSAGE だからです。
6. RP は長さをオクテットで、TP は桁で数えるからです。
7. SC が持っており、HSS が SC のアドレスを MWD に残します。UE 2 が戻ったと IP-SM-GW が伝えると、HSS が ALR で SC に知らせます。知らせが無ければ、SC の再送がもう一度送ります。

---

## まとめ

IMS の中心となる登録、通話、ショートメッセージの 6 回は、これで終わりです。

| 回 | 題 | 読む文書 |
| --- | --- | --- |
| 1 | パッケージ構成 | [README.ja.md](../../../README.ja.md) |
| 2 | 網の地図 | [docs/guide/ja/README.md](../../../docs/guide/ja/README.md) |
| 3 | キャプチャとラボ | [docs/guide/ja/capture.md](../../../docs/guide/ja/capture.md) |
| 4 | 登録 | [docs/guide/ja/registration.md](../../../docs/guide/ja/registration.md) |
| 5 | 通話 | [docs/guide/ja/call.md](../../../docs/guide/ja/call.md) |
| 6 | SMS | [docs/guide/ja/sms.md](../../../docs/guide/ja/sms.md) |

- 次は MMTel AS の付加サービス（[mmtel.md](../../../docs/guide/ja/mmtel.md)）です。その次が、付加サービスの上に立つ VoLTE の通話（[volte.md](../../../docs/guide/ja/volte.md)）です。

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```
