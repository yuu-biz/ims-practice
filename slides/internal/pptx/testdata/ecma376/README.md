# Schemas of ECMA-376

[日本語](README.ja.md)

The XML schemas that `schema_test.go` validates the parts of a pptx
against. They are copies, byte for byte, of files that Ecma
International publishes with ECMA-376 "Office Open XML file formats",
from [the page of the
standard](https://ecma-international.org/publications-and-standards/standards/ecma-376/).

The writer makes the Transitional namespaces (`.../presentationml/2006/main`
rather than the Strict `purl.oclc.org/ooxml/presentationml/main`), so the
schemas here are the Transitional ones, which Part 4 publishes. The
Strict schemas, which Part 1 publishes, declare no element of these
namespaces and refuse every part of the package.

Eleven files come from Part 4 "Transitional Migration Features", 5th
edition (`ECMA-376-4_5th_edition_december_2016.zip`, then
`OfficeOpenXML-XMLSchema-Transitional.zip` inside it), where Annex A
gives the W3C XML Schema of each:

| File | Clause | What it defines |
| --- | --- | --- |
| `pml.xsd` | A.4 | PresentationML: the presentation, the master, the layouts and the slides |
| `dml-main.xsd` | A.5.1 | DrawingML Main: the theme, the table styles, and the shapes and text of a slide |
| `dml-picture.xsd` | A.5.2 | DrawingML Picture |
| `dml-lockedCanvas.xsd` | A.5.3 | DrawingML Locked Canvas |
| `dml-chart.xsd` | A.6.1 | DrawingML Charts |
| `dml-chartDrawing.xsd` | A.6.2 | DrawingML Chart Drawings |
| `dml-diagram.xsd` | A.6.3 | DrawingML Diagrams |
| `shared-documentPropertiesExtended.xsd` | A.8.2 | The extended properties, `docProps/app.xml` |
| `shared-documentPropertiesVariantTypes.xsd` | A.8.4 | The variant types that the properties use |
| `shared-relationshipReference.xsd` | A.8.8 | The `r:id` attributes that point at a relationship |
| `shared-commonSimpleTypes.xsd` | A.8.9 | The simple types that the rest share |

Two come from Part 2 "Open Packaging Conventions", 5th edition
(`ECMA-376-2_5th_edition_december_2021.zip`, then
`OpenPackagingConventions-XMLSchema.zip` inside it), where Annex C gives
the schemas of the package itself:

| File | Clause | What it defines |
| --- | --- | --- |
| `opc-contentTypes.xsd` | C.2 | `[Content_Types].xml` |
| `opc-relationships.xsd` | C.5 | The `.rels` parts |

Only the files that a validator needs are here: the four that a part of
the package is validated against, and everything those import, directly
or not. A pptx holds no chart, no diagram and no picture, but
`dml-main.xsd` imports their schemas, so a validator cannot read it
without them. The archives hold the schemas of WordprocessingML,
SpreadsheetML and VML as well, which a pptx does not use at all.

`docProps/core.xml` has no schema here. Its schema,
`opc-coreProperties.xsd` of Part 2 (C.3), imports the Dublin Core
schemas by their address at `dublincore.org`, and Ecma does not publish
those with the standard, so a validator cannot read it from the files of
the standard alone. `TestPackageIsComplete` reads that part instead.

## Licence

The licences of this repository (the Apache License 2.0 and CC BY 4.0, see `NOTICE`) do not apply to these files; the terms below do.

Ecma International gives its standards away free of charge, and its
text copyright policy says that they may be copied and republished
unchanged, under the copyright notice below, which Ecma has put in every
new standard since 2009 and which it says applies in practice to the
older ones as well. These editions of ECMA-376 carry no notice of their
own. The policy and the notice are at
<https://ecma-international.org/policies/by-ipr/ecma-text-copyright-policy/>.

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

The files here are unchanged, so nothing but this directory needs the
notice. Do not edit them: a change would both break the licence and
turn the check into a check against something other than the standard.
To take a newer edition, download it from the page above and replace
the files.
