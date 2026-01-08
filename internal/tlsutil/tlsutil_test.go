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

func TestGenerateSelfSignedCert(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T) (certPath, keyPath string)
		wantErr   bool
	}{
		{
			name: "generates valid certificate",
			setupFunc: func(t *testing.T) (string, string) {
				tmpDir := t.TempDir()
				return filepath.Join(tmpDir, "cert.pem"), filepath.Join(tmpDir, "key.pem")
			},
			wantErr: false,
		},
		{
			name: "fails with invalid path",
			setupFunc: func(t *testing.T) (string, string) {
				return "/nonexistent/directory/cert.pem", "/nonexistent/directory/key.pem"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certPath, keyPath := tt.setupFunc(t)

			err := GenerateSelfSignedCert(certPath, keyPath)

			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateSelfSignedCert() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify files exist and can be loaded
				if _, err := os.Stat(certPath); err != nil {
					t.Errorf("Certificate file not created: %v", err)
				}
				if _, err := os.Stat(keyPath); err != nil {
					t.Errorf("Key file not created: %v", err)
				}

				cert, err := tls.LoadX509KeyPair(certPath, keyPath)
				if err != nil {
					t.Fatalf("Failed to load certificate: %v", err)
				}

				x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
				if err != nil {
					t.Fatalf("Failed to parse certificate: %v", err)
				}

				if x509Cert.Subject.CommonName != "dutagent" {
					t.Errorf("CommonName = %q, want %q", x509Cert.Subject.CommonName, "dutagent")
				}
			}
		})
	}
}

func TestLoadOrGenerateCert(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T) (certPath, keyPath string)
		wantErr   bool
	}{
		{
			name: "generates when files don't exist",
			setupFunc: func(t *testing.T) (string, string) {
				tmpDir := t.TempDir()
				return filepath.Join(tmpDir, "cert.pem"), filepath.Join(tmpDir, "key.pem")
			},
			wantErr: false,
		},
		{
			name: "loads existing certificate",
			setupFunc: func(t *testing.T) (string, string) {
				tmpDir := t.TempDir()
				certPath := filepath.Join(tmpDir, "cert.pem")
				keyPath := filepath.Join(tmpDir, "key.pem")
				if err := GenerateSelfSignedCert(certPath, keyPath); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
				return certPath, keyPath
			},
			wantErr: false,
		},
		{
			name: "fails when only cert exists",
			setupFunc: func(t *testing.T) (string, string) {
				tmpDir := t.TempDir()
				certPath := filepath.Join(tmpDir, "cert.pem")
				keyPath := filepath.Join(tmpDir, "key.pem")
				if err := os.WriteFile(certPath, []byte("invalid"), 0644); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
				return certPath, keyPath
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certPath, keyPath := tt.setupFunc(t)

			cert, err := LoadOrGenerateCert(certPath, keyPath)

			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrGenerateCert() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && len(cert.Certificate) == 0 {
				t.Error("Certificate is empty")
			}
		})
	}
}
