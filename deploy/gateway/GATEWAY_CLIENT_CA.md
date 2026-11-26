# ออกใบรับรอง mTLS ให้ Gateway: คีย์อยู่ Linux, CA อยู่ Mac

ใช้สำหรับ **Gateway กลาง → Hospital API** เท่านั้น ไม่ใช่ใบรับรอง HTTPS ที่มือถือใช้ และไม่ใช่ CA สำหรับ MySQL หรือ HTTPS ฝั่ง Hospital. เรียกสคริปต์บน Mac ที่เชื่อถือได้ โดยใช้ Python 3 และ OpenSSL 3 ขึ้นไป (ไม่ใช้ LibreSSL ที่มากับ macOS)

## 1. Linux Gateway: ใช้ CSR ที่สร้างไว้แล้ว

เปิด **วิธีลงนาม CA และนำเข้าใบรับรอง** ข้าง CSR ในหน้า Setup ขั้นใบรับรอง หรือหน้า Admin → ใบรับรอง เพื่ออ่านขั้นตอนและคัดลอกคำสั่งบน Mac ระหว่างกรอกฟอร์มได้ คู่มือแยกการสร้าง CA ครั้งแรกออกจากการลงนามด้วย CA เดิม และไม่สร้าง CSR หรือส่งฟอร์มเมื่อเปิดอ่าน

หากตั้งค่าผ่านเว็บ ให้ไปขั้น **ใบรับรอง** กด **สร้าง CSR ที่ Gateway** แล้วกด **ดาวน์โหลด CSR** ใต้ช่องคำขอ จะได้ `gateway-client.csr.pem` บนเครื่องที่เปิดเบราว์เซอร์ หรือใช้ **คัดลอก CSR** เพื่อบันทึกข้อความเดิมเป็นไฟล์ PEM ปุ่มสองปุ่มนี้ใช้เฉพาะคำขอที่แสดงอยู่ ไม่สร้าง private key ใหม่ หากยังตั้งค่าไม่เสร็จ อย่า reload หน้าเพราะข้อมูลในฟอร์มที่ยังไม่บันทึกจะหาย

ใช้ไฟล์ที่ดาวน์โหลดแทน `gateway-client.csr` ในคำสั่งเซ็นด้านล่างได้ ไม่ต้อง SSH ไปสร้าง CSR ซ้ำ หลังเซ็น สคริปต์ให้ **ใบ client ใบเดียว** สำหรับนำเข้าผ่านเว็บต้องรวมใบ CA สาธารณะไว้ท้ายไฟล์ด้วย เช่น:

```sh
cat "$HOME/Downloads/gateway-client.crt.pem" \
  "$HOME/.hospital-erp-pki/central-gateway-client-ca/gateway-client-ca.crt.pem" \
  > "$HOME/Downloads/gateway-client-chain.pem"
```

เลือกไฟล์ `gateway-client-chain.pem` ในช่อง **ใบรับรอง client และ chain (PEM)** ของหน้า Setup หรือนำเข้าในหน้าใบรับรอง Admin ไม่ใส่ CA private key ลงในไฟล์นี้

หากมี CSR และ private key อยู่แล้ว ให้ข้ามการสร้างและใช้ CSR เดิม อย่าสร้างคีย์ใหม่แทนคีย์ที่ใช้งานอยู่ เส้นทางตัวอย่าง:

```sh
cd /home/cpherp/web-server/gateway-release/tls
ls -l gateway-client.csr
```

หากเป็น **การติดตั้งใหม่** และยังไม่มีทั้งสองไฟล์ ให้สร้างบน Linux ในไดเรกทอรีที่ผู้ดูแลจำกัดสิทธิ์ไว้ก่อน (คำสั่งย่อยเปิดโหมดไม่เขียนทับ):

```sh
(
  set -euC
  umask 077
  test ! -e gateway-client-key.pem && test ! -L gateway-client-key.pem
  test ! -e gateway-client.csr && test ! -L gateway-client.csr
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:prime256v1 > gateway-client-key.pem
  openssl req -new -key gateway-client-key.pem -subj '/CN=Hospital ERP Central Gateway' > gateway-client.csr
)
```

**ห้ามส่ง `gateway-client-key.pem` ออกจาก Linux** ใช้เฉพาะ `gateway-client.csr` ซึ่งมี public key และไม่ใช่ private key ตรวจสอบคำขอและแหล่งที่มากับผู้ดูแล Gateway ก่อนออกใบรับรอง เพราะลายเซ็น CSR บอกว่าใครบางคนถือ private key ที่คู่กัน แต่ไม่ได้ระบุเจ้าของเครื่อง

## 2. Mac: สร้าง CA ครั้งเดียว แล้วเซ็น CSR

จากโฟลเดอร์ `~/dev/erp-platform/gateway` บน Mac:

```sh
cd ~/dev/erp-platform/gateway
command -v openssl && openssl version
PKI="$HOME/.hospital-erp-pki"
mkdir -p -m 700 "$PKI"
CA="$PKI/central-gateway-client-ca"
bash deploy/gateway/issue-gateway-client-cert.sh init "$CA"
```

หาก `openssl version` ขึ้นเป็น LibreSSL ให้ใช้ OpenSSL ของ Homebrew เช่นเติม `OPENSSL="$(brew --prefix openssl@3)/bin/openssl"` หน้าคำสั่งสคริปต์ (เมื่อได้ติดตั้งสูตรนั้นไว้แล้ว). ตอนสร้างและใช้คีย์ สคริปต์จะถาม passphrase ใน Terminal โดยไม่แสดงสิ่งที่พิมพ์ และส่งตรงให้ OpenSSL โดยไม่ใส่ในไฟล์หรืออาร์กิวเมนต์ อย่าใส่รหัสในคำสั่งหรือไฟล์ Git เก็บคีย์ CA แบบเข้ารหัสบน Mac ที่ป้องกันไว้ และสำรองแบบเข้ารหัสในที่แยกกันอย่างปลอดภัย **อย่า init ใหม่เมื่อต่ออายุใบเดิม** เพราะ Hospital เชื่อถือ CA ตัวเดิม

บน Mac แทน `GATEWAY_HOST` ด้วยชื่อ Linux จริง ใช้บัญชี SSH ที่มีสิทธิ์ในโฟลเดอร์นี้ และตรวจสอบ host key ก่อนโอน:

```sh
GATEWAY_SSH='cpherp@GATEWAY_HOST'   # เปลี่ยน GATEWAY_HOST เป็นชื่อจริงก่อนใช้
GATEWAY_TLS='/home/cpherp/web-server/gateway-release/tls'
WORK="$(mktemp -d "$PKI/issue.XXXXXX")"
scp "$GATEWAY_SSH:$GATEWAY_TLS/gateway-client.csr" "$WORK/gateway-client.csr"

bash deploy/gateway/issue-gateway-client-cert.sh sign \
  "$CA" "$WORK/gateway-client.csr" "$WORK/gateway-client-chain.pem"
```

อายุเริ่มต้น 90 วัน (ระบุจำนวน 1–365 วันเป็นอาร์กิวเมนต์ท้ายสุดได้). สคริปต์ตรวจลายเซ็น CSR, ออกใบที่ใช้ได้เฉพาะ TLS client, ไม่คัดลอก subject/SAN/สิทธิ์จาก CSR, ตรวจสายใบรับรองและ public key หลังเซ็น และแสดงค่า `HOSPITAL_GATEWAY_CLIENT_SPKI_SHA256=` เป็น hex 64 ตัว

ในคู่มือบนหน้า Setup และหน้าใบรับรองของ Gateway เลือกอายุใบ client ได้ 1–365 วัน หรือใช้ค่า 90/180/365 วัน คำสั่งลงนามจะเปลี่ยนตามค่าที่เลือกเพื่อนำไปวางใน Terminal บน Mac อายุเริ่มนับเมื่อรันคำสั่งเซ็นจริง การเลือกค่าในเว็บไม่เปลี่ยนใบที่ออกแล้วและไม่เปลี่ยนอายุ CA สคริปต์ยังตรวจว่า CA เหลืออายุพอสำหรับใบใหม่

ไฟล์แต่ละส่วนอยู่คนละที่:

| ตำแหน่ง | ไฟล์/ข้อมูล |
|---|---|
| Mac ผู้ลงนาม | CA private key ที่เข้ารหัส และใบ CA สาธารณะ |
| Gateway กลาง | ใบ client ที่นำเข้าใหม่, chain และ Gateway private key เดิม |
| เครื่อง API ของลูกค้า | ใบ CA สาธารณะและ SPKI pin สำหรับตรวจ Gateway |

เมื่อต่ออายุด้วย CA เดิมและ Gateway key เดิม ให้เซ็น CSR แล้วนำใบ client/chain ใหม่เข้า Gateway โดยปกติไม่ต้องเปลี่ยนไฟล์ฝั่ง API ค่า SPKI คงเดิมเมื่อ key เดิมยังใช้อยู่ หากเปลี่ยน CA หรือ Gateway key ต้องปรับ trust/pin ฝั่ง API ตามแผนก่อนสลับใบ

## 3. ส่งเฉพาะไฟล์สาธารณะ และตรวจว่าคู่กับคีย์ Linux

จาก Mac ส่งใบ client ที่เซ็นแล้วเป็นชื่อสำหรับตรวจสอบก่อน เพื่อไม่ทับใบที่กำลังใช้ (เลือกชื่อปลายทางใหม่หากมีอยู่แล้ว):

```sh
scp "$WORK/gateway-client-chain.pem" \
  "$GATEWAY_SSH:$GATEWAY_TLS/gateway-client-chain.next.pem"
```

บน Linux ในโฟลเดอร์ `tls` เปิด Bash แล้วตรวจให้ fingerprint ของใบและคีย์ตรงกัน และตรงกับผลที่ Mac พิมพ์ หากไฟล์หาย อ่านไม่ได้ หรือรูปแบบผิด ให้หยุด ไม่ใช้ SHA-256 ของข้อมูลว่างเป็น pin:

```bash
(
  set -euo pipefail
  test -s gateway-client-chain.next.pem
  test -s gateway-client-key.pem
  CERT_PIN="$(openssl x509 -in gateway-client-chain.next.pem -noout -pubkey \
    | openssl pkey -pubin -outform DER | openssl dgst -sha256 -r | awk '{print $1}')"
  KEY_PIN="$(openssl pkey -in gateway-client-key.pem -pubout -outform DER \
    | openssl dgst -sha256 -r | awk '{print $1}')"
  [[ "$CERT_PIN" =~ ^[a-f0-9]{64}$ && "$CERT_PIN" == "$KEY_PIN" ]] \
    || { echo 'Public key ไม่ตรงกัน; หยุดก่อนตั้งค่า' >&2; exit 1; }
  printf 'Client SPKI SHA-256: %s\n' "$CERT_PIN"
)
```

- **Gateway กลาง**: ตั้ง `GATEWAY_CLIENT_TLS_CERT_HOST_PATH` เป็นพาธใบ client ที่ตรวจแล้ว และ `GATEWAY_CLIENT_TLS_KEY_HOST_PATH` เป็น key เดิมบน Linux. CA นี้เซ็นโดยตรงจึงใช้ไฟล์ใบ client ใบเดียวได้ ตั้งให้บริการอ่าน key ได้โดยไม่เปิดให้คนอื่น และไม่คัดลอก key ไปกับชุดติดตั้ง
- **แต่ละ Hospital API**: ส่งเฉพาะไฟล์ public **`gateway-client-ca.crt.pem`** จาก `$CA` ให้ผู้ดูแลผ่านช่องทางที่ตรวจสอบได้ ตรวจ SHA-256 certificate fingerprint ที่แสดงตอนสร้าง CA, ตั้งไฟล์นี้ใน `HOSPITAL_GATEWAY_CLIENT_CA_HOST_PATH` และตั้ง **SPKI pin ของใบ client** ที่สคริปต์พิมพ์ใน `HOSPITAL_GATEWAY_CLIENT_SPKI_SHA256`. ค่า CA certificate fingerprint กับค่า client SPKI pin เป็นคนละค่า
- **Mac**: `gateway-client-ca.key.pem` เป็นคีย์ CA แบบเข้ารหัส ต้องอยู่กับผู้ดูแลที่ออกใบเท่านั้น ห้ามส่งไป Gateway, Hospital หรือแอป

ก่อนเปลี่ยนไฟล์ที่ใช้งาน ให้เก็บใบเดิมไว้และเตรียมย้อนกลับ หากต่ออายุด้วยคีย์เดิม SPKI ไม่เปลี่ยน หากเปลี่ยนคีย์ ให้เพิ่ม pin ใหม่และเก่าแบบคั่นจุลภาคที่ Hospital ตามแผนก่อนสลับใบ Gateway ทดสอบ mTLS ด้วยข้อมูลทดสอบ แล้วจึงถอด pin เดิม สคริปต์ออกใบในเครื่องเท่านั้น ไม่ SSH แก้ `.env` หรือ restart บริการ
