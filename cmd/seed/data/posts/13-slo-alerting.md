---
title: Kechasi uyg'otmaydigan alertlar: SLO va burn rate
subtitle: CPU 80% alertidan voz kechib, foydalanuvchi tajribasiga asoslangan ogohlantirishlarga o'tamiz
author: nodira
category: Observability
tags: sre, slo, alerting, prometheus
labels: Chuqur tahlil, Tavsiya etiladi
days_ago: 41
cover: alarm-clock
---
Navbatchi muhandis bir haftada 140 ta alert oldi. Ulardan 6 tasi haqiqiy muammo edi. Qolganlari — "CPU 85%", "disk 70%", "pod restart". Natija: odamlar alertlarni o'qimay qo'yishdi va haqiqiy uzilishni 40 daqiqa kech sezishdi.

## Simptom, sabab emas

Foydalanuvchi CPU'ni sezmaydi. U sekin sahifa va xatolarni sezadi. Alert faqat foydalanuvchi og'rig'ida chalinishi kerak.

## SLO ta'rifi

> 30 kunlik oynada `GET /api/posts` so'rovlarining 99.5 foizi 300 ms dan tez va xatosiz javob beradi.

99.5% — bu oyiga ~3.6 soatlik **xato byudjeti**. Byudjet bor ekan — tez deploy qilamiz. Tugasa — barqarorlikka e'tibor.

## Burn rate

Burn rate = byudjet qanchalik tez yonayotgani. 1 — aynan 30 kunda tugaydi. 14.4 — 2 kunda tugaydi.

```yaml
groups:
  - name: api-slo
    rules:
      - record: slo:error_ratio:5m
        expr: |
          sum(rate(http_requests_total{job="api",code=~"5.."}[5m]))
          / sum(rate(http_requests_total{job="api"}[5m]))
      - record: slo:error_ratio:1h
        expr: |
          sum(rate(http_requests_total{job="api",code=~"5.."}[1h]))
          / sum(rate(http_requests_total{job="api"}[1h]))
      - alert: APIErrorBudgetFastBurn
        expr: slo:error_ratio:1h > (14.4 * 0.005) and slo:error_ratio:5m > (14.4 * 0.005)
        labels: { severity: page }
        annotations:
          summary: "API xato byudjeti 2 kundan kam vaqtda tugaydi"
```

Ikki oyna (1 soat va 5 daqiqa) bir vaqtda tekshiriladi: uzoq oyna shovqinni filtrlaydi, qisqasi muammo tugaganda alertni tez o'chiradi.

## Ikki daraja

- **page** (telefon): tez yonish, 14.4x 1 soatda yoki 6x 6 soatda.
- **ticket** (Slack/Jira): sekin yonish, 1x 3 kunda.

## Natija

140 alert → haftasiga 4-5 ta. Har biri haqiqiy va har birida runbook havolasi bor.

Sloth yoki Pyrra kabi vositalar SLO YAML'idan bu qoidalarni avtomatik generatsiya qiladi.
