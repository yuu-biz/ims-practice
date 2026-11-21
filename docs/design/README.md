# 設計の記録

このディレクトリは、このプロジェクトがなぜこう作ったかを、主題ごとにまとめたものです。何を作るかは [requirements.md](../requirements.md)、何ができていて何をこれから作るかは [roadmap.md](../roadmap.md) にあります。設計を変えたときは、該当する記録を書き換えます。前の設計と変えた理由は、Git の履歴で追えます。

| 記録 | 決めていること | 大事な理由 |
| --- | --- | --- |
| [implementation.md](implementation.md) | Go で書くこと。SIP の解析、トランザクション層、ダイアログ層を自作し、Diameter の符号化には go-diameter を使うこと | IMS の手順が SIP のどの層の動きなのかを読めるようにするため |
| [ue-simulator.md](ue-simulator.md) | UE シミュレータを自作し、SIPp と pjsua を独立した試験相手にすること | sec-agree を含む IMS-AKA の全体を見せ、UE と CSCF に同じ誤解が入るのを防ぐため |
| [media-control.md](media-control.md) | メディア中継ノードを独自の API と H.248 で制御すること。P-CSCF の通過ごとのコンテキスト。網の境界の装置の振る舞い | 通話の基本を先に学べるようにし、実際の網と同じ形で中継を見せるため |
| [environment-capture.md](environment-capture.md) | ホストとコンテナのあいだを NAT 越しとして扱うこと。ブリッジ全体を 1 つのコンテナでキャプチャすること | ホストのソフトフォンとつなぎ、すべての信号を 1 つの pcap で見るため |
| [codecs.md](codecs.md) | G.711 から始め、AMR と AMR-WB を符号化済みのフレームで運ぶこと | IR.92 と NG.114 の必須のコーデックに従い、製品に cgo を入れないため |
| [volte-access.md](volte-access.md) | EPC と PCRF のスタブ、ESP をかけない IMS-AKA、AKAv1-MD5 | VoLTE の手順（ベアラ、Rx、sec-agree）をキャプチャで見せるため |
| [lab-oam.md](lab-oam.md) | 学習用の画面の作りと、学習用の画面と OAM を分けること | 学習の道具と保守の装置の受け持ちを分けるため |
| [sms.md](sms.md) | IP-SM-GW の役割と、その中の SC のスタブで SMS を運ぶこと | 通話の着信と同じ流れで SMS を届け、SC をあとで差し替えられるようにするため |
| [mmtel-mrf.md](mmtel-mrf.md) | MMTel AS を routeing B2BUA として作り、MRF を自作すること | IR.92 が必須にする補足サービスの信号を見せるため |
| [state-redundancy.md](state-redundancy.md) | 状態を `Store` の後ろに置き、冗長構成では外部の保存先に置くこと | 各役割を複数台で動かし、ノードを作り直しても登録と呼を保つため |
