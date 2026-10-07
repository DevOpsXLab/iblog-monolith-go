---
title: Go'da graceful shutdown: so'rovlarni yo'qotmasdan to'xtash
subtitle: SIGTERM, http.Server.Shutdown va Kubernetes'dagi nozik jihatlar
author: jasur
category: Dasturlash
tags: go, golang, kubernetes, backend
labels: Chuqur tahlil
days_ago: 98
cover: code-on-screen
---
Har deploy'da bir nechta 502 xato ko'ryapsizmi? Ehtimol, ilovangiz SIGTERM kelganda darhol o'ladi va jarayondagi so'rovlar uziladi.

## Minimal to'g'ri variant

```go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	// keyin: worker'lar, DB pool, tracer flush
}
```

`Shutdown` yangi ulanishlarni qabul qilishni to'xtatadi va mavjud so'rovlar tugashini kutadi.

## Kubernetes'dagi poyga holati

Pod o'chirilganda ikki narsa **parallel** sodir bo'ladi:

1. kubelet konteynerga SIGTERM yuboradi;
2. Endpoint controller Pod'ni Service'dan olib tashlaydi, kube-proxy va ingress buni bir necha soniyadan keyin bilib oladi.

Agar ilova 1-qadamda darhol port'ni yopsa, 2-qadam hali tugamagan bo'ladi va trafik yopiq port'ga boradi. Yechim — readiness'ni o'chirib, biroz kutish:

```go
<-ctx.Done()
ready.Store(false)          // /readyz endi 503 qaytaradi
time.Sleep(5 * time.Second) // load balancer'lar yangilanishiga vaqt
srv.Shutdown(shutdownCtx)
```

Yoki Kubernetes 1.30+ da `preStop: sleep` hook:

```yaml
lifecycle:
  preStop:
    sleep: { seconds: 5 }
terminationGracePeriodSeconds: 35
```

`terminationGracePeriodSeconds` sizning shutdown timeout + preStop vaqtidan katta bo'lishi shart, aks holda SIGKILL keladi.

## Uzoq so'rovlar va streaming

SSE yoki WebSocket ulanishlar `Shutdown` ni bloklab qo'yadi. `srv.RegisterOnShutdown` orqali ularga "qayta ulaning" signalini yuboring va context'ni bekor qiling.

Shu uch qator kod bizning deploy'lardagi xatolarni noldan kam qilmadi — to'g'ridan-to'g'ri nolga tushirdi.
