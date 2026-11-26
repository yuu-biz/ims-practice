# AMR test tones

[日本語](README.ja.md)

Each file is one second of a 440 Hz sine at half of full scale, in the
single-channel storage format of RFC 4867 5, made with `tools/amrgen`:

| File | Codec and mode | Command |
| --- | --- | --- |
| `tone-amr-12k2.amr` | AMR 12.2 kbit/s (frame type 7) | `CGO_ENABLED=1 go run -tags amrgen ./tools/amrgen -codec AMR -mode 7 -hz 440 -seconds 1 -o internal/amr/testdata/tone-amr-12k2.amr` |
| `tone-amrwb-23k85.amr` | AMR-WB 23.85 kbit/s (frame type 8) | `CGO_ENABLED=1 go run -tags amrgen ./tools/amrgen -codec AMR-WB -mode 8 -hz 440 -seconds 1 -o internal/amr/testdata/tone-amrwb-23k85.amr` |

The encoders were opencore-amr 0.1.6 and vo-amrwbenc 0.1.3 from Ubuntu
24.04 (`libopencore-amrnb-dev`, `libvo-amrwbenc-dev`), both under the
Apache License 2.0. `ffmpeg -i <file> out.wav` decodes the files with
FFmpeg's own decoders.
