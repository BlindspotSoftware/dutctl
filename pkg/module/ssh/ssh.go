// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ssh provides a dutagent module that connects to the DUT via Secure Shell
// and executes commands that are passed to the module from the dutctl client.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"golang.org/x/crypto/ssh"
)

func init() {
	module.Register(module.Record{
		ID:  "ssh",
		New: func() module.Module { return &SSH{} },
	})
}

// SSH is a module that executes commands on a remote host. It is non-interactive and closes the connection after
// the passed command is executed.
type SSH struct {
	Host       string // Host is the hostname or IP address of the DUT.
	Port       int    // Port is the port number of the SSH server on the DUT. Default is 22.
	User       string // User is the username to use for the SSH connection. Default is "root".
	Password   string // Password is the password to use for the SSH connection. Default is "".
	PrivateKey string // PrivateKey is the path to the dutagent's private key file.
	HostKey    string // HostKey is the server host key to use for the SSH connection.

	addr   string            // addr is the address of the DUT in the form of "host:port".
	config *ssh.ClientConfig // config is the SSH client configuration.
}

// Ensure implementing the Module interface.
var _ module.Module = &SSH{}

const abstract = `Establish a Secure Shell (SSH) connection to the DUT and execute a command.
`
const usage = `
ARGUMENTS:
	[command-string]

`
const description = `
The connection is closed after the passed command is executed.
The command-string is passed to the shell as a single argument. The command-string must not contain any newlines.
Quote the command-string if it contains spaces or special characters. E.g.: "ls -l /tmp"
The ssh module is non-interactive yet.
`

func (s *SSH) Help() string {
	log.Println("ssh module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(fmt.Sprintf("Host: %s, Port: %d\n", s.Host, s.Port))
	help.WriteString(fmt.Sprintf("User: %s\n", s.User))
	help.WriteString(description)

	return help.String()
}

func (s *SSH) Init() error {
	log.Println("ssh module: Init called")

	err := s.evalConfiguration()
	if err != nil {
		return err
	}

	var (
		authMethods     []ssh.AuthMethod
		hostKeyCallback ssh.HostKeyCallback
	)

	if s.PrivateKey != "" {
		privKey, err := os.ReadFile(s.PrivateKey)
		if err != nil {
			return fmt.Errorf("failed to read private key: %w", err)
		}

		signer, err := ssh.ParsePrivateKey(privKey)
		if err != nil {
			return fmt.Errorf("failed to parse private key: %w", err)
		}

		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	authMethods = append(authMethods, ssh.Password(s.Password))

	if s.HostKey != "" {
		hostKey, err := ssh.ParsePublicKey([]byte(s.HostKey))
		if err != nil {
			return fmt.Errorf("failed to parse host key: %w", err)
		}

		hostKeyCallback = ssh.FixedHostKey(hostKey)
	} else {
		//nolint: gosec // ignore the InsecureIgnoreHostKey warning, it is only used as a fallback
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	s.config = &ssh.ClientConfig{
		User:            s.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}

	// test the connection
	client, err := ssh.Dial("tcp", s.addr, s.config)
	if err != nil {
		return fmt.Errorf("SSH connection to DUT failed: %w", err)
	}
	defer client.Close()

	return nil
}

func (s *SSH) Deinit() error {
	log.Println("ssh module: Deinit called")

	return nil
}

func (s *SSH) Run(_ context.Context, sesh module.Session, args ...string) error {
	log.Println("ssh module: Run called")

	if len(args) == 0 {
		return fmt.Errorf("missing command-string")
	}

	if len(args) > 1 {
		return fmt.Errorf("too many arguments - if the command-string contains spaces or special characters, quote it")
	}

	client, err := ssh.Dial("tcp", s.addr, s.config)
	if err != nil {
		return fmt.Errorf("failed to dial SSH server: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	// Execute the command and capture output
	output, err := session.CombinedOutput(args[0])
	if err != nil {
		// Still show any output that might have been generated before the error
		if len(output) > 0 {
			sesh.Print(string(output))
		}

		return fmt.Errorf("failed to execute command: %w", err)
	}

	// Send the output back to the client
	sesh.Print(string(output))

	return nil
}

// evalConfiguration evaluates the configuration of the SSH module and sets default values if necessary.
func (s *SSH) evalConfiguration() error {
	if s.Host == "" {
		return errors.New("host is required")
	}

	if s.Port == 0 {
		s.Port = 22
	}

	if s.User == "" {
		s.User = "root"
	}

	s.addr = fmt.Sprintf("%s:%d", s.Host, s.Port)

	if s.Password == "" && s.PrivateKey == "" {
		return errors.New("unable to authenticate, either password or private key must be configured")
	}

	return nil
}
