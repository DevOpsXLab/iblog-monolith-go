---
title: Aybdorsiz postmortem: uzilishdan qanday qilib foyda olish mumkin
subtitle: Shablon, vaqt chizig'i va haqiqatda bajariladigan harakatlar
author: azizbek
category: Observability
tags: sre, incident, postmortem, culture
labels: Tavsiya etiladi
publication: iblog-weekly
days_ago: 15
cover: team-meeting
---
Juma kuni soat 17:40 da to'lov sahifasi 47 daqiqa ishlamadi. Dushanba kuni yig'ilishda birinchi savol: "Kim deploy qildi?" Bu — noto'g'ri savol.

## Nega aybdorsiz?

Odamlarni jazolasangiz, ular xatolarni yashirishni o'rganadi. Keyingi safar "kichik muammo"ni hech kim aytmaydi va u katta uzilishga aylanadi. Odam xato qiladi — tizim bunga yo'l qo'ymasligi kerak edi.

## Shablon

```markdown
# 2026-09-18: To'lov sahifasi 47 daqiqa 502 qaytardi

## Ta'sir
- 47 daqiqa, to'lovlarning ~92% muvaffaqiyatsiz
- ~1 300 foydalanuvchi, taxminiy yo'qotish: ...

## Vaqt chizig'i (UTC+5)
17:38  payments v2.14 deploy (CI, avtomatik)
17:40  5xx ulushi 0.1% → 91%
17:52  Birinchi mijoz shikoyati support'ga
17:58  Navbatchi alertni oldi (SLO burn rate)
18:15  Sabab topildi: yangi env o'zgaruvchisi prod'da yo'q
18:27  Rollback, xizmat tiklandi

## Asosiy sabab
Yangi PAYMENT_WEBHOOK_SECRET staging'ga qo'shilgan, prod'ga emas.
Ilova ishga tushganda tekshirmadi va birinchi so'rovda panic qildi.

## Nima yaxshi ishladi / nima yomon
...

## Harakatlar
| # | Harakat | Egasi | Muddat |
|---|---|---|---|
| 1 | Konfiguratsiyani startup'da validatsiya qilish | jasur | 25.09 |
| 2 | Deploy'dan keyin avtomatik rollback (5xx > 5%) | sardor | 02.10 |
| 3 | Alert kechikishini 18 → 3 daqiqaga tushirish | nodira | 30.09 |
```

## Muhim jihatlar

**Vaqt chizig'i — eng qimmatli qism.** Undan "17:40 dan 17:58 gacha nega hech kim bilmadi?" degan savol tug'iladi — bu ko'pincha asosiy saboq.

**"Asosiy sabab" ko'pincha bitta emas.** 5 marta "nega?" deb so'rang: nega env yo'q edi? Nega ilova tekshirmadi? Nega canary bo'lmadi?

**Harakatlar egasi va muddati bo'lsin.** Egasi yo'q harakat — bajarilmaydigan harakat. Ularni oddiy tiket sifatida backlog'ga qo'ying va keyingi postmortem'da holatini tekshiring.

**"Ehtiyot bo'lish kerak" — harakat emas.** Odamlarning e'tiborliroq bo'lishiga tayanmang; tizimni o'zgartiring.

Postmortem'ni butun kompaniyaga ochiq e'lon qiling. Boshqa jamoalar sizning xatoingizdan bepul saboq oladi.
