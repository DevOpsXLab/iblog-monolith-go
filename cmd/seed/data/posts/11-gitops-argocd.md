---
title: GitOps amalda: Argo CD bilan deploy qilish
subtitle: kubectl apply'dan voz kechib, Git'ni yagona haqiqat manbaiga aylantiramiz
author: sardor
category: CI/CD
tags: gitops, argocd, kubernetes, cicd
labels: Qo'llanma
days_ago: 58
cover: git-branches
---
Klasselik CI/CD'da pipeline klasterga `kubectl apply` qiladi. Demak CI'da klasterning admin huquqi bor va klaster holatini faqat oxirgi pipeline log'idan bilish mumkin.

GitOps buni teskari qiladi: klaster ichidagi agent Git'ni kuzatadi va farqni o'zi tuzatadi.

## Argo CD o'rnatish

```bash
kubectl create namespace argocd
kubectl apply -n argocd --server-side -f \
  https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
```

## Application

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: api-prod
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/acme/deploy.git
    targetRevision: main
    path: apps/api/overlays/prod
  destination:
    server: https://kubernetes.default.svc
    namespace: api
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions: [CreateNamespace=true]
```

`selfHeal: true` — kimdir klasterda qo'lda o'zgartirsa, Argo CD uni Git'dagi holatga qaytaradi.

## Ikki repo qoidasi

- **app repo** — kod, Dockerfile, testlar. CI image yig'adi.
- **deploy repo** — Kustomize/Helm manifestlari. CI bu yerga faqat image tag'ini yangilaydi:

```bash
cd apps/api/overlays/prod
kustomize edit set image api=ghcr.io/acme/api@${DIGEST}
git commit -am "api: deploy ${SHA}" && git push
```

Yoki Argo CD Image Updater buni avtomatik qiladi.

## Rollback = git revert

Muammo chiqdimi? `git revert` va push. Audit log tayyor: kim, qachon, nima uchun — barchasi commit tarixida.

## Ko'p klaster

ApplicationSet bitta shablondan dev, staging, prod klasterlari uchun Application'lar yaratadi. Yangi klaster qo'shish — bitta label.

GitOps'ning eng katta foydasi texnik emas: jamoa infratuzilma o'zgarishlarini ham kod kabi review qilishni boshlaydi.
