package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// copyBufferSize is the buffer size for data copying between connections
	copyBufferSize = 32 * 1024 // 32KB

	// reconnectDelayMin is the minimum delay before attempting to reconnect
	reconnectDelayMin = 5 * time.Second

	// reconnectDelayMax is the maximum delay before attempting to reconnect
	reconnectDelayMax = 60 * time.Second

	// sshTimeout is the timeout for establishing SSH connections
	sshTimeout = 30 * time.Second

	// localDialTimeout is the timeout for connecting to local services
	localDialTimeout = 10 * time.Second

	// readDeadlineInterval is the read deadline for context-aware copy operations
	readDeadlineInterval = 10 * time.Second
)

// Forwarder manages a single SSH port forwarding connection
type Forwarder struct {
	rule       Rule
	client     *ssh.Client
	clientLock sync.Mutex
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	activeConns sync.WaitGroup // Track active connections for graceful shutdown
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
	
	// Wait for active connections to complete with timeout
	done := make(chan struct{})
	go func() {
		f.activeConns.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		// All connections completed gracefully
	case <-time.After(5 * time.Second):
		// Timeout waiting for connections, force close
		log.Printf("Timeout waiting for connections to %s to complete, forcing shutdown", f.rule.Server)
	}
	
	f.clientLock.Lock()
	defer f.clientLock.Unlock()
	if f.client != nil {
		f.client.Close()
		f.client = nil
	}
}

// run manages the SSH connection with auto-reconnect and exponential backoff
func (f *Forwarder) run(ctx context.Context) {
	defer f.wg.Done()

	delay := reconnectDelayMin
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := f.connectAndForward(ctx)
		if err != nil {
			log.Printf("Connection to %s failed: %v", f.rule.Server, err)
			// Wait before reconnecting or check for cancellation
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
				log.Printf("Reconnecting to %s...", f.rule.Server)
				// Exponential backoff: double the delay on failure, up to max
				delay *= 2
				if delay > reconnectDelayMax {
					delay = reconnectDelayMax
				}
			}
		} else {
			// Connection was successful and then disconnected gracefully
			// Reset delay for next reconnection
			delay = reconnectDelayMin
			
			// Brief pause before reconnecting
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectDelayMin):
				log.Printf("Reconnecting to %s...", f.rule.Server)
			}
		}
	}
}

// connectAndForward establishes SSH connection and sets up port forwarding
func (f *Forwarder) connectAndForward(ctx context.Context) error {
	// Parse server address (user@host:port)
	user, host, port := parseServerAddress(f.rule.Server)

	// SSH client config
	// WARNING: Using InsecureIgnoreHostKey() disables host key verification,
	// making the connection vulnerable to man-in-the-middle attacks.
	// For production use, consider implementing proper host key verification
	// using ssh.FixedHostKey() or knownhosts.New() from golang.org/x/crypto/ssh/knownhosts
	sshConfig := &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         sshTimeout,
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

	f.clientLock.Lock()
	f.client = client
	f.clientLock.Unlock()

	log.Printf("Connected to %s", f.rule.Server)

	// Listen on remote server
	listener, err := client.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", f.rule.RemotePort))
	if err != nil {
		f.clientLock.Lock()
		if f.client == client {
			f.client = nil
		}
		f.clientLock.Unlock()
		client.Close()
		return fmt.Errorf("failed to listen on remote port: %w", err)
	}

	log.Printf("Remote port forwarding established: connections to remote 127.0.0.1:%d will be forwarded to local 127.0.0.1:%d",
		f.rule.RemotePort, f.rule.LocalPort)

	// Accept connections
	var listenerCloseOnce sync.Once
	closeListener := func() {
		listenerCloseOnce.Do(func() {
			listener.Close()
		})
	}
	defer closeListener()

	acceptChan := make(chan error, 1)

	go func() {
		for {
			remoteConn, err := listener.Accept()
			if err != nil {
				// Close the listener on accept error to ensure prompt cleanup
				closeListener()
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

	f.activeConns.Add(1)
	defer f.activeConns.Done()

	// Connect to local service with timeout
	localConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", f.rule.LocalPort), localDialTimeout)
	if err != nil {
		log.Printf("Failed to connect to local port %d: %v", f.rule.LocalPort, err)
		return
	}
	defer localConn.Close()

	// Start bidirectional forwarding
	errChan := make(chan error, 2)

	// Remote -> Local
	go func() {
		_, err := copyWithContext(ctx, localConn, remoteConn)
		errChan <- err
	}()

	// Local -> Remote
	go func() {
		_, err := copyWithContext(ctx, remoteConn, localConn)
		errChan <- err
	}()

	// Wait for either direction to finish or context cancellation
	select {
	case <-ctx.Done():
		// Context cancelled, close connections to terminate both goroutines
		localConn.Close()
		remoteConn.Close()

		// Wait for both copyWithContext goroutines to report completion
		<-errChan
		<-errChan
	case <-errChan:
		// One direction finished, close connections to terminate the other goroutine
		localConn.Close()
		remoteConn.Close()

		// Wait for the remaining copyWithContext goroutine to report completion
		<-errChan
	}
}

// copyWithContext copies data between connections with context awareness
func copyWithContext(ctx context.Context, dst, src net.Conn) (int64, error) {
	var written int64
	buf := make([]byte, copyBufferSize)

	// Set up a goroutine to cancel reads when context is done
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			// Set a very short deadline to interrupt any blocked Read
			src.SetReadDeadline(time.Now().Add(time.Millisecond))
		case <-done:
		}
	}()

	for {
		// Set a reasonable read deadline to allow periodic context checks without excessive overhead
		src.SetReadDeadline(time.Now().Add(readDeadlineInterval))

		nr, readErr := src.Read(buf)
		if nr > 0 {
			nw, writeErr := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if writeErr != nil {
				return written, writeErr
			}
			if nr != nw {
				return written, fmt.Errorf("short write: wrote %d bytes, expected %d", nw, nr)
			}
		}
		if readErr != nil {
			// Check if context was cancelled
			select {
			case <-ctx.Done():
				return written, ctx.Err()
			default:
			}

			// Ignore timeout errors if context is still active
			var netErr net.Error
			if errors.As(readErr, &netErr) && netErr.Timeout() {
				continue
			}

			if !errors.Is(readErr, io.EOF) {
				return written, readErr
			}
			break
		}
	}
	return written, nil
}

// parseServerAddress parses user@host:port format
// Note: This function has limitations with IPv6 addresses and special characters.
// Supported formats:
//   - host
//   - host:port
//   - user@host
//   - user@host:port
// IPv6 addresses with brackets are not supported (e.g., [::1]:22)
func parseServerAddress(addr string) (username, host string, port int) {
	originalAddr := addr // Preserve original address for error messages

	// Extract username if present (before @)
	atIndex := strings.Index(addr, "@")
	if atIndex != -1 {
		username = addr[:atIndex]
		addr = addr[atIndex+1:]
	}

	// Extract port if present (after last :)
	// Note: This will not work correctly for IPv6 addresses without brackets
	colonIndex := strings.LastIndex(addr, ":")
	if colonIndex != -1 {
		host = addr[:colonIndex]
		portStr := addr[colonIndex+1:]
		// Default to SSH port 22 in case of parsing errors or invalid values
		port = 22
		parsedPort, err := strconv.Atoi(portStr)
		if err != nil || parsedPort <= 0 || parsedPort > 65535 {
			log.Printf("invalid port %q in address %q, defaulting to 22", portStr, originalAddr)
		} else {
			port = parsedPort
		}
	} else {
		host = addr
		port = 22
	}

	if username == "" {
		// Use current system user as fallback
		if currentUser, err := user.Current(); err == nil {
			username = currentUser.Username
		} else {
			// If we can't determine the current user, return empty username
			// which will cause an error during SSH authentication
			log.Printf("Warning: could not determine current user for %s, username will be empty", originalAddr)
		}
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
		// ssh.ParsePrivateKey does not support encrypted private keys.
		// Provide a clearer error message if the key appears to be encrypted.
		if strings.Contains(err.Error(), "encrypted") || strings.Contains(err.Error(), "passphrase") {
			return nil, fmt.Errorf("failed to parse private key %s: key appears to be encrypted; only unencrypted private keys are supported: %w", expandedPath, err)
		}
		return nil, fmt.Errorf("failed to parse private key %s: %w", expandedPath, err)
	}
	return ssh.PublicKeys(signer), nil
}
