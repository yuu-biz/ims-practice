# コーデック

この記録は音声コーデックの選び方と、AMR と AMR-WB の扱いの設計です。

## 選んだこと

- UE と網の音声コーデックは GSMA IR.92（VoLTE）と NG.114（VoNR）の必須の要件に従います。
- 音声通話は G.711 μ-law（PCMU、ペイロードタイプ 0）から始めました。
- AMR と AMR-WB に対応し、RFC 4867 のペイロード形式（bandwidth-efficient と octet-aligned）を扱います。AMR と AMR-WB は符号化済みのフレームを使います。製品は cgo を使いません。
- G.711 は残します。
- UE の offer のコーデックは AMR-WB、AMR、PCMU、telephone-event の順にします。
- EVS は EVS のコーデックを用意したときに offer に加えます。それまでは、NR のアクセスで使う offer（`sdp.VoNRCodecs`）も VoLTE の offer と同じにし、EVS を入れません。

## IR.92 と NG.114 に従う理由と、その中身

IR.92 は G.711 に触れておらず、端末には AMR と AMR-WB を求めています。NG.114 はこれに加えて EVS を求めています（[profiles.md](../background/profiles.md)）。

G.711 を残すのは、国内の網間接続の標準（JJ-90.30）が、移動網どうし以外の接続で G.711 を求めているためです。G.711 は Wireshark の RTP Player でそのまま再生できます。

今の選択は G.711 から始めて、IR.92 の必須の AMR と AMR-WB を加えた形です。EVS の扱いが決まったら、この選択に EVS が加わります（[requirements.md](../requirements.md) の未決事項）。

## AMR と AMR-WB は符号化済みのフレームで運ぶ

RFC 4867 5 章の格納形式で、符号化済みのフレームを用意します。UE は Go だけで書いたパケット化と復元で、フレームを RTP に載せます（bandwidth-efficient と octet-aligned、CMR、ToC）。製品は CGO_ENABLED=0 のままで、中継ノードは AMR と AMR-WB の符号化も復号もしません。

フレームを作る道具は cgo で opencore-amr と vo-amrwbenc（Apache-2.0）を使います。この道具はビルドタグで製品のビルドから外します。送ったフレームと受けたフレームをビットで比べられます。

CMR に応じたモードの切り替えを省くときは、`SIMPLIFIED:` に書きます。

採らなかった案は opencore-amr と vo-amrwbenc を cgo で製品に組み込む形です。この 2 つのライブラリは、フレームを作る道具だけが使います。

製品が AMR を復号しないので、MRFP は AMR の音声を混ぜられません。MRFP の扱いは [mmtel-mrf.md](mmtel-mrf.md) にあります。

## EVS を送れるまで、EVS を offer しない

UE は EVS の符号器を持たないので、送れるのは AMR-WB と AMR のフレームです。EVS のペイロードタイプを offer すると、EVS Primary のビットレートと帯域を示すことになります。EVS を offer する端末は、ビットレートの全範囲を扱える必要があります（TS 26.114 6.2.2.2 の Table 6.2a）。端末は最初の offer に `evs-mode-switch` を書けないので（同じ表）、AMR-WB IO だけで送ることも offer では示せません。AMR-WB IO しか送れない UE には、事実に合う EVS の offer を作れません。

送れないレートと帯域を書いた SDP は、UE の能力を偽ります。仕様に合う端末はそういう offer をしません。このプロジェクトは実際の網で起きない動きを選ばないので（[AGENTS.md](../../AGENTS.md)）、NR のアクセスで使う offer にも EVS を入れず、AMR-WB と AMR で通話します。EVS を含む offer を受けたときは、UE は持っていない EVS を断り、AMR-WB を選びます。answer の m= 行には offer にあるコーデックのうち、answer の側が送りも受けもできるものを書きます（RFC 3264 6.1）。端末は RFC 4867 と TS 26.445 のペイロード形式のオプションをすべて使えなくてもかまいませんが、どれも正しく受け入れるか断れる必要があります（TS 26.114 6.2.2.3 の最初の段落）。EVS を断って AMR-WB と AMR が残れば、AMR-WB を選びます（同じ節の Table 6.3）。

TS 26.114 5.2.1.1 は端末に超広帯域の音声を求め、超広帯域には EVS が要ります。NG.114 は VoNR の UE に EVS を求めています。この 2 つを満たさないことは、`internal/sdp` の `VoNRCodecs` に `SIMPLIFIED:` で書きました。

採らなかった案は、SDP では EVS を offer し、中身は AMR-WB のフレームを EVS AMR-WB IO として運ぶ形です（[5gc.md](../background/5gc.md) の 7 章の案 B）。上のとおり、SDP が UE の能力を偽ることになります。

## UE の offer の順

AMR-WB、AMR、PCMU、telephone-event の順にします。NR のアクセスで使う offer も同じ順です。PCMU を残すのは、PCMU だけのソフトフォンと話すためと、国内の網間接続（JJ-90.30）が G.711 を求めるためです。その分、b=AS と、PCRF に求める帯域は PCMU に合わせて大きくなります。
