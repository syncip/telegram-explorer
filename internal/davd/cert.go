package davd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	certSetting = "webdav_cert"
	// Apple refuses TLS certificates that are valid for longer than 825 days.
	certLifetime = 800 * 24 * time.Hour
	certRenew    = 30 * 24 * time.Hour
)

type storedCert struct {
	Cert  []byte   `json:"cert"`
	Key   []byte   `json:"key"`
	Hosts []string `json:"hosts"`
}

// hosts are the names the certificate is made out to, from WEBDAV_HOSTS.
func (s *Server) hosts() []string {
	out := []string{"localhost"}
	for _, h := range strings.Split(s.Hosts, ",") {
		if h = strings.TrimSpace(h); h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// certificate returns the server certificate: the one from WEBDAV_CERT and
// WEBDAV_KEY if given, otherwise a self-signed one that is kept sealed in the
// database and renewed before it runs out or when the names change.
func (s *Server) certificate() (tls.Certificate, []byte, error) {
	if s.CertFile != "" || s.KeyFile != "" {
		pair, err := tls.LoadX509KeyPair(s.CertFile, s.KeyFile)
		if err != nil {
			return tls.Certificate{}, nil, fmt.Errorf("WEBDAV_CERT/WEBDAV_KEY: %w", err)
		}
		raw, err := os.ReadFile(s.CertFile)
		return pair, raw, err
	}
	v := s.Engine.Vault()
	if v == nil {
		return tls.Certificate{}, nil, errors.New("gesperrt")
	}
	hosts := s.hosts()
	if sealed, err := s.Engine.Store.Setting(certSetting); err == nil && sealed != nil {
		if raw, err := v.Open(certSetting, sealed); err == nil {
			var sc storedCert
			if json.Unmarshal(raw, &sc) == nil && slices.Equal(sc.Hosts, hosts) {
				if pair, err := tls.X509KeyPair(sc.Cert, sc.Key); err == nil && time.Until(pair.Leaf.NotAfter) > certRenew {
					return pair, sc.Cert, nil
				}
			}
		}
	}
	sc, err := newCert(hosts)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	sealed, err := v.Seal(certSetting, raw)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := s.Engine.Store.SetSetting(certSetting, sealed); err != nil {
		return tls.Certificate{}, nil, err
	}
	pair, err := tls.X509KeyPair(sc.Cert, sc.Key)
	return pair, sc.Cert, err
}

func newCert(hosts []string) (storedCert, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return storedCert{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return storedCert{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Telegram Explorer WebDAV", Organization: []string{"Telegram Explorer"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(certLifetime),
		// It signs itself, so it may be imported as a trusted root.
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return storedCert{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return storedCert{}, err
	}
	return storedCert{
		Cert:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Key:   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		Hosts: hosts,
	}, nil
}

// CertInfo describes the certificate for the settings page.
type CertInfo struct {
	PEM         []byte   `json:"-"`
	Fingerprint string   `json:"fingerprint"`
	Hosts       []string `json:"hosts"`
	NotAfter    int64    `json:"notAfter"`
	Own         bool     `json:"own"`
}

// Cert returns the certificate clients have to trust.
func (s *Server) Cert() (CertInfo, error) {
	pair, raw, err := s.certificate()
	if err != nil {
		return CertInfo{}, err
	}
	sum := sha256.Sum256(pair.Leaf.Raw)
	hex := strings.ToUpper(fmt.Sprintf("% x", sum[:]))
	info := CertInfo{PEM: raw, Fingerprint: strings.ReplaceAll(hex, " ", ":"), NotAfter: pair.Leaf.NotAfter.Unix(), Own: s.CertFile != ""}
	info.Hosts = append(info.Hosts, pair.Leaf.DNSNames...)
	for _, ip := range pair.Leaf.IPAddresses {
		info.Hosts = append(info.Hosts, ip.String())
	}
	return info, nil
}
