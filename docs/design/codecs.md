# コーデック

この記録は音声コーデックの選び方と、AMR と AMR-WB の扱いの設計です。

## 選んだこと

- UE と網の音声コーデックは GSMA IR.92（VoLTE）と NG.114（VoNR）の必須の要件に従います。
- 音声通話は G.711 μ-law（PCMU、ペイロードタイプ 0）から始めました。
- AMR と AMR-WB に対応し、RFC 4867 のペイロード形式（bandwidth-efficient と octet-aligned）を扱います。AMR と AMR-WB は符号化済みのフレームを使います。製品は cgo を使いません。
- G.711 は残します。
- UE の offer のコーデックは AMR-WB、AMR、PCMU、telephone-event の順にします。
- EVS は VoNR を扱うときに加えます。

## IR.92 と NG.114 に従う理由と、その中身

IR.92 は G.711 に触れておらず、端末には AMR と AMR-WB を求めています。NG.114 はこれに加えて EVS を求めています（[profiles.md](../background/profiles.md)）。

G.711 を残すのは、国内の網間接続の標準（JJ-90.30）が、移動網どうし以外の接続で G.711 を求めているためです。G.711 は Wireshark の RTP Player でそのまま再生できます。

今の選択は G.711 から始めて、IR.92 の必須の AMR と AMR-WB を加えた形です。EVS の扱いが決まったら、この選択に EVS が加わります（[requirements.md](../requirements.md) の未決事項）。

## AMR と AMR-WB は符号化済みのフレームで運ぶ

RFC 4867 5 章の格納形式で、符号化済みのフレームを用意します。UE は Go だけで書いたパケット化と復元で、フレームを RTP に載せます（bandwidth-efficient と octet-aligned、CMR、ToC）。製品は CGO_ENABLED=0 のままで、中継ノードは符号化も復号もしません。

フレームを作る道具は cgo で opencore-amr と vo-amrwbenc（Apache-2.0）を使います。この道具はビルドタグで製品のビルドから外します。送ったフレームと受けたフレームをビットで比べられます。

CMR に応じたモードの切り替えを省くときは、`SIMPLIFIED:` に書きます。

採らなかった案は opencore-amr と vo-amrwbenc を cgo で製品に組み込む形です。この 2 つのライブラリは、フレームを作る道具だけが使います。

製品が AMR を復号しないので、MRFP は AMR の音声を混ぜられません。MRFP の扱いは [mmtel-mrf.md](mmtel-mrf.md) にあります。

## UE の offer の順

AMR-WB、AMR、PCMU、telephone-event の順にします。PCMU を残すのは、PCMU だけのソフトフォンと話すためと、国内の網間接続（JJ-90.30）が G.711 を求めるためです。その分、b=AS と、PCRF に求める帯域は PCMU に合わせて大きくなります。
