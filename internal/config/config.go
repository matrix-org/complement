package config

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"
)

type Complement struct {
	BaseImageURI          string
	BaseImageArgs         []string
	DebugLoggingEnabled   bool
	AlwaysPrintServerLogs bool
	BestEffort            bool
	SpawnHSTimeout        time.Duration
	KeepBlueprints        []string
	// The namespace for all complement created blueprints and deployments
	PackageNamespace string
	// Certificate Authority generated values for this run of complement. Homeservers will use this
	// as a base to derive their own signed Federation certificates.
	CACertificate *x509.Certificate
	CAPrivateKey  *rsa.PrivateKey
}

func NewConfigFromEnvVars() *Complement {
	cfg := &Complement{}
	cfg.BaseImageURI = os.Getenv("COMPLEMENT_BASE_IMAGE")
	cfg.BaseImageArgs = strings.Split(os.Getenv("COMPLEMENT_BASE_IMAGE_ARGS"), " ")
	cfg.DebugLoggingEnabled = os.Getenv("COMPLEMENT_DEBUG") == "1"
	cfg.AlwaysPrintServerLogs = os.Getenv("COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS") == "1"
	cfg.SpawnHSTimeout = time.Duration(parseEnvWithDefault("COMPLEMENT_SPAWN_HS_TIMEOUT_SECS", 30)) * time.Second
	if os.Getenv("COMPLEMENT_VERSION_CHECK_ITERATIONS") != "" {
		fmt.Fprintln(os.Stderr, "Deprecated: COMPLEMENT_VERSION_CHECK_ITERATIONS will be removed in a later version. Use COMPLEMENT_SPAWN_HS_TIMEOUT_SECS instead which does the same thing and is clearer.")
		// each iteration had a 50ms sleep between tries so the timeout is 50 * iteration ms
		cfg.SpawnHSTimeout = time.Duration(50*parseEnvWithDefault("COMPLEMENT_VERSION_CHECK_ITERATIONS", 100)) * time.Millisecond
	}
	cfg.KeepBlueprints = strings.Split(os.Getenv("COMPLEMENT_KEEP_BLUEPRINTS"), " ")
	if cfg.BaseImageURI == "" {
		panic("COMPLEMENT_BASE_IMAGE must be set")
	}
	cfg.PackageNamespace = "pkg"

	// create CA certs and keys
	if err := cfg.GenerateCA(); err != nil {
		panic("Failed to generate CA certificate/key: " + err.Error())
	}

	return cfg
}

func (c *Complement) GenerateCA() error {
	cert, key, err := generateCAValues()
	if err != nil {
		return err
	}
	c.CACertificate = cert
	c.CAPrivateKey = key
	return nil
}

func (c *Complement) CACertificateBytes() ([]byte, error) {
	cert := bytes.NewBuffer(nil)
	err := pem.Encode(cert, &pem.Block{Type: "CERTIFICATE", Bytes: c.CACertificate.Raw})
	return cert.Bytes(), err
}

func (c *Complement) CAPrivateKeyBytes() ([]byte, error) {
	caKey := bytes.NewBuffer(nil)
	err := pem.Encode(caKey, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(c.CAPrivateKey),
	})
	return caKey.Bytes(), err
}

func parseEnvWithDefault(key string, def int) int {
	s := os.Getenv(key)
	if s != "" {
		i, err := strconv.Atoi(s)
		if err != nil {
			// Don't bother trying to report it
			return def
		}
		return i
	}
	return def
}

// Generate a certificate and private key
func generateCAValues() (*x509.Certificate, *rsa.PrivateKey, error) {
	// valid for 10 years
	certificateDuration := time.Hour * 24 * 365 * 10
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	notBefore := time.Now()
	notAfter := notBefore.Add(certificateDuration)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, err
	}
	caCert := x509.Certificate{
		SerialNumber:          serialNumber,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		Subject: pkix.Name{
			Organization:  []string{"matrix.org"},
			Country:       []string{"GB"},
			Province:      []string{"London"},
			Locality:      []string{"London"},
			StreetAddress: []string{"123 Street"},
			PostalCode:    []string{"12345"},
		},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &caCert, &caCert, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	selfSignedCert, err := x509.ParseCertificates(derBytes)
	if err != nil {
		return nil, nil, err
	}

	return selfSignedCert[0], priv, nil
}
