# 5GC のスタブ

[English](README.md)

このパッケージは、5GS のアクセス網のスタブです。1 つのノードが 5GC の AMF、SMF、UPF の代わりをし、その中に PCF のスタブを持ちます。EPS に対するパッケージ `epc` と同じ役目を、5GS で果たします。UE は、HTTP と JSON の API で登録し、PDU セッションを張ります。API のメッセージには、TS 24.501 の NAS のメッセージの名前を付けています。スタブは UE に、固定の IP アドレスと P-CSCF のアドレスを渡し、SIP のための既定の QoS ルールの QoS フロー（5QI 5）を保ちます。PCF は、N5（パッケージ `n5`）で P-CSCF に応えます。通話のメディアを PCF が認めると、スタブは音声のための GBR の QoS フロー（5QI 1）を足し、UE のイベントのストリームで知らせます。

このプロジェクトのノードは、このパッケージを動かしていません。`cmd` のどのバイナリにも組み込まれていません。動く様子は試験と例で見られます。

```sh
go test ./internal/fivegc
go doc ./internal/fivegc
```

## このパッケージで簡略化していること

どの行の場所にも、コードに `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| パッケージ `fivegc`、TS 23.502 4.3.2.2.1 | AMF、SMF、UPF、PCF を 1 つのノードにしているので、N11、N4、N7 は 1 つの Store を使う Go の呼び出しです。NRF も UDM もありません。スタブは設定にある加入者に応え、AF には設定で PCF の apiRoot を渡します。ユーザプレーンもありません。N3 と N6 は、同じ 1 つの Docker のネットワークです。 | IMS を学ぶ人が見るのは、UE、P-CSCF、PCF のあいだの信号です。これらのインタフェースは、その信号を運びません。 |
| UE の API、TS 24.501 と TS 38.413 | NAS の符号化、NGAP、無線。メッセージは HTTP と JSON で運び、URL の IMSI が、N2 の UE ごとの接続と、REGISTRATION REQUEST の SUCI の代わりになります。セキュリティ（5G-AKA、セキュリティモードの制御）、5G-GUTI、ネットワークスライス（NSSAI と S-NSSAI）、移動と定期的な登録の更新、網の側のタイマーは省きます。 | 学ぶのは手順で、符号化ではありません。UE はアクセス網のインタフェースを通してスタブにつなぐので、本物の NAS を話す実装と入れ替えられます。 |
| `RegistrationResult`、TS 24.501 9.11.3.6 | NAS での SMS。「SMS over NAS transport allowed」のビットは立てません。 | このスタブにつないだ UE は、ショートメッセージを IP-SM-GW を通して IP で送ります。 |
| `PDUSessionEstablishmentRequest`、TS 24.501 8.3.1 | Integrity protection maximum data rate の IE。 | 守るべきユーザプレーンがありません。 |
| `SystemInformation`、TS 38.331 | システム情報の報知。スタブは代わりに HTTP のパスで渡します。 | 報知に使う無線がありません。 |
| `Subscriber`、TS 24.501 6.2.4.2 | アドレスのプールと UPF。加入者ごとに固定のアドレスを持ち、SMF はそれを PDU address の IE で渡します。 | UE のコンテナは Docker のネットワークで同じアドレスを持つので、UE のパケットはトンネルもホストのネットワークの設定も無しに、IMS に直接届きます。 |
| `FiveGC.Register`、TS 23.502 4.2.2.2.2 | 認証（AUSF と UDM による 5G-AKA）、NAS のセキュリティ、UDM の UE のコンテキスト、5G-GUTI と、それに伴う REGISTRATION COMPLETE（TS 24.501 5.5.1.2.4）。REGISTRATION REJECT も送りません。スタブが応えられない要求には、API のエラーを返します。 | スタブは、設定にある IMSI に応えます。 |
| `FiveGC.DeregistrationAccepted`、TS 24.501 5.5.2.3 | タイマー T3522 と、それが切れたときの登録解除の要求の再送。 | UE の API は、メッセージを失わない TCP の上で動きます。 |
| `FiveGC.EstablishPDUSession`、TS 24.501 6.4.1 | UE ごとに 2 つ以上の PDU セッション、IPv4 のほかの種類、1 のほかの SSC モード、既定の DNN。PDU SESSION ESTABLISHMENT REJECT も送りません。スタブが応えられない要求には、API のエラーを返します。 | 既定の DNN を取ってくる加入の情報が無いので、UE は設定にある DNN を指定しなければなりません。 |
| `FiveGC.PDUSessionReleaseCompleted`、TS 24.501 6.3.3.2 | `PDU SESSION INACTIVE PENDING` の状態と、タイマー T3592。網は PDU SESSION RELEASE COMMAND を送った時点でセッションを忘れます。 | UE の API は、メッセージを失わない TCP の上で動きます。 |
| SMF と PCF、TS 29.512 | Npcf_SMPolicyControl（確立のときの Create、PCC ルールを運ぶ UpdateNotify、ルールの報告を運ぶ Update、解放のときの Delete）。代わりに、PCF と SMF は Store の PDU セッションを共有します。 | スタブには PCF と SMF が 1 つずつしかありません。EPC のスタブの Gx と同じく、N7 は IMS を学ぶ人に要らないメッセージを増やすだけです。 |
| `sessionAMBR`、TS 23.501 5.7.2.7 | 加入の情報の Session-AMBR。どのセッションも同じ値で、スタブは速度を制限しません。 | 加入の値を取ってくる UDM が、スタブにはありません。 |
| `FiveGC.reconcileVoiceQoSFlow`、TS 24.501 6.3.2.2 | フローがあるあいだにルールが変わったときの PDU SESSION MODIFICATION COMMAND と、それに伴う、1 つのコマンドの応答を待ってから次を送ることと、タイマー T3591。スタブは、QoS ルールと QoS の自分の記録だけを更新します。 | スタブはどちらも強制しません。UE は、フローがあることだけを知れば足ります。 |
| `voiceQoSFlow`、TS 23.501 5.7.1.2 | NG-RAN に送る QoS のプロファイルと、その ARP。スタブは、どちらの速度も制限しません。 | フローを受け入れる RAN がありません。 |
| PCF、TS 29.513 8.4 | BSF への PDU セッションの登録（TS 29.521）。AF には PCF の apiRoot を渡します。 | スタブには PCF が 1 つしかありません。 |
| `pcf.Create`、TS 29.513 6.2 | IPv6 のアドレス。IPv6 のアドレスで結び付けたコンテキストには、PDU セッションが見つかりません。 | スタブは IPv4 の PDU セッションだけを扱います。 |
| `pcf.Update`、TS 29.514 4.2.3.2 と RFC 7396 | メディアの要素を、属性ごとに合わせること。パッチのメディアの要素は、同じ番号の要素を丸ごと置き換えます。 | このプロジェクトの P-CSCF は、毎回 SDP の全体からすべての要素を作ります。 |
| `pcf.Delete`、TS 29.514 4.2.4.2 | Delete で求められたイベント。応答はいつも 204 です。 | パッケージ `n5` が名前を付けたイベントは、リソースの割り当てのものだけです。コンテキストのリソースを解放したあとには、報告することが残りません。 |
| `pccRules`、TS 29.513 Table 7.3.3-1 | 音声のほかのメディア。音声は 5QI 1 で認めます。表は、運用者の方針が無いとき音声に 5QI 1 を当て、その NOTE 14 は、SRVCC を使う IMS のサービスに 5QI 1 を求めます。 | スタブには、ほかのメディアのための QoS フローが無いので、それらを断ります。 |
| `maxDataRates`、TS 29.513 Table 7.3.3-1 | 表が、運用者の方針、コーデックやアプリケーションに固有の計算、「運用者が決める」速度に任せている場合。これらは 0 にします。 | スタブには、値を取ってくる運用者の方針がありません。 |
| `parseFDesc`、TS 29.214 5.3.8 | フローの記述の IPv6 のアドレス。 | スタブは IPv4 の PDU セッションだけを扱います。 |
| `packetFilter`、TS 24.501 9.11.4.13 | 両方向のフィルタと、ローカルのアドレス。フローごとに、その向きだけのフィルタを作ります。 | UE のアドレスは、PDU セッションの 1 つだけです。 |
