package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"erp-platform/gateway/internal/bootstrap"
	"golang.org/x/sys/unix"
)

type passwordPrompt func(string) ([]byte, error)

func setupPasswordWithPrompts(ctx context.Context, prompt passwordPrompt, reset func(context.Context, string) (string, error), output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	first, err := prompt("New Admin password: ")
	defer clear(first)
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			return err
		}
		return errors.New("ยกเลิกการป้อนรหัสผ่าน ข้อมูลเดิมยังคงอยู่")
	}
	if len(first) == 0 || len(first) > 4096 {
		return errors.New("รหัสผ่านใหม่ต้องไม่ว่างและไม่เกิน 4096 ไบต์")
	}
	second, err := prompt("Confirm new Admin password: ")
	defer clear(second)
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			return err
		}
		return errors.New("ยกเลิกการยืนยันรหัสผ่าน ข้อมูลเดิมยังคงอยู่")
	}
	if subtle.ConstantTimeCompare(first, second) != 1 {
		return errors.New("รหัสผ่านที่ยืนยันไม่ตรงกัน ข้อมูลเดิมยังคงอยู่")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	username, err := reset(ctx, string(first))
	if err != nil {
		var warning *bootstrap.PasswordResetDurabilityWarning
		if !errors.As(err, &warning) || username == "" {
			return err
		}
		fmt.Fprintln(output, "คำเตือน:", warning.Error())
	}
	fmt.Fprintf(output, "ตั้งรหัสผ่าน Admin ใหม่แล้ว ชื่อผู้ใช้เดิม: %q\nข้อมูล การตั้งค่า ใบรับรอง และ session ของ Mobile ยังคงเดิม ไม่ต้อง restart Gateway\n", username)
	return nil
}

func runSetupPassword(ctx context.Context, directory string, input *os.File, prompts, output io.Writer) error {
	return setupPasswordWithPrompts(ctx, func(prompt string) ([]byte, error) {
		return readTerminalPassword(ctx, input, prompts, prompt)
	}, func(ctx context.Context, password string) (string, error) {
		return bootstrap.RequestAdminPasswordReset(ctx, directory, password)
	}, output)
}

// Read one byte at a time so pasted confirmation input is never consumed by the
// first prompt. Raw, no-echo input also avoids canonical terminal line truncation.
func readTerminalPassword(ctx context.Context, input *os.File, output io.Writer, prompt string) (value []byte, err error) {
	fd := int(input.Fd())
	original, err := getPasswordTerminal(fd)
	if err != nil {
		return nil, errors.New("รัน setup-password ใน Terminal แบบโต้ตอบ: docker compose exec gateway /usr/local/bin/gateway setup-password (ไม่ใช้ -T)")
	}
	hidden := *original
	hidden.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON
	hidden.Cc[unix.VMIN] = 1
	hidden.Cc[unix.VTIME] = 0
	if err := setPasswordTerminal(fd, &hidden); err != nil {
		return nil, errors.New("ปิดการแสดงรหัสผ่านใน Terminal ไม่สำเร็จ")
	}
	defer func() {
		if restoreErr := setPasswordTerminal(fd, original); restoreErr != nil && err == nil {
			clear(value)
			value, err = nil, errors.New("คืนค่าการแสดงผล Terminal ไม่สำเร็จ กรุณาเปิด Terminal ใหม่")
		}
		fmt.Fprintln(output)
	}()
	if _, err := io.WriteString(output, prompt); err != nil {
		return nil, errors.New("แสดงคำถามรหัสผ่านใน Terminal ไม่สำเร็จ")
	}
	line := make([]byte, 0, 64)
	defer func() {
		if err != nil {
			clear(line)
		}
	}()
	overflow := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, errors.New("อ่านรหัสผ่านจาก Terminal ไม่สำเร็จ")
		}
		if fds[0].Revents == 0 {
			continue
		}
		if fds[0].Revents&unix.POLLNVAL != 0 {
			return nil, io.EOF
		}
		var one [1]byte
		n, err := unix.Read(fd, one[:])
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil || n == 0 {
			return nil, io.EOF
		}
		switch one[0] {
		case '\r', '\n':
			if overflow {
				return nil, errors.New("รหัสผ่านใหม่ต้องไม่เกิน 4096 bytes")
			}
			return line, nil
		case 3, 26:
			return nil, context.Canceled
		case 4:
			return nil, io.EOF
		case 21:
			clear(line)
			line = line[:0]
			overflow = false
		case '\b', 127:
			if !overflow && len(line) > 0 {
				_, size := utf8.DecodeLastRune(line)
				clear(line[len(line)-size:])
				line = line[:len(line)-size]
			}
		default:
			if len(line) == 4096 {
				overflow = true
			}
			if !overflow {
				line = append(line, one[0])
			}
		}
	}
}
