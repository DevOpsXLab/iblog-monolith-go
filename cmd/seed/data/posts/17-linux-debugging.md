---
title: Server sekinlashdi: Linux'da birinchi 60 soniya
subtitle: uptime'dan iostat'gacha — muammoni tez toraytirish uchun 10 ta buyruq
author: timur
category: Linux
tags: linux, troubleshooting, performance, sre
labels: Tavsiya etiladi, Yangi boshlovchilar
publication: cloud-native-uz
days_ago: 108
cover: terminal-green
---
Brendan Gregg'ning mashhur ro'yxatiga asoslangan, kundalik ishda sinalgan tartib. Maqsad — bir daqiqada muammo CPU, xotira, disk yoki tarmoqdami, shuni aniqlash.

```bash
uptime
dmesg -T | tail -20
vmstat 1 5
mpstat -P ALL 1 3
pidstat 1 3
iostat -xz 1 3
free -m
sar -n DEV 1 3
sar -n TCP,ETCP 1 3
top
```

## Nimaga qarash kerak

**uptime** — load average 1, 5, 15 daqiqa. 1-daqiqalik 15-daqiqalikdan ancha katta bo'lsa, muammo hozir boshlangan.

**dmesg** — OOM killer, disk xatolari, TCP drop'lar. Ko'pincha javob shu yerda.

**vmstat** — `r` ustuni (navbatdagi jarayonlar) yadrolar sonidan katta bo'lsa — CPU yetishmayapti. `si/so` noldan katta — swap, xotira tugagan. `wa` yuqori — disk kutilmoqda.

**mpstat** — bitta yadro 100%, qolganlari bo'sh? Bitta oqimli jarayon yoki interrupt muammosi.

**iostat -xz** — `%util` 100% ga yaqin va `await` o'nlab ms — disk to'yingan.

**free -m** — `available` ustuniga qarang, `free` ga emas. Linux bo'sh xotirani page cache uchun ishlatadi — bu normal.

**sar -n TCP,ETCP** — `retrans/s` o'sib borsa, tarmoqda paket yo'qolishi bor.

## Konteyner ichida

Konteynerda bu vositalar ko'pincha yo'q va ular xost ko'rsatkichlarini ko'rsatadi. cgroup statistikasini to'g'ridan-to'g'ri o'qing:

```bash
cat /sys/fs/cgroup/cpu.stat        # nr_throttled > 0 — CPU limit'ga urilyapti
cat /sys/fs/cgroup/memory.events   # oom_kill hisoblagichi
```

Kubernetes'da CPU throttling — sekinlikning eng ko'p uchraydigan, lekin eng kam tekshiriladigan sababi. Ko'p jamoalar shu sababli CPU limit'larni umuman olib tashlab, faqat request qoldiradi.
