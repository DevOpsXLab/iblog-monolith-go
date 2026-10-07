---
title: SSH'ni to'g'ri sozlash: parollardan sertifikatlargacha
subtitle: Yangi serverda birinchi 10 daqiqada qilinadigan xavfsizlik ishlari
author: timur
category: Linux
tags: linux, ssh, security, hardening
labels: Qo'llanma
days_ago: 34
cover: server-rack
---
Ochiq 22-portli yangi VPS internetga chiqqandan keyin o'rtacha 2-3 daqiqada birinchi brute-force urinishlari boshlanadi. `journalctl -u ssh` ga qarang — ishonasiz.

## Minimal sshd_config

```
# /etc/ssh/sshd_config.d/10-hardening.conf
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
AllowGroups ssh-users
MaxAuthTries 3
LoginGraceTime 20
X11Forwarding no
```

```bash
sudo sshd -t && sudo systemctl reload ssh
```

> Joriy sessiyani yopmang! Yangi terminalda kirib ko'ring. Aks holda o'zingizni serverdan qulflab qo'yishingiz mumkin.

## Kalit turi

```bash
ssh-keygen -t ed25519 -C "timur@laptop"
```

RSA 2048 eskirgan. Ed25519 — qisqa, tez va xavfsiz. Iloji bo'lsa, kalitni apparat tokenda saqlang: `ssh-keygen -t ed25519-sk`.

## Jamoa uchun: SSH sertifikatlari

10 ta server va 15 ta muhandis — `authorized_keys` boshqaruvi dahshatga aylanadi. Kimdir ketdi — 10 ta serverdan kalitini o'chirish kerak.

SSH CA bilan har bir serverga faqat CA ochiq kaliti qo'yiladi:

```bash
# CA yaratish (bir marta, xavfsiz joyda)
ssh-keygen -t ed25519 -f ssh_ca

# Muhandisga 8 soatlik sertifikat berish
ssh-keygen -s ssh_ca -I timur -n timur -V +8h ~/.ssh/id_ed25519.pub
```

Serverda:

```
TrustedUserCAKeys /etc/ssh/ssh_ca.pub
```

Sertifikat 8 soatdan keyin o'zi yaroqsiz bo'ladi. Ketgan xodimning kalitini o'chirish shart emas — unga yangi sertifikat berilmaydi, xolos. Teleport, Vault SSH yoki step-ca bu jarayonni SSO bilan avtomatlashtiradi.

## Qo'shimcha

- `fail2ban` yoki `sshguard` — takroriy urinishlarni bloklaydi.
- Ochiq portni umuman yopish — Tailscale/WireGuard orqali kirish yoki AWS SSM Session Manager.
- `unattended-upgrades` — xavfsizlik yamalarini avtomatik o'rnatish.
