// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tlsutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// File and directory permissions.
	certFileMode = 0644 // Public read, owner write.
	keyFileMode  = 0600 // Owner read/write only
	dirMode      = 0755 // Standard directory permissions.

	// Certificate serial number bit size.
	serialNumberBits = 128
)

// GenerateSelfSignedCert creates a new self-signed TLS certificate and private key.
// The certificate is valid for 10 years and includes localhost and system hostname in SANs.
// Uses Ed25519 for better performance and security compared to RSA.
func GenerateSelfSignedCert(certPath, keyPath string) error {
	// Generate Ed25519 private key (much faster than RSA)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate keys: %w", err)
	}

	// Create self-signed certificate
	derBytes, err := createSelfSignedCertificate(publicKey, privateKey)
	if err != nil {
		return err
	}

	err = writeCertificate(certPath, derBytes)
	if err != nil {
		return err
	}

	err = writePrivateKey(keyPath, privateKey)
	if err != nil {
		return err
	}

	log.Printf("Generated self-signed TLS certificate: %s", certPath)
	log.Printf("Generated private key: %s", keyPath)

	return nil
}

func createSelfSignedCertificate(publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) ([]byte, error) {
	// Generate a random serial number
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), serialNumberBits)

	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Get system hostname for SANs
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost" // Fallback if hostname detection fails
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Blindspot Software"},
			CommonName:   "dutagent",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", hostname},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	return derBytes, nil
}

func writeCertificate(certPath string, derBytes []byte) error {
	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, certFileMode)
	if err != nil {
		return fmt.Errorf("failed to create certificate file: %w", err)
	}
	defer certOut.Close()

	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	return nil
}

func writePrivateKey(keyPath string, privateKey ed25519.PrivateKey) error {
	// Marshal Ed25519 private key in PKCS8 format
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, keyFileMode)
	if err != nil {
		return fmt.Errorf("failed to create key file: %w", err)
	}
	defer keyOut.Close()

	err = pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	if err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// LoadOrGenerateCert attempts to load an existing TLS certificate/key pair.
// If the files don't exist, it generates a new self-signed certificate.
// If the files exist but cannot be loaded, it returns an error without overwriting them.
func LoadOrGenerateCert(certPath, keyPath string) (tls.Certificate, error) {
	// Check if certificate and key files exist
	certExists := fileExists(certPath)
	keyExists := fileExists(keyPath)

	// If either file exists, we must load them (don't auto-generate)
	if certExists || keyExists {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("certificate/key files exist but failed to load (cert exists: %v, key exists: %v): %w",
				certExists, keyExists, err)
		}

		log.Printf("Loaded existing TLS certificate from: %s", certPath)

		return cert, nil
	}

	// Neither file exists, generate new certificate
	log.Printf("TLS certificate not found, generating new self-signed certificate...")

	// Derive directory from cert path
	certDir := filepath.Dir(certPath)
	keyDir := filepath.Dir(keyPath)

	// Ensure directories exist
	err := os.MkdirAll(certDir, dirMode)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to create certificate directory: %w", err)
	}

	if certDir != keyDir {
		err := os.MkdirAll(keyDir, dirMode)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("failed to create key directory: %w", err)
		}
	}

	// Generate certificate
	err = GenerateSelfSignedCert(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Load the newly generated certificate
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to load generated certificate: %w", err)
	}

	return cert, nil
}

// fileExists checks if a file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return !info.IsDir()
}
