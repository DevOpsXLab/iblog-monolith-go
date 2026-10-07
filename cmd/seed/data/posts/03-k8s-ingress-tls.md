---
title: Kubernetes noldan: Ingress, cert-manager va bepul TLS
subtitle: Ilovani domen orqali HTTPS bilan tashqi dunyoga chiqaramiz
author: azizbek
category: Kubernetes
tags: kubernetes, ingress, tls, cert-manager
labels: Qo'llanma
publication: devopsxlab-weekly
series: kubernetes-noldan
days_ago: 89
cover: network-cables
---
Service klaster ichida ishlaydi. Foydalanuvchilar esa `https://app.example.uz` orqali kirishi kerak. Buning uchun Ingress controller va avtomatik TLS sertifikat kerak.

## Ingress controller o'rnatish

Ingress obyekti o'zi hech narsa qilmaydi — uni o'qib, trafikni yo'naltiradigan controller kerak. Eng keng tarqalgani ingress-nginx:

```bash
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx --create-namespace
```

Yangi loyihalar uchun **Gateway API** ga ham qarang — u Ingress'ning vorisi va rollarni (infra jamoasi / ilova jamoasi) yaxshiroq ajratadi.

## cert-manager va Let's Encrypt

```bash
helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --namespace cert-manager --create-namespace --set crds.enabled=true
```

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: ops@example.uz
    privateKeySecretRef: { name: letsencrypt-key }
    solvers:
      - http01:
          ingress: { ingressClassName: nginx }
```

## Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
spec:
  ingressClassName: nginx
  tls:
    - hosts: [app.example.uz]
      secretName: web-tls
  rules:
    - host: app.example.uz
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: { name: web, port: { number: 80 } }
```

Bir-ikki daqiqada `kubectl get certificate` holati `Ready=True` bo'ladi. Sertifikat muddati tugashidan 30 kun oldin avtomatik yangilanadi.

## Tekshiruv ro'yxati

- DNS A yozuvi load balancer IP'siga qaraganmi?
- 80-port ochiqmi? (HTTP-01 challenge unga muhtoj)
- Staging issuer bilan sinab ko'ring — Let's Encrypt prod'da rate limit bor.

Seriya shu yerda tugaydi. Endi sizda haqiqiy ilovani Kubernetes'da ishga tushirish uchun minimal, lekin to'liq zanjir bor.
