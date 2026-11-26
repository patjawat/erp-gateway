package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const adminControlBodyLimit = 32 << 10

var errAdminControlUnavailable = errors.New("ติดต่อช่องควบคุมภายในของ Gateway ไม่ได้ กรุณารันคำสั่งใน container ของ Gateway ที่กำลังทำงาน และใช้ image รุ่นที่รองรับ setup-password")

type adminControl struct {
	server    *http.Server
	listener  *net.UnixListener
	lock      *os.File
	socket    string
	identity  os.FileInfo
	done      chan struct{}
	closeOnce sync.Once
}

func privateControlDirectory(directory string, create bool) (string, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", errors.New("ตำแหน่ง config ของ Gateway ไม่ถูกต้อง")
	}
	private := filepath.Join(directory, ".admin")
	if create {
		if err := os.Mkdir(private, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", errors.New("สร้างไดเรกทอรีช่องควบคุมภายในไม่ได้ กรุณาตรวจสิทธิ์ config volume")
		}
	}
	info, err := os.Lstat(private)
	if err != nil {
		return "", errAdminControlUnavailable
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || (int(stat.Uid) != os.Geteuid() && (create || os.Geteuid() != 0)) {
		return "", errors.New("ไดเรกทอรี .admin ต้องเป็นไดเรกทอรีส่วนตัวของผู้รัน Gateway (0700) และไม่ใช่ symlink")
	}
	return private, nil
}

// StartAdminControl serves only a private filesystem socket. It must never be
// registered on the public Gateway HTTP router. Possession of this socket is
// deliberately equivalent to local access to the Gateway configuration volume.
func StartAdminControl(directory string, reset func(context.Context, string) (string, error), controls ...http.Handler) (io.Closer, error) {
	if reset == nil {
		return nil, errors.New("password reset operation is unavailable")
	}
	if len(controls) > 1 {
		return nil, errors.New("only one private backup control handler is supported")
	}
	private, err := privateControlDirectory(directory, true)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(private, "lock")
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, errors.New("เปิด lock ของช่องควบคุมภายในไม่ได้")
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	info, err := lock.Stat()
	if err != nil {
		lock.Close()
		return nil, errors.New("ตรวจ lock ของช่องควบคุมภายในไม่ได้")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 || int(stat.Uid) != os.Geteuid() {
		lock.Close()
		return nil, errors.New("lock ของช่องควบคุมภายในต้องเป็นไฟล์ของผู้รัน Gateway และไม่มีลิงก์")
	}
	if err := lock.Chmod(0600); err != nil {
		lock.Close()
		return nil, errors.New("กำหนดสิทธิ์ lock ของช่องควบคุมภายในไม่ได้")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("มี Gateway ใช้ช่องควบคุมภายในของ config volume นี้อยู่แล้ว")
	}
	release := func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = lock.Close() }
	socket := filepath.Join(private, "sock")
	if info, err := os.Lstat(socket); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSocket == 0 || int(stat.Uid) != os.Geteuid() {
			release()
			return nil, errors.New("พาธช่องควบคุมภายในถูกใช้โดยไฟล์อื่น ระบบจะไม่เขียนทับ")
		}
		connection, dialErr := net.DialTimeout("unix", socket, 200*time.Millisecond)
		if dialErr == nil {
			connection.Close()
			release()
			return nil, errors.New("socket ควบคุมภายในยังมีบริการใช้งานอยู่ ระบบจะไม่แทนที่")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			release()
			return nil, errors.New("ยืนยันไม่ได้ว่า socket ควบคุมภายในหยุดทำงานแล้ว ระบบจะไม่แทนที่")
		}
		if err := os.Remove(socket); err != nil {
			release()
			return nil, errors.New("ล้าง socket ที่หยุดทำงานแล้วไม่ได้")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		release()
		return nil, errors.New("ตรวจพาธช่องควบคุมภายในไม่ได้")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		release()
		return nil, errors.New("เปิด socket ควบคุมภายในไม่ได้ กรุณาตรวจสิทธิ์และใช้พาธ config ที่สั้นพอสำหรับ Unix socket")
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socket, 0600); err != nil {
		listener.Close()
		os.Remove(socket)
		release()
		return nil, errors.New("กำหนดสิทธิ์ socket ควบคุมภายในไม่ได้")
	}
	identity, err := os.Lstat(socket)
	if err != nil {
		listener.Close()
		os.Remove(socket)
		release()
		return nil, errors.New("ตรวจ socket ควบคุมภายในไม่ได้")
	}
	control := &adminControl{listener: listener, lock: lock, socket: socket, identity: identity, done: make(chan struct{})}
	control.server = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(controls) == 1 && controls[0] != nil && (r.URL.Path == "/backups" || strings.HasPrefix(r.URL.Path, "/backups/")) {
				if path.Clean(r.URL.Path) != r.URL.Path {
					jsonError(w, http.StatusNotFound, "NOT_FOUND", "ไม่พบคำสั่งควบคุมภายใน")
					return
				}
				copy := r.Clone(r.Context())
				copy.URL.Path = "/admin/api/backups" + strings.TrimPrefix(r.URL.Path, "/backups")
				copy.URL.RawPath = ""
				w.Header().Set("Cache-Control", "no-store, private")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				http.NewResponseController(w).SetReadDeadline(time.Now().Add(20 * time.Minute))
				http.NewResponseController(w).SetWriteDeadline(time.Now().Add(20 * time.Minute))
				controls[0].ServeHTTP(w, copy)
				return
			}
			if r.URL.Path != "/password" || r.URL.RawQuery != "" {
				jsonError(w, http.StatusNotFound, "NOT_FOUND", "ไม่พบคำสั่งควบคุมภายใน")
				return
			}
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				jsonError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "ใช้ POST สำหรับคำสั่งนี้")
				return
			}
			var in struct {
				Password string `json:"password"`
			}
			if !readJSON(w, r, &in, adminControlBodyLimit) {
				return
			}
			if in.Password == "" || len(in.Password) > 4096 {
				jsonError(w, http.StatusBadRequest, "INVALID_PASSWORD", "รหัสผ่านใหม่ต้องไม่ว่างและไม่เกิน 4096 ไบต์")
				return
			}
			username, err := reset(r.Context(), in.Password)
			var durabilityWarning *PasswordResetDurabilityWarning
			warning := errors.As(err, &durabilityWarning)
			if err != nil && (!warning || username == "") {
				// Never forward an unexpected callback/driver error containing input.
				jsonError(w, http.StatusConflict, "PASSWORD_RESET_FAILED", "เปลี่ยนรหัสผ่านไม่สำเร็จ กรุณาตรวจว่า Gateway ตั้งค่าผ่านเว็บแล้วและ config/key เดิมครบ ข้อมูลเดิมยังคงอยู่")
				return
			}
			log.Print("Gateway administrator password reset from local console")
			jsonData(w, map[string]any{"username": username, "durability_warning": warning})
		}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 45 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 4 << 10,
	}
	go func() {
		defer close(control.done)
		if err := control.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print("Gateway local administrator control listener stopped")
		}
	}()
	return control, nil
}

func (control *adminControl) Close() error {
	control.closeOnce.Do(func() {
		_ = control.server.Close()
		<-control.done
		if current, err := os.Lstat(control.socket); err == nil && os.SameFile(current, control.identity) {
			_ = os.Remove(control.socket)
		}
		_ = unix.Flock(int(control.lock.Fd()), unix.LOCK_UN)
		_ = control.lock.Close()
	})
	return nil
}

// RequestAdminPasswordReset never dials TCP or uses HTTP proxy environment
// variables; the only destination is the current Gateway's private Unix socket.
func RequestAdminPasswordReset(ctx context.Context, directory, password string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if password == "" || len(password) > 4096 {
		return "", errors.New("รหัสผ่านใหม่ต้องไม่ว่างและไม่เกิน 4096 ไบต์")
	}
	private, err := privateControlDirectory(directory, false)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(filepath.Join(private, "sock"))
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return "", errAdminControlUnavailable
	}
	body, err := json.Marshal(struct {
		Password string `json:"password"`
	}{password})
	if err != nil {
		return "", errors.New("เตรียมคำสั่งเปลี่ยนรหัสผ่านไม่ได้")
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(private, "sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/password", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("เตรียมคำสั่งเปลี่ยนรหัสผ่านไม่ได้")
	}
	r.Header.Set("Content-Type", "application/json")
	response, err := client.Do(r)
	if err != nil {
		var network *net.OpError
		if errors.As(err, &network) && network.Op == "dial" {
			return "", errAdminControlUnavailable
		}
		// A timeout/cancellation after sending may occur after atomic commit.
		return "", errors.New("ยืนยันผลการเปลี่ยนรหัสผ่านไม่ได้ รหัสผ่านใหม่อาจถูกบันทึกแล้ว กรุณาตรวจการเข้าสู่ระบบหรือรัน setup-password ใหม่")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<10)+1))
	if err != nil || len(data) > 8<<10 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return "", errors.New("ข้อมูลตอบกลับจากช่องควบคุมภายในไม่ถูกต้อง กรุณาตรวจการเข้าสู่ระบบก่อนลองใหม่")
	}
	var result struct {
		Data struct {
			Username          string `json:"username"`
			DurabilityWarning bool   `json:"durability_warning"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &result) != nil {
		return "", errors.New("ข้อมูลตอบกลับจากช่องควบคุมภายในไม่ถูกต้อง กรุณาตรวจการเข้าสู่ระบบก่อนลองใหม่")
	}
	if response.StatusCode != http.StatusOK {
		if result.Error.Code == "PASSWORD_RESET_FAILED" {
			return "", errors.New("เปลี่ยนรหัสผ่านไม่สำเร็จ กรุณาตรวจว่า Gateway ตั้งค่าผ่านเว็บแล้วและ config/key เดิมครบ ข้อมูลเดิมยังคงอยู่")
		}
		return "", errors.New("ช่องควบคุมภายในปฏิเสธคำสั่งเปลี่ยนรหัสผ่าน")
	}
	if result.Data.Username == "" || len(result.Data.Username) > 100 {
		return "", errors.New("ข้อมูลชื่อผู้ดูแลจากช่องควบคุมภายในไม่ถูกต้อง กรุณาตรวจการเข้าสู่ระบบก่อนลองใหม่")
	}
	if result.Data.DurabilityWarning {
		return result.Data.Username, &PasswordResetDurabilityWarning{}
	}
	return result.Data.Username, nil
}
