# ติดตั้งและอัปเกรด Gateway ด้วยหน้าเว็บแบบ C

Gateway เป็นโปรเจกต์แยกจาก Hospital API ใช้ HTTPS/reverse proxy เดิม รุ่นที่มี web setup เปิด container ได้ก่อนมี encryption key, บัญชีผู้ดูแล หรือใบ mTLS โดยใช้รหัสตั้งค่าจากเครื่อง Gateway การเริ่ม container ไม่ได้สร้าง CA หรือทำให้ Hospital เดิมเชื่อถือใบใหม่โดยอัตโนมัติ

## เตรียม release ให้ตรง CPU

ตรวจ `uname -m` ที่ Linux ปลายทาง: `x86_64` ใช้ `linux/amd64`; `aarch64`/`arm64` ใช้ `linux/arm64` อย่าเลือกตาม CPU ของ Mac

บนเครื่อง build ที่มี Bash, Python 3, Docker Buildx และ source ที่เชื่อถือได้:

```bash
cd ~/dev/erp-platform/gateway
./deploy/gateway/build-gateway.sh
```

หรือระบุ CPU และ tag ใหม่ที่ไม่ซ้ำ:

```bash
./deploy/gateway/build-gateway.sh linux/amd64 hospital-erp-gateway-local:20261125-websetup-amd64
```

ผลลัพธ์ `deploy/release/gateway-release.tar` รวม image, checksums, metadata, Compose, template ว่าง, installer, upgrade guard และคู่มือ ไม่รวม DB, `.env` จริง, private key, setup code หรือ CA key การ build อาจโหลด base images/Go modules เมื่อยังไม่มีใน cache และไม่ publish image ออก registry

ส่ง bundle ไป Linux และส่ง **Bundle SHA-256 ผ่านช่องทางที่เชื่อถือได้แยกกัน** ตรวจ hash ก่อนแตก ห้ามแตกทับชุดที่ใช้งานอยู่:

```bash
BUNDLE=gateway-release.tar
BUNDLE_SHA='<trusted bundle SHA-256>'
printf '%s  %s\n' "$BUNDLE_SHA" "$BUNDLE" | sha256sum -c -
test ! -e gateway-release
test ! -L gateway-release
tar -xf "$BUNDLE"
cd gateway-release
```

หากคำสั่งตรวจสอบใดไม่ผ่าน ให้หยุดก่อนแตก/ติดตั้ง Linux ต้องมี Docker Engine, Compose v2, Bash และ Python 3 และใช้ Docker context แบบ local Unix socket

## ระบบใหม่: เปิด container แล้วตั้งค่าผ่านเว็บ

**ใช้ลำดับนี้เฉพาะระบบใหม่และ volume ว่าง** image ต้องเป็น release ที่มี web setup รุ่นนี้ ห้ามนำ `0.1.0` เก่ามาใช้แทน

วิธีแนะนำคือ installer ซึ่งตรวจ checksum/CPU/tag/entrypoint ก่อนโหลด image และเริ่มเฉพาะ Gateway โดยไม่ build/pull:

```bash
bash install-gateway.sh
```

ไม่ต้องสร้าง encryption key, admin password หรือไฟล์ client certificate ก่อน installer สร้าง `.env` จาก template ว่างแบบไม่ทับไฟล์เดิม ตัว bundle ตั้ง image tag/default ให้ตรง release ที่ตรวจแล้ว

หากใช้ Compose โดยตรง หลังตรวจแหล่งที่มา/CPU/metadata ของ release และ checksum แล้ว ลำดับหลักคือ:

```bash
sha256sum -c gateway-image.tar.sha256
docker load --input gateway-image.tar
docker compose up -d
docker compose exec gateway /usr/local/bin/gateway setup-code
```

Installer ตรวจการรักษา installation เดิมเพิ่มเติม วิธี Compose โดยตรงไม่ทำ upgrade guard ให้ อย่าใช้ flow ระบบใหม่กับ volume ที่มีข้อมูลเก่า

เปิด `http://localhost:8080/setup/` ที่เครื่อง Gateway ถ้าเข้าจาก Mac ให้ใช้ SSH tunnel ไปยังพอร์ต loopback เช่น:

```bash
ssh -L 18080:127.0.0.1:8080 administrator@gateway-server
```

แล้วเปิด `http://localhost:18080/setup/` ใช้ setup code อายุ 10 นาที ใช้ครั้งเดียว ไม่ใส่ code ใน URL, `.env` หรือ logs การขอ code ใหม่ทำจากเครื่องที่มีสิทธิ์ Docker เท่านั้น

หน้าแบบ C นำทางตั้งบัญชีผู้ดูแล, public HTTPS URL เดิม, allowlist/เครือข่าย Hospital, client mTLS และตรวจความพร้อม ระบบใหม่สร้าง encryption key สุ่มครั้งเดียวใน `gateway-config` และเก็บ password verifier แทนรหัสผ่านจริง หลัง activate เข้า Admin ผ่าน HTTPS URL ที่ตั้งไว้ เพราะ cookie เป็น Secure

หากจะใช้ HTTPS proxy ตั้งแต่หน้า setup ให้ตั้ง `GATEWAY_SETUP_PUBLIC_URL` และ `GATEWAY_TRUSTED_PROXY_CIDRS` เป็น proxy จริงก่อนเริ่ม ดู `WEB_SETUP.md` ค่า bind เริ่มต้นยังเป็น `127.0.0.1`; เปลี่ยน bind แล้วไม่ได้เปิด public HTTP setup และไม่ต้องเปลี่ยน HTTPS certificate ของโดเมน Gateway

## mTLS: สร้าง CSR ในเว็บ แล้วใช้ authority เดิมลงนาม

1. สร้าง Gateway key/CSR ผ่านเว็บ Private key อยู่ใน protected config volume ของ Gateway ดาวน์โหลดเฉพาะ CSR
2. ให้ CA ที่ Hospital เชื่อถืออยู่แล้วลงนามเป็น `clientAuth` บนเครื่องผู้ดูแลเดิม ห้ามย้าย offline CA private key เข้า Gateway
3. Import certificate chain ที่เซ็นแล้ว รวม issuer/root ที่ใช้ตรวจ chain กลับผ่านเว็บ ระบบตรวจ key match, chain, clientAuth และวันหมดอายุก่อน activate
4. ตรวจ public CA/SPKI ให้ตรงกับ trust ของ Hospital แล้วทดสอบไซต์นำร่องจากหน้า Admin ก่อนอนุมัติ/ขยายการใช้งาน

ตัวอย่างใช้ CA helper เดิมบน Mac กับ OpenSSL 3 และ CSR ที่ตรวจที่มาแล้ว:

```bash
cd ~/dev/erp-platform/gateway
CA="$HOME/.hospital-erp-pki/central-gateway-client-ca"
bash deploy/gateway/issue-gateway-client-cert.sh sign "$CA" \
  /path/to/downloaded-gateway-client.csr /path/to/gateway-client-chain.next.pem
```

ใช้ CA เดิมที่สำรองไว้ ไม่รัน `init` เพื่อแทน CA เดิม ถ้ายังไม่เคยมี authority ให้จัดเตรียม trust ครั้งแรกตาม `deploy/gateway/GATEWAY_CLIENT_CA.md` ใน source บน Mac ก่อนใช้งานจริง ใบเริ่มต้นอายุ 90 วัน

Hospital รับเฉพาะ **public client CA และ Gateway client SPKI pin** ไม่รับ Gateway private key ส่วน Hospital server certificate/private key เป็นอีก identity หนึ่ง Server private key ต้องอยู่เครื่อง Hospital หาก Hospital ใช้ private server CA ให้นำ public server CA เข้า setup ไม่ใช้ client CA แทน server trust

Delegated issuer/การต่ออายุอัตโนมัติต้องตั้ง authority แยกต่างหาก รุ่นนี้ใช้ CSR/import/activate กับ offline CA เดิมได้ ไม่อ้างว่ากดออกใบที่ลูกค้าเก่าเชื่อถือได้ทันที และไม่ออก self-signed production leaf แทน

## อัปเกรดระบบเดิม: รักษา key, project, volume และ mounts

สำรอง SQLite แบบ consistent รวม DB/WAL ตามวิธีที่ผู้ดูแลใช้อยู่ พร้อม encryption key, `.env`, TLS identity และ `gateway-config` ถ้ามี **ห้าม `down -v`, เปลี่ยนชื่อ project/volume หรือสุ่ม encryption key ใหม่เพื่อให้ error หาย**

แตก release ใหม่ในโฟลเดอร์ใหม่ คง project `erp-mobile-gateway` และ volume `erp-mobile-gateway_gateway-data` เดิม อัปเดตเฉพาะ `GATEWAY_IMAGE` ใน `.env` เดิมให้ตรง tag ใหม่ แล้วใช้ `--env-file` ชี้ไฟล์เดิมอย่างชัดเจน

Gateway เก่าที่ bind client cert/key ใน base Compose ต้องเพิ่ม `--legacy-tls` แม้ label `config_files` เดิมมีเพียง `docker-compose.yml`:

```bash
bash install-gateway.sh --env-file /absolute/path/to/existing/.env --legacy-tls
```

คง overlay เดิมด้วย เช่น `--hospital-ca`, `--notifications` หรือ `--notifications-volume` (notifications ใช้ได้อย่างใดอย่างหนึ่ง):

```bash
bash install-gateway.sh --env-file /absolute/path/to/existing/.env \
  --legacy-tls --hospital-ca --notifications-volume
```

Installer ตรวจ actual mounts/environment ในหน่วยความจำโดยไม่พิมพ์ secrets และหยุด **ก่อนโหลด image/recreate** หาก key/admin/trust จะหาย, ลืม TLS overlay, source `/data`/`/config`/credential เปลี่ยน หรือสิทธิ์ mount เปลี่ยน Custom Compose ที่ไม่รองรับต้องตรวจ migration เอง อย่าลบ guard เพื่อให้ผ่าน

Legacy env mode คงอ่าน cert/key จาก bind เดิม รักษาสิทธิ์ให้ UID:GID `65532:65532` อ่านได้ตาม policy ของ host ไม่เปิด private key เป็น `644`/`777` ถ้าจะย้ายเข้า persisted config ให้ทำ explicit recovery/import ผ่านเว็บด้วย key/identity เดิมหลังสำรอง ไม่ใช้ installer เปลี่ยน identity เงียบ ๆ

ระบบที่มี `runtime.json` ใช้ persisted config ก่อน legacy env หากไฟล์/key หายหรือผิดจะเข้า recovery ไม่ initialize ทับข้อมูล เมื่อใช้ persisted mode สำรองทั้ง `gateway-data:/data` และ `gateway-config:/config` แยกอย่างปลอดภัยจาก offline CA backup

## Jenkins และปุ่มอัปเดต Hospital API

Jenkins build/publish เตรียม release ผู้ดูแลเลือกรุ่นและหนึ่ง/หลาย/ทุกหน่วยงานที่รองรับจาก Gateway แล้ว Updater ฝั่งลูกค้ารับงานเอง รุ่นนี้ส่ง **OCI image pin ด้วย manifest digest และ signed manifest** ส่วน signed TAR/resume/offline delivery ของ Updater ยังไม่รวม ไม่ใช้ Docker image config ID แทน registry manifest digest

ตั้งความเชื่อถือครั้งแรกบน Gateway:

```dotenv
GATEWAY_UPDATE_REPOSITORIES=<exact OCI repository names, comma-separated>
GATEWAY_UPDATE_SIGNING_PUBLIC_KEY=<base64 Ed25519 public key>
GATEWAY_RELEASE_PUBLISH_TOKEN_SHA256=<SHA-256 hex of dedicated Jenkins publisher token>
```

Private signing key อยู่ Jenkins แยกจาก CA key ส่งเฉพาะ public key ไป Gateway/Updater Publisher token publish ได้เฉพาะ catalog ที่ลายเซ็นผ่าน ไม่มีสิทธิ์เริ่ม rollout ค่า policy ว่างปิด publishing/dispatch นำ env เข้า process ตาม release procedure ที่ตรวจแล้ว ไม่แก้ DB โดยตรง

ใน workspace ดู `updater/README.md` และ `updater/deploy/jenkins/Jenkinsfile.API` สำหรับ pipeline/credentials/source-only build ชุดลูกค้าใช้ `api` กับ `updater` เป็นคนละ service ใน Compose project เดียว มีเพียง Updater ที่ได้รับ Docker socket Gateway และ Hospital API ไม่มี socket

Updater ต้อง pin **Compose ทุกไฟล์ตามลำดับเดิม** ใน `UPDATER_COMPOSE_FILES` รวม network, MySQL CA, uploads, notifications และ `compose.usage.yml` ถ้าเปิด analytics พร้อม `UPDATER_COMPOSE_ENV_FILE` หากใช้ env file เฉพาะ มันตรวจ runtime inventory และ block หากไฟล์/volumes/networks ไม่ตรง ไม่ทิ้ง overlay เพื่อเปลี่ยนเฉพาะ image

Usage/บุคลากรแสดงเมื่อ Hospital API รุ่นที่รองรับส่ง aggregate แล้ว `compose.usage.yml` เก็บ usage DB ใน volume ของ API เมื่อไม่มีข้อมูล Gateway แสดงว่ายังไม่มีข้อมูล ไม่ตีความเป็นศูนย์หรือยอดดาวน์โหลด Store

## ตรวจหลังเริ่มระบบ

```bash
curl -fsS http://127.0.0.1:8080/healthz
docker compose ps
docker compose logs --tail 50 gateway
```

ใช้พอร์ต/Compose options จริง Health อาจเป็น setup/recovery หรือ process healthy เท่านั้น **ยังไม่ยืนยันการเชื่อม Hospital** ดูสถานะเว็บและทดสอบหน่วยงานที่อนุญาตผ่าน Gateway แยก ไม่เรียก deprecated ERP API module

ถ้า setup code/Host/Origin ถูกปฏิเสธ ให้ใช้ localhost/SSH หรือ proxy HTTPS ที่ประกาศไว้ ไม่ลด Secure cookie/การตรวจ certificate Key เดิมหายต้องกู้ backup ไม่สร้างใหม่ ถ้า upgrade guard ปฏิเสธ ให้ตรวจ `.env`/overlays เดิมโดยไม่ลบ volume หากพอร์ต/พื้นที่มีปัญหาให้ตรวจสิ่งที่ใช้อยู่ ไม่ reset Docker/prune volume/หยุดบริการอื่นโดยเดา ไม่แชร์ credentials หรือ log ที่ยังไม่ปกปิด
