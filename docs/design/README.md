# 設計の記録

このディレクトリは、このプロジェクトがなぜこう作ったかを、主題ごとにまとめたものです。何を作るかは [requirements.md](../requirements.md)、何ができていて何をこれから作るかは [roadmap.md](../roadmap.md) にあります。設計を変えたときは、該当する記録を書き換えます。前の設計と変えた理由は、Git の履歴で追えます。

| 記録 | 決めていること | 大事な理由 |
| --- | --- | --- |
| [implementation.md](implementation.md) | Go で書くこと。SIP の解析、トランザクション層、ダイアログ層を自作し、Diameter の符号化には go-diameter を使うこと。SIP のスタックのループで TCP の送信を待たないこと。最初の INVITE を送る側と同じく、re-INVITE を送る側も信頼できる暫定応答に PRACK を返すこと | IMS の手順が SIP のどの層の動きなのかを読めるようにするため。1 つの相手を待つあいだに、役割全体を止めないため。re-INVITE の Supported に 100rel を挙げる UA は PRACK を返さなければならないため |
| [ue-simulator.md](ue-simulator.md) | UE シミュレータを自作し、SIPp と pjsua を独立した試験相手にすること | sec-agree を含む IMS-AKA の全体を見せ、UE と CSCF に同じ誤解が入るのを防ぐため |
| [media-control.md](media-control.md) | メディア中継ノードを独自の API と H.248 で制御すること。P-CSCF の通過ごとのコンテキスト。フォークした INVITE の別のダイアログから届き、中継ノードに通せない 2xx と、そのダイアログの中の要求の扱い。網の境界の装置の振る舞い | 通話の基本を先に学べるようにし、実際の網と同じ形で中継を見せるため |
| [environment-capture.md](environment-capture.md) | ホストとコンテナのあいだを NAT 越しとして扱うこと。ブリッジ全体を 1 つのコンテナでキャプチャすること | ホストのソフトフォンとつなぎ、すべての信号を 1 つの pcap で見るため |
| [codecs.md](codecs.md) | G.711 から始め、AMR と AMR-WB を符号化済みのフレームで運ぶこと | IR.92 と NG.114 の必須のコーデックに従い、製品に cgo を入れないため |
| [volte-access.md](volte-access.md) | 最初のアクセス網を EPC にすること。EPC と PCRF のスタブ。既定では ESP をかけず、上書きの構成で ESP（暗号化は null）をかける IMS-AKA。AKAv1-MD5 | VoLTE の手順（ベアラ、Rx、sec-agree）をキャプチャで見せるため |
| [lab-oam.md](lab-oam.md) | 学習用の画面の作りと、学習用の画面と OAM を分けること | 学習の道具と保守の装置の受け持ちを分けるため |
| [sms.md](sms.md) | IP-SM-GW の役割 `ipsmgw` と SC の役割 `sc` で SMS を運ぶこと。2 つのあいだは SGd で、SC を置かない構成では IP-SM-GW の中の SC のスタブを使うこと | 通話の着信と同じ流れで SMS を届け、IP-SM-GW から見た SC を境界の後ろで差し替えられるようにするため |
| [mmtel-mrf.md](mmtel-mrf.md) | MMTel AS を routeing B2BUA として作り、MRF を自作すること | IR.92 が必須にする付加サービスの信号を見せるため |
| [state-redundancy.md](state-redundancy.md) | 状態を `Store` の後ろに置き、冗長構成では外部の保存先に置くこと | 各役割を複数台で動かし、ノードを作り直しても登録と呼を保つため |
| [hss-provisioning.md](hss-provisioning.md) | HSS の加入者のプロビジョニングの API。1 件ずつの操作、ページに分けた一覧と書き出し、一括の登録、試験用の利用者の印、サービスのプロファイルの選び方、収容の方針 | 加入者のデータを OAM からまとめて扱い、機能を試験用の利用者だけに有効にするため |
| [provisioning-northbound.md](provisioning-northbound.md) | BSS から加入者を入れる口を、標準に寄せずに独自の API のままにすること | 標準にある形（SuM の Itf-N に流す Bulk CM の XML）が、この実装の運ぶ長期の鍵、OPc、SIP Digest のパスワードを持たないため |
| [fm-northbound.md](fm-northbound.md) | OSS に警報を渡す口を、TS 28.111 の故障管理と TS 28.532 の ProvMnS の REST の形で作ること。警報の一覧の読み出し、確認、`NtfSubscriptionControl` による購読と通知。通知は、ノードごとのリースを持つ OAM のインスタンスが送ること | 標準の Itf-N の形で OSS につなぎ、OAM を複数のインスタンスで動かしても、1 台のノードの通知を 1 か所から順に送るため |
| [b2bua.md](b2bua.md) | IBCF と MMTel AS が 2 つのレグを中継する部分を `internal/b2bua` にまとめ、ダイアログの中の要求を届いた順に中継すること | 同じ直しを 2 か所に入れ続けずに済み、オファーとアンサーの順を崩さないため |
| [slides.md](slides.md) | スライドの元データに、ノードとイベントのモデルと、段階で進むシーンを書けるようにしたこと。HTML は段階を進める舞台、pptx はチェックポイントごとの静止のページにすること | 信号を 1 区間ずつ追い、ノードの判断を図で見せるため。動かない pptx でも同じことを読めるようにするため |
| [openapi.md](openapi.md) | 独自の HTTP の API を API ごとの OpenAPI 3.1 で書き、試験でハンドラーと突き合わせること。スキーマの検査に santhosh-tekuri/jsonschema を使うこと。3GPP の OpenAPI に従う API の示し方 | 文書とハンドラーの食い違いを試験で見つけ、どの API が標準に従うかを見分けられるようにするため |
