---
title: Platform engineering: DevOps jamoasi tiket navbatiga aylanmasligi uchun
subtitle: Golden path, self-service va platformani mahsulot sifatida boshqarish
author: dilnoza
category: Cloud
tags: platform-engineering, devops, backstage, culture
labels: Chuqur tahlil
days_ago: 6
cover: building-blocks
---
DevOps jamoamizda 140 ta ochiq tiket bor edi: "yangi bucket kerak", "staging'ga namespace bering", "pipeline'ni sozlab bering". Har bir jamoa 3-5 kun kutardi. "DevOps" madaniyat emas, alohida bo'limga aylangan edi.

## Muammo: kognitiv yuk

Ilova dasturchisidan Kubernetes, Terraform, IAM, Helm, Prometheus va yana o'nlab vositani bilishni talab qilish real emas. Natija — yoki hamma narsa DevOps jamoasiga tiket bo'lib keladi, yoki har bir jamoa o'z velosipedini yasaydi.

## Golden path

Golden path — eng ko'p uchraydigan holat uchun tayyor, qo'llab-quvvatlanadigan yo'l. Majburiy emas, lekin shunchalik qulayki, hamma undan foydalanadi.

Biz uchun bu:

1. Shablondan yangi servis: repo, Dockerfile, CI, Helm chart, dashboard, alertlar — 2 daqiqada.
2. Infratuzilma so'rovi — YAML fayl, PR orqali:

```yaml
apiVersion: platform.acme.uz/v1
kind: Database
metadata:
  name: orders
  team: checkout
spec:
  engine: postgres
  size: small        # small | medium | large
  backups: daily
```

Crossplane bu faylni RDS instansiyasiga, Secret'ga va monitoring'ga aylantiradi. Platforma jamoasi ishtirok etmaydi.

## Platforma — mahsulot

Eng muhim o'zgarish texnik emas edi. Biz platformani ichki **mahsulot** deb qaray boshladik:

- **Mijozlar** — ilova jamoalari. Har chorakda ular bilan intervyu.
- **Metrikalar** — yangi servisni prod'ga chiqarish vaqti, golden path'dan foydalanish ulushi, DORA metrikalari.
- **Roadmap** — eng og'riqli muammolardan boshlab.
- **Hujjatlar** — Backstage'da yagona katalog: kim qaysi servisga egalik qiladi, API'lar, runbook'lar.

## Natija (6 oydan keyin)

| Metrika | Oldin | Keyin |
|---|---|---|
| Yangi servis → prod | 12 kun | 1 kun |
| Ochiq infra tiketlar | 140 | 18 |
| Haftalik deploy'lar | 35 | 160 |

## Qayerdan boshlash kerak

Backstage o'rnatishdan boshlamang. Eng ko'p tiket kelayotgan **bitta** so'rovni toping va uni self-service qiling. Keyin keyingisini. Platforma portal emas — u olib tashlangan qo'l mehnati.
