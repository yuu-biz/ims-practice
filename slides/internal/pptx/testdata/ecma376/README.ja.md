# ECMA-376 のスキーマ

[English](README.md)

`schema_test.go` が pptx の各パートを検査するのに使う XML のスキーマです。どれも、Ecma International が ECMA-376「Office Open XML file formats」と一緒に公開しているファイルを、1 バイトも変えずに写したものです。取得元は[この標準のページ](https://ecma-international.org/publications-and-standards/standards/ecma-376/)です。

書き出す側は Transitional の名前空間（Strict の `purl.oclc.org/ooxml/presentationml/main` ではなく `.../presentationml/2006/main`）を使うので、ここに置くのは Part 4 が公開する Transitional のスキーマです。Part 1 が公開する Strict のスキーマは、これらの名前空間の要素を 1 つも宣言しないので、パッケージのどのパートも通しません。

11 個のファイルは、Part 4「Transitional Migration Features」の第 5 版（`ECMA-376-4_5th_edition_december_2016.zip` の中の `OfficeOpenXML-XMLSchema-Transitional.zip`）から取りました。それぞれの W3C XML Schema は Annex A にあります。

| ファイル | 条項 | 定めるもの |
| --- | --- | --- |
| `pml.xsd` | A.4 | PresentationML。プレゼンテーション、マスター、レイアウト、スライド |
| `dml-main.xsd` | A.5.1 | DrawingML Main。テーマ、表のスタイル、スライドの図形と文字 |
| `dml-picture.xsd` | A.5.2 | DrawingML Picture |
| `dml-lockedCanvas.xsd` | A.5.3 | DrawingML Locked Canvas |
| `dml-chart.xsd` | A.6.1 | DrawingML Charts |
| `dml-chartDrawing.xsd` | A.6.2 | DrawingML Chart Drawings |
| `dml-diagram.xsd` | A.6.3 | DrawingML Diagrams |
| `shared-documentPropertiesExtended.xsd` | A.8.2 | 拡張プロパティ、`docProps/app.xml` |
| `shared-documentPropertiesVariantTypes.xsd` | A.8.4 | プロパティが使う variant の型 |
| `shared-relationshipReference.xsd` | A.8.8 | リレーションシップを指す `r:id` の属性 |
| `shared-commonSimpleTypes.xsd` | A.8.9 | ほかのスキーマが共有する単純型 |

2 個のファイルは、Part 2「Open Packaging Conventions」の第 5 版（`ECMA-376-2_5th_edition_december_2021.zip` の中の `OpenPackagingConventions-XMLSchema.zip`）から取りました。パッケージそのもののスキーマは Annex C にあります。

| ファイル | 条項 | 定めるもの |
| --- | --- | --- |
| `opc-contentTypes.xsd` | C.2 | `[Content_Types].xml` |
| `opc-relationships.xsd` | C.5 | `.rels` のパート |

置いてあるのは、検査に要るファイルだけです。パッケージのパートを検査する 4 つと、それらが直接または間接に取り込むものすべてです。pptx はグラフも図表も画像も持ちませんが、`dml-main.xsd` がそれらのスキーマを取り込むので、無いと検査する側が `dml-main.xsd` を読めません。アーカイブには WordprocessingML、SpreadsheetML、VML のスキーマもありますが、pptx はどれも使いません。

`docProps/core.xml` のスキーマはここにありません。そのスキーマである Part 2 の `opc-coreProperties.xsd`（C.3）は、Dublin Core のスキーマを `dublincore.org` のアドレスで取り込みます。Ecma はそれを標準と一緒に公開していないので、標準のファイルだけでは検査する側が読めません。このパートは、代わりに `TestPackageIsComplete` が読みます。

## ライセンス

このリポジトリのライセンス（Apache License 2.0 と CC BY 4.0。`NOTICE` を参照）は、これらのファイルには当たりません。当たるのは下の条件です。

Ecma International は標準を無償で配っています。その文書の著作権の方針は、下の著作権の表示を付けて、変えずに写し、公開し直してよいとしています。Ecma は 2009 年からすべての新しい標準にこの表示を入れており、それより古い標準にも実際上は当たるとしています。ここで使う ECMA-376 の版には、それぞれの表示がありません。方針と表示は <https://ecma-international.org/policies/by-ipr/ecma-text-copyright-policy/> にあります。

表示は原文のまま載せます。表示自身が、正式なものは英語の版だとしているためです。

> COPYRIGHT NOTICE
>
> © Ecma International
>
> By obtaining and/or copying this work, you (the licensee) agree that
> you have read, understood, and will comply with the following terms
> and conditions.
>
> This document may be copied, published and distributed to others, and
> certain derivative works of it may be prepared, copied, published, and
> distributed, in whole or in part, provided that the above copyright
> notice and this Copyright License and Disclaimer are included on all
> such copies and derivative works. The only derivative works that are
> permissible under this Copyright License and Disclaimer are:
>
> (i) works which incorporate all or portion of this document for the
> purpose of providing commentary or explanation (such as an annotated
> version of the document),
>
> (ii) works which incorporate all or portion of this document for the
> purpose of incorporating features that provide accessibility,
>
> (iii) translations of this document into languages other than English
> and into different formats and
>
> (iv) works by making use of this specification in standard conformant
> products by implementing (e.g. by copy and paste wholly or partly) the
> functionality therein.
>
> However, the content of this document itself may not be modified in
> any way, including by removing the copyright notice or references to
> Ecma International, except as required to translate it into languages
> other than English or into a different format.
>
> The official version of an Ecma International document is the English
> language version on the Ecma International website. In the event of
> discrepancies between a translated version and the official version,
> the official version shall govern.
>
> The limited permissions granted above are perpetual and will not be
> revoked by Ecma International or its successors or assigns.
>
> This document and the information contained herein is provided on an
> "AS IS" basis and ECMA INTERNATIONAL DISCLAIMS ALL WARRANTIES, EXPRESS
> OR IMPLIED, INCLUDING BUT NOT LIMITED TO ANY WARRANTY THAT THE USE OF
> THE INFORMATION HEREIN WILL NOT INFRINGE ANY OWNERSHIP RIGHTS OR ANY
> IMPLIED WARRANTIES OF MERCHANTABILITY OR FITNESS FOR A PARTICULAR
> PURPOSE.

ここのファイルは変えていないので、この表示が要るのはこのディレクトリだけです。ファイルを書き換えないでください。書き換えればライセンスに反し、検査も標準ではないものとの比較になります。新しい版にするときは、上のページから取ってきて、ファイルを置き換えます。
