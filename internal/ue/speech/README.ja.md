# UE シミュレータの音声

[English](README.md)

コーデックが AMR か AMR-WB の通話で、UE はこの 1 秒の音を `-tone` の長さだけ繰り返して流します。`-amr-in` でほかのファイルを与えたときは、そちらを流します。これらのファイルは `internal/amr/testdata` の写しです。`tools/amrgen` でどう作ったかは、そこの README にあります。

```sh
cp internal/amr/testdata/tone-amr-12k2.amr internal/amr/testdata/tone-amrwb-23k85.amr internal/ue/speech/
```
