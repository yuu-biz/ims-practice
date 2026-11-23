# BSS から加入者を入れる口

この記録は、契約を受けた業務システム（BSS）が加入者を IMS に入れる口（northbound）を、標準に合わせるかどうかを決めます。対象は OAM の `/oam/v1/provisioning` と `/oam/v1/subscriptions/{impi}`、その先にある HSS のプロビジョニングの API の、外から見た形です。API の中身は [hss-provisioning.md](hss-provisioning.md) が決めます。調べた結果は [oam.md](../background/oam.md) の 7 章にあります。

## 選んだこと

- 標準の口は作らず、今の独自の API のままにします。
- 加入者の管理（SuM）には OSS に面する標準のインタフェースがあります。TS 32.141 4.2.1 は、SuM の Itf-N を IRP として実現するとしています。その具体的な形は TS 28.753 の Bulk CM の XML です（4 章、Annex A.3.1）。
- そのインタフェースが運ぶ情報モデル（TS 28.752）に、長期の鍵（K）、OPc、SIP Digest のパスワードを入れる項目がありません。この実装のプロビジョニングはこれらを運ぶので、標準の形だけでは加入者を入れられません。
- 要件の側はこのデータを含んでいます。TS 32.140 の 1 章は、この文書が HSS に向けた標準のインタフェースを目指すものだとしています。6.2.1.4 は、SuM が AuC のためのプロファイルの部品を HSS の中で扱えることを求めています。情報モデルがその要件に追いついていません。
- UDC の構成では、OSS に面する口が仕様の対象外です。TS 23.335 4.2.3 は、UDR が運用者の OSS に向けたプロビジョニングのインタフェースを持つことを求めたうえで、そのインタフェースを対象外と書いています。4.2.2 も UDC とプロビジョニングの仕組みのあいだのやり取りを対象外と書いています。
- データの項目の名前はすでに仕様の用語です。標準の情報モデルには、意味の近い項目があるものと、当たる項目が無いものの両方があります（下の表）。名前を寄せ直す価値がありません。
- 手順書に、この API がこの実装の独自のものであることと、標準にある形がこの実装の運ぶ資格情報を持たないことを、根拠の条項とともに書きます。学習者がこの API を標準の口と読み違えず、標準の側に何があって何が足りないかを知れるようにするためです。

## 標準の口を作らない理由

標準の口の候補は 4 つです。どれも、この実装が運ぶものに届きません。

| 候補 | 運べるもの | 形 | 採らない理由 |
| --- | --- | --- | --- |
| SuM の情報モデル（TS 28.751、TS 28.752、TS 28.753） | IMPI、IMPU、barring の印、iFC、S-CSCF の名前、登録の状態、Service-Indication の一覧、IMSI、MSISDN | Itf-N に流す Bulk CM の XML のファイル（TS 28.753 4 章、Annex A.3.1） | 長期の鍵、OPc、SIP Digest のパスワードの項目が無い。Solution Set は、クラスの対応付けが「この版には無い」と自ら書いている（Annex A.2.2）。要件（TS 32.140）と構成（TS 32.141）の最後の CR は 2013 年、情報モデル（TS 28.752）は 2014 年で、書庫にある最新の版は Release 19 のもの |
| SA5 の provisioning の management service（TS 28.532 11.1） | 情報モデルが決めた項目 | REST（12.1.1）か YANG と NETCONF（12.1.3）の共通の操作 | 加入者のデータを自分では決めない（11.1.0）。組み合わせる相手の SuM の情報モデルに、REST と YANG の Solution Set が無い |
| Nudr（TS 29.505） | 5GC の加入者のデータ | 5GC の中のサービスベースのインタフェース | 5GC のネットワーク機能が読むための口（4 章）。加入のデータ（`provisioned-data` とその下）は GET だけで、IMS の加入者のデータを持たない |
| TM Forum の TMF641 と TMF640 | 注文と活性化の外枠。中身は名前と値の組 | REST | 名前の集合を決めるのは運用者のサービスの目録で、仕様ではない。どちらの定義にも IMS の加入者の識別子の名前が無い |

標準の口を作らない理由は、標準が何も決めなかったことではありません。決めたものが、この実装が運ぶデータに届かないことです。TS 32.140 の 1 章によれば、この文書が目指すのは HSS に向けた標準のインタフェースです。6.2.1.4 は、AuC のためのプロファイルの部品を SuM が HSS の中で扱えることを求めています。一方で、その要件を具体化した情報モデル（TS 28.752）には、長期の鍵、OPc、SIP Digest のパスワードの項目がありません。要件と構成の最後の CR は 2013 年、情報モデルの最後の CR は 2014 年で、この隔たりはそのまま残っています。

## 今の API と標準の情報モデルの対応

識別子と登録の状態には、標準の情報モデルに意味の近い項目があります。資格情報とサービスの設定には、当たる項目がありません。

意味の近い項目は次のとおりです。

| 今の API の項目 | TS 28.752 のクラスと項目 | 条項 | 違い |
| --- | --- | --- | --- |
| `private_identity` | `SuMCredentials` の `iMSPrivateUserIdentity` | 4.3.18 | なし |
| `public_identities` | `IMSPublicIdentification` の `publicUserIdentity` | 4.3.19 | なし |
| `barred_identities` | `IMSPublicIdentification` の `barringIndication` | 4.3.19 | 今の API は barring する識別子の一覧、標準は公開の識別子ごとの印 |
| `tel` | `MobileUserData` の `mSISDN` | 4.3.20 | 今の API は公開の識別子に足す tel URI、標準は MSISDN の文字列 |
| `registration_state` | `IMSServiceProfile` の `registrationStatus` | 4.3.13 | 今の API は読み取りだけ、標準は書き換えられる項目（`isWritable`） |
| `server_name` | `IMSServiceProfile` の `sCSCFName` | 4.3.13 | どちらも読み取りだけ |

当たる項目が無いものは次のとおりです。

| 今の API の項目 | 標準の情報モデルの側 |
| --- | --- |
| `password`、`aka` の `k`、`opc`、`amf`、`sqn` | 長期の鍵、OPc、SIP Digest のパスワードの項目が無い。4.3.13 にあるのは GBA と GAA の設定（`gBAUserSecuritySettings`、`userSecuritySetting`、`uICCSecurityType`、`keyLifetime`）で、加入者の鍵そのものではない |
| `ut_password`、`mmtel_services`、`customized_alerting_tone`、および [hss-provisioning.md](hss-provisioning.md) が足す `repository_data` | Repository Data の中身を入れる項目が無い。`IMSServiceProfile` にあるのは Service-Indication の一覧（`serviceIndicationList`、4.3.13）で、記録の名前だけ |
| `authentication_schemes` | 当たる項目が無い。今の API が、持っている資格情報から組み立てて返すもの |
| [hss-provisioning.md](hss-provisioning.md) が足す `service_profile` | 当たる項目が無い。`initialFilterCriteriaList`（4.3.13）は iFC の一覧そのもので、プロファイルの名前ではない |
| [hss-provisioning.md](hss-provisioning.md) が足す `test_user` | 当たる項目が無い |

項目の名前を標準の綴りに変える価値はありません。この API はキャプチャに写らないので、名前を変えても学習者の目に入るものが変わりません。一方で `iMSPrivateUserIdentity` のような綴りは、この実装のほかの JSON の項目の書き方と揃いません。意味の近さと違いは上の表で示せます。

## 採らなかった案

| 案 | 採らなかった理由 |
| --- | --- |
| SuM の Bulk CM の XML のファイルを受け取る口を作る | 長期の鍵、OPc、SIP Digest のパスワードを運べないので、この形だけでは加入者を入れられない。資格情報を独自の拡張で足すと、標準の形に見えて標準ではないものになる |
| TMF641 の形に寄せる | 注文を受けて状態を進める非同期の仕組みが要る。今の同期の HTTP より読む手間が増える。加入者の項目が名前と値の組になり、識別子には仕様の用語を使う決まりに反する |
| 項目の名前だけを TS 28.752 の綴りに変える | キャプチャに写らない所の名前を変えるだけで、学習者の目に入るものが変わらない。意味の対応は上の表で示せる |
| SA5 の provisioning の MnS の REST の形（`/{className}={id}` と `/{URI-LDN-first-part}`）を真似る | SuM の情報モデルに REST の Solution Set が無いので、真似られるのは経路の書き方だけになる。保守用の API を標準ではなく独自のものにした理由（[oam.md](../background/oam.md) の 1 章）と同じで、中身が決まっていない形に合わせても得るものが無い |
| 標準の口を足し、独自の API と並べて持つ | 口が 2 つになり、同じ加入者を入れる道が 2 つできる。使う相手はこの実装の OAM だけなので、1 か所しか使わない抽象化を作らない決まりに反する |

## 作らないもの

- SuM の情報モデルの XML を読む口と、その schema の検査。
- TMF641 と TMF640 の注文を受ける口。
- Nudr と Ud の実装。加入者のデータは HSS の `Store` の後ろにあります（[state-redundancy.md](state-redundancy.md)）。
- 項目の名前の付け替え。

## 読んだ仕様

この記録の条項は次の版の本文で確かめました。TS 32.140 V19.0.0（1 章、冒頭の Introduction、4.4.1、4.4.2、4.4.3、4.5.1、6.1、6.2、6.2.1、6.2.1.3、6.2.1.4、6.3、Annex C の変更の履歴）、TS 32.141 V19.0.0（4.1、4.2.1、4.3、4.3.1、4.4.1 から 4.4.3、Annex B の変更の履歴）、TS 28.752 V19.0.0（4.3 のクラスの一覧、4.3.4、4.3.8、4.3.13、4.3.17 から 4.3.20、4.3.22、4.3.23、4.4.1 のうち認証と GBA に関わる属性と `serviceIndicationList` の定義、Annex A の変更の履歴）、TS 28.753 V19.0.0（1 章、4 章、Annex A の A.2.1、A.2.2、A.3.1 から A.3.3、Annex B の変更の履歴）、TS 28.532 V20.2.0（1 章、11.1、11.1.0、11.1.1.1 から 11.1.1.4a、12.1.1、12.1.3）、TS 29.505 V20.0.0（1 章、4 章、5.2 の資源と方法の表、5.2.2.1、5.2.2.3.2、5.2.14.3、5.2.14B.3）、TS 23.335 V19.0.0（4.1 から 4.3、5.4、5.5、Annex B の変更の履歴）、TS 32.181 V19.0.0（1 章）、TS 32.182 V19.0.0（1 章、4 章、5.3 のクラスの一覧、5.3.8 から 5.3.12）です。TM Forum の TMF641 4.2.0 と TMF640 4.0.0 は、TM Forum が Apache-2.0 で公開している写しの OpenAPI の定義を読みました。

## 残っている課題

- 手順書には、プロビジョニングの口がこの実装の独自のものであることの説明がありません。この説明がないと、学習者はこの API を標準の口と読み違えることがあります。
- TS 28.752 の情報モデルは、1 つの加入に複数のサービスプロファイルを持たせる形（`IMSServiceProfile` が `IMSPublicIdentification` を複数まとめる形）を持ちます。この実装は 1 つの加入に 1 つのプロファイルです（[hss-provisioning.md](hss-provisioning.md) の「作らないもの」）。公開の識別子ごとのプロファイルを作るときは、この形をそのまま参考にできます。
