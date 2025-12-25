package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Forwarder manages a single SSH port forwarding connection
type Forwarder struct {
	rule   Rule
	client *ssh.Client
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Start establishes the SSH connection and starts port forwarding
func (f *Forwarder) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel

	f.wg.Add(1)
	go f.run(ctx)

	return nil
}

// Stop gracefully stops the forwarder
func (f *Forwarder) Stop() {
	if f.cancel != nil {
		f.cancel()
	}
	if f.client != nil {
		f.client.Close()
	}
}

// run manages the SSH connection with auto-reconnect
func (f *Forwarder) run(ctx context.Context) {
	defer f.wg.Done()

	reconnectDelay := 15 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := f.connectAndForward(ctx); err != nil {
			log.Printf("Connection to %s failed: %v", f.rule.Server, err)
		}

		// Wait before reconnecting or check for cancellation
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
			log.Printf("Reconnecting to %s...", f.rule.Server)
		}
	}
}

// connectAndForward establishes SSH connection and sets up port forwarding
func (f *Forwarder) connectAndForward(ctx context.Context) error {
	// Parse server address (user@host:port)
	user, host, port := parseServerAddress(f.rule.Server)

	// SSH client config
	sshConfig := &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	// Require identity_file for authentication
	if f.rule.IdentityFile == "" {
		return fmt.Errorf("identity_file is required for authentication")
	}

	auth, err := getPublicKeyAuth(f.rule.IdentityFile)
	if err != nil {
		return fmt.Errorf("failed to setup key authentication: %w", err)
	}
	sshConfig.Auth = []ssh.AuthMethod{auth}

	// Establish SSH connection
	address := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to dial SSH: %w", err)
	}
	f.client = client

	log.Printf("Connected to %s", f.rule.Server)

	// Listen on remote server
	listener, err := client.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", f.rule.RemotePort))
	if err != nil {
		client.Close()
		return fmt.Errorf("failed to listen on remote port: %w", err)
	}

	log.Printf("Remote port forwarding established: 127.0.0.1:%d -> 127.0.0.1:%d",
		f.rule.RemotePort, f.rule.LocalPort)

	// Accept connections
	defer listener.Close()

	acceptChan := make(chan error, 1)

	go func() {
		for {
			remoteConn, err := listener.Accept()
			if err != nil {
				acceptChan <- err
				return
			}

			f.wg.Add(1)
			go f.handleConnection(ctx, remoteConn)
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("Stopping forwarder for %s", f.rule.Server)
		return nil
	case err := <-acceptChan:
		return fmt.Errorf("accept error: %w", err)
	}
}

// handleConnection forwards data between remote and local connections
func (f *Forwarder) handleConnection(ctx context.Context, remoteConn net.Conn) {
	defer f.wg.Done()
	defer remoteConn.Close()

	// Connect to local service
	localConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", f.rule.LocalPort))
	if err != nil {
		log.Printf("Failed to connect to local port %d: %v", f.rule.LocalPort, err)
		return
	}
	defer localConn.Close()

	// Start bidirectional forwarding
	errChan := make(chan error, 2)

	// Remote -> Local
	go func() {
		_, err := copyData(ctx, localConn, remoteConn)
		errChan <- err
	}()

	// Local -> Remote
	go func() {
		_, err := copyData(ctx, remoteConn, localConn)
		errChan <- err
	}()

	// Wait for either direction to finish
	select {
	case <-ctx.Done():
	case <-errChan:
	}
}

// copyData copies data between connections with context awareness
func copyData(ctx context.Context, dst, src net.Conn) (int64, error) {
	var written int64
	buf := make([]byte, 32*1024)

	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}

		nr, err := src.Read(buf)
		if nr > 0 {
			nw, err := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if err != nil {
				return written, err
			}
			if nr != nw {
				return written, fmt.Errorf("short write")
			}
		}
		if err != nil {
			if err.Error() != "EOF" {
				return written, err
			}
			break
		}
	}
	return written, nil
}

// parseServerAddress parses user@host:port format
func parseServerAddress(addr string) (user, host string, port int) {
	parts := strings.Split(addr, "@")
	if len(parts) == 2 {
		user = parts[0]
		addr = parts[1]
	} else {
		user = os.Getenv("USER")
	}

	hostParts := strings.Split(addr, ":")
	if len(hostParts) == 2 {
		host = hostParts[0]
		fmt.Sscanf(hostParts[1], "%d", &port)
	} else {
		host = addr
		port = 22
	}

	if user == "" {
		user = "root"
	}

	return
}

// getPublicKeyAuth reads a private key file and returns an SSH auth method
func getPublicKeyAuth(keyPath string) (ssh.AuthMethod, error) {
	expandedPath := expandHomeDir(keyPath)
	key, err := os.ReadFile(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file %s: %w", expandedPath, err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}
	return ssh.PublicKeys(signer), nil
}
