---
title: 音声通話の流れ
footer: ims-practice 勉強会 5. 通話
lang: ja
format: visual
---

```ims-model
nodes:
  - {id: ue1, label: UE 1, role: 発信者, at: [0, 0]}
  - {id: agw, label: IMS-AGW, role: メディア中継ノード, at: [0, 1]}
  - {id: ue2, label: UE 2, role: 着信者, at: [0, 2]}
  - {id: pcscf, label: P-CSCF, role: 両方の UE を担当, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: ホーム網の入口, at: [2, 0]}
  - {id: hss, label: HSS, role: 加入者のデータ, at: [4, 0]}
  - {id: scscf, label: S-CSCF, role: 発信側と着信側を兼ねる, at: [3, 1]}
  - {id: mmtel, label: MMTel AS, role: 付加サービス, at: [4, 1]}
  - {id: dns, label: DNS, role: ENUM のレコード, at: [3, 2]}
events:
  # 発信側（手順 1 から 3）
  - id: inv-ue1-p
    msg: INVITE
    from: ue1
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`tel:+1-202-555-0102` 番号だけで、居場所は知らない"
      - Route: "P-CSCF と、登録で受け取った Service-Route（`sip:orig@scscf...`）"
      - Supported: "`100rel`、`timer`、`199`、`precondition`"
      - Session-Expires: 1800。UE 1 が UPDATE でセッションを更新する
      - SDP のオファー: UE 1 のアドレスとポート。PCMU と telephone-event
      - "`a=curr` と `a=des`": プリコンディション。UE 1 の資源はもうある
  - id: p-orig
    do: 発信側だと判断する
    at: pcscf
    fields:
      - 一番上の Route: "Path のユーザー部 `term` が無い"
      - 残りの Route: Service-Route と一致するかを確かめる
  - id: ctx1-post
    msg: POST /contexts
    from: pcscf
    to: agw
    proto: HTTP
    note: (コンテキスト 1)
    fields:
      - ターミネーション: アクセス側（UE 1 へ）とコア側（網へ）
      - アクセス側の送り先: UE 1 の SDP のアドレス
  - id: ctx1-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (コンテキスト 1)
    fields:
      - 返るもの: コンテキストの ID と、2 つのターミネーションのアドレス
  - id: p-rewrite
    do: ヘッダを足し、SDP を書き換える
    at: pcscf
    fields:
      - P-Asserted-Identity: UE 1 の既定の公開の識別子
      - Record-Route: P-CSCF。あとの要求がここを通る
      - "SDP の `c=` と `m=`": コンテキスト 1 のコア側のターミネーション
  - id: inv-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    fields:
      - Route: "Service-Route の `sip:orig@scscf...`"
      - P-Asserted-Identity: UE 1 の既定の公開の識別子
      - "SDP の `c=` と `m=`": 中継ノードの 172.28.0.50
  - id: s-orig
    do: 発信者を担当すると判断する
    at: scscf
    fields:
      - 一番上の Route: "`orig` がある"
      - 担当する利用者: P-Asserted-Identity の利用者。登録済みかを確かめる
  - id: s-assert
    do: 識別子とサービスを書く
    at: scscf
    fields:
      - P-Asserted-Identity: 2 つ目として、UE 1 の tel URI が加わる
      - P-Asserted-Service: MMTel の ICSI。P-Preferred-Service は消える
  - id: inv-s-as1
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (発信側)
    fields:
      - Route: "先頭に AS、次に `odi` 付きの S-CSCF"
      - P-Served-User: "`sescase=orig`"
  - id: udr1
    msg: UDR
    from: mmtel
    to: hss
    proto: Sh
    note: (UE 1)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: MMTEL-Services
  - id: uda1
    msg: UDA
    from: hss
    to: mmtel
    proto: Sh
    note: (UE 1)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - 中身: UE 1 の MMTelServices。既定の設定では何も変えない
  - id: inv-as-s1
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (発信側)
    fields:
      - Call-ID: AS が新しく作ったもの
      - Route: "`odi` 付きの S-CSCF"
  - id: enum-q
    msg: NAPTR
    from: scscf
    to: dns
    proto: DNS
    note: (ENUM)
    fields:
      - 問い合わせる名前: "`2.0.1.0.5.5.5.2.0.2.1.e164.arpa` 番号を逆順にしたもの"
  - id: enum-a
    msg: NAPTR
    from: dns
    to: scscf
    proto: DNS
    note: (ENUM の答え)
    fields:
      - サービス: "`E2U+sip`"
      - 正規表現: "`sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org` を作る"
  - id: s-uri
    do: Request-URI を SIP URI にする
    at: scscf
    fields:
      - 前: "`tel:+1-202-555-0102`"
      - 後: "`sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`"
  # 網の入口（手順 4 と 5）
  - id: loc-q
    msg: NAPTR, SRV
    from: scscf
    to: dns
    proto: DNS
    note: (I-CSCF を探す)
    fields:
      - 問い合わせるドメイン: "`ims.mnc001.mcc001.3gppnetwork.org`"
  - id: loc-a
    msg: NAPTR, SRV
    from: dns
    to: scscf
    proto: DNS
    note: (答え)
    fields:
      - 見つかるもの: 登録のときと同じく、ホーム網の入口の I-CSCF
  - id: inv-s-i
    msg: INVITE
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - Request-URI: "`sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`"
      - Via: "`SIP/2.0/TCP` INVITE が 1300 バイトを超えた"
  - id: lir
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    fields:
      - Public-Identity: "`sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`"
  - id: lia
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Server-Name: UE 2 が登録している S-CSCF
  - id: inv-i-s
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    fields:
      - Route: 先頭に、LIA の Server-Name の S-CSCF
      - Record-Route: I-CSCF は付けない
  # 着信側（手順 6 と 7）
  - id: s-term
    do: 着信者を担当すると判断する
    at: scscf
    fields:
      - 一番上の Route: "`orig` が無い"
      - 見つけるもの: UE 2 の登録
      - P-Asserted-Service: そのまま残す
  - id: inv-s-as2
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (着信側)
    fields:
      - P-Served-User: "`sescase=term`"
  - id: udr2
    msg: UDR
    from: mmtel
    to: hss
    proto: Sh
    note: (UE 2)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: MMTEL-Services
  - id: uda2
    msg: UDA
    from: hss
    to: mmtel
    proto: Sh
    note: (UE 2)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - 中身: UE 2 の MMTelServices
  - id: inv-as-s2
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (着信側)
    fields:
      - Call-ID: AS が新しく作ったもの
  - id: s-path
    do: UE 2 への経路を決める
    at: scscf
    fields:
      - Record-Route: S-CSCF
      - P-Called-Party-ID: 発信された宛先
      - Request-URI: UE 2 が登録した Contact
      - Route: UE 2 の REGISTER にあった Path
  - id: inv-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: UE 2 が登録した Contact
      - Route: "UE 2 の Path（`sip:term@pcscf...`）"
  - id: p-term
    do: 着信側だと判断する
    at: pcscf
    fields:
      - 一番上の Route: "自分の Path の URI。ユーザー部が `term`"
  - id: ctx2-post
    msg: POST /contexts
    from: pcscf
    to: agw
    proto: HTTP
    note: (コンテキスト 2)
    fields:
      - ターミネーション: アクセス側（UE 2 へ）とコア側（網へ）
      - コア側の送り先: オファーのアドレス。コンテキスト 1 のコア側
  - id: ctx2-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (コンテキスト 2)
    fields:
      - 返るもの: コンテキストの ID と、2 つのターミネーションのアドレス
  - id: inv-p-ue2
    msg: INVITE
    from: pcscf
    to: ue2
    proto: SIP
    fields:
      - 送り先: UE 2 の REGISTER が来たアドレス。Contact ではない
      - "SDP の `c=` と `m=`": コンテキスト 2 のアクセス側のターミネーション
  # 183（手順 8）
  - id: r183-ue2-p
    msg: "183"
    from: ue2
    to: pcscf
    proto: SIP
    fields:
      - SDP のアンサー: PCMU と、UE 2 のアドレスとポート
      - Require: "`100rel` と `precondition`"
      - RSeq: 信頼性のある暫定応答の番号
      - プリコンディション: "両端とも資源がある。`a=conf` は無い"
      - To のタグ、Contact、Record-Route: 初期ダイアログができる
  - id: mod2-put
    msg: PUT .../terminations/access
    from: pcscf
    to: agw
    proto: HTTP
    note: (コンテキスト 2)
    fields:
      - 送り先: UE 2 のアンサーのアドレス
  - id: mod2-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (コンテキスト 2)
  - id: r183-p-s3
    msg: "183"
    from: pcscf
    to: scscf
    proto: SIP
    note: (ダイアログ 3)
    fields:
      - "SDP の `c=` と `m=`": コンテキスト 2 のコア側のターミネーション
  - id: r183-s-as3
    msg: "183"
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 3)
  - id: r183-as-s2
    msg: "183"
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 2)
  - id: r183-s-i
    msg: "183"
    from: scscf
    to: icscf
    proto: SIP
    note: (ダイアログ 2)
  - id: r183-i-s
    msg: "183"
    from: icscf
    to: scscf
    proto: SIP
    note: (ダイアログ 2)
  - id: r183-s-as2
    msg: "183"
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 2)
  - id: r183-as-s1
    msg: "183"
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 1)
  - id: r183-s-p1
    msg: "183"
    from: scscf
    to: pcscf
    proto: SIP
    note: (ダイアログ 1)
  - id: mod1-put
    msg: PUT .../terminations/core
    from: pcscf
    to: agw
    proto: HTTP
    note: (コンテキスト 1)
    fields:
      - 送り先: アンサーのアドレス。コンテキスト 2 のコア側
  - id: mod1-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (コンテキスト 1)
  - id: r183-p-ue1
    msg: "183"
    from: pcscf
    to: ue1
    proto: SIP
    note: (ダイアログ 1)
    fields:
      - "SDP の `c=` と `m=`": コンテキスト 1 のアクセス側のターミネーション
      - 書き換え: P-CSCF が 2 回書き換えている
  # PRACK（手順 9）
  - id: prack-ue1-p
    msg: PRACK
    from: ue1
    to: pcscf
    proto: SIP
    note: (ダイアログ 1)
    fields:
      - RAck: 183 の RSeq と、INVITE の CSeq
      - Route: Record-Route から作ったルートセット
  - id: prack-p-s1
    msg: PRACK
    from: pcscf
    to: scscf
    proto: SIP
    note: (ダイアログ 1)
  - id: prack-s-as1
    msg: PRACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 1)
  - id: pok-as-s1
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (PRACK)
  - id: pok-s-p1
    msg: 200 OK
    from: scscf
    to: pcscf
    proto: SIP
    note: (PRACK)
  - id: pok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (PRACK)
  - id: prack-as-s2
    msg: PRACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 2)
  - id: prack-s-as2
    msg: PRACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 2)
  - id: pok-as-s2
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (PRACK)
  - id: pok-s-as2
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (PRACK)
  - id: prack-as-s3
    msg: PRACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 3)
  - id: prack-s-p3
    msg: PRACK
    from: scscf
    to: pcscf
    proto: SIP
    note: (ダイアログ 3)
  - id: prack-p-ue2
    msg: PRACK
    from: pcscf
    to: ue2
    proto: SIP
    note: (ダイアログ 3)
  - id: pok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (PRACK)
  - id: pok-p-s3
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (PRACK)
  - id: pok-s-as3
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (PRACK)
  # 180、200 OK、ACK（手順 10 と 11）
  - id: ue2-ring
    do: 利用者を呼び出す
    at: ue2
    fields:
      - 条件: 必須のプリコンディションがすべて満たされた
      - この構成: どちらの UE も資源があるので、183 のすぐあと
  - id: r180-ue2-p
    msg: "180"
    from: ue2
    to: pcscf
    proto: SIP
    fields:
      - Require: "`100rel` 信頼性のある暫定応答。これにも PRACK が返る"
  - id: ok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (INVITE)
    fields:
      - SDP: 無い。アンサーは信頼性のある 183 で届いている
      - 送り直し: ACK が届くまで、間隔を広げながら送り直す
  - id: ok-p-s3
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-as3
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (INVITE)
  - id: ack-as-s3
    msg: ACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 3)
  - id: ack-s-p3
    msg: ACK
    from: scscf
    to: pcscf
    proto: SIP
    note: (ダイアログ 3)
  - id: ack-p-ue2
    msg: ACK
    from: pcscf
    to: ue2
    proto: SIP
    note: (ダイアログ 3)
  - id: ok-as-s2
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (INVITE)
  - id: ok-i-s
    msg: 200 OK
    from: icscf
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-as2
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (INVITE)
  - id: ack-as-s2
    msg: ACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 2)
  - id: ack-s-as2
    msg: ACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 2)
  - id: ok-as-s1
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-p1
    msg: 200 OK
    from: scscf
    to: pcscf
    proto: SIP
    note: (INVITE)
  - id: ok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (INVITE)
    fields:
      - 意味: ダイアログが確定する
  - id: ack-ue1-p
    msg: ACK
    from: ue1
    to: pcscf
    proto: SIP
    note: (ダイアログ 1)
    fields:
      - 経路: ルートセットに沿って進む
      - トランザクション: INVITE のトランザクションの一部ではない
  - id: ack-p-s1
    msg: ACK
    from: pcscf
    to: scscf
    proto: SIP
    note: (ダイアログ 1)
  - id: ack-s-as1
    msg: ACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 1)
  # 音声（手順 12）
  - id: rtp-ue1-agw
    msg: RTP
    from: ue1
    to: agw
    proto: RTP
    fields:
      - 間隔と大きさ: 20 ms ごと。G.711 μ-law が 160 バイト
      - 宛先: コンテキスト 1 のアクセス側のターミネーション
      - ポート: 受信と同じポートから送る。対称 RTP
  - id: agw-relay
    do: 2 つのコンテキストを通す
    at: agw
    fields:
      - 経路: "アクセス（1）、コア（1）、コア（2）、アクセス（2）"
      - コンテキスト 1: 発信側を通る P-CSCF が作った
      - コンテキスト 2: 着信側を通る P-CSCF が作った
  - id: rtp-agw-ue2
    msg: RTP
    from: agw
    to: ue2
    proto: RTP
    fields:
      - 送信元: コンテキスト 2 のアクセス側のターミネーション
  - id: rtp-ue2-agw
    msg: RTP
    from: ue2
    to: agw
    proto: RTP
  - id: agw-latch
    do: 送り先を決める（ラッチング）
    at: agw
    fields:
      - UE 側: 最初に受け取ったパケットの送信元へ送る
      - 網の側: 最新の SDP のアドレスへ送る
  - id: rtp-agw-ue1
    msg: RTP
    from: agw
    to: ue1
    proto: RTP
  - id: rtcp-ue1-agw
    msg: RTCP
    from: ue1
    to: agw
    proto: RTCP
    fields:
      - ポート: RTP の 1 つ上のポート
      - 間隔: 5 秒に 1 回以上
      - 中身: 送信の報告（何も送っていない間は受信の報告）と、CNAME の入った SDES
  # 切断（手順 13）
  - id: bye-ue1-p
    msg: BYE
    from: ue1
    to: pcscf
    proto: SIP
    note: (ダイアログ 1)
    fields:
      - Reason: '`RELEASE_CAUSE;cause=1;text="User ends call"`'
      - 経路: ルートセットに沿って進む
  - id: bye-p-s1
    msg: BYE
    from: pcscf
    to: scscf
    proto: SIP
    note: (ダイアログ 1)
  - id: bye-s-as1
    msg: BYE
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 1)
  - id: bok-as-s1
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (BYE)
  - id: bok-s-p1
    msg: 200 OK
    from: scscf
    to: pcscf
    proto: SIP
    note: (BYE)
  - id: bok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (BYE)
  - id: del1
    msg: "DELETE /contexts/{id}"
    from: pcscf
    to: agw
    proto: HTTP
    note: (コンテキスト 1)
  - id: del1-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (コンテキスト 1)
  - id: bye-as-s2
    msg: BYE
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 2)
  - id: bye-s-as2
    msg: BYE
    from: scscf
    to: mmtel
    proto: SIP
    note: (ダイアログ 2)
  - id: bok-as-s2
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (BYE)
  - id: bok-s-as2
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (BYE)
  - id: bye-as-s3
    msg: BYE
    from: mmtel
    to: scscf
    proto: SIP
    note: (ダイアログ 3)
  - id: bye-s-p3
    msg: BYE
    from: scscf
    to: pcscf
    proto: SIP
    note: (ダイアログ 3)
  - id: bye-p-ue2
    msg: BYE
    from: pcscf
    to: ue2
    proto: SIP
    note: (ダイアログ 3)
  - id: bok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (BYE)
  - id: bok-p-s3
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (BYE)
  - id: bok-s-as3
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (BYE)
  - id: del2
    msg: "DELETE /contexts/{id}"
    from: pcscf
    to: agw
    proto: HTTP
    note: (コンテキスト 2)
  - id: del2-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (コンテキスト 2)
  # 失敗
  - id: r486-ue2-p
    msg: "486"
    from: ue2
    to: pcscf
    proto: SIP
    fields:
      - 送信元: UE 2。この応答を作ったノード
  - id: lir-u
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (未登録)
    fields:
      - Public-Identity: "`sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org`"
  - id: lia-u
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (未登録)
    fields:
      - Experimental-Result: DIAMETER_UNREGISTERED_SERVICE (2003)
      - Server-Capabilities: S-CSCF を選ぶための能力
  - id: inv-i-s-u
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    note: (未登録)
    fields:
      - 送り先: 初回の登録と同じように I-CSCF が選んだ S-CSCF
  - id: sar-u
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    note: (未登録)
    fields:
      - Server-Assignment-Type: UNREGISTERED_USER (3)
  - id: saa-u
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    note: (未登録)
    fields:
      - User-Data: "プロファイル。TERMINATING_UNREGISTERED で MMTel AS を呼ぶ iFC"
  - id: inv-s-as-u
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (未登録)
  - id: inv-as-s-u
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (未登録)
    fields:
      - 中身: 着信転送が無いので、そのまま送り返す
  - id: s-480
    do: 送る先の登録が無い
    at: scscf
    fields:
      - 結果: どのサービスも通話を受けなかったので 480
  - id: r480-s-as
    msg: "480"
    from: scscf
    to: mmtel
    proto: SIP
    fields:
      - 送信元: S-CSCF。AS はこれを自分のダイアログに写す
  - id: enum-q-x
    msg: NAPTR
    from: scscf
    to: dns
    proto: DNS
    note: (ENUM)
    fields:
      - 問い合わせる名前: "`8.9.1.0.5.5.5.2.0.2.1.e164.arpa`"
  - id: enum-a-x
    msg: NAPTR
    from: dns
    to: scscf
    proto: DNS
    note: (ENUM の答え)
    fields:
      - 答え: レコードが無い
  - id: s-404
    do: SIP URI に変えられない
    at: scscf
    fields:
      - 結果: BGCF が無いので、S-CSCF が 404 で答える
  - id: r404-s-as
    msg: "404"
    from: scscf
    to: mmtel
    proto: SIP
    fields:
      - 送信元: S-CSCF。AS の新しい INVITE への応答
```

---

# 音声通話の流れ

ims-practice 勉強会 第 5 回（中継ノードを通る通話と、その途中のプリコンディションと MMTel AS）

---

[layout: summary]

## この回で分かること

最初の問い。UE 1 は UE 2 の IP アドレスを知っているでしょうか。知りません。知っているのは電話番号だけです。

- 電話番号が SIP のアドレスに変わるしくみ。
- 各 CSCF が、次の CSCF を見つける方法。
- 2 台の UE が、メディアを合意する方法。
- 音声が、メディア中継ノードを通って流れる様子。

> 手順書 [音声通話の流れを追う](../../../docs/guide/ja/call.md)（`docs/guide/ja/call.md`）
> 仕様の版は Release 18 です。TS 24.229 V18.11.0、TS 29.228 V18.0.0 と、各手順で挙げる RFC。
> トランザクション、ダイアログ、SDP、PRACK、プリコンディションは [用語集](../../../docs/glossary.md) にあります。

---

## 前の回から引き継ぐもの

通話は、登録で決まった次の 3 つを使います。

| 登録で決まったもの | 持っているノード | 通話で使う場面 |
| --- | --- | --- |
| Service-Route | UE | UE の要求を、自分の S-CSCF へ届ける。 |
| Path | S-CSCF | UE への要求を、その UE の P-CSCF へ届ける。 |
| IP の対応付け | P-CSCF | 要求が、登録した UE から来たことを確かめる。 |

- 通話の前に、2 台の UE が前の回のとおりに登録します。キャプチャの最初にその REGISTER があります。

> 手順書 [登録の「10. S-CSCF が登録を受け付ける（200 OK）」](../../../docs/guide/ja/registration.md#10-s-cscf-が登録を受け付ける200-ok)

---

## 登場するノード

```ims-scene
id: nodes
lead: 1 台のノードが発信側と着信側の 2 つの立場で動きます。これがこの回のキャプチャを読む鍵です。
steps:
  - id: ues
    title: UE 1（172.28.0.100）と UE 2（172.28.0.101）
    focus: [ue1, ue2]
    text: |
      UE 1 は発信者で `+1-202-555-0101` です。UE 2 は着信者で `+1-202-555-0102` です。
      UE 1 は UE 2 のアドレスを知りません。知っているのは番号だけです。
  - id: pcscf
    title: P-CSCF（172.28.0.40）
    focus: [pcscf]
    text: 両方の UE を担当します。1 回の通話で 2 回通ります。1 回目は発信側、2 回目は着信側です。
  - id: scscf
    title: S-CSCF（172.28.0.30）
    focus: [scscf]
    checkpoint: true
    text: これも両方の UE を担当するので、発信側と着信側の S-CSCF を兼ねます。
  - id: icscf
    title: I-CSCF（172.28.0.20）と HSS（172.28.0.10）
    focus: [icscf, hss]
    text: I-CSCF は着信者のホーム網の入口です。着信者の所在を HSS に尋ねます。
  - id: mmtel
    title: MMTel AS（172.28.0.75）
    focus: [mmtel]
    text: |
      発信者と着信者の INVITE を、それぞれ受けて送り返します。
      ここでは何も変えません。AS が何をするかは付加サービスの回で見ます。
  - id: dns
    title: DNS（172.28.0.2）
    focus: [dns]
    checkpoint: true
    text: 番号を SIP URI に変える ENUM のレコードを持ちます。
  - id: agw
    title: IMS-AGW（172.28.0.50）
    focus: [agw]
    text: |
      メディア中継ノードです。RTP を中継し、P-CSCF が制御します。
      UE どうしは直接やり取りしません。
```

| ノード | アドレス | 通話での役割 |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | 発信者。`+1-202-555-0101`。 |
| UE 2 | 172.28.0.101 | 着信者。`+1-202-555-0102`。 |
| P-CSCF | 172.28.0.40 | 両方の UE を担当する。1 回の通話で 2 回通る。 |
| S-CSCF | 172.28.0.30 | 発信側と着信側の S-CSCF を兼ねる。 |
| I-CSCF | 172.28.0.20 | 着信者のホーム網の入口。HSS に所在を尋ねる。 |
| DNS | 172.28.0.2 | 番号を SIP URI に変える ENUM のレコードを持つ。 |
| メディア中継ノード | 172.28.0.50 | RTP を中継する。P-CSCF が制御する。 |
| MMTel AS | 172.28.0.75 | 発信者と着信者の INVITE を、それぞれ受けて送り返す。 |

- 1 台のノードが、発信側と着信側の 2 つの立場で動きます。これがこの回のキャプチャを読む鍵です。実際の網では、2 つの側は別のノードで、別の網にあることも多いです。
- ここでの MMTel AS は、INVITE を何も変えずに送り返します。AS が何をするかは付加サービスの回で見ます。
- HSS（172.28.0.10）は、I-CSCF の所在の問い合わせに Cx で、MMTel AS のサービスの設定の問い合わせに Sh で答えます。

> 手順書 [「登場するノード」](../../../docs/guide/ja/call.md#登場するノード)

---

## 実行する

```sh
docker compose up -d --build

# 1 つ目の端末。UE 2 が着信を待つ。
docker compose run --rm ue2 answer \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2 -wav-out /captures/ue2-heard.wav

# 2 つ目の端末。UE 1 が番号で発信し、3 秒のトーンを流して切る。
docker compose run --rm ue call -tone 3s tel:+1-202-555-0102
```

- `captures/ue2-heard.wav` に、UE 2 が聞いた音声が残ります。
- 番号は 555-0100 から 555-0199 の範囲です。北米の番号計画（NANP）が架空の用途に取ってある番号です。

> 手順書 [「発信する」](../../../docs/guide/ja/call.md#発信する)

---

## キャプチャを開く

```text
sip || diameter.cmd.code == 302 || dns.qry.type == 35 || rtp || rtcp
```

- **Telephony > VoIP Calls** に通話が出ます。
  - **Flow Sequence** でシーケンス図を見ます。
  - **Play Streams** で両方向の音声を聞きます。
- RTP がただの UDP と表示されるときは、**Analyze > Enabled Protocols** で **rtp_udp** を有効にします。
- 手順書のシーケンス図の番号 1 から 13 が、次からのスライドの番号です。

> 手順書 [「キャプチャを開く」](../../../docs/guide/ja/call.md#キャプチャを開く) と [「シーケンス」](../../../docs/guide/ja/call.md#シーケンス)。シーケンス図の全体は手順書にあります。

---

## 全体の形

```ims-scene
id: overview
view: ladder
nodes: [ue1, pcscf, scscf, mmtel, icscf, hss, ue2]
lead: INVITE は S-CSCF を 2 回通り、そのたびに MMTel AS へ寄ります。
steps:
  - id: orig
    title: INVITE が発信者の S-CSCF に届く（1、2）
    show: [inv-ue1-p, inv-p-s]
    detail: []
    text: UE 1 は P-CSCF に INVITE を送り、P-CSCF は Service-Route の S-CSCF へ送ります。
  - id: as-orig
    title: 発信側の AS が新しい INVITE を送り返す（3）
    show: [inv-s-as1, inv-as-s1]
    active: inv-s-as1
    detail: []
    text: S-CSCF は iFC に従って INVITE を AS に送ります。AS は自分の Call-ID で新しい INVITE を送り返します。
  - id: entry
    title: ホーム網の入口で所在を尋ねる（4、5）
    show: [inv-s-i, lir, lia, inv-i-s]
    active: lir
    detail: []
    checkpoint: true
    text: |
      S-CSCF は番号を SIP URI に変え、I-CSCF に送ります。
      I-CSCF は HSS に着信者の S-CSCF を尋ね、INVITE は同じ S-CSCF に戻ります。
  - id: as-term
    title: 着信側の AS がもう一度送り返す（6）
    show: [inv-s-as2, inv-as-s2]
    active: inv-s-as2
    detail: []
    text: 今度は着信者を担当する立場で、同じ AS に寄ります。
  - id: term
    title: Path に沿って UE 2 に届く（6、7）
    show: [inv-s-p, inv-p-ue2]
    active: inv-p-ue2
    detail: []
    text: |
      AS の新しい INVITE で、1 つの通話にダイアログが 3 つできます。
      UE 1 と発信側の AS（ダイアログ 1）、2 つの AS のあいだ（ダイアログ 2）、着信側の AS と UE 2（ダイアログ 3）です。
```

INVITE は、次の順でノードを通ります。

```ladder
UE 1, P-CSCF, S-CSCF, MMTel AS, I-CSCF, HSS, UE 2
UE 1 -> P-CSCF -> S-CSCF: INVITE (orig) // 1 - 2
S-CSCF -> MMTel AS: INVITE (orig) // 3
S-CSCF <- MMTel AS: 新しい INVITE
S-CSCF -> I-CSCF: INVITE // 4
I-CSCF <-> HSS: LIR, LIA // 5
I-CSCF -> S-CSCF: INVITE (term) // 6
S-CSCF -> MMTel AS: INVITE (term)
S-CSCF <- MMTel AS: 新しい INVITE
S-CSCF -> P-CSCF: INVITE (term) // 6
P-CSCF -> UE 2: INVITE (term) // 7
```

- `orig` は発信側の立場（originating）、`term` は着信側の立場（terminating）です。
- AS はルーティングの B2BUA です（TS 24.229 5.7.5）。AS の新しい INVITE は自分の Call-ID を持つので、1 つの通話にダイアログが 3 つできます。AS がこれを作るしくみは第 7 回で読みます。

---

## 通話の区間

| 区間 | 手順 | 起きること |
| --- | --- | --- |
| 発信側 | 1 から 3 | 発信者を確かめ、AS を通し、番号を SIP URI に変える。 |
| 網の入口 | 4 と 5 | 着信者を担当する S-CSCF を、HSS に尋ねる。 |
| 着信側 | 6 と 7 | AS を通し、Path に沿って着信者の UE に届ける。 |
| 応答 | 8 から 11 | 183、PRACK、180、PRACK、200 OK、ACK。 |
| 音声と切断 | 12 と 13 | RTP が中継ノードを通る。BYE で終わる。 |

- この表がこの回の地図です。新しい区間に入るたびに戻ってきます。

---

## 1 と 2。INVITE と、発信側の P-CSCF

```ims-scene
id: orig-pcscf
lead: P-CSCF を出た INVITE には、UE 1 ではなく中継ノードのアドレスが書いてあります。
steps:
  - id: send
    title: UE 1 が番号に INVITE を送る
    checkpoint: true
    only: [inv-ue1-p]
    text: |
      Request-URI は番号だけです。UE 1 は UE 2 がどこにいるかを知りません。
      Route は 2 つで、P-CSCF と、登録で受け取った Service-Route です。
  - id: orig
    title: P-CSCF が発信側だと判断する
    show: [p-orig]
    focus: [pcscf]
    text: 一番上の Route に、自分の Path にある `term` が無いので、発信側の通過です。
  - id: context
    title: 中継ノードにコンテキストを作らせる
    checkpoint: true
    show: [ctx1-post]
    zoom: [ue1, agw, ue2, pcscf]
    text: |
      UE 1 に向いたアクセス側と、網に向いたコア側のターミネーションを求めます。
      これは SIP ではなく、このプロジェクトの小さな HTTP の API です。
  - id: context-ok
    title: 中継ノードがコンテキストを返す
    show: [ctx1-ok]
    zoom: [ue1, agw, ue2, pcscf]
    text: 中継ノードは 2 つのターミネーションのアドレスを答えます。
  - id: rewrite
    title: P-CSCF がヘッダを足し、SDP を書き換える
    checkpoint: true
    show: [p-rewrite]
    focus: [pcscf]
    text: |
      網が信じるのは From ではなく P-Asserted-Identity です。UE が From に何を書いても関係ありません。
      SDP はコア側のターミネーションを指します。この先の網に UE 1 は見えません。
  - id: forward
    title: INVITE が S-CSCF に届く
    show: [inv-p-s]
    text: Record-Route に P-CSCF が入ったので、この通話のあとの要求も P-CSCF を通ります。
```

| 見るもの | 見どころ |
| --- | --- |
| Request-URI | `tel:+1-202-555-0102`。UE は番号をダイヤルするだけ。 |
| Route | 2 つ。P-CSCF と、登録で受け取った Service-Route。 |
| Supported | `100rel`、`timer`、`199`、`precondition`。 |
| Session-Expires | 1800。UE 1 が UPDATE でセッションを更新する。 |
| SDP のオファー | UE 1 のアドレスとポート。PCMU と telephone-event。 |
| オファーの `a=curr` と `a=des` | プリコンディション。UE 1 の資源はもうある。 |
| P-CSCF が足すヘッダ | P-Asserted-Identity と Record-Route。 |
| P-CSCF を出たあとの SDP | `c=` と `m=` が中継ノードを指す。 |

- 網が信じるのは From ではなく P-Asserted-Identity です。UE が From に何を書いても関係ありません。
- UE 1 には資源を確保するアクセス網が無いので、オファーは資源がもうあると伝えます。`-epc` を付けて起動した UE は VoLTE の UE で、PCMU より先に AMR-WB と AMR を提示します。その通話と、電話が鳴る前に資源を確保する流れは、第 9 回（VoLTE）で追います。
- P-CSCF と中継ノードのやり取りは、このプロジェクト独自の小さな HTTP の API です。キャプチャにも出てきます。この用途の標準のプロトコルは H.248 です。

> 手順書 [1](../../../docs/guide/ja/call.md#1-ue-1-が-invite-を送る) と [2](../../../docs/guide/ja/call.md#2-p-cscf-が発信者の-s-cscf-に送る)。TS 24.229 5.1.3.1、5.1.2A.1.1、6.1.2、5.2.6.2、5.2.6.3.3。RFC 3262 4。RFC 3312。RFC 3264 5.1。

---

## 3。S-CSCF が番号を SIP URI に変える

```ims-scene
id: orig-scscf
lead: S-CSCF は先に MMTel AS を通し、そのあとで番号を DNS の ENUM で SIP URI に変えます。
steps:
  - id: arrive
    title: S-CSCF が発信者を担当すると判断する
    only: [inv-p-s, s-orig]
    focus: [scscf]
    text: Route の先頭に `orig` があるので、S-CSCF は発信者を担当する側として動きます。
  - id: assert
    title: 識別子とサービスを書く
    checkpoint: true
    show: [s-assert]
    focus: [scscf]
    text: UE 1 の tel URI を 2 つ目の P-Asserted-Identity に加え、MMTel の ICSI を P-Asserted-Service に入れます。
  - id: to-as
    title: iFC に従って MMTel AS へ送る
    checkpoint: true
    show: [inv-s-as1]
    zoom: [icscf, hss, scscf, mmtel]
    text: Route の先頭に AS を、次に `odi` を付けた自分を置きます。AS はこの Route で INVITE を送り返します。
  - id: sh
    title: AS が発信者のサービスを読む（UDR と UDA）
    checkpoint: true
    show: [udr1, uda1]
    active: udr1
    detail: [udr1, uda1]
    zoom: [icscf, hss, scscf, mmtel]
    text: AS は利用者のサービスの設定を Sh で HSS から読みます。既定の設定では、通話は何も変わりません。
  - id: back
    title: AS が新しい INVITE を送り返す
    checkpoint: true
    show: [inv-as-s1]
    zoom: [icscf, hss, scscf, mmtel]
    text: S-CSCF はその中に自分の `odi` を見つけ、次の iFC へ進みます。
  - id: enum
    title: S-CSCF が ENUM を引く
    only: [inv-as-s1, enum-q]
    active: enum-q
    zoom: [scscf, dns, mmtel]
    text: 番号の数字を逆に並べ、1 桁ずつラベルにした名前の NAPTR を引きます。
  - id: enum-answer
    title: NAPTR の答えが SIP URI を作る
    checkpoint: true
    show: [enum-a]
    zoom: [scscf, dns, mmtel]
    text: サービスは `E2U+sip` です。正規表現が番号から SIP URI を作ります。
  - id: uri
    title: Request-URI が SIP URI になる
    show: [s-uri]
    focus: [scscf]
    zoom: [scscf, dns, mmtel]
    text: |
      UE 1 の INVITE の Request-URI が、ここで 1 回目に変わります。
      レコードの無い番号なら、S-CSCF は 404 で答えます。
```

| 見るもの | 見どころ |
| --- | --- |
| P-Asserted-Identity | 2 つ目として、UE 1 の tel URI が加わる。 |
| P-Asserted-Service | MMTel の ICSI。P-Preferred-Service は消える。 |
| AS に送る INVITE | Route の先頭に AS、次に `odi` 付きの S-CSCF。`sescase=orig`。 |
| DNS の問い合わせ | `2.0.1.0.5.5.5.2.0.2.1.e164.arpa` の NAPTR。番号を逆順にしたもの。 |
| NAPTR の答え | サービスは `E2U+sip`。正規表現が SIP URI を作る。 |
| S-CSCF を出たあとの Request-URI | `sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`。 |

フィルタは `dns.qry.type == 35` です。レコードは `deploy/compose/dns/e164.arpa.zone` にあります。

- 番号から ENUM のドメインを手で組み立ててみます。数字を逆に並べ、1 桁ずつラベルにします。
- AS は発信者のサービスを Sh で HSS から読みます。既定の設定では何も変えず、新しい INVITE を送り返します。S-CSCF はその中に自分の `odi` を見つけて次の iFC へ進み、そのあとで番号を SIP URI に変えます。
- AS の UDR と UDA は `diameter.cmd.code == 306` で見られます。Sh での読み出しは付加サービスの回で詳しく読みます。

> 手順書 [3](../../../docs/guide/ja/call.md#3-s-cscf-が番号を-sip-uri-に変える)。TS 24.229 5.4.3.1、5.4.3.2、5.7.5。RFC 6050。RFC 6116。

---

## 4 と 5。ホーム網の入口と、LIR

```ims-scene
id: entry
lead: I-CSCF は着信者の S-CSCF を HSS に尋ね、INVITE は同じ S-CSCF に戻ってきます。
steps:
  - id: lookup
    title: S-CSCF が I-CSCF を探す
    only: [loc-q]
    zoom: [icscf, scscf, dns]
    text: 登録のときの P-CSCF と同じく、Request-URI のドメインを NAPTR と SRV で引きます。
  - id: lookup-answer
    title: ホーム網の入口が分かる
    checkpoint: true
    show: [loc-a]
    zoom: [icscf, scscf, dns]
    text: 答えはそのドメインの入口にある I-CSCF のアドレスです。
  - id: forward
    title: INVITE が TCP で I-CSCF に届く
    checkpoint: true
    only: [inv-s-i]
    zoom: [icscf, scscf, dns]
    text: INVITE はノードを通るたびに大きくなり、1300 バイトを超えました。S-CSCF から先の Via は `SIP/2.0/TCP` です。
  - id: lir
    title: I-CSCF が所在を尋ねる（LIR）
    show: [lir]
    zoom: [icscf, hss, scscf]
    text: Cx のコマンド 302 です。尋ねることは登録の UAR と同じで、どの S-CSCF がその利用者を担当しているか、です。
  - id: lia
    title: HSS が S-CSCF の名前を返す（LIA）
    checkpoint: true
    show: [lia]
    zoom: [icscf, hss, scscf]
    text: UE 2 は登録しているので、HSS は UE 2 が登録した S-CSCF の Server-Name を返します。
  - id: back
    title: INVITE が同じ S-CSCF に戻る
    show: [inv-i-s]
    zoom: [icscf, hss, scscf]
    text: |
      I-CSCF は Record-Route を付けません。
      そのため、あとの PRACK、ACK、BYE は I-CSCF を通りません。
```

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF の DNS の問い合わせ | 登録のときと同じ NAPTR と SRV。I-CSCF が見つかる。 |
| S-CSCF から先の Via | `SIP/2.0/TCP`。INVITE が 1300 バイトを超えた。 |
| LIR | 着信者の公開の識別子が入る。 |
| LIA | DIAMETER_SUCCESS と、UE 2 が登録している S-CSCF の Server-Name。 |
| I-CSCF が転送する INVITE | Route の先頭にその S-CSCF。Record-Route は付かない。 |

フィルタは `diameter.cmd.code == 302` です。

- I-CSCF は Record-Route を付けないので、あとの PRACK、ACK、BYE は I-CSCF を通りません。
- LIR が尋ねることは、登録の UAR と同じです。どの S-CSCF がその利用者を担当しているか、です。

> 手順書 [4](../../../docs/guide/ja/call.md#4-s-cscf-が着信者のホーム網を見つける) と [5](../../../docs/guide/ja/call.md#5-i-cscf-が着信者の所在を-hss-に問い合わせるlir-と-lia)。TS 24.229 5.3.2.1。TS 29.228 6.1.4。RFC 3261 18.1.1。

---

## 6 と 7。着信側の S-CSCF と P-CSCF

```ims-scene
id: term
lead: 着信側の S-CSCF は Path に沿って送り、P-CSCF は 2 つ目のコンテキストを作って UE 2 に届けます。
steps:
  - id: arrive
    title: S-CSCF が着信者を担当すると判断する
    only: [inv-i-s, s-term]
    focus: [scscf]
    text: 届いた Route に `orig` が無いので、今度は着信者を担当する側として動きます。
  - id: to-as
    title: 着信側の iFC で MMTel AS へ送る
    checkpoint: true
    show: [inv-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    text: 同じ AS に、今度は `sescase=term` で送ります。
  - id: sh
    title: AS が着信者のサービスを読む
    checkpoint: true
    show: [udr2, uda2]
    active: udr2
    detail: [udr2, uda2]
    zoom: [icscf, hss, scscf, mmtel]
    text: 今度読むのは UE 2 のサービスです。ここでも何も変わりません。
  - id: back
    title: AS が新しい INVITE を送り返す
    checkpoint: true
    show: [inv-as-s2]
    zoom: [icscf, hss, scscf, mmtel]
    text: この INVITE が着信側の AS と UE 2 のダイアログを始めます。
  - id: path
    title: S-CSCF が UE 2 への経路を決める
    only: [inv-as-s2, s-path]
    focus: [scscf]
    text: |
      Request-URI を UE 2 の Contact にし、UE 2 の REGISTER にあった Path を Route に入れます。
      Request-URI が変わるのは、ここで 2 回目です。
  - id: to-pcscf
    title: INVITE が Path の P-CSCF に届く
    checkpoint: true
    show: [inv-s-p]
    text: Path は、UE 2 が登録に使った P-CSCF を指しています。
  - id: p-term
    title: P-CSCF が着信側だと判断する
    checkpoint: true
    only: [inv-s-p, p-term]
    focus: [pcscf]
    text: Route の先頭が自分の Path の URI で、ユーザー部が `term` なので、今回の通過は着信側です。
  - id: context
    title: 2 つ目のコンテキストを作る
    checkpoint: true
    show: [ctx2-post, ctx2-ok]
    active: ctx2-post
    detail: [ctx2-post, ctx2-ok]
    zoom: [ue1, agw, ue2, pcscf]
    text: |
      コア側の送り先はオファーに書いてあるアドレスで、コンテキスト 1 のコア側です。
      P-CSCF はどちらの通過でも、自分の側だけを扱います。
  - id: deliver
    title: INVITE が UE 2 に届く
    show: [inv-p-ue2]
    zoom: [ue1, agw, ue2, pcscf]
    text: 送り先は Contact ではなく、UE 2 の REGISTER が来たアドレスです。NAT の後ろの UE に届くのは、そのアドレスだけだからです。
```

| 見るもの | 見どころ |
| --- | --- |
| S-CSCF に届いた Route | `orig` が無い。今度は着信者を担当する側として動く。 |
| AS に送る INVITE | `sescase=term`。AS が新しい INVITE を送り返す。 |
| P-Called-Party-ID | 発信された宛先。 |
| S-CSCF を出たあとの Request-URI | UE 2 が登録した Contact。 |
| S-CSCF を出たあとの Route | UE 2 の REGISTER にあった Path。 |
| P-CSCF を出たあとの SDP | 2 つ目のコンテキストの、アクセス側のターミネーションを指す。 |
| INVITE の送り先 | UE 2 の REGISTER が来たアドレス。 |

- INVITE は S-CSCF から I-CSCF へ出て、同じ S-CSCF に戻ってきます。
- P-CSCF が着信側だと分かるのは、Route の先頭が自分の Path の URI で、ユーザー部が `term` だからです。
- 送り先は Contact ではなく、REGISTER が来たアドレスです。NAT の後ろの UE に届くのは、そのアドレスだけだからです。

> 手順書 [6](../../../docs/guide/ja/call.md#6-着信側の-s-cscf-が-path-に沿って-invite-を送る) と [7](../../../docs/guide/ja/call.md#7-p-cscf-が-invite-を-ue-2-に届ける)。TS 24.229 5.4.3.3、5.2.6.4.3。

---

## 8。UE 2 が信頼性のある 183 で答える

```ims-scene
id: answer
lead: 183 は INVITE と同じノードを逆にたどり、P-CSCF は 2 回の通過でそれぞれ SDP を書き換えます。
steps:
  - id: answer
    title: UE 2 が 183 で答える
    only: [r183-ue2-p]
    checkpoint: true
    text: |
      SDP のアンサーと、`Require: 100rel` が付いた信頼性のある暫定応答です。
      UE 2 は PRACK が届くまで 183 を送り直します。
  - id: relay
    title: 着信側の P-CSCF がアンサーを中継ノードに伝える
    show: [mod2-put, mod2-ok]
    active: mod2-put
    zoom: [ue1, agw, ue2, pcscf]
    text: 答えた側に向いたターミネーション、ここではアクセス側に、UE 2 がどこで受けるかを伝えます。
  - id: up
    title: 183 が着信側の AS に届く
    show: [r183-p-s3, r183-s-as3]
    active: r183-p-s3
    checkpoint: true
    text: P-CSCF は SDP を、コンテキスト 2 のコア側を指すように書き換えます。ダイアログ 3 の相手は着信側の AS です。
  - id: dialog2
    title: AS が 183 を次のダイアログに写す
    only: [r183-as-s2, r183-s-i, r183-i-s, r183-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: r183-s-i
    detail: []
    text: |
      応答は INVITE の Via をたどるので、I-CSCF も通ります。
      S-CSCF を 2 回通り、発信側の AS に届きます。
  - id: dialog1
    title: 183 が発信側の P-CSCF に届く
    only: [r183-as-s1, r183-s-p1]
    active: r183-s-p1
    detail: []
    text: 発信側の AS が 183 をダイアログ 1 に写します。
  - id: relay1
    title: 発信側の P-CSCF もアンサーを伝える
    show: [mod1-put, mod1-ok]
    active: mod1-put
    zoom: [ue1, agw, ue2, pcscf]
    checkpoint: true
    text: 今度は網の側から来たアンサーなので、コア側のターミネーションに送り先を伝えます。
  - id: ue1
    title: 183 が UE 1 に届く
    show: [r183-p-ue1]
    zoom: [ue1, agw, ue2, pcscf]
    text: UE 1 に見えるのは自分のコンテキストのアクセス側です。どちらの UE も中継ノードとだけやり取りします。
```

| 見るもの | 見どころ |
| --- | --- |
| 183 の SDP | アンサー。PCMU と、UE 2 のアドレスとポート。 |
| 183 の Require と RSeq | `100rel` と `precondition`。信頼性のある暫定応答。 |
| 183 のプリコンディション | 両端とも資源がある。`a=conf` は無い。 |
| 183 の To のタグ、Contact、Record-Route | 初期ダイアログができる。 |
| UE 1 に届いた 183 の SDP | P-CSCF が 2 回書き換えている。中継ノードを指す。 |

- 183 は INVITE の Via をたどって戻るので、Record-Route を付けない I-CSCF も通ります。I-CSCF を通らないのは、このあとのダイアログの中の要求です。
- P-CSCF は、SDP を書き換える前に、答えた側に向いたターミネーションの送り先を `Modify`（HTTP の PUT）で中継ノードに伝えます。

> 手順書 [8](../../../docs/guide/ja/call.md#8-ue-2-がオファーに答える信頼性のある-183)。RFC 3264 6.1。RFC 3262 3。TS 24.229 5.1.4.1。

---

## 9。UE 1 が 183 を確認する（PRACK）

```ims-scene
id: prack
lead: PRACK はダイアログの中の要求です。3 つのダイアログに、PRACK と 200 が 1 組ずつあります。
steps:
  - id: dialog1
    title: UE 1 の PRACK が発信側の AS で終わる
    only: [prack-ue1-p, prack-p-s1, prack-s-as1, pok-as-s1, pok-s-p1, pok-p-ue1]
    active: prack-ue1-p
    checkpoint: true
    text: |
      PRACK はルートセットに沿って、P-CSCF と S-CSCF を通ります。
      ダイアログ 1 の相手の AS がすぐに 200 で答えます。
  - id: dialog2
    title: AS が次のダイアログで自分の PRACK を送る
    only: [prack-as-s2, prack-s-as2, pok-as-s2, pok-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: prack-as-s2
    detail: []
    checkpoint: true
    text: ダイアログ 2 の PRACK は S-CSCF を通って着信側の AS に届きます。I-CSCF は通りません。
  - id: dialog3
    title: PRACK が UE 2 に届き、UE 2 が 200 で答える
    only: [prack-as-s3, prack-s-p3, prack-p-ue2, pok-ue2-p, pok-p-s3, pok-s-as3]
    active: prack-p-ue2
    detail: []
    text: |
      着信側の AS がダイアログ 3 で PRACK を送ります。
      PRACK が届いたので、UE 2 は 183 を送り直すのをやめ、200 で答えます。
```

| 見るもの | 見どころ |
| --- | --- |
| PRACK の RAck | 183 の RSeq と、INVITE の CSeq。 |
| PRACK の経路 | 発信者の AS で終わり、AS が次のダイアログに送る。 |

- PRACK はダイアログの中の要求なので、そのダイアログの相手で終わります。AS は受け取った PRACK にすぐ 200 で答え、次のダイアログに自分の PRACK を送ります。3 つのダイアログに、PRACK と 200 が 1 組ずつあります。Record-Route を付けない I-CSCF は、どれも通りません。
- 2 つのダイアログのメッセージは別々の経路を進むので、キャプチャでの順序は決まっていません。

> 手順書 [9](../../../docs/guide/ja/call.md#9-ue-1-が-183-を確認するprack)。RFC 3262 3、4。RFC 3261 12.2.1.1。

---

## 10 と 11。180、200 OK、ACK

```ims-scene
id: confirm
lead: UE 2 が応答すると、200 OK がダイアログごとに戻り、それぞれの相手が ACK で確かめます。
steps:
  - id: ring
    title: UE 2 が鳴らし、180 を送る
    checkpoint: true
    only: [ue2-ring, r180-ue2-p]
    active: r180-ue2-p
    detail: [ue2-ring, r180-ue2-p]
    text: |
      180 も 183 と同じノードを通って UE 1 に届き、PRACK が返ります。
      資源がまだ無い着信側は、資源がそろうまで 180 を送りません。
  - id: answer
    title: UE 2 が応答し、200 OK が着信側の AS に届く
    checkpoint: true
    only: [ok-ue2-p, ok-p-s3, ok-s-as3]
    active: ok-ue2-p
    text: 200 OK に SDP はありません。アンサーは信頼性のある 183 で届いています。
  - id: ack3
    title: 着信側の AS がすぐ ACK を返す
    checkpoint: true
    show: [ack-as-s3, ack-s-p3, ack-p-ue2]
    active: ack-p-ue2
    detail: []
    text: |
      AS は UE 2 の 200 に、自分のダイアログですぐ ACK を返します。
      だから UE 2 への ACK が UE 1 への 200 より先に来ることがあります。
  - id: dialog2
    title: ダイアログ 2 の 200 OK と ACK
    checkpoint: true
    only: [ok-as-s2, ok-s-i, ok-i-s, ok-s-as2, ack-as-s2, ack-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: ok-s-i
    detail: []
    text: 200 OK は Via をたどって I-CSCF を通ります。ACK はルートセットに沿うので、I-CSCF を通りません。
  - id: dialog1
    title: 200 OK が UE 1 に届く
    only: [ok-as-s1, ok-s-p1, ok-p-ue1]
    active: ok-p-ue1
    text: これでダイアログ 1 が確定します。
  - id: ack1
    title: UE 1 が ACK を送る
    show: [ack-ue1-p, ack-p-s1, ack-s-as1]
    active: ack-ue1-p
    text: ACK はルートセットに沿って、ダイアログ 1 の相手の AS に届きます。
```

| 見るもの | 見どころ |
| --- | --- |
| 180 Ringing | プリコンディションがそろってから送る。これにも PRACK が返る。 |
| INVITE への 200 OK | ダイアログが確定する。 |
| 200 OK の SDP | 無い。アンサーは信頼性のある 183 で届いている。 |
| ACK | ルートセットに沿って進む。INVITE のトランザクションの一部ではない。 |

- UE 2 は ACK が届くまで、間隔を広げながら 200 OK を送り直します。
- 資源がまだ無い着信側は、資源がそろうまで 180 を送りません。この構成の UE はどちらも資源があるので、183 のすぐあとに 180 が続きます。
- AS は UE 2 の 200 に、自分のダイアログですぐ ACK を返します。だから UE 2 への ACK が、UE 1 への 200 より先に来ることがあります。

> 手順書 [10](../../../docs/guide/ja/call.md#10-ue-2-が鳴らしてから応答する180-と-200-ok) と [11](../../../docs/guide/ja/call.md#11-ue-1-が確認するack)。RFC 3312 6。TS 24.229 5.1.4.1。IR.92 2.4.1。RFC 3261 13.3.1.4、13.2.1、13.2.2.4。RFC 3262 5。

---

## 12。音声が中継ノードを通る

```ims-scene
id: media
lead: RTP は UE と中継ノードの間だけを流れ、中継ノードの中で 2 つのコンテキストを通ります。
steps:
  - id: send
    title: UE 1 が RTP を中継ノードに送る
    checkpoint: true
    only: [rtp-ue1-agw]
    zoom: [ue1, agw, ue2, pcscf]
    text: 宛先は、UE 1 が 183 で受け取ったアドレスです。コンテキスト 1 のアクセス側のターミネーションです。
  - id: inside
    title: 中継ノードの中で 2 つのコンテキストを通る
    checkpoint: true
    show: [agw-relay]
    focus: [agw]
    zoom: [ue1, agw, ue2, pcscf]
    text: |
      1 つの通話に、中継ノードのコンテキストが 2 つあります。
      2 つの網の IMS-AGW を通るのと同じように、RTP は中継ノードを 2 回通ります。
  - id: deliver
    title: RTP が UE 2 に届く
    checkpoint: true
    show: [rtp-agw-ue2]
    zoom: [ue1, agw, ue2, pcscf]
    text: UE どうしは直接やり取りしません。2 台の UE が別々の P-CSCF の後ろにいても、通話は同じに見えます。
  - id: latch
    title: 中継ノードが送り先を決める（ラッチング）
    only: [rtp-ue2-agw, agw-latch]
    focus: [agw]
    zoom: [ue1, agw, ue2, pcscf]
    text: |
      UE 側では最初に受け取ったパケットの送信元を送り先にします。
      ホストのソフトフォンのように NAT の後ろの UE は、SDP に書けないアドレスから送ってきます。
  - id: back
    title: UE 2 の音声が UE 1 に届く
    show: [rtp-agw-ue1]
    zoom: [ue1, agw, ue2, pcscf]
    text: 網の側ではラッチングをせず、最新の SDP のアドレスへ送ります。
  - id: rtcp
    title: RTCP も中継ノードを通る
    only: [rtcp-ue1-agw]
    zoom: [ue1, agw, ue2, pcscf]
    text: RTCP は RTP の 1 つ上のポートを使います。通話の終わりには RTCP の BYE を送ります。
```

```text
UE 1 -> relay access (context 1) -> relay core (context 1)
     -> relay core (context 2) -> relay access (context 2) -> UE 2
```

| 見るもの | 見どころ |
| --- | --- |
| RTP の間隔と大きさ | 20 ms ごと。G.711 μ-law が 160 バイト。 |
| RTP の送信元と宛先 | UE と中継ノードの間だけ。UE どうしは直接やり取りしない。 |
| 送信と受信のポート | 同じポート。対称 RTP。 |
| RTCP | RTP の 1 つ上のポート。5 秒に 1 回以上。 |

- 中継ノードは、UE 側では、最初に受け取ったパケットの送信元を送り先にします。ラッチングと呼びます。網の側では、最新の SDP のアドレスへ送ります。

フィルタは `rtp || rtcp` です。**Play Streams** で音声を聞きます。

> 手順書 [12](../../../docs/guide/ja/call.md#12-音声が中継ノードを通って流れる)。RFC 3550 5.1、6。RFC 3551 4.5.14。RFC 4961。IR.92 3.2.4。

---

## 12。2 つのコンテキストとラッチング

- 1 つの通話に、中継ノードのコンテキストが 2 つあります。発信側を通る P-CSCF が 1 つ、着信側を通る P-CSCF がもう 1 つを作ります。P-CSCF はどちらの回も、自分の側だけを扱います。
- だから RTP は、2 つの網の IMS-AGW を通るのと同じように、中継ノードを 2 回通ります。キャプチャでも 2 つの側を見分けられます。2 台の UE が別々の P-CSCF の後ろにいても、通話は同じに見えます。
- ラッチングは NAT の後ろの UE のためです。ホストのソフトフォンは、SDP には書けないアドレスから送ってきます。実際の網の P-CSCF と IMS-AGW も同じことをします。
- RTCP の複合パケットには、送信の報告（UE が何も送っていない間は受信の報告）と、CNAME の入った SDES があります。RTCP の BYE で流れが終わります。

---

## 13。切断

```ims-scene
id: bye
lead: BYE もダイアログごとに進み、P-CSCF は 200 が通るときに自分の側のコンテキストを削除します。
steps:
  - id: bye1
    title: UE 1 が BYE を送る
    checkpoint: true
    only: [bye-ue1-p, bye-p-s1, bye-s-as1]
    active: bye-ue1-p
    text: BYE はダイアログの中の要求で、ルートセットに沿って発信側の AS まで進みます。
  - id: ok1
    title: 発信側の AS が 200 で答える
    show: [bok-as-s1, bok-s-p1, bok-p-ue1]
    active: bok-s-p1
    detail: []
    text: AS は UE 1 の BYE に、自分のダイアログで答えます。
  - id: delete1
    title: P-CSCF がコンテキスト 1 を削除する
    checkpoint: true
    show: [del1, del1-ok]
    active: del1
    zoom: [ue1, agw, ue2, pcscf]
    text: |
      P-CSCF は 200 が通るときに削除します。BYE が断られることもあるので、応答を待ちます。
      この削除は SIP のメッセージには出ません。中継ノードの API を呼びます。
  - id: dialog2
    title: AS が次のダイアログに自分の BYE を送る
    only: [bye-as-s2, bye-s-as2, bok-as-s2, bok-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: bye-as-s2
    detail: []
    text: |
      ダイアログ 2 の BYE と 200 は、S-CSCF を通って 2 つの AS のあいだを行き来します。
      UE 1 への 200 と UE 2 への BYE は、どちらが先に来ることもあります。
  - id: dialog3
    title: BYE が UE 2 に届く
    checkpoint: true
    only: [bye-as-s3, bye-s-p3, bye-p-ue2]
    active: bye-p-ue2
    detail: []
    text: 着信側の AS がダイアログ 3 で BYE を送ります。
  - id: ok3
    title: UE 2 が最後の 200 を返す
    only: [bok-ue2-p, bok-p-s3, bok-s-as3]
    active: bok-ue2-p
    detail: []
    text: 最後の 200 は UE 2 から返ります。
  - id: delete2
    title: P-CSCF がコンテキスト 2 を削除する
    show: [del2, del2-ok]
    active: del2
    zoom: [ue1, agw, ue2, pcscf]
    text: 着信側の通過で作ったコンテキストも、200 が通るときに削除します。
```

| 見るもの | 見どころ |
| --- | --- |
| BYE | ダイアログの中の要求。ルートセットに沿って進む。 |
| BYE の Reason | `RELEASE_CAUSE;cause=1`。通話を終えた理由。 |
| BYE への 200 | ダイアログごとに相手が返す。最後は UE 2。 |
| P-CSCF の動き | 200 が通るときに、中継のコンテキストを削除する。 |

- P-CSCF が応答を待つのは、BYE が断られることもあるからです。
- コンテキストの削除は、SIP のメッセージには出ません。P-CSCF は中継ノードの API を呼びます。このプロジェクト独自の小さな HTTP の API で、H.248 のような大きなプロトコルより中継ノードを読みやすくするためのものです。
- AS は UE 1 の BYE に自分のダイアログで答え、次のダイアログに自分の BYE を送ります。だから UE 1 への 200 と UE 2 への BYE は、どちらが先に来ることもあります。

> 手順書 [13](../../../docs/guide/ja/call.md#13-ue-1-が切断するbye)。TS 24.229 5.1.5。IR.92 2.2.4。RFC 3261 15.1.1、15.1.2、12.2.2。

---

## 失敗を見る

```ims-scene
id: failures
lead: 応答を作ったノードは、その応答を運ぶ最初のパケットの送信元で分かります。
steps:
  - id: busy
    title: 話中。UE 2 が 486 で断る
    checkpoint: true
    only: [r486-ue2-p]
    text: UE 2 を `-reject 486` 付きで起動すると、通話は 486 Busy Here で終わります。
  - id: unregistered
    title: 未登録。LIA が 2003 で答える
    checkpoint: true
    only: [lir-u, lia-u]
    active: lia-u
    detail: [lir-u, lia-u]
    zoom: [icscf, hss, scscf, mmtel]
    text: 登録していない `tel:+12025550103` に発信します。未登録の利用者にも、MMTel AS を呼ぶ iFC があります。
  - id: select
    title: I-CSCF が S-CSCF を選ぶ
    checkpoint: true
    show: [inv-i-s-u]
    zoom: [icscf, hss, scscf, mmtel]
    text: 名前の代わりに能力が返ったので、I-CSCF は初回の登録と同じように S-CSCF を選びます。
  - id: profile
    title: S-CSCF がプロファイルを取る（SAR と SAA）
    only: [sar-u, saa-u]
    active: sar-u
    detail: [sar-u, saa-u]
    zoom: [icscf, hss, scscf, mmtel]
    text: 未登録の利用者のプロファイルを UNREGISTERED_USER の SAR で HSS から受け取ります。
  - id: as
    title: AS が INVITE をそのまま送り返す
    checkpoint: true
    only: [inv-s-as-u, inv-as-s-u]
    active: inv-as-s-u
    zoom: [icscf, hss, scscf, mmtel]
    text: 着信転送を設定していないので、AS は通話を受けません。
  - id: no-contact
    title: S-CSCF に送る先の登録が無い
    only: [s-480]
    focus: [scscf]
    zoom: [icscf, hss, scscf, mmtel]
    text: 着信者は登録していないので、S-CSCF は AS が送り返した INVITE を届けられません。どのサービスも通話を受けませんでした。
  - id: r480
    title: S-CSCF が 480 を返す
    checkpoint: true
    show: [r480-s-as]
    zoom: [icscf, hss, scscf, mmtel]
    text: 最初のパケットは S-CSCF から AS へ出ます。AS は受け取った応答を自分のダイアログに写すので、AS より先を探します。
```

| 場合 | 試し方 | 終わり方 | 返すノード |
| --- | --- | --- | --- |
| 話中 | UE 2 を `-reject 486` 付きで起動する。 | 486 Busy Here | UE 2 |
| 未登録 | `tel:+12025550103` に発信する。 | 480 Temporarily Unavailable | S-CSCF |
| 存在しない番号 | `tel:+12025550198` に発信する。 | 404 Not Found | S-CSCF |

- 未登録の利用者にも、MMTel AS を呼ぶ iFC があります。LIA は DIAMETER_UNREGISTERED_SERVICE（2003）になります。I-CSCF が選んだ S-CSCF は、AS が INVITE を送り返したところで 480 を返します。
- 存在しない番号には ENUM のレコードがありません。このプロジェクトには BGCF が無いので、S-CSCF が 404 で答えます。
- 応答を作ったノードは、その応答を運ぶ最初のパケットの送信元で分かります。AS は受け取った応答を自分のダイアログに写すので、AS より先を探します。

> 手順書 [「発信する」](../../../docs/guide/ja/call.md#発信する)、[3](../../../docs/guide/ja/call.md#3-s-cscf-が番号を-sip-uri-に変える)、[5](../../../docs/guide/ja/call.md#5-i-cscf-が着信者の所在を-hss-に問い合わせるlir-と-lia)。TS 29.228 6.1.4.1。TS 24.229 5.4.3.3。

---

## 失敗を見る（存在しない番号）

```ims-scene
id: unknown-number
lead: 番号の ENUM のレコードが無ければ、S-CSCF が 404 で答えます。
steps:
  - id: unknown
    title: 存在しない番号。ENUM にレコードが無い
    checkpoint: true
    only: [enum-q-x, enum-a-x]
    active: enum-a-x
    detail: [enum-q-x, enum-a-x]
    zoom: [scscf, dns, mmtel]
    text: '`tel:+12025550198` に発信します。この番号の ENUM のレコードはありません。'
  - id: no-uri
    title: S-CSCF は SIP URI に変えられない
    only: [enum-a-x, s-404]
    focus: [scscf]
    zoom: [scscf, dns, mmtel]
    text: このプロジェクトには BGCF が無いので、電話網へ送ることもできません。
  - id: r404
    title: S-CSCF が 404 で答える
    only: [r404-s-as]
    zoom: [scscf, dns, mmtel]
    text: 404 は AS の新しい INVITE への応答です。ここでも最初のパケットの送信元が S-CSCF です。
```

- 存在しない番号には ENUM のレコードがありません。このプロジェクトには BGCF が無いので、S-CSCF が 404 で答えます。

> 手順書 [3](../../../docs/guide/ja/call.md#3-s-cscf-が番号を-sip-uri-に変える)。TS 24.229 5.4.3.2。

---

## ラボから通話をかける

```sh
docker compose --profile phones up -d --build
```

1. <http://localhost:8080> を開きます。`phone2` のカードで、応答方針を `manual` にします。
2. `phone1` のカードで **Call** を押します。
3. `phone2` のカードで **Answer** を押します。どちらかで **Hang up** を押します。
4. 通話の横の **ladder** を選ぶと、通話中でもシーケンス図が出ます。

- 応答の前に切ると、発信側は CANCEL を送り、着信側は 603 で断ります。
- `phones` と `ue` のプロファイルを同時に動かさないでください。同じ加入者の UE が 2 台になり、互いの登録を上書きします。UE を動かす HTTP の API はこのプロジェクト独自のもので、IMS には何も流れません。

> 手順書 [ラボで IMS を見る「UE シミュレータを操作する」](../../../docs/guide/ja/lab.md#ue-シミュレータを操作する) と、[通話の「HTTP で UE を操作する」](../../../docs/guide/ja/call.md#http-で-ue-を操作する)

---

## コードで確かめる

| 手順 | 開くファイル |
| --- | --- |
| 1、8 から 13。UE の発信、応答、音声 | `internal/ue/call.go`、`internal/ue/media.go` |
| 2、7。P-CSCF の 2 回の通過と SDP の書き換え | `internal/pcscf/session.go` |
| 3、6。S-CSCF の発信側と着信側 | `internal/scscf/session.go` |
| 3、6。MMTel AS（routeing B2BUA） | `internal/mmtel/call.go` |
| 8 から 10。プリコンディション | `internal/precondition` |
| 3。ENUM | `internal/sipdns/enum.go` |
| 5。LIR | `internal/icscf/session.go` |
| 12。中継とラッチング | `internal/agw/relay.go` |
| ダイアログとルートセット | `internal/sip/dialog.go` |

- `internal/scscf/session.go` で、発信側と着信側を見分ける条件を探します。

> 手順書 [「このプロジェクトで簡略化していること」](../../../docs/guide/ja/call.md#このプロジェクトで簡略化していること)

---

[layout: exercise]

## 振り返り

1. UE 1 の INVITE の Request-URI は、UE 2 に届くまでに何回変わりますか。
2. S-CSCF は、自分が発信側か着信側かを、何で見分けますか。
3. I-CSCF が PRACK と BYE の経路にいないのは、なぜですか。
4. UE 1 が受け取る SDP のアドレスは、誰のものですか。
5. 480 と 404 は、それぞれどのノードが、何を根拠に返しますか。
6. UE 2 の 200 OK に SDP が無いのは、なぜですか。

---

[layout: answers]

## 振り返りの答え

1. 2 回です。S-CSCF が tel URI を SIP URI に変え、着信側の S-CSCF が UE 2 の Contact にします。
2. Route の先頭の URI にある `orig` で見分けます。
3. Record-Route を付けないからです。
4. メディア中継ノードのものです。UE 1 のコンテキストのアクセス側です。
5. どちらも S-CSCF です。480 は、LIA が DIAMETER_UNREGISTERED_SERVICE で、未登録の利用者のサービスである MMTel AS が通話を受けなかったからです。404 は、ENUM にレコードが無いからです。
6. 答えが信頼できる 183 で届いているからです（RFC 3262 5）。

---

## 次の回

- 次の回は SMS です。登録と、I-CSCF が S-CSCF を見つけるしくみを、そのまま使います。
- この回で MMTel AS を呼んだ iFC が、次の回の主役です。ショートメッセージは iFC で IP-SM-GW に届きます。

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```

> 次の回の手順書は [SMS の流れを追う](../../../docs/guide/ja/sms.md) です。
