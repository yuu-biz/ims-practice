# 会議の情報

[English](README.md)

このパッケージは、RFC 4575 の会議の情報の文書（`application/conference-info+xml`）を組み立てて読みます。会議のイベントパッケージの NOTIFY は、どれもこの文書を本文に入れます。会議のフォーカスは、この文書で、会議に誰がいて、それぞれがどの状態にあるかを参加者に知らせます（TS 24.147 5.3.3.2、TS 24.605 4.5.2.2.2）。3 者会議では、フォーカスの MMTel AS がこの文書を組み立て、UE が読みます。

```sh
go test ./internal/confinfo
go doc ./internal/confinfo
```

## このパッケージで簡略化していること

どの行の場所にも、コードに `SIMPLIFIED:` のコメントがあります。

| 場所 | 省いたこと | 理由 |
| --- | --- | --- |
| `Conference.FullState`、RFC 4575 3.2 | 一部だけの文書。既定の方針では、SUBSCRIBE に答える NOTIFY で文書の全体を送り、そのあとは変わったところだけを送ります。このパッケージは全体の文書だけを組み立てるので、フォーカスは毎回すべての状態を送ります。 | 購読者は、全体の文書をいつでも受け付けなければなりません（RFC 4575 4.6）。IR.92 の会議は参加者が少ないので、文書は小さいままです。 |
| `Document`、RFC 4575 5.3、5.4、5.6.2 から 5.6.5、5.9 | 要素の host-info、sidebars-by-ref、sidebars-by-val、free-text、keywords、associated-aors、roles、languages、cascaded-focus と、ほかの名前空間の要素と属性。Parse はこれらを読み飛ばすので、読んだ文書を書き直すと失われます。 | このプロジェクトの会議の AS は、これらを作りません。GSMA IR.92 2.3.3 も、これらを求めていません。 |
