// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tlsutil provides the TLS key pair that dutagent and dutserver serve
// with: it loads an existing certificate/key pair from disk, or generates a
// self-signed one on first start.
//
// A generated certificate chains to no CA, and the peers in this project skip
// verification anyway (see internal/rpc newTLSClient), so the pair buys
// encryption without authentication: it stops passive eavesdropping on the
// wire, not an active man-in-the-middle, and it does not authenticate clients
// either. Supplying a CA-issued pair via -tls-cert/-tls-key does not change
// that today, because no client in this project verifies what it is offered.
package tlsutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// File and directory permissions.
	certFileMode = 0o644 // Owner read/write, world-readable.
	keyFileMode  = 0o600 // Owner read/write only.
	dirMode      = 0o750 // Owner access, group traverse; keeps the key out of world reach.

	// Certificate serial number bit size.
	serialNumberBits = 128
)

// certValidity is how long a generated certificate stays valid. Nothing in this
// project enforces it — every peer sets InsecureSkipVerify, so NotAfter is never
// checked, and there is no rotation path. It is deliberately long so that the
// day verification is turned on, existing deployments do not all expire at once.
const certValidity = 10 * 365 * 24 * time.Hour

// LoadOrGenerateCert returns the TLS key pair at certPath/keyPath, generating a
// self-signed one on first start.
//
// It generates only when neither file exists, creating the parent directories as
// needed. If either file is present it loads the pair and returns an error rather
// than overwriting, so a half-provisioned or unreadable pair fails startup instead
// of silently replacing an operator's certificate. An existing pair is loaded
// as-is: expiry is not checked and nothing is ever rotated.
//
// It reports whether it generated the pair, so the caller can log that once —
// this package does not log itself.
func LoadOrGenerateCert(certPath, keyPath string) (tls.Certificate, bool, error) {
	certExists := fileExists(certPath)
	keyExists := fileExists(keyPath)

	// If either file exists, load rather than generate: overwriting half a key
	// pair is worse than failing loudly.
	if certExists || keyExists {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, false, fmt.Errorf(
				"loading key pair (cert exists: %v, key exists: %v): %w", certExists, keyExists, err)
		}

		return cert, false, nil
	}

	certDir := filepath.Dir(certPath)
	keyDir := filepath.Dir(keyPath)

	err := os.MkdirAll(certDir, dirMode)
	if err != nil {
		return tls.Certificate{}, false, fmt.Errorf("creating certificate directory: %w", err)
	}

	if certDir != keyDir {
		err := os.MkdirAll(keyDir, dirMode)
		if err != nil {
			return tls.Certificate{}, false, fmt.Errorf("creating key directory: %w", err)
		}
	}

	err = generateSelfSignedCert(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, false, err
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, false, fmt.Errorf("loading generated key pair: %w", err)
	}

	return cert, true, nil
}

// generateSelfSignedCert writes a new self-signed certificate to certPath and its
// Ed25519 private key to keyPath.
//
// Both files are written to temporary paths alongside their targets and renamed
// into place only once both have been written. A half-written pair would be worse
// than none: LoadOrGenerateCert refuses to generate when either file exists, so a
// stray cert.pem with no key would make every subsequent start fail until an
// operator deleted it by hand.
//
// Ed25519 is used because it keeps key generation cheap on the small boards
// dutagent runs on. The certificate carries localhost, the system hostname,
// 127.0.0.1 and ::1 as SANs; those are advisory only, since no peer in this
// project verifies the certificate at all.
func generateSelfSignedCert(certPath, keyPath string) error {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generating keys: %w", err)
	}

	derBytes, err := createSelfSignedCertificate(publicKey, privateKey)
	if err != nil {
		return err
	}

	certTmp := certPath + ".tmp"
	keyTmp := keyPath + ".tmp"

	err = writeCertificate(certTmp, derBytes)
	if err != nil {
		os.Remove(certTmp)

		return err
	}

	err = writePrivateKey(keyTmp, privateKey)
	if err != nil {
		os.Remove(certTmp)
		os.Remove(keyTmp)

		return err
	}

	// Rename the key first: if the second rename fails, the leftover is a key
	// without a certificate, which LoadOrGenerateCert reports as a load failure
	// naming both files — clearer than a certificate whose key vanished.
	err = os.Rename(keyTmp, keyPath)
	if err != nil {
		os.Remove(certTmp)
		os.Remove(keyTmp)

		return fmt.Errorf("installing private key: %w", err)
	}

	err = os.Rename(certTmp, certPath)
	if err != nil {
		os.Remove(certTmp)

		return fmt.Errorf("installing certificate: %w", err)
	}

	return nil
}

func createSelfSignedCertificate(publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) ([]byte, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), serialNumberBits)

	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("generating serial number: %w", err)
	}

	// A hostname SAN is a convenience for the day verification is turned on, not
	// a requirement today, so a host that cannot name itself is not an error —
	// localhost alone is a valid SAN list.
	dnsNames := []string{"localhost"}

	hostname, err := os.Hostname()
	if err == nil && hostname != "localhost" {
		dnsNames = append(dnsNames, hostname)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Blindspot Software"},
			CommonName:   "dutagent",
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(certValidity),
		// Ed25519 signs; it cannot do key encipherment, so KeyUsageDigitalSignature
		// is the whole of what this leaf needs for a TLS 1.3 handshake.
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// Marks the (absent) IsCA and path-length fields as meaningful, so the
		// leaf explicitly says "not a CA" rather than leaving it unstated.
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating certificate: %w", err)
	}

	return derBytes, nil
}

// writeCertificate writes the PEM-encoded certificate to path. Close is explicit
// rather than deferred: a failure to flush leaves an unusable file, and the
// caller must learn about it to clean up.
func writeCertificate(path string, derBytes []byte) error {
	certOut, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, certFileMode)
	if err != nil {
		return fmt.Errorf("creating certificate file: %w", err)
	}

	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err != nil {
		certOut.Close()

		return fmt.Errorf("writing certificate: %w", err)
	}

	err = certOut.Close()
	if err != nil {
		return fmt.Errorf("closing certificate file: %w", err)
	}

	return nil
}

// writePrivateKey writes the PKCS#8 PEM-encoded private key to path, with
// keyFileMode so it is never group- or world-readable. Close is explicit for the
// same reason as in writeCertificate.
func writePrivateKey(path string, privateKey ed25519.PrivateKey) error {
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("marshaling private key: %w", err)
	}

	keyOut, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, keyFileMode)
	if err != nil {
		return fmt.Errorf("creating key file: %w", err)
	}

	err = pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	if err != nil {
		keyOut.Close()

		return fmt.Errorf("writing private key: %w", err)
	}

	err = keyOut.Close()
	if err != nil {
		return fmt.Errorf("closing key file: %w", err)
	}

	return nil
}

// fileExists checks if a file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return !info.IsDir()
}
