# ตั้งค่า Gateway ผ่านเว็บ

ใช้กับ image รุ่นใหม่ที่มี first-run setup เท่านั้น ไม่ใช้ image เก่า `0.1.0` แทน binary ที่ยังไม่ได้ build/publish รุ่นนี้

## เริ่มระบบใหม่

1. ถ้า clone จากซอร์ส ให้ใช้ root Compose ตาม [README](../../README.md) เพื่อ build image รวม frontend/backend หากรับ image จาก registry ให้ใช้รุ่นที่ build/test แล้วใน `GATEWAY_IMAGE`
2. ในโฟลเดอร์ที่มี `docker-compose.yml` รัน `docker compose up -d`
3. ใช้ `SETUP=true` ตามค่าเริ่มต้นของ root Compose เปิด `http://localhost:8080/setup/` หรือ `http://IP-เซิร์ฟเวอร์:8080/setup/` จาก LAN/VPN ไม่ต้องกรอก URL/CIDR/proxy ใน `.env` ก่อน
4. รัน `docker compose exec gateway /usr/local/bin/gateway setup-code` เพื่ออ่านรหัสอายุ 10 นาที ใช้ครั้งเดียว ไม่ใส่รหัสใน URL หรือ logs
5. ยืนยันรหัสในเว็บ ระบบจะเสนอ URL ที่ตรวจจากการเข้าครั้งนี้ให้ยืนยัน ตั้งบัญชีผู้ดูแล ปลายทาง Hospital API และ private destination network ที่จำเป็น
6. สร้าง Gateway key/CSR ผ่านเว็บ ดาวน์โหลดเฉพาะ CSR แล้วนำ chain ที่ลงนามด้วย `clientAuth` กลับมานำเข้า ต้องรวม issuer/root ที่ตรวจ chain ได้ Private key ไม่ออกจาก Gateway
7. หรือเลือกนำเข้า identity เดิม โดยกรอก client certificate chain และ private key เดิมเฉพาะครั้งย้ายระบบ ไม่สร้าง CA root ใน Gateway
8. ตรวจผลก่อนเปิดใช้งาน ใบผ่านการตรวจไม่ได้แปลว่าตรวจ end-to-end กับทุก Hospital แล้ว หลังเปิดใช้ให้ทดสอบหน่วยงานนำร่องก่อนอนุมัติ

บัญชีผู้ดูแลเก็บเป็น PBKDF2-HMAC-SHA256 verifier ที่สุ่ม salt และใช้ 600,000 iterations ไม่เก็บรหัสผ่านจริงใน config โหมดปกติรองรับ HTTP บน localhost/IP ภายใน หรือ HTTPS ที่เตรียมไว้ Cookie ใช้ Secure ตาม URL ที่บันทึก และยังคง HttpOnly/SameSite/CSRF

## ความหมายของ SETUP และนโยบายที่บันทึก

ไฟล์ [.env.example](../../.env.example) มี `SETUP=true` และ `GATEWAY_PORT=8080` สำหรับเลือกพอร์ตบนเซิร์ฟเวอร์ `SETUP=true` เปิดการติดตั้งครั้งแรก/recovery สำหรับผู้มีรหัสจากเซิร์ฟเวอร์ `SETUP=false` ปิดเฉพาะส่วนนี้และการออกรหัสในระบบที่ยังไม่พร้อม เมื่อระบบ active แล้ว Setup ปิดเสมอไม่ว่าค่าจะเป็นอะไร และ Admin/Mobile ยังทำงานตามคอนฟิกเดิม

ก่อนยืนยันรหัส URL จากคำขอเป็นเพียงค่าที่เสนอ หลังยืนยันแล้ว session จะผูกกับ origin ที่ตรวจสอบแล้ว คำขอเขียนต้องมาจาก origin เดิมพร้อม CSRF ระบบยอมรับ HTTP แบบ direct จาก peer ภายใน/loopback และ Host แบบ IP ภายในหรือ localhost เท่านั้น ไม่อนุมาน HTTPS/ตัวตนจาก forwarded headers ที่ยังไม่เชื่อถือ

เมื่อเปิดใช้งาน ระบบบันทึก `web_access` พร้อม URL ใน `runtime.json` จึงใช้ HTTP เดิมได้หลัง restart หรือเปลี่ยน `SETUP=false` โดยไม่ต้องคงตัวแปร LAN เดิม การอัปเกรด config เก่าที่สมบูรณ์จะบันทึก snapshot ของนโยบายที่ตรวจ/build ผ่านภายใต้ lock โดยเก็บ CIDR เดิมที่กำหนดไว้อย่างชัดเจน การเปลี่ยนเป็น true ไม่ลบ config/key/identity หรือรีเซ็ตบัญชี

HTTP ไม่เข้ารหัสระหว่างเครื่องกับ Gateway แต่การเชื่อมต่อ Gateway ไป API ยังคงใช้ HTTPS/mTLS ทุกกรณี ตัวเลือกขั้นสูงอยู่ใน [.env.advanced.example](../../.env.advanced.example) และการกู้คืน archive ยังต้องตรวจนโยบายของเครื่องปลายทาง

## Volumes และความปลอดภัย

- Root Compose ใช้ `gateway-workspace-data:/data` และ `gateway-workspace-config:/config`; ชุด legacy ใน deploy ใช้ชื่อ `gateway-data`/`gateway-config` เดิม รักษาชื่อและ project ของชุดที่ติดตั้งจริง
- Config volume เก็บ `runtime.json`, `encryption.key`, protected key/chain และไฟล์ setup code ที่เก็บเฉพาะ hash ไฟล์ secret สิทธิ์ `0600` และโฟลเดอร์ identity `0700`
- Backup `/data` แบบ consistent รวม DB/WAL ตาม SQLite และ backup `/config` อย่างปลอดภัยแยกจาก offline CA backup ห้ามแชร์ config volume/release bundle ที่มี secrets
- Restart และ update ใช้ encryption key เดิม ไม่สร้างใหม่เมื่อพบ DB/WAL/การตั้งค่าบางส่วน หาก key หาย/ผิดต้องกู้คืน key เดิมจาก backup ระบบจะไม่แก้ฐานข้อมูลเพื่อให้ผ่าน
- Runtime ที่ active จะปิด setup mutation routes และยกเลิก setup session ใช้ Admin ปกติสำหรับ CSR/import/ต่ออายุ
- Gateway ไม่มี Docker socket และไม่มี endpoint รัน shell/แก้ host firewall

## เข้าตั้งค่าผ่าน HTTPS proxy เดิม

ก่อนตั้งค่าครั้งแรกผ่านโดเมน public ระบุ `GATEWAY_SETUP_PUBLIC_URL=https://gateway.example.org` และ `GATEWAY_TRUSTED_PROXY_CIDRS` เป็น CIDR ของ proxy ที่ส่ง `X-Forwarded-Proto: https` จริง Proxy ต้องรักษา Host เดิม ห้ามใส่เครือข่ายทั้งหมดหรือเชื่อ header จาก public client โดยตรง

Root Compose bind ที่ `0.0.0.0:8080` เพื่อเริ่มตั้งค่าผ่าน LAN/VPN ได้ หากใช้ proxy บน host เดียวกันและต้องการ bind เฉพาะ loopback ให้ระบุ `GATEWAY_BIND_ADDRESS=127.0.0.1` เป็น override พอร์ตที่ Docker เปิดกับสิทธิ์เข้า Setup เป็นคนละชั้น ระบบยังตรวจ Host/Origin/peer และรหัส Setup

`GATEWAY_SETUP_LOCAL_PROXY_CIDRS` เป็นตัวเลือกเข้ากันได้กับ bootstrap แบบเดิมสำหรับ Docker bridge ที่ส่งต่อ host loopback ไม่ต้องตั้งในการติดตั้งปกติ ค่านี้ไม่ใช่ trusted HTTPS proxy และไม่แทนรหัส Setup เมื่อรัน binary โดยไม่มีตัวแปร SETUP โค้ดจะรักษาพฤติกรรม bootstrap แบบเดิม

## ย้ายระบบเดิม

ห้ามลบหรือเปลี่ยน Compose project/volume เพื่อให้ setup ผ่าน สำรอง DB และ encryption key ก่อน แล้วรักษา `erp-mobile-gateway_gateway-data` เดิม

- ถ้าจะรัน legacy env เดิมชั่วคราว ใช้ `docker compose -f docker-compose.yml -f docker-compose.legacy-tls.yml up -d` พร้อม env encryption key/admin password/cert host paths เดิม
- หากมี `runtime.json` แล้ว persisted config มีลำดับก่อน legacy env และไฟล์ต้องครบ หากอ่านไม่ได้เข้าสู่ recovery ไม่ fallback ไปสร้าง config/key ใหม่
- ระบบเดิมที่ยังไม่มี persisted config สามารถเริ่ม recovery โดยไม่ส่ง legacy env แล้วนำ encryption key เดิม + identity เดิมกลับเข้าทาง local setup ข้อมูล DB ต้องอยู่ volume เดิมเท่านั้น ไม่ upload ERP `.env` หรือ arbitrary host paths
- การตรวจ key เดิมเปิด SQLite แบบ read-only และตรวจ AES-GCM sentinel ก่อนเปิด operational store หรือ migration หากไม่มี sentinel ต้อง restore backup/ตรวจข้อมูล ไม่ initialize ทับ

## ใบรับรองและ issuer

ใบ Gateway เป็น client identity สำหรับ mTLS ไม่ใช่ HTTPS ของโดเมน และไม่ใช่ Hospital server identity การสร้าง CSR ไม่ออกใบ self-signed ให้ใช้งาน production และไม่ย้าย offline root CA key มาไว้ในเว็บ

CA เดิมแบบ `pathlen:0` ยังต้องเซ็น leaf จาก authority เดิม บริการ delegated issuer/automatic renewal ยังต้องตั้ง authority ที่ถูกต้องก่อน จึงยังไม่อ้างว่าเปิด container แล้วออกใบที่ลูกค้าเดิมเชื่อถือได้เอง

การนำเข้า chain ใหม่ stage ไฟล์ใน generation ใหม่ ตรวจ key/chain/clientAuth/เวลา สร้าง runtime candidate แล้วสลับ config/handler เมื่อสำเร็จเท่านั้น คำขอเดิมจะ drain ก่อนปิด runtime เก่า ถ้า validate/build/persist ล้มเหลวใบและ config ปัจจุบันยังใช้งานต่อได้

## เปลี่ยนค่าผ่านเว็บหลังติดตั้ง

หน้า Settings แก้ได้เฉพาะ Gateway URL ที่นโยบายอนุญาต, trusted proxy CIDRs, รายการปลายทาง Hospital API และ private destination CIDRs โดยใช้ authenticated Admin + CSRF การบันทึก validate และสร้าง runtime candidate ก่อนสลับอย่าง atomic ไม่เปลี่ยน admin verifier, encryption key, private key หรือ certificate identity หลังบันทึกต้องเข้าสู่ระบบใหม่เพราะ session ผู้ดูแลของ runtime เดิมสิ้นสุด

Trusted proxy ต้องเป็น CIDR แบบ canonical ที่แคบพอ: IPv4 `/16` ขึ้นไป หรือ IPv6 `/48` ขึ้นไป (ยกเว้น loopback ที่ระบุชัดเจน) แนะนำ `/32` หรือ `/128` ของ proxy จริง ไม่ใช้ `0.0.0.0/0` หรือเครือข่าย public กว้าง การตั้งค่านี้แยกจาก Docker ingress allowance ของหน้า setup

หาก config/key ถูกแก้จากภายนอกระหว่าง runtime ทำงาน เว็บจะไม่เขียนทับเงียบ ๆ ต้องตรวจและ restart อย่างปลอดภัยก่อน ระบบ legacy ที่ยังใช้ env อย่างเดียวต้องนำเข้าระบบถาวรก่อนจึงจะแก้และ export ผ่านเว็บได้

## ไฟล์กู้คืนที่เข้ารหัส

ผู้ดูแล export จากเว็บโดยตั้ง passphrase แยกยาวอย่างน้อย 16 bytes ไม่ใช้ password ผู้ดูแลเป็นค่าปริยาย ไฟล์ schema `erp-gateway-recovery.v1` ใช้ AES-256-GCM พร้อม PBKDF2-HMAC-SHA256 600,000 iterations, salt/nonce สุ่มทุกครั้ง และ authentication tag ครอบคลุมข้อมูล ไฟล์ประกอบด้วย config, admin verifier, encryption key และ Gateway private identity/certificate ที่ใช้อยู่ภายใน ciphertext ไม่เก็บ passphrase และไม่รวม offline root CA key หรือฐานข้อมูล

ต้องสำรอง DB/WAL แบบ consistent แยกต่างหากและเก็บ passphrase แยกจาก archive การมีไฟล์กู้คืน config ไม่แทน database backup

กู้คืนด้วย binary ของ Gateway บนเครื่องผู้ดูแลหรือ container ที่ mount archive แบบ read-only:

```sh
gateway restore-config /backup/gateway-recovery.json /config/restored < /private/recovery-passphrase.txt
```

passphrase อ่านจาก standard input บรรทัดแรก ไม่ใส่ใน arguments หรือ environment และไฟล์ passphrase ถ้าใช้ต้องป้องกันด้วยสิทธิ์ `0600` เช่นเดียวกับ archive ปลายทางต้องเป็น directory ใหม่ที่ยังไม่มีอยู่ใต้ parent ที่ผู้ดูแลเตรียมไว้ ระบบตรวจ passphrase/format/settings/key match/chain ก่อนเขียน ไม่เขียนทับ directory เดิม สร้าง directory `0700` และ fixed filenames `0600` โดยไม่ใช้ paths จาก archive

หลัง restore ให้กำหนด `GATEWAY_CONFIG_DIR` ไปยัง directory ที่กู้คืน เช่นใส่ `GATEWAY_CONFIG_DIR=/config/restored` ใน `.env` ของ Compose และใช้ mount `/config` เดิมตามขั้นตอนกู้คืน ต้องใช้ path ภายใน container เดิมที่กำหนดตอน restore เพราะไฟล์ตั้งค่าจะอ้าง identity ภายใต้ directory นี้ นำ consistent DB backup ที่คู่กันกลับมายัง data volume ก่อนเริ่ม Runtime จะตรวจ encryption sentinel แบบ read-only ก่อนเปิด DB ถ้า key/DB ไม่ตรงหรือใบรับรองหมดอายุให้กู้คืนชุดที่ถูกต้อง/ต่ออายุจาก authority เดิม ไม่สร้าง key หรือ trust ใหม่เพื่อให้ผ่าน
