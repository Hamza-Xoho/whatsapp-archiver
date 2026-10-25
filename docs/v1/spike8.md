# Spike 8: FTS5 tokenizers with the Go Arabic folder

Gates `search_fts` (schema.md R18, section 3, 4.2). Driver: modernc.org/sqlite v1.60.1 (SQLite 3.53.4); FTS5, `unicode61`,
`trigram`, `fts5vocab` and `contentless_delete=1` are all compiled in. Text and query were both folded in Go first
(strip U+064B-065F/0670 harakat, strip tatweel, أإآ->ا, ة->ه, ى->ي, lower-case). 30 mixed Arabic/English/emoji/number/URL
sample rows. The throwaway test was run with `GOTOOLCHAIN=local` and the repo caches (Makefile env) and then deleted.

## Results (folded query, contentless tables, same rowids)

| Case | unicode61 | trigram |
|---|---|---|
| exact word (الله) | yes | yes |
| typed without diacritics (كتاب vs كِتَاب) | yes | yes |
| tatweel in text, plain query (مدرسه vs مـدرسـة) | yes | yes |
| hamza variant (احمد finds أحمد) | yes | yes |
| ta marbuta / alef maqsura (رحمه, الي) | yes | yes |
| **prefix attached, query without prefix** (كتاب must find والكتاب; مدرسه must find المدرسه/بالمدرسه) | **no** (whole-token only; `*` is prefix-only) | **yes** |
| **substring** (درس, تاب, ذهب inside a word) | **no** | **yes** |
| 2-char query (في, ok) | yes | **no** (needs 3+ chars; returns nothing) |
| English prefix `foo*` | yes | n/a; plain `foo` is a substring match (also hits "football") |
| Arabic stem prefix `مكتب*` | yes | yes (as substring) |
| phrase `"this is"`, `"رقم الهاتف"` | yes | yes |
| mixed-script (hello, صديقي in one row) | yes | yes |
| URL host / full URL as phrase | yes | yes |
| number 0501234567 | yes | yes |
| case-insensitive English | yes | yes |
| emoji query (😀) | **no** (emoji are separators) | **no** (1 char < 3) |

Trigram is looser by design: `w4242` matched 55 rows against 6 for unicode61 (substring noise), and it has no word boundaries.

## Contentless table behaviour (both tokenizers)

- `WHERE rowid = n` works but only as an existence test: the row's columns come back NULL.
- `highlight()` and `snippet()` return NULL, no error; `SELECT body` is NULL. In-chat highlighting must be done in Go:
  fetch the message text by `seq`, fold it with a char-offset map back to the original, and mark the folded query terms
  (or for trigram, the folded substring) in the original string.
- Plain `DELETE` fails: "cannot DELETE from contentless fts5 table". Options: the `'delete'` command
  (`INSERT INTO t(t, rowid, body) VALUES('delete', n, <exact original folded body>)`; wrong text silently corrupts the index), or create
  with `contentless_delete=1`, after which `DELETE` and `UPDATE` by rowid work (tested) and the rowid can be reused.
  Negative rowids (R13) insert and match fine. `fts5vocab` and `ORDER BY rank` work.
- Raw tables are insert-only and RebuildDerived drops and recreates, so deletes are rare; still use `contentless_delete=1`.

## Measurements (100k generated rows, 8.8 MB folded text, file DB, WAL, after `optimize`)

| | unicode61 | trigram |
|---|---|---|
| insert 100k (one tx) | 1.0 s | 1.9 s |
| db size | 8.1 MB | 23.7 MB (2.9x) |
| top-50 query, common word (24k hits) | 60-70 us | 110-120 us |
| top-50 two terms / AND | 100 us | 280-590 us |
| top-50 phrase "hello world" | 0.5 ms | 1.7 ms |
| rare token (w4242) | 25 us | 0.5 ms |

Both are far inside the interactive budget at this size. At 1M rows expect roughly 80 MB vs 240 MB.

## Recommendation: unicode61 + `search_tri`

unicode61 alone fails the most common Arabic case: attached prefixes (ال, و, ب, ل, ك, ف) and substrings. A user typing
مدرسه would miss المدرسة. Add the second contentless table, same rowids and same folded body, maintained in the same
transaction as `search_fts`. Query routing (Go): fold the query; if every term is 3+ chars use `search_tri`
(superset: substring, phrase and AND all work; rank is poor, so order by `rowid DESC`/time, which is what chat search wants);
if any term is 1-2 chars, use `search_fts` (word match, supports `foo*`). Emoji are not searchable either way; accept for V1.
Cost: about +2x db size over the FTS index alone (about +16 MB per 100k messages) and about 2x insert time on that table.
Untested alternative: add prefix-stripped Arabic stems to the unicode61 body to avoid trigram; it fixes prefixes but not substrings.

```sql
CREATE VIRTUAL TABLE search_fts USING fts5(body, content='', contentless_delete=1,
  tokenize='unicode61 remove_diacritics 0');
CREATE VIRTUAL TABLE search_tri USING fts5(body, content='', contentless_delete=1,
  tokenize='trigram');
```

Schema follow-ups: schema.md R18/section 3 gain `contentless_delete=1` and `search_tri`; RebuildDerived drops/recreates both.
