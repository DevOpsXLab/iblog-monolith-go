---
title: Supply chain xavfsizligi: SBOM, imzo va provenance
subtitle: Prod'da aynan siz yig'gan image ishlayotganini qanday isbotlash mumkin
author: malika
category: Xavfsizlik
tags: security, devsecops, sigstore, sbom
labels: Chuqur tahlil
days_ago: 91
cover: padlock-digital
---
xz-utils voqeasidan keyin "biz faqat o'z kodimizni tekshiramiz" degan yondashuv yetarli emasligi hammaga ayon bo'ldi. Bugun image'ingizning 90 foizi boshqalar yozgan kod.

## Uch savol

1. **Image ichida nima bor?** — SBOM
2. **Uni kim yig'gan?** — imzo
3. **Qayerda va qanday yig'ilgan?** — provenance (SLSA)

## SBOM yaratish

```bash
syft ghcr.io/acme/api:1.4.2 -o spdx-json > sbom.json
grype sbom:sbom.json --fail-on high
```

Docker BuildKit buni build paytida o'zi qila oladi:

```bash
docker buildx build --sbom=true --provenance=mode=max -t ghcr.io/acme/api:1.4.2 --push .
```

## Kalitsiz imzolash (Sigstore)

GitHub Actions OIDC tokeni orqali uzoq muddatli kalit saqlamasdan imzolaymiz:

```yaml
permissions:
  id-token: write
  packages: write
steps:
  - uses: sigstore/cosign-installer@v3
  - run: cosign sign --yes ghcr.io/acme/api@${{ steps.build.outputs.digest }}
```

Imzo Rekor shaffoflik jurnaliga yoziladi — kim, qachon, qaysi workflow'dan imzolagani ochiq ko'rinadi.

## Klasterda tekshirish

Imzolash — yarim ish. Kubernetes imzosiz image'ni rad etishi kerak. Kyverno siyosati:

```yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: verify-images
spec:
  validationFailureAction: Enforce
  rules:
    - name: signed-by-ci
      match:
        any:
          - resources: { kinds: [Pod] }
      verifyImages:
        - imageReferences: ["ghcr.io/acme/*"]
          attestors:
            - entries:
                - keyless:
                    issuer: https://token.actions.githubusercontent.com
                    subject: https://github.com/acme/*/.github/workflows/release.yml@refs/heads/main
```

## Tag emas, digest

`api:latest` yoki hatto `api:1.4.2` o'zgarishi mumkin. Manifestlarda doim digest ishlating: `api@sha256:...`. Renovate bot digest'larni avtomatik yangilaydi.

Kichik qadamdan boshlang: bugun CI'ga `grype --fail-on critical` qo'shing.
