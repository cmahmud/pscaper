package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ValidationResult struct {
	OK      bool
	Latency time.Duration
	ExitIP  string
	Error   string
}

type Validator struct{ cfg Config }

func NewValidator(cfg Config) *Validator { return &Validator{cfg: cfg} }

func (v *Validator) Validate(ctx context.Context, p ProxyRecord) ValidationResult {
	start := time.Now()
	var ip string
	var err error
	switch p.Protocol {
	case "http":
		ip, err = v.httpProxy(ctx, p, false)
	case "https":
		ip, err = v.httpProxy(ctx, p, true)
	case "socks4":
		ip, err = v.socks4(ctx, p)
	case "socks5":
		ip, err = v.socks5(ctx, p)
	default:
		err = fmt.Errorf("unsupported protocol %q", p.Protocol)
	}
	if err != nil {
		return ValidationResult{Latency: time.Since(start), Error: err.Error()}
	}
	return ValidationResult{OK: true, Latency: time.Since(start), ExitIP: ip}
}

func (v *Validator) httpProxy(ctx context.Context, p ProxyRecord, httpsTarget bool) (string, error) {
	proxySchemes := []string{"http"}
	if p.Protocol == "https" {
		proxySchemes = append(proxySchemes, "https")
	}
	var last error
	for _, ps := range proxySchemes {
		proxyURL := &url.URL{Scheme: ps, Host: p.Address()}
		tr := &http.Transport{
			Proxy:                 http.ProxyURL(proxyURL),
			DialContext:           (&net.Dialer{Timeout: v.cfg.Timeout, KeepAlive: -1}).DialContext,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // probe uses an ephemeral self-signed cert
			TLSHandshakeTimeout:   v.cfg.Timeout,
			ResponseHeaderTimeout: v.cfg.Timeout,
			DisableKeepAlives:     true,
			ForceAttemptHTTP2:     false,
		}
		client := &http.Client{Transport: tr, Timeout: v.cfg.Timeout}
		scheme := "http"
		port := v.cfg.ProbeHTTPPort
		if httpsTarget {
			scheme = "https"
			port = v.cfg.ProbeHTTPSPort
		}
		target := fmt.Sprintf("%s://%s/probe/%s", scheme, net.JoinHostPort(v.cfg.ProbeHost, strconv.Itoa(port)), v.cfg.ProbeToken)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		req.Header.Set("Connection", "close")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if rerr != nil {
			last = rerr
			continue
		}
		if resp.StatusCode != 200 {
			last = fmt.Errorf("probe HTTP %d", resp.StatusCode)
			continue
		}
		if ip, err := parseProbeBody(body); err == nil {
			return ip, nil
		} else {
			last = err
		}
	}
	return "", last
}

func (v *Validator) socks5(ctx context.Context, p ProxyRecord) (string, error) {
	return v.dialAndProbe(ctx, func(network, addr string) (net.Conn, error) {
		return dialSOCKS5(ctx, p.Address(), addr, v.cfg.Timeout)
	})
}

func (v *Validator) socks4(ctx context.Context, p ProxyRecord) (string, error) {
	return v.dialAndProbe(ctx, func(network, addr string) (net.Conn, error) {
		return dialSOCKS4(ctx, p.Address(), addr, v.cfg.Timeout)
	})
}

func (v *Validator) dialAndProbe(ctx context.Context, dial func(string, string) (net.Conn, error)) (string, error) {
	target := net.JoinHostPort(v.cfg.ProbeHost, strconv.Itoa(v.cfg.ProbeHTTPPort))
	conn, err := dial("tcp", target)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(v.cfg.Timeout))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target+"/probe/"+v.cfg.ProbeToken, nil)
	req.Host = target
	req.Header.Set("Connection", "close")
	if err := req.Write(conn); err != nil {
		return "", err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("probe HTTP %d", resp.StatusCode)
	}
	return parseProbeBody(body)
}

func parseProbeBody(body []byte) (string, error) {
	var v struct {
		OK bool   `json:"ok"`
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("bad probe response: %w", err)
	}
	if !v.OK || net.ParseIP(v.IP) == nil {
		return "", fmt.Errorf("invalid probe identity")
	}
	return v.IP, nil
}

func dialSOCKS5(ctx context.Context, proxyAddr, targetAddr string, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, KeepAlive: -1}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (net.Conn, error) { conn.Close(); return nil, e }
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err = conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return fail(err)
	}
	greet := make([]byte, 2)
	if _, err = io.ReadFull(conn, greet); err != nil {
		return fail(err)
	}
	if greet[0] != 0x05 || greet[1] != 0x00 {
		return fail(fmt.Errorf("SOCKS5 auth method rejected"))
	}
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return fail(err)
	}
	port, _ := strconv.Atoi(portStr)
	req := []byte{0x05, 0x01, 0x00}
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		req = append(req, 0x01)
		req = append(req, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		req = append(req, 0x04)
		req = append(req, ip6...)
	} else {
		if len(host) > 255 {
			return fail(fmt.Errorf("target hostname too long"))
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err = conn.Write(req); err != nil {
		return fail(err)
	}
	head := make([]byte, 4)
	if _, err = io.ReadFull(conn, head); err != nil {
		return fail(err)
	}
	if head[0] != 0x05 || head[1] != 0x00 {
		return fail(fmt.Errorf("SOCKS5 connect rejected: 0x%02x", head[1]))
	}
	var n int
	switch head[3] {
	case 0x01:
		n = 4
	case 0x04:
		n = 16
	case 0x03:
		b := make([]byte, 1)
		if _, err = io.ReadFull(conn, b); err != nil {
			return fail(err)
		}
		n = int(b[0])
	default:
		return fail(fmt.Errorf("SOCKS5 bad address type"))
	}
	discard := make([]byte, n+2)
	if _, err = io.ReadFull(conn, discard); err != nil {
		return fail(err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func dialSOCKS4(ctx context.Context, proxyAddr, targetAddr string, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, KeepAlive: -1}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (net.Conn, error) { conn.Close(); return nil, e }
	_ = conn.SetDeadline(time.Now().Add(timeout))
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return fail(err)
	}
	port, _ := strconv.Atoi(portStr)
	buf := []byte{0x04, 0x01, byte(port >> 8), byte(port)}
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		buf = append(buf, ip4...)
		buf = append(buf, 0x00)
	} else {
		buf = append(buf, 0x00, 0x00, 0x00, 0x01, 0x00)
		buf = append(buf, []byte(host)...)
		buf = append(buf, 0x00)
	}
	if _, err := conn.Write(buf); err != nil {
		return fail(err)
	}
	resp := make([]byte, 8)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fail(err)
	}
	if resp[1] != 0x5a {
		return fail(fmt.Errorf("SOCKS4 rejected: 0x%02x", resp[1]))
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func StartProbeServers(ctx context.Context, cfg Config) error {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/probe/"+cfg.ProbeToken {
			http.NotFound(w, r)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ip": host, "ts": time.Now().UTC()})
	})
	plain := &http.Server{Addr: cfg.ProbeHTTPAddr, Handler: h, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 2 * time.Second}
	ln, err := net.Listen("tcp", cfg.ProbeHTTPAddr)
	if err != nil {
		return err
	}
	cert, err := ephemeralCert()
	if err != nil {
		ln.Close()
		return err
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	tlsLnRaw, err := net.Listen("tcp", cfg.ProbeHTTPSAddr)
	if err != nil {
		ln.Close()
		return err
	}
	tlsLn := tls.NewListener(tlsLnRaw, tlsCfg)
	secure := &http.Server{Addr: cfg.ProbeHTTPSAddr, Handler: h, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 2 * time.Second, TLSConfig: tlsCfg}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = plain.Shutdown(c)
		_ = secure.Shutdown(c)
	}()
	go func() { _ = plain.Serve(ln) }()
	go func() { _ = secure.Serve(tlsLn) }()
	return nil
}

func ephemeralCert() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "pscaper-probe"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(30 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return tls.X509KeyPair(certPEM, keyPEM)
}
