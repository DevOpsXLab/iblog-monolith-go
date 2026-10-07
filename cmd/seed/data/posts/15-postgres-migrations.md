---
title: PostgreSQL ishlab chiqarishda: uzilishsiz migratsiyalar
subtitle: ALTER TABLE nega butun saytni to'xtatishi mumkin va bundan qanday qochish kerak
author: bekzod
category: Ma'lumotlar bazasi
tags: postgresql, migrations, database, zero-downtime
labels: Chuqur tahlil, Tavsiya etiladi
series: postgres-prod
days_ago: 54
cover: tools-workbench
---
Oddiy ko'ringan `ALTER TABLE users ADD COLUMN ...` bir marta bizning saytimizni 6 daqiqaga to'xtatdi. Migratsiyaning o'zi 50 ms davom etdi. Muammo — lock navbati.

## Lock navbati qanday ishlaydi

1. Uzoq analitik so'rov `users` jadvalini 6 daqiqa o'qiyapti (`ACCESS SHARE` lock).
2. Migratsiya `ACCESS EXCLUSIVE` lock so'raydi va **kutadi**.
3. Endi har bir oddiy `SELECT` ham migratsiya orqasida navbatga turadi.
4. Ulanishlar puli to'ladi — sayt "yotdi".

## Qoida 1: lock_timeout

```sql
SET lock_timeout = '3s';
SET statement_timeout = '30s';
ALTER TABLE users ADD COLUMN bio text NOT NULL DEFAULT '';
```

Lock 3 soniyada olinmasa — migratsiya xato bilan tugaydi va qayta urinish mumkin. Sayt ishlashda davom etadi.

## Qoida 2: xavfsiz operatsiyalarni biling

| Operatsiya | Xavfsizmi? |
|---|---|
| `ADD COLUMN` (default bilan, PG 11+) | Ha, tez |
| `ADD COLUMN ... NOT NULL` default'siz | Yo'q — jadval bo'sh bo'lmasa xato |
| `CREATE INDEX` | Yo'q — yozishni bloklaydi |
| `CREATE INDEX CONCURRENTLY` | Ha |
| `ALTER COLUMN TYPE` | Ko'pincha yo'q — jadval qayta yoziladi |
| `ADD FOREIGN KEY` | `NOT VALID` + `VALIDATE` bilan ha |

## Qoida 3: expand → migrate → contract

Ustun nomini o'zgartirish kerakmi? Bir qadamda emas:

1. **Expand:** yangi ustun qo'shing, ilova ikkalasiga ham yozsin.
2. **Migrate:** eski ma'lumotlarni partiyalab ko'chiring.
3. **Switch:** ilova yangi ustundan o'qisin.
4. **Contract:** keyingi relizda eski ustunni o'chiring.

```sql
-- 10 minglik partiyalar, replikatsiyani bo'g'maslik uchun
UPDATE users SET display_name = name
WHERE id IN (SELECT id FROM users WHERE display_name IS NULL LIMIT 10000);
```

## Qoida 4: FK va CHECK ikki bosqichda

```sql
ALTER TABLE posts ADD CONSTRAINT posts_user_fk
  FOREIGN KEY (user_id) REFERENCES users (id) NOT VALID;  -- tez
ALTER TABLE posts VALIDATE CONSTRAINT posts_user_fk;      -- yozishni bloklamaydi
```

Migratsiya vositasi (goose, Atlas, Flyway) muhim emas — muhimi bu qoidalarni har bir PR review'da tekshirish. Squawk linteri buni CI'da avtomatik qiladi.
