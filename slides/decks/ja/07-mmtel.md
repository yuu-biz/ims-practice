---
title: MMTel AS の付加サービス
footer: ims-practice 勉強会 7. 付加サービス
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: ue1, label: UE 1, role: 発信者, at: [0, 1]}
  - {id: ue2, label: UE 2, role: サービスを設定する, at: [0, 2]}
  - {id: ue3, label: UE 3, role: 転送先, at: [0, 3]}
  - {id: pcscf, label: P-CSCF, role: 識別子を示し、隠す, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: ホーム網の入口, at: [2, 0]}
  - {id: scscf, label: S-CSCF, role: iFC で AS に送る, at: [3, 1]}
  - {id: hss, label: HSS, role: iFC と利用者の設定, at: [4, 1]}
  - {id: as1, label: MMTel AS, role: UE 1 のサービス, at: [4, 0]}
  - {id: as2, label: MMTel AS, role: UE 2 のサービス, at: [4, 2]}
  - {id: mrfc, label: MRFC, role: 音声ガイダンス, at: [5, 2]}
events:
  # 参照点
  - {id: ref-gm, rel: Gm, from: ue2, to: pcscf, proto: SIP}
  - {id: ref-mw, rel: Mw, from: pcscf, to: scscf, proto: SIP}
  - {id: ref-isc, rel: ISC, from: scscf, to: as2, proto: SIP}
  - {id: ref-cx, rel: Cx, from: scscf, to: hss, proto: Diameter}
  - {id: ref-sh, rel: Sh, from: as2, to: hss, proto: Diameter}
  - {id: ref-ut, rel: Ut, from: ue2, to: as2, proto: HTTP}
  - {id: ref-mr, rel: Mr', from: as2, to: mrfc, proto: SIP}
  # AS を通る通常の通話
  - id: c1-ue-p
    msg: INVITE
    from: ue1
    to: pcscf
    proto: SIP
    note: (C1)
    fields:
      - Request-URI: "`tel:+12025550102`"
  - id: c1-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    note: (C1)
    fields:
      - P-Charging-Vector: "P-CSCF が `icid-value` を入れる"
  - id: scscf-ifc-orig
    do: iFC を照らし合わせる
    at: scscf
    fields:
      - 優先度 0 と 1: SMS の条件。REGISTER と MESSAGE なので一致しない
      - 優先度 10: INVITE かつ ORIGINATING_REGISTERED。一致する
      - ServerName: "`sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060`"
      - DefaultHandling: SESSION_TERMINATED
  - id: scscf-default
    do: AS が答えないときの扱い
    at: scscf
    fields:
      - 発信側（優先度 10 と 13）: SESSION_TERMINATED。通話を終える
      - 着信側（優先度 11 と 12）: SESSION_CONTINUED。AS 無しで続ける
      - 理由: 規制を確実に効かせる
  - id: c1-s-as1
    msg: INVITE
    from: scscf
    to: as1
    proto: SIP
    note: (C1)
    fields:
      - P-Served-User: "`<sip:+12025550101@…>;sescase=orig;regstate=reg`"
      - Route: "`<sip:mmtel.…:5060;lr>,<sip:scscf.…:5060;lr;odi=...>`"
  - id: as1-served
    do: served user と場合を決める
    at: as1
    fields:
      - served user: UE 1
      - 場合: orig
      - 動かすサービス: OCB、OIR、応答に TIP
  - id: udr1
    msg: UDR
    from: as1
    to: hss
    proto: Sh
    note: (UE 1)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: "`MMTEL-Services`"
  - id: uda1
    msg: UDA
    from: hss
    to: as1
    proto: Sh
    note: (UE 1)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Sh-User-Data: UE 1 の MMTelServices の文書
  - id: c2-as1-s
    msg: INVITE
    from: as1
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - Call-ID: AS が作った新しい値
      - icid-value: C1 と同じ
      - Route: "`<sip:scscf.…:5060;lr;odi=...>`"
  - id: scscf-odi
    do: ODI から続ける
    at: scscf
    fields:
      - 見るもの: "Route の `odi`"
      - 続ける所: 一致した条件（優先度 10）の次
      - 結果: 発信側の条件はもう無い。ENUM を引き、I-CSCF へ
  - id: c2-s-i
    msg: INVITE
    from: scscf
    to: icscf
    proto: SIP
    note: (C2)
    fields:
      - Request-URI: "ENUM のあとの `sip:+12025550102@…`"
  - id: lir
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 2)
  - id: lia
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 2)
    fields:
      - Server-Name: UE 2 の S-CSCF
  - id: c2-i-s
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    note: (C2)
  - id: c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`<sip:+12025550102@…>;sescase=term;regstate=reg`"
      - 一致した条件: 優先度 11。INVITE かつ TERMINATING_REGISTERED
  - id: as2-served
    do: served user と場合を決める
    at: as2
    fields:
      - served user: UE 2
      - 場合: term
      - 動かすサービス: ICB と ACR、CFU と CFNL、OIP と TIR、利用者への通話
      - 通話の終わり方で: CFB か CFNR
  - id: udr2
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (UE 2)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: "`MMTEL-Services`"
  - id: uda2
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (UE 2)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Sh-User-Data: UE 2 の MMTelServices の文書
  - id: c3-as2-s
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C3)
    fields:
      - Call-ID: もう 1 つ新しい値
      - icid-value: C1、C2 と同じ
  - id: c3-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    note: (C3)
  - id: c3-p-ue2
    msg: INVITE
    from: pcscf
    to: ue2
    proto: SIP
    note: (C3)
    fields:
      - P-Charging-Vector: 無い。P-CSCF が UE に送るものから消す
  - id: ok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
  - id: pcscf-tip
    do: 応答した相手を示す
    at: pcscf
    fields:
      - P-Asserted-Identity: INVITE の P-Called-Party-ID を入れる
      - 値: "`<sip:+12025550102@…>`"
  - id: ok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - P-Asserted-Identity: "`<sip:+12025550102@…>`。これが TIP"
  # 設定の文書と Ut
  - id: hss-doc
    do: 利用者ごとの文書を持つ
    at: hss
    fields:
      - 文書: XCAP の simservs
      - 置き方: "Sh の repository data。Service-Indication は `MMTEL-Services`"
      - 最初の規則: "CDIV と CB は `rule-deactivated`。転送先は無い"
  - id: as2-nocopy
    do: 写しを持たない
    at: as2
    fields:
      - 読むとき: 通話ごとに UDR で
      - 理由: AS のどのインスタンスでも、どの通話でも受けられる
  - id: ut-get1
    msg: GET
    from: ue2
    to: as2
    proto: HTTP
    note: (1 回目)
    fields:
      - URI: 文書の全体。利用者の部分は XUI
      - Authorization: 無い
  - id: as2-challenge
    do: チャレンジを作る
    at: as2
    fields:
      - HSS に尋ねること: 無い。UDR を送らない
      - realm: ホームドメイン。どの利用者でも同じ
      - algorithm: SHA-256、MD5 の順
      - nonce: どのチャレンジも同じ
  - id: ut-401
    msg: "401"
    from: as2
    to: ue2
    proto: HTTP
    fields:
      - WWW-Authenticate: "`Digest` が 2 つ。SHA-256 と MD5"
  - id: ut-get2
    msg: GET
    from: ue2
    to: as2
    proto: HTTP
    note: (2 回目)
    fields:
      - Authorization: "`Digest`。1 つ目のチャレンジへの答え"
  - id: ut-udr-cred
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (パスワード)
    fields:
      - Service-Indication: "`Ut-Digest-Credential`"
  - id: ut-uda-cred
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (パスワード)
    fields:
      - 中身: HA1 の値。パスワードそのものではない
  - id: ut-udr-doc
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (文書)
    fields:
      - Service-Indication: "`MMTEL-Services`"
  - id: ut-uda-doc
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (文書)
    fields:
      - 中身: 文書と、その Sequence Number 0
  - id: ut-200-get
    msg: 200 OK
    from: as2
    to: ue2
    proto: HTTP
    fields:
      - ETag: "`\"0\"`"
      - 本文: 文書の全体
  - id: ut-put
    msg: PUT
    from: ue2
    to: as2
    proto: HTTP
    fields:
      - URI: "`~~` のあとがノードセレクタ。規則 `cfu` を指す"
      - If-Match: "`\"0\"`"
      - Content-Type: "`application/xcap-el+xml`"
      - 本文: "`rule-deactivated` が消え、転送先 `tel:+12025550103` が入った規則"
  - id: ut-udr-cred2
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (もう一度)
  - id: ut-uda-cred2
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (もう一度)
  - id: ut-udr-doc2
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (もう一度)
  - id: ut-uda-doc2
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (もう一度)
  - id: as2-replace
    do: 規則を置き換えて確かめる
    at: as2
    fields:
      - If-Match: "読んだ版の `\"0\"` と合う"
      - 合わないとき: PUR を送らずに 412
  - id: ut-pur
    msg: PUR
    from: as2
    to: hss
    proto: Sh
    fields:
      - Service-Indication: "`MMTEL-Services`"
      - Sh-User-Data: "`<SequenceNumber>1</SequenceNumber>` と新しい文書"
  - id: ut-pua
    msg: PUA
    from: hss
    to: as2
    proto: Sh
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - 断るとき: DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC
  - id: ut-200-put
    msg: 200 OK
    from: as2
    to: ue2
    proto: HTTP
    fields:
      - ETag: "`\"1\"`"
  # OIR
  - id: oir-c1-s-as1
    msg: INVITE
    from: scscf
    to: as1
    proto: SIP
    note: (C1)
    fields:
      - From: UE 1
      - P-Served-User: "`sescase=orig`"
  - id: as1-oir
    do: 識別子を隠す（OIR）
    at: as1
    fields:
      - 設定: "`ue xcap oir restricted` で既定が非通知"
      - Privacy: "`id` を足す"
      - From: "`\"Anonymous\" <sip:anonymous@anonymous.invalid>` にする"
      - P-Asserted-Identity: 残す
  - id: oir-c2-as1-s
    msg: INVITE
    from: as1
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - From: "`\"Anonymous\" <sip:anonymous@anonymous.invalid>`"
      - Privacy: "`id`"
      - P-Asserted-Identity: "`<sip:+12025550101@…>` 網の中では残る"
  - id: oir-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`sescase=term`"
  - id: as2-oip
    do: 発信者の識別子を見せる（OIP）
    at: as2
    fields:
      - OIP: UE 2 は持っている
      - "Privacy の `id`": 残す。UE 2 は発信者が隠したことを知る
  - id: oir-c3-as2-s
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C3)
  - id: scscf-strip
    do: P-Asserted-Identity を取り除く
    at: scscf
    fields:
      - 理由: "UE は信頼ドメインの外にあり、Privacy に `id` がある"
      - 取り除くもの: P-Asserted-Identity
  - id: oir-c3-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    note: (C3)
  - id: oir-c3-p-ue2
    msg: INVITE
    from: pcscf
    to: ue2
    proto: SIP
    note: (C3)
    fields:
      - From: "`\"Anonymous\" <sip:anonymous@anonymous.invalid>`"
      - Privacy: "`id`"
      - P-Asserted-Identity: 無い
  # CFU
  - id: cfu-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`sescase=term;regstate=reg`"
  - id: as2-cfu
    do: CFU で転送する
    at: as2
    fields:
      - 規則: "`cfu`。`rule-deactivated` が無い"
      - 転送先: "`tel:+12025550103`"
      - いつ: INVITE が届いた時点。UE 2 を呼び出さない
  - id: cfu-181
    msg: "181"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - P-Asserted-Identity: UE 2
      - History-Info: "転送の経歴。UE 3 の項目に `Privacy=history`"
  - id: cfu-181-ue1
    msg: "181"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - 意味: Call Is Being Forwarded
  - id: cfu-c4
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C4)
    fields:
      - Request-URI: "`sip:+12025550103@…;user=phone;cause=302`"
      - History-Info: "index 1 が UE 2、index 1.1 が UE 3 で `mp=1`"
  - id: scscf-cdiv
    do: 転送に気づく
    at: scscf
    fields:
      - 気づくこと: ODI の付いた要求の Request-URI が変わった
      - 照らす条件: 転送元の UE 2 の ORIGINATING_CDIV（優先度 13）
  - id: cfu-c4-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C4)
    fields:
      - P-Served-User: "`<sip:+12025550102@…>;orig-cdiv;regstate=reg`"
  - id: as2-cdiv
    do: 転送元の発信の規制
    at: as2
    fields:
      - served user: 転送元の UE 2
      - 場合: orig-cdiv
      - 動かすサービス: UE 2 の OCB だけ
  - id: cfu-c5
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C5)
  - id: cfu-c6-p-ue3
    msg: INVITE
    from: pcscf
    to: ue3
    proto: SIP
    note: (C6)
    fields:
      - History-Info: UE 2 と UE 3 の 2 つの項目
  # CFB と CFNR
  - id: cfb-486-ue2-p
    msg: "486"
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
    fields:
      - 意味: Busy Here
  - id: cfb-486-p-s
    msg: "486"
    from: pcscf
    to: scscf
    proto: SIP
    note: (C3)
  - id: cfb-486-s-as2
    msg: "486"
    from: scscf
    to: as2
    proto: SIP
    note: (C3)
  - id: as2-cfb
    do: 話し中で転送する（CFB）
    at: as2
    fields:
      - きっかけ: C3 の 486
      - すること: ACK を返し、UE 1 に 181 を送る
      - 転送: "`cause=486`"
      - History-Info の UE 2 の項目: "`Reason=SIP%3Bcause%3D486...`"
  - id: cfnr-180-ue2-p
    msg: "180"
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
  - id: cfnr-180-p-s
    msg: "180"
    from: pcscf
    to: scscf
    proto: SIP
    note: (C3)
  - id: cfnr-180-s-as2
    msg: "180"
    from: scscf
    to: as2
    proto: SIP
    note: (C3)
    fields:
      - P-Asserted-Identity: UE 2
  - id: as2-timer
    do: 無応答のタイマーを始める
    at: as2
    fields:
      - 始まり: 最初の 180
      - 長さ: "UE 2 の `NoReplyTimer`。e2e では 5 秒"
      - 無いとき: 事業者の既定の 20 秒
  - id: cfnr-cancel-as2-s
    msg: CANCEL
    from: as2
    to: scscf
    proto: SIP
    note: (C3)
    fields:
      - Reason: "`SIP;cause=408;text=\"Request Timeout\"`"
      - いつ: この回では 180 の 5.0 秒あと
  - id: cfnr-cancel-s-p
    msg: CANCEL
    from: scscf
    to: pcscf
    proto: SIP
    note: (C3)
  - id: cfnr-cancel-p-ue2
    msg: CANCEL
    from: pcscf
    to: ue2
    proto: SIP
    note: (C3)
    fields:
      - Reason: 同じ Reason。途中の CSCF も写す
  - id: cfnr-487-ue2-p
    msg: "487"
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
  - id: cfnr-487-p-s
    msg: "487"
    from: pcscf
    to: scscf
    proto: SIP
    note: (C3)
  - id: cfnr-487-s-as2
    msg: "487"
    from: scscf
    to: as2
    proto: SIP
    note: (C3)
  - id: cfnr-199
    msg: "199"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - 意味: Early Dialog Terminated
      - Reason: "`SIP;cause=408`"
  - id: cfnr-199-s-as1
    msg: "199"
    from: scscf
    to: as1
    proto: SIP
    note: (C2)
  - id: as1-keep
    do: 199 を UE 1 に送らない
    at: as1
    fields:
      - UE 1 との初期ダイアログ: 1 つのまま保つ
      - 結果: UE 1 は 181 と UE 3 の応答を同じ初期ダイアログで受ける
  - id: cfnr-181
    msg: "181"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - History-Info: "UE 2 と、`cause=408` の UE 3"
  - id: cfnr-181-ue1
    msg: "181"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
  - id: cfnr-c4
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C4)
    fields:
      - Request-URI: "`sip:+12025550103@…;user=phone;cause=408`"
  # CFNL
  - id: cfnl-c2-s-i
    msg: INVITE
    from: scscf
    to: icscf
    proto: SIP
    note: (C2)
  - id: cfnl-lir
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (未登録)
  - id: cfnl-lia
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (未登録)
    fields:
      - Experimental-Result: DIAMETER_UNREGISTERED_SERVICE (2003)
      - Server-Capabilities: S-CSCF の能力。名前ではない
  - id: icscf-choose
    do: S-CSCF を選ぶ
    at: icscf
    fields:
      - 求められた能力: LIA の Server-Capabilities
      - 選んだもの: その能力を持つ S-CSCF
  - id: cfnl-c2-i-s
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    note: (C2)
  - id: cfnl-sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Server-Assignment-Type: UNREGISTERED_USER (3)
  - id: cfnl-saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - User-Data: UE 2 のプロファイルと iFC
  - id: scscf-ifc-unreg
    do: 未登録の着信の条件
    at: scscf
    fields:
      - 優先度 12: INVITE かつ TERMINATING_UNREGISTERED。一致する
      - DefaultHandling: SESSION_CONTINUED
  - id: cfnl-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`<sip:+12025550102@…>;sescase=term;regstate=unreg`"
  - id: as2-cfnl
    do: CFNL ですぐ転送する
    at: as2
    fields:
      - 規則: "`cfnl`。条件は `<not-registered/>`"
      - 転送: "すぐに `cause=404`"
      - CFNL が無いとき: INVITE をそのまま返し、S-CSCF が 480
  - id: cfnl-c4
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C4)
    fields:
      - Request-URI: "`sip:+12025550103@…;user=phone;cause=404`"
  # 規制
  - id: bar-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`sescase=term`"
  - id: as2-icb
    do: 着信を規制する（BAIC）
    at: as2
    fields:
      - 規則: "`baic`。UE 2 が `baic on` にした"
      - 順番: ほかのどのサービスよりも先
      - 結果: 603。その前に音声ガイダンス
  - id: bar-m-invite
    msg: INVITE
    from: as2
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - Request-URI: "`sip:annc@mrfc...;play=/provisioned/notice`"
      - SDP: 無い
  - id: bar-m-200
    msg: 200 OK
    from: mrfc
    to: as2
    proto: SIP
    note: (M)
    fields:
      - SDP: offer。MRFP の 172.28.0.86、PCMU、sendonly
  - id: bar-183
    msg: "183"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - SDP: MRFP を指す answer
      - P-Early-Media: "`sendonly`"
      - 送り方: 高信頼の暫定応答
  - id: bar-183-ue1
    msg: "183"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
  - id: bar-m-ack
    msg: ACK
    from: as2
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - SDP: answer。発信者のメディア、recvonly
      - いつ: 発信者が PRACK で 183 を受け取ったあと
  - id: bar-m-bye
    msg: BYE
    from: mrfc
    to: as2
    proto: SIP
    note: (M)
    fields:
      - 意味: 音声ガイダンスが終わった
  - id: bar-603
    msg: "603"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - Reason: "`SIP;cause=603;text=\"Decline\"`"
  - id: bar-603-ue1
    msg: "603"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - Reason: "`SIP;cause=603;text=\"Decline\"`"
  # CW と HOLD
  - id: cw-180-ue2-p
    msg: "180"
    from: ue2
    to: pcscf
    proto: SIP
    note: (2 本目)
    fields:
      - Alert-Info: "`<urn:alert:service:call-waiting>`"
  - id: cw-180-p-s
    msg: "180"
    from: pcscf
    to: scscf
    proto: SIP
    note: (2 本目)
  - id: cw-180-s-as2
    msg: "180"
    from: scscf
    to: as2
    proto: SIP
    note: (2 本目)
  - id: as2-cw
    do: 割込通話の知らせ方を決める
    at: as2
    fields:
      - 事業者の設定: "`calling-user-receives-notification-his-call-is-waiting` が `false`"
      - 結果: Alert-Info を取り除いて渡す
  - id: cw-180-as2-s
    msg: "180"
    from: as2
    to: scscf
    proto: SIP
    note: (2 本目)
    fields:
      - Alert-Info: 無い
  - id: hold-ue2-p
    msg: INVITE
    from: ue2
    to: pcscf
    proto: SIP
    note: (保留)
    fields:
      - SDP: "`a=sendonly`"
  - id: hold-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    note: (保留)
  - id: hold-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (保留)
  - id: as2-hold
    do: もう一方の側に渡す
    at: as2
    fields:
      - SDP を持つ re-INVITE と UPDATE: 届いたまま、もう一方の側に渡す
      - ボディの無い UPDATE: AS で終わる。セッションタイマーはダイアログごと
  - id: hold-as2-s
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (保留)
    fields:
      - SDP: "`a=sendonly`"
```

---

# MMTel AS の付加サービス

ims-practice 勉強会 第 7 回（MMTel AS、Ut/XCAP での設定、キャプチャで見るサービス）

---

[layout: summary]

## この回で分かること

最初の問い。着信を転送している電話にかけると、別の電話が鳴ります。それを決めているのは、どのノードでしょうか。

- 付加サービスとは何か。どのノードで動くか。
- S-CSCF が通話を AS に渡すしくみ。iFC、P-Served-User、ODI。
- AS が B2BUA として通話を 3 つのダイアログに分けること。
- 利用者の設定がどこにあり、UE が Ut の XCAP でそれをどう変えるか。
- サービスごとに、キャプチャで何を見るか。識別子、着信転送、規制。

> 手順書 [MMTel AS の付加サービス](../../../docs/guide/ja/mmtel.md)（`docs/guide/ja/mmtel.md`）
> 仕様の版は手順書の冒頭にあります。TS 24.229 V20.1.0、TS 24.604 V19.0.0、TS 24.607 V19.1.0、TS 24.611 V19.0.0、TS 24.623 V19.0.0、TS 29.364 V19.1.0、GSMA IR.92 v24.0 などです。

---

## 付加サービスはどこで動くか

```ims-scene
id: where
nodes: [ue2, pcscf, scscf, hss, as2, mrfc]
lead: CSCF は経路を決めるだけです。利用者の設定を知って通話を変えるのは、アプリケーションサーバーの MMTel AS です。
steps:
  - id: route
    title: CSCF は経路を決める
    show: [ref-gm, ref-mw]
    focus: [ue2, pcscf, scscf]
    text: UE の INVITE は Gm で P-CSCF に、Mw で S-CSCF に届きます。どちらの CSCF も利用者の設定を知りません。
  - id: isc
    title: S-CSCF が iFC で AS に送る
    show: [ref-isc]
    focus: [scscf, as2]
    text: S-CSCF と AS の間の SIP の参照点を ISC と呼びます。どの要求を AS に送るかは iFC が決めます。
  - id: cx
    title: iFC は Cx で届く
    show: [ref-cx]
    focus: [scscf, hss]
    text: 登録のときの SAA が、iFC を含むプロファイルを S-CSCF に届けます。
  - id: sh
    title: AS が Sh で設定を読む
    show: [ref-sh]
    focus: [as2, hss]
    checkpoint: true
    text: 利用者の設定は HSS にあります。AS は通話ごとに Sh で読みます。
  - id: ut
    title: 利用者は Ut で設定を変える
    show: [ref-ut]
    focus: [ue2, as2]
    text: Ut は UE と AS の間の HTTP のインタフェースです。`mmtel` の役割は AS として SIP の 5060 番で、XCAP のサーバーとして HTTP の 80 番で待ちます。
  - id: mrf
    title: 音声は MRF が流す
    show: [ref-mr]
    focus: [as2, mrfc]
    text: 音声ガイダンスを流すのも会議の音声を混ぜるのも MRF です。AS は Mr' で MRFC に頼みます。
```

- 付加サービスは、電話網が通話のほかに提供してきたものです。番号の通知と非通知、着信転送、規制、保留、割込通話などです。
- IMS の電話は MMTel のサービスです（TS 24.173）。網が何を提供するかは GSMA IR.92 2.3 にあります。
- CSCF は経路を決めるだけです。利用者の設定を知って通話を変えるのは、アプリケーションサーバー（AS）の MMTel AS です。

```text
 UE --- P-CSCF --- S-CSCF ===(SIP, iFC)===> MMTel AS ---(Sh)--- HSS
  |                                            |
  +---------------(Ut, HTTP + XCAP)------------+
```

- S-CSCF と AS の間の SIP の参照点を ISC と呼びます。`mmtel` の役割は、AS として SIP の 5060 番、XCAP のサーバーとして HTTP の 80 番で待ちます。
- IR.92 が網には任意としている MWI と ECT は作っていません。音声ガイダンスを流すのも会議の音声を混ぜるのも MRF です。Compose では `mrfc` と `mrfp` として動かしています。

> 手順書 [「MMTel と、AS が要る理由」](../../../docs/guide/ja/mmtel.md#mmtel-とas-が要る理由)

---

## 登場するノード

```ims-scene
id: nodes
nodes: [ue1, ue2, ue3, pcscf, icscf, scscf, hss, as1, as2]
lead: MMTel AS は 1 台です。この回の図では受け持つ利用者ごとに分けて、2 か所に描きます。
steps:
  - id: ues
    title: UE 1、UE 2、UE 3
    focus: [ue1, ue2, ue3]
    text: |
      UE 1（172.28.0.100）が発信し、UE 2（172.28.0.101）がサービスを設定します。
      UE 3（172.28.0.103）は UE 2 の着信の転送先です。
  - id: cscfs
    title: P-CSCF、I-CSCF、S-CSCF
    focus: [pcscf, icscf, scscf]
    text: |
      S-CSCF は 3 人の利用者をすべて担当し、iFC に従ってすべての INVITE を MMTel AS に送ります。
      P-CSCF は誰が発信し誰が応答したかを、網として示します。隠された識別子は UE に渡しません。
  - id: as
    title: MMTel AS（172.28.0.75）
    focus: [as1, as2]
    checkpoint: true
    text: |
      役割 `mmtel` のノードです。上は UE 1 のサービスを、下は UE 2 のサービスを動かすときの姿で、同じノードです。
      SIP は `mmtel.…`、Ut は `xcap.…` で待ちます。
  - id: hss
    title: HSS（172.28.0.10）
    focus: [hss]
    text: iFC を Cx で渡します。利用者ごとの設定と Ut のパスワードは Sh で渡します。
```

| ノード | アドレス | 役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 発信者。`tel:+12025550101`。 |
| UE 2 | 172.28.0.101 | サービスを設定する利用者。`tel:+12025550102`。 |
| UE 3 | 172.28.0.103 | UE 2 の着信の転送先。`tel:+12025550103`。 |
| P-CSCF、S-CSCF、I-CSCF | 172.28.0.40、30、20 | 識別子を示し、隠す。iFC で AS に送る。 |
| MMTel AS | 172.28.0.75 | 役割 `mmtel`。SIP は `mmtel.…`、Ut は `xcap.…`。 |
| HSS | 172.28.0.10 | iFC（Cx）。設定と Ut のパスワード（Sh）。 |

> 手順書 [「登場するノード」](../../../docs/guide/ja/mmtel.md#登場するノード)

---

## 要求が AS に届くしくみ（4 つのフィルタ条件）

```ims-scene
id: ifc
nodes: [ue1, pcscf, icscf, scscf, hss, as1, as2]
lead: S-CSCF は INVITE を iFC と照らし合わせ、一致した条件の AS に送ります。AS が答えないときの扱いも条件に書いてあります。
steps:
  - id: arrive
    title: UE 1 の INVITE が S-CSCF に届く
    only: [c1-ue-p, c1-p-s]
    active: c1-p-s
    text: UE 1 は Service-Route に沿って INVITE を送ります。S-CSCF はこれを UE 1 がかける通話として扱います。
  - id: match
    title: S-CSCF が iFC を照らし合わせる
    show: [scscf-ifc-orig]
    focus: [scscf]
    zoom: [scscf, hss, as1]
    text: 条件は優先度の順に評価します。SMS の 2 つの条件のあと、優先度 10 の条件に一致します。
  - id: send
    title: 一致した条件の AS に送る
    show: [c1-s-as1]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: 送り先は条件の ServerName です。S-CSCF は P-Served-User と、ODI を付けた Route を足します。
  - id: default
    title: AS が答えないとき
    show: [scscf-default]
    focus: [scscf]
    zoom: [scscf, hss, as1]
    text: 発信側の条件は通話を終え、着信側の条件は AS 無しで続けます。規制を確実に効かせるためです。
```

| 優先度 | 一致する要求 | DefaultHandling |
| --- | --- | --- |
| 10 | INVITE、ORIGINATING_REGISTERED | SESSION_TERMINATED |
| 11 | INVITE、TERMINATING_REGISTERED | SESSION_CONTINUED |
| 12 | INVITE、TERMINATING_UNREGISTERED | SESSION_CONTINUED |
| 13 | INVITE、ORIGINATING_CDIV | SESSION_TERMINATED |

- 送り先はどれも `sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060` です。
- 発信側は、AS が答えないと通話を終えます。規制を確実に効かせるためです。
- SMS の 2 つの条件（優先度 0 と 1）のあとに並びます。どれも 1 つのセッションケースにしか一致しないので、互いに競いません。TERMINATING_UNREGISTERED の条件は CFNL のためのものです。

> 手順書 [「4 つのフィルタ条件」](../../../docs/guide/ja/mmtel.md#4-つのフィルタ条件)。TS 29.228 Annex E、B.2.3。TS 24.229 5.4.3.2 step 5、5.4.3.3 step 4。

---

## 発信側か着信側かを伝える P-Served-User

```ims-scene
id: sescase
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: 1 つの AS が通話の両端を受け持ちます。誰のための、どの場合の INVITE かは、S-CSCF が P-Served-User で伝えます。
steps:
  - id: orig
    title: orig（利用者がかける通話）
    only: [c1-s-as1, as1-served]
    active: as1-served
    detail: [as1-served]
    zoom: [scscf, as1, as2]
    checkpoint: true
    text: "`sescase=orig` の INVITE です。AS は UE 1 の発信側のサービスを動かします。"
  - id: term
    title: term（利用者への通話）
    only: [c2-s-as2, as2-served]
    active: as2-served
    detail: [as2-served]
    zoom: [scscf, as1, as2]
    checkpoint: true
    text: "`sescase=term` の INVITE です。規制が先で、転送、識別子と続きます。"
  - id: unreg
    title: regstate=unreg（登録していない利用者への通話）
    only: [cfnl-c2-s-as2]
    zoom: [scscf, as1, as2]
    text: 未登録の状態のサービスを持つ利用者への通話です。CFNL のスライドで、この INVITE をもう一度見ます。
  - id: cdiv
    title: orig-cdiv（AS が転送した通話）
    only: [cfu-c4-s-as2, as2-cdiv]
    active: as2-cdiv
    detail: [cfu-c4-s-as2, as2-cdiv]
    zoom: [scscf, as1, as2]
    text: 転送した通話は転送元の利用者の発信側のサービスを受けます。動くのは OCB だけです。
```

```text
P-Served-User: <sip:+12025550101@…>;sescase=orig;regstate=reg
P-Served-User: <sip:+12025550102@…>;sescase=term;regstate=reg
P-Served-User: <sip:+12025550102@…>;sescase=term;regstate=unreg
P-Served-User: <sip:+12025550102@…>;orig-cdiv;regstate=reg
```

| 場合 | AS が動かすサービスの順 |
| --- | --- |
| orig | OCB、OIR、応答に TIP |
| term | ICB と ACR、CFU と CFNL、OIP と TIR、利用者への通話。終わり方で CFB か CFNR |
| orig-cdiv | 転送元の利用者の OCB |

フィルタは `sip.P-Served-User` です。

- 1 つの AS が通話の両端を受け持つので、どの INVITE が、どの利用者の、どのケースで来たかを知る必要があります。P-Served-User の無い INVITE は S-CSCF から来たものではないので、AS は 403 を返します。

> 手順書 [「P-Served-User と session case」](../../../docs/guide/ja/mmtel.md#p-served-user-と-session-case)。RFC 5502、RFC 8498。TS 24.229 5.7.1.3A。

---

## コードで見る着信側の順番

[internal/mmtel/sessioncase.go](../../../internal/mmtel/sessioncase.go)

```go
	info := c.callInfo(fields, inv.RequestURI)
	if c.barIncoming(info) {
		return
	}
	// CFU, and CFNL for a user who is not registered (TS 24.604 4.6.7).
	if d, ok := c.divertAtSetup(info); ok {
		c.divert(d)
		return
	}
```

- 規制が先です。規制した通話は転送しません。
- CFU と CFNL は、INVITE が届いた時点で転送します。利用者を呼び出しません。
- このあとに OIP と TIR が続きます。そのあと `c.attempt` が利用者への区間を試し、その終わり方で CFB、CFNRc、CFNR が決まります。コメントの条項は、サービスどうしの関係を定めた条項です。

---

## ODI と、1 つの通話に 3 つのダイアログ

```ims-scene
id: odi
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: AS は INVITE に UA として答え、自分の Call-ID で新しい INVITE を S-CSCF に送り返します。1 つの通話が 3 つのダイアログになります。
steps:
  - id: c1
    title: C1（UE 1 から UE 1 の AS まで）
    only: [c1-ue-p, c1-p-s, c1-s-as1]
    text: S-CSCF は Route の先頭に AS を、その次に ODI を付けた自分を置きます。
  - id: c2
    title: C2（AS が新しい INVITE を送り返す）
    show: [c2-as1-s]
    zoom: [scscf, hss, as1]
    text: AS は自分の Route を取り除き、残りの Route に沿って送ります。INVITE は ODI を持って S-CSCF に戻ります。
  - id: odi
    title: S-CSCF が ODI から続ける
    show: [scscf-odi]
    focus: [scscf]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: S-CSCF は一致した条件の次から照らし合わせを続けます。SMS の回の IP-SM-GW は何も送り返しませんでしたが、MMTel AS は送り返します。
  - id: icscf
    title: I-CSCF が UE 2 の S-CSCF を探す
    only: [c2-s-i, lir, lia, c2-i-s]
    active: c2-i-s
    detail: [c2-s-i, lia]
    text: ENUM で番号を SIP URI に変え、I-CSCF に送ります。LIR はダイアログ C2 のものです。S-CSCF が I-CSCF に送るのが AS の INVITE だからです。
  - id: term
    title: 着信側の条件で、もう一度 AS へ
    show: [c2-s-as2]
    checkpoint: true
    text: 今度は UE 2 の着信側の条件に一致し、`sescase=term` で UE 2 の AS に届きます。
  - id: c3
    title: C3（UE 2 の AS から UE 2 まで）
    only: [c2-s-as2, c3-as2-s, c3-s-p, c3-p-ue2]
    active: c3-p-ue2
    detail: [c3-as2-s, c3-p-ue2]
    text: |
      3 つのダイアログはそれぞれ自分の Call-ID、タグ、CSeq を持ちます。
      プロキシではなく B2BUA なのは、CFNR、To を変える転送、From を匿名にする OIR、音声ガイダンス、会議が、どれも B2BUA を必要とするからです。
```

```text
Route: <sip:mmtel.…:5060;lr>,<sip:scscf.…:5060;lr;odi=...>
```

```text
 UE 1 ... S-CSCF -- AS(UE 1) -- S-CSCF ... I-CSCF ... S-CSCF -- AS(UE 2) -- S-CSCF ... UE 2
 |<------ C1 ------>|<------------------ C2 ------------------->|<--------- C3 ---------->|
```

- AS は routeing B2BUA です。受けた INVITE に UA として答え、自分の Call-ID で新しい INVITE を送ります。
- 新しい INVITE は ODI を持って S-CSCF に戻り、S-CSCF は次の条件から続けます。SMS の回の IP-SM-GW は何も送り返しませんでしたが、MMTel AS は送り返します。
- プロキシではなく B2BUA なのはなぜでしょうか。CFNR、To を変える転送、From を匿名にする OIR、音声ガイダンス、会議が、どれも B2BUA を必要とするからです。

> 手順書 [「オリジナルダイアログ識別子」](../../../docs/guide/ja/mmtel.md#オリジナルダイアログ識別子) と [「プロキシではなく B2BUA」](../../../docs/guide/ja/mmtel.md#プロキシではなく-b2bua1-つの通話に-3-つのダイアログ)。TS 24.229 5.7.5、5.7.5.2.1。

---

## 3 つのダイアログがキャプチャに与えるもの

| 見るもの | 見どころ |
| --- | --- |
| Call-ID | 1 つの通話に 3 つ。転送した通話ではさらに 3 つ増える。 |
| P-Charging-Vector の icid-value | 網の中では 3 つとも同じ。ラボはこれで 1 つの通話にまとめる。 |
| LIR | ダイアログ C2 のもの。 |
| PRACK と ACK | ダイアログごとに進む。ACK が 200 より先に UE 2 に届くことがある。 |
| BYE | AS が答え、同じ Reason を付けた新しい BYE を次に送る。 |

フィルタは `sip.P-Charging-Vector contains "icid-value=..."` です。`...` には、UE 1 の P-CSCF が S-CSCF に送る INVITE から写した値を入れます。

- このヘッダは P-CSCF で止まります。UE は付けず、P-CSCF は UE に送る要求と応答のすべてから消します。ims0 ブリッジのキャプチャには UE との区間も入っていますが、このフィルタでは出てきません。
- Wireshark の **VoIP Calls** は、ダイアログを 1 つずつ別の通話として並べます。1 つの通話が、一見 3 つに見えます。

> 手順書 [「プロキシではなく B2BUA」](../../../docs/guide/ja/mmtel.md#プロキシではなく-b2bua1-つの通話に-3-つのダイアログ)。TS 24.229 5.7.5.1、5.2.1。RFC 3326 2。

---

## 設定のある場所（simservs の文書）

```ims-scene
id: document
nodes: [ue2, pcscf, scscf, hss, as2]
lead: 利用者の設定は HSS にある文書 1 つだけです。AS は通話ごとにそれを読み、写しを持ちません。
steps:
  - id: hss
    title: HSS が利用者ごとの文書を持つ
    only: [hss-doc]
    focus: [hss]
    text: XCAP の simservs の文書です（TS 24.623、RFC 4825）。置き場所は HSS だけです。
  - id: call
    title: 着信のたびに読む
    only: [c2-s-as2, udr2, uda2]
    active: udr2
    detail: [udr2, uda2]
    zoom: [scscf, hss, as2]
    text: AS は着信の INVITE を受けると、UE 2 の文書を UDR で読みます（TS 29.364 7.2）。
  - id: nocopy
    title: AS は写しを持たない
    show: [as2-nocopy]
    focus: [as2]
    zoom: [scscf, hss, as2]
    checkpoint: true
    text: そのため、AS のどのインスタンスでも、どの通話でも受けられます。
  - id: ut
    title: UE は Ut で同じ文書を変える
    only: [hss-doc, ut-get1]
    active: ut-get1
    text: XCAP のサーバーも AS のノードにあり、要求のたびに同じ文書を Sh で読み書きします。UE は規則を足さず、ある規則を変えてサービスを有効にします。
```

- 利用者ごとに XML の文書が 1 つあります。XCAP の simservs です（TS 24.623、RFC 4825）。
- 置き場所は HSS だけです。Sh の repository data で、Service-Indication は `MMTEL-Services` です（TS 29.364 7.2）。
- AS は通話ごとに UDR で読み、写しを持ちません。AS のどのインスタンスでも、どの通話でも受けられるようにするためです。

[deploy/compose/hss.yaml](../../../deploy/compose/hss.yaml)

```xml
            <cp:rule id="cfu">
              <cp:conditions><rule-deactivated/></cp:conditions>
              <cp:actions><forward-to><target/></forward-to></cp:actions>
            </cp:rule>
```

- 最初の文書には、IR.92 が求めるサービスすべての規則があります。CDIV と CB の規則は `rule-deactivated` で止めてあり、転送先もありません。UE は規則を足しません。ある規則を変えて、サービスを有効にします。
- 文書のアドレスは `http://xcap.ims.mnc001.mcc001.3gppnetwork.org/simservs.ngn.etsi.org/users/<XUI>/simservs.xml` です。XUI は利用者の公開識別子です。

> 手順書 [「文書」](../../../docs/guide/ja/mmtel.md#文書)。GSMA IR.92 2.3.2 Note 3。

---

## Ut で CFU を有効にする

```ims-scene
id: ut
view: ladder
nodes: [ue2, as2, hss]
lead: 1 回の変更で、UE は GET、GET、PUT と送ります。AS は要求のたびに HSS の文書を読み、文書の版を ETag で示します。
steps:
  - id: get1
    title: 資格情報を付けずに GET する
    show: [ut-get1]
    text: "`ue xcap set cfu on tel:+12025550103` は、まず文書の全体を読みに行きます。"
  - id: challenge
    title: AS がチャレンジを返す（401）
    show: [as2-challenge, ut-401]
    active: as2-challenge
    detail: [as2-challenge, ut-401]
    checkpoint: true
    text: AS はほかの何よりも先に要求を認証します。利用者について何も読まずに返すので、どの XUI でも同じ 401 になります。
  - id: get2
    title: Digest で答えて、もう一度 GET する
    show: [ut-get2, ut-udr-cred, ut-uda-cred, ut-udr-doc, ut-uda-doc]
    active: ut-get2
    detail: [ut-get2, ut-udr-cred, ut-uda-cred]
    text: AS はここで初めて Ut のパスワードを読み、答えを確かめてから文書を読みます。UDR は 2 つです。
  - id: etag
    title: 文書と ETag が返る
    show: [ut-200-get]
    checkpoint: true
    text: ETag は、HSS が更新のたびに確かめる Sequence Number です。
  - id: put
    title: 規則を 1 つだけ PUT する
    show: [ut-put]
    text: 読んだ版の ETag を If-Match に入れて送ります。
  - id: reread
    title: AS が読み直して、規則を置き換える
    show: [ut-udr-cred2, ut-uda-cred2, ut-udr-doc2, ut-uda-doc2, as2-replace]
    active: as2-replace
    checkpoint: true
    text: 資格情報と文書をもう一度読みます。途中で文書が変わっていれば If-Match が合わず、AS は PUR を送らずに 412 を返します。
  - id: pur
    title: PUR で書き込む
    show: [ut-pur, ut-pua]
    active: ut-pur
    detail: [ut-pur, ut-pua]
    text: 読んでから PUR までの間にほかの書き込みが入ると、HSS が PUR を断ります。AS はやはり 412 を返します。
  - id: done
    title: 200 と新しい ETag
    show: [ut-200-put]
    text: このあと UE は文書の全体をもう一度 GET し、設定として表示します。
```

`ue xcap set cfu on tel:+12025550103` が送るもの。

| 順 | 要求 | 応答と、その間の Sh |
| --- | --- | --- |
| 1 | GET。資格情報なし | UDR は無し。401 とチャレンジ 2 つ（SHA-256、MD5）。どの利用者でも同じ。 |
| 2 | GET。`Authorization: Digest` | UDR が 2 つ。Ut のパスワードと文書。200 と `ETag: "0"`。 |
| 3 | 規則 1 つの PUT。`~~` のノードセレクタ、`If-Match: "0"` | PUR に `<SequenceNumber>1</SequenceNumber>`。PUA のあと 200 と `ETag: "1"`。 |
| 4 | GET | 設定として表示する文書。 |

- 途中で文書が変わっていれば、If-Match が合わず、AS は PUR を送らずに 412 を返します。ETag は、HSS が更新のたびに確かめる Sequence Number です。読んでから PUR までの間にほかの書き込みが入ると、HSS が DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC で PUR を断り、AS はやはり 412 を返します。
- フィルタは `http && ip.addr == 172.28.0.75` です。`http.www_authenticate` でチャレンジを、`http.request.method == "PUT"` で変更を探します。PUT の URI とボディの全体は手順書にあります。

> 手順書 [「CFU を有効にする変更」](../../../docs/guide/ja/mmtel.md#cfu-を有効にする変更)。RFC 7616 3.3。TS 24.623 5.2.3.2.1。RFC 4825 6、7.11。TS 29.328 7.6.1、6.1.2.1。

---

## コードで見る HTTP Digest のチャレンジ

[internal/xcap/digest.go](../../../internal/xcap/digest.go)

```go
func (n nonces) challenge(h http.Header, realm string, offered []Algorithm, stale bool) {
	nonce := n.issue()
	for _, a := range offered {
		h.Add("WWW-Authenticate", sip.Challenge{
			Realm: realm, Nonce: nonce, Stale: stale, Algorithm: string(a), QOP: []string{qopAuth},
		}.String())
	}
}
```

- 最初のチャレンジは、ホームドメインで `algorithms`（SHA-256、MD5 の順）を示します。利用者によらないので、AS は利用者について何も読まずに送り、どの XUI でも同じになります。どのチャレンジも同じ nonce を使います。
- 認証できなかった資格情報のあとは、`realm` と `offered` が利用者の資格情報のものになります。運用者がほかの realm や MD5 だけで与えたのでなければ、同じです。
- 資格情報は HSS の repository data です。Service-Indication は `Ut-Digest-Credential` です。入っているのはパスワードではなく HA1 の値です（`internal/xcap/credential.go` のコメント）。
- WWW-Authenticate は SIP Digest と同じ形なので、コードは `internal/sip` の `Challenge` をそのまま使います。登録の回の 401 と比べてみます。

---

## 実行する

```sh
docker compose up -d --build
docker compose run --rm ue2 xcap \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2 set cfu on tel:+12025550103

# UE 3 が着信を待つ
docker compose run --rm ue3 answer -impi 001010000000003@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org -password test-password-3
docker compose run --rm ue call tel:+12025550102
```

- UE 1 のログに `"msg":"call progress","status":181` と `"msg":"call answered"` が出ます。
- 止めるときは `set cfu off` です。`ue xcap` は登録を必要としません。ほかのコマンドは `get`、`set SERVICE on|off [TARGET]`、`oir restricted|not-restricted`、`noreply SECONDS` です。
- `ue2` のサービスはアドレスが決まっているので、ほかの `ue2` のコンテナが動いている間は `ue2 xcap` を実行できません。

> 手順書 [「動かしてみる」](../../../docs/guide/ja/mmtel.md#動かしてみる)。全部のサービスを、`scripts/` にある付加サービスの e2e のスクリプトが順に行います。

---

## キャプチャを開く

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (http && ip.addr == 172.28.0.75) || diameter.cmd.code in {301, 302, 306, 307}
```

- 通話、Ut、AS の Sh の読み出しと更新、LIR と SAR が残ります。
- **Telephony > VoIP Calls** は、ダイアログを 1 つずつ別の通話として並べます。
- e2e のスクリプトは、すべてのサービスを 1 つのキャプチャに残します。表示フィルタで 1 つのサービスに絞ります。

| サービス | きっかけ | キャプチャで見るもの |
| --- | --- | --- |
| OIR | 発信者の既定が非通知 | 匿名の From、`Privacy: id` |
| CFU、CFB、CFNR、CFNL | 着信、486、無応答、未登録 | 181、History-Info、`cause=` |
| BAIC、ACR、BAOC | 着信、匿名の着信、発信 | 183 で音声ガイダンス、そのあと 603 か 433 |

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/mmtel.md#キャプチャを開く)

---

## AS を通る通常の通話と TIP

```ims-scene
id: normal
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: 既定の文書では、AS は通話を何も変えません。それでも INVITE は AS を 2 回通り、200 OK が応答した相手を示します。
steps:
  - id: c1
    title: C1 が UE 1 の AS に届く
    only: [c1-ue-p, c1-p-s, c1-s-as1]
    active: c1-s-as1
    text: "`sescase=orig` の P-Served-User と、`odi` の付いた Route を見ます。"
  - id: udr
    title: AS が UE 1 の設定を読む（UDR）
    show: [udr1, uda1]
    active: udr1
    detail: [udr1, uda1]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: 既定の文書なので、通話は何も変わりません。
  - id: c2
    title: AS が C2 の INVITE を送る
    only: [c2-as1-s]
    text: AS の INVITE は C1 と同じ icid-value を持ちます。
  - id: lir
    title: I-CSCF が LIR で UE 2 の S-CSCF を探す
    only: [c2-s-i, lir, lia, c2-i-s]
    active: lir
    text: S-CSCF は ENUM を引いて I-CSCF に送ります。この LIR はダイアログ C2 のものです。
  - id: term
    title: 2 回目の AS
    only: [c2-s-as2, udr2, uda2]
    active: c2-s-as2
    checkpoint: true
    text: 今度は `sescase=term` で届き、AS は UE 2 の設定を読みます。
  - id: c3
    title: C3 が UE 2 に届く
    only: [c3-as2-s, c3-s-p, c3-p-ue2]
    active: c3-as2-s
    text: このあと 183、PRACK、180 がダイアログごとに進みます。通話の回のとおりです。
  - id: answer
    title: P-CSCF が応答した相手を示す
    only: [ok-ue2-p, pcscf-tip]
    active: pcscf-tip
    text: UE 2 の P-CSCF は INVITE の P-Called-Party-ID を 200 OK の P-Asserted-Identity に入れます。
  - id: tip
    title: UE 1 が応答した相手を受け取る（TIP）
    hide: [ok-ue2-p]
    show: [ok-p-ue1]
    text: |
      200 OK は 2 つの AS を通って UE 1 に戻ります。図では途中の区間を省いています。
      どの利用者も既定で TIP を持っています。
```

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF から AS への C1 の INVITE | `sescase=orig` の P-Served-User と、`odi` の付いた Route。 |
| AS の UDR | Data-Reference 0、Service-Indication `MMTEL-Services`。 |
| AS の C2 の INVITE | 同じ icid-value。このあと ENUM と LIR。 |
| 2 回目の AS | `sescase=term` で届き、C3 を送り返す。 |
| 200 OK | P-Asserted-Identity に UE 2。これが TIP。 |

- 既定の文書では、AS は通話を何も変えません。それでも、どの INVITE も AS を 2 回通ります。どの利用者も既定で TIP を持ち、P-CSCF が P-Called-Party-ID を P-Asserted-Identity にします。
- 手順書のフレーム番号はスクリプトを 1 回動かしたときのものなので、手元のキャプチャとは違います。

> 手順書 [「AS を通る通常の通話」](../../../docs/guide/ja/mmtel.md#as-を通る通常の通話)。TS 24.229 5.4.3.2 step 5、5.7.5.2.1、5.2.6.4.4 step 1。TS 29.364 7.2.0。TS 24.608 4.5.2.4。

---

## OIR（発信者が識別子を隠す）

```ims-scene
id: oir
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: 網の中では、識別子は確かめられたまま残ります。隠すのは、信頼の範囲の外にある UE に対してだけです。
steps:
  - id: restrict
    title: UE 1 の AS が識別子を隠す
    only: [oir-c1-s-as1, as1-oir]
    active: as1-oir
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: "UE 1 は既定を非通知にしてから発信しました。AS は From を匿名にし、`Privacy: id` を足します。"
  - id: c2
    title: 網の中では P-Asserted-Identity が残る
    show: [oir-c2-as1-s]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: 識別子は網が保証したままです。図では I-CSCF を通る区間を省いています。
  - id: oip
    title: UE 2 の AS が OIP を動かす
    only: [oir-c2-s-as2, as2-oip]
    active: as2-oip
    text: UE 2 は OIP を持っているので、AS は `id` を残します。
  - id: strip
    title: S-CSCF が P-Asserted-Identity を取り除く
    show: [oir-c3-as2-s, scscf-strip]
    active: scscf-strip
    focus: [scscf]
    checkpoint: true
    text: UE は信頼ドメインの外にあり、Privacy に `id` があるからです。
  - id: deliver
    title: UE 2 に届く INVITE
    show: [oir-c3-s-p, oir-c3-p-ue2]
    active: oir-c3-p-ue2
    text: "匿名の From と `Privacy: id` を持ち、UE 1 を示すものは何も入っていません。"
```

`ue xcap oir restricted` で、UE 1 の既定を非通知にしてから発信します。

| ノード | すること |
| --- | --- |
| UE 1 の AS | `Privacy: id` を足し、From を `"Anonymous" <sip:anonymous@anonymous.invalid>` にする。P-Asserted-Identity は残す。 |
| UE 2 の AS | OIP を動かす。`id` を残す。 |
| UE 2 の S-CSCF | P-Asserted-Identity を取り除く。UE は信頼ドメインの外にある。 |

フィルタは `sip.Privacy` と `sip.from.addr == "sip:anonymous@anonymous.invalid"` です。

- 網の中では、識別子は確かめられたまま残ります。隠すのは、信頼の範囲の外にある UE に対してだけです。OIR は一時モードなので、UE は `Privacy: none` で 1 回だけ番号を通知できます。
- TIR は同じしくみの逆向きです。UE 2 の AS が応答の Privacy に `id` を入れ、UE 1 の P-CSCF が P-Asserted-Identity を取り除きます（TS 24.608 4.5.2.9、RFC 3325 7）。

> 手順書 [「OIR（発信者が識別子を隠す）」](../../../docs/guide/ja/mmtel.md#oir発信者が識別子を隠す)。TS 24.607 4.5.2.4、4.5.2.9。RFC 3323 4.1.1.3。TS 24.229 5.4.3.3 step 12、4.4.2。RFC 3325。

---

## CFU（すべての着信を転送する）

```ims-scene
id: cfu
view: ladder
nodes: [ue1, ue3, pcscf, scscf, as2]
lead: CFU では、UE 2 の AS が INVITE を受けた時点で転送します。UE 2 の電話は鳴りません。
steps:
  - id: arrive
    title: 着信が UE 2 の AS に届く
    show: [cfu-c2-s-as2]
    text: UE 2 は Ut で CFU を UE 3 に設定し、登録したままです。UE 1 が UE 2 に発信しました。
  - id: decide
    title: AS が CFU で転送を決める
    show: [as2-cfu]
    focus: [as2]
    text: 規制の次に CFU を評価します。INVITE は UE 2 に届きません。
  - id: notify
    title: 181 で発信者に知らせる
    show: [cfu-181, cfu-181-ue1]
    active: cfu-181
    detail: [cfu-181, cfu-181-ue1]
    checkpoint: true
    text: 図では UE 2 の AS から UE 1 までの途中の区間を省いています。UE 3 の項目に `Privacy=history` が付くのは、UE 3 が番号の表示を許すかどうかを UE 2 の AS が知らないからです。
  - id: c4
    title: 転送の INVITE（C4）
    show: [cfu-c4]
    active: cfu-c4
    text: Request-URI に転送の理由（RFC 4458）が付き、History-Info に転送の経歴が入ります。
  - id: cdiv
    title: S-CSCF が転送に気づく
    show: [scscf-cdiv]
    focus: [scscf]
    text: ODI の付いた要求の Request-URI が変わったので、転送元の UE 2 の ORIGINATING_CDIV の条件を評価します。
  - id: ocb
    title: 転送元の発信の規制
    show: [cfu-c4-s-as2, as2-cdiv]
    active: as2-cdiv
    detail: [cfu-c4-s-as2, as2-cdiv]
    checkpoint: true
    text: INVITE は `orig-cdiv` を付けて AS に戻ります。UE 2 の発信の規制はここで UE 3 への通話に掛かります。
  - id: ue3
    title: 通話が UE 3 に届く
    show: [cfu-c5, cfu-c6-p-ue3]
    active: cfu-c6-p-ue3
    text: S-CSCF は番号を ENUM で UE 3 の SIP URI に変えます。通話は I-CSCF と UE 3 の AS を通って UE 3 に届きます。図では途中の区間を省いています。
```

| 見るもの | 見どころ |
| --- | --- |
| UE 2 の AS | INVITE が届いた時点で転送する。UE 2 には届かない。 |
| 181 Call Is Being Forwarded | 発信者への通知。転送先の項目に `Privacy=history`。 |
| 転送の INVITE（C4）の Request-URI | `sip:+12025550103@…;user=phone;cause=302` |
| History-Info | index 1 が UE 2、index 1.1 が UE 3 で `mp=1`。 |
| S-CSCF から AS への INVITE | `orig-cdiv`。UE 2 の発信の規制がここで掛かる。 |

- 転送は 1 つの通話で 5 回までです。超えると `Warning: 399` を付けた 480 です。
- cause は CFU が 302、CFB が 486、CFNR が 408、CFNL が 404、CFNRc が 503 です（TS 24.604 Annex C）。手順書のシーケンス図にはダイアログが C1 から C6 まで 6 つあります。
- UE 3 の項目に `Privacy=history` が付くのはなぜでしょうか。UE 3 が番号の表示を許すかどうかを、UE 2 の AS は知らないからです。

フィルタは `sip.History-Info`、`sip.r-uri contains "cause="`、`sip.Status-Code == 181` です。

> 手順書 [「CFU（すべての着信を転送する）」](../../../docs/guide/ja/mmtel.md#cfuすべての着信を転送する)。TS 24.604 4.5.2.6.2、4.5.2.6.3、4.5.2.6.4、4.5.2.6.1。RFC 4458。RFC 7044 10.3。TS 24.229 5.4.3.3 step 3。

---

## CFB（着信者が話し中）

```ims-scene
id: cfb
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: CFB では、AS はまず UE 2 を呼び出します。UE 2 が話し中と答えたら、転送します。
steps:
  - id: busy
    title: UE 2 が話し中（486）
    show: [cfb-486-ue2-p, cfb-486-p-s, cfb-486-s-as2]
    active: cfb-486-ue2-p
    checkpoint: true
    text: UE 2 がダイアログ C3 の INVITE に 486 Busy Here を返します。
  - id: cfb
    title: AS が話し中で転送する
    show: [as2-cfb]
    focus: [as2]
    text: "AS は 486 に ACK を返し、UE 1 に 181 を送って、`cause=486` で転送します。181 から先は CFU と同じです。"
```

| 場合 | 見どころ |
| --- | --- |
| CFB | UE 2 が C3 に 486。AS は ACK を返し、181 を送り、`cause=486` で転送する。 |

> 手順書 [「CFB」](../../../docs/guide/ja/mmtel.md#cfb着信者が話し中)。TS 24.604 4.5.2.6.2.2 b、4.5.2.6.3。

---

## CFNR（誰も応答しない）

```ims-scene
id: cfnr
view: ladder
nodes: [ue1, ue2, pcscf, scscf, as1, as2]
lead: CFNR では、AS は UE 2 を呼び出しながら時間を測ります。時間内に応答が無ければ、呼び出しを止めて転送します。
steps:
  - id: ringing
    title: UE 2 が鳴る（180）
    show: [cfnr-180-ue2-p, cfnr-180-p-s, cfnr-180-s-as2]
    active: cfnr-180-s-as2
    text: UE 2 は CFNR を UE 3 に、無応答のタイマーを 5 秒に設定しています。応答せずに鳴り続けます。
  - id: timer
    title: 最初の 180 でタイマーが始まる
    show: [as2-timer]
    focus: [as2]
    checkpoint: true
    text: フォークのほかの分岐から来た 180 では、タイマーをやり直しません。
  - id: cancel
    title: タイマーが切れて CANCEL
    show: [cfnr-cancel-as2-s, cfnr-cancel-s-p, cfnr-cancel-p-ue2]
    active: cfnr-cancel-as2-s
    detail: [cfnr-cancel-as2-s, cfnr-cancel-p-ue2]
    text: 途中の CSCF はどれも、自分の CANCEL にこの Reason を写します。そのため UE 2 にも理由が伝わります。
  - id: terminated
    title: UE 2 が 487 で答える
    show: [cfnr-487-ue2-p, cfnr-487-p-s, cfnr-487-s-as2]
    active: cfnr-487-ue2-p
    checkpoint: true
    text: UE 2 への区間がここで終わります。
  - id: early
    title: 199 が UE 2 の初期ダイアログを終える
    show: [cfnr-199, cfnr-199-s-as1, as1-keep]
    active: as1-keep
    detail: [cfnr-199, as1-keep]
    text: 199 はダイアログ C2 の上を UE 1 の AS まで進みます。UE 1 には届きません。図では I-CSCF を通る区間を省いています。
  - id: divert
    title: 181 と、cause=408 の転送
    show: [cfnr-181, cfnr-181-ue1, cfnr-c4]
    active: cfnr-c4
    detail: [cfnr-181, cfnr-c4]
    text: 181 から先は CFU と同じです。キャプチャで 180 から CANCEL までの時間を測ってみます。
```

| 場合 | 見どころ |
| --- | --- |
| CFNR のタイマー | 最初の 180 で始まる。長さは `NoReplyTimer`。e2e では 5 秒。 |
| CFNR の CANCEL | `Reason: SIP;cause=408;text="Request Timeout"`。途中の CSCF も写す。 |
| 199 Early Dialog Terminated | C2 で UE 2 の初期ダイアログを終える。UE 1 には届かない。 |
| そのあと | 181 を送り、`cause=408` で転送する。 |

フィルタは `sip.Method == "CANCEL" && sip.Reason` と `sip.Status-Code == 199` です。

- NoReplyTimer が無ければ、事業者の既定の 20 秒になります。キャプチャで 180 から CANCEL までの時間を測ってみます。
- UE 1 の AS は UE 1 との初期ダイアログを 1 つのまま保つので、UE 1 は 181 と UE 3 の応答を同じ初期ダイアログで受けます。

> 手順書 [「CFNR」](../../../docs/guide/ja/mmtel.md#cfnr誰も応答しない)。TS 24.604 4.5.2.6.3 item 2、4.8.1。RFC 3326 2。RFC 6228。

---

## CFNL（着信者が登録していない）

```ims-scene
id: cfnl
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: 登録していない UE 2 への通話も、未登録の状態のサービスがあれば AS に届きます。AS は呼び出さずに、すぐに転送します。
steps:
  - id: lir
    title: I-CSCF の LIR に 2003 が返る
    only: [cfnl-c2-s-i, cfnl-lir, cfnl-lia]
    active: cfnl-lia
    zoom: [icscf, hss, scscf]
    text: UE 2 は CFNL を UE 3 に設定し、登録していません。HSS は S-CSCF の名前の代わりに能力を返します。
  - id: choose
    title: I-CSCF が S-CSCF を選ぶ
    show: [icscf-choose, cfnl-c2-i-s]
    active: icscf-choose
    zoom: [icscf, hss, scscf]
    text: 登録のときの 1 回目の UAA と同じく、能力から S-CSCF を選び、INVITE を送ります。
  - id: sar
    title: S-CSCF がプロファイルを取る（SAR）
    show: [cfnl-sar, cfnl-saa]
    active: cfnl-sar
    detail: [cfnl-sar, cfnl-saa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: UNREGISTERED_USER の SAR です。SAA に UE 2 の iFC が入っています。
  - id: match
    title: 未登録の着信の条件に一致する
    only: [cfnl-c2-i-s, scscf-ifc-unreg]
    active: scscf-ifc-unreg
    focus: [scscf]
    text: TERMINATING_UNREGISTERED の条件を評価し、AS に送ります。
  - id: to-as
    title: regstate=unreg の INVITE が AS に届く
    show: [cfnl-c2-s-as2]
    checkpoint: true
    text: P-Served-User が、登録していない利用者への通話であることを伝えます。
  - id: divert
    title: AS がすぐに転送する
    only: [as2-cfnl, cfnl-c4]
    active: as2-cfnl
    detail: [as2-cfnl, cfnl-c4]
    text: CFNL の規則には `<not-registered/>` の条件があるので、AS は UE 2 を呼び出さずに転送します。
```

| 順 | 見るもの | 見どころ |
| --- | --- | --- |
| 1 | I-CSCF の LIR | DIAMETER_UNREGISTERED_SERVICE（2003）と S-CSCF の能力。 |
| 2 | S-CSCF の SAR | UNREGISTERED_USER でプロファイルを取る。 |
| 3 | AS への INVITE | `sescase=term;regstate=unreg` |
| 4 | AS の転送 | `<not-registered/>` の規則で、すぐに `cause=404`。 |

- CFNL が無ければ、AS は INVITE をそのまま返し、I-CSCF ではなく S-CSCF が 480 を返します。
- LIA が 2003 なのは、どの試験用の加入者にも未登録のときのサービス（TERMINATING_UNREGISTERED の MMTel AS の iFC）があるからです。それが無い加入者なら DIAMETER_ERROR_IDENTITY_NOT_REGISTERED になり、I-CSCF が 480 を返します。

フィルタは `diameter.Experimental-Result-Code == 2003` と `sip.P-Served-User contains "regstate=unreg"` です。

> 手順書 [「CFNL（着信者が登録していない）」](../../../docs/guide/ja/mmtel.md#cfnl着信者が登録していない)。TS 29.228 6.1.4.1 step 3、6.7、6.1.2.1。TS 24.229 5.4.3.3。TS 24.604 4.5.2.6.2、4.6.7。

---

## 規制

```ims-scene
id: barring
view: ladder
nodes: [ue1, pcscf, scscf, as2, mrfc]
lead: 着信の規制は、UE 2 の AS が最初に評価する規則です。断る前に、発信者に音声ガイダンスを流します。
steps:
  - id: arrive
    title: 着信が UE 2 の AS に届く
    show: [bar-c2-s-as2]
    text: UE 2 は `baic on` にしています。UE 1 からここまでは AS を通る通常の通話と同じです。
  - id: bar
    title: AS が着信を規制する
    show: [as2-icb]
    checkpoint: true
    text: ほかのどのサービスよりも先に評価します。通話は UE 2 に届きません。
  - id: mrfc
    title: MRFC に音声ガイダンスを頼む
    show: [bar-m-invite, bar-m-200]
    active: bar-m-invite
    detail: [bar-m-invite, bar-m-200]
    text: 流す音声を Request-URI で指定した INVITE を、SDP なしで送ります。MRFC は MRFP を指す SDP で答えます。
  - id: early
    title: 183 で発信者のメディアを MRFP に向ける
    show: [bar-183, bar-183-ue1]
    active: bar-183
    checkpoint: true
    text: 183 は高信頼の暫定応答です。図では UE 2 の AS から P-CSCF までの途中の区間を省いています。
  - id: play
    title: 音声ガイダンスが流れる
    show: [bar-m-ack]
    text: 発信者が PRACK で 183 を受け取ったことを伝えると、AS は MRFC に ACK を送ります。MRFP が音声ガイダンスを流し始めます。
  - id: decline
    title: 音声ガイダンスが終わって 603
    show: [bar-m-bye, bar-603, bar-603-ue1]
    active: bar-603
    text: MRFC が BYE を送り、AS は Reason を付けた 603 を返します。ACR なら 433 です。BAOC なら UE 1 の AS が 603 を返し、C2 はありません。
```

| サービス | 設定 | 見どころ |
| --- | --- | --- |
| BAIC | UE 2 の `baic on` | UE 2 の AS が 603 Decline。ほかのどのサービスよりも先。 |
| ACR | `<anonymous/>` の規則 | 433 Anonymity Disallowed。 |
| BAOC | UE 1 の `baoc on` | UE 1 の AS が 603。自分の INVITE を送らない。緊急通報は規制しない。 |
| BOIC、BOIC-exHC | `boic on` など | 着信者の国番号をホーム網の `1` と比べる。 |

- 603 や 433 の前に、発信者には音声ガイダンスが流れます。AS は MRFP の SDP と `P-Early-Media: sendonly` を付けた 183 を、高信頼の暫定応答で返します。MRFC が BYE で音声ガイダンスを終えると、`Reason: SIP;cause=603` を付けた 603 が続きます。
- 603 は `sip.Status-Code == 603` で探します。BAOC のときは C2 がありません。ホーム網の国番号は `deploy/compose/mmtel.yaml` にあります。

> 手順書 [「規制」](../../../docs/guide/ja/mmtel.md#規制)。TS 24.611 4.5.2.6.1、4.6.7、4.5.2.4.1、4.9.1.4。RFC 5079。

---

## CW と HOLD

```ims-scene
id: cw-hold
view: ladder
nodes: [ue2, pcscf, scscf, as2]
lead: 割込通話は UE が行い、AS は発信者への知らせ方を決めるだけです。保留の re-INVITE も、AS はもう一方の側にそのまま渡します。
steps:
  - id: waiting
    title: UE 2 が 2 本目の着信を知らせる
    show: [cw-180-ue2-p, cw-180-p-s, cw-180-s-as2]
    active: cw-180-ue2-p
    checkpoint: true
    text: IR.92 は割込通話を網ではなく UE で行うよう求めています（2.3.4）。通話中の UE 2 は 2 本目の着信に、call-waiting の Alert-Info を付けた 180 を返します。
  - id: cw
    title: AS が Alert-Info を取り除く
    show: [as2-cw, cw-180-as2-s]
    active: as2-cw
    detail: [as2-cw, cw-180-as2-s]
    checkpoint: true
    text: AS は 180 をそのまま渡すか、事業者の設定で Alert-Info を取り除きます。この網の文書は発信者に知らせない設定です。
  - id: hold
    title: UE 2 が 1 本目を保留にする
    show: [hold-ue2-p, hold-p-s, hold-s-as2]
    active: hold-ue2-p
    text: "`ue serve` は `POST /answer` を受けると、1 本目を保留にしてから待っている着信に出ます。保留は `a=sendonly` の re-INVITE です。"
  - id: pass
    title: AS が re-INVITE を渡す
    show: [as2-hold, hold-as2-s]
    active: as2-hold
    detail: [as2-hold, hold-as2-s]
    text: AS は保留の re-INVITE を、届いたままもう一方の側に渡します。
```

- IR.92 は、割込通話を網ではなく UE で行うよう求めています（2.3.4）。
  - 着信者の UE が `Alert-Info: <urn:alert:service:call-waiting>` の 180 を送ります。
  - AS はそれを渡すか、事業者の設定で Alert-Info を取り除きます。
- HOLD は `a=sendonly` の re-INVITE です。AS はもう一方の側にそのまま渡します。
- `ue serve` は、通話中に 2 本目の着信を受けられます。その Alert-Info で呼び出し、`POST /answer` で 1 本目を保留にして、待っている着信に出ます。CW は `scripts/e2e-mrf.sh` がキャプチャで見せます。1 つのプロセスの試験でも確かめています。

```sh
go test -run 'TestCommunicationWaiting|TestHoldThroughTheIMS' -v ./internal/mmtel
go test -run TestServeTwoCalls -v ./internal/ue
```

- `ue serve` は 3 本目の着信に 486 で答えます。ラボの **Hold** のボタンも HOLD を送ります。

> 手順書 [「CW と HOLD」](../../../docs/guide/ja/mmtel.md#cw-と-hold)。TS 24.615 4.5.5.3.2、4.5.5.2.3。RFC 7462。TS 24.610 4.5.2.1。

---

## IR.92 が求めること

| IR.92 | 内容 | このプロジェクト |
| --- | --- | --- |
| 2.3 | VoLTE の網が提供する付加サービス | OIP、OIR、TIP、TIR、CDIV、CB、HOLD、CW |
| 2.3.2 | Ut の XCAP で設定する。1 回に規則 1 つ。If-Match | `ue xcap` |
| 2.3.2 Note 3 | 事業者が最初の文書に規則を入れておく | hss.yaml の止めた規則 |
| 2.3.4 | CW は UE で行う | AS は Alert-Info を渡す |
| 2.3.5、2.3.11 | MWI と ECT は網の対応が任意 | 作らない |
| 2.2.2 | GBA は推奨 | 作らない。Digest |

- このプロジェクトは、3GPP の共通の部分に従います。国ごとの違い（日本では TTC）は網どうしの接続にかかわるので、接続する相手の網ごとのプロファイルに置きます。網間接続の手順書で扱います。

> 手順書 [「MMTel と、AS が要る理由」](../../../docs/guide/ja/mmtel.md#mmtel-とas-が要る理由) と [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/mmtel.md#このプロジェクトで簡略化していること)

---

## 簡略化していること

| 場所 | 主なもの |
| --- | --- |
| AS | 181 で音声ガイダンスを流さない。設定を購読せず、通話ごとに読む。BIC-Roam は成り立たない。 |
| XCAP | GBA が無い。キャプチャで XML を読めるように平文の HTTP。nonce count の繰り返しに気づかない。xcap-diff の購読が無い。 |
| simservs | 一部の条件と serv-cap が無い。国は国番号で決め、国番号 1 の国々は 1 つになる。 |
| CSCF と HSS | History-Info の非通知を掛けない。公開サービス識別子と ORIGINATING_UNREGISTERED が無い。 |

```sh
grep -rn "SIMPLIFIED:" internal/mmtel internal/xcap internal/simservs internal/config/xcap.go cmd/ue/xcap.go
```

- 出力を手順書の表と 1 つずつ突き合わせます。CSCF と HSS の項目は `internal/scscf`、`internal/icscf`、`internal/hss` にあります。
- nonce count を確かめないのは、複数のインスタンスで動かすための代わりです。数を覚えるには、どのインスタンスも共有する Store が要ります。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/mmtel.md#このプロジェクトで簡略化していること)。項目ごとの仕様の条項と理由は手順書の表にあります。

---

## ラボで設定する

```sh
docker compose --profile phones up -d --build
```

1. `phone2` のカードで **Supplementary services (Ut/XCAP)** を開きます。どのサービスも最初は `off` です。
2. **CFU (unconditional)** を選び、転送先を書いて **On** を押します。
3. 一覧に `CFU (unconditional): on to …` と出て、ETag が 1 つ増えます。
4. **Off** で止めます。転送先は消えずに残ります。

- ラボのシーケンス図には XCAP も Sh も出ません。どちらも SIP の要求には属さないからです。キャプチャには、80 番の HTTP と 3868 番の Diameter として残ります。
- ラボは 1 つの通話のダイアログをすべて 1 つのシーケンス図に描き、MMTel AS の列も置きます。`phones` と `ue` のプロファイルを同時に動かさないでください。同じ加入者の UE が 2 台になります。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する)

---

## コードで確かめる

| 見たもの | 開く場所 |
| --- | --- |
| B2BUA、通話ごとの 2 つの側 | `internal/mmtel` の `mmtel.go`、`call.go` |
| サービスの順番 | `internal/mmtel/sessioncase.go` |
| 識別子、転送、規制、CW | `internal/mmtel` の `identity.go`、`diversion.go`、`barring.go`、`waiting.go` |
| 文書と規則の評価 | `internal/simservs` |
| Ut の XCAP サーバーと Digest | `internal/xcap` |
| UE の `ue xcap` | `internal/ue/xcap.go`、`cmd/ue/xcap.go` |
| P-Served-User、ODI、iFC | `internal/scscf/services.go`、`internal/ifc` |
| HSS の設定と Ut のパスワード | `internal/hss` の `servicedata.go`、`sh.go`、`deploy/compose/hss.yaml` |

- `internal/mmtel/network_test.go` は、すべてのサービスを IMS 全体を通して 1 つのプロセスで動かします。`internal/mmtel/diversion.go` で cause の値を探し、キャプチャの `cause=302` と照らし合わせます。

> 手順書 [「コードの場所」](../../../docs/guide/ja/mmtel.md#コードの場所)

---

[layout: exercise]

## 演習

1. CFNR を設定し、`noreply 5` を `noreply 10` に変えて発信します。180 から CANCEL までの時間を測ります。
2. 転送した通話の icid-value で絞り込み、ダイアログの数を数えます。
3. `boic on` にして、UE 1 から UE 2 に発信します。通話は規制されるでしょうか。
4. `hss.yaml` で、発信側の条件の DefaultHandling だけが SESSION_TERMINATED なのはなぜかを説明します。
5. XCAP の 2 回目の GET の `Authorization` を読み、どちらのアルゴリズムで答えたかを確かめます。

1 と 3 では、「実行する」のスライドの形で、UE 1 か UE 2 として `ue xcap` を実行します。

---

[layout: answers]

## 演習の答え

1. 10 秒ほどです。タイマーは最初の 180 で始まります。
2. C1 から C6 までの 6 つです。
3. 規制されません。UE 2 の番号の国番号は 1 で、ホーム網と同じだからです。
4. 規制を確実に効かせるためです。AS が答えないときに AS を通さず通話を続けると、規制をすり抜けてしまいます。
5. SHA-256 です。最初のチャレンジです。

---

[layout: exercise]

## 振り返り

1. S-CSCF は、INVITE を MMTel AS に送ることを、どうやって知りますか。
2. AS は、その INVITE が発信側のものか着信側のものかを、何で知りますか。
3. 1 回の通話に Call-ID が 3 つあるのは、なぜですか。
4. AS が利用者の設定の写しを持たないのは、なぜですか。
5. 2 人が同時に設定を変えたとき、どちらかの PUT が 412 になるのは、なぜですか。
6. CFU と CFNR で、UE 2 の電話が鳴るのはどちらですか。

---

[layout: answers]

## 振り返りの答え

1. 登録のときの SAA で届いた iFC のうち、優先度 10 から 13 の条件に INVITE が一致するからです。
2. P-Served-User の `sescase` と `orig-cdiv` で知ります。
3. 利用者ごとに AS が経路にいて、B2BUA の AS が新しいダイアログを作るからです。
4. どのインスタンスでもどの通話でも受けられるようにし、AS を複数台で動かせるようにするためです。設定は HSS にだけあります。
5. PUT は、読んだ版の ETag を If-Match に入れて送ります。先に書いた人が Sequence Number を進めたので、版が合いません。AS が If-Match で気づくか、間に合わなければ HSS が PUR を断ります。
6. CFNR です。CFU は INVITE が届いた時点で転送するので、UE 2 は鳴りません。

---

## 次の回

- 次は MRF の回です。通話を断る前の音声ガイダンス、着信側が選ぶ呼び出し音、2 つ目の着信、3 人の会議を扱います。どれを頼むのもこの回の AS で、会議ではフォーカスにもなります。
- その次が VoLTE の回です。VoLTE の通話も、この回の MMTel AS を通ります。

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```

- このコマンドは `ue` と `phones` の両方のプロファイルを止め、イメージも消します。手順書の「後片付け」はすべてのプロファイルを止め、イメージを残します。次の回をビルドし直さずに始めるなら、そちらを使います。

> 手順書の一覧は [docs/guide/](../../../docs/guide/) にあります。
