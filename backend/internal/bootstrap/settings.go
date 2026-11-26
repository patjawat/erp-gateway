package bootstrap

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
)

// PublicSettings contains only the fields the authenticated settings form may edit.
// Credentials, keys, identities and database locations cannot enter this contract.
type PublicSettings struct {
	PublicURL            string   `json:"public_url"`
	TrustedProxyCIDRs    []string `json:"trusted_proxy_cidrs"`
	ApprovedUpstreams    []string `json:"approved_upstreams"`
	PrivateUpstreamCIDRs []string `json:"private_upstream_cidrs"`
}

func cloneStrings(values []string) []string { return append([]string{}, values...) }

func publicSettings(c RuntimeConfig) PublicSettings {
	return PublicSettings{PublicURL: c.PublicURL, TrustedProxyCIDRs: cloneStrings(c.TrustedProxyCIDRs), ApprovedUpstreams: cloneStrings(c.ApprovedUpstreams), PrivateUpstreamCIDRs: cloneStrings(c.PrivateUpstreamCIDRs)}
}

func validatePublicSettings(in PublicSettings, validatePolicy func([]string, []string) error) error {
	if len(in.PublicURL) > 2048 || in.PublicURL != strings.TrimSpace(in.PublicURL) {
		return errors.New("ตรวจสอบ HTTPS URL ของ Gateway")
	}
	if _, err := publicURL(in.PublicURL); err != nil {
		return err
	}
	if len(in.TrustedProxyCIDRs) > 32 || len(in.ApprovedUpstreams) > 10000 || len(in.PrivateUpstreamCIDRs) > 128 {
		return errors.New("จำนวนการตั้งค่าเครือข่ายเกินขอบเขต")
	}
	seen := map[string]bool{}
	for _, value := range in.TrustedProxyCIDRs {
		ip, network, err := net.ParseCIDR(value)
		ones, bits := networkSize(network)
		if err != nil || ip == nil || ip.IsUnspecified() || ip.IsMulticast() || !ip.Equal(network.IP) || len(value) > 128 || seen[value] || ones == 0 || (bits == 32 && ones < 16 && !ip.IsLoopback()) || (bits == 128 && ones < 48 && !ip.IsLoopback()) {
			return errors.New("Trusted proxy ต้องเป็น CIDR เฉพาะ proxy: IPv4 /16 ขึ้นไป หรือ IPv6 /48 ขึ้นไป และห้ามใช้เครือข่ายทั้งหมด")
		}
		seen[value] = true
	}
	for _, entry := range in.ApprovedUpstreams {
		if len(entry) > 4096 {
			return errors.New("ปลายทาง Hospital API ยาวเกินขอบเขต")
		}
	}
	for _, entry := range in.PrivateUpstreamCIDRs {
		if len(entry) > 128 {
			return errors.New("Private network CIDR ยาวเกินขอบเขต")
		}
	}
	if validatePolicy == nil {
		return errors.New("network policy validator is unavailable")
	}
	return validatePolicy(in.ApprovedUpstreams, in.PrivateUpstreamCIDRs)
}

// currentLocked refuses out-of-band edits rather than silently overwriting them.
func (m *Manager) currentLocked() (RuntimeConfig, []byte, error) {
	if m.current == nil || m.config.Version != 1 {
		return RuntimeConfig{}, nil, errors.New("การตั้งค่าผ่านเว็บต้องใช้ persistent setup; ระบบ legacy ต้องนำเข้าค่าเดิมก่อน")
	}
	cfg, key, err := LoadRuntime(m.options.Directory)
	if err != nil {
		return cfg, nil, errors.New("อ่าน config/key ปัจจุบันไม่สำเร็จ กรุณาตรวจ volume หรือกู้คืน backup")
	}
	if subtle.ConstantTimeCompare(key, m.key) != 1 {
		return cfg, nil, errors.New("encryption key ใน volume เปลี่ยนไป กรุณากู้คืนหรือ restart อย่างปลอดภัย")
	}
	want, _ := json.Marshal(m.config)
	got, _ := json.Marshal(cfg)
	if !bytes.Equal(want, got) {
		return cfg, nil, errors.New("config ถูกแก้จากภายนอก กรุณาตรวจและ restart ก่อนบันทึกผ่านเว็บ")
	}
	return cfg, append([]byte(nil), key...), nil
}

func (m *Manager) Settings() (PublicSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, _, err := m.currentLocked()
	if err != nil {
		return PublicSettings{}, err
	}
	return publicSettings(cfg), nil
}

// ApplySettings validates and builds a candidate without changing credentials or
// trust. The root Admin handler separately protects active hospital destinations.
func (m *Manager) ApplySettings(ctx context.Context, in PublicSettings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePublicSettings(in, m.options.ValidatePolicy); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, key, err := m.currentLocked()
	if err != nil {
		return err
	}
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		return err
	}
	defer unlock()
	cfg.PublicURL = in.PublicURL
	cfg.TrustedProxyCIDRs = cloneStrings(in.TrustedProxyCIDRs)
	cfg.ApprovedUpstreams = cloneStrings(in.ApprovedUpstreams)
	cfg.PrivateUpstreamCIDRs = cloneStrings(in.PrivateUpstreamCIDRs)
	if err = m.validateStored(cfg, key); err != nil {
		return err
	}
	runtime, err := m.options.Build(cfg, key)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err == nil {
		err = persistJSON(filepath.Join(m.options.Directory, "runtime.json"), cfg)
	}
	if err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		return err
	}
	m.config = cfg
	m.install(runtime)
	return nil
}
