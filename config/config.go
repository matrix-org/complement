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
	"regexp"
	"strconv"
	"strings"
	"time"
)

type HostMount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// The config for running Complement. This is configured using environment variables. The comments
// in this struct are structured so they can be automatically parsed via gendoc. See /cmd/gendoc.
type Complement struct {
	// Name: COMPLEMENT_BASE_IMAGE
	// Description: **Required.** The name of the Docker image to use as a base homeserver when generating
	// blueprints. This image must conform to Complement's rules on containers, such as listening on the
	// correct ports.
	BaseImageURI string
	// Name: COMPLEMENT_DEBUG
	// Default: 0
	// Description: If 1, prints out more verbose logging such as HTTP request/response bodies.
	DebugLoggingEnabled bool
	// Name: COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS
	// Default: 0
	// Description: If 1, always prints the Homeserver container logs even on success. When used with
	// COMPLEMENT_ENABLE_DIRTY_RUNS, server logs are only printed once for reused deployments, at the very
	// end of the test suite.
	AlwaysPrintServerLogs bool
	// Name: COMPLEMENT_SHARE_ENV_PREFIX
	// Description: If set, all environment variables on the host with this prefix will be shared with
	// every homeserver, with the prefix removed. For example, if the prefix was `FOO_` then setting
	// `FOO_BAR=baz` on the host would translate to `BAR=baz` on the container. Useful for passing through
	// extra Homeserver configuration options without sharing all host environment variables.
	EnvVarsPropagatePrefix string
	// Name: COMPLEMENT_SPAWN_HS_TIMEOUT_SECS
	// Default: 30
	// Description: The number of seconds to wait for a Homeserver container to be responsive after
	// starting the container. Responsiveness is detected by `HEALTHCHECK` being healthy *and*
	// the `/versions` endpoint returning 200 OK.
	SpawnHSTimeout time.Duration
	// Name: COMPLEMENT_KEEP_BLUEPRINTS
	// Description: A list of space separated blueprint names to not clean up after running. For example,
	// `one_to_one_room alice` would not delete the homeserver images for the blueprints `alice` and
	// `one_to_one_room`. This can speed up homeserver runs if you frequently run the same base image
	// over and over again. If the base image changes, this should not be set as it means an older version
	// of the base image will be used for the named blueprints.
	KeepBlueprints []string
	// Name: COMPLEMENT_HOST_MOUNTS
	// Description: A list of semicolon separated host mounts to mount on every container. The structure
	// of the mount is `host-path:container-path:[ro]` for example `/path/on/host:/path/on/container` - you
	// can optionally specify `:ro` to mount the path as readonly. A complete example with multiple mounts
	// would look like `/host/a:/container/a:ro;/host/b:/container/b;/host/c:/container/c`
	HostMounts []HostMount
	// Name: COMPLEMENT_BASE_IMAGE_*
	// Description: This allows you to override the base image used for a particular named homeserver.
	// For example, `COMPLEMENT_BASE_IMAGE_HS1=complement-dendrite:latest` would use `complement-dendrite:latest`
	// for the `hs1` homeserver in blueprints, but not any other homeserver (e.g `hs2`). This matching
	// is case-insensitive. This allows Complement to test how different homeserver implementations work with each other.
	BaseImageURIs map[string]string

	// The namespace for all complement created blueprints and deployments
	PackageNamespace string
	// Certificate Authority generated values for this run of complement. Homeservers will use this
	// as a base to derive their own signed Federation certificates.
	CACertificate *x509.Certificate
	CAPrivateKey  *rsa.PrivateKey

	BestEffort bool

	// Name: COMPLEMENT_HOSTNAME_RUNNING_COMPLEMENT
	// Default: host.docker.internal
	// Description: The hostname of Complement from the perspective of a Homeserver running inside a container.
	// This can be useful for container runtimes using another hostname to access the host from a container,
	// like Podman that uses `host.containers.internal` instead.
	HostnameRunningComplement string

	// Name: COMPLEMENT_ENABLE_DIRTY_RUNS
	// Default: 0
	// Description: If 1, eligible tests will be provided with reusable deployments rather than a clean deployment.
	// Eligible tests are tests run with `Deploy(t, numHomeservers)`. If enabled, COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS
	// and COMPLEMENT_POST_TEST_SCRIPT are run exactly once, at the end of all tests in the package. The post test script
	// is run with the test name "COMPLEMENT_ENABLE_DIRTY_RUNS", and failed=false.
	//
	// Enabling dirty runs can greatly speed up tests, at the cost of clear server logs and the chance of tests
	// polluting each other. Tests using `OldDeploy` and blueprints will still have a fresh image for each test.
	// Fresh images can still be desirable e.g user directory tests need a clean homeserver else search results can
	// be polluted, tests which can blacklist a server over federation also need isolated deployments to stop failures
	// impacting other tests. For these reasons, there will always be a way for a test to override this setting and
	// get a dedicated deployment.
	//
	// Eventually, dirty runs will become the default running mode of Complement, with an environment variable to
	// disable this behaviour being added later, once this has stablised.
	EnableDirtyRuns bool

	// The hostname that will be used to bind the homeserver ports to, e.g. `127.0.0.1`
	HSPortBindingIP string

	// Name: COMPLEMENT_POST_TEST_SCRIPT
	// Default: ""
	// Description: An arbitrary script to execute after a test was executed and before the container is removed.
	// This can be used to extract, for example, server logs or database files. The script is passed the parameters:
	// ContainerID, TestName, TestFailed (true/false). When combined with COMPLEMENT_ENABLE_DIRTY_RUNS, the script is
	// called exactly once at the end of the test suite, and is called with the TestName of "COMPLEMENT_ENABLE_DIRTY_RUNS"
	// and TestFailed=false.
	PostTestScript string
}

var hsRegex = regexp.MustCompile(`COMPLEMENT_BASE_IMAGE_(.+)=(.+)$`)

func NewConfigFromEnvVars(pkgNamespace, baseImageURI string) *Complement {
	cfg := &Complement{BaseImageURIs: map[string]string{}}
	cfg.BaseImageURI = os.Getenv("COMPLEMENT_BASE_IMAGE")
	if cfg.BaseImageURI == "" {
		cfg.BaseImageURI = baseImageURI
	}
	cfg.DebugLoggingEnabled = os.Getenv("COMPLEMENT_DEBUG") == "1"
	cfg.AlwaysPrintServerLogs = os.Getenv("COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS") == "1"
	cfg.EnableDirtyRuns = os.Getenv("COMPLEMENT_ENABLE_DIRTY_RUNS") == "1"
	cfg.EnvVarsPropagatePrefix = os.Getenv("COMPLEMENT_SHARE_ENV_PREFIX")
	cfg.PostTestScript = os.Getenv("COMPLEMENT_POST_TEST_SCRIPT")
	cfg.SpawnHSTimeout = time.Duration(parseEnvWithDefault("COMPLEMENT_SPAWN_HS_TIMEOUT_SECS", 30)) * time.Second
	if os.Getenv("COMPLEMENT_VERSION_CHECK_ITERATIONS") != "" {
		fmt.Fprintln(os.Stderr, "Deprecated: COMPLEMENT_VERSION_CHECK_ITERATIONS will be removed in a later version. Use COMPLEMENT_SPAWN_HS_TIMEOUT_SECS instead which does the same thing and is clearer.")
		// each iteration had a 50ms sleep between tries so the timeout is 50 * iteration ms
		cfg.SpawnHSTimeout = time.Duration(50*parseEnvWithDefault("COMPLEMENT_VERSION_CHECK_ITERATIONS", 100)) * time.Millisecond
	}
	cfg.KeepBlueprints = strings.Split(os.Getenv("COMPLEMENT_KEEP_BLUEPRINTS"), " ")
	var err error
	hostMounts := os.Getenv("COMPLEMENT_HOST_MOUNTS")
	if hostMounts != "" {
		cfg.HostMounts, err = newHostMounts(strings.Split(hostMounts, ";"))
		if err != nil {
			panic("COMPLEMENT_HOST_MOUNTS parse error: " + err.Error())
		}
	}
	if cfg.BaseImageURI == "" {
		panic("COMPLEMENT_BASE_IMAGE must be set")
	}
	// Parse HS specific base images
	for _, env := range os.Environ() {
		// FindStringSubmatch returns the complete match as well as the capture groups.
		// In this case we expect there to be 3 matches.
		if matches := hsRegex.FindStringSubmatch(env); len(matches) == 3 {
			hs := matches[1]                   // first capture group; homeserver name
			cfg.BaseImageURIs[hs] = matches[2] // second capture group; homeserver image
		}
	}

	cfg.PackageNamespace = pkgNamespace

	// create CA certs and keys
	if err := cfg.GenerateCA(); err != nil {
		panic("Failed to generate CA certificate/key: " + err.Error())
	}
	if cfg.PackageNamespace == "" {
		panic("package namespace must be set")
	}

	HostnameRunningComplement := os.Getenv("COMPLEMENT_HOSTNAME_RUNNING_COMPLEMENT")
	if HostnameRunningComplement != "" {
		cfg.HostnameRunningComplement = HostnameRunningComplement
	} else {
		cfg.HostnameRunningComplement = "host.docker.internal"
	}

	// HSPortBindingIP is fixed here, but used by homerunner to override.
	cfg.HSPortBindingIP = "127.0.0.1"
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

func newHostMounts(mounts []string) ([]HostMount, error) {
	var hostMounts []HostMount
	for _, m := range mounts {
		segments := strings.Split(m, ":")
		if len(segments) < 2 {
			return nil, fmt.Errorf("mount '%s' malformed", m)
		}
		var ro string
		if len(segments) == 3 {
			ro = segments[2]
		}
		hostMounts = append(hostMounts, HostMount{
			HostPath:      segments[0],
			ContainerPath: segments[1],
			ReadOnly:      ro == "ro" || ro == "readonly",
		})
	}
	return hostMounts, nil
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
			CommonName:    "Complement Test CA",
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
