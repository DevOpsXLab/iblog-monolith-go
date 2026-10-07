---
title: PostgreSQL ishlab chiqarishda: indekslar va EXPLAIN ANALYZE
subtitle: Sekin so'rovni topish, tushunish va 300 barobar tezlashtirish
author: bekzod
category: Ma'lumotlar bazasi
tags: postgresql, database, performance, sql
labels: Chuqur tahlil
series: postgres-prod
days_ago: 95
cover: database-servers
---
Lenta sahifasi 4 soniyada ochilyapti. Sabab deyarli har doim bitta: indekssiz so'rov.

## Sekin so'rovni topish

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

SELECT round(total_exec_time) AS total_ms, calls,
       round(mean_exec_time, 1) AS mean_ms, left(query, 80)
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 10;
```

`total_exec_time` bo'yicha saralang — kuniga million marta chaqiriladigan 20 ms'li so'rov bitta 5 soniyalik hisobotdan muhimroq.

## EXPLAIN ANALYZE o'qish

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, title, published_at FROM posts
WHERE status = 'published'
ORDER BY published_at DESC
LIMIT 20;
```

```
Limit  (actual time=812.4..812.5 rows=20)
  ->  Sort  (actual time=812.4..812.4 rows=20)
        Sort Method: top-N heapsort
        ->  Seq Scan on posts  (actual time=0.02..701.3 rows=1840221)
              Filter: (status = 'published')
```

Belgilar: `Seq Scan` 1.8 million qator ustida, keyin `Sort`. Bizga faqat 20 ta kerak edi.

## To'g'ri indeks

```sql
CREATE INDEX CONCURRENTLY posts_published_idx
  ON posts (published_at DESC, id DESC)
  WHERE status = 'published';
```

- **Partial** (`WHERE status = ...`) — indeks kichikroq, faqat kerakli qatorlar.
- **Tartib** ORDER BY bilan mos — sort umuman kerak emas.
- **`id DESC`** — keyset pagination uchun tie-breaker.
- **`CONCURRENTLY`** — jadvalni yozishga bloklamaydi.

Natija: `Index Scan`, **2.6 ms**.

## OFFSET emas, keyset

```sql
-- sekin: 10000-sahifada 200k qatorni o'qib tashlaydi
SELECT ... ORDER BY published_at DESC LIMIT 20 OFFSET 200000;

-- tez: har doim indeksdan 20 qator
SELECT ... WHERE (published_at, id) < ($1, $2)
ORDER BY published_at DESC, id DESC LIMIT 20;
```

## Ortiqcha indekslar ham zarar

Har bir indeks INSERT/UPDATE'ni sekinlashtiradi. Ishlatilmaydiganlarini toping:

```sql
SELECT indexrelname, idx_scan, pg_size_pretty(pg_relation_size(indexrelid))
FROM pg_stat_user_indexes WHERE idx_scan = 0;
```

Keyingi qismda: ulanishlar puli, PgBouncer va migratsiyalarni uzilishsiz o'tkazish.
