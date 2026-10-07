---
title: Redis kesh: tezlik va noto'g'ri ma'lumot o'rtasidagi muvozanat
subtitle: Cache-aside, TTL, stampede va invalidatsiya bo'yicha amaliy tajriba
author: bekzod
category: Ma'lumotlar bazasi
tags: redis, caching, performance, backend
labels: Qo'llanma
days_ago: 22
cover: lightning-speed
---
"Kompyuter fanida ikkita qiyin narsa bor: kesh invalidatsiyasi va nomlash." Hazil emas — keshdagi xatolar eng qiyin topiladigan xatolar.

## Cache-aside

```go
func (r *Repo) Post(ctx context.Context, id int) (Post, error) {
	key := fmt.Sprintf("post:%d", id)
	if b, err := r.rdb.Get(ctx, key).Bytes(); err == nil {
		var p Post
		if json.Unmarshal(b, &p) == nil {
			return p, nil
		}
	}
	p, err := r.db.Post(ctx, id)
	if err != nil {
		return Post{}, err
	}
	b, _ := json.Marshal(p)
	r.rdb.Set(ctx, key, b, 5*time.Minute+jitter())
	return p, nil
}
```

Yozishda keshni yangilamang — **o'chiring**. Yangilash poyga holatiga olib keladi: ikki parallel yozish keshda eski qiymatni qoldirishi mumkin.

## TTL har doim bo'lsin

Invalidatsiyani unutgan joyingiz bo'ladi. TTL — sug'urta: eng yomon holatda ma'lumot 5 daqiqa eskiradi, abadiy emas.

`jitter()` — tasodifiy ±10%. Aks holda bir vaqtda yozilgan minglab kalitlar bir vaqtda tugaydi.

## Cache stampede

Mashhur kalit tugadi va 2000 so'rov bir vaqtda bazaga yugurdi. Yechim — bitta so'rov hisoblasin, qolganlari kutsin:

```go
var g singleflight.Group

v, err, _ := g.Do(key, func() (any, error) {
	return r.loadAndCache(ctx, id)
})
```

Ko'p instansiya bo'lsa, Redis'da qisqa lock: `SET lock:post:42 1 NX PX 3000`.

## Kesh kalitlarini versiyalang

Struct o'zgardimi? Eski formatdagi JSON deploy'dan keyin xato beradi. Kalitga versiya qo'shing: `v3:post:42`. Deploy — yangi kalitlar, eskilari TTL bilan o'zi yo'qoladi.

## Nimani keshlamaslik kerak

- Har bir foydalanuvchiga xos va kam o'qiladigan ma'lumot — hit rate past bo'ladi.
- Pul va huquqlar bilan bog'liq qarorlar — eskirgan ruxsat xavfsizlik teshigi.

Hit rate'ni metrikaga chiqaring. 60 foizdan past bo'lsa, kesh ehtimol foydadan ko'ra murakkablik qo'shyapti.
