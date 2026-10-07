---
title: Kubernetes noldan: Pod, Deployment va Service
subtitle: Klasterdagi uchta asosiy obyektni amalda tushunib olamiz
author: azizbek
category: Kubernetes
tags: kubernetes, k8s, deployment, beginners
labels: Qo'llanma, Yangi boshlovchilar
publication: devopsxlab-weekly
series: kubernetes-noldan
days_ago: 118
cover: kubernetes-cluster
---
Kubernetes'ni o'rganishni boshlaganlar odatda yuzlab obyekt turini ko'rib qo'rqib ketishadi. Aslida kundalik ishning 80 foizi uchta obyekt atrofida aylanadi: **Pod**, **Deployment** va **Service**.

## Pod — eng kichik birlik

Pod — bitta yoki bir nechta konteynerdan iborat guruh. Ular bitta tarmoq nomlar maydonini (IP, portlar) va volume'larni bo'lishadi.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: web
  labels:
    app: web
spec:
  containers:
    - name: nginx
      image: nginx:1.29-alpine
      ports:
        - containerPort: 80
```

Pod'ni qo'lda yaratish deyarli hech qachon kerak bo'lmaydi: u o'lsa, uni hech kim qayta ko'tarmaydi.

## Deployment — kerakli holatni saqlash

Deployment "menga doim 3 ta nusxa kerak" degan istakni ifodalaydi. Controller ReplicaSet orqali Pod'lar sonini kuzatib turadi va yangi image chiqqanda **rolling update** qiladi.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web
  strategy:
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: nginx
          image: nginx:1.29-alpine
          resources:
            requests: { cpu: 50m, memory: 64Mi }
            limits: { memory: 128Mi }
```

`maxUnavailable: 0` — yangilanish paytida birorta ham nusxa kamaymaydi, foydalanuvchi uzilishni sezmaydi.

## Service — barqaror manzil

Pod'larning IP manzili har qayta yaratilganda o'zgaradi. Service label selector orqali Pod'larni topib, ularga barqaror DNS nom beradi: `web.default.svc.cluster.local`.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector:
    app: web
  ports:
    - port: 80
      targetPort: 80
```

## Tekshirib ko'ramiz

```bash
kubectl apply -f web.yaml
kubectl get deploy,rs,pods -l app=web
kubectl rollout status deploy/web
kubectl set image deploy/web nginx=nginx:1.29.1-alpine
kubectl rollout undo deploy/web   # muammo bo'lsa, ortga qaytamiz
```

Keyingi qismda ConfigMap, Secret va probe'lar bilan ilovani ishlab chiqarishga tayyorlaymiz.
