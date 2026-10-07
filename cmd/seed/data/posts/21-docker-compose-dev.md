---
title: Docker Compose bilan lokal muhit: "mening kompyuterimda ishlaydi"ga chek qo'yamiz
subtitle: healthcheck, depends_on, profillar va compose watch
author: sardor
category: Docker
tags: docker, docker-compose, developer-experience, beginners
labels: Yangi boshlovchilar, Qo'llanma
days_ago: 28
cover: laptop-desk
---
Yangi dasturchi jamoaga qo'shildi. README'da 23 qadam: Postgres o'rnating, Redis o'rnating, bu versiyani, u portni... Ikkinchi kuni ham loyiha ishga tushmadi.

Maqsad: `git clone` va `docker compose up` — bo'ldi.

## Asosiy compose.yaml

```yaml
services:
  api:
    build: .
    ports: ["8080:8080"]
    environment:
      DATABASE_URL: postgres://app:app@postgres:5432/app?sslmode=disable
      REDIS_URL: redis://redis:6379/0
    depends_on:
      postgres: { condition: service_healthy }
      redis: { condition: service_healthy }

  postgres:
    image: postgres:18-alpine
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: app
    volumes: [pgdata:/var/lib/postgresql]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
      interval: 3s
      retries: 10

  redis:
    image: redis:8-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 3s

volumes:
  pgdata:
```

`depends_on` + `condition: service_healthy` — api Postgres haqiqatan tayyor bo'lgandagina ishga tushadi, faqat konteyner yaratilganda emas.

## Profillar: kerak bo'lganda yoqiladigan servislar

```yaml
  mailpit:
    image: axllent/mailpit
    ports: ["8025:8025"]
    profiles: [mail]

  grafana:
    image: grafana/otel-lgtm
    ports: ["3000:3000"]
    profiles: [observability]
```

```bash
docker compose --profile observability up
```

## compose watch: hot reload

```yaml
  web:
    build: ./web
    develop:
      watch:
        - action: sync
          path: ./web/src
          target: /app/src
        - action: rebuild
          path: ./web/package.json
```

```bash
docker compose watch
```

Kod o'zgarganda fayllar konteynerga sinxronlanadi, `package.json` o'zgarganda image qayta yig'iladi.

## Kichik maslahatlar

- Portlarni standartdan farqli qiling (`5433:5432`) — lokal o'rnatilgan Postgres bilan to'qnashmaydi.
- `.env.example` faylini repo'ga qo'ying, `.env` ni `.gitignore` ga.
- Seed ma'lumotlarini alohida buyruq qiling: `docker compose run --rm api seed`.

Natija: yangi dasturchi birinchi soatda birinchi PR'ini ochdi.
