package ssh

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type directTCPIPChannelData struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

func startDirectTCPIPSSHServer(t *testing.T) (*ssh.Client, string) {
	t.Helper()

	signer := newTestSSHSigner(t)
	backend, backendAddr := startEchoBackend(t)
	serverListener := startDirectTCPIPServer(t)
	serverConnDone := serveDirectTCPIPConnection(serverListener, signer)

	client, err := ssh.Dial("tcp", serverListener.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		Timeout:         2 * time.Second,
	})
	if err != nil {
		_ = serverListener.Close()
		_ = backend.Close()
		t.Fatalf("dial SSH server: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
		_ = serverListener.Close()
		_ = backend.Close()
		<-serverConnDone
	})
	return client, backendAddr
}

func newTestSSHSigner(t *testing.T) ssh.Signer {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}
	return signer
}

func startEchoBackend(t *testing.T) (net.Listener, string) {
	t.Helper()

	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for backend: %v", err)
	}
	go serveEchoBackend(backend)
	return backend, backend.Addr().String()
}

func serveEchoBackend(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

func startDirectTCPIPServer(t *testing.T) net.Listener {
	t.Helper()

	serverListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for SSH server: %v", err)
	}
	return serverListener
}

func serveDirectTCPIPConnection(listener net.Listener, signer ssh.Signer) <-chan struct{} {
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	serverConnDone := make(chan struct{})
	go func() {
		defer close(serverConnDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		serverConn, channels, requests, handshakeErr := ssh.NewServerConn(conn, serverConfig)
		if handshakeErr != nil {
			return
		}
		go ssh.DiscardRequests(requests)
		for newChannel := range channels {
			handleDirectTCPIPChannel(newChannel)
		}
		_ = serverConn.Close()
	}()
	return serverConnDone
}

func handleDirectTCPIPChannel(newChannel ssh.NewChannel) {
	if newChannel.ChannelType() != "direct-tcpip" {
		_ = newChannel.Reject(ssh.UnknownChannelType, "only direct-tcpip is supported")
		return
	}
	var data directTCPIPChannelData
	if err := ssh.Unmarshal(newChannel.ExtraData(), &data); err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	channel, channelRequests, err := newChannel.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(channelRequests)
	backendAddr := net.JoinHostPort(data.DestAddr, strconv.FormatUint(uint64(data.DestPort), 10))
	backendConn, err := net.Dial("tcp", backendAddr)
	if err != nil {
		_ = channel.Close()
		return
	}
	go bridgeDirectTCPIPChannel(channel, backendConn)
}

func bridgeDirectTCPIPChannel(channel ssh.Channel, backendConn net.Conn) {
	defer func() { _ = channel.Close() }()
	defer func() { _ = backendConn.Close() }()
	go func() { _, _ = io.Copy(backendConn, channel) }()
	_, _ = io.Copy(channel, backendConn)
}

func openDirectTCPIP(t *testing.T, client *ssh.Client, backendAddr string) ssh.Channel {
	t.Helper()
	host, portString, err := net.SplitHostPort(backendAddr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portString, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	channel, _, err := client.OpenChannel("direct-tcpip", ssh.Marshal(&directTCPIPChannelData{
		DestAddr:   host,
		DestPort:   uint32(port),
		OriginAddr: "127.0.0.1",
		OriginPort: 0,
	}))
	if err != nil {
		t.Fatalf("open direct-tcpip channel: %v", err)
	}
	return channel
}

func TestDirectTCPIPRepeatedChannelsKeepParentAlive(t *testing.T) {
	client, backendAddr := startDirectTCPIPSSHServer(t)

	for range 10 {
		channel := openDirectTCPIP(t, client, backendAddr)
		message := []byte("ping")
		if _, err := channel.Write(message); err != nil {
			t.Fatalf("write: %v", err)
		}
		response := make([]byte, len(message))
		if _, err := io.ReadFull(channel, response); err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(response) != string(message) {
			t.Fatalf("response = %q, want %q", response, message)
		}
		_ = channel.Close()
	}
}

func TestDirectTCPIPConcurrentChannelsAreIndependent(t *testing.T) {
	client, backendAddr := startDirectTCPIPSSHServer(t)
	channels := make([]ssh.Channel, 5)
	for i := range channels {
		channels[i] = openDirectTCPIP(t, client, backendAddr)
	}

	var wg sync.WaitGroup
	for i, channel := range channels {
		wg.Go(func() {
			message := fmt.Appendf(nil, "channel-%d", i)
			if err := exchangeDirectTCPIP(channel, message); err != nil {
				t.Errorf("channel %d exchange: %v", i, err)
			}
		})
	}
	wg.Wait()

	// Closing channels in non-FIFO order exercises parent/channel ownership.
	for _, index := range []int{4, 1, 3, 0, 2} {
		_ = channels[index].Close()
	}

	channel := openDirectTCPIP(t, client, backendAddr)
	defer func() { _ = channel.Close() }()
	message := []byte("still-alive")
	if err := exchangeDirectTCPIP(channel, message); err != nil {
		t.Fatalf("parent transport unusable after channel churn: %v", err)
	}
}

func TestDirectTCPIPIdleParentAcceptsNewChannel(t *testing.T) {
	client, backendAddr := startDirectTCPIPSSHServer(t)
	time.Sleep(25 * time.Millisecond)
	channel := openDirectTCPIP(t, client, backendAddr)
	defer func() { _ = channel.Close() }()
	message := []byte("after-idle")
	if err := exchangeDirectTCPIP(channel, message); err != nil {
		t.Fatalf("exchange after idle: %v", err)
	}
}

func exchangeDirectTCPIP(channel ssh.Channel, message []byte) error {
	if _, err := channel.Write(message); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	response := make([]byte, len(message))
	if _, err := io.ReadFull(channel, response); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if string(response) != string(message) {
		return fmt.Errorf("response = %q, want %q", response, message)
	}
	return nil
}
