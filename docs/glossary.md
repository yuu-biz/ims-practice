# Glossary / 用語集

This glossary lists the abbreviations used in this project. Each entry has an English and a Japanese description. The specifications cited in each entry give the normative definitions.

このプロジェクトで使う略語の一覧です。各項目に英語と日本語の説明があります。正式な定義は、各項目に挙げた仕様にあります。

## Nodes / ノード

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| UE | User Equipment | The subscriber's device. In this project, the UE simulator or a softphone plays this role. | 加入者の端末。このプロジェクトでは UE シミュレータかソフトフォンがこの役割を持つ。 |
| CSCF | Call Session Control Function | The SIP servers at the center of IMS. There are three roles: P-CSCF, I-CSCF, and S-CSCF. | IMS の中心にある SIP サーバー。P-CSCF、I-CSCF、S-CSCF の 3 つの役割がある。 |
| P-CSCF | Proxy-CSCF | The first SIP server the UE talks to. It sits at the edge of the IMS and controls media resources for the access network. | UE が最初に通信する SIP サーバー。IMS の入口にあり、アクセス網のメディア資源を制御する。 |
| I-CSCF | Interrogating-CSCF | The entry point of the home network. It asks the HSS which S-CSCF serves the user and forwards requests there. | ホーム網の入口。どの S-CSCF が加入者を担当するかを HSS に問い合わせ、その S-CSCF へ転送する。 |
| S-CSCF | Serving-CSCF | The registrar and session controller for a user. It authenticates the UE, keeps the registration, and invokes application servers. | 加入者の登録とセッションを管理する。UE を認証し、登録状態を保持し、アプリケーションサーバーを呼び出す。 |
| HSS | Home Subscriber Server | The subscriber database. It stores identities, authentication keys, the user profile, and the assigned S-CSCF. | 加入者データベース。識別子、認証鍵、ユーザープロファイル、担当の S-CSCF を保持する。 |
| AS | Application Server | A server that adds a service on top of basic call control, such as SMS or call forwarding. The S-CSCF invokes it through iFCs. | SMS や着信転送など、基本の呼制御の上にサービスを加えるサーバー。S-CSCF が iFC に従って呼び出す。 |
| IP-SM-GW | IP Short Message Gateway | The AS that handles SMS over IP. | SMS over IP を処理する AS。 |
| SC | Service Centre (also SMSC) | The node that stores short messages and forwards them from the sender to the recipient (TS 23.040). In this project it is a stub inside the IP-SM-GW that stores nothing. | 送信者から受け取ったショートメッセージを蓄積し、受信者へ転送するノード（TS 23.040）。このプロジェクトでは IP-SM-GW の中のスタブで、蓄積はしない。 |
| MMTel AS / TAS | Multimedia Telephony Application Server / Telephony Application Server | The AS that provides the MMTel supplementary services, such as call forwarding and call barring. GSMA IR.92 calls it the TAS. | 着信転送や規制などの MMTel の補足サービスを提供する AS。GSMA IR.92 では TAS と呼ぶ。 |
| MRF | Multimedia Resource Function | A node that plays announcements and tones and mixes conference audio. It has two parts, the MRFC and the MRFP. | 音声ガイダンスやトーンを流し、会議の音声を合成するノード。MRFC と MRFP の 2 つに分かれる。 |
| MRFC | Multimedia Resource Function Controller | The SIP side of the MRF. It controls the MRFP over the Mp reference point (TS 23.333). | MRF の SIP の側。Mp 参照点で MRFP を制御する（TS 23.333）。 |
| MRFP | Multimedia Resource Function Processor | The media side of the MRF. It plays the audio and mixes the streams that the MRFC asks for. | MRF のメディアの側。MRFC の指示で音声を流し、複数の音声を合成する。 |
| IMS-ALG | IMS Application Level Gateway | The P-CSCF function that controls the IMS-AGW. | IMS-AGW を制御する P-CSCF の機能。 |
| Context / Termination | H.248 context and termination | The model of a media gateway: a context joins terminations, and each termination is one place where media goes in and out, such as a UDP port. The media relay of this project uses the same words. | メディアゲートウェイの考え方。コンテキストがターミネーションをつなぎ、ターミネーションは UDP のポートのようなメディアの出入り口の 1 つ。このプロジェクトのメディア中継ノードも同じ言葉を使う。 |
| IMS-AGW | IMS Access Gateway | The media relay at the edge of the IMS, controlled by the IMS-ALG over the Iq reference point. | IMS の入口にあるメディア中継装置。IMS-ALG が Iq 参照点で制御する。 |
| BGCF | Breakout Gateway Control Function | The node that chooses where to send a call that leaves the IMS. | IMS の外へ出る呼の行き先を選ぶノード。 |
| IBCF | Interconnection Border Control Function | The border node toward another operator's IMS. It is the exit point for the requests that leave the home network and the entry point for those that come in, hides the home network's topology and applies the interconnect profile (TS 24.229 5.10). | 他事業者の IMS との境界にあるノード。自網から出る要求の出口、入ってくる要求の入口として働き、自網の構成を隠し、相互接続プロファイルを当てる（TS 24.229 5.10）。 |
| TrGW | Transition Gateway | The media relay at the border toward another operator's IMS, controlled by the IBCF over Ix. It changes the addresses and ports in the media path (TS 29.165 5.2.2). In this project it is a node of the agw role. | 他事業者の IMS との境界にあるメディア中継装置。IBCF が Ix で制御する。メディアの経路でアドレスとポートを変える（TS 29.165 5.2.2）。このプロジェクトでは役割 agw のノード。 |

## Access network / アクセス網

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| EPC | Evolved Packet Core | The 4G core network. It gives the UE an IP connection to the IMS. | 4G のコアネットワーク。UE に IMS への IP 接続を提供する。 |
| 5GC | 5G Core | The 5G core network. Its functions talk to each other over HTTP/2 service-based interfaces. | 5G のコアネットワーク。機能どうしは HTTP/2 のサービスベースインタフェースで通信する。 |
| P-GW | PDN Gateway | The EPC node that assigns the UE's IP address and connects it to the IMS. | UE の IP アドレスを割り当て、IMS へ接続する EPC のノード。 |
| MME | Mobility Management Entity | The EPC control-plane node that handles the UE's attach and sets up its bearers with NAS signaling. In this project the EPC stub plays this role. | UE の接続を受け、NAS の信号で bearer を用意する EPC の制御のノード。このプロジェクトでは EPC スタブがこの役割を持つ。 |
| S-GW | Serving Gateway | The EPC user-plane node between the radio network and the P-GW. | 無線の網と P-GW のあいだにある、EPC のユーザーデータのノード。 |
| APN | Access Point Name | The name of the packet data network that the UE connects to. VoLTE uses the IMS APN for SIP (GSMA IR.92 4.4.1). | UE が接続するパケットの網の名前。VoLTE は SIP に IMS の APN を使う（GSMA IR.92 4.4.1）。 |
| PCO | Protocol Configuration Options | An information element in the bearer setup messages. The UE asks for the P-CSCF addresses in it, and the network returns the list in it (TS 24.008 10.5.6.3, TS 24.229 L.2.2.1). | bearer を作るメッセージの情報要素。UE はこの中で P-CSCF のアドレスを求め、網はこの中で一覧を返す（TS 24.008 10.5.6.3、TS 24.229 L.2.2.1）。 |
| EPS bearer | EPS bearer | A path with one QoS between the UE and the P-GW. The default bearer is made when the UE connects; the network adds dedicated bearers, such as the one for voice. | UE と P-GW のあいだの、1 つの QoS を持つ経路。接続のときに default bearer ができ、網が音声用などの dedicated bearer を加える。 |
| TFT | Traffic Flow Template | The packet filters that tell which IP flows a bearer carries. The UE uses them to match a dedicated bearer to its SIP session (TS 24.301 9.9.4.16, TS 24.229 L.2.2.5.1B). | bearer が運ぶ IP フローを示すパケットのフィルタ。UE はこれで dedicated bearer を SIP のセッションと対応づける（TS 24.301 9.9.4.16、TS 24.229 L.2.2.5.1B）。 |
| PCRF | Policy and Charging Rules Function | The EPC node that decides QoS. The P-CSCF asks it over Rx for a voice bearer. | QoS を決める EPC のノード。P-CSCF が Rx で音声用ベアラを要求する。 |
| PCEF | Policy and Charging Enforcement Function | The function in the P-GW that applies the PCC rules the PCRF sends over Gx. | PCRF が Gx で送る PCC rule を当てはめる、P-GW の中の機能。 |
| PCF | Policy Control Function | The 5GC counterpart of the PCRF. | 5GC で PCRF に当たる機能。 |
| SBI | Service Based Interface | The HTTP/2 interfaces between 5GC functions. | 5GC の機能間の HTTP/2 インタフェース。 |
| QCI | QoS Class Identifier | A number that names a QoS class in the EPC. Voice uses QCI 1, and IMS signaling uses QCI 5. | EPC の QoS クラスを表す番号。音声は QCI 1、IMS の信号は QCI 5 を使う。 |
| GBR | Guaranteed Bit Rate | A bearer whose bit rate the network reserves. The voice bearer (QCI 1) is a GBR bearer; the signaling bearer (QCI 5) is not (TS 23.203 6.1.7). | 網がビットレートを確保する bearer。音声の bearer（QCI 1）は GBR で、信号の bearer（QCI 5）は GBR ではない（TS 23.203 6.1.7）。 |
| VoLTE | Voice over LTE | Voice calls over IMS on 4G access. GSMA IR.92 defines the profile. | 4G のアクセスで IMS を使う音声通話。GSMA IR.92 がプロファイルを定める。 |

## Identities / 識別子

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| IMSI | International Mobile Subscriber Identity | The subscriber identity stored on the SIM. This project uses only test values in the 001-01 test network. | SIM に記録された加入者の識別子。このプロジェクトでは試験用のネットワーク 001-01 の値だけを使う。 |
| MSISDN | Mobile Station International Subscriber Directory Number | The subscriber's phone number. | 加入者の電話番号。 |
| IMPI | IP Multimedia Private Identity | The identity used for authentication. It is not shown to other users. | 認証に使う識別子。他の加入者には見せない。 |
| IMPU | IP Multimedia Public Identity | The identity other users call, as a SIP URI or a tel URI. | 他の加入者が呼び出すときに使う識別子。SIP URI か tel URI で表す。 |
| Temporary IMPU | Temporary Public User Identity | The IMPU that a UE with a USIM and no ISIM derives from the IMSI, `sip:<IMSI>@ims.mnc<MNC>.mcc<MCC>.3gppnetwork.org` (TS 23.003 13.4B). TS 23.228 E.3.1 strongly recommends that the operator bar it; then it is used in REGISTER only, and only the CSCFs and the HSS see it. | ISIM が無く USIM だけを持つ UE が、IMSI から作る IMPU。形は `sip:<IMSI>@ims.mnc<MNC>.mcc<MCC>.3gppnetwork.org`（TS 23.003 13.4B）。TS 23.228 E.3.1 は運用者にこれを規制するよう強く勧めている。規制すると REGISTER にだけ使われ、見えるのは CSCF と HSS だけになる。 |
| Default IMPU | Default Public User Identity | The first URI of P-Associated-URI in the 200 to REGISTER, which is never barred. The UE uses it in its requests, and the P-CSCF asserts it when the UE names no registered IMPU (TS 24.229 5.1.1.2.1, 5.2.6.3.1). | REGISTER の 200 の P-Associated-URI の最初の URI。規制された識別子にはならない。UE はリクエストにこれを使い、UE が登録済みの IMPU を指定しないとき、P-CSCF はこれを P-Asserted-Identity に入れる（TS 24.229 5.1.1.2.1、5.2.6.3.1）。 |
| PSI | Public Service Identity | An identity that names a service, such as a conference, instead of a user. An application server hosts it, and its domain is one that the operator defines (TS 23.003 13.5). A request reaches the AS by the originating filter criteria of the user, by the HSS, or by DNS for a subdomain of PSIs (TS 23.228 5.4.12). | 利用者ではなく、会議のようなサービスを指す識別子。アプリケーションサーバーが受け持ち、ドメインは事業者が決める（TS 23.003 13.5）。要求は、利用者の発信側のフィルタ条件、HSS、または PSI のサブドメインの DNS のどれかで AS に届く（TS 23.228 5.4.12）。 |
| GRUU | Globally Routable User Agent URI | A URI that reaches one device of a user, not every device registered with the identity. It has the `gr` URI parameter (RFC 5627 3.1). An IBCF passes a Contact with a GRUU on as it came (TS 24.229 5.10.5). | 利用者の 1 台の端末だけに届く URI。その識別子で登録したすべての端末には届かない。URI に `gr` パラメータを持つ（RFC 5627 3.1）。IBCF は、GRUU の Contact を届いたまま渡す（TS 24.229 5.10.5）。 |
| IMEI | International Mobile Equipment Identity | The identity of the device itself. It appears in the `+sip.instance` Contact parameter. | 端末そのものの識別子。Contact ヘッダの `+sip.instance` パラメータに入る。 |
| tel URI | tel URI | A URI that names a phone number, such as `tel:+<country code><number>`. The network must convert it to a SIP URI before routing. | 電話番号を表す URI（`tel:+<国番号><番号>` など）。ルーティングの前に、ネットワークが SIP URI に変換する。 |
| ISIM | IMS Subscriber Identity Module | The SIM application that holds the IMS identities and the key K. | IMS の識別子と鍵 K を保持する SIM のアプリケーション。 |

## Signaling / 信号

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| SIP | Session Initiation Protocol | The protocol that sets up, changes, and ends sessions. IMS uses it for all call control (RFC 3261). | セッションの確立、変更、終了を行うプロトコル。IMS はすべての呼制御に使う（RFC 3261）。 |
| SDP | Session Description Protocol | The format that describes media, such as codecs, addresses, and ports. It travels in SIP bodies. | コーデック、アドレス、ポートなどのメディアを記述する形式。SIP のボディで運ぶ。 |
| Transaction | SIP transaction | One request and all of its responses, managed with timers and retransmissions. | 1 つのリクエストと、それに対するすべての応答。タイマーと再送で管理する。 |
| Dialog | SIP dialog | A peer-to-peer relationship between two UAs that lasts for a call or a subscription. Call-ID and both tags identify it. | 通話や購読の間続く、2 つの UA の間の関係。Call-ID と双方のタグで識別する。 |
| PRACK | Provisional Response Acknowledgement | A request that confirms a reliable provisional response, such as 183 (RFC 3262). | 183 などの信頼性のある暫定応答を確認するリクエスト（RFC 3262）。 |
| Precondition | SIP precondition | A rule that the call alerts only after network resources, such as the voice bearer, are ready (RFC 3312). | 音声用ベアラなどのネットワーク資源が準備できてから呼び出しを始める仕組み（RFC 3312）。 |
| Session timer | SIP session timer | Periodic refreshes of a session with UPDATE or re-INVITE, so that a session whose end was lost does not stay forever (RFC 4028, Session-Expires). | 終わりを見失ったセッションが残り続けないように、UPDATE か re-INVITE でセッションを定期的に更新するしくみ（RFC 4028、Session-Expires）。 |
| Record-Route | Record-Route header | A header in which each proxy that wants to stay on the path of a dialog puts its URI. The UAs turn it into the route set of the dialog (RFC 3261 16.6, 12.1). | ダイアログの経路に残りたいプロキシが自分の URI を入れるヘッダ。UA はこれをダイアログのルートセットにする（RFC 3261 16.6、12.1）。 |
| P-Asserted-Identity | P-Asserted-Identity header | The identity of the caller as the network vouches for it. The P-CSCF puts it in; nodes inside the network trust it instead of From (RFC 3325). | 網が保証する発信者の識別子。P-CSCF が入れ、網の中のノードは From ではなくこれを信用する（RFC 3325）。 |
| RSeq / RAck | Response Sequence / Response Acknowledgment | RSeq numbers each reliable provisional response; the PRACK names the one it acknowledges in RAck, with the CSeq of the INVITE (RFC 3262 7.1, 7.2). | RSeq は信頼性のある暫定応答の通し番号。PRACK は確認する応答を、RSeq と INVITE の CSeq を入れた RAck で示す（RFC 3262 7.1、7.2）。 |
| Early dialog | Early dialog | The dialog that a provisional response with a To tag creates before the final answer. PRACK travels in it (RFC 3261 12.1, RFC 3262). | 最終応答の前に、To のタグ付きの暫定応答で作られるダイアログ。PRACK はこの中で送られる（RFC 3261 12.1、RFC 3262）。 |
| Path | Path header | A header that records the P-CSCF during registration, so the S-CSCF can reach the UE through it (RFC 3327). | 登録時に P-CSCF を記録するヘッダ。S-CSCF はこの経路で UE に届ける（RFC 3327）。 |
| SUBSCRIBE / NOTIFY | SIP event notification | SUBSCRIBE asks for the state of a resource and its changes; each NOTIFY reports the state. The subscription is a usage of a dialog (RFC 6665). | SUBSCRIBE で資源の状態とその変化を求め、NOTIFY で状態を知らせる。購読はダイアログの使い方の 1 つ（RFC 6665）。 |
| Reg event | Registration event package | The event package `reg`, in which the S-CSCF tells the UE and the P-CSCF the state of a registration. The body is reginfo (RFC 3680, TS 24.229 5.4.2.1). | S-CSCF が登録の状態を UE と P-CSCF に知らせるイベントパッケージ `reg`。本文は reginfo（RFC 3680、TS 24.229 5.4.2.1）。 |
| reginfo | Registration information document | The XML body of a reg event NOTIFY, of type `application/reginfo+xml`. It holds a registration for each identity and a contact for each binding (RFC 3680 5). | reg イベントの NOTIFY の XML の本文。型は `application/reginfo+xml`。識別子ごとの registration と、登録先ごとの contact を持つ（RFC 3680 5）。 |
| Service-Route | Service-Route header | A header that tells the UE which S-CSCF route to use for its requests (RFC 3608). | UE が送るリクエストの経路（S-CSCF）を UE に知らせるヘッダ（RFC 3608）。 |
| rport | Via `rport` parameter | A Via parameter that makes the server send the response to the address and port the request came from (RFC 3581). | リクエストが届いた送信元のアドレスとポートへ応答を返させる Via のパラメータ（RFC 3581）。 |
| iFC | Initial Filter Criteria | Rules in the user profile that tell the S-CSCF which requests to send to which AS. | どのリクエストをどの AS に送るかを S-CSCF に指示する、ユーザープロファイルの規則。 |
| SPT | Service Point Trigger | One condition of an iFC. It tests the method, a header, the Request-URI or the session case (TS 29.228 B.2.3). | iFC の条件の 1 つ。メソッド、ヘッダ、Request-URI、セッションケースのどれかを調べる（TS 29.228 B.2.3）。 |
| ODI | Original Dialog Identifier | A value that the S-CSCF puts in its own Route URI before it sends a request to an AS. When the AS sends the request back, the S-CSCF goes on with the next iFC by this value (TS 24.229 5.4.3.4). | S-CSCF が AS へリクエストを送る前に、自分の Route の URI に入れる値。AS がリクエストを送り返したとき、S-CSCF はこの値で続きの iFC から評価する（TS 24.229 5.4.3.4）。 |
| B2BUA | Back-to-Back User Agent | A node that answers a request as a UA and sends a new request of its own, so that it holds a dialog on each side. The MMTel AS is a routeing B2BUA (TS 24.229 5.7.5), and the IBCF of this project is one too (5.10.5). | 要求に UA として答え、自分の新しい要求を送るノード。両側にダイアログを 1 つずつ持つ。MMTel AS は routeing B2BUA である（TS 24.229 5.7.5）。このプロジェクトの IBCF も B2BUA である（5.10.5）。 |
| P-Served-User | P-Served-User header | The header in which the S-CSCF names the user whom an AS serves, with the session case and the registration state (RFC 5502, RFC 8498). | AS が担当する利用者を、session case と登録の状態とともに S-CSCF が示すヘッダ（RFC 5502、RFC 8498）。 |
| Third-party REGISTER | Third-party registration | A REGISTER that the S-CSCF sends to an AS by the iFCs when a user's registration succeeds, so that the AS learns the registration state (TS 24.229 5.4.1.7). | 利用者の登録が成功したとき、S-CSCF が iFC に従って AS へ送る REGISTER。AS はこれで登録の状態を知る（TS 24.229 5.4.1.7）。 |
| Feature tag | Media feature tag | A Contact parameter by which a UE tells the network what it can take, such as `+g.3gpp.smsip` for SMS over IP (TS 24.341 5.3.2.2). A request picks the contacts that have it with Accept-Contact (RFC 3841). | UE が受けられるものを網に伝える Contact のパラメータ。SMS over IP では `+g.3gpp.smsip`（TS 24.341 5.3.2.2）。リクエストは Accept-Contact で、このパラメータを持つ連絡先を選べる（RFC 3841）。 |
| ICSI | IMS Communication Service Identifier | A URN that names an IMS service. MMTel uses `urn:urn-7:3gpp-service.ims.icsi.mmtel`, which the UE puts in its Contact and in P-Preferred-Service (TS 24.173 5.1). | IMS のサービスを指す URN。MMTel は `urn:urn-7:3gpp-service.ims.icsi.mmtel` で、UE は Contact と P-Preferred-Service に入れる（TS 24.173 5.1）。 |
| P-Access-Network-Info | P-Access-Network-Info header | A header in which the UE tells the network its access type and cell, such as `3GPP-E-UTRAN-FDD` and the cell ID (TS 24.229 7.2A.4). | UE がアクセスの種類とセルを網に伝えるヘッダ。`3GPP-E-UTRAN-FDD` とセルの ID などを入れる（TS 24.229 7.2A.4）。 |
| History-Info | History-Info header | A header that records each change of the Request-URI, such as a call forwarding, with an index. The UE shows the forwarding user from it (RFC 7044, TS 24.604). | 着信転送などで Request-URI が変わるたびに、番号を付けて記録するヘッダ。UE はこれで転送元を表示する（RFC 7044、TS 24.604）。 |
| P-Charging-Vector | P-Charging-Vector header | The header that carries the charging identifiers. Its icid-value is the charging identity of the call; a routeing B2BUA keeps it, so every dialog of the call has it (TS 24.229 7.2A.5, 5.7.5.1). | 課金の識別子を運ぶヘッダ。icid-value は通話の課金の識別子で、routeing B2BUA もそのまま残すので、通話のどのダイアログにも同じ値が入る（TS 24.229 7.2A.5、5.7.5.1）。 |
| MESSAGE | SIP MESSAGE method | A SIP request that carries content outside a dialog (RFC 3428). In SMS over IP it carries the short messages and the reports. | ダイアログの外で内容を運ぶ SIP のリクエスト（RFC 3428）。SMS over IP では、ショートメッセージと各種の報告を運ぶ。 |
| Diameter | Diameter base protocol | The AAA protocol used between IMS nodes and the HSS or the PCRF (RFC 6733). | IMS のノードと HSS や PCRF の間で使う AAA プロトコル（RFC 6733）。 |
| AVP | Attribute-Value Pair | One field in a Diameter message. | Diameter メッセージの 1 つのフィールド。 |
| CER / CEA | Capabilities-Exchange-Request / Answer | The first Diameter exchange on a new connection. Peers tell each other which applications they support. | 新しい接続で最初に行う Diameter のやり取り。対応するアプリケーションを互いに伝える。 |
| DWR / DWA | Device-Watchdog-Request / Answer | A periodic Diameter exchange that checks that the peer is alive. | 相手が動いているかを定期的に確かめる Diameter のやり取り。 |
| Cx | Cx reference point | The Diameter interface between the I-CSCF or S-CSCF and the HSS (TS 29.228, TS 29.229). | I-CSCF または S-CSCF と HSS の間の Diameter インタフェース（TS 29.228、TS 29.229）。 |
| UAR / UAA | User-Authorization-Request / Answer | The Cx exchange in which the I-CSCF asks the HSS which S-CSCF to use at registration. | 登録時に、使う S-CSCF を I-CSCF が HSS に問い合わせる Cx のやり取り。 |
| MAR / MAA | Multimedia-Auth-Request / Answer | The Cx exchange in which the S-CSCF fetches authentication vectors from the HSS. | S-CSCF が HSS から認証ベクトルを取得する Cx のやり取り。 |
| SAR / SAA | Server-Assignment-Request / Answer | The Cx exchange in which the S-CSCF registers itself with the HSS and downloads the user profile. | S-CSCF が担当を HSS に登録し、ユーザープロファイルを取得する Cx のやり取り。 |
| LIR / LIA | Location-Info-Request / Answer | The Cx exchange in which the I-CSCF finds the S-CSCF that serves a called user. | 着信先の加入者を担当する S-CSCF を I-CSCF が調べる Cx のやり取り。 |
| RTR / RTA | Registration-Termination-Request / Answer | The Cx exchange in which the HSS tells the S-CSCF to deregister a user, for example after the subscription is removed. | 加入の削除などのとき、利用者の登録を解除するよう HSS が S-CSCF に伝える Cx のやり取り。 |
| Sh | Sh reference point | The Diameter interface between an AS and the HSS (TS 29.328, TS 29.329). | AS と HSS の間の Diameter インタフェース（TS 29.328、TS 29.329）。 |
| UDR / UDA | User-Data-Request / Answer | The Sh exchange in which an AS reads subscriber data from the HSS. The IP-SM-GW uses it to find the IMPUs of an MSISDN. | AS が HSS から加入者のデータを読む Sh のやり取り。IP-SM-GW は MSISDN から IMPU を引くときに使う。 |
| PUR / PUA | Profile-Update-Request / Answer | The Sh exchange in which an AS updates subscriber data in the HSS. The IP-SM-GW uses it to register itself with the HSS for a subscriber's SMS. | AS が HSS にある加入者のデータを更新する Sh のやり取り。IP-SM-GW はこれを使い、加入者の SMS を受け持つことを HSS に登録する。 |
| Rx | Rx reference point | The Diameter interface between the P-CSCF and the PCRF (TS 29.214). | P-CSCF と PCRF の間の Diameter インタフェース（TS 29.214）。 |
| AAR / AAA | AA-Request / AA-Answer | The Rx exchange in which the P-CSCF gives the PCRF the media of a session, or asks to be told about the signaling path (TS 29.214 4.4.1, 4.4.5). | P-CSCF が PCRF にセッションのメディアを伝える、または信号の経路の状態の通知を申し込む Rx のやり取り（TS 29.214 4.4.1、4.4.5）。 |
| RAR / RAA | Re-Auth-Request / Re-Auth-Answer | The Rx exchange in which the PCRF tells the P-CSCF that some flows of a session were stopped (TS 29.214 4.4.6.2). | PCRF がセッションのフローの一部が止まったことを P-CSCF に知らせる Rx のやり取り（TS 29.214 4.4.6.2）。 |
| STR / STA | Session-Termination-Request / Session-Termination-Answer | The Rx exchange with which the P-CSCF ends an Rx session, such as when a call ends (TS 29.214 4.4.4). | 通話が終わったときなどに、P-CSCF が Rx のセッションを終えるやり取り（TS 29.214 4.4.4）。 |
| ASR / ASA | Abort-Session-Request / Abort-Session-Answer | The Rx exchange in which the PCRF ends an Rx session, for example because the voice bearer was lost (TS 29.214 4.4.6.1, 4.4.6.2). | 音声の bearer を失ったときなどに、PCRF が Rx のセッションを終えるやり取り（TS 29.214 4.4.6.1、4.4.6.2）。 |
| Gx | Gx reference point | The Diameter interface between the PCRF and the PCEF (TS 29.212). In this project it is a function call inside the EPC stub. | PCRF と PCEF の間の Diameter インタフェース（TS 29.212）。このプロジェクトでは EPC スタブの中の関数の呼び出しにする。 |
| Mp | Mp reference point | The interface between the MRFC and the MRFP. It uses H.248 (TS 23.333, TS 29.333). | MRFC と MRFP の間のインタフェース。H.248 を使う（TS 23.333、TS 29.333）。 |
| Mr / Mr' | Mr and Mr' reference points | The SIP interfaces to the MRFC: Mr from the S-CSCF, Mr' straight from an AS. In this project the MMTel AS sends its INVITEs to the MRFC over Mr' (TS 23.218). | MRFC への SIP のインタフェース。Mr は S-CSCF から、Mr' は AS から直接つながる。このプロジェクトでは MMTel AS が Mr' で MRFC に INVITE を送る（TS 23.218）。 |
| Cr | Cr reference point | The interface between an AS and the MRFC for fetching documents and media and for media control commands (TS 23.218). This project has no Cr: the MRFP plays announcements that it has provisioned. | AS と MRFC の間で、文書や音声を取ってきたり、メディア制御の命令を交わしたりするインタフェース（TS 23.218）。このプロジェクトには Cr が無く、MRFP は用意してある音声だけを流す。 |
| H.248 / Megaco | ITU-T H.248.1 Gateway control protocol | The protocol with which a controller drives a media gateway, built on contexts and terminations. The IETF version was called Megaco (RFC 3525, now Historic). 3GPP uses it on Iq, Ix and Mp. This project controls its media nodes with a simpler HTTP API of its own. | 制御する側がメディアゲートウェイを操作するプロトコル。コンテキストとターミネーションの考え方を使う。IETF 版の名前は Megaco（RFC 3525、今は Historic）。3GPP は Iq、Ix、Mp で使う。このプロジェクトは、独自の簡単な HTTP の API でメディアのノードを制御する。 |
| XCAP / Ut | XML Configuration Access Protocol / Ut reference point | The HTTP-based protocol with which the UE reads and changes its supplementary service settings in the AS. It runs over the Ut reference point (TS 24.623). | UE が AS にある補足サービスの設定を読み書きする、HTTP を使うプロトコル。Ut 参照点で使う（TS 24.623）。 |
| XUI | XCAP User Identifier | The identity that names the user's documents in XCAP URIs. It is the default IMPU from the P-Associated-URI of the 200 to REGISTER (GSMA IR.92 2.3.2). | XCAP の URI で利用者の文書を指す識別子。REGISTER の 200 の P-Associated-URI にある既定の IMPU を使う（GSMA IR.92 2.3.2）。 |
| simservs | Supplementary services document | The XML document of a user's supplementary service settings that the UE reads and writes over XCAP (TS 24.623 6.2). | UE が XCAP で読み書きする、利用者の補足サービスの設定の XML 文書（TS 24.623 6.2）。 |
| ETag | Entity tag | The version of a document in HTTP. An XCAP client sends it back in If-Match, so that a change applies only to the version it read (RFC 4825 7.11). | HTTP での文書の版。XCAP のクライアントは If-Match に入れて返し、読んだ版にだけ変更を当てる（RFC 4825 7.11）。 |
| netann | Basic network media services with SIP | A way for an AS to ask a media server for a service by the user part of the Request-URI: `annc` plays an announcement, `conf=` joins a conference (RFC 4240). The MMTel AS drives the MRFC with it. | AS が Request-URI の user part でメディアサーバーにサービスを頼む方法。`annc` は音声ガイダンス、`conf=` は会議（RFC 4240）。MMTel AS はこれで MRFC を動かす。 |
| Iq | Iq reference point | The interface between the IMS-ALG and the IMS-AGW. It uses H.248 (TS 23.334). This project uses a simpler API of its own instead. | IMS-ALG と IMS-AGW の間のインタフェース。H.248 を使う（TS 23.334）。このプロジェクトでは、独自の簡単な API で代用する。 |
| ENUM | E.164 Number Mapping | A DNS-based lookup that converts a phone number into a SIP URI. The digits are reversed into a domain name and queried for NAPTR records (RFC 6116). | 電話番号を SIP URI に変換する、DNS を使った検索。番号の桁を逆順にしたドメイン名で NAPTR レコードを引く（RFC 6116）。 |
| II-NNI | Inter-IMS Network to Network Interface | The interface between the IMS networks of two operators. It is made of the Ici and the Izi reference points (TS 29.165 5.1). | 2 つの事業者の IMS 網のあいだのインタフェース。Ici と Izi の 2 つの参照点からなる（TS 29.165 5.1）。 |
| Ici / Izi | Ici and Izi reference points | Ici is between the IBCFs of two networks and carries SIP; Izi is between their TrGWs and carries the media (TS 29.165 3.2). | Ici は 2 つの網の IBCF のあいだで SIP を、Izi は TrGW のあいだでメディアを運ぶ（TS 29.165 3.2）。 |
| Mx / Ix | Mx and Ix reference points | Mx is between a CSCF and the IBCF of the same network; Ix is between the IBCF and its TrGW (TS 29.165 3.2). | Mx は同じ網の CSCF と IBCF のあいだ、Ix は IBCF と TrGW のあいだの参照点（TS 29.165 3.2）。 |
| Exit point / Entry point | IBCF as an exit point or an entry point | The IBCF is the exit point of a request that leaves the home network and the entry point of one that comes in (TS 24.229 5.10.2, 5.10.3). | IBCF は、自網から出る要求の出口であり、入ってくる要求の入口である（TS 24.229 5.10.2、5.10.3）。 |
| THIG | Network topology hiding | The IBCF function that hides the home network's entries in Via, Route, Record-Route, Path and Service-Route by sealing them into tokens with a tokenized-by parameter (TS 24.229 5.10.4), for an IBCF that passes requests on as a proxy. The IBCF of this project is a B2BUA, and these entries do not cross it at all. | Via、Route、Record-Route、Path、Service-Route にある自網の値を、tokenized-by を付けたトークンに封じて隠す IBCF の機能（TS 24.229 5.10.4）。要求をプロキシとして通す IBCF が使う。このプロジェクトの IBCF は B2BUA なので、こうした値はそもそも境界を越えない。 |
| Trust domain | Trust domain | The nodes that trust one another with header fields such as P-Asserted-Identity. A peer network is in it only by agreement; at the boundary the header fields are removed (TS 24.229 4.4). | P-Asserted-Identity などのヘッダを互いに信頼するノードの範囲。相手網は取り決めがあるときだけ入る。境界ではそのヘッダを消す（TS 24.229 4.4）。 |
| Interconnect profile | Interconnect profile | This project's name for the agreement with a peer network: trust, topology hiding, transports, methods, option tags, header rules, number format and codecs. TS 29.165 Annex C lists such choices as option items. | 相手網との取り決めを表す、このプロジェクトの呼び方。信頼、構成の秘匿、トランスポート、メソッド、option tag、ヘッダの規則、番号の形式、コーデックを決める。TS 29.165 の Annex C は、こうした選択を選択項目として並べている。 |
| NAPTR / SRV | DNS NAPTR and SRV records | DNS records that tell a SIP node which transport, host, and port to use for a domain (RFC 3263). | ドメインに対して使うトランスポート、ホスト、ポートを SIP のノードに教える DNS のレコード（RFC 3263）。 |

## Security / セキュリティ

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| AKA | Authentication and Key Agreement | Mutual authentication between the UE and the network, based on a key K shared by the HSS and the ISIM, or the USIM of a card without an ISIM (TS 33.203, TS 24.229 5.1.1.1A). | HSS と ISIM（ISIM の無いカードでは USIM）が共有する鍵 K に基づく、UE とネットワークの相互認証（TS 33.203、TS 24.229 5.1.1.1A）。 |
| Milenage | MILENAGE algorithm set | The algorithms that compute AKA values from K and OP or OPc (TS 35.206). TS 35.208 gives test data. | K と OP または OPc から AKA の値を計算するアルゴリズム（TS 35.206）。試験用データは TS 35.208 にある。 |
| Digest | HTTP Digest authentication | Password-based challenge and response authentication. IMS AKA reuses its format as AKAv1-MD5 (RFC 3310). | パスワードに基づくチャレンジとレスポンスの認証。IMS-AKA は AKAv1-MD5 としてこの形式を流用する（RFC 3310）。 |
| H(A1) | Hash of A1 | The MD5 hash of username, realm and password in Digest authentication (RFC 2617 3.2.2.2). The HSS gives it to the S-CSCF instead of the password. | Digest 認証で、ユーザー名、レルム、パスワードをつないだ文字列の MD5 ハッシュ（RFC 2617 3.2.2.2）。HSS はパスワードの代わりにこれを S-CSCF に渡す。 |
| Nonce | Number used once | The random value a server puts in a Digest challenge. The client's response is computed from it, so an old response cannot be replayed. | Digest の認証の要求にサーバーが入れる乱数。クライアントの応答はこの値から計算するので、古い応答を使い回せない。 |
| AV | Authentication Vector | The set of RAND, XRES, CK, IK and AUTN that the HSS makes for one AKA run. Each AV is used only once (TS 33.203 6.1.1). | 1 回の AKA のために HSS が作る RAND、XRES、CK、IK、AUTN の組。1 つの AV は 1 回だけ使う（TS 33.203 6.1.1）。 |
| RAND | Random challenge | The random number in an AV that the network sends to the UE. | AV に入っている乱数。網が UE に送る。 |
| AUTN | Authentication Token | The value that lets the UE check that the challenge came from its home network and is fresh (TS 33.102). | 認証の要求が自分のホーム網から来た新しいものかを、UE が確かめるための値（TS 33.102）。 |
| RES / XRES | Response / Expected Response | RES is the UE's answer to RAND; XRES is the value the S-CSCF expects. In IMS the RES is used as the Digest password (RFC 3310). | RES は RAND に対する UE の答えで、XRES は S-CSCF が期待する値。IMS では RES を Digest のパスワードとして使う（RFC 3310）。 |
| CK / IK | Cipher Key / Integrity Key | The keys that AKA produces. The S-CSCF passes them to the P-CSCF in the 401, and the P-CSCF uses them for the IPsec SAs (TS 33.203). | AKA で得られる鍵。S-CSCF が 401 で P-CSCF に渡し、P-CSCF は IPsec の SA に使う（TS 33.203）。 |
| SQN | Sequence Number | A counter that the HSS and the USIM keep so that the UE can reject an old AV (TS 33.102 C.1, C.2). | 古い AV を UE が断れるように、HSS と USIM が持つ通し番号（TS 33.102 C.1、C.2）。 |
| AUTS | Resynchronization Token | The value the UE sends when the SQN in the AUTN is out of range. The HSS uses it to set its SQN again (TS 33.102 6.3.3, 6.3.5). | AUTN の SQN が範囲の外のときに UE が送る値。HSS はこれで自分の SQN を合わせ直す（TS 33.102 6.3.3、6.3.5）。 |
| OP / OPc | Operator Variant Algorithm Configuration Field | An operator value that Milenage mixes with K. OPc is derived from OP and K (TS 35.206). | Milenage が K と組み合わせる事業者の値。OPc は OP と K から作る（TS 35.206）。 |
| IP association | IP association | The P-CSCF's record that requests from one address and port belong to an authenticated user, for SIP Digest without TLS (TS 24.229 5.2.2.3). | TLS を使わない SIP Digest で、あるアドレスとポートからの要求が認証済みの利用者のものだという P-CSCF の記録（TS 24.229 5.2.2.3）。 |
| sec-agree | Security mechanism agreement | The SIP procedure in which the UE and the P-CSCF agree on IPsec parameters (RFC 3329). | UE と P-CSCF が IPsec のパラメータを合意する SIP の手順（RFC 3329）。 |
| SA | Security Association | One direction of IPsec protection between the UE and the P-CSCF. They set up two pairs of SAs (TS 33.203 7.1). | UE と P-CSCF のあいだの、一方向の IPsec の保護。2 組の SA を作る（TS 33.203 7.1）。 |
| Protected port | Protected server port and protected client port | The ports of the UE and the P-CSCF that the security associations cover. A UE with IMS AKA sends everything after the challenge to the P-CSCF's protected server port (TS 33.203 7.1). This project agrees on both and uses only the server ports, without ESP. | セキュリティアソシエーションが保護する、UE と P-CSCF のポート。IMS-AKA の UE は challenge のあとのすべてを P-CSCF の保護サーバーポートに送る（TS 33.203 7.1）。このプロジェクトは両方を取り決め、ESP なしでサーバーポートだけを使う。 |
| SPI | Security Parameter Index | The number that names an SA. The UE and the P-CSCF exchange it in the Security-Client and Security-Server headers (TS 33.203 Annex H). | SA を指す番号。UE と P-CSCF は Security-Client と Security-Server のヘッダで伝え合う（TS 33.203 Annex H）。 |
| IPsec | IP security | Encryption and integrity protection between the UE and the P-CSCF. It is off by default in this project. | UE と P-CSCF の間の暗号化と完全性保護。このプロジェクトでは既定で無効にする。 |

## Media / メディア

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| RTP | Real-time Transport Protocol | The protocol that carries voice packets (RFC 3550). | 音声のパケットを運ぶプロトコル（RFC 3550）。 |
| RTCP | RTP Control Protocol | The companion protocol that reports reception quality for RTP. | RTP の受信品質を報告するプロトコル。 |
| Symmetric RTP | Symmetric RTP | Sending and receiving RTP on the same port, which lets a relay answer to the address it sees (RFC 4961). | RTP の送信と受信に同じポートを使うこと。中継ノードは見えた送信元へ返せる（RFC 4961）。 |
| Latching | Media latching | A relay sends RTP back to the source address of the first packet it receives, instead of the address in SDP. | 中継ノードが、SDP のアドレスではなく、最初に受け取った RTP の送信元へ返送すること。 |
| G.711 | ITU-T G.711 (PCMU, PCMA) | A simple voice codec. Wireshark can play it back directly. | 単純な音声コーデック。Wireshark でそのまま再生できる。 |
| AMR-WB | Adaptive Multi-Rate Wideband | The wideband voice codec that VoLTE commonly uses. | VoLTE でよく使う広帯域の音声コーデック。 |
| AMR | Adaptive Multi-Rate | The narrowband voice codec with eight modes that GSMA IR.92 requires with AMR-WB. This project sends pre-encoded frames. | 8 つのモードを持つ狭帯域の音声コーデック。GSMA IR.92 が AMR-WB とともに必須にしている。このプロジェクトでは符号化済みのフレームを送る。 |
| CMR | Codec Mode Request | A field of the AMR and AMR-WB RTP payload in which the receiver asks the sender for a mode (RFC 4867). | AMR と AMR-WB の RTP ペイロードの欄。受ける側が送る側にモードを求める（RFC 4867）。 |
| DTMF | Dual-Tone Multi-Frequency | The tones of the keypad digits. VoLTE sends them as telephone-event RTP packets (RFC 4733, TS 26.114 Annex G). | 番号のボタンの音。VoLTE では telephone-event の RTP で送る（RFC 4733、TS 26.114 Annex G）。 |

## Services and tools / サービスとツール

| Term | Full name | English | 日本語 |
| --- | --- | --- | --- |
| IMS | IP Multimedia Subsystem | The 3GPP architecture that delivers voice, video, and messaging over IP (TS 23.228). | IP で音声、映像、メッセージを提供する 3GPP のアーキテクチャ（TS 23.228）。 |
| 3GPP | 3rd Generation Partnership Project | The body that writes the mobile network specifications, including IMS. | IMS を含む移動通信の仕様を作る団体。 |
| MMTel | Multimedia Telephony | The IMS telephony service, including supplementary services (TS 24.173). | 補足サービスを含む IMS の電話サービス（TS 24.173）。 |
| OIP / OIR | Originating Identification Presentation / Restriction | Showing the caller's identity to the callee, and the caller withholding it (TS 24.607). | 発信者の識別子を着信者に通知するサービスと、発信者がそれを隠すサービス（TS 24.607）。 |
| TIP / TIR | Terminating Identification Presentation / Restriction | Showing the caller who answered, and the callee withholding it (TS 24.608). | 応答した相手の識別子を発信者に通知するサービスと、着信者がそれを隠すサービス（TS 24.608）。 |
| CDIV | Communication Diversion | Call forwarding: CFU (unconditional), CFB (busy), CFNR (no reply), CFNRc (not reachable) and CFNL (not logged in) (TS 24.604). | 着信転送。無条件の CFU、話し中の CFB、無応答の CFNR、届かないときの CFNRc、未登録の CFNL がある（TS 24.604）。 |
| CB | Communication Barring | Barring calls by the user's rules: incoming (ICB, such as BAIC and ACR) and outgoing (OCB, such as BAOC, BOIC and BOIC-exHC) (TS 24.611). | 利用者の規則で通話を規制するサービス。着信の規制（ICB。BAIC や ACR など）と発信の規制（OCB。BAOC、BOIC、BOIC-exHC など）がある（TS 24.611）。 |
| ACR | Anonymous Communication Rejection | Rejecting calls from callers who withhold their identity, with 433 (TS 24.611, RFC 5079). | 識別子を隠した発信者からの着信を 433 で断るサービス（TS 24.611、RFC 5079）。 |
| CW | Communication Waiting | Telling the user of a second call during a call. GSMA IR.92 asks the UE to do it (TS 24.615, GSMA IR.92 2.3.4). | 通話中に 2 本目の着信を知らせるサービス。GSMA IR.92 は UE で行うよう求めている（TS 24.615、GSMA IR.92 2.3.4）。 |
| HOLD | Communication Hold | Holding a call with a re-INVITE whose SDP is sendonly or inactive (TS 24.610). | SDP が sendonly か inactive の re-INVITE で、通話を保留するサービス（TS 24.610）。 |
| CAT | Customized Alerting Tones | A service in which the called user chooses the tone that the caller hears while the call is alerting. The network plays it as early media (TS 24.182). | 着信側の利用者が選んだ音を、呼び出し中に発信者に聞かせるサービス。網が early media で流す（TS 24.182）。 |
| Early media | Early media | Media that flows before the call is answered, such as a ringing tone or an announcement from the network. P-Early-Media tells the caller which early dialog the network authorizes it for (RFC 5009). | 通話に応答する前に流れるメディア。網からの呼び出し音や音声ガイダンスなど。どの early dialog の early media を網が認めたかは P-Early-Media で伝える（RFC 5009）。 |
| Conference focus | Conference focus | The SIP user agent that holds a dialog with each participant of a conference and keeps its state (RFC 4353, TS 24.147). In this project the MMTel AS is the focus, and the MRFC and the MRFP mix the media. | 会議の参加者それぞれと dialog を持ち、会議の状態を管理する SIP の UA（RFC 4353、TS 24.147）。このプロジェクトでは MMTel AS がフォーカスになり、メディアは MRFC と MRFP が混ぜる。 |
| Conference factory URI | Conference factory URI | The PSI to which a UE sends an INVITE to create a conference. Without a configured one the UE uses `sip:mmtel@conf-factory.<home domain>` (TS 23.003 13.10, GSMA IR.92 2.3.3). | 会議を作るために UE が INVITE を送る PSI。設定が無ければ `sip:mmtel@conf-factory.<ホーム網のドメイン>` を使う（TS 23.003 13.10、GSMA IR.92 2.3.3）。 |
| Conference URI | Conference URI | The PSI of one conference, which the focus allocates and gives in its Contact with `isfocus` (TS 24.147 5.3.2.3.1). Participants subscribe and refer to it. Here it is `sip:conf-<id>@conf.<home domain>`, which DNS resolves to the AS that hosts the conference. | フォーカスが割り当て、`isfocus` を付けた Contact で知らせる、1 つの会議の PSI（TS 24.147 5.3.2.3.1）。参加者はこれを購読し、REFER の宛先にする。このプロジェクトでは `sip:conf-<id>@conf.<ホーム網のドメイン>` で、DNS がその会議を受け持つ AS に向ける。 |
| Conference event package | Conference event package | The SIP event package with which the focus tells its subscribers who takes part in a conference, as conference-info XML in NOTIFYs (RFC 4575). | 会議に誰が参加しているかを、フォーカスが購読者に NOTIFY の conference-info の XML で伝える SIP のイベントパッケージ（RFC 4575）。 |
| SMS over IP | SMS over IP | Sending SMS in SIP MESSAGE requests with the content type `application/vnd.3gpp.sms` (TS 23.204, TS 24.341). | SMS を SIP の MESSAGE リクエストに入れ、コンテンツタイプ `application/vnd.3gpp.sms` で送る方式（TS 23.204、TS 24.341）。 |
| RP | Short Message Relay Layer | The outer layer of the body of SMS over IP. RP-DATA carries a short message, RP-ACK and RP-ERROR answer it, and RP-SMMA tells that memory is free again (TS 24.011). | SMS over IP のボディの外側の層。RP-DATA がショートメッセージを運び、RP-ACK と RP-ERROR がそれに答え、RP-SMMA はメモリに空きができたことを伝える（TS 24.011）。 |
| TPDU | Transfer Protocol Data Unit | The PDU of the transfer layer inside RP-DATA, RP-ACK or RP-ERROR: SMS-SUBMIT, SMS-DELIVER and the reports (TS 23.040). | RP-DATA、RP-ACK、RP-ERROR の中に入る転送層の PDU。SMS-SUBMIT、SMS-DELIVER と各種の報告がある（TS 23.040）。 |
| SMS-SUBMIT / SMS-DELIVER | SMS-SUBMIT / SMS-DELIVER | The TPDU of a short message from the UE to the SC, and the one from the SC to the UE (TS 23.040 9.2.2.2, 9.2.2.1). | UE から SC へ送るショートメッセージの TPDU と、SC から UE へ届けるショートメッセージの TPDU（TS 23.040 9.2.2.2、9.2.2.1）。 |
| Submit report / delivery report / status report | SMS-SUBMIT-REPORT, SMS-DELIVER-REPORT, SMS-STATUS-REPORT | The SC's answer to an SMS-SUBMIT, the recipient UE's answer to an SMS-DELIVER, and the SC's report to the sender of whether the message reached the recipient (TS 23.040 9.2.2.2a, 9.2.2.1a, 9.2.2.3). | SMS-SUBMIT に対する SC の答え、SMS-DELIVER に対する受信側の UE の答え、メッセージが受信者に届いたかを SC が送信者に知らせる報告（TS 23.040 9.2.2.2a、9.2.2.1a、9.2.2.3）。 |
| HEP | Homer Encapsulation Protocol | A protocol that copies the signaling each node sends and receives to a collector. | 各ノードが送受信した信号を収集サーバーへ複製するプロトコル。 |
| pcap | Packet capture file | A file of captured packets that Wireshark opens. | Wireshark で開くパケットキャプチャのファイル。 |
| SIPp | SIPp | An open-source SIP test tool driven by XML scenarios. This project uses it as an independent test peer. | XML のシナリオで動く、オープンソースの SIP 試験ツール。このプロジェクトでは独立した試験相手として使う。 |
