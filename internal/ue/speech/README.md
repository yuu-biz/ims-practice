# Speech of the UE simulator

The UE plays these one-second tones in calls whose codec is AMR or
AMR-WB, repeated for the length of `-tone`, unless `-amr-in` gives a
file of its own. They are copies of `internal/amr/testdata`, where the
README tells how `tools/amrgen` made them:

```sh
cp internal/amr/testdata/tone-amr-12k2.amr internal/amr/testdata/tone-amrwb-23k85.amr internal/ue/speech/
```
