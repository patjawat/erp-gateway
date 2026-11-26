# Hospital ERP Gateway

Gateway กลางรับคำขอจาก Mobile แล้วส่งต่อไปยัง API ของหน่วยงานที่ได้รับอนุญาต หน้า Admin ใช้ Angular + Tailwind CSS โทนคราม–ทองเหมือน Mobile รองรับโหมดสว่าง/มืดและฟอร์มโมดอล ผู้ดูแลส่วนกลางใช้บัญชีเดียว

```text
gateway/
  backend/            Go Gateway, Admin API และระบบตั้งค่าครั้งแรก
  frontend/           Angular + Tailwind CSS
  .env.example        เปิด/ปิด Setup และกำหนดพอร์ต
  .env.advanced.example ตัวเลือกพอร์ต/image/proxy สำหรับกรณีพิเศษ
  .env.lan.example    ตัวอย่าง LAN แบบเดียวกับชุดปกติ
  Dockerfile          build Angular แล้วฝัง static assets ใน Go image เดียว
  docker-compose.yml  เริ่มจากซอร์สใน workspace
  deploy/gateway/     เครื่องมือ dev และการติดตั้งเดิม
```

## เริ่มจากซอร์สด้วย Docker

ติดตั้ง Docker Engine/Desktop และ Compose v2 แล้วรันจากโฟลเดอร์ `gateway/`:

```bash
docker compose up -d
```

ค่าเริ่มต้นใช้พอร์ต `8080` บนทุก IPv4 interface ของเซิร์ฟเวอร์ ไม่ต้องกำหนด IP, URL, CIDR หรือ localhost proxy ก่อนเข้าเว็บ หากต้องการไฟล์ `.env` ให้คัดลอกด้วย `cp -n .env.example .env` แล้วใช้ `SETUP=true` และ `GATEWAY_PORT=8080` เปลี่ยนพอร์ตได้ตามต้องการ ส่วน `GATEWAY_ADMIN_PASSWORD` สำหรับโหมด legacy ให้เว้นว่างเมื่อตั้งค่าผ่านเว็บ คำสั่งคัดลอกจะเก็บไฟล์เดิมไว้เมื่อมีอยู่แล้ว

ครั้งแรกจะดาวน์โหลด dependencies และ build Angular/Go เปิด [หน้าตั้งค่าครั้งแรกในเครื่อง](http://localhost:8080/setup/) หรือ `http://IP-เซิร์ฟเวอร์:8080/setup/` จาก LAN/VPN แล้วอ่านรหัสอายุสั้นบนเซิร์ฟเวอร์:

```bash
docker compose exec gateway /usr/local/bin/gateway setup-code
```

กรอกรหัส Setup ในเว็บก่อนตั้งค่า ระบบจะเสนอ URL ของหน้าที่คุณเปิดให้ตรวจยืนยัน จากนั้นตั้งบัญชีผู้ดูแล ปลายทาง API และใบรับรอง เมื่อเปิดใช้งานแล้วเข้า `/admin/` ผ่าน URL ที่บันทึกไว้ ดู [WEB_SETUP.md](deploy/gateway/WEB_SETUP.md) ต้องมีใบ client ที่ CA ปลายทางเชื่อถือ หรือส่ง CSR ให้ CA เดิมลงนามแล้วนำใบกลับเข้า Gateway การลงนามและต่ออายุอัตโนมัติยังไม่รองรับ

HTTP ในโหมดนี้รองรับ localhost และ IP ภายในแบบ direct เท่านั้น ข้อมูลระหว่างทางไม่เข้ารหัส ไม่เชื่อ forwarded headers เพื่ออนุญาต Setup จากภายนอก หากใช้ HTTPS reverse proxy ดูหัวข้อขั้นสูงใน [WEB_SETUP.md](deploy/gateway/WEB_SETUP.md)

คู่มือลงนามในหน้า Setup และหน้าใบรับรองเลือกอายุ **ใบ client ของ Gateway** ได้ 1–365 วัน แล้วคัดลอกคำสั่งไปเซ็นบน Mac อายุเริ่มเมื่อเซ็นจริง นำใบใหม่กลับเข้า Gateway; API ลูกค้าเก็บใบ CA สาธารณะและ pin เดิมต่อได้เมื่อใช้ CA/key เดิม ดู [ตำแหน่งไฟล์และการต่ออายุ](deploy/gateway/GATEWAY_CLIENT_CA.md)

Compose ใช้ project `erp-gateway` และ volumes แยก `gateway-workspace-data`/`gateway-workspace-config` จึงไม่ย้ายหรือใช้ข้อมูล production เดิม พอร์ตเริ่มต้นคือ `0.0.0.0:8080` ถ้าพอร์ตถูกใช้อยู่ กำหนดพอร์ตเฉพาะคำสั่งได้:

```bash
GATEWAY_PORT=18080 docker compose up -d
```

หากเคยรัน root Compose ด้วย project `erp-mobile-gateway-workspace` การเปลี่ยนชื่อเป็น `erp-gateway` จะใช้ volumes ชุดใหม่ ไม่ย้ายข้อมูลและการตั้งค่าเดิมให้อัตโนมัติ ระหว่างที่ยังไม่ได้ย้ายข้อมูล ให้คง project เดิมด้วย `COMPOSE_PROJECT_NAME=erp-mobile-gateway-workspace` เมื่อใช้คำสั่ง Compose และเก็บ volumes เดิมไว้

ชื่อ container ที่แสดงใน `docker ps` กำหนดเป็น `erp-gateway` ส่วนคำสั่ง Compose ยังใช้ชื่อ service `gateway` เช่น `docker compose logs gateway` ชื่อ container นี้ใช้ได้เพียงหนึ่งตัวต่อ Docker host หากใช้ project `erp-gateway` อยู่แล้วและต้องการให้ container เดิมเปลี่ยนชื่อหลังดึงโค้ดล่าสุด ให้รันคำสั่งต่อไปนี้ ซึ่งจะ recreate container และใช้ volumes เดิม:

```bash
docker compose up -d --no-build --pull never gateway
```

หยุดด้วย `docker compose stop` ข้อมูลคงอยู่ใน volumes เก็บ volumes และ encryption key เดิมไว้เมื่อต้องอัปเกรด

## เปิด/ปิด Setup และอัปเกรดระบบเดิม

```env
SETUP=true
```

- `true` อนุญาตการตั้งค่าครั้งแรกหรือ recovery โดยต้องมีรหัสใช้ครั้งเดียวจากเซิร์ฟเวอร์
- `false` ปิดการตั้งค่าและการขอรหัสสำหรับระบบที่ยังไม่เปิดใช้ แต่ไม่ปิด Admin/Mobile ของระบบที่ตั้งค่าแล้ว
- เมื่อเปิดใช้งานสำเร็จ Setup จะปิดอัตโนมัติ แม้ค่ายังเป็น `true` การเปลี่ยนกลับ `true` ไม่เริ่มติดตั้งทับของเดิมและไม่รีเซ็ตรหัสผ่าน

นโยบาย HTTP/HTTPS จะบันทึกพร้อม URL ใน config ถาวร การเปลี่ยนเป็น `SETUP=false` หรือ restart จึงไม่ทำให้ Admin แบบ HTTP ที่ตั้งค่าแล้วเข้าไม่ได้ Cookie ยังใช้ HttpOnly/SameSite/CSRF และเลือก Secure ตาม URL; การเชื่อมต่อ Gateway ไป API โรงพยาบาลยังใช้ HTTPS/mTLS

สำหรับเครื่องที่ใช้รุ่นเก่า ให้อัปเกรดโดยรักษา `.env`, Compose project, volumes, encryption key และใบรับรองเดิมก่อน:

```bash
git pull --ff-only && docker compose up -d --build gateway
```

ค่าขั้นสูงเดิมยังรองรับ ระบบที่เปิดใช้งานสมบูรณ์และยังไม่มีนโยบายใน config จะบันทึก snapshot ของนโยบายที่ตรวจแล้ว โดยรักษา CIDR เดิมหากกำหนดไว้ หลังบันทึกสำเร็จ ระบบใช้ค่าถาวรเป็นหลัก ไม่ย้าย URL ของระบบ HTTPS เดิมเป็น HTTP จากการแก้ `.env`

การเปลี่ยน URL ทำผ่านหน้า Settings หลังเตรียมเส้นทางใหม่ให้เข้าถึงได้ ค่า port/bind/image และ proxy ตอนเข้าครั้งแรกผ่าน HTTPS เป็นตัวเลือกเพิ่มเติมใน [.env.advanced.example](.env.advanced.example) การกู้คืนไฟล์สำรอง HTTP ต้องใช้นโยบายปลายทางที่อนุญาตรูปแบบนั้น ไม่ใช้ archive เพื่อข้ามข้อกำหนด

## รหัสผ่านผู้ดูแลและกรณีลืม

เมื่อลืมรหัสผ่านของบัญชีที่ตั้งค่าผ่านเว็บ ให้เปิด Terminal บนเครื่องที่รัน Gateway หรือ SSH เข้าเครื่องนั้น แล้วรันจากโฟลเดอร์ Compose ชุดที่ใช้งานจริง:

```bash
docker compose exec gateway /usr/local/bin/gateway setup-password
```

ใส่รหัสผ่านใหม่และยืนยันอีกครั้งใน Terminal โดยไม่แสดงสิ่งที่พิมพ์ รหัสผ่าน Admin ไม่กำหนดจำนวนตัวอักษรขั้นต่ำ แต่ต้องไม่เป็นค่าว่าง ไม่ต้องใช้รหัสผ่านเดิม คำสั่งจะแสดงชื่อผู้ใช้เดิมให้ใช้เข้าสู่ระบบด้วยรหัสใหม่ ข้อมูลหน่วยงาน ฐานข้อมูล encryption key การตั้งค่าและใบรับรองเดิมยังอยู่ครบ ไม่ต้องเริ่ม Setup หรือ restart เพื่อเปลี่ยนรหัส Session ผู้ดูแลเดิมสิ้นสุด ส่วน session ของ Mobile ยังคงใช้งานต่อได้ คำสั่งทำงานได้แม้ `SETUP=false`

การเปลี่ยน `GATEWAY_ADMIN_PASSWORD` ใน `.env`, เปิด `SETUP=true` หรือขอ `setup-code` ไม่รีเซ็ตบัญชีที่เปิดใช้งานแล้ว หาก image ที่ใช้อยู่ยังไม่รองรับ `setup-password` ให้อัปเดต Gateway โดยรักษา Compose project และ volumes เดิมตามหัวข้อก่อนหน้า

`GATEWAY_ADMIN_PASSWORD` ใช้กับระบบ legacy ที่ยังไม่มี config ถาวรผ่านเว็บเท่านั้น ระบบลักษณะนี้เปลี่ยนรหัสใน `.env` แล้ว recreate service ด้วย Compose ชุดเดิมได้ ดู [วิธีใช้และกรณีลืมรหัสผ่าน](deploy/gateway/WEB_SETUP.md#รหัสผ่านผู้ดูแลและกรณีลืมรหัสผ่าน) พร้อมข้อจำกัดการกู้คืน เก็บ volumes, encryption key และใบรับรองเดิมไว้

## สำรองข้อมูล กู้คืน และย้ายเครื่อง

เปิด **สำรองข้อมูลและย้ายเครื่อง** ที่ `/admin/backups` เพื่อสำรองครบทั้งฐานข้อมูล การตั้งค่า บัญชีผู้ดูแล และใบรับรอง/กุญแจของ Gateway กด **สำรองข้อมูลตอนนี้** แล้วดาวน์โหลดไฟล์พร้อมชุดกู้คืนแยกกันได้ โดยไม่ต้องรอ Google Drive

เครื่องใหม่รัน Compose แล้วเลือก **นำเข้าข้อมูล Gateway เดิม** ใน Setup หรือเปิดรับการโอนตรงจากต้นทางด้วย HTTPS URL และรหัสจับคู่ ระบบเตรียมข้อมูลก่อน แล้วพักต้นทางเฉพาะตอนเก็บชุดสุดท้ายและสลับใช้งาน บัญชีผู้ดูแลและ Mobile token ที่ยังไม่หมดอายุในชุดข้อมูลยังใช้ต่อได้เมื่อปลายทาง ERP และโดเมนเดิมพร้อม

ดู [คู่มือสำรอง กู้คืน และย้ายเครื่อง](deploy/gateway/BACKUP_MIGRATION.md) สำหรับขั้นตอนครบ คำสั่งฉุกเฉิน และข้อจำกัด และ [เชื่อม Google Drive ส่วนตัว](deploy/gateway/GOOGLE_DRIVE_BACKUP.md) สำหรับรอบอัตโนมัติที่ปรับได้ (เริ่มต้น 15 นาที)

## Dev บน Mac โดยไม่ใช้ Docker

ต้องมี Go 1.26.11, Node ที่ตรงกับ `frontend/package.json` (สาย 22 ตั้งแต่ 22.22.3 หรือสาย 24 ตั้งแต่ 24.15.0), npm, Python 3 และ OpenSSL:

```bash
./deploy/gateway/dev-gateway.sh
```

สคริปต์ติดตั้ง frontend dependencies ตาม lockfile เมื่อยังไม่มี `node_modules`, build Angular แล้วฝังใน Go และเปิดแบบ foreground ที่ `http://127.0.0.1:8080/admin/` ใช้ `dev-admin` กับรหัสผ่านสุ่มที่แสดงใน Terminal กด Ctrl+C เพื่อหยุด หากพอร์ตถูกใช้ให้เติม `--port 18080`

สถานะ dev อยู่ใน `secrets/local-gateway/` ใช้ SQLite/config และใบรับรองทดสอบอายุสั้นแยกจาก production ไม่มี Hospital upstream หรือ FCM สคริปต์ไม่อ่าน production `.env` และไม่เปลี่ยน key เดิมเมื่อเปิดซ้ำ Go toolchain/dependencies ต้องมีใน cache; frontend ครั้งแรกอาจต้องเชื่อมต่อ npm registry

สำหรับแก้ Angular แล้วเห็นผลทันที เปิด backend ด้วยคำสั่งข้างบนค้างไว้ แล้วเปิดอีก Terminal:

```bash
cd frontend
npm start
```

เข้า `http://127.0.0.1:4200/admin/` โดย proxy ส่ง `/admin/api` และ `/setup` ไป backend พอร์ต 8080 หากเปลี่ยนพอร์ต backend ให้แก้ `frontend/proxy.conf.json` ให้ตรงกัน โหมดนี้ใช้ระหว่าง dev; image production ไม่มี Node runtime

## ใช้ image ที่ build แล้ว

Dockerfile สร้าง image รวม frontend/backend โดยไม่สร้าง TAR:

```bash
docker build --target gateway -t YOUR_REGISTRY/gateway:VERSION .
```

สำหรับ **การติดตั้งใหม่** บนเซิร์ฟเวอร์ที่รับ image จาก registry ที่คุณจัดการ:

```bash
GATEWAY_IMAGE=YOUR_REGISTRY/gateway:VERSION GATEWAY_PULL_POLICY=always docker compose up -d --no-build --pull always
```

ใช้ tag รุ่นที่กำหนดชัดเจนหรือ image digest และตั้ง HTTPS reverse proxy ตามเดิม การ build ในเครื่องไม่ได้ publish image ให้ registry โดยอัตโนมัติ

**ระบบ production ที่ใช้งานอยู่แล้ว:** ใช้ `deploy/gateway/docker-compose.yml`, `.env`, project, volumes, encryption key และ certificate mounts เดิมต่อไปเมื่อเปลี่ยน image หน้า Angular รวมอยู่ใน image เดียว ไม่ต้องย้ายข้อมูลเพื่อเปลี่ยนหน้าเว็บ อย่าสลับไป root Compose แล้วเข้าใจว่า volumes ใหม่เป็นข้อมูลเดิม หากมี overlays ให้ใช้ครบชุดและลำดับเดิม ดู [setup/recovery](deploy/gateway/WEB_SETUP.md) และ [การตรวจอัปเกรดเดิม](deploy/gateway/OFFLINE_GATEWAY.md) ขั้นสร้าง/โหลด TAR ในคู่มือเก่าเป็นประวัติที่พักไว้แล้ว

## ขอบเขตรุ่นแรก

- Angular เชื่อม Admin API เดิม: ทะเบียนหน่วยงาน อนุมัติ/ระงับ ทดสอบการเชื่อมต่อ แก้วันเริ่ม/หมดอายุ เอกสารแนบ ใบรับรอง ประวัติ และการตั้งค่า
- วันหมดอายุใช้ติดตามลูกค้า **ไม่ระงับ Mobile อัตโนมัติ** ผู้ดูแลสั่งระงับ/เปิดใช้เอง
- Dashboard ใช้ผู้ใช้งานที่ยืนยันตัวตนไม่ซ้ำใน 30 วัน ÷ บุคลากรปัจจุบันในฐานเดียวกัน รวมหลายหน่วยงานด้วยผลรวมผู้ใช้ ÷ ผลรวมบุคลากร แยก Android/iOS ที่ตรวจพบออกจากจำนวนคนและยอดดาวน์โหลด Store
- ใช้ aggregates จริงพร้อมช่วงเวลา/ความสด ถ้าข้อมูลขาด เก่า หรือยังไม่รองรับ แสดงสถานะนั้นแทนศูนย์ รายการและประวัติใช้ server pagination
- คงสัญญา proxy/authentication เดิม การย้ายปลายทางจาก Go Hospital API ไป ERP API ใหม่เป็นงานถัดไป ไม่เรียกโมดูล ERP API เก่าที่เลิกใช้แล้วเป็น fallback
- พัก Jenkins สำหรับ build Go API, TAR ทั้ง Go API/Gateway และ workflow ส่งอัปเดต Go API ผ่าน Gateway/Updater หน้า Angular ไม่มีคำสั่งเหล่านี้ สคริปต์สร้าง TAR จะหยุดพร้อมแจ้งเหตุผล การเปลี่ยนแปลงนี้ไม่ได้สั่งหยุด Jenkins หรือแก้เครื่องลูกค้า
- การอัปเดต ERP image/Compose/migrations และ Provider ID/ThaID ยังคงอยู่นอกขอบเขต

Gateway client private key อยู่ที่ Gateway เท่านั้น โรงพยาบาลรับ public CA/pin และ key เฉพาะของตนตามการจัดการใบรับรองเดิม ไม่ส่ง key ของ Gateway ให้หน่วยงาน

## ตรวจสอบก่อนส่งมอบ

```bash
cd frontend
npm ci
npm test
cd ..
./deploy/gateway/build-admin-ui.sh
cd backend
go test ./...
go vet ./...
go build -o ./bin/gateway ./cmd/gateway
```

`build-admin-ui.sh` สร้าง `frontend/dist/browser/` แล้ววาง static assets ใน `backend/internal/admin/assets/ui/` สำหรับ Go embed โดยตรวจ base URL `/admin/` และ CSP nonce ไฟล์เหล่านี้เป็น build output ไม่แก้ด้วยมือ Dockerfile ทำขั้นตอนเดียวกันใน image build

Wire contracts และ TLS helpers อยู่ใน `backend/internal/contracts`/`backend/internal/transportauth` การเปลี่ยนสัญญาระหว่างบริการต้องตรวจ integration ทั้งสองฝั่งแยกจากการเปลี่ยนหน้า Admin
