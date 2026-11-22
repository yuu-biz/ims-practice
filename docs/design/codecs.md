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

## AMR と AMR-WB の特許について公開されている資料

学習者が通話の音声を聞けるようにするために、AMR（TS 26.071、TS 26.090）と AMR-WB（TS 26.171、TS 26.190）の特許とライセンスを調べました。公開されている資料で読み取れたことと、読み取れなかったことを分けて書きます。法律の判断はしません。

読み取れたこと。

- ライセンスの窓口は VoiceAge です。同社の[ライセンスの一覧](https://voiceage.com/Overview-lic.html)には [AMR-NB（AMR）](https://voiceage.com/AMR-NB.AMR.html)と [AMR-WB/G.722.2](https://voiceage.com/AMR-WB.G.722.2.html) の両方が並んでいます。どちらのページにも料率と期限は無く、問い合わせ先の電子メールだけがあります。AMR-WB の窓口は Acacia Research の担当者です。
- VoiceAge は[標準ごとの特許ファミリーの一覧](https://voiceage.com/Patent-Portfolio.html)を公開しています。この表にはファミリーごとに満了の年が入っていて、3GPP-AMR-WB の列に印が付いたファミリー（VAC 300 から VAC 800）の満了の年は 2019 年から 2024 年です。いちばん遅いのは VAC 800 の 2024 年（米国）と、VAC 400 の 2023 年（ブラジル）です。ページの末尾の著作権の表示は 2021 年なので、この表が今の状態を表しているとはかぎりません。AMR-WB に必須だとする 150 件の登録特許は[国ごとの一覧](https://voiceage.com/Patent-Portfolio-Essential.html)にあります。この表に AMR-NB の列はありません。
- Via LA が運営している音声コーデックのプログラムが対象にしているのは、[EVS と IVAS](https://www.via-la.com/licensing-2/voice-codec/) です。AMR-WB はその中で EVS の AMR-WB 互換の機能（TS 26.446）として出てくるだけで、AMR-NB と AMR-WB のプログラムはありません。
- opencore-amr と vo-amrwbenc のライセンスは Apache-2.0 です。Apache-2.0 の特許の許諾は、提供者が持つ請求項のうち、その提供物が必ず侵害するものだけを対象にします（[Apache License 2.0 の 3 条](https://www.apache.org/licenses/LICENSE-2.0.txt)）。ほかの権利者の標準必須特許は入りません。
- FFmpeg は AMR と AMR-WB の復号器を自分で持っていて、AMR のライブラリを入れなくても復号できます（`libavcodec/amrnbdec.c` と `libavcodec/amrwbdec.c`、LGPL-2.1 以降）。Ubuntu 24.04 の ffmpeg 6.1.1 には amrnb と amrwb の復号器と、格納形式を読む demuxer が入っていて、このリポジトリの試験音（`internal/amr/testdata`）を 440 Hz の正弦波として復号できました。符号器は入っていません。
- ETSI の [IPR のデータベース](https://ipr.etsi.org/)で、AMR と AMR-WB の仕様（TS 26.071、26.073、26.090 から 26.094、26.104、26.171、26.173、26.190 から 26.194、26.204）に宣言された特許を探しました。これらの仕様を挙げた開示は 580 件ありました。宣言した会社は、Qualcomm、Nokia、Ericsson、NEC、VoiceAge、Orange、Samsung、InterDigital などです。ETSI は、宣言された権利が本当に必須かどうかを保証しないと書いています。そのため、宣言にはコーデックと関わりの薄い題名の特許（ビームフォーミングなど）も含まれています。
- 開示に出ている米国、欧州、日本の登録公報 475 件の状態を、[Google Patents](https://patents.google.com/) で調べました。満了（Expired）が 458 件、失効や取り消しが 6 件、存続（Active）が 11 件です。Google Patents は、この状態を推定であり法的な結論ではないとしています。
  - AMR-NB の音声の符号化の本体（TS 26.090）に宣言されたものに、存続の表示はありませんでした。いちばん遅い満了は 2020 年でした。
  - VoiceAge の AMR-WB の 6 つのファミリーは、どれも満了の表示でした。いちばん遅い満了は 2024 年（米国）です。
  - 存続の表示のうち、題名がコーデックに関わるものは 1 件でした。Nokia が AMR-WB の C コード（TS 26.173）に宣言した「AMR-WB DTX synchronization」で、米国の公報（US8090588B2）の満了の見込みは 2030 年 11 月です。ほかの 10 件の題名は、VoIP のビットレートの調整やビームフォーミングなど、別の技術のものでした。請求項は読んでいないので、復号に及ぶかどうかは分かりません。

読み取れなかったこと。

- すべての権利者の特許の範囲。宣言の中には、米国、欧州、日本の公報を含まないもの（AMR-NB で 32 件、AMR-WB で 37 件）と、出願の段階のものがあり、その状態は調べていません。ITU-T の G.722.2 の特許の宣言と、GSM の成果物（GSM 06.90 など）だけに宣言されたものも見ていません。存続と表示された特許の請求項が AMR の復号に及ぶかどうかも、読んでいません。
- AMR-NB の特許の満了。VoiceAge の標準ごとの表に AMR-NB の列は無い一方で、ライセンスの一覧には AMR-NB のプログラムが載っています。どちらのページにも、プログラムを終えたとも続けているとも書いていません。
- 復号を符号化と別に扱うかどうか。権利者の公開資料に、復号だけの実装を別に扱うという記述は見つかりませんでした。二次資料には、パーソナルコンピュータ向けの製品（メディアプレーヤーなど）の復号器は無償だという記述があります。一次資料で確かめられなかったので、根拠には使いません。

## EVS を送れるまで、EVS を offer しない

UE は EVS の符号器を持たないので、送れるのは AMR-WB と AMR のフレームです。EVS のペイロードタイプを offer すると、EVS Primary のビットレートと帯域を示すことになります。EVS を offer する端末は、ビットレートの全範囲を扱える必要があります（TS 26.114 6.2.2.2 の Table 6.2a）。端末は最初の offer に `evs-mode-switch` を書けないので（同じ表）、AMR-WB IO だけで送ることも offer では示せません。AMR-WB IO しか送れない UE には、事実に合う EVS の offer を作れません。

送れないレートと帯域を書いた SDP は、UE の能力を偽ります。仕様に合う端末はそういう offer をしません。このプロジェクトは実際の網で起きない動きを選ばないので（[AGENTS.md](../../AGENTS.md)）、NR のアクセスで使う offer にも EVS を入れず、AMR-WB と AMR で通話します。EVS を含む offer を受けたときは、UE は持っていない EVS を断り、AMR-WB を選びます。answer の m= 行には offer にあるコーデックのうち、answer の側が送りも受けもできるものを書きます（RFC 3264 6.1）。端末は RFC 4867 と TS 26.445 のペイロード形式のオプションをすべて使えなくてもかまいませんが、どれも正しく受け入れるか断れる必要があります（TS 26.114 6.2.2.3 の最初の段落）。EVS を断って AMR-WB と AMR が残れば、AMR-WB を選びます（同じ節の Table 6.3）。

TS 26.114 5.2.1.1 は端末に超広帯域の音声を求め、超広帯域には EVS が要ります。NG.114 は VoNR の UE に EVS を求めています。この 2 つを満たさないことは、`internal/sdp` の `VoNRCodecs` に `SIMPLIFIED:` で書きました。

採らなかった案は、SDP では EVS を offer し、中身は AMR-WB のフレームを EVS AMR-WB IO として運ぶ形です（[5gc.md](../background/5gc.md) の 7 章の案 B）。上のとおり、SDP が UE の能力を偽ることになります。

## UE の offer の順

AMR-WB、AMR、PCMU、telephone-event の順にします。NR のアクセスで使う offer も同じ順です。PCMU を残すのは、PCMU だけのソフトフォンと話すためと、国内の網間接続（JJ-90.30）が G.711 を求めるためです。その分、b=AS と、PCRF に求める帯域は PCMU に合わせて大きくなります。
