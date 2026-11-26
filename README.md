# Hospital ERP Gateway

Gateway กลางรับคำขอจาก Mobile แล้วส่งต่อไปยัง API ของหน่วยงานที่ได้รับอนุญาต หน้า Admin ใช้ Angular + Tailwind CSS โทนคราม–ทองเหมือน Mobile รองรับโหมดสว่าง/มืดและฟอร์มโมดอล ผู้ดูแลส่วนกลางใช้บัญชีเดียว

```text
gateway/
  backend/            Go Gateway, Admin API และระบบตั้งค่าครั้งแรก
  frontend/           Angular + Tailwind CSS
  .env.example        ตัวอย่างคอนฟิกสำหรับ Compose ที่ root
  .env.lan.example    ตัวอย่าง HTTP สำหรับ LAN ที่เปิดโหมดนี้โดยชัดเจน
  Dockerfile          build Angular แล้วฝัง static assets ใน Go image เดียว
  docker-compose.yml  เริ่มจากซอร์สใน workspace
  deploy/gateway/     เครื่องมือ dev และการติดตั้งเดิม
```

## เริ่มจากซอร์สด้วย Docker

ติดตั้ง Docker Engine/Desktop และ Compose v2 แล้วรันจากโฟลเดอร์ `gateway/`:

```bash
cp -n .env.example .env
docker compose up -d
```

ไฟล์ตัวอย่างใช้พอร์ต `18080` และรับเฉพาะ localhost หากมี `.env` อยู่แล้ว คำสั่งคัดลอกจะเก็บไฟล์เดิมไว้ ให้แก้ `GATEWAY_PORT` ในไฟล์เดิมตามต้องการ หากใช้ HTTPS reverse proxy ให้ชี้ปลายทางไป `http://127.0.0.1:18080` และใส่ `GATEWAY_SETUP_PUBLIC_URL` เป็น URL จริงพร้อม `GATEWAY_TRUSTED_PROXY_CIDRS` เฉพาะ proxy ที่คุณดูแลก่อนเปิด Setup ผ่าน proxy ดูรายละเอียดใน [WEB_SETUP.md](deploy/gateway/WEB_SETUP.md)

ครั้งแรกจะดาวน์โหลด dependencies และ build Angular/Go เปิด [หน้าตั้งค่าครั้งแรก](http://localhost:18080/setup/) บนเครื่องที่รัน Gateway หรือผ่าน SSH tunnel; หากรันบนเซิร์ฟเวอร์ให้เข้าผ่านโดเมน HTTPS ที่ตั้งไว้ แล้วอ่านรหัสตั้งค่าที่เครื่อง:

```bash
docker compose exec gateway /usr/local/bin/gateway setup-code
```

ตั้งบัญชีผู้ดูแล ที่อยู่ HTTPS เดิม ปลายทางที่อนุญาต และใบรับรอง เมื่อเปิดใช้งานแล้วเข้า `/admin/` ผ่านที่อยู่ที่ตั้งไว้ ดู [WEB_SETUP.md](deploy/gateway/WEB_SETUP.md) หากยังไม่มี SSL และใช้ภายใน LAN ให้ใช้โหมด HTTP ด้านล่าง ต้องมีใบ client ที่ CA ปลายทางเชื่อถือ หรือส่ง CSR ให้ CA เดิมลงนามแล้วนำใบกลับเข้า Gateway การลงนามและต่ออายุอัตโนมัติยังไม่รองรับ

คู่มือลงนามในหน้า Setup และหน้าใบรับรองเลือกอายุ **ใบ client ของ Gateway** ได้ 1–365 วัน แล้วคัดลอกคำสั่งไปเซ็นบน Mac อายุเริ่มเมื่อเซ็นจริง นำใบใหม่กลับเข้า Gateway; API ลูกค้าเก็บใบ CA สาธารณะและ pin เดิมต่อได้เมื่อใช้ CA/key เดิม ดู [ตำแหน่งไฟล์และการต่ออายุ](deploy/gateway/GATEWAY_CLIENT_CA.md)

Compose ใช้ project `erp-gateway` และ volumes แยก `gateway-workspace-data`/`gateway-workspace-config` จึงไม่ย้ายหรือใช้ข้อมูล production เดิม หากไม่ใช้ `.env` หรือไม่กำหนดพอร์ต ค่าเริ่มต้นของ Compose ยังเป็น `127.0.0.1:8080` สามารถกำหนดพอร์ตเฉพาะคำสั่งได้:

```bash
GATEWAY_PORT=18080 docker compose up -d
```

หากเคยรัน root Compose ด้วย project `erp-mobile-gateway-workspace` การเปลี่ยนชื่อเป็น `erp-gateway` จะใช้ volumes ชุดใหม่ ไม่ย้ายข้อมูลและการตั้งค่าเดิมให้อัตโนมัติ ระหว่างที่ยังไม่ได้ย้ายข้อมูล ให้คง project เดิมด้วย `COMPOSE_PROJECT_NAME=erp-mobile-gateway-workspace` เมื่อใช้คำสั่ง Compose และเก็บ volumes เดิมไว้

ชื่อ container ที่แสดงใน `docker ps` กำหนดเป็น `erp-gateway` ส่วนคำสั่ง Compose ยังใช้ชื่อ service `gateway` เช่น `docker compose logs gateway` ชื่อ container นี้ใช้ได้เพียงหนึ่งตัวต่อ Docker host หากใช้ project `erp-gateway` อยู่แล้วและต้องการให้ container เดิมเปลี่ยนชื่อหลังดึงโค้ดล่าสุด ให้รันคำสั่งต่อไปนี้ ซึ่งจะ recreate container และใช้ volumes เดิม:

```bash
docker compose up -d --no-build --pull never gateway
```

หยุดด้วย `docker compose stop` ข้อมูลคงอยู่ใน volumes เก็บ volumes และ encryption key เดิมไว้เมื่อต้องอัปเกรด

## HTTP ภายใน LAN โดยไม่มี SSL

เปิดได้ทั้ง Setup และ Admin เมื่อระบุ opt-in และเครือข่ายเครื่องผู้ดูแล โดยข้อมูลระหว่างทางเป็น HTTP ที่ไม่เข้ารหัส ค่าเริ่มต้นของระบบยังใช้ HTTPS และการเชื่อมต่อไป API โรงพยาบาลยังใช้ HTTPS/mTLS

ขั้นตอนนี้สำหรับการตั้งค่าครั้งแรก หรือระบบที่บันทึก URL แบบ HTTP ไว้แล้ว หากเปิดใช้ Gateway ด้วย URL แบบ HTTPS ไปแล้ว การแก้ `.env` ไม่เปลี่ยน URL ถาวร: ให้เปิดโหมด LAN และ build รุ่นใหม่ จากนั้นเข้าหน้า Settings ผ่าน HTTPS เดิมเพื่อเปลี่ยนที่อยู่ Gateway เป็น HTTP ก่อนย้ายมาเข้า LAN

สำหรับเซิร์ฟเวอร์ `10.10.8.127` พอร์ต `8080` ให้แก้เฉพาะค่าต่อไปนี้ใน `.env` เดิม (มีตัวอย่างใน [.env.lan.example](.env.lan.example)):

```env
GATEWAY_PORT=8080
GATEWAY_BIND_ADDRESS=10.10.8.127
GATEWAY_ALLOW_LAN_HTTP=true
GATEWAY_LAN_HTTP_CIDRS=10.10.8.0/24
GATEWAY_SETUP_PUBLIC_URL=http://10.10.8.127:8080
GATEWAY_TRUSTED_PROXY_CIDRS=
```

CIDR ต้องตรงกับ IP ต้นทางของเครื่องผู้ดูแลที่ Gateway เห็นจริง ตัวอย่างนี้ยอมรับเครื่องใน `10.10.8.0/24` เท่านั้น ใช้ IP/CIDR ภายในที่จำเป็น คั่นหลาย CIDR ด้วยจุลภาค ไม่เชื่อ `X-Forwarded-For` เพื่อข้าม allowlist และไม่ใช้ `GATEWAY_SETUP_LOCAL_PROXY_CIDRS` แทนค่านี้

หลังดึงโค้ดรุ่นที่รองรับ LAN ให้ build image ใหม่ เพราะแก้ `.env` อย่างเดียวไม่เพิ่มความสามารถให้ image เดิม:

```bash
git pull --ff-only
docker compose up -d --build gateway
docker compose exec gateway /usr/local/bin/gateway setup-code
```

เปิด [Setup ผ่าน LAN](http://10.10.8.127:8080/setup/) แล้วกรอกที่อยู่ Gateway เป็น `http://10.10.8.127:8080` เมื่อเปิดใช้งานแล้วเข้า [Admin](http://10.10.8.127:8080/admin/) โหมดนี้ตั้ง cookie สำหรับ HTTP ให้อัตโนมัติ โดยยังใช้ HttpOnly, SameSite และ CSRF ไม่ต้องเปลี่ยน `GATEWAY_COOKIE_SECURE` เพื่อข้ามข้อกำหนด

ต้องคง opt-in และ allowlist นี้เมื่อ restart หรือกู้คืน backup ของ HTTP config หากต้องการย้ายกลับ HTTPS ให้ตั้ง HTTPS/proxy ให้พร้อมแล้วแก้ URL ผ่านหน้า Settings ก่อนปิดโหมด LAN รักษา Compose project, volumes และ key เดิม

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
