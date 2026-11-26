package bootstrap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const backupControlReplyLimit = 128 << 10

var errBackupControlUnavailable = errors.New("ติดต่อช่องสำรองข้อมูลภายในไม่ได้ กรุณารันคำสั่งใน container ของ Gateway ที่กำลังทำงาน และใช้ image รุ่นที่รองรับ backup-export/backup-import")

// backupControlClient cannot dial TCP or obey HTTP proxy environment variables.
// The private filesystem directory/socket are the sole authentication boundary.
func backupControlClient(directory string) (*http.Client, func(), error) {
	private, err := privateControlDirectory(directory, false)
	if err != nil {
		if errors.Is(err, errAdminControlUnavailable) {
			return nil, nil, errBackupControlUnavailable
		}
		return nil, nil, err
	}
	privateInfo, err := os.Lstat(private)
	if err != nil {
		return nil, nil, errBackupControlUnavailable
	}
	owner, ok := privateInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, nil, errBackupControlUnavailable
	}
	socket := filepath.Join(private, "sock")
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 {
		return nil, nil, errBackupControlUnavailable
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner.Uid {
		return nil, nil, errBackupControlUnavailable
	}
	transport := &http.Transport{DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, transport.CloseIdleConnections, nil
}

func backupControlRequest(ctx context.Context, client *http.Client, method, suffix, contentType string, body io.Reader) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, "http://unix/backups"+suffix, body)
	if err != nil {
		return nil, errors.New("เตรียมคำสั่งสำรองข้อมูลภายในไม่ได้")
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("Accept", "application/json, application/octet-stream")
	response, err := client.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("ติดต่อช่องสำรองข้อมูลภายในไม่ได้ กรุณาตรวจว่า Gateway ยังทำงานอยู่")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("Gateway ปฏิเสธคำสั่งสำรองหรือกู้คืน (HTTP %d) ตรวจรายละเอียดในหน้าจัดการ", response.StatusCode)
	}
	return response, nil
}

func backupControlJSON(ctx context.Context, client *http.Client, method, suffix string, input, out any) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return errors.New("เตรียมคำสั่งสำรองข้อมูลไม่ได้")
		}
	}
	response, err := backupControlRequest(ctx, client, method, suffix, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decodeBackupControl(response, out)
}

func decodeBackupControl(response *http.Response, out any) error {
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return errors.New("รูปแบบคำตอบช่องสำรองข้อมูลภายในไม่ถูกต้อง")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, backupControlReplyLimit+1))
	if err != nil || len(body) > backupControlReplyLimit {
		return errors.New("อ่านคำตอบช่องสำรองข้อมูลภายในไม่ได้")
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) || json.Unmarshal(envelope.Data, out) != nil {
		return errors.New("คำตอบช่องสำรองข้อมูลภายในไม่ครบหรือไม่ถูกต้อง")
	}
	return nil
}

func waitBackupControlJob(ctx context.Context, client *http.Client, id string) (backupJob, error) {
	var job backupJob
	if !validBackupID(id) {
		return job, errors.New("Gateway ไม่ยืนยันหมายเลขงานสำรองข้อมูล")
	}
	failures := 0
	for {
		err := backupControlJSON(ctx, client, http.MethodGet, "/jobs/"+id, nil, &job)
		if err == nil {
			failures = 0
			if job.ID != id {
				return job, errors.New("หมายเลขงานตอบกลับไม่ตรงกับงานที่เริ่ม")
			}
			switch job.State {
			case "completed":
				return job, nil
			case "failed":
				return job, errors.New("งานสำรองหรือกู้คืนไม่สำเร็จ ตรวจรายละเอียดในหน้าจัดการ Gateway")
			case "cancelled":
				return job, errors.New("งานถูกยกเลิก ไฟล์สำรองที่ตรวจสอบแล้วอาจยังดาวน์โหลดได้จากหน้าจัดการ Gateway")
			case "running":
			default:
				return job, errors.New("สถานะงานตอบกลับไม่ถูกต้อง")
			}
		} else {
			if ctx.Err() != nil {
				return job, ctx.Err()
			}
			failures++
			if failures >= 10 {
				return job, err
			}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return job, ctx.Err()
		case <-timer.C:
		}
	}
}

func backupControlOutputPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("ระบุพาธไฟล์สำรองและชุดกู้คืน")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("พาธไฟล์สำรองไม่ถูกต้อง")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", errors.New("โฟลเดอร์ปลายทางต้องมีอยู่ก่อนและเขียนไฟล์ได้")
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func openBackupControlOutput(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("สร้างไฟล์ส่งออกไม่ได้ ระบบไม่เขียนทับไฟล์เดิม กรุณาเลือกพาธใหม่")
	}
	if err = file.Chmod(0600); err != nil {
		file.Close()
		os.Remove(path)
		return nil, errors.New("กำหนดสิทธิ์ไฟล์ส่งออกไม่ได้")
	}
	return file, nil
}

type backupControlKit struct {
	Version  int    `json:"version"`
	Secret   string `json:"secret"`
	Dataset  string `json:"dataset"`
	BackupID string `json:"backup_id"`
}

func decodeBackupControlKit(body []byte) (backupControlKit, error) {
	var kit backupControlKit
	if len(body) > 32<<10 || json.Unmarshal(body, &kit) != nil || kit.Version != 1 || !validBackupID(kit.Dataset) {
		return kit, errors.New("ชุดกู้คืนไม่ถูกต้องหรือรูปแบบไม่รองรับ")
	}
	secret, err := backupSecret(kit.Secret)
	if err != nil {
		return kit, errors.New("ชุดกู้คืนขาดกุญแจเปิดไฟล์สำรอง")
	}
	kit.Secret = secret
	return kit, nil
}

// RunBackupExport uses the same coordinator as the web UI. Both output files
// are exclusive, private and removed if any step fails; passwords never appear
// in process arguments, terminal output or logs.
func RunBackupExport(ctx context.Context, configDir, bundlePath, kitPath string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	bundlePath, err := backupControlOutputPath(bundlePath)
	if err != nil {
		return err
	}
	kitPath, err = backupControlOutputPath(kitPath)
	if err != nil {
		return err
	}
	if bundlePath == kitPath {
		return errors.New("ไฟล์สำรองและชุดกู้คืนต้องใช้คนละพาธ")
	}
	bundle, err := openBackupControlOutput(bundlePath)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		bundle.Close()
		if !complete {
			_ = os.Remove(bundlePath)
		}
	}()
	kit, err := openBackupControlOutput(kitPath)
	if err != nil {
		return err
	}
	defer func() {
		kit.Close()
		if !complete {
			_ = os.Remove(kitPath)
		}
	}()
	client, cleanup, err := backupControlClient(configDir)
	if err != nil {
		return err
	}
	defer cleanup()
	var started struct {
		Job backupJob `json:"job"`
	}
	if err = backupControlJSON(ctx, client, http.MethodPost, "", map[string]any{}, &started); err != nil {
		return err
	}
	job, err := waitBackupControlJob(ctx, client, started.Job.ID)
	if err != nil {
		return err
	}
	if !validBackupID(job.BackupID) {
		return errors.New("งานสำรองไม่ยืนยันชุดข้อมูลที่ดาวน์โหลดได้")
	}
	response, err := backupControlRequest(ctx, client, http.MethodGet, "/"+job.BackupID+"/download", "", nil)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/octet-stream") || response.ContentLength > maxFullBackupBytes {
		response.Body.Close()
		return errors.New("ไฟล์สำรองตอบกลับไม่ถูกต้องหรือใหญ่เกินขอบเขต")
	}
	n, copyErr := io.Copy(bundle, io.LimitReader(response.Body, maxFullBackupBytes+1))
	response.Body.Close()
	if copyErr != nil || n < 1 || n > maxFullBackupBytes {
		return errors.New("ดาวน์โหลดไฟล์สำรองไม่ครบ กรุณาลองใหม่")
	}
	if err = bundle.Sync(); err != nil {
		return errors.New("บันทึกไฟล์สำรองลงดิสก์ไม่สำเร็จ")
	}
	if err = bundle.Close(); err != nil {
		return errors.New("ปิดไฟล์สำรองไม่สำเร็จ")
	}
	requestBody, _ := json.Marshal(map[string]string{"backup_id": job.BackupID})
	response, err = backupControlRequest(ctx, client, http.MethodPost, "/recovery-kit", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (32<<10)+1))
	response.Body.Close()
	if readErr != nil || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return errors.New("ดาวน์โหลดชุดกู้คืนไม่ครบ กรุณาลองใหม่")
	}
	decoded, err := decodeBackupControlKit(body)
	if err != nil {
		return err
	}
	if decoded.BackupID != "" && decoded.BackupID != job.BackupID {
		return errors.New("ชุดกู้คืนไม่ตรงกับไฟล์สำรอง ระบบไม่บันทึกไฟล์ที่ไม่ตรงกัน")
	}
	if _, err = kit.Write(body); err != nil {
		return errors.New("บันทึกชุดกู้คืนไม่สำเร็จ")
	}
	if err = kit.Sync(); err != nil {
		return errors.New("บันทึกชุดกู้คืนลงดิสก์ไม่สำเร็จ")
	}
	if err = kit.Close(); err != nil {
		return errors.New("ปิดไฟล์ชุดกู้คืนไม่สำเร็จ")
	}
	complete = true
	return nil
}

func backupControlInput(path string, max int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > max {
		return nil, errors.New("ไฟล์นำเข้าต้องเป็นไฟล์ปกติที่มีข้อมูลและไม่เกินขอบเขต")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("เปิดไฟล์นำเข้าไม่ได้")
	}
	current, err := file.Stat()
	if err != nil || !os.SameFile(info, current) {
		file.Close()
		return nil, errors.New("ไฟล์นำเข้าเปลี่ยนระหว่างตรวจสอบ")
	}
	return file, nil
}

func uploadBackupControl(ctx context.Context, client *http.Client, bundle *os.File, secret, public string) (backupJob, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		err := multipartWriter.WriteField("recovery_secret", secret)
		if err == nil {
			err = multipartWriter.WriteField("public_url", public)
		}
		var part io.Writer
		if err == nil {
			part, err = multipartWriter.CreateFormFile("file", "gateway-backup.bundle")
		}
		if err == nil {
			_, err = io.Copy(part, io.LimitReader(bundle, maxFullBackupBytes+1))
		}
		if err == nil {
			err = multipartWriter.Close()
		}
		_ = writer.CloseWithError(err)
		done <- err
	}()
	response, err := backupControlRequest(ctx, client, http.MethodPost, "/restore", multipartWriter.FormDataContentType(), reader)
	reader.CloseWithError(err)
	uploadErr := <-done
	if err != nil {
		return backupJob{}, err
	}
	defer response.Body.Close()
	if uploadErr != nil {
		return backupJob{}, errors.New("ส่งไฟล์นำเข้าไม่ครบ กรุณาลองใหม่")
	}
	var started struct {
		Job backupJob `json:"job"`
	}
	if err = decodeBackupControl(response, &started); err != nil {
		return backupJob{}, err
	}
	return waitBackupControlJob(ctx, client, started.Job.ID)
}

func backupConfirmation(ctx context.Context, reader *bufio.Reader, want string) bool {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() { line, err := reader.ReadString('\n'); done <- result{line, err} }()
	select {
	case <-ctx.Done():
		return false
	case got := <-done:
		if got.err != nil && got.err != io.EOF {
			return false
		}
		return len(got.line) <= 4096 && strings.TrimSuffix(strings.TrimSuffix(got.line, "\n"), "\r") == want
	}
}

func printBackupInspection(output io.Writer, inspection restoreInspection) error {
	months := []string{"ม.ค.", "ก.พ.", "มี.ค.", "เม.ย.", "พ.ค.", "มิ.ย.", "ก.ค.", "ส.ค.", "ก.ย.", "ต.ค.", "พ.ย.", "ธ.ค."}
	at := inspection.CreatedAt.In(time.FixedZone("Asia/Bangkok", 7*60*60))
	if _, err := fmt.Fprintf(output, "ข้อมูลสำรอง: %d %s %d เวลา %s น.\n", at.Day(), months[int(at.Month())-1], at.Year()+543, at.Format("15:04")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "ที่อยู่ Gateway ปลายทาง: %s\n", inspection.PublicURL); err != nil {
		return err
	}
	for _, entry := range []struct{ key, label string }{{"hospitals", "หน่วยงาน"}, {"sessions", "เซสชัน Mobile"}, {"audit", "ประวัติ"}, {"notify_jobs", "งานแจ้งเตือน"}, {"update_jobs", "งานอัปเดต"}} {
		if count, ok := inspection.Summary[entry.key]; ok {
			if _, err := fmt.Fprintf(output, "%s: %d\n", entry.label, count); err != nil {
				return err
			}
		}
	}
	if inspection.RepairRequired || inspection.CertificateExpired || inspection.HospitalCAExpired {
		_, err := fmt.Fprintln(output, "กู้คืนข้อมูลได้ แต่ต้องแก้ไขหรือต่ออายุใบรับรองผ่านหน้าจัดการก่อนเชื่อม ERP")
		return err
	}
	return nil
}

// RunBackupImport stages and inspects before asking for the two explicit
// confirmations. Its Unix control authentication survives Admin session/key
// replacement; it never signs into or falls back to a public HTTP endpoint.
func RunBackupImport(ctx context.Context, configDir, bundlePath, kitPath, publicURL string, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	if input == nil || output == nil || len(publicURL) > 2048 || strings.ContainsAny(publicURL, "\r\n\x00") {
		return errors.New("ข้อมูลหรือช่องยืนยันการนำเข้าไม่ถูกต้อง")
	}
	bundle, err := backupControlInput(bundlePath, maxFullBackupBytes)
	if err != nil {
		return err
	}
	defer bundle.Close()
	kitFile, err := backupControlInput(kitPath, 32<<10)
	if err != nil {
		return err
	}
	defer kitFile.Close()
	bundleInfo, _ := bundle.Stat()
	kitInfo, _ := kitFile.Stat()
	if os.SameFile(bundleInfo, kitInfo) {
		return errors.New("ไฟล์สำรองและชุดกู้คืนต้องเป็นคนละไฟล์")
	}
	body, err := io.ReadAll(io.LimitReader(kitFile, (32<<10)+1))
	if err != nil {
		return errors.New("อ่านชุดกู้คืนไม่ได้")
	}
	kit, err := decodeBackupControlKit(body)
	if err != nil {
		return err
	}
	client, cleanup, err := backupControlClient(configDir)
	if err != nil {
		return err
	}
	defer cleanup()
	job, err := uploadBackupControl(ctx, client, bundle, kit.Secret, publicURL)
	if err != nil {
		return err
	}
	if !validBackupID(job.RestoreID) {
		return errors.New("การตรวจไฟล์ไม่ยืนยันชุดข้อมูลสำหรับนำเข้า")
	}
	var inspection restoreInspection
	if err = backupControlJSON(ctx, client, http.MethodGet, "/restore/"+job.RestoreID, nil, &inspection); err != nil {
		return err
	}
	if inspection.ID != job.RestoreID || inspection.CreatedAt.IsZero() || !inspection.Ready {
		return errors.New("ชุดข้อมูลยังไม่พร้อมนำเข้า กรุณาตรวจในหน้าจัดการ")
	}
	if err = printBackupInspection(output, inspection); err != nil {
		return errors.New("แสดงผลการตรวจไฟล์ไม่ได้ ยังไม่เปิดใช้ชุดข้อมูล")
	}
	if _, err = fmt.Fprintln(output, "ข้อมูลเครื่องปลายทางจะถูกแทนที่ พิมพ์ RESTORE เพื่อยืนยัน:"); err != nil {
		return err
	}
	reader := bufio.NewReader(io.LimitReader(input, 8193))
	if !backupConfirmation(ctx, reader, "RESTORE") {
		return errors.New("ยกเลิกการนำเข้า ยังไม่เปิดใช้ชุดข้อมูล")
	}
	if _, err = fmt.Fprintln(output, "หยุดหรือพัก Gateway ต้นทางแล้วเท่านั้น พิมพ์ SOURCE STOPPED เพื่อยืนยัน:"); err != nil {
		return err
	}
	if !backupConfirmation(ctx, reader, "SOURCE STOPPED") {
		return errors.New("ยกเลิกการนำเข้า ยังไม่เปิดใช้ชุดข้อมูล")
	}
	var activation struct {
		Job backupJob `json:"job"`
	}
	err = backupControlJSON(ctx, client, http.MethodPost, "/restore/"+job.RestoreID+"/activate", map[string]any{"confirm_id": job.RestoreID, "source_stopped": true, "public_url": publicURL}, &activation)
	if err == nil {
		_, err = waitBackupControlJob(ctx, client, activation.Job.ID)
	}
	if err != nil {
		return errors.New("ยืนยันผลการเปิดใช้ข้อมูลไม่ได้ ปลายทางอาจเปิดใช้งานแล้ว กรุณาตรวจผลก่อนเปิดต้นทางอีกครั้ง")
	}
	_, err = fmt.Fprintln(output, "นำเข้าข้อมูล Gateway เสร็จแล้ว ตรวจ public endpoint และการเชื่อม ERP ก่อนสลับให้ Mobile ใช้งาน")
	return err
}

// RunMigrationCancelOffline is deliberately separate from ordinary migration
// cancellation. Only a local operator who has stopped the destination and
// selected the source dataset as authoritative may override a missing ack.
func RunMigrationCancelOffline(ctx context.Context, configDir string, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	if input == nil || output == nil {
		return errors.New("ระบุช่องยืนยันการกลับมาใช้เครื่องต้นทาง")
	}
	client, cleanup, err := backupControlClient(configDir)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err = fmt.Fprintln(output, "การกลับมาใช้ข้อมูลต้นทางอาจทำให้ข้อมูลที่ปลายทางรับหลังสลับไม่ติดมาด้วย หากปลายทางเคยรับข้อมูลใหม่ ให้สำรองข้อมูลปลายทางก่อนและเก็บไว้แยกต่างหาก"); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(output, "หยุด Gateway ปลายทางให้แน่ชัดแล้ว พิมพ์ DESTINATION STOPPED เพื่อยืนยัน:"); err != nil {
		return err
	}
	reader := bufio.NewReader(io.LimitReader(input, 8193))
	if !backupConfirmation(ctx, reader, "DESTINATION STOPPED") {
		return errors.New("ยกเลิกคำสั่ง เครื่องต้นทางยังคงพักการทำงาน")
	}
	if _, err = fmt.Fprintln(output, "ยอมรับใช้ชุดข้อมูลต้นทางเป็นข้อมูลหลัก พิมพ์ USE SOURCE DATA เพื่อยืนยัน:"); err != nil {
		return err
	}
	if !backupConfirmation(ctx, reader, "USE SOURCE DATA") {
		return errors.New("ยกเลิกคำสั่ง เครื่องต้นทางยังคงพักการทำงาน")
	}
	var result struct {
		Cancelled bool `json:"cancelled"`
	}
	err = backupControlJSON(ctx, client, http.MethodPost, "/migration/cancel", map[string]any{"destination_inactive_confirmed": true, "offline_override": true, "authoritative_source": "USE SOURCE DATA"}, &result)
	if err != nil || !result.Cancelled {
		return errors.New("ยืนยันผลการกลับมาใช้ต้นทางไม่ได้ กรุณาตรวจสถานะทั้งสองเครื่องก่อนเปิดบริการเพิ่ม")
	}
	_, err = fmt.Fprintln(output, "ยกเลิกการย้ายและกลับมาใช้ข้อมูลต้นทางแล้ว ต้องคงเครื่องปลายทางไว้ในสถานะหยุดจนกว่าจะจัดการข้อมูลที่แตกต่างกันเรียบร้อย")
	return err
}
