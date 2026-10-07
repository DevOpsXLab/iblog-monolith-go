---
title: Terraform state: jamoada xavfsiz ishlash qoidalari
subtitle: Remote backend, locking va state'ni bo'laklarga ajratish
author: dilnoza
category: Cloud
tags: terraform, iac, aws, opentofu
labels: Chuqur tahlil, Tavsiya etiladi
publication: cloud-native-uz
days_ago: 112
cover: cloud-architecture
---
Terraform'dagi eng og'riqli hodisalar deyarli har doim state fayli bilan bog'liq: ikki kishi bir vaqtda `apply` qildi, kimdir state'ni noutbukida qoldirdi yoki bitta ulkan state'ni yangilash 15 daqiqa oldi.

## 1. State hech qachon lokal bo'lmasin

```hcl
terraform {
  backend "s3" {
    bucket       = "acme-tfstate"
    key          = "prod/network/terraform.tfstate"
    region       = "eu-central-1"
    encrypt      = true
    use_lockfile = true
  }
}
```

Terraform 1.10+ da S3 o'zi native locking qiladi (`use_lockfile`), DynamoDB jadvali endi shart emas. Bucket'da versioning yoqilgan bo'lsin — buzilgan state'ni oldingi versiyadan tiklash mumkin.

## 2. State'ni bo'laklang

Bitta state'da butun infratuzilma — bu katta "blast radius". Odatiy bo'linish:

```
live/
  prod/
    network/     # VPC, subnet, NAT
    data/        # RDS, ElastiCache
    platform/    # EKS, IAM
    apps/        # har bir servis alohida
```

Qatlamlar o'rtasida ma'lumot `terraform_remote_state` yoki yaxshisi SSM Parameter Store orqali uzatiladi.

## 3. Sirlar state'ga tushadi

`aws_db_instance` parolini kiritsangiz, u state'da ochiq matnda saqlanadi. Yechimlar:

- `manage_master_user_password = true` — parolni Secrets Manager boshqaradi;
- `ephemeral` resurslar va write-only argumentlar (Terraform 1.11+);
- state bucket'ga kirishni IAM bilan qattiq cheklash.

## 4. apply faqat CI'dan

Lokal `apply` — kim nima o'zgartirganini bilmaslik demak. Oqim:

1. PR ochiladi — CI `terraform plan` natijasini izohga yozadi.
2. Review va tasdiq.
3. `main` ga merge — CI `apply` qiladi.

## 5. Drift'ni kuzating

Har kecha `terraform plan -detailed-exitcode` ishga tushiring. Exit kodi 2 bo'lsa — kimdir konsolda qo'lda nimanidir o'zgartirgan. Slack'ga xabar yuboring.

OpenTofu ishlatsangiz ham, bu qoidalarning barchasi bir xil amal qiladi.
