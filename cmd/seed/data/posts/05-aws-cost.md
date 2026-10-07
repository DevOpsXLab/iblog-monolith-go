---
title: AWS hisobini 40% kamaytirgan 7 ta oddiy qadam
subtitle: Arxitekturani buzmasdan, bir oyda qilingan real optimizatsiya
author: dilnoza
category: Cloud
tags: aws, finops, cost, cloud
labels: Tavsiya etiladi
publication: cloud-native-uz
days_ago: 76
cover: coins-calculator
---
O'tgan chorakda AWS hisobimiz oyiga 18 ming dollardan oshdi. Hech qanday katta qayta yozishsiz, bir oy ichida uni 10.8 mingga tushirdik. Mana nima qildik.

## 1. Cost Explorer'ni teglar bo'yicha oching

`team`, `service`, `env` teglarini majburiy qildik (SCP + Tag Policies). Bir haftadan keyin xarajatlarning 23 foizi "hech kimniki" ekanligi ma'lum bo'ldi — eski test muhitlari.

## 2. NAT Gateway — yashirin qotil

Oyiga 2.1 ming dollar NAT Gateway trafikiga ketayotgan edi. Sabab: Pod'lar S3 va ECR'ga NAT orqali chiqardi.

```hcl
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.main.id
  service_name      = "com.amazonaws.eu-central-1.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id
}
```

S3 Gateway endpoint bepul. ECR uchun Interface endpoint qo'shdik. NAT xarajati 70% kamaydi.

## 3. Graviton'ga o'tish

Go va Node servislarimiz arm64'da muammosiz ishladi. `m6i` dan `m7g` ga o'tish — bir xil ishlash, ~20% arzon. Docker image'larni `docker buildx --platform linux/amd64,linux/arm64` bilan yig'dik.

## 4. Savings Plans

Barqaror bazaviy yuklamaning 70 foiziga 1 yillik Compute Savings Plan oldik. Qolgan qismi on-demand va spot.

## 5. Spot'da CI runner'lar

GitHub Actions self-hosted runner'larini Karpenter orqali spot node'larda ishga tushirdik. CI xarajati uch barobar kamaydi.

## 6. CloudWatch Logs retention

Ko'p log guruhlarida retention "Never expire" edi. 14 kunga tushirdik, eskilarini S3 Glacier'ga eksport qildik.

## 7. gp2 → gp3

Bitta buyruq bilan barcha EBS disklarni gp3'ga o'tkazdik: 20% arzon va IOPS alohida sozlanadi.

```bash
aws ec2 describe-volumes --filters Name=volume-type,Values=gp2 \
  --query 'Volumes[].VolumeId' --output text |
  xargs -n1 -I{} aws ec2 modify-volume --volume-id {} --volume-type gp3
```

Asosiy saboq: xarajat — bu ham metrika. Uni Grafana dashboard'ga chiqaring va har hafta ko'rib chiqing.
