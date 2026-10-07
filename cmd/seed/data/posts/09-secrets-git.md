---
title: Git'ga sir tushib qoldi. Endi nima qilish kerak?
subtitle: Birinchi 15 daqiqadagi harakatlar rejasi va kelajakda oldini olish
author: malika
category: Xavfsizlik
tags: security, git, secrets, incident
labels: Yangi boshlovchilar
days_ago: 47
cover: keys-on-table
---
Kimdir `.env` faylini public repo'ga push qildi. Botlar GitHub'dagi yangi commit'larni bir necha **soniyada** skanerlaydi, shuning uchun vaqt hal qiluvchi.

## 1-qadam: kalitni bekor qiling (birinchi navbatda!)

Commit'ni o'chirish emas, aynan **rotate** — birinchi ish. Git tarixidan o'chirish siz o'ylagandan sekin va to'liq emas: fork'lar, clone'lar, CI cache'lari, GitHub'ning o'z kesh'i.

- AWS: IAM'da access key'ni `Inactive` qiling, yangisini yarating.
- Ma'lumotlar bazasi paroli: yangi parol, eski ulanishlarni uzing.
- Stripe/Telegram/Slack token: provayder panelida revoke.

## 2-qadam: ishlatilganmi?

AWS CloudTrail'da shu access key bilan qilingan so'rovlarni qidiring:

```bash
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=AccessKeyId,AttributeValue=AKIA... \
  --max-results 50
```

Notanish region'larda `RunInstances` — klassik kripto-mayning belgisi.

## 3-qadam: tarixni tozalash

```bash
git filter-repo --path .env --invert-paths
git push --force --all
```

So'ng GitHub Support'dan cached view'larni tozalashni so'rang.

## Kelajakda oldini olish

**Lokal pre-commit hook:**

```yaml
# .pre-commit-config.yaml
repos:
  - repo: https://github.com/gitleaks/gitleaks
    rev: v8.28.0
    hooks:
      - id: gitleaks
```

**GitHub push protection** — Settings → Code security → Secret scanning'ni yoqing. Ma'lum formatdagi tokenlar push paytida bloklanadi.

**Uzoq muddatli kalitlardan voz keching.** CI uchun OIDC (GitHub → AWS role), ilovalar uchun IRSA/Workload Identity. Saqlanmaydigan kalitni o'g'irlab bo'lmaydi.

Postmortem'da "kim aybdor?" emas, "nega tizim bunga yo'l qo'ydi?" deb so'rang.
