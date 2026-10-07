---
title: Core Web Vitals: LCP, INP va CLS'ni real foydalanuvchida o'lchash
subtitle: Lighthouse 100 ball bersa ham foydalanuvchilar nega shikoyat qiladi
author: kamola
category: Dasturlash
tags: frontend, performance, web-vitals, react
labels: Chuqur tahlil
days_ago: 66
cover: mobile-phone-hand
---
Lighthouse noutbukingizda tez Wi-Fi bilan 100 ball ko'rsatadi. Foydalanuvchi esa Andijonda 4G va 3 yillik Android telefonda saytni ochmoqda. Haqiqiy ma'lumot faqat haqiqiy foydalanuvchidan keladi.

## Uchta metrika

| Metrika | Nima o'lchaydi | Yaxshi |
|---|---|---|
| **LCP** | Asosiy kontent qachon ko'rindi | ≤ 2.5 s |
| **INP** | Bosish/yozishga javob tezligi | ≤ 200 ms |
| **CLS** | Sahifa "sakrashi" | ≤ 0.1 |

Baholash 75-persentil bo'yicha: foydalanuvchilarning 75 foizi "yaxshi" chegarada bo'lishi kerak.

## RUM: real foydalanuvchi monitoringi

```js
import { onLCP, onINP, onCLS } from 'web-vitals/attribution';

function send(metric) {
  navigator.sendBeacon('/api/vitals', JSON.stringify({
    name: metric.name,
    value: metric.value,
    rating: metric.rating,
    page: location.pathname,
    target: metric.attribution?.interactionTarget,
  }));
}

onLCP(send);
onINP(send);
onCLS(send);
```

`attribution` versiyasi aynan qaysi element sekin ekanini aytadi.

## LCP'ni yaxshilash

Ko'pincha LCP elementi — muqova rasmi.

```html
<link rel="preload" as="image" href="/cover.webp" fetchpriority="high">
<img src="/cover.webp" width="1200" height="630" fetchpriority="high" alt="">
```

Muqova rasmiga hech qachon `loading="lazy"` qo'ymang. WebP/AVIF va `srcset` bilan mobil uchun kichik o'lcham bering.

## INP'ni yaxshilash

INP — eng yangi va eng qiyin metrika. Sabab odatda asosiy oqimdagi uzun vazifalar.

```js
async function onFilterChange(value) {
  setInputValue(value);          // darhol javob
  await scheduler.yield();       // brauzerga chizishga imkon
  setResults(expensiveFilter(value));
}
```

React'da `useTransition` / `useDeferredValue` xuddi shu vazifani bajaradi.

## CLS'ni yaxshilash

- Har bir `img` va `video` uchun `width`/`height` yoki `aspect-ratio`.
- Reklama va banner joylari uchun oldindan joy ajrating.
- Shriftlar uchun `font-display: optional` yoki `size-adjust`.

Dashboard'da metrikalarni sahifa turi va qurilma bo'yicha ajrating — o'rtacha qiymat muammoni yashiradi.
