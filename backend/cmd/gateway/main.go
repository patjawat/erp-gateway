package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"erp-platform/gateway/internal/admin"
	"erp-platform/gateway/internal/bootstrap"
	"erp-platform/gateway/internal/gateway"
	"erp-platform/gateway/internal/transportauth"
	"erp-platform/gateway/internal/webtransport"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func boolean(key string, fallback bool) (bool, error) {
	if os.Getenv(key) == "" {
		return fallback, nil
	}
	return strconv.ParseBool(os.Getenv(key))
}
func entries(key string) []string {
	var result []string
	for _, value := range strings.Split(os.Getenv(key), ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func webPolicyFromEnvironment() (webtransport.Policy, error) {
	allowLANHTTP, err := boolean("GATEWAY_ALLOW_LAN_HTTP", false)
	if err != nil {
		return webtransport.Policy{}, errors.New("invalid GATEWAY_ALLOW_LAN_HTTP: use true or false")
	}
	policy, err := webtransport.New(allowLANHTTP, entries("GATEWAY_LAN_HTTP_CIDRS"))
	if err != nil {
		return webtransport.Policy{}, fmt.Errorf("invalid GATEWAY_LAN_HTTP_CIDRS: %w", err)
	}
	return policy, nil
}

func fileExists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
func main() {
	address := env("LISTEN_ADDR", ":8080")
	databasePath := env("GATEWAY_DATABASE_PATH", "data/gateway.db")
	configDir := env("GATEWAY_CONFIG_DIR", "data/config")
	if len(os.Args) == 4 && os.Args[1] == "restore-config" {
		webPolicy, err := webPolicyFromEnvironment()
		if err != nil {
			log.Fatal(err)
		}
		f, err := os.Open(os.Args[2])
		if err != nil {
			log.Fatal("cannot read recovery archive")
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > bootstrap.MaxRecoveryArchive {
			f.Close()
			log.Fatal("recovery archive must be a bounded regular file")
		}
		archive, err := io.ReadAll(io.LimitReader(f, bootstrap.MaxRecoveryArchive+1))
		f.Close()
		if err != nil || len(archive) > bootstrap.MaxRecoveryArchive {
			log.Fatal("cannot read bounded recovery archive")
		}
		reader := bufio.NewReader(io.LimitReader(os.Stdin, 4098))
		passphrase, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			log.Fatal("cannot read recovery passphrase from standard input")
		}
		passphrase = strings.TrimSuffix(strings.TrimSuffix(passphrase, "\n"), "\r")
		if err = bootstrap.RestoreRecoveryWithPolicy(context.Background(), archive, passphrase, os.Args[3], gateway.ValidateUpstreamPolicy, webPolicy); err != nil {
			log.Fatal("recovery refused: ", err)
		}
		fmt.Println("Gateway configuration restored into a new private directory; restore the matching consistent database backup separately")
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			os.Exit(1)
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil {
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "setup-code" {
		webPolicy, err := webPolicyFromEnvironment()
		if err != nil {
			log.Fatal(err)
		}
		if cfg, key, err := bootstrap.LoadRuntime(configDir); err == nil && bootstrap.CheckDatabaseKey(databasePath, key) == nil {
			_, urlErr := webPolicy.ValidateURL(cfg.PublicURL)
			certPEM, certErr := os.ReadFile(cfg.ClientCertFile)
			keyPEM, keyErr := os.ReadFile(cfg.ClientKeyFile)
			_, identityErr := bootstrap.ValidateIdentity(certPEM, keyPEM)
			if urlErr == nil && certErr == nil && keyErr == nil && identityErr == nil && gateway.ValidateUpstreamPolicy(cfg.ApprovedUpstreams, cfg.PrivateUpstreamCIDRs) == nil {
				log.Fatal("Gateway is configured; use the authenticated administrator interface")
			}
		}
		code, err := bootstrap.IssueSetupCode(configDir)
		if err != nil {
			log.Fatal("cannot issue local setup code: ", err)
		}
		// Only this explicitly invoked local command prints the short-lived credential.
		fmt.Println(code)
		return
	}
	if len(os.Args) > 1 {
		log.Fatal("supported commands: healthcheck, setup-code, restore-config <archive> <new-directory> (passphrase on stdin)")
	}
	allowHTTP, err := boolean("GATEWAY_ALLOW_HTTP_UPSTREAM", false)
	if err != nil || allowHTTP {
		log.Fatal("Gateway-to-Hospital transport requires HTTPS mutual TLS")
	}
	legacySecure, err := boolean("GATEWAY_COOKIE_SECURE", true)
	if err != nil {
		log.Fatal("invalid GATEWAY_COOKIE_SECURE")
	}
	webPolicy, err := webPolicyFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	var manager *bootstrap.Manager
	management := &gateway.ManagementState{}
	build := func(c bootstrap.RuntimeConfig, key []byte) (bootstrap.Runtime, error) {
		if err := bootstrap.CheckDatabaseKey(databasePath, key); err != nil {
			return bootstrap.Runtime{}, err
		}
		clientCert, err := transportauth.LoadPair(c.ClientCertFile, c.ClientKeyFile)
		if err != nil {
			return bootstrap.Runtime{}, err
		}
		var hospitalRoots *x509.CertPool
		if c.HospitalCAFile != "" {
			hospitalRoots, err = transportauth.LoadRoots(c.HospitalCAFile, true)
			if err != nil {
				return bootstrap.Runtime{}, err
			}
		}
		if err = gateway.ValidateUpstreamPolicy(c.ApprovedUpstreams, c.PrivateUpstreamCIDRs); err != nil {
			return bootstrap.Runtime{}, err
		}
		store, err := gateway.OpenStore(databasePath, key)
		if err != nil {
			return bootstrap.Runtime{}, err
		}
		workerCtx, cancelWorker := context.WithCancel(context.Background())
		var sender gateway.PushSender
		if path := os.Getenv("FCM_CREDENTIALS_FILE"); path != "" {
			sender, err = gateway.NewFCMSender(workerCtx, path)
			if err != nil {
				cancelWorker()
				store.Close()
				return bootstrap.Runtime{}, err
			}
		}
		cfg := gateway.Config{Store: store, AdminUsername: c.AdminUsername, PublicURL: c.PublicURL, WebPolicy: webPolicy, CookieSecure: !webPolicy.IsLANHTTPURL(c.PublicURL), TrustedProxyCIDRs: c.TrustedProxyCIDRs, ApprovedUpstreams: c.ApprovedUpstreams, PrivateUpstreamCIDRs: c.PrivateUpstreamCIDRs, NotificationSender: sender, GatewayClientCert: &clientCert, HospitalServerRoots: hospitalRoots, Management: management}
		if c.Version == 1 {
			cfg.AdminPasswordVerifier = c.VerifyPassword
			cfg.IdentityDirectory = filepath.Join(configDir, "csr")
			cfg.ActivateIdentity = func(ctx context.Context, cert, key, roots []byte) error {
				return manager.ReplaceIdentity(ctx, cert, key, roots)
			}
			cfg.ReadSettings = func() (gateway.RuntimeSettings, error) {
				in, err := manager.Settings()
				return gateway.RuntimeSettings{PublicURL: in.PublicURL, TrustedProxyCIDRs: in.TrustedProxyCIDRs, ApprovedUpstreams: in.ApprovedUpstreams, PrivateUpstreamCIDRs: in.PrivateUpstreamCIDRs}, err
			}
			cfg.ApplySettings = func(ctx context.Context, in gateway.RuntimeSettings) error {
				return manager.ApplySettings(ctx, bootstrap.PublicSettings{PublicURL: in.PublicURL, TrustedProxyCIDRs: in.TrustedProxyCIDRs, ApprovedUpstreams: in.ApprovedUpstreams, PrivateUpstreamCIDRs: in.PrivateUpstreamCIDRs})
			}
			cfg.ExportRecovery = func(ctx context.Context, passphrase string) ([]byte, error) {
				return manager.ExportRecovery(ctx, passphrase)
			}
		} else {
			cfg.AdminPassword = os.Getenv("GATEWAY_ADMIN_PASSWORD")
			cfg.CookieSecure = legacySecure
		}
		app, err := gateway.New(cfg)
		if err != nil {
			cancelWorker()
			store.Close()
			return bootstrap.Runtime{}, err
		}
		var started sync.Once
		var done chan struct{}
		var startMu sync.Mutex
		start := func() {
			started.Do(func() {
				startMu.Lock()
				done = make(chan struct{})
				localDone := done
				startMu.Unlock()
				go func() {
					defer close(localDone)
					var workers sync.WaitGroup
					workers.Add(2)
					go func() { defer workers.Done(); app.RunNotificationWorker(workerCtx) }()
					go func() { defer workers.Done(); app.RunUsageWorker(workerCtx) }()
					workers.Wait()
				}()
			})
		}
		closeRuntime := func() {
			cancelWorker()
			startMu.Lock()
			localDone := done
			startMu.Unlock()
			if localDone != nil {
				<-localDone
			}
			_ = store.Close()
		}
		return bootstrap.Runtime{Handler: app.Handler(), Start: start, Stop: cancelWorker, Close: closeRuntime}, nil
	}
	var legacy *bootstrap.Runtime
	if _, err := os.Stat(filepath.Join(configDir, "runtime.json")); errors.Is(err, os.ErrNotExist) && !fileExists(filepath.Join(configDir, "encryption.key")) && !fileExists(filepath.Join(configDir, "identities")) && os.Getenv("GATEWAY_ENCRYPTION_KEY") != "" {
		key, keyErr := base64.StdEncoding.DecodeString(os.Getenv("GATEWAY_ENCRYPTION_KEY"))
		if keyErr == nil && len(key) == 32 {
			cfg := bootstrap.RuntimeConfig{AdminUsername: env("GATEWAY_ADMIN_USERNAME", "admin"), ClientCertFile: os.Getenv("GATEWAY_CLIENT_TLS_CERT_FILE"), ClientKeyFile: os.Getenv("GATEWAY_CLIENT_TLS_KEY_FILE"), HospitalCAFile: os.Getenv("GATEWAY_HOSPITAL_SERVER_CA_FILE"), TrustedProxyCIDRs: entries("GATEWAY_TRUSTED_PROXY_CIDRS"), ApprovedUpstreams: entries("GATEWAY_APPROVED_UPSTREAMS"), PrivateUpstreamCIDRs: entries("GATEWAY_PRIVATE_UPSTREAM_CIDRS")}
			if runtime, err := build(cfg, key); err == nil {
				legacy = &runtime
			} else {
				log.Print("legacy runtime could not be opened; local recovery is available and existing data was preserved")
			}
		} else {
			log.Print("legacy encryption key is invalid; local recovery is available and no replacement key was generated")
		}
	}
	manager, err = bootstrap.New(bootstrap.Options{Directory: configDir, DatabasePath: databasePath, PublicSetupURL: os.Getenv("GATEWAY_SETUP_PUBLIC_URL"), WebPolicy: webPolicy, TrustedProxyCIDRs: entries("GATEWAY_TRUSTED_PROXY_CIDRS"), LocalSetupCIDRs: entries("GATEWAY_SETUP_LOCAL_PROXY_CIDRS"), Build: build, ValidatePolicy: gateway.ValidateUpstreamPolicy, Assets: admin.LegacyHandler(), Legacy: legacy})
	if err != nil {
		if legacy != nil && legacy.Close != nil {
			legacy.Close()
		}
		log.Fatal(err)
	}
	defer manager.Close()
	server := &http.Server{Addr: address, Handler: manager.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("gateway listening on %s (unconfigured systems expose local setup only)", address)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print("Gateway listener failed: ", err)
		return
	}
}
