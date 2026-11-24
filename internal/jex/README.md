# Jex expressions

[日本語](README.ja.md)

This package reads and evaluates the Jex expressions of TS 32.161, with which the 3GPP management services say what a request or a subscription is about. It takes the Jex conditions profile (TS 32.161 7.6), whose grammar is in A.4: paths through a JSON document, comparisons, `and`, `or`, `not()`, parentheses and `contains()`. The `notificationFilter` of a subscription to notifications is such an expression, evaluated against each notification. A read of an alarm list picks alarm records with the forms that TS 28.111 A.1.4 shows: a `filter` or `dataNodeSelector` such as `/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="CRITICAL"]`, and `fields` that name records by their id.

An expression that breaks the grammar is a syntax error; one that keeps it but asks for more than the package evaluates, or is longer than 4096 bytes or nested deeper than 64 levels, is unsupported. TS 32.158 6.6.5.2 answers the first with 400 and the second with 500. The package comment lists what is evaluated.

```sh
go test ./internal/jex
go doc ./internal/jex
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `jex`, TS 32.161 7.5, A.4 | A predicate that is a position, such as `[0]`, which selects an item of a JSON array. Such an expression is unsupported. | The documents here are notifications and alarm lists, whose alarm records are a JSON object keyed by their id, so no expression needs one. |
