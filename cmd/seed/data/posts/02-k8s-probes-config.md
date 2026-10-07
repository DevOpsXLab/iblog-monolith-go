---
title: Kubernetes noldan: ConfigMap, Secret va health probe'lar
subtitle: Ilovani konfiguratsiyadan ajratish va o'lik Pod'larni avtomatik almashtirish
author: azizbek
category: Kubernetes
tags: kubernetes, configmap, probes, k8s
labels: Qo'llanma
publication: iblog-weekly
series: kubernetes-noldan
days_ago: 104
cover: server-room-lights
---
Birinchi qismda Deployment va Service yaratdik. Endi ilovani haqiqiy muhitga tayyorlaymiz: konfiguratsiyani image'dan chiqaramiz va Kubernetes'ga ilova "tirikmi" yoki yo'qligini bilishni o'rgatamiz.

## ConfigMap va Secret

12-factor tamoyiliga ko'ra, konfiguratsiya muhit o'zgaruvchilarida yashaydi. Bir xil image dev, staging va prod'da ishlashi kerak.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
data:
  LOG_LEVEL: info
  CACHE_TTL: "300"
---
apiVersion: v1
kind: Secret
metadata:
  name: api-secrets
type: Opaque
stringData:
  DATABASE_URL: postgres://app:parol@postgres:5432/app
```

Deployment ichida:

```yaml
envFrom:
  - configMapRef: { name: api-config }
  - secretRef: { name: api-secrets }
```

> Eslatma: Secret faqat base64 — shifrlash emas. etcd encryption at rest'ni yoqing yoki External Secrets / Sealed Secrets ishlating.

## Uch xil probe

| Probe | Savol | Muvaffaqiyatsiz bo'lsa |
|---|---|---|
| `startupProbe` | Ilova ishga tushib bo'ldimi? | Qolgan probe'lar kutadi |
| `readinessProbe` | Trafik qabul qila oladimi? | Service'dan chiqariladi |
| `livenessProbe` | Hali tirikmi? | Konteyner qayta ishga tushiriladi |

```yaml
startupProbe:
  httpGet: { path: /healthz, port: 8080 }
  failureThreshold: 30
  periodSeconds: 2
readinessProbe:
  httpGet: { path: /readyz, port: 8080 }
  periodSeconds: 5
livenessProbe:
  httpGet: { path: /healthz, port: 8080 }
  periodSeconds: 10
  failureThreshold: 3
```

## Ko'p uchraydigan xato

Liveness probe ichida ma'lumotlar bazasini tekshirmang. Baza 30 soniya sekinlashsa, Kubernetes **barcha** Pod'larni birdaniga qayta ishga tushiradi va kichik muammo to'liq uzilishga aylanadi. Tashqi bog'liqliklar faqat readiness'da tekshiriladi.

Keyingi qismda Ingress va TLS sertifikatlarini sozlaymiz.
