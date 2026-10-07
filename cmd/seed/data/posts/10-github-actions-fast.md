---
title: GitHub Actions pipeline'ni 18 daqiqadan 4 daqiqaga tushirish
subtitle: Cache, parallel job'lar va o'zgargan fayllar bo'yicha filtrlash
author: sardor
category: CI/CD
tags: github-actions, ci, cicd, performance
labels: Tavsiya etiladi
publication: devopsxlab-weekly
days_ago: 84
cover: speed-motion
---
Sekin CI — bu shunchaki noqulaylik emas. Dasturchilar kichik PR'lar o'rniga katta PR'lar ochishni boshlaydi, chunki har bir push 18 daqiqa kutish demak.

## 1. Dependency cache

```yaml
- uses: actions/setup-go@v6
  with:
    go-version-file: go.mod
    cache: true
- uses: oven-sh/setup-bun@v2
- uses: actions/cache@v4
  with:
    path: ~/.bun/install/cache
    key: bun-${{ hashFiles('**/bun.lock') }}
```

Faqat shu qadam 3 daqiqa tejadi.

## 2. Docker layer cache

```yaml
- uses: docker/build-push-action@v6
  with:
    push: true
    tags: ghcr.io/acme/api:${{ github.sha }}
    cache-from: type=gha
    cache-to: type=gha,mode=max
```

## 3. Ketma-ketlik emas, parallellik

Lint, unit test, build bir-biriga bog'liq emas — ularni alohida job qiling. Testlarni bo'laklarga bo'ling:

```yaml
strategy:
  matrix:
    shard: [1, 2, 3, 4]
steps:
  - run: go test $(go list ./... | awk "NR % 4 == ${{ matrix.shard }} - 1")
```

## 4. Faqat o'zgargan qismni tekshirish

Monorepo'da frontend o'zgarsa, backend testlari kerak emas:

```yaml
on:
  pull_request:
    paths:
      - 'Backend/**'
      - '.github/workflows/backend.yml'
```

## 5. Eskirgan ishga tushirishlarni bekor qilish

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true
```

Bir PR'ga ketma-ket 3 marta push qilsangiz, faqat oxirgisi ishlaydi.

## 6. Kattaroq runner

Ba'zan eng arzon optimizatsiya — 4 yadroli o'rniga 16 yadroli runner. Daqiqa narxi yuqori, lekin umumiy vaqt va pul kamroq.

## Natija

| Bosqich | Oldin | Keyin |
|---|---|---|
| Dependencies | 3:10 | 0:15 |
| Test | 9:40 | 2:30 |
| Docker build | 5:20 | 1:05 |
| **Jami** | **18:10** | **3:50** |

Har bir optimizatsiyadan keyin o'lchang — taxmin qilmang.
