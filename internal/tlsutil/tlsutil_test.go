// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateSelfSignedCert covers the generator itself: the pair it writes must
// load back as a usable key pair, the private key must not be readable by anyone
// but its owner, and a failed generation must leave nothing behind.
func TestGenerateSelfSignedCert(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T) (certPath, keyPath string)
		wantErr   bool
	}{
		{
			name: "generates valid certificate",
			setupFunc: func(t *testing.T) (string, string) {
				t.Helper()

				tmpDir := t.TempDir()

				return filepath.Join(tmpDir, "cert.pem"), filepath.Join(tmpDir, "key.pem")
			},
			wantErr: false,
		},
		{
			name: "fails with invalid path",
			setupFunc: func(t *testing.T) (string, string) {
				t.Helper()

				return "/nonexistent/directory/cert.pem", "/nonexistent/directory/key.pem"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certPath, keyPath := tt.setupFunc(t)

			err := generateSelfSignedCert(certPath, keyPath)

			if (err != nil) != tt.wantErr {
				t.Errorf("generateSelfSignedCert() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if tt.wantErr {
				// A failed generation must not leave either file behind: a stray
				// cert.pem would make every later start refuse to regenerate.
				if fileExists(certPath) || fileExists(keyPath) {
					t.Error("failed generation left files behind")
				}

				return
			}

			keyInfo, err := os.Stat(keyPath)
			if err != nil {
				t.Fatalf("stat key: %v", err)
			}

			if perm := keyInfo.Mode().Perm(); perm != keyFileMode {
				t.Errorf("key mode = %#o, want %#o - the private key must stay owner-only", perm, keyFileMode)
			}

			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				t.Fatalf("load key pair: %v", err)
			}

			x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				t.Fatalf("parse certificate: %v", err)
			}

			if x509Cert.Subject.CommonName != "dutagent" {
				t.Errorf("CommonName = %q, want %q", x509Cert.Subject.CommonName, "dutagent")
			}
		})
	}
}

// TestLoadOrGenerateCert covers the load-or-generate decision, including the
// refusal to overwrite a half-provisioned pair in either direction.
func TestLoadOrGenerateCert(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func(t *testing.T) (certPath, keyPath string)
		wantGenerated bool
		wantErr       bool
	}{
		{
			name: "generates when files don't exist",
			setupFunc: func(t *testing.T) (string, string) {
				t.Helper()

				tmpDir := t.TempDir()

				return filepath.Join(tmpDir, "cert.pem"), filepath.Join(tmpDir, "key.pem")
			},
			wantGenerated: true,
		},
		{
			name: "creates the target directory",
			setupFunc: func(t *testing.T) (string, string) {
				t.Helper()

				tmpDir := filepath.Join(t.TempDir(), "tls")

				return filepath.Join(tmpDir, "cert.pem"), filepath.Join(tmpDir, "key.pem")
			},
			wantGenerated: true,
		},
		{
			name: "loads existing certificate",
			setupFunc: func(t *testing.T) (string, string) {
				t.Helper()

				tmpDir := t.TempDir()
				certPath := filepath.Join(tmpDir, "cert.pem")
				keyPath := filepath.Join(tmpDir, "key.pem")

				if err := generateSelfSignedCert(certPath, keyPath); err != nil {
					t.Fatalf("setup: %v", err)
				}

				return certPath, keyPath
			},
			wantGenerated: false,
		},
		{
			name: "fails when only cert exists",
			setupFunc: func(t *testing.T) (string, string) {
				t.Helper()

				tmpDir := t.TempDir()
				certPath := filepath.Join(tmpDir, "cert.pem")
				keyPath := filepath.Join(tmpDir, "key.pem")

				if err := os.WriteFile(certPath, []byte("invalid"), certFileMode); err != nil {
					t.Fatalf("setup: %v", err)
				}

				return certPath, keyPath
			},
			wantErr: true,
		},
		{
			name: "fails when only key exists",
			setupFunc: func(t *testing.T) (string, string) {
				t.Helper()

				tmpDir := t.TempDir()
				certPath := filepath.Join(tmpDir, "cert.pem")
				keyPath := filepath.Join(tmpDir, "key.pem")

				if err := os.WriteFile(keyPath, []byte("invalid"), keyFileMode); err != nil {
					t.Fatalf("setup: %v", err)
				}

				return certPath, keyPath
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certPath, keyPath := tt.setupFunc(t)

			cert, generated, err := LoadOrGenerateCert(certPath, keyPath)

			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrGenerateCert() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if tt.wantErr {
				return
			}

			if generated != tt.wantGenerated {
				t.Errorf("generated = %v, want %v", generated, tt.wantGenerated)
			}

			if len(cert.Certificate) == 0 {
				t.Error("certificate is empty")
			}
		})
	}
}
