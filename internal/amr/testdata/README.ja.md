# AMR の試験用の音

[English](README.md)

どのファイルも、フルスケールの半分の振幅で 440 Hz の正弦波を 1 秒鳴らしたものです。形式は RFC 4867 5 の 1 チャネルの保存形式で、`tools/amrgen` で作りました。

| ファイル | コーデックとモード | コマンド |
| --- | --- | --- |
| `tone-amr-12k2.amr` | AMR 12.2 kbit/s（frame type 7） | `CGO_ENABLED=1 go run -tags amrgen ./tools/amrgen -codec AMR -mode 7 -hz 440 -seconds 1 -o internal/amr/testdata/tone-amr-12k2.amr` |
| `tone-amrwb-23k85.amr` | AMR-WB 23.85 kbit/s（frame type 8） | `CGO_ENABLED=1 go run -tags amrgen ./tools/amrgen -codec AMR-WB -mode 8 -hz 440 -seconds 1 -o internal/amr/testdata/tone-amrwb-23k85.amr` |

符号化には、Ubuntu 24.04 の opencore-amr 0.1.6 と vo-amrwbenc 0.1.3（`libopencore-amrnb-dev`、`libvo-amrwbenc-dev`）を使いました。どちらも Apache License 2.0 です。`ffmpeg -i <ファイル> out.wav` を実行すると、FFmpeg が自前のデコーダでこれらのファイルを復号します。
