# VoLTE のアクセス網

この記録は VoLTE の手順を見せるためのアクセス網（EPC と PCRF のスタブ）と、IMS-AKA の扱いの設計です。調査の記録は [volte.md](../background/volte.md) にあります。

## 選んだこと

- EPC と PCRF はスタブにし、役割 `epc` に置きます。
- UE と EPC スタブのあいだは TS 24.301 の ESM のメッセージに合わせた HTTP の API にします。
- UE の IP アドレスは加入者ごとの静的なアドレスにします。
- IMS-AKA は sec-agree と保護ポートの手順を行い、ESP だけをかけません。
- AKA の版は AKAv1-MD5（RFC 3310）だけにします。
- P-CSCF と PCRF のあいだは Diameter の Rx（TS 29.214）です。

| 主題 | 作り |
| --- | --- |
| UE と EPC スタブの境界 | UE の側に `AccessNetwork` のインタフェースを置き、EPC スタブへの HTTP のクライアントで実装します。メッセージの名前と項目は TS 24.301 の ESM のメッセージに合わせ、本文は JSON にします。bearer の活性化や解放のような網からの通知は、Server-Sent Events で送ります。NAS と S1 を省いたことを `SIMPLIFIED:` に書きます。SMS を NAS で届けるときは、同じインタフェースの後ろに NAS の実装を置きます。スタブを、インタフェースの境界で本格的な実装に差し替えられるようにするためです。 |
| UE の IP アドレス | 加入者ごとの静的なアドレス（TS 23.401 5.3.1.1）にし、今の 1 つのブリッジ（ims0）に置きます。TUN は使いません。TUN はコンテナに NET_ADMIN が要るので、Open5GS と UERANSIM の構成で扱います。1 つのブリッジのままなら、キャプチャ（[environment-capture.md](environment-capture.md)）を変えずに済み、ホスト固有のネットワーク設定にも依りません。GTP-U と SGi を省いたことを `SIMPLIFIED:` に書きます。 |
| IPsec | ESP をかける設定は作っていません。既定は無効で、IPsec を有効にする構成は拡張の候補です（[roadmap.md](../roadmap.md)）。P-CSCF と UE のコンテナに NET_ADMIN が要り、Windows の Docker Desktop で xfrm が使えるかも確かめていないためです。 |
| 平文の AKA | sec-agree（Security-Client、Security-Server、Security-Verify）と保護ポートの手順はそのまま行い、ESP だけを省きます。学習者がキャプチャで sec-agree の手順を見られるようにするためです。sec-agree を行って ESP だけを省く形は標準に無いので、`SIMPLIFIED:` に理由を書きます。IPsec を使わない AKA は、標準では TLS の上の AKAv2-SHA-256 だけです（TS 24.229 4.2B.1）。 |
| AKA の版 | AKAv1-MD5（RFC 3310）だけにします。独立した試験相手の SIPp（[ue-simulator.md](ue-simulator.md)）が AKAv2 に対応していないので、同じ AKAv1-MD5 で比べるためです。TS 33.203 6.1.1 は、S-CSCF が AKAv2-SHA-256 と AKAv1-MD5 の両方を示し、UE が AKAv2-SHA-256 に対応することを求めています。AKAv2 を省いたことを `SIMPLIFIED:` に書きます。 |
| PCRF スタブ | 役割 `epc` の中に置きます。PGW のスタブとのあいだの Gx はプロセスの中の関数の呼び出しにし、`SIMPLIFIED:` に書きます。P-CSCF とのあいだの Rx は Diameter（TS 29.214）です。 |
| EPC を通らない UE | ホストのソフトフォンのように EPC を通らない UE では、PCRF が IP-CAN_SESSION_NOT_AVAILABLE を返します。そのとき P-CSCF は、その UE の呼で Rx を使わずに進めます。 |
| P-CSCF のダイアログ | P-CSCF は、自分で BYE を作るのに要るダイアログの情報（相手の Contact、To と From、CSeq、Route）を `Store` の後ろに置きます。bearer を失ったときに、網の側から呼を切るためです（TS 24.229 5.2.8.1.2）。どの台でもこの情報を読めるように、`Store` の後ろに置きます（[state-redundancy.md](state-redundancy.md)）。 |

コーデック（AMR と AMR-WB、UE の offer の順）は [codecs.md](codecs.md) にあります。

## 採らなかった案

調査は、S-CSCF が AKAv2-SHA-256 と AKAv1-MD5 の両方を出し、UE シミュレータは AKAv2 を選ぶ案を勧めていました。上の「AKA の版」の理由で、AKAv1-MD5 だけにしました。
